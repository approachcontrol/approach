package flowstore

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin the post-commit ordering of the linked-plan sync: the Flow
// write commits first, the plan write happens with no Flow writer held, and the
// needs_attention compensation lands in a second, guarded update. They drive the
// boundary through store.beforeLinkedPlanPhaseSync, which fires after the plan
// store opens and before the plan write, so every assertion below is made while
// the sync is parked rather than on a timing guess.

// oneShotSyncBlock parks the FIRST linked-plan sync at the hook boundary and
// lets the test release it. Later syncs pass straight through: no test here
// needs a second one to block, and a re-arming hook would park a later edit's
// extra sync on itself with the test goroutine already past its release.
type oneShotSyncBlock struct {
	reached     chan struct{}
	resume      chan struct{}
	armed       atomic.Bool
	releaseOnce sync.Once
}

func newOneShotSyncBlock() *oneShotSyncBlock {
	block := &oneShotSyncBlock{reached: make(chan struct{}), resume: make(chan struct{})}
	block.armed.Store(true)
	return block
}

func (b *oneShotSyncBlock) hook(planID, phaseID string) {
	if !b.armed.CompareAndSwap(true, false) {
		return
	}
	close(b.reached)
	<-b.resume
}

func (b *oneShotSyncBlock) waitReached(t *testing.T) {
	t.Helper()
	select {
	case <-b.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the linked-plan sync boundary")
	}
}

// release is idempotent so every test can defer it. A t.Fatalf before the
// release would otherwise strand the parked goroutine inside store.Close.
func (b *oneShotSyncBlock) release() {
	b.releaseOnce.Do(func() { close(b.resume) })
}

// syncOutcome carries what the parked call returned once it is released.
type syncOutcome struct {
	record FlowRecord
	err    error
}

// advancingClock returns a Now function that moves forward on every read. The
// stale-compensation guard compares phase timestamps, so a frozen clock would
// make half of it vacuous.
func advancingClock(start time.Time, step time.Duration) func() time.Time {
	var mu sync.Mutex
	current := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current = current.Add(step)
		return current.UTC()
	}
}

// newPostCommitSeamStore builds a Flow with a linked plan id beside a second
// Flow with no plan at all, so writes to the unrelated Flow can be timed against
// a parked sync on the linked one. Both live in the same database, which is what
// makes the unrelated write meaningful: the SQLite writer is database-wide.
func newPostCommitSeamStore(t *testing.T, syncer planPhaseSyncer, opts StoreOptions) (*Store, FlowRecord, FlowRecord) {
	t.Helper()
	root := t.TempDir()
	opts.Root = root
	if opts.LockTimeout <= 0 {
		opts.LockTimeout = 250 * time.Millisecond
	}
	store, err := NewStore(opts)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.planSync = syncer

	linked, err := store.Create(FlowRecord{
		Title:        "Linked",
		Instructions: "Complete a plan-linked phase.",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create(linked) error = %v", err)
	}
	linked, err = store.SetStartMetadata(StartMetadataUpdate{FlowID: linked.FlowID, PlanID: "seam-plan"})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	unrelated, err := store.Create(FlowRecord{
		Title:        "Unrelated",
		Instructions: "Never touches a plan.",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create(unrelated) error = %v", err)
	}
	return store, linked, unrelated
}

func phaseFromStore(t *testing.T, store *Store, flowID, phaseID string) FlowPhase {
	t.Helper()
	record, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	index := phaseIndexByID(record.Phases, phaseID)
	if index < 0 {
		t.Fatalf("phase %q missing from %#v", phaseID, record.Phases)
	}
	return record.Phases[index]
}

// TestSetPhaseCommitsBeforeTheLinkedPlanSyncRuns pins both halves of the move:
// the phase change is already durable and readable when the sync starts, and no
// Flow writer is held across it, so an unrelated Flow stays writable well inside
// the store's own lock budget.
func TestSetPhaseCommitsBeforeTheLinkedPlanSyncRuns(t *testing.T) {
	const lockTimeout = 250 * time.Millisecond
	syncer := &fakePlanSyncer{}
	store, linked, unrelated := newPostCommitSeamStore(t, syncer, StoreOptions{LockTimeout: lockTimeout})
	block := newOneShotSyncBlock()
	store.beforeLinkedPlanPhaseSync = block.hook

	results := make(chan syncOutcome, 1)
	finished := make(chan struct{})
	defer func() { <-finished }()
	defer block.release()
	go func() {
		defer close(finished)
		record, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: "plan", Status: PhaseCompleted})
		results <- syncOutcome{record: record, err: err}
	}()
	block.waitReached(t)

	if got := phaseFromStore(t, store, linked.FlowID, "plan").Status; got != PhaseCompleted {
		t.Fatalf("plan phase status during the sync = %q, want the committed %q", got, PhaseCompleted)
	}
	start := time.Now()
	_, err := store.SetAutoMode(AutoModeUpdate{FlowID: unrelated.FlowID, Enabled: false})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unrelated flow write during a parked plan sync error = %v (after %s)", err, elapsed)
	}
	if elapsed >= lockTimeout {
		t.Fatalf("unrelated flow write took %s, want well under the %s writer budget", elapsed, lockTimeout)
	}

	block.release()
	if got := <-results; got.err != nil {
		t.Fatalf("SetPhase() error = %v", got.err)
	}
}

