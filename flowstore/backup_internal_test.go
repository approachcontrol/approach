package flowstore

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/version"
)

func backupNames(t *testing.T, backupDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestAMigrationWritesAVerifiedBackup(t *testing.T) {
	root := schemaFiveRoot(t)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(root, backupDirName)
	names := backupNames(t, backupDir)
	if len(names) != 1 {
		t.Fatalf("backups = %v, want exactly one", names)
	}
	// The root fingerprint sits between the two, so this is a prefix plus a
	// contains rather than one prefix.
	if !strings.HasPrefix(names[0], databaseFilename+"-") || !strings.Contains(names[0], "-v5-") {
		t.Fatalf("backup %q does not name the migrated file and its schema", names[0])
	}
	// The very first migration of a pre-sidecar root has no generation to draw
	// on, which is the ordinary case rather than an edge one.
	if !strings.HasSuffix(names[0], "-nogen.db") {
		t.Fatalf("backup %q should have fallen back to nogen", names[0])
	}
	backupPath := filepath.Join(backupDir, names[0])
	db, err := sql.Open("sqlite", sqliteDSN(backupPath, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(result, "ok") {
		t.Fatalf("integrity_check = %q", result)
	}
	var version int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("backup user_version = %d, want the pre-migration 5", version)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != artifacts.FilePerm {
		t.Fatalf("backup mode = %04o, want %04o", info.Mode().Perm(), artifacts.FilePerm)
	}
}

func TestAnUnwritableBackupTargetAbortsTheMigration(t *testing.T) {
	root := schemaFiveRoot(t)
	// A regular file where the backup directory must go: MkdirAll fails, so the
	// migration aborts before it can touch user_version.
	backupDir := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(backupDir, []byte("not a directory"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator, BackupDir: backupDir})
	if err == nil {
		t.Fatal("expected the migration to abort")
	}
	if !strings.Contains(err.Error(), "migration backup to") ||
		!strings.Contains(err.Error(), "'approach db migrate --backup-dir <path>'") {
		t.Fatalf("error %q does not name the escape hatch", err)
	}
	if version, err := readUserVersionRO(filepath.Join(root, databaseFilename)); err != nil || version != 5 {
		t.Fatalf("user_version = %d (err %v), want an unchanged 5", version, err)
	}
}

func TestAnAlreadyCurrentDatabaseWritesNoBackup(t *testing.T) {
	root := t.TempDir()
	created, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	// Every TUI startup takes this path. A copy of the database on each one
	// would be a real cost for no benefit.
	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if names := backupNames(t, filepath.Join(root, backupDirName)); len(names) != 0 {
		t.Fatalf("backups = %v, want none", names)
	}
}

// The gate-ordering regression: everything that can still refuse sits above the
// backup, so a refused open never pays for a full copy first.
func TestARefusedMigrationWritesNoBackup(t *testing.T) {
	t.Run("dev live refusal", func(t *testing.T) {
		stateHome := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateHome)
		releaseRoot, err := artifacts.ReleaseDefaultRoot()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(releaseRoot, artifacts.DirPerm); err != nil {
			t.Fatal(err)
		}
		db := createParentReleaseV5Database(t, releaseRoot)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		isDevelopmentBuild = func() bool { return true }
		t.Cleanup(func() { isDevelopmentBuild = version.IsDevelopment })

		if _, err := NewStore(StoreOptions{Root: releaseRoot, Role: RoleMigrator}); err == nil {
			t.Fatal("expected the dev-live refusal")
		}
		if names := backupNames(t, filepath.Join(releaseRoot, backupDirName)); len(names) != 0 {
			t.Fatalf("a refused open wrote backups: %v", names)
		}
	})

	t.Run("unsupported predecessor", func(t *testing.T) {
		root := t.TempDir()
		db := createParentReleaseV5Database(t, root)
		if _, err := db.Exec("PRAGMA user_version = 42"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator}); err == nil {
			t.Fatal("expected an unsupported-predecessor error")
		}
		if names := backupNames(t, filepath.Join(root, backupDirName)); len(names) != 0 {
			t.Fatalf("a refused open wrote backups: %v", names)
		}
	})
}

func TestRetentionKeepsEightBackupsPerMigratedFile(t *testing.T) {
	backupDir := t.TempDir()
	stamp := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	var oldest string
	for i := range 9 {
		name := backupFilename(databaseFilename, 5, stamp.Add(time.Duration(i)*time.Minute), "nogen")
		if i == 0 {
			oldest = name
		}
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("x"), artifacts.FilePerm); err != nil {
			t.Fatal(err)
		}
	}
	// A staged-file backup must never be able to evict the approach.db ones.
	stageBackup := backupFilename(stageFilename, 4, stamp, "nogen")
	if err := os.WriteFile(filepath.Join(backupDir, stageBackup), []byte("x"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}

	if err := pruneBackups(backupDir, databaseFilename, ""); err != nil {
		t.Fatal(err)
	}
	names := backupNames(t, backupDir)
	if len(names) != backupRetention+1 {
		t.Fatalf("kept %d entries (%v), want %d plus the staged one", len(names), names, backupRetention)
	}
	for _, name := range names {
		if name == oldest {
			t.Fatalf("retention kept the oldest backup %q", oldest)
		}
	}
	if _, err := os.Stat(filepath.Join(backupDir, stageBackup)); err != nil {
		t.Fatalf("pruning approach.db backups removed a staged-file backup: %v", err)
	}
}

// The stage-resume call site migrates approach.db.migrating, so its backup must
// name that file rather than approach.db.
func TestTheStageResumeBackupNamesTheStagedFile(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV5DatabaseAt(t, filepath.Join(root, stageFilename))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("stage resume: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	names := backupNames(t, filepath.Join(root, backupDirName))
	if len(names) != 1 || !strings.HasPrefix(names[0], stageFilename+"-") || !strings.Contains(names[0], "-v5-") {
		t.Fatalf("backups = %v, want one named for %s", names, stageFilename)
	}
}

// A shared --backup-dir is an explicit operator choice, and two roots migrating
// the same schema in the same second used to collide on one filename: the
// second VACUUM INTO failed because its target already existed.
func TestASharedBackupDirKeepsRootsApart(t *testing.T) {
	backupDir := t.TempDir()
	var names []string
	for range 2 {
		root := schemaFiveRoot(t)
		store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator, BackupDir: backupDir})
		if err != nil {
			t.Fatalf("migrate into a shared backup dir: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	names = backupNames(t, backupDir)
	if len(names) != 2 {
		t.Fatalf("backups = %v, want one per root", names)
	}
	if names[0] == names[1] {
		t.Fatalf("both roots wrote %q", names[0])
	}
}

// Retention groups on the stem, so a stem that was only the basename let one
// root's ninth migration delete another root's sole pre-migration copy.
func TestRetentionInASharedBackupDirNeverEvictsAnotherRoot(t *testing.T) {
	backupDir := t.TempDir()
	stamp := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	mine := backupStem(filepath.Join("/state/mine", databaseFilename), "/state/mine")
	theirs := backupFilename(backupStem(filepath.Join("/state/theirs", databaseFilename), "/state/theirs"),
		5, stamp, "nogen")
	if err := os.WriteFile(filepath.Join(backupDir, theirs), []byte("x"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	for i := range backupRetention + 1 {
		name := backupFilename(mine, 5, stamp.Add(time.Duration(i)*time.Minute), "nogen")
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("x"), artifacts.FilePerm); err != nil {
			t.Fatal(err)
		}
	}

	if err := pruneBackups(backupDir, mine, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, theirs)); err != nil {
		t.Fatalf("pruning one root's backups removed another root's: %v", err)
	}
	if names := backupNames(t, backupDir); len(names) != backupRetention+1 {
		t.Fatalf("kept %d entries (%v), want %d plus the other root's", len(names), names, backupRetention)
	}
}

// Retention sorts lexically over an embedded wall-clock timestamp. A clock that
// stepped backwards, or one stale entry stamped in the future, makes the copy
// just written sort oldest — and pruning it would leave the migration running
// with no backup of the state it is about to change.
func TestRetentionNeverPrunesTheBackupJustWritten(t *testing.T) {
	backupDir := t.TempDir()
	stamp := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	// The new backup carries the EARLIEST timestamp, so every existing entry
	// sorts after it and a naive prune takes it first.
	fresh := backupFilename(databaseFilename, 5, stamp, "nogen")
	if err := os.WriteFile(filepath.Join(backupDir, fresh), []byte("x"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= backupRetention+2; i++ {
		name := backupFilename(databaseFilename, 5, stamp.Add(time.Duration(i)*time.Hour), "nogen")
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("x"), artifacts.FilePerm); err != nil {
			t.Fatal(err)
		}
	}

	if err := pruneBackups(backupDir, databaseFilename, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, fresh)); err != nil {
		t.Fatalf("retention deleted the backup the migration is about to rely on: %v", err)
	}
	// The exempt copy counts against the budget rather than raising it.
	if names := backupNames(t, backupDir); len(names) != backupRetention {
		t.Fatalf("kept %d entries (%v), want %d", len(names), names, backupRetention)
	}
}

// VACUUM INTO creates its output with SQLite's default mode reduced by the
// umask, so a custom --backup-dir outside the 0700 state root must not be left
// holding a world-readable copy of every Flow record.
func TestABackupInACustomDirectoryIsNeverWorldReadable(t *testing.T) {
	previous := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(previous) })

	root := schemaFiveRoot(t)
	backupDir := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator, BackupDir: backupDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	names := backupNames(t, backupDir)
	if len(names) != 1 {
		t.Fatalf("backups = %v, want exactly one", names)
	}
	// Nothing but the backup: the staging directory the copy is built in must be
	// cleaned up rather than left behind holding a second copy.
	info, err := os.Stat(filepath.Join(backupDir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != artifacts.FilePerm {
		t.Fatalf("backup mode = %04o, want %04o", info.Mode().Perm(), artifacts.FilePerm)
	}
}

// VACUUM INTO cannot run inside a transaction, so the copy reads its own
// snapshot and releases. A build too old to honour the bootstrap lease can
// commit either while that copy is being taken or in the gap before the
// migration's write lock; both leave the published backup describing a state
// that is not the one migrated, and neither the row count nor an in-place
// UPDATE would reveal it.
func TestAWriteBetweenTheBackupAndTheMigrationAbortsIt(t *testing.T) {
	for name, window := range map[string]string{
		"during the copy":   "backup",
		"before the commit": "migration",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			seed := createParentReleaseV5Database(t, root)
			insertV5Flow(t, seed, FlowRecord{
				SchemaVersion: schemaVersion, FlowID: "race", Title: "Race", Instructions: "Test.",
				Status: StatusPending, RepoPath: filepath.Join(root, "repo"),
			})
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			databasePath := filepath.Join(root, databaseFilename)

			// Stand in for that older build: a second connection committing in
			// whichever window this case covers.
			commit := func() {
				other, err := sql.Open("sqlite", sqliteDSN(databasePath, map[string][]string{"mode": {"rw"}}))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = other.Close() }()
				result, err := other.Exec("UPDATE flows SET updated_at = updated_at || 'x'")
				if err != nil {
					t.Fatal(err)
				}
				if affected, err := result.RowsAffected(); err != nil || affected == 0 {
					t.Fatalf("the racing write changed nothing (%d rows, %v); the case proves nothing", affected, err)
				}
			}
			originalBackup, originalMigration := backupRaceProbe, migrationRaceProbe
			t.Cleanup(func() { backupRaceProbe, migrationRaceProbe = originalBackup, originalMigration })
			if window == "backup" {
				backupRaceProbe = commit
			} else {
				migrationRaceProbe = commit
			}

			_, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
			if err == nil {
				t.Fatal("the migration proceeded with a backup that no longer matched the database")
			}
			if !strings.Contains(err.Error(), "another process wrote to the flow database") {
				t.Fatalf("error %q does not name the race", err)
			}
			// Nothing migrated: the refusal lands before any schema statement runs.
			if version, err := readUserVersionRO(databasePath); err != nil || version != 5 {
				t.Fatalf("user_version = %d (err %v), want an unchanged 5", version, err)
			}
		})
	}
}

