package model

import (
	"github.com/brian-bell/wtui/embeddedterm"
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

func FlowHeadlessForTest(m Model) bool {
	return m.flowHeadless
}

func EmbeddedTerminalTickMsgForTest(m Model) any {
	return embeddedTerminalTickMsg{Generation: m.embeddedTerminalTickGen}
}

func HasRunningFlowEmbeddedTerminalForPhaseForTest(m Model, flowID, phaseID string) bool {
	return m.hasRunningFlowEmbeddedTerminalForPhase(flowID, phaseID)
}
