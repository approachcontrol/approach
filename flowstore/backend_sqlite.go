package flowstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	_ "modernc.org/sqlite"
)

const (
	databaseFilename = "approach.db"
	stageFilename    = "approach.db.migrating"
)

// databaseSchemaVersion is stamped into PRAGMA user_version. It exists so a
// future schema change has something to branch on, and so an older binary can
// tell "a newer approach wrote this" apart from "this file is corrupt" —
// without it, both surface as a raw column-set mismatch dump.
//
// Current sequence: v3 prepared_at + receipt trigger; v4 epic-progression
// `done` JSON + compatibility triggers; v5 progression-claim marker trigger;
// v6 flows.preparation_nonce projection, backfill, and nonce-protection trigger;
// v7 recovery-generation projection and predecessor-writer compatibility
// trigger.
const databaseSchemaVersion = 7

// DatabaseSchemaVersion is the physical flow database schema this build writes.
// It is exported so launch and diagnostic surfaces can report the schema an
// agent's binary must be able to write without importing store internals.
func DatabaseSchemaVersion() int {
	return databaseSchemaVersion
}

// errDatabaseFromNewerBuild marks the one validation failure that is NOT a
// damaged database. The file is fine; this binary is old. It must stay
// distinguishable, because the recovery advice attached to a corrupt database —
// move it aside and re-migrate from flows/ — would silently roll a perfectly
// good corpus back to its migration-day snapshot. The only correct action is
// the one the message already states: upgrade approach.
var errDatabaseFromNewerBuild = errors.New("flow database was written by a newer version of approach")

const flowTableSchemaV1 = `
CREATE TABLE IF NOT EXISTS flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL
)`

const flowTableSchemaV2 = `
CREATE TABLE IF NOT EXISTS flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL,
    bead_id TEXT NOT NULL DEFAULT '',
    epic_id TEXT NOT NULL DEFAULT ''
 )`

const flowTableSchemaV3 = `
CREATE TABLE IF NOT EXISTS flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL,
    bead_id TEXT NOT NULL DEFAULT '',
    epic_id TEXT NOT NULL DEFAULT '',
    prepared_at TEXT NOT NULL DEFAULT ''
 )`

const flowTableSchemaV4 = flowTableSchemaV3

const flowTableSchemaV5 = flowTableSchemaV4

const flowTableSchemaV6 = `
CREATE TABLE IF NOT EXISTS flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL,
    bead_id TEXT NOT NULL DEFAULT '',
    epic_id TEXT NOT NULL DEFAULT '',
    prepared_at TEXT NOT NULL DEFAULT '',
    preparation_nonce TEXT NOT NULL DEFAULT ''
 )`

const flowTableSchemaV7 = `
CREATE TABLE IF NOT EXISTS flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL,
    bead_id TEXT NOT NULL DEFAULT '',
    epic_id TEXT NOT NULL DEFAULT '',
    prepared_at TEXT NOT NULL DEFAULT '',
    preparation_nonce TEXT NOT NULL DEFAULT '',
    recovery_generation INTEGER NOT NULL DEFAULT 0
 )`

const epicProgressionTableSchema = `
CREATE TABLE IF NOT EXISTS epic_progressions (
    repo_path TEXT NOT NULL,
    epic_id TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL,
    PRIMARY KEY(repo_path, epic_id)
)`

const flowBeadCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_linked_flow_record_update
BEFORE UPDATE OF record ON flows
WHEN (OLD.bead_id <> '' OR OLD.epic_id <> '')
    AND (
        COALESCE(json_extract(CAST(NEW.record AS TEXT), '$.bead.id'), '') <> NEW.bead_id
        OR COALESCE(json_extract(CAST(NEW.record AS TEXT), '$.bead.epic_id'), '') <> NEW.epic_id
    )
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot remove persisted Bead link');
END`

const flowPreparedCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_prepared_flow_record_update
BEFORE UPDATE OF record, prepared_at ON flows
WHEN OLD.prepared_at <> '' AND (NEW.prepared_at <> OLD.prepared_at OR COALESCE(json_extract(CAST(NEW.record AS TEXT), '$.prepared_at'), '') <> OLD.prepared_at)
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot remove persisted preparation receipt');
END`

const flowProgressionClaimCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_progression_claim_record_update
BEFORE UPDATE OF record ON flows
WHEN COALESCE(json_extract(CAST(OLD.record AS TEXT), '$.progression_claim'), 0) = 1
    AND COALESCE(json_extract(CAST(NEW.record AS TEXT), '$.progression_claim'), 0) <> 1
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot remove persisted progression claim marker');
END`

const epicProgressionDoneInsertCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_epic_progression_done_insert
BEFORE INSERT ON epic_progressions
WHEN json_type(CAST(NEW.record AS TEXT), '$.done') IS NULL
    OR json_type(CAST(NEW.record AS TEXT), '$.done') NOT IN ('true', 'false')
    OR (SELECT count(*) FROM json_each(CAST(NEW.record AS TEXT)) WHERE lower(key) = 'done') != 1
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot write epic progression without done');
END`

const epicProgressionDoneUpdateCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_epic_progression_done_record_update
BEFORE UPDATE OF record ON epic_progressions
WHEN json_type(CAST(NEW.record AS TEXT), '$.done') IS NULL
    OR json_type(CAST(NEW.record AS TEXT), '$.done') NOT IN ('true', 'false')
    OR (SELECT count(*) FROM json_each(CAST(NEW.record AS TEXT)) WHERE lower(key) = 'done') != 1
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot remove epic progression done state');
END`

const flowPreparationNonceCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_preparation_nonce_update
BEFORE UPDATE OF record, preparation_nonce ON flows
WHEN OLD.preparation_nonce <> '' AND (NEW.preparation_nonce <> OLD.preparation_nonce OR COALESCE(json_extract(CAST(NEW.record AS TEXT), '$.preparation_nonce'), '') <> OLD.preparation_nonce)
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot remove persisted preparation nonce');
END`

