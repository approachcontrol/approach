package flowstore

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

func inspectOrFail(t *testing.T, root string) InspectReport {
	t.Helper()
	report, err := Inspect(root)
	if err != nil {
		t.Fatalf("Inspect(%q) error = %v", root, err)
	}
	return report
}

func currentRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertUnreadable(t *testing.T, report InspectReport, tier string) {
	t.Helper()
	if report.Tier != tier {
		t.Fatalf("tier = %q, want %q (reason %v)", report.Tier, tier, stringOrNil(report.Reason))
	}
	if report.Readable {
		t.Fatalf("readable = true, want false in tier %q", tier)
	}
	if report.Reason == nil || report.NextAction == nil {
		t.Fatalf("tier %q must set both reason and next_action, got %v / %v",
			tier, stringOrNil(report.Reason), stringOrNil(report.NextAction))
	}
	if report.SidecarStale != nil {
		t.Fatalf("sidecar_stale = %v in tier %q; it is a comparison and must be null without a user_version",
			*report.SidecarStale, tier)
	}
}

func stringOrNil(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func TestInspectReportsAFreshRootAsMissing(t *testing.T) {
	report := inspectOrFail(t, t.TempDir())
	assertUnreadable(t, report, TierMissing)
	if *report.Reason != "no flow database" {
		t.Fatalf("reason = %q", *report.Reason)
	}
	if *report.NextAction == *report.Reason {
		t.Fatal("next_action must be an action, not a restatement of reason")
	}
	if report.GenerationID != nil || report.MinReaderGeneration != nil || report.MinWriterGeneration != nil {
		t.Fatal("a never-migrated root must report null generations")
	}
}

func TestInspectTreatsAnAbsentRootAsMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	report := inspectOrFail(t, root)
	assertUnreadable(t, report, TierMissing)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("Inspect created the root it was asked about")
	}
}

func TestInspectDistinguishesALegacyCorpusFromAnInterruptedCutover(t *testing.T) {
	legacyRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(legacyRoot, "flows"), artifacts.DirPerm); err != nil {
		t.Fatal(err)
	}
	legacy := inspectOrFail(t, legacyRoot)
	assertUnreadable(t, legacy, TierMissing)
	if *legacy.Reason != "legacy flow corpus not yet imported" {
		t.Fatalf("reason = %q", *legacy.Reason)
	}

	stageRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(stageRoot, stageFilename), []byte("staged"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	stage := inspectOrFail(t, stageRoot)
	assertUnreadable(t, stage, TierMissing)
	if *stage.Reason != "interrupted cutover" {
		t.Fatalf("reason = %q", *stage.Reason)
	}
	if *stage.NextAction != "run 'approach db migrate'" {
		t.Fatalf("next_action = %q", *stage.NextAction)
	}
}

func TestInspectReportsANonRegularDatabasePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, databaseFilename), artifacts.DirPerm); err != nil {
		t.Fatal(err)
	}
	report := inspectOrFail(t, root)
	assertUnreadable(t, report, TierNotADatabase)
	if !strings.Contains(*report.Reason, "is a directory, not a regular file") {
		t.Fatalf("reason = %q", *report.Reason)
	}
}

func TestInspectOpensACurrentDatabase(t *testing.T) {
	root := currentRoot(t)
	report := inspectOrFail(t, root)
	if report.Tier != TierOpen || !report.Readable {
		t.Fatalf("tier = %q readable = %v", report.Tier, report.Readable)
	}
	if report.UserVersion == nil || *report.UserVersion != databaseSchemaVersion {
		t.Fatalf("user_version = %v", report.UserVersion)
	}
	if report.JournalMode == nil || *report.JournalMode != "wal" {
		t.Fatalf("journal_mode = %v", report.JournalMode)
	}
	if report.Reason != nil || report.NextAction != nil {
		t.Fatal("a readable report must leave reason and next_action null")
	}
	if report.SidecarStale == nil || *report.SidecarStale {
		t.Fatalf("sidecar_stale = %v, want false on a root with no sidecar to disagree", report.SidecarStale)
	}
	if report.Executable.Schema != databaseSchemaVersion {
		t.Fatalf("executable.schema = %d", report.Executable.Schema)
	}
}

