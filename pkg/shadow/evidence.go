// Package shadow defines Veto's redacted, provider-neutral shadow-evaluation
// evidence and its deterministic offline evaluator. It performs no network or
// credential access.
package shadow

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// SchemaVersion is the only evidence schema version understood by this build.
const SchemaVersion = 1

// EventType identifies a payload in the append-only evidence stream.
type EventType string

const (
	EventRouteComparison EventType = "route_comparison"
	EventExecutionLabel  EventType = "execution_label"
)

// DecisionStatus is a bounded machine-readable result, never provider detail.
type DecisionStatus string

const (
	StatusSelected    DecisionStatus = "selected"
	StatusNoSelection DecisionStatus = "no_selection"
	StatusError       DecisionStatus = "error"
	StatusUnavailable DecisionStatus = "unavailable"
	StatusTimeout     DecisionStatus = "timeout"
	StatusCanceled    DecisionStatus = "canceled"
	StatusMalformed   DecisionStatus = "malformed"
)

// KnownFloat preserves unknown, known zero, and known nonzero values.
type KnownFloat struct {
	Known bool    `json:"known"`
	Value float64 `json:"value"`
}

// KnownBool preserves unknown, known false, and known true values.
type KnownBool struct {
	Known bool `json:"known"`
	Value bool `json:"value"`
}

// KnownUsage preserves unknown usage independently from measured zero usage.
type KnownUsage struct {
	Known        bool `json:"known"`
	InputTokens  int  `json:"input_tokens"`
	OutputTokens int  `json:"output_tokens"`
	TotalTokens  int  `json:"total_tokens"`
}

// KnownDuration preserves unknown latency independently from measured zero.
type KnownDuration struct {
	Known  bool  `json:"known"`
	Millis int64 `json:"millis"`
}

// Telemetry contains measurements only. Unknown measurements use canonical
// zero values; in particular, unknown cost is never interpreted as free.
type Telemetry struct {
	Usage   KnownUsage    `json:"usage"`
	CostUSD KnownFloat    `json:"cost_usd"`
	Latency KnownDuration `json:"latency"`
}

// DecisionEvidence is the redacted decision shape used for comparison. A
// candidate key is an opaque local key from the route's bounded shortlist.
type DecisionEvidence struct {
	Status            DecisionStatus `json:"status"`
	SelectedCandidate string         `json:"selected_candidate,omitempty"`
	Probability       KnownFloat     `json:"probability"`
	Confidence        KnownFloat     `json:"confidence"`
	Telemetry         Telemetry      `json:"telemetry"`
	ErrorCode         string         `json:"error_code,omitempty"`
}

// Candidate contains only an opaque key. Model/provider names are deliberately
// absent so fixture data can be shared without exposing account configuration.
type Candidate struct {
	Key string `json:"key"`
}

// RouteComparison records the authoritative and shadow decisions for one
// route. TaskKind and Risk are bounded enums used only for aggregate slices.
type RouteComparison struct {
	RouteID    string           `json:"route_id"`
	ObservedAt time.Time        `json:"observed_at"`
	TaskKind   string           `json:"task_kind"`
	Risk       string           `json:"risk"`
	Candidates []Candidate      `json:"candidates"`
	Authority  DecisionEvidence `json:"authority"`
	Shadow     DecisionEvidence `json:"shadow"`
}

// ExecutionLabel supplies a later outcome for one offered candidate. It can
// represent counterfactual labels as well as the executed authority choice.
type ExecutionLabel struct {
	RouteID    string        `json:"route_id"`
	ObservedAt time.Time     `json:"observed_at"`
	Candidate  string        `json:"candidate"`
	Success    KnownBool     `json:"success"`
	Score      KnownFloat    `json:"score"`
	Usage      KnownUsage    `json:"usage"`
	CostUSD    KnownFloat    `json:"cost_usd"`
	Latency    KnownDuration `json:"latency"`
}

// Event is one schema-versioned JSONL envelope. Exactly one payload must match
// Type. There are intentionally no objective, constraint, response, credential,
// path, or free-form detail fields in this persisted type graph.
type Event struct {
	SchemaVersion int              `json:"schema_version"`
	Type          EventType        `json:"type"`
	Comparison    *RouteComparison `json:"comparison,omitempty"`
	Label         *ExecutionLabel  `json:"label,omitempty"`
}

var (
	opaqueIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	codePattern     = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

var allowedTaskKinds = map[string]bool{
	"extract": true, "summarize": true, "code-change": true, "debug": true,
	"plan": true, "review": true, "refactor": true,
}

var allowedRisks = map[string]bool{"low": true, "medium": true, "high": true}

// Validate checks the complete envelope and rejects non-canonical unknowns.
func (e Event) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("shadow evidence: unsupported schema version %d", e.SchemaVersion)
	}
	switch e.Type {
	case EventRouteComparison:
		if e.Comparison == nil || e.Label != nil {
			return fmt.Errorf("shadow evidence: route_comparison requires only comparison payload")
		}
		return e.Comparison.validate()
	case EventExecutionLabel:
		if e.Label == nil || e.Comparison != nil {
			return fmt.Errorf("shadow evidence: execution_label requires only label payload")
		}
		return e.Label.validate()
	default:
		return fmt.Errorf("shadow evidence: unsupported event type %q", e.Type)
	}
}

