package model

import (
	"testing"

	"github.com/approachcontrol/approach/flowoccupancy"
	"github.com/approachcontrol/approach/flowstore"
)

// The acceptance criterion this suite is most likely to miss: sources holding
// the Flow at the same time, not one at a time. Every table above installs at
// most two; these install four and five, and record what each consumer reports
// for the identical state.

// occupancyAllFiveSources is the state the bead names: a cross-process lease, an
// in-process attempt, a retained terminal slot, a persisted running phase, and a
// live session on that phase.
func occupancyAllFiveSources() []occupancySource {
	return []occupancySource{srcLeaseHeld, srcAttemptManualPhase, srcFlowTerminal, srcRunningPhase, srcPhaseSession}
}

// occupancyFourInProcessSources is the same state with the lease released. The
// lease outranks everything in four of the ladders, so dropping it is what makes
// the lower rungs — and their disagreements — observable.
func occupancyFourInProcessSources() []occupancySource {
	return []occupancySource{srcAttemptManualPhase, srcFlowTerminal, srcRunningPhase, srcPhaseSession}
}

// occupancyRepairComparableSources is the same set minus the phase session,
// which is the one source repair cannot be compared on: a matched live session
// makes the record un-repairable outright, so repair answers with silence rather
// than with a rung of its ladder. TestRepairRefusesALiveSessionFlowInSilence
// pins that separately.
func occupancyRepairComparableSources() []occupancySource {
	return []occupancySource{srcAttemptManualPhase, srcFlowTerminal, srcRunningPhase}
}

// TestEverySourceHoldingAtOnceIsSeenByEveryComposite proves the fixture really
// installed all five, through the composites rather than through the installers.
func TestEverySourceHoldingAtOnceIsSeenByEveryComposite(t *testing.T) {
	f := newOccupancyFixture(t, occupancyAllFiveSources()...)

	if !f.m.flowLaunchAdmissionOccupied(f.flowID()) {
		t.Fatal("admission occupancy must hold")
	}
	if !f.m.flowLaunchRuntimeOccupied(f.flowID()) {
		t.Fatal("runtime occupancy must hold")
	}
	if !f.m.flowAutoAdvanceOccupied(f.record) {
		t.Fatal("the drain gate must hold")
	}
	if !flowLaunchPhaseSessionOccupied(f.phase(), f.stored) {
		t.Fatal("the phase-session union must hold")
	}
	if reason := worktreeAgentRecordOccupancyReason(f.record); reason == "" {
		t.Fatal("the worktree agent's persisted rung must hold")
	}
}

