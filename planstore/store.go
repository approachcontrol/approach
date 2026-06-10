// Package planstore persists saved agent plans beside the agent-session store.
//
// Plans live under <artifact-root>/plans/<plan-id>/ with a meta.json metadata
// file and a plan.md Markdown body. The artifact root is shared with the
// sessions store (see the sessions package); moving or cleaning that root also
// moves or removes saved plans.
package planstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brian-bell/wtui/internal/artifacts"
)

const schemaVersion = 1

var validStatuses = map[string]bool{
	"draft":       true,
	"approved":    true,
	"in_progress": true,
	"completed":   true,
	"blocked":     true,
	"superseded":  true,
}

var validPhaseStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"completed":   true,
	"blocked":     true,
	"skipped":     true,
}

// Store reads and writes saved plans under an artifact root.
type Store struct {
	root string
	now  func() time.Time
}

// StoreOptions configures a Store.
type StoreOptions struct {
	Root string
	// Now overrides the clock used for generated IDs and timestamps. Tests use
	// it for deterministic output; production leaves it nil (UTC wall clock).
	Now func() time.Time
}

// PlanPhase is one ordered step within a plan.
type PlanPhase struct {
	PhaseID string `json:"phase_id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Order   int    `json:"order"`
}

// PlanningSession links a plan to the agent session that produced it. All
// fields are best-effort; v1 does not require a provider-native session ID.
type PlanningSession struct {
	Provider  string `json:"provider,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	LaunchID  string `json:"launch_id,omitempty"`
}

// PlanRecord is the full persisted plan: metadata plus Markdown body.
type PlanRecord struct {
	SchemaVersion int         `json:"schema_version"`
	PlanID        string      `json:"plan_id"`
	Title         string      `json:"title"`
	Summary       string      `json:"summary,omitempty"`
	Status        string      `json:"status"`
	Source        string      `json:"source,omitempty"`
	Provider      string      `json:"provider,omitempty"`
	SessionID     string      `json:"session_id,omitempty"`
	LaunchID      string      `json:"launch_id,omitempty"`
	RepoPath      string      `json:"repo_path,omitempty"`
	WorktreePath  string      `json:"worktree_path,omitempty"`
	Branch        string      `json:"branch,omitempty"`
	Commit        string      `json:"commit,omitempty"`
	Phases        []PlanPhase `json:"phases,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`

	// Markdown is the plan body, stored in plan.md rather than meta.json.
	Markdown string `json:"-"`
}

// PlanFilter narrows the plans returned by List. An empty filter matches all.
type PlanFilter struct {
	RepoPath string
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
	root, err := artifacts.RequireAbsoluteRoot(root, "plan")
	if err != nil {
		return nil, err
	}
	if err := artifacts.EnsureCollection(root, "plans"); err != nil {
		return nil, fmt.Errorf("create plan store: %w", err)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{root: root, now: now}, nil
}

// DefaultRoot returns the default artifact root, matching sessions.DefaultRoot.
func DefaultRoot() (string, error) {
	root, err := artifacts.DefaultRoot()
	if err != nil {
		return "", fmt.Errorf("resolve plan state root: %w", err)
	}
	return root, nil
}

// MarkdownPath returns the expected plan.md path for planID without reading it.
func MarkdownPath(root, planID string) (string, error) {
	if err := validatePlanID(planID); err != nil {
		return "", err
	}
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return "", err
		}
	}
	root, err := artifacts.RequireAbsoluteRoot(root, "plan")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "plans", planID, "plan.md"), nil
}

// HasPlan reports whether a valid plan record already exists.
func (s *Store) HasPlan(planID string) bool {
	_, ok := s.readRecord(planID)
	return ok
}

// ReadMetadata returns one plan metadata record by ID without reading plan.md.
func (s *Store) ReadMetadata(planID string) (PlanRecord, error) {
	return s.readMetadataStrict(planID)
}

