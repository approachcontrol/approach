package model

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/ui"
)

// defaultFlowRefreshTickInterval is the production cadence. Options may
// override it; tests drive the loop far faster so they never wait on wall time.
const defaultFlowRefreshTickInterval = time.Second

// flowRefreshTickDelay is the cadence this Model refreshes at. A zero field
// means "unset" — a directly constructed Model still ticks at the production
// rate.
func (m Model) flowRefreshTickDelay() time.Duration {
	if m.flowRefreshTickInterval > 0 {
		return m.flowRefreshTickInterval
	}
	return defaultFlowRefreshTickInterval
}

type flowRefreshTickMsg struct {
	Generation uint64
}

func (m Model) flowRefreshTickCmd() tea.Cmd {
	generation := m.flowRefreshTickGen
	if generation == 0 {
		return nil
	}
	return tea.Tick(m.flowRefreshTickDelay(), func(time.Time) tea.Msg {
		return flowRefreshTickMsg{Generation: generation}
	})
}

func (m Model) startFlowsModeFetchWithRefreshTick() (Model, tea.Cmd) {
	m.flowRefreshInFlight = 0
	m.flowRefreshInFlightMode = 0
	return m.startFlowRefreshFetch()
}

func (m Model) startActiveFlowsFetchWithRefreshTick() (Model, tea.Cmd) {
	m.flowRefreshInFlight = 0
	m.flowRefreshInFlightMode = 0
	return m.startActiveFlowRefreshFetch()
}

func (m Model) startFlowSurfaceFetch() (Model, tea.Cmd) {
	if m.prBabysitterSurfaceVisible() {
		return m.startPRBabysitterRefresh()
	}
	if m.activeFlowSurfaceVisible() {
		return m.startActiveFlowsFetchWithRefreshTick()
	}
	return m.startFetchMode(ui.ModeFlows)
}

func (m Model) startFlowSurfaceRefreshFetch() (Model, tea.Cmd) {
	if m.prBabysitterSurfaceVisible() {
		return m.startPRBabysitterRefresh()
	}
	if m.activeFlowSurfaceVisible() {
		return m.startActiveFlowRefreshFetch()
	}
	return m.startFlowRefreshFetch()
}

func (m Model) startFlowRefreshFetch() (Model, tea.Cmd) {
	if m.flowRefreshInFlight != 0 {
		return m, nil
	}
	if _, ok := m.currentRepoPath(); !ok {
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
	if (mode != ui.ModeFlows && mode != ui.ModeActiveFlows) || request == 0 || request != m.flowRefreshInFlight {
		return m, nil
	}
	if mode != m.flowRefreshInFlightMode {
		return m, nil
	}
	m.flowRefreshInFlight = 0
	m.flowRefreshInFlightMode = 0
	if !m.flowRefreshSurfaceVisible() {
		return m, nil
	}
	return m, m.flowRefreshTickCmd()
}
