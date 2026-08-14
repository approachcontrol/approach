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
const databaseSchemaVersion = 1

// errDatabaseFromNewerBuild marks the one validation failure that is NOT a
// damaged database. The file is fine; this binary is old. It must stay
// distinguishable, because the recovery advice attached to a corrupt database —
// move it aside and re-migrate from flows/ — would silently roll a perfectly
// good corpus back to its migration-day snapshot. The only correct action is
// the one the message already states: upgrade approach.
var errDatabaseFromNewerBuild = errors.New("flow database was written by a newer version of approach")

const flowSchemaSQL = `
CREATE TABLE IF NOT EXISTS flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_flows_updated
    ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX IF NOT EXISTS idx_flows_repo_updated
    ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX IF NOT EXISTS idx_flows_status_updated
    ON flows(status, updated_at DESC, flow_id ASC);
`

type sqliteBackend struct {
	db *sql.DB
	// root is the canonical, symlink-resolved store root. Kept because it is the
	// evidence that secureCanonicalRoot's resolution reached the backend.
	root    string
	beginTx func(context.Context) (sqliteTransaction, error)
}

type sqliteTransaction interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
	Commit() error
	Rollback() error
}

func openSQLiteBackend(root string, lockTimeout time.Duration) (*sqliteBackend, error) {
	path := filepath.Join(root, databaseFilename)
	millis, err := sqliteBusyTimeoutMillis(lockTimeout)
	if err != nil {
		return nil, err
	}
	dsn := sqliteDSN(path, map[string][]string{
		"mode":    {"rw"},
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis)},
		"_txlock": {"immediate"},
	})
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
	if err := secureExistingSidecars(path); err != nil {
		return nil, err
	}
	if err := validateSQLiteSchema(db); err != nil {
		return nil, err
	}
	if err := stampSchemaVersionIfUnset(db); err != nil {
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
	backend := &sqliteBackend{db: db, root: root}
	backend.beginTx = func(ctx context.Context) (sqliteTransaction, error) {
		return db.BeginTx(ctx, nil)
	}
	return backend, nil
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

// stampSchemaVersionIfUnset upgrades a database written before the stamp
// existed. Databases from a newer approach are rejected by validateSQLiteSchema
// before this runs, so this only ever moves 0 forward.
func stampSchemaVersionIfUnset(db *sql.DB) error {
	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if version != 0 {
		return nil
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", databaseSchemaVersion)); err != nil {
		return fmt.Errorf("stamp flow database schema version: %w", err)
	}
	return nil
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
	// instead of dumping a column-set diff the operator cannot interpret. A 0
	// means "written before the stamp existed" and is accepted: the column and
	// index checks below are the real compatibility gate for that generation.
	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if version > databaseSchemaVersion {
		return fmt.Errorf("%w (database schema %d, this build supports %d); upgrade approach",
			errDatabaseFromNewerBuild, version, databaseSchemaVersion)
	}
	// An empty file reads as version 0 with no tables, so the version check above
	// cannot catch it and the column comparison below would report it as a diff
	// against an empty column set — the unreadable dump this check exists to
	// avoid. Name the real condition instead.
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		return fmt.Errorf("validate flow database contents: %w", err)
	}
	if tables == 0 {
		return fmt.Errorf("flow database is empty or was never initialized")
	}
	rows, err := db.Query("PRAGMA table_info(flows)")
	if err != nil {
		return fmt.Errorf("validate flow database schema: %w", err)
	}
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("validate flow database schema columns: %w", err)
		}
		columns = append(columns, name+":"+strings.ToUpper(columnType)+":"+strconv.Itoa(notNull)+":"+strconv.Itoa(primaryKey))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("validate flow database schema columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close flow database schema rows: %w", err)
	}
	wantColumns := []string{"flow_id:TEXT:0:1", "repo_path:TEXT:1:0", "status:TEXT:1:0", "updated_at:TEXT:1:0", "record:BLOB:1:0"}
	if !equalStrings(columns, wantColumns) {
		return fmt.Errorf("flow database has incompatible flows columns: got %v, want %v", columns, wantColumns)
	}
	wantIndexes := map[string][]string{
		"idx_flows_updated":        {"updated_at:1", "flow_id:0"},
		"idx_flows_repo_updated":   {"repo_path:0", "updated_at:1", "flow_id:0"},
		"idx_flows_status_updated": {"status:0", "updated_at:1", "flow_id:0"},
	}
	for index, wantColumns := range wantIndexes {
		var found int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name=? AND tbl_name='flows'", index).Scan(&found); err != nil {
			return fmt.Errorf("validate flow database index %q: %w", index, err)
		}
		if found != 1 {
			return fmt.Errorf("flow database is missing required index %q", index)
		}
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
				gotColumns = append(gotColumns, name.String+":"+strconv.Itoa(descending))
			}
		}
		if err := indexRows.Err(); err != nil {
			_ = indexRows.Close()
			return fmt.Errorf("iterate flow database index %q: %w", index, err)
		}
		if err := indexRows.Close(); err != nil {
			return fmt.Errorf("close flow database index %q rows: %w", index, err)
		}
		if !equalStrings(gotColumns, wantColumns) {
			return fmt.Errorf("flow database index %q has columns %v, want %v", index, gotColumns, wantColumns)
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
		"SELECT flow_id, repo_path, status, updated_at, record FROM flows WHERE flow_id = ?", flowID,
	), flowID)
}

