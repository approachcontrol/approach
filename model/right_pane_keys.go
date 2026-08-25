package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/ui"
)

type rightPaneModeKeyHandler func(Model, string) (tea.Model, tea.Cmd, bool)

func rightPaneModeHandler(mode ui.Mode) rightPaneModeKeyHandler {
	switch mode {
	case ui.ModeWorktrees:
		return Model.handleWorktreesPaneKey
	case ui.ModeBranches:
		return Model.handleBranchesPaneKey
	case ui.ModePlans:
		return Model.handlePlansPaneKey
	case ui.ModeSessions:
		return Model.handleSessionsPaneKey
	case ui.ModeFlows:
		return Model.handleFlowsPaneKey
	case ui.ModeBeadsReady:
		return Model.handleBeadsReadyPaneKey
	default:
		return nil
	}
}

func (m Model) handleWorktreesPaneKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "x":
		next, cmd := m.handleToggleWorktreeSessions()
		return next, cmd, true
	case "n":
		next, cmd := m.handleNewWorktree(false)
		return next, cmd, true
	case "P":
		next, cmd := m.handleNewPullRequestWorktree()
		return next, cmd, true
	case "m":
		next, cmd := m.handleMoveWorktree()
		return next, cmd, true
	case "N":
		next, cmd := m.handleNewWorktree(true)
		return next, cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) handleBranchesPaneKey(key string) (tea.Model, tea.Cmd, bool) {
	if key == "n" {
		next, cmd := m.handleNewBranch()
		return next, cmd, true
	}
	return m, nil, false
}

func (m Model) handlePlansPaneKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "y":
		next, cmd := m.handleCopyPlanPath()
		return next, cmd, true
	case "i", "a":
		next, cmd := m.handleImplementPlan()
		return next, cmd, true
	case "x":
		next, cmd := m.handleTogglePlanPhases()
		return next, cmd, true
	case "o":
		next, cmd := m.handleOpenPlanText()
		return next, cmd, true
	case "e":
		next, cmd := m.handleEditPlan()
		return next, cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) handleSessionsPaneKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "y":
		next, cmd := m.handleCopySessionID()
		return next, cmd, true
	case "o":
		next, cmd := m.handleEnter()
		return next, cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) handleFlowsPaneKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "h":
		next, cmd := m.handleToggleFlowHeadless()
		return next, cmd, true
	case "n":
		next, cmd := m.handleNewFlow()
		return next, cmd, true
	default:
		if !m.flowSurfaceVisible() {
			return m, nil, false
		}
		return m.handleFlowSurfacePaneKey(key)
	}
}

func (m Model) handleFlowSurfacePaneKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "y":
		next, cmd := m.handleCopyFlowWorktreePath()
		return next, cmd, true
	case "s":
		next, cmd := m.handleStartSelectedFlowWorktreeAgent()
		return next, cmd, true
	case "r":
		next, cmd := m.handleResumeFlowPhaseSession()
		return next, cmd, true
	case "E":
		next, cmd := m.handleSetReasoningEffort()
		return next, cmd, true
	case "M":
		next, cmd := m.handleSetModel()
		return next, cmd, true
	case "i":
		next, cmd := m.handleOpenSelectedFlowIssue()
		return next, cmd, true
	case "x":
		next, cmd := m.handleResetSelectedFlowPhase()
		return next, cmd, true
	case "g":
		next, cmd := m.handleLaunchNextFlowPhase()
		return next, cmd, true
	case "R":
		next, cmd := m.handleRepairSelectedFlow()
		return next, cmd, true
	case "p":
		next, cmd := m.handleOpenSelectedFlowPR()
		return next, cmd, true
	case "o":
		next, cmd := m.handleOpenFlowPlanText()
		return next, cmd, true
	case "m":
		next, cmd := m.handleMarkFlowManuallyMerged()
		return next, cmd, true
	case "U":
		next, cmd := m.handleAutofixSelectedFlowPR()
		return next, cmd, true
	case "C":
		next, cmd := m.handleCloseFlow()
		return next, cmd, true
	case "a":
		next, cmd := m.handleToggleFlowAutoMode()
		return next, cmd, true
	case "G":
		next, cmd := m.handleCycleFlowAutoMerge()
		return next, cmd, true
	case "c":
		next, cmd := m.handleCopyFlowID()
		return next, cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) handleBeadsReadyPaneKey(key string) (tea.Model, tea.Cmd, bool) {
	if key == "f" {
		next, cmd := m.handleReadyBeadFlowCreate(readyBeadFlowCreateOnly)
		return next, cmd, true
	}
	return m, nil, false
}

