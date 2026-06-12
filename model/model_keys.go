package model

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/internal/artifacts"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.modal.IsOpen() {
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
		} else if outcome == modal.Accepted && cmd != nil && isFlowCreateInput(view) {
			var request uint64
			m, request = m.nextFlowCreateRequest()
			cmd = tagFlowCreateRequest(cmd, request)
		}
		return m, cmd
	}

	if next, cmd, handled := m.handleEmbeddedTerminalKey(msg); handled {
		return next, cmd
	}

	m = m.clearAnyStatus()

	if m.searchActive {
		return m.handleSearchKey(msg)
	}

	if key == "/" {
		m = m.setSearchActive(true)
		return m, nil
	}

	if key == "esc" && m.activeSearchQuery() != "" {
		oldRepoPath, _ := m.currentRepoPath()
		m = m.setActiveSearchQuery("")
		m = m.setSearchActive(false)
		if m.activePane == 0 && oldRepoPath != "" {
			m = m.selectFilteredRepo(oldRepoPath)
		}
		m = m.clampSelectionsAfterFilter()
		if m.activePane == 0 {
			newRepoPath, ok := m.currentRepoPath()
			if oldRepoPath != newRepoPath {
				m = m.resetRightPaneCursors()
				if ok {
					return m.startFetchForMode()
				}
			}
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

	if key == "f5" {
		return m.startGlobalRefresh()
	}

	if m.activePane == 0 {
		return m.handleLeftPaneKey(key)
	}
	return m.handleRightPaneKey(key)
}

func isWorktreeCreateInput(view modal.View) bool {
	if view.Kind != modal.Input {
		return false
	}
	return view.Placeholder == ui.WorktreeInputPlaceholder || view.Placeholder == ui.PRWorktreeInputPlaceholder
}

func isFlowCreateInput(view modal.View) bool {
	return view.Kind == modal.Input && view.Placeholder == ui.FlowBaseRefInputPlaceholder
}

func isRepoCreateForm(view modal.View) bool {
	return view.Kind == modal.Form && view.Form.Purpose == repoCreateFormPurpose
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
		case PlanLaunchRequestedMsg:
			if msg.Request == 0 {
				msg.Request = request
			}
			return msg
		case FlowEmbeddedLaunchRequestedMsg:
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

	if m.activePane == 0 && oldRepoPath != "" {
		m = m.selectFilteredRepo(oldRepoPath)
	}
	m = m.clampSelectionsAfterFilter()
	if m.activePane == 0 {
		newRepoPath, ok := m.currentRepoPath()
		if oldRepoPath != newRepoPath {
			m = m.resetRightPaneCursors()
			if ok {
				return m.startFetchForMode()
			}
		}
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
	case "tab":
		m.activePane = 1
	case "left":
		return m.handleHorizontalNavigation(-1)
	case "right":
		return m.handleHorizontalNavigation(1)
	case "up", "k":
		if len(m.filteredRepos()) > 0 {
			m.repos = m.repos.Move(-1, m.repoContentHeight(), ui.LeftPaneWidth-2)
			m.pendingRepoSelection = ""
			m = m.resetRightPaneCursors()
			return m.startFetchForMode()
		}
	case "down", "j":
		if len(m.filteredRepos()) > 0 {
			m.repos = m.repos.Move(1, m.repoContentHeight(), ui.LeftPaneWidth-2)
			m.pendingRepoSelection = ""
			m = m.resetRightPaneCursors()
			return m.startFetchForMode()
		}
	case "f":
		return m.startFetchVisibleRepos()
	case "n":
		return m.handleNewRepo()
	case "q", "ctrl+c", "esc":
		return m.handleEmbeddedTerminalQuitPrefix()
	}
	return m, nil
}

func (m Model) handleRightPaneKey(key string) (tea.Model, tea.Cmd) {
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
		if m.mode == ui.ModeFlows {
			return m.handleToggleFlowHeadless()
		}
		if m.mode > ui.ModeWorktrees {
			m.mode--
			m = m.resetModeCursors()
			return m.startFetchForMode()
		}
	case "l":
		if m.mode < ui.ModeFlows {
			m.mode++
			m = m.resetModeCursors()
			if m.mode == ui.ModeFlows {
				return m.startFlowsModeFetchWithRefreshTick()
			}
			return m.startFetchForMode()
		}
	case "1":
		if m.mode != ui.ModeWorktrees {
			m.mode = ui.ModeWorktrees
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModeWorktrees)
		}
	case "2":
		if m.mode != ui.ModeBranches {
			m.mode = ui.ModeBranches
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModeBranches)
		}
	case "3":
		if m.mode != ui.ModeStashes {
			m.mode = ui.ModeStashes
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModeStashes)
		}
	case "4":
		if m.mode != ui.ModeHistory {
			m.mode = ui.ModeHistory
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModeHistory)
		}
	case "5":
		if m.mode != ui.ModeReflog {
			m.mode = ui.ModeReflog
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModeReflog)
		}
	case "6":
		if m.mode != ui.ModeSessions {
			m.mode = ui.ModeSessions
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModeSessions)
		}
	case "7":
		if m.mode != ui.ModePlans {
			m.mode = ui.ModePlans
			m = m.resetModeCursors()
			return m.startFetchMode(ui.ModePlans)
		}
	case "8":
		if m.mode != ui.ModeFlows {
			m.mode = ui.ModeFlows
			m = m.resetModeCursors()
			return m.startFlowsModeFetchWithRefreshTick()
		}
	case "y":
		if m.mode == ui.ModePlans {
			return m.handleCopyPlanPath()
		}
		if m.mode == ui.ModeSessions {
			return m.handleCopySessionID()
		}
		if m.mode == ui.ModeFlows {
			return m.handleCopyFlowID()
		}
		return m.handleCopyHash()
	case "s":
		return m.handleShowSessionSummary()
	case "r":
		if m.mode == ui.ModeFlows {
			return m.handleResumeFlowPhaseSession()
		}
		return m.handleResumeSession()
	case "i":
		if m.mode == ui.ModePlans {
			return m.handleImplementPlan()
		}
	case "x":
		if m.mode == ui.ModeWorktrees {
			return m.handleToggleWorktreeSessions()
		}
		if m.mode == ui.ModePlans {
			return m.handleTogglePlanPhases()
		}
	case "tab":
		if m.mode == ui.ModeFlows && m.hasEmbeddedTerminalForScope(embeddedTerminalScopeFlow) {
			m.flowFocus = flowFocusTerminal
			m.terminalPrefixActive = true
			return m, nil
		}
		m.activePane = 0
		if m.mode == ui.ModePlans {
			m = m.clearSelectedPlanPhase()
		}
		if m.mode == ui.ModeFlows {
			m = m.clearSelectedFlowPhase()
		}
	case "enter":
		return m.handleEnter()
	case "n":
		if m.mode == ui.ModeWorktrees {
			return m.handleNewWorktree(false)
		}
		if m.mode == ui.ModeBranches {
			return m.handleNewBranch()
		}
		if m.mode == ui.ModeFlows {
			return m.handleNewFlow()
		}
	case "P":
		if m.mode == ui.ModeWorktrees {
			return m.handleNewPullRequestWorktree()
		}
	case "o":
		if m.mode == ui.ModeSessions {
			return m.handleEnter()
		}
		if m.mode == ui.ModePlans {
			return m.handleOpenPlanText()
		}
		if m.mode == ui.ModeFlows {
			return m.handleOpenFlowPlanText()
		}
	case "e":
		if m.mode == ui.ModePlans {
			return m.handleEditPlan()
		}
	case "m":
		if m.mode == ui.ModeWorktrees {
			return m.handleMoveWorktree()
		}
	case "N":
		if m.mode == ui.ModeWorktrees {
			return m.handleNewWorktree(true)
		}
	case "a":
		if m.mode == ui.ModePlans {
			return m.handleImplementPlan()
		}
		if m.mode == ui.ModeFlows {
			return m, nil
		}
		return m.handleOpenAgent()
	case "d":
		return m.handleDelete()
	case "p":
		return m.handlePrune()
	case "u":
		return m.handleUnlock()
	case "f":
		return m.handleFetch()
	case "F":
		return m.handlePull()
	case "t":
		return m.handleOpenTerminal()
	case "c":
		return m.handleOpenCode()
	case "q", "ctrl+c", "esc":
		return m.handleEmbeddedTerminalQuitPrefix()
	}
	return m, nil
}

