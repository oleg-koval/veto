package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router"
)

func main() {
	if maybeOfferAutomaticUpdate(os.Args[1:]) {
		return
	}
	if len(os.Args) < 2 {
		printUsage(os.Stdout)
		os.Exit(0)
	}
	if rootHelpRequested(os.Args[1]) {
		printUsage(os.Stdout)
		return
	}
	// Notify once if new skills are pending approval (non-blocking).
	if os.Args[1] != "setup" && os.Args[1] != "version" && os.Args[1] != "--version" && os.Args[1] != "benchmark" && os.Args[1] != "verify-models" && os.Args[1] != "doctor" {
		checkPendingSkills()
	}
	switch os.Args[1] {
	case "route":
		cmdRoute(os.Args[2:])
	case "benchmark":
		cmdBenchmark(os.Args[2:])
	case "verify-models":
		cmdVerifyModels(os.Args[2:])
	case "doctor":
		cmdDoctor(os.Args[2:])
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
	case "exec":
		cmdExec(os.Args[2:])
	case "setup":
		cmdSetup()
	case "disable":
		cmdDisable(os.Args[2:])
	case "enable":
		cmdEnable(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("veto " + resolvedVersion())
	case "install-git-hook":
		cmdInstallGitHook(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func rootHelpRequested(arg string) bool {
	switch arg {
	case "help", "--help", "-h":
		return true
	default:
		return false
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
	fmt.Fprintln(o, "  setup              discover and approve skills from your skill directories")
	fmt.Fprintln(o, "  run                route a task and execute it — prints the model's response")
	fmt.Fprintln(o, "  exec               execute a veto plan file step-by-step")
	fmt.Fprintln(o, "  route              route a task to the best available model (no execution)")
	fmt.Fprintln(o, "  benchmark          replay an offline routing corpus and emit JSON metrics")
	fmt.Fprintln(o, "  verify-models      verify catalog IDs against one provider account")
	fmt.Fprintln(o, "  doctor             diagnose installation and ~/.veto integrity")
	fmt.Fprintln(o, "  providers          show which providers are configured")
	fmt.Fprintln(o, "  version            print veto version")
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
	fmt.Fprintln(o, "  --max-cost  estimated preflight ceiling in USD, e.g. 0.01  (default: none)")
	fmt.Fprintln(o, "  --quiet     print only the selected model name — useful in scripts")
	fmt.Fprintln(o, "  --json      print one JSON result line — implies --quiet and --no-resume")
	fmt.Fprintln(o, "  --no-resume ignore a saved checkpoint and start fresh")
	fmt.Fprintln(o, "  --dashboard open a live routing view in your browser")
	fmt.Fprintln(o, "  --criteria  comma-separated acceptance criteria; run a QA review after execution")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "SKILLS")
	fmt.Fprintln(o, "  Skills are instruction snippets injected into the executor prompt.")
	fmt.Fprintln(o, "  veto scans skill directories you approve: ~/.veto/skills/ (auto-approved,")
	fmt.Fprintln(o, "  veto-generated) and any other directories added via 'veto setup'.")
	fmt.Fprintln(o, "  New skills found at startup are flagged for approval on next 'veto setup'.")
	fmt.Fprintln(o, "  Run 'veto setup' to discover existing skills in ~/.claude/skills/ or elsewhere.")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "PROVIDERS")
	fmt.Fprintln(o, "  ANTHROPIC_API_KEY     Claude Haiku · Sonnet · Opus")
	fmt.Fprintln(o, "  OPENAI_API_KEY        OpenAI catalog models")
	fmt.Fprintf(o, "  OPENROUTER_API_KEY    %s\n", catalogModelDescription("openrouter"))
	fmt.Fprintln(o, "  XAI_API_KEY           Grok 4.5, 4.3, 3, 3-mini (xAI)")
	fmt.Fprintln(o, "  (or run 'veto login' — veto stores keys in ~/.veto/credentials.json)")
}

// cmdRoute routes a task through the admission pipeline with live progress display.
func cmdRoute(args []string) {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	taskObj := fs.String("task", "", "task objective (or pass as a positional argument)")
	kindFlag := fs.String("kind", "", "task kind (auto-detected from objective if omitted): extract|summarize|code-change|debug|plan|review|refactor")
	risk := fs.String("risk", "medium", "risk level: low|medium|high")
	maxCost := fs.Float64("max-cost", 0, "estimated preflight cost ceiling in USD (0 = none)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-model admission timeout")
	quiet := fs.Bool("quiet", false, "suppress routing animation (useful in scripts)")
	jsonOut := fs.Bool("json", false, "emit the routing result as a single JSON line (implies --quiet and --no-resume; ideal for scripting/agent infra)")
	noResume := fs.Bool("no-resume", false, "ignore any saved checkpoint and start fresh")
	dashFlag := fs.Bool("dashboard", false, "watch routing live in a local web page")
	_ = fs.Parse(args)

	// --json is a scripting mode: no animation, no interactive checkpoint resume.
	if *jsonOut {
		*quiet = true
		*noResume = true
	}

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
	complexity := string(router.InferComplexity(objective, router.TaskKind(kind)))

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
	ctx := sigCtx

	// only route across models whose provider is configured
	modelReg := router.NewRegistryFromModels(reg.modelCaps())
	gate := router.NewAdmissionGateWithFactory(reg)
	gate.SetTimeout(*timeout)
	// FileStore persists accept/reject history so it compounds across runs and
	// feeds future ranking — see NewManager wiring below.
	store := router.NewFileStore(historyPath())
	mgr := router.NewManager(modelReg, gate, store)

	render := NewRenderer(*quiet)
	render.PrintTaskHeader(objective, kind, *risk, complexity, *maxCost, kindInferred)

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

	var providerErrors []routeJSONProviderError
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
		case router.EventAskReject:
			cp.add(e.Model, false, e.Reasons)
		case router.EventAskError:
			cp.add(e.Model, false, e.Reasons)
			providerErrors = append(providerErrors, routeJSONProviderError{
				Model: e.Model, Detail: normalizeErrorDetail(e.Detail),
			})
		}
	}

	spec := router.TaskSpec{
		ID:         hash,
		Kind:       router.TaskKind(kind),
		Complexity: router.Complexity(complexity),
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
		if *jsonOut {
			printRouteJSONError(os.Stdout, "no_candidate", kind, *risk, complexity, providerErrors)
			os.Exit(1)
		}
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

	// json mode: single machine-readable line for scripting / agent infra
	if *jsonOut {
		printRouteJSONSuccess(os.Stdout, model, kind, *risk, complexity, decision.Confidence, saved)
	} else if *quiet {
		// quiet mode: machine-readable single line (model name only)
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

type routeJSONSuccess struct {
	Model      string  `json:"model"`
	Tier       string  `json:"tier"`
	Kind       string  `json:"kind"`
	Risk       string  `json:"risk"`
	Complexity string  `json:"complexity"`
	Confidence float64 `json:"confidence"`
	SavedUSD   float64 `json:"saved_usd"`
}

type routeJSONError struct {
	Error          string                   `json:"error"`
	Kind           string                   `json:"kind"`
	Risk           string                   `json:"risk"`
	Complexity     string                   `json:"complexity"`
	ProviderErrors []routeJSONProviderError `json:"provider_errors,omitempty"`
}

type routeJSONProviderError struct {
	Model  string `json:"model"`
	Detail string `json:"detail"`
}

func printRouteJSONSuccess(w io.Writer, model router.ModelCapabilities, kind, risk, complexity string, confidence, savedUSD float64) {
	_ = json.NewEncoder(w).Encode(routeJSONSuccess{
		Model:      model.Name,
		Tier:       model.Tier,
		Kind:       kind,
		Risk:       risk,
		Complexity: complexity,
		Confidence: confidence,
		SavedUSD:   savedUSD,
	})
}

func printRouteJSONError(w io.Writer, code, kind, risk, complexity string, providerErrors []routeJSONProviderError) {
	_ = json.NewEncoder(w).Encode(routeJSONError{
		Error:          code,
		Kind:           kind,
		Risk:           risk,
		Complexity:     complexity,
		ProviderErrors: providerErrors,
	})
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

// version is set at build time via -ldflags "-X main.version=<version>".
var version = "dev"

func resolvedVersion() string {
	moduleVersion := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = info.Main.Version
	}
	return effectiveVersion(version, moduleVersion)
}

func effectiveVersion(linkedVersion, moduleVersion string) string {
	if linkedVersion != "" && linkedVersion != "dev" {
		return strings.TrimPrefix(linkedVersion, "v")
	}
	if moduleVersion != "" && moduleVersion != "(devel)" {
		return strings.TrimPrefix(moduleVersion, "v")
	}
	return "dev"
}

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
		models := catalogModelDescription(p.provider)
		// Anthropic: check subscription mode before API key
		if p.envKey == "ANTHROPIC_API_KEY" {
			switch {
			case os.Getenv("CLAUDE_SUBSCRIPTION") == "true" || creds["CLAUDE_SUBSCRIPTION"] == "true":
				fmt.Printf("%-14s  %-18s  %s\n", p.name, "subscription (cli)", "Claude Haiku, Sonnet, Opus")
				configured++
			case os.Getenv(p.envKey) != "":
				fmt.Printf("%-14s  %-18s  %s\n", p.name, "env var", models)
				configured++
			case creds[p.envKey] != "":
				fmt.Printf("%-14s  %-18s  %s\n", p.name, "veto login", models)
				configured++
			default:
				fmt.Printf("%-14s  %-18s  run 'veto login'\n", p.name, "not set")
			}
			continue
		}
		switch {
		case os.Getenv(p.envKey) != "":
			fmt.Printf("%-14s  %-18s  %s\n", p.name, "env var", models)
			configured++
		case creds[p.envKey] != "":
			fmt.Printf("%-14s  %-18s  %s\n", p.name, "veto login", models)
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

func catalogModelDescription(provider string) string {
	var names []string
	for _, model := range router.NewRegistry().All() {
		if model.Provider == provider {
			names = append(names, model.Name)
		}
	}
	if provider == "openrouter" {
		noun := "models"
		if len(names) == 1 {
			noun = "model"
		}
		return fmt.Sprintf("%d routable %s: %s", len(names), noun, strings.Join(names, ", "))
	}
	return strings.Join(names, ", ")
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
	for name, c := range r.caps {
		if exec := r.executors[name]; exec != nil {
			if tools, ok := exec.(executor.ToolProvider); ok {
				c.SupportsTools = append([]string(nil), tools.EffectiveTools()...)
			} else {
				c.SupportsTools = nil
			}
		}
		caps = append(caps, c)
	}
	return caps
}

func buildProviderRegistry() (*providerRegistry, error) {
	creds, _ := loadCredentials() // best-effort; env vars take precedence
	catalog := router.NewRegistry()
	disabled := loadDisabledModels()
	reg := &providerRegistry{
		executors: make(map[string]router.Executor),
		caps:      make(map[string]router.ModelCapabilities),
	}

	addBuiltin := func(model router.ModelCapabilities, exec router.Executor) {
		if disabled[model.Name] {
			return
		}
		reg.executors[model.Name] = exec
		reg.caps[model.Name] = model
	}

	// Subscription mode: use claude CLI (flat-fee, $0 marginal) instead of API key.
	// Subscription takes precedence over API key when both are present.
	subscription := creds["CLAUDE_SUBSCRIPTION"] == "true" || os.Getenv("CLAUDE_SUBSCRIPTION") == "true"
	providerKeys := map[string]string{
		"anthropic":  getKey("ANTHROPIC_API_KEY", creds),
		"openai":     getKey("OPENAI_API_KEY", creds),
		"openrouter": getKey("OPENROUTER_API_KEY", creds),
	}
	for _, model := range catalog.All() {
		var modelExecutor router.Executor
		switch model.Provider {
		case "anthropic":
			if subscription {
				modelExecutor = executor.NewClaudeCLIExecutor(model.APIModel)
			} else if key := providerKeys[model.Provider]; key != "" {
				modelExecutor = executor.NewAnthropicExecutor(key, model.APIModel)
			}
		case "openai":
			if key := providerKeys[model.Provider]; key != "" {
				modelExecutor = executor.NewOpenAIExecutor(key, model.APIModel)
			}
		case "openrouter":
			if key := providerKeys[model.Provider]; key != "" {
				modelExecutor = executor.NewOpenRouterExecutor(key, model.APIModel)
			}
		}
		if modelExecutor != nil {
			addBuiltin(model, modelExecutor)
		}
	}
	if key := getKey("XAI_API_KEY", creds); key != "" {
		for _, name := range []string{"grok-3-mini", "grok-3", "grok-4.3", "grok-4.5"} {
			if model, ok := catalog.ByName(name); ok {
				addBuiltin(model, executor.NewXAIExecutor(key, model.Name))
			}
		}
	}

	// Local / self-hosted models (OpenAI-compatible endpoints).
	locals, _ := loadLocalModels()
	for _, lm := range locals {
		reg.executors[lm.Name] = executor.NewOpenAICompatibleExecutor(lm.APIKey, lm.Model, lm.Endpoint)
		reg.caps[lm.Name] = lm.capabilities()
	}

	if len(reg.executors) == 0 {
		return nil, fmt.Errorf("no providers configured — run 'veto login' or set ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY / XAI_API_KEY")
	}
	return reg, nil
}

// loadDisabledModels reads the "disabled_models" list from ~/.veto/config.json.
func loadDisabledModels() map[string]bool {
	data, err := os.ReadFile(vetoCfgPath())
	if err != nil {
		return nil
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return nil
	}
	raw, ok := full["disabled_models"]
	if !ok {
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// saveDisabledModels writes the "disabled_models" list to ~/.veto/config.json (merges).
func saveDisabledModels(names []string) error {
	path := vetoCfgPath()
	var full map[string]json.RawMessage
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &full)
	}
	if full == nil {
		full = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return err
	}
	full["disabled_models"] = raw
	out, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

// cmdDisable disables one or more named models so veto never routes to them.
func cmdDisable(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: veto disable <model> [model ...]")
		os.Exit(1)
	}
	disabled := loadDisabledModels()
	if disabled == nil {
		disabled = map[string]bool{}
	}
	for _, name := range args {
		disabled[name] = true
	}
	list := make([]string, 0, len(disabled))
	for name := range disabled {
		list = append(list, name)
	}
	if err := saveDisabledModels(list); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, name := range args {
		fmt.Printf("  ✗ %s disabled — veto will not route to it\n", name)
	}
}

// cmdEnable re-enables one or more previously disabled models.
func cmdEnable(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: veto enable <model> [model ...]")
		os.Exit(1)
	}
	disabled := loadDisabledModels()
	toRemove := make(map[string]bool, len(args))
	for _, name := range args {
		toRemove[name] = true
	}
	list := make([]string, 0, len(disabled))
	for name := range disabled {
		if !toRemove[name] {
			list = append(list, name)
		}
	}
	if err := saveDisabledModels(list); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, name := range args {
		fmt.Printf("  ✓ %s enabled\n", name)
	}
}
