package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/beadsmutate"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowownership"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/internal/flowlease"
	"github.com/approachcontrol/approach/internal/launchcontrol"
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
	total     int
	// repoPath is the repository the loaded rows describe. Retention across a
	// refetch is same-repo only, so a fetch for a different repository drops
	// the rows and cursor instead of clamping them onto the new repo's results.
	repoPath string
}

type beadExpansionTarget struct {
	token    uint64
	repoPath string
	mode     ui.Mode
	epicID   string
}

type beadExpansionSnapshot struct {
	target     beadExpansionTarget
	projection ui.BeadExpansion
}

type flowDegradationState struct {
	repoPath   string
	diagnostic *flowstore.PartialListError
}

type gitDegradationState struct {
	repoPath   string
	diagnostic *gitquery.PartialQueryError
}

type takeoverMode uint8

const (
	takeoverNone takeoverMode = iota
	takeoverActiveFlows
	takeoverPRBabysitter
)

// Model is the bubbletea application model.
type Model struct {
	repos                       pane.Pane[scanner.Repo]
	width                       int
	height                      int
	topMode                     ui.Mode
	bottomMode                  ui.Mode
	contentPane                 ui.Pane
	lastGitMode                 ui.Mode
	lastBeadsMode               ui.Mode
	rows                        pane.Pane[gitquery.BranchRow]
	stashes                     pane.Pane[gitquery.Stash]
	worktrees                   pane.Pane[gitquery.Worktree]
	worktreeSessions            pane.Pane[sessions.SessionRecord]
	commits                     pane.Pane[gitquery.Commit]
	reflogs                     pane.Pane[gitquery.ReflogEntry]
	sessions                    pane.Pane[sessions.SessionRecord]
	plans                       pane.Pane[planstore.PlanRecord]
	flows                       pane.Pane[flowstore.FlowRecord]
	notificationsEnabled        bool
	activeFlowRecords           []flowstore.FlowRecord
	latestFlowMutations         []cachedFlowMutation
	pendingFlowHeadlessWrites   []pendingFlowHeadlessWrite
	pendingFlowAutoMergeWrites  []pendingFlowAutoMergeWrite
	activeFlows                 pane.Pane[flowstore.FlowRecord]
	prBabysitterRecords         []flowstore.FlowRecord
	prBabysitterFlows           pane.Pane[flowstore.FlowRecord]
	prBabysitterStatuses        map[string]actions.PullRequestStatus
	tmuxNotificationWatches     map[string]tmuxNotificationWatch
	beads                       [beadSubviewCount]beadSubviewState
	beadExpansion               beadExpansionSnapshot
	beadExpansionSeq            uint64
	expandedPlanID              string
	expandedFlowID              string
	expandedActiveFlowID        string
	selectedPlanPhaseID         string
	selectedFlowPhaseID         string
	selectedActiveFlowPhaseID   string
	expandedPRBabysitterFlowID  string
	selectedPRBabysitterPhaseID string
	modal                       modal.Modal
	listFetchState
	flowPreparationAdmission bool
	flowPreparationSeq       uint64
	flowPreparationOwner     flowPreparationOwner
	sliceEpicLaunch          sliceEpicLaunchRecord

	flowDegradations         [listRequestSlots]flowDegradationState
	gitDegradations          [listRequestSlots]gitDegradationState
	activePane               ui.Pane
	repoPaneCollapsed        bool
	activeFlowSurface        bool
	takeover                 takeoverMode
	activeFlowReturnPane     ui.Pane
	activeFlowReturnContent  ui.Pane
	activeFlowReturnSet      bool
	prBabysitterCancel       context.CancelFunc
	destructive              bool
	status                   statusError
	statusSeq                uint64
	statusTimer              statusTimerFactory
	statusSchedule           StatusTimings
	pendingStatusCmds        []tea.Cmd
	searchActive             bool
	pendingBranchSelection   string
	pendingWorktreeSelection string
	agentConfig
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
	listChildrenBeads         func(string, string) ([]beadsquery.Bead, error)
	listReadyBeads            func(string) ([]beadsquery.Bead, error)
	claimBead                 func(repoPath, beadID string) error
	closeBead                 func(repoPath, beadID, reason string) error
	readEpicProgression       func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error)
	setEpicProgression        func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error)
	haltEpicProgression       func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error)
	enableEpicProgression     func(flowstore.PreparedEpicProgressionUpdate) (flowstore.EpicProgression, flowstore.FlowRecord, error)
	reconcileEpicSuccessor    func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error)
	showBead                  func(repoPath, beadID string) (string, error)
	countClosedBeads          func(string) (int, error)
	createFlow                func(FlowStartRequest) (FlowStartResult, error)
	setFlowPhase              func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	setFlowPhaseAgentSettings func(flowstore.PhaseAgentSettingsUpdate) (flowstore.FlowRecord, error)
	setFlowAutoMode           func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error)
	setFlowAutoMerge          func(flowstore.AutoMergeUpdate) (flowstore.FlowRecord, error)
	setFlowHeadless           func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error)
	autoMerge                 bool
	autoMergePolicy           *autoMergePolicyGate
	globalAutoMergeWrite      globalAutoMergeWrite
	globalAutoMergeWriteSeq   uint64
	saveFlowAutoMerge         func(bool) error
	lookupPRMerge             func(int, string) (actions.PullRequestMerge, error)
	lookupPRStatus            func(context.Context, int, string) (actions.PullRequestStatus, error)
	markFlowManualMerge       func(flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error)
	closeFlow                 func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error)
	reopenFlow                func(string) (flowstore.FlowRecord, error)
	reserveFlowRepairLaunch   func(string) (flowstore.FlowRecord, func(), error)
	reserveFlowLaunch         func(string) (flowstore.FlowRecord, func(), error)
	reserveFlowPreparation    func(string) (flowstore.FlowRecord, func(), error)
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
	savePromptTemplate        func(string, string, string) error
	resetPromptTemplate       func(string, string) error
	launchTerminal            func(string) (actions.TerminalLaunchSpec, error)
	launchDetachedTerminal    func(string, string) (actions.TerminalLaunchSpec, error)
	launchAgent               func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error)
	launchBackend             string
	tmuxLaunchAvailable       func() bool
	launchRepoTmuxAgent       func(actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error)
	repoTmuxSessionExists     func(string) bool
	repoTmuxLaunchWindowLive  func(string, ...string) bool
	repoTmuxLaunchStatus      func(string, ...string) (bool, error)
	repoTmuxSessionAttached   func(string) bool
	insideMultiplexer         func() bool
	// repoTmuxTerminalPending holds the repos whose first tmux-mode terminal
	// window has been dispatched but has not reported back. It debounces two
	// launches into one repo seconds apart, where the second would probe
	// `list-clients` before the first terminal's client has registered. It is
	// never the authority — RepoTmuxSessionAttached is — so it needs no expiry
	// beyond the result message that clears it.
	repoTmuxTerminalPending map[string]bool
	inspectFlowLease        func(string, string) (flowlease.LeaseState, error)
	leaseInspectInjected    bool
	tmuxAttachHint          bool
	embeddedTerminalState
	autoAdvanceState
	epicProgressionBaselines map[string]epicProgressionBaseline

	epicProgressionBaselineMinimumRequests map[string]uint64

	epicProgressionOwnedSuccessors map[string]epicProgressionOwnedSuccessor
	epicProgressionAdvanceSeq      uint64
	activeEpicProgressionAdvance   epicProgressionAdvanceRequest
	epicProgressionAdvanceCursor   string
	epicProgressionHaltSeq         uint64
	activeEpicProgressionHalt      epicProgressionHaltRequest

	pendingRepairAutoDrainFlowIDs map[string]repairAutoDrainMarker
	// flowAutofixTmuxLaunches maps a Flow ID to every autofix tmux
	// launch it made in this process. A phase-untracked launch writes no phase,
	// so on the tmux route this is the only in-process record that the Flow
	// already has an agent window open.
	//
	// Every launch is retained rather than the newest alone, exactly as a phase
	// keeps every ID in its LaunchIDs. The probe is advisory in one direction
	// only — a `list-windows` that times out answers false for a window that is
	// still open — so a second press is admitted while the first agent runs, and
	// keeping only the newer ID would leave the older agent unprobed as soon as
	// its window outlived the newer one.
	//
	// It needs no expiry: the probe asks whether those windows are still live,
	// so closed ones re-enable the shortcut on their own, and the slice is
	// bounded by how many times one Flow was launched in one TUI session.
	flowAutofixTmuxLaunches  map[string][]string
	flowOwnership            flowownership.Ownership[flowLaunchAttempt, flowLaunchSavedSessionKey]
	quitAfterFlowLaunch      bool
	interruptAfterFlowLaunch bool
	launchSeams              flowLaunchSeams

	// Tick cadences for the two 1 Hz poll loops. Zero means the production
	// default; Options injects a faster value so tests never wait on wall time.
	flowRefreshTickInterval time.Duration
	flowRefreshTickGen      uint64
	flowRefreshInFlight     uint64
	flowRefreshInFlightMode ui.Mode
	finalizeAgentSession    func(actions.AgentLaunchContext) error
	sessionStateRoot        string
	// launchPin is the approach binary agents launched by this Model must run.
	launchPin controlplane.Pin
	// launchControl registers launches with the process's launch controller;
	// nil leaves every launch on the direct path.
	launchControl LaunchRegistrar
	// reconcileLaunchExit reports an embedded terminal's exit for a tracked
	// launch; the controller decides whether the phase needs attention.
	reconcileLaunchExit func(flowID, phaseID, launchID string, ev launchcontrol.ExitEvidence) error
	// sweepLaunches runs the controller's periodic sweep off the render path.
	sweepLaunches        func()
	launchSweepTickGen   uint64
	bootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	runBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
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
	// AutoRank orders the messages that share statusFlowAutoAdvance. It is
	// meaningless for every other source.
	AutoRank autoAdvanceStatusRank
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

