package router

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestFileStore_TaskKindMetricsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewFileStore(path)
	s.RecordExecution("task-1", "model", KindPlan, ExecutionMetrics{
		Status:       "success",
		Score:        0.9,
		ScoreKnown:   true,
		TotalTokens:  300,
		UsageKnown:   true,
		CostUSD:      0.006,
		CostKnown:    true,
		LatencyMs:    250,
		LatencyKnown: true,
	})
	require.NoError(t, s.Save())

	reloaded := NewFileStore(path)
	sig := reloaded.Signal("model", KindPlan)
	assert.InDelta(t, 0.9, sig.AvgEvalScore, 0.001)
	assert.InDelta(t, 300.0, sig.AvgTotalTokens, 0.001)
	assert.InDelta(t, 0.006, sig.RecentCostUSD, 0.0001)
	assert.InDelta(t, 250.0, sig.RecentLatencyMs, 0.001)
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

func TestFileStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	s := NewFileStore(path)
	s.LogDecision("task-1", "haiku", AdmissionDecision{Accept: false})
	s.LogDecision("task-2", "haiku", AdmissionDecision{Accept: false})
	s.LogResult("task-3", "sonnet", 0.9, "success")
	require.NoError(t, s.Save())

	// a fresh store loaded from the same path must see the prior history
	reloaded := NewFileStore(path)
	sig := reloaded.Signal("haiku", KindExtract)
	assert.InDelta(t, 1.0, sig.HistoricalRejectRate, 0.001, "both haiku decisions were rejects")

	sonnetSig := reloaded.Signal("sonnet", KindReview)
	assert.InDelta(t, 0.9, sonnetSig.AvgEvalScore, 0.001)
}

func TestFileStore_MissingFile_EmptyBaseline(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "does-not-exist.json"))
	sig := s.Signal("opus", KindPlan)
	// no data → neutral baseline
	assert.InDelta(t, 0.1, sig.HistoricalRejectRate, 0.001)
}

func TestFileStore_CorruptFile_EmptyBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0600))

	s := NewFileStore(path)
	sig := s.Signal("opus", KindPlan)
	assert.InDelta(t, 0.1, sig.HistoricalRejectRate, 0.001, "corrupt history must fall back to baseline, not crash")
}

func TestFileStore_LegacyHistoryFallsBackAcrossTaskKinds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	legacy := []byte(`[{"kind":"result","task_id":"old","model":"model","score":0.8,"status":"success"}]`)
	require.NoError(t, os.WriteFile(path, legacy, 0600))

	sig := NewFileStore(path).Signal("model", KindReview)
	assert.InDelta(t, 1.0, sig.HistoricalSuccessRate, 0.001)
	assert.InDelta(t, 0.8, sig.AvgEvalScore, 0.001)
}

func TestFileStore_SatisfiesSignalSource(t *testing.T) {
	// compile-time check that FileStore can drive RankCandidates
	var _ SignalSource = NewFileStore(filepath.Join(t.TempDir(), "h.json"))
	var _ KindAwareStore = NewFileStore(filepath.Join(t.TempDir(), "h2.json"))
}
