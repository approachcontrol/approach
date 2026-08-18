package launchcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const (
	// launchesCollection is the directory under the state root that holds one
	// directory per launch. It is a sibling of bin/pins, not a child: pins are
	// content-addressed binary retention, launch directories are a time-based
	// mutation log, and the two fail for different reasons.
	launchesCollection = "launches"
	requestsDir        = "requests"

	launchFile   = "launch.json"
	baselineFile = "baseline.json"
	appliedFile  = "applied.json"
	rejectedFile = "rejected.json"
	exitFile     = "exit.json"
	noticeFile   = "FLOW-REPLAY-NOTICE.txt"
	seqLockFile  = ".seq.lock"

	// LogSchemaVersion versions every file in a launch directory.
	LogSchemaVersion = 1

	// LaunchLockTimeout bounds how long a writer waits for another process's
	// hold on a launch's sequence lock.
	LaunchLockTimeout = 5 * time.Second
)

// Written-by markers name which path appended a request.
const (
	WrittenByController = "controller"
	WrittenByDirect     = "direct"
	WrittenBySpool      = "spool"
)

// Applied-result markers.
const (
	ResultApplied    = "applied"
	ResultRefused    = "refused"
	ResultReconciled = "reconciled"
	ResultRejected   = "rejected"
)

// Rejection reasons. They are also the outcome tokens a demotion writes.
const (
	ReasonPhaseResultMissing = "phase_result_missing"
	ReasonPhaseResultStale   = "phase_result_stale"
	ReasonRequestInvalid     = "request_invalid"
	ReasonBaselineMissing    = "baseline_missing"
)

