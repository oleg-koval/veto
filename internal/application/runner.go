// Package application contains Veto's delivery-neutral task use cases.
//
// It coordinates routing and full-task execution through the contracts owned
// by pkg/router and pkg/execution. CLI rendering, logging, and process
// termination stay outside this package.
package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/router"
)

// Router is the application-facing routing port. The concrete manager is
// supplied by the composition root.
type Router interface {
	Route(context.Context, router.TaskSpec) (router.ModelCapabilities, router.AdmissionDecision, error)
	RecordExecution(router.TaskSpec, string, router.ExecutionMetrics)
}

// RuntimeResolver is the application-facing execution port. It deliberately
// returns the stable runtime contract rather than a provider implementation.
type RuntimeResolver interface {
	RuntimeFor(string) (execution.RuntimeAdapter, bool)
}

// Streamer is an optional compatibility port implemented by legacy CLI
// runtimes. It is kept here so the use case does not depend on concrete
// executor packages.
type Streamer interface {
	Stream(context.Context, string, io.Writer) error
}

// ExecutionEventKind identifies lifecycle events emitted by Runner.
type ExecutionEventKind string

const (
	ExecutionStarted   ExecutionEventKind = "started"
	ExecutionCompleted ExecutionEventKind = "completed"
	ExecutionFailed    ExecutionEventKind = "failed"
)

// ExecutionEvent is a delivery-neutral event for logging and metrics
// adapters. RuntimeEvent is intentionally allowlisted by pkg/execution.
type ExecutionEvent struct {
	Kind         ExecutionEventKind
	TaskID       string
	Model        router.ModelCapabilities
	Metrics      router.ExecutionMetrics
	Detail       string
	RuntimeEvent *execution.RuntimeEvent
}

// Hooks lets an outer adapter render or persist application events without
// making the use case depend on terminal or ledger packages.
type Hooks struct {
	OnExecutionEvent func(ExecutionEvent)
	OnRuntimeEvent   func(string, router.ModelCapabilities, execution.RuntimeEvent)
}

// Runner implements the route-and-execute use case.
type Runner struct {
	Router  Router
	Runtime RuntimeResolver
	Hooks   Hooks
}

// Request is the plain input model crossing the application boundary.
type Request struct {
	Task    router.TaskSpec
	Skills  []string
	Options execution.ExecutionOptions
	Writer  io.Writer
}

// Response is the plain output model returned by the application boundary.
type Response struct {
	Model         router.ModelCapabilities
	Decision      router.AdmissionDecision
	Output        string
	Result        execution.Result
	OutputWritten bool
	Streamed      bool
}

// Execute routes a task, then executes it on the selected runtime. It records
// execution telemetry through the router's store and reports lifecycle events
// through Hooks. Errors are returned for the delivery layer to present.
func (r Runner) Execute(ctx context.Context, request Request) (Response, error) {
	if r.Router == nil {
		return Response{}, errors.New("application: router is nil")
	}
	if r.Runtime == nil {
		return Response{}, errors.New("application: runtime resolver is nil")
	}

	model, decision, err := r.Router.Route(ctx, request.Task)
	if err != nil {
		return Response{}, err
	}
	runtime, ok := r.Runtime.RuntimeFor(model.Name)
	if !ok || runtime == nil {
		return Response{Model: model, Decision: decision}, fmt.Errorf("no executor for model %q", model.Name)
	}

	prompt := BuildExecutionPrompt(request.Task.Objective, request.Skills)
	if IsTextOnlyRuntime(runtime) {
		prompt += textOnlyInstruction
	}

	started := time.Now()
	r.emit(ExecutionEvent{Kind: ExecutionStarted, TaskID: request.Task.ID, Model: model,
		Metrics: router.ExecutionMetrics{Status: "started"}})

	var result execution.Result
	var output string
	var streamed bool
	switch {
	case isEventExecutor(runtime):
		var buffer strings.Builder
		writer := io.Writer(&buffer)
		if request.Writer != nil {
			writer = io.MultiWriter(request.Writer, &buffer)
		}
		result = runtime.(execution.EventTaskExecutor).ExecuteWithEvents(ctx, prompt, request.Options, writer,
			func(event execution.RuntimeEvent) {
				if r.Hooks.OnRuntimeEvent != nil {
					r.Hooks.OnRuntimeEvent(request.Task.ID, model, event)
				}
			})
		output = result.Output
		if output == "" {
			output = buffer.String()
		}
		streamed = request.Writer != nil
	case request.Writer != nil && isStreamer(runtime):
		var buffer strings.Builder
		writer := io.MultiWriter(request.Writer, &buffer)
		streamed = true
		if err := runtime.(Streamer).Stream(ctx, prompt, writer); err != nil {
			result.Error = err
		}
		output = buffer.String()
	case isTaskExecutor(runtime):
		result = runtime.(execution.TaskExecutor).Execute(ctx, prompt, request.Options)
		output = result.Output
	default:
		return Response{Model: model, Decision: decision}, fmt.Errorf("executor for %q does not support task execution", model.Name)
	}

	if err := ValidateExecutionResult(result); err != nil {
		status := ExecutionStatus(ctx, "error")
		if result.Truncated {
			status = "truncated"
		}
		metrics := ExecutionMetrics(model, result, time.Since(started), status)
		r.Router.RecordExecution(request.Task, model.Name, metrics)
		r.emit(ExecutionEvent{Kind: ExecutionFailed, TaskID: request.Task.ID, Model: model,
			Metrics: metrics, Detail: err.Error()})
		return Response{Model: model, Decision: decision, Output: output, Result: result, Streamed: streamed}, err
	}

	metrics := ExecutionMetrics(model, result, time.Since(started), "success")
	r.Router.RecordExecution(request.Task, model.Name, metrics)
	r.emit(ExecutionEvent{Kind: ExecutionCompleted, TaskID: request.Task.ID, Model: model, Metrics: metrics})
	return Response{Model: model, Decision: decision, Output: output, Result: result,
		OutputWritten: streamed, Streamed: streamed}, nil
}

