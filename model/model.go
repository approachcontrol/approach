package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/model/pane"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

const listRequestSlots = int(ui.ModeFlows) + 1

// Model is the bubbletea application model.
type Model struct {
	repos                     pane.Pane[scanner.Repo]
	width                     int
	height                    int
	mode                      ui.Mode
	rows                      pane.Pane[gitquery.BranchRow]
	stashes                   pane.Pane[gitquery.Stash]
	worktrees                 pane.Pane[gitquery.Worktree]
	worktreeSessions          pane.Pane[sessions.SessionRecord]
	commits                   pane.Pane[gitquery.Commit]
	reflogs                   pane.Pane[gitquery.ReflogEntry]
	sessions                  pane.Pane[sessions.SessionRecord]
	plans                     pane.Pane[planstore.PlanRecord]
	flows                     pane.Pane[flowstore.FlowRecord]
	expandedPlanID            string
	expandedFlowID            string
	selectedPlanPhaseID       string
	selectedFlowPhaseID       string
	modal                     modal.Modal
	diffRequestSeq            uint64
	activeViewRequest         uint64
	activeViewKind            FetchKind
	activeViewMode            ui.Mode
	listRequestSeq            uint64
	worktreeSessionRequestSeq uint64
	activeWorktreeSessionReq  uint64
	inlineWorktreeSessionRepo string
	inlineWorktreeSessionPath string
	pendingInlineSessionRepo  string
	pendingInlineSessionPath  string
	pendingInlineSessionList  uint64
	worktreeCreateSeq         uint64
	activeWorktreeCreate      uint64
	flowCreateSeq             uint64
	activeFlowCreate          uint64
	repoRefreshSeq            uint64
	activeRepoRefresh         uint64
	listRequests              [listRequestSlots]uint64
	activePane                int // 0=left (repos), 1=right (content)
	destructive               bool
	status                    statusError
	visibleRepoFetchSeq       uint64
	visibleRepoFetchStatusSeq uint64
	visibleRepoFetch          visibleRepoFetchState
	searchActive              bool
	pendingBranchSelection    string
	pendingWorktreeSelection  string
	agentCommand              string
	planPromptTemplate        string
	flowPromptTemplates       FlowPromptTemplates
	scanRepos                 func() ([]scanner.Repo, error)
	fetchRepo                 func(string) error
	listSessions              func(sessions.SessionFilter) ([]sessions.SessionRecord, error)
	readTranscript            func(sessions.Provider, string) ([]sessions.TranscriptEvent, error)
	listPlans                 func(planstore.PlanFilter) ([]planstore.PlanRecord, error)
	listFlows                 func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error)
	startFlowPlan             func(FlowStartRequest) (FlowStartResult, error)
	setFlowPhase              func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	addFlowPhaseLaunchID      func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	readPlan                  func(string) (string, error)
	planMarkdownPath          func(string) (string, error)
	copyToClipboard           func(string) error
	pageText                  func(string) (actions.TerminalLaunchSpec, error)
	editFile                  func(string) (actions.TerminalLaunchSpec, error)
	saveAgent                 func(string) error
	launchTerminal            func(string) (actions.TerminalLaunchSpec, error)
	launchAgent               func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error)
	startEmbeddedTerminal     EmbeddedTerminalStarter
	embeddedTerminals         []embeddedTerminalSlot
	activeEmbeddedTerminalNum int
	embeddedTerminalTickGen   uint64
	terminalPrefixActive      bool
	finalizeAgentSession      func(actions.AgentLaunchContext) error
	sessionStateRoot          string
	bootstrapHookForRepo      func(string) (actions.BootstrapHook, bool)
	runBootstrapHook          func(actions.BootstrapContext, actions.BootstrapHook) error
}

type statusSource int

const (
	statusNone statusSource = iota
	statusFetch
	statusGitMutation
	statusOther
)

type statusError struct {
	Text      string
	Source    statusSource
	FetchKind FetchKind
	Mode      ui.Mode
	FadeStep  int
}

type visibleRepoFetchState struct {
	Request       uint64
	Total         int
	Completed     int
	Successes     int
	FailureNames  []string
	FailureCount  int
	CapturedPaths map[string]struct{}
}

// Options customizes production-only integrations while keeping New(repos)
// simple for tests.
type Options struct {
	AgentCommand          string
	StartupMode           ui.Mode
	PlanPromptTemplate    string
	FlowPromptTemplates   FlowPromptTemplates
	ScanRepos             func() ([]scanner.Repo, error)
	FetchRepo             func(string) error
	ListSessions          func(sessions.SessionFilter) ([]sessions.SessionRecord, error)
	ReadTranscript        func(sessions.Provider, string) ([]sessions.TranscriptEvent, error)
	ListPlans             func(planstore.PlanFilter) ([]planstore.PlanRecord, error)
	ListFlows             func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error)
	StartFlowPlan         func(FlowStartRequest) (FlowStartResult, error)
	SetFlowPhase          func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	AddFlowPhaseLaunchID  func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	ReadPlan              func(string) (string, error)
	PlanMarkdownPath      func(planID string) (string, error)
	CopyToClipboard       func(text string) error
	PageText              func(body string) (actions.TerminalLaunchSpec, error)
	EditFile              func(path string) (actions.TerminalLaunchSpec, error)
	SaveAgentCommand      func(string) error
	LaunchTerminal        func(path string) (actions.TerminalLaunchSpec, error)
	LaunchAgent           func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error)
	StartEmbeddedTerminal EmbeddedTerminalStarter
	FinalizeAgentSession  func(actions.AgentLaunchContext) error
	SessionStateRoot      string
	BootstrapHookForRepo  func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook      func(actions.BootstrapContext, actions.BootstrapHook) error
}

// New creates a Model from discovered repos.
func New(repos []scanner.Repo) Model {
	return NewWithOptions(repos, Options{})
}

