package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
		if event.Kind != "route.completed" || event.ActionID != "route" || event.Version != controlplane.SchemaVersion {
			t.Fatalf("event = %#v", event)
		}
	default:
		t.Fatal("route completion event was not published")
	}
}

func TestControlServiceRefreshesRoutingBeforeRouteAndRun(t *testing.T) {
	t.Parallel()

	for _, actionID := range []string{"route", "run"} {
		t.Run(actionID, func(t *testing.T) {
			routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "fresh-model", Provider: "test"}}
			runner := Runner{Router: routerPort, Runtime: serviceResolver{runtime: serviceRuntime{}}}
			service := NewControlService(runner, routerPort)
			refreshes := 0
			service.SetRoutingRefresher(func() error {
				refreshes++
				return nil
			})

			_, err := service.Execute(context.Background(), controlplane.ActionRequest{
				ActionID:  actionID,
				Arguments: map[string]string{"objective": "inspect the repository"},
			})
			if err != nil {
				t.Fatalf("%s failed: %v", actionID, err)
			}
			if refreshes != 1 {
				t.Fatalf("routing refreshes = %d, want 1", refreshes)
			}
		})
	}
}

func TestControlServiceStopsRouteWhenRoutingRefreshFails(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{}
	service := NewControlService(Runner{}, routerPort)
	service.SetRoutingRefresher(func() error { return errors.New("Codex login changed") })
	_, err := service.Execute(context.Background(), controlplane.ActionRequest{
		ActionID: "route", Arguments: map[string]string{"objective": "inspect the repository"},
	})
	if err == nil || !strings.Contains(err.Error(), "refresh routing providers") {
		t.Fatalf("refresh error = %v", err)
	}
	if routerPort.called {
		t.Fatal("router called after refresh failure")
	}
}

func TestControlServiceRejectsUnsupportedRequestSchema(t *testing.T) {
	t.Parallel()

	service := NewControlService(Runner{}, &serviceRouter{})
	if _, err := service.Execute(context.Background(), controlplane.ActionRequest{Version: controlplane.SchemaVersion + 1, ActionID: "doctor"}); err == nil {
		t.Fatal("unsupported request schema unexpectedly succeeded")
	}
}

func TestTaskFromRequestKeepsOutputBudgetOutOfRoutingCapabilities(t *testing.T) {
	t.Parallel()

	task, err := taskFromRequest(controlplane.ActionRequest{Arguments: map[string]string{
		"kind": "review", "risk": "high", "required-tools": "read, browser-dom", "requires-executable-tools": "true", "criteria": "tests pass; no regression", "max-cost": "0.25", "max-output-tokens": "120",
	}}, "inspect the change")
	if err != nil {
		t.Fatalf("taskFromRequest returned error: %v", err)
	}
	if task.Kind != router.KindReview || task.Risk != router.RiskHigh || !task.RequiresExecutableTools || task.MaxCostUSD != 0.25 || task.MaxTokens != 0 {
		t.Fatalf("task = %#v", task)
	}
	if len(task.RequiredTools) != 2 || task.RequiredTools[1] != "browser-dom" || len(task.SuccessCriteria) != 2 {
		t.Fatalf("task capabilities = %#v criteria = %#v", task.RequiredTools, task.SuccessCriteria)
	}
	if options := executionOptions(controlplane.ActionRequest{Arguments: map[string]string{"max-output-tokens": "120"}}); options.MaxOutputTokens != 120 {
		t.Fatalf("execution options = %#v", options)
	}
}

func TestTaskFromRequestInfersKindWhenComposerLeavesKindEmpty(t *testing.T) {
	t.Parallel()

	task, err := taskFromRequest(controlplane.ActionRequest{}, "summarize this incident")
	if err != nil {
		t.Fatalf("taskFromRequest returned error: %v", err)
	}
	if task.Kind != router.KindSummarize {
		t.Fatalf("inferred kind = %q, want %q", task.Kind, router.KindSummarize)
	}
}

func TestTaskFromRequestInfersExecutableRequirement(t *testing.T) {
	t.Parallel()

	task, err := taskFromRequest(controlplane.ActionRequest{}, "commit and push the repository changes")
	if err != nil {
		t.Fatalf("taskFromRequest returned error: %v", err)
	}
	if !task.RequiresExecutableTools {
		t.Fatalf("task should require executable tools: %#v", task)
	}
}

func TestTaskFromRequestRejectsMalformedMaxCost(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"0.1x", "NaN", "Inf"} {
		if _, err := taskFromRequest(controlplane.ActionRequest{Arguments: map[string]string{"max-cost": raw}}, "do the task"); err == nil {
			t.Fatalf("max-cost %q should be rejected", raw)
		}
	}
}

