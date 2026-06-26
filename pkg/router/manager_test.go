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