func (m Model) handleHorizontalNavigation(direction int) (tea.Model, tea.Cmd) {
	if direction == 0 {
		return m, nil
	}
	if m.activePane == 0 {
		m.activePane = 1
		targetMode := ui.ModeWorktrees
		if direction < 0 {
			targetMode = ui.ModeFlows
		}
		if m.mode != targetMode {
			m.mode = targetMode
			m = m.resetModeCursors()
			if m.mode == ui.ModeFlows {
				return m.startFlowsModeFetchWithRefreshTick()
			}
			return m.startFetchForMode()
		}
		return m, nil
	}

	if direction > 0 {
		if m.mode == ui.ModeFlows {
			m.activePane = 0
			m = m.clearSelectedFlowPhase()
			return m, nil
		}
		m.mode++
		m = m.resetModeCursors()
		if m.mode == ui.ModeFlows {
			return m.startFlowsModeFetchWithRefreshTick()
		}
		return m.startFetchForMode()
	}

	if m.mode == ui.ModeWorktrees {
		m.activePane = 0
		return m, nil
	}
	m.mode--
	m = m.resetModeCursors()
	return m.startFetchForMode()
}

// --- Cursor navigation ---

func (m Model) handleCursorUp() (tea.Model, tea.Cmd) {
	return m.moveCursor(-1), nil
}

func (m Model) handleCursorDown() (tea.Model, tea.Cmd) {
	return m.moveCursor(1), nil
}

// moveCursor moves the selected item in the active right-pane view by delta
// (-1 for up, +1 for down) and keeps the new selection visible.
func (m Model) moveCursor(delta int) Model {
	h, w := m.contentHeightForMode(), m.contentWidth()
	switch m.mode {
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
	}
	return m
}

// --- Action handlers ---

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	if m.mode == ui.ModeWorktrees {
		if m.inlineWorktreeSessionPath != "" {
			record, ok := m.selectedWorktreeSession()
			if !ok {
				return m, nil
			}
			ctx, ok, next := m.sessionResumeLaunchContext(record)
			if !ok {
				return next, nil
			}
			return next.launchAgentWithContext(ctx)
		}
		wt, ok := m.selectedWorktree()
		if ok && wt.Dirty && !wt.Stale {
			m = m.startViewRequest(FetchWorktreeDiff, ui.ModeWorktrees)
			return m, m.fetchWorktreeDiff()
		}
		return m, nil
	}
	if m.mode == ui.ModeBranches && m.isSelectedBranchDirtyWorktree() {
		m = m.startViewRequest(FetchBranchDiff, ui.ModeBranches)
		return m, m.fetchBranchDiff()
	}
	if m.mode == ui.ModeStashes && len(m.filteredStashes()) > 0 {
		m = m.startViewRequest(FetchStashDiff, ui.ModeStashes)
		return m, m.fetchStashDiff()
	}
	if m.mode == ui.ModeHistory && len(m.filteredCommits()) > 0 {
		m = m.startViewRequest(FetchCommitDiff, ui.ModeHistory)
		return m, m.fetchCommitDiff()
	}
	if m.mode == ui.ModeReflog && len(m.filteredReflogs()) > 0 {
		m = m.startViewRequest(FetchReflogDiff, ui.ModeReflog)
		return m, m.fetchReflogDiff()
	}
	if m.mode == ui.ModeSessions && len(m.filteredSessions()) > 0 {
		m = m.startViewRequest(FetchSessionTranscript, ui.ModeSessions)
		return m, m.fetchSessionTranscript()
	}
	if m.mode == ui.ModePlans && len(m.filteredPlans()) > 0 {
		if planID := m.selectedPlanID(); planID != "" {
			if m.expandedPlanID == planID {
				m = m.setExpandedPlanID("")
			} else {
				m = m.setExpandedPlanID(planID)
			}
		}
		return m, nil
	}
	if m.mode == ui.ModeFlows {
		return m.handleFlowEnter()
	}
	return m, nil
}

func (m Model) handleFlowEnter() (tea.Model, tea.Cmd) {
	if m.selectedFlowPhaseID == "" {
		return m.handleToggleFlowPhases()
	}
	return m.handleLaunchSelectedFlowPhase()
}

func (m Model) handleToggleFlowHeadless() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeFlows {
		return m, nil
	}
	m.flowHeadless = !m.flowHeadless
	return m, nil
}

