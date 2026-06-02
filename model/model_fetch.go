package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/ui"
)

// --- Fetch commands ---

func (m Model) fetchForMode() tea.Cmd {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.fetchWorktrees()
	case ui.ModeBranches:
		return m.fetchBranches()
	case ui.ModeStashes:
		return m.fetchStashes()
	case ui.ModeHistory:
		return m.fetchCommits()
	case ui.ModeReflog:
		return m.fetchReflog()
	}
	return nil
}

func (m Model) canFetch() bool {
	if m.activePane != 1 {
		return false
	}
	_, _, ok := m.fetchTargetPath()
	return ok
}

func (m Model) canPull() bool {
	if m.activePane != 1 {
		return false
	}
	_, _, ok := m.pullTargetPath()
	return ok
}

func (m Model) fetchTargetPath() (string, string, bool) {
	return m.gitTargetPath(false)
}

func (m Model) pullTargetPath() (string, string, bool) {
	return m.gitTargetPath(true)
}

// gitTargetPath returns the repo path and the working directory to run a fetch
// or pull against. When forPull is true the ui.ModeBranches case additionally
// requires the selected branch to have a checked-out worktree, since a bare
// branch has no working tree to pull into.
func (m Model) gitTargetPath(forPull bool) (string, string, bool) {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return "", "", false
	}
	switch m.mode {
	case ui.ModeWorktrees:
		wt, ok := m.selectedWorktree()
		if !ok {
			return repoPath, repoPath, true
		}
		if wt.Stale {
			return "", "", false
		}
		return repoPath, wt.Path, true
	case ui.ModeBranches:
		row, ok := m.selectedRow()
		if !ok {
			return repoPath, repoPath, true
		}
		if row.Stale {
			return "", "", false
		}
		if row.WorktreePath != "" {
			return repoPath, row.WorktreePath, true
		}
		if forPull {
			return "", "", false
		}
		return repoPath, repoPath, true
	default:
		return "", "", false
	}
}

func (m Model) createWorktree(input string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreateWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error()}
		}
		return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath}
	}
}

func (m Model) fetchWorktrees() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktrees, err := gitquery.ListWorktrees(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "worktrees", Err: fmt.Sprintf("failed to load worktrees: %v", err)}
		}
		return WorktreeResultMsg{RepoPath: repoPath, Worktrees: worktrees}
	}
}

func (m Model) fetchBranches() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		branches, err := gitquery.ListBranches(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "branches", Err: fmt.Sprintf("failed to load branches: %v", err)}
		}
		return BranchResultMsg{RepoPath: repoPath, Branches: branches}
	}
}

func (m Model) fetchStashes() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		stashes, err := gitquery.ListStashes(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "stashes", Err: fmt.Sprintf("failed to load stashes: %v", err)}
		}
		return StashResultMsg{RepoPath: repoPath, Stashes: stashes}
	}
}

func (m Model) fetchBranchDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	row, ok := m.selectedRow()
	if !ok || !row.Branch.Dirty || !row.Branch.IsWorktree {
		return nil
	}
	worktreePath := row.WorktreePath
	if worktreePath == "" {
		worktreePath = repoPath
	}
	branchName := row.Branch.Name

	return func() tea.Msg {
		diff, err := gitquery.BranchDiff(worktreePath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "branch diff", Err: fmt.Sprintf("failed to load diff: %v", err)}
		}
		return BranchDiffResultMsg{
			RepoPath:   repoPath,
			BranchName: branchName,
			Diff:       diff,
		}
	}
}

func (m Model) fetchWorktreeDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	wt, ok := m.selectedWorktree()
	if !ok || !wt.Dirty || wt.Stale {
		return nil
	}
	worktreePath := wt.Path
	return func() tea.Msg {
		diff, err := gitquery.BranchDiff(worktreePath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "worktree diff", Err: fmt.Sprintf("failed to load diff: %v", err)}
		}
		return WorktreeDiffResultMsg{
			RepoPath:     repoPath,
			WorktreePath: worktreePath,
			Diff:         diff,
		}
	}
}

func (m Model) fetchStashDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	stash, ok := m.selectedStash()
	if !ok {
		return nil
	}
	index := stash.Index
	return func() tea.Msg {
		diff, err := gitquery.StashDiff(repoPath, index)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "stash diff", Err: fmt.Sprintf("failed to load diff: %v", err)}
		}
		return StashDiffResultMsg{RepoPath: repoPath, Index: index, Diff: diff}
	}
}

func (m Model) fetchCommits() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		commits, err := gitquery.ListCommits(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "history", Err: fmt.Sprintf("failed to load commits: %v", err)}
		}
		return CommitResultMsg{RepoPath: repoPath, Commits: commits}
	}
}

func (m Model) fetchReflog() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		reflogs, err := gitquery.ListReflog(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "reflog", Err: fmt.Sprintf("failed to load reflog: %v", err)}
		}
		return ReflogResultMsg{RepoPath: repoPath, Reflogs: reflogs}
	}
}

func (m Model) fetchReflogDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	entry, ok := m.selectedReflog()
	if !ok {
		return nil
	}
	hash := entry.Hash
	return func() tea.Msg {
		diff, err := gitquery.ReflogDiff(repoPath, hash)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "reflog diff", Err: fmt.Sprintf("failed to load diff: %v", err)}
		}
		return ReflogDiffResultMsg{RepoPath: repoPath, Hash: hash, Diff: diff}
	}
}

func (m Model) fetchCommitDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	commit, ok := m.selectedCommit()
	if !ok {
		return nil
	}
	hash := commit.Hash
	return func() tea.Msg {
		diff, err := gitquery.CommitDiff(repoPath, hash)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "commit diff", Err: fmt.Sprintf("failed to load diff: %v", err)}
		}
		return CommitDiffResultMsg{RepoPath: repoPath, Hash: hash, Diff: diff}
	}
}
