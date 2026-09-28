package paired

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/internal/adapter/shadowhistory"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/oleg-koval/veto/pkg/shadow"
	"github.com/stretchr/testify/require"
)

type decisionFunc func(context.Context, router.DecisionRequest) (router.DecisionOutcome, error)

func (f decisionFunc) Decide(ctx context.Context, request router.DecisionRequest) (router.DecisionOutcome, error) {
	return f(ctx, request)
}

type shadowFunc func(context.Context, router.DecisionRequest) (router.ShadowPrediction, error)

func (f shadowFunc) DecideShadow(ctx context.Context, request router.DecisionRequest) (router.ShadowPrediction, error) {
	return f(ctx, request)
}

type witnessCollector struct{ witness router.PrivateRouteWitness }

func (c *witnessCollector) RecordPrivateRouteWitness(witness router.PrivateRouteWitness) error {
	c.witness = witness
	return nil
}

func testCandidateBinding(name, providerModel string) router.PrivateCandidateBinding {
	return router.PrivateCandidateBinding{
		Model: name,
		Identity: router.ModelIdentity{
			Source: "fixture", Provider: "fixture-provider", Model: providerModel, Runtime: "fixture-runtime",
		},
		Tools: router.ToolCapabilities{Tools: []string{"read", "shell"}, Known: true},
	}
}

func manifestFixture() (shadow.Dataset, Manifest) {
	dataset, trial := replayFixture()
	routeKey := strings.Repeat("7", 64)
	bindingA, bindingB := testCandidateBinding("model-a", "api-v1"), testCandidateBinding("model-b", "api-v2")
	keyA, _ := router.PrivateCandidateKey(routeKey, bindingA.Model, bindingA.Identity, bindingA.Tools)
	keyB, _ := router.PrivateCandidateKey(routeKey, bindingB.Model, bindingB.Identity, bindingB.Tools)
	bindingA.Key, bindingB.Key = keyA, keyB
	dataset.Routes[0].Comparison.Candidates = []shadow.Candidate{{Key: keyA}, {Key: keyB}}
	dataset.Routes[0].Comparison.Authority.SelectedCandidate = keyA
	dataset.Routes[0].Comparison.Shadow.SelectedCandidate = keyB
	fingerprint, _ := router.PrivateTaskFingerprint(routeKey, router.TaskSpec{
		Kind: router.TaskKind(trial.TaskKind), Risk: router.Risk(trial.Risk),
		Objective: trial.Objective, SuccessCriteria: trial.Criteria,
		ExecutionMaxOutputTokens: trial.MaxOutputTokens,
	})
	witness := router.PrivateRouteWitness{
		Version: router.PrivateWitnessVersion, RouteID: trial.RouteID,
		ObservedAt: dataset.Routes[0].Comparison.ObservedAt,
		TaskKind:   router.TaskKind(trial.TaskKind), Risk: router.Risk(trial.Risk),
		RouteKey: routeKey, TaskFingerprint: fingerprint,
		Bindings: []router.PrivateCandidateBinding{bindingA, bindingB},
	}
	return dataset, Manifest{
		Version: ManifestVersion, WorkspaceIndependent: true, RouteID: trial.RouteID, TaskKind: router.TaskKind(trial.TaskKind),
		Risk: router.Risk(trial.Risk), Objective: trial.Objective, Criteria: trial.Criteria,
		MaxOutputTokens: trial.MaxOutputTokens, TimeoutMillis: trial.Timeout.Milliseconds(), Witness: witness,
	}
}

func TestManifestRejectsMismatchedTaskAndBindingsBeforeRunning(t *testing.T) {
	dataset, original := manifestFixture()
	for _, test := range []struct {
		name   string
		change func(*Manifest)
	}{
		{name: "objective", change: func(m *Manifest) { m.Objective = "wrong private objective" }},
		{name: "criteria", change: func(m *Manifest) { m.Criteria = []string{"wrong criterion"} }},
		{name: "budget", change: func(m *Manifest) { m.MaxOutputTokens++ }},
		{name: "binding", change: func(m *Manifest) { m.Witness.Bindings[0].Key = "b" }},
		{name: "swapped models", change: func(m *Manifest) {
			m.Witness.Bindings[0].Model, m.Witness.Bindings[1].Model = m.Witness.Bindings[1].Model, m.Witness.Bindings[0].Model
		}},
		{name: "route key", change: func(m *Manifest) { m.Witness.RouteKey = strings.Repeat("8", 64) }},
		{name: "time", change: func(m *Manifest) { m.Witness.ObservedAt = m.Witness.ObservedAt.Add(time.Second) }},
		{name: "route", change: func(m *Manifest) { m.RouteID = "other" }},
		{name: "version", change: func(m *Manifest) { m.Witness.Version++ }},
		{name: "workspace assertion", change: func(m *Manifest) { m.WorkspaceIndependent = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := original
			manifest.Witness.Bindings = append([]router.PrivateCandidateBinding(nil), original.Witness.Bindings...)
			test.change(&manifest)
			calls := 0
			events, err := ReplayManifest(t.Context(), dataset, manifest, runnerFunc(func(context.Context, RunRequest) (RunResult, error) {
				calls++
				return RunResult{}, nil
			}), graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
				return shadow.KnownBool{}, shadow.KnownFloat{}, nil
			}))
			require.Error(t, err)
			require.Empty(t, events)
			require.Zero(t, calls)
			require.NotContains(t, err.Error(), manifest.Objective)
		})
	}
}

