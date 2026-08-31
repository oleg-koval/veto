package main

import (
	"context"

	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
)

// Keep aliases for cmd package tests and existing internal callers while the
// review data contract lives with the application use case.
type CriterionResult = application.CriterionResult
type ReviewResult = application.ReviewResult

// Compatibility wrappers keep the existing cmd package helpers available to
// in-package callers while the review policy lives in application.
func buildReviewPrompt(spec router.TaskSpec, output string) string {
	return application.BuildReviewPrompt(spec, output)
}

func parseReviewJSON(output string) (ReviewResult, bool) {
	return application.ParseReviewJSON(output)
}

func validateReviewResult(criteria []string, result ReviewResult) error {
	return application.ValidateReviewResult(criteria, result)
}

// reviewOutput runs an acceptance-criteria check on output.
//
// Returns a zero result without error when no criteria were requested.
// Review unavailability and malformed output are returned as errors.
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

	prompt := application.BuildReviewPrompt(original, output)
	reviewTaskID := taskHash(prompt, "review", "low", 0)
	render := NewRenderer(true) // quiet: suppress nested pipeline noise
	prev := mgr.OnEvent
	mgr.OnEvent = func(e router.ProgressEvent) {
		render.OnEvent(e)
		logEvent(reviewTaskID, string(router.KindReview), string(router.RiskLow), e)
	}
	defer func() { mgr.OnEvent = prev }()
	runner := newApplicationRunner(reg, mgr)
	result, err := runner.Review(ctx, application.ReviewRequest{
		Original: original, Output: output, ExecutorModel: executorModel,
		TaskID: reviewTaskID,
	})
	if err != nil {
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
