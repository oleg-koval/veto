package main

import (
	"strings"
	"testing"
)

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