func TestManifestReplaysDivergentPairFromLargerShortlist(t *testing.T) {
	dataset, manifest := manifestFixture()
	bindingC := testCandidateBinding("model-c", "api-v3")
	bindingC.Key, _ = router.PrivateCandidateKey(manifest.Witness.RouteKey, bindingC.Model, bindingC.Identity, bindingC.Tools)
	dataset.Routes[0].Comparison.Candidates = append(dataset.Routes[0].Comparison.Candidates, shadow.Candidate{Key: bindingC.Key})
	manifest.Witness.Bindings = append(manifest.Witness.Bindings, bindingC)

	trial, err := ValidateManifest(dataset, manifest)
	require.NoError(t, err)
	require.Len(t, trial.ModelsByKey, 2)
	require.NotContains(t, trial.ModelsByKey, bindingC.Key)

	runModels := make(map[string]bool)
	labels, err := ReplayManifest(t.Context(), dataset, manifest, runnerFunc(func(_ context.Context, request RunRequest) (RunResult, error) {
		runModels[request.Model] = true
		return RunResult{Output: request.Model}, nil
	}), graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
		return shadow.KnownBool{Known: true, Value: true}, shadow.KnownFloat{}, nil
	}))
	require.NoError(t, err)
	require.Len(t, labels, 2)
	require.True(t, runModels["model-a"])
	require.True(t, runModels["model-b"])
	require.NotContains(t, runModels, "model-c")
}

func TestManifestPrivateFileRejectsOverwriteUnsafePermissionsAndUnknownFields(t *testing.T) {
	dataset, manifest := manifestFixture()
	privateDir := t.TempDir()
	require.NoError(t, os.Chmod(privateDir, 0700))
	path := filepath.Join(privateDir, "private.json")
	require.NoError(t, SaveManifest(path, dataset, manifest))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.Error(t, SaveManifest(path, dataset, manifest))
	loaded, err := LoadManifest(path)
	require.NoError(t, err)
	require.Equal(t, manifest, loaded)

	require.NoError(t, os.Chmod(path, 0644))
	_, err = LoadManifest(path)
	require.ErrorContains(t, err, "unsafe private file")
	require.NoError(t, os.Chmod(path, 0600))
	link := filepath.Join(filepath.Dir(path), "link.json")
	require.NoError(t, os.Symlink(path, link))
	_, err = LoadManifest(link)
	require.ErrorContains(t, err, "unsafe private file")

	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	unknown := filepath.Join(filepath.Dir(path), "unknown.json")
	require.NoError(t, os.WriteFile(unknown, append([]byte(`{"unexpected":true,`), encoded[1:]...), 0600))
	_, err = LoadManifest(unknown)
	require.ErrorContains(t, err, "invalid private JSON")
	require.Error(t, SaveManifest("relative.json", dataset, manifest))
	worldReadableParent := t.TempDir()
	require.NoError(t, os.Chmod(worldReadableParent, 0755))
	require.Error(t, SaveManifest(filepath.Join(worldReadableParent, "trial.json"), dataset, manifest))
	repoRoot := t.TempDir()
	privateRepoDir := filepath.Join(repoRoot, "private")
	require.NoError(t, os.Mkdir(privateRepoDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".git"), []byte("gitdir: elsewhere"), 0600))
	require.ErrorContains(t, SaveManifest(filepath.Join(privateRepoDir, "trial.json"), dataset, manifest), "repository paths")
	linkedParent := filepath.Join(t.TempDir(), "linked-private")
	require.NoError(t, os.Symlink(privateDir, linkedParent))
	require.ErrorContains(t, SaveManifest(filepath.Join(linkedParent, "trial.json"), dataset, manifest), "private parent directory")
}

