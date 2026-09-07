package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/router"
)

const tickInterval = 80 * time.Millisecond

const runningSpinner = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

var routingStages = []string{"FILTER", "SHORTLIST", "ADMIT", "WINNER", "EXECUTE", "REVIEW"}

// vetoWordmark uses terminal-native half blocks so the mark and letters share
// one visual weight. Each row is exactly three terminal cells high.
const vetoWordmark = `█   █  █▀▀▀  ▀▀█▀▀  ▄▀▀▄
▀▄ ▄▀  █▀▀     █    █  █
  ▀    ▀▀▀▀    ▀    ▀▄▄▀`

type markRow struct {
	left  string
	check string
	right string
}

var vetoMarkRows = []markRow{
	{left: "▛▀ ", check: "       ▄█", right: " ▀▜"},
	{left: "▌  ", check: "▄    ▄█▀ ", right: "  ▐"},
	{left: "▙▄ ", check: " ▀█▄█▀   ", right: " ▄▟"},
}

// Options controls presentation-only behavior. Runtime actions remain owned
// by the control-plane service and can be added without changing the shell.
type Options struct {
	Motion         bool
	NoColor        bool
	Mouse          bool
	ScreenReader   bool
	Version        string
	Executable     string
	Service        controlplane.Service
	ServiceFactory func() (controlplane.Service, error)
}

type tickMsg time.Time

type eventMsg struct {
	event controlplane.Event
	ok    bool
}

type executionResultMsg struct {
	result  controlplane.ActionResult
	err     error
	request controlplane.ActionRequest
}

type nativeFinishedMsg struct{ err error }

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
	catalog             controlplane.Catalog
	options             Options
	width               int
	height              int
	selected            int
	hoveredCommand      int
	activeAction        string
	plansCursor         int
	integrationCursor   int
	composerOpen        bool
	composerAction      string
	composerInput       string
	composerFields      []controlplane.FlagSpec
	composerValues      map[string]string
	composerField       int
	composerEditing     bool
	confirmOpen         bool
	pendingRequest      controlplane.ActionRequest
	pendingNative       controlplane.NativeCommand
	running             bool
	lastEvent           string
	decisionModel       string
	decisionWhy         string
	decisionConf        string
	eventHistory        []controlplane.Event
	output              strings.Builder
	outputAction        string
	pendingHealthFix    string
	activeHealthFix     string
	healthVerification  string
	verifyHealthFix     bool
	healthLoading       bool
	events              <-chan controlplane.Event
	cancelRun           context.CancelFunc
	snapshot            controlplane.Snapshot
	paletteOpen         bool
	helpOpen            bool
	dataFilterOpen      bool
	dataFilterQuery     string
	dataCursor          int
	dataDetailOpen      bool
	historyDetailCursor int
	mouseClickPending   bool
	mouseClickX         int
	mouseClickY         int
	missionSequence     uint64
	returnToProviders   bool
	paletteQuery        string
	paletteCursor       int
	frame               uint8
	status              string
}

// NewModel creates a shell with a deterministic initial state.
func NewModel(catalog controlplane.Catalog, options Options) *Model {
	status := "Ready · compose a mission"
	if options.ServiceFactory != nil {
		status = "Loading · control plane"
	}
	return &Model{catalog: catalog, options: options, status: status, hoveredCommand: -1}
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
			m.restoreDecisionFromSnapshot()
			m.healthLoading = false
			m.setDataCursor(m.currentDataCursor())
			m.finishHealthVerification()
		} else if m.verifyHealthFix && m.activeHealthFix != "" {
			m.verifyHealthFix = false
			m.healthVerification = fmt.Sprintf("%s · VERIFICATION ERROR · %s", m.activeHealthFix, message.err)
			m.status = "Verification failed · " + m.activeHealthFix
		}
		return m, nil
	case serviceReadyMsg:
		m.options.ServiceFactory = nil
		if message.err != nil {
			m.status = "Error · control plane unavailable"
			return m, nil
		}
		m.options.Service = message.service
		m.status = "Ready · compose a mission"
		return m, m.loadSnapshot()
	case tea.MouseClickMsg:
		if message.Button != tea.MouseLeft {
			return m, nil
		}
		m.mouseClickPending = true
		m.mouseClickX, m.mouseClickY = message.X, message.Y
		return m, m.updateMouse(message)
	case tea.MouseReleaseMsg:
		// Some terminal emulators report a cell click as a release when
		// cell-motion tracking is enabled. Treat a left-button release as the
		// same activation so modal rows remain clickable across terminals.
		if m.mouseClickPending && message.X == m.mouseClickX && message.Y == m.mouseClickY {
			m.mouseClickPending = false
			return m, nil
		}
		m.mouseClickPending = false
		if message.Button == tea.MouseLeft {
			return m, m.updateMouseAt(message.X, message.Y)
		}
		return m, nil
	case tea.MouseMotionMsg:
		m.updateMouseHover(message)
		return m, nil
	case tea.MouseWheelMsg:
		if m.supportsDataFilter() {
			if message.Button == tea.MouseWheelDown {
				m.moveDataCursor(1)
			} else if message.Button == tea.MouseWheelUp {
				m.moveDataCursor(-1)
			}
		} else if message.Button == tea.MouseWheelDown {
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
		message.event.Message = ansi.Strip(message.event.Message)
		message.event.Model = ansi.Strip(message.event.Model)
		for index := range message.event.Reasons {
			message.event.Reasons[index] = ansi.Strip(message.event.Reasons[index])
		}
		if m.activeAction == "" && message.event.ActionID != "" {
			m.activeAction = message.event.ActionID
		}
		m.eventHistory = append(m.eventHistory, message.event)
		if len(m.eventHistory) > 64 {
			m.eventHistory = m.eventHistory[len(m.eventHistory)-64:]
		}
		m.lastEvent = message.event.Kind + " · " + message.event.Message
		if message.event.Model != "" && isDecisionModelEvent(message.event.Kind) {
			m.decisionModel = message.event.Model
		}
		if message.event.ConfidenceKnown && isDecisionModelEvent(message.event.Kind) {
			m.decisionConf = fmt.Sprintf("%.0f%%", message.event.Confidence*100)
		}
		if len(message.event.Reasons) > 0 && isDecisionReasonEvent(message.event.Kind) {
			m.decisionWhy = explainReasons(message.event.Reasons)
		}
		if message.event.Kind == "route.ask_start" && message.event.Model != "" {
			m.decisionWhy = "Evaluating " + message.event.Model + " against capability, cost, context, and policy."
		}
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
		if message.result.Command != nil && message.err == nil {
			m.pendingNative = message.result.Command
			m.pendingRequest = message.request
			m.confirmOpen = true
			m.output.WriteString(message.result.Summary)
			m.status = "Review · press Enter to launch native agent"
			return m, nil
		}
		if m.healthLoading {
			if message.err != nil {
				m.healthLoading = false
				m.status = "Error · diagnostics failed: " + message.err.Error()
				return m, nil
			}
			m.status = "Loading · fresh health results"
			return m, m.loadSnapshot()
		}
		if !isRoutingAction(message.result.ActionID) {
			m.eventHistory = nil
		}
		returnToProviders := m.returnToProviders
		verifyHealth := m.activeHealthFix != ""
		m.returnToProviders = false
		if message.err != nil {
			m.healthLoading = false
			if message.result.Output != "" && m.output.Len() == 0 {
				m.output.WriteString(ansi.Strip(message.result.Output))
			}
			if errors.Is(message.err, context.Canceled) {
				m.status = "Ready · action cancelled"
				if returnToProviders {
					m.activeAction = "providers"
				}
				return m, nil
			}
			if m.output.Len() == 0 {
				m.output.WriteString(ansi.Strip(message.err.Error()))
			}
			if errors.Is(message.err, router.ErrNoCandidate) {
				m.decisionModel = ""
				m.decisionWhy = "No candidate accepted the task. Review Live Routing for each rejection reason."
			}
			m.status = "Error · " + ansi.Strip(message.err.Error())
			if returnToProviders {
				m.activeAction = "providers"
			}
			if verifyHealth && m.options.Service != nil {
				m.verifyHealthFix = true
				m.status = "Verifying · " + m.activeHealthFix
				return m, m.loadSnapshot()
			}
			return m, nil
		}
		m.activeAction = message.result.ActionID
		if returnToProviders {
			m.activeAction = "providers"
		}
		m.status = "Ready · " + message.result.Summary
		if m.output.Len() == 0 {
			m.output.WriteString(ansi.Strip(message.result.Output))
		}
		if verifyHealth {
			m.verifyHealthFix = true
			m.status = "Verifying · " + m.activeHealthFix
		}
		if m.options.Service != nil {
			return m, m.loadSnapshot()
		}
		return m, nil
	case nativeFinishedMsg:
		m.running = false
		m.pendingNative = nil
		if message.err != nil {
			m.status = "Native agent exited with error · " + ansi.Strip(message.err.Error())
		} else {
			m.status = "Native agent returned · dispatch outcome is not task correctness"
		}
		return m, m.loadSnapshot()
	case tea.KeyPressMsg:
		return m.updateKey(message)
	default:
		return m, nil
	}
}

func (m *Model) updateMouse(message tea.MouseClickMsg) tea.Cmd {
	return m.updateMouseAt(message.X, message.Y)
}

func (m *Model) updateMouseAt(x, y int) tea.Cmd {
	if m.dataDetailOpen {
		if m.dataDetailFixButtonAt(x, y) {
			m.openSelectedAIFix()
		} else if m.activeAction == "history" {
			m.activateHistoryDetailAt(x, y)
		} else if m.activeAction == "providers" {
			m.activateProviderDetailAt(x, y)
		}
		return nil
	}
	if m.composerOpen || m.confirmOpen || m.paletteOpen || m.helpOpen {
		return nil
	}
	if index := m.primaryTabIndexAt(x, y); index >= 0 {
		return m.openPrimaryView(index)
	}
	if index, ok := m.dataRowIndexAt(x, y); ok {
		m.setDataCursor(index)
		if m.aiFixActionAt(x, y) && m.selectedAIFixAvailable() {
			m.openSelectedAIFix()
			return nil
		}
		m.status = "Selected · " + m.selectedDataLabel()
		m.openSelectedDataDetail()
	}
	return nil
}

func (m *Model) aiFixActionAt(x, y int) bool {
	if (m.activeAction != "doctor" && m.activeAction != "history") || x < 0 || y < 0 {
		return false
	}
	lines := strings.Split(ansi.Strip(m.renderShell()), "\n")
	if y >= len(lines) {
		return false
	}
	start := strings.Index(lines[y], "[F]")
	return start >= 0 && x >= start && x < start+len("[F] Diagnose with AI")
}

func (m *Model) primaryTabIndexAt(x, y int) int {
	width := m.width
	if width < 1 {
		width = 80
	}
	mainWidth := width
	if width >= 96 {
		rightWidth := 27
		if m.isHome() || m.isMissionComposer() {
			rightWidth = 30
		}
		mainWidth = width - rightWidth - 2
	}
	if mainWidth < 76 || y != lipgloss.Height(m.renderAppHeader(mainWidth))-1 {
		return -1
	}
	cursor := 0
	for index, tab := range primaryTabLabels {
		segmentWidth := lipgloss.Width(" " + primaryTabText(tab, mainWidth) + " ")
		if x >= cursor && x < cursor+segmentWidth {
			return index
		}
		cursor += segmentWidth + 1
	}
	return -1
}

func (m *Model) updateMouseHover(message tea.MouseMotionMsg) {
	if m.composerOpen || m.confirmOpen || m.paletteOpen || m.helpOpen || m.dataDetailOpen {
		m.hoveredCommand = -1
		return
	}
	m.hoveredCommand = -1
	if index := m.primaryTabIndexAt(message.X, message.Y); index >= 0 {
		m.status = "Hint · switch to " + strings.ToLower(primaryViews[index].tab)
	}
}

