package sessions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const schemaVersion = 1

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
	if err := os.MkdirAll(filepath.Join(root, "sessions"), dirPerm); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
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
	if record.Provider == "" || record.SessionID == "" {
		return fmt.Errorf("session provider and session ID are required")
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = schemaVersion
	}
	dir := s.sessionDir(record.Provider, record.SessionID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session metadata: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), data); err != nil {
		return fmt.Errorf("write session metadata: %w", err)
	}
	if record.TranscriptPath != "" {
		if err := s.writeTranscriptFiles(record); err != nil {
			return err
		}
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
			return fmt.Errorf("parse session metadata %s: %w", path, err)
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
		record.EndedAt = endedAt
		record.LastSeenAt = endedAt
		if err := s.Upsert(record); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) sessionDir(provider Provider, sessionID string) string {
	return filepath.Join(s.root, "sessions", string(provider), sessionID)
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
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind provider transcript: %w", err)
		}
	}

	events, err := normalizeTranscript(input)
	if err != nil {
		return err
	}
	var data []byte
	buffer := bytes.NewBuffer(data)
	encoder := json.NewEncoder(buffer)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("encode normalized transcript: %w", err)
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, "transcript.jsonl"), buffer.Bytes()); err != nil {
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
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("copy raw transcript: %w", err)
	}
	if err := writeFileAtomic(dst, data); err != nil {
		return fmt.Errorf("write raw transcript: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
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

func normalizeTranscript(input io.Reader) ([]TranscriptEvent, error) {
	events, err := readTranscriptEvents(input)
	if err != nil {
		return nil, fmt.Errorf("normalize transcript: %w", err)
	}
	visible := events[:0]
	for _, event := range events {
		if event.Text == "" || !visibleEventKind(event.Kind) || !visibleRole(event.Role) {
			continue
		}
		visible = append(visible, event)
	}
	return visible, nil
}

func readTranscriptEvents(input io.Reader) ([]TranscriptEvent, error) {
	var events []TranscriptEvent
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		var event TranscriptEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
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
