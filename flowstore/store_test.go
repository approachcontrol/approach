package flowstore_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/planstore"
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

	metaJSON := readSQLiteRecordForTest(t, root, record.FlowID)
	if strings.Contains(string(metaJSON), "0001-01-01") || strings.Contains(string(metaJSON), "merged_at") {
		t.Fatalf("pending flow storage should not serialize a zero merge timestamp:\n%s", metaJSON)
	}
	databasePath := filepath.Join(root, "approach.db")
	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatalf("stat approach.db: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("approach.db mode = %o, want 0600", info.Mode().Perm())
	}
	assertMode(t, root, 0o700)
	if _, err := os.Stat(filepath.Join(root, "flows")); !os.IsNotExist(err) {
		t.Fatalf("fresh SQLite store unexpectedly created flows/: %v", err)
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

func TestStoreAllocateIDIsNonDurableAndCreateWithOptionsArbitratesExactID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 14, 12, 34, 56, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, title := range []string{"", "  \t\n"} {
		if id, err := store.AllocateID(title); err == nil || id != "" || !strings.Contains(err.Error(), "flow title is required") {
			t.Fatalf("AllocateID(%q) = %q, %v, want empty ID and title error", title, id, err)
		}
	}

	first, err := store.AllocateID("Lifecycle Flow")
	if err != nil {
		t.Fatalf("AllocateID() error = %v", err)
	}
	second, err := store.AllocateID("Lifecycle Flow")
	if err != nil {
		t.Fatalf("second AllocateID() error = %v", err)
	}
	if first != second {
		t.Fatalf("pre-write allocations = %q/%q, want the same non-durable candidate", first, second)
	}
	listed, err := store.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("AllocateID created records: %#v", listed)
	}

	created, err := store.CreateWithOptions(flowstore.FlowRecord{
		FlowID: first, Title: "Lifecycle Flow", Instructions: "Exercise exact identity.", RepoPath: filepath.Join(root, "repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreateWithOptions(exact ID) error = %v", err)
	}
	if created.FlowID != first {
		t.Fatalf("CreateWithOptions().FlowID = %q, want allocated %q", created.FlowID, first)
	}
	if _, err := store.CreateWithOptions(flowstore.FlowRecord{
		FlowID: first, Title: "Collision", Instructions: "Must lose.", RepoPath: filepath.Join(root, "repo"),
	}, flowstore.CreateOptions{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("colliding CreateWithOptions() error = %v, want already exists", err)
	}

	next, err := store.AllocateID("Lifecycle Flow")
	if err != nil {
		t.Fatalf("AllocateID(after create) error = %v", err)
	}
	if next == first {
		t.Fatalf("AllocateID(after persisted collision) = %q, want a suffixed candidate", next)
	}
}

func TestStoreCreateHeadlessPreferenceDefaultsOnAndPreservesExplicitValues(t *testing.T) {
	tests := []struct {
		name     string
		headless *bool
		want     bool
	}{
		{name: "omitted defaults on", want: true},
		{name: "explicit on", headless: testBoolPtr(true), want: true},
		{name: "explicit off", headless: testBoolPtr(false), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			record, err := store.CreateWithOptions(flowstore.FlowRecord{
				Title:        "Headless Preference",
				Instructions: "Persist the launch preference.",
				RepoPath:     filepath.Join(root, "repo"),
			}, flowstore.CreateOptions{Headless: tt.headless})
			if err != nil {
				t.Fatalf("CreateWithOptions() error = %v", err)
			}
			if record.Headless != tt.want {
				t.Fatalf("CreateWithOptions().Headless = %v, want %v", record.Headless, tt.want)
			}
			read, err := store.Read(record.FlowID)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			listed, err := store.List(flowstore.FlowFilter{RepoPath: record.RepoPath})
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if read.Headless != tt.want || len(listed) != 1 || listed[0].Headless != tt.want {
				t.Fatalf("headless preference did not round-trip: read=%#v list=%#v", read, listed)
			}
			data := readSQLiteRecordForTest(t, root, record.FlowID)
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("Unmarshal(SQLite record) error = %v", err)
			}
			if _, ok := raw["headless"]; !ok {
				t.Fatalf("SQLite record omitted headless:\n%s", data)
			}
		})
	}
}

func TestStoreSetHeadlessUpdatesOnlyTargetAndNoOpPreservesTimestamp(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	create := func(title string) flowstore.FlowRecord {
		record, err := store.Create(flowstore.FlowRecord{Title: title, Instructions: "Test isolation.", RepoPath: filepath.Join(root, "repo")})
		if err != nil {
			t.Fatalf("Create(%q) error = %v", title, err)
		}
		return record
	}
	first := create("First Flow")
	second := create("Second Flow")

	now = now.Add(time.Minute)
	updated, err := store.SetHeadless(flowstore.HeadlessUpdate{FlowID: first.FlowID, Enabled: false})
	if err != nil {
		t.Fatalf("SetHeadless(false) error = %v", err)
	}
	if updated.Headless || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("SetHeadless(false) = %#v, want disabled with updated timestamp", updated)
	}
	unchanged, err := store.SetHeadless(flowstore.HeadlessUpdate{FlowID: first.FlowID, Enabled: false})
	if err != nil {
		t.Fatalf("SetHeadless(false) no-op error = %v", err)
	}
	if !unchanged.UpdatedAt.Equal(updated.UpdatedAt) {
		t.Fatalf("no-op UpdatedAt = %s, want %s", unchanged.UpdatedAt, updated.UpdatedAt)
	}
	other, err := store.Read(second.FlowID)
	if err != nil {
		t.Fatalf("Read(second) error = %v", err)
	}
	if !other.Headless {
		t.Fatalf("second Flow Headless = false after first update: %#v", other)
	}
}

