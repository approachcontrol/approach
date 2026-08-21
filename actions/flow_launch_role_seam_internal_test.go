package actions

import "testing"

// flowLaunchSeamDecisions is what the four actions-side consumers answer for one
// launch context: whether the context names a well-formed role, whether the
// embedded dock is prefilled, which session ID the provider resumes (and
// whether the resume is refused outright), and how the tracked tmux route reads
// the same context — whether it takes the leased-window branch and whether the
// role validation accepts it.
type flowLaunchSeamDecisions struct {
	Role             FlowLaunchRole
	WellFormed       bool
	Prefills         bool
	ResumeSessionID  string
	ResumeRefused    bool
	TrackedTmuxLease bool
	TrackedTmuxValid bool
}

// flowLaunchSeamDecisionsOf runs all four consumers over one context. It exists
// so the table below cannot quietly test three of them.
func flowLaunchSeamDecisionsOf(ctx AgentLaunchContext) flowLaunchSeamDecisions {
	role := FlowLaunchRoleOf(ctx)
	resumeSessionID, resumeErr := resumeSessionIDForContext(ctx)
	return flowLaunchSeamDecisions{
		Role:            role,
		WellFormed:      validateFlowLaunchRole(ctx, role) == nil,
		Prefills:        ShouldPrefillEmbeddedPrompt(ctx),
		ResumeSessionID: resumeSessionID,
		ResumeRefused:   resumeErr != nil,
		// The tracked tmux route gates its role validation, its lease branch and
		// its leased window name on the marker — the launch's claim on the Flow
		// lease — and asks the role whether that claim is well formed.
		TrackedTmuxLease: ctx.FlowLaunchTracked,
		TrackedTmuxValid: validateTrackedRepoTmuxRole(ctx, role) == nil,
	}
}

// flowLaunchSeamRow is one variant of docs/flow-launch-variant-matrix.md built
// as the literal the builder produces for it, paired with what the seam answers.
type flowLaunchSeamRow struct {
	name string
	ctx  AgentLaunchContext
	want flowLaunchSeamDecisions
}

// flowLaunchSeamPhaseCtx is the shared marker shape of the phase-attached
// variants V1–V10: a Flow, its phase, the phase kind, and the tracked marker.
func flowLaunchSeamPhaseCtx() AgentLaunchContext {
	return AgentLaunchContext{
		Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree", FlowID: "flow-1",
		FlowPhaseID: "phase-1", FlowPhaseKind: "implement",
		FlowLaunchTracked: true, InitialPrompt: "work the phase", Embedded: true,
	}
}

// flowLaunchSeamUntrackedCtx is the shared marker shape of the untracked
// variants V11–V17: a Flow and nothing else, with the caller free to set the one
// marker that names the role.
func flowLaunchSeamUntrackedCtx() AgentLaunchContext {
	return AgentLaunchContext{
		Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree", FlowID: "flow-1", Embedded: true,
	}
}