const flowRecoveryGenerationCompatibilityTrigger = `
CREATE TRIGGER IF NOT EXISTS guard_recovered_launch_state_update
BEFORE UPDATE OF record ON flows
WHEN NEW.recovery_generation <= OLD.recovery_generation
    AND EXISTS (
        SELECT 1
        FROM json_each(CAST(OLD.record AS TEXT), '$.phases') AS phase
        WHERE json_type(phase.value, '$.reconciliation') = 'object'
            OR (
                json_type(phase.value, '$.recovered_launch_ids') = 'array'
                AND json_array_length(phase.value, '$.recovered_launch_ids') > 0
            )
    )
BEGIN
    SELECT RAISE(ABORT, 'older approach version cannot remove persisted recovered launch state');
END`

const flowSchemaSQL = flowTableSchemaV7 + `;
CREATE INDEX IF NOT EXISTS idx_flows_updated
    ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX IF NOT EXISTS idx_flows_repo_updated
    ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX IF NOT EXISTS idx_flows_status_updated
    ON flows(status, updated_at DESC, flow_id ASC);
` + epicProgressionTableSchema + `;
` + flowBeadCompatibilityTrigger + `;
` + flowPreparedCompatibilityTrigger + `;
` + flowProgressionClaimCompatibilityTrigger + `;
` + epicProgressionDoneInsertCompatibilityTrigger + `;
` + epicProgressionDoneUpdateCompatibilityTrigger + `;
` + flowPreparationNonceCompatibilityTrigger + `;
` + flowRecoveryGenerationCompatibilityTrigger + `;`

type sqliteBackend struct {
	db *sql.DB
	// root is the canonical, symlink-resolved store root. Kept because it is the
	// evidence that secureCanonicalRoot's resolution reached the backend.
	root string
	// role is carried on the concrete backend rather than in a decorator: the
	// package reaches past the backend interface four times in
	// epic_progression.go (two type assertions to *sqliteBackend and two to
	// epicProgressionBackend), and a wrapper would break all four — including
	// the ReadEpicProgression read `approach serve` depends on.
	role        Role
	diagnostics OpenDiagnostics
	// guard fences writes through a handle whose database was migrated or
	// restored underneath it. See generation_guard.go.
	guard           generationGuard
	generationClock func() time.Time
	readGeneration  func() string
	readUserVersion func() (int64, error)
	beginTx         func(context.Context) (sqliteTransaction, error)
	queryListRows   func(string, ...any) (flowListRows, error)
}

// backendOptions carries what an open needs. It replaces a positional parameter
// list that had already reached four and would have reached seven: every later
// change here is a field, not a signature.
type backendOptions struct {
	root        string
	lockTimeout time.Duration
	presets     []Preset
	// allowDevLiveMigration acknowledges a development build advancing the
	// schema of the database a released build owns. It is a separate gate from
	// role: that one asks "which build", this one asks "which role".
	role Role
	// rootExplicit reports that the caller named this root rather than falling
	// back to config or the built-in default. It decides only whether a missing
	// root is created, never which root helper the role uses.
	rootExplicit          bool
	allowDevLiveMigration bool
	// backupDir overrides <root>/backups/ for the pre-migration copy.
	backupDir string
	// ownerNonce is this opener's own owners-lease holder, excluded from the
	// scan a migration runs so a long-lived migrator does not block itself.
	ownerNonce string
}

// requireWriter refuses a mutation on a read-only handle.
//
// It is called from exactly two places — the beginTx closure and delete — which
// is a pair rather than an enumeration: every write in this package is either
// inside a transaction or is delete's single direct Exec. A write added later
// either begins a transaction or does not, and both are covered.
func (b *sqliteBackend) requireWriter() error {
	if b.role == RoleReader {
		return errReaderWrite
	}
	return nil
}

type flowListRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type sqliteTransaction interface {
	QueryRow(query string, args ...any) *sql.Row
	// Query returns *sql.Rows and NOT flowListRows on purpose. Go return types
	// are invariant, so declaring it as (flowListRows, error) — even though
	// *sql.Rows satisfies that interface — would stop *sql.Tx from satisfying
	// sqliteTransaction and break beginTx below. Do not "improve" it.
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
	Commit() error
	Rollback() error
}

