package router

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type decisionEngineFunc func(context.Context, DecisionRequest) (DecisionOutcome, error)

func (f decisionEngineFunc) Decide(ctx context.Context, r DecisionRequest) (DecisionOutcome, error) {
	return f(ctx, r)
}

type admissionRecord struct {
	model    string
	decision AdmissionDecision
	kind     TaskKind
}
type legacyDecisionStore struct{ records []admissionRecord }

func (s *legacyDecisionStore) LogDecision(_ string, model string, d AdmissionDecision) {
	s.records = append(s.records, admissionRecord{model: model, decision: d})
}
func (*legacyDecisionStore) LogResult(string, string, float64, string) {}
func (*legacyDecisionStore) Signal(string, TaskKind) RoutingSignal     { return RoutingSignal{} }

type kindDecisionStore struct{ *legacyDecisionStore }

func (s kindDecisionStore) LogDecisionForKind(_ string, model string, kind TaskKind, d AdmissionDecision) {
	s.records = append(s.records, admissionRecord{model: model, kind: kind, decision: d})
}

func parityModels() []ModelCapabilities {
	var models []ModelCapabilities
	for i := 0; i < 5; i++ {
		models = append(models, ModelCapabilities{Name: fmt.Sprintf("m%d", i), Tier: tierMid, Provider: "test", CostPer1kInputUSD: float64(i+1) * 0.001})
	}
	return models
}

func TestSequentialManagerParity(t *testing.T) {
	full := `{"accept":true,"confidence":0.91,"reason_codes":[],"estimated_tokens":123,"estimated_cost_usd":0.002,"suggested_alternative_model":"other","required_task_changes":["clarify"]}`
	for _, tc := range []struct {
		name, first                                    string
		transport, timeout, cancel, skip, allSkip, cap bool
		reason                                         string
	}{
		{name: "accept", first: full},
		{name: "reject", first: rejectJSON(ReasonWeakKind), reason: ReasonWeakKind},
		{name: "low confidence", first: `{"accept":true,"confidence":0.1}`, reason: ReasonLowConfidence},
		{name: "parse failure", first: "broken", reason: ReasonParseFailure},
		{name: "transport failure", transport: true},
		{name: "timeout", timeout: true},
		{name: "cancellation", cancel: true},
		{name: "skipped", skip: true, first: full},
		{name: "all skipped", allSkip: true},
		{name: "three attempt cap", transport: true, cap: true},
		{name: "parse failures exhaust cap", first: "broken", cap: true},
		{name: "rejections exhaust cap", first: rejectJSON(ReasonWeakKind), cap: true},
	} {
		for _, kindAware := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/kind=%v", tc.name, kindAware), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				models := parityModels()
				records := &legacyDecisionStore{}
				var store Store = records
				if kindAware {
					store = kindDecisionStore{records}
				}
				var events []ProgressEvent
				var calls []string
				exec := &executorMock{RunFunc: func(ctx context.Context, prompt string) AdmissionResult {
					model := ""
					for _, m := range models {
						if strings.Contains(prompt, "You are the "+m.Name+" model") {
							model = m.Name
						}
					}
					calls = append(calls, model)
					require.Equal(t, ProgressEvent{Kind: EventAskStart, Model: model}, events[len(events)-1], "start must be live before Run")
					require.Len(t, records.records, len(calls)-1, "previous attempt must already be persisted")
					if len(calls) == 1 || tc.cap {
						if tc.cancel {
							cancel()
							return AdmissionResult{Error: ctx.Err()}
						}
						if tc.timeout {
							<-ctx.Done()
							return AdmissionResult{Error: ctx.Err()}
						}
						if tc.transport {
							return AdmissionResult{Error: errors.New("transport failed")}
						}
						return AdmissionResult{Output: tc.first}
					}
					return AdmissionResult{Output: full}
				}}
				gate := NewAdmissionGate(exec)
				gate.SetTimeout(time.Millisecond)
				mgr := NewManager(NewRegistryFromModels(models), gate, store)
				mgr.OnEvent = func(e ProgressEvent) {
					if e.Kind == EventAskStart || e.Kind == EventAskAccept || e.Kind == EventAskReject || e.Kind == EventAskError {
						if e.Kind != EventAskStart {
							require.Len(t, records.records, len(calls), "persist before terminal event")
						}
						events = append(events, e)
					}
				}
				task := TaskSpec{ID: "parity", Kind: KindSummarize}
				if tc.skip {
					task.SkipModels = []string{"m0", "m1"}
				}
				if tc.allSkip {
					for _, m := range models {
						task.SkipModels = append(task.SkipModels, m.Name)
					}
				}
				model, d, err := mgr.Route(ctx, task)
				if tc.cancel {
					require.ErrorIs(t, err, context.Canceled)
					require.Equal(t, "routing: context canceled", err.Error())
					require.Len(t, events, 1)
					require.Empty(t, records.records)
					return
				}
				if tc.cap || tc.allSkip {
					require.ErrorIs(t, err, ErrNoCandidate)
					if tc.cap {
						require.Equal(t, []string{"m0", "m1", "m2"}, calls)
					} else {
						require.Empty(t, calls)
					}
					return
				}
				require.NoError(t, err)
				expected, _ := parseAdmissionJSON(full)
				require.Equal(t, expected, d)
				offset := 0
				if tc.skip {
					offset = 2
				}
				require.Equal(t, models[offset+len(calls)-1], model)
				require.Equal(t, ProgressEvent{Kind: EventAskAccept, Model: model.Name, Confidence: .91, EstTokens: 123, EstCost: .002}, events[len(events)-1])
				for i, r := range records.records {
					require.Equal(t, fmt.Sprintf("m%d", offset+i), r.model)
					if kindAware {
						require.Equal(t, task.Kind, r.kind)
					}
				}
				if tc.reason != "" {
					require.Equal(t, ProgressEvent{Kind: EventAskReject, Model: "m0", Reasons: []string{tc.reason}}, events[1])
				}
				if tc.transport || tc.timeout {
					detail := "transport failed"
					if tc.timeout {
						detail = context.DeadlineExceeded.Error()
					}
					require.Equal(t, ProgressEvent{Kind: EventAskError, Model: "m0", Detail: detail}, events[1])
					require.Equal(t, []string{ReasonParseFailure}, records.records[0].decision.ReasonCodes)
				}
			})
		}
	}
}

