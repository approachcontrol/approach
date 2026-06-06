package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/ui"
)

// --- Fetch commands ---

func (m Model) startFetchForMode() (Model, tea.Cmd) {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.startFetchWorktrees()
	case ui.ModeBranches:
		return m.startFetchBranches()
	case ui.ModeStashes:
		return m.startFetchStashes()
	case ui.ModeHistory:
		return m.startFetchCommits()
	case ui.ModeReflog:
		return m.startFetchReflog()
	}
	return m, nil
}

func (m Model) fetchForMode() tea.Cmd {
	request := m.currentListRequest(m.mode)
	switch m.mode {
	case ui.ModeWorktrees:
		return m.fetchWorktrees(request)
	case ui.ModeBranches:
		return m.fetchBranches(request)
	case ui.ModeStashes:
		return m.fetchStashes(request)
	case ui.ModeHistory:
		return m.fetchCommits(request)
	case ui.ModeReflog:
		return m.fetchReflog(request)
	}
	return nil
}

func (m Model) currentListRequest(mode ui.Mode) uint64 {
	if int(mode) < 0 || int(mode) >= len(m.listRequests) {
		return 0
	}
	return m.listRequests[int(mode)]
}

func (m Model) nextListFetchRequest(mode ui.Mode) (Model, uint64) {
	m.listRequestSeq++
	request := m.listRequestSeq
	if int(mode) >= 0 && int(mode) < len(m.listRequests) {
		m.listRequests[int(mode)] = request
	}
	return m, request
}

func (m Model) startFetchWorktrees() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeWorktrees)
	return m, m.fetchWorktrees(request)
}

func (m Model) startFetchBranches() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeBranches)
	return m, m.fetchBranches(request)
}

func (m Model) startFetchStashes() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeStashes)
	return m, m.fetchStashes(request)
}

func (m Model) startFetchCommits() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeHistory)
	return m, m.fetchCommits(request)
}

func (m Model) startFetchReflog() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeReflog)
	return m, m.fetchReflog(request)
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

func (m Model) createWorktree(input string, launchAgent bool) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreateWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), LaunchAgent: launchAgent}
		}
		return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath, LaunchAgent: launchAgent}
	}
}

func (m Model) createBranch(input string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return m.createBranchCommand(repoPath, input, m.selectedBranchStartPoint())
}

func (m Model) createBranchFromStartPoint(input, startPoint string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return m.createBranchCommand(repoPath, input, startPoint)
}

func (m Model) createBranchCommand(repoPath, input, startPoint string) tea.Cmd {
	return func() tea.Msg {
		if err := actions.CreateBranch(repoPath, input, startPoint); err != nil {
			return BranchCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), StartPoint: startPoint}
		}
		return BranchCreatedMsg{RepoPath: repoPath, Name: input}
	}
}

func (m Model) selectedBranchStartPoint() string {
	if m.mode != ui.ModeBranches {
		return ""
	}
	row, ok := m.selectedRow()
	if !ok || row.Branch.Name == "(detached)" {
		return ""
	}
	if row.Branch.FullRef != "" {
		return row.Branch.FullRef
	}
	return "refs/heads/" + row.Branch.Name
}

func (m Model) createPullRequestWorktree(input string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreatePullRequestWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), Kind: WorktreeCreatePullRequest}
		}
		return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath}
	}
}

func (m Model) moveWorktree(oldPath, input string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		newPath, err := actions.MoveWorktree(repoPath, oldPath, input)
		if err != nil {
			return WorktreeMoveFailedMsg{RepoPath: repoPath, OldPath: oldPath, Input: input, Err: err.Error()}
		}
		return WorktreeMovedMsg{RepoPath: repoPath, OldPath: oldPath, NewPath: newPath}
	}
}

func (m Model) fetchWorktrees(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktrees, err := gitquery.ListWorktrees(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "worktrees", Err: fmt.Sprintf("failed to load worktrees: %v", err), Kind: FetchList, Mode: ui.ModeWorktrees, ListRequest: request}
		}
		return WorktreeResultMsg{RepoPath: repoPath, Worktrees: worktrees, ListRequest: request}
	}
}

