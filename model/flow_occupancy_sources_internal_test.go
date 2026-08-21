package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/flowlease"
	"github.com/approachcontrol/approach/sessions"
)

// One table per leaf source predicate from docs/flow-occupancy-matrix.md §1.
// These assert current behavior, including the parts that read as surprising:
// lease-read failure is occupancy everywhere (S2 is fail-closed), and a blank
// Flow ID is not occupancy in any of the trimmed predicates.

// TestTrackedFlowLeaseOccupiedSources covers S1 and S2. Five of the six
// non-Free outcomes are error paths, and admission treats every one of them as
// occupancy, so the error cases are the load-bearing rows here.
func TestTrackedFlowLeaseOccupiedSources(t *testing.T) {
	inspectErr := errors.New("unsafe flow-leases directory")
	tests := []struct {
		name string
		// mutate rewrites the lease seam and its preconditions on the built
		// Model, which is the only place sessionStateRoot and the injection flag
		// can be varied without a second Options build.
		mutate       func(Model) Model
		flowID       string
		wantOccupied bool
		wantErr      string
	}{
		{
			name:   "free lease is not occupancy",
			mutate: func(m Model) Model { return m },
			flowID: "flow-1",
		},
		{
			name: "held lease is occupancy",
			mutate: func(m Model) Model {
				m.inspectFlowLease = func(string, string) (flowlease.LeaseState, error) { return flowlease.Held, nil }
				return m
			},
			flowID:       "flow-1",
			wantOccupied: true,
		},
		{
			name: "inspector error is reported, not swallowed",
			mutate: func(m Model) Model {
				m.inspectFlowLease = func(string, string) (flowlease.LeaseState, error) { return 0, inspectErr }
				return m
			},
			flowID:  "flow-1",
			wantErr: inspectErr.Error(),
		},
		{
			name: "nil inspector fails closed",
			mutate: func(m Model) Model {
				m.inspectFlowLease = nil
				return m
			},
			flowID:  "flow-1",
			wantErr: "Flow lease inspector is unavailable",
		},
		{
			// The injected inspector owns its own root contract, so a blank root
			// is not an error when a test or embedder supplied the inspector.
			name: "blank artifact root is tolerated for an injected inspector",
			mutate: func(m Model) Model {
				m.sessionStateRoot = ""
				return m
			},
			flowID: "flow-1",
		},
		{
			name: "blank artifact root fails closed for the production inspector",
			mutate: func(m Model) Model {
				m.sessionStateRoot = ""
				m.leaseInspectInjected = false
				return m
			},
			flowID:  "flow-1",
			wantErr: "Flow lease artifact root is unavailable",
		},
		{
			name: "an unrecognized lease state fails closed",
			mutate: func(m Model) Model {
				m.inspectFlowLease = func(string, string) (flowlease.LeaseState, error) {
					return flowlease.LeaseState(99), nil
				}
				return m
			},
			flowID:  "flow-1",
			wantErr: "invalid Flow lease state 99",
		},
		{
			// A blank Flow ID short-circuits before every check above, including
			// the ones that would otherwise fail closed.
			name: "blank Flow ID is neither occupancy nor an error",
			mutate: func(m Model) Model {
				m.inspectFlowLease = nil
				return m
			},
			flowID: "   ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t)
			occupied, err := tc.mutate(f.m).trackedFlowLeaseOccupied(tc.flowID)
			if occupied != tc.wantOccupied {
				t.Fatalf("occupied = %v, want %v", occupied, tc.wantOccupied)
			}
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("err = nil, want %q", tc.wantErr)
			case tc.wantErr != "" && err.Error() != tc.wantErr:
				t.Fatalf("err = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestFlowLeaseSetupErrorStatusWrapsTheInspectionError pins the one refusal
// string that is not a constant: it is a prefix plus the wrapped error text.
func TestFlowLeaseSetupErrorStatusWrapsTheInspectionError(t *testing.T) {
	const prefix = "Flow phase launch deferred because tracked tmux occupancy could not be inspected: "
	got := flowLeaseSetupErrorStatus(occupancyLeaseErr())
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("status = %q, want prefix %q", got, prefix)
	}
	if got != prefix+occupancyLeaseErrText {
		t.Fatalf("status = %q, want the wrapped error appended verbatim", got)
	}
}

// TestFlowLaunchAttemptOccupancySources covers S3 and its companion S4. Matching
// is on the exact canonical Flow ID, which is the contract that stops
// prefix-like IDs from colliding.
func TestFlowLaunchAttemptOccupancySources(t *testing.T) {
	tests := []struct {
		name         string
		sources      []occupancySource
		query        string
		wantOccupied bool
		wantKind     flowLaunchKind
	}{
		{name: "no attempt", query: "flow-1"},
		{
			name:         "manual phase attempt",
			sources:      []occupancySource{srcAttemptManualPhase},
			query:        "flow-1",
			wantOccupied: true,
			wantKind:     flowLaunchKindManualPhase,
		},
		{
			name:         "repair attempt",
			sources:      []occupancySource{srcAttemptRepair},
			query:        "flow-1",
			wantOccupied: true,
			wantKind:     flowLaunchKindRepair,
		},
		{
			// Surrounding whitespace on the query is trimmed before the lookup,
			// so the same attempt still answers.
			name:         "whitespace around the query still matches",
			sources:      []occupancySource{srcAttemptRepair},
			query:        "  flow-1  ",
			wantOccupied: true,
			wantKind:     flowLaunchKindRepair,
		},
		{
			name:    "blank Flow ID is not occupancy",
			sources: []occupancySource{srcAttemptRepair},
			query:   "   ",
		},
		{
			// The stated contract: an ID that merely has the held one as a
			// prefix is a different Flow.
			name:    "prefix-like Flow ID does not collide",
			sources: []occupancySource{srcAttemptRepair},
			query:   "flow-11",
		},
		{
			name:    "a different Flow ID does not collide",
			sources: []occupancySource{srcAttemptRepair},
			query:   "flow-2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			if got := f.m.flowLaunchAttemptOccupied(tc.query); got != tc.wantOccupied {
				t.Fatalf("flowLaunchAttemptOccupied(%q) = %v, want %v", tc.query, got, tc.wantOccupied)
			}
			if got := f.m.flowLaunchAttemptKind(tc.query); got != tc.wantKind {
				t.Fatalf("flowLaunchAttemptKind(%q) = %v, want %v", tc.query, got, tc.wantKind)
			}
		})
	}
}

// TestFlowLaunchAttemptKindIsZeroForATokenlessAttempt pins the other way an
// attempt entry fails to count: the map holds it, but its token is blank, so
// every lookup treats the Flow as unheld.
func TestFlowLaunchAttemptKindIsZeroForATokenlessAttempt(t *testing.T) {
	f := newOccupancyFixture(t)
	m := f.m
	m.flowLaunchAttempts = map[string]flowLaunchAttempt{
		f.flowID(): {Kind: flowLaunchKindRepair, FlowID: f.flowID(), State: flowLaunchStateReading},
	}
	if m.flowLaunchAttemptOccupied(f.flowID()) {
		t.Fatal("an attempt with no token must not occupy the Flow")
	}
	if got := m.flowLaunchAttemptKind(f.flowID()); got != 0 {
		t.Fatalf("flowLaunchAttemptKind = %v, want the zero kind", got)
	}
}

// TestFlowEmbeddedTerminalPredicatesOverlapRatherThanNest is F3 made into data.
// One table over the four (Terminal != nil × FlowRepair) combinations, asserting
// both predicates on every row, because previewPhaseResume's `∧¬` depends on the
// overlap being exactly this shape.
func TestFlowEmbeddedTerminalPredicatesOverlapRatherThanNest(t *testing.T) {
	tests := []struct {
		name       string
		terminal   bool
		repair     bool
		wantFlow   bool
		wantRepair bool
	}{
		{name: "no slot at all"},
		{name: "live terminal, not repair", terminal: true, wantFlow: true},
		{name: "repair slot with no terminal", repair: true, wantRepair: true},
		{name: "repair slot with a live terminal", terminal: true, repair: true, wantFlow: true, wantRepair: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t)
			m := f.m
			if tc.terminal || tc.repair {
				slot := embeddedTerminalSlot{
					Scope:      embeddedTerminalScopeFlow,
					FlowID:     f.flowID(),
					FlowRepair: tc.repair,
				}
				if tc.terminal {
					slot.Terminal = flowPhaseLaunchTestTerminal{state: "running"}
				}
				m.embeddedTerminals = append(m.embeddedTerminals, slot)
			}
			if got := m.hasFlowEmbeddedTerminalForFlow(f.flowID()); got != tc.wantFlow {
				t.Fatalf("hasFlowEmbeddedTerminalForFlow = %v, want %v", got, tc.wantFlow)
			}
			if got := m.hasFlowRepairEmbeddedTerminalForFlow(f.flowID()); got != tc.wantRepair {
				t.Fatalf("hasFlowRepairEmbeddedTerminalForFlow = %v, want %v", got, tc.wantRepair)
			}
		})
	}
}