func queryStoredFlow(row interface{ Scan(...any) error }, requestedID string) (storedFlow, bool, error) {
	var flowID, repoPath, status, updatedAt string
	var record []byte
	if err := row.Scan(&flowID, &repoPath, &status, &updatedAt, &record); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storedFlow{}, false, nil
		}
		return storedFlow{}, false, fmt.Errorf("read flow %q row: %w", requestedID, err)
	}
	decoded, err := decodeStoredFlow(flowID, repoPath, status, updatedAt, record)
	if err != nil {
		return storedFlow{}, false, err
	}
	return decoded, true, nil
}

func (b *sqliteBackend) list(filter FlowFilter) ([]storedFlow, error) {
	query := "SELECT flow_id, repo_path, status, updated_at, record FROM flows"
	var args []any
	if filter.RepoPath != "" {
		query += " WHERE repo_path = ?"
		args = append(args, filepath.Clean(filter.RepoPath))
	}
	query += " ORDER BY updated_at DESC, flow_id ASC"
	rows, err := b.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	var flows []storedFlow
	for rows.Next() {
		var flowID, repoPath, status, updatedAt string
		var record []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &record); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan flow list row: %w", err)
		}
		decoded, err := decodeStoredFlow(flowID, repoPath, status, updatedAt, record)
		if err != nil {
			_ = rows.Close()
			return nil, err
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
	return flows, nil
}

func (b *sqliteBackend) delete(flowID string) error {
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
		"SELECT flow_id, repo_path, status, updated_at, record FROM flows WHERE flow_id = ?", s.flowID,
	), s.flowID)
}

func (s sqliteSession) exists() (bool, error) {
	var exists int
	if err := s.tx.QueryRow("SELECT EXISTS(SELECT 1 FROM flows WHERE flow_id = ?)", s.flowID).Scan(&exists); err != nil {
		return false, err
	}
	return exists != 0, nil
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
INSERT INTO flows(flow_id, repo_path, status, updated_at, record)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(flow_id) DO UPDATE SET
    repo_path=excluded.repo_path,
    status=excluded.status,
    updated_at=excluded.updated_at,
    record=excluded.record`,
		projection.flowID, projection.repoPath, projection.status, projection.updatedAt, data)
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
	result, err := s.tx.Exec("UPDATE flows SET updated_at = ?, record = ? WHERE flow_id = ?", updatedAt, patched, s.flowID)
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
