// Package paired coordinates explicit, isolated evaluation of two candidates.
// It has no provider adapter, credential lookup, network access, or file writer.
package paired

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/oleg-koval/veto/pkg/shadow"
)

const maxRunTimeout = 30 * time.Minute

// Trial is a private, in-memory join from one redacted route to a frozen task.
// Callers must not serialize it into the shadow evidence stream.
type Trial struct {
	RouteID         string
	TaskKind        string
	Risk            string
	Objective       string
	Criteria        []string
	ModelsByKey     map[string]string
	IdentitiesByKey map[string]router.ModelIdentity
	ToolsByKey      map[string]router.ToolCapabilities
	MaxOutputTokens int
	Timeout         time.Duration
}

// RunRequest contains private task data for one isolated candidate run.
// A Runner must honor context deadlines, deny or mock external side effects,
// and use only authorized model endpoints. Separate workspaces do not provide
// network isolation. When Identity and Tools are populated, it must reject a
// different current identity and honor the captured known tool set. It should
// call RunRequest.ValidateResolvedCandidate before executing.
type RunRequest struct {
	Model           string
	Identity        router.ModelIdentity
	Tools           router.ToolCapabilities
	Objective       string
	Criteria        []string
	Workspace       string
	MaxOutputTokens int
}

// ValidateResolvedCandidate rejects a replay if the configured model identity
// or route-time known tool snapshot changed since capture. Provenance-aware
// runners must call it after resolving Model and before starting execution.
func (r RunRequest) ValidateResolvedCandidate(model router.ModelCapabilities, tools router.ToolCapabilities) error {
	if r.Model != model.Name || r.Identity != model.Identity() || !r.Tools.Known || !tools.Known || !slices.Equal(r.Tools.Tools, tools.Tools) {
		return errors.New("paired replay: resolved model identity or tools differ from route-time snapshot")
	}
	return nil
}

// RunResult remains in memory. Output and private model identity are never
// copied into the redacted events returned by Replay.
type RunResult struct {
	Output  string
	Usage   shadow.KnownUsage
	CostUSD shadow.KnownFloat
}

type Runner interface {
	Run(context.Context, RunRequest) (RunResult, error)
}

// Grader sees the same frozen criteria for each output, without knowing which
// decision engine selected its candidate. It must honor context deadlines.
type Grader interface {
	Grade(context.Context, []string, string) (shadow.KnownBool, shadow.KnownFloat, error)
}

// Replay evaluates only an explicitly supplied divergent route. It returns
// two validated labels atomically, in authority-then-shadow order, and never
// appends to evidence itself. Runs occur in random order in temporary workspaces,
// removing each workspace before the next run; runner and grader must honor
// their separate trial.Timeout deadlines, each greater than zero and at most
// 30 minutes, subject to ctx's deadline.
//
// Any failure returns nil events, including invalid trial data, missing runner
// or grader, randomization, workspace creation or cleanup, execution, grading,
// or label validation failures. Runner and grader errors and expired child
// contexts become generic errors; comparison and label validation errors are
// wrapped, and cancellation detected between running and grading returns ctx.Err().
func Replay(ctx context.Context, dataset shadow.Dataset, trial Trial, runner Runner, grader Grader) (events []shadow.Event, replayErr error) {
	if runner == nil || grader == nil {
		return nil, errors.New("paired replay: runner and grader are required")
	}
	comparison, keys, err := validateTrial(dataset, trial)
	if err != nil {
		return nil, err
	}
	var random [1]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("paired replay: randomize order failed")
	}
	order := [2]int{0, 1}
	if random[0]&1 == 1 {
		order = [2]int{1, 0}
	}
	root, err := os.MkdirTemp("", "veto-paired-replay-")
	if err != nil {
		return nil, errors.New("paired replay: create isolated workspaces failed")
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			events = nil
			replayErr = errors.New("paired replay: remove isolated workspaces failed")
		}
	}()
	labels := [2]shadow.ExecutionLabel{}
	events = make([]shadow.Event, 2)
	for _, index := range order {
		workspace, err := os.MkdirTemp(root, "candidate-")
		if err != nil {
			return nil, errors.New("paired replay: create candidate workspace failed")
		}
		runCtx, cancel := context.WithTimeout(ctx, trial.Timeout)
		started := time.Now()
		result, runErr := runner.Run(runCtx, RunRequest{
			Model: trial.ModelsByKey[keys[index]], Identity: trial.IdentitiesByKey[keys[index]],
			Tools: cloneTools(trial.ToolsByKey[keys[index]]), Objective: trial.Objective,
			Criteria: append([]string(nil), trial.Criteria...), Workspace: workspace,
			MaxOutputTokens: trial.MaxOutputTokens,
		})
		elapsed := time.Since(started)
		runContextErr := runCtx.Err()
		cancel()
		if runErr != nil || runContextErr != nil {
			return nil, errors.New("paired replay: candidate execution failed")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		gradeCtx, cancelGrade := context.WithTimeout(ctx, trial.Timeout)
		success, score, gradeErr := grader.Grade(gradeCtx, append([]string(nil), trial.Criteria...), result.Output)
		gradeContextErr := gradeCtx.Err()
		cancelGrade()
		if gradeErr != nil || gradeContextErr != nil || !success.Known {
			return nil, errors.New("paired replay: candidate grade is unavailable")
		}
		labels[index] = shadow.ExecutionLabel{
			RouteID: comparison.RouteID, ObservedAt: time.Now().UTC(), Candidate: keys[index],
			Success: success, Score: score, Usage: result.Usage, CostUSD: result.CostUSD,
			Latency: shadow.KnownDuration{Known: true, Millis: elapsed.Milliseconds()},
		}
		events[index] = shadow.Event{SchemaVersion: shadow.SchemaVersion, Type: shadow.EventExecutionLabel, Label: &labels[index]}
		if err := events[index].Validate(); err != nil {
			return nil, fmt.Errorf("paired replay: invalid label %d: %w", index+1, err)
		}
		// Remove the completed candidate's files before the next runner starts.
		// The root cleanup still covers errors on either path.
		if err := os.RemoveAll(workspace); err != nil {
			return nil, errors.New("paired replay: remove candidate workspace failed")
		}
	}
	return events, nil
}