func openSQLiteBackend(opts backendOptions) (*sqliteBackend, error) {
	root := opts.root
	path := filepath.Join(root, databaseFilename)
	millis, err := sqliteBusyTimeoutMillis(opts.lockTimeout)
	if err != nil {
		return nil, err
	}
	dsn := sqliteDSN(path, map[string][]string{
		"mode":    {"rw"},
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis)},
		"_txlock": {"immediate"},
	})
	if opts.role == RoleReader {
		// No _txlock=immediate: a reader must not open every transaction with
		// BEGIN IMMEDIATE. query_only is the SQLite-level backstop under
		// requireWriter, so even a path that reached the driver cannot write.
		dsn = sqliteDSN(path, map[string][]string{
			"mode":    {"ro"},
			"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis), "query_only(1)"},
		})
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open flow database: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = db.Close()
		}
	}()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open flow database: %w", err)
	}
	diagnostics, err := inspectOpenRoot(root)
	if err != nil {
		return nil, err
	}
	if opts.role == RoleReader {
		// A reader mutates nothing on the open path: no chmod of the database,
		// no journal_mode write, no sidecar tightening. That drops the assertion
		// that the database really is in WAL mode, so read the pragma instead
		// and report a DELETE-mode database rather than silently accepting it.
		var journalMode string
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
			return nil, fmt.Errorf("read flow database journal mode: %w", err)
		}
		diagnostics.JournalMode = strings.ToLower(journalMode)
		if !strings.EqualFold(journalMode, "wal") {
			diagnostics.Warnings = append(diagnostics.Warnings, fmt.Sprintf(
				"flow database %q is in %q journal mode, not WAL", path, diagnostics.JournalMode))
		}
	} else {
		// Secure the main file BEFORE switching to WAL: SQLite copies the database
		// file's mode onto the -wal and -shm it creates, and those sidecars hold
		// recently written Flow records. Chmod'ing afterwards would leave a database
		// that arrived with a loose mode (a tarball restored under a default umask)
		// with world-readable sidecars while the main file looked correctly locked
		// down. The 0700 root is still the real boundary; this is defence in depth.
		if err := os.Chmod(path, artifacts.FilePerm); err != nil {
			return nil, fmt.Errorf("secure flow database: %w", err)
		}
		var journalMode string
		if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
			return nil, fmt.Errorf("enable flow database WAL: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return nil, fmt.Errorf("enable flow database WAL: SQLite reported %q", journalMode)
		}
		diagnostics.JournalMode = strings.ToLower(journalMode)
		if err := secureExistingSidecars(path); err != nil {
			return nil, err
		}
	}
	if err := validateSQLiteSchema(db); err != nil {
		return nil, err
	}
	// The cap must be far ABOVE the realistic concurrent-writer count, and it must
	// exist. Both halves were learned the hard way:
	//
	// Too tight starves readers. With _txlock=immediate a writer issues BEGIN
	// IMMEDIATE at BeginTx and then HOLDS its pooled connection for the whole
	// busy_timeout while it waits its turn. At a cap of 4, four parked writers own
	// the entire pool and a read — which under WAL must never block on a writer —
	// queues in database/sql instead: measured 2.74s for List() versus ~1ms with
	// headroom. The TUI's Flows pane is exactly that read.
	//
	// No cap at all leaks descriptors permanently. SQLite's unix VFS cannot close
	// an fd while the process holds POSIX locks on that inode, so under WAL it
	// parks fds on a pending list instead of closing them. Idle-closing a pooled
	// connection therefore does NOT return its descriptor: measured, 300
	// concurrent reads left 285 fds held for the life of the process, and a second
	// burst grew that to 294 — the default MaxIdleConns does not bound it or slow
	// it down. Sharing one Store per process bounds the number of POOLS, not the
	// descriptors one pool accumulates. Store.Close does reclaim all of them,
	// which is why the per-operation CLI paths close.
	//
	// 64 holds the fd set at ~68 and clears any fan-out this app produces (every
	// newFlowStore caller in model/ is per-user-action; nothing batches). Note it
	// is a cliff, not a gradient: reads stall once parked writers reach cap-1.
	// The structural alternative, if that ever binds, is two handles on the same
	// file — a writer pool capped at 1 so writers queue in Go instead of parking
	// in BEGIN IMMEDIATE, plus a small reader pool.
	db.SetMaxOpenConns(64)
	closeOnError = false
	backend := &sqliteBackend{db: db, root: root, role: opts.role, diagnostics: diagnostics}
	// The live-handle guard's baseline, captured at open. Both seams are fields
	// rather than package functions so a test can count sidecar reads and drive
	// the throttle without sleeping.
	backend.generationClock = time.Now
	backend.readGeneration = func() string {
		sidecar, ok := readSidecar(root)
		if !ok {
			return ""
		}
		return sidecar.GenerationID
	}
	backend.readUserVersion = func() (int64, error) {
		var observed int64
		err := db.QueryRow("PRAGMA user_version").Scan(&observed)
		return observed, err
	}
	if observed, err := backend.readUserVersion(); err == nil {
		backend.guard.openUserVersion = observed
	}
	backend.guard.openGeneration = backend.readGeneration()
	backend.beginTx = func(ctx context.Context) (sqliteTransaction, error) {
		if err := backend.guardWrite(); err != nil {
			return nil, err
		}
		return db.BeginTx(ctx, nil)
	}
	backend.queryListRows = func(query string, args ...any) (flowListRows, error) {
		return db.Query(query, args...)
	}
	return backend, nil
}

// inspectOpenRoot records the root's mode as found. For a writer or a migrator
// SecureCanonicalRoot has already forced 0700, so this is a restatement; for a
// reader it is the substitute for the 0700 assertion that open no longer makes.
func inspectOpenRoot(root string) (OpenDiagnostics, error) {
	info, err := os.Stat(root)
	if err != nil {
		return OpenDiagnostics{}, fmt.Errorf("inspect flow store root: %w", err)
	}
	diagnostics := OpenDiagnostics{DirectoryMode: info.Mode().Perm()}
	if diagnostics.DirectoryMode != artifacts.DirPerm {
		diagnostics.Warnings = append(diagnostics.Warnings, fmt.Sprintf(
			"flow store root %q has permissions %04o, not %04o", root,
			diagnostics.DirectoryMode, artifacts.DirPerm))
	}
	return diagnostics, nil
}

// readUserVersionRO reads PRAGMA user_version without writing anything.
//
// The only other pre-validation open in the tree is inside
// migrateAuthoritativeDatabase, and it is mode=rw + _txlock=immediate — which a
// reader must not use and which fails on the read-only directory `db inspect`
// exists to report, turning a clean refusal into a write error. The open error
// is returned unwrapped so the store and Inspect agree on the error shape.
func readUserVersionRO(path string) (int64, error) {
	millis, err := sqliteBusyTimeoutMillis(defaultLockTimeout)
	if err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{
		"mode":    {"ro"},
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis), "query_only(1)"},
	}))
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var userVersion int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return 0, err
	}
	return userVersion, nil
}

