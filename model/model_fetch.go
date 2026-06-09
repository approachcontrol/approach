package model

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

// --- Fetch commands ---

const visibleRepoFetchFailureNameLimit = 3
const visibleRepoFetchStatusTTL = 3 * time.Second
const visibleRepoFetchFadeStepDuration = 1 * time.Second

func (m Model) startFetchForMode() (Model, tea.Cmd) {
	return m.startFetchMode(m.mode)
}

func (m Model) startGlobalRefresh() (Model, tea.Cmd) {
	if m.scanRepos == nil {
		m = m.setStatus(statusOther, "refresh unavailable")
		return m, nil
	}

	m.repoRefreshSeq++
	request := m.repoRefreshSeq
	m.activeRepoRefresh = request

	scanRepos := m.scanRepos
	scanCmd := func() tea.Msg {
		repos, err := scanRepos()
		if err != nil {
			return RepoRefreshFailedMsg{Request: request, Err: err.Error()}
		}
		return RepoRefreshResultMsg{Request: request, Repos: repos}
	}

	cmds := []tea.Cmd{scanCmd}
	if repoPath, ok := m.currentRepoPath(); ok {
		if _, ok := listFetchDescriptorForMode(m.mode); ok {
			inlineWorktreePath := ""
			if m.mode == ui.ModeWorktrees && m.inlineWorktreeSessionPath != "" {
				inlineWorktreePath = m.inlineWorktreeSessionPath
			}
			var fetchCmd tea.Cmd
			m, fetchCmd = m.startFetchForMode()
			if inlineWorktreePath != "" && fetchCmd != nil {
				m.pendingInlineSessionRepo = repoPath
				m.pendingInlineSessionPath = inlineWorktreePath
				m.pendingInlineSessionList = m.currentListRequest(ui.ModeWorktrees)
			}
			if fetchCmd != nil {
				cmds = append(cmds, fetchCmd)
			}
		}
	}
	if len(cmds) == 1 {
		return m, scanCmd
	}
	return m, tea.Batch(cmds...)
}

