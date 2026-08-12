package flowstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureBootstrapWarnings redirects the cutover notice so a test can assert
// what the operator is actually told.
func captureBootstrapWarnings(t *testing.T) *[]string {
	t.Helper()
	var warnings []string
	restore := bootstrapWarnf
	bootstrapWarnf = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { bootstrapWarnf = restore })
	return &warnings
}

// A crash during the very first bootstrap of a brand-new root leaves a stage
// behind with no legacy source next to it. That stage cannot be the sole
// carrier of anything, so it must be discarded and rebuilt. Refusing instead
// would leave a fresh install permanently unable to start, and the refusal used
// to point the operator at a flows.legacy/ directory this build never creates.
func TestFirstBootstrapCrashDoesNotBrickAFreshRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, root string)
	}{
		{
			name: "zero byte stage",
			build: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, stageFilename), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stage with a hot journal",
			build: func(t *testing.T, root string) {
				stage := filepath.Join(root, stageFilename)
				if err := buildStagedDatabase(stage, nil); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stage+"-journal", []byte("interrupted"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.build(t, root)

			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v, want a fresh root to recover from an interrupted first bootstrap", err)
			}
			defer store.Close()
			records, err := store.List(FlowFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 0 {
				t.Fatalf("records = %d, want 0", len(records))
			}
			for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
				if _, err := os.Lstat(filepath.Join(root, stageFilename) + suffix); !os.IsNotExist(err) {
					t.Fatalf("stage artifact %q survived the rebuild: %v", stageFilename+suffix, err)
				}
			}
		})
	}
}

// An unusable approach.db is recoverable precisely because this cutover leaves
// flows/ where it found it. The error has to say so — an operator staring at
// "file is not a database" has no way to know their records are sitting intact
// next to the broken file.
func TestUnusableDatabaseNamesTheLegacyRecoveryPath(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyFlow(t, root, "kept-flow", legacyRecord("kept-flow", now))
	if err := os.WriteFile(filepath.Join(root, databaseFilename), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("NewStore() error = nil, want a corrupt database to be rejected")
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "flows")) {
		t.Fatalf("NewStore() error = %v, want it to name the retained legacy source", err)
	}
	if !strings.Contains(err.Error(), databaseFilename) {
		t.Fatalf("NewStore() error = %v, want it to name the database to move aside", err)
	}

	// Following the instructions must actually work.
	if err := os.Remove(filepath.Join(root, databaseFilename)); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() after following the recovery advice error = %v", err)
	}
	defer store.Close()
	if _, err := store.Read("kept-flow"); err != nil {
		t.Fatalf("Read(kept-flow) error = %v, want the legacy record to be re-migrated", err)
	}
}

