package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryStore_NeutralBaseline(t *testing.T) {
	s := NewMemoryStore()
	sig := s.Signal("opus", KindCodeChange)
	assert.InDelta(t, 0.5, sig.HistoricalSuccessRate, 0.001)
	assert.InDelta(t, 0.1, sig.HistoricalRejectRate, 0.001)
	assert.InDelta(t, 0.7, sig.AvgEvalScore, 0.001)
}

func TestMemoryStore_LogDecision(t *testing.T) {
	s := NewMemoryStore()
	s.LogDecision("task-1", "haiku", AdmissionDecision{Accept: true})
	s.LogDecision("task-2", "haiku", AdmissionDecision{Accept: false})

	sig := s.Signal("haiku", KindExtract)
	// 1 of 2 accepted → reject rate = 0.5
	assert.InDelta(t, 0.5, sig.HistoricalRejectRate, 0.001)
}

func TestMemoryStore_LogResult_ScoreAggregation(t *testing.T) {
	s := NewMemoryStore()
	s.LogResult("task-1", "sonnet", 0.8, "success")
	s.LogResult("task-2", "sonnet", 0.6, "success")
	s.LogResult("task-3", "sonnet", 0.9, "failure")

	sig := s.Signal("sonnet", KindReview)
	// 2 successes out of 3 events → success rate = 2/3
	assert.InDelta(t, 2.0/3.0, sig.HistoricalSuccessRate, 0.001)
	// avg score of successes: (0.8 + 0.6) / 2 = 0.7
	assert.InDelta(t, 0.7, sig.AvgEvalScore, 0.001)
}

func TestMemoryStore_TaskKindSignalsAreIsolated(t *testing.T) {
	s := NewMemoryStore()
	s.LogDecisionForKind("task-code", "model", KindCodeChange, AdmissionDecision{Accept: false})
	s.LogDecisionForKind("task-review", "model", KindReview, AdmissionDecision{Accept: true})

	codeSig := s.Signal("model", KindCodeChange)
	reviewSig := s.Signal("model", KindReview)

	assert.InDelta(t, 1.0, codeSig.HistoricalRejectRate, 0.001)
	assert.InDelta(t, 0.0, reviewSig.HistoricalRejectRate, 0.001)
}

func TestMemoryStore_RecordExecutionMetricsKnownAndUnknown(t *testing.T) {
	s := NewMemoryStore()
	s.RecordExecution("task-1", "model", KindCodeChange, ExecutionMetrics{
		Status:       "success",
		Score:        0.8,
		ScoreKnown:   true,
		InputTokens:  100,
		OutputTokens: 40,
		TotalTokens:  140,
		UsageKnown:   true,
		CostUSD:      0.004,
		CostKnown:    true,
		LatencyMs:    120,
		LatencyKnown: true,
	})
	s.RecordExecution("task-2", "model", KindCodeChange, ExecutionMetrics{
		Status: "failure",
	})

	sig := s.Signal("model", KindCodeChange)
	assert.InDelta(t, 0.5, sig.HistoricalSuccessRate, 0.001)
	assert.InDelta(t, 0.8, sig.AvgEvalScore, 0.001)
	assert.InDelta(t, 0.004, sig.RecentCostUSD, 0.0001)
	assert.InDelta(t, 120.0, sig.RecentLatencyMs, 0.001)
	assert.InDelta(t, 100.0, sig.AvgInputTokens, 0.001)
	assert.InDelta(t, 40.0, sig.AvgOutputTokens, 0.001)
	assert.InDelta(t, 140.0, sig.AvgTotalTokens, 0.001)
	assert.True(t, sig.UsageKnown)
	assert.True(t, sig.CostKnown)
	assert.True(t, sig.LatencyKnown)
	assert.True(t, sig.EvalScoreKnown)
}

func TestMemoryStore_UnknownEvaluationScoreRemainsNeutral(t *testing.T) {
	s := NewMemoryStore()
	s.RecordExecution("task-1", "model", KindCodeChange, ExecutionMetrics{Status: "success"})

	sig := s.Signal("model", KindCodeChange)
	assert.InDelta(t, 0.7, sig.AvgEvalScore, 0.001)
	assert.False(t, sig.EvalScoreKnown)
}

func TestMemoryStore_IsolatedByModel(t *testing.T) {
	s := NewMemoryStore()
	s.LogResult("task-1", "haiku", 1.0, "success")
	s.LogResult("task-2", "opus", 0.0, "failure")

	haikuSig := s.Signal("haiku", KindExtract)
	opusSig := s.Signal("opus", KindPlan)
	assert.Greater(t, haikuSig.HistoricalSuccessRate, opusSig.HistoricalSuccessRate)
}

func TestMemoryStore_Concurrent(t *testing.T) {
	s := NewMemoryStore()
	done := make(chan struct{})
	for range 50 {
		go func() {
			s.LogDecision("t", "m", AdmissionDecision{Accept: true})
			s.LogResult("t", "m", 1.0, "success")
			s.Signal("m", KindExtract)
			done <- struct{}{}
		}()
	}
	for range 50 {
		<-done
	}
}
