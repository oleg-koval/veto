package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/stretchr/testify/require"
)

func TestReadTUIHistoryKeepsNewestEventsAcrossFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logDir := filepath.Join(home, ".veto", "logs")
	require.NoError(t, os.MkdirAll(logDir, 0700))

	var old strings.Builder
	for index := 0; index < maxTUIHistoryEvents; index++ {
		data, err := json.Marshal(ledger.Event{SchemaVersion: ledger.SchemaVersion, Timestamp: time.Unix(int64(index+1), 0).UTC(), EventID: fmt.Sprintf("old-%d", index), RunID: "old", Type: ledger.EventExecutionCompleted})
		require.NoError(t, err)
		old.Write(data)
		old.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "veto-old.log"), []byte(old.String()), 0600))
	newest, err := json.Marshal(ledger.Event{SchemaVersion: ledger.SchemaVersion, Timestamp: time.Unix(int64(maxTUIHistoryEvents+1), 0).UTC(), EventID: "new", RunID: "new", Type: ledger.EventNativeExited})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(experimentPath(), append(newest, '\n'), 0600))

	history := readTUIHistory()
	require.Len(t, history, maxTUIHistoryEvents)
	require.Equal(t, string(ledger.EventNativeExited), history[0].Type)
	require.Equal(t, "new", history[0].EventID)
	require.NotEqual(t, "old-0", history[len(history)-1].EventID)
}

// TestOldSchemaOneHistoryStillRendersAlongsideDecisionEvents verifies that
// TUI history preserves legacy measured zeros alongside decision events.
func TestOldSchemaOneHistoryStillRendersAlongsideDecisionEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".veto", "logs")
	require.NoError(t, os.MkdirAll(dir, 0700))
	lines := `{"schema_version":1,"timestamp":"2026-08-30T07:00:00Z","event_id":"old","run_id":"run","type":"admission.accepted","model":"legacy","confidence":0,"estimated_cost_usd":0}
{"schema_version":1,"timestamp":"2026-08-30T07:00:01Z","event_id":"new","run_id":"run","type":"decision.completed","decision":{"contract_version":1,"mode":"sequential-admission","candidate_count":1,"status":"selected","selected_model":"legacy"}}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "veto-old.log"), []byte(lines), 0600))
	history := readTUIHistory()
	require.Len(t, history, 2)
	require.Equal(t, "decision.completed", history[0].Type)
	old := history[1]
	require.Equal(t, "legacy", old.Model)
	require.Equal(t, "admission.accepted", old.Type)
	require.True(t, old.ConfidenceKnown)
	require.Zero(t, old.Confidence)
	require.True(t, old.EstimatedCostKnown)
	require.Zero(t, old.EstimatedCostUSD)
}
