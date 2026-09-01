package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/router"
)

// ControlService adapts the existing application use cases to the TUI
// control-plane contract. It is in-process by design; no daemon or HTTP hop is
// introduced between the shell and the Runner.
type ControlService struct {
	runner       Runner
	router       Router
	source       func(context.Context) (controlplane.Snapshot, error)
	handlers     map[string]func(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error)
	reviewer     func(context.Context, router.TaskSpec, string, string) (bool, error)
	outputWriter func(string, string, bool) error

	mu       sync.RWMutex
	snapshot controlplane.Snapshot
	subs     map[chan controlplane.Event]struct{}
	active   map[string]context.CancelFunc
}

// SetReviewer wires the existing acceptance-review use case into TUI runs.
// The callback is optional; without one, runs remain compatible with runtimes
// that do not expose review capabilities.
func (s *ControlService) SetReviewer(reviewer func(context.Context, router.TaskSpec, string, string) (bool, error)) {
	s.reviewer = reviewer
}

// SetOutputWriter wires the CLI's safe relative-path writer into TUI runs.
// The callback receives only the user-selected path, output, and force flag.
func (s *ControlService) SetOutputWriter(writer func(string, string, bool) error) {
	s.outputWriter = writer
}

// NewControlService creates a service over an existing Runner and Router.
// Hooks on the supplied Runner are preserved and wrapped to publish events.
func NewControlService(runner Runner, routerPort Router) *ControlService {
	return newControlService(runner, routerPort, nil)
}

// NewControlServiceWithSnapshot adds a composition-root snapshot source for
// local history, health, analytics, and integration metadata. The source must
// return redacted data and is evaluated only when the shell asks for a
// snapshot.
func NewControlServiceWithSnapshot(runner Runner, routerPort Router, source func(context.Context) (controlplane.Snapshot, error)) *ControlService {
	return newControlService(runner, routerPort, source)
}

func newControlService(runner Runner, routerPort Router, source func(context.Context) (controlplane.Snapshot, error)) *ControlService {
	service := &ControlService{
		runner:   runner,
		router:   routerPort,
		source:   source,
		subs:     make(map[chan controlplane.Event]struct{}),
		active:   make(map[string]context.CancelFunc),
		handlers: make(map[string]func(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error)),
		snapshot: controlplane.Snapshot{Status: "idle"},
	}
	service.runner.Hooks = service.wrapHooks(runner.Hooks)
	if emitter, ok := routerPort.(interface {
		SetOnEvent(func(router.ProgressEvent))
	}); ok {
		emitter.SetOnEvent(service.publishRouteEvent)
	}
	return service
}

// RegisterHandler adds a non-routing action at the composition root. Handlers
// are for existing CLI use cases (doctor, benchmark, integrations, and
// similar); they must preserve the same redaction and confirmation rules.
func (s *ControlService) RegisterHandler(actionID string, handler func(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error)) {
	if strings.TrimSpace(actionID) == "" || handler == nil {
		return
	}
	s.handlers[actionID] = handler
}

func (s *ControlService) Snapshot(ctx context.Context) (controlplane.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.RLock()
	snapshot := s.snapshot
	s.mu.RUnlock()
	if s.source != nil {
		provided, err := s.source(ctx)
		if err != nil {
			return snapshot, err
		}
		snapshot = mergeSnapshot(snapshot, provided)
	}
	if source, ok := s.runner.Runtime.(interface {
		Models() []router.ModelCapabilities
	}); ok {
		runtimeModels := source.Models()
		preferences := router.CandidatePreferences{}
		if preferenceSource, ok := s.runner.Runtime.(interface {
			Preferences() router.CandidatePreferences
		}); ok {
			preferences = preferenceSource.Preferences()
		}
		if len(runtimeModels) > 0 {
			// Runtime metadata is authoritative for routable models. Keep the
			// composition-root provider list, but never duplicate stale models
			// returned by a separate snapshot source.
			snapshot.Models = nil
		}
		for _, model := range runtimeModels {
			pinned := slices.Contains(preferences.PinnedModels, model.Name) || slices.Contains(preferences.PinnedProviders, model.Provider)
			favorite := slices.Contains(preferences.FavoriteModels, model.Name) || slices.Contains(preferences.FavoriteProviders, model.Provider)
			excluded := slices.Contains(preferences.DisabledModels, model.Name) || slices.Contains(preferences.ExcludedModels, model.Name) || slices.Contains(preferences.ExcludedProviders, model.Provider)
			snapshot.Models = append(snapshot.Models, controlplane.ModelSnapshot{
				Name: model.Name, Source: model.Source, Provider: model.Provider, Runtime: model.Runtime, Tier: model.Tier,
				ContextTokens: model.MaxContextTokens, Tools: append([]string(nil), model.SupportsTools...), ToolsKnown: model.SupportsTools != nil,
				CostPer1kInputUSD: model.CostPer1kInputUSD, CostPer1kOutputUSD: model.CostPer1kOutputUSD,
				CostPer1kInputKnown: !model.CostPer1kInputUnknown, CostPer1kOutputKnown: !model.CostPer1kOutputUnknown, Status: "available",
				Pinned: pinned, Favorite: favorite, Excluded: excluded,
			})
		}
		sort.Slice(snapshot.Models, func(i, j int) bool { return snapshot.Models[i].Name < snapshot.Models[j].Name })
		counts := make(map[string]int)
		for _, model := range snapshot.Models {
			counts[model.Provider]++
		}
		for provider, count := range counts {
			found := false
			for index := range snapshot.Providers {
				if strings.EqualFold(snapshot.Providers[index].Name, provider) {
					snapshot.Providers[index].Configured = snapshot.Providers[index].Configured || count > 0
					snapshot.Providers[index].ModelCount = count
					found = true
					break
				}
			}
			if !found {
				snapshot.Providers = append(snapshot.Providers, controlplane.ProviderSnapshot{Name: provider, Configured: count > 0, ModelCount: count})
			}
		}
		sort.Slice(snapshot.Providers, func(i, j int) bool { return snapshot.Providers[i].Name < snapshot.Providers[j].Name })
	}
	return snapshot, nil
}

