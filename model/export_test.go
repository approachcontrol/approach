package model

import (
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

func FlowPhaseDoneInstructionForTest() string {
	return flowPhaseDoneInstruction
}

func FlowPlanPromptForTest(record flowstore.FlowRecord, templates FlowPromptTemplates) string {
	return flowPlanPrompt(record, templates)
}

func FlowPhasePromptForTest(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string, templates FlowPromptTemplates) string {
	return flowPhasePrompt(record, phase, planPath, planBody, templates)
}
