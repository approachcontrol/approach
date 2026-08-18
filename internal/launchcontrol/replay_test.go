package launchcontrol

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/flowlease"
)

// spool writes a request into a launch log the way the CLI's spool path does.
func spool(t *testing.T, root string, req Request) *Log {
	t.Helper()
	log, err := OpenLog(root, req.LaunchID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(mustEnvelope(t, req, WrittenBySpool)); err != nil {
		t.Fatal(err)
	}
	return log
}

// launchWithBaseline records a launch through the RecordBaseline decorator so
// the launch directory has baseline.json, as a real launch does.
func launchWithBaseline(t *testing.T, store *flowstore.Store, root, flowID, phaseID, launchID string) {
	t.Helper()
	next := RecordBaseline(root, store.AddPhaseLaunchID)
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: flowID, PhaseID: phaseID, LaunchID: launchID}); err != nil {
		t.Fatalf("AddPhaseLaunchID: %v", err)
	}
	log, _ := OpenLog(root, launchID)
	if err := log.WriteLaunch(LaunchInfo{FlowID: flowID, PhaseID: phaseID, Kind: "phase"}); err != nil {
		t.Fatal(err)
	}
}

func TestReplayCase1MarksAppliedWhenCommitLandedButMarkerDidNot(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Case One")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	c := newTestController(t, store, root)
	original := applyMarkerHook
	applyMarkerHook = func() error { return errors.New("crash before applied.json") }
	req := mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "landed"})
	log, _ := OpenLog(root, "launch-1")
	unlock, _ := log.Lock(time.Second)
	_, err := ApplyLogged(store, log, req, mustEnvelope(t, req, WrittenByController), time.Now())
	unlock()
	applyMarkerHook = original
	if err == nil || !strings.Contains(err.Error(), "crash before applied.json") {
		t.Fatalf("ApplyLogged error = %v", err)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseCompleted {
		t.Fatal("commit did not land")
	}
	if pending, _ := log.Pending(); len(pending) != 1 {
		t.Fatalf("pending = %#v, want the unmarked request", pending)
	}
	report := c.Sweep()
	if report.Replayed != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseCompleted || phase.Summary != "landed" {
		t.Fatalf("phase after replay = %#v", phase)
	}
	applied, ok, _ := log.Applied()
	if !ok || applied.AppliedSeq != 1 || applied.Status != flowstore.PhaseCompleted || applied.Result != ResultApplied {
		t.Fatalf("applied = %#v", applied)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("pending after replay = %#v", pending)
	}
}

func TestReplayAppliesSpooledBatchInOrderRegardlessOfObservedTimestamps(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Batch")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, req := range []Request{
		mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "launch-1", PhaseSetPayload{Status: flowstore.PhaseRunning, Notes: "still going"}),
		mustRequest(t, VerbPlanSet, created.FlowID, "plan", "launch-1", PlanSetPayload{PlanID: "plan-9"}),
		mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "final"}),
	} {
		log, _ := OpenLog(root, "launch-1")
		env := mustEnvelope(t, req, WrittenBySpool)
		env.Observed = ObservedPhase{Status: flowstore.PhaseRunning, UpdatedAt: stale}
		if _, err := log.Append(env); err != nil {
			t.Fatal(err)
		}
	}
	// A session attach between the result and the restart bumps UpdatedAt.
	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{FlowID: created.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "claude", SessionID: "s1", LaunchID: "launch-1"}}); err != nil {
		t.Fatal(err)
	}
	c := newTestController(t, store, root)
	report := c.Sweep()
	// plan.set for an unknown plan is refused (request_invalid) — the other two land.
	if report.Replayed != 2 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseCompleted || phase.Summary != "final" || phase.Notes != "still going" {
		t.Fatalf("phase = %#v", phase)
	}
	log, _ := OpenLog(root, "launch-1")
	rejected, _, _ := log.Rejected()
	if len(rejected.Batches) != 1 || rejected.Batches[0].Reason != ReasonRequestInvalid || len(rejected.Batches[0].Requests) != 1 {
		t.Fatalf("rejected = %#v", rejected)
	}
	applied, _, _ := log.Applied()
	if applied.AppliedSeq != 3 || applied.Status != flowstore.PhaseCompleted {
		t.Fatalf("applied = %#v", applied)
	}
}

