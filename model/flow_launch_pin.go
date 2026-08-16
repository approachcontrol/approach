package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/internal/controlplane"
)

// applyLaunchPin stamps the launching build onto a launch context so the agent
// invokes exactly the binary that launched it rather than whatever `approach`
// ambient PATH resolves. A zero Pin stamps nothing, which is the pre-pin
// behaviour and still correct for a manually started session.
func applyLaunchPin(ctx actions.AgentLaunchContext, pin controlplane.Pin) actions.AgentLaunchContext {
	if strings.TrimSpace(pin.ExecutablePath) == "" {
		return ctx
	}
	ctx.Executable = pin.ExecutablePath
	ctx.BuildVersion = pin.Version
	ctx.DBSchemaVersion = pin.SchemaVersion
	return ctx
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
