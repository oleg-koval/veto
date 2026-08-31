package main

import (
	"testing"

	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseReviewJSON_Happy parses a well-formed review response.
func TestParseReviewJSON_Happy(t *testing.T) {
	raw := `{
  "passed": false,
  "score": 0.5,
  "criteria": [
    {"criterion": "uses table-driven tests", "met": true,  "note": "yes"},
    {"criterion": "has error handling",      "met": false, "note": "missing"}
  ]
}`
	result, ok := application.ParseReviewJSON(raw)
	require.True(t, ok)
	assert.False(t, result.Passed)
	assert.InDelta(t, 0.5, result.Score, 0.001)
	require.Len(t, result.Criteria, 2)
	assert.True(t, result.Criteria[0].Met)
	assert.False(t, result.Criteria[1].Met)
	assert.Equal(t, "missing", result.Criteria[1].Note)
}

// TestParseReviewJSON_WithProse handles model output with surrounding prose.
func TestParseReviewJSON_WithProse(t *testing.T) {
	raw := `Here is my review:
{"passed": true, "score": 1.0, "criteria": [{"criterion": "c1", "met": true, "note": "ok"}]}
Done.`
	result, ok := application.ParseReviewJSON(raw)
	require.True(t, ok)
	assert.True(t, result.Passed)
}

// TestParseReviewJSON_Garbage returns false on non-JSON output.
func TestParseReviewJSON_Garbage(t *testing.T) {
	_, ok := application.ParseReviewJSON("sorry I cannot review this")
	assert.False(t, ok)
}

// TestParseReviewJSON_MalformedJSON returns false on bad JSON.
func TestParseReviewJSON_MalformedJSON(t *testing.T) {
	_, ok := application.ParseReviewJSON(`{"passed": true, "score": "not-a-float"}`)
	assert.False(t, ok)
}

func TestValidateReviewResult(t *testing.T) {
	tests := []struct {
		name     string
		criteria []string
		result   ReviewResult
		wantErr  string
	}{
		{
			name:     "valid pass",
			criteria: []string{"tests pass", "docs updated"},
			result: ReviewResult{Passed: true, Score: 1, Criteria: []CriterionResult{
				{Criterion: "tests pass", Met: true},
				{Criterion: "docs updated", Met: true},
			}},
		},
		{
			name:     "missing criterion",
			criteria: []string{"tests pass", "docs updated"},
			result: ReviewResult{Passed: true, Score: 1, Criteria: []CriterionResult{
				{Criterion: "tests pass", Met: true},
			}},
			wantErr: "returned 1 criterion result; expected 2",
		},
		{
			name:     "passed contradicts unmet criterion",
			criteria: []string{"tests pass"},
			result: ReviewResult{Passed: true, Score: 1, Criteria: []CriterionResult{
				{Criterion: "tests pass", Met: false},
			}},
			wantErr: "passed=true with unmet criterion",
		},
		{
			name:     "failed contradicts all criteria met",
			criteria: []string{"tests pass"},
			result: ReviewResult{Passed: false, Score: 1, Criteria: []CriterionResult{
				{Criterion: "tests pass", Met: true},
			}},
			wantErr: "passed=false with all criteria met",
		},
		{
			name:     "criterion order mismatch",
			criteria: []string{"tests pass"},
			result: ReviewResult{Passed: false, Score: 0, Criteria: []CriterionResult{
				{Criterion: "something else", Met: false},
			}},
			wantErr: "criterion 1 does not match request",
		},
		{
			name:     "score out of range",
			criteria: []string{"tests pass"},
			result: ReviewResult{Passed: false, Score: 1.2, Criteria: []CriterionResult{
				{Criterion: "tests pass", Met: false},
			}},
			wantErr: "score must be between 0 and 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := application.ValidateReviewResult(tt.criteria, tt.result)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestBuildReviewPrompt embeds all criteria and objective in the prompt.
func TestBuildReviewPrompt(t *testing.T) {
	spec := router.TaskSpec{
		Objective:       "write a Go function",
		SuccessCriteria: []string{"has unit tests", "returns error not panic"},
	}
	prompt := application.BuildReviewPrompt(spec, "func foo() { }")
	assert.Contains(t, prompt, "write a Go function")
	assert.Contains(t, prompt, "has unit tests")
	assert.Contains(t, prompt, "returns error not panic")
	assert.Contains(t, prompt, "func foo() { }")
}

// TestReviewOutput_SkippedWhenNoCriteria verifies early-exit: no model call.
// Passes nil ctx/reg/mgr — safe because the function exits before using them.
func TestReviewOutput_SkippedWhenNoCriteria(t *testing.T) {
	spec := router.TaskSpec{Objective: "do stuff"} // no SuccessCriteria
	_, err := reviewOutput(nil, nil, nil, spec, "some output", "")
	require.NoError(t, err)
}

// The application review tests cover skip-self routing with a stub runner.
func TestReviewSkipModels_InReviewSpec(t *testing.T) {
	t.Log("SkipModels routing is covered by internal/application review tests")
}