func TestPrivateRootPinsCheckedDirectoryAcrossRename(t *testing.T) {
	container := t.TempDir()
	privateDir := filepath.Join(container, "private")
	require.NoError(t, os.Mkdir(privateDir, 0700))
	root, name, err := openPrivateRoot(filepath.Join(privateDir, "manifest.json"))
	require.NoError(t, err)
	defer root.Close()
	moved := filepath.Join(container, "moved")
	require.NoError(t, os.Rename(privateDir, moved))
	require.NoError(t, os.Mkdir(privateDir, 0700))
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, err = os.Stat(filepath.Join(moved, name))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(privateDir, name))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRouteTimeWitnessToPrivateManifestToFakePairedLabels(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0700))
	evidencePath := filepath.Join(root, "redacted.jsonl")
	redacted := shadowhistory.NewFileRecorder(evidencePath, 10)
	private := &witnessCollector{}
	task := router.TaskSpec{
		Kind: router.KindReview, Risk: router.RiskLow, Objective: "private frozen objective",
		SuccessCriteria: []string{"answer is correct"}, ExecutionMaxOutputTokens: 1024,
	}
	engine := router.NewShadowingDecisionEngine(
		decisionFunc(func(context.Context, router.DecisionRequest) (router.DecisionOutcome, error) {
			return router.DecisionOutcome{Version: router.DecisionVersion, SelectedCandidate: "model-a", Mode: router.DecisionModeSequentialAdmission}, nil
		}),
		shadowFunc(func(context.Context, router.DecisionRequest) (router.ShadowPrediction, error) {
			return router.ShadowPrediction{SelectedCandidate: "model-b"}, nil
		}), redacted, time.Second, "jev:fake",
	)
	engine.SetPrivateWitnessRecorder(private)
	_, err := engine.Decide(t.Context(), router.DecisionRequest{
		Version: router.DecisionVersion, Task: task,
		Candidates: []router.DecisionCandidate{
			{Model: router.ModelCapabilities{Name: "model-a", Source: "fixture", Provider: "provider-a", APIModel: "api-v1", Runtime: "fixture-runtime"}, Tools: router.ToolCapabilities{Tools: []string{"read", "shell"}, Known: true}},
			{Model: router.ModelCapabilities{Name: "model-b", Source: "fixture", Provider: "provider-b", APIModel: "api-v2", Runtime: "fixture-runtime"}, Tools: router.ToolCapabilities{Tools: []string{"read", "shell"}, Known: true}},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, private.witness.RouteID)
	evidence, err := os.ReadFile(evidencePath)
	require.NoError(t, err)
	require.NotContains(t, string(evidence), task.Objective)
	require.NotContains(t, string(evidence), "model-a")
	require.NotContains(t, string(evidence), "model-b")
	events, err := shadow.Load(strings.NewReader(string(evidence)))
	require.NoError(t, err)
	dataset, err := shadow.Materialize(events)
	require.NoError(t, err)
	manifest := Manifest{
		Version: ManifestVersion, WorkspaceIndependent: true, RouteID: private.witness.RouteID,
		TaskKind: task.Kind, Risk: task.Risk, Objective: task.Objective,
		Criteria: task.SuccessCriteria, MaxOutputTokens: task.ExecutionMaxOutputTokens,
		TimeoutMillis: 1000, Witness: private.witness,
	}
	path := filepath.Join(root, "private-manifest.json")
	require.NoError(t, SaveManifest(path, dataset, manifest))
	loaded, err := LoadManifest(path)
	require.NoError(t, err)
	labels, err := ReplayManifest(t.Context(), dataset, loaded, runnerFunc(func(_ context.Context, request RunRequest) (RunResult, error) {
		if request.Model == "model-a" {
			return RunResult{Output: "pass"}, nil
		}
		return RunResult{Output: "fail"}, nil
	}), graderFunc(func(_ context.Context, criteria []string, output string) (shadow.KnownBool, shadow.KnownFloat, error) {
		require.Equal(t, task.SuccessCriteria, criteria)
		return shadow.KnownBool{Known: true, Value: output == "pass"}, shadow.KnownFloat{}, nil
	}))
	require.NoError(t, err)
	require.Len(t, labels, 2)
	require.Equal(t, dataset.Routes[0].Comparison.Authority.SelectedCandidate, labels[0].Label.Candidate)
	require.True(t, labels[0].Label.Success.Value)
	require.Equal(t, dataset.Routes[0].Comparison.Shadow.SelectedCandidate, labels[1].Label.Candidate)
	require.False(t, labels[1].Label.Success.Value)
	encodedLabels, err := json.Marshal(labels)
	require.NoError(t, err)
	require.NotContains(t, string(encodedLabels), task.Objective)
	require.NotContains(t, string(encodedLabels), "model-a")
	require.NotContains(t, string(encodedLabels), "model-b")
}
