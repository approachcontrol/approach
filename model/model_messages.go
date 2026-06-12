package model

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
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

type VisibleRepoFetchStatusFadeMsg struct {
	Request uint64
	Text    string
	Step    int
}

type VisibleRepoFetchStatusExpiredMsg struct {
	Request uint64
	Text    string
}

type RepoRefreshResultMsg struct {
	Request uint64
	Repos   []scanner.Repo
}

type RepoRefreshFailedMsg struct {
	Request uint64
	Err     string
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
	Branch       string
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

type SessionResultMsg struct {
	RepoPath    string
	Sessions    []sessions.SessionRecord
	ListRequest uint64
}

type WorktreeSessionResultMsg struct {
	RepoPath     string
	WorktreePath string
	Sessions     []sessions.SessionRecord
	Request      uint64
}

type SessionTranscriptResultMsg struct {
	RepoPath    string
	Provider    sessions.Provider
	SessionID   string
	DiffRequest uint64
	Transcript  string
}

type PlanResultMsg struct {
	RepoPath    string
	Plans       []planstore.PlanRecord
	ListRequest uint64
}

type FlowResultMsg struct {
	RepoPath    string
	Flows       []flowstore.FlowRecord
	ListRequest uint64
}

type PlanReadResultMsg struct {
	RepoPath    string
	PlanID      string
	Mode        ui.Mode
	DiffRequest uint64
	Text        string
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

type PlanEditResultMsg struct {
	RepoPath string
	Err      string
}

type AgentSetMsg struct {
	Command string
}

type AgentSetFailedMsg struct {
	Command string
	Err     string
}

type AgentResultMsg struct {
	LaunchContext actions.AgentLaunchContext
	Err           string
	// Detached reports that the agent was launched into an external
	// terminal/multiplexer session that keeps running after the launch command
	// returns. Detached launches must not finalize the captured session here;
	// provider hooks remain the source of truth for completed session metadata.
	Detached bool
}

type PlanLaunchRequestedMsg struct {
	LaunchContext actions.AgentLaunchContext
	Request       uint64
}

type FlowEmbeddedLaunchRequestedMsg struct {
	LaunchContext actions.AgentLaunchContext
	Request       uint64
}

type FlowTitleSubmittedMsg struct {
	Title string
}

type FlowInstructionsSubmittedMsg struct {
	Title        string
	Instructions string
}

type FlowCreateFailedMsg struct {
	RepoPath string
	Title    string
	Err      string
	Request  uint64
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
	FetchSessionTranscript
	FetchPlanText
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
	Provider     sessions.Provider
	SessionID    string
	PlanID       string
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

func (m Model) acceptListResult(repoPath string, mode ui.Mode, request uint64) (Model, bool) {
	if !m.isCurrentRepo(repoPath) || !m.isCurrentListRequest(mode, request) {
		return m, false
	}
	return m.clearFetchListStatus(mode), true
}

func (m Model) clearAnyStatus() Model {
	m.status = statusError{}
	return m
}

func (m Model) visibleStatusText() string {
	if m.visibleRepoFetch.Request != 0 {
		// In-flight batch fetch progress owns the transient status line until
		// the batch completes; later statuses replace the final batch summary.
		return m.visibleRepoFetchProgressText()
	}
	return m.status.Text
}

func (m Model) visibleStatusFadeStep() int {
	if m.visibleRepoFetch.Request != 0 {
		return 0
	}
	return m.status.FadeStep
}

func (m Model) handleWorktreeResult(msg WorktreeResultMsg) (Model, tea.Cmd) {
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeWorktrees, msg.ListRequest)
	if !ok {
		return m, nil
	}
	inlineRefreshPath, refreshInline := m.pendingInlineSessionRefresh(msg.RepoPath, msg.ListRequest)
	m.worktrees = m.worktrees.SetItems(msg.Worktrees)
	m = m.clearInlineWorktreeSessions()
	if m.pendingWorktreeSelection != "" {
		pendingPath := m.pendingWorktreeSelection
		m.worktrees = m.worktrees.SelectFunc(func(wt gitquery.Worktree) bool {
			return wt.Path == pendingPath
		})
		m.pendingWorktreeSelection = ""
	}
	if refreshInline {
		for _, wt := range m.filteredWorktrees() {
			if wt.Path != inlineRefreshPath {
				continue
			}
			m.worktrees = m.worktrees.SelectFunc(func(wt gitquery.Worktree) bool {
				return wt.Path == inlineRefreshPath
			})
			var request uint64
			m, request = m.nextWorktreeSessionRequest(msg.RepoPath, inlineRefreshPath)
			m = m.clampSelectionsAfterFilter()
			return m, m.fetchWorktreeSessions(inlineRefreshPath, request)
		}
	}
	m = m.clampSelectionsAfterFilter()
	return m, nil
}

func (m Model) handleWorktreeRemoved(msg WorktreeRemovedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if m.WorktreeSelected() >= len(m.Worktrees())-1 && m.WorktreeSelected() > 0 {
		m.worktrees = m.worktrees.Move(-1, m.worktreeContentHeight(), m.contentWidth())
	}
	if msg.BranchName == "" {
		return m.startFetchMode(ui.ModeWorktrees)
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
	return m.startFetchMode(ui.ModeWorktrees)
}

func (m Model) handleWorktreePruned(msg WorktreePrunedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.WorktreeSelected() >= len(m.Worktrees())-1 && m.WorktreeSelected() > 0 {
			m.worktrees = m.worktrees.Move(-1, m.worktreeContentHeight(), m.contentWidth())
		}
		return m.startFetchMode(ui.ModeWorktrees)
	}
	return m, nil
}

func (m Model) handleWorktreeUnlocked(msg WorktreeUnlockedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.clearStatus(statusOther)
		return m.startFetchMode(ui.ModeWorktrees)
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
	m.visibleRepoFetchStatusSeq++
	statusRequest := m.visibleRepoFetchStatusSeq
	m = m.setStatus(statusGitMutation, finalStatus)
	statusCmds := []tea.Cmd{
		fadeVisibleRepoFetchStatus(statusRequest, finalStatus, 1),
		fadeVisibleRepoFetchStatus(statusRequest, finalStatus, 2),
		expireVisibleRepoFetchStatus(statusRequest, finalStatus),
	}
	if currentOK && shouldRefresh {
		var fetchCmd tea.Cmd
		m, fetchCmd = m.startFetchForMode()
		statusCmds = append([]tea.Cmd{fetchCmd}, statusCmds...)
	}
	return m, tea.Batch(statusCmds...)
}

func (m Model) handleVisibleRepoFetchStatusFade(msg VisibleRepoFetchStatusFadeMsg) Model {
	if msg.Request == 0 || msg.Request != m.visibleRepoFetchStatusSeq {
		return m
	}
	if m.status.Source == statusGitMutation && m.status.Text == msg.Text {
		m.status.FadeStep = msg.Step
	}
	return m
}

func (m Model) handleVisibleRepoFetchStatusExpired(msg VisibleRepoFetchStatusExpiredMsg) Model {
	if msg.Request == 0 || msg.Request != m.visibleRepoFetchStatusSeq {
		return m
	}
	if m.status.Source == statusGitMutation && m.status.Text == msg.Text {
		m.status = statusError{}
	}
	return m
}

func (m Model) handleRepoRefreshResult(msg RepoRefreshResultMsg) (tea.Model, tea.Cmd) {
	if msg.Request == 0 || msg.Request != m.activeRepoRefresh {
		return m, nil
	}
	m.activeRepoRefresh = 0

	oldPath, oldOK := m.currentRepoPath()
	var selectedChanged, hasSelection bool
	m, selectedChanged, hasSelection = m.replaceReposPreservingVisibleSelection(msg.Repos, oldPath)
	if !hasSelection {
		m = m.resetRightPaneCursors()
		if len(msg.Repos) == 0 {
			m = m.setStatus(statusOther, "No repositories found")
		} else {
			m = m.setStatus(statusOther, "No repositories match filter")
		}
		return m, nil
	}
	if selectedChanged || !oldOK {
		m = m.resetRightPaneCursors()
		return m.startFetchForMode()
	}
	return m.clearStatus(statusOther), nil
}

func (m Model) handleRepoRefreshFailed(msg RepoRefreshFailedMsg) Model {
	if msg.Request == 0 || msg.Request != m.activeRepoRefresh {
		return m
	}
	m.activeRepoRefresh = 0
	errText := msg.Err
	if errText == "" {
		errText = "unknown error"
	}
	return m.setStatus(statusOther, "failed to refresh repos: "+errText)
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
	m, fetchCmd := m.startFetchMode(ui.ModeWorktrees)
	if !msg.LaunchAgent {
		return m, fetchCmd
	}
	m, launchCmd := m.launchAgentAtPathWithBranch(msg.WorktreePath, &msg.Branch)
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
	return m.startFetchMode(ui.ModeWorktrees)
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
		m.modal = modal.OpenSingleLineInput(
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
	return m.startFetchMode(ui.ModeWorktrees)
}

func (m Model) handleWorktreeMoveFailed(msg WorktreeMoveFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		errText := msg.Err
		if errText == "" {
			errText = "Unable to move worktree"
		}
		oldPath := msg.OldPath
		m.modal = modal.OpenSingleLineInput(
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
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeBranches, msg.ListRequest)
	if !ok {
		return m
	}
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
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeStashes, msg.ListRequest)
	if !ok {
		return m
	}
	m.stashes = m.stashes.SetItems(msg.Stashes)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleStashDiffResult(msg StashDiffResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchStashDiff, ui.ModeStashes, msg.DiffRequest) {
		if stash, ok := m.selectedStash(); ok && stashMatchesDiffResult(stash, msg) {
			return m.pageBody(msg.Diff)
		}
	}
	return m, nil
}

func (m Model) handleWorktreeDiffResult(msg WorktreeDiffResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchWorktreeDiff, ui.ModeWorktrees, msg.DiffRequest) {
		if wt, ok := m.selectedWorktree(); ok && wt.Path == msg.WorktreePath {
			return m.pageBody(msg.Diff)
		}
	}
	return m, nil
}

func (m Model) handleBranchDiffResult(msg BranchDiffResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchBranchDiff, ui.ModeBranches, msg.DiffRequest) {
		if row, ok := m.selectedRow(); ok && branchMatchesDiffResult(row, msg) {
			return m.pageBody(msg.Diff)
		}
	}
	return m, nil
}

