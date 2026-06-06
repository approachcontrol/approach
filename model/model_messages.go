package model

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

// --- Messages ---

type BranchResultMsg struct {
	RepoPath    string
	Branches    []gitquery.Branch
	ListRequest uint64
}

type StashResultMsg struct {
	RepoPath    string
	Stashes     []gitquery.Stash
	ListRequest uint64
}

type StashDiffResultMsg struct {
	RepoPath    string
	Index       int
	Date        string
	Message     string
	DiffRequest uint64
	Diff        string
}

type BranchDiffResultMsg struct {
	RepoPath     string
	BranchName   string
	WorktreePath string
	DiffRequest  uint64
	Diff         string
}

type BranchDeletedMsg struct {
	RepoPath string
}

type BranchCreatedMsg struct {
	RepoPath string
	Name     string
}

type BranchCreateFailedMsg struct {
	RepoPath   string
	Input      string
	Err        string
	StartPoint string
}

type StashDroppedMsg struct {
	RepoPath string
}

type WorktreeResultMsg struct {
	RepoPath    string
	Worktrees   []gitquery.Worktree
	ListRequest uint64
}

type CommitResultMsg struct {
	RepoPath    string
	Commits     []gitquery.Commit
	ListRequest uint64
}

type CommitDiffResultMsg struct {
	RepoPath    string
	Hash        string
	DiffRequest uint64
	Diff        string
}

