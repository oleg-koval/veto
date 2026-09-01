package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/oleg-koval/veto/internal/controlplane"
)

const tickInterval = 80 * time.Millisecond

// Options controls presentation-only behavior. Runtime actions remain owned
// by the control-plane service and can be added without changing the shell.
type Options struct {
	Motion         bool
	NoColor        bool
	Mouse          bool
	Service        controlplane.Service
	ServiceFactory func() (controlplane.Service, error)
}

type tickMsg time.Time

type eventMsg struct {
	event controlplane.Event
	ok    bool
}

type executionResultMsg struct {
	result controlplane.ActionResult
	err    error
}

type snapshotMsg struct {
	snapshot controlplane.Snapshot
	err      error
}

type serviceReadyMsg struct {
	service controlplane.Service
	err     error
}

// Model is the keyboard-first Veto shell. It intentionally contains no
// provider clients or credential state.
type Model struct {
	catalog         controlplane.Catalog
	options         Options
	width           int
	height          int
	selected        int
	activeAction    string
	composerOpen    bool
	composerAction  string
	composerInput   string
	composerFields  []controlplane.FlagSpec
	composerValues  map[string]string
	composerField   int
	composerEditing bool
	confirmOpen     bool
	pendingRequest  controlplane.ActionRequest
	running         bool
	lastEvent       string
	eventHistory    []controlplane.Event
	output          strings.Builder
	events          <-chan controlplane.Event
	cancelRun       context.CancelFunc
	snapshot        controlplane.Snapshot
	paletteOpen     bool
	helpOpen        bool
	paletteQuery    string
	paletteCursor   int
	frame           uint8
	status          string
}

// NewModel creates a shell with a deterministic initial state.
func NewModel(catalog controlplane.Catalog, options Options) *Model {
	status := "Ready · choose a command"
	if options.ServiceFactory != nil {
		status = "Loading · control plane"
	}
	return &Model{catalog: catalog, options: options, status: status}
}

func (m *Model) Init() tea.Cmd {
	commands := make([]tea.Cmd, 0, 2)
	if m.options.Motion {
		commands = append(commands, nextTick())
	}
	if m.options.Service != nil {
		commands = append(commands, m.loadSnapshot())
	}
	if m.options.ServiceFactory != nil {
		commands = append(commands, m.loadService())
	}
	return tea.Batch(commands...)
}

func nextTick() tea.Cmd {
	return tea.Tick(tickInterval, func(now time.Time) tea.Msg { return tickMsg(now) })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch message := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		return m, nil
	case tickMsg:
		m.frame++
		if m.options.Motion {
			return m, nextTick()
		}
		return m, nil
	case snapshotMsg:
		if message.err == nil {
			m.snapshot = message.snapshot
		}
		return m, nil
	case serviceReadyMsg:
		m.options.ServiceFactory = nil
		if message.err != nil {
			m.status = "Error · control plane unavailable"
			return m, nil
		}
		m.options.Service = message.service
		m.status = "Ready · choose a command"
		return m, m.loadSnapshot()
	case tea.MouseClickMsg:
		m.updateMouse(message)
		return m, nil
	case tea.MouseWheelMsg:
		if message.Button == tea.MouseWheelDown {
			m.moveSelection(1)
		} else if message.Button == tea.MouseWheelUp {
			m.moveSelection(-1)
		}
		return m, nil
	case eventMsg:
		if !message.ok {
			return m, nil
		}
		if message.event.Version != 0 && message.event.Version != controlplane.SchemaVersion {
			m.status = fmt.Sprintf("Error · unsupported event schema %d", message.event.Version)
			return m, nil
		}
		m.eventHistory = append(m.eventHistory, message.event)
		if len(m.eventHistory) > 64 {
			m.eventHistory = m.eventHistory[len(m.eventHistory)-64:]
		}
		m.lastEvent = message.event.Kind + " · " + message.event.Message
		if message.event.Kind == "output" {
			m.output.WriteString(message.event.Message)
		}
		return m, waitForEvent(m.events)
	case executionResultMsg:
		m.running = false
		if m.cancelRun != nil {
			m.cancelRun()
		}
		m.cancelRun = nil
		if message.err != nil {
			m.status = "Error · " + message.err.Error()
			return m, nil
		}
		m.activeAction = message.result.ActionID
		m.status = "Ready · " + message.result.Summary
		if m.output.Len() == 0 {
			m.output.WriteString(message.result.Output)
		}
		if m.options.Service != nil {
			return m, m.loadSnapshot()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(message)
	default:
		return m, nil
	}
}

