package model

import (
	"errors"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// flowLaunchTarget is the payload half of a Flow launch: the role plus exactly
// the inputs that role needs. It is a sum rather than one wide struct so a
// role cannot be paired with another role's payload — the pairing is
// unrepresentable, not merely unreached.
type flowLaunchTarget interface {
	role() actions.FlowLaunchRole
}

// worktreeAgentTarget is the generic untracked worktree agent started by s. It
// carries the whole record rather than pre-split scalars so the mapping from
// record fields onto context fields — including WorkingDir being the worktree,
// not the repo — lives inside the builder instead of at the call site.
type worktreeAgentTarget struct {
	LaunchID string
	Record   flowstore.FlowRecord
	// PlanPath is the resolved markdown path for the Flow's linked plan. It is
	// separate from the record because the prepare stage may have had to
	// resolve it from PlanID, and that resolution is admission's job.
	PlanPath string
}

func (worktreeAgentTarget) role() actions.FlowLaunchRole { return actions.RoleWorktreeAgent }

// repairTarget is the untracked repair session started by R. Its record is the
// reserved one — the authority for branch, commit, plan, headless and the
// prompt — while the read stage's resolutions ride alongside as fallbacks with
// a defined precedence. They stay separate fields rather than being folded into
// a synthesized record because flattening them would hide the ladder the path
// resolution depends on.
type repairTarget struct {
	// LaunchID is the admission token, never a fresh ID: every LaunchID-keyed
	// fence downstream is on it.
	LaunchID string
	Record   flowstore.FlowRecord
	// Agent is the obstruction phase's resolved settings. Repair's command,
	// model and effort come from what that phase persisted rather than from the
	// TUI preference snapshot, and resolving them needs the reserved record, so
	// the resolution belongs to admission and travels on the payload. Folding it
	// into the settings snapshot instead would make that snapshot mean two
	// different things depending on which caller filled it.
	Agent agent.Settings
	// FallbackRepoPath and FallbackWorktreePath are the read stage's resolved
	// directories, tried after the record's own when the record points at
	// directories that are gone.
	FallbackRepoPath     string
	FallbackWorktreePath string
	// PlanID and PlanPath are the read stage's plan resolution. PlanPath is kept
	// only while PlanID still matches the record's, so a Flow that has moved to
	// another plan cannot carry the old plan's markdown into the repair session.
	PlanID   string
	PlanPath string
}

func (repairTarget) role() actions.FlowLaunchRole { return actions.RoleRepair }

// autofixTarget is the prompted, PR-gated untracked agent started by U. Its
// record is the reserved one when the reservation answered: it is the authority
// for the worktree, the headless preference, and the PR number the prompt
// names. The PR number is read off that record rather than travelling as its own
// scalar so one launch can never carry two sources for it.
type autofixTarget struct {
	// LaunchID is the admission token, never a fresh ID: every LaunchID-keyed
	// fence and the tmux window registry key on it.
	LaunchID string
	Record   flowstore.FlowRecord
	// PlanPath is the record's plan path verbatim, with no PlanMarkdownPath
	// resolution: this agent needs no plan body, so a plan-linked Flow launching
	// with an empty PlanPath is expected.
	PlanPath string
	// FallbackRepoPath is the read stage's resolved repository, used only when
	// the record carries none of its own.
	FallbackRepoPath string
}

func (autofixTarget) role() actions.FlowLaunchRole { return actions.RoleAutofix }

// savedSessionResumeTarget is the phase-untracked resume of a Flow's saved
// session, started from the Sessions pane, the dock picker or an inline
// worktree row. It carries the whole refreshed session record for the reason
// worktreeAgentTarget carries its record: the mapping from session fields onto
// context fields — WorkingDir being CWD-then-worktree, Command coming from the
// provider — is the part that belongs inside the builder.
type savedSessionResumeTarget struct {
	// LaunchID is the admission token, never a fresh ID: the reservation and
	// every LaunchID-keyed fence downstream are already on it.
	LaunchID string
	// Record is the reserved Flow record. Only its FlowID reaches the context —
	// the resumed session's own branch, commit and plan are what the agent gets
	// back, not the Flow's current ones.
	Record flowstore.FlowRecord
	// Session is the exact-key refreshed session record. It is the authority for
	// command and provider identity here, unlike every other role, which takes
	// them from the settings snapshot.
	Session sessions.SessionRecord
}

func (savedSessionResumeTarget) role() actions.FlowLaunchRole {
	return actions.RoleSavedSessionResume
}

// phaseResumeTarget is the phase-tracked resume of a Flow phase's saved
// provider session, started by r. Unlike savedSessionResumeTarget it re-enters
// the phase itself: the launch is tracked, carries the phase's ID, kind and
// terminal flag, and its failure handling turns on that flag. It carries two
// records rather than one because the resume reads a record and then writes
// one, and the two answer different questions.
type phaseResumeTarget struct {
	// LaunchID is the admission token, never a fresh ID: the prefill-failure
	// re-reservation and the failure-persisted fence both key on it.
	LaunchID string
	// Record is the read stage's record — the authority for branch, commit,
	// plan ID, worktree and Flow ID. Prepare never refreshes it, so the
	// worktree it names is the same value the read stage validated.
	Record flowstore.FlowRecord
	// PersistedRecord is the record AddPhaseLaunchID returned, consulted only
	// for the phase. It decides whether this resume preserved a terminal phase
	// or reopened a running one, and seams routinely return phase-less records,
	// which is why the lookup below is guarded rather than direct.
	PersistedRecord flowstore.FlowRecord
	// ReadPhase is the read stage's phase, used when PersistedRecord carries
	// none. Falling back rather than defaulting to the zero phase is what keeps
	// a phase-less write from silently reporting "not terminal".
	ReadPhase flowstore.FlowPhase
	PhaseID   string
	// Command is the provider the session was written by, resolved by the read
	// stage — not the settings snapshot's preference, which may have moved on
	// since the session was recorded.
	Command string
	// ResumeSessionID is the provider's own session ID, carried verbatim: the
	// trim in the validation below is an emptiness test, never a rewrite.
	ResumeSessionID string
	// PlanPath is the read stage's resolution, passed through with no
	// PlanMarkdownPath step, exactly as resume has always resolved it.
	PlanPath string
	// FallbackRepoPath is the intent's repository, used only when the record
	// carries none of its own.
	FallbackRepoPath string
}

func (phaseResumeTarget) role() actions.FlowLaunchRole { return actions.RolePhaseResume }

// errIncompleteFlowLaunchTarget reports a payload that upstream admission
// should already have guaranteed. The builder still checks: it is the one
// place every Flow launch context is constructed, so it is the only place the
// launch invariants can be enforced for all of them at once.
var errIncompleteFlowLaunchTarget = errors.New("flow launch target is missing required fields")

// errSavedSessionResumeNoWorkingDir reports a session with neither a cwd nor a
// worktree to resume into. Its message is what the user sees, so it is the
// prepare stage's wording verbatim rather than a builder-shaped restatement.
var errSavedSessionResumeNoWorkingDir = errors.New("Session has no worktree path or cwd to resume from")

// flowLaunchRouting is the routing input a role's builder decides against. It
// is a snapshot rather than a receiver read: a lifecycle attempt takes both at
// admission so its route is decided against what the attempt was admitted with,
// while Model reads config's value live.
type flowLaunchRouting struct {
	// Backend is the launch backend the attempt was admitted with.
	Backend string
	// TmuxAvailable is the snapshotted probe. A nil probe means the real PATH.
	TmuxAvailable func() bool
}

// flowLaunchRouteDecision is the route a role's builder chose plus the note the
// user is owed when it could not take the one it wanted. The note rides here
// rather than being recomputed by the caller because fellBack is a second
// output of the same rule: returning only the route would put two readers on
// one decision.
type flowLaunchRouteDecision struct {
	Route flowLaunchRoute
	// FallbackNote is tmuxFallbackNote exactly when the tmux route was eligible
	// and tmux was missing. Every other decline is by design, not a fallback.
	FallbackNote string
}

// newFlowLaunchContext builds the launch context and handoff route for target,
// stamping the launching build and registering the launch with the controller
// before returning. Callers submit a role payload and take back a finished
// context; they no longer decide which markers a role sets or which route it
// takes.
func newFlowLaunchContext(
	target flowLaunchTarget,
	settings flowLaunchAgentSettingsSnapshot,
	routing flowLaunchRouting,
) (actions.AgentLaunchContext, flowLaunchRouteDecision, error) {
	switch payload := target.(type) {
	case worktreeAgentTarget:
		return newWorktreeAgentLaunchContext(payload, settings)
	case repairTarget:
		return newRepairLaunchContext(payload, settings)
	case autofixTarget:
		return newAutofixLaunchContext(payload, settings, routing)
	case savedSessionResumeTarget:
		return newSavedSessionResumeLaunchContext(payload, settings)
	case phaseResumeTarget:
		return newPhaseResumeLaunchContext(payload, settings, routing)
	default:
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errIncompleteFlowLaunchTarget
	}
}

// newWorktreeAgentLaunchContext ignores routing: this kind's route is a
// constant, not an unrouted gap. The generic worktree agent always takes the
// embedded slot.
func newWorktreeAgentLaunchContext(
	target worktreeAgentTarget,
	settings flowLaunchAgentSettingsSnapshot,
) (actions.AgentLaunchContext, flowLaunchRouteDecision, error) {
	record := target.Record
	if strings.TrimSpace(target.LaunchID) == "" ||
		strings.TrimSpace(record.FlowID) == "" ||
		strings.TrimSpace(record.WorktreePath) == "" {
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errIncompleteFlowLaunchTarget
	}
	// The stamp is applied last, after every field is set: registration reads
	// the finished context to name the launch, so stamping early would change
	// the shape of what the controller records.
	return applyLaunchStamp(actions.AgentLaunchContext{
		Command: settings.Command, LaunchID: target.LaunchID,
		RepoPath: record.RepoPath, WorktreePath: record.WorktreePath, WorkingDir: record.WorktreePath,
		Branch: record.Branch, Commit: record.Commit, Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
		SessionStateRoot: settings.SessionStateRoot, PlanID: record.PlanID, PlanPath: target.PlanPath,
		FlowID: record.FlowID, FlowAgent: true, Embedded: true, Headless: false, InitialPrompt: "",
	}, settings.stamp()), flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded}, nil
}

