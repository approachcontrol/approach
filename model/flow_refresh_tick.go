package model

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/ui"
)

const flowRefreshTickInterval = time.Second

type flowRefreshTickMsg struct {
	Generation uint64
}

func (m Model) flowRefreshTickCmd() tea.Cmd {
	generation := m.flowRefreshTickGen
	if generation == 0 {
		return nil
	}
	return tea.Tick(flowRefreshTickInterval, func(time.Time) tea.Msg {
		return flowRefreshTickMsg{Generation: generation}
	})
}

func (m Model) startFlowsModeFetchWithRefreshTick() (Model, tea.Cmd) {
	m.flowRefreshInFlight = 0
	return m.startFlowRefreshFetch()
}

func (m Model) startActiveFlowsFetchWithRefreshTick() (Model, tea.Cmd) {
	m.flowRefreshInFlight = 0
	return m.startActiveFlowRefreshFetch()
}

func (m Model) startFlowSurfaceFetch() (Model, tea.Cmd) {
	if m.activeFlowSurfaceVisible() {
		return m.startActiveFlowsFetchWithRefreshTick()
	}
	return m.startFetchMode(ui.ModeFlows)
}

func (m Model) startFlowRefreshFetch() (Model, tea.Cmd) {
	if m.flowRefreshInFlight != 0 {
		return m, nil
	}
	var fetchCmd tea.Cmd
	m, fetchCmd = m.startFetchMode(ui.ModeFlows)
	if fetchCmd == nil {
		m.flowRefreshTickGen++
		return m, m.flowRefreshTickCmd()
	}
	return m, fetchCmd
}

func (m Model) startActiveFlowRefreshFetch() (Model, tea.Cmd) {
	if m.flowRefreshInFlight != 0 {
		return m, nil
	}
	var fetchCmd tea.Cmd
	m, fetchCmd = m.startFetchActiveFlows()
	if fetchCmd == nil {
		m.flowRefreshTickGen++
		return m, m.flowRefreshTickCmd()
	}
	return m, fetchCmd
}

func (m Model) finishFlowRefreshFetch(mode ui.Mode, request uint64) (Model, tea.Cmd) {
	if mode != ui.ModeFlows || request == 0 || request != m.flowRefreshInFlight {
		return m, nil
	}
	m.flowRefreshInFlight = 0
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	return m, m.flowRefreshTickCmd()
}
