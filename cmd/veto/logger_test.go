package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupLoggerWritesReplayablePrivateLedger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	previous := eventLedger
	t.Cleanup(func() { eventLedger = previous })

	setupLogger()
	logEvent("task-1", "plan", "low", router.ProgressEvent{
		Kind: router.EventFilterPass, Model: "sol",
	})

	paths, err := filepath.Glob(filepath.Join(home, ".veto", "logs", "veto-*.log"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	info, err := os.Stat(paths[0])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	file, err := os.Open(paths[0])
	require.NoError(t, err)
	defer file.Close()
	events, corrupt, err := ledger.Read(file)
	require.NoError(t, err)
	assert.Zero(t, corrupt)
	require.Len(t, events, 1)
	assert.Equal(t, ledger.EventFilterPass, events[0].Type)
}

func TestLogEventIncludesProviderErrorDetail(t *testing.T) {
	var output bytes.Buffer
	previous := eventLedger
	eventLedger = ledger.NewWriter(&output)
	t.Cleanup(func() { eventLedger = previous })

	logEvent("plan a QA pass", "plan", "low", router.ProgressEvent{
		Kind:   router.EventAskError,
		Model:  "sol",
		Detail: "openai api: unsupported parameter",
	})

	var event map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	assert.Equal(t, "openai api: unsupported parameter", event["detail"])
	assert.Equal(t, float64(ledger.SchemaVersion), event["schema_version"])
	assert.Equal(t, "admission.error", event["type"])
	assert.NotContains(t, event, "task_obj")
}

func TestLogExecutionPreservesKnownUsage(t *testing.T) {
	var output bytes.Buffer
	previous := eventLedger
	eventLedger = ledger.NewWriter(&output)
	t.Cleanup(func() { eventLedger = previous })

	logExecution("task-1", ledger.EventExecutionCompleted, router.ModelCapabilities{
		Name: "model", Runtime: "openai-api",
	}, router.ExecutionMetrics{
		Status: "success", InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		UsageKnown: true, CostUSD: 0.01, CostKnown: true, LatencyMs: 20, LatencyKnown: true,
	}, "")

	var event ledger.Event
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	require.NotNil(t, event.Usage)
	assert.Equal(t, 15, event.Usage.TotalTokens)
	require.NotNil(t, event.CostUSD)
	assert.Equal(t, 0.01, *event.CostUSD)
}

func TestLedgerTypeMapsRouterEventsAndRejectsUnknown(t *testing.T) {
	tests := map[router.EventKind]ledger.EventType{
		router.EventFilterPass: ledger.EventFilterPass,
		router.EventFilterFail: ledger.EventFilterFail,
		router.EventAskStart:   ledger.EventAdmissionStarted,
		router.EventAskAccept:  ledger.EventAdmissionAccepted,
		router.EventAskReject:  ledger.EventAdmissionRejected,
		router.EventAskError:   ledger.EventAdmissionError,
	}
	for input, want := range tests {
		got, ok := ledgerType(input)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
	_, ok := ledgerType(router.EventKind("future_event"))
	assert.False(t, ok)
}
