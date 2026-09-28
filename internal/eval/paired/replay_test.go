package paired

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/shadow"
	"github.com/stretchr/testify/require"
)

type runnerFunc func(context.Context, RunRequest) (RunResult, error)

func (f runnerFunc) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	return f(ctx, request)
}

type graderFunc func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error)

func (f graderFunc) Grade(ctx context.Context, criteria []string, output string) (shadow.KnownBool, shadow.KnownFloat, error) {
	return f(ctx, criteria, output)
}

func replayFixture() (shadow.Dataset, Trial) {
	comparison := shadow.RouteComparison{
		RouteID: "route-1", ObservedAt: time.Unix(1, 0).UTC(), TaskKind: "review", Risk: "low",
		Candidates: []shadow.Candidate{{Key: "a"}, {Key: "b"}},
		Authority:  shadow.DecisionEvidence{Status: shadow.StatusSelected, SelectedCandidate: "a"},
		Shadow:     shadow.DecisionEvidence{Status: shadow.StatusSelected, SelectedCandidate: "b"},
	}
	dataset := shadow.Dataset{Routes: []shadow.RouteRecord{{Comparison: comparison}}}
	trial := Trial{
		RouteID: "route-1", TaskKind: "review", Risk: "low", Objective: "private frozen task",
		Criteria: []string{"answer is correct"}, ModelsByKey: map[string]string{"a": "model-a", "b": "model-b"},
		MaxOutputTokens: 1024, Timeout: time.Second,
	}
	return dataset, trial
}

func TestReplayReturnsOnlyRedactedValidatedPairedLabels(t *testing.T) {
	dataset, trial := replayFixture()
	var workspaces []string
	runner := runnerFunc(func(_ context.Context, request RunRequest) (RunResult, error) {
		require.Equal(t, trial.Objective, request.Objective)
		require.Equal(t, trial.Criteria, request.Criteria)
		require.Equal(t, 1024, request.MaxOutputTokens)
		for _, previous := range workspaces {
			require.NotEqual(t, previous, request.Workspace)
			_, err := os.Stat(previous)
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		workspaces = append(workspaces, request.Workspace)
		require.NoError(t, os.WriteFile(filepath.Join(request.Workspace, filepath.Base(request.Workspace)), []byte("marker"), 0600))
		output := "fail"
		if request.Model == "model-a" {
			output = "pass"
		}
		return RunResult{Output: output, Usage: shadow.KnownUsage{Known: true, InputTokens: 10, OutputTokens: 2, TotalTokens: 12}}, nil
	})
	grader := graderFunc(func(_ context.Context, criteria []string, output string) (shadow.KnownBool, shadow.KnownFloat, error) {
		require.Equal(t, trial.Criteria, criteria)
		return shadow.KnownBool{Known: true, Value: output == "pass"}, shadow.KnownFloat{Known: true, Value: 1}, nil
	})

	events, err := Replay(t.Context(), dataset, trial, runner, grader)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "a", events[0].Label.Candidate)
	require.True(t, events[0].Label.Success.Value)
	require.Equal(t, "b", events[1].Label.Candidate)
	require.False(t, events[1].Label.Success.Value)
	for _, event := range events {
		require.NoError(t, event.Validate())
		require.True(t, event.Label.Latency.Known)
		require.False(t, event.Label.CostUSD.Known)
	}
	for _, workspace := range workspaces {
		_, err := os.Stat(workspace)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	encoded, err := json.Marshal(events)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), trial.Objective)
	require.NotContains(t, string(encoded), "model-a")
	require.NotContains(t, string(encoded), "model-b")
}

func TestReplayRejectsWrongPrivateJoinBeforeRunning(t *testing.T) {
	dataset, trial := replayFixture()
	trial.ModelsByKey["b"] = ""
	calls := 0
	_, err := Replay(t.Context(), dataset, trial, runnerFunc(func(context.Context, RunRequest) (RunResult, error) {
		calls++
		return RunResult{}, nil
	}), graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
		return shadow.KnownBool{}, shadow.KnownFloat{}, nil
	}))
	require.ErrorContains(t, err, "private candidate bindings")
	require.Zero(t, calls)
}

