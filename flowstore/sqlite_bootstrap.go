package flowstore

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/dblease"
	"github.com/approachcontrol/approach/internal/version"
)

const bootstrapLockFilename = ".approach.db.bootstrap.lock"

// migrationNoticeFilename records the cutover notice on disk. bootstrapWarnf
// writes to stderr, and the TUI takes the alternate screen within milliseconds
// of NewStore returning, so the stderr copy is realistically unreadable. The
// whole reversibility story of this design — "flows/ was left behind and you
// were told" — cannot rest on a message nobody sees.
const migrationNoticeFilename = "FLOW-MIGRATION-NOTICE.txt"

// bootstrapLockTimeout bounds the wait for the cutover lease. It is deliberately
// not the caller's per-operation lock timeout: migrating a large corpus can
// outlast the 5s default, and a second approach failing during a legitimate,
// progressing migration would look like a hang with no explanation.
const bootstrapLockTimeout = 2 * time.Minute

// bootstrapWarnf reports a non-fatal cutover notice. It exists because the
// migration deliberately leaves flows/ where it found it: a build without the
// SQLite backend keeps reading that directory, so the two views of the same
// state silently diverge and the operator has to be told. Tests override it.
var bootstrapWarnf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "approach: "+format+"\n", args...)
}

type cutoverState struct {
	database  bool
	legacy    bool
	tombstone bool
	stage     bool
}

// isDevelopmentBuild classifies this build. It is a var so tests can reach both
// branches: under `go test` the version ldflags are unset, so the ambient answer
// is always "development".
var isDevelopmentBuild = version.IsDevelopment

// errDevLiveMigrationRefused marks the guard's refusal so it stays
// distinguishable from a damaged predecessor schema. The distinction is
// load-bearing on the cutover-resume path: an unusable stage is discarded and
// rebuilt, which is right for corruption and catastrophic for a refusal, since
// the stage is intact and the operator was told to re-run with an
// acknowledgement.
var errDevLiveMigrationRefused = errors.New("development build refused to migrate the release artifact root")

// readUserVersion is the role gate's probe. A var so a test can stand in for a
// probe that could not answer — the interesting case is not a corrupt file but
// a database another process is holding, which is what makes the gate's
// behaviour on versionErr a safety property rather than a formality.
var readUserVersion = readUserVersionRO

func newSQLiteStoreBackend(opts backendOptions) (*sqliteBackend, error) {
	canonicalRoot, err := prepareStoreRoot(opts)
	if err != nil {
		return nil, err
	}
	opts.root = canonicalRoot
	// TOTAL ACQUISITION ORDER: the owners lease is scanned BEFORE the bootstrap
	// lock, never the reverse, and internal/dblease's doc comment says the same
	// from its side. The bootstrap lock is held for up to two minutes by a
	// running migration; taking it first and then discovering a refusal would
	// block every other open on a migration that was never going to happen.
	//
	// Only a migrator, and only when the database is actually behind. The
	// version probe here is the same lock-free read Inspect uses, so a
	// current-schema open — the overwhelmingly common case — does not read the
	// owners directory at all.
	if err := refuseMigrationForBlockingOwners(canonicalRoot, opts); err != nil {
		return nil, err
	}
	// The lock is taken for every role, deliberately: a reader must not observe
	// a half-promoted cutover. Note that acquiring it CREATES, chmods, and
	// writes the lock file, so even a reader writes this one path and a reader
	// against a genuinely read-only mount fails outright. Inspect is the
	// lock-free diagnostic for that case.
	release, err := artifacts.AcquireFileLockNoFollow(
		filepath.Join(canonicalRoot, bootstrapLockFilename),
		"flow database bootstrap lock (another approach process may be migrating this state root)",
		bootstrapLockTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("bootstrap flow database: %w", err)
	}
	dblease.Probe(dblease.ProbeBootstrapLock)
	defer release()

	state, err := inspectCutoverState(canonicalRoot)
	if err != nil {
		return nil, err
	}
	if state.database {
		databasePath := filepath.Join(canonicalRoot, databaseFilename)
		// Role first, then dev-live. The role check is cheaper and
		// build-independent, and a non-migrator has no business being told how
		// to acknowledge a migration it would refuse anyway. It returns ahead of
		// the describeUnusableDatabase wraps below on purpose: an unmigrated
		// database must never collect "move approach.db aside" advice.
		storedVersion, versionErr := readUserVersion(databasePath)
		if err := refuseUnmigratedForRole(storedVersion, versionErr, opts.role); err != nil {
			return nil, err
		}
		// A non-migrator that got past the gate with a READABLE user_version has
		// already been proved current: the gate refuses anything older and anything
		// newer, so databaseSchemaVersion is the only value left. Calling the
		// migration helper for it would open approach.db with mode=rw and ping it
		// purely to rediscover that, on every `flow list`, `serve`, and hook open.
		//
		// A non-migrator never enters the helper at all, INCLUDING when the probe
		// failed. Falling through on versionErr would be the incident this unit
		// exists to prevent: the probe can fail merely because another process
		// held the database past its busy timeout, and the helper — which has no
		// role check of its own — retries under the caller's longer lockTimeout,
		// so a `flow list` or session hook that lost one race would migrate a
		// predecessor database. An unreadable version instead falls to the
		// read-only validateAuthoritativeDatabase below, which reaches the same
		// describeUnusableDatabase classification without a write-capable open.
		outcome := migrationOutcome{}
		if opts.role == RoleMigrator {
			result, err := migrateAuthoritativeDatabase(databasePath, opts.lockTimeout, canonicalRoot, opts.backupDir, opts.allowDevLiveMigration)
			if err != nil {
				return nil, describeUnusableDatabase(canonicalRoot, err)
			}
			outcome = result
		}
		if err := validateAuthoritativeDatabase(databasePath); err != nil {
			return nil, describeUnusableDatabase(canonicalRoot, err)
		}
		// Still under the bootstrap lease. One call covers both the fresh
		// provenance entry and the repair of a sidecar that drifted from
		// user_version — the second is unreachable from inside
		// migrateAuthoritativeDatabase, whose early return fires first.
		//
		// The staleness is reported as OBSERVED, by every role including the
		// one that then repairs it. OpenDiagnostics answers "what did this open
		// find", and a migrator that reported a clean sidecar because it had
		// just rewritten one would hide the drift entirely.
		//
		// The outcome is what the helper REPORTED doing, never what the probe
		// above predicted: the probe can fail transiently while the helper — on
		// its own connection, under the longer lock timeout — goes on to
		// migrate, and inferring from the probe would stamp no provenance for a
		// migration that really happened.
		sidecarStale := sidecarDisagrees(canonicalRoot, databaseSchemaVersion)
		if opts.role == RoleMigrator {
			if err := reconcileSidecar(canonicalRoot, databaseSchemaVersion, outcome); err != nil {
				return nil, err
			}
		}
		// A stage left behind by a crash that still managed to promote is
		// obsolete the moment approach.db exists. Drop it here: leaving it would
		// resurrect a stale corpus as the "interrupted" stage if the user ever
		// removed approach.db to start over. A reader skips it — this is a
		// destructive filesystem mutation, and `flow list` must not delete a
		// staged database.
		if opts.role != RoleReader {
			if err := discardObsoleteStage(canonicalRoot); err != nil {
				return nil, err
			}
		}
		backend, err := openSQLiteBackend(opts)
		if err != nil {
			return nil, err
		}
		backend.diagnostics.SidecarStale = sidecarStale
		// The compatibility notice, on the one channel every surface already
		// reads: `db migrate` prints Warnings to stderr and the TUI renders
		// them beside controlplane.PathMismatchNotice. A notice rather than a
		// refusal, because user_version — the authority — is a version this
		// build opens, and the sidecar is a cache that never drives a decision.
		if notice := sidecarCompatibilityNotice(canonicalRoot); notice != "" {
			backend.diagnostics.Warnings = append(backend.diagnostics.Warnings, notice)
		}
		if _, err := presetRegistry(opts.presets); err != nil {
			_ = backend.db.Close()
			return nil, err
		}
		return backend, nil
	}

	presets, err := presetRegistry(opts.presets)
	if err != nil {
		return nil, err
	}
	if err := completeCutover(opts, state, presets); err != nil {
		return nil, err
	}
	// Creation is not migration, so it stays available to every role — but the
	// creation path produces a DELETE-mode database (buildStagedDatabase and
	// settleStagedSQLiteFile both assert DELETE) and WAL comes only from a
	// writer-privileged open. A reader that opened it directly would be left
	// holding a DELETE-mode database, so the post-creation open always runs as a
	// writer and a reader then reopens read-only. Two opens on the create path
	// only, never on the steady-state path.
	creationOpts := opts
	if creationOpts.role == RoleReader {
		creationOpts.role = RoleWriter
	}
	backend, err := openSQLiteBackend(creationOpts)
	if err != nil {
		return nil, err
	}
	if opts.role == RoleReader {
		if err := backend.db.Close(); err != nil {
			return nil, fmt.Errorf("close flow database creation handle: %w", err)
		}
		return openSQLiteBackend(opts)
	}
	return backend, nil
}

