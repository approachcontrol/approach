package model

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.modal.IsOpen() {
		if next, cmd, handled := m.handlePromptTemplateModalKey(msg); handled {
			return next, cmd
		}
		var cmd tea.Cmd
		view := m.modal.View()
		var outcome modal.Outcome
		terminalConfirmOpen := m.terminalConfirmID != 0
		m.modal, outcome, cmd = m.modal.Update(msg)
		if terminalConfirmOpen && !m.modal.IsOpen() {
			m = m.clearEmbeddedTerminalConfirm()
		}
		if outcome == modal.Accepted && cmd != nil && isWorktreeCreateInput(view) {
			var request uint64
			m, request = m.nextWorktreeCreateRequest()
			cmd = tagWorktreeCreateRequest(cmd, request)
		} else if outcome == modal.Accepted && cmd != nil && isRepoCreateForm(view) {
			var request uint64
			m, request = m.nextRepoCreateRequest()
			cmd = tagRepoCreateRequest(cmd, request)
		} else if outcome == modal.Accepted && cmd != nil && isPromptTemplateEditor(view) {
			cmd = tagPromptTemplateCursor(cmd, view.InputCursor)
		} else if outcome == modal.Accepted && cmd != nil && isFlowCreateForm(view) {
			if m.activeFlowCreate != 0 {
				return m.setStatus(statusOther, flowCreateInProgressStatus), nil
			}
			var request uint64
			m, request = m.nextFlowCreateRequest()
			cmd = tagFlowCreateRequest(cmd, request)
		}
		return m, cmd
	}

	terminalInputFocused := m.terminalEffectivelyExpanded() && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal && !m.terminalPrefixActive && m.hasActiveEmbeddedTerminal()
	if !m.searchActive && key == "ctrl+t" && !terminalInputFocused {
		m = m.clearAnyStatus()
		return m.toggleEmbeddedTerminalDock(), nil
	}

	if next, cmd, handled := m.handleEmbeddedTerminalKey(msg); handled {
		return next, cmd
	}

	m = m.clearAnyStatus()

	if !m.searchActive && key == "ctrl+r" {
		return m.focusRepoPane(), nil
	}

	if !m.searchActive && key == "ctrl+a" {
		return m.handleActiveFlowsToggle()
	}
	if !m.searchActive && key == "ctrl+p" {
		return m.handlePRBabysitterToggle()
	}

	if !m.searchActive && m.contentListInputEligible() && !m.takeoverVisible() && isNumberedModeKey(key) {
		next, cmd, handled := m.switchModeFromKey(key)
		if handled {
			return next, cmd
		}
	}

	if m.searchActive {
		return m.handleSearchKey(msg)
	}

	if key == "/" {
		m = m.setSearchActive(true)
		return m, nil
	}

	if key == "esc" && m.activeSearchQuery() != "" {
		beadsSearch := m.activePane != ui.PaneRepos && ui.IsBeadsMode(m.focusedMode())
		oldRepoPath, _ := m.currentRepoPath()
		m = m.setActiveSearchQuery("")
		m = m.setSearchActive(false)
		if m.activePane == ui.PaneRepos && oldRepoPath != "" {
			m = m.selectFilteredRepo(oldRepoPath)
		}
		m = m.clampSelectionsAfterFilter()
		if m.activePane == ui.PaneRepos {
			newRepoPath, ok := m.currentRepoPath()
			if oldRepoPath != newRepoPath {
				return m.handleRepoSelectionChanged(ok)
			}
		}
		if beadsSearch {
			return m.reconcileBeadExpansion()
		}
		return m, nil
	}

	if key == "D" {
		m.destructive = !m.destructive
		return m, nil
	}

	if key == "A" {
		return m.handleSetAgent()
	}

	if key == "M" && m.flowSurfaceVisible() {
		return m.handleSetModel()
	}

	if key == "E" && m.flowSurfaceVisible() {
		return m.handleSetReasoningEffort()
	}

	if key == "f5" {
		return m.startGlobalRefresh()
	}

	if key == "f2" {
		return m.handlePromptTemplates()
	}

	if key == "tab" {
		m = m.cyclePaneFocusForward()
		return m, nil
	}

	if isPaneBackKey(key) {
		m = m.cyclePaneFocusBackward()
		return m, nil
	}

	if m.activePane == ui.PaneRepos {
		return m.handleLeftPaneKey(key)
	}
	return m.handleRightPaneKey(key)
}

func (m Model) toggleEmbeddedTerminalDock() Model {
	if len(m.embeddedTerminals) == 0 {
		return m.setStatus(statusOther, "No embedded terminals")
	}
	if m.terminalDockVisible {
		m.terminalDockVisible = false
		if m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal {
			m.terminalFocus = terminalFocusList
			m.terminalPrefixActive = false
		}
	} else {
		m.terminalDockVisible = true
	}
	m = m.resizeEmbeddedTerminals()
	return m.reflowForTerminalDock()
}

func (m Model) reflowForTerminalDock() Model {
	m = m.reflowRepos()
	m = m.reflowStoredPanes()
	m = m.reflowActiveFlows()
	return m.reflowPRBabysitter()
}

func (m Model) reflowStoredPanes() Model {
	m = m.reflowWorktrees()
	m = m.reflowBranches()
	m = m.reflowStashes()
	m = m.reflowCommits()
	m = m.reflowReflogs()
	m = m.reflowSessions()
	m = m.reflowPlans()
	m = m.reflowFlows()
	return m.reflowAllBeads()
}

func isPaneBackKey(key string) bool {
	return key == "backspace" || key == "ctrl+h"
}

func (m Model) contentListInputEligible() bool {
	return m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusList
}

func isWorktreeCreateInput(view modal.View) bool {
	if view.Kind != modal.Input {
		return false
	}
	return view.Placeholder == ui.WorktreeInputPlaceholder || view.Placeholder == ui.PRWorktreeInputPlaceholder
}

func isPromptTemplateEditor(view modal.View) bool {
	return view.Kind == modal.Input && view.Editor.Enabled
}

// tagPromptTemplateCursor stamps the pre-submit cursor onto a failed write. The
// submit closure cannot supply it: it receives only the string, and by the
// time it runs the modal has already been zeroed. Both failure messages are
// tagged because a whitespace-only submit fails as a reset, not a save.
func tagPromptTemplateCursor(cmd tea.Cmd, cursor int) tea.Cmd {
	return func() tea.Msg {
		switch failed := cmd().(type) {
		case PromptTemplateSaveFailedMsg:
			failed.Cursor = cursor
			return failed
		case PromptTemplateResetFailedMsg:
			failed.Cursor = cursor
			return failed
		default:
			return failed
		}
	}
}

func isRepoCreateForm(view modal.View) bool {
	return view.Kind == modal.Form && view.Form.Purpose == repoCreateFormPurpose
}

func isFlowCreateForm(view modal.View) bool {
	return view.Kind == modal.Form && view.Form.Purpose == flowCreateFormPurpose
}

func tagWorktreeCreateRequest(cmd tea.Cmd, request uint64) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		switch msg := msg.(type) {
		case WorktreeCreatedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		case WorktreeCreateFailedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		case WorktreeBootstrapFailedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		default:
			return msg
		}
	}
}

func tagRepoCreateRequest(cmd tea.Cmd, request uint64) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		switch msg := msg.(type) {
		case RepoCreatedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		case RepoCreateFailedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		default:
			return msg
		}
	}
}

func tagFlowCreateRequest(cmd tea.Cmd, request uint64) tea.Cmd {
	return func() tea.Msg {
		msg := cmd()
		switch msg := msg.(type) {
		case flowLaunchCreateRequestedMsg:
			if msg.Create.Presentation.Request == 0 {
				msg.Create.Presentation.Request = request
			}
			return msg
		case FlowCreatedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		case FlowCreateFailedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		default:
			return msg
		}
	}
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	beadsSearch := m.activePane != ui.PaneRepos && ui.IsBeadsMode(m.focusedMode())
	oldRepoPath, _ := m.currentRepoPath()

	switch key {
	case "esc":
		m = m.setActiveSearchQuery("")
		m = m.setSearchActive(false)
	case "enter":
		m = m.setSearchActive(false)
	case "backspace", "ctrl+h":
		q := m.activeSearchQuery()
		if q != "" {
			runes := []rune(q)
			m = m.setActiveSearchQuery(string(runes[:len(runes)-1]))
		} else {
			m = m.setActiveSearchQuery("")
			m = m.setSearchActive(false)
		}
	case "ctrl+u":
		m = m.setActiveSearchQuery("")
	default:
		if msg.Type == tea.KeyRunes {
			m = m.setActiveSearchQuery(m.activeSearchQuery() + string(msg.Runes))
		}
	}

	if m.activePane == ui.PaneRepos && oldRepoPath != "" {
		m = m.selectFilteredRepo(oldRepoPath)
	}
	m = m.clampSelectionsAfterFilter()
	if m.activePane == ui.PaneRepos {
		newRepoPath, ok := m.currentRepoPath()
		if oldRepoPath != newRepoPath {
			return m.handleRepoSelectionChanged(ok)
		}
	}
	if beadsSearch {
		return m.reconcileBeadExpansion()
	}
	return m, nil
}

func (m Model) setSearchActive(active bool) Model {
	if m.searchActive == active {
		return m
	}
	m.searchActive = active
	return m.resizeEmbeddedTerminals()
}

// --- Key handlers by context ---

func (m Model) handleLeftPaneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		if len(m.filteredRepos()) > 0 {
			m = m.setRepoPaneCollapsed(true)
			if m.takeoverVisible() {
				m.activePane = m.contentPane
				if m.prBabysitterSurfaceVisible() {
					return m.syncPRBabysitterFromCache(), nil
				}
				return m.syncActiveFlowsFromCache(), nil
			}
			m = m.focusContentPane(ui.PaneTop)
		}
	case "up", "k":
		if len(m.filteredRepos()) > 0 {
			return m.moveRepoSelection(-1)
		}
	case "down", "j":
		if len(m.filteredRepos()) > 0 {
			return m.moveRepoSelection(1)
		}
	case "f":
		return m.startFetchVisibleRepos()
	case "n":
		return m.handleNewRepo()
	case "T":
		if m.tmuxLaunchBackend() {
			return m.handleAttachRepoTmuxSession()
		}
	case "q", "ctrl+c", "esc":
		return m.handleEmbeddedTerminalQuitPrefix()
	}
	return m, nil
}

func (m Model) moveRepoSelection(delta int) (tea.Model, tea.Cmd) {
	previousPath, _ := m.currentRepoPath()
	m.repos = m.repos.Move(delta, m.repoContentHeight(), ui.LeftPaneWidth-2)
	m.pendingRepoSelection = ""
	currentPath, _ := m.currentRepoPath()
	if sameRepoPath(previousPath, currentPath) {
		return m, nil
	}
	return m.handleRepoSelectionChanged(true)
}

func (m Model) handleRepoSelectionChanged(repoSelected bool) (tea.Model, tea.Cmd) {
	m = m.invalidateReadyBeadFlowCreateRequest()
	if m.takeoverVisible() {
		m = m.resetStoredPaneCursors()
		if m.prBabysitterSurfaceVisible() {
			m = m.syncPRBabysitterFromCache()
		} else {
			m = m.syncActiveFlowsFromCache()
		}
		if repoSelected {
			return m.startStoredModeFetches()
		}
		return m, nil
	}
	m = m.resetRightPaneCursors()
	if repoSelected {
		return m.startStoredModeFetches()
	}
	return m, nil
}

