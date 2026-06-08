package flowstore_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brian-bell/wtui/flowstore"
)

func TestStoreCreatePersistsDefaultFlowRecord(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Add Flow Mode",
		Instructions: "Build the first tracer bullet.",
		RepoPath:     repoPath,
		WorktreePath: filepath.Join(root, "repo-worktrees", "flow-add-flow-mode"),
		Branch:       "flow/add-flow-mode",
		BaseRef:      "main",
		Commit:       "abc123",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if record.FlowID != "20260607T120000Z-add-flow-mode" {
		t.Fatalf("FlowID = %q, want timestamp slug", record.FlowID)
	}
	if record.Status != flowstore.StatusPending {
		t.Fatalf("Status = %q, want pending", record.Status)
	}
	if record.Merge.Status != flowstore.MergePending {
		t.Fatalf("Merge.Status = %q, want pending", record.Merge.Status)
	}
	if record.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", record.SchemaVersion)
	}
	if record.CreatedAt != now || record.UpdatedAt != now {
		t.Fatalf("timestamps = %s/%s, want %s", record.CreatedAt, record.UpdatedAt, now)
	}
	if len(record.Phases) != 7 {
		t.Fatalf("phase count = %d, want default pipeline: %#v", len(record.Phases), record.Phases)
	}
	if record.Phases[0].PhaseID != "plan" || record.Phases[0].Status != flowstore.PhaseReady {
		t.Fatalf("first phase = %#v, want ready plan", record.Phases[0])
	}
	for _, phase := range record.Phases[1:] {
		if phase.Status != flowstore.PhasePending {
			t.Fatalf("phase %q status = %q, want pending", phase.PhaseID, phase.Status)
		}
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Title != "Add Flow Mode" ||
		read.Instructions != "Build the first tracer bullet." ||
		read.Merge.Status != flowstore.MergePending ||
		read.RepoPath != repoPath ||
		read.WorktreePath != filepath.Join(root, "repo-worktrees", "flow-add-flow-mode") ||
		read.Branch != "flow/add-flow-mode" ||
		read.BaseRef != "main" ||
		read.Commit != "abc123" {
		t.Fatalf("record did not round-trip: %#v", read)
	}

	meta := filepath.Join(root, "flows", record.FlowID, "meta.json")
	metaJSON, err := os.ReadFile(meta)
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	if strings.Contains(string(metaJSON), "0001-01-01") || strings.Contains(string(metaJSON), "merged_at") {
		t.Fatalf("pending flow metadata should not serialize a zero merge timestamp:\n%s", metaJSON)
	}
	info, err := os.Stat(meta)
	if err != nil {
		t.Fatalf("stat meta.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("meta.json mode = %o, want 0600", info.Mode().Perm())
	}
	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "flows"), 0o700)
	dirInfo, err := os.Stat(filepath.Dir(meta))
	if err != nil {
		t.Fatalf("stat flow dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("flow dir mode = %o, want 0700", dirInfo.Mode().Perm())
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestStoreCreateAllocatesCollisionSuffix(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	first, err := store.Create(flowstore.FlowRecord{Title: "Add Flow Mode", Instructions: "one", RepoPath: repoPath})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := store.Create(flowstore.FlowRecord{Title: "Add Flow Mode", Instructions: "two", RepoPath: repoPath})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	if first.FlowID != "20260607T120000Z-add-flow-mode" || second.FlowID != "20260607T120000Z-add-flow-mode-2" {
		t.Fatalf("ids = %q, %q; want collision suffix", first.FlowID, second.FlowID)
	}
}

func TestStoreCreateRejectsDuplicateSuppliedFlowID(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	first, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-flow",
		Title:        "First",
		Instructions: "keep this",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}

	_, err = store.Create(flowstore.FlowRecord{
		FlowID:       first.FlowID,
		Title:        "Second",
		Instructions: "do not overwrite",
		RepoPath:     repoPath,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Create(duplicate) error = %v, want already exists", err)
	}

	read, err := store.Read(first.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Title != "First" || read.Instructions != "keep this" {
		t.Fatalf("duplicate Create() overwrote record: %#v", read)
	}
}

func TestStoreListFiltersSortsAndSkipsBadRecords(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	bravo := filepath.Join(root, "bravo")
	times := []time.Time{
		time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 2, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 3, 10, 0, 1, 0, time.UTC),
	}
	i := 0
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now: func() time.Time {
			tm := times[i]
			i++
			return tm
		},
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	older, err := store.Create(flowstore.FlowRecord{Title: "Older", Instructions: "old", RepoPath: alpha})
	if err != nil {
		t.Fatalf("Create(older) error = %v", err)
	}
	newer, err := store.Create(flowstore.FlowRecord{Title: "Newer", Instructions: "new", RepoPath: alpha})
	if err != nil {
		t.Fatalf("Create(newer) error = %v", err)
	}
	if _, err := store.Create(flowstore.FlowRecord{Title: "Other", Instructions: "other", RepoPath: bravo}); err != nil {
		t.Fatalf("Create(other) error = %v", err)
	}
	badDir := filepath.Join(root, "flows", "bad")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	futureDir := filepath.Join(root, "flows", "future")
	if err := os.MkdirAll(futureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	futureMeta := `{"schema_version":99,"flow_id":"future","title":"Future","repo_path":"` + alpha + `"}`
	if err := os.WriteFile(filepath.Join(futureDir, "meta.json"), []byte(futureMeta), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flows", "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	records, err := store.List(flowstore.FlowFilter{RepoPath: alpha})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("List() returned %d records, want 2: %#v", len(records), records)
	}
	if records[0].FlowID != newer.FlowID || records[1].FlowID != older.FlowID {
		t.Fatalf("List() order = %#v, want updated_at desc", records)
	}
}

func TestStoreSetPhasePersistsUpdateAndDerivesStatus(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	times := []time.Time{
		time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 3, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 4, 0, time.UTC),
	}
	i := 0
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now: func() time.Time {
			tm := times[i]
			i++
			return tm
		},
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Phase updates",
		Instructions: "exercise phase set",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	running, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase(running) error = %v", err)
	}
	if running.Status != flowstore.StatusInProgress {
		t.Fatalf("running flow status = %q, want in_progress", running.Status)
	}
	if running.Phases[0].Status != flowstore.PhaseRunning || running.Phases[0].UpdatedAt != times[2] {
		t.Fatalf("running phase = %#v, want running at %s", running.Phases[0], times[2])
	}

	completed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseCompleted,
		Summary: "Plan saved and reviewed.",
	})
	if err != nil {
		t.Fatalf("SetPhase(completed) error = %v", err)
	}
	if completed.Status != flowstore.StatusInProgress {
		t.Fatalf("completed first phase flow status = %q, want in_progress", completed.Status)
	}
	if completed.UpdatedAt != times[3] {
		t.Fatalf("flow UpdatedAt = %s, want %s", completed.UpdatedAt, times[3])
	}
	if completed.Phases[0].Status != flowstore.PhaseCompleted || completed.Phases[0].Summary != "Plan saved and reviewed." {
		t.Fatalf("completed phase = %#v", completed.Phases[0])
	}
	if completed.Phases[1].Status != flowstore.PhaseReady {
		t.Fatalf("next phase status = %q, want ready", completed.Phases[1].Status)
	}

	repeated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseCompleted,
	})
	if err != nil {
		t.Fatalf("SetPhase(repeated completed) error = %v", err)
	}
	if repeated.Phases[0].Summary != "Plan saved and reviewed." {
		t.Fatalf("repeated update should preserve summary, got %#v", repeated.Phases[0])
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Status != completed.Status || read.Phases[0].Status != flowstore.PhaseCompleted || read.Phases[1].Status != flowstore.PhaseReady {
		t.Fatalf("persisted record = %#v, want completed plan and ready next phase", read)
	}
}