// prepareStoreRoot resolves the root the way opts.role is allowed to.
//
// The rule is unconditional: a RoleReader calls ResolveCanonicalRoot and never
// SecureCanonicalRoot, whatever rootExplicit says. SecureCanonicalRoot's chmods
// succeed on a user-owned 0755 or 0500 directory, so a reader that used it
// would repair the very state OpenDiagnostics and `db inspect` exist to report —
// and `approach serve` would tighten a 0755 root when its root came from config
// while merely warning when the same directory was passed with --state-root.
// What rootExplicit varies is only whether a MISSING root is created.
func prepareStoreRoot(opts backendOptions) (string, error) {
	if opts.role != RoleReader {
		return secureCanonicalRoot(opts.root)
	}
	if _, err := os.Stat(opts.root); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect flow store root: %w", err)
		}
		if opts.rootExplicit {
			// A typo'd root should say so rather than silently materialise an
			// empty tree under it.
			return "", fmt.Errorf("state root %s does not exist", opts.root)
		}
		if err := os.MkdirAll(opts.root, artifacts.DirPerm); err != nil {
			return "", fmt.Errorf("create flow store root: %w", err)
		}
	}
	return artifacts.ResolveCanonicalRoot(opts.root, "flow store root")
}

// refuseUnmigratedForRole is the role gate on the steady-state open path.
//
// It runs before migrateAuthoritativeDatabase and before any schema validation,
// so a non-migrator on a predecessor-schema root gets a refusal naming
// `approach db migrate` rather than validateSQLiteSchema's "requires bootstrap
// migration to 6", which names migration but not the role and offers no command.
//
// A user_version that cannot be READ falls through to today's path unchanged: a
// corrupt or garbage approach.db has no version to compare, and returning the
// raw error here would replace describeUnusableDatabase's classification of
// those files. Inspect is where a corrupt database gets a better answer.
func refuseUnmigratedForRole(storedVersion int64, readErr error, role Role) error {
	if readErr != nil {
		return nil
	}
	switch {
	case storedVersion > databaseSchemaVersion:
		// The required generation would come from the sidecar's
		// min_reader_generation / min_writer_generation; until that reader
		// exists, user_version is the only source, and a sidecar that disagrees
		// with it never drives a compatibility decision anyway.
		return refuseIncompatibleBuild(storedVersion, storedVersion)
	case storedVersion < databaseSchemaVersion && role != RoleMigrator:
		return refuseRoleMigration(storedVersion, role)
	default:
		return nil
	}
}

// refuseMigrationForBlockingOwners is the migrator's pre-lock gate.
//
// It answers "would this open advance the schema" with a lock-free
// user_version read, exactly as Inspect does, so it costs one read-only open
// on a migrator path that was about to open the database anyway — and nothing
// at all for a reader or a writer.
//
// A probe that cannot answer falls through unchanged. The version is then read
// again under the bootstrap lock by the migration itself, which is where an
// unreadable database gets its real classification; refusing here on a
// transient BUSY would turn a locked database into a migration failure.
func refuseMigrationForBlockingOwners(canonicalRoot string, opts backendOptions) error {
	if opts.role != RoleMigrator {
		return nil
	}
	storedVersion, err := readUserVersion(filepath.Join(canonicalRoot, databaseFilename))
	if err != nil || storedVersion >= databaseSchemaVersion {
		return nil
	}
	return refuseMigrationForOwners(canonicalRoot, opts.ownerNonce)
}

// postMigrationValidation re-proves a just-migrated database is the thing this
// build writes, after the commit and before the lease is released. It is a var
// so a test can force the failure path: the whole point of the check is what
// happens when it fails, and nothing else can produce that state.
var postMigrationValidation = func(db *sql.DB, target int64, tolerated map[string]bool) error {
	if err := validateSQLiteSchemaVersion(db, target); err != nil {
		return err
	}
	return validateMigratedRecordRoundTrip(db, tolerated)
}

// migratedRecordSampleSize bounds the record round-trip. A sample, not a sweep:
// the migration is holding the bootstrap lease while this runs, and decoding
// every record in a large corpus would add unbounded time to a startup. A
// migration that broke the codec breaks it for the first record as readily as
// the thousandth.
const migratedRecordSampleSize = 32

// validateMigratedRecordRoundTrip decodes a sample of records through the same
// codec every read uses. The schema check above proves the SHAPE; this proves
// the CONTENT survived, which an ALTER TABLE with a bad UPDATE would not.
//
// tolerated names the records that ALREADY failed to decode before the
// migration ran. They are not this migration's doing, and the store's read path
// reports them as a partial list rather than a failure; refusing a migration
// over one would make an upgrade impossible for exactly the corpus that most
// needs it. The question here is narrow and answerable: did anything that
// decoded before stop decoding?
//
// An empty database passes trivially, and correctly: there is nothing a
// migration could have corrupted.
func validateMigratedRecordRoundTrip(db *sql.DB, tolerated map[string]bool) error {
	rows, err := db.Query(migratedRecordSampleQuery(true), migratedRecordSampleSize)
	if err != nil {
		return fmt.Errorf("read migrated flow records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, nonce string
		var blob []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &beadID, &epicID,
			&preparedAt, &nonce, &blob); err != nil {
			return fmt.Errorf("read migrated flow record: %w", err)
		}
		if _, err := decodeStoredFlowWithPreparation(flowID, repoPath, status, updatedAt,
			beadID, epicID, preparedAt, nonce, blob); err != nil {
			if tolerated[flowID] {
				continue
			}
			return fmt.Errorf("migrated flow record %q no longer decodes: %w", flowID, err)
		}
	}
	return rows.Err()
}

// migratedRecordSampleQuery names the same rows before and after the migration.
// ORDER BY, deliberately: an unordered LIMIT may return different rows on the
// two sides, and the comparison this sample feeds is only meaningful over the
// same set. withProjections is false before the migration, where the columns
// the current schema adds do not exist yet.
func migratedRecordSampleQuery(withProjections bool) string {
	columns := "flow_id, repo_path, status, updated_at, bead_id, epic_id, '', '', record"
	if withProjections {
		columns = "flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record"
	}
	return "SELECT " + columns + " FROM flows ORDER BY flow_id LIMIT ?"
}

// sampleUndecodableFlows records which of the sampled records already fail to
// decode, BEFORE the migration touches anything.
//
// A predecessor schema may not have the projection columns the current one
// does, so the query substitutes empty strings for them; the decode this
// baseline performs only has to agree with the post-commit one about which
// records are readable, not about what they contain.
//
// A query that fails here yields an empty baseline rather than an error: this
// is a leniency input, and being unable to build it must not turn into a
// migration failure of its own.
func sampleUndecodableFlows(db *sql.DB, predecessor int64) map[string]bool {
	undecodable := map[string]bool{}
	rows, err := db.Query(migratedRecordSampleQuery(predecessor >= databaseSchemaVersion),
		migratedRecordSampleSize)
	if err != nil {
		return undecodable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, nonce string
		var blob []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &beadID, &epicID,
			&preparedAt, &nonce, &blob); err != nil {
			return undecodable
		}
		if _, err := decodeStoredFlowWithPreparation(flowID, repoPath, status, updatedAt,
			beadID, epicID, preparedAt, nonce, blob); err != nil {
			undecodable[flowID] = true
		}
	}
	return undecodable
}

// refusePostCommitValidation reports a migration that committed and produced a
// database this build cannot use. It names the backup it just wrote and the
// exact command that undoes the migration, because at this point the operator
// has a schema-advanced database and a verified copy of what it used to be, and
// nothing else in the tree will connect the two for them.
func refusePostCommitValidation(canonicalRoot, backupPath string, cause error) error {
	if backupPath == "" {
		backupPath = filepath.Join(canonicalRoot, backupDirName)
	}
	return fmt.Errorf("flow database migration committed but the result did not validate: %w;"+
		" the pre-migration copy is at %s — undo this with"+
		" 'approach db restore --backup %s --state-root %s'",
		cause, backupPath, shellQuote(backupPath), shellQuote(canonicalRoot))
}

