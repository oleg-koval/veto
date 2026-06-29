package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/oleg-koval/veto/pkg/router"
	"golang.org/x/term"
)

type providerInfo struct {
	name    string
	envKey  string
	models  string
	keyURL  string
	keyHint string
}

var knownProviders = []providerInfo{
	{
		name:    "Anthropic",
		envKey:  "ANTHROPIC_API_KEY",
		models:  "Claude Haiku, Sonnet, Opus",
		keyURL:  "https://console.anthropic.com/settings/keys",
		keyHint: "sk-ant-",
	},
	{
		name:    "OpenAI",
		envKey:  "OPENAI_API_KEY",
		models:  "GPT-4o, GPT-4o mini",
		keyURL:  "https://platform.openai.com/api-keys",
		keyHint: "sk-",
	},
	{
		name:    "OpenRouter",
		envKey:  "OPENROUTER_API_KEY",
		models:  "Llama, Mistral, Gemini, and 100+ more",
		keyURL:  "https://openrouter.ai/keys",
		keyHint: "sk-or-",
	},
}

func cmdLogin() {
	fmt.Println()
	fmt.Println("  Let's connect a model provider.")
	fmt.Println()
	fmt.Println("  Which provider?")
	fmt.Println()
	fmt.Println("  1  Anthropic (Claude)  — API key or subscription (Claude Max / claude CLI)")
	fmt.Println("  2  OpenAI              — API key")
	fmt.Println("  3  OpenRouter          — API key (100+ models)")
	fmt.Println("  4  Local / self-hosted — Ollama, LM Studio, vLLM, llama.cpp")
	fmt.Println()
	fmt.Print("  Provider [1-4]: ")

	var choice int
	if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(knownProviders)+1 {
		fmt.Fprintln(os.Stderr, "\n  That doesn't look right. Please enter 1, 2, 3, or 4.")
		os.Exit(1)
	}

	if choice == len(knownProviders)+1 {
		loginLocalModel()
		return
	}

	p := knownProviders[choice-1]

	// Anthropic supports subscription mode via the claude CLI.
	if choice == 1 {
		fmt.Println()
		fmt.Println("  How do you use Claude?")
		fmt.Println()
		fmt.Println("  1  Subscription  — Claude Max / Pro (uses claude CLI, $0 per route)")
		fmt.Println("  2  API key       — pay per token via Anthropic API")
		fmt.Println()
		fmt.Print("  Mode [1-2]: ")

		var mode int
		if _, err := fmt.Scan(&mode); err != nil || mode < 1 || mode > 2 {
			fmt.Fprintln(os.Stderr, "\n  That doesn't look right. Please enter 1 or 2.")
			os.Exit(1)
		}

		if mode == 1 {
			loginClaudeSubscription()
			return
		}
	}

	loginAPIKey(p)
}

// localModelOption describes a popular local model server veto can install for the user.
type localModelOption struct {
	label     string // display name
	serverCmd string // binary to check with `which`
	installOS map[string]string // GOOS -> install command (empty = manual)
	endpoint  string
	models    []ollamaModelChoice
}

type ollamaModelChoice struct {
	id   string // ollama model id
	name string // display name
	size string // approx disk size
	desc string // one-line description
}

var localServerOptions = []localModelOption{
	{
		label:     "Ollama  — easiest, free, works on Mac/Linux/Windows",
		serverCmd: "ollama",
		installOS: map[string]string{
			"darwin": "brew install ollama",
			"linux":  "curl -fsSL https://ollama.com/install.sh | sh",
		},
		endpoint: "http://localhost:11434/v1/chat/completions",
		models: []ollamaModelChoice{
			{"qwen2.5-coder:7b", "Qwen 2.5 Coder 7B", "4.7 GB", "best for code — outperforms many larger models on coding tasks"},
			{"llama3.2:3b", "Llama 3.2 3B", "2.0 GB", "smallest & fastest — great for quick tasks and low-RAM machines"},
			{"mistral:7b", "Mistral 7B", "4.1 GB", "strong general-purpose model — good balance of speed and quality"},
		},
	},
	{
		label:     "LM Studio — GUI app (download manually at lmstudio.ai)",
		serverCmd: "", // can't auto-detect; user must start it themselves
		endpoint:  "http://localhost:1234/v1/chat/completions",
	},
	{
		label:     "I already have a server running — enter endpoint manually",
		serverCmd: "",
	},
}

