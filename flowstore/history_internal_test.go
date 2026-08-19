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
