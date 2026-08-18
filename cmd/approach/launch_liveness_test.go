package main

import (
	"testing"
	"time"

	"github.com/approachcontrol/approach/sessions"
)

// The launch controller's liveness probe answers "did this launch's agent
// process end" from session records. Only a Claude record can say so: Codex
// records `ended` after every turn (its Stop hook) and Cursor's stop hook is
// the same shape, so for those providers an ended record is a turn boundary,
// not exit evidence, and the probe must not report the launch as ended.
func TestSessionLivenessProbeIsProviderAware(t *testing.T) {
	endedAt := time.Date(2026, 6, 6, 14, 10, 0, 0, time.UTC)
	cases := []struct {
		name      string
		provider  sessions.Provider
		wantEnded bool
	}{
		{name: "claude ended record is exit evidence", provider: sessions.ProviderClaude, wantEnded: true},
		{name: "codex ended record is a turn boundary", provider: sessions.ProviderCodex, wantEnded: false},
		{name: "cursor ended record is a turn boundary", provider: sessions.ProviderCursor, wantEnded: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionStore, _, _ := testArtifactStores(t)
			if err := sessionStore.Upsert(sessions.SessionRecord{
				Provider:  tc.provider,
				SessionID: "session-1",
				LaunchID:  "launch-1",
				Status:    "ended",
				EndedAt:   endedAt,
			}); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			liveness, err := sessionLivenessProbe(sessionStore)("launch-1")
			if err != nil {
				t.Fatalf("probe error = %v", err)
			}
			if !liveness.RecordKnown {
				t.Fatalf("liveness = %#v, want the record known", liveness)
			}
			if liveness.Ended != tc.wantEnded {
				t.Fatalf("liveness = %#v, want Ended=%v", liveness, tc.wantEnded)
			}
			if tc.wantEnded && !liveness.EndedAt.Equal(endedAt) {
				t.Fatalf("liveness = %#v, want EndedAt=%v", liveness, endedAt)
			}
		})
	}
	t.Run("unknown launch is not ended", func(t *testing.T) {
		sessionStore, _, _ := testArtifactStores(t)
		liveness, err := sessionLivenessProbe(sessionStore)("launch-none")
		if err != nil {
			t.Fatalf("probe error = %v", err)
		}
		if liveness.RecordKnown || liveness.Ended {
			t.Fatalf("liveness = %#v, want unknown and not ended", liveness)
		}
	})
	t.Run("one live record keeps the launch alive", func(t *testing.T) {
		sessionStore, _, _ := testArtifactStores(t)
		for _, record := range []sessions.SessionRecord{
			{Provider: sessions.ProviderClaude, SessionID: "session-1", LaunchID: "launch-1", Status: "ended", EndedAt: endedAt},
			{Provider: sessions.ProviderClaude, SessionID: "session-2", LaunchID: "launch-1", Status: "last_seen"},
		} {
			if err := sessionStore.Upsert(record); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
		}
		liveness, err := sessionLivenessProbe(sessionStore)("launch-1")
		if err != nil {
			t.Fatalf("probe error = %v", err)
		}
		if !liveness.RecordKnown || liveness.Ended {
			t.Fatalf("liveness = %#v, want known and not ended", liveness)
		}
	})
}
