package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
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
	runner         Runner
	router         Router
	source         func(context.Context) (controlplane.Snapshot, error)
	handlers       map[string]func(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error)
	reviewer       func(context.Context, router.TaskSpec, string, string) (bool, error)
	outputWriter   func(string, string, bool) error
	skillResolver  func(context.Context, router.TaskSpec) []string
	historySaver   func() error
	routeRecorder  func(router.ProgressEvent)
	routingRefresh func() error

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

// SetSkillResolver supplies the approved skill bodies for TUI executions.
func (s *ControlService) SetSkillResolver(resolver func(context.Context, router.TaskSpec) []string) {
	s.skillResolver = resolver
}

// SetHistorySaver persists routing history after a completed control-plane action.
func (s *ControlService) SetHistorySaver(saver func() error) {
	s.historySaver = saver
}

// SetRouteEventRecorder wires delivery-side persistence for routing events.
func (s *ControlService) SetRouteEventRecorder(recorder func(router.ProgressEvent)) {
	s.routeRecorder = recorder
}

// SetRoutingRefresher updates runtime bindings immediately before a route or
// run so a long-lived TUI sees provider changes made outside the process.
func (s *ControlService) SetRoutingRefresher(refresh func() error) {
	s.routingRefresh = refresh
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
	s.mu.Lock()
	s.handlers[actionID] = handler
	s.mu.Unlock()
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
			kind := controlplane.ModelKindModel
			if model.Runtime == "codex-cli" && (model.APIModel == "" || model.APIModel == "default") {
				kind = controlplane.ModelKindHarness
			}
			pinned := slices.Contains(preferences.PinnedModels, model.Name) || slices.Contains(preferences.PinnedProviders, model.Provider)
			favorite := slices.Contains(preferences.FavoriteModels, model.Name) || slices.Contains(preferences.FavoriteProviders, model.Provider)
			excluded := slices.Contains(preferences.DisabledModels, model.Name) || slices.Contains(preferences.ExcludedModels, model.Name) || slices.Contains(preferences.ExcludedProviders, model.Provider)
			snapshot.Models = append(snapshot.Models, controlplane.ModelSnapshot{
				Name: model.Name, ModelID: model.Identity().Model, Kind: kind, Source: model.Source, Provider: model.Provider, Runtime: model.Runtime, Tier: model.Tier,
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
	if monitorSnapshotPresent(provided.Monitor) {
		base.Monitor = provided.Monitor
	}
	// The composition-root source is authoritative and returns a complete
	// redacted snapshot. Replace collections even when they are empty so a
	// logout, cleanup, or recovered empty state cannot leave stale UI data.
	base.Providers = append([]controlplane.ProviderSnapshot(nil), provided.Providers...)
	base.Models = append([]controlplane.ModelSnapshot(nil), provided.Models...)
	base.History = append([]controlplane.HistorySnapshot(nil), provided.History...)
	base.Plans = append([]controlplane.PlanSnapshot(nil), provided.Plans...)
	base.Health = append([]controlplane.HealthSnapshot(nil), provided.Health...)
	base.Analytics = provided.Analytics
	base.Integrations = append([]controlplane.IntegrationSnapshot(nil), provided.Integrations...)
	return base
}

func monitorSnapshotPresent(m controlplane.MonitorSnapshot) bool {
	return m.ActiveSessions != 0 || m.ActiveTools != 0 || m.PendingApprovals != 0 || m.Artifacts != 0 ||
		m.LastModel != "" || m.LastProvider != "" || m.LastRuntime != "" || m.LastConfidence != 0 || m.LastConfidenceKnown || len(m.LastReasons) > 0 ||
		m.InputTokens != 0 || m.CachedInputTokens != 0 || m.CachedInputKnown || m.OutputTokens != 0 || m.TotalTokens != 0 || m.TokensKnown || m.CostUSD != 0 || m.CostKnown || m.LatencyMs != 0 || m.LatencyKnown
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

func (s *ControlService) Execute(ctx context.Context, request controlplane.ActionRequest) (_ controlplane.ActionResult, executeErr error) {
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
	var requestCtx context.Context
	var cancel context.CancelFunc
	var admissionTimeout time.Duration
	if request.ActionID == "run" && strings.TrimSpace(request.Arguments["timeout"]) != "" {
		rawTimeout := strings.TrimSpace(request.Arguments["timeout"])
		timeout, err := time.ParseDuration(rawTimeout)
		if err != nil || timeout <= 0 {
			return controlplane.ActionResult{}, fmt.Errorf("control plane: invalid timeout %q", rawTimeout)
		}
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		requestCtx, cancel = context.WithCancel(ctx)
	}
	admissionTimeoutValue := strings.TrimSpace(request.Arguments["admission-timeout"])
	if request.ActionID == "route" && admissionTimeoutValue == "" {
		// The TUI's timeout flag has the CLI route command's per-model semantics;
		// it must not become a deadline for the entire routing operation.
		admissionTimeoutValue = strings.TrimSpace(request.Arguments["timeout"])
	}
	if (request.ActionID == "route" || request.ActionID == "run") && admissionTimeoutValue != "" {
		rawAdmissionTimeout := admissionTimeoutValue
		parsedTimeout, err := time.ParseDuration(rawAdmissionTimeout)
		if err != nil || parsedTimeout <= 0 {
			cancel()
			return controlplane.ActionResult{}, fmt.Errorf("control plane: invalid admission-timeout %q", rawAdmissionTimeout)
		}
		admissionTimeout = parsedTimeout
	}
	s.mu.Lock()
	if _, exists := s.active[request.ActionID]; exists {
		s.mu.Unlock()
		cancel()
		return controlplane.ActionResult{}, fmt.Errorf("control plane: action %q is already active", request.ActionID)
	}
	s.active[request.ActionID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, request.ActionID)
		s.mu.Unlock()
		if s.historySaver != nil {
			if err := s.historySaver(); err != nil {
				historyErr := fmt.Errorf("save routing history: %w", err)
				s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
				s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "history.error", Message: strings.Join(strings.Fields(historyErr.Error()), " ")})
				executeErr = errors.Join(executeErr, historyErr)
			}
		}
	}()
	s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "running"})
	if (request.ActionID == "route" || request.ActionID == "run") && s.routingRefresh != nil {
		if err := s.routingRefresh(); err != nil {
			s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
			return controlplane.ActionResult{ActionID: request.ActionID}, fmt.Errorf("refresh routing providers: %w", err)
		}
	}

	switch request.ActionID {
	case "route":
		if s.router == nil {
			return controlplane.ActionResult{}, errors.New("control plane: router is nil")
		}
		task, err := taskFromRequest(request, objective)
		if err != nil {
			s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
			return controlplane.ActionResult{ActionID: request.ActionID}, err
		}
		model, decision, err := routeWithTimeout(s.router, requestCtx, task, admissionTimeout)
		if err != nil {
			s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
			return controlplane.ActionResult{ActionID: request.ActionID}, err
		}
		s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "ready", Provider: model.Provider, Model: model.Name})
		reasons := routeDecisionReasons(decision)
		s.publish(controlplane.Event{ActionID: request.ActionID, Kind: "route.completed", Message: fmt.Sprintf("%s accepted (%.0f%% confidence)", model.Name, decision.Confidence*100), Model: model.Name, Confidence: decision.Confidence, ConfidenceKnown: true, Reasons: reasons})
		return controlplane.ActionResult{ActionID: request.ActionID, Summary: "model selected", Model: model.Name}, nil
	case "run":
		if s.router == nil {
			return controlplane.ActionResult{}, errors.New("control plane: router is nil")
		}
		task, err := taskFromRequest(request, objective)
		if err != nil {
			s.setSnapshot(controlplane.Snapshot{ActiveAction: request.ActionID, Status: "error"})
			return controlplane.ActionResult{ActionID: request.ActionID}, err
		}
		skills := []string(nil)
		if s.skillResolver != nil {
			skills = s.skillResolver(requestCtx, task)
		}
		response, err := s.runner.Execute(requestCtx, Request{Task: task, Skills: skills, AdmissionTimeout: admissionTimeout, Options: executionOptions(request), Writer: outputWriter{s: s, actionID: request.ActionID}})
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
		s.mu.RLock()
		handler, ok := s.handlers[request.ActionID]
		s.mu.RUnlock()
		if ok {
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

func taskFromRequest(request controlplane.ActionRequest, objective string) (router.TaskSpec, error) {
	kind := router.TaskKind(request.Arguments["kind"])
	if kind == "" {
		kind = router.InferKind(objective)
	}
	risk := router.Risk(request.Arguments["risk"])
	if risk == "" {
		risk = router.RiskMedium
	}
	var maxCost float64
	if raw := strings.TrimSpace(request.Arguments["max-cost"]); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed < 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return router.TaskSpec{}, fmt.Errorf("invalid max-cost %q", raw)
		}
		maxCost = parsed
	}
	if raw := request.Arguments["max-output-tokens"]; raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return router.TaskSpec{}, fmt.Errorf("invalid max-output-tokens %q", raw)
		}
	}
	// max-output-tokens is an execution-only budget (see executionOptions); it is
	// validated here but deliberately kept out of TaskSpec so it cannot influence
	// routing/admission decisions.
	return router.TaskSpec{ID: request.Arguments["task-id"], Kind: kind, Objective: objective, Risk: risk, MaxCostUSD: maxCost, RequiredTools: splitRequestList(request.Arguments["required-tools"]), RequiresExecutableTools: request.Arguments["requires-executable-tools"] == "true" || router.RequiresExecutableRuntime(objective), SuccessCriteria: splitRequestList(request.Arguments["criteria"]), RuntimeFilter: request.Arguments["runtime"], ProviderFilter: request.Arguments["provider"], Source: "tui"}, nil
}