type WorktreeDiffResultMsg struct {
	RepoPath     string
	WorktreePath string
	DiffRequest  uint64
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

type VisibleRepoFetchResultMsg struct {
	Request     uint64
	RepoPath    string
	DisplayName string
	Err         string
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
	LaunchAgent  bool
	BootstrapRan bool
	Request      uint64
}

type WorktreeMovedMsg struct {
	RepoPath string
	OldPath  string
	NewPath  string
}

type WorktreeMoveFailedMsg struct {
	RepoPath string
	OldPath  string
	Input    string
	Err      string
}

type WorktreeCreateKind int

const (
	WorktreeCreateGeneric WorktreeCreateKind = iota
	WorktreeCreatePullRequest
)

type WorktreeCreateFailedMsg struct {
	RepoPath    string
	Input       string
	Err         string
	Kind        WorktreeCreateKind
	LaunchAgent bool
	Request     uint64
}

type WorktreeBootstrapFailedMsg struct {
	RepoPath     string
	WorktreePath string
	Err          string
	LaunchAgent  bool
	Request      uint64
}

type ReflogResultMsg struct {
	RepoPath    string
	Reflogs     []gitquery.ReflogEntry
	ListRequest uint64
}

type ReflogDiffResultMsg struct {
	RepoPath    string
	Hash        string
	DiffRequest uint64
	Diff        string
}

type ClipboardResultMsg struct {
	Err string
}

type TerminalResultMsg struct {
	Err string
}

type AgentSetMsg struct {
	Command string
}

type AgentSetFailedMsg struct {
	Command string
	Err     string
}

type AgentResultMsg struct {
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

type FetchKind int

const (
	FetchUnknown FetchKind = iota
	FetchList
	FetchWorktreeDiff
	FetchBranchDiff
	FetchStashDiff
	FetchCommitDiff
	FetchReflogDiff
)

// FetchErrorMsg carries an error encountered while loading data for a pane,
// so the failure can be surfaced instead of showing a blank pane. Pane is only
// for display; Kind and target fields drive stale-result checks.
type FetchErrorMsg struct {
	RepoPath     string
	Pane         string
	Err          string
	Kind         FetchKind
	Mode         ui.Mode
	ListRequest  uint64
	DiffRequest  uint64
	WorktreePath string
	BranchName   string
	StashIndex   int
	StashDate    string
	StashMessage string
	Hash         string
}

// ActionFailedMsg carries an error from a destructive action (drop/prune)
// so the failure can be surfaced via the transient error line.
type ActionFailedMsg struct {
	RepoPath string
	Err      string
}

// --- Message handlers ---

func (m Model) currentRepoPath() (string, bool) {
	repo, ok := m.currentRepo()
	if !ok {
		return "", false
	}
	return repo.Path, true
}

func (m Model) currentRepo() (scanner.Repo, bool) {
	repo, ok := m.repos.Selected()
	if !ok {
		return scanner.Repo{}, false
	}
	return repo, true
}

func (m Model) isCurrentRepo(repoPath string) bool {
	current, ok := m.currentRepoPath()
	return ok && current == repoPath
}

func (m Model) setStatus(source statusSource, text string) Model {
	m.status = statusError{Text: text, Source: source}
	return m
}

func (m Model) setFetchStatus(msg FetchErrorMsg) Model {
	m.status = statusError{Text: msg.Err, Source: statusFetch, FetchKind: msg.Kind, Mode: msg.Mode}
	return m
}

func (m Model) clearStatus(source statusSource) Model {
	if m.status.Source == source {
		m.status = statusError{}
	}
	return m
}

func (m Model) clearFetchListStatus(mode ui.Mode) Model {
	if m.status.Source == statusFetch && m.status.FetchKind == FetchList && m.status.Mode == mode {
		m.status = statusError{}
	}
	return m
}

func (m Model) isCurrentListRequest(mode ui.Mode, request uint64) bool {
	if request == 0 {
		return false
	}
	if int(mode) < 0 || int(mode) >= len(m.listRequests) {
		return false
	}
	return m.listRequests[int(mode)] == request
}

func (m Model) clearAnyStatus() Model {
	m.status = statusError{}
	return m
}

func (m Model) visibleStatusText() string {
	if m.visibleRepoFetch.Request != 0 {
		return m.visibleRepoFetchProgressText()
	}
	return m.status.Text
}

func (m Model) handleWorktreeResult(msg WorktreeResultMsg) Model {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentListRequest(ui.ModeWorktrees, msg.ListRequest) {
		return m
	}
	m = m.clearFetchListStatus(ui.ModeWorktrees)
	m.worktrees = m.worktrees.SetItems(msg.Worktrees)
	if m.pendingWorktreeSelection != "" {
		pendingPath := m.pendingWorktreeSelection
		m.worktrees = m.worktrees.SelectFunc(func(wt gitquery.Worktree) bool {
			return wt.Path == pendingPath
		})
		m.pendingWorktreeSelection = ""
	}
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleWorktreeRemoved(msg WorktreeRemovedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if m.WorktreeSelected() >= len(m.Worktrees())-1 && m.WorktreeSelected() > 0 {
		m.worktrees = m.worktrees.Move(-1, m.worktreeContentHeight(), m.contentWidth())
	}
	if msg.BranchName == "" {
		return m.startFetchWorktrees()
	}
	repoPath := msg.RepoPath
	branchName := msg.BranchName
	m.modal = modal.OpenConfirm(fmt.Sprintf("Also delete branch %s? (y/n)", branchName), func() tea.Cmd {
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
	})
	return m.startFetchWorktrees()
}

func (m Model) handleWorktreePruned(msg WorktreePrunedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.WorktreeSelected() >= len(m.Worktrees())-1 && m.WorktreeSelected() > 0 {
			m.worktrees = m.worktrees.Move(-1, m.worktreeContentHeight(), m.contentWidth())
		}
		return m.startFetchWorktrees()
	}
	return m, nil
}

func (m Model) handleWorktreeUnlocked(msg WorktreeUnlockedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.clearStatus(statusOther)
		return m.startFetchWorktrees()
	}
	return m, nil
}

func (m Model) handleWorktreeUnlockFailed(msg WorktreeUnlockFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.setStatus(statusOther, msg.Err)
	}
	return m
}

func (m Model) handleGitFetched(msg GitFetchedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.clearStatus(statusGitMutation)
		return m.startFetchForMode()
	}
	return m, nil
}

func (m Model) handleGitFetchFailed(msg GitFetchFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.setStatus(statusGitMutation, msg.Err)
	}
	return m
}

func (m Model) handleVisibleRepoFetchResult(msg VisibleRepoFetchResultMsg) (tea.Model, tea.Cmd) {
	if msg.Request == 0 || msg.Request != m.visibleRepoFetch.Request {
		return m, nil
	}
	if msg.Err == "" {
		m.visibleRepoFetch.Successes++
	} else {
		m.visibleRepoFetch.FailureCount++
		if len(m.visibleRepoFetch.FailureNames) < visibleRepoFetchFailureNameLimit {
			name := msg.DisplayName
			if name == "" {
				name = msg.RepoPath
			}
			m.visibleRepoFetch.FailureNames = append(m.visibleRepoFetch.FailureNames, name)
		}
	}
	m.visibleRepoFetch.Completed++
	if m.visibleRepoFetch.Completed < m.visibleRepoFetch.Total {
		return m, nil
	}

	currentPath, currentOK := m.currentRepoPath()
	_, shouldRefresh := m.visibleRepoFetch.CapturedPaths[currentPath]
	finalStatus := m.visibleRepoFetchFinalStatusText()
	m.visibleRepoFetch = visibleRepoFetchState{}
	m = m.setStatus(statusGitMutation, finalStatus)
	if currentOK && shouldRefresh {
		return m.startFetchForMode()
	}
	return m, nil
}

