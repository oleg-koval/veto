package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
) (ReviewResult, bool) {
	if len(original.SuccessCriteria) == 0 {
		return ReviewResult{}, false
	}

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
		// no review model available or routing failed — skip gracefully
		fmt.Fprintf(os.Stderr, "  (review skipped: %v)\n", err)
		return ReviewResult{}, false
	}

	result, ok := parseReviewJSON(reviewOutput)
	if !ok {
		fmt.Fprintln(os.Stderr, "  (review skipped: reviewer returned unparseable output)")
		return ReviewResult{}, false
	}
	return result, true
}
