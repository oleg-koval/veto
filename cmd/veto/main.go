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

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stdout)
		os.Exit(0)
	}
	switch os.Args[1] {
	case "route":
		cmdRoute(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "providers":
		cmdProviders()
	case "login":
		cmdLogin()
	case "logout":
		if len(os.Args) > 2 {
			cmdLogout(os.Args[2:])
		} else {
			cmdLogout(nil)
		}
	case "install-git-hook":
		cmdInstallGitHook(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func printUsage(w io.Writer) {
	o := w
	fmt.Fprintln(o, "veto — route tasks to the right AI model, automatically")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "  Each candidate model is asked whether it accepts the task.")
	fmt.Fprintln(o, "  The first to accept — with ≥70% confidence — is selected.")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "USAGE")
	fmt.Fprintln(o, "  veto <command> [flags]")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "COMMANDS")
	fmt.Fprintln(o, "  login              connect a provider (opens browser, masked key input)")
	fmt.Fprintln(o, "  logout             remove a configured provider or local model")
	fmt.Fprintln(o, "  exec               execute a veto plan file step-by-step")
	fmt.Fprintln(o, "  run                route a task and execute it — prints the model's response")
	fmt.Fprintln(o, "  route              route a task to the best available model (no execution)")
	fmt.Fprintln(o, "  providers          show which providers are configured")
	fmt.Fprintln(o, "  install-git-hook   add veto to your git workflow")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "QUICK START")
	fmt.Fprintln(o, "  veto login")
	fmt.Fprintln(o, `  veto run "refactor the auth middleware to use JWT"`)
	fmt.Fprintln(o, `  veto run --kind debug --risk high "explain the race condition in sync.go"`)
	fmt.Fprintln(o, `  veto route "refactor the auth middleware to use JWT"   # pick model only`)
	fmt.Fprintln(o)
	fmt.Fprintln(o, "ROUTE FLAGS")
	fmt.Fprintln(o, "  --kind      extract|summarize|code-change|debug|plan|review|refactor")
	fmt.Fprintln(o, "              (auto-detected from the task text if omitted)")
	fmt.Fprintln(o, "  --risk      low|medium|high  (default: medium)")
	fmt.Fprintln(o, "  --max-cost  max spend in USD, e.g. --max-cost 0.01  (default: no limit)")
	fmt.Fprintln(o, "  --quiet     print only the selected model name — useful in scripts")
	fmt.Fprintln(o, "  --no-resume ignore a saved checkpoint and start fresh")
	fmt.Fprintln(o, "  --dashboard open a live routing view in your browser")
	fmt.Fprintln(o, "  --criteria  comma-separated acceptance criteria; run a QA review after execution")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "SKILLS")
	fmt.Fprintln(o, "  Skills are instruction snippets injected into the executor prompt.")
	fmt.Fprintln(o, "  veto matches skills in ~/.veto/skills/ by task kind and keywords.")
	fmt.Fprintln(o, "  If none match, a skill is generated and saved there for future reuse.")
	fmt.Fprintln(o, "  To remove a skill: delete its .md file in ~/.veto/skills/.")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "PROVIDERS")
	fmt.Fprintln(o, "  ANTHROPIC_API_KEY     Claude Haiku · Sonnet · Opus")
	fmt.Fprintln(o, "  OPENAI_API_KEY        GPT-4o · GPT-4o mini")
	fmt.Fprintln(o, "  OPENROUTER_API_KEY    Llama, Mistral, Gemini, and 100+ more")
	fmt.Fprintln(o, "  (or run 'veto login' — veto stores keys in ~/.veto/credentials.json)")
}

