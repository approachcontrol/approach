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
	transcriptPath := filepath.Join(root, "codex.jsonl")
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

	metaPath := singleSessionFile(t, filepath.Join(stateHome, "wtui", "sessions", "v1"), sessions.ProviderCodex, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("expected default-root metadata at %s: %v", metaPath, err)
	}
}

func TestNewStoreRejectsRelativeRoot(t *testing.T) {
	_, err := sessions.NewStore(sessions.StoreOptions{Root: ".wtui-sessions"})
	if err == nil {
		t.Fatal("NewStore() error = nil, want relative root error")
	}
	if !strings.Contains(err.Error(), "session store root must be absolute") {
		t.Fatalf("NewStore() error = %q", err)
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
	providerTranscript := filepath.Join(root, "provider.jsonl")
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
	providerTranscript := filepath.Join(root, "provider.jsonl")
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
	providerTranscript := filepath.Join(root, "large.jsonl")
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
