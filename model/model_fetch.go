package model

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

// --- Fetch commands ---

const visibleRepoFetchFailureNameLimit = 3
const visibleRepoFetchStatusTTL = 3 * time.Second
const visibleRepoFetchFadeStepDuration = 1 * time.Second

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
	case ui.ModeSessions:
		return m.startFetchSessions()
	case ui.ModePlans:
		return m.startFetchPlans()
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
	case ui.ModeSessions:
		return m.fetchSessions(request)
	case ui.ModePlans:
		return m.fetchPlans(request)
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

func (m Model) nextWorktreeCreateRequest() (Model, uint64) {
	m.worktreeCreateSeq++
	m.activeWorktreeCreate = m.worktreeCreateSeq
	return m, m.activeWorktreeCreate
}

func (m Model) isCurrentWorktreeCreateRequest(request uint64) bool {
	return request == m.activeWorktreeCreate
}

func (m Model) clearWorktreeCreateRequest(request uint64) Model {
	if request != 0 && request == m.activeWorktreeCreate {
		m.activeWorktreeCreate = 0
	}
	return m
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

func (m Model) startFetchSessions() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeSessions)
	return m, m.fetchSessions(request)
}

func (m Model) startFetchPlans() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModePlans)
	m.expandedPlanID = ""
	return m, m.fetchPlans(request)
}

func (m Model) startFetchVisibleRepos() (Model, tea.Cmd) {
	if m.visibleRepoFetch.Request != 0 {
		return m, nil
	}

	repos := m.filteredRepos()
	if len(repos) == 0 {
		m.visibleRepoFetch = visibleRepoFetchState{}
		m = m.setStatus(statusGitMutation, "No visible repos to fetch")
		return m, nil
	}

	m.visibleRepoFetchSeq++
	request := m.visibleRepoFetchSeq
	capturedPaths := make(map[string]struct{}, len(repos))
	cmds := make([]tea.Cmd, 0, len(repos))
	for _, repo := range repos {
		repo := repo
		capturedPaths[repo.Path] = struct{}{}
		cmds = append(cmds, func() tea.Msg {
			errText := ""
			if err := m.fetchRepo(repo.Path); err != nil {
				errText = fmt.Sprintf("fetch failed: %v", err)
			}
			return VisibleRepoFetchResultMsg{
				Request:     request,
				RepoPath:    repo.Path,
				DisplayName: repo.DisplayName,
				Err:         errText,
			}
		})
	}
	m.visibleRepoFetch = visibleRepoFetchState{
		Request:       request,
		Total:         len(repos),
		CapturedPaths: capturedPaths,
	}
	return m, tea.Batch(cmds...)
}

func (m Model) canFetch() bool {
	if m.activePane != 1 {
		return false
	}
	_, _, ok := m.fetchTargetPath()
	return ok
}

func (m Model) canFetchVisibleRepos() bool {
	return m.activePane == 0 && len(m.filteredRepos()) > 0
}

func (m Model) visibleRepoFetchProgressText() string {
	return fmt.Sprintf("Fetching %d/%d visible %s...", m.visibleRepoFetch.Completed, m.visibleRepoFetch.Total, visibleRepoNoun(m.visibleRepoFetch.Total))
}

func (m Model) visibleRepoFetchFinalStatusText() string {
	total := m.visibleRepoFetch.Total
	if m.visibleRepoFetch.FailureCount == 0 {
		return fmt.Sprintf("Fetched %d visible %s", total, visibleRepoNoun(total))
	}
	failed := strings.Join(m.visibleRepoFetch.FailureNames, ", ")
	remaining := m.visibleRepoFetch.FailureCount - len(m.visibleRepoFetch.FailureNames)
	if remaining > 0 {
		failed = fmt.Sprintf("%s +%d more", failed, remaining)
	}
	return fmt.Sprintf("Fetched %d/%d visible %s; failed: %s", m.visibleRepoFetch.Successes, total, visibleRepoNoun(total), failed)
}

func visibleRepoNoun(count int) string {
	if count == 1 {
		return "repo"
	}
	return "repos"
}

func expireVisibleRepoFetchStatus(request uint64, text string) tea.Cmd {
	return tea.Tick(visibleRepoFetchStatusTTL, func(time.Time) tea.Msg {
		return VisibleRepoFetchStatusExpiredMsg{Request: request, Text: text}
	})
}

