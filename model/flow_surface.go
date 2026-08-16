package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/pane"
	"github.com/approachcontrol/approach/ui"
)

func (m Model) activeFlowSurfaceVisible() bool {
	return m.takeover == takeoverActiveFlows || m.activeFlowSurface
}

func (m Model) prBabysitterSurfaceVisible() bool {
	return m.takeover == takeoverPRBabysitter
}

func (m Model) takeoverVisible() bool {
	return m.activeFlowSurfaceVisible() || m.prBabysitterSurfaceVisible()
}

func (m Model) focusedMode() ui.Mode {
	if m.prBabysitterSurfaceVisible() {
		return ui.ModePRBabysitter
	}
	if m.activeFlowSurfaceVisible() {
		return ui.ModeActiveFlows
	}
	if m.contentPane == ui.PaneBottom {
		return m.bottomMode
	}
	return m.topMode
}

// modeStored reports whether a repository-scoped mode currently belongs to
// either stable pane. Stored background panes continue accepting their own
// current results even while focus is elsewhere.
func (m Model) modeStored(mode ui.Mode) bool {
	pane, ok := ui.PaneForMode(mode)
	if !ok {
		return false
	}
	if pane == ui.PaneTop {
		return m.topMode == mode
	}
	return m.bottomMode == mode
}

// storedModeVisible reports whether a stored mode currently receives rows in
// the stacked content layout. Before the first window-size message, the layout
// is unknown, so stored modes remain provisionally visible for startup fetch
// and refresh bookkeeping.
func (m Model) storedModeVisible(mode ui.Mode) bool {
	if m.takeoverVisible() || !m.modeStored(mode) {
		return false
	}
	if m.height <= 0 {
		return true
	}
	sharedOuterRows := m.embeddedTerminalDockAllocation().SharedOuterRows
	layout := ui.StackedContentLayout(sharedOuterRows, m.activePane, m.contentPane)
	pane, _ := ui.PaneForMode(mode)
	if pane == ui.PaneTop {
		return layout.TopRows > 0
	}
	return layout.BottomRows > 0
}

func (m Model) flowSurfaceVisible() bool {
	return m.focusedMode() == ui.ModeFlows || m.takeoverVisible()
}

func (m Model) flowRefreshSurfaceVisible() bool {
	if m.activeFlowSurfaceVisible() {
		return true
	}
	if _, ok := m.currentRepoPath(); !ok {
		return false
	}
	return m.storedModeVisible(ui.ModeFlows)
}

func (m Model) activeContentFetchMode() ui.Mode {
	if m.prBabysitterSurfaceVisible() {
		return ui.ModePRBabysitter
	}
	if m.activeFlowSurfaceVisible() {
		return ui.ModeActiveFlows
	}
	return m.focusedMode()
}

// selectStoredMode updates the canonical mode owner and moves content focus to
// that owner. Active Flows is intentionally rejected because it is a takeover,
// not a stored pane mode.
func (m Model) selectStoredMode(mode ui.Mode) (Model, bool) {
	pane, ok := ui.PaneForMode(mode)
	if !ok {
		return m, false
	}
	if pane == ui.PaneTop {
		if m.topMode != mode && ui.IsBeadsMode(m.topMode) {
			m = m.clearBeadExpansion()
		}
		m.topMode = mode
	} else {
		m.bottomMode = mode
	}
	m.activePane = pane
	m.contentPane = pane
	if ui.IsGitMode(mode) {
		m.lastGitMode = mode
	}
	if ui.IsBeadsMode(mode) {
		m.lastBeadsMode = mode
	}
	return m, true
}

func (m Model) syncActiveFlowsFromCache() Model {
	selectedFlowID := m.selectedActiveFlowID()
	expandedFlowID := m.expandedActiveFlowID
	selectedPhaseID := m.selectedActiveFlowPhaseID
	m.activeFlows = m.activeFlows.SetItems(activeFlowRecords(m.visibleActiveFlowRecords()))
	if selectedFlowID != "" {
		m.activeFlows = m.activeFlows.SelectFunc(func(record flowstore.FlowRecord) bool {
			return record.FlowID == selectedFlowID
		})
	}
	m = m.restoreActiveExpandedFlowSelection(expandedFlowID, selectedPhaseID)
	return m.reflowActiveFlows()
}

func (m Model) visibleActiveFlowRecords() []flowstore.FlowRecord {
	if m.activeFlowSurfaceVisible() && m.activePane == ui.PaneRepos {
		repoPath, ok := m.currentRepoPath()
		if !ok {
			return nil
		}
		records := make([]flowstore.FlowRecord, 0, len(m.activeFlowRecords))
		for _, record := range m.activeFlowRecords {
			if sameRepoPath(record.RepoPath, repoPath) {
				records = append(records, record)
			}
		}
		return records
	}
	return m.activeFlowRecords
}