// Edge recovery is preset-dependent and now runs exactly once, at migration.
// Under the file store it re-ran on every read, so restoring a missing preset
// healed the record. It no longer does, which makes the migration the only
// moment the operator can act — and makes a fence message that says "restore
// the preset" actively misleading.
func TestMigrationReportsFlowsLeftGraphUnresolved(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	record := legacyRecord("preset-flow", now)
	record.PresetName = "custom"
	record.Phases = []FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
		{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
	}
	writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", record)

	warnings := captureBootstrapWarnings(t)
	// The preset is absent from config, so the graph cannot be rebuilt.
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	notice := strings.Join(*warnings, "\n")
	if !strings.Contains(notice, "preset-flow") || !strings.Contains(notice, "custom") {
		t.Fatalf("cutover notice = %q, want it to name the unresolved flow and its preset", notice)
	}
	if !strings.Contains(notice, "re-run the cutover") {
		t.Fatalf("cutover notice = %q, want it to state the only recovery that works", notice)
	}
	// Re-running the cutover works only while the database holds nothing but the
	// records just migrated. flows/ is frozen at migration time, so the same
	// advice read months later would discard every Flow created since — and this
	// text is written to a file that stays in the state root.
	assertRollbackIsBounded(t, "notice", notice)
	if strings.Contains(notice, "remove "+databasePathFor(root)) {
		t.Fatalf("cutover notice = %q, want it to say move aside rather than remove", notice)
	}

	// The notice must also survive on disk: stderr is taken by the alt screen
	// moments after NewStore returns.
	body, err := os.ReadFile(filepath.Join(root, migrationNoticeFilename))
	if err != nil {
		t.Fatalf("read migration notice: %v", err)
	}
	if !strings.Contains(string(body), "preset-flow") {
		t.Fatalf("migration notice file = %q, want it to name the unresolved flow", body)
	}

	// And the fence itself must not promise a remedy that cannot work.
	_, err = store.SetPhase(PhaseUpdate{FlowID: "preset-flow", PhaseID: "plan", Status: PhaseRunning})
	if err == nil {
		t.Fatal("SetPhase() error = nil, want the unresolved graph to fence phase mutation")
	}
	if strings.Contains(err.Error(), "restore preset \"custom\" or explicit depends_on before mutating phases") {
		t.Fatalf("SetPhase() error = %v, want it to stop claiming that restoring the preset alone heals the record", err)
	}
	if !strings.Contains(err.Error(), "cannot be\n\t\t rebuilt now") && !strings.Contains(err.Error(), "cannot be rebuilt now") {
		t.Fatalf("SetPhase() error = %v, want it to say recovery cannot run again", err)
	}
	// The fence fires on a database that has been in use, so it must never
	// suggest re-running the cutover: flows/ is frozen at migration time and
	// re-running would discard every Flow created since.
	for _, forbidden := range []string{"re-run the migration", "re-run the cutover", "remove " + databaseFilename} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("SetPhase() error = %v, want it not to suggest %q on an in-use database", err, forbidden)
		}
	}
}

