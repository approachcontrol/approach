package model

import (
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

// flowLaunchRoleMatrixDecisions is what the eight model-side consumers answer
// for one launch context. Every field is a decision a user can observe: the
// dock label, whether the slot may be detached, the three Flow markers the slot
// carries, whether the launch grabs focus, whether it takes the Flow lease,
// whether a failed launch marks a phase, whether it may route to tmux, and
// whether it must take the Flow lifecycle rather than the plain one.
type flowLaunchRoleMatrixDecisions struct {
	Identity           string
	Detach             embeddedTerminalDetachPolicy
	SlotRepair         bool
	SlotAgent          bool
	SlotSavedResume    bool
	FocusesInput       bool
	Reserves           bool
	FailureMarksPhase  bool
	FailurePhaseStatus flowstore.PhaseStatus
	TmuxEligible       bool
	RequiresLifecycle  bool
}

// flowLaunchRoleMatrixConsumers runs all eight consumers over one context. It
// exists so the table below cannot quietly test seven of them.
func flowLaunchRoleMatrixConsumers(t *testing.T, ctx actions.AgentLaunchContext) flowLaunchRoleMatrixDecisions {
	t.Helper()

	role := actions.FlowLaunchRoleOf(ctx)
	reserved := false
	reserving := Model{reserveFlowLaunch: func(string) (flowstore.FlowRecord, func(), error) {
		reserved = true
		return flowstore.FlowRecord{}, func() {}, nil
	}}
	if _, err := reserving.reserveFlowSpawn(ctx); err != nil {
		t.Fatalf("reserveFlowSpawn: %v", err)
	}

	update, marksPhase := Model{}.flowLaunchFailureUpdate(ctx, "boom")

	return flowLaunchRoleMatrixDecisions{
		Identity: flowEmbeddedTerminalIdentity(ctx),
		Detach:   flowEmbeddedTerminalDetachPolicy(ctx),
		// The slot stamps these three from the role rather than from the
		// context's raw fields, so the assertion is that the two still agree.
		SlotRepair:         role == actions.RoleRepair,
		SlotAgent:          role == actions.RoleWorktreeAgent,
		SlotSavedResume:    role == actions.RoleSavedSessionResume,
		FocusesInput:       flowLaunchRoleMatrixFocusesInput(ctx),
		Reserves:           reserved,
		FailureMarksPhase:  marksPhase,
		FailurePhaseStatus: update.Status,
		TmuxEligible:       tmuxRouteEligible(ctx),
		RequiresLifecycle:  flowLaunchContextRequiresLifecycle(ctx),
	}
}

// flowLaunchRoleMatrixFocusesInput asks updateFlowTerminalFocusAfterLaunch the
// role question alone. The model it runs on keeps the Flow surface hidden and
// the dock collapsed, so only the leading role branch — the worktree agent and
// the saved session resume, which focus regardless of surface — can expand it.
func flowLaunchRoleMatrixFocusesInput(ctx actions.AgentLaunchContext) bool {
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom,
		width:       160,
		height:      24,
		embeddedTerminalState: embeddedTerminalState{
			terminalFocus:       terminalFocusList,
			terminalDockVisible: false,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeFlow,
				Terminal: &internalFakeDetachableEmbeddedTerminal{},
			}},
		},
	}
	return m.updateFlowTerminalFocusAfterLaunch(ctx).terminalDockVisible
}

