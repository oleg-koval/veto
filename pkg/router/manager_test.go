package router

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router/mocks"
)

func acceptJSON() string {
	return `{"accept":true,"confidence":0.9,"reason_codes":[]}`
}

func rejectJSON(code string) string {
	return `{"accept":false,"confidence":0.8,"reason_codes":["` + code + `"]}`
}

func TestManager_Route_FirstAccepted(t *testing.T) {
	calls := 0
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			calls++
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	task := TaskSpec{ID: "t1", Kind: KindCodeChange}
	model, decision, err := mgr.Route(context.Background(), task)
	require.NoError(t, err)
	assert.True(t, decision.Accept)
	assert.NotEmpty(t, model.Name)
	assert.Equal(t, 1, calls, "should stop after first acceptance")
}

func TestManager_Route_SkipRejectTakeSecond(t *testing.T) {
	n := 0
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			n++
			if n == 1 {
				return executor.Result{Output: rejectJSON(ReasonContextTooLarge)}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	store := NewMemoryStore()
	mgr := NewManager(reg, gate, store)

	model, decision, err := mgr.Route(context.Background(), TaskSpec{ID: "t2", Kind: KindCodeChange})
	require.NoError(t, err)
	assert.True(t, decision.Accept)
	assert.Equal(t, 2, n)
	assert.NotEmpty(t, model.Name)
}

func TestManager_Route_NoCandidateError(t *testing.T) {
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Output: rejectJSON(ReasonWeakKind)}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	_, _, err := mgr.Route(context.Background(), TaskSpec{ID: "t3", Kind: KindCodeChange})
	assert.ErrorIs(t, err, ErrNoCandidate)
}

func TestManager_Route_ExecErrorIsSkipped(t *testing.T) {
	n := 0
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			n++
			if n == 1 {
				return executor.Result{Error: errors.New("crash")}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	_, decision, err := mgr.Route(context.Background(), TaskSpec{ID: "t4", Kind: KindCodeChange})
	require.NoError(t, err)
	assert.True(t, decision.Accept)
	assert.Equal(t, 2, n)
}

func TestManager_Route_NoModelsAfterHardFilter(t *testing.T) {
	exec := &mocks.ExecutorMock{}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	// no model supports "quantum-tool"
	_, _, err := mgr.Route(context.Background(), TaskSpec{
		ID:            "t5",
		Kind:          KindCodeChange,
		RequiredTools: []string{"quantum-tool"},
	})
	require.ErrorIs(t, err, ErrNoCandidate)
	assert.Empty(t, exec.RunCalls(), "executor must not be called when hard filter eliminates all")
}

func TestManager_Route_ContextCancellation_ReturnsCtxErr(t *testing.T) {
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Error: errors.New("context canceled")}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, _, err := mgr.Route(ctx, TaskSpec{ID: "t-ctx", Kind: KindCodeChange})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled, "canceled context must surface as context.Canceled, not ErrNoCandidate")
	require.NotErrorIs(t, err, ErrNoCandidate)
}

func TestManager_Route_LogsDecisions(t *testing.T) {
	n := 0
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			n++
			if n < 3 {
				return executor.Result{Output: rejectJSON(ReasonWeakKind)}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	store := NewMemoryStore()
	mgr := NewManager(reg, gate, store)

	_, _, _ = mgr.Route(context.Background(), TaskSpec{ID: "t6", Kind: KindCodeChange})
	// verifying store received decisions via Signal (indirect)
	// if store is working, signal is non-zero
	sig := store.Signal("sonnet", KindCodeChange)
	assert.NotZero(t, sig.HistoricalRejectRate+sig.HistoricalSuccessRate)
}

func TestManager_Route_EmitsFilterEvents(t *testing.T) {
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	var filterPass, filterFail []string
	mgr.OnEvent = func(e ProgressEvent) {
		switch e.Kind {
		case EventFilterPass:
			filterPass = append(filterPass, e.Model)
		case EventFilterFail:
			filterFail = append(filterFail, e.Model)
		}
	}

	_, _, _ = mgr.Route(context.Background(), TaskSpec{ID: "ev1", Kind: KindCodeChange})

	// at least one model must pass hard filter (the registry has multiple models)
	assert.NotEmpty(t, filterPass, "expected at least one filter_pass event")
}

func TestManager_Route_EmitsAskEvents(t *testing.T) {
	n := 0
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			n++
			if n == 1 {
				return executor.Result{Output: rejectJSON(ReasonWeakKind)}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	var events []EventKind
	mgr.OnEvent = func(e ProgressEvent) { events = append(events, e.Kind) }

	_, _, err := mgr.Route(context.Background(), TaskSpec{ID: "ev2", Kind: KindCodeChange})
	require.NoError(t, err)

	assert.Contains(t, events, EventAskStart)
	assert.Contains(t, events, EventAskReject)
	assert.Contains(t, events, EventAskAccept)
}

func TestManager_Route_EmitsErrorEvent(t *testing.T) {
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Error: errors.New("exec failure")}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	var gotErrorEvent bool
	mgr.OnEvent = func(e ProgressEvent) {
		if e.Kind == EventAskError {
			gotErrorEvent = true
		}
	}

	_, _, _ = mgr.Route(context.Background(), TaskSpec{ID: "ev3", Kind: KindCodeChange})
	assert.True(t, gotErrorEvent)
}

func TestManager_Route_OnEventNil_NoPanic(t *testing.T) {
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Output: acceptJSON()}
		},
	}
	mgr := NewManager(NewRegistry(), NewAdmissionGate(exec), NewMemoryStore())
	// mgr.OnEvent is nil — must not panic
	assert.NotPanics(t, func() {
		_, _, _ = mgr.Route(context.Background(), TaskSpec{ID: "ev4", Kind: KindCodeChange})
	})
}

func TestManager_Route_SkipModels(t *testing.T) {
	callOrder := []string{}
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, prompt string) executor.Result {
			return executor.Result{Output: acceptJSON()}
		},
	}
	reg := NewRegistry()
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	var askedModels []string
	mgr.OnEvent = func(e ProgressEvent) {
		if e.Kind == EventAskStart {
			askedModels = append(askedModels, e.Model)
		}
	}

	// get the ranked candidates to know which model will be first
	all := reg.All()
	ranked := RankCandidates(TaskSpec{Kind: KindCodeChange}, all, reg)
	require.NotEmpty(t, ranked)
	topModel := ranked[0].Name

	// skip the top model — it should not be asked
	_, _, err := mgr.Route(context.Background(), TaskSpec{
		ID:         "skip1",
		Kind:       KindCodeChange,
		SkipModels: []string{topModel},
	})
	require.NoError(t, err)

	for _, m := range askedModels {
		assert.NotEqual(t, topModel, m, "skipped model %q must not be asked", topModel)
	}
	_ = callOrder
}
