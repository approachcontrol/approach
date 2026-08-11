// Package flowstore persists task-centric Flow records beside the agent-session store.
//
// Agents persist Flow changes through the `approach flow` CLI
// (cmd/approach/flow.go), never by editing meta.json by hand — hand edits
// bypass the store's locking, validation, and phase-ID normalization.
package flowstore

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/planstore"
)

const schemaVersion = 1

const defaultLockTimeout = 5 * time.Second

var errFlowNotFound = errors.New("flow not found")

// ErrAutoLaunchOutdated is the sentinel every outdated-auto-launch rejection
// wraps. It is exported so callers outside this package can build the rejection
// their AutoMode handling has to survive.
var ErrAutoLaunchOutdated = errors.New("auto launch outdated")

const (
	StatusPending        = "pending"
	StatusInProgress     = "in_progress"
	StatusNeedsAttention = "needs_attention"
	StatusBlocked        = "blocked"
	StatusCompleted      = "completed"
	StatusMerged         = "merged"
	StatusAbandoned      = "abandoned"
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

// Store reads and writes flow records under an artifact root.
type Store struct {
	backend  backend
	planSync planPhaseSyncer
	// root outlives the storage seam because the plan side is only half behind
	// it: SetPhase syncs through planSync (which captures root itself), but
	// SetPlanLink still resolves plan paths and constructs a planstore.Store
	// inline. Finishing that extraction is follow-up work, not part of the
	// storage seam.
	root                      string
	now                       func() time.Time
	beforeLinkedPlanPhaseSync func(planID, phaseID string)
	presets                   map[string]Preset
}

// StoreOptions configures a Store.
type StoreOptions struct {
	Root        string
	Now         func() time.Time
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
	PhaseID       string    `json:"phase_id"`
	ParentPhaseID string    `json:"parent_phase_id,omitempty"`
	Title         string    `json:"title"`
	Kind          string    `json:"kind"`
	DependsOn     []string  `json:"depends_on"`
	Status        string    `json:"status"`
	Order         int       `json:"order"`
	Outcome       string    `json:"outcome,omitempty"`
	Notes         string    `json:"notes,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	LaunchIDs     []string  `json:"launch_ids,omitempty"`
	Sessions      []Session `json:"sessions,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// Issue stores agent-reported issue metadata.
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
	Issue         Issue       `json:"issue,omitempty"`
	PR            PullRequest `json:"pr,omitempty"`
	Merge         Merge       `json:"merge,omitempty"`
	AutoMode      bool        `json:"auto_mode,omitempty"`
	// Headless is the per-Flow manual-launch preference. Like AutoMode, it is
	// forced on at creation and can only be changed afterwards through
	// SetHeadless or CreateOptions.Headless — a value set on a record passed to
	// Create is ignored. It is written without omitempty so an explicit false
	// stays distinguishable from a legacy record that predates the field.
	Headless      bool               `json:"headless"`
	Phases        []FlowPhase        `json:"phases"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	GraphRecovery GraphRecoveryState `json:"-"`

	preserveMissingDependsOn map[string]bool
}

// GraphRecoveryState reports non-persisted recovery performed while reading a
// Flow record whose persisted graph metadata was missing or degraded.
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
	// Ordering is load-bearing: the backend's EnsureCollection failure must
	// still surface before a bad preset does. Only the lockTimeout default is
	// hoisted, because the backend needs it at construction.
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = defaultLockTimeout
	}
	store, err := newFileBackend(root, lockTimeout)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	presets, err := presetRegistry(opts.Presets)
	if err != nil {
		return nil, err
	}
	return &Store{
		backend:  store,
		planSync: planstoreSyncer{root: root},
		root:     root,
		now:      now,
		presets:  presets,
	}, nil
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

// CreateWithOptions writes a new flow record, optionally seeding empty phase
// lists from a preset instead of the default graph.
func (s *Store) CreateWithOptions(record FlowRecord, opts CreateOptions) (FlowRecord, error) {
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
			draft.Phases = seedPhases(preset.Phases, draft.CreatedAt, draft.UpdatedAt)
			draft = refreshPhaseReadiness(draft, now)
		} else {
			if err := validateDeclaredPhaseDependencies(draft.Phases); err != nil {
				return FlowRecord{}, err
			}
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
	record, ok := s.readRecord(flowID)
	if !ok {
		return FlowRecord{}, flowNotFoundError(flowID)
	}
	return record, nil
}

// SetPhase validates and persists one phase update on an existing flow.
func (s *Store) SetPhase(update PhaseUpdate) (FlowRecord, error) {
	update.PhaseID = artifacts.NormalizePhaseID(update.PhaseID)
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.backend.update(update.FlowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok := sess.get()
		if !ok {
			return FlowRecord{}, flowNotFoundError(update.FlowID)
		}
		record := s.hydrate(stored, true)
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

		now := s.now()
		phase := record.Phases[phaseIndex]
		originalStatus := phase.Status
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
		if err := s.syncLinkedPlanPhase(record, phase); err != nil {
			if originalStatus == PhaseCompleted {
				return record, nil
			}
			// The compensating write below must survive this error: see the
			// durability clause on backend.update.
			failedPhase := markPhaseSyncNeedsAttention(phase, err, now)
			if failedIndex := phaseIndexByID(record.Phases, failedPhase.PhaseID); failedIndex >= 0 {
				record.Phases[failedIndex] = failedPhase
			}
			record.UpdatedAt = now
			record = refreshPhaseReadiness(record, now)
			record.Status = DeriveStatus(record)
			if writeErr := s.saveSession(sess, record); writeErr != nil {
				return FlowRecord{}, fmt.Errorf("%w; additionally failed to persist needs_attention state: %v", err, writeErr)
			}
			// Deliberately a ZERO record beside the error, unlike
			// MarkManualMerge, which returns the compensated record. Both are
			// pinned by tests; see clause 3 on backend.update.
			return FlowRecord{}, err
		}
		return record, nil
	})
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
		child := FlowPhase{
			PhaseID:       update.PhaseID,
			ParentPhaseID: update.ParentPhaseID,
			Title:         strings.TrimSpace(update.Title),
			Kind:          KindImplementationChild,
			Status:        PhasePending,
			Order:         update.Order,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
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
	planPath, err := planstore.MarkdownPath(s.root, planID)
	if err != nil {
		return FlowRecord{}, err
	}
	if supplied := strings.TrimSpace(update.PlanPath); supplied != "" {
		if !filepath.IsAbs(supplied) {
			return FlowRecord{}, fmt.Errorf("flow plan path must be absolute: %s", supplied)
		}
		if filepath.Clean(supplied) != planPath {
			return FlowRecord{}, fmt.Errorf("flow plan path %q does not match plan %q path %q", filepath.Clean(supplied), planID, planPath)
		}
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: s.root})
	if err != nil {
		return FlowRecord{}, err
	}
	if !planStore.HasPlan(planID) {
		return FlowRecord{}, fmt.Errorf("plan %q not found", planID)
	}
	if _, err := planStore.ReadPlan(planID); err != nil {
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
func (s *Store) MarkManualMerge(update ManualMergeUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.backend.update(update.FlowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok := sess.get()
		if !ok {
			return FlowRecord{}, flowNotFoundError(update.FlowID)
		}
		record := s.hydrate(stored, true)
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
			return record, nil
		}

		now := s.now()
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
		if err := s.syncLinkedPlanPhase(record, phase); err != nil {
			// The compensating write below must survive this error: see the
			// durability clause on backend.update.
			failedPhase := markPhaseSyncNeedsAttention(phase, err, now)
			if failedIndex := phaseIndexByID(record.Phases, failedPhase.PhaseID); failedIndex >= 0 {
				record.Phases[failedIndex] = failedPhase
			}
			record.PR.Status = previousPRStatus
			record.Merge = Merge{Status: MergePending}
			record.UpdatedAt = now
			record = refreshPhaseReadiness(record, now)
			record.Status = DeriveStatus(record)
			if writeErr := s.saveSession(sess, record); writeErr != nil {
				return FlowRecord{}, fmt.Errorf("%w; additionally failed to persist needs_attention state: %v", err, writeErr)
			}
			// Deliberately the COMPENSATED record beside the error, unlike
			// SetPhase, which returns a zero record. Both are pinned by tests;
			// see clause 3 on backend.update.
			return record, err
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
			if phase.Sessions[i].LaunchID != launchID {
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

// Delete removes only the persisted Flow record directory.
func (s *Store) Delete(flowID string) error {
	if err := validateFlowID(flowID); err != nil {
		return err
	}
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
		stored, ok := sess.get()
		if !ok {
			return FlowRecord{}, flowNotFoundError(flowID)
		}
		record := s.hydrate(stored, selfHealOnRead)
		if selfHealOnRead {
			if err := validatePhaseGraphResolved(record); err != nil {
				return FlowRecord{}, err
			}
		}
		record, err := mutate(record, s.now())
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

func validatePhaseGraphResolved(record FlowRecord) error {
	if record.GraphRecovery.Status != GraphRecoveryMissingEdgesUnresolved {
		return nil
	}
	preset := normalizePresetName(record.PresetName)
	if preset == "" || preset == "default" {
		return nil
	}
	return fmt.Errorf("flow %q has unresolved missing dependencies; restore preset %q or explicit depends_on before mutating phases", record.FlowID, preset)
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
	stored, err := s.backend.list()
	if err != nil {
		return nil, err
	}
	var records []FlowRecord
	for _, flow := range stored {
		// List reaches hydration through readRecord today, so it self-heals.
		record := s.hydrate(flow, true)
		if matchesFilter(record, filter) {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})
	return records, nil
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
	return seedPhases(DefaultPreset().Phases, createdAt, updatedAt)
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

func (s *Store) readRecord(flowID string) (FlowRecord, bool) {
	stored, ok := s.backend.get(flowID)
	if !ok {
		return FlowRecord{}, false
	}
	return s.hydrate(stored, true), true
}

// hydrate turns a decoded record plus its raw encoding hints into the domain
// record the public API returns: legacy field defaults, graph recovery,
// normalization, readiness, and derived status.
func (s *Store) hydrate(stored storedFlow, selfHealOnRead bool) FlowRecord {
	record := stored.record
	presence := stored.dependsOnHints()
	// Legacy records predate per-flow headless and stored no field for it, so
	// they read back as headless rather than as Go's zero value.
	if stored.legacyEncoding && !stored.headlessPresent {
		record.Headless = true
	}
	selfHeal := rawDependsOnPresentForTopLevel(record.Phases, presence)
	record = s.restoreMissingDependsOn(record, presence)
	unresolvedGraph := false
	if record.GraphRecovery.Status == GraphRecoveryPresetEdgesRestored {
		selfHeal = true
	} else if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved {
		selfHeal = false
		unresolvedGraph = true
	}
	if selfHealOnRead {
		if unresolvedGraph {
			record = normalizeRecordBase(record)
			record = normalizeReviewOutcomes(record)
		} else {
			record = normalizeRecord(record, selfHeal)
		}
	} else {
		record = normalizeRecordBase(record)
		if unresolvedGraph {
			record.preserveMissingDependsOn = missingTopLevelDependsOnByID(record.Phases, presence)
		}
	}
	record.Status = DeriveStatus(record)
	return record
}

func (s *Store) restoreMissingDependsOn(record FlowRecord, presence []rawDependsOnState) FlowRecord {
	if rawDependsOnPresentForEveryTopLevel(record.Phases, presence) {
		record.Phases = normalizeDependsOnValues(record.Phases)
		return record
	}
	if preset, ok := s.presetForRecovery(record); ok && presetMatchesRecord(preset, record) {
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

func (s *Store) presetForRecovery(record FlowRecord) (Preset, bool) {
	name := normalizePresetName(record.PresetName)
	if name == "" || name == "default" || s.presets == nil {
		return Preset{}, false
	}
	preset, ok := s.presets[name]
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

func missingTopLevelDependsOnByID(phases []FlowPhase, presence []rawDependsOnState) map[string]bool {
	missing := make(map[string]bool)
	for i, phase := range phases {
		if phase.ParentPhaseID != "" {
			continue
		}
		if i < len(presence) && presence[i].Present {
			continue
		}
		id := artifacts.NormalizePhaseID(phase.PhaseID)
		if id != "" {
			missing[id] = true
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
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

func matchesFilter(record FlowRecord, filter FlowFilter) bool {
	if filter.RepoPath != "" && filepath.Clean(record.RepoPath) != filepath.Clean(filter.RepoPath) {
		return false
	}
	return true
}

func defaultTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
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
	return record
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
// history from dropped rows is merged into the survivor; dropped notes and
// summaries are kept only when the survivor's own fields are empty.
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