// NewWithOptions creates a Model from discovered repos and startup options.
func NewWithOptions(repos []scanner.Repo, opts Options) Model {
	saveAgent := opts.SaveAgentCommand
	if saveAgent == nil {
		saveAgent = func(string) error { return nil }
	}
	fetchRepo := opts.FetchRepo
	if fetchRepo == nil {
		fetchRepo = actions.Fetch
	}
	listSessions := opts.ListSessions
	if listSessions == nil {
		listSessions = func(sessions.SessionFilter) ([]sessions.SessionRecord, error) { return nil, nil }
	}
	readTranscript := opts.ReadTranscript
	if readTranscript == nil {
		readTranscript = func(sessions.Provider, string) ([]sessions.TranscriptEvent, error) { return nil, nil }
	}
	listPlans := opts.ListPlans
	if listPlans == nil {
		listPlans = func(planstore.PlanFilter) ([]planstore.PlanRecord, error) { return nil, nil }
	}
	listFlows := opts.ListFlows
	if listFlows == nil {
		listFlows = func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil }
	}
	setFlowPhase := opts.SetFlowPhase
	if setFlowPhase == nil {
		root := opts.SessionStateRoot
		setFlowPhase = func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetPhase(update)
		}
	}
	addFlowPhaseLaunchID := opts.AddFlowPhaseLaunchID
	if addFlowPhaseLaunchID == nil {
		root := opts.SessionStateRoot
		addFlowPhaseLaunchID = func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.AddPhaseLaunchID(update)
		}
	}
	readPlan := opts.ReadPlan
	if readPlan == nil {
		readPlan = func(string) (string, error) { return "", nil }
	}
	planMarkdownPath := opts.PlanMarkdownPath
	if planMarkdownPath == nil {
		root := opts.SessionStateRoot
		planMarkdownPath = func(planID string) (string, error) {
			return planstore.MarkdownPath(root, planID)
		}
	}
	copyToClipboard := opts.CopyToClipboard
	if copyToClipboard == nil {
		copyToClipboard = actions.CopyToClipboard
	}
	pageText := opts.PageText
	if pageText == nil {
		pageText = actions.PageText
	}
	editFile := opts.EditFile
	if editFile == nil {
		editFile = actions.EditFile
	}
	launchTerminal := opts.LaunchTerminal
	if launchTerminal == nil {
		launchTerminal = actions.TerminalLaunch
	}
	launchAgent := opts.LaunchAgent
	if launchAgent == nil {
		launchAgent = actions.AgentLaunch
	}
	startEmbeddedTerminal := opts.StartEmbeddedTerminal
	if startEmbeddedTerminal == nil {
		startEmbeddedTerminal = defaultEmbeddedTerminalStarter
	}
	bootstrapHookForRepo := opts.BootstrapHookForRepo
	if bootstrapHookForRepo == nil {
		bootstrapHookForRepo = func(string) (actions.BootstrapHook, bool) { return actions.BootstrapHook{}, false }
	}
	runBootstrapHook := opts.RunBootstrapHook
	if runBootstrapHook == nil {
		runBootstrapHook = actions.RunBootstrapHook
	}
	startFlowPlan := opts.StartFlowPlan
	if startFlowPlan == nil {
		root := opts.SessionStateRoot
		createFlow := func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.Create(record)
		}
		setFlowStartMetadata := func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetStartMetadata(update)
		}
		starter := NewFlowStarter(FlowStarterOptions{
			CreateFlow:           createFlow,
			CreateWorktree:       actions.CreateFlowWorktree,
			SetStartMetadata:     setFlowStartMetadata,
			SetPhase:             setFlowPhase,
			AddPhaseLaunchID:     addFlowPhaseLaunchID,
			BootstrapHookForRepo: bootstrapHookForRepo,
			RunBootstrapHook:     runBootstrapHook,
			ResolveCommit:        actions.ResolveWorktreeCommit,
			NewLaunchID:          newLaunchID,
			FlowPromptTemplates:  opts.FlowPromptTemplates,
		})
		startFlowPlan = starter.StartPlan
	}
	finalizeAgentSession := opts.FinalizeAgentSession
	if finalizeAgentSession == nil {
		finalizeAgentSession = func(actions.AgentLaunchContext) error { return nil }
	}
	m := Model{
		repos:                 newRepoPane().SetItems(repos),
		rows:                  newBranchPane(),
		stashes:               newStashPane(),
		worktrees:             newWorktreePane(),
		worktreeSessions:      newSessionPane(),
		commits:               newCommitPane(),
		reflogs:               newReflogPane(),
		sessions:              newSessionPane(),
		plans:                 newPlanPane(),
		flows:                 newFlowPane(),
		mode:                  startupMode(opts.StartupMode),
		agentCommand:          agent.Normalize(opts.AgentCommand),
		planPromptTemplate:    opts.PlanPromptTemplate,
		flowPromptTemplates:   opts.FlowPromptTemplates,
		scanRepos:             opts.ScanRepos,
		fetchRepo:             fetchRepo,
		listSessions:          listSessions,
		readTranscript:        readTranscript,
		listPlans:             listPlans,
		listFlows:             listFlows,
		startFlowPlan:         startFlowPlan,
		setFlowPhase:          setFlowPhase,
		addFlowPhaseLaunchID:  addFlowPhaseLaunchID,
		readPlan:              readPlan,
		planMarkdownPath:      planMarkdownPath,
		copyToClipboard:       copyToClipboard,
		pageText:              pageText,
		editFile:              editFile,
		saveAgent:             saveAgent,
		launchTerminal:        launchTerminal,
		launchAgent:           launchAgent,
		startEmbeddedTerminal: startEmbeddedTerminal,
		finalizeAgentSession:  finalizeAgentSession,
		sessionStateRoot:      opts.SessionStateRoot,
		bootstrapHookForRepo:  bootstrapHookForRepo,
		runBootstrapHook:      runBootstrapHook,
	}
	for mode := ui.ModeWorktrees; mode <= ui.ModeFlows; mode++ {
		m.listRequestSeq++
		m.listRequests[int(mode)] = m.listRequestSeq
	}
	return m
}

func startupMode(mode ui.Mode) ui.Mode {
	if mode >= ui.ModeWorktrees && mode <= ui.ModeFlows {
		return mode
	}
	return ui.ModeWorktrees
}

