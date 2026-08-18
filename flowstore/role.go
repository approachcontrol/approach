package flowstore

import (
	"errors"
	"fmt"
	"os"

	"github.com/approachcontrol/approach/internal/version"
)

// Role names what an opener is allowed to do to the database it opens.
//
// The zero value is RoleMigrator, deliberately: every bare
// NewStore(StoreOptions{Root: root}) in the tree — and there are well over a
// hundred of them in tests alone — keeps today's behaviour byte for byte. A
// restriction that is inherited by default is a restriction nobody can reason
// about; naming the role at the entry point is the whole point of this seam.
type Role int

const (
	// RoleMigrator may advance the schema. Exactly one process-level entry point
	// should hold it: `approach db migrate` and TUI startup.
	RoleMigrator Role = iota
	// RoleReader opens read-only. It never migrates, never repairs the root's
	// mode, never discards a staged database, and refuses writes in Go before
	// SQLite ever sees them.
	RoleReader
	// RoleWriter reads and writes records but will not advance the schema.
	RoleWriter
)

// String names the role the way the refusal messages spell it.
func (r Role) String() string {
	switch r {
	case RoleReader:
		return "reader"
	case RoleWriter:
		return "writer"
	case RoleMigrator:
		return "migrator"
	default:
		return fmt.Sprintf("role(%d)", int(r))
	}
}

// errRoleRefusedMigration marks a refusal to migrate that is about the opener's
// role, not about the database. Like errDatabaseFromNewerBuild it must stay
// distinguishable: the database is healthy and merely unmigrated, so
// describeUnusableDatabase's "move approach.db aside and rebuild an empty one"
// advice would talk an operator into discarding a good corpus.
var errRoleRefusedMigration = errors.New("flow database open refused migration for this role")

// roleRefusal carries an exact operator-facing sentence while staying
// errors.Is-compatible with errRoleRefusedMigration. Wrapping with %w would
// prepend the sentinel's own text to a message whose wording is asserted
// verbatim by tests and read verbatim by operators.
type roleRefusal struct{ message string }

func (e *roleRefusal) Error() string { return e.message }

func (e *roleRefusal) Unwrap() error { return errRoleRefusedMigration }

// refuseRoleMigration is the one refusal for an unmigrated database.
func refuseRoleMigration(storedVersion int64, role Role) error {
	return &roleRefusal{message: fmt.Sprintf(
		"flow database schema %d needs migration to %d; run 'approach db migrate' "+
			"(this process opened the store as %s and will not migrate)",
		storedVersion, databaseSchemaVersion, role)}
}

// refuseRoleLegacyImport and refuseRoleStageResume are separate strings on
// purpose. A legacy corpus and an interrupted cutover are different situations
// with different operator answers, and telling someone staring at an
// approach.db.migrating that there is a legacy corpus to import is a wrong
// diagnosis rather than a terse one. `db inspect` draws the same distinction.
func refuseRoleLegacyImport(legacyPath string, role Role) error {
	return &roleRefusal{message: fmt.Sprintf(
		"legacy flow corpus at %s must be imported by 'approach db migrate' "+
			"(this process opened the store as %s and will not migrate)", legacyPath, role)}
}

func refuseRoleStageResume(stagePath string, role Role) error {
	return &roleRefusal{message: fmt.Sprintf(
		"interrupted flow database cutover at %s must be resumed by 'approach db migrate' "+
			"(this process opened the store as %s and will not migrate)", stagePath, role)}
}

// refuseIncompatibleBuild reports a database this build cannot open because it
// is ahead of it. It names a schema generation rather than a release name: the
// generation to release mapping lives in a compatibility manifest this build
// does not have, and inventing a version string here would be a number the
// operator cannot check.
//
// It wraps errDatabaseFromNewerBuild so describeUnusableDatabase keeps
// suppressing the rebuild advice for it.
func refuseIncompatibleBuild(storedVersion, requiredGeneration int64) error {
	path, build := launcherIdentity()
	return fmt.Errorf("%w: database schema %d needs an approach build supporting flow database"+
		" schema %d or newer, but %s is build %s (schema %d); upgrade approach",
		errDatabaseFromNewerBuild, storedVersion, requiredGeneration, path, build, databaseSchemaVersion)
}

// launcherIdentity names the binary the operator has to replace. Inside a
// launched agent that is the pinned executable the TUI exported, not this
// process — an agent told to upgrade "approach" has no way to know which copy.
func launcherIdentity() (string, string) {
	path := os.Getenv("APPROACH_EXECUTABLE")
	if path == "" {
		if executable, err := os.Executable(); err == nil {
			path = executable
		} else {
			path = "approach"
		}
	}
	build := os.Getenv("APPROACH_BUILD_VERSION")
	if build == "" {
		build = version.Version()
	}
	return path, build
}

// errReaderWrite is the Go-level answer for a write attempted through a
// read-only store. _pragma=query_only(1) is the backstop underneath, but it
// surfaces as "attempt to write a readonly database" from inside a transaction,
// which names neither the role nor the caller's mistake.
var errReaderWrite = errors.New("flow database opened read-only; this operation requires a writer")

// OpenDiagnostics reports what an open observed about the root and the database
// without repairing it. It is the replacement surface for the two checks a
// RoleReader skips — SecureCanonicalRoot's 0700 assertion and openSQLiteBackend's
// journal_mode=WAL assertion — so that dropping them is a substitution rather
// than a deletion.
type OpenDiagnostics struct {
	// DirectoryMode is the resolved root's permission bits as found.
	DirectoryMode os.FileMode
	// JournalMode is what PRAGMA journal_mode reported, lowercased.
	JournalMode string
	// SidecarStale reports an approach.db.meta.json that disagrees with
	// user_version. Only a RoleMigrator repairs it; every other role reports it.
	SidecarStale bool
	// Warnings are operator-facing notices that are not failures.
	Warnings []string
}

// IsSchemaCompatibilityRefusal reports whether err means "this binary must
// not touch this database": the database was written by a newer build, or
// this opener's role forbids the migration it needs. Both are refusals about
// the pairing of build and database, not about the request, so a caller that
// can defer its write to a compatible process (the launch controller's spool
// path) should do that rather than fail. Every other error — a locked file,
// a corrupt page, a typo'd root — is not one of these.
func IsSchemaCompatibilityRefusal(err error) bool {
	return errors.Is(err, errDatabaseFromNewerBuild) || errors.Is(err, errRoleRefusedMigration)
}