// secureExistingSidecars tightens WAL/SHM files that a previous process may have
// created under a looser mode. Absent sidecars are not an error: SQLite drops
// them on a clean close and recreates them, inheriting the now-correct mode.
func secureExistingSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		info, err := os.Lstat(sidecar)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect flow database sidecar %q: %w", sidecar, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("flow database sidecar %q must be a real regular file", sidecar)
		}
		if info.Mode().Perm() == artifacts.FilePerm {
			continue
		}
		if err := os.Chmod(sidecar, artifacts.FilePerm); err != nil {
			return fmt.Errorf("secure flow database sidecar %q: %w", sidecar, err)
		}
	}
	return nil
}

func sqliteBusyTimeoutMillis(timeout time.Duration) (int64, error) {
	if timeout <= 0 {
		timeout = defaultLockTimeout
	}
	if timeout > time.Duration(math.MaxInt64/1_000_000)*time.Millisecond {
		return 0, fmt.Errorf("flow lock timeout %s is too large", timeout)
	}
	millis := timeout / time.Millisecond
	if timeout%time.Millisecond != 0 {
		millis++
	}
	if millis < 1 {
		millis = 1
	}
	return int64(millis), nil
}

func sqliteDSN(path string, values map[string][]string) string {
	u := url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	for key, entries := range values {
		for _, value := range entries {
			query.Add(key, value)
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func initializeSQLiteSchema(db *sql.DB) error {
	if _, err := db.Exec(flowSchemaSQL); err != nil {
		return fmt.Errorf("initialize flow database schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", databaseSchemaVersion)); err != nil {
		return fmt.Errorf("stamp flow database schema version: %w", err)
	}
	return validateSQLiteSchema(db)
}

func readSchemaVersion(db *sql.DB) (int64, error) {
	var version int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read flow database schema version: %w", err)
	}
	return version, nil
}

func validateSQLiteSchema(db *sql.DB) error {
	// Version first, so a database from a newer approach reports that plainly
	// instead of dumping a column-set diff the operator cannot interpret.
	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if version > databaseSchemaVersion {
		return fmt.Errorf("%w (database schema %d, this build supports %d); upgrade approach",
			errDatabaseFromNewerBuild, version, databaseSchemaVersion)
	}
	if version != databaseSchemaVersion {
		return fmt.Errorf("flow database schema %d requires bootstrap migration to %d", version, databaseSchemaVersion)
	}
	return validateSQLiteSchemaVersion(db, databaseSchemaVersion)
}

// validateSQLiteSchemaVersion checks one exact physical generation. The
// bootstrap migrator uses v1 to reject corrupt or arbitrary v0/v1 layouts
// before any ALTER TABLE statement runs; normal readers validate only v7.
func validateSQLiteSchemaVersion(db *sql.DB, version int64) error {
	// An empty file reads as version 0 with no tables, so the version check above
	// cannot catch it and the column comparison below would report it as a diff
	// against an empty column set — the unreadable dump this check exists to
	// avoid. Name the real condition instead.
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		return fmt.Errorf("validate flow database contents: %w", err)
	}
	if tables == 0 {
		return errors.New("flow database is empty or was never initialized")
	}
	if err := validateSQLiteSchemaObjects(db, version); err != nil {
		return err
	}
	if err := validateSQLiteFlowTableDefinition(db, version); err != nil {
		return err
	}
	rows, err := db.Query("PRAGMA table_xinfo(flows)")
	if err != nil {
		return fmt.Errorf("validate flow database schema: %w", err)
	}
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			_ = rows.Close()
			return fmt.Errorf("validate flow database schema columns: %w", err)
		}
		defaultText := "<nil>"
		if defaultValue != nil {
			defaultText = fmt.Sprint(defaultValue)
		}
		columns = append(columns, name+":"+strings.ToUpper(columnType)+":"+strconv.Itoa(notNull)+":"+strconv.Itoa(primaryKey)+":"+defaultText+":"+strconv.Itoa(hidden))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("validate flow database schema columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close flow database schema rows: %w", err)
	}
	var wantColumns []string
	switch version {
	case 1:
		wantColumns = []string{"flow_id:TEXT:0:1:<nil>:0", "repo_path:TEXT:1:0:<nil>:0", "status:TEXT:1:0:<nil>:0", "updated_at:TEXT:1:0:<nil>:0", "record:BLOB:1:0:<nil>:0"}
	case 2:
		wantColumns = []string{"flow_id:TEXT:0:1:<nil>:0", "repo_path:TEXT:1:0:<nil>:0", "status:TEXT:1:0:<nil>:0", "updated_at:TEXT:1:0:<nil>:0", "record:BLOB:1:0:<nil>:0", "bead_id:TEXT:1:0:'':0", "epic_id:TEXT:1:0:'':0"}
	case 3, 4, 5:
		wantColumns = []string{"flow_id:TEXT:0:1:<nil>:0", "repo_path:TEXT:1:0:<nil>:0", "status:TEXT:1:0:<nil>:0", "updated_at:TEXT:1:0:<nil>:0", "record:BLOB:1:0:<nil>:0", "bead_id:TEXT:1:0:'':0", "epic_id:TEXT:1:0:'':0", "prepared_at:TEXT:1:0:'':0"}
	case 6:
		wantColumns = []string{"flow_id:TEXT:0:1:<nil>:0", "repo_path:TEXT:1:0:<nil>:0", "status:TEXT:1:0:<nil>:0", "updated_at:TEXT:1:0:<nil>:0", "record:BLOB:1:0:<nil>:0", "bead_id:TEXT:1:0:'':0", "epic_id:TEXT:1:0:'':0", "prepared_at:TEXT:1:0:'':0", "preparation_nonce:TEXT:1:0:'':0"}
	case 7:
		wantColumns = []string{"flow_id:TEXT:0:1:<nil>:0", "repo_path:TEXT:1:0:<nil>:0", "status:TEXT:1:0:<nil>:0", "updated_at:TEXT:1:0:<nil>:0", "record:BLOB:1:0:<nil>:0", "bead_id:TEXT:1:0:'':0", "epic_id:TEXT:1:0:'':0", "prepared_at:TEXT:1:0:'':0", "preparation_nonce:TEXT:1:0:'':0", "recovery_generation:INTEGER:1:0:0:0"}
	default:
		return fmt.Errorf("no flow database schema contract for version %d", version)
	}
	if !equalStrings(columns, wantColumns) {
		return fmt.Errorf("flow database has incompatible flows columns: got %v, want %v", columns, wantColumns)
	}
	if err := validateSQLiteIndexes(db); err != nil {
		return err
	}
	if err := validateSQLiteBeadCompatibilityTrigger(db, version); err != nil {
		return err
	}
	if version >= 3 {
		if err := validateSQLiteEpicProgressionTable(db); err != nil {
			return err
		}
		if err := validateSQLitePreparedCompatibilityTrigger(db); err != nil {
			return err
		}
		if version >= 4 {
			if err := validateSQLiteEpicProgressionDoneCompatibilityTriggers(db); err != nil {
				return err
			}
			if version >= 5 {
				if err := validateSQLiteProgressionClaimCompatibilityTrigger(db); err != nil {
					return err
				}
			}
			if version >= 6 {
				if err := validateSQLitePreparationNonceCompatibilityTrigger(db); err != nil {
					return err
				}
				if version >= 7 {
					return validateSQLiteRecoveryGenerationCompatibilityTrigger(db)
				}
				return nil
			}
			return nil
		}
	}
	return nil
}

