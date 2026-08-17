package flowlease

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const (
	handoffCollection      = "flow-launch-handoffs"
	protocolVersion        = 1
	recordRunnerClaim      = "runner-claim"
	recordReady            = "ready"
	recordDecision         = "decision"
	recordStarted          = "started"
	recordStartedConfirmed = "started-confirmed"
	recordFailure          = "failure"
	decisionCommit         = "commit"
	decisionAbort          = "abort"
	maxRecordBytes         = 4096
	maxFailureDetail       = 2048
)

var errRecordExists = errors.New("handoff record already exists")

type handoffAttempt struct {
	Root       string
	FlowID     string
	PhaseID    string
	LaunchID   string
	Nonce      string
	HandoffDir string
}

type handoffRecord struct {
	Version  int    `json:"version"`
	Kind     string `json:"kind"`
	FlowID   string `json:"flow_id"`
	PhaseID  string `json:"phase_id"`
	LaunchID string `json:"launch_id"`
	Nonce    string `json:"nonce"`
	Outcome  string `json:"outcome,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func createHandoff(attempt handoffAttempt) error {
	canonical, err := validateHandoffAttempt(attempt)
	if err != nil {
		return err
	}
	collection, err := ensurePrivateCollection(canonical, handoffCollection)
	if err != nil {
		return err
	}
	collectionLock, err := lockDirectory(collection, handoffCollection)
	if err != nil {
		return err
	}
	defer collectionLock.Close()
	if err := os.Mkdir(attempt.HandoffDir, artifacts.DirPerm); err != nil {
		return fmt.Errorf("create exclusive Flow launch handoff: %w", err)
	}
	if err := normalizeCreatedDirectory(attempt.HandoffDir, "Flow launch handoff"); err != nil {
		return err
	}
	info, err := os.Lstat(attempt.HandoffDir)
	if err != nil {
		return fmt.Errorf("inspect Flow launch handoff: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Flow launch handoff must be a directory")
	}
	return requireOwnedPrivate(info, artifacts.DirPerm, "Flow launch handoff")
}

func validateExistingHandoff(attempt handoffAttempt) error {
	canonical, err := validateHandoffAttempt(attempt)
	if err != nil {
		return err
	}
	collection := filepath.Join(canonical, handoffCollection)
	collectionInfo, err := os.Lstat(collection)
	if err != nil {
		return fmt.Errorf("inspect Flow launch handoff collection: %w", err)
	}
	if collectionInfo.Mode()&os.ModeSymlink != 0 || !collectionInfo.IsDir() {
		return errors.New("Flow launch handoff collection must be a directory, not a symlink or other node")
	}
	if err := requireOwnedPrivate(collectionInfo, artifacts.DirPerm, "Flow launch handoff collection"); err != nil {
		return err
	}
	info, err := os.Lstat(attempt.HandoffDir)
	if err != nil {
		return fmt.Errorf("inspect Flow launch handoff: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Flow launch handoff must be a directory")
	}
	return requireOwnedPrivate(info, artifacts.DirPerm, "Flow launch handoff")
}

func validateHandoffAttempt(attempt handoffAttempt) (string, error) {
	canonical, err := ResolveRoot(attempt.Root)
	if err != nil {
		return "", err
	}
	if canonical != filepath.Clean(attempt.Root) {
		return "", errors.New("Flow launch handoff root must already be canonical")
	}
	for label, value := range map[string]string{
		"Flow ID": attempt.FlowID, "phase ID": attempt.PhaseID,
		"launch ID": attempt.LaunchID, "nonce": attempt.Nonce,
	} {
		if !artifacts.IsSafeID(value) {
			return "", fmt.Errorf("invalid Flow launch handoff %s %q", label, value)
		}
	}
	base := filepath.Base(attempt.HandoffDir)
	if !artifacts.IsSafeID(base) || !strings.HasPrefix(base, attempt.LaunchID+"-") {
		return "", errors.New("Flow launch handoff directory must use the launch ID and a safe random suffix")
	}
	wantParent := filepath.Join(canonical, handoffCollection)
	if filepath.Dir(filepath.Clean(attempt.HandoffDir)) != wantParent {
		return "", errors.New("Flow launch handoff directory escapes its collection")
	}
	return canonical, nil
}

func publishHandoffRecord(attempt handoffAttempt, kind, outcome string) error {
	return publishHandoffRecordWithHook(attempt, kind, outcome, nil)
}

func publishHandoffFailure(attempt handoffAttempt, detail string) error {
	detail = strings.TrimSpace(detail)
	if len(detail) > maxFailureDetail {
		detail = detail[:maxFailureDetail]
		for !utf8.ValidString(detail) {
			detail = detail[:len(detail)-1]
		}
	}
	return publishHandoffRecordData(attempt, recordFailure, "", detail, nil)
}

// publishHandoffRecordWithHook stages and verifies a complete immutable record
// before linking it at its protocol name. The final hard link is the atomic
// publication point: a reader sees either ENOENT or the fully written payload,
// never the O_EXCL-created file while it is still being written. beforePublish
// exists only to make that boundary deterministic in package tests.
func publishHandoffRecordWithHook(attempt handoffAttempt, kind, outcome string, beforePublish func()) error {
	return publishHandoffRecordData(attempt, kind, outcome, "", beforePublish)
}

func publishHandoffRecordData(attempt handoffAttempt, kind, outcome, detail string, beforePublish func()) error {
	if err := validateExistingHandoff(attempt); err != nil {
		return err
	}
	if err := validateRecordPayload(kind, outcome, detail); err != nil {
		return err
	}
	record := handoffRecord{
		Version: protocolVersion, Kind: kind, FlowID: attempt.FlowID,
		PhaseID: attempt.PhaseID, LaunchID: attempt.LaunchID,
		Nonce: attempt.Nonce, Outcome: outcome, Detail: detail,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode Flow launch handoff %s: %w", kind, err)
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(attempt.HandoffDir, ".handoff-"+kind+"-")
	if err != nil {
		return fmt.Errorf("create staged Flow launch handoff %s: %w", kind, err)
	}
	stagedPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(stagedPath)
	}()
	if err := file.Chmod(artifacts.FilePerm); err != nil {
		return fmt.Errorf("set staged Flow launch handoff %s permissions: %w", kind, err)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect staged Flow launch handoff %s: %w", kind, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged Flow launch handoff %s must be regular", kind)
	}
	if err := requireOwnedPrivate(info, artifacts.FilePerm, "staged Flow launch handoff "+kind); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write staged Flow launch handoff %s: %w", kind, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync staged Flow launch handoff %s: %w", kind, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind staged Flow launch handoff %s: %w", kind, err)
	}
	verified, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return fmt.Errorf("verify staged Flow launch handoff %s: %w", kind, err)
	}
	if !bytes.Equal(verified, data) {
		return fmt.Errorf("verify staged Flow launch handoff %s: readback mismatch", kind)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged Flow launch handoff %s: %w", kind, err)
	}
	if beforePublish != nil {
		beforePublish()
	}
	path := filepath.Join(attempt.HandoffDir, kind)
	if err := os.Link(stagedPath, path); errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EEXIST) {
		return errRecordExists
	} else if err != nil {
		return fmt.Errorf("publish Flow launch handoff %s: %w", kind, err)
	}
	return nil
}

func readHandoffRecord(attempt handoffAttempt, kind string) (handoffRecord, error) {
	if err := validateExistingHandoff(attempt); err != nil {
		return handoffRecord{}, err
	}
	switch kind {
	case recordRunnerClaim, recordReady, recordDecision, recordStarted, recordStartedConfirmed, recordFailure:
	default:
		return handoffRecord{}, fmt.Errorf("unexpected Flow launch handoff record %q", kind)
	}
	path := filepath.Join(attempt.HandoffDir, kind)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return handoffRecord{}, fmt.Errorf("open Flow launch handoff %s: %w", kind, err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return handoffRecord{}, fmt.Errorf("inspect Flow launch handoff %s: %w", kind, err)
	}
	if !info.Mode().IsRegular() {
		return handoffRecord{}, fmt.Errorf("Flow launch handoff %s must be regular", kind)
	}
	if err := requireOwnedPrivate(info, artifacts.FilePerm, "Flow launch handoff "+kind); err != nil {
		return handoffRecord{}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return handoffRecord{}, fmt.Errorf("read Flow launch handoff %s: %w", kind, err)
	}
	if len(data) > maxRecordBytes {
		return handoffRecord{}, fmt.Errorf("Flow launch handoff %s is too large", kind)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record handoffRecord
	if err := decoder.Decode(&record); err != nil {
		return handoffRecord{}, fmt.Errorf("decode Flow launch handoff %s: %w", kind, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return handoffRecord{}, fmt.Errorf("Flow launch handoff %s has trailing data", kind)
	}
	if record.Version != protocolVersion || record.Kind != kind ||
		record.FlowID != attempt.FlowID || record.PhaseID != attempt.PhaseID ||
		record.LaunchID != attempt.LaunchID || record.Nonce != attempt.Nonce {
		return handoffRecord{}, fmt.Errorf("Flow launch handoff %s identity mismatch", kind)
	}
	if err := validateRecordPayload(kind, record.Outcome, record.Detail); err != nil {
		return handoffRecord{}, err
	}
	return record, nil
}

func validateRecordPayload(kind, outcome, detail string) error {
	switch kind {
	case recordRunnerClaim, recordReady, recordStarted, recordStartedConfirmed:
		if outcome != "" || detail != "" {
			return fmt.Errorf("Flow launch handoff %s cannot have a payload", kind)
		}
	case recordDecision:
		if detail != "" || (outcome != decisionCommit && outcome != decisionAbort) {
			return fmt.Errorf("invalid Flow launch decision %q", outcome)
		}
	case recordFailure:
		if outcome != "" || strings.TrimSpace(detail) == "" || len(detail) > maxFailureDetail {
			return errors.New("invalid Flow launch runner failure detail")
		}
	default:
		return fmt.Errorf("unexpected Flow launch handoff record %q", kind)
	}
	return nil
}

func cleanupHandoff(attempt handoffAttempt) error {
	if err := validateExistingHandoff(attempt); err != nil {
		if isMissingRecord(err) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(attempt.HandoffDir)
	if err != nil {
		return fmt.Errorf("list Flow launch handoff: %w", err)
	}
	for _, entry := range entries {
		kind := entry.Name()
		if strings.HasPrefix(kind, ".handoff-") {
			if err := os.Remove(filepath.Join(attempt.HandoffDir, kind)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove staged Flow launch handoff %s: %w", kind, err)
			}
			continue
		}
		if kind != recordRunnerClaim && kind != recordReady && kind != recordDecision && kind != recordStarted && kind != recordStartedConfirmed && kind != recordFailure {
			return fmt.Errorf("unexpected Flow launch handoff artifact %q", kind)
		}
		if _, err := readHandoffRecord(attempt, kind); err != nil {
			return err
		}
	}
	for _, kind := range []string{recordRunnerClaim, recordReady, recordDecision, recordStarted, recordStartedConfirmed, recordFailure} {
		if err := os.Remove(filepath.Join(attempt.HandoffDir, kind)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Flow launch handoff %s: %w", kind, err)
		}
	}
	if err := os.Remove(attempt.HandoffDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove Flow launch handoff directory: %w", err)
	}
	return nil
}