func (m *Model) commandIndexAt(x, y int) int {
	commands := m.catalog.Commands()
	if len(commands) == 0 || x < 0 || y < 0 || m.isHome() {
		return -1
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
	if x >= listWidth || y < listOffset+1 {
		return -1
	}
	index := y - listOffset - 1 // COMMANDS header occupies the first row
	if m.hoveredCommand >= 0 && index > m.hoveredCommand {
		// The hovered command owns one extra tooltip row in the rail.
		index--
	}
	if index < 0 || index >= len(commands) {
		return -1
	}
	return index
}

func (m *Model) dataRowIndexAt(x, y int) (int, bool) {
	if !m.supportsDataFilter() || x < 1 || y < 0 {
		return 0, false
	}
	width := m.width
	if width < 1 {
		width = 80
	}
	mainWidth := width
	if width >= 96 {
		mainWidth = width - 32
	}
	if x >= mainWidth-1 {
		return 0, false
	}
	rowY := lipgloss.Height(m.renderAppHeader(mainWidth)) + 10
	if m.activeAction == "providers" {
		rowY++
	}
	if m.activeAction == "models" {
		rows := m.filteredFleetDataRows()
		start, end := m.dataPageRange(len(rows))
		modelIndexes := make([]int, 0, end-start)
		harnessIndexes := make([]int, 0, end-start)
		for index := start; index < end; index++ {
			if rows[index].harness {
				harnessIndexes = append(harnessIndexes, index)
			} else {
				modelIndexes = append(modelIndexes, index)
			}
		}
		if offset := y - rowY; offset >= 0 && offset < len(modelIndexes) {
			return modelIndexes[offset], true
		}
		harnessY := rowY + len(modelIndexes) + 5
		if offset := y - harnessY; offset >= 0 && offset < len(harnessIndexes) {
			return harnessIndexes[offset], true
		}
		return 0, false
	}
	start, end := m.dataPageRange(m.dataRowCount())
	if offset := y - rowY; offset >= 0 && offset < end-start {
		return start + offset, true
	}
	return 0, false
}

func (m *Model) updateKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.Key()
	// Ctrl+C is the emergency escape hatch in every screen, including forms
	// and overlays where ordinary character keys are intentionally captured.
	if key.Mod == tea.ModCtrl && key.Code == 'c' {
		if m.cancelRun != nil {
			m.cancelRun()
		}
		return m, tea.Quit
	}
	if m.running && key.String() == "esc" {
		if m.cancelRun != nil {
			m.cancelRun()
		}
		action := m.composerAction
		if action == "" {
			action = m.activeAction
		}
		m.status = "Cancelling · " + action
		return m, nil
	}
	if m.running && key.String() != "q" {
		m.status = "Busy · " + m.activeAction + " is still running"
		return m, nil
	}
	if m.confirmOpen {
		switch key.String() {
		case "esc", "n":
			m.pendingNative = nil
			m.confirmOpen = false
			m.pendingRequest = controlplane.ActionRequest{}
			m.returnToProviders = false
			m.status = "Ready · action cancelled"
		case "enter", "y":
			if m.running {
				m.status = "Busy · " + m.activeAction + " is still running"
				return m, nil
			}
			if m.pendingNative != nil {
				command := m.pendingNative
				m.pendingNative = nil
				m.confirmOpen = false
				m.pendingRequest = controlplane.ActionRequest{}
				m.running = true
				m.status = "Running · native agent"
				return m, tea.Exec(command, func(err error) tea.Msg { return nativeFinishedMsg{err: err} })
			}
			request := m.pendingRequest
			m.confirmOpen = false
			m.pendingRequest = controlplane.ActionRequest{}
			return m.beginExecution(request)
		}
		return m, nil
	}
	if m.dataDetailOpen {
		if m.activeAction == "providers" {
			return m.updateProviderDetail(key)
		}
		if m.activeAction == "history" {
			switch key.String() {
			case "j", "down":
				m.moveHistoryDetailCursor(1)
				return m, nil
			case "k", "up":
				m.moveHistoryDetailCursor(-1)
				return m, nil
			case "pgdown":
				m.moveHistoryDetailCursor(m.historyDetailWindowSize())
				return m, nil
			case "pgup":
				m.moveHistoryDetailCursor(-m.historyDetailWindowSize())
				return m, nil
			case "home":
				m.setHistoryDetailCursor(0)
				return m, nil
			case "end":
				m.setHistoryDetailCursor(len(m.relatedHistoryForSelectedMission()))
				return m, nil
			}
		}
		if m.selectedAIFixAvailable() && (key.String() == "f" || key.String() == "F" || key.String() == "enter") {
			m.openSelectedAIFix()
			return m, nil
		}
		if key.String() == "esc" || key.String() == "enter" {
			m.dataDetailOpen = false
			m.status = "Ready · " + strings.ToLower(m.activeTab())
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
	if key.Mod == tea.ModCtrl && key.Code == 'k' {
		m.dataFilterOpen = false
		m.paletteOpen = true
		m.paletteQuery = ""
		m.paletteCursor = 0
		return m, nil
	}
	if m.dataFilterOpen {
		return m.updateDataFilter(key)
	}
	if m.supportsDataFilter() && (key.Code == tea.KeyPgDown || key.Code == tea.KeyPgUp) {
		delta := m.dataPageSize()
		if key.Code == tea.KeyPgUp {
			delta = -delta
		}
		m.moveDataCursor(delta)
		return m, nil
	}
	if key.Mod == tea.ModCtrl {
		switch key.Code {
		case 'r':
			if action, ok := m.catalog.Find("route"); ok {
				m.openComposer(action)
			}
			return m, nil
		case 'f':
			return m, m.openPrimaryView(1)
		case 'm':
			return m, m.openPrimaryView(2)
		case 'h':
			return m, m.openPrimaryView(3)
		}
	}
	if key.Mod == tea.ModAlt {
		switch key.Code {
		case 'm':
			return m, m.openPrimaryView(2)
		case 'i':
			return m, m.openPrimaryView(4)
		}
	}
	switch key.String() {
	case "tab", "right":
		return m, m.movePrimaryView(1)
	case "shift+tab", "left":
		return m, m.movePrimaryView(-1)
	}
	if m.isHome() {
		if key.String() == "enter" {
			m.openTaskComposer("")
			return m, nil
		}
		if key.Mod == 0 && key.Text != "" && key.String() != "/" && key.String() != "?" {
			m.openTaskComposer(key.Text)
			return m, nil
		}
	}

	switch key.String() {
	case "q":
		if m.cancelRun != nil {
			m.cancelRun()
		}
		return m, tea.Quit
	case "c":
		if m.activeAction == "" && (len(m.eventHistory) > 0 || m.output.Len() > 0 || m.healthVerification != "") {
			m.clearMissionResult()
			return m, nil
		}
	case "f":
		if m.selectedAIFixAvailable() {
			m.openSelectedAIFix()
			return m, nil
		}
	case "/":
		if m.supportsDataFilter() {
			m.dataFilterOpen = true
			m.status = "Filter · type to narrow this view"
			return m, nil
		}
		m.paletteOpen = true
		m.paletteQuery = ""
		m.paletteCursor = 0
		return m, nil
	case "?":
		m.helpOpen = true
		return m, nil
	case "esc":
		if m.dataFilterQuery != "" && m.supportsDataFilter() {
			m.dataFilterQuery = ""
			m.dataCursor = 0
			m.plansCursor = 0
			m.integrationCursor = 0
			m.status = "Ready · filter cleared"
			return m, nil
		}
		m.goHome()
		return m, nil
	case "h":
		m.activeAction = "history"
		m.resetDataNavigation()
		m.status = "Ready · history"
	case "i":
		m.activeAction = "integrations"
		m.resetDataNavigation()
		m.status = "Ready · integrations"
	case "p":
		if m.activeTab() == "FLEET" {
			m.activeAction = "providers"
			m.resetDataNavigation()
			m.status = "Ready · providers"
			return m, nil
		}
		m.activeAction = "plans"
		m.resetDataNavigation()
		m.status = "Ready · plans"
	case "P":
		m.activeAction = "providers"
		m.resetDataNavigation()
		m.status = "Ready · providers"
	case "s":
		if action, ok := m.catalog.Find("start"); ok {
			m.openComposer(action)
		}
		return m, nil
	case "a":
		if m.activeTab() == "FLEET" {
			m.openProviderLogin("")
			return m, nil
		}
	case "j", "down":
		if m.activeAction == "plans" && len(m.snapshot.Plans) > 0 {
			m.movePlanCursor(1)
			return m, nil
		}
		if m.activeTab() == "INTEGRATIONS" && len(m.snapshot.Integrations) > 0 {
			m.moveIntegrationCursor(1)
			return m, nil
		}
		if m.supportsDataFilter() {
			m.moveDataCursor(1)
			return m, nil
		}
		m.moveSelection(1)
	case "k", "up":
		if m.activeAction == "plans" && len(m.snapshot.Plans) > 0 {
			m.movePlanCursor(-1)
			return m, nil
		}
		if m.activeTab() == "INTEGRATIONS" && len(m.snapshot.Integrations) > 0 {
			m.moveIntegrationCursor(-1)
			return m, nil
		}
		if m.supportsDataFilter() {
			m.moveDataCursor(-1)
			return m, nil
		}
		m.moveSelection(-1)
	case "enter":
		if m.activeTab() == "INTEGRATIONS" {
			return m.activateSelectedIntegration()
		}
		if m.activeAction == "plans" && len(m.filteredPlans()) > 0 {
			m.openPlanComposer()
			return m, nil
		}
		if m.supportsDataFilter() && m.dataRowCount() > 0 {
			m.openSelectedDataDetail()
			m.status = "Inspecting · " + m.selectedDataLabel()
			return m, nil
		}
		if m.activeAction != "" {
			if action, ok := m.catalog.Find(m.activeAction); ok {
				if m.actionSupportsForm(action) {
					m.openComposer(action)
					return m, nil
				}
				if m.options.Service != nil && directActions[action.ID] {
					return m.startAction(action.ID)
				}
			}
			return m, nil
		}
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
		if m.activeAction == "" {
			m.openTaskComposer("")
			return m, nil
		}
		if m.activeTab() == "INTEGRATIONS" {
			return m.activateSelectedIntegration()
		}
		if m.activeAction == "providers" && m.options.Service != nil {
			m.status = "Refreshing · providers"
			return m, m.loadSnapshot()
		}
		if (m.activeAction == "models" || m.activeAction == "doctor") && m.options.Service != nil {
			if m.activeAction == "doctor" {
				return m, m.startHealthRefresh()
			}
			return m.startAction(m.activeAction)
		}
		if action, ok := m.catalog.Find(m.activeAction); ok && m.actionHasForm(action) {
			m.openComposer(action)
		}
	case "t":
		if m.activeAction == "" {
			if action, ok := m.catalog.Find("route"); ok {
				m.openComposer(action)
			}
		}
	case "m":
		m.activeAction = "models"
		m.resetDataNavigation()
		m.status = "Ready · models"
	case "d":
		return m, m.openPrimaryView(3)
	case "home", "0":
		m.goHome()
	default:
		if m.activeAction == "" && key.Mod == 0 && key.Text != "" {
			m.openTaskComposer(key.Text)
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
	case "doctor", "models":
		return false
	default:
		return m.actionHasForm(action)
	}
}

var directActions = map[string]bool{
	"benchmark": true,
	"doctor":    true,
	"models":    true,
	"providers": true,
	"version":   true,
}

type primaryView struct {
	tab    string
	action string
}

var primaryViews = []primaryView{
	{tab: "COMMAND CENTER"},
	{tab: "FLEET", action: "models"},
	{tab: "MISSIONS", action: "history"},
	{tab: "HEALTH", action: "doctor"},
	{tab: "INTEGRATIONS", action: "integrations"},
}

func (m *Model) activeTab() string {
	switch m.activeAction {
	case "models", "providers", "login", "logout", "disable", "enable":
		return "FLEET"
	case "history", "plans", "exec":
		return "MISSIONS"
	case "doctor", "benchmark", "verify-models":
		return "HEALTH"
	case "integrations", "opencode", "hermes", "impeccable":
		return "INTEGRATIONS"
	default:
		return "COMMAND CENTER"
	}
}

func (m *Model) primaryViewIndex() int {
	active := m.activeTab()
	for index, view := range primaryViews {
		if view.tab == active {
			return index
		}
	}
	return 0
}

func (m *Model) openPrimaryView(index int) tea.Cmd {
	if len(primaryViews) == 0 {
		return nil
	}
	index = (index + len(primaryViews)) % len(primaryViews)
	view := primaryViews[index]
	if view.action == "" {
		m.goHome()
		return nil
	}
	m.activeAction = view.action
	m.dataFilterOpen = false
	m.dataFilterQuery = ""
	m.dataCursor = 0
	m.dataDetailOpen = false
	m.plansCursor = 0
	m.integrationCursor = 0
	m.selected = 0
	m.returnToProviders = false
	m.status = "Ready · " + strings.ToLower(view.tab)
	if view.action == "doctor" && m.options.Service != nil {
		return m.startHealthRefresh()
	}
	return nil
}

func (m *Model) startHealthRefresh() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel
	m.running = true
	m.activeAction = "doctor"
	m.healthLoading = true
	m.status = "Checking · fresh health diagnostics"
	request := controlplane.ActionRequest{ActionID: "doctor", Arguments: map[string]string{}}
	return m.execute(request, ctx)
}

func (m *Model) resetDataNavigation() {
	m.dataFilterOpen = false
	m.dataFilterQuery = ""
	m.dataCursor = 0
	m.plansCursor = 0
	m.integrationCursor = 0
	m.dataDetailOpen = false
	m.historyDetailCursor = 0
}

func (m *Model) supportsDataFilter() bool {
	if m.composerOpen || m.confirmOpen || m.paletteOpen || m.helpOpen {
		return false
	}
	switch m.activeAction {
	case "models", "providers", "history", "plans", "doctor", "integrations", "opencode", "hermes":
		return true
	default:
		return false
	}
}

func (m *Model) updateDataFilter(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.dataFilterOpen = false
		m.dataFilterQuery = ""
		m.dataCursor = 0
		m.plansCursor = 0
		m.integrationCursor = 0
		m.status = "Ready · filter cleared"
	case "enter":
		m.dataFilterOpen = false
		m.status = "Ready · filter applied"
	case "backspace":
		runes := []rune(m.dataFilterQuery)
		if len(runes) > 0 {
			m.dataFilterQuery = string(runes[:len(runes)-1])
		}
		m.dataCursor = 0
		m.plansCursor = 0
		m.integrationCursor = 0
	default:
		if key.Text != "" && key.Mod == 0 {
			m.dataFilterQuery += key.Text
			m.dataCursor = 0
			m.plansCursor = 0
			m.integrationCursor = 0
		}
	}
	return m, nil
}

func (m *Model) movePrimaryView(delta int) tea.Cmd {
	return m.openPrimaryView(m.primaryViewIndex() + delta)
}

func (m *Model) goHome() {
	m.activeAction = ""
	m.composerOpen = false
	m.confirmOpen = false
	m.resetDataNavigation()
	m.status = "Ready · Command Center"
}

func (m *Model) clearMissionResult() {
	m.output.Reset()
	m.outputAction = ""
	m.eventHistory = nil
	m.lastEvent = ""
	m.decisionModel = ""
	m.decisionWhy = ""
	m.decisionConf = ""
	m.activeHealthFix = ""
	m.healthVerification = ""
	m.verifyHealthFix = false
	m.status = "Ready · results cleared"
}

func (m *Model) finishHealthVerification() {
	if !m.verifyHealthFix || m.activeHealthFix == "" {
		return
	}
	m.verifyHealthFix = false
	status := "RESOLVED"
	message := "finding is no longer reported"
	for _, finding := range m.snapshot.Health {
		if finding.ID != m.activeHealthFix {
			continue
		}
		status = strings.ToUpper(strings.TrimSpace(finding.Status))
		message = finding.Message
		break
	}
	if m.activeHealthFix == "release.integrity" && strings.Contains(strings.ToLower(message), "offline") {
		status = "UNVERIFIED"
		message = "online checksum verification is still required"
	} else if healthFindingInformational(m.activeHealthFix, status) {
		status = "EXPECTED"
		message += " · use an official release binary if verified release provenance is required"
	}
	m.healthVerification = fmt.Sprintf("%s · %s · %s", m.activeHealthFix, status, message)
	if status == "PASS" || status == "FIXED" || status == "RESOLVED" {
		m.status = "Verified fixed · " + m.activeHealthFix
		return
	}
	if status == "UNVERIFIED" {
		m.status = "Verification required · " + m.activeHealthFix
		return
	}
	if status == "EXPECTED" {
		m.status = "Expected development warning · " + m.activeHealthFix
		return
	}
	m.status = "Still " + status + " · " + m.activeHealthFix
}

func (m *Model) moveIntegrationCursor(delta int) {
	integrations := m.filteredIntegrations()
	if len(integrations) == 0 {
		return
	}
	m.integrationCursor = (m.integrationCursor + delta + len(integrations)) % len(integrations)
	m.status = "Ready · " + integrations[m.integrationCursor].Name
}

func (m *Model) activateSelectedIntegration() (tea.Model, tea.Cmd) {
	integrations := m.filteredIntegrations()
	if len(integrations) == 0 {
		m.status = "Ready · no integration selected"
		return m, nil
	}
	selected := integrations[m.integrationCursor%len(integrations)]
	integration := strings.ToLower(strings.TrimSpace(selected.Name))
	switch integration {
	case "opencode":
		if selected.PrimaryAction == "connect" {
			return m.requestExecution(controlplane.ActionRequest{ActionID: "opencode", Arguments: map[string]string{"subcommand": "connect", "cli": "true"}})
		}
		return m.requestExecution(controlplane.ActionRequest{ActionID: "opencode", Arguments: map[string]string{"subcommand": "status"}})
	case "hermes":
		operation := selected.PrimaryAction
		arguments := map[string]string{"subcommand": "plugin", "operation": operation}
		if operation == "repair" {
			arguments["operation"] = "install"
			arguments["force"] = "true"
		}
		return m.requestExecution(controlplane.ActionRequest{ActionID: "hermes", Arguments: arguments})
	case "impeccable":
		return m.requestExecution(controlplane.ActionRequest{ActionID: "impeccable", Arguments: map[string]string{"operation": "install"}})
	default:
		m.status = "Ready · unsupported integration"
		return m, nil
	}
}

func (m *Model) filteredIntegrations() []controlplane.IntegrationSnapshot {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return m.snapshot.Integrations
	}
	filtered := make([]controlplane.IntegrationSnapshot, 0, len(m.snapshot.Integrations))
	for _, integration := range m.snapshot.Integrations {
		searchable := strings.Join([]string{integration.Name, integration.Status, integration.Detail, integration.PrimaryAction}, " ")
		if strings.Contains(strings.ToLower(searchable), query) {
			filtered = append(filtered, integration)
		}
	}
	return filtered
}

func isRoutingAction(actionID string) bool {
	return actionID == "run" || actionID == "route" || actionID == "exec"
}