// agentConfig is the command/model/effort cluster shared by Model and Options.
// Options keeps the exported field names as the public projection; New copies
// them through agentConfigFromOptions.
type agentConfig struct {
	agentCommand          string
	codexModel            string
	claudeModel           string
	cursorModel           string
	codexReasoningEffort  string
	claudeReasoningEffort string
}

func agentConfigFromOptions(opts Options) agentConfig {
	return agentConfig{
		agentCommand:          agent.NormalizeStored(opts.AgentCommand),
		codexModel:            agent.NormalizeModel(opts.CodexModel),
		claudeModel:           agent.NormalizeModel(opts.ClaudeModel),
		cursorModel:           agent.NormalizeModel(opts.CursorModel),
		codexReasoningEffort:  agent.NormalizeReasoningEffort(opts.CodexReasoningEffort),
		claudeReasoningEffort: agent.NormalizeReasoningEffort(opts.ClaudeReasoningEffort),
	}
}

// Options customizes production-only integrations while keeping New(repos)
// simple for tests.
type Options struct {
	NotificationsEnabled      bool
	AgentCommand              string
	CodexModel                string
	ClaudeModel               string
	CursorModel               string
	CodexReasoningEffort      string
	ClaudeReasoningEffort     string
	PlanPromptTemplate        string
	FlowPromptTemplates       FlowPromptTemplates
	FlowPresets               []flowstore.Preset
	FlowPreset                *flowstore.Preset
	RepoCreateRoot            string
	ScanRepos                 func() ([]scanner.Repo, error)
	CreateRepo                func(actions.RepoCreateOptions) (actions.RepoCreateResult, error)
	FetchRepo                 func(string) error
	ListSessions              func(sessions.SessionFilter) ([]sessions.SessionRecord, error)
	ReadSession               func(sessions.Provider, string) (sessions.SessionRecord, error)
	ReadTranscript            func(sessions.Provider, string) ([]sessions.TranscriptEvent, error)
	ListPlans                 func(planstore.PlanFilter) ([]planstore.PlanRecord, error)
	ListFlows                 func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error)
	ListReadyBeads            func(repoPath string) ([]beadsquery.Bead, error)
	ListChildrenBeads         func(repoPath, parentID string) ([]beadsquery.Bead, error)
	ClaimBead                 func(repoPath, beadID string) error
	CloseBead                 func(repoPath, beadID, reason string) error
	ReadEpicProgression       func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error)
	SetEpicProgression        func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error)
	HaltEpicProgression       func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error)
	EnableEpicProgression     func(flowstore.PreparedEpicProgressionUpdate) (flowstore.EpicProgression, flowstore.FlowRecord, error)
	ReconcileEpicSuccessor    func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error)
	ListBlockedBeads          func(repoPath string) ([]beadsquery.Bead, error)
	ListOpenBeads             func(repoPath string) ([]beadsquery.Bead, error)
	ListInProgressBeads       func(repoPath string) ([]beadsquery.Bead, error)
	ListClosedBeads           func(repoPath string) ([]beadsquery.Bead, error)
	ShowBead                  func(repoPath, beadID string) (string, error)
	CountClosedBeads          func(repoPath string) (int, error)
	CreateFlow                func(FlowStartRequest) (FlowStartResult, error)
	ReadFlow                  func(flowID string) (flowstore.FlowRecord, error)
	SetFlowPhase              func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	SetFlowPhaseAgentSettings func(flowstore.PhaseAgentSettingsUpdate) (flowstore.FlowRecord, error)
	SetFlowAutoMode           func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error)
	SetFlowAutoMerge          func(flowstore.AutoMergeUpdate) (flowstore.FlowRecord, error)
	SetFlowHeadless           func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error)
	LookupPRMerge             func(int, string) (actions.PullRequestMerge, error)
	LookupPRStatus            func(context.Context, int, string) (actions.PullRequestStatus, error)
	MarkFlowManualMerge       func(flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error)
	CloseFlow                 func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error)
	ReopenFlow                func(flowID string) (flowstore.FlowRecord, error)
	ReserveFlowRepairLaunch   func(flowID string) (flowstore.FlowRecord, func(), error)
	ReserveFlowLaunch         func(flowID string) (flowstore.FlowRecord, func(), error)
	AddFlowPhaseLaunchID      func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	ResetFlowPhase            func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error)
	DeleteFlow                func(flowID string) error
	ReadPlan                  func(string) (string, error)
	PlanMarkdownPath          func(planID string) (string, error)
	CopyToClipboard           func(text string) error
	OpenCode                  func(path string) error
	OpenURL                   func(url string) error
	PageText                  func(body string) (actions.TerminalLaunchSpec, error)
	EditFile                  func(path string) (actions.TerminalLaunchSpec, error)
	SaveAgentCommand          func(string) error
	SaveAgentModel            func(string, string) error
	SaveAgentReasoningEffort  func(string, string) error
	FlowAutoMerge             bool
	SaveFlowAutoMerge         func(bool) error
	SavePromptTemplate        func(section, key, value string) error
	ResetPromptTemplate       func(section, key string) error
	LaunchTerminal            func(path string) (actions.TerminalLaunchSpec, error)
	LaunchDetachedTerminal    func(targetShellCommand, cwd string) (actions.TerminalLaunchSpec, error)
	LaunchAgent               func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error)
	// LaunchBackend is config's [launch].backend. Empty means embedded, which
	// leaves every launch route exactly as it is without tmux mode.
	LaunchBackend         string
	TmuxLaunchAvailable   func() bool
	LaunchRepoTmuxAgent   func(actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error)
	RepoTmuxSessionExists func(repoPath string) bool
	// RepoTmuxLaunchWindowLive probes whether any of these launches still has an
	// open tmux window. It is only consulted on user-initiated reset, resume,
	// and repair.
	RepoTmuxLaunchWindowLive func(repoPath string, launchIDs ...string) bool
	// RepoTmuxLaunchStatus distinguishes a confirmed missing window from
	// an inconclusive tmux probe. Notification sweeps consume watches only on a
	// successful probe that reports no live window.
	RepoTmuxLaunchStatus func(repoPath string, launchIDs ...string) (bool, error)
	// RepoTmuxSessionAttached probes whether a terminal is already watching a
	// repo's tmux session. It decides whether a tmux-mode launch opens the
	// repo's first terminal window, and runs only inside a command goroutine.
	RepoTmuxSessionAttached func(repoPath string) bool
	// InsideMultiplexer reports whether approach itself runs inside tmux or
	// Zellij, where tmux mode opens no terminal window of its own.
	InsideMultiplexer func() bool
	// InspectFlowLease is the cheap non-blocking occupancy seam used by render,
	// manual admission, and AutoMode. It must never invoke tmux or fork.
	InspectFlowLease      func(root, flowID string) (flowlease.LeaseState, error)
	StartEmbeddedTerminal EmbeddedTerminalStarter
	FinalizeAgentSession  func(actions.AgentLaunchContext) error
	SessionStateRoot      string
	// FlowStore is the process's already-open Flow store. When set, the fallback
	// mutators below reuse it instead of opening a second one against the same
	// approach.db: two pools would bootstrap twice and then contend for SQLite's
	// single writer through nothing but busy_timeout, so a write from one could
	// fail with "database is locked" while the other holds the writer. Tests and
	// callers that leave it nil still get the lazily-built cached store.
	FlowStore            *flowstore.Store
	BootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
	// LaunchPin identifies the approach binary every agent this TUI launches
	// must invoke. It is resolved by the process entry point rather than here so
	// model construction stays free of filesystem work: a zero Pin means "no
	// pin", which leaves agents on ambient PATH exactly as before.
	LaunchPin controlplane.Pin
	// AutoAdvanceTickInterval and FlowRefreshTickInterval override the two 1 Hz
	// poll cadences. Zero leaves each at its production default; tests set a
	// tiny value so driving a loop costs no real time.
	AutoAdvanceTickInterval time.Duration
	FlowRefreshTickInterval time.Duration
	// StatusTimings overrides the transient-status fade and expiry schedule.
	// Zero fields keep production timing.
	StatusTimings StatusTimings
	// LaunchPinNotice is context, never a gate: a degraded binary cache, or an
	// `approach` on PATH that is a different build from the one launching
	// agents. It is shown once at startup and changes nothing about routing.
	LaunchPinNotice string
	// LaunchControl registers every Flow-scoped launch with the process's
	// launch controller, which hands the agent the socket endpoint and token
	// its `approach flow` writes are proxied through. Nil means no controller:
	// agents open the store directly, as before.
	LaunchControl LaunchRegistrar
	// ReconcileLaunchExit is called when an embedded tracked launch's terminal
	// exits, with the exit evidence the model observed. The controller replays
	// anything the launch left pending and demotes a still-running phase.
	ReconcileLaunchExit func(flowID, phaseID, launchID string, ev launchcontrol.ExitEvidence) error
	// SweepLaunches is the controller's periodic sweep, run every
	// launchSweepInterval as a command so a detached agent that exits without
	// a result is reconciled while the TUI is up. Nil disables the tick.
	SweepLaunches func()
}

// New creates a Model from discovered repos.
func New(repos []scanner.Repo) Model {
	return NewWithOptions(repos, Options{})
}