func (m Model) handleStashDropped(msg StashDroppedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.StashSelected() >= len(m.Stashes())-1 && m.StashSelected() > 0 {
			m.stashes = m.stashes.Move(-1, m.stashContentHeight(), m.contentWidth())
		}
		return m.startFetchMode(ui.ModeStashes)
	}
	return m, nil
}

func (m Model) handleBranchDeleted(msg BranchDeletedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		return m.startFetchMode(ui.ModeBranches)
	}
	return m, nil
}

func (m Model) handleBranchCreated(msg BranchCreatedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m.mode = ui.ModeBranches
		m.rows = m.rows.SetQuery("")
		m.pendingBranchSelection = msg.Name
		return m.startFetchMode(ui.ModeBranches)
	}
	return m, nil
}

func (m Model) handleBranchCreateFailed(msg BranchCreateFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		errText := msg.Err
		if msg.Err == "" {
			errText = "Unable to create branch"
		}
		m.modal = modal.OpenSingleLineInput(
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
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeHistory, msg.ListRequest)
	if !ok {
		return m
	}
	m.commits = m.commits.SetItems(msg.Commits)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleReflogResult(msg ReflogResultMsg) Model {
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeReflog, msg.ListRequest)
	if !ok {
		return m
	}
	m.reflogs = m.reflogs.SetItems(msg.Reflogs)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleSessionResult(msg SessionResultMsg) Model {
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeSessions, msg.ListRequest)
	if !ok {
		return m
	}
	m.sessions = m.sessions.SetItems(msg.Sessions)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleWorktreeSessionResult(msg WorktreeSessionResultMsg) Model {
	if !m.isCurrentWorktreeSessionRequest(msg) {
		return m
	}
	m.worktreeSessions = m.worktreeSessions.SetItems(msg.Sessions)
	m = m.clearFetchListStatus(ui.ModeWorktrees)
	m = m.reflowWorktreeSessions()
	return m
}

func (m Model) handlePlanResult(msg PlanResultMsg) Model {
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModePlans, msg.ListRequest)
	if !ok {
		return m
	}
	selectedPlanID := m.selectedPlanID()
	m.plans = m.plans.SetItems(msg.Plans)
	if selectedPlanID != "" {
		m.plans = m.plans.SelectFunc(func(record planstore.PlanRecord) bool {
			return record.PlanID == selectedPlanID
		})
	}
	m = m.setExpandedPlanID("")
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) handleFlowResult(msg FlowResultMsg) Model {
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeFlows, msg.ListRequest)
	if !ok {
		return m
	}
	selectedFlowID := m.selectedFlowID()
	expandedFlowID := m.expandedFlowID
	selectedFlowPhaseID := m.selectedFlowPhaseID
	m.flows = m.flows.SetItems(msg.Flows)
	if selectedFlowID != "" {
		m.flows = m.flows.SelectFunc(func(record flowstore.FlowRecord) bool {
			return record.FlowID == selectedFlowID
		})
	}
	m = m.restoreExpandedFlowSelection(expandedFlowID, selectedFlowPhaseID)
	m = m.clampSelectionsAfterFilter()
	return m
}

