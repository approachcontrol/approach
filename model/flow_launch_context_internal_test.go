package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/sessions"
)

// launchContextVariantRecord is the fixture every variant row derives its
// target from. It is deliberately a plain literal rather than the generic
// agent's harness record: the builder is being pinned at its own interface,
// so the row must not inherit whatever the prepare stage's fixture happens to
// set.
func launchContextVariantRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree",
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
	}
}

func launchContextVariantSettings() flowLaunchAgentSettingsSnapshot {
	return flowLaunchAgentSettingsSnapshot{
		Command:          "codex",
		Model:            "gpt-5",
		ReasoningEffort:  "high",
		SessionStateRoot: "/state",
	}
}

// TestNewFlowLaunchContextPinsEachVariantsCanonicalContext compares the whole
// returned struct, not a subset of fields, so a field added to any Flow role's
// launch context has to be declared in this table before it can ship.
func TestNewFlowLaunchContextPinsEachVariantsCanonicalContext(t *testing.T) {
	record := launchContextVariantRecord()
	repair, repairRecord := launchContextRepairTarget(t)
	autofixRecord := launchContextAutofixRecord()
	resumeRecord, resumeSession := launchContextSavedSessionResumeTarget()
	headlessAutofixRecord := launchContextAutofixRecord()
	headlessAutofixRecord.Headless = true
	phaseResume := launchContextPhaseResumeTarget()
	// The read phase stays running while the persisted one is completed, so the
	// row cannot pass by reading the wrong record.
	terminalPhaseResume := launchContextPhaseResumeTarget()
	terminalPhase := terminalPhaseResume.ReadPhase
	terminalPhase.Status = flowstore.PhaseCompleted
	terminalPhaseResume.PersistedRecord.Phases = []flowstore.FlowPhase{terminalPhase}
	createPhase := launchContextCreatePhaseTarget()
	headlessCreatePhase := launchContextCreatePhaseTarget()
	headlessCreatePhase.Record.Headless = true
	for _, variant := range []struct {
		name     string
		target   flowLaunchTarget
		routing  flowLaunchRouting
		want     actions.AgentLaunchContext
		decision flowLaunchRouteDecision
	}{
		{
			name: "worktree agent",
			target: worktreeAgentTarget{
				LaunchID: "launch-1",
				Record:   record,
				PlanPath: record.PlanPath,
			},
			want: actions.AgentLaunchContext{
				Command:          "codex",
				LaunchID:         "launch-1",
				RepoPath:         record.RepoPath,
				WorktreePath:     record.WorktreePath,
				WorkingDir:       record.WorktreePath,
				Branch:           record.Branch,
				Commit:           record.Commit,
				SessionStateRoot: "/state",
				PlanID:           record.PlanID,
				PlanPath:         record.PlanPath,
				FlowID:           record.FlowID,
				FlowAgent:        true,
				Embedded:         true,
				Model:            "gpt-5",
				ReasoningEffort:  "high",
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// Repair sets no FlowPhaseID and leaves FlowLaunchTracked false. That
			// empty phase ID is load-bearing: it is what makes
			// flowLaunchFailureUpdate refuse, which is what keeps a failed repair
			// from mutating a phase. WorkingDir stays empty too — unlike the
			// worktree agent, repair does not chdir into a directory it cannot
			// assume still exists.
			name:   "repair",
			target: repair,
			want: actions.AgentLaunchContext{
				// Command, Model and effort come from the obstruction phase's
				// resolved settings on the target, not from the TUI snapshot.
				Command:          "claude",
				LaunchID:         "launch-1",
				RepoPath:         repairRecord.RepoPath,
				WorktreePath:     repairRecord.WorktreePath,
				Branch:           repairRecord.Branch,
				Commit:           repairRecord.Commit,
				SessionStateRoot: "/state",
				PlanID:           repairRecord.PlanID,
				PlanPath:         repairRecord.PlanPath,
				FlowID:           repairRecord.FlowID,
				FlowRepair:       true,
				Embedded:         true,
				Headless:         true,
				Model:            "opus",
				ReasoningEffort:  "medium",
				InitialPrompt:    launchContextRepairPrompt(repairRecord, ""),
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// Autofix is phase-untracked like repair, but it chdirs into the
			// worktree the gate guaranteed and it carries the two markers the
			// prefill boundary reads. Every phase/tracked/repair/agent marker
			// stays zero.
			name: "autofix embedded",
			target: autofixTarget{
				LaunchID:         "launch-1",
				Record:           autofixRecord,
				PlanPath:         autofixRecord.PlanPath,
				FallbackRepoPath: "/dev/read-stage",
			},
			want: actions.AgentLaunchContext{
				Command:             "codex",
				LaunchID:            "launch-1",
				RepoPath:            autofixRecord.RepoPath,
				WorktreePath:        autofixRecord.WorktreePath,
				WorkingDir:          autofixRecord.WorktreePath,
				Branch:              autofixRecord.Branch,
				Commit:              autofixRecord.Commit,
				SessionStateRoot:    "/state",
				PlanID:              autofixRecord.PlanID,
				PlanPath:            autofixRecord.PlanPath,
				FlowID:              autofixRecord.FlowID,
				FlowAutofix:         true,
				FlowAutofixPRNumber: autofixRecord.PR.Number,
				Embedded:            true,
				Model:               "gpt-5",
				ReasoningEffort:     "high",
				InitialPrompt:       launchContextAutofixPrompt(autofixRecord, ""),
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// A headless launch is never tmux-eligible, so it stays embedded even
			// with the tmux backend and tmux on PATH — and declining a route the
			// design excludes is not a fallback, so no note is owed.
			name: "autofix headless",
			target: autofixTarget{
				LaunchID:         "launch-1",
				Record:           headlessAutofixRecord,
				PlanPath:         headlessAutofixRecord.PlanPath,
				FallbackRepoPath: "/dev/read-stage",
			},
			routing: flowLaunchRouting{
				Backend:       config.LaunchBackendTmux,
				TmuxAvailable: func() bool { return true },
			},
			want: actions.AgentLaunchContext{
				Command:             "codex",
				LaunchID:            "launch-1",
				RepoPath:            headlessAutofixRecord.RepoPath,
				WorktreePath:        headlessAutofixRecord.WorktreePath,
				WorkingDir:          headlessAutofixRecord.WorktreePath,
				Branch:              headlessAutofixRecord.Branch,
				Commit:              headlessAutofixRecord.Commit,
				SessionStateRoot:    "/state",
				PlanID:              headlessAutofixRecord.PlanID,
				PlanPath:            headlessAutofixRecord.PlanPath,
				FlowID:              headlessAutofixRecord.FlowID,
				FlowAutofix:         true,
				FlowAutofixPRNumber: headlessAutofixRecord.PR.Number,
				Embedded:            true,
				Headless:            true,
				Model:               "gpt-5",
				ReasoningEffort:     "high",
				InitialPrompt:       launchContextAutofixPrompt(headlessAutofixRecord, ""),
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// The tmux route's context is the embedded one with Embedded cleared:
			// a tmux window has no dock to prefill, so clearing it is what sends
			// the prompt to argv.
			name: "autofix tmux",
			target: autofixTarget{
				LaunchID:         "launch-1",
				Record:           autofixRecord,
				PlanPath:         autofixRecord.PlanPath,
				FallbackRepoPath: "/dev/read-stage",
			},
			routing: flowLaunchRouting{
				Backend:       config.LaunchBackendTmux,
				TmuxAvailable: func() bool { return true },
			},
			want: actions.AgentLaunchContext{
				Command:             "codex",
				LaunchID:            "launch-1",
				RepoPath:            autofixRecord.RepoPath,
				WorktreePath:        autofixRecord.WorktreePath,
				WorkingDir:          autofixRecord.WorktreePath,
				Branch:              autofixRecord.Branch,
				Commit:              autofixRecord.Commit,
				SessionStateRoot:    "/state",
				PlanID:              autofixRecord.PlanID,
				PlanPath:            autofixRecord.PlanPath,
				FlowID:              autofixRecord.FlowID,
				FlowAutofix:         true,
				FlowAutofixPRNumber: autofixRecord.PR.Number,
				Model:               "gpt-5",
				ReasoningEffort:     "high",
				InitialPrompt:       launchContextAutofixPrompt(autofixRecord, ""),
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteTmux},
		},
		{
			// The one role whose command comes from the session rather than the
			// snapshot, and the one that must leave Model and ReasoningEffort
			// empty: agentCommandSpec refuses a resume that carries them, so the
			// snapshot's gpt-5/high must not reach the context. Every
			// phase/tracked/repair/agent/autofix marker stays zero, and the Flow
			// ID is the reserved record's while everything else is the session's.
			name: "saved session resume",
			target: savedSessionResumeTarget{
				LaunchID: "launch-1",
				Record:   resumeRecord,
				Session:  resumeSession,
			},
			want: actions.AgentLaunchContext{
				Command:                "codex-cli",
				LaunchID:               "launch-1",
				RepoPath:               resumeSession.RepoPath,
				WorktreePath:           resumeSession.WorktreePath,
				WorkingDir:             resumeSession.CWD,
				Branch:                 resumeSession.Branch,
				Commit:                 resumeSession.Commit,
				SessionStateRoot:       "/state",
				ResumeSessionID:        resumeSession.SessionID,
				PlanID:                 resumeSession.PlanID,
				PlanPath:               resumeSession.PlanPath,
				FlowID:                 resumeRecord.FlowID,
				FlowSavedSessionResume: true,
				Embedded:               true,
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// The tracked resume: FlowLaunchTracked and the phase's ID, kind and
			// terminal flag all set, every untracked role's marker
			// (FlowAgent/FlowRepair/FlowAutofix/FlowSavedSessionResume) zero. That
			// exact shape is what validateTrackedRepoTmuxRole accepts.
			name:     "phase resume embedded",
			target:   phaseResume,
			want:     launchContextPhaseResumeContext(phaseResume.Record),
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// Phase resume is genuinely tmux-eligible where repair and
			// saved-session resume are not: it sets neither Headless nor
			// FlowRepair, the only markers tmuxRouteEligible refuses on. Embedded
			// clears because a tmux window has no dock to prefill.
			name:   "phase resume tmux",
			target: phaseResume,
			routing: flowLaunchRouting{
				Backend:       config.LaunchBackendTmux,
				TmuxAvailable: func() bool { return true },
			},
			want: func() actions.AgentLaunchContext {
				ctx := launchContextPhaseResumeContext(phaseResume.Record)
				ctx.Embedded = false
				return ctx
			}(),
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteTmux},
		},
		{
			// V5: the first launch of a freshly created Flow. It is the only role
			// that sets the PlanPhase trio, and like every other role it emits
			// its final FlowLaunchTracked and Embedded here rather than leaving
			// them for install: the one window before install
			// (failCreateFlowLaunchEmbedded) persists the same phase update
			// either way, since flowLaunchFailureUpdate reads FlowLaunchTracked
			// only for a resume. PlanID, PlanPath and WorkingDir stay zero too —
			// no plan exists yet, and actions falls back to WorktreePath.
			name:     "create phase interactive",
			target:   createPhase,
			want:     launchContextCreatePhaseContext(createPhase),
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// V6: the headless create launch, with the tmux backend and tmux on
			// PATH to pin that this role's route is a constant rather than a
			// decision: create always takes the embedded slot, so no note is owed
			// and Embedded is not cleared for argv.
			name:   "create phase headless",
			target: headlessCreatePhase,
			routing: flowLaunchRouting{
				Backend:       config.LaunchBackendTmux,
				TmuxAvailable: func() bool { return true },
			},
			want:     launchContextCreatePhaseContext(headlessCreatePhase),
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// The persisted phase decides the terminal flag, not the read one:
			// resuming a completed phase must keep FlowPhaseTerminal set, which is
			// what stops a failed resume from regressing it back to running.
			name:   "phase resume terminal phase",
			target: terminalPhaseResume,
			want: func() actions.AgentLaunchContext {
				ctx := launchContextPhaseResumeContext(terminalPhaseResume.Record)
				ctx.FlowPhaseTerminal = true
				return ctx
			}(),
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			ctx, decision, err := newFlowLaunchContext(
				variant.target, launchContextVariantSettings(), variant.routing)
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx != variant.want {
				t.Fatalf("context = %#v, want %#v", ctx, variant.want)
			}
			if decision != variant.decision {
				t.Fatalf("decision = %#v, want %#v", decision, variant.decision)
			}
			// The classifier is the builder's inverse, not a parallel guess at
			// the same rule: every arm's built context has to read back as the
			// role its payload declared.
			if role := actions.FlowLaunchRoleOf(ctx); role != variant.target.role() {
				t.Fatalf("FlowLaunchRoleOf(built) = %v, want %v", role, variant.target.role())
			}
		})
	}
}

func TestNewFlowLaunchContextStampsThePinnedBuild(t *testing.T) {
	claims := stubRetainLaunchPin(t)
	settings := launchContextVariantSettings()
	settings.Pin = controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
		Digest:         "abc123def456",
	}

	ctx, _, err := newFlowLaunchContext(worktreeAgentTarget{
		LaunchID: "launch-1",
		Record:   launchContextVariantRecord(),
	}, settings, flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.Executable != settings.Pin.ExecutablePath || ctx.BuildVersion != "v0.10.3" || ctx.DBSchemaVersion != 6 {
		t.Fatalf("builder did not stamp the pin: %#v", ctx)
	}
	if len(*claims) != 1 || (*claims)[0] != [3]string{"/state", "launch-1", "abc123def456"} {
		t.Fatalf("pin claims = %v, want exactly one for this launch", *claims)
	}
}

func TestNewFlowLaunchContextRejectsIncompletePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		target worktreeAgentTarget
	}{
		{
			name:   "launch id",
			target: worktreeAgentTarget{Record: launchContextVariantRecord()},
		},
		{
			name: "flow id",
			target: func() worktreeAgentTarget {
				record := launchContextVariantRecord()
				record.FlowID = "  "
				return worktreeAgentTarget{LaunchID: "launch-1", Record: record}
			}(),
		},
		{
			name: "worktree path",
			target: func() worktreeAgentTarget {
				record := launchContextVariantRecord()
				record.WorktreePath = ""
				return worktreeAgentTarget{LaunchID: "launch-1", Record: record}
			}(),
		},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			ctx, decision, err := newFlowLaunchContext(
				missing.target, launchContextVariantSettings(), flowLaunchRouting{})
			if err == nil {
				t.Fatalf("incomplete payload built a context: %#v", ctx)
			}
			if decision != (flowLaunchRouteDecision{}) {
				t.Fatalf("failed build returned decision %#v", decision)
			}
		})
	}
}

