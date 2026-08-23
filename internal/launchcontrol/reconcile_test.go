package launchcontrol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/flowlease"
)

func TestSweepWithoutEvidenceLeavesRunningPhaseAlone(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "No Evidence")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	c := newTestController(t, store, root)
	before := phaseOf(t, store, created.FlowID, "plan")
	if report := c.Sweep(); report.Reconciled != 0 || report.Launches != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	after := phaseOf(t, store, created.FlowID, "plan")
	if after.Status != flowstore.PhaseRunning || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("phase written without evidence: %#v", after)
	}
	log, _ := OpenLog(root, "launch-1")
	if _, ok, _ := log.Applied(); ok {
		t.Fatal("applied.json written without evidence")
	}
}

func TestSweepDemotesOnExitJSONWithCodeInNotes(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Exit JSON")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	if err := RecordLaunchExit(root, created.FlowID, "plan", "launch-1", 3, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	c := newTestController(t, store, root)
	var events []AppliedEvent
	c.SetAppliedNotifier(func(e AppliedEvent) { events = append(events, e) })
	if report := c.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseNeedsAttention || phase.Outcome != ReasonPhaseResultMissing {
		t.Fatalf("phase = %#v", phase)
	}
	if phase.Reconciliation == nil || phase.Reconciliation.Reason != ReasonPhaseResultMissing || phase.Reconciliation.LaunchID != "launch-1" {
		t.Fatalf("reconciliation stamp = %#v", phase.Reconciliation)
	}
	for _, want := range []string{
		"phase_result_missing: launch launch-1 exited (periodic_sweep, exit code 3) without a valid result for phase plan",
		"observed running",
		"Recover with: approach flow phase recover --flow-id " + created.FlowID + ` --phase-id plan`,
	} {
		if !strings.Contains(phase.Notes, want) {
			t.Fatalf("notes = %q, missing %q", phase.Notes, want)
		}
	}
	if len(events) != 1 || events[0].LaunchID != "launch-1" {
		t.Fatalf("events = %#v", events)
	}
	log, _ := OpenLog(root, "launch-1")
	applied, ok, _ := log.Applied()
	if !ok || applied.Result != ResultReconciled || applied.Status != string(flowstore.PhaseNeedsAttention) {
		t.Fatalf("applied = %#v", applied)
	}
	// Idempotent: a second sweep finds needs_attention and does nothing.
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("second sweep = %#v", report)
	}
}

func TestSweepLivenessProbeRespectsGraceAndLeaseVeto(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Probe")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	now := time.Now().UTC()
	endedAt := now
	probe := func(launchID string) (LaunchLiveness, error) {
		return LaunchLiveness{RecordKnown: true, Ended: true, EndedAt: endedAt}, nil
	}
	lease := freeLease
	c := newTestController(t, store, root, func(o *Options) {
		o.Liveness = probe
		o.InspectLease = func(root, flowID string) (flowlease.LeaseState, error) { return lease(root, flowID) }
		o.Now = func() time.Time { return now }
	})
	endedAt = now.Add(-2 * time.Minute)
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("sweep inside grace = %#v", report)
	}
	endedAt = now.Add(-30 * time.Minute)
	lease = heldLease
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("sweep with held lease = %#v", report)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("held lease did not veto demotion")
	}
	lease = freeLease
	if report := c.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep past grace = %#v", report)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseNeedsAttention || !strings.Contains(phase.Notes, "exit code unknown") {
		t.Fatalf("phase = %#v", phase)
	}
}

func TestSweepSkipsPhasesNotRunningOrNotLatest(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Skip")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	_ = RecordLaunchExit(root, created.FlowID, "plan", "launch-1", 0, false, time.Now())
	// launch-2 now owns the phase, still running.
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-2")
	c := newTestController(t, store, root)
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	if phase := phaseOf(t, store, created.FlowID, "plan"); phase.Status != flowstore.PhaseRunning || flowstore.LatestPhaseLaunchID(phase) != "launch-2" {
		t.Fatalf("phase = %#v", phase)
	}
	// Completed under launch-2, and launch-2 exits: not running, untouched.
	completePhase(t, store, created.FlowID, "plan", "")
	_ = RecordLaunchExit(root, created.FlowID, "plan", "launch-2", 0, false, time.Now())
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("sweep after completion = %#v", report)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseCompleted {
		t.Fatal("completed phase demoted")
	}
}