// NewWithOptions creates a Model from discovered repos and startup options.
func NewWithOptions(repos []scanner.Repo, opts Options) Model {
	customPhaseLaunchPersistence := opts.AddFlowPhaseLaunchID != nil
	// A flowstore.Store owns a pooled SQLite handle for its whole life, so the
	// fallback mutators below must share one rather than build a store per
	// operation the way they did when the backend was plain files — that pattern
	// was free against files and leaks descriptors against SQLite.
	//
	// Only success is cached. These closures run on tea.Cmd goroutines, so the
	// mutex makes the lazy construction race-free; caching the error too would
	// be wrong, because the most likely failure is a bootstrap lock held by
	// another approach process mid-cutover, and that clears on its own. A
	// permanent cache would leave the TUI unable to write any Flow until restart.
	//
	// OWNERSHIP: a store built here is owned by the Model and lives until the
	// process exits — nothing closes it, because a value-type Bubble Tea model has
	// no lifecycle hook to close it from. That is bounded at one pool per Model
	// that actually performs a fallback write, and the TUI never builds one:
	// cmd/approach opens the process store and injects it as Options.FlowStore, so
	// this branch is reached only by tests and embedders, which build few Models
	// and exit. Anything that constructs Models repeatedly in a long-lived process
	// must inject Options.FlowStore and close it itself.
	var (
		flowStoreMu sync.Mutex
		flowStore   *flowstore.Store
	)
	newFlowStore := func() (*flowstore.Store, error) {
		if opts.FlowStore != nil {
			return opts.FlowStore, nil
		}
		flowStoreMu.Lock()
		defer flowStoreMu.Unlock()
		if flowStore != nil {
			return flowStore, nil
		}
		// RoleWriter, not RoleMigrator: the process-wide store built at startup
		// holds the one migrator role. This lazy fallback exists for when that
		// store is nil, so against a predecessor-schema root it refuses and
		// points at `approach db migrate` rather than migrating from inside the
		// alt screen.
		store, err := flowstore.NewStore(flowstore.StoreOptions{
			Root:    opts.SessionStateRoot,
			Role:    flowstore.RoleWriter,
			Presets: opts.FlowPresets,
		})
		if err != nil {
			return nil, err
		}
		flowStore = store
		return flowStore, nil
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
	saveFlowAutoMerge := opts.SaveFlowAutoMerge
	if saveFlowAutoMerge == nil {
		saveFlowAutoMerge = func(bool) error { return nil }
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
	readSession := opts.ReadSession
	if readSession == nil {
		readSession = func(provider sessions.Provider, sessionID string) (sessions.SessionRecord, error) {
			return sessions.SessionRecord{}, fmt.Errorf("session %q/%q not found", provider, sessionID)
		}
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
	listChildrenBeads := opts.ListChildrenBeads
	if listChildrenBeads == nil {
		listChildrenBeads = beadsquery.ListChildren
	}
	claimBead := opts.ClaimBead
	if claimBead == nil {
		claimBead = beadsmutate.Claim
	}
	closeBead := opts.CloseBead
	if closeBead == nil {
		closeBead = beadsmutate.Close
	}
	readEpicProgression := opts.ReadEpicProgression
	if readEpicProgression == nil {
		if opts.FlowStore == nil && strings.TrimSpace(opts.SessionStateRoot) == "" {
			// Expansion loads are automatic. A bare Model used by tests or an
			// embedder must not open and migrate the user's default artifact root.
			readEpicProgression = func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
				return flowstore.EpicProgression{}, false, nil
			}
		} else {
			readEpicProgression = func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
				store, err := newFlowStore()
				if err != nil {
					return flowstore.EpicProgression{}, false, err
				}
				return store.ReadEpicProgression(key)
			}
		}
	}
	setEpicProgression := opts.SetEpicProgression
	if setEpicProgression == nil {
		setEpicProgression = func(update flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.EpicProgression{}, err
			}
			return store.SetEpicProgression(update)
		}
	}
	haltEpicProgression := opts.HaltEpicProgression
	if haltEpicProgression == nil {
		haltEpicProgression = func(update flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.EpicProgression{}, err
			}
			return store.HaltEpicProgression(update)
		}
	}
	enableEpicProgression := opts.EnableEpicProgression
	if enableEpicProgression == nil {
		enableEpicProgression = func(update flowstore.PreparedEpicProgressionUpdate) (flowstore.EpicProgression, flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.EpicProgression{}, flowstore.FlowRecord{}, err
			}
			return store.EnableEpicProgressionForPreparedFlow(update)
		}
	}
	reconcileEpicSuccessor := opts.ReconcileEpicSuccessor
	if reconcileEpicSuccessor == nil {
		reconcileEpicSuccessor = func(update flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorRetryable}, err
			}
			return store.ReconcileEpicProgressionSuccessor(update)
		}
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
	showBead := opts.ShowBead
	if showBead == nil {
		showBead = beadsquery.Show
	}
	countClosedBeads := opts.CountClosedBeads
	if countClosedBeads == nil {
		countClosedBeads = beadsquery.CountClosed
	}
	readFlow := opts.ReadFlow
	if readFlow == nil {
		readFlow = func(flowID string) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.Read(flowID)
		}
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
	setFlowPhaseAgentSettings := opts.SetFlowPhaseAgentSettings
	if setFlowPhaseAgentSettings == nil {
		setFlowPhaseAgentSettings = func(update flowstore.PhaseAgentSettingsUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetPhaseAgentSettings(update)
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
	setFlowAutoMerge := opts.SetFlowAutoMerge
	if setFlowAutoMerge == nil {
		setFlowAutoMerge = func(update flowstore.AutoMergeUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetAutoMerge(update)
		}
	}
	setFlowHeadless := opts.SetFlowHeadless
	if setFlowHeadless == nil {
		setFlowHeadless = func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.SetHeadless(update)
		}
	}
	lookupPRMerge := opts.LookupPRMerge
	if lookupPRMerge == nil {
		lookupPRMerge = actions.LookupGitHubPRMerge
	}
	lookupPRStatus := opts.LookupPRStatus
	if lookupPRStatus == nil {
		lookupPRStatus = actions.LookupGitHubPRStatus
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
	closeFlow := opts.CloseFlow
	if closeFlow == nil {
		closeFlow = func(update flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.CloseFlow(update)
		}
	}
	reopenFlow := opts.ReopenFlow
	if reopenFlow == nil {
		reopenFlow = func(flowID string) (flowstore.FlowRecord, error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, err
			}
			return store.ReopenFlow(flowID)
		}
	}
	reserveFlowRepairLaunch := opts.ReserveFlowRepairLaunch
	if reserveFlowRepairLaunch == nil {
		reserveFlowRepairLaunch = func(flowID string) (flowstore.FlowRecord, func(), error) {
			store, err := newFlowStore()
			if err != nil {
				return flowstore.FlowRecord{}, nil, err
			}
			return store.ReserveRepairLaunch(flowID)
		}
	}
	reserveFlowLaunch := opts.ReserveFlowLaunch
	if reserveFlowLaunch == nil {
		if customPhaseLaunchPersistence {
			// A caller that replaces launch persistence owns its storage boundary;
			// opening the default store here would reserve a different backend.
			reserveFlowLaunch = func(flowID string) (flowstore.FlowRecord, func(), error) {
				return flowstore.FlowRecord{FlowID: flowID}, func() {}, nil
			}
		} else {
			reserveFlowLaunch = func(flowID string) (flowstore.FlowRecord, func(), error) {
				store, err := newFlowStore()
				if err != nil {
					return flowstore.FlowRecord{}, nil, err
				}
				return store.ReserveAgentLaunch(flowID)
			}
		}
	}
	createReserveFlowLaunch := reserveFlowLaunch
	if opts.ReserveFlowLaunch == nil && customPhaseLaunchPersistence {
		// Custom phase persistence may belong to a different backend, so its
		// compatibility reservation above deliberately owns no lock. That is safe
		// for legacy launch seams but cannot authorize preparation side effects.
		// Reuse an explicitly provided Store when it is the shared backend;
		// otherwise leave this nil so flowCreator refuses before persistence.
		createReserveFlowLaunch = nil
		if opts.FlowStore != nil {
			createReserveFlowLaunch = opts.FlowStore.ReserveAgentLaunch
		}
	}
	reserveFlowPreparation := createReserveFlowLaunch
	if reserveFlowPreparation == nil {
		reserveFlowPreparation = func(string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{}, nil, fmt.Errorf("authoritative Flow preparation reservation is unavailable")
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
	// Decorated once, here, before the one resolved value is handed both to
	// the launch seams and to the Model field: three callers publish launch IDs
	// (create, resume, and the tracked phase path that bypasses the seams), and
	// every one of them must leave baseline.json behind or replay has nothing
	// to compare against. A blank root makes the decorator a pass-through.
	addFlowPhaseLaunchID = launchcontrol.RecordBaseline(opts.SessionStateRoot, addFlowPhaseLaunchID)
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
	tmuxLaunchAvailable := opts.TmuxLaunchAvailable
	if tmuxLaunchAvailable == nil {
		tmuxLaunchAvailable = actions.TmuxAvailable
	}
	launchRepoTmuxAgent := opts.LaunchRepoTmuxAgent
	if launchRepoTmuxAgent == nil {
		launchRepoTmuxAgent = actions.RepoTmuxAgentLaunch
	}
	repoTmuxSessionExists := opts.RepoTmuxSessionExists
	if repoTmuxSessionExists == nil {
		repoTmuxSessionExists = actions.RepoTmuxSessionExists
	}
	repoTmuxLaunchWindowLive := opts.RepoTmuxLaunchWindowLive
	if repoTmuxLaunchWindowLive == nil {
		repoTmuxLaunchWindowLive = actions.RepoTmuxLaunchWindowLive
	}
	repoTmuxLaunchStatus := opts.RepoTmuxLaunchStatus
	if repoTmuxLaunchStatus == nil {
		repoTmuxLaunchStatus = actions.RepoTmuxLaunchWindowStatus
	}
	repoTmuxSessionAttached := opts.RepoTmuxSessionAttached
	if repoTmuxSessionAttached == nil {
		repoTmuxSessionAttached = actions.RepoTmuxSessionAttached
	}
	insideMultiplexer := opts.InsideMultiplexer
	if insideMultiplexer == nil {
		insideMultiplexer = actions.InsideMultiplexer
	}
	leaseInspectInjected := opts.InspectFlowLease != nil
	inspectFlowLease := opts.InspectFlowLease
	if inspectFlowLease == nil {
		inspectFlowLease = flowlease.Inspect
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
	allocateFlowID := func(title string) (string, error) {
		store, err := newFlowStore()
		if err != nil {
			return "", err
		}
		return store.AllocateID(title)
	}
	createFlow := func(record flowstore.FlowRecord, createOpts flowstore.CreateOptions) (flowstore.FlowRecord, error) {
		store, err := newFlowStore()
		if err != nil {
			return flowstore.FlowRecord{}, err
		}
		createOpts.Preset = opts.FlowPreset
		return store.CreateWithOptions(record, createOpts)
	}
	createPreparation := func(record flowstore.FlowRecord, createOpts flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
		store, err := newFlowStore()
		if err != nil {
			return flowstore.FlowRecord{}, nil, err
		}
		createOpts.Preset = opts.FlowPreset
		return store.CreatePreparation(record, createOpts)
	}
	setFlowStartMetadata := func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
		store, err := newFlowStore()
		if err != nil {
			return flowstore.FlowRecord{}, err
		}
		return store.SetStartMetadata(update)
	}
	creator := newFlowCreator(flowCreatorOptions{
		CreateFlow:           createFlow,
		CreatePreparation:    createPreparation,
		CreateWorktree:       actions.CreateFlowWorktree,
		SetStartMetadata:     setFlowStartMetadata,
		SetPhase:             setFlowPhase,
		ReserveLaunch:        createReserveFlowLaunch,
		ReadFlow:             readFlow,
		BootstrapHookForRepo: bootstrapHookForRepo,
		RunBootstrapHook:     runBootstrapHook,
		ResolveCommit:        actions.ResolveWorktreeCommit,
	})
	createFlowForRepo := opts.CreateFlow
	if createFlowForRepo == nil {
		createFlowForRepo = creator.Create
	}
	var ensureFlowWorktree func(flowstore.FlowRecord) (flowstore.FlowRecord, error)
	if !customPhaseLaunchPersistence {
		// Same storage boundary as reserveFlowLaunch above, and for a stronger
		// reason: the default seam runs real git and a real bootstrap hook, then
		// writes start metadata into the default store. Left nil, the launcher's
		// documented contract takes over and refuses worktree-less launches.
		ensureFlowWorktree = creator.EnsureWorktree
	}
	launchSeams := newFlowLaunchSeams(
		readFlow,
		readSession,
		listSessions,
		addFlowPhaseLaunchID,
		setFlowPhase,
		planMarkdownPath,
		readPlan,
	)
	launchSeams.AllocateFlowID = allocateFlowID
	launchSeams.CreateFlow = createFlow
	launchSeams.CreatePreparation = createPreparation
	launchSeams.ReserveLaunch = createReserveFlowLaunch
	launchSeams.EnsureWorktree = ensureFlowWorktree
	launchSeams.CreateWorktree = actions.CreateFlowWorktree
	launchSeams.ResolveCommit = actions.ResolveWorktreeCommit
	launchSeams.BootstrapHookForRepo = bootstrapHookForRepo
	launchSeams.RunBootstrapHook = runBootstrapHook
	launchSeams.SetStartMetadata = setFlowStartMetadata
	launchSeams.ReconcileEpicSuccessor = reconcileEpicSuccessor
	finalizeAgentSession := opts.FinalizeAgentSession
	if finalizeAgentSession == nil {
		finalizeAgentSession = func(actions.AgentLaunchContext) error { return nil }
	}
	topMode, bottomMode, contentPane, activeFlowSurface := startupPaneState(ui.ModeBeadsReady)
	m := Model{
		repos:                newRepoPane().SetItems(repos),
		rows:                 newBranchPane(),
		stashes:              newStashPane(),
		worktrees:            newWorktreePane(),
		worktreeSessions:     newSessionPane(),
		commits:              newCommitPane(),
		reflogs:              newReflogPane(),
		sessions:             newSessionPane(),
		plans:                newPlanPane(),
		flows:                newFlowPane(),
		activeFlows:          newFlowPane(),
		prBabysitterFlows:    newPRBabysitterFlowPane(nil),
		prBabysitterStatuses: make(map[string]actions.PullRequestStatus),
		beads:                newBeadSubviews(),
		embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible:   true,
			startEmbeddedTerminal: startEmbeddedTerminal,
		},
		autoAdvanceState: autoAdvanceState{
			autoAdvanceTickInterval: opts.AutoAdvanceTickInterval,
		},
		flowRefreshTickGen:      1,
		flowRefreshTickInterval: opts.FlowRefreshTickInterval,
		statusSchedule:          opts.StatusTimings,
		topMode:                 topMode,
		bottomMode:              bottomMode,
		contentPane:             contentPane,
		activeFlowSurface:       activeFlowSurface,
		takeover:                takeoverFromStartup(activeFlowSurface),
		agentConfig:             agentConfigFromOptions(opts),
		planPromptTemplate:      opts.PlanPromptTemplate,
		flowPromptTemplates:     opts.FlowPromptTemplates,
		repoCreateRoot:          opts.RepoCreateRoot,
		scanRepos:               opts.ScanRepos,
		createRepo:              createRepo,
		fetchRepo:               fetchRepo,
		listSessions:            listSessions,
		readTranscript:          readTranscript,
		listPlans:               listPlans,
		listFlows:               listFlows,
		listBeads: [beadSubviewCount]func(string) ([]beadsquery.Bead, error){
			listReadyBeads,
			listBlockedBeads,
			listOpenBeads,
			listInProgressBeads,
			listClosedBeads,
		},
		listChildrenBeads:         listChildrenBeads,
		listReadyBeads:            listReadyBeads,
		claimBead:                 claimBead,
		closeBead:                 closeBead,
		readEpicProgression:       readEpicProgression,
		setEpicProgression:        setEpicProgression,
		haltEpicProgression:       haltEpicProgression,
		enableEpicProgression:     enableEpicProgression,
		reconcileEpicSuccessor:    reconcileEpicSuccessor,
		showBead:                  showBead,
		countClosedBeads:          countClosedBeads,
		createFlow:                createFlowForRepo,
		setFlowPhase:              setFlowPhase,
		setFlowPhaseAgentSettings: setFlowPhaseAgentSettings,
		launchSeams:               launchSeams,
		setFlowAutoMode:           setFlowAutoMode,
		setFlowAutoMerge:          setFlowAutoMerge,
		setFlowHeadless:           setFlowHeadless,
		autoMerge:                 opts.FlowAutoMerge,
		autoMergePolicy:           newAutoMergePolicyGate(opts.FlowAutoMerge),
		saveFlowAutoMerge:         saveFlowAutoMerge,
		lookupPRStatus:            lookupPRStatus,
		lookupPRMerge:             lookupPRMerge,
		markFlowManualMerge:       markFlowManualMerge,
		closeFlow:                 closeFlow,
		reopenFlow:                reopenFlow,
		reserveFlowRepairLaunch:   reserveFlowRepairLaunch,
		reserveFlowLaunch:         reserveFlowLaunch,
		reserveFlowPreparation:    reserveFlowPreparation,
		addFlowPhaseLaunchID:      addFlowPhaseLaunchID,
		resetFlowPhase:            resetFlowPhase,
		deleteFlow:                deleteFlow,
		readPlan:                  readPlan,
		planMarkdownPath:          planMarkdownPath,
		copyToClipboard:           copyToClipboard,
		openCode:                  openCode,
		openURL:                   openURL,
		pageText:                  pageText,
		editFile:                  editFile,
		saveAgent:                 saveAgent,
		saveAgentModel:            saveAgentModel,
		saveAgentReasoningEffort:  saveAgentReasoningEffort,
		savePromptTemplate:        savePromptTemplate,
		resetPromptTemplate:       resetPromptTemplate,
		launchTerminal:            launchTerminal,
		launchDetachedTerminal:    launchDetachedTerminal,
		launchAgent:               launchAgent,
		launchBackend:             normalizeLaunchBackend(opts.LaunchBackend),
		tmuxLaunchAvailable:       tmuxLaunchAvailable,
		launchRepoTmuxAgent:       launchRepoTmuxAgent,
		repoTmuxSessionExists:     repoTmuxSessionExists,
		repoTmuxLaunchWindowLive:  repoTmuxLaunchWindowLive,
		repoTmuxLaunchStatus:      repoTmuxLaunchStatus,
		repoTmuxSessionAttached:   repoTmuxSessionAttached,
		insideMultiplexer:         insideMultiplexer,
		inspectFlowLease:          inspectFlowLease,
		leaseInspectInjected:      leaseInspectInjected,
		tmuxAttachHint:            normalizeLaunchBackend(opts.LaunchBackend) == config.LaunchBackendTmux && tmuxLaunchAvailable(),
		finalizeAgentSession:      finalizeAgentSession,
		sessionStateRoot:          opts.SessionStateRoot,
		launchPin:                 opts.LaunchPin,
		launchControl:             opts.LaunchControl,
		reconcileLaunchExit:       opts.ReconcileLaunchExit,
		sweepLaunches:             opts.SweepLaunches,
		notificationsEnabled:      opts.NotificationsEnabled,
		bootstrapHookForRepo:      bootstrapHookForRepo,
		runBootstrapHook:          runBootstrapHook,
	}
	if ui.IsGitMode(m.topMode) {
		m.lastGitMode = m.topMode
	}
	if index, ok := beadSubviewIndex(m.topMode); ok {
		m.lastBeadsMode = m.topMode
		if _, hasRepo := m.currentRepoPath(); hasRepo {
			m.beads[index].pending = true
		}
	}
	for mode := ui.ModeWorktrees; mode <= ui.ModeBeadsClosed; mode++ {
		m.listRequestSeq++
		m.listRequests[int(mode)] = m.listRequestSeq
	}
	if m.modeStored(ui.ModeFlows) {
		if _, ok := m.currentRepoPath(); ok {
			m.flowRefreshInFlight = m.currentListRequest(ui.ModeFlows)
			m.flowRefreshInFlightMode = ui.ModeFlows
		}
	}
	// setStatusNow rather than setStatus: construction has no command channel to
	// queue the expiry on, and the first real status replaces this one anyway.
	if notice := strings.TrimSpace(opts.LaunchPinNotice); notice != "" {
		m = m.setStatusNow(statusOther, notice)
	}
	return m
}

func startupPaneState(mode ui.Mode) (ui.Mode, ui.Mode, ui.Pane, bool) {
	topMode := ui.ModeWorktrees
	bottomMode := ui.ModeFlows
	contentPane := ui.PaneTop
	if pane, ok := ui.PaneForMode(mode); ok {
		contentPane = pane
		if pane == ui.PaneTop {
			topMode = mode
		} else {
			bottomMode = mode
		}
		return topMode, bottomMode, contentPane, false
	}
	if mode == ui.ModeActiveFlows {
		return topMode, bottomMode, ui.PaneBottom, true
	}
	return topMode, bottomMode, contentPane, false
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
func (m Model) Mode() ui.Mode              { return m.focusedMode() }
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
func (m Model) FormView() ui.FormView           { return uiFormView(m.modal.View().Form) }
func (m Model) ConfirmPrompt() string           { return m.modal.View().Prompt }
func (m Model) ConfirmForce() bool              { return m.modal.View().Force }
func (m Model) WorktreeInput() string           { return m.modal.View().Input }
func (m Model) InputMode() modal.InputMode      { return m.modal.View().InputMode }
func (m Model) InputCursor() int                { return m.modal.View().InputCursor }
func (m Model) WorktreeInputErr() string        { return m.modal.View().InputErr }
func (m Model) BranchScroll() int               { return m.rows.Scroll() }
func (m Model) RepoScroll() int                 { return m.repos.Scroll() }
func (m Model) StashScroll() int                { return m.stashes.Scroll() }
func (m Model) ActivePane() ui.Pane             { return m.activePane }
func (m Model) RepoPaneCollapsed() bool         { return m.repoPaneCollapsed }
func (m Model) Destructive() bool               { return m.destructive }
func (m Model) TransientError() string          { return m.visibleStatusText() }
func (m Model) TransientErrorFadeStep() int     { return m.visibleStatusFadeStep() }
func (m Model) SearchActive() bool              { return m.searchActive }
func (m Model) RepoSearch() string              { return m.repos.Query() }
func (m Model) ItemSearch() string              { return m.activeItemPaneQuery() }
func (m Model) ListRequest(mode ui.Mode) uint64 { return m.currentListRequest(mode) }
func (m Model) AgentCommand() string            { return m.agentCommand }
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
	case agent.CommandCursor:
		return m.cursorModel
	default:
		return ""
	}
}

