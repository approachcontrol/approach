package model

import (
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
)

// The composed admissions of §2.1, the previews and footers of §2.3, and the
// AutoMode drain of §2.4. Each admission is cross-checked against the footer
// predicate that advertises its key, because "what the footer advertises and
// what the key accepts can never disagree" is asserted in production comments
// but not everywhere in tests — and D4 records three places where they are
// deliberately allowed to disagree.

// TestManualPhaseAdmissionAndFooterAgree covers `g`. The headless rung is first
// in both, so the footer withdraws for exactly the reasons admission refuses.
func TestManualPhaseAdmissionAndFooterAgree(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    string
	}{
		{name: "unoccupied Flow admits", want: ""},
		{name: "pending headless write", sources: []occupancySource{srcHeadlessWrite}, want: flowHeadlessWritePendingStatus},
		{name: "held lease", sources: []occupancySource{srcLeaseHeld}, want: flowLeaseOccupiedStatus},
		{name: "unreadable lease", sources: []occupancySource{srcLeaseError}, want: flowLeaseSetupErrorStatus(occupancyLeaseErr())},
		{name: "competing attempt", sources: []occupancySource{srcAttemptRepair}, want: noLaunchableFlowPhaseStatus},
		{name: "Flow terminal", sources: []occupancySource{srcFlowTerminal}, want: `Close, detach, or dismiss Flow terminal "flow" before launching this Flow`},
		{name: "Flow terminal over a competing attempt", sources: []occupancySource{srcAttemptManualPhase, srcFlowTerminal}, want: `Close, detach, or dismiss Flow terminal "flow" before launching this Flow`},
		{name: "Flow terminal over an attempt and repair slot", sources: []occupancySource{srcAttemptManualPhase, srcFlowTerminal, srcRepairSlot}, want: `Close, detach, or dismiss Flow terminal "flow" before launching this Flow`},
		{name: "terminal-less repair slot", sources: []occupancySource{srcRepairSlot}, want: noLaunchableFlowPhaseStatus},
		{
			// The headless rung is checked before the lease, so it wins even
			// though the lease is the more durable obstacle.
			name:    "headless write over a held lease",
			sources: []occupancySource{srcHeadlessWrite, srcLeaseHeld},
			want:    flowHeadlessWritePendingStatus,
		},
		{
			name:    "headless write over an unreadable lease",
			sources: []occupancySource{srcHeadlessWrite, srcLeaseError},
			want:    flowHeadlessWritePendingStatus,
		},
		{
			name:    "held lease over a runtime holder",
			sources: []occupancySource{srcLeaseHeld, srcFlowTerminal},
			want:    flowLeaseOccupiedStatus,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			wantAdmitted := tc.want == ""

			if got := f.m.selectedFlowHasLaunchablePhase(); got != wantAdmitted {
				t.Fatalf("selectedFlowHasLaunchablePhase = %v, want %v", got, wantAdmitted)
			}
			_, _, previewed := f.m.previewFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindManualPhase,
				FlowID: f.flowID(),
			})
			// previewFlowLaunch has no headless term of its own; the footer adds
			// it. Everything else the footer refuses, the preview refuses too.
			wantPreview := !f.m.flowLaunchAdmissionOccupied(f.flowID())
			if previewed != wantPreview {
				t.Fatalf("previewFlowLaunch ok = %v, want %v", previewed, wantPreview)
			}
			if wantPreview && !wantAdmitted && tc.want != flowHeadlessWritePendingStatus {
				t.Fatal("the preview may only outrun the footer for a pending headless write")
			}

			next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindManualPhase,
				FlowID: f.flowID(),
			})
			if admitted != wantAdmitted || (cmd != nil) != wantAdmitted {
				t.Fatalf("admitted = %v, cmd != nil = %v, want %v", admitted, cmd != nil, wantAdmitted)
			}
			if got := next.status.Text; got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestManualPhasePreviewAndFooterNeverProbeTmux(t *testing.T) {
	f := newOccupancyFixture(t, srcTmuxAutofixWindow)
	if !f.m.selectedFlowHasLaunchablePhase() {
		t.Fatal("tmux-only source withdrew the manual phase footer")
	}
	if _, _, ok := f.m.previewFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: f.flowID()}); !ok {
		t.Fatal("tmux-only source refused the manual phase preview")
	}
	if len(f.h.tmuxWindowProbes) != 0 {
		t.Fatalf("preview or footer invoked tmux probes: %#v", f.h.tmuxWindowProbes)
	}
}

