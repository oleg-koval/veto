// Package eval provides deterministic, offline replay evaluation for routing
// policies. It never creates providers or makes network requests.
package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/oleg-koval/veto/pkg/router"
)

const corpusVersion = 1

// Corpus is a recorded set of routing tasks and model outcomes.
type Corpus struct {
	Version int            `json:"version"`
	Models  []ModelFixture `json:"models"`
	Tasks   []TaskFixture  `json:"tasks"`
}

// ModelFixture describes the capabilities and prices used by replay.
type ModelFixture struct {
	Name               string            `json:"name"`
	Tier               string            `json:"tier"`
	MaxContextTokens   int               `json:"max_context_tokens"`
	SupportsTools      []string          `json:"supports_tools,omitempty"`
	CostPer1kInputUSD  float64           `json:"cost_per_1k_input_usd"`
	CostPer1kOutputUSD float64           `json:"cost_per_1k_output_usd"`
	Strengths          []router.TaskKind `json:"strengths,omitempty"`
	Weaknesses         []router.TaskKind `json:"weaknesses,omitempty"`
}

// TaskFixture describes one replay task. Missing model outcomes are treated as
// admission rejection, which makes partial recordings safe to evaluate.
type TaskFixture struct {
	ID         string             `json:"id"`
	Kind       router.TaskKind    `json:"kind"`
	Complexity router.Complexity  `json:"complexity"`
	Objective  string             `json:"objective,omitempty"`
	Risk       router.Risk        `json:"risk,omitempty"`
	MaxCostUSD float64            `json:"max_cost_usd,omitempty"`
	MaxTokens  int                `json:"max_tokens,omitempty"`
	Outcomes   map[string]Outcome `json:"outcomes"`
}

// Outcome is the recorded result for one model's admission and execution.
type Outcome struct {
	Accepted        bool    `json:"accepted"`
	Success         bool    `json:"success"`
	Confidence      float64 `json:"confidence,omitempty"`
	ConfidenceKnown bool    `json:"confidence_known,omitempty"`
	Score           float64 `json:"score,omitempty"`
	ScoreKnown      bool    `json:"score_known,omitempty"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	TotalTokens     int     `json:"total_tokens,omitempty"`
	UsageKnown      bool    `json:"usage_known,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
	CostKnown       bool    `json:"cost_known,omitempty"`
	LatencyMs       int64   `json:"latency_ms,omitempty"`
	LatencyKnown    bool    `json:"latency_known,omitempty"`
}