// cmdRoute routes a task through the admission pipeline with live progress display.
func cmdRoute(args []string) {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	taskObj := fs.String("task", "", "task objective (or pass as a positional argument)")
	kindFlag := fs.String("kind", "", "task kind (auto-detected from objective if omitted): extract|summarize|code-change|debug|plan|review|refactor")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "max cost in USD (0 = no limit)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-model admission timeout")
	quiet := fs.Bool("quiet", false, "suppress routing animation (useful in scripts)")
	noResume := fs.Bool("no-resume", false, "ignore any saved checkpoint and start fresh")
	dashFlag := fs.Bool("dashboard", false, "watch routing live in a local web page")
	_ = fs.Parse(args)

	// objective: --task wins, else the positional args joined
	objective := *taskObj
	if objective == "" {
		objective = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if objective == "" {
		fmt.Fprintln(os.Stderr, "error: provide a task objective via --task or as an argument")
		fs.Usage()
		os.Exit(1)
	}

	// kind: explicit --kind wins, else inferred from the objective text
	kind := *kindFlag
	kindInferred := kind == ""
	if kindInferred {
		kind = inferKind(objective)
	}

	setupLogger()

	reg, err := buildProviderRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// checkpoint: resume interrupted routing sessions
	hash := taskHash(objective, kind, *risk, *maxCost)
	cp := &Checkpoint{Hash: hash, Objective: objective}
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

	// only route across models whose provider is configured
	modelReg := router.NewRegistryFromModels(reg.modelCaps())
	gate := router.NewAdmissionGateWithFactory(reg)
	// FileStore persists accept/reject history so it compounds across runs and
	// feeds future ranking — see NewManager wiring below.
	store := router.NewFileStore(historyPath())
	mgr := router.NewManager(modelReg, gate, store)

	render := NewRenderer(*quiet)
	render.PrintTaskHeader(objective, kind, *risk, *maxCost, kindInferred)

	// optional live web dashboard
	var dash *dashboard
	var dashURL string
	if *dashFlag {
		dash = newDashboard()
		if url, derr := dash.start(objective, kind, *risk); derr == nil {
			dashURL = url
			fmt.Printf("\n  Dashboard: %s\n", dashURL)
			openBrowser(dashURL)
		} else {
			fmt.Fprintln(os.Stderr, "  (dashboard failed to start:", derr, ")")
			dash = nil
		}
	}

	mgr.OnEvent = func(e router.ProgressEvent) {
		render.OnEvent(e)
		logEvent(objective, kind, *risk, e)
		if dash != nil {
			dash.OnEvent(e)
		}

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
		Kind:       router.TaskKind(kind),
		Objective:  objective,
		Risk:       router.Risk(*risk),
		MaxCostUSD: *maxCost,
		SkipModels: cp.triedNames(),
	}

	model, decision, err := mgr.Route(ctx, spec)
	// persist history regardless of outcome — os.Exit below skips defers
	_ = store.Save()

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
		if dash != nil {
			dash.sendResult("", "", 0, false)
			keepDashboardAlive(dashURL)
		}
		os.Exit(1)
	}
	if err != nil {
		deleteCheckpoint(hash)
		fmt.Fprintf(os.Stderr, "routing failed: %v\n", err)
		os.Exit(1)
	}

	deleteCheckpoint(hash)

	// reward: show what routing to this model saved vs always reaching for opus
	saved := 0.0
	if opus, ok := modelReg.ByName("opus"); ok && model.Name != opus.Name {
		saved = router.EstimatedCost(opus, spec) - router.EstimatedCost(model, spec)
	}
	render.PrintResult(model, decision, saved)

	// quiet mode: machine-readable single line
	if *quiet {
		fmt.Printf("%s\n", model.Name)
	}

	if routeLog != nil {
		routeLog.Info("route_done",
			"model", model.Name,
			"tier", model.Tier,
			"confidence", decision.Confidence,
			"task", objective,
		)
	}

	if dash != nil {
		dash.sendResult(model.Name, model.Tier, saved, true)
		keepDashboardAlive(dashURL)
	}
}

// keepDashboardAlive blocks until the user interrupts, so the served page stays
// reachable after a fast route completes.
func keepDashboardAlive(url string) {
	fmt.Printf("\n  Dashboard live at %s — press Ctrl+C to stop.\n", url)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
}