// TestSimultaneousSourcesReportPerConsumerStatuses is the matrix. Each row is one
// consumer's answer to the identical Model state, and the rows are meant to be
// read side by side: they are what F2's "four ladders, no shared table" costs.
func TestSimultaneousSourcesReportPerConsumerStatuses(t *testing.T) {
	t.Run("with the lease held, every loud consumer names the lease", func(t *testing.T) {
		sources := occupancyAllFiveSources()
		f := newOccupancyFixture(t, sources...)

		if got := statusFromRefusedLaunch(t, f, flowLaunchKindManualPhase); got != flowLeaseOccupiedStatus {
			t.Fatalf("manual phase status = %q, want %q", got, flowLeaseOccupiedStatus)
		}
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindPhaseResume); got != flowLeaseOccupiedStatus {
			t.Fatalf("phase resume status = %q, want %q", got, flowLeaseOccupiedStatus)
		}
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindWorktreeAgent); got != flowLeaseOccupiedStatus {
			t.Fatalf("worktree agent status = %q, want %q", got, flowLeaseOccupiedStatus)
		}
		if got := statusFromRefusedAutofix(t, sources); got != flowLeaseOccupiedStatus {
			t.Fatalf("autofix status = %q, want %q", got, flowLeaseOccupiedStatus)
		}
		if got := statusFromRefusedRepair(t, occupancyRepairComparableSources(), srcLeaseHeld); got != flowLeaseOccupiedStatus {
			t.Fatalf("repair status = %q, want %q", got, flowLeaseOccupiedStatus)
		}
		// The auto admission stays silent through all five, exactly as it does
		// through one.
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindAutoPhase); got != "" {
			t.Fatalf("auto phase status = %q, want silence", got)
		}
		// The module owns the lease and runtime ordering together, so the named
		// verdict reports the lease above repair's runtime ladder.
		verdict := f.m.repairOccupancy(f.flowID(), flowoccupancy.StageAdmission)
		if got := flowRepairOccupancyStatus(verdict); got != flowLeaseOccupiedStatus {
			t.Fatalf("repair ladder = %q, want %q", got, flowLeaseOccupiedStatus)
		}
	})

	t.Run("without the lease, repair and autofix disagree about the same state", func(t *testing.T) {
		sources := occupancyFourInProcessSources()

		// The identical state — a manual phase attempt plus a retained Flow
		// terminal — is reported by repair as a competing *attempt* and by
		// autofix as a competing *terminal*. Both hold simultaneously; the two
		// ladders simply rank them in opposite orders. ADR 0003 Q2 keeps this
		// divergence, and this row is what pins it.
		comparable := occupancyRepairComparableSources()
		if got := statusFromRefusedRepair(t, comparable); got != flowRepairPhasePendingStatus {
			t.Fatalf("repair status = %q, want %q", got, flowRepairPhasePendingStatus)
		}
		if got := statusFromRefusedAutofix(t, comparable); got != flowAutofixTerminalStatus {
			t.Fatalf("autofix status = %q, want %q", got, flowAutofixTerminalStatus)
		}

		f := newOccupancyFixture(t, sources...)
		// Manual collapses all four into one generic status; it names nothing.
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindManualPhase); got != noLaunchableFlowPhaseStatus {
			t.Fatalf("manual phase status = %q, want %q", got, noLaunchableFlowPhaseStatus)
		}
		// Resume names the terminal, because the terminal is not a repair slot.
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindPhaseResume); got != flowPhaseResumeTerminalStatus {
			t.Fatalf("phase resume status = %q, want %q", got, flowPhaseResumeTerminalStatus)
		}
		// The worktree agent stops at its attempt rung, two rungs above the
		// slot rung the same state also satisfies.
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindWorktreeAgent); got != flowWorktreeAgentPendingStatus {
			t.Fatalf("worktree agent status = %q, want %q", got, flowWorktreeAgentPendingStatus)
		}
		if got := statusFromRefusedLaunch(t, f, flowLaunchKindAutoPhase); got != "" {
			t.Fatalf("auto phase status = %q, want silence", got)
		}
	})
}

// TestRepairRefusesALiveSessionFlowInSilence is why repair drops out of the
// four-source comparison above. A matched live session is treated as active work
// even when the persisted phase already says blocked, so the record stops being
// repairable at all — and an un-repairable selection refuses silently, before
// any rung of the occupancy ladder is consulted.
func TestRepairRefusesALiveSessionFlowInSilence(t *testing.T) {
	f := newOccupancyFixtureFor(t, occupancyRepairFlowRecord(), srcPhaseSession, srcFlowTerminal)

	if _, repairable := flowRepairObstructionForRecord(f.record); repairable {
		t.Fatal("a live session on a blocked phase makes the record un-repairable")
	}
	before := f.m.setStatus(statusOther, "user is looking at this")
	next, cmd, admitted := before.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindRepair,
		FlowID: f.flowID(),
	})
	if admitted || cmd != nil {
		t.Fatalf("admitted = %v, cmd != nil = %v, want a refusal", admitted, cmd != nil)
	}
	if got := next.status.Text; got != "user is looking at this" {
		t.Fatalf("status = %q, want the pre-existing status untouched", got)
	}
	if f.m.selectedFlowRepairReady() {
		t.Fatal("R must not be advertised for an un-repairable Flow")
	}
}

