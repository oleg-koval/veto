package tui

import (
	"fmt"
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
	Motion  bool
	NoColor bool
	Mouse   bool
}

type tickMsg time.Time

// Model is the keyboard-first Veto shell. It intentionally contains no
// provider clients or credential state.
type Model struct {
	catalog       controlplane.Catalog
	options       Options
	width         int
	height        int
	selected      int
	activeAction  string
	paletteOpen   bool
	helpOpen      bool
	paletteQuery  string
	paletteCursor int
	frame         uint8
	status        string
}

// NewModel creates a shell with a deterministic initial state.
func NewModel(catalog controlplane.Catalog, options Options) *Model {
	return &Model{catalog: catalog, options: options, status: "Ready · choose a command"}
}

func (m *Model) Init() tea.Cmd {
	if !m.options.Motion {
		return nil
	}
	return nextTick()
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
	case tea.KeyPressMsg:
		return m.updateKey(message)
	default:
		return m, nil
	}
}

func (m *Model) updateKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.Key()
	if m.helpOpen {
		if key.String() == "esc" || key.String() == "?" {
			m.helpOpen = false
		}
		return m, nil
	}
	if m.paletteOpen {
		return m.updatePalette(key)
	}
	if key.Mod == tea.ModCtrl && key.Code == 'c' {
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
		return m, tea.Quit
	case "/":
		m.paletteOpen = true
		m.paletteQuery = ""
		m.paletteCursor = 0
		return m, nil
	case "?":
		m.helpOpen = true
		return m, nil
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "tab":
		m.moveSelection(1)
	case "enter":
		m.activateSelected()
	}
	return m, nil
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
	return lipgloss.NewStyle().Width(width).Render(body) + "\n" + m.statusLine(width)
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
	b.WriteString(panelStyle.Render(truncate("⌘  Run a task   /  Find command   ?  Help", width-4)))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render("NEXT"))
	b.WriteString("\n")
	b.WriteString("Start with a command or open the palette to inspect flags.\n")
	b.WriteString(mutedStyle.Render("No provider calls are made until you confirm an action."))
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) renderInspector(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("STATUS"))
	b.WriteString("\n")
	b.WriteString("● ready\n")
	b.WriteString(mutedStyle.Render("providers  —\nmodel      —\nlatency    —"))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render("GUIDANCE"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Tab moves focus\nEnter selects\nEsc closes overlays"))
	return lipgloss.NewStyle().Width(width).Render(b.String())
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
