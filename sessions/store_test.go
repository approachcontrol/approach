package sessions_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/sessions"
)

func TestStoreSavesAndListsSessionsByRepoPath(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo", "feature")
	startedAt := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC)
	lastSeenAt := time.Date(2026, 6, 6, 14, 45, 0, 0, time.UTC)

	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	record := sessions.SessionRecord{
		SchemaVersion: 1,
		Provider:      sessions.ProviderCodex,
		SessionID:     "codex-session-1",
		LaunchID:      "launch-1",
		Status:        "ended",
		StartedAt:     startedAt,
		LastSeenAt:    lastSeenAt,
		CWD:           worktreePath,
		RepoPath:      repoPath,
		WorktreePath:  worktreePath,
		Branch:        "feature/sessions",
		Commit:        "abcdef123456",
		Model:         "gpt-5",
		Summary:       "Implement session capture",
		CaptureSource: "hook",
	}
	if err := store.Upsert(record); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1: %#v", len(records), records)
	}

	got := records[0]
	if got.Provider != sessions.ProviderCodex ||
		got.SessionID != "codex-session-1" ||
		got.LaunchID != "launch-1" ||
		got.Status != "ended" ||
		!got.StartedAt.Equal(startedAt) ||
		!got.LastSeenAt.Equal(lastSeenAt) ||
		got.CWD != worktreePath ||
		got.RepoPath != repoPath ||
		got.WorktreePath != worktreePath ||
		got.Branch != "feature/sessions" ||
		got.Commit != "abcdef123456" ||
		got.Model != "gpt-5" ||
		got.Summary != "Implement session capture" ||
		got.CaptureSource != "hook" {
		t.Fatalf("record did not round-trip:\n got: %#v\nwant: %#v", got, record)
	}
}

func TestStoreUpsertUpdatesExistingSession(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	firstSeen := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC)
	lastSeen := time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)

	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	record := sessions.SessionRecord{
		Provider:   sessions.ProviderClaude,
		SessionID:  "same-session",
		Status:     "active",
		LastSeenAt: firstSeen,
		RepoPath:   repoPath,
		Summary:    "first summary",
	}
	if err := store.Upsert(record); err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}
	record.Status = "ended"
	record.LastSeenAt = lastSeen
	record.Summary = "updated summary"
	if err := store.Upsert(record); err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1: %#v", len(records), records)
	}
	got := records[0]
	if got.Status != "ended" || got.Summary != "updated summary" || !got.LastSeenAt.Equal(lastSeen) {
		t.Fatalf("record was not updated: %#v", got)
	}
}

