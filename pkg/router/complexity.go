package router

import "strings"

// InferComplexity estimates task complexity from objective text and kind.
// The score is keyword-driven: specific technical terms accumulate weight,
// simplicity signals subtract, and the task kind provides a baseline bump.
//
// Thresholds: score >= 4 → complex, >= 1 → moderate, else simple.
func InferComplexity(objective string, kind TaskKind) Complexity {
	s := strings.ToLower(objective)
	score := 0

	for _, kw := range highComplexKeywords {
		if strings.Contains(s, kw) {
			score += 3
		}
	}
	for _, kw := range medComplexKeywords {
		if strings.Contains(s, kw) {
			score += 2
		}
	}
	for _, kw := range lowComplexKeywords {
		if strings.Contains(s, kw) {
			score++
		}
	}
	for _, kw := range simpleKeywords {
		if strings.Contains(s, kw) {
			score -= 2
		}
	}

	switch kind {
	case KindPlan:
		score += 2
	case KindDebug:
		score++
	case KindExtract, KindSummarize:
		score -= 2
	}

	switch {
	case score >= 4:
		return ComplexityComplex
	case score >= 1:
		return ComplexityModerate
	default:
		return ComplexitySimple
	}
}

// tierMeetsComplexity returns false when a model's tier is too low for the task.
// The mapping is strict: complex tasks require large-tier models, moderate tasks
// require at least mid-tier, so cheap small models cannot self-admit into tasks
// that are structurally beyond them.
func tierMeetsComplexity(tier string, complexity Complexity) bool {
	switch complexity {
	case ComplexityComplex:
		return tier == tierLarge
	case ComplexityModerate:
		return tier == tierMid || tier == tierLarge
	default: // simple or unset
		return true
	}
}

// highComplexKeywords are highly specific terms that almost always signal a
// complex, large-model task (e.g. distributed systems, event-driven architectures).
var highComplexKeywords = []string{
	"cqrs", "event sourcing", "event-driven", "microservice", "microservices",
	"distributed system", "distributed architecture", "multi-tenant",
}

// medComplexKeywords are technical terms that likely indicate complex work but
// can also appear in simpler tasks — they raise the score without guaranteeing complex.
var medComplexKeywords = []string{
	"architecture", "infrastructure", "distributed", "orchestrat",
	"production-grade", "production grade", "system design", "from scratch",
	"full system", "scalable", "enterprise",
}

// lowComplexKeywords add mild weight — common in moderate tasks.
var lowComplexKeywords = []string{
	"e2e", "end-to-end", "end to end", "infra", "pipeline",
	"service", "implement", "deploy", "integrate",
	"all codex comments", "all review comments", "all review threads",
}

// simpleKeywords are signals that the task is straightforward.
var simpleKeywords = []string{
	"simple", "basic", "quick", "hello world",
}
