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
	"github.com/brian-bell/wtui/planstore"
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
	if !record.AutoMode {
		t.Fatal("AutoMode = false, want new flows to default to auto mode enabled")
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
		read.Commit != "abc123" ||
		!read.AutoMode {
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

func TestStoreCreateDefaultsAutoModeOnEvenWhenCallerPassesFalse(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Default Automation",
		Instructions: "Start the pipeline.",
		RepoPath:     filepath.Join(root, "repo"),
		AutoMode:     false,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if !record.AutoMode {
		t.Fatal("AutoMode = false, want Create to default every new flow to auto mode enabled")
	}
}

func TestStoreSetAutoModeDisablesNewlyCreatedFlow(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Manual Follow Up",
		Instructions: "Let the user opt out after creation.",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !record.AutoMode {
		t.Fatal("Create().AutoMode = false, want new flow to start with auto mode enabled")
	}

	updated, err := store.SetAutoMode(flowstore.AutoModeUpdate{
		FlowID:  record.FlowID,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("SetAutoMode(false) error = %v", err)
	}
	if updated.AutoMode {
		t.Fatalf("SetAutoMode(false).AutoMode = true: %#v", updated)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.AutoMode {
		t.Fatalf("Read().AutoMode = true after disable: %#v", read)
	}

	records, err := store.List(flowstore.FlowFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].AutoMode {
		t.Fatalf("List() = %#v, want one disabled flow", records)
	}
}

func TestStoreReadPreservesLegacyOmittedAutoModeAsDisabled(t *testing.T) {
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

	flowID := "20260607T120000Z-legacy-flow"
	meta := filepath.Join(root, "flows", flowID, "meta.json")
	if err := os.MkdirAll(filepath.Dir(meta), 0o700); err != nil {
		t.Fatalf("create legacy flow dir: %v", err)
	}
	legacy := map[string]any{
		"schema_version": 1,
		"flow_id":        flowID,
		"title":          "Legacy Flow",
		"instructions":   "Existing automation preference should stay off.",
		"status":         flowstore.StatusPending,
		"repo_path":      repoPath,
		"merge": map[string]any{
			"status": flowstore.MergePending,
		},
		"phases": []map[string]any{
			{
				"phase_id":   "plan",
				"title":      "Plan",
				"kind":       "plan",
				"status":     flowstore.PhaseReady,
				"order":      1,
				"created_at": now.Format(time.RFC3339Nano),
				"updated_at": now.Format(time.RFC3339Nano),
			},
		},
		"created_at": now.Format(time.RFC3339Nano),
		"updated_at": now.Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy record: %v", err)
	}
	if err := os.WriteFile(meta, data, 0o600); err != nil {
		t.Fatalf("write legacy meta.json: %v", err)
	}

	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.AutoMode {
		t.Fatalf("Read().AutoMode = true for legacy omitted field: %#v", read)
	}

	records, err := store.List(flowstore.FlowFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].AutoMode {
		t.Fatalf("List() = %#v, want one legacy flow with auto mode disabled", records)
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

func TestStoreSetPhaseSyncsCompletedLinkedPlanPhase(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("plan NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Linked Plan",
		Markdown: "Build the thing.",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Status:  "in_progress",
			Order:   3,
		}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Phase sync",
		Instructions: "sync the linked plan",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPlanLink(flowstore.PlanLinkUpdate{FlowID: record.FlowID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("SetPlanLink() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation running) error = %v", err)
	}

	completed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Implementation finished.",
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}
	if got := phaseByID(t, completed, "implementation").Status; got != flowstore.PhaseCompleted {
		t.Fatalf("flow implementation status = %q, want completed", got)
	}

	plans, err := planStore.List(planstore.PlanFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("plan List() error = %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plan List() returned %d records, want 1: %#v", len(plans), plans)
	}
	implementation := planPhaseByID(t, plans[0], "implementation")
	if implementation.Status != "completed" {
		t.Fatalf("linked plan implementation status = %q, want completed; phases = %#v", implementation.Status, plans[0].Phases)
	}
}

func TestStoreSetPhaseDoesNotAddMissingLinkedPlanPhase(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("plan NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Linked Plan",
		Markdown: "Build the thing.",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Status:  "in_progress",
			Order:   3,
		}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Skip missing plan phase",
		Instructions: "do not pollute the plan",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPlanLink(flowstore.PlanLinkUpdate{FlowID: record.FlowID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("SetPlanLink() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation")

	completed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "review-loop",
		Status:  flowstore.PhaseCompleted,
		Summary: "Review loop passed.",
	})
	if err != nil {
		t.Fatalf("SetPhase(review-loop completed) error = %v", err)
	}
	if got := phaseByID(t, completed, "review-loop").Status; got != flowstore.PhaseCompleted {
		t.Fatalf("flow review-loop status = %q, want completed", got)
	}

	plans, err := planStore.List(planstore.PlanFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("plan List() error = %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plan List() returned %d records, want 1: %#v", len(plans), plans)
	}
	if _, ok := maybePlanPhaseByID(plans[0], "review-loop"); ok {
		t.Fatalf("linked plan unexpectedly gained review-loop phase: %#v", plans[0].Phases)
	}
}

func TestStoreSetPhaseCompletesWithoutLinkedPlanMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "No linked plan",
		Instructions: "complete without plan metadata",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation running) error = %v", err)
	}

	completed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Implementation finished without a linked plan.",
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}
	phase := phaseByID(t, completed, "implementation")
	if phase.Status != flowstore.PhaseCompleted || completed.PlanID != "" || completed.PlanPath != "" {
		t.Fatalf("completed flow without linked plan = %#v", completed)
	}
}

func TestStoreSetPhaseMarksNeedsAttentionWhenLinkedPlanSyncFails(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Plan sync failure",
		Instructions: "surface failed plan persistence",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation running) error = %v", err)
	}
	record, err = store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:   record.FlowID,
		PlanID:   "missing-plan",
		PlanPath: filepath.Join(root, "plans", "missing-plan", "plan.md"),
	})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}

	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Implementation finished.",
	})
	if err == nil || !strings.Contains(err.Error(), "sync linked plan phase") {
		t.Fatalf("SetPhase(implementation completed) error = %v, want linked plan sync failure", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := phaseByID(t, read, "implementation")
	if phase.Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("implementation status after sync failure = %q, want needs_attention", phase.Status)
	}
	if !strings.Contains(phase.Notes, "missing-plan") {
		t.Fatalf("implementation notes = %q, want missing plan detail", phase.Notes)
	}
	if read.Status != flowstore.StatusNeedsAttention {
		t.Fatalf("flow status = %q, want needs_attention", read.Status)
	}
}

func TestStoreSetPhaseMarksNeedsAttentionWhenMatchingLinkedPlanPhaseUpdateFails(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("plan NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Linked Plan",
		Markdown: "Build the thing.",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{{
			PhaseID: "implementation",
			Status:  "in_progress",
			Order:   3,
		}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Plan phase update failure",
		Instructions: "surface matching phase persistence failure",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPlanLink(flowstore.PlanLinkUpdate{FlowID: record.FlowID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("SetPlanLink() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation running) error = %v", err)
	}

	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Implementation finished.",
	})
	if err == nil || !strings.Contains(err.Error(), "phase title is required") {
		t.Fatalf("SetPhase(implementation completed) error = %v, want plan phase title failure", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := phaseByID(t, read, "implementation")
	if phase.Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("implementation status after matching plan phase update failure = %q, want needs_attention", phase.Status)
	}
	if !strings.Contains(phase.Notes, "phase title is required") {
		t.Fatalf("implementation notes = %q, want phase title failure detail", phase.Notes)
	}
}

func TestStoreSetPhaseSkipsAlreadyCompletedLinkedPlanPhase(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("plan NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Linked Plan",
		Markdown: "Build the thing.",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{{
			PhaseID: "implementation",
			Status:  "completed",
			Order:   3,
		}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Already synced plan phase",
		Instructions: "do not rewrite completed plan phase",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetPlanLink(flowstore.PlanLinkUpdate{FlowID: record.FlowID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("SetPlanLink() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation running) error = %v", err)
	}

	completed, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Implementation finished.",
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}
	if got := phaseByID(t, completed, "implementation").Status; got != flowstore.PhaseCompleted {
		t.Fatalf("flow implementation status = %q, want completed", got)
	}
}

func TestStoreSetPhaseCompletedRetryIgnoresPlanSyncFailureAndPreservesCompletedFlow(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Completed retry",
		Instructions: "do not demote completed phase",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Implementation finished before the plan link existed.",
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}
	record, err = store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:   record.FlowID,
		PlanID:   "missing-plan",
		PlanPath: filepath.Join(root, "plans", "missing-plan", "plan.md"),
	})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseCompleted,
		Summary: "Idempotent completion retry.",
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation completed retry) error = %v", err)
	}
	if got := phaseByID(t, updated, "implementation").Status; got != flowstore.PhaseCompleted {
		t.Fatalf("updated implementation status = %q, want completed", got)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := phaseByID(t, read, "implementation")
	if phase.Status != flowstore.PhaseCompleted {
		t.Fatalf("implementation status after completed retry sync failure = %q, want completed", phase.Status)
	}
	if read.Status != flowstore.StatusInProgress {
		t.Fatalf("flow status = %q, want in_progress", read.Status)
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

func TestStoreResetAwaitingSessionPhaseReturnsRunningOrphanToReady(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Reset orphaned implementation launch",
		Instructions: "recover a phase stuck waiting on session capture",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "implementation",
		LaunchID: "launch-old",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(old) error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{
			Provider:  "codex",
			SessionID: "session-old",
			LaunchID:  "launch-old",
			Status:    "ended",
		},
	})
	if err != nil {
		t.Fatalf("AttachSession(old) error = %v", err)
	}
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "implementation",
		LaunchID: "launch-orphan",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(orphan) error = %v", err)
	}
	if phase := phaseByID(t, record, "implementation"); phase.Status != flowstore.PhaseRunning || !flowstore.PhaseAwaitingSession(phase) {
		t.Fatalf("implementation before reset = %#v, want running await-session", phase)
	}

	reset, err := store.ResetAwaitingSessionPhase(flowstore.PhaseResetUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
	})
	if err != nil {
		t.Fatalf("ResetAwaitingSessionPhase() error = %v", err)
	}

	phase := phaseByID(t, reset, "implementation")
	if phase.Status != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready", phase.Status)
	}
	if flowstore.PhaseAwaitingSession(phase) {
		t.Fatalf("implementation should no longer be awaiting session: %#v", phase)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-old" {
		t.Fatalf("launch ids = %#v, want only older attached launch", phase.LaunchIDs)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].LaunchID != "launch-old" || phase.Sessions[0].SessionID != "session-old" {
		t.Fatalf("sessions = %#v, want older session preserved", phase.Sessions)
	}
	if reset.Status != flowstore.StatusInProgress {
		t.Fatalf("flow status = %q, want in_progress", reset.Status)
	}
}