func TestStoreUpsertUsesLatestEndTimeAcrossResumedLaunches(t *testing.T) {
	store, err := sessions.NewStore(sessions.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	firstEnd := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	secondEnd := firstEnd.Add(time.Hour)
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderCodex,
		SessionID:  "resumed-session",
		LaunchID:   "launch-1",
		Status:     "ended",
		EndedAt:    firstEnd,
		LastSeenAt: firstEnd,
	}); err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderCodex,
		SessionID:  "resumed-session",
		LaunchID:   "launch-2",
		Status:     "ended",
		EndedAt:    secondEnd,
		LastSeenAt: secondEnd,
	}); err != nil {
		t.Fatalf("resumed Upsert() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	got := records[0]
	if got.LaunchID != "launch-2" || !got.EndedAt.Equal(secondEnd) {
		t.Fatalf("resumed session launch/end = %q/%s, want launch-2/%s", got.LaunchID, got.EndedAt, secondEnd)
	}
}

func TestStoreUpsertDoesNotRegressToStaleLaunch(t *testing.T) {
	store, err := sessions.NewStore(sessions.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	firstEnd := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	secondEnd := firstEnd.Add(time.Hour)
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderCodex,
		SessionID:  "shared-session",
		LaunchID:   "launch-2",
		Status:     "ended",
		EndedAt:    secondEnd,
		LastSeenAt: secondEnd,
	}); err != nil {
		t.Fatalf("current Upsert() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderCodex,
		SessionID:  "shared-session",
		LaunchID:   "launch-1",
		Status:     "last_seen",
		LastSeenAt: firstEnd,
	}); err != nil {
		t.Fatalf("stale Upsert() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	got := records[0]
	if got.LaunchID != "launch-2" || got.Status != "ended" || !got.EndedAt.Equal(secondEnd) {
		t.Fatalf("session regressed after stale launch update: %#v", got)
	}
}

func TestStoreUpsertIgnoresStaleEndedLaunchWhileResumeIsActive(t *testing.T) {
	store, err := sessions.NewStore(sessions.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	oldEnd := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	resumeSeen := oldEnd.Add(time.Hour)
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderClaude,
		SessionID:  "shared-session",
		LaunchID:   "launch-2",
		Status:     "last_seen",
		LastSeenAt: resumeSeen,
		Summary:    "current launch",
	}); err != nil {
		t.Fatalf("current Upsert() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderClaude,
		SessionID:  "shared-session",
		LaunchID:   "launch-1",
		Status:     "ended",
		EndedAt:    oldEnd,
		LastSeenAt: oldEnd,
		Summary:    "stale launch",
	}); err != nil {
		t.Fatalf("stale Upsert() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	got := records[0]
	if got.LaunchID != "launch-2" || got.Status != "last_seen" || !got.EndedAt.IsZero() || got.Summary != "current launch" {
		t.Fatalf("active resume changed after stale ended update: %#v", got)
	}
}

func TestStoreTranscriptPathMustBeRegularFileInsideProviderRoot(t *testing.T) {
	stateRoot := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	transcriptRoot := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(transcriptRoot, 0o700); err != nil {
		t.Fatalf("create transcript root: %v", err)
	}
	validPath := filepath.Join(transcriptRoot, "session.jsonl")
	if err := os.WriteFile(validPath, []byte(`{"role":"user","kind":"message","text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write valid transcript: %v", err)
	}
	store, err := sessions.NewStore(sessions.StoreOptions{
		Root: stateRoot,
		Env:  map[string]string{"CODEX_HOME": codexHome, "HOME": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "valid", TranscriptPath: validPath}); err != nil {
		t.Fatalf("valid in-root Upsert() error = %v", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write outside transcript: %v", err)
	}
	symlink := filepath.Join(transcriptRoot, "escape.jsonl")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatalf("create transcript symlink: %v", err)
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "relative", path: "session.jsonl"},
		{name: "outside", path: outside},
		{name: "symlink escape", path: symlink},
		{name: "directory", path: transcriptRoot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID := "rejected-" + strings.ReplaceAll(tt.name, " ", "-")
			err := store.Upsert(sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: sessionID, TranscriptPath: tt.path})
			if err == nil {
				t.Fatalf("Upsert(%q) error = nil, want transcript rejection", tt.path)
			}
			sum := sha256.Sum256([]byte(sessionID))
			dir := filepath.Join(stateRoot, "sessions", "codex", hex.EncodeToString(sum[:]))
			if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
				t.Fatalf("rejected transcript created session artifacts at %s: %v", dir, statErr)
			}
		})
	}
}

func TestStoreUpsertReportsSessionLockTimeout(t *testing.T) {
	root := t.TempDir()
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root, LockTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "locked"}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	sum := sha256.Sum256([]byte("locked"))
	lockPath := filepath.Join(root, "sessions", ".locks", "codex-"+hex.EncodeToString(sum[:])+".lock")
	release, err := artifacts.AcquireFileLock(lockPath, "test session lock", time.Second)
	if err != nil {
		t.Fatalf("hold session lock: %v", err)
	}
	defer release()
	err = store.Upsert(sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "locked", Summary: "new"})
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for session lock") {
		t.Fatalf("Upsert() error = %v, want bounded session lock timeout", err)
	}
}

func TestStoreListSkipsCorruptMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "good-session",
		RepoPath:  "/repo",
		Status:    "ended",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	badDir := filepath.Join(root, "sessions", "codex", "bad")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "meta.json"), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}

	records, err := store.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].SessionID != "good-session" {
		t.Fatalf("List() = %#v, want only good-session", records)
	}
}

func TestStoreWritesArtifactsWithRestrictivePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	providerTranscript := providerTranscriptPath(t, sessions.ProviderCodex, "provider.jsonl")
	if err := os.WriteFile(providerTranscript, []byte(`{"role":"user","kind":"message","text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write provider transcript: %v", err)
	}

	store, err := sessions.NewStore(sessions.StoreOptions{Root: root, CopyRawTranscripts: true})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:       sessions.ProviderCodex,
		SessionID:      "permission-check",
		Status:         "ended",
		TranscriptPath: providerTranscript,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "sessions"), 0o700)
	assertMode(t, filepath.Join(root, "sessions", "codex"), 0o700)
	metaPath := singleSessionFile(t, root, sessions.ProviderCodex, "meta.json")
	sessionDir := filepath.Dir(metaPath)
	assertMode(t, sessionDir, 0o700)
	assertMode(t, metaPath, 0o600)
	assertMode(t, filepath.Join(sessionDir, "transcript.jsonl"), 0o600)
	assertMode(t, filepath.Join(sessionDir, "raw.jsonl"), 0o600)
}

func TestStoreMarksLaunchEnded(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	transcriptPath := providerTranscriptPath(t, sessions.ProviderCodex, "codex.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"role":"user","kind":"message","text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	endedAt := time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC)
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:       sessions.ProviderCodex,
		SessionID:      "codex-1",
		LaunchID:       "launch-1",
		Status:         "last_seen",
		RepoPath:       repoPath,
		LastSeenAt:     endedAt.Add(-time.Minute),
		TranscriptPath: transcriptPath,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := os.Remove(transcriptPath); err != nil {
		t.Fatalf("remove provider transcript: %v", err)
	}

	if err := store.MarkLaunchEnded("launch-1", endedAt); err != nil {
		t.Fatalf("MarkLaunchEnded() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	got := records[0]
	if got.Status != "ended" || !got.EndedAt.Equal(endedAt) || !got.LastSeenAt.Equal(endedAt) {
		t.Fatalf("launch was not ended: %#v", got)
	}
}

// Finalization ends a launch in this store and in the Flow phase mirror as one
// operation, and ingestion copies APPROACH_LAUNCH_ID into both verbatim. A
// padded stored ID that matched one half and not the other would leave the
// launch ended in one place and live in the other — the stall shape that makes
// a Flow phase unlaunchable.
func TestStoreMarkLaunchEndedMatchesPaddedLaunchIDs(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	endedAt := time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC)
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderCodex,
		SessionID:  "codex-1",
		LaunchID:   " launch-1 ",
		Status:     "last_seen",
		RepoPath:   repoPath,
		LastSeenAt: endedAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	if err := store.MarkLaunchEnded("launch-1", endedAt); err != nil {
		t.Fatalf("MarkLaunchEnded() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	if records[0].Status != "ended" {
		t.Fatalf("padded launch ID was not ended: %#v", records[0])
	}
}

func TestStoreMarkLaunchEndedAdvancesResumedLaunch(t *testing.T) {
	store, err := sessions.NewStore(sessions.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	firstEnd := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	resumeSeen := firstEnd.Add(30 * time.Minute)
	secondEnd := firstEnd.Add(time.Hour)
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderClaude,
		SessionID:  "resumed-session",
		LaunchID:   "launch-1",
		Status:     "ended",
		EndedAt:    firstEnd,
		LastSeenAt: firstEnd,
	}); err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:   sessions.ProviderClaude,
		SessionID:  "resumed-session",
		LaunchID:   "launch-2",
		Status:     "last_seen",
		LastSeenAt: resumeSeen,
	}); err != nil {
		t.Fatalf("resumed Upsert() error = %v", err)
	}
	if err := store.MarkLaunchEnded("launch-2", secondEnd); err != nil {
		t.Fatalf("MarkLaunchEnded() error = %v", err)
	}

	records, err := store.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	got := records[0]
	if got.LaunchID != "launch-2" || got.Status != "ended" || !got.EndedAt.Equal(secondEnd) || !got.LastSeenAt.Equal(secondEnd) {
		t.Fatalf("finalized resumed session = %#v, want launch-2 ended at %s", got, secondEnd)
	}
}

func TestStoreMarkLaunchEndedSkipsLegacyBlankSessionIDRecords(t *testing.T) {
	root := t.TempDir()
	endedAt := time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC)
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:  sessions.ProviderClaude,
		SessionID: "claude-1",
		LaunchID:  "launch-1",
		Status:    "last_seen",
		RepoPath:  "/repo",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	legacyDir := filepath.Join(root, "sessions", "claude", "legacy-blank")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schema_version":1,"provider":"claude","session_id":"   ","launch_id":"launch-1","status":"last_seen"}`)
	if err := os.WriteFile(filepath.Join(legacyDir, "meta.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := store.MarkLaunchEnded("launch-1", endedAt); err != nil {
		t.Fatalf("MarkLaunchEnded() error = %v, want legacy blank-ID record skipped", err)
	}

	records, err := store.List(sessions.SessionFilter{RepoPath: "/repo"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].Status != "ended" {
		t.Fatalf("valid launch record was not ended: %#v", records)
	}
}

func TestStoreMarkLaunchEndedPreservesProviderEndedAt(t *testing.T) {
	root := t.TempDir()
	providerEndedAt := time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC)
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	for _, finalizeEndedAt := range []time.Time{
		providerEndedAt.Add(5 * time.Second),
		providerEndedAt.Add(-5 * time.Second),
	} {
		sessionID := "claude-" + finalizeEndedAt.Format("150405")
		if err := store.Upsert(sessions.SessionRecord{
			Provider:   sessions.ProviderClaude,
			SessionID:  sessionID,
			LaunchID:   sessionID,
			Status:     "ended",
			EndedAt:    providerEndedAt,
			LastSeenAt: providerEndedAt,
		}); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}

		if err := store.MarkLaunchEnded(sessionID, finalizeEndedAt); err != nil {
			t.Fatalf("MarkLaunchEnded() error = %v", err)
		}

		records, err := store.List(sessions.SessionFilter{})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		var got sessions.SessionRecord
		for _, record := range records {
			if record.SessionID == sessionID {
				got = record
				break
			}
		}
		if !got.EndedAt.Equal(providerEndedAt) {
			t.Fatalf("EndedAt = %s, want provider time %s", got.EndedAt, providerEndedAt)
		}
		wantLastSeen := providerEndedAt
		if finalizeEndedAt.After(wantLastSeen) {
			wantLastSeen = finalizeEndedAt
		}
		if !got.LastSeenAt.Equal(wantLastSeen) {
			t.Fatalf("LastSeenAt = %s, want %s", got.LastSeenAt, wantLastSeen)
		}
	}
}