// migrationOutcome is what a migrator open actually did, and it is the only
// honest input to the provenance record. Callers used to infer "migrated" from
// the earlier user_version probe, which is wrong whenever that probe failed
// transiently and the migration then succeeded on its own connection.
type migrationOutcome struct {
	// Migrated reports that the schema was ADVANCED, not merely opened.
	Migrated bool
	// FromVersion is the version migrated away from. Meaningful only when
	// Migrated.
	FromVersion int64
	// BackupPath names the verified pre-migration copy.
	BackupPath string
	// Reconstructed marks a sidecar rebuilt from user_version rather than
	// written by the migration that moved it. Its history entry leaves the
	// fields it cannot know explicitly null.
	Reconstructed bool
}

// fromVersionOrNil and backupPathOrNil are what the history entry records. A
// reconstruction genuinely does not know either, and null says so; a zero would
// claim the database was migrated from the unstamped original, and an empty
// string would claim a backup at the root.
func (o migrationOutcome) fromVersionOrNil() *int64 {
	if !o.Migrated {
		return nil
	}
	from := o.FromVersion
	return &from
}

func (o migrationOutcome) backupPathOrNil() *string {
	if o.BackupPath == "" {
		return nil
	}
	path := o.BackupPath
	return &path
}

// supportedPredecessorVersions is the exact set migrateAuthoritativeDatabase
// will advance to databaseSchemaVersion.
//
// A named slice rather than the inline chain of comparisons it replaced,
// because manifest_test.go asserts it equals the manifest's declared
// migration_tested_predecessors: accepting a new predecessor without declaring
// it — and without an executing migration test for it — fails the build's own
// gate rather than shipping an untested upgrade path. Version 0 is the
// unstamped original, whose shape is v1's.
var supportedPredecessorVersions = []int64{0, 1, 2, 3, 4, 5}

// migrateAuthoritativeDatabase upgrades only the accepted predecessor schema.
// It runs while newSQLiteStoreBackend holds the bootstrap lease, before the
// authoritative read-only validation and before the WAL runtime handle opens.
//
// It reports whether it ACTUALLY advanced the schema, because that is the only
// honest input to the provenance decision. Callers used to infer it from the
// earlier user_version probe, which is wrong whenever that probe failed
// transiently and this helper — retrying on its own connection under the
// caller's longer timeout — then succeeded: the migration ran, and nothing
// recorded it.
func migrateAuthoritativeDatabase(path string, lockTimeout time.Duration, canonicalRoot, backupDir string, allowDevLiveMigration bool) (migrationOutcome, error) {
	millis, err := sqliteBusyTimeoutMillis(lockTimeout)
	if err != nil {
		return migrationOutcome{}, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{
		"mode":    {"rw"},
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis)},
		"_txlock": {"immediate"},
	}))
	if err != nil {
		return migrationOutcome{}, fmt.Errorf("open authoritative flow database for migration: %w", err)
	}
	// One connection for the whole migration. PRAGMA data_version is a
	// PER-CONNECTION signal — it moves only when some OTHER connection commits —
	// so the check across the backup boundary below is only meaningful if the
	// backup, the probe, and the migration transaction all sit on the same
	// connection. A pool would hand them out independently and silently.
	db.SetMaxOpenConns(1)
	closeDB := true
	defer func() {
		if closeDB {
			_ = db.Close()
		}
	}()
	if err := db.Ping(); err != nil {
		return migrationOutcome{}, fmt.Errorf("open authoritative flow database for migration: %w", err)
	}
	version, err := readSchemaVersion(db)
	if err != nil {
		return migrationOutcome{}, err
	}
	if version > databaseSchemaVersion {
		// Same helper as the steady-state role gate, so exactly one
		// compatibility refusal string exists in the tree rather than two that
		// can drift apart. Reached here only on the stage-resume call site,
		// where the gate above has no database to have read.
		return migrationOutcome{}, refuseIncompatibleBuild(version, version)
	}
	if version == databaseSchemaVersion {
		if err := db.Close(); err != nil {
			return migrationOutcome{}, fmt.Errorf("close authoritative flow database migration handle: %w", err)
		}
		closeDB = false
		return migrationOutcome{}, nil
	}
	if !slices.Contains(supportedPredecessorVersions, version) {
		return migrationOutcome{}, fmt.Errorf("flow database has unsupported predecessor schema version %d", version)
	}
	if err := refuseDevLiveMigration(canonicalRoot, version, allowDevLiveMigration); err != nil {
		return migrationOutcome{}, err
	}
	predecessor := int64(1)
	if version >= 2 {
		predecessor = version
	}
	stampOnly := false
	if version == 4 || version == 5 {
		if err := validateSQLiteSchemaVersion(db, version); err != nil {
			if validateSQLiteSchemaVersion(db, 6) == nil {
				stampOnly = true
			} else if version == 4 && validateSQLiteSchemaVersion(db, 5) == nil {
				// Parent-release v4 that already has the v5 claim trigger still
				// needs the v6 nonce column; do not stamp-only.
			} else {
				return migrationOutcome{}, fmt.Errorf("validate predecessor flow database schema v%d: %w", version, err)
			}
		}
	} else if err := validateSQLiteSchemaVersion(db, predecessor); err != nil {
		return migrationOutcome{}, fmt.Errorf("validate predecessor flow database schema v%d: %w", version, err)
	}
	// Placement is exact. Everything above can still refuse — the dev-live
	// guard, the unsupported-predecessor check, and the already-current early
	// return — and a backup written any higher would mean every refused open
	// and every TUI startup first paid for a full copy of the database.
	//
	// Autocommit, deliberately: SQLite answers `cannot VACUUM from within a
	// transaction`, so this must stay above db.Begin.
	// Sampled BEFORE the copy, not after. VACUUM INTO cannot run inside a
	// transaction, so it reads its own snapshot and then releases; in WAL mode
	// another connection can commit while it is still copying, which lands after
	// the snapshot the backup captured. Sampling afterwards would record that
	// commit as the baseline and compare it against itself, so the check would
	// pass on exactly the backup it exists to catch. Taking the baseline here
	// covers the copy itself as well as the gap before the write lock.
	//
	// The bootstrap lease does not close that window: it serializes this build's
	// opens, and the writer that matters is a build that predates the lease
	// entirely — an older release still holding the v5 database open is the
	// mixed-build situation this unit exists for. A commit in that window would
	// be migrated but absent from the backup, and neither the row count nor an
	// in-place UPDATE would reveal it.
	dataVersion, err := readDataVersion(db)
	if err != nil {
		return migrationOutcome{}, err
	}
	backupPath, err := backupBeforeMigration(db, path, canonicalRoot, backupDir, version)
	if err != nil {
		return migrationOutcome{}, err
	}
	outcome := migrationOutcome{Migrated: true, FromVersion: version, BackupPath: backupPath}
	// Sampled before the write lock, on the predecessor shape, so the
	// post-commit check can tell "this migration broke a record" from "this
	// record was already unreadable" — the second is a partial list on the read
	// path, and refusing an upgrade over one would strand exactly the corpus
	// that most needs migrating.
	tolerated := sampleUndecodableFlows(db, version)
	migrationRaceProbe()
	tx, err := db.Begin()
	if err != nil {
		return migrationOutcome{}, fmt.Errorf("begin flow database schema migration: %w", err)
	}
	rollback := func(cause error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("%w; rollback flow database schema migration: %v", cause, rollbackErr)
		}
		return cause
	}
	// Now that the write lock is held, nothing else can commit. Re-reading here
	// closes the window: a change means the backup is already stale, and the
	// migration has not touched anything yet, so refusing is free and the
	// operator can close the other process and re-run. Detect-and-abort rather
	// than copy-again, because a writer that raced once will race again and a
	// retry loop would be the wrong shape for a one-shot command.
	if err := verifyDataVersion(tx, dataVersion); err != nil {
		return migrationOutcome{}, rollback(err)
	}
	statements := make([]string, 0, 10)
	if !stampOnly {
		if predecessor == 1 {
			statements = append(statements,
				"ALTER TABLE flows ADD COLUMN bead_id TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE flows ADD COLUMN epic_id TEXT NOT NULL DEFAULT ''",
				flowBeadCompatibilityTrigger,
			)
		}
		if predecessor <= 2 {
			statements = append(statements,
				"ALTER TABLE flows ADD COLUMN prepared_at TEXT NOT NULL DEFAULT ''",
				epicProgressionTableSchema,
				flowPreparedCompatibilityTrigger,
			)
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				return migrationOutcome{}, rollback(fmt.Errorf("migrate flow database schema: %w", err))
			}
		}
		if predecessor == 3 {
			if err := migrateLegacyV3EpicProgressions(tx); err != nil {
				return migrationOutcome{}, rollback(err)
			}
		}
		statements = nil
		if predecessor <= 3 {
			statements = append(statements,
				epicProgressionDoneInsertCompatibilityTrigger,
				epicProgressionDoneUpdateCompatibilityTrigger,
			)
		}
		if predecessor <= 4 {
			statements = append(statements, flowProgressionClaimCompatibilityTrigger)
		}
		if predecessor <= 5 {
			statements = append(statements,
				"ALTER TABLE flows ADD COLUMN preparation_nonce TEXT NOT NULL DEFAULT ''",
				"UPDATE flows SET preparation_nonce = CASE WHEN json_valid(CAST(record AS TEXT)) THEN COALESCE(json_extract(CAST(record AS TEXT), '$.preparation_nonce'), '') ELSE '' END",
				flowPreparationNonceCompatibilityTrigger,
			)
		}
	}
	statements = append(statements, fmt.Sprintf("PRAGMA user_version = %d", databaseSchemaVersion))
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return migrationOutcome{}, rollback(fmt.Errorf("migrate flow database schema: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return migrationOutcome{}, rollback(fmt.Errorf("commit flow database schema migration: %w", err))
	}
	// After the commit and BEFORE the bootstrap lease is released, so nothing
	// else can open the database in the window between producing it and
	// discovering it is unusable. A migration that returned success here and
	// left a database no build can read would be the worst possible outcome:
	// the backup is still on disk, and nothing would tell the operator to use
	// it.
	if err := postMigrationValidation(db, databaseSchemaVersion, tolerated); err != nil {
		return migrationOutcome{}, refusePostCommitValidation(canonicalRoot, backupPath, err)
	}
	if err := db.Close(); err != nil {
		return migrationOutcome{}, fmt.Errorf("close authoritative flow database migration handle: %w", err)
	}
	closeDB = false
	return outcome, nil
}

