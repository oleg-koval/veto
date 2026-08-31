package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/oleg-koval/veto/pkg/router"
)

// CriterionResult is the per-criterion verdict from the reviewer.
type CriterionResult struct {
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Note      string `json:"note"`
}

// ReviewResult is the structured output from an acceptance review.
type ReviewResult struct {
	Passed   bool              `json:"passed"`
	Score    float64           `json:"score"`
	Criteria []CriterionResult `json:"criteria"`
}

// ReviewRequest is the plain input model for the acceptance-review use case.
type ReviewRequest struct {
	Original      router.TaskSpec
	Output        string
	ExecutorModel string
	// TaskID preserves the caller's review task identity for telemetry. When
	// empty, a stable ID is derived from the review prompt.
	TaskID string
}

// Review routes a reviewer distinct from the executor and validates its
// complete, internally consistent response. Review failures are returned so
// delivery adapters can fail closed.
func (r Runner) Review(ctx context.Context, request ReviewRequest) (ReviewResult, error) {
	if len(request.Original.SuccessCriteria) == 0 {
		return ReviewResult{}, nil
	}
	prompt := BuildReviewPrompt(request.Original, request.Output)
	skip := []string(nil)
	if request.ExecutorModel != "" {
		skip = []string{request.ExecutorModel}
	}
	taskID := request.TaskID
	if taskID == "" {
		taskID = reviewTaskID(prompt)
	}
	response, err := r.Execute(ctx, Request{Task: router.TaskSpec{
		ID: taskID, Kind: router.KindReview, Objective: prompt,
		Risk: router.RiskLow, SkipModels: skip,
	}})
	if err != nil {
		return ReviewResult{}, fmt.Errorf("review unavailable: %w", err)
	}
	result, ok := ParseReviewJSON(response.Output)
	if !ok {
		return ReviewResult{}, errors.New("reviewer returned unparseable output")
	}
	if err := ValidateReviewResult(request.Original.SuccessCriteria, result); err != nil {
		return ReviewResult{}, err
	}
	return result, nil
}

func reviewTaskID(prompt string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|review|low|%.6f", prompt, 0.0)))
	return hex.EncodeToString(hash[:8])
}

// BuildReviewPrompt constructs the JSON-only prompt sent to the reviewer.
func BuildReviewPrompt(spec router.TaskSpec, output string) string {
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

// ParseReviewJSON extracts a ReviewResult from optional surrounding prose.
func ParseReviewJSON(output string) (ReviewResult, bool) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end <= start {
		return ReviewResult{}, false
	}
	var result ReviewResult
	if err := json.Unmarshal([]byte(output[start:end+1]), &result); err != nil {
		return ReviewResult{}, false
	}
	return result, true
}

// ValidateReviewResult rejects incomplete or internally inconsistent output.
func ValidateReviewResult(criteria []string, result ReviewResult) error {
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
