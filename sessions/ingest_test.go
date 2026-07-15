package sessions_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/sessions"
)

func TestIngestHookResolvesGitMetadataFromCWD(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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

func TestIngestHookResolvesLinkedWorktreeToMainRepoPath(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo-worktrees", "feature")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	runGit(t, repoPath, "init")
	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoPath, "add", "README.md")
	runGit(t, repoPath, "commit", "-m", "initial")
	runGit(t, repoPath, "worktree", "add", "-b", "feature/sessions", worktreePath, "HEAD")
	canonicalRepoPath, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("resolve repo path: %v", err)
	}
	canonicalWorktreePath, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}

	payload := []byte(`{"session_id":"manual-linked-worktree","cwd":` + quoteJSON(worktreePath) + `}`)
	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(payload), sessions.IngestOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	if record.RepoPath != canonicalRepoPath {
		t.Fatalf("RepoPath = %q, want main repo %q", record.RepoPath, canonicalRepoPath)
	}
	if record.WorktreePath != canonicalWorktreePath {
		t.Fatalf("WorktreePath = %q, want linked worktree %q", record.WorktreePath, canonicalWorktreePath)
	}
	if record.Branch != "feature/sessions" {
		t.Fatalf("Branch = %q, want feature/sessions", record.Branch)
	}
}

func TestIngestHookKeepsSeparateGitDirRepoPathAsWorktree(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	gitDir := filepath.Join(root, "gitdata")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	runGit(t, root, "init", "--separate-git-dir", gitDir, repoPath)
	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoPath, "add", "README.md")
	runGit(t, repoPath, "commit", "-m", "initial")
	canonicalRepoPath, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("resolve repo path: %v", err)
	}

	payload := []byte(`{"session_id":"manual-separate-git-dir","cwd":` + quoteJSON(repoPath) + `}`)
	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(payload), sessions.IngestOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	if record.RepoPath != canonicalRepoPath {
		t.Fatalf("RepoPath = %q, want worktree repo %q", record.RepoPath, canonicalRepoPath)
	}
	if record.WorktreePath != canonicalRepoPath {
		t.Fatalf("WorktreePath = %q, want worktree repo %q", record.WorktreePath, canonicalRepoPath)
	}
}

func TestIngestHookAvoidsSeparateGitDirMetadataPathForLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	gitDir := filepath.Join(root, "gitdata")
	worktreePath := filepath.Join(root, "repo-worktrees", "feature")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	runGit(t, root, "init", "--separate-git-dir", gitDir, repoPath)
	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoPath, "add", "README.md")
	runGit(t, repoPath, "commit", "-m", "initial")
	runGit(t, repoPath, "worktree", "add", "-b", "feature/sessions", worktreePath, "HEAD")
	canonicalWorktreePath, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}

	payload := []byte(`{"session_id":"manual-separate-git-dir-linked","cwd":` + quoteJSON(worktreePath) + `}`)
	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(payload), sessions.IngestOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	if record.RepoPath != canonicalWorktreePath {
		t.Fatalf("RepoPath = %q, want linked worktree %q", record.RepoPath, canonicalWorktreePath)
	}
	if record.WorktreePath != canonicalWorktreePath {
		t.Fatalf("WorktreePath = %q, want linked worktree %q", record.WorktreePath, canonicalWorktreePath)
	}
	if record.RepoPath == filepath.Clean(gitDir) {
		t.Fatalf("RepoPath should not be separate git metadata dir %q", gitDir)
	}
}