func migrateLegacyV3EpicProgressions(tx *sql.Tx) error {
	type migrationRow struct {
		repoPath  string
		epicID    string
		enabled   int
		updatedAt string
		record    []byte
	}
	rows, err := tx.Query("SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions ORDER BY repo_path, epic_id")
	if err != nil {
		return fmt.Errorf("read v3 epic progressions for migration: %w", err)
	}
	var pending []migrationRow
	for rows.Next() {
		var row migrationRow
		if err := rows.Scan(&row.repoPath, &row.epicID, &row.enabled, &row.updatedAt, &row.record); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan v3 epic progression for migration: %w", err)
		}
		row.record = append([]byte(nil), row.record...)
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate v3 epic progressions for migration: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close v3 epic progression migration rows: %w", err)
	}
	for _, row := range pending {
		record, err := decodeLegacyV3EpicProgression(row.repoPath, row.epicID, row.enabled, row.updatedAt, row.record)
		if err != nil {
			return fmt.Errorf("migrate v3 epic progression: %w", err)
		}
		data, migratedUpdatedAt, err := encodeEpicProgression(record)
		if err != nil {
			return fmt.Errorf("encode migrated epic progression %q/%q: %w", row.repoPath, row.epicID, err)
		}
		if migratedUpdatedAt != row.updatedAt {
			return fmt.Errorf("migrate v3 epic progression %q/%q changed updated_at projection", row.repoPath, row.epicID)
		}
		result, err := tx.Exec("UPDATE epic_progressions SET record = ? WHERE repo_path = ? AND epic_id = ?", data, row.repoPath, row.epicID)
		if err != nil {
			return fmt.Errorf("rewrite v3 epic progression %q/%q: %w", row.repoPath, row.epicID, err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("rewrite v3 epic progression %q/%q affected %d rows: %v", row.repoPath, row.epicID, affected, err)
		}
	}
	return nil
}

// describeUnusableDatabase adds the recovery path to an authoritative-database
// failure. Leaving flows/ in place is the whole point of this cutover design,
// but an operator staring at "file is not a database" has no way to know their
// pre-migration records are still sitting next to the broken file.
func describeUnusableDatabase(root string, cause error) error {
	// A database from a newer build is not unusable, it is just ahead of this
	// binary. Attaching "re-migrate from flows/" to it would tell the operator to
	// roll a live corpus back to its migration-day snapshot, immediately after
	// correctly telling them to upgrade. Return it untouched.
	//
	// The dev-live-migration refusal is the same shape and worse to get wrong:
	// the database is healthy, this build simply declined to advance it, and the
	// recovery paragraph would talk an operator into moving a perfectly good
	// release database aside over a check whose own message already says to pass
	// an acknowledgement or use a scratch root.
	//
	// The role refusal is the same shape again, and its entry here is
	// deliberate belt-and-braces rather than dead code: the steady-state gate
	// returns ahead of the wraps in newSQLiteStoreBackend, so this allowlist is
	// what stops a LATER caller that does wrap it — the stage-resume route is
	// the obvious candidate — from reintroducing rebuild advice on a database
	// that is merely unmigrated.
	if errors.Is(cause, errDatabaseFromNewerBuild) || errors.Is(cause, errDevLiveMigrationRefused) ||
		errors.Is(cause, errRoleRefusedMigration) {
		return cause
	}
	databasePath := filepath.Join(root, databaseFilename)
	legacyPath := filepath.Join(root, "flows")
	tombstonePath := filepath.Join(root, "flows.legacy")
	legacy, err := inspectReservedDirectory(legacyPath)
	if err != nil || !legacy {
		// A root migrated by the older rename-based build retained its corpus as
		// flows.legacy/, so "no flows/" is not the same as "nothing to recover".
		// Offering an empty rebuild here reads as "your Flows are gone" while every
		// one of them is still on disk under the other name.
		// Only when flows/ is confirmed absent: an inspection error means the path
		// exists as something that is not a usable directory, and `mv` onto it would
		// either fail or bury the sole retained corpus inside it.
		tombstone, tombstoneErr := inspectReservedDirectory(tombstonePath)
		if err == nil && tombstoneErr == nil && tombstone {
			return fmt.Errorf("%w (your pre-migration Flows are still in %q. Move %q aside — keep it, it holds"+
				" anything created since the migration, and may be repairable by hand. Rebuilding from the"+
				" retained copy needs it back under its original name first: run `mv %s %s`. %s)",
				cause, tombstonePath, databasePath, shellQuote(tombstonePath), shellQuote(legacyPath),
				rollbackAdvice(databasePath, legacyPath))
		}
		return fmt.Errorf("%w (move %q aside — keep it — and start approach again to rebuild an empty one)",
			cause, databasePath)
	}
	// This is NOT only reachable on an unreadable database. validateAuthoritative-
	// Database checks schema shape — columns and indexes — so a perfectly readable
	// database that is merely missing an index lands here with every post-migration
	// Flow intact and queryable. Re-migrating from flows/ would throw all of them
	// away. So the instruction gets the same bound as every other place it is
	// offered, and the message leads with the non-destructive option.
	return fmt.Errorf("%w (your pre-migration Flows are still in %q. Move %q aside — keep it, it holds"+
		" anything created since the migration, and may be repairable by hand. %s)",
		cause, legacyPath, databasePath, rollbackAdvice(databasePath, legacyPath))
}

// shellQuote renders a path as a single POSIX shell token. These messages hand
// the operator a command to copy, and %q is Go quoting, not shell quoting: a
// state root holding $VAR or $(...) expands or executes inside the double
// quotes %q emits, so a copied recovery command could move the wrong directory.
func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// secureCanonicalRoot is the flow store's spelling of
// artifacts.SecureCanonicalRoot. The shared helper lives in internal/artifacts
// because internal/controlplane executes a binary out of the same root and must
// apply the identical trust checks.
func secureCanonicalRoot(root string) (string, error) {
	return artifacts.SecureCanonicalRoot(root, "flow store root")
}

