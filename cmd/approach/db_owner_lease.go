package main

import (
	"os"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/dblease"
	"github.com/approachcontrol/approach/internal/version"
)

// acquireDatabaseOwnerLease publishes this process as a live holder of root's
// flow database, so a concurrent `approach db migrate` refuses rather than
// migrating underneath a handle that is deliberately never closed.
//
// Only LONG-LIVED processes take one: the TUI, which holds its store open for
// the whole session, and `approach serve`. Short-lived `flow` and `plan` leaves
// take nothing — they open, act, and exit inside the window a migration would
// have to lose a race with anyway, and a lease per leaf would turn every
// scripted loop into a stream of holder files.
//
// Best effort. A lease that cannot be taken is a missing safety net, not a
// reason to refuse to start: the failure mode without it is exactly today's
// behaviour, and refusing to open the TUI because a directory is read-only
// would be a strictly worse trade.
func acquireDatabaseOwnerLease(root string) *dblease.Holder {
	if root == "" {
		return nil
	}
	// Canonical, always. The lease directory sits beside the database, and the
	// migrator scans the root the STORE canonicalized; a symlinked spelling
	// (/tmp on macOS) would publish this holder into a directory nothing else
	// ever reads, which is a lease that silently protects nothing.
	canonical, err := artifacts.ResolveCanonicalRoot(root, "flow store root")
	if err != nil {
		return nil
	}
	root = canonical
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}
	holder, err := dblease.Acquire(root, dblease.Identity{
		BuildVersion:  version.Version(),
		Commit:        version.Commit(),
		Executable:    executable,
		SchemaVersion: flowstore.DatabaseSchemaVersion(),
		StartedAt:     time.Now().UTC(),
	})
	if err != nil {
		return nil
	}
	return holder
}
