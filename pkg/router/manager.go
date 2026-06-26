package router

import (
	"context"
	"errors"
	"fmt"
)

// Manager orchestrates hard-filtering, scoring, and admission gating.
// Route returns the first candidate that passes the admission gate.
type Manager struct {
	registry   *Registry
	gate       *AdmissionGate
	store      Store
	maxRetries int          // ponytail: simple cap, no backoff — add if needed
	OnEvent    func(ProgressEvent) // nil = no-op; wire a Renderer or logger here
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
	all := m.registry.All()
	ranked := RankCandidates(task, all, m.registry)

	// emit per-model filter events so the CLI can show what was pruned and why
	passSet := make(map[string]bool, len(ranked))
	for _, c := range ranked {
		passSet[c.Name] = true
	}
	for _, c := range all {
		if passSet[c.Name] {
			m.emit(ProgressEvent{Kind: EventFilterPass, Model: c.Name})
		} else {
			m.emit(ProgressEvent{Kind: EventFilterFail, Model: c.Name,
				Reasons: []string{FilterReason(c, task)}})
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

	// when resuming we try all remaining candidates; fresh runs are capped at maxRetries
	limit := len(ranked)
	if len(task.SkipModels) == 0 {
		limit = min(m.maxRetries, len(ranked))
	}

	for _, model := range ranked[:limit] {
		if skipSet[model.Name] {
			continue
		}
		m.emit(ProgressEvent{Kind: EventAskStart, Model: model.Name})

		decision, err := m.gate.Ask(ctx, task, model)
		if err != nil {
			if ctx.Err() != nil {
				return ModelCapabilities{}, AdmissionDecision{}, fmt.Errorf("routing: %w", ctx.Err())
			}
			// exec failure (not cancellation) — log and skip, do not abort
			m.store.LogDecision(task.ID, model.Name, AdmissionDecision{
				Accept:      false,
				ReasonCodes: []string{ReasonParseFailure},
			})
			m.emit(ProgressEvent{Kind: EventAskError, Model: model.Name,
				Reasons: []string{ReasonParseFailure}})
			continue
		}
		m.store.LogDecision(task.ID, model.Name, decision)
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

func (m *Manager) emit(e ProgressEvent) {
	if m.OnEvent != nil {
		m.OnEvent(e)
	}
}

// ErrNoCandidate is returned when no model accepts the task.
var ErrNoCandidate = errors.New("no candidate model accepted the task")
