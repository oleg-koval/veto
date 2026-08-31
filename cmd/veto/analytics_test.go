package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyticsStatusDefaultsToLocalOnly(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previous := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previous })

	status, err := currentAnalyticsStatus()
	require.NoError(t, err)
	assert.True(t, status.LocalCollection)
	assert.Equal(t, "not_implemented", status.RemoteCollection)
	assert.Equal(t, "not set", status.RemoteSharing)
	assert.False(t, status.RemoteTransportActive)
	assert.False(t, remoteAnalyticsOptedIn())
}

func TestAnalyticsPreferenceMergesConfigAndControlsFutureTransport(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previous := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previous })
	require.NoError(t, os.WriteFile(configPath, []byte(`{"on_failure":"continue"}`), 0600))

	require.NoError(t, saveAnalyticsPreference(analyticsOptIn))
	assert.True(t, remoteAnalyticsOptedIn())
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var full map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &full))
	var analytics analyticsConfig
	require.NoError(t, json.Unmarshal(full["analytics"], &analytics))
	assert.Equal(t, analyticsOptIn, analytics.RemoteSharing)
	assert.Equal(t, analyticsPolicyVersion, analytics.PolicyVersion)
	assert.Contains(t, string(full["on_failure"]), "continue")

	require.NoError(t, saveAnalyticsPreference(analyticsOptOut))
	assert.False(t, remoteAnalyticsOptedIn())
}

func TestAnalyticsStatusJSONAndCommandOutput(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previous := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previous })

	var jsonOutput strings.Builder
	assert.Equal(t, 0, runAnalyticsCommand([]string{"status", "--json"}, &jsonOutput, &strings.Builder{}))
	var status analyticsStatus
	require.NoError(t, json.Unmarshal([]byte(jsonOutput.String()), &status))
	assert.Equal(t, "not_implemented", status.RemoteCollection)

	var output strings.Builder
	assert.Equal(t, 0, runAnalyticsCommand([]string{"enable"}, &output, &strings.Builder{}))
	assert.Contains(t, output.String(), "Nothing is sent today")
}

func TestAnalyticsRejectsMalformedConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previous := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previous })
	require.NoError(t, os.WriteFile(configPath, []byte(`{"analytics":`), 0600))

	assert.Error(t, saveAnalyticsPreference(analyticsOptIn))
	assert.Error(t, func() error {
		_, err := currentAnalyticsStatus()
		return err
	}())
}
