package flowstore

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/dblease"
	sqlitedriver "modernc.org/sqlite"
)

// inspectSchemaVersion versions the report's own JSON so a consumer can tell a
// shape change from a content change.
//
// 2 adds first_compatible_release and owners. Both are additive, but the
// constant exists precisely so a consumer never has to discover that by
// parsing.
const inspectSchemaVersion = 2

// The six tiers Inspect can land in. They are values, not step indexes: two
// separate conditions report not_a_database and two report not_writable, so a
// caller discriminates on Reason.
const (
	TierMissing      = "missing"
	TierOpen         = "open"
	TierNotWritable  = "not_writable"
	TierMalformed    = "malformed"
	TierNotADatabase = "not_a_database"
	TierHeader       = "header"
)

// SQLite result codes this classification keys on, measured against
// modernc.org/sqlite v1.56.0 with this package's DSN construction.
const (
	sqliteBusy              = 5    // the database file is locked
	sqliteLocked            = 6    // a table in the database is locked
	sqliteIOErr             = 10   // some kind of disk I/O error occurred
	sqliteCantOpen          = 14   // unable to open database file
	sqliteCorrupt           = 11   // database disk image is malformed
	sqliteNotADatabase      = 26   // file is not a database
	sqliteReadonlyDirectory = 1544 // attempt to write a readonly database
	sqliteHeaderSize        = 100
	sqliteUserVersionOffset = 60
)

var sqliteHeaderMagic = []byte("SQLite format 3\x00")

// InspectWAL reports the write-ahead log's state on disk.
type InspectWAL struct {
	Present    bool `json:"present"`
	ShmPresent bool `json:"shm_present"`
	// Dirty means a -wal file exists with non-zero size.
	Dirty bool `json:"dirty"`
}

// InspectExecutable names the binary that produced the report.
type InspectExecutable struct {
	Path         string `json:"path"`
	BuildVersion string `json:"build_version"`
	Schema       int    `json:"schema"`
}

// InspectMigrationOwner is the PID the bootstrap lock file carries. Verified is
// always false: a stale PID from a crashed migrator is indistinguishable from a
// live one, so this is evidence for a human and never drives a refusal.
type InspectMigrationOwner struct {
	PID      int  `json:"pid"`
	Verified bool `json:"verified"`
}