func (m Model) handleRightPaneKey(key string) (tea.Model, tea.Cmd) {
	if m.takeoverVisible() {
		return m.handleActiveFlowSurfaceKey(key)
	}
	if next, cmd, handled := m.handleGitSubviewKey(key); handled {
		return next, cmd
	}
	if next, cmd, handled := m.handleBeadsSubviewKey(key); handled {
		return next, cmd
	}
	mode := m.focusedMode()
	switch key {
	case "up", "k":
		return m.handleCursorUp()
	case "down", "j":
		return m.handleCursorDown()
	case "left":
		return m.handleHorizontalNavigation(-1)
	case "right":
		return m.handleHorizontalNavigation(1)
	case "h":
		if mode == ui.ModeFlows {
			return m.handleToggleFlowHeadless()
		}
	case "y":
		if mode == ui.ModePlans {
			return m.handleCopyPlanPath()
		}
		if mode == ui.ModeSessions {
			return m.handleCopySessionID()
		}
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
		if mode == ui.ModePlans {
			return m.handleImplementPlan()
		}
		if m.flowSurfaceVisible() {
			return m.handleOpenSelectedFlowIssue()
		}
	case "x":
		if mode == ui.ModeWorktrees {
			return m.handleToggleWorktreeSessions()
		}
		if mode == ui.ModePlans {
			return m.handleTogglePlanPhases()
		}
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
	case "n":
		if mode == ui.ModeWorktrees {
			return m.handleNewWorktree(false)
		}
		if mode == ui.ModeBranches {
			return m.handleNewBranch()
		}
		if mode == ui.ModeFlows {
			return m.handleNewFlow()
		}
	case "P":
		if mode == ui.ModeWorktrees {
			return m.handleNewPullRequestWorktree()
		}
	case "p":
		if m.flowSurfaceVisible() {
			return m.handleOpenSelectedFlowPR()
		}
		return m.handlePrune()
	case "o":
		if mode == ui.ModeSessions {
			return m.handleEnter()
		}
		if mode == ui.ModePlans {
			return m.handleOpenPlanText()
		}
		if m.flowSurfaceVisible() {
			return m.handleOpenFlowPlanText()
		}
	case "e":
		if mode == ui.ModePlans {
			return m.handleEditPlan()
		}
	case "m":
		if mode == ui.ModeWorktrees {
			return m.handleMoveWorktree()
		}
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
	case "N":
		if mode == ui.ModeWorktrees {
			return m.handleNewWorktree(true)
		}
	case "a":
		if mode == ui.ModePlans {
			return m.handleImplementPlan()
		}
		if m.flowSurfaceVisible() {
			return m.handleToggleFlowAutoMode()
		}
		if m.epicProgressionKeysOwned() {
			return m.handleToggleEpicProgression()
		}
		return m.handleOpenAgent()
	case "d":
		return m.handleDelete()
	case "u":
		return m.handleUnlock()
	case "f":
		if mode == ui.ModeBeadsReady {
			return m.handleReadyBeadFlowCreate(readyBeadFlowCreateOnly)
		}
		return m.handleFetch()
	case "F":
		if m.readyBeadFlowKeysOwned() {
			return m.handleReadyBeadFlowCreate(readyBeadFlowCreateAndStart)
		}
		return m.handlePull()
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

type readyBeadFlowIntent uint8

const (
	readyBeadFlowCreateOnly readyBeadFlowIntent = iota
	readyBeadFlowCreateAndStart
)

func (m Model) readyBeadFlowKeysOwned() bool {
	terminalInputFocused := m.terminalEffectivelyExpanded() && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal && m.hasActiveEmbeddedTerminal()
	if m.modal.IsOpen() || m.searchActive || terminalInputFocused ||
		!m.contentListInputEligible() || m.focusedMode() != ui.ModeBeadsReady {
		return false
	}
	if _, ok := m.currentRepoPath(); !ok {
		return false
	}
	bead, ok := m.selectedVisibleBead()
	// An empty ID would produce an unrunnable `bd show ` instruction, so the
	// action is not offered for rows that carry no usable Bead ID.
	return ok && strings.TrimSpace(bead.ID) != ""
}

func (m Model) canCreateReadyBeadFlow() bool {
	return m.readyBeadFlowKeysOwned() && !m.flowPreparationAdmission
}

func (m Model) canStartReadyBeadFlow() bool {
	if !m.canCreateReadyBeadFlow() {
		return false
	}
	command, _, _ := m.flowLaunchAgentSettings()
	return agent.Normalize(command) != ""
}

func (m Model) handleReadyBeadFlowCreate(intent readyBeadFlowIntent) (Model, tea.Cmd) {
	if !m.canCreateReadyBeadFlow() || (intent == readyBeadFlowCreateAndStart && !m.canStartReadyBeadFlow()) {
		return m, nil
	}
	repoPath, _ := m.currentRepoPath()
	bead, _ := m.selectedVisibleBead()
	beadID := strings.TrimSpace(bead.ID)
	beadLink := flowstore.BeadLink{ID: beadID, EpicID: strings.TrimSpace(bead.Parent)}
	title := beadID + ": " + strings.TrimSpace(bead.Title)
	instructions := fmt.Sprintf("Use Bead %s as the durable source of requirements. Read it with `bd show %s` before planning or implementation.", beadID, beadID)
	var request uint64
	m, request = m.nextReadyBeadFlowCreateRequest()
	if intent == readyBeadFlowCreateOnly {
		return m, m.createReadyBeadFlowOnly(repoPath, title, instructions, beadLink, request, m.flowPreparationOwner.Token)
	}
	return m, m.requestReadyBeadFlowLaunch(repoPath, title, instructions, beadLink, request)
}

func (m Model) togglePrimaryPaneFocus() Model {
	if m.activePane == ui.PaneRepos {
		m.activePane = m.contentPane
		if m.takeoverVisible() {
			if m.prBabysitterSurfaceVisible() {
				return m.syncPRBabysitterFromCache()
			}
			return m.syncActiveFlowsFromCache()
		}
		return m
	}
	return m.focusRepoPane()
}

func (m Model) focusRepoPane() Model {
	m = m.setRepoPaneCollapsed(false)
	m.activePane = ui.PaneRepos
	if m.takeoverVisible() {
		m = m.clearSelectedFlowPhase()
		if m.prBabysitterSurfaceVisible() {
			return m.syncPRBabysitterFromCache()
		}
		return m.syncActiveFlowsFromCache()
	}
	if m.focusedMode() == ui.ModePlans {
		m = m.clearSelectedPlanPhase()
	}
	if m.focusedMode() == ui.ModeFlows || m.takeoverVisible() {
		m = m.clearSelectedFlowPhase()
	}
	return m
}

func (m Model) setRepoPaneCollapsed(collapsed bool) Model {
	if m.repoPaneCollapsed == collapsed {
		return m
	}
	m.repoPaneCollapsed = collapsed
	// Embedded PTYs span the full app width, so collapse needs no resize.
	return m.clampSelectionsAfterFilter()
}

func (m Model) cyclePaneFocusForward() Model {
	terminalEligible := m.terminalEffectivelyExpanded() && m.hasActiveEmbeddedTerminal()
	if m.terminalFocus == terminalFocusTerminal {
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
		if m.takeoverVisible() && m.repoPaneCollapsed {
			return m.focusContentPane(m.contentPane)
		}
		if m.repoPaneCollapsed {
			return m.focusContentPane(ui.PaneTop)
		}
		return m.focusRepoPane()
	}
	if m.takeoverVisible() {
		if m.activePane == ui.PaneRepos {
			return m.focusContentPane(m.contentPane)
		}
		if terminalEligible {
			return m.focusTerminalCommand()
		}
		if m.repoPaneCollapsed {
			return m
		}
		return m.focusRepoPane()
	}
	switch m.activePane {
	case ui.PaneRepos:
		return m.focusContentPane(ui.PaneTop)
	case ui.PaneTop:
		return m.focusContentPane(ui.PaneBottom)
	case ui.PaneBottom:
		if terminalEligible {
			return m.focusTerminalCommand()
		}
		if m.repoPaneCollapsed {
			return m.focusContentPane(ui.PaneTop)
		}
		return m.focusRepoPane()
	default:
		return m
	}
}

func (m Model) cyclePaneFocusBackward() Model {
	terminalEligible := m.terminalEffectivelyExpanded() && m.hasActiveEmbeddedTerminal()
	if m.terminalFocus == terminalFocusTerminal {
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
		if m.takeoverVisible() {
			return m.focusContentPane(m.contentPane)
		}
		return m.focusContentPane(ui.PaneBottom)
	}
	if m.takeoverVisible() {
		if m.activePane != ui.PaneRepos {
			if m.repoPaneCollapsed && terminalEligible {
				return m.focusTerminalCommand()
			}
			if m.repoPaneCollapsed {
				return m
			}
			return m.focusRepoPane()
		}
		if terminalEligible {
			m = m.focusContentPane(m.contentPane)
			return m.focusTerminalCommand()
		}
		return m.focusContentPane(m.contentPane)
	}
	switch m.activePane {
	case ui.PaneRepos:
		if terminalEligible {
			m.activePane = ui.PaneBottom
			m.contentPane = ui.PaneBottom
			return m.focusTerminalCommand()
		}
		return m.focusContentPane(ui.PaneBottom)
	case ui.PaneBottom:
		return m.focusContentPane(ui.PaneTop)
	case ui.PaneTop:
		if m.repoPaneCollapsed {
			if terminalEligible {
				return m.focusTerminalCommand()
			}
			return m.focusContentPane(ui.PaneBottom)
		}
		return m.focusRepoPane()
	default:
		return m
	}
}

func (m Model) focusContentPane(pane ui.Pane) Model {
	changed := m.contentPane != pane
	if changed {
		m = m.invalidateViewRequest()
	}
	m.activePane = pane
	m.contentPane = pane
	m.terminalFocus = terminalFocusList
	m.terminalPrefixActive = false
	if m.takeoverVisible() {
		if m.prBabysitterSurfaceVisible() {
			return m.syncPRBabysitterFromCache()
		}
		return m.syncActiveFlowsFromCache()
	}
	if changed {
		m = m.reflowStoredPanes()
	}
	if m.flowSurfaceVisible() {
		return m.syncActiveFlowTerminalToSelectedFlow()
	}
	return m
}

func (m Model) focusTerminalCommand() Model {
	if !m.terminalEffectivelyExpanded() || !m.hasActiveEmbeddedTerminal() {
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
		return m
	}
	m.terminalFocus = terminalFocusTerminal
	m.terminalPrefixActive = true
	return m
}

func (m Model) handleActiveFlowSurfaceKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		return m.handleCursorUp()
	case "down", "j":
		return m.handleCursorDown()
	case "h":
		return m.handleToggleFlowHeadless()
	case "g":
		return m.handleLaunchNextFlowPhase()
	case "R":
		return m.handleRepairSelectedFlow()
	case "s":
		return m.handleStartSelectedFlowWorktreeAgent()
	case "enter":
		return m.handleFlowEnter()
	case "o":
		return m.handleOpenFlowPlanText()
	case "m":
		return m.handleMarkFlowManuallyMerged()
	case "U":
		return m.handleAutofixSelectedFlowPR()
	case "C":
		return m.handleCloseFlow()
	case "a":
		return m.handleToggleFlowAutoMode()
	case "i":
		return m.handleOpenSelectedFlowIssue()
	case "p":
		return m.handleOpenSelectedFlowPR()
	case "y":
		return m.handleCopyFlowWorktreePath()
	case "c":
		return m.handleCopyFlowID()
	case "r":
		return m.handleResumeFlowPhaseSession()
	case "E":
		return m.handleSetReasoningEffort()
	case "M":
		return m.handleSetModel()
	case "x":
		return m.handleResetSelectedFlowPhase()
	case "d":
		return m.handleDelete()
	case "T":
		if m.tmuxLaunchBackend() {
			return m.handleAttachRepoTmuxSession()
		}
	case "q", "ctrl+c", "esc":
		return m.handleEmbeddedTerminalQuitPrefix()
	}
	return m, nil
}

func (m Model) handleHorizontalNavigation(direction int) (tea.Model, tea.Cmd) {
	if direction == 0 {
		return m, nil
	}
	if !m.contentListInputEligible() {
		return m, nil
	}
	currentMode := m.focusedMode()
	nextMode := m.modeAfterHorizontalNavigation(direction)
	if nextMode == currentMode {
		return m, nil
	}
	m, _ = m.selectStoredMode(nextMode)
	m = m.resetModeCursorsForSwitch(currentMode, nextMode)
	if nextMode == ui.ModeFlows {
		return m.startFlowsModeFetchWithRefreshTick()
	}
	return m.startFetchForMode()
}

// modeAfterHorizontalNavigation steps within the focused stored pane: Git and
// Beads wrap in the top pane, while Sessions, Plans, and Flows wrap in the
// bottom pane. Group entries use their remembered subview. Active Flows is
// intentionally outside both cycles.
func (m Model) modeAfterHorizontalNavigation(direction int) ui.Mode {
	mode := m.focusedMode()
	var stops []ui.Mode
	switch m.activePane {
	case ui.PaneTop:
		stops = []ui.Mode{m.lastGitSubview(), m.lastBeadsSubview()}
	case ui.PaneBottom:
		stops = []ui.Mode{ui.ModeSessions, ui.ModePlans, ui.ModeFlows}
	default:
		return mode
	}
	for i, stop := range stops {
		if stop == mode {
			return stops[(i+direction+len(stops))%len(stops)]
		}
	}
	// Only the ungrouped modes reach here, and Active Flows is intercepted
	// before horizontal navigation runs. A mode missing from stops is
	// arrow-unreachable by design, so hold the current view.
	return mode
}

// --- Cursor navigation ---

func (m Model) handleCursorUp() (tea.Model, tea.Cmd) {
	next := m.moveCursor(-1)
	if ui.IsBeadsMode(m.focusedMode()) {
		return next.reconcileBeadExpansion()
	}
	return next, nil
}

func (m Model) handleCursorDown() (tea.Model, tea.Cmd) {
	next := m.moveCursor(1)
	if ui.IsBeadsMode(m.focusedMode()) {
		return next.reconcileBeadExpansion()
	}
	return next, nil
}

// moveCursor moves the selected item in the active right-pane view by delta
// (-1 for up, +1 for down) and keeps the new selection visible.
func (m Model) moveCursor(delta int) Model {
	w := m.contentWidth()
	if m.takeoverVisible() {
		h := m.flowSurfaceContentHeight()
		if next, ok := m.moveSelectedFlowPhase(delta); ok {
			return next
		}
		if m.canScrollExpandedFlow(delta, h) {
			m = m.setCurrentFlowPane(m.currentFlowPane().ScrollBy(delta, h, w))
			return m
		}
		if m.currentFlowPane().Len() <= 1 {
			return m
		}
		before := m.selectedFlowID()
		m = m.setCurrentFlowPane(m.currentFlowPane().Move(delta, h, w))
		if after := m.selectedFlowID(); before != "" && after != before {
			m = m.setExpandedFlowID("")
		}
		m = m.syncActiveFlowTerminalToSelectedFlow()
		return m
	}
	h := m.contentHeightForMode()
	mode := m.focusedMode()
	switch mode {
	case ui.ModeWorktrees:
		if m.inlineWorktreeSessionPath != "" {
			m.worktreeSessions = m.worktreeSessions.Move(delta, m.worktreeSessionContentHeight(), w)
			return m
		}
		m.worktrees = m.worktrees.Move(delta, h, w)
	case ui.ModeBranches:
		m.rows = m.rows.Move(delta, h, w)
	case ui.ModeStashes:
		m.stashes = m.stashes.Move(delta, h, w)
	case ui.ModeHistory:
		m.commits = m.commits.Move(delta, h, w)
	case ui.ModeReflog:
		m.reflogs = m.reflogs.Move(delta, h, w)
	case ui.ModeSessions:
		m.sessions = m.sessions.Move(delta, h, w)
	case ui.ModePlans:
		if next, ok := m.moveSelectedPlanPhase(delta); ok {
			return next
		}
		if m.canScrollExpandedPlan(delta, h) {
			m.plans = m.plans.ScrollBy(delta, h, w)
			return m
		}
		if m.plans.Len() <= 1 {
			return m
		}
		before := m.selectedPlanID()
		m.plans = m.plans.Move(delta, h, w)
		if after := m.selectedPlanID(); before != "" && after != before {
			m = m.setExpandedPlanID("")
		}
	case ui.ModeFlows:
		if next, ok := m.moveSelectedFlowPhase(delta); ok {
			return next
		}
		if m.canScrollExpandedFlow(delta, h) {
			m.flows = m.flows.ScrollBy(delta, h, w)
			return m
		}
		if m.flows.Len() <= 1 {
			return m
		}
		before := m.selectedFlowID()
		m.flows = m.flows.Move(delta, h, w)
		if after := m.selectedFlowID(); before != "" && after != before {
			m = m.setExpandedFlowID("")
		}
		m = m.syncActiveFlowTerminalToSelectedFlow()
	case ui.ModeBeadsReady, ui.ModeBeadsBlocked, ui.ModeBeadsOpen, ui.ModeBeadsInProgress, ui.ModeBeadsClosed:
		index, _ := beadSubviewIndex(mode)
		// A pending subview renders its loading message instead of its retained
		// rows, so cursor keys must not move a selection the user cannot see.
		if m.beads[index].pending {
			return m
		}
		if m.canScrollBeadExpansion(delta, h) {
			m.beads[index].pane = m.beads[index].pane.ScrollBy(delta, h, w)
			return m
		}
		if m.beads[index].pane.Len() <= 1 {
			return m
		}
		m.beads[index].pane = m.beads[index].pane.Move(delta, h, w)
	}
	return m
}

// --- Action handlers ---

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	mode := m.focusedMode()
	if mode == ui.ModeWorktrees {
		if m.inlineWorktreeSessionPath != "" {
			record, ok := m.selectedWorktreeSession()
			if !ok {
				return m, nil
			}
			next, cmd := m.routeSavedSessionResume(record, flowLaunchOriginInlineWorktreeSession)
			return next, cmd
		}
		wt, ok := m.selectedWorktree()
		if ok && wt.Dirty && !wt.Stale {
			m = m.startViewRequest(FetchWorktreeDiff, ui.ModeWorktrees)
			return m, m.fetchWorktreeDiff()
		}
		return m, nil
	}
	if mode == ui.ModeBranches && m.isSelectedBranchDirtyWorktree() {
		m = m.startViewRequest(FetchBranchDiff, ui.ModeBranches)
		return m, m.fetchBranchDiff()
	}
	if mode == ui.ModeStashes && len(m.filteredStashes()) > 0 {
		m = m.startViewRequest(FetchStashDiff, ui.ModeStashes)
		return m, m.fetchStashDiff()
	}
	if mode == ui.ModeHistory && len(m.filteredCommits()) > 0 {
		m = m.startViewRequest(FetchCommitDiff, ui.ModeHistory)
		return m, m.fetchCommitDiff()
	}
	if mode == ui.ModeReflog && len(m.filteredReflogs()) > 0 {
		m = m.startViewRequest(FetchReflogDiff, ui.ModeReflog)
		return m, m.fetchReflogDiff()
	}
	if mode == ui.ModeSessions && len(m.filteredSessions()) > 0 {
		m = m.startViewRequest(FetchSessionTranscript, ui.ModeSessions)
		return m, m.fetchSessionTranscript()
	}
	if ui.IsBeadsMode(mode) {
		if _, ok := m.selectedVisibleBead(); !ok {
			return m, nil
		}
		m = m.startViewRequest(FetchBeadDetail, mode)
		return m, m.fetchBeadDetail()
	}
	if mode == ui.ModePlans && len(m.filteredPlans()) > 0 {
		if planID := m.selectedPlanID(); planID != "" {
			if m.expandedPlanID == planID {
				m = m.setExpandedPlanID("")
			} else {
				m = m.setExpandedPlanID(planID)
			}
		}
		return m, nil
	}
	if m.flowSurfaceVisible() {
		return m.handleFlowEnter()
	}
	return m, nil
}