// Acceptance criterion 3: usable against a future schema. `db inspect` must
// never refuse on the input it exists to diagnose.
func TestInspectReadsAFutureSchemaWithoutRefusing(t *testing.T) {
	root := t.TempDir()
	stampUserVersion(t, root, 99)
	report := inspectOrFail(t, root)
	if report.Tier != TierOpen || !report.Readable {
		t.Fatalf("tier = %q readable = %v, want an open report on a future schema", report.Tier, report.Readable)
	}
	if report.UserVersion == nil || *report.UserVersion != 99 {
		t.Fatalf("user_version = %v, want 99", report.UserVersion)
	}
}

// The immutable=1 regression guard: an uncheckpointed WAL must not be hidden
// behind a stale checkpointed view.
func TestInspectSeesUncheckpointedWALContent(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	backend := store.backend.(*sqliteBackend)
	if _, err := backend.db.Exec("PRAGMA user_version = 6"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(FlowRecord{
		SchemaVersion: schemaVersion, Title: "WAL", Instructions: "WAL.", RepoPath: filepath.Join(root, "repo"),
	}); err != nil {
		t.Fatal(err)
	}
	report := inspectOrFail(t, root)
	if report.Tier != TierOpen {
		t.Fatalf("tier = %q", report.Tier)
	}
	if !report.WAL.Present || !report.WAL.Dirty {
		t.Fatalf("wal = %+v, want a present and dirty log", report.WAL)
	}
}

func TestInspectReportsACleanDatabaseInAReadOnlyDirectoryAsNotWritable(t *testing.T) {
	root := currentRoot(t)
	// Clean: no dirty WAL. SQLite answers SQLITE_READONLY_DIRECTORY here.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(filepath.Join(root, databaseFilename) + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, artifacts.DirPerm) })

	report := inspectOrFail(t, root)
	assertUnreadable(t, report, TierNotWritable)
	if !strings.Contains(*report.NextAction, "write access") {
		t.Fatalf("next_action = %q", *report.NextAction)
	}
	if report.DirectoryMode == nil || *report.DirectoryMode != "0500" {
		t.Fatalf("directory_mode = %v", report.DirectoryMode)
	}
}

// The corrected predicate. A crashed database in a read-only directory answers
// SQLITE_CANTOPEN, not SQLITE_READONLY_DIRECTORY, because SQLite cannot create
// the -shm it needs to read the WAL. Under a rule keyed only on 1544 this would
// be reported as corruption.
func TestInspectReportsACrashedDatabaseInAReadOnlyDirectoryAsNotWritable(t *testing.T) {
	root := currentRoot(t)
	path := filepath.Join(root, databaseFilename)
	if err := os.WriteFile(path+"-wal", []byte(strings.Repeat("w", 4096)), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + "-shm"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, artifacts.DirPerm) })

	report := inspectOrFail(t, root)
	assertUnreadable(t, report, TierNotWritable)
	if report.Tier == TierHeader {
		t.Fatal("a crashed database in a read-only directory was reported as a corruption diagnosis")
	}
}

func TestInspectReportsATruncatedDatabaseAsMalformed(t *testing.T) {
	root := currentRoot(t)
	path := filepath.Join(root, databaseFilename)
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Truncate(path, 40); err != nil {
		t.Fatal(err)
	}
	report := inspectOrFail(t, root)
	assertUnreadable(t, report, TierMalformed)
	if !strings.Contains(*report.NextAction, backupDirName) {
		t.Fatalf("next_action = %q, want the backup directory", *report.NextAction)
	}
}

func TestInspectReportsGarbageAsNotADatabase(t *testing.T) {
	for name, body := range map[string][]byte{
		"20 bytes":            []byte("not a database at al"),
		"large without magic": []byte(strings.Repeat("z", 4096)),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, databaseFilename), body, artifacts.FilePerm); err != nil {
				t.Fatal(err)
			}
			report := inspectOrFail(t, root)
			assertUnreadable(t, report, TierNotADatabase)
			if report.UserVersion != nil || report.CheckpointedUserVersion != nil {
				t.Fatal("a non-database must never be reported with a fabricated version")
			}
		})
	}
}