func (m Model) fetchForMode() tea.Cmd {
	return m.fetchMode(m.mode, m.currentListRequest(m.mode))
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

func (m Model) invalidateListRequests() Model {
	m.listRequestSeq++
	for i := range m.listRequests {
		m.listRequests[i] = m.listRequestSeq
	}
	return m
}

func (m Model) nextWorktreeSessionRequest(repoPath, worktreePath string) (Model, uint64) {
	m.worktreeSessionRequestSeq++
	m.activeWorktreeSessionReq = m.worktreeSessionRequestSeq
	m.inlineWorktreeSessionRepo = repoPath
	m.inlineWorktreeSessionPath = worktreePath
	m.worktreeSessions = newSessionPane()
	return m, m.activeWorktreeSessionReq
}

func (m Model) isCurrentWorktreeSessionRequest(msg WorktreeSessionResultMsg) bool {
	return msg.Request != 0 &&
		msg.Request == m.activeWorktreeSessionReq &&
		msg.RepoPath == m.inlineWorktreeSessionRepo &&
		msg.WorktreePath == m.inlineWorktreeSessionPath
}

func (m Model) pendingInlineSessionRefresh(repoPath string, listRequest uint64) (string, bool) {
	if m.pendingInlineSessionRepo != repoPath || m.pendingInlineSessionList != listRequest {
		return "", false
	}
	return m.pendingInlineSessionPath, m.pendingInlineSessionPath != ""
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

func (m Model) nextFlowCreateRequest() (Model, uint64) {
	m.flowCreateSeq++
	m.activeFlowCreate = m.flowCreateSeq
	return m, m.activeFlowCreate
}

func (m Model) isCurrentFlowCreateRequest(request uint64) bool {
	return request == m.activeFlowCreate
}

func (m Model) clearFlowCreateRequest(request uint64) Model {
	if request != 0 && request == m.activeFlowCreate {
		m.activeFlowCreate = 0
	}
	return m
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

type listFetchDescriptor struct {
	mode        ui.Mode
	pane        string
	errorPrefix string
	load        func(Model, string, uint64) (tea.Msg, error)
	beforeStart func(Model) Model
}

func listFetchDescriptorForMode(mode ui.Mode) (listFetchDescriptor, bool) {
	switch mode {
	case ui.ModeWorktrees:
		return listFetchDescriptor{
			mode:        ui.ModeWorktrees,
			pane:        "worktrees",
			errorPrefix: "failed to load worktrees",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				worktrees, err := gitquery.ListWorktrees(repoPath)
				if err != nil {
					return nil, err
				}
				return WorktreeResultMsg{RepoPath: repoPath, Worktrees: worktrees, ListRequest: request}, nil
			},
		}, true
	case ui.ModeBranches:
		return listFetchDescriptor{
			mode:        ui.ModeBranches,
			pane:        "branches",
			errorPrefix: "failed to load branches",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				branches, err := gitquery.ListBranches(repoPath)
				if err != nil {
					return nil, err
				}
				return BranchResultMsg{RepoPath: repoPath, Branches: branches, ListRequest: request}, nil
			},
		}, true
	case ui.ModeStashes:
		return listFetchDescriptor{
			mode:        ui.ModeStashes,
			pane:        "stashes",
			errorPrefix: "failed to load stashes",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				stashes, err := gitquery.ListStashes(repoPath)
				if err != nil {
					return nil, err
				}
				return StashResultMsg{RepoPath: repoPath, Stashes: stashes, ListRequest: request}, nil
			},
		}, true
	case ui.ModeHistory:
		return listFetchDescriptor{
			mode:        ui.ModeHistory,
			pane:        "history",
			errorPrefix: "failed to load commits",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				commits, err := gitquery.ListCommits(repoPath)
				if err != nil {
					return nil, err
				}
				return CommitResultMsg{RepoPath: repoPath, Commits: commits, ListRequest: request}, nil
			},
		}, true
	case ui.ModeReflog:
		return listFetchDescriptor{
			mode:        ui.ModeReflog,
			pane:        "reflog",
			errorPrefix: "failed to load reflog",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				reflogs, err := gitquery.ListReflog(repoPath)
				if err != nil {
					return nil, err
				}
				return ReflogResultMsg{RepoPath: repoPath, Reflogs: reflogs, ListRequest: request}, nil
			},
		}, true
	case ui.ModeSessions:
		return listFetchDescriptor{
			mode:        ui.ModeSessions,
			pane:        "sessions",
			errorPrefix: "failed to load sessions",
			load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
				records, err := m.listSessions(sessions.SessionFilter{RepoPath: repoPath})
				if err != nil {
					return nil, err
				}
				return SessionResultMsg{RepoPath: repoPath, Sessions: records, ListRequest: request}, nil
			},
		}, true
	case ui.ModePlans:
		return listFetchDescriptor{
			mode:        ui.ModePlans,
			pane:        "plans",
			errorPrefix: "failed to load plans",
			beforeStart: func(m Model) Model {
				return m.setExpandedPlanID("")
			},
			load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
				records, err := m.listPlans(planstore.PlanFilter{RepoPath: repoPath})
				if err != nil {
					return nil, err
				}
				return PlanResultMsg{RepoPath: repoPath, Plans: records, ListRequest: request}, nil
			},
		}, true
	case ui.ModeFlows:
		return listFetchDescriptor{
			mode:        ui.ModeFlows,
			pane:        "flows",
			errorPrefix: "failed to load flows",
			load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
				records, err := m.listFlows(flowstore.FlowFilter{RepoPath: repoPath})
				if err != nil {
					return nil, err
				}
				return FlowResultMsg{RepoPath: repoPath, Flows: records, ListRequest: request}, nil
			},
		}, true
	default:
		return listFetchDescriptor{}, false
	}
}

