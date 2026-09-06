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
