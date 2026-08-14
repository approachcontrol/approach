package model

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
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

type promptTemplateEditRequestedMsg struct {
	Value string
}

type PromptTemplateSavedMsg struct {
	Section string
	Key     string
	Value   string
}

type PromptTemplateSaveFailedMsg struct {
	Section string
	Key     string
	Value   string
	Err     string
}

type PromptTemplateResetMsg struct {
	Section string
	Key     string
}

type PromptTemplateResetFailedMsg struct {
	Section string
	Key     string
	Err     string
}

type RepoRefreshResultMsg struct {
	Request uint64
	Repos   []scanner.Repo
}

type RepoRefreshFailedMsg struct {
	Request uint64
	Err     string
}

type RepoCreatedMsg struct {
	Name    string
	Result  actions.RepoCreateResult
	Request uint64
}

type RepoCreateFailedMsg struct {
	Input        string
	CreateGitHub bool
	Visibility   actions.RepoVisibility
	Result       actions.RepoCreateResult
	Err          string
	Request      uint64
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
	Degradation *flowstore.PartialListError
	ListRequest uint64
}

type ActiveFlowResultMsg struct {
	Flows       []flowstore.FlowRecord
	Degradation *flowstore.PartialListError
	ListRequest uint64
}

type BeadsReadyResultMsg struct {
	RepoPath    string
	Beads       []beadsquery.Bead
	ListRequest uint64
	Available   bool
	Error       string
}

type BeadsBlockedResultMsg struct {
	RepoPath    string
	Beads       []beadsquery.Bead
	ListRequest uint64
	Available   bool
	Error       string
}

type BeadsOpenResultMsg struct {
	RepoPath    string
	Beads       []beadsquery.Bead
	ListRequest uint64
	Available   bool
	Error       string
}

type BeadsInProgressResultMsg struct {
	RepoPath    string
	Beads       []beadsquery.Bead
	ListRequest uint64
	Available   bool
	Error       string
}

type BeadsClosedResultMsg struct {
	RepoPath    string
	Beads       []beadsquery.Bead
	Total       int
	ListRequest uint64
	Available   bool
	Error       string
}

type BeadDetailResultMsg struct {
	RepoPath    string
	Mode        ui.Mode
	BeadID      string
	DiffRequest uint64
	Body        string
}

type FlowAutoModeSetMsg struct {
	RepoPath string
	FlowID   string
	Flow     flowstore.FlowRecord
	Enabled  bool
}

type FlowAutoModeSetFailedMsg struct {
	RepoPath string
	FlowID   string
	Err      string
}

type FlowHeadlessSetMsg struct {
	RepoPath        string
	FlowID          string
	Flow            flowstore.FlowRecord
	Enabled         bool
	AllRepositories bool
}

type FlowHeadlessSetFailedMsg struct {
	RepoPath        string
	FlowID          string
	Enabled         bool
	Err             string
	AllRepositories bool
}

type FlowManualMergeSetMsg struct {
	RepoPath string
	FlowID   string
	Flow     flowstore.FlowRecord
}

type FlowManualMergeSetFailedMsg struct {
	RepoPath string
	FlowID   string
	Flow     flowstore.FlowRecord
	Err      string
}

type FlowClosedMsg struct {
	RepoPath string
	FlowID   string
	Flow     flowstore.FlowRecord
}

type FlowCloseFailedMsg struct {
	RepoPath string
	FlowID   string
	Err      string
}

type FlowReopenedMsg struct {
	RepoPath string
	FlowID   string
	Flow     flowstore.FlowRecord
}