func TestManualPhaseFooterSkipsLeaseForUnlaunchableFlow(t *testing.T) {
	f := newOccupancyFixtureFor(t, occupancyRepairFlowRecord())
	if f.m.selectedFlowHasLaunchablePhase() {
		t.Fatal("blocked Flow advertised a manual phase launch")
	}
	if f.h.leaseInspections != 0 {
		t.Fatalf("lease inspections = %d, want 0", f.h.leaseInspections)
	}
}

func TestManualPhaseAdmissionNamesRetainedTerminalAndAdmitsAfterRelease(t *testing.T) {
	f := newOccupancyFixture(t, srcFlowTerminal)
	f.m.embeddedTerminals[0].Identity = "implementation"
	next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: f.flowID()})
	if admitted || cmd != nil {
		t.Fatal("retained terminal admitted a launch")
	}
	const want = `Close, detach, or dismiss Flow terminal "implementation" before launching this Flow`
	if next.status.Text != want {
		t.Fatalf("status = %q, want %q", next.status.Text, want)
	}

	released := f.m
	released.embeddedTerminals = nil
	_, cmd, admitted = released.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: f.flowID()})
	if !admitted || cmd == nil {
		t.Fatalf("launch after release admitted = %v, cmd = %T", admitted, cmd)
	}
}

// TestAutoPhaseAdmissionRefusesInSilence is D3. Silence is a behavior, not an
// omission: the advance poll runs at 1 Hz and is view-independent, so a status
// would be repainted every second over whatever the user is looking at. A test
// that only checked the boolean would not catch a migration that started
// rendering text.
func TestAutoPhaseAdmissionRefusesInSilence(t *testing.T) {
	sourceSets := [][]occupancySource{
		{srcLeaseHeld},
		{srcLeaseError},
		{srcAttemptManualPhase},
		{srcFlowTerminal},
		{srcRepairSlot},
		{srcLeaseHeld, srcFlowTerminal, srcAttemptRepair},
	}
	for _, sources := range sourceSets {
		t.Run(occupancySourcesName(sources), func(t *testing.T) {
			f := newOccupancyFixture(t, sources...)
			// A status set before the refusal proves the refusal did not merely
			// leave the field empty; it never touched it.
			before := f.m.setStatus(statusOther, "user is looking at this")
			next, cmd, admitted := before.requestFlowLaunch(flowLaunchIntent{
				Kind:    flowLaunchKindAutoPhase,
				FlowID:  f.flowID(),
				PhaseID: f.phase().PhaseID,
			})
			if admitted || cmd != nil {
				t.Fatalf("admitted = %v, cmd != nil = %v, want a refusal", admitted, cmd != nil)
			}
			if got := next.status.Text; got != "user is looking at this" {
				t.Fatalf("status = %q, want the pre-existing status untouched", got)
			}
		})
	}
}

// TestAutoPhaseAdmissionIgnoresTheHeadlessWrite pins the other half of the auto
// admission's set: it reads flowLaunchAdmissionOccupied and nothing else, so a
// pending headless write does not stop it, where it stops `g`.
func TestAutoPhaseAdmissionIgnoresTheHeadlessWrite(t *testing.T) {
	f := newOccupancyFixture(t, srcHeadlessWrite)
	_, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
		Kind:    flowLaunchKindAutoPhase,
		FlowID:  f.flowID(),
		PhaseID: f.phase().PhaseID,
	})
	if !admitted || cmd == nil {
		t.Fatalf("admitted = %v, cmd != nil = %v, want an admitted auto launch", admitted, cmd != nil)
	}
}

