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

func TestFileStore_SatisfiesSignalSource(t *testing.T) {
	// compile-time check that FileStore can drive RankCandidates
	var _ SignalSource = NewFileStore(filepath.Join(t.TempDir(), "h.json"))
}