func TestStoreResetRecoverableRunningPhaseReturnsEndedLatestSessionToReady(t *testing.T) {
	root := t.TempDir()
	endedAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateFlow(t, store, "Reset ended implementation launch")
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-old"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(old) error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{
			Provider:  "codex",
			SessionID: "session-old",
			LaunchID:  "launch-old",
			Status:    "ended",
			EndedAt:   endedAt.Add(-time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("AttachSession(old) error = %v", err)
	}
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-ended"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(ended) error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{
			Provider:  "claude",
			SessionID: "session-ended",
			LaunchID:  "launch-ended",
			Status:    "ended",
			EndedAt:   endedAt,
		},
	})
	if err != nil {
		t.Fatalf("AttachSession(ended) error = %v", err)
	}

	reset, err := store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
	})
	if err != nil {
		t.Fatalf("ResetRecoverableRunningPhase() error = %v", err)
	}

	phase := phaseByID(t, reset, "implementation")
	if phase.Status != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready", phase.Status)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-old" {
		t.Fatalf("launch ids = %#v, want only older launch", phase.LaunchIDs)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].SessionID != "session-old" || phase.Sessions[0].LaunchID != "launch-old" {
		t.Fatalf("sessions = %#v, want only older session preserved", phase.Sessions)
	}
	if reset.Status != flowstore.StatusInProgress {
		t.Fatalf("flow status = %q, want in_progress", reset.Status)
	}
}

func TestStoreResetAwaitingSessionPhaseRejectsIneligiblePhases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord
		phaseID string
		want    string
	}{
		{
			name: "ready phase",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				record := mustCreateFlow(t, store, "Ready reset rejection")
				mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
				return record
			},
			phaseID: "implementation",
			want:    "requires running recoverable phase",
		},
		{
			name: "latest launch has attached session",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				record := mustCreateFlow(t, store, "Attached session reset rejection")
				mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
				var err error
				record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-1"})
				if err != nil {
					t.Fatalf("AddPhaseLaunchID() error = %v", err)
				}
				record, err = store.AttachSession(flowstore.SessionAttachUpdate{
					FlowID:  record.FlowID,
					PhaseID: "implementation",
					Session: flowstore.Session{Provider: "codex", SessionID: "session-1", LaunchID: "launch-1"},
				})
				if err != nil {
					t.Fatalf("AttachSession() error = %v", err)
				}
				return record
			},
			phaseID: "implementation",
			want:    "requires latest launch without a live attached session",
		},
		{
			name: "latest launch has live session with older ended history",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				record := mustCreateFlow(t, store, "Live latest session reset rejection")
				mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
				var err error
				record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-old"})
				if err != nil {
					t.Fatalf("AddPhaseLaunchID(old) error = %v", err)
				}
				record, err = store.AttachSession(flowstore.SessionAttachUpdate{
					FlowID:  record.FlowID,
					PhaseID: "implementation",
					Session: flowstore.Session{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"},
				})
				if err != nil {
					t.Fatalf("AttachSession(old) error = %v", err)
				}
				record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-live"})
				if err != nil {
					t.Fatalf("AddPhaseLaunchID(live) error = %v", err)
				}
				record, err = store.AttachSession(flowstore.SessionAttachUpdate{
					FlowID:  record.FlowID,
					PhaseID: "implementation",
					Session: flowstore.Session{Provider: "claude", SessionID: "session-live", LaunchID: "launch-live", Status: "running"},
				})
				if err != nil {
					t.Fatalf("AttachSession(live) error = %v", err)
				}
				return record
			},
			phaseID: "implementation",
			want:    "requires latest launch without a live attached session",
		},
		{
			name: "latest launch has mixed live and ended sessions",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				record := mustCreateFlow(t, store, "Mixed latest session reset rejection")
				mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
				var err error
				record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-mixed"})
				if err != nil {
					t.Fatalf("AddPhaseLaunchID() error = %v", err)
				}
				for _, session := range []flowstore.Session{
					{Provider: "codex", SessionID: "session-ended", LaunchID: "launch-mixed", Status: "ended"},
					{Provider: "claude", SessionID: "session-live", LaunchID: "launch-mixed"},
				} {
					record, err = store.AttachSession(flowstore.SessionAttachUpdate{FlowID: record.FlowID, PhaseID: "implementation", Session: session})
					if err != nil {
						t.Fatalf("AttachSession(%s) error = %v", session.SessionID, err)
					}
				}
				return record
			},
			phaseID: "implementation",
			want:    "requires latest launch without a live attached session",
		},
		{
			name: "attached session launch mismatch",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				record, err := store.Create(flowstore.FlowRecord{
					Title:        "Mismatched session reset rejection",
					Instructions: "do not reset mismatched sessions",
					RepoPath:     filepath.Join(t.TempDir(), "repo"),
					Phases: []flowstore.FlowPhase{
						{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
						{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Order: 2},
						{
							PhaseID:   "implementation",
							Title:     "Implementation",
							Status:    flowstore.PhaseRunning,
							Order:     3,
							LaunchIDs: []string{"launch-orphan"},
							Sessions: []flowstore.Session{
								{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale"},
							},
						},
					},
				})
				if err != nil {
					t.Fatalf("Create(mismatched) error = %v", err)
				}
				return record
			},
			phaseID: "implementation",
			want:    "requires attached sessions to match phase launch ids",
		},
		{
			name: "missing phase",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				return mustCreateFlow(t, store, "Missing phase reset rejection")
			},
			phaseID: "missing-phase",
			want:    `phase "missing-phase" not found`,
		},
		{
			name: "predecessors unsatisfied",
			setup: func(t *testing.T, store *flowstore.Store) flowstore.FlowRecord {
				t.Helper()
				record, err := store.Create(flowstore.FlowRecord{
					FlowID:       "unsatisfied-reset",
					Title:        "Unsatisfied reset rejection",
					Instructions: "do not reset behind an open predecessor",
					RepoPath:     filepath.Join(t.TempDir(), "repo"),
					Phases: []flowstore.FlowPhase{
						{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseRunning, Order: 1},
						{PhaseID: "beta", Title: "Beta", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-orphan"}},
					},
				})
				if err != nil {
					t.Fatalf("Create(custom) error = %v", err)
				}
				return record
			},
			phaseID: "beta",
			want:    "requires satisfied predecessors",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record := tc.setup(t, store)

			_, err = store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: tc.phaseID})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ResetRecoverableRunningPhase() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStoreResetAwaitingSessionPhaseCollapsesDuplicateRows(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicate-reset",
		Title:        "Duplicate reset",
		Instructions: "reset the active duplicate row",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{
				PhaseID: "Step-1", Title: "Step 1", Status: flowstore.PhaseCompleted, Order: 2,
				LaunchIDs: []string{"launch-old", "launch-orphan"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"},
					{Provider: "claude", SessionID: "session-orphan", LaunchID: "launch-orphan", Status: "ended"},
				},
			},
			{PhaseID: "step-1", Title: "Step 1", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-orphan"}},
			{PhaseID: "omega", Title: "Omega", Status: flowstore.PhasePending, Order: 3},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.ResetAwaitingSessionPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: "step-1"})
	if err != nil {
		t.Fatalf("ResetAwaitingSessionPhase() error = %v", err)
	}

	count := 0
	var survivor flowstore.FlowPhase
	for _, phase := range record.Phases {
		if strings.EqualFold(phase.PhaseID, "step-1") {
			count++
			survivor = phase
		}
	}
	if count != 1 {
		t.Fatalf("duplicate rows not collapsed: %#v", record.Phases)
	}
	if survivor.PhaseID != "step-1" || survivor.Status != flowstore.PhaseReady {
		t.Fatalf("survivor = %#v, want normalized ready step-1", survivor)
	}
	if len(survivor.LaunchIDs) != 1 || survivor.LaunchIDs[0] != "launch-old" {
		t.Fatalf("survivor launch ids = %#v, want older launch only", survivor.LaunchIDs)
	}
	if flowstore.PhaseAwaitingSession(survivor) {
		t.Fatalf("survivor should not keep duplicate orphan launch after reset: %#v", survivor)
	}
	if len(survivor.Sessions) != 1 || survivor.Sessions[0].SessionID != "session-old" {
		t.Fatalf("survivor sessions = %#v, want older session preserved", survivor.Sessions)
	}
	if got := phaseByID(t, record, "omega").Status; got != flowstore.PhasePending {
		t.Fatalf("omega status = %q, want pending behind ready reset phase", got)
	}
}

func TestStoreResetRecoverableRunningPhaseRemovesEndedSessionMergedFromDuplicateRow(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicate-ended-reset",
		Title:        "Duplicate ended reset",
		Instructions: "reset the active duplicate row with ended session history",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{
				PhaseID: "Step-1", Title: "Step 1", Status: flowstore.PhaseCompleted, Order: 2,
				LaunchIDs: []string{"launch-old", "launch-ended"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"},
					{Provider: "claude", SessionID: "session-ended", LaunchID: "launch-ended", Status: "ended"},
				},
			},
			{PhaseID: "step-1", Title: "Step 1", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-ended"}},
			{PhaseID: "omega", Title: "Omega", Status: flowstore.PhasePending, Order: 3},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: "step-1"})
	if err != nil {
		t.Fatalf("ResetRecoverableRunningPhase() error = %v", err)
	}

	survivor := phaseByID(t, record, "step-1")
	if survivor.Status != flowstore.PhaseReady {
		t.Fatalf("survivor status = %q, want ready", survivor.Status)
	}
	for _, launchID := range survivor.LaunchIDs {
		if launchID == "launch-ended" {
			t.Fatalf("survivor launch ids = %#v, want ended launch removed", survivor.LaunchIDs)
		}
	}
	for _, session := range survivor.Sessions {
		if session.LaunchID == "launch-ended" {
			t.Fatalf("survivor sessions = %#v, want ended launch session removed", survivor.Sessions)
		}
	}
	if len(survivor.Sessions) != 1 || survivor.Sessions[0].SessionID != "session-old" {
		t.Fatalf("survivor sessions = %#v, want older session preserved", survivor.Sessions)
	}
}

