package router

import (
	"cmp"
	"slices"
)

// scoring weights — must sum to 1.0.
const (
	weightKindMatch   = 0.30
	weightSuccessRate = 0.25
	weightCostFit     = 0.20
	weightRejectRate  = 0.15
	weightEvalScore   = 0.10
)

// Score computes a routing score for a model against a task and its historical signal.
// Higher score = better candidate. Range is 0.0–1.0.
func Score(task TaskSpec, model ModelCapabilities, signal RoutingSignal) float64 {
	kindScore := kindMatch(model, task.Kind)
	costScore := costFit(model, task)
	// reject rate is inverted: lower reject rate = higher score.
	rejectScore := 1.0 - signal.HistoricalRejectRate

	return kindScore*weightKindMatch +
		signal.HistoricalSuccessRate*weightSuccessRate +
		costScore*weightCostFit +
		rejectScore*weightRejectRate +
		signal.AvgEvalScore*weightEvalScore
}

// SignalSource supplies historical routing signal for ranking.
// Both *Registry (neutral baseline) and the stores satisfy it, so ranking can
// be driven by real accumulated history instead of a static stub.
type SignalSource interface {
	Signal(modelName string, kind TaskKind) RoutingSignal
}

// RankCandidates hard-filters models, scores the survivors, and returns them
// sorted from highest to lowest score.
func RankCandidates(task TaskSpec, models []ModelCapabilities, signals SignalSource) []ModelCapabilities {
	candidates := HardFilter(task, models)
	if len(candidates) == 0 {
		return nil
	}

	type scored struct {
		model ModelCapabilities
		score float64
	}
	ranked := make([]scored, len(candidates))
	for i, m := range candidates {
		sig := signals.Signal(m.Name, task.Kind)
		ranked[i] = scored{model: m, score: Score(task, m, sig)}
	}

	slices.SortFunc(ranked, func(a, b scored) int {
		return cmp.Compare(b.score, a.score)
	})

	out := make([]ModelCapabilities, len(ranked))
	for i, s := range ranked {
		out[i] = s.model
	}
	return out
}

// kindMatch returns 1.0 if the kind is a model strength, 0.5 if neutral, 0.0 if weak.
func kindMatch(m ModelCapabilities, kind TaskKind) float64 {
	if slices.Contains(m.Strengths, kind) {
		return 1.0
	}
	if slices.Contains(m.Weaknesses, kind) {
		return 0.0
	}
	return 0.5
}

// costFit returns 1.0 when cost is well within the ceiling, scaling down as cost approaches it.
// when no ceiling is set, returns 0.5 (neutral).
func costFit(m ModelCapabilities, task TaskSpec) float64 {
	if task.MaxCostUSD <= 0 {
		return 0.5
	}
	est := estimatedCost(m, task)
	if est >= task.MaxCostUSD {
		return 0.0
	}
	ratio := est / task.MaxCostUSD
	return 1.0 - ratio
}