// TestFlowEmbeddedTerminalPredicatesScopeAndTrimming pins the two ways a slot
// can exist and still not answer: a non-Flow scope, and a Flow ID that is not
// the one asked about. The two predicates diverge on trimming, and that
// divergence is current behavior rather than a decision either way.
func TestFlowEmbeddedTerminalPredicatesScopeAndTrimming(t *testing.T) {
	f := newOccupancyFixture(t, srcFlowTerminal, srcRepairSlot)

	if f.m.hasFlowEmbeddedTerminalForFlow("flow-2") || f.m.hasFlowRepairEmbeddedTerminalForFlow("flow-2") {
		t.Fatal("a slot on one Flow must not answer for another")
	}
	if f.m.hasFlowEmbeddedTerminalForFlow("  ") || f.m.hasFlowRepairEmbeddedTerminalForFlow("  ") {
		t.Fatal("a blank Flow ID is not occupancy")
	}
	// hasFlowRepairEmbeddedTerminalForFlow trims its argument; the broad
	// predicate compares it as given. Both are asserted so a migration that
	// unified them has to do so deliberately.
	if f.m.hasFlowEmbeddedTerminalForFlow("  flow-1  ") {
		t.Fatal("hasFlowEmbeddedTerminalForFlow compares the Flow ID as given")
	}
	if !f.m.hasFlowRepairEmbeddedTerminalForFlow("  flow-1  ") {
		t.Fatal("hasFlowRepairEmbeddedTerminalForFlow trims before comparing")
	}

	scoped := newOccupancyFixture(t)
	m := scoped.m
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Scope:      embeddedTerminalScopeSession,
		FlowID:     scoped.flowID(),
		FlowRepair: true,
		Terminal:   flowPhaseLaunchTestTerminal{state: "running"},
	})
	if m.hasFlowEmbeddedTerminalForFlow(scoped.flowID()) || m.hasFlowRepairEmbeddedTerminalForFlow(scoped.flowID()) {
		t.Fatal("a session-scoped slot must not occupy the Flow")
	}
}

