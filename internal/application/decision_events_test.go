package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/require"
)

type decisionAdmission struct{}

func (decisionAdmission) Run(context.Context, string) router.AdmissionResult {
	return router.AdmissionResult{Output: `{"accept":true,"confidence":0.9}`}
}

func TestControlServiceDefaultManagerDecisionComposition(t *testing.T) {
	for _, action := range []string{"route", "run"} {
		t.Run(action, func(t *testing.T) {
			mgr := router.NewManager(router.NewRegistryFromModels([]router.ModelCapabilities{{Name: "fixture", Tier: "large", Provider: "test"}}), router.NewAdmissionGate(decisionAdmission{}), router.NewMemoryStore())
			service := NewControlService(Runner{Router: mgr, Runtime: serviceResolver{runtime: serviceRuntime{}}}, mgr)
			updates := service.Subscribe(t.Context())
			var recorded []router.ProgressEvent
			service.SetRouteEventRecorder(func(e router.ProgressEvent) { recorded = append(recorded, e) })
			_, err := service.Execute(t.Context(), controlplane.ActionRequest{ActionID: action, Arguments: map[string]string{"objective": "summarize this"}})
			require.NoError(t, err)
			var boundaries []controlplane.Event
			for len(updates) > 0 {
				e := <-updates
				if e.Decision != nil {
					boundaries = append(boundaries, e)
				}
			}
			require.Len(t, boundaries, 2)
			require.Equal(t, "decision.started", boundaries[0].Kind)
			require.Equal(t, "decision.completed", boundaries[1].Kind)
			require.Equal(t, "fixture", boundaries[1].Decision.SelectedModel)
			require.Equal(t, router.DecisionModeSequentialAdmission, boundaries[1].Decision.Mode)
			var count int
			for _, e := range recorded {
				if router.IsDecisionEvent(e.Kind) {
					count++
				}
			}
			require.Equal(t, 2, count)
		})
	}
}

func TestControlPlaneDecisionMappingExcludesLegacyDetail(t *testing.T) {
	service := NewControlService(Runner{}, &serviceRouter{})
	updates := service.Subscribe(t.Context())
	for _, kind := range []router.EventKind{router.EventDecisionStarted, router.EventDecisionCompleted, router.EventDecisionError} {
		service.publishRouteEvent(router.ProgressEvent{Kind: kind, Model: "SECRET", Reasons: []string{"SECRET"}, Detail: "SECRET", Confidence: 1, Decision: &router.DecisionProgress{Version: 1, Mode: router.DecisionModeSequentialAdmission, CandidateCount: 1, Status: "error"}})
		event := <-updates
		require.Equal(t, string(kind), event.Kind)
		require.NotNil(t, event.Decision)
		require.Empty(t, event.Message)
		require.Empty(t, event.Model)
		require.False(t, event.ConfidenceKnown)
		data, err := json.Marshal(event)
		require.NoError(t, err)
		require.NotContains(t, string(data), "SECRET")
	}
}
