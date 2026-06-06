package sessions

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const schemaVersion = 1
const maxTranscriptLineBytes = 16 * 1024 * 1024

const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"
)

type Store struct {
	root               string
	copyRawTranscripts bool
}

type StoreOptions struct {
	Root               string
	CopyRawTranscripts bool
}

type SessionRecord struct {
	SchemaVersion  int       `json:"schema_version"`
	Provider       Provider  `json:"provider"`
	SessionID      string    `json:"session_id"`
	LaunchID       string    `json:"launch_id,omitempty"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitempty"`
	CWD            string    `json:"cwd,omitempty"`
	RepoPath       string    `json:"repo_path,omitempty"`
	WorktreePath   string    `json:"worktree_path,omitempty"`
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
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("session store root must be absolute: %s", root)
	}
	if err := os.MkdirAll(filepath.Join(root, "sessions"), dirPerm); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}
	if err := os.Chmod(root, dirPerm); err != nil {
		return nil, fmt.Errorf("secure session root: %w", err)
	}
	if err := os.Chmod(filepath.Join(root, "sessions"), dirPerm); err != nil {
		return nil, fmt.Errorf("secure sessions directory: %w", err)
	}
	return &Store{root: root, copyRawTranscripts: opts.CopyRawTranscripts}, nil
}

func DefaultRoot() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "wtui", "sessions", "v1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve session state root: %w", err)
	}
	return filepath.Join(home, ".local", "state", "wtui", "sessions", "v1"), nil
}

func (s *Store) Upsert(record SessionRecord) error {
	if err := validateRecordKey(record.Provider, record.SessionID); err != nil {
		return err
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = schemaVersion
	}
	if err := s.writeMetadata(record); err != nil {
		return err
	}
	if record.TranscriptPath != "" {
		if err := s.writeTranscriptFiles(record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) writeMetadata(record SessionRecord) error {
	dir := s.sessionDir(record.Provider, record.SessionID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("secure session directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session metadata: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
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

func (s *Store) MarkLaunchEnded(launchID string, endedAt time.Time) error {
	if launchID == "" {
		return nil
	}
	records, err := s.List(SessionFilter{})
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.LaunchID != launchID {
			continue
		}
		record.Status = "ended"
		if record.EndedAt.IsZero() {
			record.EndedAt = endedAt
		}
		if record.LastSeenAt.IsZero() || endedAt.After(record.LastSeenAt) {
			record.LastSeenAt = endedAt
		}
		if err := validateRecordKey(record.Provider, record.SessionID); err != nil {
			return err
		}
		if err := s.writeMetadata(record); err != nil {
			return err
		}
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

func validateRecordKey(provider Provider, sessionID string) error {
	if provider == "" || sessionID == "" {
		return fmt.Errorf("session provider and session ID are required")
	}
	if !validProvider(provider) {
		return fmt.Errorf("unsupported session provider %q", provider)
	}
	return nil
}

func validProvider(provider Provider) bool {
	switch provider {
	case ProviderClaude, ProviderCodex:
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

func (s *Store) writeTranscriptFiles(record SessionRecord) error {
	input, err := os.Open(record.TranscriptPath)
	if err != nil {
		return fmt.Errorf("read provider transcript: %w", err)
	}
	defer input.Close()

	dir := s.sessionDir(record.Provider, record.SessionID)
	if s.copyRawTranscripts {
		if err := copyFile(record.TranscriptPath, filepath.Join(dir, "raw.jsonl")); err != nil {
			return err
		}
	}

	if err := writeNormalizedTranscript(filepath.Join(dir, "transcript.jsonl"), input); err != nil {
		return fmt.Errorf("write normalized transcript: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("read raw transcript: %w", err)
	}
	defer input.Close()
	if err := writeFileAtomicFromReader(dst, input); err != nil {
		return fmt.Errorf("write raw transcript: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	return writeFileAtomicFromReader(path, bytes.NewReader(data))
}

func writeFileAtomicFromReader(path string, input io.Reader) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(filePerm); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func writeNormalizedTranscript(path string, input io.Reader) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(filePerm); err != nil {
		_ = temp.Close()
		return err
	}

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLineBytes)
	encoder := json.NewEncoder(temp)
	for scanner.Scan() {
		event, ok, err := parseTranscriptLine(scanner.Bytes())
		if err != nil {
			_ = temp.Close()
			return fmt.Errorf("normalize transcript: %w", err)
		}
		if !ok {
			continue
		}
		if event.Text == "" || !visibleEventKind(event.Kind) || !visibleRole(event.Role) {
			continue
		}
		if err := encoder.Encode(event); err != nil {
			_ = temp.Close()
			return fmt.Errorf("encode normalized transcript: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("normalize transcript: %w", err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
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
