package flowstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLegacyFlow(t *testing.T, root, flowID string, record FlowRecord) {
	t.Helper()
	dir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeLegacyFlowWithoutDependsOn writes a legacy meta.json with the depends_on
// key absent from every phase. FlowPhase.DependsOn has no omitempty, so plain
// marshalling would emit `"depends_on": null` — and the migration's presence
// check keys off the field existing at all, not its value.
func writeLegacyFlowWithoutDependsOn(t *testing.T, root, flowID string, record FlowRecord) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	phases, ok := raw["phases"].([]any)
	if !ok {
		t.Fatalf("phases is %T, want a JSON array", raw["phases"])
	}
	for _, phase := range phases {
		entry, ok := phase.(map[string]any)
		if !ok {
			t.Fatalf("phase is %T, want a JSON object", phase)
		}
		delete(entry, "depends_on")
	}
	stripped, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), stripped, 0o600); err != nil {
		t.Fatal(err)
	}
}

func legacyRecord(flowID string, now time.Time) FlowRecord {
	return FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        flowID,
		Title:         flowID,
		Instructions:  "instructions",
		Status:        StatusPending,
		RepoPath:      "/tmp/repo",
		Phases: []FlowPhase{{
			PhaseID: "plan", Title: "Plan", Kind: KindPlan, DependsOn: []string{},
			Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// An unreadable meta.json is environmental, not "this record is garbage". The
// migration must refuse to run rather than import a truncated corpus, because
// once approach.db exists the legacy tree is never consulted again and the
// missing Flow would be lost permanently.
func TestMigrationRefusesToDropUnreadableLegacyFlow(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the unreadable-file permission check")
	}
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyFlow(t, root, "readable-flow", legacyRecord("readable-flow", now))
	writeLegacyFlow(t, root, "locked-flow", legacyRecord("locked-flow", now))

	locked := filepath.Join(root, "flows", "locked-flow", "meta.json")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	_, err := NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatalf("NewStore() error = nil, want the migration to abort on an unreadable legacy flow")
	}
	if !strings.Contains(err.Error(), "locked-flow") {
		t.Fatalf("NewStore() error = %v, want it to name the unreadable flow", err)
	}
	// The legacy source must remain authoritative so a retry can succeed.
	if _, statErr := os.Stat(filepath.Join(root, "flows", "readable-flow")); statErr != nil {
		t.Fatalf("legacy source was disturbed by the aborted migration: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, databaseFilename)); !os.IsNotExist(statErr) {
		t.Fatalf("aborted migration left a database behind: %v", statErr)
	}

	// Fixing the cause and retrying must migrate the whole corpus.
	if err := os.Chmod(locked, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() after repair error = %v", err)
	}
	defer store.Close()
	records, err := store.List(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("migrated %d flows, want 2", len(records))
	}
}

// A crash between the stage build and the database promotion must resume even
// if the user's preset config changed in the meantime. Re-deriving the expected
// record set from the legacy source would make canonicalization
// preset-dependent and wedge the app permanently.
func TestInterruptedCutoverResumesAfterPresetConfigChanges(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	record := legacyRecord("preset-flow", now)
	record.PresetName = "custom"
	// Two phases with depends_on omitted entirely, so canonicalization must
	// consult the preset registry to restore the plan -> build edge. A
	// single-phase graph would canonicalize identically with or without the
	// preset and would not exercise the resume path's preset dependence.
	record.Phases = []FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
		{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
	}
	writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", record)

	custom := Preset{Name: "custom", Phases: []PhaseSpec{
		{ID: "plan", Title: "Plan", Kind: KindPlan},
		{ID: "build", Title: "Build", Kind: KindImplementation, DependsOn: []string{"plan"}},
	}}

	// Build the interrupted state the way production does — canonicalize with the
	// preset available, then stage without promoting. Renaming a cleanly closed
	// approach.db into the stage slot would leave a WAL-mode file, which is not
	// what an interrupted cutover actually produces (the stage is journal_mode
	// DELETE), so this path has to be exercised through buildStagedDatabase.
	presets, err := presetRegistry([]Preset{custom})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := readAndCanonicalizeLegacy(root, "flows", presets)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.unresolved) != 0 {
		t.Fatalf("unresolved = %v, want the preset to resolve the graph", imported.unresolved)
	}
	if err := buildStagedDatabase(filepath.Join(root, stageFilename), imported.records); err != nil {
		t.Fatal(err)
	}

	// The user removes the preset from their config before restarting.
	resumed, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() after preset removal error = %v, want the resume to succeed", err)
	}
	defer resumed.Close()
	records, err := resumed.List(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].FlowID != "preset-flow" {
		t.Fatalf("resumed records = %#v, want the staged preset-flow", records)
	}
}

// A stage left behind by a crash that still promoted must not be resurrected as
// an "interrupted" cutover if the user later removes approach.db.
func TestObsoleteStageIsDiscardedOnceDatabaseExists(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, stageFilename)
	if err := os.WriteFile(stage, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() with a stale stage error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stale stage survived the open: %v", err)
	}
}

// Close must release the pooled handle, and the Store must not be usable after.
func TestStoreCloseReleasesTheDatabase(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Create(FlowRecord{Title: "t", Instructions: "i", RepoPath: "/tmp/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.List(FlowFilter{}); err == nil {
		t.Fatalf("List() after Close() error = nil, want a closed-database error")
	}
	// A fresh Store over the same root still works, so Close released cleanly.
	reopened, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() after Close() error = %v", err)
	}
	defer reopened.Close()
	records, err := reopened.List(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records after reopen = %d, want 1", len(records))
	}
}
