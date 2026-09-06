// Package routinghistory provides the filesystem adapter for routing history.
//
// The router package owns the Store and KindAwareStore policy contracts and
// MemoryStore implementation. This package owns the volatile JSON/filesystem
// detail used by the CLI. The former router.NewFileStore API was intentionally
// removed because Go cannot provide an adapter alias without introducing an
// import cycle; CLI callers should construct NewFileStore here instead.
package routinghistory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/oleg-koval/veto/pkg/router"
)

// persistedEvent is the stable JSON representation used by routing history
// files. Keep this schema compatible with files written by earlier releases.
type persistedEvent struct {
	Kind         string          `json:"kind"`
	TaskID       string          `json:"task_id"`
	ModelName    string          `json:"model"`
	TaskKind     router.TaskKind `json:"task_kind,omitempty"`
	Accepted     bool            `json:"accepted"`
	Score        float64         `json:"score,omitempty"`
	ScoreKnown   *bool           `json:"score_known,omitempty"`
	Status       string          `json:"status,omitempty"`
	InputTokens  int             `json:"input_tokens,omitempty"`
	OutputTokens int             `json:"output_tokens,omitempty"`
	TotalTokens  int             `json:"total_tokens,omitempty"`
	UsageKnown   bool            `json:"usage_known,omitempty"`
	CostUSD      float64         `json:"cost_usd,omitempty"`
	CostKnown    bool            `json:"cost_known,omitempty"`
	LatencyMs    int64           `json:"latency_ms,omitempty"`
	LatencyKnown bool            `json:"latency_known,omitempty"`
}

// FileStore is a persistent router.Store backed by a JSON file. Routing
// signals are calculated by the inner router.MemoryStore; this adapter only
// translates calls to and from the filesystem representation.
type FileStore struct {
	mu     sync.RWMutex
	store  *router.MemoryStore
	path   string
	events []persistedEvent
}

const maxHistoryEvents = 2000

// NewFileStore returns a store backed by path, loading any existing history.
// A missing or corrupt file yields an empty store: history is best-effort and
// never a reason to fail a route.
func NewFileStore(path string) *FileStore {
	s := &FileStore{store: router.NewMemoryStore(), path: path}
	s.load()
	return s
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
	if len(persisted) > maxHistoryEvents {
		persisted = persisted[len(persisted)-maxHistoryEvents:]
	}

	for i := range persisted {
		if persisted[i].ScoreKnown == nil {
			// Old records did not distinguish transport completion from task
			// success. Keep them readable, but do not let them train the policy.
			persisted[i].ScoreKnown = boolPtr(false)
			if persisted[i].Kind == "result" && persisted[i].Status == "success" {
				persisted[i].Status = "completed"
			}
		}
		s.apply(persisted[i])
	}
	s.mu.Lock()
	s.events = persisted
	s.mu.Unlock()
}

func (s *FileStore) apply(event persistedEvent) {
	if event.Kind == "decision" {
		s.store.LogDecisionForKind(event.TaskID, event.ModelName, event.TaskKind, router.AdmissionDecision{
			Accept: event.Accepted,
		})
		return
	}

	scoreKnown := event.ScoreKnown != nil && *event.ScoreKnown
	s.store.RecordExecution(event.TaskID, event.ModelName, event.TaskKind, router.ExecutionMetrics{
		Status:       event.Status,
		Score:        event.Score,
		ScoreKnown:   scoreKnown,
		InputTokens:  event.InputTokens,
		OutputTokens: event.OutputTokens,
		TotalTokens:  event.TotalTokens,
		UsageKnown:   event.UsageKnown,
		CostUSD:      event.CostUSD,
		CostKnown:    event.CostKnown,
		LatencyMs:    event.LatencyMs,
		LatencyKnown: event.LatencyKnown,
	})
}

// LogDecision records an unscoped admission decision.
func (s *FileStore) LogDecision(taskID, modelName string, d router.AdmissionDecision) {
	s.LogDecisionForKind(taskID, modelName, "", d)
}

// LogDecisionForKind records a task-kind-scoped admission decision.
func (s *FileStore) LogDecisionForKind(taskID, modelName string, kind router.TaskKind, d router.AdmissionDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.LogDecisionForKind(taskID, modelName, kind, d)
	s.events = append(s.events, persistedEvent{
		Kind: "decision", TaskID: taskID, ModelName: modelName,
		TaskKind: kind, Accepted: d.Accept,
	})
	s.trimAndRebuild()
}

// LogResult records an unscoped execution result.
func (s *FileStore) LogResult(taskID, modelName string, score float64, status string) {
	s.RecordExecution(taskID, modelName, "", router.ExecutionMetrics{
		Score: score, ScoreKnown: status == "success", Status: status,
	})
}

// RecordExecution records an execution outcome and provider telemetry.
func (s *FileStore) RecordExecution(taskID, modelName string, kind router.TaskKind, metrics router.ExecutionMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.RecordExecution(taskID, modelName, kind, metrics)
	s.events = append(s.events, persistedEvent{
		Kind: "result", TaskID: taskID, ModelName: modelName,
		TaskKind: kind, Score: metrics.Score, ScoreKnown: boolPtr(metrics.ScoreKnown),
		Status: metrics.Status, InputTokens: metrics.InputTokens,
		OutputTokens: metrics.OutputTokens, TotalTokens: metrics.TotalTokens,
		UsageKnown: metrics.UsageKnown, CostUSD: metrics.CostUSD,
		CostKnown: metrics.CostKnown, LatencyMs: metrics.LatencyMs,
		LatencyKnown: metrics.LatencyKnown,
	})
	s.trimAndRebuild()
}

func (s *FileStore) trimAndRebuild() {
	if len(s.events) <= maxHistoryEvents {
		return
	}
	s.events = append([]persistedEvent(nil), s.events[len(s.events)-maxHistoryEvents:]...)
	s.store = router.NewMemoryStore()
	for _, event := range s.events {
		s.apply(event)
	}
}

// Signal returns the aggregate signal maintained by the inner policy store.
func (s *FileStore) Signal(modelName string, kind router.TaskKind) router.RoutingSignal {
	return s.store.Signal(modelName, kind)
}

// Save writes accumulated history to disk. Call after a route completes.
func (s *FileStore) Save() error {
	s.mu.RLock()
	persisted := make([]persistedEvent, len(s.events))
	copy(persisted, s.events)
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".history-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace routing history: %w", err)
	}
	return nil
}

func boolPtr(value bool) *bool { return &value }

var _ router.Store = (*FileStore)(nil)
var _ router.KindAwareStore = (*FileStore)(nil)