func TestStoreSetStartMetadataAddsWorktreeBranchPlanAndCommit(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "New Flow Launch",
		Instructions: "Plan the work",
		RepoPath:     repoPath,
		BaseRef:      "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:       record.FlowID,
		WorktreePath: filepath.Join(root, "repo-worktrees", "flow-new-flow-launch"),
		Branch:       "flow/new-flow-launch",
		BaseRef:      "origin/main",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     filepath.Join(root, "plans", "plan-1", "plan.md"),
	})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}

	if updated.WorktreePath != filepath.Join(root, "repo-worktrees", "flow-new-flow-launch") {
		t.Fatalf("WorktreePath = %q", updated.WorktreePath)
	}
	if updated.Branch != "flow/new-flow-launch" || updated.BaseRef != "origin/main" || updated.Commit != "abc123" {
		t.Fatalf("metadata not persisted: %#v", updated)
	}
	if updated.PlanID != "plan-1" || updated.PlanPath != filepath.Join(root, "plans", "plan-1", "plan.md") {
		t.Fatalf("plan metadata not persisted: %#v", updated)
	}
}

func TestStoreAddPhaseLaunchIDMarksPhaseRunning(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "New Flow Launch",
		Instructions: "Plan the work",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "plan",
		LaunchID: "launch-1",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}

	phase := phaseByID(t, updated, "plan")
	if phase.Status != flowstore.PhaseRunning {
		t.Fatalf("plan phase status = %q, want running", phase.Status)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-1" {
		t.Fatalf("launch ids = %#v", phase.LaunchIDs)
	}
	if updated.Status != flowstore.StatusInProgress {
		t.Fatalf("flow status = %q, want in_progress", updated.Status)
	}
}

func TestStoreSetPhaseRejectsInvalidTransitions(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Validate transitions",
		Instructions: "reject invalid updates",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for _, tc := range []struct {
		name   string
		update flowstore.PhaseUpdate
		want   string
	}{
		{
			name:   "invalid status",
			update: flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: "done"},
			want:   "invalid phase status",
		},
		{
			name:   "force ready",
			update: flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseReady},
			want:   "cannot set phase status to ready",
		},
		{
			name:   "pending to completed",
			update: flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseCompleted},
			want:   "invalid phase transition",
		},
		{
			name:   "skipped without notes",
			update: flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseSkipped},
			want:   "skipped phase requires notes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err = store.SetPhase(tc.update)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetPhase() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStoreSetPhaseExplainsNeedsAttentionRecovery(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Autoreview recovery",
		Instructions: "explain how to recover attention phases",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/autoreview-recovery",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
	record, err = store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     115,
		URL:        "https://github.com/brian-bell/wtui/pull/115",
		HeadBranch: "flow/autoreview-recovery",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Status:  flowstore.PhaseNeedsAttention,
		Outcome: "needs_attention",
		Notes:   "Follow-up findings remain.",
	})
	if err != nil {
		t.Fatalf("SetPhase(autoreview needs_attention) error = %v", err)
	}

	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Status:  flowstore.PhaseCompleted,
		Outcome: "passed",
	})
	if err == nil {
		t.Fatal("SetPhase(needs_attention -> completed) error = nil")
	}
	for _, want := range []string{
		"invalid phase transition needs_attention -> completed",
		"restart with --status running --notes before completing",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("SetPhase() error = %q, want %q", err, want)
		}
	}

	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Status:  flowstore.PhaseRunning,
		Notes:   "Rerunning autoreview after addressing prior findings.",
	})
	if err != nil {
		t.Fatalf("SetPhase(needs_attention -> running) error = %v", err)
	}
	if got := phaseByID(t, record, "autoreview").Status; got != flowstore.PhaseRunning {
		t.Fatalf("autoreview status = %q, want running", got)
	}
	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Status:  flowstore.PhaseCompleted,
		Outcome: "passed",
		Summary: "Autoreview passed after rerun.",
	})
	if err != nil {
		t.Fatalf("SetPhase(running -> completed) error = %v", err)
	}
}

func TestStoreAddPhaseLaunchIDRestartsNeedsAttentionPhase(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Autoreview relaunch",
		Instructions: "restart autoreview from needs_attention",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/autoreview-relaunch",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
	record, err = store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     115,
		URL:        "https://github.com/brian-bell/wtui/pull/115",
		HeadBranch: "flow/autoreview-relaunch",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Status:  flowstore.PhaseNeedsAttention,
		Outcome: "needs_attention",
		Notes:   "Follow-up findings remain.",
	})
	if err != nil {
		t.Fatalf("SetPhase(autoreview needs_attention) error = %v", err)
	}

	relaunched, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "autoreview",
		LaunchID: "launch-autoreview-2",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(autoreview) error = %v", err)
	}

	phase := phaseByID(t, relaunched, "autoreview")
	if phase.Status != flowstore.PhaseRunning || phase.Outcome != "" {
		t.Fatalf("autoreview after relaunch = %#v, want running with cleared outcome", phase)
	}
	if !strings.Contains(phase.Notes, "Relaunched after needs_attention") {
		t.Fatalf("autoreview notes = %q, want restart note", phase.Notes)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-autoreview-2" {
		t.Fatalf("launch ids = %#v", phase.LaunchIDs)
	}
}