// launchAgentSettings resolves the stored per-provider selections down to the
// single model and reasoning effort that apply to command.
func (m Model) launchAgentSettings(command string) agent.Settings {
	prefs := m.agentPreferences()
	prefs.Command = command
	return agent.Resolve(prefs)
}

func (m Model) agentPreferences() agent.Preferences {
	return agent.Preferences{
		Command:      m.agentCommand,
		CodexModel:   m.codexModel,
		ClaudeModel:  m.claudeModel,
		CursorModel:  m.cursorModel,
		CodexEffort:  m.codexReasoningEffort,
		ClaudeEffort: m.claudeReasoningEffort,
	}
}

func (m Model) flowLaunchAgentSettings() (string, string, string) {
	settings := m.launchAgentSettings(m.agentCommand)
	return settings.Command, settings.Model, settings.ReasoningEffort
}

func (m Model) flowModelLabel() string {
	settings, valid := m.effectiveSelectedFlowAgentSettings()
	if !valid {
		return "invalid"
	}
	command := settings.Command
	switch command {
	case agent.CommandCodex:
		return modelDisplay(settings.Model)
	case agent.CommandClaude:
		return strings.TrimPrefix(modelDisplay(settings.Model), "claude-")
	case agent.CommandCursor:
		return modelDisplay(settings.Model)
	default:
		return ""
	}
}

