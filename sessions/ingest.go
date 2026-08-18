package sessions

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

type IngestOptions struct {
	StateRoot string
	// StateRootExplicit reports that StateRoot was named by --state-root or by
	// APPROACH_SESSION_STATE_ROOT rather than falling back to config. The
	// caller has already flattened those three sources into one string, so the
	// distinction cannot be recovered here.
	StateRootExplicit  bool
	CopyRawTranscripts bool
	FlowPresets        []flowstore.Preset
	Env                map[string]string
}

// HookResult is what a hook run produced. It is deliberately NOT persisted:
// SessionRecord's JSON is written to disk by store.upsert, and hanging warnings
// off it would change the on-disk session schema for a message whose whole
// lifetime is one command invocation.
type HookResult struct {
	Record   SessionRecord
	Warnings []string
}

// IngestHook keeps the signature the 23 existing call sites use. The warnings
// channel is a separate entry point rather than a signature change: 13 of those
// call sites bind and dereference the record, and churning them buys nothing.
func IngestHook(provider Provider, input io.Reader, opts IngestOptions) (SessionRecord, error) {
	result, err := IngestHookWithWarnings(provider, input, opts)
	return result.Record, err
}

// IngestHookWithWarnings reports the Flow-store failures the hook used to
// swallow. After role separation a post-bump session attachment fails on every
// hook run until someone migrates, and the whole point of the hook is that
// nobody is watching it — so it has to say so.
func IngestHookWithWarnings(provider Provider, input io.Reader, opts IngestOptions) (HookResult, error) {
	var warnings []string
	record, err := ingestHook(provider, input, opts, &warnings)
	return HookResult{Record: record, Warnings: warnings}, err
}

func ingestHook(provider Provider, input io.Reader, opts IngestOptions, warnings *[]string) (SessionRecord, error) {
	var payload hookPayload
	if err := json.NewDecoder(input).Decode(&payload); err != nil {
		return SessionRecord{}, fmt.Errorf("parse hook payload: %w", err)
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(payload.ConversationID)
	}
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
	if record.EndedAt.IsZero() && (provider == ProviderClaude || provider == ProviderCursor) {
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
	if record.TranscriptPath == "" && provider == ProviderCursor && opts.Env != nil {
		record.TranscriptPath = strings.TrimSpace(opts.Env["CURSOR_TRANSCRIPT_PATH"])
	}
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
		stateRoot = opts.Env["APPROACH_SESSION_STATE_ROOT"]
	}
	store, err := NewStore(StoreOptions{Root: stateRoot, CopyRawTranscripts: opts.CopyRawTranscripts, Env: opts.Env})
	if err != nil {
		return SessionRecord{}, err
	}
	launchStale, releaseLaunchStale := flowLaunchStaleResolver(record, opts, warnings)
	store.launchStale = launchStale
	// The release is idempotent, so it runs inline below AND deferred: a panic in
	// upsert would otherwise leak the Flow store's pooled handle, which is the
	// exact hygiene this resolver rework was for. Clearing launchStale is
	// future-proofing — upsert runs once today, and a resolver whose store is
	// closed would degrade silently, not loudly.
	releaseResolver := func() {
		releaseLaunchStale()
		store.launchStale = nil
	}
	defer releaseResolver()
	record, err = store.upsert(record)
	if err != nil {
		return SessionRecord{}, err
	}
	// Released before attachment, not at return: attachFlowSession opens a second
	// flowstore.Store, and the hook path holds only one pooled SQLite handle at a
	// time (docs/architecture.md). Two live pools on one database would double
	// descriptor use and let independent pools contend for the same writer.
	releaseResolver()
	// Upsert releases the per-session lock before Flow attachment. Keeping that
	// boundary prevents a session-lock -> Flow-lock edge in the store lock order.
	attachFlowSession(record, opts, warnings)
	// After the attach, and with its own store: the hook path holds one Flow
	// store handle at a time.
	reconcileFlowLaunchExit(record, opts, warnings)
	return record, nil
}

