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
	attempts     map[string]actions.FlowLaunchRole
	flows        map[string]bool
	repairs      map[string]bool
	headless     map[string]bool
	repairDrains map[string]bool
}

func (runtime runtimeFixture) AttemptHolder(flowID string) (actions.FlowLaunchRole, bool) {
	role, ok := runtime.attempts[flowID]
	return role, ok
}

func (runtime runtimeFixture) HasFlowTerminal(flowID string) bool   { return runtime.flows[flowID] }
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
