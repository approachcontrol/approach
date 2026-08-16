package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/internal/controlplane"
)

// retainLaunchPin claims a cached binary against cache retention. It is a var so
// tests can observe the claim without writing into a real state root.
var retainLaunchPin = controlplane.RetainPin

// applyLaunchPin stamps the launching build onto a launch context so the agent
// invokes exactly the binary that launched it rather than whatever `approach`
// ambient PATH resolves, and claims the pinned copy against cache retention. A
// zero Pin stamps nothing, which is the pre-pin behaviour and still correct for
// a manually started session.
//
// The two halves are deliberately one function. agentCommandSpec bakes the
// pinned path into the provider session-hook argv of EVERY launch kind — phase,
// repair, autofix, generic worktree agent, both resumes, plan and plain agent
// launches — so a launch kind that stamped a pin without claiming it would let
// retention evict the binary its own detached agent still has to run. Making
// this the single place both happen is what stops the next launch kind from
// forgetting.
func applyLaunchPin(ctx actions.AgentLaunchContext, pin controlplane.Pin) actions.AgentLaunchContext {
	if strings.TrimSpace(pin.ExecutablePath) == "" {
		return ctx
	}
	ctx.Executable = pin.ExecutablePath
	ctx.BuildVersion = pin.Version
	ctx.DBSchemaVersion = pin.SchemaVersion
	claimLaunchPin(ctx, pin)
	return ctx
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