func (m *Model) updateMouse(message tea.MouseClickMsg) {
	if message.Button != tea.MouseLeft || m.composerOpen || m.confirmOpen || m.paletteOpen || m.helpOpen {
		return
	}
	commands := m.catalog.Commands()
	if len(commands) == 0 {
		return
	}
	width := m.width
	if width < 1 {
		width = 80
	}
	listWidth := 25
	listOffset := 0
	switch {
	case width < 58:
		listWidth = width
		listOffset = lipgloss.Height(m.renderMain(width)) + 2
	case width < 96:
		listWidth = width / 3
		if listWidth < 20 {
			listWidth = 20
		}
	}
	if message.X < 0 || message.X >= listWidth || message.Y < listOffset+1 {
		return
	}
	index := message.Y - listOffset - 1 // COMMANDS header occupies the first row
	if index < 0 || index >= len(commands) {
		return
	}
	m.selected = index
	m.status = "Ready · " + commands[index].Command
}

func (m *Model) updateKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.Key()
	if m.running && key.String() == "esc" {
		if m.cancelRun != nil {
			m.cancelRun()
		}
		m.status = "Cancelling · " + m.composerAction
		return m, nil
	}
	if m.confirmOpen {
		switch key.String() {
		case "esc", "n":
			m.confirmOpen = false
			m.pendingRequest = controlplane.ActionRequest{}
			m.status = "Ready · action cancelled"
		case "enter", "y":
			request := m.pendingRequest
			m.confirmOpen = false
			m.pendingRequest = controlplane.ActionRequest{}
			return m.beginExecution(request)
		}
		return m, nil
	}
	if m.helpOpen {
		if key.String() == "esc" || key.String() == "?" {
			m.helpOpen = false
		}
		return m, nil
	}
	if m.paletteOpen {
		return m.updatePalette(key)
	}
	if m.composerOpen {
		return m.updateComposer(key)
	}
	if key.Mod == tea.ModCtrl && key.Code == 'c' {
		if m.cancelRun != nil {
			m.cancelRun()
		}
		return m, tea.Quit
	}
	if key.Mod == tea.ModCtrl && key.Code == 'k' {
		m.paletteOpen = true
		m.paletteQuery = ""
		m.paletteCursor = 0
		return m, nil
	}

	switch key.String() {
	case "q":
		if m.cancelRun != nil {
			m.cancelRun()
		}
		return m, tea.Quit
	case "/":
		m.paletteOpen = true
		m.paletteQuery = ""
		m.paletteCursor = 0
		return m, nil
	case "?":
		m.helpOpen = true
		return m, nil
	case "h":
		m.activeAction = "history"
		m.status = "Ready · history"
	case "i":
		m.activeAction = "integrations"
		m.status = "Ready · integrations"
	case "p":
		m.activeAction = "plans"
		m.status = "Ready · plans"
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "tab":
		m.moveSelection(1)
	case "enter":
		commands := m.catalog.Commands()
		if len(commands) > 0 && m.actionSupportsForm(commands[m.selected]) {
			m.openComposer(commands[m.selected])
		} else {
			m.activateSelected()
			if len(commands) > 0 && m.options.Service != nil && directActions[commands[m.selected].ID] {
				return m.startAction(commands[m.selected].ID)
			}
		}
	case "r":
		commands := m.catalog.Commands()
		if len(commands) > 0 && m.actionHasForm(commands[m.selected]) {
			m.openComposer(commands[m.selected])
		}
	}
	return m, nil
}

func (m *Model) actionHasForm(action controlplane.ActionSpec) bool {
	return action.ID == "run" || action.ID == "route" || len(action.Flags) > 0 || len(action.Subcommands) > 0
}

