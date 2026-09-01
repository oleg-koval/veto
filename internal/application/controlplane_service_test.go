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

type serviceRouter struct {
	model  router.ModelCapabilities
	called bool
}

func (r *serviceRouter) Route(_ context.Context, _ router.TaskSpec) (router.ModelCapabilities, router.AdmissionDecision, error) {
	r.called = true
	return r.model, router.AdmissionDecision{Accept: true}, nil
}

func (r *serviceRouter) RecordExecution(router.TaskSpec, string, router.ExecutionMetrics) {}

type serviceResolver struct {
	runtime execution.RuntimeAdapter
}

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
