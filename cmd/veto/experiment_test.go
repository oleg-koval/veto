package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/stretchr/testify/require"
)

func TestAppendExperimentEventRotatesActiveOversizedLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, resetExperimentLogger())
	t.Cleanup(func() { _ = resetExperimentLogger() })
	require.NoError(t, os.MkdirAll(filepath.Dir(experimentPath()), 0700))
	require.NoError(t, os.WriteFile(experimentPath(), make([]byte, maxExperimentLogBytes), 0600))

	require.NoError(t, appendExperimentEvent(ledger.Event{RunID: "run-test", Type: ledger.EventNativeExited}))
	data, err := os.ReadFile(experimentPath())
	require.NoError(t, err)
	require.Less(t, len(data), maxExperimentLogBytes)
	require.Contains(t, string(data), string(ledger.EventNativeExited))
}