type FlowReopenFailedMsg struct {
	RepoPath string
	FlowID   string
	Err      string
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

type OpenURLResultMsg struct {
	Label string
	Err   string
}

type TerminalResultMsg struct {
	Err string
}

type EmbeddedTerminalDetachHandoffResultMsg struct {
	Target string
	Err    string
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

type AgentModelSetMsg struct {
	Command string
	Model   string
}

type AgentModelSetFailedMsg struct {
	Command string
	Model   string
	Err     string
}

type AgentReasoningEffortSetMsg struct {
	Command string
	Effort  string
}

type AgentReasoningEffortSetFailedMsg struct {
	Command string
	Effort  string
	Err     string
}

type FlowPhaseAgentSettingsSetMsg struct {
	Flow          flowstore.FlowRecord
	PhaseID       string
	PhaseIdentity string
}

type FlowPhaseAgentSettingsSetFailedMsg struct {
	FlowID  string
	PhaseID string
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
	// LaunchedStatus replaces the generic "Launched <agent> in a terminal
	// session" text for transports that can say something more useful, such as
	// tmux mode naming the session and its attach command. Empty keeps the
	// generic text.
	LaunchedStatus string
}

type agentSessionFinalizedMsg struct {
	Result AgentResultMsg
	Err    error
}

type flowLaunchFailurePersistedMsg struct {
	LaunchContext             actions.AgentLaunchContext
	OriginalErr               string
	PersistErr                error
	Release                   func()
	Create                    *flowLaunchCreateRequest
	releaseReadyBeadAdmission bool
}

type agentLaunchRequestedMsg struct {
	LaunchContext actions.AgentLaunchContext
}

type FlowCreatedMsg struct {
	RepoPath string
	FlowID   string
	Title    string
	Request  uint64
}

type FlowCreateFailedMsg struct {
	RepoPath string
	FlowID   string
	Title    string
	Err      string
	Request  uint64
}

type ReadyBeadFlowCreatedMsg struct {
	RepoPath string
	FlowID   string
	Title    string
	Request  uint64
}

type ReadyBeadFlowCreateFailedMsg struct {
	RepoPath string
	FlowID   string
	Title    string
	Err      string
	Request  uint64
}

type FlowDeletedMsg struct {
	RepoPath string
	FlowID   string
	Title    string
}

type FlowDeleteFailedMsg struct {
	RepoPath string
	FlowID   string
	Title    string
	Err      string
	NotFound bool
}

type flowPhaseResetConfirmedMsg struct {
	RepoPath string
	FlowID   string
	PhaseID  string
}

type flowPhaseResetMsg struct {
	RepoPath string
	FlowID   string
	PhaseID  string
	Flow     flowstore.FlowRecord
}

type flowPhaseResetFailedMsg struct {
	RepoPath string
	FlowID   string
	PhaseID  string
	Err      string
}

// flowPhaseSessionReleaseProbedMsg carries the authoritative both-halves answer
// back to the key press. Err is not optional: ListFlowSessions can fail, and
// without it a failed probe would be indistinguishable from a phase with
// nothing to release.
type flowPhaseSessionReleaseProbedMsg struct {
	RepoPath  string
	FlowID    string
	PhaseID   string
	LaunchIDs []string
	Err       string
}

// flowPhaseSessionReleaseConfirmedMsg carries the probed launch IDs across the
// confirmation hop, which is what lets the release intersect rather than
// recompute.
type flowPhaseSessionReleaseConfirmedMsg struct {
	RepoPath  string
	FlowID    string
	PhaseID   string
	LaunchIDs []string
}

type flowPhaseSessionReleasedMsg struct {
	RepoPath string
	FlowID   string
	PhaseID  string
	Released int
}

// flowPhaseSessionReleaseFailedMsg carries Released for the same reason the
// success message does: finalization runs once per launch, so a failure on the
// second of two leaves the first genuinely ended, and reporting only the error
// would send the user to retry a phase that has already moved.
//
// Finalized covers what Released cannot. One finalize call is itself two writes
// — the session store, then the phase mirror — so a failure on the second one
// changed persisted state without advancing Released. Anything that entered
// finalization is therefore treated as having changed the phase; only a failure
// before the first call is provably inert.
type flowPhaseSessionReleaseFailedMsg struct {
	RepoPath  string
	FlowID    string
	PhaseID   string
	Released  int
	Finalized bool
	Err       string
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
	FetchBeadDetail
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
	BeadID       string
}

// ActionFailedMsg carries an async action error so the failure can be surfaced
// via the transient error line.
type ActionFailedMsg struct {
	RepoPath                string
	Err                     string
	AutoAdvanceRetryFlowID  string
	AutoAdvanceRetryPhaseID string
	AutoAdvanceLaunchID     string
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
	m = m.setCurrentListError(mode, "")
	return m.clearFetchListStatus(mode), true
}

func (m Model) acceptActiveFlowResult(request uint64) (Model, bool) {
	if !m.activeFlowSurfaceVisible() || !m.isCurrentListRequest(ui.ModeActiveFlows, request) {
		return m, false
	}
	m = m.setCurrentListError(ui.ModeActiveFlows, "")
	return m.clearFetchListStatus(ui.ModeActiveFlows), true
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
		return m.startFetchMode(m.topMode)
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
	m = m.setVisibleRepoFetchSummaryStatus(finalStatus)
	if currentOK && shouldRefresh {
		var fetchCmd tea.Cmd
		m, fetchCmd = m.startFetchMode(m.topMode)
		return m, fetchCmd
	}
	return m, nil
}

func (m Model) handleRepoRefreshResult(msg RepoRefreshResultMsg) (tea.Model, tea.Cmd) {
	if msg.Request == 0 || msg.Request != m.activeRepoRefresh {
		return m, nil
	}
	m.activeRepoRefresh = 0

	if m.pendingRepoSelection != "" {
		pendingPath := m.pendingRepoSelection
		m.repos = m.repos.SetQuery("").SetItems(msg.Repos)
		for _, repo := range m.filteredRepos() {
			if !sameRepoPath(repo.Path, pendingPath) {
				continue
			}
			m.repos = m.repos.SelectFunc(func(repo scanner.Repo) bool {
				return sameRepoPath(repo.Path, pendingPath)
			})
			m.pendingRepoSelection = ""
			m = m.reflowRepos()
			m = m.resetStoredPanesForRepoRefresh()
			return m.startStoredModeFetches()
		}
		m.pendingRepoSelection = ""
	}

	oldPath, oldOK := m.currentRepoPath()
	var selectedChanged, hasSelection bool
	m, selectedChanged, hasSelection = m.replaceReposPreservingVisibleSelection(msg.Repos, oldPath)
	if !hasSelection {
		m = m.resetStoredPanesForRepoRefresh()
		if len(msg.Repos) == 0 {
			m = m.setStatus(statusOther, "No repositories found")
		} else {
			m = m.setStatus(statusOther, "No repositories match filter")
		}
		return m, nil
	}
	if selectedChanged || !oldOK {
		m = m.resetStoredPanesForRepoRefresh()
		return m.startStoredModeFetches()
	}
	return m.clearStatus(statusOther), nil
}

func (m Model) resetStoredPanesForRepoRefresh() Model {
	if !m.activeFlowSurfaceVisible() {
		return m.resetRightPaneCursors()
	}
	m = m.resetStoredPaneCursors()
	return m.syncActiveFlowsFromCache()
}

func sameRepoPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(absA) == filepath.Clean(absB)
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

func (m Model) handleRepoCreated(msg RepoCreatedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepoCreateRequest(msg.Request) {
		return m, nil
	}
	m = m.clearRepoCreateRequest(msg.Request)
	destination := msg.Result.DestinationPath
	if destination != "" {
		m.pendingRepoSelection = destination
	}
	name := strings.TrimSpace(msg.Name)
	if name == "" && destination != "" {
		name = filepath.Base(destination)
	}
	if name == "" {
		name = "repo"
	}
	m = m.setStatus(statusOther, "Created repo "+name)
	return m.startGlobalRefresh()
}

func (m Model) handleRepoCreateFailed(msg RepoCreateFailedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepoCreateRequest(msg.Request) {
		return m, nil
	}
	m = m.clearRepoCreateRequest(msg.Request)
	errText := msg.Err
	if errText == "" {
		errText = "Unable to create repo"
	}
	retryPath := ""
	if msg.Result.PartialSuccess && msg.Result.RetryAllowed {
		retryPath = msg.Result.ExistingLocalPath
		if retryPath == "" {
			retryPath = msg.Result.DestinationPath
		}
		if retryPath != "" {
			m.pendingRepoSelection = retryPath
		}
		errText = "Local repo created; GitHub/origin setup failed: " + errText
	}
	m.modal = m.repoCreateForm(msg.Input, msg.CreateGitHub, msg.Visibility, retryPath, errText)
	if retryPath != "" {
		return m.startGlobalRefresh()
	}
	return m, nil
}

func (m Model) handleGitPulled(msg GitPulledMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		m = m.clearStatus(statusGitMutation)
		return m.startFetchMode(m.topMode)
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
	m.activeFlowSurface = false
	m, _ = m.selectStoredMode(ui.ModeWorktrees)
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
	m.activeFlowSurface = false
	m, _ = m.selectStoredMode(ui.ModeWorktrees)
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

func (m Model) handleAgentReasoningEffortSet(msg AgentReasoningEffortSetMsg) Model {
	m = m.withReasoningEffort(msg.Command, msg.Effort)
	m = m.clearStatus(statusOther)
	return m
}

func (m Model) handleAgentModelSet(msg AgentModelSetMsg) Model {
	m = m.withModel(msg.Command, msg.Model)
	m = m.clearStatus(statusOther)
	return m
}

func (m Model) handleFlowPhaseAgentSettingsSet(msg FlowPhaseAgentSettingsSetMsg) Model {
	if msg.Flow.FlowID == "" {
		return m.setStatus(statusOther, "Unable to update Flow phase agent settings")
	}
	record, phase, ok := m.selectedFlowPhaseAgentTarget()
	if !ok || record.FlowID != msg.Flow.FlowID || phase.PhaseID != msg.PhaseID ||
		artifacts.NormalizePhaseID(phase.PhaseID) != msg.PhaseIdentity {
		return m.setStatus(statusOther, "Flow phase selection changed before agent settings were applied")
	}
	updatedPhaseIndex := flowPhaseStoredIndexByID(msg.Flow.Phases, msg.PhaseID)
	if updatedPhaseIndex < 0 {
		return m.setStatus(statusOther, "Unable to update Flow phase agent settings")
	}
	updatedPhase := msg.Flow.Phases[updatedPhaseIndex]
	m = m.replaceFlowRecord(
		msg.Flow,
		flowPhaseAgentSettingsMutationField(msg.PhaseID),
		flowPhaseAgentSettingsOverlay(
			msg.PhaseID,
			updatedPhase.AgentSettings(),
			msg.Flow.UpdatedAt,
			updatedPhase.UpdatedAt,
		),
	)
	return m.setStatus(statusOther, fmt.Sprintf("Updated agent settings for Flow phase %s", msg.PhaseID))
}

func (m Model) handleFlowPhaseAgentSettingsSetFailed(msg FlowPhaseAgentSettingsSetFailedMsg) Model {
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "Unable to update Flow phase agent settings"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) handleAgentModelSetFailed(msg AgentModelSetFailedMsg) Model {
	// Keep the selection usable for this session even when persistence fails.
	m = m.withModel(msg.Command, msg.Model)
	errText := msg.Err
	if errText == "" {
		errText = "Unable to persist model"
	}
	m = m.setStatus(statusOther, errText)
	return m
}

func (m Model) handleAgentReasoningEffortSetFailed(msg AgentReasoningEffortSetFailedMsg) Model {
	// Keep the selection usable for this session even when persistence fails.
	m = m.withReasoningEffort(msg.Command, msg.Effort)
	errText := msg.Err
	if errText == "" {
		errText = "Unable to persist reasoning effort"
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
		m.activeFlowSurface = false
		m, _ = m.selectStoredMode(ui.ModeBranches)
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
	if m.activeFlowSurfaceVisible() && msg.Kind == FetchList && msg.Mode == ui.ModeActiveFlows && msg.Pane == "active-flows" {
		if next, ok := m.acceptActiveFlowResult(msg.ListRequest); ok {
			next = next.setCurrentListError(ui.ModeActiveFlows, msg.Err)
			return next.setFetchStatus(msg)
		}
		return m
	}
	if !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	if !m.fetchErrorMatchesCurrentTarget(msg) {
		return m
	}
	if msg.Kind == FetchList && msg.Pane != "worktree sessions" {
		m = m.setCurrentListError(msg.Mode, msg.Err)
	}
	m = m.setFetchStatus(msg)
	return m
}

func (m Model) handleActionFailed(msg ActionFailedMsg) (Model, tea.Cmd) {
	if msg.AutoAdvanceLaunchID != "" {
		attempt, ok := m.matchingFlowLaunchAttempt(msg.AutoAdvanceRetryFlowID, msg.AutoAdvanceLaunchID, flowLaunchKindAutoPhase, flowLaunchStatePreparing)
		if !ok {
			return m, nil
		}
		m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
		if msg.Err == "" || attempt.AutoRetrySuppressed {
			return m, nil
		}
	}
	autoAdvanceRetry := msg.AutoAdvanceRetryFlowID != "" && msg.AutoAdvanceRetryPhaseID != ""
	autoAdvanceFailure := autoAdvanceRetry
	if msg.AutoAdvanceRetryFlowID != "" {
		// Token and stop-edge validation above keep a delayed failure from
		// undoing newer drain state.
		m = m.armAutoAdvanceDrain(msg.AutoAdvanceRetryFlowID)
	}
	if m.activeFlowSurfaceVisible() || m.isCurrentRepo(msg.RepoPath) {
		m = m.setStatus(statusOther, msg.Err)
		return m, nil
	}
	if !autoAdvanceFailure {
		return m, nil
	}
	title := ""
	if strings.TrimSpace(title) == "" {
		title = msg.AutoAdvanceRetryFlowID
	}
	// The prepare-stage sibling of the read-stage failure in
	// handleAutoFlowLaunchRead, and ranked with it: this path re-armed the drain
	// above, so the failure repeats on the next poll and must not displace a
	// transition edge that will not.
	var statusCmd tea.Cmd
	m, statusCmd = m.setAutoAdvanceLaunchStatus("Flow " + title + ": " + msg.Err)
	return m, statusCmd
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

func (m Model) handleBeadsOpenResult(msg BeadsOpenResultMsg) (Model, bool) {
	return m.handleBeadsResult(ui.ModeBeadsOpen, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error)
}

func (m Model) handleBeadsResult(mode ui.Mode, repoPath string, beads []beadsquery.Bead, request uint64, available bool, errorDetail string) (Model, bool) {
	return m.handleBeadsResultWithTotal(mode, repoPath, beads, request, available, errorDetail, 0)
}

func (m Model) handleBeadsClosedResult(msg BeadsClosedResultMsg) (Model, bool) {
	return m.handleBeadsResultWithTotal(ui.ModeBeadsClosed, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error, msg.Total)
}

func (m Model) handleBeadsResultWithTotal(mode ui.Mode, repoPath string, beads []beadsquery.Bead, request uint64, available bool, errorDetail string, total int) (Model, bool) {
	if !m.modeStored(mode) {
		return m, false
	}
	var ok bool
	m, ok = m.acceptListResult(repoPath, mode, request)
	if !ok {
		return m, false
	}
	index, ok := beadSubviewIndex(mode)
	if !ok {
		return m, false
	}
	if !available || errorDetail != "" {
		beads = nil
		total = 0
	}
	m.beads[index].pane = m.beads[index].pane.SetItems(beads)
	m.beads[index].available = available && errorDetail == ""
	m.beads[index].pending = false
	m.beads[index].error = errorDetail
	m.beads[index].total = total
	m.beads[index].repoPath = repoPath
	return m.reflowBeads(mode), true
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

func (m Model) handleFlowResult(msg FlowResultMsg) (Model, tea.Cmd) {
	var ok bool
	m, ok = m.acceptListResult(msg.RepoPath, ui.ModeFlows, msg.ListRequest)
	if !ok {
		return m, nil
	}
	m = m.setFlowDegradation(ui.ModeFlows, msg.RepoPath, msg.Degradation)
	flows := preferNewerCachedFlowRecords(msg.Flows, msg.ListRequest, m.latestFlowMutations)
	m = m.pruneAcknowledgedFlowMutations()
	if msg.Degradation == nil {
		m = m.seedAutoAdvanceSnapshot(flows)
	}
	selectedFlowID := ""
	if record, ok := m.flows.Selected(); ok {
		selectedFlowID = record.FlowID
	}
	expandedFlowID := m.expandedFlowID
	selectedFlowPhaseID := m.selectedFlowPhaseID
	m.flows = m.flows.SetItems(flows)
	if selectedFlowID != "" {
		m.flows = m.flows.SelectFunc(func(record flowstore.FlowRecord) bool {
			return record.FlowID == selectedFlowID
		})
	}
	m = m.restoreExpandedFlowSelection(expandedFlowID, selectedFlowPhaseID)
	m = m.syncActiveFlowsFromCache()
	m = m.clampSelectionsAfterFilter()
	if m.focusedMode() == ui.ModeFlows && m.terminalFocus != terminalFocusTerminal {
		m = m.syncActiveFlowTerminalToSelectedFlow()
	}
	return m, nil
}

func (m Model) handleActiveFlowResult(msg ActiveFlowResultMsg) (Model, tea.Cmd) {
	var ok bool
	m, ok = m.acceptActiveFlowResult(msg.ListRequest)
	if !ok {
		return m, nil
	}
	m = m.setFlowDegradation(ui.ModeActiveFlows, "", msg.Degradation)
	flows := preferNewerCachedFlowRecords(msg.Flows, msg.ListRequest, m.latestFlowMutations)
	m = m.pruneAcknowledgedFlowMutations()
	if msg.Degradation == nil {
		m = m.seedAutoAdvanceSnapshot(flows)
	}
	m.activeFlowRecords = append([]flowstore.FlowRecord(nil), flows...)
	m = m.syncActiveFlowsFromCache()
	m = m.clampSelectionsAfterFilter()
	if m.terminalFocus != terminalFocusTerminal {
		m = m.syncActiveFlowTerminalToSelectedFlow()
	}
	return m, nil
}

func (m Model) handleFlowAutoModeSet(msg FlowAutoModeSetMsg) Model {
	if msg.FlowID == "" || (!m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath)) {
		return m
	}
	return m.replaceFlowRecord(msg.Flow, flowMutationAutoMode, flowAutoModeOverlay(msg.Enabled))
}

func (m Model) handleFlowAutoModeSetFailed(msg FlowAutoModeSetFailedMsg) Model {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to set Flow auto mode"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) handleFlowHeadlessSet(msg FlowHeadlessSetMsg) (Model, tea.Cmd) {
	// Toggles queued behind this write may have been entered from a different
	// surface. Honour the latest intent's scope so a result that satisfies it is
	// not rejected as off-repo, including when coalescing needs no follow-up.
	if queued, ok := m.queuedFlowHeadlessValue(msg.FlowID); ok {
		msg.AllRepositories = msg.AllRepositories || queued.allRepositories
	}
	m, followUp := m.resolveFlowHeadlessWrite(msg)
	if msg.FlowID == "" || msg.Flow.FlowID != msg.FlowID || msg.Flow.Headless != msg.Enabled {
		return m, followUp
	}
	if !msg.AllRepositories && !m.isCurrentRepo(msg.RepoPath) {
		return m, followUp
	}
	if !sameRepoPath(msg.Flow.RepoPath, msg.RepoPath) {
		return m, followUp
	}
	return m.replaceFlowRecord(msg.Flow, flowMutationHeadless, flowHeadlessOverlay(msg.Flow.Headless)), followUp
}

func (m Model) handleFlowHeadlessSetFailed(msg FlowHeadlessSetFailedMsg) Model {
	// The write left the stored preference untouched, so the toggles queued
	// behind it are unreachable. Retrying here could ping-pong against a
	// persistent failure, so report the dropped intent instead of hiding it.
	queued, dropped := m.queuedFlowHeadlessValue(msg.FlowID)
	dropped = dropped && queued.enabled == msg.Enabled
	m = m.clearFlowHeadlessWritePending(msg.FlowID)
	// The queued intent may have been entered from a different surface, so honour
	// its scope rather than the failed write's when deciding to report.
	if dropped {
		msg.AllRepositories = msg.AllRepositories || queued.allRepositories
	}
	if !msg.AllRepositories && !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to set Flow headless mode"
	}
	if dropped {
		errText += "; headless mode is unchanged, press h again to retry"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) handleFlowManualMergeSet(msg FlowManualMergeSetMsg) Model {
	if msg.FlowID == "" || (!m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath)) {
		return m
	}
	return m.replaceFlowRecord(msg.Flow, flowMutationWholeRecord, nil)
}

func (m Model) handleFlowManualMergeSetFailed(msg FlowManualMergeSetFailedMsg) Model {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to mark Flow as merged"
	}
	if msg.Flow.FlowID != "" {
		m = m.replaceFlowRecord(msg.Flow, flowMutationWholeRecord, nil)
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) handleFlowClosed(msg FlowClosedMsg) Model {
	if msg.FlowID == "" || (!m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath)) {
		return m
	}
	// An unscoped poll disarms a drain on a closed Flow only after occupancy
	// clears, so a close taken while a terminal is running would otherwise
	// leave the drain armed and let a reopen launch the successor. Disarming
	// here makes the close, not poll timing, decide.
	m = m.disarmAutoAdvanceDrain(msg.FlowID)
	m = m.withoutRepairAutoDrainMarker(msg.FlowID)
	return m.replaceFlowRecord(msg.Flow, flowMutationWholeRecord, nil)
}

func (m Model) handleFlowCloseFailed(msg FlowCloseFailedMsg) Model {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to close Flow"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) handleFlowReopened(msg FlowReopenedMsg) Model {
	if msg.FlowID == "" || (!m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath)) {
		return m
	}
	return m.replaceFlowRecord(msg.Flow, flowMutationWholeRecord, nil)
}

func (m Model) handleFlowReopenFailed(msg FlowReopenFailedMsg) Model {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to reopen Flow"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) replaceFlowRecord(flow flowstore.FlowRecord, field flowMutationField, apply func(flowstore.FlowRecord) flowstore.FlowRecord) Model {
	if flow.FlowID == "" {
		return m
	}
	if field.overlayOnly() && m.flowMutationSuperseded(flow, field) {
		return m
	}
	m = m.rememberFlowMutation(flow, field, apply)
	selectedFlowID := ""
	if record, ok := m.flows.Selected(); ok {
		selectedFlowID = record.FlowID
	}
	expandedFlowID := m.expandedFlowID
	selectedFlowPhaseID := m.selectedFlowPhaseID
	overlayOnly := field.overlayOnly() && apply != nil
	// An overlay-only write result is authoritative only for its named field.
	// Copying unrelated fields from it could regress a peer write that landed
	// after the store returned; the regular refresh surfaces unrelated changes.
	items := append([]flowstore.FlowRecord(nil), m.flows.Items()...)
	replacedFlows := false
	for i := range items {
		if items[i].FlowID != flow.FlowID {
			continue
		}
		if overlayOnly {
			items[i] = apply(items[i])
		} else {
			if flow.UpdatedAt.Before(items[i].UpdatedAt) {
				continue
			}
			items[i] = flow
		}
		replacedFlows = true
		break
	}
	if replacedFlows {
		m.flows = m.flows.SetItems(items)
	}
	if replacedFlows && selectedFlowID != "" {
		m.flows = m.flows.SelectFunc(func(record flowstore.FlowRecord) bool {
			return record.FlowID == selectedFlowID
		})
	}
	if replacedFlows {
		m = m.restoreExpandedFlowSelection(expandedFlowID, selectedFlowPhaseID)
	}
	activeRecords := append([]flowstore.FlowRecord(nil), m.activeFlowRecords...)
	replacedActive := false
	for i := range activeRecords {
		if activeRecords[i].FlowID != flow.FlowID {
			continue
		}
		if overlayOnly {
			activeRecords[i] = apply(activeRecords[i])
		} else {
			if flow.UpdatedAt.Before(activeRecords[i].UpdatedAt) {
				continue
			}
			activeRecords[i] = flow
		}
		replacedActive = true
		break
	}
	if replacedActive {
		m.activeFlowRecords = activeRecords
	}
	if !replacedFlows && !replacedActive {
		return m
	}
	m = m.syncActiveFlowsFromCache()
	return m.clampSelectionsAfterFilter()
}

func (m Model) flowMutationSuperseded(flow flowstore.FlowRecord, field flowMutationField) bool {
	for _, cached := range m.latestFlowMutations {
		if cached.field == field &&
			cached.record.FlowID == flow.FlowID &&
			sameRepoPath(cached.record.RepoPath, flow.RepoPath) &&
			flow.UpdatedAt.Before(cached.record.UpdatedAt) {
			return true
		}
	}
	for _, records := range [][]flowstore.FlowRecord{m.flows.Items(), m.activeFlowRecords} {
		for _, record := range records {
			if record.FlowID == flow.FlowID &&
				sameRepoPath(record.RepoPath, flow.RepoPath) &&
				flow.UpdatedAt.Before(record.UpdatedAt) {
				return true
			}
		}
	}
	return false
}

// cachedFlowMutation retains a Flow write this process persisted, together with
// the list request generation current when the write completed. The generation
// is a causal version: a fetch issued at a later generation already observes the
// write, so the cached copy can be dropped. Wall-clock UpdatedAt cannot serve
// that role because a peer writing the shared state root may have a clock behind
// this process.
//
// apply re-applies only the field this process changed. A fetch issued before
// the write can still read the store after a peer wrote a causally newer record,
// so targeted phase mutations use an overlay-only field and never restore their
// whole cached record over the incoming one.
type cachedFlowMutation struct {
	record     flowstore.FlowRecord
	generation uint64
	field      flowMutationField
	apply      func(flowstore.FlowRecord) flowstore.FlowRecord
}

// flowMutationField identifies what a cached write changed, so writes to
// different fields of one Flow are cached independently.
type flowMutationField string

const (
	// flowMutationWholeRecord covers writes that change phases and derived
	// state together, which cannot be expressed as a single-field overlay.
	flowMutationWholeRecord              flowMutationField = "whole-record"
	flowMutationHeadless                 flowMutationField = "headless"
	flowMutationAutoMode                 flowMutationField = "auto-mode"
	flowMutationPhaseAgentSettingsPrefix                   = "phase-agent-settings:"
)

func flowPhaseAgentSettingsMutationField(phaseID string) flowMutationField {
	return flowMutationField(flowMutationPhaseAgentSettingsPrefix + phaseID)
}

func flowPhaseStoredIndexByID(phases []flowstore.FlowPhase, phaseID string) int {
	for i := range phases {
		if phases[i].PhaseID == phaseID {
			return i
		}
	}
	identity := artifacts.NormalizePhaseID(phaseID)
	for i := range phases {
		if artifacts.NormalizePhaseID(phases[i].PhaseID) == identity {
			return i
		}
	}
	return -1
}

func (field flowMutationField) overlayOnly() bool {
	return strings.HasPrefix(string(field), flowMutationPhaseAgentSettingsPrefix)
}

func flowHeadlessOverlay(enabled bool) func(flowstore.FlowRecord) flowstore.FlowRecord {
	return func(record flowstore.FlowRecord) flowstore.FlowRecord {
		record.Headless = enabled
		return record
	}
}

func flowAutoModeOverlay(enabled bool) func(flowstore.FlowRecord) flowstore.FlowRecord {
	return func(record flowstore.FlowRecord) flowstore.FlowRecord {
		record.AutoMode = enabled
		return record
	}
}

func flowPhaseAgentSettingsOverlay(
	phaseID string,
	settings flowstore.PhaseAgentSettings,
	recordUpdatedAt time.Time,
	phaseUpdatedAt time.Time,
) func(flowstore.FlowRecord) flowstore.FlowRecord {
	return func(record flowstore.FlowRecord) flowstore.FlowRecord {
		target := flowPhaseStoredIndexByID(record.Phases, phaseID)
		if target < 0 || record.Phases[target].UpdatedAt.After(phaseUpdatedAt) {
			return record
		}
		if record.UpdatedAt.Before(recordUpdatedAt) {
			record.UpdatedAt = recordUpdatedAt
		}
		record.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
		record.Phases[target].Agent = settings.Agent
		record.Phases[target].Model = settings.Model
		record.Phases[target].ReasoningEffort = settings.ReasoningEffort
		if record.Phases[target].UpdatedAt.Before(phaseUpdatedAt) {
			record.Phases[target].UpdatedAt = phaseUpdatedAt
		}
		return record
	}
}

// preferNewerCachedFlowRecords re-applies mutations that a fetch issued before
// the write could not have seen. Mutations the fetch supersedes are dropped by
// pruneAcknowledgedFlowMutations.
//
// The generation alone cannot decide the merge: a request is numbered when it is
// issued, but the store read happens later and unlocked, so an older request can
// still return a record that already carries this write plus a newer peer write.
//
// Reconciliation therefore runs in two steps. UpdatedAt selects the base record
// from mutations allowed to contribute one: a cached record stamped later than
// the incoming one carries phase and status metadata the write result proved
// newer, so it becomes the base; otherwise the incoming record wins and a peer's
// newer work is kept. Targeted phase mutations are overlay-only and never
// contribute a base. Overlays are then re-applied on top of whichever base was
// chosen, so a field this process persisted survives either way, and a
// whole-record write that read the store before a concurrent toggle cannot
// revert it.
//
// UpdatedAt is the strongest ordering signal the store offers today, and it is
// not a causal version: a peer may have a clock behind this process, and a
// record-level stamp cannot separate a peer deliberately writing a field from a
// peer carrying a stale copy of it while writing others. Every residual case
// self-heals on the next refresh. Removing them needs a persisted per-record
// revision, tracked in approach-mfb.
func preferNewerCachedFlowRecords(incoming []flowstore.FlowRecord, request uint64, mutations []cachedFlowMutation) []flowstore.FlowRecord {
	merged := append([]flowstore.FlowRecord(nil), incoming...)
	for i, record := range merged {
		for _, mutation := range mutations {
			if mutation.field.overlayOnly() || !unacknowledgedFlowMutationFor(mutation, request, record) {
				continue
			}
			if merged[i].UpdatedAt.Before(mutation.record.UpdatedAt) {
				merged[i] = mutation.record
			}
		}
		for _, mutation := range mutations {
			if mutation.apply == nil || !unacknowledgedFlowMutationFor(mutation, request, record) {
				continue
			}
			merged[i] = mutation.apply(merged[i])
		}
	}
	return merged
}

func unacknowledgedFlowMutationFor(mutation cachedFlowMutation, request uint64, record flowstore.FlowRecord) bool {
	return mutation.generation >= request &&
		mutation.record.FlowID == record.FlowID &&
		sameRepoPath(mutation.record.RepoPath, record.RepoPath)
}

// pruneAcknowledgedFlowMutations drops base-contributing mutations that every
// surface able to accept a Flow result has already observed. Overlay-only
// mutations remain as same-field result-ordering watermarks until their Flow is
// deleted; their acknowledged generation keeps them inert during fetch merges.
// Repository Flows and Active Flows keep separate request counters and both
// accept results, so a repository fetch started while an Active Flows fetch is
// still outstanding must not retire a mutation that the older request still
// needs. A hidden Active Flows surface rejects its own results and re-fetches on
// entry, so it never holds pruning back.
func (m Model) pruneAcknowledgedFlowMutations() Model {
	threshold := m.currentListRequest(ui.ModeFlows)
	if active := m.currentListRequest(ui.ModeActiveFlows); m.activeFlowSurfaceVisible() && active < threshold {
		threshold = active
	}
	retained := make([]cachedFlowMutation, 0, len(m.latestFlowMutations))
	for _, mutation := range m.latestFlowMutations {
		if mutation.field.overlayOnly() || mutation.generation >= threshold {
			retained = append(retained, mutation)
		}
	}
	if len(retained) == len(m.latestFlowMutations) {
		return m
	}
	m.latestFlowMutations = retained
	return m
}

// rememberFlowMutation caches one write per Flow and field. Writes to different
// fields are independent, so a later auto-mode write must not evict a headless
// write that an in-flight fetch has still not observed.
func (m Model) rememberFlowMutation(flow flowstore.FlowRecord, field flowMutationField, apply func(flowstore.FlowRecord) flowstore.FlowRecord) Model {
	if flow.FlowID == "" || strings.TrimSpace(flow.RepoPath) == "" {
		return m
	}
	mutation := cachedFlowMutation{record: flow, generation: m.listRequestSeq, field: field, apply: apply}
	mutations := append([]cachedFlowMutation(nil), m.latestFlowMutations...)
	for i, cached := range mutations {
		if cached.field != field || cached.record.FlowID != flow.FlowID || !sameRepoPath(cached.record.RepoPath, flow.RepoPath) {
			continue
		}
		if !flow.UpdatedAt.Before(cached.record.UpdatedAt) {
			mutations[i] = mutation
		}
		m.latestFlowMutations = mutations
		return m
	}
	m.latestFlowMutations = append(mutations, mutation)
	return m
}

func (m Model) handleFlowDeleted(msg FlowDeletedMsg) (tea.Model, tea.Cmd) {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	m = m.clearDeletedFlowState(msg.FlowID)
	return m.startFlowSurfaceFetch()
}

func (m Model) handleFlowPhaseReset(msg flowPhaseResetMsg) (tea.Model, tea.Cmd) {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	phaseID := strings.TrimSpace(msg.PhaseID)
	if phaseID == "" {
		phaseID = "phase"
	}
	m = m.setStatus(statusOther, fmt.Sprintf("Reset Flow phase %s to ready", phaseID))
	return m.startFlowSurfaceFetch()
}

func (m Model) handleFlowPhaseResetFailed(msg flowPhaseResetFailedMsg) (tea.Model, tea.Cmd) {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to reset Flow phase"
	}
	m = m.setStatus(statusOther, errText)
	return m, nil
}

// handleFlowPhaseSessionReleased reports what release selected, not what it
// wrote: both halves of finalization no-op silently when they match nothing, so
// a successful call proves nothing was persisted. The surface refresh is what
// shows the truth.
func (m Model) handleFlowPhaseSessionReleased(msg flowPhaseSessionReleasedMsg) (tea.Model, tea.Cmd) {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	phaseID := strings.TrimSpace(msg.PhaseID)
	if phaseID == "" {
		phaseID = "phase"
	}
	noun := "sessions"
	if msg.Released == 1 {
		noun = "session"
	}
	m = m.setStatus(statusOther, fmt.Sprintf("Released %d unfinished %s on Flow phase %s", msg.Released, noun, phaseID))
	return m.startFlowSurfaceFetch()
}

func (m Model) handleFlowPhaseSessionReleaseFailed(msg flowPhaseSessionReleaseFailedMsg) (tea.Model, tea.Cmd) {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	errText := strings.TrimSpace(msg.Err)
	if errText == "" {
		errText = "failed to release Flow phase session"
	}
	if msg.Released > 0 {
		errText = fmt.Sprintf("Released %d, then %s", msg.Released, errText)
	}
	// A partial release changed the phase, so the surface has to be refetched
	// even though the gesture failed — and a finalize call that failed part way
	// through is a partial release too, one Released cannot count.
	if msg.Released > 0 || msg.Finalized {
		m = m.setStatus(statusOther, errText)
		return m.startFlowSurfaceFetch()
	}
	return m.setStatus(statusOther, errText), nil
}

func (m Model) handleFlowDeleteFailed(msg FlowDeleteFailedMsg) (tea.Model, tea.Cmd) {
	if !m.activeFlowSurfaceVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if msg.NotFound {
		m = m.clearDeletedFlowState(msg.FlowID)
		m = m.setStatus(statusOther, fmt.Sprintf("Flow already deleted: %s", flowDisplayName(msg.Title, msg.FlowID)))
		return m.startFlowSurfaceFetch()
	}
	errText := msg.Err
	if strings.TrimSpace(errText) == "" {
		errText = fmt.Sprintf("Unable to delete Flow %s", flowDisplayName(msg.Title, msg.FlowID))
	}
	m = m.setStatus(statusOther, errText)
	return m, nil
}

func (m Model) clearDeletedFlowState(flowID string) Model {
	if flowID == "" {
		return m
	}
	if m.expandedFlowID == flowID {
		m.expandedFlowID = ""
		m.selectedFlowPhaseID = ""
		m.flows = m.flows.SetItemHeight(flowItemHeight(""))
	}
	if m.expandedActiveFlowID == flowID {
		m.expandedActiveFlowID = ""
		m.selectedActiveFlowPhaseID = ""
		m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(""))
	}
	if record, ok := m.flows.Selected(); ok && record.FlowID == flowID {
		m.selectedFlowPhaseID = ""
	}
	if record, ok := m.activeFlows.Selected(); ok && record.FlowID == flowID {
		m.selectedActiveFlowPhaseID = ""
	}
	retainedMutations := make([]cachedFlowMutation, 0, len(m.latestFlowMutations))
	for _, mutation := range m.latestFlowMutations {
		if mutation.record.FlowID != flowID {
			retainedMutations = append(retainedMutations, mutation)
		}
	}
	m.latestFlowMutations = retainedMutations
	return m
}