// newAutofixLaunchContext is the first kind whose route is a decision rather
// than a constant, so it is the arm that takes routing. The order matters and is
// enforced by construction here: the record's headless preference is read into
// the context before the route is decided, because a headless launch is never
// tmux-eligible.
func newAutofixLaunchContext(
	target autofixTarget,
	settings flowLaunchAgentSettingsSnapshot,
	routing flowLaunchRouting,
) (actions.AgentLaunchContext, flowLaunchRouteDecision, error) {
	record := target.Record
	// The strictest validation of the three kinds. The worktree is required
	// rather than falling back to the repository root because the agent's cwd is
	// the whole point of the shortcut, and PR.Number > 0 is required because
	// nothing on this path may ever emit `autofix pr #0`.
	if strings.TrimSpace(target.LaunchID) == "" ||
		strings.TrimSpace(record.FlowID) == "" ||
		strings.TrimSpace(record.WorktreePath) == "" ||
		record.PR.Number <= 0 {
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errIncompleteFlowLaunchTarget
	}
	repoPath := strings.TrimSpace(record.RepoPath)
	if repoPath == "" {
		repoPath = target.FallbackRepoPath
	}
	ctx := applyLaunchStamp(actions.AgentLaunchContext{
		Command: settings.Command, LaunchID: target.LaunchID,
		RepoPath: repoPath, WorktreePath: record.WorktreePath,
		// WorkingDir is what actions turns into the agent's cwd, and it is the
		// worktree by construction: the validation above refuses a worktree-less
		// payload, as the U gate refuses a worktree-less Flow.
		WorkingDir: record.WorktreePath,
		Branch:     record.Branch, Commit: record.Commit,
		Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
		SessionStateRoot: settings.SessionStateRoot, PlanID: record.PlanID, PlanPath: target.PlanPath,
		FlowID: record.FlowID,
		// No FlowPhaseID, no FlowLaunchTracked, no FlowRepair, no FlowAgent: this
		// is the phase-untracked autofix agent. FlowAutofix is the explicit signal
		// the prefill boundary reads rather than inferring it from those absences.
		FlowAutofix: true, FlowAutofixPRNumber: record.PR.Number,
		Embedded: true, Headless: record.Headless,
		// The prompt renders settings.Pin.ExecutablePath, not the stamped
		// ctx.Executable, for the reason newRepairLaunchContext gives: the stamp
		// is applied last, so there is no stamped value to read yet.
		InitialPrompt: autofixPrompt(record, settings.PromptTemplates, settings.Pin.ExecutablePath),
	}, settings.stamp())
	tmuxRoute, fellBack := tmuxLaunchRouteFor(routing.Backend, routing.TmuxAvailable, ctx)
	if tmuxRoute {
		// A tmux window has no dock to prefill and renders its own output, so
		// clearing Embedded is what sends the prompt to argv instead.
		ctx.Embedded = false
		return ctx, flowLaunchRouteDecision{Route: flowLaunchRouteTmux}, nil
	}
	decision := flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded}
	if fellBack {
		decision.FallbackNote = tmuxFallbackNote
	}
	return ctx, decision, nil
}