// TestFlowLaunchRoleMatrixConsumerDecisions is the behavior pin for
// approach-hyl.10: every reachable variant of docs/flow-launch-variant-matrix.md,
// plus the non-Flow literals and the probe shapes, driven through all eight
// model-side consumers that now read the role instead of raw marker fields.
//
// The Flow rows come out of the real builder rather than out of hand-written
// literals, so a builder change that moves a variant shows up here as a changed
// decision rather than as a stale row that still passes.
func TestFlowLaunchRoleMatrixConsumerDecisions(t *testing.T) {
	tmuxRouting := flowLaunchRouting{
		Backend:       config.LaunchBackendTmux,
		TmuxAvailable: func() bool { return true },
	}
	repairTargetValue, _ := launchContextRepairTarget(t)
	interactiveRepair := repairTargetValue
	interactiveRepair.Record.Headless = false

	autofixRecord := launchContextAutofixRecord()
	headlessAutofixRecord := launchContextAutofixRecord()
	headlessAutofixRecord.Headless = true

	autoPhase := launchContextTrackedPhaseTarget()
	autoPhase.AutoLaunch = true
	autoPhase.RequestedHeadless = true
	headlessPhase := launchContextTrackedPhaseTarget()
	headlessPhase.PersistedRecord.Headless = true

	headlessCreate := launchContextCreatePhaseTarget()
	headlessCreate.Record.Headless = true

	terminalPhaseResume := func() phaseResumeTarget {
		target := launchContextPhaseResumeTarget()
		phase := target.ReadPhase
		phase.Status = flowstore.PhaseCompleted
		target.PersistedRecord.Phases = []flowstore.FlowPhase{phase}
		return target
	}

	resumeRecord, resumeSession := launchContextSavedSessionResumeTarget()

	// The tracked-phase and resume rows share their decisions because
	// headlessness and transport are not role. Interactive is the base: tmux eligibility turns on Headless and the
	// command, not on the route the launch actually took, so the headless rows
	// below clear it and the tmux rows inherit it unchanged.
	trackedPhaseDecisions := flowLaunchRoleMatrixDecisions{
		Identity:           "implementation",
		Detach:             embeddedTerminalDetachAllowed,
		Reserves:           true,
		FailureMarksPhase:  true,
		FailurePhaseStatus: flowstore.PhaseNeedsAttention,
		TmuxEligible:       true,
		RequiresLifecycle:  true,
	}
	headlessPhaseDecisions := trackedPhaseDecisions
	headlessPhaseDecisions.TmuxEligible = false
	createPhaseDecisions := trackedPhaseDecisions
	createPhaseDecisions.Identity = "plan"

	for _, variant := range []struct {
		name    string
		target  flowLaunchTarget
		routing flowLaunchRouting
		want    flowLaunchRoleMatrixDecisions
	}{
		{
			name:   "V1 tracked phase embedded interactive",
			target: launchContextTrackedPhaseTarget(),
			want:   trackedPhaseDecisions,
		},
		{
			name:   "V2 tracked phase embedded headless",
			target: headlessPhase,
			want:   headlessPhaseDecisions,
		},
		{
			name:    "V3 tracked phase tmux",
			target:  launchContextTrackedPhaseTarget(),
			routing: tmuxRouting,
			want:    trackedPhaseDecisions,
		},
		{
			name:   "V4 auto phase headless",
			target: autoPhase,
			want:   headlessPhaseDecisions,
		},
		{
			name:   "V5 create phase interactive",
			target: launchContextCreatePhaseTarget(),
			want:   createPhaseDecisions,
		},
		{
			name:   "V6 create phase headless",
			target: headlessCreate,
			want: func() flowLaunchRoleMatrixDecisions {
				want := createPhaseDecisions
				want.TmuxEligible = false
				return want
			}(),
		},
		{
			name:   "V7 phase resume embedded",
			target: launchContextPhaseResumeTarget(),
			want:   trackedPhaseDecisions,
		},
		{
			name:   "V8 phase resume embedded terminal phase",
			target: terminalPhaseResume(),
			want: func() flowLaunchRoleMatrixDecisions {
				// A terminal phase refuses the failure update: a failed resume
				// must not regress a phase that had already finished.
				want := trackedPhaseDecisions
				want.FailureMarksPhase = false
				want.FailurePhaseStatus = ""
				return want
			}(),
		},
		{
			name:    "V9 phase resume tmux",
			target:  launchContextPhaseResumeTarget(),
			routing: tmuxRouting,
			want:    trackedPhaseDecisions,
		},
		{
			name:    "V10 phase resume tmux terminal phase",
			target:  terminalPhaseResume(),
			routing: tmuxRouting,
			want: func() flowLaunchRoleMatrixDecisions {
				want := trackedPhaseDecisions
				want.FailureMarksPhase = false
				want.FailurePhaseStatus = ""
				return want
			}(),
		},
		{
			name:   "V11 repair embedded interactive",
			target: interactiveRepair,
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "repair",
				Detach:            embeddedTerminalDetachAllowed,
				SlotRepair:        true,
				RequiresLifecycle: true,
			},
		},
		{
			name:   "V12 repair embedded headless",
			target: repairTargetValue,
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "repair",
				Detach:            embeddedTerminalDetachAllowed,
				SlotRepair:        true,
				RequiresLifecycle: true,
			},
		},
		{
			name: "V13 autofix embedded interactive",
			target: autofixTarget{
				LaunchID: "launch-1", Record: autofixRecord, PlanPath: autofixRecord.PlanPath,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "autofix pr 116",
				Detach:            embeddedTerminalDetachAllowed,
				TmuxEligible:      true,
				RequiresLifecycle: true,
			},
		},
		{
			// Headless autofix has no dock to label, so it takes the generic
			// ladder and shows the Flow ID.
			name: "V14 autofix embedded headless",
			target: autofixTarget{
				LaunchID: "launch-1", Record: headlessAutofixRecord,
				PlanPath: headlessAutofixRecord.PlanPath,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "flow-1",
				Detach:            embeddedTerminalDetachAllowed,
				RequiresLifecycle: true,
			},
		},
		{
			name: "V15 autofix tmux",
			target: autofixTarget{
				LaunchID: "launch-1", Record: autofixRecord, PlanPath: autofixRecord.PlanPath,
			},
			routing: tmuxRouting,
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "flow-1",
				Detach:            embeddedTerminalDetachAllowed,
				TmuxEligible:      true,
				RequiresLifecycle: true,
			},
		},
		{
			name: "V16 worktree agent",
			target: worktreeAgentTarget{
				LaunchID: "launch-1",
				Record:   launchContextVariantRecord(),
				PlanPath: launchContextVariantRecord().PlanPath,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "agent",
				Detach:            embeddedTerminalDetachNever,
				SlotAgent:         true,
				FocusesInput:      true,
				TmuxEligible:      true,
				RequiresLifecycle: true,
			},
		},
		{
			// The resumed session's Command is its provider name verbatim, which
			// is not one of the three tmux-routable binaries, so this row is
			// ineligible for reasons that have nothing to do with its role.
			name: "V17 saved session resume",
			target: savedSessionResumeTarget{
				LaunchID: "launch-1", Record: resumeRecord, Session: resumeSession,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity:          "session session-",
				Detach:            embeddedTerminalDetachNever,
				SlotSavedResume:   true,
				FocusesInput:      true,
				RequiresLifecycle: true,
			},
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			ctx, _, err := newFlowLaunchContext(
				variant.target, launchContextVariantSettings(), variant.routing)
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if got := flowLaunchRoleMatrixConsumers(t, ctx); got != variant.want {
				t.Fatalf("consumer decisions = %#v, want %#v", got, variant.want)
			}
		})
	}
}