func TestIngestHookKeepsDotGitNamedBareRepoPathForLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	bareRepoPath := filepath.Join(root, "container", ".git")
	worktreePath := filepath.Join(root, "worktrees", "feature")
	if err := os.MkdirAll(filepath.Dir(bareRepoPath), 0o755); err != nil {
		t.Fatalf("create bare parent dir: %v", err)
	}
	runGit(t, root, "init", "--bare", bareRepoPath)
	runGit(t, bareRepoPath, "worktree", "add", "-b", "feature/sessions", worktreePath)
	canonicalBareRepoPath, err := filepath.EvalSymlinks(bareRepoPath)
	if err != nil {
		t.Fatalf("resolve bare repo path: %v", err)
	}
	canonicalWorktreePath, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}

	payload := []byte(`{"session_id":"manual-dot-git-bare-linked","cwd":` + quoteJSON(worktreePath) + `}`)
	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader(payload), sessions.IngestOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	if record.RepoPath != canonicalBareRepoPath {
		t.Fatalf("RepoPath = %q, want bare repo %q", record.RepoPath, canonicalBareRepoPath)
	}
	if record.WorktreePath != canonicalWorktreePath {
		t.Fatalf("WorktreePath = %q, want linked worktree %q", record.WorktreePath, canonicalWorktreePath)
	}
}

func TestIngestHookCreatesEndedClaudeRecordFromSessionEnd(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "worktree")
	repoPath := filepath.Join(root, "repo")
	transcriptPath := providerTranscriptPath(t, sessions.ProviderClaude, "claude-transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"timestamp":"2026-06-06T14:01:00Z","role":"user","kind":"message","text":"Fix scanner tests"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	canonicalTranscriptPath, err := filepath.EvalSymlinks(transcriptPath)
	if err != nil {
		t.Fatalf("canonicalize transcript: %v", err)
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
		record.TranscriptPath != canonicalTranscriptPath ||
		record.Summary != "Fix scanner tests" ||
		record.CaptureSource != "hook" ||
		!record.EndedAt.Equal(wantEndedAt) ||
		!record.LastSeenAt.Equal(wantEndedAt) {
		t.Fatalf("Claude record mismatch: %#v", record)
	}
}

func TestIngestHookPersistsFlowMetadataAndAttachesSession(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo-worktrees", "flow-new-flow-launch")
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "New Flow Launch",
		Instructions: "Plan the work",
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Branch:       "flow/new-flow-launch",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "codex-flow-1",
		"cwd": `+quoteJSON(worktreePath)+`,
		"timestamp": "2026-06-06T14:10:00Z"
	}`)), sessions.IngestOptions{
		Env: map[string]string{
			"WTUI_LAUNCH_ID":          "launch-flow-1",
			"WTUI_REPO_PATH":          repoPath,
			"WTUI_WORKTREE_PATH":      worktreePath,
			"WTUI_BRANCH":             "flow/new-flow-launch",
			"WTUI_SESSION_STATE_ROOT": root,
			"WTUI_FLOW_STATE_ROOT":    root,
			"WTUI_FLOW_ID":            flow.FlowID,
			"WTUI_FLOW_PHASE_ID":      "plan",
		},
	})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}

	if record.FlowID != flow.FlowID || record.FlowPhaseID != "plan" {
		t.Fatalf("flow metadata not stored on session: %#v", record)
	}
	read, err := flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := flowPhaseByID(t, read, "plan")
	if len(phase.Sessions) != 1 {
		t.Fatalf("attached sessions = %#v, want one", phase.Sessions)
	}
	attached := phase.Sessions[0]
	if attached.Provider != string(sessions.ProviderCodex) ||
		attached.SessionID != "codex-flow-1" ||
		attached.LaunchID != "launch-flow-1" ||
		attached.Status != "last_seen" {
		t.Fatalf("attached session mismatch: %#v", attached)
	}
}

func TestIngestHookPersistsToDefaultRootWhenNoStateRootProvided(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "codex-default-root",
		"cwd": "/tmp",
		"hook_event_name": "Stop",
		"last_assistant_message": "Captured without explicit state root"
	}`)), sessions.IngestOptions{})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}
	if record.Summary != "Captured without explicit state root" {
		t.Fatalf("Summary = %q", record.Summary)
	}
	if record.Status != "ended" || record.EndedAt.IsZero() {
		t.Fatalf("expected Codex Stop to be ended, got %#v", record)
	}
	if record.LastSeenAt.IsZero() {
		t.Fatal("expected LastSeenAt fallback")
	}

	store, err := sessions.NewStore(sessions.StoreOptions{})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	records, err := store.List(sessions.SessionFilter{Provider: sessions.ProviderCodex})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].SessionID != "codex-default-root" {
		t.Fatalf("default-root records = %#v", records)
	}
}