// reconcileFlowLaunchExit reports a session's end to the launch controller
// so anything the launch spooled is replayed first. Session-end is not a
// death certificate — Codex Stop is per-turn and Claude SessionEnd fires
// on /clear — so demotion waits for SessionEndGrace; the Flow lease veto
// still applies for tracked tmux launches. Failures are warnings: the
// session record this hook exists to write was still captured.
func reconcileFlowLaunchExit(record SessionRecord, opts IngestOptions, warnings *[]string) {
	if record.Status != "ended" || record.FlowID == "" || record.FlowPhaseID == "" || strings.TrimSpace(record.LaunchID) == "" {
		return
	}
	root, explicit := flowStateRoot(opts)
	if root == "" {
		return
	}
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:         root,
		RootExplicit: explicit,
		Role:         flowstore.RoleWriter,
		Presets:      opts.FlowPresets,
	})
	if err != nil {
		appendWarning(warnings, fmt.Sprintf("could not reconcile launch %s for Flow %s phase %s: %v",
			record.LaunchID, record.FlowID, record.FlowPhaseID, err))
		return
	}
	defer func() { _ = store.Close() }()
	controller, err := launchcontrol.New(launchcontrol.Options{Root: root, Store: store})
	if err != nil {
		appendWarning(warnings, fmt.Sprintf("could not reconcile launch %s for Flow %s phase %s: %v",
			record.LaunchID, record.FlowID, record.FlowPhaseID, err))
		return
	}
	outcome, err := controller.Reconcile(record.FlowID, record.FlowPhaseID, strings.TrimSpace(record.LaunchID), launchcontrol.ExitEvidence{
		Source:  launchcontrol.SourceSessionEnd,
		EndedAt: record.EndedAt,
	})
	for _, notice := range outcome.Notices {
		appendWarning(warnings, notice)
	}
	if err != nil {
		appendWarning(warnings, fmt.Sprintf("could not reconcile launch %s for Flow %s phase %s: %v",
			record.LaunchID, record.FlowID, record.FlowPhaseID, err))
		return
	}
	if outcome.Action == launchcontrol.ActionDemoted {
		appendWarning(warnings, fmt.Sprintf("Flow %s phase %s marked %s: launch %s ended without a valid result (%s)",
			record.FlowID, record.FlowPhaseID, outcome.Status, record.LaunchID, outcome.Reason))
	}
}

type hookPayload struct {
	SessionID      string    `json:"session_id"`
	ConversationID string    `json:"conversation_id"`
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
	if provider == ProviderClaude || provider == ProviderCursor {
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
		record.LaunchID = env["APPROACH_LAUNCH_ID"]
	}
	if record.RepoPath == "" {
		record.RepoPath = env["APPROACH_REPO_PATH"]
	}
	if record.WorktreePath == "" {
		record.WorktreePath = env["APPROACH_WORKTREE_PATH"]
	}
	if record.PlanID == "" {
		record.PlanID = env["APPROACH_PLAN_ID"]
	}
	if record.PlanPath == "" {
		record.PlanPath = env["APPROACH_PLAN_PATH"]
	}
	if record.FlowID == "" {
		record.FlowID = env["APPROACH_FLOW_ID"]
	}
	if record.FlowPhaseID == "" {
		record.FlowPhaseID = env["APPROACH_FLOW_PHASE_ID"]
	}
	if record.Branch == "" {
		record.Branch = env["APPROACH_BRANCH"]
	}
	if record.Commit == "" {
		record.Commit = env["APPROACH_COMMIT"]
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

// attachFlowSession takes the warning channel as an out-param rather than a
// return value: it is called for its side effect, and a returned error would
// have to be discarded by a caller that has already succeeded at the thing the
// hook exists to do.
func attachFlowSession(record SessionRecord, opts IngestOptions, warnings *[]string) {
	if record.FlowID == "" || record.FlowPhaseID == "" || strings.TrimSpace(record.SessionID) == "" {
		return
	}
	root, explicit := flowStateRoot(opts)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:         root,
		RootExplicit: explicit,
		Role:         flowstore.RoleWriter,
		Presets:      opts.FlowPresets,
	})
	if err != nil {
		appendWarning(warnings, fmt.Sprintf("could not attach this session to Flow %s phase %s: %v",
			record.FlowID, record.FlowPhaseID, err))
		return
	}
	defer func() { _ = store.Close() }()
	// The attach itself gets the same channel as the open. Opening the store is
	// only the first way this can fail — an absent Flow or phase, a rejected
	// update, or a SQLite error during the write all leave the session
	// unattached, and discarding those is the silent failure this reports.
	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{
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
	}); err != nil {
		appendWarning(warnings, fmt.Sprintf("could not attach this session to Flow %s phase %s: %v",
			record.FlowID, record.FlowPhaseID, err))
	}
}