// The fence must not recommend a rollback that destroys post-migration work.
// Following its advice has to leave Flows created after the cutover intact.
func TestFenceAdviceDoesNotDiscardPostMigrationFlows(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	record := legacyRecord("preset-flow", now)
	record.PresetName = "custom"
	record.Phases = []FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
		{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
	}
	writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", record)

	captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Work happens after the migration. flows/ never sees any of it.
	later, err := store.Create(FlowRecord{Title: "later", Instructions: "i", RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	_, fenceErr := store.SetPhase(PhaseUpdate{FlowID: "preset-flow", PhaseID: "plan", Status: PhaseRunning})
	if fenceErr == nil {
		t.Fatal("SetPhase() error = nil, want the fence")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Whatever the fence proposes, it must not be "throw the database away".
	if strings.Contains(fenceErr.Error(), databaseFilename) {
		t.Fatalf("fence error = %v, want it not to name the database as something to remove", fenceErr)
	}

	// Prove the hazard the message must avoid: re-running the cutover really
	// does drop post-migration Flows, which is why it is only offered in the
	// migration notice and never here.
	if err := os.Remove(filepath.Join(root, databaseFilename)); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(filepath.Join(root, databaseFilename) + suffix)
	}
	remigrated, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer remigrated.Close()
	if _, err := remigrated.Read(later.FlowID); err == nil {
		t.Fatal("Read(post-migration flow) error = nil after re-running the cutover; " +
			"if this ever passes, re-running is safe and the fence wording can be relaxed")
	}
}

func databasePathFor(root string) string {
	return filepath.Join(root, databaseFilename)
}

// proposesRebuildFromLegacy matches the INSTRUCTION, not one phrasing of it.
// The previous version of this test grepped for the literal "re-run the
// cutover", so a paragraph that said "re-migrate from flows/" instead walked
// straight past it — which is exactly how the instruction escaped through
// describeUnusableDatabase after the class was supposedly closed.
func proposesRebuildFromLegacy(text string) bool {
	for _, phrase := range []string{"re-run the cutover", "re-migrate", "rebuild from", "redo the cutover"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// assertRollbackIsBounded is the invariant: any user-facing string that
// proposes rebuilding the database from flows/ must say when that is safe.
// flows/ is frozen at migration time, so the instruction is right for minutes
// and destroys everything since at any later point.
func assertRollbackIsBounded(t *testing.T, label, text string) {
	t.Helper()
	if !proposesRebuildFromLegacy(text) {
		return
	}
	if !strings.Contains(text, "Do NOT do this later") ||
		!strings.Contains(text, "snapshot frozen at migration time") {
		t.Fatalf("%s proposes rebuilding from the legacy source without stating when that is safe:\n%s",
			label, text)
	}
}

// Fixing this one message at a time did not hold — it came back in the next
// paragraph written, twice. This asserts the class across every bootstrap
// message that can carry the instruction.
func TestEveryRollbackInstructionIsTimeBounded(t *testing.T) {
	// Errors, not just notices: describeUnusableDatabase is reachable on a
	// perfectly readable database whose schema shape is wrong (a dropped index),
	// where every post-migration Flow is intact and would be discarded.
	t.Run("unusable database error with legacy present", func(t *testing.T) {
		root := t.TempDir()
		now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		writeLegacyFlow(t, root, "migrated-flow", legacyRecord("migrated-flow", now))
		captureBootstrapWarnings(t)
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Create(FlowRecord{Title: "since", Instructions: "i", RepoPath: "/tmp/repo"}); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}

		// A readable database with live data, but the wrong schema shape.
		db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename),
			map[string][]string{"mode": {"rw"}}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("DROP INDEX idx_flows_status_updated"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		_, err = NewStore(StoreOptions{Root: root})
		if err == nil {
			t.Fatal("NewStore() error = nil, want the schema check to reject it")
		}
		assertRollbackIsBounded(t, "describeUnusableDatabase", err.Error())
	})

	notices := map[string]func(t *testing.T) []string{
		"fresh migration with an unresolved preset": func(t *testing.T) []string {
			root := t.TempDir()
			now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			record := legacyRecord("preset-flow", now)
			record.PresetName = "custom"
			record.Phases = []FlowPhase{
				{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
				{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
			}
			writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", record)
			warnings := captureBootstrapWarnings(t)
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			return *warnings
		},
		"resume that dropped a record": func(t *testing.T) []string {
			root := t.TempDir()
			now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			writeLegacyFlow(t, root, "staged-flow", legacyRecord("staged-flow", now))
			imported, err := readAndCanonicalizeLegacy(root, "flows", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := buildStagedDatabase(filepath.Join(root, stageFilename), imported.records); err != nil {
				t.Fatal(err)
			}
			writeLegacyFlow(t, root, "added-after-stage", legacyRecord("added-after-stage", now))
			warnings := captureBootstrapWarnings(t)
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			return *warnings
		},
	}

	for name, build := range notices {
		t.Run(name, func(t *testing.T) {
			notice := strings.Join(build(t), "\n")
			if !proposesRebuildFromLegacy(notice) {
				t.Fatalf("notice = %q, want it to propose the rollback for this scenario", notice)
			}
			assertRollbackIsBounded(t, "cutover notice", notice)
			if strings.Contains(notice, "remove "+databaseFilename) {
				t.Fatalf("notice says remove rather than move aside: %q", notice)
			}
		})
	}
}

// The migration would never have imported these, so they are not losses. A
// false "records were dropped" alarm sitting next to a rollback instruction is
// how an operator discards a good database for nothing.
func TestResumeDoesNotReportSkippableLegacyRecordsAsDropped(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyFlow(t, root, "good-flow", legacyRecord("good-flow", now))

	imported, err := readAndCanonicalizeLegacy(root, "flows", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := buildStagedDatabase(filepath.Join(root, stageFilename), imported.records); err != nil {
		t.Fatal(err)
	}

	// Neither of these would ever become a record: one has no meta.json, the
	// other's meta.json cannot be decoded.
	if err := os.MkdirAll(filepath.Join(root, "flows", "empty-flow"), 0o700); err != nil {
		t.Fatal(err)
	}
	brokenDir := filepath.Join(root, "flows", "broken-flow")
	if err := os.MkdirAll(brokenDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	warnings := captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	notice := strings.Join(*warnings, "\n")
	if strings.Contains(notice, "not in the promoted database") {
		t.Fatalf("resume notice = %q, want no dropped-record alarm for records the migration would skip", notice)
	}
	if strings.Contains(notice, "re-run the cutover") {
		t.Fatalf("resume notice = %q, want no rollback advice when nothing was actually dropped", notice)
	}
}

// A database from a newer build is not damaged, so it must not inherit the
// corrupt-database advice — following that would roll a live corpus back to its
// migration-day snapshot right after correctly being told to upgrade.
func TestNewerSchemaErrorCarriesNoRollbackAdvice(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyFlow(t, root, "migrated-flow", legacyRecord("migrated-flow", now))
	captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, databaseFilename)
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", databaseSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("NewStore() error = nil, want a newer schema to be rejected")
	}
	if !errors.Is(err, errDatabaseFromNewerBuild) {
		t.Fatalf("NewStore() error = %v, want it to classify as a newer-build database", err)
	}
	// flows/ is present, so the corrupt-database path would have appended
	// re-migration advice. It must not, because the database is fine.
	for _, forbidden := range []string{"re-migrate", "pre-migration Flows are still in", "move"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("NewStore() error = %v, want no recovery advice on a newer-build database (found %q)",
				err, forbidden)
		}
	}
}

// An empty database must say so rather than dump a column-set diff.
func TestEmptyDatabaseIsNamedNotDiffed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, databaseFilename), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("NewStore() error = nil, want an empty database to be rejected")
	}
	if !strings.Contains(err.Error(), "empty or was never initialized") {
		t.Fatalf("NewStore() error = %v, want it to name the real condition", err)
	}
	if strings.Contains(err.Error(), "want [flow_id") {
		t.Fatalf("NewStore() error = %v, want no raw column-set dump", err)
	}
}

// The fence keys off the graph_recovery marker, not off the edges, so the
// documented manual repair has to name both edits. This performs exactly what
// the error message and docs/config.md prescribe and asserts the Flow is usable
// afterwards — if this fails, the documented recipe is wrong again.
func TestDocumentedManualRepairClearsTheFence(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	record := legacyRecord("preset-flow", now)
	record.PresetName = "custom"
	record.Phases = []FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
		{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
	}
	writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", record)

	captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	fenceErr := func() error {
		_, err := store.SetPhase(PhaseUpdate{FlowID: "preset-flow", PhaseID: "plan", Status: PhaseRunning})
		return err
	}()
	if fenceErr == nil {
		t.Fatal("SetPhase() error = nil, want the fence")
	}
	// The message must name both edits, or the recipe below is undiscoverable.
	if !strings.Contains(fenceErr.Error(), "depends_on") ||
		!strings.Contains(fenceErr.Error(), GraphRecoveryMissingEdgesUnresolved) {
		t.Fatalf("fence error = %v, want it to name both the depends_on edit and the marker removal", fenceErr)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Perform the documented repair directly on the record JSON.
	path := filepath.Join(root, databaseFilename)
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	if err := db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", "preset-flow").Scan(&blob); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatal(err)
	}
	phases, ok := raw["phases"].([]any)
	if !ok {
		t.Fatalf("phases is %T", raw["phases"])
	}
	for _, phase := range phases {
		entry := phase.(map[string]any)
		if entry["phase_id"] == "build" {
			entry["depends_on"] = []any{"plan"}
		}
	}
	delete(raw, "graph_recovery")
	repaired, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", repaired, "preset-flow"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() after the documented repair error = %v", err)
	}
	defer reopened.Close()
	// The fence must be gone. Ordinary domain rules (transition legality,
	// readiness) still apply and are not what this test is about, so assert on
	// the fence specifically rather than on blanket success.
	_, err = reopened.SetPhase(PhaseUpdate{FlowID: "preset-flow", PhaseID: "plan", Status: PhaseRunning})
	if err != nil && strings.Contains(err.Error(), "unresolved missing dependencies") {
		t.Fatalf("SetPhase() after the documented repair error = %v; the repair documented in the fence "+
			"message and docs/config.md does not actually unblock the Flow", err)
	}
	// And a legal transition must now go through.
	if _, err := reopened.SetPhase(PhaseUpdate{
		FlowID: "preset-flow", PhaseID: "plan", Status: PhaseSkipped, Notes: "repaired by hand",
	}); err != nil {
		t.Fatalf("SetPhase(skipped) after the documented repair error = %v", err)
	}
}