func activeFlowRecords(records []flowstore.FlowRecord) []flowstore.FlowRecord {
	active := make([]flowstore.FlowRecord, 0, len(records))
	for _, record := range records {
		if record.Status == flowstore.StatusMerged || flowstore.FlowClosed(record) {
			continue
		}
		active = append(active, record)
	}
	return active
}

func (m Model) selectedActiveFlow() (flowstore.FlowRecord, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return flowstore.FlowRecord{}, false
	}
	return m.activeFlows.Selected()
}

func (m Model) selectedPRBabysitterFlow() (flowstore.FlowRecord, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return flowstore.FlowRecord{}, false
	}
	return m.prBabysitterFlows.Selected()
}

func (m Model) selectedActiveFlowID() string {
	record, ok := m.selectedActiveFlow()
	if !ok {
		return ""
	}
	return record.FlowID
}

func (m Model) currentFlowPane() pane.Pane[flowstore.FlowRecord] {
	if m.prBabysitterSurfaceVisible() {
		return m.prBabysitterFlows
	}
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows
	}
	return m.flows
}

func (m Model) currentFlowSelectedIndex() int {
	if m.prBabysitterSurfaceVisible() {
		return m.prBabysitterFlows.SelectedIndex()
	}
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.SelectedIndex()
	}
	return m.flows.SelectedIndex()
}

func (m Model) currentFlowScroll() int {
	if m.prBabysitterSurfaceVisible() {
		return m.prBabysitterFlows.Scroll()
	}
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.Scroll()
	}
	return m.flows.Scroll()
}

func (m Model) currentExpandedFlowID() string {
	if m.prBabysitterSurfaceVisible() {
		return m.expandedPRBabysitterFlowID
	}
	if m.activeFlowSurfaceVisible() {
		return m.expandedActiveFlowID
	}
	return m.expandedFlowID
}

func (m Model) currentSelectedFlowPhaseID() string {
	if m.prBabysitterSurfaceVisible() {
		return m.selectedPRBabysitterPhaseID
	}
	if m.activeFlowSurfaceVisible() {
		return m.selectedActiveFlowPhaseID
	}
	return m.selectedFlowPhaseID
}

func (m Model) setCurrentSelectedFlowPhaseID(phaseID string) Model {
	if m.prBabysitterSurfaceVisible() {
		m.selectedPRBabysitterPhaseID = phaseID
		return m
	}
	if m.activeFlowSurfaceVisible() {
		m.selectedActiveFlowPhaseID = phaseID
		return m
	}
	m.selectedFlowPhaseID = phaseID
	return m
}

func (m Model) currentFilteredFlows() []flowstore.FlowRecord {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	flows, _, _ := m.currentFlowPane().View()
	return flows
}

func (m Model) flowSurfaceContentHeight() int {
	if m.prBabysitterSurfaceVisible() {
		return m.paneContentHeight(ui.ModePRBabysitter)
	}
	if m.activeFlowSurfaceVisible() {
		return m.paneContentHeight(ui.ModeActiveFlows)
	}
	return m.paneContentHeight(ui.ModeFlows)
}

func (m Model) flowSurfaceItemHeight(expandedFlowID string) pane.ItemHeight[flowstore.FlowRecord] {
	return flowItemHeight(expandedFlowID)
}

func (m Model) setCurrentFlowPane(p pane.Pane[flowstore.FlowRecord]) Model {
	if m.prBabysitterSurfaceVisible() {
		m.prBabysitterFlows = p
		return m
	}
	if m.activeFlowSurfaceVisible() {
		m.activeFlows = p
		return m
	}
	m.flows = p
	return m
}

func (m Model) restoreActiveExpandedFlowSelection(flowID, phaseID string) Model {
	if flowID == "" {
		m.expandedActiveFlowID = ""
		m.selectedActiveFlowPhaseID = ""
		m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(""))
		return m
	}
	record, ok := m.selectedActiveFlow()
	if !ok || record.FlowID != flowID {
		m.expandedActiveFlowID = ""
		m.selectedActiveFlowPhaseID = ""
		m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(""))
		return m
	}
	if phaseID != "" {
		phase, ok := flowRecordPhaseByID(record, phaseID)
		if !ok {
			m.expandedActiveFlowID = ""
			m.selectedActiveFlowPhaseID = ""
			m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(""))
			return m
		}
		phaseID = phase.PhaseID
	}
	m.expandedActiveFlowID = flowID
	m.selectedActiveFlowPhaseID = phaseID
	m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(flowID))
	return m
}

func (m Model) reflowActiveFlows() Model {
	m.activeFlows = m.activeFlows.Reflow(m.paneContentHeight(ui.ModeActiveFlows), m.contentWidth())
	if m.activeFlowSurfaceVisible() {
		if m.selectedActiveFlowPhaseID != "" {
			return m.ensureSelectedFlowPhaseVisible()
		}
		if m.expandedActiveFlowID != "" {
			return m.reflowExpandedFlow()
		}
	}
	return m
}

func isNumberedModeKey(key string) bool {
	return key >= "1" && key <= "9"
}

