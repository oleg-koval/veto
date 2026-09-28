package router

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type privateWitnessSink struct {
	witnesses []PrivateRouteWitness
	err       error
	panic     bool
}

func (s *privateWitnessSink) RecordPrivateRouteWitness(witness PrivateRouteWitness) error {
	if s.panic {
		panic("private witness recorder failed")
	}
	s.witnesses = append(s.witnesses, witness)
	return s.err
}

func TestPrivateWitnessCapturesRouteTimeBindingsWithoutRawTask(t *testing.T) {
	request := shadowRequest()
	request.Task.Objective = "private task text"
	request.Task.SuccessCriteria = []string{"private criterion"}
	request.Task.ExecutionMaxOutputTokens = 1024
	redacted := &recordingShadowSink{}
	private := &privateWitnessSink{}
	engine := NewShadowingDecisionEngine(decisionEngineFunc(func(context.Context, DecisionRequest) (DecisionOutcome, error) {
		return DecisionOutcome{Version: DecisionVersion, SelectedCandidate: "a", Mode: DecisionModeSequentialAdmission}, nil
	}), shadowDeciderFunc(func(context.Context, DecisionRequest) (ShadowPrediction, error) {
		return ShadowPrediction{SelectedCandidate: "b"}, nil
	}), redacted, time.Second, "jev:test")
	engine.SetPrivateWitnessRecorder(private)
	_, err := engine.Decide(t.Context(), request)
	require.NoError(t, err)
	require.Len(t, redacted.comparisons, 1)
	require.Len(t, private.witnesses, 1)
	witness := private.witnesses[0]
	require.Equal(t, redacted.comparisons[0].RouteID, witness.RouteID)
	fingerprint, err := PrivateTaskFingerprint(witness.RouteKey, request.Task)
	require.NoError(t, err)
	require.Equal(t, fingerprint, witness.TaskFingerprint)
	require.Equal(t, []PrivateCandidateBinding{
		{Key: redacted.comparisons[0].Candidates[0], Model: "a"},
		{Key: redacted.comparisons[0].Candidates[1], Model: "b"},
	}, witness.Bindings)
	encoded, err := json.Marshal(witness)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), request.Task.Objective)
	require.NotContains(t, string(encoded), request.Task.SuccessCriteria[0])
	redactedJSON, err := json.Marshal(redacted.comparisons[0])
	require.NoError(t, err)
	require.NotContains(t, string(redactedJSON), "\"Model\"")
}

func TestPrivateWitnessFailureCannotChangeAuthority(t *testing.T) {
	for _, sink := range []*privateWitnessSink{{err: errors.New("private failure")}, {panic: true}} {
		engine := NewShadowingDecisionEngine(decisionEngineFunc(func(context.Context, DecisionRequest) (DecisionOutcome, error) {
			return DecisionOutcome{Version: DecisionVersion, SelectedCandidate: "a", Mode: DecisionModeSequentialAdmission}, nil
		}), nil, &recordingShadowSink{}, time.Second, "jev:test")
		engine.SetPrivateWitnessRecorder(sink)
		outcome, err := engine.Decide(t.Context(), shadowRequest())
		require.NoError(t, err)
		require.Equal(t, "a", outcome.SelectedCandidate)
	}
}

type shadowDeciderFunc func(context.Context, DecisionRequest) (ShadowPrediction, error)

// DecideShadow delegates shadow prediction to the test function.
func (f shadowDeciderFunc) DecideShadow(ctx context.Context, request DecisionRequest) (ShadowPrediction, error) {
	return f(ctx, request)
}

type recordingShadowSink struct {
	mu          sync.Mutex
	comparisons []ShadowComparisonRecord
	labels      []ShadowExecutionLabelRecord
	err         error
	panic       bool
}

