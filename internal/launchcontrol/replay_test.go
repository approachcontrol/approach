package launchcontrol

import (
	"errors"
	"path/filepath"
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
	if !ok || applied.AppliedSeq != 1 || applied.Status != string(flowstore.PhaseCompleted) || applied.Result != ResultApplied {
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
		mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "launch-1", PhaseSetPayload{Status: string(flowstore.PhaseRunning), Notes: "still going"}),
		mustRequest(t, VerbPlanSet, created.FlowID, "plan", "launch-1", PlanSetPayload{PlanID: "plan-9"}),
		mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "final"}),
	} {
		log, _ := OpenLog(root, "launch-1")
		env := mustEnvelope(t, req, WrittenBySpool)
		env.Observed = ObservedPhase{Status: string(flowstore.PhaseRunning), UpdatedAt: stale}
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
	if applied.AppliedSeq != 3 || applied.Status != string(flowstore.PhaseCompleted) {
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
	if !ok || len(rejected.Batches) != 1 || rejected.Batches[0].Reason != ReasonPhaseResultStale || rejected.Batches[0].IntendedStatus != string(flowstore.PhaseCompleted) || rejected.Batches[0].ObservedStatus != string(flowstore.PhaseRunning) {
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
	if err := log.WriteApplied(AppliedState{AppliedSeq: 0, Status: string(flowstore.PhaseNeedsAttention), Result: ResultApplied}); err != nil {
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
		`approach flow phase recover --flow-id ` + created.FlowID + ` --phase-id plan`,
	} {
		if !strings.Contains(phase.Notes, want) {
			t.Fatalf("notes = %q, missing %q", phase.Notes, want)
		}
	}
	applied, _, _ := log.Applied()
	if applied.Status != string(flowstore.PhaseNeedsAttention) || applied.Result != ResultReconciled || applied.AppliedSeq != 1 {
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
	_ = log.WriteApplied(AppliedState{AppliedSeq: 0, Status: string(flowstore.PhaseBlocked), Result: ResultApplied})
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan-review", "launch-1", PhaseActionPayload{Outcome: flowstore.OutcomeApproved}))
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan-review")
	if phase.Status != flowstore.PhaseBlocked || phase.Outcome != flowstore.OutcomeBlocked || !strings.HasPrefix(phase.Notes, ReasonPhaseResultStale+":") {
		t.Fatalf("plan-review phase = %#v", phase)
	}
	wantRecovery := "Recover with: approach flow phase recover --flow-id " + created.FlowID + " --phase-id plan-review"
	if !strings.Contains(phase.Notes, wantRecovery) {
		t.Fatalf("plan-review notes = %q, missing %q", phase.Notes, wantRecovery)
	}
	if _, err := store.RecoverReconciledPhase(flowstore.PhaseRecoveryUpdate{
		FlowID: created.FlowID, PhaseID: "plan-review", ExpectedStatus: phase.Status, ExpectedOutcome: phase.Outcome,
		ExpectedLaunchID: "launch-1", ExpectedUpdatedAt: phase.UpdatedAt,
	}); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("post-recovery sweep = %#v", report)
	}
	if got := phaseOf(t, store, created.FlowID, "plan-review"); got.Status != flowstore.PhaseReady || got.Outcome != "" || len(got.LaunchIDs) != 0 {
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
	_ = log.WriteApplied(AppliedState{AppliedSeq: 0, Status: string(flowstore.PhaseNeedsAttention), Result: ResultApplied})
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
	spool(t, root, mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "launch-1", PhaseSetPayload{Status: string(flowstore.PhaseRunning)}))
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

// Case 1 marks a request applied without executing it, so it must see every
// field the request would write. A live record that matches only the fields
// an agent is likeliest to repeat (number, URL) is not proof the request
// landed: the branch or timestamp it changes would be lost forever.
func TestTargetReachedComparesEveryFieldOfFlowLevelPayloads(t *testing.T) {
	mergedAt := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC)
	later := mergedAt.Add(time.Hour)
	record := flowstore.FlowRecord{
		PR:    flowstore.PullRequest{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", HeadBranch: "flow/x", BaseBranch: "main", Status: "open"},
		Issue: flowstore.Issue{Provider: "github", Number: 3, URL: "https://github.com/o/r/issues/3"},
		Merge: flowstore.Merge{Status: flowstore.MergeMerged, Commit: "abc123", MergedAt: &mergedAt},
	}
	env := func(verb Verb, payload any) RequestEnvelope {
		req, err := NewRequest(verb, payload)
		if err != nil {
			t.Fatal(err)
		}
		return RequestEnvelope{Verb: verb, Payload: req.Payload}
	}
	cases := []struct {
		name string
		env  RequestEnvelope
		want bool
	}{
		{"pr identical", env(VerbPRSet, PRSetPayload{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", Head: "flow/x", Base: "main", Status: "open"}), true},
		{"pr normalized identical", env(VerbPRSet, PRSetPayload{Provider: " GitHub ", Number: 7, URL: " https://github.com/o/r/pull/7 ", Head: "flow/x ", Base: " main", Status: "open "}), true},
		{"pr base differs", env(VerbPRSet, PRSetPayload{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", Head: "flow/x", Base: "release", Status: "open"}), false},
		{"pr head differs", env(VerbPRSet, PRSetPayload{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", Head: "flow/y", Base: "main", Status: "open"}), false},
		{"pr status cleared", env(VerbPRSet, PRSetPayload{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", Head: "flow/x", Base: "main"}), false},
		{"issue identical", env(VerbIssueSet, IssueSetPayload{Provider: "github", Number: 3, URL: "https://github.com/o/r/issues/3"}), true},
		{"issue provider differs", env(VerbIssueSet, IssueSetPayload{Provider: "gitlab", Number: 3, URL: "https://github.com/o/r/issues/3"}), false},
		{"merge identical", env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeMerged, Commit: "abc123", MergedAt: mergedAt.Format(time.RFC3339)}), true},
		{"merge identical in another zone", env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeMerged, Commit: "abc123", MergedAt: mergedAt.In(time.FixedZone("x", 3600)).Format(time.RFC3339)}), true},
		{"merge timestamp differs", env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeMerged, Commit: "abc123", MergedAt: later.Format(time.RFC3339)}), false},
		{"merge commit differs", env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeMerged, Commit: "def456", MergedAt: mergedAt.Format(time.RFC3339)}), false},
		{"merge status differs", env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeBlocked}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := targetReached(record, flowstore.FlowPhase{}, tc.env); got != tc.want {
				t.Fatalf("targetReached = %v, want %v", got, tc.want)
			}
		})
	}
	blocked := flowstore.FlowRecord{Merge: flowstore.Merge{Status: flowstore.MergeBlocked}}
	if !targetReached(blocked, flowstore.FlowPhase{}, env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeBlocked})) {
		t.Fatal("blocked merge without a timestamp should be reached")
	}
	if targetReached(flowstore.FlowRecord{}, flowstore.FlowPhase{}, env(VerbMergeSet, MergeSetPayload{Status: flowstore.MergeBlocked})) {
		t.Fatal("unset merge must not be reached")
	}
}

// End to end: a spooled pr.set that changes only the base branch of the PR the
// Flow already references is executed on replay, not marked applied.
func TestReplayExecutesPRSetThatChangesOnlyTheBaseBranch(t *testing.T) {
	store, root := newTestStore(t)
	created, err := store.Create(flowstore.FlowRecord{
		Title:        "PR Base",
		Instructions: "test flow",
		RepoPath:     filepath.Join(t.TempDir(), "repo"),
		Branch:       "flow/pr-base",
	})
	if err != nil {
		t.Fatal(err)
	}
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	if _, err := store.SetPR(flowstore.PRUpdate{FlowID: created.FlowID, Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", HeadBranch: "flow/pr-base", BaseBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	req := mustRequest(t, VerbPRSet, created.FlowID, "plan", "launch-1", PRSetPayload{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", Head: "flow/pr-base", Base: "release"})
	log, _ := OpenLog(root, "launch-1")
	if _, err := log.Append(mustEnvelope(t, req, WrittenBySpool)); err != nil {
		t.Fatal(err)
	}
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Replayed != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	after, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PR.BaseBranch != "release" {
		t.Fatalf("PR after replay = %#v, want base release", after.PR)
	}
}

// Case 1 must see the effect Execute would have, not the payload's spelling
// of it: a phase action without --outcome derives the kind's default, and a
// plan.set without --plan-path resolves the saved plan's path.
func TestTargetReachedUsesTheEffectiveOutcomeAndNeverAssumesAPlanPath(t *testing.T) {
	env := func(verb Verb, payload any) RequestEnvelope {
		req, err := NewRequest(verb, payload)
		if err != nil {
			t.Fatal(err)
		}
		return RequestEnvelope{Verb: verb, Payload: req.Payload}
	}
	review := flowstore.FlowPhase{PhaseID: "plan-review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApprovedWithConcerns}
	if targetReached(flowstore.FlowRecord{}, review, env(VerbPhaseComplete, PhaseActionPayload{})) {
		t.Fatal("complete without --outcome on a plan review means approved; approved_with_concerns is not that")
	}
	review.Outcome = flowstore.OutcomeApproved
	if !targetReached(flowstore.FlowRecord{}, review, env(VerbPhaseComplete, PhaseActionPayload{})) {
		t.Fatal("completed/approved is exactly what a bare complete writes on a plan review")
	}
	autoreview := flowstore.FlowPhase{PhaseID: "autoreview", Kind: flowstore.KindAutoreview, Status: flowstore.PhaseNeedsAttention, Outcome: "needs_attention"}
	if !targetReached(flowstore.FlowRecord{}, autoreview, env(VerbPhaseNeedsAttention, PhaseActionPayload{})) {
		t.Fatal("needs_attention/needs_attention is what a bare needs-attention writes on autoreview")
	}
	autoreview.Outcome = "custom"
	if targetReached(flowstore.FlowRecord{}, autoreview, env(VerbPhaseNeedsAttention, PhaseActionPayload{})) {
		t.Fatal("a different outcome is not the default")
	}
	// A kind with no default outcome keeps the wildcard: any outcome matches.
	plain := flowstore.FlowPhase{PhaseID: "implementation", Kind: "implementation", Status: flowstore.PhaseCompleted, Outcome: "implemented"}
	if !targetReached(flowstore.FlowRecord{}, plain, env(VerbPhaseComplete, PhaseActionPayload{})) {
		t.Fatal("bare complete on a kind without a default outcome reaches any completed outcome")
	}
	linked := flowstore.FlowRecord{PlanID: "plan-9", PlanPath: "/plans/plan-9/plan.md"}
	if targetReached(linked, flowstore.FlowPhase{}, env(VerbPlanSet, PlanSetPayload{PlanID: "plan-9"})) {
		t.Fatal("plan.set without --plan-path resolves a path Execute must write; it is never assumed reached")
	}
	if !targetReached(linked, flowstore.FlowPhase{}, env(VerbPlanSet, PlanSetPayload{PlanID: "plan-9", PlanPath: "/plans/plan-9/plan.md"})) {
		t.Fatal("plan.set with the same id and path is reached")
	}
	if targetReached(linked, flowstore.FlowPhase{}, env(VerbPlanSet, PlanSetPayload{PlanID: "plan-9", PlanPath: "/elsewhere/plan.md"})) {
		t.Fatal("plan.set with another path is not reached")
	}
}
