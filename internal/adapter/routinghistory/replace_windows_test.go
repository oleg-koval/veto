//go:build windows

package routinghistory

import (
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/require"
)

func TestFileStoreSaveReplacesExistingHistoryOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	first := NewFileStore(path)
	first.LogResult("first", "model", 0.9, "success")
	require.NoError(t, first.Save())

	second := NewFileStore(path)
	second.LogResult("second", "model", 0.8, "success")
	require.NoError(t, second.Save())

	reloaded := NewFileStore(path)
	asserted := reloaded.Signal("model", router.KindReview)
	require.InDelta(t, 0.8, asserted.AvgEvalScore, 0.001)
}
