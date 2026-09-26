package router

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultShadowTimeout = time.Second

// ErrShadowUnavailable classifies missing optional shadow prerequisites.
var ErrShadowUnavailable = errors.New("shadow decider unavailable")

// ErrShadowMalformed classifies an invalid typed shadow response.
var ErrShadowMalformed = errors.New("shadow response malformed")

// ShadowDecider predicts from a bounded request but cannot return the
// authoritative DecisionOutcome consumed by Manager.Route.
type ShadowDecider interface {
	DecideShadow(context.Context, DecisionRequest) (ShadowPrediction, error)
}

// ShadowPrediction is evidence only. It deliberately has no admission payload
// or DecisionMode, so it cannot be mistaken for an authoritative outcome.
type ShadowPrediction struct {
	SelectedCandidate string
	Probability       DecisionProbability
	Confidence        DecisionProbability
	Telemetry         DecisionTelemetry
}

// ShadowRecordStatus is a bounded machine state, never provider detail.
type ShadowRecordStatus string

const (
	ShadowStatusSelected    ShadowRecordStatus = "selected"
	ShadowStatusNoSelection ShadowRecordStatus = "no_selection"
	ShadowStatusError       ShadowRecordStatus = "error"
	ShadowStatusUnavailable ShadowRecordStatus = "unavailable"
	ShadowStatusTimeout     ShadowRecordStatus = "timeout"
	ShadowStatusCanceled    ShadowRecordStatus = "canceled"
	ShadowStatusMalformed   ShadowRecordStatus = "malformed"
)

// ShadowDecisionRecord is the router-owned redacted observation DTO.
type ShadowDecisionRecord struct {
	Status            ShadowRecordStatus
	SelectedCandidate string
	Probability       DecisionProbability
	Confidence        DecisionProbability
	Telemetry         DecisionTelemetry
	ErrorCode         string
}

// ShadowComparisonRecord contains opaque candidate keys only.
type ShadowComparisonRecord struct {
	RouteID           string
	ObservedAt        time.Time
	TaskKind          TaskKind
	Risk              Risk
	Candidates        []string
	AuthorityStrategy string
	ShadowStrategy    string
	Authority         ShadowDecisionRecord
	Shadow            ShadowDecisionRecord
}

// ShadowKnownBool preserves unknown, known false, and known true labels.
type ShadowKnownBool struct {
	Known bool
	Value bool
}

// ShadowExecutionLabelRecord carries later normalized execution evidence.
type ShadowExecutionLabelRecord struct {
	RouteID    string
	ObservedAt time.Time
	Candidate  string
	Success    ShadowKnownBool
	Score      DecisionProbability
	Telemetry  DecisionTelemetry
}

// ShadowEvidenceRecorder is an outer adapter port. Recorder errors and panics
// are non-authoritative and cannot fail a route.
type ShadowEvidenceRecorder interface {
	RecordShadowComparison(ShadowComparisonRecord) error
	RecordShadowExecutionLabel(ShadowExecutionLabelRecord) error
}

// ShadowingDecisionEngine decorates one authority with a bounded observer.
type ShadowingDecisionEngine struct {
	authority         DecisionEngine
	shadow            ShadowDecider
	recorder          ShadowEvidenceRecorder
	timeout           time.Duration
	authorityStrategy string
	shadowStrategy    string
	candidateSecret   []byte
	now               func() time.Time
}

// NewShadowingDecisionEngine constructs an evidence-only observer.
func NewShadowingDecisionEngine(authority DecisionEngine, decider ShadowDecider, recorder ShadowEvidenceRecorder, timeout time.Duration, shadowStrategy string) *ShadowingDecisionEngine {
	if timeout <= 0 {
		timeout = defaultShadowTimeout
	}
	if strings.TrimSpace(shadowStrategy) == "" {
		shadowStrategy = "shadow"
	}
	return &ShadowingDecisionEngine{
		authority: authority, shadow: decider, recorder: recorder, timeout: timeout,
		authorityStrategy: string(DecisionModeSequentialAdmission), shadowStrategy: shadowStrategy,
		candidateSecret: newShadowCandidateSecret(),
		now:             time.Now,
	}
}