func (c RouteComparison) validate() error {
	if err := validateID("route_id", c.RouteID); err != nil {
		return err
	}
	if c.ObservedAt.IsZero() {
		return fmt.Errorf("shadow evidence: observed_at is required")
	}
	if !allowedTaskKinds[c.TaskKind] {
		return fmt.Errorf("shadow evidence: unsupported task_kind %q", c.TaskKind)
	}
	if !allowedRisks[c.Risk] {
		return fmt.Errorf("shadow evidence: unsupported risk %q", c.Risk)
	}
	if len(c.Candidates) == 0 || len(c.Candidates) > 3 {
		return fmt.Errorf("shadow evidence: candidate count must be between 1 and 3")
	}
	keys := make(map[string]bool, len(c.Candidates))
	for _, candidate := range c.Candidates {
		if err := validateID("candidate key", candidate.Key); err != nil {
			return err
		}
		if keys[candidate.Key] {
			return fmt.Errorf("shadow evidence: duplicate candidate %q", candidate.Key)
		}
		keys[candidate.Key] = true
	}
	if err := c.Authority.validate("authority", keys, false); err != nil {
		return err
	}
	return c.Shadow.validate("shadow", keys, true)
}

func (d DecisionEvidence) validate(role string, candidates map[string]bool, shadow bool) error {
	valid := d.Status == StatusSelected || d.Status == StatusNoSelection || d.Status == StatusError
	if shadow {
		valid = valid || d.Status == StatusUnavailable || d.Status == StatusTimeout || d.Status == StatusCanceled || d.Status == StatusMalformed
	}
	if !valid {
		return fmt.Errorf("shadow evidence: unsupported %s status %q", role, d.Status)
	}
	if err := d.Probability.validate(role + " probability"); err != nil {
		return err
	}
	if err := d.Confidence.validate(role + " confidence"); err != nil {
		return err
	}
	if err := d.Telemetry.validate(role); err != nil {
		return err
	}
	if d.Status == StatusSelected {
		if !candidates[d.SelectedCandidate] {
			return fmt.Errorf("shadow evidence: %s selected unknown candidate %q", role, d.SelectedCandidate)
		}
	} else {
		if d.SelectedCandidate != "" {
			return fmt.Errorf("shadow evidence: %s candidate requires selected status", role)
		}
		if d.Probability.Known {
			return fmt.Errorf("shadow evidence: %s probability requires selected status", role)
		}
	}
	isFailure := d.Status != StatusSelected && d.Status != StatusNoSelection
	if isFailure {
		if !codePattern.MatchString(d.ErrorCode) {
			return fmt.Errorf("shadow evidence: %s failure requires a machine error_code", role)
		}
		if d.Confidence.Known {
			return fmt.Errorf("shadow evidence: %s failure cannot have confidence", role)
		}
	} else if d.ErrorCode != "" {
		return fmt.Errorf("shadow evidence: %s successful decision cannot have error_code", role)
	}
	return nil
}

func (l ExecutionLabel) validate() error {
	if err := validateID("route_id", l.RouteID); err != nil {
		return err
	}
	if err := validateID("candidate", l.Candidate); err != nil {
		return err
	}
	if l.ObservedAt.IsZero() {
		return fmt.Errorf("shadow evidence: observed_at is required")
	}
	if !l.Success.Known && l.Success.Value {
		return fmt.Errorf("shadow evidence: unknown success must be false")
	}
	if err := l.Score.validate("score"); err != nil {
		return err
	}
	if err := l.Usage.validate(); err != nil {
		return err
	}
	if err := l.CostUSD.validateNonnegative("cost_usd"); err != nil {
		return err
	}
	return l.Latency.validate()
}

func (v KnownFloat) validate(name string) error {
	if math.IsNaN(v.Value) || math.IsInf(v.Value, 0) || v.Value < 0 || v.Value > 1 {
		return fmt.Errorf("shadow evidence: %s must be finite and between 0 and 1", name)
	}
	if !v.Known && v.Value != 0 {
		return fmt.Errorf("shadow evidence: unknown %s must be zero", name)
	}
	return nil
}

func (v KnownFloat) validateNonnegative(name string) error {
	if math.IsNaN(v.Value) || math.IsInf(v.Value, 0) || v.Value < 0 {
		return fmt.Errorf("shadow evidence: %s must be finite and nonnegative", name)
	}
	if !v.Known && v.Value != 0 {
		return fmt.Errorf("shadow evidence: unknown %s must be zero", name)
	}
	return nil
}

func (u KnownUsage) validate() error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 {
		return fmt.Errorf("shadow evidence: usage must be nonnegative")
	}
	if !u.Known && (u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0) {
		return fmt.Errorf("shadow evidence: unknown usage must be zero")
	}
	return nil
}

func (d KnownDuration) validate() error {
	if d.Millis < 0 {
		return fmt.Errorf("shadow evidence: latency must be nonnegative")
	}
	if !d.Known && d.Millis != 0 {
		return fmt.Errorf("shadow evidence: unknown latency must be zero")
	}
	return nil
}

func (t Telemetry) validate(role string) error {
	if err := t.Usage.validate(); err != nil {
		return fmt.Errorf("shadow evidence: %s telemetry: %w", role, err)
	}
	if err := t.CostUSD.validateNonnegative("cost_usd"); err != nil {
		return fmt.Errorf("shadow evidence: %s telemetry: %w", role, err)
	}
	if err := t.Latency.validate(); err != nil {
		return fmt.Errorf("shadow evidence: %s telemetry: %w", role, err)
	}
	return nil
}

func validateID(name, value string) error {
	if !opaqueIDPattern.MatchString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("shadow evidence: invalid %s", name)
	}
	return nil
}
