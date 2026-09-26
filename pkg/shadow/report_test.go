package shadow

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEvaluateFixture checks deterministic metrics and insufficient readiness for the offline fixture.
func TestEvaluateFixture(t *testing.T) {
	file, err := os.Open("testdata/shadow_v1.jsonl")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	events, err := Load(file)
	require.NoError(t, err)
	dataset, err := Materialize(events)
	require.NoError(t, err)

	report := Evaluate(dataset, DefaultFallbackConfidence)
	require.Equal(t, 4, report.Routes)
	require.Equal(t, 4, report.LabeledRoutes)
	require.Equal(t, map[string]int{"debug": 1, "plan": 3}, report.LabeledRoutesByTaskKind)
	require.Equal(t, Rate{Count: 1, Total: 3, Value: 1.0 / 3.0}, report.SelectionAgreement)
	require.Equal(t, Rate{Count: 1, Total: 4, Value: .25}, report.ShadowUnavailableOrError)
	require.Equal(t, Rate{Count: 2, Total: 4, Value: .5}, report.SimulatedFallback)
	require.Equal(t, Rate{Count: 3, Total: 4, Value: .75}, report.Authority.Success)
	require.Equal(t, Rate{Count: 1, Total: 2, Value: .5}, report.Shadow.Success)
	require.InDelta(t, .103125, report.Authority.Calibration.BrierScore, 1e-9)
	require.InDelta(t, .265, report.Shadow.Calibration.BrierScore, 1e-9)
	require.Equal(t, Distribution{Known: 4, Average: 1150, P95: 1300}, report.Authority.LatencyMs)
	require.Equal(t, Distribution{Known: 4, Average: 265, P95: 750}, report.Shadow.LatencyMs)
	require.Equal(t, 0, report.Shadow.CostUSD.Known)
	require.Equal(t, ReadinessInsufficient, report.Readiness.Status)
}

// TestPromotionPolicyReadyDataset checks that a sufficiently labeled synthetic dataset passes every promotion gate.
func TestPromotionPolicyReadyDataset(t *testing.T) {
	kinds := []string{"plan", "debug", "review", "refactor", "code-change"}
	dataset := Dataset{}
	for index := 0; index < 500; index++ {
		routeID := fmt.Sprintf("route-%d", index)
		comparison := RouteComparison{
			RouteID: routeID, ObservedAt: time.Unix(int64(index+1), 0), TaskKind: kinds[index%len(kinds)], Risk: "low",
			Candidates: []Candidate{{Key: "c1"}}, AuthorityStrategy: "sequential-admission", ShadowStrategy: "jev:test",
			Authority: readyDecision(.01, 600), Shadow: readyDecision(.001, 100),
		}
		label := ExecutionLabel{RouteID: routeID, ObservedAt: time.Unix(int64(index+2), 0), Candidate: "c1", Success: KnownBool{Known: true, Value: true}}
		dataset.Routes = append(dataset.Routes, RouteRecord{Comparison: comparison, Labels: map[string]ExecutionLabel{"c1": label}})
	}
	report := Evaluate(dataset, DefaultFallbackConfidence)
	for name, gate := range report.Readiness.Gates {
		require.Equal(t, GatePass, gate.Status, name)
	}
	require.Equal(t, ReadinessReady, report.Readiness.Status)
}

// TestCalibrationUsesConfidenceWhenSuccessProbabilityIsUnknown checks the authority confidence proxy for calibration.
func TestCalibrationUsesConfidenceWhenSuccessProbabilityIsUnknown(t *testing.T) {
	dataset := Dataset{Routes: []RouteRecord{{
		Comparison: RouteComparison{
			RouteID: "route", TaskKind: "plan", Risk: "low", Candidates: []Candidate{{Key: "c1"}},
			Authority: DecisionEvidence{Status: StatusSelected, SelectedCandidate: "c1", Confidence: KnownFloat{Known: true, Value: .8}},
			Shadow:    DecisionEvidence{Status: StatusNoSelection},
		},
		Labels: map[string]ExecutionLabel{"c1": {Success: KnownBool{Known: true, Value: true}}},
	}}}

	report := Evaluate(dataset, DefaultFallbackConfidence)

	require.Equal(t, 1, report.Authority.Calibration.Samples)
	require.InDelta(t, .04, report.Authority.Calibration.BrierScore, 1e-12)
}

// TestPromotionPolicyRequiresPairedLabelCoverage checks that missing counterfactual labels block success and calibration gates.
func TestPromotionPolicyRequiresPairedLabelCoverage(t *testing.T) {
	kinds := []string{"plan", "debug", "review", "refactor", "code-change"}
	dataset := Dataset{}
	for index := 0; index < 500; index++ {
		routeID := fmt.Sprintf("coverage-%d", index)
		shadowCandidate := "c1"
		if index < 26 {
			shadowCandidate = "c2"
		}
		comparison := RouteComparison{
			RouteID: routeID, ObservedAt: time.Unix(int64(index+1), 0), TaskKind: kinds[index%len(kinds)], Risk: "low",
			Candidates: []Candidate{{Key: "c1"}, {Key: "c2"}}, AuthorityStrategy: "sequential-admission", ShadowStrategy: "jev:test",
			Authority: readyDecision(.01, 600), Shadow: readyDecision(.001, 100),
		}
		comparison.Shadow.SelectedCandidate = shadowCandidate
		label := ExecutionLabel{RouteID: routeID, Candidate: "c1", Success: KnownBool{Known: true, Value: true}}
		dataset.Routes = append(dataset.Routes, RouteRecord{Comparison: comparison, Labels: map[string]ExecutionLabel{"c1": label}})
	}

	report := Evaluate(dataset, DefaultFallbackConfidence)

	require.Equal(t, GateInsufficientData, report.Readiness.Gates["routing_success_noninferiority"].Status)
	require.Equal(t, GateInsufficientData, report.Readiness.Gates["calibration"].Status)
	require.Equal(t, ReadinessInsufficient, report.Readiness.Status)
}

// readyDecision builds a successful decision fixture with known cost and latency.
func readyDecision(cost float64, latency int64) DecisionEvidence {
	return DecisionEvidence{
		Status: StatusSelected, SelectedCandidate: "c1",
		Probability: KnownFloat{Known: true, Value: 1}, Confidence: KnownFloat{Known: true, Value: 1},
		Telemetry: Telemetry{CostUSD: KnownFloat{Known: true, Value: cost}, Latency: KnownDuration{Known: true, Millis: latency}},
	}
}
