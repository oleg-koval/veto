package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/internal/controlplane"
)

func TestRunTUISetupDiscoversWithoutChangingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "review.md"), []byte("---\nname: review\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runTUISetup(context.Background(), controlplane.ActionRequest{ActionID: "setup", Arguments: map[string]string{"directory": directory}})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if !strings.Contains(result.Summary, "no changes") || !strings.Contains(result.Output, "review.md") {
		t.Fatalf("setup result = %#v", result)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("discovery unexpectedly changed config: %v", err)
	}
}

func TestRunTUISetupAutoApprovesSelectedDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "review.md"), []byte("---\nname: review\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runTUISetup(context.Background(), controlplane.ActionRequest{ActionID: "setup", Arguments: map[string]string{"directory": directory, "auto-approve": "true"}})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if !strings.Contains(result.Summary, "approved 1") {
		t.Fatalf("setup result = %#v", result)
	}
	if !containsStr(loadSkillsConfig().ApprovedDirs, directory) {
		t.Fatalf("approved dirs = %#v", loadSkillsConfig().ApprovedDirs)
	}
}

func TestRunTUIExecDryRunValidatesAndListsPlan(t *testing.T) {
	planPath := filepath.Join(t.TempDir(), "plan.md")
	data := []byte("---\ntitle: TUI plan\nversion: 1\nsteps:\n  - task: inspect the change\n    kind: review\n    risk: low\n---\n")
	if err := os.WriteFile(planPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runTUIExec(context.Background(), controlplane.ActionRequest{ActionID: "exec", Arguments: map[string]string{"plan": planPath, "dry-run": "true"}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if result.Summary != "plan validated" || !strings.Contains(result.Output, "inspect the change") {
		t.Fatalf("dry-run result = %#v", result)
	}
}