func TestControlServiceUsesRouteTimeoutPerAdmission(t *testing.T) {
	t.Parallel()

	routerPort := &timedServiceRouter{serviceRouter: serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}}
	service := NewControlService(Runner{}, routerPort)
	_, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{
		"objective": "summarize this", "timeout": "25ms",
	}})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if routerPort.admissionTimeout != 25*time.Millisecond {
		t.Fatalf("admission timeout = %s, want 25ms", routerPort.admissionTimeout)
	}
}

func TestControlServiceResolvesSkillsBeforeRun(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	runtime := &capturingRuntime{}
	service := NewControlService(Runner{Router: routerPort, Runtime: serviceResolver{runtime: runtime}}, routerPort)
	service.SetSkillResolver(func(context.Context, router.TaskSpec) []string { return []string{"approved skill instructions"} })
	_, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "run", Arguments: map[string]string{"objective": "write"}})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !strings.Contains(runtime.prompt, "approved skill instructions") {
		t.Fatalf("run prompt = %q, missing approved skill", runtime.prompt)
	}
}

func TestControlServicePropagatesHistorySaveFailure(t *testing.T) {
	t.Parallel()

	historyErr := errors.New("history unavailable")
	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	service := NewControlService(Runner{}, routerPort)
	service.SetHistorySaver(func() error { return historyErr })
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{"objective": "summarize this"}})
	if !errors.Is(err, historyErr) {
		t.Fatalf("Execute error = %v, want history error", err)
	}
	if result.Model != "test-model" {
		t.Fatalf("result = %#v, want completed route result", result)
	}
}

func TestControlServiceTracksBoundedRuntimeMonitorCounters(t *testing.T) {
	t.Parallel()

	service := NewControlService(Runner{}, &serviceRouter{})
	service.recordExecutionMonitor(ExecutionEvent{Kind: ExecutionStarted})
	service.recordRuntimeMonitor(execution.RuntimeEvent{Kind: execution.RuntimeToolStarted})
	service.recordRuntimeMonitor(execution.RuntimeEvent{Kind: execution.RuntimeApprovalRequested})
	service.recordRuntimeMonitor(execution.RuntimeEvent{Kind: execution.RuntimeArtifactCreated, Count: 2})
	service.recordExecutionMonitor(ExecutionEvent{Kind: ExecutionCompleted, Model: router.ModelCapabilities{Name: "haiku", Provider: "anthropic", Runtime: "claude-cli"}, Metrics: router.ExecutionMetrics{InputTokens: 40, CachedInputTokens: 30, CachedInputKnown: true, OutputTokens: 2, TotalTokens: 42, UsageKnown: true, CostUSD: 0.12, CostKnown: true, LatencyMs: 80, LatencyKnown: true}})
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	monitor := snapshot.Monitor
	if monitor.ActiveSessions != 0 || monitor.ActiveTools != 1 || monitor.PendingApprovals != 1 || monitor.Artifacts != 2 || monitor.LastModel != "haiku" || monitor.LastProvider != "anthropic" || monitor.LastRuntime != "claude-cli" || monitor.InputTokens != 40 || monitor.CachedInputTokens != 30 || !monitor.CachedInputKnown || monitor.OutputTokens != 2 || monitor.TotalTokens != 42 || !monitor.CostKnown || monitor.LatencyMs != 80 {
		t.Fatalf("monitor = %#v", monitor)
	}
}

func TestControlServiceRetainsLastRouteWhenRefreshingAnotherScreen(t *testing.T) {
	service := NewControlService(Runner{}, &serviceRouter{})
	service.setSnapshot(controlplane.Snapshot{Model: "haiku", Provider: "anthropic", Status: "ready"})
	service.setSnapshot(controlplane.Snapshot{ActiveAction: "doctor", Status: "running"})
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if snapshot.Model != "haiku" || snapshot.Provider != "anthropic" {
		t.Fatalf("last route was cleared by screen refresh: %#v", snapshot)
	}
}

