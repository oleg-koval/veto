package router

import "strings"

// InferKind classifies an objective using the same keyword policy used by the
// CLI. Keeping this in the router package lets every client, including the
// in-process TUI control plane, construct the same TaskSpec when --kind is
// omitted.
func InferKind(objective string) TaskKind {
	s := strings.ToLower(objective)
	switch {
	case containsKindAny(s, "fix", "bug", "debug", "error", "crash", "broken", "failing"):
		return KindDebug
	case containsKindAny(s, "refactor", "clean up", "restructure", "rename", "extract method"):
		return KindRefactor
	case containsKindAny(s, "summarize", "summary", "tl;dr", "recap"):
		return KindSummarize
	case containsKindAny(s, "extract", "parse", "pull out", "scrape"):
		return KindExtract
	case containsKindAny(s, "review", "audit", "critique", "check"):
		return KindReview
	case containsKindAny(s, "plan", "design", "architect", "propose"):
		return KindPlan
	default:
		return KindCodeChange
	}
}

func containsKindAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
