package model

import (
	"testing"

	"github.com/approachcontrol/approach/flowownership"
)

// The ordered refusals of docs/flow-occupancy-matrix.md §4. F2 records that four
// ladders exist and none shares a table; these tests give each one a table, and
// every rank is asserted both alone and against a lower rank installed at the
// same time, because a ladder that only ever sees one source cannot be shown to
// be ordered at all.

// TestFlowRepairOccupancyRefusalLadder is §4.1. It exercises the module verdict
// and the model-owned rendering together so neither side can silently reorder
// or rename a refusal.
func TestFlowRepairOccupancyRefusalLadder(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    string
	}{
		{name: "lease unreadable above the runtime ladder", sources: []occupancySource{srcLeaseError, srcAttemptRepair}, want: flowLeaseSetupErrorStatus(occupancyLeaseErr())},
		{name: "held lease above the runtime ladder", sources: []occupancySource{srcLeaseHeld, srcAttemptRepair}, want: flowLeaseOccupiedStatus},
		// Rank 1–3 key on the attempt's kind.
		{name: "rank 1: repair attempt", sources: []occupancySource{srcAttemptRepair}, want: flowRepairPendingStatus},
		{name: "rank 2: phase resume attempt", sources: []occupancySource{srcAttemptPhaseResume}, want: flowRepairResumePendingStatus},
		{name: "rank 3: manual phase attempt", sources: []occupancySource{srcAttemptManualPhase}, want: flowRepairPhasePendingStatus},
		{name: "rank 3: auto phase attempt", sources: []occupancySource{srcAttemptAutoPhase}, want: flowRepairPhasePendingStatus},
		// Rank 4 is the fallback for every other attempt kind and for the slots.
		{name: "rank 4: autofix attempt", sources: []occupancySource{srcAttemptAutofix}, want: flowRepairTerminalStatus},
		{name: "rank 4: worktree agent attempt", sources: []occupancySource{srcAttemptWorktreeAgent}, want: flowRepairTerminalStatus},
		{name: "rank 4: Flow terminal", sources: []occupancySource{srcFlowTerminal}, want: flowRepairTerminalStatus},
		{name: "rank 4: terminal-less repair slot", sources: []occupancySource{srcRepairSlot}, want: flowRepairTerminalStatus},
		{name: "rank 5: pending headless write", sources: []occupancySource{srcHeadlessWrite}, want: flowHeadlessWritePendingStatus},

		// Precedence: each rank over a lower one installed simultaneously.
		{
			name:    "rank 1 over rank 4",
			sources: []occupancySource{srcAttemptRepair, srcFlowTerminal, srcRepairSlot},
			want:    flowRepairPendingStatus,
		},
		{
			name:    "rank 2 over rank 4",
			sources: []occupancySource{srcAttemptPhaseResume, srcFlowTerminal},
			want:    flowRepairResumePendingStatus,
		},
		{
			name:    "rank 3 over rank 4",
			sources: []occupancySource{srcAttemptManualPhase, srcFlowTerminal},
			want:    flowRepairPhasePendingStatus,
		},
		{
			name:    "rank 4 over rank 5",
			sources: []occupancySource{srcFlowTerminal, srcHeadlessWrite},
			want:    flowRepairTerminalStatus,
		},
		{
			name:    "rank 1 over every lower rank at once",
			sources: []occupancySource{srcAttemptRepair, srcFlowTerminal, srcRepairSlot, srcHeadlessWrite},
			want:    flowRepairPendingStatus,
		},
		{name: "free Flow has no refusal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			verdict := f.m.repairOccupancy(f.flowID(), flowownership.StageAdmission)
			if got := flowRepairOccupancyStatus(verdict); got != tc.want {
				t.Fatalf("flowRepairOccupancyStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAutofixOccupancyLadderPrecedence extends the existing single-source table
// (autofixOccupancyCases) rather than restating it: those rows already pin every
// rank alone, so this adds only the rows they lack — two sources at once, where
// the order is the whole point.
func TestAutofixOccupancyLadderPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    string
	}{
		{
			// The lease outranks the whole inline ladder, and it is answered
			// typed so a peer race is not collapsed into the in-flight status.
			name:    "lease over a repair attempt",
			sources: []occupancySource{srcLeaseHeld, srcAttemptRepair},
			want:    flowLeaseOccupiedStatus,
		},
		{
			name:    "unreadable lease over a Flow terminal",
			sources: []occupancySource{srcLeaseError, srcFlowTerminal},
			want:    flowLeaseSetupErrorStatus(occupancyLeaseErr()),
		},
		{
			name:    "repair attempt over a Flow terminal",
			sources: []occupancySource{srcAttemptRepair, srcFlowTerminal},
			want:    flowRepairPendingStatus,
		},
		{
			// The literal is inline in production rather than a constant, so it
			// is asserted as a literal: a copy edit there has to fail here.
			name:    "phase resume attempt over a repair slot",
			sources: []occupancySource{srcAttemptPhaseResume, srcRepairSlot},
			want:    "A phase resume is already pending for this Flow",
		},
		{
			// A manual attempt does not reach the terminal rung's name: the
			// terminal is checked first, so the slot is what gets reported.
			name:    "Flow terminal over a manual phase attempt",
			sources: []occupancySource{srcFlowTerminal, srcAttemptManualPhase},
			want:    flowAutofixTerminalStatus,
		},
		{
			name:    "terminal-less repair slot still outranks the generic in-flight status",
			sources: []occupancySource{srcRepairSlot, srcAttemptManualPhase},
			want:    flowAutofixTerminalStatus,
		},
		{
			name:    "a bare attempt reports the generic in-flight status",
			sources: []occupancySource{srcAttemptManualPhase, srcHeadlessWrite},
			want:    flowAutofixInFlightStatus,
		},
		{
			// Unlike repair's rank 5, autofix's headless rung is a real test, so
			// it is reached only when nothing durable holds the Flow.
			name:    "headless write is last",
			sources: []occupancySource{srcHeadlessWrite},
			want:    flowHeadlessWritePendingStatus,
		},
	}
	record := autofixFlowRecord()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixtureFor(t, record, tc.sources...)
			next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindAutofix,
				FlowID: record.FlowID,
			})
			if admitted || cmd != nil {
				t.Fatalf("admitted = %v, cmd != nil = %v, want a refusal that starts no work", admitted, cmd != nil)
			}
			if got := next.status.Text; got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWorktreeAgentOccupancyLadder is §4.4, the only admission that reads every
// source family. Each rank is asserted alone and then over the next one down.
func TestWorktreeAgentOccupancyLadder(t *testing.T) {
	const (
		runningPhaseReason = "a running phase implementation already occupies this Flow"
		phaseSessionReason = "an active persisted session on phase implementation already occupies this Flow"
	)
	tests := []struct {
		name    string
		sources []occupancySource
		want    string
	}{
		{name: "rank 1: unreadable lease", sources: []occupancySource{srcLeaseError}, want: flowLeaseSetupErrorStatus(occupancyLeaseErr())},
		{name: "rank 1: held lease", sources: []occupancySource{srcLeaseHeld}, want: flowLeaseOccupiedStatus},
		{name: "rank 2: lifecycle attempt", sources: []occupancySource{srcAttemptManualPhase}, want: flowWorktreeAgentPendingStatus},
		{name: "rank 3: live autofix tmux window", sources: []occupancySource{srcTmuxAutofixWindow}, want: flowWorktreeAgentPendingStatus},
		{name: "rank 4: Flow terminal", sources: []occupancySource{srcFlowTerminal}, want: flowWorktreeAgentSlotStatus},
		{name: "rank 4: terminal-less repair slot", sources: []occupancySource{srcRepairSlot}, want: flowWorktreeAgentSlotStatus},
		{name: "rank 5: session mirror", sources: []occupancySource{srcSessionMirror}, want: flowWorktreeAgentSessionStatus},
		{name: "rank 6: persisted running phase", sources: []occupancySource{srcRunningPhase}, want: runningPhaseReason},
		{name: "rank 6: session-attached phase", sources: []occupancySource{srcPhaseSession}, want: phaseSessionReason},

		{
			name:    "rank 1 over rank 2",
			sources: []occupancySource{srcLeaseHeld, srcAttemptManualPhase},
			want:    flowLeaseOccupiedStatus,
		},
		{
			name:    "rank 2 over rank 3",
			sources: []occupancySource{srcAttemptManualPhase, srcTmuxAutofixWindow},
			want:    flowWorktreeAgentPendingStatus,
		},
		{
			// Ranks 2 and 3 answer with the same string, so the rung that
			// separates them is asserted by installing the tmux window with a
			// slot below it instead.
			name:    "rank 3 over rank 4",
			sources: []occupancySource{srcTmuxAutofixWindow, srcFlowTerminal},
			want:    flowWorktreeAgentPendingStatus,
		},
		{
			name:    "rank 4 over rank 5",
			sources: []occupancySource{srcRepairSlot, srcSessionMirror},
			want:    flowWorktreeAgentSlotStatus,
		},
		{
			name:    "rank 5 over rank 6",
			sources: []occupancySource{srcSessionMirror, srcRunningPhase},
			want:    flowWorktreeAgentSessionStatus,
		},
		{
			name:    "the whole ladder at once reports the lease",
			sources: []occupancySource{srcLeaseHeld, srcAttemptManualPhase, srcFlowTerminal, srcSessionMirror, srcRunningPhase},
			want:    flowLeaseOccupiedStatus,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindWorktreeAgent,
				FlowID: f.flowID(),
			})
			if admitted || cmd != nil {
				t.Fatalf("admitted = %v, cmd != nil = %v, want a refusal that starts no work", admitted, cmd != nil)
			}
			if got := next.status.Text; got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
			// Whatever already held the Flow may still hold it; what must not
			// exist is a worktree-agent attempt this refusal created.
			if next.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindWorktreeAgent {
				t.Fatal("a refused admission must reserve nothing of its own")
			}
		})
	}
}

