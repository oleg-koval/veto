package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
)

func TestTUIModelPolicyRefreshesLiveRoutingPreferences(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })

	service := application.NewControlService(application.Runner{}, nil)
	refreshed := false
	registerTUIActionHandlers(service, func() { refreshed = true })
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{
		ActionID:  "disable",
		Arguments: map[string]string{"model": "gpt-test"},
	})
	if err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if !refreshed {
		t.Fatal("live routing preferences were not refreshed")
	}
	if !strings.Contains(result.Summary, "disabled") {
		t.Fatalf("disable result = %#v", result)
	}
}

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

func TestRunTUIDoctorJSONReturnsDiagnosticReport(t *testing.T) {
	result, err := runTUIDoctor(controlplane.ActionRequest{ActionID: "doctor", Arguments: map[string]string{
		"offline": "true", "json": "true",
	}})
	if err != nil {
		t.Fatalf("doctor failed: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("doctor output is not JSON: %v", err)
	}
	if _, ok := report["checks"]; !ok {
		t.Fatalf("doctor report missing checks: %#v", report)
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

func TestRunTUISetupApprovesSelectedFiles(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })
	directory := t.TempDir()
	first := filepath.Join(directory, "first.md")
	second := filepath.Join(directory, "second.md")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("---\nname: skill\n---\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := runTUISetup(context.Background(), controlplane.ActionRequest{ActionID: "setup", Arguments: map[string]string{
		"directory": directory, "approved-files": "first.md",
	}})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if !strings.Contains(result.Summary, "approved 1") {
		t.Fatalf("setup result = %#v", result)
	}
	if !containsStr(loadSkillsConfig().ApprovedFiles, first) || containsStr(loadSkillsConfig().ApprovedFiles, second) {
		t.Fatalf("approved files = %#v", loadSkillsConfig().ApprovedFiles)
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

func TestRunTUIFeedbackStdinUsesFormPayload(t *testing.T) {
	feedbackDir := t.TempDir()
	previous := feedbackPathOverride
	feedbackPathOverride = feedbackDir
	t.Cleanup(func() { feedbackPathOverride = previous })
	result, err := runTUIFeedback(context.Background(), controlplane.ActionRequest{ActionID: "feedback", Arguments: map[string]string{
		"stdin": "true", "kind": "feature", "summary": "add a TUI report", "reproduction": "open feedback form", "expected": "report saves", "actual": "report needs form payload", "scope": "tui",
		"acceptance-criteria": "saved report; redacted output",
	}})
	if err != nil {
		t.Fatalf("feedback failed: %v", err)
	}
	if !strings.Contains(result.Output, "add a TUI report") {
		t.Fatalf("feedback output = %q", result.Output)
	}
}

func TestResolveTUIPlanPathUsesVetoPlanDirectoryForNames(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveTUIPlanPath("release.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".veto", "plans", "release.md")
	if got != want {
		t.Fatalf("resolved plan path = %q, want %q", got, want)
	}
}
