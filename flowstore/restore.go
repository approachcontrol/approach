package flowstore

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/dblease"
)

// sidecarProvenanceRestored marks a database put back from a backup. It is a
// third value beside migrated and reconstructed because it is a third thing: the
// schema went BACKWARDS, deliberately, and a history that spelled that
// "migrated" would be a lie an operator reads later.
const sidecarProvenanceRestored = "restored"

// preRestoreBackupPrefix names the copy taken of the database a restore
// replaces, so the restore is itself reversible.
const preRestoreBackupPrefix = "pre-restore"

// The refusals Restore returns. Each is typed because each has a different
// remedy: fix or re-take the backup, close the named processes, or acknowledge
// with --force.
var (
	ErrRestoreBackupUnusable     = errors.New("flow database backup is not usable")
	ErrRestoreBlockedByOwners    = errors.New("flow database restore blocked by a live owner")
	ErrRestoreGenerationMismatch = errors.New("flow database backup is from a different generation")
)

// RestoreOptions names what to put back and where.
type RestoreOptions struct {
	Root       string
	BackupPath string
	// Force proceeds past a generation mismatch. It does not skip the backup
	// verification or the owners refusal: those are not acknowledgements, they
	// are the difference between a restore and data loss.
	Force bool
	// LockTimeout bounds the bootstrap lease wait. Zero uses the package default.
	LockTimeout time.Duration
}

// RestoreResult is what `approach db restore --json` prints. Keys match
// `db inspect`'s style so an operator reading both does not have to switch
// vocabularies.
type RestoreResult struct {
	SchemaVersion int64  `json:"schema_version"`
	Path          string `json:"path"`
	RestoredFrom  string `json:"restored_from"`
	// PreRestoreBackup is the copy of the database this restore replaced.
	PreRestoreBackup string `json:"pre_restore_backup"`
	UserVersion      int64  `json:"user_version"`
	GenerationID     string `json:"generation_id"`
	Forced           bool   `json:"forced"`
}

// restoreResultSchemaVersion versions the result's own JSON, for the same
// reason inspectSchemaVersion does.
const restoreResultSchemaVersion = 1

// Restore puts a verified backup back in place of the live flow database.
//
// The order below is the whole safety argument and must not be rearranged:
//
//  1. verify the backup, before anything is touched — a restore that replaced a
//     working database with an unreadable copy is strictly worse than the
//     problem it was called for;
//  2. scan the owners lease and refuse while any process holds the database
//     open, naming each. Restoring under a live handle is strictly worse than
//     migrating under one: the file is replaced outright, and the holder's
//     open descriptor keeps referring to an unlinked inode;
//  3. take the bootstrap lock — after the lease scan, the same total order the
//     migration path uses;
//  4. compare generations, and refuse a mismatch without --force;
//  5. copy the live database aside, then replace it, then drop the stale WAL.
func Restore(opts RestoreOptions) (RestoreResult, error) {
	if strings.TrimSpace(opts.BackupPath) == "" {
		return RestoreResult{}, fmt.Errorf("restore requires a backup path (--backup)")
	}
	root, err := secureCanonicalRoot(opts.Root)
	if err != nil {
		return RestoreResult{}, err
	}
	backupPath, err := filepath.Abs(opts.BackupPath)
	if err != nil {
		return RestoreResult{}, err
	}
	backupVersion, err := verifyRestoreSource(backupPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := refuseRestoreForOwners(root); err != nil {
		return RestoreResult{}, err
	}
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = bootstrapLockTimeout
	}
	release, err := artifacts.AcquireFileLockNoFollow(
		filepath.Join(root, bootstrapLockFilename),
		"flow database bootstrap lock (another approach process may be migrating this state root)",
		lockTimeout,
	)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore flow database: %w", err)
	}
	dblease.Probe(dblease.ProbeBootstrapLock)
	defer release()

	// Re-scanned under the lock is deliberately NOT done: a holder that
	// appeared since the scan above cannot have opened this database through
	// this build, because every open takes the bootstrap lock this call is now
	// holding. The scan above is the one that matters.
	live, _ := readSidecar(root)
	if err := refuseRestoreGenerationMismatch(live, backupPath, opts.Force); err != nil {
		return RestoreResult{}, err
	}

	databasePath := filepath.Join(root, databaseFilename)
	preRestore, err := copyDatabaseAside(root, databasePath)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := replaceDatabase(root, databasePath, backupPath); err != nil {
		return RestoreResult{}, err
	}
	generation := newGenerationID()
	from := backupVersion
	if err := writeSidecar(root, backupVersion, sidecarProvenanceRestored, generation, migrationOutcome{
		Migrated:    true,
		FromVersion: from,
		BackupPath:  backupPath,
	}); err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{
		SchemaVersion:    restoreResultSchemaVersion,
		Path:             databasePath,
		RestoredFrom:     backupPath,
		PreRestoreBackup: preRestore,
		UserVersion:      backupVersion,
		GenerationID:     generation,
		Forced:           opts.Force,
	}, nil
}

