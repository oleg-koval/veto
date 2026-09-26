package shadow

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEventValidation checks rejection of invalid versions, candidates, error codes, and probability values.
func TestEventValidation(t *testing.T) {
	valid := comparisonEvent("route-1", StatusSelected, "c1")
	require.NoError(t, valid.Validate())

	tests := map[string]func(*Event){
		"version":             func(e *Event) { e.SchemaVersion = 2 },
		"duplicate candidate": func(e *Event) { e.Comparison.Candidates = append(e.Comparison.Candidates, Candidate{Key: "c1"}) },
		"unknown selection":   func(e *Event) { e.Comparison.Shadow.SelectedCandidate = "missing" },
		"free form error": func(e *Event) {
			e.Comparison.Shadow = DecisionEvidence{Status: StatusError, ErrorCode: "provider said no"}
		},
		"unknown nonzero": func(e *Event) { e.Comparison.Shadow.Probability = KnownFloat{Value: .5} },
		"nan":             func(e *Event) { e.Comparison.Shadow.Confidence = KnownFloat{Known: true, Value: math.NaN()} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			event := comparisonEvent("route-1", StatusSelected, "c1")
			mutate(&event)
			require.Error(t, event.Validate())
		})
	}
}

// TestExecutionLabelKnownZeroAndUnknown checks valid known zeros and rejects a true value marked unknown.
func TestExecutionLabelKnownZeroAndUnknown(t *testing.T) {
	label := Event{SchemaVersion: 1, Type: EventExecutionLabel, Label: &ExecutionLabel{
		RouteID: "route-1", Candidate: "c1", ObservedAt: time.Unix(2, 0).UTC(),
		Success: KnownBool{Known: true}, Score: KnownFloat{Known: true},
		Usage: KnownUsage{Known: true}, CostUSD: KnownFloat{}, Latency: KnownDuration{},
	}}
	require.NoError(t, label.Validate())
	label.Label.Success = KnownBool{Value: true}
	require.EqualError(t, label.Validate(), "shadow evidence: unknown success must be false")
}

// comparisonEvent builds a comparison fixture with configurable route identity and shadow selection.
func comparisonEvent(id string, shadowStatus DecisionStatus, shadowCandidate string) Event {
	shadowDecision := DecisionEvidence{Status: shadowStatus}
	if shadowStatus == StatusSelected {
		shadowDecision.SelectedCandidate = shadowCandidate
		shadowDecision.Probability = KnownFloat{Known: true, Value: .8}
		shadowDecision.Confidence = KnownFloat{Known: true, Value: .9}
	}
	return Event{SchemaVersion: 1, Type: EventRouteComparison, Comparison: &RouteComparison{
		RouteID: id, ObservedAt: time.Unix(1, 0).UTC(), TaskKind: "plan", Risk: "medium",
		AuthorityStrategy: "sequential-admission", ShadowStrategy: "fixture-shadow",
		Candidates: []Candidate{{Key: "c1"}, {Key: "c2"}},
		Authority:  DecisionEvidence{Status: StatusSelected, SelectedCandidate: "c1", Probability: KnownFloat{Known: true, Value: .9}, Confidence: KnownFloat{Known: true, Value: .9}},
		Shadow:     shadowDecision,
	}}
}