func flowDisplayName(title, flowID string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return flowID
	}
	if flowID == "" {
		return title
	}
	return fmt.Sprintf("%s (%s)", title, flowID)
}

func (m Model) restoreExpandedFlowSelection(flowID, phaseID string) Model {
	if flowID == "" {
		m.expandedFlowID = ""
		m.selectedFlowPhaseID = ""
		m.flows = m.flows.SetItemHeight(flowItemHeight(""))
		return m
	}
	record, ok := m.flows.Selected()
	if !ok || record.FlowID != flowID {
		m.expandedFlowID = ""
		m.selectedFlowPhaseID = ""
		m.flows = m.flows.SetItemHeight(flowItemHeight(""))
		return m
	}
	if phaseID != "" {
		phase, ok := flowRecordPhaseByID(record, phaseID)
		if !ok {
			m.expandedFlowID = ""
			m.selectedFlowPhaseID = ""
			m.flows = m.flows.SetItemHeight(flowItemHeight(""))
			return m
		}
		phaseID = phase.PhaseID
	}
	m.expandedFlowID = flowID
	m.selectedFlowPhaseID = phaseID
	m.flows = m.flows.SetItemHeight(flowItemHeight(flowID))
	if m.activeFlowSurfaceVisible() {
		return m
	}
	return m.reflowFlows()
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

func (m Model) handleBeadDetailResult(msg BeadDetailResultMsg) (Model, tea.Cmd) {
	if m.beadDetailTargetMatches(msg.RepoPath, msg.Mode, msg.BeadID, msg.DiffRequest) {
		return m.pageBody(msg.Body)
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
		return (m.modeStored(msg.Mode) || msg.Mode == m.activeContentFetchMode()) &&
			m.isCurrentListRequest(msg.Mode, msg.ListRequest)
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
	case FetchBeadDetail:
		return m.beadDetailTargetMatches(msg.RepoPath, msg.Mode, msg.BeadID, msg.DiffRequest)
	default:
		return false
	}
}

func (m Model) beadDetailTargetMatches(repoPath string, mode ui.Mode, beadID string, request uint64) bool {
	if !m.isCurrentRepo(repoPath) || m.focusedMode() != mode || !ui.IsBeadsMode(mode) ||
		!m.activeViewMatches(FetchBeadDetail, mode, request) {
		return false
	}
	bead, ok := m.selectedVisibleBead()
	return ok && bead.ID == beadID
}

func (m Model) currentPlanTextTargetMatches(mode ui.Mode, planID string) bool {
	switch mode {
	case ui.ModePlans:
		record, ok := m.selectedPlan()
		return ok && record.PlanID == planID
	case ui.ModeFlows:
		record, ok := m.selectedFlow()
		return ok && record.PlanID == planID
	case ui.ModeActiveFlows:
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
	mode := m.focusedMode()
	switch {
	case mode == ui.ModeHistory:
		commit, ok := m.selectedCommit()
		if !ok {
			return m, nil
		}
		hash = commit.Hash
	case mode == ui.ModeReflog:
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
	if m.focusedMode() != ui.ModeSessions {
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

func (m Model) handleCopyFlowWorktreePath() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	flow, ok := m.selectedFlow()
	if !ok {
		return m, nil
	}
	value := flow.WorktreePath
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

func (m Model) handleCopyFlowID() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	flowID := m.selectedFlowID()
	if strings.TrimSpace(flowID) == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		if err := m.copyToClipboard(flowID); err != nil {
			return ClipboardResultMsg{Err: err.Error()}
		}
		return ClipboardResultMsg{}
	}
}

func (m Model) handleOpenSelectedFlowPR() (tea.Model, tea.Cmd) {
	pr, ok := m.selectedFlowPR()
	if !ok {
		return m, nil
	}
	return m, func() tea.Msg {
		if err := m.openURL(pr.URL); err != nil {
			return OpenURLResultMsg{Err: err.Error()}
		}
		return OpenURLResultMsg{Label: fmt.Sprintf("Opened PR #%d in browser", pr.Number)}
	}
}

func (m Model) handleOpenSelectedFlowIssue() (tea.Model, tea.Cmd) {
	issue, ok := m.selectedFlowIssue()
	if !ok {
		return m, nil
	}
	return m, func() tea.Msg {
		if err := m.openURL(issue.URL); err != nil {
			return OpenURLResultMsg{Err: err.Error()}
		}
		return OpenURLResultMsg{Label: fmt.Sprintf("Opened issue #%d in browser", issue.Number)}
	}
}

func (m Model) handleShowSessionSummary() (tea.Model, tea.Cmd) {
	if m.focusedMode() != ui.ModeSessions {
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