func mergeSnapshot(base, provided controlplane.Snapshot) controlplane.Snapshot {
	if provided.Status != "" {
		base.Status = provided.Status
	}
	if provided.Monitor != (controlplane.MonitorSnapshot{}) {
		base.Monitor = provided.Monitor
	}
	if len(provided.Providers) > 0 {
		base.Providers = provided.Providers
	}
	if len(provided.Models) > 0 {
		base.Models = provided.Models
	}
	if len(provided.History) > 0 {
		base.History = provided.History
	}
	if len(provided.Plans) > 0 {
		base.Plans = provided.Plans
	}
	if len(provided.Health) > 0 {
		base.Health = provided.Health
	}
	if provided.Analytics.LocalPath != "" {
		base.Analytics = provided.Analytics
	}
	if len(provided.Integrations) > 0 {
		base.Integrations = provided.Integrations
	}
	return base
}

func (s *ControlService) Subscribe(ctx context.Context) <-chan controlplane.Event {
	if ctx == nil {
		ctx = context.Background()
	}
	updates := make(chan controlplane.Event, 64)
	s.mu.Lock()
	s.subs[updates] = struct{}{}
	s.mu.Unlock()
	if ctx.Done() == nil {
		return updates
	}
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.subs, updates)
		close(updates)
		s.mu.Unlock()
	}()
	return updates
}

func (s *ControlService) Cancel(_ context.Context, actionID string) error {
	s.mu.RLock()
	cancel, ok := s.active[actionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("control plane: no active action %q", actionID)
	}
	cancel()
	return nil
}

