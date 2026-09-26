package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestManagerDecisionLifecycle verifies decision start and terminal events,
// telemetry presence, and exclusion of private data from boundary events.
func TestManagerDecisionLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, status                   string
		fail, invalid, selected, known bool
	}{
		{name: "selected", status: "selected", selected: true},
		{name: "known zero", status: "selected", selected: true, known: true},
		{name: "no selection", status: "no_selection"},
		{name: "engine error", status: "error", fail: true},
		{name: "invalid outcome", status: "error", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(NewRegistryFromModels(parityModels()), NewAdmissionGate(&executorMock{}), &legacyDecisionStore{})
			var events []ProgressEvent
			mgr.OnEvent = func(e ProgressEvent) {
				if IsDecisionEvent(e.Kind) {
					events = append(events, e)
				}
			}
			mgr.SetDecisionEngine(decisionEngineFunc(func(_ context.Context, r DecisionRequest) (DecisionOutcome, error) {
				require.Len(t, events, 1, "start precedes engine call")
				require.Equal(t, EventDecisionStarted, events[0].Kind)
				o := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission, Reasons: []DecisionReason{{Code: "private", Detail: "SECRET response"}}}
				if tc.selected {
					o.SelectedCandidate = r.Candidates[0].Model.Name
				}
				if tc.known {
					o.Telemetry = DecisionTelemetry{UsageKnown: true, CachedInputKnown: true, CostKnown: true, LatencyKnown: true}
				}
				if tc.invalid {
					o.SelectedCandidate = "SECRET unoffered model"
				}
				if tc.fail {
					return o, errors.New("SECRET provider payload")
				}
				return o, nil
			}))
			_, _, err := mgr.Route(t.Context(), TaskSpec{ID: "task", Kind: KindSummarize, Objective: "SECRET objective"})
			if tc.selected {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Len(t, events, 2)
			wantKind := EventDecisionCompleted
			if tc.status == "error" {
				wantKind = EventDecisionError
			}
			require.Equal(t, wantKind, events[1].Kind)
			p := events[1].Decision
			require.Equal(t, DecisionVersion, p.Version)
			require.Equal(t, DecisionModeSequentialAdmission, p.Mode)
			require.Equal(t, 3, p.CandidateCount)
			require.Equal(t, tc.status, p.Status)
			encoded, err := json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "SECRET")
			require.Empty(t, events[1].Detail)
			if tc.known {
				require.NotNil(t, p.TotalTokens)
				require.Zero(t, *p.TotalTokens)
				require.NotNil(t, p.CostUSD)
				require.Zero(t, *p.CostUSD)
				require.NotNil(t, p.CachedInputTokens)
				require.NotNil(t, p.LatencyMS)
			} else {
				require.Nil(t, p.TotalTokens)
				require.Nil(t, p.CostUSD)
				require.Nil(t, p.CachedInputTokens)
				require.Nil(t, p.LatencyMS)
			}
		})
	}
}

// TestDefaultDecisionEventsEncloseLegacyAdmission verifies that boundary
// events enclose admission events and are absent when all models are skipped.
func TestDefaultDecisionEventsEncloseLegacyAdmission(t *testing.T) {
	var kinds []EventKind
	exec := &executorMock{RunFunc: func(context.Context, string) AdmissionResult {
		require.Equal(t, EventAskStart, kinds[len(kinds)-1])
		return AdmissionResult{Output: `{"accept":true,"confidence":0.9}`}
	}}
	mgr := NewManager(NewRegistryFromModels(parityModels()[:1]), NewAdmissionGate(exec), &legacyDecisionStore{})
	mgr.OnEvent = func(e ProgressEvent) { kinds = append(kinds, e.Kind) }
	_, _, err := mgr.Route(t.Context(), TaskSpec{Kind: KindSummarize})
	require.NoError(t, err)
	require.Equal(t, []EventKind{EventFilterPass, EventShortlist, EventDecisionStarted, EventAskStart, EventAskAccept, EventDecisionCompleted}, kinds)
	kinds = nil
	_, _, err = mgr.Route(t.Context(), TaskSpec{Kind: KindSummarize, SkipModels: []string{"m0"}})
	require.ErrorIs(t, err, ErrNoCandidate)
	require.Equal(t, []EventKind{EventFilterPass, EventShortlist}, kinds, "no engine call when every candidate is skipped")
}

// TestDecisionTelemetryKnownFieldsAreIndependent verifies that each known
// flag controls its own event fields, preserving zero and omitting unknowns.
func TestDecisionTelemetryKnownFieldsAreIndependent(t *testing.T) {
	p := decisionProgress(1, "no_selection", &DecisionOutcome{Telemetry: DecisionTelemetry{CachedInputKnown: true}})
	data, err := json.Marshal(p)
	require.NoError(t, err)
	require.Contains(t, string(data), `"cached_input_tokens":0`)
	require.NotContains(t, string(data), `"input_tokens"`)
	require.NotContains(t, string(data), `"total_tokens"`)
	require.NotContains(t, string(data), `"cost_usd"`)
	require.NotContains(t, string(data), `"latency_ms"`)

	p = decisionProgress(1, "selected", &DecisionOutcome{Telemetry: DecisionTelemetry{UsageKnown: true, InputTokens: 2, OutputTokens: 3, TotalTokens: 5, CostKnown: true, CostUSD: 0.01, LatencyKnown: true, LatencyMs: 7}})
	require.Equal(t, 2, *p.InputTokens)
	require.Equal(t, 3, *p.OutputTokens)
	require.Equal(t, 5, *p.TotalTokens)
	require.Equal(t, 0.01, *p.CostUSD)
	require.EqualValues(t, 7, *p.LatencyMS)
	require.Nil(t, p.CachedInputTokens)
}
