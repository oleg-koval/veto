package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/router"
)

func TestModelRendersAccessibleShellAndStatusline(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := model.View()

	for _, want := range []string{"LOCAL AI CONTROL PLANE", "Ctrl+K commands", "STATUS", "providers", "? help"} {
		if !strings.Contains(view.Content, want) {
			t.Errorf("shell view missing %q\n%s", want, view.Content)
		}
	}
}

func TestModelStartsWithTaskFirstHome(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Version: "0.8.1-0.20260902070832-3734d74dbae2+dirty"})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := model.View().Content
	for _, want := range []string{
		"▛▀        ▄█ ▀▜",
		"█   █  █▀▀▀  ▀▀█▀▀  ▄▀▀▄",
		"TUI v0.8.1-dev",
		"COMMAND CENTER",
		"NEW MISSION",
		"What should Veto accomplish?",
		"FILTER → SHORTLIST → ADMIT → WINNER → EXECUTE → REVIEW",
		"SUGGESTED MISSIONS",
		"FLEET AT A GLANCE",
		"LIVE ROUTING",
		"No mission in flight",
		"Enter compose",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("task-first home missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "Every CLI command is available through the palette.") {
		t.Fatal("task-first home still renders the old generic instruction")
	}
	for _, old := range []string{"START HERE", "MISSION QUEUE", "DECISION PREVIEW", "20260902070832"} {
		if strings.Contains(view, old) {
			t.Fatalf("command center still renders old or noisy content %q:\n%s", old, view)
		}
	}
	if strings.Contains(view, "… more below") {
		t.Fatalf("command center clips at its target 120x40 viewport:\n%s", view)
	}
}

func TestModelPaletteKeepsSelectedCommandVisible(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model.paletteOpen = true
	model.paletteCursor = len(model.filteredActions()) - 1
	view := model.View().Content
	if want := fmt.Sprintf("%d commands", len(model.catalog.Commands())); !strings.Contains(view, want) {
		t.Fatalf("palette count missing:\n%s", view)
	}
	if !strings.Contains(view, "install-git-hook") {
		t.Fatalf("selected tail command is not visible:\n%s", view)
	}
	if !strings.Contains(view, "showing") {
		t.Fatalf("palette does not explain its visible window:\n%s", view)
	}
}

func TestModelRunShortcutOpensTaskComposerFromHome(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.selected = 2 // a stale rail selection must not change the home shortcut
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "run" {
		t.Fatalf("home run shortcut = open:%v action:%q", model.composerOpen, model.composerAction)
	}
}

func TestModelProviderComposerDefaultsToAnthropic(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.openProviderLogin("")

	if got := model.composerValues["provider"]; got != "anthropic" {
		t.Fatalf("provider default = %q, want anthropic", got)
	}
	if !strings.Contains(model.renderComposerControl(model.composerFields[0]), "anthropic") {
		t.Fatalf("provider control does not show its selected default: %s", model.renderComposerControl(model.composerFields[0]))
	}
}

func TestModelTypingStartsTaskComposerFromHome(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "f", Code: 'f'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "run" || model.composerInput != "f" {
		t.Fatalf("home typing = open:%v action:%q input:%q", model.composerOpen, model.composerAction, model.composerInput)
	}
}

func TestModelHomeComposerOwnsPrintableKeysAndEnter(t *testing.T) {
	for _, key := range []tea.Key{{Text: "m", Code: 'm'}, {Code: tea.KeyEnter}} {
		model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
		updated, _ := model.Update(tea.KeyPressMsg(key))
		model = updated.(*Model)
		if !model.composerOpen || model.composerAction != "run" {
			t.Fatalf("home input did not open run composer for %q", key.String())
		}
	}
}

func TestModelHomeControlShortcutsOpenOperatorViews(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "f", Code: 'f', Mod: tea.ModCtrl}))
	model = updated.(*Model)
	if model.activeAction != "models" || model.composerOpen {
		t.Fatalf("Ctrl+F did not open Fleet: action=%q composer=%v", model.activeAction, model.composerOpen)
	}
}

func TestModelAltIShortcutOpensIntegrationsWithoutConflictingWithTab(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "i", Code: 'i', Mod: tea.ModAlt}))
	model = updated.(*Model)
	if model.activeAction != "integrations" {
		t.Fatalf("Alt+I action = %q, want integrations", model.activeAction)
	}
}

func TestDisplayVersionCompactsDevelopmentMetadata(t *testing.T) {
	if got := displayVersion("v0.8.1-0.20260902070832-3734d74dbae2+dirty"); got != "0.8.1-dev" {
		t.Fatalf("displayVersion = %q, want 0.8.1-dev", got)
	}
	if got := displayVersion("v0.8.1"); got != "0.8.1" {
		t.Fatalf("release displayVersion = %q", got)
	}
}

func TestModelShortcutContextDrivesRunForm(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.activeAction = "models"
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "models" {
		t.Fatalf("shortcut context form = open:%v action:%q", model.composerOpen, model.composerAction)
	}
}

func TestModelEnterRunsActiveShortcutContext(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Service: staticService{}})
	model.activeAction = "models"
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.activeAction != "models" || !model.running || cmd == nil {
		t.Fatalf("active shortcut enter = action:%q running:%v cmd:%v", model.activeAction, model.running, cmd != nil)
	}
}

func TestModelFillsTerminalHeightAndNamesComposerContext(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "setup"
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	view := model.View().Content
	if got := lipgloss.Height(view); got != 40 {
		t.Fatalf("rendered height = %d, want terminal height 40", got)
	}
	if !strings.Contains(view, "Setup") || !strings.Contains(view, "Ctrl+Enter starts this mission") {
		t.Fatalf("composer context is unclear:\n%s", view)
	}
}

func TestModelSupportsKeyboardNavigationAndHelp(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.activeAction = "login"
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

func TestModelSupportsMouseTabNavigation(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	y := lipgloss.Height(model.renderAppHeader(88)) - 1
	x := -1
	for candidate := 0; candidate < 88; candidate++ {
		if model.primaryTabIndexAt(candidate, y) == 2 {
			x = candidate
			break
		}
	}
	if x < 0 {
		t.Fatal("Missions tab has no mouse hit target")
	}
	updated, _ := model.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	model = updated.(*Model)
	if model.activeAction != "history" {
		t.Fatalf("mouse tab action = %q, want history", model.activeAction)
	}
}

func TestModelShowsTabHintOnMouseHover(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	y := lipgloss.Height(model.renderAppHeader(88)) - 1
	x := -1
	for candidate := 0; candidate < 88; candidate++ {
		if model.primaryTabIndexAt(candidate, y) == 1 {
			x = candidate
			break
		}
	}
	if x < 0 {
		t.Fatal("Fleet tab has no mouse hit target")
	}
	updated, _ := model.Update(tea.MouseMotionMsg{X: x, Y: y})
	model = updated.(*Model)
	if !strings.Contains(model.status, "switch to fleet") {
		t.Fatalf("tab hover status = %q", model.status)
	}
}

func TestModelClipsLongTooltipStatusOnNarrowTerminal(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 32, Height: 12})
	model.status = "Hint · this deliberately long navigation description must fit"
	if lipgloss.Width(model.statusLine(32)) > 32 {
		t.Fatalf("status line width = %d, want <= 32", lipgloss.Width(model.statusLine(32)))
	}
}

