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
// the second update. A concurrent re-completion lands on the same phase while
// the sync is parked and rewrites the summary: the phase is still completed, so
// status alone would not catch it, and demoting it would attribute this
// caller's sync failure to a completion it did not make. The concurrent call
// takes SetPhase's own completed-retry path, so it leaves no marker of its own
// and the assertions below see only what the guard did.
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

	if _, err := store.SetPhase(PhaseUpdate{
		FlowID:  linked.FlowID,
		PhaseID: "plan",
		Status:  PhaseCompleted,
		Summary: "Re-completed by another writer.",
	}); err != nil {
		t.Fatalf("SetPhase(concurrent re-completion) error = %v", err)
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
		t.Fatalf("plan phase notes = %q, want no compensation note over a concurrent completion", phase.Notes)
	}
	if phase.Summary != "Re-completed by another writer." {
		t.Fatalf("plan phase summary = %q, want the concurrent completion preserved", phase.Summary)
	}
}

// TestSetPhaseCompensatesWhenOnlyPhaseMetadataChanged is the other side of the
// guard, and the reason it cannot key on UpdatedAt alone. AttachSession bumps a
// completed phase's UpdatedAt without touching the completion, and it is driven
// by agent-session ingest — which fires at almost exactly the moment the
// completing write is parked on the plan lock. Rejecting on that would turn a
// compensable sync failure into a silently stale plan with no marker anywhere.
func TestSetPhaseCompensatesWhenOnlyPhaseMetadataChanged(t *testing.T) {
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

	if _, err := store.AttachSession(SessionAttachUpdate{
		FlowID:  linked.FlowID,
		PhaseID: "plan",
		Session: Session{Provider: "claude", SessionID: "sess-1", LaunchID: "launch-1", Status: "running"},
	}); err != nil {
		t.Fatalf("AttachSession(during the window) error = %v", err)
	}
	before := phaseFromStore(t, store, linked.FlowID, "plan")
	if !before.UpdatedAt.After(time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)) || len(before.Sessions) != 1 {
		t.Fatalf("phase after the metadata write = %+v, want a bumped UpdatedAt and the attached session", before)
	}

	block.release()
	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase") {
		t.Fatalf("SetPhase() error = %v, want the sync failure", got.err)
	}
	assertPhaseNeedsAttention(t, store, linked.FlowID, "plan")
	phase := phaseFromStore(t, store, linked.FlowID, "plan")
	if len(phase.Sessions) != 1 || phase.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("plan phase sessions = %+v, want the concurrent attach preserved through the compensation", phase.Sessions)
	}
}