func (m Model) handleTogglePlanPhases() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModePlans || len(m.filteredPlans()) == 0 {
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
	if m.mode != ui.ModeFlows || len(m.filteredFlows()) == 0 {
		return m, nil
	}
	flowID := m.selectedFlowID()
	if flowID == "" {
		return m, nil
	}
	if m.expandedFlowID == flowID {
		m = m.setExpandedFlowID("")
	} else {
		m = m.setExpandedFlowID(flowID)
	}
	return m, nil
}

func (m Model) handleToggleWorktreeSessions() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeWorktrees || len(m.filteredWorktrees()) == 0 {
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
	if m.mode == ui.ModePlans && len(m.filteredPlans()) > 0 {
		m = m.startViewRequest(FetchPlanText, ui.ModePlans)
		return m, m.fetchPlanText()
	}
	return m, nil
}

func (m Model) handleOpenFlowPlanText() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeFlows || len(m.filteredFlows()) == 0 {
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
	m = m.startViewRequest(FetchPlanText, ui.ModeFlows)
	return m, m.fetchPlanTextByID(record.PlanID, ui.ModeFlows)
}

func (m Model) handleEditPlan() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModePlans || len(m.filteredPlans()) == 0 {
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
	if m.mode == ui.ModeHistory || m.mode == ui.ModeReflog {
		return m, nil
	}
	if m.mode == ui.ModeStashes && len(m.filteredStashes()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmStashDrop()
	}
	if m.mode == ui.ModeFlows && len(m.filteredFlows()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmFlowDelete()
	}
	if m.mode == ui.ModeBranches && len(m.filteredRepos()) > 0 {
		return m.confirmBranchDelete()
	}
	if m.mode == ui.ModeWorktrees && len(m.filteredWorktrees()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmWorktreeDelete()
	}
	return m, nil
}

func (m Model) handleSetAgent() (tea.Model, tea.Cmd) {
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
		{Label: agent.CommandCodexApp, Value: agent.CommandCodexApp},
		{Label: agent.CommandClaude, Value: agent.CommandClaude},
	}
}

func selectedAgentIndex(command string) int {
	switch agent.Normalize(command) {
	case agent.CommandCodexApp:
		return 1
	case agent.CommandClaude:
		return 2
	default:
		return 0
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

const (
	repoCreateFormPurpose     = "repo-create"
	repoCreateNameField       = "name"
	repoCreateGitHubField     = "github"
	repoCreateVisibilityField = "visibility"
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
	if _, ok := m.currentRepoPath(); !ok {
		return m, nil
	}
	if m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching a flow")
		return m, nil
	}
	m.modal = modal.OpenSingleLineInput(
		ui.FlowTitlePrompt,
		ui.FlowTitleInputPlaceholder,
		"",
		validateFlowTitleInput,
		func(input string) tea.Cmd {
			return func() tea.Msg { return FlowTitleSubmittedMsg{Title: input} }
		},
	)
	return m, nil
}

func (m Model) handleFlowTitleSubmitted(msg FlowTitleSubmittedMsg) Model {
	m.modal = modal.OpenMultiLineInput(
		ui.FlowInstructionsPrompt,
		ui.FlowInstructionsInputPlaceholder,
		"",
		validateFlowInstructionsInput,
		func(input string) tea.Cmd {
			return func() tea.Msg { return FlowInstructionsSubmittedMsg{Title: msg.Title, Instructions: input} }
		},
	)
	return m
}

func (m Model) handleFlowInstructionsSubmitted(msg FlowInstructionsSubmittedMsg) Model {
	m.modal = modal.OpenSingleLineInput(
		ui.FlowBaseRefPrompt,
		ui.FlowBaseRefInputPlaceholder,
		"",
		validateFlowBaseRefInput,
		func(input string) tea.Cmd {
			return m.createFlowAndLaunchPlan(msg.Title, msg.Instructions, input)
		},
	)
	return m
}

func (m Model) handleFlowCreateFailed(msg FlowCreateFailedMsg) (Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) || (msg.Request != 0 && !m.isCurrentFlowCreateRequest(msg.Request)) {
		return m, nil
	}
	m = m.clearFlowCreateRequest(msg.Request)
	errText := msg.Err
	if errText == "" {
		errText = "Unable to create flow"
	}
	m = m.setStatus(statusOther, errText)
	if m.mode == ui.ModeFlows {
		return m.startFetchMode(ui.ModeFlows)
	}
	return m, nil
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
	if m.activePane != 1 || m.mode != ui.ModeWorktrees {
		return false
	}
	wt, ok := m.selectedWorktree()
	return ok && canMoveWorktree(wt)
}

func (m Model) handleUnlock() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeWorktrees {
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
	if m.activePane != 1 {
		return false
	}
	if m.mode == ui.ModeFlows {
		_, ok := m.selectedFlow()
		return ok
	}
	_, ok := m.agentTargetPath()
	return ok
}

func (m Model) canCreateAndLaunchAgent() bool {
	return m.activePane == 1 && m.mode == ui.ModeWorktrees
}

func (m Model) agentTargetPath() (string, bool) {
	if m.mode == ui.ModeWorktrees {
		if _, ok := m.currentRepoPath(); !ok {
			return "", false
		}
		if wt, ok := m.selectedWorktree(); ok && !wt.Stale {
			return wt.Path, true
		}
		return "", false
	}
	if m.mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok && !row.Stale && row.WorktreePath != "" {
			return row.WorktreePath, true
		}
	}
	return "", false
}

func (m Model) pathForOpenAction() (string, bool) {
	if m.mode == ui.ModeWorktrees {
		if _, ok := m.currentRepoPath(); !ok {
			return "", false
		}
		if wt, ok := m.selectedWorktree(); ok && !wt.Stale {
			return wt.Path, true
		}
		return "", false
	}
	if m.mode == ui.ModeHistory {
		repo, ok := m.currentRepo()
		if !ok || repo.IsBare {
			return "", false
		}
		return repo.Path, true
	}
	if m.mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok && row.WorktreePath != "" {
			return row.WorktreePath, true
		}
	}
	return "", false
}

