package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

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

// persistedEvent is the JSON-serializable form of routingEvent.
// routingEvent has unexported fields; this DTO carries them across save/load.
type persistedEvent struct {
	Kind      string  `json:"kind"`
	TaskID    string  `json:"task_id"`
	ModelName string  `json:"model"`
	Accepted  bool    `json:"accepted"`
	Score     float64 `json:"score,omitempty"`
	Status    string  `json:"status,omitempty"`
}

// FileStore is a MemoryStore that loads from and saves to a JSON file.
// This is what makes routing history compound across runs: each session's
// accept/reject decisions persist and feed the next run's ranking.
type FileStore struct {
	*MemoryStore
	path string
}

// NewFileStore returns a store backed by path, loading any existing history.
// A missing or corrupt file yields an empty store — history is best-effort,
// never a reason to fail a route.
func NewFileStore(path string) *FileStore {
	fs := &FileStore{MemoryStore: NewMemoryStore(), path: path}
	fs.load()
	return fs
}

func (s *FileStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var persisted []persistedEvent
	if err := json.Unmarshal(data, &persisted); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = make([]routingEvent, len(persisted))
	for i, p := range persisted {
		s.events[i] = routingEvent{
			kind: p.Kind, taskID: p.TaskID, modelName: p.ModelName,
			accepted: p.Accepted, score: p.Score, status: p.Status,
		}
	}
}

// Save writes the accumulated history to disk. Call after a route completes.
func (s *FileStore) Save() error {
	s.mu.RLock()
	persisted := make([]persistedEvent, len(s.events))
	for i, e := range s.events {
		persisted[i] = persistedEvent{
			Kind: e.kind, TaskID: e.taskID, ModelName: e.modelName,
			Accepted: e.accepted, Score: e.score, Status: e.status,
		}
	}
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}