// TestSetPhaseCompensatesWhenALaunchIDWasAppended is the same guarantee for the
// other metadata writer that lands on completed rows: a resume launch-id
// append. The compensation is computed from the freshly read row, so it demotes
// the phase without discarding the append.
func TestSetPhaseCompensatesWhenALaunchIDWasAppended(t *testing.T) {
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
	if got := <-results; got.err == nil {
		t.Fatal("SetPhase() error = nil, want the sync failure")
	}
	assertPhaseNeedsAttention(t, store, linked.FlowID, "plan")
	phase := phaseFromStore(t, store, linked.FlowID, "plan")
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

// TestMarkManualMergeSkipsCompensationWhenTheMergeChanged pins the half of the
// merge guard that SetPhase has no equivalent for. The merge phase is untouched
// during the window, so the phase half of the guard still passes; only the
// merge metadata moves. Without the mergeEqual clause the rollback would fire
// and overwrite a concurrent writer's merge with MergePending.
func TestMarkManualMergeSkipsCompensationWhenTheMergeChanged(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, record := newMergeReadySeamFlow(t, syncer)
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

	if _, err := store.SetMerge(MergeUpdate{
		FlowID:   record.FlowID,
		Status:   MergeMerged,
		Commit:   "cafebabe",
		MergedAt: time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("SetMerge(during the window) error = %v", err)
	}

	block.release()
	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase") {
		t.Fatalf("MarkManualMerge() error = %v, want the sync failure returned even when compensation is skipped", got.err)
	}
	// The documented return contract for a guard rejection: still merged, beside
	// the error, because that is the truth of the durable state.
	if got.record.FlowID != record.FlowID || got.record.PR.Status != MergeMerged {
		t.Fatalf("MarkManualMerge() record = %+v, want the still-merged record beside the error", got.record)
	}
	stored, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.PR.Status != MergeMerged {
		t.Fatalf("stored PR status = %q, want no rollback over a concurrent merge write", stored.PR.Status)
	}
	if stored.Merge.Commit != "cafebabe" || stored.Merge.Status == MergePending {
		t.Fatalf("stored merge = %+v, want the concurrent writer's merge preserved", stored.Merge)
	}
	phase := phaseFromStore(t, store, record.FlowID, "merge")
	if phase.Status != PhaseCompleted {
		t.Fatalf("merge phase status = %q, want %q preserved", phase.Status, PhaseCompleted)
	}
	if strings.Contains(phase.Notes, "Linked plan phase sync failed") {
		t.Fatalf("merge phase notes = %q, want no compensation note over a concurrent write", phase.Notes)
	}
}

// TestMarkManualMergeSkipsCompensationWhenThePRStatusChanged pins the remaining
// clause of the merge guard, which mergeEqual does NOT make redundant: SetPR
// replaces record.PR wholesale, including Status, without touching record.Merge
// and without stamping the merge phase's UpdatedAt. So a concurrent SetPR clears
// both other clauses, and only this one stops the rollback from overwriting the
// PR status that writer just set.
func TestMarkManualMergeSkipsCompensationWhenThePRStatusChanged(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, record := newMergeReadySeamFlow(t, syncer)
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

	if _, err := store.SetPR(PRUpdate{
		FlowID: record.FlowID, Provider: "github", Number: record.PR.Number,
		URL: record.PR.URL, HeadBranch: "feat/seam", BaseBranch: "main", Status: "closed",
	}); err != nil {
		t.Fatalf("SetPR(during the window) error = %v", err)
	}

	block.release()
	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase") {
		t.Fatalf("MarkManualMerge() error = %v, want the sync failure", got.err)
	}
	stored, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.PR.Status != "closed" {
		t.Fatalf("stored PR status = %q, want the concurrent writer's %q preserved", stored.PR.Status, "closed")
	}
	if stored.Merge.Commit != "deadbeef" {
		t.Fatalf("stored merge = %+v, want no rollback over a concurrent PR write", stored.Merge)
	}
	phase := phaseFromStore(t, store, record.FlowID, "merge")
	if phase.Status != PhaseCompleted {
		t.Fatalf("merge phase status = %q, want %q preserved", phase.Status, PhaseCompleted)
	}
	if strings.Contains(phase.Notes, "Linked plan phase sync failed") {
		t.Fatalf("merge phase notes = %q, want no compensation note over a concurrent write", phase.Notes)
	}
}

// TestCommittedPhaseRowSurvivesDuplicateRowCollapse pins why the capture is
// taken after collapseDuplicatePhaseRows. On a legacy record with duplicate rows
// for one logical phase, collapse fills an empty survivor Summary from a row it
// merges away. Capturing the pre-collapse local would leave the guard comparing
// against a Summary that was never stored, so any later metadata write would
// make it reject this caller's own completion.
func TestCommittedPhaseRowSurvivesDuplicateRowCollapse(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	local := FlowPhase{PhaseID: "plan", Status: PhaseCompleted, UpdatedAt: now}
	record := FlowRecord{Phases: []FlowPhase{
		local,
		{PhaseID: "Plan", Status: PhaseCompleted, Summary: "carried by the duplicate row", UpdatedAt: now},
	}}
	record.Phases = collapseDuplicatePhaseRows(record.Phases, 0)

	committed := committedPhaseRow(record, local)
	if committed.Summary != "carried by the duplicate row" {
		t.Fatalf("committed phase summary = %q, want the collapsed survivor's", committed.Summary)
	}

	// A metadata write lands afterwards: new stamp, same completion.
	current := committed
	current.UpdatedAt = now.Add(time.Millisecond)
	if !phaseStillHoldsCommittedCompletion(current, committed) {
		t.Fatal("guard rejected the row it committed after a metadata-only write")
	}
	if phaseStillHoldsCommittedCompletion(current, local) {
		t.Fatal("guard accepted the pre-collapse local, so this test would not catch the regression")
	}
}

// TestCommittedCompletionGuardRejectsASameStampedRewrite pins that an equal
// UpdatedAt is never on its own proof of identity. A timestamp is not a unique
// write token: clock resolution, a clock adjustment, or an injected
// StoreOptions.Now that does not advance can all give a later, different
// completion the same stamp as this caller's. Accepting on the stamp alone would
// let the compensation demote that writer's completion, which is exactly the
// clobber the guard exists to prevent.
func TestCommittedCompletionGuardRejectsASameStampedRewrite(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	committed := FlowPhase{
		PhaseID:   "plan",
		Status:    PhaseCompleted,
		Outcome:   "approved",
		Summary:   "Committed by this caller.",
		UpdatedAt: now,
	}
	if !phaseStillHoldsCommittedCompletion(committed, committed) {
		t.Fatal("guard rejected the untouched row it committed")
	}
	for name, mutate := range map[string]func(FlowPhase) FlowPhase{
		"outcome": func(phase FlowPhase) FlowPhase { phase.Outcome = "changes-requested"; return phase },
		"summary": func(phase FlowPhase) FlowPhase { phase.Summary = "Re-completed by another writer."; return phase },
		"notes":   func(phase FlowPhase) FlowPhase { phase.Notes = "Re-completed by another writer."; return phase },
	} {
		t.Run(name, func(t *testing.T) {
			current := mutate(committed)
			if !current.UpdatedAt.Equal(committed.UpdatedAt) {
				t.Fatalf("test setup changed UpdatedAt, which is the condition under test")
			}
			if phaseStillHoldsCommittedCompletion(current, committed) {
				t.Fatalf("guard accepted a different completion sharing this caller's stamp: %+v", current)
			}
		})
	}
}

