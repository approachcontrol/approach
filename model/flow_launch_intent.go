package model

import "github.com/approachcontrol/approach/actions"

// flowLaunchKind is the closed set of launch intents the lifecycle can be asked
// to run. Every kind is declared now so later beads add implementations rather
// than constants; flowLaunchKindManualPhase and flowLaunchKindAutoPhase are
// implemented today and requestFlowLaunch refuses the rest.
type flowLaunchKind int

const (
	flowLaunchKindManualPhase flowLaunchKind = iota + 1
	flowLaunchKindCreatePhase
	flowLaunchKindAutoPhase
	flowLaunchKindPhaseResume
	flowLaunchKindRepair
	flowLaunchKindWorktreeAgent
	flowLaunchKindSavedSessionResume
)

// flowLaunchOrigin records which surface submitted the intent. Nothing branches
// on it yet; preserving it at admission lets routing tests distinguish the two
// manual entry points and gives later lifecycle kinds explicit provenance.
type flowLaunchOrigin int

const (
	flowLaunchOriginFlows flowLaunchOrigin = iota + 1
	flowLaunchOriginActiveFlows
	flowLaunchOriginAutoMode
	flowLaunchOriginRepair
)

// flowLaunchRoute selects the handoff the prepared context takes.
type flowLaunchRoute int

const (
	flowLaunchRouteExternal flowLaunchRoute = iota
	flowLaunchRouteEmbedded
)

// flowLaunchIntent is what a caller submits. It carries only what the caller
// knows: everything else — agent settings, prompt templates, phase, headless
// preference — the lifecycle reads from the Model or the authoritative record.
// ResumeContext is the one exception, and it is documented on the field.
type flowLaunchIntent struct {
	Kind    flowLaunchKind
	FlowID  string
	PhaseID string
	Origin  flowLaunchOrigin
	// FlowTitle is the submitter's snapshot title, already resolved through
	// flowTitleForStatus. It exists so a failed authoritative read still renders
	// "Flow <title>: <err>"; admission has no other source, because AutoMode
	// deliberately skips the display cache and the fresh record does not exist
	// yet. Manual launch leaves it empty.
	FlowTitle string
	// Provider, ProviderSessionID, and ResumeCommand are phase-resume identity.
	// Provider is the normalized session provider and ProviderSessionID its
	// exact ID; together they are what the authoritative read re-validates
	// against the fresh phase. ResumeCommand is the already-resolved agent
	// command, after the codex → codex-app preference mapping and
	// agent.Validate; it is deliberately not flowLaunchAgentSettingsSnapshot's
	// Command, which means the agent *setting* and genuinely diverges.
	Provider          string
	ProviderSessionID string
	ResumeCommand     string
	// FallbackRepoPath is m.currentRepoPath() snapshotted at the key press,
	// because the read command has no Model. The read stage uses the record's
	// repo path when it has one and this otherwise.
	FallbackRepoPath string
	// ResumeContext is the codex-app exception and nothing else. The untracked
	// navigation command needs a fully built context that no authoritative read
	// produces, so the resolver hands it over on the intent. The tracked resume
	// route ignores this field entirely and rebuilds from the record its read
	// returned.
	ResumeContext actions.AgentLaunchContext
}

func (m Model) flowLaunchOrigin() flowLaunchOrigin {
	if m.activeFlowSurfaceVisible() {
		return flowLaunchOriginActiveFlows
	}
	return flowLaunchOriginFlows
}
