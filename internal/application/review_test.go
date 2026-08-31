package application

import (
	"context"
	"testing"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewRoutesReviewerAndSkipsExecutor(t *testing.T) {
	routerPort := &testRouter{model: router.ModelCapabilities{Name: "reviewer"}}
	runtime := &testRuntime{result: execution.Result{Output: `{"passed":true,"score":1,"criteria":[{"criterion":"tests pass","met":true,"note":"ok"}]}`}, known: true}
	runner := Runner{Router: routerPort, Runtime: testResolver{runtime: runtime}}

	result, err := runner.Review(context.Background(), ReviewRequest{
		Original: router.TaskSpec{ID: "task", Objective: "implement feature", SuccessCriteria: []string{"tests pass"}},
		Output:   "all tests pass", ExecutorModel: "executor", TaskID: "review-task",
	})

	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "review-task", routerPort.routedTask.ID)
	assert.Equal(t, []string{"executor"}, routerPort.routedTask.SkipModels)
	assert.Contains(t, runtime.prompt, "tests pass")
}

func TestReviewFailsClosedForMalformedOutput(t *testing.T) {
	runner := Runner{
		Router:  &testRouter{model: router.ModelCapabilities{Name: "reviewer"}},
		Runtime: testResolver{runtime: &testRuntime{result: execution.Result{Output: "not json"}, known: true}},
	}

	_, err := runner.Review(context.Background(), ReviewRequest{
		Original: router.TaskSpec{SuccessCriteria: []string{"tests pass"}}, Output: "output",
	})

	require.EqualError(t, err, "reviewer returned unparseable output")
}

func TestReviewSkipsWithoutCriteria(t *testing.T) {
	result, err := (Runner{}).Review(nil, ReviewRequest{Original: router.TaskSpec{Objective: "no gate"}})

	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestValidateReviewResultRejectsContradictoryAndIncompleteResponses(t *testing.T) {
	tests := []struct {
		name    string
		result  ReviewResult
		message string
	}{
		{
			name: "incomplete", result: ReviewResult{Passed: true, Score: 1},
			message: "review returned 0 criterion result; expected 1",
		},
		{
			name: "passed with unmet", result: ReviewResult{Passed: true, Score: 1, Criteria: []CriterionResult{{Criterion: "c", Met: false}}},
			message: "review returned passed=true with unmet criterion",
		},
		{
			name: "failed with all met", result: ReviewResult{Passed: false, Score: 1, Criteria: []CriterionResult{{Criterion: "c", Met: true}}},
			message: "review returned passed=false with all criteria met",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, ValidateReviewResult([]string{"c"}, tt.result), tt.message)
		})
	}
}
