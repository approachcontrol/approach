package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/ui"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.modal.IsOpen() {
		var cmd tea.Cmd
		view := m.modal.View()
		var outcome modal.Outcome
		m.modal, outcome, cmd = m.modal.Update(msg)
		if outcome == modal.Accepted && cmd != nil && isWorktreeCreateInput(view) {
			var request uint64
			m, request = m.nextWorktreeCreateRequest()
			cmd = tagWorktreeCreateRequest(cmd, request)
		}
		return m, cmd
	}

	m = m.clearAnyStatus()

	if m.searchActive {
		return m.handleSearchKey(msg)
	}

	if key == "/" {
		m.searchActive = true
		return m, nil
	}

	if key == "esc" && m.activeSearchQuery() != "" {
		oldRepoPath, _ := m.currentRepoPath()
		m = m.setActiveSearchQuery("")
		m.searchActive = false
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

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	oldRepoPath, _ := m.currentRepoPath()

	switch key {
	case "esc":
		m = m.setActiveSearchQuery("")
		m.searchActive = false
	case "enter":
		m.searchActive = false
	case "backspace", "ctrl+h":
		q := m.activeSearchQuery()
		if q != "" {
			runes := []rune(q)
			m = m.setActiveSearchQuery(string(runes[:len(runes)-1]))
		} else {
			m = m.setActiveSearchQuery("")
			m.searchActive = false
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

// --- Key handlers by context ---

func (m Model) handleLeftPaneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "tab":
		m.activePane = 1
	case "up", "k":
		if len(m.filteredRepos()) > 0 {
			m.repos = m.repos.Move(-1, m.repoContentHeight(), ui.LeftPaneWidth-2)
			m = m.resetRightPaneCursors()
			return m.startFetchForMode()
		}
	case "down", "j":
		if len(m.filteredRepos()) > 0 {
			m.repos = m.repos.Move(1, m.repoContentHeight(), ui.LeftPaneWidth-2)
			m = m.resetRightPaneCursors()
			return m.startFetchForMode()
		}
	case "f":
		return m.startFetchVisibleRepos()
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleRightPaneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		return m.handleCursorUp()
	case "down", "j":
		return m.handleCursorDown()
	case "left", "h":
		if m.mode > ui.ModeWorktrees {
			m.mode--
			m = m.resetModeCursors()
			return m.startFetchForMode()
		}
	case "right", "l":
		if m.mode < ui.ModeSessions {
			m.mode++
			m = m.resetModeCursors()
			return m.startFetchForMode()
		}
	case "1":
		if m.mode != ui.ModeWorktrees {
			m.mode = ui.ModeWorktrees
			m = m.resetModeCursors()
			return m.startFetchWorktrees()
		}
	case "2":
		if m.mode != ui.ModeBranches {
			m.mode = ui.ModeBranches
			m = m.resetModeCursors()
			return m.startFetchBranches()
		}
	case "3":
		if m.mode != ui.ModeStashes {
			m.mode = ui.ModeStashes
			m = m.resetModeCursors()
			return m.startFetchStashes()
		}
	case "4":
		if m.mode != ui.ModeHistory {
			m.mode = ui.ModeHistory
			m = m.resetModeCursors()
			return m.startFetchCommits()
		}
	case "5":
		if m.mode != ui.ModeReflog {
			m.mode = ui.ModeReflog
			m = m.resetModeCursors()
			return m.startFetchReflog()
		}
	case "6":
		if m.mode != ui.ModeSessions {
			m.mode = ui.ModeSessions
			m = m.resetModeCursors()
			return m.startFetchSessions()
		}
	case "y":
		return m.handleCopyHash()
	case "tab":
		m.activePane = 0
	case "enter":
		return m.handleEnter()
	case "n":
		if m.mode == ui.ModeWorktrees {
			return m.handleNewWorktree(false)
		}
		if m.mode == ui.ModeBranches {
			return m.handleNewBranch()
		}
	case "P":
		if m.mode == ui.ModeWorktrees {
			return m.handleNewPullRequestWorktree()
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
		return m, tea.Quit
	}
	return m, nil
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
	}
	return m
}

// --- Action handlers ---

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	if m.mode == ui.ModeWorktrees {
		wt, ok := m.selectedWorktree()
		if ok && wt.Dirty && !wt.Stale {
			m = m.openDiff(modal.DiffWorktree)
			return m, m.fetchWorktreeDiff()
		}
		return m, nil
	}
	if m.mode == ui.ModeBranches && m.isSelectedBranchDirtyWorktree() {
		m = m.openDiff(modal.DiffBranch)
		return m, m.fetchBranchDiff()
	}
	if m.mode == ui.ModeStashes && len(m.filteredStashes()) > 0 {
		m = m.openDiff(modal.DiffStash)
		return m, m.fetchStashDiff()
	}
	if m.mode == ui.ModeHistory && len(m.filteredCommits()) > 0 {
		m = m.openDiff(modal.DiffCommit)
		return m, m.fetchCommitDiff()
	}
	if m.mode == ui.ModeReflog && len(m.filteredReflogs()) > 0 {
		m = m.openDiff(modal.DiffReflog)
		return m, m.fetchReflogDiff()
	}
	if m.mode == ui.ModeSessions && len(m.filteredSessions()) > 0 {
		m = m.openDiff(modal.DiffSessionTranscript)
		return m, m.fetchSessionTranscript()
	}
	return m, nil
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
	if m.mode == ui.ModeBranches && len(m.filteredRepos()) > 0 {
		return m.confirmBranchDelete()
	}
	if m.mode == ui.ModeWorktrees && len(m.filteredWorktrees()) > 0 && len(m.filteredRepos()) > 0 {
		return m.confirmWorktreeDelete()
	}
	return m, nil
}

