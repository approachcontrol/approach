package flowstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSidecarRaw(t *testing.T, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAMigrationAppendsOneHistoryEntryNamingItsBackup(t *testing.T) {
	root := predecessorRoot(t)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	sidecar, ok := readSidecar(root)
	if !ok {
		t.Fatal("no sidecar after a migration")
	}
	if len(sidecar.History) != 1 {
		t.Fatalf("history = %#v, want exactly one entry", sidecar.History)
	}
	entry := sidecar.History[0]
	if entry.FromVersion == nil || *entry.FromVersion != databaseSchemaVersion-1 {
		t.Fatalf("from_version = %v", entry.FromVersion)
	}
	if entry.ToVersion != databaseSchemaVersion {
		t.Fatalf("to_version = %d", entry.ToVersion)
	}
	if entry.GenerationID != sidecar.GenerationID {
		t.Fatalf("history generation %q does not match the sidecar's %q", entry.GenerationID, sidecar.GenerationID)
	}
	if entry.BackupPath == nil {
		t.Fatal("history entry names no backup")
	}
	if _, err := os.Lstat(*entry.BackupPath); err != nil {
		t.Fatalf("history names a backup that is not on disk: %v", err)
	}
}

func TestTwoMigrationsAppendInOrder(t *testing.T) {
	root := predecessorRoot(t)
	first, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Stamp back and migrate again: a second real migration of the same root.
	stampUserVersion(t, root, databaseSchemaVersion-1)
	second, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	sidecar, _ := readSidecar(root)
	if len(sidecar.History) != 2 {
		t.Fatalf("history = %#v, want two entries", sidecar.History)
	}
	if sidecar.History[0].At > sidecar.History[1].At {
		t.Fatalf("history is not append-ordered: %v then %v", sidecar.History[0].At, sidecar.History[1].At)
	}
	if sidecar.History[1].GenerationID != sidecar.GenerationID {
		t.Fatal("the newest history entry does not carry the current generation")
	}
}

// TestACrashBetweenCommitAndSidecarWriteIsRepairedWithAReconstructedEntry: the
// database advanced and nothing recorded it. The repair must say so rather than
// invent the fields it cannot know.
func TestACrashBetweenCommitAndSidecarWriteIsRepairedWithAReconstructedEntry(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// A sidecar left behind by the previous generation, in the shape the
	// previous build wrote: user_version moved, the sidecar did not.
	writeLegacyV1Sidecar(t, root, databaseSchemaVersion-1, "aabbccddeeff0011")
	before, _ := readSidecar(root)

	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	sidecar, _ := readSidecar(root)
	if sidecar.PhysicalVersion != databaseSchemaVersion {
		t.Fatalf("physical_version = %d, want the repaired %d", sidecar.PhysicalVersion, databaseSchemaVersion)
	}
	if sidecar.GenerationID != before.GenerationID {
		t.Fatalf("the repair discarded generation_id %q", before.GenerationID)
	}
	if len(sidecar.History) != 1 {
		t.Fatalf("history = %#v, want one reconstructed entry", sidecar.History)
	}
	entry := sidecar.History[0]
	if entry.Provenance != sidecarProvenanceReconstructed {
		t.Fatalf("provenance = %q", entry.Provenance)
	}
	// The fields a reconstruction cannot know are explicitly null, never guessed.
	if entry.FromVersion != nil {
		t.Fatalf("from_version = %v, want null on a reconstructed entry", *entry.FromVersion)
	}
	if entry.BackupPath != nil {
		t.Fatalf("backup_path = %v, want null on a reconstructed entry", *entry.BackupPath)
	}
}

// TestAUnitTwoShapedSidecarIsUpgradedWithoutLosingItsGeneration: the generation
// names existing backups, so an upgrade that dropped it would orphan them.
func TestAUnitTwoShapedSidecarIsUpgradedWithoutLosingItsGeneration(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	writeLegacyV1Sidecar(t, root, databaseSchemaVersion-1, "0011223344556677")

	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	sidecar, ok := readSidecar(root)
	if !ok {
		t.Fatal("the upgraded sidecar is unreadable")
	}
	if sidecar.SchemaVersion != sidecarSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", sidecar.SchemaVersion, sidecarSchemaVersion)
	}
	if sidecar.GenerationID != "0011223344556677" {
		t.Fatalf("generation_id = %q, want the one the v1 sidecar carried", sidecar.GenerationID)
	}
}

