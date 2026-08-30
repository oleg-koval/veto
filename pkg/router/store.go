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

// KindAwareStore extends Store with task-kind-scoped decision logging and execution metrics.
// The legacy Store methods remain available for callers that do not have a
// task kind or execution telemetry yet.
type KindAwareStore interface {
	Store
	LogDecisionForKind(taskID, modelName string, kind TaskKind, d AdmissionDecision)
	RecordExecution(taskID, modelName string, kind TaskKind, metrics ExecutionMetrics)
}

// routingEvent is one recorded outcome stored in MemoryStore.
// kind is "decision" (admit/reject) or "result" (execution outcome).
type routingEvent struct {
	kind         string // "decision" | "result"
	taskID       string
	modelName    string
	taskKind     TaskKind
	accepted     bool
	score        float64
	scoreKnown   bool
	status       string
	inputTokens  int
	outputTokens int
	totalTokens  int
	usageKnown   bool
	costUSD      float64
	costKnown    bool
	latencyMs    int64
	latencyKnown bool
}

type statsKey struct {
	modelName string
	taskKind  TaskKind
}

// modelStats holds incrementally-updated aggregates per model so Signal() is O(1)
// instead of scanning the full event log on every routing call.
type modelStats struct {
	decisionTotal float64
	accepted      float64
	resultTotal   float64
	completed     float64
	scoreSum      float64
	scoreCount    float64
	inputTokens   float64
	outputTokens  float64
	totalTokens   float64
	usageCount    float64
	costSum       float64
	costCount     float64
	latencySum    float64
	latencyCount  float64
}

// MemoryStore is an in-process, non-persistent Store used for MVP and tests.
type MemoryStore struct {
	mu     sync.RWMutex
	events []routingEvent
	stats  map[statsKey]*modelStats // incremental aggregates; keyed by model and task kind
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{stats: make(map[statsKey]*modelStats)}
}

// statFor returns the stats entry for modelName, creating it if absent.
// Must be called with mu held for writing.
func (s *MemoryStore) statFor(model string, kind TaskKind) *modelStats {
	key := statsKey{modelName: model, taskKind: kind}
	st := s.stats[key]
	if st == nil {
		st = &modelStats{}
		s.stats[key] = st
	}
	return st
}

// rebuildStats reconstructs s.stats from s.events in a single pass.
// Must be called with mu held for writing.
func (s *MemoryStore) rebuildStats() {
	s.stats = make(map[statsKey]*modelStats, len(s.events)/3+1)
	for _, e := range s.events {
		s.accumulate(e)
	}
}

// accumulate applies one event to the incremental aggregate. The caller must
// hold s.mu for writing.
func (s *MemoryStore) accumulate(e routingEvent) {
	st := s.statFor(e.modelName, e.taskKind)
	if e.kind == "decision" {
		st.decisionTotal++
		if e.accepted {
			st.accepted++
		}
		return
	}

	st.resultTotal++
	if e.status == "success" {
		st.completed++
	}
	if e.scoreKnown {
		st.scoreCount++
		st.scoreSum += e.score
	}
	if e.usageKnown {
		st.usageCount++
		st.inputTokens += float64(e.inputTokens)
		st.outputTokens += float64(e.outputTokens)
		st.totalTokens += float64(e.totalTokens)
	}
	if e.costKnown {
		st.costCount++
		st.costSum += e.costUSD
	}
	if e.latencyKnown {
		st.latencyCount++
		st.latencySum += float64(e.latencyMs)
	}
}

// LogDecision records accept/reject for a task-model pair.
func (s *MemoryStore) LogDecision(taskID, modelName string, d AdmissionDecision) {
	s.LogDecisionForKind(taskID, modelName, "", d)
}

// LogDecisionForKind records an admission decision scoped to a task kind.
func (s *MemoryStore) LogDecisionForKind(taskID, modelName string, kind TaskKind, d AdmissionDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := routingEvent{
		kind:      "decision",
		taskID:    taskID,
		modelName: modelName,
		taskKind:  kind,
		accepted:  d.Accept,
	}
	s.events = append(s.events, e)
	s.accumulate(e)
}

// LogResult records the execution outcome.
func (s *MemoryStore) LogResult(taskID, modelName string, score float64, status string) {
	s.RecordExecution(taskID, modelName, "", ExecutionMetrics{
		Score:      score,
		ScoreKnown: status == "success",
		Status:     status,
	})
}

// RecordExecution records a task execution and any provider telemetry.
func (s *MemoryStore) RecordExecution(taskID, modelName string, kind TaskKind, metrics ExecutionMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := routingEvent{
		kind:         "result",
		taskID:       taskID,
		modelName:    modelName,
		taskKind:     kind,
		score:        metrics.Score,
		scoreKnown:   metrics.ScoreKnown,
		status:       metrics.Status,
		inputTokens:  metrics.InputTokens,
		outputTokens: metrics.OutputTokens,
		totalTokens:  metrics.TotalTokens,
		usageKnown:   metrics.UsageKnown,
		costUSD:      metrics.CostUSD,
		costKnown:    metrics.CostKnown,
		latencyMs:    metrics.LatencyMs,
		latencyKnown: metrics.LatencyKnown,
	}
	s.events = append(s.events, e)
	s.accumulate(e)
}

