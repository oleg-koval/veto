package router

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
)

// DecisionVersion is the supported request and outcome contract version.
const DecisionVersion = 1

// MaxDecisionCandidates matches Veto's three-call admission budget. Callers
// filter, rank, and remove skipped models before bounding the shortlist.
const MaxDecisionCandidates = 3

// DecisionEngine selects from a complete, ordered shortlist. Implementations
// must treat requests as read-only and support concurrent calls. The caller
// validates the request before dispatch and the outcome before using it.
// This contract does not imply parallel admission or authorize task execution.
type DecisionEngine interface {
	Decide(context.Context, DecisionRequest) (DecisionOutcome, error)
}

// DecisionCandidate contains router-owned metadata, never a provider SDK type
// or executor. Model.Name is the exact, case-sensitive selection key; aliases
// sharing a runtime remain distinct candidates. Tools describes the active
// transport, not the model's theoretical SupportsTools capabilities.
type DecisionCandidate struct {
	Model ModelCapabilities
	Tools ToolCapabilities
}

// DecisionRequest is a versioned batch in authoritative preference/rank order.
// The bound applies to candidate count, not task text size. Engines must not
// mutate the task or nested candidate slices, or expand the offered shortlist.
type DecisionRequest struct {
	Version    int
	Task       TaskSpec
	Candidates []DecisionCandidate
	admission  sequentialAdmissionOptions
	// shadowRouteID is an execution-attempt identifier created by Manager only
	// when an evidence recorder is configured. It is deliberately outside the
	// public decision contract and is never sent to an authority or provider.
	shadowRouteID string
}

// Validate checks the contract without modifying or reordering candidates.
func (r DecisionRequest) Validate() error {
	if r.Version != DecisionVersion {
		return fmt.Errorf("decision request: unsupported version %d", r.Version)
	}
	if len(r.Candidates) == 0 || len(r.Candidates) > MaxDecisionCandidates {
		return fmt.Errorf("decision request: candidate count must be between 1 and %d", MaxDecisionCandidates)
	}
	seen := make(map[string]bool, len(r.Candidates))
	for _, candidate := range r.Candidates {
		name := candidate.Model.Name
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("decision request: candidate name is empty")
		}
		if seen[name] {
			return fmt.Errorf("decision request: duplicate candidate %q", name)
		}
		seen[name] = true
	}
	return nil
}

// DecisionMode identifies the selection strategy, independently of providers.
// Only sequential admission is defined for v0.13; new modes require explicit
// future support and do not enable themselves through this contract.
type DecisionMode string

const DecisionModeSequentialAdmission DecisionMode = "sequential-admission"

// DecisionProbability is an estimate in [0,1], not a calibrated guarantee.
// Known=false means unavailable; its canonical Value is zero. Known=true with
// Value=0 is an explicit zero estimate. This is not a distribution over models.
type DecisionProbability struct {
	Value float64
	Known bool
}

// validate rejects nonfinite or out-of-range estimates and nonzero unknown
// values, using label to identify the estimate in errors.
func (p DecisionProbability) validate(label string) error {
	if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || p.Value < 0 || p.Value > 1 {
		return fmt.Errorf("decision outcome: %s must be finite and between 0 and 1", label)
	}
	if !p.Known && p.Value != 0 {
		return fmt.Errorf("decision outcome: unknown %s must have zero value", label)
	}
	return nil
}

// DecisionTelemetry describes measured decision work, not estimated or actual
// task execution. Each Known flag governs its corresponding measurement;
// false means unavailable, never free. UsageKnown governs input/output/total;
// CachedInputKnown is independent. Unknown measurements must have zero values.
// No flag is inferred from a numeric value, including zero.
type DecisionTelemetry struct {
	InputTokens       int
	OutputTokens      int
	TotalTokens       int
	UsageKnown        bool
	CachedInputTokens int
	CachedInputKnown  bool
	CostUSD           float64
	CostKnown         bool
	LatencyMs         int64
	LatencyKnown      bool
}

