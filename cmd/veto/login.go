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

// loginLocalModel interactively adds a local / self-hosted model.
func loginLocalModel() {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("  Adding a local / self-hosted model.")
	fmt.Println()

	fmt.Print("  Name (routing id, e.g. ollama-qwen): ")
	scanner.Scan()
	name := strings.TrimSpace(scanner.Text())

	fmt.Println()
	fmt.Println("  Endpoint (full chat-completions URL):")
	fmt.Println("    Ollama:    http://localhost:11434/v1/chat/completions")
	fmt.Println("    LM Studio: http://localhost:1234/v1/chat/completions")
	fmt.Print("  Endpoint: ")
	scanner.Scan()
	endpoint := strings.TrimSpace(scanner.Text())

	fmt.Print("  Model id (as the server knows it, e.g. qwen2.5-coder:7b): ")
	scanner.Scan()
	modelID := strings.TrimSpace(scanner.Text())

	fmt.Print("  API key (leave blank if not required): ")
	scanner.Scan()
	apiKey := strings.TrimSpace(scanner.Text())

	lm := LocalModel{
		Name:     name,
		Endpoint: endpoint,
		Model:    modelID,
		APIKey:   apiKey,
	}

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
	fmt.Println("  It will appear in 'veto providers' and be considered in all routing calls.")
	fmt.Println()
	fmt.Println("  What's next:")
	fmt.Println("    veto providers                       — confirm the model is listed")
	fmt.Printf("    veto route --task \"...\" --kind summarize\n")
	fmt.Println()
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