func TestReplayReturnsNoPartialLabelsOnFailure(t *testing.T) {
	dataset, trial := replayFixture()
	grader := graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
		return shadow.KnownBool{Known: true, Value: true}, shadow.KnownFloat{}, nil
	})
	for _, failedModel := range []string{"model-a", "model-b"} {
		t.Run(failedModel, func(t *testing.T) {
			events, err := Replay(t.Context(), dataset, trial, runnerFunc(func(_ context.Context, request RunRequest) (RunResult, error) {
				if request.Model == failedModel {
					return RunResult{}, errors.New("private task text must not escape")
				}
				return RunResult{Output: "done"}, nil
			}), grader)
			require.Empty(t, events)
			require.ErrorContains(t, err, "candidate execution failed")
			require.False(t, strings.Contains(err.Error(), "private task text"))
		})
	}
}

func TestReplayReturnsNoLabelsOnGradeFailureOrInvalidLabel(t *testing.T) {
	dataset, trial := replayFixture()
	for _, test := range []struct {
		name   string
		grader graderFunc
		want   string
	}{
		{name: "grade failure", grader: func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
			return shadow.KnownBool{}, shadow.KnownFloat{}, errors.New("private grade detail")
		}, want: "candidate grade is unavailable"},
		{name: "invalid score", grader: func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
			return shadow.KnownBool{Known: true, Value: true}, shadow.KnownFloat{Known: true, Value: 2}, nil
		}, want: "invalid label"},
	} {
		t.Run(test.name, func(t *testing.T) {
			events, err := Replay(t.Context(), dataset, trial, runnerFunc(func(context.Context, RunRequest) (RunResult, error) {
				return RunResult{Output: "done"}, nil
			}), test.grader)
			require.Empty(t, events)
			require.ErrorContains(t, err, test.want)
			require.NotContains(t, err.Error(), "private grade detail")
		})
	}
}

func TestReplayHonorsBoundedContext(t *testing.T) {
	dataset, trial := replayFixture()
	trial.Timeout = time.Millisecond
	gradeCalls := 0
	events, err := Replay(t.Context(), dataset, trial, runnerFunc(func(ctx context.Context, _ RunRequest) (RunResult, error) {
		<-ctx.Done()
		return RunResult{}, ctx.Err()
	}), graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
		gradeCalls++
		return shadow.KnownBool{}, shadow.KnownFloat{}, nil
	}))
	require.Empty(t, events)
	require.ErrorContains(t, err, "candidate execution failed")
	require.Zero(t, gradeCalls)
}

func TestReplayStopsAtInvalidLabel(t *testing.T) {
	for _, invalidCall := range []int{1, 2} {
		t.Run(fmt.Sprintf("candidate call %d", invalidCall), func(t *testing.T) {
			dataset, trial := replayFixture()
			var workspaces []string
			runCalls, gradeCalls := 0, 0
			invalidIndex := 0
			runner := runnerFunc(func(_ context.Context, request RunRequest) (RunResult, error) {
				runCalls++
				workspaces = append(workspaces, request.Workspace)
				if runCalls == invalidCall {
					invalidIndex = 1
					if request.Model == trial.ModelsByKey["b"] {
						invalidIndex = 2
					}
				}
				return RunResult{Output: "done"}, nil
			})
			grader := graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
				gradeCalls++
				score := shadow.KnownFloat{Known: true, Value: 1}
				if gradeCalls == invalidCall {
					score.Value = 2
				}
				return shadow.KnownBool{Known: true, Value: true}, score, nil
			})

			events, err := Replay(t.Context(), dataset, trial, runner, grader)
			require.Nil(t, events)
			require.EqualError(t, err, fmt.Sprintf("paired replay: invalid label %d: shadow evidence: score must be finite and between 0 and 1", invalidIndex))
			require.Equal(t, invalidCall, runCalls)
			require.Equal(t, invalidCall, gradeCalls)
			for _, workspace := range workspaces {
				_, err := os.Stat(workspace)
				require.ErrorIs(t, err, os.ErrNotExist)
				_, err = os.Stat(filepath.Dir(workspace))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestReplayRandomizesCandidateOrder(t *testing.T) {
	dataset, trial := replayFixture()
	seen := map[string]bool{}
	for range 32 {
		first := ""
		events, err := Replay(t.Context(), dataset, trial, runnerFunc(func(_ context.Context, request RunRequest) (RunResult, error) {
			if first == "" {
				first = request.Model
			}
			return RunResult{Output: "done"}, nil
		}), graderFunc(func(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error) {
			return shadow.KnownBool{Known: true, Value: true}, shadow.KnownFloat{}, nil
		}))
		require.NoError(t, err)
		require.Len(t, events, 2)
		seen[first] = true
	}
	require.Len(t, seen, 2)
}
