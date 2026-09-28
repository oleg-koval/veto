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
	"sync"
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
	privateWitness    PrivateRouteWitnessRecorder
	timeout           time.Duration
	authorityStrategy string
	shadowStrategy    string
	candidateSecret   []byte
	candidateMu       sync.Mutex
	candidateKeys     map[string]map[string]string
	candidateKeyOrder []string
	now               func() time.Time
}

// SetPrivateWitnessRecorder opts this engine into emitting private route-time
// bindings. Configure it before concurrent Decide calls. Production composition
// does not install this recorder.
func (e *ShadowingDecisionEngine) SetPrivateWitnessRecorder(recorder PrivateRouteWitnessRecorder) {
	if e != nil {
		e.privateWitness = recorder
	}
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

// record converts authority and shadow outcomes to evidence with opaque candidate keys.
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
	candidateKeys := make(map[string]string, len(request.Candidates))
	comparison.Candidates = make([]string, 0, len(request.Candidates))
	for _, candidate := range request.Candidates {
		key := e.candidateKeyForCandidate(comparison.RouteID, candidate)
		comparison.Candidates = append(comparison.Candidates, key)
		candidateKeys[candidate.Model.Name] = key
	}
	comparison.Authority.SelectedCandidate = candidateKeys[comparison.Authority.SelectedCandidate]
	comparison.Shadow.SelectedCandidate = candidateKeys[comparison.Shadow.SelectedCandidate]
	e.rememberCandidateKeys(comparison.RouteID, candidateKeys)
	safeRecordComparison(e.recorder, comparison)
	if e.privateWitness != nil {
		routeKey := e.privateRouteKey(comparison.RouteID)
		fingerprint, _ := PrivateTaskFingerprint(routeKey, request.Task)
		witness := PrivateRouteWitness{
			Version: PrivateWitnessVersion, RouteID: comparison.RouteID,
			ObservedAt: comparison.ObservedAt, TaskKind: request.Task.Kind,
			Risk: request.Task.Risk, RouteKey: routeKey, TaskFingerprint: fingerprint,
			Bindings: make([]PrivateCandidateBinding, 0, len(request.Candidates)),
		}
		for _, candidate := range request.Candidates {
			witness.Bindings = append(witness.Bindings, PrivateCandidateBinding{
				Key: candidateKeys[candidate.Model.Name], Model: candidate.Model.Name,
				Identity: candidate.Model.Identity(), Tools: cloneToolCapabilities(candidate.Tools),
			})
		}
		safeRecordPrivateWitness(e.privateWitness, witness)
	}
}

func safeRecordPrivateWitness(recorder PrivateRouteWitnessRecorder, witness PrivateRouteWitness) {
	defer func() { _ = recover() }()
	_ = recorder.RecordPrivateRouteWitness(witness)
}

// authorityEvidence converts the authoritative outcome to evidence with a fixed error code on failure.
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

// shadowEvidence validates predictions and maps shadow failures to bounded statuses and error codes.
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

// validate checks measurements and requires any selection to belong to the offered shortlist.
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

// newShadowCandidateSecret returns a random HMAC key, or nil if entropy is unavailable.
func newShadowCandidateSecret() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil
	}
	return secret
}

// candidateKey returns a previously captured route-scoped identifier for later execution labels.
func (e *ShadowingDecisionEngine) candidateKey(routeID, modelName string) string {
	e.candidateMu.Lock()
	defer e.candidateMu.Unlock()
	return e.candidateKeys[routeID][modelName]
}

func (e *ShadowingDecisionEngine) candidateKeyForCandidate(routeID string, candidate DecisionCandidate) string {
	modelName := candidate.Model.Name
	if modelName == "" {
		return ""
	}
	key, _ := PrivateCandidateKey(e.privateRouteKey(routeID), modelName, candidate.Model.Identity(), candidate.Tools)
	return key
}

func (e *ShadowingDecisionEngine) rememberCandidateKeys(routeID string, keys map[string]string) {
	if routeID == "" {
		return
	}
	e.candidateMu.Lock()
	defer e.candidateMu.Unlock()
	if e.candidateKeys == nil {
		e.candidateKeys = make(map[string]map[string]string)
	}
	if _, exists := e.candidateKeys[routeID]; !exists {
		e.candidateKeyOrder = append(e.candidateKeyOrder, routeID)
	}
	copy := make(map[string]string, len(keys))
	for name, key := range keys {
		copy[name] = key
	}
	e.candidateKeys[routeID] = copy
	const maxCandidateRoutes = 256
	for len(e.candidateKeyOrder) > maxCandidateRoutes {
		oldest := e.candidateKeyOrder[0]
		e.candidateKeyOrder = e.candidateKeyOrder[1:]
		delete(e.candidateKeys, oldest)
	}
}

func cloneToolCapabilities(tools ToolCapabilities) ToolCapabilities {
	return ToolCapabilities{Tools: append([]string(nil), tools.Tools...), Known: tools.Known}
}

func (e *ShadowingDecisionEngine) privateRouteKey(routeID string) string {
	if routeID == "" || len(e.candidateSecret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, e.candidateSecret)
	_, _ = mac.Write([]byte("veto-private-route-key-v1\x00" + routeID))
	return hex.EncodeToString(mac.Sum(nil))
}

// safeRecordComparison records a comparison while discarding recorder errors and recovering panics.
func safeRecordComparison(recorder ShadowEvidenceRecorder, comparison ShadowComparisonRecord) {
	defer func() { _ = recover() }()
	_ = recorder.RecordShadowComparison(comparison)
}

// safeRecordExecution records an execution label while discarding recorder errors and recovering panics.
func safeRecordExecution(recorder ShadowEvidenceRecorder, label ShadowExecutionLabelRecord) {
	defer func() { _ = recover() }()
	if label.RouteID == "" || label.Candidate == "" {
		return
	}
	_ = recorder.RecordShadowExecutionLabel(label)
}

// executionLabelRecord maps execution metrics to a label, leaving unrecognized completion statuses unknown.
func executionLabelRecord(routeID, modelName, candidate string, metrics ExecutionMetrics, observedAt time.Time) ShadowExecutionLabelRecord {
	success := ShadowKnownBool{}
	switch metrics.Status {
	case "success":
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

// shadowExecutionKey hashes task identity and model name for in-memory execution correlation.
func shadowExecutionKey(task TaskSpec, modelName string) string {
	value := task.ID
	if value == "" {
		value = strings.Join([]string{task.Objective, string(task.Kind), string(task.Risk)}, "\x00")
	}
	sum := sha256.Sum256([]byte(value + "\x00" + modelName))
	return hex.EncodeToString(sum[:])
}
