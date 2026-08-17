package flowstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// backupRetention keeps this many backups PER migrated file PER source root.
// Per-stem, so a run of staged-file backups can never evict the approach.db
// ones, and a shared --backup-dir can never evict another root's.
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
	// Recorded BEFORE the create, because afterwards there is no way to tell
	// which levels MkdirAll had to make. --backup-dir is an arbitrary path and
	// may be missing several.
	created := missingAncestors(backupDir)
	if err := os.MkdirAll(backupDir, artifacts.DirPerm); err != nil {
		return backupFailure(backupDir, err)
	}
	stem := backupStem(path, canonicalRoot)
	destination := filepath.Join(backupDir, backupFilename(stem, fromVersion,
		time.Now().UTC(), sidecarGenerationOrNoGen(canonicalRoot)))
	// The copy is built inside a 0700 staging directory and renamed into place,
	// never written at the destination name directly. Two reasons, both about
	// --backup-dir: VACUUM INTO creates its output with SQLite's default mode
	// reduced by the umask, so under the usual 0022 a custom backup directory
	// would hold a world-readable copy of every Flow record for the whole
	// duration of the copy, and chmod'ing afterwards closes the window far too
	// late. The default <root>/backups/ is shielded by the 0700 state root; the
	// documented escape hatch is not. Staging also means a half-written or
	// unverified copy never appears under a name an operator could restore from.
	//
	// A staging directory rather than an O_EXCL pre-created file because
	// VACUUM INTO refuses to write to a path that already exists.
	staging, err := os.MkdirTemp(backupDir, ".backup-")
	if err != nil {
		return backupFailure(backupDir, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	staged := filepath.Join(staging, filepath.Base(destination))
	// Out of space surfaces here rather than through a pre-flight probe: there
	// is no free-space helper in the tree, Statfs' field widths differ per
	// platform, and a probe would be racy against the copy it precedes and
	// untestable besides. The wrap names the escape hatch.
	backupRaceProbe()
	if _, err := db.Exec("VACUUM INTO " + sqliteStringLiteral(staged)); err != nil {
		return backupFailure(destination, err)
	}
	if err := os.Chmod(staged, artifacts.FilePerm); err != nil {
		return backupFailure(destination, err)
	}
	if err := verifyBackup(staged, fromVersion, sourceRows); err != nil {
		return backupFailure(destination, err)
	}
	// VACUUM INTO's own refusal to overwrite is what kept two migrations from
	// clobbering one backup, and the rename below would not inherit it. Keep the
	// property explicitly rather than lose it to the staging step.
	if _, err := os.Lstat(destination); err == nil {
		return backupFailure(destination, fmt.Errorf("a backup by that name already exists"))
	} else if !os.IsNotExist(err) {
		return backupFailure(destination, err)
	}
	if err := os.Rename(staged, destination); err != nil {
		return backupFailure(destination, err)
	}
	// VACUUM INTO syncs the backup's CONTENTS; nothing has synced the directory
	// entry that names it, nor any directory MkdirAll just created. The whole
	// promise of this file is that a crash during the migration below leaves a
	// recoverable copy, and after a power loss an unsynced entry can be gone
	// while the migrated database — committed and synced afterwards — is not.
	// Syncing the backup directory alone is not enough: a directory is only an
	// entry in ITS parent, so a brand-new /mnt/new/a/backups needs the whole
	// created chain persisted or the pathname to the copy is lost.
	if err := syncBackupPath(backupDir, created); err != nil {
		return backupFailure(destination, err)
	}
	// Only after a successful verify, never before: pruning first would trade a
	// proven backup for an unproven one.
	return pruneBackups(backupDir, stem, filepath.Base(destination))
}

// missingAncestors lists the directories MkdirAll will have to create, deepest
// first. It stops at the first path that already exists: that one's own entry
// is already as durable as whoever created it made it, and is not this
// migration's to guarantee.
func missingAncestors(dir string) []string {
	var missing []string
	for {
		if _, err := os.Stat(dir); err == nil {
			return missing
		}
		missing = append(missing, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding anything that exists.
			// MkdirAll will fail; returning what we have keeps this total.
			return missing
		}
		dir = parent
	}
}

// syncBackupPath persists the backup's directory entry and the entry of every
// directory this migration had to create to reach it.
func syncBackupPath(backupDir string, created []string) error {
	if err := syncDirectory(backupDir); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	for _, dir := range created {
		parent := filepath.Dir(dir)
		if parent == dir {
			continue
		}
		if err := syncDirectory(parent); err != nil {
			return fmt.Errorf("sync backup directory ancestor %q: %w", parent, err)
		}
	}
	return nil
}

// backupStem names the file being migrated AND the root it came from.
//
// The basename alone is not an identity. --backup-dir is an explicit path, so
// two state roots can legitimately share one directory, and every first
// migration of a v5 root produces the same `approach.db-v5-<second>-nogen`
// name: the second root's VACUUM INTO fails outright because the target
// already exists. Worse, pruneBackups groups on this stem, so once a shared
// directory holds more than backupRetention `approach.db-v*` entries, migrating
// one root deletes another root's only pre-migration copy — the exact recovery
// path this file exists to guarantee.
//
// A hash rather than the root's basename, because roots are conventionally
// named for their schema generation (`.../sessions/v1`) and their basenames
// collide far more often than the roots do. Truncated to 4 bytes: this
// disambiguates a handful of roots in one directory, it is not a security
// boundary, and a full digest would bury the readable part of the name.
func backupStem(path, canonicalRoot string) string {
	sum := sha256.Sum256([]byte(canonicalRoot))
	return fmt.Sprintf("%s-%s", filepath.Base(path), hex.EncodeToString(sum[:4]))
}

// backupFilename keys on the file being migrated, not on approach.db: the
// stage-resume call migrates approach.db.migrating, and a backup that lied
// about its source would be worse than none.
func backupFilename(stem string, fromVersion int64, at time.Time, generation string) string {
	return fmt.Sprintf("%s-v%d-%s-%s.db", stem, fromVersion, at.Format(backupTimestampLayout), generation)
}

// backupTimestampLayout is the stamp backupFilename writes. Named so the
// parser and the writer cannot drift apart.
const backupTimestampLayout = "20060102T150405Z"

// backupTimestamp recovers when a backup was taken from its name, which is the
// only chronology available: the mtime of a restored or rsynced copy says when
// it was last written, not when it captured the database.
//
// Reported as (time, ok) rather than an error because a name this build did not
// write is not a failure — pruning classifies it and moves on.
func backupTimestamp(stem, name string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(name, stem+"-v")
	if !ok {
		return time.Time{}, false
	}
	// Past the version, which is variable-width and is exactly why the whole
	// name cannot be sorted directly.
	_, rest, ok = strings.Cut(rest, "-")
	if !ok {
		return time.Time{}, false
	}
	stamp, _, ok := strings.Cut(rest, "-")
	if !ok {
		return time.Time{}, false
	}
	at, err := time.ParseInLocation(backupTimestampLayout, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
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
//
// keep names the backup the caller just wrote and verified, which is exempt
// whatever the sort says. Ordering is over an embedded wall-clock timestamp, so
// a clock that stepped backwards — or one stale entry stamped in the future —
// makes the newest copy sort oldest, and pruning it would leave the migration
// below running with no backup of the state it is about to change. Empty when
// no new backup is in play, as on the retention-only path.
func pruneBackups(backupDir, stem, keep string) error {
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
		if entry.Name() == keep {
			continue
		}
		names = append(names, entry.Name())
	}
	// The exempt backup still counts against the budget, so retention holds at
	// backupRetention rather than quietly becoming one more.
	budget := backupRetention
	if keep != "" {
		budget--
	}
	if len(names) <= budget {
		return nil
	}
	// Sorted on the PARSED timestamp, not the filename. The name carries
	// `-v<version>-` ahead of the stamp, so lexical order is version order
	// first: a re-migrated restore writes a newer backup under a lower version
	// and sorts oldest, and once versions reach two digits `v10` precedes `v9`
	// outright. Either way retention prunes the newest recovery copies, which
	// is precisely backwards for the file it exists to preserve.
	//
	// The name breaks the tie so one second holding several backups still
	// prunes deterministically, and an unparseable entry sorts oldest: it is
	// not a name this build writes, and preferring it over a copy whose
	// provenance is legible would be the wrong way to be cautious.
	sort.Slice(names, func(i, j int) bool {
		left, leftOK := backupTimestamp(stem, names[i])
		right, rightOK := backupTimestamp(stem, names[j])
		if leftOK != rightOK {
			return rightOK
		}
		if leftOK && !left.Equal(right) {
			return left.Before(right)
		}
		return names[i] < names[j]
	})
	for _, name := range names[:len(names)-budget] {
		if err := os.Remove(filepath.Join(backupDir, name)); err != nil {
			return fmt.Errorf("prune backup %q: %w", name, err)
		}
	}
	return nil
}

// backupRaceProbe and migrationRaceProbe are no-op seams marking the two
// windows a concurrent commit can land in: during the copy itself, and between
// the copy and the migration's write lock. Neither can be hit deterministically
// from a test otherwise, and an untested consistency check is not one worth
// trusting. They are separate so a test can prove the baseline is sampled
// before the copy rather than after it.
var (
	backupRaceProbe    = func() {}
	migrationRaceProbe = func() {}
)

// readDataVersion samples SQLite's cross-connection commit counter. It is
// explicitly NOT affected by this connection's own writes, which is what makes
// it the right signal: the question is whether anyone ELSE committed.
func readDataVersion(db *sql.DB) (int64, error) {
	var dataVersion int64
	if err := db.QueryRow("PRAGMA data_version").Scan(&dataVersion); err != nil {
		return 0, fmt.Errorf("read flow database data version: %w", err)
	}
	return dataVersion, nil
}

// verifyDataVersion proves nothing committed between the backup and the write
// lock. The message names the actual remedy: this is what a build too old to
// honour the bootstrap lease looks like from in here.
func verifyDataVersion(tx *sql.Tx, expected int64) error {
	var current int64
	if err := tx.QueryRow("PRAGMA data_version").Scan(&current); err != nil {
		return fmt.Errorf("re-read flow database data version: %w", err)
	}
	if current != expected {
		return fmt.Errorf("another process wrote to the flow database while its pre-migration backup" +
			" was being taken, so the backup no longer matches the database this migration would change;" +
			" nothing has been migrated. Close any other approach process using this state root" +
			" — including an older build, which does not honour the migration lease — and re-run")
	}
	return nil
}

// sqliteStringLiteral quotes a path for VACUUM INTO, which takes a literal
// rather than a bind parameter. A state root holding a single quote is legal on
// every platform this ships to.
func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