func fadeVisibleRepoFetchStatus(request uint64, text string, step int) tea.Cmd {
	return tea.Tick(time.Duration(step)*visibleRepoFetchFadeStepDuration, func(time.Time) tea.Msg {
		return VisibleRepoFetchStatusFadeMsg{Request: request, Text: text, Step: step}
	})
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
	repo, ok := m.currentRepo()
	if !ok {
		return "", "", false
	}
	repoPath := repo.Path
	switch m.mode {
	case ui.ModeWorktrees:
		wt, ok := m.selectedWorktree()
		if !ok {
			if forPull && repo.IsBare {
				return "", "", false
			}
			return repoPath, repoPath, true
		}
		if wt.Stale {
			return "", "", false
		}
		return repoPath, wt.Path, true
	case ui.ModeBranches:
		row, ok := m.selectedRow()
		if !ok {
			if forPull && repo.IsBare {
				return "", "", false
			}
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

func (m Model) createWorktree(input string, launchAgent bool, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreateWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), LaunchAgent: launchAgent, Request: request}
		}
		return m.finishWorktreeCreate(repoPath, worktreePath, input, actions.WorktreeCreateGeneric, launchAgent, request)
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

func (m Model) createPullRequestWorktree(input string, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreatePullRequestWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), Kind: WorktreeCreatePullRequest, Request: request}
		}
		ref, err := actions.NormalizePullRequestWorktreeRef(input)
		if err != nil {
			ref = input
		}
		return m.finishWorktreeCreate(repoPath, worktreePath, ref, actions.WorktreeCreatePullRequest, false, request)
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

func (m Model) finishWorktreeCreate(repoPath, worktreePath, ref string, kind actions.WorktreeCreateKind, launchAgent bool, request uint64) tea.Msg {
	branch := createdWorktreeBranch(worktreePath, ref, kind)
	hook, ok := m.bootstrapHookForRepo(repoPath)
	if !ok {
		return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath, Branch: branch, LaunchAgent: launchAgent, Request: request}
	}
	ctx := actions.BootstrapContext{
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Ref:          ref,
		Kind:         kind,
	}
	if err := m.runBootstrapHook(ctx, hook); err != nil {
		return WorktreeBootstrapFailedMsg{RepoPath: repoPath, WorktreePath: worktreePath, Err: err.Error(), LaunchAgent: launchAgent, Request: request}
	}
	return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath, Branch: branch, LaunchAgent: launchAgent, BootstrapRan: true, Request: request}
}

func createdWorktreeBranch(worktreePath, ref string, kind actions.WorktreeCreateKind) string {
	if branch, err := gitquery.CurrentBranch(worktreePath); err == nil {
		return branch
	}
	if kind == actions.WorktreeCreatePullRequest {
		return "pr-" + ref
	}
	return ""
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

func (m Model) fetchSessions(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		records, err := m.listSessions(sessions.SessionFilter{RepoPath: repoPath})
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "sessions", Err: fmt.Sprintf("failed to load sessions: %v", err), Kind: FetchList, Mode: ui.ModeSessions, ListRequest: request}
		}
		return SessionResultMsg{RepoPath: repoPath, Sessions: records, ListRequest: request}
	}
}

func (m Model) fetchPlans(request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		records, err := m.listPlans(planstore.PlanFilter{RepoPath: repoPath})
		if err != nil {
			return FetchErrorMsg{RepoPath: repoPath, Pane: "plans", Err: fmt.Sprintf("failed to load plans: %v", err), Kind: FetchList, Mode: ui.ModePlans, ListRequest: request}
		}
		return PlanResultMsg{RepoPath: repoPath, Plans: records, ListRequest: request}
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

func (m Model) fetchSessionTranscript() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	record, ok := m.selectedSession()
	if !ok {
		return nil
	}
	diffRequest := m.modal.View().Request
	return func() tea.Msg {
		events, err := m.readTranscript(record.Provider, record.SessionID)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "session transcript",
				Err:         fmt.Sprintf("failed to load transcript: %v", err),
				Kind:        FetchSessionTranscript,
				Mode:        ui.ModeSessions,
				DiffRequest: diffRequest,
				Provider:    record.Provider,
				SessionID:   record.SessionID,
			}
		}
		return SessionTranscriptResultMsg{
			RepoPath:    repoPath,
			Provider:    record.Provider,
			SessionID:   record.SessionID,
			DiffRequest: diffRequest,
			Transcript:  formatTranscript(events),
		}
	}
}

func (m Model) fetchPlanText() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	record, ok := m.selectedPlan()
	if !ok {
		return nil
	}
	planID := record.PlanID
	diffRequest := m.modal.View().Request
	return func() tea.Msg {
		body, err := m.readPlan(planID)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "plan",
				Err:         fmt.Sprintf("failed to load plan: %v", err),
				Kind:        FetchPlanText,
				Mode:        ui.ModePlans,
				DiffRequest: diffRequest,
				PlanID:      planID,
			}
		}
		return PlanReadResultMsg{
			RepoPath:    repoPath,
			PlanID:      planID,
			DiffRequest: diffRequest,
			Text:        body,
		}
	}
}

func formatTranscript(events []sessions.TranscriptEvent) string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		prefix := event.Role
		if event.Kind != "" && event.Kind != "message" {
			prefix += " " + event.Kind
		}
		lines = append(lines, fmt.Sprintf("%s: %s", prefix, event.Text))
	}
	return strings.Join(lines, "\n")
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