func TestStoreResetRecoverableRunningPhaseRejectsLiveSessionMergedFromDuplicateRow(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicate-live-reset",
		Title:        "Duplicate live reset",
		Instructions: "do not discard live session history from duplicate rows",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{
				PhaseID:   "Step-1",
				Title:     "Step 1",
				Status:    flowstore.PhaseCompleted,
				Order:     2,
				LaunchIDs: []string{"launch-live"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-live", LaunchID: "launch-live", Status: "running"},
				},
			},
			{PhaseID: "step-1", Title: "Step 1", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-live"}},
			{PhaseID: "omega", Title: "Omega", Status: flowstore.PhasePending, Order: 3},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: "step-1"})
	if err == nil || !strings.Contains(err.Error(), "requires latest launch without a live attached session") {
		t.Fatalf("ResetRecoverableRunningPhase() error = %v, want live session rejection", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(read.Phases) != len(record.Phases) {
		t.Fatalf("phase rows changed after rejected reset: %#v", read.Phases)
	}
	for i := range read.Phases {
		if read.Phases[i].PhaseID != record.Phases[i].PhaseID ||
			read.Phases[i].Status != record.Phases[i].Status ||
			strings.Join(read.Phases[i].LaunchIDs, ",") != strings.Join(record.Phases[i].LaunchIDs, ",") ||
			len(read.Phases[i].Sessions) != len(record.Phases[i].Sessions) {
			t.Fatalf("phase %d changed after rejected reset: before=%#v after=%#v", i, record.Phases[i], read.Phases[i])
		}
	}
}

func TestStoreResetRecoverableRunningPhaseRejectsOtherLiveLaunchMergedFromDuplicateRow(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicate-other-live-reset",
		Title:        "Duplicate other live reset",
		Instructions: "do not reset while any duplicate-row launch is live",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{
				PhaseID:   "Step-1",
				Title:     "Step 1",
				Status:    flowstore.PhaseCompleted,
				Order:     2,
				LaunchIDs: []string{"launch-live"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-live", LaunchID: "launch-live", Status: "running"},
				},
			},
			{PhaseID: "step-1", Title: "Step 1", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-stale"}},
			{PhaseID: "omega", Title: "Omega", Status: flowstore.PhasePending, Order: 3},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: "step-1"})
	if err == nil || !strings.Contains(err.Error(), "requires latest launch without a live attached session") {
		t.Fatalf("ResetRecoverableRunningPhase() error = %v, want duplicate live launch rejection", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(read.Phases) != len(record.Phases) {
		t.Fatalf("phase rows changed after rejected reset: %#v", read.Phases)
	}
	for i := range read.Phases {
		if read.Phases[i].PhaseID != record.Phases[i].PhaseID ||
			read.Phases[i].Status != record.Phases[i].Status ||
			strings.Join(read.Phases[i].LaunchIDs, ",") != strings.Join(record.Phases[i].LaunchIDs, ",") ||
			len(read.Phases[i].Sessions) != len(record.Phases[i].Sessions) {
			t.Fatalf("phase %d changed after rejected reset: before=%#v after=%#v", i, record.Phases[i], read.Phases[i])
		}
	}
}

func TestStoreMarkPhaseLaunchEndedUpdatesAttachedFlowSession(t *testing.T) {
	root := t.TempDir()
	endedAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateFlow(t, store, "Finalize attached Flow session")
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-1"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{Provider: "codex", SessionID: "session-1", LaunchID: "launch-1", Status: "last_seen"},
	})
	if err != nil {
		t.Fatalf("AttachSession() error = %v", err)
	}

	record, err = store.MarkPhaseLaunchEnded(flowstore.PhaseLaunchEndUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "implementation",
		LaunchID: "launch-1",
		EndedAt:  endedAt,
	})
	if err != nil {
		t.Fatalf("MarkPhaseLaunchEnded() error = %v", err)
	}

	phase := phaseByID(t, record, "implementation")
	if reason, ok := flowstore.RecoverableRunningPhaseResetReason(phase); !ok || reason != flowstore.PhaseResetReasonEndedSession {
		t.Fatalf("RecoverableRunningPhaseResetReason() = %q, %v; want ended-session", reason, ok)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].Status != "ended" || !phase.Sessions[0].EndedAt.Equal(endedAt) {
		t.Fatalf("session = %#v, want ended at %s", phase.Sessions, endedAt)
	}
}

func TestStoreMarkPhaseLaunchEndedUpdatesSessionMergedFromDuplicateRow(t *testing.T) {
	root := t.TempDir()
	endedAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicate-finalize",
		Title:        "Duplicate finalize",
		Instructions: "mark duplicate-row sessions ended",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{
				PhaseID:   "Step-1",
				Title:     "Step 1",
				Status:    flowstore.PhaseCompleted,
				Order:     2,
				LaunchIDs: []string{"launch-1"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-1", LaunchID: "launch-1", Status: "last_seen"},
				},
			},
			{PhaseID: "step-1", Title: "Step 1", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-1"}},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.MarkPhaseLaunchEnded(flowstore.PhaseLaunchEndUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "step-1",
		LaunchID: "launch-1",
		EndedAt:  endedAt,
	})
	if err != nil {
		t.Fatalf("MarkPhaseLaunchEnded() error = %v", err)
	}

	phase := phaseByID(t, record, "step-1")
	if reason, ok := flowstore.RecoverableRunningPhaseResetReason(phase); !ok || reason != flowstore.PhaseResetReasonEndedSession {
		t.Fatalf("RecoverableRunningPhaseResetReason() = %q, %v; want ended-session", reason, ok)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].Status != "ended" || !phase.Sessions[0].EndedAt.Equal(endedAt) {
		t.Fatalf("session = %#v, want ended duplicate-row session", phase.Sessions)
	}
}

func TestStoreMarkPhaseLaunchEndedNoopsWhenLaunchHasNoAttachedSession(t *testing.T) {
	root := t.TempDir()
	endedAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicate-finalize-noop",
		Title:        "Duplicate finalize noop",
		Instructions: "do not rewrite unmatched launch finalization",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{
				PhaseID:   "Step-1",
				Title:     "Step 1",
				Status:    flowstore.PhaseCompleted,
				Order:     2,
				LaunchIDs: []string{"launch-old"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"},
				},
			},
			{PhaseID: "step-1", Title: "Step 1", Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-orphan"}},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	before, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal(before) error = %v", err)
	}

	updated, err := store.MarkPhaseLaunchEnded(flowstore.PhaseLaunchEndUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "step-1",
		LaunchID: "launch-orphan",
		EndedAt:  endedAt,
	})
	if err != nil {
		t.Fatalf("MarkPhaseLaunchEnded() error = %v", err)
	}
	after, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("Marshal(after) error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("MarkPhaseLaunchEnded changed unmatched launch record:\nbefore=%s\nafter=%s", before, after)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	persisted, err := json.Marshal(read)
	if err != nil {
		t.Fatalf("Marshal(read) error = %v", err)
	}
	if string(persisted) != string(before) {
		t.Fatalf("MarkPhaseLaunchEnded persisted changes for unmatched launch:\nbefore=%s\nafter=%s", before, persisted)
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

func TestStoreRestartPhaseAtomicallyRequiresRecoveryState(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Atomic restart",
		Instructions: "restart only recovery states",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/atomic-restart",
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
		HeadBranch: "flow/atomic-restart",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}

	_, err = store.RestartPhase(flowstore.PhaseRestartUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Notes:   "Rerunning Autoreview after addressing prior findings.",
	})
	if err == nil || !strings.Contains(err.Error(), "autoreview is ready") {
		t.Fatalf("RestartPhase(ready) error = %v, want ready rejection", err)
	}

	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Status:  flowstore.PhaseNeedsAttention,
		Outcome: "needs_attention",
		Notes:   "Follow-up concern remains.",
	})
	if err != nil {
		t.Fatalf("SetPhase(needs_attention) error = %v", err)
	}
	record, err = store.RestartPhase(flowstore.PhaseRestartUpdate{
		FlowID:  record.FlowID,
		PhaseID: "autoreview",
		Notes:   "Rerunning Autoreview after addressing prior findings.",
	})
	if err != nil {
		t.Fatalf("RestartPhase(needs_attention) error = %v", err)
	}
	phase := phaseByID(t, record, "autoreview")
	if phase.Status != flowstore.PhaseRunning || phase.Outcome != "" {
		t.Fatalf("autoreview after restart = %#v, want running with cleared outcome", phase)
	}
}