// TestAV2SidecarReadsAsAbsentThroughTheV1ValidationPath is the compatibility
// claim the sidecar bump rests on, pinned rather than assumed: an older build
// treats an unrecognised sidecar as absent, and reconcileSidecar's !present
// branch returns without rewriting, so it neither believes nor destroys the
// newer record.
func TestAV2SidecarReadsAsAbsentThroughTheV1ValidationPath(t *testing.T) {
	root := t.TempDir()
	if err := writeSidecar(root, databaseSchemaVersion, sidecarProvenanceMigrated,
		newGenerationID(), migrationOutcome{Migrated: true, FromVersion: databaseSchemaVersion - 1}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var sidecar databaseSidecar
	if err := json.Unmarshal(data, &sidecar); err != nil {
		t.Fatal(err)
	}
	// The predecessor's exact rule, restated: it accepted only its own version.
	if validSidecarForVersion(sidecar, 1) {
		t.Fatal("a v1 build would have believed a v2 sidecar")
	}
	// And it must not be rewritten: this is the !present branch's whole job.
	before, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := reconcileSidecar(root, databaseSchemaVersion, migrationOutcome{}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a matching-version open rewrote the sidecar")
	}
}

func TestHistoryTruncationSetsItsFlag(t *testing.T) {
	root := t.TempDir()
	sidecar := databaseSidecar{
		SchemaVersion:   sidecarSchemaVersion,
		GenerationID:    newGenerationID(),
		PhysicalVersion: databaseSchemaVersion,
	}
	for i := 0; i < sidecarHistoryLimit+5; i++ {
		sidecar.History = append(sidecar.History, sidecarHistoryEntry{
			At: "2026-08-18T00:00:00Z", ToVersion: databaseSchemaVersion,
			GenerationID: "seed", Provenance: sidecarProvenanceMigrated,
		})
	}
	trimmed, truncated := trimSidecarHistory(sidecar.History)
	if !truncated {
		t.Fatal("trimming did not report truncation")
	}
	if len(trimmed) != sidecarHistoryLimit {
		t.Fatalf("history length = %d, want the cap %d", len(trimmed), sidecarHistoryLimit)
	}
	// Oldest first out: the newest entries are the ones an operator needs.
	if trimmed[len(trimmed)-1].GenerationID != "seed" {
		t.Fatal("trimming kept the wrong end")
	}
	_ = root
}

func writeLegacyV1Sidecar(t *testing.T, root string, physical int64, generation string) {
	t.Helper()
	raw := map[string]any{
		"schema_version":        1,
		"generation_id":         generation,
		"physical_version":      physical,
		"min_reader_generation": physical,
		"min_writer_generation": physical,
		"migrated_by":           map[string]any{"build_version": "v0.10.3", "commit": "abc", "at": "2026-08-01T00:00:00Z"},
		"provenance":            sidecarProvenanceMigrated,
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath(root), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPostCommitValidationFailureNamesTheBackupAndTheRestoreCommand: a
// migration that produced an unusable database must say so, and say what undoes
// it, rather than returning success.
func TestPostCommitValidationFailureNamesTheBackupAndTheRestoreCommand(t *testing.T) {
	root := predecessorRoot(t)
	original := postMigrationValidation
	t.Cleanup(func() { postMigrationValidation = original })
	postMigrationValidation = func(*sql.DB, int64, map[string]bool) error {
		return errors.New("the migrated database is missing an index")
	}
	_, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err == nil {
		t.Fatal("a migration whose post-commit validation failed returned success")
	}
	message := err.Error()
	backups := filepath.Join(root, backupDirName)
	if !strings.Contains(message, backups) {
		t.Fatalf("failure %q does not name the backup directory %q", message, backups)
	}
	if !strings.Contains(message, "approach db restore --backup ") {
		t.Fatalf("failure %q does not name the restore command", message)
	}
}

// TestPostCommitValidationFailureLeavesTheAdvertisedRestoreUsable: the refusal
// names one exact recovery command, and on a root that has migrated before, the
// history is what decides whether `db restore` accepts it. A committed
// migration that goes unrecorded leaves the previous migration's backup as the
// newest entry, and the command in the error is then refused as a generation
// mismatch — advice that does not work is worse than none.
func TestPostCommitValidationFailureLeavesTheAdvertisedRestoreUsable(t *testing.T) {
	root := predecessorRoot(t)
	first, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	stampUserVersion(t, root, databaseSchemaVersion-1)
	original := postMigrationValidation
	t.Cleanup(func() { postMigrationValidation = original })
	postMigrationValidation = func(*sql.DB, int64, map[string]bool) error {
		return errors.New("the migrated database is missing an index")
	}
	_, err = NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err == nil {
		t.Fatal("a migration whose post-commit validation failed returned success")
	}
	advertised := restoreCommandBackup(t, err.Error())

	postMigrationValidation = original
	if _, restoreErr := Restore(RestoreOptions{Root: root, BackupPath: advertised}); restoreErr != nil {
		t.Fatalf("the restore the refusal advertised was itself refused: %v", restoreErr)
	}
	if storedSchemaVersion(t, root) != databaseSchemaVersion-1 {
		t.Fatal("the advertised restore did not put the predecessor back")
	}
}

// restoreCommandBackup extracts the backup path from the refusal's recovery
// command, so the test runs the command an operator would copy.
func restoreCommandBackup(t *testing.T, message string) string {
	t.Helper()
	const marker = "approach db restore --backup "
	at := strings.Index(message, marker)
	if at < 0 {
		t.Fatalf("failure %q does not name the restore command", message)
	}
	rest := strings.TrimSpace(message[at+len(marker):])
	if quote := rest[:1]; quote == `"` || quote == "'" {
		end := strings.Index(rest[1:], quote)
		if end < 0 {
			t.Fatalf("failure %q leaves the backup path unterminated", message)
		}
		return rest[1 : 1+end]
	}
	return strings.Fields(rest)[0]
}

// TestTheSampleQueryFitsAV1PredecessorsColumns: bead_id and epic_id arrive in
// v2. Naming them against a v1 database does not read empty values — it fails
// the query, which empties the leniency baseline and turns that root's
// pre-existing damage into a post-commit migration failure.
func TestTheSampleQueryFitsAV1PredecessorsColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE flows (
		flow_id TEXT PRIMARY KEY,
		repo_path TEXT NOT NULL,
		status TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		record BLOB NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO flows VALUES('f', '/repo', 'active', '2026-08-18T00:00:00Z', X'7B7D')"); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(migratedRecordSampleQuery(1), migratedRecordSampleSize)
	if err != nil {
		t.Fatalf("the v1 sample query does not run against a v1 database: %v", err)
	}
	if !rows.Next() {
		t.Fatal("the v1 sample query returned no rows")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	// The current schema's query genuinely cannot run there, which is why the
	// version has to pick the columns.
	if _, err := db.Query(migratedRecordSampleQuery(databaseSchemaVersion), migratedRecordSampleSize); err == nil {
		t.Fatal("the current-schema query unexpectedly ran against a v1 database")
	}
	// A baseline built from it is a real answer rather than the empty map a
	// failed query yields.
	if undecodable := sampleUndecodableFlows(db, 1); len(undecodable) != 1 || !undecodable["f"] {
		t.Fatalf("baseline = %v, want the undecodable v1 record", undecodable)
	}
}
