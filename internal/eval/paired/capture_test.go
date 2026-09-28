package paired

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/require"
)

func privateCaptureTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "captures")
	require.NoError(t, os.Mkdir(dir, 0700))
	return dir
}

func captureFixture(routeID string) (router.PrivateRouteWitness, router.TaskSpec) {
	task := router.TaskSpec{
		Source: "user", Kind: router.KindPlan, Risk: router.RiskLow,
		Objective: "summarize the fixture", SuccessCriteria: []string{"include the key result"},
		ExecutionMaxOutputTokens: 512,
	}
	routeKey := strings.Repeat("a", 64)
	binding := router.PrivateCandidateBinding{
		Model:    "fixture-model",
		Identity: router.ModelIdentity{Source: "fixture", Provider: "fixture", Model: "fixture-v1", Runtime: "fixture-runtime"},
		Tools:    router.ToolCapabilities{Tools: []string{"read"}, Known: true},
	}
	binding.Key, _ = router.PrivateCandidateKey(routeKey, binding.Model, binding.Identity, binding.Tools)
	fingerprint, _ := router.PrivateTaskFingerprint(routeKey, task)
	witness := router.PrivateRouteWitness{
		Version: router.PrivateWitnessVersion, RouteID: routeID, ObservedAt: time.Now().UTC(),
		TaskKind: task.Kind, Risk: task.Risk, RouteKey: routeKey, TaskFingerprint: fingerprint,
		Bindings: []router.PrivateCandidateBinding{binding},
	}
	return witness, task
}

func TestFileCaptureRecorderWritesPrivateReplayManifest(t *testing.T) {
	dir := privateCaptureTestDir(t)
	recorder, err := NewFileCaptureRecorder(dir, 2)
	require.NoError(t, err)
	witness, task := captureFixture("r-000000000000000000000001")
	require.NoError(t, recorder.RecordPrivateRouteCapture(witness, task))

	path := filepath.Join(dir, "manifest-"+witness.RouteID+".json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	manifest, err := LoadManifest(path)
	require.NoError(t, err)
	require.Equal(t, task.Objective, manifest.Objective)
	require.Equal(t, task.SuccessCriteria, manifest.Criteria)
	require.Equal(t, witness.Bindings, manifest.Witness.Bindings)
	require.Equal(t, defaultReplayTimeout.Milliseconds(), manifest.TimeoutMillis)
}

func TestFileCaptureRecorderSkipsIneligibleTasks(t *testing.T) {
	dir := privateCaptureTestDir(t)
	recorder, err := NewFileCaptureRecorder(dir, 2)
	require.NoError(t, err)
	witness, task := captureFixture("r-000000000000000000000002")

	variants := []router.TaskSpec{task, task, task}
	variants[0].Source = "system"
	variants[1].SuccessCriteria = nil
	variants[2].ExecutionMaxOutputTokens = 0
	variants = append(variants, task)
	variants[3].ExcludeFromPrivateCapture = true
	for _, variant := range variants {
		require.NoError(t, recorder.RecordPrivateRouteCapture(witness, variant))
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestFileCaptureRecorderBoundsRetentionAndRejectsUnsafeRouteID(t *testing.T) {
	dir := privateCaptureTestDir(t)
	recorder, err := NewFileCaptureRecorder(dir, 1)
	require.NoError(t, err)
	first, task := captureFixture("r-000000000000000000000003")
	require.NoError(t, recorder.RecordPrivateRouteCapture(first, task))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "manifest-"+first.RouteID+".json"), old, old))
	second, task := captureFixture("r-000000000000000000000004")
	require.NoError(t, recorder.RecordPrivateRouteCapture(second, task))
	require.NoFileExists(t, filepath.Join(dir, "manifest-"+first.RouteID+".json"))
	require.FileExists(t, filepath.Join(dir, "manifest-"+second.RouteID+".json"))

	unsafe, task := captureFixture("../outside")
	require.Error(t, recorder.RecordPrivateRouteCapture(unsafe, task))
	require.NoFileExists(t, filepath.Join(filepath.Dir(dir), "outside.json"))
}