// RecordShadowComparison captures comparisons or simulates a configured recorder failure.
func (r *recordingShadowSink) RecordShadowComparison(comparison ShadowComparisonRecord) error {
	if r.panic {
		panic("recorder")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comparisons = append(r.comparisons, comparison)
	return r.err
}

// RecordShadowExecutionLabel captures execution labels or simulates a configured recorder failure.
func (r *recordingShadowSink) RecordShadowExecutionLabel(label ShadowExecutionLabelRecord) error {
	if r.panic {
		panic("recorder")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.labels = append(r.labels, label)
	return r.err
}

// TestShadowingDecisionEngineReturnsAuthorityUnchanged checks that shadow request mutations and predictions cannot alter the authority result.
func TestShadowingDecisionEngineReturnsAuthorityUnchanged(t *testing.T) {
	request := shadowRequest()
	want := DecisionOutcome{Version: DecisionVersion, SelectedCandidate: "a", Mode: DecisionModeSequentialAdmission, Probability: DecisionProbability{Known: true, Value: .8}, Confidence: DecisionProbability{Known: true, Value: .9}}
	recorder := &recordingShadowSink{}
	engine := NewShadowingDecisionEngine(decisionEngineFunc(func(_ context.Context, got DecisionRequest) (DecisionOutcome, error) {
		require.Equal(t, "original", got.Task.Objective)
		return want, nil
	}), shadowDeciderFunc(func(_ context.Context, got DecisionRequest) (ShadowPrediction, error) {
		got.Task.Objective = "mutated"
		got.Candidates[0].Model.Name = "mutated"
		return ShadowPrediction{SelectedCandidate: "b", Probability: DecisionProbability{Known: true, Value: .7}, Confidence: DecisionProbability{Known: true, Value: .8}}, nil
	}), recorder, time.Second, "jev:test")
	engine.now = func() time.Time { return time.Unix(1, 0) }

	got, err := engine.Decide(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Len(t, recorder.comparisons, 1)
	comparison := recorder.comparisons[0]
	require.NotEqual(t, comparison.Authority.SelectedCandidate, comparison.Shadow.SelectedCandidate)
	require.Equal(t, "jev:test", comparison.ShadowStrategy)
}

// TestShadowingDecisionEnginePreservesAuthorityError checks that shadow panics and recorder errors preserve the authority error.
func TestShadowingDecisionEnginePreservesAuthorityError(t *testing.T) {
	wantErr := errors.New("authority failed")
	recorder := &recordingShadowSink{err: errors.New("disk full")}
	engine := NewShadowingDecisionEngine(decisionEngineFunc(func(context.Context, DecisionRequest) (DecisionOutcome, error) {
		return DecisionOutcome{Version: 99}, wantErr
	}), shadowDeciderFunc(func(context.Context, DecisionRequest) (ShadowPrediction, error) {
		panic("shadow panic")
	}), recorder, time.Second, "jev:test")
	_, err := engine.Decide(t.Context(), shadowRequest())
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, ShadowStatusError, recorder.comparisons[0].Authority.Status)
	require.Equal(t, "PANIC", recorder.comparisons[0].Shadow.ErrorCode)
}

// TestShadowingDecisionEngineBoundsDelayAndIgnoresRecorderPanic checks the shadow deadline and recorder panic isolation.
func TestShadowingDecisionEngineBoundsDelayAndIgnoresRecorderPanic(t *testing.T) {
	recorder := &recordingShadowSink{panic: true}
	engine := NewShadowingDecisionEngine(decisionEngineFunc(func(context.Context, DecisionRequest) (DecisionOutcome, error) {
		return DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission}, nil
	}), shadowDeciderFunc(func(ctx context.Context, _ DecisionRequest) (ShadowPrediction, error) {
		<-ctx.Done()
		return ShadowPrediction{}, ctx.Err()
	}), recorder, 10*time.Millisecond, "jev:test")
	started := time.Now()
	outcome, err := engine.Decide(t.Context(), shadowRequest())
	require.NoError(t, err)
	require.Equal(t, DecisionVersion, outcome.Version)
	require.Less(t, time.Since(started), time.Second)
}

// TestShadowingDecisionEngineRecordsMalformedAndCanceled checks evidence classification for malformed, canceled, and timed-out predictions.
func TestShadowingDecisionEngineRecordsMalformedAndCanceled(t *testing.T) {
	for name, decider := range map[string]ShadowDecider{
		"malformed": shadowDeciderFunc(func(context.Context, DecisionRequest) (ShadowPrediction, error) {
			return ShadowPrediction{SelectedCandidate: "missing"}, nil
		}),
		"canceled": shadowDeciderFunc(func(context.Context, DecisionRequest) (ShadowPrediction, error) {
			return ShadowPrediction{}, context.Canceled
		}),
		"timeout": shadowDeciderFunc(func(context.Context, DecisionRequest) (ShadowPrediction, error) {
			return ShadowPrediction{}, context.DeadlineExceeded
		}),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &recordingShadowSink{}
			engine := NewShadowingDecisionEngine(decisionEngineFunc(func(context.Context, DecisionRequest) (DecisionOutcome, error) {
				return DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission}, nil
			}), decider, recorder, time.Second, "jev:test")
			_, err := engine.Decide(t.Context(), shadowRequest())
			require.NoError(t, err)
			require.Len(t, recorder.comparisons, 1)
			require.NotEmpty(t, recorder.comparisons[0].Shadow.ErrorCode)
		})
	}
}

// TestManagerRecordsExecutionLabelWithoutChangingStore checks that shadow labels preserve unknown success and existing store telemetry.
func TestManagerRecordsExecutionLabelWithoutChangingStore(t *testing.T) {
	recorder := &recordingShadowSink{}
	store := NewMemoryStore()
	mgr := NewManager(NewRegistry(), NewAdmissionGate(&executorMock{}), store)
	mgr.SetShadowEvidenceRecorder(recorder)
	mgr.now = func() time.Time { return time.Unix(3, 0) }
	task := TaskSpec{ID: "task-1", Kind: KindPlan, Risk: RiskMedium}
	metrics := ExecutionMetrics{Status: "completed", ScoreKnown: true, Score: .75, CostKnown: true, CostUSD: 0}
	mgr.RecordExecution(task, "a", metrics)
	require.Empty(t, recorder.labels)
	require.True(t, store.Signal("a", KindPlan).EvalScoreKnown)
}