// TestFlowHeadlessWritePendingComparesTheFlowIDUntrimmed is S14, and it is the
// odd one out: pendingFlowHeadlessWriteIndex compares the raw string where every
// neighbouring predicate trims first. Pinned deliberately.
func TestFlowHeadlessWritePendingComparesTheFlowIDUntrimmed(t *testing.T) {
	f := newOccupancyFixture(t, srcHeadlessWrite)

	if !f.m.flowHeadlessWritePending(f.flowID()) {
		t.Fatal("an in-flight headless write should be pending for its own Flow")
	}
	if f.m.flowHeadlessWritePending("  " + f.flowID() + "  ") {
		t.Fatal("flowHeadlessWritePending compares the Flow ID untrimmed")
	}
	if f.m.flowHeadlessWritePending("flow-2") {
		t.Fatal("a write on one Flow must not be pending for another")
	}
	if f.m.flowHeadlessWritePending("") {
		t.Fatal("no write is enqueued under a blank Flow ID")
	}
	// markFlowHeadlessWritePending refuses a blank Flow ID outright, so the
	// untrimmed comparison can never be reached through an empty key.
	if newOccupancyFixture(t).m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{}).flowHeadlessWritePending("") {
		t.Fatal("a blank-Flow pending write must not be recorded at all")
	}
}