// newRepairLaunchContext deliberately validates less than the worktree agent
// does: it requires a launch ID and a flow ID, but not a worktree path. Repair
// exists precisely for Flows whose recorded directories are gone, so copying the
// worktree agent's check here would refuse exactly the Flows repair is for. The
// no-usable-directory refusal is admission's, in the read stage. Like the
// worktree agent it ignores routing: tmuxRouteEligible refuses FlowRepair on its
// own, so repair's route is a constant rather than an unrouted gap.
func newRepairLaunchContext(
	target repairTarget,
	settings flowLaunchAgentSettingsSnapshot,
) (actions.AgentLaunchContext, flowLaunchRouteDecision, error) {
	record := target.Record
	if strings.TrimSpace(target.LaunchID) == "" || strings.TrimSpace(record.FlowID) == "" {
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errIncompleteFlowLaunchTarget
	}
	repoPath, worktreePath, ok := flowRepairLaunchPaths(
		record.RepoPath, record.WorktreePath, target.FallbackWorktreePath, target.FallbackRepoPath)
	if !ok {
		// Nothing on the ladder was a usable directory. Keeping the read stage's
		// pair verbatim leaves the repair agent pointed at what the user was
		// looking at rather than at a path the record only claims exists.
		repoPath = target.FallbackRepoPath
		worktreePath = target.FallbackWorktreePath
	}
	planPath := strings.TrimSpace(record.PlanPath)
	if planPath == "" && record.PlanID == target.PlanID {
		planPath = target.PlanPath
	}
	obstruction, _ := flowRepairObstructionForRecord(record)
	// The prompt renders settings.Pin.ExecutablePath, not the stamped
	// ctx.Executable: the stamp is applied last, so there is no stamped value to
	// read yet. This is the same string applyLaunchStamp would write, and the
	// empty-pin case is what flowPromptBinary already falls back from.
	return applyLaunchStamp(actions.AgentLaunchContext{
		Command: target.Agent.Command, LaunchID: target.LaunchID,
		RepoPath: repoPath, WorktreePath: worktreePath,
		Branch: record.Branch, Commit: record.Commit,
		Model: target.Agent.Model, ReasoningEffort: target.Agent.ReasoningEffort,
		SessionStateRoot: settings.SessionStateRoot, PlanID: record.PlanID, PlanPath: planPath,
		FlowID: record.FlowID, FlowRepair: true, Embedded: true, Headless: record.Headless,
		InitialPrompt: flowRepairPrompt(record, obstruction, settings.Pin.ExecutablePath),
	}, settings.stamp()), flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded}, nil
}

