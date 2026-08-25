package main

import (
	"testing"
	"time"

	"github.com/approachcontrol/approach/sessions"
)

// The launch controller's liveness probe reports a death certificate only for
// recognized final Claude reasons. Other ended records remain history.
func TestSessionLivenessProbeIsProviderAware(t *testing.T) {
	endedAt := time.Date(2026, 6, 6, 14, 10, 0, 0, time.UTC)
	cases := []struct {
		name          string
		provider      sessions.Provider
		reason        string
		wantCertified bool
	}{
		{name: "claude logout", provider: sessions.ProviderClaude, reason: "logout", wantCertified: true},
		{name: "claude prompt input exit", provider: sessions.ProviderClaude, reason: "prompt_input_exit", wantCertified: true},
		{name: "claude other", provider: sessions.ProviderClaude, reason: "other", wantCertified: true},
		{name: "claude clear", provider: sessions.ProviderClaude, reason: "clear"},
		{name: "claude missing reason", provider: sessions.ProviderClaude},
		{name: "claude unknown reason", provider: sessions.ProviderClaude, reason: "future_reason"},
		{name: "codex ended record", provider: sessions.ProviderCodex, reason: "logout"},
		{name: "cursor ended record", provider: sessions.ProviderCursor, reason: "logout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionStore, _, _ := testArtifactStores(t)
			if err := sessionStore.Upsert(sessions.SessionRecord{
				Provider:  tc.provider,
				SessionID: "session-1",
				LaunchID:  "launch-1",
				Status:    "ended",
				EndReason: tc.reason,
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
			if liveness.DeathCertificate != tc.wantCertified {
				t.Fatalf("liveness = %#v, want DeathCertificate=%v", liveness, tc.wantCertified)
			}
			if tc.wantCertified && !liveness.EndedAt.Equal(endedAt) {
				t.Fatalf("liveness = %#v, want EndedAt=%v", liveness, endedAt)
			}
		})
	}
	// Claude's SessionEnd fires on /clear with the agent still alive and no
	// record for the new session until it ends, so a launch whose latest end
	// was a /clear is treated as continued, not ended; a later real end for
	// the same launch is evidence again.
	t.Run("claude clear is a continued session", func(t *testing.T) {
		sessionStore, _, _ := testArtifactStores(t)
		if err := sessionStore.Upsert(sessions.SessionRecord{
			Provider: sessions.ProviderClaude, SessionID: "session-1", LaunchID: "launch-1",
			Status: "ended", EndedAt: endedAt, EndReason: "clear",
		}); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		liveness, err := sessionLivenessProbe(sessionStore)("launch-1")
		if err != nil {
			t.Fatalf("probe error = %v", err)
		}
		if !liveness.RecordKnown || liveness.DeathCertificate {
			t.Fatalf("liveness = %#v, want known without a certificate after /clear", liveness)
		}
		if err := sessionStore.Upsert(sessions.SessionRecord{
			Provider: sessions.ProviderClaude, SessionID: "session-2", LaunchID: "launch-1",
			Status: "ended", EndedAt: endedAt.Add(time.Hour), EndReason: "prompt_input_exit",
		}); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		liveness, err = sessionLivenessProbe(sessionStore)("launch-1")
		if err != nil {
			t.Fatalf("probe error = %v", err)
		}
		if !liveness.DeathCertificate || !liveness.EndedAt.Equal(endedAt.Add(time.Hour)) {
			t.Fatalf("liveness = %#v, want the later final exit certified", liveness)
		}
	})
	t.Run("unknown launch is not ended", func(t *testing.T) {
		sessionStore, _, _ := testArtifactStores(t)
		liveness, err := sessionLivenessProbe(sessionStore)("launch-none")
		if err != nil {
			t.Fatalf("probe error = %v", err)
		}
		if liveness.RecordKnown || liveness.DeathCertificate {
			t.Fatalf("liveness = %#v, want unknown without a certificate", liveness)
		}
	})
	t.Run("one live record keeps the launch alive", func(t *testing.T) {
		sessionStore, _, _ := testArtifactStores(t)
		for _, record := range []sessions.SessionRecord{
			{Provider: sessions.ProviderClaude, SessionID: "session-1", LaunchID: "launch-1", Status: "ended", EndedAt: endedAt, EndReason: "logout"},
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
		if !liveness.RecordKnown || liveness.DeathCertificate {
			t.Fatalf("liveness = %#v, want active record to suppress the certificate", liveness)
		}
	})
}
