// Package flowstore persists task-centric Flow records beside the agent-session store.
//
// Agents persist Flow changes through the `approach flow` CLI
// (cmd/approach/flow.go), never by editing approach.db — direct edits bypass
// the store's transactions, validation, and phase-ID normalization.
package flowstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/internal/artifacts"
)

const schemaVersion = 1

const defaultLockTimeout = 5 * time.Second

// ErrFlowNotFound is the sentinel every missing-record error wraps. It is
// exported alongside IsNotFound so callers and their test doubles can build the
// same error the store returns.
var ErrFlowNotFound = errors.New("flow not found")

var errFlowNotFound = ErrFlowNotFound

// PartialListEntry identifies one authoritative Flow row that could be scanned
// but not decoded while listing. Cause retains the row-local corruption error.
type PartialListEntry struct {
	FlowID string
	Cause  error
}

// DiagnosticFlowID renders the authoritative row ID without allowing corrupt
// control bytes or an ambiguous blank value to reach terminal and log sinks.
// FlowID itself remains unchanged so callers can inspect the exact stored key.
func (e PartialListEntry) DiagnosticFlowID() string {
	if strings.TrimSpace(e.FlowID) == "" {
		return strconv.QuoteToASCII(e.FlowID)
	}
	for _, r := range e.FlowID {
		if !strconv.IsPrint(r) {
			return strconv.QuoteToASCII(e.FlowID)
		}
	}
	return e.FlowID
}

// PartialListError reports rows omitted from an otherwise usable List result.
// Entries retain the list query's deterministic SQL order.
type PartialListError struct {
	Entries []PartialListEntry
}

func (e *PartialListError) Error() string {
	if e == nil {
		return ""
	}
	parts := make([]string, len(e.Entries))
	for i, entry := range e.Entries {
		parts[i] = fmt.Sprintf("%s: %v", entry.DiagnosticFlowID(), entry.Cause)
	}
	return fmt.Sprintf("skipped %d unreadable Flows: %s", len(e.Entries), strings.Join(parts, "; "))
}

// Unwrap exposes every retained row-local cause to errors.Is and errors.As.
func (e *PartialListError) Unwrap() []error {
	if e == nil {
		return nil
	}
	causes := make([]error, len(e.Entries))
	for i, entry := range e.Entries {
		causes[i] = entry.Cause
	}
	return causes
}

