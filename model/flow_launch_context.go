package model

import (
	"errors"
	"strings"

	"github.com/approachcontrol/approach/actions"
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
