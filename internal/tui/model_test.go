package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

func TestModelSupportsMouseSelectionAndWheelNavigation(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ := model.Update(tea.MouseClickMsg{X: 3, Y: 3, Button: tea.MouseLeft})
	model = updated.(*Model)
	if model.selected != 2 {
		t.Fatalf("mouse selected = %d, want 2", model.selected)
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = updated.(*Model)
	if model.selected != 3 {
		t.Fatalf("wheel selected = %d, want 3", model.selected)
	}
}

func TestModelShowsCommandTooltipOnMouseHover(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ := model.Update(tea.MouseMotionMsg{X: 3, Y: 1})
	model = updated.(*Model)
	if model.hoveredCommand != 0 {
		t.Fatalf("hovered command = %d, want 0", model.hoveredCommand)
	}
	if !strings.Contains(model.View().Content, "Connect a provider with masked key input") {
		t.Fatal("hover tooltip description missing")
	}
}

func TestModelMouseHitTestAccountsForTooltipRow(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ := model.Update(tea.MouseMotionMsg{X: 3, Y: 1})
	model = updated.(*Model)
	updated, _ = model.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	model = updated.(*Model)
	if model.selected != 2 {
		t.Fatalf("selected command after tooltip = %d, want 2", model.selected)
	}
}

func TestModelClipsLongTooltipStatusOnNarrowTerminal(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 32, Height: 12})
	updated, _ := model.Update(tea.MouseMotionMsg{X: 2, Y: lipgloss.Height(model.renderMain(32)) + 1})
	model = updated.(*Model)
	if lipgloss.Width(model.statusLine(32)) > 32 {
		t.Fatalf("status line width = %d, want <= 32", lipgloss.Width(model.statusLine(32)))
	}
}

func TestModelSupportsMouseSelectionInNarrowLayout(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true})
	model.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	offset := lipgloss.Height(model.renderMain(40)) + 2
	updated, _ := model.Update(tea.MouseClickMsg{X: 2, Y: offset + 2, Button: tea.MouseLeft})
	model = updated.(*Model)
	if model.selected != 1 {
		t.Fatalf("narrow mouse selected = %d, want 1", model.selected)
	}
}

func TestModelTogglesBooleanFormFlagsWithSpace(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.selected = 18 // install-git-hook
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerEditing || model.composerFields[0].Value != "bool" {
		t.Fatalf("boolean form = editing:%v fields:%#v", model.composerEditing, model.composerFields)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: " ", Code: ' '}))
	model = updated.(*Model)
	if model.composerValues["force"] != "true" {
		t.Fatalf("force after space = %q, want true", model.composerValues["force"])
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: " ", Code: ' '}))
	model = updated.(*Model)
	if model.composerValues["force"] != "false" {
		t.Fatalf("force after second space = %q, want false", model.composerValues["force"])
	}
}