// AsPartialList classifies a valid standalone partial Flow-list result without
// requiring callers to match diagnostic text. Ordinary single-error wrapping
// is accepted, but aggregate error trees are rejected so a joined fatal error
// can never be downgraded merely because it also contains a partial diagnostic.
func AsPartialList(err error) (*PartialListError, bool) {
	for err != nil {
		if partial, ok := err.(*PartialListError); ok {
			if partial == nil || len(partial.Entries) == 0 {
				return nil, false
			}
			for _, entry := range partial.Entries {
				if entry.Cause == nil {
					return nil, false
				}
			}
			return partial, true
		}
		if _, aggregate := err.(interface{ Unwrap() []error }); aggregate {
			return nil, false
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = wrapped.Unwrap()
	}
	return nil, false
}

// ErrAutoLaunchOutdated is the sentinel every outdated-auto-launch rejection
// wraps. It is exported so callers outside this package can build the rejection
// their AutoMode handling has to survive.
var ErrAutoLaunchOutdated = errors.New("auto launch outdated")

// ErrFlowClosed is the sentinel every closed-Flow mutation refusal wraps. A
// reservation reports a closed Flow and an unreadable one through the same
// error return, and only the first may block a caller: callers that must stay
// permissive when a Flow is missing test for this rather than for any error.
var ErrFlowClosed = errors.New("flow is closed")

const (
	StatusPending        = "pending"
	StatusInProgress     = "in_progress"
	StatusNeedsAttention = "needs_attention"
	StatusBlocked        = "blocked"
	StatusCompleted      = "completed"
	StatusMerged         = "merged"
	StatusAbandoned      = "abandoned"
	StatusClosed         = "closed"
)

const (
	PhasePending        = "pending"
	PhaseReady          = "ready"
	PhaseRunning        = "running"
	PhaseNeedsAttention = "needs_attention"
	PhaseCompleted      = "completed"
	PhaseBlocked        = "blocked"
	PhaseSkipped        = "skipped"
)

const (
	MergePending = "pending"
	MergeMerged  = "merged"
	MergeBlocked = "blocked"
)

const (
	OutcomeApproved             = "approved"
	OutcomeApprovedWithConcerns = "approved_with_concerns"
	OutcomeChangesRequested     = "changes_requested"
	OutcomeBlocked              = "blocked"
)

const (
	KindPlan                = "plan"
	KindPlanReview          = "plan_review"
	KindImplementation      = "implementation"
	KindReviewLoop          = "review_loop"
	KindPRCreation          = "pr_creation"
	KindAutoreview          = "autoreview"
	KindMerge               = "merge"
	KindImplementationChild = "implementation_child"
)

const (
	GraphRecoveryPresetEdgesRestored    = "preset_edges_restored"
	GraphRecoveryMissingEdgesUnresolved = "missing_edges_unresolved"
)

// Store reads and writes Flow rows in the artifact root's approach.db.
type Store struct {
	backend  backend
	planLink planLinkResolver
	planSync planPhaseSyncer
	// canonicalRoot and lockTimeout back the short cross-process repair-launch
	// reservation shared with CloseFlow. The advisory lock orders the final
	// closed-state read and terminal start without persisting transient launch
	// metadata that a crash could strand.
	canonicalRoot             string
	lockTimeout               time.Duration
	now                       func() time.Time
	beforeLinkedPlanPhaseSync func(planID, phaseID string)
}

// StoreOptions configures a Store.
type StoreOptions struct {
	Root string
	Now  func() time.Time
	// LockTimeout bounds bootstrap lease and SQLite writer acquisition waits.
	LockTimeout time.Duration
	Presets     []Preset
}

// IsNotFound reports whether err means the requested Flow record does not exist.
func IsNotFound(err error) bool {
	return errors.Is(err, errFlowNotFound)
}

// IsAutoLaunchOutdated reports whether err means an automatic launch request
// lost its race with newer Flow state and should be ignored.
func IsAutoLaunchOutdated(err error) bool {
	return errors.Is(err, ErrAutoLaunchOutdated)
}

// FlowPhase is one phase in the persisted Flow pipeline.
type FlowPhase struct {
	PhaseID       string `json:"phase_id"`
	ParentPhaseID string `json:"parent_phase_id,omitempty"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	// Agent, Model, and ReasoningEffort are optional launch overrides; see
	// PhaseAgentSettings and ResolvePhaseAgentSettings.
	Agent           string    `json:"agent,omitempty"`
	Model           string    `json:"model,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	DependsOn       []string  `json:"depends_on"`
	Status          string    `json:"status"`
	Order           int       `json:"order"`
	Outcome         string    `json:"outcome,omitempty"`
	Notes           string    `json:"notes,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	LaunchIDs       []string  `json:"launch_ids,omitempty"`
	Sessions        []Session `json:"sessions,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PhaseAgentSettings is the agent selection captured for a Flow phase.
//
// Each field is read independently: an empty field means nothing was captured
// and the value must be resolved from the global setting in effect at launch,
// while "default" means the provider default was captured explicitly. Writing
// them is not independent — see Validate.
type PhaseAgentSettings struct {
	Agent           string
	Model           string
	ReasoningEffort string
}

// PhaseAgentSettingsFrom converts a resolved launch selection into the
// persistence shape. It is the only conversion between the two triples, so
// callers holding an agent.Settings should route through it rather than
// assigning the three fields by hand.
func PhaseAgentSettingsFrom(settings agent.Settings) PhaseAgentSettings {
	return PhaseAgentSettings{
		Agent:           settings.Command,
		Model:           settings.Model,
		ReasoningEffort: settings.ReasoningEffort,
	}
}

// IsZero reports whether no setting was captured. Emptiness is evaluated after
// normalization, so whitespace-only fields count as empty.
func (s PhaseAgentSettings) IsZero() bool {
	return s.Normalize().isEmpty()
}

// isEmpty is the plain emptiness check on an already-normalized triple.
func (s PhaseAgentSettings) isEmpty() bool {
	return s.Agent == "" && s.Model == "" && s.ReasoningEffort == ""
}

// Normalize lowercases and trims every field.
func (s PhaseAgentSettings) Normalize() PhaseAgentSettings {
	return PhaseAgentSettings{
		Agent:           agent.Normalize(s.Agent),
		Model:           agent.NormalizeModel(s.Model),
		ReasoningEffort: agent.NormalizeReasoningEffort(s.ReasoningEffort),
	}
}

// Validate checks the normalized triple as a unit: an unset selection is
// allowed, but a model or reasoning effort without an agent is not, because a
// model cannot be interpreted without knowing its agent.
func (s PhaseAgentSettings) Validate() error {
	s = s.Normalize()
	if s.isEmpty() {
		return nil
	}
	if err := agent.Validate(s.Agent); err != nil {
		return err
	}
	if err := agent.ValidateModel(s.Agent, s.Model); err != nil {
		return err
	}
	return agent.ValidateReasoningEffort(s.Agent, s.ReasoningEffort)
}

// ResolvePhaseAgentSettings validates a phase's raw persisted stamp before
// applying field-by-field fallbacks from the current provider preferences.
// An empty raw stamp follows the globally selected command. A non-empty agent
// selects that provider's global model and effort, even when another provider
// is selected globally. The literal "default" is non-empty and therefore
// remains an explicit provider-default choice.
func ResolvePhaseAgentSettings(prefs agent.Preferences, raw PhaseAgentSettings) (agent.Settings, error) {
	raw = raw.Normalize()
	if err := raw.Validate(); err != nil {
		return agent.Settings{}, err
	}

	effectivePrefs := prefs
	if raw.Agent != "" {
		effectivePrefs.Command = raw.Agent
	}
	resolved := agent.Resolve(effectivePrefs)
	resolved.Command = agent.Normalize(resolved.Command)
	resolved.Model = agent.NormalizeModel(resolved.Model)
	resolved.ReasoningEffort = agent.NormalizeReasoningEffort(resolved.ReasoningEffort)
	if raw.Model != "" {
		resolved.Model = raw.Model
	}
	if raw.ReasoningEffort != "" {
		resolved.ReasoningEffort = raw.ReasoningEffort
	}
	if err := agent.Validate(resolved.Command); err != nil {
		return agent.Settings{}, err
	}
	if err := agent.ValidateModel(resolved.Command, resolved.Model); err != nil {
		return agent.Settings{}, err
	}
	if err := agent.ValidateReasoningEffort(resolved.Command, resolved.ReasoningEffort); err != nil {
		return agent.Settings{}, err
	}
	return resolved, nil
}

// AgentSettings returns the agent selection captured for this phase.
func (p FlowPhase) AgentSettings() PhaseAgentSettings {
	return PhaseAgentSettings{
		Agent:           p.Agent,
		Model:           p.Model,
		ReasoningEffort: p.ReasoningEffort,
	}
}

func (p FlowPhase) withAgentSettings(settings PhaseAgentSettings) FlowPhase {
	p.Agent = settings.Agent
	p.Model = settings.Model
	p.ReasoningEffort = settings.ReasoningEffort
	return p
}

// applyDeclaredPhaseAgentSettings treats each declared phase's settings as
// all-or-nothing: a phase that declares none of the three takes the create
// defaults, and a phase that declares any of them keeps its own triple, which
// is then validated as a unit. Nothing is merged per field, so a phase that
// declares a model without an agent fails rather than silently inheriting one.
//
// Phases are stamped in place, so a rejected phase leaves the ones before it
// already stamped. Callers must discard the slice on error rather than reuse
// it; CreateWithOptions does, by returning no record at all.
func applyDeclaredPhaseAgentSettings(phases []FlowPhase, defaults PhaseAgentSettings) ([]FlowPhase, error) {
	for i, phase := range phases {
		settings := phase.AgentSettings().Normalize()
		if settings.isEmpty() {
			settings = defaults
		}
		if err := settings.Validate(); err != nil {
			return nil, fmt.Errorf("phase %q agent settings: %w", phase.PhaseID, err)
		}
		phases[i] = phase.withAgentSettings(settings)
	}
	return phases, nil
}

// Session references a provider session without duplicating transcript contents.
type Session struct {
	Provider       string    `json:"provider,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	LaunchID       string    `json:"launch_id,omitempty"`
	Status         string    `json:"status,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	TranscriptPath string    `json:"transcript_path,omitempty"`
}

// PullRequest stores agent-reported PR metadata.
type PullRequest struct {
	Provider   string `json:"provider,omitempty"`
	Number     int    `json:"number,omitempty"`
	URL        string `json:"url,omitempty"`
	HeadBranch string `json:"head_branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	Status     string `json:"status,omitempty"`
}

// BeadLink stores the independent Beads issue associated with a Flow. EpicID
// identifies the selected issue's parent epic when that relationship is known.
type BeadLink struct {
	ID     string `json:"id"`
	EpicID string `json:"epic_id,omitempty"`
}

// UnmarshalJSON distinguishes an omitted link from a present but incomplete
// object. The zero value remains valid on pre-Bead records because this method
// is not called when the containing field is absent.
func (link *BeadLink) UnmarshalJSON(data []byte) error {
	type beadLink BeadLink
	var decoded beadLink
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value := BeadLink(decoded)
	if err := validateBeadLink(value); err != nil {
		return err
	}
	if value.ID == "" {
		return errors.New("bead requires a bead id")
	}
	*link = value
	return nil
}

func validateBeadLink(link BeadLink) error {
	if link.ID == "" && link.EpicID != "" {
		return fmt.Errorf("bead epic_id %q requires a bead id", link.EpicID)
	}
	return nil
}

// Issue stores agent-reported GitHub issue metadata.
type Issue struct {
	Provider string `json:"provider,omitempty"`
	Number   int    `json:"number,omitempty"`
	URL      string `json:"url,omitempty"`
}

// PRUpdate records metadata for the pull request created by a Flow.
type PRUpdate struct {
	FlowID     string
	Provider   string
	Number     int
	URL        string
	HeadBranch string
	BaseBranch string
	Status     string
}

// IssueUpdate records metadata for the issue referenced by a Flow.
type IssueUpdate struct {
	FlowID   string
	Provider string
	Number   int
	URL      string
}

// MergeUpdate records metadata for the merge that completed or blocked a Flow.
type MergeUpdate struct {
	FlowID   string
	Status   string
	Commit   string
	MergedAt time.Time
}

// ManualMergeUpdate records metadata for a PR that was manually merged in GitHub.
type ManualMergeUpdate struct {
	FlowID   string
	PRNumber int
	PRURL    string
	Commit   string
	MergedAt time.Time
	Summary  string
}

// Merge stores agent-reported merge metadata.
type Merge struct {
	Status   string     `json:"status,omitempty"`
	Commit   string     `json:"commit,omitempty"`
	MergedAt *time.Time `json:"merged_at,omitempty"`
}

// Closure records why and when a Flow was deliberately closed. Derivation keys
// on ClosedAt, never on Reason, so a record can never drift into closed because
// of an empty-string comparison.
type Closure struct {
	Reason   string     `json:"reason,omitempty"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
}

// ClosureUpdate records a deliberate close of a Flow with its required reason.
type ClosureUpdate struct {
	FlowID string
	Reason string
}

// FlowClosed reports whether a Flow was deliberately closed.
func FlowClosed(record FlowRecord) bool {
	return record.Closed.ClosedAt != nil
}

func validateClosure(closure Closure) error {
	reason := strings.TrimSpace(closure.Reason)
	switch {
	case closure.ClosedAt != nil && reason == "":
		return errors.New("flow closure timestamp requires a reason")
	case closure.ClosedAt == nil && reason != "":
		return errors.New("flow closure reason requires a timestamp")
	default:
		return nil
	}
}

func closedFlowMutationError(record FlowRecord, action string) error {
	return fmt.Errorf("cannot %s flow %q because it is closed: %w", action, record.FlowID, ErrFlowClosed)
}

// IsFlowClosed reports whether err is a refusal to mutate or launch a closed
// Flow, as opposed to a missing record or a failed read.
func IsFlowClosed(err error) bool {
	return errors.Is(err, ErrFlowClosed)
}

// AutoModeUpdate changes whether the TUI may automatically launch ready phases
// for a single Flow after successful phase completion.
type AutoModeUpdate struct {
	FlowID  string
	Enabled bool
}

// HeadlessUpdate changes the manual launch preference for one Flow.
type HeadlessUpdate struct {
	FlowID  string
	Enabled bool
}

// FlowRecord is the persisted task workflow record.
type FlowRecord struct {
	SchemaVersion int         `json:"schema_version"`
	FlowID        string      `json:"flow_id"`
	Title         string      `json:"title"`
	Instructions  string      `json:"instructions"`
	Status        string      `json:"status"`
	RepoPath      string      `json:"repo_path"`
	WorktreePath  string      `json:"worktree_path,omitempty"`
	Branch        string      `json:"branch,omitempty"`
	BaseRef       string      `json:"base_ref,omitempty"`
	Commit        string      `json:"commit,omitempty"`
	PresetName    string      `json:"preset_name,omitempty"`
	PlanID        string      `json:"plan_id,omitempty"`
	PlanPath      string      `json:"plan_path,omitempty"`
	Bead          BeadLink    `json:"bead,omitzero"`
	Issue         Issue       `json:"issue,omitempty"`
	PR            PullRequest `json:"pr,omitempty"`
	Merge         Merge       `json:"merge,omitempty"`
	Closed        Closure     `json:"closed,omitzero"`
	AutoMode      bool        `json:"auto_mode,omitempty"`
	// Headless is the per-Flow manual-launch preference. Like AutoMode, it is
	// forced on at creation and can only be changed afterwards through
	// SetHeadless or CreateOptions.Headless — a value set on a record passed to
	// Create is ignored. It is written without omitempty so an explicit false
	// stays distinguishable from a legacy record that predates the field.
	Headless      bool               `json:"headless"`
	Phases        []FlowPhase        `json:"phases"`
	PreparedAt    *time.Time         `json:"prepared_at,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	GraphRecovery GraphRecoveryState `json:"-"`
}

// GraphRecoveryState reports an unresolved graph discovered during one-time
// legacy migration. The SQLite storage codec persists the unresolved marker.
type GraphRecoveryState struct {
	Status string
}

// FlowFilter narrows records returned by List.
type FlowFilter struct {
	RepoPath string
}

// PhaseUpdate describes one persisted phase status update.
type PhaseUpdate struct {
	FlowID  string
	PhaseID string
	Status  string
	Outcome string
	Notes   string
	Summary string
}

// PhaseAgentSettingsUpdate replaces one phase's complete persisted agent
// settings stamp. Empty settings clear the stamp and restore global fallback.
type PhaseAgentSettingsUpdate struct {
	FlowID   string
	PhaseID  string
	Settings PhaseAgentSettings
}

// PhaseRestartUpdate restarts a blocked or needs-attention phase as running.
type PhaseRestartUpdate struct {
	FlowID  string
	PhaseID string
	Notes   string
}

// ChildPhaseUpdate creates or updates a stable child phase under Implementation.
type ChildPhaseUpdate struct {
	FlowID        string
	ParentPhaseID string
	PhaseID       string
	Title         string
	Order         int
}

// PlanLinkUpdate links a saved approach plan artifact to an existing Flow.
type PlanLinkUpdate struct {
	FlowID   string
	PlanID   string
	PlanPath string
}

// StartMetadataUpdate adds launch-start metadata that is only known after a
// Flow record has been allocated.
type StartMetadataUpdate struct {
	FlowID       string
	WorktreePath string
	Branch       string
	BaseRef      string
	Commit       string
	PlanID       string
	PlanPath     string
}

// PhaseLaunchUpdate records one agent launch attempt against a Flow phase.
// Resume marks the launch as a session resume: resuming a phase in a terminal
// status (completed, skipped) records the launch without reopening the phase,
// while non-resume launches always mark the phase running.
type PhaseLaunchUpdate struct {
	FlowID     string
	PhaseID    string
	LaunchID   string
	Resume     bool
	AutoLaunch bool
}

// PhaseResetUpdate identifies one UI-owned phase recovery mutation.
type PhaseResetUpdate struct {
	FlowID  string
	PhaseID string
}

// PhaseLaunchEndUpdate records that a tracked Flow launch has ended.
type PhaseLaunchEndUpdate struct {
	FlowID   string
	PhaseID  string
	LaunchID string
	EndedAt  time.Time
}

// SessionAttachUpdate attaches a captured provider session to a Flow phase.
type SessionAttachUpdate struct {
	FlowID  string
	PhaseID string
	Session Session
}

// NewStore creates a Store rooted at an absolute artifact root.
func NewStore(opts StoreOptions) (*Store, error) {
	root := opts.Root
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	root, err := artifacts.RequireAbsoluteRoot(root, "flow")
	if err != nil {
		return nil, err
	}
	// Ordering is load-bearing: a backend bootstrap failure must still surface
	// before a bad preset does. Only the lockTimeout default is hoisted, because
	// the backend needs it at construction.
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = defaultLockTimeout
	}
	store, err := newSQLiteStoreBackend(root, lockTimeout, opts.Presets)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	planAdapter := planstoreSyncer{root: root, lockTimeout: lockTimeout}
	return &Store{
		backend:       store,
		planLink:      planAdapter,
		planSync:      planAdapter,
		canonicalRoot: store.root,
		lockTimeout:   lockTimeout,
		now:           now,
	}, nil
}

// Close releases the Store's database handles. A Store holds a pooled
// connection for its whole life, so callers that build one per operation must
// Close it or leak descriptors; long-lived callers may Close at shutdown or not
// at all. Using the Store after Close returns an error rather than panicking.
// It is safe to call more than once.
//
// Callers that discard the error are not being sloppy: every record a caller
// wrote is already durable when its mutation returned, so a Close failure can
// only mean the final WAL checkpoint did not run. Turning that into a non-zero
// exit would report a successful mutation as a failure, which is the worse
// outcome for a CLI whose caller is usually an agent.
func (s *Store) Close() error {
	return s.backend.close()
}

// DefaultRoot returns the default artifact root, matching sessions and plans.
func DefaultRoot() (string, error) {
	root, err := artifacts.DefaultRoot()
	if err != nil {
		return "", fmt.Errorf("resolve flow state root: %w", err)
	}
	return root, nil
}

// Create writes a new flow record with the default Flow phase graph.
// New records always start with auto mode enabled; callers that need manual
// mode should create the Flow, then opt out with SetAutoMode(false).
func (s *Store) Create(record FlowRecord) (FlowRecord, error) {
	return s.CreateWithOptions(record, CreateOptions{})
}

// AllocateID returns the canonical timestamped Flow ID CreateWithOptions would
// choose for title at this instant. Allocation is intentionally non-durable:
// it does not create or reserve a record, and concurrent callers may receive
// the same candidate. CreateWithOptions remains the atomic collision arbiter
// when the caller supplies the returned ID in FlowRecord.FlowID.
func (s *Store) AllocateID(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("flow title is required")
	}
	return s.backend.allocateID(title, s.now())
}

// CreateWithOptions writes a new flow record, optionally seeding empty phase
// lists from a preset instead of the default graph.
func (s *Store) CreateWithOptions(record FlowRecord, opts CreateOptions) (FlowRecord, error) {
	// A preparation receipt is a capability minted only by a successful
	// preparation finalizer. General creation never trusts a caller-supplied one.
	record.PreparedAt = nil
	if strings.TrimSpace(record.Title) == "" {
		return FlowRecord{}, fmt.Errorf("flow title is required")
	}
	if strings.TrimSpace(record.Instructions) == "" {
		return FlowRecord{}, fmt.Errorf("flow instructions are required")
	}
	if strings.TrimSpace(record.RepoPath) == "" {
		return FlowRecord{}, fmt.Errorf("flow repo path is required")
	}
	if !filepath.IsAbs(record.RepoPath) {
		return FlowRecord{}, fmt.Errorf("flow repo path must be absolute: %s", record.RepoPath)
	}
	if err := validateClosure(record.Closed); err != nil {
		return FlowRecord{}, err
	}
	// Validate the captured agent settings before any record is written, so a
	// rejected selection can never leave a partial Flow behind.
	phaseAgent := opts.PhaseAgent.Normalize()
	if err := phaseAgent.Validate(); err != nil {
		return FlowRecord{}, fmt.Errorf("flow phase agent settings: %w", err)
	}
	if record.FlowID == "" {
		// Create reads the clock twice: once to allocate the timestamped ID and
		// again below for the record's own timestamps. Both reads are counted by
		// the existing tests, so neither may be collapsed into the other.
		id, err := s.backend.allocateID(record.Title, s.now())
		if err != nil {
			return FlowRecord{}, err
		}
		record.FlowID = id
	} else if err := validateFlowID(record.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.backend.update(record.FlowID, func(sess flowSession) (FlowRecord, error) {
		if exists, err := sess.exists(); err != nil {
			return FlowRecord{}, fmt.Errorf("check flow id collision: %w", err)
		} else if exists {
			return FlowRecord{}, fmt.Errorf("flow %q already exists", record.FlowID)
		}

		// draft is a shallow copy: draft.Phases still aliases the caller's
		// array, and backfillLinearDependsOnForCreate rewrites depends_on
		// through it. That makes this closure single-use — a second pass would
		// see the first pass's edges and take the authoritative-graph branch
		// instead. Clause 1 on backend.update is what makes that safe.
		draft := record
		now := s.now()
		draft.SchemaVersion = schemaVersion
		draft.CreatedAt = defaultTime(draft.CreatedAt, now)
		draft.UpdatedAt = defaultTime(draft.UpdatedAt, now)
		draft.AutoMode = true
		draft.Headless = true
		if opts.Headless != nil {
			draft.Headless = *opts.Headless
		}
		if opts.Preset != nil && len(draft.Phases) > 0 {
			return FlowRecord{}, fmt.Errorf("preset cannot be used with declared phases")
		}
		if len(draft.Phases) == 0 {
			preset := DefaultPreset()
			if opts.Preset != nil {
				preset = *opts.Preset
				draft.PresetName = strings.ToLower(strings.TrimSpace(preset.Name))
			}
			if err := validatePreset(preset); err != nil {
				return FlowRecord{}, err
			}
			draft.Phases = seedPhases(preset.Phases, phaseAgent, draft.CreatedAt, draft.UpdatedAt)
			draft = refreshPhaseReadiness(draft, now)
		} else {
			if err := validateDeclaredPhaseDependencies(draft.Phases); err != nil {
				return FlowRecord{}, err
			}
			phases, err := applyDeclaredPhaseAgentSettings(draft.Phases, phaseAgent)
			if err != nil {
				return FlowRecord{}, err
			}
			draft.Phases = phases
			authoritativeEdges := graphHasAuthoritativeEdges(draft.Phases)
			draft.Phases = backfillLinearDependsOnForCreate(draft.Phases)
			if err := validateUniqueMergePhaseKind(draft.Phases); err != nil {
				return FlowRecord{}, err
			}
			if authoritativeEdges {
				if err := validatePhaseGraph(draft.Phases); err != nil {
					return FlowRecord{}, err
				}
			}
		}
		draft = normalizeRecord(draft, false)
		draft.Status = DeriveStatus(draft)
		if err := s.saveSession(sess, draft); err != nil {
			return FlowRecord{}, err
		}
		return draft, nil
	})
}

// Read returns one flow record by ID.
func (s *Store) Read(flowID string) (FlowRecord, error) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, err
	}
	record, ok, err := s.readRecord(flowID)
	if err != nil {
		return FlowRecord{}, err
	}
	if !ok {
		return FlowRecord{}, flowNotFoundError(flowID)
	}
	return record, nil
}

// SetPhase validates and persists one phase update on an existing flow.
//
// The linked-plan sync runs AFTER the phase change commits, so the return
// contract has four cases:
//
//   - sync succeeded: the committed record, nil.
//   - sync failed on a repeat of an already-completed phase: the committed
//     record and nil, because the durable state is correct and the plan write is
//     idempotent. The sync error is DISCARDED, so this recovery path reports
//     success even when it failed again; the signal is the linked plan's own
//     phase status, exactly as on MarkManualMerge's retry.
//   - sync failed otherwise: a ZERO record beside the sync error, whether the
//     needs_attention compensation was persisted by the second update or its
//     guard declined to write. MarkManualMerge deliberately differs and returns
//     its compensated record; both halves are pinned by tests.
//   - sync failed and the compensation failed too: a zero record beside one
//     error carrying both, with %w on the sync error.
//
// See syncLinkedPlanPhase for the ordering and its accepted windows.
func (s *Store) SetPhase(update PhaseUpdate) (FlowRecord, error) {
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	// Assigned whole on the closure's success path only, for the post-commit
	// sync and its guarded compensation. Off that path it is the zero value and
	// nothing below reads it, because err is non-nil there.
	var committed phaseCommit
	record, err := s.backend.update(update.FlowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !ok {
			return FlowRecord{}, flowNotFoundError(update.FlowID)
		}
		record := stored.record
		if FlowClosed(record) && update.Status == PhaseRunning {
			return FlowRecord{}, closedFlowMutationError(record, "set a phase running on")
		}
		if err := validatePhaseGraphResolved(record); err != nil {
			return FlowRecord{}, err
		}
		// When a legacy record still holds duplicate rows for this logical phase,
		// the first row wins: it is validated, updated, and kept, while the others
		// are merged into it by collapseDuplicatePhaseRows below.
		phaseIndex := phaseIndexByID(record.Phases, update.PhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}

		now := flowMutationTime(record, s.now())
		phase := record.Phases[phaseIndex]
		priorStatus := phase.Status
		if err := validatePhaseUpdate(phase, update); err != nil {
			return FlowRecord{}, err
		}
		phase.Status = update.Status
		if clearsPhaseOutcome(update.Status) {
			phase.Outcome = ""
		}
		if outcome := strings.TrimSpace(update.Outcome); outcome != "" {
			phase.Outcome = outcome
		}
		if update.Notes != "" {
			phase.Notes = update.Notes
		}
		if update.Summary != "" {
			phase.Summary = update.Summary
		}
		phase.PhaseID = update.PhaseID
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		record = resetMergePendingForActiveMergePhase(record, phase)
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		record.Status = DeriveStatus(record)
		if err := s.saveSession(sess, record); err != nil {
			return FlowRecord{}, err
		}
		committed = phaseCommit{phase: phase, stored: committedPhaseRow(record, phase), priorStatus: priorStatus}
		return record, nil
	})
	if err != nil {
		return FlowRecord{}, err
	}
	syncErr := s.syncLinkedPlanPhase(record, committed.phase)
	if syncErr == nil {
		return record, nil
	}
	if committed.priorStatus == PhaseCompleted {
		// A repeat of an already-completed phase: the durable state is correct
		// and the plan write is idempotent, so a failure here must not demote it.
		// This is the retry path the accepted post-commit windows recover through.
		return record, nil
	}
	if _, compErr := s.compensatePhaseSyncFailure(update.FlowID, committed.stored, syncErr); compErr != nil {
		// One error carrying both. The %w stays on the SYNC error: that is the
		// primary outcome and the substring callers assert on. A deliberate
		// consequence is that a Flow deleted concurrently folds its
		// flowNotFoundError in with %v, so errors.Is(err, errFlowNotFound) is
		// false here.
		return FlowRecord{}, fmt.Errorf("%w; additionally failed to persist needs_attention state: %v", syncErr, compErr)
	}
	return FlowRecord{}, syncErr
}

// SetPhaseAgentSettings atomically replaces only one phase's agent settings.
// It intentionally bypasses Flow normalization, readiness derivation, linked
// plan synchronization, and unrelated phase validation: settings affect only a
// future launch and legacy records must remain otherwise byte-for-byte stable.
func (s *Store) SetPhaseAgentSettings(update PhaseAgentSettingsUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	normalizedPhaseID := artifacts.NormalizePhaseID(update.PhaseID)
	if normalizedPhaseID == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	settings := update.Settings.Normalize()
	if err := settings.Validate(); err != nil {
		return FlowRecord{}, err
	}

	return s.backend.update(update.FlowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !ok {
			return FlowRecord{}, flowNotFoundError(update.FlowID)
		}
		record := stored.record
		phaseIndex := -1
		for i := range record.Phases {
			if record.Phases[i].PhaseID == update.PhaseID {
				phaseIndex = i
				break
			}
		}
		if phaseIndex < 0 {
			for i := range record.Phases {
				if artifacts.NormalizePhaseID(record.Phases[i].PhaseID) == normalizedPhaseID {
					phaseIndex = i
					break
				}
			}
		}
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		if phase.AgentSettings().Normalize() == settings && phase.AgentSettings() == settings {
			return record, nil
		}
		now := flowMutationTime(record, s.now())
		record.Phases[phaseIndex] = phase.withAgentSettings(settings)
		record.Phases[phaseIndex].UpdatedAt = now
		record.UpdatedAt = now
		if err := sess.savePhaseAgentSettings(phaseAgentSettingsSave{
			PhaseIndex:      phaseIndex,
			PhaseID:         phase.PhaseID,
			Settings:        settings,
			PhaseUpdatedAt:  now,
			RecordUpdatedAt: now,
		}); err != nil {
			return FlowRecord{}, err
		}
		return record, nil
	})
}

// compensatePhaseSyncFailure demotes a committed phase to needs_attention after
// its linked-plan sync failed, in a second update computed from freshly read
// state. It saves nothing when that state no longer represents this completion
// attempt; see phaseStillHoldsCommittedCompletion for what "no longer" means and
// why declining beats clobbering.
//
// It also declines when the demotion would contradict durable merge metadata.
// validateMergeUpdate refuses to record a merged Merge unless the merge phase is
// completed, so "Merge merged beside a merge phase that is not completed" was
// unreachable while the sync held the Flow writer. Post-commit it is reachable:
// the phase is durably completed for the length of the sync, so a concurrent
// merge write is legal, and demoting afterwards would leave merged metadata that
// DeriveStatus reads as a merged Flow — hiding the needs_attention phase behind
// a status the TUI drops from its active list. Declining keeps the phase
// completed, which is the accepted no-marker window and leaves the idempotent
// retry available.
//
// That test is made on the compensated record rather than on the row being
// demoted, because the merge phase does not have to be the demoted one. Demoting
// any phase the merge depends on unsatisfies its gate, and the readiness refresh
// then resets the merge row to pending — same contradiction, one hop away. So
// the compensation is computed into a copy first and saved only if the pairing
// still holds. It covers a concurrently blocked merge for the same reason: that
// pairing is equally enforced, and StatusBlocked masks needs_attention just as
// StatusMerged does.
func (s *Store) compensatePhaseSyncFailure(flowID string, committedPhase FlowPhase, syncErr error) (FlowRecord, error) {
	return s.backend.update(flowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !ok {
			return FlowRecord{}, flowNotFoundError(flowID)
		}
		record := stored.record
		index := phaseIndexByID(record.Phases, committedPhase.PhaseID)
		if index < 0 || !phaseStillHoldsCommittedCompletion(record.Phases[index], committedPhase) {
			return record, nil
		}
		// One fresh clock read for all three stamps. Reusing the committing
		// update's now would leave the compensated phase — and the record — at or
		// behind a concurrent writer's timestamp, and the TUI drops a record whose
		// UpdatedAt is behind the one it already holds.
		now := flowMutationTime(record, s.now())
		compensated := record
		compensated.Phases = append([]FlowPhase(nil), record.Phases...)
		compensated.Phases[index] = markPhaseSyncNeedsAttention(compensated.Phases[index], syncErr, now)
		compensated.UpdatedAt = now
		compensated = refreshPhaseReadiness(compensated, now)
		compensated.Status = DeriveStatus(compensated)
		if !recordedMergeKeepsItsPhasePairing(compensated) {
			return record, nil
		}
		if err := s.saveSession(sess, compensated); err != nil {
			return FlowRecord{}, err
		}
		return compensated, nil
	})
}

// recordedMergeKeepsItsPhasePairing reports whether a record's merge metadata
// still sits beside the merge phase status validateMergeUpdate requires for it:
// completed for a merged Merge, blocked for a blocked one. A record that breaks
// either pairing is one no caller could have produced directly, and both hide a
// compensated phase behind a derived status — merged or blocked — that outranks
// needs_attention.
func recordedMergeKeepsItsPhasePairing(record FlowRecord) bool {
	var want string
	switch record.Merge.Status {
	case MergeMerged:
		want = PhaseCompleted
	case MergeBlocked:
		want = PhaseBlocked
	default:
		return true
	}
	index := mergePhaseIndex(record)
	return index >= 0 && record.Phases[index].Status == want
}

// committedPhaseRow returns the phase row as it was actually stored, which is
// what the compensation guard has to compare against on the re-read.
//
// It is deliberately taken AFTER collapseDuplicatePhaseRows rather than from the
// local the closure built. Collapse leaves Status and UpdatedAt alone, but on a
// legacy record carrying duplicate rows for one logical phase it fills an empty
// survivor Notes or Summary from the rows it merges away — so the pre-collapse
// local can differ from the stored row in exactly the fields the guard reads.
//
// Lookup is by ID and never by index: refreshPhaseReadiness reorders the slice
// through OrderedPhases, so an index captured before it points at a different
// phase afterwards. If the row cannot be found the local is returned, which at
// worst makes the guard reject and skip a compensation — never demote the wrong
// phase.
func committedPhaseRow(record FlowRecord, local FlowPhase) FlowPhase {
	if index := phaseIndexByID(record.Phases, local.PhaseID); index >= 0 {
		return record.Phases[index]
	}
	return local
}

// phaseCommit is what SetPhase's committing closure hands to the post-commit
// sync. It is assigned as a whole on the success path so a half-filled capture
// is not representable.
type phaseCommit struct {
	// phase is the local the closure built, and is what the sync is handed —
	// the same value it has always been handed.
	//
	// Keeping it separate from stored is defensive rather than load-bearing
	// today: the sync reads only Status and PhaseID, and the two rows can only
	// disagree on Status if refreshPhaseReadiness resets the just-completed row
	// to pending, which needs unsatisfied dependency gates that no completable
	// phase has (transitions.go forbids pending -> completed, so only ready or
	// running can complete). No test pins the difference because none can
	// currently reach it. The split stays because the alternative silently
	// couples what gets synced to readiness, and a later edit to either side
	// would be the thing that reaches it.
	phase FlowPhase
	// stored is that same phase as it was actually committed, and is the only
	// value the compensation guard may compare against. See committedPhaseRow.
	stored FlowPhase
	// priorStatus is the status the phase held BEFORE this update, which is what
	// distinguishes a first completion from a repeat of one.
	priorStatus string
}

// manualMergeCommit is the same capture for MarkManualMerge, which needs the
// merge metadata and the pre-merge PR status to roll back as well. retry marks
// the already-merged no-op path, whose sync error is discarded rather than
// compensated.
type manualMergeCommit struct {
	phase  FlowPhase
	stored FlowPhase
	merge  Merge
	// pr is the WHOLE PullRequest as committed, not just its status, because
	// SetPR replaces record.PR wholesale. A concurrent writer can point the Flow
	// at a different PR that is itself already merged — new number and URL, same
	// merged status — without touching record.Merge or the merge phase, and a
	// guard that only checked the status would then stamp previousPRStatus onto
	// that writer's PR. PullRequest is all scalars, so == is a full comparison.
	pr               PullRequest
	previousPRStatus string
	retry            bool
}

// phaseStillHoldsCommittedCompletion reports whether a freshly read phase row is
// still the completion this caller committed, and is the gate on every
// post-commit compensation.
//
// Status alone is insufficient: a concurrent re-completion preserves completed,
// and demoting it would attribute this caller's sync failure to a completion it
// did not make. So the test is the completion-bearing fields, and only a change
// there means someone else owns this completion now.
//
// phase.UpdatedAt is deliberately NOT part of it, in either direction. It is too
// strict as a requirement, because writes that are not completions stamp it too:
// AttachSession and MarkPhaseLaunchEnded both bump it on a completed row without
// touching the completion, and both are driven by agent session lifecycle events
// that fire at almost exactly the moment the completing write is parked on the
// plan lock. Rejecting on those is the expensive direction — it produces a
// completed phase with a stale plan and NO marker anywhere, which is silent.
//
// And it is not sufficient as a shortcut, because a timestamp is not a unique
// write token. Two writes can carry the same stamp under a coarse or adjusted
// clock, or under an injected StoreOptions.Now that does not advance, so an equal
// UpdatedAt cannot license skipping the field comparison: doing so would accept a
// different completion that happened to land on this caller's stamp. The fields
// are compared unconditionally, which costs nothing — when the row really is
// this caller's write they are equal by construction.
//
// It is a heuristic, not a fence. A bare re-completion that rewrites none of
// those fields is indistinguishable from a metadata write, so it is accepted and
// may demote a phase whose plan a concurrent writer already synced. Only a
// per-phase completion counter would be exact. That residual is the cheap
// direction: it costs a visible needs_attention marker, which an operator clears
// with RestartPhase and then a fresh completion — needs_attention -> completed is
// not a legal transition — against the silent staleness of the alternative.
//
// When it rejects, the caller saves nothing. The operator-visible result is a
// completed phase with a stale linked plan, NO needs_attention marker anywhere,
// and only the returned error as a signal; recovery is to repeat the completion.
// The middle option — appending the sync-failure note without the demotion — is
// rejected on purpose: the concurrent re-completion may already have synced the
// plan successfully, which would make the note false.
func phaseStillHoldsCommittedCompletion(current, committed FlowPhase) bool {
	if current.Status != PhaseCompleted {
		return false
	}
	return current.Outcome == committed.Outcome &&
		current.Summary == committed.Summary &&
		current.Notes == committed.Notes
}

// RestartPhase atomically restarts a blocked or needs-attention phase as running.
func (s *Store) RestartPhase(update PhaseRestartUpdate) (FlowRecord, error) {
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	if err := validatePhaseID(update.PhaseID); err != nil {
		return FlowRecord{}, err
	}
	if strings.TrimSpace(update.Notes) == "" {
		return FlowRecord{}, fmt.Errorf("phase restart requires notes")
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			return FlowRecord{}, closedFlowMutationError(record, "restart a phase on")
		}
		phaseIndex := phaseIndexByID(record.Phases, update.PhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		if phase.Status != PhaseNeedsAttention && phase.Status != PhaseBlocked {
			return FlowRecord{}, fmt.Errorf("flow phase restart requires current status needs_attention or blocked; %s is %s", phase.PhaseID, phase.Status)
		}
		if err := validatePhaseUpdate(phase, PhaseUpdate{
			FlowID:  update.FlowID,
			PhaseID: update.PhaseID,
			Status:  PhaseRunning,
			Notes:   update.Notes,
		}); err != nil {
			return FlowRecord{}, err
		}
		phase.Status = PhaseRunning
		phase.Outcome = ""
		phase.Notes = update.Notes
		phase.PhaseID = update.PhaseID
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		record = resetMergePendingForActiveMergePhase(record, phase)
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		return record, nil
	})
}

// AddChildPhase creates or updates a stable child phase under Implementation.
func (s *Store) AddChildPhase(update ChildPhaseUpdate) (FlowRecord, error) {
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	update.ParentPhaseID = artifacts.NormalizePhaseID(update.ParentPhaseID)
	if err := validateChildPhaseUpdate(update); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		parentIndex := phaseIndexByID(record.Phases, update.ParentPhaseID)
		if parentIndex < 0 {
			return FlowRecord{}, fmt.Errorf("parent phase %q not found in flow %q", update.ParentPhaseID, update.FlowID)
		}
		if SemanticKind(record.Phases[parentIndex]) != KindImplementation {
			return FlowRecord{}, fmt.Errorf("child phases can only be added under implementation")
		}
		childIndex := phaseIndexByID(record.Phases, update.PhaseID)
		if childIndex >= 0 {
			child := record.Phases[childIndex]
			if child.ParentPhaseID != update.ParentPhaseID {
				return FlowRecord{}, fmt.Errorf("phase %q already belongs to parent %q", update.PhaseID, child.ParentPhaseID)
			}
			// Repair duplicate rows even when the surviving row already matches
			// the update; the unchanged early return below must not skip it.
			record.Phases = collapseDuplicatePhaseRows(record.Phases, childIndex)
			childIndex = phaseIndexByID(record.Phases, update.PhaseID)
			child = record.Phases[childIndex]
			if child.PhaseID == update.PhaseID &&
				child.Title == strings.TrimSpace(update.Title) &&
				child.Kind == KindImplementationChild &&
				child.Order == update.Order {
				return record, nil
			}
			child.PhaseID = update.PhaseID
			child.Title = strings.TrimSpace(update.Title)
			child.Kind = KindImplementationChild
			child.Order = update.Order
			child.UpdatedAt = now
			record.Phases[childIndex] = child
			record.UpdatedAt = now
			record.Phases = orderImplementationChildren(record.Phases, update.ParentPhaseID)
			record = refreshPhaseReadiness(record, now)
			return record, nil
		}
		// New children inherit the implementation parent's captured settings;
		// existing children keep whatever they already carry, since
		// AddChildPhase is re-run idempotently.
		child := FlowPhase{
			PhaseID:       update.PhaseID,
			ParentPhaseID: update.ParentPhaseID,
			Title:         strings.TrimSpace(update.Title),
			Kind:          KindImplementationChild,
			Status:        PhasePending,
			Order:         update.Order,
			CreatedAt:     now,
			UpdatedAt:     now,
		}.withAgentSettings(record.Phases[parentIndex].AgentSettings())
		record.Phases = append(record.Phases, child)
		record.Phases = orderImplementationChildren(record.Phases, update.ParentPhaseID)
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		return record, nil
	})
}

func clearsPhaseOutcome(status string) bool {
	return status == PhaseRunning
}

// PhaseStatusTerminal reports whether a phase has finished (successfully or by
// being skipped), as opposed to states that still expect agent work.
func PhaseStatusTerminal(status string) bool {
	return status == PhaseCompleted || status == PhaseSkipped
}

func markPhaseSyncNeedsAttention(phase FlowPhase, err error, now time.Time) FlowPhase {
	phase.Status = PhaseNeedsAttention
	phase.Outcome = ""
	note := fmt.Sprintf("Linked plan phase sync failed: %v", err)
	if strings.TrimSpace(phase.Notes) != "" {
		phase.Notes = strings.TrimSpace(phase.Notes) + "\n" + note
	} else {
		phase.Notes = note
	}
	phase.UpdatedAt = now
	return phase
}

// syncLinkedPlanPhase mirrors a completed Flow phase into its linked plan.
//
// ORDERING: commit, then sync, then compensate. Callers run this AFTER their
// authoritative Flow update has committed and while they hold no Flow writer,
// and persist the needs_attention compensation in a second, guarded update. The
// reason is the SQLite writer's blast radius: it is database-wide, so holding it
// across planstore's own global file lock stalls every Flow write in every
// process, not just this one. Foreign I/O must not hold it.
//
// That opens a window between the commit and the compensation, spanning the
// plan-lock wait, the plan write, and the second update's own writer
// acquisition. Three consequences, all accepted:
//
//  1. CRASH. A crash inside the window leaves a completed Flow phase with a
//     stale linked-plan phase and no compensation marker.
//  2. OBSERVABILITY, the common case. The window is externally visible, not just
//     a crash hazard: the first update already derived readiness and status, so
//     any reader sees a legitimately completed phase with a legitimately ready
//     successor. The TUI's auto-advance drain can launch that successor; the
//     compensation then demotes the predecessor, and readiness resets the
//     just-launched successor to pending with a live agent session attached. The
//     reset is not limited to a running successor: refreshPhaseReadiness resets
//     every downstream row whose gate the demotion unsatisfies, so a successor
//     that reached completed or skipped inside the window goes back to pending
//     with its outcome cleared, and repeating the predecessor's completion
//     restores its readiness but not that outcome. All of it is already
//     reachable without this change — completed -> running is a legal
//     transition, so restarting a completed predecessor produces the same resets
//     — and the session does not wedge: MarkPhaseLaunchEnded works on any phase
//     status, though the agent's own completed write is rejected while its phase
//     sits at pending until readiness re-promotes the row.
//  3. NO MARKER AT ALL. The second update can fail to acquire the writer under
//     contention, or its guard can deliberately decline to write: because
//     another writer re-completed the phase first, or because a concurrent
//     SetMerge recorded a durable merge against this merge phase, which the
//     demotion would contradict. Either way the window never closes: a completed
//     phase, a stale plan, no needs_attention marker, and only the returned
//     error as a signal. See phaseStillHoldsCommittedCompletion for why the
//     guard declines on a competing completion but not on a concurrent session
//     or launch-id write, and compensatePhaseSyncFailure for the merge clause.
//
// All three are accepted for the same reasons. The two stores have never been
// atomic — the file backend had the opposite skew — the Flow database remains
// authoritative and commits first, and the plan status update is idempotent, so
// the recovery for every one of them is identical and already exists: repeat the
// phase completion. That is why both entry points have a retry path. SetPhase
// re-syncs on a completed -> completed repeat, and MarkManualMerge re-syncs on
// an already-merged repeat; both discard the retry's own sync error rather than
// demote durable state.
//
// Two limits on that recovery, both documented for operators in
// docs/flow-phases.md. It applies to the windows above, where the phase is still
// completed — once the compensation HAS landed the phase must be restarted
// before it can complete again, because needs_attention -> completed is not a
// legal transition. And the merge-side repeat is currently store-level only: the
// TUI gates its manual-merge action on the Flow not being derived as merged, so
// after a durable merge the reachable repair is the plan artifact itself.
func (s *Store) syncLinkedPlanPhase(record FlowRecord, phase FlowPhase) error {
	planID := strings.TrimSpace(record.PlanID)
	if planID == "" || phase.Status != PhaseCompleted {
		return nil
	}
	writer, err := s.planSync.open()
	if err != nil {
		return fmt.Errorf("sync linked plan phase: %w", err)
	}
	if s.beforeLinkedPlanPhaseSync != nil {
		s.beforeLinkedPlanPhaseSync(planID, phase.PhaseID)
	}
	if err := writer.markPhaseCompleted(planID, phase.PhaseID); err != nil {
		return fmt.Errorf("sync linked plan phase: %w", err)
	}
	return nil
}

// SetPlanLink validates and persists the saved plan artifact linked to a Flow.
func (s *Store) SetPlanLink(update PlanLinkUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	planID := strings.TrimSpace(update.PlanID)
	if planID == "" {
		return FlowRecord{}, fmt.Errorf("plan id is required")
	}
	planPath, err := s.planLink.resolvePlanLink(planID, update.PlanPath)
	if err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if record.PlanID == planID && record.PlanPath == planPath {
			return record, nil
		}
		record.PlanID = planID
		record.PlanPath = planPath
		record.UpdatedAt = now
		return record, nil
	})
}

// SetPR validates and persists the pull request metadata reported by an agent.
func (s *Store) SetPR(update PRUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		pr, err := validatePRUpdate(record, update)
		if err != nil {
			return FlowRecord{}, err
		}
		if record.PR == pr {
			return record, nil
		}
		record.PR = pr
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		return record, nil
	})
}

// SetIssue validates and persists the issue metadata reported by an agent.
func (s *Store) SetIssue(update IssueUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		issue, err := validateIssueUpdate(update)
		if err != nil {
			return FlowRecord{}, err
		}
		if record.Issue == issue {
			return record, nil
		}
		record.Issue = issue
		record.UpdatedAt = now
		return record, nil
	})
}

// SetMerge validates and persists the merge metadata reported by an agent.
func (s *Store) SetMerge(update MergeUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			return FlowRecord{}, closedFlowMutationError(record, "set merge metadata on")
		}
		merge, err := validateMergeUpdate(record, update)
		if err != nil {
			return FlowRecord{}, err
		}
		if mergeEqual(record.Merge, merge) {
			return record, nil
		}
		record.Merge = merge
		record.UpdatedAt = now
		return record, nil
	})
}

// MarkManualMerge completes the merge phase and records verified GitHub merge
// metadata for a PR that was merged outside approach.
//
// The linked-plan sync runs AFTER the merge commits, giving four cases:
//
//   - sync succeeded: the committed record, nil.
//   - sync failed on an already-merged repeat: the record and nil. The error is
//     DISCARDED rather than allowed to demote durable merge state.
//   - sync failed otherwise: the compensated record beside the error, or the
//     current state when the compensation guard rejects — deliberately unlike
//     SetPhase, which returns a zero record. A guard-rejected record is still
//     merged and still reaches the TUI beside a merge-failure message; that is
//     the truth of the durable state.
//   - sync failed and the compensation failed too: a zero record beside one
//     error carrying both, matching SetPhase.
//
// See syncLinkedPlanPhase for the ordering and its accepted windows.
func (s *Store) MarkManualMerge(update ManualMergeUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	// Assigned whole on the closure's success paths only, for the post-commit
	// sync and its guarded compensation.
	var committed manualMergeCommit
	record, err := s.backend.update(update.FlowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !ok {
			return FlowRecord{}, flowNotFoundError(update.FlowID)
		}
		record := stored.record
		if FlowClosed(record) {
			return FlowRecord{}, closedFlowMutationError(record, "mark merged")
		}
		if err := validatePhaseGraphResolved(record); err != nil {
			return FlowRecord{}, err
		}
		phaseIndex := mergePhaseIndex(record)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", "merge", update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		merge, err := validateManualMergeUpdate(record, phase, update)
		if err != nil {
			return FlowRecord{}, err
		}
		if record.PR.Status == MergeMerged &&
			phase.Status == PhaseCompleted &&
			mergeEqual(record.Merge, merge) {
			// Flow semantics of the retry are unchanged: no mutation, no save,
			// and the record comes back as it was. Only the post-commit re-sync
			// below is new. Every field is filled even though retry short-circuits
			// before the compensation reads them, so that moving that check can
			// never roll a merge back against zero values.
			committed = manualMergeCommit{
				phase:            phase,
				stored:           phase,
				merge:            record.Merge,
				pr:               record.PR,
				previousPRStatus: record.PR.Status,
				retry:            true,
			}
			return record, nil
		}

		now := flowMutationTime(record, s.now())
		summary := strings.TrimSpace(update.Summary)
		if summary == "" {
			summary = fmt.Sprintf("Marked GitHub PR #%d as manually merged at %s.", record.PR.Number, merge.Commit)
		}
		previousPRStatus := record.PR.Status
		phase.Status = PhaseCompleted
		phase.Outcome = MergeMerged
		phase.Summary = summary
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		record.PR.Status = MergeMerged
		record.Merge = merge
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		record.Status = DeriveStatus(record)
		if err := s.saveSession(sess, record); err != nil {
			return FlowRecord{}, err
		}
		committed = manualMergeCommit{
			phase:            phase,
			stored:           committedPhaseRow(record, phase),
			merge:            merge,
			pr:               record.PR,
			previousPRStatus: previousPRStatus,
		}
		return record, nil
	})
	if err != nil {
		return FlowRecord{}, err
	}
	syncErr := s.syncLinkedPlanPhase(record, committed.phase)
	if syncErr == nil {
		return record, nil
	}
	if committed.retry {
		// The merge was already recorded, so repeating it re-runs the idempotent
		// plan write and DISCARDS a failure, mirroring SetPhase's completed-retry
		// semantics. Two accepted costs. The retry now does plan-store I/O where
		// it previously did none — post-commit, holding no Flow writer, and it
		// cannot change the return contract. And because the error is discarded,
		// the designated recovery for every post-commit window reports success
		// even when it fails again: the signal on this path is the linked plan's
		// own phase status, not the return value. Demoting durable merge state on
		// a retry would be worse, and a compensated merge phase cannot be retried
		// at all until an operator runs RestartPhase.
		return record, nil
	}
	compensated, compErr := s.compensateManualMergeSyncFailure(update.FlowID, committed, syncErr)
	if compErr != nil {
		// Same combined error as SetPhase, with %w on the sync error.
		return FlowRecord{}, fmt.Errorf("%w; additionally failed to persist needs_attention state: %v", syncErr, compErr)
	}
	return compensated, syncErr
}

// compensateManualMergeSyncFailure rolls a recorded manual merge back after its
// linked-plan sync failed, in a second update computed from freshly read state.
// Its guard is stricter than SetPhase's because it rewrites three fields: the
// merge phase must still be this caller's completion AND the PR and merge
// metadata must still be the ones it committed. Any mismatch means a concurrent
// writer owns that state now, so nothing is saved.
func (s *Store) compensateManualMergeSyncFailure(flowID string, committed manualMergeCommit, syncErr error) (FlowRecord, error) {
	return s.backend.update(flowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !ok {
			return FlowRecord{}, flowNotFoundError(flowID)
		}
		record := stored.record
		index := phaseIndexByID(record.Phases, committed.stored.PhaseID)
		if index < 0 ||
			!phaseStillHoldsCommittedCompletion(record.Phases[index], committed.stored) ||
			record.PR != committed.pr ||
			!mergeEqual(record.Merge, committed.merge) {
			return record, nil
		}
		now := flowMutationTime(record, s.now())
		record.Phases[index] = markPhaseSyncNeedsAttention(record.Phases[index], syncErr, now)
		record.PR.Status = committed.previousPRStatus
		record.Merge = Merge{Status: MergePending}
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		record.Status = DeriveStatus(record)
		if err := s.saveSession(sess, record); err != nil {
			return FlowRecord{}, err
		}
		return record, nil
	})
}

// SetAutoMode enables or disables TUI-owned automatic phase launching for one Flow.
func (s *Store) SetAutoMode(update AutoModeUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		// Disabling auto mode is safe cleanup on a closed Flow; only arming it
		// could revive stale launch work after the close wins the race.
		if FlowClosed(record) && update.Enabled {
			return FlowRecord{}, closedFlowMutationError(record, "enable auto mode on")
		}
		if record.AutoMode == update.Enabled {
			return record, nil
		}
		record.AutoMode = update.Enabled
		record.UpdatedAt = now
		return record, nil
	})
}

// SetHeadless enables or disables headless manual launches for one Flow.
func (s *Store) SetHeadless(update HeadlessUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if record.Headless == update.Enabled {
			return record, nil
		}
		record.Headless = update.Enabled
		record.UpdatedAt = now
		return record, nil
	})
}

// CloseFlow marks a Flow deliberately closed with a required reason. Phases are
// left exactly as they are, so the record still explains where work stopped;
// terminality is enforced by the launch guards, not by rewriting phase rows.
//
// Unlike SetAutoMode and SetHeadless, a redundant call is an error rather than a
// no-op: a second close discards a reason the user typed, which is worth
// reporting.
func (s *Store) CloseFlow(update ClosureUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	reason := strings.TrimSpace(update.Reason)
	if reason == "" {
		return FlowRecord{}, errors.New("flow close requires a reason")
	}
	release, err := s.acquireLaunchCloseLock(update.FlowID)
	if err != nil {
		return FlowRecord{}, err
	}
	defer release()
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			return FlowRecord{}, fmt.Errorf("flow %q is already closed", update.FlowID)
		}
		switch DeriveStatus(record) {
		case StatusAbandoned:
			return FlowRecord{}, fmt.Errorf("flow %q is already abandoned", update.FlowID)
		case StatusMerged:
			return FlowRecord{}, fmt.Errorf("flow %q is already merged", update.FlowID)
		}
		closedAt := now
		record.Closed = Closure{Reason: reason, ClosedAt: &closedAt}
		record.UpdatedAt = now
		return record, nil
	})
}

// ReserveRepairLaunch holds the Flow's cross-process repair-launch lock and
// returns the current record. Callers must invoke the returned release function
// after the terminal has either started or failed to start. CloseFlow takes the
// same lock, so exactly one ordering wins: a completed close makes this call
// reject, while an admitted repair starts before a concurrent close proceeds.
// The OS releases the advisory lock automatically if the process exits.
func (s *Store) ReserveRepairLaunch(flowID string) (FlowRecord, func(), error) {
	return s.reserveLaunchAgainstClose(flowID, "reserve a repair launch for")
}

// ReserveAgentLaunch orders an agent spawn with CloseFlow. The caller must
// hold the returned reservation until the terminal or external launcher has
// either started or failed, so a close and a launch have one authoritative
// cross-process ordering.
func (s *Store) ReserveAgentLaunch(flowID string) (FlowRecord, func(), error) {
	return s.reserveLaunchAgainstClose(flowID, "launch an agent for")
}

// ReserveEpicProgressionSuccessor holds the Flow's launch/close lock while
// sequential progression classifies it. Unlike launch reservations, this
// reservation intentionally permits missing and closed Flows so the caller can
// apply progression-first inactive precedence to every Flow condition.
func (s *Store) ReserveEpicProgressionSuccessor(flowID string) (func(), error) {
	if err := validateFlowID(flowID); err != nil {
		return nil, err
	}
	return s.acquireLaunchCloseLock(flowID)
}

func (s *Store) reserveLaunchAgainstClose(flowID, action string) (FlowRecord, func(), error) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, nil, err
	}
	release, err := s.acquireLaunchCloseLock(flowID)
	if err != nil {
		return FlowRecord{}, nil, err
	}
	record, err := s.Read(flowID)
	if err != nil {
		release()
		return FlowRecord{}, nil, err
	}
	if FlowClosed(record) {
		release()
		return FlowRecord{}, nil, closedFlowMutationError(record, action)
	}
	return record, release, nil
}

func (s *Store) acquireLaunchCloseLock(flowID string) (func(), error) {
	// Keep the original filename so mixed-version processes contend on the same
	// advisory lock while the reservation's scope expands beyond repair.
	path := filepath.Join(s.canonicalRoot, ".approach.flow-repair."+flowID+".lock")
	release, err := artifacts.AcquireFileLockNoFollow(path, "Flow launch/close lock", s.lockTimeout)
	if err != nil {
		return nil, fmt.Errorf("acquire launch/close reservation for flow %q: %w", flowID, err)
	}
	return release, nil
}

// ReopenFlow clears the closure from a closed Flow, restoring the launchability
// it had before the close.
func (s *Store) ReopenFlow(flowID string) (FlowRecord, error) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(flowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if !FlowClosed(record) {
			return FlowRecord{}, fmt.Errorf("flow %q is not closed", flowID)
		}
		record.Closed = Closure{}
		record.UpdatedAt = now
		return record, nil
	})
}

// SetStartMetadata persists branch/worktree/plan metadata discovered while
// starting a Flow. Empty fields leave existing values unchanged.
func (s *Store) SetStartMetadata(update StartMetadataUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	if strings.TrimSpace(update.WorktreePath) != "" && !filepath.IsAbs(update.WorktreePath) {
		return FlowRecord{}, fmt.Errorf("flow worktree path must be absolute: %s", update.WorktreePath)
	}
	if strings.TrimSpace(update.PlanPath) != "" && !filepath.IsAbs(update.PlanPath) {
		return FlowRecord{}, fmt.Errorf("flow plan path must be absolute: %s", update.PlanPath)
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if record.PreparedAt != nil {
			if value := strings.TrimSpace(update.WorktreePath); value != "" && filepath.Clean(value) != record.WorktreePath {
				return FlowRecord{}, fmt.Errorf("flow %q preparation receipt protects worktree path %q", record.FlowID, record.WorktreePath)
			}
			if value := strings.TrimSpace(update.Branch); value != "" && value != record.Branch {
				return FlowRecord{}, fmt.Errorf("flow %q preparation receipt protects branch %q", record.FlowID, record.Branch)
			}
		}
		if value := strings.TrimSpace(update.WorktreePath); value != "" {
			record.WorktreePath = filepath.Clean(value)
		}
		if value := strings.TrimSpace(update.Branch); value != "" {
			record.Branch = value
		}
		if value := strings.TrimSpace(update.BaseRef); value != "" {
			record.BaseRef = value
		}
		if value := strings.TrimSpace(update.Commit); value != "" {
			record.Commit = value
		}
		if value := strings.TrimSpace(update.PlanID); value != "" {
			record.PlanID = value
		}
		if value := strings.TrimSpace(update.PlanPath); value != "" {
			record.PlanPath = filepath.Clean(value)
		}
		record.UpdatedAt = now
		return record, nil
	})
}

// AddPhaseLaunchID records a launch attempt. Fresh launches mark the phase
// running; resume launches of terminal phases preserve the terminal status.
func (s *Store) AddPhaseLaunchID(update PhaseLaunchUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	requestedPhaseID := strings.TrimSpace(update.PhaseID)
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if update.PhaseID == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	launchID := strings.TrimSpace(update.LaunchID)
	if launchID == "" {
		return FlowRecord{}, fmt.Errorf("launch id is required")
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			if update.AutoLaunch {
				return FlowRecord{}, fmt.Errorf("auto launch target %q is not eligible because flow %q is closed: %w",
					update.PhaseID, record.FlowID, ErrAutoLaunchOutdated)
			}
			return FlowRecord{}, closedFlowMutationError(record, "record a phase launch for")
		}
		// Launch bookkeeping targets the requested phase row. Legacy records may
		// contain an earlier stale duplicate whose id only matches after
		// normalization; prefer the exact row before deciding whether a resume
		// should preserve terminal state or restart active work.
		phaseIndex := phaseIndexPreferringExactID(record.Phases, requestedPhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		// Launching a phase is a mutation that derives its readiness. Custom graphs
		// persist with pending roots (Create defers readiness derivation), so a
		// now-eligible pending target would otherwise be rejected as an invalid
		// pending -> running transition. Promote only the target row when the graph
		// releases it; stale ready rows and other phases are left to the eligibility
		// checks and the post-launch readiness refresh.
		if phase.Status == PhasePending {
			graph := buildPhaseGraph(record.Phases)
			if graph.released[phaseIndex] && !graph.duplicateRows[phaseIndex] && allDependencyGatesSatisfied(record, graph, phaseIndex) {
				phase.Status = PhaseReady
				record.Phases[phaseIndex] = phase
			}
		}
		if update.AutoLaunch {
			if err := validateAutoPhaseLaunch(record, phaseIndex); err != nil {
				return FlowRecord{}, err
			}
		}
		if update.Resume && PhaseStatusTerminal(phase.Status) {
			// Resuming a finished phase's session is read-back, not new work:
			// record the launch so the session can re-link, but leave the
			// phase's terminal status, outcome, and notes intact.
			phase.LaunchIDs = appendUnique(phase.LaunchIDs, launchID)
			phase.PhaseID = update.PhaseID
			phase.UpdatedAt = now
			record.Phases[phaseIndex] = phase
			record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
			record.UpdatedAt = now
			record = refreshPhaseReadiness(record, now)
			record.Status = DeriveStatus(record)
			return record, nil
		}
		if err := validateFlowLaunchOccupancy(record, phaseIndex, update.AutoLaunch); err != nil {
			return FlowRecord{}, err
		}
		launchPhaseUpdate := PhaseUpdate{FlowID: update.FlowID, PhaseID: update.PhaseID, Status: PhaseRunning}
		if phase.Status == PhaseNeedsAttention || phase.Status == PhaseBlocked {
			launchPhaseUpdate.Notes = fmt.Sprintf("Relaunched after %s.", phase.Status)
		}
		if err := validatePhaseUpdate(phase, launchPhaseUpdate); err != nil {
			return FlowRecord{}, err
		}
		phase.Status = PhaseRunning
		if clearsPhaseOutcome(phase.Status) {
			phase.Outcome = ""
		}
		if launchPhaseUpdate.Notes != "" {
			phase.Notes = launchPhaseUpdate.Notes
		}
		phase.LaunchIDs = appendUnique(phase.LaunchIDs, launchID)
		phase.PhaseID = update.PhaseID
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		record = resetMergePendingForActiveMergePhase(record, phase)
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		record.Status = DeriveStatus(record)
		return record, nil
	})
}

func validateFlowLaunchOccupancy(record FlowRecord, targetIndex int, autoLaunch bool) error {
	graph := buildPhaseGraph(record.Phases)
	for i, phase := range record.Phases {
		if i == targetIndex || phase.Status != PhaseRunning {
			continue
		}
		if phaseDependsOnTarget(graph, i, targetIndex) {
			continue
		}
		message := fmt.Sprintf("flow %q already has running phase %q", record.FlowID, phase.PhaseID)
		if autoLaunch {
			return fmt.Errorf("%s: %w", message, ErrAutoLaunchOutdated)
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func phaseDependsOnTarget(graph phaseGraph, phaseIndex, targetIndex int) bool {
	seen := make(map[int]bool)
	var visit func(int) bool
	visit = func(idx int) bool {
		if idx == targetIndex {
			return true
		}
		if seen[idx] {
			return false
		}
		seen[idx] = true
		for _, prereq := range graph.prereqsByIdx[idx] {
			if visit(prereq) {
				return true
			}
		}
		return false
	}
	return visit(phaseIndex)
}

func validateAutoPhaseLaunch(record FlowRecord, phaseIndex int) error {
	var phase FlowPhase
	if phaseIndex >= 0 && phaseIndex < len(record.Phases) {
		phase = record.Phases[phaseIndex]
	}
	switch {
	case !record.AutoMode:
		return fmt.Errorf("auto launch for flow %q is disabled: %w", record.FlowID, ErrAutoLaunchOutdated)
	case artifacts.NormalizePhaseID(phase.PhaseID) == "" || SemanticKind(phase) == KindMerge:
		return fmt.Errorf("auto launch target %q is not eligible: %w", phase.PhaseID, ErrAutoLaunchOutdated)
	case phase.Status != PhaseReady:
		return fmt.Errorf("auto launch target %q is %s, not ready: %w", phase.PhaseID, phase.Status, ErrAutoLaunchOutdated)
	case !phaseLaunchEligibleAtIndex(record, phaseIndex):
		return fmt.Errorf("auto launch target %q is not eligible: %w", phase.PhaseID, ErrAutoLaunchOutdated)
	default:
		return nil
	}
}

// ResetAwaitingSessionPhase removes an orphaned latest launch attempt from a
// running phase and lets approach derive it back to ready. This is intentionally
// not part of the agent-facing phase transition table.
func (s *Store) ResetAwaitingSessionPhase(update PhaseResetUpdate) (FlowRecord, error) {
	return s.ResetRecoverableRunningPhase(update)
}

// ResetRecoverableRunningPhase removes the latest stale launch attempt from a
// running phase and lets approach derive it back to ready. This is intentionally
// not part of the agent-facing phase transition table.
func (s *Store) ResetRecoverableRunningPhase(update PhaseResetUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	requestedPhaseID := strings.TrimSpace(update.PhaseID)
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if update.PhaseID == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			return FlowRecord{}, closedFlowMutationError(record, "reset a phase on")
		}
		phaseIndex := phaseIndexPreferringExactID(record.Phases, requestedPhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		selectedPhase := record.Phases[phaseIndex]
		if selectedPhase.Status != PhaseRunning {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires running recoverable phase; %s is %s", selectedPhase.PhaseID, selectedPhase.Status)
		}
		removedLaunchID := LatestPhaseLaunchID(selectedPhase)
		if removedLaunchID == "" {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires a latest launch id")
		}
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		phaseIndex = phaseIndexByID(record.Phases, update.PhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		if phase.Status != PhaseRunning {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires running recoverable phase; %s is %s", phase.PhaseID, phase.Status)
		}
		if PhaseSessionLaunchMismatch(phase) {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires attached sessions to match phase launch ids")
		}
		if !PhasePredecessorsSatisfied(record, phase.PhaseID) {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires satisfied predecessors for %s", phase.PhaseID)
		}
		if _, ok := recoverableRunningPhaseResetReasonForLaunch(phase, removedLaunchID); !ok {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires latest launch without a live attached session")
		}
		phase.LaunchIDs = removePhaseLaunchID(phase.LaunchIDs, removedLaunchID)
		phase.Sessions = removePhaseSessionsForLaunchID(phase.Sessions, removedLaunchID)
		phase.Status = PhasePending
		phase.PhaseID = update.PhaseID
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		if PhaseSessionLaunchMismatch(phase) {
			return FlowRecord{}, fmt.Errorf("flow phase reset requires attached sessions to match phase launch ids")
		}
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		resetIndex := phaseIndexByID(record.Phases, update.PhaseID)
		if resetIndex < 0 || record.Phases[resetIndex].Status != PhaseReady {
			return FlowRecord{}, fmt.Errorf("flow phase reset could not derive %s back to ready", update.PhaseID)
		}
		record.Status = DeriveStatus(record)
		return record, nil
	})
}

func removePhaseSessionsForLaunchID(values []Session, target string) []Session {
	if target == "" {
		return values
	}
	out := make([]Session, 0, len(values))
	for _, value := range values {
		if value.LaunchID == target {
			continue
		}
		out = append(out, value)
	}
	return out
}

func removePhaseLaunchID(values []string, target string) []string {
	if target == "" {
		return values
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == target {
			continue
		}
		out = append(out, value)
	}
	return out
}

// MarkPhaseLaunchEnded mirrors launch finalization into Flow-attached session
// metadata so recovery labels and reset eligibility do not depend on a later
// provider hook.
func (s *Store) MarkPhaseLaunchEnded(update PhaseLaunchEndUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	requestedPhaseID := strings.TrimSpace(update.PhaseID)
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if update.PhaseID == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	launchID := strings.TrimSpace(update.LaunchID)
	if launchID == "" {
		return FlowRecord{}, fmt.Errorf("launch id is required")
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		phaseIndex := phaseIndexPreferringExactID(record.Phases, requestedPhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		original := record
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		phaseIndex = phaseIndexByID(record.Phases, update.PhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		changed := false
		for i := range phase.Sessions {
			// The stored side is trimmed too, and for the same reason the
			// incoming one is: ingestion copies APPROACH_LAUNCH_ID into both this
			// mirror and the session store verbatim, while every reader that
			// decides whether a session still blocks its phase trims. An
			// untrimmed comparison here would end the launch in the session store
			// and leave this row live, which is the stall shape release exists to
			// clear.
			if strings.TrimSpace(phase.Sessions[i].LaunchID) != launchID {
				continue
			}
			wasEnded := strings.TrimSpace(phase.Sessions[i].Status) == "ended"
			phase.Sessions[i].Status = "ended"
			if phase.Sessions[i].EndedAt.IsZero() || (!wasEnded && update.EndedAt.After(phase.Sessions[i].EndedAt)) {
				phase.Sessions[i].EndedAt = update.EndedAt
			}
			changed = true
		}
		if !changed {
			return original, nil
		}
		phase.PhaseID = update.PhaseID
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		record.UpdatedAt = now
		return record, nil
	})
}

// AttachSession records a provider session against a phase. Re-attaching the
// same provider/session id updates the existing reference in place.
func (s *Store) AttachSession(update SessionAttachUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	requestedPhaseID := strings.TrimSpace(update.PhaseID)
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if update.PhaseID == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	if strings.TrimSpace(update.Session.Provider) == "" {
		return FlowRecord{}, fmt.Errorf("session provider is required")
	}
	if strings.TrimSpace(update.Session.SessionID) == "" {
		return FlowRecord{}, fmt.Errorf("session id is required")
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		// Attaching a session is metadata-only and never changes phase status,
		// so prefer the row that matches the id exactly: when a legacy record
		// still holds a stale duplicate ahead of the active row, collapsing
		// into the first normalized match would silently drop the active
		// row's status.
		phaseIndex := phaseIndexPreferringExactID(record.Phases, requestedPhaseID)
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		session := update.Session
		replaced := false
		for i, existing := range phase.Sessions {
			if sameSession(existing, session) {
				if launchPrecedes(phase.LaunchIDs, session.LaunchID, existing.LaunchID) {
					return record, nil
				}
				phase.Sessions[i] = session
				replaced = true
				break
			}
		}
		if !replaced {
			phase.Sessions = append(phase.Sessions, session)
		}
		phase.PhaseID = update.PhaseID
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.Phases = collapseDuplicatePhaseRows(record.Phases, phaseIndex)
		record.UpdatedAt = now
		return record, nil
	})
}

func launchPrecedes(launchIDs []string, candidate, current string) bool {
	candidateIndex, currentIndex := -1, -1
	for i, launchID := range launchIDs {
		if launchID == candidate {
			candidateIndex = i
		}
		if launchID == current {
			currentIndex = i
		}
	}
	return candidateIndex >= 0 && currentIndex >= 0 && candidateIndex < currentIndex
}

// Delete removes only the requested Flow row. It takes the launch/close
// reservation so a delete and same-ID recreate cannot replace a Flow while an
// admitted launch still owns that identity.
func (s *Store) Delete(flowID string) error {
	if err := validateFlowID(flowID); err != nil {
		return err
	}
	release, err := s.acquireLaunchCloseLock(flowID)
	if err != nil {
		return err
	}
	defer release()
	return s.backend.delete(flowID)
}

func (s *Store) updateFlow(flowID string, mutate func(FlowRecord, time.Time) (FlowRecord, error)) (FlowRecord, error) {
	return s.updateFlowWithReadiness(flowID, true, mutate)
}

func (s *Store) updateFlowMetadataOnly(flowID string, mutate func(FlowRecord, time.Time) (FlowRecord, error)) (FlowRecord, error) {
	return s.updateFlowWithReadiness(flowID, false, mutate)
}

func (s *Store) updateFlowWithReadiness(flowID string, selfHealOnRead bool, mutate func(FlowRecord, time.Time) (FlowRecord, error)) (FlowRecord, error) {
	return s.backend.update(flowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !ok {
			return FlowRecord{}, flowNotFoundError(flowID)
		}
		record := stored.record
		if selfHealOnRead {
			if err := validatePhaseGraphResolved(record); err != nil {
				return FlowRecord{}, err
			}
		}
		record, err = mutate(record, flowMutationTime(record, s.now()))
		if err != nil {
			return FlowRecord{}, err
		}
		record = normalizeRecordBase(record)
		record.Status = DeriveStatus(record)
		if err := s.saveSession(sess, record); err != nil {
			return FlowRecord{}, err
		}
		return record, nil
	})
}

// fencingPresetName returns the normalized preset name when a record's phase
// graph could not be rebuilt from it, which fences phase mutation on that
// record, and "" when the record is not fenced.
func fencingPresetName(record FlowRecord) string {
	if record.GraphRecovery.Status != GraphRecoveryMissingEdgesUnresolved {
		return ""
	}
	preset := normalizePresetName(record.PresetName)
	if preset == "" || preset == "default" {
		return ""
	}
	return preset
}

func mutationFencedByPreset(record FlowRecord) bool {
	return fencingPresetName(record) != ""
}

func validatePhaseGraphResolved(record FlowRecord) error {
	preset := fencingPresetName(record)
	if preset == "" {
		return nil
	}
	// Two things this deliberately does NOT say.
	//
	// "Restore the preset and retry" is false: under the file store edge recovery
	// re-ran on every read and restoring the preset really did heal the record,
	// but records are canonical at write time now, so recovery ran once during
	// migration and never runs again.
	//
	// "Remove approach.db and re-run the migration" is worse than false — it
	// works, and it destroys data. flows/ is a snapshot frozen at migration time,
	// not a live mirror; nothing has written to it since. Re-running the cutover
	// discards every Flow created or changed since that day. It is only safe in
	// the minutes after the migration, which is the one place it is offered (see
	// reportCutover), and it must never be offered here, where by definition the
	// database has been in use.
	// The manual repair names BOTH edits on purpose. This check keys off the
	// graph_recovery marker, not off the edges, so setting depends_on alone
	// leaves the record fenced exactly as before — a half-stated recipe reads as
	// a complete one and sends the operator away convinced the product is broken.
	return fmt.Errorf("flow %q has unresolved missing dependencies from preset %q;"+
		" its phase graph could not be rebuilt during the one-time legacy migration and cannot be"+
		" rebuilt now, because preset edge recovery does not run again — recreate this Flow, or repair"+
		" it directly in the flow database by setting depends_on on its phases AND removing the"+
		" %q marker from its stored record (see docs/config.md)",
		record.FlowID, preset, GraphRecoveryMissingEdgesUnresolved)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// List returns records matching filter, sorted by UpdatedAt descending.
func (s *Store) List(filter FlowFilter) ([]FlowRecord, error) {
	stored, err := s.backend.list(filter)
	if _, partial := AsPartialList(err); err != nil && !partial {
		return nil, err
	}
	records := make([]FlowRecord, 0, len(stored))
	for _, flow := range stored {
		records = append(records, flow.record)
	}
	return records, err
}

func validatePhaseUpdate(current FlowPhase, update PhaseUpdate) error {
	if strings.TrimSpace(update.Status) == "" {
		return fmt.Errorf("phase status is required")
	}
	if update.Status == PhaseReady {
		return fmt.Errorf("cannot set phase status to ready; readiness is derived")
	}
	if !slices.Contains(agentSettablePhaseStatuses, update.Status) {
		return fmt.Errorf("invalid phase status %q", update.Status)
	}
	if update.Status == PhaseSkipped && strings.TrimSpace(update.Notes) == "" {
		return fmt.Errorf("skipped phase requires notes")
	}
	if err := validatePlanReviewUpdate(current, update); err != nil {
		return err
	}
	if current.Status == update.Status {
		return nil
	}
	if !phaseTransitionAllowed(current.Status, update.Status) {
		return invalidPhaseTransitionError(current.Status, update.Status)
	}
	restarting := current.Status == PhaseNeedsAttention || current.Status == PhaseBlocked
	if restarting && update.Status == PhaseRunning && strings.TrimSpace(update.Notes) == "" {
		return fmt.Errorf("restarting %s phase requires notes", current.Status)
	}
	return nil
}

func phaseTransitionAllowed(currentStatus, nextStatus string) bool {
	return slices.Contains(phaseTransitions[currentStatus], nextStatus)
}

func invalidPhaseTransitionError(currentStatus, nextStatus string) error {
	message := fmt.Sprintf("invalid phase transition %s -> %s", currentStatus, nextStatus)
	if allowed := AllowedNextPhaseStatuses(currentStatus); len(allowed) > 0 {
		message += fmt.Sprintf("; allowed from %s: %s", currentStatus, strings.Join(allowed, ", "))
	}
	if (currentStatus == PhaseNeedsAttention || currentStatus == PhaseBlocked) && nextStatus == PhaseCompleted {
		message += "; restart with --status running --notes before completing"
	}
	return fmt.Errorf("%s", message)
}

func validateChildPhaseUpdate(update ChildPhaseUpdate) error {
	if err := validateFlowID(update.FlowID); err != nil {
		return err
	}
	if err := validatePhaseID(update.ParentPhaseID); err != nil {
		return fmt.Errorf("invalid parent phase id: %w", err)
	}
	if err := validatePhaseID(update.PhaseID); err != nil {
		return err
	}
	if update.PhaseID == update.ParentPhaseID {
		return fmt.Errorf("child phase id must differ from parent phase id")
	}
	if strings.TrimSpace(update.Title) == "" {
		return fmt.Errorf("child phase title is required")
	}
	if update.Order < 1 {
		return fmt.Errorf("child phase order must be positive")
	}
	return nil
}

func refreshPhaseReadiness(record FlowRecord, now time.Time) FlowRecord {
	record.Phases = OrderedPhases(record.Phases)
	record.Phases = normalizeDependsOnValues(record.Phases)
	graph := buildPhaseGraph(record.Phases)
	failedPlanReview := make(map[int]bool)
	for _, i := range graph.topo {
		phase := record.Phases[i]
		dependenciesSatisfied := allDependencyGatesSatisfied(record, graph, i)
		resetBlockedDownstream := phaseDependsOnPlanReviewFailure(record, graph, i, failedPlanReview)
		if dependenciesSatisfied && phase.Status == PhasePending {
			phase.Status = PhaseReady
			phase.UpdatedAt = now
			record.Phases[i] = phase
		} else if !dependenciesSatisfied && shouldResetBlockedDownstreamPhase(phase, resetBlockedDownstream) {
			phase.Status = PhasePending
			phase.Outcome = ""
			phase.UpdatedAt = now
			record.Phases[i] = phase
		}
		phase = record.Phases[i]
		if !phaseSatisfiesDownstreamGate(record, phase) {
			if SemanticKind(phase) == KindPlanReview {
				failedPlanReview[i] = true
			}
			if phaseDependsOnPlanReviewFailure(record, graph, i, failedPlanReview) {
				failedPlanReview[i] = true
			}
		}
	}
	return record
}

func orderImplementationChildren(phases []FlowPhase, parentPhaseID string) []FlowPhase {
	if phaseIndexByID(phases, parentPhaseID) < 0 {
		return phases
	}
	return OrderedPhases(phases)
}

// OrderedPhases returns phases with child phases grouped directly below their
// parent, sorting siblings by Order and then phase id. Top-level phase order is
// otherwise preserved for backward compatibility with existing records.
func OrderedPhases(phases []FlowPhase) []FlowPhase {
	if len(phases) == 0 {
		return nil
	}
	childrenByParent := make(map[string][]FlowPhase)
	for _, phase := range phases {
		if phase.ParentPhaseID != "" {
			childrenByParent[phase.ParentPhaseID] = append(childrenByParent[phase.ParentPhaseID], phase)
		}
	}
	for parentID := range childrenByParent {
		sort.SliceStable(childrenByParent[parentID], func(i, j int) bool {
			left := childrenByParent[parentID][i]
			right := childrenByParent[parentID][j]
			if left.Order == right.Order {
				return left.PhaseID < right.PhaseID
			}
			return left.Order < right.Order
		})
	}
	out := make([]FlowPhase, 0, len(phases))
	insertedChildren := make(map[string]bool)
	for _, phase := range phases {
		if phase.ParentPhaseID != "" {
			continue
		}
		out = append(out, phase)
		if children := childrenByParent[phase.PhaseID]; len(children) > 0 {
			out = append(out, children...)
			insertedChildren[phase.PhaseID] = true
		}
	}
	for _, phase := range phases {
		if phase.ParentPhaseID != "" && !insertedChildren[phase.ParentPhaseID] {
			out = append(out, phase)
		}
	}
	return out
}

func shouldResetBlockedDownstreamPhase(phase FlowPhase, resetBlocked bool) bool {
	switch phase.Status {
	case PhaseReady, PhaseRunning, PhaseNeedsAttention, PhaseCompleted, PhaseSkipped:
		return true
	case PhaseBlocked:
		return resetBlocked
	default:
		return false
	}
}

func phaseSatisfiesDownstreamGate(record FlowRecord, phase FlowPhase) bool {
	switch SemanticKind(phase) {
	case KindPlanReview:
		switch phase.Status {
		case PhaseSkipped:
			return strings.TrimSpace(phase.Notes) != ""
		case PhaseCompleted:
			return phase.Outcome == OutcomeApproved || phase.Outcome == OutcomeApprovedWithConcerns
		default:
			return false
		}
	case KindPRCreation:
		return phase.Status == PhaseCompleted && HasPRTarget(record.PR)
	}
	if phase.Status == PhaseSkipped {
		return strings.TrimSpace(phase.Notes) != ""
	}
	return phase.Status == PhaseCompleted
}

// PhaseGateSatisfied reports whether phase satisfies the semantic gate that
// unlocks downstream phases in the Flow graph.
func PhaseGateSatisfied(record FlowRecord, phase FlowPhase) bool {
	return phaseSatisfiesDownstreamGate(record, phase)
}

// SemanticKind returns the normalized semantic kind for a phase. Persisted kind
// wins; otherwise default preset phase IDs are inferred for legacy records.
func SemanticKind(phase FlowPhase) string {
	if kind := strings.ToLower(strings.TrimSpace(phase.Kind)); kind != "" {
		return kind
	}
	switch artifacts.NormalizePhaseID(phase.PhaseID) {
	case "plan":
		return KindPlan
	case "plan-review":
		return KindPlanReview
	case "implementation":
		return KindImplementation
	case "review-loop":
		return KindReviewLoop
	case "pr-creation":
		return KindPRCreation
	case "autoreview":
		return KindAutoreview
	case "merge":
		return KindMerge
	default:
		return ""
	}
}

// FindPhaseByKind returns the first phase with the requested semantic kind.
func FindPhaseByKind(record FlowRecord, kind string) (FlowPhase, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, phase := range OrderedPhases(record.Phases) {
		if SemanticKind(phase) == kind {
			return phase, true
		}
	}
	return FlowPhase{}, false
}

// PhasePredecessorsSatisfied reports whether all graph prerequisites for
// phaseID satisfy the Flow gate rules used to derive downstream readiness.
func PhasePredecessorsSatisfied(record FlowRecord, phaseID string) bool {
	ordered := backfillLinearDependsOnForCreate(OrderedPhases(record.Phases))
	graph := buildPhaseGraph(ordered)
	idx := phaseIndexByID(ordered, phaseID)
	if idx < 0 || !graph.released[idx] {
		return false
	}
	return allDependencyGatesSatisfied(FlowRecord{PR: record.PR, Phases: ordered}, graph, idx)
}

// HasPRTarget reports whether PR metadata contains enough target context for
// downstream Autoreview work.
func HasPRTarget(pr PullRequest) bool {
	if strings.ToLower(strings.TrimSpace(pr.Provider)) != "github" ||
		pr.Number <= 0 ||
		strings.TrimSpace(pr.HeadBranch) == "" ||
		strings.TrimSpace(pr.BaseBranch) == "" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(pr.URL))
	return err == nil &&
		parsed.Host != "" &&
		(parsed.Scheme == "https" || parsed.Scheme == "http") &&
		validateGitHubPRURL(parsed, pr.Number) == nil
}

// HasIssueTarget reports whether issue metadata contains enough target context
// to open the issue in a browser.
func HasIssueTarget(issue Issue) bool {
	if strings.ToLower(strings.TrimSpace(issue.Provider)) != "github" || issue.Number <= 0 {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(issue.URL))
	return err == nil &&
		parsed.Host != "" &&
		(parsed.Scheme == "https" || parsed.Scheme == "http") &&
		validateGitHubIssueURL(parsed, issue.Number) == nil
}

func validatePRUpdate(record FlowRecord, update PRUpdate) (PullRequest, error) {
	provider := strings.ToLower(strings.TrimSpace(update.Provider))
	if provider != "github" {
		return PullRequest{}, fmt.Errorf("unsupported PR provider %q", update.Provider)
	}
	if update.Number <= 0 {
		return PullRequest{}, fmt.Errorf("PR number must be positive")
	}
	prURL := strings.TrimSpace(update.URL)
	parsed, err := url.Parse(prURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return PullRequest{}, fmt.Errorf("PR URL must be an absolute http(s) URL")
	}
	if err := validateGitHubPRURL(parsed, update.Number); err != nil {
		return PullRequest{}, err
	}
	head := strings.TrimSpace(update.HeadBranch)
	if head == "" {
		return PullRequest{}, fmt.Errorf("PR head branch is required")
	}
	base := strings.TrimSpace(update.BaseBranch)
	if base == "" {
		return PullRequest{}, fmt.Errorf("PR base branch is required")
	}
	flowBranch := strings.TrimSpace(record.Branch)
	if flowBranch == "" {
		return PullRequest{}, fmt.Errorf("flow branch is required before recording PR metadata")
	}
	if head != flowBranch {
		return PullRequest{}, fmt.Errorf("PR head branch %q must match flow branch %q", head, flowBranch)
	}
	return PullRequest{
		Provider:   provider,
		Number:     update.Number,
		URL:        prURL,
		HeadBranch: head,
		BaseBranch: base,
		Status:     strings.TrimSpace(update.Status),
	}, nil
}

func validateIssueUpdate(update IssueUpdate) (Issue, error) {
	provider := strings.ToLower(strings.TrimSpace(update.Provider))
	if provider != "github" {
		return Issue{}, fmt.Errorf("unsupported issue provider %q", update.Provider)
	}
	if update.Number <= 0 {
		return Issue{}, fmt.Errorf("issue number must be positive")
	}
	issueURL := strings.TrimSpace(update.URL)
	parsed, err := url.Parse(issueURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return Issue{}, fmt.Errorf("issue URL must be an absolute http(s) URL")
	}
	if err := validateGitHubIssueURL(parsed, update.Number); err != nil {
		return Issue{}, err
	}
	return Issue{
		Provider: provider,
		Number:   update.Number,
		URL:      issueURL,
	}, nil
}

func validateGitHubPRURL(parsed *url.URL, number int) error {
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return fmt.Errorf("GitHub PR URL must use github.com")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" {
		return fmt.Errorf("GitHub PR URL must have /owner/repo/pull/number path")
	}
	urlNumber, err := strconv.Atoi(parts[3])
	if err != nil || urlNumber <= 0 {
		return fmt.Errorf("GitHub PR URL must have numeric pull request number")
	}
	if urlNumber != number {
		return fmt.Errorf("GitHub PR URL number %d must match PR number %d", urlNumber, number)
	}
	return nil
}

func validateGitHubIssueURL(parsed *url.URL, number int) error {
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return fmt.Errorf("GitHub issue URL must use github.com")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "issues" {
		return fmt.Errorf("GitHub issue URL must have /owner/repo/issues/number path")
	}
	urlNumber, err := strconv.Atoi(parts[3])
	if err != nil || urlNumber <= 0 {
		return fmt.Errorf("GitHub issue URL must have numeric issue number")
	}
	if urlNumber != number {
		return fmt.Errorf("GitHub issue URL number %d must match issue number %d", urlNumber, number)
	}
	return nil
}

func validateMergeUpdate(record FlowRecord, update MergeUpdate) (Merge, error) {
	status := strings.TrimSpace(update.Status)
	switch status {
	case MergeMerged:
		if !HasPRTarget(record.PR) {
			return Merge{}, fmt.Errorf("merge status merged requires existing PR metadata")
		}
		commit := strings.TrimSpace(update.Commit)
		if commit == "" {
			return Merge{}, fmt.Errorf("merge status merged requires merge commit")
		}
		if update.MergedAt.IsZero() {
			return Merge{}, fmt.Errorf("merge status merged requires merge timestamp")
		}
		phaseIndex := mergePhaseIndex(record)
		if phaseIndex < 0 || record.Phases[phaseIndex].Status != PhaseCompleted {
			return Merge{}, fmt.Errorf("merge status merged requires completed merge phase")
		}
		mergedAt := update.MergedAt.UTC()
		return Merge{Status: MergeMerged, Commit: commit, MergedAt: &mergedAt}, nil
	case MergeBlocked:
		phaseIndex := mergePhaseIndex(record)
		if phaseIndex < 0 || record.Phases[phaseIndex].Status != PhaseBlocked || strings.TrimSpace(record.Phases[phaseIndex].Notes) == "" {
			return Merge{}, fmt.Errorf("merge status blocked requires blocked merge phase notes")
		}
		return Merge{Status: MergeBlocked}, nil
	default:
		return Merge{}, fmt.Errorf("invalid merge status %q", update.Status)
	}
}

func validateManualMergeUpdate(record FlowRecord, phase FlowPhase, update ManualMergeUpdate) (Merge, error) {
	if !HasPRTarget(record.PR) {
		return Merge{}, fmt.Errorf("manual merge requires existing PR metadata")
	}
	if update.PRNumber <= 0 || strings.TrimSpace(update.PRURL) == "" {
		return Merge{}, fmt.Errorf("manual merge requires verified PR target")
	}
	if update.PRNumber != record.PR.Number || strings.TrimSpace(update.PRURL) != strings.TrimSpace(record.PR.URL) {
		return Merge{}, fmt.Errorf("manual merge PR target changed after verification")
	}
	commit := strings.TrimSpace(update.Commit)
	if commit == "" {
		return Merge{}, fmt.Errorf("manual merge requires merge commit")
	}
	if update.MergedAt.IsZero() {
		return Merge{}, fmt.Errorf("manual merge requires merge timestamp")
	}
	if !PhasePredecessorsSatisfied(record, phase.PhaseID) {
		return Merge{}, fmt.Errorf("manual merge requires satisfied predecessors for %s", phase.PhaseID)
	}
	mergedAt := update.MergedAt.UTC()
	merge := Merge{Status: MergeMerged, Commit: commit, MergedAt: &mergedAt}
	switch phase.Status {
	case PhaseReady:
		if err := validatePhaseUpdate(phase, PhaseUpdate{
			FlowID:  update.FlowID,
			PhaseID: phase.PhaseID,
			Status:  PhaseCompleted,
			Outcome: MergeMerged,
		}); err != nil {
			return Merge{}, err
		}
	case PhaseCompleted:
		if record.Merge.Status == MergeMerged && !mergeEqual(record.Merge, merge) {
			return Merge{}, fmt.Errorf("manual merge metadata differs from existing merge metadata")
		}
	default:
		return Merge{}, fmt.Errorf("manual merge requires merge phase ready or completed; merge is %s", phase.Status)
	}
	return merge, nil
}

func mergeEqual(left, right Merge) bool {
	if left.Status != right.Status || left.Commit != right.Commit {
		return false
	}
	switch {
	case left.MergedAt == nil && right.MergedAt == nil:
		return true
	case left.MergedAt == nil || right.MergedAt == nil:
		return false
	default:
		return left.MergedAt.Equal(*right.MergedAt)
	}
}

func validatePlanReviewUpdate(current FlowPhase, update PhaseUpdate) error {
	if SemanticKind(current) != KindPlanReview {
		return nil
	}
	if current.Status == PhasePending && update.Status != PhaseSkipped {
		return nil
	}
	outcome := strings.TrimSpace(update.Outcome)
	notes := strings.TrimSpace(update.Notes)
	if outcome == "" {
		switch update.Status {
		case PhaseCompleted:
			return fmt.Errorf("%s completed requires outcome approved or approved_with_concerns", reviewPhaseLabel(current))
		case PhaseNeedsAttention:
			return fmt.Errorf("%s needs_attention requires outcome changes_requested", reviewPhaseLabel(current))
		case PhaseBlocked:
			return fmt.Errorf("%s blocked requires outcome blocked", reviewPhaseLabel(current))
		}
		return nil
	}
	if update.Status == PhaseBlocked && outcome != OutcomeBlocked {
		return fmt.Errorf("%s blocked requires outcome blocked", reviewPhaseLabel(current))
	}
	switch outcome {
	case OutcomeApproved:
		if update.Status != PhaseCompleted {
			return fmt.Errorf("%s outcome approved requires completed status", reviewPhaseLabel(current))
		}
	case OutcomeApprovedWithConcerns:
		if update.Status != PhaseCompleted {
			return fmt.Errorf("%s outcome approved_with_concerns requires completed status", reviewPhaseLabel(current))
		}
		if notes == "" {
			return fmt.Errorf("%s approved_with_concerns requires notes", reviewPhaseLabel(current))
		}
	case OutcomeChangesRequested:
		if update.Status != PhaseNeedsAttention {
			return fmt.Errorf("%s outcome changes_requested requires needs_attention status", reviewPhaseLabel(current))
		}
		if notes == "" {
			return fmt.Errorf("%s changes_requested requires notes", reviewPhaseLabel(current))
		}
	case OutcomeBlocked:
		if update.Status != PhaseBlocked {
			return fmt.Errorf("%s blocked requires outcome blocked", reviewPhaseLabel(current))
		}
		if notes == "" {
			return fmt.Errorf("%s blocked requires notes", reviewPhaseLabel(current))
		}
	default:
		return fmt.Errorf("invalid %s outcome %q", reviewPhaseLabel(current), outcome)
	}
	return nil
}

func reviewPhaseLabel(phase FlowPhase) string {
	if artifacts.NormalizePhaseID(phase.PhaseID) == "plan-review" {
		return "plan-review"
	}
	return fmt.Sprintf("%s (%s)", phase.PhaseID, KindPlanReview)
}

// DeriveStatus computes the flow-level status from phase and merge state.
func DeriveStatus(record FlowRecord) string {
	// A deliberate human close outranks anything derived from phase or merge state.
	if FlowClosed(record) {
		return StatusClosed
	}
	if record.Status == StatusAbandoned {
		return StatusAbandoned
	}
	switch record.Merge.Status {
	case MergeMerged:
		return StatusMerged
	case MergeBlocked:
		return StatusBlocked
	}
	for _, phase := range record.Phases {
		if phase.Status == PhaseBlocked {
			return StatusBlocked
		}
	}
	for _, phase := range record.Phases {
		if phase.Status == PhaseNeedsAttention {
			return StatusNeedsAttention
		}
	}
	if len(record.Phases) == 0 {
		return StatusPending
	}
	allDone := true
	anyStarted := false
	for _, phase := range record.Phases {
		switch phase.Status {
		case PhaseCompleted, PhaseSkipped:
			anyStarted = true
		case PhaseRunning:
			anyStarted = true
			allDone = false
		default:
			allDone = false
		}
	}
	if allDone {
		return StatusCompleted
	}
	if anyStarted {
		return StatusInProgress
	}
	return StatusPending
}

func defaultPhases(createdAt, updatedAt time.Time) []FlowPhase {
	return seedPhases(DefaultPreset().Phases, PhaseAgentSettings{}, createdAt, updatedAt)
}

// saveSession validates and normalizes one record, then persists it through the
// critical section the caller already holds. Normalization is deliberately in
// place: callers observe the normalized DependsOn slices on the record they
// passed in, and on every record the public API returns.
func (s *Store) saveSession(sess flowSession, record FlowRecord) error {
	if err := validateFlowID(record.FlowID); err != nil {
		return err
	}
	record.Phases = normalizeDependsOnValues(record.Phases)
	return sess.save(record)
}

func (s *Store) readRecord(flowID string) (FlowRecord, bool, error) {
	stored, ok, err := s.backend.get(flowID)
	if err != nil {
		return FlowRecord{}, false, err
	}
	if !ok {
		return FlowRecord{}, false, nil
	}
	return stored.record, true, nil
}

// canonicalizeLegacyFlow performs the one-time healing applied while importing
// historical meta.json records. Runtime SQLite reads only validate and decode.
func canonicalizeLegacyFlow(stored legacyStoredFlow, presets map[string]Preset) FlowRecord {
	record := stored.record
	presence := stored.dependsOnPresence
	if !stored.headlessPresent {
		record.Headless = true
	}
	selfHeal := rawDependsOnPresentForTopLevel(record.Phases, presence)
	record = restoreLegacyMissingDependsOn(record, presence, presets)
	unresolvedGraph := false
	if record.GraphRecovery.Status == GraphRecoveryPresetEdgesRestored {
		selfHeal = true
	} else if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved {
		selfHeal = false
		unresolvedGraph = true
	}
	if unresolvedGraph {
		record = normalizeRecordBase(record)
		record = normalizeReviewOutcomes(record)
	} else {
		record = normalizeRecord(record, selfHeal)
	}
	record.Status = DeriveStatus(record)
	return record
}

func restoreLegacyMissingDependsOn(record FlowRecord, presence []rawDependsOnState, presets map[string]Preset) FlowRecord {
	if rawDependsOnPresentForEveryTopLevel(record.Phases, presence) {
		record.Phases = normalizeDependsOnValues(record.Phases)
		return record
	}
	if preset, ok := presetForLegacyRecovery(record, presets); ok && presetMatchesRecord(preset, record) {
		record.Phases = applyPresetDependsOn(record.Phases, preset)
		record.GraphRecovery.Status = GraphRecoveryPresetEdgesRestored
		return record
	}
	if recordAllowsDefaultEdgeRecovery(record) && phaseIDsCompatibleWithDefaultSequence(record.Phases) {
		record.Phases = backfillLinearDependsOn(record.Phases)
		return record
	}
	record.Phases = normalizeDependsOnValues(record.Phases)
	record.GraphRecovery.Status = GraphRecoveryMissingEdgesUnresolved
	return record
}

func presetForLegacyRecovery(record FlowRecord, presets map[string]Preset) (Preset, bool) {
	name := normalizePresetName(record.PresetName)
	if name == "" || name == "default" || presets == nil {
		return Preset{}, false
	}
	preset, ok := presets[name]
	return preset, ok
}

func recordAllowsDefaultEdgeRecovery(record FlowRecord) bool {
	name := normalizePresetName(record.PresetName)
	return name == "" || name == "default"
}

func presetMatchesRecord(preset Preset, record FlowRecord) bool {
	var ids []string
	for _, phase := range OrderedPhases(record.Phases) {
		if phase.ParentPhaseID != "" {
			continue
		}
		ids = append(ids, artifacts.NormalizePhaseID(phase.PhaseID))
	}
	if len(ids) != len(preset.Phases) {
		return false
	}
	for i, spec := range preset.Phases {
		if ids[i] != artifacts.NormalizePhaseID(spec.ID) {
			return false
		}
	}
	return true
}

func applyPresetDependsOn(phases []FlowPhase, preset Preset) []FlowPhase {
	dependsOnByID := make(map[string][]string, len(preset.Phases))
	for _, spec := range preset.Phases {
		id := artifacts.NormalizePhaseID(spec.ID)
		dependsOnByID[id] = append([]string{}, spec.DependsOn...)
	}
	for i := range phases {
		if phases[i].ParentPhaseID != "" {
			phases[i].DependsOn = []string{}
			continue
		}
		id := artifacts.NormalizePhaseID(phases[i].PhaseID)
		phases[i].DependsOn = append([]string{}, dependsOnByID[id]...)
	}
	return normalizeDependsOnValues(phases)
}

func rawDependsOnPresentForTopLevel(phases []FlowPhase, presence []rawDependsOnState) bool {
	for i, phase := range phases {
		if phase.ParentPhaseID == "" && i < len(presence) && presence[i].Present {
			return true
		}
	}
	return false
}

func rawDependsOnPresentForEveryTopLevel(phases []FlowPhase, presence []rawDependsOnState) bool {
	seenTopLevel := false
	for i, phase := range phases {
		if phase.ParentPhaseID != "" {
			continue
		}
		seenTopLevel = true
		if i >= len(presence) || !presence[i].Present {
			return false
		}
	}
	return seenTopLevel
}

func flowNotFoundError(flowID string) error {
	return fmt.Errorf("flow %q not found: %w", flowID, errFlowNotFound)
}

func validateFlowID(flowID string) error {
	if !artifacts.IsSafeID(flowID) {
		return fmt.Errorf("invalid flow id %q", flowID)
	}
	return nil
}

func validatePhaseID(phaseID string) error {
	if !artifacts.IsSafeID(phaseID) {
		return fmt.Errorf("invalid phase id %q", phaseID)
	}
	return nil
}

func defaultTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func monotonicMutationTime(candidate time.Time, floors ...time.Time) time.Time {
	stamp := candidate.UTC()
	for _, floor := range floors {
		if floor.IsZero() {
			continue
		}
		floor = floor.UTC()
		if stamp.Before(floor) {
			stamp = floor
		}
	}
	return stamp
}

func flowMutationTime(record FlowRecord, candidate time.Time) time.Time {
	floors := []time.Time{record.CreatedAt, record.UpdatedAt}
	if record.PreparedAt != nil {
		floors = append(floors, *record.PreparedAt)
	}
	return monotonicMutationTime(candidate, floors...)
}

func normalizeRecord(record FlowRecord, selfHeal bool) FlowRecord {
	record = normalizeRecordBase(record)
	// Load-path normalization only: edge-aware records and plan-review-kind
	// records self-heal here; phase-affecting mutations call
	// refreshPhaseReadiness explicitly for every graph shape.
	if selfHeal || hasPlanReviewKind(record) {
		record = normalizeReviewOutcomes(record)
		record = refreshPhaseReadiness(record, record.UpdatedAt)
	}
	return record
}

func normalizeRecordBase(record FlowRecord) FlowRecord {
	if record.Merge.Status == "" {
		record.Merge.Status = MergePending
	}
	record.Phases = backfillPhaseKinds(record.Phases)
	record.Phases = normalizePhaseAgentSettings(record.Phases)
	return record
}

// normalizePhaseAgentSettings trims and lowercases stored phase settings on
// read. It deliberately does not validate them: an odd record still has to be
// readable, and validation belongs to the create boundary.
func normalizePhaseAgentSettings(phases []FlowPhase) []FlowPhase {
	for i := range phases {
		settings := phases[i].AgentSettings().Normalize()
		settings.Agent = agent.NormalizeStored(settings.Agent)
		phases[i] = phases[i].withAgentSettings(settings)
	}
	return phases
}

func normalizeReviewOutcomes(record FlowRecord) FlowRecord {
	for i := range record.Phases {
		phase := record.Phases[i]
		if SemanticKind(phase) != KindPlanReview {
			continue
		}
		phase.Outcome = strings.TrimSpace(phase.Outcome)
		if phase.Status == PhaseCompleted && phase.Outcome == "" {
			phase.Outcome = OutcomeApproved
		}
		record.Phases[i] = phase
	}
	return record
}

func backfillPhaseKinds(phases []FlowPhase) []FlowPhase {
	for i := range phases {
		if strings.TrimSpace(phases[i].Kind) == "" {
			phases[i].Kind = SemanticKind(phases[i])
		}
	}
	return phases
}

func hasPlanReviewKind(record FlowRecord) bool {
	_, ok := FindPhaseByKind(record, KindPlanReview)
	return ok
}

// collapseDuplicatePhaseRows keeps the row at keepIndex and drops every other
// row whose normalized phase id matches it, repairing records that duplicated
// one logical phase before phase ids were normalized. Launch and session
// history from dropped rows is merged into the survivor; dropped notes,
// summaries, and agent settings are kept only when the survivor's own fields
// are empty.
func collapseDuplicatePhaseRows(phases []FlowPhase, keepIndex int) []FlowPhase {
	survivor := phases[keepIndex]
	want := artifacts.NormalizePhaseID(survivor.PhaseID)
	kept := make([]FlowPhase, 0, len(phases))
	survivorPos := -1
	for i, phase := range phases {
		if i == keepIndex {
			survivorPos = len(kept)
			kept = append(kept, phase)
			continue
		}
		if artifacts.NormalizePhaseID(phase.PhaseID) != want {
			kept = append(kept, phase)
			continue
		}
		for _, launchID := range phase.LaunchIDs {
			survivor.LaunchIDs = appendUnique(survivor.LaunchIDs, launchID)
		}
		for _, session := range phase.Sessions {
			survivor.Sessions = appendUniqueSession(survivor.Sessions, session)
		}
		if survivor.Notes == "" {
			survivor.Notes = phase.Notes
		}
		if survivor.Summary == "" {
			survivor.Summary = phase.Summary
		}
		// Agent settings fall back as a whole triple, never per field, so a
		// survivor never ends up with a model from one row and an agent from
		// another.
		if survivor.AgentSettings().IsZero() {
			survivor = survivor.withAgentSettings(phase.AgentSettings())
		}
	}
	kept[survivorPos] = survivor
	return kept
}

func sameSession(left, right Session) bool {
	return left.Provider == right.Provider && left.SessionID == right.SessionID
}

func appendUniqueSession(sessions []Session, session Session) []Session {
	for _, existing := range sessions {
		if sameSession(existing, session) {
			return sessions
		}
	}
	return append(sessions, session)
}

// phaseIndexPreferringExactID resolves a phase like phaseIndexByID but prefers
// the row whose stored id matches phaseID exactly over an earlier row that
// only matches after normalization. Metadata-only updates use it so legacy
// duplicate rows collapse into the row the caller actually targeted.
func phaseIndexPreferringExactID(phases []FlowPhase, phaseID string) int {
	for i, phase := range phases {
		if phase.PhaseID == phaseID {
			return i
		}
	}
	return phaseIndexByID(phases, phaseID)
}

func phaseIndexByID(phases []FlowPhase, phaseID string) int {
	want := artifacts.NormalizePhaseID(phaseID)
	for i, phase := range phases {
		if artifacts.NormalizePhaseID(phase.PhaseID) == want {
			return i
		}
	}
	return -1
}

func mergePhaseIndex(record FlowRecord) int {
	for i, phase := range record.Phases {
		if SemanticKind(phase) == KindMerge {
			return i
		}
	}
	return -1
}

func resetMergePendingForActiveMergePhase(record FlowRecord, phase FlowPhase) FlowRecord {
	if SemanticKind(phase) == KindMerge && (phase.Status == PhaseRunning || phase.Status == PhaseSkipped) {
		record.Merge = Merge{Status: MergePending}
	}
	return record
}
