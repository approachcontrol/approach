package flowstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

// This is the only test that drives a real planstoreSyncer against planstore's
// actual global mutation lock rather than the injected fake, so it is where the
// end-to-end plan-lock behavior lives.
//
// It pins two things at once. First, the plan-lock budget is the STORE's
// resolved lock timeout, plumbed through planstoreSyncer: nothing else fails if
// an implementer forgets to pass it, because planstore silently falls back to
// its own much longer default and every other test still passes. Second, the
// original property that survived the move — a contended plan lock must not
// stall unrelated Flow writes. That used to depend on a nested-wait cap held
// below the Flow busy timeout; now it holds because the sync runs after the Flow
// write commits and holds no Flow writer at all.
//
// The plan lock is held for the whole test on purpose. Releasing it early would
// race the budget and make both assertions timing-dependent. There is
// deliberately no "committed state is readable" assertion here either — it would
// hold only until the parked sync gives up and the compensation demotes the
// phase. TestSetPhaseCommitsBeforeTheLinkedPlanSyncRuns pins that
// deterministically through the sync hook.
func TestContendedPlanLockFailsFastWithoutStallingUnrelatedFlowWrites(t *testing.T) {
	const lockTimeout = 250 * time.Millisecond
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: lockTimeout})
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

	// Hold planstore's global mutation lock from "another process" for the whole
	// test, so the sync can never acquire it.
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

	phaseID := linked.Phases[0].PhaseID
	completion := make(chan error, 1)
	go func() {
		if _, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: phaseID, Status: PhaseRunning}); err != nil {
			completion <- err
			return
		}
		_, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: phaseID, Status: PhaseCompleted})
		completion <- err
	}()

	// Give the completion time to commit and park its sync on the plan lock.
	time.Sleep(100 * time.Millisecond)

	// The unrelated Flow must still be writable, and quickly: no Flow writer is
	// held across the plan lock any more.
	start := time.Now()
	_, err = store.SetAutoMode(AutoModeUpdate{FlowID: unrelated.FlowID, Enabled: false})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unrelated flow write failed while a linked-plan sync was parked on the plan lock: %v"+
			" (after %s). The sync must not hold the database-wide Flow writer.", err, elapsed)
	}
	if elapsed >= lockTimeout {
		t.Fatalf("unrelated flow write took %s behind a parked linked-plan sync, want well under the %s"+
			" writer budget", elapsed, lockTimeout)
	}

	// The sync itself gives up on the STORE's budget, not planstore's default.
	select {
	case err := <-completion:
		if err == nil || !strings.Contains(err.Error(), "sync linked plan phase") {
			t.Fatalf("SetPhase(completed) error = %v, want a linked plan sync failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("linked-plan sync did not give up within a second; the Store's lock budget is not" +
			" reaching planstore, which fell back to its own longer default")
	}
	if got := phaseFromStore(t, store, linked.FlowID, phaseID).Status; got != PhaseNeedsAttention {
		t.Fatalf("phase status after the sync failure = %q, want %q", got, PhaseNeedsAttention)
	}
}