func (m Model) flowReasoningEffortLabel() string {
	settings, valid := m.effectiveSelectedFlowAgentSettings()
	if !valid {
		return "effort: invalid"
	}
	command := settings.Command
	switch command {
	case agent.CommandCodex, agent.CommandClaude:
		return fmt.Sprintf("effort: %s", reasoningEffortDisplay(settings.ReasoningEffort))
	default:
		return ""
	}
}

func (m Model) flowAgentShortcutLabel() string {
	settings, valid := m.effectiveSelectedFlowAgentSettings()
	if !valid {
		return "invalid settings"
	}
	switch command := settings.Command; command {
	case agent.CommandCodex, agent.CommandClaude, agent.CommandCursor:
		return command
	default:
		return "choose agent"
	}
}

func (m Model) effectiveSelectedFlowAgentSettings() (agent.Settings, bool) {
	if m.flowPhaseAgentControlsSelected() {
		if phase, ok := m.selectedFlowPhase(); ok {
			settings, err := flowstore.ResolvePhaseAgentSettings(m.agentPreferences(), phase.AgentSettings())
			if err != nil {
				return agent.Settings{}, false
			}
			return settings, true
		}
	}
	return m.launchAgentSettings(m.agentCommand), true
}

func (m Model) flowPhaseAgentControlsSelected() bool {
	if !m.flowSurfaceVisible() || m.activePane == ui.PaneRepos {
		return false
	}
	_, ok := m.selectedFlowPhase()
	return ok
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
	case agent.CommandCursor:
		m.cursorModel = model
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
	return batchNonNil(m.fetchStoredModes(), m.autoAdvanceTickCmd(), m.launchSweepTickCmd())
}

