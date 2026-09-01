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
