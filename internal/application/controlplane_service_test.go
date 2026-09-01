package application

import (
	"context"
	"testing"

	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/router"
)

func TestControlServiceRoutesAndPublishesProgress(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	service := NewControlService(Runner{}, routerPort)
	updates := service.Subscribe(context.Background())
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{
		ActionID:  "route",
		Arguments: map[string]string{"objective": "summarize this"},
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if result.Model != "test-model" {
		t.Fatalf("result model = %q, want test-model", result.Model)
	}
	if routerPort.task.Objective != "summarize this" || routerPort.task.Risk != router.RiskMedium {
		t.Fatalf("routed task = %#v", routerPort.task)
	}
	select {
	case event := <-updates:
		if event.Kind != "route.completed" || event.ActionID != "route" {
			t.Fatalf("event = %#v", event)
		}
	default:
		t.Fatal("route completion event was not published")
	}
}

func TestControlServiceRejectsMissingObjectiveBeforeRouting(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{}
	service := NewControlService(Runner{}, routerPort)
	if _, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route"}); err == nil {
		t.Fatal("route without objective unexpectedly succeeded")
	}
	if routerPort.called {
		t.Fatal("router called for invalid request")
	}
}

func TestControlServiceRunsThroughRunnerAndStreamsOutput(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	runner := Runner{Router: routerPort, Runtime: serviceResolver{runtime: serviceRuntime{}}}
	service := NewControlService(runner, routerPort)
	updates := service.Subscribe(context.Background())
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{
		ActionID:  "run",
		Arguments: map[string]string{"objective": "write a short answer", "max-output-tokens": "16"},
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Output != "done" || result.Model != "test-model" {
		t.Fatalf("result = %#v", result)
	}
	var kinds []string
	for {
		select {
		case event := <-updates:
			kinds = append(kinds, event.Kind)
		default:
			if len(kinds) < 2 {
				t.Fatalf("events = %#v", kinds)
			}
			return
		}
	}
}

func TestControlServiceSnapshotExposesOnlyModelMetadata(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{}
	service := NewControlService(Runner{Runtime: modelSource{models: []router.ModelCapabilities{{Name: "safe", Source: "catalog", Provider: "test", Runtime: "cli", Tier: "small", MaxContextTokens: 1000, SupportsTools: []string{"read"}, CostPer1kInputUSD: 0.1}}, preferences: router.CandidatePreferences{PinnedModels: []string{"safe"}}}}, routerPort)
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snapshot.Models) != 1 || snapshot.Models[0].Name != "safe" {
		t.Fatalf("models = %#v", snapshot.Models)
	}
	if snapshot.Models[0].Source != "catalog" || !snapshot.Models[0].ToolsKnown || !snapshot.Models[0].CostPer1kInputKnown {
		t.Fatalf("model metadata = %#v", snapshot.Models[0])
	}
	if !snapshot.Models[0].Pinned || snapshot.Models[0].Excluded {
		t.Fatalf("model policy = %#v", snapshot.Models[0])
	}
	if len(snapshot.Providers) != 1 || !snapshot.Providers[0].Configured {
		t.Fatalf("providers = %#v", snapshot.Providers)
	}
}

func TestControlServiceSnapshotKeepsKnownProvidersWithoutDuplicatingRuntimeMetadata(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{}
	service := NewControlServiceWithSnapshot(
		Runner{Runtime: modelSource{models: []router.ModelCapabilities{{Name: "safe", Provider: "openai", Runtime: "api"}}}},
		routerPort,
		func(context.Context) (controlplane.Snapshot, error) {
			return controlplane.Snapshot{
				Providers: []controlplane.ProviderSnapshot{{Name: "OpenAI", Configured: true}},
				Models:    []controlplane.ModelSnapshot{{Name: "stale", Provider: "openai"}},
			}, nil
		},
	)
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snapshot.Models) != 1 || snapshot.Models[0].Name != "safe" {
		t.Fatalf("models = %#v, want runtime metadata only", snapshot.Models)
	}
	if len(snapshot.Providers) != 1 || snapshot.Providers[0].Name != "OpenAI" || snapshot.Providers[0].ModelCount != 1 {
		t.Fatalf("providers = %#v, want merged known provider", snapshot.Providers)
	}
}