func TestModelTabNavigationAndEscapeReturnHome(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	model = updated.(*Model)
	if model.activeAction != "models" {
		t.Fatalf("Tab action = %q, want models", model.activeAction)
	}
	model.output.WriteString("stale")
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	model = updated.(*Model)
	if model.activeAction != "" || model.output.String() != "stale" || model.isHome() {
		t.Fatalf("Escape did not preserve the Command Center result: action=%q output=%q", model.activeAction, model.output.String())
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "c", Code: 'c'}))
	model = updated.(*Model)
	if model.output.Len() != 0 || !model.isHome() {
		t.Fatalf("explicit clear did not reset the Command Center: output=%q", model.output.String())
	}
}

func TestModelTogglesBooleanFormFlagsWithSpace(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.activeAction = "install-git-hook"
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
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

func TestMissionComposerDistinguishesValuesFromCheckboxes(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.openTaskComposer("")
	view := model.View().Content
	if !strings.Contains(view, "Ctrl+Enter starts this mission") {
		t.Fatalf("mission composer does not explain how to start:\n%s", view)
	}
	for _, want := range []string{"Risk [medium]", "Budget [auto]", "Tools [auto]", "Criteria [optional]", "[Ctrl+Enter] RUN MISSION", "[Enter] or [Tab] configure"} {
		if !strings.Contains(view, want) {
			t.Fatalf("mission composer missing %q:\n%s", want, view)
		}
	}

	control := model.renderComposerControl(controlplane.FlagSpec{Name: "requires-executable-tools", Value: "bool"})
	if !strings.Contains(control, "[ ] disabled") {
		t.Fatalf("unchecked boolean control = %q", control)
	}
	model.composerValues["requires-executable-tools"] = "true"
	control = model.renderComposerControl(controlplane.FlagSpec{Name: "requires-executable-tools", Value: "bool"})
	if !strings.Contains(control, "[x] enabled") {
		t.Fatalf("checked boolean control = %q", control)
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

func TestModelTypingReplacesPrefilledFlagDefault(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	execAction, ok := model.catalog.Find("exec")
	if !ok {
		t.Fatal("exec action missing from catalog")
	}
	model.openComposer(execAction)
	if model.composerValues["timeout"] != "60s" {
		t.Fatalf("timeout default = %q, want 60s", model.composerValues["timeout"])
	}
	for model.composerFields[model.composerField].Name != "timeout" {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		model = updated.(*Model)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "5", Code: '5'}))
	model = updated.(*Model)
	if got := model.composerValues["timeout"]; got != "5" {
		t.Fatalf("typed timeout = %q, want 5", got)
	}
}

func TestModelLoginModeChoicesFollowProvider(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	login, ok := model.catalog.Find("login")
	if !ok {
		t.Fatal("login action missing from catalog")
	}
	model.openComposer(login)
	model.composerValues["provider"] = "openai"
	choices := model.fieldChoices(controlplane.FlagSpec{Name: "mode"})
	if len(choices) != 1 || choices[0] != "api-key" {
		t.Fatalf("openai mode choices = %#v", choices)
	}
	model.composerValues["provider"] = "openrouter"
	choices = model.fieldChoices(controlplane.FlagSpec{Name: "mode"})
	if len(choices) != 2 || choices[1] != "browser" {
		t.Fatalf("openrouter mode choices = %#v", choices)
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

func TestModelCommandPaletteSearchesCategories(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.paletteQuery = "diagnostics"
	filtered := model.filteredActions()
	if len(filtered) == 0 {
		t.Fatal("diagnostics category did not produce palette results")
	}
}

func TestModelCommandPaletteBackspaceRemovesCompleteRune(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.paletteOpen = true
	model.paletteQuery = "run🙂"

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	model = updated.(*Model)
	if model.paletteQuery != "run" {
		t.Fatalf("palette query after backspace = %q, want %q", model.paletteQuery, "run")
	}
	if !utf8.ValidString(model.paletteQuery) {
		t.Fatalf("palette query is invalid UTF-8: %q", model.paletteQuery)
	}
}

func TestModelRunningStatuslineAdvertisesCancellation(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.running = true
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(model.statusLine(80), "Esc cancel") {
		t.Fatalf("running statusline does not explain cancellation: %s", model.statusLine(80))
	}
}

func TestModelCommandPaletteIsVisibleInViewport(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	model = updated.(*Model)
	view := model.View().Content
	palette := strings.Index(view, "Command palette")
	if palette < 0 {
		t.Fatalf("palette overlay is missing:\n%s", view)
	}
	if lines := strings.Count(view[:palette], "\n"); lines >= 24 {
		t.Fatalf("palette overlay begins below viewport at line %d", lines)
	}
	if strings.Contains(view, "What do you want to do?") {
		t.Fatal("palette should take focus without rendering the home screen underneath")
	}
	if !strings.Contains(view, "↑/↓ move") || !strings.Contains(view, "Enter open") {
		t.Fatalf("palette footer is missing clear controls:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	model = updated.(*Model)
	if model.paletteOpen {
		t.Fatal("escape did not close the visible command palette")
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

func TestModelCommandPaletteLaunchesIntegrationForms(t *testing.T) {
	for _, actionID := range []string{"hermes", "opencode"} {
		t.Run(actionID, func(t *testing.T) {
			model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
			model.paletteOpen = true
			model.paletteQuery = actionID
			updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			model = updated.(*Model)
			if model.paletteOpen || !model.composerOpen || model.composerAction != actionID {
				t.Fatalf("integration palette launch = palette:%v composer:%v action:%q", model.paletteOpen, model.composerOpen, model.composerAction)
			}
			if model.composerValues["subcommand"] == "" {
				t.Fatal("integration form has no safe default operation")
			}
		})
	}
}

func TestModelIntegrationPageEnterRunsSelectedPrimaryAction(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Service: staticService{}})
	model.activeAction = "integrations"
	model.snapshot.Integrations = []controlplane.IntegrationSnapshot{{Name: "OpenCode", PrimaryAction: "connect"}, {Name: "Hermes", PrimaryAction: "status"}}
	model.integrationCursor = 1
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.running || model.activeAction != "hermes" || cmd == nil {
		t.Fatalf("selected integration action = running:%v action:%q cmd:%v", model.running, model.activeAction, cmd != nil)
	}
}

func TestModelIntegrationPageRAlsoRunsSelectedPrimaryAction(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Service: staticService{}})
	model.activeAction = "integrations"
	model.snapshot.Integrations = []controlplane.IntegrationSnapshot{{Name: "OpenCode", PrimaryAction: "connect"}, {Name: "Hermes", PrimaryAction: "status"}}
	model.integrationCursor = 1
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	model = updated.(*Model)
	if !model.running || model.activeAction != "hermes" || cmd == nil {
		t.Fatalf("selected integration action = running:%v action:%q cmd:%v", model.running, model.activeAction, cmd != nil)
	}
}

func TestModelImpeccableIntegrationAsksVetoToInstall(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Service: staticService{}})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model.activeAction = "integrations"
	model.snapshot.Integrations = []controlplane.IntegrationSnapshot{{Name: "Impeccable", Status: "available", Detail: "Install curated design skills directly into Veto", PrimaryAction: "install"}}
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	view := model.View().Content
	if cmd != nil || !model.confirmOpen || model.pendingRequest.ActionID != "impeccable" || !strings.Contains(view, "Install Impeccable for Veto?") {
		t.Fatalf("Impeccable install confirmation is missing: confirm=%v request=%#v cmd=%v\n%s", model.confirmOpen, model.pendingRequest, cmd != nil, view)
	}
}

func TestModelOpenCodeConnectIsPerformedByVetoAfterConfirmation(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Service: staticService{}})
	model.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	model.activeAction = "integrations"
	model.snapshot.Integrations = []controlplane.IntegrationSnapshot{{Name: "OpenCode", Status: "not configured", Detail: "Connect Veto to the installed OpenCode runtime", PrimaryAction: "connect"}}
	if view := model.View().Content; strings.Contains(view, "run veto opencode connect") || !strings.Contains(view, "[Enter] Connect") {
		t.Fatalf("OpenCode row still delegates setup to the user:\n%s", view)
	}
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if cmd != nil || !model.confirmOpen || model.pendingRequest.ActionID != "opencode" || model.pendingRequest.Arguments["subcommand"] != "connect" {
		t.Fatalf("OpenCode connect confirmation = open:%v request:%#v cmd:%v", model.confirmOpen, model.pendingRequest, cmd != nil)
	}
	updated, cmd = model.Update(tea.KeyPressMsg(tea.Key{Text: "y", Code: 'y'}))
	model = updated.(*Model)
	if !model.running || model.activeAction != "opencode" || cmd == nil {
		t.Fatalf("Veto did not start OpenCode connection: running=%v action=%q cmd=%v", model.running, model.activeAction, cmd != nil)
	}
}