func (m Model) handleFlowEnter() (tea.Model, tea.Cmd) {
	return m.handleToggleFlowPhases()
}

func (m Model) handleToggleFlowHeadless() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() || len(m.currentFilteredFlows()) == 0 {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" {
		return m, nil
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if repoPath == "" {
		return m, nil
	}
	allRepositories := m.takeoverVisible()
	// The other half of the fence admission holds. Admission refuses a launch
	// while a headless write is outstanding; this refuses the toggle once the
	// launch is past admission, which is the only ordering the launch side
	// cannot see coming.
	//
	// It covers the two headless-aware phase-untracked kinds, and only those:
	// each resolves
	// headless in its prepare stage from a record read under the launch/close
	// lock — autofix from ReserveAgentLaunch, repair from
	// ReserveRepairLaunch by way of refreshFlowRepairLaunchContext. SetHeadless
	// writes under the Flow store's own lock and so is ordered against neither,
	// leaving both free to observe either value; for autofix the
	// route turns on it too, since a headless launch is never tmux-eligible.
	// Generic s ignores the persisted preference and always launches an
	// interactive embedded terminal, so it needs no headless fence.
	//
	// Every tracked kind is deliberately left free to toggle: it resolves
	// headless from the record its own phase write returns, and that write
	// contends with SetHeadless on the store's write lock, so whichever lands
	// second wins and the launcher sees a consistent record either way.
	switch m.flowLaunchAttemptKind(record.FlowID) {
	case flowLaunchKindAutofix, flowLaunchKindRepair:
		return m.setStatus(statusOther, flowHeadlessLaunchInFlightStatus), nil
	}
	// A write is already outstanding for this Flow. Commands run concurrently, so
	// starting a second one would let the two values reach the store in either
	// order. Record the new intent instead and let the in-flight completion
	// persist it. The queued intent keeps the scope of the surface it was
	// entered from, which may differ from the surface that started the write.
	if queued, ok := m.queuedFlowHeadlessValue(record.FlowID); ok {
		return m.updateQueuedFlowHeadlessWrite(record.FlowID, !queued.enabled, allRepositories), nil
	}
	// Launches read the persisted preference under the Flow store lock, so the
	// launch must wait for this write instead of racing it.
	enabled := !record.Headless
	m = m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
		flowID: record.FlowID, repoPath: repoPath, enabled: enabled, allRepositories: allRepositories,
	})
	return m, m.setFlowHeadlessCmd(repoPath, record.FlowID, enabled, allRepositories)
}

// flowHeadlessWritePendingStatus explains why a launch waits for an in-flight
// headless toggle instead of starting in the previous mode.
const flowHeadlessWritePendingStatus = "Applying headless mode change; retry the launch in a moment"

// flowHeadlessLaunchInFlightStatus is that refusal in the other direction: the
// toggle is the thing that waits, because the launch it would race has already
// been admitted. It names the launch rather than the key so the wording holds
// wherever the toggle was pressed from.
const flowHeadlessLaunchInFlightStatus = "A Flow launch is in flight; retry the headless change in a moment"

// pendingFlowHeadlessWrite fences launches for one Flow while a headless write
// is outstanding and carries the value the user last asked for, along with the
// scope of the surface that asked for it. At most one entry exists per Flow:
// toggles arriving while a write is in flight update the entry in place, and the
// completion persists it if it still differs.
type pendingFlowHeadlessWrite struct {
	flowID          string
	repoPath        string
	enabled         bool
	allRepositories bool
}

func (m Model) markFlowHeadlessWritePending(pending pendingFlowHeadlessWrite) Model {
	if pending.flowID == "" {
		return m
	}
	m.pendingFlowHeadlessWrites = append(slices.Clone(m.pendingFlowHeadlessWrites), pending)
	return m
}

func (m Model) updateQueuedFlowHeadlessWrite(flowID string, enabled bool, allRepositories bool) Model {
	index := m.pendingFlowHeadlessWriteIndex(flowID)
	if index < 0 {
		return m
	}
	pending := slices.Clone(m.pendingFlowHeadlessWrites)
	pending[index].enabled = enabled
	// Scope accumulates across the serialized chain. Once any toggle in it came
	// from Active Flows, every result in the chain has to reach the global cache,
	// even if a later toggle was entered from a repository Flow list.
	pending[index].allRepositories = pending[index].allRepositories || allRepositories
	m.pendingFlowHeadlessWrites = pending
	return m
}

func (m Model) clearFlowHeadlessWritePending(flowID string) Model {
	index := m.pendingFlowHeadlessWriteIndex(flowID)
	if index < 0 {
		return m
	}
	m.pendingFlowHeadlessWrites = slices.Delete(slices.Clone(m.pendingFlowHeadlessWrites), index, index+1)
	return m
}

func (m Model) pendingFlowHeadlessWriteIndex(flowID string) int {
	return slices.IndexFunc(m.pendingFlowHeadlessWrites, func(pending pendingFlowHeadlessWrite) bool {
		return pending.flowID == flowID
	})
}

func (m Model) flowHeadlessWritePending(flowID string) bool {
	_, ok := m.queuedFlowHeadlessValue(flowID)
	return ok
}

// queuedFlowHeadlessValue reports the outstanding write for this Flow and the
// value it is expected to leave persisted.
func (m Model) queuedFlowHeadlessValue(flowID string) (pendingFlowHeadlessWrite, bool) {
	index := m.pendingFlowHeadlessWriteIndex(flowID)
	if index < 0 {
		return pendingFlowHeadlessWrite{}, false
	}
	return m.pendingFlowHeadlessWrites[index], true
}

// resolveFlowHeadlessWrite retires the outstanding write for this Flow. When
// toggles arrived while it was in flight, the coalesced intent is persisted by a
// follow-up command so only one write is ever outstanding per Flow. The
// follow-up carries the queued intent's own scope, not the scope of the write
// that just completed.
func (m Model) resolveFlowHeadlessWrite(msg FlowHeadlessSetMsg) (Model, tea.Cmd) {
	queued, ok := m.queuedFlowHeadlessValue(msg.FlowID)
	if !ok {
		return m, nil
	}
	if queued.enabled == msg.Enabled {
		return m.clearFlowHeadlessWritePending(msg.FlowID), nil
	}
	return m, m.setFlowHeadlessCmd(queued.repoPath, msg.FlowID, queued.enabled, queued.allRepositories)
}

func (m Model) setFlowHeadlessCmd(repoPath, flowID string, enabled, allRepositories bool) tea.Cmd {
	return func() tea.Msg {
		flow, err := m.setFlowHeadless(flowstore.HeadlessUpdate{FlowID: flowID, Enabled: enabled})
		if err != nil {
			return FlowHeadlessSetFailedMsg{
				RepoPath: repoPath, FlowID: flowID, Enabled: enabled, AllRepositories: allRepositories,
				Err: fmt.Sprintf("failed to set Flow headless mode: %v", err),
			}
		}
		return FlowHeadlessSetMsg{
			RepoPath: repoPath, FlowID: flowID, Flow: flow, Enabled: enabled, AllRepositories: allRepositories,
		}
	}
}

func (m Model) handleTogglePlanPhases() (tea.Model, tea.Cmd) {
	if m.focusedMode() != ui.ModePlans || len(m.filteredPlans()) == 0 {
		return m, nil
	}
	planID := m.selectedPlanID()
	if planID == "" {
		return m, nil
	}
	if m.expandedPlanID == planID {
		m = m.setExpandedPlanID("")
	} else {
		m = m.setExpandedPlanID(planID)
	}
	return m, nil
}

func (m Model) handleToggleFlowPhases() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() || len(m.currentFilteredFlows()) == 0 {
		return m, nil
	}
	flowID := m.selectedFlowID()
	if flowID == "" {
		return m, nil
	}
	if m.currentExpandedFlowID() == flowID {
		m = m.setExpandedFlowID("")
	} else {
		m = m.setExpandedFlowID(flowID)
	}
	return m, nil
}

func (m Model) handleToggleFlowAutoMode() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() || len(m.currentFilteredFlows()) == 0 {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" || flowstore.FlowClosed(record) {
		return m, nil
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if repoPath == "" {
		return m, nil
	}
	return m, m.setFlowAutoModeCmd(repoPath, record.FlowID, !record.AutoMode)
}

func (m Model) setFlowAutoModeCmd(repoPath, flowID string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		flow, err := m.setFlowAutoMode(flowstore.AutoModeUpdate{
			FlowID:  flowID,
			Enabled: enabled,
		})
		if err != nil {
			return FlowAutoModeSetFailedMsg{
				RepoPath: repoPath,
				FlowID:   flowID,
				Err:      fmt.Sprintf("failed to set Flow auto mode: %v", err),
			}
		}
		return FlowAutoModeSetMsg{
			RepoPath: repoPath,
			FlowID:   flowID,
			Flow:     flow,
			Enabled:  enabled,
		}
	}
}

func (m Model) handleMarkFlowManuallyMerged() (tea.Model, tea.Cmd) {
	record, repoPath, ok := m.selectedManualMergeFlow()
	if !ok {
		return m, nil
	}
	m.modal = modal.OpenConfirm(fmt.Sprintf("Mark PR #%d as merged? (y/n)", record.PR.Number), func() tea.Cmd {
		return m.markFlowManuallyMergedCmd(repoPath, record)
	})
	return m, nil
}

// flowCloseAction is the tri-state C binds to: an open Flow can be closed, a
// closed one reopened, and everything else offers nothing.
type flowCloseAction int

const (
	flowCloseActionNone flowCloseAction = iota
	flowCloseActionClose
	flowCloseActionReopen
)

func flowCloseActionForRecord(record flowstore.FlowRecord) flowCloseAction {
	if record.FlowID == "" {
		return flowCloseActionNone
	}
	if flowstore.FlowClosed(record) {
		return flowCloseActionReopen
	}
	switch flowstore.DeriveStatus(record) {
	case flowstore.StatusAbandoned, flowstore.StatusMerged:
		return flowCloseActionNone
	}
	return flowCloseActionClose
}

// selectedFlowCloseAction resolves what C would do for the selected Flow. It
// deliberately ignores phase-row selection: the footer needs the Flow's own
// closed state even while a phase row is selected, to suppress the r, x and a
// hints. handleCloseFlow applies the phase-row check itself.
func (m Model) selectedFlowCloseAction() (flowstore.FlowRecord, string, flowCloseAction) {
	if !m.flowSurfaceVisible() {
		return flowstore.FlowRecord{}, "", flowCloseActionNone
	}
	record, ok := m.selectedFlow()
	if !ok {
		return flowstore.FlowRecord{}, "", flowCloseActionNone
	}
	action := flowCloseActionForRecord(record)
	if action == flowCloseActionNone {
		return flowstore.FlowRecord{}, "", flowCloseActionNone
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if repoPath == "" {
		return flowstore.FlowRecord{}, "", flowCloseActionNone
	}
	return record, repoPath, action
}

// selectedFlowCloseActionHint is the footer's view of selectedFlowCloseAction.
func (m Model) selectedFlowCloseActionHint() ui.FlowCloseAction {
	_, _, action := m.selectedFlowCloseAction()
	switch action {
	case flowCloseActionClose:
		return ui.FlowCloseActionClose
	case flowCloseActionReopen:
		return ui.FlowCloseActionReopen
	default:
		return ui.FlowCloseActionNone
	}
}

func (m Model) handleCloseFlow() (tea.Model, tea.Cmd) {
	if m.currentSelectedFlowPhaseID() != "" {
		return m, nil
	}
	record, repoPath, action := m.selectedFlowCloseAction()
	switch action {
	case flowCloseActionClose:
		m.modal = modal.OpenSingleLineInput(
			fmt.Sprintf("Close Flow %s (%s)? Reason:", record.Title, record.FlowID),
			ui.FlowCloseReasonInputPlaceholder,
			"",
			validateFlowCloseReason,
			func(reason string) tea.Cmd {
				return m.closeFlowCmd(repoPath, record, reason)
			},
		)
	case flowCloseActionReopen:
		m.modal = modal.OpenConfirm(
			fmt.Sprintf("Reopen Flow %s (%s)? (y/n)", record.Title, record.FlowID),
			func() tea.Cmd {
				return m.reopenFlowCmd(repoPath, record)
			},
		)
	default:
		return m, nil
	}
	return m, nil
}

func validateFlowCloseReason(input string) error {
	if input == "" {
		return fmt.Errorf("enter a reason for closing this flow")
	}
	return nil
}

func (m Model) closeFlowCmd(repoPath string, record flowstore.FlowRecord, reason string) tea.Cmd {
	return func() tea.Msg {
		flow, err := m.closeFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: reason})
		if err != nil {
			return FlowCloseFailedMsg{
				RepoPath: repoPath,
				FlowID:   record.FlowID,
				Err:      fmt.Sprintf("failed to close Flow: %v", err),
			}
		}
		return FlowClosedMsg{RepoPath: repoPath, FlowID: record.FlowID, Flow: flow}
	}
}

func (m Model) reopenFlowCmd(repoPath string, record flowstore.FlowRecord) tea.Cmd {
	return func() tea.Msg {
		flow, err := m.reopenFlow(record.FlowID)
		if err != nil {
			return FlowReopenFailedMsg{
				RepoPath: repoPath,
				FlowID:   record.FlowID,
				Err:      fmt.Sprintf("failed to reopen Flow: %v", err),
			}
		}
		return FlowReopenedMsg{RepoPath: repoPath, FlowID: record.FlowID, Flow: flow}
	}
}