func TestStoreReadLegacyRecordWithoutHeadlessDefaultsOnWithoutRewriting(t *testing.T) {
	root := t.TempDir()
	flowID := "20260811T163138Z-legacy-headless"
	writeRawFlowMeta(t, root, flowID, `
  "phases": [
    {"phase_id": "plan", "title": "Plan", "kind": "plan", "status": "ready", "order": 1,
     "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	legacyPath := filepath.Join(root, "flows", flowID, "meta.json")
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(legacy meta.json) error = %v", err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !read.Headless {
		t.Fatalf("legacy Read().Headless = false: %#v", read)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(meta.json) after read error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("retained legacy source changed during migration/read\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestStoreLegacyImportIgnoresPreparationReceiptFields(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "valid timestamp", value: `"2026-01-01T00:00:00Z"`},
		{name: "malformed timestamp", value: `"not-a-timestamp"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			flowID := "20260814T120000Z-legacy-receipt"
			writeRawFlowMeta(t, root, flowID, `
  "prepared_at": `+tt.value+`,
  "phases": [
    {"phase_id": "plan", "title": "Plan", "kind": "plan", "status": "ready", "order": 1,
     "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)

			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() legacy migration error = %v", err)
			}
			record, err := store.Read(flowID)
			if err != nil {
				t.Fatalf("Read() migrated legacy Flow error = %v", err)
			}
			if record.PreparedAt != nil {
				t.Fatalf("migrated legacy PreparedAt = %v, want nil", record.PreparedAt)
			}
		})
	}
}

func testBoolPtr(value bool) *bool {
	return &value
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
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
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
		wantStatus := record.Phases[i].Status
		if read.Phases[i].PhaseID != record.Phases[i].PhaseID ||
			read.Phases[i].Status != wantStatus ||
			strings.Join(read.Phases[i].LaunchIDs, ",") != strings.Join(record.Phases[i].LaunchIDs, ",") ||
			len(read.Phases[i].Sessions) != len(record.Phases[i].Sessions) {
			t.Fatalf("phase %d changed after rejected reset: before=%#v after=%#v", i, record.Phases[i], read.Phases[i])
		}
	}
}

func TestStoreRecoverReconciledPhaseRemovesOnlyExpectedLaunch(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	record := mustCreateFlow(t, store, "Recover reconciled phase")
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "plan", LaunchID: "launch-old"})
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{FlowID: record.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"}})
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "plan", LaunchID: "launch-stale", Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{FlowID: record.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"}})
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconcile", Summary: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	observed := phaseByID(t, record, "plan")

	record, err = store.RecoverReconciledPhase(flowstore.PhaseRecoveryUpdate{
		FlowID: record.FlowID, PhaseID: "plan", ExpectedStatus: observed.Status,
		ExpectedOutcome: observed.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: observed.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("RecoverReconciledPhase() error = %v", err)
	}
	phase := phaseByID(t, record, "plan")
	if phase.Status != flowstore.PhaseReady || phase.Outcome != "" || phase.Notes != "" || phase.Summary != "" {
		t.Fatalf("recovered phase = %#v", phase)
	}
	if len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "launch-old" {
		t.Fatalf("launch ids = %#v, want only launch-old", phase.LaunchIDs)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].SessionID != "session-old" {
		t.Fatalf("sessions = %#v, want only session-old", phase.Sessions)
	}
}

func TestStoreRecoverReconciledPhaseRefusesDriftAndUnsafeRecordsWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate)
	}{
		{name: "changed status", prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate) {
			phase := phaseByID(t, record, "plan")
			return record, flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: "plan", ExpectedStatus: flowstore.PhaseBlocked, ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}
		}},
		{name: "changed outcome", prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate) {
			phase := phaseByID(t, record, "plan")
			return record, flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: "plan", ExpectedStatus: phase.Status, ExpectedOutcome: flowstore.OutcomePhaseResultStale, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}
		}},
		{name: "replaced launch", prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate) {
			phase := phaseByID(t, record, "plan")
			return record, flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: "plan", ExpectedStatus: phase.Status, ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-other", ExpectedUpdatedAt: phase.UpdatedAt}
		}},
		{name: "changed timestamp", prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate) {
			phase := phaseByID(t, record, "plan")
			return record, flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: "plan", ExpectedStatus: phase.Status, ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt.Add(-time.Second)}
		}},
		{name: "live session", prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate) {
			read, err := store.AttachSession(flowstore.SessionAttachUpdate{FlowID: record.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "claude", SessionID: "session-live", LaunchID: "launch-stale", Status: "running"}})
			if err != nil {
				t.Fatal(err)
			}
			phase := phaseByID(t, read, "plan")
			return read, flowstore.PhaseRecoveryUpdate{FlowID: read.FlowID, PhaseID: "plan", ExpectedStatus: phase.Status, ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}
		}},
		{name: "closed flow", prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) (flowstore.FlowRecord, flowstore.PhaseRecoveryUpdate) {
			phase := phaseByID(t, record, "plan")
			update := flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: "plan", ExpectedStatus: phase.Status, ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}
			closed, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed for test"})
			if err != nil {
				t.Fatal(err)
			}
			return closed, update
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			record := mustCreateFlow(t, store, "Reject recovery "+tc.name)
			record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "plan", LaunchID: "launch-stale"})
			if err != nil {
				t.Fatal(err)
			}
			record, err = store.AttachSession(flowstore.SessionAttachUpdate{FlowID: record.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"}})
			if err != nil {
				t.Fatal(err)
			}
			record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconciled"})
			if err != nil {
				t.Fatal(err)
			}
			before, update := tc.prepare(t, store, record)
			beforeJSON, _ := json.Marshal(before)
			if _, err := store.RecoverReconciledPhase(update); err == nil {
				t.Fatal("RecoverReconciledPhase() succeeded")
			}
			after, err := store.Read(record.FlowID)
			if err != nil {
				t.Fatal(err)
			}
			afterJSON, _ := json.Marshal(after)
			if string(afterJSON) != string(beforeJSON) {
				t.Fatalf("record changed after refusal\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
			}
		})
	}
}

func TestStoreRecoverReconciledPhaseRefusesUnsupportedAndAmbiguousRecords(t *testing.T) {
	tests := []struct {
		name   string
		phases []flowstore.FlowPhase
	}{
		{
			name:   "plan-review blocked form",
			phases: []flowstore.FlowPhase{{PhaseID: "plan-review", Title: "Plan review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseBlocked, Outcome: flowstore.OutcomeBlocked, LaunchIDs: []string{"launch-stale"}, Sessions: []flowstore.Session{{Provider: "codex", SessionID: "s", LaunchID: "launch-stale", Status: "ended"}}}},
		},
		{
			name: "duplicate phase rows",
			phases: []flowstore.FlowPhase{
				{PhaseID: "Plan", Title: "Plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, LaunchIDs: []string{"launch-stale"}},
				{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, LaunchIDs: []string{"launch-stale"}},
			},
		},
		{
			name:   "session launch mismatch",
			phases: []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, LaunchIDs: []string{"launch-stale"}, Sessions: []flowstore.Session{{Provider: "codex", SessionID: "s", LaunchID: "launch-other", Status: "ended"}}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			record, err := store.Create(flowstore.FlowRecord{FlowID: "recover-refusal", Title: tc.name, Instructions: tc.name, RepoPath: filepath.Join(root, "repo"), Phases: tc.phases})
			if err != nil {
				t.Fatal(err)
			}
			phase := phaseByID(t, record, tc.phases[len(tc.phases)-1].PhaseID)
			before, _ := json.Marshal(record)
			_, err = store.RecoverReconciledPhase(flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: phase.PhaseID, ExpectedStatus: phase.Status, ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt})
			if err == nil {
				t.Fatal("RecoverReconciledPhase() succeeded")
			}
			after, readErr := store.Read(record.FlowID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			afterJSON, _ := json.Marshal(after)
			if string(afterJSON) != string(before) {
				t.Fatalf("record changed after refusal\nbefore: %s\nafter:  %s", before, afterJSON)
			}
		})
	}
}

func TestStoreRecoverReconciledPhaseRefusesUnsatisfiedPredecessorWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID: "recover-dependency", Title: "Recover dependency", Instructions: "refuse drift", RepoPath: filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "target", Title: "Target", DependsOn: []string{"alpha"}, Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Order: 2, LaunchIDs: []string{"launch-stale"}, Sessions: []flowstore.Session{{Provider: "codex", SessionID: "s", LaunchID: "launch-stale", Status: "ended"}}, UpdatedAt: time.Now().UTC()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := phaseByID(t, record, "target")
	for i := range record.Phases {
		if record.Phases[i].PhaseID == "alpha" {
			record.Phases[i].Status = flowstore.PhaseRunning
		}
	}
	data, _ := json.Marshal(record)
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", data, record.FlowID); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	_, err = store.RecoverReconciledPhase(flowstore.PhaseRecoveryUpdate{FlowID: record.FlowID, PhaseID: "target", ExpectedStatus: target.Status, ExpectedOutcome: target.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: target.UpdatedAt})
	if err == nil || !strings.Contains(err.Error(), "requires satisfied predecessors") {
		t.Fatalf("RecoverReconciledPhase() error = %v", err)
	}
	after, _ := store.Read(record.FlowID)
	afterJSON, _ := json.Marshal(after)
	if string(afterJSON) != string(beforeJSON) {
		t.Fatalf("record changed after refusal\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
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
		wantStatus := record.Phases[i].Status
		if read.Phases[i].PhaseID != record.Phases[i].PhaseID ||
			read.Phases[i].Status != wantStatus ||
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

// The mirror half of the same rule the sessions store follows: every reader
// that decides whether a session still blocks its phase trims, so the write
// that ends it has to match trimmed too.
func TestStoreMarkPhaseLaunchEndedMatchesPaddedLaunchIDs(t *testing.T) {
	root := t.TempDir()
	endedAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateFlow(t, store, "Finalize a padded launch ID")
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-1"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{Provider: "codex", SessionID: "session-1", LaunchID: " launch-1 ", Status: "last_seen"},
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
	if len(phase.Sessions) != 1 || phase.Sessions[0].Status != "ended" {
		t.Fatalf("session = %#v, want the padded launch ended", phase.Sessions)
	}
}

func TestStoreMarkPhaseLaunchEndedAdvancesResumedSessionEndTime(t *testing.T) {
	root := t.TempDir()
	previousEnd := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	resumedEnd := previousEnd.Add(time.Hour)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateFlow(t, store, "Finalize resumed Flow session")
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-2"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{
			Provider:  "codex",
			SessionID: "session-1",
			LaunchID:  "launch-2",
			Status:    "last_seen",
			EndedAt:   previousEnd,
		},
	})
	if err != nil {
		t.Fatalf("AttachSession() error = %v", err)
	}

	record, err = store.MarkPhaseLaunchEnded(flowstore.PhaseLaunchEndUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "implementation",
		LaunchID: "launch-2",
		EndedAt:  resumedEnd,
	})
	if err != nil {
		t.Fatalf("MarkPhaseLaunchEnded() error = %v", err)
	}

	phase := phaseByID(t, record, "implementation")
	if len(phase.Sessions) != 1 || phase.Sessions[0].Status != "ended" || !phase.Sessions[0].EndedAt.Equal(resumedEnd) {
		t.Fatalf("resumed session = %#v, want ended at %s", phase.Sessions, resumedEnd)
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
		URL:        "https://github.com/approachcontrol/approach/pull/115",
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
		URL:        "https://github.com/approachcontrol/approach/pull/115",
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
		URL:        "https://github.com/approachcontrol/approach/pull/115",
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
		URL:        "https://github.com/approachcontrol/approach/pull/115",
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

func TestStoreAddPhaseLaunchIDLaunchesReadyRootOfCustomGraph(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// A custom depends_on DAG without plan-review is created with pending roots;
	// launching the root must derive readiness first so the transition to running
	// is not rejected, otherwise the flow can never launch any phase.
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-launch",
		Title:        "Custom launch",
		Instructions: "launch the root of a custom DAG without plan-review",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "alpha", Title: "Alpha", Status: flowstore.PhasePending, Order: 1},
			{PhaseID: "beta", Title: "Beta", Status: flowstore.PhasePending, Order: 2, DependsOn: []string{"alpha"}},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	launched, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "alpha",
		LaunchID: "launch-alpha-1",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(alpha) error = %v", err)
	}
	if got := phaseByID(t, launched, "alpha").Status; got != flowstore.PhaseRunning {
		t.Fatalf("alpha status after launch = %q, want running", got)
	}
	if got := phaseByID(t, launched, "beta").Status; got != flowstore.PhasePending {
		t.Fatalf("beta status after launch = %q, want pending behind running alpha", got)
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
		status           flowstore.PhaseStatus
		notes            string
		wantFlowStatus   string
		wantReviewStatus flowstore.PhaseStatus
		wantImplStatus   flowstore.PhaseStatus
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

func TestStoreReadDoesNotHealCanonicalPlanReviewApproval(t *testing.T) {
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
	if err := writeFlowRecordForTest(root, record); err != nil {
		t.Fatalf("write legacy-shaped SQLite record: %v", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	review := phaseByID(t, read, "plan-review")
	if review.Status != flowstore.PhaseCompleted || review.Outcome != "" {
		t.Fatalf("plan-review = %#v, want canonical row returned without runtime healing", review)
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
		status  flowstore.PhaseStatus
		outcome string
		notes   string
	}{
		{status: flowstore.PhaseRunning},
		{status: flowstore.PhaseNeedsAttention, notes: "Implementation needs review."},
		{status: flowstore.PhaseCompleted, outcome: "implemented"},
		{status: flowstore.PhaseBlocked, notes: "Implementation is blocked."},
		{status: flowstore.PhaseSkipped, notes: "Implementation was covered elsewhere."},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
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
	contender, err := sql.Open("sqlite", "file:"+filepath.Join(root, "approach.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open contending database handle: %v", err)
	}
	defer contender.Close()
	tx, err := contender.Begin()
	if err != nil {
		t.Fatalf("begin contending immediate transaction: %v", err)
	}
	defer tx.Rollback()

	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseRunning,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("SetPhase() error = %v, want SQLite locked timeout", err)
	}
}

func TestStoreSetPhaseIgnoresLegacyFlowLockArtifacts(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Dir(oldRecordLockPath), 0o700); err != nil {
		t.Fatalf("create legacy flow directory: %v", err)
	}
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
	if _, err := os.Stat(flowLockPath(root, record.FlowID)); !os.IsNotExist(err) {
		t.Fatalf("SQLite update unexpectedly created a Flow flock artifact: %v", err)
	}
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
	if _, err := os.Stat(filepath.Join(root, "approach.db")); err != nil {
		t.Fatalf("Delete() removed the shared Flow database: %v", err)
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
					URL:        "https://github.com/approachcontrol/approach/pull/12",
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
			{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, DependsOn: []string{}, Status: flowstore.PhaseRunning, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, DependsOn: []string{}, Status: flowstore.PhaseRunning, Order: 2, CreatedAt: now, UpdatedAt: now},
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
		URL:        "https://github.com/approachcontrol/approach/pull/115",
		HeadBranch: "flow/pr-metadata",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}

	if updated.PR.Provider != "github" ||
		updated.PR.Number != 115 ||
		updated.PR.URL != "https://github.com/approachcontrol/approach/pull/115" ||
		updated.PR.HeadBranch != "flow/pr-metadata" ||
		updated.PR.BaseBranch != "main" ||
		updated.PR.Status != "open" {
		t.Fatalf("PR metadata = %#v", updated.PR)
	}
	if got := phaseByID(t, updated, "autoreview").Status; got != flowstore.PhaseReady {
		t.Fatalf("autoreview status after PR metadata = %q, want ready", got)
	}
}

func TestStoreSetPRPreservesCanonicalPlanReviewApprovalBeforeRefresh(t *testing.T) {
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
		Title:        "Legacy PR metadata",
		Instructions: "record the pull request for a legacy review outcome",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/legacy-pr-metadata",
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
	for i := range record.Phases {
		if record.Phases[i].PhaseID == "plan-review" {
			record.Phases[i].Outcome = flowstore.OutcomeApproved
		}
	}
	if err := writeFlowRecordForTest(root, record); err != nil {
		t.Fatalf("write legacy-shaped SQLite record: %v", err)
	}

	updated, err := store.SetPR(flowstore.PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     116,
		URL:        "https://github.com/approachcontrol/approach/pull/116",
		HeadBranch: "flow/legacy-pr-metadata",
		BaseBranch: "main",
		Status:     "open",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}

	if got := phaseByID(t, updated, "plan-review").Outcome; got != flowstore.OutcomeApproved {
		t.Fatalf("plan-review outcome after SetPR = %q, want canonical approval preserved", got)
	}
	if got := phaseByID(t, updated, "implementation").Status; got != flowstore.PhaseCompleted {
		t.Fatalf("implementation status after SetPR = %q, want completed", got)
	}
	if got := phaseByID(t, updated, "autoreview").Status; got != flowstore.PhaseReady {
		t.Fatalf("autoreview status after SetPR = %q, want ready", got)
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
		URL:      "https://github.com/approachcontrol/approach/issues/123",
	})
	if err != nil {
		t.Fatalf("SetIssue() error = %v", err)
	}

	if updated.Issue.Provider != "github" ||
		updated.Issue.Number != 123 ||
		updated.Issue.URL != "https://github.com/approachcontrol/approach/issues/123" {
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

func TestStoreSetIssueDoesNotRefreshCustomFlowReadiness(t *testing.T) {
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
		Instructions: "record the issue without touching phase state",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{
				PhaseID:   "discover",
				Title:     "Discover",
				Kind:      flowstore.KindPlan,
				Status:    flowstore.PhaseRunning,
				Order:     1,
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				PhaseID:   "build",
				Title:     "Build",
				Kind:      flowstore.KindImplementation,
				DependsOn: []string{"discover"},
				Status:    flowstore.PhaseRunning,
				Order:     2,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
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
		URL:      "https://github.com/approachcontrol/approach/issues/123",
	})
	if err != nil {
		t.Fatalf("SetIssue() error = %v", err)
	}

	if got := phaseByID(t, updated, "build").Status; got != flowstore.PhaseRunning {
		t.Fatalf("build status after SetIssue = %q, want running", got)
	}
	metaJSON := readSQLiteRecordForTest(t, root, record.FlowID)
	var persisted flowstore.FlowRecord
	if err := json.Unmarshal(metaJSON, &persisted); err != nil {
		t.Fatalf("decode meta.json: %v", err)
	}
	if got := phaseByID(t, persisted, "build").Status; got != flowstore.PhaseRunning {
		t.Fatalf("persisted build status after SetIssue = %q, want running", got)
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
		URL:      "https://github.com/approachcontrol/approach/issues/123",
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
		URL:      "https://github.com/approachcontrol/approach/issues/123",
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
		{name: "url", issue: flowstore.Issue{Provider: "github", Number: 123, URL: "https://github.com/approachcontrol/approach/pull/123"}},
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
			update: flowstore.IssueUpdate{Provider: "gitlab", Number: 1, URL: "https://github.com/approachcontrol/approach/issues/1"},
			want:   "unsupported issue provider",
		},
		{
			name:   "number",
			update: flowstore.IssueUpdate{Provider: "github", Number: 0, URL: "https://github.com/approachcontrol/approach/issues/1"},
			want:   "issue number must be positive",
		},
		{
			name:   "url",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "not-a-url"},
			want:   "issue URL must be an absolute http(s) URL",
		},
		{
			name:   "url host",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "https://example.com/approachcontrol/approach/issues/1"},
			want:   "GitHub issue URL must use github.com",
		},
		{
			name:   "url number",
			update: flowstore.IssueUpdate{Provider: "github", Number: 2, URL: "https://github.com/approachcontrol/approach/issues/1"},
			want:   "GitHub issue URL number",
		},
		{
			name:   "url pull path",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "https://github.com/approachcontrol/approach/pull/1"},
			want:   "GitHub issue URL must have /owner/repo/issues/number path",
		},
		{
			name:   "url extra path",
			update: flowstore.IssueUpdate{Provider: "github", Number: 1, URL: "https://github.com/approachcontrol/approach/issues/1/comments"},
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
		URL:        "https://github.com/approachcontrol/approach/pull/115",
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
		{name: "url", pr: flowstore.PullRequest{Provider: "github", Number: 115, URL: "https://github.com/approachcontrol/approach/issues/115", HeadBranch: valid.HeadBranch, BaseBranch: valid.BaseBranch}},
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
			update: flowstore.PRUpdate{Provider: "gitlab", Number: 1, URL: "https://github.com/approachcontrol/approach/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "unsupported PR provider",
		},
		{
			name:   "number",
			update: flowstore.PRUpdate{Provider: "github", Number: 0, URL: "https://github.com/approachcontrol/approach/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "PR number must be positive",
		},
		{
			name:   "url",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "not-a-url", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "PR URL must be an absolute http(s) URL",
		},
		{
			name:   "url host",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://example.com/approachcontrol/approach/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "GitHub PR URL must use github.com",
		},
		{
			name:   "url number",
			update: flowstore.PRUpdate{Provider: "github", Number: 2, URL: "https://github.com/approachcontrol/approach/pull/1", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "GitHub PR URL number",
		},
		{
			name:   "url extra path",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/approachcontrol/approach/pull/1/files", HeadBranch: "flow/pr", BaseBranch: "main"},
			want:   "GitHub PR URL must have /owner/repo/pull/number path",
		},
		{
			name:   "head branch",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/approachcontrol/approach/pull/1", BaseBranch: "main"},
			want:   "PR head branch is required",
		},
		{
			name:   "base branch",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/approachcontrol/approach/pull/1", HeadBranch: "flow/pr"},
			want:   "PR base branch is required",
		},
		{
			name:   "branch consistency",
			update: flowstore.PRUpdate{Provider: "github", Number: 1, URL: "https://github.com/approachcontrol/approach/pull/1", HeadBranch: "feature/other", BaseBranch: "main"},
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
		URL:        "https://github.com/approachcontrol/approach/pull/116",
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
		URL:        "https://github.com/approachcontrol/approach/pull/116",
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
		PRURL:    "https://github.com/approachcontrol/approach/pull/116",
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

func TestStoreMarkManualMergeRejectsUnresolvedMissingDependsOn(t *testing.T) {
	root := t.TempDir()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	flowID := "20260607T120000Z-unresolved-manual-merge"
	writeRawFlowMeta(t, root, flowID, `
  "branch": "flow/manual-merge",
  "preset_name": "research",
  "pr": {"provider": "github", "number": 116, "url": "https://github.com/approachcontrol/approach/pull/116", "head_branch": "flow/manual-merge", "base_branch": "main", "status": "open"},
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "completed", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "ready", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	before := readSQLiteRecordForTest(t, root, flowID)
	_, err = store.MarkManualMerge(flowstore.ManualMergeUpdate{
		FlowID:   flowID,
		PRNumber: 116,
		PRURL:    "https://github.com/approachcontrol/approach/pull/116",
		Commit:   "0123456789abcdef",
		MergedAt: mergedAt,
	})
	if err == nil || !strings.Contains(err.Error(), "unresolved missing dependencies") {
		t.Fatalf("MarkManualMerge() error = %v, want unresolved graph error", err)
	}
	after := readSQLiteRecordForTest(t, root, flowID)
	if string(after) != string(before) {
		t.Fatalf("rejected manual merge changed authoritative record\n--- before ---\n%s\n--- after ---\n%s", before, after)
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
		URL:        "https://github.com/approachcontrol/approach/pull/117",
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
		PRURL:    "https://github.com/approachcontrol/approach/pull/116",
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
		mergeStatus flowstore.PhaseStatus
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
		PRURL:    "https://github.com/approachcontrol/approach/pull/116",
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
		PRURL:    "https://github.com/approachcontrol/approach/pull/116",
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
					URL:        "https://github.com/approachcontrol/approach/pull/116",
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
		phaseStatus  flowstore.PhaseStatus
		phaseNotes   string
		reopenStatus flowstore.PhaseStatus
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
				URL:        "https://github.com/approachcontrol/approach/pull/116",
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
		URL:        "https://github.com/approachcontrol/approach/pull/116",
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
		URL:        "https://github.com/approachcontrol/approach/pull/189",
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

func TestStoreAttachSessionDoesNotReplaceNewerLaunchWithStaleLaunch(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := mustCreateFlow(t, store, "Fence stale session attachment")
	mustCompleteFlowPhases(t, store, &record, "plan", "plan-review")
	for _, launchID := range []string{"launch-1", "launch-2"} {
		record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
			FlowID:   record.FlowID,
			PhaseID:  "implementation",
			LaunchID: launchID,
		})
		if err != nil {
			t.Fatalf("AddPhaseLaunchID(%s) error = %v", launchID, err)
		}
		record, err = store.AttachSession(flowstore.SessionAttachUpdate{
			FlowID:  record.FlowID,
			PhaseID: "implementation",
			Session: flowstore.Session{Provider: "codex", SessionID: "session-1", LaunchID: launchID, Status: "last_seen"},
		})
		if err != nil {
			t.Fatalf("AttachSession(%s) error = %v", launchID, err)
		}
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{Provider: "codex", SessionID: "session-1", LaunchID: "launch-1", Status: "ended"},
	})
	if err != nil {
		t.Fatalf("AttachSession(stale) error = %v", err)
	}

	phase := phaseByID(t, record, "implementation")
	if len(phase.Sessions) != 1 || phase.Sessions[0].LaunchID != "launch-2" || phase.Sessions[0].Status != "last_seen" {
		t.Fatalf("session regressed after stale attachment: %#v", phase.Sessions)
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

func TestStoreCollapseCarriesPhaseAgentSettingsAsAWholeTriple(t *testing.T) {
	claude := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	codex := flowstore.PhaseAgentSettings{Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium"}
	collapse := func(t *testing.T, first, second flowstore.PhaseAgentSettings) flowstore.FlowPhase {
		t.Helper()
		root := t.TempDir()
		now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
		store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		record, err := store.Create(flowstore.FlowRecord{
			FlowID:       "duplicate-agent-settings",
			Title:        "Duplicate agent settings",
			Instructions: "collapse duplicate child rows",
			RepoPath:     filepath.Join(root, "repo"),
			CreatedAt:    now,
			UpdatedAt:    now,
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 2, CreatedAt: now, UpdatedAt: now},
				{
					PhaseID: "Step-1", ParentPhaseID: "implementation", Title: "Step 1",
					Status: flowstore.PhaseReady, Kind: "implementation_child", Order: 10,
					Agent: first.Agent, Model: first.Model, ReasoningEffort: first.ReasoningEffort,
					CreatedAt: now, UpdatedAt: now,
				},
				{
					PhaseID: "step-1", ParentPhaseID: "implementation", Title: "Step 1",
					Status: flowstore.PhasePending, Kind: "implementation_child", Order: 10,
					Agent: second.Agent, Model: second.Model, ReasoningEffort: second.ReasoningEffort,
					CreatedAt: now, UpdatedAt: now,
				},
				{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 3, CreatedAt: now, UpdatedAt: now},
			},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "step-1", Status: flowstore.PhaseCompleted})
		if err != nil {
			t.Fatalf("SetPhase(step-1 completed) error = %v", err)
		}
		var survivors []flowstore.FlowPhase
		for _, phase := range record.Phases {
			if phase.ParentPhaseID == "implementation" {
				survivors = append(survivors, phase)
			}
		}
		if len(survivors) != 1 {
			t.Fatalf("duplicate child rows not collapsed: %#v", record.Phases)
		}
		return survivors[0]
	}

	t.Run("survivor without settings takes the dropped row's", func(t *testing.T) {
		got := collapse(t, flowstore.PhaseAgentSettings{}, claude).AgentSettings()
		if got != claude {
			t.Fatalf("collapsed settings = %#v, want the dropped row's %#v", got, claude)
		}
	})

	t.Run("conflicting rows never mix fields", func(t *testing.T) {
		// Whichever row survives, the result must be one intact triple: a
		// survivor must never end up with one row's agent and another's model.
		got := collapse(t, codex, claude).AgentSettings()
		if got != codex && got != claude {
			t.Fatalf("collapsed settings = %#v, want either %#v or %#v intact", got, codex, claude)
		}
	})
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
	data := readSQLiteRecordForTest(t, root, record.FlowID)
	if !strings.Contains(string(data), `"depends_on": [
        "root"
      ]`) {
		t.Fatalf("SQLite record did not persist depends_on edge:\n%s", data)
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
    {"phase_id": "beta", "title": "Beta", "kind": "", "depends_on": null, "status": "ready", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
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
	if got := phaseByID(t, read, "beta").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("beta DependsOn = %#v, want explicit empty slice", got)
	}
	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: "alpha", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase(alpha completed) error = %v", err)
	}
	if got := phaseByID(t, updated, "beta").Status; got != flowstore.PhaseReady {
		t.Fatalf("beta status = %q, want ready as independent root", got)
	}
	updated, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: "alpha", Status: flowstore.PhaseRunning})
	if err != nil {
		t.Fatalf("SetPhase(alpha running) error = %v", err)
	}
	if got := phaseByID(t, updated, "beta").Status; got != flowstore.PhaseReady {
		t.Fatalf("beta status = %q after alpha regression, want ready as independent root", got)
	}
	data := readSQLiteRecordForTest(t, root, flowID)
	if strings.Contains(string(data), `"depends_on": null`) {
		t.Fatalf("SQLite record kept null depends_on:\n%s", data)
	}
}