// TestSimultaneousSourcesWithdrawEveryFooter is the display half of the matrix:
// with four sources holding, no key is advertised anywhere.
func TestSimultaneousSourcesWithdrawEveryFooter(t *testing.T) {
	f := newOccupancyFixture(t, occupancyFourInProcessSources()...)
	if f.m.selectedFlowHasLaunchablePhase() {
		t.Fatal("g must not be advertised")
	}
	if f.m.previewPhaseResume(f.flowID()) {
		t.Fatal("the resume footer must withdraw for a non-repair Flow terminal")
	}
	if f.m.selectedFlowWorktreeAgentReady() {
		t.Fatal("the worktree agent footer must withdraw")
	}
	if newOccupancyFixtureFor(t, autofixFlowRecord(), occupancyFourInProcessSources()...).m.selectedFlowAutofixReady() {
		t.Fatal("U must not be advertised")
	}
	if newOccupancyFixtureFor(t, occupancyRepairFlowRecord(), occupancyFourInProcessSources()...).m.selectedFlowRepairReady() {
		t.Fatal("R must not be advertised")
	}
}

// TestSimultaneousSourcesRefuseTheInstallBackstopPerKind runs the last line of
// defense against the same four-source state. Both slot predicates hold here, so
// every kind refuses — including savedSessionResume, which reads S6 alone.
func TestSimultaneousSourcesRefuseTheInstallBackstopPerKind(t *testing.T) {
	f := newOccupancyFixture(t, append(occupancyFourInProcessSources(), srcRepairSlot)...)
	kinds := []flowLaunchKind{
		flowLaunchKindManualPhase,
		flowLaunchKindCreatePhase,
		flowLaunchKindAutoPhase,
		flowLaunchKindPhaseResume,
		flowLaunchKindRepair,
		flowLaunchKindAutofix,
		flowLaunchKindWorktreeAgent,
		flowLaunchKindSavedSessionResume,
	}
	for _, kind := range kinds {
		status, refused := f.m.flowLaunchEmbeddedBackstop(kind, f.flowID())
		if !refused || status == "" {
			t.Fatalf("kind %v: refused = %v, status = %q, want a refusal with text", kind, refused, status)
		}
	}
}

// statusFromRefusedLaunch drives one admission through the lifecycle's only
// entry point and returns the status it left behind, failing if it admitted.
func statusFromRefusedLaunch(t *testing.T, f occupancyFixture, kind flowLaunchKind) string {
	t.Helper()
	next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
		Kind:    kind,
		FlowID:  f.flowID(),
		PhaseID: f.phase().PhaseID,
	})
	if admitted || cmd != nil {
		t.Fatalf("kind %v: admitted = %v, cmd != nil = %v, want a refusal", kind, admitted, cmd != nil)
	}
	return next.status.Text
}

// statusFromRefusedAutofix and statusFromRefusedRepair rebuild the fixture on
// the record their kind requires: autofix needs a fully completed Flow with an
// open PR, and repair needs a gated one with no launchable phase, so neither can
// share the launchable record the other tables use.
func statusFromRefusedAutofix(t *testing.T, sources []occupancySource, extra ...occupancySource) string {
	t.Helper()
	return statusFromRefusedKindOnRecord(t, autofixFlowRecord(), flowLaunchKindAutofix, append(append([]occupancySource(nil), sources...), extra...))
}

func statusFromRefusedRepair(t *testing.T, sources []occupancySource, extra ...occupancySource) string {
	t.Helper()
	return statusFromRefusedKindOnRecord(t, occupancyRepairFlowRecord(), flowLaunchKindRepair, append(append([]occupancySource(nil), sources...), extra...))
}

func statusFromRefusedKindOnRecord(t *testing.T, record flowstore.FlowRecord, kind flowLaunchKind, sources []occupancySource) string {
	t.Helper()
	f := newOccupancyFixtureFor(t, record, sources...)
	next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{Kind: kind, FlowID: f.flowID()})
	if admitted || cmd != nil {
		t.Fatalf("kind %v: admitted = %v, cmd != nil = %v, want a refusal", kind, admitted, cmd != nil)
	}
	return next.status.Text
}