// launchContextRepairTarget is the repair fixture. Unlike the worktree agent's
// record it needs real directories: the builder resolves paths through
// flowRepairLaunchPaths, whose ladder is an os.Stat probe, so a fixture built
// from names that merely happen not to exist would pin the fallback branch by
// accident rather than by intent.
func launchContextRepairTarget(t *testing.T) (repairTarget, flowstore.FlowRecord) {
	t.Helper()
	dir := t.TempDir()
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     dir,
		WorktreePath: dir,
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Kind:    flowstore.KindImplementation,
			Status:  flowstore.PhaseBlocked,
			Outcome: flowstore.OutcomeBlocked,
			Notes:   "persisted metadata is inconsistent",
			Order:   1,
		}},
	}
	return repairTarget{
		LaunchID:             "launch-1",
		Record:               record,
		Agent:                agent.Settings{Command: "claude", Model: "opus", ReasoningEffort: "medium"},
		FallbackRepoPath:     dir,
		FallbackWorktreePath: dir,
		PlanID:               record.PlanID,
		PlanPath:             record.PlanPath,
	}, record
}

// launchContextRepairPrompt computes the expectation rather than pasting it, so
// an edit to the repair prompt copy does not have to be mirrored here. What the
// row pins is that the builder renders the prompt from this record with this
// binary — not the wording.
func launchContextRepairPrompt(record flowstore.FlowRecord, binary string) string {
	obstruction, _ := flowRepairObstructionForRecord(record)
	return flowRepairPrompt(record, obstruction, binary)
}

