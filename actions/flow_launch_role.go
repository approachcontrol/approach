package actions

import "strings"

// FlowLaunchRole names the kind of Flow-scoped launch a context represents. It
// is a closed enum: every Flow launch the TUI can start is exactly one of these
// seven, and the launch context's marker flags are the role's encoding rather
// than seven independent booleans a caller may combine freely.
//
// It lives in actions rather than model because actions is the package model
// imports, not the other way round, and because the role's consumers — resume
// identity, tmux role validation, the tracked-lease branch — already live here.
type FlowLaunchRole int

const (
	// RoleNone is the zero value: a context that is not a Flow launch at all,
	// or one whose markers name no role. It is deliberately the zero value so a
	// context nobody classified reads as "not a Flow launch" rather than as the
	// first real role.
	RoleNone FlowLaunchRole = 0
)

const (
	// RoleTrackedPhase is a phase-attached launch that reserves the Flow lease
	// and writes phase attempt history.
	RoleTrackedPhase FlowLaunchRole = iota + 1
	// RolePhaseResume resumes a phase's saved provider session.
	RolePhaseResume
	// RoleRepair is the untracked Flow-scoped repair session.
	RoleRepair
	// RoleAutofix is the prompted, PR-gated untracked agent.
	RoleAutofix
	// RoleWorktreeAgent is the generic untracked worktree agent started by s.
	RoleWorktreeAgent
	// RoleSavedSessionResume is a phase-untracked resume of a Flow's saved
	// provider session.
	RoleSavedSessionResume
	// RoleCreatePhase is the first launch of a freshly created Flow's startup
	// root, started by Plan Now and by Ready-Bead F. It appends rather than
	// slotting in beside RoleTrackedPhase so the existing values keep their
	// numbers.
	RoleCreatePhase
)

// Tracked reports whether this role reserves the Flow launch lease and writes
// phase attempt history. Stating it once here is what keeps the reservation and
// the failure-update refusal from each re-deriving "tracked" from raw markers
// and disagreeing.
func (role FlowLaunchRole) Tracked() bool {
	switch role {
	case RoleTrackedPhase, RolePhaseResume, RoleCreatePhase:
		return true
	default:
		return false
	}
}

// FlowLaunchRoleOf is the inverse of the builder: it recovers the role a
// finished launch context encodes. It is total — a context that names no role,
// including every non-Flow launch, classifies as RoleNone.
//
// The order is precedence, not arbitrary sequence. Contexts that set more than
// one marker are malformed, but they do reach the consumers this classifier
// serves, and the ordering here reproduces exactly what those consumers'
// hand-written predicates already answered for them: the repair / agent /
// saved-session chain first, then the phase-attached roles, then autofix.
//
// Embedded, Headless and FlowAutofixPRNumber are deliberately not inputs. They
// are transport and payload rather than role, and the one consumer that cares
// reads them itself alongside the role.
func FlowLaunchRoleOf(ctx AgentLaunchContext) FlowLaunchRole {
	switch {
	case ctx.FlowRepair:
		return RoleRepair
	case ctx.FlowAgent:
		return RoleWorktreeAgent
	case ctx.FlowSavedSessionResume:
		return RoleSavedSessionResume
	}
	// The phase roles need the Flow they attach to as well as the phase: their
	// consumers reserve that Flow's lease and write that Flow's phase, and a
	// phase ID with no Flow behind it has never done either.
	if strings.TrimSpace(ctx.FlowPhaseID) != "" && strings.TrimSpace(ctx.FlowID) != "" {
		switch {
		// ResumeSessionID is set by exactly one phase-attached arm, and
		// PlanPhaseID by exactly one other, so each discriminates its role.
		case ctx.ResumeSessionID != "":
			return RolePhaseResume
		case ctx.PlanPhaseID != "":
			return RoleCreatePhase
		default:
			return RoleTrackedPhase
		}
	}
	// Autofix carries its well-formedness conjuncts here rather than at the
	// call site: the marker on its own, without a Flow, with the tracked flag
	// set, or alongside any phase ID at all, names no reachable launch. The
	// phase-ID test here is untrimmed on purpose — a whitespace-only phase ID
	// makes no phase role above, but it is still enough to disqualify autofix,
	// which is what today's identity ladder answers for that shape.
	if ctx.FlowAutofix &&
		strings.TrimSpace(ctx.FlowID) != "" &&
		ctx.FlowPhaseID == "" &&
		!ctx.FlowLaunchTracked {
		return RoleAutofix
	}
	return RoleNone
}

// IsFlowLaunchContext reports whether this context carries any Flow identity at
// all. It is deliberately wider than a role check: a context may carry a Flow ID
// or the auto-launch marker without naming a launch role, and the lifecycle
// guard that reads this is a refusal guard, so it must fail closed on those
// rather than waving them onto the non-Flow route.
func IsFlowLaunchContext(ctx AgentLaunchContext) bool {
	return strings.TrimSpace(ctx.FlowID) != "" ||
		strings.TrimSpace(ctx.FlowPhaseID) != "" ||
		strings.TrimSpace(ctx.FlowPhaseKind) != "" ||
		ctx.FlowLaunchTracked || ctx.FlowAutoLaunch || ctx.FlowRepair || ctx.FlowAgent ||
		ctx.FlowSavedSessionResume || ctx.FlowAutofix || ctx.FlowPhaseTerminal
}

// String names the role for diagnostics. An unknown value names itself rather
// than masquerading as a real role.
func (role FlowLaunchRole) String() string {
	switch role {
	case RoleNone:
		return "no flow launch role"
	case RoleTrackedPhase:
		return "tracked phase"
	case RolePhaseResume:
		return "phase resume"
	case RoleRepair:
		return "repair"
	case RoleAutofix:
		return "autofix"
	case RoleWorktreeAgent:
		return "worktree agent"
	case RoleSavedSessionResume:
		return "saved session resume"
	case RoleCreatePhase:
		return "create phase"
	default:
		return "unknown flow launch role"
	}
}