func (m Model) selectedManualMergeFlow() (flowstore.FlowRecord, string, bool) {
	if !m.flowSurfaceVisible() || m.currentSelectedFlowPhaseID() != "" {
		return flowstore.FlowRecord{}, "", false
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" {
		return flowstore.FlowRecord{}, "", false
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if repoPath == "" || !flowManualMergeEligible(record) {
		return flowstore.FlowRecord{}, "", false
	}
	return record, repoPath, true
}

func flowManualMergeEligible(record flowstore.FlowRecord) bool {
	if flowstore.FlowClosed(record) {
		return false
	}
	if record.Status == flowstore.StatusMerged || !flowstore.HasPRTarget(record.PR) {
		return false
	}
	merge, ok := flowstore.FindPhaseByKind(record, flowstore.KindMerge)
	if !ok || !flowstore.PhasePredecessorsSatisfied(record, merge.PhaseID) {
		return false
	}
	switch merge.Status {
	case flowstore.PhaseReady:
		return true
	case flowstore.PhaseCompleted:
		return record.Merge.Status == flowstore.MergePending || record.Merge.Status == flowstore.MergeMerged
	default:
		return false
	}
}

func prBabysitterEligible(record flowstore.FlowRecord) bool {
	if !flowstore.HasPRTarget(record.PR) ||
		record.GraphRecovery.Status == flowstore.GraphRecoveryMissingEdgesUnresolved {
		return false
	}
	switch flowstore.DeriveStatus(record) {
	case flowstore.StatusMerged, flowstore.StatusClosed, flowstore.StatusAbandoned:
		return false
	}
	mergeCount := 0
	var merge flowstore.FlowPhase
	for _, phase := range record.Phases {
		if flowstore.SemanticKind(phase) != flowstore.KindMerge {
			continue
		}
		mergeCount++
		merge = phase
	}
	if mergeCount != 1 || merge.ParentPhaseID != "" || record.Merge.Status == flowstore.MergeMerged {
		return false
	}
	switch merge.Status {
	case flowstore.PhaseReady, flowstore.PhaseCompleted:
		return record.Merge.Status == flowstore.MergePending &&
			flowstore.PhasePredecessorsSatisfied(record, merge.PhaseID)
	case flowstore.PhaseBlocked:
		return record.Merge.Status == flowstore.MergePending || record.Merge.Status == flowstore.MergeBlocked
	default:
		return false
	}
}

func (m Model) markFlowManuallyMergedCmd(repoPath string, record flowstore.FlowRecord) tea.Cmd {
	return func() tea.Msg {
		merge, err := m.lookupPRMerge(record.PR.Number, record.PR.URL)
		if err != nil {
			return FlowManualMergeSetFailedMsg{
				RepoPath: repoPath,
				FlowID:   record.FlowID,
				Err:      fmt.Sprintf("failed to verify PR merge: %v", err),
			}
		}
		flow, err := m.markFlowManualMerge(flowstore.ManualMergeUpdate{
			FlowID:   record.FlowID,
			PRNumber: record.PR.Number,
			PRURL:    record.PR.URL,
			Commit:   merge.Commit,
			MergedAt: merge.MergedAt,
		})
		if err != nil {
			return FlowManualMergeSetFailedMsg{
				RepoPath: repoPath,
				FlowID:   record.FlowID,
				Flow:     flow,
				Err:      fmt.Sprintf("failed to mark Flow as merged: %v", err),
			}
		}
		return FlowManualMergeSetMsg{
			RepoPath: repoPath,
			FlowID:   record.FlowID,
			Flow:     flow,
		}
	}
}

func (m Model) handleToggleWorktreeSessions() (tea.Model, tea.Cmd) {
	if m.focusedMode() != ui.ModeWorktrees || len(m.filteredWorktrees()) == 0 {
		return m, nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	wt, ok := m.selectedWorktree()
	if !ok || wt.Path == "" {
		return m, nil
	}
	if m.inlineWorktreeSessionRepo == repoPath && m.inlineWorktreeSessionPath == wt.Path {
		return m.clearInlineWorktreeSessions(), nil
	}
	var request uint64
	m, request = m.nextWorktreeSessionRequest(repoPath, wt.Path)
	return m, m.fetchWorktreeSessions(wt.Path, request)
}

func (m Model) handleOpenPlanText() (tea.Model, tea.Cmd) {
	if m.focusedMode() == ui.ModePlans && len(m.filteredPlans()) > 0 {
		m = m.startViewRequest(FetchPlanText, ui.ModePlans)
		return m, m.fetchPlanText()
	}
	return m, nil
}

func (m Model) handleOpenFlowPlanText() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() || len(m.currentFilteredFlows()) == 0 {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok {
		return m, nil
	}
	if record.PlanID == "" {
		m = m.setStatus(statusOther, "Flow has no linked plan")
		return m, nil
	}
	mode := m.activeContentFetchMode()
	m = m.startViewRequest(FetchPlanText, mode)
	return m, m.fetchPlanTextByID(record.PlanID, mode)
}

func (m Model) handleEditPlan() (tea.Model, tea.Cmd) {
	if m.focusedMode() != ui.ModePlans || len(m.filteredPlans()) == 0 {
		return m, nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	plan, ok := m.selectedPlan()
	if !ok {
		return m, nil
	}
	planPath, err := m.planMarkdownPath(plan.PlanID)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	launch, err := m.editFile(planPath)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	return m, runPlanEditLaunch(repoPath, launch)
}

func (m Model) handleDelete() (tea.Model, tea.Cmd) {
	if !m.destructive {
		return m, nil
	}
	if m.flowSurfaceVisible() {
		if len(m.currentFilteredFlows()) > 0 && len(m.filteredRepos()) > 0 {
			return m.confirmFlowDelete()
		}
		return m, nil
	}
	mode := m.focusedMode()
	if mode == ui.ModeHistory || mode == ui.ModeReflog {
		return m, nil
	}
	if mode == ui.ModeStashes && len(m.filteredStashes()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmStashDrop()
	}
	if mode == ui.ModeBranches && len(m.filteredRepos()) > 0 {
		return m.confirmBranchDelete()
	}
	if mode == ui.ModeWorktrees && len(m.filteredWorktrees()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmWorktreeDelete()
	}
	return m, nil
}

func (m Model) handleSetAgent() (tea.Model, tea.Cmd) {
	if m.flowPhaseAgentControlsSelected() {
		return m.handleSetFlowPhaseAgent()
	}
	m.modal = modal.OpenSelectWithLayout(
		"Choose interactive helper",
		agentSelectItems(),
		selectedAgentIndex(m.agentCommand),
		modal.Layout{Width: 32, Height: 6, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd { return m.setAgent(agent.Normalize(value)) },
	)
	return m, nil
}

func agentSelectItems() []modal.SelectItem {
	return []modal.SelectItem{
		{Label: agent.CommandCodex, Value: agent.CommandCodex},
		{Label: agent.CommandClaude, Value: agent.CommandClaude},
	}
}

func selectedAgentIndex(command string) int {
	switch agent.Normalize(command) {
	case agent.CommandClaude:
		return 1
	default:
		return 0
	}
}

const inheritFlowPhaseAgentSetting = "__inherit_global__"

func (m Model) selectedFlowPhaseAgentTarget() (flowstore.FlowRecord, flowstore.FlowPhase, bool) {
	record, ok := m.selectedFlow()
	if !ok || record.FlowID != m.currentExpandedFlowID() || m.currentSelectedFlowPhaseID() == "" {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
	}
	phase, ok := flowRecordPhaseByID(record, m.currentSelectedFlowPhaseID())
	return record, phase, ok
}

func (m Model) handleSetFlowPhaseAgent() (tea.Model, tea.Cmd) {
	record, phase, ok := m.selectedFlowPhaseAgentTarget()
	if !ok {
		return m.setStatus(statusOther, "Select an expanded Flow phase before setting its agent"), nil
	}
	items := append([]modal.SelectItem{{Label: "inherit global settings", Value: inheritFlowPhaseAgentSetting}}, agentSelectItems()...)
	selected := 0
	if raw := agent.Normalize(phase.Agent); raw != "" {
		selected = selectedAgentIndex(raw) + 1
	}
	m.modal = modal.OpenSelectWithLayout(
		fmt.Sprintf("Choose agent for phase %s", phase.PhaseID),
		items,
		selected,
		modal.Layout{Width: 36, Height: len(items) + 3, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd {
			settings := flowstore.PhaseAgentSettings{}
			if value != inheritFlowPhaseAgentSetting {
				settings.Agent = agent.Normalize(value)
			}
			return m.setFlowPhaseAgentSettingsCmd(record.FlowID, phase.PhaseID, settings)
		},
	)
	return m, nil
}

func (m Model) handleSetFlowPhaseModel() (tea.Model, tea.Cmd) {
	record, phase, ok := m.selectedFlowPhaseAgentTarget()
	if !ok {
		return m.setStatus(statusOther, "Select an expanded Flow phase before setting its model"), nil
	}
	effective, err := flowstore.ResolvePhaseAgentSettings(m.agentPreferences(), phase.AgentSettings())
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil
	}
	items := append([]modal.SelectItem{{Label: "inherit global", Value: inheritFlowPhaseAgentSetting}}, modelSelectItems(effective.Command)...)
	selected := 0
	if raw := agent.NormalizeModel(phase.Model); raw != "" {
		selected = selectedModelIndex(effective.Command, raw) + 1
	}
	m.modal = modal.OpenSelectWithLayout(
		fmt.Sprintf("Choose model for phase %s", phase.PhaseID), items, selected,
		modal.Layout{Width: 40, Height: len(items) + 3, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd {
			settings := phase.AgentSettings().Normalize()
			if value == inheritFlowPhaseAgentSetting {
				settings.Model = ""
			} else {
				if settings.Agent == "" {
					settings.Agent = effective.Command
				}
				settings.Model = agent.NormalizeModel(value)
			}
			return m.setFlowPhaseAgentSettingsCmd(record.FlowID, phase.PhaseID, settings)
		},
	)
	return m, nil
}

func (m Model) handleSetFlowPhaseReasoningEffort() (tea.Model, tea.Cmd) {
	record, phase, ok := m.selectedFlowPhaseAgentTarget()
	if !ok {
		return m.setStatus(statusOther, "Select an expanded Flow phase before setting reasoning effort"), nil
	}
	effective, err := flowstore.ResolvePhaseAgentSettings(m.agentPreferences(), phase.AgentSettings())
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil
	}
	items := append([]modal.SelectItem{{Label: "inherit global", Value: inheritFlowPhaseAgentSetting}}, reasoningEffortSelectItems(effective.Command)...)
	selected := 0
	if raw := agent.NormalizeReasoningEffort(phase.ReasoningEffort); raw != "" {
		selected = selectedReasoningEffortIndex(effective.Command, raw) + 1
	}
	m.modal = modal.OpenSelectWithLayout(
		fmt.Sprintf("Choose effort for phase %s", phase.PhaseID), items, selected,
		modal.Layout{Width: 40, Height: len(items) + 3, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd {
			settings := phase.AgentSettings().Normalize()
			if value == inheritFlowPhaseAgentSetting {
				settings.ReasoningEffort = ""
			} else {
				if settings.Agent == "" {
					settings.Agent = effective.Command
				}
				settings.ReasoningEffort = agent.NormalizeReasoningEffort(value)
			}
			return m.setFlowPhaseAgentSettingsCmd(record.FlowID, phase.PhaseID, settings)
		},
	)
	return m, nil
}

func (m Model) setFlowPhaseAgentSettingsCmd(flowID, phaseID string, settings flowstore.PhaseAgentSettings) tea.Cmd {
	return func() tea.Msg {
		flow, err := m.setFlowPhaseAgentSettings(flowstore.PhaseAgentSettingsUpdate{FlowID: flowID, PhaseID: phaseID, Settings: settings})
		if err != nil {
			return FlowPhaseAgentSettingsSetFailedMsg{FlowID: flowID, PhaseID: phaseID, Err: err.Error()}
		}
		return FlowPhaseAgentSettingsSetMsg{Flow: flow, PhaseID: phaseID, PhaseIdentity: artifacts.NormalizePhaseID(phaseID)}
	}
}

func (m Model) setAgent(command string) tea.Cmd {
	return func() tea.Msg {
		if err := m.saveAgent(command); err != nil {
			return AgentSetFailedMsg{Command: command, Err: err.Error()}
		}
		return AgentSetMsg{Command: command}
	}
}

func (m Model) handleSetReasoningEffort() (tea.Model, tea.Cmd) {
	if m.flowPhaseAgentControlsSelected() {
		return m.handleSetFlowPhaseReasoningEffort()
	}
	command := agent.Normalize(m.agentCommand)
	if command == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before setting reasoning effort")
		return m, nil
	}
	if err := agent.Validate(command); err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	items := reasoningEffortSelectItems(command)
	m.modal = modal.OpenSelectWithLayout(
		fmt.Sprintf("Choose %s reasoning effort", command),
		items,
		selectedReasoningEffortIndex(command, m.ReasoningEffortFor(command)),
		modal.Layout{Width: 36, Height: len(items) + 3, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd { return m.setReasoningEffort(command, value) },
	)
	return m, nil
}

func (m Model) handleSetModel() (tea.Model, tea.Cmd) {
	if m.flowPhaseAgentControlsSelected() {
		return m.handleSetFlowPhaseModel()
	}
	command := agent.Normalize(m.agentCommand)
	if command == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before setting model")
		return m, nil
	}
	if err := agent.Validate(command); err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	items := modelSelectItems(command)
	m.modal = modal.OpenSelectWithLayout(
		fmt.Sprintf("Choose %s model", command),
		items,
		selectedModelIndex(command, m.ModelFor(command)),
		modal.Layout{Width: 38, Height: len(items) + 3, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd { return m.setModel(command, value) },
	)
	return m, nil
}

func modelSelectItems(command string) []modal.SelectItem {
	choices := agent.ModelChoices(command)
	items := make([]modal.SelectItem, 0, len(choices))
	for _, choice := range choices {
		items = append(items, modal.SelectItem{Label: choice, Value: choice})
	}
	return items
}

func selectedModelIndex(command, model string) int {
	model = modelDisplay(model)
	for i, choice := range agent.ModelChoices(command) {
		if choice == model {
			return i
		}
	}
	return 0
}

func (m Model) setModel(command, selectedModel string) tea.Cmd {
	command = agent.Normalize(command)
	selectedModel = agent.NormalizeModel(selectedModel)
	return func() tea.Msg {
		if err := m.saveAgentModel(command, selectedModel); err != nil {
			return AgentModelSetFailedMsg{Command: command, Model: selectedModel, Err: err.Error()}
		}
		return AgentModelSetMsg{Command: command, Model: selectedModel}
	}
}

func reasoningEffortSelectItems(command string) []modal.SelectItem {
	choices := agent.ReasoningEffortChoices(command)
	items := make([]modal.SelectItem, 0, len(choices))
	for _, choice := range choices {
		items = append(items, modal.SelectItem{Label: choice, Value: choice})
	}
	return items
}

func selectedReasoningEffortIndex(command, effort string) int {
	effort = reasoningEffortDisplay(effort)
	for i, choice := range agent.ReasoningEffortChoices(command) {
		if choice == effort {
			return i
		}
	}
	return 0
}

func (m Model) setReasoningEffort(command, effort string) tea.Cmd {
	command = agent.Normalize(command)
	effort = agent.NormalizeReasoningEffort(effort)
	return func() tea.Msg {
		if err := m.saveAgentReasoningEffort(command, effort); err != nil {
			return AgentReasoningEffortSetFailedMsg{Command: command, Effort: effort, Err: err.Error()}
		}
		return AgentReasoningEffortSetMsg{Command: command, Effort: effort}
	}
}

const (
	repoCreateFormPurpose       = "repo-create"
	repoCreateNameField         = "name"
	repoCreateGitHubField       = "github"
	repoCreateVisibilityField   = "visibility"
	flowCreateFormPurpose       = "flow-create"
	flowCreateTitleField        = "title"
	flowCreateInstructionsField = "instructions"
	flowCreateBaseRefField      = "base-ref"
	flowCreateHeadlessField     = "headless"
	flowCreatePlanNowField      = "plan-now"
)

func (m Model) handleNewRepo() (tea.Model, tea.Cmd) {
	if !m.canCreateRepo() {
		m = m.setStatus(statusOther, "repo creation unavailable: scan root is not configured")
		return m, nil
	}
	m.modal = m.repoCreateForm("", true, actions.RepoVisibilityPublic, "", "")
	return m, nil
}

func (m Model) repoCreateForm(name string, createGitHub bool, visibility actions.RepoVisibility, retryPath, errText string) modal.Modal {
	selectedVisibility := 0
	if visibility == actions.RepoVisibilityPrivate {
		selectedVisibility = 1
	}
	form := modal.OpenForm(modal.FormSpec{
		Purpose: repoCreateFormPurpose,
		Title:   "New repo",
		Fields: []modal.FormField{
			{ID: repoCreateNameField, Kind: modal.FormText, Label: "Repo name", Placeholder: "my-repo", Value: name},
			{ID: repoCreateGitHubField, Kind: modal.FormCheckbox, Label: "Create GitHub repo", Checked: createGitHub},
			{ID: repoCreateVisibilityField, Kind: modal.FormChoice, Label: "Visibility", Options: []modal.SelectItem{
				{Label: "Public", Value: string(actions.RepoVisibilityPublic)},
				{Label: "Private", Value: string(actions.RepoVisibilityPrivate)},
			}, SelectedIndex: selectedVisibility},
		},
		Validate: func(values modal.FormValues) error {
			return validateRepoCreateForm(values, retryPath)
		},
		Submit: func(values modal.FormValues) tea.Cmd {
			opts := m.repoCreateOptionsFromForm(values, retryPath)
			return m.repoCreate(opts, 0)
		},
	})
	if errText != "" {
		form = form.SetFormError(errText)
	}
	return form
}

func validateRepoCreateForm(values modal.FormValues, retryPath string) error {
	name := strings.TrimSpace(values.Text[repoCreateNameField])
	if name == "" {
		return fmt.Errorf("repo name cannot be empty")
	}
	if retryPath != "" {
		retryName := filepath.Base(filepath.Clean(retryPath))
		if name != retryName {
			return fmt.Errorf("repo name must remain %s when retrying GitHub setup", retryName)
		}
		if !values.Checked[repoCreateGitHubField] {
			return fmt.Errorf("GitHub creation must stay enabled when retrying GitHub setup")
		}
	}
	return nil
}

func (m Model) repoCreateOptionsFromForm(values modal.FormValues, retryPath string) actions.RepoCreateOptions {
	visibility := actions.RepoVisibility(values.Choice[repoCreateVisibilityField])
	if visibility != actions.RepoVisibilityPrivate {
		visibility = actions.RepoVisibilityPublic
	}
	opts := actions.RepoCreateOptions{
		Root:         m.repoCreateRoot,
		Name:         values.Text[repoCreateNameField],
		CreateGitHub: values.Checked[repoCreateGitHubField],
		Visibility:   visibility,
	}
	if retryPath != "" {
		opts.RemoteOnlyRetry = true
		opts.ExistingLocalPath = retryPath
	}
	return opts
}

func (m Model) handleNewWorktree(launchAgent bool) (tea.Model, tea.Cmd) {
	if _, ok := m.currentRepoPath(); !ok {
		return m, nil
	}
	if launchAgent && m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching an agent")
		return m, nil
	}
	prompt := "Create worktree from"
	if launchAgent {
		prompt = "Create worktree and launch agent from"
	}
	m.modal = modal.OpenSingleLineInput(
		prompt,
		ui.WorktreeInputPlaceholder,
		"",
		validateWorktreeInput,
		func(input string) tea.Cmd { return m.createWorktree(input, launchAgent, 0) },
	)
	return m, nil
}

func (m Model) handleNewBranch() (tea.Model, tea.Cmd) {
	if _, ok := m.currentRepoPath(); !ok {
		return m, nil
	}
	m.modal = modal.OpenSingleLineInput(
		ui.BranchPrompt,
		ui.BranchInputPlaceholder,
		"",
		validateBranchInput,
		func(input string) tea.Cmd { return m.createBranch(input) },
	)
	return m, nil
}

func (m Model) handleNewPullRequestWorktree() (tea.Model, tea.Cmd) {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	m.modal = modal.OpenSingleLineInput(
		ui.PRWorktreePrompt,
		ui.PRWorktreeInputPlaceholder,
		"",
		func(input string) error { return validatePullRequestWorktreeInput(repoPath, input) },
		func(input string) tea.Cmd { return m.createPullRequestWorktree(input, 0) },
	)
	return m, nil
}

func (m Model) handleNewFlow() (tea.Model, tea.Cmd) {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	m.modal = modal.OpenForm(modal.FormSpec{
		Purpose: flowCreateFormPurpose,
		Title:   "New flow",
		Fields: []modal.FormField{
			{ID: flowCreateTitleField, Kind: modal.FormText, Label: "Title", Placeholder: ui.FlowTitleInputPlaceholder},
			{ID: flowCreateInstructionsField, Kind: modal.FormMultilineText, Label: "Instructions", Placeholder: ui.FlowInstructionsInputPlaceholder},
			{ID: flowCreateBaseRefField, Kind: modal.FormText, Label: "Base ref", Placeholder: ui.FlowBaseRefInputPlaceholder},
			{ID: flowCreateHeadlessField, Kind: modal.FormCheckbox, Label: "Headless", Checked: true},
			{ID: flowCreatePlanNowField, Kind: modal.FormCheckbox, Label: "Plan Now", Checked: true},
		},
		Validate: func(values modal.FormValues) error {
			return m.validateFlowCreateForm(values)
		},
		Submit: func(values modal.FormValues) tea.Cmd {
			if !values.Checked[flowCreatePlanNowField] {
				return m.createFlowForRepo(
					repoPath,
					values.Text[flowCreateTitleField],
					values.Text[flowCreateInstructionsField],
					values.Text[flowCreateBaseRefField],
					values.Checked[flowCreateHeadlessField],
				)
			}
			return m.createFlowAndLaunchPlanForRepo(
				repoPath,
				values.Text[flowCreateTitleField],
				values.Text[flowCreateInstructionsField],
				values.Text[flowCreateBaseRefField],
				values.Checked[flowCreateHeadlessField],
			)
		},
	})
	return m, nil
}

func (m Model) handleFlowCreateFailed(msg FlowCreateFailedMsg) (Model, tea.Cmd) {
	if msg.Request != 0 && !m.isCurrentFlowCreateRequest(msg.Request) {
		return m, nil
	}
	m = m.clearFlowCreateRequest(msg.Request)
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	errText := msg.Err
	if errText == "" {
		errText = "Unable to create flow"
	}
	m = m.setStatus(statusOther, errText)
	if m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func (m Model) handleFlowCreated(msg FlowCreatedMsg) (Model, tea.Cmd) {
	if msg.Request != 0 && !m.isCurrentFlowCreateRequest(msg.Request) {
		return m, nil
	}
	m = m.clearFlowCreateRequest(msg.Request)
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		title = "Flow"
	}
	m = m.setStatus(statusOther, "Created flow: "+title)
	if m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func (m Model) handleReadyBeadFlowCreated(msg ReadyBeadFlowCreatedMsg) (Model, tea.Cmd) {
	current := m.isCurrentReadyBeadFlowCreateRequest(msg.Request)
	token := msg.preparationToken
	if token == 0 && m.flowPreparationOwner.Kind == flowPreparationReadyBead {
		token = m.flowPreparationOwner.Token
	}
	m = m.releaseFlowPreparation(flowPreparationReadyBead, token)
	if !current {
		return m, nil
	}
	// Release the request before the repo check so a repo change that bypassed
	// invalidation can never strand the shortcut.
	m = m.clearReadyBeadFlowCreateRequest(msg.Request)
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		title = "Flow"
	}
	m = m.setStatus(statusOther, "Created flow: "+title)
	if m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func (m Model) handleReadyBeadFlowCreateFailed(msg ReadyBeadFlowCreateFailedMsg) (Model, tea.Cmd) {
	current := m.isCurrentReadyBeadFlowCreateRequest(msg.Request)
	token := msg.preparationToken
	if token == 0 && m.flowPreparationOwner.Kind == flowPreparationReadyBead {
		token = m.flowPreparationOwner.Token
	}
	m = m.releaseFlowPreparation(flowPreparationReadyBead, token)
	if !current {
		return m, nil
	}
	m = m.clearReadyBeadFlowCreateRequest(msg.Request)
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "Unable to create flow"
	}
	if flowID := strings.TrimSpace(msg.FlowID); flowID != "" {
		errText = fmt.Sprintf("Flow %s was created, but preparation failed: %s", flowID, errText)
	}
	m = m.setStatus(statusOther, errText)
	if m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func validateFlowCreateForm(values modal.FormValues) error {
	if err := validateFlowTitleInput(values.Text[flowCreateTitleField]); err != nil {
		return err
	}
	if err := validateFlowInstructionsInput(values.Text[flowCreateInstructionsField]); err != nil {
		return err
	}
	return validateFlowBaseRefInput(values.Text[flowCreateBaseRefField])
}

func (m Model) validateFlowCreateForm(values modal.FormValues) error {
	if err := validateFlowCreateForm(values); err != nil {
		return err
	}
	if values.Checked[flowCreatePlanNowField] && m.agentCommand == "" {
		return fmt.Errorf("Press A to choose %s before launching a flow", ui.AgentInputPlaceholder)
	}
	return nil
}

func validateWorktreeInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter a branch, tag, or new branch name")
	}
	return nil
}

func validateBranchInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter a branch name")
	}
	return nil
}

func validateFlowTitleInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter a flow title")
	}
	return nil
}

func validateFlowInstructionsInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter flow instructions")
	}
	return nil
}

func validateFlowBaseRefInput(string) error {
	return nil
}

func validatePullRequestWorktreeInput(repoPath, input string) error {
	return actions.ValidatePullRequestWorktreeInput(repoPath, input)
}

func (m Model) handleMoveWorktree() (tea.Model, tea.Cmd) {
	wt, ok := m.selectedWorktree()
	if !ok || !canMoveWorktree(wt) {
		return m, nil
	}
	oldPath := wt.Path
	m.modal = modal.OpenSingleLineInput(
		ui.WorktreeMovePrompt,
		ui.WorktreeMoveInputPlaceholder,
		"",
		validateWorktreeMoveInput,
		func(input string) tea.Cmd { return m.moveWorktree(oldPath, input) },
	)
	return m, nil
}

func validateWorktreeMoveInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter a new path or sibling name")
	}
	return nil
}

func canMoveWorktree(wt gitquery.Worktree) bool {
	return !wt.IsMain && !wt.Stale && !wt.Locked
}

func (m Model) canMoveWorktree() bool {
	if m.activePane == ui.PaneRepos || m.focusedMode() != ui.ModeWorktrees {
		return false
	}
	wt, ok := m.selectedWorktree()
	return ok && canMoveWorktree(wt)
}

func (m Model) handleUnlock() (tea.Model, tea.Cmd) {
	if m.focusedMode() != ui.ModeWorktrees {
		return m, nil
	}
	wt, ok := m.selectedWorktree()
	if !ok || !wt.Locked {
		return m, nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	worktreePath := wt.Path
	return m, func() tea.Msg {
		if err := actions.UnlockWorktree(repoPath, worktreePath); err != nil {
			return WorktreeUnlockFailedMsg{RepoPath: repoPath, Err: err.Error()}
		}
		return WorktreeUnlockedMsg{RepoPath: repoPath}
	}
}

func (m Model) handleFetch() (tea.Model, tea.Cmd) {
	repoPath, path, ok := m.fetchTargetPath()
	if !ok {
		return m, nil
	}
	return m, func() tea.Msg {
		if err := m.fetchRepo(path); err != nil {
			return GitFetchFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("fetch failed: %v", err)}
		}
		return GitFetchedMsg{RepoPath: repoPath}
	}
}

func (m Model) handlePull() (tea.Model, tea.Cmd) {
	repoPath, path, ok := m.pullTargetPath()
	if !ok {
		return m, nil
	}
	return m, func() tea.Msg {
		if err := actions.Pull(path); err != nil {
			return GitPullFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("pull failed: %v", err)}
		}
		return GitPulledMsg{RepoPath: repoPath}
	}
}

func (m Model) openAtPath(action func(string) error) (tea.Model, tea.Cmd) {
	path, ok := m.pathForOpenAction()
	if !ok {
		return m, nil
	}
	return m, func() tea.Msg { _ = action(path); return nil }
}

func (m Model) canLaunchAgent() bool {
	if m.activePane == ui.PaneRepos {
		return false
	}
	if m.flowSurfaceVisible() {
		_, ok := m.selectedFlow()
		return ok
	}
	_, ok := m.agentTargetPath()
	return ok
}

func (m Model) canCreateAndLaunchAgent() bool {
	return m.activePane != ui.PaneRepos && m.focusedMode() == ui.ModeWorktrees
}

func (m Model) agentTargetPath() (string, bool) {
	mode := m.focusedMode()
	if mode == ui.ModeWorktrees {
		if _, ok := m.currentRepoPath(); !ok {
			return "", false
		}
		if wt, ok := m.selectedWorktree(); ok && !wt.Stale {
			return wt.Path, true
		}
		return "", false
	}
	if mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok && !row.Stale && row.WorktreePath != "" {
			return row.WorktreePath, true
		}
	}
	return "", false
}

func (m Model) pathForOpenAction() (string, bool) {
	mode := m.focusedMode()
	if mode == ui.ModeWorktrees {
		if _, ok := m.currentRepoPath(); !ok {
			return "", false
		}
		if wt, ok := m.selectedWorktree(); ok && !wt.Stale {
			return wt.Path, true
		}
		return "", false
	}
	if mode == ui.ModeHistory {
		repo, ok := m.currentRepo()
		if !ok || repo.IsBare {
			return "", false
		}
		return repo.Path, true
	}
	if mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok && row.WorktreePath != "" {
			return row.WorktreePath, true
		}
	}
	return "", false
}

func (m Model) handleOpenAgent() (tea.Model, tea.Cmd) {
	if m.flowSurfaceVisible() {
		return m, nil
	}
	path, ok := m.agentTargetPath()
	if !ok {
		return m, nil
	}
	if m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching an agent")
		return m, nil
	}
	return m.launchAgentAtPath(path)
}

func (m Model) handleLaunchNextFlowPhase() (tea.Model, tea.Cmd) {
	record, ok := m.selectedFlow()
	if !ok {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	// An autofix agent (U) writes no phase, so in tmux mode nothing admission can
	// see reports that its agent is still working in this Flow's worktree. The
	// probe goes here rather than in admission for repair's reason — it shells
	// out — and it asks only about that agent: a phase's own live window is
	// already the business of the phase state admission reads.
	if m.tmuxAutofixAgentStillRunning(record, "") {
		return m.setStatus(statusOther, tmuxFlowLiveWindowRefusal), nil
	}
	// Manual launch surfaces the refusal through the returned Model's status,
	// so the admission verdict itself is only AutoMode's concern.
	next, cmd, _ := m.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID,
		Origin: m.flowLaunchOrigin(),
	})
	return next, cmd
}

// handleResetSelectedFlowPhase owns x on a selected Flow phase. Reset keeps
// first refusal; a phase reset cannot recover falls through to session release,
// which is the other half of "recover the selected phase" and applies exactly
// where reset does not.
func (m Model) handleResetSelectedFlowPhase() (tea.Model, tea.Cmd) {
	record, phase, repoPath, ok := m.selectedFlowPhaseResetTarget()
	if !ok {
		return m.handleReleaseSelectedFlowPhaseSession()
	}
	m.modal = modal.OpenConfirm(fmt.Sprintf("Reset Flow phase %s to ready?", phase.PhaseID), func() tea.Cmd {
		return func() tea.Msg {
			return flowPhaseResetConfirmedMsg{
				RepoPath: repoPath,
				FlowID:   record.FlowID,
				PhaseID:  phase.PhaseID,
			}
		}
	})
	return m, nil
}