// flowLaunchStaleResolver returns the resolver and a release func the caller
// must run once the resolver is done. The Flow store holds a pooled SQLite
// handle now, so an unclosed one leaks descriptors for the life of the hook.
func flowLaunchStaleResolver(record SessionRecord, opts IngestOptions, warnings *[]string) (launchStaleFunc, func()) {
	noRelease := func() {}
	if record.FlowID == "" || record.FlowPhaseID == "" {
		return nil, noRelease
	}
	root, explicit := flowStateRoot(opts)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:         root,
		RootExplicit: explicit,
		Role:         flowstore.RoleReader,
		Presets:      opts.FlowPresets,
	})
	if err != nil {
		// The reader role is precisely the one that starts refusing after a
		// release bump, so reporting only attachFlowSession's failure would
		// leave half of this channel's own premise unaddressed.
		appendWarning(warnings, fmt.Sprintf("could not read Flow %s to order this session against"+
			" earlier launches: %v", record.FlowID, err))
		return nil, noRelease
	}
	// The caller also clears launchStale after releasing. That is future-proofing,
	// not a live bug: the sessions.Store is local to IngestHook and upsert runs
	// exactly once. It matters only if a second upsert is ever added, because a
	// resolver whose captured store is closed would degrade SILENTLY — the
	// closure swallows the Read error and reports "not stale".
	// sync.Once, not a plain close: the caller releases inline as soon as the
	// resolver is done and again from its defer, and closing a pooled handle
	// twice would report an error the caller has no way to act on.
	var closeOnce sync.Once
	release := func() { closeOnce.Do(func() { _ = store.Close() }) }
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
	}, release
}

// appendWarning collects an operator notice. A nil channel is a legitimate
// caller — the compatibility wrapper does not want them — so it is not an error.
func appendWarning(warnings *[]string, message string) {
	if warnings == nil {
		return
	}
	*warnings = append(*warnings, message)
}

// flowStateRoot returns the Flow store root and whether it was named
// explicitly. It derives the explicitness of its own two environment reads
// internally, because it consults them ahead of opts.StateRoot and
// opts.StateRootExplicit cannot speak for them.
//
// The trailing APPROACH_SESSION_STATE_ROOT read is deliberately NOT explicit:
// the command has already folded that variable into opts.StateRoot alongside
// the flag and the config fallback, so opts.StateRootExplicit speaks for it,
// and this read is only reached when sessions was constructed by something
// other than that command.
func flowStateRoot(opts IngestOptions) (string, bool) {
	if root := opts.Env["APPROACH_FLOW_STATE_ROOT"]; root != "" {
		return root, true
	}
	if root := opts.Env["APPROACH_PLAN_STATE_ROOT"]; root != "" {
		return root, true
	}
	if opts.StateRoot != "" {
		return opts.StateRoot, opts.StateRootExplicit
	}
	return opts.Env["APPROACH_SESSION_STATE_ROOT"], false
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