// TestMarkManualMergeSkipsCompensationWhenThePRIdentityChanged pins the rest of
// the PR clause. SetPR replaces record.PR wholesale, so a concurrent writer can
// point the Flow at a different PR that is itself already merged — same status,
// new number and URL — without touching record.Merge or the merge phase. Keying
// the guard on PR.Status alone would let the rollback stamp this caller's
// pre-merge status onto that writer's PR.
func TestMarkManualMergeSkipsCompensationWhenThePRIdentityChanged(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, record := newMergeReadySeamFlow(t, syncer)
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

	if _, err := store.SetPR(PRUpdate{
		FlowID: record.FlowID, Provider: "github", Number: 99,
		URL: "https://github.com/o/r/pull/99", HeadBranch: "feat/seam", BaseBranch: "main", Status: MergeMerged,
	}); err != nil {
		t.Fatalf("SetPR(during the window) error = %v", err)
	}

	block.release()
	got := <-results
	if got.err == nil || !strings.Contains(got.err.Error(), "sync linked plan phase") {
		t.Fatalf("MarkManualMerge() error = %v, want the sync failure", got.err)
	}
	stored, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if stored.PR.Number != 99 || stored.PR.Status != MergeMerged {
		t.Fatalf("stored PR = %+v, want the concurrent writer's PR untouched", stored.PR)
	}
	if stored.Merge.Commit != "deadbeef" {
		t.Fatalf("stored merge = %+v, want no rollback over a concurrent PR write", stored.Merge)
	}
	phase := phaseFromStore(t, store, record.FlowID, "merge")
	if phase.Status != PhaseCompleted {
		t.Fatalf("merge phase status = %q, want %q preserved", phase.Status, PhaseCompleted)
	}
	if strings.Contains(phase.Notes, "Linked plan phase sync failed") {
		t.Fatalf("merge phase notes = %q, want no compensation note over a concurrent write", phase.Notes)
	}
}

// failSecondUpdateBackend delegates to the real backend but fails every update
// after the first. In both entry points the first update is the committing one
// and the second is the compensation, so this drives the folded-error path
// without any timing dependence.
type failSecondUpdateBackend struct {
	backend
	updates atomic.Int64
	err     error
}

func (b *failSecondUpdateBackend) update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error) {
	if b.updates.Add(1) > 1 {
		return FlowRecord{}, b.err
	}
	return b.backend.update(flowID, mutate)
}

// TestSetPhaseFoldsACompensationFailureIntoTheSyncError pins the one behavior
// change the commit calls out but nothing else covers: when the compensation
// update itself fails, both errors come back in one value with %w on the SYNC
// error, so a compensation failure that happens to be a not-found is NOT
// reported as errors.Is(err, errFlowNotFound).
func TestSetPhaseFoldsACompensationFailureIntoTheSyncError(t *testing.T) {
	syncFailure := errors.New("plan write exploded")
	syncer := &fakePlanSyncer{writeErr: syncFailure}
	store, linked, _ := newPostCommitSeamStore(t, syncer, StoreOptions{})
	store.backend = &failSecondUpdateBackend{backend: store.backend, err: flowNotFoundError(linked.FlowID)}

	got, err := store.SetPhase(PhaseUpdate{FlowID: linked.FlowID, PhaseID: "plan", Status: PhaseCompleted})
	if err == nil {
		t.Fatal("SetPhase() error = nil, want the folded sync and compensation failure")
	}
	if !errors.Is(err, syncFailure) {
		t.Fatalf("SetPhase() error = %v, want the sync failure to stay unwrappable via %%w", err)
	}
	if !strings.Contains(err.Error(), "additionally failed to persist needs_attention state") {
		t.Fatalf("SetPhase() error = %v, want the compensation failure folded in", err)
	}
	// The deliberate consequence of keeping %w on the sync error.
	if errors.Is(err, errFlowNotFound) {
		t.Fatalf("SetPhase() error = %v, want the folded not-found to be unwrappable only as text", err)
	}
	if got.FlowID != "" {
		t.Fatalf("SetPhase() record = %+v, want the zero record beside the folded error", got)
	}
	// The phase change itself committed and stays committed: only the marker is
	// missing, which is the accepted no-marker window.
	if status := phaseFromStore(t, store, linked.FlowID, "plan").Status; status != PhaseCompleted {
		t.Fatalf("plan phase status = %q, want the committed %q with no marker", status, PhaseCompleted)
	}
}

