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
	maxRetries int // ponytail: simple cap, no backoff — add if needed
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
	ranked := RankCandidates(task, m.registry.All(), m.registry)
	if len(ranked) == 0 {
		return ModelCapabilities{}, AdmissionDecision{}, ErrNoCandidate
	}

	limit := min(m.maxRetries, len(ranked))

	for _, model := range ranked[:limit] {
		decision, err := m.gate.Ask(ctx, task, model)
		if err != nil {
			if ctx.Err() != nil {
				return ModelCapabilities{}, AdmissionDecision{}, fmt.Errorf("routing: %w", ctx.Err())
			}
			// exec failure (not cancellation) — log and skip, do not abort.
			m.store.LogDecision(task.ID, model.Name, AdmissionDecision{
				Accept:      false,
				ReasonCodes: []string{ReasonParseFailure},
			})
			continue
		}
		m.store.LogDecision(task.ID, model.Name, decision)
		if decision.Accept {
			return model, decision, nil
		}
	}
	return ModelCapabilities{}, AdmissionDecision{}, ErrNoCandidate
}

// ErrNoCandidate is returned when no model accepts the task.
var ErrNoCandidate = errors.New("no candidate model accepted the task")