func (s *ControlService) Execute(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request.Version != 0 && request.Version != controlplane.SchemaVersion {
		return controlplane.ActionResult{}, fmt.Errorf("control plane: unsupported request schema version %d", request.Version)
	}
	request.Version = controlplane.SchemaVersion
	objective := strings.TrimSpace(request.Arguments["objective"])
	if (request.ActionID == "route" || request.ActionID == "run") && objective == "" {
		return controlplane.ActionResult{}, errors.New("control plane: objective is required")
	}
	requestCtx := ctx
	cancel := func() {}
	if (request.ActionID == "route" || request.ActionID == "run") && strings.TrimSpace(request.Arguments["timeout"]) != "" {
		rawTimeout := strings.TrimSpace(request.Arguments["timeout"])
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil || timeout <= 0 {
			return controlplane.ActionResult{}, fmt.Errorf("control plane: invalid timeout %q", rawTimeout)
		}
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		requestCtx, cancel = context.WithCancel(ctx)
	}
	if (request.ActionID == "route" || request.ActionID == "run") && strings.TrimSpace(request.Arguments["admission-timeout"]) != "" {
		rawAdmissionTimeout := strings.TrimSpace(request.Arguments["admission-timeout"])
		admissionTimeout, err := time.ParseDuration(rawAdmissionTimeout)
		if err != nil || admissionTimeout <= 0 {
			cancel()
			return controlplane.ActionResult{}, fmt.Errorf("control plane: invalid admission-timeout %q", rawAdmissionTimeout)
		}
		if setter, ok := s.router.(interface{ SetAdmissionTimeout(time.Duration) }); ok {
			setter.SetAdmissionTimeout(admissionTimeout)
		}
	}
	s.mu.Lock()
	s.active[request.ActionID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, request.ActionID)
		s.mu.Unlock()
	}()
	s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "running"})

	switch request.ActionID {
	case "route":
		if s.router == nil {
			return controlplane.ActionResult{}, errors.New("control plane: router is nil")
		}
		task := taskFromRequest(request, objective)
		model, decision, err := s.router.Route(requestCtx, task)
		if err != nil {
			s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
			return controlplane.ActionResult{ActionID: request.ActionID}, err
		}
		s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "ready", Provider: model.Provider, Model: model.Name})
		s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "route.completed", Message: fmt.Sprintf("%s accepted (%.0f%% confidence)", model.Name, decision.Confidence*100)})
		return controlplane.ActionResult{ActionID: request.ActionID, Summary: "model selected", Model: model.Name}, nil
	case "run":
		if s.router == nil {
			return controlplane.ActionResult{}, errors.New("control plane: router is nil")
		}
		task := taskFromRequest(request, objective)
		response, err := s.runner.Execute(requestCtx, Request{Task: task, Options: executionOptions(request), Writer: outputWriter{s: s, actionID: request.ActionID}})
		status := "ready"
		if err != nil {
			status = "error"
		}
		s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: status, Provider: response.Model.Provider, Model: response.Model.Name})
		if err != nil {
			return controlplane.ActionResult{ActionID: request.ActionID, Model: response.Model.Name, Output: response.Output}, err
		}
		if outputPath := strings.TrimSpace(request.Arguments["output"]); outputPath != "" {
			if s.outputWriter == nil {
				return controlplane.ActionResult{ActionID: request.ActionID, Model: response.Model.Name, Output: response.Output}, errors.New("control plane: output writing is unavailable")
			}
			if err := s.outputWriter(outputPath, response.Output, request.Arguments["force"] == "true"); err != nil {
				s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
				return controlplane.ActionResult{ActionID: request.ActionID, Model: response.Model.Name, Output: response.Output}, fmt.Errorf("write output: %w", err)
			}
			s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "output.saved", Message: outputPath})
		}
		if s.reviewer != nil {
			criteria := task.SuccessCriteria
			if len(criteria) > 0 {
				s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "review.started", Message: fmt.Sprintf("checking %d acceptance criteria", len(criteria))})
				passed, reviewErr := s.reviewer(requestCtx, task, response.Output, response.Model.Name)
				if reviewErr != nil {
					s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
					s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "review.error", Message: strings.Join(strings.Fields(reviewErr.Error()), " ")})
					return controlplane.ActionResult{ActionID: request.ActionID, Model: response.Model.Name, Output: response.Output}, reviewErr
				}
				if !passed {
					s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
					s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "review.completed", Message: "acceptance criteria not met"})
					return controlplane.ActionResult{ActionID: request.ActionID, Model: response.Model.Name, Output: response.Output}, errors.New("acceptance criteria not met")
				}
				s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "review.completed", Message: "acceptance criteria passed"})
			}
		}
		s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "run.completed", Message: "task completed"})
		return controlplane.ActionResult{ActionID: request.ActionID, Summary: "task completed", Model: response.Model.Name, Output: response.Output}, nil
	default:
		if handler, ok := s.handlers[request.ActionID]; ok {
			result, err := handler(requestCtx, request)
			status := "ready"
			if err != nil {
				status = "error"
			}
			s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: status})
			if err == nil {
				if result.Output != "" {
					s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "output", Message: result.Output})
				}
				s.publish(controlplane.Event{ActionID: request.ActionID, Kind: request.ActionID + ".completed", Message: result.Summary})
			}
			return result, err
		}
		s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "unsupported"})
		return controlplane.ActionResult{}, fmt.Errorf("control plane: action %q is not executable yet", request.ActionID)
	}
}

func taskFromRequest(request controlplane.ActionRequest, objective string) router.TaskSpec {
	kind := router.TaskKind(request.Arguments["kind"])
	if kind == "" {
		kind = router.KindCodeChange
	}
	risk := router.Risk(request.Arguments["risk"])
	if risk == "" {
		risk = router.RiskMedium
	}
	maxCost, _ := strconv.ParseFloat(request.Arguments["max-cost"], 64)
	maxTokens, _ := strconv.Atoi(request.Arguments["max-output-tokens"])
	return router.TaskSpec{ID: request.Arguments["task-id"], Kind: kind, Objective: objective, Risk: risk, MaxCostUSD: maxCost, MaxTokens: maxTokens, RequiredTools: splitRequestList(request.Arguments["required-tools"]), RequiresExecutableTools: request.Arguments["requires-executable-tools"] == "true", SuccessCriteria: splitRequestList(request.Arguments["criteria"]), RuntimeFilter: request.Arguments["runtime"], ProviderFilter: request.Arguments["provider"], Source: "tui"}
}

