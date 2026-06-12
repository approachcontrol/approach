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

func batchCommands(cmds ...tea.Cmd) tea.Cmd {
	nonNil := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			nonNil = append(nonNil, cmd)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return tea.Batch(nonNil...)
	}
}

func (m Model) startFlowRefreshTick() (Model, tea.Cmd) {
	m.flowRefreshTickGen++
	return m, m.flowRefreshTickCmd()
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
	var fetchCmd tea.Cmd
	m, fetchCmd = m.startFetchMode(ui.ModeFlows)
	var tickCmd tea.Cmd
	m, tickCmd = m.startFlowRefreshTick()
	return m, batchCommands(fetchCmd, tickCmd)
}
