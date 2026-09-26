package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type shadowDeciderFunc func(context.Context, DecisionRequest) (ShadowPrediction, error)

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

func (r *recordingShadowSink) RecordShadowComparison(comparison ShadowComparisonRecord) error {
	if r.panic {
		panic("recorder")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comparisons = append(r.comparisons, comparison)
	return r.err
}

func (r *recordingShadowSink) RecordShadowExecutionLabel(label ShadowExecutionLabelRecord) error {
	if r.panic {
		panic("recorder")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.labels = append(r.labels, label)
	return r.err
}

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

func TestManagerRecordsExecutionLabelWithoutChangingStore(t *testing.T) {
	recorder := &recordingShadowSink{}
	store := NewMemoryStore()
	mgr := NewManager(NewRegistry(), NewAdmissionGate(&executorMock{}), store)
	mgr.SetShadowEvidenceRecorder(recorder)
	mgr.now = func() time.Time { return time.Unix(3, 0) }
	task := TaskSpec{ID: "task-1", Kind: KindPlan, Risk: RiskMedium}
	metrics := ExecutionMetrics{Status: "completed", ScoreKnown: true, Score: .75, CostKnown: true, CostUSD: 0}
	mgr.RecordExecution(task, "a", metrics)
	require.Len(t, recorder.labels, 1)
	require.False(t, recorder.labels[0].Success.Known)
	require.True(t, recorder.labels[0].Score.Known)
	require.True(t, store.Signal("a", KindPlan).EvalScoreKnown)
}

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

func shadowRequest() DecisionRequest {
	return DecisionRequest{Version: DecisionVersion, Task: TaskSpec{ID: "task-1", Kind: KindPlan, Risk: RiskMedium, Objective: "original"}, Candidates: []DecisionCandidate{{Model: ModelCapabilities{Name: "a"}}, {Model: ModelCapabilities{Name: "b"}}}}
}