func newLaunchID() string {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("wtui-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("wtui-%d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
}

func (m Model) Selected() int              { return m.repos.SelectedIndex() }
func (m Model) Width() int                 { return m.width }
func (m Model) Height() int                { return m.height }
func (m Model) Mode() ui.Mode              { return m.mode }
func (m Model) Rows() []gitquery.BranchRow { rows, _, _ := m.rows.View(); return rows }
func (m Model) Stashes() []gitquery.Stash  { stashes, _, _ := m.stashes.View(); return stashes }
func (m Model) BranchSelected() int        { return m.rows.SelectedIndex() }
func (m Model) StashSelected() int         { return m.stashes.SelectedIndex() }
func (m Model) Worktrees() []gitquery.Worktree {
	worktrees, _, _ := m.worktrees.View()
	return worktrees
}
func (m Model) WorktreeSessions() []sessions.SessionRecord {
	sessions, _, _ := m.worktreeSessions.View()
	return sessions
}
func (m Model) WorktreeSelected() int           { return m.worktrees.SelectedIndex() }
func (m Model) WorktreeScroll() int             { return m.worktrees.Scroll() }
func (m Model) WorktreeSessionSelected() int    { return m.worktreeSessions.SelectedIndex() }
func (m Model) Commits() []gitquery.Commit      { commits, _, _ := m.commits.View(); return commits }
func (m Model) CommitSelected() int             { return m.commits.SelectedIndex() }
func (m Model) CommitScroll() int               { return m.commits.Scroll() }
func (m Model) Reflogs() []gitquery.ReflogEntry { reflogs, _, _ := m.reflogs.View(); return reflogs }
func (m Model) Sessions() []sessions.SessionRecord {
	sessions, _, _ := m.sessions.View()
	return sessions
}
func (m Model) Plans() []planstore.PlanRecord {
	plans, _, _ := m.plans.View()
	return plans
}
func (m Model) Flows() []flowstore.FlowRecord {
	flows, _, _ := m.flows.View()
	return flows
}
func (m Model) PlanSelected() int               { return m.plans.SelectedIndex() }
func (m Model) PlanScroll() int                 { return m.plans.Scroll() }
func (m Model) FlowSelected() int               { return m.flows.SelectedIndex() }
func (m Model) FlowScroll() int                 { return m.flows.Scroll() }
func (m Model) ExpandedPlanID() string          { return m.expandedPlanID }
func (m Model) ExpandedFlowID() string          { return m.expandedFlowID }
func (m Model) SelectedPlanPhaseID() string     { return m.selectedPlanPhaseID }
func (m Model) SelectedFlowPhaseID() string     { return m.selectedFlowPhaseID }
func (m Model) ReflogSelected() int             { return m.reflogs.SelectedIndex() }
func (m Model) ReflogScroll() int               { return m.reflogs.Scroll() }
func (m Model) Overlay() ui.OverlayState        { return m.overlayState() }
func (m Model) OverlayDiff() string             { return m.modal.View().Diff }
func (m Model) OverlayText() string             { return m.modal.View().Text }
func (m Model) OverlayScroll() int              { return m.modal.View().Scroll }
func (m Model) ConfirmPrompt() string           { return m.modal.View().Prompt }
func (m Model) ConfirmForce() bool              { return m.modal.View().Force }
func (m Model) WorktreeInput() string           { return m.modal.View().Input }
func (m Model) WorktreeInputErr() string        { return m.modal.View().InputErr }
func (m Model) BranchScroll() int               { return m.rows.Scroll() }
func (m Model) RepoScroll() int                 { return m.repos.Scroll() }
func (m Model) StashScroll() int                { return m.stashes.Scroll() }
func (m Model) ActivePane() int                 { return m.activePane }
func (m Model) Destructive() bool               { return m.destructive }
func (m Model) TransientError() string          { return m.visibleStatusText() }
func (m Model) TransientErrorFadeStep() int     { return m.visibleStatusFadeStep() }
func (m Model) SearchActive() bool              { return m.searchActive }
func (m Model) RepoSearch() string              { return m.repos.Query() }
func (m Model) ItemSearch() string              { return m.activeItemPaneQuery() }
func (m Model) ListRequest(mode ui.Mode) uint64 { return m.currentListRequest(mode) }
func (m Model) AgentCommand() string            { return m.agentCommand }

func (m Model) Init() tea.Cmd {
	return m.fetchForMode()
}

func (m Model) View() string {
	repos, selected, repoScroll := m.repos.View()
	worktrees, worktreeSelected, worktreeScroll := m.worktrees.View()
	worktreeSessions, worktreeSessionSelected, worktreeSessionScroll := m.worktreeSessions.View()
	rows, branchSelected, branchScroll := m.rows.View()
	stashes, stashSelected, stashScroll := m.stashes.View()
	commits, commitSelected, commitScroll := m.commits.View()
	reflogs, reflogSelected, reflogScroll := m.reflogs.View()
	sessions, sessionSelected, sessionScroll := m.sessions.View()
	plans, planSelected, planScroll := m.plans.View()
	flows, flowSelected, flowScroll := m.flows.View()
	repoEmptyMessage := m.repoEmptyMessage(len(repos))
	rightEmptyMessage := m.rightEmptyMessage(len(repos), len(worktrees), len(rows), len(stashes), len(commits), len(reflogs), len(sessions), len(plans), len(flows))
	if len(repos) == 0 {
		worktrees = nil
		rows = nil
		stashes = nil
		commits = nil
		reflogs = nil
		sessions = nil
		plans = nil
		flows = nil
	}
	modalView := m.modal.View()
	return ui.Render(ui.RenderParams{
		Repos:                      repos,
		Selected:                   selected,
		Width:                      m.width,
		Height:                     m.height,
		Mode:                       m.mode,
		Branches:                   rows,
		Stashes:                    stashes,
		BranchSelected:             branchSelected,
		StashSelected:              stashSelected,
		Overlay:                    m.overlayState(),
		OverlayDiff:                modalView.Diff,
		OverlayScroll:              modalView.Scroll,
		ConfirmPrompt:              modalView.Prompt,
		ConfirmForce:               modalView.Force,
		WorktreeInputPrompt:        modalView.Prompt,
		WorktreeInputPlaceholder:   modalView.Placeholder,
		WorktreeInput:              modalView.Input,
		WorktreeInputErr:           modalView.InputErr,
		SelectPrompt:               modalView.Prompt,
		SelectItems:                uiSelectItems(modalView.SelectItems),
		SelectSelected:             modalView.SelectIndex,
		BranchScroll:               branchScroll,
		RepoScroll:                 repoScroll,
		StashScroll:                stashScroll,
		ActivePane:                 m.activePane,
		Destructive:                m.destructive,
		Worktrees:                  worktrees,
		WorktreeSelected:           worktreeSelected,
		WorktreeScroll:             worktreeScroll,
		WorktreeSessions:           worktreeSessions,
		WorktreeSessionSelected:    worktreeSessionSelected,
		WorktreeSessionScroll:      worktreeSessionScroll,
		InlineWorktreeSessions:     m.inlineWorktreeSessionPath != "",
		Commits:                    commits,
		CommitSelected:             commitSelected,
		CommitScroll:               commitScroll,
		Reflogs:                    reflogs,
		ReflogSelected:             reflogSelected,
		ReflogScroll:               reflogScroll,
		Sessions:                   sessions,
		SessionSelected:            sessionSelected,
		SessionScroll:              sessionScroll,
		EmbeddedTerminals:          m.embeddedTerminalTabs(),
		EmbeddedTerminalLines:      m.embeddedTerminalLines(),
		EmbeddedTerminalPrefix:     m.terminalPrefixActive,
		Plans:                      plans,
		PlanSelected:               planSelected,
		PlanScroll:                 planScroll,
		Flows:                      flows,
		FlowSelected:               flowSelected,
		FlowScroll:                 flowScroll,
		ExpandedPlanID:             m.expandedPlanID,
		ExpandedFlowID:             m.expandedFlowID,
		SelectedPlanPhaseID:        m.selectedPlanPhaseID,
		SelectedFlowPhaseID:        m.selectedFlowPhaseID,
		FlowPhaseLaunchReady:       m.selectedFlowPhaseLaunchReady(),
		FlowPhaseResumableSelected: m.selectedFlowPhaseResumable(),
		OverlayText:                modalView.Text,
		TransientError:             m.visibleStatusText(),
		TransientErrorFadeStep:     m.visibleStatusFadeStep(),
		SearchActive:               m.searchActive,
		RepoSearch:                 m.repos.Query(),
		ItemSearch:                 m.activeItemPaneQuery(),
		RepoEmptyMessage:           repoEmptyMessage,
		RightEmptyMessage:          rightEmptyMessage,
		FetchAvailable:             m.canFetch(),
		FetchVisibleAvailable:      m.canFetchVisibleRepos(),
		PullAvailable:              m.canPull(),
		WorktreeMoveAvailable:      m.canMoveWorktree(),
		WorktreeSessionsOpen:       m.inlineWorktreeSessionPath != "",
		AgentAvailable:             m.canLaunchAgent(),
		NewAgentAvailable:          m.canCreateAndLaunchAgent(),
	})
}

func (m Model) repoEmptyMessage(filteredRepos int) string {
	if filteredRepos > 0 {
		return ""
	}
	itemCount := m.repos.ItemCount()
	if m.repos.Query() != "" && itemCount > 0 {
		return "No repo results for " + m.repos.Query()
	}
	if itemCount == 0 {
		return "No repositories found"
	}
	return "No repo results"
}

func (m Model) rightEmptyMessage(filteredRepos, filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows int) string {
	if filteredRepos == 0 {
		if m.repos.Query() != "" && m.repos.ItemCount() > 0 {
			return "No matching repo"
		}
		return "No selected repo"
	}
	sourceCount, filteredCount := m.activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows)
	if m.activeItemPaneQuery() != "" && sourceCount > 0 && filteredCount == 0 {
		return "No " + modeResultName(m.mode) + " results for " + m.activeItemPaneQuery()
	}
	if m.status.Source == statusFetch && m.status.FetchKind == FetchList && m.status.Mode == m.mode {
		return "Could not load " + modeDataName(m.mode) + "; see status bar"
	}
	return modeEmptyMessage(m.mode)
}