func TestStoreSetPhaseAllowsSkippedWithNotesAndIdempotentUpdates(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Skip and repeat",
		Instructions: "exercise idempotency",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	skipped, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseSkipped,
		Notes:   "Existing plan already approved.",
	})
	if err != nil {
		t.Fatalf("SetPhase(skipped) error = %v", err)
	}
	if skipped.Phases[0].Status != flowstore.PhaseSkipped || skipped.Phases[0].Notes != "Existing plan already approved." {
		t.Fatalf("skipped phase = %#v", skipped.Phases[0])
	}
	if skipped.Phases[1].Status != flowstore.PhaseReady {
		t.Fatalf("next phase status = %q, want ready", skipped.Phases[1].Status)
	}

	repeated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseSkipped,
		Notes:   "Existing plan already approved.",
		Summary: "No new plan needed.",
	})
	if err != nil {
		t.Fatalf("SetPhase(repeated skipped) error = %v", err)
	}
	if len(repeated.Phases) != len(record.Phases) {
		t.Fatalf("phase count = %d, want %d", len(repeated.Phases), len(record.Phases))
	}
	if repeated.Phases[0].Summary != "No new plan needed." || repeated.Phases[1].Status != flowstore.PhaseReady {
		t.Fatalf("repeated update record = %#v", repeated)
	}

	pendingSkipped, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseSkipped,
		Notes:   "Implementation is covered by the existing branch.",
	})
	if err != nil {
		t.Fatalf("SetPhase(pending skipped) error = %v", err)
	}
	if pendingSkipped.Phases[2].Status != flowstore.PhasePending || pendingSkipped.Phases[2].Notes != "Implementation is covered by the existing branch." {
		t.Fatalf("gated downstream skip = %#v, want pending implementation with preserved notes", pendingSkipped.Phases[2])
	}
}

func TestStoreSetPhasePlanReviewOutcomeGatesImplementation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		outcome          string
		status           string
		notes            string
		wantFlowStatus   string
		wantReviewStatus string
		wantImplStatus   string
	}{
		{
			name:             "approved",
			outcome:          "approved",
			status:           flowstore.PhaseCompleted,
			wantFlowStatus:   flowstore.StatusInProgress,
			wantReviewStatus: flowstore.PhaseCompleted,
			wantImplStatus:   flowstore.PhaseReady,
		},
		{
			name:             "approved with concerns",
			outcome:          "approved_with_concerns",
			status:           flowstore.PhaseCompleted,
			notes:            "Implementation can proceed if it handles the rollout risk.",
			wantFlowStatus:   flowstore.StatusInProgress,
			wantReviewStatus: flowstore.PhaseCompleted,
			wantImplStatus:   flowstore.PhaseReady,
		},
		{
			name:             "changes requested",
			outcome:          "changes_requested",
			status:           flowstore.PhaseNeedsAttention,
			notes:            "Revise the API boundary before implementation.",
			wantFlowStatus:   flowstore.StatusNeedsAttention,
			wantReviewStatus: flowstore.PhaseNeedsAttention,
			wantImplStatus:   flowstore.PhasePending,
		},
		{
			name:             "blocked",
			outcome:          "blocked",
			status:           flowstore.PhaseBlocked,
			notes:            "Waiting on product decision.",
			wantFlowStatus:   flowstore.StatusBlocked,
			wantReviewStatus: flowstore.PhaseBlocked,
			wantImplStatus:   flowstore.PhasePending,
		},
		{
			name:             "skipped override",
			status:           flowstore.PhaseSkipped,
			notes:            "Human already reviewed and approved the linked plan.",
			wantFlowStatus:   flowstore.StatusInProgress,
			wantReviewStatus: flowstore.PhaseSkipped,
			wantImplStatus:   flowstore.PhaseReady,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "Review Gate",
				Instructions: "gate implementation",
				RepoPath:     filepath.Join(root, "repo"),
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "plan",
				Status:  flowstore.PhaseCompleted,
				Outcome: "plan_saved",
			})
			if err != nil {
				t.Fatalf("SetPhase(plan completed) error = %v", err)
			}

			updated, err := store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "plan-review",
				Status:  tc.status,
				Outcome: tc.outcome,
				Notes:   tc.notes,
			})
			if err != nil {
				t.Fatalf("SetPhase(plan-review) error = %v", err)
			}

			if updated.Status != tc.wantFlowStatus {
				t.Fatalf("flow status = %q, want %q", updated.Status, tc.wantFlowStatus)
			}
			if got := phaseByID(t, updated, "plan-review").Status; got != tc.wantReviewStatus {
				t.Fatalf("plan-review status = %q, want %q", got, tc.wantReviewStatus)
			}
			if got := phaseByID(t, updated, "implementation").Status; got != tc.wantImplStatus {
				t.Fatalf("implementation status = %q, want %q", got, tc.wantImplStatus)
			}
		})
	}
}

func TestStoreSetPhaseTrimsPlanReviewOutcome(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Trim Review Outcome",
		Instructions: "accept human input with whitespace",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(plan completed) error = %v", err)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan-review",
		Status:  flowstore.PhaseCompleted,
		Outcome: " approved ",
	})
	if err != nil {
		t.Fatalf("SetPhase(plan-review completed) error = %v", err)
	}

	if got := phaseByID(t, updated, "plan-review").Outcome; got != flowstore.OutcomeApproved {
		t.Fatalf("plan-review outcome = %q, want trimmed approved", got)
	}
	if got := phaseByID(t, updated, "implementation").Status; got != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready", got)
	}
}

func TestStoreReadMigratesLegacyPlanReviewApproval(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Persisted Gate",
		Instructions: "normalize old records",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for i := range record.Phases {
		switch record.Phases[i].PhaseID {
		case "plan", "plan-review":
			record.Phases[i].Status = flowstore.PhaseCompleted
		case "implementation":
			record.Phases[i].Status = flowstore.PhaseReady
		}
		record.Phases[i].UpdatedAt = now
	}
	record.Status = flowstore.StatusInProgress
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	metaPath := filepath.Join(root, "flows", record.FlowID, "meta.json")
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(meta.json) error = %v", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	review := phaseByID(t, read, "plan-review")
	if review.Status != flowstore.PhaseCompleted || review.Outcome != flowstore.OutcomeApproved {
		t.Fatalf("plan-review = %#v, want completed approved legacy migration", review)
	}
	if got := phaseByID(t, read, "implementation").Status; got != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready after legacy approval migration", got)
	}
}

