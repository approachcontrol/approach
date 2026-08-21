package actions

import "testing"

func TestFlowLaunchRoleOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  AgentLaunchContext
		want FlowLaunchRole
	}{
		{
			name: "empty context is not a flow launch",
			ctx:  AgentLaunchContext{},
			want: RoleNone,
		},
		{
			name: "probe context carries only a command",
			ctx:  AgentLaunchContext{Command: "claude"},
			want: RoleNone,
		},
		{
			name: "non-flow session resume",
			ctx:  AgentLaunchContext{Command: "claude", ResumeSessionID: "sess-1"},
			want: RoleNone,
		},
		{
			name: "flow id alone is not a launch role",
			ctx:  AgentLaunchContext{FlowID: "flow-1"},
			want: RoleNone,
		},
		{
			name: "auto-launch marker alone is not a launch role",
			ctx:  AgentLaunchContext{FlowID: "flow-1", FlowAutoLaunch: true},
			want: RoleNone,
		},
		{
			name: "repair",
			ctx:  AgentLaunchContext{FlowID: "flow-1", FlowRepair: true},
			want: RoleRepair,
		},
		{
			name: "worktree agent",
			ctx:  AgentLaunchContext{FlowID: "flow-1", FlowAgent: true},
			want: RoleWorktreeAgent,
		},
		{
			name: "saved session resume",
			ctx:  AgentLaunchContext{FlowID: "flow-1", FlowSavedSessionResume: true, ResumeSessionID: "sess-1"},
			want: RoleSavedSessionResume,
		},
		{
			name: "phase resume",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1",
				FlowLaunchTracked: true, ResumeSessionID: "sess-1",
			},
			want: RolePhaseResume,
		},
		{
			name: "create phase",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1",
				FlowLaunchTracked: true, PlanPhaseID: "phase-1",
			},
			want: RoleCreatePhase,
		},
		{
			name: "tracked phase",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1", FlowLaunchTracked: true,
			},
			want: RoleTrackedPhase,
		},
		{
			name: "autofix",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowAutofix: true, FlowAutofixPRNumber: 12,
			},
			want: RoleAutofix,
		},
		{
			name: "autofix without a flow id is not well formed",
			ctx:  AgentLaunchContext{FlowAutofix: true, FlowAutofixPRNumber: 12},
			want: RoleNone,
		},
		{
			name: "autofix marked tracked is not well formed",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowAutofix: true, FlowLaunchTracked: true,
			},
			want: RoleNone,
		},
		{
			// A whitespace phase ID names no phase role, but it still
			// disqualifies autofix: the shape is malformed either way, and the
			// identity ladder has always fallen through to the Flow ID for it.
			name: "a whitespace phase id disqualifies autofix",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "  ",
				FlowAutofix: true, FlowAutofixPRNumber: 12,
			},
			want: RoleNone,
		},
		{
			name: "repair wins over every other marker",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1", FlowRepair: true,
				FlowAgent: true, FlowSavedSessionResume: true, FlowAutofix: true,
			},
			want: RoleRepair,
		},
		{
			name: "worktree agent wins over saved session resume and autofix",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowAgent: true,
				FlowSavedSessionResume: true, FlowAutofix: true, FlowAutofixPRNumber: 12,
			},
			want: RoleWorktreeAgent,
		},
		{
			name: "saved session resume wins over autofix",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowSavedSessionResume: true,
				FlowAutofix: true, FlowAutofixPRNumber: 12,
			},
			want: RoleSavedSessionResume,
		},
		{
			name: "a phase id outranks the autofix marker",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1",
				FlowAutofix: true, FlowAutofixPRNumber: 12,
			},
			want: RoleTrackedPhase,
		},
		{
			name: "resume session id outranks the plan phase id",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1", FlowLaunchTracked: true,
				ResumeSessionID: "sess-1", PlanPhaseID: "phase-1",
			},
			want: RolePhaseResume,
		},
		{
			// The untracked marker is decisive only for a resume: promoting
			// this shape would take the Flow lease and mark the phase for a
			// launch that declared itself untracked.
			name: "an untracked phase resume names no role",
			ctx: AgentLaunchContext{
				FlowID: "flow-1", FlowPhaseID: "phase-1", ResumeSessionID: "sess-1",
			},
			want: RoleNone,
		},
		{
			name: "a phase id with no flow behind it makes no phase role",
			ctx:  AgentLaunchContext{FlowPhaseID: "phase-1", FlowLaunchTracked: true},
			want: RoleNone,
		},
		{
			name: "a blank phase id does not make a phase role",
			ctx:  AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "   ", FlowLaunchTracked: true},
			want: RoleNone,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := FlowLaunchRoleOf(testCase.ctx); got != testCase.want {
				t.Fatalf("FlowLaunchRoleOf() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestFlowLaunchRoleTracked(t *testing.T) {
	t.Parallel()

	tracked := map[FlowLaunchRole]bool{
		RoleTrackedPhase: true,
		RolePhaseResume:  true,
		RoleCreatePhase:  true,
	}
	for _, role := range []FlowLaunchRole{
		RoleNone, RoleTrackedPhase, RolePhaseResume, RoleRepair,
		RoleAutofix, RoleWorktreeAgent, RoleSavedSessionResume, RoleCreatePhase,
	} {
		if got := role.Tracked(); got != tracked[role] {
			t.Errorf("%v.Tracked() = %t, want %t", role, got, tracked[role])
		}
	}
}

func TestIsFlowLaunchContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ctx  AgentLaunchContext
		want bool
	}{
		{name: "empty", ctx: AgentLaunchContext{}, want: false},
		{name: "probe", ctx: AgentLaunchContext{Command: "claude"}, want: false},
		{
			name: "non-flow session resume",
			ctx:  AgentLaunchContext{Command: "claude", ResumeSessionID: "sess-1"},
			want: false,
		},
		{name: "flow id only", ctx: AgentLaunchContext{FlowID: "flow-1"}, want: true},
		{name: "phase id only", ctx: AgentLaunchContext{FlowPhaseID: "phase-1"}, want: true},
		{name: "phase kind only", ctx: AgentLaunchContext{FlowPhaseKind: "implement"}, want: true},
		{name: "tracked only", ctx: AgentLaunchContext{FlowLaunchTracked: true}, want: true},
		{name: "auto launch only", ctx: AgentLaunchContext{FlowAutoLaunch: true}, want: true},
		{name: "repair only", ctx: AgentLaunchContext{FlowRepair: true}, want: true},
		{name: "agent only", ctx: AgentLaunchContext{FlowAgent: true}, want: true},
		{
			name: "saved session resume only",
			ctx:  AgentLaunchContext{FlowSavedSessionResume: true},
			want: true,
		},
		{name: "autofix only", ctx: AgentLaunchContext{FlowAutofix: true}, want: true},
		{name: "phase terminal only", ctx: AgentLaunchContext{FlowPhaseTerminal: true}, want: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := IsFlowLaunchContext(testCase.ctx); got != testCase.want {
				t.Fatalf("IsFlowLaunchContext() = %t, want %t", got, testCase.want)
			}
		})
	}
}