type shadowResult struct {
	prediction ShadowPrediction
	err        error
	panicked   bool
}

// Decide returns the authority's value and error unchanged. Shadow work runs
// concurrently and can delay return only until its independent timeout.
func (e *ShadowingDecisionEngine) Decide(ctx context.Context, request DecisionRequest) (DecisionOutcome, error) {
	if e == nil || e.authority == nil {
		return DecisionOutcome{}, errors.New("shadow decision engine: authority is required")
	}
	shadowCtx, cancelShadow := context.WithTimeout(ctx, e.timeout)
	defer cancelShadow()
	resultCh := make(chan shadowResult, 1)
	if e.shadow == nil {
		resultCh <- shadowResult{err: ErrShadowUnavailable}
	} else {
		go func() {
			result := shadowResult{}
			defer func() {
				if recover() != nil {
					result.panicked = true
				}
				resultCh <- result
			}()
			result.prediction, result.err = e.shadow.DecideShadow(shadowCtx, cloneDecisionRequest(request))
		}()
	}

	outcome, authorityErr := e.authority.Decide(ctx, cloneDecisionRequest(request))
	var observed shadowResult
	select {
	case observed = <-resultCh:
	case <-shadowCtx.Done():
		observed.err = shadowCtx.Err()
	}
	e.record(request, outcome, authorityErr, observed)
	return outcome, authorityErr
}

func (e *ShadowingDecisionEngine) record(request DecisionRequest, authority DecisionOutcome, authorityErr error, observed shadowResult) {
	if e.recorder == nil {
		return
	}
	routeID := request.shadowRouteID
	if routeID == "" {
		routeID = newShadowRouteID()
	}
	comparison := ShadowComparisonRecord{
		RouteID: routeID, ObservedAt: e.now().UTC(),
		TaskKind: request.Task.Kind, Risk: request.Task.Risk,
		AuthorityStrategy: e.authorityStrategy, ShadowStrategy: e.shadowStrategy,
		Authority: authorityEvidence(authority, authorityErr), Shadow: shadowEvidence(request, observed),
	}
	comparison.Candidates = make([]string, 0, len(request.Candidates))
	for _, candidate := range request.Candidates {
		comparison.Candidates = append(comparison.Candidates, e.candidateKey(comparison.RouteID, candidate.Model.Name))
	}
	comparison.Authority.SelectedCandidate = e.candidateKey(comparison.RouteID, comparison.Authority.SelectedCandidate)
	comparison.Shadow.SelectedCandidate = e.candidateKey(comparison.RouteID, comparison.Shadow.SelectedCandidate)
	safeRecordComparison(e.recorder, comparison)
}

func authorityEvidence(outcome DecisionOutcome, err error) ShadowDecisionRecord {
	if err != nil {
		return ShadowDecisionRecord{Status: ShadowStatusError, ErrorCode: "AUTHORITY_ERROR"}
	}
	status := ShadowStatusNoSelection
	if outcome.SelectedCandidate != "" {
		status = ShadowStatusSelected
	}
	return ShadowDecisionRecord{
		Status: status, SelectedCandidate: outcome.SelectedCandidate,
		Probability: outcome.Probability, Confidence: outcome.Confidence, Telemetry: outcome.Telemetry,
	}
}

func shadowEvidence(request DecisionRequest, result shadowResult) ShadowDecisionRecord {
	if result.panicked {
		return ShadowDecisionRecord{Status: ShadowStatusError, ErrorCode: "PANIC"}
	}
	if result.err != nil {
		status, code := ShadowStatusError, "DECIDER_ERROR"
		switch {
		case errors.Is(result.err, context.DeadlineExceeded):
			status, code = ShadowStatusTimeout, "TIMEOUT"
		case errors.Is(result.err, context.Canceled):
			status, code = ShadowStatusCanceled, "CANCELED"
		case errors.Is(result.err, ErrShadowMalformed):
			status, code = ShadowStatusMalformed, "MALFORMED"
		case errors.Is(result.err, ErrShadowUnavailable):
			status, code = ShadowStatusUnavailable, "UNAVAILABLE"
		}
		return ShadowDecisionRecord{Status: status, ErrorCode: code, Telemetry: result.prediction.Telemetry}
	}
	if err := result.prediction.validate(request); err != nil {
		return ShadowDecisionRecord{Status: ShadowStatusMalformed, ErrorCode: "MALFORMED", Telemetry: result.prediction.Telemetry}
	}
	status := ShadowStatusNoSelection
	if result.prediction.SelectedCandidate != "" {
		status = ShadowStatusSelected
	}
	return ShadowDecisionRecord{
		Status: status, SelectedCandidate: result.prediction.SelectedCandidate,
		Probability: result.prediction.Probability, Confidence: result.prediction.Confidence, Telemetry: result.prediction.Telemetry,
	}
}

