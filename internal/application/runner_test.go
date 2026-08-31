package application

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testRouter struct {
	model        router.ModelCapabilities
	decision     router.AdmissionDecision
	routeErr     error
	routedTask   router.TaskSpec
	recorded     []router.ExecutionMetrics
	recordedTask router.TaskSpec
}

func (r *testRouter) Route(_ context.Context, task router.TaskSpec) (router.ModelCapabilities, router.AdmissionDecision, error) {
	r.routedTask = task
	return r.model, r.decision, r.routeErr
}

func (r *testRouter) RecordExecution(task router.TaskSpec, _ string, metrics router.ExecutionMetrics) {
	r.recordedTask = task
	r.recorded = append(r.recorded, metrics)
}

type testResolver struct{ runtime execution.RuntimeAdapter }

func (r testResolver) RuntimeFor(string) (execution.RuntimeAdapter, bool) {
	return r.runtime, r.runtime != nil
}

type testRuntime struct {
	result  execution.Result
	prompt  string
	options execution.ExecutionOptions
	tools   []string
	known   bool
}

func (r *testRuntime) Run(context.Context, string) execution.Result { return execution.Result{} }

func (r *testRuntime) Execute(_ context.Context, prompt string, options execution.ExecutionOptions) execution.Result {
	r.prompt, r.options = prompt, options
	return r.result
}

func (r *testRuntime) EffectiveTools() []string  { return r.tools }
func (r *testRuntime) EffectiveToolsKnown() bool { return r.known }
func (r *testRuntime) RuntimeID() string         { return "test" }

func TestRunnerExecuteRoutesAndRecordsTelemetry(t *testing.T) {
	routerPort := &testRouter{
		model:    router.ModelCapabilities{Name: "model", Runtime: "test", CostPer1kInputUSD: 0.01, CostPer1kOutputUSD: 0.02},
		decision: router.AdmissionDecision{Accept: true, Confidence: 0.9},
	}
	runtime := &testRuntime{result: execution.Result{
		Output: "done", Usage: execution.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Known: true},
	}, known: true}
	var events []ExecutionEvent
	runner := Runner{
		Router: routerPort, Runtime: testResolver{runtime: runtime},
		Hooks: Hooks{OnExecutionEvent: func(event ExecutionEvent) { events = append(events, event) }},
	}

	response, err := runner.Execute(context.Background(), Request{
		Task:   router.TaskSpec{ID: "task-1", Objective: "summarize", Kind: router.KindSummarize},
		Skills: []string{"follow the style"}, Options: execution.ExecutionOptions{MaxOutputTokens: 123},
	})

	require.NoError(t, err)
	assert.Equal(t, "done", response.Output)
	assert.Equal(t, "test", response.Model.Runtime)
	assert.Equal(t, 123, runtime.options.MaxOutputTokens)
	assert.Contains(t, runtime.prompt, "## Relevant skills")
	assert.Contains(t, runtime.prompt, "Output the requested content directly")
	require.Len(t, routerPort.recorded, 1)
	assert.True(t, routerPort.recorded[0].CostKnown)
	assert.Equal(t, "success", routerPort.recorded[0].Status)
	assert.Equal(t, []ExecutionEventKind{ExecutionStarted, ExecutionCompleted}, eventKinds(events))
}

func TestRunnerExecutePreservesToolRuntimePrompt(t *testing.T) {
	runtime := &testRuntime{result: execution.Result{Output: "changed"}, tools: []string{"write"}, known: true}
	runner := Runner{Router: &testRouter{model: router.ModelCapabilities{Name: "agent"}}, Runtime: testResolver{runtime: runtime}}

	_, err := runner.Execute(context.Background(), Request{Task: router.TaskSpec{Objective: "edit file"}})

	require.NoError(t, err)
	assert.NotContains(t, runtime.prompt, "Output the requested content directly")
}

func TestRunnerExecuteTruncationIsFailureAndRecordsStatus(t *testing.T) {
	routerPort := &testRouter{model: router.ModelCapabilities{Name: "model"}}
	runtime := &testRuntime{result: execution.Result{Output: "partial", Truncated: true, FinishReason: "length"}, known: true}
	var failed ExecutionEvent
	runner := Runner{
		Router: routerPort, Runtime: testResolver{runtime: runtime},
		Hooks: Hooks{OnExecutionEvent: func(event ExecutionEvent) {
			if event.Kind == ExecutionFailed {
				failed = event
			}
		}},
	}

	response, err := runner.Execute(context.Background(), Request{Task: router.TaskSpec{ID: "task"}})

	require.Error(t, err)
	assert.Equal(t, "partial", response.Output)
	assert.ErrorContains(t, err, "increase --max-output-tokens")
	require.Len(t, routerPort.recorded, 1)
	assert.Equal(t, "truncated", routerPort.recorded[0].Status)
	assert.Equal(t, ExecutionFailed, failed.Kind)
	assert.Contains(t, failed.Detail, "length")
}

func TestRunnerExecutePreservesRuntimeError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	routerPort := &testRouter{model: router.ModelCapabilities{Name: "model"}}
	runner := Runner{Router: routerPort, Runtime: testResolver{runtime: &testRuntime{result: execution.Result{Error: providerErr}, known: true}}}

	_, err := runner.Execute(context.Background(), Request{Task: router.TaskSpec{ID: "task"}})

	assert.ErrorIs(t, err, providerErr)
	assert.Equal(t, "error", routerPort.recorded[0].Status)
}

func TestBuildExecutionPromptAddsPRWorkflowOnlyWhenRequested(t *testing.T) {
	plain := BuildExecutionPrompt("summarize the release", nil)
	assert.Equal(t, "summarize the release", plain)
	agentic := BuildExecutionPrompt("resolve CodeRabbit comments on PR #123 and push changes", nil)
	assert.Contains(t, agentic, "reviewThreads(first:100)")
	assert.Contains(t, agentic, "zero unresolved matching threads")
}

func TestRunnerEventRuntimeForwardsEventsAndOutput(t *testing.T) {
	runtime := &eventRuntime{testRuntime: testRuntime{known: true}}
	var gotEvent execution.RuntimeEvent
	var output strings.Builder
	runner := Runner{
		Router: &testRouter{model: router.ModelCapabilities{Name: "agent"}}, Runtime: testResolver{runtime: runtime},
		Hooks: Hooks{OnRuntimeEvent: func(_ string, _ router.ModelCapabilities, event execution.RuntimeEvent) { gotEvent = event }},
	}
	response, err := runner.Execute(context.Background(), Request{Task: router.TaskSpec{ID: "task"}, Writer: &output})

	require.NoError(t, err)
	assert.Equal(t, "streamed", response.Output)
	assert.Equal(t, "streamed", output.String())
	assert.Equal(t, execution.RuntimeToolCompleted, gotEvent.Kind)
}

type eventRuntime struct{ testRuntime }

func (r *eventRuntime) ExecuteWithEvents(_ context.Context, _ string, _ execution.ExecutionOptions, w io.Writer, onEvent func(execution.RuntimeEvent)) execution.Result {
	_, _ = io.WriteString(w, "streamed")
	onEvent(execution.RuntimeEvent{Kind: execution.RuntimeToolCompleted, Name: "write", Status: "ok"})
	return execution.Result{Output: "streamed"}
}

func eventKinds(events []ExecutionEvent) []ExecutionEventKind {
	kinds := make([]ExecutionEventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}
