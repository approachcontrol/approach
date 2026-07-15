package sessions

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/internal/artifacts"
)

type IngestOptions struct {
	StateRoot          string
	CopyRawTranscripts bool
	FlowPresets        []flowstore.Preset
	Env                map[string]string
}

func IngestHook(provider Provider, input io.Reader, opts IngestOptions) (SessionRecord, error) {
	var payload hookPayload
	if err := json.NewDecoder(input).Decode(&payload); err != nil {
		return SessionRecord{}, fmt.Errorf("parse hook payload: %w", err)
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return SessionRecord{}, fmt.Errorf("%s hook payload has no usable session ID; rejecting session capture", provider)
	}
	now := time.Now().UTC()
	record := SessionRecord{
		Provider:       provider,
		SessionID:      sessionID,
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
	if record.TranscriptPath != "" {
		canonical, err := ValidateTranscriptPath(provider, record.TranscriptPath, opts.Env)
		if err != nil {
			return SessionRecord{}, err
		}
		record.TranscriptPath = canonical
	}
	stateRoot := opts.StateRoot
	if stateRoot == "" {
		stateRoot = opts.Env["WTUI_SESSION_STATE_ROOT"]
	}
	store, err := NewStore(StoreOptions{Root: stateRoot, CopyRawTranscripts: opts.CopyRawTranscripts, Env: opts.Env})
	if err != nil {
		return SessionRecord{}, err
	}
	store.launchStale = flowLaunchStaleResolver(record, opts)
	record, err = store.upsert(record)
	if err != nil {
		return SessionRecord{}, err
	}
	// Upsert releases the per-session lock before Flow attachment. Keeping that
	// boundary prevents a session-lock -> Flow-lock edge in the store lock order.
	attachFlowSession(record, opts)
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
	if record.PlanID == "" {
		record.PlanID = env["WTUI_PLAN_ID"]
	}
	if record.PlanPath == "" {
		record.PlanPath = env["WTUI_PLAN_PATH"]
	}
	if record.FlowID == "" {
		record.FlowID = env["WTUI_FLOW_ID"]
	}
	if record.FlowPhaseID == "" {
		record.FlowPhaseID = env["WTUI_FLOW_PHASE_ID"]
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

func attachFlowSession(record SessionRecord, opts IngestOptions) {
	if record.FlowID == "" || record.FlowPhaseID == "" || strings.TrimSpace(record.SessionID) == "" {
		return
	}
	root := flowStateRoot(opts)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: opts.FlowPresets})
	if err != nil {
		return
	}
	_, _ = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: record.FlowPhaseID,
		Session: flowstore.Session{
			Provider:       string(record.Provider),
			SessionID:      record.SessionID,
			LaunchID:       record.LaunchID,
			Status:         record.Status,
			StartedAt:      record.StartedAt,
			EndedAt:        record.EndedAt,
			TranscriptPath: record.TranscriptPath,
		},
	})
}

func flowLaunchStaleResolver(record SessionRecord, opts IngestOptions) launchStaleFunc {
	if record.FlowID == "" || record.FlowPhaseID == "" {
		return nil
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: flowStateRoot(opts), Presets: opts.FlowPresets})
	if err != nil {
		return nil
	}
	// This resolver runs while the session lock is held. Keep it read-only:
	// taking a Flow lock here would create a session-lock -> Flow-lock edge.
	return func(existing, incoming SessionRecord) (bool, bool) {
		if existing.FlowID == "" || existing.FlowID != incoming.FlowID ||
			existing.FlowPhaseID == "" || artifacts.NormalizePhaseID(existing.FlowPhaseID) != artifacts.NormalizePhaseID(incoming.FlowPhaseID) {
			return false, false
		}
		flow, err := store.Read(incoming.FlowID)
		if err != nil {
			return false, false
		}
		phaseID := artifacts.NormalizePhaseID(incoming.FlowPhaseID)
		for _, phase := range flow.Phases {
			if artifacts.NormalizePhaseID(phase.PhaseID) != phaseID {
				continue
			}
			incomingIndex, existingIndex := -1, -1
			for i, launchID := range phase.LaunchIDs {
				if launchID == incoming.LaunchID {
					incomingIndex = i
				}
				if launchID == existing.LaunchID {
					existingIndex = i
				}
			}
			if incomingIndex >= 0 && existingIndex >= 0 {
				return incomingIndex < existingIndex, true
			}
		}
		return false, false
	}
}

func flowStateRoot(opts IngestOptions) string {
	if root := opts.Env["WTUI_FLOW_STATE_ROOT"]; root != "" {
		return root
	}
	if root := opts.Env["WTUI_PLAN_STATE_ROOT"]; root != "" {
		return root
	}
	if opts.StateRoot != "" {
		return opts.StateRoot
	}
	return opts.Env["WTUI_SESSION_STATE_ROOT"]
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