func (m Model) activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows int) (int, int) {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.worktrees.ItemCount(), filteredWorktrees
	case ui.ModeBranches:
		return m.rows.ItemCount(), filteredBranches
	case ui.ModeStashes:
		return m.stashes.ItemCount(), filteredStashes
	case ui.ModeHistory:
		return m.commits.ItemCount(), filteredCommits
	case ui.ModeReflog:
		return m.reflogs.ItemCount(), filteredReflogs
	case ui.ModeSessions:
		return m.sessions.ItemCount(), filteredSessions
	case ui.ModePlans:
		return m.plans.ItemCount(), filteredPlans
	case ui.ModeFlows:
		return m.flows.ItemCount(), filteredFlows
	default:
		return 0, 0
	}
}

func modeDataName(mode ui.Mode) string {
	switch mode {
	case ui.ModeWorktrees:
		return "worktrees"
	case ui.ModeBranches:
		return "branches"
	case ui.ModeStashes:
		return "stashes"
	case ui.ModeHistory:
		return "commits"
	case ui.ModeReflog:
		return "reflog"
	case ui.ModeSessions:
		return "sessions"
	case ui.ModePlans:
		return "plans"
	case ui.ModeFlows:
		return "flows"
	default:
		return "items"
	}
}

func modeResultName(mode ui.Mode) string {
	switch mode {
	case ui.ModeWorktrees:
		return "worktree"
	case ui.ModeBranches:
		return "branch"
	case ui.ModeStashes:
		return "stash"
	case ui.ModeHistory:
		return "commit"
	case ui.ModeReflog:
		return "reflog"
	case ui.ModeSessions:
		return "session"
	case ui.ModePlans:
		return "plan"
	case ui.ModeFlows:
		return "flow"
	default:
		return "item"
	}
}

func modeEmptyMessage(mode ui.Mode) string {
	switch mode {
	case ui.ModeWorktrees:
		return "No worktrees to show"
	case ui.ModeBranches:
		return "No branches to show"
	case ui.ModeStashes:
		return "No stashes"
	case ui.ModeHistory:
		return "No commits"
	case ui.ModeReflog:
		return "No reflog entries"
	case ui.ModeSessions:
		return "No sessions"
	case ui.ModePlans:
		return "No plans"
	case ui.ModeFlows:
		return "No flows"
	default:
		return "nothing here yet"
	}
}