func TestControlServicePublishesUsefulSafeRuntimeDetails(t *testing.T) {
	executionMessage := executionEventMessage(ExecutionEvent{
		Model: router.ModelCapabilities{Name: "codex"},
		Metrics: router.ExecutionMetrics{
			Status: "success", InputTokens: 385562, CachedInputTokens: 300000, CachedInputKnown: true, OutputTokens: 3297, UsageKnown: true,
			LatencyMs: 103540, LatencyKnown: true,
		},
	})
	for _, want := range []string{"codex", "success", "385562 input + 3297 output (300000 reused)", "1m43.54s"} {
		if !strings.Contains(executionMessage, want) {
			t.Fatalf("execution message %q missing %q", executionMessage, want)
		}
	}
	if got := runtimeEventMessage(execution.RuntimeEvent{Kind: execution.RuntimeToolError, Name: "shell", Status: "failed (exit 7)"}); got != "shell · failed (exit 7)" {
		t.Fatalf("runtime message = %q", got)
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

func TestControlServiceRejectsInvalidNumericLimitsBeforeRouting(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		argument  string
		value     string
		wantError string
	}{
		{name: "max cost", argument: "max-cost", value: "0.1x", wantError: "invalid max-cost"},
		{name: "negative max cost", argument: "max-cost", value: "-1", wantError: "invalid max-cost"},
		{name: "non-finite max cost", argument: "max-cost", value: "NaN", wantError: "invalid max-cost"},
		{name: "max output tokens", argument: "max-output-tokens", value: "many", wantError: "invalid max-output-tokens"},
		{name: "zero max output tokens", argument: "max-output-tokens", value: "0", wantError: "invalid max-output-tokens"},
		{name: "negative max output tokens", argument: "max-output-tokens", value: "-1", wantError: "invalid max-output-tokens"},
	} {
		t.Run(test.name, func(t *testing.T) {
			routerPort := &serviceRouter{}
			service := NewControlService(Runner{}, routerPort)
			_, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{
				"objective": "summarize this", test.argument: test.value,
			}})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("invalid limit error = %v, want %q", err, test.wantError)
			}
			if routerPort.called {
				t.Fatal("router called for invalid numeric limit")
			}
		})
	}
}

func TestControlServiceRunsThroughRunnerAndStreamsOutput(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	runtime := &capturingServiceRuntime{}
	runner := Runner{Router: routerPort, Runtime: serviceResolver{runtime: runtime}}
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
	if routerPort.task.MaxTokens != 0 {
		t.Fatalf("routing max tokens = %d, want execution budget kept out of TaskSpec", routerPort.task.MaxTokens)
	}
	if runtime.options.MaxOutputTokens != 16 {
		t.Fatalf("execution max output tokens = %d, want 16", runtime.options.MaxOutputTokens)
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

func TestControlServiceRunsAcceptanceReviewForComposerCriteria(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	service := NewControlService(Runner{Router: routerPort, Runtime: serviceResolver{runtime: serviceRuntime{}}}, routerPort)
	service.SetReviewer(func(_ context.Context, task router.TaskSpec, output, model string) (bool, error) {
		if len(task.SuccessCriteria) != 2 || output != "done" || model != "test-model" {
			t.Fatalf("review input = task:%#v output:%q model:%q", task, output, model)
		}
		return true, nil
	})
	updates := service.Subscribe(context.Background())
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "run", Arguments: map[string]string{"objective": "write", "criteria": "tests pass; no regression"}})
	if err != nil || result.Summary != "task completed" {
		t.Fatalf("run result = %#v err=%v", result, err)
	}
	foundReview := false
	for {
		select {
		case event := <-updates:
			if event.Kind == "review.completed" {
				foundReview = true
			}
		default:
			if !foundReview {
				t.Fatal("review completion event was not published")
			}
			return
		}
	}
}