// TestSetPhaseCompensatesAfterAPostCommitSyncFailure pins the compensation now
// that it lands in a second update rather than in the committing one.
func TestSetPhaseCompensatesAfterAPostCommitSyncFailure(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, linked, _ := newPostCommitSeamStore(t, syncer, StoreOptions{})
	block := newOneShotSyncBlock()
	store.beforeLinkedPlanPhaseSync = block.hook

	results := make(chan syncOutcome, 1)
	finished := make(chan struct{})
	defer func() { <-finished }()
	defer block.release()
	go func() {
		defer close(finished)
		record, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: "plan", Status: PhaseCompleted})
		results <- syncOutcome{record: record, err: err}
	}()
	block.waitReached(t)
	block.release()

	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase: plan write exploded") {
		t.Fatalf("SetPhase() error = %v, want the sync failure", got.err)
	}
	if got.record.FlowID != "" {
		t.Fatalf("SetPhase() record = %+v, want the zero record beside the error", got.record)
	}
	assertPhaseNeedsAttention(t, store, linked.FlowID, "plan")
}

// TestMarkManualMergeCommitsBeforeTheLinkedPlanSyncRuns is the merge-side twin
// of the SetPhase test above: merged state is readable while the sync is parked,
// and a released failure rolls the merge back through the second update.
func TestMarkManualMergeCommitsBeforeTheLinkedPlanSyncRuns(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, record := newMergeReadySeamFlow(t, syncer)
	previousPRStatus := record.PR.Status
	block := newOneShotSyncBlock()
	store.beforeLinkedPlanPhaseSync = block.hook

	results := make(chan syncOutcome, 1)
	finished := make(chan struct{})
	defer func() { <-finished }()
	defer block.release()
	go func() {
		defer close(finished)
		merged, err := store.MarkManualMerge(ManualMergeUpdate{
			FlowID:   record.FlowID,
			PRNumber: record.PR.Number,
			PRURL:    record.PR.URL,
			Commit:   "deadbeef",
			MergedAt: time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC),
		})
		results <- syncOutcome{record: merged, err: err}
	}()
	block.waitReached(t)

	during, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if during.PR.Status != MergeMerged || during.Merge.Commit != "deadbeef" {
		t.Fatalf("record during the sync = PR %q / merge %+v, want the committed merge", during.PR.Status, during.Merge)
	}
	if during.Status != StatusMerged {
		t.Fatalf("flow status during the sync = %q, want %q", during.Status, StatusMerged)
	}
	if got := phaseFromStore(t, store, record.FlowID, "merge").Status; got != PhaseCompleted {
		t.Fatalf("merge phase during the sync = %q, want %q", got, PhaseCompleted)
	}

	block.release()
	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase") {
		t.Fatalf("MarkManualMerge() error = %v, want the sync failure", got.err)
	}
	if got.record.FlowID != record.FlowID {
		t.Fatalf("MarkManualMerge() record = %+v, want the compensated record beside the error", got.record)
	}
	if got.record.PR.Status != previousPRStatus {
		t.Fatalf("PR status = %q, want the pre-merge %q restored", got.record.PR.Status, previousPRStatus)
	}
	if got.record.Merge.Status != MergePending {
		t.Fatalf("merge status = %q, want %q", got.record.Merge.Status, MergePending)
	}
	assertPhaseNeedsAttention(t, store, record.FlowID, "merge")
}

// TestSetPhaseSkipsCompensationWhenTheCommittedPhaseChanged pins the guard on
// the second update. A resume launch-id append lands on the same phase while the
// sync is parked: it keeps the phase completed and stamps a new UpdatedAt, so
// status alone would not catch it. Compensating anyway would revert the append.
func TestSetPhaseSkipsCompensationWhenTheCommittedPhaseChanged(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, linked, _ := newPostCommitSeamStore(t, syncer, StoreOptions{
		Now: advancingClock(time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC), time.Millisecond),
	})
	block := newOneShotSyncBlock()
	store.beforeLinkedPlanPhaseSync = block.hook

	results := make(chan syncOutcome, 1)
	finished := make(chan struct{})
	defer func() { <-finished }()
	defer block.release()
	go func() {
		defer close(finished)
		record, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: "plan", Status: PhaseCompleted})
		results <- syncOutcome{record: record, err: err}
	}()
	block.waitReached(t)

	if _, err := store.AddPhaseLaunchID(PhaseLaunchUpdate{
		FlowID:   linked.FlowID,
		PhaseID:  "plan",
		LaunchID: "resume-launch",
		Resume:   true,
	}); err != nil {
		t.Fatalf("AddPhaseLaunchID(resume) error = %v", err)
	}

	block.release()
	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase") {
		t.Fatalf("SetPhase() error = %v, want the sync failure returned even when compensation is skipped", got.err)
	}
	phase := phaseFromStore(t, store, linked.FlowID, "plan")
	if phase.Status != PhaseCompleted {
		t.Fatalf("plan phase status = %q, want the concurrent writer's %q preserved", phase.Status, PhaseCompleted)
	}
	if strings.Contains(phase.Notes, "Linked plan phase sync failed") {
		t.Fatalf("plan phase notes = %q, want no compensation note over a concurrent write", phase.Notes)
	}
	found := false
	for _, launchID := range phase.LaunchIDs {
		if launchID == "resume-launch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan phase launch ids = %v, want the concurrent resume append preserved", phase.LaunchIDs)
	}
}