// TestCreatePhaseAdmissionIgnoresTheLease is D2, the sharpest divergence in the
// matrix: a held lease refuses every other existing-Flow route and does not
// refuse a create, because creation allocates a brand-new Flow that remains
// embedded and unleased.
func TestCreatePhaseAdmissionIgnoresTheLease(t *testing.T) {
	tests := []struct {
		name        string
		sources     []occupancySource
		wantRefused bool
	}{
		{name: "held lease does not refuse a create", sources: []occupancySource{srcLeaseHeld}},
		{name: "unreadable lease does not refuse a create", sources: []occupancySource{srcLeaseError}},
		{name: "a competing attempt refuses", sources: []occupancySource{srcAttemptRepair}, wantRefused: true},
		{name: "a Flow terminal refuses", sources: []occupancySource{srcFlowTerminal}, wantRefused: true},
		{name: "a terminal-less repair slot refuses", sources: []occupancySource{srcRepairSlot}, wantRefused: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			create := flowLaunchCreateRequest{
				Presentation: flowLaunchCreatePresentation{Origin: flowLaunchOriginNewFlow, Request: 1},
				RepoPath:     f.record.RepoPath,
			}
			m := f.m.setStatus(statusOther, "leave this status alone")
			m.flowCreateReq.current = create.Presentation.Request

			next, _ := m.handleCreateFlowAllocated(flowLaunchEventMsg{
				Kind:   flowLaunchKindCreatePhase,
				Token:  "create-token",
				FlowID: f.flowID(),
				Create: create,
			})
			reserved := next.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindCreatePhase
			if reserved == tc.wantRefused {
				t.Fatalf("createPhase reserved = %v, want refused = %v", reserved, tc.wantRefused)
			}
			if tc.wantRefused {
				if next.status.Text != noLaunchableFlowPhaseStatus {
					t.Fatalf("status = %q, want %q", next.status.Text, noLaunchableFlowPhaseStatus)
				}
				return
			}
			attempt, ok := next.flowLaunchAttempt(f.flowID())
			if !ok || attempt.State != flowLaunchStateCreateSessionReading {
				t.Fatalf("attempt = %#v, ok = %v, want createSessionReading reservation", attempt, ok)
			}
			if next.status.Text != "leave this status alone" {
				t.Fatalf("status = %q, want the pre-existing status untouched", next.status.Text)
			}
		})
	}
}

func TestFlowOccupancyRuntimeClassifiesEveryLaunchKind(t *testing.T) {
	tests := []struct {
		kind flowLaunchKind
		want actions.FlowLaunchRole
	}{
		{kind: flowLaunchKindManualPhase, want: actions.RoleTrackedPhase},
		{kind: flowLaunchKindAutoPhase, want: actions.RoleTrackedPhase},
		{kind: flowLaunchKindCreatePhase, want: actions.RoleCreatePhase},
		{kind: flowLaunchKindPhaseResume, want: actions.RolePhaseResume},
		{kind: flowLaunchKindRepair, want: actions.RoleRepair},
		{kind: flowLaunchKindAutofix, want: actions.RoleAutofix},
		{kind: flowLaunchKindWorktreeAgent, want: actions.RoleWorktreeAgent},
		{kind: flowLaunchKindSavedSessionResume, want: actions.RoleSavedSessionResume},
	}
	for _, tc := range tests {
		t.Run(tc.want.String(), func(t *testing.T) {
			m := newOccupancyFixture(t).m
			m.flowLaunchAttempts = map[string]flowLaunchAttempt{
				"flow-1": {Token: "token", Kind: tc.kind, FlowID: "flow-1"},
			}
			role, ok := (flowOccupancyRuntime{model: m}).AttemptHolder("flow-1")
			if !ok || role != tc.want {
				t.Fatalf("AttemptHolder() = (%v, %v), want (%v, true)", role, ok, tc.want)
			}
		})
	}
}

