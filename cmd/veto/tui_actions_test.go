package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/internal/tui"
	"github.com/oleg-koval/veto/pkg/ledger"
)

func TestTUIScreenReaderModeUsesStableTextPresentation(t *testing.T) {
	model := tui.NewModel(controlplane.DefaultCatalog(), tui.Options{Motion: false, NoColor: true, Mouse: false, ScreenReader: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	if !model.View().AltScreen {
		t.Fatal("screen-reader presentation should use a stable alternate-screen frame")
	}
	presentation := model.View()
	view := presentation.Content
	if strings.Contains(view, "\x1b[") {
		t.Fatal("screen-reader presentation contains ANSI escape codes")
	}
	if !presentation.DisableBracketedPasteMode || presentation.ReportFocus {
		t.Fatalf("screen-reader presentation enables terminal protocol controls: bracketed-paste-disabled=%v report-focus=%v", presentation.DisableBracketedPasteMode, presentation.ReportFocus)
	}
	if !strings.Contains(view, "STATUS") || !strings.Contains(view, "COMMANDS") {
		t.Fatalf("screen-reader presentation lacks stable labels:\n%s", view)
	}
}

func TestTUIIntegrationsIncludeImpeccableHarness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	openCodeFound := false
	for _, integration := range readTUIIntegrations() {
		if integration.Name == "OpenCode" {
			openCodeFound = true
			if integration.PrimaryAction != "connect" || strings.Contains(integration.Detail, "run veto") {
				t.Fatalf("OpenCode snapshot delegates work to the user: %#v", integration)
			}
		}
		if integration.Name == "Impeccable" {
			if integration.Status != "available" || integration.PrimaryAction != "install" || !strings.Contains(integration.Detail, "design skills") {
				t.Fatalf("Impeccable snapshot = %#v", integration)
			}
			if !openCodeFound {
				t.Fatal("OpenCode integration is missing")
			}
			return
		}
	}
	t.Fatal("Impeccable integration is missing")
}

func TestTUIImpeccableInstallUsesAvailableCLI(t *testing.T) {
	var executable string
	var arguments []string
	result, err := runTUIImpeccableInstall(context.Background(), func(name string) (string, error) {
		if name == "impeccable" {
			return "/safe/bin/impeccable", nil
		}
		return "", fmt.Errorf("unexpected executable %s", name)
	}, func(_ context.Context, name string, args ...string) ([]byte, error) {
		executable = name
		arguments = append([]string(nil), args...)
		return []byte("installed"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if executable != "/safe/bin/impeccable" || strings.Join(arguments, " ") != "install --providers=veto --scope=global" {
		t.Fatalf("Impeccable invocation = %q %#v", executable, arguments)
	}
	if result.Summary != "Impeccable installed for Veto" || result.Output != "installed" {
		t.Fatalf("Impeccable result = %#v", result)
	}
}

func TestTUIImpeccableInstallFallsBackToNonInteractiveNPX(t *testing.T) {
	var arguments []string
	_, err := runTUIImpeccableInstall(context.Background(), func(name string) (string, error) {
		if name == "npx" {
			return "/safe/bin/npx", nil
		}
		return "", os.ErrNotExist
	}, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		arguments = append([]string(nil), args...)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(arguments, " ") != "--yes impeccable install --providers=veto --scope=global" {
		t.Fatalf("npx invocation = %#v", arguments)
	}
}

func TestTUIHistoryTreatsCodexAsHarnessWithUnknownModel(t *testing.T) {
	model, harness := tuiHistoryIdentity(ledger.Event{Model: "codex", Runtime: "codex-cli"})
	if model != "" || harness != "Codex CLI" {
		t.Fatalf("codex identity = model:%q harness:%q", model, harness)
	}
	model, harness = tuiHistoryIdentity(ledger.Event{Model: "luna", Runtime: "openai-api"})
	if model != "luna" || harness != "OpenAI API" {
		t.Fatalf("model identity = model:%q harness:%q", model, harness)
	}
}

func TestTUIHistoryLoadsAllEventsForClientSideFiltering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logs := filepath.Join(home, ".veto", "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(logs, "veto-pagination.log"))
	if err != nil {
		t.Fatal(err)
	}
	writer := ledger.NewWriter(file)
	for index := 0; index < 55; index++ {
		if err := writer.Append(ledger.Event{RunID: "run-pagination", Type: ledger.EventFilterPass, Model: fmt.Sprintf("model-%02d", index)}); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	history := readTUIHistory()
	if len(history) != 55 {
		t.Fatalf("history rows = %d, want all 55", len(history))
	}
}

func TestTUIHistoryPreservesRedactedMissionEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logs := filepath.Join(home, ".veto", "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(logs, "veto-evidence.log"))
	if err != nil {
		t.Fatal(err)
	}
	confidence, estimatedCost, cost, latency := 0.91, 0.01, 0.012, int64(4321)
	estimatedTokens := 800
	writer := ledger.NewWriter(file)
	if err := writer.Append(ledger.Event{
		RunID: "run-evidence", TaskID: "task-evidence", TaskKind: "review", Risk: "medium",
		Type: ledger.EventReviewError, Model: "luna", Runtime: "openai-api", Status: "error",
		Reasons: []string{"criteria-unavailable"}, Confidence: &confidence, EstimatedTokens: &estimatedTokens,
		EstimatedCostUSD: &estimatedCost, Usage: &ledger.Usage{InputTokens: 300, OutputTokens: 200, TotalTokens: 500},
		CostUSD: &cost, LatencyMS: &latency, Detail: "token=secret reviewer unavailable",
	}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	history := readTUIHistory()
	if len(history) != 1 {
		t.Fatalf("history rows = %d, want 1", len(history))
	}
	event := history[0]
	if event.RunID != "run-evidence" || event.TaskID != "task-evidence" || event.TaskKind != "review" || event.Risk != "medium" || event.Model != "luna" || event.Runtime != "OpenAI API" {
		t.Fatalf("history identity metadata = %#v", event)
	}
	if !event.ConfidenceKnown || event.Confidence != confidence || !event.EstimatedTokensKnown || event.EstimatedTokens != estimatedTokens || !event.EstimatedCostKnown || !event.UsageKnown || !event.CostKnown || !event.LatencyKnown {
		t.Fatalf("history evidence metadata = %#v", event)
	}
	if strings.Contains(event.Detail, "secret") || !strings.Contains(event.Detail, "[REDACTED]") {
		t.Fatalf("history detail was not redacted: %q", event.Detail)
	}
}

func TestTUIPlansLoadsAllFilesForClientSideFiltering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	plansDir := filepath.Join(home, ".veto", "plans")
	if err := os.MkdirAll(plansDir, 0700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 55; index++ {
		path := filepath.Join(plansDir, fmt.Sprintf("plan-%02d.md", index))
		if err := os.WriteFile(path, []byte("# plan\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if plans := readTUIPlans(); len(plans) != 55 {
		t.Fatalf("plan rows = %d, want all 55", len(plans))
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

func TestTUILoginRejectsUnsupportedProviderMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	result, err := runTUILogin(context.Background(), controlplane.ActionRequest{ActionID: "login", Arguments: map[string]string{
		"provider": "openai", "mode": "subscription",
	}})
	if err == nil || !strings.Contains(err.Error(), "only supported for anthropic") {
		t.Fatalf("unsupported mode result=%#v err=%v", result, err)
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
	if err != nil && !strings.Contains(err.Error(), "doctor found") {
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
