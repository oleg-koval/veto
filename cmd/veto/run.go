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

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router"
)

// cmdRun routes the task then executes it on the winning model, printing the response.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	taskObj := fs.String("task", "", "task objective (or pass as a positional argument)")
	kindFlag := fs.String("kind", "", "task kind (auto-detected if omitted)")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "estimated preflight cost ceiling in USD (0 = none)")
	timeout := fs.Duration("timeout", 120*time.Second, "total timeout (routing + execution)")
	quiet := fs.Bool("quiet", false, "suppress routing pipeline — print model output only")
	criteriaFlag := fs.String("criteria", "", "comma-separated acceptance criteria; review runs after execution")
	maxOutputTokens := fs.Int("max-output-tokens", executor.DefaultExecutionMaxTokens, "maximum output tokens for task execution")
	outputPath := fs.String("output", "", "write task output to a relative file path")
	forceOutput := fs.Bool("force", false, "overwrite an existing --output file")
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
	store := router.NewFileStore(historyPath())
	mgr := router.NewManager(modelReg, gate, store)

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
		ID:              taskHash(objective, kind, *risk, *maxCost),
		Kind:            router.TaskKind(kind),
		Complexity:      complexity,
		Objective:       objective,
		Risk:            router.Risk(*risk),
		MaxCostUSD:      *maxCost,
		SuccessCriteria: criteria,
	}

	// resolve skills in parallel with no blocking — local match is instant;
	// generation (rare miss path) runs before routing since it needs a model call anyway.
	skillNames, skillBodies := resolveSkills(ctx, reg, mgr, spec)
	render.PrintSkills(skillNames)

	mgr.OnEvent = func(e router.ProgressEvent) {
		render.OnEvent(e)
		logEvent(objective, kind, *risk, e)
	}

	model, _, err := mgr.Route(ctx, spec)
	_ = store.Save()

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

	exec, ok := reg.For(model.Name)
	if !ok {
		fmt.Fprintf(os.Stderr, "no executor for model %q\n", model.Name)
		os.Exit(1)
	}

	if !*quiet {
		fmt.Printf("\n  ── Running on %-10s %s\n\n", model.Name, strings.Repeat("─", 35))
	}

	// Use streaming if the executor supports it, otherwise buffer and print.
	// Skills are injected into the execution prompt; routing used the clean objective.
	// For text-only executors (HTTP-based, no tool definitions passed), append an
	// instruction to output content directly — not prose about what they would do.
	prompt := withSkills(objective, skillBodies)
	if !hasEffectiveTools(exec) {
		prompt += "\n\n---\nOutput the requested content directly. No explanation, no description of what you will do, no markdown prose. If the task is to create a file, output the file contents only."
	}
	type streamer interface {
		Stream(ctx context.Context, prompt string, w interface{ Write([]byte) (int, error) }) error
	}
	var output string
	started := time.Now()
	if s, ok := exec.(streamer); ok {
		// When criteria are set, tee stream output to a buffer so the reviewer can read it.
		if len(criteria) > 0 || *outputPath != "" {
			var buf strings.Builder
			w := io.MultiWriter(os.Stdout, &buf)
			if serr := s.Stream(ctx, prompt, w); serr != nil {
				mgr.RecordExecution(spec, model.Name, router.ExecutionMetrics{
					Status: executionStatus(ctx, "error"), LatencyMs: time.Since(started).Milliseconds(), LatencyKnown: true,
				})
				fmt.Fprintf(os.Stderr, "run failed: %v\n", serr)
				os.Exit(1)
			}
			fmt.Println()
			output = buf.String()
		} else {
			if serr := s.Stream(ctx, prompt, os.Stdout); serr != nil {
				mgr.RecordExecution(spec, model.Name, router.ExecutionMetrics{
					Status: executionStatus(ctx, "error"), LatencyMs: time.Since(started).Milliseconds(), LatencyKnown: true,
				})
				fmt.Fprintf(os.Stderr, "run failed: %v\n", serr)
				os.Exit(1)
			}
			fmt.Println()
		}
	} else {
		taskExec, ok := exec.(executor.TaskExecutor)
		if !ok {
			fmt.Fprintf(os.Stderr, "executor for %q does not support task execution\n", model.Name)
			os.Exit(1)
		}
		result := taskExec.Execute(ctx, prompt, executor.ExecutionOptions{MaxOutputTokens: *maxOutputTokens})
		if resultErr := validateExecutionResult(result); resultErr != nil {
			status := executionStatus(ctx, "error")
			if result.Truncated {
				status = "truncated"
			}
			mgr.RecordExecution(spec, model.Name, executionMetrics(model, result, time.Since(started), status))
			fmt.Fprintf(os.Stderr, "run failed: %v\n", resultErr)
			os.Exit(1)
		}
		output = result.Output
		fmt.Println(output)
		metrics := executionMetrics(model, result, time.Since(started), "success")
		mgr.RecordExecution(spec, model.Name, metrics)
		if !*quiet {
			if metrics.CostKnown {
				fmt.Fprintf(os.Stderr, "  actual provider cost: $%.6f (%d input, %d output tokens)\n", metrics.CostUSD, metrics.InputTokens, metrics.OutputTokens)
				if spec.MaxCostUSD > 0 && metrics.CostUSD > spec.MaxCostUSD {
					fmt.Fprintf(os.Stderr, "  warning: actual execution cost exceeded the estimated ceiling of $%.6f\n", spec.MaxCostUSD)
				}
			} else {
				fmt.Fprintln(os.Stderr, "  actual provider cost: unavailable")
			}
		}
	}
	if _, streaming := exec.(streamer); streaming {
		mgr.RecordExecution(spec, model.Name, router.ExecutionMetrics{
			Status: "success", LatencyMs: time.Since(started).Milliseconds(), LatencyKnown: true,
		})
		if !*quiet {
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

func prepareRouting() (*providerRegistry, *router.Manager, *router.FileStore, error) {
	reg, err := buildProviderRegistry()
	if err != nil {
		return nil, nil, nil, err
	}
	modelReg := router.NewRegistryFromModels(reg.modelCaps())
	gate := router.NewAdmissionGateWithFactory(reg)
	store := router.NewFileStore(historyPath())
	mgr := router.NewManager(modelReg, gate, store)
	return reg, mgr, store, nil
}

// routeAndCapture routes spec to the best model and runs the executor.
// skills bodies are prepended to the execution prompt (admission always uses the clean Objective).
// Pass nil skills for internal/meta routes (review, skill generation, plan conversion) to avoid recursion.
func routeAndCapture(ctx context.Context, reg *providerRegistry, mgr *router.Manager, render *Renderer, spec router.TaskSpec, skills []string) (string, string, error) {
	return routeAndCaptureWithOptions(ctx, reg, mgr, render, spec, skills, executor.ExecutionOptions{})
}

func routeAndCaptureWithOptions(ctx context.Context, reg *providerRegistry, mgr *router.Manager, render *Renderer, spec router.TaskSpec, skills []string, options executor.ExecutionOptions) (string, string, error) {
	prev := mgr.OnEvent
	mgr.OnEvent = func(e router.ProgressEvent) { render.OnEvent(e) }
	defer func() { mgr.OnEvent = prev }()
	model, _, err := mgr.Route(ctx, spec)
	if err != nil {
		return "", "", err
	}
	exec, ok := reg.For(model.Name)
	if !ok {
		return "", "", fmt.Errorf("no executor for model %q", model.Name)
	}
	prompt := withSkills(spec.Objective, skills)
	if !hasEffectiveTools(exec) {
		prompt += "\n\n---\nOutput the requested content directly. No explanation, no description of what you will do, no markdown prose. If the task is to create a file, output the file contents only."
	}
	taskExec, ok := exec.(executor.TaskExecutor)
	if !ok {
		return model.Name, "", fmt.Errorf("executor for %q does not support task execution", model.Name)
	}
	started := time.Now()
	result := taskExec.Execute(ctx, prompt, options)
	if resultErr := validateExecutionResult(result); resultErr != nil {
		status := executionStatus(ctx, "error")
		if result.Truncated {
			status = "truncated"
		}
		mgr.RecordExecution(spec, model.Name, executionMetrics(model, result, time.Since(started), status))
		return model.Name, "", resultErr
	}
	mgr.RecordExecution(spec, model.Name, executionMetrics(model, result, time.Since(started), "success"))
	return model.Name, result.Output, nil
}

func hasEffectiveTools(exec router.Executor) bool {
	tools, ok := exec.(executor.ToolProvider)
	return ok && len(tools.EffectiveTools()) > 0
}

func validateExecutionResult(result executor.Result) error {
	if result.Error != nil {
		return result.Error
	}
	if result.Truncated {
		reason := result.FinishReason
		if reason == "" {
			reason = "provider output limit"
		}
		return fmt.Errorf("execution output truncated (%s); increase --max-output-tokens", reason)
	}
	return nil
}

func executionStatus(ctx context.Context, fallback string) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	default:
		return fallback
	}
}

func executionMetrics(model router.ModelCapabilities, result executor.Result, elapsed time.Duration, status string) router.ExecutionMetrics {
	metrics := router.ExecutionMetrics{
		Status: status, LatencyMs: elapsed.Milliseconds(), LatencyKnown: true,
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
		TotalTokens: result.Usage.TotalTokens, UsageKnown: result.Usage.Known,
	}
	if result.Usage.Known {
		metrics.CostUSD = float64(result.Usage.InputTokens)/1000*model.CostPer1kInputUSD +
			float64(result.Usage.OutputTokens)/1000*model.CostPer1kOutputUSD
		metrics.CostKnown = true
	}
	return metrics
}
