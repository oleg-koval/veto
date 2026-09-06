package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/dispatch"
	"github.com/stretchr/testify/require"
)

func TestRunUnavailableAcceptsAgentBeforeFlagsAndCanClear(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, resetExperimentLogger())
	t.Cleanup(func() { _ = resetExperimentLogger() })
	store := dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json"))
	var output, diagnostics bytes.Buffer
	require.Equal(t, 0, runUnavailable([]string{"claude", "--for", "2h"}, &output, &diagnostics, store))
	require.Contains(t, output.String(), "claude unavailable until")
	output.Reset()
	require.Equal(t, 0, runUnavailable([]string{"claude", "--clear"}, &output, &diagnostics, store))
	require.Contains(t, output.String(), "available")
	require.NotContains(t, diagnostics.String(), "error")
	entries, err := store.List()
	require.NoError(t, err)
	require.Empty(t, entries)
	if strings.Contains(output.String(), "sk-secret") {
		t.Fatal("availability output leaked a secret")
	}
}

func TestRunUnavailableRejectsMutationFlagsWithoutAgent(t *testing.T) {
	for _, args := range [][]string{{"--for", "2h"}, {"--clear"}} {
		var output, diagnostics bytes.Buffer
		code := runUnavailable(args, &output, &diagnostics, dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json")))
		require.Equal(t, 2, code, "args=%v output=%q diagnostics=%q", args, output.String(), diagnostics.String())
		require.Contains(t, diagnostics.String(), "agent is required")
	}
}
