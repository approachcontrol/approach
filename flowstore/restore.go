package flowstore

import (
	"context"
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

	// The lease scan above is not re-run under the lock, and on its own it is
	// not the whole guarantee: only LONG-LIVED processes publish a lease, and a
	// short-lived `flow` or `plan` leaf releases the bootstrap lock as soon as
	// its store is open, so it can hold a live SQLite handle while this call
	// owns the lock. The lease scan names such processes for the operator; the
	// connection probe below is what catches the unleased ones.
	live, _ := readSidecar(root)
	if err := refuseRestoreGenerationMismatch(live, backupPath, opts.Force); err != nil {
		return RestoreResult{}, err
	}

	databasePath := filepath.Join(root, databaseFilename)
	if err := refuseRestoreForOpenConnections(databasePath); err != nil {
		return RestoreResult{}, err
	}
	preRestore, err := copyDatabaseAside(root, databasePath)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := replaceDatabase(root, databasePath, backupPath); err != nil {
		// The pre-restore copy is named here because the result that would have
		// carried it is never returned on this path, and an operator told only
		// that a restore failed has no way to find the copy of the database it
		// was about to replace.
		if preRestore != "" {
			return RestoreResult{}, fmt.Errorf("%w (the database this restore would have replaced was copied to %s)", err, preRestore)
		}
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

// refuseRestoreForOpenConnections refuses while any process still has the
// database open, whether or not it published an owners lease.
//
// The lease covers the TUI and `serve`; the short-lived `flow` and `plan`
// leaves deliberately publish nothing, and one of them can be mid-command right
// now. Replacing the file underneath such a handle is silent data loss: the
// writer keeps the unlinked inode, its commit succeeds, and the bytes are
// unreachable the moment it exits.
//
// The probe is SQLite's own answer to "is anyone else attached": in WAL mode
// every open connection holds a shared lock on the database file for its whole
// lifetime, so entering exclusive locking mode is refused with SQLITE_BUSY
// exactly when another connection exists. busy_timeout(0) makes that answer
// immediate rather than a wait.
//
// Only a BUSY/LOCKED answer refuses. Every other failure — a missing database,
// a read-only mount, a file too damaged to open — is inconclusive, and a
// restore is precisely the command those cases need to still run.
func refuseRestoreForOpenConnections(databasePath string) error {
	if _, err := os.Lstat(databasePath); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", sqliteDSN(databasePath, map[string][]string{
		"_pragma": {"busy_timeout(0)", "locking_mode(exclusive)"},
	}))
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(context.Background())
	if err != nil {
		return restoreBusyRefusal(databasePath, err)
	}
	defer func() { _ = conn.Close() }()
	// BEGIN IMMEDIATE, not a read: exclusive locking mode is entered on the
	// first lock the connection actually takes, and a read would settle for a
	// shared one that a second reader is happy to share.
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return restoreBusyRefusal(databasePath, err)
	}
	_, err = conn.ExecContext(context.Background(), "ROLLBACK")
	return err
}

// restoreBusyRefusal turns a probe failure into a refusal only when SQLite said
// the file is held; anything else reports no problem, per the doc above.
func restoreBusyRefusal(databasePath string, err error) error {
	switch sqliteResultCode(err) {
	case sqliteBusy, sqliteLocked:
		return fmt.Errorf("%w: another process still has %s open (it may be a short-lived"+
			" 'approach flow' or 'approach plan' command, which publishes no owner record);"+
			" wait for it to exit, then re-run 'approach db restore'",
			ErrRestoreBlockedByOwners, databasePath)
	default:
		return nil
	}
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
	destination, err := createUniqueBackupName(backupDir)
	if err != nil {
		return "", err
	}
	// The WAL is copied BEFORE the checkpoint below, because opening a database
	// SQLite cannot make sense of DELETES the -wal beside it — the exact file
	// this copy exists to keep. Copy first, then decide whether to keep it.
	if err := copyFileDurablyIfPresent(databasePath+"-wal", destination+"-wal"); err != nil {
		return "", fmt.Errorf("copy the flow database's write-ahead log aside before restoring: %w", err)
	}
	// Checkpointed so the copy is SELF-CONTAINED where it can be: a backup
	// whose committed rows lived only in a companion -wal needs a second file
	// nobody thinks to carry. Best effort — a database too damaged to open is
	// precisely the one worth keeping, and that is what the copy above is for.
	checkpointed := checkpointBeforeCopy(databasePath)
	// A plain file copy, not VACUUM INTO: the bytes on disk are exactly what
	// this copy has to preserve, damage included.
	if err := copyFileDurably(databasePath, destination); err != nil {
		return "", fmt.Errorf("copy the flow database aside before restoring: %w", err)
	}
	if checkpointed {
		// Folded in, so the companion is now a stale shadow of the copy it sits
		// beside — and replaceDatabase would promote it back over a database
		// that already contains it.
		if err := os.Remove(destination + "-wal"); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("drop the checkpointed write-ahead log beside the pre-restore copy: %w", err)
		}
	}
	return destination, nil
}