func TestStoreRestartPhaseClearsBlockedMergeMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Restart merge",
		Instructions: "clear blocked merge metadata",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/restart-merge",
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
		HeadBranch: "flow/restart-merge",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "autoreview")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "merge",
		Status:  flowstore.PhaseBlocked,
		Outcome: flowstore.OutcomeBlocked,
		Notes:   "CI is not green.",
	})
	if err != nil {
		t.Fatalf("SetPhase(merge blocked) error = %v", err)
	}
	record, err = store.SetMerge(flowstore.MergeUpdate{
		FlowID: record.FlowID,
		Status: flowstore.MergeBlocked,
	})
	if err != nil {
		t.Fatalf("SetMerge(blocked) error = %v", err)
	}
	if record.Merge.Status != flowstore.MergeBlocked || record.Status != flowstore.StatusBlocked {
		t.Fatalf("blocked merge record = %#v", record)
	}

	record, err = store.RestartPhase(flowstore.PhaseRestartUpdate{
		FlowID:  record.FlowID,
		PhaseID: "merge",
		Notes:   "Rerunning Merge after CI recovered.",
	})
	if err != nil {
		t.Fatalf("RestartPhase(merge) error = %v", err)
	}
	if got := phaseByID(t, record, "merge").Status; got != flowstore.PhaseRunning {
		t.Fatalf("merge phase status = %q, want running", got)
	}
	if record.Merge.Status != flowstore.MergePending {
		t.Fatalf("merge metadata after restart = %#v, want pending", record.Merge)
	}
	if record.Status != flowstore.StatusInProgress {
		t.Fatalf("flow status after merge restart = %q, want in_progress", record.Status)
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

func TestStoreAddPhaseLaunchIDResumePreservesCompletedPhase(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Resume completed phase",
		Instructions: "resume a session on a finished review loop",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/resume-completed",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "review-loop",
		Status:  flowstore.PhaseCompleted,
		Outcome: flowstore.OutcomeApproved,
	})
	if err != nil {
		t.Fatalf("SetPhase(review-loop completed) error = %v", err)
	}
	if got := phaseByID(t, record, "pr-creation").Status; got != flowstore.PhaseReady {
		t.Fatalf("pr-creation status before resume = %q, want ready", got)
	}

	resumed, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "review-loop",
		LaunchID: "launch-resume-1",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(review-loop resume) error = %v", err)
	}

	phase := phaseByID(t, resumed, "review-loop")
	if phase.Status != flowstore.PhaseCompleted || phase.Outcome != flowstore.OutcomeApproved {
		t.Fatalf("review-loop after resume = %#v, want completed with approved outcome", phase)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-resume-1" {
		t.Fatalf("launch ids = %#v", phase.LaunchIDs)
	}
	if got := phaseByID(t, resumed, "pr-creation").Status; got != flowstore.PhaseReady {
		t.Fatalf("pr-creation status after resume = %q, want ready", got)
	}
}

func TestStoreAddPhaseLaunchIDResumePreservesSkippedPhase(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Resume skipped phase",
		Instructions: "resume a session on a skipped review loop",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/resume-skipped",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "review-loop",
		Status:  flowstore.PhaseSkipped,
		Notes:   "Review covered during implementation.",
	})
	if err != nil {
		t.Fatalf("SetPhase(review-loop skipped) error = %v", err)
	}

	resumed, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "review-loop",
		LaunchID: "launch-resume-1",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(review-loop resume) error = %v", err)
	}

	phase := phaseByID(t, resumed, "review-loop")
	if phase.Status != flowstore.PhaseSkipped || phase.Notes != "Review covered during implementation." {
		t.Fatalf("review-loop after resume = %#v, want skipped with original notes", phase)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-resume-1" {
		t.Fatalf("launch ids = %#v", phase.LaunchIDs)
	}
}

func TestStoreAddPhaseLaunchIDResumeRefreshesReadinessForCustomGraph(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-terminal-resume",
		Title:        "Custom terminal resume",
		Instructions: "resume a terminal phase in a custom graph",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "beta", Title: "Beta", Status: flowstore.PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := phaseByID(t, record, "beta").Status; got != flowstore.PhasePending {
		t.Fatalf("beta status before resume = %q, want pending to prove Create did not refresh custom graph", got)
	}

	resumed, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "alpha",
		LaunchID: "launch-resume-1",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(alpha resume) error = %v", err)
	}

	alpha := phaseByID(t, resumed, "alpha")
	if alpha.Status != flowstore.PhaseCompleted {
		t.Fatalf("alpha after resume = %#v, want completed", alpha)
	}
	if len(alpha.LaunchIDs) != 1 || alpha.LaunchIDs[0] != "launch-resume-1" {
		t.Fatalf("alpha launch ids = %#v", alpha.LaunchIDs)
	}
	if got := phaseByID(t, resumed, "beta").Status; got != flowstore.PhaseReady {
		t.Fatalf("beta status after resume = %q, want ready", got)
	}
	if resumed.Status != flowstore.StatusInProgress {
		t.Fatalf("flow status after resume = %q, want in_progress", resumed.Status)
	}
}

func TestStoreAddPhaseLaunchIDResumeStillRestartsNeedsAttentionPhase(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Resume needs-attention phase",
		Instructions: "resume a session to keep fixing a flagged phase",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/resume-needs-attention",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Status:  flowstore.PhaseNeedsAttention,
		Notes:   "Tests are failing.",
	})
	if err != nil {
		t.Fatalf("SetPhase(implementation needs_attention) error = %v", err)
	}

	resumed, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "implementation",
		LaunchID: "launch-resume-1",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(implementation resume) error = %v", err)
	}

	phase := phaseByID(t, resumed, "implementation")
	if phase.Status != flowstore.PhaseRunning {
		t.Fatalf("implementation after resume = %#v, want running", phase)
	}
	if !strings.Contains(phase.Notes, "Relaunched after needs_attention") {
		t.Fatalf("implementation notes = %q, want relaunch note", phase.Notes)
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
	lockPath := flowLockPath(root, record.FlowID)
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
	oldRecordLockPath := filepath.Join(root, "flows", record.FlowID, ".update.lock")
	if err := os.WriteFile(oldRecordLockPath, []byte("not a live lock\n"), 0o600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	if err := os.Chmod(oldRecordLockPath, 0o644); err != nil {
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
	oldLockData, err := os.ReadFile(oldRecordLockPath)
	if err != nil {
		t.Fatalf("read old lock file: %v", err)
	}
	if string(oldLockData) != "not a live lock\n" {
		t.Fatalf("old in-record lock marker was modified: %q", oldLockData)
	}
	lockPath := flowLockPath(root, record.FlowID)
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if !strings.Contains(string(lockData), "\n") || strings.Contains(string(lockData), "not a live lock") {
		t.Fatalf("lock marker was not refreshed: %q", lockData)
	}
	assertMode(t, lockPath, 0o600)
}

func TestStoreDeleteRemovesOnlyFlowArtifacts(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo-worktrees", "flow-delete")
	planDir := filepath.Join(root, "plans", "plan-1")
	planPath := filepath.Join(planDir, "plan.md")
	sessionDir := filepath.Join(root, "sessions", "session-1")
	transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
	for _, dir := range []string{repoPath, worktreePath, planDir, sessionDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "delete-flow",
		Title:        "Delete Flow",
		Instructions: "remove only the flow artifact directory",
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		PlanID:       "plan-1",
		PlanPath:     planPath,
		Phases: []flowstore.FlowPhase{
			{
				PhaseID: "plan",
				Title:   "Plan",
				Status:  flowstore.PhaseCompleted,
				Order:   1,
				Sessions: []flowstore.Session{{
					Provider:       "codex",
					SessionID:      "session-1",
					TranscriptPath: transcriptPath,
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := store.Delete(record.FlowID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "flows", record.FlowID)); !os.IsNotExist(err) {
		t.Fatalf("flow directory still exists or stat failed with non-not-exist error: %v", err)
	}
	if _, err := store.Read(record.FlowID); !flowstore.IsNotFound(err) {
		t.Fatalf("Read(deleted) error = %v, want not found", err)
	}
	records, err := store.List(flowstore.FlowFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("List() returned deleted flow: %#v", records)
	}
	for _, path := range []string{repoPath, worktreePath, planPath, sessionDir, transcriptPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("Delete() removed or damaged non-flow artifact %s: %v", path, err)
		}
	}
	if _, err := os.Stat(flowLockPath(root, record.FlowID)); err != nil {
		t.Fatalf("Delete() should leave an out-of-record lock file: %v", err)
	}
}

func TestStoreDeleteMissingFlowReportsNotFound(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Delete("../bad"); err == nil || !strings.Contains(err.Error(), "invalid flow id") {
		t.Fatalf("Delete(invalid) error = %v, want invalid flow id", err)
	}
	if err := store.Delete("missing-flow"); !flowstore.IsNotFound(err) {
		t.Fatalf("Delete(missing) error = %v, want not found", err)
	}
	if _, err := store.Read("missing-flow"); !flowstore.IsNotFound(err) {
		t.Fatalf("Read(missing) error = %v, want not found", err)
	}
}

func TestStoreMutatorsReportNotFoundAfterDelete(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("plan NewStore() error = %v", err)
	}
	planID, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Plan",
		Status:   "approved",
		Markdown: "# Plan\n",
	})
	if err != nil {
		t.Fatalf("Save(plan) error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "stale-flow",
		Title:        "Stale Flow",
		Instructions: "delete before stale mutations return",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/stale",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Delete(record.FlowID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "SetPhase",
			run: func() error {
				_, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseRunning})
				return err
			},
		},
		{
			name: "RestartPhase",
			run: func() error {
				_, err := store.RestartPhase(flowstore.PhaseRestartUpdate{FlowID: record.FlowID, PhaseID: "plan", Notes: "rerun"})
				return err
			},
		},
		{
			name: "AddChildPhase",
			run: func() error {
				_, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
					FlowID:        record.FlowID,
					ParentPhaseID: "implementation",
					PhaseID:       "implementation-api",
					Title:         "API",
					Order:         10,
				})
				return err
			},
		},
		{
			name: "SetPlanLink",
			run: func() error {
				_, err := store.SetPlanLink(flowstore.PlanLinkUpdate{FlowID: record.FlowID, PlanID: planID})
				return err
			},
		},
		{
			name: "SetPR",
			run: func() error {
				_, err := store.SetPR(flowstore.PRUpdate{
					FlowID:     record.FlowID,
					Provider:   "github",
					Number:     12,
					URL:        "https://github.com/brian-bell/wtui/pull/12",
					HeadBranch: "flow/stale",
					BaseBranch: "main",
					Status:     "open",
				})
				return err
			},
		},
		{
			name: "SetMerge",
			run: func() error {
				_, err := store.SetMerge(flowstore.MergeUpdate{FlowID: record.FlowID, Status: flowstore.MergeBlocked})
				return err
			},
		},
		{
			name: "SetStartMetadata",
			run: func() error {
				_, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{FlowID: record.FlowID, Branch: "flow/stale"})
				return err
			},
		},
		{
			name: "AddPhaseLaunchID",
			run: func() error {
				_, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "plan", LaunchID: "launch-1"})
				return err
			},
		},
		{
			name: "AttachSession",
			run: func() error {
				_, err := store.AttachSession(flowstore.SessionAttachUpdate{
					FlowID:  record.FlowID,
					PhaseID: "plan",
					Session: flowstore.Session{Provider: "codex", SessionID: "session-1"},
				})
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !flowstore.IsNotFound(err) {
				t.Fatalf("%s() error = %v, want not found", tc.name, err)
			}
		})
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

