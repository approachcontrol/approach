package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

func closedTestClosure() flowstore.Closure {
	closedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	return flowstore.Closure{Reason: "closing this line of work", ClosedAt: &closedAt}
}

func closedGuardRecord(phases ...flowstore.FlowPhase) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-closed",
		Title:        "Closed Flow",
		RepoPath:     "/dev/approach",
		WorktreePath: "/dev/approach-worktrees/flow-closed",
		Branch:       "flow/closed",
		Status:       flowstore.StatusInProgress,
		Phases:       phases,
	}
}

// flowPhaseCanLaunchAtIndex has two branches that bypass PhaseLaunchEligible —
// a ready Merge phase and an autoreview rerun — so guarding the flowstore
// primitive alone leaves both open on a closed Flow.
func TestFlowPhaseCanLaunchAtIndexRefusesClosedFlow(t *testing.T) {
	prTarget := flowstore.PullRequest{
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/closed",
		BaseBranch: "main",
	}
	tests := []struct {
		name   string
		record flowstore.FlowRecord
	}{
		{
			name: "ready implementation phase",
			record: closedGuardRecord(flowstore.FlowPhase{
				PhaseID: "implementation", Kind: flowstore.KindImplementation,
				Status: flowstore.PhaseReady, DependsOn: []string{},
			}),
		},
		{
			name: "ready merge phase",
			record: closedGuardRecord(flowstore.FlowPhase{
				PhaseID: "merge", Kind: flowstore.KindMerge,
				Status: flowstore.PhaseReady, DependsOn: []string{},
			}),
		},
		{
			name: "autoreview rerun with PR metadata",
			record: func() flowstore.FlowRecord {
				record := closedGuardRecord(flowstore.FlowPhase{
					PhaseID: "autoreview", Kind: flowstore.KindAutoreview,
					Status: flowstore.PhaseNeedsAttention, DependsOn: []string{},
				})
				record.PR = prTarget
				return record
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !flowPhaseCanLaunchAtIndex(tc.record, 0) {
				t.Fatal("fixture is not launchable before closing; the closed case would pass vacuously")
			}
			closed := tc.record
			closed.Closed = closedTestClosure()
			if flowPhaseCanLaunchAtIndex(closed, 0) {
				t.Fatal("flowPhaseCanLaunchAtIndex(closed) = true, want false")
			}
		})
	}
}

func TestFlowLaunchablePhaseRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation", Kind: flowstore.KindImplementation,
		Status: flowstore.PhaseReady, DependsOn: []string{},
	})
	if _, ok := flowLaunchablePhase(record, ""); !ok {
		t.Fatal("fixture has no launchable phase before closing")
	}
	record.Closed = closedTestClosure()
	if phase, ok := flowLaunchablePhase(record, ""); ok {
		t.Fatalf("flowLaunchablePhase(closed, any) = %q, true; want no phase", phase.PhaseID)
	}
	if phase, ok := flowLaunchablePhase(record, "implementation"); ok {
		t.Fatalf("flowLaunchablePhase(closed, implementation) = %q, true; want no phase", phase.PhaseID)
	}
}

// The auto-mode drain reaches the guarded flowstore primitive through
// nextAutoLaunchPhase, which is the route AC4's auto-mode clause depends on.
func TestNextAutoLaunchPhaseRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation", Kind: flowstore.KindImplementation,
		Status: flowstore.PhaseReady, DependsOn: []string{}, Order: 1,
	})
	record.AutoMode = true
	if _, ok := nextAutoLaunchPhase(record); !ok {
		t.Fatal("fixture has no auto-launchable phase before closing")
	}
	record.Closed = closedTestClosure()
	if phase, ok := nextAutoLaunchPhase(record); ok {
		t.Fatalf("nextAutoLaunchPhase(closed) = %q, true; want no phase", phase.PhaseID)
	}
}

func TestFlowManualMergeEligibleRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "merge", Kind: flowstore.KindMerge,
		Status: flowstore.PhaseReady, DependsOn: []string{},
	})
	record.PR = flowstore.PullRequest{
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/closed",
		BaseBranch: "main",
	}
	if !flowManualMergeEligible(record) {
		t.Fatal("fixture is not manual-merge eligible before closing")
	}
	record.Closed = closedTestClosure()
	if flowManualMergeEligible(record) {
		t.Fatal("flowManualMergeEligible(closed) = true, want false")
	}
}

func TestFlowSearchTextIncludesClosure(t *testing.T) {
	record := closedGuardRecord()
	record.Closed = closedTestClosure()
	text := flowSearchText(record)
	if !strings.Contains(text, "closing this line of work") {
		t.Fatalf("flowSearchText() = %q, want the close reason", text)
	}
	want := record.Closed.ClosedAt.UTC().Format(time.RFC3339)
	if !strings.Contains(text, want) {
		t.Fatalf("flowSearchText() = %q, want the formatted closed-at %q", text, want)
	}
}