// switchModeFromKey routes a number only within the focused stored pane.
func (m Model) switchModeFromKey(key string) (Model, tea.Cmd, bool) {
	if !isNumberedModeKey(key) {
		return m, nil, false
	}
	mode, ok := m.topLevelModeForNumberedKey(key)
	if !ok {
		return m, nil, true
	}
	currentMode := m.focusedMode()
	if currentMode == mode || (ui.IsGitMode(mode) && ui.IsGitMode(currentMode)) ||
		(ui.IsBeadsMode(mode) && ui.IsBeadsMode(currentMode)) {
		return m, nil, true
	}
	previousMode := currentMode
	m, _ = m.selectStoredMode(mode)
	m = m.resetModeCursorsForSwitch(previousMode, mode)
	if mode == ui.ModeFlows {
		next, cmd := m.startFlowsModeFetchWithRefreshTick()
		return next, cmd, true
	}
	next, cmd := m.startFetchMode(mode)
	return next, cmd, true
}

func (m Model) handleActiveFlowsToggle() (Model, tea.Cmd) {
	return m.handleTakeoverToggle(takeoverActiveFlows)
}

func (m Model) handlePRBabysitterToggle() (Model, tea.Cmd) {
	return m.handleTakeoverToggle(takeoverPRBabysitter)
}

func (m Model) handleTakeoverToggle(target takeoverMode) (Model, tea.Cmd) {
	current := m.currentTakeover()
	if current == takeoverNone {
		previousMode := m.focusedMode()
		m.activeFlowReturnPane = m.activePane
		m.activeFlowReturnContent = m.contentPane
		m.activeFlowReturnSet = true
		m = m.clearBeadExpansion()
		m = m.setTakeover(target)
		m = m.resetModeCursorsForSwitch(previousMode, m.takeoverUIMode(target))
		return m.startTakeoverFetch(target)
	}
	if current != target {
		previousMode := m.takeoverUIMode(current)
		m = m.cancelTakeoverFetch(current)
		if current == takeoverActiveFlows {
			m.flowRefreshTickGen++
		}
		m = m.setTakeover(target)
		m = m.resetModeCursorsForSwitch(previousMode, m.takeoverUIMode(target))
		return m.startTakeoverFetch(target)
	}

	m = m.cancelTakeoverFetch(current)
	m = m.setTakeover(takeoverNone)
	if m.activeFlowReturnSet {
		m.activePane = m.activeFlowReturnPane
		m.contentPane = m.activeFlowReturnContent
		m.activeFlowReturnSet = false
	}
	returnMode := m.focusedMode()
	m = m.resetModeCursorsForSwitch(m.takeoverUIMode(current), returnMode)
	m, expansionCmd := m.reconcileBeadExpansion()
	if m.flowRefreshSurfaceVisible() {
		next, refreshCmd := m.startFlowsModeFetchWithRefreshTick()
		return next, batchNonNil(refreshCmd, expansionCmd)
	}
	// No replacement refresh will advance the generation for a hidden or
	// unavailable stored pane, so invalidate the Active Flows tick explicitly.
	if current == takeoverActiveFlows {
		m.flowRefreshTickGen++
	}
	return m, expansionCmd
}

func takeoverFromStartup(active bool) takeoverMode {
	if active {
		return takeoverActiveFlows
	}
	return takeoverNone
}

func (m Model) currentTakeover() takeoverMode {
	if m.takeover == takeoverNone && m.activeFlowSurface {
		return takeoverActiveFlows
	}
	return m.takeover
}

func (m Model) setTakeover(mode takeoverMode) Model {
	if m.currentTakeover() == takeoverPRBabysitter && mode != takeoverPRBabysitter {
		m = m.cancelPRBabysitterRefresh()
	}
	m.takeover = mode
	m.activeFlowSurface = mode == takeoverActiveFlows
	return m
}

func (m Model) takeoverUIMode(mode takeoverMode) ui.Mode {
	if mode == takeoverPRBabysitter {
		return ui.ModePRBabysitter
	}
	return ui.ModeActiveFlows
}

func (m Model) startTakeoverFetch(mode takeoverMode) (Model, tea.Cmd) {
	if mode == takeoverPRBabysitter {
		return m.startPRBabysitterRefresh()
	}
	return m.startActiveFlowsFetchWithRefreshTick()
}

func (m Model) cancelTakeoverFetch(mode takeoverMode) Model {
	if mode == takeoverPRBabysitter {
		return m.cancelPRBabysitterRefresh()
	}
	if m.currentListRequest(ui.ModeActiveFlows) != 0 {
		m, _ = m.nextListFetchRequest(ui.ModeActiveFlows)
	}
	if m.flowRefreshInFlightMode == ui.ModeActiveFlows {
		m.flowRefreshInFlight = 0
		m.flowRefreshInFlightMode = 0
	}
	// Active Flows cannot cancel its synchronous list command, so fence its
	// result and release the shared refresh owner. The caller advances the tick
	// generation directly or starts the replacement stored-Flow refresh.
	return m
}
