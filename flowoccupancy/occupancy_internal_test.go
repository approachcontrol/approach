package flowoccupancy

import (
	"errors"
	"testing"

	"github.com/approachcontrol/approach/actions"
)

func TestPurposeValidRegistry(t *testing.T) {
	validStages := map[actions.FlowLaunchRole][]Stage{
		actions.RoleNone:               {StageFooter, StageSessionRelease},
		actions.RoleTrackedPhase:       {StagePreview, StageFooter, StageAdmission, StageAutoAdvance, StageAuthoritative, StageReserved, StageInstall, StageDrain, StageDrainControl},
		actions.RoleCreatePhase:        {StageAdmission, StageAuthoritative, StageInstall},
		actions.RolePhaseResume:        {StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleRepair:             {StagePreview, StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleAutofix:            {StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleWorktreeAgent:      {StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleSavedSessionResume: {StageAdmission, StageAuthoritative, StageReserved, StageInstall},
	}
	want := make(map[Purpose]bool)
	for role, stages := range validStages {
		for _, stage := range stages {
			want[Purpose{Role: role, Stage: stage}] = true
		}
	}
	roles := []actions.FlowLaunchRole{
		actions.RoleNone, actions.RoleTrackedPhase, actions.RolePhaseResume,
		actions.RoleRepair, actions.RoleAutofix, actions.RoleWorktreeAgent,
		actions.RoleSavedSessionResume, actions.RoleCreatePhase,
	}
	for _, role := range roles {
		for stage := StageUnknown; stage <= StageSessionRelease; stage++ {
			purpose := Purpose{Role: role, Stage: stage}
			if got := purpose.Valid(); got != want[purpose] {
				t.Errorf("Purpose{%s, %s}.Valid() = %v, want %v", role, stage, got, want[purpose])
			}
		}
	}
	if len(purposeRegistry) != len(want) {
		t.Fatalf("purpose registry has %d rows, want %d", len(purposeRegistry), len(want))
	}
}

func TestQueryRejectsInvalidInputsAndMissingRuntime(t *testing.T) {
	tests := []struct {
		name  string
		query Query
		want  error
	}{
		{
			name:  "blank Flow ID",
			query: Query{FlowID: "   ", Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission}},
			want:  ErrInvalidQuery,
		},
		{
			name:  "phantom purpose",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleAutofix, Stage: StageDrain}},
			want:  ErrInvalidQuery,
		},
		{
			name:  "unsupported freshness",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StagePreview}, Freshness: FreshnessAuthoritative},
			want:  ErrInvalidQuery,
		},
		{
			name:  "unknown freshness",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission}, Freshness: FreshnessAuthoritative + 1},
			want:  ErrInvalidQuery,
		},
		{
			name:  "required runtime adapter missing",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission}},
			want:  ErrMissingRuntime,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{}).Query(tc.query)
			if !verdict.Occupied() {
				t.Fatal("Occupied() = false, want fail-closed occupancy")
			}
			if !errors.Is(verdict.Err(), tc.want) {
				t.Fatalf("Err() = %v, want errors.Is(_, %v)", verdict.Err(), tc.want)
			}
		})
	}
}

type runtimeFixture struct {
	attempts       map[string]actions.FlowLaunchRole
	flows          map[string]bool
	nonRepairFlows map[string]bool
	repairs        map[string]bool
	headless       map[string]bool
	repairDrains   map[string]bool
}

type leaseFixture struct {
	wantFlowID string
	occupied   bool
	err        error
	calls      int
	t          *testing.T
}

func (lease *leaseFixture) FlowLeaseOccupied(flowID string) (bool, error) {
	lease.calls++
	if lease.wantFlowID != "" && flowID != lease.wantFlowID {
		lease.t.Fatalf("FlowLeaseOccupied() flow ID = %q, want %q", flowID, lease.wantFlowID)
	}
	return lease.occupied, lease.err
}