func (m Model) restoreExpandedFlowSelection(flowID, phaseID string) Model {
	if flowID == "" {
		return m.setExpandedFlowID("")
	}
	record, ok := m.selectedFlow()
	if !ok || record.FlowID != flowID {
		return m.setExpandedFlowID("")
	}
	if phaseID != "" && !flowRecordHasPhase(record, phaseID) {
		return m.setExpandedFlowID("")
	}
	m.expandedFlowID = flowID
	m.selectedFlowPhaseID = phaseID
	m.flows = m.flows.SetItemHeight(flowItemHeight(flowID))
	return m.reflowFlows()
}

func flowRecordHasPhase(record flowstore.FlowRecord, phaseID string) bool {
	for _, phase := range flowstore.OrderedPhases(record.Phases) {
		if phase.PhaseID == phaseID {
			return true
		}
	}
	return false
}

func (m Model) handleSessionTranscriptResult(msg SessionTranscriptResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchSessionTranscript, ui.ModeSessions, msg.DiffRequest) {
		if record, ok := m.selectedSession(); ok && record.Provider == msg.Provider && record.SessionID == msg.SessionID {
			return m.pageBody(msg.Transcript)
		}
	}
	return m, nil
}

func (m Model) handlePlanReadResult(msg PlanReadResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchPlanText, msg.Mode, msg.DiffRequest) {
		if m.currentPlanTextTargetMatches(msg.Mode, msg.PlanID) {
			return m.pageBody(msg.Text)
		}
	}
	return m, nil
}