// validate checks that measurements are nonnegative, cost is finite, and
// unknown measurements have zero values without inferring any known flags.
func (t DecisionTelemetry) validate() error {
	if t.InputTokens < 0 || t.OutputTokens < 0 || t.TotalTokens < 0 || t.CachedInputTokens < 0 || t.LatencyMs < 0 {
		return fmt.Errorf("decision outcome: telemetry tokens and latency must be nonnegative")
	}
	if math.IsNaN(t.CostUSD) || math.IsInf(t.CostUSD, 0) || t.CostUSD < 0 {
		return fmt.Errorf("decision outcome: telemetry cost must be finite and nonnegative")
	}
	if !t.UsageKnown && (t.InputTokens != 0 || t.OutputTokens != 0 || t.TotalTokens != 0) {
		return fmt.Errorf("decision outcome: unknown usage must have zero values")
	}
	if !t.CachedInputKnown && t.CachedInputTokens != 0 {
		return fmt.Errorf("decision outcome: unknown cached input must have zero value")
	}
	if !t.CostKnown && t.CostUSD != 0 {
		return fmt.Errorf("decision outcome: unknown cost must have zero value")
	}
	if !t.LatencyKnown && t.LatencyMs != 0 {
		return fmt.Errorf("decision outcome: unknown latency must have zero value")
	}
	return nil
}

// DecisionReason carries a machine-readable code and optional explanation.
// Codes must be nonblank; existing admission reason codes can be reused.
// Reasons are optional for either selection or no selection. Detail is not safe to log
// without redaction; the contract does not persist reasons automatically.
type DecisionReason struct {
	Code   string
	Detail string
}

// DecisionOutcome returns at most one selection. An empty SelectedCandidate
// means no selection; it is not permission to choose a fallback. Probability
// estimates selected-task success; Confidence expresses certainty in the
// decision (including no selection). Neither implies the other's value.
// Versions apply to outcomes as well as requests so callers fail closed.
type DecisionOutcome struct {
	// Admission optionally preserves the complete selected self-admission response.
	// It must accept and may only accompany a selection.
	Admission         *AdmissionDecision
	Version           int
	SelectedCandidate string
	Probability       DecisionProbability
	Confidence        DecisionProbability
	Telemetry         DecisionTelemetry
	Mode              DecisionMode
	Reasons           []DecisionReason
}

// Validate checks an outcome against the exact request offered to the engine.
// Strategy-specific acceptance thresholds belong to the engine, not this
// provider-neutral structural validation.
func (o DecisionOutcome) Validate(request DecisionRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if o.Version != DecisionVersion {
		return fmt.Errorf("decision outcome: unsupported version %d", o.Version)
	}
	if o.Mode != DecisionModeSequentialAdmission {
		return fmt.Errorf("decision outcome: unsupported mode %q", o.Mode)
	}
	if err := o.Probability.validate("probability"); err != nil {
		return err
	}
	if err := o.Confidence.validate("confidence"); err != nil {
		return err
	}
	if err := o.Telemetry.validate(); err != nil {
		return err
	}
	for _, reason := range o.Reasons {
		if strings.TrimSpace(reason.Code) == "" {
			return fmt.Errorf("decision outcome: reason code is empty")
		}
	}
	if o.Admission != nil && (o.SelectedCandidate == "" || !o.Admission.Accept) {
		return fmt.Errorf("decision outcome: admission requires an accepted selection")
	}
	if o.SelectedCandidate == "" {
		if o.Probability.Known {
			return fmt.Errorf("decision outcome: probability requires a selected candidate")
		}
		return nil
	}
	for _, candidate := range request.Candidates {
		if candidate.Model.Name == o.SelectedCandidate {
			return nil
		}
	}
	return fmt.Errorf("decision outcome: unknown selected candidate %q", o.SelectedCandidate)
}

// cloneDecisionRequest isolates all mutable contract data from the caller.
func cloneDecisionRequest(r DecisionRequest) DecisionRequest {
	r.Task.Constraints = slices.Clone(r.Task.Constraints)
	r.Task.RequiredTools = slices.Clone(r.Task.RequiredTools)
	r.Task.SuccessCriteria = slices.Clone(r.Task.SuccessCriteria)
	r.Task.SkipModels = slices.Clone(r.Task.SkipModels)
	r.Candidates = slices.Clone(r.Candidates)
	for i := range r.Candidates {
		c := &r.Candidates[i]
		c.Tools.Tools = slices.Clone(c.Tools.Tools)
		c.Model.SupportsTools = slices.Clone(c.Model.SupportsTools)
		c.Model.InputModalities = slices.Clone(c.Model.InputModalities)
		c.Model.OutputModalities = slices.Clone(c.Model.OutputModalities)
		c.Model.SupportedParameters = slices.Clone(c.Model.SupportedParameters)
		c.Model.Strengths = slices.Clone(c.Model.Strengths)
		c.Model.Weaknesses = slices.Clone(c.Model.Weaknesses)
	}
	return r
}