// TestFlowLaunchRoleMatrixNonFlowContexts is the other half of the pin: the
// four non-Flow launch literals and the two routing probes carry no Flow marker,
// so every Flow-side consumer has to leave them exactly where they are.
func TestFlowLaunchRoleMatrixNonFlowContexts(t *testing.T) {
	for _, row := range []struct {
		name string
		ctx  actions.AgentLaunchContext
		want flowLaunchRoleMatrixDecisions
	}{
		{
			name: "non-flow session resume",
			ctx: actions.AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				WorktreePath: "/dev/alpha-worktree", ResumeSessionID: "session-1",
				Embedded: true,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity: "alpha-worktree", Detach: embeddedTerminalDetachAllowed,
				TmuxEligible: true,
			},
		},
		{
			name: "plan implementation",
			ctx: actions.AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				WorktreePath: "/dev/alpha-worktree", PlanID: "plan-1",
				PlanPath: "/state/plan.md", InitialPrompt: "implement", Embedded: true,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity: "alpha-worktree", Detach: embeddedTerminalDetachAllowed,
				TmuxEligible: true,
			},
		},
		{
			name: "open agent in worktree",
			ctx: actions.AgentLaunchContext{
				Command: "claude", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				WorktreePath: "/dev/alpha-worktree", Embedded: true,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity: "alpha-worktree", Detach: embeddedTerminalDetachAllowed,
				TmuxEligible: true,
			},
		},
		{
			name: "slice epic",
			ctx: actions.AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				InitialPrompt: "slice the epic", Embedded: true,
			},
			want: flowLaunchRoleMatrixDecisions{
				Identity: "flow", Detach: embeddedTerminalDetachAllowed,
				TmuxEligible: true,
			},
		},
		{
			name: "routing probe",
			ctx:  actions.AgentLaunchContext{Command: "codex"},
			want: flowLaunchRoleMatrixDecisions{
				Identity: "flow", Detach: embeddedTerminalDetachAllowed,
				TmuxEligible: true,
			},
		},
		{
			name: "routing probe for an ineligible command",
			ctx:  actions.AgentLaunchContext{Command: "bash"},
			want: flowLaunchRoleMatrixDecisions{
				Identity: "flow", Detach: embeddedTerminalDetachAllowed,
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			if role := actions.FlowLaunchRoleOf(row.ctx); role != actions.RoleNone {
				t.Fatalf("FlowLaunchRoleOf() = %v, want no role", role)
			}
			if got := flowLaunchRoleMatrixConsumers(t, row.ctx); got != row.want {
				t.Fatalf("consumer decisions = %#v, want %#v", got, row.want)
			}
		})
	}
}