// InspectOwner is one live holder of the owners lease.
//
// Verified is always TRUE, and that is the difference from InspectMigrationOwner
// above: this record was published by a process that still holds an exclusive
// flock on it, so its liveness was proved rather than inferred from a PID that
// a crash could have left behind.
type InspectOwner struct {
	PID           int    `json:"pid"`
	BuildVersion  string `json:"build_version"`
	Commit        string `json:"commit,omitempty"`
	Executable    string `json:"executable,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	StartedAt     string `json:"started_at,omitempty"`
	Verified      bool   `json:"verified"`
}

// InspectReport is the answer `approach db inspect --json` prints. Every
// nullable field is a pointer so "not applicable" and "zero" stay distinct —
// reporting sidecar_stale: false in a tier that never compared anything would
// assert agreement nobody checked.
type InspectReport struct {
	SchemaVersion           int64      `json:"schema_version"`
	Path                    string     `json:"path"`
	Tier                    string     `json:"tier"`
	Readable                bool       `json:"readable"`
	UserVersion             *int64     `json:"user_version"`
	CheckpointedUserVersion *int64     `json:"checkpointed_user_version"`
	WAL                     InspectWAL `json:"wal"`
	JournalMode             *string    `json:"journal_mode"`
	DirectoryMode           *string    `json:"directory_mode"`
	GenerationID            *string    `json:"generation_id"`
	MinReaderGeneration     *int64     `json:"min_reader_generation"`
	MinWriterGeneration     *int64     `json:"min_writer_generation"`
	// FirstCompatibleRelease is the earliest release that opens a database at
	// the version found here, taken from this build's compatibility manifest.
	// Null when the version is one this build does not declare — a database
	// from a newer build, or a predecessor the manifest cannot map honestly.
	FirstCompatibleRelease *string                `json:"first_compatible_release"`
	SidecarStale           *bool                  `json:"sidecar_stale"`
	Executable             InspectExecutable      `json:"executable"`
	MigrationOwner         *InspectMigrationOwner `json:"migration_owner"`
	// Owners are the live holders of the owners lease, each verified. Always an
	// array, never null: "no live holders" is an answer, and a consumer must
	// not have to distinguish it from "not checked".
	Owners     []InspectOwner `json:"owners"`
	Warnings   []string       `json:"warnings"`
	Reason     *string        `json:"reason"`
	NextAction *string        `json:"next_action"`
}

// Inspect answers "what is in this state root, and can approach open it" for an
// operator who is already stuck.
//
// It never constructs a Store, never takes the bootstrap lock — that lock is
// held for up to two minutes by a running migration, and this is the command
// you reach for while one is running — never opens with immutable=1, which
// would hide uncheckpointed WAL content behind a stale checkpointed view, and
// never routes through SecureCanonicalRoot, which would chmod the very
// directory the not_writable tier exists to report.
//
// Its error return is reserved for the cases where it cannot produce a report
// at all. A database it merely could not open is a report, not an error.
func Inspect(root string) (InspectReport, error) {
	if root == "" {
		defaultRoot, err := DefaultRoot()
		if err != nil {
			return InspectReport{}, err
		}
		root = defaultRoot
	}
	root, err := artifacts.RequireAbsoluteRoot(root, "flow")
	if err != nil {
		return InspectReport{}, err
	}
	// Best effort: an absent root is a report (tier missing), not a failure.
	if canonical, err := artifacts.ResolveCanonicalRoot(root, "flow store root"); err == nil {
		root = canonical
	}
	path := filepath.Join(root, databaseFilename)

	executablePath, buildVersion := launcherIdentity()
	report := InspectReport{
		SchemaVersion: inspectSchemaVersion,
		Path:          path,
		Warnings:      []string{},
		Owners:        []InspectOwner{},
		Executable: InspectExecutable{
			Path:         executablePath,
			BuildVersion: buildVersion,
			Schema:       databaseSchemaVersion,
		},
	}
	applyDirectoryMode(&report, root)
	applyWALState(&report, path)
	applyMigrationOwner(&report, root)
	applyOwners(&report, root)
	// The sidecar is read independently of the tier: it describes the root, not
	// the open, and a root whose database will not open is exactly where its
	// provenance is most worth having.
	applySidecar(&report, root)

	classifyDatabase(&report, root, path)
	return report, nil
}

func applyDirectoryMode(report *InspectReport, root string) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	mode := fmt.Sprintf("%04o", info.Mode().Perm())
	report.DirectoryMode = &mode
	if info.Mode().Perm() != artifacts.DirPerm {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"flow store root %q has permissions %s, not %04o", root, mode, artifacts.DirPerm))
	}
}

func applyWALState(report *InspectReport, path string) {
	if info, err := os.Stat(path + "-wal"); err == nil {
		report.WAL.Present = true
		report.WAL.Dirty = info.Size() > 0
	}
	if _, err := os.Stat(path + "-shm"); err == nil {
		report.WAL.ShmPresent = true
	}
}

func applyMigrationOwner(report *InspectReport, root string) {
	data, err := os.ReadFile(filepath.Join(root, bootstrapLockFilename))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	report.MigrationOwner = &InspectMigrationOwner{PID: pid}
}

// applyOwners reports the live holders. Deliberately does NOT reap the dead
// ones: this command never mutates the root it is diagnosing, and unlinking a
// holder file is a mutation. A migrator's scan does the reaping.
func applyOwners(report *InspectReport, root string) {
	live, err := dblease.Observe(root)
	if err != nil {
		return
	}
	for _, record := range live {
		owner := InspectOwner{
			PID:           record.PID,
			BuildVersion:  record.BuildVersion,
			Commit:        record.Commit,
			Executable:    record.Executable,
			SchemaVersion: record.SchemaVersion,
			Verified:      true,
		}
		if !record.StartedAt.IsZero() {
			owner.StartedAt = record.StartedAt.UTC().Format(time.RFC3339)
		}
		report.Owners = append(report.Owners, owner)
	}
}

// applyFirstCompatibleRelease answers the only manifest question an operator
// can act on. Set from user_version rather than from the sidecar: user_version
// is authoritative, and the sidecar is a cache that is never believed.
func applyFirstCompatibleRelease(report *InspectReport, userVersion int64) {
	entry, ok := manifestEntry(userVersion)
	if !ok || entry.FirstCompatibleRelease == "" {
		return
	}
	release := entry.FirstCompatibleRelease
	report.FirstCompatibleRelease = &release
}

func applySidecar(report *InspectReport, root string) {
	sidecar, ok := readSidecar(root)
	if !ok {
		return
	}
	generation := sidecar.GenerationID
	report.GenerationID = &generation
	minReader := sidecar.MinReaderGeneration
	report.MinReaderGeneration = &minReader
	minWriter := sidecar.MinWriterGeneration
	report.MinWriterGeneration = &minWriter
}

// classifyDatabase lands the report in exactly one tier. The order is what
// makes them mutually exclusive and total.
func classifyDatabase(report *InspectReport, root, path string) {
	// Lstat, not Stat: Stat would follow a symlinked approach.db into tier 1
	// and report on whatever it pointed at.
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		reportMissing(report, root)
		return
	case err != nil:
		unreadable(report, TierNotADatabase, fmt.Sprintf("cannot inspect %s: %v", path, err),
			"check the path and its permissions")
		return
	case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
		unreadable(report, TierNotADatabase,
			fmt.Sprintf("%s is a %s, not a regular file", path, describeFileKind(info.Mode())),
			"move it aside; approach will not open a non-regular database path")
		return
	}

	userVersion, journalMode, openErr := readInspectableDatabase(path)
	if openErr == nil {
		report.UserVersion = &userVersion
		report.JournalMode = &journalMode
		// Reading PRAGMAs proves the FILE is a database, not that it is a flow
		// store. NewStore also runs validateAuthoritativeDatabase, so a
		// current-version database missing the flows table or an index is
		// rejected there while this command called it `open` — the diagnostic
		// contradicting the thing it exists to diagnose. Reported as malformed
		// because that is what it is to approach, and because backups/ is the
		// same right answer as for a corrupt page.
		//
		// Ahead of sidecar_stale on purpose: a shape this build rejects is not a
		// database to draw a provenance conclusion about.
		if shapeErr := inspectSchemaShape(path, userVersion); shapeErr != nil {
			unreadable(report, TierMalformed, shapeErr.Error(),
				"restore from "+filepath.Join(filepath.Dir(path), backupDirName)+"/")
			return
		}
		// sidecar_stale is a COMPARISON, so it is true or false only on a
		// database this build would actually open, and null everywhere else.
		stale := report.MinWriterGeneration != nil && *report.MinWriterGeneration != userVersion
		if sidecar, ok := readSidecar(root); ok {
			stale = sidecar.PhysicalVersion != userVersion
		}
		report.SidecarStale = &stale
		applyFirstCompatibleRelease(report, userVersion)
		report.Tier = TierOpen
		report.Readable = true
		// A database from a newer build reads cleanly here — that is the whole
		// point of a diagnostic that never refuses — but leaving next_action
		// empty makes the one command that still answers say nothing about the
		// one thing to do. The advice is UPGRADE, never restore: rolling back
		// to a pre-migration backup would discard everything the newer build
		// has written since.
		if userVersion > databaseSchemaVersion {
			reason := fmt.Sprintf("database schema %d was written by a newer approach; this build writes %d",
				userVersion, databaseSchemaVersion)
			nextAction := "upgrade approach; this build cannot open a newer flow database"
			report.Reason = &reason
			report.NextAction = &nextAction
		}
		return
	}
	classifyOpenFailure(report, path, openErr)
}

func reportMissing(report *InspectReport, root string) {
	// The next action depends on what else is in the root: reporting "no flow
	// database" over a legacy corpus would be actively misleading now that
	// importing one is migrator-only. A flows.legacy tombstone without a
	// stage is an incomplete older cutover: migrate refuses it and asks for
	// `mv flows.legacy flows` first, so that case must not share the
	// "not yet imported / run db migrate" branch.
	if present, err := inspectReservedDirectory(filepath.Join(root, "flows")); err == nil && present {
		unreadable(report, TierMissing, "legacy flow corpus not yet imported", "run 'approach db migrate'")
		return
	}
	if present, err := inspectReservedDirectory(filepath.Join(root, "flows.legacy")); err == nil && present {
		if _, err := os.Lstat(filepath.Join(root, stageFilename)); err != nil {
			tombstonePath := filepath.Join(root, "flows.legacy")
			legacyPath := filepath.Join(root, "flows")
			unreadable(report, TierMissing, "incomplete older cutover",
				fmt.Sprintf("run `mv %s %s` and start approach again",
					shellQuote(tombstonePath), shellQuote(legacyPath)))
			return
		}
	}
	if _, err := os.Lstat(filepath.Join(root, stageFilename)); err == nil {
		unreadable(report, TierMissing, "interrupted cutover", "run 'approach db migrate'")
		return
	}
	unreadable(report, TierMissing, "no flow database",
		"run 'approach flow create' or start approach to create one")
}

// classifyOpenFailure covers tiers 2 through 5. Every branch keys on the error
// SQLite returned, never on the directory mode — the mode is not the authority
// when the process is root or an ACL is in play.
func classifyOpenFailure(report *InspectReport, path string, cause error) {
	dir := filepath.Dir(path)
	switch code := sqliteResultCode(cause); {
	case code == sqliteReadonlyDirectory:
		unreadable(report, TierNotWritable,
			fmt.Sprintf("the directory holding %s is not writable: %v", databaseFilename, cause),
			"re-run with write access to "+dir)
	case code == sqliteCantOpen && report.WAL.Present && !report.WAL.ShmPresent && !directoryIsWritable(dir):
		// The post-crash state this command exists to explain: SQLite cannot
		// create the -shm it needs to read the WAL, so it answers CANTOPEN
		// rather than READONLY_DIRECTORY. Under a rule that keyed only on 1544
		// this would fall through to the header tier and be reported as
		// corruption.
		unreadable(report, TierNotWritable,
			fmt.Sprintf("a dirty write-ahead log needs a -shm file, and %s is not writable", dir),
			"re-run with write access to "+dir)
	case code == sqliteCorrupt:
		unreadable(report, TierMalformed, "database disk image is malformed",
			"restore from "+filepath.Join(dir, backupDirName)+"/")
	case code == sqliteNotADatabase:
		unreadable(report, TierNotADatabase, "file is not a database",
			path+" is not a SQLite database")
	default:
		classifyByHeader(report, path, dir)
	}
}

// classifyByHeader is the residual tier, for a file SQLite would not open with
// any of the codes above.
func classifyByHeader(report *InspectReport, path, dir string) {
	header, err := readFileHeader(path)
	if err != nil {
		// A 0000-mode file, or a directory with x but no r, surfaces as
		// CANTOPEN with a WRITABLE directory and so falls past tier 2. Without
		// this branch the residual tier's first act fails with EACCES and no
		// tier covers it.
		unreadable(report, TierNotWritable, fmt.Sprintf("cannot read %s: %v", path, err),
			"re-run with read access to "+path)
		return
	}
	if len(header) < len(sqliteHeaderMagic) || string(header[:len(sqliteHeaderMagic)]) != string(sqliteHeaderMagic) {
		// Gated on the magic so a large non-database file can never be reported
		// with a fabricated version parsed out of arbitrary bytes.
		unreadable(report, TierNotADatabase, "file does not carry the SQLite header magic",
			path+" is not a SQLite database")
		return
	}
	if len(header) < sqliteHeaderSize {
		// Defensive: SQLite classifies a truncated file that still carries the
		// magic as malformed (code 11) before this tier is reached, so this
		// branch exists so the header parse below can never read past the bytes
		// it actually has.
		unreadable(report, TierHeader, "file too short for a SQLite header",
			"restore from "+filepath.Join(dir, backupDirName)+"/")
		return
	}
	checkpointed := int64(binary.BigEndian.Uint32(header[sqliteUserVersionOffset : sqliteUserVersionOffset+4]))
	report.CheckpointedUserVersion = &checkpointed
	// A floor, not the live version: anything in an uncheckpointed WAL is
	// invisible here, and a zero on its own is never evidence of corruption.
	unreadable(report, TierHeader, "readable only as a checkpointed header",
		"run 'approach db inspect' after freeing the database, or restore from "+
			filepath.Join(dir, backupDirName)+"/ if this persists")
}

func unreadable(report *InspectReport, tier, reason, nextAction string) {
	report.Tier = tier
	report.Readable = false
	report.Reason = &reason
	report.NextAction = &nextAction
}

// inspectBusyTimeout bounds the wait for a locked database. It is deliberately
// short: this is the command an operator runs precisely because something else
// is holding the database, and the answer for that case is the header tier, not
// a five-second pause followed by the same report.
const inspectBusyTimeout = time.Second

// readInspectableDatabase opens read-only and reads the two facts that prove
// the file is a usable flow database. Schema-object and record-shape validation
// are deliberately skipped: a future schema must report cleanly here, which is
// the whole of "usable against a future schema".
// inspectSchemaShape validates the object shape of a database whose stored
// version is the one THIS build defines. Only that version, deliberately: a
// predecessor is legitimately not in the current shape and inspect must keep
// reporting it as readable-but-unmigrated, and a version from a newer build is
// a shape this binary has no definition of, so asserting the current one would
// report every future database as malformed. Both are already classified by
// user_version alone.
//
// A second open rather than threading a handle out of readInspectableDatabase:
// this is a one-shot diagnostic command, and the alternative is leaking an open
// *sql.DB through a classifier whose other branches never hold one.
func inspectSchemaShape(path string, userVersion int64) error {
	if userVersion != databaseSchemaVersion {
		return nil
	}
	millis, err := sqliteBusyTimeoutMillis(inspectBusyTimeout)
	if err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{
		"mode":    {"ro"},
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis), "query_only(1)"},
	}))
	if err != nil {
		// The open above already succeeded once. A failure here is not evidence
		// of a malformed database, so it must not be reported as one.
		return nil
	}
	defer func() { _ = db.Close() }()
	err = validateSQLiteSchema(db)
	if isTransientSQLiteError(err) {
		// The database would not answer — BUSY from the migration this command is
		// explicitly meant to stay usable during, CANTOPEN, an I/O error. That is
		// not evidence of a bad shape, and the tier it would otherwise land in
		// tells the operator to restore from a backup.
		return nil
	}
	// Everything else propagates, CORRUPT and NOTADB especially. PRAGMA
	// user_version and journal_mode are answered out of the file header, so a
	// database whose sqlite_master or another schema page is damaged reads them
	// fine and is only caught here. Suppressing that would report it as `open`
	// while NewStore rejects it — the misclassification this check exists to end.
	return err
}

// isTransientSQLiteError reports an error that says "not now" rather than
// "not a flow database".
func isTransientSQLiteError(err error) bool {
	return isTransientSQLiteCode(sqliteResultCode(err))
}

// isTransientSQLiteCode is split out because modernc's Error has unexported
// fields and no constructor, so the classification is only testable on the code.
// The primary code is masked out of the extended one: the driver reports
// extended codes (SQLITE_READONLY_DIRECTORY is 1544), and BUSY and IOERR each
// have a family of them. Zero means the error carried no SQLite code at all —
// a plain shape mismatch — which is emphatically not transient.
func isTransientSQLiteCode(code int) bool {
	if code == 0 {
		return false
	}
	switch code & 0xff {
	case sqliteBusy, sqliteLocked, sqliteIOErr, sqliteCantOpen:
		return true
	default:
		return false
	}
}

func readInspectableDatabase(path string) (int64, string, error) {
	millis, err := sqliteBusyTimeoutMillis(inspectBusyTimeout)
	if err != nil {
		return 0, "", err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{
		"mode":    {"ro"},
		"_pragma": {fmt.Sprintf("busy_timeout(%d)", millis), "query_only(1)"},
	}))
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = db.Close() }()
	var userVersion int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return 0, "", err
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return 0, "", err
	}
	return userVersion, strings.ToLower(journalMode), nil
}

func readFileHeader(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, sqliteHeaderSize)
	read, err := file.Read(header)
	if err != nil && read == 0 {
		return nil, err
	}
	return header[:read], nil
}

func sqliteResultCode(err error) int {
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()
	}
	return 0
}

// directoryIsWritable probes with access(2) rather than reading mode bits: the
// mode is not the authority when the process is root or an ACL is in play. The
// W_OK constant lives only in golang.org/x/sys/unix, which is an indirect
// dependency here and must not be promoted, so the mode is a literal.
func directoryIsWritable(dir string) bool {
	return syscall.Access(dir, 0x2 /* W_OK */) == nil
}

func describeFileKind(mode os.FileMode) string {
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode.IsDir():
		return "directory"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeDevice != 0:
		return "device"
	case mode&os.ModeNamedPipe != 0:
		return "named pipe"
	default:
		return "special file"
	}
}
