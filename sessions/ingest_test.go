package sessions_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

func TestIngestHookValidatesProviderTranscriptPathsBeforePersistence(t *testing.T) {
	providerCases := []struct {
		name                 string
		provider             sessions.Provider
		rawTranscript        []byte
		normalizedTranscript []byte
	}{
		{
			name:                 "codex",
			provider:             sessions.ProviderCodex,
			rawTranscript:        []byte(`{"timestamp":"2026-08-08T20:00:00Z","role":"user","content":"codex transcript"}` + "\n"),
			normalizedTranscript: []byte(`{"timestamp":"2026-08-08T20:00:00Z","role":"user","kind":"message","text":"codex transcript"}` + "\n"),
		},
		{
			name:                 "claude",
			provider:             sessions.ProviderClaude,
			rawTranscript:        []byte(`{"timestamp":"2026-08-08T20:00:00Z","type":"user","message":{"role":"user","content":[{"type":"text","text":"claude transcript"}]}}` + "\n"),
			normalizedTranscript: []byte(`{"timestamp":"2026-08-08T20:00:00Z","role":"user","kind":"message","text":"claude transcript"}` + "\n"),
		},
	}

	for _, providerCase := range providerCases {
		t.Run(providerCase.name, func(t *testing.T) {
			t.Run("accepts in-root regular file", func(t *testing.T) {
				stateRoot := t.TempDir()
				transcriptRoot, env := providerTranscriptRoot(t, providerCase.provider)
				transcriptPath := filepath.Join(transcriptRoot, "valid.jsonl")
				if err := os.WriteFile(transcriptPath, providerCase.rawTranscript, 0o600); err != nil {
					t.Fatalf("write provider transcript: %v", err)
				}
				canonicalPath, err := filepath.EvalSymlinks(transcriptPath)
				if err != nil {
					t.Fatalf("canonicalize provider transcript: %v", err)
				}
				sessionID := "valid-" + providerCase.name
				payload := []byte(`{"session_id":` + quoteJSON(sessionID) + `,"transcript_path":` + quoteJSON(transcriptPath) + `}`)

				record, err := sessions.IngestHook(providerCase.provider, bytes.NewReader(payload), sessions.IngestOptions{
					StateRoot:          stateRoot,
					CopyRawTranscripts: true,
					Env:                env,
				})
				if err != nil {
					t.Fatalf("IngestHook() error = %v", err)
				}
				if record.TranscriptPath != canonicalPath {
					t.Fatalf("TranscriptPath = %q, want canonical path %q", record.TranscriptPath, canonicalPath)
				}
				store, err := sessions.NewStore(sessions.StoreOptions{Root: stateRoot})
				if err != nil {
					t.Fatalf("NewStore() error = %v", err)
				}
				records, err := store.List(sessions.SessionFilter{Provider: providerCase.provider})
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
				if len(records) != 1 || records[0].TranscriptPath != canonicalPath {
					t.Fatalf("stored records = %#v, want one record with canonical transcript path %q", records, canonicalPath)
				}

				artifactDir := sessionArtifactDir(stateRoot, providerCase.provider, sessionID)
				raw, err := os.ReadFile(filepath.Join(artifactDir, "raw.jsonl"))
				if err != nil {
					t.Fatalf("read raw transcript: %v", err)
				}
				if !bytes.Equal(raw, providerCase.rawTranscript) {
					t.Fatalf("raw transcript = %q, want %q", raw, providerCase.rawTranscript)
				}
				normalized, err := os.ReadFile(filepath.Join(artifactDir, "transcript.jsonl"))
				if err != nil {
					t.Fatalf("read normalized transcript: %v", err)
				}
				if !bytes.Equal(normalized, providerCase.normalizedTranscript) {
					t.Fatalf("normalized transcript = %q, want %q", normalized, providerCase.normalizedTranscript)
				}
				assertMode(t, artifactDir, 0o700)
				for _, name := range []string{"meta.json", "transcript.jsonl", "raw.jsonl"} {
					assertMode(t, filepath.Join(artifactDir, name), 0o600)
				}
			})

			rejectedCases := []struct {
				name          string
				errorContains string
				payloadRoot   bool
				path          func(t *testing.T, transcriptRoot string) string
			}{
				{
					name:          "relative",
					errorContains: "must be absolute",
					path: func(_ *testing.T, _ string) string {
						return "relative.jsonl"
					},
				},
				{
					name:          "parent traversal",
					errorContains: "outside expected",
					path: func(t *testing.T, transcriptRoot string) string {
						outsideName := "parent-traversal.jsonl"
						outsidePath := filepath.Join(filepath.Dir(transcriptRoot), outsideName)
						if err := os.WriteFile(outsidePath, []byte("outside parent\n"), 0o600); err != nil {
							t.Fatalf("write parent traversal target: %v", err)
						}
						return transcriptRoot + string(filepath.Separator) + ".." + string(filepath.Separator) + outsideName
					},
				},
				{
					name:          "sibling prefix",
					errorContains: "outside expected",
					path: func(t *testing.T, transcriptRoot string) string {
						siblingRoot := filepath.Join(filepath.Dir(transcriptRoot), filepath.Base(transcriptRoot)+"-sibling")
						if err := os.MkdirAll(siblingRoot, 0o700); err != nil {
							t.Fatalf("create sibling transcript root: %v", err)
						}
						outsidePath := filepath.Join(siblingRoot, "prefix-collision.jsonl")
						if err := os.WriteFile(outsidePath, []byte("outside sibling\n"), 0o600); err != nil {
							t.Fatalf("write sibling-prefix target: %v", err)
						}
						return outsidePath
					},
				},
				{
					name:          "symlink escape",
					errorContains: "outside expected",
					path: func(t *testing.T, transcriptRoot string) string {
						outsidePath := filepath.Join(t.TempDir(), "symlink-target.jsonl")
						if err := os.WriteFile(outsidePath, []byte("outside symlink\n"), 0o600); err != nil {
							t.Fatalf("write symlink target: %v", err)
						}
						symlinkPath := filepath.Join(transcriptRoot, "escape.jsonl")
						if err := os.Symlink(outsidePath, symlinkPath); err != nil {
							t.Fatalf("create transcript symlink: %v", err)
						}
						return symlinkPath
					},
				},
				{
					name:          "hook supplied root",
					errorContains: "outside expected",
					payloadRoot:   true,
					path: func(t *testing.T, _ string) string {
						attackerRoot := t.TempDir()
						outsidePath := filepath.Join(attackerRoot, "payload-root.jsonl")
						if err := os.WriteFile(outsidePath, []byte("outside payload root\n"), 0o600); err != nil {
							t.Fatalf("write hook-supplied-root target: %v", err)
						}
						return outsidePath
					},
				},
			}

			for _, rejectedCase := range rejectedCases {
				t.Run("rejects "+rejectedCase.name, func(t *testing.T) {
					stateRoot := t.TempDir()
					transcriptRoot, env := providerTranscriptRoot(t, providerCase.provider)
					transcriptPath := rejectedCase.path(t, transcriptRoot)
					sessionID := "rejected-" + providerCase.name + "-" + strings.ReplaceAll(rejectedCase.name, " ", "-")
					flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: stateRoot})
					if err != nil {
						t.Fatalf("NewStore() error = %v", err)
					}
					flow, err := flowStore.Create(flowstore.FlowRecord{
						FlowID:       "flow-" + sessionID,
						Title:        "Reject untrusted transcript",
						Instructions: "Reject before persistence",
						RepoPath:     filepath.Join(stateRoot, "repo"),
					})
					if err != nil {
						t.Fatalf("Create() error = %v", err)
					}
					env["APPROACH_FLOW_STATE_ROOT"] = stateRoot
					env["APPROACH_FLOW_ID"] = flow.FlowID
					env["APPROACH_FLOW_PHASE_ID"] = "plan"
					payloadRoot := ""
					if rejectedCase.payloadRoot {
						payloadRoot = `,"transcript_root":` + quoteJSON(filepath.Dir(transcriptPath))
					}
					payload := []byte(`{"session_id":` + quoteJSON(sessionID) + `,"transcript_path":` + quoteJSON(transcriptPath) + payloadRoot + `}`)

					_, err = sessions.IngestHook(providerCase.provider, bytes.NewReader(payload), sessions.IngestOptions{
						StateRoot:          stateRoot,
						CopyRawTranscripts: true,
						Env:                env,
					})
					if err == nil {
						t.Fatalf("IngestHook(%q) error = nil, want transcript rejection", transcriptPath)
					}
					if !strings.Contains(err.Error(), rejectedCase.errorContains) {
						t.Fatalf("IngestHook(%q) error = %v, want %q", transcriptPath, err, rejectedCase.errorContains)
					}

					artifactDir := sessionArtifactDir(stateRoot, providerCase.provider, sessionID)
					for _, path := range []string{
						artifactDir,
						filepath.Join(artifactDir, "meta.json"),
						filepath.Join(artifactDir, "transcript.jsonl"),
						filepath.Join(artifactDir, "raw.jsonl"),
					} {
						if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
							t.Fatalf("rejected transcript published artifact %s: %v", path, statErr)
						}
					}
					assertNoSessionRecords(t, stateRoot)

					read, err := flowStore.Read(flow.FlowID)
					if err != nil {
						t.Fatalf("Read() error = %v", err)
					}
					phase := flowPhaseByID(t, read, "plan")
					if len(phase.Sessions) != 0 {
						t.Fatalf("rejected transcript attached sessions = %#v, want none", phase.Sessions)
					}
				})
			}
		})
	}
}

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
			"APPROACH_LAUNCH_ID":          "launch-claude-1",
			"APPROACH_REPO_PATH":          repoPath,
			"APPROACH_WORKTREE_PATH":      cwd,
			"APPROACH_BRANCH":             "feature/sessions",
			"APPROACH_COMMIT":             "abcdef123456",
			"APPROACH_SESSION_STATE_ROOT": root,
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
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_REPO_PATH":          repoPath,
			"APPROACH_WORKTREE_PATH":      worktreePath,
			"APPROACH_BRANCH":             "flow/new-flow-launch",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
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

