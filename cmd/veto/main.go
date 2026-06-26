package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "route":
		cmdRoute(os.Args[2:])
	case "providers":
		cmdProviders()
	case "login":
		cmdLogin()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "veto — model router with self-admitting receivers")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "usage: veto <command> [flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  login       save a provider API key interactively")
	fmt.Fprintln(os.Stderr, "  route       route a task to the best available model")
	fmt.Fprintln(os.Stderr, "  providers   show configured provider status")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "auth (env vars):")
	fmt.Fprintln(os.Stderr, "  ANTHROPIC_API_KEY     claude-haiku / sonnet / opus")
	fmt.Fprintln(os.Stderr, "  OPENAI_API_KEY        gpt-4o / gpt-4o-mini")
	fmt.Fprintln(os.Stderr, "  OPENROUTER_API_KEY    any model via openrouter.ai")
}

// cmdRoute routes a task through the admission pipeline with live progress display.
func cmdRoute(args []string) {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	taskObj := fs.String("task", "", "task objective (required)")
	kind := fs.String("kind", "code-change", "task kind: extract|summarize|code-change|debug|plan|review|refactor")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "max cost in USD (0 = no limit)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-model admission timeout")
	quiet := fs.Bool("quiet", false, "suppress routing animation (useful in scripts)")
	noResume := fs.Bool("no-resume", false, "ignore any saved checkpoint and start fresh")
	_ = fs.Parse(args)

	if *taskObj == "" {
		fmt.Fprintln(os.Stderr, "error: --task is required")
		fs.Usage()
		os.Exit(1)
	}

	setupLogger()

	reg, err := buildProviderRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// checkpoint: resume interrupted routing sessions
	hash := taskHash(*taskObj, *kind, *risk, *maxCost)
	cp := &Checkpoint{Hash: hash, Objective: *taskObj}
	if !*noResume {
		if saved, ok := loadCheckpoint(hash); ok {
			if !*quiet {
				fmt.Println()
				fmt.Printf("  Resuming previous session (%d model(s) already tried).\n", len(saved.Tried))
				for _, t := range saved.Tried {
					status := "✗ rejected"
					if t.Accepted {
						status = "✓ accepted"
					}
					fmt.Printf("    %-16s  %s\n", t.Model, status)
				}
			}
			cp = saved
		}
	}

	// signal handling: Ctrl+C saves checkpoint instead of losing progress
	sigCtx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()
	ctx, cancel := context.WithTimeout(sigCtx, *timeout)
	defer cancel()

	modelReg := router.NewRegistry()
	gate := router.NewAdmissionGateWithFactory(reg)
	store := router.NewMemoryStore()
	mgr := router.NewManager(modelReg, gate, store)

	render := NewRenderer(*quiet)
	render.PrintTaskHeader(*taskObj, *kind, *risk, *maxCost)

	mgr.OnEvent = func(e router.ProgressEvent) {
		render.OnEvent(e)
		logEvent(*taskObj, *kind, *risk, e)

		// track admission decisions for checkpoint
		switch e.Kind {
		case router.EventAskAccept:
			cp.add(e.Model, true, nil)
		case router.EventAskReject, router.EventAskError:
			cp.add(e.Model, false, e.Reasons)
		}
	}

	spec := router.TaskSpec{
		ID:         hash,
		Kind:       router.TaskKind(*kind),
		Objective:  *taskObj,
		Risk:       router.Risk(*risk),
		MaxCostUSD: *maxCost,
		SkipModels: cp.triedNames(),
	}

	model, decision, err := mgr.Route(ctx, spec)

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		saveCheckpoint(cp)
		if !*quiet {
			fmt.Println()
			fmt.Println("  Routing interrupted. Progress saved.")
			fmt.Printf("  Run the same command to resume where you left off.\n")
			fmt.Printf("  Run with --no-resume to start fresh.\n")
		}
		os.Exit(130)
	}
	if errors.Is(err, router.ErrNoCandidate) {
		deleteCheckpoint(hash)
		if !*quiet {
			fmt.Println()
			fmt.Println("  No model accepted this task.")
			fmt.Println("  Try adjusting --max-cost, --risk, or --kind.")
		} else {
			fmt.Fprintln(os.Stderr, "no candidate model accepted the task")
		}
		os.Exit(1)
	}
	if err != nil {
		deleteCheckpoint(hash)
		fmt.Fprintf(os.Stderr, "routing failed: %v\n", err)
		os.Exit(1)
	}

	deleteCheckpoint(hash)
	render.PrintResult(model, decision)

	// quiet mode: machine-readable single line
	if *quiet {
		fmt.Printf("%s\n", model.Name)
	}

	if routeLog != nil {
		routeLog.Info("route_done",
			"model", model.Name,
			"tier", model.Tier,
			"confidence", decision.Confidence,
			"task", *taskObj,
		)
	}
}

// cmdProviders prints which provider API keys are configured and their source.
func cmdProviders() {
	creds, _ := loadCredentials()
	fmt.Printf("%-14s  %-14s  %s\n", "provider", "status", "models")
	fmt.Printf("%-14s  %-14s  %s\n", "──────────────", "──────────────", "──────────────────────")
	for _, p := range knownProviders {
		switch {
		case os.Getenv(p.envKey) != "":
			fmt.Printf("%-14s  %-14s  %s\n", p.name, "env var", p.models)
		case creds[p.envKey] != "":
			fmt.Printf("%-14s  %-14s  %s\n", p.name, "veto login", p.models)
		default:
			fmt.Printf("%-14s  %-14s  run 'veto login'\n", p.name, "not set")
		}
	}
}

// providerRegistry maps model names to their executors.
// Lives here so cmd imports both pkg/executor and pkg/router without circular deps.
type providerRegistry struct {
	executors map[string]router.Executor
}

func (r *providerRegistry) For(name string) (router.Executor, bool) {
	e, ok := r.executors[name]
	return e, ok
}

func buildProviderRegistry() (*providerRegistry, error) {
	creds, _ := loadCredentials() // best-effort; env vars take precedence
	reg := &providerRegistry{executors: make(map[string]router.Executor)}

	if key := getKey("ANTHROPIC_API_KEY", creds); key != "" {
		reg.executors["haiku"] = executor.NewAnthropicExecutor(key, "claude-haiku-4-5-20251001")
		reg.executors["sonnet"] = executor.NewAnthropicExecutor(key, "claude-sonnet-4-6")
		reg.executors["opus"] = executor.NewAnthropicExecutor(key, "claude-opus-4-8")
	}
	if key := getKey("OPENAI_API_KEY", creds); key != "" {
		reg.executors["gpt-4o"] = executor.NewOpenAIExecutor(key, "gpt-4o")
		reg.executors["gpt-4o-mini"] = executor.NewOpenAIExecutor(key, "gpt-4o-mini")
	}
	if key := getKey("OPENROUTER_API_KEY", creds); key != "" {
		reg.executors["llama-3.1-405b"] = executor.NewOpenRouterExecutor(key, "meta-llama/llama-3.1-405b")
	}

	if len(reg.executors) == 0 {
		return nil, fmt.Errorf("no API keys configured — run 'veto login' or set ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY")
	}
	return reg, nil
}
