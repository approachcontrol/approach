// Package sessions stores agent-session metadata and normalized transcripts
// under the user state directory.
//
// Transcripts may contain secrets: they must stay under user state with
// restrictive permissions (0700 dirs, 0600 files) and must never be written
// inside a repository.
package sessions

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const schemaVersion = 1
const maxTranscriptLineBytes = 16 * 1024 * 1024
const defaultLockTimeout = 5 * time.Second

type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
	ProviderCursor Provider = "cursor-agent"
)

type Store struct {
	root               string
	copyRawTranscripts bool
	lockTimeout        time.Duration
	env                map[string]string
	launchStale        launchStaleFunc
	acquireFileLock    acquireFileLockFunc
}

type launchStaleFunc func(existing, incoming SessionRecord) (stale, known bool)
type acquireFileLockFunc func(path, label string, timeout time.Duration) (func(), error)

type StoreOptions struct {
	Root               string
	CopyRawTranscripts bool
	LockTimeout        time.Duration
	Env                map[string]string
}

type SessionRecord struct {
	SchemaVersion int      `json:"schema_version"`
	Provider      Provider `json:"provider"`
	SessionID     string   `json:"session_id"`
	LaunchID      string   `json:"launch_id,omitempty"`
	Status        string   `json:"status"`
	// EndReason is the provider's reason for an ended record when the hook
	// supplied one (Claude's SessionEnd `reason`: clear, logout,
	// prompt_input_exit, other). A `clear` end leaves the agent process alive
	// on a new session, so it is not process-exit evidence.
	EndReason      string    `json:"end_reason,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitempty"`
	CWD            string    `json:"cwd,omitempty"`
	RepoPath       string    `json:"repo_path,omitempty"`
	WorktreePath   string    `json:"worktree_path,omitempty"`
	PlanID         string    `json:"plan_id,omitempty"`
	PlanPath       string    `json:"plan_path,omitempty"`
	FlowID         string    `json:"flow_id,omitempty"`
	FlowPhaseID    string    `json:"flow_phase_id,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	Commit         string    `json:"commit,omitempty"`
	Model          string    `json:"model,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	TranscriptPath string    `json:"transcript_path,omitempty"`
	CaptureSource  string    `json:"capture_source,omitempty"`
}

type SessionFilter struct {
	RepoPath     string
	WorktreePath string
	Branch       string
	Provider     Provider
}