func (m Model) handleGitPulled(msg GitPulledMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.clearStatus(statusGitMutation)
		return m.startFetchForMode()
	}
	return m, nil
}

func (m Model) handleGitPullFailed(msg GitPullFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.setStatus(statusGitMutation, msg.Err)
	}
	return m
}

func (m Model) handleWorktreeCreated(msg WorktreeCreatedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentWorktreeCreateRequest(msg.Request) {
		return m, nil
	}
	m = m.clearWorktreeCreateRequest(msg.Request)
	m.mode = ui.ModeWorktrees
	m.worktrees = m.worktrees.ResetSelection()
	m, fetchCmd := m.startFetchWorktrees()
	if !msg.LaunchAgent {
		return m, fetchCmd
	}
	m, launchCmd := m.launchAgentAtPath(msg.WorktreePath)
	return m, tea.Batch(fetchCmd, launchCmd)
}

func (m Model) handleWorktreeBootstrapFailed(msg WorktreeBootstrapFailedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentWorktreeCreateRequest(msg.Request) {
		return m, nil
	}
	m = m.clearWorktreeCreateRequest(msg.Request)
	errText := msg.Err
	if errText == "" {
		errText = "bootstrap hook failed"
	} else {
		errText = "bootstrap hook failed: " + errText
	}
	m.mode = ui.ModeWorktrees
	m.worktrees = m.worktrees.ResetSelection()
	m = m.setStatus(statusGitMutation, errText)
	return m.startFetchWorktrees()
}

func (m Model) handleWorktreeCreateFailed(msg WorktreeCreateFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) && m.isCurrentWorktreeCreateRequest(msg.Request) {
		m = m.clearWorktreeCreateRequest(msg.Request)
		errText := msg.Err
		if msg.Err == "" {
			errText = "Unable to create worktree"
		}
		prompt := "New worktree"
		placeholder := ui.WorktreeInputPlaceholder
		validate := validateWorktreeInput
		submit := func(input string) tea.Cmd { return m.createWorktree(input, msg.LaunchAgent, 0) }
		if msg.Kind == WorktreeCreatePullRequest {
			prompt = ui.PRWorktreePrompt
			placeholder = ui.PRWorktreeInputPlaceholder
			validate = func(input string) error { return validatePullRequestWorktreeInput(msg.RepoPath, input) }
			submit = func(input string) tea.Cmd { return m.createPullRequestWorktree(input, 0) }
		} else if msg.LaunchAgent {
			prompt = "Create worktree and launch agent from"
		}
		m.modal = modal.OpenInput(
			prompt,
			placeholder,
			msg.Input,
			validate,
			submit,
		).SetInputError(errText)
	}
	return m
}

func (m Model) handleWorktreeMoved(msg WorktreeMovedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	m.pendingWorktreeSelection = msg.NewPath
	m = m.clearStatus(statusOther)
	return m.startFetchWorktrees()
}

func (m Model) handleWorktreeMoveFailed(msg WorktreeMoveFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		errText := msg.Err
		if errText == "" {
			errText = "Unable to move worktree"
		}
		oldPath := msg.OldPath
		m.modal = modal.OpenInput(
			ui.WorktreeMovePrompt,
			ui.WorktreeMoveInputPlaceholder,
			msg.Input,
			validateWorktreeMoveInput,
			func(input string) tea.Cmd { return m.moveWorktree(oldPath, input) },
		).SetInputError(errText)
	}
	return m
}

func (m Model) handleAgentSet(msg AgentSetMsg) Model {
	m.agentCommand = msg.Command
	m = m.clearStatus(statusOther)
	return m
}

func (m Model) handleAgentSetFailed(msg AgentSetFailedMsg) Model {
	// Keep the selection usable for this session even when persistence fails.
	m.agentCommand = msg.Command
	errText := msg.Err
	if errText == "" {
		errText = "Unable to persist agent selection"
	}
	m = m.setStatus(statusOther, errText)
	return m
}

