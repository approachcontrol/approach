package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/model/pane"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

const (
	listRequestSlots = int(ui.ModeBeadsClosed) + 1
	beadSubviewCount = int(ui.ModeBeadsClosed-ui.ModeBeadsReady) + 1
)

type beadSubviewState struct {
	pane      pane.Pane[beadsquery.Bead]
	available bool
	pending   bool
	error     string
	// repoPath is the repository the loaded rows describe. Retention across a
	// refetch is same-repo only, so a fetch for a different repository drops
	// the rows and cursor instead of clamping them onto the new repo's results.
	repoPath string
}

// Model is the bubbletea application model.
type Model struct {
	repos                     pane.Pane[scanner.Repo]
	width                     int
	height                    int
	mode                      ui.Mode
	lastGitMode               ui.Mode
	lastBeadsMode             ui.Mode
	rows                      pane.Pane[gitquery.BranchRow]
	stashes                   pane.Pane[gitquery.Stash]
	worktrees                 pane.Pane[gitquery.Worktree]
	worktreeSessions          pane.Pane[sessions.SessionRecord]
	commits                   pane.Pane[gitquery.Commit]
	reflogs                   pane.Pane[gitquery.ReflogEntry]
	sessions                  pane.Pane[sessions.SessionRecord]
	plans                     pane.Pane[planstore.PlanRecord]
	flows                     pane.Pane[flowstore.FlowRecord]
	activeFlowRecords         []flowstore.FlowRecord
	activeFlows               pane.Pane[flowstore.FlowRecord]
	beads                     [beadSubviewCount]beadSubviewState
	expandedPlanID            string
	expandedFlowID            string
	expandedActiveFlowID      string
	selectedPlanPhaseID       string
	selectedFlowPhaseID       string
	selectedActiveFlowPhaseID string
	flowHeadless              bool
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
	repoCreateSeq             uint64
	activeRepoCreate          uint64
	flowCreateSeq             uint64
	activeFlowCreate          uint64
	repoRefreshSeq            uint64
	activeRepoRefresh         uint64
	pendingRepoSelection      string
	listRequests              [listRequestSlots]uint64
	activePane                int // 0=left (repos), 1=right (content)
	activeFlowsReturnMode     ui.Mode
	destructive               bool
	status                    statusError
	statusSeq                 uint64
	statusTimer               statusTimerFactory
	pendingStatusCmds         []tea.Cmd
	visibleRepoFetchSeq       uint64
	visibleRepoFetch          visibleRepoFetchState
	searchActive              bool
	pendingBranchSelection    string
	pendingWorktreeSelection  string
	agentCommand              string
	codexModel                string
	claudeModel               string
	codexReasoningEffort      string
	claudeReasoningEffort     string
	defaultView               ui.Mode
	planPromptTemplate        string
	flowPromptTemplates       FlowPromptTemplates
	repoCreateRoot            string
	scanRepos                 func() ([]scanner.Repo, error)
	createRepo                func(actions.RepoCreateOptions) (actions.RepoCreateResult, error)
	fetchRepo                 func(string) error
	listSessions              func(sessions.SessionFilter) ([]sessions.SessionRecord, error)
	readTranscript            func(sessions.Provider, string) ([]sessions.TranscriptEvent, error)
	listPlans                 func(planstore.PlanFilter) ([]planstore.PlanRecord, error)
	listFlows                 func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error)
	listBeads                 [beadSubviewCount]func(string) ([]beadsquery.Bead, error)
	createFlow                func(FlowStartRequest) (FlowStartResult, error)
	startFlowPlan             func(FlowStartRequest) (FlowStartResult, error)
	setFlowPhase              func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	setFlowAutoMode           func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error)
	lookupPRMerge             func(int, string) (actions.PullRequestMerge, error)
	markFlowManualMerge       func(flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error)
	addFlowPhaseLaunchID      func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	resetFlowPhase            func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error)
	deleteFlow                func(string) error
	readPlan                  func(string) (string, error)
	planMarkdownPath          func(string) (string, error)
	copyToClipboard           func(string) error
	openCode                  func(string) error
	openURL                   func(string) error
	pageText                  func(string) (actions.TerminalLaunchSpec, error)
	editFile                  func(string) (actions.TerminalLaunchSpec, error)
	saveAgent                 func(string) error
	saveAgentModel            func(string, string) error
	saveAgentReasoningEffort  func(string, string) error
	saveDefaultView           func(ui.Mode) error
	savePromptTemplate        func(string, string, string) error
	resetPromptTemplate       func(string, string) error
	launchTerminal            func(string) (actions.TerminalLaunchSpec, error)
	launchDetachedTerminal    func(string, string) (actions.TerminalLaunchSpec, error)
	launchAgent               func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error)
	startEmbeddedTerminal     EmbeddedTerminalStarter
	embeddedTerminals         []embeddedTerminalSlot
	nextEmbeddedTerminalID    int
	activeEmbeddedTerminalNum int
	activeFlowTerminalNum     int
	flowFocus                 flowFocus
	autoAdvanceDrainFlows     map[string]struct{}
	pendingFlowPhaseResumes   map[flowPhaseResumeKey]string
	embeddedTerminalTickGen   uint64
	flowRefreshTickGen        uint64
	flowRefreshInFlight       uint64
	flowRefreshInFlightMode   ui.Mode
	autoAdvanceRequestSeq     uint64
	autoAdvanceInFlight       uint64
	autoAdvanceSnapshot       []flowstore.FlowRecord
	autoAdvanceLaunchedPhases []autoAdvanceLaunchedPhase
	terminalPrefixActive      bool
	terminalConfirmID         embeddedTerminalID
	terminalConfirmScope      embeddedTerminalScope
	finalizeAgentSession      func(actions.AgentLaunchContext) error
	sessionStateRoot          string
	bootstrapHookForRepo      func(string) (actions.BootstrapHook, bool)
	runBootstrapHook          func(actions.BootstrapContext, actions.BootstrapHook) error
}

type flowPhaseResumeKey struct {
	FlowID  string
	PhaseID string
}

type statusSource int

const (
	statusNone statusSource = iota
	statusFetch
	statusGitMutation
	statusOther
	statusFlowAutoAdvance
	statusVisibleRepoFetchSummary
)