func TestReplayRejectsBatchWhenPhaseRelaunchedUnderNewerLaunch(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Newer Launch")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "old"}))
	// The operator reset and relaunched the phase before the controller returned.
	if _, err := store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: created.FlowID, PhaseID: "plan"}); err != nil {
		t.Fatal(err)
	}
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-2")
	c := newTestController(t, store, root)
	report := c.Sweep()
	if report.Replayed != 0 || report.Reconciled != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseRunning || phase.Summary != "" || flowstore.LatestPhaseLaunchID(phase) != "launch-2" {
		t.Fatalf("live phase touched: %#v", phase)
	}
	log, _ := OpenLog(root, "launch-1")
	rejected, ok, _ := log.Rejected()
	if !ok || len(rejected.Batches) != 1 || rejected.Batches[0].Reason != ReasonPhaseResultStale || rejected.Batches[0].IntendedStatus != flowstore.PhaseCompleted || rejected.Batches[0].ObservedStatus != flowstore.PhaseRunning {
		t.Fatalf("rejected = %#v", rejected)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %#v", pending)
	}
	if notice, ok := log.Notice(); !ok || !strings.Contains(notice, ReasonPhaseResultStale) {
		t.Fatalf("notice = %q %v", notice, ok)
	}
	if !strings.Contains(strings.Join(report.Notices, "\n"), "launch-2") {
		t.Fatalf("notices = %v", report.Notices)
	}
}

func TestReplayCase3DemotesRunningPhaseOwnedByThisLaunch(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Stale")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	// The phase moved (needs_attention, then restarted to running by the
	// operator) after the launch's baseline: the spooled completion is stale.
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Notes: "manual"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseRunning, Notes: "manual restart"}); err != nil {
		t.Fatal(err)
	}
	log, _ := OpenLog(root, "launch-1")
	if err := log.WriteApplied(AppliedState{AppliedSeq: 0, Status: flowstore.PhaseNeedsAttention, Result: ResultApplied}); err != nil {
		t.Fatal(err)
	}
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "late"}))
	c := newTestController(t, store, root)
	report := c.Sweep()
	if report.Reconciled != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseNeedsAttention || phase.Outcome != ReasonPhaseResultStale {
		t.Fatalf("phase = %#v", phase)
	}
	for _, want := range []string{
		"phase_result_stale: launch launch-1",
		"intended completed, observed running",
		`approach flow phase set --flow-id ` + created.FlowID + ` --phase-id plan --status running --notes "phase_result_stale"`,
	} {
		if !strings.Contains(phase.Notes, want) {
			t.Fatalf("notes = %q, missing %q", phase.Notes, want)
		}
	}
	applied, _, _ := log.Applied()
	if applied.Status != flowstore.PhaseNeedsAttention || applied.Result != ResultReconciled || applied.AppliedSeq != 1 {
		t.Fatalf("applied = %#v", applied)
	}
}

func TestReplayCase3OnPlanReviewKindWritesBlockedAndSucceeds(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Review Stale")
	completePhase(t, store, created.FlowID, "plan", "")
	launchWithBaseline(t, store, root, created.FlowID, "plan-review", "launch-1")
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseBlocked, Outcome: flowstore.OutcomeBlocked, Notes: "manual"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseRunning, Notes: "manual restart"}); err != nil {
		t.Fatal(err)
	}
	log, _ := OpenLog(root, "launch-1")
	_ = log.WriteApplied(AppliedState{AppliedSeq: 0, Status: flowstore.PhaseBlocked, Result: ResultApplied})
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan-review", "launch-1", PhaseActionPayload{Outcome: flowstore.OutcomeApproved}))
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan-review")
	if phase.Status != flowstore.PhaseBlocked || phase.Outcome != flowstore.OutcomeBlocked || !strings.HasPrefix(phase.Notes, ReasonPhaseResultStale+":") {
		t.Fatalf("plan-review phase = %#v", phase)
	}
	// blocked -> running is legal and clears the outcome.
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan-review", Status: flowstore.PhaseRunning, Notes: ReasonPhaseResultStale}); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if got := phaseOf(t, store, created.FlowID, "plan-review"); got.Status != flowstore.PhaseRunning || got.Outcome != "" {
		t.Fatalf("after recovery = %#v", got)
	}
}