func (m Model) fetchBranches(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		branches, err := gitquery.ListBranches(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "branches", Err: fmt.Sprintf("failed to load branches: %v", err), Kind: FetchList, Mode: ui.ModeBranches, ListRequest: request}
		}
		return BranchResultMsg{RepoPath: repoPath, Branches: branches, ListRequest: request}
	}
}

func (m Model) fetchStashes(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		stashes, err := gitquery.ListStashes(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "stashes", Err: fmt.Sprintf("failed to load stashes: %v", err), Kind: FetchList, Mode: ui.ModeStashes, ListRequest: request}
		}
		return StashResultMsg{RepoPath: repoPath, Stashes: stashes, ListRequest: request}
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
	diffRequest := m.modal.View().Request

	return func() tea.Msg {
		diff, err := gitquery.BranchDiff(worktreePath)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "branch diff",
				Err:          fmt.Sprintf("failed to load diff: %v", err),
				Kind:         FetchBranchDiff,
				Mode:         ui.ModeBranches,
				DiffRequest:  diffRequest,
				BranchName:   branchName,
				WorktreePath: worktreePath,
			}
		}
		return BranchDiffResultMsg{
			RepoPath:     repoPath,
			BranchName:   branchName,
			WorktreePath: worktreePath,
			DiffRequest:  diffRequest,
			Diff:         diff,
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
	diffRequest := m.modal.View().Request
	return func() tea.Msg {
		diff, err := gitquery.BranchDiff(worktreePath)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "worktree diff",
				Err:          fmt.Sprintf("failed to load diff: %v", err),
				Kind:         FetchWorktreeDiff,
				Mode:         ui.ModeWorktrees,
				DiffRequest:  diffRequest,
				WorktreePath: worktreePath,
			}
		}
		return WorktreeDiffResultMsg{
			RepoPath:     repoPath,
			WorktreePath: worktreePath,
			DiffRequest:  diffRequest,
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
	stashDate := stash.Date
	stashMessage := stash.Message
	diffRequest := m.modal.View().Request
	return func() tea.Msg {
		diff, err := gitquery.StashDiff(repoPath, index)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "stash diff",
				Err:          fmt.Sprintf("failed to load diff: %v", err),
				Kind:         FetchStashDiff,
				Mode:         ui.ModeStashes,
				DiffRequest:  diffRequest,
				StashIndex:   index,
				StashDate:    stashDate,
				StashMessage: stashMessage,
			}
		}
		return StashDiffResultMsg{
			RepoPath:    repoPath,
			Index:       index,
			Date:        stashDate,
			Message:     stashMessage,
			DiffRequest: diffRequest,
			Diff:        diff,
		}
	}
}

func (m Model) fetchCommits(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		commits, err := gitquery.ListCommits(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "history", Err: fmt.Sprintf("failed to load commits: %v", err), Kind: FetchList, Mode: ui.ModeHistory, ListRequest: request}
		}
		return CommitResultMsg{RepoPath: repoPath, Commits: commits, ListRequest: request}
	}
}

func (m Model) fetchReflog(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		reflogs, err := gitquery.ListReflog(repoPath)
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "reflog", Err: fmt.Sprintf("failed to load reflog: %v", err), Kind: FetchList, Mode: ui.ModeReflog, ListRequest: request}
		}
		return ReflogResultMsg{RepoPath: repoPath, Reflogs: reflogs, ListRequest: request}
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
	diffRequest := m.modal.View().Request
	return func() tea.Msg {
		diff, err := gitquery.ReflogDiff(repoPath, hash)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "reflog diff",
				Err:         fmt.Sprintf("failed to load diff: %v", err),
				Kind:        FetchReflogDiff,
				Mode:        ui.ModeReflog,
				DiffRequest: diffRequest,
				Hash:        hash,
			}
		}
		return ReflogDiffResultMsg{RepoPath: repoPath, Hash: hash, DiffRequest: diffRequest, Diff: diff}
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
	diffRequest := m.modal.View().Request
	return func() tea.Msg {
		diff, err := gitquery.CommitDiff(repoPath, hash)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "commit diff",
				Err:         fmt.Sprintf("failed to load diff: %v", err),
				Kind:        FetchCommitDiff,
				Mode:        ui.ModeHistory,
				DiffRequest: diffRequest,
				Hash:        hash,
			}
		}
		return CommitDiffResultMsg{RepoPath: repoPath, Hash: hash, DiffRequest: diffRequest, Diff: diff}
	}
}
