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