func TestStoreSetIssuePersistsMetadata(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Issue metadata",
		Instructions: "record the issue",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/issue-metadata",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	later := now.Add(time.Minute)
	store, err = flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return later },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	updated, err := store.SetIssue(flowstore.IssueUpdate{
		FlowID:   record.FlowID,
		Provider: "github",
		Number:   123,
		URL:      "https://github.com/brian-bell/wtui/issues/123",
	})
	if err != nil {
		t.Fatalf("SetIssue() error = %v", err)
	}

	if updated.Issue.Provider != "github" ||
		updated.Issue.Number != 123 ||
		updated.Issue.URL != "https://github.com/brian-bell/wtui/issues/123" {
		t.Fatalf("Issue metadata = %#v", updated.Issue)
	}
	if updated.UpdatedAt != later {
		t.Fatalf("UpdatedAt = %s, want %s", updated.UpdatedAt, later)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Issue != updated.Issue {
		t.Fatalf("Read().Issue = %#v, want %#v", read.Issue, updated.Issue)
	}
}

func TestStoreSetIssueIdempotent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Issue metadata",
		Instructions: "record the issue",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/issue-metadata",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	update := flowstore.IssueUpdate{
		FlowID:   record.FlowID,
		Provider: "github",
		Number:   123,
		URL:      "https://github.com/brian-bell/wtui/issues/123",
	}
	record, err = store.SetIssue(update)
	if err != nil {
		t.Fatalf("SetIssue() error = %v", err)
	}

	later := now.Add(time.Minute)
	store, err = flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return later },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	same, err := store.SetIssue(update)
	if err != nil {
		t.Fatalf("SetIssue() second call error = %v", err)
	}
	if same.UpdatedAt != record.UpdatedAt {
		t.Fatalf("UpdatedAt changed on idempotent SetIssue: %s, want %s", same.UpdatedAt, record.UpdatedAt)
	}
}

func TestStoreReadLegacyFlowWithoutIssue(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-legacy-issue-flow"
	meta := filepath.Join(root, "flows", flowID, "meta.json")
	if err := os.MkdirAll(filepath.Dir(meta), 0o700); err != nil {
		t.Fatalf("create legacy flow dir: %v", err)
	}
	legacy := map[string]any{
		"schema_version": 1,
		"flow_id":        flowID,
		"title":          "Legacy issue flow",
		"instructions":   "older record",
		"status":         flowstore.StatusPending,
		"repo_path":      filepath.Join(root, "repo"),
		"merge":          map[string]any{"status": flowstore.MergePending},
		"phases": []map[string]any{
			{
				"phase_id":   "plan",
				"title":      "Plan",
				"kind":       "plan",
				"status":     flowstore.PhaseReady,
				"order":      1,
				"created_at": time.Now().UTC(),
				"updated_at": time.Now().UTC(),
			},
		},
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy record: %v", err)
	}
	if err := os.WriteFile(meta, data, 0o600); err != nil {
		t.Fatalf("write legacy meta.json: %v", err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Issue != (flowstore.Issue{}) {
		t.Fatalf("Issue = %#v, want zero value", read.Issue)
	}
	if flowstore.HasIssueTarget(read.Issue) {
		t.Fatalf("HasIssueTarget(%#v) = true, want false", read.Issue)
	}
}

func TestHasIssueTargetRequiresValidGitHubIssue(t *testing.T) {
	valid := flowstore.Issue{
		Provider: "github",
		Number:   123,
		URL:      "https://github.com/brian-bell/wtui/issues/123",
	}
	if !flowstore.HasIssueTarget(valid) {
		t.Fatalf("HasIssueTarget(valid) = false, want true")
	}
	for _, tc := range []struct {
		name  string
		issue flowstore.Issue
	}{
		{name: "provider", issue: flowstore.Issue{Provider: "gitlab", Number: 123, URL: valid.URL}},
		{name: "number", issue: flowstore.Issue{Provider: "github", Number: 0, URL: valid.URL}},
		{name: "url", issue: flowstore.Issue{Provider: "github", Number: 123, URL: "https://github.com/brian-bell/wtui/pull/123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if flowstore.HasIssueTarget(tc.issue) {
				t.Fatalf("HasIssueTarget(%#v) = true, want false", tc.issue)
			}
		})
	}
}

func TestStoreSetIssueValidatesMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update flowstore.IssueUpdate
		want   string
	}{
		{
			name:   "provider",
			update: flowstore.IssueUpdate{Provider: "gitlab", Number: 1, URL: "https://github.com/brian-bell/wtui/issues/1"},
			want:   "unsupported issue provider",
		},
		{
			name:   "number",
			update: flowstore.IssueUpdate{Provider: "github", Number: 0, URL: "https://github.com/brian-bell/wtui/issues/1"},
			want:   "issue number must be positive",
		},
		{
			name:   "url",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "not-a-url"},
			want:   "issue URL must be an absolute http(s) URL",
		},
		{
			name:   "url host",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "https://example.com/brian-bell/wtui/issues/1"},
			want:   "GitHub issue URL must use github.com",
		},
		{
			name:   "url number",
			update: flowstore.IssueUpdate{Provider: "github", Number: 2, URL: "https://github.com/brian-bell/wtui/issues/1"},
			want:   "GitHub issue URL number",
		},
		{
			name:   "url pull path",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "https://github.com/brian-bell/wtui/pull/1"},
			want:   "GitHub issue URL must have /owner/repo/issues/number path",
		},
		{
			name:   "url extra path",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "https://github.com/brian-bell/wtui/issues/1/comments"},
			want:   "GitHub issue URL must have /owner/repo/issues/number path",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.Create(flowstore.FlowRecord{
				Title:        "Issue validation",
				Instructions: "validate issue metadata",
				RepoPath:     filepath.Join(root, "repo"),
				Branch:       "flow/issue",
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			tc.update.FlowID = record.FlowID
			_, err = store.SetIssue(tc.update)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetIssue() error = %v, want %q", err, tc.want)
			}
		})
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

