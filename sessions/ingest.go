package sessions

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
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
	record := SessionRecord{
		Provider:       provider,
		SessionID:      payload.SessionID,
		Status:         statusForProvider(provider),
		StartedAt:      payload.StartedAt,
		EndedAt:        payload.EndedAt,
		CWD:            payload.CWD,
		Model:          payload.Model,
		Summary:        payload.Summary,
		TranscriptPath: payload.TranscriptPath,
		CaptureSource:  "hook",
	}
	if !payload.Timestamp.IsZero() {
		record.LastSeenAt = payload.Timestamp
	}
	if record.LastSeenAt.IsZero() && !payload.EndedAt.IsZero() {
		record.LastSeenAt = payload.EndedAt
	}
	applyEnvMetadata(&record, opts.Env)
	resolveGitMetadata(&record)
	stateRoot := opts.StateRoot
	if stateRoot == "" {
		stateRoot = opts.Env["WTUI_SESSION_STATE_ROOT"]
	}
	if stateRoot != "" {
		store, err := NewStore(StoreOptions{Root: stateRoot, CopyRawTranscripts: opts.CopyRawTranscripts})
		if err != nil {
			return SessionRecord{}, err
		}
		if err := store.Upsert(record); err != nil {
			return SessionRecord{}, err
		}
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
}

func statusForProvider(provider Provider) string {
	if provider == ProviderClaude {
		return "ended"
	}
	return "last_seen"
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
	if record.RepoPath == "" {
		if out, err := gitOutput(record.CWD, "rev-parse", "--show-toplevel"); err == nil {
			record.RepoPath = out
		}
	}
	if record.WorktreePath == "" {
		record.WorktreePath = record.RepoPath
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

func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