func TestQueryAnswersFromLeaseSource(t *testing.T) {
	inspectErr := errors.New("unsafe flow-leases directory")
	tests := []struct {
		name       string
		lease      *leaseFixture
		wantHolder Holder
		wantErr    error
	}{
		{name: "free", lease: &leaseFixture{}, wantHolder: HolderNone},
		{name: "held", lease: &leaseFixture{occupied: true}, wantHolder: HolderPeerLease},
		{name: "inspection error", lease: &leaseFixture{err: inspectErr}, wantHolder: HolderLeaseUnreadable, wantErr: inspectErr},
		{name: "missing adapter", wantHolder: HolderLeaseUnreadable, wantErr: ErrMissingLease},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lease != nil {
				tc.lease.t = t
				tc.lease.wantFlowID = "flow-exact"
			}
			sources := Sources{Runtime: runtimeFixture{}}
			if tc.lease != nil {
				sources.Lease = tc.lease
			}
			verdict := New(sources).Query(Query{
				FlowID:  " flow-exact ",
				Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StagePreview},
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
			if !errors.Is(verdict.Err(), tc.wantErr) {
				t.Fatalf("Err() = %v, want errors.Is(_, %v)", verdict.Err(), tc.wantErr)
			}
			if tc.lease != nil && tc.lease.calls != 1 {
				t.Fatalf("lease calls = %d, want 1", tc.lease.calls)
			}
		})
	}
}

func TestTrackedPhaseLeaseAndRuntimePrecedence(t *testing.T) {
	tests := []struct {
		name          string
		stage         Stage
		leaseOccupied bool
		runtime       runtimeFixture
		wantHolder    Holder
	}{
		{
			name:          "preview lease outranks attempt and terminal",
			stage:         StagePreview,
			leaseOccupied: true,
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}},
			wantHolder:    HolderPeerLease,
		},
		{
			name:          "footer headless write outranks lease",
			stage:         StageFooter,
			leaseOccupied: true,
			runtime:       runtimeFixture{headless: map[string]bool{"flow-1": true}},
			wantHolder:    HolderHeadlessWrite,
		},
		{
			name:       "footer Flow terminal outranks attempt",
			stage:      StageFooter,
			runtime:    runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}},
			wantHolder: HolderFlowTerminal,
		},
		{
			name:       "footer non-repair Flow terminal outranks attempt and repair slot",
			stage:      StageFooter,
			runtime:    runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}, flows: map[string]bool{"flow-1": true}, nonRepairFlows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}},
			wantHolder: HolderFlowTerminal,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{
				Lease:   &leaseFixture{occupied: tc.leaseOccupied, t: t, wantFlowID: "flow-1"},
				Runtime: tc.runtime,
			}).Query(Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: tc.stage},
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
		})
	}

	t.Run("footer headless write outranks unreadable lease", func(t *testing.T) {
		verdict := New(Sources{
			Lease:   &leaseFixture{err: errors.New("lease unreadable"), t: t, wantFlowID: "flow-1"},
			Runtime: runtimeFixture{headless: map[string]bool{"flow-1": true}},
		}).Query(Query{
			FlowID:  "flow-1",
			Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageFooter},
		})
		if verdict.Holder() != HolderHeadlessWrite || verdict.Err() != nil {
			t.Fatalf("verdict = (%v, %v), want headless write with nil error", verdict.Holder(), verdict.Err())
		}
	})
}

