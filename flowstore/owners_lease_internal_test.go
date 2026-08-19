package flowstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/dblease"
)

// predecessorRoot stages a database one generation behind this build, which is
// the only input under which the owners lease is consulted at all.
func predecessorRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	db := createParentReleaseV5DatabaseAt(t, filepath.Join(root, databaseFilename))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func holdLease(t *testing.T, root string, schema int, build string) *dblease.Holder {
	t.Helper()
	holder, err := dblease.Acquire(root, dblease.Identity{
		BuildVersion:  build,
		Executable:    "/opt/approach/" + build,
		SchemaVersion: schema,
		StartedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Release() })
	return holder
}

// TestMigrationNamesEveryBlockingHolderInOneRefusal is the acceptance
// criterion. Plural is the point: an operator told about one of two processes
// closes it, re-runs, and is refused again.
func TestMigrationNamesEveryBlockingHolderInOneRefusal(t *testing.T) {
	root := predecessorRoot(t)
	holdLease(t, root, databaseSchemaVersion-1, "v0.10.2")
	holdLease(t, root, databaseSchemaVersion-1, "v0.10.1")

	_, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err == nil {
		t.Fatal("migration proceeded with two incompatible live holders")
	}
	if !errors.Is(err, ErrMigrationBlockedByOwners) {
		t.Fatalf("err = %v, want one wrapping ErrMigrationBlockedByOwners", err)
	}
	message := err.Error()
	for _, want := range []string{
		fmt.Sprintf("pid %d", os.Getpid()),
		"v0.10.2", "v0.10.1",
		"/opt/approach/v0.10.2", "/opt/approach/v0.10.1",
		"approach db migrate",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("refusal %q is missing %q", message, want)
		}
	}
	if storedSchemaVersion(t, root) != databaseSchemaVersion-1 {
		t.Fatal("a refused migration advanced the schema anyway")
	}
}

func TestAHolderAtTheTargetSchemaDoesNotBlockMigration(t *testing.T) {
	root := predecessorRoot(t)
	holdLease(t, root, databaseSchemaVersion, "v0.10.3")

	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("a holder already at the target schema blocked the migration: %v", err)
	}
	defer store.Close()
	if storedSchemaVersion(t, root) != databaseSchemaVersion {
		t.Fatal("the migration did not run")
	}
}

func TestADeadHolderDoesNotBlockMigration(t *testing.T) {
	root := predecessorRoot(t)
	holder := holdLease(t, root, databaseSchemaVersion-1, "v0.10.2")
	// Release stands in for the process dying: both end with no live lock on
	// the file, which is the only thing the scan can observe.
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("a released holder blocked the migration: %v", err)
	}
	defer store.Close()
	if storedSchemaVersion(t, root) != databaseSchemaVersion {
		t.Fatal("the migration did not run")
	}
}

// TestAMigratorDoesNotBlockItself is the TUI's case: it holds its own lease for
// the process lifetime and migrates while still holding it. Releasing and
// reacquiring around the migration would open exactly the window the lease
// exists to close.
func TestAMigratorDoesNotBlockItself(t *testing.T) {
	root := predecessorRoot(t)
	self := holdLease(t, root, databaseSchemaVersion-1, "self")

	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator, OwnerNonce: self.Nonce()})
	if err != nil {
		t.Fatalf("a migrator blocked itself: %v", err)
	}
	defer store.Close()
	if storedSchemaVersion(t, root) != databaseSchemaVersion {
		t.Fatal("the migration did not run")
	}
}

// TestTheOwnersLeaseIsScannedBeforeTheBootstrapLock asserts the total
// acquisition order as a property. The reverse order would hold the bootstrap
// lock — which a migration keeps for up to two minutes — across a scan that can
// refuse, so every other process would block waiting for a migration that was
// never going to happen.
func TestTheOwnersLeaseIsScannedBeforeTheBootstrapLock(t *testing.T) {
	root := predecessorRoot(t)
	var steps []string
	restore := dblease.SetAcquisitionProbe(func(step string) { steps = append(steps, step) })
	defer restore()

	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scanAt, lockAt := -1, -1
	for i, step := range steps {
		if step == probeOwnersScan && scanAt < 0 {
			scanAt = i
		}
		if step == dblease.ProbeBootstrapLock && lockAt < 0 {
			lockAt = i
		}
	}
	if scanAt < 0 || lockAt < 0 {
		t.Fatalf("steps = %v, want both an owners scan and a bootstrap lock", steps)
	}
	if scanAt > lockAt {
		t.Fatalf("steps = %v: the owners lease must be scanned before the bootstrap lock", steps)
	}
}

// TestACurrentDatabaseNeverScansTheOwnersLease is the ordinary case: nothing to
// advance, so nothing to refuse, and no filesystem cost for the common open.
func TestACurrentDatabaseNeverScansTheOwnersLease(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	holdLease(t, root, databaseSchemaVersion-1, "v0.10.2")

	var scanned bool
	restore := dblease.SetAcquisitionProbe(func(step string) {
		if step == probeOwnersScan {
			scanned = true
		}
	})
	defer restore()
	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("an incompatible holder blocked a no-op open: %v", err)
	}
	defer reopened.Close()
	if scanned {
		t.Fatal("a no-op open scanned the owners lease")
	}
}

// TestARootWithNoOwnersDirectoryMigratesExactlyAsBefore is the no-regression
// case for every root that predates this lease.
func TestARootWithNoOwnersDirectoryMigratesExactlyAsBefore(t *testing.T) {
	root := predecessorRoot(t)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if storedSchemaVersion(t, root) != databaseSchemaVersion {
		t.Fatal("the migration did not run")
	}
	if _, err := os.Lstat(dblease.Dir(root)); err == nil {
		t.Fatal("a migration created an owners directory nobody asked for")
	}
}

func TestInspectReportsVerifiedOwners(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	holdLease(t, root, databaseSchemaVersion, "v0.10.3")

	report, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Owners) != 1 {
		t.Fatalf("owners = %#v, want one", report.Owners)
	}
	owner := report.Owners[0]
	if owner.PID != os.Getpid() || owner.BuildVersion != "v0.10.3" || owner.SchemaVersion != databaseSchemaVersion {
		t.Fatalf("owner = %#v", owner)
	}
	// Unlike migration_owner, these ARE verified: the flock probe proved the
	// holder is alive rather than reading a PID out of a file.
	if !owner.Verified {
		t.Fatal("a live holder was reported unverified")
	}
}
