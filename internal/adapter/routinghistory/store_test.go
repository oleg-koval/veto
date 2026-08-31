package routinghistory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStore_TaskKindMetricsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewFileStore(path)
	s.RecordExecution("task-1", "model", router.KindPlan, router.ExecutionMetrics{
		Status: "success", Score: 0.9, ScoreKnown: true, TotalTokens: 300,
		UsageKnown: true, CostUSD: 0.006, CostKnown: true,
		LatencyMs: 250, LatencyKnown: true,
	})
	require.NoError(t, s.Save())

	reloaded := NewFileStore(path)
	sig := reloaded.Signal("model", router.KindPlan)
	assert.InDelta(t, 0.9, sig.AvgEvalScore, 0.001)
	assert.InDelta(t, 300.0, sig.AvgTotalTokens, 0.001)
	assert.InDelta(t, 0.006, sig.RecentCostUSD, 0.0001)
	assert.InDelta(t, 250.0, sig.RecentLatencyMs, 0.001)
}

func TestFileStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	s := NewFileStore(path)
	s.LogDecision("task-1", "haiku", router.AdmissionDecision{Accept: false})
	s.LogDecision("task-2", "haiku", router.AdmissionDecision{Accept: false})
	s.LogResult("task-3", "sonnet", 0.9, "success")
	require.NoError(t, s.Save())

	reloaded := NewFileStore(path)
	sig := reloaded.Signal("haiku", router.KindExtract)
	assert.InDelta(t, 1.0, sig.HistoricalRejectRate, 0.001, "both haiku decisions were rejects")

	sonnetSig := reloaded.Signal("sonnet", router.KindReview)
	assert.InDelta(t, 0.9, sonnetSig.AvgEvalScore, 0.001)
}

func TestFileStore_MissingFile_EmptyBaseline(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "does-not-exist.json"))
	sig := s.Signal("opus", router.KindPlan)
	assert.InDelta(t, 0.1, sig.HistoricalRejectRate, 0.001)
}

func TestFileStore_CorruptFile_EmptyBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0600))

	s := NewFileStore(path)
	sig := s.Signal("opus", router.KindPlan)
	assert.InDelta(t, 0.1, sig.HistoricalRejectRate, 0.001, "corrupt history must fall back to baseline, not crash")
}

func TestFileStore_LegacyHistoryFallsBackAcrossTaskKinds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	legacy := []byte(`[{"kind":"result","task_id":"old","model":"model","score":0.8,"status":"success"}]`)
	require.NoError(t, os.WriteFile(path, legacy, 0600))

	sig := NewFileStore(path).Signal("model", router.KindReview)
	assert.InDelta(t, 1.0, sig.HistoricalSuccessRate, 0.001)
	assert.InDelta(t, 0.8, sig.AvgEvalScore, 0.001)
}

func TestFileStore_SatisfiesRouterStoreContracts(t *testing.T) {
	var _ router.SignalSource = NewFileStore(filepath.Join(t.TempDir(), "h.json"))
	var _ router.KindAwareStore = NewFileStore(filepath.Join(t.TempDir(), "h2.json"))
}

func TestFileStore_PreservesHistoryFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := NewFileStore(path)
	s.LogResult("task", "model", 0.9, "success")
	require.NoError(t, s.Save())
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
