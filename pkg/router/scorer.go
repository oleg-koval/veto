package router

import (
	"cmp"
	"slices"
)

// scoring weights — must sum to 1.0.
// costFit is weighted highest because the admission gate already enforces kind-fit;
// the scorer's job is to order candidates cheapest-viable first.
const (
	weightKindMatch   = 0.20
	weightSuccessRate = 0.25
	weightCostFit     = 0.35
	weightRejectRate  = 0.10
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
		if byScore := cmp.Compare(b.score, a.score); byScore != 0 {
			return byScore
		}
		return cmp.Compare(a.model.Name, b.model.Name)
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

// costFit returns a cost-efficiency score in [0.05, 1.0].
// With a ceiling: scales linearly from 1.0 (free) to 0.0 (at ceiling).
// Without a ceiling: uses opus input cost as reference so cheaper models naturally rank higher.
// Local/free models score 1.0; opus scores ~0.05.
func costFit(m ModelCapabilities, task TaskSpec) float64 {
	if !costKnown(m) {
		return 0.05
	}
	if task.MaxCostUSD > 0 {
		est := estimatedCost(m, task)
		if est >= task.MaxCostUSD {
			return 0.0
		}
		return 1.0 - (est / task.MaxCostUSD)
	}
	// ponytail: ref = opus-level input cost; update if opus pricing changes
	const ref = 0.015
	if m.CostPer1kInputUSD <= 0 {
		return 1.0 // local/free model
	}
	return max(0.05, 1.0-(m.CostPer1kInputUSD/ref))
}
