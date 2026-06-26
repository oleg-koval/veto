package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

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
	fmt.Println("  veto will open the API keys page for you — just copy your key and paste it here.")
	fmt.Println()
	fmt.Println("  Which provider would you like to connect?")
	fmt.Println()
	for i, p := range knownProviders {
		fmt.Printf("  %d  %-12s  %s\n", i+1, p.name, p.models)
	}
	fmt.Println()
	fmt.Print("  Provider [1-3]: ")

	var choice int
	if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(knownProviders) {
		fmt.Fprintln(os.Stderr, "\n  That doesn't look right. Please enter 1, 2, or 3.")
		os.Exit(1)
	}
	p := knownProviders[choice-1]

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
		fmt.Printf("\n  Heads up: %s keys usually start with %q — double-check it if something doesn't work.\n", p.name, p.keyHint)
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