func (m *Model) actionSupportsForm(action controlplane.ActionSpec) bool {
	if action.ID == "run" || action.ID == "route" {
		return true
	}
	// These commands are also navigation surfaces. Enter opens their screen;
	// r opens the typed flag/subcommand form when an operation is needed.
	switch action.ID {
	case "analytics", "hermes", "models", "opencode":
		return false
	default:
		return m.actionHasForm(action)
	}
}

var directActions = map[string]bool{
	"benchmark": true,
	"doctor":    true,
	"version":   true,
}

func (m *Model) updateComposer(key tea.Key) (tea.Model, tea.Cmd) {
	if m.composerEditing {
		return m.updateComposerField(key)
	}
	switch key.String() {
	case "esc":
		m.composerOpen = false
		m.composerInput = ""
	case "backspace":
		if len(m.composerInput) > 0 {
			m.composerInput = m.composerInput[:len(m.composerInput)-1]
		}
	case "enter":
		if m.composerNeedsObjective() && strings.TrimSpace(m.composerInput) == "" {
			m.status = "Error · enter a task objective"
			return m, nil
		}
		if len(m.composerFields) > 0 {
			m.composerEditing = true
			m.composerField = 0
			m.status = "Flags · " + m.composerFields[0].Name
			return m, nil
		}
		return m.startExecution()
	default:
		if key.Text != "" {
			m.composerInput += key.Text
		}
	}
	return m, nil
}

func (m *Model) openComposer(action controlplane.ActionSpec) {
	m.composerAction = action.ID
	m.composerInput = ""
	m.composerFields = make([]controlplane.FlagSpec, 0, len(action.Flags))
	m.composerValues = make(map[string]string)
	for _, subcommand := range action.Subcommands {
		subcommand = defaultSubcommand(action.ID, subcommand)
		m.composerFields = append(m.composerFields, controlplane.FlagSpec{Name: "subcommand", Value: "command", Default: subcommand, Description: "Command operation."})
		m.composerValues["subcommand"] = subcommand
		break
	}
	for _, field := range action.Flags {
		if field.Name == "task" {
			continue
		}
		m.composerFields = append(m.composerFields, field)
		m.composerValues[field.Name] = field.Default
	}
	m.composerField = 0
	m.composerEditing = false
	m.composerOpen = true
	if !m.composerNeedsObjective() && len(m.composerFields) > 0 {
		m.composerEditing = true
		m.status = "Flags · " + m.composerFields[0].Name
	}
}

func defaultSubcommand(actionID, fallback string) string {
	switch actionID {
	case "analytics", "opencode":
		return "status"
	case "hermes":
		return "api"
	default:
		return fallback
	}
}

func (m *Model) composerNeedsObjective() bool {
	return m.composerAction == "run" || m.composerAction == "route"
}

func (m *Model) updateComposerField(key tea.Key) (tea.Model, tea.Cmd) {
	if len(m.composerFields) == 0 {
		return m.startExecution()
	}
	field := m.composerFields[m.composerField]
	switch key.String() {
	case "esc":
		m.composerOpen = false
		m.composerEditing = false
	case "tab", "down":
		m.composerField = (m.composerField + 1) % len(m.composerFields)
		m.status = "Flags · " + m.composerFields[m.composerField].Name
	case "shift+tab", "up":
		m.composerField = (m.composerField - 1 + len(m.composerFields)) % len(m.composerFields)
		m.status = "Flags · " + m.composerFields[m.composerField].Name
	case "backspace":
		value := m.composerValues[field.Name]
		if len(value) > 0 {
			m.composerValues[field.Name] = value[:len(value)-1]
		}
	case "enter":
		if m.composerField < len(m.composerFields)-1 {
			m.composerField++
			m.status = "Flags · " + m.composerFields[m.composerField].Name
			return m, nil
		}
		return m.startExecution()
	default:
		if key.Text != "" {
			m.composerValues[field.Name] += key.Text
		}
	}
	return m, nil
}

