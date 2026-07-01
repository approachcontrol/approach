package flowstore_test

import (
	"testing"
	"time"

	"github.com/brian-bell/wtui/flowstore"
)

func TestLatestPhaseSessionLatestLaunchWins(t *testing.T) {
	phase := flowstore.FlowPhase{
		LaunchIDs: []string{"launch-old", "launch-new"},
		Sessions: []flowstore.Session{
			{Provider: "claude", SessionID: "claude-newer-time", LaunchID: "launch-old", StartedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)},
			{Provider: "codex", SessionID: "codex-latest-launch", LaunchID: "launch-new", StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
		},
	}

	session, ok := flowstore.LatestPhaseSession(phase, true)
	if !ok {
		t.Fatal("LatestPhaseSession() ok = false, want true")
	}
	if session.SessionID != "codex-latest-launch" {
		t.Fatalf("LatestPhaseSession() = %#v, want latest launch session", session)
	}
}

func TestLatestPhaseSessionLegacyFallbackUsesTimeAndSliceOrder(t *testing.T) {
	phase := flowstore.FlowPhase{
		Sessions: []flowstore.Session{
			{Provider: "codex", SessionID: "first", StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
			{Provider: "claude", SessionID: "latest", StartedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)},
			{Provider: "codex", SessionID: "tie-wins", StartedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)},
		},
	}

	session, ok := flowstore.LatestPhaseSession(phase, true)
	if !ok {
		t.Fatal("LatestPhaseSession() ok = false, want true")
	}
	if session.SessionID != "tie-wins" {
		t.Fatalf("LatestPhaseSession() = %#v, want later equal-time slice entry", session)
	}
}

func TestPhaseAwaitingSessionReportsNewestLaunchWithoutAttachedSession(t *testing.T) {
	phase := flowstore.FlowPhase{
		LaunchIDs: []string{"launch-old", "launch-new"},
		Sessions: []flowstore.Session{
			{Provider: "codex", SessionID: "codex-old", LaunchID: "launch-old"},
		},
	}

	if !flowstore.PhaseAwaitingSession(phase) {
		t.Fatal("PhaseAwaitingSession() = false, want true")
	}
	if session, ok := flowstore.LatestPhaseSession(phase, true); !ok || session.SessionID != "codex-old" {
		t.Fatalf("LatestPhaseSession() = %#v, %v; want stale legacy fallback for display only", session, ok)
	}
}

func TestPhaseSessionLaunchMismatch(t *testing.T) {
	tests := []struct {
		name  string
		phase flowstore.FlowPhase
		want  bool
	}{
		{
			name: "older matched session and newer orphan launch",
			phase: flowstore.FlowPhase{
				LaunchIDs: []string{"launch-old", "launch-orphan"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old"}},
			},
		},
		{
			name: "stale session outside phase launches",
			phase: flowstore.FlowPhase{
				LaunchIDs: []string{"launch-orphan"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale"}},
			},
			want: true,
		},
		{
			name: "attached session missing launch id",
			phase: flowstore.FlowPhase{
				LaunchIDs: []string{"launch-orphan"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-stale"}},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := flowstore.PhaseSessionLaunchMismatch(tc.phase); got != tc.want {
				t.Fatalf("PhaseSessionLaunchMismatch() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLatestPhaseSessionDistinguishesMalformedLatestSession(t *testing.T) {
	phase := flowstore.FlowPhase{
		LaunchIDs: []string{"launch-new"},
		Sessions: []flowstore.Session{
			{Provider: "codex", LaunchID: "launch-new", Status: "ended"},
			{Provider: "claude", SessionID: "claude-legacy", Status: "ended"},
		},
	}

	session, ok := flowstore.LatestPhaseSession(phase, false)
	if !ok {
		t.Fatal("LatestPhaseSession(requireSessionID=false) ok = false, want true")
	}
	if session.SessionID != "" || session.LaunchID != "launch-new" {
		t.Fatalf("LatestPhaseSession(requireSessionID=false) = %#v, want malformed latest launch session", session)
	}
	session, ok = flowstore.LatestPhaseSession(phase, true)
	if !ok || session.SessionID != "claude-legacy" {
		t.Fatalf("LatestPhaseSession(requireSessionID=true) = %#v, %v; want legacy usable fallback", session, ok)
	}
}

func TestRecoverableRunningPhaseResetReasonClassifiesLatestLaunchOnly(t *testing.T) {
	endedAt := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		phase flowstore.FlowPhase
		want  string
		ok    bool
	}{
		{
			name: "latest launch without session awaits session capture",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-old", "launch-new"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"}},
			},
			want: "await-session",
			ok:   true,
		},
		{
			name: "latest launch with ended session",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-new"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-new", LaunchID: "launch-new", Status: "ended"}},
			},
			want: "ended-session",
			ok:   true,
		},
		{
			name: "latest launch with ended timestamp",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-new"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-new", LaunchID: "launch-new", EndedAt: endedAt}},
			},
			want: "ended-session",
			ok:   true,
		},
		{
			name: "latest launch with live session",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-new"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-new", LaunchID: "launch-new", Status: "running"}},
			},
		},
		{
			name: "latest launch with mixed ended and live sessions",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-new"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-ended", LaunchID: "launch-new", Status: "ended"},
					{Provider: "claude", SessionID: "session-live", LaunchID: "launch-new"},
				},
			},
		},
		{
			name: "older ended session does not classify newer live launch",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-old", "launch-new"},
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"},
					{Provider: "claude", SessionID: "session-new", LaunchID: "launch-new"},
				},
			},
		},
		{
			name: "session mismatch is not recoverable",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-new"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"}},
			},
		},
		{
			name: "non-running phase is not recoverable",
			phase: flowstore.FlowPhase{
				Status:    flowstore.PhaseCompleted,
				LaunchIDs: []string{"launch-new"},
				Sessions:  []flowstore.Session{{Provider: "codex", SessionID: "session-new", LaunchID: "launch-new", Status: "ended"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := flowstore.RecoverableRunningPhaseResetReason(tc.phase)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("RecoverableRunningPhaseResetReason() = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