func flowLaunchSeamReachableRows() []flowLaunchSeamRow {
	// A tracked phase launch is the base: it prefills the dock, resumes
	// nothing, takes the leased window and passes the tmux role validation.
	trackedPhase := flowLaunchSeamDecisions{
		Role: RoleTrackedPhase, WellFormed: true, Prefills: true,
		TrackedTmuxLease: true, TrackedTmuxValid: true,
	}
	phaseResume := trackedPhase
	phaseResume.Role = RolePhaseResume
	phaseResume.Prefills = false
	phaseResume.ResumeSessionID = "session-1"
	createPhase := trackedPhase
	createPhase.Role = RoleCreatePhase

	// Headless clears the dock prefill — the prompt goes to argv — and the tmux
	// route refuses headless outright.
	headless := func(row flowLaunchSeamDecisions) flowLaunchSeamDecisions {
		row.Prefills = false
		row.TrackedTmuxValid = false
		return row
	}
	// The tmux route clears Embedded, which is the same thing that sends the
	// prompt to argv; the role and its tmux validation are unchanged.
	viaTmux := func(ctx AgentLaunchContext) AgentLaunchContext {
		ctx.Embedded = false
		return ctx
	}

	autoPhase := flowLaunchSeamPhaseCtx()
	autoPhase.FlowAutoLaunch = true
	autoPhase.Headless = true

	headlessPhase := flowLaunchSeamPhaseCtx()
	headlessPhase.Headless = true

	createPhaseCtx := flowLaunchSeamPhaseCtx()
	createPhaseCtx.PlanPhaseID = "phase-1"
	createPhaseCtx.PlanPhaseTitle = "Implement"
	createPhaseCtx.PlanPhaseStatus = "running"
	headlessCreateCtx := createPhaseCtx
	headlessCreateCtx.Headless = true

	phaseResumeCtx := flowLaunchSeamPhaseCtx()
	phaseResumeCtx.ResumeSessionID = "session-1"
	phaseResumeCtx.InitialPrompt = ""
	phaseResumeCtx.WorkingDir = "/dev/alpha-worktree"
	terminalPhaseResumeCtx := phaseResumeCtx
	terminalPhaseResumeCtx.FlowPhaseTerminal = true

	repairCtx := flowLaunchSeamUntrackedCtx()
	repairCtx.FlowRepair = true
	repairCtx.InitialPrompt = "repair the flow"
	headlessRepairCtx := repairCtx
	headlessRepairCtx.Headless = true

	autofixCtx := flowLaunchSeamUntrackedCtx()
	autofixCtx.FlowAutofix = true
	autofixCtx.FlowAutofixPRNumber = 116
	autofixCtx.InitialPrompt = "address the review"
	autofixCtx.WorkingDir = "/dev/alpha-worktree"
	headlessAutofixCtx := autofixCtx
	headlessAutofixCtx.Headless = true

	agentCtx := flowLaunchSeamUntrackedCtx()
	agentCtx.FlowAgent = true
	agentCtx.WorkingDir = "/dev/alpha-worktree"
	promptedAgentCtx := agentCtx
	promptedAgentCtx.InitialPrompt = "look around"

	savedResumeCtx := flowLaunchSeamUntrackedCtx()
	savedResumeCtx.Command = "claude"
	savedResumeCtx.FlowSavedSessionResume = true
	savedResumeCtx.ResumeSessionID = "session-9"
	savedResumeCtx.WorkingDir = "/dev/alpha-worktree"

	untracked := flowLaunchSeamDecisions{WellFormed: true}
	repairDecisions := untracked
	repairDecisions.Role = RoleRepair
	repairDecisions.Prefills = true
	autofixDecisions := untracked
	autofixDecisions.Role = RoleAutofix
	autofixDecisions.Prefills = true
	agentDecisions := untracked
	agentDecisions.Role = RoleWorktreeAgent
	savedResumeDecisions := untracked
	savedResumeDecisions.Role = RoleSavedSessionResume
	savedResumeDecisions.ResumeSessionID = "session-9"

	return []flowLaunchSeamRow{
		{name: "V1 tracked phase embedded interactive", ctx: flowLaunchSeamPhaseCtx(), want: trackedPhase},
		{name: "V2 tracked phase embedded headless", ctx: headlessPhase, want: headless(trackedPhase)},
		{name: "V3 tracked phase tmux", ctx: viaTmux(flowLaunchSeamPhaseCtx()), want: func() flowLaunchSeamDecisions {
			// The tmux route is not an embedded slot, so there is no dock to
			// prefill; the role and the lease branch are the embedded row's.
			want := trackedPhase
			want.Prefills = false
			return want
		}()},
		{name: "V4 auto phase headless", ctx: autoPhase, want: func() flowLaunchSeamDecisions {
			// The auto-launch marker is refused by the tmux route at the call
			// site, not by the role: an auto phase launch is a tracked phase.
			want := headless(trackedPhase)
			want.TrackedTmuxValid = false
			return want
		}()},
		{name: "V5 create phase interactive", ctx: createPhaseCtx, want: createPhase},
		{name: "V6 create phase headless", ctx: headlessCreateCtx, want: headless(createPhase)},
		{name: "V7 phase resume embedded", ctx: phaseResumeCtx, want: phaseResume},
		{name: "V8 phase resume embedded terminal phase", ctx: terminalPhaseResumeCtx, want: phaseResume},
		{name: "V9 phase resume tmux", ctx: viaTmux(phaseResumeCtx), want: phaseResume},
		{name: "V10 phase resume tmux terminal phase", ctx: viaTmux(terminalPhaseResumeCtx), want: phaseResume},
		{name: "V11 repair embedded interactive", ctx: repairCtx, want: repairDecisions},
		{name: "V12 repair embedded headless", ctx: headlessRepairCtx, want: func() flowLaunchSeamDecisions {
			want := repairDecisions
			want.Prefills = false
			return want
		}()},
		{name: "V13 autofix embedded interactive", ctx: autofixCtx, want: autofixDecisions},
		{name: "V14 autofix embedded headless", ctx: headlessAutofixCtx, want: func() flowLaunchSeamDecisions {
			want := autofixDecisions
			want.Prefills = false
			return want
		}()},
		{name: "V15 autofix tmux", ctx: viaTmux(autofixCtx), want: func() flowLaunchSeamDecisions {
			want := autofixDecisions
			want.Prefills = false
			return want
		}()},
		{
			// The builder gives the worktree agent no prompt, so there is
			// nothing to prefill; the role still says it would.
			name: "V16 worktree agent", ctx: agentCtx, want: agentDecisions,
		},
		{name: "V16 worktree agent with a prompt", ctx: promptedAgentCtx, want: func() flowLaunchSeamDecisions {
			want := agentDecisions
			want.Prefills = true
			return want
		}()},
		{name: "V17 saved session resume", ctx: savedResumeCtx, want: savedResumeDecisions},
	}
}