func TestIngestHookPersistsUntrackedFlowRepairWithoutAttachingPhase(t *testing.T) {
	root := t.TempDir()
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-repair",
		Title:        "Repair session",
		Instructions: "Repair persisted state",
		RepoPath:     "/repo",
		WorktreePath: "/repo/worktree",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "codex-repair-1",
		"timestamp": "2026-08-09T18:00:00Z"
	}`)), sessions.IngestOptions{Env: map[string]string{
		"APPROACH_LAUNCH_ID":          "repair-launch-1",
		"APPROACH_REPO_PATH":          "/repo",
		"APPROACH_WORKTREE_PATH":      "/repo/worktree",
		"APPROACH_SESSION_STATE_ROOT": root,
		"APPROACH_FLOW_STATE_ROOT":    root,
		"APPROACH_FLOW_ID":            flow.FlowID,
		"APPROACH_FLOW_PHASE_ID":      "",
	}})
	if err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}
	if record.FlowID != flow.FlowID || record.FlowPhaseID != "" || record.LaunchID != "repair-launch-1" {
		t.Fatalf("repair session metadata = %#v", record)
	}

	read, err := flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	for _, phase := range read.Phases {
		if len(phase.LaunchIDs) != 0 || len(phase.Sessions) != 0 {
			t.Fatalf("repair attached to phase %s: launches=%#v sessions=%#v", phase.PhaseID, phase.LaunchIDs, phase.Sessions)
		}
	}
}

func TestIngestHookUsesFlowLaunchOrderToRejectStaleSessionUpdate(t *testing.T) {
	root := t.TempDir()
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-launch-order",
		Title:        "Fence stale hook",
		Instructions: "Keep the resumed launch current",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, launchID := range []string{"launch-1", "launch-2"} {
		flow, err = flowStore.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
			FlowID:   flow.FlowID,
			PhaseID:  "plan",
			LaunchID: launchID,
		})
		if err != nil {
			t.Fatalf("AddPhaseLaunchID(%s) error = %v", launchID, err)
		}
	}
	envForLaunch := func(launchID string) map[string]string {
		return map[string]string{
			"APPROACH_LAUNCH_ID":          launchID,
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
		}
	}
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "shared-session",
		"timestamp": "2026-07-15T14:20:00Z"
	}`)), sessions.IngestOptions{Env: envForLaunch("launch-2")}); err != nil {
		t.Fatalf("current IngestHook() error = %v", err)
	}
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "shared-session",
		"timestamp": "2026-07-15T14:30:00Z",
		"hook_event_name": "Stop"
	}`)), sessions.IngestOptions{Env: envForLaunch("launch-1")}); err != nil {
		t.Fatalf("stale IngestHook() error = %v", err)
	}

	sessionStore, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	records, err := sessionStore.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].LaunchID != "launch-2" || records[0].Status != "last_seen" {
		t.Fatalf("session regressed after stale hook: %#v", records)
	}
	flow, err = flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := flowPhaseByID(t, flow, "plan")
	if len(phase.Sessions) != 1 || phase.Sessions[0].LaunchID != "launch-2" || phase.Sessions[0].Status != "last_seen" {
		t.Fatalf("Flow session regressed after stale hook: %#v", phase.Sessions)
	}
}

func TestIngestHookDoesNotRestoreFlowLinkForOrdinarySessionResume(t *testing.T) {
	root := t.TempDir()
	trackedLaunchID := "approach-100-aaaaaaaaaaaa"
	resumeLaunchID := "approach-200-bbbbbbbbbbbb"
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-untracked-resume",
		Title:        "Keep ordinary resume untracked",
		Instructions: "Do not restore old Flow metadata",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	flow, err = flowStore.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   flow.FlowID,
		PhaseID:  "plan",
		LaunchID: trackedLaunchID,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "shared-session",
		"timestamp": "2026-07-15T14:10:00Z"
	}`)), sessions.IngestOptions{Env: map[string]string{
		"APPROACH_LAUNCH_ID":          trackedLaunchID,
		"APPROACH_SESSION_STATE_ROOT": root,
		"APPROACH_FLOW_STATE_ROOT":    root,
		"APPROACH_FLOW_ID":            flow.FlowID,
		"APPROACH_FLOW_PHASE_ID":      "plan",
	}}); err != nil {
		t.Fatalf("tracked IngestHook() error = %v", err)
	}

	record, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "shared-session",
		"timestamp": "2026-07-15T14:20:00Z"
	}`)), sessions.IngestOptions{Env: map[string]string{
		"APPROACH_LAUNCH_ID":          resumeLaunchID,
		"APPROACH_SESSION_STATE_ROOT": root,
	}})
	if err != nil {
		t.Fatalf("ordinary resume IngestHook() error = %v", err)
	}
	if record.LaunchID != resumeLaunchID || record.FlowID != "" || record.FlowPhaseID != "" {
		t.Fatalf("ordinary resume restored Flow metadata: %#v", record)
	}
	record, err = sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "shared-session",
		"timestamp": "2026-07-15T14:30:00Z",
		"hook_event_name": "Stop"
	}`)), sessions.IngestOptions{Env: map[string]string{
		"APPROACH_LAUNCH_ID":          trackedLaunchID,
		"APPROACH_SESSION_STATE_ROOT": root,
		"APPROACH_FLOW_STATE_ROOT":    root,
		"APPROACH_FLOW_ID":            flow.FlowID,
		"APPROACH_FLOW_PHASE_ID":      "plan",
	}})
	if err != nil {
		t.Fatalf("delayed tracked IngestHook() error = %v", err)
	}
	if record.LaunchID != resumeLaunchID || record.FlowID != "" || record.FlowPhaseID != "" || record.Status != "last_seen" {
		t.Fatalf("delayed tracked hook regressed ordinary resume: %#v", record)
	}

	flow, err = flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phase := flowPhaseByID(t, flow, "plan")
	if len(phase.Sessions) != 1 || phase.Sessions[0].LaunchID != trackedLaunchID {
		t.Fatalf("ordinary resume changed Flow attachment: %#v", phase.Sessions)
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
		"APPROACH_SESSION_STATE_ROOT": root,
		"APPROACH_REPO_PATH":          repoPath,
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
			"APPROACH_FLOW_STATE_ROOT": root,
			"APPROACH_FLOW_ID":         flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":   "plan",
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

func providerTranscriptRoot(t *testing.T, provider sessions.Provider) (string, map[string]string) {
	t.Helper()
	providerHome := filepath.Join(t.TempDir(), string(provider)+"-home")
	env := make(map[string]string)
	var transcriptRoot string
	switch provider {
	case sessions.ProviderCodex:
		env["CODEX_HOME"] = providerHome
		transcriptRoot = filepath.Join(providerHome, "sessions")
	case sessions.ProviderClaude:
		env["CLAUDE_CONFIG_DIR"] = providerHome
		transcriptRoot = filepath.Join(providerHome, "projects")
	default:
		t.Fatalf("unsupported test provider %q", provider)
	}
	if err := os.MkdirAll(transcriptRoot, 0o700); err != nil {
		t.Fatalf("create provider transcript root: %v", err)
	}
	return transcriptRoot, env
}

func sessionArtifactDir(root string, provider sessions.Provider, sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(root, "sessions", string(provider), hex.EncodeToString(sum[:]))
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