// TestMarkManualMergeFoldsACompensationFailureIntoTheSyncError is the merge-side
// twin. The merge stays durably recorded because the rollback never landed.
func TestMarkManualMergeFoldsACompensationFailureIntoTheSyncError(t *testing.T) {
	syncFailure := errors.New("plan write exploded")
	syncer := &fakePlanSyncer{writeErr: syncFailure}
	store, record := newMergeReadySeamFlow(t, syncer)
	store.backend = &failSecondUpdateBackend{backend: store.backend, err: errors.New("writer unavailable")}

	got, err := store.MarkManualMerge(ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: record.PR.Number,
		PRURL:    record.PR.URL,
		Commit:   "deadbeef",
		MergedAt: time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("MarkManualMerge() error = nil, want the folded sync and compensation failure")
	}
	if !errors.Is(err, syncFailure) {
		t.Fatalf("MarkManualMerge() error = %v, want the sync failure to stay unwrappable via %%w", err)
	}
	if !strings.Contains(err.Error(), "additionally failed to persist needs_attention state") {
		t.Fatalf("MarkManualMerge() error = %v, want the compensation failure folded in", err)
	}
	if got.FlowID != "" {
		t.Fatalf("MarkManualMerge() record = %+v, want the zero record when the compensation itself failed", got)
	}
	stored, readErr := store.Read(record.FlowID)
	if readErr != nil {
		t.Fatalf("Read() error = %v", readErr)
	}
	if stored.PR.Status != MergeMerged || stored.Merge.Commit != "deadbeef" {
		t.Fatalf("stored record = PR %q / merge %+v, want the merge still recorded with no rollback", stored.PR.Status, stored.Merge)
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

// phaseScopedPlanSyncer fails the plan write for ONE phase id and lets every
// other phase through, so a test can park a failing sync on the predecessor
// while a concurrent writer completes the successor for real. It locks because,
// unlike the other post-commit tests, both syncs here run to completion.
type phaseScopedPlanSyncer struct {
	mu        sync.Mutex
	failPhase string
	err       error
	calls     []string
}

func (s *phaseScopedPlanSyncer) open() (planPhaseWriter, error) {
	return phaseScopedPlanWriter{parent: s}, nil
}

type phaseScopedPlanWriter struct{ parent *phaseScopedPlanSyncer }

func (w phaseScopedPlanWriter) markPhaseCompleted(planID, phaseID string) error {
	w.parent.mu.Lock()
	defer w.parent.mu.Unlock()
	w.parent.calls = append(w.parent.calls, planID+"/"+phaseID)
	if phaseID == w.parent.failPhase {
		return w.parent.err
	}
	return nil
}

// TestPostCommitSyncFailureResetsATerminalSuccessor is the sharper edge of the
// same accepted window, and the reason the docs do not limit it to a launched
// successor. A successor that reaches a TERMINAL status inside the window is
// reset too, losing the outcome the concurrent writer recorded, because
// refreshPhaseReadiness resets every downstream row whose gate the demotion
// unsatisfies. That is not special to the compensation — any predecessor
// demotion does it, including an operator restarting a completed phase — but the
// compensation reaches it without an operator, so it is pinned here to catch the
// behavior changing silently rather than to assert it is desirable.
func TestPostCommitSyncFailureResetsATerminalSuccessor(t *testing.T) {
	syncer := &phaseScopedPlanSyncer{failPhase: "plan", err: errors.New("plan write exploded")}
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

	// The successor is legitimately ready in the window, so this completion is a
	// legal write that succeeds and returns nil to its own caller.
	if _, err := store.SetPhase(PhaseUpdate{
		FlowID:  linked.FlowID,
		PhaseID: "plan-review",
		Status:  PhaseCompleted,
		Outcome: OutcomeApproved,
	}); err != nil {
		t.Fatalf("SetPhase(successor during the window) error = %v", err)
	}
	if got := phaseFromStore(t, store, linked.FlowID, "plan-review"); got.Status != PhaseCompleted || got.Outcome != OutcomeApproved {
		t.Fatalf("successor during the window = %+v, want a completed %q", got, OutcomeApproved)
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
	if successor.Status != PhasePending || successor.Outcome != "" {
		t.Fatalf("successor = %+v, want it reset to %q with the outcome cleared", successor, PhasePending)
	}
}