// TestNewFlowLaunchContextBuildsRepairFromTheRecordsPlanRule pins the rule the
// call site used to own: the record's own plan path always wins, and the read
// stage's resolved path survives only while the record still points at the same
// plan. A record that has moved to another plan must not carry the old plan's
// markdown into the repair session.
func TestNewFlowLaunchContextBuildsRepairFromTheRecordsPlanRule(t *testing.T) {
	for _, tt := range []struct {
		name           string
		recordPlanPath string
		recordPlanID   string
		want           string
	}{
		{name: "record plan path wins", recordPlanPath: "/state/record-plan.md", recordPlanID: "plan-1", want: "/state/record-plan.md"},
		{name: "matching plan id keeps the read stage path", recordPlanPath: "", recordPlanID: "plan-1", want: "/state/read-plan.md"},
		{name: "different plan id drops the read stage path", recordPlanPath: "", recordPlanID: "plan-2", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := launchContextRepairTarget(t)
			target.Record.PlanPath = tt.recordPlanPath
			target.Record.PlanID = tt.recordPlanID
			target.PlanID = "plan-1"
			target.PlanPath = "/state/read-plan.md"

			ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings(), flowLaunchRouting{})
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx.PlanPath != tt.want {
				t.Fatalf("plan path = %q, want %q", ctx.PlanPath, tt.want)
			}
			if ctx.PlanID != tt.recordPlanID {
				t.Fatalf("plan id = %q, want the record's %q", ctx.PlanID, tt.recordPlanID)
			}
		})
	}
}

