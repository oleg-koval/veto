package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
)

// CriterionResult is the per-criterion verdict from the reviewer.
type CriterionResult struct {
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Note      string `json:"note"`
}

// ReviewResult is the structured output from a review pass.
type ReviewResult struct {
	Passed   bool              `json:"passed"`
	Score    float64           `json:"score"`
	Criteria []CriterionResult `json:"criteria"`
}

// buildReviewPrompt constructs the prompt sent to the reviewer model.
// The model must reply with JSON only — no prose.
func buildReviewPrompt(spec router.TaskSpec, output string) string {
	criteria := strings.Join(spec.SuccessCriteria, "\n")
	return fmt.Sprintf(`You are a QA reviewer. Evaluate whether the output below meets all acceptance criteria.

TASK OBJECTIVE:
%s

ACCEPTANCE CRITERIA:
%s

OUTPUT TO REVIEW:
%s

Respond with ONLY valid JSON — no prose, no markdown.
The JSON must match this exact schema:
{
  "passed": <bool — true only if ALL criteria are met>,
  "score":  <float 0.0-1.0 — fraction of criteria met>,
  "criteria": [
    { "criterion": <string>, "met": <bool>, "note": <short explanation> }
  ]
}

Include one entry per acceptance criterion in the same order.
Respond with JSON only. Nothing before or after the JSON object.`,
		spec.Objective, criteria, output)
}

// parseReviewJSON extracts a ReviewResult from model output.
// Mirrors parseAdmissionJSON: finds first '{' … last '}'.
func parseReviewJSON(output string) (ReviewResult, bool) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end <= start {
		return ReviewResult{}, false
	}
	var r ReviewResult
	if err := json.Unmarshal([]byte(output[start:end+1]), &r); err != nil {
		return ReviewResult{}, false
	}
	return r, true
}

// validateReviewResult rejects incomplete or internally inconsistent reviewer
// output. A requested quality gate must not pass merely because the model set
// the top-level passed field to true.
func validateReviewResult(criteria []string, result ReviewResult) error {
	if result.Score < 0 || result.Score > 1 {
		return errors.New("review score must be between 0 and 1")
	}
	if len(result.Criteria) != len(criteria) {
		return fmt.Errorf("review returned %d criterion result; expected %d", len(result.Criteria), len(criteria))
	}
	allMet := true
	for i, expected := range criteria {
		got := result.Criteria[i]
		if strings.TrimSpace(got.Criterion) != strings.TrimSpace(expected) {
			return fmt.Errorf("review criterion %d does not match request", i+1)
		}
		allMet = allMet && got.Met
	}
	if result.Passed && !allMet {
		return errors.New("review returned passed=true with unmet criterion")
	}
	if !result.Passed && allMet {
		return errors.New("review returned passed=false with all criteria met")
	}
	return nil
}

// reviewOutput runs an acceptance-criteria check on output.
//
// Returns (result, true) when a review was performed.
// Returns (_, false) when skipped: no criteria, no review-capable model, or routing error.
//
// executorModel is excluded from the review routing (SkipModels) to avoid self-grading bias.
// Pass "" to skip exclusion (e.g. in the final integrator pass where the executor isn't one model).
func reviewOutput(
	ctx context.Context,
	reg *providerRegistry,
	mgr *router.Manager,
	original router.TaskSpec,
	output string,
	executorModel string,
) (ReviewResult, error) {
	if len(original.SuccessCriteria) == 0 {
		return ReviewResult{}, nil
	}
	logLifecycle(original.ID, ledger.EventReviewStarted, "started", "")

	prompt := buildReviewPrompt(original, output)
	skip := []string{}
	if executorModel != "" {
		skip = []string{executorModel}
	}
	reviewSpec := router.TaskSpec{
		ID:         taskHash(prompt, "review", "low", 0),
		Kind:       router.KindReview,
		Objective:  prompt,
		Risk:       router.RiskLow,
		SkipModels: skip,
	}

	render := NewRenderer(true) // quiet: suppress nested pipeline noise
	_, reviewOutput, err := routeAndCapture(ctx, reg, mgr, render, reviewSpec, nil)
	if err != nil {
		logLifecycle(original.ID, ledger.EventReviewError, "error", err.Error())
		return ReviewResult{}, fmt.Errorf("review unavailable: %w", err)
	}

	result, ok := parseReviewJSON(reviewOutput)
	if !ok {
		logLifecycle(original.ID, ledger.EventReviewError, "error", "reviewer returned unparseable output")
		return ReviewResult{}, errors.New("reviewer returned unparseable output")
	}
	if err := validateReviewResult(original.SuccessCriteria, result); err != nil {
		logLifecycle(original.ID, ledger.EventReviewError, "error", err.Error())
		return ReviewResult{}, err
	}
	status := "failed"
	if result.Passed {
		status = "passed"
	}
	logLifecycle(original.ID, ledger.EventReviewCompleted, status, "")
	return result, nil
}