func (m Model) handleOpenAgent() (tea.Model, tea.Cmd) {
	if m.mode == ui.ModeFlows {
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

func (m Model) handleLaunchSelectedFlowPhase() (tea.Model, tea.Cmd) {
	target, ok, next := m.selectedFlowPhaseLaunchTarget()
	if !ok {
		return next, nil
	}
	launchID := newLaunchID()
	switch agent.Normalize(next.agentCommand) {
	case agent.CommandCodex, agent.CommandClaude:
		return next, next.prepareFlowPhaseEmbeddedLaunch(target.record, target.phase, target.repoPath, target.worktreePath, target.planPath, launchID, next.flowHeadless)
	}
	return next, next.prepareFlowPhaseLaunch(target.record, target.phase, target.repoPath, target.worktreePath, target.planPath, launchID)
}

type flowPhaseLaunchTarget struct {
	record       flowstore.FlowRecord
	phase        flowstore.FlowPhase
	repoPath     string
	worktreePath string
	planPath     string
}

func (m Model) selectedFlowPhaseLaunchTarget() (flowPhaseLaunchTarget, bool, Model) {
	record, ok := m.selectedFlow()
	if !ok {
		return flowPhaseLaunchTarget{}, false, m
	}
	phase, ok := m.selectedFlowPhase()
	if !ok {
		return flowPhaseLaunchTarget{}, false, m
	}
	if !flowPhaseCanLaunch(record, phase) {
		m = m.setStatus(statusOther, flowPhaseNotLaunchableMessage(record, phase))
		return flowPhaseLaunchTarget{}, false, m
	}
	return m.flowPhaseLaunchTarget(record, phase)
}

func (m Model) flowPhaseLaunchTarget(record flowstore.FlowRecord, phase flowstore.FlowPhase) (flowPhaseLaunchTarget, bool, Model) {
	if m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching an agent")
		return flowPhaseLaunchTarget{}, false, m
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	worktreePath := record.WorktreePath
	if worktreePath == "" {
		worktreePath = repoPath
	}
	if worktreePath == "" {
		m = m.setStatus(statusOther, "Cannot determine launch path for this flow")
		return flowPhaseLaunchTarget{}, false, m
	}
	planPath := record.PlanPath
	if record.PlanID != "" && planPath == "" {
		var err error
		planPath, err = m.planMarkdownPath(record.PlanID)
		if err != nil {
			m = m.setStatus(statusOther, err.Error())
			return flowPhaseLaunchTarget{}, false, m
		}
	}
	if phase.PhaseID == "plan-review" && record.PlanID == "" {
		m = m.setStatus(statusOther, "Plan Review needs a linked plan before launch")
		return flowPhaseLaunchTarget{}, false, m
	}
	return flowPhaseLaunchTarget{
		record:       record,
		phase:        phase,
		repoPath:     repoPath,
		worktreePath: worktreePath,
		planPath:     planPath,
	}, true, m
}

func (m Model) prepareFlowPhaseLaunch(record flowstore.FlowRecord, phase flowstore.FlowPhase, repoPath, worktreePath, planPath, launchID string) tea.Cmd {
	return m.prepareFlowPhaseLaunchCmd(record, phase, repoPath, worktreePath, planPath, launchID, func(ctx actions.AgentLaunchContext) tea.Msg {
		return PlanLaunchRequestedMsg{LaunchContext: ctx}
	})
}

func (m Model) prepareFlowPhaseEmbeddedLaunch(record flowstore.FlowRecord, phase flowstore.FlowPhase, repoPath, worktreePath, planPath, launchID string, headless bool) tea.Cmd {
	return m.prepareFlowPhaseLaunchCmd(record, phase, repoPath, worktreePath, planPath, launchID, func(ctx actions.AgentLaunchContext) tea.Msg {
		ctx.FlowLaunchTracked = true
		ctx.Embedded = true
		ctx.Headless = headless
		return FlowEmbeddedLaunchRequestedMsg{LaunchContext: ctx}
	})
}

func (m Model) prepareFlowPhaseLaunchCmd(record flowstore.FlowRecord, phase flowstore.FlowPhase, repoPath, worktreePath, planPath, launchID string, wrap func(actions.AgentLaunchContext) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		planBody := ""
		if record.PlanID != "" && flowPhasePromptNeedsPlanBody(phase.PhaseID) {
			body, err := m.readPlan(record.PlanID)
			if err != nil {
				return ActionFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("failed to read linked plan %s: %v", record.PlanID, err)}
			}
			planBody = body
		}
		if _, err := m.addFlowPhaseLaunchID(flowstore.PhaseLaunchUpdate{
			FlowID:   record.FlowID,
			PhaseID:  phase.PhaseID,
			LaunchID: launchID,
		}); err != nil {
			return ActionFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("failed to mark flow phase running: %v", err)}
		}
		return wrap(actions.AgentLaunchContext{
			Command:          m.agentCommand,
			LaunchID:         launchID,
			RepoPath:         repoPath,
			WorktreePath:     worktreePath,
			Branch:           record.Branch,
			Commit:           record.Commit,
			SessionStateRoot: m.sessionStateRoot,
			PlanID:           record.PlanID,
			PlanPath:         planPath,
			FlowID:           record.FlowID,
			FlowPhaseID:      phase.PhaseID,
			InitialPrompt:    flowPhasePrompt(record, phase, planPath, planBody, m.flowPromptTemplates),
		})
	}
}

func (m Model) handleResumeSession() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeSessions {
		return m, nil
	}
	record, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	ctx, ok, next := m.sessionResumeLaunchContext(record)
	if !ok {
		return next, nil
	}
	if ctx.Command != agent.CommandCodexApp {
		return next.resumeSessionInEmbeddedTerminal(ctx, record)
	}
	return next.launchAgentWithContext(ctx)
}

func (m Model) handleResumeFlowPhaseSession() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeFlows {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" || record.FlowID != m.expandedFlowID || m.selectedFlowPhaseID == "" {
		return m, nil
	}
	phase, ok := m.selectedFlowPhase()
	if !ok {
		return m, nil
	}
	if phase.Status == flowstore.PhaseRunning && flowstore.PhaseAwaitingSession(phase) {
		m = m.setStatus(statusOther, "Flow phase is awaiting session capture")
		return m, nil
	}
	if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
		m = m.setStatus(statusOther, "Flow phase has missing session id")
		return m, nil
	}
	session, ok := flowstore.LatestPhaseSession(phase, true)
	if !ok {
		m = m.setStatus(statusOther, "Flow phase has no session to resume")
		return m, nil
	}
	ctx, ok, next := m.flowPhaseSessionResumeLaunchContext(record, phase, session)
	if !ok {
		return next, nil
	}
	if ctx.Command == agent.CommandCodexApp {
		// Codex App resume deep links cannot carry wtui launch metadata, so treat
		// them as app navigation instead of a tracked Flow launch attempt.
		ctx.LaunchID = ""
		ctx.FlowID = ""
		ctx.FlowPhaseID = ""
		return next.launchAgentWithContext(ctx)
	}
	return next.launchTrackedFlowPhaseResumeWithContext(ctx)
}

