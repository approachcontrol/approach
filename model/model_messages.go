package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/ui"
)

// --- Messages ---

type BranchResultMsg struct {
	RepoPath string
	Branches []gitquery.Branch
}

type StashResultMsg struct {
	RepoPath string
	Stashes  []gitquery.Stash
}

type StashDiffResultMsg struct {
	RepoPath string
	Index    int
	Diff     string
}

type BranchDiffResultMsg struct {
	RepoPath   string
	BranchName string
	Diff       string
}

type BranchDeletedMsg struct {
	RepoPath string
}

type StashDroppedMsg struct {
	RepoPath string
}

type WorktreeResultMsg struct {
	RepoPath  string
	Worktrees []gitquery.Worktree
}

type CommitResultMsg struct {
	RepoPath string
	Commits  []gitquery.Commit
}

type CommitDiffResultMsg struct {
	RepoPath string
	Hash     string
	Diff     string
}

type WorktreeDiffResultMsg struct {
	RepoPath     string
	WorktreePath string
	Diff         string
}

type WorktreeRemovedMsg struct {
	RepoPath   string
	BranchName string // empty if detached
}

type WorktreeDeleteCompletedMsg struct {
	RepoPath string
}

type WorktreePrunedMsg struct {
	RepoPath string
}

type WorktreeUnlockedMsg struct {
	RepoPath string
}

type WorktreeUnlockFailedMsg struct {
	RepoPath string
	Err      string
}

type GitFetchedMsg struct {
	RepoPath string
}

type GitFetchFailedMsg struct {
	RepoPath string
	Err      string
}

type GitPulledMsg struct {
	RepoPath string
}

type GitPullFailedMsg struct {
	RepoPath string
	Err      string
}

type WorktreeCreatedMsg struct {
	RepoPath     string
	WorktreePath string
}

type WorktreeCreateFailedMsg struct {
	RepoPath string
	Input    string
	Err      string
}

type ReflogResultMsg struct {
	RepoPath string
	Reflogs  []gitquery.ReflogEntry
}

type ReflogDiffResultMsg struct {
	RepoPath string
	Hash     string
	Diff     string
}

type ClipboardResultMsg struct {
	Err string
}

type TerminalResultMsg struct {
	Err string
}

type DeleteFailedMsg struct {
	RepoPath    string
	Target      string       // display name (branch name or worktree path)
	ForceAction func() error // the --force variant to call
	SuccessMsg  tea.Msg      // returned after force succeeds; defaults to BranchDeletedMsg
}

// ForceDeleteFailedMsg is returned when the --force delete variant itself fails.
type ForceDeleteFailedMsg struct {
	RepoPath string
	Target   string
	Err      string
}

// FetchErrorMsg carries an error encountered while loading data for a pane,
// so the failure can be surfaced instead of showing a blank pane.
type FetchErrorMsg struct {
	RepoPath string
	Pane     string
	Err      string
}

// ActionFailedMsg carries an error from a destructive action (drop/prune)
// so the failure can be surfaced via the transient error line.
type ActionFailedMsg struct {
	RepoPath string
	Err      string
}

// --- Message handlers ---

func (m Model) currentRepoPath() (string, bool) {
	repos := m.filteredRepos()
	if len(repos) == 0 || m.selected >= len(repos) {
		return "", false
	}
	return repos[m.selected].Path, true
}

func (m Model) isCurrentRepo(repoPath string) bool {
	current, ok := m.currentRepoPath()
	return ok && current == repoPath
}

func (m Model) handleWorktreeResult(msg WorktreeResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.worktrees = msg.Worktrees
		m = m.clampSelectionsAfterFilter()
	}
	return m
}

func (m Model) handleWorktreeRemoved(msg WorktreeRemovedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if m.worktreeSelected >= len(m.worktrees)-1 && m.worktreeSelected > 0 {
		m.worktreeSelected--
	}
	if msg.BranchName == "" {
		return m, m.fetchWorktrees()
	}
	repoPath := msg.RepoPath
	branchName := msg.BranchName
	m.confirmPrompt = fmt.Sprintf("Also delete branch %s? (y/n)", branchName)
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.DeleteBranch(repoPath, branchName); err != nil {
				return DeleteFailedMsg{
					RepoPath:    repoPath,
					Target:      branchName,
					ForceAction: func() error { return actions.ForceDeleteBranch(repoPath, branchName) },
					SuccessMsg:  WorktreeDeleteCompletedMsg{RepoPath: repoPath},
				}
			}
			return WorktreeDeleteCompletedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = ui.OverlayConfirm
	return m, m.fetchWorktrees()
}

func (m Model) handleWorktreePruned(msg WorktreePrunedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.worktreeSelected >= len(m.worktrees)-1 && m.worktreeSelected > 0 {
			m.worktreeSelected--
		}
		return m, m.fetchWorktrees()
	}
	return m, nil
}

func (m Model) handleWorktreeUnlocked(msg WorktreeUnlockedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = ""
		return m, m.fetchWorktrees()
	}
	return m, nil
}

func (m Model) handleWorktreeUnlockFailed(msg WorktreeUnlockFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = msg.Err
	}
	return m
}