// createUniqueBackupName reserves a pre-restore name no existing file holds.
//
// Exclusive creation, not a timestamp alone: the name is second-resolution, and
// two restores in the same second would otherwise have the second copy truncate
// the first. Worse, an operator restoring the pre-restore backup they were just
// handed would, within that second, generate ITS name — and the copy would
// overwrite the very file the restore is about to read.
func createUniqueBackupName(backupDir string) (string, error) {
	stamp := time.Now().UTC().Format(backupTimestampLayout)
	for attempt := 0; attempt < 100; attempt++ {
		suffix := ""
		if attempt > 0 {
			suffix = fmt.Sprintf("-%d", attempt+1)
		}
		candidate := filepath.Join(backupDir, fmt.Sprintf("%s-%s-%s%s.db",
			preRestoreBackupPrefix, databaseFilename, stamp, suffix))
		file, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, artifacts.FilePerm)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", fmt.Errorf("reserve a name for the pre-restore copy: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("reserve a name for the pre-restore copy: %w", err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("reserve a name for the pre-restore copy: %s already holds 100 copies for this second", backupDir)
}

// checkpointBeforeCopy folds a WAL back into the database file it belongs to,
// and reports whether that really happened.
//
// Every failure is inspected rather than ignored: the answer decides whether
// the companion WAL copied aside is redundant or is the only place the rows
// live. A refusal is never fatal — this is an optimization of what the copy
// captures, not a precondition for taking one.
func checkpointBeforeCopy(databasePath string) bool {
	db, err := sql.Open("sqlite", sqliteDSN(databasePath, nil))
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	var busy, logFrames, checkpointed int64
	if err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return false
	}
	// busy is SQLite's own answer: 0 means the log was checkpointed, 1 means it
	// could not be, and a copy taken after "could not be" needs its WAL.
	return busy == 0
}

// copyFileDurablyIfPresent copies source when it exists and has content.
// A missing or empty source is not an error and is not copied: a cleanly closed
// or checkpointed WAL database has no -wal file, or a zero-length one, and
// neither carries a row worth moving.
func copyFileDurablyIfPresent(source, destination string) error {
	_, err := copyFileDurablyIfPresentReporting(source, destination)
	return err
}

// copyFileDurablyIfPresentReporting is copyFileDurablyIfPresent, and also says
// whether it copied anything — which is what decides whether there is a staged
// file to promote.
func copyFileDurablyIfPresentReporting(source, destination string) (bool, error) {
	info, err := os.Lstat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	if err := copyFileDurably(source, destination); err != nil {
		return false, err
	}
	return true, nil
}

// replaceDatabase puts the backup in place, with the WAL that belongs to it,
// and drops the one the previous database left.
//
// Everything that can FAIL happens before anything is promoted: both files are
// staged and fsynced first, and the live database is replaced only by renames
// and unlinks after that. The order matters because a failure after the rename
// is not recoverable by returning an error — the old database is already gone —
// and a half-copied WAL promoted beside a restored database would be worse than
// either alone.
//
// Removing the replaced database's -wal and -shm is safe here and ONLY here:
// the bootstrap lock is held and nothing has the database open, so nothing can
// be mid-transaction. Leaving them would shadow the restored file with the
// replaced one's uncheckpointed content, which is data loss dressed as a
// successful restore.
//
// A backup that carries its OWN -wal is the mirror image: it was copied from a
// database SQLite could not checkpoint, its committed rows live there, and
// promoting the database without it would silently drop exactly what the
// pre-restore copy went out of its way to keep. It is renamed into place BEFORE
// the database, so no window exists in which the restored database is visible
// without the log it needs. -shm is never promoted; SQLite rebuilds it.
func replaceDatabase(root, databasePath, backupPath string) error {
	staged := filepath.Join(root, ".approach.db.restoring")
	stagedWAL := staged + ".wal"
	cleanup := func() {
		_ = os.Remove(staged)
		_ = os.Remove(stagedWAL)
	}
	if err := copyFileDurably(backupPath, staged); err != nil {
		cleanup()
		return fmt.Errorf("stage the restored flow database: %w", err)
	}
	stagedWALPresent, err := copyFileDurablyIfPresentReporting(backupPath+"-wal", stagedWAL)
	if err != nil {
		cleanup()
		return fmt.Errorf("stage the restored flow database's write-ahead log: %w", err)
	}
	// Nothing below copies bytes. From here the live database is replaced by
	// renames and unlinks only.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(databasePath + suffix); err != nil && !os.IsNotExist(err) {
			cleanup()
			return fmt.Errorf("remove the replaced database's %s file: %w", suffix, err)
		}
	}
	if stagedWALPresent {
		if err := os.Rename(stagedWAL, databasePath+"-wal"); err != nil {
			cleanup()
			return fmt.Errorf("promote the restored flow database's write-ahead log: %w", err)
		}
	}
	if err := os.Rename(staged, databasePath); err != nil {
		cleanup()
		return fmt.Errorf("promote the restored flow database: %w", err)
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