// TestMarkManualMergeRetryResyncsTheLinkedPlan pins the one deliberate behavior
// change: an already-merged retry now re-runs the idempotent plan write, which
// is the recovery path for every window the post-commit ordering opens. The
// Flow semantics of the no-op retry are unchanged.
func TestMarkManualMergeRetryResyncsTheLinkedPlan(t *testing.T) {
	syncer := &fakePlanSyncer{}
	store, record := newMergeReadySeamFlow(t, syncer)
	update := ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: record.PR.Number,
		PRURL:    record.PR.URL,
		Commit:   "deadbeef",
		MergedAt: time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC),
	}
	merged, err := store.MarkManualMerge(update)
	if err != nil {
		t.Fatalf("MarkManualMerge() error = %v", err)
	}
	mergedPhase := phaseFromStore(t, store, record.FlowID, "merge")
	// A delta, not an absolute count: newMergeReadySeamFlow plus the merge above
	// have already recorded calls on the shared fake.
	before := len(syncer.calls)

	retried, err := store.MarkManualMerge(update)
	if err != nil {
		t.Fatalf("MarkManualMerge(retry) error = %v", err)
	}
	if got := len(syncer.calls) - before; got != 1 {
		t.Fatalf("plan sync calls on retry = %d, want exactly 1 idempotent re-sync", got)
	}
	if last := syncer.calls[len(syncer.calls)-1]; last != "seam-plan/merge" {
		t.Fatalf("retry sync boundary = %q, want seam-plan/merge", last)
	}
	if retried.FlowID != merged.FlowID || retried.PR.Status != MergeMerged || !mergeEqual(retried.Merge, merged.Merge) {
		t.Fatalf("retry record = %+v, want the unchanged merged record", retried)
	}
	if got := phaseFromStore(t, store, record.FlowID, "merge"); !got.UpdatedAt.Equal(mergedPhase.UpdatedAt) {
		t.Fatalf("merge phase UpdatedAt = %s, want the no-op retry to leave %s alone", got.UpdatedAt, mergedPhase.UpdatedAt)
	}
}

// TestPostCommitSyncFailureResetsAnAutoLaunchedSuccessor makes the accepted
// observability window explicit. During the window the successor is legitimately
// ready, so the TUI's auto-advance drain can launch it; the compensation then
// demotes the predecessor and readiness resets the just-launched successor to
// pending with its launch id still attached. This test exists to catch that
// changing silently, not to assert it is desirable.
func TestPostCommitSyncFailureResetsAnAutoLaunchedSuccessor(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, linked, _ := newPostCommitSeamStore(t, syncer, StoreOptions{
		Now: advancingClock(time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC), time.Millisecond),
	})
	block := newOneShotSyncBlock()
	store.beforeLinkedPlanPhaseSync = block.hook

	results := make(chan syncOutcome, 1)
	finished := make(chan struct{})
	defer func() { <-finished }()
	defer block.release()
	go func() {
		defer close(finished)
		record, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: "plan", Status: PhaseCompleted})
		results <- syncOutcome{record: record, err: err}
	}()
	block.waitReached(t)

	// The path the drain actually takes, so the store-side auto-launch
	// validation runs against the window state rather than being bypassed.
	if _, err := store.AddPhaseLaunchID(PhaseLaunchUpdate{
		FlowID:     linked.FlowID,
		PhaseID:    "plan-review",
		LaunchID:   "auto-launch",
		AutoLaunch: true,
	}); err != nil {
		t.Fatalf("AddPhaseLaunchID(auto launch during the window) error = %v", err)
	}
	if got := phaseFromStore(t, store, linked.FlowID, "plan-review").Status; got != PhaseRunning {
		t.Fatalf("successor status during the window = %q, want %q", got, PhaseRunning)
	}

	block.release()
	if got := <-results; got.err == nil {
		t.Fatal("SetPhase() error = nil, want the sync failure")
	}

	predecessor := phaseFromStore(t, store, linked.FlowID, "plan")
	if predecessor.Status != PhaseNeedsAttention {
		t.Fatalf("predecessor status = %q, want %q", predecessor.Status, PhaseNeedsAttention)
	}
	successor := phaseFromStore(t, store, linked.FlowID, "plan-review")
	if successor.Status != PhasePending {
		t.Fatalf("successor status = %q, want the compensation to reset it to %q", successor.Status, PhasePending)
	}
	if len(successor.LaunchIDs) != 1 || successor.LaunchIDs[0] != "auto-launch" {
		t.Fatalf("successor launch ids = %v, want the launched attempt retained", successor.LaunchIDs)
	}
}