func validateSQLiteFlowTableDefinition(db *sql.DB, version int64) error {
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='table' AND name='flows'").Scan(&got); err != nil {
		return fmt.Errorf("validate flow database table definition: %w", err)
	}
	var want string
	switch version {
	case 1:
		want = flowTableSchemaV1
	case 2:
		want = flowTableSchemaV2
	case 3:
		want = flowTableSchemaV3
	case 4:
		want = flowTableSchemaV4
	case 5:
		want = flowTableSchemaV5
	case 6:
		want = flowTableSchemaV6
	case 7:
		want = flowTableSchemaV7
	default:
		return fmt.Errorf("no flow database table contract for version %d", version)
	}
	got = normalizeSQLiteSchemaSQL(got)
	want = normalizeSQLiteSchemaSQL(want)
	if got != want {
		return fmt.Errorf("flow database has incompatible flows table definition: got %q, want %q", got, want)
	}
	return nil
}

func normalizeSQLiteSchemaSQL(statement string) string {
	normalized := strings.Join(strings.Fields(statement), " ")
	normalized = strings.Replace(normalized, "CREATE TABLE IF NOT EXISTS ", "CREATE TABLE ", 1)
	normalized = strings.Replace(normalized, "CREATE TRIGGER IF NOT EXISTS ", "CREATE TRIGGER ", 1)
	for _, spacing := range [][2]string{{" (", "("}, {"( ", "("}, {" )", ")"}, {" ,", ","}, {", ", ","}} {
		normalized = strings.ReplaceAll(normalized, spacing[0], spacing[1])
	}
	return normalized
}

func validateSQLiteSchemaObjects(db *sql.DB, version int64) error {
	rows, err := db.Query("SELECT type, name, tbl_name FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*' ORDER BY type, name")
	if err != nil {
		return fmt.Errorf("validate flow database schema objects: %w", err)
	}
	var objects []string
	for rows.Next() {
		var objectType, name, tableName string
		if err := rows.Scan(&objectType, &name, &tableName); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan flow database schema objects: %w", err)
		}
		objects = append(objects, objectType+":"+name+":"+tableName)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate flow database schema objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close flow database schema object rows: %w", err)
	}
	want := []string{
		"index:idx_flows_repo_updated:flows",
		"index:idx_flows_status_updated:flows",
		"index:idx_flows_updated:flows",
		"table:flows:flows",
	}
	if version >= 2 {
		want = append(want, "trigger:guard_linked_flow_record_update:flows")
	}
	if version >= 3 {
		want = append(want,
			"table:epic_progressions:epic_progressions",
			"trigger:guard_prepared_flow_record_update:flows",
		)
		if version >= 4 {
			want = append(want,
				"trigger:guard_epic_progression_done_insert:epic_progressions",
				"trigger:guard_epic_progression_done_record_update:epic_progressions",
			)
			if version >= 5 {
				want = append(want, "trigger:guard_progression_claim_record_update:flows")
			}
			if version >= 6 {
				want = append(want, "trigger:guard_preparation_nonce_update:flows")
				if version >= 7 {
					want = append(want, "trigger:guard_recovered_launch_state_update:flows")
				}
			}
		}
		sort.Strings(want)
	}
	if !equalStrings(objects, want) {
		return fmt.Errorf("flow database has incompatible schema objects: got %v, want %v", objects, want)
	}
	return nil
}

func validateSQLiteBeadCompatibilityTrigger(db *sql.DB, version int64) error {
	if version == 1 {
		return nil
	}
	if version != 2 && version != 3 && version != 4 && version != 5 && version != 6 && version != 7 {
		return fmt.Errorf("no flow database trigger contract for version %d", version)
	}
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='guard_linked_flow_record_update'").Scan(&got); err != nil {
		return fmt.Errorf("validate flow database Bead compatibility trigger: %w", err)
	}
	got = normalizeSQLiteSchemaSQL(got)
	want := normalizeSQLiteSchemaSQL(flowBeadCompatibilityTrigger)
	if got != want {
		return fmt.Errorf("flow database has incompatible Bead compatibility trigger: got %q, want %q", got, want)
	}
	return nil
}