// inferKind guesses the task kind from the objective text so users don't have
// to pass --kind. ponytail: keyword heuristic; --kind always overrides it.
func inferKind(objective string) string {
	s := strings.ToLower(objective)
	switch {
	case containsAny(s, "fix", "bug", "debug", "error", "crash", "broken", "failing"):
		return "debug"
	case containsAny(s, "refactor", "clean up", "restructure", "rename", "extract method"):
		return "refactor"
	case containsAny(s, "summarize", "summary", "tl;dr", "recap"):
		return "summarize"
	case containsAny(s, "extract", "parse", "pull out", "scrape"):
		return "extract"
	case containsAny(s, "review", "audit", "critique", "check"):
		return "review"
	case containsAny(s, "plan", "design", "architect", "propose"):
		return "plan"
	default:
		return "code-change"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// historyPath is where routing decisions persist across runs.
func historyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "history.json")
}

// hookMarker identifies a prepare-commit-msg hook that veto installed, so we
// can safely overwrite our own hook but never someone else's.
const hookMarker = "installed by veto install-git-hook"

// cmdInstallGitHook writes a prepare-commit-msg hook that runs veto route before each commit.
func cmdInstallGitHook(args []string) {
	fs := flag.NewFlagSet("install-git-hook", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite an existing prepare-commit-msg hook")
	_ = fs.Parse(args)

	hookPath := filepath.Join(".git", "hooks", "prepare-commit-msg")
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "error: not inside a git repository")
		os.Exit(1)
	}

	// don't clobber a hook we didn't write — that could destroy the user's own hook
	if existing, err := os.ReadFile(hookPath); err == nil && !*force {
		if !strings.Contains(string(existing), hookMarker) {
			fmt.Fprintf(os.Stderr, "error: %s already exists and was not installed by veto\n", hookPath)
			fmt.Fprintln(os.Stderr, "re-run with --force to overwrite it")
			os.Exit(1)
		}
	}

	// objective comes from the staged stat; --kind is omitted so veto infers it
	script := "#!/bin/sh\n# " + hookMarker + "\n" +
		"MODEL=$(veto route --quiet --task \"$(git diff --cached --stat)\" 2>/dev/null)\n" +
		"if [ -n \"$MODEL\" ]; then\n  printf '\\n# veto suggested model: %s\\n' \"$MODEL\" >> \"$1\"\nfi\n"
	if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error writing hook: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Installed: %s\n", hookPath)
	fmt.Println("  veto will suggest a model in each commit message going forward.")
}

// cmdProviders prints which provider API keys are configured and their source.
func cmdProviders() {
	creds, _ := loadCredentials()
	fmt.Printf("%-14s  %-18s  %s\n", "provider", "status", "models")
	fmt.Printf("%-14s  %-18s  %s\n", "──────────────", "──────────────────", "──────────────────────")
	configured := 0
	for _, p := range knownProviders {
		// Anthropic: check subscription mode before API key
		if p.envKey == "ANTHROPIC_API_KEY" {
			switch {
			case os.Getenv("CLAUDE_SUBSCRIPTION") == "true" || creds["CLAUDE_SUBSCRIPTION"] == "true":
				fmt.Printf("%-14s  %-18s  %s\n", p.name, "subscription (cli)", "Claude Haiku, Sonnet, Opus")
				configured++
			case os.Getenv(p.envKey) != "":
				fmt.Printf("%-14s  %-18s  %s\n", p.name, "env var", p.models)
				configured++
			case creds[p.envKey] != "":
				fmt.Printf("%-14s  %-18s  %s\n", p.name, "veto login", p.models)
				configured++
			default:
				fmt.Printf("%-14s  %-18s  run 'veto login'\n", p.name, "not set")
			}
			continue
		}
		switch {
		case os.Getenv(p.envKey) != "":
			fmt.Printf("%-14s  %-18s  %s\n", p.name, "env var", p.models)
			configured++
		case creds[p.envKey] != "":
			fmt.Printf("%-14s  %-18s  %s\n", p.name, "veto login", p.models)
			configured++
		default:
			fmt.Printf("%-14s  %-18s  run 'veto login'\n", p.name, "not set")
		}
	}
	locals, _ := loadLocalModels()
	if len(locals) > 0 {
		fmt.Println()
		fmt.Printf("%-14s  %-18s  %s\n", "local model", "endpoint", "model id")
		fmt.Printf("%-14s  %-18s  %s\n", "─────────────", "──────────────────", "─────────────────")
		for _, lm := range locals {
			fmt.Printf("%-14s  %-18s  %s\n", lm.Name, lm.Endpoint, lm.Model)
		}
		configured += len(locals)
	}

	fmt.Println()
	if configured == 0 {
		fmt.Println("  No providers configured — run 'veto login' to get started.")
	} else {
		// build an accurate model count from the registry
		reg, err := buildProviderRegistry()
		if err == nil {
			fmt.Printf("  %d model(s) available for routing\n", len(reg.modelCaps()))
		}
	}
}

