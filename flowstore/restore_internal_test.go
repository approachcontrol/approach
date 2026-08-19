package flowstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// migratedRootWithBackup stages the state a restore acts on: a root that was
// migrated, so a verified pre-migration backup exists and the sidecar carries
// the generation that migration produced.
func migratedRootWithBackup(t *testing.T) (root, backup string) {
	t.Helper()
	root = predecessorRoot(t)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	sidecar, ok := readSidecar(root)
	if !ok || len(sidecar.History) != 1 || sidecar.History[0].BackupPath == nil {
		t.Fatalf("migration left no backup in its history: %#v", sidecar)
	}
	return root, *sidecar.History[0].BackupPath
}

func TestRestoreReplacesTheDatabaseWithTheBackup(t *testing.T) {
	root, backup := migratedRootWithBackup(t)

	result, err := Restore(RestoreOptions{Root: root, BackupPath: backup})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if result.UserVersion != databaseSchemaVersion-1 {
		t.Fatalf("restored user_version = %d, want the backup's %d", result.UserVersion, databaseSchemaVersion-1)
	}
	if storedSchemaVersion(t, root) != databaseSchemaVersion-1 {
		t.Fatal("the database on disk is not the backup's generation")
	}
	// The pre-restore copy exists and opens, so the restore is itself
	// reversible.
	if result.PreRestoreBackup == "" {
		t.Fatal("no pre-restore copy was recorded")
	}
	if _, err := os.Lstat(result.PreRestoreBackup); err != nil {
		t.Fatalf("the pre-restore copy is not on disk: %v", err)
	}
	// And the sidecar records what happened.
	sidecar, ok := readSidecar(root)
	if !ok {
		t.Fatal("no sidecar after a restore")
	}
	if sidecar.Provenance != sidecarProvenanceRestored {
		t.Fatalf("provenance = %q, want %q", sidecar.Provenance, sidecarProvenanceRestored)
	}
	entry := sidecar.History[len(sidecar.History)-1]
	if entry.Provenance != sidecarProvenanceRestored {
		t.Fatalf("newest history entry = %#v", entry)
	}
	if entry.BackupPath == nil || *entry.BackupPath != backup {
		t.Fatalf("the history entry does not name the source backup: %#v", entry)
	}
	// The restored database is openable by the migrator, which re-advances it.
	// That is the point: a restore returns the corpus, not a dead end.
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("the restored database does not open: %v", err)
	}
	defer store.Close()
}

func TestRestoreRemovesStaleWALAndSHM(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	database := filepath.Join(root, databaseFilename)
	// A WAL from the database being replaced. Left in place it would shadow
	// the restored file with the previous one's uncheckpointed content.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(database+suffix, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Restore(RestoreOptions{Root: root, BackupPath: backup}); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(database + suffix); !os.IsNotExist(err) {
			t.Fatalf("%s survived the restore: %v", database+suffix, err)
		}
	}
}

func TestRestoreRefusesABackupThatFailsIntegrityCheck(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	before := storedSchemaVersion(t, root)
	corruptBackupPage(t, backup)

	_, err := Restore(RestoreOptions{Root: root, BackupPath: backup})
	if err == nil {
		t.Fatal("Restore accepted a corrupt backup")
	}
	if !errors.Is(err, ErrRestoreBackupUnusable) {
		t.Fatalf("err = %v, want ErrRestoreBackupUnusable", err)
	}
	if storedSchemaVersion(t, root) != before {
		t.Fatal("a refused restore changed the database anyway")
	}
}

