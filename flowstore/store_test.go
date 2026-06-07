package flowstore_test

import (
	"os"
	"path/filepath"
	"strings"
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