func TestStoreRejectsRecordsWithoutProviderAndSessionID(t *testing.T) {
	store, err := sessions.NewStore(sessions.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	err = store.Upsert(sessions.SessionRecord{})
	if err == nil {
		t.Fatal("Upsert() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "provider") || !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("Upsert() error = %q, want provider and session ID validation", err)
	}
}

func TestStoreRejectsWhitespaceOnlySessionID(t *testing.T) {
	store, err := sessions.NewStore(sessions.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	err = store.Upsert(sessions.SessionRecord{Provider: sessions.ProviderClaude, SessionID: "   "})
	if err == nil {
		t.Fatal("Upsert() error = nil, want validation error for whitespace session ID")
	}
	if !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("Upsert() error = %q, want session ID validation", err)
	}
}

func TestNewStoreDefaultsRootFromXDGStateHome(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	store, err := sessions.NewStore(sessions.StoreOptions{})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "default-root",
		RepoPath:  "/repo",
		Status:    "ended",
	}
	if err := store.Upsert(record); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	metaPath := singleSessionFile(t, filepath.Join(stateHome, "approach", "sessions", "v1"), sessions.ProviderCodex, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("expected default-root metadata at %s: %v", metaPath, err)
	}
}

func TestNewStoreRejectsRelativeRoot(t *testing.T) {
	_, err := sessions.NewStore(sessions.StoreOptions{Root: ".approach-sessions"})
	if err == nil {
		t.Fatal("NewStore() error = nil, want relative root error")
	}
	if !strings.Contains(err.Error(), "session store root must be absolute") {
		t.Fatalf("NewStore() error = %q", err)
	}
}

func TestStoreCopiesRawTranscriptAndReadsNormalizedEvents(t *testing.T) {
	root := t.TempDir()
	providerTranscript := providerTranscriptPath(t, sessions.ProviderCodex, "provider.jsonl")
	providerData := []byte(`{"timestamp":"2026-06-06T14:01:00Z","role":"user","kind":"message","text":"Implement the sessions view"}
{"timestamp":"2026-06-06T14:02:00Z","role":"assistant","kind":"internal","text":"hidden provider-private note"}
{"timestamp":"2026-06-06T14:03:00Z","role":"assistant","kind":"message","text":"Done"}
`)
	if err := os.WriteFile(providerTranscript, providerData, 0o600); err != nil {
		t.Fatalf("write provider transcript: %v", err)
	}

	store, err := sessions.NewStore(sessions.StoreOptions{Root: root, CopyRawTranscripts: true})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:       sessions.ProviderCodex,
		SessionID:      "with-transcript",
		Status:         "ended",
		RepoPath:       filepath.Join(root, "repo"),
		TranscriptPath: providerTranscript,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	rawPath := singleSessionFile(t, root, sessions.ProviderCodex, "raw.jsonl")
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("read copied raw transcript: %v", err)
	}
	if string(raw) != string(providerData) {
		t.Fatalf("raw transcript mismatch:\n got: %q\nwant: %q", raw, providerData)
	}

	events, err := store.ReadTranscript(sessions.ProviderCodex, "with-transcript")
	if err != nil {
		t.Fatalf("ReadTranscript() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ReadTranscript() returned %d events, want 2: %#v", len(events), events)
	}
	if events[0].Role != "user" || events[0].Kind != "message" || events[0].Text != "Implement the sessions view" {
		t.Fatalf("first event mismatch: %#v", events[0])
	}
	if events[1].Role != "assistant" || events[1].Kind != "message" || events[1].Text != "Done" {
		t.Fatalf("second event mismatch: %#v", events[1])
	}
}

