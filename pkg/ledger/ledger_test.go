package ledger

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriterAppendsVersionedRedactedEnvelope(t *testing.T) {
	var output bytes.Buffer
	w := NewWriter(&output)
	w.now = func() time.Time { return time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC) }

	require.NoError(t, w.Append(Event{
		RunID: "run-1", Type: EventAdmissionError, TaskID: "task-1",
		Detail: "Authorization: Bearer secret-token OPENROUTER_API_KEY=sk-or-secret",
	}))

	events, corrupt, err := Read(strings.NewReader(output.String()))
	require.NoError(t, err)
	assert.Zero(t, corrupt)
	require.Len(t, events, 1)
	assert.Equal(t, SchemaVersion, events[0].SchemaVersion)
	assert.Equal(t, "run-1", events[0].RunID)
	assert.Equal(t, EventAdmissionError, events[0].Type)
	assert.NotEmpty(t, events[0].EventID)
	assert.Equal(t, "2026-08-30T07:00:00Z", events[0].Timestamp.Format(time.RFC3339))
	assert.NotContains(t, events[0].Detail, "secret-token")
	assert.NotContains(t, events[0].Detail, "sk-or-secret")
	assert.NotContains(t, output.String(), "objective")
}

func TestReadSkipsCorruptLines(t *testing.T) {
	input := strings.Join([]string{
		`{"schema_version":1,"timestamp":"2026-08-30T07:00:00Z","event_id":"one","run_id":"run","type":"route.filter_pass"}`,
		`{not-json}`,
		`{"schema_version":1,"timestamp":"2026-08-30T07:00:01Z","event_id":"two","run_id":"run","type":"execution.completed"}`,
	}, "\n")

	events, corrupt, err := Read(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, 1, corrupt)
	require.Len(t, events, 2)
	assert.Equal(t, EventFilterPass, events[0].Type)
	assert.Equal(t, EventExecutionCompleted, events[1].Type)
}

func TestWriterRejectsMissingRequiredEnvelopeFields(t *testing.T) {
	var output bytes.Buffer
	w := NewWriter(&output)
	assert.Error(t, w.Append(Event{Type: EventFilterPass}))
	assert.Error(t, w.Append(Event{RunID: "run"}))
	assert.Empty(t, output.String())
}

func TestEventTypesCoverPlannedLifecycle(t *testing.T) {
	for _, eventType := range []EventType{
		EventFilterPass, EventAdmissionStarted, EventExecutionStarted,
		EventToolStarted, EventApprovalRequested, EventArtifactCreated,
		EventReviewStarted, EventGoalStarted, EventGoalStopped,
	} {
		assert.NotEmpty(t, eventType)
	}
}