func flowLaunchSeamNonFlowRows() []flowLaunchSeamRow {
	return []flowLaunchSeamRow{
		{
			name: "non-flow session resume",
			ctx: AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				WorktreePath: "/dev/alpha-worktree", ResumeSessionID: "session-1",
				Embedded: true,
			},
			want: flowLaunchSeamDecisions{ResumeSessionID: "session-1"},
		},
		{
			name: "plan implementation",
			ctx: AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				WorktreePath: "/dev/alpha-worktree", PlanID: "plan-1",
				PlanPath: "/state/plan.md", InitialPrompt: "implement", Embedded: true,
			},
		},
		{
			name: "open agent in worktree",
			ctx: AgentLaunchContext{
				Command: "claude", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				WorktreePath: "/dev/alpha-worktree", Embedded: true,
			},
		},
		{
			name: "slice epic",
			ctx: AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/alpha",
				InitialPrompt: "slice the epic", Embedded: true,
			},
		},
		{name: "routing probe", ctx: AgentLaunchContext{Command: "codex"}},
		{name: "routing probe for an ineligible command", ctx: AgentLaunchContext{Command: "bash"}},
	}
}

// flowLaunchSeamInvalidRows are marker combinations no builder arm emits. They
// exist so the seam's one rejection path is pinned as deliberately as its
// acceptances: each row states which role, if any, the combination names and
// what each consumer does with it.
func flowLaunchSeamInvalidRows() []flowLaunchSeamRow {
	tracked := flowLaunchSeamPhaseCtx()

	trackedSavedResume := flowLaunchSeamUntrackedCtx()
	trackedSavedResume.FlowSavedSessionResume = true
	trackedSavedResume.FlowLaunchTracked = true
	trackedSavedResume.ResumeSessionID = "session-9"

	repairAndAgent := flowLaunchSeamUntrackedCtx()
	repairAndAgent.FlowRepair = true
	repairAndAgent.FlowAgent = true
	repairAndAgent.InitialPrompt = "repair the flow"

	autofixWithPhase := flowLaunchSeamUntrackedCtx()
	autofixWithPhase.FlowAutofix = true
	autofixWithPhase.FlowAutofixPRNumber = 116
	autofixWithPhase.FlowPhaseID = "phase-1"
	autofixWithPhase.InitialPrompt = "address the review"

	trackedAutofix := flowLaunchSeamUntrackedCtx()
	trackedAutofix.FlowAutofix = true
	trackedAutofix.FlowLaunchTracked = true
	trackedAutofix.InitialPrompt = "address the review"

	untrackedPhaseResume := flowLaunchSeamPhaseCtx()
	untrackedPhaseResume.FlowLaunchTracked = false
	untrackedPhaseResume.InitialPrompt = ""
	untrackedPhaseResume.ResumeSessionID = "session-1"

	blankFlowRepair := flowLaunchSeamUntrackedCtx()
	blankFlowRepair.FlowID = "   "
	blankFlowRepair.FlowRepair = true
	blankFlowRepair.InitialPrompt = "repair the flow"

	flowlessRepair := blankFlowRepair
	flowlessRepair.FlowID = ""

	promptedSavedResume := flowLaunchSeamUntrackedCtx()
	promptedSavedResume.FlowSavedSessionResume = true
	promptedSavedResume.ResumeSessionID = "session-9"
	promptedSavedResume.InitialPrompt = "carry on"

	headlessTrackedTmux := flowLaunchSeamPhaseCtx()
	headlessTrackedTmux.Embedded = false
	headlessTrackedTmux.Headless = true

	repairWithPhase := flowLaunchSeamPhaseCtx()
	repairWithPhase.FlowRepair = true

	return []flowLaunchSeamRow{
		{
			// The tracked marker on a saved-session resume is the shape the
			// resume ladder has always named outright: it would promote an
			// untracked launch into one that takes the Flow lease.
			name: "saved session resume marked tracked",
			ctx:  trackedSavedResume,
			want: flowLaunchSeamDecisions{
				Role: RoleSavedSessionResume, ResumeRefused: true, TrackedTmuxLease: true,
			},
		},
		{
			// Repair outranks the agent marker, so this is a repair — but one
			// carrying a marker the repair role does not own, which is not a
			// well-formed launch of any role, so nothing prefills it.
			name: "repair and worktree agent together",
			ctx:  repairAndAgent,
			want: flowLaunchSeamDecisions{Role: RoleRepair},
		},
		{
			// A phase ID outranks the autofix marker, so this is a phase-attached
			// launch that set neither the tracked marker its role requires nor
			// the empty autofix marker: malformed twice over, and prefilled by
			// nothing.
			name: "autofix carrying a phase id",
			ctx:  autofixWithPhase,
			want: flowLaunchSeamDecisions{Role: RoleTrackedPhase},
		},
		{
			name: "autofix marked tracked",
			ctx:  trackedAutofix,
			want: flowLaunchSeamDecisions{Role: RoleNone, TrackedTmuxLease: true},
		},
		{
			// A resume carrying a phase without the tracked marker names no
			// role, so the tmux route does not take the leased branch for it.
			name: "untracked phase resume",
			ctx:  untrackedPhaseResume,
			want: flowLaunchSeamDecisions{Role: RoleNone, ResumeSessionID: "session-1"},
		},
		{
			// A whitespace Flow ID is no Flow to the role, so no launch it names
			// is well formed and the dock stays empty. The old untrimmed
			// FlowID conjunct filled it.
			name: "repair with a blank flow id",
			ctx:  blankFlowRepair,
			want: flowLaunchSeamDecisions{Role: RoleRepair},
		},
		{
			name: "repair with no flow id at all",
			ctx:  flowlessRepair,
			want: flowLaunchSeamDecisions{Role: RoleRepair},
		},
		{
			name: "saved session resume carrying a prompt",
			ctx:  promptedSavedResume,
			want: flowLaunchSeamDecisions{
				Role: RoleSavedSessionResume, WellFormed: true, ResumeRefused: true,
			},
		},
		{
			name: "headless tracked tmux launch",
			ctx:  headlessTrackedTmux,
			want: flowLaunchSeamDecisions{
				Role: RoleTrackedPhase, WellFormed: true, TrackedTmuxLease: true,
			},
		},
		{
			// Repair wins the precedence, and a repair role owns no phase, so
			// the phase markers make it malformed rather than making it a phase.
			// The tracked marker it also carries claims the tmux lease, and the
			// role validation is what refuses that claim.
			name: "repair carrying phase markers",
			ctx:  repairWithPhase,
			want: flowLaunchSeamDecisions{Role: RoleRepair, TrackedTmuxLease: true},
		},
		{
			name: "tracked phase carrying an autofix pr number",
			ctx: func() AgentLaunchContext {
				ctx := tracked
				ctx.FlowAutofixPRNumber = 116
				return ctx
			}(),
			want: flowLaunchSeamDecisions{
				Role: RoleTrackedPhase, WellFormed: true, Prefills: true,
				TrackedTmuxLease: true,
			},
		},
	}
}