// The resume path promotes a stage this process did not build, so it has no
// import accounting to carry over. Both facts that matter are still recoverable
// from the promoted database, and both are only actionable now: a crashed first
// migration that left Flows fenced would otherwise tell nobody, and a record
// added to flows/ after the stage was built is dropped by the promotion.
func TestResumedCutoverReportsFencedFlowsAndDroppedRecords(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fenced := legacyRecord("preset-flow", now)
	fenced.PresetName = "custom"
	fenced.Phases = []FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
		{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
	}
	writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", fenced)

	// Stage without promoting: the interrupted run's state. The preset is absent,
	// so the staged record carries the unresolved marker.
	imported, err := readAndCanonicalizeLegacy(root, "flows", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.unresolved) != 1 {
		t.Fatalf("unresolved = %v, want the missing preset to fence the record", imported.unresolved)
	}
	if err := buildStagedDatabase(filepath.Join(root, stageFilename), imported.records); err != nil {
		t.Fatal(err)
	}

	// An older file-store build adds a Flow to flows/ before the resume. The
	// promotion does not re-derive, so this record is silently absent afterwards.
	writeLegacyFlow(t, root, "added-after-stage", legacyRecord("added-after-stage", now))

	warnings := captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	if _, err := store.Read("added-after-stage"); err == nil {
		t.Fatal("Read(added-after-stage) error = nil; if the resume now re-derives, this test is obsolete")
	}

	notice := strings.Join(*warnings, "\n")
	if !strings.Contains(notice, "preset-flow") {
		t.Fatalf("resume notice = %q, want it to name the Flow left with an unresolved graph", notice)
	}
	if !strings.Contains(notice, "not in the promoted database") ||
		!strings.Contains(notice, "added-after-stage") {
		t.Fatalf("resume notice = %q, want it to name the record the promotion did not import", notice)
	}
	assertRollbackIsBounded(t, "notice", notice)
	body, err := os.ReadFile(filepath.Join(root, migrationNoticeFilename))
	if err != nil {
		t.Fatalf("read migration notice: %v", err)
	}
	if !strings.Contains(string(body), "preset-flow") {
		t.Fatalf("migration notice file = %q, want the resume report persisted too", body)
	}
}