func TestModelHermesRepairIsPerformedByVetoAfterConfirmation(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Service: staticService{}})
	model.activeAction = "integrations"
	model.snapshot.Integrations = []controlplane.IntegrationSnapshot{{Name: "Hermes", Status: "needs attention", Detail: "installed=3 missing=1 modified=1", PrimaryAction: "repair"}}
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if cmd != nil || !model.confirmOpen || model.pendingRequest.ActionID != "hermes" || model.pendingRequest.Arguments["operation"] != "install" || model.pendingRequest.Arguments["force"] != "true" {
		t.Fatalf("Hermes repair confirmation = open:%v request:%#v cmd:%v", model.confirmOpen, model.pendingRequest, cmd != nil)
	}
}

func TestModelIntegrationStatusFormStartsExecution(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Service: staticService{}})
	hermes, ok := model.catalog.Find("hermes")
	if !ok {
		t.Fatal("Hermes action missing")
	}
	model.openComposer(hermes)
	var cmd tea.Cmd
	for range len(model.composerFields) {
		updated, next := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		model = updated.(*Model)
		if next != nil {
			cmd = next
		}
	}
	if !model.running || model.activeAction != "hermes" || cmd == nil {
		t.Fatalf("Hermes status did not start: running=%v action=%q cmd=%v", model.running, model.activeAction, cmd != nil)
	}
}

func TestModelIntegrationFailureRendersPersistentResult(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.activeAction = "opencode"
	updated, _ := model.Update(executionResultMsg{
		result: controlplane.ActionResult{ActionID: "opencode"},
		err:    errors.New("OpenCode is not connected; run veto opencode connect"),
	})
	model = updated.(*Model)
	view := model.View().Content
	if !strings.Contains(view, "OPENCODE RESULT") || !strings.Contains(view, "OpenCode is not connected") {
		t.Fatalf("integration error is not persistent:\n%s", view)
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

func TestModelFleetExposesProviderManagement(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	model.activeAction = "models"
	model.snapshot.Providers = []controlplane.ProviderSnapshot{
		{Name: "Anthropic", Configured: false},
		{Name: "OpenAI", Configured: true, ModelCount: 3},
		{Name: "Codex", Configured: true, ModelCount: 1},
	}

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "p", Code: 'p'}))
	model = updated.(*Model)
	view := model.View().Content
	for _, want := range []string{"FLEET · PROVIDERS", "OpenAI", "connected", "Veto credentials", "[Enter] Manage", "A Add provider"} {
		if !strings.Contains(view, want) {
			t.Fatalf("provider management surface missing %q:\n%s", want, view)
		}
	}

	model.dataCursor = 1
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	view = model.View().Content
	for _, want := range []string{"FLEET · PROVIDER MANAGER", "OPENAI", "[E] EDIT / UPDATE", "[V] VERIFY MODELS", "[X] REMOVE"} {
		if !strings.Contains(view, want) {
			t.Fatalf("provider manager missing %q:\n%s", want, view)
		}
	}
}

func TestModelProviderActionsUseTypedVetoCommands(t *testing.T) {
	tests := []struct {
		name       string
		key        tea.Key
		wantAction string
		confirm    bool
	}{
		{name: "edit", key: tea.Key{Text: "e", Code: 'e'}, wantAction: "login"},
		{name: "verify", key: tea.Key{Text: "v", Code: 'v'}, wantAction: "verify-models"},
		{name: "remove", key: tea.Key{Text: "x", Code: 'x'}, wantAction: "logout", confirm: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
			model.activeAction = "providers"
			model.snapshot.Providers = []controlplane.ProviderSnapshot{{Name: "OpenAI", Configured: true, ModelCount: 3}}
			model.dataDetailOpen = true
			updated, _ := model.Update(tea.KeyPressMsg(test.key))
			model = updated.(*Model)
			if test.confirm {
				if !model.confirmOpen || model.pendingRequest.ActionID != test.wantAction || model.pendingRequest.Arguments["target"] != "openai" {
					t.Fatalf("remove request = confirm:%v action:%q args:%v", model.confirmOpen, model.pendingRequest.ActionID, model.pendingRequest.Arguments)
				}
				return
			}
			if !model.composerOpen || model.composerAction != test.wantAction || model.composerValues["provider"] != "openai" || !model.returnToProviders {
				t.Fatalf("provider action = open:%v action:%q provider:%q return:%v", model.composerOpen, model.composerAction, model.composerValues["provider"], model.returnToProviders)
			}
		})
	}
}