func TestModelCyclesEnumFormFieldsWithSpace(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	analytics, ok := model.catalog.Find("analytics")
	if !ok {
		t.Fatal("analytics action missing from catalog")
	}
	model.openComposer(analytics)
	if model.composerValues["subcommand"] != "status" {
		t.Fatalf("default subcommand = %q", model.composerValues["subcommand"])
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: " ", Code: ' '}))
	model = updated.(*Model)
	if model.composerValues["subcommand"] != "enable" {
		t.Fatalf("cycled subcommand = %q, want enable", model.composerValues["subcommand"])
	}

	run, ok := model.catalog.Find("run")
	if !ok {
		t.Fatal("run action missing from catalog")
	}
	model.openComposer(run)
	if model.composerFields[0].Name != "kind" {
		t.Fatalf("first run field = %q, want kind", model.composerFields[0].Name)
	}
	model.composerEditing = true
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: " ", Code: ' '}))
	model = updated.(*Model)
	if model.composerValues["kind"] != "extract" {
		t.Fatalf("cycled kind = %q, want extract", model.composerValues["kind"])
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

func TestModelCommandPaletteLaunchesFormCapableAction(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	model = updated.(*Model)
	for _, text := range []string{"f", "e", "e", "d"} {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: text, Code: rune(text[0])}))
		model = updated.(*Model)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "feedback" || model.paletteOpen {
		t.Fatalf("palette launch = composer:%v action:%q palette:%v", model.composerOpen, model.composerAction, model.paletteOpen)
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

func TestModelPlanSelectionOpensExecuteComposer(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.snapshot = controlplane.Snapshot{
		Plans: []controlplane.PlanSnapshot{{Name: "first.md"}, {Name: "second.md"}},
	}
	model.activeAction = "plans"
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	model = updated.(*Model)
	if model.plansCursor != 1 {
		t.Fatalf("plan cursor = %d, want 1", model.plansCursor)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "exec" || model.composerValues["plan"] != "second.md" {
		t.Fatalf("plan composer = open:%v action:%q plan:%q", model.composerOpen, model.composerAction, model.composerValues["plan"])
	}
}

func TestModelRendersMonitorCountersAndRuntimeState(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.snapshot = controlplane.Snapshot{
		Model:     "gpt-test",
		Providers: []controlplane.ProviderSnapshot{{Name: "openai", Configured: true}},
		Monitor:   controlplane.MonitorSnapshot{ActiveSessions: 1, ActiveTools: 2, PendingApprovals: 1, Artifacts: 3, TotalTokens: 99, TokensKnown: true, CostUSD: 0.42, CostKnown: true, LatencyMs: 120, LatencyKnown: true},
	}
	model.running = true
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := model.View().Content
	for _, want := range []string{"running", "sessions   1", "tools      2", "approvals  1", "tokens     99", "cost       $0.4200", "latency    120ms", "artifacts  3"} {
		if !strings.Contains(view, want) {
			t.Errorf("monitor view missing %q\n%s", want, view)
		}
	}
}

func TestModelUsesSmoothRunningSpinnerAndRespectsReducedMotion(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: true, NoColor: true})
	model.running = true
	model.frame = 3
	view := model.View().Content
	if !strings.Contains(view, string([]rune(runningSpinner)[3])) {
		t.Fatalf("running view missing spinner frame %q\n%s", string([]rune(runningSpinner)[3]), view)
	}

	model.options.Motion = false
	view = model.View().Content
	if strings.Contains(view, "⠸") {
		t.Fatalf("reduced-motion view still contains spinner frame\n%s", view)
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

func TestModelReportsCancelledExecutionAsReady(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.running = true
	model.activeAction = "doctor"
	model.cancelRun = func() {}
	updated, _ := model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "doctor"}, err: context.Canceled})
	model = updated.(*Model)
	if model.running || !strings.Contains(model.status, "action cancelled") {
		t.Fatalf("cancelled execution state = running:%v status:%q", model.running, model.status)
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
	}{{40, 12}, {80, 24}, {120, 40}, {40, 2}, {40, 1}} {
		model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
		model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		lines := strings.Split(model.View().Content, "\n")
		if len(lines) > size.height {
			t.Errorf("%dx%d view has %d lines", size.width, size.height, len(lines))
		}
	}
}