func TestRestoreRefusesGarbage(t *testing.T) {
	root, _ := migratedRootWithBackup(t)
	garbage := filepath.Join(t.TempDir(), "not-a-database.db")
	if err := os.WriteFile(garbage, []byte("this is not a SQLite file at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(RestoreOptions{Root: root, BackupPath: garbage}); !errors.Is(err, ErrRestoreBackupUnusable) {
		t.Fatalf("err = %v, want ErrRestoreBackupUnusable", err)
	}
}

func TestRestoreRefusesWhileALiveOwnerHoldsTheDatabase(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	holdLease(t, root, databaseSchemaVersion, "v0.10.3")

	_, err := Restore(RestoreOptions{Root: root, BackupPath: backup})
	if !errors.Is(err, ErrRestoreBlockedByOwners) {
		t.Fatalf("err = %v, want ErrRestoreBlockedByOwners", err)
	}
	if !strings.Contains(err.Error(), "v0.10.3") {
		t.Fatalf("refusal %q does not name the holder", err)
	}
	if storedSchemaVersion(t, root) != databaseSchemaVersion {
		t.Fatal("a refused restore changed the database anyway")
	}
}

// TestRestoreRefusesAGenerationMismatchWithoutForce: the backup records the
// generation the database was at when it was taken. A live database at another
// generation has been migrated since, and restoring would discard that.
func TestRestoreRefusesAGenerationMismatchWithoutForce(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	// A second migration moves the generation on, so the backup's recorded one
	// is no longer the live one.
	stampUserVersion(t, root, databaseSchemaVersion-1)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	live, _ := readSidecar(root)

	_, err = Restore(RestoreOptions{Root: root, BackupPath: backup})
	if !errors.Is(err, ErrRestoreGenerationMismatch) {
		t.Fatalf("err = %v, want ErrRestoreGenerationMismatch", err)
	}
	if !strings.Contains(err.Error(), live.GenerationID) || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("refusal %q must name the live generation and --force", err)
	}
	result, err := Restore(RestoreOptions{Root: root, BackupPath: backup, Force: true})
	if err != nil {
		t.Fatalf("--force did not proceed: %v", err)
	}
	if !result.Forced {
		t.Fatal("the result does not record that the restore was forced")
	}
}

func TestRestoreRequiresABackupPath(t *testing.T) {
	root, _ := migratedRootWithBackup(t)
	if _, err := Restore(RestoreOptions{Root: root}); err == nil {
		t.Fatal("Restore with no backup path succeeded")
	}
}

// TestRestoreResultCarriesTheDocumentedJSONKeys keeps `db restore --json` in
// the same key style as `db inspect`.
func TestRestoreResultCarriesTheDocumentedJSONKeys(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	result, err := Restore(RestoreOptions{Root: root, BackupPath: backup})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"schema_version", "path", "restored_from", "pre_restore_backup",
		"user_version", "generation_id", "forced",
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("result is missing %q: %s", key, data)
		}
	}
	if len(decoded) != len(want) {
		t.Fatalf("result has %d keys, want %d: %s", len(decoded), len(want), data)
	}
}