func validateSQLitePreparedCompatibilityTrigger(db *sql.DB) error {
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='guard_prepared_flow_record_update'").Scan(&got); err != nil {
		return fmt.Errorf("validate flow database preparation compatibility trigger: %w", err)
	}
	if normalizeSQLiteSchemaSQL(got) != normalizeSQLiteSchemaSQL(flowPreparedCompatibilityTrigger) {
		return fmt.Errorf("flow database has incompatible preparation compatibility trigger: got %q, want %q", normalizeSQLiteSchemaSQL(got), normalizeSQLiteSchemaSQL(flowPreparedCompatibilityTrigger))
	}
	return nil
}

func validateSQLiteProgressionClaimCompatibilityTrigger(db *sql.DB) error {
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='guard_progression_claim_record_update'").Scan(&got); err != nil {
		return fmt.Errorf("validate flow database progression claim compatibility trigger: %w", err)
	}
	if normalizeSQLiteSchemaSQL(got) != normalizeSQLiteSchemaSQL(flowProgressionClaimCompatibilityTrigger) {
		return fmt.Errorf("flow database has incompatible progression claim compatibility trigger: got %q, want %q", normalizeSQLiteSchemaSQL(got), normalizeSQLiteSchemaSQL(flowProgressionClaimCompatibilityTrigger))
	}
	return nil
}

func validateSQLiteEpicProgressionDoneCompatibilityTriggers(db *sql.DB) error {
	for _, contract := range []struct {
		name string
		want string
	}{
		{name: "guard_epic_progression_done_insert", want: epicProgressionDoneInsertCompatibilityTrigger},
		{name: "guard_epic_progression_done_record_update", want: epicProgressionDoneUpdateCompatibilityTrigger},
	} {
		var got string
		if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?", contract.name).Scan(&got); err != nil {
			return fmt.Errorf("validate epic progression done compatibility trigger %q: %w", contract.name, err)
		}
		if normalizeSQLiteSchemaSQL(got) != normalizeSQLiteSchemaSQL(contract.want) {
			return fmt.Errorf("flow database has incompatible epic progression done compatibility trigger %q: got %q, want %q", contract.name, normalizeSQLiteSchemaSQL(got), normalizeSQLiteSchemaSQL(contract.want))
		}
	}
	return nil
}

func validateSQLitePreparationNonceCompatibilityTrigger(db *sql.DB) error {
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='guard_preparation_nonce_update'").Scan(&got); err != nil {
		return fmt.Errorf("validate flow database preparation nonce compatibility trigger: %w", err)
	}
	if normalizeSQLiteSchemaSQL(got) != normalizeSQLiteSchemaSQL(flowPreparationNonceCompatibilityTrigger) {
		return fmt.Errorf("flow database has incompatible preparation nonce compatibility trigger: got %q, want %q", normalizeSQLiteSchemaSQL(got), normalizeSQLiteSchemaSQL(flowPreparationNonceCompatibilityTrigger))
	}
	return nil
}

func validateSQLiteRecoveryGenerationCompatibilityTrigger(db *sql.DB) error {
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='guard_recovered_launch_state_update'").Scan(&got); err != nil {
		return fmt.Errorf("validate flow database recovery generation compatibility trigger: %w", err)
	}
	if normalizeSQLiteSchemaSQL(got) != normalizeSQLiteSchemaSQL(flowRecoveryGenerationCompatibilityTrigger) {
		return fmt.Errorf("flow database has incompatible recovery generation compatibility trigger: got %q, want %q", normalizeSQLiteSchemaSQL(got), normalizeSQLiteSchemaSQL(flowRecoveryGenerationCompatibilityTrigger))
	}
	return nil
}

func validateSQLiteEpicProgressionTable(db *sql.DB) error {
	var got string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE type='table' AND name='epic_progressions'").Scan(&got); err != nil {
		return fmt.Errorf("validate epic progression table definition: %w", err)
	}
	if normalizeSQLiteSchemaSQL(got) != normalizeSQLiteSchemaSQL(epicProgressionTableSchema) {
		return fmt.Errorf("flow database has incompatible epic progression table definition: got %q, want %q", normalizeSQLiteSchemaSQL(got), normalizeSQLiteSchemaSQL(epicProgressionTableSchema))
	}
	rows, err := db.Query("PRAGMA table_xinfo(epic_progressions)")
	if err != nil {
		return fmt.Errorf("validate epic progression columns: %w", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return fmt.Errorf("scan epic progression columns: %w", err)
		}
		defaultText := "<nil>"
		if defaultValue != nil {
			defaultText = fmt.Sprint(defaultValue)
		}
		columns = append(columns, name+":"+strings.ToUpper(columnType)+":"+strconv.Itoa(notNull)+":"+strconv.Itoa(primaryKey)+":"+defaultText+":"+strconv.Itoa(hidden))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate epic progression columns: %w", err)
	}
	want := []string{"repo_path:TEXT:1:1:<nil>:0", "epic_id:TEXT:1:2:<nil>:0", "enabled:INTEGER:1:0:<nil>:0", "updated_at:TEXT:1:0:<nil>:0", "record:BLOB:1:0:<nil>:0"}
	if !equalStrings(columns, want) {
		return fmt.Errorf("flow database has incompatible epic progression columns: got %v, want %v", columns, want)
	}
	return nil
}