// TestFlowLaunchRoleSeamDecisions is the behavior pin for approach-hyl.11: every
// reachable variant, the non-Flow literals and the probes, and the malformed
// marker combinations, driven through all four actions-side consumers that read
// the Flow launch role.
func TestFlowLaunchRoleSeamDecisions(t *testing.T) {
	t.Parallel()

	rows := flowLaunchSeamReachableRows()
	rows = append(rows, flowLaunchSeamNonFlowRows()...)
	rows = append(rows, flowLaunchSeamInvalidRows()...)
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := flowLaunchSeamDecisionsOf(row.ctx); got != row.want {
				t.Fatalf("seam decisions = %#v, want %#v", got, row.want)
			}
		})
	}
}

// TestFlowLaunchRoleSeamCoversEveryRole is the acceptance criterion that the
// table above names every valid Flow role, not just the ones whose consumers
// happen to be easy to assert.
func TestFlowLaunchRoleSeamCoversEveryRole(t *testing.T) {
	t.Parallel()

	covered := map[FlowLaunchRole]bool{}
	for _, row := range flowLaunchSeamReachableRows() {
		covered[row.want.Role] = true
	}
	for _, role := range []FlowLaunchRole{
		RoleTrackedPhase, RolePhaseResume, RoleRepair, RoleAutofix,
		RoleWorktreeAgent, RoleSavedSessionResume, RoleCreatePhase,
	} {
		if !covered[role] {
			t.Errorf("the reachable-variant table names no %v launch", role)
		}
	}
}