// newSavedSessionResumeLaunchContext ignores routing: tmuxRouteEligible refuses
// FlowSavedSessionResume outright, so this kind's route is the constant
// embedded slot rather than an unrouted gap. It is also the one role whose
// command and provider identity come from the session record instead of the
// settings snapshot — Model and ReasoningEffort must stay empty, because
// agentCommandSpec refuses a resume that carries them.
func newSavedSessionResumeLaunchContext(
	target savedSessionResumeTarget,
	settings flowLaunchAgentSettingsSnapshot,
) (actions.AgentLaunchContext, flowLaunchRouteDecision, error) {
	session := target.Session
	if strings.TrimSpace(target.LaunchID) == "" ||
		strings.TrimSpace(target.Record.FlowID) == "" ||
		strings.TrimSpace(string(session.Provider)) == "" ||
		strings.TrimSpace(session.SessionID) == "" {
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errIncompleteFlowLaunchTarget
	}
	workingDir := session.CWD
	if strings.TrimSpace(workingDir) == "" {
		workingDir = session.WorktreePath
	}
	if strings.TrimSpace(workingDir) == "" {
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errSavedSessionResumeNoWorkingDir
	}
	return applyLaunchStamp(actions.AgentLaunchContext{
		// The provider is the command: a resume re-enters the agent that wrote
		// the session, so the TUI's current preference has no say here.
		Command: string(session.Provider), LaunchID: target.LaunchID,
		RepoPath: session.RepoPath, WorktreePath: session.WorktreePath, WorkingDir: workingDir,
		Branch: session.Branch,
		// Commit is the session's, not a re-resolved one: agentCommandSpec skips
		// ResolveWorktreeCommit exactly when FlowSavedSessionResume is set, and
		// setting that marker here is what keeps that true.
		Commit:           session.Commit,
		SessionStateRoot: settings.SessionStateRoot,
		// Assigned untrimmed: resumeSessionIDForContext returns this field
		// verbatim for this role, so the trim above is a test for emptiness
		// only, never a rewrite of the ID the provider stored.
		ResumeSessionID: session.SessionID,
		PlanID:          session.PlanID, PlanPath: session.PlanPath,
		// The Flow ID is the reserved record's, while every other field is the
		// session's: the reservation is what the lease and the release are keyed
		// on. No Model or ReasoningEffort — agentCommandSpec errors with "model
		// cannot be set for session resume" if the snapshot's ever leak in.
		FlowID: target.Record.FlowID, FlowSavedSessionResume: true,
		Embedded: true, Headless: false, InitialPrompt: "",
	}, settings.stamp()), flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded}, nil
}