type statusError struct {
	Text      string
	Source    statusSource
	Seq       uint64
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
	AgentCommand             string
	CodexModel               string
	ClaudeModel              string
	CodexReasoningEffort     string
	ClaudeReasoningEffort    string
	StartupMode              ui.Mode
	PlanPromptTemplate       string
	FlowPromptTemplates      FlowPromptTemplates
	FlowPresets              []flowstore.Preset
	FlowPreset               *flowstore.Preset
	RepoCreateRoot           string
	ScanRepos                func() ([]scanner.Repo, error)
	CreateRepo               func(actions.RepoCreateOptions) (actions.RepoCreateResult, error)
	FetchRepo                func(string) error
	ListSessions             func(sessions.SessionFilter) ([]sessions.SessionRecord, error)
	ReadTranscript           func(sessions.Provider, string) ([]sessions.TranscriptEvent, error)
	ListPlans                func(planstore.PlanFilter) ([]planstore.PlanRecord, error)
	ListFlows                func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error)
	ListReadyBeads           func(repoPath string) ([]beadsquery.Bead, error)
	ListBlockedBeads         func(repoPath string) ([]beadsquery.Bead, error)
	ListOpenBeads            func(repoPath string) ([]beadsquery.Bead, error)
	ListInProgressBeads      func(repoPath string) ([]beadsquery.Bead, error)
	ListClosedBeads          func(repoPath string) ([]beadsquery.Bead, error)
	CreateFlow               func(FlowStartRequest) (FlowStartResult, error)
	StartFlowPlan            func(FlowStartRequest) (FlowStartResult, error)
	SetFlowPhase             func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	SetFlowAutoMode          func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error)
	LookupPRMerge            func(int, string) (actions.PullRequestMerge, error)
	MarkFlowManualMerge      func(flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error)
	AddFlowPhaseLaunchID     func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	ResetFlowPhase           func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error)
	DeleteFlow               func(flowID string) error
	ReadPlan                 func(string) (string, error)
	PlanMarkdownPath         func(planID string) (string, error)
	CopyToClipboard          func(text string) error
	OpenCode                 func(path string) error
	OpenURL                  func(url string) error
	PageText                 func(body string) (actions.TerminalLaunchSpec, error)
	EditFile                 func(path string) (actions.TerminalLaunchSpec, error)
	SaveAgentCommand         func(string) error
	SaveAgentModel           func(string, string) error
	SaveAgentReasoningEffort func(string, string) error
	SaveDefaultView          func(ui.Mode) error
	SavePromptTemplate       func(section, key, value string) error
	ResetPromptTemplate      func(section, key string) error
	LaunchTerminal           func(path string) (actions.TerminalLaunchSpec, error)
	LaunchDetachedTerminal   func(targetShellCommand, cwd string) (actions.TerminalLaunchSpec, error)
	LaunchAgent              func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error)
	StartEmbeddedTerminal    EmbeddedTerminalStarter
	FinalizeAgentSession     func(actions.AgentLaunchContext) error
	SessionStateRoot         string
	BootstrapHookForRepo     func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook         func(actions.BootstrapContext, actions.BootstrapHook) error
}

// New creates a Model from discovered repos.
func New(repos []scanner.Repo) Model {
	return NewWithOptions(repos, Options{})
}