// TestNewFlowLaunchContextResolvesRepairPathsThroughTheFallbackLadder covers the
// argument order the builder has to transcribe correctly: recorded worktree,
// then recorded repo, then the read stage's worktree, then its repo — and, when
// nothing is usable, the read stage's pair kept verbatim so repair can still
// reach a Flow whose directories are all gone.
func TestNewFlowLaunchContextResolvesRepairPathsThroughTheFallbackLadder(t *testing.T) {
	fallbackWorktree := t.TempDir()
	fallbackRepo := t.TempDir()
	for _, tt := range []struct {
		name             string
		recordRepo       string
		recordWorktree   string
		fallbackWorktree string
		fallbackRepo     string
		wantRepo         string
		wantWorktree     string
	}{
		{
			name:             "unusable record falls back to the read stage worktree",
			recordRepo:       "/dev/null/missing-repo",
			recordWorktree:   "/dev/null/missing-worktree",
			fallbackWorktree: fallbackWorktree,
			fallbackRepo:     fallbackRepo,
			wantRepo:         fallbackWorktree,
			wantWorktree:     fallbackWorktree,
		},
		{
			name:             "no read stage worktree falls back to its repo",
			recordRepo:       "/dev/null/missing-repo",
			recordWorktree:   "/dev/null/missing-worktree",
			fallbackWorktree: "/dev/null/missing-fallback",
			fallbackRepo:     fallbackRepo,
			wantRepo:         fallbackRepo,
			wantWorktree:     fallbackRepo,
		},
		{
			name:             "nothing usable keeps the read stage pair",
			recordRepo:       "/dev/null/missing-repo",
			recordWorktree:   "/dev/null/missing-worktree",
			fallbackWorktree: "/dev/null/missing-fallback-worktree",
			fallbackRepo:     "/dev/null/missing-fallback-repo",
			wantRepo:         "/dev/null/missing-fallback-repo",
			wantWorktree:     "/dev/null/missing-fallback-worktree",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := launchContextRepairTarget(t)
			target.Record.RepoPath = tt.recordRepo
			target.Record.WorktreePath = tt.recordWorktree
			target.FallbackWorktreePath = tt.fallbackWorktree
			target.FallbackRepoPath = tt.fallbackRepo

			ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings(), flowLaunchRouting{})
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx.RepoPath != tt.wantRepo || ctx.WorktreePath != tt.wantWorktree {
				t.Fatalf("paths = (%q, %q), want (%q, %q)", ctx.RepoPath, ctx.WorktreePath, tt.wantRepo, tt.wantWorktree)
			}
		})
	}
}

// TestNewFlowLaunchContextRendersTheRepairPromptWithThePinnedBinary is the
// ordering guard. The prompt used to read ctx.Executable, which only had a value
// because the old call site stamped before refreshing; the builder stamps last,
// so it must render from the pin directly. A regression here is silent — the
// prompt would still be well-formed, just telling the agent to run whatever
// `approach` is on PATH instead of the pinned build.
func TestNewFlowLaunchContextRendersTheRepairPromptWithThePinnedBinary(t *testing.T) {
	stubRetainLaunchPin(t)
	target, record := launchContextRepairTarget(t)
	settings := launchContextVariantSettings()
	settings.Pin = controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
		Digest:         "abc123def456",
	}

	ctx, _, err := newFlowLaunchContext(target, settings, flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if !strings.Contains(ctx.InitialPrompt, settings.Pin.ExecutablePath) {
		t.Fatalf("repair prompt does not name the pinned binary:\n%s", ctx.InitialPrompt)
	}
	if ctx.InitialPrompt != launchContextRepairPrompt(record, settings.Pin.ExecutablePath) {
		t.Fatalf("repair prompt = %q, want the pinned-binary rendering", ctx.InitialPrompt)
	}
	if ctx.Executable != settings.Pin.ExecutablePath {
		t.Fatalf("repair context was not stamped: %#v", ctx)
	}
}

// TestNewFlowLaunchContextRejectsIncompleteRepairPayloads pins repair's
// validation as deliberately looser than the worktree agent's: repair exists for
// Flows whose recorded directories are gone, so an empty worktree path is an
// accepted payload here. The no-usable-directory refusal is admission's.
func TestNewFlowLaunchContextRejectsIncompleteRepairPayloads(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*repairTarget)
		wantErr bool
	}{
		{name: "missing launch id", mutate: func(target *repairTarget) { target.LaunchID = "  " }, wantErr: true},
		{name: "missing flow id", mutate: func(target *repairTarget) { target.Record.FlowID = "  " }, wantErr: true},
		{
			name: "empty worktree path is accepted",
			mutate: func(target *repairTarget) {
				target.Record.WorktreePath = ""
				target.FallbackWorktreePath = ""
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := launchContextRepairTarget(t)
			tt.mutate(&target)

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), flowLaunchRouting{})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("incomplete payload built a context: %#v", ctx)
				}
				if decision != (flowLaunchRouteDecision{}) {
					t.Fatalf("failed build returned decision %#v", decision)
				}
				return
			}
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if decision != (flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded}) {
				t.Fatalf("decision = %#v, want embedded with no note", decision)
			}
		})
	}
}

// launchContextAutofixRecord is the autofix fixture: the variant record plus the
// PR target the gate guarantees. Nothing on this path may emit `autofix pr #0`,
// so the number is what the prompt row and the validation row both turn on.
func launchContextAutofixRecord() flowstore.FlowRecord {
	record := launchContextVariantRecord()
	record.PR = flowstore.PullRequest{Number: 116}
	return record
}

// launchContextAutofixPrompt computes the expectation rather than pasting it,
// for the same reason the repair helper does: the rows pin that the builder
// renders autofix's prompt from this record with this binary, not the wording.
func launchContextAutofixPrompt(record flowstore.FlowRecord, binary string) string {
	return autofixPrompt(record, FlowPromptTemplates{}, binary)
}

// TestNewFlowLaunchContextFallsBackWhenTmuxIsMissing pins the one case that owes
// the user a note: the tmux route was eligible and tmux was not on PATH. The
// launch still lands in the embedded slot, so Embedded stays set.
func TestNewFlowLaunchContextFallsBackWhenTmuxIsMissing(t *testing.T) {
	record := launchContextAutofixRecord()
	ctx, decision, err := newFlowLaunchContext(autofixTarget{
		LaunchID: "launch-1",
		Record:   record,
		PlanPath: record.PlanPath,
	}, launchContextVariantSettings(), flowLaunchRouting{
		Backend:       config.LaunchBackendTmux,
		TmuxAvailable: func() bool { return false },
	})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if decision != (flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded, FallbackNote: tmuxFallbackNote}) {
		t.Fatalf("decision = %#v, want the embedded route with the tmux fallback note", decision)
	}
	if !ctx.Embedded {
		t.Fatal("a tmux fallback lands in the embedded slot, so Embedded must stay set")
	}
}

// TestNewFlowLaunchContextRendersTheAutofixPromptWithThePinnedBinary is the same
// ordering guard the repair prompt has: the builder stamps last, so the prompt
// has to render from the pin rather than from the not-yet-stamped ctx.Executable.
func TestNewFlowLaunchContextRendersTheAutofixPromptWithThePinnedBinary(t *testing.T) {
	stubRetainLaunchPin(t)
	record := launchContextAutofixRecord()
	settings := launchContextVariantSettings()
	settings.PromptTemplates = FlowPromptTemplates{Autofix: "run {approach_bin} autofix pr #{pr_number}"}
	settings.Pin = controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
		Digest:         "abc123def456",
	}

	ctx, _, err := newFlowLaunchContext(autofixTarget{
		LaunchID: "launch-1",
		Record:   record,
		PlanPath: record.PlanPath,
	}, settings, flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if !strings.Contains(ctx.InitialPrompt, settings.Pin.ExecutablePath) {
		t.Fatalf("autofix prompt does not name the pinned binary:\n%s", ctx.InitialPrompt)
	}
	if ctx.Executable != settings.Pin.ExecutablePath {
		t.Fatalf("autofix context was not stamped: %#v", ctx)
	}
}