// newPhaseResumeLaunchContext is the second arm whose route is a decision
// rather than a constant, after autofix. Phase resume is genuinely
// tmux-eligible where repair and saved-session resume are not:
// tmuxRouteEligible reads only Headless, FlowRepair and Command, and this role
// sets neither marker, so deciding against the finished context is identical to
// the Command-only probe the call site used to make on the Model.
//
// Like saved-session resume it must leave Model and ReasoningEffort empty:
// agentCommandSpec refuses a resume that carries a model, and setting either
// would silently change the resumed command line.
func newPhaseResumeLaunchContext(
	target phaseResumeTarget,
	settings flowLaunchAgentSettingsSnapshot,
	routing flowLaunchRouting,
) (actions.AgentLaunchContext, flowLaunchRouteDecision, error) {
	record := target.Record
	// The worktree is required here even though the read stage already refuses a
	// worktree-less resume with flowPhaseResumeNoWorktreeStatus: this is the
	// builder-side invariant for a role that chdirs into the worktree, not a new
	// refusal path the user can reach.
	if strings.TrimSpace(target.LaunchID) == "" ||
		strings.TrimSpace(record.FlowID) == "" ||
		strings.TrimSpace(record.WorktreePath) == "" ||
		strings.TrimSpace(target.PhaseID) == "" ||
		strings.TrimSpace(target.Command) == "" ||
		strings.TrimSpace(target.ResumeSessionID) == "" {
		return actions.AgentLaunchContext{}, flowLaunchRouteDecision{}, errIncompleteFlowLaunchTarget
	}
	repoPath := strings.TrimSpace(record.RepoPath)
	if repoPath == "" {
		repoPath = target.FallbackRepoPath
	}
	// The persisted phase wins, and the guard is load-bearing: an unguarded
	// lookup would answer with the zero phase — "not terminal" — for every
	// phase-less record a seam returns, which is exactly the case the read
	// stage's phase exists to cover. Kind and terminal come from the same phase
	// so one cannot describe a phase the other no longer does.
	launchPhase := target.ReadPhase
	if persistedPhase, ok := flowPhaseByID(target.PersistedRecord, target.PhaseID); ok {
		launchPhase = persistedPhase
	}
	ctx := applyLaunchStamp(actions.AgentLaunchContext{
		// The command is the provider that wrote the session, resolved by the
		// read stage, for the reason saved-session resume takes it off the
		// session record: a resume re-enters the agent it came from.
		Command: target.Command, LaunchID: target.LaunchID,
		RepoPath: repoPath, WorktreePath: record.WorktreePath, WorkingDir: record.WorktreePath,
		Branch: record.Branch, Commit: record.Commit,
		// No Model or ReasoningEffort: the snapshot is in scope and deliberately
		// unread here.
		SessionStateRoot: settings.SessionStateRoot,
		ResumeSessionID:  target.ResumeSessionID,
		PlanID:           record.PlanID, PlanPath: target.PlanPath,
		FlowID: record.FlowID, FlowPhaseID: target.PhaseID,
		FlowPhaseKind:     string(flowstore.SemanticKind(launchPhase)),
		FlowPhaseTerminal: flowstore.PhaseStatusTerminal(string(launchPhase.Status)),
		Embedded:          true, FlowLaunchTracked: true,
	}, settings.stamp())
	tmuxRoute, fellBack := tmuxLaunchRouteFor(routing.Backend, routing.TmuxAvailable, ctx)
	if tmuxRoute {
		// A tmux window has no dock to prefill and renders its own output, so
		// clearing Embedded is what sends the resume to argv instead.
		ctx.Embedded = false
		return ctx, flowLaunchRouteDecision{Route: flowLaunchRouteTmux}, nil
	}
	decision := flowLaunchRouteDecision{Route: flowLaunchRouteEmbedded}
	if fellBack {
		decision.FallbackNote = tmuxFallbackNote
	}
	return ctx, decision, nil
}
