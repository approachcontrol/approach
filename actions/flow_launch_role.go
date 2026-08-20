package actions

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

// String names the role for diagnostics. An unknown value names itself rather
// than masquerading as a real role.
func (role FlowLaunchRole) String() string {
	switch role {
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