// TestHasKnownActiveFlowSessionReadsBothMirrors is S12. It is the only source
// the worktree agent reads and nothing else does, and it unions the Flow session
// pane with the worktree session pane.
func TestHasKnownActiveFlowSessionReadsBothMirrors(t *testing.T) {
	tests := []struct {
		name  string
		build func(occupancyFixture, Model) Model
		query string
		want  bool
	}{
		{name: "empty mirrors", build: func(_ occupancyFixture, m Model) Model { return m }, query: "flow-1"},
		{
			name: "active session in the Flow mirror",
			build: func(f occupancyFixture, m Model) Model {
				return m.withOccupancySessionMirror(f.flowID())
			},
			query: "flow-1",
			want:  true,
		},
		{
			name: "active session in the worktree mirror",
			build: func(f occupancyFixture, m Model) Model {
				m.worktreeSessions = m.worktreeSessions.SetItems([]sessions.SessionRecord{{
					FlowID: f.flowID(), Status: "active",
				}})
				return m
			},
			query: "flow-1",
			want:  true,
		},
		{
			// The mirror's own Flow ID is trimmed before comparison, unlike S14.
			name: "the mirrored Flow ID is trimmed before comparison",
			build: func(f occupancyFixture, m Model) Model {
				m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{
					FlowID: "  " + f.flowID() + "  ", Status: "active",
				}})
				return m
			},
			query: "flow-1",
			want:  true,
		},
		{
			name: "ended session is not occupancy",
			build: func(f occupancyFixture, m Model) Model {
				m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{
					FlowID: f.flowID(), Status: "ended",
				}})
				return m
			},
			query: "flow-1",
		},
		{
			name: "another Flow's active session is not occupancy",
			build: func(_ occupancyFixture, m Model) Model {
				m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{FlowID: "flow-2", Status: "active"}})
				return m
			},
			query: "flow-1",
		},
		{
			name: "blank Flow ID is not occupancy",
			build: func(f occupancyFixture, m Model) Model {
				return m.withOccupancySessionMirror(f.flowID())
			},
			query: "  ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t)
			if got := tc.build(f, f.m).hasKnownActiveFlowSession(tc.query); got != tc.want {
				t.Fatalf("hasKnownActiveFlowSession(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestFlowLaunchRuntimeOccupiedTruthTable is the S3 ∨ S6 ∨ S7 composite, over
// every combination of its three disjuncts.
func TestFlowLaunchRuntimeOccupiedTruthTable(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    bool
	}{
		{name: "nothing holds the Flow"},
		{name: "attempt only", sources: []occupancySource{srcAttemptManualPhase}, want: true},
		{name: "Flow terminal only", sources: []occupancySource{srcFlowTerminal}, want: true},
		{name: "repair slot only", sources: []occupancySource{srcRepairSlot}, want: true},
		{name: "attempt and terminal", sources: []occupancySource{srcAttemptManualPhase, srcFlowTerminal}, want: true},
		{name: "terminal and repair slot", sources: []occupancySource{srcFlowTerminal, srcRepairSlot}, want: true},
		{name: "all three", sources: []occupancySource{srcAttemptRepair, srcFlowTerminal, srcRepairSlot}, want: true},
		// The lease is not a runtime term; this is the whole of D2.
		{name: "held lease is not runtime occupancy", sources: []occupancySource{srcLeaseHeld}},
		{name: "lease error is not runtime occupancy", sources: []occupancySource{srcLeaseError}},
		{name: "headless write is not runtime occupancy", sources: []occupancySource{srcHeadlessWrite}},
		{name: "running phase is not runtime occupancy", sources: []occupancySource{srcRunningPhase}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			if got := f.m.flowLaunchRuntimeOccupied(f.flowID()); got != tc.want {
				t.Fatalf("flowLaunchRuntimeOccupied = %v, want %v", got, tc.want)
			}
			if f.m.flowLaunchRuntimeOccupied("  ") {
				t.Fatal("a blank Flow ID is not runtime occupancy")
			}
		})
	}
}

// TestFlowLaunchAdmissionOccupiedTruthTable is S2 ∨ S1 ∨ runtime. Both lease
// outcomes collapse into the same boolean here — that collapse is D1, and it is
// why five admissions read the lease typed instead.
func TestFlowLaunchAdmissionOccupiedTruthTable(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    bool
	}{
		{name: "nothing holds the Flow"},
		{name: "held lease", sources: []occupancySource{srcLeaseHeld}, want: true},
		{name: "unreadable lease", sources: []occupancySource{srcLeaseError}, want: true},
		{name: "runtime attempt", sources: []occupancySource{srcAttemptAutofix}, want: true},
		{name: "runtime terminal", sources: []occupancySource{srcFlowTerminal}, want: true},
		{name: "runtime repair slot", sources: []occupancySource{srcRepairSlot}, want: true},
		{name: "lease and runtime together", sources: []occupancySource{srcLeaseHeld, srcFlowTerminal}, want: true},
		// Neither the headless write nor the persisted record is part of the
		// composite; every consumer that wants them adds them itself.
		{name: "headless write alone", sources: []occupancySource{srcHeadlessWrite}},
		{name: "running phase alone", sources: []occupancySource{srcRunningPhase}},
		{name: "phase session alone", sources: []occupancySource{srcPhaseSession}},
		{name: "session mirror alone", sources: []occupancySource{srcSessionMirror}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			if got := f.m.flowLaunchAdmissionOccupied(f.flowID()); got != tc.want {
				t.Fatalf("flowLaunchAdmissionOccupied = %v, want %v", got, tc.want)
			}
			// A blank Flow ID returns before the lease is inspected, so even a
			// fail-closed inspector cannot make it occupancy.
			if f.m.flowLaunchAdmissionOccupied("   ") {
				t.Fatal("a blank Flow ID is not admission occupancy")
			}
		})
	}
}