func (m Model) handleGitFetched(msg GitFetchedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = ""
		return m, m.fetchForMode()
	}
	return m, nil
}

func (m Model) handleGitFetchFailed(msg GitFetchFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = msg.Err
	}
	return m
}

func (m Model) handleGitPulled(msg GitPulledMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = ""
		return m, m.fetchForMode()
	}
	return m, nil
}

func (m Model) handleGitPullFailed(msg GitPullFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = msg.Err
	}
	return m
}

func (m Model) handleWorktreeCreated(msg WorktreeCreatedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	m.mode = ui.ModeWorktrees
	m.worktreeSelected = 0
	m.worktreeScroll = 0
	return m, m.fetchWorktrees()
}

func (m Model) handleWorktreeCreateFailed(msg WorktreeCreateFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.overlay = ui.OverlayWorktreeInput
		m.worktreeInput = msg.Input
		if msg.Err == "" {
			m.worktreeInputErr = "Unable to create worktree"
		} else {
			m.worktreeInputErr = msg.Err
		}
	}
	return m
}

func (m Model) handleBranchResult(msg BranchResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		allRows := gitquery.FlattenBranches(msg.Branches)
		var filtered []gitquery.BranchRow
		for _, row := range allRows {
			if row.Branch.IsWorktree && row.WorktreePath != msg.RepoPath {
				continue
			}
			filtered = append(filtered, row)
		}
		// Pin root worktree to position 0
		for i, row := range filtered {
			if row.WorktreePath == msg.RepoPath {
				if i != 0 {
					root := filtered[i]
					copy(filtered[1:i+1], filtered[:i])
					filtered[0] = root
				}
				break
			}
		}
		m.rows = filtered
		m = m.clampSelectionsAfterFilter()
	}
	return m
}

func (m Model) handleStashResult(msg StashResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.stashes = msg.Stashes
		m = m.clampSelectionsAfterFilter()
	}
	return m
}

func (m Model) handleStashDiffResult(msg StashDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.overlayDiff = msg.Diff
	}
	return m
}

func (m Model) handleWorktreeDiffResult(msg WorktreeDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if wt, ok := m.selectedWorktree(); ok && wt.Path == msg.WorktreePath {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleBranchDiffResult(msg BranchDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if row, ok := m.selectedRow(); ok && row.Branch.Name == msg.BranchName {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleStashDropped(msg StashDroppedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.stashSelected >= len(m.stashes)-1 && m.stashSelected > 0 {
			m.stashSelected--
		}
		return m, m.fetchStashes()
	}
	return m, nil
}

func (m Model) handleBranchDeleted(msg BranchDeletedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		return m, m.fetchBranches()
	}
	return m, nil
}

func (m Model) handleDeleteFailed(msg DeleteFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.confirmPrompt = fmt.Sprintf("Force delete %s? (y/n)", msg.Target)
		m.confirmForce = true
		m.overlay = ui.OverlayConfirm
		successMsg := msg.SuccessMsg
		repoPath := msg.RepoPath
		target := msg.Target
		forceAction := msg.ForceAction
		m.confirmAction = func() tea.Cmd {
			return func() tea.Msg {
				if err := forceAction(); err != nil {
					return ForceDeleteFailedMsg{
						RepoPath: repoPath,
						Target:   target,
						Err:      err.Error(),
					}
				}
				if successMsg != nil {
					return successMsg
				}
				return BranchDeletedMsg{RepoPath: repoPath}
			}
		}
	}
	return m
}

func (m Model) handleForceDeleteFailed(msg ForceDeleteFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if msg.Err != "" {
			m.transientError = msg.Err
		} else {
			m.transientError = fmt.Sprintf("force delete %s failed", msg.Target)
		}
	}
	return m
}

func (m Model) handleFetchError(msg FetchErrorMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = msg.Err
	}
	return m
}

func (m Model) handleActionFailed(msg ActionFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.transientError = msg.Err
	}
	return m
}

func (m Model) handleCommitResult(msg CommitResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.commits = msg.Commits
		m = m.clampSelectionsAfterFilter()
	}
	return m
}

func (m Model) handleReflogResult(msg ReflogResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.reflogs = msg.Reflogs
		m = m.clampSelectionsAfterFilter()
	}
	return m
}

func (m Model) handleCommitDiffResult(msg CommitDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if commit, ok := m.selectedCommit(); ok && commit.Hash == msg.Hash {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleReflogDiffResult(msg ReflogDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if entry, ok := m.selectedReflog(); ok && entry.Hash == msg.Hash {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleCopyHash() (tea.Model, tea.Cmd) {
	var hash string
	switch {
	case m.mode == ui.ModeHistory:
		commit, ok := m.selectedCommit()
		if !ok {
			return m, nil
		}
		hash = commit.Hash
	case m.mode == ui.ModeReflog:
		entry, ok := m.selectedReflog()
		if !ok {
			return m, nil
		}
		hash = entry.Hash
	default:
		return m, nil
	}
	return m, func() tea.Msg {
		if err := actions.CopyToClipboard(hash); err != nil {
			return ClipboardResultMsg{Err: err.Error()}
		}
		return ClipboardResultMsg{}
	}
}