// verifyRestoreSource proves the backup is a usable flow database before
// anything is replaced, and reports the generation it holds.
//
// Its schema shape is checked against ITS OWN user_version, never the current
// one: a pre-migration backup is by definition a predecessor copy, and
// asserting the current shape would reject every backup this command exists to
// restore.
func verifyRestoreSource(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrRestoreBackupUnusable, err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%w: %s is not a regular file", ErrRestoreBackupUnusable, path)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{
		"mode":    {"ro"},
		"_pragma": {"query_only(1)"},
	}))
	if err != nil {
		return 0, fmt.Errorf("%w: open %s: %v", ErrRestoreBackupUnusable, path, err)
	}
	defer func() { _ = db.Close() }()
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return 0, fmt.Errorf("%w: %s failed to answer integrity_check: %v", ErrRestoreBackupUnusable, path, err)
	}
	if !strings.EqualFold(integrity, "ok") {
		return 0, fmt.Errorf("%w: %s failed integrity_check: %s", ErrRestoreBackupUnusable, path, integrity)
	}
	var backupVersion int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&backupVersion); err != nil {
		return 0, fmt.Errorf("%w: cannot read %s's schema version: %v", ErrRestoreBackupUnusable, path, err)
	}
	if err := validateRestorableShape(db, backupVersion); err != nil {
		return 0, fmt.Errorf("%w: %s is stamped schema %d but is not a shape this build recognises: %v",
			ErrRestoreBackupUnusable, path, backupVersion, err)
	}
	if err := db.Close(); err != nil {
		return 0, fmt.Errorf("%w: close %s: %v", ErrRestoreBackupUnusable, path, err)
	}
	return backupVersion, nil
}

// validateRestorableShape accepts a backup whose object shape matches its own
// stamp OR any later generation up to this build's.
//
// Exactly as permissive as the migrator, and for the same reason: a database
// stamped N whose shape is already N+1 is the in-place case
// migrateAuthoritativeDatabase handles by stamping only, and it is a shape that
// really exists on disk. Insisting on an exact match would refuse a legitimate
// backup that the migration path itself would have accepted, which is the worst
// kind of strictness — it blocks the recovery without preventing anything.
//
// A shape newer than this build's is still refused: it is not a shape this
// binary has a definition of, so it cannot be checked at all.
func validateRestorableShape(db *sql.DB, backupVersion int64) error {
	err := validateSQLiteSchemaVersion(db, backupVersion)
	for candidate := backupVersion + 1; err != nil && candidate <= databaseSchemaVersion; candidate++ {
		if validateSQLiteSchemaVersion(db, candidate) == nil {
			return nil
		}
	}
	return err
}

// refuseRestoreForOwners refuses while ANY process holds the database open,
// compatible build or not. This is stricter than the migration path on purpose:
// a migration advances a file in place, and a holder at the target schema is
// fine with the result; a restore REPLACES the file, and every live holder ends
// up reading an unlinked inode whatever build it is.
func refuseRestoreForOwners(root string) error {
	dblease.Probe(probeOwnersScan)
	live, _, err := dblease.Scan(root)
	if err != nil || len(live) == 0 {
		return nil
	}
	described := make([]string, 0, len(live))
	for _, record := range live {
		described = append(described, record.Describe())
	}
	noun := "process is"
	if len(live) > 1 {
		noun = "processes are"
	}
	return fmt.Errorf("%w: %d %s holding %s open — %s; stop these processes, then re-run"+
		" 'approach db restore'", ErrRestoreBlockedByOwners, len(live), noun, root,
		strings.Join(described, "; "))
}