func routeWithTimeout(port Router, ctx context.Context, task router.TaskSpec, timeout time.Duration) (router.ModelCapabilities, router.AdmissionDecision, error) {
	if timeout > 0 {
		if timed, ok := port.(timedRouter); ok {
			return timed.RouteWithAdmissionTimeout(ctx, task, timeout)
		}
	}
	return port.Route(ctx, task)
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
			s.publish(controlplane.Event{ActionID: "run", Kind: "execution." + string(event.Kind), Message: executionEventMessage(event), Model: event.Model.Name})
		},
		OnRuntimeEvent: func(taskID string, model router.ModelCapabilities, event execution.RuntimeEvent) {
			if original.OnRuntimeEvent != nil {
				original.OnRuntimeEvent(taskID, model, event)
			}
			s.recordRuntimeMonitor(event)
			s.publish(controlplane.Event{ActionID: "run", Kind: "runtime." + string(event.Kind), Message: runtimeEventMessage(event)})
		},
	}
}

func executionEventMessage(event ExecutionEvent) string {
	parts := make([]string, 0, 4)
	if event.Model.Name != "" {
		parts = append(parts, event.Model.Name)
	}
	if event.Metrics.Status != "" {
		parts = append(parts, event.Metrics.Status)
	}
	if event.Metrics.UsageKnown {
		usage := fmt.Sprintf("%d input + %d output", event.Metrics.InputTokens, event.Metrics.OutputTokens)
		if event.Metrics.CachedInputKnown {
			usage += fmt.Sprintf(" (%d reused)", event.Metrics.CachedInputTokens)
		}
		parts = append(parts, usage)
	}
	if event.Metrics.LatencyKnown {
		parts = append(parts, (time.Duration(event.Metrics.LatencyMs) * time.Millisecond).Round(time.Millisecond).String())
	}
	if detail := strings.Join(strings.Fields(event.Detail), " "); detail != "" {
		parts = append(parts, detail)
	}
	return strings.Join(parts, " · ")
}