func TestStoreMarkManualMergeCompletesMergeAndRecordsMetadata(t *testing.T) {
	root := t.TempDir()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Manual merge",
		Instructions: "record manually merged GitHub PR",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/manual-merge",
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
		HeadBranch: "flow/manual-merge",
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

	updated, err := store.MarkManualMerge(flowstore.ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: 116,
		PRURL:    "https://github.com/brian-bell/wtui/pull/116",
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err != nil {
		t.Fatalf("MarkManualMerge() error = %v", err)
	}

	if updated.Status != flowstore.StatusMerged {
		t.Fatalf("flow status = %q, want merged", updated.Status)
	}
	if updated.PR.Status != flowstore.MergeMerged {
		t.Fatalf("PR status = %q, want merged", updated.PR.Status)
	}
	mergePhase := phaseByID(t, updated, "merge")
	if mergePhase.Status != flowstore.PhaseCompleted ||
		mergePhase.Outcome != flowstore.MergeMerged ||
		!strings.Contains(mergePhase.Summary, "Marked GitHub PR #116 as manually merged at 0123456789abcdef") {
		t.Fatalf("merge phase = %#v", mergePhase)
	}
	if updated.Merge.Status != flowstore.MergeMerged ||
		updated.Merge.Commit != "0123456789abcdef" ||
		updated.Merge.MergedAt == nil ||
		!updated.Merge.MergedAt.Equal(mergedAt) {
		t.Fatalf("merge metadata = %#v", updated.Merge)
	}
}

func TestStoreMarkManualMergeRejectsChangedPRTarget(t *testing.T) {
	root := t.TempDir()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateManualMergeFlow(t, store, root, true, true)
	record, err = store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     117,
		URL:        "https://github.com/brian-bell/wtui/pull/117",
		HeadBranch: "flow/manual-merge",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}

	_, err = store.MarkManualMerge(flowstore.ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: 116,
		PRURL:    "https://github.com/brian-bell/wtui/pull/116",
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err == nil || !strings.Contains(err.Error(), "PR target changed") {
		t.Fatalf("MarkManualMerge() error = %v, want PR target changed", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Status == flowstore.StatusMerged || read.PR.Status == flowstore.MergeMerged || read.Merge.Status == flowstore.MergeMerged {
		t.Fatalf("rejected manual merge mutated record to merged: %#v", read)
	}
}

func TestStoreMarkManualMergeValidatesEligibility(t *testing.T) {
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		withPR      bool
		commit      string
		mergedAt    time.Time
		mergeStatus string
		skipAutoRev bool
		mutate      func(*flowstore.FlowRecord)
		want        string
	}{
		{name: "missing PR", commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseReady, want: "requires existing PR metadata"},
		{name: "missing commit", withPR: true, mergedAt: mergedAt, mergeStatus: flowstore.PhaseReady, want: "requires merge commit"},
		{name: "missing timestamp", withPR: true, commit: "abc123", mergeStatus: flowstore.PhaseReady, want: "requires merge timestamp"},
		{name: "predecessors incomplete", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhasePending, skipAutoRev: true, want: "requires satisfied predecessors"},
		{name: "running merge", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseRunning, want: "requires merge phase ready or completed"},
		{name: "blocked merge", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseBlocked, want: "requires merge phase ready or completed"},
		{name: "needs attention merge", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseNeedsAttention, want: "requires merge phase ready or completed"},
		{name: "skipped merge", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseSkipped, want: "requires merge phase ready or completed"},
		{name: "invalid PR provider", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseReady, mutate: func(record *flowstore.FlowRecord) {
			record.PR.Provider = "gitlab"
		}, want: "requires existing PR metadata"},
		{name: "invalid PR number", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseReady, mutate: func(record *flowstore.FlowRecord) {
			record.PR.Number = 0
		}, want: "requires existing PR metadata"},
		{name: "invalid PR URL", withPR: true, commit: "abc123", mergedAt: mergedAt, mergeStatus: flowstore.PhaseReady, mutate: func(record *flowstore.FlowRecord) {
			record.PR.URL = "not-a-url"
		}, want: "requires existing PR metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record := mustCreateManualMergeFlow(t, store, root, tc.withPR, !tc.skipAutoRev)
			if tc.mergeStatus != "" {
				for i := range record.Phases {
					if record.Phases[i].PhaseID == "merge" {
						record.Phases[i].Status = tc.mergeStatus
						if tc.mergeStatus == flowstore.PhaseBlocked || tc.mergeStatus == flowstore.PhaseSkipped {
							record.Phases[i].Notes = "not mergeable"
						}
					}
				}
				if err := writeFlowRecordForTest(root, record); err != nil {
					t.Fatalf("write test flow: %v", err)
				}
			}
			if tc.mutate != nil {
				tc.mutate(&record)
				if err := writeFlowRecordForTest(root, record); err != nil {
					t.Fatalf("write test flow: %v", err)
				}
			}

			_, err = store.MarkManualMerge(flowstore.ManualMergeUpdate{
				FlowID:   record.FlowID,
				PRNumber: record.PR.Number,
				PRURL:    record.PR.URL,
				Commit:   tc.commit,
				MergedAt: tc.mergedAt,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("MarkManualMerge() error = %v, want %q", err, tc.want)
			}
			read, readErr := store.Read(record.FlowID)
			if readErr != nil {
				t.Fatalf("Read() error = %v", readErr)
			}
			if read.Merge.Status == flowstore.MergeMerged || read.Status == flowstore.StatusMerged {
				t.Fatalf("rejected manual merge mutated record to merged: %#v", read)
			}
		})
	}
}

func TestStoreMarkManualMergeCollapsesDuplicateMergePhases(t *testing.T) {
	root := t.TempDir()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateManualMergeFlow(t, store, root, true, true)
	record.Phases = append(record.Phases, flowstore.FlowPhase{
		PhaseID: " Merge ",
		Title:   "Legacy duplicate merge",
		Status:  flowstore.PhaseBlocked,
		Notes:   "legacy duplicate should collapse",
	})
	if err := writeFlowRecordForTest(root, record); err != nil {
		t.Fatalf("write test flow: %v", err)
	}

	updated, err := store.MarkManualMerge(flowstore.ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: 116,
		PRURL:    "https://github.com/brian-bell/wtui/pull/116",
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err != nil {
		t.Fatalf("MarkManualMerge() error = %v", err)
	}

	var mergeRows int
	for _, phase := range updated.Phases {
		if strings.EqualFold(strings.TrimSpace(phase.PhaseID), "merge") {
			mergeRows++
			if phase.PhaseID != "merge" || phase.Status != flowstore.PhaseCompleted {
				t.Fatalf("collapsed merge phase = %#v", phase)
			}
		}
	}
	if mergeRows != 1 {
		t.Fatalf("merge phase rows = %d, want 1 in %#v", mergeRows, updated.Phases)
	}
}

func TestStoreMarkManualMergeLinkedPlanSyncFailureKeepsFlowRecoverable(t *testing.T) {
	root := t.TempDir()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateManualMergeFlow(t, store, root, true, true)
	record, err = store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:   record.FlowID,
		PlanID:   "missing-plan",
		PlanPath: filepath.Join(root, "plans", "missing-plan", "plan.md"),
	})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}

	updated, err := store.MarkManualMerge(flowstore.ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: 116,
		PRURL:    "https://github.com/brian-bell/wtui/pull/116",
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err == nil || !strings.Contains(err.Error(), "sync linked plan phase") {
		t.Fatalf("MarkManualMerge() error = %v, want sync failure", err)
	}
	if updated.FlowID != record.FlowID {
		t.Fatalf("MarkManualMerge() returned flow ID %q, want recovered record", updated.FlowID)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	mergePhase := phaseByID(t, read, "merge")
	if mergePhase.Status != flowstore.PhaseNeedsAttention || !strings.Contains(mergePhase.Notes, "missing-plan") {
		t.Fatalf("merge phase after sync failure = %#v", mergePhase)
	}
	if read.Merge.Status != flowstore.MergePending || read.Merge.Commit != "" || read.Merge.MergedAt != nil {
		t.Fatalf("merge metadata after sync failure = %#v, want pending", read.Merge)
	}
	if read.PR.Status != "open" {
		t.Fatalf("PR status after sync failure = %q, want open", read.PR.Status)
	}
	if read.Status == flowstore.StatusMerged {
		t.Fatalf("flow status after sync failure = %q, want recoverable non-merged", read.Status)
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

func TestStoreAddPhaseLaunchIDResumePreservesTerminalMergeMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Resume merge",
		Instructions: "resume a completed merge session",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/resume-merge",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
	record, err = store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     189,
		URL:        "https://github.com/brian-bell/wtui/pull/189",
		HeadBranch: "flow/resume-merge",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "autoreview", "merge")
	mergedAt := time.Date(2026, 6, 12, 11, 12, 13, 0, time.UTC)
	record, err = store.SetMerge(flowstore.MergeUpdate{
		FlowID:   record.FlowID,
		Status:   flowstore.MergeMerged,
		Commit:   "abcdef0123456789",
		MergedAt: mergedAt,
	})
	if err != nil {
		t.Fatalf("SetMerge() error = %v", err)
	}

	resumed, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "merge",
		LaunchID: "launch-merge-resume",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(merge resume) error = %v", err)
	}

	phase := phaseByID(t, resumed, "merge")
	if phase.Status != flowstore.PhaseCompleted {
		t.Fatalf("merge phase after resume = %#v, want completed", phase)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-merge-resume" {
		t.Fatalf("merge launch ids = %#v", phase.LaunchIDs)
	}
	if resumed.Merge.Status != flowstore.MergeMerged ||
		resumed.Merge.Commit != "abcdef0123456789" ||
		resumed.Merge.MergedAt == nil ||
		!resumed.Merge.MergedAt.Equal(mergedAt) {
		t.Fatalf("resumed merge metadata = %#v, want original merged metadata", resumed.Merge)
	}
	if resumed.Status != flowstore.StatusMerged {
		t.Fatalf("resumed flow status = %q, want merged", resumed.Status)
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

func TestStoreAddChildPhaseUpsertsNormalizedPhaseIDVariants(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Variant children",
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
		Order:         10,
	})
	if err != nil {
		t.Fatalf("AddChildPhase() error = %v", err)
	}
	// Re-adding the same logical child with a case or whitespace variant of the
	// phase id must update in place, not create a second row.
	for _, variant := range []string{"Implementation-API", " implementation-api "} {
		record, err = store.AddChildPhase(flowstore.ChildPhaseUpdate{
			FlowID:        record.FlowID,
			ParentPhaseID: "implementation",
			PhaseID:       variant,
			Title:         "API integration updated",
			Order:         10,
		})
		if err != nil {
			t.Fatalf("AddChildPhase(%q) error = %v", variant, err)
		}
	}

	count := 0
	for _, phase := range record.Phases {
		if phase.ParentPhaseID != "" {
			count++
			if phase.PhaseID != "implementation-api" {
				t.Fatalf("stored child phase id not normalized: %q", phase.PhaseID)
			}
		}
	}
	if count != 1 {
		t.Fatalf("variant phase ids duplicated child rows: %#v", record.Phases)
	}
	if child := phaseByID(t, record, "implementation-api"); child.Title != "API integration updated" {
		t.Fatalf("child not updated in place: %#v", child)
	}
}

func TestStoreSetPhaseMatchesNormalizedPhaseIDVariants(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Variant set",
		Instructions: "complete phases",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Completing a phase with a case or whitespace variant of its id must
	// resolve to the existing phase rather than failing or duplicating.
	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: " Plan ", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(\" Plan \") error = %v", err)
	}
	if len(updated.Phases) != len(record.Phases) {
		t.Fatalf("variant phase id changed phase count: %#v", updated.Phases)
	}
	if got := phaseByID(t, updated, "plan").Status; got != flowstore.PhaseCompleted {
		t.Fatalf("plan status = %q, want completed", got)
	}
}

func TestStoreSetPhaseCollapsesExistingDuplicatePhaseRows(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Records written before phase-id normalization may already hold duplicate
	// rows for one logical child phase; completing it must repair the record
	// instead of leaving a second row behind.
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicated-children",
		Title:        "Duplicated children",
		Instructions: "complete child phase",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseReady, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhasePending, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "step-1", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(step-1 completed) error = %v", err)
	}

	count := 0
	for _, phase := range record.Phases {
		if phase.ParentPhaseID == "implementation" {
			count++
			if phase.Status != flowstore.PhaseCompleted {
				t.Fatalf("surviving child not completed: %#v", phase)
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate child rows not collapsed on completion: %#v", record.Phases)
	}
}

func TestStoreAddChildPhaseCollapsesExistingDuplicateRows(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "duplicated-add-child",
		Title:        "Duplicated add-child",
		Instructions: "re-add child phase",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseReady, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhasePending, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "step-1",
		Title:         "Step 1 updated",
		Order:         10,
	})
	if err != nil {
		t.Fatalf("AddChildPhase() error = %v", err)
	}

	count := 0
	for _, phase := range record.Phases {
		if phase.ParentPhaseID == "implementation" {
			count++
			if phase.PhaseID != "step-1" || phase.Title != "Step 1 updated" {
				t.Fatalf("surviving child not updated in place: %#v", phase)
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate child rows not collapsed on add-child: %#v", record.Phases)
	}
}

func TestStoreAddPhaseLaunchIDMatchesNormalizedPhaseIDVariants(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Variant launch",
		Instructions: "launch phases",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: " Plan ", LaunchID: "launch-1"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(\" Plan \") error = %v", err)
	}
	plan := phaseByID(t, updated, "plan")
	if plan.Status != flowstore.PhaseRunning {
		t.Fatalf("plan status = %q, want running", plan.Status)
	}
	if len(plan.LaunchIDs) != 1 || plan.LaunchIDs[0] != "launch-1" {
		t.Fatalf("plan launch ids = %#v", plan.LaunchIDs)
	}
}