// providerRegistry maps model names to their executors and capabilities.
// Lives here so cmd imports both pkg/executor and pkg/router without circular deps.
type providerRegistry struct {
	executors map[string]router.Executor
	caps      map[string]router.ModelCapabilities
}

func (r *providerRegistry) For(name string) (router.Executor, bool) {
	e, ok := r.executors[name]
	return e, ok
}

// modelCaps returns the capability list for all configured models (built-ins + locals).
func (r *providerRegistry) modelCaps() []router.ModelCapabilities {
	caps := make([]router.ModelCapabilities, 0, len(r.caps))
	for _, c := range r.caps {
		caps = append(caps, c)
	}
	return caps
}

func buildProviderRegistry() (*providerRegistry, error) {
	creds, _ := loadCredentials() // best-effort; env vars take precedence
	catalog := router.NewRegistry()
	reg := &providerRegistry{
		executors: make(map[string]router.Executor),
		caps:      make(map[string]router.ModelCapabilities),
	}

	addBuiltin := func(name string, exec router.Executor) {
		reg.executors[name] = exec
		if m, ok := catalog.ByName(name); ok {
			reg.caps[name] = m
		}
	}

	// Subscription mode: use claude CLI (flat-fee, $0 marginal) instead of API key.
	// Subscription takes precedence over API key when both are present.
	if creds["CLAUDE_SUBSCRIPTION"] == "true" || os.Getenv("CLAUDE_SUBSCRIPTION") == "true" {
		addBuiltin("haiku", executor.NewClaudeCLIExecutor("claude-haiku-4-5-20251001"))
		addBuiltin("sonnet", executor.NewClaudeCLIExecutor("claude-sonnet-4-6"))
		addBuiltin("opus", executor.NewClaudeCLIExecutor("claude-opus-4-8"))
	} else if key := getKey("ANTHROPIC_API_KEY", creds); key != "" {
		addBuiltin("haiku", executor.NewAnthropicExecutor(key, "claude-haiku-4-5-20251001"))
		addBuiltin("sonnet", executor.NewAnthropicExecutor(key, "claude-sonnet-4-6"))
		addBuiltin("opus", executor.NewAnthropicExecutor(key, "claude-opus-4-8"))
	}
	if key := getKey("OPENAI_API_KEY", creds); key != "" {
		addBuiltin("gpt-4o", executor.NewOpenAIExecutor(key, "gpt-4o"))
		addBuiltin("gpt-4o-mini", executor.NewOpenAIExecutor(key, "gpt-4o-mini"))
	}
	if key := getKey("OPENROUTER_API_KEY", creds); key != "" {
		addBuiltin("llama-3.1-405b", executor.NewOpenRouterExecutor(key, "meta-llama/llama-3.1-405b"))
	}

	// Local / self-hosted models (OpenAI-compatible endpoints).
	locals, _ := loadLocalModels()
	for _, lm := range locals {
		reg.executors[lm.Name] = executor.NewOpenAICompatibleExecutor(lm.APIKey, lm.Model, lm.Endpoint)
		reg.caps[lm.Name] = lm.capabilities()
	}

	if len(reg.executors) == 0 {
		return nil, fmt.Errorf("no providers configured — run 'veto login' or set ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY")
	}
	return reg, nil
}