func (m Model) overlayState() ui.OverlayState {
	view := m.modal.View()
	switch view.Kind {
	case modal.Confirm:
		return ui.OverlayConfirm
	case modal.Input:
		return ui.OverlayWorktreeInput
	case modal.Select:
		return ui.OverlayAgentSelect
	case modal.Diff:
		switch view.DiffKind {
		case modal.DiffStash:
			return ui.OverlayStashDiff
		case modal.DiffBranch:
			return ui.OverlayBranchDiff
		case modal.DiffCommit:
			return ui.OverlayCommitDiff
		case modal.DiffWorktree:
			return ui.OverlayWorktreeDiff
		case modal.DiffReflog:
			return ui.OverlayReflogDiff
		case modal.DiffSessionTranscript:
			return ui.OverlaySessionTranscript
		}
	case modal.Text:
		return ui.OverlayPlanText
	}
	return ui.OverlayNone
}

func uiSelectItems(items []modal.SelectItem) []ui.SelectItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]ui.SelectItem, len(items))
	for i, item := range items {
		out[i] = ui.SelectItem{Label: item.Label, Value: item.Value}
	}
	return out
}

func (m Model) startViewRequest(kind FetchKind, mode ui.Mode) Model {
	m.diffRequestSeq++
	m.activeViewRequest = m.diffRequestSeq
	m.activeViewKind = kind
	m.activeViewMode = mode
	return m
}

func (m Model) invalidateViewRequest() Model {
	m.activeViewRequest = 0
	m.activeViewKind = FetchUnknown
	m.activeViewMode = 0
	return m
}

func (m Model) activeViewMatches(kind FetchKind, mode ui.Mode, request uint64) bool {
	return request != 0 &&
		request == m.activeViewRequest &&
		kind == m.activeViewKind &&
		mode == m.activeViewMode
}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resizeEmbeddedTerminals()
		m = m.clampSelectionsAfterFilter()
	case embeddedSessionPickerSelectedMsg:
		return m.handleEmbeddedSessionPickerSelected(msg)
	case terminateEmbeddedTerminalMsg:
		return m.handleTerminateEmbeddedTerminal(msg)
	case quitEmbeddedTerminalsMsg:
		return m.handleQuitEmbeddedTerminals()
	case embeddedTerminalTickMsg:
		if msg.Generation != m.embeddedTerminalTickGen {
			return m, nil
		}
		if len(m.embeddedTerminals) > 0 {
			return m, m.embeddedTerminalTickCmd()
		}
		return m, nil
	case BranchResultMsg:
		return m.handleBranchResult(msg), nil
	case StashResultMsg:
		return m.handleStashResult(msg), nil
	case StashDiffResultMsg:
		return m.handleStashDiffResult(msg)
	case BranchDiffResultMsg:
		return m.handleBranchDiffResult(msg)
	case StashDroppedMsg:
		return m.handleStashDropped(msg)
	case BranchDeletedMsg:
		return m.handleBranchDeleted(msg)
	case BranchCreatedMsg:
		return m.handleBranchCreated(msg)
	case BranchCreateFailedMsg:
		return m.handleBranchCreateFailed(msg), nil
	case WorktreeResultMsg:
		return m.handleWorktreeResult(msg)
	case WorktreeRemovedMsg:
		return m.handleWorktreeRemoved(msg)
	case WorktreeDeleteCompletedMsg:
		return m, nil
	case WorktreePrunedMsg:
		return m.handleWorktreePruned(msg)
	case WorktreeUnlockedMsg:
		return m.handleWorktreeUnlocked(msg)
	case WorktreeUnlockFailedMsg:
		return m.handleWorktreeUnlockFailed(msg), nil
	case GitFetchedMsg:
		return m.handleGitFetched(msg)
	case GitFetchFailedMsg:
		return m.handleGitFetchFailed(msg), nil
	case VisibleRepoFetchResultMsg:
		return m.handleVisibleRepoFetchResult(msg)
	case VisibleRepoFetchStatusFadeMsg:
		return m.handleVisibleRepoFetchStatusFade(msg), nil
	case VisibleRepoFetchStatusExpiredMsg:
		return m.handleVisibleRepoFetchStatusExpired(msg), nil
	case RepoRefreshResultMsg:
		return m.handleRepoRefreshResult(msg)
	case RepoRefreshFailedMsg:
		return m.handleRepoRefreshFailed(msg), nil
	case GitPulledMsg:
		return m.handleGitPulled(msg)
	case GitPullFailedMsg:
		return m.handleGitPullFailed(msg), nil
	case WorktreeCreatedMsg:
		return m.handleWorktreeCreated(msg)
	case WorktreeCreateFailedMsg:
		return m.handleWorktreeCreateFailed(msg), nil
	case WorktreeMovedMsg:
		return m.handleWorktreeMoved(msg)
	case WorktreeMoveFailedMsg:
		return m.handleWorktreeMoveFailed(msg), nil
	case WorktreeBootstrapFailedMsg:
		return m.handleWorktreeBootstrapFailed(msg)
	case CommitResultMsg:
		return m.handleCommitResult(msg), nil
	case ReflogResultMsg:
		return m.handleReflogResult(msg), nil
	case SessionResultMsg:
		return m.handleSessionResult(msg), nil
	case WorktreeSessionResultMsg:
		return m.handleWorktreeSessionResult(msg), nil
	case SessionTranscriptResultMsg:
		return m.handleSessionTranscriptResult(msg)
	case PlanResultMsg:
		return m.handlePlanResult(msg), nil
	case FlowResultMsg:
		return m.handleFlowResult(msg), nil
	case PlanReadResultMsg:
		return m.handlePlanReadResult(msg)
	case WorktreeDiffResultMsg:
		return m.handleWorktreeDiffResult(msg)
	case CommitDiffResultMsg:
		return m.handleCommitDiffResult(msg)
	case ReflogDiffResultMsg:
		return m.handleReflogDiffResult(msg)
	case ClipboardResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		}
		return m, nil
	case TerminalResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		}
		return m, nil
	case PlanEditResultMsg:
		if !m.isCurrentRepo(msg.RepoPath) {
			return m, nil
		}
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
			return m, nil
		}
		if m.mode == ui.ModePlans {
			return m.startFetchMode(ui.ModePlans)
		}
		return m, nil
	case AgentSetMsg:
		return m.handleAgentSet(msg), nil
	case AgentSetFailedMsg:
		return m.handleAgentSetFailed(msg), nil
	case PlanLaunchRequestedMsg:
		if msg.Request != 0 && (!m.isCurrentRepo(msg.LaunchContext.RepoPath) || !m.isCurrentFlowCreateRequest(msg.Request)) {
			return m, nil
		}
		m = m.clearFlowCreateRequest(msg.Request)
		next, launchCmd := m.launchAgentWithContext(msg.LaunchContext)
		if msg.LaunchContext.FlowID != "" && next.mode == ui.ModeFlows {
			next, fetchCmd := next.startFetchMode(ui.ModeFlows)
			return next, tea.Batch(fetchCmd, launchCmd)
		}
		return next, launchCmd
	case FlowTitleSubmittedMsg:
		return m.handleFlowTitleSubmitted(msg), nil
	case FlowInstructionsSubmittedMsg:
		return m.handleFlowInstructionsSubmitted(msg), nil
	case FlowCreateFailedMsg:
		return m.handleFlowCreateFailed(msg)
	case AgentResultMsg:
		resultErr := msg.Err
		// Detached launches only start the agent in an external
		// terminal/multiplexer session and return while it keeps running, so the
		// captured session must not be finalized here; provider hooks own that.
		if !msg.Detached && msg.LaunchContext.LaunchID != "" {
			if err := m.finalizeAgentSession(msg.LaunchContext); err != nil {
				if resultErr != "" {
					resultErr = fmt.Sprintf("%s; finalize session: %v", resultErr, err)
				} else {
					resultErr = fmt.Sprintf("finalize session: %v", err)
				}
			}
		}
		if resultErr != "" {
			m, resultErr = m.markFlowLaunchNeedsAttention(msg.LaunchContext, resultErr)
			m = m.setStatus(statusOther, resultErr)
			if msg.LaunchContext.FlowID != "" && m.mode == ui.ModeFlows {
				return m.startFetchMode(ui.ModeFlows)
			}
		} else if msg.Detached {
			m = m.setStatus(statusOther, agentLaunchedStatus(msg.LaunchContext.Command))
		}
		return m, nil
	case DeleteFailedMsg:
		return m.handleDeleteFailed(msg), nil
	case ForceDeleteFailedMsg:
		return m.handleForceDeleteFailed(msg), nil
	case FetchErrorMsg:
		return m.handleFetchError(msg), nil
	case ActionFailedMsg:
		next := m.handleActionFailed(msg)
		if next.mode == ui.ModeFlows && next.isCurrentRepo(msg.RepoPath) {
			return next.startFetchMode(ui.ModeFlows)
		}
		return next, nil
	}
	return m, nil
}

