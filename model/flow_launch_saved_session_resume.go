package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowoccupancy"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

const (
	savedSessionResumePendingStatus      = "Another launch or session resume is already pending"
	savedSessionResumeFlowOccupiedStatus = "Saved session resume canceled because the authoritative Flow is occupied"
)

// routeSavedSessionResume is the one record-based entry point for all three UI
// surfaces. The cached row supplies only the exact provider/session key; the
// authoritative refresh decides whether the established non-Flow route or the
// Flow lifecycle owns the resume.
func (m Model) routeSavedSessionResume(record sessions.SessionRecord, origin flowLaunchOrigin) (Model, tea.Cmd) {
	if strings.TrimSpace(record.SessionID) == "" {
		return m.setStatus(statusOther, "Session has no provider session ID and cannot be resumed"), nil
	}
	key, err := newFlowLaunchSavedSessionKey(record.Provider, record.SessionID)
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil
	}
	intent := flowLaunchIntent{
		Kind:       flowLaunchKindSavedSessionResume,
		FlowID:     savedSessionFlowLaunchProvisionalID(key),
		Origin:     origin,
		SessionKey: key,
	}
	next, cmd, _ := m.requestFlowLaunch(intent)
	return next, cmd
}

func (m Model) routeNonFlowSavedSessionResume(record sessions.SessionRecord, origin flowLaunchOrigin) (Model, tea.Cmd) {
	ctx, release, ok, next := m.sessionResumeLaunchContext(record)
	if !ok {
		return next, nil
	}
	switch origin {
	case flowLaunchOriginInlineWorktreeSession:
		return next.launchAgentForBackend(ctx, release)
	case flowLaunchOriginSessionsPane, flowLaunchOriginEmbeddedSessionPicker:
		return next.resumeSessionForBackend(ctx, record, release)
	default:
		releaseFlowLaunchReservation(release)
		return next.setStatus(statusOther, "Unsupported saved-session resume origin"), nil
	}
}

func (m Model) admitSavedSessionFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	key, err := newFlowLaunchSavedSessionKey(intent.SessionKey.Provider, intent.SessionKey.SessionID)
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil, false
	}
	flowID := savedSessionFlowLaunchProvisionalID(key)
	intent.FlowID = flowID
	intent.SessionKey = key
	if _, occupied := m.flowLaunchSessionOwners[key]; occupied {
		return m.setStatus(statusOther, savedSessionResumePendingStatus), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m.setStatus(statusOther, savedSessionResumePendingStatus), nil, false
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	next, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:      token,
		Kind:       flowLaunchKindSavedSessionResume,
		FlowID:     flowID,
		Origin:     intent.Origin,
		Settings:   settings,
		SessionKey: key,
	}, flowLaunchStateReadingSession)
	if !ok {
		return m.setStatus(statusOther, savedSessionResumePendingStatus), nil, false
	}
	return next, savedSessionFlowLaunchSessionReadCmd(next.launchSeams, intent, token), true
}

func savedSessionFlowLaunchProvisionalID(key flowLaunchSavedSessionKey) string {
	return fmt.Sprintf("\x00saved-session:%s:%d:%s", key.Provider, len(key.SessionID), key.SessionID)
}