func TestStoreAddPhaseLaunchIDResumePrefersExactRowOverEarlierTerminalDuplicate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "exact-row-launch",
		Title:        "Exact row launch",
		Instructions: "resume launch targets active duplicate",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseCompleted, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{
				PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1",
				Status: flowstore.PhaseNeedsAttention, Outcome: "needs_attention", Notes: "PTY startup failed.",
				Kind:      "implementation_child",
				Order:     10,
				LaunchIDs: []string{"launch-1"},
				CreatedAt: now, UpdatedAt: now,
			},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "step-1",
		LaunchID: "launch-2",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(step-1 resume) error = %v", err)
	}

	count := 0
	var survivor flowstore.FlowPhase
	for _, phase := range record.Phases {
		if phase.ParentPhaseID == "implementation" {
			count++
			survivor = phase
		}
	}
	if count != 1 {
		t.Fatalf("duplicate child rows not collapsed on launch: %#v", record.Phases)
	}
	if survivor.PhaseID != "step-1" {
		t.Fatalf("survivor phase id = %q, want step-1", survivor.PhaseID)
	}
	if survivor.Status != flowstore.PhaseRunning || survivor.Outcome != "" {
		t.Fatalf("survivor after resume launch = %#v, want running with cleared outcome", survivor)
	}
	if !strings.Contains(survivor.Notes, "Relaunched after needs_attention") {
		t.Fatalf("survivor notes = %q, want relaunch note", survivor.Notes)
	}
	if len(survivor.LaunchIDs) != 2 || survivor.LaunchIDs[0] != "launch-1" || survivor.LaunchIDs[1] != "launch-2" {
		t.Fatalf("launch ids = %#v, want [launch-1 launch-2]", survivor.LaunchIDs)
	}
}

func TestStoreAddPhaseLaunchIDResumePrefersRawExactRowBeforeNormalizedDuplicate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "raw-exact-row-launch",
		Title:        "Raw exact row launch",
		Instructions: "resume launch targets requested duplicate casing",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseCompleted, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{
				PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1",
				Status: flowstore.PhaseNeedsAttention, Outcome: "needs_attention", Notes: "PTY startup failed.",
				Kind:      "implementation_child",
				Order:     10,
				LaunchIDs: []string{"launch-1"},
				CreatedAt: now, UpdatedAt: now,
			},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "Step-1",
		LaunchID: "launch-2",
		Resume:   true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(Step-1 resume) error = %v", err)
	}

	phase := phaseByID(t, record, "step-1")
	if phase.Status != flowstore.PhaseRunning || phase.Outcome != "" {
		t.Fatalf("phase after resume launch = %#v, want running with cleared outcome", phase)
	}
	if !strings.Contains(phase.Notes, "Relaunched after needs_attention") {
		t.Fatalf("phase notes = %q, want relaunch note", phase.Notes)
	}
	if len(phase.LaunchIDs) != 2 || phase.LaunchIDs[0] != "launch-1" || phase.LaunchIDs[1] != "launch-2" {
		t.Fatalf("launch ids = %#v, want [launch-1 launch-2]", phase.LaunchIDs)
	}
}

func TestStoreAttachSessionMatchesNormalizedPhaseIDVariants(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Variant attach",
		Instructions: "attach sessions",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: " PLAN ",
		Session: flowstore.Session{Provider: "claude", SessionID: "sess-1"},
	})
	if err != nil {
		t.Fatalf("AttachSession(\" PLAN \") error = %v", err)
	}
	plan := phaseByID(t, updated, "plan")
	if len(plan.Sessions) != 1 || plan.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("plan sessions = %#v", plan.Sessions)
	}
}

func TestStoreAttachSessionPrefersExactRowOverEarlierDuplicate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Legacy duplicates can leave a stale row ahead of the active one, e.g. a
	// completed "Step-1" followed by the exact "step-1" row that is actually
	// running. Attaching a session is metadata-only: it must target the exact
	// row and keep its running status instead of collapsing into the stale row.
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "exact-row-attach",
		Title:        "Exact row attach",
		Instructions: "attach session to active duplicate",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseCompleted, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{
				PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1",
				Status: flowstore.PhaseRunning, Kind: "implementation_child", Order: 10,
				LaunchIDs: []string{"launch-1"},
				CreatedAt: now, UpdatedAt: now,
			},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "step-1",
		Session: flowstore.Session{Provider: "codex", SessionID: "sess-9"},
	})
	if err != nil {
		t.Fatalf("AttachSession(step-1) error = %v", err)
	}

	count := 0
	var survivor flowstore.FlowPhase
	for _, phase := range record.Phases {
		if phase.ParentPhaseID == "implementation" {
			count++
			survivor = phase
		}
	}
	if count != 1 {
		t.Fatalf("duplicate child rows not collapsed on attach: %#v", record.Phases)
	}
	if survivor.PhaseID != "step-1" {
		t.Fatalf("survivor phase id = %q, want step-1", survivor.PhaseID)
	}
	if survivor.Status != flowstore.PhaseRunning {
		t.Fatalf("survivor status = %q, want running; metadata-only attach must not change phase status", survivor.Status)
	}
	if len(survivor.LaunchIDs) != 1 || survivor.LaunchIDs[0] != "launch-1" {
		t.Fatalf("launch ids lost in collapse: %#v", survivor.LaunchIDs)
	}
	if len(survivor.Sessions) != 1 || survivor.Sessions[0].SessionID != "sess-9" {
		t.Fatalf("sessions = %#v, want attached sess-9", survivor.Sessions)
	}
}

func TestStoreAttachSessionPrefersRawExactRowBeforeNormalizedDuplicate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "raw-exact-row-attach",
		Title:        "Raw exact row attach",
		Instructions: "attach session to requested duplicate casing",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseCompleted, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{
				PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1",
				Status:    flowstore.PhaseRunning,
				Kind:      "implementation_child",
				Order:     10,
				LaunchIDs: []string{"launch-1"},
				CreatedAt: now, UpdatedAt: now,
			},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "Step-1",
		Session: flowstore.Session{Provider: "codex", SessionID: "sess-9"},
	})
	if err != nil {
		t.Fatalf("AttachSession(Step-1) error = %v", err)
	}

	phase := phaseByID(t, record, "step-1")
	if phase.Status != flowstore.PhaseRunning {
		t.Fatalf("phase status = %q, want running; metadata-only attach must not change phase status", phase.Status)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-1" {
		t.Fatalf("launch ids lost in collapse: %#v", phase.LaunchIDs)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].SessionID != "sess-9" {
		t.Fatalf("sessions = %#v, want attached sess-9", phase.Sessions)
	}
}

func TestStoreCollapseMergesLaunchAndSessionMetadataFromDuplicateRows(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Legacy duplicates often split metadata across rows: one row carries
	// launch/session history while the other carries status. Repair must keep
	// both halves.
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "split-duplicates",
		Title:        "Split duplicates",
		Instructions: "complete child phase",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseReady, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{
				PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1",
				Status: flowstore.PhasePending, Kind: "implementation_child", Order: 10,
				Notes:     "launched from TUI",
				LaunchIDs: []string{"launch-1"},
				Sessions:  []flowstore.Session{{Provider: "claude", SessionID: "sess-1"}},
				CreatedAt: now, UpdatedAt: now,
			},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "step-1", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(step-1 completed) error = %v", err)
	}

	count := 0
	var survivor flowstore.FlowPhase
	for _, phase := range record.Phases {
		if phase.ParentPhaseID == "implementation" {
			count++
			survivor = phase
		}
	}
	if count != 1 {
		t.Fatalf("duplicate child rows not collapsed: %#v", record.Phases)
	}
	if survivor.Status != flowstore.PhaseCompleted {
		t.Fatalf("survivor status = %q, want completed", survivor.Status)
	}
	if len(survivor.LaunchIDs) != 1 || survivor.LaunchIDs[0] != "launch-1" {
		t.Fatalf("launch ids lost in collapse: %#v", survivor.LaunchIDs)
	}
	if len(survivor.Sessions) != 1 || survivor.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions lost in collapse: %#v", survivor.Sessions)
	}
	if survivor.Notes != "launched from TUI" {
		t.Fatalf("notes lost in collapse: %q", survivor.Notes)
	}
}

func TestStoreAddChildPhaseIdempotentRerunStillCollapsesDuplicates(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The first row already matches the incoming update exactly; the rerun must
	// still repair the trailing duplicate row.
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "idempotent-duplicates",
		Title:        "Idempotent duplicates",
		Instructions: "re-add child phase",
		RepoPath:     filepath.Join(root, "repo"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 3, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhaseReady, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "step-1 ", ParentPhaseID: "implementation", Title: "Step 1", Status: flowstore.PhasePending, Kind: "implementation_child", Order: 10, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	created, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	record, err = store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation",
		PhaseID:       "step-1",
		Title:         "Step 1",
		Order:         10,
	})
	if err != nil {
		t.Fatalf("AddChildPhase() error = %v", err)
	}

	count := 0
	for _, phase := range record.Phases {
		if phase.ParentPhaseID == "implementation" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("idempotent rerun left duplicate child rows: %#v", record.Phases)
	}
	if record.UpdatedAt != created.UpdatedAt {
		t.Fatalf("repair-only rerun bumped flow UpdatedAt from %s to %s", created.UpdatedAt, record.UpdatedAt)
	}
	if got := phaseByID(t, record, "step-1").UpdatedAt; got != now {
		t.Fatalf("repair-only rerun bumped child UpdatedAt to %s", got)
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

func TestFlowPhaseDependsOnRoundTrip(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-depends-roundtrip", []flowstore.FlowPhase{
		graphPhase(now, "root", flowstore.PhaseReady, nil),
		graphPhase(now, "publish", flowstore.PhasePending, []string{"root"}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "publish").DependsOn; len(got) != 1 || got[0] != "root" {
		t.Fatalf("publish DependsOn = %#v, want [root]", got)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "root", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(root completed) error = %v", err)
	}
	if got := phaseByID(t, updated, "publish").DependsOn; len(got) != 1 || got[0] != "root" {
		t.Fatalf("updated publish DependsOn = %#v, want [root]", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "flows", record.FlowID, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta.json) error = %v", err)
	}
	if !strings.Contains(string(data), `"depends_on": [
        "root"
      ]`) {
		t.Fatalf("meta.json did not persist depends_on edge:\n%s", data)
	}
}

func TestDependsOnNullCountsAsPresentEmpty(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-null-depends"
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Null depends",
  "instructions": "explicit all roots",
  "status": "pending",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "phases": [
    {"phase_id": "alpha", "title": "Alpha", "kind": "", "depends_on": null, "status": "ready", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "beta", "title": "Beta", "kind": "", "depends_on": [], "status": "ready", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "alpha").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("alpha DependsOn = %#v, want explicit empty slice", got)
	}
	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: "alpha", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(alpha completed) error = %v", err)
	}
	if got := phaseByID(t, updated, "beta").Status; got != flowstore.PhaseReady {
		t.Fatalf("beta status = %q, want ready as independent root", got)
	}
	data, err := os.ReadFile(filepath.Join(flowDir, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta.json) error = %v", err)
	}
	if strings.Contains(string(data), `"depends_on": null`) {
		t.Fatalf("meta.json kept null depends_on:\n%s", data)
	}
}