func TestReplayCase3OnAutoreviewKindAcceptsLiteralReasonOutcome(t *testing.T) {
	store, root := newTestStore(t)
	created, err := store.Create(flowstore.FlowRecord{Title: "Autoreview Stale", Instructions: "x", RepoPath: root + "/repo", Branch: "flow/x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, phaseID := range []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation"} {
		outcome := ""
		if phaseID == "plan-review" {
			outcome = flowstore.OutcomeApproved
		}
		completePhase(t, store, created.FlowID, phaseID, outcome)
	}
	if _, err := store.SetPR(flowstore.PRUpdate{FlowID: created.FlowID, Provider: "github", Number: 1, URL: "https://github.com/o/r/pull/1", HeadBranch: "flow/x", BaseBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	launchWithBaseline(t, store, root, created.FlowID, "autoreview", "launch-1")
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "autoreview", Status: flowstore.PhaseNeedsAttention, Notes: "manual"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "autoreview", Status: flowstore.PhaseRunning, Notes: "manual restart"}); err != nil {
		t.Fatal(err)
	}
	log, _ := OpenLog(root, "launch-1")
	_ = log.WriteApplied(AppliedState{AppliedSeq: 0, Status: flowstore.PhaseNeedsAttention, Result: ResultApplied})
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "autoreview", "launch-1", PhaseActionPayload{}))
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	if phase := phaseOf(t, store, created.FlowID, "autoreview"); phase.Status != flowstore.PhaseNeedsAttention || phase.Outcome != ReasonPhaseResultStale {
		t.Fatalf("autoreview phase = %#v", phase)
	}
}

func TestReplayRejectionAgainstTerminalPhaseWritesNothing(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Terminal")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	// The phase was completed by hand with a different summary after the launch.
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseCompleted, Summary: "by hand"}); err != nil {
		t.Fatal(err)
	}
	spool(t, root, mustRequest(t, VerbPhaseNeedsAttention, created.FlowID, "plan", "launch-1", PhaseActionPayload{Notes: "late"}))
	before := phaseOf(t, store, created.FlowID, "plan")
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Reconciled != 0 || report.Replayed != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	after := phaseOf(t, store, created.FlowID, "plan")
	if after.Status != before.Status || after.Summary != before.Summary || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("terminal phase written: %#v -> %#v", before, after)
	}
	log, _ := OpenLog(root, "launch-1")
	if rejected, ok, _ := log.Rejected(); !ok || rejected.Batches[0].Reason != ReasonPhaseResultStale {
		t.Fatalf("rejected = %#v", rejected)
	}
}

func TestReplayWithoutBaselineRejectsAndNeverDemotes(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "No Baseline")
	launchPhase(t, store, created.FlowID, "plan", "launch-1")
	log, _ := OpenLog(root, "launch-1")
	_ = log.WriteLaunch(LaunchInfo{FlowID: created.FlowID, PhaseID: "plan"})
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{}))
	c := newTestController(t, store, root)
	// No pending target reached, no baseline: nothing may be applied or demoted.
	// (A completion that already landed would be case 1 before the baseline check;
	// here the phase is still running, so case 1 does not fire.)
	if report := c.Sweep(); report.Replayed != 0 || report.Reconciled != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("phase changed without a baseline")
	}
	rejected, ok, _ := log.Rejected()
	if !ok || rejected.Batches[0].Reason != ReasonBaselineMissing {
		t.Fatalf("rejected = %#v", rejected)
	}
}

