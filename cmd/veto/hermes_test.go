package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHermesAPIHandshake(t *testing.T) {
	oldVersion := version
	version = "0.4.0-test"
	t.Cleanup(func() { version = oldVersion })
	var stdout, stderr bytes.Buffer
	code := runHermesCommand([]string{"api", "--json"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, float64(hermesPluginAPIVersion), got["api_version"])
	assert.Equal(t, "0.4.0-test", got["version"])
}

func TestHermesPluginCommandLifecycle(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hermes")
	var stdout, stderr bytes.Buffer
	code := runHermesCommand([]string{"plugin", "install", "--home", home}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "hermes plugins enable veto --no-allow-tool-override")

	stdout.Reset()
	stderr.Reset()
	code = runHermesCommand([]string{"plugin", "status", "--home", home}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "installed and current")

	stdout.Reset()
	stderr.Reset()
	code = runHermesCommand([]string{"plugin", "uninstall", "--home", home}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "configuration was not changed")
}