// TestWorktreeAgentAdmissionAdmitsAnUnoccupiedFlow is the ladder's control row:
// without it, every case above could pass because the fixture never admits.
func TestWorktreeAgentAdmissionAdmitsAnUnoccupiedFlow(t *testing.T) {
	f := newOccupancyFixture(t)
	next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindWorktreeAgent,
		FlowID: f.flowID(),
	})
	if !admitted || cmd == nil {
		t.Fatalf("admitted = %v, cmd != nil = %v, want an admitted launch", admitted, cmd != nil)
	}
	if got := next.status.Text; got != "" {
		t.Fatalf("status = %q, want silence on an admitted launch", got)
	}
	if !next.flowLaunchAttemptOccupied(f.flowID()) {
		t.Fatal("an admitted launch reserves the Flow")
	}
}

// TestFlowLaunchEmbeddedBackstopPerKind is §4.2: seven kind groups over the four
// (S6, S7) states. Admission makes every branch unreachable in production, and
// the backstop is still tested directly, per its own comment — dropping it would
// be a regression against a future unguarded source.
func TestFlowLaunchEmbeddedBackstopPerKind(t *testing.T) {
	const (
		createPhaseText   = "Flow creation launch canceled because a terminal is already open for this Flow"
		savedResumeText   = "Saved session resume canceled because an embedded terminal already occupies this Flow"
		phaseResumeText   = "Flow phase resume canceled because a repair terminal is already open for this Flow"
		manualLaunchText  = "Flow phase launch canceled because a repair terminal is already open for this Flow"
		neitherSlotStates = "neither slot"
	)
	slotStates := []struct {
		name    string
		sources []occupancySource
		// s6 and s7 name which predicate the state satisfies, which is what the
		// per-kind expectations below are written against.
		s6, s7 bool
	}{
		{name: neitherSlotStates},
		{name: "Flow terminal only", sources: []occupancySource{srcFlowTerminal}, s6: true},
		{name: "repair slot only", sources: []occupancySource{srcRepairSlot}, s7: true},
		{name: "both slots", sources: []occupancySource{srcFlowTerminal, srcRepairSlot}, s6: true, s7: true},
	}
	kinds := []struct {
		name string
		kind flowLaunchKind
		// refusal returns the expected text for a (S6, S7) state, or "" when the
		// kind admits it.
		refusal func(s6, s7 bool) string
	}{
		{
			name: "createPhase reads both slots",
			kind: flowLaunchKindCreatePhase,
			refusal: func(s6, s7 bool) string {
				if s6 || s7 {
					return createPhaseText
				}
				return ""
			},
		},
		{
			name: "worktreeAgent reads both slots",
			kind: flowLaunchKindWorktreeAgent,
			refusal: func(s6, s7 bool) string {
				if s6 || s7 {
					return flowWorktreeAgentSlotStatus
				}
				return ""
			},
		},
		{
			// The one kind that reads S6 alone: a terminal-less repair slot gets
			// past it, and that is current behavior.
			name: "savedSessionResume reads S6 only",
			kind: flowLaunchKindSavedSessionResume,
			refusal: func(s6, _ bool) string {
				if s6 {
					return savedResumeText
				}
				return ""
			},
		},
		{
			name: "repair reads both slots",
			kind: flowLaunchKindRepair,
			refusal: func(s6, s7 bool) string {
				if s6 || s7 {
					return flowRepairTerminalStatus
				}
				return ""
			},
		},
		{
			name: "phaseResume reads S7 only",
			kind: flowLaunchKindPhaseResume,
			refusal: func(_, s7 bool) string {
				if s7 {
					return phaseResumeText
				}
				return ""
			},
		},
		{
			name: "autofix reads S7 only and names no phase",
			kind: flowLaunchKindAutofix,
			refusal: func(_, s7 bool) string {
				if s7 {
					return flowAutofixCanceledStatus
				}
				return ""
			},
		},
		{
			name: "manualPhase reads S7 only",
			kind: flowLaunchKindManualPhase,
			refusal: func(_, s7 bool) string {
				if s7 {
					return manualLaunchText
				}
				return ""
			},
		},
		{
			name: "autoPhase shares manualPhase's branch",
			kind: flowLaunchKindAutoPhase,
			refusal: func(_, s7 bool) string {
				if s7 {
					return manualLaunchText
				}
				return ""
			},
		},
	}
	for _, kind := range kinds {
		for _, state := range slotStates {
			t.Run(kind.name+"/"+state.name, func(t *testing.T) {
				f := newOccupancyFixture(t, state.sources...)
				want := kind.refusal(state.s6, state.s7)
				direct := flowownership.Evaluate(flowownership.Sources{
					Runtime: flowOccupancyRuntime{model: f.m},
				}, flowownership.Query{
					FlowID: f.flowID(),
					Purpose: flowownership.Purpose{
						Role:  flowLaunchRole(kind.kind),
						Stage: flowownership.StageInstall,
					},
				})
				install := f.m.flowLaunchInstallOccupancy(kind.kind, f.flowID())
				if install.Occupied() != direct.Occupied() || install.Holder() != direct.Holder() || install.Err() != direct.Err() {
					t.Fatalf("install verdict = (%v, %s, %v), direct StageInstall verdict = (%v, %s, %v)",
						install.Occupied(), install.Holder(), install.Err(), direct.Occupied(), direct.Holder(), direct.Err())
				}
				status, refused := f.m.flowLaunchEmbeddedBackstop(kind.kind, f.flowID())
				if refused != direct.Occupied() {
					t.Fatalf("backstop refused = %v, direct StageInstall occupied = %v", refused, direct.Occupied())
				}
				if refused != (want != "") {
					t.Fatalf("refused = %v, want %v", refused, want != "")
				}
				if status != want {
					t.Fatalf("status = %q, want %q", status, want)
				}
			})
		}
	}
}

func TestFlowLaunchEmbeddedBackstopFailsClosedForInvalidInstallQuery(t *testing.T) {
	f := newOccupancyFixture(t)
	tests := []struct {
		name   string
		kind   flowLaunchKind
		flowID string
	}{
		{name: "unknown launch kind", kind: flowLaunchKind(255), flowID: f.flowID()},
		{name: "blank Flow ID", kind: flowLaunchKindManualPhase},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, refused := f.m.flowLaunchEmbeddedBackstop(tc.kind, tc.flowID); !refused {
				t.Fatal("invalid install query was not refused")
			}
		})
	}
}
