package router

import "sync"

// Store records routing outcomes so the router can improve over time.
type Store interface {
	// LogDecision records the admission decision for a task-model pair.
	LogDecision(taskID, modelName string, d AdmissionDecision)
	// LogResult records execution outcome and eval score for a task-model pair.
	LogResult(taskID, modelName string, score float64, status string)
	// Signal returns aggregated historical data for a model+kind pair.
	// implementations that lack data return a neutral baseline.
	Signal(modelName string, kind TaskKind) RoutingSignal
}

// routingEvent is one recorded outcome stored in MemoryStore.
// kind is "decision" (admit/reject) or "result" (execution outcome).
type routingEvent struct {
	kind      string // "decision" | "result"
	taskID    string
	modelName string
	accepted  bool
	score     float64
	status    string
}

// MemoryStore is an in-process, non-persistent Store used for MVP and tests.
type MemoryStore struct {
	mu     sync.RWMutex
	events []routingEvent
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// LogDecision records accept/reject for a task-model pair.
func (s *MemoryStore) LogDecision(taskID, modelName string, d AdmissionDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, routingEvent{
		kind:      "decision",
		taskID:    taskID,
		modelName: modelName,
		accepted:  d.Accept,
	})
}

// LogResult records the execution outcome.
func (s *MemoryStore) LogResult(taskID, modelName string, score float64, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, routingEvent{
		kind:      "result",
		taskID:    taskID,
		modelName: modelName,
		score:     score,
		status:    status,
	})
}

// Signal returns aggregated stats for a model across all task kinds.
// MVP: ignores kind, aggregates all events for the model.
// decision events drive rejectRate; result events drive successRate and avgScore.
func (s *MemoryStore) Signal(modelName string, _ TaskKind) RoutingSignal {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var decisionTotal, accepted float64
	var resultTotal, completed, scoreSum float64
	for _, e := range s.events {
		if e.modelName != modelName {
			continue
		}
		if e.kind == "decision" {
			decisionTotal++
			if e.accepted {
				accepted++
			}
		} else {
			resultTotal++
			if e.status == "success" {
				completed++
				scoreSum += e.score
			}
		}
	}

	if decisionTotal+resultTotal == 0 {
		// no data yet — return neutral baseline.
		return RoutingSignal{
			HistoricalSuccessRate: 0.5,
			HistoricalRejectRate:  0.1,
			AvgEvalScore:          0.7,
		}
	}

	rejectRate := 0.0
	if decisionTotal > 0 {
		rejectRate = (decisionTotal - accepted) / decisionTotal
	}
	successRate := 0.0
	if resultTotal > 0 {
		successRate = completed / resultTotal
	}
	avgScore := 0.0
	if completed > 0 {
		avgScore = scoreSum / completed
	}

	return RoutingSignal{
		HistoricalSuccessRate: successRate,
		HistoricalRejectRate:  rejectRate,
		AvgEvalScore:          avgScore,
	}
}
