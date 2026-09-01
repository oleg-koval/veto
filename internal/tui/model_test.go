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

func TestModelOpensFlagFormForNonRoutingActionWithR(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for range 9 { // feedback
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		model = updated.(*Model)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerOpen || !model.composerEditing || model.composerAction != "feedback" {
		t.Fatalf("feedback form = open:%v editing:%v action:%q", model.composerOpen, model.composerEditing, model.composerAction)
	}
	if len(model.composerFields) == 0 || model.composerFields[0].Name != "kind" {
		t.Fatalf("feedback fields = %#v", model.composerFields)
	}
}

func TestModelKeepsOperationalScreensOnEnterAndUsesSafeSubcommandDefaults(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	for range 10 { // analytics
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		model = updated.(*Model)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.activeAction != "analytics" || model.composerOpen {
		t.Fatalf("analytics enter = action:%q composer:%v", model.activeAction, model.composerOpen)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerValues["subcommand"] != "status" {
		t.Fatalf("analytics form = open:%v subcommand:%q", model.composerOpen, model.composerValues["subcommand"])
	}
}

func TestModelConfirmsStateChangingActionAndMasksSecretFields(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.selected = 0 // login
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "login" {
		t.Fatalf("login form = open:%v action:%q selected:%d", model.composerOpen, model.composerAction, model.selected)
	}
	// Advance provider and mode fields to the secret field.
	for range 2 {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		model = updated.(*Model)
	}
	model.composerValues["api-key"] = "sk-secret"
	view := model.View().Content
	if strings.Contains(view, "sk-secret") || !strings.Contains(view, "•••••••••") {
		t.Fatalf("secret field was not masked: %q", view)
	}
}

func TestModelConfirmationCanBeCancelled(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.confirmOpen = true
	model.pendingRequest = controlplane.ActionRequest{ActionID: "logout"}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "n", Code: 'n'}))
	model = updated.(*Model)
	if model.confirmOpen || model.pendingRequest.ActionID != "" || !strings.Contains(model.status, "cancelled") {
		t.Fatalf("confirmation cancel state = open:%v request:%#v status:%q", model.confirmOpen, model.pendingRequest, model.status)
	}
}

func TestRequiresConfirmationOnlyForMutatingOperations(t *testing.T) {
	tests := []struct {
		name    string
		request controlplane.ActionRequest
		want    bool
	}{
		{name: "analytics status", request: controlplane.ActionRequest{ActionID: "analytics", Arguments: map[string]string{"subcommand": "status"}}},
		{name: "analytics enable", request: controlplane.ActionRequest{ActionID: "analytics", Arguments: map[string]string{"subcommand": "enable"}}, want: true},
		{name: "opencode status", request: controlplane.ActionRequest{ActionID: "opencode", Arguments: map[string]string{"subcommand": "status"}}},
		{name: "opencode connect", request: controlplane.ActionRequest{ActionID: "opencode", Arguments: map[string]string{"subcommand": "connect"}}, want: true},
		{name: "hermes api", request: controlplane.ActionRequest{ActionID: "hermes", Arguments: map[string]string{"subcommand": "api"}}},
		{name: "setup discovery", request: controlplane.ActionRequest{ActionID: "setup"}},
		{name: "setup approval", request: controlplane.ActionRequest{ActionID: "setup", Arguments: map[string]string{"auto-approve": "true"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requiresConfirmation(test.request); got != test.want {
				t.Fatalf("requiresConfirmation = %v, want %v", got, test.want)
			}
		})
	}
}

func TestModelReplaysVersionedEventsDeterministically(t *testing.T) {
	events := []controlplane.Event{
		{Version: controlplane.SchemaVersion, ActionID: "route", Kind: "route.filtering", Message: "2 candidates"},
		{Version: controlplane.SchemaVersion, ActionID: "route", Kind: "route.completed", Message: "safe accepted"},
		{Version: controlplane.SchemaVersion, ActionID: "run", Kind: "output", Message: "done"},
	}
	newModel := func() *Model {
		model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
		model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		for _, event := range events {
			updated, _ := model.Update(eventMsg{event: event, ok: true})
			model = updated.(*Model)
		}
		return model
	}
	first, second := newModel(), newModel()
	if first.lastEvent != second.lastEvent || first.output.String() != second.output.String() || first.View().Content != second.View().Content {
		t.Fatalf("event replay diverged: first=%q/%q second=%q/%q", first.lastEvent, first.output.String(), second.lastEvent, second.output.String())
	}
	if !strings.Contains(first.View().Content, "filtering") || !strings.Contains(first.View().Content, "completed") {
		t.Fatalf("replayed live timeline missing stages: %s", first.View().Content)
	}
}