// A locked database is the input that reaches the residual tier, and it is the
// one the tier's own wording describes: SQLite answers SQLITE_BUSY, so the
// header is all Inspect can read, and the next action is to look again once the
// database is free.
//
// The plan's suggested fixture — a header-valid file with an invalid page-size
// field — was measured here to return code 26 and land in not_a_database, so it
// would not have exercised this tier at all.
func TestInspectFallsBackToTheHeaderOnALockedDatabase(t *testing.T) {
	root := currentRoot(t)
	path := filepath.Join(root, databaseFilename)
	locker, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Close() }()
	if _, err := locker.Exec("PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE; CREATE TABLE inspect_lock(x)"); err != nil {
		t.Fatalf("hold an exclusive lock: %v", err)
	}

	report := inspectOrFail(t, root)
	if report.Tier != TierHeader {
		t.Fatalf("tier = %q, want %q (reason %v)", report.Tier, TierHeader, stringOrNil(report.Reason))
	}
	if report.CheckpointedUserVersion == nil || *report.CheckpointedUserVersion != databaseSchemaVersion {
		t.Fatalf("checkpointed_user_version = %v, want %d", report.CheckpointedUserVersion, databaseSchemaVersion)
	}
	if report.Reason == nil || *report.Reason != "readable only as a checkpointed header" {
		t.Fatalf("reason = %v", stringOrNil(report.Reason))
	}
	if report.NextAction == nil || !strings.Contains(*report.NextAction, "after freeing the database") {
		t.Fatalf("next_action = %v", stringOrNil(report.NextAction))
	}
	if report.UserVersion != nil {
		t.Fatal("the header tier must not report a live user_version")
	}
}

func TestInspectReportsAnUnreadableDatabaseFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the file mode")
	}
	root := currentRoot(t)
	path := filepath.Join(root, databaseFilename)
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, artifacts.FilePerm) })

	report := inspectOrFail(t, root)
	assertUnreadable(t, report, TierNotWritable)
	if !strings.Contains(*report.NextAction, "read access") {
		t.Fatalf("next_action = %q, want read access advice", *report.NextAction)
	}
}

// Inspect is specified never to take the bootstrap lock, so it must answer
// while a migration holds it rather than waiting out bootstrapLockTimeout.
func TestInspectDoesNotWaitOnTheBootstrapLock(t *testing.T) {
	root := currentRoot(t)
	release, err := artifacts.AcquireFileLockNoFollow(
		filepath.Join(root, bootstrapLockFilename), "test bootstrap lock", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	done := make(chan InspectReport, 1)
	go func() {
		report, err := Inspect(root)
		if err != nil {
			t.Errorf("Inspect() error = %v", err)
		}
		done <- report
	}()
	select {
	case report := <-done:
		if report.Tier != TierOpen {
			t.Fatalf("tier = %q, want %q", report.Tier, TierOpen)
		}
		if report.MigrationOwner == nil || report.MigrationOwner.Verified {
			t.Fatalf("migration_owner = %+v, want an unverified PID", report.MigrationOwner)
		}
		if report.MigrationOwner.PID != os.Getpid() {
			t.Fatalf("migration_owner.pid = %d, want %d", report.MigrationOwner.PID, os.Getpid())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Inspect blocked on the bootstrap lock (bootstrapLockTimeout is %s)", bootstrapLockTimeout)
	}
}

// The report's keys are a contract with `approach db inspect --json`, so they
// are pinned here rather than left to whatever the struct happens to emit.
func TestInspectReportCarriesTheDocumentedJSONKeys(t *testing.T) {
	report := inspectOrFail(t, currentRoot(t))
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"schema_version", "path", "tier", "readable", "user_version",
		"checkpointed_user_version", "wal", "journal_mode", "directory_mode",
		"generation_id", "min_reader_generation", "min_writer_generation",
		"sidecar_stale", "executable", "migration_owner", "warnings",
		"reason", "next_action",
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("report is missing %q: %s", key, data)
		}
	}
	if len(decoded) != len(want) {
		t.Fatalf("report has %d keys, want %d: %s", len(decoded), len(want), data)
	}
	wal, ok := decoded["wal"].(map[string]any)
	if !ok {
		t.Fatalf("wal is not an object: %s", data)
	}
	for _, key := range []string{"present", "shm_present", "dirty"} {
		if _, ok := wal[key]; !ok {
			t.Fatalf("wal is missing %q: %s", key, data)
		}
	}
	if _, ok := decoded["warnings"].([]any); !ok {
		t.Fatalf("warnings must be an array, not null: %s", data)
	}
}