func (m *Model) startExecution() (tea.Model, tea.Cmd) {
	objective := strings.TrimSpace(m.composerInput)
	for _, field := range m.composerFields {
		if field.Required && strings.TrimSpace(m.composerValues[field.Name]) == "" {
			m.status = "Error · " + field.Name + " is required"
			return m, nil
		}
	}
	m.composerOpen = false
	m.composerEditing = false
	arguments := make(map[string]string, len(m.composerValues)+1)
	arguments["objective"] = objective
	for name, value := range m.composerValues {
		if value != "" {
			arguments[name] = value
		}
	}
	request := controlplane.ActionRequest{ActionID: m.composerAction, Arguments: arguments}
	if requiresConfirmation(request) {
		m.pendingRequest = request
		m.confirmOpen = true
		m.status = "Confirm · " + request.ActionID
		return m, nil
	}
	return m.beginExecution(request)
}

func (m *Model) beginExecution(request controlplane.ActionRequest) (tea.Model, tea.Cmd) {
	m.running = true
	m.output.Reset()
	m.eventHistory = nil
	m.lastEvent = "starting"
	m.status = "Running · " + request.ActionID
	if m.options.Service == nil {
		m.activeAction = request.ActionID
		m.running = false
		m.status = "Ready · service unavailable in preview"
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel
	m.events = m.options.Service.Subscribe(ctx)
	return m, tea.Batch(m.execute(request, ctx), waitForEvent(m.events))
}

func requiresConfirmation(request controlplane.ActionRequest) bool {
	switch request.ActionID {
	case "login", "logout", "disable", "enable", "install-git-hook":
		return true
	case "setup":
		return request.Arguments["auto-approve"] == "true"
	case "opencode":
		subcommand := request.Arguments["subcommand"]
		return subcommand == "connect" || subcommand == "disconnect" || (subcommand == "plugin" && request.Arguments["operation"] != "status")
	case "hermes":
		return request.Arguments["subcommand"] == "plugin" && request.Arguments["operation"] != "status"
	case "analytics":
		return request.Arguments["subcommand"] == "enable" || request.Arguments["subcommand"] == "disable"
	default:
		return false
	}
}

func (m *Model) startAction(actionID string) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel
	m.events = m.options.Service.Subscribe(ctx)
	m.running = true
	m.lastEvent = "starting"
	m.status = "Running · " + actionID
	request := controlplane.ActionRequest{ActionID: actionID, Arguments: map[string]string{}}
	return m, tea.Batch(m.execute(request, ctx), waitForEvent(m.events))
}

func (m *Model) execute(request controlplane.ActionRequest, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		result, err := m.options.Service.Execute(ctx, request)
		return executionResultMsg{result: result, err: err}
	}
}

func (m *Model) loadSnapshot() tea.Cmd {
	return func() tea.Msg {
		snapshot, err := m.options.Service.Snapshot(context.Background())
		return snapshotMsg{snapshot: snapshot, err: err}
	}
}

func (m *Model) loadService() tea.Cmd {
	return func() tea.Msg {
		service, err := m.options.ServiceFactory()
		return serviceReadyMsg{service: service, err: err}
	}
}

func waitForEvent(updates <-chan controlplane.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-updates
		return eventMsg{event: event, ok: ok}
	}
}

func (m *Model) updatePalette(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.paletteOpen = false
		m.paletteQuery = ""
	case "enter":
		filtered := m.filteredActions()
		if len(filtered) > 0 {
			m.activeAction = filtered[m.paletteCursor%len(filtered)].ID
			m.status = "Ready · " + filtered[m.paletteCursor%len(filtered)].Command
		}
		m.paletteOpen = false
		m.paletteQuery = ""
	case "backspace":
		if len(m.paletteQuery) > 0 {
			m.paletteQuery = m.paletteQuery[:len(m.paletteQuery)-1]
			m.paletteCursor = 0
		}
	case "j", "down":
		m.movePaletteCursor(1)
	case "k", "up":
		m.movePaletteCursor(-1)
	default:
		if key.Text != "" {
			m.paletteQuery += key.Text
			m.paletteCursor = 0
		}
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	commands := m.catalog.Commands()
	if len(commands) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(commands)) % len(commands)
	m.status = "Ready · " + commands[m.selected].Command
}

func (m *Model) activateSelected() {
	commands := m.catalog.Commands()
	if len(commands) == 0 {
		return
	}
	m.activeAction = commands[m.selected].ID
	m.status = "Ready · veto " + commands[m.selected].Command
}

