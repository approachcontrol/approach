package sessions_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/brian-bell/wtui/sessions"
)

func TestIngestHookResolvesGitMetadataFromCWD(t *testing.T) {
	repoPath := t.TempDir()
	runGit(t, repoPath, "init")
	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoPath, "add", "README.md")
	runGit(t, repoPath, "commit", "-m", "initial")
	runGit(t, repoPath, "checkout", "-b", "feature/sessions")
	canonicalRepoPath, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("resolve repo path: %v", err)
	}
	commit := gitOutput(t, repoPath, "rev-parse", "HEAD")

	payload := []byte(`{"session_id":"codex-git-session","cwd":` + quoteJSON(repoPath) + `}`)
	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(payload), sessions.IngestOptions{})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	if record.RepoPath != canonicalRepoPath {
		t.Fatalf("RepoPath = %q, want %q", record.RepoPath, canonicalRepoPath)
	}
	if record.WorktreePath != canonicalRepoPath {
		t.Fatalf("WorktreePath = %q, want %q", record.WorktreePath, canonicalRepoPath)
	}
	if record.Branch != "feature/sessions" {
		t.Fatalf("Branch = %q, want feature/sessions", record.Branch)
	}
	if record.Commit != commit {
		t.Fatalf("Commit = %q, want %q", record.Commit, commit)
	}
}

func TestIngestHookCreatesEndedClaudeRecordFromSessionEnd(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "worktree")
	repoPath := filepath.Join(root, "repo")
	transcriptPath := filepath.Join(root, "claude-transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"timestamp":"2026-06-06T14:01:00Z","role":"user","kind":"message","text":"Fix scanner tests"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	payload := []byte(`{
		"session_id": "claude-session-1",
		"cwd": ` + quoteJSON(cwd) + `,
		"transcript_path": ` + quoteJSON(transcriptPath) + `,
		"model": "claude-opus-4",
		"summary": "Fix scanner tests",
		"started_at": "2026-06-06T14:00:00Z",
		"ended_at": "2026-06-06T14:45:00Z"
	}`)

	record, err := sessions.IngestHook(sessions.ProviderClaude, bytes.NewReader(payload), sessions.IngestOptions{
		Env: map[string]string{
			"WTUI_LAUNCH_ID":          "launch-claude-1",
			"WTUI_REPO_PATH":          repoPath,
			"WTUI_WORKTREE_PATH":      cwd,
			"WTUI_BRANCH":             "feature/sessions",
			"WTUI_COMMIT":             "abcdef123456",
			"WTUI_SESSION_STATE_ROOT": root,
		},
	})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	wantEndedAt := time.Date(2026, 6, 6, 14, 45, 0, 0, time.UTC)
	if record.Provider != sessions.ProviderClaude ||
		record.SessionID != "claude-session-1" ||
		record.LaunchID != "launch-claude-1" ||
		record.Status != "ended" ||
		record.CWD != cwd ||
		record.RepoPath != repoPath ||
		record.WorktreePath != cwd ||
		record.Branch != "feature/sessions" ||
		record.Commit != "abcdef123456" ||
		record.TranscriptPath != transcriptPath ||
		record.Summary != "Fix scanner tests" ||
		record.CaptureSource != "hook" ||
		!record.EndedAt.Equal(wantEndedAt) ||
		!record.LastSeenAt.Equal(wantEndedAt) {
		t.Fatalf("Claude record mismatch: %#v", record)
	}
}

func TestIngestHookPersistsCodexStopSnapshotsInPlace(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	transcriptPath := filepath.Join(root, "codex.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"timestamp":"2026-06-06T14:10:00Z","role":"user","kind":"message","text":"first prompt"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	env := map[string]string{
		"WTUI_SESSION_STATE_ROOT": root,
		"WTUI_REPO_PATH":          repoPath,
	}

	first := []byte(`{
		"session_id": "codex-session-1",
		"cwd": ` + quoteJSON(repoPath) + `,
		"transcript_path": ` + quoteJSON(transcriptPath) + `,
		"model": "gpt-5",
		"summary": "first prompt",
		"timestamp": "2026-06-06T14:10:00Z"
	}`)
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(first), sessions.IngestOptions{Env: env}); err != nil {
		t.Fatalf("first IngestHook() error = %v", err)
	}

	second := []byte(`{
		"session_id": "codex-session-1",
		"cwd": ` + quoteJSON(repoPath) + `,
		"transcript_path": ` + quoteJSON(transcriptPath) + `,
		"model": "gpt-5",
		"summary": "updated prompt",
		"timestamp": "2026-06-06T14:20:00Z"
	}`)
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(second), sessions.IngestOptions{Env: env}); err != nil {
		t.Fatalf("second IngestHook() error = %v", err)
	}

	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	records, err := store.List(sessions.SessionFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1: %#v", len(records), records)
	}
	got := records[0]
	wantLastSeen := time.Date(2026, 6, 6, 14, 20, 0, 0, time.UTC)
	if got.Provider != sessions.ProviderCodex ||
		got.SessionID != "codex-session-1" ||
		got.Status != "last_seen" ||
		got.Summary != "updated prompt" ||
		!got.LastSeenAt.Equal(wantLastSeen) {
		t.Fatalf("Codex snapshot mismatch: %#v", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return string(bytes.TrimSpace(out))
}

func quoteJSON(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