func TestExplicitEmptyDependsOnDefaultGraphMeansRoots(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-explicit-default-roots"
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Explicit default roots",
  "instructions": "explicit empty depends_on",
  "status": "pending",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "phases": [
    {"phase_id": "plan", "title": "Plan", "kind": "plan", "depends_on": [], "status": "running", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "plan-review", "title": "Plan Review", "kind": "plan_review", "depends_on": [], "status": "ready", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
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
	if got := phaseByID(t, read, "plan-review").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("plan-review DependsOn = %#v, want explicit empty slice", got)
	}
	if got := phaseByID(t, read, "plan-review").Status; got != flowstore.PhaseReady {
		t.Fatalf("plan-review status = %q, want ready as explicit root", got)
	}
}

func TestReadBackfillsLinearDependsOn(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
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
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
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

func TestReadMissingDependsOnForLegacyCustomFlowStaysUnresolved(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	flowID := "20260607T120000Z-legacy-custom"
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Legacy custom",
  "instructions": "backfill custom records",
  "status": "in_progress",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "phases": [
    {"phase_id": "alpha", "title": "Alpha", "kind": "", "status": "running", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "beta", "title": "Beta", "kind": "", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", read.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	if got := phaseByID(t, read, "beta").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("beta DependsOn = %#v, want explicit empty unresolved graph", got)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: "alpha", Status: flowstore.PhaseNeedsAttention})
	if err != nil {
		t.Fatalf("SetPhase(alpha needs_attention) error = %v", err)
	}
	if got := phaseByID(t, updated, "beta").Status; got != flowstore.PhaseReady {
		t.Fatalf("beta status = %q, want ready as unresolved independent root", got)
	}
}

func TestReadRestoresMissingDependsOnFromNamedPreset(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	writeRawFlowMeta(t, root, "20260607T120000Z-preset-recovery", `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root, Now: func() time.Time { return now }, Presets: []flowstore.Preset{researchPresetForStoreTest()},
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read("20260607T120000Z-preset-recovery")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != "" {
		t.Fatalf("GraphRecovery.Status = %q, want fully recovered graph marker omitted", read.GraphRecovery.Status)
	}
	if got := phaseByID(t, read, "draft").DependsOn; len(got) != 1 || got[0] != "research" {
		t.Fatalf("draft DependsOn = %#v, want [research]", got)
	}

	records, err := store.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].GraphRecovery.Status != "" {
		t.Fatalf("List() recovery = %#v", records)
	}
}

func TestReadRestoresPartiallyMissingDependsOnFromNamedPreset(t *testing.T) {
	root := t.TempDir()
	writeRawFlowMeta(t, root, "20260607T120000Z-partial-preset-recovery", `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "depends_on": [], "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "depends_on": ["draft"], "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: []flowstore.Preset{researchPresetForStoreTest()}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read("20260607T120000Z-partial-preset-recovery")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != "" {
		t.Fatalf("GraphRecovery.Status = %q, want fully recovered graph marker omitted", read.GraphRecovery.Status)
	}
	if got := phaseByID(t, read, "draft").DependsOn; len(got) != 1 || got[0] != "research" {
		t.Fatalf("draft DependsOn = %#v, want [research]", got)
	}
}

func TestReadMissingNamedPresetEdgesUnresolvedWhenPresetUnavailable(t *testing.T) {
	root := t.TempDir()
	writeRawFlowMeta(t, root, "20260607T120000Z-missing-preset", `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "completed", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read("20260607T120000Z-missing-preset")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", read.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	if got := phaseByID(t, read, "draft").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("draft DependsOn = %#v, want explicit empty unresolved graph", got)
	}
}

func TestReadMissingNamedPresetDoesNotBackfillDefaultEdges(t *testing.T) {
	root := t.TempDir()
	writeRawFlowMeta(t, root, "20260607T120000Z-missing-default-shaped-preset", `
  "preset_name": "custom",
  "phases": [
    {"phase_id": "plan", "title": "Plan", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "implementation", "title": "Implementation", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "review-loop", "title": "Review loop", "kind": "review_loop", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read("20260607T120000Z-missing-default-shaped-preset")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", read.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	if got := phaseByID(t, read, "implementation").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("implementation DependsOn = %#v, want explicit empty unresolved graph", got)
	}
	if got := phaseByID(t, read, "review-loop").DependsOn; len(got) != 0 || got == nil {
		t.Fatalf("review-loop DependsOn = %#v, want explicit empty unresolved graph", got)
	}
}

func TestReadPartiallyMissingUnavailableNamedPresetDoesNotSelfHealReadiness(t *testing.T) {
	root := t.TempDir()
	writeRawFlowMeta(t, root, "20260607T120000Z-partial-missing-preset", `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "depends_on": [], "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read("20260607T120000Z-partial-missing-preset")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", read.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	if got := phaseByID(t, read, "draft").Status; got != flowstore.PhasePending {
		t.Fatalf("draft status = %q, want pending while graph recovery is unresolved", got)
	}
}

func TestReadUnresolvedGraphWithPlanReviewDoesNotSelfHealReadiness(t *testing.T) {
	root := t.TempDir()
	writeRawFlowMeta(t, root, "20260607T120000Z-unresolved-plan-review", `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "depends_on": [], "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "review", "title": "Review", "kind": "plan_review", "depends_on": ["research"], "status": "completed", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read("20260607T120000Z-unresolved-plan-review")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", read.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	if got := phaseByID(t, read, "draft").Status; got != flowstore.PhasePending {
		t.Fatalf("draft status = %q, want pending while graph recovery is unresolved", got)
	}
	if got := phaseByID(t, read, "review").Outcome; got != flowstore.OutcomeApproved {
		t.Fatalf("review outcome = %q, want approved normalization without readiness refresh", got)
	}
}

func TestMetadataOnlyUpdatePreservesUnresolvedMissingDependsOn(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-unresolved-metadata"
	writeRawFlowMeta(t, root, flowID, `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if _, err := store.SetIssue(flowstore.IssueUpdate{
		FlowID:   flowID,
		Provider: "github",
		Number:   276,
		URL:      "https://github.com/approachcontrol/approach/issues/276",
	}); err != nil {
		t.Fatalf("SetIssue() error = %v", err)
	}
	data := readSQLiteRecordForTest(t, root, flowID)
	var raw struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal(meta.json) error = %v\n%s", err, data)
	}
	for _, phase := range raw.Phases {
		if strings.Contains(string(phase["phase_id"]), "draft") {
			if _, ok := phase["depends_on"]; !ok {
				t.Fatalf("canonical unresolved draft omitted explicit depends_on:\n%s", data)
			}
		}
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("Unmarshal(meta.json) top-level error = %v\n%s", err, data)
	}
	var headless bool
	if value, ok := metadata["headless"]; !ok || json.Unmarshal(value, &headless) != nil || !headless {
		t.Fatalf("specialized metadata write did not persist normalized headless=true:\n%s", data)
	}
	if !strings.Contains(string(data), `"graph_recovery": {`) {
		t.Fatalf("unresolved graph marker was not persisted:\n%s", data)
	}

	reread, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() after SetIssue error = %v", err)
	}
	if reread.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", reread.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	if got := phaseByID(t, reread, "draft").Status; got != flowstore.PhasePending {
		t.Fatalf("draft status after metadata write = %q, want pending", got)
	}
}

func TestSetPhaseRejectsUnresolvedMissingDependsOn(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-unresolved-phase-mutation"
	writeRawFlowMeta(t, root, flowID, `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "ready", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	before := readSQLiteRecordForTest(t, root, flowID)
	_, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: "research", Status: flowstore.PhaseCompleted})
	if err == nil || !strings.Contains(err.Error(), "unresolved missing dependencies") {
		t.Fatalf("SetPhase() error = %v, want unresolved graph error", err)
	}
	after := readSQLiteRecordForTest(t, root, flowID)
	if string(after) != string(before) {
		t.Fatalf("rejected phase mutation changed authoritative record\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestSetPhaseRestoresNamedPresetEdgesBeforeWrite(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-preset-mutation-recovery"
	writeRawFlowMeta(t, root, flowID, `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "ready", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: []flowstore.Preset{researchPresetForStoreTest()}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: "research", Status: flowstore.PhaseCompleted})
	if err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if got := phaseByID(t, updated, "draft").DependsOn; len(got) != 1 || got[0] != "research" {
		t.Fatalf("updated draft DependsOn = %#v, want [research]", got)
	}
	data := readSQLiteRecordForTest(t, root, flowID)
	if !strings.Contains(string(data), `"depends_on": [
        "research"
      ]`) {
		t.Fatalf("SQLite record did not persist restored depends_on:\n%s", data)
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

func TestKindConstantsAndSemanticKind(t *testing.T) {
	want := map[string]flowstore.PhaseKind{
		"plan":                 flowstore.KindPlan,
		"plan_review":          flowstore.KindPlanReview,
		"implementation":       flowstore.KindImplementation,
		"review_loop":          flowstore.KindReviewLoop,
		"pr_creation":          flowstore.KindPRCreation,
		"autoreview":           flowstore.KindAutoreview,
		"merge":                flowstore.KindMerge,
		"implementation_child": flowstore.KindImplementationChild,
	}
	for value, got := range want {
		if string(got) != value {
			t.Fatalf("kind constant = %q, want %q", got, value)
		}
	}
	if got := flowstore.SemanticKind(flowstore.FlowPhase{PhaseID: "plan-review"}); got != flowstore.KindPlanReview {
		t.Fatalf("SemanticKind(plan-review) = %q, want plan_review", got)
	}
	if got := flowstore.SemanticKind(flowstore.FlowPhase{PhaseID: "plan-review", Kind: flowstore.KindImplementation}); got != flowstore.KindImplementation {
		t.Fatalf("SemanticKind(explicit kind) = %q, want implementation", got)
	}
	if got := flowstore.SemanticKind(flowstore.FlowPhase{PhaseID: "custom"}); got != "" {
		t.Fatalf("SemanticKind(custom) = %q, want empty", got)
	}
}

func TestMigrationBackfillsKindFromWellKnownID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record := graphRecord(root, "20260607T120000Z-kind-backfill", []flowstore.FlowPhase{
		graphPhase(now, "plan", flowstore.PhaseCompleted, nil),
		graphPhase(now, "plan-review", flowstore.PhaseCompleted, nil),
	})
	record.Phases[1].Outcome = ""
	legacyDir := filepath.Join(root, "flows", record.FlowID)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(legacy) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "meta.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	review := phaseByID(t, read, "plan-review")
	if review.Kind != flowstore.KindPlanReview {
		t.Fatalf("plan-review Kind = %q, want %q", review.Kind, flowstore.KindPlanReview)
	}
	if review.Outcome != flowstore.OutcomeApproved {
		t.Fatalf("plan-review Outcome = %q, want approved", review.Outcome)
	}
}

func TestDownstreamGateByKind(t *testing.T) {
	pr := flowstore.PullRequest{
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/kind",
		BaseBranch: "main",
	}
	tests := []struct {
		name  string
		phase flowstore.FlowPhase
		pr    flowstore.PullRequest
		want  bool
	}{
		{name: "plan_review approved", phase: flowstore.FlowPhase{PhaseID: "design-review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved}, want: true},
		{name: "plan_review missing outcome", phase: flowstore.FlowPhase{PhaseID: "design-review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseCompleted}, want: false},
		{name: "pr_creation with target", phase: flowstore.FlowPhase{PhaseID: "open-pr", Kind: flowstore.KindPRCreation, Status: flowstore.PhaseCompleted}, pr: pr, want: true},
		{name: "pr_creation missing target", phase: flowstore.FlowPhase{PhaseID: "open-pr", Kind: flowstore.KindPRCreation, Status: flowstore.PhaseCompleted}, want: false},
		{name: "default completed", phase: flowstore.FlowPhase{PhaseID: "verify", Kind: flowstore.KindImplementation, Status: flowstore.PhaseCompleted}, want: true},
		{name: "id ignored when kind differs", phase: flowstore.FlowPhase{PhaseID: "plan-review", Kind: flowstore.KindImplementation, Status: flowstore.PhaseCompleted}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := flowstore.FlowRecord{PR: tc.pr}
			if got := flowstore.PhaseGateSatisfied(record, tc.phase); got != tc.want {
				t.Fatalf("PhaseGateSatisfied() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReviewOutcomeValidationByKind(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-review-validation",
		Title:        "Custom Review",
		Instructions: "validate custom review outcomes",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "design-review", Title: "Design Review", Kind: flowstore.KindPlanReview, DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 1, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "design-review", Status: flowstore.PhaseCompleted})
	if err == nil || !strings.Contains(err.Error(), "design-review (plan_review) completed requires outcome") {
		t.Fatalf("SetPhase() error = %v, want custom review outcome validation", err)
	}
}

func TestMergeKindPhaseByCustomID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-merge-kind",
		Title:        "Custom Merge",
		Instructions: "merge by kind",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/custom-merge",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "ship", Title: "Ship", Kind: flowstore.KindMerge, DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 1, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "ship", LaunchID: "launch-1", AutoLaunch: true})
	if err == nil || !flowstore.IsAutoLaunchOutdated(err) {
		t.Fatalf("auto AddPhaseLaunchID(custom merge) error = %v, want auto-launch outdated", err)
	}
	record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "ship", Status: flowstore.PhaseCompleted, Outcome: flowstore.MergeMerged})
	if err != nil {
		t.Fatalf("SetPhase(ship completed) error = %v", err)
	}
	record.PR = flowstore.PullRequest{Provider: "github", Number: 42, URL: "https://github.com/approachcontrol/approach/pull/42", HeadBranch: "flow/custom-merge", BaseBranch: "main"}
	record, err = store.SetPR(flowstore.PRUpdate{FlowID: record.FlowID, Provider: record.PR.Provider, Number: record.PR.Number, URL: record.PR.URL, HeadBranch: record.PR.HeadBranch, BaseBranch: record.PR.BaseBranch})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	_, err = store.SetMerge(flowstore.MergeUpdate{FlowID: record.FlowID, Status: flowstore.MergeMerged, Commit: "abc123", MergedAt: now})
	if err != nil {
		t.Fatalf("SetMerge(custom merge kind) error = %v", err)
	}
}

func TestGraphRejectsTwoMergeKindPhases(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	_, err = store.Create(flowstore.FlowRecord{
		Title:        "Two merges",
		Instructions: "reject ambiguous merge phases",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "ship", Title: "Ship", Kind: flowstore.KindMerge, DependsOn: []string{}, Status: flowstore.PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "release", Title: "Release", Kind: flowstore.KindMerge, DependsOn: []string{}, Status: flowstore.PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at most one merge phase") {
		t.Fatalf("Create() error = %v, want single merge validation", err)
	}
}

func TestGraphRejectsTwoMergeKindPhasesWithoutExplicitEdges(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	_, err = store.Create(flowstore.FlowRecord{
		Title:        "Two merges",
		Instructions: "reject ambiguous merge phases",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "ship", Title: "Ship", Kind: flowstore.KindMerge, Status: flowstore.PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "release", Title: "Release", Kind: flowstore.KindMerge, Status: flowstore.PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at most one merge phase") {
		t.Fatalf("Create() error = %v, want single merge validation", err)
	}
}

func TestAddChildPhaseUnderCustomImplementationID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-implementation-parent",
		Title:        "Custom Implementation",
		Instructions: "children by kind",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "build", Title: "Build", Kind: flowstore.KindImplementation, DependsOn: []string{}, Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{FlowID: record.FlowID, ParentPhaseID: "build", PhaseID: "api", Title: "API", Order: 1})
	if err != nil {
		t.Fatalf("AddChildPhase(custom implementation) error = %v", err)
	}
	child := phaseByID(t, updated, "api")
	if child.ParentPhaseID != "build" || child.Kind != flowstore.KindImplementationChild {
		t.Fatalf("child = %#v, want implementation child under build", child)
	}
	_, err = store.AddChildPhase(flowstore.ChildPhaseUpdate{FlowID: record.FlowID, ParentPhaseID: "missing", PhaseID: "x", Title: "X", Order: 1})
	if err == nil || !strings.Contains(err.Error(), `flow "custom-implementation-parent"`) {
		t.Fatalf("AddChildPhase(missing parent) error = %v, want flow-loaded parent error", err)
	}
}

func TestCreateRejectsChildDeclaredDependencies(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	_, err = store.Create(flowstore.FlowRecord{
		Title:        "Child dependency",
		Instructions: "reject child edges",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			graphPhase(now, "implementation", flowstore.PhasePending, nil),
			{
				PhaseID:       "api",
				ParentPhaseID: "implementation",
				Title:         "api",
				DependsOn:     []string{"implementation"},
				Status:        flowstore.PhasePending,
				Order:         1,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), `child phase "api" cannot declare dependencies`) {
		t.Fatalf("Create() error = %v, want child dependency rejection", err)
	}
}

func TestCreateRejectsInvalidDeclaredDependenciesBeforeNormalization(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		dependsOn []string
		want      string
	}{
		{
			name:      "empty dependency",
			dependsOn: []string{""},
			want:      `phase "publish" has empty dependency`,
		},
		{
			name:      "duplicate dependency",
			dependsOn: []string{"root", " Root "},
			want:      `phase "publish" has duplicate dependency " Root "`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			_, err = store.Create(flowstore.FlowRecord{
				Title:        tc.name,
				Instructions: "reject invalid dependency declarations",
				RepoPath:     filepath.Join(root, "repo"),
				Phases: []flowstore.FlowPhase{
					graphPhase(now, "root", flowstore.PhasePending, []string{}),
					graphPhase(now, "publish", flowstore.PhasePending, tc.dependsOn),
				},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create() error = %v, want %q", err, tc.want)
			}
		})
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

func TestSelfDependencyNotLaunchEligible(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record := flowstore.FlowRecord{AutoMode: true, Phases: []flowstore.FlowPhase{
		graphPhase(now, "self", flowstore.PhaseReady, []string{"self"}),
	}}
	if flowstore.PhaseLaunchEligible(record, 0) {
		t.Fatalf("self-dependent ready row reported launch eligible")
	}
	if phase, idx, ok := flowstore.FirstLaunchablePhase(record); ok {
		t.Fatalf("FirstLaunchablePhase() = (%#v, %d, true), want none", phase, idx)
	}
}

func TestFirstLaunchablePhasePreservesPRMetadataForAutoreviewGate(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	record := flowstore.FlowRecord{
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     273,
			URL:        "https://github.com/approachcontrol/approach/pull/273",
			HeadBranch: "flow/phase-graph",
			BaseBranch: "main",
		},
		Phases: []flowstore.FlowPhase{
			graphPhase(now, "pr-creation", flowstore.PhaseCompleted, []string{}),
			graphPhase(now, "autoreview", flowstore.PhaseReady, []string{"pr-creation"}),
		},
	}

	phase, idx, ok := flowstore.FirstLaunchablePhase(record)
	if !ok || phase.PhaseID != "autoreview" || idx != 1 {
		t.Fatalf("FirstLaunchablePhase() = (%#v, %d, %v), want autoreview at index 1", phase, idx, ok)
	}
}

func TestStoreAddPhaseLaunchIDRejectsDuplicateReadyRow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-duplicate-launch", []flowstore.FlowPhase{
		graphPhase(now, "Step-1", flowstore.PhaseCompleted, []string{}),
		graphPhase(now, "step-1", flowstore.PhaseReady, []string{}),
		graphPhase(now, "step-2", flowstore.PhaseReady, []string{"step-1"}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:     record.FlowID,
		PhaseID:    "step-1",
		LaunchID:   "launch-duplicate",
		AutoLaunch: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("AddPhaseLaunchID(duplicate) error = %v, want not eligible", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "step-1"); len(got.LaunchIDs) != 0 || got.Status != flowstore.PhaseReady {
		t.Fatalf("duplicate row after rejected launch = %#v, want unchanged ready without launch", got)
	}
}

func TestStoreAddPhaseLaunchIDRejectsReadyPhaseWithUnsatisfiedDependency(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-stale-ready-launch", []flowstore.FlowPhase{
		graphPhase(now, "root", flowstore.PhaseRunning, []string{}),
		graphPhase(now, "publish", flowstore.PhaseReady, []string{"root"}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:     record.FlowID,
		PhaseID:    "publish",
		LaunchID:   "launch-stale-ready",
		AutoLaunch: true,
	})
	if err == nil || !flowstore.IsAutoLaunchOutdated(err) {
		t.Fatalf("AddPhaseLaunchID(stale ready) error = %v, want auto-launch outdated", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "publish"); len(got.LaunchIDs) != 0 || got.Status != flowstore.PhaseReady {
		t.Fatalf("publish after rejected launch = %#v, want unchanged canonical row without launch", got)
	}
}

func TestStoreAddPhaseLaunchIDRejectsAutoLaunchWhenAnotherPhaseRunning(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-auto-occupied-launch", []flowstore.FlowPhase{
		graphPhase(now, "branch-a", flowstore.PhaseRunning, []string{}),
		graphPhase(now, "branch-b", flowstore.PhaseReady, []string{}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:     record.FlowID,
		PhaseID:    "branch-b",
		LaunchID:   "launch-branch-b",
		AutoLaunch: true,
	})
	if err == nil || !flowstore.IsAutoLaunchOutdated(err) || !strings.Contains(err.Error(), "already has running phase") {
		t.Fatalf("AddPhaseLaunchID(auto occupied) error = %v, want auto-launch outdated occupancy error", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "branch-b"); len(got.LaunchIDs) != 0 || got.Status != flowstore.PhaseReady {
		t.Fatalf("branch-b after rejected auto launch = %#v, want unchanged ready without launch", got)
	}
}

func TestStoreAddPhaseLaunchIDRejectsManualLaunchWhenAnotherPhaseRunning(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-manual-occupied-launch", []flowstore.FlowPhase{
		graphPhase(now, "branch-a", flowstore.PhaseRunning, []string{}),
		graphPhase(now, "branch-b", flowstore.PhaseReady, []string{}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "branch-b",
		LaunchID: "launch-branch-b",
	})
	if err == nil || flowstore.IsAutoLaunchOutdated(err) || !strings.Contains(err.Error(), "already has running phase") {
		t.Fatalf("AddPhaseLaunchID(manual occupied) error = %v, want manual occupancy error", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "branch-b"); len(got.LaunchIDs) != 0 || got.Status != flowstore.PhaseReady {
		t.Fatalf("branch-b after rejected manual launch = %#v, want unchanged ready without launch", got)
	}
}

func TestStoreAddPhaseLaunchIDRejectsRelaunchWhenAnotherPhaseRunning(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-manual-occupied-relaunch", []flowstore.FlowPhase{
		graphPhase(now, "branch-a", flowstore.PhaseRunning, []string{}),
		graphPhase(now, "branch-b", flowstore.PhaseNeedsAttention, []string{}),
	})
	writeFreshFlowRecordForTest(t, root, record)

	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "branch-b",
		LaunchID: "launch-branch-b",
	})
	if err == nil || !strings.Contains(err.Error(), "already has running phase") {
		t.Fatalf("AddPhaseLaunchID(manual occupied relaunch) error = %v, want occupancy error", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(t, read, "branch-b"); len(got.LaunchIDs) != 0 || got.Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("branch-b after rejected relaunch = %#v, want unchanged needs_attention without launch", got)
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

func graphPhase(now time.Time, phaseID string, status flowstore.PhaseStatus, dependsOn []string) flowstore.FlowPhase {
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

func researchPresetForStoreTest() flowstore.Preset {
	return flowstore.Preset{
		Name: "research",
		Phases: []flowstore.PhaseSpec{
			{ID: "research", Title: "Research", Kind: flowstore.KindPlan, DependsOn: []string{}},
			{ID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, DependsOn: []string{"research"}},
			{ID: "publish", Title: "Publish", Kind: flowstore.KindMerge, DependsOn: []string{"draft"}},
		},
	}
}

func writeRawFlowMeta(t *testing.T, root, flowID, fields string) {
	t.Helper()
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Raw flow",
  "instructions": "raw migration fixture",
  "status": "in_progress",
  "repo_path": "` + filepath.Join(root, "repo") + `",
` + fields + `,
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func writeFreshFlowRecordForTest(t *testing.T, root string, record flowstore.FlowRecord) {
	t.Helper()
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
			URL:        "https://github.com/approachcontrol/approach/pull/116",
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
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	updatedAt := record.UpdatedAt.UTC()
	projectionTime := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%09dZ",
		updatedAt.Year(), updatedAt.Month(), updatedAt.Day(), updatedAt.Hour(), updatedAt.Minute(), updatedAt.Second(), updatedAt.Nanosecond())
	_, err = db.Exec(`
INSERT INTO flows(flow_id, repo_path, status, updated_at, record) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(flow_id) DO UPDATE SET repo_path=excluded.repo_path, status=excluded.status,
updated_at=excluded.updated_at, record=excluded.record`,
		record.FlowID, filepath.Clean(record.RepoPath), record.Status, projectionTime, data)
	return err
}

func readSQLiteRecordForTest(t *testing.T, root, flowID string) []byte {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatalf("open approach.db: %v", err)
	}
	defer db.Close()
	var data []byte
	if err := db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", flowID).Scan(&data); err != nil {
		t.Fatalf("read SQLite record %q: %v", flowID, err)
	}
	return data
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

func TestCreateWithOptionsStampsPhaseAgentSettings(t *testing.T) {
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

	want := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	record, err := store.CreateWithOptions(flowstore.FlowRecord{
		Title:        "Stamp agent settings",
		Instructions: "Capture the active agent settings.",
		RepoPath:     repoPath,
	}, flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{
		Agent:           " Claude ",
		Model:           "Claude-Opus-5",
		ReasoningEffort: "HIGH",
	}})
	if err != nil {
		t.Fatalf("CreateWithOptions() error = %v", err)
	}
	for _, phase := range record.Phases {
		if got := phase.AgentSettings(); got != want {
			t.Fatalf("phase %q settings = %#v, want %#v", phase.PhaseID, got, want)
		}
	}

	reread, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	for _, phase := range reread.Phases {
		if got := phase.AgentSettings(); got != want {
			t.Fatalf("reread phase %q settings = %#v, want %#v", phase.PhaseID, got, want)
		}
	}

	data := readSQLiteRecordForTest(t, root, record.FlowID)
	var raw struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal(meta.json) error = %v\n%s", err, data)
	}
	if len(raw.Phases) == 0 {
		t.Fatalf("meta.json has no phases:\n%s", data)
	}
	for _, phase := range raw.Phases {
		for key, wantValue := range map[string]string{
			"agent":            `"claude"`,
			"model":            `"claude-opus-5"`,
			"reasoning_effort": `"high"`,
		} {
			got, ok := phase[key]
			if !ok {
				t.Fatalf("meta.json phase missing %q:\n%s", key, data)
			}
			if string(got) != wantValue {
				t.Fatalf("meta.json phase %s = %s, want %s", key, got, wantValue)
			}
		}
	}
}

func TestCreateWithOptionsValidatesPhaseAgentSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings flowstore.PhaseAgentSettings
		wantErr  string
	}{
		{
			name:     "unsupported command",
			settings: flowstore.PhaseAgentSettings{Agent: "gemini"},
			wantErr:  `unsupported agent "gemini"`,
		},
		{
			name:     "retired command",
			settings: flowstore.PhaseAgentSettings{Agent: "codex-app"},
			wantErr:  `unsupported agent "codex-app"; choose codex, claude, or cursor-agent`,
		},
		{
			name:     "unsupported model for command",
			settings: flowstore.PhaseAgentSettings{Agent: "claude", Model: "gpt-5.5"},
			wantErr:  `unsupported model "gpt-5.5" for claude`,
		},
		{
			name:     "unsupported reasoning effort for command",
			settings: flowstore.PhaseAgentSettings{Agent: "codex", ReasoningEffort: "max"},
			wantErr:  `unsupported reasoning effort "max" for codex`,
		},
		{
			name:     "model without command",
			settings: flowstore.PhaseAgentSettings{Model: "claude-opus-5"},
			wantErr:  "agent is not set",
		},
		{
			name:     "reasoning effort without command",
			settings: flowstore.PhaseAgentSettings{ReasoningEffort: "high"},
			wantErr:  "agent is not set",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			_, err = store.CreateWithOptions(flowstore.FlowRecord{
				Title:        "Invalid settings",
				Instructions: "Reject unsupported agent settings.",
				RepoPath:     filepath.Join(root, "repo"),
			}, flowstore.CreateOptions{PhaseAgent: tc.settings})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CreateWithOptions() error = %v, want error containing %q", err, tc.wantErr)
			}
			assertNoSQLiteFlowRows(t, root)
		})
	}
}

func TestCreateWithOptionsRejectsCodexAppAtBothInputBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		record  flowstore.FlowRecord
		options flowstore.CreateOptions
	}{
		{
			name:    "create default",
			options: flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{Agent: "codex-app"}},
		},
		{
			name: "declared phase",
			record: flowstore.FlowRecord{Phases: []flowstore.FlowPhase{{
				PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan,
				Status: flowstore.PhasePending, Order: 1, Agent: "codex-app",
			}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			tc.record.Title = "Rejected legacy command"
			tc.record.Instructions = "New input must use a supported command."
			tc.record.RepoPath = filepath.Join(root, "repo")
			created, err := store.CreateWithOptions(tc.record, tc.options)
			want := `unsupported agent "codex-app"; choose codex, claude, or cursor-agent`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("CreateWithOptions() = %#v, %v, want error containing %q", created, err, want)
			}
			if created.FlowID != "" {
				t.Fatalf("CreateWithOptions() returned partial Flow %#v", created)
			}
			assertNoSQLiteFlowRows(t, root)
		})
	}
}

func TestCreateWithOptionsAcceptsEmptyPhaseAgentSettings(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.CreateWithOptions(flowstore.FlowRecord{
		Title:        "No settings",
		Instructions: "Stamp nothing when no settings are supplied.",
		RepoPath:     filepath.Join(root, "repo"),
	}, flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{
		Agent:           "  ",
		Model:           "\t",
		ReasoningEffort: " ",
	}})
	if err != nil {
		t.Fatalf("CreateWithOptions() error = %v", err)
	}
	for _, phase := range record.Phases {
		if got := phase.AgentSettings(); !got.IsZero() {
			t.Fatalf("phase %q settings = %#v, want zero", phase.PhaseID, got)
		}
	}
	assertNoPhaseAgentKeys(t, readSQLiteRecordForTest(t, root, record.FlowID))
}

func TestLegacyFlowRecordWithoutPhaseAgentSettingsStaysClean(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Legacy flow",
		Instructions: "Created before per-phase agent settings existed.",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertNoPhaseAgentKeys(t, readSQLiteRecordForTest(t, root, record.FlowID))

	if _, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   record.FlowID,
		PhaseID:  "plan",
		LaunchID: "launch-1",
	}); err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	if _, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseBlocked,
		Notes:   "blocked for the fixture",
	}); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if _, err := store.RestartPhase(flowstore.PhaseRestartUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Notes:   "restarting",
	}); err != nil {
		t.Fatalf("RestartPhase() error = %v", err)
	}

	reread, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	for _, phase := range reread.Phases {
		if got := phase.AgentSettings(); !got.IsZero() {
			t.Fatalf("phase %q settings = %#v, want zero", phase.PhaseID, got)
		}
	}
	assertNoPhaseAgentKeys(t, readSQLiteRecordForTest(t, root, record.FlowID))
}

func TestStoredCodexAppPhaseAgentNormalizesOnSQLiteReadWithoutRewrite(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title: "Stored legacy agent", Instructions: "Normalize only the read view.",
		RepoPath: filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record.Phases[0].Agent = "codex-app"
	if err := writeFlowRecordForTest(root, record); err != nil {
		t.Fatalf("write legacy SQLite record: %v", err)
	}
	before := readSQLiteRecordForTest(t, root, record.FlowID)
	if !bytes.Contains(before, []byte(`"agent": "codex-app"`)) {
		t.Fatalf("fixture does not contain retired spelling:\n%s", before)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := read.Phases[0].Agent; got != "codex" {
		t.Fatalf("Read() phase agent = %q, want codex", got)
	}
	listed, err := store.List(flowstore.FlowFilter{RepoPath: record.RepoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Phases[0].Agent != "codex" {
		t.Fatalf("List() = %#v, want one Flow normalized to codex", listed)
	}
	after := readSQLiteRecordForTest(t, root, record.FlowID)
	if !bytes.Equal(after, before) {
		t.Fatalf("Read/List rewrote stored bytes:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestStoredCodexAppPhaseAgentNormalizesDuringLegacyImportWithoutRewritingSource(t *testing.T) {
	root := t.TempDir()
	flowID := "20260813T120000Z-legacy-codex-app"
	writeRawFlowMeta(t, root, flowID, `
  "merge": {"status": "pending"},
  "phases": [
    {"phase_id": "plan", "title": "Plan", "kind": "plan", "agent": "codex-app", "depends_on": [], "status": "running", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ]`)
	legacyPath := filepath.Join(root, "flows", flowID, "meta.json")
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := read.Phases[0].Agent; got != "codex" {
		t.Fatalf("Read() phase agent = %q, want codex", got)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("legacy import rewrote source bytes:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestMetadataOnlyUpdatePreservesPhaseAgentSettingsOnUnresolvedGraph(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-unresolved-agent-settings"
	writeRawFlowMeta(t, root, flowID, `
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "running", "order": 1, "agent": " CLAUDE ", "model": "Claude-Opus-5", "reasoning_effort": "HIGH ", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "agent": "claude", "model": "claude-opus-5", "reasoning_effort": "high", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
	    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "agent": "claude", "model": "claude-opus-5", "reasoning_effort": "high", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
	  ]`)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  flowID,
		PhaseID: "research",
		Session: flowstore.Session{Provider: "claude", SessionID: "session-1", LaunchID: "launch-1"},
	}); err != nil {
		t.Fatalf("AttachSession() error = %v", err)
	}

	reread, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if reread.GraphRecovery.Status != flowstore.GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("GraphRecovery.Status = %q, want %q", reread.GraphRecovery.Status, flowstore.GraphRecoveryMissingEdgesUnresolved)
	}
	want := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	for _, phase := range reread.Phases {
		if got := phase.AgentSettings(); got != want {
			t.Fatalf("phase %q settings = %#v, want %#v", phase.PhaseID, got, want)
		}
	}

	data := readSQLiteRecordForTest(t, root, flowID)
	var raw struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal(meta.json) error = %v\n%s", err, data)
	}
	for _, phase := range raw.Phases {
		for key, wantValue := range map[string]string{
			"agent":            `"claude"`,
			"model":            `"claude-opus-5"`,
			"reasoning_effort": `"high"`,
		} {
			got, ok := phase[key]
			if !ok {
				t.Fatalf("meta.json phase missing %q after metadata-only write:\n%s", key, data)
			}
			if string(got) != wantValue {
				t.Fatalf("meta.json phase %s = %s, want %s", key, got, wantValue)
			}
		}
	}
}

func TestCreateWithOptionsDeclaredPhaseAgentSettings(t *testing.T) {
	declared := func(phases ...flowstore.FlowPhase) flowstore.FlowRecord {
		return flowstore.FlowRecord{
			Title:        "Declared phases",
			Instructions: "Declared phases carry their own settings.",
			Phases:       phases,
		}
	}
	t.Run("empty declared settings take the create defaults", func(t *testing.T) {
		root := t.TempDir()
		store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		record := declared(
			flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhasePending, Order: 1},
			flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2},
		)
		record.RepoPath = filepath.Join(root, "repo")
		created, err := store.CreateWithOptions(record, flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{
			Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium",
		}})
		if err != nil {
			t.Fatalf("CreateWithOptions() error = %v", err)
		}
		want := flowstore.PhaseAgentSettings{Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium"}
		for _, phase := range created.Phases {
			if got := phase.AgentSettings(); got != want {
				t.Fatalf("phase %q settings = %#v, want %#v", phase.PhaseID, got, want)
			}
		}
	})

	t.Run("declared settings win over the create defaults", func(t *testing.T) {
		root := t.TempDir()
		store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		record := declared(
			flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhasePending, Order: 1, Agent: "claude"},
			flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2},
		)
		record.RepoPath = filepath.Join(root, "repo")
		created, err := store.CreateWithOptions(record, flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{
			Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium",
		}})
		if err != nil {
			t.Fatalf("CreateWithOptions() error = %v", err)
		}
		if got := phaseByID(t, created, "plan").AgentSettings(); got != (flowstore.PhaseAgentSettings{Agent: "claude"}) {
			t.Fatalf("plan settings = %#v, want claude only", got)
		}
		want := flowstore.PhaseAgentSettings{Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium"}
		if got := phaseByID(t, created, "implementation").AgentSettings(); got != want {
			t.Fatalf("implementation settings = %#v, want %#v", got, want)
		}
	})

	t.Run("declared model without an agent is rejected", func(t *testing.T) {
		root := t.TempDir()
		store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		record := declared(
			flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhasePending, Order: 1, Model: "claude-opus-5"},
		)
		record.RepoPath = filepath.Join(root, "repo")
		_, err = store.CreateWithOptions(record, flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{
			Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium",
		}})
		if err == nil || !strings.Contains(err.Error(), "agent is not set") {
			t.Fatalf("CreateWithOptions() error = %v, want agent is not set", err)
		}
	})
}

func TestAddChildPhaseInheritsImplementationAgentSettings(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Two implementation phases carrying different settings, so the assertion
	// can only pass if the child copied from its own parent rather than from
	// the create defaults or the first phase in the record.
	wantFirst := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	wantSecond := flowstore.PhaseAgentSettings{Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "medium"}
	record, err := store.CreateWithOptions(flowstore.FlowRecord{
		Title:        "Child inheritance",
		Instructions: "Children inherit their own implementation phase settings.",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhasePending, Order: 1},
			{
				PhaseID: "implementation-first", Title: "First", Kind: flowstore.KindImplementation,
				Status: flowstore.PhasePending, Order: 2,
				Agent: wantFirst.Agent, Model: wantFirst.Model, ReasoningEffort: wantFirst.ReasoningEffort,
			},
			{
				PhaseID: "implementation-second", Title: "Second", Kind: flowstore.KindImplementation,
				Status: flowstore.PhasePending, Order: 3,
				Agent: wantSecond.Agent, Model: wantSecond.Model, ReasoningEffort: wantSecond.ReasoningEffort,
			},
		},
	}, flowstore.CreateOptions{PhaseAgent: flowstore.PhaseAgentSettings{
		Agent: "claude", Model: "claude-sonnet-5", ReasoningEffort: "low",
	}})
	if err != nil {
		t.Fatalf("CreateWithOptions() error = %v", err)
	}
	updated, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation-second",
		PhaseID:       "implementation-second-step-1",
		Title:         "Step one",
		Order:         1,
	})
	if err != nil {
		t.Fatalf("AddChildPhase() error = %v", err)
	}
	if got := phaseByID(t, updated, "implementation-second-step-1").AgentSettings(); got != wantSecond {
		t.Fatalf("child settings = %#v, want its own parent's %#v", got, wantSecond)
	}

	rewritten, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        record.FlowID,
		ParentPhaseID: "implementation-second",
		PhaseID:       "implementation-second-step-1",
		Title:         "Step one renamed",
		Order:         1,
	})
	if err != nil {
		t.Fatalf("AddChildPhase() re-run error = %v", err)
	}
	if got := phaseByID(t, rewritten, "implementation-second-step-1"); got.Title != "Step one renamed" {
		t.Fatalf("re-run title = %q, want the update branch to have run", got.Title)
	}
	if got := phaseByID(t, rewritten, "implementation-second-step-1").AgentSettings(); got != wantSecond {
		t.Fatalf("child settings after re-run = %#v, want %#v", got, wantSecond)
	}
	if got := phaseByID(t, rewritten, "implementation-first").AgentSettings(); got != wantFirst {
		t.Fatalf("sibling implementation settings = %#v, want %#v", got, wantFirst)
	}
}

func TestPhaseAgentSettingsNormalizeValidateAndIsZero(t *testing.T) {
	tests := []struct {
		name     string
		settings flowstore.PhaseAgentSettings
		want     flowstore.PhaseAgentSettings
		wantZero bool
		wantErr  string
	}{
		{
			name:     "trims and lowercases",
			settings: flowstore.PhaseAgentSettings{Agent: " CLAUDE ", Model: "Claude-Opus-5", ReasoningEffort: "HIGH "},
			want:     flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"},
		},
		{
			name:     "whitespace only is zero",
			settings: flowstore.PhaseAgentSettings{Agent: " ", Model: "\t", ReasoningEffort: "\n"},
			want:     flowstore.PhaseAgentSettings{},
			wantZero: true,
		},
		{
			name:     "agent alone is valid",
			settings: flowstore.PhaseAgentSettings{Agent: "codex"},
			want:     flowstore.PhaseAgentSettings{Agent: "codex"},
		},
		{
			name:     "retired agent is rejected without normalization",
			settings: flowstore.PhaseAgentSettings{Agent: "codex-app"},
			want:     flowstore.PhaseAgentSettings{Agent: "codex-app"},
			wantErr:  `unsupported agent "codex-app"; choose codex, claude, or cursor-agent`,
		},
		{
			name:     "default is stored verbatim",
			settings: flowstore.PhaseAgentSettings{Agent: "claude", Model: "default", ReasoningEffort: "default"},
			want:     flowstore.PhaseAgentSettings{Agent: "claude", Model: "default", ReasoningEffort: "default"},
		},
		{
			name:     "model without an agent is rejected",
			settings: flowstore.PhaseAgentSettings{Model: "claude-opus-5"},
			want:     flowstore.PhaseAgentSettings{Model: "claude-opus-5"},
			wantErr:  "agent is not set",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.settings.Normalize(); got != tc.want {
				t.Fatalf("Normalize() = %#v, want %#v", got, tc.want)
			}
			if got := tc.settings.IsZero(); got != tc.wantZero {
				t.Fatalf("IsZero() = %t, want %t", got, tc.wantZero)
			}
			err := tc.settings.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestPhaseAgentSettingsFromResolvedAgentSettings(t *testing.T) {
	got := flowstore.PhaseAgentSettingsFrom(agent.Settings{
		Command:         "claude",
		Model:           "claude-opus-5",
		ReasoningEffort: "high",
	})
	want := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	if got != want {
		t.Fatalf("PhaseAgentSettingsFrom() = %#v, want %#v", got, want)
	}
}

func assertNoSQLiteFlowRows(t *testing.T, root string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatalf("open approach.db: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM flows").Scan(&count); err != nil {
		t.Fatalf("count SQLite Flow rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("SQLite Flow row count = %d, want zero after rejected settings", count)
	}
}

func assertNoPhaseAgentKeys(t *testing.T, data []byte) {
	t.Helper()
	var raw struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal(meta.json) error = %v\n%s", err, data)
	}
	for _, phase := range raw.Phases {
		for _, key := range []string{"agent", "model", "reasoning_effort"} {
			if _, ok := phase[key]; ok {
				t.Fatalf("meta.json phase unexpectedly carries %q:\n%s", key, data)
			}
		}
	}
}
