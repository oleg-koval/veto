package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// cmdLogout removes a configured provider or local model.
// With no argument: interactive menu.
// With one argument: non-interactive (e.g. "veto logout ANTHROPIC_API_KEY",
// "veto logout ollama-qwen", "veto logout subscription").
func cmdLogout(args []string) {
	if len(args) > 0 {
		logoutNonInteractive(args[0])
		return
	}
	logoutInteractive()
}

func logoutInteractive() {
	creds, _ := loadCredentials()
	locals, _ := loadLocalModels()

	type item struct {
		label  string
		remove func() error
	}
	var items []item

	// subscription
	if creds["CLAUDE_SUBSCRIPTION"] == "true" {
		items = append(items, item{
			label:  "Claude subscription (claude CLI)",
			remove: func() error { return removeCredential("CLAUDE_SUBSCRIPTION") },
		})
	}
	// API-key providers
	for _, p := range knownProviders {
		if creds[p.envKey] != "" {
			p := p // capture
			items = append(items, item{
				label:  p.name + " (" + p.envKey + ")",
				remove: func() error { return removeCredential(p.envKey) },
			})
		}
	}
	// local models
	for _, lm := range locals {
		lm := lm // capture
		items = append(items, item{
			label:  "Local: " + lm.Name + " → " + lm.Endpoint,
			remove: func() error { return removeLocalModel(lm.Name) },
		})
	}

	if len(items) == 0 {
		fmt.Println()
		fmt.Println("  Nothing is configured — run 'veto login' to add a provider.")
		fmt.Println()
		return
	}

	fmt.Println()
	fmt.Println("  What would you like to remove?")
	fmt.Println()
	for i, it := range items {
		fmt.Printf("  %d  %s\n", i+1, it.label)
	}
	fmt.Println()
	fmt.Printf("  Choice [1-%d]: ", len(items))

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	text := strings.TrimSpace(scanner.Text())
	var choice int
	if _, err := fmt.Sscan(text, &choice); err != nil || choice < 1 || choice > len(items) {
		fmt.Fprintln(os.Stderr, "\n  Invalid choice.")
		os.Exit(1)
	}

	selected := items[choice-1]
	fmt.Printf("\n  Remove %q? [y/N]: ", selected.label)
	scanner.Scan()
	confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if confirm != "y" && confirm != "yes" {
		fmt.Println("\n  Cancelled.")
		return
	}

	if err := selected.remove(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n  Removed: %s\n\n", selected.label)
}

func logoutNonInteractive(arg string) {
	// "subscription" → CLAUDE_SUBSCRIPTION
	if strings.EqualFold(arg, "subscription") {
		mustRemoveCredential("CLAUDE_SUBSCRIPTION", "subscription")
		return
	}
	// known env key → API-key provider
	for _, p := range knownProviders {
		if strings.EqualFold(arg, p.envKey) {
			mustRemoveCredential(p.envKey, p.name)
			return
		}
	}
	// otherwise treat as local model name
	locals, _ := loadLocalModels()
	for _, lm := range locals {
		if lm.Name == arg {
			if err := removeLocalModel(arg); err != nil {
				fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("  Removed local model %q.\n", arg)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "  %q is not a configured provider or local model.\n", arg)
	os.Exit(1)
}

func mustRemoveCredential(envKey, label string) {
	if err := removeCredential(envKey); err != nil {
		fmt.Fprintf(os.Stderr, "  Error removing %s: %v\n", label, err)
		os.Exit(1)
	}
	fmt.Printf("  Removed: %s\n", label)
}
