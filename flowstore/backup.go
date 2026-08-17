package flowstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

// backupDirName is the default home for pre-migration copies, under the state
// root so a backup travels with the database it came from.
const backupDirName = "backups"

// backupRetention keeps this many backups PER migrated file. Per-stem, so a
// run of staged-file backups can never evict the approach.db ones.
const backupRetention = 8

// backupBeforeMigration copies the database about to be migrated and proves the
// copy is readable before the migration is allowed to proceed.
//
// It runs through the caller's existing handle in autocommit via db.Exec. It
// must never move inside the migration transaction: SQLite answers
// `cannot VACUUM from within a transaction`.
//
// VACUUM INTO rather than a raw copy, because a raw copy of a live database is
// only as consistent as its WAL, and this copy is the thing an operator falls
// back to when a migration goes wrong.
func backupBeforeMigration(db *sql.DB, path, canonicalRoot, backupDir string, fromVersion int64) error {
	if backupDir == "" {
		backupDir = filepath.Join(canonicalRoot, backupDirName)
	}
	// Read the source row count before the copy so the verification has
	// something to compare against. The predecessor-schema validation above has
	// already proved the flows table exists in the shape this version claims.
	var sourceRows int64
	if err := db.QueryRow("SELECT count(*) FROM flows").Scan(&sourceRows); err != nil {
		return backupFailure(backupDir, fmt.Errorf("count source rows: %w", err))
	}
	if err := os.MkdirAll(backupDir, artifacts.DirPerm); err != nil {
		return backupFailure(backupDir, err)
	}
	stem := filepath.Base(path)
	destination := filepath.Join(backupDir, backupFilename(stem, fromVersion,
		time.Now().UTC(), sidecarGenerationOrNoGen(canonicalRoot)))
	// Out of space surfaces here rather than through a pre-flight probe: there
	// is no free-space helper in the tree, Statfs' field widths differ per
	// platform, and a probe would be racy against the copy it precedes and
	// untestable besides. The wrap names the escape hatch.
	if _, err := db.Exec("VACUUM INTO " + sqliteStringLiteral(destination)); err != nil {
		return backupFailure(destination, err)
	}
	if err := os.Chmod(destination, artifacts.FilePerm); err != nil {
		return backupFailure(destination, err)
	}
	if err := verifyBackup(destination, fromVersion, sourceRows); err != nil {
		return backupFailure(destination, err)
	}
	// Only after a successful verify, never before: pruning first would trade a
	// proven backup for an unproven one.
	return pruneBackups(backupDir, stem)
}

// backupFilename keys on the file being migrated, not on approach.db: the
// stage-resume call migrates approach.db.migrating, and a backup that lied
// about its source would be worse than none.
func backupFilename(stem string, fromVersion int64, at time.Time, generation string) string {
	return fmt.Sprintf("%s-v%d-%s-%s.db", stem, fromVersion, at.Format("20060102T150405Z"), generation)
}

func backupFailure(destination string, cause error) error {
	return fmt.Errorf("migration backup to %s failed: %w;"+
		" free space or pass 'approach db migrate --backup-dir <path>'", destination, cause)
}

// verifyBackup reopens the copy and proves it is structurally sound AND is the
// database it claims to be. integrity_check alone would accept a well-formed
// file with the wrong contents, so the schema stamp and the row count are
// checked too.
//
// Deliberately NOT validateAuthoritativeDatabase: that asserts the CURRENT
// schema, and a pre-migration backup is by definition a predecessor copy, so it
// would reject every backup this function exists to write. The generation the
// backup names is the generation it is checked against.
func verifyBackup(path string, fromVersion, sourceRows int64) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		return fmt.Errorf("open backup for verification: %w", err)
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("verify backup integrity: %w", err)
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("backup failed integrity_check: %s", result)
	}
	var backupVersion int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&backupVersion); err != nil {
		return fmt.Errorf("read backup schema version: %w", err)
	}
	if backupVersion != fromVersion {
		return fmt.Errorf("backup schema version %d does not match the migrated database's %d",
			backupVersion, fromVersion)
	}
	var backupRows int64
	if err := db.QueryRow("SELECT count(*) FROM flows").Scan(&backupRows); err != nil {
		return fmt.Errorf("count backup rows: %w", err)
	}
	if backupRows != sourceRows {
		return fmt.Errorf("backup holds %d flow records, the migrated database holds %d",
			backupRows, sourceRows)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close backup verification handle: %w", err)
	}
	return nil
}

// pruneBackups keeps the newest backupRetention entries for one stem. Pruning
// failures are reported: a backup directory that cannot be read is worth
// knowing about before the migration commits, not after it fills the disk.
func pruneBackups(backupDir, stem string) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("read backup directory %q: %w", backupDir, err)
	}
	var names []string
	prefix := stem + "-v"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) <= backupRetention {
		return nil
	}
	// The timestamp sits at a fixed position in a fixed format, so lexical
	// order is chronological order for one stem.
	sort.Strings(names)
	for _, name := range names[:len(names)-backupRetention] {
		if err := os.Remove(filepath.Join(backupDir, name)); err != nil {
			return fmt.Errorf("prune backup %q: %w", name, err)
		}
	}
	return nil
}

// sqliteStringLiteral quotes a path for VACUUM INTO, which takes a literal
// rather than a bind parameter. A state root holding a single quote is legal on
// every platform this ships to.
func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
