package sessions_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/sessions"
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

func TestStoreMarksLaunchEnded(t *testing.T) {
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
		LaunchID:   "launch-1",
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
	got := records[0]
	if got.Status != "ended" || !got.EndedAt.Equal(endedAt) || !got.LastSeenAt.Equal(endedAt) {
		t.Fatalf("launch was not ended: %#v", got)
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

	metaPath := filepath.Join(stateHome, "wtui", "sessions", "v1", "sessions", "codex", "default-root", "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("expected default-root metadata at %s: %v", metaPath, err)
	}
}

func TestStoreCopiesRawTranscriptAndReadsNormalizedEvents(t *testing.T) {
	root := t.TempDir()
	providerTranscript := filepath.Join(root, "provider.jsonl")
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

	rawPath := filepath.Join(root, "sessions", "codex", "with-transcript", "raw.jsonl")
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