// Save writes a plan record and returns its plan ID. When a plan with the same
// ID already exists, Markdown and Title are always replaced from the incoming
// record (both are required), while Status, Source, Summary, phases,
// repo/session fields, and CreatedAt are preserved unless the incoming record
// supplies a new value.
func (s *Store) Save(record PlanRecord) (string, error) {
	if strings.TrimSpace(record.Title) == "" {
		return "", fmt.Errorf("plan title is required")
	}
	if record.Markdown == "" {
		return "", fmt.Errorf("plan content (markdown) is required")
	}
	// Validate a supplied status; an empty status defaults to draft only for a
	// brand-new plan (handled after the merge so updates keep the prior status).
	if record.Status != "" && !validStatuses[record.Status] {
		return "", fmt.Errorf("invalid plan status %q", record.Status)
	}
	if record.PlanID == "" {
		generated, err := s.generateID(record.Title)
		if err != nil {
			return "", err
		}
		record.PlanID = generated
	} else if err := validatePlanID(record.PlanID); err != nil {
		return "", err
	}

	now := s.now()
	if existing, ok := s.readRecord(record.PlanID); ok {
		record = mergeRecord(existing, record)
	}
	if record.Status == "" {
		record.Status = "draft"
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = schemaVersion
	}
	if err := s.write(record); err != nil {
		return "", err
	}
	return record.PlanID, nil
}

// SetPhase creates or updates a single ordered phase on an existing plan. The
// phase is matched by PhaseID; phases are kept sorted by Order.
func (s *Store) SetPhase(planID string, phase PlanPhase) error {
	if err := validatePlanID(planID); err != nil {
		return err
	}
	if phase.Status == "" {
		phase.Status = "pending"
	}
	if !validPhaseStatuses[phase.Status] {
		return fmt.Errorf("invalid phase status %q", phase.Status)
	}
	if strings.TrimSpace(phase.Title) == "" {
		return fmt.Errorf("phase title is required")
	}
	phase.PhaseID = artifacts.NormalizePhaseID(phase.PhaseID)
	if phase.PhaseID == "" {
		return fmt.Errorf("phase id is required")
	}
	record, ok := s.readRecord(planID)
	if !ok {
		return fmt.Errorf("plan %q not found", planID)
	}

	updated := false
	kept := record.Phases[:0]
	for _, existing := range record.Phases {
		if artifacts.NormalizePhaseID(existing.PhaseID) != phase.PhaseID {
			kept = append(kept, existing)
			continue
		}
		// Collapse every spelling of this logical phase into the one updated row.
		if !updated {
			kept = append(kept, phase)
			updated = true
		}
	}
	record.Phases = kept
	if !updated {
		record.Phases = append(record.Phases, phase)
	}
	sort.SliceStable(record.Phases, func(i, j int) bool {
		return record.Phases[i].Order < record.Phases[j].Order
	})

	record.UpdatedAt = s.now()
	return s.write(record)
}

// ReadPlan returns the Markdown body for a plan.
func (s *Store) ReadPlan(planID string) (string, error) {
	if err := validatePlanID(planID); err != nil {
		return "", err
	}
	dir := s.planDir(planID)
	markdown, err := os.ReadFile(filepath.Join(dir, "plan.md"))
	if err != nil {
		return "", fmt.Errorf("read plan: %w", err)
	}
	return string(markdown), nil
}