func (m Model) handleBranchResult(msg BranchResultMsg) Model {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentListRequest(ui.ModeBranches, msg.ListRequest) {
		return m
	}
	m = m.clearFetchListStatus(ui.ModeBranches)
	repo, _ := m.currentRepo()
	m.rows = m.rows.SetItems(branchRowsForRepo(repo, msg.Branches))
	if m.pendingBranchSelection != "" {
		pendingRef := "refs/heads/" + m.pendingBranchSelection
		m.rows = m.rows.SelectFunc(func(row gitquery.BranchRow) bool {
			return row.Branch.Name == m.pendingBranchSelection || row.Branch.FullRef == pendingRef
		})
		m.pendingBranchSelection = ""
	}
	m = m.clampSelectionsAfterFilter()
	return m
}

func branchRowsForRepo(repo scanner.Repo, branches []gitquery.Branch) []gitquery.BranchRow {
	allRows := gitquery.FlattenBranches(branches)
	filtered := make([]gitquery.BranchRow, 0, len(allRows))
	for _, row := range allRows {
		if !repo.IsBare && row.Branch.IsWorktree && !samePath(row.WorktreePath, repo.Path) {
			continue
		}
		filtered = append(filtered, row)
	}
	for i, row := range filtered {
		if !repo.IsBare && samePath(row.WorktreePath, repo.Path) {
			if i != 0 {
				root := filtered[i]
				copy(filtered[1:i+1], filtered[:i])
				filtered[0] = root
			}
			break
		}
	}
	return filtered
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return canonicalPath(a) == canonicalPath(b)
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func (m Model) handleStashResult(msg StashResultMsg) Model {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentListRequest(ui.ModeStashes, msg.ListRequest) {
		return m
	}
	m = m.clearFetchListStatus(ui.ModeStashes)
	m.stashes = m.stashes.SetItems(msg.Stashes)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleStashDiffResult(msg StashDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if stash, ok := m.selectedStash(); ok && stashMatchesDiffResult(stash, msg) {
			m.modal = m.modal.SetDiffForRequest(modal.DiffStash, msg.DiffRequest, msg.Diff)
		}
	}
	return m
}

func (m Model) handleWorktreeDiffResult(msg WorktreeDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if wt, ok := m.selectedWorktree(); ok && wt.Path == msg.WorktreePath {
			m.modal = m.modal.SetDiffForRequest(modal.DiffWorktree, msg.DiffRequest, msg.Diff)
		}
	}
	return m
}

func (m Model) handleBranchDiffResult(msg BranchDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if row, ok := m.selectedRow(); ok && branchMatchesDiffResult(row, msg) {
			m.modal = m.modal.SetDiffForRequest(modal.DiffBranch, msg.DiffRequest, msg.Diff)
		}
	}
	return m
}

func (m Model) handleStashDropped(msg StashDroppedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.StashSelected() >= len(m.Stashes())-1 && m.StashSelected() > 0 {
			m.stashes = m.stashes.Move(-1, m.stashContentHeight(), m.contentWidth())
		}
		return m.startFetchStashes()
	}
	return m, nil
}

func (m Model) handleBranchDeleted(msg BranchDeletedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		return m.startFetchBranches()
	}
	return m, nil
}

func (m Model) handleBranchCreated(msg BranchCreatedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m.mode = ui.ModeBranches
		m.rows = m.rows.SetQuery("")
		m.pendingBranchSelection = msg.Name
		return m.startFetchBranches()
	}
	return m, nil
}

func (m Model) handleBranchCreateFailed(msg BranchCreateFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		errText := msg.Err
		if msg.Err == "" {
			errText = "Unable to create branch"
		}
		m.modal = modal.OpenInput(
			ui.BranchPrompt,
			ui.BranchInputPlaceholder,
			msg.Input,
			validateBranchInput,
			func(input string) tea.Cmd { return m.createBranchFromStartPoint(input, msg.StartPoint) },
		).SetInputError(errText)
	}
	return m
}

func (m Model) handleDeleteFailed(msg DeleteFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		successMsg := msg.SuccessMsg
		repoPath := msg.RepoPath
		target := msg.Target
		forceAction := msg.ForceAction
		m.modal = modal.OpenForce(fmt.Sprintf("Force delete %s? (y/n)", msg.Target), func() tea.Cmd {
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
		})
	}
	return m
}

