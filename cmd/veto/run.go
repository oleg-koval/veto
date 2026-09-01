package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/oleg-koval/veto/internal/adapter/routinghistory"
	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/ledger"
	"github.com/oleg-koval/veto/pkg/router"
)

const (
	defaultRunTimeout       = 2 * time.Hour
	defaultAdmissionTimeout = 60 * time.Second
)

// cmdRun routes the task then executes it on the winning model, printing the response.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	taskObj := fs.String("task", "", "task objective (or pass as a positional argument)")
	kindFlag := fs.String("kind", "", "task kind (auto-detected if omitted)")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "estimated preflight cost ceiling in USD (0 = none)")
	timeout := fs.Duration("timeout", defaultRunTimeout, "total timeout (routing + execution)")
	admissionTimeout := fs.Duration("admission-timeout", defaultAdmissionTimeout, "timeout for each model admission decision")
	quiet := fs.Bool("quiet", false, "suppress routing pipeline — print model output only")
	criteriaFlag := fs.String("criteria", "", "comma-separated acceptance criteria; review runs after execution")
	maxOutputTokens := fs.Int("max-output-tokens", execution.DefaultExecutionMaxTokens, "maximum output tokens for task execution")
	outputPath := fs.String("output", "", "write task output to a relative file path")
	forceOutput := fs.Bool("force", false, "overwrite an existing --output file")
	noFeedback := fs.Bool("no-feedback", false, "disable the opt-in post-run feedback prompt")
	_ = fs.Parse(args)

	objective := *taskObj
	if objective == "" {
		objective = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if objective == "" {
		fmt.Fprintln(os.Stderr, "error: provide a task objective via --task or as an argument")
		fs.Usage()
		os.Exit(1)
	}

	kindInferred := *kindFlag == ""
	kind := *kindFlag
	if kindInferred {
		kind = inferKind(objective)
	}
	complexity := router.InferComplexity(objective, router.TaskKind(kind))

	setupLogger()

	reg, err := buildProviderRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	sigCtx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()
	ctx, cancel := context.WithTimeout(sigCtx, *timeout)
	defer cancel()

	modelReg := router.NewRegistryFromModels(reg.modelCaps())
	gate := router.NewAdmissionGateWithFactory(reg)
	gate.SetTimeout(*admissionTimeout)
	store := routinghistory.NewFileStore(historyPath())
	mgr := router.NewManager(modelReg, gate, store)
	mgr.SetCandidatePreferences(loadCandidatePreferences())

	render := NewRenderer(*quiet)
	render.PrintTaskHeader(objective, kind, *risk, string(complexity), *maxCost, kindInferred)

	var criteria []string
	if *criteriaFlag != "" {
		for _, c := range strings.Split(*criteriaFlag, ",") {
			if t := strings.TrimSpace(c); t != "" {
				criteria = append(criteria, t)
			}
		}
	}

	spec := router.TaskSpec{
		ID:                      taskHash(objective, kind, *risk, *maxCost),
		Kind:                    router.TaskKind(kind),
		Complexity:              complexity,
		Objective:               objective,
		RequiresExecutableTools: requiresExecutableRuntime(objective),
		Risk:                    router.Risk(*risk),
		MaxCostUSD:              *maxCost,
		SuccessCriteria:         criteria,
	}

	// resolve skills in parallel with no blocking — local match is instant;
	// generation (rare miss path) runs before routing since it needs a model call anyway.
	skillNames, skillBodies := resolveSkills(ctx, reg, mgr, spec)
	render.PrintSkills(skillNames)

	mgr.OnEvent = func(e router.ProgressEvent) {
		render.OnEvent(e)
		logEvent(spec.ID, kind, *risk, e)
	}

	var executionMetrics router.ExecutionMetrics
	runner := application.Runner{
		Router:  mgr,
		Runtime: reg,
		Hooks: application.Hooks{
			OnExecutionEvent: func(event application.ExecutionEvent) {
				switch event.Kind {
				case application.ExecutionStarted:
					if !*quiet {
						fmt.Printf("\n  ── Running on %-10s %s\n\n", event.Model.Name, strings.Repeat("─", 35))
					}
					logExecution(event.TaskID, ledger.EventExecutionStarted, event.Model, event.Metrics, event.Detail)
				case application.ExecutionCompleted:
					executionMetrics = event.Metrics
					logExecution(event.TaskID, ledger.EventExecutionCompleted, event.Model, event.Metrics, event.Detail)
				case application.ExecutionFailed:
					executionMetrics = event.Metrics
					logExecution(event.TaskID, ledger.EventExecutionError, event.Model, event.Metrics, event.Detail)
				}
			},
			OnRuntimeEvent: logRuntimeEvent,
		},
	}
	response, err := runner.Execute(ctx, application.Request{
		Task: spec, Skills: skillBodies,
		Options: execution.ExecutionOptions{MaxOutputTokens: *maxOutputTokens},
		Writer:  os.Stdout,
	})
	_ = store.Save()

	if err != nil && response.Model.Name != "" {
		fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
		os.Exit(1)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(os.Stderr, "\n  Timed out during routing.")
		os.Exit(130)
	}
	if errors.Is(err, router.ErrNoCandidate) {
		fmt.Fprintln(os.Stderr, "\n  No model accepted this task. Try adjusting --kind or --risk.")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "routing failed: %v\n", err)
		os.Exit(1)
	}

	model := response.Model
	output := response.Output
	if response.OutputWritten {
		fmt.Println()
	} else {
		fmt.Println(output)
	}
	if !*quiet {
		if executionMetrics.CostKnown {
			fmt.Fprintf(os.Stderr, "  actual provider cost: $%.6f (%d input, %d output tokens)\n", executionMetrics.CostUSD, executionMetrics.InputTokens, executionMetrics.OutputTokens)
			if spec.MaxCostUSD > 0 && executionMetrics.CostUSD > spec.MaxCostUSD {
				fmt.Fprintf(os.Stderr, "  warning: actual execution cost exceeded the estimated ceiling of $%.6f\n", spec.MaxCostUSD)
			}
		} else {
			fmt.Fprintln(os.Stderr, "  actual provider cost: unavailable")
		}
	}

	if *outputPath != "" {
		if strings.TrimSpace(output) == "" {
			fmt.Fprintln(os.Stderr, "output failed: executor returned empty output")
			os.Exit(1)
		}
		if err := writeOutputFile(*outputPath, output, *forceOutput); err != nil {
			fmt.Fprintf(os.Stderr, "output failed: %v\n", err)
			os.Exit(1)
		}
		if !*quiet {
			fmt.Printf("\n  → saved %s\n", *outputPath)
		}
		logLifecycle(spec.ID, ledger.EventArtifactCreated, "created", "")
	}

	// final QA: check acceptance criteria when --criteria was supplied
	if len(criteria) > 0 {
		if strings.TrimSpace(output) == "" {
			fmt.Fprintln(os.Stderr, "review failed: executor returned empty output")
			os.Exit(1)
		}
		result, err := reviewOutput(ctx, reg, mgr, spec, output, model.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review failed: %v\n", err)
			os.Exit(1)
		}
		render.PrintReview(result)
		if !result.Passed {
			os.Exit(1)
		}
	}
	if !*noFeedback {
		maybeOfferPostRunFeedback("run", *risk, model.Name)
	}
}

