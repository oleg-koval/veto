package router

import "slices"

// HardFilter removes models that obviously cannot handle the task.
// Pure function — zero I/O, deterministic. Callers receive rejection reasons
// through AdmissionDecision, not here; this only culls the candidate list.
func HardFilter(task TaskSpec, models []ModelCapabilities) []ModelCapabilities {
	out := make([]ModelCapabilities, 0, len(models))
	for _, m := range models {
		if !hasRequiredTools(m, task.RequiredTools) {
			continue
		}
		if task.MaxTokens > 0 && m.MaxContextTokens < task.MaxTokens {
			continue
		}
		if task.MaxCostUSD > 0 && estimatedCost(m, task) > task.MaxCostUSD {
			continue
		}
		if isWeakKind(m, task.Kind) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// hasRequiredTools returns true when the model supports all required tools.
func hasRequiredTools(m ModelCapabilities, required []string) bool {
	for _, req := range required {
		if !slices.Contains(m.SupportsTools, req) {
			return false
		}
	}
	return true
}

// estimatedCost computes a rough cost estimate assuming a fixed 1000-token exchange.
// real costs are refined by the admission gate's EstimatedCostUSD field.
func estimatedCost(m ModelCapabilities, task TaskSpec) float64 {
	inputTokens := float64(1000)
	if task.MaxTokens > 0 {
		inputTokens = float64(task.MaxTokens)
	}
	// ponytail: output ≈ 10% of input; refine with real data when available
	outputTokens := inputTokens / 10
	return (inputTokens/1000)*m.CostPer1kInputUSD + (outputTokens/1000)*m.CostPer1kOutputUSD
}

// isWeakKind returns true when the task kind is listed as a model weakness.
func isWeakKind(m ModelCapabilities, kind TaskKind) bool {
	return slices.Contains(m.Weaknesses, kind)
}

// FilterReason returns the hard-filter rejection reason for m, or "" if it passes.
// Used by Manager to emit per-model filter events without duplicating HardFilter logic.
func FilterReason(m ModelCapabilities, task TaskSpec) string {
	if !hasRequiredTools(m, task.RequiredTools) {
		return ReasonMissingTool
	}
	if task.MaxTokens > 0 && m.MaxContextTokens < task.MaxTokens {
		return ReasonContextTooLarge
	}
	if task.MaxCostUSD > 0 && estimatedCost(m, task) > task.MaxCostUSD {
		return ReasonCostCeiling
	}
	if isWeakKind(m, task.Kind) {
		return ReasonWeakKind
	}
	return ""
}