func (m Model) sessionResumeLaunchContext(record sessions.SessionRecord) (actions.AgentLaunchContext, bool, Model) {
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		m = m.setStatus(statusOther, "Session has no provider session ID and cannot be resumed")
		return actions.AgentLaunchContext{}, false, m
	}
	command := string(record.Provider)
	if record.Provider == sessions.ProviderCodex && agent.Normalize(m.agentCommand) == agent.CommandCodexApp {
		command = agent.CommandCodexApp
	}
	workingDir := record.CWD
	if workingDir == "" {
		workingDir = record.WorktreePath
	}
	if workingDir == "" && command != agent.CommandCodexApp {
		m = m.setStatus(statusOther, "Session has no worktree path or cwd to resume from")
		return actions.AgentLaunchContext{}, false, m
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
	return ctx, true, m
}

func (m Model) flowPhaseSessionResumeLaunchContext(record flowstore.FlowRecord, phase flowstore.FlowPhase, session flowstore.Session) (actions.AgentLaunchContext, bool, Model) {
	command := agent.Normalize(strings.TrimSpace(session.Provider))
	if command == "" {
		m = m.setStatus(statusOther, "Flow phase session has no provider")
		return actions.AgentLaunchContext{}, false, m
	}
	if command == agent.CommandCodex && agent.Normalize(m.agentCommand) == agent.CommandCodexApp {
		command = agent.CommandCodexApp
	}
	if err := agent.Validate(command); err != nil {
		m = m.setStatus(statusOther, err.Error())
		return actions.AgentLaunchContext{}, false, m
	}
	sessionID := strings.TrimSpace(session.SessionID)
	if sessionID == "" {
		m = m.setStatus(statusOther, "Flow phase has missing session id")
		return actions.AgentLaunchContext{}, false, m
	}
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	workingDir := record.WorktreePath
	if workingDir == "" && command != agent.CommandCodexApp {
		m = m.setStatus(statusOther, "Flow phase has no worktree path to resume from")
		return actions.AgentLaunchContext{}, false, m
	}
	ctx := actions.AgentLaunchContext{
		Command:           command,
		LaunchID:          newLaunchID(),
		RepoPath:          repoPath,
		WorktreePath:      record.WorktreePath,
		WorkingDir:        workingDir,
		Branch:            record.Branch,
		Commit:            record.Commit,
		SessionStateRoot:  m.sessionStateRoot,
		ResumeSessionID:   sessionID,
		PlanID:            record.PlanID,
		PlanPath:          record.PlanPath,
		FlowID:            record.FlowID,
		FlowPhaseID:       phase.PhaseID,
		FlowPhaseTerminal: flowstore.PhaseStatusTerminal(phase.Status),
	}
	return ctx, true, m
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
			return func() tea.Msg { return PlanLaunchRequestedMsg{LaunchContext: ctx} }
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
	ctx := actions.AgentLaunchContext{
		Command:          m.agentCommand,
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
		ctx.InitialPrompt = implementationPromptForPhase(plan, planPath, phase)
	}
	return ctx, true, m
}

func validatePlanLaunchInput(input string) error {
	if input == "" {
		return fmt.Errorf("enter launch instructions")
	}
	return nil
}

func (m Model) implementationPrompt(plan planstore.PlanRecord, planPath, repoPath, worktreePath string) string {
	template := m.planPromptTemplate
	if strings.TrimSpace(template) == "" {
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
	return replacer.Replace(template)
}

func defaultImplementationPrompt(plan planstore.PlanRecord, planPath string) string {
	title := plan.Title
	if title == "" {
		title = "(untitled)"
	}
	return fmt.Sprintf("Implement the saved wtui plan %q (ID: %s) at %s. Read the plan file, then begin implementation.", title, plan.PlanID, planPath)
}

func implementationPromptForPhase(plan planstore.PlanRecord, planPath string, phase planstore.PlanPhase) string {
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
	return fmt.Sprintf("Implement only the selected phase of the saved wtui plan %q (ID: %s) at %s. Selected phase: %s (%q), status %s. Read the plan file, then begin implementation of only that phase.", title, plan.PlanID, planPath, phase.PhaseID, phaseTitle, phaseStatus)
}

func flowPhaseCanLaunch(record flowstore.FlowRecord, phase flowstore.FlowPhase) bool {
	if phase.Status == flowstore.PhaseReady {
		return true
	}
	return phase.PhaseID == "autoreview" &&
		(phase.Status == flowstore.PhaseNeedsAttention || phase.Status == flowstore.PhaseBlocked) &&
		flowstore.HasPRTarget(record.PR) &&
		flowstore.PhasePredecessorsSatisfied(record, phase.PhaseID)
}

func flowPhaseNotLaunchableMessage(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	if phase.PhaseID == "autoreview" && flowAutoreviewMissingPRTarget(record) {
		return "Autoreview needs PR metadata; run `wtui flow pr set` after PR Creation records the PR target"
	}
	if phase.PhaseID == "implementation" && phase.Status == flowstore.PhasePending {
		if review, ok := flowPhaseByID(record, "plan-review"); ok {
			return "Implementation is not ready; Plan Review is " + flowPhaseStatusDetail(review)
		}
	}
	detail := flowPhaseStatusDetail(phase)
	if phase.PhaseID == "" {
		return "Selected Flow phase is not launchable; status is " + detail
	}
	return "Selected Flow phase " + phase.PhaseID + " is not launchable; status is " + detail
}

func flowPhaseStatusDetail(phase flowstore.FlowPhase) string {
	detail := strings.TrimSpace(phase.Status)
	if detail == "" {
		detail = "unknown"
	}
	if phase.Outcome != "" {
		detail += " / " + phase.Outcome
	}
	if phase.Notes != "" {
		detail += ": " + phase.Notes
	} else if phase.Summary != "" {
		detail += ": " + phase.Summary
	}
	return detail
}

func flowAutoreviewMissingPRTarget(record flowstore.FlowRecord) bool {
	if flowstore.HasPRTarget(record.PR) {
		return false
	}
	prCreation, hasPRCreation := flowPhaseByID(record, "pr-creation")
	autoreview, hasAutoreview := flowPhaseByID(record, "autoreview")
	if !hasPRCreation || !hasAutoreview || prCreation.Status != flowstore.PhaseCompleted {
		return false
	}
	return autoreview.Status == flowstore.PhasePending ||
		autoreview.Status == flowstore.PhaseNeedsAttention ||
		autoreview.Status == flowstore.PhaseBlocked
}

func flowPhasePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string, templates FlowPromptTemplates) string {
	if template := templates.templateForPhase(phase.PhaseID); strings.TrimSpace(template) != "" {
		return renderFlowPromptTemplate(template, record, phase, planPath, planBody)
	}
	switch phase.PhaseID {
	case "plan-review":
		return flowPlanReviewPrompt(record, phase, planPath, planBody)
	case "implementation":
		return flowImplementationPrompt(record, phase, planPath, planBody)
	case "review-loop":
		return flowReviewLoopPrompt(record, phase, planPath, planBody)
	case "pr-creation":
		return flowPRCreationPrompt(record, phase, planPath, planBody)
	case "autoreview":
		return flowAutoreviewPrompt(record, phase, planPath, planBody)
	case "merge":
		return flowMergePrompt(record, phase, planPath, planBody)
	default:
		return flowGenericPhasePrompt(record, phase, planPath, planBody)
	}
}

