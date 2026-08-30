package router

import (
	"context"
	"errors"
	"fmt"
)

// Manager orchestrates hard-filtering, scoring, and admission gating.
// Route returns the first candidate that passes the admission gate.
type Manager struct {
	registry    *Registry
	gate        *AdmissionGate
	store       Store
	maxRetries  int // ponytail: simple cap, no backoff — add if needed
	preferences CandidatePreferences
	OnEvent     func(ProgressEvent) // nil = no-op; wire a Renderer or logger here
}

// SetCandidatePreferences applies user-owned local filtering and ordering.
func (m *Manager) SetCandidatePreferences(preferences CandidatePreferences) {
	m.preferences = preferences
}

// NewManager creates a Manager with a default maxRetries of 3.
func NewManager(registry *Registry, gate *AdmissionGate, store Store) *Manager {
	return &Manager{
		registry:   registry,
		gate:       gate,
		store:      store,
		maxRetries: 3,
	}
}

// Route selects a model for the task.
// It hard-filters, ranks, then asks each candidate in score order.
// The first accepted candidate is returned. All decisions are logged.
// Returns ErrNoCandidate if no model passes the admission gate.
func (m *Manager) Route(ctx context.Context, task TaskSpec) (ModelCapabilities, AdmissionDecision, error) {
	if task.Complexity == "" {
		task.Complexity = InferComplexity(task.Objective, task.Kind)
	}

	all := m.registry.All()
	eligible := m.preferences.Filter(all)
	// Store history is the routing signal source. This is deliberately kept
	// separate from the static registry so persisted outcomes affect later runs.
	ranked := m.preferences.Prioritize(RankCandidates(task, eligible, m.store))

	// emit per-model filter events so the CLI can show what was pruned and why
	passSet := make(map[string]bool, len(ranked))
	for _, c := range ranked {
		passSet[c.Name] = true
	}
	for _, c := range all {
		if passSet[c.Name] {
			m.emit(ProgressEvent{Kind: EventFilterPass, Model: c.Name})
		} else {
			reason := FilterReason(c, task)
			if reason == "" {
				reason = ReasonPolicyExcluded
			}
			m.emit(ProgressEvent{Kind: EventFilterFail, Model: c.Name,
				Reasons: []string{reason}})
		}
	}

	if len(ranked) == 0 {
		return ModelCapabilities{}, AdmissionDecision{}, ErrNoCandidate
	}

	// build skip set for resume: models already tried in a prior interrupted run
	skipSet := make(map[string]bool, len(task.SkipModels))
	for _, name := range task.SkipModels {
		skipSet[name] = true
	}

	// Every admission call consumes the per-run budget, including transport
	// failures. Resume can continue with untried candidates in a later run.
	attempts := 0
	for _, model := range ranked {
		if skipSet[model.Name] {
			continue
		}
		if attempts >= m.maxRetries {
			break
		}
		if ctx.Err() != nil {
			return ModelCapabilities{}, AdmissionDecision{}, fmt.Errorf("routing: %w", ctx.Err())
		}
		m.emit(ProgressEvent{Kind: EventAskStart, Model: model.Name})
		attempts++

		decision, err := m.gate.Ask(ctx, task, model)
		if err != nil {
			if ctx.Err() != nil {
				return ModelCapabilities{}, AdmissionDecision{}, fmt.Errorf("routing: %w", ctx.Err())
			}
			// exec/parse failure — log, show the real error, skip
			m.logDecision(task.ID, model.Name, task.Kind, AdmissionDecision{
				Accept:      false,
				ReasonCodes: []string{ReasonParseFailure},
			})
			m.emit(ProgressEvent{Kind: EventAskError, Model: model.Name,
				Detail: err.Error()})
			continue
		}
		m.logDecision(task.ID, model.Name, task.Kind, decision)
		if decision.Accept {
			m.emit(ProgressEvent{
				Kind:       EventAskAccept,
				Model:      model.Name,
				Confidence: decision.Confidence,
				EstTokens:  decision.EstimatedTokens,
				EstCost:    decision.EstimatedCostUSD,
			})
			return model, decision, nil
		}
		m.emit(ProgressEvent{Kind: EventAskReject, Model: model.Name,
			Reasons: decision.ReasonCodes})
	}
	return ModelCapabilities{}, AdmissionDecision{}, ErrNoCandidate
}

// logDecision preserves the original Store API for third-party stores while
// using task-kind-aware history when the built-in extension is available.
func (m *Manager) logDecision(taskID, modelName string, kind TaskKind, decision AdmissionDecision) {
	if store, ok := m.store.(interface {
		LogDecisionForKind(string, string, TaskKind, AdmissionDecision)
	}); ok {
		store.LogDecisionForKind(taskID, modelName, kind, decision)
		return
	}
	m.store.LogDecision(taskID, modelName, decision)
}

func (m *Manager) emit(e ProgressEvent) {
	if m.OnEvent != nil {
		m.OnEvent(e)
	}
}

// RecordExecution persists an execution outcome when the configured store
// supports kind-aware telemetry, while preserving compatibility with legacy
// Store implementations.
func (m *Manager) RecordExecution(task TaskSpec, modelName string, metrics ExecutionMetrics) {
	if store, ok := m.store.(KindAwareStore); ok {
		store.RecordExecution(task.ID, modelName, task.Kind, metrics)
		return
	}
	score := metrics.Score
	if !metrics.ScoreKnown {
		score = 0
	}
	m.store.LogResult(task.ID, modelName, score, metrics.Status)
}

// ErrNoCandidate is returned when no model accepts the task.
var ErrNoCandidate = errors.New("no candidate model accepted the task")