func TestRepairPurposeHolderPrecedence(t *testing.T) {
	leaseErr := errors.New("unsafe flow-leases directory")
	tests := []struct {
		name          string
		leaseOccupied bool
		leaseErr      error
		runtime       runtimeFixture
		wantPreview   Holder
		wantAdmission Holder
		wantFooter    Holder
	}{
		{name: "free"},
		{name: "repair attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}}, wantPreview: HolderRepairAttempt, wantAdmission: HolderRepairAttempt, wantFooter: HolderRepairAttempt},
		{name: "phase resume attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RolePhaseResume}}, wantPreview: HolderPhaseResumeAttempt, wantAdmission: HolderPhaseResumeAttempt, wantFooter: HolderPhaseResumeAttempt},
		{name: "phase attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}}, wantPreview: HolderPhaseAttempt, wantAdmission: HolderPhaseAttempt, wantFooter: HolderPhaseAttempt},
		{name: "other attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleAutofix}}, wantPreview: HolderOtherAttempt, wantAdmission: HolderOtherAttempt, wantFooter: HolderOtherAttempt},
		{name: "Flow terminal", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}}, wantPreview: HolderFlowTerminal, wantAdmission: HolderFlowTerminal, wantFooter: HolderFlowTerminal},
		{name: "terminal-less repair slot", runtime: runtimeFixture{repairs: map[string]bool{"flow-1": true}}, wantPreview: HolderRepairTerminal, wantAdmission: HolderRepairTerminal, wantFooter: HolderRepairTerminal},
		{name: "headless write", runtime: runtimeFixture{headless: map[string]bool{"flow-1": true}}, wantAdmission: HolderHeadlessWrite, wantFooter: HolderHeadlessWrite},
		{
			name:          "repair attempt wins every runtime source",
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}, headless: map[string]bool{"flow-1": true}},
			wantPreview:   HolderRepairAttempt,
			wantAdmission: HolderRepairAttempt,
			wantFooter:    HolderRepairAttempt,
		},
		{
			name:          "held lease wins every runtime source",
			leaseOccupied: true,
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}, headless: map[string]bool{"flow-1": true}},
			wantPreview:   HolderPeerLease,
			wantAdmission: HolderPeerLease,
			wantFooter:    HolderPeerLease,
		},
		{
			name:          "unreadable lease wins every runtime source",
			leaseErr:      leaseErr,
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}, headless: map[string]bool{"flow-1": true}},
			wantPreview:   HolderLeaseUnreadable,
			wantAdmission: HolderLeaseUnreadable,
			wantFooter:    HolderLeaseUnreadable,
		},
	}
	for _, tc := range tests {
		for _, stage := range []struct {
			name  string
			stage Stage
			want  Holder
		}{
			{name: "preview", stage: StagePreview, want: tc.wantPreview},
			{name: "admission", stage: StageAdmission, want: tc.wantAdmission},
			{name: "footer", stage: StageFooter, want: tc.wantFooter},
		} {
			t.Run(tc.name+"/"+stage.name, func(t *testing.T) {
				lease := &leaseFixture{occupied: tc.leaseOccupied, err: tc.leaseErr, t: t, wantFlowID: "flow-1"}
				verdict := New(Sources{Lease: lease, Runtime: tc.runtime}).Query(Query{
					FlowID:  "flow-1",
					Purpose: Purpose{Role: actions.RoleRepair, Stage: stage.stage},
				})
				if verdict.Holder() != stage.want {
					t.Fatalf("Holder() = %v, want %v", verdict.Holder(), stage.want)
				}
				if !errors.Is(verdict.Err(), tc.leaseErr) {
					t.Fatalf("Err() = %v, want errors.Is(_, %v)", verdict.Err(), tc.leaseErr)
				}
			})
		}
	}
}

func (runtime runtimeFixture) AttemptHolder(flowID string) (actions.FlowLaunchRole, bool) {
	role, ok := runtime.attempts[flowID]
	return role, ok
}

func (runtime runtimeFixture) HasFlowTerminal(flowID string) bool { return runtime.flows[flowID] }
func (runtime runtimeFixture) HasNonRepairFlowTerminal(flowID string) bool {
	if runtime.nonRepairFlows != nil {
		return runtime.nonRepairFlows[flowID]
	}
	return runtime.flows[flowID] && !runtime.repairs[flowID]
}
func (runtime runtimeFixture) HasRepairTerminal(flowID string) bool { return runtime.repairs[flowID] }
func (runtime runtimeFixture) HeadlessWritePending(flowID string) bool {
	return runtime.headless[flowID]
}
func (runtime runtimeFixture) RepairDrainPending(flowID string) bool {
	return runtime.repairDrains[flowID]
}