func (m Model) handleCommitDiffResult(msg CommitDiffResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchCommitDiff, ui.ModeHistory, msg.DiffRequest) {
		if commit, ok := m.selectedCommit(); ok && commit.Hash == msg.Hash {
			return m.pageBody(msg.Diff)
		}
	}
	return m, nil
}

func (m Model) handleReflogDiffResult(msg ReflogDiffResultMsg) (Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) && m.activeViewMatches(FetchReflogDiff, ui.ModeReflog, msg.DiffRequest) {
		if entry, ok := m.selectedReflog(); ok && entry.Hash == msg.Hash {
			body := msg.Diff
			if body == "" {
				body = "No changes at this reflog entry"
			}
			return m.pageBody(body)
		}
	}
	return m, nil
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
		if msg.Pane == "worktree sessions" {
			return msg.Mode == ui.ModeWorktrees &&
				msg.ListRequest == m.activeWorktreeSessionReq &&
				msg.WorktreePath == m.inlineWorktreeSessionPath
		}
		return msg.Mode == m.mode && m.isCurrentListRequest(msg.Mode, msg.ListRequest)
	case FetchWorktreeDiff:
		if !m.activeViewMatches(FetchWorktreeDiff, ui.ModeWorktrees, msg.DiffRequest) {
			return false
		}
		wt, ok := m.selectedWorktree()
		return ok && wt.Path == msg.WorktreePath
	case FetchBranchDiff:
		if !m.activeViewMatches(FetchBranchDiff, ui.ModeBranches, msg.DiffRequest) {
			return false
		}
		row, ok := m.selectedRow()
		return ok && branchMatchesDiffError(row, msg)
	case FetchStashDiff:
		if !m.activeViewMatches(FetchStashDiff, ui.ModeStashes, msg.DiffRequest) {
			return false
		}
		stash, ok := m.selectedStash()
		return ok && stashMatchesDiffError(stash, msg)
	case FetchCommitDiff:
		if !m.activeViewMatches(FetchCommitDiff, ui.ModeHistory, msg.DiffRequest) {
			return false
		}
		commit, ok := m.selectedCommit()
		return ok && commit.Hash == msg.Hash
	case FetchReflogDiff:
		if !m.activeViewMatches(FetchReflogDiff, ui.ModeReflog, msg.DiffRequest) {
			return false
		}
		entry, ok := m.selectedReflog()
		return ok && entry.Hash == msg.Hash
	case FetchSessionTranscript:
		if !m.activeViewMatches(FetchSessionTranscript, ui.ModeSessions, msg.DiffRequest) {
			return false
		}
		record, ok := m.selectedSession()
		return ok && record.Provider == msg.Provider && record.SessionID == msg.SessionID
	case FetchPlanText:
		if !m.activeViewMatches(FetchPlanText, msg.Mode, msg.DiffRequest) {
			return false
		}
		return m.currentPlanTextTargetMatches(msg.Mode, msg.PlanID)
	default:
		return false
	}
}