// --- Helpers ---

func (m Model) selectedRow() (gitquery.BranchRow, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.BranchRow{}, false
	}
	return m.rows.Selected()
}

func (m Model) selectedWorktree() (gitquery.Worktree, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Worktree{}, false
	}
	return m.worktrees.Selected()
}

func (m Model) selectedStash() (gitquery.Stash, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Stash{}, false
	}
	return m.stashes.Selected()
}

func (m Model) selectedCommit() (gitquery.Commit, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Commit{}, false
	}
	return m.commits.Selected()
}

func (m Model) selectedReflog() (gitquery.ReflogEntry, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.ReflogEntry{}, false
	}
	return m.reflogs.Selected()
}

func (m Model) selectedSession() (sessions.SessionRecord, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return sessions.SessionRecord{}, false
	}
	return m.sessions.Selected()
}

func (m Model) selectedWorktreeSession() (sessions.SessionRecord, bool) {
	if _, ok := m.currentRepoPath(); !ok || m.inlineWorktreeSessionPath == "" {
		return sessions.SessionRecord{}, false
	}
	return m.worktreeSessions.Selected()
}

func (m Model) selectedPlan() (planstore.PlanRecord, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return planstore.PlanRecord{}, false
	}
	return m.plans.Selected()
}

func (m Model) selectedFlow() (flowstore.FlowRecord, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return flowstore.FlowRecord{}, false
	}
	return m.flows.Selected()
}

func (m Model) selectedFlowID() string {
	record, ok := m.selectedFlow()
	if !ok {
		return ""
	}
	return record.FlowID
}

func (m Model) selectedPlanID() string {
	record, ok := m.selectedPlan()
	if !ok {
		return ""
	}
	return record.PlanID
}

func (m Model) selectedPlanPhase() (planstore.PlanPhase, bool) {
	record, ok := m.selectedPlan()
	if !ok || record.PlanID == "" || record.PlanID != m.expandedPlanID || m.selectedPlanPhaseID == "" {
		return planstore.PlanPhase{}, false
	}
	for _, phase := range record.Phases {
		if phase.PhaseID == m.selectedPlanPhaseID {
			return phase, true
		}
	}
	return planstore.PlanPhase{}, false
}

func (m Model) selectedPlanPhaseIndex() (int, bool) {
	record, ok := m.selectedPlan()
	if !ok || record.PlanID == "" || record.PlanID != m.expandedPlanID || m.selectedPlanPhaseID == "" {
		return 0, false
	}
	for i, phase := range record.Phases {
		if phase.PhaseID == m.selectedPlanPhaseID {
			return i, true
		}
	}
	return 0, false
}