// --backup-dir is an arbitrary path and MkdirAll may create several levels. A
// directory is only an entry in ITS parent, so syncing the backup directory
// alone leaves the pathname chain to the copy unpersisted: a power loss after
// the migration commits would lose the route to the backup while keeping the
// migrated database.
func TestANestedBackupDirectorySyncsEveryCreatedAncestor(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "new", "a", "backups")

	// Every level below base is missing, and base itself is not this
	// migration's to guarantee.
	missing := missingAncestors(nested)
	want := []string{nested, filepath.Join(base, "new", "a"), filepath.Join(base, "new")}
	if len(missing) != len(want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
	for i, dir := range want {
		if missing[i] != dir {
			t.Fatalf("missing[%d] = %q, want %q", i, missing[i], dir)
		}
	}

	root := schemaFiveRoot(t)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator, BackupDir: nested})
	if err != nil {
		t.Fatalf("migrate into a nested backup dir: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if names := backupNames(t, nested); len(names) != 1 {
		t.Fatalf("backups = %v, want exactly one", names)
	}

	// An existing directory contributes nothing to sync: its entry is already
	// as durable as whoever created it made it.
	if missing := missingAncestors(base); len(missing) != 0 {
		t.Fatalf("missingAncestors of an existing directory = %v, want none", missing)
	}
}

