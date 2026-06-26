package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	fmt.Fprintln(os.Stderr, "  route       route a task to the best available model")
	fmt.Fprintln(os.Stderr, "  providers   show configured provider status")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "auth (env vars):")
	fmt.Fprintln(os.Stderr, "  ANTHROPIC_API_KEY     claude-haiku / sonnet / opus")
	fmt.Fprintln(os.Stderr, "  OPENAI_API_KEY        gpt-4o / gpt-4o-mini")
	fmt.Fprintln(os.Stderr, "  OPENROUTER_API_KEY    any model via openrouter.ai")
}

// cmdRoute routes a task through the admission pipeline and prints the accepted model.
func cmdRoute(args []string) {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	task := fs.String("task", "", "task objective (required)")
	kind := fs.String("kind", "code-change", "task kind: extract|summarize|code-change|debug|plan|review|refactor")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "max cost in USD (0 = no limit)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-model admission timeout")
	_ = fs.Parse(args)

	if *task == "" {
		fmt.Fprintln(os.Stderr, "error: --task is required")
		fs.Usage()
		os.Exit(1)
	}

	reg, err := buildProviderRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	modelReg := router.NewRegistry()
	gate := router.NewAdmissionGateWithFactory(reg)
	store := router.NewMemoryStore()
	mgr := router.NewManager(modelReg, gate, store)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	spec := router.TaskSpec{
		ID:         "cli",
		Kind:       router.TaskKind(*kind),
		Objective:  *task,
		Risk:       router.Risk(*risk),
		MaxCostUSD: *maxCost,
	}

	model, decision, err := mgr.Route(ctx, spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "routing failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("model:      %s (%s tier)\n", model.Name, model.Tier)
	fmt.Printf("confidence: %.0f%%\n", decision.Confidence*100)
	if decision.EstimatedTokens > 0 {
		fmt.Printf("est tokens: %d\n", decision.EstimatedTokens)
	}
	if decision.EstimatedCostUSD > 0 {
		fmt.Printf("est cost:   $%.4f\n", decision.EstimatedCostUSD)
	}
}

// cmdProviders prints which provider API keys are configured.
func cmdProviders() {
	type entry struct{ name, envVar, models string }
	entries := []entry{
		{"anthropic", "ANTHROPIC_API_KEY", "haiku, sonnet, opus"},
		{"openai", "OPENAI_API_KEY", "gpt-4o, gpt-4o-mini"},
		{"openrouter", "OPENROUTER_API_KEY", "any openrouter model"},
	}
	fmt.Printf("%-14s  %-12s  %s\n", "provider", "status", "models")
	fmt.Printf("%-14s  %-12s  %s\n", "──────────────", "────────────", "──────────────────────")
	for _, e := range entries {
		if os.Getenv(e.envVar) != "" {
			fmt.Printf("%-14s  %-12s  %s\n", e.name, "configured", e.models)
		} else {
			fmt.Printf("%-14s  %-12s  set %s\n", e.name, "not set", e.envVar)
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
	reg := &providerRegistry{executors: make(map[string]router.Executor)}

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		reg.executors["haiku"] = executor.NewAnthropicExecutor(key, "claude-haiku-4-5-20251001")
		reg.executors["sonnet"] = executor.NewAnthropicExecutor(key, "claude-sonnet-4-6")
		reg.executors["opus"] = executor.NewAnthropicExecutor(key, "claude-opus-4-8")
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		reg.executors["gpt-4o"] = executor.NewOpenAIExecutor(key, "gpt-4o")
		reg.executors["gpt-4o-mini"] = executor.NewOpenAIExecutor(key, "gpt-4o-mini")
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		reg.executors["llama-3.1-405b"] = executor.NewOpenRouterExecutor(key, "meta-llama/llama-3.1-405b")
	}

	if len(reg.executors) == 0 {
		return nil, fmt.Errorf("no API keys configured — set ANTHROPIC_API_KEY, OPENAI_API_KEY, or OPENROUTER_API_KEY")
	}
	return reg, nil
}