// An operator who followed the migration notice and removed flows/ is the one
// most likely to never hear that a crashed cutover left Flows fenced. The
// fenced-flow report comes from the promoted database and needs no legacy
// source, so it must not be gated on one — and with flows/ gone the rollback is
// impossible, so the notice must offer the manual repair instead.
func TestResumeReportsFencedFlowsWithoutTheLegacySource(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	record := legacyRecord("preset-flow", now)
	record.PresetName = "custom"
	record.Phases = []FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhasePending, Order: 1, CreatedAt: now, UpdatedAt: now},
		{PhaseID: "build", Title: "Build", Kind: KindImplementation, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
	}
	writeLegacyFlowWithoutDependsOn(t, root, "preset-flow", record)

	imported, err := readAndCanonicalizeLegacy(root, "flows", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.unresolved) != 1 {
		t.Fatalf("unresolved = %v, want the missing preset to fence the record", imported.unresolved)
	}
	if err := buildStagedDatabase(filepath.Join(root, stageFilename), imported.records); err != nil {
		t.Fatal(err)
	}
	// The operator cleans up flows/ before the resume.
	if err := os.RemoveAll(filepath.Join(root, "flows")); err != nil {
		t.Fatal(err)
	}

	warnings := captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	notice := strings.Join(*warnings, "\n")
	if !strings.Contains(notice, "preset-flow") {
		t.Fatalf("resume notice = %q, want fenced Flows named even with no legacy source", notice)
	}
	// No legacy source means no rollback is possible, so none may be offered.
	if proposesRebuildFromLegacy(notice) {
		t.Fatalf("resume notice = %q, want no rollback advice when the legacy source is gone", notice)
	}
	// The manual repair must name both edits, or it reads as complete and is not.
	if !strings.Contains(notice, "depends_on") ||
		!strings.Contains(notice, GraphRecoveryMissingEdgesUnresolved) {
		t.Fatalf("resume notice = %q, want the manual repair to name both edits", notice)
	}
}