// refuseDevLiveMigration stops a development build from silently advancing the
// schema of the database a released build owns.
//
// The condition is deliberately narrow — a development build, an older stored
// schema, and *exact* path equality with the release default root. What it
// protects is the state a *released* build owns by default, which is one path.
//
// The broader rule ("refuse on any root but the dev default") was considered and
// rejected on cost, not on test breakage: the other two conditions already
// restrict this to roots holding a predecessor-schema database, which no test
// outside this package creates. It was rejected because iterating on a schema
// change means repeatedly reopening scratch roots written by the previous
// iteration of the same development build, and a guard that fires there charges
// a daily toll to protect state nobody else reads. The residual gap is real and
// stated in docs/architecture.md: an explicit shared root — `[sessions].root` on
// a mixed dev/release team — gets no protection.
//
// Reads of an already-current release database stay allowed: this gate exists to
// stop a schema advance, not to fence development builds out of release state.
func refuseDevLiveMigration(canonicalRoot string, storedVersion int64, allowed bool) error {
	if storedVersion >= databaseSchemaVersion {
		return nil
	}
	if !isReleaseOwnedRoot(canonicalRoot, allowed) {
		return nil
	}
	// The acknowledgement is named for the surfaces that can still act on it.
	// This guard only fires when the stored schema is behind, and on that input
	// `approach flow` and `approach serve` now return the ROLE refusal first,
	// which no environment variable acknowledges. Naming them here would offer
	// an acknowledgement that cannot help and omit the one command that can.
	return fmt.Errorf(
		"%w %s from schema %d to %d: this is a development build (%s), "+
			"and a released approach would then be unable to open its own state. "+
			"Use a scratch --state-root, or pass --allow-dev-live-migration on the TUI "+
			"(or set APPROACH_ALLOW_DEV_LIVE_MIGRATION=1, which `approach db migrate` "+
			"reads too) to acknowledge it",
		errDevLiveMigrationRefused, canonicalRoot, storedVersion, databaseSchemaVersion, version.Version())
}

// refuseDevLiveCreation is the same guard for the path that has no stored schema
// to compare. `completeCutover` does not migrate anything — it BUILDS a
// current-schema database and renames it onto approach.db — so a development
// build importing a legacy `flows/` tree, or bootstrapping an empty release
// root, reaches exactly the end state refuseDevLiveMigration exists to prevent
// (a database a released build cannot open) by creating rather than advancing.
// Same narrow condition, same acknowledgement.
func refuseDevLiveCreation(canonicalRoot string, allowed bool) error {
	if !isReleaseOwnedRoot(canonicalRoot, allowed) {
		return nil
	}
	return fmt.Errorf(
		"%w %s: this is a development build (%s), and creating a schema-%d Flow database there would "+
			"leave a released approach unable to open its own state. Use a scratch --state-root, or pass "+
			"--allow-dev-live-migration on the TUI (or set APPROACH_ALLOW_DEV_LIVE_MIGRATION=1, which the "+
			"`approach flow` and `approach serve` commands read too) to acknowledge it",
		errDevLiveMigrationRefused, canonicalRoot, version.Version(), databaseSchemaVersion)
}

// isReleaseOwnedRoot reports whether an unacknowledged development build is
// about to write the state a *released* build owns by default.
func isReleaseOwnedRoot(canonicalRoot string, allowed bool) bool {
	if allowed || !isDevelopmentBuild() {
		return false
	}
	releaseRoot, err := artifacts.ReleaseDefaultRoot()
	if err != nil {
		// Not knowing the release default is not a reason to refuse: the guard
		// is about one exact path, and without it there is nothing to compare.
		return false
	}
	return sameCanonicalPath(canonicalRoot, releaseRoot)
}

// sameCanonicalPath compares a root the store has already canonicalized against
// one that has not been. EvalSymlinks failing is not a match: a release default
// root that does not exist cannot be the root being migrated.
//
// String equality is checked first because it is free, but it is not sufficient:
// EvalSymlinks preserves the caller's spelling of every component, and macOS
// ships a case-insensitive filesystem by default, so `--state-root` typed as
// `/Users/x/.local/State/approach/sessions/v1` resolves to the same directory as
// the release default and compares unequal — silently disarming the guard on the
// one path it exists to protect. Identity is the real question, so the
// filesystem answers it.
func sameCanonicalPath(canonical, candidate string) bool {
	if canonical == candidate {
		return true
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	if resolved == canonical {
		return true
	}
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		return false
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	return os.SameFile(canonicalInfo, resolvedInfo)
}

func inspectCutoverState(root string) (cutoverState, error) {
	database, err := inspectReservedRegular(filepath.Join(root, databaseFilename))
	if err != nil {
		return cutoverState{}, err
	}
	if database {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if _, err := inspectReservedRegular(filepath.Join(root, databaseFilename) + suffix); err != nil {
				return cutoverState{}, err
			}
		}
		return cutoverState{database: true}, nil
	}

	legacy, err := inspectReservedDirectory(filepath.Join(root, "flows"))
	if err != nil {
		return cutoverState{}, err
	}
	tombstone, err := inspectReservedDirectory(filepath.Join(root, "flows.legacy"))
	if err != nil {
		return cutoverState{}, err
	}
	stage, err := inspectReservedRegular(filepath.Join(root, stageFilename))
	if err != nil {
		return cutoverState{}, err
	}
	for _, base := range []string{databaseFilename, stageFilename} {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if _, err := inspectReservedRegular(filepath.Join(root, base) + suffix); err != nil {
				return cutoverState{}, err
			}
		}
	}
	return cutoverState{legacy: legacy, tombstone: tombstone, stage: stage}, nil
}

func inspectReservedRegular(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect reserved path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("reserved path %q must be a real regular file", path)
	}
	return true, nil
}

func inspectReservedDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect reserved path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("reserved path %q must be a real directory", path)
	}
	return true, nil
}