func (r Runner) emit(event ExecutionEvent) {
	if r.Hooks.OnExecutionEvent != nil {
		r.Hooks.OnExecutionEvent(event)
	}
}

func isEventExecutor(runtime execution.RuntimeAdapter) bool {
	_, ok := runtime.(execution.EventTaskExecutor)
	return ok
}

func isStreamer(runtime execution.RuntimeAdapter) bool {
	_, ok := runtime.(Streamer)
	return ok
}

func isTaskExecutor(runtime execution.RuntimeAdapter) bool {
	_, ok := runtime.(execution.TaskExecutor)
	return ok
}

const textOnlyInstruction = "\n\n---\nOutput the requested content directly. No explanation, no description of what you will do, no markdown prose. If the task is to create a file, output the file contents only."

// BuildExecutionPrompt adds approved skill snippets and the live PR thread
// workflow only for objectives that explicitly request review-comment work.
func BuildExecutionPrompt(objective string, skills []string) string {
	prompt := objective
	if len(skills) > 0 {
		prompt = "## Relevant skills\n\n" + strings.Join(skills, "\n\n") + "\n\n## Task\n\n" + objective
	}
	if !requiresPullRequestThreadWorkflow(objective) {
		return prompt
	}
	return prompt + `

## Required live pull-request workflow

- Inspect the live PR's inline review threads with GitHub GraphQL reviewThreads(first:100). Follow pageInfo.hasNextPage with endCursor until all pages have been inspected; do not infer that there are no findings from gh pr view --comments, reviews, check summaries, or template text.
- Select unresolved threads from the reviewer named in the task, inspect the referenced code, and address every applicable finding.
- Run focused verification for each change, then reply to and resolve every requested review thread.
- After verification, commit and push the changes to the PR head branch when the task requests it.
- Re-query the live PR before finishing. Do not report completion unless there are zero unresolved matching threads and the requested remote update is present. If access or verification fails, report the task as incomplete instead of claiming success.`
}

func requiresPullRequestThreadWorkflow(objective string) bool {
	prTarget, reviewTarget, mutation := pullRequestMutationSignals(objective)
	return prTarget && reviewTarget && mutation
}

func pullRequestMutationSignals(objective string) (prTarget, reviewTarget, mutation bool) {
	s := strings.ToLower(objective)
	prTarget = containsAny(s, "pull request", "/pull/", "this pr", "the pr") || containsWord(s, "pr")
	reviewTarget = containsAny(s, "review comment", "review thread", "codex comment", "coderabbit comment", "comments in this pr", "comments on this pr")
	mutation = containsAny(s, "fix", "resolve", "address", "implement", "modify", "edit", "update", "change", "refactor", "push")
	return
}

func containsWord(s, word string) bool {
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	}) {
		if field == word {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// IsTextOnlyRuntime reports whether the runtime can invoke any effective tool.
func IsTextOnlyRuntime(runtime execution.RuntimeAdapter) bool {
	if status, ok := runtime.(execution.ToolCapabilityStatus); ok && !status.EffectiveToolsKnown() {
		return false
	}
	provider, ok := runtime.(execution.ToolProvider)
	return !ok || len(provider.EffectiveTools()) == 0
}

// ValidateExecutionResult rejects provider errors and incomplete output.
func ValidateExecutionResult(result execution.Result) error {
	if result.Error != nil {
		return result.Error
	}
	if result.Truncated {
		reason := result.FinishReason
		if reason == "" {
			reason = "provider output limit"
		}
		return fmt.Errorf("execution output truncated (%s); increase --max-output-tokens", reason)
	}
	return nil
}

// ExecutionStatus maps context termination to stable telemetry status.
func ExecutionStatus(ctx context.Context, fallback string) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	default:
		return fallback
	}
}

// ExecutionMetrics maps a runtime result to router telemetry, retaining
// unknown usage and cost as unknown.
func ExecutionMetrics(model router.ModelCapabilities, result execution.Result, elapsed time.Duration, status string) router.ExecutionMetrics {
	metrics := router.ExecutionMetrics{
		Status: status, LatencyMs: elapsed.Milliseconds(), LatencyKnown: true,
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
		TotalTokens: result.Usage.TotalTokens, UsageKnown: result.Usage.Known,
	}
	if result.CostKnown {
		metrics.CostUSD = result.CostUSD
		metrics.CostKnown = true
	} else if result.Usage.Known && !model.CostPer1kInputUnknown && !model.CostPer1kOutputUnknown {
		metrics.CostUSD = float64(result.Usage.InputTokens)/1000*model.CostPer1kInputUSD +
			float64(result.Usage.OutputTokens)/1000*model.CostPer1kOutputUSD
		metrics.CostKnown = true
	}
	return metrics
}
