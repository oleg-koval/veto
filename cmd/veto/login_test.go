package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCatalogModelDescriptionReportsOpenRouterRoutableCount(t *testing.T) {
	assert.Equal(
		t,
		"1 built-in model plus the dynamic catalog: meta-llama/llama-4-maverick",
		catalogModelDescription("openrouter"),
	)
}

func TestCatalogModelDescriptionPreservesDirectProviderNames(t *testing.T) {
	assert.Equal(
		t,
		"gpt-4.1, gpt-4.1-mini, sol, terra, luna",
		catalogModelDescription("openai"),
	)
}

func TestOllamaInstallCommandsDoNotPipeRemoteScripts(t *testing.T) {
	for _, opt := range localServerOptions {
		if opt.serverCmd != "ollama" {
			continue
		}
		for platform, command := range opt.installOS {
			if strings.Contains(command, "curl") || strings.Contains(command, "| sh") {
				t.Fatalf("ollama install command for %s executes a remote shell pipeline: %q", platform, command)
			}
		}
		if _, ok := opt.installOS["linux"]; ok {
			t.Fatal("ollama Linux setup must require manual installation instead of curl|sh")
		}
	}
}