func flowPhasePromptNeedsPlanBody(phaseID string) bool {
	switch phaseID {
	case "plan-review", "implementation", "review-loop", "pr-creation", "autoreview", "merge":
		return false
	default:
		return true
	}
}

func flowPlanReviewPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	return flowMinimalArtifactPrompt("Use the review loop skill to review the saved plan.\nUse the wtui-flow skill to record the Plan Review verdict before finishing; the phase is not done until the verdict is persisted.", planPath, record, phase)
}

func flowImplementationPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	if strings.TrimSpace(planPath) == "" {
		return flowImplementationWithoutPlanPrompt(record, phase)
	}
	return flowMinimalArtifactPrompt("Implement the approved plan.\nUse the commit skill before completing this phase.", planPath, record, phase)
}

func flowImplementationWithoutPlanPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	var b strings.Builder
	b.WriteString("Implement the Flow instructions.\n\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowPromptHeader(&b, record, "")
	writeFlowPromptPlanContext(&b, record, "")
	writeFlowPromptPhaseSummary(&b, record, "Plan Review context", "plan-review")
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	b.WriteString("\nUse the commit skill before completing this phase.")
	b.WriteString("\nAdvance this phase with `wtui flow phase set` only after the implementation is complete, blocked, or needs attention.")
	return b.String()
}

func flowReviewLoopPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	return flowMinimalChangePrompt("Use the review-loop workflow to review the changes.\nUse the commit skill when revisions are made.\nUse the wtui-flow skill to record the Review Loop result before finishing; the phase is not done until the result is persisted.", record, phase)
}

func flowPRCreationPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	head := strings.TrimSpace(record.Branch)
	if head == "" {
		head = "<head>"
	}
	base := strings.TrimSpace(record.BaseRef)
	if base == "" {
		base = "<base>"
	}
	instruction := fmt.Sprintf("Use the ship skill to create a PR for the changes.\nAfter the PR exists, run `wtui flow pr set --flow-id %s --provider github --number <number> --url <url> --head %s --base %s` before completing this phase.", record.FlowID, head, base)
	return flowMinimalChangePrompt(instruction, record, phase)
}

func flowMinimalArtifactPrompt(instruction, planPath string, record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\nPlan: ")
	b.WriteString(planPath)
	b.WriteString("\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	return b.String()
}

func flowAutoreviewPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	var b strings.Builder
	b.WriteString("Use the autoreview skill for this second-level review.\n")
	b.WriteString("Use the ship skill when fixes require commits or pushes.\n")
	b.WriteString("Use the wtui-flow skill to record the Autoreview result before finishing; the phase is not done until the result is persisted.\n\n")
	writeFlowChangeMetadata(&b, record)
	if flowstore.HasPRTarget(record.PR) {
		fmt.Fprintf(&b, "\nPR target:\n- PR: %s #%d\n- URL: %s\n- Head: %s\n- Base: %s", record.PR.Provider, record.PR.Number, record.PR.URL, record.PR.HeadBranch, record.PR.BaseBranch)
		if record.PR.Status != "" {
			fmt.Fprintf(&b, "\n- Status: %s", record.PR.Status)
		}
	} else {
		b.WriteString("\nPR target: missing. Do not run Autoreview until `wtui flow pr set` records provider, number, URL, head, and base.\n")
	}
	return b.String()
}

func writeFlowRestartPromptIfNeeded(b *strings.Builder, record flowstore.FlowRecord, phase flowstore.FlowPhase) {
	if phase.Status != flowstore.PhaseNeedsAttention && phase.Status != flowstore.PhaseBlocked {
		return
	}
	fmt.Fprintf(b, "\nRestart required: this phase is %s. Before marking it completed, record the rerun:\n", phase.Status)
	fmt.Fprintf(b, "wtui flow phase restart --flow-id %s --phase-id %s --notes \"Rerunning %s after addressing prior findings.\"\n", record.FlowID, phase.PhaseID, phase.Title)
}

func flowMergePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	var b strings.Builder
	b.WriteString("Merge the PR deliberately.\n\n")
	writeFlowChangeMetadata(&b, record)
	if flowstore.HasPRTarget(record.PR) {
		fmt.Fprintf(&b, "\n\nPR target:\n- PR: %s #%d\n- URL: %s\n- Head: %s\n- Base: %s\n", record.PR.Provider, record.PR.Number, record.PR.URL, record.PR.HeadBranch, record.PR.BaseBranch)
		if record.PR.Status != "" {
			fmt.Fprintf(&b, "- Status: %s\n", record.PR.Status)
		}
	} else {
		b.WriteString("\n\nPR target: missing. Do not merge until `wtui flow pr set` records provider, number, URL, head, and base.\n")
	}
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	fmt.Fprintf(&b, "\nmerged:\nwtui flow phase set --flow-id %s --phase-id %s --status completed --outcome merged --summary \"...\"\n", record.FlowID, phase.PhaseID)
	fmt.Fprintf(&b, "wtui flow merge set --flow-id %s --status merged --commit <merge-commit> --merged-at <rfc3339>\n\n", record.FlowID)
	fmt.Fprintf(&b, "blocked:\nwtui flow phase set --flow-id %s --phase-id %s --status blocked --outcome blocked --notes \"...\"\n", record.FlowID, phase.PhaseID)
	fmt.Fprintf(&b, "wtui flow merge set --flow-id %s --status blocked", record.FlowID)
	return b.String()
}

