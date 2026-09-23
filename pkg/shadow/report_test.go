package shadow

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

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
}