// mergeRecord folds an incoming update onto the existing on-disk record.
func mergeRecord(existing, incoming PlanRecord) PlanRecord {
	merged := incoming
	merged.CreatedAt = existing.CreatedAt
	if !incoming.CreatedAt.IsZero() {
		merged.CreatedAt = incoming.CreatedAt
	}
	// Bump UpdatedAt on every update unless the caller pins it explicitly.
	merged.UpdatedAt = incoming.UpdatedAt
	merged.Status = preferString(incoming.Status, existing.Status)
	merged.Summary = preferString(incoming.Summary, existing.Summary)
	merged.Source = preferString(incoming.Source, existing.Source)
	merged.Provider = preferString(incoming.Provider, existing.Provider)
	merged.SessionID = preferString(incoming.SessionID, existing.SessionID)
	merged.LaunchID = preferString(incoming.LaunchID, existing.LaunchID)
	merged.RepoPath = preferString(incoming.RepoPath, existing.RepoPath)
	merged.WorktreePath = preferString(incoming.WorktreePath, existing.WorktreePath)
	merged.Branch = preferString(incoming.Branch, existing.Branch)
	merged.Commit = preferString(incoming.Commit, existing.Commit)
	if len(incoming.Phases) == 0 {
		merged.Phases = existing.Phases
	}
	return merged
}

// generateID builds a unique plan ID from the current timestamp and a slug of
// the title, appending a numeric suffix on collision.
func (s *Store) generateID(title string) (string, error) {
	return artifacts.AllocateTimestampedID(artifacts.IDOptions{
		Root:         s.root,
		Collection:   "plans",
		Title:        title,
		FallbackSlug: "plan",
		Kind:         "plan",
		Now:          s.now(),
	})
}

func preferString(incoming, existing string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

func (s *Store) write(record PlanRecord) error {
	if err := validatePlanID(record.PlanID); err != nil {
		return err
	}
	dir, err := artifacts.EnsureRecordDir(s.root, "plans", record.PlanID)
	if err != nil {
		return fmt.Errorf("secure plan directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan metadata: %w", err)
	}
	if err := artifacts.WriteFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
		return fmt.Errorf("write plan metadata: %w", err)
	}
	// Only (re)write the body when we actually have one. Save always supplies
	// non-empty Markdown; metadata-only updates such as SetPhase carry whatever
	// readRecord loaded, so guarding here avoids clobbering an existing plan.md
	// if its body could not be read back.
	if record.Markdown != "" {
		if err := artifacts.WriteFileAtomic(filepath.Join(dir, "plan.md"), []byte(record.Markdown)); err != nil {
			return fmt.Errorf("write plan markdown: %w", err)
		}
	}
	return nil
}

// List returns plans matching the filter, sorted by UpdatedAt descending.
func (s *Store) List(filter PlanFilter) ([]PlanRecord, error) {
	root := filepath.Join(s.root, "plans")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list plans: %w", err)
	}
	var records []PlanRecord
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

func (s *Store) readRecord(planID string) (PlanRecord, bool) {
	record, err := s.readMetadataStrict(planID)
	if err != nil {
		return PlanRecord{}, false
	}
	dir := s.planDir(planID)
	markdown, err := os.ReadFile(filepath.Join(dir, "plan.md"))
	if err == nil {
		record.Markdown = string(markdown)
	}
	return record, true
}

func (s *Store) readMetadataStrict(planID string) (PlanRecord, error) {
	if err := validatePlanID(planID); err != nil {
		return PlanRecord{}, err
	}
	dir := s.planDir(planID)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return PlanRecord{}, fmt.Errorf("read plan metadata: %w", err)
	}
	var record PlanRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return PlanRecord{}, fmt.Errorf("decode plan metadata: %w", err)
	}
	if record.PlanID != planID {
		return PlanRecord{}, fmt.Errorf("plan metadata id %q does not match %q", record.PlanID, planID)
	}
	return record, nil
}

func (s *Store) planDir(planID string) string {
	return artifacts.RecordDir(s.root, "plans", planID)
}

func validatePlanID(planID string) error {
	if !artifacts.IsSafeID(planID) {
		return fmt.Errorf("invalid plan id %q", planID)
	}
	return nil
}

func matchesFilter(record PlanRecord, filter PlanFilter) bool {
	if filter.RepoPath != "" && filepath.Clean(record.RepoPath) != filepath.Clean(filter.RepoPath) {
		return false
	}
	return true
}