func (m Model) handleForceDeleteFailed(msg ForceDeleteFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		} else {
			m = m.setStatus(statusOther, fmt.Sprintf("force delete %s failed", msg.Target))
		}
	}
	return m
}

func (m Model) handleFetchError(msg FetchErrorMsg) Model {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	if !m.fetchErrorMatchesCurrentTarget(msg) {
		return m
	}
	m = m.setFetchStatus(msg)
	return m
}

func (m Model) handleActionFailed(msg ActionFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.setStatus(statusOther, msg.Err)
	}
	return m
}

func (m Model) handleCommitResult(msg CommitResultMsg) Model {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentListRequest(ui.ModeHistory, msg.ListRequest) {
		return m
	}
	m = m.clearFetchListStatus(ui.ModeHistory)
	m.commits = m.commits.SetItems(msg.Commits)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleReflogResult(msg ReflogResultMsg) Model {
	if !m.isCurrentRepo(msg.RepoPath) || !m.isCurrentListRequest(ui.ModeReflog, msg.ListRequest) {
		return m
	}
	m = m.clearFetchListStatus(ui.ModeReflog)
	m.reflogs = m.reflogs.SetItems(msg.Reflogs)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleCommitDiffResult(msg CommitDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if commit, ok := m.selectedCommit(); ok && commit.Hash == msg.Hash {
			m.modal = m.modal.SetDiffForRequest(modal.DiffCommit, msg.DiffRequest, msg.Diff)
		}
	}
	return m
}

func (m Model) handleReflogDiffResult(msg ReflogDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if entry, ok := m.selectedReflog(); ok && entry.Hash == msg.Hash {
			m.modal = m.modal.SetDiffForRequest(modal.DiffReflog, msg.DiffRequest, msg.Diff)
		}
	}
	return m
}

func stashMatchesDiffResult(stash gitquery.Stash, msg StashDiffResultMsg) bool {
	if stash.Index != msg.Index {
		return false
	}
	if stash.Date != msg.Date {
		return false
	}
	if stash.Message != msg.Message {
		return false
	}
	return true
}

func branchMatchesDiffResult(row gitquery.BranchRow, msg BranchDiffResultMsg) bool {
	if row.Branch.Name != msg.BranchName {
		return false
	}
	return row.WorktreePath == msg.WorktreePath
}

func (m Model) fetchErrorMatchesCurrentTarget(msg FetchErrorMsg) bool {
	switch msg.Kind {
	case FetchUnknown:
		return false
	case FetchList:
		return msg.Mode == m.mode && m.isCurrentListRequest(msg.Mode, msg.ListRequest)
	case FetchWorktreeDiff:
		if m.modal.View().Request != msg.DiffRequest {
			return false
		}
		wt, ok := m.selectedWorktree()
		return ok && wt.Path == msg.WorktreePath
	case FetchBranchDiff:
		if m.modal.View().Request != msg.DiffRequest {
			return false
		}
		row, ok := m.selectedRow()
		return ok && branchMatchesDiffError(row, msg)
	case FetchStashDiff:
		if m.modal.View().Request != msg.DiffRequest {
			return false
		}
		stash, ok := m.selectedStash()
		return ok && stashMatchesDiffError(stash, msg)
	case FetchCommitDiff:
		if m.modal.View().Request != msg.DiffRequest {
			return false
		}
		commit, ok := m.selectedCommit()
		return ok && commit.Hash == msg.Hash
	case FetchReflogDiff:
		if m.modal.View().Request != msg.DiffRequest {
			return false
		}
		entry, ok := m.selectedReflog()
		return ok && entry.Hash == msg.Hash
	default:
		return false
	}
}

func branchMatchesDiffError(row gitquery.BranchRow, msg FetchErrorMsg) bool {
	if row.Branch.Name != msg.BranchName {
		return false
	}
	return msg.WorktreePath != "" && row.WorktreePath == msg.WorktreePath
}

func stashMatchesDiffError(stash gitquery.Stash, msg FetchErrorMsg) bool {
	if stash.Index != msg.StashIndex {
		return false
	}
	if msg.StashDate == "" || stash.Date != msg.StashDate {
		return false
	}
	if msg.StashMessage == "" || stash.Message != msg.StashMessage {
		return false
	}
	return true
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