func TestActiveFlowRecordsDropsClosedFlows(t *testing.T) {
	open := closedGuardRecord()
	open.FlowID = "flow-open"

	closed := closedGuardRecord()
	closed.FlowID = "flow-closed"
	closed.Closed = closedTestClosure()
	closed.Status = flowstore.StatusClosed

	merged := closedGuardRecord()
	merged.FlowID = "flow-merged"
	merged.Status = flowstore.StatusMerged

	active := activeFlowRecords([]flowstore.FlowRecord{open, closed, merged})
	if len(active) != 1 || active[0].FlowID != "flow-open" {
		t.Fatalf("activeFlowRecords() = %#v, want only the open Flow", active)
	}
}

// A close committed between launch bookkeeping and the spawn would leave a
// launch ID for an agent that never started, so the reservation has to be held
// across both rather than acquired at the terminal-open step.
func TestFlowStarterPlanHoldsLaunchReservationAcrossBookkeeping(t *testing.T) {
	var order []string
	released := 0
	starterOptions := func(reserveErr error) FlowStarterOptions {
		return FlowStarterOptions{
			CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
				record.FlowID = "created-flow"
				record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady, Order: 1}}
				return record, nil
			},
			CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
				return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/created", Branch: "flow/created"}, nil
			},
			ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
				if reserveErr != nil {
					return flowstore.FlowRecord{}, nil, reserveErr
				}
				order = append(order, "reserve:"+flowID)
				return flowstore.FlowRecord{FlowID: flowID}, func() { released++ }, nil
			},
			AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
				order = append(order, "launch-id:"+update.FlowID)
				return flowstore.FlowRecord{FlowID: update.FlowID}, nil
			},
			ResolveCommit: func(string) string { return "abc123" },
			NewLaunchID:   func() string { return "launch-1" },
		}
	}

	result, err := NewFlowStarter(starterOptions(nil)).StartPlan(FlowStartRequest{RepoPath: "/dev/alpha", Title: "New flow"})
	if err != nil {
		t.Fatalf("StartPlan() error = %v", err)
	}
	want := []string{"reserve:created-flow", "launch-id:created-flow"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("call order = %v, want the reservation before launch bookkeeping %v", order, want)
	}
	if result.LaunchRelease == nil {
		t.Fatal("StartPlan must hand the held reservation to the caller that spawns")
	}
	if released != 0 {
		t.Fatalf("release count = %d, want the reservation still held at return", released)
	}
	result.LaunchRelease()
	if released != 1 {
		t.Fatalf("release count = %d, want 1 after the caller releases", released)
	}

	// A Flow closed during startup must fail the launch outright rather than
	// persisting a launch ID for an agent that will be refused.
	closedErr := errors.New("cannot launch an agent for flow \"created-flow\" because it is closed")
	order = nil
	failed, err := NewFlowStarter(starterOptions(closedErr)).StartPlan(FlowStartRequest{RepoPath: "/dev/alpha", Title: "New flow"})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("StartPlan() error = %v, want the closed-Flow refusal", err)
	}
	if len(order) != 0 {
		t.Fatalf("call order = %v, want no launch bookkeeping after a refused reservation", order)
	}
	if failed.LaunchRelease != nil {
		t.Fatal("a refused reservation must not hand back a release")
	}
}