func TestStoreSetPhaseValidatesPlanReviewOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update flowstore.PhaseUpdate
		want   string
	}{
		{
			name: "completed requires approved outcome",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseCompleted,
			},
			want: "plan-review completed requires outcome approved or approved_with_concerns",
		},
		{
			name: "approved with concerns requires notes",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseCompleted,
				Outcome: "approved_with_concerns",
			},
			want: "approved_with_concerns requires notes",
		},
		{
			name: "changes requested requires notes",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseNeedsAttention,
				Outcome: "changes_requested",
			},
			want: "changes_requested requires notes",
		},
		{
			name: "blocked requires blocked outcome",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseBlocked,
				Outcome: "changes_requested",
				Notes:   "Waiting on input.",
			},
			want: "plan-review blocked requires outcome blocked",
		},
		{
			name: "blocked requires notes",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseBlocked,
				Outcome: "blocked",
			},
			want: "plan-review blocked requires notes",
		},
		{
			name: "needs attention requires changes requested outcome",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseNeedsAttention,
				Notes:   "Revise scope.",
			},
			want: "plan-review needs_attention requires outcome changes_requested",
		},
		{
			name: "unknown outcome",
			update: flowstore.PhaseUpdate{
				PhaseID: "plan-review",
				Status:  flowstore.PhaseNeedsAttention,
				Outcome: "maybe",
				Notes:   "Unclear.",
			},
			want: "invalid plan-review outcome",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "Review Outcomes",
				Instructions: "validate outcomes",
				RepoPath:     filepath.Join(root, "repo"),
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "plan",
				Status:  flowstore.PhaseCompleted,
			})
			if err != nil {
				t.Fatalf("SetPhase(plan completed) error = %v", err)
			}
			tc.update.FlowID = record.FlowID
			_, err = store.SetPhase(tc.update)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetPhase() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStoreSetPhasePlanReviewRerunResetsImplementation(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Review Rerun",
		Instructions: "rerun review",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(plan completed) error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseCompleted, Outcome: "approved"})
	if err != nil {
		t.Fatalf("SetPhase(plan-review approved) error = %v", err)
	}
	if got := phaseByID(t, record, "implementation").Status; got != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready", got)
	}

	rerun, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan-review",
		Status:  flowstore.PhaseRunning,
		Notes:   "Plan changed; re-review before implementation.",
	})
	if err != nil {
		t.Fatalf("SetPhase(plan-review running) error = %v", err)
	}
	if got := phaseByID(t, rerun, "implementation").Status; got != flowstore.PhasePending {
		t.Fatalf("implementation status after rerun = %q, want pending", got)
	}
	if got := phaseByID(t, rerun, "plan-review").Outcome; got != "" {
		t.Fatalf("plan-review outcome after rerun = %q, want cleared", got)
	}
}

func TestStoreAddPhaseLaunchIDRerunsPlanReviewAndResetsImplementation(t *testing.T) {
	for _, tc := range []struct {
		status  string
		outcome string
		notes   string
	}{
		{status: flowstore.PhaseRunning},
		{status: flowstore.PhaseNeedsAttention, notes: "Implementation needs review."},
		{status: flowstore.PhaseCompleted, outcome: "implemented"},
		{status: flowstore.PhaseBlocked, notes: "Implementation is blocked."},
		{status: flowstore.PhaseSkipped, notes: "Implementation was covered elsewhere."},
	} {
		t.Run(tc.status, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "Review Relaunch",
				Instructions: "relaunch review",
				RepoPath:     filepath.Join(root, "repo"),
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseCompleted})
			if err != nil {
				t.Fatalf("SetPhase(plan completed) error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseCompleted, Outcome: "approved"})
			if err != nil {
				t.Fatalf("SetPhase(plan-review approved) error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "implementation",
				Status:  tc.status,
				Outcome: tc.outcome,
				Notes:   tc.notes,
			})
			if err != nil {
				t.Fatalf("SetPhase(implementation %s) error = %v", tc.status, err)
			}

			relaunched, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
				FlowID:   record.FlowID,
				PhaseID:  "plan-review",
				LaunchID: "launch-review-2",
			})
			if err != nil {
				t.Fatalf("AddPhaseLaunchID(plan-review) error = %v", err)
			}

			review := phaseByID(t, relaunched, "plan-review")
			if review.Status != flowstore.PhaseRunning || review.Outcome != "" {
				t.Fatalf("plan-review after relaunch = %#v, want running with cleared outcome", review)
			}
			if got := phaseByID(t, relaunched, "implementation").Status; got != flowstore.PhasePending {
				t.Fatalf("implementation status after relaunch = %q, want pending", got)
			}
		})
	}
}

func TestStoreSetPhaseReportsLockTimeout(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:        root,
		LockTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Lock timeout",
		Instructions: "hold the update lock",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	lockPath := filepath.Join(root, "flows", record.FlowID, ".update.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock lock file: %v", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	if _, err := lockFile.WriteString("held\n"); err != nil {
		t.Fatalf("write lock file: %v", err)
	}

	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseRunning,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for flow lock") {
		t.Fatalf("SetPhase() error = %v, want lock timeout", err)
	}
}

func TestStoreSetPhaseIgnoresAbandonedLockMarker(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Stale lock",
		Instructions: "recover phase updates",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	lockPath := filepath.Join(root, "flows", record.FlowID, ".update.lock")
	if err := os.WriteFile(lockPath, []byte("not a live lock\n"), 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatalf("loosen lock file: %v", err)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if updated.Phases[0].Status != flowstore.PhaseRunning {
		t.Fatalf("phase status = %q, want running", updated.Phases[0].Status)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if !strings.Contains(string(lockData), "\n") || strings.Contains(string(lockData), "not a live lock") {
		t.Fatalf("lock marker was not refreshed: %q", lockData)
	}
	assertMode(t, lockPath, 0o600)
}

func TestStoreSetPhaseConcurrentUpdatesDoNotOverwriteEachOther(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "concurrent-flow",
		Title:        "Concurrent updates",
		Instructions: "preserve both mutations",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Kind: "plan", Status: flowstore.PhaseRunning, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Kind: "implementation", Status: flowstore.PhaseRunning, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := store.SetPhase(flowstore.PhaseUpdate{
			FlowID:  record.FlowID,
			PhaseID: "plan",
			Status:  flowstore.PhaseCompleted,
			Summary: "Plan complete.",
		})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := store.SetPhase(flowstore.PhaseUpdate{
			FlowID:  record.FlowID,
			PhaseID: "implementation",
			Status:  flowstore.PhaseBlocked,
			Notes:   "Needs human input.",
		})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SetPhase() error = %v", err)
		}
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Phases[0].Status != flowstore.PhaseCompleted || read.Phases[0].Summary != "Plan complete." {
		t.Fatalf("plan phase after concurrent updates = %#v", read.Phases[0])
	}
	if read.Phases[1].Status != flowstore.PhaseBlocked || read.Phases[1].Notes != "Needs human input." {
		t.Fatalf("implementation phase after concurrent updates = %#v", read.Phases[1])
	}
	if read.Status != flowstore.StatusBlocked {
		t.Fatalf("flow status = %q, want blocked", read.Status)
	}
}

func TestStoreSetPhaseChildPhasesGateDownstreamReadiness(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "child-gate-flow",
		Title:        "Child gate",
		Instructions: "child phases gate downstream",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation-followup", ParentPhaseID: "implementation", Title: "Follow-up", Status: flowstore.PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhasePending, Order: 3, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}
	if updated.Phases[1].Status != flowstore.PhaseReady {
		t.Fatalf("child phase status = %q, want ready", updated.Phases[1].Status)
	}
	if updated.Phases[2].Status != flowstore.PhasePending {
		t.Fatalf("downstream phase status = %q, want pending while child is not done", updated.Phases[2].Status)
	}

	updated, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation-followup",
		Status:  flowstore.PhaseCompleted,
	})
	if err != nil {
		t.Fatalf("SetPhase(child completed) error = %v", err)
	}
	if updated.Phases[2].Status != flowstore.PhaseReady {
		t.Fatalf("downstream phase status = %q, want ready after child completion", updated.Phases[2].Status)
	}
}