func TestControlServiceHonorsTimeoutAndSafeOutputWriter(t *testing.T) {
	t.Parallel()

	routerPort := &serviceRouter{model: router.ModelCapabilities{Name: "test-model", Provider: "test"}}
	service := NewControlService(Runner{Router: routerPort, Runtime: serviceResolver{runtime: serviceRuntime{}}}, routerPort)
	var gotPath, gotOutput string
	var gotForce bool
	service.SetOutputWriter(func(path, output string, force bool) error {
		gotPath, gotOutput, gotForce = path, output, force
		return nil
	})
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "run", Arguments: map[string]string{
		"objective": "write", "timeout": "1s", "output": "answer.md", "force": "true",
	}})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if gotPath != "answer.md" || gotOutput != "done" || !gotForce || result.Output != "done" {
		t.Fatalf("output writer = path:%q output:%q force:%v result:%#v", gotPath, gotOutput, gotForce, result)
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

func TestControlServiceSnapshotClassifiesHostSelectedCodexAsHarness(t *testing.T) {
	t.Parallel()

	service := NewControlService(Runner{Runtime: modelSource{models: []router.ModelCapabilities{
		{Name: "codex", Provider: "codex", APIModel: "default", Runtime: "codex-cli"},
		{Name: "luna", Provider: "openai", APIModel: "gpt-5.6-luna", Runtime: "openai-api"},
	}}}, &serviceRouter{})
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snapshot.Models) != 2 {
		t.Fatalf("models = %#v", snapshot.Models)
	}
	for _, model := range snapshot.Models {
		switch model.Name {
		case "codex":
			if model.Kind != controlplane.ModelKindHarness || model.ModelID != "default" {
				t.Fatalf("codex identity = %#v", model)
			}
		case "luna":
			if model.Kind != controlplane.ModelKindModel || model.ModelID != "gpt-5.6-luna" {
				t.Fatalf("luna identity = %#v", model)
			}
		}
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

func TestControlServiceSnapshotSourceClearsStaleCollections(t *testing.T) {
	t.Parallel()

	service := NewControlServiceWithSnapshot(Runner{}, &serviceRouter{}, func(context.Context) (controlplane.Snapshot, error) {
		return controlplane.Snapshot{Status: "ready"}, nil
	})
	service.setSnapshot(controlplane.Snapshot{
		Providers:    []controlplane.ProviderSnapshot{{Name: "stale"}},
		History:      []controlplane.HistorySnapshot{{Model: "stale"}},
		Plans:        []controlplane.PlanSnapshot{{Name: "stale.md"}},
		Health:       []controlplane.HealthSnapshot{{ID: "stale"}},
		Integrations: []controlplane.IntegrationSnapshot{{Name: "stale"}},
		Analytics:    controlplane.AnalyticsSnapshot{LocalPath: "stale"},
	})
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(snapshot.Providers) != 0 || len(snapshot.History) != 0 || len(snapshot.Plans) != 0 || len(snapshot.Health) != 0 || len(snapshot.Integrations) != 0 || snapshot.Analytics.LocalPath != "" {
		t.Fatalf("stale collections survived source refresh: %#v", snapshot)
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

func TestControlServiceRejectsDuplicateActiveActionID(t *testing.T) {
	routerPort := &duplicateActionRouter{started: make(chan struct{}), release: make(chan struct{})}
	service := NewControlService(Runner{}, routerPort)
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{"objective": "first"}})
		firstDone <- err
	}()
	<-routerPort.started

	_, secondErr := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "route", Arguments: map[string]string{"objective": "second"}})
	close(routerPort.release)
	<-firstDone
	if secondErr == nil || !strings.Contains(secondErr.Error(), "already active") {
		t.Fatalf("duplicate action error = %v", secondErr)
	}
}

type serviceRouter struct {
	model  router.ModelCapabilities
	called bool
	task   router.TaskSpec
}

type timedServiceRouter struct {
	serviceRouter
	admissionTimeout time.Duration
}

func (r *timedServiceRouter) RouteWithAdmissionTimeout(ctx context.Context, task router.TaskSpec, timeout time.Duration) (router.ModelCapabilities, router.AdmissionDecision, error) {
	r.admissionTimeout = timeout
	return r.Route(ctx, task)
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

type duplicateActionRouter struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (r *duplicateActionRouter) Route(ctx context.Context, _ router.TaskSpec) (router.ModelCapabilities, router.AdmissionDecision, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return router.ModelCapabilities{}, router.AdmissionDecision{}, ctx.Err()
		}
	}
	return router.ModelCapabilities{Name: "test-model"}, router.AdmissionDecision{Accept: true}, nil
}

func (r *duplicateActionRouter) RecordExecution(router.TaskSpec, string, router.ExecutionMetrics) {}

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

type capturingRuntime struct {
	prompt string
}

func (r *capturingRuntime) Run(context.Context, string) execution.Result {
	return execution.Result{Output: "accepted"}
}
func (r *capturingRuntime) Execute(_ context.Context, prompt string, _ execution.ExecutionOptions) execution.Result {
	r.prompt = prompt
	return execution.Result{Output: "done"}
}
func (*capturingRuntime) EffectiveTools() []string { return nil }
func (*capturingRuntime) RuntimeID() string        { return "capturing" }

func (serviceRuntime) Run(context.Context, string) execution.Result {
	return execution.Result{Output: "accepted"}
}
func (serviceRuntime) Execute(context.Context, string, execution.ExecutionOptions) execution.Result {
	return execution.Result{Output: "done"}
}
func (serviceRuntime) EffectiveTools() []string { return []string{} }
func (serviceRuntime) RuntimeID() string        { return "test" }

type capturingServiceRuntime struct {
	options execution.ExecutionOptions
}

func (*capturingServiceRuntime) Run(context.Context, string) execution.Result {
	return execution.Result{Output: "accepted"}
}
func (r *capturingServiceRuntime) Execute(_ context.Context, _ string, options execution.ExecutionOptions) execution.Result {
	r.options = options
	return execution.Result{Output: "done"}
}
func (*capturingServiceRuntime) EffectiveTools() []string { return []string{} }
func (*capturingServiceRuntime) RuntimeID() string        { return "test" }
