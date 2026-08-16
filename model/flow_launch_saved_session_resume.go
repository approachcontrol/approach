package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
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
		recordSessions, err := seams.ListFlowSessions(flowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if err := validateSavedSessionResumeFlow(flowID, record, recordSessions); err != nil {
			event.Err = err.Error()
			return event
		}
		event.Record = record
		return event
	}
}

func validateSavedSessionResumeFlow(flowID string, record flowstore.FlowRecord, stored []sessions.SessionRecord) error {
	if record.FlowID != flowID {
		return fmt.Errorf("saved session resume expected Flow %q but read %q", flowID, record.FlowID)
	}
	if flowstore.FlowClosed(record) {
		return fmt.Errorf("Flow is closed; reopen it to resume this session")
	}
	for _, phase := range record.Phases {
		if phase.Status == flowstore.PhaseRunning {
			return fmt.Errorf("a running phase already occupies this Flow")
		}
		if phaseHasMatchingLiveSession(phase) {
			return fmt.Errorf("an active phase session already occupies this Flow")
		}
	}
	for _, saved := range stored {
		if strings.TrimSpace(saved.SessionID) != "" && sessions.IsActive(saved.Status, saved.EndedAt) {
			return fmt.Errorf("an active persisted session already occupies this Flow")
		}
	}
	return nil
}

func (m Model) savedSessionFlowLaunchPrepareCmd(msg flowLaunchEventMsg) tea.Cmd {
	reserve := m.reserveFlowLaunch
	seams := m.launchSeams
	pin := m.launchPin
	sessionStateRoot := m.sessionStateRoot
	if root := strings.TrimSpace(m.flowLaunchAttempts[msg.FlowID].Settings.SessionStateRoot); root != "" {
		sessionStateRoot = root
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
			Route:      flowLaunchRouteEmbedded,
		}
		if reserve == nil {
			event.Err = "Flow launch reservation is unavailable"
			return event
		}
		record, release, err := reserve(msg.FlowID)
		event.Release = release
		if err != nil {
			event.Err = err.Error()
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
		stored, err := seams.ListFlowSessions(msg.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if err := validateSavedSessionResumeFlow(msg.FlowID, record, stored); err != nil {
			event.Err = err.Error()
			return event
		}
		workingDir := refreshed.CWD
		if strings.TrimSpace(workingDir) == "" {
			workingDir = refreshed.WorktreePath
		}
		if strings.TrimSpace(workingDir) == "" {
			event.Err = "Session has no worktree path or cwd to resume from"
			return event
		}
		event.Record = record
		event.Session = refreshed
		event.RepoPath = refreshed.RepoPath
		event.Context = applyLaunchPin(actions.AgentLaunchContext{
			Command:                string(refreshed.Provider),
			LaunchID:               msg.Token,
			RepoPath:               refreshed.RepoPath,
			WorktreePath:           refreshed.WorktreePath,
			WorkingDir:             workingDir,
			Branch:                 refreshed.Branch,
			Commit:                 refreshed.Commit,
			SessionStateRoot:       sessionStateRoot,
			ResumeSessionID:        refreshed.SessionID,
			PlanID:                 refreshed.PlanID,
			PlanPath:               refreshed.PlanPath,
			FlowID:                 record.FlowID,
			FlowSavedSessionResume: true,
			Embedded:               true,
		}, pin)
		return event
	}
}