func TestModelProviderFilteringSelectsTheVisibleProvider(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.activeAction = "providers"
	model.snapshot.Providers = []controlplane.ProviderSnapshot{
		{Name: "Anthropic", Configured: true},
		{Name: "OpenAI", Configured: false},
	}
	model.dataFilterQuery = "openai"
	model.dataDetailOpen = true
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "c", Code: 'c'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerValues["provider"] != "openai" {
		t.Fatalf("filtered provider action targeted %q", model.composerValues["provider"])
	}
}

func TestModelProviderRowsOpenWithMouse(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "providers"
	model.snapshot.Providers = []controlplane.ProviderSnapshot{
		{Name: "Anthropic", Configured: true},
		{Name: "OpenAI", Configured: true},
	}
	mainWidth := 120 - 32
	rowY := lipgloss.Height(model.renderAppHeader(mainWidth)) + 12
	updated, _ := model.Update(tea.MouseClickMsg{X: 8, Y: rowY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if model.dataCursor != 1 || !model.dataDetailOpen || !strings.Contains(model.View().Content, "OPENAI") {
		t.Fatalf("provider mouse open = cursor:%d detail:%v\n%s", model.dataCursor, model.dataDetailOpen, model.View().Content)
	}
}

func TestModelProviderResultReturnsToProviderList(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.returnToProviders = true
	model.activeAction = "login"
	updated, _ := model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "login", Summary: "OpenAI connected"}})
	model = updated.(*Model)
	if model.activeAction != "providers" || model.status != "Ready · OpenAI connected" {
		t.Fatalf("provider result = action:%q status:%q", model.activeAction, model.status)
	}
}

