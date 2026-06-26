// Package router provides model routing with self-admitting receivers.
// Each candidate model must explicitly accept or reject a task before execution.
package router

// TaskKind classifies the type of work in a task.
type TaskKind string

const (
	KindExtract   TaskKind = "extract"
	KindSummarize TaskKind = "summarize"
	KindCodeChange TaskKind = "code-change"
	KindDebug     TaskKind = "debug"
	KindPlan      TaskKind = "plan"
	KindReview    TaskKind = "review"
	KindRefactor  TaskKind = "refactor"
)

// Risk classifies the potential impact of a task.
type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// reason codes for admission decisions — machine-readable, not prose.
const (
	ReasonMissingTool       = "MISSING_REQUIRED_TOOL"
	ReasonContextTooLarge   = "CONTEXT_TOO_LARGE"
	ReasonCostCeiling       = "COST_CEILING_EXCEEDED"
	ReasonWeakKind          = "TASK_KIND_OUTSIDE_STRENGTHS"
	ReasonRiskTooHigh       = "RISK_TOO_HIGH"
	ReasonParseFailure      = "PARSE_FAILURE"
	ReasonLowConfidence     = "LOW_CONFIDENCE"
)

// TaskSpec describes a unit of work to be routed and executed.
type TaskSpec struct {
	ID              string
	Kind            TaskKind
	Objective       string
	Context         string
	Constraints     []string
	RequiredTools   []string
	SuccessCriteria []string
	Risk            Risk
	MaxCostUSD      float64 // 0 = no limit
	MaxTokens       int     // 0 = no limit
	Source          string  // "user" | "cron" | "webhook" | "system"
}

// ModelCapabilities describes what a model can and cannot handle.
type ModelCapabilities struct {
	Name                string
	Tier                string // "small" | "mid" | "large"
	MaxContextTokens    int
	SupportsTools       []string
	CostPer1kInputUSD   float64
	CostPer1kOutputUSD  float64
	Strengths           []TaskKind
	Weaknesses          []TaskKind
}

// AdmissionDecision is the structured response a model returns when asked
// whether it accepts a task. ReasonCodes must always be populated on rejection.
type AdmissionDecision struct {
	Accept                    bool
	Confidence                float64
	ReasonCodes               []string
	EstimatedTokens           int
	EstimatedCostUSD          float64
	SuggestedAlternativeModel string
	RequiredTaskChanges       []string
}

// RoutingSignal holds historical performance data for a model+kind pair,
// used to score candidates after hard filtering.
type RoutingSignal struct {
	HistoricalSuccessRate float64
	HistoricalRejectRate  float64
	AvgEvalScore          float64
	RecentCostUSD         float64
	RecentLatencyMs       float64
}