// selectedFlowPhaseTarget resolves what x acts on, without deciding which
// action applies. Both recoveries need the record, the phase, and a repo path,
// so eligibility is kept out of it: collapsing an ineligible phase into a bare
// false here would discard the target the other recovery still wants.
func (m Model) selectedFlowPhaseTarget() (flowstore.FlowRecord, flowstore.FlowPhase, string, bool) {
	record, ok := m.selectedFlow()
	if !ok {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, "", false
	}
	phase, ok := m.selectedFlowPhase()
	if !ok {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, "", false
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if repoPath == "" {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, "", false
	}
	return record, phase, repoPath, true
}

func (m Model) selectedFlowPhaseResetTarget() (flowstore.FlowRecord, flowstore.FlowPhase, string, bool) {
	record, phase, repoPath, ok := m.selectedFlowPhaseTarget()
	if !ok || !m.flowPhaseResettable(record, phase) {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, "", false
	}
	return record, phase, repoPath, true
}

// flowPhaseResettable feeds both the x handler and its footer hint, so the
// closed-Flow guard here covers AC4 and AC7 in one place.
func (m Model) flowPhaseResettable(record flowstore.FlowRecord, phase flowstore.FlowPhase) bool {
	if flowstore.FlowClosed(record) {
		return false
	}
	_, recoverable := flowstore.RecoverableRunningPhaseResetReason(phase)
	return recoverable &&
		!flowstore.PhaseSessionLaunchMismatch(phase) &&
		flowstore.PhasePredecessorsSatisfied(record, phase.PhaseID) &&
		!m.hasRunningFlowEmbeddedTerminalForPhase(record.FlowID, phase.PhaseID)
}

func (m Model) hasRunningFlowEmbeddedTerminalForPhase(flowID, phaseID string) bool {
	return m.hasRunningFlowEmbeddedTerminalForPhaseLaunch(flowID, phaseID, "")
}

func (m Model) hasRunningFlowEmbeddedTerminalForPhaseLaunch(flowID, phaseID, launchID string) bool {
	wantPhaseID := artifacts.NormalizePhaseID(phaseID)
	if wantPhaseID == "" {
		return false
	}
	for _, slot := range m.embeddedTerminals {
		if flowEmbeddedTerminalSlotMatchesPhaseLaunch(slot, flowID, wantPhaseID, launchID) &&
			embeddedTerminalRunning(slot.Terminal) {
			return true
		}
	}
	return false
}

func (m Model) hasFlowEmbeddedTerminalForPhase(flowID, phaseID string) bool {
	return m.hasFlowEmbeddedTerminalForPhaseLaunch(flowID, phaseID, "")
}

func (m Model) hasFlowEmbeddedTerminalForPhaseLaunch(flowID, phaseID, launchID string) bool {
	wantPhaseID := artifacts.NormalizePhaseID(phaseID)
	if wantPhaseID == "" {
		return false
	}
	for _, slot := range m.embeddedTerminals {
		if flowEmbeddedTerminalSlotMatchesPhaseLaunch(slot, flowID, wantPhaseID, launchID) {
			return true
		}
	}
	return false
}

func (m Model) hasAutoClosingFlowEmbeddedTerminalForPhase(flowID, phaseID string) bool {
	return m.hasAutoClosingFlowEmbeddedTerminalForPhaseLaunch(flowID, phaseID, "")
}

func (m Model) hasAutoClosingFlowEmbeddedTerminalForPhaseLaunch(flowID, phaseID, launchID string) bool {
	wantPhaseID := artifacts.NormalizePhaseID(phaseID)
	if wantPhaseID == "" {
		return false
	}
	for _, slot := range m.embeddedTerminals {
		if flowEmbeddedTerminalSlotMatchesPhaseLaunch(slot, flowID, wantPhaseID, launchID) &&
			flowEmbeddedTerminalAutoCloses(slot.Terminal.State()) {
			return true
		}
	}
	return false
}

func flowEmbeddedTerminalSlotMatchesPhase(slot embeddedTerminalSlot, flowID, normalizedPhaseID string) bool {
	return slot.Scope == embeddedTerminalScopeFlow &&
		slot.FlowID == flowID &&
		artifacts.NormalizePhaseID(slot.FlowPhaseID) == normalizedPhaseID &&
		slot.Terminal != nil
}

func flowEmbeddedTerminalSlotMatchesPhaseLaunch(slot embeddedTerminalSlot, flowID, normalizedPhaseID, launchID string) bool {
	if !flowEmbeddedTerminalSlotMatchesPhase(slot, flowID, normalizedPhaseID) {
		return false
	}
	launchID = strings.TrimSpace(launchID)
	return launchID == "" || strings.TrimSpace(slot.LaunchID) == launchID
}

func (m Model) handleFlowPhaseResetConfirmed(msg flowPhaseResetConfirmedMsg) (Model, tea.Cmd) {
	if !m.takeoverVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if m.hasRunningFlowEmbeddedTerminalForPhase(msg.FlowID, msg.PhaseID) {
		return m.setStatus(statusOther, "Flow phase has an active embedded terminal"), nil
	}
	record, phase, ok := m.flowPhaseByID(msg.FlowID, msg.PhaseID)
	if !ok || !m.flowPhaseResettable(record, phase) {
		return m.setStatus(statusOther, "Flow phase is not awaiting session recovery"), nil
	}
	// A reset makes the phase launchable again, so in tmux mode this is where the
	// live-window probe belongs: flowPhaseResettable feeds the footer hint and
	// must stay allocation-cheap, while this runs once, on a confirmation.
	if m.tmuxPhaseAgentStillRunning(record, phase, msg.RepoPath) {
		return m.setStatus(statusOther, tmuxPhaseLiveWindowRefusal), nil
	}
	return m, m.resetFlowPhaseCmd(msg.RepoPath, msg.FlowID, msg.PhaseID)
}

func (m Model) resetFlowPhaseCmd(repoPath, flowID, phaseID string) tea.Cmd {
	return func() tea.Msg {
		flow, err := m.resetFlowPhase(flowstore.PhaseResetUpdate{
			FlowID:  flowID,
			PhaseID: phaseID,
		})
		if err != nil {
			return flowPhaseResetFailedMsg{
				RepoPath: repoPath,
				FlowID:   flowID,
				PhaseID:  phaseID,
				Err:      fmt.Sprintf("failed to reset Flow phase: %v", err),
			}
		}
		return flowPhaseResetMsg{
			RepoPath: repoPath,
			FlowID:   flowID,
			PhaseID:  phaseID,
			Flow:     flow,
		}
	}
}

func (m Model) handleResumeSession() (tea.Model, tea.Cmd) {
	if m.focusedMode() != ui.ModeSessions {
		return m, nil
	}
	record, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	next, cmd := m.routeSavedSessionResume(record, flowLaunchOriginSessionsPane)
	return next, cmd
}

// resumeSessionForBackend routes a CLI-agent resume to the repo's tmux session
// in tmux mode and to the embedded terminal otherwise. The tmux branch takes
// over the resume reservation exactly as resumeSessionInEmbeddedTerminal's
// external fallback does.
func (m Model) resumeSessionForBackend(ctx actions.AgentLaunchContext, record sessions.SessionRecord, release func()) (Model, tea.Cmd) {
	route, fellBack := m.tmuxLaunchRoute(ctx)
	if route {
		return m.launchAgentInRepoTmuxSession(ctx, release)
	}
	if !fellBack {
		return m.resumeSessionInEmbeddedTerminal(ctx, record, release)
	}
	// Not tmuxFallbackNote: resumeSessionInEmbeddedTerminal falls through to an
	// external launch when the agent has no embedded slot, so this path cannot
	// promise the embedded terminal. The note is set now for the embedded case
	// and also handed down as the external case's launched status, because that
	// launch's own AgentResultMsg would otherwise overwrite it a moment later.
	m = m.setStatus(statusOther, tmuxUnavailableNote)
	externalStatus := withFallbackNote(agentLaunchedStatus(ctx.Command), tmuxUnavailableNote)
	return m.resumeSessionInEmbeddedTerminalWithStatus(ctx, record, release, externalStatus)
}

func (m Model) handleResumeFlowPhaseSession() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" || record.FlowID != m.currentExpandedFlowID() || m.currentSelectedFlowPhaseID() == "" {
		return m, nil
	}
	// A resume spawns an agent and records a launch, and the store-side auto
	// gate does not cover it, so a closed Flow has to refuse here.
	if flowstore.FlowClosed(record) {
		return m, nil
	}
	phase, ok := m.selectedFlowPhase()
	if !ok {
		return m, nil
	}
	// Everything past surface, selection, and closure is the lifecycle's: the
	// resolver owns the snapshot refusals, and admission owns occupancy. The
	// check order is unchanged, because occupancy already ran last.
	intent, refusal, ok := m.flowPhaseResumeRequest(record, phase)
	if !ok {
		if refusal == "" {
			return m, nil
		}
		return m.setStatus(statusOther, refusal), nil
	}
	// Nothing above stops a repeat resume of a terminal phase, and in tmux mode
	// the embedded slot that would have is absent. Resuming twice would run the
	// same provider session in two windows, so the probe goes here, on the
	// keystroke that admits the resume. It sits below the resolver so the
	// subprocess only runs for otherwise-resumable phases.
	if m.tmuxPhaseAgentStillRunning(record, phase, record.WorktreePath) {
		return m.setStatus(statusOther, tmuxPhaseLiveWindowRefusal), nil
	}
	// A resumed agent lands in the same worktree a phase-untracked autofix agent
	// is working in, and no phase records that one, so it needs the Flow-scoped
	// half too.
	//
	// The subprocess stays off the common path regardless:
	// tmuxAutofixAgentStillRunning reads the registry first and only shells out
	// for a Flow that actually launched an autofix agent in this process.
	if m.tmuxAutofixAgentStillRunning(record, record.WorktreePath) {
		return m.setStatus(statusOther, tmuxFlowLiveWindowRefusal), nil
	}
	intent.Origin = m.flowLaunchOrigin()
	next, cmd, _ := m.requestFlowLaunch(intent)
	return next, cmd
}

// sessionResumeLaunchContext builds the established non-Flow resume route.
// Flow-associated records are refreshed and reserved by the launch lifecycle
// before they can reach this helper.
func (m Model) sessionResumeLaunchContext(record sessions.SessionRecord) (actions.AgentLaunchContext, func(), bool, Model) {
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		m = m.setStatus(statusOther, "Session has no provider session ID and cannot be resumed")
		return actions.AgentLaunchContext{}, nil, false, m
	}
	// A resumed session's provider hook argv carries the pinned path just as a
	// fresh launch's does, and its agent runs `approach plan` commands against
	// the same store. Refused here, where the route still has a status channel.
	if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
		return actions.AgentLaunchContext{}, nil, false, m.setStatus(statusOther, refusal)
	}
	command := string(record.Provider)
	workingDir := record.CWD
	if workingDir == "" {
		workingDir = record.WorktreePath
	}
	if workingDir == "" {
		m = m.setStatus(statusOther, "Session has no worktree path or cwd to resume from")
		return actions.AgentLaunchContext{}, nil, false, m
	}
	// In tmux mode there is no embedded slot to make a still-running agent
	// visible, so nothing else stops a resume from starting a second process on
	// a session whose first one is alive in a window. Probed here, above the
	// reservation, because this is the one admission point all three
	// record-based resumes share and there is nothing to release yet.
	if m.tmuxSessionAgentStillRunning(record, command) {
		m = m.setStatus(statusOther, tmuxSessionLiveWindowRefusal)
		return actions.AgentLaunchContext{}, nil, false, m
	}
	ctx := actions.AgentLaunchContext{
		Command:          command,
		LaunchID:         newLaunchID(),
		RepoPath:         record.RepoPath,
		WorktreePath:     record.WorktreePath,
		WorkingDir:       workingDir,
		Branch:           record.Branch,
		Commit:           record.Commit,
		SessionStateRoot: m.sessionStateRoot,
		ResumeSessionID:  sessionID,
		PlanID:           record.PlanID,
		PlanPath:         record.PlanPath,
	}
	return applyLaunchPin(ctx, m.launchPin), func() {}, true, m
}

func (m Model) handleImplementPlan() (tea.Model, tea.Cmd) {
	ctx, ok, next := m.planLaunchContext()
	if !ok {
		return next, nil
	}
	m = next
	m.modal = modal.OpenMultiLineInput(
		ui.LaunchInstructionsPrompt,
		"launch instructions",
		ctx.InitialPrompt,
		validatePlanLaunchInput,
		func(input string) tea.Cmd {
			ctx.InitialPrompt = input
			return func() tea.Msg { return agentLaunchRequestedMsg{LaunchContext: ctx} }
		},
	)
	return m, nil
}

func (m Model) planLaunchContext() (actions.AgentLaunchContext, bool, Model) {
	repoPath, repoOK := m.currentRepoPath()
	plan, ok := m.selectedPlan()
	if !ok {
		if !repoOK {
			m = m.setStatus(statusOther, "Cannot determine launch path for this plan")
		}
		return actions.AgentLaunchContext{}, false, m
	}
	if m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching an agent")
		return actions.AgentLaunchContext{}, false, m
	}
	// Refused before the launch-instructions modal opens rather than after the
	// user has typed into it: this agent persists plan phases through the pinned
	// binary, so an unusable pin is worth reporting at the keypress.
	if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
		return actions.AgentLaunchContext{}, false, m.setStatus(statusOther, refusal)
	}
	planPath, err := m.planMarkdownPath(plan.PlanID)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return actions.AgentLaunchContext{}, false, m
	}
	if plan.RepoPath != "" {
		repoPath = plan.RepoPath
	}
	launchPath := plan.WorktreePath
	if launchPath == "" {
		launchPath = plan.RepoPath
	}
	if launchPath == "" {
		launchPath = repoPath
	}
	if launchPath == "" {
		m = m.setStatus(statusOther, "Cannot determine launch path for this plan")
		return actions.AgentLaunchContext{}, false, m
	}
	launch := m.launchAgentSettings(m.agentCommand)
	ctx := actions.AgentLaunchContext{
		Command:          m.agentCommand,
		Model:            launch.Model,
		ReasoningEffort:  launch.ReasoningEffort,
		LaunchID:         newLaunchID(),
		RepoPath:         repoPath,
		WorktreePath:     launchPath,
		Branch:           plan.Branch,
		Commit:           plan.Commit,
		SessionStateRoot: m.sessionStateRoot,
		PlanID:           plan.PlanID,
		PlanPath:         planPath,
		InitialPrompt:    m.implementationPrompt(plan, planPath, repoPath, launchPath),
	}
	if phase, ok := m.selectedPlanPhase(); ok {
		ctx.PlanPhaseID = phase.PhaseID
		ctx.PlanPhaseTitle = phase.Title
		ctx.PlanPhaseStatus = phase.Status
		ctx.InitialPrompt = m.implementationPromptForPhase(plan, planPath, repoPath, launchPath, phase)
	}
	return applyLaunchPin(ctx, m.launchPin), true, m
}

func validatePlanLaunchInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter launch instructions")
	}
	return nil
}

func (m Model) implementationPrompt(plan planstore.PlanRecord, planPath, repoPath, worktreePath string) string {
	return m.renderPlanPromptTemplate(plan, planPath, repoPath, worktreePath, planstore.PlanPhase{}, false)
}

func (m Model) implementationPromptForPhase(plan planstore.PlanRecord, planPath, repoPath, worktreePath string, phase planstore.PlanPhase) string {
	return m.renderPlanPromptTemplate(plan, planPath, repoPath, worktreePath, phase, true)
}