// TestNewFlowLaunchContextRejectsIncompleteAutofixPayloads pins autofix's
// validation as the strictest of the three: the worktree is the point of the
// shortcut, and a zero PR number would emit `autofix pr #0` — the one string
// this path may never produce.
func TestNewFlowLaunchContextRejectsIncompleteAutofixPayloads(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*autofixTarget)
	}{
		{name: "missing launch id", mutate: func(target *autofixTarget) { target.LaunchID = "  " }},
		{name: "missing flow id", mutate: func(target *autofixTarget) { target.Record.FlowID = "  " }},
		{name: "missing worktree path", mutate: func(target *autofixTarget) { target.Record.WorktreePath = " " }},
		{name: "missing pr number", mutate: func(target *autofixTarget) { target.Record.PR.Number = 0 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := launchContextAutofixRecord()
			target := autofixTarget{LaunchID: "launch-1", Record: record, PlanPath: record.PlanPath}
			tt.mutate(&target)

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), flowLaunchRouting{})
			if err == nil {
				t.Fatalf("incomplete payload built a context: %#v", ctx)
			}
			if decision != (flowLaunchRouteDecision{}) {
				t.Fatalf("failed build returned decision %#v", decision)
			}
		})
	}
}

// TestNewFlowLaunchContextFallsBackToTheReadStageRepoPath pins autofix's one
// path precedence: the record's repo path wins, and the read stage's resolution
// is used only when the record has none.
func TestNewFlowLaunchContextFallsBackToTheReadStageRepoPath(t *testing.T) {
	record := launchContextAutofixRecord()
	record.RepoPath = ""
	ctx, _, err := newFlowLaunchContext(autofixTarget{
		LaunchID:         "launch-1",
		Record:           record,
		PlanPath:         record.PlanPath,
		FallbackRepoPath: "/dev/read-stage",
	}, launchContextVariantSettings(), flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.RepoPath != "/dev/read-stage" {
		t.Fatalf("repo path = %q, want the read stage's", ctx.RepoPath)
	}
}

// launchContextSavedSessionResumeTarget is the saved-resume fixture: the
// reserved Flow record plus the exact-key refreshed session. They differ in
// every overlapping field on purpose — the variant row is what pins that the
// session wins everywhere except the Flow ID.
func launchContextSavedSessionResumeTarget() (flowstore.FlowRecord, sessions.SessionRecord) {
	record := launchContextVariantRecord()
	record.Branch = "flow/record-branch"
	record.Commit = "recordcommit"
	return record, sessions.SessionRecord{
		Provider:     sessions.Provider("codex-cli"),
		SessionID:    "session-1",
		CWD:          "/dev/alpha-worktree/nested",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree",
		Branch:       "flow/session-branch",
		Commit:       "sessioncommit",
		PlanID:       "plan-session",
		PlanPath:     "/state/session-plan.md",
		FlowID:       record.FlowID,
	}
}

// TestNewFlowLaunchContextRejectsIncompleteSavedSessionResumePayloads pins the
// four fields the role cannot do without. Provider and session ID are the strict
// ones: resumeSessionIDForContext refuses a blank ResumeSessionID, and an empty
// Command would leave agentCommandSpec with no binary to resume.
func TestNewFlowLaunchContextRejectsIncompleteSavedSessionResumePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		mutate func(*savedSessionResumeTarget)
	}{
		{name: "launch id", mutate: func(target *savedSessionResumeTarget) { target.LaunchID = "  " }},
		{name: "flow id", mutate: func(target *savedSessionResumeTarget) { target.Record.FlowID = "  " }},
		{name: "provider", mutate: func(target *savedSessionResumeTarget) { target.Session.Provider = "  " }},
		{name: "session id", mutate: func(target *savedSessionResumeTarget) { target.Session.SessionID = "  " }},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			record, session := launchContextSavedSessionResumeTarget()
			target := savedSessionResumeTarget{LaunchID: "launch-1", Record: record, Session: session}
			missing.mutate(&target)

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), flowLaunchRouting{})
			if !errors.Is(err, errIncompleteFlowLaunchTarget) {
				t.Fatalf("err = %v, want errIncompleteFlowLaunchTarget (context %#v)", err, ctx)
			}
			if decision != (flowLaunchRouteDecision{}) {
				t.Fatalf("failed build returned decision %#v", decision)
			}
		})
	}
}

// TestNewFlowLaunchContextResolvesSavedSessionResumeWorkingDir pins the ladder
// the prepare stage used to own: the session's cwd wins, the worktree is the
// fallback, and neither is a refusal whose wording is what the user reads.
func TestNewFlowLaunchContextResolvesSavedSessionResumeWorkingDir(t *testing.T) {
	for _, tt := range []struct {
		name     string
		cwd      string
		worktree string
		want     string
		wantErr  error
	}{
		{name: "cwd wins", cwd: "/dev/session-cwd", worktree: "/dev/alpha-worktree", want: "/dev/session-cwd"},
		{name: "blank cwd falls back to the worktree", cwd: "  ", worktree: "/dev/alpha-worktree", want: "/dev/alpha-worktree"},
		{name: "neither is refused", cwd: "", worktree: "", wantErr: errSavedSessionResumeNoWorkingDir},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record, session := launchContextSavedSessionResumeTarget()
			session.CWD = tt.cwd
			session.WorktreePath = tt.worktree

			ctx, decision, err := newFlowLaunchContext(savedSessionResumeTarget{
				LaunchID: "launch-1", Record: record, Session: session,
			}, launchContextVariantSettings(), flowLaunchRouting{})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				// The message is the user-visible status the prepare stage used to
				// set inline, so it is pinned as copy, not just as an identity.
				if err.Error() != "Session has no worktree path or cwd to resume from" {
					t.Fatalf("refusal message = %q", err.Error())
				}
				if decision != (flowLaunchRouteDecision{}) {
					t.Fatalf("failed build returned decision %#v", decision)
				}
				return
			}
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx.WorkingDir != tt.want {
				t.Fatalf("working dir = %q, want %q", ctx.WorkingDir, tt.want)
			}
		})
	}
}

