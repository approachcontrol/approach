package actions

import (
	"errors"
	"fmt"
	"strings"
)

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
// serves, and the ordering reproduces what those consumers' hand-written
// predicates answered: the repair / agent / saved-session chain first, then the
// phase-attached roles, then autofix.
//
// One role is one answer, so on two malformed shapes the single answer cannot
// equal every old predicate's, and these are the deliberate divergences. A
// context setting FlowRepair alongside FlowAgent or FlowSavedSessionResume is
// repair here, so the detach policy allows detaching where the old
// marker-or-marker test refused. A phase-attached context without
// FlowLaunchTracked is a phase role here, so the reservation takes the Flow
// lease where the old test skipped it — the conservative direction, and the one
// that keeps the reservation agreeing with the failure update, which has always
// marked that shape's phase. Neither shape is emitted by any builder arm: the
// arms set exactly one marker each, and every phase-attached arm sets
// FlowLaunchTracked.
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
			// A resume that carries a phase but not the tracked marker is the
			// one mixed-marker shape the old failure-update refusal named
			// outright, and the reservation refused it too. Classifying it as a
			// phase resume would promote an explicitly untracked launch into a
			// tracked one — taking the Flow lease and marking the phase — so it
			// names no role instead, which is what both consumers answered for
			// it before.
			if !ctx.FlowLaunchTracked {
				return RoleNone
			}
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

// Prefills reports whether a launch in this role fills the embedded dock with
// its prompt rather than passing it as argv. The two resume roles carry no
// prompt to place, and RoleNone is not a Flow launch at all.
func (role FlowLaunchRole) Prefills() bool {
	switch role {
	case RoleTrackedPhase, RoleCreatePhase, RoleRepair, RoleWorktreeAgent, RoleAutofix:
		return true
	default:
		return false
	}
}

// errInvalidFlowLaunchRole is the seam's one rejection. Call sites wrap it in
// their own message so their existing diagnostics survive.
var errInvalidFlowLaunchRole = errors.New("invalid Flow launch role")

// flowLaunchRoleMarkers is one row of the "Marker fields" table in
// docs/flow-launch-variant-matrix.md §3, transcribed so the table is executable
// rather than prose. A role owns exactly the markers its row sets; every other
// marker on the context is a marker the role does not own.
type flowLaunchRoleMarkers struct {
	// phaseAttached roles address a phase of their Flow, so they require a
	// phase ID and may carry its kind. The rest own neither.
	phaseAttached bool
	tracked       bool
	repair        bool
	agent         bool
	savedResume   bool
	autofix       bool
	// allowAutoLaunch is the auto-vs-manual distinction inside the tracked
	// phase role rather than a role of its own (V4). allowPhaseTerminal is the
	// resumed phase that had already finished (V8, V10).
	allowAutoLaunch    bool
	allowPhaseTerminal bool
}

func flowLaunchRoleMarkersOf(role FlowLaunchRole) (flowLaunchRoleMarkers, bool) {
	switch role {
	case RoleTrackedPhase:
		return flowLaunchRoleMarkers{phaseAttached: true, tracked: true, allowAutoLaunch: true}, true
	case RolePhaseResume:
		return flowLaunchRoleMarkers{phaseAttached: true, tracked: true, allowPhaseTerminal: true}, true
	case RoleCreatePhase:
		return flowLaunchRoleMarkers{phaseAttached: true, tracked: true}, true
	case RoleRepair:
		return flowLaunchRoleMarkers{repair: true}, true
	case RoleAutofix:
		return flowLaunchRoleMarkers{autofix: true}, true
	case RoleWorktreeAgent:
		return flowLaunchRoleMarkers{agent: true}, true
	case RoleSavedSessionResume:
		return flowLaunchRoleMarkers{savedResume: true}, true
	default:
		return flowLaunchRoleMarkers{}, false
	}
}

// validateFlowLaunchRole reports one canonical rejection when ctx is not a
// well-formed instance of want: it sets a marker the role does not own, or
// misses one the role requires. FlowLaunchRoleOf answers which role a context
// names, by precedence; this answers whether the context is a clean instance of
// that role, which is the half the hand-written ladders at the seam used to
// carry on their own.
//
// Transport and payload are deliberately not inputs. Embedded, Headless,
// FlowAutofixPRNumber, InitialPrompt and ResumeSessionID are read by exactly one
// consumer each, next to that consumer's single role call.
func validateFlowLaunchRole(ctx AgentLaunchContext, want FlowLaunchRole) error {
	markers, ok := flowLaunchRoleMarkersOf(want)
	if !ok {
		return fmt.Errorf("%w: %v", errInvalidFlowLaunchRole, want)
	}
	// Every role is scoped to a Flow: without one there is no lease to take, no
	// phase to mark and no session to resume.
	if strings.TrimSpace(ctx.FlowID) == "" {
		return fmt.Errorf("%w: %v requires a Flow ID", errInvalidFlowLaunchRole, want)
	}
	if markers.phaseAttached {
		if strings.TrimSpace(ctx.FlowPhaseID) == "" {
			return fmt.Errorf("%w: %v requires a phase ID", errInvalidFlowLaunchRole, want)
		}
	} else if strings.TrimSpace(ctx.FlowPhaseID) != "" || strings.TrimSpace(ctx.FlowPhaseKind) != "" {
		return fmt.Errorf("%w: %v owns no phase", errInvalidFlowLaunchRole, want)
	}
	for _, marker := range []struct {
		name string
		got  bool
		want bool
		// optional markers are the two the role may carry but need not: the
		// auto-launch flag on a tracked phase and the terminal flag on a
		// phase resume.
		optional bool
	}{
		{name: "tracked", got: ctx.FlowLaunchTracked, want: markers.tracked},
		{name: "repair", got: ctx.FlowRepair, want: markers.repair},
		{name: "worktree agent", got: ctx.FlowAgent, want: markers.agent},
		{name: "saved session resume", got: ctx.FlowSavedSessionResume, want: markers.savedResume},
		{name: "autofix", got: ctx.FlowAutofix, want: markers.autofix},
		{name: "auto launch", got: ctx.FlowAutoLaunch, optional: markers.allowAutoLaunch},
		{name: "phase terminal", got: ctx.FlowPhaseTerminal, optional: markers.allowPhaseTerminal},
	} {
		if marker.got == marker.want || (marker.got && marker.optional) {
			continue
		}
		if marker.want {
			return fmt.Errorf("%w: %v requires the %s marker", errInvalidFlowLaunchRole, want, marker.name)
		}
		return fmt.Errorf("%w: %v does not own the %s marker", errInvalidFlowLaunchRole, want, marker.name)
	}
	return nil
}