func completeCutover(opts backendOptions, state cutoverState, presets map[string]Preset) error {
	root := opts.root
	lockTimeout := opts.lockTimeout
	allowDevLiveMigration := opts.allowDevLiveMigration
	stagePath := filepath.Join(root, stageFilename)
	legacyPath := filepath.Join(root, "flows")
	tombstonePath := filepath.Join(root, "flows.legacy")
	databasePath := filepath.Join(root, databaseFilename)

	if state.legacy && state.tombstone {
		return fmt.Errorf("flow database cutover conflict: both flows and flows.legacy exist under %q; "+
			"keep whichever holds your Flows, move the other aside, and start approach again", root)
	}
	if !state.legacy && state.tombstone && !state.stage {
		// The tombstone is a byte-identical copy of the legacy source, so renaming
		// it back is lossless and lets the cutover run again from scratch.
		return fmt.Errorf("flow database cutover is incomplete: %q exists without a staged database; "+
			"your Flows are intact there — run `mv %s %s` and start approach again",
			tombstonePath, shellQuote(tombstonePath), shellQuote(legacyPath))
	}

	// Role first, then dev-live — the same order as the authoritative-database
	// path above, and for the same reason. Importing a legacy corpus and
	// resuming an interrupted cutover are RoleMigrator-only; a non-migrator
	// that saw refuseDevLiveCreation first would be told to set
	// APPROACH_ALLOW_DEV_LIVE_MIGRATION=1 and retry `flow` or `serve`, which
	// still cannot import or resume. Creation from empty stays open to every
	// role, so that path falls through this gate to the creation guard.
	if opts.role != RoleMigrator {
		if state.stage {
			return refuseRoleStageResume(stagePath, opts.role)
		}
		if state.legacy {
			return refuseRoleLegacyImport(legacyPath, opts.role)
		}
	}

	// Every path out of this function that does not return an error publishes a
	// current-schema approach.db in root — by promoting the stage below, or by
	// building a fresh database further down. refuseDevLiveMigration cannot see
	// either one: the first is a stage that may already be at this build's
	// schema, in which case migrateAuthoritativeDatabase returns before the
	// guard ever runs, and the second has no stored version to compare at all.
	// So the creation guard belongs here, ahead of every mutating branch,
	// rather than only in front of the build. Checked before anything is
	// renamed or removed so a refusal leaves the root byte-identical.
	if err := refuseDevLiveCreation(root, allowDevLiveMigration); err != nil {
		return err
	}

	if state.stage {
		// Integrity and schema only — deliberately NOT a record-by-record diff
		// against the legacy source. The stage is fsynced before it is promoted,
		// so a validating stage is already known complete and durable;
		// re-deriving the expected set would add no safety and plenty of failure
		// surface, because canonicalizeLegacyFlow is preset-dependent. A user who
		// crashed mid-cutover and then edited a preset out of their config would
		// get a mismatch on every subsequent launch and could never start again.
		// A predecessor-schema stage from an older build is still that durable
		// corpus: upgrade it in place before current-schema validation, or the
		// resume path treats a valid predecessor stage as unusable and discards it.
		// root is passed rather than elided even though the creation guard above
		// already covers this branch: the stage is renamed onto approach.db in
		// this root, so the migration guard is correct here on its own terms and
		// keeping it wired means a later reordering cannot quietly disarm both.
		// The helper REPORTS whether it advanced the stage, which is what tells a
		// resumed CREATION (a stage the previous run had already built current,
		// which stamps no provenance) from a resumed MIGRATION (a predecessor
		// stage this call advances, which must). Reading user_version here
		// instead would answer the question with a probe that can fail for
		// reasons unrelated to the schema, losing the provenance for a migration
		// that did run. Only a RoleMigrator reaches this branch, so reconciling
		// below is role-correct.
		migratedStage, err := migrateAuthoritativeDatabase(stagePath, lockTimeout, root, opts.backupDir, allowDevLiveMigration)
		if err == nil {
			err = settleStagedSQLiteFile(stagePath)
		}
		if err == nil {
			err = validateStagedDatabase(stagePath, nil, false)
		}
		switch {
		case err == nil:
			if err := os.Rename(stagePath, databasePath); err != nil {
				return fmt.Errorf("promote interrupted staged flow database: %w", err)
			}
			if err := syncDirectory(root); err != nil {
				return fmt.Errorf("sync promoted flow database directory: %w", err)
			}
			// A predecessor stage that this call advanced is a real migration —
			// it even wrote a backup — but it promotes through here rather than
			// through the authoritative-database branch that normally reconciles
			// provenance. Without this the sidecar is never written at all, and a
			// later open reads the absence as the legitimate never-migrated state
			// and leaves it that way forever.
			if migratedStage.Migrated {
				if err := reconcileSidecar(root, databaseSchemaVersion, migratedStage); err != nil {
					return err
				}
			}
			reportResumedCutover(root, legacyPath, databasePath, state.legacy)
			return nil
		case errors.Is(err, errDevLiveMigrationRefused):
			// Unreachable while refuseDevLiveCreation above still gates the same
			// condition, and kept anyway: it is what makes reordering these two
			// guards safe. A refusal is not a damaged stage, and falling through
			// to the discard below would destroy an intact corpus over a policy
			// check the operator was just told how to satisfy — on the one path
			// where the stage can be the only copy left.
			return err
		case !state.legacy && state.tombstone:
			// Only a root migrated by a build that renamed flows/ can reach this:
			// the stage is unusable and the tombstone is the sole surviving copy,
			// so refuse rather than rebuild from nothing.
			// Both steps, or the operator does the first and hits a second refusal:
			// removing the stage alone leaves a tombstone with no stage, which is
			// the "cutover is incomplete" branch above.
			return fmt.Errorf("validate interrupted staged flow database: %w"+
				" (the original records are still in %q. To redo the cutover from them, remove %q AND"+
				" run `mv %s %s`, then start approach again)",
				err, tombstonePath, stagePath, shellQuote(tombstonePath), shellQuote(legacyPath))
		default:
			// Either flows/ is still in place and authoritative, or there is no
			// legacy source at all. In both cases an unusable stage from an
			// interrupted run carries nothing that is not either still on disk or
			// already gone, so it is discarded and rebuilt below. Refusing here
			// instead would leave a fresh install that was interrupted during its
			// very first bootstrap permanently unable to start.
		}
	}

	if err := cleanupReservedSQLiteFiles(root); err != nil {
		return err
	}
	var imported legacyImport
	var err error
	if state.legacy {
		imported, err = readAndCanonicalizeLegacy(root, "flows", presets)
		if err != nil {
			return err
		}
	}
	records := imported.records
	if err := buildStagedDatabase(stagePath, records); err != nil {
		return err
	}
	if err := validateStagedDatabase(stagePath, records, true); err != nil {
		return err
	}
	// The legacy source is deliberately left where it is. Renaming it to
	// flows.legacy/ is invisible to this build but silently empties the Flow
	// list of any build still on the file store, including one launched by
	// accident against a shared state root. Coexistence plus a warning is the
	// reversible option while both backends are in play.
	if err := os.Rename(stagePath, databasePath); err != nil {
		return fmt.Errorf("promote flow database: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		return fmt.Errorf("sync flow database directory: %w", err)
	}
	if state.legacy {
		reportCutover(root, legacyPath, databasePath, imported)
	}
	return nil
}

// rollbackAdvice is the ONLY place "re-run the cutover" may be phrased.
//
// Re-running rebuilds approach.db from flows/, and flows/ is a snapshot frozen
// at migration time — nothing has written to it since. So the instruction is
// harmless in the minutes after a cutover, when the database holds nothing but
// what was just imported, and destroys every Flow created since at any later
// point. These strings are also written to a file that sits in the state root
// for months, which is exactly when they are most likely to be read and least
// likely to be safe.
//
// Centralizing it means no future paragraph can emit the instruction without
// the bound. TestEveryRollbackInstructionIsTimeBounded pins that.
func rollbackAdvice(databasePath, legacyPath string) string {
	return fmt.Sprintf("To re-run the cutover, move %q aside and start approach again; it will rebuild"+
		" from %q.\n"+
		"Do NOT do this later: %q is a snapshot frozen at migration time, not a live mirror — approach"+
		" has never written to it since — so this is safe ONLY while the database holds nothing beyond"+
		" what the migration imported. Once you have created or changed Flows here, re-running the"+
		" cutover discards every one of them.",
		databasePath, legacyPath, legacyPath)
}

// elideAfter bounds a list interpolated into a notice. These lists are written
// to stderr AND to a file, and a corpus with hundreds of stray directories would
// otherwise bury the instructions underneath them.
func elideAfter(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append(append([]string(nil), values[:limit]...),
		fmt.Sprintf("... and %d more", len(values)-limit))
}

// fenceRepairAdvice is the ONLY place the manual repair for a graph-fenced
// record may be phrased, for the same reason rollbackAdvice is centralized: the
// fence keys off the graph_recovery marker, not off the edges, so a recipe that
// names only the depends_on edit reads as complete and leaves the Flow blocked.
// Both edits, every time.
func fenceRepairAdvice(databasePath string) string {
	return fmt.Sprintf("Recreate those Flows, or repair each one directly in %q: set depends_on on its"+
		" phases AND delete the %q marker from its stored record — the fence keys off that marker, not"+
		" off the edges, so editing depends_on alone leaves the Flow blocked (see docs/config.md).",
		databasePath, GraphRecoveryMissingEdgesUnresolved)
}

// reportResumedCutover reports a promotion of a stage this process did not
// build. There is no import accounting to carry over, but the two facts that
// matter are still recoverable from the promoted database itself, and both are
// only actionable now: a crashed first migration that left Flows fenced would
// otherwise tell nobody, and a record added to flows/ after the stage was built
// (by falling back to an older file-store build) is dropped by the promotion.
func reportResumedCutover(root, legacyPath, databasePath string, legacyPresent bool) {
	promoted, unresolved, err := summarizePromotedDatabase(databasePath)
	if err == nil && !legacyPresent && len(unresolved) == 0 {
		// A fresh install whose very first bootstrap crashed after staging has no
		// legacy source and nothing fenced. Writing "promoted a staged Flow
		// database" into the state root would file an incident report for a
		// non-event on an install that never had any Flows.
		return
	}

	notice := fmt.Sprintf("promoted a staged Flow database into %q.", databasePath)
	switch {
	case err != nil:
		notice += fmt.Sprintf("\n\nCould not summarize the promoted records (%v);"+
			" check the Flow list yourself.", err)
	default:
		// Identity, not counts. A directory the migration would legitimately skip
		// — no meta.json, unreadable JSON — is not a dropped record, and raising a
		// false alarm next to a rollback instruction is how an operator ends up
		// discarding a database for no reason.
		if legacyPresent {
			missing, missingErr := legacyFlowIDsMissingFrom(legacyPath, promoted)
			switch {
			case missingErr != nil:
				notice += fmt.Sprintf("\n\nCould not compare the promoted records against %q (%v);"+
					" check the Flow list yourself.", legacyPath, missingErr)
			case len(missing) > 0:
				notice += fmt.Sprintf("\n\nThese Flow(s) are in %q but not in the promoted database:\n  - %s\n"+
					"The stage is promoted as built and deliberately not re-derived, so anything added to %q"+
					" after the interrupted run staged it was not imported.\n%s",
					legacyPath, strings.Join(elideAfter(missing, 20), "\n  - "), legacyPath,
					rollbackAdvice(databasePath, legacyPath))
			}
			// Matching by id cannot see an EDIT: a record whose meta.json was
			// rewritten after staging keeps its id, so it looks present. Say so
			// rather than let the list above read as the complete story.
			notice += fmt.Sprintf("\n\nThe resume promotes the stage exactly as it was built. Any Flow in"+
				" %q that was CHANGED after that stage was created also kept its pre-change form here.",
				legacyPath)
		}
		// Deliberately NOT gated on the legacy source still existing: this comes
		// from the promoted database, and an operator who already removed flows/
		// is the one most likely to never hear about it otherwise.
		if len(unresolved) > 0 {
			notice += fmt.Sprintf("\n\n%d Flow(s) were migrated with an unresolved phase graph, so phase"+
				" mutation on them is blocked:\n  - %s\n"+
				"Preset edge recovery runs only during migration.", len(unresolved),
				strings.Join(elideAfter(unresolved, 20), "\n  - "))
			if legacyPresent {
				notice += "\nRestore those presets first, then: " + rollbackAdvice(databasePath, legacyPath)
			} else {
				notice += fmt.Sprintf("\n%q is gone, so the cutover cannot be re-run. %s",
					legacyPath, fenceRepairAdvice(databasePath))
			}
		}
	}

	if legacyPresent {
		notice += fmt.Sprintf("\n\n%q was left in place and is no longer read."+
			" Builds without the SQLite flow backend still read it and will not see changes made here."+
			" Remove it yourself once every build you run is on the SQLite backend.", legacyPath)
	}

	bootstrapWarnf("%s", notice)
	writeMigrationNotice(root, notice)
}

// summarizePromotedDatabase reads back what was promoted. It deliberately does
// not re-canonicalize anything — that would reintroduce the preset dependence
// the resume path exists to avoid (see completeCutover).
func summarizePromotedDatabase(path string) (map[string]bool, []string, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows ORDER BY flow_id")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	promoted := map[string]bool{}
	var unresolved []string
	for rows.Next() {
		var flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce string
		var data []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &beadID, &epicID, &preparedAt, &preparationNonce, &data); err != nil {
			return nil, nil, err
		}
		promoted[flowID] = true
		stored, err := decodeStoredFlowWithPreparation(flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce, data)
		if err != nil {
			return nil, nil, err
		}
		if preset := fencingPresetName(stored.record); preset != "" {
			unresolved = append(unresolved, fmt.Sprintf("%s: preset %q", stored.record.FlowID, preset))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return promoted, unresolved, nil
}

// legacyFlowIDsMissingFrom names the legacy records the promotion did not
// import. It matches readAndCanonicalizeLegacy's candidate predicate AND its
// skip rules: a directory with no meta.json, or one whose meta.json the
// migration would refuse to decode, was never going to be imported and is not
// a dropped record. Reporting those as losses would raise a false alarm next
// to a rollback instruction, which is how an operator discards a database for
// nothing.
func legacyFlowIDsMissingFrom(dir string, promoted map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, entry := range entries {
		if !entry.IsDir() || validateFlowID(entry.Name()) != nil || promoted[entry.Name()] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
		if err != nil {
			// A missing meta.json was never a record. Anything else — EACCES, EIO —
			// is environmental, and this is the one moment it can be reported: the
			// fresh path treats the same condition as fatal precisely because the
			// loss becomes permanent. Silently skipping it here would be the
			// opposite stance on identical evidence.
			if os.IsNotExist(err) {
				continue
			}
			missing = append(missing, fmt.Sprintf("%s (unreadable: %v)", entry.Name(), err))
			continue
		}
		if _, ok := decodeLegacyFlow(entry.Name(), data); !ok {
			continue
		}
		missing = append(missing, entry.Name())
	}
	return missing, nil
}

// reportCutover tells the operator what the one-way migration actually did.
// Every line here describes something that is only actionable now: once
// approach.db exists the legacy tree is never read again, so a record that was
// skipped or left graph-unresolved stays that way until the operator removes
// the database and re-runs the cutover.
func reportCutover(root, legacyPath, databasePath string, imported legacyImport) {
	notice := fmt.Sprintf("migrated %d of %d Flow records from %q into %q.",
		len(imported.records), imported.found, legacyPath, databasePath)
	if len(imported.skipped) > 0 {
		notice += fmt.Sprintf("\n\nSkipped %d unreadable record(s); their files are untouched under %q:\n  - %s",
			len(imported.skipped), legacyPath, strings.Join(elideAfter(imported.skipped, 20), "\n  - "))
	}
	if len(imported.unresolved) > 0 {
		// Edge recovery is preset-dependent and now runs exactly once, so unlike
		// the file store this does NOT heal on a later launch with the preset
		// restored. Re-running the cutover is the only real fix, and it is safe
		// ONLY right now, while the database holds nothing but what was just
		// migrated. Say both halves — this text also lands in a file that will
		// still be sitting in the state root months from now.
		notice += fmt.Sprintf("\n\n%d Flow(s) reference a preset that is not in your config, so their phase"+
			" dependencies could not be restored and phase mutation on them is blocked:\n  - %s\n"+
			"Preset edge recovery runs only during this migration. Restore those presets first, then: %s",
			len(imported.unresolved), strings.Join(elideAfter(imported.unresolved, 20), "\n  - "),
			rollbackAdvice(databasePath, legacyPath))
	}
	notice += fmt.Sprintf("\n\n%q was left in place and is no longer read."+
		" Builds without the SQLite flow backend still read it and will not see changes made here."+
		" Remove it yourself once every build you run is on the SQLite backend.", legacyPath)

	bootstrapWarnf("%s", notice)
	writeMigrationNotice(root, notice)
}

// writeMigrationNotice persists the cutover report next to the database. Best
// effort on purpose: failing to write an advisory file must never fail a
// migration that already succeeded and is already durable.
func writeMigrationNotice(root, notice string) {
	path := filepath.Join(root, migrationNoticeFilename)
	body := "approach Flow storage migration\n\n" + notice + "\n\nThis file is advisory. Delete it once you have read it.\n"
	// O_NOFOLLOW like every other write in this file: os.WriteFile would happily
	// follow a symlink planted at the notice path and write through it. The 0700
	// root makes that hard to reach, but nothing here should be the one primitive
	// that trusts its final path component.
	fd, err := syscall.Open(path,
		syscall.O_CREAT|syscall.O_WRONLY|syscall.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		uint32(artifacts.FilePerm))
	if err != nil {
		return
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(body); err != nil {
		return
	}
	_ = file.Chmod(artifacts.FilePerm)
}

// discardObsoleteStage drops a stage that approach.db has already made
// irrelevant. It runs on EVERY healthy startup, so it must not be able to
// refuse one: a stray directory or symlink at a stage path — a botched rsync, a
// backup tool, a curious user — is not a stage worth promoting and is left
// alone rather than turned into a startup failure. inspectReservedRegular's
// strictness belongs on the promotion path, where the file's contents become
// authoritative, not on a best-effort cleanup.
func discardObsoleteStage(root string) error {
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		path := filepath.Join(root, stageFilename) + suffix
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect obsolete staged flow database %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove obsolete staged flow database %q: %w", path, err)
		}
	}
	return nil
}

// cleanupReservedSQLiteFiles clears the reserved SQLite paths before a fresh
// stage is built. It removes approach.db itself, so it MUST NOT be called on a
// root that already has an authoritative database — the guard below turns a
// future misuse into an error instead of silent data loss.
func cleanupReservedSQLiteFiles(root string) error {
	authoritative, err := inspectReservedRegular(filepath.Join(root, databaseFilename))
	if err != nil {
		return err
	}
	if authoritative {
		return fmt.Errorf("refusing to clear reserved SQLite artifacts: %q already exists",
			filepath.Join(root, databaseFilename))
	}
	for _, base := range []string{databaseFilename, stageFilename} {
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			path := filepath.Join(root, base) + suffix
			exists, err := inspectReservedRegular(path)
			if err != nil {
				return err
			}
			if exists {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("remove reserved SQLite artifact %q: %w", path, err)
				}
			}
		}
	}
	return nil
}