// The notice is written into a 0700 root, but nothing in this file should be
// the one primitive that writes through a symlink it did not create.
func TestMigrationNoticeDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, migrationNoticeFilename)); err != nil {
		t.Fatal(err)
	}

	writeMigrationNotice(root, "should not be written through the symlink")

	body, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "original" {
		t.Fatalf("symlink target = %q, want it untouched", body)
	}
}

// discardObsoleteStage runs on every healthy startup, so a stray non-regular
// path at a stage name must not be able to refuse one.
func TestStrayStagePathDoesNotBlockStartup(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Create(FlowRecord{Title: "keep", Instructions: "i", RepoPath: "/tmp/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// A botched rsync, a backup tool, or a curious user.
	if err := os.Mkdir(filepath.Join(root, stageFilename), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(root, stageFilename+"-wal")); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() with a stray stage path error = %v, want a healthy database to still open", err)
	}
	defer reopened.Close()
	records, err := reopened.List(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want the existing Flow intact", len(records))
	}
}

// A skipped legacy record used to come back when its JSON was repaired. After
// cutover the legacy tree is never read again, so the skip is permanent and the
// migration is the last moment it can be reported.
func TestMigrationReportsSkippedLegacyRecords(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyFlow(t, root, "good-flow", legacyRecord("good-flow", now))

	brokenDir := filepath.Join(root, "flows", "broken-flow")
	if err := os.MkdirAll(brokenDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	warnings := captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	notice := strings.Join(*warnings, "\n")
	if !strings.Contains(notice, "broken-flow") {
		t.Fatalf("cutover notice = %q, want it to name the skipped record", notice)
	}
	// A migration that imports 1 of 2 must not read like one that imported everything.
	if !strings.Contains(notice, "migrated 1 of 2") {
		t.Fatalf("cutover notice = %q, want the migrated/found counts", notice)
	}
	records, err := store.List(FlowFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].FlowID != "good-flow" {
		t.Fatalf("records = %#v, want only good-flow", records)
	}
}

// Without a version stamp, an older binary reading a newer database reports a
// raw column-set diff, which reads as corruption rather than "upgrade approach".
func TestDatabaseFromANewerApproachIsRejectedClearly(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, databaseFilename)
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	var stamped int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if stamped != databaseSchemaVersion {
		t.Fatalf("user_version = %d, want %d stamped at creation", stamped, databaseSchemaVersion)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", databaseSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("NewStore() error = nil, want a newer schema to be rejected")
	}
	if !strings.Contains(err.Error(), "newer version of approach") {
		t.Fatalf("NewStore() error = %v, want it to name the real cause", err)
	}
}

// Close is documented as safe to call more than once, and a Store handed to
// several subsystems will be closed by whichever shuts down first.
func TestStoreCloseIsIdempotent(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want Close to be idempotent", err)
	}
}

