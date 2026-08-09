package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/pane"
	"github.com/approachcontrol/approach/ui"
)

func (m Model) activeFlowSurfaceVisible() bool {
	return m.mode == ui.ModeActiveFlows
}

func (m Model) flowSurfaceVisible() bool {
	return m.mode == ui.ModeFlows || m.activeFlowSurfaceVisible()
}

func (m Model) activeContentFetchMode() ui.Mode {
	if m.activeFlowSurfaceVisible() {
		return ui.ModeActiveFlows
	}
	return m.mode
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
	if m.activeFlowSurfaceVisible() && m.activePane == 0 {
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
		if record.Status == flowstore.StatusMerged {
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

func (m Model) selectedActiveFlowID() string {
	record, ok := m.selectedActiveFlow()
	if !ok {
		return ""
	}
	return record.FlowID
}

func (m Model) currentFlowPane() pane.Pane[flowstore.FlowRecord] {
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows
	}
	return m.flows
}

func (m Model) currentFlowSelectedIndex() int {
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.SelectedIndex()
	}
	return m.flows.SelectedIndex()
}

func (m Model) currentFlowScroll() int {
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.Scroll()
	}
	return m.flows.Scroll()
}

func (m Model) currentExpandedFlowID() string {
	if m.activeFlowSurfaceVisible() {
		return m.expandedActiveFlowID
	}
	return m.expandedFlowID
}

func (m Model) currentSelectedFlowPhaseID() string {
	if m.activeFlowSurfaceVisible() {
		return m.selectedActiveFlowPhaseID
	}
	return m.selectedFlowPhaseID
}

func (m Model) setCurrentSelectedFlowPhaseID(phaseID string) Model {
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
	return m.flowContentHeight()
}

func (m Model) flowSurfaceItemHeight(expandedFlowID string) pane.ItemHeight[flowstore.FlowRecord] {
	return flowItemHeight(expandedFlowID)
}

func (m Model) setCurrentFlowPane(p pane.Pane[flowstore.FlowRecord]) Model {
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
	m.activeFlows = m.activeFlows.Reflow(m.flowContentHeight(), m.contentWidth())
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

// switchModeFromKey routes the top-level number keys: 1 selects the Git view
// (landing on its last-used subview), 2-4 select the remaining top-level
// views, 5 selects Beads Open, and 6-9 are silent no-ops kept reserved for the
// future.
func (m Model) switchModeFromKey(key string) (Model, tea.Cmd, bool) {
	if !isNumberedModeKey(key) {
		return m, nil, false
	}
	mode, ok := m.topLevelModeForNumberedKey(key)
	if !ok {
		return m, nil, true
	}
	if m.mode == mode || (ui.IsGitMode(mode) && ui.IsGitMode(m.mode)) {
		return m, nil, true
	}
	previousMode := m.mode
	m.mode = mode
	m = m.rememberGitSubview()
	m = m.resetModeCursorsForSwitch(previousMode, m.mode)
	if m.mode == ui.ModeFlows {
		next, cmd := m.startFlowsModeFetchWithRefreshTick()
		return next, cmd, true
	}
	if m.mode == ui.ModeActiveFlows {
		next, cmd := m.startActiveFlowsFetchWithRefreshTick()
		return next, cmd, true
	}
	next, cmd := m.startFetchMode(mode)
	return next, cmd, true
}

func (m Model) handleActiveFlowsToggle() (Model, tea.Cmd) {
	if m.mode != ui.ModeActiveFlows {
		previousMode := m.mode
		m.activeFlowsReturnMode = previousMode
		m.mode = ui.ModeActiveFlows
		m = m.rememberGitSubview()
		m = m.resetModeCursorsForSwitch(previousMode, m.mode)
		return m.startActiveFlowsFetchWithRefreshTick()
	}

	returnMode := m.activeFlowsReturnMode
	if returnMode == 0 || returnMode == ui.ModeActiveFlows {
		returnMode = ui.ModeFlows
	}
	previousMode := m.mode
	m.mode = returnMode
	m.activeFlowsReturnMode = 0
	m = m.rememberGitSubview()
	m = m.resetModeCursorsForSwitch(previousMode, m.mode)
	if m.mode == ui.ModeFlows {
		return m.startFlowsModeFetchWithRefreshTick()
	}
	return m.startFetchMode(m.mode)
}
