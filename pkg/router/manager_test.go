package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func acceptJSON() string {
	return `{"accept":true,"confidence":0.9,"reason_codes":[]}`
}

func rejectJSON(code string) string {
	return `{"accept":false,"confidence":0.8,"reason_codes":["` + code + `"]}`
}

// TestManager_Route_FirstAccepted verifies sequential rank-order selection:
// candidates are asked in score order and the first to accept wins.
// Under cost-first scoring, the cheapest model passing hard-filter wins when
// all models accept. For KindDebug, haiku/gpt-4.1-mini are hard-filtered
// (Weakness), so the winner is the cheapest remaining model — llama-4-maverick.
func TestManager_Route_FirstAccepted(t *testing.T) {
	calls := 0
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			calls++
			return executor.Result{Output: acceptJSON()}
		},
	}
	// Use a fixed subset of the catalog so that adding new built-in models (e.g. grok-*)
	// does not change the cheapest-survivor for this test scenario.
	testModels := []string{"haiku", "sonnet", "opus", "gpt-4.1", "gpt-4.1-mini", "meta-llama/llama-4-maverick"}
	reg := NewRegistryFor(testModels)
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	task := TaskSpec{ID: "t1", Kind: KindDebug}
	model, decision, err := mgr.Route(context.Background(), task)
	require.NoError(t, err)
	assert.True(t, decision.Accept)
	// cheapest model that passes hard-filter wins (cost-first scoring)
	// (haiku + gpt-4.1-mini filtered by debug weakness)
	assert.Equal(t, "meta-llama/llama-4-maverick", model.Name, "cheapest non-filtered model should win")
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

// TestManager_Route_EmitsAskEvents verifies that asking stops after the first
// acceptance: only the asked candidates emit ask_start/accept/reject events,
// and no events are emitted for candidates ranked below the winner.
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

	var events []ProgressEvent
	mgr.OnEvent = func(e ProgressEvent) {
		events = append(events, e)
	}

	_, _, err := mgr.Route(context.Background(), TaskSpec{ID: "t7", Kind: KindCodeChange})
	require.NoError(t, err)

	var askEvents []ProgressEvent
	for _, e := range events {
		if e.Kind == EventAskStart || e.Kind == EventAskAccept || e.Kind == EventAskReject || e.Kind == EventAskError {
			askEvents = append(askEvents, e)
		}
	}

	// exactly two asks happened (1 reject, 1 accept) => 4 ask-related events:
	// ask_start, ask_reject, ask_start, ask_accept
	require.Len(t, askEvents, 4)
	assert.Equal(t, EventAskStart, askEvents[0].Kind)
	assert.Equal(t, EventAskReject, askEvents[1].Kind)
	assert.Equal(t, EventAskStart, askEvents[2].Kind)
	assert.Equal(t, EventAskAccept, askEvents[3].Kind)
}

// TestManager_Route_RankOrderDeterministic verifies that when multiple candidates
// would accept, the manager asks strictly in rank order and stops after the first
// acceptance — and produces the same winner on every run (no randomness).
// Under cost-first scoring, the cheapest non-filtered model wins: for KindDebug,
// haiku/gpt-4.1-mini are hard-filtered (Weakness), so llama-4-maverick wins.
func TestManager_Route_RankOrderDeterministic(t *testing.T) {
	var askedModels []string
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, prompt string) executor.Result {
			// the admission prompt embeds "You are the <name> model"
			for _, name := range []string{"opus", "sonnet", "haiku", "gpt-4.1", "gpt-4.1-mini", "meta-llama/llama-4-maverick"} {
				if strings.Contains(prompt, "You are the "+name) {
					askedModels = append(askedModels, name)
					break
				}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}
	// Use a fixed subset of the catalog so that adding new built-in models (e.g. grok-*)
	// does not change the cheapest-survivor for this test scenario.
	testModels := []string{"haiku", "sonnet", "opus", "gpt-4.1", "gpt-4.1-mini", "meta-llama/llama-4-maverick"}
	reg := NewRegistryFor(testModels)
	gate := NewAdmissionGate(exec)

	// KindDebug: haiku/gpt-4.1-mini hard-filtered (Weakness).
	// Cost-first scoring: llama-4-maverick ($0.0002) ranks #1.
	task := TaskSpec{ID: "t8", Kind: KindDebug}

	for i := 0; i < 5; i++ {
		askedModels = nil
		mgr := NewManager(reg, gate, NewMemoryStore())
		model, decision, err := mgr.Route(context.Background(), task)
		require.NoError(t, err)
		assert.True(t, decision.Accept)
		assert.Equal(t, "meta-llama/llama-4-maverick", model.Name, "cheapest non-filtered model must win deterministically")
		assert.Equal(t, []string{"meta-llama/llama-4-maverick"}, askedModels, "only first model asked when it accepts")
	}
}

func TestManager_Route_UsesStoreSignalsForRanking(t *testing.T) {
	models := []ModelCapabilities{
		{Name: "unreliable", Tier: tierSmall, MaxContextTokens: 10000, Strengths: []TaskKind{KindCodeChange}},
		{Name: "reliable", Tier: tierSmall, MaxContextTokens: 10000, Strengths: []TaskKind{KindCodeChange}},
	}
	reg := NewRegistryFromModels(models)
	store := NewMemoryStore()
	store.RecordExecution("old-1", "unreliable", KindCodeChange, ExecutionMetrics{Status: "failure"})
	store.RecordExecution("old-2", "reliable", KindCodeChange, ExecutionMetrics{Status: "success"})

	var asked string
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, prompt string) executor.Result {
			for _, model := range models {
				if strings.Contains(prompt, "You are the "+model.Name+" model") {
					asked = model.Name
					break
				}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}
	mgr := NewManager(reg, NewAdmissionGate(exec), store)

	_, _, err := mgr.Route(context.Background(), TaskSpec{ID: "new", Kind: KindCodeChange})
	require.NoError(t, err)
	assert.Equal(t, "reliable", asked)
}