func TestStoreSetPRPersistsMetadataAndUngatesAutoreview(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "PR metadata",
		Instructions: "record the pull request",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/pr-metadata",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, phaseID := range []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation"} {
		update := flowstore.PhaseUpdate{
			FlowID:  record.FlowID,
			PhaseID: phaseID,
			Status:  flowstore.PhaseCompleted,
		}
		if phaseID == "plan-review" {
			update.Outcome = flowstore.OutcomeApproved
		}
		record, err = store.SetPhase(update)
		if err != nil {
			t.Fatalf("SetPhase(%s completed) error = %v", phaseID, err)
		}
	}
	if got := phaseByID(t, record, "autoreview").Status; got != flowstore.PhasePending {
		t.Fatalf("autoreview status before PR metadata = %q, want pending", got)
	}

	updated, err := store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     115,
		URL:        "https://github.com/brian-bell/wtui/pull/115",
		HeadBranch: "flow/pr-metadata",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}

	if updated.PR.Provider != "github" ||
		updated.PR.Number != 115 ||
		updated.PR.URL != "https://github.com/brian-bell/wtui/pull/115" ||
		updated.PR.HeadBranch != "flow/pr-metadata" ||
		updated.PR.BaseBranch != "main" ||
		updated.PR.Status != "open" {
		t.Fatalf("PR metadata = %#v", updated.PR)
	}
	if got := phaseByID(t, updated, "autoreview").Status; got != flowstore.PhaseReady {
		t.Fatalf("autoreview status after PR metadata = %q, want ready", got)
	}
}

func TestStoreSetPhaseSkippedPRCreationDoesNotUngateAutoreview(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Skipped PR gate",
		Instructions: "pr creation cannot be skipped into autoreview",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/skipped-pr",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop")

	updated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "pr-creation",
		Status:  flowstore.PhaseSkipped,
		Notes:   "No PR was needed.",
	})
	if err != nil {
		t.Fatalf("SetPhase(pr-creation skipped) error = %v", err)
	}

	if got := phaseByID(t, updated, "autoreview").Status; got != flowstore.PhasePending {
		t.Fatalf("autoreview status = %q, want pending without PR metadata", got)
	}
}

func TestHasPRTargetRequiresValidGitHubTarget(t *testing.T) {
	valid := flowstore.PullRequest{
		Provider:   "github",
		Number:     115,
		URL:        "https://github.com/brian-bell/wtui/pull/115",
		HeadBranch: "flow/pr",
		BaseBranch: "main",
	}
	if !flowstore.HasPRTarget(valid) {
		t.Fatalf("HasPRTarget(valid) = false, want true")
	}
	for _, tc := range []struct {
		name string
		pr   flowstore.PullRequest
	}{
		{name: "provider", pr: flowstore.PullRequest{Provider: "gitlab", Number: 115, URL: valid.URL, HeadBranch: valid.HeadBranch, BaseBranch: valid.BaseBranch}},
		{name: "number", pr: flowstore.PullRequest{Provider: "github", Number: 0, URL: valid.URL, HeadBranch: valid.HeadBranch, BaseBranch: valid.BaseBranch}},
		{name: "url", pr: flowstore.PullRequest{Provider: "github", Number: 115, URL: "https://github.com/brian-bell/wtui/issues/115", HeadBranch: valid.HeadBranch, BaseBranch: valid.BaseBranch}},
		{name: "head", pr: flowstore.PullRequest{Provider: "github", Number: 115, URL: valid.URL, BaseBranch: valid.BaseBranch}},
		{name: "base", pr: flowstore.PullRequest{Provider: "github", Number: 115, URL: valid.URL, HeadBranch: valid.HeadBranch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if flowstore.HasPRTarget(tc.pr) {
				t.Fatalf("HasPRTarget(%#v) = true, want false", tc.pr)
			}
		})
	}
}

func TestStoreSetPRValidatesMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update flowstore.PRUpdate
		want   string
	}{
		{
			name:   "provider",
			update: flowstore.PRUpdate{Provider: "gitlab", Number: 1, URL: "https://github.com/brian-bell/wtui/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "unsupported PR provider",
		},
		{
			name:   "number",
			update: flowstore.PRUpdate{Provider: "github", Number: 0, URL: "https://github.com/brian-bell/wtui/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "PR number must be positive",
		},
		{
			name:   "url",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "not-a-url", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "PR URL must be an absolute http(s) URL",
		},
		{
			name:   "url host",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://example.com/brian-bell/wtui/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "GitHub PR URL must use github.com",
		},
		{
			name:   "url number",
			update: flowstore.PRUpdate{Provider: "github", Number: 2, URL: "https://github.com/brian-bell/wtui/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "GitHub PR URL number",
		},
		{
			name:   "url extra path",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/brian-bell/wtui/pull/1/files", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "GitHub PR URL must have /owner/repo/pull/number path",
		},
		{
			name:   "head branch",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/brian-bell/wtui/pull/1", BaseBranch: "main"},
			want:   "PR head branch is required",
		},
		{
			name:   "base branch",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/brian-bell/wtui/pull/1", HeadBranch: "flow/pr"},
			want:   "PR base branch is required",
		},
		{
			name:   "branch consistency",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/brian-bell/wtui/pull/1", HeadBranch: "feature/other", BaseBranch: "main"},
			want:   "PR head branch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "PR validation",
				Instructions: "validate pr metadata",
				RepoPath:     filepath.Join(root, "repo"),
				Branch:       "flow/pr",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			tc.update.FlowID = record.FlowID
			_, err = store.SetPR(tc.update)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetPR() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStoreSetMergePersistsMergedMetadataAndCompletesFlow(t *testing.T) {
	root := t.TempDir()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Merge metadata",
		Instructions: "record the merge",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/merge-metadata",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
	record, err = store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     116,
		URL:        "https://github.com/brian-bell/wtui/pull/116",
		HeadBranch: "flow/merge-metadata",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "autoreview", "merge")

	updated, err := store.SetMerge(flowstore.MergeUpdate{
		FlowID:   record.FlowID,
		Status:   flowstore.MergeMerged,
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err != nil {
		t.Fatalf("SetMerge() error = %v", err)
	}

	if updated.Status != flowstore.StatusMerged {
		t.Fatalf("flow status = %q, want merged", updated.Status)
	}
	if updated.Merge.Status != flowstore.MergeMerged ||
		updated.Merge.Commit != "0123456789abcdef" ||
		updated.Merge.MergedAt == nil ||
		!updated.Merge.MergedAt.Equal(mergedAt) {
		t.Fatalf("merge metadata = %#v", updated.Merge)
	}
	repeated, err := store.SetMerge(flowstore.MergeUpdate{
		FlowID:   record.FlowID,
		Status:   flowstore.MergeMerged,
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err != nil {
		t.Fatalf("SetMerge(repeated) error = %v", err)
	}
	if repeated.UpdatedAt != updated.UpdatedAt {
		t.Fatalf("idempotent SetMerge changed UpdatedAt from %s to %s", updated.UpdatedAt, repeated.UpdatedAt)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Status != flowstore.StatusMerged || read.Merge.Commit != "0123456789abcdef" {
		t.Fatalf("persisted merged record = %#v", read)
	}
}

func TestStoreSetMergeValidatesMergedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withPR     bool
		status     string
		commit     string
		mergedAt   time.Time
		blockPhase bool
		blockNotes string
		want       string
		wantStatus string
		wantMerge  string
	}{
		{
			name:     "missing PR",
			status:   flowstore.MergeMerged,
			commit:   "abc123",
			mergedAt: time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC),
			want:     "requires existing PR metadata",
		},
		{
			name:     "missing commit",
			withPR:   true,
			status:   flowstore.MergeMerged,
			mergedAt: time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC),
			want:     "requires merge commit",
		},
		{
			name:   "missing timestamp",
			withPR: true,
			status: flowstore.MergeMerged,
			commit: "abc123",
			want:   "requires merge timestamp",
		},
		{
			name:     "merge phase not completed",
			withPR:   true,
			status:   flowstore.MergeMerged,
			commit:   "abc123",
			mergedAt: time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC),
			want:     "requires completed merge phase",
		},
		{
			name:       "blocked without phase notes",
			withPR:     true,
			status:     flowstore.MergeBlocked,
			blockPhase: true,
			want:       "requires blocked merge phase notes",
		},
		{
			name:       "blocked with phase notes",
			withPR:     true,
			status:     flowstore.MergeBlocked,
			blockPhase: true,
			blockNotes: "Merge is waiting on failing CI.",
			wantStatus: flowstore.StatusBlocked,
			wantMerge:  flowstore.MergeBlocked,
		},
		{
			name:   "invalid status",
			withPR: true,
			status: "done",
			want:   "invalid merge status",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "Merge validation",
				Instructions: "validate merge metadata",
				RepoPath:     filepath.Join(root, "repo"),
				Branch:       "flow/merge-validation",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
			if tc.withPR {
				record, err = store.SetPR(flowstore.PRUpdate{
					FlowID:     record.FlowID,
					Provider:   "github",
					Number:     116,
					URL:        "https://github.com/brian-bell/wtui/pull/116",
					HeadBranch: "flow/merge-validation",
					BaseBranch: "main",
					Status:     "open",
				})
				if err != nil {
					t.Fatalf("SetPR() error = %v", err)
				}
			}
			if tc.blockPhase {
				record, err = store.SetPhase(flowstore.PhaseUpdate{
					FlowID:  record.FlowID,
					PhaseID: "autoreview",
					Status:  flowstore.PhaseCompleted,
					Outcome: "passed",
				})
				if err != nil {
					t.Fatalf("SetPhase(autoreview completed) error = %v", err)
				}
				record, err = store.SetPhase(flowstore.PhaseUpdate{
					FlowID:  record.FlowID,
					PhaseID: "merge",
					Status:  flowstore.PhaseBlocked,
					Notes:   tc.blockNotes,
				})
				if err != nil && tc.blockNotes != "" {
					t.Fatalf("SetPhase(merge blocked) error = %v", err)
				}
			}

			updated, err := store.SetMerge(flowstore.MergeUpdate{
				FlowID:   record.FlowID,
				Status:   tc.status,
				Commit:   tc.commit,
				MergedAt: tc.mergedAt,
			})
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("SetMerge() error = %v, want %q", err, tc.want)
				}
				read, readErr := store.Read(record.FlowID)
				if readErr != nil {
					t.Fatalf("Read() error = %v", readErr)
				}
				if read.Merge.Status != flowstore.MergePending {
					t.Fatalf("rejected merge update mutated record: %#v", read.Merge)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetMerge() error = %v", err)
			}
			if updated.Status != tc.wantStatus || updated.Merge.Status != tc.wantMerge {
				t.Fatalf("updated record = %#v, want status %q merge %q", updated, tc.wantStatus, tc.wantMerge)
			}
		})
	}
}

func TestStoreSetPhaseReopeningMergeClearsTerminalMergeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name         string
		merge        flowstore.MergeUpdate
		phaseStatus  string
		phaseNotes   string
		reopenStatus string
		reopenNotes  string
		wantStatus   string
	}{
		{
			name: "merged",
			merge: flowstore.MergeUpdate{
				Status:   flowstore.MergeMerged,
				Commit:   "0123456789abcdef",
				MergedAt: time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC),
			},
			phaseStatus:  flowstore.PhaseCompleted,
			reopenStatus: flowstore.PhaseRunning,
			reopenNotes:  "Retrying merge after new information.",
			wantStatus:   flowstore.StatusInProgress,
		},
		{
			name:         "blocked",
			merge:        flowstore.MergeUpdate{Status: flowstore.MergeBlocked},
			phaseStatus:  flowstore.PhaseBlocked,
			phaseNotes:   "CI is still failing.",
			reopenStatus: flowstore.PhaseRunning,
			reopenNotes:  "Retrying merge after new information.",
			wantStatus:   flowstore.StatusInProgress,
		},
		{
			name:         "blocked skipped",
			merge:        flowstore.MergeUpdate{Status: flowstore.MergeBlocked},
			phaseStatus:  flowstore.PhaseBlocked,
			phaseNotes:   "Human decided not to merge this PR.",
			reopenStatus: flowstore.PhaseSkipped,
			reopenNotes:  "Merge intentionally skipped after user decision.",
			wantStatus:   flowstore.StatusCompleted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "Reopen merge",
				Instructions: "retry merge",
				RepoPath:     filepath.Join(root, "repo"),
				Branch:       "flow/reopen-merge",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
			record, err = store.SetPR(flowstore.PRUpdate{
				FlowID:     record.FlowID,
				Provider:   "github",
				Number:     116,
				URL:        "https://github.com/brian-bell/wtui/pull/116",
				HeadBranch: "flow/reopen-merge",
				BaseBranch: "main",
				Status:     "open",
			})
			if err != nil {
				t.Fatalf("SetPR() error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "autoreview",
				Status:  flowstore.PhaseCompleted,
				Outcome: "passed",
			})
			if err != nil {
				t.Fatalf("SetPhase(autoreview completed) error = %v", err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "merge",
				Status:  tc.phaseStatus,
				Outcome: tc.merge.Status,
				Notes:   tc.phaseNotes,
			})
			if err != nil {
				t.Fatalf("SetPhase(merge terminal) error = %v", err)
			}
			tc.merge.FlowID = record.FlowID
			record, err = store.SetMerge(tc.merge)
			if err != nil {
				t.Fatalf("SetMerge() error = %v", err)
			}
			if record.Merge.Status != tc.merge.Status {
				t.Fatalf("merge status = %q, want %q", record.Merge.Status, tc.merge.Status)
			}

			reopened, err := store.SetPhase(flowstore.PhaseUpdate{
				FlowID:  record.FlowID,
				PhaseID: "merge",
				Status:  tc.reopenStatus,
				Notes:   tc.reopenNotes,
			})
			if err != nil {
				t.Fatalf("SetPhase(merge %s) error = %v", tc.reopenStatus, err)
			}
			if reopened.Merge.Status != flowstore.MergePending || reopened.Merge.Commit != "" || reopened.Merge.MergedAt != nil {
				t.Fatalf("reopened merge metadata = %#v, want pending", reopened.Merge)
			}
			if reopened.Status != tc.wantStatus {
				t.Fatalf("reopened flow status = %q, want %q", reopened.Status, tc.wantStatus)
			}
		})
	}
}

