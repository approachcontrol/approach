package model

import (
	"errors"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
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

// errIncompleteFlowLaunchTarget reports a payload that upstream admission
// should already have guaranteed. The builder still checks: it is the one
// place every Flow launch context is constructed, so it is the only place the
// launch invariants can be enforced for all of them at once.
var errIncompleteFlowLaunchTarget = errors.New("flow launch target is missing required fields")

// newFlowLaunchContext builds the launch context and handoff route for target,
// stamping the launching build and registering the launch with the controller
// before returning. Callers submit a role payload and take back a finished
// context; they no longer decide which markers a role sets or which route it
// takes.
func newFlowLaunchContext(
	target flowLaunchTarget,
	settings flowLaunchAgentSettingsSnapshot,
) (actions.AgentLaunchContext, flowLaunchRoute, error) {
	switch payload := target.(type) {
	case worktreeAgentTarget:
		return newWorktreeAgentLaunchContext(payload, settings)
	case repairTarget:
		return newRepairLaunchContext(payload, settings)
	default:
		return actions.AgentLaunchContext{}, 0, errIncompleteFlowLaunchTarget
	}
}

func newWorktreeAgentLaunchContext(
	target worktreeAgentTarget,
	settings flowLaunchAgentSettingsSnapshot,
) (actions.AgentLaunchContext, flowLaunchRoute, error) {
	record := target.Record
	if strings.TrimSpace(target.LaunchID) == "" ||
		strings.TrimSpace(record.FlowID) == "" ||
		strings.TrimSpace(record.WorktreePath) == "" {
		return actions.AgentLaunchContext{}, 0, errIncompleteFlowLaunchTarget
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
	}, settings.stamp()), flowLaunchRouteEmbedded, nil
}

// newRepairLaunchContext deliberately validates less than the worktree agent
// does: it requires a launch ID and a flow ID, but not a worktree path. Repair
// exists precisely for Flows whose recorded directories are gone, so copying the
// worktree agent's check here would refuse exactly the Flows repair is for. The
// no-usable-directory refusal is admission's, in the read stage.
func newRepairLaunchContext(
	target repairTarget,
	settings flowLaunchAgentSettingsSnapshot,
) (actions.AgentLaunchContext, flowLaunchRoute, error) {
	record := target.Record
	if strings.TrimSpace(target.LaunchID) == "" || strings.TrimSpace(record.FlowID) == "" {
		return actions.AgentLaunchContext{}, 0, errIncompleteFlowLaunchTarget
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
	}, settings.stamp()), flowLaunchRouteEmbedded, nil
}