type TranscriptEvent struct {
	Timestamp time.Time         `json:"timestamp"`
	Role      string            `json:"role"`
	Kind      string            `json:"kind"`
	Text      string            `json:"text"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func NewStore(opts StoreOptions) (*Store, error) {
	root := opts.Root
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	if _, err := artifacts.RequireAbsoluteRoot(root, "session"); err != nil {
		return nil, err
	}
	if err := artifacts.EnsureCollection(root, "sessions"); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}
	lockTimeout := opts.LockTimeout
	if lockTimeout <= 0 {
		lockTimeout = defaultLockTimeout
	}
	return &Store{
		root:               root,
		copyRawTranscripts: opts.CopyRawTranscripts,
		lockTimeout:        lockTimeout,
		env:                opts.Env,
		acquireFileLock:    artifacts.AcquireFileLock,
	}, nil
}

func DefaultRoot() (string, error) {
	root, err := artifacts.DefaultRoot()
	if err != nil {
		return "", fmt.Errorf("resolve session state root: %w", err)
	}
	return root, nil
}

func (s *Store) Upsert(record SessionRecord) error {
	_, err := s.upsert(record)
	return err
}

func (s *Store) upsert(record SessionRecord) (SessionRecord, error) {
	if err := validateRecordKey(record.Provider, record.SessionID); err != nil {
		return SessionRecord{}, err
	}
	var transcript *os.File
	if record.TranscriptPath != "" {
		var canonical string
		var err error
		transcript, canonical, err = openValidatedTranscript(record.Provider, record.TranscriptPath, s.env)
		if err != nil {
			return SessionRecord{}, err
		}
		defer transcript.Close()
		record.TranscriptPath = canonical
	}
	release, err := s.acquireSessionLock(record.Provider, record.SessionID)
	if err != nil {
		return SessionRecord{}, err
	}
	defer release()
	existing, ok, err := s.readMetadata(record.Provider, record.SessionID)
	if err != nil {
		return SessionRecord{}, err
	}
	hasIncomingTranscript := record.TranscriptPath != ""
	if ok {
		if s.isStaleLaunchUpdate(existing, record) {
			return existing, nil
		}
		record = mergeSessionRecord(existing, record)
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = schemaVersion
	}
	if err := s.writeMetadata(record); err != nil {
		return SessionRecord{}, err
	}
	if hasIncomingTranscript && record.Provider != ProviderCursor {
		if err := s.writeTranscriptFiles(record, transcript); err != nil {
			return SessionRecord{}, err
		}
	}
	return record, nil
}

func (s *Store) writeMetadata(record SessionRecord) error {
	if err := artifacts.EnsureCollection(filepath.Join(s.root, "sessions"), providerPathPart(record.Provider)); err != nil {
		return fmt.Errorf("create session provider directory: %w", err)
	}
	dir := s.sessionDir(record.Provider, record.SessionID)
	if _, err := artifacts.EnsureRecordDir(filepath.Join(s.root, "sessions", providerPathPart(record.Provider)), "", safeSessionDirName(record.SessionID)); err != nil {
		return fmt.Errorf("secure session directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session metadata: %w", err)
	}
	if err := artifacts.WriteFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
		return fmt.Errorf("write session metadata: %w", err)
	}
	return nil
}

func (s *Store) List(filter SessionFilter) ([]SessionRecord, error) {
	var records []SessionRecord
	root := filepath.Join(s.root, "sessions")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record SessionRecord
		if err := json.Unmarshal(data, &record); err != nil {
			// Corrupt metadata should not make every other session unavailable.
			return nil
		}
		if matchesFilter(record, filter) {
			records = append(records, record)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return sortTime(records[i]).After(sortTime(records[j]))
	})
	return records, nil
}

// Read returns the record stored under the exact provider and raw session ID.
// The decoded key is verified as well as the hashed path so corrupt or
// misplaced metadata cannot masquerade as the requested session.
func (s *Store) Read(provider Provider, sessionID string) (SessionRecord, error) {
	if err := validateRecordKey(provider, sessionID); err != nil {
		return SessionRecord{}, err
	}
	record, ok, err := s.readMetadata(provider, sessionID)
	if err != nil {
		return SessionRecord{}, err
	}
	if !ok {
		return SessionRecord{}, fmt.Errorf("session %q/%q not found", provider, sessionID)
	}
	if record.Provider != provider || record.SessionID != sessionID {
		return SessionRecord{}, fmt.Errorf(
			"session metadata key %q/%q does not match requested key %q/%q",
			record.Provider, record.SessionID, provider, sessionID,
		)
	}
	return record, nil
}

// IsActive applies the shared current-status and legacy EndedAt liveness rule.
// An explicit status is authoritative; records without one are active only
// until the legacy end timestamp is present.
func IsActive(status string, endedAt time.Time) bool {
	if status = strings.TrimSpace(status); status != "" {
		return status != "ended"
	}
	return endedAt.IsZero()
}

func (s *Store) ReadTranscript(provider Provider, sessionID string) ([]TranscriptEvent, error) {
	if err := validateRecordKey(provider, sessionID); err != nil {
		return nil, err
	}
	path := filepath.Join(s.sessionDir(provider, sessionID), "transcript.jsonl")
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	defer file.Close()
	return readTranscriptEvents(file)
}

// MarkLaunchEnded matches launch IDs on their trimmed form, on both sides of
// the comparison, and flowstore.MarkPhaseLaunchEnded now does the same. The two
// run as one operation — FinalizeAgentSession calls both to end a launch in the
// store and in the Flow phase mirror — so a padded ID that matched one half and
// not the other would leave a launch recorded as ended in one place and still
// live in the other, which is the exact shape of the stall session release
// exists to clear. Ingestion takes APPROACH_LAUNCH_ID from the environment
// verbatim into both, so the padding is the caller's to produce, not either
// store's to assume away.
func (s *Store) MarkLaunchEnded(launchID string, endedAt time.Time) error {
	launchID = strings.TrimSpace(launchID)
	if launchID == "" {
		return nil
	}
	records, err := s.List(SessionFilter{})
	if err != nil {
		return err
	}
	for _, record := range records {
		if strings.TrimSpace(record.LaunchID) != launchID {
			continue
		}
		if err := validateRecordKey(record.Provider, record.SessionID); err != nil {
			// Legacy records with unusable keys cannot be rewritten in place;
			// skip them rather than abort updates for the remaining records.
			continue
		}
		release, err := s.acquireSessionLock(record.Provider, record.SessionID)
		if err != nil {
			return err
		}
		current, ok, err := s.readMetadata(record.Provider, record.SessionID)
		if err != nil {
			release()
			return err
		}
		if !ok || strings.TrimSpace(current.LaunchID) != launchID {
			release()
			continue
		}
		wasEnded := current.Status == "ended"
		current.Status = "ended"
		if current.EndedAt.IsZero() || !wasEnded {
			current.EndedAt = latestTime(current.EndedAt, endedAt)
		}
		if current.LastSeenAt.IsZero() || endedAt.After(current.LastSeenAt) {
			current.LastSeenAt = endedAt
		}
		if err := s.writeMetadata(current); err != nil {
			release()
			return err
		}
		release()
	}
	return nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) sessionDir(provider Provider, sessionID string) string {
	return filepath.Join(s.root, "sessions", providerPathPart(provider), safeSessionDirName(sessionID))
}

func safeSessionDirName(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}

func (s *Store) acquireSessionLock(provider Provider, sessionID string) (func(), error) {
	lockDir := filepath.Join(s.root, "sessions", ".locks")
	if err := os.MkdirAll(lockDir, artifacts.DirPerm); err != nil {
		return nil, fmt.Errorf("create session lock directory: %w", err)
	}
	if err := os.Chmod(lockDir, artifacts.DirPerm); err != nil {
		return nil, fmt.Errorf("secure session lock directory: %w", err)
	}
	lockName := providerPathPart(provider) + "-" + safeSessionDirName(sessionID) + ".lock"
	label := fmt.Sprintf("session lock %q/%q", provider, sessionID)
	acquire := s.acquireFileLock
	if acquire == nil {
		acquire = artifacts.AcquireFileLock
	}
	return acquire(filepath.Join(lockDir, lockName), label, s.lockTimeout)
}

func (s *Store) readMetadata(provider Provider, sessionID string) (SessionRecord, bool, error) {
	data, err := os.ReadFile(filepath.Join(s.sessionDir(provider, sessionID), "meta.json"))
	if os.IsNotExist(err) {
		return SessionRecord{}, false, nil
	}
	if err != nil {
		return SessionRecord{}, false, fmt.Errorf("read session metadata: %w", err)
	}
	var record SessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return SessionRecord{}, false, fmt.Errorf("decode session metadata: %w", err)
	}
	return record, true, nil
}

func mergeSessionRecord(existing, incoming SessionRecord) SessionRecord {
	incoming.Provider = existing.Provider
	incoming.SessionID = existing.SessionID
	newerLaunch := incoming.LaunchID != "" && incoming.LaunchID != existing.LaunchID
	if incoming.SchemaVersion == 0 {
		incoming.SchemaVersion = existing.SchemaVersion
	}
	preserveBlank := func(value *string, old string) {
		if *value == "" {
			*value = old
		}
	}
	preserveBlank(&incoming.LaunchID, existing.LaunchID)
	preserveBlank(&incoming.Status, existing.Status)
	preserveBlank(&incoming.EndReason, existing.EndReason)
	preserveBlank(&incoming.CWD, existing.CWD)
	preserveBlank(&incoming.RepoPath, existing.RepoPath)
	preserveBlank(&incoming.WorktreePath, existing.WorktreePath)
	preserveBlank(&incoming.PlanID, existing.PlanID)
	preserveBlank(&incoming.PlanPath, existing.PlanPath)
	if !newerLaunch {
		preserveBlank(&incoming.FlowID, existing.FlowID)
		preserveBlank(&incoming.FlowPhaseID, existing.FlowPhaseID)
	}
	preserveBlank(&incoming.Branch, existing.Branch)
	preserveBlank(&incoming.Commit, existing.Commit)
	preserveBlank(&incoming.Model, existing.Model)
	preserveBlank(&incoming.Summary, existing.Summary)
	preserveBlank(&incoming.TranscriptPath, existing.TranscriptPath)
	preserveBlank(&incoming.CaptureSource, existing.CaptureSource)
	if existing.Status == "ended" && !newerLaunch {
		incoming.Status = "ended"
	}
	incoming.StartedAt = earliestNonzero(existing.StartedAt, incoming.StartedAt)
	incoming.LastSeenAt = latestTime(existing.LastSeenAt, incoming.LastSeenAt)
	incoming.EndedAt = latestTime(existing.EndedAt, incoming.EndedAt)
	return incoming
}

func (s *Store) isStaleLaunchUpdate(existing, incoming SessionRecord) bool {
	if incoming.LaunchID == "" || incoming.LaunchID == existing.LaunchID {
		return false
	}
	if s.launchStale != nil {
		if stale, known := s.launchStale(existing, incoming); known {
			return stale
		}
	}
	existingOrder, existingOrdered := approachLaunchOrder(existing.LaunchID)
	incomingOrder, incomingOrdered := approachLaunchOrder(incoming.LaunchID)
	if existingOrdered && incomingOrdered && existingOrder != incomingOrder {
		return incomingOrder < existingOrder
	}
	return !sortTime(incoming).After(sortTime(existing))
}

func approachLaunchOrder(launchID string) (int64, bool) {
	value, ok := strings.CutPrefix(launchID, "approach-")
	if !ok {
		return 0, false
	}
	if timestamp, _, found := strings.Cut(value, "-"); found {
		value = timestamp
	}
	order, err := strconv.ParseInt(value, 10, 64)
	return order, err == nil
}

func earliestNonzero(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

func latestTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func validateRecordKey(provider Provider, sessionID string) error {
	if provider == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session provider and session ID are required")
	}
	if !validProvider(provider) {
		return fmt.Errorf("unsupported session provider %q", provider)
	}
	return nil
}

func validProvider(provider Provider) bool {
	switch provider {
	case ProviderClaude, ProviderCodex, ProviderCursor:
		return true
	default:
		return false
	}
}

func providerPathPart(provider Provider) string {
	switch provider {
	case ProviderClaude:
		return string(ProviderClaude)
	case ProviderCodex:
		return string(ProviderCodex)
	case ProviderCursor:
		return string(ProviderCursor)
	default:
		return "_invalid"
	}
}

func matchesFilter(record SessionRecord, filter SessionFilter) bool {
	if filter.RepoPath != "" && record.RepoPath != filter.RepoPath {
		return false
	}
	if filter.WorktreePath != "" && record.WorktreePath != filter.WorktreePath {
		return false
	}
	if filter.Branch != "" && record.Branch != filter.Branch {
		return false
	}
	if filter.Provider != "" && record.Provider != filter.Provider {
		return false
	}
	return true
}

func sortTime(record SessionRecord) time.Time {
	if !record.LastSeenAt.IsZero() {
		return record.LastSeenAt
	}
	if !record.EndedAt.IsZero() {
		return record.EndedAt
	}
	return record.StartedAt
}

func (s *Store) writeTranscriptFiles(record SessionRecord, input *os.File) error {
	dir := s.sessionDir(record.Provider, record.SessionID)
	if s.copyRawTranscripts {
		if _, err := input.Seek(0, 0); err != nil {
			return fmt.Errorf("rewind provider transcript for raw copy: %w", err)
		}
		if err := artifacts.WriteFileAtomicFromReader(filepath.Join(dir, "raw.jsonl"), input); err != nil {
			return fmt.Errorf("write raw transcript: %w", err)
		}
	}
	if _, err := input.Seek(0, 0); err != nil {
		return fmt.Errorf("rewind provider transcript for normalization: %w", err)
	}

	if err := writeNormalizedTranscript(filepath.Join(dir, "transcript.jsonl"), input); err != nil {
		return fmt.Errorf("write normalized transcript: %w", err)
	}
	return nil
}

func writeNormalizedTranscript(path string, input io.Reader) error {
	return artifacts.WriteFileAtomicFunc(path, func(output io.Writer) error {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64*1024), maxTranscriptLineBytes)
		encoder := json.NewEncoder(output)
		for scanner.Scan() {
			event, ok, err := parseTranscriptLine(scanner.Bytes())
			if err != nil {
				return fmt.Errorf("normalize transcript: %w", err)
			}
			if !ok {
				continue
			}
			if event.Text == "" || !visibleEventKind(event.Kind) || !visibleRole(event.Role) {
				continue
			}
			if err := encoder.Encode(event); err != nil {
				return fmt.Errorf("encode normalized transcript: %w", err)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("normalize transcript: %w", err)
		}
		return nil
	})
}

func readTranscriptEvents(input io.Reader) ([]TranscriptEvent, error) {
	var events []TranscriptEvent
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLineBytes)
	for scanner.Scan() {
		event, ok, err := parseTranscriptLine(scanner.Bytes())
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func parseTranscriptLine(line []byte) (TranscriptEvent, bool, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return TranscriptEvent{}, false, err
	}
	role := firstString(raw, "role")
	kind := firstString(raw, "kind", "type", "hook_event_name")
	text := firstString(raw, "text", "content", "last_assistant_message", "summary")
	timestamp := parseTranscriptTimestamp(firstString(raw, "timestamp"))
	if message, ok := raw["message"].(map[string]any); ok {
		if role == "" {
			role = firstString(message, "role")
		}
		if text == "" {
			text = textFromContent(message["content"])
		}
	}
	if text == "" {
		text = textFromContent(raw["content"])
	}
	if role == "" {
		switch kind {
		case "assistant", "assistant_message":
			role = "assistant"
		case "user", "user_message":
			role = "user"
		case "system":
			role = "system"
		}
	}
	switch kind {
	case "assistant", "assistant_message", "user", "user_message", "system":
		kind = "message"
	}
	if kind == "" {
		kind = "message"
	}
	if text == "" || role == "" {
		return TranscriptEvent{}, false, nil
	}
	return TranscriptEvent{Timestamp: timestamp, Role: role, Kind: kind, Text: text}, true, nil
}

func parseTranscriptTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return timestamp
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func textFromContent(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		parts := make([]string, 0, len(content))
		for _, item := range content {
			switch item := item.(type) {
			case string:
				if item != "" {
					parts = append(parts, item)
				}
			case map[string]any:
				if text := firstString(item, "text", "content"); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func visibleEventKind(kind string) bool {
	switch kind {
	case "message", "tool_call", "tool_result", "status":
		return true
	default:
		return false
	}
}

func visibleRole(role string) bool {
	switch role {
	case "user", "assistant", "tool", "system":
		return true
	default:
		return false
	}
}