// Signal returns aggregated stats for a model — O(1) map lookup.
// Aggregates are maintained incrementally by LogDecision and LogResult.
func (s *MemoryStore) Signal(modelName string, kind TaskKind) RoutingSignal {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st := s.stats[statsKey{modelName: modelName, taskKind: kind}]
	if st == nil {
		// Events written by older versions have no task kind. They remain useful
		// as a model-wide fallback when no kind-specific history exists.
		st = s.stats[statsKey{modelName: modelName}]
	}
	if st == nil || st.decisionTotal+st.resultTotal == 0 {
		return RoutingSignal{
			HistoricalSuccessRate: 0.5,
			HistoricalRejectRate:  0.1,
			AvgEvalScore:          0.7,
		}
	}

	rejectRate := 0.0
	if st.decisionTotal > 0 {
		rejectRate = (st.decisionTotal - st.accepted) / st.decisionTotal
	}
	successRate := 0.0
	if st.resultTotal > 0 {
		successRate = st.completed / st.resultTotal
	}
	// Unknown evaluation scores remain neutral rather than being interpreted as
	// a zero-quality result.
	avgScore := 0.7
	evalScoreKnown := false
	if st.scoreCount > 0 {
		avgScore = st.scoreSum / st.scoreCount
		evalScoreKnown = true
	}

	signal := RoutingSignal{
		HistoricalSuccessRate: successRate,
		HistoricalRejectRate:  rejectRate,
		AvgEvalScore:          avgScore,
		EvalScoreKnown:        evalScoreKnown,
	}
	if st.usageCount > 0 {
		signal.UsageKnown = true
		signal.AvgInputTokens = st.inputTokens / st.usageCount
		signal.AvgOutputTokens = st.outputTokens / st.usageCount
		signal.AvgTotalTokens = st.totalTokens / st.usageCount
	}
	if st.costCount > 0 {
		signal.CostKnown = true
		signal.RecentCostUSD = st.costSum / st.costCount
	}
	if st.latencyCount > 0 {
		signal.LatencyKnown = true
		signal.RecentLatencyMs = st.latencySum / st.latencyCount
	}
	return signal
}

// persistedEvent is the JSON-serializable form of routingEvent.
// routingEvent has unexported fields; this DTO carries them across save/load.
type persistedEvent struct {
	Kind         string   `json:"kind"`
	TaskID       string   `json:"task_id"`
	ModelName    string   `json:"model"`
	TaskKind     TaskKind `json:"task_kind,omitempty"`
	Accepted     bool     `json:"accepted"`
	Score        float64  `json:"score,omitempty"`
	ScoreKnown   *bool    `json:"score_known,omitempty"`
	Status       string   `json:"status,omitempty"`
	InputTokens  int      `json:"input_tokens,omitempty"`
	OutputTokens int      `json:"output_tokens,omitempty"`
	TotalTokens  int      `json:"total_tokens,omitempty"`
	UsageKnown   bool     `json:"usage_known,omitempty"`
	CostUSD      float64  `json:"cost_usd,omitempty"`
	CostKnown    bool     `json:"cost_known,omitempty"`
	LatencyMs    int64    `json:"latency_ms,omitempty"`
	LatencyKnown bool     `json:"latency_known,omitempty"`
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
		scoreKnown := p.ScoreKnown != nil && *p.ScoreKnown
		// Pre-telemetry history implied that a successful result's score was
		// known. Preserve that behavior when loading legacy files.
		if p.ScoreKnown == nil && p.Kind == "result" && p.Status == "success" {
			scoreKnown = true
		}
		s.events[i] = routingEvent{
			kind: p.Kind, taskID: p.TaskID, modelName: p.ModelName,
			taskKind: p.TaskKind, accepted: p.Accepted, score: p.Score,
			scoreKnown: scoreKnown, status: p.Status,
			inputTokens: p.InputTokens, outputTokens: p.OutputTokens,
			totalTokens: p.TotalTokens, usageKnown: p.UsageKnown,
			costUSD: p.CostUSD, costKnown: p.CostKnown,
			latencyMs: p.LatencyMs, latencyKnown: p.LatencyKnown,
		}
	}
	s.rebuildStats()
}

// Save writes the accumulated history to disk. Call after a route completes.
func (s *FileStore) Save() error {
	s.mu.RLock()
	persisted := make([]persistedEvent, len(s.events))
	for i, e := range s.events {
		scoreKnown := e.scoreKnown
		persisted[i] = persistedEvent{
			Kind: e.kind, TaskID: e.taskID, ModelName: e.modelName,
			TaskKind: e.taskKind, Accepted: e.accepted, Score: e.score,
			ScoreKnown: &scoreKnown, Status: e.status,
			InputTokens: e.inputTokens, OutputTokens: e.outputTokens,
			TotalTokens: e.totalTokens, UsageKnown: e.usageKnown,
			CostUSD: e.costUSD, CostKnown: e.costKnown,
			LatencyMs: e.latencyMs, LatencyKnown: e.latencyKnown,
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
