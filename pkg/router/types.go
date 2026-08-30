// Package router provides model routing with self-admitting receivers.
// Each candidate model must explicitly accept or reject a task before execution.
package router

// TaskKind classifies the type of work in a task.
type TaskKind string

const (
	KindExtract    TaskKind = "extract"
	KindSummarize  TaskKind = "summarize"
	KindCodeChange TaskKind = "code-change"
	KindDebug      TaskKind = "debug"
	KindPlan       TaskKind = "plan"
	KindReview     TaskKind = "review"
	KindRefactor   TaskKind = "refactor"
)

// Risk classifies the potential impact of a task.
type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// Complexity classifies how demanding a task is, used to enforce tier constraints.
type Complexity string

const (
	ComplexitySimple   Complexity = "simple"   // any tier
	ComplexityModerate Complexity = "moderate" // mid or large
	ComplexityComplex  Complexity = "complex"  // large only
)

// reason codes for admission decisions — machine-readable, not prose.
const (
	ReasonMissingTool       = "MISSING_REQUIRED_TOOL"
	ReasonContextTooLarge   = "CONTEXT_TOO_LARGE"
	ReasonCostCeiling       = "COST_CEILING_EXCEEDED"
	ReasonComplexityCeiling = "COMPLEXITY_TOO_HIGH"
	ReasonWeakKind          = "TASK_KIND_OUTSIDE_STRENGTHS"
	ReasonRiskTooHigh       = "RISK_TOO_HIGH"
	ReasonPolicyExcluded    = "USER_POLICY_EXCLUDED"
	ReasonParseFailure      = "PARSE_FAILURE"
	ReasonLowConfidence     = "LOW_CONFIDENCE"
)

// TaskSpec describes a unit of work to be routed and executed.
type TaskSpec struct {
	ID              string
	Kind            TaskKind
	Complexity      Complexity // "" = inferred by Manager from Objective+Kind
	Objective       string
	Context         string
	Constraints     []string
	RequiredTools   []string
	SuccessCriteria []string
	Risk            Risk
	MaxCostUSD      float64  // 0 = no limit
	MaxTokens       int      // 0 = no limit
	Source          string   // "user" | "cron" | "webhook" | "system"
	SkipModels      []string // resume: models already decided in a prior interrupted run
}

// ModelCapabilities describes what a model can and cannot handle.
type ModelCapabilities struct {
	Name                   string
	Source                 string
	Provider               string
	APIModel               string
	Runtime                string
	Tier                   string   // "small" | "mid" | "large"; empty = unknown
	MaxContextTokens       int      // 0 = unknown
	SupportsTools          []string // nil = unknown; empty = known unsupported
	InputModalities        []string // nil = unknown; empty = known unsupported
	OutputModalities       []string // nil = unknown; empty = known unsupported
	SupportedParameters    []string // nil = unknown; empty = known unsupported
	CostPer1kInputUSD      float64
	CostPer1kOutputUSD     float64
	CostPer1kInputUnknown  bool // preserves unknown separately from a known zero price
	CostPer1kOutputUnknown bool // preserves unknown separately from a known zero price
	Strengths              []TaskKind
	Weaknesses             []TaskKind
}

// ModelIdentity identifies a routable model independently from its display
// name. Source describes where metadata came from, Provider owns the model,
// Model is the provider-facing ID, and Runtime is the active execution adapter.
type ModelIdentity struct {
	Source   string
	Provider string
	Model    string
	Runtime  string
}

// Identity returns the stable routing identity for a model binding.
func (m ModelCapabilities) Identity() ModelIdentity {
	model := m.APIModel
	if model == "" {
		model = m.Name
	}
	return ModelIdentity{
		Source:   m.Source,
		Provider: m.Provider,
		Model:    model,
		Runtime:  m.Runtime,
	}
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
	EvalScoreKnown        bool
	RecentCostUSD         float64
	RecentLatencyMs       float64
	AvgInputTokens        float64
	AvgOutputTokens       float64
	AvgTotalTokens        float64
	UsageKnown            bool
	CostKnown             bool
	LatencyKnown          bool
}

// ExecutionMetrics records the outcome and provider telemetry for one model
// execution. Known flags distinguish an observed zero from an unavailable
// measurement (for example, subscription CLIs usually do not expose usage).
type ExecutionMetrics struct {
	Status       string
	Score        float64
	ScoreKnown   bool
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	UsageKnown   bool
	CostUSD      float64
	CostKnown    bool
	LatencyMs    int64
	LatencyKnown bool
}