func TestStoreAddPhaseLaunchIDReopeningMergeClearsTerminalMergeMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Relaunch merge",
		Instructions: "retry merge from the TUI",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/relaunch-merge",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
	record, err = store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     116,
		URL:        "https://github.com/brian-bell/wtui/pull/116",
		HeadBranch: "flow/relaunch-merge",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "autoreview", "merge")
	record, err = store.SetMerge(flowstore.MergeUpdate{
		FlowID:   record.FlowID,
		Status:   flowstore.MergeMerged,
		Commit:   "0123456789abcdef",
		MergedAt: time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("SetMerge() error = %v", err)
	}

	relaunched, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "merge",
		LaunchID: "launch-merge-retry",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(merge retry) error = %v", err)
	}
	if relaunched.Merge.Status != flowstore.MergePending || relaunched.Merge.Commit != "" || relaunched.Merge.MergedAt != nil {
		t.Fatalf("relaunched merge metadata = %#v, want pending", relaunched.Merge)
	}
	if relaunched.Status != flowstore.StatusInProgress {
		t.Fatalf("relaunched flow status = %q, want in_progress", relaunched.Status)
	}
}

func TestStoreAddChildImplementationPhasePersistsIdempotentlyAndGatesDownstream(t *testing.T) {
	root := t.TempDir()
	times := []time.Time{
		time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 3, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 4, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 5, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 6, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 7, 0, time.UTC),
		time.Date(2026, 6, 7, 12, 0, 8, 0, time.UTC),
	}
	i := 0
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now: func() time.Time {
			tm := times[i]
			i++
			return tm
		},
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Child phases",
		Instructions: "split implementation",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(plan completed) error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseCompleted, Outcome: "approved"})
	if err != nil {
		t.Fatalf("SetPhase(plan-review approved) error = %v", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "implementation", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}
	if got := phaseByID(t, record, "review-loop").Status; got != flowstore.PhaseReady {
		t.Fatalf("review-loop before child = %q, want ready", got)
	}

	added, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "implementation-api",
		Title:         "API integration",
		Order:         10,
	})
	if err != nil {
		t.Fatalf("AddChildPhase() error = %v", err)
	}
	child := phaseByID(t, added, "implementation-api")
	if child.ParentPhaseID != "implementation" ||
		child.Title != "API integration" ||
		child.Kind != "implementation_child" ||
		child.Status != flowstore.PhaseReady ||
		child.Order != 10 ||
		child.CreatedAt != times[5] ||
		child.UpdatedAt != times[5] {
		t.Fatalf("child phase = %#v", child)
	}
	if got := phaseByID(t, added, "review-loop").Status; got != flowstore.PhasePending {
		t.Fatalf("review-loop after child add = %q, want pending", got)
	}
	if got := phaseByID(t, added, "pr-creation").Status; got != flowstore.PhasePending {
		t.Fatalf("pr-creation after child add = %q, want pending", got)
	}

	repeated, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "implementation-api",
		Title:         "API integration",
		Order:         10,
	})
	if err != nil {
		t.Fatalf("AddChildPhase(repeated) error = %v", err)
	}
	if repeated.UpdatedAt != added.UpdatedAt {
		t.Fatalf("idempotent add changed flow UpdatedAt from %s to %s", added.UpdatedAt, repeated.UpdatedAt)
	}
	if got := phaseByID(t, repeated, "implementation-api").UpdatedAt; got != child.UpdatedAt {
		t.Fatalf("idempotent add changed child UpdatedAt from %s to %s", child.UpdatedAt, got)
	}

	completed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation-api",
		Status:  flowstore.PhaseCompleted,
		Summary: "API integration finished.",
	})
	if err != nil {
		t.Fatalf("SetPhase(child completed) error = %v", err)
	}
	if got := phaseByID(t, completed, "review-loop").Status; got != flowstore.PhaseReady {
		t.Fatalf("review-loop after child completion = %q, want ready", got)
	}
	if got := phaseByID(t, completed, "pr-creation").Status; got != flowstore.PhasePending {
		t.Fatalf("pr-creation after child completion = %q, want pending until review-loop is done", got)
	}

	reviewed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "review-loop",
		Status:  flowstore.PhaseCompleted,
		Outcome: "completed",
		Summary: "Review loop passed.",
	})
	if err != nil {
		t.Fatalf("SetPhase(review-loop completed) error = %v", err)
	}
	if got := phaseByID(t, reviewed, "pr-creation").Status; got != flowstore.PhaseReady {
		t.Fatalf("pr-creation after review-loop completion = %q, want ready", got)
	}
}