// legacyImport is the accounting for one cutover pass. found counts candidate
// record directories so a migration that imports 0 of 400 cannot look identical
// to one that imports 400 of 400.
type legacyImport struct {
	records    []FlowRecord
	found      int
	skipped    []string
	unresolved []string
}

func readAndCanonicalizeLegacy(root, collection string, presets map[string]Preset) (legacyImport, error) {
	dir := filepath.Join(root, collection)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return legacyImport{}, fmt.Errorf("read legacy flow source: %w", err)
	}
	var imported legacyImport
	for _, entry := range entries {
		if !entry.IsDir() || validateFlowID(entry.Name()) != nil {
			continue
		}
		imported.found++
		metaPath := filepath.Join(dir, entry.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			// A missing meta.json is a legitimately empty directory and was
			// invisible under the file store too. Anything else — EACCES from a
			// botched chown, EIO on a network state root — is environmental, and
			// skipping it would migrate a truncated corpus and then make the loss
			// permanent, because the legacy tree is never read again once
			// approach.db exists. Fail instead so the operator can fix the cause
			// and retry with the legacy source still authoritative.
			if os.IsNotExist(err) {
				imported.skipped = append(imported.skipped,
					fmt.Sprintf("%s: no meta.json", entry.Name()))
				continue
			}
			return legacyImport{}, fmt.Errorf("read legacy flow %q: %w (migration aborted so no Flow is lost; "+
				"fix the file's permissions or availability and start approach again)", entry.Name(), err)
		}
		stored, ok := decodeLegacyFlow(entry.Name(), data)
		if !ok {
			// Undecodable content was already invisible under the file store, so
			// dropping it changes nothing today and the bytes stay under flows/.
			// It does become permanent, though: repairing the JSON used to bring
			// the Flow back on the next launch and no longer does. That is what
			// makes reporting it here the last useful moment.
			imported.skipped = append(imported.skipped,
				fmt.Sprintf("%s: unreadable meta.json (invalid JSON, mismatched flow_id, or unsupported schema_version)",
					entry.Name()))
			continue
		}
		record := canonicalizeLegacyFlow(stored, presets)
		record.Phases = collapseAllDuplicatePhaseRows(record.Phases)
		if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved {
			record = normalizeRecordBase(record)
			record = normalizeReviewOutcomes(record)
		} else {
			record = normalizeRecord(record, true)
		}
		record.Status = DeriveStatus(record)
		if record.GraphRecovery.Status == GraphRecoveryPresetEdgesRestored {
			record.GraphRecovery.Status = ""
		}
		if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved && mutationFencedByPreset(record) {
			imported.unresolved = append(imported.unresolved,
				fmt.Sprintf("%s: preset %q", record.FlowID, normalizePresetName(record.PresetName)))
		}
		if _, _, err := encodeStoredFlow(record); err != nil {
			return legacyImport{}, fmt.Errorf("canonicalize legacy flow %q: %w (its stored timestamps are"+
				" outside the range this build can persist; fix it, or move %q aside — never delete it, it is"+
				" the only copy of that Flow — and start approach again)",
				record.FlowID, err, filepath.Join(dir, entry.Name(), "meta.json"))
		}
		imported.records = append(imported.records, record)
	}
	return imported, nil
}