func writeOutputFile(path, output string, force bool) error {
	clean := filepath.Clean(path)
	if path == "" || filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe output path %q: use a relative path inside the current directory", path)
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("unsafe output path %q: hidden files and directories are not allowed", path)
		}
	}
	parent := filepath.Dir(clean)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	resolvedParent, err = filepath.Abs(resolvedParent)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cwd, resolvedParent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe output path %q: resolved directory escapes the current directory", path)
	}
	if info, statErr := os.Lstat(clean); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe output path %q: symbolic links are not allowed", path)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(clean, flags, 0600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; use --force to overwrite", clean)
		}
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return fmt.Errorf("set output permissions: %w", err)
	}
	_, err = io.WriteString(f, stripCodeFence(output)+"\n")
	return err
}

// stripCodeFence removes a single markdown code fence wrapper (```lang … ```)
// from model output. Models often wrap content in fences even when told not to.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// drop the opening fence line (```html, ```go, etc.)
	nl := strings.Index(s, "\n")
	if nl == -1 {
		return s
	}
	s = s[nl+1:]
	// drop the closing fence
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	return s
}

var validKinds = map[string]bool{
	"code-change": true,
	"debug":       true,
	"refactor":    true,
	"summarize":   true,
	"extract":     true,
	"review":      true,
	"plan":        true,
}