// TestNewFlowLaunchContextKeepsTheSavedSessionIDVerbatim is the counterpart to
// the trim in the emptiness check. resumeSessionIDForContext returns
// ctx.ResumeSessionID unmodified for this role — every other path trims — so a
// builder that assigned the trimmed value would silently rewrite the ID the
// provider stored and resume the wrong session, or none.
func TestNewFlowLaunchContextKeepsTheSavedSessionIDVerbatim(t *testing.T) {
	record, session := launchContextSavedSessionResumeTarget()
	session.SessionID = "  session-1  "

	ctx, _, err := newFlowLaunchContext(savedSessionResumeTarget{
		LaunchID: "launch-1", Record: record, Session: session,
	}, launchContextVariantSettings(), flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.ResumeSessionID != "  session-1  " {
		t.Fatalf("resume session id = %q, want the untrimmed session ID", ctx.ResumeSessionID)
	}
}

// launchContextPhaseResumeTarget is the phase-resume fixture: the variant
// record plus a running phase whose kind is set explicitly, so the row pins
// what SemanticKind read off the phase rather than what it inferred from the
// phase ID. PersistedRecord carries the same phase the read stage saw, which is
// the ordinary case; the terminal row is what moves them apart.
func launchContextPhaseResumeTarget() phaseResumeTarget {
	record := launchContextVariantRecord()
	phase := flowstore.FlowPhase{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseRunning,
		Order:   1,
	}
	record.Phases = []flowstore.FlowPhase{phase}
	return phaseResumeTarget{
		LaunchID:         "launch-1",
		Record:           record,
		PersistedRecord:  record,
		ReadPhase:        phase,
		PhaseID:          phase.PhaseID,
		Command:          "codex",
		ResumeSessionID:  "session-1",
		PlanPath:         record.PlanPath,
		FallbackRepoPath: "/dev/read-stage",
	}
}

// launchContextPhaseResumeContext is the embedded context every phase-resume
// row starts from: the tmux row clears Embedded, and the terminal row flips
// FlowPhaseTerminal. Model, ReasoningEffort and InitialPrompt stay empty —
// agentCommandSpec refuses a resume that carries a model, so the snapshot's
// gpt-5/high must not reach the context.
func launchContextPhaseResumeContext(record flowstore.FlowRecord) actions.AgentLaunchContext {
	return actions.AgentLaunchContext{
		Command:           "codex",
		LaunchID:          "launch-1",
		RepoPath:          record.RepoPath,
		WorktreePath:      record.WorktreePath,
		WorkingDir:        record.WorktreePath,
		Branch:            record.Branch,
		Commit:            record.Commit,
		SessionStateRoot:  "/state",
		ResumeSessionID:   "session-1",
		PlanID:            record.PlanID,
		PlanPath:          record.PlanPath,
		FlowID:            record.FlowID,
		FlowPhaseID:       "implementation",
		FlowPhaseKind:     string(flowstore.KindImplementation),
		FlowPhaseTerminal: false,
		Embedded:          true,
		FlowLaunchTracked: true,
	}
}

// TestNewFlowLaunchContextFallsBackToTheReadPhaseWhenTheWriteReturnsNone pins
// the guard that moved into the builder with the rest of phase resume's
// context. Seams routinely return phase-less records, and an unguarded lookup
// would answer with the zero phase — kind empty, terminal false — for every one
// of them, quietly clearing the flag that stops a failed resume from regressing
// a completed phase.
func TestNewFlowLaunchContextFallsBackToTheReadPhaseWhenTheWriteReturnsNone(t *testing.T) {
	target := launchContextPhaseResumeTarget()
	target.ReadPhase.Status = flowstore.PhaseCompleted
	target.PersistedRecord.Phases = nil

	ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings(), flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if !ctx.FlowPhaseTerminal {
		t.Fatal("a phase-less persisted record must fall back to the read phase, not to the zero phase")
	}
	if ctx.FlowPhaseKind != string(flowstore.KindImplementation) {
		t.Fatalf("phase kind = %q, want the read phase's", ctx.FlowPhaseKind)
	}
}

// TestNewFlowLaunchContextRejectsIncompletePhaseResumePayloads pins the six
// fields this role cannot do without. The worktree is required even though the
// read stage already refuses a worktree-less resume: this role chdirs into it,
// so it is the builder-side invariant rather than a refusal the user can reach.
func TestNewFlowLaunchContextRejectsIncompletePhaseResumePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		mutate func(*phaseResumeTarget)
	}{
		{name: "launch id", mutate: func(target *phaseResumeTarget) { target.LaunchID = "  " }},
		{name: "flow id", mutate: func(target *phaseResumeTarget) { target.Record.FlowID = "  " }},
		{name: "worktree path", mutate: func(target *phaseResumeTarget) { target.Record.WorktreePath = " " }},
		{name: "phase id", mutate: func(target *phaseResumeTarget) { target.PhaseID = "  " }},
		{name: "command", mutate: func(target *phaseResumeTarget) { target.Command = "  " }},
		{name: "resume session id", mutate: func(target *phaseResumeTarget) { target.ResumeSessionID = "  " }},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			target := launchContextPhaseResumeTarget()
			missing.mutate(&target)

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), flowLaunchRouting{})
			if !errors.Is(err, errIncompleteFlowLaunchTarget) {
				t.Fatalf("err = %v, want errIncompleteFlowLaunchTarget (context %#v)", err, ctx)
			}
			if decision != (flowLaunchRouteDecision{}) {
				t.Fatalf("failed build returned decision %#v", decision)
			}
		})
	}
}

// TestNewFlowLaunchContextKeepsThePhaseResumeSessionIDVerbatim is the
// counterpart to the trim in the emptiness check, for the reason the
// saved-session row gives: a builder that assigned the trimmed value would
// rewrite the ID the provider stored and resume the wrong session, or none.
func TestNewFlowLaunchContextKeepsThePhaseResumeSessionIDVerbatim(t *testing.T) {
	target := launchContextPhaseResumeTarget()
	target.ResumeSessionID = "  session-1  "

	ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings(), flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.ResumeSessionID != "  session-1  " {
		t.Fatalf("resume session id = %q, want the untrimmed session ID", ctx.ResumeSessionID)
	}
}

