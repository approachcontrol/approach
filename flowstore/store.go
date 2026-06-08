// Package flowstore persists task-centric Flow records beside the agent-session store.
package flowstore

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/brian-bell/wtui/internal/artifacts"
	"github.com/brian-bell/wtui/planstore"
)

const schemaVersion = 1

const defaultLockTimeout = 5 * time.Second

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

// Store reads and writes flow records under an artifact root.
type Store struct {
	root        string
	now         func() time.Time
	lockTimeout time.Duration
}

// StoreOptions configures a Store.
type StoreOptions struct {
	Root        string
	Now         func() time.Time
	LockTimeout time.Duration
}

// FlowPhase is one phase in the persisted Flow pipeline.
type FlowPhase struct {
	PhaseID       string    `json:"phase_id"`
	ParentPhaseID string    `json:"parent_phase_id,omitempty"`
	Title         string    `json:"title"`
	Kind          string    `json:"kind"`
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

// MergeUpdate records metadata for the merge that completed or blocked a Flow.
type MergeUpdate struct {
	FlowID   string
	Status   string
	Commit   string
	MergedAt time.Time
}

// Merge stores agent-reported merge metadata.
type Merge struct {
	Status   string     `json:"status,omitempty"`
	Commit   string     `json:"commit,omitempty"`
	MergedAt *time.Time `json:"merged_at,omitempty"`
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
	PlanID        string      `json:"plan_id,omitempty"`
	PlanPath      string      `json:"plan_path,omitempty"`
	PR            PullRequest `json:"pr,omitempty"`
	Merge         Merge       `json:"merge,omitempty"`
	Phases        []FlowPhase `json:"phases"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
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

// ChildPhaseUpdate creates or updates a stable child phase under Implementation.
type ChildPhaseUpdate struct {
	FlowID        string
	ParentPhaseID string
	PhaseID       string
	Title         string
	Order         int
}

// PlanLinkUpdate links a saved wtui plan artifact to an existing Flow.
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
type PhaseLaunchUpdate struct {
	FlowID   string
	PhaseID  string
	LaunchID string
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
	if err := artifacts.EnsureCollection(root, "flows"); err != nil {
		return nil, fmt.Errorf("create flow store: %w", err)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = defaultLockTimeout
	}
	return &Store{root: root, now: now, lockTimeout: lockTimeout}, nil
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
func (s *Store) Create(record FlowRecord) (FlowRecord, error) {
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
		id, err := s.generateID(record.Title)
		if err != nil {
			return FlowRecord{}, err
		}
		record.FlowID = id
	} else if err := validateFlowID(record.FlowID); err != nil {
		return FlowRecord{}, err
	} else if _, err := os.Stat(s.flowDir(record.FlowID)); err == nil {
		return FlowRecord{}, fmt.Errorf("flow %q already exists", record.FlowID)
	} else if !os.IsNotExist(err) {
		return FlowRecord{}, fmt.Errorf("check flow id collision: %w", err)
	}

	now := s.now()
	record.SchemaVersion = schemaVersion
	record.CreatedAt = defaultTime(record.CreatedAt, now)
	record.UpdatedAt = defaultTime(record.UpdatedAt, now)
	if len(record.Phases) == 0 {
		record.Phases = defaultPhases(record.CreatedAt, record.UpdatedAt)
	}
	record = normalizeRecord(record)
	record.Status = DeriveStatus(record)
	if err := s.write(record); err != nil {
		return FlowRecord{}, err
	}
	return record, nil
}

// Read returns one flow record by ID.
func (s *Store) Read(flowID string) (FlowRecord, error) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, err
	}
	record, ok := s.readRecord(flowID)
	if !ok {
		return FlowRecord{}, fmt.Errorf("flow %q not found", flowID)
	}
	return record, nil
}

// SetPhase validates and persists one phase update on an existing flow.
func (s *Store) SetPhase(update PhaseUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	if _, err := os.Stat(s.flowDir(update.FlowID)); os.IsNotExist(err) {
		return FlowRecord{}, fmt.Errorf("flow %q not found", update.FlowID)
	} else if err != nil {
		return FlowRecord{}, fmt.Errorf("stat flow %q: %w", update.FlowID, err)
	}
	release, err := s.acquireFlowLock(update.FlowID)
	if err != nil {
		return FlowRecord{}, err
	}
	defer release()
	record, ok := s.readRecord(update.FlowID)
	if !ok {
		return FlowRecord{}, fmt.Errorf("flow %q not found", update.FlowID)
	}
	phaseIndex := -1
	for i, phase := range record.Phases {
		if phase.PhaseID == update.PhaseID {
			phaseIndex = i
			break
		}
	}
	if phaseIndex < 0 {
		return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
	}

	now := s.now()
	phase := record.Phases[phaseIndex]
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
	phase.UpdatedAt = now
	record.Phases[phaseIndex] = phase
	if phase.PhaseID == "merge" && (phase.Status == PhaseRunning || phase.Status == PhaseSkipped) {
		record.Merge = Merge{Status: MergePending}
	}
	record.UpdatedAt = now
	record = refreshPhaseReadiness(record, now)
	record.Status = DeriveStatus(record)
	if err := s.write(record); err != nil {
		return FlowRecord{}, err
	}
	return record, nil
}

// AddChildPhase creates or updates a stable child phase under Implementation.
func (s *Store) AddChildPhase(update ChildPhaseUpdate) (FlowRecord, error) {
	if err := validateChildPhaseUpdate(update); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		parentIndex := phaseIndexByID(record.Phases, update.ParentPhaseID)
		if parentIndex < 0 {
			return FlowRecord{}, fmt.Errorf("parent phase %q not found in flow %q", update.ParentPhaseID, update.FlowID)
		}
		if record.Phases[parentIndex].PhaseID != "implementation" {
			return FlowRecord{}, fmt.Errorf("child phases can only be added under implementation")
		}
		childIndex := phaseIndexByID(record.Phases, update.PhaseID)
		if childIndex >= 0 {
			child := record.Phases[childIndex]
			if child.ParentPhaseID != update.ParentPhaseID {
				return FlowRecord{}, fmt.Errorf("phase %q already belongs to parent %q", update.PhaseID, child.ParentPhaseID)
			}
			if child.Title == strings.TrimSpace(update.Title) &&
				child.Kind == "implementation_child" &&
				child.Order == update.Order {
				return record, nil
			}
			child.Title = strings.TrimSpace(update.Title)
			child.Kind = "implementation_child"
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
			Kind:          "implementation_child",
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
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
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

// SetMerge validates and persists the merge metadata reported by an agent.
func (s *Store) SetMerge(update MergeUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
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
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
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

// AddPhaseLaunchID records a launch attempt and marks the phase running.
func (s *Store) AddPhaseLaunchID(update PhaseLaunchUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	if strings.TrimSpace(update.PhaseID) == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	launchID := strings.TrimSpace(update.LaunchID)
	if launchID == "" {
		return FlowRecord{}, fmt.Errorf("launch id is required")
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		phaseIndex := -1
		for i, phase := range record.Phases {
			if phase.PhaseID == update.PhaseID {
				phaseIndex = i
				break
			}
		}
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		if err := validatePhaseUpdate(phase, PhaseUpdate{FlowID: update.FlowID, PhaseID: update.PhaseID, Status: PhaseRunning}); err != nil {
			return FlowRecord{}, err
		}
		phase.Status = PhaseRunning
		if clearsPhaseOutcome(phase.Status) {
			phase.Outcome = ""
		}
		phase.LaunchIDs = appendUnique(phase.LaunchIDs, launchID)
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		if phase.PhaseID == "merge" && phase.Status == PhaseRunning {
			record.Merge = Merge{Status: MergePending}
		}
		record.UpdatedAt = now
		record = refreshPhaseReadiness(record, now)
		record.Status = DeriveStatus(record)
		return record, nil
	})
}

// AttachSession records a provider session against a phase. Re-attaching the
// same provider/session id updates the existing reference in place.
func (s *Store) AttachSession(update SessionAttachUpdate) (FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return FlowRecord{}, err
	}
	if strings.TrimSpace(update.PhaseID) == "" {
		return FlowRecord{}, fmt.Errorf("phase id is required")
	}
	if strings.TrimSpace(update.Session.Provider) == "" {
		return FlowRecord{}, fmt.Errorf("session provider is required")
	}
	if strings.TrimSpace(update.Session.SessionID) == "" {
		return FlowRecord{}, fmt.Errorf("session id is required")
	}
	return s.updateFlow(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		phaseIndex := -1
		for i, phase := range record.Phases {
			if phase.PhaseID == update.PhaseID {
				phaseIndex = i
				break
			}
		}
		if phaseIndex < 0 {
			return FlowRecord{}, fmt.Errorf("phase %q not found in flow %q", update.PhaseID, update.FlowID)
		}
		phase := record.Phases[phaseIndex]
		session := update.Session
		replaced := false
		for i, existing := range phase.Sessions {
			if existing.Provider == session.Provider && existing.SessionID == session.SessionID {
				phase.Sessions[i] = session
				replaced = true
				break
			}
		}
		if !replaced {
			phase.Sessions = append(phase.Sessions, session)
		}
		phase.UpdatedAt = now
		record.Phases[phaseIndex] = phase
		record.UpdatedAt = now
		return record, nil
	})
}

func (s *Store) updateFlow(flowID string, mutate func(FlowRecord, time.Time) (FlowRecord, error)) (FlowRecord, error) {
	if _, err := os.Stat(s.flowDir(flowID)); os.IsNotExist(err) {
		return FlowRecord{}, fmt.Errorf("flow %q not found", flowID)
	} else if err != nil {
		return FlowRecord{}, fmt.Errorf("stat flow %q: %w", flowID, err)
	}
	release, err := s.acquireFlowLock(flowID)
	if err != nil {
		return FlowRecord{}, err
	}
	defer release()
	record, ok := s.readRecord(flowID)
	if !ok {
		return FlowRecord{}, fmt.Errorf("flow %q not found", flowID)
	}
	record, err = mutate(record, s.now())
	if err != nil {
		return FlowRecord{}, err
	}
	record = normalizeRecord(record)
	record.Status = DeriveStatus(record)
	if err := s.write(record); err != nil {
		return FlowRecord{}, err
	}
	return record, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *Store) acquireFlowLock(flowID string) (func(), error) {
	lockPath := filepath.Join(s.flowDir(flowID), ".update.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, artifacts.FilePerm)
	if err != nil {
		return nil, fmt.Errorf("open flow lock: %w", err)
	}
	if err := file.Chmod(artifacts.FilePerm); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure flow lock: %w", err)
	}
	deadline := time.Now().Add(s.lockTimeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			if err := file.Truncate(0); err != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, fmt.Errorf("truncate flow lock: %w", err)
			}
			if _, err := file.Seek(0, 0); err != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, fmt.Errorf("seek flow lock: %w", err)
			}
			if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, fmt.Errorf("write flow lock: %w", err)
			}
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, fmt.Errorf("acquire flow lock: %w", err)
		}
		if !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("timed out waiting for flow lock %q", flowID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// List returns records matching filter, sorted by UpdatedAt descending.
func (s *Store) List(filter FlowFilter) ([]FlowRecord, error) {
	root := filepath.Join(s.root, "flows")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list flows: %w", err)
	}
	var records []FlowRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, ok := s.readRecord(entry.Name())
		if !ok {
			continue
		}
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
	switch update.Status {
	case PhaseRunning, PhaseNeedsAttention, PhaseCompleted, PhaseBlocked, PhaseSkipped:
	case PhaseReady:
		return fmt.Errorf("cannot set phase status to ready; readiness is derived")
	default:
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
	switch current.Status {
	case PhaseReady:
		switch update.Status {
		case PhaseRunning, PhaseCompleted, PhaseNeedsAttention, PhaseBlocked, PhaseSkipped:
			return nil
		}
	case PhaseRunning:
		switch update.Status {
		case PhaseCompleted, PhaseNeedsAttention, PhaseBlocked, PhaseSkipped:
			return nil
		}
	case PhaseNeedsAttention, PhaseBlocked:
		switch update.Status {
		case PhaseRunning, PhaseSkipped:
			if update.Status == PhaseRunning && strings.TrimSpace(update.Notes) == "" {
				return fmt.Errorf("restarting %s phase requires notes", current.Status)
			}
			return nil
		}
	case PhaseCompleted, PhaseSkipped:
		if update.Status == PhaseRunning {
			return nil
		}
	case PhasePending:
		if update.Status == PhaseSkipped {
			return nil
		}
	}
	return fmt.Errorf("invalid phase transition %s -> %s", current.Status, update.Status)
}

func validateChildPhaseUpdate(update ChildPhaseUpdate) error {
	if err := validateFlowID(update.FlowID); err != nil {
		return err
	}
	if err := validatePhaseID(update.ParentPhaseID); err != nil {
		return fmt.Errorf("invalid parent phase id: %w", err)
	}
	if update.ParentPhaseID != "implementation" {
		return fmt.Errorf("child phases can only be added under implementation")
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
	predecessorsSatisfied := true
	resetBlockedDownstream := false
	for i := range record.Phases {
		phase := record.Phases[i]
		if predecessorsSatisfied && (phase.Status == PhasePending || phase.Status == PhaseReady) {
			nextStatus := PhasePending
			nextStatus = PhaseReady
			if phase.Status != nextStatus {
				phase.Status = nextStatus
				phase.UpdatedAt = now
				record.Phases[i] = phase
			}
		} else if !predecessorsSatisfied && shouldResetBlockedDownstreamPhase(phase, resetBlockedDownstream) {
			phase.Status = PhasePending
			phase.Outcome = ""
			phase.UpdatedAt = now
			record.Phases[i] = phase
		}
		if !phaseSatisfiesDownstreamGate(record, phase) {
			predecessorsSatisfied = false
			if phase.PhaseID == "plan-review" {
				resetBlockedDownstream = true
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
	if phase.PhaseID == "plan-review" {
		switch phase.Status {
		case PhaseSkipped:
			return strings.TrimSpace(phase.Notes) != ""
		case PhaseCompleted:
			return phase.Outcome == OutcomeApproved || phase.Outcome == OutcomeApprovedWithConcerns
		default:
			return false
		}
	}
	if phase.PhaseID == "pr-creation" {
		return phase.Status == PhaseCompleted && HasPRTarget(record.PR)
	}
	if phase.Status == PhaseSkipped {
		return strings.TrimSpace(phase.Notes) != ""
	}
	return phase.Status == PhaseCompleted
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
		phaseIndex := phaseIndexByID(record.Phases, "merge")
		if phaseIndex < 0 || record.Phases[phaseIndex].Status != PhaseCompleted {
			return Merge{}, fmt.Errorf("merge status merged requires completed merge phase")
		}
		mergedAt := update.MergedAt.UTC()
		return Merge{Status: MergeMerged, Commit: commit, MergedAt: &mergedAt}, nil
	case MergeBlocked:
		phaseIndex := phaseIndexByID(record.Phases, "merge")
		if phaseIndex < 0 || record.Phases[phaseIndex].Status != PhaseBlocked || strings.TrimSpace(record.Phases[phaseIndex].Notes) == "" {
			return Merge{}, fmt.Errorf("merge status blocked requires blocked merge phase notes")
		}
		return Merge{Status: MergeBlocked}, nil
	default:
		return Merge{}, fmt.Errorf("invalid merge status %q", update.Status)
	}
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
	if current.PhaseID != "plan-review" {
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
			return fmt.Errorf("plan-review completed requires outcome approved or approved_with_concerns")
		case PhaseNeedsAttention:
			return fmt.Errorf("plan-review needs_attention requires outcome changes_requested")
		case PhaseBlocked:
			return fmt.Errorf("plan-review blocked requires outcome blocked")
		}
		return nil
	}
	if update.Status == PhaseBlocked && outcome != OutcomeBlocked {
		return fmt.Errorf("plan-review blocked requires outcome blocked")
	}
	switch outcome {
	case OutcomeApproved:
		if update.Status != PhaseCompleted {
			return fmt.Errorf("plan-review outcome approved requires completed status")
		}
	case OutcomeApprovedWithConcerns:
		if update.Status != PhaseCompleted {
			return fmt.Errorf("plan-review outcome approved_with_concerns requires completed status")
		}
		if notes == "" {
			return fmt.Errorf("plan-review approved_with_concerns requires notes")
		}
	case OutcomeChangesRequested:
		if update.Status != PhaseNeedsAttention {
			return fmt.Errorf("plan-review outcome changes_requested requires needs_attention status")
		}
		if notes == "" {
			return fmt.Errorf("plan-review changes_requested requires notes")
		}
	case OutcomeBlocked:
		if update.Status != PhaseBlocked {
			return fmt.Errorf("plan-review blocked requires outcome blocked")
		}
		if notes == "" {
			return fmt.Errorf("plan-review blocked requires notes")
		}
	default:
		return fmt.Errorf("invalid plan-review outcome %q", outcome)
	}
	return nil
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
	specs := []struct {
		id    string
		title string
		kind  string
	}{
		{"plan", "Plan", "plan"},
		{"plan-review", "Plan Review", "plan_review"},
		{"implementation", "Implementation", "implementation"},
		{"review-loop", "Review loop", "review_loop"},
		{"pr-creation", "PR creation", "pr_creation"},
		{"autoreview", "Autoreview", "autoreview"},
		{"merge", "Merge", "merge"},
	}
	phases := make([]FlowPhase, 0, len(specs))
	for i, spec := range specs {
		status := PhasePending
		if i == 0 {
			status = PhaseReady
		}
		phases = append(phases, FlowPhase{
			PhaseID:   spec.id,
			Title:     spec.title,
			Kind:      spec.kind,
			Status:    status,
			Order:     i + 1,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}
	return phases
}

func (s *Store) generateID(title string) (string, error) {
	return artifacts.AllocateTimestampedID(artifacts.IDOptions{
		Root:         s.root,
		Collection:   "flows",
		Title:        title,
		FallbackSlug: "flow",
		Kind:         "flow",
		Now:          s.now(),
	})
}

func (s *Store) write(record FlowRecord) error {
	if err := validateFlowID(record.FlowID); err != nil {
		return err
	}
	dir, err := artifacts.EnsureRecordDir(s.root, "flows", record.FlowID)
	if err != nil {
		return fmt.Errorf("secure flow directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode flow metadata: %w", err)
	}
	if err := artifacts.WriteFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
		return fmt.Errorf("write flow metadata: %w", err)
	}
	return nil
}

func (s *Store) readRecord(flowID string) (FlowRecord, bool) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, false
	}
	data, err := os.ReadFile(filepath.Join(s.flowDir(flowID), "meta.json"))
	if err != nil {
		return FlowRecord{}, false
	}
	var record FlowRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return FlowRecord{}, false
	}
	if record.FlowID != flowID || record.SchemaVersion != schemaVersion {
		return FlowRecord{}, false
	}
	record = normalizeRecord(record)
	record.Status = DeriveStatus(record)
	return record, true
}

func (s *Store) flowDir(flowID string) string {
	return artifacts.RecordDir(s.root, "flows", flowID)
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

func normalizeRecord(record FlowRecord) FlowRecord {
	if record.Merge.Status == "" {
		record.Merge.Status = MergePending
	}
	if hasPhase(record, "plan-review") {
		record = normalizePlanReviewOutcomes(record)
		record = refreshPhaseReadiness(record, record.UpdatedAt)
	}
	return record
}

func normalizePlanReviewOutcomes(record FlowRecord) FlowRecord {
	for i := range record.Phases {
		phase := record.Phases[i]
		if phase.PhaseID != "plan-review" {
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

func hasPhase(record FlowRecord, phaseID string) bool {
	return phaseIndexByID(record.Phases, phaseID) >= 0
}

func phaseIndexByID(phases []FlowPhase, phaseID string) int {
	for i, phase := range phases {
		if phase.PhaseID == phaseID {
			return i
		}
	}
	return -1
}