func decodeLegacyFlow(flowID string, data []byte) (legacyStoredFlow, bool) {
	raw := decodeRawFlowFields(data)
	if raw == nil {
		return legacyStoredFlow{}, false
	}
	presence := raw.dependsOnPresence()
	headlessPresent := raw.present("headless")
	// Preparation receipts did not exist in the legacy file store and may only
	// be minted by the protected post-bootstrap SQLite transition. Ignore both
	// the persisted spelling and the Go field spelling that encoding/json would
	// otherwise accept case-insensitively. Removing the raw value before the
	// typed decode also keeps malformed future timestamps forward-compatible.
	for field := range raw {
		if strings.EqualFold(field, "prepared_at") || strings.EqualFold(field, "PreparedAt") {
			delete(raw, field)
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return legacyStoredFlow{}, false
	}
	var record FlowRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return legacyStoredFlow{}, false
	}
	if record.FlowID != flowID || record.SchemaVersion != schemaVersion {
		return legacyStoredFlow{}, false
	}
	return legacyStoredFlow{record: record, dependsOnPresence: presence, headlessPresent: headlessPresent}, true
}

func collapseAllDuplicatePhaseRows(phases []FlowPhase) []FlowPhase {
	seen := map[string]bool{}
	for i := 0; i < len(phases); i++ {
		id := artifacts.NormalizePhaseID(phases[i].PhaseID)
		if seen[id] {
			continue
		}
		seen[id] = true
		phases = collapseDuplicatePhaseRows(phases, i)
	}
	return phases
}

func buildStagedDatabase(path string, records []FlowRecord) error {
	file, err := openExclusiveRegular(path)
	if err != nil {
		return fmt.Errorf("create staged flow database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged flow database placeholder: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		return fmt.Errorf("open staged flow database: %w", err)
	}
	closeDB := true
	defer func() {
		if closeDB {
			_ = db.Close()
		}
	}()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		return fmt.Errorf("configure staged flow database rollback journal: mode=%q err=%w", mode, err)
	}
	if err := initializeSQLiteSchema(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin legacy flow import: %w", err)
	}
	for _, record := range records {
		data, projection, err := encodeStoredFlow(record)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)",
			projection.flowID, projection.repoPath, projection.status, projection.updatedAt, projection.beadID, projection.epicID, projection.preparedAt, projection.preparationNonce, data); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("import legacy flow %q: %w", record.FlowID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy flow import: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close staged flow database: %w", err)
	}
	closeDB = false
	if err := os.Chmod(path, artifacts.FilePerm); err != nil {
		return fmt.Errorf("secure staged flow database: %w", err)
	}
	file, err = os.Open(path)
	if err != nil {
		return fmt.Errorf("open staged flow database for sync: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync staged flow database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged flow database after sync: %w", err)
	}
	return nil
}

func openExclusiveRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(artifacts.FilePerm))
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := file.Chmod(artifacts.FilePerm); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// validateStagedDatabase checks the staged file's schema and integrity, and
// proves the read-only pass did not mutate it. When compareRecords is set it
// additionally diffs every row against expected; the fresh-build path wants
// that, the crash-resume path deliberately does not (see completeCutover).
func validateStagedDatabase(path string, expected []FlowRecord, compareRecords bool) error {
	before, err := fileFingerprint(path)
	if err != nil {
		return err
	}
	// ORDER IS LOAD-BEARING: this sidecar check must stay ahead of the
	// immutable=1 open below. immutable tells SQLite the file cannot change, so
	// it skips hot-journal recovery entirely — a stage carrying a -journal from
	// an interrupted transaction would then validate as "ok" while its pages are
	// half-committed. Checking first is what makes the immutable open safe.
	if err := requireNoSQLiteSidecars(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}, "immutable": {"1"}}))
	if err != nil {
		return err
	}
	if err := validateSQLiteSchema(db); err != nil {
		_ = db.Close()
		return err
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = db.Close()
		return fmt.Errorf("staged flow database integrity check = %q, err=%v", integrity, err)
	}
	rows, err := db.Query("SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows ORDER BY flow_id")
	if err != nil {
		_ = db.Close()
		return err
	}
	var actual []FlowRecord
	for rows.Next() {
		var flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce string
		var data []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &beadID, &epicID, &preparedAt, &preparationNonce, &data); err != nil {
			_ = rows.Close()
			_ = db.Close()
			return err
		}
		stored, err := decodeStoredFlowWithPreparation(flowID, repoPath, status, updatedAt, beadID, epicID, preparedAt, preparationNonce, data)
		if err != nil {
			_ = rows.Close()
			_ = db.Close()
			return err
		}
		actual = append(actual, stored.record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = db.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if !compareRecords {
		after, err := fileFingerprint(path)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("read-only staged database validation mutated %q", path)
		}
		return requireNoSQLiteSidecars(path)
	}
	// Sort a copy: expected is the caller's live record slice, and a validation
	// step has no business reordering it.
	expected = append([]FlowRecord(nil), expected...)
	sort.Slice(expected, func(i, j int) bool { return expected[i].FlowID < expected[j].FlowID })
	if len(actual) != len(expected) {
		return fmt.Errorf("staged flow count = %d, want %d", len(actual), len(expected))
	}
	for i := range expected {
		wantData, wantProjection, err := encodeStoredFlow(expected[i])
		if err != nil {
			return err
		}
		gotData, gotProjection, err := encodeStoredFlow(actual[i])
		if err != nil {
			return err
		}
		if wantProjection != gotProjection || !bytes.Equal(wantData, gotData) {
			return fmt.Errorf("staged flow %q does not match canonical legacy record", expected[i].FlowID)
		}
	}
	after, err := fileFingerprint(path)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("read-only staged database validation mutated %q", path)
	}
	return requireNoSQLiteSidecars(path)
}

type fingerprint struct {
	hash    [sha256.Size]byte
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func fileFingerprint(path string) (fingerprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fingerprint{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fingerprint{}, err
	}
	return fingerprint{hash: sha256.Sum256(data), size: info.Size(), mode: info.Mode(), modTime: info.ModTime()}, nil
}

func settleStagedSQLiteFile(path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		return fmt.Errorf("open staged flow database to settle journal: %w", err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		_ = db.Close()
		return fmt.Errorf("settle staged flow database rollback journal: mode=%q err=%w", mode, err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close staged flow database after settle: %w", err)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove staged flow database sidecar %q: %w", path+suffix, err)
		}
	}
	return nil
}

func requireNoSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("staged flow database unexpectedly has sidecar %q", path+suffix)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func validateAuthoritativeDatabase(path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		return fmt.Errorf("open authoritative flow database for validation: %w", err)
	}
	defer db.Close()
	if err := validateSQLiteSchema(db); err != nil {
		return fmt.Errorf("validate authoritative flow database: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil && runtime.GOOS == "windows" && (errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)) {
		return nil
	}
	return err
}