// TestPhaseResumeAdmissionAndPreviewDisagreeByDesign is D4's first case. Resume
// refuses a competing attempt and an open repair terminal in silence, and its
// footer keeps advertising the key for both — withdrawing it would be a behavior
// change the survey records rather than makes.
func TestPhaseResumeAdmissionAndPreviewDisagreeByDesign(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		// wantStatus is "" for both an admitted resume and a silent refusal;
		// wantAdmitted is what tells those apart.
		wantAdmitted bool
		wantStatus   string
		wantPreview  bool
	}{
		{name: "unoccupied Flow", wantAdmitted: true, wantPreview: true},
		{name: "held lease", sources: []occupancySource{srcLeaseHeld}, wantStatus: flowLeaseOccupiedStatus},
		{
			name:       "unreadable lease",
			sources:    []occupancySource{srcLeaseError},
			wantStatus: flowLeaseSetupErrorStatus(occupancyLeaseErr()),
		},
		{
			// S6 ∧ ¬S7: the only runtime state resume names.
			name:       "Flow terminal with no repair slot",
			sources:    []occupancySource{srcFlowTerminal},
			wantStatus: flowPhaseResumeTerminalStatus,
		},
		{
			// A competing attempt refuses silently and the footer stays lit.
			name:        "competing attempt",
			sources:     []occupancySource{srcAttemptRepair},
			wantPreview: true,
		},
		{
			// So does a repair slot, whether or not it has a live terminal:
			// the status is scoped to the non-repair case by the conjunction.
			name:        "terminal-less repair slot",
			sources:     []occupancySource{srcRepairSlot},
			wantPreview: true,
		},
		{
			name:        "repair slot with a live terminal",
			sources:     []occupancySource{srcFlowTerminal, srcRepairSlot},
			wantPreview: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			if got := f.m.previewPhaseResume(f.flowID()); got != tc.wantPreview {
				t.Fatalf("previewPhaseResume = %v, want %v", got, tc.wantPreview)
			}
			next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
				Kind:    flowLaunchKindPhaseResume,
				FlowID:  f.flowID(),
				PhaseID: f.phase().PhaseID,
			})
			if admitted != tc.wantAdmitted || (cmd != nil) != tc.wantAdmitted {
				t.Fatalf("admitted = %v, cmd != nil = %v, want %v", admitted, cmd != nil, tc.wantAdmitted)
			}
			if got := next.status.Text; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}

// TestPreviewPhaseResumeIsTrueForABlankFlowID pins the preview's short circuit:
// with no Flow named there is nothing to be occupied, and the key stays lit.
func TestPreviewPhaseResumeIsTrueForABlankFlowID(t *testing.T) {
	f := newOccupancyFixture(t, srcLeaseHeld, srcFlowTerminal)
	if !f.m.previewPhaseResume("   ") {
		t.Fatal("previewPhaseResume answers true for a blank Flow ID regardless of what holds any Flow")
	}
}

// TestRepairFooterAddsTheHeadlessTermToThePreview is D4's second case: repair's
// footer occupancy set is wider than its preview's, because admission's
// occupancy set has no headless notion and the composite is shared with kinds
// that must not inherit one.
func TestRepairFooterAddsTheHeadlessTermToThePreview(t *testing.T) {
	tests := []struct {
		name        string
		sources     []occupancySource
		wantPreview bool
		wantFooter  bool
		wantStatus  string
	}{
		{name: "unoccupied repairable Flow", wantPreview: true, wantFooter: true},
		{
			// The headless write is invisible to the preview and withdraws the
			// footer, and admission refuses with the ladder's rank 5.
			name:        "pending headless write",
			sources:     []occupancySource{srcHeadlessWrite},
			wantPreview: true,
			wantStatus:  flowHeadlessWritePendingStatus,
		},
		{name: "held lease", sources: []occupancySource{srcLeaseHeld}, wantStatus: flowLeaseOccupiedStatus},
		{name: "Flow terminal", sources: []occupancySource{srcFlowTerminal}, wantStatus: flowRepairTerminalStatus},
		{name: "repair attempt", sources: []occupancySource{srcAttemptRepair}, wantStatus: flowRepairPendingStatus},
	}
	record := occupancyRepairFlowRecord()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixtureFor(t, record, tc.sources...)
			if _, ok := f.m.previewRepairLaunch(f.m.repairFlowLaunchIntent("")); ok != tc.wantPreview {
				t.Fatalf("previewRepairLaunch ok = %v, want %v", ok, tc.wantPreview)
			}
			if got := f.m.selectedFlowRepairReady(); got != tc.wantFooter {
				t.Fatalf("selectedFlowRepairReady = %v, want %v", got, tc.wantFooter)
			}
			next, cmd, admitted := f.m.requestFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindRepair,
				FlowID: f.flowID(),
			})
			wantAdmitted := tc.wantStatus == ""
			if admitted != wantAdmitted || (cmd != nil) != wantAdmitted {
				t.Fatalf("admitted = %v, cmd != nil = %v, want %v", admitted, cmd != nil, wantAdmitted)
			}
			if got := next.status.Text; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}

