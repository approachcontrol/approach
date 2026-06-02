package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/ui"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.overlay == ui.OverlayConfirm {
		return m.handleConfirmKey(key)
	}
	if m.overlay == ui.OverlayWorktreeInput {
		return m.handleWorktreeInputKey(msg)
	}
	if m.overlay != ui.OverlayNone {
		if m.activePane == 0 {
			return m.handleLeftPaneKey(key)
		}
		return m.handleOverlayKey(key)
	}

	m.transientError = ""

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
					return m, m.fetchForMode()
				}
			}
		}
		return m, nil
	}

	if key == "D" {
		m.destructive = !m.destructive
		return m, nil
	}

	if m.activePane == 0 {
		return m.handleLeftPaneKey(key)
	}
	return m.handleRightPaneKey(key)
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
				return m, m.fetchForMode()
			}
		}
	}
	return m, nil
}

// --- Key handlers by context ---

func (m Model) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "enter":
		action := m.confirmAction
		m = m.clearConfirm()
		return m, action()
	case "n", "q", "esc":
		m = m.clearConfirm()
	}
	return m, nil
}

func (m Model) handleWorktreeInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		input := strings.TrimSpace(m.worktreeInput)
		if input == "" {
			m.worktreeInputErr = "Enter a branch, tag, or new branch name"
			return m, nil
		}
		m = m.clearWorktreeInput()
		return m, m.createWorktree(input)
	case "esc", "ctrl+c":
		m = m.clearWorktreeInput()
	case "backspace", "ctrl+h":
		runes := []rune(m.worktreeInput)
		if len(runes) > 0 {
			m.worktreeInput = string(runes[:len(runes)-1])
			m.worktreeInputErr = ""
		}
	default:
		if len(msg.Runes) > 0 {
			m.worktreeInput += string(msg.Runes)
			m.worktreeInputErr = ""
		}
	}
	return m, nil
}

func (m Model) handleLeftPaneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "tab":
		m.activePane = 1
	case "up", "k":
		repos := m.filteredRepos()
		if len(repos) > 0 {
			if m.selected > 0 {
				m.selected--
			} else {
				m.selected = len(repos) - 1
			}
			m = m.ensureRepoVisible()
			m = m.resetRightPaneCursors()
			return m, m.fetchForMode()
		}
	case "down", "j":
		repos := m.filteredRepos()
		if len(repos) > 0 {
			if m.selected < len(repos)-1 {
				m.selected++
			} else {
				m.selected = 0
			}
			m = m.ensureRepoVisible()
			m = m.resetRightPaneCursors()
			return m, m.fetchForMode()
		}
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleOverlayKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc":
		m.overlay = ui.OverlayNone
		m.overlayDiff = ""
		m.overlayScroll = 0
	case "up", "k":
		if m.overlayScroll > 0 {
			m.overlayScroll--
		}
	case "down", "j":
		m.overlayScroll++
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
			return m, m.fetchForMode()
		}
	case "right", "l":
		if m.mode < ui.ModeReflog {
			m.mode++
			m = m.resetModeCursors()
			return m, m.fetchForMode()
		}
	case "1":
		if m.mode != ui.ModeWorktrees {
			m.mode = ui.ModeWorktrees
			m = m.resetModeCursors()
			return m, m.fetchWorktrees()
		}
	case "2":
		if m.mode != ui.ModeBranches {
			m.mode = ui.ModeBranches
			m = m.resetModeCursors()
			return m, m.fetchBranches()
		}
	case "3":
		if m.mode != ui.ModeStashes {
			m.mode = ui.ModeStashes
			m = m.resetModeCursors()
			return m, m.fetchStashes()
		}
	case "4":
		if m.mode != ui.ModeHistory {
			m.mode = ui.ModeHistory
			m = m.resetModeCursors()
			return m, m.fetchCommits()
		}
	case "5":
		if m.mode != ui.ModeReflog {
			m.mode = ui.ModeReflog
			m = m.resetModeCursors()
			return m, m.fetchReflog()
		}
	case "y":
		return m.handleCopyHash()
	case "tab":
		m.activePane = 0
	case "enter":
		return m.handleEnter()
	case "n":
		if m.mode == ui.ModeWorktrees {
			return m.handleNewWorktree()
		}
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

// moveItemCursor advances a cursor by delta within [0,count), wrapping around
// at both ends. When there are no items it leaves the cursor unchanged.
func moveItemCursor(selected, count, delta int) int {
	if count <= 0 {
		return selected
	}
	return ((selected+delta)%count + count) % count
}

// moveCursor moves the selected item in the active right-pane view by delta
// (-1 for up, +1 for down) and keeps the new selection visible.
func (m Model) moveCursor(delta int) Model {
	switch m.mode {
	case ui.ModeWorktrees:
		m.worktreeSelected = moveItemCursor(m.worktreeSelected, len(m.filteredWorktrees()), delta)
		m = m.ensureWorktreeVisible()
	case ui.ModeBranches:
		m.branchSelected = moveItemCursor(m.branchSelected, len(m.filteredRows()), delta)
		m = m.ensureBranchVisible()
	case ui.ModeStashes:
		m.stashSelected = moveItemCursor(m.stashSelected, len(m.filteredStashes()), delta)
		m = m.ensureStashVisible()
	case ui.ModeHistory:
		m.commitSelected = moveItemCursor(m.commitSelected, len(m.filteredCommits()), delta)
		m = m.ensureCommitVisible()
	case ui.ModeReflog:
		m.reflogSelected = moveItemCursor(m.reflogSelected, len(m.filteredReflogs()), delta)
		m = m.ensureReflogVisible()
	}
	return m
}