// launchContextTrackedPhaseTarget is the tracked-phase fixture: the variant
// record plus a running implementation phase, with PersistedRecord carrying a
// non-zero UpdatedAt so the reservation override rule is live by default. Rows
// that need the zero-time guard clear it explicitly.
func launchContextTrackedPhaseTarget() trackedPhaseTarget {
	record := launchContextVariantRecord()
	phase := flowstore.FlowPhase{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseRunning,
		Order:   1,
	}
	record.Phases = []flowstore.FlowPhase{phase}
	return trackedPhaseTarget{
		LaunchID:        "launch-1",
		Record:          record,
		PersistedRecord: record,
		Phase:           phase,
		Agent: agent.Settings{
			Command:         "codex",
			Model:           "gpt-5",
			ReasoningEffort: "high",
		},
		RepoPath:     record.RepoPath,
		WorktreePath: record.WorktreePath,
		PlanPath:     record.PlanPath,
	}
}

// launchContextTrackedPhaseContext is the embedded, interactive, manual context
// every tracked-phase row starts from. WorkingDir stays empty on purpose: this
// role has never set it, and actions falls back to WorktreePath, so adding it
// here would be a silent behavior change rather than a tidy-up.
func launchContextTrackedPhaseContext(target trackedPhaseTarget) actions.AgentLaunchContext {
	return actions.AgentLaunchContext{
		Command:           "codex",
		Model:             "gpt-5",
		ReasoningEffort:   "high",
		LaunchID:          "launch-1",
		RepoPath:          target.RepoPath,
		WorktreePath:      target.WorktreePath,
		Branch:            target.Record.Branch,
		Commit:            target.Record.Commit,
		SessionStateRoot:  "/state",
		PlanID:            target.Record.PlanID,
		PlanPath:          target.PlanPath,
		FlowID:            target.Record.FlowID,
		FlowPhaseID:       "implementation",
		FlowPhaseKind:     string(flowstore.KindImplementation),
		FlowLaunchTracked: true,
		Embedded:          true,
		InitialPrompt: flowPhasePrompt(
			target.Record, target.Phase, target.PlanPath, target.PlanBody, FlowPromptTemplates{}, ""),
	}
}

// TestNewFlowLaunchContextPinsTheTrackedPhaseVariants covers the four reachable
// tracked-phase variants from docs/flow-launch-variant-matrix.md. It compares
// whole structs for the reason the shared variant table does: a field this role
// starts setting has to be declared here before it can ship.
func TestNewFlowLaunchContextPinsTheTrackedPhaseVariants(t *testing.T) {
	for _, variant := range []struct {
		name     string
		mutate   func(*trackedPhaseTarget)
		routing  flowLaunchRouting
		want     func(trackedPhaseTarget) actions.AgentLaunchContext
		decision flowLaunchRouteDecision
	}{
		{
			// V1: the ordinary g launch. The reservation answered with an
			// interactive record, so the requested value and the persisted one
			// agree and Headless stays false.
			name:     "tracked phase manual embedded",
			want:     launchContextTrackedPhaseContext,
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// V2: the reservation override. The caller asked for an interactive
			// launch and the persisted record says headless, so the persisted
			// value wins. A row that merely requested headless would pass without
			// the rule.
			name: "tracked phase manual headless",
			mutate: func(target *trackedPhaseTarget) {
				target.RequestedHeadless = false
				target.PersistedRecord.Headless = true
			},
			want: func(target trackedPhaseTarget) actions.AgentLaunchContext {
				ctx := launchContextTrackedPhaseContext(target)
				ctx.Headless = true
				return ctx
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
		{
			// V3: interactive plus a tmux backend with tmux on PATH. Embedded
			// clears because a tmux window has no dock to prefill.
			name: "tracked phase manual tmux",
			routing: flowLaunchRouting{
				Backend:       config.LaunchBackendTmux,
				TmuxAvailable: func() bool { return true },
			},
			want: func(target trackedPhaseTarget) actions.AgentLaunchContext {
				ctx := launchContextTrackedPhaseContext(target)
				ctx.Embedded = false
				return ctx
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteTmux},
		},
		{
			// V4: AutoMode. The persisted record says interactive and the request
			// says headless, and headless wins — auto launches skip the
			// reservation override entirely.
			name: "tracked phase auto headless",
			mutate: func(target *trackedPhaseTarget) {
				target.AutoLaunch = true
				target.RequestedHeadless = true
				target.PersistedRecord.Headless = false
			},
			want: func(target trackedPhaseTarget) actions.AgentLaunchContext {
				ctx := launchContextTrackedPhaseContext(target)
				ctx.FlowAutoLaunch = true
				ctx.Headless = true
				return ctx
			},
			decision: flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded},
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			target := launchContextTrackedPhaseTarget()
			if variant.mutate != nil {
				variant.mutate(&target)
			}

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), variant.routing)
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if want := variant.want(target); ctx != want {
				t.Fatalf("context = %#v, want %#v", ctx, want)
			}
			if decision != variant.decision {
				t.Fatalf("decision = %#v, want %#v", decision, variant.decision)
			}
			if role := actions.FlowLaunchRoleOf(ctx); role != target.role() {
				t.Fatalf("FlowLaunchRoleOf(built) = %v, want %v", role, target.role())
			}
		})
	}
}

// TestNewFlowLaunchContextKeepsRequestedHeadlessOnAZeroTimeReservation pins the
// guard that travelled with the rule: a zero-time partial result from an
// injected launcher seam is not a persisted reservation, so it cannot replace
// the requested preference.
func TestNewFlowLaunchContextKeepsRequestedHeadlessOnAZeroTimeReservation(t *testing.T) {
	for _, tt := range []struct {
		name              string
		requestedHeadless bool
		persistedHeadless bool
	}{
		{name: "requested interactive", requestedHeadless: false, persistedHeadless: true},
		{name: "requested headless", requestedHeadless: true, persistedHeadless: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target := launchContextTrackedPhaseTarget()
			target.RequestedHeadless = tt.requestedHeadless
			target.PersistedRecord.Headless = tt.persistedHeadless
			target.PersistedRecord.UpdatedAt = time.Time{}

			ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings(), flowLaunchRouting{})
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx.Headless != tt.requestedHeadless {
				t.Fatalf("headless = %v, want the requested %v", ctx.Headless, tt.requestedHeadless)
			}
		})
	}
}