func flowMinimalChangePrompt(instruction string, record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	return b.String()
}

func writeFlowChangeMetadata(b *strings.Builder, record flowstore.FlowRecord) {
	b.WriteString("Worktree: ")
	b.WriteString(record.WorktreePath)
	b.WriteString("\nBranch: ")
	b.WriteString(record.Branch)
	b.WriteString("\nStart commit: ")
	b.WriteString(record.Commit)
}

func flowGenericPhasePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	var b strings.Builder
	b.WriteString("Use the wtui-flow skill for this launch.\n\n")
	b.WriteString("Flow phase: ")
	if phase.Title != "" {
		b.WriteString(phase.Title)
	} else {
		b.WriteString(phase.PhaseID)
	}
	b.WriteString(" (")
	b.WriteString(phase.PhaseID)
	b.WriteString(").\n")
	writeFlowPromptHeader(&b, record, planPath)
	writeFlowPromptPlanContext(&b, record, planBody)
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	b.WriteString("\nAdvance this phase with `wtui flow phase set` only after the corresponding work is complete, blocked, or needs attention.")
	return b.String()
}

func writeFlowPromptPhaseSummary(b *strings.Builder, record flowstore.FlowRecord, title, phaseID string) {
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	if phase, ok := flowPhaseByID(record, phaseID); ok {
		writeFlowPhaseContext(b, phase)
		return
	}
	b.WriteString("- Phase: ")
	b.WriteString(phaseID)
	b.WriteString("\n")
}

func writeFlowPromptHeader(b *strings.Builder, record flowstore.FlowRecord, planPath string) {
	if record.Instructions != "" {
		b.WriteString("\nCustom instructions:\n")
		b.WriteString(record.Instructions)
		b.WriteString("\n")
	}
	if record.PlanID != "" {
		b.WriteString("\nLinked plan: ")
		b.WriteString(record.PlanID)
		if planPath != "" {
			b.WriteString(" at ")
			b.WriteString(planPath)
		}
		b.WriteString("\n")
	}
}

func writeFlowPromptPlanContext(b *strings.Builder, record flowstore.FlowRecord, planBody string) {
	if plan, ok := flowPhaseByID(record, "plan"); ok {
		b.WriteString("\nPrior Plan context:\n")
		writeFlowPhaseContext(b, plan)
	}
	if planBody != "" {
		b.WriteString("\nSaved plan body:\n")
		b.WriteString(planBody)
		if !strings.HasSuffix(planBody, "\n") {
			b.WriteString("\n")
		}
	}
}

func writeFlowPhaseContext(b *strings.Builder, phase flowstore.FlowPhase) {
	if phase.PhaseID != "" {
		b.WriteString("- Phase: ")
		b.WriteString(phase.PhaseID)
		b.WriteString("\n")
	}
	if phase.Title != "" {
		b.WriteString("- Title: ")
		b.WriteString(phase.Title)
		b.WriteString("\n")
	}
	b.WriteString("- Status: ")
	b.WriteString(phase.Status)
	b.WriteString("\n")
	if phase.Outcome != "" {
		b.WriteString("- Outcome: ")
		b.WriteString(phase.Outcome)
		b.WriteString("\n")
	}
	if phase.Summary != "" {
		b.WriteString("- Summary: ")
		b.WriteString(phase.Summary)
		b.WriteString("\n")
	}
	if phase.Notes != "" {
		b.WriteString("- Notes: ")
		b.WriteString(phase.Notes)
		b.WriteString("\n")
	}
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

func (m Model) launchAgentAtPath(path string) (Model, tea.Cmd) {
	ctx := m.agentLaunchContext(path)
	return m.launchAgentWithContext(ctx)
}

func (m Model) launchAgentAtPathWithBranch(path string, branch *string) (Model, tea.Cmd) {
	ctx := m.agentLaunchContext(path)
	if branch != nil {
		ctx.Branch = *branch
	}
	return m.launchAgentWithContext(ctx)
}

func (m Model) launchAgentWithContext(ctx actions.AgentLaunchContext) (Model, tea.Cmd) {
	launch, err := m.launchAgent(ctx)
	if err != nil {
		errText := err.Error()
		m, errText = m.markFlowLaunchNeedsAttention(ctx, errText)
		m = m.setStatus(statusOther, errText)
		return m, nil
	}
	return m.runAgentLaunchWithContext(ctx, launch)
}

func (m Model) launchFlowEmbeddedWithContext(ctx actions.AgentLaunchContext) (Model, tea.Cmd) {
	ctx.Embedded = true
	ctx.FlowLaunchTracked = true
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err := m.openFlowEmbeddedTerminal(ctx)
	if err != nil || !opened {
		errText := "Maximum embedded terminals reached"
		if err != nil {
			errText = err.Error()
		}
		next, errText = next.markFlowLaunchNeedsAttention(ctx, errText)
		next = next.setStatus(statusOther, errText)
		return next, nil
	}
	if needsTick {
		return next.startEmbeddedTerminalTick()
	}
	return next, nil
}

func (m Model) launchTrackedFlowPhaseResumeWithContext(ctx actions.AgentLaunchContext) (Model, tea.Cmd) {
	ctx.FlowLaunchTracked = true
	ctx.Embedded = true
	ctx.Headless = false
	updated, err := m.addFlowPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   ctx.FlowID,
		PhaseID:  ctx.FlowPhaseID,
		LaunchID: ctx.LaunchID,
		Resume:   true,
	})
	if err != nil {
		m = m.setStatus(statusOther, fmt.Sprintf("failed to mark flow phase resume: %v", err))
		return m, nil
	}
	// The store decided from the persisted record whether this resume preserved
	// a terminal phase or reopened a running one; the snapshot the launch
	// context was built from may be stale, so failure handling must follow the
	// persisted status.
	if phase, ok := flowPhaseByID(updated, ctx.FlowPhaseID); ok {
		ctx.FlowPhaseTerminal = flowstore.PhaseStatusTerminal(phase.Status)
	}
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err := m.openFlowEmbeddedTerminal(ctx)
	if err != nil || !opened {
		errText := "Maximum embedded terminals reached"
		if err != nil {
			errText = err.Error()
		}
		next, errText = next.markFlowLaunchNeedsAttention(ctx, errText)
		next = next.setStatus(statusOther, errText)
		if next.mode == ui.ModeFlows {
			next, fetchCmd := next.startFetchMode(ui.ModeFlows)
			return next, fetchCmd
		}
		return next, nil
	}
	var launchCmd tea.Cmd
	if needsTick {
		next, launchCmd = next.startEmbeddedTerminalTick()
	}
	if next.mode == ui.ModeFlows {
		next, fetchCmd := next.startFetchMode(ui.ModeFlows)
		return next, tea.Batch(fetchCmd, launchCmd)
	}
	return next, launchCmd
}

