package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// LaunchRegistrar registers a launch with the process's launch controller and
// returns the endpoint the agent should report through. A nil registrar or a
// zero Endpoint leaves the launch on the direct path.
type LaunchRegistrar interface {
	Register(launchcontrol.Registration) (launchcontrol.Endpoint, error)
}

// launchStamp is everything applyLaunchStamp writes onto a launch context: the
// pinned binary and the controller registration. It travels together so a
// launch kind cannot pick up one without the other.
type launchStamp struct {
	Pin     controlplane.Pin
	Control LaunchRegistrar
}

// launchStamp is the Model's stamp for launches it starts directly.
func (m Model) launchStamp() launchStamp {
	return launchStamp{Pin: m.launchPin, Control: m.launchControl}
}

// retainLaunchPin claims a cached binary against cache retention. It is a var so
// tests can observe the claim without writing into a real state root.
var retainLaunchPin = controlplane.RetainPin

// verifyLaunchPin re-checks a pinned binary before a route commits to it. It is
// a var so tests can fail the check without corrupting a real cached binary.
//
// It is package-level rather than a field on flowLaunchPreparation because more
// than one route needs it. The tracked phase-launch preflight is not the only
// path that marks a phase running and bakes the pinned path into a detached
// agent's argv: phase resume, repair, autofix, and the generic worktree agent
// all reserve and write without ever going through that preflight. A check that
// lived only there would leave every one of them launching an unverified binary
// — a cached copy the state root lost, or, under a degraded pin, a source path
// an upgrade replaced underneath a long-lived TUI.
//
// An unpinned launch verifies nothing: there is no claim to check, and refusing
// would break every caller that never had a pin.
var verifyLaunchPin = func(pin controlplane.Pin) error {
	if strings.TrimSpace(pin.ExecutablePath) == "" {
		return nil
	}
	return pin.Verify()
}

// refuseUnverifiedLaunchPin returns the refusal message for pin, or "" when the
// pin is usable. Routes call it before they reserve or write, so a launch
// refused for an unusable binary leaves Flow state exactly as it found it.
func refuseUnverifiedLaunchPin(pin controlplane.Pin) string {
	if err := verifyLaunchPin(pin); err != nil {
		return flowPhaseLaunchPinRefusal(pin, err)
	}
	return ""
}

// applyLaunchStamp stamps the launching build onto a launch context so the
// agent invokes exactly the binary that launched it rather than whatever
// `approach` ambient PATH resolves, claims the pinned copy against cache
// retention, and registers the launch with the launch controller so the
// agent's phase results are proxied and logged rather than written straight
// into the database. A zero Pin stamps no pin, which is the pre-pin behaviour
// and still correct for a manually started session; a nil registrar or a
// launch without a Flow registers nothing.
//
// The halves are deliberately one function. agentCommandSpec bakes the pinned
// path into the provider session-hook argv of EVERY launch kind — phase,
// repair, autofix, generic worktree agent, both resumes, plan and plain agent
// launches — so a launch kind that stamped a pin without claiming it would let
// retention evict the binary its own detached agent still has to run, and a
// launch kind that pinned without registering would send its results down the
// unlogged path. Making this the single place all of it happens is what stops
// the next launch kind from forgetting.
func applyLaunchStamp(ctx actions.AgentLaunchContext, stamp launchStamp) actions.AgentLaunchContext {
	if strings.TrimSpace(stamp.Pin.ExecutablePath) != "" {
		ctx.Executable = stamp.Pin.ExecutablePath
		ctx.BuildVersion = stamp.Pin.Version
		ctx.DBSchemaVersion = stamp.Pin.SchemaVersion
		claimLaunchPin(ctx, stamp.Pin)
	}
	registerLaunchControl(&ctx, stamp.Control)
	return ctx
}

// registerLaunchControl is best-effort like claimLaunchPin: a registration
// that fails leaves the endpoint fields empty and the launch proceeds on the
// direct path, which the fallback rules cover. Only Flow-scoped launches with
// a launch ID register; a launch without a phase (repair, autofix, generic
// worktree agent) registers as unowned, so its writes are proxied and logged
// but never replayed against a phase it does not own.
func registerLaunchControl(ctx *actions.AgentLaunchContext, control LaunchRegistrar) {
	if control == nil {
		return
	}
	if strings.TrimSpace(ctx.FlowID) == "" || strings.TrimSpace(ctx.LaunchID) == "" {
		return
	}
	endpoint, err := control.Register(launchcontrol.Registration{
		FlowID:   ctx.FlowID,
		PhaseID:  ctx.FlowPhaseID,
		LaunchID: ctx.LaunchID,
		Kind:     launchKind(*ctx),
	})
	if err != nil || endpoint.Path == "" || endpoint.Token == "" {
		return
	}
	ctx.ControlEndpoint = endpoint.Path
	ctx.ControlToken = endpoint.Token
}

// launchKind names the launch for launch.json diagnostics.
func launchKind(ctx actions.AgentLaunchContext) string {
	switch {
	case ctx.FlowRepair:
		return "repair"
	case ctx.FlowAutofix:
		return "autofix"
	case ctx.FlowAgent:
		return "generic"
	case ctx.FlowSavedSessionResume || strings.TrimSpace(ctx.ResumeSessionID) != "":
		return "resume"
	default:
		return "phase"
	}
}

// claimLaunchPin is best-effort by design. Retention is hygiene: failing a
// launch because a claim file could not be written would trade a bounded disk
// cost for an outage, which is the trade this whole mechanism refuses. A
// degraded pin has no cached copy to protect, so there is nothing to claim.
//
// Claiming at context construction is earlier than strictly necessary — a
// launch that fails after this point leaves a claim behind until it expires.
// That is the accepted price of having ONE place where the stamp and the claim
// happen together; splitting them to claim later is exactly how a launch kind
// ends up with one and not the other.
func claimLaunchPin(ctx actions.AgentLaunchContext, pin controlplane.Pin) {
	if pin.Degraded {
		return
	}
	root := strings.TrimSpace(ctx.SessionStateRoot)
	if root == "" || strings.TrimSpace(ctx.LaunchID) == "" {
		return
	}
	_ = retainLaunchPin(root, ctx.LaunchID, pin.Digest)
}

// flowPhaseLaunchPinRefusal explains a refused launch. It names the pinned path,
// this build, and the schema that build writes, because the operator's next
// question is always "which two binaries disagreed".
func flowPhaseLaunchPinRefusal(pin controlplane.Pin, err error) string {
	detail := "is unusable"
	switch {
	case errors.Is(err, controlplane.ErrPinMissing):
		detail = "is missing"
	case errors.Is(err, controlplane.ErrPinNotExecutable):
		detail = "is not executable"
	case errors.Is(err, controlplane.ErrPinDigestMismatch):
		detail = "no longer matches the build that started this session"
	}
	version := strings.TrimSpace(pin.Version)
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf(
		"Launch refused: the pinned approach binary %s %s (%s, schema %d). Restart approach to re-pin it. (%v)",
		pin.ExecutablePath, detail, version, pin.SchemaVersion, err)
}