func (m Model) handleRightPaneSharedKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		return m.handleCursorUp()
	case "down", "j":
		return m.handleCursorDown()
	case "left":
		return m.handleHorizontalNavigation(-1)
	case "right":
		return m.handleHorizontalNavigation(1)
	case "y":
		if m.flowSurfaceVisible() {
			return m.handleCopyFlowWorktreePath()
		}
		return m.handleCopyHash()
	case "s":
		if m.flowSurfaceVisible() {
			return m.handleStartSelectedFlowWorktreeAgent()
		}
		return m.handleShowSessionSummary()
	case "r":
		if m.flowSurfaceVisible() {
			return m.handleResumeFlowPhaseSession()
		}
		return m.handleResumeSession()
	case "E":
		if m.flowSurfaceVisible() {
			return m.handleSetReasoningEffort()
		}
	case "M":
		if m.flowSurfaceVisible() {
			return m.handleSetModel()
		}
	case "i":
		if m.flowSurfaceVisible() {
			return m.handleOpenSelectedFlowIssue()
		}
	case "x":
		if m.flowSurfaceVisible() {
			return m.handleResetSelectedFlowPhase()
		}
	case "g":
		if m.flowSurfaceVisible() {
			return m.handleLaunchNextFlowPhase()
		}
	case "R":
		if m.flowSurfaceVisible() {
			return m.handleRepairSelectedFlow()
		}
	case "enter":
		return m.handleEnter()
	case "p":
		if m.flowSurfaceVisible() {
			return m.handleOpenSelectedFlowPR()
		}
		return m.handlePrune()
	case "o":
		if m.flowSurfaceVisible() {
			return m.handleOpenFlowPlanText()
		}
	case "m":
		if m.flowSurfaceVisible() {
			return m.handleMarkFlowManuallyMerged()
		}
	case "U":
		if m.flowSurfaceVisible() {
			return m.handleAutofixSelectedFlowPR()
		}
	case "C":
		if m.flowSurfaceVisible() {
			return m.handleCloseFlow()
		}
	case "a":
		if m.flowSurfaceVisible() {
			return m.handleToggleFlowAutoMode()
		}
		if m.epicProgressionKeysOwned() {
			return m.handleToggleEpicProgression()
		}
		return m.handleOpenAgent()
	case "G":
		if m.flowSurfaceVisible() {
			return m.handleCycleFlowAutoMerge()
		}
	case "d":
		return m.handleDelete()
	case "u":
		return m.handleUnlock()
	case "f":
		return m.handleFetch()
	case "F":
		if m.readyBeadFlowKeysOwned() {
			return m.handleReadyBeadFlowCreate(readyBeadFlowCreateAndStart)
		}
		return m.handlePull()
	case "S":
		// S has no other binding in any pane, so the handler's own ownership
		// gate is the whole rule and an unconditional case is correct.
		return m.handleSliceSelectedEpic()
	case "t":
		return m.handleOpenTerminal()
	case "T":
		if m.tmuxLaunchBackend() {
			return m.handleAttachRepoTmuxSession()
		}
	case "c":
		if m.flowSurfaceVisible() {
			return m.handleCopyFlowID()
		}
		return m.handleOpenCode()
	case "q", "ctrl+c", "esc":
		return m.handleEmbeddedTerminalQuitPrefix()
	}
	return m, nil
}
