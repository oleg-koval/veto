package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type credentials map[string]string

func credentialsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "credentials.json")
}

func loadCredentials() (credentials, error) {
	path := credentialsPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c credentials
	return c, json.Unmarshal(data, &c)
}

func saveCredential(envKey, value string) error {
	path := credentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	c, err := loadCredentials()
	if err != nil {
		c = credentials{}
	}
	c[envKey] = value
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// getKey returns the API key for envKey — env var wins, then credentials file.
func getKey(envKey string, creds credentials) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return creds[envKey]
}

func cmdLogin() {
	type provider struct {
		name   string
		envKey string
		label  string
	}
	providers := []provider{
		{"anthropic", "ANTHROPIC_API_KEY", "claude-haiku / sonnet / opus"},
		{"openai", "OPENAI_API_KEY", "gpt-4o / gpt-4o-mini"},
		{"openrouter", "OPENROUTER_API_KEY", "any model via openrouter.ai"},
	}

	fmt.Println("Select a provider:")
	for i, p := range providers {
		fmt.Printf("  %d) %-12s %s\n", i+1, p.name, p.label)
	}
	fmt.Print("\nProvider [1-3]: ")
	var choice int
	if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(providers) {
		fmt.Fprintln(os.Stderr, "invalid selection")
		os.Exit(1)
	}
	p := providers[choice-1]

	fmt.Printf("%s API key: ", p.name)
	var key string
	if _, err := fmt.Scan(&key); err != nil || key == "" {
		fmt.Fprintln(os.Stderr, "no key entered")
		os.Exit(1)
	}

	if err := saveCredential(p.envKey, key); err != nil {
		fmt.Fprintln(os.Stderr, "error saving credentials:", err)
		os.Exit(1)
	}
	fmt.Printf("saved %s to %s\n", p.envKey, credentialsPath())
}
