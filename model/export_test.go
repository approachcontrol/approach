package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/embeddedterm"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/ui"
)

func NewRealEmbeddedTerminalForTest(term *embeddedterm.Terminal) EmbeddedTerminal {
	return realEmbeddedTerminal{term: term}
}

func SetSearchActiveForTest(m Model, active bool) Model {
	return m.setSearchActive(active)
}

func FormForTest(m Model) ui.FormView {
	return uiFormView(m.modal.View().Form)
}

func ActiveFlowCreateForTest(m Model) uint64 {
	return m.activeFlowCreate
}

func ActiveFlowSelectedForTest(m Model) int {
	return m.activeFlows.SelectedIndex()
}

func ActiveFlowsForTest(m Model) []flowstore.FlowRecord {
	flows, _, _ := m.activeFlows.View()
	return flows
}

func SelectedActiveFlowPhaseIDForTest(m Model) string {
	return m.selectedActiveFlowPhaseID
}

func FlowHeadlessForTest(m Model) bool {
	return m.flowHeadless
}

func EmbeddedTerminalTickMsgForTest(m Model) any {
	return embeddedTerminalTickMsg{Generation: m.embeddedTerminalTickGen}
}

func HasRunningFlowEmbeddedTerminalForPhaseForTest(m Model, flowID, phaseID string) bool {
	return m.hasRunningFlowEmbeddedTerminalForPhase(flowID, phaseID)
}

func ClearFlowEmbeddedTerminalsForTest(m Model) Model {
	var kept []embeddedTerminalSlot
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow {
			kept = append(kept, slot)
		}
	}
	m.embeddedTerminals = kept
	return m
}

func AutoAdvanceResultForTest(m Model, flows []flowstore.FlowRecord) (Model, tea.Cmd) {
	m.autoAdvanceInFlight = 1
	return m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: flows, Request: 1})
}

func AutoAdvanceLaunchCommandForTest(m Model, flows []flowstore.FlowRecord) (Model, tea.Cmd) {
	previous := cloneFlowRecords(m.autoAdvanceSnapshot)
	current := cloneFlowRecords(flows)
	m.autoAdvanceSnapshot = current
	m.autoAdvanceLaunchedPhases = nil
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m, cmd, _ = m.prepareAutoFlowPhaseLaunch(previous, current)
	cmds = append(cmds, cmd)
	return m, batchNonNil(cmds...)
}

func WithAutoAdvanceSnapshotForTest(m Model, flows []flowstore.FlowRecord) Model {
	m.autoAdvanceSnapshot = cloneFlowRecords(flows)
	return m
}

func FlowPhaseDoneInstructionForTest() string {
	return flowPhaseDoneInstruction
}

func FlowPlanPromptForTest(record flowstore.FlowRecord, templates FlowPromptTemplates) string {
	return flowPlanPrompt(record, flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan", Kind: flowstore.KindPlan}, templates)
}

func FlowPhasePromptForTest(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string, templates FlowPromptTemplates) string {
	return flowPhasePrompt(record, phase, planPath, planBody, templates)
}
