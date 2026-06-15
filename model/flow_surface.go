package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model/pane"
	"github.com/brian-bell/wtui/ui"
)

func (m Model) activeFlowSurfaceVisible() bool {
	return m.contentSurface == surfaceActiveFlows
}

func (m Model) flowSurfaceVisible() bool {
	return m.mode == ui.ModeFlows || m.activeFlowSurfaceVisible()
}

func (m Model) activeContentFetchMode() ui.Mode {
	if m.activeFlowSurfaceVisible() {
		return ui.ModeFlows
	}
	return m.mode
}

func (m Model) enterActiveFlowsSurface() (Model, tea.Cmd) {
	m.contentSurface = surfaceActiveFlows
	m.flowFocus = flowFocusList
	m.terminalPrefixActive = false
	m = m.syncActiveFlowsFromCache()
	return m.startFlowsModeFetchWithRefreshTick()
}

func (m Model) exitActiveFlowsSurface() Model {
	m.contentSurface = surfaceModeContent
	if m.flowFocus == flowFocusTerminal {
		m.flowFocus = flowFocusList
		m.terminalPrefixActive = false
	}
	return m
}

func (m Model) toggleActiveFlowsSurface() (Model, tea.Cmd) {
	if m.activeFlowSurfaceVisible() {
		return m.exitActiveFlowsSurface(), nil
	}
	return m.enterActiveFlowsSurface()
}

func (m Model) syncActiveFlowsFromCache() Model {
	selectedFlowID := m.selectedActiveFlowID()
	expandedFlowID := m.expandedActiveFlowID
	selectedPhaseID := m.selectedActiveFlowPhaseID
	m.activeFlows = m.activeFlows.SetItems(activeFlowRecords(m.flows.Items()))
	if selectedFlowID != "" {
		m.activeFlows = m.activeFlows.SelectFunc(func(record flowstore.FlowRecord) bool {
			return record.FlowID == selectedFlowID
		})
	}
	m = m.restoreActiveExpandedFlowSelection(expandedFlowID, selectedPhaseID)
	return m.reflowActiveFlows()
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
	return key >= "1" && key <= "8"
}

func modeForNumberedKey(key string) (ui.Mode, bool) {
	switch key {
	case "1":
		return ui.ModeWorktrees, true
	case "2":
		return ui.ModeBranches, true
	case "3":
		return ui.ModeStashes, true
	case "4":
		return ui.ModeHistory, true
	case "5":
		return ui.ModeReflog, true
	case "6":
		return ui.ModeSessions, true
	case "7":
		return ui.ModePlans, true
	case "8":
		return ui.ModeFlows, true
	default:
		return ui.ModeWorktrees, false
	}
}

func (m Model) switchModeFromKey(key string) (Model, tea.Cmd, bool) {
	mode, ok := modeForNumberedKey(key)
	if !ok {
		return m, nil, false
	}
	m = m.exitActiveFlowsSurface()
	if m.mode == mode {
		return m, nil, true
	}
	m.mode = mode
	m = m.resetModeCursors()
	if m.mode == ui.ModeFlows {
		next, cmd := m.startFlowsModeFetchWithRefreshTick()
		return next, cmd, true
	}
	next, cmd := m.startFetchMode(mode)
	return next, cmd, true
}