func savedSessionFlowLaunchSessionReadCmd(seams flowLaunchSeams, intent flowLaunchIntent, token string) tea.Cmd {
	return func() tea.Msg {
		event := flowLaunchEventMsg{
			Token:      token,
			Kind:       flowLaunchKindSavedSessionResume,
			From:       flowLaunchStateReadingSession,
			FlowID:     intent.FlowID,
			Stage:      flowLaunchStageSessionRead,
			SessionKey: intent.SessionKey,
		}
		if seams.ReadSession == nil {
			event.Err = "saved session reader is unavailable"
			return event
		}
		record, err := seams.ReadSession(intent.SessionKey.Provider, intent.SessionKey.SessionID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if record.Provider != intent.SessionKey.Provider || record.SessionID != intent.SessionKey.SessionID {
			event.Err = fmt.Sprintf("saved session key changed from %q/%q to %q/%q", intent.SessionKey.Provider, intent.SessionKey.SessionID, record.Provider, record.SessionID)
			return event
		}
		event.Session = record
		return event
	}
}

func (m Model) handleSavedSessionFlowLaunchSessionRead(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	if msg.Err != "" {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).setStatus(statusOther, msg.Err), nil
	}
	// The cached row may say ended while its provider is still live in a tmux
	// window (Codex records an ended session after each turn). Probe only after
	// the exact authoritative refresh so stale launch metadata cannot admit a
	// duplicate process. The helper is backend-gated and runs only for this
	// user-initiated resume hop.
	if m.tmuxSessionAgentStillRunning(msg.Session, string(msg.Session.Provider)) {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).
			setStatus(statusOther, tmuxSessionLiveWindowRefusal), nil
	}
	authoritativeFlowID := strings.TrimSpace(msg.Session.FlowID)
	if authoritativeFlowID == "" {
		m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
		return m.routeNonFlowSavedSessionResume(msg.Session, attempt.Origin)
	}
	if occupied, err := m.trackedFlowLeaseOccupied(authoritativeFlowID); err != nil {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).
			setStatus(statusOther, flowLeaseSetupErrorStatus(err)), nil
	} else if occupied {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).
			setStatus(statusOther, flowLeaseOccupiedStatus), nil
	}
	next, ok := m.transferSavedSessionFlowLaunchAttempt(attempt.FlowID, attempt.Token, attempt.SessionKey, authoritativeFlowID)
	if !ok {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).
			setStatus(statusOther, savedSessionResumeFlowOccupiedStatus), nil
	}
	return next, savedSessionFlowLaunchFlowReadCmd(next.launchSeams, attempt.Token, attempt.SessionKey, msg.Session)
}

func savedSessionFlowLaunchFlowReadCmd(seams flowLaunchSeams, token string, key flowLaunchSavedSessionKey, session sessions.SessionRecord) tea.Cmd {
	flowID := strings.TrimSpace(session.FlowID)
	return func() tea.Msg {
		event := flowLaunchEventMsg{
			Token:      token,
			Kind:       flowLaunchKindSavedSessionResume,
			From:       flowLaunchStateReading,
			FlowID:     flowID,
			Stage:      flowLaunchStageRead,
			Session:    session,
			SessionKey: key,
			RepoPath:   session.RepoPath,
		}
		record, err := seams.ReadFlow(flowID)
		if err != nil {
			event.FlowMissing = flowstore.IsNotFound(err)
			event.Err = err.Error()
			return event
		}
		if err := validateSavedSessionResumeFlowRecord(flowID, record); err != nil {
			event.Err = err.Error()
			return event
		}
		if verdict := flowAuthoritativeOccupancy(seams, flowID, record, actions.RoleSavedSessionResume); verdict.Occupied() {
			event.Err = savedSessionResumeAuthoritativeOccupancyStatus(verdict)
			return event
		}
		event.Record = record
		return event
	}
}

func validateSavedSessionResumeFlowRecord(flowID string, record flowstore.FlowRecord) error {
	if record.FlowID != flowID {
		return fmt.Errorf("saved session resume expected Flow %q but read %q", flowID, record.FlowID)
	}
	if flowstore.FlowClosed(record) {
		return fmt.Errorf("Flow is closed; reopen it to resume this session")
	}
	return nil
}

func savedSessionResumeAuthoritativeOccupancyStatus(verdict flowoccupancy.Verdict) string {
	if verdict.Err() != nil {
		return verdict.Err().Error()
	}
	switch verdict.Holder() {
	case flowoccupancy.HolderRunningPhase:
		return "a running phase already occupies this Flow"
	case flowoccupancy.HolderPhaseSession:
		return "an active phase session already occupies this Flow"
	case flowoccupancy.HolderFlowSession:
		return "an active persisted session already occupies this Flow"
	default:
		return savedSessionResumeFlowOccupiedStatus
	}
}