func TestManagerDecisionBoundary(t *testing.T) {
	for _, tc := range []string{"valid", "unknown", "invalid version", "invalid admission", "mutated shortlist", "no selection", "engine error", "invalid request", "empty"} {
		t.Run(tc, func(t *testing.T) {
			models := parityModels()
			if tc == "invalid request" {
				models[1].Name = " "
			}
			if tc == "empty" {
				models = nil
			}
			mgr := NewManager(NewRegistryFromModels(models), NewAdmissionGate(&executorMock{}), NewMemoryStore())
			calls := 0
			sentinel := errors.New("engine failed")
			mgr.SetDecisionEngine(decisionEngineFunc(func(_ context.Context, r DecisionRequest) (DecisionOutcome, error) {
				calls++
				require.NoError(t, r.Validate())
				require.Len(t, r.Candidates, 3)
				require.Equal(t, []string{"m1", "m2", "m3"}, []string{r.Candidates[0].Model.Name, r.Candidates[1].Model.Name, r.Candidates[2].Model.Name})
				require.True(t, r.Candidates[0].Tools.Known)
				o := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission, SelectedCandidate: "m2"}
				switch tc {
				case "unknown":
					o.SelectedCandidate = "m4"
				case "invalid version":
					o.Version = 99
				case "invalid admission":
					o.Admission = &AdmissionDecision{}
				case "mutated shortlist":
					r.Candidates[0].Model.Name = "intruder"
					o.SelectedCandidate = "intruder"
				case "no selection":
					o.SelectedCandidate = ""
				case "engine error":
					return DecisionOutcome{}, sentinel
				}
				return o, nil
			}))
			model, d, err := mgr.Route(t.Context(), TaskSpec{Kind: KindSummarize, SkipModels: []string{"m0"}})
			switch tc {
			case "valid":
				require.NoError(t, err)
				require.Equal(t, models[2], model)
				require.True(t, d.Accept)
			case "no selection", "empty":
				require.ErrorIs(t, err, ErrNoCandidate)
			case "engine error":
				require.ErrorIs(t, err, sentinel)
			default:
				require.Error(t, err)
				require.Empty(t, model)
				require.Empty(t, d)
			}
			if tc == "empty" || tc == "invalid request" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestSequentialConcurrentTimeouts(t *testing.T) {
	exec := &executorMock{RunFunc: func(ctx context.Context, prompt string) AdmissionResult {
		deadline, ok := ctx.Deadline()
		if !ok {
			return AdmissionResult{Error: errors.New("missing deadline")}
		}
		remaining := time.Until(deadline)
		if strings.Contains(prompt, "objective: long") && remaining < time.Minute {
			return AdmissionResult{Error: errors.New("request timeout leaked")}
		}
		if strings.Contains(prompt, "objective: short") && remaining > time.Second {
			return AdmissionResult{Error: errors.New("override ignored")}
		}
		return AdmissionResult{Output: acceptJSON()}
	}}
	gate := NewAdmissionGate(exec)
	mgr := NewManager(NewRegistryFromModels(parityModels()[:1]), gate, NewMemoryStore())
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gate.SetTimeout(time.Duration(i+1) * time.Second)
			objective := "long"
			timeout := 2 * time.Minute
			if i%3 == 0 {
				objective = "configured"
				timeout = 0
			} else if i%2 == 0 {
				objective = "short"
				timeout = time.Second
			}
			_, d, err := mgr.RouteWithAdmissionTimeout(t.Context(), TaskSpec{Kind: KindSummarize, Objective: objective}, timeout)
			if err != nil || !d.Accept {
				t.Errorf("route: %v, %+v", err, d)
			}
		}(i)
	}
	wg.Wait()
}

func TestSequentialEngineRequestReadOnly(t *testing.T) {
	r := DecisionRequest{Version: DecisionVersion, Task: TaskSpec{SkipModels: []string{"skip"}}, Candidates: []DecisionCandidate{{Model: ModelCapabilities{Name: "skip"}}, {Model: ModelCapabilities{Name: "accept"}}}}
	before := cloneDecisionRequest(r)
	engine := NewSequentialAdmissionEngine(NewAdmissionGate(&executorMock{RunFunc: func(context.Context, string) AdmissionResult { return AdmissionResult{Output: acceptJSON()} }}))
	o, err := engine.Decide(t.Context(), r)
	require.NoError(t, err)
	require.NoError(t, o.Validate(r))
	require.Equal(t, "accept", o.SelectedCandidate)
	require.True(t, reflect.DeepEqual(before, r))
}

func TestSequentialLegacyConfidence(t *testing.T) {
	for _, confidence := range []float64{.9, 1.1} {
		t.Run(fmt.Sprint(confidence), func(t *testing.T) {
			gate := NewAdmissionGate(&executorMock{RunFunc: func(context.Context, string) AdmissionResult {
				return AdmissionResult{Output: fmt.Sprintf(`{"accept":true,"confidence":%g}`, confidence)}
			}})
			request := decisionRequestForTest("a")
			outcome, err := NewSequentialAdmissionEngine(gate).Decide(t.Context(), request)
			require.NoError(t, err)
			require.NoError(t, outcome.Validate(request))
			require.Equal(t, confidence, outcome.Admission.Confidence)
			require.Equal(t, confidence <= 1, outcome.Confidence.Known)
			mgr := NewManager(NewRegistryFromModels(parityModels()), gate, NewMemoryStore())
			_, admission, err := mgr.Route(t.Context(), TaskSpec{Kind: KindSummarize})
			require.NoError(t, err)
			require.Equal(t, *outcome.Admission, admission)
		})
	}
}