func (m Model) View() tea.View {
	mode := m.focusedMode()
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
	beadsAvailable, beadsPending, beadsError, beadsQuery := false, false, "", ""
	beadsSourceCount, beadsClosedTotal := 0, 0
	if state, ok := m.beadSubview(m.topMode); ok {
		beadsActive, beadsSelected, beadsScroll = state.pane.View()
		beadsAvailable = state.available
		beadsPending = state.pending
		beadsError = state.error
		beadsQuery = state.pane.Query()
		beadsSourceCount = state.pane.ItemCount()
		if m.topMode == ui.ModeBeadsClosed {
			beadsClosedTotal = state.total
		}
	}
	if m.activeFlowSurfaceVisible() {
		flows, flowSelected, flowScroll = m.activeFlows.View()
	}
	if m.prBabysitterSurfaceVisible() {
		flows, flowSelected, flowScroll = m.prBabysitterFlows.View()
	}
	flowAutoModeSelected := false
	var flowAutoMergeSelected *bool
	if flowSelected >= 0 && flowSelected < len(flows) {
		flowAutoModeSelected = flows[flowSelected].AutoMode
		flowAutoMergeSelected = flows[flowSelected].AutoMerge
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
		beadsAvailable = false
		beadsPending = false
		beadsSourceCount = 0
		beadsClosedTotal = 0
	}
	modalView := m.modal.View()
	content := ui.Render(ui.RenderParams{
		Repos:                        repos,
		ActiveTerminalRepoPaths:      m.activeTerminalRepoPaths(),
		Selected:                     selected,
		Width:                        m.width,
		Height:                       m.height,
		Mode:                         mode,
		TopMode:                      m.topMode,
		BottomMode:                   m.bottomMode,
		ContentPane:                  m.contentPane,
		Branches:                     rows,
		Stashes:                      stashes,
		BranchSelected:               branchSelected,
		StashSelected:                stashSelected,
		BranchScroll:                 branchScroll,
		RepoScroll:                   repoScroll,
		StashScroll:                  stashScroll,
		ActivePane:                   m.activePane,
		RepoPaneCollapsed:            m.repoPaneCollapsed,
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
		Plans:                        plans,
		PlanSelected:                 planSelected,
		PlanScroll:                   planScroll,
		BeadsOpen:                    beadsActive,
		BeadsOpenSelected:            beadsSelected,
		BeadsOpenScroll:              beadsScroll,
		BeadsOpenAvailable:           beadsAvailable,
		BeadsOpenPending:             beadsPending,
		ReadyBeadFlowCreateAvailable: m.canCreateReadyBeadFlow(),
		ReadyBeadFlowStartAvailable:  m.canStartReadyBeadFlow(),
		ReadyBeadFlowKeysOwned:       m.readyBeadFlowKeysOwned(),
		BeadSliceEpicAvailable:       m.canSliceSelectedEpic(),
		EpicAutoOnAvailable:          m.canEnableEpicProgression(),
		EpicAutoOffAvailable:         m.canDisableEpicProgression(),
		EpicAutoKeyOwned:             m.epicProgressionKeysOwned(),
		BeadsError:                   beadsError,
		BeadsQuery:                   beadsQuery,
		BeadsSourceCount:             beadsSourceCount,
		BeadsClosedTotal:             beadsClosedTotal,
		BeadExpansion:                cloneBeadExpansion(m.beadExpansion.projection),
		ExpandedPlanID:               m.expandedPlanID,
		SelectedPlanPhaseID:          m.selectedPlanPhaseID,
		TransientError:               m.visibleStatusText(),
		TransientErrorFadeStep:       m.visibleStatusFadeStep(),
		SearchActive:                 m.searchActive,
		RepoSearch:                   m.repos.Query(),
		ItemSearch:                   m.activeItemPaneQuery(),
		ItemSourceCount:              m.activeItemPaneSourceCount(),
		TopItemSearch:                m.itemPaneQuery(m.topMode),
		BottomItemSearch:             m.itemPaneQuery(m.bottomMode),
		TopItemSourceCount:           m.itemPaneSourceCount(m.topMode),
		BottomItemSourceCount:        m.itemPaneSourceCount(m.bottomMode),
		RepoEmptyMessage:             repoEmptyMessage,
		RightEmptyMessage:            rightEmptyMessage,
		TopListError:                 m.currentListError(m.topMode),
		BottomListError:              m.currentListError(m.bottomMode),
		TopDegradationWarning:        m.gitDegradationWarning(m.topMode),
		BottomDegradationWarning:     m.gitDegradationWarning(m.bottomMode),
		FetchAvailable:               m.canFetch(),
		FetchVisibleAvailable:        m.canFetchVisibleRepos(),
		RepoCreateAvailable:          m.canCreateRepo(),
		PullAvailable:                m.canPull(),
		WorktreeMoveAvailable:        m.canMoveWorktree(),
		WorktreeSessionsOpen:         m.inlineWorktreeSessionPath != "",
		AgentAvailable:               m.canLaunchAgent(),
		NewAgentAvailable:            m.canCreateAndLaunchAgent(),
		TmuxAttachAvailable:          m.tmuxModeAttachAvailable(),
		OverlayParams: ui.OverlayParams{
			Overlay:                  m.overlayState(),
			OverlayDiff:              modalView.Diff,
			OverlayScroll:            modalView.Scroll,
			ConfirmPrompt:            modalView.Prompt,
			ConfirmForce:             modalView.Force,
			InputPrompt:              modalView.Prompt,
			InputPlaceholder:         modalView.Placeholder,
			InputValue:               modalView.Input,
			InputError:               modalView.InputErr,
			InputMode:                uiInputMode(modalView.InputMode),
			InputHeight:              modalView.InputHeight,
			InputCursor:              modalView.InputCursor,
			Editor:                   uiEditorParams(modalView.Editor),
			WorktreeInputPrompt:      modalView.Prompt,
			WorktreeInputPlaceholder: modalView.Placeholder,
			WorktreeInput:            modalView.Input,
			WorktreeInputErr:         modalView.InputErr,
			SelectPrompt:             modalView.Prompt,
			SelectItems:              uiSelectItems(modalView.SelectItems),
			SelectSelected:           modalView.SelectIndex,
			SelectWidth:              modalView.SelectLayout.Width,
			SelectHeight:             modalView.SelectLayout.Height,
			SelectNote:               modalView.SelectNote,
			SelectNoteKind:           uiNoteKind(modalView.SelectNoteKind),
			SelectPlacement:          uiSelectPlacement(modalView.SelectLayout.Placement),
			Form:                     uiFormView(modalView.Form),
			OverlayText:              modalView.Text,
		},
		FlowParams: ui.FlowParams{
			ActiveFlows:                    m.activeFlowSurfaceVisible(),
			PRBabysitter:                   m.prBabysitterSurfaceVisible(),
			Flows:                          flows,
			PRBabysitterRows:               m.prBabysitterRows(flows),
			FlowSelected:                   flowSelected,
			FlowScroll:                     flowScroll,
			FlowDegradationWarning:         m.flowDegradationWarning(ui.ModeFlows),
			ActiveFlowDegradationWarning:   m.flowDegradationWarning(ui.ModeActiveFlows),
			PRBabysitterDegradationWarning: m.flowDegradationWarning(ui.ModePRBabysitter),
			ExpandedFlowID:                 m.currentExpandedFlowID(),
			SelectedFlowPhaseID:            m.currentSelectedFlowPhaseID(),
			FlowHeadless:                   m.selectedFlowHeadless(),
			FlowAutoModeSelected:           flowAutoModeSelected,
			FlowAutoMergeSelected:          flowAutoMergeSelected,
			GlobalAutoMerge:                m.autoMerge,
			FlowIssueTargetSelected:        flowIssueTargetSelected,
			FlowPRTargetSelected:           flowPRTargetSelected,
			FlowAgentLabel:                 m.flowAgentShortcutLabel(),
			FlowModel:                      m.flowModelLabel(),
			FlowReasoningEffort:            m.flowReasoningEffortLabel(),
			FlowNextLaunchReady:            m.selectedFlowHasLaunchablePhase(),
			FlowWorktreeAgentReady:         m.selectedFlowWorktreeAgentReady(),
			FlowRepairReady:                m.selectedFlowRepairReady(),
			FlowManualMergeReadySelected:   m.selectedFlowManualMergeReady(),
			FlowAutofixReadySelected:       m.selectedFlowAutofixReady(),
			FlowCloseActionSelected:        m.selectedFlowCloseActionHint(),
			FlowPhaseResetReadySelected:    m.selectedFlowPhaseResettable(),
			FlowPhaseReleaseSelected:       m.selectedFlowPhaseSessionReleasable(),
			FlowPhaseResumableSelected:     m.selectedFlowPhaseResumable(),
			ActiveFlowsListError:           m.currentListError(ui.ModeActiveFlows),
			PRBabysitterListError:          m.currentListError(ui.ModePRBabysitter),
		},
		EmbeddedTerminalParams: ui.EmbeddedTerminalParams{
			EmbeddedTerminals:       m.embeddedTerminalTabs(),
			EmbeddedTerminalLines:   m.embeddedTerminalLines(),
			EmbeddedTerminalPrefix:  m.terminalPrefixActive,
			EmbeddedTerminalVisible: m.terminalDockVisible,
			EmbeddedTerminalFocused: m.terminalEffectivelyExpanded() && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal,
			FlowTerminalActivity:    m.flowTerminalActivity()}})
	view := tea.NewView(content)
	view.AltScreen = true
	return view
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
	mode := m.focusedMode()
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
			return "loading " + beadsModeName(mode) + " beads"
		}
		if state.error != "" {
			return "Could not load " + beadsModeName(mode) + " beads: " + state.error
		}
	}
	sourceCount, filteredCount := m.activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows, filteredBeadsOpen)
	if m.activeItemPaneQuery() != "" && sourceCount > 0 && filteredCount == 0 {
		if m.takeoverVisible() {
			return "No flow results for " + m.activeItemPaneQuery()
		}
		return "No " + modeResultName(mode) + " results for " + m.activeItemPaneQuery()
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
	if m.prBabysitterSurfaceVisible() {
		if m.prBabysitterCancel != nil && m.prBabysitterFlows.Len() == 0 {
			return "loading PR babysitter"
		}
		return "No PRs awaiting merge"
	}
	if state, ok := m.activeBeadSubview(); ok {
		if state.available {
			return "no " + beadsModeName(mode) + " beads"
		}
		return "beads not configured"
	}
	return modeEmptyMessage(mode)
}