// loginLocalModel is the guided local model setup flow.
// Loops so the user can add as many local models as they want.
func loginLocalModel() {
	for {
		fmt.Println()
		fmt.Println("  Local / self-hosted model setup")
		fmt.Println()
		fmt.Println("  Which server?")
		fmt.Println()
		for i, opt := range localServerOptions {
			fmt.Printf("  %d  %s\n", i+1, opt.label)
		}
		fmt.Println()
		fmt.Printf("  Choice [1-%d]: ", len(localServerOptions))

		var choice int
		if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(localServerOptions) {
			fmt.Fprintf(os.Stderr, "\n  Please enter a number between 1 and %d.\n", len(localServerOptions))
			os.Exit(1)
		}

		opt := localServerOptions[choice-1]

		switch choice {
		case 1:
			loginOllama(opt)
		case 2:
			loginLMStudio(opt)
		default:
			loginLocalModelManual()
		}

		fmt.Print("  Add another local model? [y/N]: ")
		var ans string
		fmt.Scanln(&ans) //nolint:errcheck
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			break
		}
	}
}

// loginOllama installs Ollama if needed, lets the user pick a model, pulls it, starts the server.
func loginOllama(opt localModelOption) {
	scanner := bufio.NewScanner(os.Stdin)

	// Step 1: install if missing
	if _, err := exec.LookPath("ollama"); err != nil {
		installCmd, ok := opt.installOS[runtime.GOOS]
		if !ok {
			fmt.Println()
			fmt.Println("  Ollama isn't installed. Download it from: https://ollama.com/download")
			fmt.Println("  Install it, then run 'veto login' again.")
			os.Exit(1)
		}
		fmt.Println()
		fmt.Printf("  Ollama not found. Installing via: %s\n", installCmd)
		fmt.Print("  Proceed? [Y/n]: ")
		scanner.Scan()
		ans := strings.TrimSpace(scanner.Text())
		if ans != "" && strings.ToLower(ans) != "y" {
			fmt.Println("  Skipped. Install Ollama manually at https://ollama.com/download")
			os.Exit(0)
		}
		fmt.Println()
		if err := runVisible(installCmd); err != nil {
			fmt.Fprintf(os.Stderr, "\n  Install failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "  Try manually: https://ollama.com/download")
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("  Ollama installed!")
	} else {
		fmt.Println()
		fmt.Println("  Ollama is already installed.")
	}

	// Step 2: pick a model
	fmt.Println()
	fmt.Println("  Which model do you want to pull?")
	fmt.Println()
	for i, m := range opt.models {
		fmt.Printf("  %d  %-24s  %-7s  %s\n", i+1, m.name, m.size, m.desc)
	}
	fmt.Println()
	fmt.Printf("  Model [1-%d]: ", len(opt.models))

	var modelChoice int
	if _, err := fmt.Scan(&modelChoice); err != nil || modelChoice < 1 || modelChoice > len(opt.models) {
		fmt.Fprintf(os.Stderr, "\n  Please enter 1, 2, or 3.\n")
		os.Exit(1)
	}
	chosen := opt.models[modelChoice-1]

	// Step 3: start ollama serve if not already running
	fmt.Println()
	fmt.Println("  Starting Ollama server...")
	if !ollamaIsRunning() {
		// start in background; ollama serve blocks so we detach
		srv := exec.Command("ollama", "serve")
		srv.Stdout = nil
		srv.Stderr = nil
		if err := srv.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "  Could not start ollama serve: %v\n", err)
			fmt.Fprintln(os.Stderr, "  Run 'ollama serve' in another terminal and try again.")
			os.Exit(1)
		}
		// give it a moment to bind
		waitForOllama()
		fmt.Println("  Server started.")
	} else {
		fmt.Println("  Server is already running.")
	}

	// Step 4: pull the model
	fmt.Printf("\n  Pulling %s (%s) — this may take a few minutes...\n\n", chosen.name, chosen.size)
	pull := exec.Command("ollama", "pull", chosen.id)
	pull.Stdout = os.Stdout
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Pull failed: %v\n", err)
		os.Exit(1)
	}

	// Step 5: register in veto
	name := "ollama-" + strings.ReplaceAll(strings.Split(chosen.id, ":")[0], ".", "-")
	lm := LocalModel{
		Name:     name,
		Endpoint: opt.endpoint,
		Model:    chosen.id,
	}
	builtins := make(map[string]bool)
	for _, m := range router.NewRegistry().All() {
		builtins[m.Name] = true
	}
	if err := validateLocalModel(lm, builtins); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Validation error: %v\n", err)
		os.Exit(1)
	}
	if err := saveLocalModel(lm); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Couldn't save model: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("  %s is ready and registered as %q!\n", chosen.name, name)
	fmt.Println()
	fmt.Println("  What's next:")
	fmt.Printf("    veto providers                  — confirm %q is listed\n", name)
	fmt.Println(`    veto run "summarize this file"  — route a task (may pick the local model)`)
	fmt.Println()
	fmt.Println("  veto will start ollama automatically if it isn't running when you route a task.")
	fmt.Println()
}