func cloneTools(tools router.ToolCapabilities) router.ToolCapabilities {
	return router.ToolCapabilities{Tools: append([]string(nil), tools.Tools...), Known: tools.Known}
}

// validateTrial returns the unique matching comparison and its authority and
// shadow candidate keys, without mutating the inputs. It rejects invalid
// comparison data, mismatched task metadata, blank objectives, missing or blank
// criteria, nonpositive output budgets, timeouts outside (0, 30 minutes], and
// selections that are not divergent or lack distinct, nonblank model bindings
// for exactly those two keys.
// Comparison validation errors are wrapped; low-level replay does not check
// model-identity provenance. Use ReplayManifest and an identity-aware Runner.
func validateTrial(dataset shadow.Dataset, trial Trial) (shadow.RouteComparison, [2]string, error) {
	var comparison shadow.RouteComparison
	matches := 0
	for _, route := range dataset.Routes {
		if route.Comparison.RouteID == trial.RouteID {
			comparison = route.Comparison
			matches++
		}
	}
	if matches != 1 {
		return shadow.RouteComparison{}, [2]string{}, errors.New("paired replay: route ID must match exactly one comparison")
	}
	if err := (shadow.Event{SchemaVersion: shadow.SchemaVersion, Type: shadow.EventRouteComparison, Comparison: &comparison}).Validate(); err != nil {
		return shadow.RouteComparison{}, [2]string{}, fmt.Errorf("paired replay: invalid comparison: %w", err)
	}
	if trial.TaskKind != comparison.TaskKind || trial.Risk != comparison.Risk || strings.TrimSpace(trial.Objective) == "" || len(trial.Criteria) == 0 {
		return shadow.RouteComparison{}, [2]string{}, errors.New("paired replay: private task does not match route metadata or lacks criteria")
	}
	for _, criterion := range trial.Criteria {
		if strings.TrimSpace(criterion) == "" {
			return shadow.RouteComparison{}, [2]string{}, errors.New("paired replay: empty criterion")
		}
	}
	if trial.Timeout <= 0 || trial.Timeout > maxRunTimeout || trial.MaxOutputTokens <= 0 {
		return shadow.RouteComparison{}, [2]string{}, errors.New("paired replay: positive bounded timeout and output budget are required")
	}
	authority, selected := comparison.Authority, comparison.Shadow
	if authority.Status != shadow.StatusSelected || selected.Status != shadow.StatusSelected || authority.SelectedCandidate == selected.SelectedCandidate {
		return shadow.RouteComparison{}, [2]string{}, errors.New("paired replay: route must have divergent selected candidates")
	}
	keys := [2]string{authority.SelectedCandidate, selected.SelectedCandidate}
	if len(trial.ModelsByKey) != 2 || strings.TrimSpace(trial.ModelsByKey[keys[0]]) == "" || strings.TrimSpace(trial.ModelsByKey[keys[1]]) == "" || trial.ModelsByKey[keys[0]] == trial.ModelsByKey[keys[1]] {
		return shadow.RouteComparison{}, [2]string{}, errors.New("paired replay: private candidate bindings must match both selections")
	}
	return comparison, keys, nil
}