func TestReplayOfResumeLaunchWithCompletedBaselineReplaysNothing(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Resume")
	completePhase(t, store, created.FlowID, "plan", "")
	next := RecordBaseline(root, store.AddPhaseLaunchID)
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-r", Resume: true}); err != nil {
		t.Fatal(err)
	}
	log, _ := OpenLog(root, "launch-r")
	_ = log.WriteLaunch(LaunchInfo{FlowID: created.FlowID, PhaseID: "plan", Kind: "resume"})
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Replayed != 0 || report.Reconciled != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	if _, ok, _ := log.Applied(); ok {
		t.Fatal("applied.json written with nothing pending")
	}
}

func TestReplayNoOpFirstRequestAgainstNewerLaunchAppliesNothing(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Coincidence")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	// launch-1 spooled "running" (a no-op) then "completed"; the phase has since
	// been relaunched under launch-2 and is coincidentally running.
	spool(t, root, mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "launch-1", PhaseSetPayload{Status: flowstore.PhaseRunning}))
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{}))
	if _, err := store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: created.FlowID, PhaseID: "plan"}); err != nil {
		t.Fatal(err)
	}
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-2")
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Replayed != 0 || report.Reconciled != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("newer launch's phase changed")
	}
}

func TestReplayCase2RefusalIsRequestInvalidNotStaleness(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Invalid")
	completePhase(t, store, created.FlowID, "plan", "")
	launchWithBaseline(t, store, root, created.FlowID, "plan-review", "launch-1")
	// approved_with_concerns without notes is refused by validatePhaseUpdate.
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan-review", "launch-1", PhaseActionPayload{Outcome: flowstore.OutcomeApprovedWithConcerns}))
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Reconciled != 0 || report.Replayed != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	if phaseOf(t, store, created.FlowID, "plan-review").Status != flowstore.PhaseRunning {
		t.Fatal("phase demoted for a client error")
	}
	log, _ := OpenLog(root, "launch-1")
	rejected, _, _ := log.Rejected()
	if len(rejected.Batches) != 1 || rejected.Batches[0].Reason != ReasonRequestInvalid || !strings.Contains(rejected.Batches[0].Error, "requires notes") {
		t.Fatalf("rejected = %#v", rejected)
	}
	applied, _, _ := log.Applied()
	if applied.AppliedSeq != 1 || applied.Result != ResultRejected {
		t.Fatalf("applied = %#v", applied)
	}
	// The alive agent's next valid request still lands.
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan-review", "launch-1", PhaseActionPayload{Outcome: flowstore.OutcomeApproved}))
	if report := c.Sweep(); report.Replayed != 1 {
		t.Fatalf("second sweep = %#v", report)
	}
	if phaseOf(t, store, created.FlowID, "plan-review").Status != flowstore.PhaseCompleted {
		t.Fatal("valid follow-up did not land")
	}
}

func TestReplayWaitsForAnotherProcessHoldingTheSequenceLock(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Locked")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{}))
	holder, _ := OpenLog(root, "launch-1")
	unlock, err := holder.Lock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := newTestController(t, store, root)
	done := make(chan Outcome, 1)
	go func() {
		outcome, _ := c.Reconcile(created.FlowID, "plan", "launch-1", ExitEvidence{Source: SourceTerminalExit, CodeKnown: true})
		done <- outcome
	}()
	select {
	case <-done:
		t.Fatal("reconcile did not wait for the lock holder")
	case <-time.After(300 * time.Millisecond):
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("phase changed while the lock was held")
	}
	unlock()
	select {
	case outcome := <-done:
		if outcome.Replayed != 1 || outcome.Action != ActionNone {
			t.Fatalf("outcome = %#v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile never finished after the lock was released")
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseCompleted {
		t.Fatal("spooled completion did not land once the lock was free")
	}
}

func heldLease(string, string) (flowlease.LeaseState, error) { return flowlease.Held, nil }
func freeLease(string, string) (flowlease.LeaseState, error) { return flowlease.Free, nil }