func (p ShadowPrediction) validate(request DecisionRequest) error {
	if err := p.Probability.validate("shadow probability"); err != nil {
		return err
	}
	if err := p.Confidence.validate("shadow confidence"); err != nil {
		return err
	}
	if err := p.Telemetry.validate(); err != nil {
		return err
	}
	if p.SelectedCandidate == "" {
		if p.Probability.Known {
			return errors.New("shadow prediction: probability requires selection")
		}
		return nil
	}
	for _, candidate := range request.Candidates {
		if candidate.Model.Name == p.SelectedCandidate {
			return nil
		}
	}
	return fmt.Errorf("shadow prediction: unknown candidate %q", p.SelectedCandidate)
}

// newShadowRouteID returns a random, non-identifying ID for one routing
// attempt. Entropy failure returns an empty ID; evidence validation then fails
// closed without affecting the authoritative route.
func newShadowRouteID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	return "r-" + hex.EncodeToString(value[:])
}

func newShadowCandidateSecret() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil
	}
	return secret
}

func (e *ShadowingDecisionEngine) candidateKey(routeID, modelName string) string {
	if modelName == "" {
		return ""
	}
	if len(e.candidateSecret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, e.candidateSecret)
	_, _ = mac.Write([]byte(routeID + "\x00" + modelName))
	return "c-" + hex.EncodeToString(mac.Sum(nil)[:16])
}

func safeRecordComparison(recorder ShadowEvidenceRecorder, comparison ShadowComparisonRecord) {
	defer func() { _ = recover() }()
	_ = recorder.RecordShadowComparison(comparison)
}

func safeRecordExecution(recorder ShadowEvidenceRecorder, label ShadowExecutionLabelRecord) {
	defer func() { _ = recover() }()
	_ = recorder.RecordShadowExecutionLabel(label)
}

func executionLabelRecord(routeID, modelName, candidate string, metrics ExecutionMetrics, observedAt time.Time) ShadowExecutionLabelRecord {
	if routeID == "" {
		routeID = newShadowRouteID()
	}
	success := ShadowKnownBool{}
	switch metrics.Status {
	case "success", "completed":
		success = ShadowKnownBool{Known: true, Value: true}
	case "failure", "error", "truncated", "timeout", "canceled":
		success = ShadowKnownBool{Known: true, Value: false}
	}
	return ShadowExecutionLabelRecord{
		RouteID: routeID, ObservedAt: observedAt.UTC(), Candidate: candidate,
		Success: success, Score: DecisionProbability{Known: metrics.ScoreKnown, Value: metrics.Score},
		Telemetry: DecisionTelemetry{
			InputTokens: metrics.InputTokens, OutputTokens: metrics.OutputTokens, TotalTokens: metrics.TotalTokens, UsageKnown: metrics.UsageKnown,
			CostUSD: metrics.CostUSD, CostKnown: metrics.CostKnown, LatencyMs: metrics.LatencyMs, LatencyKnown: metrics.LatencyKnown,
		},
	}
}

func shadowExecutionKey(task TaskSpec, modelName string) string {
	value := task.ID
	if value == "" {
		value = strings.Join([]string{task.Objective, string(task.Kind), string(task.Risk)}, "\x00")
	}
	sum := sha256.Sum256([]byte(value + "\x00" + modelName))
	return hex.EncodeToString(sum[:])
}