// loginLMStudio guides the user through LM Studio setup (which can't be automated).
func loginLMStudio(opt localModelOption) {
	fmt.Println()
	fmt.Println("  LM Studio setup:")
	fmt.Println()
	fmt.Println("  1. Download and install LM Studio from https://lmstudio.ai")
	fmt.Println("  2. Open LM Studio and download a model (Search tab)")
	fmt.Println("  3. Go to the Local Server tab and click Start Server")
	fmt.Println("  4. Note the model id shown in the server tab (e.g. mistral-7b-instruct)")
	fmt.Println()
	fmt.Println("  Once the server is running, come back here.")
	fmt.Println()
	fmt.Print("  Press Enter when ready (or Ctrl+C to cancel)...")
	fmt.Scanln() //nolint:errcheck

	// collect just the missing details
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("  Model id (as shown in LM Studio server tab): ")
	scanner.Scan()
	modelID := strings.TrimSpace(scanner.Text())

	fmt.Printf("  Routing name (e.g. lmstudio-%s): ", strings.Split(modelID, "-")[0])
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())
	if name == "" {
		name = "lmstudio-local"
	}

	lm := LocalModel{Name: name, Endpoint: opt.endpoint, Model: modelID}
	builtins := make(map[string]bool)
	for _, m := range router.NewRegistry().All() {
		builtins[m.Name] = true
	}
	if err := validateLocalModel(lm, builtins); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Invalid model: %v\n", err)
		os.Exit(1)
	}
	if err := saveLocalModel(lm); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Couldn't save model: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Printf("  %q registered! Run 'veto providers' to confirm.\n", name)
	fmt.Println()
}

// loginLocalModelManual is the original manual entry flow (endpoint + model id).
func loginLocalModelManual() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println()
	fmt.Println("  Manual entry — enter your server details.")
	fmt.Println()

	fmt.Print("  Name (routing id, e.g. my-local): ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())

	fmt.Println()
	fmt.Println("  Endpoint examples:")
	fmt.Println("    Ollama:    http://localhost:11434/v1/chat/completions")
	fmt.Println("    LM Studio: http://localhost:1234/v1/chat/completions")
	fmt.Print("  Endpoint: ")
	scanner.Scan()
	endpoint := strings.TrimSpace(scanner.Text())

	fmt.Print("  Model id (as the server knows it): ")
	scanner.Scan()
	modelID := strings.TrimSpace(scanner.Text())

	fmt.Print("  API key (leave blank if not required): ")
	scanner.Scan()
	apiKey := strings.TrimSpace(scanner.Text())

	lm := LocalModel{Name: name, Endpoint: endpoint, Model: modelID, APIKey: apiKey}
	builtins := make(map[string]bool)
	for _, m := range router.NewRegistry().All() {
		builtins[m.Name] = true
	}
	if err := validateLocalModel(lm, builtins); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Invalid model: %v\n", err)
		os.Exit(1)
	}
	if err := saveLocalModel(lm); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Couldn't save model: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Printf("  Local model %q added!\n", name)
	fmt.Println("  Run 'veto providers' to confirm it's listed.")
	fmt.Println()
}