// NewWithOptions creates a Model from discovered repos and startup options.
func NewWithOptions(repos []scanner.Repo, opts Options) Model {
	newFlowStore := func() (*flowstore.Store, error) {
		return flowstore.NewStore(flowstore.StoreOptions{
			Root:    opts.SessionStateRoot,
			Presets: opts.FlowPresets,
		})
	}
	saveAgent := opts.SaveAgentCommand
	if saveAgent == nil {
		saveAgent = func(string) error { return nil }
	}
	saveAgentReasoningEffort := opts.SaveAgentReasoningEffort
	if saveAgentReasoningEffort == nil {
		saveAgentReasoningEffort = func(string, string) error { return nil }
	}
	saveAgentModel := opts.SaveAgentModel
	if saveAgentModel == nil {
		saveAgentModel = func(string, string) error { return nil }
	}
	saveDefaultView := opts.SaveDefaultView
	if saveDefaultView == nil {
		saveDefaultView = func(ui.Mode) error { return nil }
	}
	savePromptTemplate := opts.SavePromptTemplate
	if savePromptTemplate == nil {
		savePromptTemplate = func(string, string, string) error { return nil }
	}
	resetPromptTemplate := opts.ResetPromptTemplate
	if resetPromptTemplate == nil {
		resetPromptTemplate = func(string, string) error { return nil }
	}
	fetchRepo := opts.FetchRepo
	if fetchRepo == nil {
		fetchRepo = actions.Fetch
	}
	createRepo := opts.CreateRepo
	if createRepo == nil {
		createRepo = actions.CreateRepo
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
	listReadyBeads := opts.ListReadyBeads
	if listReadyBeads == nil {
		listReadyBeads = beadsquery.ListReady
	}
	listBlockedBeads := opts.ListBlockedBeads
	if listBlockedBeads == nil {
		listBlockedBeads = beadsquery.ListBlocked
	}
	listOpenBeads := opts.ListOpenBeads
	if listOpenBeads == nil {
		listOpenBeads = beadsquery.ListOpen
	}
	listInProgressBeads := opts.ListInProgressBeads
	if listInProgressBeads == nil {
		listInProgressBeads = beadsquery.ListInProgress
	}
	listClosedBeads := opts.ListClosedBeads
	if listClosedBeads == nil {
		listClosedBeads = beadsquery.ListClosed
	}
	setFlowPhase := opts.SetFlowPhase
	if setFlowPhase == nil {
		setFlowPhase = func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetPhase(update)
		}
	}
	setFlowAutoMode := opts.SetFlowAutoMode
	if setFlowAutoMode == nil {
		setFlowAutoMode = func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetAutoMode(update)
		}
	}
	lookupPRMerge := opts.LookupPRMerge
	if lookupPRMerge == nil {
		lookupPRMerge = actions.LookupGitHubPRMerge
	}
	markFlowManualMerge := opts.MarkFlowManualMerge
	if markFlowManualMerge == nil {
		markFlowManualMerge = func(update flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.MarkManualMerge(update)
		}
	}
	addFlowPhaseLaunchID := opts.AddFlowPhaseLaunchID
	if addFlowPhaseLaunchID == nil {
		addFlowPhaseLaunchID = func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.AddPhaseLaunchID(update)
		}
	}
	resetFlowPhase := opts.ResetFlowPhase
	if resetFlowPhase == nil {
		resetFlowPhase = func(update flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.ResetRecoverableRunningPhase(update)
		}
	}
	deleteFlow := opts.DeleteFlow
	if deleteFlow == nil {
		deleteFlow = func(flowID string) error {
			store, err := newFlowStore()
			if err != nil {
				return err
			}
			return store.Delete(flowID)
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
	openCode := opts.OpenCode
	if openCode == nil {
		openCode = actions.OpenVSCode
	}
	openURL := opts.OpenURL
	if openURL == nil {
		openURL = actions.OpenURL
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
	launchDetachedTerminal := opts.LaunchDetachedTerminal
	if launchDetachedTerminal == nil {
		launchDetachedTerminal = func(targetShellCommand, cwd string) (actions.TerminalLaunchSpec, error) {
			return actions.DetachedTerminalLaunch(targetShellCommand, cwd, actions.LaunchOptions{})
		}
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
	createFlowForRepo := opts.CreateFlow
	startFlowPlan := opts.StartFlowPlan
	if createFlowForRepo == nil || startFlowPlan == nil {
		createFlow := func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.CreateWithOptions(record, flowstore.CreateOptions{Preset: opts.FlowPreset})
		}
		setFlowStartMetadata := func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
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
		if createFlowForRepo == nil {
			createFlowForRepo = starter.PrepareFlow
		}
		if startFlowPlan == nil {
			startFlowPlan = starter.StartPlan
		}
	}
	finalizeAgentSession := opts.FinalizeAgentSession
	if finalizeAgentSession == nil {
		finalizeAgentSession = func(actions.AgentLaunchContext) error { return nil }
	}
	initialMode := startupMode(opts.StartupMode)
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
		activeFlows:           newFlowPane(),
		beads:                 newBeadSubviews(),
		flowHeadless:          true,
		flowRefreshTickGen:    1,
		mode:                  initialMode,
		defaultView:           initialMode,
		agentCommand:          agent.Normalize(opts.AgentCommand),
		codexModel:            agent.NormalizeModel(opts.CodexModel),
		claudeModel:           agent.NormalizeModel(opts.ClaudeModel),
		codexReasoningEffort:  agent.NormalizeReasoningEffort(opts.CodexReasoningEffort),
		claudeReasoningEffort: agent.NormalizeReasoningEffort(opts.ClaudeReasoningEffort),
		planPromptTemplate:    opts.PlanPromptTemplate,
		flowPromptTemplates:   opts.FlowPromptTemplates,
		repoCreateRoot:        opts.RepoCreateRoot,
		scanRepos:             opts.ScanRepos,
		createRepo:            createRepo,
		fetchRepo:             fetchRepo,
		listSessions:          listSessions,
		readTranscript:        readTranscript,
		listPlans:             listPlans,
		listFlows:             listFlows,
		listBeads: [beadSubviewCount]func(string) ([]beadsquery.Bead, error){
			listReadyBeads,
			listBlockedBeads,
			listOpenBeads,
			listInProgressBeads,
			listClosedBeads,
		},
		createFlow:               createFlowForRepo,
		startFlowPlan:            startFlowPlan,
		setFlowPhase:             setFlowPhase,
		setFlowAutoMode:          setFlowAutoMode,
		lookupPRMerge:            lookupPRMerge,
		markFlowManualMerge:      markFlowManualMerge,
		addFlowPhaseLaunchID:     addFlowPhaseLaunchID,
		resetFlowPhase:           resetFlowPhase,
		deleteFlow:               deleteFlow,
		readPlan:                 readPlan,
		planMarkdownPath:         planMarkdownPath,
		copyToClipboard:          copyToClipboard,
		openCode:                 openCode,
		openURL:                  openURL,
		pageText:                 pageText,
		editFile:                 editFile,
		saveAgent:                saveAgent,
		saveAgentModel:           saveAgentModel,
		saveAgentReasoningEffort: saveAgentReasoningEffort,
		saveDefaultView:          saveDefaultView,
		savePromptTemplate:       savePromptTemplate,
		resetPromptTemplate:      resetPromptTemplate,
		launchTerminal:           launchTerminal,
		launchDetachedTerminal:   launchDetachedTerminal,
		launchAgent:              launchAgent,
		startEmbeddedTerminal:    startEmbeddedTerminal,
		finalizeAgentSession:     finalizeAgentSession,
		sessionStateRoot:         opts.SessionStateRoot,
		bootstrapHookForRepo:     bootstrapHookForRepo,
		runBootstrapHook:         runBootstrapHook,
	}
	if ui.IsGitMode(m.mode) {
		m.lastGitMode = m.mode
	}
	for mode := ui.ModeWorktrees; mode <= ui.ModeBeadsClosed; mode++ {
		m.listRequestSeq++
		m.listRequests[int(mode)] = m.listRequestSeq
	}
	if m.mode == ui.ModeFlows {
		if _, ok := m.currentRepoPath(); ok {
			m.flowRefreshInFlight = m.currentListRequest(ui.ModeFlows)
			m.flowRefreshInFlightMode = ui.ModeFlows
		}
	}
	if m.mode == ui.ModeActiveFlows {
		m.flowRefreshInFlight = m.currentListRequest(ui.ModeActiveFlows)
		m.flowRefreshInFlightMode = ui.ModeActiveFlows
	}
	return m
}

func startupMode(mode ui.Mode) ui.Mode {
	if mode >= ui.ModeWorktrees && mode <= ui.ModeActiveFlows {
		return mode
	}
	return ui.ModeWorktrees
}

func batchNonNil(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return tea.Batch(filtered...)
}

func newLaunchID() string {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("approach-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("approach-%d-%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
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
func (m Model) Beads(mode ui.Mode) []beadsquery.Bead {
	state, ok := m.beadSubview(mode)
	if !ok {
		return nil
	}
	beads, _, _ := state.pane.View()
	return beads
}
func (m Model) BeadsAvailable(mode ui.Mode) bool {
	state, ok := m.beadSubview(mode)
	return ok && state.available
}
func (m Model) BeadsPending(mode ui.Mode) bool {
	state, ok := m.beadSubview(mode)
	return ok && state.pending
}
func (m Model) BeadsError(mode ui.Mode) string {
	state, ok := m.beadSubview(mode)
	if !ok {
		return ""
	}
	return state.error
}
func (m Model) BeadsSelected(mode ui.Mode) int {
	state, ok := m.beadSubview(mode)
	if !ok {
		return 0
	}
	return state.pane.SelectedIndex()
}
func (m Model) BeadsScroll(mode ui.Mode) int {
	state, ok := m.beadSubview(mode)
	if !ok {
		return 0
	}
	return state.pane.Scroll()
}
func (m Model) BeadsReady() []beadsquery.Bead      { return m.Beads(ui.ModeBeadsReady) }
func (m Model) BeadsBlocked() []beadsquery.Bead    { return m.Beads(ui.ModeBeadsBlocked) }
func (m Model) BeadsOpen() []beadsquery.Bead       { return m.Beads(ui.ModeBeadsOpen) }
func (m Model) BeadsInProgress() []beadsquery.Bead { return m.Beads(ui.ModeBeadsInProgress) }
func (m Model) BeadsClosed() []beadsquery.Bead     { return m.Beads(ui.ModeBeadsClosed) }
func (m Model) BeadsOpenAvailable() bool           { return m.BeadsAvailable(ui.ModeBeadsOpen) }
func (m Model) BeadsOpenPending() bool             { return m.BeadsPending(ui.ModeBeadsOpen) }
func (m Model) BeadsOpenSelected() int             { return m.BeadsSelected(ui.ModeBeadsOpen) }
func (m Model) BeadsOpenScroll() int               { return m.BeadsScroll(ui.ModeBeadsOpen) }
func (m Model) PlanSelected() int                  { return m.plans.SelectedIndex() }
func (m Model) PlanScroll() int                    { return m.plans.Scroll() }
func (m Model) FlowSelected() int                  { return m.flows.SelectedIndex() }
func (m Model) FlowScroll() int                    { return m.flows.Scroll() }
func (m Model) ExpandedPlanID() string             { return m.expandedPlanID }
func (m Model) ExpandedFlowID() string             { return m.expandedFlowID }
func (m Model) SelectedPlanPhaseID() string        { return m.selectedPlanPhaseID }
func (m Model) SelectedFlowPhaseID() string        { return m.selectedFlowPhaseID }
func (m Model) ReflogSelected() int                { return m.reflogs.SelectedIndex() }
func (m Model) ReflogScroll() int                  { return m.reflogs.Scroll() }
func (m Model) Overlay() ui.OverlayState           { return m.overlayState() }
func (m Model) OverlayDiff() string                { return m.modal.View().Diff }
func (m Model) OverlayText() string                { return m.modal.View().Text }
func (m Model) OverlayScroll() int                 { return m.modal.View().Scroll }
func (m Model) FormView() ui.FormView              { return uiFormView(m.modal.View().Form) }
func (m Model) ConfirmPrompt() string              { return m.modal.View().Prompt }
func (m Model) ConfirmForce() bool                 { return m.modal.View().Force }
func (m Model) WorktreeInput() string              { return m.modal.View().Input }
func (m Model) InputMode() modal.InputMode         { return m.modal.View().InputMode }
func (m Model) InputCursor() int                   { return m.modal.View().InputCursor }
func (m Model) WorktreeInputErr() string           { return m.modal.View().InputErr }
func (m Model) BranchScroll() int                  { return m.rows.Scroll() }
func (m Model) RepoScroll() int                    { return m.repos.Scroll() }
func (m Model) StashScroll() int                   { return m.stashes.Scroll() }
func (m Model) ActivePane() int                    { return m.activePane }
func (m Model) Destructive() bool                  { return m.destructive }
func (m Model) TransientError() string             { return m.visibleStatusText() }
func (m Model) TransientErrorFadeStep() int        { return m.visibleStatusFadeStep() }
func (m Model) SearchActive() bool                 { return m.searchActive }
func (m Model) RepoSearch() string                 { return m.repos.Query() }
func (m Model) ItemSearch() string                 { return m.activeItemPaneQuery() }
func (m Model) ListRequest(mode ui.Mode) uint64    { return m.currentListRequest(mode) }
func (m Model) AgentCommand() string               { return m.agentCommand }
func (m Model) DefaultView() ui.Mode               { return m.defaultView }
func (m Model) ReasoningEffortFor(command string) string {
	switch agent.Normalize(command) {
	case agent.CommandCodex:
		return m.codexReasoningEffort
	case agent.CommandClaude:
		return m.claudeReasoningEffort
	default:
		return ""
	}
}

func (m Model) ModelFor(command string) string {
	switch agent.Normalize(command) {
	case agent.CommandCodex:
		return m.codexModel
	case agent.CommandClaude:
		return m.claudeModel
	default:
		return ""
	}
}

func (m Model) launchModelFor(command string) string {
	switch agent.Normalize(command) {
	case agent.CommandCodex, agent.CommandClaude:
		return m.ModelFor(command)
	default:
		return ""
	}
}

func (m Model) launchReasoningEffortFor(command string) string {
	switch agent.Normalize(command) {
	case agent.CommandCodex, agent.CommandClaude:
		return m.ReasoningEffortFor(command)
	default:
		return ""
	}
}

func (m Model) flowLaunchAgentSettings() (string, string, string) {
	command := agent.Normalize(m.agentCommand)
	return command, m.launchModelFor(command), m.launchReasoningEffortFor(command)
}

func (m Model) flowModelLabel() string {
	command := agent.Normalize(m.agentCommand)
	switch command {
	case agent.CommandCodex:
		return modelDisplay(m.ModelFor(command))
	case agent.CommandClaude:
		return strings.TrimPrefix(modelDisplay(m.ModelFor(command)), "claude-")
	case agent.CommandCodexApp:
		return "app"
	default:
		return ""
	}
}

func (m Model) flowReasoningEffortLabel() string {
	command := agent.Normalize(m.agentCommand)
	switch command {
	case agent.CommandCodex, agent.CommandClaude:
		return fmt.Sprintf("effort: %s", reasoningEffortDisplay(m.ReasoningEffortFor(command)))
	case agent.CommandCodexApp:
		return "effort: app"
	default:
		return ""
	}
}

func (m Model) flowAgentShortcutLabel() string {
	switch command := agent.Normalize(m.agentCommand); command {
	case agent.CommandCodex, agent.CommandCodexApp, agent.CommandClaude:
		return command
	default:
		return "choose agent"
	}
}

func reasoningEffortDisplay(effort string) string {
	effort = agent.NormalizeReasoningEffort(effort)
	if effort == "" {
		return agent.ReasoningEffortDefault
	}
	return effort
}

func modelDisplay(model string) string {
	model = agent.NormalizeModel(model)
	if model == "" {
		return agent.ModelDefault
	}
	return model
}

func (m Model) withModel(command, model string) Model {
	model = agent.NormalizeModel(model)
	switch agent.Normalize(command) {
	case agent.CommandCodex:
		m.codexModel = model
	case agent.CommandClaude:
		m.claudeModel = model
	}
	return m
}

func (m Model) withReasoningEffort(command, effort string) Model {
	effort = agent.NormalizeReasoningEffort(effort)
	switch agent.Normalize(command) {
	case agent.CommandCodex:
		m.codexReasoningEffort = effort
	case agent.CommandClaude:
		m.claudeReasoningEffort = effort
	}
	return m
}

func (m Model) RepoCreateRoot() string { return m.repoCreateRoot }

func (m Model) Init() tea.Cmd {
	fetchCmd := m.fetchForMode()
	if m.mode == ui.ModeFlows && fetchCmd == nil {
		fetchCmd = m.flowRefreshTickCmd()
	}
	return batchNonNil(fetchCmd, autoAdvanceTickCmd())
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
	var beadsActive []beadsquery.Bead
	beadsSelected, beadsScroll := 0, 0
	beadsAvailable, beadsPending, beadsError := false, false, ""
	if state, ok := m.activeBeadSubview(); ok {
		beadsActive, beadsSelected, beadsScroll = state.pane.View()
		beadsAvailable = state.available
		beadsPending = state.pending
		beadsError = state.error
	}
	if m.activeFlowSurfaceVisible() {
		flows, flowSelected, flowScroll = m.activeFlows.View()
	}
	flowAutoModeSelected := false
	if flowSelected >= 0 && flowSelected < len(flows) {
		flowAutoModeSelected = flows[flowSelected].AutoMode
	}
	_, flowIssueTargetSelected := m.selectedFlowIssue()
	_, flowPRTargetSelected := m.selectedFlowPR()
	repoEmptyMessage := m.repoEmptyMessage(len(repos))
	rightEmptyMessage := m.rightEmptyMessage(len(repos), len(worktrees), len(rows), len(stashes), len(commits), len(reflogs), len(sessions), len(plans), len(flows), len(beadsActive))
	if len(repos) == 0 {
		worktrees = nil
		rows = nil
		stashes = nil
		commits = nil
		reflogs = nil
		sessions = nil
		plans = nil
		flows = nil
		beadsActive = nil
	}
	modalView := m.modal.View()
	return ui.Render(ui.RenderParams{
		Repos:                        repos,
		ActiveTerminalRepoPaths:      m.activeTerminalRepoPaths(),
		Selected:                     selected,
		Width:                        m.width,
		Height:                       m.height,
		Mode:                         m.mode,
		ActiveFlows:                  m.activeFlowSurfaceVisible(),
		Branches:                     rows,
		Stashes:                      stashes,
		BranchSelected:               branchSelected,
		StashSelected:                stashSelected,
		Overlay:                      m.overlayState(),
		OverlayDiff:                  modalView.Diff,
		OverlayScroll:                modalView.Scroll,
		ConfirmPrompt:                modalView.Prompt,
		ConfirmForce:                 modalView.Force,
		InputPrompt:                  modalView.Prompt,
		InputPlaceholder:             modalView.Placeholder,
		InputValue:                   modalView.Input,
		InputError:                   modalView.InputErr,
		InputMode:                    uiInputMode(modalView.InputMode),
		InputHeight:                  modalView.InputHeight,
		InputCursor:                  modalView.InputCursor,
		WorktreeInputPrompt:          modalView.Prompt,
		WorktreeInputPlaceholder:     modalView.Placeholder,
		WorktreeInput:                modalView.Input,
		WorktreeInputErr:             modalView.InputErr,
		SelectPrompt:                 modalView.Prompt,
		SelectItems:                  uiSelectItems(modalView.SelectItems),
		SelectSelected:               modalView.SelectIndex,
		SelectWidth:                  modalView.SelectLayout.Width,
		SelectHeight:                 modalView.SelectLayout.Height,
		SelectPlacement:              uiSelectPlacement(modalView.SelectLayout.Placement),
		Form:                         uiFormView(modalView.Form),
		BranchScroll:                 branchScroll,
		RepoScroll:                   repoScroll,
		StashScroll:                  stashScroll,
		ActivePane:                   m.activePane,
		Destructive:                  m.destructive,
		Worktrees:                    worktrees,
		WorktreeSelected:             worktreeSelected,
		WorktreeScroll:               worktreeScroll,
		WorktreeSessions:             worktreeSessions,
		WorktreeSessionSelected:      worktreeSessionSelected,
		WorktreeSessionScroll:        worktreeSessionScroll,
		InlineWorktreeSessions:       m.inlineWorktreeSessionPath != "",
		Commits:                      commits,
		CommitSelected:               commitSelected,
		CommitScroll:                 commitScroll,
		Reflogs:                      reflogs,
		ReflogSelected:               reflogSelected,
		ReflogScroll:                 reflogScroll,
		Sessions:                     sessions,
		SessionSelected:              sessionSelected,
		SessionScroll:                sessionScroll,
		EmbeddedTerminals:            m.embeddedTerminalTabs(),
		EmbeddedTerminalLines:        m.embeddedTerminalLines(),
		EmbeddedTerminalPrefix:       m.terminalPrefixActive,
		Plans:                        plans,
		PlanSelected:                 planSelected,
		PlanScroll:                   planScroll,
		Flows:                        flows,
		FlowSelected:                 flowSelected,
		FlowScroll:                   flowScroll,
		BeadsOpen:                    beadsActive,
		BeadsOpenSelected:            beadsSelected,
		BeadsOpenScroll:              beadsScroll,
		BeadsOpenAvailable:           beadsAvailable,
		BeadsOpenPending:             beadsPending,
		BeadsError:                   beadsError,
		FlowEmbeddedTerminals:        m.flowEmbeddedTerminalTabs(),
		FlowEmbeddedTerminalLines:    m.flowEmbeddedTerminalLines(),
		FlowEmbeddedTerminalPrefix:   m.terminalPrefixActive && m.flowFocus == flowFocusTerminal,
		FlowTerminalActivity:         m.flowTerminalActivity(),
		FlowTerminalFocused:          m.flowFocus == flowFocusTerminal && m.hasEmbeddedTerminalForScope(embeddedTerminalScopeFlow),
		ExpandedPlanID:               m.expandedPlanID,
		ExpandedFlowID:               m.currentExpandedFlowID(),
		SelectedPlanPhaseID:          m.selectedPlanPhaseID,
		SelectedFlowPhaseID:          m.currentSelectedFlowPhaseID(),
		FlowHeadless:                 m.flowHeadless,
		FlowAutoModeSelected:         flowAutoModeSelected,
		FlowIssueTargetSelected:      flowIssueTargetSelected,
		FlowPRTargetSelected:         flowPRTargetSelected,
		FlowAgentLabel:               m.flowAgentShortcutLabel(),
		FlowModel:                    m.flowModelLabel(),
		FlowReasoningEffort:          m.flowReasoningEffortLabel(),
		DefaultViewLabel:             ViewChoiceLabel(m.defaultView),
		FlowNextLaunchReady:          m.selectedFlowHasLaunchablePhase(),
		FlowManualMergeReadySelected: m.selectedFlowManualMergeReady(),
		FlowPhaseResetReadySelected:  m.selectedFlowPhaseResettable(),
		FlowPhaseResumableSelected:   m.selectedFlowPhaseResumable(),
		OverlayText:                  modalView.Text,
		TransientError:               m.visibleStatusText(),
		TransientErrorFadeStep:       m.visibleStatusFadeStep(),
		SearchActive:                 m.searchActive,
		RepoSearch:                   m.repos.Query(),
		ItemSearch:                   m.activeItemPaneQuery(),
		RepoEmptyMessage:             repoEmptyMessage,
		RightEmptyMessage:            rightEmptyMessage,
		FetchAvailable:               m.canFetch(),
		FetchVisibleAvailable:        m.canFetchVisibleRepos(),
		RepoCreateAvailable:          m.canCreateRepo(),
		PullAvailable:                m.canPull(),
		WorktreeMoveAvailable:        m.canMoveWorktree(),
		WorktreeSessionsOpen:         m.inlineWorktreeSessionPath != "",
		AgentAvailable:               m.canLaunchAgent(),
		NewAgentAvailable:            m.canCreateAndLaunchAgent(),
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

func (m Model) rightEmptyMessage(filteredRepos, filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows, filteredBeadsOpen int) string {
	if filteredRepos == 0 {
		if m.repos.Query() != "" && m.repos.ItemCount() > 0 {
			return "No matching repo"
		}
		return "No selected repo"
	}
	// A pending Beads query hides its retained rows, so the pane reports
	// loading rather than a filter or fetch verdict about rows it is replacing.
	if state, ok := m.activeBeadSubview(); ok {
		if state.pending {
			return "loading " + beadsModeName(m.mode) + " beads"
		}
		if state.error != "" {
			return "Could not load " + beadsModeName(m.mode) + " beads: " + state.error
		}
	}
	sourceCount, filteredCount := m.activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows, filteredBeadsOpen)
	if m.activeItemPaneQuery() != "" && sourceCount > 0 && filteredCount == 0 {
		if m.activeFlowSurfaceVisible() {
			return "No flow results for " + m.activeItemPaneQuery()
		}
		return "No " + modeResultName(m.mode) + " results for " + m.activeItemPaneQuery()
	}
	if m.status.Source == statusFetch && m.status.FetchKind == FetchList && m.status.Mode == m.activeContentFetchMode() {
		message := "Could not load " + modeDataName(m.activeContentFetchMode())
		if m.status.Text != "" {
			message += "; see status bar"
		}
		return message
	}
	if m.activeFlowSurfaceVisible() {
		return "No active flows"
	}
	if state, ok := m.activeBeadSubview(); ok {
		if state.available {
			return "no " + beadsModeName(m.mode) + " beads"
		}
		return "beads not configured"
	}
	return modeEmptyMessage(m.mode)
}

func (m Model) activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows, filteredBeadsOpen int) (int, int) {
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.ItemCount(), filteredFlows
	}
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
	case ui.ModeActiveFlows:
		return m.activeFlows.ItemCount(), filteredFlows
	case ui.ModeBeadsReady, ui.ModeBeadsBlocked, ui.ModeBeadsOpen, ui.ModeBeadsInProgress, ui.ModeBeadsClosed:
		state, _ := m.activeBeadSubview()
		return state.pane.ItemCount(), filteredBeadsOpen
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
	case ui.ModeActiveFlows:
		return "active flows"
	case ui.ModeBeadsReady, ui.ModeBeadsBlocked, ui.ModeBeadsOpen, ui.ModeBeadsInProgress, ui.ModeBeadsClosed:
		return beadsModeName(mode) + " beads"
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
	case ui.ModeActiveFlows:
		return "flow"
	case ui.ModeBeadsReady, ui.ModeBeadsBlocked, ui.ModeBeadsOpen, ui.ModeBeadsInProgress, ui.ModeBeadsClosed:
		return "bead"
	default:
		return "item"
	}
}

func beadsModeName(mode ui.Mode) string {
	switch mode {
	case ui.ModeBeadsReady:
		return "ready"
	case ui.ModeBeadsBlocked:
		return "blocked"
	case ui.ModeBeadsOpen:
		return "open"
	case ui.ModeBeadsInProgress:
		return "in-progress"
	case ui.ModeBeadsClosed:
		return "closed"
	default:
		return ""
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
		return ui.OverlayInput
	case modal.Select:
		return ui.OverlaySelect
	case modal.Form:
		return ui.OverlayForm
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

func uiInputMode(mode modal.InputMode) ui.InputMode {
	if mode == modal.InputMultiLine {
		return ui.InputMultiLine
	}
	return ui.InputSingleLine
}

func uiSelectPlacement(placement modal.Placement) ui.SelectPlacement {
	switch placement {
	case modal.PlacementTopCenter:
		return ui.SelectPlacementTopCenter
	case modal.PlacementBottomCenter:
		return ui.SelectPlacementBottomCenter
	default:
		return ui.SelectPlacementCenter
	}
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

func uiFormView(view modal.FormView) ui.FormView {
	fields := make([]ui.FormField, len(view.Fields))
	for i, field := range view.Fields {
		fields[i] = ui.FormField{
			ID:            field.ID,
			Kind:          uiFormFieldKind(field.Kind),
			Label:         field.Label,
			Placeholder:   field.Placeholder,
			Value:         field.Value,
			Cursor:        field.Cursor,
			Checked:       field.Checked,
			Options:       uiSelectItems(field.Options),
			SelectedIndex: field.SelectedIndex,
		}
	}
	return ui.FormView{
		Purpose:    view.Purpose,
		Title:      view.Title,
		Fields:     fields,
		FocusIndex: view.FocusIndex,
		Error:      view.Error,
	}
}

func uiFormFieldKind(kind modal.FormFieldKind) ui.FormFieldKind {
	switch kind {
	case modal.FormMultilineText:
		return ui.FormMultilineText
	case modal.FormCheckbox:
		return ui.FormCheckbox
	case modal.FormChoice:
		return ui.FormChoice
	default:
		return ui.FormText
	}
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

func (m Model) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	defer func() {
		modelNext, ok := next.(Model)
		if !ok {
			return
		}
		modelNext, cmd = modelNext.drainStatusCmds(cmd)
		next = modelNext
	}()
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
	case embeddedPromptPrefillResultMsg:
		if !m.hasEmbeddedTerminalID(msg.ID) {
			return m, nil
		}
		if msg.Err != nil {
			m = m.dismissEmbeddedTerminal(msg.ID)
			return m.startFlowLaunchFailure(msg.LaunchContext, msg.Err.Error())
		}
		m = m.activateEmbeddedTerminal(msg.ID)
		return m.updateFlowTerminalFocusAfterLaunch(msg.LaunchContext), nil
	case embeddedTerminalTickMsg:
		if msg.Generation != m.embeddedTerminalTickGen {
			return m, nil
		}
		hadExitedFlowTerminals := m.hasExitedFlowEmbeddedTerminalAutoClose()
		m = m.dismissExitedFlowEmbeddedTerminals()
		var cmds []tea.Cmd
		if hadExitedFlowTerminals {
			var refreshCmd tea.Cmd
			m, refreshCmd = m.startFlowSurfaceRefreshFetch()
			cmds = append(cmds, refreshCmd)
		}
		if m.hasRunningEmbeddedTerminal() {
			cmds = append(cmds, m.embeddedTerminalTickCmd())
		}
		return m, batchNonNil(cmds...)
	case flowRefreshTickMsg:
		if msg.Generation != m.flowRefreshTickGen || !m.flowSurfaceVisible() {
			return m, nil
		}
		return m.startFlowSurfaceRefreshFetch()
	case autoAdvanceTickMsg:
		return m.startAutoAdvanceFetch()
	case AutoAdvanceResultMsg:
		return m.handleAutoAdvanceResult(msg)
	case StatusExpiredMsg:
		return m.handleStatusExpired(msg), nil
	case StatusFadeMsg:
		return m.handleStatusFade(msg), nil
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
	case RepoRefreshResultMsg:
		return m.handleRepoRefreshResult(msg)
	case RepoRefreshFailedMsg:
		return m.handleRepoRefreshFailed(msg), nil
	case RepoCreatedMsg:
		return m.handleRepoCreated(msg)
	case RepoCreateFailedMsg:
		return m.handleRepoCreateFailed(msg)
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
		next, autoLaunchCmd := m.handleFlowResult(msg)
		next, refreshCmd := next.finishFlowRefreshFetch(ui.ModeFlows, msg.ListRequest)
		return next, batchNonNil(refreshCmd, autoLaunchCmd)
	case ActiveFlowResultMsg:
		next, autoLaunchCmd := m.handleActiveFlowResult(msg)
		next, refreshCmd := next.finishFlowRefreshFetch(ui.ModeActiveFlows, msg.ListRequest)
		return next, batchNonNil(refreshCmd, autoLaunchCmd)
	case BeadsReadyResultMsg:
		return m.handleBeadsResult(ui.ModeBeadsReady, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error), nil
	case BeadsBlockedResultMsg:
		return m.handleBeadsResult(ui.ModeBeadsBlocked, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error), nil
	case BeadsOpenResultMsg:
		return m.handleBeadsOpenResult(msg), nil
	case BeadsInProgressResultMsg:
		return m.handleBeadsResult(ui.ModeBeadsInProgress, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error), nil
	case BeadsClosedResultMsg:
		return m.handleBeadsResult(ui.ModeBeadsClosed, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error), nil
	case FlowAutoModeSetMsg:
		return m.handleFlowAutoModeSet(msg), nil
	case FlowAutoModeSetFailedMsg:
		return m.handleFlowAutoModeSetFailed(msg), nil
	case FlowManualMergeSetMsg:
		return m.handleFlowManualMergeSet(msg), nil
	case FlowManualMergeSetFailedMsg:
		return m.handleFlowManualMergeSetFailed(msg), nil
	case flowPhaseResetConfirmedMsg:
		return m.handleFlowPhaseResetConfirmed(msg)
	case flowPhaseResetMsg:
		return m.handleFlowPhaseReset(msg)
	case flowPhaseResetFailedMsg:
		return m.handleFlowPhaseResetFailed(msg)
	case FlowDeletedMsg:
		return m.handleFlowDeleted(msg)
	case FlowDeleteFailedMsg:
		return m.handleFlowDeleteFailed(msg)
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
	case OpenURLResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		} else if msg.Label != "" {
			m = m.setStatus(statusOther, msg.Label)
		}
		return m, nil
	case TerminalResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		}
		return m, nil
	case EmbeddedTerminalDetachHandoffResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, "Detached embedded terminal, but failed to open terminal: "+msg.Err)
			return m, nil
		}
		target := strings.TrimSpace(msg.Target)
		if target == "" {
			target = "tmux"
		}
		m = m.setStatus(statusOther, "Detached embedded terminal and opened terminal: "+target)
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
	case AgentModelSetMsg:
		return m.handleAgentModelSet(msg), nil
	case AgentModelSetFailedMsg:
		return m.handleAgentModelSetFailed(msg), nil
	case AgentReasoningEffortSetMsg:
		return m.handleAgentReasoningEffortSet(msg), nil
	case AgentReasoningEffortSetFailedMsg:
		return m.handleAgentReasoningEffortSetFailed(msg), nil
	case DefaultViewSetMsg:
		return m.handleDefaultViewSet(msg), nil
	case DefaultViewSetFailedMsg:
		return m.handleDefaultViewSetFailed(msg), nil
	case promptTemplateEditRequestedMsg:
		return m.handlePromptTemplateEditRequested(msg), nil
	case PromptTemplateSavedMsg:
		return m.handlePromptTemplateSaved(msg), nil
	case PromptTemplateSaveFailedMsg:
		return m.handlePromptTemplateSaveFailed(msg), nil
	case PromptTemplateResetMsg:
		return m.handlePromptTemplateReset(msg), nil
	case PromptTemplateResetFailedMsg:
		return m.handlePromptTemplateResetFailed(msg), nil
	case PlanLaunchRequestedMsg:
		if msg.Request != 0 && (!m.isCurrentRepo(msg.LaunchContext.RepoPath) || !m.isCurrentFlowCreateRequest(msg.Request)) {
			return m, nil
		}
		m = m.clearFlowCreateRequest(msg.Request)
		next, launchCmd := m.launchAgentWithContext(msg.LaunchContext)
		if msg.LaunchContext.FlowID != "" && next.flowSurfaceVisible() {
			next, fetchCmd := next.startFlowSurfaceFetch()
			return next, tea.Batch(fetchCmd, launchCmd)
		}
		return next, launchCmd
	case FlowEmbeddedLaunchRequestedMsg:
		if msg.Request != 0 {
			if !m.isCurrentRepo(msg.LaunchContext.RepoPath) || !m.isCurrentFlowCreateRequest(msg.Request) {
				return m, nil
			}
			m = m.clearFlowCreateRequest(msg.Request)
		}
		next, launchCmd := m.launchFlowEmbeddedWithContext(msg.LaunchContext)
		if msg.LaunchContext.FlowID != "" && next.flowSurfaceVisible() {
			next, fetchCmd := next.startFlowSurfaceFetch()
			return next, tea.Batch(fetchCmd, launchCmd)
		}
		return next, launchCmd
	case flowPhaseResumePersistedMsg:
		return m.handleFlowPhaseResumePersisted(msg)
	case flowPhaseResumePersistFailedMsg:
		return m.handleFlowPhaseResumePersistFailed(msg)
	case FlowCreatedMsg:
		return m.handleFlowCreated(msg)
	case FlowCreateFailedMsg:
		return m.handleFlowCreateFailed(msg)
	case AgentResultMsg:
		// Detached launches only start the agent in an external
		// terminal/multiplexer session and return while it keeps running, so the
		// captured session must not be finalized here; provider hooks own that.
		if !msg.Detached && msg.LaunchContext.LaunchID != "" {
			return m, func() tea.Msg {
				return agentSessionFinalizedMsg{Result: msg, Err: m.finalizeAgentSession(msg.LaunchContext)}
			}
		}
		return m.handleAgentResultAfterFinalization(msg, nil)
	case agentSessionFinalizedMsg:
		return m.handleAgentResultAfterFinalization(msg.Result, msg.Err)
	case flowLaunchFailurePersistedMsg:
		return m.handleFlowLaunchFailurePersisted(msg)
	case DeleteFailedMsg:
		return m.handleDeleteFailed(msg), nil
	case ForceDeleteFailedMsg:
		return m.handleForceDeleteFailed(msg), nil
	case FetchErrorMsg:
		next := m.handleFetchError(msg)
		return next.finishFlowRefreshFetch(msg.Mode, msg.ListRequest)
	case ActionFailedMsg:
		next, statusCmd := m.handleActionFailed(msg)
		if next.flowSurfaceVisible() && (next.activeFlowSurfaceVisible() || next.isCurrentRepo(msg.RepoPath)) {
			var refreshCmd tea.Cmd
			next, refreshCmd = next.startFlowSurfaceFetch()
			return next, batchNonNil(statusCmd, refreshCmd)
		}
		return next, statusCmd
	}
	return m, nil
}

