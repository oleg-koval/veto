package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupLoggerWritesReplayablePrivateLedger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	previous := eventLedger
	previousRunID := eventRunID
	t.Cleanup(func() {
		eventLedger = previous
		eventRunID = previousRunID
	})

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
	assert.Equal(t, "task-1", events[0].TaskID)
	assert.Regexp(t, `^run-[0-9a-f]{32}$`, events[0].RunID)
	assert.NotEqual(t, events[0].TaskID, events[0].RunID)
}

func TestLogEventIncludesProviderErrorDetail(t *testing.T) {
	var output bytes.Buffer
	previous := eventLedger
	previousRunID := eventRunID
	eventLedger = ledger.NewWriter(&output)
	eventRunID = "run-test"
	t.Cleanup(func() {
		eventLedger = previous
		eventRunID = previousRunID
	})

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
	assert.Equal(t, "run-test", event["run_id"])
	assert.Equal(t, "plan a QA pass", event["task_id"])
	assert.NotContains(t, event, "task_obj")
}

func TestLogExecutionPreservesKnownUsage(t *testing.T) {
	var output bytes.Buffer
	previous := eventLedger
	previousRunID := eventRunID
	eventLedger = ledger.NewWriter(&output)
	eventRunID = "run-test"
	t.Cleanup(func() {
		eventLedger = previous
		eventRunID = previousRunID
	})

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

func TestLogRuntimeEventUsesAllowlistedFields(t *testing.T) {
	var output bytes.Buffer
	previous := eventLedger
	previousRunID := eventRunID
	eventLedger = ledger.NewWriter(&output)
	eventRunID = "run-test"
	t.Cleanup(func() {
		eventLedger = previous
		eventRunID = previousRunID
	})

	logRuntimeEvent("task-1", router.ModelCapabilities{Name: "opencode/openai/gpt-5", Runtime: "opencode"}, execution.RuntimeEvent{
		Kind: execution.RuntimeArtifactCreated, Name: "patch", Status: "created", Count: 2,
	})

	var event ledger.Event
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	assert.Equal(t, ledger.EventArtifactCreated, event.Type)
	assert.Equal(t, "opencode", event.Runtime)
	assert.Equal(t, "name=patch count=2", event.Detail)
	assert.NotContains(t, output.String(), "prompt")
	assert.NotContains(t, output.String(), "response")
}

func TestRuntimeLedgerTypeIsClosed(t *testing.T) {
	for input, want := range map[execution.RuntimeEventKind]ledger.EventType{
		execution.RuntimeToolStarted:       ledger.EventToolStarted,
		execution.RuntimeToolCompleted:     ledger.EventToolCompleted,
		execution.RuntimeToolError:         ledger.EventToolError,
		execution.RuntimeApprovalRequested: ledger.EventApprovalRequested,
		execution.RuntimeApprovalGranted:   ledger.EventApprovalGranted,
		execution.RuntimeApprovalDenied:    ledger.EventApprovalDenied,
		execution.RuntimeArtifactCreated:   ledger.EventArtifactCreated,
	} {
		got, ok := runtimeLedgerType(input)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}
	_, ok := runtimeLedgerType(execution.RuntimeEventKind("future"))
	assert.False(t, ok)
}

func TestLogEventPreservesAcceptedZeroEstimates(t *testing.T) {
	var output bytes.Buffer
	previous := eventLedger
	previousRunID := eventRunID
	eventLedger = ledger.NewWriter(&output)
	eventRunID = "run-test"
	t.Cleanup(func() {
		eventLedger = previous
		eventRunID = previousRunID
	})

	logEvent("task-1", "extract", "low", router.ProgressEvent{
		Kind: router.EventAskAccept, Model: "local", Confidence: 0.9,
		EstTokens: 0, EstCost: 0,
	})

	var event ledger.Event
	require.NoError(t, json.Unmarshal(output.Bytes(), &event))
	require.NotNil(t, event.Confidence)
	assert.Equal(t, 0.9, *event.Confidence)
	require.NotNil(t, event.EstimatedTokens)
	assert.Zero(t, *event.EstimatedTokens)
	require.NotNil(t, event.EstimatedCostUSD)
	assert.Zero(t, *event.EstimatedCostUSD)
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
