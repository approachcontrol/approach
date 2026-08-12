package flowstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

// The linked-plan sync runs inside the Flow write transaction, which under
// SQLite is the DATABASE-WIDE write lock rather than the file store's per-Flow
// lock. planstore's mutation lock is itself a single global file lock across
// every plan and process, so a Flow writer parked on it stalls every other Flow
// write.
//
// That is accepted (see backend.update clause 5 and syncLinkedPlanPhase), but
// only because the nested plan-lock wait is capped well below the Flow busy
// timeout. At planstore's own 5s default the two budgets are equal and a parked
// syncer burns another writer's entire timeout, so an unrelated Flow — no linked
// plan, no relationship at all — fails outright with "database is locked".
//
// This pins the gap, not the mechanism: a completely unrelated Flow must still
// be writable while a plan-linked phase completion is stuck on the plan lock.
func TestContendedPlanLockDoesNotFailUnrelatedFlowWrites(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	linked, err := store.Create(FlowRecord{Title: "linked", Instructions: "i", RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := store.Create(FlowRecord{Title: "unrelated", Instructions: "i", RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}

	// Point the linked Flow at a plan id without going through SetPlanLink, which
	// would require a real plan on disk. syncLinkedPlanPhase only needs PlanID to
	// be non-empty to attempt the sync.
	if _, err := store.backend.update(linked.FlowID, func(sess flowSession) (FlowRecord, error) {
		stored, ok, err := sess.get()
		if err != nil || !ok {
			t.Fatalf("get linked flow: ok=%v err=%v", ok, err)
		}
		record := stored.record
		record.PlanID = "some-plan"
		return record, sess.save(record)
	}); err != nil {
		t.Fatal(err)
	}

	// Hold planstore's global mutation lock from "another process".
	lockDir := filepath.Join(root, "plans", ".locks")
	if err := os.MkdirAll(lockDir, artifacts.DirPerm); err != nil {
		t.Fatal(err)
	}
	releasePlanLock, err := artifacts.AcquireFileLock(
		filepath.Join(lockDir, "mutations.lock"), "test-held plan mutation lock", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer releasePlanLock()

	// Complete a phase on the linked Flow. Its sync will park on the plan lock
	// while holding the database-wide Flow writer.
	stalled := make(chan struct{})
	go func() {
		defer close(stalled)
		phaseID := linked.Phases[0].PhaseID
		_, _ = store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: phaseID, Status: PhaseRunning})
		_, _ = store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: phaseID, Status: PhaseCompleted})
	}()

	// Give the syncer time to take the Flow writer and park on the plan lock.
	time.Sleep(300 * time.Millisecond)

	// The unrelated Flow must still be writable. It may wait, but it must not be
	// starved out of its entire busy timeout.
	start := time.Now()
	_, err = store.SetAutoMode(AutoModeUpdate{FlowID: unrelated.FlowID, Enabled: false})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unrelated flow write failed while a linked-plan sync was stuck on the plan lock: %v"+
			" (after %s). The nested plan-lock wait must stay well under the Flow busy timeout.", err, elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("unrelated flow write took %s behind a stuck linked-plan sync; the nested plan-lock"+
			" budget is too close to the Flow busy timeout", elapsed)
	}

	releasePlanLock()
	<-stalled
}
