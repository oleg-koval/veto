package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
)

// cmdRun routes the task then executes it on the winning model, printing the response.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	taskObj := fs.String("task", "", "task objective (or pass as a positional argument)")
	kindFlag := fs.String("kind", "", "task kind (auto-detected if omitted)")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "max cost in USD (0 = no limit)")
	timeout := fs.Duration("timeout", 120*time.Second, "total timeout (routing + execution)")
	quiet := fs.Bool("quiet", false, "suppress routing pipeline — print model output only")
	criteriaFlag := fs.String("criteria", "", "comma-separated acceptance criteria; review runs after execution")
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
	prompt := withSkills(objective, skillBodies)
	type streamer interface {
		Stream(ctx context.Context, prompt string, w interface{ Write([]byte) (int, error) }) error
	}
	var output string
	if s, ok := exec.(streamer); ok {
		// When criteria are set, tee stream output to a buffer so the reviewer can read it.
		if len(criteria) > 0 {
			var buf strings.Builder
			w := io.MultiWriter(os.Stdout, &buf)
			if serr := s.Stream(ctx, prompt, w); serr != nil {
				fmt.Fprintf(os.Stderr, "run failed: %v\n", serr)
				os.Exit(1)
			}
			fmt.Println()
			output = buf.String()
		} else {
			if serr := s.Stream(ctx, prompt, os.Stdout); serr != nil {
				fmt.Fprintf(os.Stderr, "run failed: %v\n", serr)
				os.Exit(1)
			}
			fmt.Println()
		}
	} else {
		result := exec.Run(ctx, prompt)
		if result.Error != nil {
			fmt.Fprintf(os.Stderr, "run failed: %v\n", result.Error)
			os.Exit(1)
		}
		output = result.Output
		fmt.Println(output)
	}

	// final QA: check acceptance criteria when --criteria was supplied
	if len(criteria) > 0 && output != "" {
		if result, ok := reviewOutput(ctx, reg, mgr, spec, output, model.Name); ok {
			render.PrintReview(result)
			if !result.Passed {
				os.Exit(1)
			}
		}
	}
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
	result := exec.Run(ctx, withSkills(spec.Objective, skills))
	if result.Error != nil {
		return model.Name, "", result.Error
	}
	return model.Name, result.Output, nil
}