// Retention is chronological, and the filename is not: the -v<version>-
// component sits ahead of the timestamp, so sorting the whole name sorts by
// schema version first. Two cases fall out of that, and both prune the newest
// pre-migration recovery copies rather than the oldest — the exact copies an
// operator reaches for when a migration goes wrong.
func TestRetentionPrunesByTimestampAcrossSchemaVersions(t *testing.T) {
	stamp := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)

	t.Run("a newer low-version backup outlives older high-version ones", func(t *testing.T) {
		backupDir := t.TempDir()
		// An operator restored an old database and re-migrated it, so the newest
		// backup carries the LOWEST version in the directory.
		for i := range backupRetention {
			writeBackupFile(t, backupDir,
				backupFilename(databaseFilename, 5, stamp.Add(time.Duration(i)*time.Minute), "nogen"))
		}
		newest := backupFilename(databaseFilename, 4, stamp.Add(time.Hour), "nogen")
		writeBackupFile(t, backupDir, newest)

		if err := pruneBackups(backupDir, databaseFilename, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(backupDir, newest)); err != nil {
			t.Fatalf("retention pruned the newest backup %q: %v", newest, err)
		}
	})

	t.Run("two-digit versions do not sort before single-digit ones", func(t *testing.T) {
		backupDir := t.TempDir()
		oldest := backupFilename(databaseFilename, 9, stamp, "nogen")
		writeBackupFile(t, backupDir, oldest)
		// Lexically "v10" precedes "v9", so every one of these sorts oldest.
		var newest string
		for i := range backupRetention {
			newest = backupFilename(databaseFilename, 10, stamp.Add(time.Duration(i+1)*time.Minute), "nogen")
			writeBackupFile(t, backupDir, newest)
		}

		if err := pruneBackups(backupDir, databaseFilename, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(backupDir, newest)); err != nil {
			t.Fatalf("retention pruned the newest backup %q (v10 sorted before v9): %v", newest, err)
		}
		if _, err := os.Stat(filepath.Join(backupDir, oldest)); !os.IsNotExist(err) {
			t.Fatalf("retention kept the oldest backup %q: %v", oldest, err)
		}
	})
}

func writeBackupFile(t *testing.T, backupDir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte("x"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
}