func TestReconcileReplaysFirstAndDoesNotDemoteALandedResult(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Landed")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	spool(t, root, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "spooled"}))
	c := newTestController(t, store, root)
	outcome, err := c.Reconcile(created.FlowID, "plan", "launch-1", ExitEvidence{Source: SourceTerminalExit, Code: 0, CodeKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Action != ActionNone || outcome.Replayed != 1 || outcome.Status != string(flowstore.PhaseCompleted) {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestReconcileDemotesRunningPhaseOnTerminalExitAndPlanReviewBlocked(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Terminal Exit")
	completePhase(t, store, created.FlowID, "plan", "")
	launchWithBaseline(t, store, root, created.FlowID, "plan-review", "launch-1")
	c := newTestController(t, store, root, func(o *Options) { o.InspectLease = heldLease })
	// terminal_exit is the model's own evidence; the lease veto does not apply.
	outcome, err := c.Reconcile(created.FlowID, "Plan-Review", "launch-1", ExitEvidence{Source: SourceTerminalExit, Code: 1, CodeKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Action != ActionDemoted || outcome.Status != string(flowstore.PhaseBlocked) || outcome.Reason != ReasonPhaseResultMissing {
		t.Fatalf("outcome = %#v", outcome)
	}
	phase := phaseOf(t, store, created.FlowID, "plan-review")
	if phase.Status != flowstore.PhaseBlocked || phase.Outcome != flowstore.OutcomeBlocked || !strings.HasPrefix(phase.Notes, "phase_result_missing: launch launch-1 exited (terminal_exit, exit code 1)") {
		t.Fatalf("plan-review = %#v", phase)
	}
	if phase.Reconciliation == nil || phase.Reconciliation.Reason != ReasonPhaseResultMissing || phase.Reconciliation.LaunchID != "launch-1" {
		t.Fatalf("plan-review reconciliation stamp = %#v", phase.Reconciliation)
	}
	wantRecovery := "Recover with: approach flow phase recover --flow-id " + created.FlowID + " --phase-id plan-review"
	if !strings.Contains(phase.Notes, wantRecovery) {
		t.Fatalf("plan-review notes = %q, missing %q", phase.Notes, wantRecovery)
	}
	// session_end respects the lease veto even with a recent EndedAt.
	other := createFlow(t, store, "Session End")
	launchWithBaseline(t, store, root, other.FlowID, "plan", "launch-2")
	outcome, err = c.Reconcile(other.FlowID, "plan", "launch-2", ExitEvidence{Source: SourceSessionEnd, EndedAt: time.Now().UTC()})
	if err != nil || outcome.Action != ActionVetoed {
		t.Fatalf("vetoed outcome = %#v %v", outcome, err)
	}
	if phaseOf(t, store, other.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("vetoed reconcile wrote")
	}
	// Unsafe launch ids are refused before any lock is taken.
	if _, err := c.Reconcile(other.FlowID, "plan", "../x", ExitEvidence{}); err == nil {
		t.Fatal("unsafe launch id accepted")
	}
}

func TestReconcileSessionEndWaitsForGraceWithoutLease(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Session End Grace")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	now := time.Now().UTC()
	c := newTestController(t, store, root, func(o *Options) {
		o.InspectLease = freeLease
		o.Now = func() time.Time { return now }
	})
	outcome, err := c.Reconcile(created.FlowID, "plan", "launch-1", ExitEvidence{Source: SourceSessionEnd, EndedAt: now})
	if err != nil || outcome.Action != ActionNone {
		t.Fatalf("fresh session_end outcome = %#v %v", outcome, err)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("fresh session_end demoted a live phase")
	}
	outcome, err = c.Reconcile(created.FlowID, "plan", "launch-1", ExitEvidence{Source: SourceSessionEnd})
	if err != nil || outcome.Action != ActionNone {
		t.Fatalf("zero EndedAt outcome = %#v %v", outcome, err)
	}
	outcome, err = c.Reconcile(created.FlowID, "plan", "launch-1", ExitEvidence{
		Source: SourceSessionEnd, EndedAt: now.Add(-SessionEndGrace),
	})
	if err != nil || outcome.Action != ActionDemoted || outcome.Reason != ReasonPhaseResultMissing {
		t.Fatalf("aged session_end outcome = %#v %v", outcome, err)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseNeedsAttention || phase.Outcome != ReasonPhaseResultMissing {
		t.Fatalf("aged session_end phase = %#v", phase)
	}
}

func TestRetainRemovesOnlyAppliedOldLaunchDirectories(t *testing.T) {
	store, root := newTestStore(t)
	c := newTestController(t, store, root)
	old := time.Now().Add(-20 * 24 * time.Hour)
	age := func(id string, names ...string) {
		log, _ := OpenLog(root, id)
		for _, name := range names {
			if err := os.Chtimes(filepath.Join(log.Dir(), name), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 1. fully applied, 20 days old → removed.
	applied, _ := OpenLog(root, "applied-old")
	_ = applied.WriteLaunch(LaunchInfo{FlowID: "f", PhaseID: "plan"})
	req := mustRequest(t, VerbPhaseComplete, "f", "plan", "applied-old", PhaseActionPayload{})
	seq, _ := applied.Append(mustEnvelope(t, req, WrittenByController))
	_ = applied.WriteApplied(AppliedState{AppliedSeq: seq, Status: "completed", Result: ResultApplied})
	requests, _ := applied.requestFiles()
	age("applied-old", "launch.json", "applied.json", filepath.Join("requests", requests[0]))
	// 2. one pending request, 20 days old → kept.
	pending, _ := OpenLog(root, "pending-old")
	_ = pending.WriteLaunch(LaunchInfo{FlowID: "f", PhaseID: "plan"})
	req = mustRequest(t, VerbPhaseComplete, "f", "plan", "pending-old", PhaseActionPayload{})
	_, _ = pending.Append(mustEnvelope(t, req, WrittenBySpool))
	requests, _ = pending.requestFiles()
	age("pending-old", "launch.json", filepath.Join("requests", requests[0]))
	// 3. exit.json only, 20 days old → removed.
	exited, _ := OpenLog(root, "exit-old")
	_ = exited.WriteLaunch(LaunchInfo{FlowID: "f", PhaseID: "plan"})
	_ = exited.WriteExit(ExitRecord{FlowID: "f", ExitCode: 0, EndedAt: old})
	age("exit-old", "launch.json", "exit.json")
	// 4. fresh → kept.
	fresh, _ := OpenLog(root, "fresh")
	_ = fresh.WriteLaunch(LaunchInfo{FlowID: "f", PhaseID: "plan"})
	// A replay pass just rewrote the applied dir's lock file.
	unlock, _ := applied.Lock(time.Second)
	unlock()

	retired, err := c.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if retired != 2 {
		t.Fatalf("retired = %d, want 2", retired)
	}
	ids, _ := ListLaunchIDs(root)
	if strings.Join(ids, ",") != "fresh,pending-old" {
		t.Fatalf("remaining = %v", ids)
	}
	// A held lock is never removed.
	heldLog, _ := OpenLog(root, "held-old")
	_ = heldLog.WriteLaunch(LaunchInfo{FlowID: "f", PhaseID: "plan"})
	age("held-old", "launch.json")
	unlock, _ = heldLog.Lock(time.Second)
	defer unlock()
	if retired, _ := c.Retain(); retired != 0 {
		t.Fatalf("retired a held launch: %d", retired)
	}
}

// Terminal exit is authoritative evidence, and it is durable before Reconcile
// waits on anything that can fail transiently — the launch lock (which the
// SessionEnd hook may hold), the store — so a reconciliation that does not
// finish is retried by the sweep from exit.json rather than lost with the
// dismissed terminal slot.
func TestReconcileWritesTerminalExitEvidenceBeforeTakingTheLaunchLock(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Durable Exit")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	holder, _ := OpenLog(root, "launch-1")
	unlock, err := holder.Lock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := newTestController(t, store, root, func(o *Options) { o.InspectLease = freeLease })
	endedAt := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC)
	done := make(chan Outcome, 1)
	go func() {
		outcome, _ := c.Reconcile(created.FlowID, "plan", "launch-1", ExitEvidence{Source: SourceTerminalExit, EndedAt: endedAt})
		done <- outcome
	}()
	// exit.json appears while the lock is still held.
	deadline := time.Now().Add(3 * time.Second)
	var exit ExitRecord
	for {
		record, ok, err := holder.Exit()
		if err == nil && ok {
			exit = record
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exit.json was not written before the lock was taken")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if exit.Source != string(SourceTerminalExit) || !exit.CodeUnknown || exit.ExitCode != 0 || !exit.EndedAt.Equal(endedAt) || exit.FlowID != created.FlowID || exit.PhaseID != "plan" {
		t.Fatalf("exit.json = %#v", exit)
	}
	select {
	case <-done:
		t.Fatal("reconcile did not wait for the lock holder")
	case <-time.After(200 * time.Millisecond):
	}
	// The sweep, which is what runs when the reconciliation itself never
	// finishes, demotes from that record with the code marked unknown.
	unlock()
	select {
	case outcome := <-done:
		if outcome.Action != ActionDemoted {
			t.Fatalf("outcome = %#v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile never finished")
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Status != flowstore.PhaseNeedsAttention || !strings.Contains(phase.Notes, "exited (terminal_exit, exit code unknown)") {
		t.Fatalf("phase = %#v", phase)
	}
	// And a sweep over a launch whose only evidence is such a record reads
	// the unknown code back rather than reporting exit code 0.
	other := createFlow(t, store, "Sweep From Exit")
	launchWithBaseline(t, store, root, other.FlowID, "plan", "launch-2")
	otherLog, _ := OpenLog(root, "launch-2")
	if err := otherLog.WriteExit(ExitRecord{FlowID: other.FlowID, PhaseID: "plan", CodeUnknown: true, EndedAt: endedAt, Source: string(SourceTerminalExit)}); err != nil {
		t.Fatal(err)
	}
	if report := c.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep = %#v", report)
	}
	if phase := phaseOf(t, store, other.FlowID, "plan"); !strings.Contains(phase.Notes, "exited (periodic_sweep, exit code unknown)") {
		t.Fatalf("swept phase = %#v", phase)
	}
}

// The liveness probe walks every session record, so the sweep asks it only
// for a launch whose phase is still running under that launch — not for the
// retained history of finished launches, every thirty seconds.
func TestSweepProbesLivenessOnlyForRunningPhasesItOwns(t *testing.T) {
	store, root := newTestStore(t)
	done := createFlow(t, store, "Done")
	launchWithBaseline(t, store, root, done.FlowID, "plan", "launch-done")
	completePhase(t, store, done.FlowID, "plan", "")
	superseded := createFlow(t, store, "Superseded")
	launchWithBaseline(t, store, root, superseded.FlowID, "plan", "launch-old")
	launchWithBaseline(t, store, root, superseded.FlowID, "plan", "launch-new")
	var probed []string
	c := newTestController(t, store, root, func(o *Options) {
		o.Liveness = func(launchID string) (LaunchLiveness, error) {
			probed = append(probed, launchID)
			return LaunchLiveness{}, nil
		}
	})
	if report := c.Sweep(); report.Reconciled != 0 {
		t.Fatalf("sweep = %#v", report)
	}
	if len(probed) != 1 || probed[0] != "launch-new" {
		t.Fatalf("probed = %v, want only the launch that owns a running phase", probed)
	}
}

// A launch that still owns a running phase is not retired however old its
// directory is: retiring it would drop the registration its agent's next
// result authenticates with, and a controller refusal is final — the CLI
// would not fall back. Once the phase has moved on, the launch retires.
func TestRetainKeepsLaunchesThatStillOwnARunningPhase(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Long Running")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	c := newTestController(t, store, root)
	if _, err := c.Register(Registration{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * 24 * time.Hour)
	log, _ := OpenLog(root, "launch-1")
	for _, name := range []string{"launch.json", "baseline.json"} {
		if err := os.Chtimes(filepath.Join(log.Dir(), name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	retired, err := c.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if retired != 0 || !log.Exists() {
		t.Fatalf("retired = %d, exists = %v; a launch owning a running phase was retired", retired, log.Exists())
	}
	c.mu.Lock()
	_, registered := c.registrations["launch-1"]
	c.mu.Unlock()
	if !registered {
		t.Fatal("registration dropped for a launch owning a running phase")
	}
	completePhase(t, store, created.FlowID, "plan", "")
	// Completing bumps nothing in the launch directory; the age still holds.
	retired, err = c.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if retired != 1 || log.Exists() {
		t.Fatalf("retired = %d, exists = %v; a finished launch was kept", retired, log.Exists())
	}
}

// A Flow the store cannot read right now is not evidence that the launch has
// finished. Retiring on that guess deletes the registration a still-running
// agent's next proxied result authenticates with, and the refusal that follows
// is final.
func TestRetainKeepsLaunchesWhoseOwnershipCannotBeRead(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Unreadable Owner")
	launchWithBaseline(t, store, root, created.FlowID, "plan", "launch-1")
	c := newTestController(t, store, root)
	if _, err := c.Register(Registration{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	completePhase(t, store, created.FlowID, "plan", "")
	old := time.Now().Add(-20 * 24 * time.Hour)
	log, _ := OpenLog(root, "launch-1")
	for _, name := range []string{"launch.json", "baseline.json"} {
		if err := os.Chtimes(filepath.Join(log.Dir(), name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	// The phase is finished, so this launch would retire — except the store
	// can no longer answer whether it is.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	retired, err := c.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if retired != 0 || !log.Exists() {
		t.Fatalf("retired = %d, exists = %v; a launch was retired on an unreadable Flow", retired, log.Exists())
	}
	c.mu.Lock()
	_, registered := c.registrations["launch-1"]
	c.mu.Unlock()
	if !registered {
		t.Fatal("registration dropped on an unreadable Flow")
	}
}
