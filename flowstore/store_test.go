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
	dirInfo, err := os.Stat(filepath.Dir(meta))
	if err != nil {
		t.Fatalf("stat flow dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("flow dir mode = %o, want 0700", dirInfo.Mode().Perm())
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