func TestStoreSessionIDCannotEscapeStateRoot(t *testing.T) {
	root := t.TempDir()
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if err := store.Upsert(sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "../escape",
		Status:    "ended",
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("session ID escaped root, stat err = %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(root, "sessions", "codex", "*", "meta.json")); err != nil || len(matches) != 1 {
		t.Fatalf("expected one hashed session metadata file, matches=%#v err=%v", matches, err)
	}
}

func TestStoreRejectsUnsupportedProviderAndCannotEscapeStateRoot(t *testing.T) {
	root := t.TempDir()
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	err = store.Upsert(sessions.SessionRecord{
		Provider:  sessions.Provider("../escape"),
		SessionID: "session-1",
		Status:    "ended",
	})
	if err == nil {
		t.Fatal("Upsert() error = nil, want unsupported provider error")
	}
	if !strings.Contains(err.Error(), "unsupported session provider") {
		t.Fatalf("Upsert() error = %q", err)
	}
	if _, err := store.ReadTranscript(sessions.Provider("../escape"), "session-1"); err == nil {
		t.Fatal("ReadTranscript() error = nil, want unsupported provider error")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("provider escaped root, stat err = %v", err)
	}
}

func TestStoreNormalizesProviderNativeTranscriptLines(t *testing.T) {
	root := t.TempDir()
	providerTranscript := providerTranscriptPath(t, sessions.ProviderClaude, "provider.jsonl")
	providerData := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"assistant","message":{"role":"assistant","content":"hi there"}}
{"type":"system","content":"hidden"}
`)
	if err := os.WriteFile(providerTranscript, providerData, 0o600); err != nil {
		t.Fatalf("write provider transcript: %v", err)
	}
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:       sessions.ProviderClaude,
		SessionID:      "native-transcript",
		Status:         "ended",
		TranscriptPath: providerTranscript,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	events, err := store.ReadTranscript(sessions.ProviderClaude, "native-transcript")
	if err != nil {
		t.Fatalf("ReadTranscript() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("ReadTranscript() returned %d events, want 3: %#v", len(events), events)
	}
	if events[0].Role != "user" || events[0].Text != "hello" || events[1].Role != "assistant" || events[1].Text != "hi there" {
		t.Fatalf("unexpected normalized events: %#v", events)
	}
}

func TestStoreNormalizesRoleContentTranscriptLines(t *testing.T) {
	root := t.TempDir()
	providerTranscript := providerTranscriptPath(t, sessions.ProviderCodex, "provider.jsonl")
	providerData := []byte(`{"role":"user","content":"hello from content"}
{"role":"assistant","content":[{"type":"text","text":"hi from array"},{"type":"text","text":"second part"}]}
`)
	if err := os.WriteFile(providerTranscript, providerData, 0o600); err != nil {
		t.Fatalf("write provider transcript: %v", err)
	}
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:       sessions.ProviderCodex,
		SessionID:      "role-content-transcript",
		Status:         "ended",
		TranscriptPath: providerTranscript,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	events, err := store.ReadTranscript(sessions.ProviderCodex, "role-content-transcript")
	if err != nil {
		t.Fatalf("ReadTranscript() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ReadTranscript() returned %d events, want 2: %#v", len(events), events)
	}
	if events[0].Role != "user" || events[0].Text != "hello from content" {
		t.Fatalf("first normalized event mismatch: %#v", events[0])
	}
	if events[1].Role != "assistant" || events[1].Text != "hi from array\nsecond part" {
		t.Fatalf("second normalized event mismatch: %#v", events[1])
	}
}

func TestStoreReadsTranscriptLinesLargerThanScannerDefault(t *testing.T) {
	root := t.TempDir()
	providerTranscript := providerTranscriptPath(t, sessions.ProviderCodex, "large.jsonl")
	largeText := strings.Repeat("x", 70*1024)
	if err := os.WriteFile(providerTranscript, []byte(`{"role":"assistant","kind":"message","text":`+quoteJSON(largeText)+`}`+"\n"), 0o600); err != nil {
		t.Fatalf("write provider transcript: %v", err)
	}
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(sessions.SessionRecord{
		Provider:       sessions.ProviderCodex,
		SessionID:      "large-transcript",
		Status:         "ended",
		TranscriptPath: providerTranscript,
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	events, err := store.ReadTranscript(sessions.ProviderCodex, "large-transcript")
	if err != nil {
		t.Fatalf("ReadTranscript() error = %v", err)
	}
	if len(events) != 1 || events[0].Text != largeText {
		t.Fatalf("large transcript event mismatch: len=%d", len(events))
	}
}

func singleSessionFile(t *testing.T, root string, provider sessions.Provider, name string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "sessions", string(provider), "*", name))
	if err != nil {
		t.Fatalf("glob session file: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("glob returned %d matches, want 1: %#v", len(matches), matches)
	}
	return matches[0]
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func providerTranscriptPath(t *testing.T, provider sessions.Provider, name string) string {
	t.Helper()
	base := t.TempDir()
	var root string
	switch provider {
	case sessions.ProviderCodex:
		t.Setenv("CODEX_HOME", base)
		root = filepath.Join(base, "sessions")
	case sessions.ProviderClaude:
		t.Setenv("CLAUDE_CONFIG_DIR", base)
		root = filepath.Join(base, "projects")
	default:
		t.Fatalf("unsupported test provider %q", provider)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create provider transcript root: %v", err)
	}
	return filepath.Join(root, name)
}