var validKindList = "code-change|debug|refactor|summarize|extract|review|plan"

func prepareRouting() (*providerRegistry, *router.Manager, *routinghistory.FileStore, error) {
	reg, err := buildProviderRegistry()
	if err != nil {
		return nil, nil, nil, err
	}
	modelReg := router.NewRegistryFromModels(reg.modelCaps())
	gate := router.NewAdmissionGateWithFactory(reg)
	store := routinghistory.NewFileStore(historyPath())
	mgr := router.NewManager(modelReg, gate, store)
	mgr.SetCandidatePreferences(loadCandidatePreferences())
	return reg, mgr, store, nil
}

// routeAndCapture routes spec to the best model and runs the executor.
// skills bodies are prepended to the execution prompt (admission always uses the clean Objective).
// Pass nil skills for internal/meta routes (review, skill generation, plan conversion) to avoid recursion.
func routeAndCapture(ctx context.Context, reg *providerRegistry, mgr *router.Manager, render *Renderer, spec router.TaskSpec, skills []string) (string, string, error) {
	return routeAndCaptureWithOptions(ctx, reg, mgr, render, spec, skills, execution.ExecutionOptions{})
}

func routeAndCaptureWithOptions(ctx context.Context, reg *providerRegistry, mgr *router.Manager, render *Renderer, spec router.TaskSpec, skills []string, options execution.ExecutionOptions) (string, string, error) {
	prev := mgr.OnEvent
	mgr.OnEvent = func(e router.ProgressEvent) {
		render.OnEvent(e)
		logEvent(spec.ID, string(spec.Kind), string(spec.Risk), e)
	}
	defer func() { mgr.OnEvent = prev }()
	runner := newApplicationRunner(reg, mgr)
	response, err := runner.Execute(ctx, application.Request{
		Task: spec, Skills: skills, Options: options,
	})
	if err != nil {
		return response.Model.Name, response.Output, err
	}
	return response.Model.Name, response.Output, nil
}

// newApplicationRunner wires delivery-side telemetry adapters around the
// delivery-neutral application use case.
func newApplicationRunner(reg *providerRegistry, mgr *router.Manager) application.Runner {
	return application.Runner{
		Router: mgr, Runtime: reg,
		Hooks: application.Hooks{
			OnExecutionEvent: func(event application.ExecutionEvent) {
				switch event.Kind {
				case application.ExecutionStarted:
					logExecution(event.TaskID, ledger.EventExecutionStarted, event.Model, event.Metrics, event.Detail)
				case application.ExecutionCompleted:
					logExecution(event.TaskID, ledger.EventExecutionCompleted, event.Model, event.Metrics, event.Detail)
				case application.ExecutionFailed:
					logExecution(event.TaskID, ledger.EventExecutionError, event.Model, event.Metrics, event.Detail)
				}
			},
			OnRuntimeEvent: logRuntimeEvent,
		},
	}
}

// executionPrompt adds live verification instructions only when the objective
// explicitly asks an agent to address pull-request review comments. GitHub's
// ordinary PR comment views omit inline review threads, which can otherwise
// make an agent incorrectly report that there is nothing to fix.
func executionPrompt(objective string, skills []string) string {
	return application.BuildExecutionPrompt(objective, skills)
}

func requiresPullRequestThreadWorkflow(objective string) bool {
	prTarget, reviewTarget, mutation := pullRequestMutationSignals(objective)
	return prTarget && reviewTarget && mutation
}

func hasEffectiveTools(exec execution.RuntimeAdapter) bool {
	tools, ok := exec.(execution.ToolProvider)
	return ok && len(tools.EffectiveTools()) > 0
}

func isTextOnlyRuntime(exec execution.RuntimeAdapter) bool {
	return application.IsTextOnlyRuntime(exec)
}

func validateExecutionResult(result execution.Result) error {
	return application.ValidateExecutionResult(result)
}

func executionStatus(ctx context.Context, fallback string) string {
	return application.ExecutionStatus(ctx, fallback)
}

func executionMetrics(model router.ModelCapabilities, result execution.Result, elapsed time.Duration, status string) router.ExecutionMetrics {
	return application.ExecutionMetrics(model, result, elapsed, status)
}