func TestControlServiceMergesRedactedLocalSnapshotSource(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{}
	service := NewControlServiceWithSnapshot(Runner{}, routerPort, func(context.Context) (controlplane.Snapshot, error) {
		return controlplane.Snapshot{
			History:   []controlplane.HistorySnapshot{{Type: "execution.completed", Model: "safe"}},
			Analytics: controlplane.AnalyticsSnapshot{LocalPath: "~/.veto/logs", RetentionDays: 7},
		}, nil
	})
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snapshot.History) != 1 || snapshot.Analytics.RetentionDays != 7 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestControlServiceRunsRegisteredReadOnlyHandler(t *testing.T) {
	t.Parallel()

	service := NewControlService(Runner{}, &serviceRouter{})
	updates := service.Subscribe(context.Background())
	service.RegisterHandler("doctor", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		return controlplane.ActionResult{ActionID: request.ActionID, Summary: "doctor complete", Output: "safe"}, nil
	})
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "doctor"})
	if err != nil || result.Summary != "doctor complete" {
		t.Fatalf("handler result = %#v, err=%v", result, err)
	}
	if first := <-updates; first.Kind != "output" || first.Message != "safe" {
		t.Fatalf("handler output event = %#v", first)
	}
}

func TestControlServiceCancelStopsActiveAction(t *testing.T) {
	t.Parallel()

	routerPort := &blockingServiceRouter{started: make(chan struct{})}
	service := NewControlService(Runner{}, routerPort)
	done := make(chan error, 1)
	go func() {
		_, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{"objective": "wait"}})
		done <- err
	}()
	<-routerPort.started
	if err := service.Cancel(context.Background(), "route"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled action unexpectedly succeeded")
	}
}

type serviceRouter struct {
	model  router.ModelCapabilities
	called bool
	task   router.TaskSpec
}

func (r *serviceRouter) Route(_ context.Context, task router.TaskSpec) (router.ModelCapabilities, router.AdmissionDecision, error) {
	r.called = true
	r.task = task
	return r.model, router.AdmissionDecision{Accept: true}, nil
}

func (r *serviceRouter) RecordExecution(router.TaskSpec, string, router.ExecutionMetrics) {}

type blockingServiceRouter struct {
	started chan struct{}
}

func (r *blockingServiceRouter) Route(ctx context.Context, _ router.TaskSpec) (router.ModelCapabilities, router.AdmissionDecision, error) {
	close(r.started)
	<-ctx.Done()
	return router.ModelCapabilities{}, router.AdmissionDecision{}, ctx.Err()
}

func (r *blockingServiceRouter) RecordExecution(router.TaskSpec, string, router.ExecutionMetrics) {}

type serviceResolver struct {
	runtime execution.RuntimeAdapter
}

type modelSource struct {
	models      []router.ModelCapabilities
	preferences router.CandidatePreferences
}

func (m modelSource) RuntimeFor(string) (execution.RuntimeAdapter, bool) { return nil, false }
func (m modelSource) Models() []router.ModelCapabilities                 { return m.models }
func (m modelSource) Preferences() router.CandidatePreferences           { return m.preferences }

func (r serviceResolver) RuntimeFor(string) (execution.RuntimeAdapter, bool) {
	return r.runtime, r.runtime != nil
}

type serviceRuntime struct{}

func (serviceRuntime) Run(context.Context, string) execution.Result {
	return execution.Result{Output: "accepted"}
}
func (serviceRuntime) Execute(context.Context, string, execution.ExecutionOptions) execution.Result {
	return execution.Result{Output: "done"}
}
func (serviceRuntime) EffectiveTools() []string { return []string{} }
func (serviceRuntime) RuntimeID() string        { return "test" }