func (m Model) handleSetAgent() (tea.Model, tea.Cmd) {
	m.modal = modal.OpenInput(
		"Set agent (codex or claude)",
		ui.AgentInputPlaceholder,
		m.agentCommand,
		validateAgentInput,
		func(input string) tea.Cmd { return m.setAgent(agent.Normalize(input)) },
	)
	return m, nil
}

func validateAgentInput(input string) error {
	return agent.Validate(input)
}

func (m Model) setAgent(command string) tea.Cmd {
	return func() tea.Msg {
		if err := m.saveAgent(command); err != nil {
			return AgentSetFailedMsg{Command: command, Err: err.Error()}
		}
		return AgentSetMsg{Command: command}
	}
}

func (m Model) handleNewWorktree(launchAgent bool) (tea.Model, tea.Cmd) {
	if _, ok := m.currentRepoPath(); !ok {
		return m, nil
	}
	if launchAgent && m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose codex or claude before launching an agent")
		return m, nil
	}
	prompt := "Create worktree from"
	if launchAgent {
		prompt = "Create worktree and launch agent from"
	}
	m.modal = modal.OpenInput(
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
	m.modal = modal.OpenInput(
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
	m.modal = modal.OpenInput(
		ui.PRWorktreePrompt,
		ui.PRWorktreeInputPlaceholder,
		"",
		func(input string) error { return validatePullRequestWorktreeInput(repoPath, input) },
		func(input string) tea.Cmd { return m.createPullRequestWorktree(input, 0) },
	)
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

func validatePullRequestWorktreeInput(repoPath, input string) error {
	return actions.ValidatePullRequestWorktreeInput(repoPath, input)
}

func (m Model) handleMoveWorktree() (tea.Model, tea.Cmd) {
	wt, ok := m.selectedWorktree()
	if !ok || !canMoveWorktree(wt) {
		return m, nil
	}
	oldPath := wt.Path
	m.modal = modal.OpenInput(
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
	path, ok := m.agentTargetPath()
	if !ok {
		return m, nil
	}
	if m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose codex or claude before launching an agent")
		return m, nil
	}
	return m.launchAgentAtPath(path)
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
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	if launch.Interactive {
		return m, tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			if err != nil {
				return AgentResultMsg{LaunchContext: ctx, Err: err.Error()}
			}
			return AgentResultMsg{LaunchContext: ctx}
		})
	}
	return m, func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			return AgentResultMsg{LaunchContext: ctx, Err: err.Error()}
		}
		return AgentResultMsg{LaunchContext: ctx}
	}
}

func (m Model) agentLaunchContext(path string) actions.AgentLaunchContext {
	repoPath, _ := m.currentRepoPath()
	branch := ""
	if m.mode == ui.ModeWorktrees {
		if wt, ok := m.selectedWorktree(); ok {
			branch = wt.BranchName
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
		SessionStateRoot: m.sessionStateRoot,
	}
}

func (m Model) handleOpenTerminal() (tea.Model, tea.Cmd) {
	path, ok := m.pathForOpenAction()
	if !ok {
		return m, nil
	}
	launch, err := actions.TerminalLaunch(path)
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
	return m
}

func (m Model) resetRightPaneCursors() Model {
	m.pendingBranchSelection = ""
	m.pendingWorktreeSelection = ""
	m.rows = m.rows.SetItems(nil).ResetSelection()
	m.stashes = m.stashes.SetItems(nil).ResetSelection()
	m.worktrees = m.worktrees.SetItems(nil).ResetSelection()
	m.commits = m.commits.SetItems(nil).ResetSelection()
	m.reflogs = m.reflogs.SetItems(nil).ResetSelection()
	m.sessions = m.sessions.SetItems(nil).ResetSelection()
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

func (m Model) contentHeightForMode() int {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.worktreeContentHeight()
	case ui.ModeStashes:
		return m.stashContentHeight()
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
