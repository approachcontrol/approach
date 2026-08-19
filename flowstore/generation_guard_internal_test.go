package flowstore

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAStoreWhoseUserVersionMovedAbortsEveryWrite(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	created, err := store.Create(FlowRecord{Title: "Before", Instructions: "x", RepoPath: filepath.Join(root, "repo")})
	if err != nil {
		t.Fatal(err)
	}

	// Something else migrated underneath this live handle.
	stampUserVersion(t, root, databaseSchemaVersion+1)

	_, err = store.SetPhase(PhaseUpdate{FlowID: created.FlowID, PhaseID: created.Phases[0].PhaseID, Status: PhaseCompleted})
	if !errors.Is(err, ErrDatabaseGenerationChanged) {
		t.Fatalf("write after a user_version change = %v, want ErrDatabaseGenerationChanged", err)
	}
	if !strings.Contains(err.Error(), "6") || !strings.Contains(err.Error(), "7") {
		t.Fatalf("refusal %q must name both the observed and the expected version", err)
	}
	// Sticky: every SUBSEQUENT operation aborts too, so a caller cannot retry
	// its way into writing through a handle whose database was replaced.
	if _, err := store.Create(FlowRecord{Title: "After", Instructions: "x", RepoPath: filepath.Join(root, "repo")}); !errors.Is(err, ErrDatabaseGenerationChanged) {
		t.Fatalf("a later write = %v, want ErrDatabaseGenerationChanged", err)
	}
}

// TestRevalidateCatchesAGenerationChangeWithAnUnchangedUserVersion is the case
// user_version cannot see: a restore put a different database at the same
// schema. The sidecar's generation ID is the only thing that moved.
func TestRevalidateCatchesAGenerationChangeWithAnUnchangedUserVersion(t *testing.T) {
	root := t.TempDir()
	if err := writeSidecar(root, databaseSchemaVersion, sidecarProvenanceMigrated,
		"1111111111111111", migrationOutcome{}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := writeSidecar(root, databaseSchemaVersion, "restored",
		"2222222222222222", migrationOutcome{}); err != nil {
		t.Fatal(err)
	}
	revalidateErr := store.Revalidate()
	if !errors.Is(revalidateErr, ErrDatabaseGenerationChanged) {
		t.Fatalf("Revalidate after a generation change = %v, want ErrDatabaseGenerationChanged", revalidateErr)
	}
	if !strings.Contains(revalidateErr.Error(), "1111111111111111") ||
		!strings.Contains(revalidateErr.Error(), "2222222222222222") {
		t.Fatalf("refusal %q must name both generations", revalidateErr)
	}
	if _, err := store.Create(FlowRecord{Title: "After", Instructions: "x", RepoPath: filepath.Join(root, "repo")}); !errors.Is(err, ErrDatabaseGenerationChanged) {
		t.Fatalf("a write after a failed Revalidate = %v, want ErrDatabaseGenerationChanged", err)
	}
}

func TestAnUnchangedGenerationRevalidatesCleanly(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 3; i++ {
		if err := store.Revalidate(); err != nil {
			t.Fatalf("Revalidate on an untouched root = %v", err)
		}
	}
	if _, err := store.Create(FlowRecord{Title: "Still fine", Instructions: "x", RepoPath: filepath.Join(root, "repo")}); err != nil {
		t.Fatalf("write after a clean Revalidate = %v", err)
	}
}

// TestTheSidecarGenerationReadIsThrottled pins the trade the guard makes: an
// unthrottled read costs a stat and a read per mutation, and the window it
// closes is bounded by one second of a restore that already refuses while any
// live holder exists.
func TestTheSidecarGenerationReadIsThrottled(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backend := store.backend.(*sqliteBackend)
	reads := 0
	original := backend.readGeneration
	t.Cleanup(func() { backend.readGeneration = original })
	backend.readGeneration = func() string {
		reads++
		return original()
	}
	now := time.Now()
	backend.generationClock = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if _, err := store.Create(FlowRecord{Title: "Throttled", Instructions: "x", RepoPath: filepath.Join(root, "repo")}); err != nil {
			t.Fatal(err)
		}
	}
	if reads > 1 {
		t.Fatalf("the sidecar was read %d times inside one throttle window", reads)
	}
	now = now.Add(2 * sidecarGenerationThrottle)
	if _, err := store.Create(FlowRecord{Title: "Past the window", Instructions: "x", RepoPath: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	if reads < 2 {
		t.Fatalf("the sidecar was not re-read after the throttle window (%d reads)", reads)
	}
}

// TestAReaderIsNotGuarded: the guard fences WRITES. A reader that observed a
// migration mid-read gets a partial list, which the read path already reports,
// and refusing every read on a root someone else migrated would take out
// `flow list` for no safety gain.
func TestAReaderIsNotGuarded(t *testing.T) {
	root := t.TempDir()
	seed, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Create(FlowRecord{Title: "Readable", Instructions: "x", RepoPath: filepath.Join(root, "repo")}); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stampUserVersion(t, root, databaseSchemaVersion+1)
	if _, err := store.List(FlowFilter{}); err != nil {
		t.Fatalf("List after a generation change = %v, want it to still read", err)
	}
}