// TestFlowRecordHasOtherRunningPhaseExcludesTheCandidate is S10 minus the
// candidate: a running candidate is staleness, which the caller classifies
// first, so only some other running phase counts.
func TestFlowRecordHasOtherRunningPhaseExcludesTheCandidate(t *testing.T) {
	record := occupancyFlowRecord()
	record.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1},
		{PhaseID: "implementation", Status: flowstore.PhaseRunning, Order: 2},
		{PhaseID: "review-loop", Status: flowstore.PhaseReady, Order: 3},
	}
	tests := []struct {
		name      string
		candidate string
		want      bool
	}{
		{name: "the running phase is the candidate", candidate: "implementation"},
		{name: "a different phase is the candidate", candidate: "review-loop", want: true},
		{name: "no candidate at all", candidate: "", want: true},
		// Phase IDs are normalized on both sides before comparison.
		{name: "the candidate is spelled differently", candidate: "  Implementation  "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := flowRecordHasOtherRunningPhase(record, tc.candidate); got != tc.want {
				t.Fatalf("flowRecordHasOtherRunningPhase(%q) = %v, want %v", tc.candidate, got, tc.want)
			}
		})
	}

	none := occupancyFlowRecord()
	if flowRecordHasOtherRunningPhase(none, "implementation") {
		t.Fatal("a record with no running phase never has another one")
	}
}