func TestModelFiltersMissionRowsAndClearsFilter(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "history"
	model.snapshot.History = []controlplane.HistorySnapshot{
		{Type: "execution.completed", Model: "luna", Status: "success", Runtime: "OpenAI API"},
		{Type: "review.error", Model: "sol", Status: "error", Runtime: "OpenAI API"},
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	model = updated.(*Model)
	for _, char := range "luna" {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: string(char), Code: char}))
		model = updated.(*Model)
	}
	view := model.View().Content
	if !strings.Contains(view, "1/2 rows") || !strings.Contains(view, "luna") || strings.Contains(view, "review.error") {
		t.Fatalf("filtered mission view is incorrect:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	model = updated.(*Model)
	if model.dataFilterOpen || model.dataFilterQuery != "" {
		t.Fatalf("filter did not clear: open=%v query=%q", model.dataFilterOpen, model.dataFilterQuery)
	}
}

func TestModelGroupsMissionHistoryByRunAndFiltersWithinGroup(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.activeAction = "history"
	model.snapshot.History = []controlplane.HistorySnapshot{
		{Timestamp: time.Date(2026, 9, 5, 9, 3, 0, 0, time.UTC), RunID: "run-a", Type: "execution.completed", Model: "luna", Runtime: "OpenAI API", Status: "success"},
		{Timestamp: time.Date(2026, 9, 5, 9, 2, 0, 0, time.UTC), RunID: "run-a", Type: "route.filter_pass", Model: "gpt-5.6", Status: ""},
		{Timestamp: time.Date(2026, 9, 5, 9, 1, 0, 0, time.UTC), RunID: "run-b", Type: "execution.completed", Model: "sonnet", Runtime: "Claude CLI", Status: "success"},
	}
	rows := model.historyDataRows()
	if len(rows) != 2 || !strings.HasPrefix(rows[0][1], "execution · ") || !strings.HasSuffix(rows[0][1], " · a") || rows[0][3] != "success" || rows[0][5] != "2" {
		t.Fatalf("mission rows were not grouped: %#v", rows)
	}
	model.dataFilterQuery = "filter_pass"
	rows = model.historyDataRows()
	if len(rows) != 1 || !strings.HasPrefix(rows[0][1], "execution · ") || !strings.HasSuffix(rows[0][1], " · a") {
		t.Fatalf("filter did not retain the matching mission group: %#v", rows)
	}
	model.dataFilterQuery = ""
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.dataDetailOpen || !strings.Contains(model.View().Content, "MISSION TIMELINE · 2 EVENTS") {
		t.Fatalf("grouped mission did not open its complete timeline:\n%s", model.View().Content)
	}
}

func TestModelPaginatesFilteredMissionRowsAndNavigatesPages(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model.activeAction = "history"
	for index := 0; index < 18; index++ {
		name := fmt.Sprintf("model-%02d", index)
		if index == 2 || index == 9 || index == 16 {
			name = "target"
		}
		model.snapshot.History = append(model.snapshot.History, controlplane.HistorySnapshot{Type: fmt.Sprintf("event-%02d", index), Model: name})
	}

	view := model.View().Content
	if !strings.Contains(view, "Page 1/4") || !strings.Contains(view, "rows 1–5 of 18") || strings.Contains(view, "event-05") {
		t.Fatalf("first mission page is incorrect:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	model = updated.(*Model)
	view = model.View().Content
	if !strings.Contains(view, "Page 2/4") || !strings.Contains(view, "event-05") || strings.Contains(view, "event-00") {
		t.Fatalf("second mission page is incorrect:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	model = updated.(*Model)
	for _, char := range "target" {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: string(char), Code: char}))
		model = updated.(*Model)
	}
	view = model.View().Content
	if !strings.Contains(view, "3/18 rows") || !strings.Contains(view, "Page 1/1") || !strings.Contains(view, "event-02") || !strings.Contains(view, "event-09") || !strings.Contains(view, "event-16") || strings.Contains(view, "event-01") {
		t.Fatalf("filter did not search every mission page:\n%s", view)
	}
}

func TestModelMissionRowsSupportMouseSelectionAndDetails(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "history"
	model.snapshot.History = []controlplane.HistorySnapshot{
		{Type: "event.first", Model: "luna"},
		{Type: "event.clicked", Model: "sol"},
	}
	mainWidth := 120 - 32
	rowY := lipgloss.Height(model.renderAppHeader(mainWidth)) + 11
	updated, _ := model.Update(tea.MouseClickMsg{X: 8, Y: rowY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if model.dataCursor != 1 || !strings.Contains(model.status, "event.clicked") {
		t.Fatalf("mouse selection = cursor:%d status:%q", model.dataCursor, model.status)
	}
	if view := model.View().Content; !strings.Contains(view, "› ") || !strings.Contains(view, "event.clicked") {
		t.Fatalf("selected row is not visibly marked:\n%s", view)
	}
	view := model.View().Content
	if !model.dataDetailOpen || !strings.Contains(view, "MISSIONS · MISSION INSPECTOR") || !strings.Contains(view, "event.clicked") {
		t.Fatalf("clicking the row did not open details:\n%s", view)
	}
}

func TestModelMissionErrorInspectorShowsRunEvidenceAndAIDiagnosis(t *testing.T) {
	cost := 0.012345
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 150, Height: 48})
	model.activeAction = "history"
	model.snapshot.History = []controlplane.HistorySnapshot{
		{Timestamp: time.Date(2026, 9, 4, 20, 10, 57, 0, time.UTC), EventID: "event-review", RunID: "run-123", Type: "review.error", Status: "error", Runtime: "Codex CLI", Detail: "reviewer unavailable", CostUSD: cost, CostKnown: true},
		{Timestamp: time.Date(2026, 9, 4, 20, 10, 29, 0, time.UTC), EventID: "event-start", RunID: "run-123", Type: "execution.started", Model: "luna", Runtime: "OpenAI API", Reasons: []string{"tool-fit"}, Confidence: 0.91, ConfidenceKnown: true},
	}

	if view := model.View().Content; !strings.Contains(view, "F diagnose") {
		t.Fatalf("mission failure is missing its diagnosis action:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	view := model.View().Content
	for _, want := range []string{"MISSIONS · MISSION INSPECTOR", "Acceptance review failed", "run-123", "reviewer unavailable", "Actual cost: $0.012345", "MISSION TIMELINE · 2 EVENTS", "execution.started", "[F] DIAGNOSE WITH AI"} {
		if !strings.Contains(view, want) {
			t.Fatalf("mission inspector missing %q:\n%s", want, view)
		}
	}

	buttonX, buttonY, found := 0, 0, false
	for y := 0; y < model.height && !found; y++ {
		for x := 0; x < model.width; x++ {
			if model.dataDetailFixButtonAt(x, y) {
				buttonX, buttonY, found = x, y, true
				break
			}
		}
	}
	if !found {
		t.Fatal("mission Diagnose with AI mouse target was not found")
	}
	updated, _ = model.Update(tea.MouseClickMsg{X: buttonX, Y: buttonY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if got := model.composerValues["kind"]; got != string(router.KindCodeChange) {
		t.Fatalf("Diagnose with AI kind = %q, want %q", got, router.KindCodeChange)
	}
	if got := model.composerValues["requires-executable-tools"]; got != "true" {
		t.Fatalf("Diagnose with AI executable-tools = %q, want true", got)
	}
	for _, want := range []string{"review.error", "run-123", "reviewer unavailable", "Related redacted run events", "search existing issues", "oleg-koval/veto"} {
		if !strings.Contains(model.composerInput, want) {
			t.Fatalf("mission diagnosis prompt missing %q:\n%s", want, model.composerInput)
		}
	}
}

func TestModelMissionInspectorScrollsAndSelectsEveryEvent(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "history"
	for index := 0; index < 30; index++ {
		model.snapshot.History = append(model.snapshot.History, controlplane.HistorySnapshot{
			Timestamp: time.Date(2026, 9, 5, 12, 0, index, 0, time.UTC), RunID: "run-scroll", Type: fmt.Sprintf("event-%02d", index), Model: "luna",
		})
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	view := model.View().Content
	if !strings.Contains(view, "MISSION TIMELINE · EVENTS 1–16 OF 30") || strings.Contains(view, "earlier events") {
		t.Fatalf("mission inspector did not render a scrollable full timeline:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	model = updated.(*Model)
	if event, ok := model.selectedHistoryEvent(); !ok || event.Type != "event-01" {
		t.Fatalf("keyboard event selection = %#v, ok=%v", event, ok)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	model = updated.(*Model)
	if event, ok := model.selectedHistoryEvent(); !ok || event.Type != "event-29" || !strings.Contains(model.View().Content, "event-29") {
		t.Fatalf("End did not reveal the final event = %#v, ok=%v\n%s", event, ok, model.View().Content)
	}
	plain := ansi.Strip(model.View().Content)
	lines := strings.Split(plain, "\n")
	clickY := -1
	for index, line := range lines {
		if strings.Contains(line, "event-28") {
			clickY = index
			break
		}
	}
	if clickY < 0 {
		t.Fatal("final event was not rendered for mouse selection")
	}
	updated, _ = model.Update(tea.MouseClickMsg{X: 60, Y: clickY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if event, ok := model.selectedHistoryEvent(); !ok || event.Type != "event-28" {
		t.Fatalf("mouse event selection = %#v, ok=%v", event, ok)
	}
	updated, _ = model.Update(tea.MouseReleaseMsg{X: 60, Y: clickY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if !model.dataDetailOpen {
		t.Fatalf("mouse release closed the mission inspector")
	}
}

func TestModelSeparatesHarnessesFromModels(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	model.activeAction = "models"
	model.snapshot.Models = []controlplane.ModelSnapshot{
		{Name: "codex", Kind: controlplane.ModelKindHarness, Provider: "codex", Runtime: "codex-cli", Status: "available"},
		{Name: "luna", ModelID: "gpt-5.6-luna", Kind: controlplane.ModelKindModel, Provider: "openai", Runtime: "openai-api", Status: "available"},
	}
	view := model.View().Content
	for _, want := range []string{"Route", "Model ID", "luna", "gpt-5.6-luna", "HARNESSES · MODEL SELECTED BY HOST", "Codex CLI", "host-selected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Fleet identity view missing %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "codex") && !strings.Contains(line, "host-selected") {
			t.Fatalf("codex rendered as a model row:\n%s", line)
		}
	}
}

func TestModelHealthWarningOffersReviewableFixWithAI(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "doctor"
	model.snapshot.Health = []controlplane.HealthSnapshot{
		{ID: "state.permissions", Status: "PASS", Message: "state is private"},
		{ID: "install.path", Status: "WARN", Message: "another veto executable takes precedence on PATH"},
	}
	model.dataCursor = 1

	if view := model.View().Content; !strings.Contains(view, "[F] Fix with") {
		t.Fatalf("health warning is missing Fix with AI action:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if view := model.View().Content; !model.dataDetailOpen || !strings.Contains(view, "[F] FIX WITH AI") {
		t.Fatalf("health details are missing Fix with AI button:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "f", Code: 'f'}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerAction != "run" {
		t.Fatalf("Fix with AI did not open the mission composer: open=%v action=%q", model.composerOpen, model.composerAction)
	}
	if got := model.composerValues["kind"]; got != string(router.KindCodeChange) {
		t.Fatalf("Fix with AI kind = %q, want %q", got, router.KindCodeChange)
	}
	if got := model.composerValues["requires-executable-tools"]; got != "true" {
		t.Fatalf("Fix with AI executable-tools = %q, want true", got)
	}
	for _, want := range []string{"install.path", "search existing issues", "oleg-koval/veto", "structured GitHub bug report", "Redact credentials", "post-run health refresh as authoritative"} {
		if !strings.Contains(model.composerInput, want) {
			t.Fatalf("Fix with AI prompt missing %q:\n%s", want, model.composerInput)
		}
	}
}

func TestModelHealthFixWithAIAcceptsDisplayedShortcutAndEnter(t *testing.T) {
	for _, key := range []tea.Key{
		{Text: "f", Code: 'f'},
		{Text: "F", Code: 'F'},
		{Code: tea.KeyEnter},
	} {
		t.Run(key.String(), func(t *testing.T) {
			model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
			model.activeAction = "doctor"
			model.snapshot.Health = []controlplane.HealthSnapshot{{ID: "install.path", Status: "WARN", Message: "another veto executable takes precedence on PATH"}}
			model.dataDetailOpen = true

			updated, _ := model.Update(tea.KeyPressMsg(key))
			model = updated.(*Model)
			if !model.composerOpen || model.composerAction != "run" || !strings.Contains(model.composerInput, "install.path") {
				t.Fatalf("key %q did not activate Fix with AI: open=%v action=%q", key.String(), model.composerOpen, model.composerAction)
			}
		})
	}
}

func TestModelHealthFixWithAIButtonSupportsMouse(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "doctor"
	model.snapshot.Health = []controlplane.HealthSnapshot{{ID: "install.path", Status: "WARN", Message: "another veto executable takes precedence on PATH"}}
	model.dataDetailOpen = true

	buttonX, buttonY, found := 0, 0, false
	for y := 0; y < model.height && !found; y++ {
		for x := 0; x < model.width; x++ {
			if model.dataDetailFixButtonAt(x, y) {
				buttonX, buttonY, found = x, y, true
				break
			}
		}
	}
	if !found {
		t.Fatal("Fix with AI mouse target was not found")
	}
	updated, _ := model.Update(tea.MouseClickMsg{X: buttonX, Y: buttonY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if !model.composerOpen || !strings.Contains(model.composerInput, "install.path") {
		t.Fatalf("clicking Fix with AI did not open the expected mission composer")
	}
}

func TestModelHealthRepairRechecksAndReportsFindingOutcome(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Service: staticService{}})
	model.activeHealthFix = "install.path"
	model.running = true
	updated, cmd := model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "run", Summary: "task completed", Output: "repair report"}})
	model = updated.(*Model)
	if cmd == nil || !model.verifyHealthFix || !strings.Contains(model.status, "Verifying") {
		t.Fatalf("repair completion did not start verification: cmd=%v pending=%v status=%q", cmd != nil, model.verifyHealthFix, model.status)
	}
	updated, _ = model.Update(snapshotMsg{snapshot: controlplane.Snapshot{Health: []controlplane.HealthSnapshot{{ID: "install.path", Status: "PASS", Message: "running executable is current"}}}})
	model = updated.(*Model)
	if model.verifyHealthFix || !strings.Contains(model.status, "Verified fixed") || !strings.Contains(model.healthVerification, "PASS") {
		t.Fatalf("repair verification = pending:%v status:%q result:%q", model.verifyHealthFix, model.status, model.healthVerification)
	}
	model.goHome()
	view := model.View().Content
	if !strings.Contains(view, "AUTHORITATIVE HEALTH VERIFICATION") || !strings.Contains(view, "repair report") || !strings.Contains(view, "c clear result") {
		t.Fatalf("completed repair was not retained on Command Center:\n%s", view)
	}
}

func TestModelBuildProvenanceUsesExplainActionAndBindsPromptToRunningBinary(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{
		Motion:     false,
		NoColor:    true,
		Version:    "0.8.0-105-g3734d74-dirty",
		Executable: "/private/tmp/veto-tui-controlplane/veto",
	})
	model.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
	model.activeAction = "doctor"
	model.snapshot.Health = []controlplane.HealthSnapshot{{ID: "build.provenance", Status: "WARN", Message: "release provenance is not claimed"}}

	if view := model.View().Content; !strings.Contains(view, "[F] Explain with AI") || strings.Contains(view, "[F] Fix with AI") {
		t.Fatalf("informational provenance warning has the wrong action:\n%s", view)
	}
	model.openSelectedHealthFix()
	for _, want := range []string{
		"Subject executable: /private/tmp/veto-tui-controlplane/veto",
		"Subject TUI version: 0.8.0-105-g3734d74-dev",
		"Do not substitute the first \"veto\" found on PATH",
		"never declare success from a different installation",
	} {
		if !strings.Contains(model.composerInput, want) {
			t.Fatalf("provenance explanation prompt missing %q:\n%s", want, model.composerInput)
		}
	}
}

func TestModelBuildProvenanceVerificationReportsExpectedDevelopmentWarning(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.activeHealthFix = "build.provenance"
	model.verifyHealthFix = true

	updated, _ := model.Update(snapshotMsg{snapshot: controlplane.Snapshot{Health: []controlplane.HealthSnapshot{{
		ID: "build.provenance", Status: "WARN", Message: "release provenance is not claimed",
	}}}})
	model = updated.(*Model)
	if !strings.Contains(model.healthVerification, "EXPECTED") || !strings.Contains(model.status, "Expected development warning") {
		t.Fatalf("development provenance warning was represented as a failed repair: status=%q verification=%q", model.status, model.healthVerification)
	}
}

func TestModelHealthFixWithAIActionCellSupportsMouse(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, Mouse: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	model.activeAction = "doctor"
	model.snapshot.Health = []controlplane.HealthSnapshot{{ID: "install.path", Status: "WARN", Message: "another veto executable takes precedence on PATH"}}

	buttonX, buttonY, found := 0, 0, false
	for y, line := range strings.Split(model.View().Content, "\n") {
		if x := strings.Index(line, "[F] Fix with AI"); x >= 0 {
			buttonX, buttonY, found = x, y, true
			break
		}
	}
	if !found {
		t.Fatal("health table Fix with AI action cell was not found")
	}
	updated, _ := model.Update(tea.MouseClickMsg{X: buttonX, Y: buttonY, Button: tea.MouseLeft})
	model = updated.(*Model)
	if !model.composerOpen || !strings.Contains(model.composerInput, "install.path") {
		t.Fatalf("clicking the health table Fix with AI action did not open the expected mission composer")
	}
}

func TestModelUsesSharedShellForOperationalPages(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.activeAction = "models"
	model.snapshot.Models = []controlplane.ModelSnapshot{{Name: "gpt-test", Provider: "openai", Runtime: "openai-api", Tier: "mid"}}
	view := model.View().Content
	for _, want := range []string{"LOCAL AI CONTROL PLANE", "FLEET · MODEL CATALOG", "Route", "Model ID", "Provider", "Esc home"} {
		if !strings.Contains(view, want) {
			t.Fatalf("shared Fleet shell missing %q:\n%s", want, view)
		}
	}
}

func TestRenderDataTableKeepsColumnsAligned(t *testing.T) {
	table := ansi.Strip(renderDataTable(
		[]string{"Model", "Provider", "Runtime"},
		[][]string{{"codex", "openai", "codex-cli"}, {"haiku", "anthropic", "claude-cli"}},
		60,
	))
	lines := strings.Split(table, "\n")
	if len(lines) != 4 {
		t.Fatalf("table lines = %d, want 4:\n%s", len(lines), table)
	}
	providerColumn := strings.Index(lines[0], "Provider")
	runtimeColumn := strings.Index(lines[0], "Runtime")
	for _, row := range lines[2:] {
		if strings.Index(row, strings.Fields(row)[1]) != providerColumn || strings.Index(row, strings.Fields(row)[2]) != runtimeColumn {
			t.Fatalf("table columns are not aligned:\n%s", table)
		}
	}
}

func TestModelTableKeepsPolicyOnTheModelRow(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.snapshot.Models = []controlplane.ModelSnapshot{{Name: "gpt-test", Provider: "openai", Runtime: "openai-api", Tier: "mid", ContextTokens: 128000, Status: "available", Excluded: true}}
	for _, width := range []int{80, 128} {
		view := ansi.Strip(model.renderModels(width))
		found := false
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "gpt-test") {
				found = true
				if !strings.Contains(line, "excluded") {
					t.Fatalf("%d-column model policy wrapped away from its row:\n%s", width, view)
				}
			}
		}
		if !found {
			t.Fatalf("%d-column model row missing:\n%s", width, view)
		}
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

func TestModelOperationalScreensExplainEmptyState(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for action, want := range map[string]string{
		"providers":    "No providers configured",
		"models":       "No configured models",
		"history":      "No redacted activity yet",
		"plans":        "No plans found",
		"exec":         "No plans found",
		"doctor":       "No health findings",
		"integrations": "No integrations detected",
	} {
		model.activeAction = action
		if view := model.View().Content; !strings.Contains(view, want) {
			t.Errorf("%s empty screen missing %q:\n%s", action, want, view)
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

func TestModelPlansFilterAcrossPagesBeforeExecution(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.activeAction = "plans"
	for index := 0; index < 11; index++ {
		model.snapshot.Plans = append(model.snapshot.Plans, controlplane.PlanSnapshot{Name: fmt.Sprintf("plan-%02d.md", index)})
	}
	model.snapshot.Plans = append(model.snapshot.Plans, controlplane.PlanSnapshot{Name: "release.md"})

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	model = updated.(*Model)
	for _, char := range "release" {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: string(char), Code: char}))
		model = updated.(*Model)
	}
	view := model.View().Content
	if !strings.Contains(view, "1/12 rows") || !strings.Contains(view, "release.md") || strings.Contains(view, "plan-00.md") {
		t.Fatalf("plan filter did not search all pages:\n%s", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.composerOpen || model.composerValues["plan"] != "release.md" {
		t.Fatalf("filtered plan was not opened: open=%v plan=%q", model.composerOpen, model.composerValues["plan"])
	}
}

func TestModelRendersMonitorCountersAndRuntimeState(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.snapshot = controlplane.Snapshot{
		Model:     "gpt-test",
		Providers: []controlplane.ProviderSnapshot{{Name: "openai", Configured: true}},
		Monitor:   controlplane.MonitorSnapshot{ActiveSessions: 1, ActiveTools: 2, PendingApprovals: 1, Artifacts: 3, InputTokens: 90, OutputTokens: 9, TotalTokens: 99, TokensKnown: true, CostUSD: 0.42, CostKnown: true, LatencyMs: 120, LatencyKnown: true},
	}
	model.running = true
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := model.View().Content
	for _, want := range []string{"running", "active runs  1", "active tools 2", "approvals    1", "exec gross   90", "exec reused  unknown", "exec fresh   unknown", "exec output  9", "exec total   99", "last cost    $0.4200", "last time    120ms", "artifacts    3"} {
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

func TestModelRoutingSweepUsesAvailableWidth(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: true, NoColor: true})
	track := ansi.Strip(model.renderRoutingSweep(100))
	if lipgloss.Width(track) != 100 {
		t.Fatalf("routing sweep width = %d, want 100", lipgloss.Width(track))
	}
}

func TestModelRoutingPipelineHighlightsAndAnimatesActiveStage(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: true, NoColor: true})
	model.running = true
	model.eventHistory = []controlplane.Event{{Kind: "route.shortlist"}}
	model.frame = 0
	first := model.renderPipeline(120)
	model.frame = 1
	second := model.renderPipeline(120)
	if first == second {
		t.Fatal("active routing stage did not animate between frames")
	}
	for _, want := range []string{"✓ FILTER", "SHORTLIST", "ADMIT", "WINNER", "EXECUTE", "REVIEW"} {
		if !strings.Contains(first, want) {
			t.Fatalf("pipeline missing %q: %s", want, first)
		}
	}
}

func TestFormatMultilineRemovesMarkdownFenceMarkers(t *testing.T) {
	got := formatMultiline("```bash\nrepair --offline\n```", 80, 10)
	if strings.Contains(got, "```") || !strings.Contains(got, "repair --offline") {
		t.Fatalf("markdown fences leaked into output: %q", got)
	}
}

func TestModelAnimatesRealRoutingFlowAndCandidateAdmission(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: true, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model.running = true
	model.activeAction = "run"
	model.frame = 2
	model.eventHistory = []controlplane.Event{
		{Kind: "route.filter_pass", Model: "luna", Message: "luna"},
		{Kind: "route.shortlist", Message: "3 candidates"},
		{Kind: "route.ask_start", Model: "luna", Message: "luna"},
		{Kind: "route.ask_reject", Model: "luna", Message: "luna"},
		{Kind: "route.ask_start", Model: "sol", Message: "sol"},
	}

	view := model.View().Content
	for _, want := range []string{"ADMIT IN PROGRESS", "CANDIDATE ADMISSION", "luna", "rejected", "sol", "evaluating", "◆"} {
		if !strings.Contains(view, want) {
			t.Fatalf("animated routing flow missing %q:\n%s", want, view)
		}
	}
}

func TestModelRoutingFlowHasStableReducedMotionFrame(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.running = true
	model.activeAction = "route"
	model.frame = 7
	first := model.renderRoutingAnimation(60)
	model.frame = 9
	second := model.renderRoutingAnimation(60)
	if first != second {
		t.Fatalf("reduced-motion routing animation changed between frames:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, "FILTER IN PROGRESS") || !strings.Contains(first, "◆") {
		t.Fatalf("reduced-motion routing state is not informative:\n%s", first)
	}
}

func TestModelEnteringHealthRunsFreshDiagnosticsAndHidesStaleRows(t *testing.T) {
	service := staticService{}
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Service: service})
	model.snapshot.Health = []controlplane.HealthSnapshot{{ID: "stale.check", Status: "WARN", Message: "old result"}}

	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Mod: tea.ModCtrl}))
	model = updated.(*Model)
	view := model.View().Content
	if cmd == nil || !model.running || !model.healthLoading {
		t.Fatalf("health entry did not start diagnostics: cmd=%v running=%v loading=%v", cmd != nil, model.running, model.healthLoading)
	}
	if !strings.Contains(view, "RUNNING DIAGNOSTICS") || !strings.Contains(view, "Previous results are hidden") || strings.Contains(view, "stale.check") {
		t.Fatalf("health loading state is unclear or leaks stale results:\n%s", view)
	}
}

func TestModelHealthShowsExplicitRefreshAction(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.activeAction = "doctor"
	model.snapshot.Health = []controlplane.HealthSnapshot{{ID: "state.shape", Status: "PASS", Message: "valid"}}
	view := model.View().Content
	if !strings.Contains(view, "[R] RUN AGAIN") || !strings.Contains(view, "R refreshes all checks") {
		t.Fatalf("health screen missing refresh affordance:\n%s", view)
	}
}

func TestModelHealthRefreshPreservesCompletedMissionResult(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true, Service: staticService{}})
	model.output.WriteString("completed mission output")
	model.outputAction = "run"
	model.eventHistory = []controlplane.Event{{Kind: "route.ask_accept", Model: "luna"}}
	model.openPrimaryView(3)

	updated, cmd := model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "doctor", Summary: "diagnostics complete"}})
	model = updated.(*Model)
	if cmd == nil || model.output.String() != "completed mission output" || len(model.eventHistory) != 1 {
		t.Fatalf("health refresh discarded retained mission result: output=%q events=%d cmd=%v", model.output.String(), len(model.eventHistory), cmd != nil)
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
	}{{40, 12}, {80, 24}, {120, 40}, {180, 55}, {40, 2}, {40, 1}} {
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
	model.activeAction = "doctor"
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.activeAction != "doctor" || !model.running || cmd == nil {
		t.Fatalf("doctor execution state = action:%q running:%v cmd:%v", model.activeAction, model.running, cmd != nil)
	}
}

func TestModelProvidersEnterRunsThroughService(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Service: staticService{}})
	model.activeAction = "providers"
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

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
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

func TestModelComposerExplainsAndSupportsDirectRun(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.openAIFixComposer("Investigate and resolve this Veto health finding.")

	for _, want := range []string{"NEXT STEP", "[Ctrl+Enter] RUN SAFE REPAIR", "[Enter] or [Tab] configure"} {
		if view := model.View().Content; !strings.Contains(view, want) {
			t.Fatalf("composer missing %q:\n%s", want, view)
		}
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	model = updated.(*Model)
	if model.composerOpen || model.activeAction != "run" || model.status != "Ready · service unavailable in preview" {
		t.Fatalf("direct run = composer:%v action:%q status:%q", model.composerOpen, model.activeAction, model.status)
	}
}

func TestModelOpensFlagFormForNonRoutingActionWithEnter(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.activeAction = "feedback"
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if !model.composerOpen || !model.composerEditing || model.composerAction != "feedback" {
		t.Fatalf("feedback form = open:%v editing:%v action:%q", model.composerOpen, model.composerEditing, model.composerAction)
	}
	if len(model.composerFields) == 0 || model.composerFields[0].Name != "kind" {
		t.Fatalf("feedback fields = %#v", model.composerFields)
	}
}

func TestModelOpensOperationalFormsWithSafeSubcommandDefaults(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.activeAction = "analytics"
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(*Model)
	if model.activeAction != "analytics" || !model.composerOpen {
		t.Fatalf("analytics enter = action:%q composer:%v", model.activeAction, model.composerOpen)
	}
	if !model.composerOpen || model.composerValues["subcommand"] != "status" {
		t.Fatalf("analytics form = open:%v subcommand:%q", model.composerOpen, model.composerValues["subcommand"])
	}
}

func TestModelConfirmsStateChangingActionAndMasksSecretFields(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false})
	model.activeAction = "login"
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
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

func TestModelConfirmationExplainsActionWithoutLeakingSecrets(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.confirmOpen = true
	model.pendingRequest = controlplane.ActionRequest{
		ActionID:  "login",
		Arguments: map[string]string{"provider": "openai", "api-key": "sk-confirmation-secret"},
	}
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	view := model.View().Content
	if !strings.Contains(view, "Connect a provider with masked key input") || !strings.Contains(view, "provider: openai") {
		t.Fatalf("confirmation lacks useful context:\n%s", view)
	}
	if strings.Contains(view, "sk-confirmation-secret") {
		t.Fatalf("confirmation leaked secret:\n%s", view)
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

func TestModelDoesNotDumpMultilineCommandOutputIntoActivity(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.activeAction = "models"
	model.eventHistory = []controlplane.Event{{
		Version:  controlplane.SchemaVersion,
		ActionID: "models",
		Kind:     "output",
		Message:  "model provider runtime\ncodex openai codex-cli",
	}}
	timeline := model.renderLiveTimeline(100)
	if strings.Contains(timeline, "codex openai") || !strings.Contains(timeline, "execution output") || !strings.Contains(timeline, "received") {
		t.Fatalf("activity leaked multiline output:\n%s", timeline)
	}
	updated, _ := model.Update(executionResultMsg{result: controlplane.ActionResult{ActionID: "models", Summary: "models complete"}})
	model = updated.(*Model)
	if len(model.eventHistory) != 0 {
		t.Fatalf("completed non-routing action retained %d activity events", len(model.eventHistory))
	}
}

func TestModelLiveTimelineShowsMoreThanSixDetailedEvents(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.height = 60
	for index := 0; index < 10; index++ {
		model.eventHistory = append(model.eventHistory, controlplane.Event{Kind: "runtime.tool.completed", Message: fmt.Sprintf("shell · completed %d", index)})
	}
	timeline := model.renderLiveTimeline(100)
	if !strings.Contains(timeline, "completed 0") || !strings.Contains(timeline, "completed 9") {
		t.Fatalf("timeline did not retain a useful event window:\n%s", timeline)
	}
}

func TestModelShowsRoutingDecisionContext(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model.snapshot = controlplane.Snapshot{Models: []controlplane.ModelSnapshot{{Name: "gpt-test", ToolsKnown: true, Tools: []string{"browser", "code"}}}}
	updated, _ := model.Update(eventMsg{event: controlplane.Event{
		Version:         controlplane.SchemaVersion,
		ActionID:        "route",
		Kind:            "route.ask_accept",
		Message:         "gpt-test accepted",
		Model:           "gpt-test",
		Confidence:      0.91,
		ConfidenceKnown: true,
		Reasons:         []string{"capability fit", "within cost ceiling"},
	}, ok: true})
	model = updated.(*Model)
	view := model.View().Content
	for _, want := range []string{"route conf   91%", "capability", "known (2", "LIVE ROUTING", "winner"} {
		if !strings.Contains(view, want) {
			t.Fatalf("decision context missing %q:\n%s", want, view)
		}
	}
	rightWidth := 30
	mainWidth := model.width - rightWidth - 2
	right := ansi.Strip(model.renderInspectorPanel(rightWidth))
	if strings.Contains(right, "LIVE ROUTING") {
		t.Fatalf("inspector still contains routing activity:\n%s", right)
	}
	if !strings.Contains(ansi.Strip(model.renderShell()), "LIVE ROUTING") || mainWidth < 36 {
		t.Fatalf("routing workspace was not rendered below the wide panes")
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

func TestModelKeepsMultipleOutputLinesReadable(t *testing.T) {
	model := NewModel(controlplane.DefaultCatalog(), Options{Motion: false, NoColor: true})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model.activeAction = "run"
	model.output.WriteString("first line\nsecond line")
	view := model.View().Content
	if !strings.Contains(view, "first line") || !strings.Contains(view, "second line") {
		t.Fatalf("multi-line output was clipped to one line:\n%s", view)
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
		{"history", "execution"},
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