func TestQueryAnswersFromInProcessRuntime(t *testing.T) {
	tests := []struct {
		name    string
		runtime runtimeFixture
		flowID  string
		want    Holder
	}{
		{name: "free", runtime: runtimeFixture{}, flowID: "flow-1", want: HolderNone},
		{name: "exact Flow ID only", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-10": actions.RoleRepair}}, flowID: "flow-1", want: HolderNone},
		{name: "repair attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}}, flowID: "flow-1", want: HolderRepairAttempt},
		{name: "phase resume attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RolePhaseResume}}, flowID: "flow-1", want: HolderPhaseResumeAttempt},
		{name: "tracked phase attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}}, flowID: "flow-1", want: HolderPhaseAttempt},
		{name: "create phase attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleCreatePhase}}, flowID: "flow-1", want: HolderPhaseAttempt},
		{name: "other attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleAutofix}}, flowID: "flow-1", want: HolderOtherAttempt},
		{name: "Flow terminal", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderFlowTerminal},
		{name: "repair slot", runtime: runtimeFixture{repairs: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderRepairTerminal},
		{name: "repair slot with live terminal", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderRepairTerminal},
		{name: "attempt wins simultaneous sources", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderRepairAttempt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{Runtime: tc.runtime}).Query(Query{
				FlowID:  tc.flowID,
				Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission},
			})
			if verdict.Holder() != tc.want {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.want)
			}
			if verdict.Occupied() != (tc.want != HolderNone) {
				t.Fatalf("Occupied() = %v, want %v", verdict.Occupied(), tc.want != HolderNone)
			}
			if verdict.Err() != nil {
				t.Fatalf("Err() = %v, want nil", verdict.Err())
			}
		})
	}
}

func TestStageInstallReadsPerRoleTerminalSources(t *testing.T) {
	tests := []struct {
		name       string
		role       actions.FlowLaunchRole
		flow       bool
		repair     bool
		wantHolder Holder
	}{
		{name: "tracked phase ignores Flow terminal", role: actions.RoleTrackedPhase, flow: true, wantHolder: HolderNone},
		{name: "tracked phase reads repair terminal", role: actions.RoleTrackedPhase, repair: true, wantHolder: HolderRepairTerminal},
		{name: "create phase reads Flow terminal", role: actions.RoleCreatePhase, flow: true, wantHolder: HolderFlowTerminal},
		{name: "create phase reads repair terminal", role: actions.RoleCreatePhase, repair: true, wantHolder: HolderRepairTerminal},
		{name: "phase resume ignores Flow terminal", role: actions.RolePhaseResume, flow: true, wantHolder: HolderNone},
		{name: "phase resume reads repair terminal", role: actions.RolePhaseResume, repair: true, wantHolder: HolderRepairTerminal},
		{name: "repair reads Flow terminal", role: actions.RoleRepair, flow: true, wantHolder: HolderFlowTerminal},
		{name: "repair reads repair terminal", role: actions.RoleRepair, repair: true, wantHolder: HolderRepairTerminal},
		{name: "autofix ignores Flow terminal", role: actions.RoleAutofix, flow: true, wantHolder: HolderNone},
		{name: "autofix reads repair terminal", role: actions.RoleAutofix, repair: true, wantHolder: HolderRepairTerminal},
		{name: "worktree agent reads Flow terminal", role: actions.RoleWorktreeAgent, flow: true, wantHolder: HolderFlowTerminal},
		{name: "worktree agent reads repair terminal", role: actions.RoleWorktreeAgent, repair: true, wantHolder: HolderRepairTerminal},
		{name: "saved-session resume reads Flow terminal", role: actions.RoleSavedSessionResume, flow: true, wantHolder: HolderFlowTerminal},
		{name: "saved-session resume ignores repair terminal", role: actions.RoleSavedSessionResume, repair: true, wantHolder: HolderNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{Runtime: runtimeFixture{
				flows:   map[string]bool{"flow-1": tc.flow},
				repairs: map[string]bool{"flow-1": tc.repair},
			}}).Query(Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: tc.role, Stage: StageInstall},
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
			if verdict.Err() != nil {
				t.Fatalf("Err() = %v, want nil", verdict.Err())
			}
		})
	}
}

func TestAutofixAdmissionUsesWholeFlowAgentProbe(t *testing.T) {
	policy := purposeRegistry[Purpose{Role: actions.RoleAutofix, Stage: StageAdmission}]
	if policy.probe != probeFlowAgent {
		t.Fatalf("probe = %v, want whole-Flow agent probe %v", policy.probe, probeFlowAgent)
	}
}

func TestPhaseResumePreviewPreservesRepairSlotException(t *testing.T) {
	policy := purposeRegistry[Purpose{Role: actions.RolePhaseResume, Stage: StageFooter}]
	tests := []struct {
		name       string
		flow       bool
		repair     bool
		wantHolder Holder
	}{
		{name: "neither", wantHolder: HolderNone},
		{name: "Flow terminal only", flow: true, wantHolder: HolderFlowTerminal},
		{name: "repair slot only", repair: true, wantHolder: HolderNone},
		{name: "overlapping Flow terminal and repair slot", flow: true, repair: true, wantHolder: HolderNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := queryRuntime(policy.runtime, runtimeFixture{
				flows:   map[string]bool{"flow-1": tc.flow},
				repairs: map[string]bool{"flow-1": tc.repair},
			}, "flow-1")
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
		})
	}
}