func runtimeEventMessage(event execution.RuntimeEvent) string {
	parts := make([]string, 0, 3)
	if event.Name != "" {
		parts = append(parts, event.Name)
	}
	if event.Status != "" {
		parts = append(parts, event.Status)
	}
	if event.Count > 1 {
		parts = append(parts, fmt.Sprintf("%d items", event.Count))
	}
	return strings.Join(parts, " · ")
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
		if event.Model.Name != "" {
			monitor.LastModel = event.Model.Name
			monitor.LastProvider = event.Model.Provider
			monitor.LastRuntime = event.Model.Runtime
		}
		if event.Metrics.UsageKnown {
			monitor.InputTokens = event.Metrics.InputTokens
			monitor.CachedInputTokens = event.Metrics.CachedInputTokens
			monitor.CachedInputKnown = event.Metrics.CachedInputKnown
			monitor.OutputTokens = event.Metrics.OutputTokens
			monitor.TotalTokens = event.Metrics.TotalTokens
			monitor.TokensKnown = true
		} else {
			monitor.InputTokens = 0
			monitor.CachedInputTokens = 0
			monitor.CachedInputKnown = false
			monitor.OutputTokens = 0
			monitor.TotalTokens = 0
			monitor.TokensKnown = false
		}
		if event.Metrics.CostKnown {
			monitor.CostUSD = event.Metrics.CostUSD
			monitor.CostKnown = true
		} else {
			monitor.CostUSD = 0
			monitor.CostKnown = false
		}
		if event.Metrics.LatencyKnown {
			monitor.LatencyMs = event.Metrics.LatencyMs
			monitor.LatencyKnown = true
		} else {
			monitor.LatencyMs = 0
			monitor.LatencyKnown = false
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
	if s.routeRecorder != nil {
		s.routeRecorder(event)
	}
	reasons := append([]string(nil), event.Reasons...)
	if event.Kind == router.EventAskAccept && len(reasons) == 0 {
		reasons = routeDecisionReasons(router.AdmissionDecision{ReasonCodes: reasons})
	}
	message := event.Model
	if len(reasons) > 0 {
		message += " · " + strings.Join(reasons, ",")
	}
	if event.Detail != "" {
		message += " · " + strings.Join(strings.Fields(event.Detail), " ")
	}
	confidenceKnown := event.Kind == router.EventAskAccept || event.Confidence > 0
	s.publish(controlplane.Event{ActionID: "route", Kind: "route." + string(event.Kind), Message: message, Model: event.Model, Confidence: event.Confidence, ConfidenceKnown: confidenceKnown, Reasons: reasons})
	if event.Kind == router.EventAskAccept && event.Model != "" {
		s.mu.Lock()
		s.snapshot.Model = event.Model
		s.snapshot.Monitor.LastModel = event.Model
		s.snapshot.Monitor.LastConfidence = event.Confidence
		s.snapshot.Monitor.LastConfidenceKnown = confidenceKnown
		s.snapshot.Monitor.LastReasons = append([]string(nil), reasons...)
		for _, model := range s.snapshot.Models {
			if model.Name == event.Model {
				s.snapshot.Provider = model.Provider
				s.snapshot.Monitor.LastProvider = model.Provider
				s.snapshot.Monitor.LastRuntime = model.Runtime
				break
			}
		}
		s.mu.Unlock()
	}
}

func routeDecisionReasons(decision router.AdmissionDecision) []string {
	if len(decision.ReasonCodes) > 0 {
		return append([]string(nil), decision.ReasonCodes...)
	}
	return []string{"ranked highest among eligible candidates", "accepted by admission gate"}
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
	if snapshot.Model == "" {
		snapshot.Model = s.snapshot.Model
	}
	if snapshot.Provider == "" {
		snapshot.Provider = s.snapshot.Provider
	}
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