// The Sessions pane resumes through a Flow-agnostic context, so without an
// explicit check `r` there would spawn an agent for a Flow the Flow-phase pane
// already refuses to resume.
func TestSessionResumeRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseRunning,
		Order:   1,
	})
	record.RepoPath = "/dev/alpha"
	record.WorktreePath = "/dev/alpha"

	closed := record
	closed.Closed = closedTestClosure()
	closed.Status = flowstore.StatusClosed

	session := sessions.SessionRecord{
		Provider:     sessions.ProviderCodex,
		SessionID:    "session-1",
		WorktreePath: record.WorktreePath,
		RepoPath:     record.RepoPath,
		FlowID:       record.FlowID,
		FlowPhaseID:  "implementation",
	}

	// The reservation is what makes the answer authoritative, and it has to
	// still be held when the context comes back: releasing it here would
	// reopen the window between the closed-state answer and the spawn.
	open := newManualLaunchHarness(t, record)
	_, release, ok, _ := open.model().sessionResumeLaunchContext(session)
	if !ok {
		t.Fatal("an open Flow must still allow a Sessions-pane resume")
	}
	if open.launchReservations != 1 || open.launchReleases != 0 {
		t.Fatalf("reservations = %d releases = %d, want the reservation held across the return",
			open.launchReservations, open.launchReleases)
	}
	release()
	if open.launchReleases != 1 {
		t.Fatalf("releases = %d, want 1 once the caller releases", open.launchReleases)
	}

	h := newManualLaunchHarness(t, closed)
	_, _, ok, next := h.model().sessionResumeLaunchContext(session)
	if ok {
		t.Fatal("a session belonging to a closed Flow must not resume")
	}
	if !strings.Contains(next.status.Text, "closed") {
		t.Fatalf("status = %q, want it to name the closed Flow", next.status.Text)
	}

	// A session can outlive its Flow record. Only a closed Flow may refuse: a
	// deleted or unreadable one reports through the same error return, and
	// treating that as a refusal would strand the session.
	orphan := session
	orphan.FlowID = "flow-missing"
	if _, _, ok, _ := h.model().sessionResumeLaunchContext(orphan); !ok {
		t.Fatal("a session whose Flow no longer exists must still resume")
	}

	// A reservation that never succeeded proves nothing about the Flow, so a
	// held lock or an unreadable store has to refuse rather than spawn
	// unreserved across a concurrent close.
	unreadable := newManualLaunchHarness(t, record)
	unreadable.reserveLaunchErr = errors.New("acquire launch/close reservation for flow \"flow-closed\": timeout")
	_, _, ok, failed := unreadable.model().sessionResumeLaunchContext(session)
	if ok {
		t.Fatal("a failed reservation must refuse the resume")
	}
	if !strings.Contains(failed.status.Text, "timeout") {
		t.Fatalf("status = %q, want the reservation failure surfaced", failed.status.Text)
	}
}

// An armed drain outlives the close whenever a running terminal occupies the
// Flow, because occupancy is checked before launchability. Reopening before the
// next unscoped poll would then launch the successor, so the close itself has
// to disarm rather than leaving it to a poll that may not run in between.
func TestHandleFlowClosedDisarmsAutoAdvanceWork(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseReady,
		Order:   1,
	})
	record.AutoMode = true
	// The handler ignores messages for other repos, so this has to match the
	// repo the harness model is focused on.
	record.RepoPath = "/dev/alpha"

	closed := record
	closed.Closed = closedTestClosure()
	closed.Status = flowstore.StatusClosed
	closed.UpdatedAt = time.Now()

	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m = m.armAutoAdvanceDrain(record.FlowID)
	m = m.armRepairAutoDrain(record.FlowID)

	m = m.handleFlowClosed(FlowClosedMsg{
		RepoPath: record.RepoPath,
		FlowID:   record.FlowID,
		Flow:     closed,
	})

	if _, armed := m.autoAdvanceDrainFlows[record.FlowID]; armed {
		t.Fatal("closing a Flow must disarm its auto-advance drain")
	}
	if m.hasPendingRepairAutoDrainMarker(record.FlowID) {
		t.Fatal("closing a Flow must drop its pending repair-drain marker")
	}
}

// A close from another process emits no local message, so the poll has to
// disarm on its own. Occupancy only defers a drain, so the closed check has to
// come first or an occupied Flow keeps its drain for as long as it is occupied.
func TestAutoAdvanceDrainDisarmsOnClosedRecordWhileOccupied(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseReady,
		Order:   1,
	})
	record.RepoPath = "/dev/alpha"
	record.WorktreePath = "/dev/alpha"
	record.AutoMode = true

	closed := record
	closed.Closed = closedTestClosure()
	closed.Status = flowstore.StatusClosed

	h := newManualLaunchHarness(t, closed)
	m := h.model()
	// A running Flow terminal occupies the Flow, which is what would otherwise
	// keep the drain armed indefinitely.
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Scope:    embeddedTerminalScopeFlow,
		FlowID:   closed.FlowID,
		Terminal: flowPhaseLaunchTestTerminal{state: "running"},
	})
	if !m.flowAutoAdvanceOccupied(closed) {
		t.Fatal("fixture should occupy the Flow")
	}
	m = m.armAutoAdvanceDrain(closed.FlowID)
	m = m.armRepairAutoDrain(closed.FlowID)

	next, cmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{closed})
	if cmd != nil {
		t.Fatal("a closed Flow must not queue an auto-advance launch")
	}
	if _, armed := next.autoAdvanceDrainFlows[closed.FlowID]; armed {
		t.Fatal("an occupied closed Flow must still have its drain disarmed")
	}
	if next.hasPendingRepairAutoDrainMarker(closed.FlowID) {
		t.Fatal("an occupied closed Flow must drop its pending repair-drain marker")
	}
}