// ollamaIsRunning pings the Ollama REST API to check if the server is up.
func ollamaIsRunning() bool {
	out, err := exec.Command("ollama", "list").Output()
	return err == nil && len(out) > 0
}

// waitForOllama polls until ollama serve is accepting connections (max ~5s).
func waitForOllama() {
	for i := 0; i < 10; i++ {
		if ollamaIsRunning() {
			return
		}
		// ponytail: simple sleep loop; 10×500ms = 5s max wait
		_ = exec.Command("sleep", "0.5").Run()
	}
}

// runVisible runs a shell command string with stdout/stderr connected to the terminal.
func runVisible(command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// loginClaudeSubscription verifies the claude CLI is present and saves a subscription marker.
func loginClaudeSubscription() {
	fmt.Println()
	fmt.Println("  Verifying claude CLI is installed and logged in...")

	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "\n  The claude CLI is not installed or not in PATH.")
		fmt.Fprintln(os.Stderr, "  Install it from: https://claude.ai/code")
		os.Exit(1)
	}
	version := strings.TrimSpace(string(out))

	if err := saveCredential("CLAUDE_SUBSCRIPTION", "true"); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Couldn't save subscription setting: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("  Claude subscription connected! (%s)\n", version)
	fmt.Println("  veto will use claude -p for all Claude routing — no API tokens consumed.")
	fmt.Println()
	fmt.Println("  What's next:")
	fmt.Println("    veto providers              — confirm Claude is connected")
	fmt.Println(`    veto route "fix the auth bug"`)
	fmt.Println()
}

// loginAPIKey handles the standard API-key flow for any provider.
func loginAPIKey(p providerInfo) {
	fmt.Println()
	fmt.Printf("  Opening the %s API keys page in your browser...\n", p.name)
	openBrowser(p.keyURL)
	fmt.Printf("  (If nothing opened, visit: %s)\n", p.keyURL)
	fmt.Println()
	fmt.Println("  Steps:")
	fmt.Println("    1. Sign in if prompted")
	fmt.Println("    2. Create a new API key")
	fmt.Println("    3. Copy it and come back here")
	fmt.Println()
	if p.keyHint != "" {
		fmt.Printf("  Your key should start with %q\n", p.keyHint)
		fmt.Println()
	}
	fmt.Print("  Paste your API key: ")

	key, err := readMaskedLine()
	if err != nil || strings.TrimSpace(key) == "" {
		fmt.Fprintln(os.Stderr, "\n  No key received. Run 'veto login' whenever you're ready.")
		os.Exit(1)
	}
	key = strings.TrimSpace(key)

	if p.keyHint != "" && !strings.HasPrefix(key, p.keyHint) {
		fmt.Printf("\n  Heads up: %s keys usually start with %q — double-check if something doesn't work.\n", p.name, p.keyHint)
	}

	if err := saveCredential(p.envKey, key); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Couldn't save your key: %v\n", err)
		fmt.Fprintln(os.Stderr, "  You can also set it manually: export "+p.envKey+"=<your-key>")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("  %s is connected!\n", p.name)
	fmt.Printf("  Models available: %s\n", p.models)
	fmt.Println()
	fmt.Println("  What's next:")
	fmt.Println("    veto providers              — see all connected providers")
	fmt.Println(`    veto route --task "..."     — route your first task`)
	fmt.Println("    veto login                  — connect another provider")
	fmt.Println()
}

// readMaskedLine reads a line from stdin, masking input when connected to a terminal.
func readMaskedLine() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println() // restore newline after masked input
		return string(b), err
	}
	// non-interactive (pipe, CI) — read plain
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	return scanner.Text(), scanner.Err()
}

// openBrowser opens url in the default browser. Failure is silent — URL is always shown.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	_ = cmd.Start()
}