// Selection records the selected model and observed outcome for one task.
type Selection struct {
	TaskID          string  `json:"task_id"`
	Model           string  `json:"model,omitempty"`
	Selected        bool    `json:"selected"`
	Success         bool    `json:"success"`
	Confidence      float64 `json:"confidence,omitempty"`
	ConfidenceKnown bool    `json:"confidence_known,omitempty"`
	Score           float64 `json:"score,omitempty"`
	ScoreKnown      bool    `json:"score_known,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
	CostKnown       bool    `json:"cost_known,omitempty"`
	LatencyMs       int64   `json:"latency_ms,omitempty"`
	LatencyKnown    bool    `json:"latency_known,omitempty"`
	Attempts        int     `json:"admission_attempts"`
}

// PolicyMetrics contains aggregate replay results for one policy.
type PolicyMetrics struct {
	Tasks                int     `json:"tasks"`
	Selected             int     `json:"selected"`
	Unresolved           int     `json:"unresolved"`
	Successes            int     `json:"successes"`
	SuccessRate          float64 `json:"success_rate"`
	AverageScore         float64 `json:"average_score"`
	AverageCostUSD       float64 `json:"average_cost_usd"`
	P95CostUSD           float64 `json:"p95_cost_usd"`
	AverageLatencyMs     float64 `json:"average_latency_ms"`
	P95LatencyMs         float64 `json:"p95_latency_ms"`
	BudgetViolations     int     `json:"budget_violations"`
	AdmissionAttempts    int     `json:"admission_attempts"`
	ConfidenceSamples    int     `json:"confidence_samples"`
	MeanConfidenceError  float64 `json:"mean_confidence_error"`
	ConfidenceBrierScore float64 `json:"confidence_brier_score"`
}

// PolicyReport contains aggregate metrics and per-task selections.
type PolicyReport struct {
	Name       string        `json:"name"`
	Metrics    PolicyMetrics `json:"metrics"`
	Selections []Selection   `json:"selections"`
}

// Report is the complete deterministic corpus replay report.
type Report struct {
	CorpusVersion int            `json:"corpus_version"`
	Policies      []PolicyReport `json:"policies"`
}

// Load parses and validates a corpus from r.
func Load(r io.Reader) (Corpus, error) {
	var corpus Corpus
	if err := json.NewDecoder(r).Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode corpus: %w", err)
	}
	if err := corpus.Validate(); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// LoadFile parses and validates a corpus file.
func LoadFile(path string) (Corpus, error) {
	f, err := os.Open(path)
	if err != nil {
		return Corpus{}, fmt.Errorf("open corpus: %w", err)
	}
	defer f.Close()
	return Load(f)
}

// Validate checks corpus shape and model/task identity uniqueness.
func (c Corpus) Validate() error {
	if c.Version != corpusVersion {
		return fmt.Errorf("unsupported corpus version %d (want %d)", c.Version, corpusVersion)
	}
	if len(c.Models) == 0 {
		return fmt.Errorf("corpus must contain at least one model")
	}
	if len(c.Tasks) == 0 {
		return fmt.Errorf("corpus must contain at least one task")
	}
	modelNames := make(map[string]struct{}, len(c.Models))
	for _, model := range c.Models {
		if model.Name == "" {
			return fmt.Errorf("model name is required")
		}
		if _, exists := modelNames[model.Name]; exists {
			return fmt.Errorf("duplicate model %q", model.Name)
		}
		modelNames[model.Name] = struct{}{}
	}
	taskIDs := make(map[string]struct{}, len(c.Tasks))
	for _, task := range c.Tasks {
		if task.ID == "" {
			return fmt.Errorf("task id is required")
		}
		if _, exists := taskIDs[task.ID]; exists {
			return fmt.Errorf("duplicate task %q", task.ID)
		}
		taskIDs[task.ID] = struct{}{}
		if task.Kind == "" {
			return fmt.Errorf("task %q kind is required", task.ID)
		}
		for modelName := range task.Outcomes {
			if _, exists := modelNames[modelName]; !exists {
				return fmt.Errorf("task %q has outcome for unknown model %q", task.ID, modelName)
			}
		}
	}
	return nil
}

// Evaluate replays all four policies in a fixed order. Each policy receives
// an independent history, so adaptive observations do not leak into baselines.
func Evaluate(corpus Corpus) Report {
	models := modelCapabilities(corpus.Models)
	policies := []struct {
		name string
		rank func(router.TaskSpec, []router.ModelCapabilities, *router.MemoryStore) []router.ModelCapabilities
	}{
		{name: "cheapest", rank: rankCheapest},
		{name: "strongest", rank: rankStrongest},
		{name: "static", rank: rankStatic},
		{name: "adaptive", rank: rankAdaptive},
	}

	report := Report{CorpusVersion: corpus.Version, Policies: make([]PolicyReport, 0, len(policies))}
	for _, policy := range policies {
		report.Policies = append(report.Policies, evaluatePolicy(corpus.Tasks, models, policy.name, policy.rank))
	}
	return report
}

func evaluatePolicy(tasks []TaskFixture, models []router.ModelCapabilities, name string, rank func(router.TaskSpec, []router.ModelCapabilities, *router.MemoryStore) []router.ModelCapabilities) PolicyReport {
	history := router.NewMemoryStore()
	report := PolicyReport{Name: name, Selections: make([]Selection, 0, len(tasks))}
	for _, fixture := range tasks {
		task := fixture.taskSpec()
		ranked := rank(task, models, history)
		selection := Selection{TaskID: fixture.ID}
		for _, model := range ranked {
			selection.Attempts++
			outcome := fixture.Outcomes[model.Name]
			if name == "adaptive" {
				history.LogDecisionForKind(fixture.ID, model.Name, fixture.Kind, router.AdmissionDecision{Accept: outcome.Accepted})
			}
			if !outcome.Accepted {
				continue
			}
			selection.Model = model.Name
			selection.Selected = true
			selection.Success = outcome.Success
			selection.Confidence = outcome.Confidence
			selection.ConfidenceKnown = outcome.ConfidenceKnown
			selection.Score = outcome.Score
			selection.ScoreKnown = outcome.ScoreKnown
			selection.CostUSD = outcome.CostUSD
			selection.CostKnown = outcome.CostKnown
			selection.LatencyMs = outcome.LatencyMs
			selection.LatencyKnown = outcome.LatencyKnown
			if name == "adaptive" {
				history.RecordExecution(fixture.ID, model.Name, fixture.Kind, outcome.metrics())
			}
			break
		}
		report.Selections = append(report.Selections, selection)
	}
	report.Metrics = summarize(tasks, report.Selections)
	return report
}

func rankCheapest(task router.TaskSpec, models []router.ModelCapabilities, _ *router.MemoryStore) []router.ModelCapabilities {
	return sortCandidates(task, models, func(a, b router.ModelCapabilities) bool {
		aCost := router.EstimatedCost(a, task)
		bCost := router.EstimatedCost(b, task)
		if aCost != bCost {
			return aCost < bCost
		}
		return a.Name < b.Name
	})
}

func rankStrongest(task router.TaskSpec, models []router.ModelCapabilities, _ *router.MemoryStore) []router.ModelCapabilities {
	return sortCandidates(task, models, func(a, b router.ModelCapabilities) bool {
		aTier := tierRank(a.Tier)
		bTier := tierRank(b.Tier)
		if aTier != bTier {
			return aTier > bTier
		}
		return a.Name < b.Name
	})
}

func rankStatic(task router.TaskSpec, models []router.ModelCapabilities, _ *router.MemoryStore) []router.ModelCapabilities {
	return router.RankCandidates(task, models, router.NewRegistryFromModels(models))
}

func rankAdaptive(task router.TaskSpec, models []router.ModelCapabilities, history *router.MemoryStore) []router.ModelCapabilities {
	return router.RankCandidates(task, models, history)
}

func sortCandidates(task router.TaskSpec, models []router.ModelCapabilities, less func(router.ModelCapabilities, router.ModelCapabilities) bool) []router.ModelCapabilities {
	candidates := router.HardFilter(task, models)
	sort.SliceStable(candidates, func(i, j int) bool { return less(candidates[i], candidates[j]) })
	return candidates
}

func tierRank(tier string) int {
	switch tier {
	case "large":
		return 3
	case "mid":
		return 2
	default:
		return 1
	}
}

func summarize(tasks []TaskFixture, selections []Selection) PolicyMetrics {
	metrics := PolicyMetrics{Tasks: len(tasks)}
	var scores, costs, latencies, confidenceErrors, confidenceBrier []float64
	for _, selection := range selections {
		metrics.AdmissionAttempts += selection.Attempts
		if !selection.Selected {
			metrics.Unresolved++
			continue
		}
		metrics.Selected++
		if selection.Success {
			metrics.Successes++
		}
		if selection.ScoreKnown {
			scores = append(scores, selection.Score)
		}
		if selection.ConfidenceKnown {
			target := 0.0
			if selection.Success {
				target = 1.0
			}
			confidenceErrors = append(confidenceErrors, abs(selection.Confidence-target))
			confidenceBrier = append(confidenceBrier, (selection.Confidence-target)*(selection.Confidence-target))
		}
		if selection.CostKnown {
			costs = append(costs, selection.CostUSD)
		}
		if selection.LatencyKnown {
			latencies = append(latencies, float64(selection.LatencyMs))
		}
		if task := findTask(tasks, selection.TaskID); task.MaxCostUSD > 0 && selection.CostKnown && selection.CostUSD > task.MaxCostUSD {
			metrics.BudgetViolations++
		}
	}
	metrics.SuccessRate = ratio(metrics.Successes, metrics.Selected)
	metrics.AverageScore = average(scores)
	metrics.AverageCostUSD = average(costs)
	metrics.P95CostUSD = percentile95(costs)
	metrics.AverageLatencyMs = average(latencies)
	metrics.P95LatencyMs = percentile95(latencies)
	metrics.ConfidenceSamples = len(confidenceErrors)
	metrics.MeanConfidenceError = average(confidenceErrors)
	metrics.ConfidenceBrierScore = average(confidenceBrier)
	return metrics
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func findTask(tasks []TaskFixture, id string) TaskFixture {
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	return TaskFixture{}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func percentile95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := (95*len(sorted)+99)/100 - 1 // nearest-rank ceil(0.95*n), zero-based
	return sorted[index]
}

func modelCapabilities(fixtures []ModelFixture) []router.ModelCapabilities {
	models := make([]router.ModelCapabilities, len(fixtures))
	for i, model := range fixtures {
		models[i] = router.ModelCapabilities{
			Name: model.Name, Tier: model.Tier, MaxContextTokens: model.MaxContextTokens,
			SupportsTools: model.SupportsTools, CostPer1kInputUSD: model.CostPer1kInputUSD,
			CostPer1kOutputUSD: model.CostPer1kOutputUSD, Strengths: model.Strengths,
			Weaknesses: model.Weaknesses,
		}
	}
	return models
}

func (t TaskFixture) taskSpec() router.TaskSpec {
	return router.TaskSpec{
		ID: t.ID, Kind: t.Kind, Complexity: t.Complexity, Objective: t.Objective,
		Risk: t.Risk, MaxCostUSD: t.MaxCostUSD, MaxTokens: t.MaxTokens,
	}
}

func (o Outcome) metrics() router.ExecutionMetrics {
	status := "failure"
	if o.Success {
		status = "success"
	}
	return router.ExecutionMetrics{
		Status: status, Score: o.Score, ScoreKnown: o.ScoreKnown,
		InputTokens: o.InputTokens, OutputTokens: o.OutputTokens, TotalTokens: o.TotalTokens,
		UsageKnown: o.UsageKnown, CostUSD: o.CostUSD, CostKnown: o.CostKnown,
		LatencyMs: o.LatencyMs, LatencyKnown: o.LatencyKnown,
	}
}

// WriteJSON writes a stable machine-readable report.
func WriteJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