func (m Model) startFetchMode(mode ui.Mode) (Model, tea.Cmd) {
	desc, ok := listFetchDescriptorForMode(mode)
	if !ok {
		return m, nil
	}
	m, request := m.nextListFetchRequest(desc.mode)
	if desc.beforeStart != nil {
		m = desc.beforeStart(m)
	}
	return m, m.fetchList(desc, request)
}

func (m Model) fetchMode(mode ui.Mode, request uint64) tea.Cmd {
	desc, ok := listFetchDescriptorForMode(mode)
	if !ok {
		return nil
	}
	return m.fetchList(desc, request)
}

func (m Model) fetchList(desc listFetchDescriptor, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		msg, err := desc.load(m, repoPath, request)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        desc.pane,
				Err:         fmt.Sprintf("%s: %v", desc.errorPrefix, err),
				Kind:        FetchList,
				Mode:        desc.mode,
				ListRequest: request,
			}
		}
		return msg
	}
}

func (m Model) fetchWorktreeSessions(worktreePath string, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || worktreePath == "" {
		return nil
	}
	return func() tea.Msg {
		records, err := m.listSessions(sessions.SessionFilter{RepoPath: repoPath, WorktreePath: worktreePath})
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "worktree sessions",
				Err:          fmt.Sprintf("failed to load worktree sessions: %v", err),
				Kind:         FetchList,
				Mode:         ui.ModeWorktrees,
				ListRequest:  request,
				WorktreePath: worktreePath,
			}
		}
		return WorktreeSessionResultMsg{RepoPath: repoPath, WorktreePath: worktreePath, Sessions: records, Request: request}
	}
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

func (m Model) replaceReposPreservingVisibleSelection(repos []scanner.Repo, previousPath string) (Model, bool, bool) {
	m.repos = m.repos.SetItems(repos)
	visible := m.filteredRepos()
	if len(visible) == 0 {
		return m.reflowRepos(), previousPath != "", false
	}
	if previousPath != "" {
		for _, repo := range visible {
			if repo.Path == previousPath {
				m = m.selectFilteredRepo(previousPath)
				return m, false, true
			}
		}
	}
	currentPath, _ := m.currentRepoPath()
	return m.reflowRepos(), previousPath != currentPath, true
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

func (m Model) createFlowAndLaunchPlan(title, instructions, baseRef string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		result, err := m.startFlowPlan(FlowStartRequest{
			RepoPath:         repoPath,
			Title:            title,
			Instructions:     instructions,
			BaseRef:          baseRef,
			AgentCommand:     m.agentCommand,
			SessionStateRoot: m.sessionStateRoot,
			PlanPhaseID:      flowPlanPhaseID,
			PlanPhaseTitle:   "Plan",
			PlanPhaseStatus:  flowstore.PhaseRunning,
		})
		if err != nil {
			return FlowCreateFailedMsg{RepoPath: repoPath, Title: title, Err: err.Error()}
		}
		return PlanLaunchRequestedMsg{LaunchContext: result.LaunchContext}
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
	diffRequest := m.activeViewRequest

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
	diffRequest := m.activeViewRequest
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
	diffRequest := m.activeViewRequest
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
	diffRequest := m.activeViewRequest
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
	diffRequest := m.activeViewRequest
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
	record, ok := m.selectedPlan()
	if !ok {
		return nil
	}
	return m.fetchPlanTextByID(record.PlanID, ui.ModePlans)
}

func (m Model) fetchPlanTextByID(planID string, mode ui.Mode) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || planID == "" {
		return nil
	}
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		body, err := m.readPlan(planID)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "plan",
				Err:         fmt.Sprintf("failed to load plan: %v", err),
				Kind:        FetchPlanText,
				Mode:        mode,
				DiffRequest: diffRequest,
				PlanID:      planID,
			}
		}
		return PlanReadResultMsg{
			RepoPath:    repoPath,
			PlanID:      planID,
			Mode:        mode,
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
	diffRequest := m.activeViewRequest
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