func TestPreviewAndFooterKeepHeadlessWritePoliciesSeparate(t *testing.T) {
	for _, role := range []actions.FlowLaunchRole{actions.RoleTrackedPhase, actions.RoleRepair} {
		runtime := runtimeFixture{headless: map[string]bool{"flow-1": true}}
		preview := purposeRegistry[Purpose{Role: role, Stage: StagePreview}]
		if verdict := queryRuntime(preview.runtime, runtime, "flow-1"); verdict.Holder() != HolderNone {
			t.Errorf("%s preview holder = %v, want none", role, verdict.Holder())
		}
		footer := purposeRegistry[Purpose{Role: role, Stage: StageFooter}]
		if verdict := queryRuntime(footer.runtime, runtime, "flow-1"); verdict.Holder() != HolderHeadlessWrite {
			t.Errorf("%s footer holder = %v, want %v", role, verdict.Holder(), HolderHeadlessWrite)
		}
	}
}

func TestDrainGateAndControlKeepRepairStateSeparate(t *testing.T) {
	gate := purposeRegistry[Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain}]
	control := purposeRegistry[Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrainControl}]
	tests := []struct {
		name        string
		runtime     runtimeFixture
		wantGate    Holder
		wantControl Holder
	}{
		{name: "Flow terminal gates", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}}, wantGate: HolderFlowTerminal},
		{name: "repair slot controls", runtime: runtimeFixture{repairs: map[string]bool{"flow-1": true}}, wantControl: HolderRepairTerminal},
		{name: "repair marker controls", runtime: runtimeFixture{repairDrains: map[string]bool{"flow-1": true}}, wantControl: HolderRepairDrain},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if verdict := queryRuntime(gate.runtime, tc.runtime, "flow-1"); verdict.Holder() != tc.wantGate {
				t.Errorf("gate holder = %v, want %v", verdict.Holder(), tc.wantGate)
			}
			if verdict := queryRuntime(control.runtime, tc.runtime, "flow-1"); verdict.Holder() != tc.wantControl {
				t.Errorf("control holder = %v, want %v", verdict.Holder(), tc.wantControl)
			}
		})
	}
	if gate.sources&readSessionCache != 0 {
		t.Fatal("drain gate reads the session mirror, want cached Flow record only")
	}
}

func TestSessionReleaseFooterAndGestureKeepSourcesSeparate(t *testing.T) {
	footer := purposeRegistry[Purpose{Role: actions.RoleNone, Stage: StageFooter}]
	if footer.sources != readFlowCache || footer.runtime != 0 {
		t.Fatalf("footer policy = {sources:%v runtime:%v}, want cached Flow only", footer.sources, footer.runtime)
	}
	gesture := purposeRegistry[Purpose{Role: actions.RoleNone, Stage: StageSessionRelease}]
	wantSources := readRuntime | readFlowCache | readSessionStore
	if gesture.sources != wantSources {
		t.Fatalf("gesture sources = %v, want %v", gesture.sources, wantSources)
	}
	if gesture.runtime != readAttempt|readHeadlessWrite {
		t.Fatalf("gesture runtime = %v, want attempt and headless-write sources", gesture.runtime)
	}
	if freshness, ok := resolveFreshness(StageSessionRelease, FreshnessDefault); !ok || freshness != FreshnessAuthoritative {
		t.Fatalf("default freshness = (%v, %v), want (%v, true)", freshness, ok, FreshnessAuthoritative)
	}
}

// Free() stays the way callers and tests express "nothing holds it", and must
// not drift into occupancy alongside the fail-closed stub.
func TestFreeIsNotOccupied(t *testing.T) {
	if Free().Occupied() {
		t.Fatal("Free().Occupied() = true, want false")
	}
	if Free().Err() != nil {
		t.Fatalf("Free().Err() = %v, want nil", Free().Err())
	}
}

// A corrupt or future Holder must not be readable as a free Flow. Naming
// HolderNone explicitly keeps the default branch for values this package does
// not know, the way Stage.String() already does.
func TestHolderStringSeparatesNoneFromUnknown(t *testing.T) {
	if got := HolderNone.String(); got != "none" {
		t.Fatalf("HolderNone.String() = %q, want %q", got, "none")
	}
	if got := (HolderHeadlessWrite + 1).String(); got != "unknown" {
		t.Fatalf("out-of-range Holder.String() = %q, want %q", got, "unknown")
	}
	if got := Holder(-1).String(); got != "unknown" {
		t.Fatalf("negative Holder.String() = %q, want %q", got, "unknown")
	}
}