func (m Model) runAgentLaunchWithContext(ctx actions.AgentLaunchContext, launch actions.TerminalLaunchSpec) (Model, tea.Cmd) {
	if launch.Interactive {
		// wtui hands over the TTY until the launch command exits. Some launch
		// commands are only terminal/multiplexer clients; launch.Detached records
		// whether provider hooks, not this result, own session completion.
		return m, tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			if err != nil {
				if launch.Cleanup != nil {
					launch.Cleanup()
				}
				return AgentResultMsg{LaunchContext: ctx, Err: err.Error(), Detached: launch.Detached}
			}
			return AgentResultMsg{LaunchContext: ctx, Detached: launch.Detached}
		})
	}
	// Detached launch: the command only opens or switches to an external
	// terminal/multiplexer session and returns while the agent keeps running.
	return m, func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			if launch.Cleanup != nil {
				launch.Cleanup()
			}
			return AgentResultMsg{LaunchContext: ctx, Err: err.Error(), Detached: true}
		}
		return AgentResultMsg{LaunchContext: ctx, Detached: true}
	}
}

func (m Model) markFlowLaunchNeedsAttention(ctx actions.AgentLaunchContext, errText string) (Model, string) {
	if ctx.FlowID == "" || ctx.FlowPhaseID == "" || (ctx.ResumeSessionID != "" && !ctx.FlowLaunchTracked) {
		return m, errText
	}
	if ctx.FlowPhaseTerminal {
		// The phase had already finished when this launch (a session resume)
		// started; a failed resume must not regress it to needs_attention.
		return m, errText
	}
	notes := "Agent launch failed"
	if errText != "" {
		notes += ": " + errText
	}
	status := flowstore.PhaseNeedsAttention
	outcome := ""
	if ctx.FlowPhaseID == "plan-review" {
		status = flowstore.PhaseBlocked
		outcome = flowstore.OutcomeBlocked
	}
	if _, err := m.setFlowPhase(flowstore.PhaseUpdate{
		FlowID:  ctx.FlowID,
		PhaseID: ctx.FlowPhaseID,
		Status:  status,
		Outcome: outcome,
		Notes:   notes,
	}); err != nil && errText != "" {
		return m, errText + "; update flow phase: " + err.Error()
	}
	return m, errText
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
	if m.mode == ui.ModeWorktrees {
		if wt, ok := m.selectedWorktree(); ok {
			branch = wt.BranchName
			commit = wt.Commit
		}
	}
	if m.mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok {
			branch = row.Branch.Name
		}
	}
	return actions.AgentLaunchContext{
		Command:          m.agentCommand,
		LaunchID:         newLaunchID(),
		RepoPath:         repoPath,
		WorktreePath:     path,
		Branch:           branch,
		Commit:           commit,
		SessionStateRoot: m.sessionStateRoot,
	}
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
	return m.openAtPath(actions.OpenVSCode)
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
	if m.mode != ui.ModeFlows || m.selectedFlowPhaseID != "" {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" {
		return m, nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
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
	if m.mode == ui.ModeWorktrees && len(m.filteredWorktrees()) > 0 && len(m.filteredRepos()) > 0 {
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

// resetModeCursors zeroes the cursor and scroll positions for non-worktree
// right-pane views without discarding loaded list data. The worktree selection
// is intentionally preserved across mode switches so users can inspect another
// pane and return to the same selected worktree.
func (m Model) resetModeCursors() Model {
	m.rows = m.rows.ResetSelection()
	m.stashes = m.stashes.ResetSelection()
	m.commits = m.commits.ResetSelection()
	m.reflogs = m.reflogs.ResetSelection()
	m.sessions = m.sessions.ResetSelection()
	m.plans = m.plans.ResetSelection()
	m.flows = m.flows.ResetSelection()
	m = m.setExpandedPlanID("")
	m = m.setExpandedFlowID("")
	m.flowFocus = flowFocusList
	m.terminalPrefixActive = false
	m = m.clearInlineWorktreeSessions()
	m = m.invalidateViewRequest()
	return m
}

func (m Model) resetRightPaneCursors() Model {
	m = m.invalidateListRequests()
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
	m = m.setExpandedPlanID("")
	m = m.setExpandedFlowID("")
	m.flowFocus = flowFocusList
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
	return height
}

func (m Model) rightContentHeight() int {
	height := m.height - ui.BranchContentOverhead
	if height <= 0 {
		return 16
	}
	return height
}

func (m Model) planContentHeight() int {
	height := m.height - ui.PlanContentOverhead
	if height <= 0 {
		return 1
	}
	return height
}

func (m Model) flowContentHeight() int {
	height := m.height - ui.FlowContentOverhead
	if height <= 0 {
		return 1
	}
	if m.hasEmbeddedTerminalForScope(embeddedTerminalScopeFlow) {
		listHeight, _ := ui.FlowSplitPanelHeights(m.rightContentHeight())
		// The split renderer spends TableHeaderRows of the list panel on the
		// header, so only the remainder holds data rows.
		if rows := listHeight - ui.TableHeaderRows; rows > 0 {
			return rows
		}
		if listHeight > 0 {
			return 1
		}
	}
	return height
}

func (m Model) sessionContentHeight() int {
	height := m.height - ui.SessionContentOverhead
	if height <= 0 {
		return 1
	}
	return height
}

func (m Model) worktreeSessionContentHeight() int {
	height := m.worktreeContentHeight() - 2
	if height <= 0 {
		return 1
	}
	return height
}

func (m Model) contentHeightForMode() int {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.worktreeContentHeight()
	case ui.ModeStashes:
		return m.stashContentHeight()
	case ui.ModeSessions:
		return m.sessionContentHeight()
	case ui.ModePlans:
		return m.planContentHeight()
	case ui.ModeFlows:
		return m.flowContentHeight()
	default:
		return m.rightContentHeight()
	}
}

func (m Model) worktreeContentHeight() int {
	height := m.height - ui.WorktreeContentOverhead
	if height <= 0 {
		return 16
	}
	return height
}

func (m Model) stashContentHeight() int {
	height := m.height - ui.StashContentOverhead
	if height <= 0 {
		return 1
	}
	return height
}