func TestReadBackfillsLinearDependsOn(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flowID := "20260607T120000Z-legacy-default"
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Legacy default",
  "instructions": "backfill old records",
  "status": "pending",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "phases": [
    {"phase_id": "plan", "title": "Plan", "kind": "plan", "status": "ready", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "plan-review", "title": "Plan Review", "kind": "plan_review", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "implementation", "title": "Implementation", "kind": "implementation", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "plan-review").DependsOn; len(got) != 1 || got[0] != "plan" {
		t.Fatalf("plan-review DependsOn = %#v, want [plan]", got)
	}
	if got := phaseByID(t, read, "implementation").DependsOn; len(got) != 1 || got[0] != "plan-review" {
		t.Fatalf("implementation DependsOn = %#v, want [plan-review]", got)
	}
}

func TestReadinessFanOut(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-fanout", []flowstore.FlowPhase{
		graphPhase(now, "root", flowstore.PhaseReady, []string{}),
		graphPhase(now, "a", flowstore.PhasePending, []string{"root"}),
		graphPhase(now, "b", flowstore.PhasePending, []string{"root"}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "root", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(root completed) error = %v", err)
	}
	if got := phaseByID(t, updated, "a").Status; got != flowstore.PhaseReady {
		t.Fatalf("a status = %q, want ready", got)
	}
	if got := phaseByID(t, updated, "b").Status; got != flowstore.PhaseReady {
		t.Fatalf("b status = %q, want ready", got)
	}
}

func TestReadinessFanIn(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-fanin", []flowstore.FlowPhase{
		graphPhase(now, "a", flowstore.PhaseReady, []string{}),
		graphPhase(now, "b", flowstore.PhaseReady, []string{}),
		graphPhase(now, "join", flowstore.PhasePending, []string{"a", "b"}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "a", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(a completed) error = %v", err)
	}
	if got := phaseByID(t, updated, "b").Status; got != flowstore.PhaseReady {
		t.Fatalf("b status = %q, want ready independent root", got)
	}
	if got := phaseByID(t, updated, "join").Status; got != flowstore.PhasePending {
		t.Fatalf("join status = %q, want pending until both branches complete", got)
	}
	updated, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "b", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(b completed) error = %v", err)
	}
	if got := phaseByID(t, updated, "join").Status; got != flowstore.PhaseReady {
		t.Fatalf("join status = %q, want ready", got)
	}
}

func TestReadinessIndependentBranchRegression(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-branch-regression", []flowstore.FlowPhase{
		graphPhase(now, "root", flowstore.PhaseCompleted, []string{}),
		graphPhase(now, "a", flowstore.PhaseCompleted, []string{"root"}),
		graphPhase(now, "b", flowstore.PhaseRunning, []string{"root"}),
		graphPhase(now, "join", flowstore.PhaseReady, []string{"a", "b"}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "a", Status: flowstore.PhaseRunning})
	if err != nil {
		t.Fatalf("SetPhase(a running) error = %v", err)
	}
	if got := phaseByID(t, updated, "b").Status; got != flowstore.PhaseRunning {
		t.Fatalf("b status = %q, want running branch preserved", got)
	}
	if got := phaseByID(t, updated, "join").Status; got != flowstore.PhasePending {
		t.Fatalf("join status = %q, want pending after dependency regression", got)
	}
}

func TestCreateSeedsLinearDependsOn(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Default edges",
		Instructions: "seed graph edges",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := phaseByID(t, record, "plan").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("plan DependsOn = %#v, want explicit empty root", got)
	}
	if got := phaseByID(t, record, "plan-review").DependsOn; len(got) != 1 || got[0] != "plan" {
		t.Fatalf("plan-review DependsOn = %#v, want [plan]", got)
	}
	if got := phaseByID(t, record, "plan").Status; got != flowstore.PhaseReady {
		t.Fatalf("plan status = %q, want ready", got)
	}
}

func TestCreateRejectsCyclicPhases(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	_, err = store.Create(flowstore.FlowRecord{
		Title:        "Cycle",
		Instructions: "reject cycles",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			graphPhase(now, "a", flowstore.PhasePending, []string{"b"}),
			graphPhase(now, "b", flowstore.PhasePending, []string{"a"}),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "phase graph contains a cycle") {
		t.Fatalf("Create() error = %v, want cycle", err)
	}
}

func TestPhasePredecessorsSatisfiedDAG(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record := flowstore.FlowRecord{Phases: []flowstore.FlowPhase{
		graphPhase(now, "a", flowstore.PhaseCompleted, []string{}),
		graphPhase(now, "b", flowstore.PhaseCompleted, []string{}),
		graphPhase(now, "join", flowstore.PhasePending, []string{"a", "b"}),
	}}
	if !flowstore.PhasePredecessorsSatisfied(record, "join") {
		t.Fatalf("PhasePredecessorsSatisfied(join) = false, want true")
	}
	record.Phases[1].Status = flowstore.PhaseRunning
	if flowstore.PhasePredecessorsSatisfied(record, "join") {
		t.Fatalf("PhasePredecessorsSatisfied(join) = true, want false with running dependency")
	}
	if flowstore.PhasePredecessorsSatisfied(record, "missing") {
		t.Fatalf("PhasePredecessorsSatisfied(missing) = true, want false")
	}
}

func TestDuplicateReadyRowNotLaunchEligible(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record := flowstore.FlowRecord{AutoMode: true, Phases: []flowstore.FlowPhase{
		graphPhase(now, "step-1", flowstore.PhaseCompleted, []string{}),
		graphPhase(now, "step-1 ", flowstore.PhaseReady, []string{}),
		graphPhase(now, "step-2", flowstore.PhaseReady, []string{"step-1"}),
	}}
	if flowstore.PhaseLaunchEligible(record, 1) {
		t.Fatalf("duplicate ready row reported launch eligible")
	}
	if !flowstore.PhaseLaunchEligible(record, 2) {
		t.Fatalf("non-duplicate ready row reported ineligible")
	}
}

func graphRecord(root, flowID string, phases []flowstore.FlowPhase) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		SchemaVersion: 1,
		FlowID:        flowID,
		Title:         "Graph fixture",
		Instructions:  "exercise phase graph",
		Status:        flowstore.StatusPending,
		RepoPath:      filepath.Join(root, "repo"),
		Merge:         flowstore.Merge{Status: flowstore.MergePending},
		AutoMode:      true,
		Phases:        phases,
		CreatedAt:     time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
	}
}

func graphPhase(now time.Time, phaseID, status string, dependsOn []string) flowstore.FlowPhase {
	return flowstore.FlowPhase{
		PhaseID:   phaseID,
		Title:     phaseID,
		Kind:      "",
		DependsOn: dependsOn,
		Status:    status,
		Order:     1,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func writeFreshFlowRecordForTest(t *testing.T, root string, record flowstore.FlowRecord) {
	t.Helper()
	flowDir := filepath.Join(root, "flows", record.FlowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeFlowRecordForTest(root, record); err != nil {
		t.Fatalf("write test flow: %v", err)
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

func planPhaseByID(t *testing.T, record planstore.PlanRecord, phaseID string) planstore.PlanPhase {
	t.Helper()
	if phase, ok := maybePlanPhaseByID(record, phaseID); ok {
		return phase
	}
	t.Fatalf("plan phase %q not found in %#v", phaseID, record.Phases)
	return planstore.PlanPhase{}
}

func maybePlanPhaseByID(record planstore.PlanRecord, phaseID string) (planstore.PlanPhase, bool) {
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase, true
		}
	}
	return planstore.PlanPhase{}, false
}

func mustCreateFlow(t *testing.T, store *flowstore.Store, title string) flowstore.FlowRecord {
	t.Helper()
	record, err := store.Create(flowstore.FlowRecord{
		Title:        title,
		Instructions: "test flow",
		RepoPath:     filepath.Join(t.TempDir(), "repo"),
	})
	if err != nil {
		t.Fatalf("Create(%q) error = %v", title, err)
	}
	return record
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

func mustCreateManualMergeFlow(t *testing.T, store *flowstore.Store, root string, withPR, completeAutoreview bool) flowstore.FlowRecord {
	t.Helper()
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Manual merge fixture",
		Instructions: "mark manually merged",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/manual-merge",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review", "implementation", "review-loop", "pr-creation")
	if withPR {
		record, err = store.SetPR(flowstore.PRUpdate{
			FlowID:     record.FlowID,
			Provider:   "github",
			Number:     116,
			URL:        "https://github.com/brian-bell/wtui/pull/116",
			HeadBranch: "flow/manual-merge",
			BaseBranch: "main",
			Status:     "open",
		})
		if err != nil {
			t.Fatalf("SetPR() error = %v", err)
		}
	}
	if completeAutoreview && withPR {
		record, err = store.SetPhase(flowstore.PhaseUpdate{
			FlowID:  record.FlowID,
			PhaseID: "autoreview",
			Status:  flowstore.PhaseCompleted,
			Outcome: "passed",
		})
		if err != nil {
			t.Fatalf("SetPhase(autoreview completed) error = %v", err)
		}
	} else if completeAutoreview {
		for i := range record.Phases {
			if record.Phases[i].PhaseID == "autoreview" {
				record.Phases[i].Status = flowstore.PhaseCompleted
				record.Phases[i].Outcome = "passed"
			}
			if record.Phases[i].PhaseID == "merge" {
				record.Phases[i].Status = flowstore.PhaseReady
			}
		}
		if err := writeFlowRecordForTest(root, record); err != nil {
			t.Fatalf("write test flow: %v", err)
		}
	}
	return record
}

func writeFlowRecordForTest(root string, record flowstore.FlowRecord) error {
	metaPath := filepath.Join(root, "flows", record.FlowID, "meta.json")
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, append(data, '\n'), 0o600)
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

func flowLockPath(root, flowID string) string {
	return filepath.Join(root, "flows", ".locks", flowID+".lock")
}