func (m Model) selectedFlowPhase() (flowstore.FlowPhase, bool) {
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" || record.FlowID != m.expandedFlowID || m.selectedFlowPhaseID == "" {
		return flowstore.FlowPhase{}, false
	}
	for _, phase := range flowstore.OrderedPhases(record.Phases) {
		if phase.PhaseID == m.selectedFlowPhaseID {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

func (m Model) selectedFlowPhaseIndex() (int, bool) {
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" || record.FlowID != m.expandedFlowID || m.selectedFlowPhaseID == "" {
		return 0, false
	}
	for i, phase := range flowstore.OrderedPhases(record.Phases) {
		if phase.PhaseID == m.selectedFlowPhaseID {
			return i, true
		}
	}
	return 0, false
}

func (m Model) selectedFlowPhaseResumable() bool {
	phase, ok := m.selectedFlowPhase()
	if !ok || (phase.Status == flowstore.PhaseRunning && flowstore.PhaseAwaitingSession(phase)) {
		return false
	}
	if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
		return false
	}
	session, ok := flowstore.LatestPhaseSession(phase, true)
	if !ok {
		return false
	}
	return agent.Validate(agent.Normalize(strings.TrimSpace(session.Provider))) == nil
}

func (m Model) selectedFlowPhaseLaunchReady() bool {
	record, ok := m.selectedFlow()
	if !ok {
		return false
	}
	_, ok = readyFlowPhase(record)
	return ok
}

func (m Model) clearSelectedPlanPhase() Model {
	m.selectedPlanPhaseID = ""
	return m
}

func (m Model) clearSelectedFlowPhase() Model {
	m.selectedFlowPhaseID = ""
	return m
}

func (m Model) setExpandedPlanID(planID string) Model {
	m.expandedPlanID = planID
	m.selectedPlanPhaseID = ""
	m.plans = m.plans.SetItemHeight(planItemHeight(planID))
	m = m.reflowPlans()
	if planID == "" {
		return m
	}
	return m.reflowExpandedPlan()
}

func (m Model) setExpandedFlowID(flowID string) Model {
	m.expandedFlowID = flowID
	m.selectedFlowPhaseID = ""
	m.flows = m.flows.SetItemHeight(flowItemHeight(flowID))
	return m.reflowFlows()
}

func (m Model) canScrollExpandedPlan(delta, viewHeight int) bool {
	if m.expandedPlanID == "" || m.selectedPlanID() != m.expandedPlanID {
		return false
	}
	if viewHeight <= 0 {
		viewHeight = 1
	}
	plans := m.filteredPlans()
	selected := m.PlanSelected()
	if selected < 0 || selected >= len(plans) {
		return false
	}

	line := 0
	for i := 0; i < selected; i++ {
		line += planVisualHeight(plans[i], m.expandedPlanID)
	}
	height := planVisualHeight(plans[selected], m.expandedPlanID)
	scroll := m.PlanScroll()
	if delta > 0 {
		return line+height > scroll+viewHeight
	}
	if delta < 0 {
		return scroll > line
	}
	return false
}

func (m Model) canScrollExpandedFlow(delta, viewHeight int) bool {
	if m.expandedFlowID == "" || m.selectedFlowID() != m.expandedFlowID {
		return false
	}
	if viewHeight <= 0 {
		viewHeight = 1
	}
	flows := m.filteredFlows()
	selected := m.FlowSelected()
	if selected < 0 || selected >= len(flows) {
		return false
	}

	line := 0
	for i := 0; i < selected; i++ {
		line += flowVisualHeight(flows[i], m.expandedFlowID)
	}
	height := flowVisualHeight(flows[selected], m.expandedFlowID)
	scroll := m.FlowScroll()
	if delta > 0 {
		return line+height > scroll+viewHeight
	}
	if delta < 0 {
		return scroll > line
	}
	return false
}

func (m Model) reflowExpandedPlan() Model {
	plans := m.filteredPlans()
	selected := m.PlanSelected()
	if selected < 0 || selected >= len(plans) {
		return m
	}

	viewHeight := m.planContentHeight()
	line := 0
	for i := 0; i < selected; i++ {
		line += planVisualHeight(plans[i], m.expandedPlanID)
	}
	height := planVisualHeight(plans[selected], m.expandedPlanID)
	scroll := m.PlanScroll()
	target := scroll
	if scroll > line {
		target = line
	}
	if height <= viewHeight && line+height > target+viewHeight {
		target = line + height - viewHeight
	} else if height > viewHeight && line+1 >= target+viewHeight {
		target = line
	}
	if target != scroll {
		m.plans = m.plans.ScrollBy(target-scroll, viewHeight, m.contentWidth())
	}
	return m
}

func (m Model) reflowExpandedFlow() Model {
	flows := m.filteredFlows()
	selected := m.FlowSelected()
	if selected < 0 || selected >= len(flows) {
		return m
	}

	viewHeight := m.flowContentHeight()
	line := 0
	for i := 0; i < selected; i++ {
		line += flowVisualHeight(flows[i], m.expandedFlowID)
	}
	height := flowVisualHeight(flows[selected], m.expandedFlowID)
	scroll := m.FlowScroll()
	target := scroll
	if scroll > line {
		target = line
	}
	if height <= viewHeight && line+height > target+viewHeight {
		target = line + height - viewHeight
	} else if height > viewHeight && line+1 >= target+viewHeight {
		target = line
	}
	if target != scroll {
		m.flows = m.flows.ScrollBy(target-scroll, viewHeight, m.contentWidth())
	}
	return m
}

func (m Model) moveSelectedPlanPhase(delta int) (Model, bool) {
	if m.mode != ui.ModePlans || m.expandedPlanID == "" || m.selectedPlanID() != m.expandedPlanID {
		return m, false
	}
	record, ok := m.selectedPlan()
	if !ok || len(record.Phases) == 0 {
		return m, false
	}

	index, hasPhase := m.selectedPlanPhaseIndex()
	if !hasPhase {
		if delta > 0 {
			m.selectedPlanPhaseID = record.Phases[0].PhaseID
			return m.ensureSelectedPlanPhaseVisible(), true
		}
		return m, false
	}

	nextIndex := index + delta
	if nextIndex < 0 {
		m = m.clearSelectedPlanPhase()
		return m.reflowExpandedPlan(), true
	}
	if nextIndex >= len(record.Phases) {
		if m.plans.Len() <= 1 {
			return m.ensureSelectedPlanPhaseVisible(), true
		}
		before := m.selectedPlanID()
		m.plans = m.plans.Move(delta, m.contentHeightForMode(), m.contentWidth())
		if after := m.selectedPlanID(); before != "" && after != before {
			m = m.clearSelectedPlanPhase()
			m = m.setExpandedPlanID("")
		}
		return m, true
	}
	m.selectedPlanPhaseID = record.Phases[nextIndex].PhaseID
	return m.ensureSelectedPlanPhaseVisible(), true
}