func (m Model) renderPlanPromptTemplate(plan planstore.PlanRecord, planPath, repoPath, worktreePath string, phase planstore.PlanPhase, phaseSelected bool) string {
	template := m.planPromptTemplate
	if strings.TrimSpace(template) == "" {
		if phaseSelected {
			return defaultImplementationPromptForPhase(plan, planPath, phase)
		}
		return defaultImplementationPrompt(plan, planPath)
	}
	title := plan.Title
	if title == "" {
		title = "(untitled)"
	}
	replacer := strings.NewReplacer(
		"{title}", title,
		"{plan_id}", plan.PlanID,
		"{plan_path}", planPath,
		"{repo_path}", repoPath,
		"{worktree_path}", worktreePath,
	)
	if phaseSelected {
		phaseTitle := phase.Title
		if phaseTitle == "" {
			phaseTitle = "(untitled)"
		}
		phaseStatus := phase.Status
		if phaseStatus == "" {
			phaseStatus = "(unknown)"
		}
		replacer = strings.NewReplacer(
			"{title}", title,
			"{plan_id}", plan.PlanID,
			"{plan_path}", planPath,
			"{repo_path}", repoPath,
			"{worktree_path}", worktreePath,
			"{phase_id}", phase.PhaseID,
			"{phase_title}", phaseTitle,
			"{phase_status}", phaseStatus,
		)
	}
	return replacer.Replace(template)
}

func defaultImplementationPrompt(plan planstore.PlanRecord, planPath string) string {
	title := plan.Title
	if title == "" {
		title = "(untitled)"
	}
	return fmt.Sprintf("Implement the saved approach plan %q (ID: %s) at %s. Read the plan file, then begin implementation.", title, plan.PlanID, planPath)
}

func defaultImplementationPromptForPhase(plan planstore.PlanRecord, planPath string, phase planstore.PlanPhase) string {
	title := plan.Title
	if title == "" {
		title = "(untitled)"
	}
	phaseTitle := phase.Title
	if phaseTitle == "" {
		phaseTitle = "(untitled)"
	}
	phaseStatus := phase.Status
	if phaseStatus == "" {
		phaseStatus = "(unknown)"
	}
	return fmt.Sprintf("Implement only the selected phase of the saved approach plan %q (ID: %s) at %s. Selected phase: %s (%q), status %s. Read the plan file, then begin implementation of only that phase.", title, plan.PlanID, planPath, phase.PhaseID, phaseTitle, phaseStatus)
}

func flowPhaseByID(record flowstore.FlowRecord, phaseID string) (flowstore.FlowPhase, bool) {
	requested := strings.TrimSpace(phaseID)
	for _, phase := range record.Phases {
		if phase.PhaseID == requested {
			return phase, true
		}
	}
	want := artifacts.NormalizePhaseID(requested)
	for _, phase := range record.Phases {
		if artifacts.NormalizePhaseID(phase.PhaseID) == want {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

// launchAgentAtPath and its branch-carrying sibling are the two entry points for
// the plain repository agent. agentLaunchContext returns a context and nothing
// else, so the refusal lives here, where there is still a status channel — and
// in both, because either one alone would leave the other on an unverified pin.
func (m Model) launchAgentAtPath(path string) (Model, tea.Cmd) {
	if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
		return m.setStatus(statusOther, refusal), nil
	}
	ctx := m.agentLaunchContext(path)
	return m.launchAgentForBackend(ctx, nil)
}

func (m Model) launchAgentAtPathWithBranch(path string, branch *string) (Model, tea.Cmd) {
	if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
		return m.setStatus(statusOther, refusal), nil
	}
	ctx := m.agentLaunchContext(path)
	if branch != nil {
		ctx.Branch = *branch
	}
	return m.launchAgentForBackend(ctx, nil)
}

func (m Model) launchAgentWithContextReservation(ctx actions.AgentLaunchContext, release func()) (Model, tea.Cmd) {
	return m.launchAgentWithContextStatus(ctx, release, "")
}

func (m Model) launchAgentWithContextStatus(ctx actions.AgentLaunchContext, release func(), launchedStatus string) (Model, tea.Cmd) {
	launch, err := m.launchAgent(ctx)
	if err != nil {
		releaseFlowLaunchReservation(release)
		return m.startFlowLaunchFailure(ctx, err.Error())
	}
	return m.runAgentLaunchWithStatus(ctx, launch, release, launchedStatus)
}

func (m Model) updateFlowTerminalFocusAfterLaunch(ctx actions.AgentLaunchContext) Model {
	if ctx.FlowAgent || ctx.FlowSavedSessionResume {
		return m.focusEmbeddedTerminalInput()
	}
	if !m.flowSurfaceVisible() {
		return m
	}
	if ctx.FlowAutoLaunch {
		return m
	}
	if ctx.Headless {
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
		return m
	}
	return m.focusEmbeddedTerminalInput()
}

func (m Model) runAgentLaunchWithContext(ctx actions.AgentLaunchContext, launch actions.TerminalLaunchSpec) (Model, tea.Cmd) {
	return m.runAgentLaunchWithReservation(ctx, launch, nil)
}

func (m Model) runAgentLaunchWithReservation(ctx actions.AgentLaunchContext, launch actions.TerminalLaunchSpec, heldRelease func()) (Model, tea.Cmd) {
	return m.runAgentLaunchWithStatus(ctx, launch, heldRelease, "")
}

// runAgentLaunchWithStatus is runAgentLaunchWithReservation with a transport
// specific success message.
//
// Where the status is set differs by transport. A detached launch's own
// AgentResultMsg lands after this returns and would replace an eagerly set
// status with the generic text, so it travels on the result. An interactive
// launch's result lands only when the user's agent session ends, which can be
// hours later and would read as a fresh launch announcement, so it is set here
// instead — before the TTY handover.
func (m Model) runAgentLaunchWithStatus(ctx actions.AgentLaunchContext, launch actions.TerminalLaunchSpec, heldRelease func(), launchedStatus string) (Model, tea.Cmd) {
	if launch.Interactive {
		if strings.TrimSpace(launchedStatus) != "" {
			m = m.setStatus(statusOther, launchedStatus)
		}
		release := heldRelease
		if release == nil {
			var err error
			release, err = m.reserveFlowSpawn(ctx)
			if err != nil {
				return m.startFlowLaunchFailure(ctx, err.Error())
			}
		}
		// approach hands over the TTY until the launch command exits. Some launch
		// commands are only terminal/multiplexer clients; launch.Detached records
		// whether provider hooks, not this result, own session completion.
		return m, tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			defer release()
			if err != nil {
				if launch.Cleanup != nil {
					launch.Cleanup()
				}
				return AgentResultMsg{LaunchContext: ctx, Err: launchErrorMessage(err, launch), Detached: launch.Detached}
			}
			// No LaunchedStatus: it was set above, and repeating it when the
			// session ends would announce the launch a second time.
			return AgentResultMsg{LaunchContext: ctx, Detached: launch.Detached}
		})
	}
	// Detached launch: the command only opens or switches to an external
	// terminal/multiplexer session and returns while the agent keeps running.
	return m, func() tea.Msg {
		release := heldRelease
		if release == nil {
			var err error
			release, err = m.reserveFlowSpawn(ctx)
			if err != nil {
				return AgentResultMsg{LaunchContext: ctx, Err: err.Error(), Detached: true}
			}
		}
		defer release()
		if err := launch.Cmd.Run(); err != nil {
			if launch.Cleanup != nil {
				launch.Cleanup()
			}
			return AgentResultMsg{LaunchContext: ctx, Err: launchErrorMessage(err, launch), Detached: true}
		}
		return AgentResultMsg{LaunchContext: ctx, Detached: true, LaunchedStatus: launchedStatus}
	}
}

// launchErrorMessage prefers a transport's captured diagnostic over the bare
// exit status a wrapper script's failure otherwise reduces to. A Flow launch
// persists this string into the phase's needs_attention note, so it is the only
// durable record of why the spawn failed.
func launchErrorMessage(err error, launch actions.TerminalLaunchSpec) string {
	if launch.ErrorDetail == nil {
		return err.Error()
	}
	detail := strings.TrimSpace(launch.ErrorDetail())
	if detail == "" {
		return err.Error()
	}
	return err.Error() + ": " + detail
}

func (m Model) reserveTrackedFlowLaunch(flowID string) (flowstore.FlowRecord, func(), error) {
	if m.reserveFlowLaunch == nil {
		return flowstore.FlowRecord{}, func() {}, nil
	}
	return m.reserveFlowLaunch(flowID)
}

func releaseFlowLaunchReservation(release func()) {
	if release != nil {
		release()
	}
}

func (m Model) reserveFlowSpawn(ctx actions.AgentLaunchContext) (func(), error) {
	tracked := ctx.FlowLaunchTracked
	if strings.TrimSpace(ctx.FlowID) == "" || !tracked || ctx.FlowRepair || m.reserveFlowLaunch == nil {
		return func() {}, nil
	}
	_, release, err := m.reserveFlowLaunch(ctx.FlowID)
	return release, err
}

func (m Model) flowLaunchFailureUpdate(ctx actions.AgentLaunchContext, errText string) (flowstore.PhaseUpdate, bool) {
	if ctx.FlowID == "" || ctx.FlowPhaseID == "" || (ctx.ResumeSessionID != "" && !ctx.FlowLaunchTracked) {
		return flowstore.PhaseUpdate{}, false
	}
	if ctx.FlowPhaseTerminal {
		// The phase had already finished when this launch (a session resume)
		// started; a failed resume must not regress it to needs_attention.
		return flowstore.PhaseUpdate{}, false
	}
	notes := "Agent launch failed"
	if errText != "" {
		notes += ": " + errText
	}
	status := flowstore.PhaseNeedsAttention
	outcome := ""
	if ctx.FlowPhaseKind == flowstore.KindPlanReview {
		status = flowstore.PhaseBlocked
		outcome = flowstore.OutcomeBlocked
	} else if _, phase, ok := m.flowPhaseByID(ctx.FlowID, ctx.FlowPhaseID); ok && flowstore.SemanticKind(phase) == flowstore.KindPlanReview {
		status = flowstore.PhaseBlocked
		outcome = flowstore.OutcomeBlocked
	} else if ctx.FlowPhaseID == "plan-review" {
		status = flowstore.PhaseBlocked
		outcome = flowstore.OutcomeBlocked
	}
	return flowstore.PhaseUpdate{
		FlowID:  ctx.FlowID,
		PhaseID: ctx.FlowPhaseID,
		Status:  status,
		Outcome: outcome,
		Notes:   notes,
	}, true
}

func (m Model) startFlowLaunchFailure(ctx actions.AgentLaunchContext, errText string) (Model, tea.Cmd) {
	return m.startFlowLaunchFailureWithReadyAdmission(ctx, errText, false)
}

func (m Model) startFlowLaunchFailureWithReadyAdmission(ctx actions.AgentLaunchContext, errText string, releaseReadyBeadAdmission bool) (Model, tea.Cmd) {
	kind, token := flowPreparationNone, uint64(0)
	if releaseReadyBeadAdmission {
		kind, token = m.flowPreparationOwner.Kind, m.flowPreparationOwner.Token
	}
	return m.startFlowLaunchFailureWithPreparationOwner(ctx, errText, kind, token)
}

func (m Model) startFlowLaunchFailureWithPreparationOwner(ctx actions.AgentLaunchContext, errText string, kind flowPreparationKind, token uint64) (Model, tea.Cmd) {
	update, ok := m.flowLaunchFailureUpdate(ctx, errText)
	if !ok {
		m = m.releaseFlowPreparation(kind, token)
		return m.setStatus(statusOther, errText), nil
	}
	return m, func() tea.Msg {
		_, err := m.setFlowPhase(update)
		return flowLaunchFailurePersistedMsg{
			LaunchContext:             ctx,
			OriginalErr:               errText,
			PersistErr:                err,
			releaseReadyBeadAdmission: kind == flowPreparationReadyBead,
			preparationKind:           kind,
			preparationToken:          token,
		}
	}
}

func (m Model) handleFlowLaunchFailurePersisted(msg flowLaunchFailurePersistedMsg) (Model, tea.Cmd) {
	releaseFlowLaunchReservation(msg.Release)
	ctx := msg.LaunchContext
	presentCreate := true
	if msg.Create != nil {
		presentCreate = m.createFlowLaunchOriginCurrent(*msg.Create)
	}
	if msg.releaseReadyBeadAdmission {
		m = m.releaseFlowPreparation(msg.preparationKind, msg.preparationToken)
	}
	// A mismatch skips only the release: this message is shared with every
	// source that has no lifecycle attempt, and returning early would swallow
	// their status and Flow surface refresh.
	if attempt, ok := m.matchingFlowLaunchAttempt(ctx.FlowID, ctx.LaunchID, 0, flowLaunchStateFailurePersisting); ok {
		if attempt.Kind == flowLaunchKindCreatePhase {
			presentCreate = m.createFlowLaunchOriginCurrent(attempt.Create)
			if msg.Create == nil {
				m = m.clearFlowLaunchCreatePresentation(attempt.Create)
			}
		}
		m = m.releaseFlowLaunchAttempt(ctx.FlowID, ctx.LaunchID)
	}
	// A create-tagged prefill correction may lose re-reservation to a newer
	// exact-Flow attempt. Request cleanup is independently fenced by the request
	// ID, so it must not depend on finding the old attempt; attempt release above
	// remains token-fenced and cannot touch the winner.
	if msg.Create != nil {
		m = m.clearFlowLaunchCreatePresentation(*msg.Create)
	}
	if !presentCreate {
		return m, nil
	}
	errText := msg.OriginalErr
	if msg.PersistErr != nil {
		if errText != "" {
			errText += "; "
		}
		errText += "update flow phase: " + msg.PersistErr.Error()
	}
	m = m.setStatus(statusOther, errText)
	if msg.LaunchContext.FlowID != "" && m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

// agentLaunchedStatus describes a successful detached launch without implying
// the agent has finished; the agent keeps running in its terminal session.
func agentLaunchedStatus(command string) string {
	if command == "" {
		return "Launched agent in a terminal session"
	}
	return fmt.Sprintf("Launched %s in a terminal session", command)
}

func (m Model) agentLaunchContext(path string) actions.AgentLaunchContext {
	repoPath, _ := m.currentRepoPath()
	branch := ""
	commit := ""
	mode := m.focusedMode()
	if mode == ui.ModeWorktrees {
		if wt, ok := m.selectedWorktree(); ok {
			branch = wt.BranchName
			commit = wt.Commit
		}
	}
	if mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok {
			branch = row.Branch.Name
		}
	}
	launch := m.launchAgentSettings(m.agentCommand)
	return applyLaunchPin(actions.AgentLaunchContext{
		Command:          m.agentCommand,
		Model:            launch.Model,
		ReasoningEffort:  launch.ReasoningEffort,
		LaunchID:         newLaunchID(),
		RepoPath:         repoPath,
		WorktreePath:     path,
		Branch:           branch,
		Commit:           commit,
		SessionStateRoot: m.sessionStateRoot,
	}, m.launchPin)
}

func (m Model) handleOpenTerminal() (tea.Model, tea.Cmd) {
	path, ok := m.pathForOpenAction()
	if !ok {
		return m, nil
	}
	launch, err := m.launchTerminal(path)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	if launch.Interactive {
		return m, tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			if err != nil {
				return TerminalResultMsg{Err: err.Error()}
			}
			return TerminalResultMsg{}
		})
	}
	return m, func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			return TerminalResultMsg{Err: err.Error()}
		}
		return TerminalResultMsg{}
	}
}

func (m Model) handleOpenCode() (tea.Model, tea.Cmd) {
	return m.openAtPath(m.openCode)
}

