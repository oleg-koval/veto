package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func neutralSignal() RoutingSignal {
	return RoutingSignal{HistoricalSuccessRate: 0.5, HistoricalRejectRate: 0.1, AvgEvalScore: 0.7}
}

func TestScore_Range(t *testing.T) {
	m := ModelCapabilities{
		Strengths:  []TaskKind{KindCodeChange},
		Weaknesses: []TaskKind{KindDebug},
	}
	task := TaskSpec{Kind: KindCodeChange}
	s := Score(task, m, neutralSignal())
	assert.GreaterOrEqual(t, s, 0.0)
	assert.LessOrEqual(t, s, 1.0)
}

func TestScore_StrengthBeatsWeakness(t *testing.T) {
	m := ModelCapabilities{
		Strengths:  []TaskKind{KindCodeChange},
		Weaknesses: []TaskKind{KindDebug},
	}
	sig := neutralSignal()
	sStrength := Score(TaskSpec{Kind: KindCodeChange}, m, sig)
	sWeakness := Score(TaskSpec{Kind: KindDebug}, m, sig)
	assert.Greater(t, sStrength, sWeakness)
}

func TestKindMatch(t *testing.T) {
	m := ModelCapabilities{
		Strengths:  []TaskKind{KindCodeChange},
		Weaknesses: []TaskKind{KindDebug},
	}
	assert.InDelta(t, 1.0, kindMatch(m, KindCodeChange), 0.001)
	assert.InDelta(t, 0.0, kindMatch(m, KindDebug), 0.001)
	assert.InDelta(t, 0.5, kindMatch(m, KindPlan), 0.001)
}

func TestCostFit(t *testing.T) {
	// $0.01/1k input — about 2/3 of opus cost ($0.015 ref)
	m := ModelCapabilities{CostPer1kInputUSD: 0.01, CostPer1kOutputUSD: 0.01}

	// no ceiling → cost-efficiency score relative to opus reference (1 - 0.01/0.015 ≈ 0.333)
	assert.InDelta(t, 0.333, costFit(m, TaskSpec{}), 0.001)

	// free model → 1.0
	free := ModelCapabilities{CostPer1kInputUSD: 0}
	assert.InDelta(t, 1.0, costFit(free, TaskSpec{}), 0.001)

	// well under ceiling → high score
	score := costFit(m, TaskSpec{MaxTokens: 1000, MaxCostUSD: 1.0})
	assert.Greater(t, score, 0.9)

	// at or above ceiling → zero (1000 input + 100 output * 0.01/1k = 0.011)
	score2 := costFit(m, TaskSpec{MaxTokens: 1000, MaxCostUSD: 0.011})
	assert.InDelta(t, 0.0, score2, 0.001)
}

func TestRankCandidates_OrderedByScore(t *testing.T) {
	reg := NewRegistry()
	// sonnet strengths: code-change, review, refactor — should rank above haiku for code task
	task := TaskSpec{Kind: KindCodeChange}
	ranked := RankCandidates(task, reg.All(), reg)
	assert.NotEmpty(t, ranked)
	// first must not be haiku (haiku is weak on code tasks because it's strong on extract/summarize only)
	assert.NotEqual(t, "haiku", ranked[0].Name)
}

func TestRankCandidates_EmptyAfterFilter(t *testing.T) {
	reg := NewRegistry()
	// require a tool nobody has
	task := TaskSpec{RequiredTools: []string{"quantum-computer"}}
	ranked := RankCandidates(task, reg.All(), reg)
	assert.Nil(t, ranked)
}

func TestRankCandidatesBreaksEqualScoresByStableModelName(t *testing.T) {
	models := []ModelCapabilities{
		{Name: "z", Tier: tierSmall, MaxContextTokens: 1000},
		{Name: "a", Tier: tierSmall, MaxContextTokens: 1000},
	}
	ranked := RankCandidates(TaskSpec{Kind: KindSummarize}, models, NewRegistryFromModels(models))
	require.Len(t, ranked, 2)
	assert.Equal(t, "a", ranked[0].Name)
	assert.Equal(t, "z", ranked[1].Name)
}