func TestIngestHookFallsBackClaudeSessionEndTimesAndSummary(t *testing.T) {
	root := t.TempDir()
	record, err := sessions.IngestHook(sessions.ProviderClaude, bytes.NewReader([]byte(`{
		"session_id": "claude-end",
		"cwd": "/tmp",
		"hook_event_name": "SessionEnd",
		"reason": "other"
	}`)), sessions.IngestOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}
	if record.Status != "ended" || record.EndedAt.IsZero() || record.LastSeenAt.IsZero() {
		t.Fatalf("expected ended Claude fallback times, got %#v", record)
	}
	if record.Summary != "Session ended: other" {
		t.Fatalf("Summary = %q", record.Summary)
	}
}

func TestIngestHookPersistsCodexStopSnapshotsInPlace(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	transcriptPath := providerTranscriptPath(t, sessions.ProviderCodex, "codex.jsonl")
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

func TestIngestHookRejectsPayloadWithBlankSessionID(t *testing.T) {
	for _, provider := range []sessions.Provider{sessions.ProviderClaude, sessions.ProviderCodex} {
		for name, sessionID := range map[string]string{
			"empty":      "",
			"whitespace": "   ",
		} {
			t.Run(string(provider)+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				payload := []byte(`{
					"session_id": ` + quoteJSON(sessionID) + `,
					"cwd": "/tmp",
					"hook_event_name": "SessionEnd"
				}`)

				_, err := sessions.IngestHook(provider, bytes.NewReader(payload), sessions.IngestOptions{StateRoot: root})
				if err == nil {
					t.Fatal("IngestHook() expected error for blank session ID")
				}
				if !strings.Contains(err.Error(), "session ID") {
					t.Fatalf("IngestHook() error = %v, want mention of session ID", err)
				}
				assertNoSessionRecords(t, root)
			})
		}
	}
}

func TestIngestHookRejectsMalformedPayload(t *testing.T) {
	root := t.TempDir()
	_, err := sessions.IngestHook(sessions.ProviderClaude, bytes.NewReader([]byte(`{"session_id": `)), sessions.IngestOptions{StateRoot: root})
	if err == nil {
		t.Fatal("IngestHook() expected error for malformed payload")
	}
	assertNoSessionRecords(t, root)
}

func TestIngestHookBlankSessionIDLeavesFlowPhaseUntouched(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-blank-session",
		Title:        "Blank session capture",
		Instructions: "Plan the work",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = sessions.IngestHook(sessions.ProviderClaude, bytes.NewReader([]byte(`{
		"session_id": "",
		"cwd": "/tmp"
	}`)), sessions.IngestOptions{
		StateRoot: root,
		Env: map[string]string{
			"WTUI_FLOW_STATE_ROOT": root,
			"WTUI_FLOW_ID":         flow.FlowID,
			"WTUI_FLOW_PHASE_ID":   "plan",
		},
	})
	if err == nil {
		t.Fatal("IngestHook() expected error for blank session ID")
	}

	read, err := flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := flowPhaseByID(t, read, "plan")
	if len(phase.Sessions) != 0 {
		t.Fatalf("blank session ID must not attach to flow phase, got %#v", phase.Sessions)
	}
}

func assertNoSessionRecords(t *testing.T, root string) {
	t.Helper()
	sessionsDir := filepath.Join(root, "sessions")
	err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			t.Fatalf("expected no persisted session files, found %s", path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk session root: %v", err)
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

func flowPhaseByID(t *testing.T, record flowstore.FlowRecord, phaseID string) flowstore.FlowPhase {
	t.Helper()
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase
		}
	}
	t.Fatalf("phase %q not found in %#v", phaseID, record.Phases)
	return flowstore.FlowPhase{}
}
