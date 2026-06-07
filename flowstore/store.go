// Package flowstore persists task-centric Flow records beside the agent-session store.
package flowstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const schemaVersion = 1

const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

const maxSlugLength = 48
const maxIDCollisionAttempts = 1000

var flowIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

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

// Store reads and writes flow records under an artifact root.
type Store struct {
	root string
	now  func() time.Time
}

// StoreOptions configures a Store.
type StoreOptions struct {
	Root string
	Now  func() time.Time
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
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("flow store root must be absolute: %s", root)
	}
	if err := os.MkdirAll(filepath.Join(root, "flows"), dirPerm); err != nil {
		return nil, fmt.Errorf("create flow store: %w", err)
	}
	if err := os.Chmod(root, dirPerm); err != nil {
		return nil, fmt.Errorf("secure flow root: %w", err)
	}
	if err := os.Chmod(filepath.Join(root, "flows"), dirPerm); err != nil {
		return nil, fmt.Errorf("secure flows directory: %w", err)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Store{root: root, now: now}, nil
}

// DefaultRoot returns the default artifact root, matching sessions and plans.
func DefaultRoot() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "wtui", "sessions", "v1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve flow state root: %w", err)
	}
	return filepath.Join(home, ".local", "state", "wtui", "sessions", "v1"), nil
}

// Root returns the artifact root in use.
func (s *Store) Root() string {
	return s.root
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

// DeriveStatus computes the flow-level status from phase and merge state.
func DeriveStatus(record FlowRecord) string {
	if record.Status == StatusAbandoned {
		return StatusAbandoned
	}
	if record.Merge.Status == MergeMerged {
		return StatusMerged
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
	base := s.now().UTC().Format("20060102T150405Z") + "-" + slug(title)
	candidate := base
	for i := 2; i < maxIDCollisionAttempts; i++ {
		_, err := os.Stat(s.flowDir(candidate))
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check flow id collision: %w", err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("could not allocate a unique flow id for %q after %d attempts", title, maxIDCollisionAttempts)
}

func slug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxSlugLength {
		out = strings.Trim(out[:maxSlugLength], "-")
	}
	if out == "" {
		return "flow"
	}
	return out
}

func (s *Store) write(record FlowRecord) error {
	if err := validateFlowID(record.FlowID); err != nil {
		return err
	}
	dir := s.flowDir(record.FlowID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create flow directory: %w", err)
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("secure flow directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode flow metadata: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
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
	return filepath.Join(s.root, "flows", flowID)
}

func validateFlowID(flowID string) error {
	if !flowIDPattern.MatchString(flowID) || flowID == "." || flowID == ".." {
		return fmt.Errorf("invalid flow id %q", flowID)
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
	return record
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := io.Copy(temp, bytes.NewReader(data)); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(filePerm); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