// --- Action handlers ---

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	if m.mode == ui.ModeWorktrees {
		wt, ok := m.selectedWorktree()
		if ok && wt.Dirty && !wt.Stale {
			m.overlay = ui.OverlayWorktreeDiff
			return m, m.fetchWorktreeDiff()
		}
		return m, nil
	}
	if m.mode == ui.ModeBranches && m.isSelectedBranchDirtyWorktree() {
		m.overlay = ui.OverlayBranchDiff
		return m, m.fetchBranchDiff()
	}
	if m.mode == ui.ModeStashes && len(m.filteredStashes()) > 0 {
		m.overlay = ui.OverlayStashDiff
		return m, m.fetchStashDiff()
	}
	if m.mode == ui.ModeHistory && len(m.filteredCommits()) > 0 {
		m.overlay = ui.OverlayCommitDiff
		return m, m.fetchCommitDiff()
	}
	if m.mode == ui.ModeReflog && len(m.filteredReflogs()) > 0 {
		m.overlay = ui.OverlayReflogDiff
		return m, m.fetchReflogDiff()
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

func (m Model) handleNewWorktree() (tea.Model, tea.Cmd) {
	if _, ok := m.currentRepoPath(); !ok {
		return m, nil
	}
	m.overlay = ui.OverlayWorktreeInput
	m.worktreeInput = ""
	m.worktreeInputErr = ""
	return m, nil
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
		if err := actions.Fetch(path); err != nil {
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
		path, ok := m.currentRepoPath()
		if !ok {
			return "", false
		}
		return path, true
	}
	if m.mode == ui.ModeBranches {
		if row, ok := m.selectedRow(); ok && row.WorktreePath != "" {
			return row.WorktreePath, true
		}
	}
	return "", false
}

func (m Model) handleOpenTerminal() (tea.Model, tea.Cmd) {
	path, ok := m.pathForOpenAction()
	if !ok {
		return m, nil
	}
	launch, err := actions.TerminalLaunch(path)
	if err != nil {
		m.transientError = err.Error()
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
	m.confirmPrompt = fmt.Sprintf("Drop stash@{%d}? (y/n)", idx)
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.DropStash(repoPath, idx); err != nil {
				return ActionFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("failed to drop stash: %v", err)}
			}
			return StashDroppedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = ui.OverlayConfirm
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
	if row.WorktreePath == repoPath {
		return m, nil
	}

	branchName := row.Branch.Name
	m.confirmPrompt = fmt.Sprintf("Delete branch %s? (y/n)", branchName)
	m.confirmAction = func() tea.Cmd {
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
	}
	m.overlay = ui.OverlayConfirm
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

	m.confirmPrompt = fmt.Sprintf("Remove worktree at %s? (y/n)", wtPath)
	m.confirmAction = func() tea.Cmd {
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
	}
	m.overlay = ui.OverlayConfirm
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
	m.confirmPrompt = "Prune stale worktrees? (y/n)"
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.PruneWorktree(repoPath); err != nil {
				return ActionFailedMsg{RepoPath: repoPath, Err: fmt.Sprintf("failed to prune worktrees: %v", err)}
			}
			return WorktreePrunedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = ui.OverlayConfirm
	return m, nil
}

func (m Model) clearConfirm() Model {
	m.overlay = ui.OverlayNone
	m.confirmPrompt = ""
	m.confirmAction = nil
	m.confirmForce = false
	return m
}

func (m Model) clearWorktreeInput() Model {
	m.overlay = ui.OverlayNone
	m.worktreeInput = ""
	m.worktreeInputErr = ""
	return m
}

// resetModeCursors zeroes the cursor and scroll positions for every right-pane
// view without discarding the loaded list data. It is used when switching modes.
func (m Model) resetModeCursors() Model {
	m.worktreeSelected = 0
	m.worktreeScroll = 0
	m.branchSelected = 0
	m.branchScroll = 0
	m.stashSelected = 0
	m.stashScroll = 0
	m.commitSelected = 0
	m.commitScroll = 0
	m.reflogSelected = 0
	m.reflogScroll = 0
	return m
}

func (m Model) resetRightPaneCursors() Model {
	m.branchSelected = 0
	m.stashSelected = 0
	m.branchScroll = 0
	m.stashScroll = 0
	m.worktreeSelected = 0
	m.worktreeScroll = 0
	m.commitSelected = 0
	m.commitScroll = 0
	m.reflogSelected = 0
	m.reflogScroll = 0
	m.rows = nil
	m.stashes = nil
	m.worktrees = nil
	m.commits = nil
	m.reflogs = nil
	return m
}
