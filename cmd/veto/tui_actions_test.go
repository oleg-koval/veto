package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/internal/tui"
)

func TestTUIScreenReaderModeUsesStableTextPresentation(t *testing.T) {
	model := tui.NewModel(controlplane.DefaultCatalog(), tui.Options{Motion: false, NoColor: true, Mouse: false, ScreenReader: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if model.View().AltScreen {
		t.Fatal("screen-reader presentation should preserve terminal scrollback")
	}
	view := model.View().Content
	if strings.Contains(view, "\x1b[") {
		t.Fatal("screen-reader presentation contains ANSI escape codes")
	}
	if !strings.Contains(view, "STATUS") || !strings.Contains(view, "COMMANDS") {
		t.Fatalf("screen-reader presentation lacks stable labels:\n%s", view)
	}
}

func TestTUIModelPolicyRefreshesLiveRoutingPreferences(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })

	service := application.NewControlService(application.Runner{}, nil)
	refreshed := false
	registerTUIActionHandlers(service, func() error { refreshed = true; return nil })
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

func TestTUIModelPolicySupportsMultipleCLIModels(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })

	result, err := runTUIModelPolicy(controlplane.ActionRequest{ActionID: "disable", Arguments: map[string]string{"model": "alpha, beta"}}, true)
	if err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if !strings.Contains(result.Summary, "alpha, beta disabled") {
		t.Fatalf("disable summary = %q", result.Summary)
	}
	disabled := loadDisabledModels()
	if !disabled["alpha"] || !disabled["beta"] {
		t.Fatalf("disabled models = %#v", disabled)
	}
	if _, err := runTUIModelPolicy(controlplane.ActionRequest{ActionID: "enable", Arguments: map[string]string{"model": "alpha, beta"}}, false); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	if disabled = loadDisabledModels(); len(disabled) != 0 {
		t.Fatalf("disabled models after enable = %#v", disabled)
	}
}

func TestTUIProvidersActionUsesInjectableCLIOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	service := application.NewControlService(application.Runner{}, nil)
	registerTUIActionHandlers(service, nil)
	result, err := service.Execute(context.Background(), controlplane.ActionRequest{ActionID: "providers"})
	if err != nil {
		t.Fatalf("providers failed: %v", err)
	}
	if !bytes.Contains([]byte(result.Output), []byte("provider")) || !strings.Contains(result.Summary, "inspected") {
		t.Fatalf("providers result = %#v", result)
	}
}

func TestTUIModelsPreservesOfflineFlagChoice(t *testing.T) {
	request := controlplane.ActionRequest{ActionID: "models"}
	if args := tuiFlagArguments(request); len(args) != 0 {
		t.Fatalf("models default unexpectedly changed CLI behavior: %#v", args)
	}
	request.Arguments = map[string]string{"offline": "true"}
	args := tuiFlagArguments(request)
	if len(args) != 1 || args[0] != "--offline" {
		t.Fatalf("models offline flag = %#v, want --offline", args)
	}
}

func TestTUIModelVerificationPreservesOutputFormatChoice(t *testing.T) {
	result := modelVerification{Provider: "OpenAI", ConfiguredModels: []string{"gpt-test"}, Artifact: "artifacts/http/openai.json"}
	textOutput, err := formatTUIModelVerification(result, false)
	if err != nil {
		t.Fatalf("text formatting failed: %v", err)
	}
	if !strings.Contains(textOutput, "OpenAI: 1 catalog model(s), 1 available") || strings.HasPrefix(strings.TrimSpace(textOutput), "{") {
		t.Fatalf("text verification output = %q", textOutput)
	}
	jsonOutput, err := formatTUIModelVerification(result, true)
	if err != nil {
		t.Fatalf("json formatting failed: %v", err)
	}
	var decoded modelVerification
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("json verification output is invalid: %v", err)
	}
	if decoded.Provider != result.Provider {
		t.Fatalf("json provider = %q, want %q", decoded.Provider, result.Provider)
	}
}

func TestPrepareTUIRoutingAllowsFreshProviderOnboarding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY", "CLAUDE_SUBSCRIPTION"} {
		t.Setenv(key, "")
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })

	reg, mgr, store, err := prepareTUIRouting()
	if err != nil {
		t.Fatalf("fresh TUI routing failed: %v", err)
	}
	if reg == nil || mgr == nil || store == nil {
		t.Fatalf("fresh TUI routing returned nil components: reg=%v mgr=%v store=%v", reg, mgr, store)
	}
	if len(reg.modelCaps()) != 0 {
		t.Fatalf("fresh registry unexpectedly contains models: %#v", reg.modelCaps())
	}
}

func TestRefreshTUIRoutingLoadsProviderAddedInSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY", "CLAUDE_SUBSCRIPTION"} {
		t.Setenv(key, "")
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousConfig := vetoCfgPathOverride
	vetoCfgPathOverride = configPath
	t.Cleanup(func() { vetoCfgPathOverride = previousConfig })
	previousModels := localModelsPathOverride
	localModelsPathOverride = filepath.Join(t.TempDir(), "models.json")
	t.Cleanup(func() { localModelsPathOverride = previousModels })

	reg, mgr, _, err := prepareTUIRouting()
	if err != nil {
		t.Fatalf("fresh TUI routing failed: %v", err)
	}
	_, err = runTUILogin(context.Background(), controlplane.ActionRequest{ActionID: "login", Arguments: map[string]string{
		"provider": "local", "name": "session-local", "endpoint": "http://127.0.0.1:11434/v1/chat/completions", "model": "qwen2.5-coder:7b",
	}})
	if err != nil {
		t.Fatalf("local login failed: %v", err)
	}
	if err := refreshTUIRouting(reg, mgr); err != nil {
		t.Fatalf("routing refresh failed: %v", err)
	}
	if _, ok := reg.caps["session-local"]; !ok {
		t.Fatalf("refreshed registry missing session-local: %#v", reg.caps)
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