// cleanupReservedSQLiteFiles removes approach.db itself, so it must refuse to
// run on a root that already has an authoritative database. Today the single
// caller is gated by !state.database under the bootstrap lock; this pins the
// guard so a future caller cannot quietly delete every Flow.
func TestCleanupReservedSQLiteFilesRefusesToDeleteAuthoritativeDatabase(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Create(FlowRecord{Title: "keep", Instructions: "i", RepoPath: "/tmp/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := cleanupReservedSQLiteFiles(root); err == nil {
		t.Fatal("cleanupReservedSQLiteFiles() error = nil, want it to refuse an authoritative database")
	}
	if _, err := os.Stat(filepath.Join(root, databaseFilename)); err != nil {
		t.Fatalf("authoritative database was removed: %v", err)
	}
}

// A root migrated by the older build that renamed flows/ keeps its pre-migration
// corpus in flows.legacy/. If that database later fails validation, an unusable-
// database error that inspects only flows/ tells the operator their records are
// gone and invites them to rebuild an empty store, while the whole corpus is
// still sitting next to it under the other name.
func TestUnusableDatabaseErrorNamesTheInheritedTombstone(t *testing.T) {
	// A root name carrying shell metacharacters is legal on disk and reachable
	// through configuration, so it also pins that the copyable command is quoted
	// for a shell rather than for Go.
	root := renameMigratedRootWithUnusableDatabase(t, "state$root")
	legacyPath := filepath.Join(root, "flows")
	tombstonePath := filepath.Join(root, "flows.legacy")

	_, err := NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("NewStore() error = nil, want the schema check to reject it")
	}
	message := err.Error()
	if !strings.Contains(message, tombstonePath) {
		t.Fatalf("NewStore() error = %q, want it to name the retained %q", message, tombstonePath)
	}
	if strings.Contains(message, "rebuild an empty one") {
		t.Fatalf("NewStore() error = %q, want it to stop offering an empty rebuild while the"+
			" pre-migration corpus is still in %q", message, tombstonePath)
	}
	wantCommand := fmt.Sprintf("mv '%s' '%s'", tombstonePath, legacyPath)
	if !strings.Contains(message, wantCommand) {
		t.Fatalf("NewStore() error = %q, want the shell-quoted restore command %q", message, wantCommand)
	}
	assertRollbackIsBounded(t, "describeUnusableDatabase with an inherited tombstone", message)
}

// The rename advice is only safe when flows/ is absent. If the path exists as
// something that is not a usable directory, `mv` either fails or buries the sole
// retained corpus inside it, so the message must not offer the rename at all.
func TestUnusableDatabaseErrorWithheldWhenLegacyPathIsUnusable(t *testing.T) {
	root := renameMigratedRootWithUnusableDatabase(t, "occupied")
	if err := os.WriteFile(filepath.Join(root, "flows"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("NewStore() error = nil, want the schema check to reject it")
	}
	if strings.Contains(err.Error(), "mv ") {
		t.Fatalf("NewStore() error = %q, want no rename advice while %q is not a usable directory",
			err, filepath.Join(root, "flows"))
	}
}

// renameMigratedRootWithUnusableDatabase reproduces a root migrated by the older
// build that renamed flows/: an authoritative database that fails validation,
// beside the retained corpus under its tombstone name. It returns the canonical
// root, because bootstrap messages name canonical paths.
func renameMigratedRootWithUnusableDatabase(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyFlow(t, root, "migrated-flow", legacyRecord("migrated-flow", now))
	captureBootstrapWarnings(t)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(canonicalRoot, "flows"), filepath.Join(canonicalRoot, "flows.legacy")); err != nil {
		t.Fatal(err)
	}

	// A readable database with live data, but the wrong schema shape.
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(canonicalRoot, databaseFilename),
		map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP INDEX idx_flows_status_updated"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return canonicalRoot
}