// corruptBackupPage damages a page past the header, so the file still opens as
// a SQLite database and fails integrity_check — which is exactly the backup
// integrity_check exists to catch and a header check would not.
func corruptBackupPage(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 8192 {
		t.Skipf("backup is only %d bytes; nothing past the first page to damage", info.Size())
	}
	if _, err := file.WriteAt([]byte(strings.Repeat("\xa5", 512)), 4096); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreRefusesWhileAnUnleasedConnectionHoldsTheDatabase: short-lived
// `flow` and `plan` leaves publish no owners lease, so the lease scan cannot
// see them. Replacing the file underneath such a handle loses every write it
// makes afterwards, so the connection probe has to refuse.
func TestRestoreRefusesWhileAnUnleasedConnectionHoldsTheDatabase(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	holder, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	// A write, so the connection is really attached rather than merely opened.
	if _, err := holder.List(FlowFilter{}); err != nil {
		t.Fatal(err)
	}

	_, err = Restore(RestoreOptions{Root: root, BackupPath: backup})
	if !errors.Is(err, ErrRestoreBlockedByOwners) {
		t.Fatalf("err = %v, want ErrRestoreBlockedByOwners", err)
	}
	if storedSchemaVersion(t, root) != databaseSchemaVersion {
		t.Fatal("a refused restore changed the database anyway")
	}
}

// TestCopyDatabaseAsidePreservesTheWALOfADatabaseSQLiteCannotOpen: the copy is
// checkpointed first, so an openable database needs no companion WAL. A damaged
// one cannot be checkpointed, its committed rows can live only in -wal, and
// dropping them would make the restore irreversible exactly when reversing it
// matters.
func TestCopyDatabaseAsidePreservesTheWALOfADatabaseSQLiteCannotOpen(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, databaseFilename)
	if err := os.WriteFile(database, []byte("this is not a SQLite file at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(database+"-wal", []byte("uncheckpointed"), 0o600); err != nil {
		t.Fatal(err)
	}

	destination, err := copyDatabaseAside(root, database)
	if err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(destination + "-wal")
	if err != nil {
		t.Fatalf("the pre-restore copy dropped the WAL: %v", err)
	}
	if string(preserved) != "uncheckpointed" {
		t.Fatalf("preserved WAL = %q, want the live one", preserved)
	}
}

// TestCopyDatabaseAsideNeverOverwritesAnEarlierCopy: the name carries a
// second-resolution timestamp, so two copies in one second must not collide —
// least of all with the pre-restore backup an operator is restoring, which
// would destroy the source before the restore reads it.
func TestCopyDatabaseAsideNeverOverwritesAnEarlierCopy(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, databaseFilename)
	if err := os.WriteFile(database, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := copyDatabaseAside(root, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(database, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := copyDatabaseAside(root, database)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both copies took the name %s", first)
	}
	kept, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "first" {
		t.Fatalf("the earlier copy now reads %q", kept)
	}
}

// TestReplaceDatabasePromotesTheBackupsOwnWAL: the mirror image of the copy
// above. A backup that carries a -wal was taken from a database SQLite could
// not checkpoint; promoting the database without it would drop exactly the rows
// the pre-restore copy went out of its way to keep.
func TestReplaceDatabasePromotesTheBackupsOwnWAL(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, databaseFilename)
	if err := os.WriteFile(database, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The replaced database's own WAL, which must not survive.
	if err := os.WriteFile(database+"-wal", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(backup, []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup+"-wal", []byte("restored-wal"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := replaceDatabase(root, database, backup); err != nil {
		t.Fatal(err)
	}
	promoted, err := os.ReadFile(database + "-wal")
	if err != nil {
		t.Fatalf("the backup's WAL was not promoted with it: %v", err)
	}
	if string(promoted) != "restored-wal" {
		t.Fatalf("promoted WAL = %q, want the backup's", promoted)
	}
}

// TestRestoreRefusesItsOwnStagingFileAsTheBackup: an interrupted restore can
// leave a valid database at the staging path, and an operator may well reach
// for it. Copying it onto itself would truncate the source and promote an empty
// file over the live database.
func TestRestoreRefusesItsOwnStagingFileAsTheBackup(t *testing.T) {
	root, backup := migratedRootWithBackup(t)
	staged := filepath.Join(root, restoreStagingFilename)
	if err := copyFileDurably(backup, staged); err != nil {
		t.Fatal(err)
	}
	before := storedSchemaVersion(t, root)

	_, err := Restore(RestoreOptions{Root: root, BackupPath: staged})
	if !errors.Is(err, ErrRestoreBackupUnusable) {
		t.Fatalf("err = %v, want ErrRestoreBackupUnusable", err)
	}
	if info, statErr := os.Lstat(staged); statErr != nil || info.Size() == 0 {
		t.Fatalf("the refused source was truncated: %v (%v)", info, statErr)
	}
	if storedSchemaVersion(t, root) != before {
		t.Fatal("a refused restore changed the database anyway")
	}
}
