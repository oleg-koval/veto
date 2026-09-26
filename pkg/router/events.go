package router

// EventKind identifies the type of progress event emitted during Manager.Route.
type EventKind string

// EventKind constants define routing pipeline progress event types.
const (
	EventDecisionStarted   EventKind = "decision.started"
	EventDecisionCompleted EventKind = "decision.completed"
	EventDecisionError     EventKind = "decision.error"
	EventFilterPass        EventKind = "filter_pass"
	EventFilterFail        EventKind = "filter_fail"
	EventShortlist         EventKind = "shortlist"
	EventAskStart          EventKind = "ask_start"
	EventAskAccept         EventKind = "ask_accept"
	EventAskReject         EventKind = "ask_reject"
	EventAskError          EventKind = "ask_error"
)

// ProgressEvent is emitted by Manager.Route at each step of the routing pipeline.
// CLI renderers and loggers subscribe via Manager.OnEvent.
type ProgressEvent struct {
	Decision   *DecisionProgress
	Kind       EventKind
	Model      string
	Reasons    []string
	Confidence float64
	EstTokens  int
	EstCost    float64
	Detail     string // human-readable detail, e.g. the underlying error on EventAskError
}

// DecisionProgress is the structural allowlist shared by decision event consumers.
// Pointers preserve measured zero separately from unknown telemetry. No admission
// estimates, reason details, provider payloads, or task content belong here.
type DecisionProgress struct {
	Version           int          `json:"contract_version"`
	Mode              DecisionMode `json:"mode"`
	CandidateCount    int          `json:"candidate_count"`
	Status            string       `json:"status"`
	SelectedModel     string       `json:"selected_model,omitempty"`
	InputTokens       *int         `json:"input_tokens,omitempty"`
	OutputTokens      *int         `json:"output_tokens,omitempty"`
	TotalTokens       *int         `json:"total_tokens,omitempty"`
	CachedInputTokens *int         `json:"cached_input_tokens,omitempty"`
	CostUSD           *float64     `json:"cost_usd,omitempty"`
	LatencyMS         *int64       `json:"latency_ms,omitempty"`
}

// IsDecisionEvent identifies boundary events without interpreting legacy fields.
func IsDecisionEvent(kind EventKind) bool {
	return kind == EventDecisionStarted || kind == EventDecisionCompleted || kind == EventDecisionError
}

// decisionProgress builds the structural boundary payload, including a
// selection and only known telemetry when an outcome is supplied.
func decisionProgress(count int, status string, outcome *DecisionOutcome) *DecisionProgress {
	p := &DecisionProgress{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission, CandidateCount: count, Status: status}
	if outcome == nil {
		return p
	}
	p.SelectedModel = outcome.SelectedCandidate
	t := outcome.Telemetry
	if t.UsageKnown {
		p.InputTokens = &t.InputTokens
		p.OutputTokens = &t.OutputTokens
		p.TotalTokens = &t.TotalTokens
	}
	if t.CachedInputKnown {
		p.CachedInputTokens = &t.CachedInputTokens
	}
	if t.CostKnown {
		p.CostUSD = &t.CostUSD
	}
	if t.LatencyKnown {
		p.LatencyMS = &t.LatencyMs
	}
	return p
}