// refuseRestoreGenerationMismatch stops a restore that would silently discard
// migrations taken since the backup.
//
// The authority is the sidecar's HISTORY, not the backup's filename. A backup
// is named for the generation the database was at when it was taken, which for
// the first migration of any root is `nogen` — so a filename comparison would
// refuse exactly the backup an operator reaches for most often. The history
// entry for the newest migration names the backup that migration wrote, and
// "is this the backup that undoes the last thing that happened here" is the
// question actually worth asking.
//
// With no history to consult — an older sidecar, or none — it falls back to the
// filename comparison, and allows the case where neither side knows anything,
// because a refusal has to be based on evidence of a conflict rather than on
// its absence.
func refuseRestoreGenerationMismatch(live databaseSidecar, backupPath string, force bool) error {
	if restoreUndoesTheNewestMigration(live, backupPath) {
		return nil
	}
	if force {
		return nil
	}
	recorded := backupGeneration(backupPath)
	return fmt.Errorf("%w: %s is not the backup the current generation %s was migrated from"+
		" (it records generation %s); restoring it would discard everything written since."+
		" Pass --force to do it anyway",
		ErrRestoreGenerationMismatch, filepath.Base(backupPath),
		generationOrNone(live.GenerationID), generationOrNone(recorded))
}

// restoreUndoesTheNewestMigration reports whether backupPath is the copy the
// most recent recorded event wrote.
func restoreUndoesTheNewestMigration(live databaseSidecar, backupPath string) bool {
	for i := len(live.History) - 1; i >= 0; i-- {
		entry := live.History[i]
		if entry.BackupPath == nil {
			// A reconstructed entry knows no backup. It is not evidence that
			// THIS backup is wrong, so keep looking back for one that is.
			continue
		}
		return sameRestorePath(*entry.BackupPath, backupPath)
	}
	// No history at all: fall back to the generation the filename carries.
	recorded := backupGeneration(backupPath)
	if recorded == "" && live.GenerationID == "" {
		return true
	}
	return recorded != "" && recorded == live.GenerationID
}

// sameRestorePath compares two backup paths by where they point, so a
// symlinked or relative spelling of the recorded path still matches.
func sameRestorePath(recorded, requested string) bool {
	if recorded == requested {
		return true
	}
	left, leftErr := filepath.EvalSymlinks(recorded)
	right, rightErr := filepath.EvalSymlinks(requested)
	return leftErr == nil && rightErr == nil && left == right
}

// backupGeneration recovers the generation a backup's name carries. Empty when
// the name is not one this build wrote, or when the backup predates any
// migration (`nogen`) — both are "unknown", and neither may silently satisfy
// the comparison above.
func backupGeneration(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".db")
	at := strings.LastIndexByte(name, '-')
	if at < 0 {
		return ""
	}
	generation := name[at+1:]
	if generation == "nogen" {
		return ""
	}
	return generation
}

// copyDatabaseAside preserves the database a restore is about to replace, so
// the restore is itself reversible. A missing database is not an error: a root
// whose approach.db was deleted is exactly the case a restore is for.
func copyDatabaseAside(root, databasePath string) (string, error) {
	if _, err := os.Lstat(databasePath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("inspect flow database before restore: %w", err)
	}
	backupDir := filepath.Join(root, backupDirName)
	if err := os.MkdirAll(backupDir, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("create backup directory for the pre-restore copy: %w", err)
	}
	// A plain file copy, not VACUUM INTO: the WAL is dropped by the restore
	// below, so the bytes on disk are exactly what this copy has to preserve —
	// including a database too damaged for SQLite to open, which is precisely
	// the one worth keeping.
	destination := filepath.Join(backupDir, fmt.Sprintf("%s-%s-%s.db",
		preRestoreBackupPrefix, databaseFilename, time.Now().UTC().Format(backupTimestampLayout)))
	if err := copyFileDurably(databasePath, destination); err != nil {
		return "", fmt.Errorf("copy the flow database aside before restoring: %w", err)
	}
	return destination, nil
}

// replaceDatabase puts the backup in place and drops the WAL the previous
// database left.
//
// Removing -wal and -shm is safe here and ONLY here: the bootstrap lock is
// held and the owners scan proved no process has the database open, so nothing
// can be mid-transaction. Leaving them would shadow the restored file with the
// replaced one's uncheckpointed content, which is data loss dressed as a
// successful restore.
func replaceDatabase(root, databasePath, backupPath string) error {
	staged := filepath.Join(root, ".approach.db.restoring")
	if err := copyFileDurably(backupPath, staged); err != nil {
		return fmt.Errorf("stage the restored flow database: %w", err)
	}
	if err := os.Rename(staged, databasePath); err != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("promote the restored flow database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(databasePath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove the replaced database's %s file: %w", suffix, err)
		}
	}
	if err := syncDirectory(root); err != nil {
		return fmt.Errorf("sync the restored flow database's directory: %w", err)
	}
	return nil
}

// copyFileDurably copies and fsyncs the destination. Durable because a restore
// that survived only in the page cache would be lost by the same power failure
// an operator is often restoring from.
func copyFileDurably(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, artifacts.FilePerm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