func (m Model) activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs, filteredSessions, filteredPlans, filteredFlows, filteredBeadsOpen int) (int, int) {
	if m.prBabysitterSurfaceVisible() {
		return m.prBabysitterFlows.ItemCount(), filteredFlows
	}
	if m.activeFlowSurfaceVisible() {
		return m.activeFlows.ItemCount(), filteredFlows
	}
	switch m.focusedMode() {
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
	case ui.ModePRBabysitter:
		return m.prBabysitterFlows.ItemCount(), filteredFlows
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
	case ui.ModePRBabysitter:
		return "PR babysitter"
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
	case ui.ModePRBabysitter:
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
	case ui.ModeActiveFlows:
		return "No active flows"
	case ui.ModePRBabysitter:
		return "No PRs awaiting merge"
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

// uiEditorParams and uiNoteKind are the single conversion point between the
// canonical modal enums and ui's parallel declarations.
func uiEditorParams(editor modal.EditorView) ui.EditorParams {
	return ui.EditorParams{
		Enabled:      editor.Enabled,
		Title:        editor.Title,
		Identity:     editor.Identity,
		Note:         editor.Note,
		NoteKind:     uiNoteKind(editor.NoteKind),
		Dirty:        editor.Dirty,
		EmptyWarning: editor.EmptyWarning,
	}
}

func uiNoteKind(kind modal.NoteKind) ui.NoteKind {
	switch kind {
	case modal.NoteSuccess:
		return ui.NoteSuccess
	case modal.NoteWarning:
		return ui.NoteWarning
	case modal.NoteError:
		return ui.NoteError
	default:
		return ui.NoteNeutral
	}
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
	flowRefreshWasVisible := m.flowRefreshSurfaceVisible()
	defer func() {
		modelNext, ok := next.(Model)
		if !ok {
			return
		}
		if !flowRefreshWasVisible && modelNext.flowRefreshSurfaceVisible() && modelNext.flowRefreshInFlight == 0 {
			var refreshCmd tea.Cmd
			modelNext, refreshCmd = modelNext.startFlowSurfaceRefreshFetch()
			cmd = batchNonNil(cmd, refreshCmd)
		}
		modelNext, cmd = modelNext.drainStatusCmds(cmd)
		if modelNext.quitAfterFlowLaunch && modelNext.flowLaunchOwnershipCount() == 0 {
			shutdownCmd := tea.Quit
			if modelNext.interruptAfterFlowLaunch {
				shutdownCmd = func() tea.Msg { return tea.Interrupt() }
			}
			cmd = batchNonNil(cmd, shutdownCmd)
		}
		next = modelNext
	}()
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg.Content)
	case flowLaunchQuitRequestedMsg:
		m.quitAfterFlowLaunch = true
		m.interruptAfterFlowLaunch = msg.Interrupt
		return m.setStatus(statusOther, flowLaunchQuitPendingStatus), nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.terminalEffectivelyExpanded() && m.terminalFocus == terminalFocusTerminal {
			m.activePane = m.contentPane
			m.terminalFocus = terminalFocusList
			m.terminalPrefixActive = false
		}
		m = m.resizeEmbeddedTerminals()
		m = m.reflowForTerminalDock()
		m = m.clampSelectionsAfterFilter()
	case embeddedSessionPickerSelectedMsg:
		return m.handleEmbeddedSessionPickerSelected(msg)
	case embeddedSessionPickerLoadedMsg:
		return m.handleEmbeddedSessionPickerLoaded(msg)
	case terminateEmbeddedTerminalMsg:
		return m.handleTerminateEmbeddedTerminal(msg)
	case quitEmbeddedTerminalsMsg:
		return m.handleQuitEmbeddedTerminals()
	case embeddedPromptPrefillResultMsg:
		if !m.hasEmbeddedTerminalID(msg.ID) {
			if msg.Create != nil {
				if msg.Err == nil {
					msg.Err = fmt.Errorf("embedded terminal closed before prompt prefill completed")
				}
				return m.handleFlowLaunchPrefillFailure(msg)
			}
			return m, nil
		}
		if msg.Create != nil && !m.createFlowLaunchOriginCurrent(*msg.Create) {
			cancelErr := fmt.Errorf("creation canceled after repository changed")
			if msg.Err != nil {
				// A failed prefill already attempted termination in its command.
				// Preserve that result rather than terminating the same process twice.
				msg.Err = errors.Join(cancelErr, msg.Err)
			} else if terminateErr := m.terminateEmbeddedTerminalByID(msg.ID); terminateErr != nil {
				msg.Err = errors.Join(cancelErr, fmt.Errorf("terminate embedded terminal after canceled prefill: %w", terminateErr))
				msg.RetainTerminal = true
			} else {
				msg.Err = cancelErr
			}
			return m.handleFlowLaunchPrefillFailure(msg)
		}
		if msg.Err != nil {
			return m.handleFlowLaunchPrefillFailure(msg)
		}
		if msg.Create != nil {
			m = m.clearFlowLaunchCreatePresentation(*msg.Create)
		}
		m = m.activateEmbeddedTerminal(msg.ID)
		return m.updateFlowTerminalFocusAfterLaunch(msg.LaunchContext), nil
	case embeddedTerminalTickMsg:
		if msg.Generation != m.embeddedTerminalTickGen {
			return m, nil
		}
		// Before dismissal, because a dismissed slot is gone from the model
		// and its exit would never be reported.
		var cmds []tea.Cmd
		var notificationCmds []tea.Cmd
		m, notificationCmds = m.collectEmbeddedExitNotifications()
		cmds = append(cmds, notificationCmds...)
		var reconcileCmds []tea.Cmd
		m, reconcileCmds = m.reconcileExitedFlowEmbeddedTerminals()
		cmds = append(cmds, reconcileCmds...)
		hadExitedFlowTerminals := m.hasExitedFlowEmbeddedTerminalAutoClose()
		m = m.dismissExitedFlowEmbeddedTerminals()
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
		if msg.Generation != m.flowRefreshTickGen || !m.flowRefreshSurfaceVisible() {
			return m, nil
		}
		return m.startFlowSurfaceRefreshFetch()
	case FlowControlAppliedMsg:
		return m.startFlowSurfaceRefreshFetch()
	case launchSweepTickMsg:
		if msg.Generation != m.launchSweepTickGen {
			return m, nil
		}
		return m.startLaunchSweep()
	case launchSweepDoneMsg:
		return m.handleLaunchSweepDone(msg)
	case launchExitReconcileDoneMsg:
		return m.handleLaunchExitReconcileDone(msg)
	case prBabysitterPollMsg:
		if !m.prBabysitterSurfaceVisible() || msg.Generation != m.currentListRequest(ui.ModePRBabysitter) {
			return m, nil
		}
		return m.startPRBabysitterRefresh()
	case autoAdvanceTickMsg:
		return m.startAutoAdvanceFetch()
	case AutoAdvanceResultMsg:
		return m.handleAutoAdvanceResult(msg)
	case epicProgressionAdvanceResultMsg:
		return m.handleEpicProgressionAdvanceResult(msg)
	case epicProgressionHaltResultMsg:
		return m.handleEpicProgressionHaltResult(msg)
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
	case PRBabysitterResultMsg:
		return m.handlePRBabysitterResult(msg)
	case BeadsReadyResultMsg:
		next, accepted := m.handleBeadsResult(ui.ModeBeadsReady, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error)
		if !accepted {
			return next, nil
		}
		return next.reconcileBeadExpansion()
	case BeadsBlockedResultMsg:
		next, accepted := m.handleBeadsResult(ui.ModeBeadsBlocked, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error)
		if !accepted {
			return next, nil
		}
		return next.reconcileBeadExpansion()
	case BeadsOpenResultMsg:
		next, accepted := m.handleBeadsOpenResult(msg)
		if !accepted {
			return next, nil
		}
		return next.reconcileBeadExpansion()
	case BeadsInProgressResultMsg:
		next, accepted := m.handleBeadsResult(ui.ModeBeadsInProgress, msg.RepoPath, msg.Beads, msg.ListRequest, msg.Available, msg.Error)
		if !accepted {
			return next, nil
		}
		return next.reconcileBeadExpansion()
	case BeadsClosedResultMsg:
		next, accepted := m.handleBeadsClosedResult(msg)
		if !accepted {
			return next, nil
		}
		return next.reconcileBeadExpansion()
	case beadExpansionResultMsg:
		return m.handleBeadExpansionResult(msg), nil
	case beadProgressionResultMsg:
		return m.handleBeadProgressionResult(msg), nil
	case epicProgressionToggleResultMsg:
		return m.handleEpicProgressionToggleResult(msg)
	case BeadDetailResultMsg:
		return m.handleBeadDetailResult(msg)
	case FlowAutoModeSetMsg:
		return m.handleFlowAutoModeSet(msg), nil
	case FlowAutoModeSetFailedMsg:
		return m.handleFlowAutoModeSetFailed(msg), nil
	case FlowAutoMergeSetMsg:
		return m.handleFlowAutoMergeSet(msg)
	case FlowAutoMergeSetFailedMsg:
		return m.handleFlowAutoMergeSetFailed(msg)
	case GlobalAutoMergeSetMsg:
		return m.handleGlobalAutoMergeSet(msg)
	case GlobalAutoMergeSetFailedMsg:
		return m.handleGlobalAutoMergeSetFailed(msg)
	case FlowHeadlessSetMsg:
		return m.handleFlowHeadlessSet(msg)
	case FlowHeadlessSetFailedMsg:
		return m.handleFlowHeadlessSetFailed(msg), nil
	case FlowManualMergeSetMsg:
		return m.handleFlowManualMergeSet(msg), nil
	case FlowManualMergeSetFailedMsg:
		return m.handleFlowManualMergeSetFailed(msg), nil
	case FlowClosedMsg:
		return m.handleFlowClosed(msg), nil
	case FlowCloseFailedMsg:
		return m.handleFlowCloseFailed(msg), nil
	case FlowReopenedMsg:
		return m.handleFlowReopened(msg), nil
	case FlowReopenFailedMsg:
		return m.handleFlowReopenFailed(msg), nil
	case flowPhaseResetConfirmedMsg:
		return m.handleFlowPhaseResetConfirmed(msg)
	case flowPhaseResetMsg:
		return m.handleFlowPhaseReset(msg)
	case flowPhaseResetFailedMsg:
		return m.handleFlowPhaseResetFailed(msg)
	case flowPhaseSessionReleaseProbedMsg:
		return m.handleFlowPhaseSessionReleaseProbed(msg)
	case flowPhaseSessionReleaseConfirmedMsg:
		return m.handleFlowPhaseSessionReleaseConfirmed(msg)
	case flowPhaseSessionReleasedMsg:
		return m.handleFlowPhaseSessionReleased(msg)
	case flowPhaseSessionReleaseFailedMsg:
		return m.handleFlowPhaseSessionReleaseFailed(msg)
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
	case repoTmuxTerminalOpenedMsg:
		return m.applyRepoTmuxTerminalOpened(msg), nil
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
		if m.modeStored(ui.ModePlans) {
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
	case FlowPhaseAgentSettingsSetMsg:
		return m.handleFlowPhaseAgentSettingsSet(msg), nil
	case FlowPhaseAgentSettingsSetFailedMsg:
		return m.handleFlowPhaseAgentSettingsSetFailed(msg), nil
	case promptTemplateEditRequestedMsg:
		return m.handlePromptTemplateEditRequested(msg), nil
	case promptTemplatePickerReturnMsg:
		return m.handlePromptTemplatePickerReturn(msg), nil
	case PromptTemplateSavedMsg:
		return m.handlePromptTemplateSaved(msg), nil
	case PromptTemplateSaveFailedMsg:
		return m.handlePromptTemplateSaveFailed(msg), nil
	case PromptTemplateResetMsg:
		return m.handlePromptTemplateReset(msg), nil
	case PromptTemplateResetFailedMsg:
		return m.handlePromptTemplateResetFailed(msg), nil
	case agentLaunchRequestedMsg:
		// Saved-plan launches verify before the instructions modal opens, then
		// emit this message later. Re-check at spawn so a pin that vanished or
		// was replaced while the modal was open cannot start an agent.
		if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
			return m.setStatus(statusOther, refusal), nil
		}
		return m.launchAgentForBackend(msg.LaunchContext, nil)
	case FlowCreatedMsg:
		return m.handleFlowCreated(msg)
	case FlowCreateFailedMsg:
		return m.handleFlowCreateFailed(msg)
	case ReadyBeadFlowCreatedMsg:
		return m.handleReadyBeadFlowCreated(msg)
	case ReadyBeadFlowCreateFailedMsg:
		return m.handleReadyBeadFlowCreateFailed(msg)
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
	case flowLaunchEventMsg:
		return m.handleFlowLaunchEvent(msg)
	case flowLaunchCreateRequestedMsg:
		intent := flowLaunchIntent{
			Kind: flowLaunchKindCreatePhase, Origin: msg.Create.Presentation.Origin,
			Create: msg.Create, Settings: msg.Settings,
		}
		next, cmd, _ := m.requestFlowLaunch(intent)
		return next, cmd
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
		if next.flowSurfaceVisible() && (next.takeoverVisible() || next.isCurrentRepo(msg.RepoPath)) {
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
	ctx := msg.LaunchContext
	var interactiveExitCmd tea.Cmd
	if !msg.Detached {
		state, codeKnown := "exited", true
		if msg.Err != "" {
			state, codeKnown = "failed", false
		}
		interactiveExitCmd = m.notificationCmd(agentExitNotification(ctx.Command, ctx.RepoPath, state, 0, codeKnown))
	}
	// A slice-epic launch holds its preparation admission across the spawn, and
	// its own result is what ends the hold. The exact LaunchID match is what
	// stops a stale result — from a launch made before a repository or
	// selection change — from releasing a newer fence or reporting against a
	// different epic.
	sliceRecord, slicedLaunch := m.sliceEpicLaunchFor(ctx.LaunchID)
	m = m.releaseSliceEpicLaunch(ctx.LaunchID)
	// Only a lifecycle handoff releases here; every other source funnels
	// through this handler with no attempt and must reach main's behaviour
	// below unchanged.
	if attempt, ok := m.matchingFlowLaunchAttempt(ctx.FlowID, ctx.LaunchID, 0, flowLaunchStateHandoffPending); ok {
		releaseFlowLaunchReservation(msg.FlowLaunchRelease)
		if resultErr != "" {
			m, cmd := m.failFlowLaunch(attempt, ctx, ctx.RepoPath, resultErr)
			return m, batchNonNil(interactiveExitCmd, cmd)
		}
		m = m.releaseFlowLaunchAttempt(ctx.FlowID, ctx.LaunchID)
	} else if msg.FlowLaunchRelease != nil {
		// A stale or duplicate lifecycle result is fenced completely: it cannot
		// release a reservation now owned by a newer attempt, persist failure, or
		// fall through to generic detached-launch status handling.
		return m, nil
	}
	if resultErr != "" {
		m, cmd := m.startFlowLaunchFailure(msg.LaunchContext, resultErr)
		return m, batchNonNil(interactiveExitCmd, cmd)
	} else if msg.Detached {
		if msg.Tmux {
			m = m.withTmuxNotificationWatch(ctx)
		}
		// Only detached launches carry a status here; an interactive launch's own
		// status is set before the TTY handover, since this message lands when the
		// user's session ends rather than when it starts.
		status := msg.LaunchedStatus
		if strings.TrimSpace(status) == "" {
			// A carried status is load-bearing on the tmux route (it is how the
			// user learns the attach command) and on the tmux-unavailable
			// fallback, so the slice status replaces only this fallback.
			if slicedLaunch {
				status = sliceEpicLaunchedStatus(ctx.Command, sliceRecord.EpicID)
			} else {
				status = agentLaunchedStatus(ctx.Command)
			}
		}
		m = m.setStatus(statusOther, status)
	} else if cmd := m.reconcileInteractiveLaunchExitCmd(ctx); cmd != nil {
		// An interactive launch's agent has ended with the TTY handed back; a
		// tracked phase it left running has no result and is reconciled the
		// same way an embedded terminal's exit is.
		return m, batchNonNil(interactiveExitCmd, cmd)
	} else if !msg.Detached {
		return m, interactiveExitCmd
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
	if m.prBabysitterSurfaceVisible() {
		return m.prBabysitterFlows.Selected()
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

func (m Model) selectedFlowHeadless() bool {
	record, ok := m.selectedFlow()
	return ok && record.Headless
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
	// Unlike selectedFlowPhaseResettable, this accessor has no record in scope,
	// so the closed-Flow gate needs its own lookup to keep the r hint in step
	// with the handler. The record is bound rather than scoped to the gate
	// because the occupancy preview below needs its Flow ID.
	record, ok := m.selectedFlow()
	if !ok || flowstore.FlowClosed(record) {
		return false
	}
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
	provider := agent.Normalize(strings.TrimSpace(session.Provider))
	if agent.Validate(provider) != nil {
		return false
	}
	return m.previewPhaseResume(record.FlowID)
}

func flowPhaseHasRecoverableRunningSession(phase flowstore.FlowPhase) bool {
	_, ok := flowstore.RecoverableRunningPhaseResetReason(phase)
	return ok
}

func flowPhaseHasStaleRunningLatestLaunch(phase flowstore.FlowPhase) bool {
	return phase.Status == flowstore.PhaseRunning &&
		(flowstore.PhaseAwaitingSession(phase) || flowstore.PhaseLatestLaunchEnded(phase))
}

// selectedFlowHasLaunchablePhase drives footer availability through the same
// preview admission uses, so what the footer advertises and what g accepts can
// never disagree.
func (m Model) selectedFlowHasLaunchablePhase() bool {
	record, ok := m.selectedFlow()
	if !ok {
		return false
	}
	_, _, ok = m.cachedFlowLaunchTarget(flowLaunchIntent{
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID,
	})
	if !ok {
		return false
	}
	return !m.trackedPhaseOccupancy(record.FlowID, flowownership.StageFooter).Occupied()
}

// withFlowAutofixTmuxLaunch appends an autofix tmux launch to the
// ones already retained for a Flow. It clones the map and the Flow's own slice
// rather than mutating either, like every other per-Flow map on the value-typed
// Model, so a copy taken before the write is unaffected — appending in place
// could otherwise write through a shared backing array into an older copy.
//
// A launch already retained is not appended twice: the same token can reach a
// handoff more than once, and a duplicate would only widen the probe's argument
// list without changing its answer.
func (m Model) withFlowAutofixTmuxLaunch(flowID, launchID string) Model {
	flowID = strings.TrimSpace(flowID)
	launchID = strings.TrimSpace(launchID)
	if flowID == "" || launchID == "" {
		return m
	}
	if slices.Contains(m.flowAutofixTmuxLaunches[flowID], launchID) {
		return m
	}
	launches := make(map[string][]string, len(m.flowAutofixTmuxLaunches)+1)
	for existingFlowID, existingLaunchIDs := range m.flowAutofixTmuxLaunches {
		launches[existingFlowID] = existingLaunchIDs
	}
	launches[flowID] = append(slices.Clone(launches[flowID]), launchID)
	m.flowAutofixTmuxLaunches = launches
	return m
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

func (m Model) selectedFlowPhaseSessionReleasable() bool {
	record, ok := m.selectedFlow()
	if !ok {
		return false
	}
	phase, ok := m.selectedFlowPhase()
	return ok && m.flowPhaseSessionReleasable(record, phase)
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
	if m.prBabysitterSurfaceVisible() {
		return m.prBabysitterRecords
	}
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
	requested := phaseID
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
	if m.prBabysitterSurfaceVisible() {
		m.selectedPRBabysitterPhaseID = ""
		return m
	}
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
	if m.prBabysitterSurfaceVisible() {
		m.expandedPRBabysitterFlowID = flowID
		m.selectedPRBabysitterPhaseID = ""
		m.prBabysitterFlows = m.prBabysitterFlows.SetItemHeight(flowItemHeight(flowID))
		return m.reflowPRBabysitter()
	}
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

	viewHeight := m.flowSurfaceContentHeight()
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
	if m.focusedMode() != ui.ModePlans || m.expandedPlanID == "" || m.selectedPlanID() != m.expandedPlanID {
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
	viewHeight := m.flowSurfaceContentHeight()
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
	m.stashes = m.stashes.Reflow(m.contentHeightForMode(ui.ModeStashes), m.contentWidth())
	return m
}

func (m Model) reflowRepos() Model {
	m.repos = m.repos.Reflow(m.repoContentHeight(), ui.LeftPaneWidth-2)
	return m
}

func (m Model) reflowWorktrees() Model {
	contentHeight := m.worktreeContentHeight()
	m.worktrees = m.worktrees.Reflow(contentHeight, m.contentWidth())
	m = m.reflowWorktreeSessions()
	return m
}

func (m Model) reflowWorktreeSessions() Model {
	m.worktreeSessions = m.worktreeSessions.Reflow(m.worktreeSessionContentHeight(), m.contentWidth())
	return m
}

func (m Model) reflowReflogs() Model {
	contentHeight := m.contentHeightForMode(ui.ModeReflog)
	m.reflogs = m.reflogs.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowSessions() Model {
	m.sessions = m.sessions.Reflow(m.contentHeightForMode(ui.ModeSessions), m.contentWidth())
	return m
}

func (m Model) reflowPlans() Model {
	m.plans = m.plans.Reflow(m.contentHeightForMode(ui.ModePlans), m.contentWidth())
	if m.selectedPlanPhaseID != "" {
		return m.ensureSelectedPlanPhaseVisible()
	}
	return m
}

func (m Model) reflowFlows() Model {
	m.flows = m.flows.Reflow(m.contentHeightForMode(ui.ModeFlows), m.contentWidth())
	if m.takeoverVisible() {
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
	contentHeight := m.contentHeightForMode(ui.ModeHistory)
	m.commits = m.commits.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowBranches() Model {
	contentHeight := m.contentHeightForMode(ui.ModeBranches)
	m.rows = m.rows.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowBeads(mode ui.Mode) Model {
	index, ok := beadSubviewIndex(mode)
	if !ok {
		return m
	}
	m.beads[index].pane = m.beads[index].pane.Reflow(m.contentHeightForMode(mode), m.contentWidth())
	return m
}

func (m Model) reflowAllBeads() Model {
	for mode := ui.ModeBeadsReady; mode <= ui.ModeBeadsClosed; mode++ {
		m = m.reflowBeads(mode)
	}
	return m
}

func (m Model) contentWidth() int {
	repoPaneWidth := ui.LeftPaneWidth
	if m.repoPaneCollapsed {
		repoPaneWidth = ui.CollapsedRepoPaneWidth
	}
	width := m.width - repoPaneWidth - 2
	if width < 0 {
		return 0
	}
	return width
}