func TestTruncatePreservesUnicodeAndTerminalWidth(t *testing.T) {
	got := truncate("模型😀 output", 7)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is invalid UTF-8: %q", got)
	}
	if lipgloss.Width(got) > 7 {
		t.Fatalf("truncated width = %d, want <= 7: %q", lipgloss.Width(got), got)
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

func TestModelProvidersEnterRunsThroughService(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Service: staticService{}})
	for range 14 { // providers
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		model = updated.(*Model)
	}
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.activeAction != "providers" || !model.running || cmd == nil {
		t.Fatalf("providers execution state = action:%q running:%v cmd:%v", model.activeAction, model.running, cmd != nil)
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
		{name: "setup individual approval", request: controlplane.ActionRequest{ActionID: "setup", Arguments: map[string]string{"approved-files": "review.md"}}, want: true},
		{name: "doctor diagnostics", request: controlplane.ActionRequest{ActionID: "doctor", Arguments: map[string]string{"fix": "false"}}},
		{name: "doctor repair", request: controlplane.ActionRequest{ActionID: "doctor", Arguments: map[string]string{"fix": "true"}}, want: true},
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

func TestModelPrioritizesCompletedOutputInMainView(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model.activeAction = "run"
	model.eventHistory = []controlplane.Event{{Version: controlplane.SchemaVersion, ActionID: "run", Kind: "route.ask_accept", Message: "local accepted"}}
	model.output.WriteString("SMOKE EXECUTION OK")
	view := model.View().Content
	if !strings.Contains(view, "OUTPUT") || !strings.Contains(view, "SMOKE EXECUTION OK") {
		t.Fatalf("completed output not prioritized in view: %s", view)
	}
}

func TestModelShowsPlanExecutionOutputAndFailureOutput(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model.activeAction = "exec"
	model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "exec", Output: "step output"}, err: errors.New("step failed")})
	view := model.View().Content
	if !strings.Contains(view, "OUTPUT") || !strings.Contains(view, "step output") || !strings.Contains(view, "Error") {
		t.Fatalf("plan failure output not visible: %s", view)
	}
}

func TestModelRendersPopulatedOperationalScreens(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.snapshot = controlplane.Snapshot{
		Providers:    []controlplane.ProviderSnapshot{{Name: "Local", Configured: true, ModelCount: 1}},
		Models:       []controlplane.ModelSnapshot{{Name: "smoke-model", Provider: "local", Runtime: "api", Tier: "small", Status: "available"}},
		Plans:        []controlplane.PlanSnapshot{{Name: "smoke-plan.md"}},
		History:      []controlplane.HistorySnapshot{{Type: "execution.completed", Model: "smoke-model", Status: "success"}},
		Health:       []controlplane.HealthSnapshot{{ID: "state.permissions", Status: "PASS", Message: "safe"}},
		Analytics:    controlplane.AnalyticsSnapshot{LocalCollection: true, LocalPath: "~/.veto/logs", RetentionDays: 7, RemoteSharing: "opt_out"},
		Integrations: []controlplane.IntegrationSnapshot{{Name: "Hermes", Status: "current", Detail: "installed=1"}},
	}
	for _, test := range []struct {
		action string
		want   string
	}{
		{"providers", "Local"},
		{"models", "smoke-model"},
		{"plans", "smoke-plan.md"},
		{"history", "execution.completed"},
		{"doctor", "state.permissions"},
		{"analytics", "~/.veto/logs"},
		{"integrations", "Hermes"},
	} {
		t.Run(test.action, func(t *testing.T) {
			model.activeAction = test.action
			if view := model.View().Content; !strings.Contains(view, test.want) {
				t.Fatalf("%s screen missing %q: %s", test.action, test.want, view)
			}
		})
	}
}

func TestModelRejectsUnknownEventSchema(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	updated, _ := model.Update(eventMsg{event: controlplane.Event{Version: controlplane.SchemaVersion + 1, Kind: "route.completed"}, ok: true})
	model = updated.(*Model)
	if len(model.eventHistory) != 0 || !strings.Contains(model.status, "unsupported event schema") {
		t.Fatalf("unknown event state = history:%#v status:%q", model.eventHistory, model.status)
	}
}

func TestModelClosesActionContextAfterCompletion(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	cancelled := false
	model.running = true
	model.cancelRun = func() { cancelled = true }
	updated, _ := model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "route", Summary: "model selected"}})
	model = updated.(*Model)
	if !cancelled || model.cancelRun != nil || model.running {
		t.Fatalf("completion lifecycle = cancelled:%v cancel:%v running:%v", cancelled, model.cancelRun != nil, model.running)
	}
}