func (m Model) currentPlanTextTargetMatches(mode ui.Mode, planID string) bool {
	switch mode {
	case ui.ModePlans:
		record, ok := m.selectedPlan()
		return ok && record.PlanID == planID
	case ui.ModeFlows:
		record, ok := m.selectedFlow()
		return ok && record.PlanID == planID
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
		if err := m.copyToClipboard(hash); err != nil {
			return ClipboardResultMsg{Err: err.Error()}
		}
		return ClipboardResultMsg{}
	}
}

func (m Model) handleCopySessionID() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeSessions {
		return m, nil
	}
	record, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	sessionID := record.SessionID
	return m, func() tea.Msg {
		if err := m.copyToClipboard(sessionID); err != nil {
			return ClipboardResultMsg{Err: err.Error()}
		}
		return ClipboardResultMsg{}
	}
}

func (m Model) handleCopyPlanPath() (tea.Model, tea.Cmd) {
	plan, ok := m.selectedPlan()
	if !ok {
		return m, nil
	}
	planPath, err := m.planMarkdownPath(plan.PlanID)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	return m, func() tea.Msg {
		if err := m.copyToClipboard(planPath); err != nil {
			return ClipboardResultMsg{Err: err.Error()}
		}
		return ClipboardResultMsg{}
	}
}

func (m Model) handleCopyFlowID() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeFlows {
		return m, nil
	}
	value := ""
	if phase, ok := m.selectedFlowPhase(); ok {
		value = phase.PhaseID
	} else {
		flow, ok := m.selectedFlow()
		if !ok {
			return m, nil
		}
		value = flow.FlowID
	}
	if strings.TrimSpace(value) == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		if err := m.copyToClipboard(value); err != nil {
			return ClipboardResultMsg{Err: err.Error()}
		}
		return ClipboardResultMsg{}
	}
}

func (m Model) handleShowSessionSummary() (tea.Model, tea.Cmd) {
	if m.mode != ui.ModeSessions {
		return m, nil
	}
	record, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	summary := record.Summary
	if strings.TrimSpace(summary) == "" {
		summary = "No summary"
	}
	m = m.invalidateViewRequest()
	return m.pageBody(summary)
}