func validateSQLiteIndexes(db *sql.DB) error {
	rows, err := db.Query("PRAGMA index_list(flows)")
	if err != nil {
		return fmt.Errorf("validate flow database indexes: %w", err)
	}
	properties := make(map[string]string)
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan flow database indexes: %w", err)
		}
		properties[name] = strconv.Itoa(unique) + ":" + origin + ":" + strconv.Itoa(partial)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate flow database indexes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close flow database index rows: %w", err)
	}
	wantProperties := map[string]string{
		"idx_flows_updated":        "0:c:0",
		"idx_flows_repo_updated":   "0:c:0",
		"idx_flows_status_updated": "0:c:0",
		"sqlite_autoindex_flows_1": "1:pk:0",
	}
	if len(properties) != len(wantProperties) {
		return fmt.Errorf("flow database has incompatible index properties: got %v, want %v", properties, wantProperties)
	}
	for name, want := range wantProperties {
		if properties[name] != want {
			return fmt.Errorf("flow database index %q has properties %q, want %q", name, properties[name], want)
		}
	}
	indexContracts := []struct {
		name    string
		columns []string
	}{
		{name: "idx_flows_updated", columns: []string{"updated_at:1:BINARY", "flow_id:0:BINARY"}},
		{name: "idx_flows_repo_updated", columns: []string{"repo_path:0:BINARY", "updated_at:1:BINARY", "flow_id:0:BINARY"}},
		{name: "idx_flows_status_updated", columns: []string{"status:0:BINARY", "updated_at:1:BINARY", "flow_id:0:BINARY"}},
	}
	for _, contract := range indexContracts {
		index := contract.name
		indexRows, err := db.Query("PRAGMA index_xinfo('" + index + "')")
		if err != nil {
			return fmt.Errorf("validate flow database index %q columns: %w", index, err)
		}
		var gotColumns []string
		for indexRows.Next() {
			var sequence, columnID, descending, key int
			var name, collation sql.NullString
			if err := indexRows.Scan(&sequence, &columnID, &name, &descending, &collation, &key); err != nil {
				_ = indexRows.Close()
				return fmt.Errorf("scan flow database index %q: %w", index, err)
			}
			if key == 1 {
				collationName := "<nil>"
				if collation.Valid {
					collationName = strings.ToUpper(collation.String)
				}
				gotColumns = append(gotColumns, name.String+":"+strconv.Itoa(descending)+":"+collationName)
			}
		}
		if err := indexRows.Err(); err != nil {
			_ = indexRows.Close()
			return fmt.Errorf("iterate flow database index %q: %w", index, err)
		}
		if err := indexRows.Close(); err != nil {
			return fmt.Errorf("close flow database index %q rows: %w", index, err)
		}
		if !equalStrings(gotColumns, contract.columns) {
			return fmt.Errorf("flow database index %q has columns %v, want %v", index, gotColumns, contract.columns)
		}
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (b *sqliteBackend) get(flowID string) (storedFlow, bool, error) {
	if err := validateFlowID(flowID); err != nil {
		return storedFlow{}, false, err
	}
	return queryStoredFlow(b.db.QueryRow(
		"SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows WHERE flow_id = ?", flowID,
	), flowID)
}

func queryStoredFlow(row interface{ Scan(...any) error }, requestedID string) (storedFlow, bool, error) {
	var flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce string
	var record []byte
	if err := row.Scan(&flowID, &repoPath, &status, &updatedAt, &beadID, &epicID, &preparedAt, &preparationNonce, &record); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storedFlow{}, false, nil
		}
		return storedFlow{}, false, fmt.Errorf("read flow %q row: %w", requestedID, err)
	}
	decoded, err := decodeStoredFlowWithPreparation(flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce, record)
	if err != nil {
		return storedFlow{}, false, err
	}
	return decoded, true, nil
}

func (b *sqliteBackend) list(filter FlowFilter) ([]storedFlow, error) {
	query := "SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows"
	var args []any
	if filter.RepoPath != "" {
		query += " WHERE repo_path = ?"
		args = append(args, filepath.Clean(filter.RepoPath))
	}
	query += " ORDER BY updated_at DESC, flow_id ASC"
	rows, err := b.queryListRows(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	flows := make([]storedFlow, 0)
	partial := &PartialListError{}
	for rows.Next() {
		var flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce string
		var record []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &beadID, &epicID, &preparedAt, &preparationNonce, &record); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan flow list row: %w", err)
		}
		decoded, err := decodeStoredFlowWithPreparation(flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce, record)
		if err != nil {
			partial.Entries = append(partial.Entries, PartialListEntry{FlowID: flowID, Cause: err})
			continue
		}
		flows = append(flows, decoded)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate flow list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close flow list: %w", err)
	}
	if len(partial.Entries) > 0 {
		return flows, partial
	}
	return flows, nil
}

func (b *sqliteBackend) delete(flowID string) error {
	if err := b.guardWrite(); err != nil {
		return err
	}
	if err := validateFlowID(flowID); err != nil {
		return err
	}
	result, err := b.db.Exec("DELETE FROM flows WHERE flow_id = ?", flowID)
	if err != nil {
		return fmt.Errorf("delete flow %q: %w", flowID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delete result for flow %q: %w", flowID, err)
	}
	if rows == 0 {
		return flowNotFoundError(flowID)
	}
	return nil
}

func (b *sqliteBackend) update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error) {
	if err := validateFlowID(flowID); err != nil {
		return FlowRecord{}, err
	}
	tx, err := b.beginTx(context.Background())
	if err != nil {
		return FlowRecord{}, fmt.Errorf("begin flow update %q: %w", flowID, err)
	}
	// Only a panic in mutate can reach this: after a normal return the Commit
	// below has already finished the transaction, so Rollback yields ErrTxDone.
	// Without it a panicking mutate would abandon a BEGIN IMMEDIATE and leave
	// the connection holding the database-wide write lock, so every later write
	// would fail with SQLITE_BUSY while reads kept working.
	defer func() { _ = tx.Rollback() }()
	record, callbackErr := mutate(sqliteSession{tx: tx, flowID: flowID})
	if err := tx.Commit(); err != nil {
		rollbackErr := tx.Rollback()
		contextText := ""
		if callbackErr != nil {
			contextText = fmt.Sprintf("; callback error: %v", callbackErr)
		}
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			contextText += fmt.Sprintf("; rollback error: %v", rollbackErr)
		}
		return FlowRecord{}, fmt.Errorf("commit flow update %q%s: %w", flowID, contextText, err)
	}
	return record, callbackErr
}