// LaunchInfo is launch.json: the launch's identity and the hash of its
// control token, written at registration for every launch kind.
type LaunchInfo struct {
	SchemaVersion int       `json:"schema_version"`
	LaunchID      string    `json:"launch_id"`
	FlowID        string    `json:"flow_id"`
	PhaseID       string    `json:"phase_id,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	TokenSHA256   string    `json:"token_sha256,omitempty"`
	RegisteredAt  time.Time `json:"registered_at"`
}

// Baseline is baseline.json: the phase status AddPhaseLaunchID returned for
// this launch. ObservedUpdatedAt is diagnostic only; replay never compares it.
type Baseline struct {
	SchemaVersion     int       `json:"schema_version"`
	BaselineStatus    string    `json:"baseline_status"`
	ObservedUpdatedAt time.Time `json:"observed_updated_at,omitempty"`
}

// ObservedPhase is what the writer saw when it appended a request. UpdatedAt
// is diagnostic only.
type ObservedPhase struct {
	Status    string    `json:"status,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// RequestEnvelope is one durable request file. It carries no token.
type RequestEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	Seq           int             `json:"seq"`
	RequestID     string          `json:"request_id"`
	LaunchID      string          `json:"launch_id"`
	FlowID        string          `json:"flow_id"`
	PhaseID       string          `json:"phase_id,omitempty"`
	Verb          Verb            `json:"verb"`
	Replayable    bool            `json:"replayable"`
	Unowned       bool            `json:"unowned,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	Observed      ObservedPhase   `json:"observed,omitzero"`
	WrittenAt     time.Time       `json:"written_at"`
	WrittenBy     string          `json:"written_by"`
}

// AppliedState is applied.json: the high-water mark of the log and the phase
// status after the last applied request. Status is the comparison state a
// later replay starts from.
type AppliedState struct {
	SchemaVersion     int       `json:"schema_version"`
	AppliedSeq        int       `json:"applied_seq"`
	Status            string    `json:"status,omitempty"`
	Result            string    `json:"result"`
	ObservedUpdatedAt time.Time `json:"observed_updated_at,omitempty"`
	AppliedAt         time.Time `json:"applied_at"`
}

// RejectedBatch is one replay rejection: the requests, why, and what was
// intended against what was observed.
type RejectedBatch struct {
	RejectedAt     time.Time         `json:"rejected_at"`
	Reason         string            `json:"reason"`
	IntendedStatus string            `json:"intended_status,omitempty"`
	ObservedStatus string            `json:"observed_status,omitempty"`
	Error          string            `json:"error,omitempty"`
	Requests       []RequestEnvelope `json:"requests"`
}

// RejectedLog is rejected.json.
type RejectedLog struct {
	SchemaVersion int             `json:"schema_version"`
	Batches       []RejectedBatch `json:"batches"`
}

// ExitRecord is exit.json: authoritative exit evidence, written by the lease
// runner after the agent's whole process group is gone and by Reconcile for
// an embedded or interactive terminal's exit before it does anything that
// can fail transiently. CodeUnknown marks a record whose writer saw the exit
// but not its status; ExitCode is then meaningless rather than 0.
type ExitRecord struct {
	SchemaVersion int       `json:"schema_version"`
	LaunchID      string    `json:"launch_id"`
	FlowID        string    `json:"flow_id"`
	PhaseID       string    `json:"phase_id,omitempty"`
	ExitCode      int       `json:"exit_code"`
	CodeUnknown   bool      `json:"code_unknown,omitempty"`
	Signaled      bool      `json:"signaled,omitempty"`
	EndedAt       time.Time `json:"ended_at"`
	Source        string    `json:"source"`
}

// Log is one launch's directory. It is a value: nothing is held open between
// calls, and every mutation is a durable file operation under Lock.
type Log struct {
	root     string
	launchID string
	dir      string
}

// LaunchesDir is <root>/launches.
func LaunchesDir(root string) string {
	return filepath.Join(root, launchesCollection)
}

// OpenLog addresses the launch directory for launchID under root without
// creating it. Unsafe IDs are refused before any path is built.
func OpenLog(root, launchID string) (*Log, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("launch log requires an absolute state root")
	}
	if !artifacts.IsSafeID(launchID) {
		return nil, fmt.Errorf("launch log refuses unsafe launch id %q", launchID)
	}
	return &Log{root: filepath.Clean(root), launchID: launchID, dir: filepath.Join(LaunchesDir(root), launchID)}, nil
}

// Dir is the launch directory path.
func (l *Log) Dir() string { return l.dir }

// LaunchID is the launch this log belongs to.
func (l *Log) LaunchID() string { return l.launchID }

// Exists reports whether the launch directory is present.
func (l *Log) Exists() bool {
	info, err := os.Lstat(l.dir)
	return err == nil && info.IsDir()
}

// ensureDir creates the launch directory (and its parent) owner-only and
// refuses a symlink in its place: the directory is written to by root-owned
// processes and must not be redirectable.
func (l *Log) ensureDir() error {
	for _, dir := range []string{LaunchesDir(l.root), l.dir, filepath.Join(l.dir, requestsDir)} {
		if err := os.MkdirAll(dir, artifacts.DirPerm); err != nil {
			return fmt.Errorf("create launch directory: %w", err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect launch directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("launch directory %s must be a real directory", dir)
		}
		if info.Mode().Perm() != artifacts.DirPerm {
			if err := os.Chmod(dir, artifacts.DirPerm); err != nil {
				return fmt.Errorf("secure launch directory: %w", err)
			}
		}
	}
	return nil
}

// Lock takes the launch's cross-process sequence lock. Every appender and
// every replayer holds it, so sequence numbers are allocated once and a
// replay never interleaves with a live write to the same launch.
func (l *Log) Lock(timeout time.Duration) (func(), error) {
	if err := l.ensureDir(); err != nil {
		return nil, err
	}
	return artifacts.AcquireFileLockNoFollow(filepath.Join(l.dir, seqLockFile), "launch log "+l.launchID, timeout)
}

var requestFilePattern = regexp.MustCompile(`^(\d{6})-([A-Za-z0-9._-]+)\.json$`)

func (l *Log) requestFiles() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(l.dir, requestsDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list launch requests: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() && requestFilePattern.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (l *Log) lastSeq() (int, error) {
	names, err := l.requestFiles()
	if err != nil {
		return 0, err
	}
	last := 0
	for _, name := range names {
		seq, err := strconv.Atoi(requestFilePattern.FindStringSubmatch(name)[1])
		if err == nil && seq > last {
			last = seq
		}
	}
	return last, nil
}

// Append allocates the next sequence number and writes env durably. The
// caller holds Lock. Once Append returns, the request survives a crash of
// this process and of the machine; ackHook runs after the write so tests can
// simulate a crash between "durable" and "acknowledged".
func (l *Log) Append(env RequestEnvelope) (int, error) {
	if !artifacts.IsSafeID(env.RequestID) {
		return 0, fmt.Errorf("launch log refuses unsafe request id %q", env.RequestID)
	}
	if err := l.ensureDir(); err != nil {
		return 0, err
	}
	last, err := l.lastSeq()
	if err != nil {
		return 0, err
	}
	env.Seq = last + 1
	env.SchemaVersion = LogSchemaVersion
	env.LaunchID = l.launchID
	if env.WrittenAt.IsZero() {
		env.WrittenAt = time.Now().UTC()
	}
	name := fmt.Sprintf("%06d-%s.json", env.Seq, env.RequestID)
	if err := writeJSONDurably(filepath.Join(l.dir, requestsDir), name, env); err != nil {
		return 0, err
	}
	if err := ackHook(); err != nil {
		return env.Seq, err
	}
	return env.Seq, nil
}

// Requests returns every request in sequence order.
func (l *Log) Requests() ([]RequestEnvelope, error) {
	names, err := l.requestFiles()
	if err != nil {
		return nil, err
	}
	envelopes := make([]RequestEnvelope, 0, len(names))
	for _, name := range names {
		var env RequestEnvelope
		if err := readJSON(filepath.Join(l.dir, requestsDir, name), &env); err != nil {
			return nil, err
		}
		if err := checkLogSchema(name, env.SchemaVersion); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, env)
	}
	slices.SortFunc(envelopes, func(a, b RequestEnvelope) int { return a.Seq - b.Seq })
	return envelopes, nil
}

// Pending returns the requests with a sequence number above applied_seq.
func (l *Log) Pending() ([]RequestEnvelope, error) {
	all, err := l.Requests()
	if err != nil {
		return nil, err
	}
	applied, ok, err := l.Applied()
	if err != nil {
		return nil, err
	}
	high := 0
	if ok {
		high = applied.AppliedSeq
	}
	pending := make([]RequestEnvelope, 0)
	for _, env := range all {
		if env.Seq > high {
			pending = append(pending, env)
		}
	}
	return pending, nil
}

// Applied reads applied.json.
func (l *Log) Applied() (AppliedState, bool, error) {
	var state AppliedState
	ok, err := readOptionalJSON(filepath.Join(l.dir, appliedFile), &state)
	if err == nil && ok {
		err = checkLogSchema(appliedFile, state.SchemaVersion)
	}
	return state, ok, err
}

// WriteApplied replaces applied.json durably.
func (l *Log) WriteApplied(state AppliedState) error {
	if err := l.ensureDir(); err != nil {
		return err
	}
	state.SchemaVersion = LogSchemaVersion
	if state.AppliedAt.IsZero() {
		state.AppliedAt = time.Now().UTC()
	}
	return writeJSONDurably(l.dir, appliedFile, state)
}

// Rejected reads rejected.json.
func (l *Log) Rejected() (RejectedLog, bool, error) {
	var log RejectedLog
	ok, err := readOptionalJSON(filepath.Join(l.dir, rejectedFile), &log)
	if err == nil && ok {
		err = checkLogSchema(rejectedFile, log.SchemaVersion)
	}
	return log, ok, err
}

// AppendRejected adds a batch to rejected.json.
func (l *Log) AppendRejected(batch RejectedBatch) error {
	if err := l.ensureDir(); err != nil {
		return err
	}
	current, _, err := l.Rejected()
	if err != nil {
		return err
	}
	if batch.RejectedAt.IsZero() {
		batch.RejectedAt = time.Now().UTC()
	}
	current.SchemaVersion = LogSchemaVersion
	current.Batches = append(current.Batches, batch)
	return writeJSONDurably(l.dir, rejectedFile, current)
}

// Baseline reads baseline.json.
func (l *Log) Baseline() (Baseline, bool, error) {
	var baseline Baseline
	ok, err := readOptionalJSON(filepath.Join(l.dir, baselineFile), &baseline)
	if err == nil && ok {
		err = checkLogSchema(baselineFile, baseline.SchemaVersion)
	}
	return baseline, ok, err
}

// WriteBaseline replaces baseline.json durably.
func (l *Log) WriteBaseline(baseline Baseline) error {
	if err := l.ensureDir(); err != nil {
		return err
	}
	baseline.SchemaVersion = LogSchemaVersion
	return writeJSONDurably(l.dir, baselineFile, baseline)
}

// Launch reads launch.json.
func (l *Log) Launch() (LaunchInfo, bool, error) {
	var info LaunchInfo
	ok, err := readOptionalJSON(filepath.Join(l.dir, launchFile), &info)
	if err == nil && ok {
		err = checkLogSchema(launchFile, info.SchemaVersion)
	}
	return info, ok, err
}

// WriteLaunch replaces launch.json durably.
func (l *Log) WriteLaunch(info LaunchInfo) error {
	if err := l.ensureDir(); err != nil {
		return err
	}
	info.SchemaVersion = LogSchemaVersion
	info.LaunchID = l.launchID
	if info.RegisteredAt.IsZero() {
		info.RegisteredAt = time.Now().UTC()
	}
	return writeJSONDurably(l.dir, launchFile, info)
}

// Exit reads exit.json.
func (l *Log) Exit() (ExitRecord, bool, error) {
	var record ExitRecord
	ok, err := readOptionalJSON(filepath.Join(l.dir, exitFile), &record)
	if err == nil && ok {
		err = checkLogSchema(exitFile, record.SchemaVersion)
	}
	return record, ok, err
}

// WriteExit replaces exit.json durably.
func (l *Log) WriteExit(record ExitRecord) error {
	if err := l.ensureDir(); err != nil {
		return err
	}
	record.SchemaVersion = LogSchemaVersion
	record.LaunchID = l.launchID
	return writeJSONDurably(l.dir, exitFile, record)
}

// WriteNotice writes the advisory replay notice. Best effort: a notice that
// cannot be written must not fail the operation it describes.
func (l *Log) WriteNotice(lines []string) error {
	if err := l.ensureDir(); err != nil {
		return err
	}
	body := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(l.dir, noticeFile)
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(artifacts.FilePerm))
	if err != nil {
		return fmt.Errorf("write replay notice: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if _, err := file.WriteString(body); err != nil {
		return fmt.Errorf("write replay notice: %w", err)
	}
	return nil
}

// Notice reads the advisory notice, if any.
func (l *Log) Notice() (string, bool) {
	data, err := os.ReadFile(filepath.Join(l.dir, noticeFile))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// AgeForRetention is the newest modification time over the files that record
// state — never the sequence lock, whose owner line is rewritten on every
// acquisition, so a replay pass that touched nothing must not renew a launch's
// retention.
func (l *Log) AgeForRetention() (time.Time, error) {
	var newest time.Time
	consider := func(path string) error {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	}
	for _, name := range []string{launchFile, baselineFile, appliedFile, rejectedFile, exitFile} {
		if err := consider(filepath.Join(l.dir, name)); err != nil {
			return time.Time{}, err
		}
	}
	names, err := l.requestFiles()
	if err != nil {
		return time.Time{}, err
	}
	for _, name := range names {
		if err := consider(filepath.Join(l.dir, requestsDir, name)); err != nil {
			return time.Time{}, err
		}
	}
	if newest.IsZero() {
		info, err := os.Lstat(l.dir)
		if err != nil {
			return time.Time{}, err
		}
		newest = info.ModTime()
	}
	return newest, nil
}

// Remove deletes the launch directory. Callers check Pending and the lock
// before calling it.
func (l *Log) Remove() error {
	return os.RemoveAll(l.dir)
}

// ListLaunchIDs returns every launch directory name under root that is a
// safe ID, sorted.
func ListLaunchIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(LaunchesDir(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list launch directories: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && artifacts.IsSafeID(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// writeJSONDurably is the sidecar pattern: temp → write → chmod → fsync →
// close → rename → fsync(dir). Not shared with flowstore on purpose; the two
// packages stay decoupled and the pattern is small.
func writeJSONDurably(dir, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	temp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(artifacts.FilePerm); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

// checkLogSchema refuses a launch-directory file written under a schema this
// build does not know. A newer build may have given a known field new
// semantics or added one this build would ignore, so the safe reading is no
// reading: the caller fails, the launch stays pending, and a controller that
// understands the file replays it. Older versions (and the pre-versioned
// zero) read fine.
func checkLogSchema(name string, version int) error {
	if version > LogSchemaVersion {
		return fmt.Errorf("launch log %s uses schema %d, written by a newer build than this one (schema %d)", name, version, LogSchemaVersion)
	}
	return nil
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func readOptionalJSON(path string, into any) (bool, error) {
	err := readJSON(path, into)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