func TestStoreAddChildImplementationPhaseOrdersAndUpdatesExistingChildren(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Ordered children",
		Instructions: "split implementation",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "implementation-api",
		Title:         "API integration",
		Order:         20,
	})
	if err != nil {
		t.Fatalf("AddChildPhase(api) error = %v", err)
	}
	record, err = store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "implementation-cli",
		Title:         "CLI integration",
		Order:         10,
	})
	if err != nil {
		t.Fatalf("AddChildPhase(cli) error = %v", err)
	}

	assertPhaseOrder(t, record, []string{"implementation", "implementation-cli", "implementation-api", "review-loop"})

	updated, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "implementation-api",
		Title:         "API and store integration",
		Order:         5,
	})
	if err != nil {
		t.Fatalf("AddChildPhase(update api) error = %v", err)
	}
	assertPhaseOrder(t, updated, []string{"implementation", "implementation-api", "implementation-cli", "review-loop"})
	if child := phaseByID(t, updated, "implementation-api"); child.Title != "API and store integration" || child.Order != 5 {
		t.Fatalf("updated child = %#v", child)
	}
	count := 0
	for _, phase := range updated.Phases {
		if phase.PhaseID == "implementation-api" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("updated child duplicated implementation-api %d times: %#v", count, updated.Phases)
	}
}

func TestStoreReadDoesNotGateDownstreamOnSkippedChildWithoutNotes(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "skipped-child-without-notes",
		Title:        "Skipped child",
		Instructions: "normalize imported child phase",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API integration", Status: flowstore.PhaseSkipped, Order: 10, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 4, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseReady, Order: 5, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "review-loop").Status; got != flowstore.PhasePending {
		t.Fatalf("review-loop status = %q, want pending when skipped child has no notes", got)
	}
	if got := phaseByID(t, read, "pr-creation").Status; got != flowstore.PhasePending {
		t.Fatalf("pr-creation status = %q, want pending when skipped child has no notes", got)
	}
}

func TestStoreReadOrdersChildrenBeforeDerivingReadiness(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "out-of-order-child",
		Title:        "Out of order child",
		Instructions: "normalize child order before gates",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 4, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API integration", Status: flowstore.PhaseReady, Order: 10, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseReady, Order: 5, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	assertPhaseOrder(t, read, []string{"implementation", "implementation-api", "review-loop"})
	if got := phaseByID(t, read, "implementation-api").Status; got != flowstore.PhaseReady {
		t.Fatalf("child status = %q, want ready", got)
	}
	if got := phaseByID(t, read, "review-loop").Status; got != flowstore.PhasePending {
		t.Fatalf("review-loop status = %q, want pending behind ready child", got)
	}
	if got := phaseByID(t, read, "pr-creation").Status; got != flowstore.PhasePending {
		t.Fatalf("pr-creation status = %q, want pending behind ready child", got)
	}
}

func TestStoreDerivesFlowStatusFromPhasesAndMerge(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	base := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Status",
		Instructions: "derive it",
		RepoPath:     "/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 2},
		},
	}
	if got := flowstore.DeriveStatus(base); got != flowstore.StatusInProgress {
		t.Fatalf("DeriveStatus(running pipeline) = %q, want in_progress", got)
	}

	blocked := base
	blocked.Phases[1].Status = flowstore.PhaseBlocked
	if got := flowstore.DeriveStatus(blocked); got != flowstore.StatusBlocked {
		t.Fatalf("DeriveStatus(blocked phase) = %q, want blocked", got)
	}

	attention := base
	attention.Phases[1].Status = flowstore.PhaseNeedsAttention
	if got := flowstore.DeriveStatus(attention); got != flowstore.StatusNeedsAttention {
		t.Fatalf("DeriveStatus(needs attention phase) = %q, want needs_attention", got)
	}

	completed := base
	for i := range completed.Phases {
		completed.Phases[i].Status = flowstore.PhaseCompleted
	}
	if got := flowstore.DeriveStatus(completed); got != flowstore.StatusCompleted {
		t.Fatalf("DeriveStatus(completed phases) = %q, want completed", got)
	}

	merged := completed
	merged.Merge.Status = flowstore.MergeMerged
	if got := flowstore.DeriveStatus(merged); got != flowstore.StatusMerged {
		t.Fatalf("DeriveStatus(merged) = %q, want merged", got)
	}

	mergeBlocked := completed
	mergeBlocked.Merge.Status = flowstore.MergeBlocked
	if got := flowstore.DeriveStatus(mergeBlocked); got != flowstore.StatusBlocked {
		t.Fatalf("DeriveStatus(blocked merge) = %q, want blocked", got)
	}

	abandoned := merged
	abandoned.Status = flowstore.StatusAbandoned
	if got := flowstore.DeriveStatus(abandoned); got != flowstore.StatusAbandoned {
		t.Fatalf("DeriveStatus(abandoned) = %q, want abandoned", got)
	}
}

func TestStoreRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		root   string
		record flowstore.FlowRecord
		want   string
	}{
		{name: "relative root", root: "relative", record: flowstore.FlowRecord{}, want: "root must be absolute"},
		{name: "missing title", root: t.TempDir(), record: flowstore.FlowRecord{Instructions: "x", RepoPath: "/repo"}, want: "title is required"},
		{name: "missing instructions", root: t.TempDir(), record: flowstore.FlowRecord{Title: "T", RepoPath: "/repo"}, want: "instructions are required"},
		{name: "missing repo", root: t.TempDir(), record: flowstore.FlowRecord{Title: "T", Instructions: "x"}, want: "repo path is required"},
		{name: "relative repo", root: t.TempDir(), record: flowstore.FlowRecord{Title: "T", Instructions: "x", RepoPath: "repo"}, want: "repo path must be absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: tc.root})
			if strings.Contains(tc.want, "root") {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("NewStore() error = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			_, err = store.Create(tc.record)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func phaseByID(t *testing.T, record flowstore.FlowRecord, phaseID string) flowstore.FlowPhase {
	t.Helper()
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase
		}
	}
	t.Fatalf("phase %q not found in %#v", phaseID, record.Phases)
	return flowstore.FlowPhase{}
}

func mustCompleteFlowPhases(t *testing.T, store *flowstore.Store, record *flowstore.FlowRecord, phaseIDs ...string) {
	t.Helper()
	for _, phaseID := range phaseIDs {
		update := flowstore.PhaseUpdate{
			FlowID:  record.FlowID,
			PhaseID: phaseID,
			Status:  flowstore.PhaseCompleted,
		}
		if phaseID == "plan-review" {
			update.Outcome = flowstore.OutcomeApproved
		}
		updated, err := store.SetPhase(update)
		if err != nil {
			t.Fatalf("SetPhase(%s completed) error = %v", phaseID, err)
		}
		*record = updated
	}
}

func assertPhaseOrder(t *testing.T, record flowstore.FlowRecord, phaseIDs []string) {
	t.Helper()
	cursor := 0
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseIDs[cursor] {
			cursor++
			if cursor == len(phaseIDs) {
				return
			}
		}
	}
	t.Fatalf("phase order missing sequence %#v in %#v", phaseIDs, record.Phases)
}