func (m *Model) movePaletteCursor(delta int) {
	filtered := m.filteredActions()
	if len(filtered) == 0 {
		return
	}
	m.paletteCursor = (m.paletteCursor + delta + len(filtered)) % len(filtered)
}

func (m *Model) filteredActions() []controlplane.ActionSpec {
	query := strings.ToLower(strings.TrimSpace(m.paletteQuery))
	if query == "" {
		return m.catalog.Commands()
	}
	filtered := make([]controlplane.ActionSpec, 0)
	for _, action := range m.catalog.Commands() {
		if strings.Contains(strings.ToLower(action.ID), query) || strings.Contains(strings.ToLower(action.Description), query) {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

func (m *Model) View() tea.View {
	content := m.renderShell()
	if m.paletteOpen {
		content = m.renderPalette(content)
	}
	if m.helpOpen {
		content = m.renderHelp(content)
	}
	if m.options.NoColor {
		content = ansi.Strip(content)
	}
	view := tea.NewView(content)
	view.AltScreen = true
	if m.options.Mouse {
		view.MouseMode = tea.MouseModeCellMotion
	}
	view.ReportFocus = true
	view.WindowTitle = "Veto"
	return view
}

func (m *Model) renderShell() string {
	width := m.width
	if width < 1 {
		width = 80
	}
	commands := m.catalog.Commands()
	var body string
	switch {
	case width < 58:
		body = m.renderNarrow(commands, width)
	case width < 96:
		body = m.renderMedium(commands, width)
	default:
		body = m.renderWide(commands, width)
	}
	body = lipgloss.NewStyle().Width(width).Render(body)
	status := m.statusLine(width)
	height := m.height
	if height < 1 {
		height = 24
	}
	bodyLines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	statusHeight := lipgloss.Height(status)
	if len(bodyLines) > height-statusHeight {
		limit := max(1, height-statusHeight-1)
		bodyLines = append(bodyLines[:limit], mutedStyle.Render("… more below; resize or use the palette"))
	}
	return strings.Join(bodyLines, "\n") + "\n" + status
}

func (m *Model) renderWide(commands []controlplane.ActionSpec, width int) string {
	leftWidth := 25
	rightWidth := 27
	mainWidth := width - leftWidth - rightWidth - 4
	if mainWidth < 24 {
		return m.renderMedium(commands, width)
	}
	left := m.renderCommandList(commands, leftWidth)
	main := m.renderMain(mainWidth)
	right := m.renderInspector(rightWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", main, "  ", right)
}

func (m *Model) renderMedium(commands []controlplane.ActionSpec, width int) string {
	listWidth := width / 3
	if listWidth < 20 {
		listWidth = 20
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, m.renderCommandList(commands, listWidth), "  ", m.renderMain(width-listWidth-2))
}

func (m *Model) renderNarrow(commands []controlplane.ActionSpec, width int) string {
	return m.renderMain(width) + "\n\n" + m.renderCommandList(commands, width)
}

func (m *Model) renderCommandList(commands []controlplane.ActionSpec, width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("COMMANDS"))
	b.WriteByte('\n')
	for index, action := range commands {
		marker := "  "
		if index == m.selected {
			marker = "▸ "
		}
		line := truncate(marker+action.Label, width)
		if index == m.selected {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if index >= 8 && width < 30 {
			b.WriteString(mutedStyle.Render("  + more in palette"))
			break
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.TrimSuffix(b.String(), "\n"))
}

func (m *Model) renderMain(width int) string {
	active := "Home"
	if m.activeAction != "" {
		if action, ok := m.catalog.Find(m.activeAction); ok {
			active = action.Label
		}
	}
	var b strings.Builder
	b.WriteString(brandStyle.Render("VETO"))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("control plane"))
	b.WriteString("\n\n")
	b.WriteString(titleStyle.Render(active))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Every CLI command is available through the palette."))
	b.WriteString("\n\n")
	if m.composerOpen {
		b.WriteString(m.renderComposer(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.confirmOpen {
		b.WriteString(m.renderConfirmation(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if len(m.eventHistory) > 0 {
		b.WriteString(m.renderLiveTimeline(width))
		b.WriteString("\n\n")
	}
	if m.activeAction == "models" {
		b.WriteString(m.renderModels(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.activeAction == "providers" {
		b.WriteString(m.renderProviders(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.activeAction == "history" {
		b.WriteString(m.renderHistory(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.activeAction == "exec" || m.activeAction == "plans" {
		b.WriteString(m.renderPlans(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.activeAction == "doctor" {
		b.WriteString(m.renderHealth(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.activeAction == "analytics" {
		b.WriteString(m.renderAnalytics(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.activeAction == "integrations" || m.activeAction == "opencode" || m.activeAction == "hermes" {
		b.WriteString(m.renderIntegrations(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	b.WriteString(panelStyle.Render(truncate("⌘  Run a task   /  Find command   ?  Help", width-4)))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render("NEXT"))
	b.WriteString("\n")
	b.WriteString("Start with a command or open the palette to inspect flags.\n")
	b.WriteString(mutedStyle.Render("No provider calls are made until you confirm an action."))
	if m.output.Len() > 0 {
		b.WriteString("\n\n")
		b.WriteString(headerStyle.Render("OUTPUT"))
		b.WriteString("\n")
		b.WriteString(panelStyle.Render(truncate(strings.TrimSpace(m.output.String()), width-4)))
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) displayComposerValue(field controlplane.FlagSpec) string {
	value := m.composerValues[field.Name]
	if field.Secret && value != "" {
		return strings.Repeat("•", len([]rune(value)))
	}
	return value
}

func (m *Model) renderComposer(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("COMPOSER · " + m.composerAction))
	b.WriteString("\n")
	if m.composerNeedsObjective() {
		b.WriteString(panelStyle.Render("objective: " + m.composerInput + "▌"))
		b.WriteString("\n")
	}
	if m.composerEditing && len(m.composerFields) > 0 {
		field := m.composerFields[m.composerField]
		b.WriteString(panelStyle.Render(field.Name + ": " + m.displayComposerValue(field) + "▌"))
		b.WriteString("\n")
		if field.Description != "" {
			b.WriteString(mutedStyle.Render(field.Description))
			b.WriteString("\n")
		}
		b.WriteString(mutedStyle.Render("Enter next field/run · Tab move · Esc cancel"))
	} else if m.composerNeedsObjective() {
		b.WriteString(mutedStyle.Render("Enter edit flags · Esc cancel"))
	} else {
		b.WriteString(mutedStyle.Render("Enter run · Esc cancel"))
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) renderConfirmation(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("CONFIRM ACTION"))
	b.WriteString("\n")
	b.WriteString(panelStyle.Render("Run veto " + m.pendingRequest.ActionID + "?"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Enter/y confirm · n/Esc cancel"))
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) renderModels(width int) string {
	if len(m.snapshot.Models) == 0 {
		return mutedStyle.Render("No configured models. Run veto login or open Providers.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("MODEL CATALOG"))
	b.WriteString("\n")
	for _, model := range m.snapshot.Models {
		contextTokens := "unknown"
		if model.ContextTokens > 0 {
			contextTokens = strconv.Itoa(model.ContextTokens)
		}
		tools := "unknown"
		if model.ToolsKnown {
			tools = strings.Join(model.Tools, ",")
			if tools == "" {
				tools = "none"
			}
		}
		policy := "normal"
		if model.Excluded {
			policy = "excluded"
		} else if model.Pinned {
			policy = "pinned"
		} else if model.Favorite {
			policy = "favorite"
		}
		line := fmt.Sprintf("%-22s %-10s %-10s %s", model.Name, model.Provider, model.Runtime, model.Tier)
		b.WriteString(truncate(line, width-2))
		b.WriteByte('\n')
		details := fmt.Sprintf("  source=%s  status=%s  policy=%s  ctx=%s  tools=%s  cost=%s/%s", model.Source, model.Status, policy, contextTokens, tools, modelCost(model.CostPer1kInputUSD, model.CostPer1kInputKnown), modelCost(model.CostPer1kOutputUSD, model.CostPer1kOutputKnown))
		b.WriteString(mutedStyle.Render(truncate(details, width-2)))
		b.WriteByte('\n')
	}
	return b.String()
}

func modelCost(value float64, known bool) string {
	if !known {
		return "unknown"
	}
	return fmt.Sprintf("$%.4f", value)
}

func (m *Model) renderProviders(width int) string {
	if len(m.snapshot.Providers) == 0 {
		return mutedStyle.Render("No providers configured. Run veto login to connect one.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("PROVIDER HEALTH"))
	b.WriteString("\n")
	for _, provider := range m.snapshot.Providers {
		state := "ready"
		if !provider.Configured {
			state = "not configured"
		}
		b.WriteString(fmt.Sprintf("%-18s %-16s %d model(s)\n", provider.Name, state, provider.ModelCount))
	}
	return b.String()
}

func (m *Model) renderHistory(width int) string {
	if len(m.snapshot.History) == 0 {
		return mutedStyle.Render("No redacted activity yet. Completed routes and runs appear here.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("RECENT ACTIVITY"))
	b.WriteString("\n")
	for _, item := range m.snapshot.History {
		stamp := item.Timestamp.Local().Format("15:04:05")
		b.WriteString(truncate(fmt.Sprintf("%s  %-24s %-18s %-10s %s", stamp, item.Type, item.Model, item.Status, item.Runtime), width-2))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *Model) renderPlans(width int) string {
	if len(m.snapshot.Plans) == 0 {
		return mutedStyle.Render("No plans found. Create a plan under ~/.veto/plans to execute it here.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("PLANS"))
	b.WriteString("\n")
	for _, plan := range m.snapshot.Plans {
		b.WriteString(truncate("▸ "+plan.Name, width-2))
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Select a plan, then execution confirmation will appear here."))
	return b.String()
}

func (m *Model) renderHealth(width int) string {
	if len(m.snapshot.Health) == 0 {
		return mutedStyle.Render("No health findings. Press Enter on Doctor to refresh diagnostics.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("HEALTH · SAFE DIAGNOSTICS"))
	b.WriteString("\n")
	for _, check := range m.snapshot.Health {
		b.WriteString(truncate(fmt.Sprintf("%-6s %-24s %s", check.Status, check.ID, check.Message), width-2))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *Model) renderAnalytics(width int) string {
	analytics := m.snapshot.Analytics
	lines := []string{
		headerStyle.Render("ANALYTICS & DATA"),
		fmt.Sprintf("Local collection: %t", analytics.LocalCollection),
		fmt.Sprintf("Local path: %s", analytics.LocalPath),
		fmt.Sprintf("Retention: %d days", analytics.RetentionDays),
		fmt.Sprintf("Future remote sharing: %s", analytics.RemoteSharing),
		fmt.Sprintf("Remote transport active: %t", analytics.RemoteTransportActive),
		mutedStyle.Render("Remote analytics are not active; preference changes remain explicit."),
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderIntegrations(width int) string {
	if len(m.snapshot.Integrations) == 0 {
		return mutedStyle.Render("No integrations detected. Use the OpenCode or Hermes command for setup.")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("INTEGRATIONS"))
	b.WriteString("\n")
	for _, integration := range m.snapshot.Integrations {
		b.WriteString(truncate(fmt.Sprintf("%-12s %-18s %s", integration.Name, integration.Status, integration.Detail), width-2))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *Model) renderInspector(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("STATUS"))
	b.WriteString("\n")
	state := "ready"
	if m.running {
		state = "running"
	}
	b.WriteString("● " + state + "\n")
	monitor := m.snapshot.Monitor
	latency := "unknown"
	if monitor.LatencyKnown {
		latency = fmt.Sprintf("%dms", monitor.LatencyMs)
	}
	cost := "unknown"
	if monitor.CostKnown {
		cost = fmt.Sprintf("$%.4f", monitor.CostUSD)
	}
	tokens := "unknown"
	if monitor.TokensKnown {
		tokens = strconv.Itoa(monitor.TotalTokens)
	}
	b.WriteString(mutedStyle.Render(fmt.Sprintf("providers  %d\nmodel      %s\nsessions   %d\ntools      %d\napprovals  %d\ntokens     %s\ncost       %s\nlatency    %s\nartifacts  %d", len(m.snapshot.Providers), valueOrDash(m.snapshot.Model), monitor.ActiveSessions, monitor.ActiveTools, monitor.PendingApprovals, tokens, cost, latency, monitor.Artifacts)))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render("GUIDANCE"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Tab moves focus\nEnter selects\nEsc closes overlays"))
	if len(m.eventHistory) > 0 {
		b.WriteString("\n\n")
		b.WriteString(m.renderLiveTimeline(width))
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func (m *Model) renderLiveTimeline(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("LIVE ROUTING"))
	b.WriteString("\n")
	start := max(0, len(m.eventHistory)-6)
	for _, event := range m.eventHistory[start:] {
		line := fmt.Sprintf("• %-22s %s", eventStage(event.Kind), event.Message)
		b.WriteString(truncate(line, width))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func eventStage(kind string) string {
	kind = strings.TrimPrefix(kind, "route.")
	kind = strings.TrimPrefix(kind, "execution.")
	kind = strings.TrimPrefix(kind, "runtime.")
	switch kind {
	case "filter_pass", "filtering":
		return "filtering"
	case "filter_fail":
		return "filtered"
	case "ask_start":
		return "admission"
	case "ask_accept":
		return "winner"
	case "ask_reject":
		return "rejected"
	case "ask_error":
		return "failure"
	case "output":
		return "execution output"
	case "review.started":
		return "review"
	case "review.completed":
		return "reviewed"
	case "review.error":
		return "review failure"
	}
	return kind
}

func (m *Model) statusLine(width int) string {
	pulse := "·"
	if m.options.Motion && m.frame%2 == 1 {
		pulse = "•"
	}
	status := fmt.Sprintf(" %s  %s", pulse, m.status)
	hints := "Ctrl+K palette  ·  Tab move  ·  ? help  ·  q quit "
	if width < 64 {
		hints = "Ctrl+K palette · ? help · q quit "
	}
	return statusStyle.Width(width).Render(status + strings.Repeat(" ", max(1, width-lipgloss.Width(status)-lipgloss.Width(hints))) + hints)
}

func (m *Model) renderPalette(background string) string {
	filtered := m.filteredActions()
	var b strings.Builder
	b.WriteString(titleStyle.Render("Command palette"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Type to filter · Enter run · Esc close"))
	b.WriteString("\n\n")
	b.WriteString(panelStyle.Render("/ " + m.paletteQuery))
	b.WriteString("\n\n")
	for index, action := range filtered {
		marker := "  "
		if index == m.paletteCursor {
			marker = "▸ "
		}
		b.WriteString(marker + action.Command + "  " + mutedStyle.Render(action.Description) + "\n")
		if index >= 8 {
			break
		}
	}
	if len(filtered) == 0 {
		b.WriteString(mutedStyle.Render("No matching commands. Try a shorter search."))
	}
	overlay := overlayStyle.Width(max(30, min(m.width-6, 100))).Render(strings.TrimSuffix(b.String(), "\n"))
	return background + "\n\n" + overlay
}

func (m *Model) renderHelp(background string) string {
	help := overlayStyle.Width(max(30, min(m.width-6, 72))).Render(strings.Join([]string{
		titleStyle.Render("Keyboard help"),
		"",
		"j / ↓       move selection",
		"k / ↑       move selection backwards",
		"Tab         move focus",
		"Ctrl+K / /   open command palette",
		"r           compose a route or run task",
		"Tab         edit the command's CLI-compatible flags",
		"h           open redacted history",
		"i           inspect integrations",
		"p           inspect plans",
		"Enter       select the focused command",
		"Esc         close an overlay",
		"q / Ctrl+C  quit cleanly",
	}, "\n"))
	return background + "\n\n" + help
}

func truncate(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return value[:1]
	}
	return value[:width-1] + "…"
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

var (
	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7EE787"))
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F2CC60"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#79C0FF"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B949E"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#30363D"))
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#30363D")).Padding(0, 1)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9")).Background(lipgloss.Color("#161B22"))
	overlayStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#7EE787")).Background(lipgloss.Color("#0D1117")).Padding(1, 2)
)
