package flowstore

import "strings"

// LatestPhaseSession returns the display/latest session for a phase. A session
// attached to the latest non-empty launch ID wins; otherwise timestamps and
// slice order provide deterministic legacy-record fallback.
func LatestPhaseSession(phase FlowPhase, requireSessionID bool) (Session, bool) {
	latestLaunchID := LatestPhaseLaunchID(phase)
	if latestLaunchID != "" {
		for i := len(phase.Sessions) - 1; i >= 0; i-- {
			session := phase.Sessions[i]
			if session.LaunchID == latestLaunchID && (!requireSessionID || strings.TrimSpace(session.SessionID) != "") {
				return session, true
			}
		}
	}
	var best Session
	bestIndex := -1
	for i, session := range phase.Sessions {
		if requireSessionID && strings.TrimSpace(session.SessionID) == "" {
			continue
		}
		if bestIndex < 0 || sessionNewer(session, best) || sessionSameTime(session, best) {
			best = session
			bestIndex = i
		}
	}
	if bestIndex < 0 {
		return Session{}, false
	}
	return best, true
}

// PhaseAwaitingSession reports whether the newest phase launch has not attached
// any session record yet. A malformed attached record is handled separately by
// callers as missing session metadata.
func PhaseAwaitingSession(phase FlowPhase) bool {
	latestLaunchID := LatestPhaseLaunchID(phase)
	if latestLaunchID == "" {
		return false
	}
	for _, session := range phase.Sessions {
		if session.LaunchID == latestLaunchID {
			return false
		}
	}
	return true
}

// PhaseSessionLaunchMismatch reports whether any attached session cannot be
// matched back to one of the phase launch attempts.
func PhaseSessionLaunchMismatch(phase FlowPhase) bool {
	if len(phase.Sessions) == 0 {
		return false
	}
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, session := range phase.Sessions {
		if session.LaunchID == "" {
			return true
		}
		if _, ok := launches[session.LaunchID]; !ok {
			return true
		}
	}
	return false
}

func LatestPhaseLaunchID(phase FlowPhase) string {
	for i := len(phase.LaunchIDs) - 1; i >= 0; i-- {
		if phase.LaunchIDs[i] != "" {
			return phase.LaunchIDs[i]
		}
	}
	return ""
}

func sessionNewer(a, b Session) bool {
	aStarted, bStarted := !a.StartedAt.IsZero(), !b.StartedAt.IsZero()
	if aStarted || bStarted {
		if aStarted != bStarted {
			return aStarted
		}
		return a.StartedAt.After(b.StartedAt)
	}
	aEnded, bEnded := !a.EndedAt.IsZero(), !b.EndedAt.IsZero()
	if aEnded || bEnded {
		if aEnded != bEnded {
			return aEnded
		}
		return a.EndedAt.After(b.EndedAt)
	}
	return false
}

func sessionSameTime(a, b Session) bool {
	if !a.StartedAt.IsZero() || !b.StartedAt.IsZero() {
		return a.StartedAt.Equal(b.StartedAt)
	}
	if !a.EndedAt.IsZero() || !b.EndedAt.IsZero() {
		return a.EndedAt.Equal(b.EndedAt)
	}
	return true
}