func splitRequestList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func executionOptions(request controlplane.ActionRequest) execution.ExecutionOptions {
	maxTokens, _ := strconv.Atoi(request.Arguments["max-output-tokens"])
	return execution.ExecutionOptions{MaxOutputTokens: maxTokens}
}

func (s *ControlService) wrapHooks(original Hooks) Hooks {
	return Hooks{
		OnExecutionEvent: func(event ExecutionEvent) {
			if original.OnExecutionEvent != nil {
				original.OnExecutionEvent(event)
			}
			s.recordExecutionMonitor(event)
			s.publish(controlplane.Event{ActionID: "run", Kind: "execution." + string(event.Kind), Message: event.Detail})
		},
		OnRuntimeEvent: func(taskID string, model router.ModelCapabilities, event execution.RuntimeEvent) {
			if original.OnRuntimeEvent != nil {
				original.OnRuntimeEvent(taskID, model, event)
			}
			s.recordRuntimeMonitor(event)
			s.publish(controlplane.Event{ActionID: "run", Kind: "runtime." + string(event.Kind), Message: event.Status})
		},
	}
}

func (s *ControlService) recordExecutionMonitor(event ExecutionEvent) {
	s.mu.Lock()
	monitor := s.snapshot.Monitor
	switch event.Kind {
	case ExecutionStarted:
		monitor.ActiveSessions++
	case ExecutionCompleted, ExecutionFailed:
		if monitor.ActiveSessions > 0 {
			monitor.ActiveSessions--
		}
		if event.Metrics.UsageKnown {
			monitor.TotalTokens = event.Metrics.TotalTokens
			monitor.TokensKnown = true
		}
		if event.Metrics.CostKnown {
			monitor.CostUSD = event.Metrics.CostUSD
			monitor.CostKnown = true
		}
		if event.Metrics.LatencyKnown {
			monitor.LatencyMs = event.Metrics.LatencyMs
			monitor.LatencyKnown = true
		}
	}
	s.snapshot.Monitor = monitor
	s.mu.Unlock()
}

func (s *ControlService) recordRuntimeMonitor(event execution.RuntimeEvent) {
	s.mu.Lock()
	monitor := s.snapshot.Monitor
	switch event.Kind {
	case execution.RuntimeToolStarted:
		monitor.ActiveTools++
	case execution.RuntimeToolCompleted, execution.RuntimeToolError:
		if monitor.ActiveTools > 0 {
			monitor.ActiveTools--
		}
	case execution.RuntimeApprovalRequested:
		monitor.PendingApprovals++
	case execution.RuntimeApprovalGranted, execution.RuntimeApprovalDenied:
		if monitor.PendingApprovals > 0 {
			monitor.PendingApprovals--
		}
	case execution.RuntimeArtifactCreated:
		count := event.Count
		if count < 1 {
			count = 1
		}
		monitor.Artifacts += count
	}
	s.snapshot.Monitor = monitor
	s.mu.Unlock()
}

func (s *ControlService) publishRouteEvent(event router.ProgressEvent) {
	message := event.Model
	if len(event.Reasons) > 0 {
		message += " · " + strings.Join(event.Reasons, ",")
	}
	if event.Detail != "" {
		message += " · " + strings.Join(strings.Fields(event.Detail), " ")
	}
	s.publish(controlplane.Event{ActionID: "route", Kind: "route." + string(event.Kind), Message: message})
}

func (s *ControlService) publish(event controlplane.Event) {
	if event.Version == 0 {
		event.Version = controlplane.SchemaVersion
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for updates := range s.subs {
		select {
		case updates <- event:
		default:
		}
	}
}

func (s *ControlService) setSnapshot(snapshot controlplane.Snapshot) {
	s.mu.Lock()
	snapshot.Monitor = s.snapshot.Monitor
	if len(snapshot.Providers) == 0 {
		snapshot.Providers = s.snapshot.Providers
	}
	if len(snapshot.Models) == 0 {
		snapshot.Models = s.snapshot.Models
	}
	if len(snapshot.Plans) == 0 {
		snapshot.Plans = s.snapshot.Plans
	}
	s.snapshot = snapshot
	s.mu.Unlock()
}

type outputWriter struct {
	s        *ControlService
	actionID string
}

func (w outputWriter) Write(data []byte) (int, error) {
	if len(data) > 0 {
		w.s.publish(controlplane.Event{ActionID: w.actionID, Kind: "output", Message: string(data)})
	}
	return len(data), nil
}

var _ io.Writer = outputWriter{}
var _ controlplane.Service = (*ControlService)(nil)