func (m Model) handleAgentResultAfterFinalization(msg AgentResultMsg, finalizeErr error) (Model, tea.Cmd) {
	resultErr := msg.Err
	if finalizeErr != nil {
		if resultErr != "" {
			resultErr = fmt.Sprintf("%s; finalize session: %v", resultErr, finalizeErr)
		} else {
			resultErr = fmt.Sprintf("finalize session: %v", finalizeErr)
		}
	}
	if resultErr != "" {
		return m.startFlowLaunchFailure(msg.LaunchContext, resultErr)
	} else if msg.Detached {
		m = m.setStatus(statusOther, agentLaunchedStatus(msg.LaunchContext.Command))
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
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.Selected()
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

func (m Model) selectedFlowPR() (flowstore.PullRequest, bool) {
	if !m.flowSurfaceVisible() {
		return flowstore.PullRequest{}, false
	}
	if _, ok := m.selectedFlowPhase(); ok {
		return flowstore.PullRequest{}, false
	}
	record, ok := m.selectedFlow()
	if !ok || !flowstore.HasPRTarget(record.PR) {
		return flowstore.PullRequest{}, false
	}
	return record.PR, true
}

func (m Model) selectedFlowIssue() (flowstore.Issue, bool) {
	if !m.flowSurfaceVisible() {
		return flowstore.Issue{}, false
	}
	if _, ok := m.selectedFlowPhase(); ok {
		return flowstore.Issue{}, false
	}
	record, ok := m.selectedFlow()
	if !ok || !flowstore.HasIssueTarget(record.Issue) {
		return flowstore.Issue{}, false
	}
	return record.Issue, true
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
	expandedFlowID := m.currentExpandedFlowID()
	selectedPhaseID := m.currentSelectedFlowPhaseID()
	if !ok || record.FlowID == "" || record.FlowID != expandedFlowID || selectedPhaseID == "" {
		return flowstore.FlowPhase{}, false
	}
	return flowRecordPhaseByID(record, selectedPhaseID)
}

func (m Model) selectedFlowPhaseIndex() (int, bool) {
	record, ok := m.selectedFlow()
	expandedFlowID := m.currentExpandedFlowID()
	selectedPhaseID := m.currentSelectedFlowPhaseID()
	if !ok || record.FlowID == "" || record.FlowID != expandedFlowID || selectedPhaseID == "" {
		return 0, false
	}
	index, _, ok := flowRecordPhaseIndexByID(record, selectedPhaseID)
	return index, ok
}

func (m Model) selectedFlowPhaseResumable() bool {
	phase, ok := m.selectedFlowPhase()
	if !ok || flowPhaseHasRecoverableRunningSession(phase) ||
		flowPhaseHasStaleRunningLatestLaunch(phase) {
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

func flowPhaseHasRecoverableRunningSession(phase flowstore.FlowPhase) bool {
	_, ok := flowstore.RecoverableRunningPhaseResetReason(phase)
	return ok
}

func flowPhaseHasStaleRunningLatestLaunch(phase flowstore.FlowPhase) bool {
	return phase.Status == flowstore.PhaseRunning &&
		(flowstore.PhaseAwaitingSession(phase) || flowstore.PhaseLatestLaunchEnded(phase))
}

func (m Model) selectedFlowHasLaunchablePhase() bool {
	_, _, ok := m.selectedFlowNextLaunchablePhase()
	return ok
}

func (m Model) selectedFlowManualMergeReady() bool {
	record, _, ok := m.selectedManualMergeFlow()
	return ok && flowManualMergeEligible(record)
}

func (m Model) selectedFlowPhaseResettable() bool {
	record, ok := m.selectedFlow()
	if !ok {
		return false
	}
	phase, ok := m.selectedFlowPhase()
	return ok && m.flowPhaseResettable(record, phase)
}

func (m Model) flowPhaseByID(flowID, phaseID string) (flowstore.FlowRecord, flowstore.FlowPhase, bool) {
	for _, record := range m.flowLookupRecords() {
		if record.FlowID != flowID {
			continue
		}
		if phase, ok := flowRecordPhaseByID(record, phaseID); ok {
			return record, phase, true
		}
		return record, flowstore.FlowPhase{}, false
	}
	return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
}

func (m Model) flowLookupRecords() []flowstore.FlowRecord {
	if m.activeFlowSurfaceVisible() {
		return m.activeFlowRecords
	}
	return m.flows.Items()
}

func flowRecordPhaseByID(record flowstore.FlowRecord, phaseID string) (flowstore.FlowPhase, bool) {
	_, phase, ok := flowRecordPhaseIndexByID(record, phaseID)
	return phase, ok
}

func flowRecordPhaseIndexByID(record flowstore.FlowRecord, phaseID string) (int, flowstore.FlowPhase, bool) {
	requested := strings.TrimSpace(phaseID)
	phases := flowstore.OrderedPhases(record.Phases)
	for i, phase := range phases {
		if phase.PhaseID == requested {
			return i, phase, true
		}
	}
	want := artifacts.NormalizePhaseID(requested)
	if want == "" {
		return 0, flowstore.FlowPhase{}, false
	}
	for i, phase := range phases {
		if artifacts.NormalizePhaseID(phase.PhaseID) == want {
			return i, phase, true
		}
	}
	return 0, flowstore.FlowPhase{}, false
}

func (m Model) clearSelectedPlanPhase() Model {
	m.selectedPlanPhaseID = ""
	return m
}

func (m Model) clearSelectedFlowPhase() Model {
	if m.activeFlowSurfaceVisible() {
		m.selectedActiveFlowPhaseID = ""
		return m
	}
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
	if m.activeFlowSurfaceVisible() {
		m.expandedActiveFlowID = flowID
		m.selectedActiveFlowPhaseID = ""
		m.activeFlows = m.activeFlows.SetItemHeight(flowItemHeight(flowID))
		return m.reflowActiveFlows()
	}
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
	expandedFlowID := m.currentExpandedFlowID()
	if expandedFlowID == "" || m.selectedFlowID() != expandedFlowID {
		return false
	}
	if viewHeight <= 0 {
		viewHeight = 1
	}
	flows := m.currentFilteredFlows()
	selected := m.currentFlowSelectedIndex()
	if selected < 0 || selected >= len(flows) {
		return false
	}

	line := 0
	for i := 0; i < selected; i++ {
		line += flowVisualHeight(flows[i], expandedFlowID)
	}
	height := flowVisualHeight(flows[selected], expandedFlowID)
	scroll := m.currentFlowScroll()
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
	flows := m.currentFilteredFlows()
	selected := m.currentFlowSelectedIndex()
	if selected < 0 || selected >= len(flows) {
		return m
	}

	viewHeight := m.flowContentHeight()
	expandedFlowID := m.currentExpandedFlowID()
	line := 0
	for i := 0; i < selected; i++ {
		line += flowVisualHeight(flows[i], expandedFlowID)
	}
	height := flowVisualHeight(flows[selected], expandedFlowID)
	scroll := m.currentFlowScroll()
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
		m = m.setCurrentFlowPane(m.currentFlowPane().ScrollBy(target-scroll, viewHeight, m.contentWidth()))
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
	expandedFlowID := m.currentExpandedFlowID()
	if !m.flowSurfaceVisible() || expandedFlowID == "" || m.selectedFlowID() != expandedFlowID {
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
			m = m.setCurrentSelectedFlowPhaseID(phases[0].PhaseID)
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
		if m.currentFlowPane().Len() <= 1 {
			return m.ensureSelectedFlowPhaseVisible(), true
		}
		before := m.selectedFlowID()
		m = m.setCurrentFlowPane(m.currentFlowPane().Move(delta, m.contentHeightForMode(), m.contentWidth()))
		if after := m.selectedFlowID(); before != "" && after != before {
			m = m.clearSelectedFlowPhase()
			m = m.setExpandedFlowID("")
			m = m.syncActiveFlowTerminalToSelectedFlow()
		}
		return m, true
	}
	m = m.setCurrentSelectedFlowPhaseID(phases[nextIndex].PhaseID)
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
	scroll := m.currentFlowScroll()
	target := scroll
	if line < target {
		target = line
	}
	if line >= target+viewHeight {
		target = line - viewHeight + 1
	}
	if target != scroll {
		m = m.setCurrentFlowPane(m.currentFlowPane().ScrollBy(target-scroll, viewHeight, m.contentWidth()))
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
	flows := m.currentFilteredFlows()
	selected := m.currentFlowSelectedIndex()
	if selected < 0 || selected >= len(flows) {
		return 0, false
	}
	expandedFlowID := m.currentExpandedFlowID()
	line := 0
	for i := 0; i < selected; i++ {
		line += flowVisualHeight(flows[i], expandedFlowID)
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
	contentHeight := m.height - ui.GitContentOverhead
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
	if m.activeFlowSurfaceVisible() {
		return m
	}
	if m.selectedFlowPhaseID != "" {
		return m.ensureSelectedFlowPhaseVisible()
	}
	if m.expandedFlowID != "" {
		return m.reflowExpandedFlow()
	}
	return m
}

func (m Model) reflowCommits() Model {
	contentHeight := m.height - ui.GitContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.commits = m.commits.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowBranches() Model {
	contentHeight := m.height - ui.GitContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.rows = m.rows.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowBeads(mode ui.Mode) Model {
	index, ok := beadSubviewIndex(mode)
	if !ok {
		return m
	}
	m.beads[index].pane = m.beads[index].pane.Reflow(m.beadsContentHeight(), m.contentWidth())
	return m
}

func (m Model) reflowAllBeads() Model {
	for mode := ui.ModeBeadsReady; mode <= ui.ModeBeadsClosed; mode++ {
		m = m.reflowBeads(mode)
	}
	return m
}

func (m Model) contentWidth() int {
	width := m.width - ui.LeftPaneWidth - 2
	if width < 0 {
		return 0
	}
	return width
}
