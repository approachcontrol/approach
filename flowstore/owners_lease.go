package flowstore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/internal/dblease"
)

// ErrMigrationBlockedByOwners marks a migration refused because a long-lived
// process is holding this database open at a build that could not read the
// result.
//
// Distinguishable from every other refusal because the remedy is different and
// entirely in the operator's hands: nothing is wrong with the database, and the
// action is to close the named processes.
var ErrMigrationBlockedByOwners = errors.New("flow database migration blocked by a live owner")

// probeOwnersScan names the moment the migrator scans the owners lease. It is
// recorded through dblease's probe seam so the total acquisition order — owners
// lease, THEN bootstrap lock — is asserted as a property rather than read out
// of a comment.
const probeOwnersScan = "owners-scan"

// refuseMigrationForOwners reports whether a migration to databaseSchemaVersion
// may proceed, given the live holders of this root.
//
// It is called only when a migration would ADVANCE the stored version. A
// database already at the target has nothing to break, so the ordinary open
// never scans and never pays for the directory read.
//
// A holder is blocking when the build it declares cannot write the schema this
// migration would produce. A holder already AT the target is compatible — the
// common case of a second current-build process — and does not block.
//
// selfNonce excludes the caller's own holder. The TUI takes its lease first and
// keeps it while it migrates: releasing and reacquiring around the migration
// would open exactly the window this lease exists to close, and a concurrent
// `approach db migrate` could take the lease in the gap.
func refuseMigrationForOwners(root string, selfNonce string) error {
	dblease.Probe(probeOwnersScan)
	live, _, err := dblease.Scan(root, selfNonce)
	if err != nil {
		// A lease directory that cannot be read is a missing safety net, not a
		// reason to refuse a migration that would otherwise be fine. The
		// alternative fails every migration on a root whose owners directory a
		// stray chmod made unreadable, with no way to proceed.
		//
		// Fail-open here is the same posture the lease takes everywhere else —
		// acquireDatabaseOwnerLease also degrades to nil rather than refusing
		// to start — and it must stay that way as a set. A gate that refuses
		// when it cannot read, published by a holder that shrugs when it cannot
		// write, would deadlock a root that a single permission bit made
		// unreadable, with nothing left to fix it with.
		return nil
	}
	var blocking []dblease.Record
	for _, record := range live {
		if int64(record.SchemaVersion) < databaseSchemaVersion {
			blocking = append(blocking, record)
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	described := make([]string, 0, len(blocking))
	for _, record := range blocking {
		described = append(described, record.Describe())
	}
	// Every blocking holder, in one message. Naming one of two would have an
	// operator close it, re-run, and be refused again by the other.
	noun := "process is"
	if len(blocking) > 1 {
		noun = "processes are"
	}
	return fmt.Errorf("%w: %d %s holding %s open at a build older than flow database schema %d"+
		" — %s; stop these processes, then re-run 'approach db migrate'",
		ErrMigrationBlockedByOwners, len(blocking), noun, root, databaseSchemaVersion,
		strings.Join(described, "; "))
}

// IsMigrationBlockedByOwners reports whether err is the owners-lease refusal.
// Like the compatibility refusals it must stay distinguishable: the database is
// healthy, so any advice about moving it aside or restoring a backup would be a
// wrong diagnosis.
func IsMigrationBlockedByOwners(err error) bool {
	return errors.Is(err, ErrMigrationBlockedByOwners)
}