// TestManagerRecordsReviewOutcomeAfterTransportCompletion checks that review outcomes retain the earlier execution telemetry.
func TestManagerRecordsReviewOutcomeAfterTransportCompletion(t *testing.T) {
	recorder := &recordingShadowSink{}
	registry := NewRegistryFromModels([]ModelCapabilities{{Name: "a", Tier: "large", Provider: "test"}})
	mgr := NewManager(registry, NewAdmissionGate(&executorMock{RunFunc: func(context.Context, string) AdmissionResult {
		return AdmissionResult{Output: `{"accept":true,"confidence":0.9}`}
	}}), NewMemoryStore())
	mgr.EnableDecisionShadow(nil, recorder, time.Second, "jev:test")
	task := TaskSpec{ID: "reviewed-task", Kind: KindPlan, Risk: RiskMedium, SuccessCriteria: []string{"tests pass"}}
	model, decision, err := mgr.Route(t.Context(), task)
	require.NoError(t, err)
	mgr.RecordExecutionForDecision(task, model.Name, decision, ExecutionMetrics{Status: "completed", UsageKnown: true, TotalTokens: 3})
	mgr.RecordReviewForDecision(task, model.Name, decision, false, 0)
	require.Len(t, recorder.labels, 2)
	require.True(t, recorder.labels[1].Success.Known)
	require.False(t, recorder.labels[1].Success.Value)
	require.True(t, recorder.labels[1].Score.Known)
	require.True(t, recorder.labels[1].Telemetry.UsageKnown)
}

// TestManagerUsesUniqueRouteIDsForRepeatedTasks checks that repeated tasks correlate labels with distinct route observations.
func TestManagerUsesUniqueRouteIDsForRepeatedTasks(t *testing.T) {
	recorder := &recordingShadowSink{}
	registry := NewRegistryFromModels([]ModelCapabilities{{Name: "a", Tier: "large", Provider: "test"}})
	mgr := NewManager(registry, NewAdmissionGate(&executorMock{RunFunc: func(context.Context, string) AdmissionResult {
		return AdmissionResult{Output: `{"accept":true,"confidence":0.9}`}
	}}), NewMemoryStore())
	mgr.EnableDecisionShadow(shadowDeciderFunc(func(context.Context, DecisionRequest) (ShadowPrediction, error) {
		return ShadowPrediction{SelectedCandidate: "a", Probability: DecisionProbability{Known: true, Value: .9}, Confidence: DecisionProbability{Known: true, Value: .9}}, nil
	}), recorder, time.Second, "jev:test")
	task := TaskSpec{ID: "same-task", Kind: KindPlan, Risk: RiskMedium}

	for range 2 {
		model, decision, err := mgr.Route(t.Context(), task)
		require.NoError(t, err)
		mgr.RecordExecutionForDecision(task, model.Name, decision, ExecutionMetrics{Status: "success"})
	}

	require.Len(t, recorder.comparisons, 2)
	require.Len(t, recorder.labels, 2)
	require.NotEqual(t, recorder.comparisons[0].RouteID, recorder.comparisons[1].RouteID)
	for index := range recorder.comparisons {
		require.Equal(t, recorder.comparisons[index].RouteID, recorder.labels[index].RouteID)
	}
}

// TestLegacyRecordExecutionUsesRememberedRouteID checks route correlation through the legacy recording API.
func TestLegacyRecordExecutionUsesRememberedRouteID(t *testing.T) {
	recorder := &recordingShadowSink{}
	registry := NewRegistryFromModels([]ModelCapabilities{{Name: "a", Tier: "large", Provider: "test"}})
	mgr := NewManager(registry, NewAdmissionGate(&executorMock{RunFunc: func(context.Context, string) AdmissionResult {
		return AdmissionResult{Output: `{"accept":true,"confidence":0.9}`}
	}}), NewMemoryStore())
	mgr.EnableDecisionShadow(nil, recorder, time.Second, "jev:test")
	task := TaskSpec{ID: "task", Kind: KindPlan, Risk: RiskMedium}
	model, _, err := mgr.Route(t.Context(), task)
	require.NoError(t, err)

	mgr.RecordExecution(task, model.Name, ExecutionMetrics{Status: "success"})

	require.Equal(t, recorder.comparisons[0].RouteID, recorder.labels[0].RouteID)
}

// shadowRequest builds a two-candidate request for shadow isolation tests.
func shadowRequest() DecisionRequest {
	return DecisionRequest{Version: DecisionVersion, Task: TaskSpec{ID: "task-1", Kind: KindPlan, Risk: RiskMedium, Objective: "original"}, Candidates: []DecisionCandidate{{Model: ModelCapabilities{Name: "a"}}, {Model: ModelCapabilities{Name: "b"}}}}
}