func (b *sqliteBackend) close() error {
	if err := b.db.Close(); err != nil {
		return fmt.Errorf("close flow database: %w", err)
	}
	return nil
}

func (b *sqliteBackend) allocateID(title string, now time.Time) (string, error) {
	opts := artifacts.IDOptions{Title: title, FallbackSlug: "flow", Kind: "flow", Now: now}
	candidates := artifacts.TimestampedIDCandidates(opts)
	for _, candidate := range candidates {
		var exists int
		if err := b.db.QueryRow("SELECT EXISTS(SELECT 1 FROM flows WHERE flow_id = ?)", candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("check flow id collision: %w", err)
		}
		if exists == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate a unique flow id for %q after %d attempts", title, len(candidates))
}

type sqliteSession struct {
	tx     sqliteTransaction
	flowID string
}

func (s sqliteSession) get() (storedFlow, bool, error) {
	return queryStoredFlow(s.tx.QueryRow(
		"SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows WHERE flow_id = ?", s.flowID,
	), s.flowID)
}

func (s sqliteSession) exists() (bool, error) {
	var exists int
	if err := s.tx.QueryRow("SELECT EXISTS(SELECT 1 FROM flows WHERE flow_id = ?)", s.flowID).Scan(&exists); err != nil {
		return false, err
	}
	return exists != 0, nil
}

// beadLinkedFlows reads through the session's own transaction, never through
// b.db: sqliteBackend.queryListRows would read outside the writer transaction
// and reopen the race the guard exists to close.
func (s sqliteSession) beadLinkedFlows(repoPath string) ([]beadFlowCandidate, error) {
	rows, err := s.tx.Query(`
SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record
FROM flows
WHERE repo_path = ? AND bead_id <> '' AND flow_id <> ?
ORDER BY updated_at DESC, flow_id ASC`, filepath.Clean(repoPath), s.flowID)
	if err != nil {
		return nil, fmt.Errorf("list bead-linked flows for %q: %w", repoPath, err)
	}
	defer func() { _ = rows.Close() }()
	flows := make([]beadFlowCandidate, 0)
	for rows.Next() {
		var flowID, rowRepoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce string
		var record []byte
		if err := rows.Scan(&flowID, &rowRepoPath, &status, &updatedAt, &beadID, &epicID, &preparedAt, &preparationNonce, &record); err != nil {
			return nil, fmt.Errorf("scan bead-linked flow row: %w", err)
		}
		candidate := beadFlowCandidate{flowID: flowID, beadID: beadID}
		decoded, err := decodeStoredFlowWithPreparation(flowID, rowRepoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce, record)
		if err != nil {
			// Reported, not fatal and not dropped — see the interface
			// contract. The bead_id projection above is what lets the store
			// tell an unrelated corrupt row from one that may still hold the
			// requested Bead.
			candidate.decodeErr = err
		} else {
			candidate.record = decoded.record
		}
		flows = append(flows, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bead-linked flows: %w", err)
	}
	return flows, nil
}

func (s sqliteSession) save(record FlowRecord) error {
	if record.FlowID != s.flowID {
		return fmt.Errorf("flow update %q cannot save record %q", s.flowID, record.FlowID)
	}
	data, projection, err := encodeStoredFlow(record)
	if err != nil {
		return err
	}
	_, err = s.tx.Exec(`
INSERT INTO flows(flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(flow_id) DO UPDATE SET
    repo_path=excluded.repo_path,
    status=excluded.status,
    updated_at=excluded.updated_at,
    bead_id=excluded.bead_id,
	epic_id=excluded.epic_id,
	prepared_at=excluded.prepared_at,
	preparation_nonce=excluded.preparation_nonce,
	recovery_generation=flows.recovery_generation+1,
    record=excluded.record`,
		projection.flowID, projection.repoPath, projection.status, projection.updatedAt, projection.beadID, projection.epicID, projection.preparedAt, projection.preparationNonce, data)
	if err != nil {
		return fmt.Errorf("save flow %q: %w", record.FlowID, err)
	}
	return nil
}

func (s sqliteSession) savePhaseAgentSettings(update phaseAgentSettingsSave) error {
	var data []byte
	if err := s.tx.QueryRow("SELECT record FROM flows WHERE flow_id = ?", s.flowID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return flowNotFoundError(s.flowID)
		}
		return fmt.Errorf("read flow %q for settings-only save: %w", s.flowID, err)
	}
	patched, err := patchStoredFlowPhaseAgentSettings(data, update)
	if err != nil {
		return fmt.Errorf("patch flow %q agent settings: %w", s.flowID, err)
	}
	updatedAt, err := formatStorageTime(update.RecordUpdatedAt)
	if err != nil {
		return fmt.Errorf("save flow %q agent settings: %w", s.flowID, err)
	}
	result, err := s.tx.Exec("UPDATE flows SET updated_at = ?, recovery_generation = recovery_generation + 1, record = ? WHERE flow_id = ?", updatedAt, patched, s.flowID)
	if err != nil {
		return fmt.Errorf("save flow %q agent settings: %w", s.flowID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read flow %q settings-only save result: %w", s.flowID, err)
	}
	if rows != 1 {
		return fmt.Errorf("save flow %q agent settings updated %d rows, want 1", s.flowID, rows)
	}
	return nil
}
