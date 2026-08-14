package model

import (
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/sessions"
)

// flowLaunchKind is the closed set of launch intents the lifecycle can be asked
// to run. Create-phase is reserved for later migration; every other declared
// kind is implemented today.
type flowLaunchKind int

const (
	flowLaunchKindManualPhase flowLaunchKind = iota + 1
	flowLaunchKindCreatePhase
	flowLaunchKindAutoPhase
	flowLaunchKindPhaseResume
	flowLaunchKindRepair
	flowLaunchKindAutofix
	flowLaunchKindWorktreeAgent
	flowLaunchKindSavedSessionResume
)

// flowLaunchOrigin records which surface submitted the intent. Saved-session
// resume uses it only when an authoritative non-Flow refresh delegates back to
// the surface's established route; Flow-associated policy never branches on it.
type flowLaunchOrigin int

const (
	flowLaunchOriginFlows flowLaunchOrigin = iota + 1
	flowLaunchOriginActiveFlows
	flowLaunchOriginAutoMode
	flowLaunchOriginRepair
	flowLaunchOriginSessionsPane
	flowLaunchOriginEmbeddedSessionPicker
	flowLaunchOriginInlineWorktreeSession
	flowLaunchOriginNewFlow
)

// flowLaunchSavedSessionKey is the exact identity shared by the session store
// and the lifecycle's duplicate fence. Provider values are validated once;
// session IDs are thereafter compared byte-for-byte.
type flowLaunchSavedSessionKey struct {
	Provider  sessions.Provider
	SessionID string
}

func newFlowLaunchSavedSessionKey(provider sessions.Provider, sessionID string) (flowLaunchSavedSessionKey, error) {
	switch provider {
	case sessions.ProviderCodex, sessions.ProviderClaude:
	default:
		return flowLaunchSavedSessionKey{}, fmt.Errorf("unsupported session provider %q", provider)
	}
	if strings.TrimSpace(sessionID) == "" {
		return flowLaunchSavedSessionKey{}, fmt.Errorf("session provider session ID is required")
	}
	return flowLaunchSavedSessionKey{Provider: provider, SessionID: sessionID}, nil
}

func (key flowLaunchSavedSessionKey) valid() bool {
	_, err := newFlowLaunchSavedSessionKey(key.Provider, key.SessionID)
	return err == nil
}

// flowLaunchRoute selects the handoff the prepared context takes.
type flowLaunchRoute int

const (
	flowLaunchRouteEmbedded flowLaunchRoute = iota + 1
	// flowLaunchRouteTmux hands the agent to a window in the repo's tmux
	// session. Ownership is external-style: the window outlives the TUI and
	// provider hooks own completion.
	flowLaunchRouteTmux
)

// flowLaunchIntent is what a caller submits. It carries only what the caller
// knows: everything else — agent settings, prompt templates, phase, headless
// preference — the lifecycle reads from the Model or the authoritative record.
// FallbackRepoPath exists because a stage that runs without a Model needs the
// current repo path. Phase resume and repair use it as a last candidate when
// resolving their launch directory.
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
	// Provider is the normalized session provider and ProviderSessionID the
	// persisted ID verbatim — both stores key a session by its raw ID, so
	// canonicalizing it here would name a session neither store has. Together
	// they are what the authoritative read re-validates against the fresh
	// phase. ResumeCommand is the already-validated session provider. It is
	// deliberately not flowLaunchAgentSettingsSnapshot's
	// Command: that field carries the agent *setting* a new launch would use,
	// and a resume follows the session's own provider instead, so the two
	// genuinely diverge.
	Provider          string
	ProviderSessionID string
	ResumeCommand     string
	// FallbackRepoPath is m.currentRepoPath() snapshotted at the key press,
	// because the read command has no Model. The read stage uses the record's
	// repo path when it has one and this otherwise.
	FallbackRepoPath string
	// SavedSession is the cached row that caused saved-session admission. It is
	// intentionally not authoritative; SessionKey is the only identity carried
	// into the exact refresh.
	SavedSession sessions.SessionRecord
	SessionKey   flowLaunchSavedSessionKey
	Create       flowLaunchCreateRequest
}

// resumeSessionIdentity names the session a resume is reattaching to the way
// both stores key it. The read stage's drift check and its occupancy exemption
// both go through this, so the two cannot disagree about what "the same
// session" means.
func (intent flowLaunchIntent) resumeSessionIdentity() flowSessionIdentity {
	return flowSessionIdentity{Provider: intent.Provider, SessionID: intent.ProviderSessionID}
}

func (m Model) flowLaunchOrigin() flowLaunchOrigin {
	if m.activeFlowSurfaceVisible() {
		return flowLaunchOriginActiveFlows
	}
	return flowLaunchOriginFlows
}