// TestAutofixFooterAddsTheHeadlessTermToTheComposite is D4's third case, the
// same shape as repair's: the footer is the composite plus S14.
func TestAutofixFooterAddsTheHeadlessTermToTheComposite(t *testing.T) {
	record := autofixFlowRecord()

	ready := newOccupancyFixtureFor(t, record)
	if !ready.m.selectedFlowAutofixReady() {
		t.Fatal("an unoccupied autofix-eligible Flow advertises U")
	}
	if ready.m.flowLaunchAdmissionOccupied(record.FlowID) {
		t.Fatal("the baseline fixture must not be occupied")
	}

	headless := newOccupancyFixtureFor(t, record, srcHeadlessWrite)
	if headless.m.flowLaunchAdmissionOccupied(record.FlowID) {
		t.Fatal("a headless write is not part of admission's occupancy set")
	}
	if headless.m.selectedFlowAutofixReady() {
		t.Fatal("the footer withdraws U for a pending headless write the composite cannot see")
	}
}

// TestSelectedFlowWorktreeAgentReadyMatchesItsAdmission is the footer half of
// §4.4. It is the only footer predicate that reads the session mirror, and the
// only one whose occupancy set is exactly its admission's minus the sources
// admission cannot answer from the Model.
func TestSelectedFlowWorktreeAgentReadyMatchesItsAdmission(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    bool
	}{
		{name: "unoccupied Flow", want: true},
		{name: "held lease", sources: []occupancySource{srcLeaseHeld}},
		{name: "unreadable lease", sources: []occupancySource{srcLeaseError}},
		{name: "competing attempt", sources: []occupancySource{srcAttemptManualPhase}},
		{name: "Flow terminal", sources: []occupancySource{srcFlowTerminal}},
		{name: "terminal-less repair slot", sources: []occupancySource{srcRepairSlot}},
		{name: "session mirror", sources: []occupancySource{srcSessionMirror}},
		{name: "running phase", sources: []occupancySource{srcRunningPhase}},
		{name: "session-attached phase", sources: []occupancySource{srcPhaseSession}},
		{
			// The footer does not probe tmux — it shells out — so a live autofix
			// window leaves the key advertised and the press refuses.
			name:    "live autofix tmux window leaves the footer lit",
			sources: []occupancySource{srcTmuxAutofixWindow},
			want:    true,
		},
		{
			// Nor does it read the headless write; only repair's and autofix's
			// footers add that term.
			name:    "pending headless write leaves the footer lit",
			sources: []occupancySource{srcHeadlessWrite},
			want:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			if got := f.m.selectedFlowWorktreeAgentReady(); got != tc.want {
				t.Fatalf("selectedFlowWorktreeAgentReady = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFlowAutoAdvanceOccupiedReadsFourSourcesAndNotTheOthers is §2.4's drain
// gate. The negatives are the point: it deliberately does not read the repair
// slot and does not shell out to tmux, because a poll on a timer must not.
func TestFlowAutoAdvanceOccupiedReadsFourSourcesAndNotTheOthers(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
		want    bool
	}{
		{name: "unoccupied Flow"},
		{name: "held lease", sources: []occupancySource{srcLeaseHeld}, want: true},
		{name: "unreadable lease", sources: []occupancySource{srcLeaseError}, want: true},
		{name: "lifecycle attempt", sources: []occupancySource{srcAttemptRepair}, want: true},
		{name: "persisted running phase", sources: []occupancySource{srcRunningPhase}, want: true},
		{name: "Flow terminal", sources: []occupancySource{srcFlowTerminal}, want: true},
		// The explicit negatives.
		{name: "a terminal-less repair slot does not occupy the drain", sources: []occupancySource{srcRepairSlot}},
		{name: "a live autofix tmux window does not occupy the drain", sources: []occupancySource{srcTmuxAutofixWindow}},
		{name: "a session-attached phase does not occupy the drain", sources: []occupancySource{srcPhaseSession}},
		{name: "the session mirror does not occupy the drain", sources: []occupancySource{srcSessionMirror}},
		{name: "a pending headless write does not occupy the drain", sources: []occupancySource{srcHeadlessWrite}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			if got := f.m.flowAutoAdvanceOccupied(f.record); got != tc.want {
				t.Fatalf("flowAutoAdvanceOccupied = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAutoAdvanceDrainDefersSilentlyAndStaysArmed pins the drain's own
// occupancy behavior: a deferral launches nothing, sets no status, and leaves
// the drain armed for the next poll. The session pre-filter is asserted the same
// way, because it is the reason a stalled phase does not cost a session-store
// walk every second.
func TestAutoAdvanceDrainDefersSilentlyAndStaysArmed(t *testing.T) {
	tests := []struct {
		name    string
		sources []occupancySource
	}{
		{name: "occupied by the drain's own gate", sources: []occupancySource{srcFlowTerminal}},
		{name: "deferred by the session pre-filter", sources: []occupancySource{srcPhaseSession}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, tc.sources...)
			record := f.record
			record.AutoMode = true

			m := f.m.armAutoAdvanceDrain(record.FlowID).setStatus(statusOther, "user is looking at this")
			next, cmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{record})
			if cmd != nil {
				t.Fatal("a deferred drain must start no work")
			}
			if got := next.status.Text; got != "user is looking at this" {
				t.Fatalf("status = %q, want the pre-existing status untouched", got)
			}
			if !next.autoAdvanceDrainArmed(record.FlowID) {
				t.Fatal("a deferral leaves the drain armed for the next poll")
			}
			if next.flowLaunchAttemptOccupied(record.FlowID) &&
				next.flowLaunchAttemptKind(record.FlowID) == flowLaunchKindAutoPhase {
				t.Fatal("a deferred drain must reserve nothing")
			}
		})
	}
}

// TestAutoAdvanceDrainIsDisarmedByARepairSlotOrMarker is the arm/disarm half of
// §2.4, and the one place S7 and S16 are read by the poll at all. The drain gate
// ignores both; the arming pass does not.
//
// Every row installs a held lease as well, so the drain gate defers in all of
// them. Without that the unoccupied row would disarm by launching, and the
// arm/disarm decision this test is about would be unobservable.
func TestAutoAdvanceDrainIsDisarmedByARepairSlotOrMarker(t *testing.T) {
	tests := []struct {
		name      string
		sources   []occupancySource
		wantArmed bool
	}{
		{name: "a deferred drain stays armed", wantArmed: true},
		{name: "an open repair slot disarms", sources: []occupancySource{srcRepairSlot}},
		{name: "an unconsumed repair outcome disarms", sources: []occupancySource{srcDrainMarker}},
		{
			// The arming pass reads S7 specifically, so a non-repair Flow
			// terminal is not a reason to disarm.
			name:      "a non-repair Flow terminal does not disarm",
			sources:   []occupancySource{srcFlowTerminal},
			wantArmed: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOccupancyFixture(t, append([]occupancySource{srcLeaseHeld}, tc.sources...)...)
			record := f.record
			record.AutoMode = true

			m := f.m.armAutoAdvanceDrain(record.FlowID)
			if !m.autoAdvanceDrainArmed(record.FlowID) {
				t.Fatal("the drain should start armed")
			}
			flows := []flowstore.FlowRecord{record}
			next, _, _ := m.prepareAutoFlowPhaseLaunch(flows, flows)
			if got := next.autoAdvanceDrainArmed(record.FlowID); got != tc.wantArmed {
				t.Fatalf("autoAdvanceDrainArmed = %v, want %v", got, tc.wantArmed)
			}
		})
	}
}

// occupancySourcesName renders a source set for a subtest name.
func occupancySourcesName(sources []occupancySource) string {
	if len(sources) == 0 {
		return "nothing"
	}
	name := sources[0].String()
	for _, source := range sources[1:] {
		name += "+" + source.String()
	}
	return name
}
