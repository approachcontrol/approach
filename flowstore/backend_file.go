package flowstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

// fileBackend stores one Flow record per directory under <root>/flows, with a
// flock-guarded critical section per flow under <root>/flows/.locks.
type fileBackend struct {
	root        string
	lockTimeout time.Duration
}

func newFileBackend(root string, lockTimeout time.Duration) (*fileBackend, error) {
	if err := artifacts.EnsureCollection(root, "flows"); err != nil {
		return nil, fmt.Errorf("create flow store: %w", err)
	}
	return &fileBackend{root: root, lockTimeout: lockTimeout}, nil
}

func (b *fileBackend) get(flowID string) (storedFlow, bool) {
	if err := validateFlowID(flowID); err != nil {
		return storedFlow{}, false
	}
	data, err := os.ReadFile(filepath.Join(b.flowDir(flowID), "meta.json"))
	if err != nil {
		return storedFlow{}, false
	}
	presence := rawDependsOnPresence(data)
	headlessPresent := rawFieldPresent(data, "headless")
	var record FlowRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return storedFlow{}, false
	}
	if record.FlowID != flowID || record.SchemaVersion != schemaVersion {
		return storedFlow{}, false
	}
	// legacyEncoding: meta.json is the encoding where a phase object could omit
	// depends_on entirely and the record could omit headless, so this backend is
	// the one that owes real hints.
	return storedFlow{
		record:            record,
		legacyEncoding:    true,
		dependsOnPresence: presence,
		headlessPresent:   headlessPresent,
	}, true
}

// list preserves os.ReadDir order. Store.List sorts stably on UpdatedAt, so
// records with equal timestamps keep storage order; reordering here would
// reorder ties.
func (b *fileBackend) list() ([]storedFlow, error) {
	root := filepath.Join(b.root, "flows")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list flows: %w", err)
	}
	var stored []storedFlow
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// .locks and any other non-record directory drop out here: get rejects
		// unsafe ids outright, and anything else has no readable meta.json.
		flow, ok := b.get(entry.Name())
		if !ok {
			continue
		}
		stored = append(stored, flow)
	}
	return stored, nil
}

// save writes the record without taking the flow lock. It must not: fileSession
// routes every in-section save through here while acquireFlowLock's flock is
// already held, and that flock is not re-entrant, so taking it again would
// block until it timed out.
//
// It also leaves depends_on alone. Normalization is domain logic and stays
// above the seam, in Store.saveSession.
func (b *fileBackend) save(record FlowRecord) error {
	dir, err := artifacts.EnsureRecordDir(b.root, "flows", record.FlowID)
	if err != nil {
		return fmt.Errorf("secure flow directory: %w", err)
	}
	data, err := marshalFlowRecord(record)
	if err != nil {
		return fmt.Errorf("encode flow metadata: %w", err)
	}
	if err := artifacts.WriteFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
		return fmt.Errorf("write flow metadata: %w", err)
	}
	return nil
}

// delete removes the record directory but deliberately leaves the flow's lock
// file behind, so a concurrent holder of the critical section keeps its lock.
func (b *fileBackend) delete(flowID string) error {
	if err := validateFlowID(flowID); err != nil {
		return err
	}
	release, err := b.acquireFlowLock(flowID)
	if err != nil {
		return err
	}
	defer release()
	dir := b.flowDir(flowID)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return flowNotFoundError(flowID)
	} else if err != nil {
		return fmt.Errorf("stat flow %q: %w", flowID, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete flow %q: %w", flowID, err)
	}
	return nil
}

func (b *fileBackend) update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, err
	}
	release, err := b.acquireFlowLock(flowID)
	if err != nil {
		return FlowRecord{}, err
	}
	defer release()
	// Invoked exactly once, and its record and error both returned verbatim:
	// the flock has no rollback, so every save is already durable, and callers
	// match on the error's identity and text.
	return mutate(fileSession{backend: b, flowID: flowID})
}

func (b *fileBackend) allocateID(title string, now time.Time) (string, error) {
	return artifacts.AllocateTimestampedID(artifacts.IDOptions{
		Root:         b.root,
		Collection:   "flows",
		Title:        title,
		FallbackSlug: "flow",
		Kind:         "flow",
		Now:          now,
	})
}

// acquireFlowLock is NOT re-entrant: artifacts.AcquireFileLock opens a fresh fd
// per call and POSIX flock is per open-file-description, so a second acquire
// from the same process blocks until it times out. Nothing reachable from
// inside update may take it again.
func (b *fileBackend) acquireFlowLock(flowID string) (func(), error) {
	if err := validateFlowID(flowID); err != nil {
		return nil, err
	}
	lockDir := filepath.Join(b.root, "flows", ".locks")
	if err := os.MkdirAll(lockDir, artifacts.DirPerm); err != nil {
		return nil, fmt.Errorf("create flow lock directory: %w", err)
	}
	if err := os.Chmod(lockDir, artifacts.DirPerm); err != nil {
		return nil, fmt.Errorf("secure flow lock directory: %w", err)
	}
	lockPath := filepath.Join(lockDir, flowID+".lock")
	return artifacts.AcquireFileLock(lockPath, fmt.Sprintf("flow lock %q", flowID), b.lockTimeout)
}