func (m Model) pageBody(body string) (Model, tea.Cmd) {
	launch, err := m.pageText(body)
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil
	}
	return m, runTerminalLaunch(launch)
}

func runTerminalLaunch(launch actions.TerminalLaunchSpec) tea.Cmd {
	if launch.Interactive {
		return tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			if err != nil {
				if launch.Cleanup != nil {
					launch.Cleanup()
				}
				return TerminalResultMsg{Err: err.Error()}
			}
			return TerminalResultMsg{}
		})
	}
	return func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			if launch.Cleanup != nil {
				launch.Cleanup()
			}
			return TerminalResultMsg{Err: err.Error()}
		}
		return TerminalResultMsg{}
	}
}

func runPlanEditLaunch(repoPath string, launch actions.TerminalLaunchSpec) tea.Cmd {
	if launch.Interactive {
		return tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			if err != nil {
				if launch.Cleanup != nil {
					launch.Cleanup()
				}
				return PlanEditResultMsg{RepoPath: repoPath, Err: err.Error()}
			}
			return PlanEditResultMsg{RepoPath: repoPath}
		})
	}
	return func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			if launch.Cleanup != nil {
				launch.Cleanup()
			}
			return PlanEditResultMsg{RepoPath: repoPath, Err: err.Error()}
		}
		return PlanEditResultMsg{RepoPath: repoPath}
	}
}

// --- Confirm dialogs ---

func (m Model) confirmStashDrop() (tea.Model, tea.Cmd) {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	stash, ok := m.selectedStash()
	if !ok {
		return m, nil
	}
	idx := stash.Index
	m.modal = modal.OpenConfirm(fmt.Sprintf("Drop stash@{%d}? (y/n)", idx), func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.DropStash(repoPath, idx); err != nil {
				return ActionFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("failed to drop stash: %v", err)}
			}
			return StashDroppedMsg{RepoPath: repoPath}
		}
	})
	return m, nil
}

func (m Model) confirmBranchDelete() (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}

	// Root branch cannot be deleted
	if samePath(row.WorktreePath, repoPath) {
		return m, nil
	}
	if repo, ok := m.currentRepo(); ok && repo.IsBare && row.WorktreePath != "" {
		return m, nil
	}

	branchName := row.Branch.Name
	m.modal = modal.OpenConfirm(fmt.Sprintf("Delete branch %s? (y/n)", branchName), func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.DeleteBranch(repoPath, branchName); err != nil {
				return DeleteFailedMsg{
					RepoPath:    repoPath,
					Target:      branchName,
					ForceAction: func() error { return actions.ForceDeleteBranch(repoPath, branchName) },
				}
			}
			return BranchDeletedMsg{RepoPath: repoPath}
		}
	})
	return m, nil
}

func (m Model) confirmWorktreeDelete() (tea.Model, tea.Cmd) {
	wt, ok := m.selectedWorktree()
	if !ok {
		return m, nil
	}
	if wt.IsMain {
		return m, nil
	}
	if wt.Locked {
		return m, nil
	}
	if wt.Stale {
		return m, nil
	}

	repoPath, ok2 := m.currentRepoPath()
	if !ok2 {
		return m, nil
	}
	wtPath := wt.Path
	branchName := wt.BranchName
	if wt.Detached {
		branchName = ""
	}

	m.modal = modal.OpenConfirm(fmt.Sprintf("Remove worktree at %s? (y/n)", wtPath), func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.RemoveWorktree(repoPath, wtPath); err != nil {
				return DeleteFailedMsg{
					RepoPath:    repoPath,
					Target:      wtPath,
					ForceAction: func() error { return actions.ForceRemoveWorktree(repoPath, wtPath) },
					SuccessMsg:  WorktreeRemovedMsg{RepoPath: repoPath, BranchName: branchName},
				}
			}
			return WorktreeRemovedMsg{RepoPath: repoPath, BranchName: branchName}
		}
	})
	return m, nil
}

func (m Model) confirmFlowDelete() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	if _, ok := m.selectedFlowPhase(); ok {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" {
		return m, nil
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if repoPath == "" {
		return m, nil
	}
	flowID := record.FlowID
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = flowID
	}
	m.modal = modal.OpenConfirm(
		fmt.Sprintf("Delete Flow %s (%s)? Flow data only; worktrees/code stay. (y/n)", title, flowID),
		func() tea.Cmd { return m.deleteFlowCommand(repoPath, flowID, title) },
	)
	return m, nil
}

func (m Model) handlePrune() (tea.Model, tea.Cmd) {
	if !m.destructive {
		return m, nil
	}
	if m.focusedMode() == ui.ModeWorktrees && len(m.filteredWorktrees()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmWorktreePrune()
	}
	return m, nil
}

func (m Model) confirmWorktreePrune() (tea.Model, tea.Cmd) {
	wt, ok := m.selectedWorktree()
	if !ok {
		return m, nil
	}
	if !wt.Stale || wt.Locked {
		return m, nil
	}

	repoPath, ok2 := m.currentRepoPath()
	if !ok2 {
		return m, nil
	}
	m.modal = modal.OpenConfirm("Prune stale worktrees? (y/n)", func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.PruneWorktree(repoPath); err != nil {
				return ActionFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("failed to prune worktrees: %v", err)}
			}
			return WorktreePrunedMsg{RepoPath: repoPath}
		}
	})
	return m, nil
}

// resetBottomPaneCursors zeroes cursor and expansion state owned by the
// bottom pane without disturbing the top pane's sticky Git/Beads state.
func (m Model) resetBottomPaneCursors() Model {
	m.sessions = m.sessions.ResetSelection()
	m.plans = m.plans.ResetSelection()
	m.flows = m.flows.ResetSelection()
	m = m.setExpandedPlanID("")
	m.expandedFlowID = ""
	m.selectedFlowPhaseID = ""
	m.flows = m.flows.SetItemHeight(flowItemHeight(""))
	m.terminalFocus = terminalFocusList
	m.terminalPrefixActive = false
	m = m.invalidateViewRequest()
	return m
}

func (m Model) resetModeCursorsForSwitch(from, to ui.Mode) Model {
	if from != to && (ui.IsBeadsMode(from) || ui.IsBeadsMode(to)) {
		m = m.clearBeadExpansion()
	}
	if from == ui.ModeActiveFlows || to == ui.ModeActiveFlows || from == ui.ModePRBabysitter || to == ui.ModePRBabysitter {
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
		return m.invalidateViewRequest()
	}
	pane, ok := ui.PaneForMode(to)
	if !ok {
		return m.invalidateViewRequest()
	}
	if pane == ui.PaneBottom {
		return m.resetBottomPaneCursors()
	}
	if from == ui.ModeWorktrees && to != ui.ModeWorktrees {
		m = m.clearInlineWorktreeSessions()
	}
	m.terminalFocus = terminalFocusList
	m.terminalPrefixActive = false
	m = m.invalidateViewRequest()
	// Terminal dimensions are mode-independent, so mode switches need no
	// PTY resize.
	return m
}

func (m Model) clearBeadsRepoOwnedState() Model {
	m = m.clearBeadExpansion()
	for i := range m.beads {
		m.beads[i].pane = m.beads[i].pane.SetItems(nil).ResetSelection()
		m.beads[i].available = false
		m.beads[i].pending = false
		m.beads[i].error = ""
		m.beads[i].total = 0
		m.beads[i].repoPath = ""
	}
	return m
}

func (m Model) resetBeadsForRepoChange() Model {
	m = m.invalidateBeadsListRequests()
	return m.clearBeadsRepoOwnedState()
}

func (m Model) resetRightPaneCursors() Model {
	m = m.resetStoredPaneCursors()
	m.listRequestSeq++
	m.listRequests[int(ui.ModeActiveFlows)] = m.listRequestSeq
	m.listRequests[int(ui.ModePRBabysitter)] = m.listRequestSeq
	m.activeFlows = m.activeFlows.SetItems(nil).ResetSelection()
	m.expandedActiveFlowID = ""
	m.selectedActiveFlowPhaseID = ""
	m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(""))
	m.prBabysitterRecords = nil
	m.prBabysitterStatuses = make(map[string]actions.PullRequestStatus)
	m.prBabysitterFlows = m.prBabysitterFlows.SetItems(nil).ResetSelection()
	m.expandedPRBabysitterFlowID = ""
	m.selectedPRBabysitterPhaseID = ""
	m.prBabysitterFlows = m.prBabysitterFlows.SetItemHeight(flowItemHeight(""))
	m = m.setFlowDegradation(ui.ModePRBabysitter, "", nil)
	return m
}

func (m Model) resetStoredPaneCursors() Model {
	m = m.invalidateStoredListRequests()
	m = m.setFlowDegradation(ui.ModeFlows, "", nil)
	// Covers the rescan-driven repo changes in handleRepoRefreshResult, which
	// never route through handleRepoSelectionChanged.
	m = m.invalidateReadyBeadFlowCreateRequest()
	m.pendingBranchSelection = ""
	m.pendingWorktreeSelection = ""
	m.rows = m.rows.SetItems(nil).ResetSelection()
	m.stashes = m.stashes.SetItems(nil).ResetSelection()
	m.worktrees = m.worktrees.SetItems(nil).ResetSelection()
	m.commits = m.commits.SetItems(nil).ResetSelection()
	m.reflogs = m.reflogs.SetItems(nil).ResetSelection()
	m.sessions = m.sessions.SetItems(nil).ResetSelection()
	m.plans = m.plans.SetItems(nil).ResetSelection()
	m.flows = m.flows.SetItems(nil).ResetSelection()
	m = m.clearBeadsRepoOwnedState()
	m = m.setExpandedPlanID("")
	m.expandedFlowID = ""
	m.selectedFlowPhaseID = ""
	m.flows = m.flows.SetItemHeight(flowItemHeight(""))
	m.terminalFocus = terminalFocusList
	m.terminalPrefixActive = false
	m = m.clearInlineWorktreeSessions()
	m = m.invalidateViewRequest()
	return m
}

func (m Model) clearInlineWorktreeSessions() Model {
	m.activeWorktreeSessionReq = 0
	m.inlineWorktreeSessionRepo = ""
	m.inlineWorktreeSessionPath = ""
	m.pendingInlineSessionRepo = ""
	m.pendingInlineSessionPath = ""
	m.pendingInlineSessionList = 0
	m.worktreeSessions = newSessionPane()
	return m
}

func (m Model) repoContentHeight() int {
	height := m.height - ui.RepoContentOverhead
	if height <= 0 {
		return 1
	}
	return m.applyEmbeddedTerminalDockContentHeight(height, 0)
}

func (m Model) rightContentHeight() int {
	return m.paneContentHeight(m.focusedMode())
}

func (m Model) planContentHeight() int {
	return m.paneContentHeight(ui.ModePlans)
}

func (m Model) flowContentHeight() int {
	return m.paneContentHeight(ui.ModeFlows)
}

func (m Model) sessionContentHeight() int {
	return m.paneContentHeight(ui.ModeSessions)
}

func (m Model) worktreeSessionContentHeight() int {
	height := m.worktreeContentHeight() - 2
	if height <= 0 {
		return 1
	}
	return height
}

func (m Model) contentHeightForMode(modes ...ui.Mode) int {
	mode := m.focusedMode()
	if len(modes) > 0 {
		mode = modes[0]
	}
	return m.paneContentHeight(mode)
}

func (m Model) paneContentHeight(mode ui.Mode) int {
	sharedOuterRows := m.embeddedTerminalDockAllocation().SharedOuterRows
	outerRows := sharedOuterRows
	headerRows := 1
	if mode != ui.ModeActiveFlows && mode != ui.ModePRBabysitter {
		layout := ui.StackedContentLayout(sharedOuterRows, m.activePane, m.contentPane)
		pane, ok := ui.PaneForMode(mode)
		if !ok {
			return 1
		}
		if pane == ui.PaneTop {
			outerRows = layout.TopRows
			headerRows = 2
		} else {
			outerRows = layout.BottomRows
		}
	}
	rows := outerRows - 2 - headerRows
	if mode == ui.ModeSessions || mode == ui.ModePlans || mode == ui.ModeFlows || mode == ui.ModeActiveFlows || mode == ui.ModePRBabysitter {
		rows -= ui.TableHeaderRows
	}
	rows -= m.paneCachedWarningRows(mode)
	rows -= m.paneFlowDegradationWarningRows(mode)
	// The positive floor predates the warning row and also covers hidden
	// background panes, which are allocated no rows at all. Viewports this
	// small cannot show a cached row either way, so the floor stays.
	if rows <= 0 {
		return 1
	}
	return rows
}

// paneCachedWarningRows is the list row the renderer spends on the "showing
// cached data" banner when a refresh fails while rows are still cached.
// Selection and scroll must use the same reduced viewport or the last visible
// row is clipped.
func (m Model) paneCachedWarningRows(mode ui.Mode) int {
	if !ui.ShowsCachedListWarning(m.currentListError(mode), m.paneHasRows(mode), m.paneSourceCount(mode)) {
		return 0
	}
	return 1
}

func (m Model) paneSourceCount(mode ui.Mode) int {
	if mode == ui.ModeActiveFlows || mode == ui.ModePRBabysitter {
		return m.activeItemPaneSourceCount()
	}
	return m.itemPaneSourceCount(mode)
}

// paneHasRows mirrors the renderer's view of whether a pane still has cached
// rows to show, including the empty-repository reset the view applies.
func (m Model) paneHasRows(mode ui.Mode) bool {
	if len(m.filteredRepos()) == 0 {
		return false
	}
	switch mode {
	case ui.ModeActiveFlows:
		return m.activeFlows.Len() > 0
	case ui.ModePRBabysitter:
		return m.prBabysitterFlows.Len() > 0
	case ui.ModeFlows:
		return m.flows.Len() > 0
	case ui.ModeWorktrees:
		return m.worktrees.Len() > 0
	case ui.ModeBranches:
		return m.rows.Len() > 0
	case ui.ModeStashes:
		return m.stashes.Len() > 0
	case ui.ModeHistory:
		return m.commits.Len() > 0
	case ui.ModeReflog:
		return m.reflogs.Len() > 0
	case ui.ModeSessions:
		return m.sessions.Len() > 0
	case ui.ModePlans:
		return m.plans.Len() > 0
	default:
		if !ui.IsBeadsMode(mode) {
			return false
		}
		state, ok := m.beadSubview(mode)
		return ok && !state.pending && state.error == "" && state.pane.Len() > 0
	}
}

func (m Model) beadsContentHeight() int {
	return m.paneContentHeight(m.topMode)
}

// gitPaneContentHeight is the list height for the branches, history, and
// reflog subviews, which render under the two-row grouped Git header.
func (m Model) gitPaneContentHeight() int {
	return m.paneContentHeight(m.topMode)
}

func (m Model) worktreeContentHeight() int {
	return m.paneContentHeight(ui.ModeWorktrees)
}

func (m Model) stashContentHeight() int {
	return m.paneContentHeight(ui.ModeStashes)
}

func (m Model) applyEmbeddedTerminalDockContentHeight(outerHeight, headerRows int) int {
	rows := outerHeight - m.embeddedTerminalDockRows() - headerRows
	if rows <= 0 {
		return 1
	}
	return rows
}

// embeddedTerminalDockRows is the number of full-width rows the top-level
// terminal dock consumes below the pane row: the framed terminal pane when
// expanded, the collapsed chip when the viewport can fit it, or zero rows at
// the existing degenerate-height boundary.
func (m Model) embeddedTerminalDockRows() int {
	return m.embeddedTerminalDockAllocation().DockRows
}
