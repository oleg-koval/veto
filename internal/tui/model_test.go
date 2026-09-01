package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/oleg-koval/veto/internal/controlplane"
)

func TestModelRendersAccessibleShellAndStatusline(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := model.View()

	for _, want := range []string{"VETO", "palette", "STATUS", "providers", "Tab move", "? help"} {
		if !strings.Contains(view.Content, want) {
			t.Errorf("shell view missing %q\n%s", want, view.Content)
		}
	}
}

func TestModelSupportsKeyboardNavigationAndHelp(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	model = updated.(*Model)
	if model.selected != 1 {
		t.Fatalf("selected action = %d, want 1", model.selected)
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "?", Code: '?'}))
	model = updated.(*Model)
	if !model.helpOpen {
		t.Fatal("help overlay did not open")
	}
	if !strings.Contains(model.View().Content, "Keyboard help") {
		t.Fatal("help overlay did not render")
	}
}

func TestModelCommandPaletteFiltersAndSelectsAction(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "k", Code: 'k', Mod: tea.ModCtrl}))
	model = updated.(*Model)
	if !model.paletteOpen {
		t.Fatal("command palette did not open")
	}
	for _, text := range []string{"d", "o", "c"} {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: text, Code: rune(text[0])}))
		model = updated.(*Model)
	}
	if len(model.filteredActions()) != 1 || model.filteredActions()[0].ID != "doctor" {
		t.Fatalf("filtered actions = %#v, want doctor", model.filteredActions())
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.paletteOpen || model.activeAction != "doctor" {
		t.Fatalf("palette selection did not activate doctor: open=%v action=%q", model.paletteOpen, model.activeAction)
	}
}

func TestModelNoColorStripsANSI(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(model.View().Content, "\x1b[") {
		t.Fatal("no-color view contains ANSI escape codes")
	}
}

func TestModelRendersProviderAndModelSnapshots(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.snapshot = controlplane.Snapshot{
		Providers: []controlplane.ProviderSnapshot{{Name: "openai", Configured: true, ModelCount: 2}},
		Models:    []controlplane.ModelSnapshot{{Name: "gpt-test", Provider: "openai", Runtime: "api", Tier: "mid"}},
	}
	model.activeAction = "providers"
	if !strings.Contains(model.View().Content, "openai") {
		t.Fatal("provider snapshot did not render")
	}
	model.activeAction = "models"
	if !strings.Contains(model.View().Content, "gpt-test") {
		t.Fatal("model snapshot did not render")
	}
}

func TestModelRendersOperationalScreens(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.snapshot = controlplane.Snapshot{
		History:      []controlplane.HistorySnapshot{{Type: "execution.completed", Model: "gpt-test", Status: "success"}},
		Plans:        []controlplane.PlanSnapshot{{Name: "release.md"}},
		Health:       []controlplane.HealthSnapshot{{ID: "state.permissions", Status: "PASS", Message: "state is private"}},
		Analytics:    controlplane.AnalyticsSnapshot{LocalCollection: true, LocalPath: "~/.veto/logs", RetentionDays: 7, RemoteSharing: "opted out"},
		Integrations: []controlplane.IntegrationSnapshot{{Name: "OpenCode", Status: "configured", Detail: "attach"}},
	}
	for action, want := range map[string]string{"history": "RECENT ACTIVITY", "exec": "release.md", "doctor": "state.permissions", "analytics": "ANALYTICS & DATA", "integrations": "OpenCode"} {
		model.activeAction = action
		if !strings.Contains(model.View().Content, want) {
			t.Errorf("%s screen missing %q", action, want)
		}
	}
}

func TestModelEscapeCancelsRunningRequest(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	called := false
	model.running = true
	model.composerAction = "run"
	model.cancelRun = func() { called = true }
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	model = updated.(*Model)
	if !called || !strings.Contains(model.status, "Cancelling") {
		t.Fatalf("cancel state = called:%v status:%q", called, model.status)
	}
}

func TestModelLoadsServiceAfterFirstFrame(t *testing.T) {
	service := staticService{}
	model := NewModel(controlplane.DefaultCatalog(), Options{ServiceFactory: func() (controlplane.Service, error) {
		return service, nil
	}})
	if !strings.Contains(model.View().Content, "Loading") {
		t.Fatal("initial frame did not show loading state")
	}
	updated, _ := model.Update(serviceReadyMsg{service: service})
	model = updated.(*Model)
	if model.options.Service == nil || !strings.Contains(model.status, "Ready") {
		t.Fatal("service was not installed after readiness message")
	}
}

func TestModelFitsSupportedTerminalHeights(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{{40, 12}, {80, 24}, {120, 40}} {
		model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		lines := strings.Split(model.View().Content, "\n")
		if len(lines) > size.height {
			t.Errorf("%dx%d view has %d lines", size.width, size.height, len(lines))
		}
	}
}

func TestModelDoctorEnterRunsThroughService(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Service: staticService{}})
	for range 8 {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		model = updated.(*Model)
	}
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.activeAction != "doctor" || !model.running || cmd == nil {
		t.Fatalf("doctor execution state = action:%q running:%v cmd:%v", model.activeAction, model.running, cmd != nil)
	}
}

type staticService struct{}

func (staticService) Snapshot(context.Context) (controlplane.Snapshot, error) {
	return controlplane.Snapshot{Status: "idle"}, nil
}
func (staticService) Execute(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error) {
	return controlplane.ActionResult{}, nil
}
func (staticService) Subscribe(context.Context) <-chan controlplane.Event {
	return make(chan controlplane.Event)
}
func (staticService) Cancel(context.Context, string) error { return nil }

func TestModelComposerCapturesObjectiveForRun(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	var updated tea.Model = model
	for range 3 {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		model = updated.(*Model)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "run" {
		t.Fatalf("composer state = open:%v action:%q", model.composerOpen, model.composerAction)
	}
	for _, text := range []string{"h", "i"} {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: text, Code: rune(text[0])}))
		model = updated.(*Model)
	}
	if model.composerInput != "hi" {
		t.Fatalf("composer input = %q, want hi", model.composerInput)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.composerEditing || len(model.composerFields) == 0 {
		t.Fatal("composer did not enter the CLI flag step")
	}
}