func (b *fileBackend) flowDir(flowID string) string {
	return artifacts.RecordDir(b.root, "flows", flowID)
}

// fileSession is the in-lock handle. It holds no state beyond the target flow,
// so every operation reads or writes through the backend directly.
type fileSession struct {
	backend *fileBackend
	flowID  string
}

func (s fileSession) get() (storedFlow, bool) {
	return s.backend.get(s.flowID)
}

func (s fileSession) exists() (bool, error) {
	if _, err := os.Stat(s.backend.flowDir(s.flowID)); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	return false, nil
}

func (s fileSession) save(record FlowRecord) error {
	return s.backend.save(record)
}

func marshalFlowRecord(record FlowRecord) ([]byte, error) {
	if len(record.preserveMissingDependsOn) == 0 {
		return json.MarshalIndent(record, "", "  ")
	}
	phases := make([]flowPhaseForWrite, 0, len(record.Phases))
	for _, phase := range record.Phases {
		phases = append(phases, flowPhaseForWriteFrom(phase, record.preserveMissingDependsOn[artifacts.NormalizePhaseID(phase.PhaseID)]))
	}
	return json.MarshalIndent(flowRecordForWrite{
		SchemaVersion: record.SchemaVersion,
		FlowID:        record.FlowID,
		Title:         record.Title,
		Instructions:  record.Instructions,
		Status:        record.Status,
		RepoPath:      record.RepoPath,
		WorktreePath:  record.WorktreePath,
		Branch:        record.Branch,
		BaseRef:       record.BaseRef,
		Commit:        record.Commit,
		PresetName:    record.PresetName,
		PlanID:        record.PlanID,
		PlanPath:      record.PlanPath,
		Issue:         record.Issue,
		PR:            record.PR,
		Merge:         record.Merge,
		AutoMode:      record.AutoMode,
		Headless:      record.Headless,
		Phases:        phases,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}, "", "  ")
}

type flowRecordForWrite struct {
	SchemaVersion int                 `json:"schema_version"`
	FlowID        string              `json:"flow_id"`
	Title         string              `json:"title"`
	Instructions  string              `json:"instructions"`
	Status        string              `json:"status"`
	RepoPath      string              `json:"repo_path"`
	WorktreePath  string              `json:"worktree_path,omitempty"`
	Branch        string              `json:"branch,omitempty"`
	BaseRef       string              `json:"base_ref,omitempty"`
	Commit        string              `json:"commit,omitempty"`
	PresetName    string              `json:"preset_name,omitempty"`
	PlanID        string              `json:"plan_id,omitempty"`
	PlanPath      string              `json:"plan_path,omitempty"`
	Issue         Issue               `json:"issue,omitempty"`
	PR            PullRequest         `json:"pr,omitempty"`
	Merge         Merge               `json:"merge,omitempty"`
	AutoMode      bool                `json:"auto_mode,omitempty"`
	Headless      bool                `json:"headless"`
	Phases        []flowPhaseForWrite `json:"phases"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}

type flowPhaseForWrite struct {
	PhaseID       string    `json:"phase_id"`
	ParentPhaseID string    `json:"parent_phase_id,omitempty"`
	Title         string    `json:"title"`
	Kind          string    `json:"kind"`
	DependsOn     *[]string `json:"depends_on,omitempty"`
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

func flowPhaseForWriteFrom(phase FlowPhase, omitDependsOn bool) flowPhaseForWrite {
	var dependsOn *[]string
	if !omitDependsOn {
		dependsOn = &phase.DependsOn
	}
	return flowPhaseForWrite{
		PhaseID:       phase.PhaseID,
		ParentPhaseID: phase.ParentPhaseID,
		Title:         phase.Title,
		Kind:          phase.Kind,
		DependsOn:     dependsOn,
		Status:        phase.Status,
		Order:         phase.Order,
		Outcome:       phase.Outcome,
		Notes:         phase.Notes,
		Summary:       phase.Summary,
		LaunchIDs:     phase.LaunchIDs,
		Sessions:      phase.Sessions,
		CreatedAt:     phase.CreatedAt,
		UpdatedAt:     phase.UpdatedAt,
	}
}

// rawDependsOnPresence records, positionally, whether each persisted phase
// object carried a depends_on key at all — the distinction between a legacy
// record with no edges and an edge-aware record with empty edges.
func rawDependsOnPresence(data []byte) []rawDependsOnState {
	var raw struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	presence := make([]rawDependsOnState, len(raw.Phases))
	for i, phase := range raw.Phases {
		_, ok := phase["depends_on"]
		presence[i] = rawDependsOnState{
			Present: ok,
		}
	}
	return presence
}

// rawFieldPresent reports whether the persisted record carried a top-level key
// at all, so the read path can tell a legacy record with no field from one that
// stored the zero value.
func rawFieldPresent(data []byte, field string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw[field]
	return ok
}
