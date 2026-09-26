package shadow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAppendLoadAndMaterializeLastLabelWins(t *testing.T) {
	var data bytes.Buffer
	require.NoError(t, Append(&data, comparisonEvent("route-1", StatusSelected, "c1")))
	for _, success := range []bool{false, true} {
		require.NoError(t, Append(&data, Event{SchemaVersion: 1, Type: EventExecutionLabel, Label: &ExecutionLabel{
			RouteID: "route-1", Candidate: "c1", ObservedAt: time.Unix(2, 0).UTC(), Success: KnownBool{Known: true, Value: success},
		}}))
	}
	events, err := Load(&data)
	require.NoError(t, err)
	dataset, err := Materialize(events)
	require.NoError(t, err)
	require.Len(t, dataset.Routes, 1)
	require.True(t, dataset.Routes[0].Labels["c1"].Success.Value)
}

func TestLoadRejectsUnknownVersionAndMultipleValues(t *testing.T) {
	_, err := Load(strings.NewReader(`{"schema_version":2,"type":"route_comparison"}` + "\n"))
	require.ErrorContains(t, err, "unsupported schema version")
	_, err = Load(strings.NewReader(`{} {}` + "\n"))
	require.ErrorContains(t, err, "multiple JSON values")
}

func TestLoadAllowsAdditiveFieldsButRejectsSensitiveFields(t *testing.T) {
	event := comparisonEvent("route-1", StatusSelected, "c1")
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(encoded, &raw))
	raw["future_metric"] = 1
	compatible, err := json.Marshal(raw)
	require.NoError(t, err)
	_, err = Load(bytes.NewReader(append(compatible, '\n')))
	require.NoError(t, err)

	raw["objective"] = "must not persist"
	forbidden, err := json.Marshal(raw)
	require.NoError(t, err)
	_, err = Load(bytes.NewReader(append(forbidden, '\n')))
	require.ErrorContains(t, err, `forbidden field "objective"`)
}

func TestMaterializeRejectsOrphanLabel(t *testing.T) {
	event := Event{SchemaVersion: 1, Type: EventExecutionLabel, Label: &ExecutionLabel{
		RouteID: "missing", Candidate: "c1", ObservedAt: time.Unix(2, 0).UTC(),
	}}
	_, err := Materialize([]Event{event})
	require.ErrorContains(t, err, "unknown route")
}