func (m *Model) updateComposer(key tea.Key) (tea.Model, tea.Cmd) {
	if m.composerEditing {
		return m.updateComposerField(key)
	}
	if key.Mod == tea.ModCtrl && key.Code == tea.KeyEnter {
		if m.composerNeedsObjective() && strings.TrimSpace(m.composerInput) == "" {
			m.status = "Error · enter a task objective"
			return m, nil
		}
		return m.startExecution()
	}
	switch key.String() {
	case "esc":
		m.composerOpen = false
		m.composerInput = ""
		m.returnToProviders = false
	case "backspace":
		if len(m.composerInput) > 0 {
			runes := []rune(m.composerInput)
			m.composerInput = string(runes[:len(runes)-1])
		}
	case "enter", "tab", "down":
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
	m.returnToProviders = false
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

func (m *Model) openProviderLogin(provider string) {
	action, ok := m.catalog.Find("login")
	if !ok {
		m.status = "Error · provider connection unavailable"
		return
	}
	m.openComposer(action)
	m.returnToProviders = true
	if provider != "" {
		m.composerValues["provider"] = provider
	}
	m.status = "Provider · connect or update securely"
}

func (m *Model) openProviderVerification(provider string) {
	action, ok := m.catalog.Find("verify-models")
	if !ok {
		m.status = "Error · provider verification unavailable"
		return
	}
	m.openComposer(action)
	m.returnToProviders = true
	m.composerValues["provider"] = provider
	m.status = "Provider · verify available models"
}

func (m *Model) openTaskComposer(initialText string) {
	m.pendingHealthFix = ""
	action, ok := m.catalog.Find("run")
	if !ok {
		m.status = "Error · run command unavailable"
		return
	}
	m.openComposer(action)
	m.composerInput = initialText
	m.status = "Task · describe what you want Veto to do"
}

// openAIFixComposer configures a repair as an executable code-change mission.
// A health or history repair must be able to inspect and modify the local
// installation; leaving these defaults unset lets text-only providers reject
// the task during admission.
func (m *Model) openAIFixComposer(initialText string) {
	m.openTaskComposer(initialText)
	if !m.composerOpen {
		return
	}
	m.composerValues["kind"] = string(router.KindCodeChange)
	m.composerValues["requires-executable-tools"] = "true"
}

func (m *Model) openPlanComposer() {
	action, ok := m.catalog.Find("exec")
	if !ok {
		m.status = "Error · execute plan command unavailable"
		return
	}
	plans := m.filteredPlans()
	if len(plans) == 0 {
		m.status = "Ready · no plan selected"
		return
	}
	selected := plans[min(m.plansCursor, len(plans)-1)].Name
	m.openComposer(action)
	if len(m.composerFields) == 0 {
		return
	}
	m.composerValues["plan"] = selected
	m.composerField = 0
	m.composerEditing = true
	m.status = "Flags · plan=" + m.composerValues["plan"]
}

func defaultSubcommand(actionID, fallback string) string {
	switch actionID {
	case "analytics", "opencode":
		return "status"
	case "hermes":
		return "plugin"
	default:
		return fallback
	}
}

func (m *Model) composerNeedsObjective() bool {
	return m.composerAction == "run" || m.composerAction == "route" || m.composerAction == "start"
}

func (m *Model) updateComposerField(key tea.Key) (tea.Model, tea.Cmd) {
	if len(m.composerFields) == 0 {
		return m.startExecution()
	}
	field := m.composerFields[m.composerField]
	if choices := m.fieldChoices(field); len(choices) > 0 && (key.String() == "space" || key.Text == " ") {
		current := m.composerValues[field.Name]
		index := 0
		for choiceIndex, choice := range choices {
			if choice == current {
				index = (choiceIndex + 1) % len(choices)
				break
			}
		}
		m.composerValues[field.Name] = choices[index]
		m.status = "Flags · " + field.Name + "=" + choices[index]
		return m, nil
	}
	if field.Value == "bool" && (key.String() == "space" || key.Text == " ") {
		if m.composerValues[field.Name] == "true" {
			m.composerValues[field.Name] = "false"
		} else {
			m.composerValues[field.Name] = "true"
		}
		m.status = "Flags · " + field.Name + "=" + m.composerValues[field.Name]
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.composerOpen = false
		m.composerEditing = false
		m.returnToProviders = false
	case "tab", "down":
		m.composerField = (m.composerField + 1) % len(m.composerFields)
		m.status = "Flags · " + m.composerFields[m.composerField].Name
	case "shift+tab", "up":
		m.composerField = (m.composerField - 1 + len(m.composerFields)) % len(m.composerFields)
		m.status = "Flags · " + m.composerFields[m.composerField].Name
	case "backspace":
		value := m.composerValues[field.Name]
		if len(value) > 0 {
			runes := []rune(value)
			m.composerValues[field.Name] = string(runes[:len(runes)-1])
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
			value := m.composerValues[field.Name]
			if value == field.Default {
				value = ""
			}
			m.composerValues[field.Name] = value + key.Text
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
	return m.requestExecution(request)
}

func (m *Model) requestExecution(request controlplane.ActionRequest) (tea.Model, tea.Cmd) {
	if requiresConfirmation(request) {
		m.pendingRequest = request
		m.confirmOpen = true
		m.status = "Confirm · " + request.ActionID
		return m, nil
	}
	return m.beginExecution(request)
}

func (m *Model) beginExecution(request controlplane.ActionRequest) (tea.Model, tea.Cmd) {
	if m.running {
		m.status = "Busy · " + m.activeAction + " is still running"
		return m, nil
	}
	if request.ActionID == "run" || request.ActionID == "route" {
		if request.Arguments == nil {
			request.Arguments = make(map[string]string)
		}
		if strings.TrimSpace(request.Arguments["task-id"]) == "" {
			m.missionSequence++
			request.Arguments["task-id"] = fmt.Sprintf("tui-%d-%d", time.Now().UTC().UnixMilli(), m.missionSequence)
		}
	}
	m.running = true
	m.activeAction = request.ActionID
	m.output.Reset()
	m.outputAction = request.ActionID
	m.eventHistory = nil
	m.lastEvent = "starting"
	m.decisionModel = ""
	m.decisionWhy = ""
	m.decisionConf = ""
	m.activeHealthFix = m.pendingHealthFix
	m.pendingHealthFix = ""
	m.healthVerification = ""
	m.verifyHealthFix = false
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
	case "doctor":
		return request.Arguments["fix"] == "true"
	case "setup":
		return request.Arguments["auto-approve"] == "true" || strings.TrimSpace(request.Arguments["approved-files"]) != ""
	case "opencode":
		subcommand := request.Arguments["subcommand"]
		return subcommand == "connect" || subcommand == "disconnect" || (subcommand == "plugin" && request.Arguments["operation"] != "status")
	case "hermes":
		return request.Arguments["subcommand"] == "plugin" && request.Arguments["operation"] != "status"
	case "impeccable":
		return request.Arguments["operation"] == "install"
	case "analytics":
		return request.Arguments["subcommand"] == "enable" || request.Arguments["subcommand"] == "disable"
	default:
		return false
	}
}

func (m *Model) startAction(actionID string) (tea.Model, tea.Cmd) {
	if m.running {
		m.status = "Busy · " + m.activeAction + " is still running"
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel
	m.events = m.options.Service.Subscribe(ctx)
	m.running = true
	m.activeAction = actionID
	m.output.Reset()
	m.outputAction = actionID
	m.eventHistory = nil
	m.lastEvent = "starting"
	m.decisionModel = ""
	m.decisionWhy = ""
	m.decisionConf = ""
	m.status = "Running · " + actionID
	request := controlplane.ActionRequest{ActionID: actionID, Arguments: map[string]string{}}
	return m, tea.Batch(m.execute(request, ctx), waitForEvent(m.events))
}

func (m *Model) execute(request controlplane.ActionRequest, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		result, err := m.options.Service.Execute(ctx, request)
		return executionResultMsg{result: result, err: err, request: request}
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
			action := filtered[m.paletteCursor%len(filtered)]
			m.resetDataNavigation()
			m.activeAction = action.ID
			m.status = "Ready · " + action.Command
			m.paletteOpen = false
			m.paletteQuery = ""
			if m.actionSupportsForm(action) {
				m.openComposer(action)
				return m, nil
			}
			if m.options.Service != nil && directActions[action.ID] {
				return m.startAction(action.ID)
			}
		}
		m.paletteOpen = false
		m.paletteQuery = ""
	case "backspace":
		if len(m.paletteQuery) > 0 {
			runes := []rune(m.paletteQuery)
			m.paletteQuery = string(runes[:len(runes)-1])
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
	m.resetDataNavigation()
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

func (m *Model) movePlanCursor(delta int) {
	plans := m.filteredPlans()
	if len(plans) == 0 {
		return
	}
	m.plansCursor = min(max(0, m.plansCursor+delta), len(plans)-1)
	m.status = "Selected · plan " + plans[m.plansCursor].Name
}

func (m *Model) filteredActions() []controlplane.ActionSpec {
	query := strings.ToLower(strings.TrimSpace(m.paletteQuery))
	if query == "" {
		return m.catalog.Commands()
	}
	filtered := make([]controlplane.ActionSpec, 0)
	for _, action := range m.catalog.Commands() {
		if strings.Contains(strings.ToLower(action.ID), query) || strings.Contains(strings.ToLower(action.Label), query) || strings.Contains(strings.ToLower(action.Command), query) || strings.Contains(strings.ToLower(action.Category), query) || strings.Contains(strings.ToLower(action.Description), query) {
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
	if m.dataDetailOpen {
		content = m.renderDataDetail()
	}
	if m.options.NoColor {
		content = ansi.Strip(content)
	}
	view := tea.NewView(content)
	// Screen-reader mode still uses the alternate screen: inline rendering
	// relies on cursor-addressing sequences that some assistive terminals do
	// not honor, which otherwise leaves every frame stacked in scrollback.
	view.AltScreen = true
	view.DisableBracketedPasteMode = m.options.ScreenReader
	// Veto does not use focus events; avoid emitting focus-reporting control
	// sequences that can confuse terminal tabs when the window is backgrounded.
	view.ReportFocus = false
	if m.options.Mouse {
		view.MouseMode = tea.MouseModeCellMotion
	}
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
	availableBody := max(0, height-statusHeight)
	if len(bodyLines) > availableBody {
		switch {
		case availableBody == 0:
			bodyLines = nil
		case availableBody == 1:
			bodyLines = []string{mutedStyle.Render("…")}
		default:
			bodyLines = append(bodyLines[:availableBody-1], mutedStyle.Render("… more below; resize or use the palette"))
		}
	}
	// Overlays are appended after the shell content. Do not pre-fill the
	// background to terminal height first, or the palette/help panel lands
	// below the viewport and captures keys while remaining invisible.
	if !m.paletteOpen && !m.helpOpen && !m.dataDetailOpen && len(bodyLines) < availableBody {
		bodyLines = append(bodyLines, make([]string, availableBody-len(bodyLines))...)
	}
	if len(bodyLines) == 0 {
		return status
	}
	return strings.Join(bodyLines, "\n") + "\n" + status
}

func (m *Model) renderWide(commands []controlplane.ActionSpec, width int) string {
	rightWidth := 30
	mainWidth := width - rightWidth - 2
	if mainWidth < 36 {
		return m.renderMedium(commands, width)
	}
	main := m.renderMain(mainWidth)
	right := m.renderInspectorPanel(rightWidth)
	return m.appendRoutingWorkspace(lipgloss.JoinHorizontal(lipgloss.Top, main, "  ", right), width)
}

func (m *Model) renderMedium(commands []controlplane.ActionSpec, width int) string {
	return m.appendRoutingWorkspace(m.renderMain(width), width)
}

func (m *Model) renderNarrow(commands []controlplane.ActionSpec, width int) string {
	return m.appendRoutingWorkspace(m.renderMain(width), width)
}

// appendRoutingWorkspace keeps the inspector concise. Routing events are a
// working surface, not sidebar metadata: they need the full terminal width to
// remain readable while a task is being admitted or executed.
func (m *Model) appendRoutingWorkspace(content string, width int) string {
	if !m.showsRoutingWorkspace() || m.height > 0 && m.height < 32 {
		return content
	}
	style := workspacePanelStyle.Width(max(20, width))
	if m.height >= 32 {
		statusHeight := lipgloss.Height(m.statusLine(width))
		workspaceHeight := m.height - lipgloss.Height(content) - statusHeight - 2
		if workspaceHeight >= 5 {
			style = style.Height(workspaceHeight)
		}
	}
	workspace := style.Render(m.renderLiveTimeline(max(16, width-6)))
	return strings.TrimSuffix(content, "\n") + "\n\n" + workspace
}

func (m *Model) showsRoutingWorkspace() bool {
	return m.activeAction == "" || m.isMissionComposer() || isRoutingAction(m.activeAction)
}

func (m *Model) isHome() bool {
	return m.activeAction == "" && !m.composerOpen && !m.confirmOpen && len(m.eventHistory) == 0 && m.output.Len() == 0
}

func (m *Model) isMissionComposer() bool {
	return m.composerOpen && (m.composerAction == "run" || m.composerAction == "route")
}

func (m *Model) renderCommandList(commands []controlplane.ActionSpec, width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("COMMANDS · %d", len(commands))))
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
		if index == m.hoveredCommand {
			description := truncate("  "+action.Description, width-2)
			b.WriteString(mutedStyle.Render(description))
			b.WriteByte('\n')
		}
		if index >= 8 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  + %d more · / palette", len(commands)-index-1)))
			break
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.TrimSuffix(b.String(), "\n"))
}

func (m *Model) renderMain(width int) string {
	active := "Command Center"
	if m.activeAction != "" {
		if action, ok := m.catalog.Find(m.activeAction); ok {
			active = action.Label
		} else {
			switch m.activeAction {
			case "history":
				active = "Recent activity"
			case "plans":
				active = "Plans"
			case "integrations":
				active = "Integrations"
			default:
				active = m.activeAction
			}
		}
	}
	if m.composerOpen {
		if action, ok := m.catalog.Find(m.composerAction); ok {
			active = action.Label
		} else if m.composerAction != "" {
			active = m.composerAction
		}
	}
	if m.confirmOpen && m.pendingRequest.ActionID != "" {
		if action, ok := m.catalog.Find(m.pendingRequest.ActionID); ok {
			active = action.Label
		} else {
			active = m.pendingRequest.ActionID
		}
	}
	var b strings.Builder
	home := m.activeAction == "" && !m.composerOpen && !m.confirmOpen && len(m.eventHistory) == 0 && m.output.Len() == 0
	if home {
		return m.renderHome(width)
	}
	b.WriteString(m.renderAppHeader(width))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render(m.activeTab()))
	b.WriteString("  ")
	b.WriteString(titleStyle.Render(active))
	b.WriteString("\n")
	instruction := m.pageInstruction()
	if m.composerOpen {
		instruction = "Set the fields below. Enter advances; Esc cancels without running anything."
	} else if m.confirmOpen {
		instruction = "Review this action before Veto makes changes."
	}
	b.WriteString(mutedStyle.Render(instruction))
	b.WriteString("\n\n")
	if m.composerOpen {
		b.WriteString(m.renderComposer(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.confirmOpen {
		b.WriteString(m.renderConfirmation(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if len(m.eventHistory) > 0 && isRoutingAction(m.activeAction) {
		b.WriteString(headerStyle.Render("ROUTING PIPELINE"))
		b.WriteString("\n")
		b.WriteString(m.renderPipeline(width))
		b.WriteString("\n\n")
		if m.height > 0 && m.height < 32 {
			b.WriteString(m.renderLiveTimeline(width))
			b.WriteString("\n\n")
		}
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
	if m.activeAction == "plans" || (m.activeAction == "exec" && m.output.Len() == 0) {
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
	if m.activeAction == "integrations" || m.activeAction == "opencode" || m.activeAction == "hermes" || m.activeAction == "impeccable" {
		b.WriteString(m.renderIntegrations(width))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	if m.healthVerification != "" {
		b.WriteString(headerStyle.Render("AUTHORITATIVE HEALTH VERIFICATION"))
		b.WriteString("\n")
		b.WriteString(m.healthVerification)
		b.WriteString("\n\n")
	}
	if m.output.Len() > 0 && (m.activeAction == "" || m.outputAction == "" || m.outputAction == m.activeAction) {
		outputHeader := "OUTPUT"
		if m.activeHealthFix != "" {
			outputHeader = "AGENT REPORT · HEALTH CHECK ABOVE IS AUTHORITATIVE"
		}
		b.WriteString(headerStyle.Render(outputHeader))
		b.WriteString("\n")
		b.WriteString(workspacePanelStyle.Width(max(12, width)).Render(formatMultiline(m.output.String(), max(10, width-8), 16)))
		b.WriteString("\n\n")
	} else {
		b.WriteString(panelStyle.Render(truncate("⌘  Run a task   /  Find command   ?  Help", width-4)))
		b.WriteString("\n\n")
		b.WriteString(headerStyle.Render("NEXT"))
		b.WriteString("\n")
		b.WriteString("Start with a command or open the palette to inspect flags.\n")
		b.WriteString(mutedStyle.Render("Run and Route start provider work when submitted; state-changing actions ask first."))
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) pageInstruction() string {
	switch m.activeTab() {
	case "FLEET":
		return "M Models · P Providers · A Add provider. Click or use ↑/↓; Enter manages the selected row."
	case "MISSIONS":
		return "Review all local routing events. Click or use ↑/↓; / filters across every page."
	case "HEALTH":
		return "Select a finding for details. Press r to refresh diagnostics; / filters every row."
	case "INTEGRATIONS":
		return "Choose an integration and press Enter. Veto performs the action; changes ask for confirmation."
	default:
		return "Follow the routing decision, execution output, and review result."
	}
}

func (m *Model) renderHome(width int) string {
	var b strings.Builder
	viewportHeight := m.height
	if viewportHeight < 1 {
		viewportHeight = 24
	}
	compact := viewportHeight < 30
	b.WriteString(m.renderAppHeader(width))
	b.WriteString("\n\n")
	b.WriteString(m.renderMissionInput(width, compact))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render("ROUTING DECISION"))
	b.WriteString("\n")
	b.WriteString(m.renderPipeline(width))
	if compact {
		models, harnesses := m.fleetEntityCounts()
		fleet := fmt.Sprintf("%d providers / %d models", len(m.snapshot.Providers), models)
		if harnesses > 0 {
			fleet += fmt.Sprintf(" / %d harness%s", harnesses, pluralSuffix(harnesses))
		}
		b.WriteString("\n\n")
		b.WriteString(panelStyle.Width(max(20, width-4)).Render(fmt.Sprintf("Fleet  %s   ·   Ctrl+K opens all %d COMMANDS", fleet, len(m.catalog.Commands()))))
		return lipgloss.NewStyle().Width(width).Render(b.String())
	}
	b.WriteString("\n\n")
	b.WriteString(m.renderHomePanels(width))
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) renderAppHeader(width int) string {
	var b strings.Builder
	identity := lipgloss.JoinHorizontal(lipgloss.Top, renderVetoMark(), "  ", brandStyle.Render(vetoWordmark))
	meta := strings.Join([]string{
		titleStyle.Render("LOCAL AI CONTROL PLANE"),
		mutedStyle.Render("TUI v" + displayVersion(m.options.Version) + "  ·  " + strings.ToUpper(m.currentState())),
	}, "\n")
	if width >= 78 {
		b.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Center,
			identity,
			"    ",
			meta,
		))
	} else {
		b.WriteString(identity)
		b.WriteString("\n")
		b.WriteString(meta)
	}
	b.WriteString("\n\n")
	if width >= 76 {
		b.WriteString(renderTabs(m.activeTab(), width))
	} else {
		b.WriteString(renderCompactTabs(m.activeTab()))
	}
	return b.String()
}

func renderVetoMark() string {
	lines := make([]string, 0, len(vetoMarkRows))
	for _, row := range vetoMarkRows {
		lines = append(lines, brandStyle.Render(row.left)+titleStyle.Render(row.check)+brandStyle.Render(row.right))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) currentState() string {
	if m.running {
		return "routing"
	}
	if strings.HasPrefix(m.status, "Error") {
		return "attention"
	}
	if strings.HasPrefix(m.status, "Loading") {
		return "loading"
	}
	return "ready"
}

func isDecisionModelEvent(kind string) bool {
	return kind == "route.ask_start" || kind == "route.ask_accept" || kind == "route.completed" || strings.HasPrefix(kind, "execution.")
}

func isDecisionReasonEvent(kind string) bool {
	return kind == "route.ask_accept" || kind == "route.completed" || kind == "route.ask_reject" || kind == "route.ask_error"
}

func (m *Model) restoreDecisionFromSnapshot() {
	monitor := m.snapshot.Monitor
	if m.decisionModel == "" {
		m.decisionModel = monitor.LastModel
	}
	if m.decisionConf == "" && monitor.LastConfidenceKnown {
		m.decisionConf = fmt.Sprintf("%.0f%%", monitor.LastConfidence*100)
	}
	if m.decisionWhy == "" && len(monitor.LastReasons) > 0 {
		m.decisionWhy = explainReasons(monitor.LastReasons)
	}
}

func explainReasons(reasons []string) string {
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		switch strings.ToUpper(reason) {
		case "MISSING_REQUIRED_TOOL":
			reason = "required tool is not available"
		case "CONTEXT_TOO_LARGE":
			reason = "task context exceeds the model limit"
		case "COST_CEILING_EXCEEDED":
			reason = "estimated cost exceeds the budget"
		case "COMPLEXITY_TOO_HIGH":
			reason = "task is above this model's complexity tier"
		case "TASK_KIND_OUTSIDE_STRENGTHS":
			reason = "task type is outside the model's strengths"
		case "RISK_TOO_HIGH":
			reason = "task risk is above the model's allowed level"
		case "USER_POLICY_EXCLUDED":
			reason = "excluded by local policy"
		case "PARSE_FAILURE":
			reason = "admission response could not be read"
		case "LOW_CONFIDENCE":
			reason = "model reported low confidence"
		}
		if reason != "" {
			parts = append(parts, reason)
		}
	}
	return strings.Join(parts, ", ")
}

func displayVersion(value string) string {
	version := strings.TrimPrefix(strings.TrimSpace(value), "v")
	if version == "" {
		return "dev"
	}
	dirty := strings.Contains(version, "+dirty") || strings.HasSuffix(version, "-dirty")
	version = strings.TrimSuffix(version, "+dirty")
	version = strings.TrimSuffix(version, "-dirty")
	if index := strings.Index(version, "-0."); index > 0 {
		version = version[:index]
		dirty = true
	}
	if index := strings.IndexByte(version, '+'); index > 0 {
		version = version[:index]
	}
	if dirty && !strings.HasSuffix(version, "-dev") {
		version += "-dev"
	}
	return version
}

type primaryTabLabel struct {
	name     string
	shortcut string
}

var primaryTabLabels = []primaryTabLabel{
	{name: "COMMAND CENTER", shortcut: "Esc"},
	{name: "FLEET", shortcut: "Ctrl+F"},
	{name: "MISSIONS", shortcut: "Alt+M"},
	{name: "HEALTH", shortcut: "Ctrl+H"},
	{name: "INTEGRATIONS", shortcut: "Alt+I"},
}

func renderTabs(active string, width int) string {
	parts := make([]string, 0, len(primaryTabLabels)+1)
	for _, tab := range primaryTabLabels {
		label := primaryTabText(tab, width)
		if tab.name == active {
			parts = append(parts, tabActiveStyle.Render(" "+label+" "))
			continue
		}
		parts = append(parts, tabInactiveStyle.Render(" "+label+" "))
	}
	if width < 116 {
		parts = append(parts, mutedStyle.Render("Tab switches"))
	}
	return strings.Join(parts, " ")
}

func primaryTabText(tab primaryTabLabel, width int) string {
	if width >= 116 {
		return tab.name + "  " + tab.shortcut
	}
	return tab.name
}

func renderCompactTabs(active string) string {
	return tabActiveStyle.Render(" "+active+" ") + " " + mutedStyle.Render("Tab/Shift+Tab switch views")
}

func (m *Model) renderMissionInput(width int, compact bool) string {
	contentWidth := max(20, width-8)
	input := selectedStyle.Width(contentWidth).Render("›  Describe a task — start typing anywhere")
	metadata := renderMissionOptions("medium", "auto", "auto", "optional")
	lines := []string{headerStyle.Render("NEW MISSION") + "  " + titleStyle.Render("What should Veto accomplish?"), input, metadata, mutedStyle.Render("Enter compose   ·   Ctrl+R route only   ·   Ctrl+K all commands")}
	if !compact {
		lines = append([]string{headerStyle.Render("NEW MISSION"), titleStyle.Render("What should Veto accomplish?")}, lines[1:]...)
	}
	content := strings.Join(lines, "\n")
	style := heroPanelStyle
	if compact {
		style = style.Padding(0, 2)
	}
	return style.Width(max(20, width-6)).Render(content)
}

func (m *Model) renderHomePanels(width int) string {
	if width < 68 {
		return m.renderSuggestions(width)
	}
	gap := 2
	leftWidth := (width - gap) * 3 / 5
	rightWidth := width - gap - leftWidth
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderSuggestions(leftWidth),
		strings.Repeat(" ", gap),
		m.renderFleetSummary(rightWidth),
	)
}

func (m *Model) renderSuggestions(width int) string {
	lines := []string{
		headerStyle.Render("SUGGESTED MISSIONS"),
		mutedStyle.Render("Start from intent; Veto handles model selection."),
		"",
		"1  Review changes for risk",
		"2  Plan within a budget",
		"3  Compare models only",
		"",
		mutedStyle.Render("Type any objective to begin. Nothing runs until you submit."),
	}
	return workspacePanelStyle.Width(max(12, width-6)).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderFleetSummary(width int) string {
	providers := len(m.snapshot.Providers)
	models, harnesses := m.fleetEntityCounts()
	count := fmt.Sprintf("%d providers / %d models", providers, models)
	if harnesses > 0 {
		count += fmt.Sprintf(" / %d harness%s", harnesses, pluralSuffix(harnesses))
	}
	lines := []string{
		headerStyle.Render("FLEET AT A GLANCE"),
		count,
		"",
		"policy       cap + cost",
		"admission    required",
		"execute      after winner",
		"review       criteria",
		"",
		mutedStyle.Render("Ctrl+F Fleet   ·   Ctrl+H Health"),
	}
	return workspacePanelStyle.Width(max(12, width-6)).Render(strings.Join(lines, "\n"))
}

func (m *Model) fleetEntityCounts() (int, int) {
	models := 0
	harnesses := 0
	for _, model := range m.snapshot.Models {
		if model.Kind == controlplane.ModelKindHarness {
			harnesses++
		} else {
			models++
		}
	}
	return models, harnesses
}

func (m *Model) displayComposerValue(field controlplane.FlagSpec) string {
	value := m.composerValues[field.Name]
	if field.Secret && value != "" {
		return strings.Repeat("•", len([]rune(value)))
	}
	return value
}

func (m *Model) renderComposer(width int) string {
	contentWidth := max(20, width-10)
	lines := []string{headerStyle.Render("MISSION COMPOSER · " + strings.ToUpper(m.composerAction))}
	if m.composerNeedsObjective() {
		objective := m.composerInput
		if objective == "" {
			objective = mutedStyle.Render("Describe the outcome you want")
		}
		lines = append(lines,
			titleStyle.Render("Objective"),
			selectedStyle.Width(contentWidth).Render("›  "+objective+"▌"),
			renderMissionOptions(
				valueOrDefault(m.composerValues["risk"], "medium"),
				valueOrDefault(m.composerValues["max-cost"], "auto"),
				valueOrDefault(m.composerValues["required-tools"], "auto"),
				valueOrDefault(m.composerValues["criteria"], "optional"),
			),
		)
	}
	if m.composerEditing && len(m.composerFields) > 0 {
		field := m.composerFields[m.composerField]
		lines = append(lines,
			"",
			headerStyle.Render(fmt.Sprintf("FOCUSED OPTION %d/%d", m.composerField+1, len(m.composerFields))),
			selectedStyle.Width(contentWidth).Render(m.renderComposerControl(field)),
			mutedStyle.Render(field.Description),
		)
		hint := "Enter next/run   ·   Tab move   ·   Esc cancel"
		if choices := m.fieldChoices(field); len(choices) > 0 {
			hint = "Space cycle " + strings.Join(choices, "/") + "   ·   Enter next/run   ·   Esc cancel"
		}
		if field.Value == "bool" {
			hint = "Space toggle   ·   Enter next/run   ·   Tab move   ·   Esc cancel"
		}
		lines = append(lines, mutedStyle.Render(hint))
	} else if m.composerNeedsObjective() {
		action := "RUN MISSION"
		if strings.Contains(strings.ToLower(m.composerInput), "veto health finding") {
			action = "RUN SAFE REPAIR"
		}
		lines = append(lines,
			"",
			headerStyle.Render("NEXT STEP"),
			selectedStyle.Render("[Ctrl+Enter] "+action)+"  "+mutedStyle.Render("Veto selects the model and shows every decision."),
			mutedStyle.Render("Optional: [Enter] or [Tab] configure risk, budget, tools, and review criteria   ·   [Esc] return"),
		)
	} else {
		lines = append(lines, mutedStyle.Render("Enter run   ·   Esc cancel"))
	}
	return heroPanelStyle.Width(max(20, width-6)).Render(strings.Join(lines, "\n"))
}

func renderMissionOptions(risk, budget, tools, criteria string) string {
	return strings.Join([]string{
		renderOptionValue("Risk", risk),
		renderOptionValue("Budget", budget),
		renderOptionValue("Tools", tools),
		renderOptionValue("Criteria", criteria),
	}, mutedStyle.Render("  ·  "))
}

func renderOptionValue(label, value string) string {
	return mutedStyle.Render(label+" ") + optionValueStyle.Render("["+value+"]")
}

func (m *Model) renderComposerControl(field controlplane.FlagSpec) string {
	label := strings.ToUpper(strings.ReplaceAll(field.Name, "-", " "))
	value := m.displayComposerValue(field)
	if field.Value == "bool" {
		checked := " "
		state := "disabled"
		if value == "true" {
			checked = "x"
			state = "enabled"
		}
		return fmt.Sprintf("›  %s  [%s] %s", label, checked, state)
	}
	if choices := m.fieldChoices(field); len(choices) > 0 {
		if value == "" {
			value = "auto"
		}
		return fmt.Sprintf("›  %s  [ %s ]  Space: next choice", label, value)
	}
	if value == "" {
		value = "type a value (optional)"
	}
	return fmt.Sprintf("›  %s  %s▌", label, value)
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (m *Model) renderPipeline(width int) string {
	completed := m.routingProgress()
	parts := make([]string, 0, len(routingStages))
	for index, stage := range routingStages {
		label := stage
		if index < completed {
			label = "✓ " + stage
		} else if index == completed && m.running {
			label = "› " + stage
		}
		parts = append(parts, label)
	}
	return truncate(strings.Join(parts, " → "), width)
}

func (m *Model) routingProgress() int {
	completed := 0
	for _, event := range m.eventHistory {
		switch strings.TrimPrefix(event.Kind, "route.") {
		case "filter_pass", "filter_fail":
			completed = max(completed, 1)
		case "shortlist":
			completed = max(completed, 2)
		case "ask_start", "ask_reject", "ask_error":
			completed = max(completed, 3)
		case "ask_accept", "completed":
			completed = max(completed, 4)
		}
		if strings.HasPrefix(event.Kind, "execution.") || event.Kind == "output" || event.Kind == "output.saved" {
			completed = max(completed, 5)
		}
		if strings.HasPrefix(event.Kind, "review.") || strings.HasSuffix(event.Kind, ".completed") && event.ActionID == "run" {
			completed = max(completed, 6)
		}
	}
	if m.running && completed == 0 {
		completed = 1
	}
	return min(completed, len(routingStages))
}

func (m *Model) fieldChoices(field controlplane.FlagSpec) []string {
	switch field.Name {
	case "subcommand":
		if action, ok := m.catalog.Find(m.composerAction); ok {
			return action.Subcommands
		}
	case "operation":
		if m.composerAction == "opencode" || m.composerAction == "hermes" {
			return []string{"status", "install", "uninstall"}
		}
	case "mode":
		if m.composerAction == "login" {
			switch strings.ToLower(m.composerValues["provider"]) {
			case "anthropic":
				return []string{"api-key", "subscription"}
			case "openrouter":
				return []string{"api-key", "browser"}
			case "local", "opencode":
				return []string{"runtime"}
			default:
				return []string{"api-key"}
			}
		}
	case "kind":
		if m.composerAction == "run" || m.composerAction == "route" {
			return []string{"extract", "summarize", "code-change", "debug", "plan", "review", "refactor"}
		}
		if m.composerAction == "feedback" {
			return []string{"bug", "feature", "optimization", "success"}
		}
	case "risk":
		return []string{"low", "medium", "high"}
	case "on-failure":
		return []string{"abort-ask", "abort", "continue"}
	}
	return nil
}

func (m *Model) renderConfirmation(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("CONFIRM ACTION"))
	b.WriteString("\n")
	action, ok := m.catalog.Find(m.pendingRequest.ActionID)
	command := m.pendingRequest.ActionID
	description := "Review the request before Veto makes changes."
	if ok {
		command = action.Command
		description = action.Description
	}
	prompt := "Run veto " + command + "?"
	switch m.pendingRequest.ActionID {
	case "opencode":
		if m.pendingRequest.Arguments["subcommand"] == "connect" {
			prompt = "Connect OpenCode to Veto?"
			description = "Veto will configure the installed OpenCode CLI as a runtime. OpenCode credentials are not changed."
		}
	case "hermes":
		if m.pendingRequest.Arguments["operation"] == "install" {
			prompt = "Install or repair the Hermes integration?"
			description = "Veto will manage its embedded Hermes plugin files."
		}
	case "impeccable":
		prompt = "Install Impeccable for Veto?"
		description = "Veto will install curated design skills into its managed integration scope."
	}
	b.WriteString(panelStyle.Render(prompt + "\n" + description))
	if m.pendingNative != nil && m.output.Len() > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("NATIVE DISPATCH DECISION"))
		b.WriteString("\n")
		b.WriteString(workspacePanelStyle.Width(max(12, width)).Render(formatMultiline(m.output.String(), max(10, width-8), 12)))
	}
	if arguments := m.confirmationArguments(action); len(arguments) > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("REQUEST"))
		b.WriteString("\n")
		for _, argument := range arguments {
			b.WriteString(truncate(argument, width-2))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Enter/y confirm · n/Esc cancel"))
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) confirmationArguments(action controlplane.ActionSpec) []string {
	switch m.pendingRequest.ActionID {
	case "opencode":
		if m.pendingRequest.Arguments["subcommand"] == "connect" {
			return []string{"Mode: installed OpenCode CLI"}
		}
	case "hermes":
		if m.pendingRequest.Arguments["operation"] == "install" {
			scope := "Hermes home"
			if m.pendingRequest.Arguments["force"] == "true" {
				scope += " · replace modified Veto plugin files"
			}
			return []string{"Scope: " + scope}
		}
	case "impeccable":
		return []string{"Scope: Veto global skills", "Installer: Impeccable CLI or npx fallback"}
	}
	if len(m.pendingRequest.Arguments) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.pendingRequest.Arguments))
	for key, value := range m.pendingRequest.Arguments {
		if strings.TrimSpace(value) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	arguments := make([]string, 0, len(keys))
	for _, key := range keys {
		value := m.pendingRequest.Arguments[key]
		if confirmationArgumentIsSecret(action, key) {
			value = strings.Repeat("•", max(1, len([]rune(value))))
		}
		arguments = append(arguments, fmt.Sprintf("%s: %s", key, value))
	}
	return arguments
}

func confirmationArgumentIsSecret(action controlplane.ActionSpec, name string) bool {
	for _, field := range action.Flags {
		if field.Name == name {
			return field.Secret
		}
	}
	lower := strings.ToLower(name)
	return strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password")
}

func renderDataTable(headers []string, rows [][]string, width int) string {
	return renderSelectableDataTable(headers, rows, width, -1)
}

func renderSelectableDataTable(headers []string, rows [][]string, width, selected int) string {
	if len(headers) == 0 {
		return ""
	}
	width = max(width, len(headers)*3)
	columnWidths := make([]int, len(headers))
	minimumWidths := make([]int, len(headers))
	for index, header := range headers {
		columnWidths[index] = max(1, lipgloss.Width(header))
		minimumWidths[index] = min(columnWidths[index], 6)
		if header == "" {
			minimumWidths[index] = 1
		}
	}
	for _, row := range rows {
		for index := range headers {
			if index < len(row) {
				columnWidths[index] = max(columnWidths[index], lipgloss.Width(row[index]))
			}
		}
	}
	const gap = 2
	for tableWidth(columnWidths, gap) > width {
		candidate := -1
		candidateSlack := 0
		for index := range columnWidths {
			slack := columnWidths[index] - minimumWidths[index]
			if slack > candidateSlack {
				candidate = index
				candidateSlack = slack
			}
		}
		if candidate < 0 {
			break
		}
		columnWidths[candidate]--
	}
	formatRow := func(cells []string) string {
		parts := make([]string, len(headers))
		for index := range headers {
			cell := ""
			if index < len(cells) {
				cell = cells[index]
			}
			cell = truncate(cell, columnWidths[index])
			if index < len(headers)-1 {
				cell += strings.Repeat(" ", max(0, columnWidths[index]-lipgloss.Width(cell)))
			}
			parts[index] = cell
		}
		return strings.Join(parts, strings.Repeat(" ", gap))
	}
	header := formatRow(headers)
	prefixWidth := 0
	if selected >= 0 {
		prefixWidth = 2
		header = "  " + header
	}
	lines := []string{headerStyle.Render(header), mutedStyle.Render(strings.Repeat("─", min(width, lipgloss.Width(header))))}
	for index, row := range rows {
		line := formatRow(row)
		if selected >= 0 {
			marker := "  "
			if index == selected {
				marker = "› "
			}
			line = marker + truncate(line, max(1, width-prefixWidth))
			if index == selected {
				line = selectedStyle.Render(line)
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func tableWidth(widths []int, gap int) int {
	total := max(0, len(widths)-1) * gap
	for _, width := range widths {
		total += width
	}
	return total
}

func (m *Model) filterRows(rows [][]string) [][]string {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return rows
	}
	filtered := make([][]string, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(strings.ToLower(strings.Join(row, " ")), query) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (m *Model) renderDataFilter(total, visible int) string {
	if !m.dataFilterOpen && strings.TrimSpace(m.dataFilterQuery) == "" {
		return mutedStyle.Render("/ filter")
	}
	query := m.dataFilterQuery
	if query == "" {
		query = "type to filter"
	}
	control := "/ " + query
	if m.dataFilterOpen {
		control += "▌"
		control = selectedStyle.Render(" " + control + " ")
		return control + mutedStyle.Render(fmt.Sprintf("  ·  %d/%d rows  ·  Enter apply  ·  Esc clear", visible, total))
	} else {
		control = optionValueStyle.Render(control)
	}
	return control + mutedStyle.Render(fmt.Sprintf("  ·  %d/%d rows  ·  / edit  ·  Esc clear", visible, total))
}

type fleetDataRow struct {
	harness bool
	detail  []string
	wide    []string
	compact []string
}

func (m *Model) dataPageSize() int {
	height := m.height
	if height < 1 {
		height = 24
	}
	return min(24, max(5, height-25))
}

func (m *Model) dataPageRange(count int) (int, int) {
	if count <= 0 {
		return 0, 0
	}
	cursor := min(max(0, m.currentDataCursor()), count-1)
	size := m.dataPageSize()
	start := (cursor / size) * size
	return start, min(count, start+size)
}

func (m *Model) renderDataPager(count, start, end int) string {
	if count == 0 {
		return mutedStyle.Render("No matching rows")
	}
	pages := (count + m.dataPageSize() - 1) / m.dataPageSize()
	page := start/m.dataPageSize() + 1
	return mutedStyle.Render(fmt.Sprintf("Page %d/%d · rows %d–%d of %d · ↑/↓ select · PgUp/PgDn page · Enter details", page, pages, start+1, end, count))
}

func (m *Model) currentDataCursor() int {
	if m.activeTab() == "INTEGRATIONS" {
		return m.integrationCursor
	}
	if m.activeAction == "plans" {
		return m.plansCursor
	}
	return m.dataCursor
}

func (m *Model) setDataCursor(index int) {
	count := m.dataRowCount()
	if count == 0 {
		index = 0
	} else {
		index = min(max(0, index), count-1)
	}
	if m.activeTab() == "INTEGRATIONS" {
		m.integrationCursor = index
		return
	}
	if m.activeAction == "plans" {
		m.plansCursor = index
		return
	}
	m.dataCursor = index
}

func (m *Model) moveDataCursor(delta int) {
	count := m.dataRowCount()
	if count == 0 {
		return
	}
	m.setDataCursor(m.currentDataCursor() + delta)
	m.status = "Selected · " + m.selectedDataLabel()
}

func (m *Model) providerDataRows() [][]string {
	providers := m.filteredProviders()
	rows := make([][]string, 0, len(providers))
	for _, provider := range providers {
		state := "connected"
		if !provider.Configured {
			state = "not connected"
		}
		capabilities := providerCapabilitiesFor(provider)
		rows = append(rows, []string{provider.Name, state, strconv.Itoa(provider.ModelCount), capabilities.manager, capabilities.tableAction(provider.Configured)})
	}
	return rows
}

type providerCapabilities struct {
	id       string
	manager  string
	connect  bool
	verify   bool
	remove   bool
	external bool
}

func providerCapabilitiesFor(provider controlplane.ProviderSnapshot) providerCapabilities {
	id := canonicalProviderID(provider.Name)
	switch id {
	case "anthropic", "openai", "openrouter", "xai":
		return providerCapabilities{id: id, manager: "Veto credentials", connect: true, verify: true, remove: true}
	case "opencode":
		return providerCapabilities{id: id, manager: "Veto integration", connect: true, remove: true}
	case "local":
		return providerCapabilities{id: id, manager: "Veto models", connect: true}
	case "codex":
		return providerCapabilities{id: id, manager: "Codex CLI", external: true}
	default:
		return providerCapabilities{id: id, manager: "runtime", external: true}
	}
}

func (capabilities providerCapabilities) tableAction(configured bool) string {
	if capabilities.external {
		return "[Enter] Inspect"
	}
	if !configured || capabilities.id == "local" {
		return "[Enter] Connect"
	}
	return "[Enter] Manage"
}

func canonicalProviderID(name string) string {
	value := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(value, "anthropic"):
		return "anthropic"
	case strings.Contains(value, "openrouter"):
		return "openrouter"
	case strings.Contains(value, "openai"):
		return "openai"
	case strings.Contains(value, "xai") || strings.Contains(value, "grok"):
		return "xai"
	case strings.Contains(value, "opencode"):
		return "opencode"
	case strings.Contains(value, "codex"):
		return "codex"
	case strings.Contains(value, "local"):
		return "local"
	default:
		return value
	}
}

func (m *Model) filteredProviders() []controlplane.ProviderSnapshot {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return m.snapshot.Providers
	}
	providers := make([]controlplane.ProviderSnapshot, 0, len(m.snapshot.Providers))
	for _, provider := range m.snapshot.Providers {
		capabilities := providerCapabilitiesFor(provider)
		state := "connected"
		if !provider.Configured {
			state = "not connected"
		}
		searchable := strings.Join([]string{provider.Name, capabilities.id, capabilities.manager, state, strconv.Itoa(provider.ModelCount)}, " ")
		if strings.Contains(strings.ToLower(searchable), query) {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (m *Model) selectedProvider() (controlplane.ProviderSnapshot, providerCapabilities, bool) {
	providers := m.filteredProviders()
	if len(providers) == 0 {
		return controlplane.ProviderSnapshot{}, providerCapabilities{}, false
	}
	provider := providers[min(m.currentDataCursor(), len(providers)-1)]
	return provider, providerCapabilitiesFor(provider), true
}

type historyMission struct {
	key    string
	events []controlplane.HistorySnapshot
}

func historyMissionKey(item controlplane.HistorySnapshot) string {
	if strings.TrimSpace(item.RunID) != "" {
		return "run:" + item.RunID
	}
	if strings.TrimSpace(item.TaskID) != "" {
		return "task:" + item.TaskID
	}
	if strings.TrimSpace(item.EventID) != "" {
		return "event:" + item.EventID
	}
	// Older or synthetic ledger entries may not carry an identifier. Keep
	// those distinct unless their stable fields prove they are the same event.
	return fmt.Sprintf("unkeyed:%d:%s:%s:%s:%s", item.Timestamp.UnixNano(), item.Type, item.Model, item.Runtime, item.Status)
}

func historyMissions(history []controlplane.HistorySnapshot) []historyMission {
	missions := make([]historyMission, 0, len(history))
	indexes := make(map[string]int, len(history))
	for _, item := range history {
		key := historyMissionKey(item)
		index, ok := indexes[key]
		if !ok {
			indexes[key] = len(missions)
			missions = append(missions, historyMission{key: key})
			index = len(missions) - 1
		}
		missions[index].events = append(missions[index].events, item)
	}
	return missions
}

func historyMissionRepresentative(mission historyMission) controlplane.HistorySnapshot {
	if len(mission.events) == 0 {
		return controlplane.HistorySnapshot{}
	}
	for _, event := range mission.events {
		if historyEventFixable(event) {
			return event
		}
	}
	return mission.events[0]
}

func historyMissionModel(mission historyMission) string {
	for _, event := range mission.events {
		if strings.TrimSpace(event.Model) != "" {
			return event.Model
		}
	}
	return ""
}

func historyMissionRuntime(mission historyMission) string {
	for _, event := range mission.events {
		if strings.TrimSpace(event.Runtime) != "" {
			return event.Runtime
		}
	}
	return ""
}

func historyMissionStatus(mission historyMission) string {
	best, bestRank := "", -1
	for _, event := range mission.events {
		status := strings.ToLower(strings.TrimSpace(event.Status))
		if status == "" {
			continue
		}
		rank := 1
		display := event.Status
		switch status {
		case "error", "failed", "fail":
			rank = 5
			display = "error"
		case "success", "completed":
			rank = 4
			display = "success"
		case "running", "started":
			rank = 3
			display = "running"
		case "warn", "warning":
			rank = 2
			display = "warning"
		}
		if rank > bestRank {
			best, bestRank = display, rank
		}
	}
	return best
}

func historyMissionKind(mission historyMission) string {
	event := historyMissionRepresentative(mission)
	kind := ""
	for _, candidate := range mission.events {
		if value := strings.TrimSpace(candidate.TaskKind); value != "" {
			kind = value
			break
		}
	}
	if kind == "" {
		kind = strings.TrimSpace(event.Type)
	}
	if kind == "" {
		kind = "mission"
	}
	kind = strings.ToLower(strings.ReplaceAll(kind, "_", "-"))
	switch {
	case strings.HasPrefix(kind, "route."):
		kind = "routing"
	case strings.HasPrefix(kind, "execution."):
		kind = "execution"
	case strings.HasPrefix(kind, "admission."):
		kind = "admission"
	case strings.HasPrefix(kind, "review."):
		kind = "review"
	case strings.HasPrefix(kind, "tool."):
		kind = "tools"
	case strings.HasPrefix(kind, "artifact."):
		kind = "artifact"
	}
	return kind
}

func historyMissionIdentity(mission historyMission) string {
	event := historyMissionRepresentative(mission)
	identity := ""
	if event.RunID != "" {
		identity = strings.TrimPrefix(event.RunID, "run-")
	} else if event.TaskID != "" {
		identity = strings.TrimPrefix(event.TaskID, "task-")
	}
	if len(identity) > 8 {
		identity = identity[:8]
	}
	return identity
}

func historyMissionLabel(mission historyMission) string {
	event := historyMissionRepresentative(mission)
	if title := strings.TrimSpace(event.MissionTitle); title != "" {
		return title
	}
	parts := []string{historyMissionKind(mission)}
	if !event.Timestamp.IsZero() {
		parts = append(parts, event.Timestamp.Local().Format("2006-01-02 15:04"))
	}
	identity := historyMissionIdentity(mission)
	if identity != "" {
		parts = append(parts, identity)
	}
	return strings.Join(parts, " · ")
}

func historyMissionTableLabel(mission historyMission) string {
	if title := strings.TrimSpace(historyMissionRepresentative(mission).MissionTitle); title != "" {
		return truncate(title, 48)
	}
	parts := []string{historyMissionKind(mission)}
	if identity := historyMissionIdentity(mission); identity != "" {
		parts = append(parts, identity)
	} else if event := historyMissionRepresentative(mission); !event.Timestamp.IsZero() {
		parts = append(parts, event.Timestamp.Local().Format("15:04"))
	}
	return strings.Join(parts, " · ")
}

func historyMissionTime(event controlplane.HistorySnapshot) string {
	if event.Timestamp.IsZero() {
		return "—"
	}
	return event.Timestamp.Local().Format("Jan 02 15:04")
}

func (m *Model) filteredHistoryMissions() []historyMission {
	missions := historyMissions(m.snapshot.History)
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return missions
	}
	filtered := make([]historyMission, 0, len(missions))
	for _, mission := range missions {
		for _, event := range mission.events {
			if historyEventMatchesQuery(event, query) {
				filtered = append(filtered, mission)
				break
			}
		}
	}
	return filtered
}

func (m *Model) historyDataRows() [][]string {
	missions := m.filteredHistoryMissions()
	rows := make([][]string, 0, len(missions))
	for _, mission := range missions {
		if len(mission.events) == 0 {
			continue
		}
		representative := mission.events[0]
		action := "—"
		for _, event := range mission.events {
			if historyEventFixable(event) {
				action = "F diagnose"
				break
			}
		}
		rows = append(rows, []string{
			historyMissionTime(representative),
			historyMissionTableLabel(mission),
			valueOrDash(historyMissionModel(mission)),
			valueOrDash(historyMissionStatus(mission)),
			valueOrDash(historyMissionRuntime(mission)),
			strconv.Itoa(len(mission.events)),
			action,
		})
	}
	return rows
}

func (m *Model) filteredHistory() []controlplane.HistorySnapshot {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return m.snapshot.History
	}
	history := make([]controlplane.HistorySnapshot, 0, len(m.snapshot.History))
	for _, item := range m.snapshot.History {
		if historyEventMatchesQuery(item, query) {
			history = append(history, item)
		}
	}
	return history
}

func historyEventMatchesQuery(item controlplane.HistorySnapshot, query string) bool {
	searchable := strings.Join([]string{
		item.EventID, item.RunID, item.TaskID, item.TaskKind, item.Risk, item.Type,
		item.Model, item.Runtime, item.Status, item.MissionTitle, item.Objective,
		strings.Join(item.Reasons, " "), item.Detail,
	}, " ")
	return strings.Contains(strings.ToLower(searchable), query)
}

func historyEventFixable(item controlplane.HistorySnapshot) bool {
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "warn", "warning", "fail", "failed", "error":
		return true
	}
	switch item.Type {
	case "admission.error", "execution.error", "tool.error", "review.error":
		return true
	default:
		return false
	}
}

func (m *Model) selectedHistoryEvent() (controlplane.HistorySnapshot, bool) {
	if m.activeAction != "history" {
		return controlplane.HistorySnapshot{}, false
	}
	mission, ok := m.selectedHistoryMission()
	if !ok {
		return controlplane.HistorySnapshot{}, false
	}
	if m.dataDetailOpen && len(mission.events) > 0 {
		return mission.events[min(max(0, m.historyDetailCursor), len(mission.events)-1)], true
	}
	return historyMissionRepresentative(mission), true
}

func (m *Model) selectedHistoryMission() (historyMission, bool) {
	if m.activeAction != "history" {
		return historyMission{}, false
	}
	missions := m.filteredHistoryMissions()
	if len(missions) == 0 {
		return historyMission{}, false
	}
	return missions[min(m.currentDataCursor(), len(missions)-1)], true
}

func (m *Model) relatedHistoryForSelectedMission() []controlplane.HistorySnapshot {
	mission, ok := m.selectedHistoryMission()
	if !ok {
		return nil
	}
	return mission.events
}

func (m *Model) openSelectedDataDetail() {
	if m.activeAction == "history" {
		mission, ok := m.selectedHistoryMission()
		if ok {
			representative := historyMissionRepresentative(mission)
			m.historyDetailCursor = 0
			for index, event := range mission.events {
				if event.EventID != "" && event.EventID == representative.EventID {
					m.historyDetailCursor = index
					break
				}
				if event.EventID == "" && event.Timestamp.Equal(representative.Timestamp) && event.Type == representative.Type {
					m.historyDetailCursor = index
					break
				}
			}
		}
	}
	m.dataDetailOpen = true
}

func (m *Model) historyDetailWindowSize() int {
	height := m.height
	if height < 1 {
		height = 24
	}
	return max(5, height-24)
}

func (m *Model) setHistoryDetailCursor(index int) {
	events := m.relatedHistoryForSelectedMission()
	if len(events) == 0 {
		m.historyDetailCursor = 0
		return
	}
	m.historyDetailCursor = min(max(0, index), len(events)-1)
	if event, ok := m.selectedHistoryEvent(); ok {
		m.status = "Inspecting event · " + event.Type
	}
}

func (m *Model) moveHistoryDetailCursor(delta int) {
	m.setHistoryDetailCursor(m.historyDetailCursor + delta)
}

func (m *Model) healthDataRows() [][]string {
	findings := m.filteredHealthFindings()
	rows := make([][]string, 0, len(findings))
	for _, check := range findings {
		action := "—"
		if healthFindingFixable(check) {
			action = healthFindingAIAction(check)
		}
		rows = append(rows, []string{check.Status, check.ID, check.Message, action})
	}
	return rows
}

func healthFindingAIAction(finding controlplane.HealthSnapshot) string {
	if healthFindingInformational(finding.ID, strings.ToUpper(strings.TrimSpace(finding.Status))) {
		return "[F] Explain with AI"
	}
	return "[F] Fix with AI"
}

func healthFindingInformational(id, status string) bool {
	if status != "WARN" && status != "WARNING" {
		return false
	}
	switch id {
	case "build.version", "build.provenance", "release.integrity":
		return true
	default:
		return false
	}
}

func (m *Model) filteredHealthFindings() []controlplane.HealthSnapshot {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return m.snapshot.Health
	}
	findings := make([]controlplane.HealthSnapshot, 0, len(m.snapshot.Health))
	for _, finding := range m.snapshot.Health {
		searchable := strings.Join([]string{finding.Status, finding.ID, finding.Message}, " ")
		if strings.Contains(strings.ToLower(searchable), query) {
			findings = append(findings, finding)
		}
	}
	return findings
}

func healthFindingFixable(finding controlplane.HealthSnapshot) bool {
	switch strings.ToUpper(strings.TrimSpace(finding.Status)) {
	case "WARN", "WARNING", "FAIL", "FAILED", "ERROR":
		return true
	default:
		return false
	}
}

func (m *Model) selectedHealthFinding() (controlplane.HealthSnapshot, bool) {
	if m.activeAction != "doctor" {
		return controlplane.HealthSnapshot{}, false
	}
	findings := m.filteredHealthFindings()
	if len(findings) == 0 {
		return controlplane.HealthSnapshot{}, false
	}
	return findings[min(m.currentDataCursor(), len(findings)-1)], true
}

func (m *Model) selectedHealthFixable() bool {
	finding, ok := m.selectedHealthFinding()
	return ok && healthFindingFixable(finding)
}

func (m *Model) selectedHistoryFixable() bool {
	event, ok := m.selectedHistoryEvent()
	return ok && historyEventFixable(event)
}

func (m *Model) selectedAIFixAvailable() bool {
	return m.selectedHealthFixable() || m.selectedHistoryFixable()
}

func healthFixPrompt(finding controlplane.HealthSnapshot, executable, version string) string {
	if strings.TrimSpace(executable) == "" {
		executable = "unknown (identify the process executable before diagnosing)"
	}
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	requiredOutcome := "Resolve the finding when a safe fix exists."
	if strings.Contains(healthFindingAIAction(finding), "Explain") {
		requiredOutcome = "This is normally an informational build-state warning. Explain it and present safe options; do not claim it was fixed unless this exact running binary's refreshed check becomes PASS."
	}
	return fmt.Sprintf(`Investigate and resolve this Veto health finding.

Check: %s
Status: %s
Message: %s
Subject executable: %s
Subject TUI version: %s
Required outcome: %s

Work from evidence:
1. Reproduce the finding against the subject executable above and identify its root cause before changing anything. Do not substitute the first "veto" found on PATH unless it is the same file.
2. If this is a local installation or configuration problem, make the smallest safe fix and verify that the health check passes.
3. If the root cause is a defect in Veto, read CONTRIBUTING.md, search existing issues in oleg-koval/veto, and create a structured GitHub bug report only when no duplicate exists. Include the command, Veto version, OS, expected behavior, actual behavior, reproduction steps, scope, and explicit acceptance criteria.
4. Redact credentials, provider responses, terminal history, and all private ~/.veto contents. Do not file an issue when the problem is environmental or cannot be reproduced.
5. Treat the TUI's post-run health refresh as authoritative. Report what you changed, evidence for this exact executable, and any GitHub issue URL; never declare success from a different installation.`, finding.ID, finding.Status, finding.Message, executable, version, requiredOutcome)
}

func (m *Model) openSelectedHealthFix() {
	finding, ok := m.selectedHealthFinding()
	if !ok || !healthFindingFixable(finding) {
		return
	}
	m.dataDetailOpen = false
	m.openAIFixComposer(healthFixPrompt(finding, m.options.Executable, displayVersion(m.options.Version)))
	m.pendingHealthFix = finding.ID
	m.status = strings.TrimPrefix(healthFindingAIAction(finding), "[F] ") + " · reviewable agent mission"
}

func historyFixPrompt(event controlplane.HistorySnapshot, related []controlplane.HistorySnapshot) string {
	var timeline strings.Builder
	for index, item := range related {
		if index == 12 {
			fmt.Fprintf(&timeline, "\n- ... %d earlier events omitted", len(related)-index)
			break
		}
		fmt.Fprintf(&timeline, "\n- %s %s", item.Timestamp.UTC().Format(time.RFC3339), item.Type)
		if item.Model != "" {
			fmt.Fprintf(&timeline, " model=%s", item.Model)
		}
		if item.Runtime != "" {
			fmt.Fprintf(&timeline, " harness=%s", item.Runtime)
		}
		if item.Status != "" {
			fmt.Fprintf(&timeline, " status=%s", item.Status)
		}
		if item.Detail != "" {
			fmt.Fprintf(&timeline, " detail=%s", item.Detail)
		}
	}
	return fmt.Sprintf(`Diagnose this failed Veto mission event and resolve its root cause.

Event: %s
Status: %s
Time: %s
Run ID: %s
Task ID: %s
Task kind: %s
Risk: %s
Model: %s
Harness: %s
Recorded detail: %s
Reasons: %s

Related redacted run events:%s

Work from evidence:
1. Reproduce the failure and inspect the relevant local code and diagnostics. The ledger intentionally does not store the objective, prompt, or model response; do not infer missing private data.
2. If this is a local installation or configuration problem, make the smallest safe fix and verify it.
3. If the root cause is a defect in Veto, read CONTRIBUTING.md, search existing issues in oleg-koval/veto, and create a structured GitHub bug report only when no duplicate exists. Include the command, Veto version, OS, expected behavior, actual behavior, reproduction steps, scope, and explicit acceptance criteria.
4. Redact credentials, provider responses, terminal history, and all private ~/.veto contents. Do not file an issue when the problem is environmental or cannot be reproduced.
5. Report the fix, verification evidence, and any GitHub issue URL.`,
		event.Type, valueOrDash(event.Status), event.Timestamp.UTC().Format(time.RFC3339), valueOrDash(event.RunID),
		valueOrDash(event.TaskID), valueOrDash(event.TaskKind), valueOrDash(event.Risk), valueOrDash(event.Model),
		valueOrDash(event.Runtime), valueOrDash(event.Detail), valueOrDash(strings.Join(event.Reasons, ", ")), timeline.String())
}

func (m *Model) relatedHistory(event controlplane.HistorySnapshot) []controlplane.HistorySnapshot {
	key := historyMissionKey(event)
	related := make([]controlplane.HistorySnapshot, 0)
	for _, candidate := range m.snapshot.History {
		if historyMissionKey(candidate) == key {
			related = append(related, candidate)
		}
	}
	if len(related) == 0 {
		return []controlplane.HistorySnapshot{event}
	}
	return related
}

func (m *Model) activateHistoryDetailAt(x, y int) {
	events := m.relatedHistoryForSelectedMission()
	if len(events) == 0 {
		return
	}
	width := m.width
	if width < 1 {
		width = 80
	}
	height := m.height
	if height < 1 {
		height = 24
	}
	panelWidth := min(max(36, width-10), 84)
	panel, ok := m.historyDetailPanel(panelWidth)
	if !ok {
		return
	}
	panelX, panelY := modalPanelOrigin(panel, width, height)
	if x < panelX || x >= panelX+lipgloss.Width(panel) {
		return
	}
	lines := strings.Split(ansi.Strip(panel), "\n")
	headerLine := -1
	for index, line := range lines {
		if strings.Contains(line, "MISSION TIMELINE") {
			headerLine = index
			break
		}
	}
	if headerLine < 0 {
		return
	}
	lineIndex := y - panelY
	eventIndex := lineIndex - headerLine - 1
	windowSize := min(len(events), m.historyDetailWindowSize())
	start := min(max(0, m.historyDetailCursor-windowSize+1), max(0, len(events)-windowSize))
	if eventIndex < 0 || eventIndex >= windowSize {
		return
	}
	index := start + eventIndex
	if index < 0 || index >= len(events) {
		return
	}
	m.historyDetailCursor = index
	m.status = "Inspecting event · " + events[index].Type
}

func (m *Model) openSelectedAIFix() {
	if finding, ok := m.selectedHealthFinding(); ok && healthFindingFixable(finding) {
		m.openSelectedHealthFix()
		return
	}
	event, ok := m.selectedHistoryEvent()
	if !ok || !historyEventFixable(event) {
		return
	}
	m.dataDetailOpen = false
	m.openAIFixComposer(historyFixPrompt(event, m.relatedHistory(event)))
	m.status = "Diagnose with AI · executable repair mission"
}

func (m *Model) integrationDataRows() [][]string {
	integrations := m.filteredIntegrations()
	rows := make([][]string, 0, len(integrations))
	for _, integration := range integrations {
		rows = append(rows, []string{integration.Name, integration.Status, integration.Detail, integrationActionLabel(integration.PrimaryAction)})
	}
	return rows
}

func integrationActionLabel(action string) string {
	switch action {
	case "connect":
		return "[Enter] Connect"
	case "repair":
		return "[Enter] Repair"
	case "install":
		return "[Enter] Install"
	case "status":
		return "[Enter] Check"
	default:
		return "—"
	}
}

func (m *Model) filteredPlans() []controlplane.PlanSnapshot {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	if query == "" {
		return m.snapshot.Plans
	}
	plans := make([]controlplane.PlanSnapshot, 0, len(m.snapshot.Plans))
	for _, plan := range m.snapshot.Plans {
		if strings.Contains(strings.ToLower(plan.Name), query) {
			plans = append(plans, plan)
		}
	}
	return plans
}

func (m *Model) planDataRows() [][]string {
	plans := m.filteredPlans()
	rows := make([][]string, 0, len(plans))
	for _, plan := range plans {
		rows = append(rows, []string{plan.Name})
	}
	return rows
}

func (m *Model) fleetDataRows() []fleetDataRow {
	rows := make([]fleetDataRow, 0, len(m.snapshot.Models))
	for _, model := range m.snapshot.Models {
		contextTokens := "unknown"
		if model.ContextTokens > 0 {
			contextTokens = strconv.Itoa(model.ContextTokens)
		}
		policy := "normal"
		if model.Excluded {
			policy = "excluded"
		} else if model.Pinned {
			policy = "pinned"
		} else if model.Favorite {
			policy = "favorite"
		}
		status := valueOrDefault(model.Status, "unknown")
		if model.Kind == controlplane.ModelKindHarness {
			cells := []string{runtimeLabel(model.Runtime), model.Name, "host-selected", status, policy}
			rows = append(rows, fleetDataRow{harness: true, detail: cells, wide: cells, compact: cells})
			continue
		}
		tools := "unknown"
		if model.ToolsKnown {
			tools = strings.Join(model.Tools, ",")
			if tools == "" {
				tools = "none"
			}
		}
		modelID := valueOrDefault(model.ModelID, model.Name)
		inputCost := modelCost(model.CostPer1kInputUSD, model.CostPer1kInputKnown)
		outputCost := modelCost(model.CostPer1kOutputUSD, model.CostPer1kOutputKnown)
		detail := []string{model.Name, modelID, model.Provider, runtimeLabel(model.Runtime), model.Tier, contextTokens, tools, inputCost, outputCost, status, policy}
		wide := []string{model.Name, modelID, model.Provider, runtimeLabel(model.Runtime), model.Tier, contextTokens, inputCost + " / " + outputCost, status, policy}
		compact := []string{wide[0], wide[1], wide[2], wide[4], wide[7], wide[8]}
		rows = append(rows, fleetDataRow{detail: detail, wide: wide, compact: compact})
	}
	return rows
}

func (m *Model) filteredFleetDataRows() []fleetDataRow {
	query := strings.ToLower(strings.TrimSpace(m.dataFilterQuery))
	rows := m.fleetDataRows()
	if query == "" {
		return rows
	}
	filtered := make([]fleetDataRow, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(strings.ToLower(strings.Join(row.detail, " ")), query) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (m *Model) dataRowsAndHeaders() ([]string, [][]string) {
	switch m.activeAction {
	case "models":
		rows := m.filteredFleetDataRows()
		if len(rows) == 0 {
			return nil, nil
		}
		cursor := min(m.currentDataCursor(), len(rows)-1)
		if rows[cursor].harness {
			return []string{"Harness", "Route", "Model", "Status", "Policy"}, [][]string{rows[cursor].detail}
		}
		return []string{"Route", "Model ID", "Provider", "Runtime", "Tier", "Context", "Tools", "Input", "Output", "Status", "Policy"}, [][]string{rows[cursor].detail}
	case "providers":
		return []string{"Provider", "State", "Models", "Managed by", "Action"}, m.providerDataRows()
	case "history":
		return []string{"Time", "Mission", "Model", "Status", "Harness", "Events", "Action"}, m.historyDataRows()
	case "plans":
		return []string{"Plan"}, m.planDataRows()
	case "doctor":
		return []string{"Status", "Check", "Message", "Action"}, m.healthDataRows()
	case "integrations", "opencode", "hermes", "impeccable":
		return []string{"Integration", "Status", "Details", "Action"}, m.integrationDataRows()
	default:
		return nil, nil
	}
}

func (m *Model) dataRowCount() int {
	if m.activeAction == "models" {
		return len(m.filteredFleetDataRows())
	}
	_, rows := m.dataRowsAndHeaders()
	return len(rows)
}

func (m *Model) selectedData() ([]string, []string, bool) {
	headers, rows := m.dataRowsAndHeaders()
	if len(rows) == 0 {
		return nil, nil, false
	}
	if m.activeAction == "models" {
		return headers, rows[0], true
	}
	index := min(m.currentDataCursor(), len(rows)-1)
	return headers, rows[index], true
}

func (m *Model) selectedDataLabel() string {
	_, row, ok := m.selectedData()
	if !ok || len(row) == 0 {
		return "no row"
	}
	if (m.activeAction == "history" || m.activeAction == "doctor") && len(row) > 1 {
		return row[1]
	}
	for _, cell := range row {
		if value := strings.TrimSpace(cell); value != "" && value != "—" {
			return value
		}
	}
	return "row"
}

func formatMultiline(value string, width, limit int) string {
	// Provider/model text is untrusted: strip escape sequences before it
	// reaches the terminal so a malicious response can't spoof the screen
	// or trigger clipboard/OSC side effects.
	value = strings.TrimSpace(ansi.Strip(value))
	if value == "" {
		return mutedStyle.Render("No output returned.")
	}
	input := strings.Split(value, "\n")
	lines := make([]string, 0, min(len(input), limit))
	for _, line := range input {
		if len(lines) == limit {
			break
		}
		lines = append(lines, truncate(strings.TrimRight(line, " \t\r"), width))
	}
	if len(input) > limit {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("… %d more lines", len(input)-limit)))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderModels(width int) string {
	if len(m.snapshot.Models) == 0 {
		return workspacePanelStyle.Width(max(12, width)).Render(mutedStyle.Render("No configured models. Run veto login or open Providers."))
	}
	headers := []string{"Route", "Model ID", "Provider", "Tier", "Status", "Policy"}
	if width >= 118 {
		headers = []string{"Route", "Model ID", "Provider", "Runtime", "Tier", "Context", "Input / Output", "Status", "Policy"}
	}
	allRows := m.fleetDataRows()
	rows := m.filteredFleetDataRows()
	start, end := m.dataPageRange(len(rows))
	page := rows[start:end]
	modelRows := make([][]string, 0, len(page))
	harnessRows := make([][]string, 0, len(page))
	modelSelected, harnessSelected := -1, -1
	for index, row := range page {
		if row.harness {
			if start+index == m.currentDataCursor() {
				harnessSelected = len(harnessRows)
			}
			harnessRows = append(harnessRows, row.wide)
			continue
		}
		if start+index == m.currentDataCursor() {
			modelSelected = len(modelRows)
		}
		if width >= 118 {
			modelRows = append(modelRows, row.wide)
		} else {
			modelRows = append(modelRows, row.compact)
		}
	}
	content := headerStyle.Render("FLEET · MODEL CATALOG") + "\n" + m.renderDataFilter(len(allRows), len(rows)) + "\n\n" + renderSelectableDataTable(headers, modelRows, max(20, width-8), modelSelected)
	if len(harnessRows) > 0 {
		content += "\n\n" + headerStyle.Render("HARNESSES · MODEL SELECTED BY HOST") + "\n\n"
		content += renderSelectableDataTable([]string{"Harness", "Route", "Model", "Status", "Policy"}, harnessRows, max(20, width-8), harnessSelected)
	}
	content += "\n\n" + m.renderDataPager(len(rows), start, end)
	content += "\n" + mutedStyle.Render("P Providers · A Add provider · R refresh models")
	return workspacePanelStyle.Width(max(12, width)).Render(content)
}

func runtimeLabel(runtime string) string {
	switch runtime {
	case "codex-cli":
		return "Codex CLI"
	case "claude-cli":
		return "Claude CLI"
	case "openai-api":
		return "OpenAI API"
	case "openrouter-api":
		return "OpenRouter API"
	case "opencode":
		return "OpenCode"
	case "openai-compatible":
		return "Local API"
	default:
		return runtime
	}
}

func modelCost(value float64, known bool) string {
	if !known {
		return "unknown"
	}
	return fmt.Sprintf("$%.4f", value)
}

func (m *Model) renderProviders(width int) string {
	if len(m.snapshot.Providers) == 0 {
		return workspacePanelStyle.Width(max(12, width)).Render(mutedStyle.Render("No providers configured. Press A to connect one securely."))
	}
	rows := m.providerDataRows()
	total := len(m.snapshot.Providers)
	start, end := m.dataPageRange(len(rows))
	page := rows[start:end]
	selected := -1
	if len(page) > 0 {
		selected = m.currentDataCursor() - start
	}
	content := headerStyle.Render("FLEET · PROVIDERS") + "\n" + mutedStyle.Render("Connect, update, verify, or remove providers without leaving Veto.") + "\n" + m.renderDataFilter(total, len(rows)) + "\n\n" + renderSelectableDataTable([]string{"Provider", "State", "Models", "Managed by", "Action"}, page, max(20, width-8), selected) + "\n\n" + m.renderDataPager(len(rows), start, end)
	content += "\n" + mutedStyle.Render("M Models · A Add provider · R refresh · Enter manage")
	if health := m.renderProviderHealth(); health != "" {
		content += "\n\n" + health
	}
	if m.output.Len() > 0 {
		content += "\n\n" + headerStyle.Render("LAST PROVIDER ACTION") + "\n" + formatMultiline(m.output.String(), max(20, width-8), 8)
	}
	return workspacePanelStyle.Width(max(12, width)).Render(content)
}

func (m *Model) renderProviderHealth() string {
	var lines []string
	for _, provider := range m.snapshot.Providers {
		if !provider.Installed && provider.Auth == "" && provider.Billing == "" && !provider.Unavailable && provider.Warning == "" {
			continue
		}
		line := fmt.Sprintf("%-12s auth=%s billing=%s", provider.Name, valueOrDash(provider.Auth), valueOrDash(provider.Billing))
		if provider.Unavailable {
			line += " · temporarily unavailable"
		}
		if provider.Warning != "" {
			line += " · " + provider.Warning
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return headerStyle.Render("PROVIDER HEALTH") + "\n" + strings.Join(lines, "\n")
}

func (m *Model) renderNativeStatus(width int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("NATIVE DISPATCH STATUS"))
	b.WriteByte('\n')
	b.WriteString("default agent: not configured (task-kind policy)\n")
	for _, provider := range m.snapshot.Providers {
		if provider.Name != "Claude" && provider.Name != "Codex" {
			continue
		}
		state := "available"
		if !provider.Installed {
			state = "executable missing"
		} else if provider.Unavailable {
			state = "temporarily unavailable"
		}
		auth := provider.Auth
		if auth == "" {
			auth = "unknown"
		}
		billing := provider.Billing
		if billing == "" {
			billing = "unknown"
		}
		b.WriteString(truncate(fmt.Sprintf("%-7s %-24s auth=%s billing=%s", provider.Name, state, auth, billing), width-2))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *Model) renderHistory(width int) string {
	if len(m.snapshot.History) == 0 {
		return workspacePanelStyle.Width(max(12, width)).Render(mutedStyle.Render("No redacted activity yet. Completed routes and runs appear here."))
	}
	rows := m.historyDataRows()
	total := len(historyMissions(m.snapshot.History))
	start, end := m.dataPageRange(len(rows))
	page := rows[start:end]
	selected := -1
	if len(page) > 0 {
		selected = m.currentDataCursor() - start
	}
	content := headerStyle.Render("MISSIONS · RECENT ACTIVITY") + "\n" + m.renderDataFilter(total, len(rows)) + "\n\n" + renderSelectableDataTable([]string{"Time", "Mission", "Model", "Status", "Harness", "Events", "Action"}, page, max(20, width-8), selected) + "\n\n" + m.renderDataPager(len(rows), start, end)
	content += "\n" + mutedStyle.Render("One row per mission/run · Enter opens its full timeline · failures: press F to diagnose with AI")
	return workspacePanelStyle.Width(max(12, width)).Render(content)
}

func (m *Model) renderPlans(width int) string {
	if len(m.snapshot.Plans) == 0 {
		return workspacePanelStyle.Width(max(12, width)).Render(mutedStyle.Render("No plans found. Create a plan under ~/.veto/plans to execute it here."))
	}
	rows := m.planDataRows()
	start, end := m.dataPageRange(len(rows))
	page := rows[start:end]
	selected := -1
	if len(page) > 0 {
		selected = m.plansCursor - start
	}
	content := headerStyle.Render("MISSIONS · SAVED PLANS") + "\n" + m.renderDataFilter(len(m.snapshot.Plans), len(rows)) + "\n\n"
	if len(rows) == 0 {
		content += mutedStyle.Render("No saved plans match this filter.")
	} else {
		content += renderSelectableDataTable([]string{"Plan"}, page, max(20, width-8), selected)
		content += "\n\n" + m.renderDataPager(len(rows), start, end)
		content += "\n" + mutedStyle.Render("Enter opens the selected plan for execution")
	}
	return workspacePanelStyle.Width(max(12, width)).Render(content)
}

func (m *Model) renderHealth(width int) string {
	if m.healthLoading {
		pulse := "◆"
		if m.options.Motion {
			frames := []rune(runningSpinner)
			pulse = string(frames[int(m.frame)%len(frames)])
		}
		content := headerStyle.Render("HEALTH · RUNNING DIAGNOSTICS") + "\n\n" +
			titleStyle.Render(pulse+"  Checking Veto, configured runtimes, state, and release integrity…") + "\n\n" +
			mutedStyle.Render("Previous results are hidden until this diagnostic run completes.\nEsc cancels · results refresh automatically when finished.")
		return workspacePanelStyle.Width(max(12, width)).Render(content)
	}
	if len(m.snapshot.Health) == 0 {
		return workspacePanelStyle.Width(max(12, width)).Render(headerStyle.Render("HEALTH · SAFE DIAGNOSTICS") + "\n\n" + mutedStyle.Render("No health findings returned. Press R to run diagnostics again."))
	}
	rows := m.healthDataRows()
	total := len(m.snapshot.Health)
	start, end := m.dataPageRange(len(rows))
	page := rows[start:end]
	selected := -1
	if len(page) > 0 {
		selected = m.currentDataCursor() - start
	}
	content := headerStyle.Render("HEALTH · SAFE DIAGNOSTICS") + "  " + titleStyle.Render("[R] RUN AGAIN") + "\n" + m.renderDataFilter(total, len(rows)) + "\n\n" + renderSelectableDataTable([]string{"Status", "Check", "Message", "Action"}, page, max(20, width-8), selected) + "\n\n" + m.renderDataPager(len(rows), start, end)
	content += "\n" + mutedStyle.Render("R refreshes all checks · warnings and failures: select a row, then press F or open details")
	if m.healthVerification != "" {
		content += "\n\n" + headerStyle.Render("AUTHORITATIVE HEALTH VERIFICATION") + "\n" + m.healthVerification
	}
	return workspacePanelStyle.Width(max(12, width)).Render(content)
}

func (m *Model) renderAnalytics(width int) string {
	analytics := m.snapshot.Analytics
	rows := [][]string{
		{"Local collection", strconv.FormatBool(analytics.LocalCollection)},
		{"Local path", analytics.LocalPath},
		{"Retention", fmt.Sprintf("%d days", analytics.RetentionDays)},
		{"Future remote sharing", analytics.RemoteSharing},
		{"Remote transport active", strconv.FormatBool(analytics.RemoteTransportActive)},
	}
	content := headerStyle.Render("ANALYTICS & DATA") + "\n\n" + renderDataTable([]string{"Setting", "Value"}, rows, max(20, width-8)) + "\n\n" + mutedStyle.Render("Remote analytics are not active; preference changes remain explicit.")
	return workspacePanelStyle.Width(max(12, width)).Render(content)
}

func (m *Model) renderIntegrations(width int) string {
	integrations := m.filteredIntegrations()
	start, end := m.dataPageRange(len(integrations))
	content := headerStyle.Render("INTEGRATIONS") + "\n" + m.renderDataFilter(len(m.snapshot.Integrations), len(integrations)) + "\n\n"
	if len(m.snapshot.Integrations) == 0 {
		content += mutedStyle.Render("No integrations detected. Use the OpenCode or Hermes command for setup.")
	} else if len(integrations) == 0 {
		content += mutedStyle.Render("No integrations match this filter.")
	} else {
		page := integrations[start:end]
		rows := make([][]string, 0, len(page))
		for _, integration := range page {
			rows = append(rows, []string{integration.Name, integration.Status, integration.Detail, integrationActionLabel(integration.PrimaryAction)})
		}
		content += renderSelectableDataTable([]string{"Integration", "Status", "Details", "Action"}, rows, max(20, width-8), m.integrationCursor-start)
		pager := strings.Replace(m.renderDataPager(len(integrations), start, end), "Enter details", "Enter action", 1)
		content += "\n\n" + pager
		content += "\n" + mutedStyle.Render("Enter performs the selected action through Veto · changes require confirmation")
	}
	if strings.TrimSpace(m.output.String()) != "" && (m.outputAction == "" || m.outputAction == m.activeAction) {
		label := m.activeAction
		if label == "integrations" {
			label = "integration"
		}
		content += "\n\n" + headerStyle.Render(strings.ToUpper(label)+" RESULT") + "\n" + formatMultiline(m.output.String(), max(20, width-8), 10)
	}
	return workspacePanelStyle.Width(max(12, width)).Render(content)
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
		latency = (time.Duration(monitor.LatencyMs) * time.Millisecond).Round(time.Millisecond).String()
	}
	cost := "unknown"
	if monitor.CostKnown {
		cost = fmt.Sprintf("$%.4f", monitor.CostUSD)
	}
	inputTokens, reusedTokens, freshTokens, outputTokens, totalTokens := "unknown", "unknown", "unknown", "unknown", "unknown"
	if monitor.TokensKnown {
		inputTokens = formatCount(monitor.InputTokens)
		outputTokens = formatCount(monitor.OutputTokens)
		totalTokens = formatCount(monitor.TotalTokens)
		if monitor.CachedInputKnown {
			reusedTokens = formatCount(monitor.CachedInputTokens)
			freshTokens = formatCount(max(0, monitor.InputTokens-monitor.CachedInputTokens))
		}
	}
	routeName := monitor.LastModel
	if routeName == "" {
		routeName = m.snapshot.Model
	}
	if m.decisionModel != "" {
		routeName = m.decisionModel
	}
	modelName := valueOrDash(routeName)
	harness := "—"
	capabilities := "unknown"
	if routeName != "" {
		for _, model := range m.snapshot.Models {
			if model.Name != routeName {
				continue
			}
			harness = valueOrDash(runtimeLabel(model.Runtime))
			if model.Kind == controlplane.ModelKindHarness || model.Runtime == "codex-cli" {
				modelName = "host-selected"
			}
			if model.ToolsKnown {
				capabilities = fmt.Sprintf("known (%d tools)", len(model.Tools))
			} else {
				capabilities = "unknown"
			}
			break
		}
		if strings.EqualFold(routeName, "codex") && harness == "—" {
			modelName = "host-selected"
			harness = "Codex CLI"
		}
		if harness == "—" && monitor.LastRuntime != "" {
			harness = runtimeLabel(monitor.LastRuntime)
		}
	}
	confidence := m.decisionConf
	if confidence == "" && monitor.LastConfidenceKnown {
		confidence = fmt.Sprintf("%.0f%%", monitor.LastConfidence*100)
	}
	connectedProviders := 0
	for _, provider := range m.snapshot.Providers {
		if provider.Configured {
			connectedProviders++
		}
	}
	b.WriteString(mutedStyle.Render(fmt.Sprintf("providers    %d/%d connected\nlast model   %s\nlast harness %s\nroute conf   %s\ncapability   %s\nactive runs  %d\nactive tools %d\napprovals    %d\nexec gross   %s\nexec reused  %s\nexec fresh   %s\nexec output  %s\nexec total   %s\nlast cost    %s\nlast time    %s\nartifacts    %d", connectedProviders, len(m.snapshot.Providers), modelName, harness, valueOrDash(confidence), capabilities, monitor.ActiveSessions, monitor.ActiveTools, monitor.PendingApprovals, inputTokens, reusedTokens, freshTokens, outputTokens, totalTokens, cost, latency, monitor.Artifacts)))
	b.WriteString("\n\n")
	whyTitle := "WHY THIS MODEL"
	if routeName == "" {
		whyTitle = "ROUTING EXPLANATION"
	}
	b.WriteString(headerStyle.Render(whyTitle))
	b.WriteString("\n")
	why := m.decisionWhy
	if why == "" {
		if routeName == "" {
			why = "No model selected yet. Start a mission to see Veto's decision."
		} else {
			why = "No explanation was recorded for the last route."
		}
	}
	b.WriteString(mutedStyle.Render(truncate(why, width-2)))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("Ctrl+F Fleet · P Providers · Tab views · Enter open · Esc home · Ctrl+K commands"))
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (m *Model) renderInspectorPanel(width int) string {
	content := m.renderInspector(max(12, width-2))
	return inspectorStyle.Width(max(12, width)).Render(content)
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
	if m.running {
		b.WriteString(m.renderRoutingAnimation(width))
		b.WriteString("\n\n")
	}
	if len(m.eventHistory) == 0 {
		if m.running {
			b.WriteString(mutedStyle.Render("Starting the routing control plane…"))
		} else {
			b.WriteString(mutedStyle.Render("No mission in flight. Describe an objective above; Veto will show filter, shortlist, admission, winner, execution, and review here."))
		}
		return b.String()
	}
	visible := 16
	if m.height > 0 {
		visible = max(8, min(24, m.height/3))
	}
	start := max(0, len(m.eventHistory)-visible)
	for _, event := range m.eventHistory[start:] {
		message := strings.TrimSpace(event.Message)
		if event.Kind == "output" {
			message = fmt.Sprintf("received · %d characters", len(message))
		} else if index := strings.IndexByte(message, '\n'); index >= 0 {
			message = message[:index]
		}
		line := fmt.Sprintf("• %-22s %s", eventStage(event.Kind), message)
		b.WriteString(truncate(line, width))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

type routingCandidate struct {
	name  string
	state string
}

func (m *Model) renderRoutingAnimation(width int) string {
	stageIndex := m.routingActiveStage()
	stage := routingStages[stageIndex]
	frames := []rune(runningSpinner)
	pulse := "◆"
	if m.options.Motion {
		pulse = string(frames[int(m.frame)%len(frames)])
	}

	headline := pulse + "  " + stage + " IN PROGRESS"
	if stage == "ADMIT" {
		headline += " · candidates are asked in ranked order"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate(headline, width)))
	b.WriteString("\n")
	b.WriteString(m.renderRoutingSweep(width))

	candidates := m.routingCandidates()
	if len(candidates) == 0 {
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("CANDIDATE ADMISSION"))
	for _, candidate := range candidates {
		marker := "○"
		state := candidate.state
		switch state {
		case "evaluating":
			marker = pulse
		case "selected":
			marker = "◆"
		case "rejected":
			marker = "×"
		case "unavailable":
			marker = "!"
		}
		line := fmt.Sprintf("\n  %-18s %s %s", truncate(candidate.name, 18), marker, state)
		if state == "evaluating" || state == "selected" {
			b.WriteString(selectedStyle.Render(truncate(line, width)))
		} else {
			b.WriteString(mutedStyle.Render(truncate(line, width)))
		}
	}
	return b.String()
}

func (m *Model) routingActiveStage() int {
	active := 0
	for _, event := range m.eventHistory {
		switch event.Kind {
		case "route.filter_pass", "route.filter_fail", "route.filtering":
			active = max(active, 0)
		case "route.shortlist", "route.ask_start", "route.ask_reject", "route.ask_error":
			active = max(active, 2)
		case "route.ask_accept", "route.completed":
			active = max(active, 3)
		case "execution.started", "runtime.tool.started", "runtime.tool.completed", "runtime.tool.error", "output", "output.saved":
			active = max(active, 4)
		case "review.started", "review.completed", "review.error":
			active = max(active, 5)
		}
	}
	return min(active, len(routingStages)-1)
}

func (m *Model) renderRoutingSweep(width int) string {
	trackWidth := min(48, max(12, width-2))
	position := trackWidth / 2
	if m.options.Motion && trackWidth > 1 {
		period := 2*trackWidth - 2
		position = int(m.frame) % period
		if position >= trackWidth {
			position = period - position
		}
	}
	left := strings.Repeat("━", position)
	right := strings.Repeat("─", trackWidth-position-1)
	return brandStyle.Render("╶"+left) + titleStyle.Render("◆") + mutedStyle.Render(right+"╴")
}

func (m *Model) routingCandidates() []routingCandidate {
	candidates := make([]routingCandidate, 0, 4)
	indexes := make(map[string]int)
	for _, event := range m.eventHistory {
		state := ""
		switch event.Kind {
		case "route.ask_start":
			state = "evaluating"
		case "route.ask_accept", "route.completed":
			state = "selected"
		case "route.ask_reject":
			state = "rejected"
		case "route.ask_error":
			state = "unavailable"
		}
		name := strings.TrimSpace(event.Model)
		if state == "" || name == "" {
			continue
		}
		if index, ok := indexes[name]; ok {
			candidates[index].state = state
			continue
		}
		indexes[name] = len(candidates)
		candidates = append(candidates, routingCandidate{name: name, state: state})
	}
	if len(candidates) > 4 {
		candidates = candidates[len(candidates)-4:]
	}
	return candidates
}

func formatCount(value int) string {
	digits := strconv.Itoa(value)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return digits
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
	case "shortlist":
		return "shortlist"
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
	if m.running && m.options.Motion {
		frames := []rune(runningSpinner)
		pulse = string(frames[int(m.frame)%len(frames)])
	} else if m.options.Motion && m.frame%2 == 1 {
		pulse = "•"
	}
	status := fmt.Sprintf(" %s  %s", pulse, m.status)
	hints := "Tab views  ·  Esc home  ·  Ctrl+K commands  ·  q quit "
	if m.isHome() {
		hints = "Enter compose  ·  Tab views  ·  Ctrl+K commands  ·  ? help "
	} else if m.activeAction == "" && (len(m.eventHistory) > 0 || m.output.Len() > 0) {
		hints = "c clear result  ·  Tab views  ·  Ctrl+K commands  ·  q quit "
	}
	if m.supportsDataFilter() {
		hints = "↑↓ rows  ·  PgUp/PgDn pages  ·  / filter  ·  Esc home "
		if m.dataFilterQuery != "" {
			hints = "↑↓ rows  ·  PgUp/PgDn pages  ·  / edit filter  ·  Esc clear "
		}
	}
	if m.running {
		hints = "Esc cancel  ·  ? help  ·  q quit "
	}
	if width < 64 {
		hints = "Tab views · Esc home · / commands "
		if m.isHome() {
			hints = "Enter compose · Tab views · / commands "
		} else if m.activeAction == "" && (len(m.eventHistory) > 0 || m.output.Len() > 0) {
			hints = "c clear · Tab views · / commands "
		}
		if m.running {
			hints = "Esc cancel · ? help · q quit "
		} else if m.supportsDataFilter() {
			hints = "↑↓ rows · PgUp/PgDn · / filter "
		}
	}
	available := max(1, width-lipgloss.Width(hints)-1)
	status = truncate(status, available)
	return statusStyle.Width(width).Render(status + strings.Repeat(" ", max(1, width-lipgloss.Width(status)-lipgloss.Width(hints))) + hints)
}

func (m *Model) renderPalette(_ string) string {
	filtered := m.filteredActions()
	width := m.width
	if width < 1 {
		width = 80
	}
	panelWidth := min(max(20, width-8), 84)
	if panelWidth > max(1, width-6) {
		panelWidth = max(1, width-6)
	}
	rowWidth := max(20, panelWidth-6)
	if rowWidth > panelWidth {
		rowWidth = panelWidth
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Command palette"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Type to filter · Enter open · Esc close"))
	b.WriteString("\n\n")
	query := m.paletteQuery
	if query == "" {
		query = mutedStyle.Render("type a command or description")
	}
	b.WriteString(panelStyle.Width(rowWidth).Render("/ " + query))
	b.WriteString("\n\n")
	start, end := m.paletteRange(len(filtered))
	visible := end - start
	b.WriteString(headerStyle.Render(fmt.Sprintf("%d command%s · showing %d", len(filtered), pluralSuffix(len(filtered)), visible)))
	b.WriteString("\n")
	if start > 0 {
		b.WriteString(mutedStyle.Render("  ↑ more above"))
		b.WriteByte('\n')
	}
	for index := start; index < end; index++ {
		action := filtered[index]
		line := truncate("▸ "+action.Command+"  "+action.Description, rowWidth)
		if index != m.paletteCursor {
			line = "  " + strings.TrimPrefix(line, "▸ ")
		}
		if index == m.paletteCursor {
			line = selectedStyle.Width(rowWidth).Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(filtered) {
		b.WriteString(mutedStyle.Render("  ↓ more below"))
		b.WriteByte('\n')
	}
	if len(filtered) == 0 {
		b.WriteString(mutedStyle.Render("No matching commands. Try a shorter search."))
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("↑/↓ move  ·  Enter open  ·  Esc close"))
	overlay := overlayStyle.Width(panelWidth).Render(strings.TrimSuffix(b.String(), "\n"))
	// A modal gets its own focused frame. Keeping the home screen underneath
	// makes the palette look like a broken overlay and leaves too much visual
	// competition for the command the user is choosing.
	return renderModalFrame(overlay, m.statusLine(width), width, m.height)
}

func (m *Model) paletteRange(count int) (int, int) {
	if count == 0 {
		return 0, 0
	}
	cursor := m.paletteCursor % count
	height := m.height
	if height < 1 {
		height = 24
	}
	rows := height - 15
	if rows < 4 {
		rows = 4
	}
	if rows > 10 {
		rows = 10
	}
	if rows > count {
		rows = count
	}
	start := cursor - rows + 1
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > count {
		end = count
		start = max(0, end-rows)
	}
	return start, end
}

func (m *Model) renderDataDetail() string {
	width := m.width
	if width < 1 {
		width = 80
	}
	panelWidth := min(max(36, width-10), 84)
	overlay, ok := m.dataDetailPanel(panelWidth)
	if !ok {
		m.dataDetailOpen = false
		return m.renderShell()
	}
	return renderModalFrame(overlay, m.statusLine(width), width, m.height)
}

func (m *Model) dataDetailPanel(panelWidth int) (string, bool) {
	if m.activeAction == "history" {
		return m.historyDetailPanel(panelWidth)
	}
	if m.activeAction == "providers" {
		return m.providerDetailPanel(panelWidth)
	}
	headers, row, ok := m.selectedData()
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(strings.ToUpper(m.activeTab()) + " · ROW DETAILS"))
	b.WriteString("\n\n")
	for index, header := range headers {
		if index >= len(row) {
			break
		}
		line := fmt.Sprintf("%-12s %s", header, valueOrDash(row[index]))
		b.WriteString(truncate(line, panelWidth-6))
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	if m.selectedAIFixAvailable() {
		action := "[F] DIAGNOSE WITH AI"
		if finding, ok := m.selectedHealthFinding(); ok {
			action = strings.ToUpper(healthFindingAIAction(finding))
		}
		b.WriteString(selectedStyle.Render(" " + action + " "))
		b.WriteString(mutedStyle.Render("  prepares a reviewable agent mission"))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("Enter / F continue · Esc close"))
		return overlayStyle.Width(panelWidth).Render(strings.TrimSuffix(b.String(), "\n")), true
	}
	b.WriteString(mutedStyle.Render("Enter / Esc close"))
	return overlayStyle.Width(panelWidth).Render(strings.TrimSuffix(b.String(), "\n")), true
}

func (m *Model) providerDetailPanel(panelWidth int) (string, bool) {
	provider, capabilities, ok := m.selectedProvider()
	if !ok {
		return "", false
	}
	lineWidth := max(16, panelWidth-6)
	state := "Connected"
	if !provider.Configured {
		state = "Not connected"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("FLEET · PROVIDER MANAGER"))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(strings.ToUpper(provider.Name)))
	b.WriteString("\n\n")
	historyDetailLine(&b, "Status", state, lineWidth)
	historyDetailLine(&b, "Models", strconv.Itoa(provider.ModelCount), lineWidth)
	historyDetailLine(&b, "Managed by", capabilities.manager, lineWidth)
	b.WriteString("\n")
	if capabilities.external {
		b.WriteString(mutedStyle.Render("Authentication is managed by " + capabilities.manager + ". Veto never reads or edits it here."))
		b.WriteString("\n\n")
	} else if !provider.Configured || capabilities.id == "local" {
		b.WriteString(selectedStyle.Render(" [C] CONNECT "))
		b.WriteString(mutedStyle.Render("  opens a secure, masked connection form"))
		b.WriteString("\n\n")
	} else {
		b.WriteString(selectedStyle.Render(" [E] EDIT / UPDATE "))
		b.WriteString(mutedStyle.Render("  replace connection settings securely"))
		b.WriteString("\n")
		if capabilities.verify {
			b.WriteString(selectedStyle.Render(" [V] VERIFY MODELS "))
			b.WriteString(mutedStyle.Render("  check the account catalog"))
			b.WriteString("\n")
		}
		if capabilities.remove {
			b.WriteString(selectedStyle.Render(" [X] REMOVE "))
			b.WriteString(mutedStyle.Render("  requires confirmation"))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(selectedStyle.Render(" [A] ADD PROVIDER "))
	b.WriteString("  ")
	b.WriteString(selectedStyle.Render(" [M] VIEW MODELS "))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("Enter runs the primary action · Esc closes"))
	return overlayStyle.Width(panelWidth).Render(strings.TrimSuffix(b.String(), "\n")), true
}

func (m *Model) updateProviderDetail(key tea.Key) (tea.Model, tea.Cmd) {
	provider, capabilities, ok := m.selectedProvider()
	if !ok {
		m.dataDetailOpen = false
		return m, nil
	}
	switch key.String() {
	case "esc":
		m.dataDetailOpen = false
		m.status = "Ready · providers"
	case "enter":
		if capabilities.external {
			m.dataDetailOpen = false
			return m, nil
		}
		m.dataDetailOpen = false
		m.openProviderLogin(capabilities.id)
	case "c", "e":
		if capabilities.connect && !capabilities.external {
			m.dataDetailOpen = false
			m.openProviderLogin(capabilities.id)
		}
	case "v":
		if provider.Configured && capabilities.verify {
			m.dataDetailOpen = false
			m.openProviderVerification(capabilities.id)
		}
	case "x":
		if provider.Configured && capabilities.remove {
			m.dataDetailOpen = false
			m.returnToProviders = true
			return m.requestExecution(controlplane.ActionRequest{ActionID: "logout", Arguments: map[string]string{"target": capabilities.id}})
		}
	case "a":
		m.dataDetailOpen = false
		m.openProviderLogin("")
	case "m":
		m.dataDetailOpen = false
		m.activeAction = "models"
		m.resetDataNavigation()
		m.status = "Ready · models"
	}
	return m, nil
}

func (m *Model) activateProviderDetailAt(x, y int) {
	width := m.width
	if width < 1 {
		width = 80
	}
	panelWidth := min(max(36, width-10), 84)
	panel, ok := m.providerDetailPanel(panelWidth)
	if !ok {
		return
	}
	height := m.height
	if height < 1 {
		height = 24
	}
	plain := ansi.Strip(panel)
	panelX, panelY := modalPanelOrigin(panel, width, height)
	for lineIndex, line := range strings.Split(plain, "\n") {
		for _, action := range []struct{ label, key string }{
			{"[C] CONNECT", "c"}, {"[E] EDIT / UPDATE", "e"}, {"[V] VERIFY MODELS", "v"},
			{"[X] REMOVE", "x"}, {"[A] ADD PROVIDER", "a"}, {"[M] VIEW MODELS", "m"},
		} {
			buttonX := strings.Index(line, action.label)
			if buttonX >= 0 && y == panelY+lineIndex && x >= panelX+buttonX && x < panelX+buttonX+len(action.label) {
				_, _ = m.updateProviderDetail(tea.Key{Text: action.key, Code: rune(action.key[0])})
				return
			}
		}
	}
}

func (m *Model) historyDetailPanel(panelWidth int) (string, bool) {
	event, ok := m.selectedHistoryEvent()
	if !ok {
		return "", false
	}
	lineWidth := max(16, panelWidth-6)
	var b strings.Builder
	b.WriteString(titleStyle.Render("MISSIONS · MISSION INSPECTOR"))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(strings.ToUpper(strings.ReplaceAll(event.Type, ".", " "))))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(truncate(historyEventMeaning(event), lineWidth)))
	b.WriteString("\n\n")
	historyDetailLine(&b, "Time", event.Timestamp.Local().Format("2006-01-02 15:04:05 MST"), lineWidth)
	historyDetailLine(&b, "Status", valueOrDash(event.Status), lineWidth)
	if mission, ok := m.selectedHistoryMission(); ok {
		historyDetailLine(&b, "Mission", historyMissionLabel(mission), lineWidth)
	}
	if event.Objective != "" {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("INITIAL PROMPT"))
		b.WriteString("\n")
		b.WriteString(formatMultiline(event.Objective, lineWidth, 12))
		b.WriteString("\n")
	}
	historyDetailLine(&b, "Run", valueOrDash(event.RunID), lineWidth)
	if event.TaskID != "" {
		historyDetailLine(&b, "Task", event.TaskID, lineWidth)
	}
	if event.TaskKind != "" || event.Risk != "" {
		historyDetailLine(&b, "Classification", joinPresent(event.TaskKind, event.Risk), lineWidth)
	}
	if event.Model != "" || event.Runtime != "" {
		historyDetailLine(&b, "Execution", joinPresent(event.Model, event.Runtime), lineWidth)
	}
	if event.Detail != "" {
		historyDetailLine(&b, "Recorded detail", event.Detail, lineWidth)
	}

	evidence := historyEvidence(event)
	if len(evidence) > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("DECISION & USAGE"))
		b.WriteString("\n")
		for _, item := range evidence {
			b.WriteString(truncate("• "+item, lineWidth))
			b.WriteString("\n")
		}
	}

	related := m.relatedHistory(event)
	b.WriteString("\n")
	windowSize := min(len(related), m.historyDetailWindowSize())
	start := min(max(0, m.historyDetailCursor-windowSize+1), max(0, len(related)-windowSize))
	end := min(len(related), start+windowSize)
	if len(related) <= windowSize {
		b.WriteString(headerStyle.Render(fmt.Sprintf("MISSION TIMELINE · %d EVENTS", len(related))))
	} else {
		b.WriteString(headerStyle.Render(fmt.Sprintf("MISSION TIMELINE · EVENTS %d–%d OF %d", start+1, end, len(related))))
	}
	b.WriteString("\n")
	for index := start; index < end; index++ {
		item := related[index]
		marker := "  "
		if index == m.historyDetailCursor {
			marker = "› "
		}
		line := fmt.Sprintf("%s%s  %-18s", marker, item.Timestamp.Local().Format("15:04:05"), item.Type)
		if item.Model != "" {
			line += "  " + item.Model
		} else if item.Runtime != "" {
			line += "  " + item.Runtime
		}
		b.WriteString(truncate(line, lineWidth))
		b.WriteString("\n")
	}
	if len(related) > windowSize {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("Showing events %d–%d of %d · ↑/↓ move · PgUp/PgDn page · Home/End jump", start+1, end, len(related))))
		b.WriteString("\n")
	}

	if historyEventFixable(event) {
		b.WriteString("\n")
		b.WriteString(selectedStyle.Render(" [F] DIAGNOSE WITH AI "))
		b.WriteString(mutedStyle.Render("  opens a reviewable repair mission"))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("↑/↓ select event · Enter/F diagnose · click event · Esc close"))
		return overlayStyle.Width(panelWidth).Render(strings.TrimSuffix(b.String(), "\n")), true
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("↑/↓ select event · click event · Enter/Esc close"))
	return overlayStyle.Width(panelWidth).Render(strings.TrimSuffix(b.String(), "\n")), true
}

func historyDetailLine(builder *strings.Builder, label, value string, width int) {
	if strings.TrimSpace(value) == "" {
		return
	}
	builder.WriteString(truncate(fmt.Sprintf("%-16s %s", label, value), width))
	builder.WriteByte('\n')
}

func joinPresent(values ...string) string {
	present := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			present = append(present, value)
		}
	}
	return strings.Join(present, " · ")
}

func historyEventMeaning(event controlplane.HistorySnapshot) string {
	switch event.Type {
	case "route.filter_pass":
		return "The model remained eligible after capability, policy, and budget filtering."
	case "route.filter_fail":
		return "The model was excluded during routing. Decision reasons below explain why."
	case "admission.started":
		return "Veto started judging the shortlisted model for this task."
	case "admission.accepted":
		return "The model passed admission and became eligible to execute the task."
	case "admission.rejected":
		return "The model did not pass admission; inspect the recorded reasons."
	case "admission.error":
		return "Admission could not complete because an error occurred."
	case "execution.started":
		return "The selected model began executing this mission."
	case "execution.completed":
		return "Execution completed successfully; usage and latency appear when recorded."
	case "execution.error":
		return "Execution failed before the mission could complete."
	case "tool.started":
		return "The execution harness started a tool operation."
	case "tool.completed":
		return "The execution harness completed a tool operation."
	case "tool.error":
		return "A tool operation failed during execution."
	case "review.started":
		return "Veto began checking the result against the mission acceptance criteria."
	case "review.completed":
		return "Acceptance review completed; the recorded status describes its outcome."
	case "review.error":
		return "Acceptance review failed before it could confirm the mission result."
	case "artifact.created":
		return "The mission produced a local artifact."
	default:
		return "A redacted lifecycle event recorded during this mission run."
	}
}

func historyEvidence(event controlplane.HistorySnapshot) []string {
	evidence := make([]string, 0, 6)
	if len(event.Reasons) > 0 {
		evidence = append(evidence, "Reasons: "+strings.Join(event.Reasons, ", "))
	}
	if event.ConfidenceKnown {
		evidence = append(evidence, fmt.Sprintf("Confidence: %.0f%%", event.Confidence*100))
	}
	if event.EstimatedTokensKnown {
		evidence = append(evidence, fmt.Sprintf("Estimated tokens: %d", event.EstimatedTokens))
	}
	if event.EstimatedCostKnown {
		evidence = append(evidence, fmt.Sprintf("Estimated cost: $%.6f", event.EstimatedCostUSD))
	}
	if event.UsageKnown {
		usage := fmt.Sprintf("Tokens: %d gross input + %d output = %d gross total", event.InputTokens, event.OutputTokens, event.TotalTokens)
		if event.CachedInputKnown {
			usage += fmt.Sprintf("; %d reused, %d fresh input", event.CachedInputTokens, max(0, event.InputTokens-event.CachedInputTokens))
		}
		evidence = append(evidence, usage)
	}
	if event.CostKnown {
		evidence = append(evidence, fmt.Sprintf("Actual cost: $%.6f", event.CostUSD))
	}
	if event.LatencyKnown {
		evidence = append(evidence, fmt.Sprintf("Latency: %d ms", event.LatencyMS))
	}
	return evidence
}

func (m *Model) dataDetailFixButtonAt(x, y int) bool {
	if !m.selectedAIFixAvailable() {
		return false
	}
	width := m.width
	if width < 1 {
		width = 80
	}
	height := m.height
	if height < 1 {
		height = 24
	}
	panel, ok := m.dataDetailPanel(min(max(36, width-10), 84))
	if !ok {
		return false
	}
	plain := ansi.Strip(panel)
	lines := strings.Split(plain, "\n")
	panelX, panelY := modalPanelOrigin(panel, width, height)
	for lineIndex, line := range lines {
		for _, label := range []string{"[F] FIX WITH AI", "[F] EXPLAIN WITH AI", "[F] DIAGNOSE WITH AI"} {
			buttonX := strings.Index(line, label)
			if buttonX >= 0 && y == panelY+lineIndex && x >= panelX+buttonX && x < panelX+buttonX+len(label) {
				return true
			}
		}
	}
	return false
}

func (m *Model) renderHelp(_ string) string {
	width := m.width
	if width < 1 {
		width = 80
	}
	help := overlayStyle.Width(max(30, min(width-6, 72))).Render(strings.Join([]string{
		titleStyle.Render("Keyboard help"),
		"",
		"Home composer",
		"Type / Enter  compose a mission",
		"Ctrl+R        route only",
		"Ctrl+F        open Fleet",
		"P             open Providers",
		"Alt+M         open Missions",
		"Ctrl+H        open Health",
		"Alt+I         open Integrations",
		"",
		"Tab / →     next primary view",
		"Shift+Tab / ← previous primary view",
		"Esc         return to Command Center",
		"j / ↓       move within a list",
		"k / ↑       move backwards within a list",
		"PgUp/PgDn   move a page in data tables",
		"/           filter every row before paging",
		"Mouse       click a row to open it",
		"Ctrl+K / /   open command palette",
		"r           run a task",
		"t           route only",
		"m           inspect models",
		"a           add a provider from Fleet",
		"d           run diagnostics",
		"0 / Home    return to Command Center",
		"h           open redacted history",
		"i           inspect integrations",
		"p           inspect plans",
		"Enter       select the focused command or edit its flags",
		"Esc         close an overlay or form first",
		"Ctrl+C      quit cleanly",
	}, "\n"))
	return renderModalFrame(help, m.statusLine(width), width, m.height)
}

func renderModalFrame(panel, footer string, width, height int) string {
	if height < 1 {
		height = 24
	}
	return lipgloss.Place(width, max(1, height-1), lipgloss.Center, lipgloss.Center, panel) + "\n" + footer
}

func modalPanelOrigin(panel string, width, height int) (int, int) {
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}
	containerHeight := max(1, height-1)
	return max(0, (width-lipgloss.Width(panel))/2), max(0, (containerHeight-lipgloss.Height(panel))/2)
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func truncate(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "…")
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
	brandStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#3DE7FF"))
	titleStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFB84A"))
	headerStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#65D9FF"))
	mutedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#8AA0A8"))
	selectedStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E9FCFF")).Background(lipgloss.Color("#17343D"))
	optionValueStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#BCECF5"))
	tabActiveStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#071014")).Background(lipgloss.Color("#3DE7FF"))
	tabInactiveStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8AA0A8"))
	panelStyle          = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#30363D")).Padding(0, 1)
	heroPanelStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#3DE7FF")).Background(lipgloss.Color("#09171B")).Padding(1, 2)
	workspacePanelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#193A45")).Background(lipgloss.Color("#091317")).Padding(0, 2)
	inspectorStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#193A45")).Background(lipgloss.Color("#081216")).Padding(0, 1)
	statusStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#C9DDE2")).Background(lipgloss.Color("#071014"))
	overlayStyle        = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#4B5563")).Background(lipgloss.Color("#0D1117")).Padding(1, 2)
)