func (m Model) savedSessionFlowLaunchPrepareCmd(
	msg flowLaunchEventMsg,
	settings flowLaunchAgentSettingsSnapshot,
) tea.Cmd {
	reserve := m.reserveFlowLaunch
	seams := m.launchSeams
	// The snapshot is frozen at admission and the Model field is live, but they
	// are the same value: flowLaunchPreparation sources SessionStateRoot, Pin and
	// Control from these Model fields. The fallback keeps a snapshot that never
	// carried a root — a synthesized attempt in a test — pointed at the Model's.
	if strings.TrimSpace(settings.SessionStateRoot) == "" {
		settings.SessionStateRoot = m.sessionStateRoot
	}
	return func() tea.Msg {
		event := flowLaunchEventMsg{
			Token:      msg.Token,
			Kind:       flowLaunchKindSavedSessionResume,
			From:       flowLaunchStatePreparing,
			FlowID:     msg.FlowID,
			Stage:      flowLaunchStagePrepared,
			Session:    msg.Session,
			SessionKey: msg.SessionKey,
			RepoPath:   msg.Session.RepoPath,
		}
		if reserve == nil {
			event.Err = "Flow launch reservation is unavailable"
			return event
		}
		// Ahead of the reservation, as on every other route: a resume bakes the
		// pinned path into the resumed session's hook argv just as a fresh
		// launch does, so an unusable pin is refused before anything is held.
		if refusal := refuseUnverifiedLaunchPin(settings.Pin); refusal != "" {
			event.Err = refusal
			return event
		}
		record, release, err := reserve(msg.FlowID)
		event.Release = release
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if occupied, inspectErr := m.trackedFlowLeaseOccupied(msg.FlowID); inspectErr != nil {
			event.LeaseDeferred = true
			event.LeaseSetupError = true
			event.Err = flowLeaseSetupErrorStatus(inspectErr)
			return event
		} else if occupied {
			event.LeaseDeferred = true
			event.Err = flowLeaseOccupiedStatus
			return event
		}
		if seams.ReadSession == nil {
			event.Err = "saved session reader is unavailable"
			return event
		}
		refreshed, err := seams.ReadSession(msg.SessionKey.Provider, msg.SessionKey.SessionID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if refreshed.Provider != msg.SessionKey.Provider || refreshed.SessionID != msg.SessionKey.SessionID {
			event.Err = fmt.Sprintf("saved session key changed from %q/%q to %q/%q", msg.SessionKey.Provider, msg.SessionKey.SessionID, refreshed.Provider, refreshed.SessionID)
			return event
		}
		if refreshedFlowID := strings.TrimSpace(refreshed.FlowID); refreshedFlowID != msg.FlowID {
			event.Err = fmt.Sprintf("saved session moved from Flow %q to %q during launch preparation", msg.FlowID, refreshedFlowID)
			return event
		}
		if m.tmuxSessionAgentStillRunning(refreshed, string(refreshed.Provider)) {
			event.Err = tmuxSessionLiveWindowRefusal
			return event
		}
		if m.tmuxAutofixAgentStillRunning(record, refreshed.WorktreePath) {
			event.Err = tmuxFlowLiveWindowRefusal
			return event
		}
		if err := validateSavedSessionResumeFlowRecord(msg.FlowID, record); err != nil {
			event.Err = err.Error()
			return event
		}
		if verdict := flowReservedOccupancyAfterFreeLease(seams, msg.FlowID, record, actions.RoleSavedSessionResume); verdict.Occupied() {
			event.Err = savedSessionResumeAuthoritativeOccupancyStatus(verdict)
			return event
		}
		event.Record = record
		event.Session = refreshed
		event.RepoPath = refreshed.RepoPath
		// The builder owns the working-dir ladder and the resume marker, and it
		// is what refuses a session with neither cwd nor worktree — including
		// the wording of that refusal.
		ctx, decision, err := newFlowLaunchContext(savedSessionResumeTarget{
			LaunchID: msg.Token,
			Record:   record,
			Session:  refreshed,
		}, settings, flowLaunchRouting{})
		if err != nil {
			event.Err = err.Error()
			return event
		}
		event.Context = ctx
		event.Route = decision.Route
		return event
	}
}