// TestNewFlowLaunchContextRejectsIncompleteTrackedPhasePayloads pins the
// builder-side invariants. These are defensive: prepare's
// flowPhaseLaunchNoWorktreeStatus refusal and preflight's agent.Validate still
// fire first and keep their own messages.
func TestNewFlowLaunchContextRejectsIncompleteTrackedPhasePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		mutate func(*trackedPhaseTarget)
	}{
		{name: "launch id", mutate: func(target *trackedPhaseTarget) { target.LaunchID = "  " }},
		{name: "flow id", mutate: func(target *trackedPhaseTarget) { target.Record.FlowID = "  " }},
		{name: "worktree path", mutate: func(target *trackedPhaseTarget) { target.WorktreePath = " " }},
		{name: "phase id", mutate: func(target *trackedPhaseTarget) { target.Phase.PhaseID = "  " }},
		{name: "agent command", mutate: func(target *trackedPhaseTarget) { target.Agent.Command = "  " }},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			target := launchContextTrackedPhaseTarget()
			missing.mutate(&target)

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), flowLaunchRouting{})
			if !errors.Is(err, errIncompleteFlowLaunchTarget) {
				t.Fatalf("err = %v, want errIncompleteFlowLaunchTarget (context %#v)", err, ctx)
			}
			if decision != (flowLaunchRouteDecision{}) {
				t.Fatalf("failed build returned decision %#v", decision)
			}
		})
	}
}

// launchContextCreatePhaseTarget is the create fixture. Its record carries no
// worktree, branch or commit on purpose: the just-written record often has none
// yet, and the bootstrap results travelling alongside it are what
// flowStartPromptRecord merges in for the prompt.
func launchContextCreatePhaseTarget() createPhaseTarget {
	return createPhaseTarget{
		LaunchID: "launch-1",
		Record: flowstore.FlowRecord{
			FlowID:    "flow-1",
			Title:     "Flow one",
			Status:    flowstore.StatusInProgress,
			UpdatedAt: time.Now(),
		},
		Request: FlowStartRequest{
			RepoPath:     "/dev/alpha",
			Title:        "Flow one",
			Instructions: "ship the thing",
			BaseRef:      "main",
		},
		Worktree: actions.FlowWorktreeCreateResult{
			WorktreePath: "/dev/alpha-worktree",
			Branch:       "flow/one",
		},
		Commit: "abc123",
		Phase: flowstore.FlowPhase{
			PhaseID: "plan",
			Title:   "Plan",
			Kind:    flowstore.KindPlan,
			Status:  flowstore.PhaseRunning,
			Order:   1,
		},
		Agent: agent.Settings{Command: "codex", Model: "gpt-5", ReasoningEffort: "high"},
	}
}

// launchContextCreatePhaseContext computes the expectation from the target for
// the reason the repair prompt helper does: what the rows pin is the mapping,
// not the prompt copy. The zero fields it never names — FlowLaunchTracked,
// Embedded, PlanID, PlanPath, WorkingDir — are the load-bearing ones.
func launchContextCreatePhaseContext(target createPhaseTarget) actions.AgentLaunchContext {
	promptRecord := flowStartPromptRecord(target.Record, target.Request, target.Worktree, target.Commit)
	return actions.AgentLaunchContext{
		Command:           "codex",
		Model:             "gpt-5",
		ReasoningEffort:   "high",
		LaunchID:          "launch-1",
		RepoPath:          target.Request.RepoPath,
		WorktreePath:      target.Worktree.WorktreePath,
		Branch:            target.Worktree.Branch,
		Commit:            target.Commit,
		SessionStateRoot:  "/state",
		PlanPhaseID:       target.Phase.PhaseID,
		PlanPhaseTitle:    target.Phase.Title,
		PlanPhaseStatus:   string(flowstore.PhaseRunning),
		FlowID:            target.Record.FlowID,
		FlowPhaseID:       target.Phase.PhaseID,
		FlowPhaseKind:     string(flowstore.SemanticKind(target.Phase)),
		Headless:          target.Record.Headless,
		Embedded:          true,
		FlowLaunchTracked: true,
		InitialPrompt: initialFlowLaunchPrompt(
			promptRecord, target.Phase, FlowPromptTemplates{}, ""),
	}
}

// TestNewFlowLaunchContextTitlesTheCreatePhaseByIDWhenItHasNoTitle pins the
// fallback that travelled into the builder with the PlanPhase trio: the prefill
// names the phase, so an untitled startup root must show its ID rather than an
// empty label.
func TestNewFlowLaunchContextTitlesTheCreatePhaseByIDWhenItHasNoTitle(t *testing.T) {
	target := launchContextCreatePhaseTarget()
	target.Phase.Title = "   "

	ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings(), flowLaunchRouting{})
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.PlanPhaseTitle != target.Phase.PhaseID {
		t.Fatalf("plan phase title = %q, want the phase ID %q", ctx.PlanPhaseTitle, target.Phase.PhaseID)
	}
}

// TestNewFlowLaunchContextRejectsIncompleteCreatePhasePayloads pins the
// builder-side invariants. They are defensive: admission's launch-proof check
// and resolveFlowStartPhaseAgentSettings refuse first and keep their own
// messages.
func TestNewFlowLaunchContextRejectsIncompleteCreatePhasePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		mutate func(*createPhaseTarget)
	}{
		{name: "launch id", mutate: func(target *createPhaseTarget) { target.LaunchID = "  " }},
		{name: "flow id", mutate: func(target *createPhaseTarget) { target.Record.FlowID = "  " }},
		{name: "phase id", mutate: func(target *createPhaseTarget) { target.Phase.PhaseID = "  " }},
		{name: "agent command", mutate: func(target *createPhaseTarget) { target.Agent.Command = "  " }},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			target := launchContextCreatePhaseTarget()
			missing.mutate(&target)

			ctx, decision, err := newFlowLaunchContext(
				target, launchContextVariantSettings(), flowLaunchRouting{})
			if !errors.Is(err, errIncompleteFlowLaunchTarget) {
				t.Fatalf("err = %v, want errIncompleteFlowLaunchTarget (context %#v)", err, ctx)
			}
			if decision != (flowLaunchRouteDecision{}) {
				t.Fatalf("failed build returned decision %#v", decision)
			}
		})
	}
}
