package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/internal/eval/paired"
	"github.com/oleg-koval/veto/pkg/router"
	shadowdata "github.com/oleg-koval/veto/pkg/shadow"
	"github.com/stretchr/testify/require"
)

// TestDecisionShadowDisabledDoesNotReadPrerequisites verifies that disabled shadow mode reads only its enable switch.
func TestDecisionShadowDisabledDoesNotReadPrerequisites(t *testing.T) {
	var lookedUp []string
	config := loadDecisionShadowConfig(func(key string) (string, bool) {
		lookedUp = append(lookedUp, key)
		return "", false
	})
	require.False(t, config.enabled)
	require.Equal(t, []string{envJevShadowEnabled}, lookedUp)
}

// TestDecisionShadowInvalidSwitchStaysDisabled verifies that an invalid switch warns without reading prerequisites.
func TestDecisionShadowInvalidSwitchStaysDisabled(t *testing.T) {
	config := loadDecisionShadowConfig(func(key string) (string, bool) {
		if key == envJevShadowEnabled {
			return "maybe", true
		}
		t.Fatal("disabled configuration read an additional environment variable")
		return "", false
	})
	require.False(t, config.enabled)
	require.Len(t, config.warnings, 1)
}

func TestPrivateCaptureIsDisabledByDefault(t *testing.T) {
	var lookedUp []string
	config := loadPrivateCaptureConfig(func(key string) (string, bool) {
		lookedUp = append(lookedUp, key)
		return "", false
	})
	require.False(t, config.enabled)
	require.Equal(t, paired.DefaultCaptureLimit, config.maxFiles)
	require.Equal(t, []string{envPrivateCapture}, lookedUp)
}

func TestPrivateCaptureRejectsInvalidRetentionLimit(t *testing.T) {
	values := map[string]string{envPrivateCapture: "true", envPrivateCaptureMax: "501"}
	config := loadPrivateCaptureConfig(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	require.False(t, config.enabled)
	require.Len(t, config.warnings, 1)
	require.Contains(t, config.warnings[0], envPrivateCaptureMax)
}

func TestPrivateCaptureWithoutShadowSourceDoesNotCreateManifests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	evidencePath := filepath.Join(t.TempDir(), "evidence.jsonl")
	values := map[string]string{envPrivateCapture: "1", envJevShadowEvidence: evidencePath}
	lookup := func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
	registry := router.NewRegistryFromModels([]router.ModelCapabilities{{Name: "fixture", Tier: "large", Provider: "test"}})
	mgr := router.NewManager(registry, router.NewAdmissionGate(experimentalShadowAdmission{}), router.NewMemoryStore())
	var warnings bytes.Buffer
	configureExperimentalDecisionShadow(mgr, &warnings, lookup, nil)

	_, _, err := mgr.Route(t.Context(), router.TaskSpec{ID: "task-1", Kind: router.KindPlan, Risk: router.RiskMedium, Objective: "design this"})
	require.NoError(t, err)
	require.Contains(t, warnings.String(), "requires an available shadow decision source")
	entries, err := os.ReadDir(filepath.Join(home, ".veto", "paired-captures"))
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestEnabledShadowWithoutKeyPreservesRouteAndRecordsUnavailable checks routing and unavailable evidence when the API key is absent.
func TestEnabledShadowWithoutKeyPreservesRouteAndRecordsUnavailable(t *testing.T) {
	evidencePath := filepath.Join(t.TempDir(), "evidence.jsonl")
	values := map[string]string{
		envJevShadowEnabled: "1", envJevShadowEvidence: evidencePath, envJevShadowTimeout: "20ms",
	}
	lookup := func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
	registry := router.NewRegistryFromModels([]router.ModelCapabilities{{Name: "fixture", Tier: "large", Provider: "test"}})
	mgr := router.NewManager(registry, router.NewAdmissionGate(experimentalShadowAdmission{}), router.NewMemoryStore())
	var warnings bytes.Buffer
	configureExperimentalDecisionShadow(mgr, &warnings, lookup, nil)

	model, _, err := mgr.Route(t.Context(), router.TaskSpec{ID: "task-1", Kind: router.KindPlan, Risk: router.RiskMedium, Objective: "design this"})
	require.NoError(t, err)
	require.Equal(t, "fixture", model.Name)
	require.Contains(t, warnings.String(), "TYPESAFE_API_KEY is not set")

	file, err := os.Open(evidencePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	events, err := shadowdata.Load(file)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, shadowdata.StatusUnavailable, events[0].Comparison.Shadow.Status)
}

// TestShadowReportFixture checks successful CLI JSON output for the offline evidence fixture.
func TestShadowReportFixture(t *testing.T) {
	path := filepath.Join("..", "..", "pkg", "shadow", "testdata", "shadow_v1.jsonl")
	var stdout, stderr bytes.Buffer
	exitCode := runShadowReport([]string{"--input", path}, &stdout, &stderr)
	require.Zero(t, exitCode, stderr.String())
	var report shadowdata.Report
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, 4, report.Routes)
	require.Empty(t, stderr.String())
}

type experimentalShadowAdmission struct{}

// Run returns a fixed accepting admission for shadow configuration tests.
func (experimentalShadowAdmission) Run(context.Context, string) router.AdmissionResult {
	return router.AdmissionResult{Output: `{"accept":true,"confidence":0.9}`}
}