func (m Model) moveSelectedFlowPhase(delta int) (Model, bool) {
	if m.mode != ui.ModeFlows || m.expandedFlowID == "" || m.selectedFlowID() != m.expandedFlowID {
		return m, false
	}
	record, ok := m.selectedFlow()
	phases := flowstore.OrderedPhases(record.Phases)
	if !ok || len(phases) == 0 {
		return m, false
	}

	index, hasPhase := m.selectedFlowPhaseIndex()
	if !hasPhase {
		if delta > 0 {
			m.selectedFlowPhaseID = phases[0].PhaseID
			return m.ensureSelectedFlowPhaseVisible(), true
		}
		return m, false
	}

	nextIndex := index + delta
	if nextIndex < 0 {
		m = m.clearSelectedFlowPhase()
		return m.reflowExpandedFlow(), true
	}
	if nextIndex >= len(phases) {
		if m.flows.Len() <= 1 {
			return m.ensureSelectedFlowPhaseVisible(), true
		}
		before := m.selectedFlowID()
		m.flows = m.flows.Move(delta, m.contentHeightForMode(), m.contentWidth())
		if after := m.selectedFlowID(); before != "" && after != before {
			m = m.clearSelectedFlowPhase()
			m = m.setExpandedFlowID("")
		}
		return m, true
	}
	m.selectedFlowPhaseID = phases[nextIndex].PhaseID
	return m.ensureSelectedFlowPhaseVisible(), true
}

func (m Model) ensureSelectedPlanPhaseVisible() Model {
	index, ok := m.selectedPlanPhaseIndex()
	if !ok {
		return m.reflowExpandedPlan()
	}
	line, ok := m.selectedPlanVisualLine()
	if !ok {
		return m
	}
	line += 1 + index
	viewHeight := m.planContentHeight()
	if viewHeight <= 0 {
		viewHeight = 1
	}
	scroll := m.PlanScroll()
	target := scroll
	if line < target {
		target = line
	}
	if line >= target+viewHeight {
		target = line - viewHeight + 1
	}
	if target != scroll {
		m.plans = m.plans.ScrollBy(target-scroll, viewHeight, m.contentWidth())
	}
	return m
}

func (m Model) ensureSelectedFlowPhaseVisible() Model {
	index, ok := m.selectedFlowPhaseIndex()
	if !ok {
		return m.reflowExpandedFlow()
	}
	line, ok := m.selectedFlowVisualLine()
	if !ok {
		return m
	}
	line += 1 + index
	viewHeight := m.flowContentHeight()
	if viewHeight <= 0 {
		viewHeight = 1
	}
	scroll := m.FlowScroll()
	target := scroll
	if line < target {
		target = line
	}
	if line >= target+viewHeight {
		target = line - viewHeight + 1
	}
	if target != scroll {
		m.flows = m.flows.ScrollBy(target-scroll, viewHeight, m.contentWidth())
	}
	return m
}

func (m Model) selectedPlanVisualLine() (int, bool) {
	plans := m.filteredPlans()
	selected := m.PlanSelected()
	if selected < 0 || selected >= len(plans) {
		return 0, false
	}
	line := 0
	for i := 0; i < selected; i++ {
		line += planVisualHeight(plans[i], m.expandedPlanID)
	}
	return line, true
}

func (m Model) selectedFlowVisualLine() (int, bool) {
	flows := m.filteredFlows()
	selected := m.FlowSelected()
	if selected < 0 || selected >= len(flows) {
		return 0, false
	}
	line := 0
	for i := 0; i < selected; i++ {
		line += flowVisualHeight(flows[i], m.expandedFlowID)
	}
	return line, true
}

func (m Model) isSelectedBranchDirtyWorktree() bool {
	row, ok := m.selectedRow()
	return ok && row.Branch.Dirty && row.Branch.IsWorktree
}

func (m Model) reflowStashes() Model {
	contentHeight := m.height - ui.StashContentOverhead
	if contentHeight <= 0 {
		contentHeight = 1
	}
	rightContentWidth := m.width - ui.LeftPaneWidth - 2
	m.stashes = m.stashes.Reflow(contentHeight, rightContentWidth)
	return m
}

func (m Model) reflowRepos() Model {
	contentHeight := m.height - ui.RepoContentOverhead
	if contentHeight <= 0 {
		contentHeight = 1
	}
	m.repos = m.repos.Reflow(contentHeight, ui.LeftPaneWidth-2)
	return m
}

func (m Model) reflowWorktrees() Model {
	contentHeight := m.height - ui.WorktreeContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.worktrees = m.worktrees.Reflow(contentHeight, m.contentWidth())
	m = m.reflowWorktreeSessions()
	return m
}

func (m Model) reflowWorktreeSessions() Model {
	m.worktreeSessions = m.worktreeSessions.Reflow(m.worktreeSessionContentHeight(), m.contentWidth())
	return m
}

func (m Model) reflowReflogs() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.reflogs = m.reflogs.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowSessions() Model {
	m.sessions = m.sessions.Reflow(m.sessionContentHeight(), m.contentWidth())
	return m
}

func (m Model) reflowPlans() Model {
	m.plans = m.plans.Reflow(m.planContentHeight(), m.contentWidth())
	if m.selectedPlanPhaseID != "" {
		return m.ensureSelectedPlanPhaseVisible()
	}
	return m
}

func (m Model) reflowFlows() Model {
	m.flows = m.flows.Reflow(m.flowContentHeight(), m.contentWidth())
	if m.selectedFlowPhaseID != "" {
		return m.ensureSelectedFlowPhaseVisible()
	}
	if m.expandedFlowID != "" {
		return m.reflowExpandedFlow()
	}
	return m
}

func (m Model) reflowCommits() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.commits = m.commits.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowBranches() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.rows = m.rows.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) contentWidth() int {
	width := m.width - ui.LeftPaneWidth - 2
	if width < 0 {
		return 0
	}
	return width
}
