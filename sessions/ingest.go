package sessions

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type IngestOptions struct {
	StateRoot          string
	CopyRawTranscripts bool
	Env                map[string]string
}

func IngestHook(provider Provider, input io.Reader, opts IngestOptions) (SessionRecord, error) {
	var payload hookPayload
	if err := json.NewDecoder(input).Decode(&payload); err != nil {
		return SessionRecord{}, fmt.Errorf("parse hook payload: %w", err)
	}
	now := time.Now().UTC()
	record := SessionRecord{
		Provider:       provider,
		SessionID:      payload.SessionID,
		Status:         statusForPayload(provider, payload),
		StartedAt:      payload.StartedAt,
		EndedAt:        payload.EndedAt,
		CWD:            payload.CWD,
		Model:          payload.Model,
		Summary:        summaryForPayload(payload),
		TranscriptPath: payload.TranscriptPath,
		CaptureSource:  "hook",
	}
	if record.EndedAt.IsZero() && provider == ProviderClaude {
		record.EndedAt = now
	}
	if record.EndedAt.IsZero() && record.Status == "ended" {
		record.EndedAt = now
	}
	if !payload.Timestamp.IsZero() {
		record.LastSeenAt = payload.Timestamp
	}
	if record.LastSeenAt.IsZero() && !payload.EndedAt.IsZero() {
		record.LastSeenAt = payload.EndedAt
	}
	if record.LastSeenAt.IsZero() {
		record.LastSeenAt = now
	}
	applyEnvMetadata(&record, opts.Env)
	resolveGitMetadata(&record)
	stateRoot := opts.StateRoot
	if stateRoot == "" {
		stateRoot = opts.Env["WTUI_SESSION_STATE_ROOT"]
	}
	store, err := NewStore(StoreOptions{Root: stateRoot, CopyRawTranscripts: opts.CopyRawTranscripts})
	if err != nil {
		return SessionRecord{}, err
	}
	if err := store.Upsert(record); err != nil {
		return SessionRecord{}, err
	}
	return record, nil
}

type hookPayload struct {
	SessionID      string    `json:"session_id"`
	CWD            string    `json:"cwd"`
	Model          string    `json:"model"`
	Summary        string    `json:"summary"`
	TranscriptPath string    `json:"transcript_path"`
	Timestamp      time.Time `json:"timestamp"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at"`
	HookEventName  string    `json:"hook_event_name"`
	Reason         string    `json:"reason"`
	LastAssistant  string    `json:"last_assistant_message"`
}

func statusForPayload(provider Provider, payload hookPayload) string {
	if provider == ProviderClaude {
		return "ended"
	}
	if provider == ProviderCodex && payload.HookEventName == "Stop" {
		return "ended"
	}
	return "last_seen"
}

func summaryForPayload(payload hookPayload) string {
	if payload.Summary != "" {
		return payload.Summary
	}
	if payload.LastAssistant != "" {
		return payload.LastAssistant
	}
	if payload.Reason != "" {
		return "Session ended: " + payload.Reason
	}
	return ""
}

func applyEnvMetadata(record *SessionRecord, env map[string]string) {
	if record.LaunchID == "" {
		record.LaunchID = env["WTUI_LAUNCH_ID"]
	}
	if record.RepoPath == "" {
		record.RepoPath = env["WTUI_REPO_PATH"]
	}
	if record.WorktreePath == "" {
		record.WorktreePath = env["WTUI_WORKTREE_PATH"]
	}
	if record.Branch == "" {
		record.Branch = env["WTUI_BRANCH"]
	}
	if record.Commit == "" {
		record.Commit = env["WTUI_COMMIT"]
	}
}

func resolveGitMetadata(record *SessionRecord) {
	if record.CWD == "" {
		return
	}
	worktreePath := ""
	if out, err := gitOutput(record.CWD, "rev-parse", "--show-toplevel"); err == nil {
		worktreePath = out
	}
	gitCommonDir := ""
	if out, err := gitOutput(record.CWD, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		gitCommonDir = out
	}
	gitDir := ""
	if out, err := gitOutput(record.CWD, "rev-parse", "--path-format=absolute", "--git-dir"); err == nil {
		gitDir = out
	}
	isBare := false
	if out, err := gitOutput(record.CWD, "rev-parse", "--is-bare-repository"); err == nil {
		isBare = out == "true"
	}
	commonDirIsBare := false
	if gitCommonDir != "" {
		if out, err := gitOutput(gitCommonDir, "rev-parse", "--is-bare-repository"); err == nil {
			commonDirIsBare = out == "true"
		}
	}
	repoPath := repoPathFromGitMetadata(worktreePath, gitDir, gitCommonDir, isBare, commonDirIsBare)
	if record.RepoPath == "" {
		if repoPath != "" {
			record.RepoPath = repoPath
		} else if worktreePath != "" {
			record.RepoPath = worktreePath
		}
	}
	if record.WorktreePath == "" {
		if worktreePath != "" {
			record.WorktreePath = worktreePath
		} else {
			record.WorktreePath = record.RepoPath
		}
	}
	if record.Branch == "" {
		if out, err := gitOutput(record.CWD, "branch", "--show-current"); err == nil {
			record.Branch = out
		}
	}
	if record.Commit == "" {
		if out, err := gitOutput(record.CWD, "rev-parse", "HEAD"); err == nil {
			record.Commit = out
		}
	}
}

func repoPathFromGitMetadata(worktreePath, gitDir, commonDir string, isBare, commonDirIsBare bool) string {
	if isBare {
		if commonDir != "" {
			return filepath.Clean(commonDir)
		}
		if gitDir == "" {
			return ""
		}
		return filepath.Clean(gitDir)
	}
	if commonDir != "" && gitDir != "" && isLinkedWorktreeGitDir(gitDir, commonDir) {
		if commonDirIsBare {
			return filepath.Clean(commonDir)
		}
		if filepath.Base(filepath.Clean(commonDir)) != ".git" {
			return worktreePath
		}
		return repoPathFromGitCommonDir(commonDir)
	}
	if worktreePath != "" {
		return worktreePath
	}
	if commonDir != "" {
		return repoPathFromGitCommonDir(commonDir)
	}
	if gitDir == "" {
		return ""
	}
	return repoPathFromGitCommonDir(gitDir)
}

func isLinkedWorktreeGitDir(gitDir, commonDir string) bool {
	rel, err := filepath.Rel(filepath.Join(filepath.Clean(commonDir), "worktrees"), filepath.Clean(gitDir))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func repoPathFromGitCommonDir(commonDir string) string {
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return commonDir
}

func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
