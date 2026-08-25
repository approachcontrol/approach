package flowstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const epicProgressionSchemaVersion = 1

var errPreparedEpicProgressionCommitUnknown = errors.New("prepared epic progression commit outcome is unknown")

// ErrEpicProgressionActivationChanged reports that a progression edge was
// observed under an activation other than the one currently stored.
var ErrEpicProgressionActivationChanged = errors.New("epic progression activation changed")

// IsPreparedEpicProgressionCommitUnknown reports that an atomic enable reached
// commit but SQLite could not confirm whether that commit became durable.
func IsPreparedEpicProgressionCommitUnknown(err error) bool {
	return errors.Is(err, errPreparedEpicProgressionCommitUnknown)
}

// EpicProgressionKey identifies one repository-local Beads epic.
type EpicProgressionKey struct {
	RepoPath string
	EpicID   string
}

// EpicProgressionHalt is the durable reason progression stopped.
type EpicProgressionHalt struct {
	ChildBeadID string `json:"child_bead_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}

// EpicProgression is the persisted auto-progression state for one epic.
type EpicProgression struct {
	SchemaVersion int
	RepoPath      string
	EpicID        string
	Enabled       bool
	Done          bool
	Halt          *EpicProgressionHalt
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// EpicProgressionUpdate requests one complete target state. Enabling clears
// done and halt; explicit normal off clears done and retains a sticky halt;
// done may be newly established only from authoritative active state.
type EpicProgressionUpdate struct {
	Key                EpicProgressionKey
	Enabled            bool
	Done               bool
	ExpectedActivation time.Time
}

// EpicProgressionHaltUpdate records why progression stopped. Only authoritative
// active state may halt, and the first halt tuple is the one that sticks.
type EpicProgressionHaltUpdate struct {
	Key                EpicProgressionKey
	Halt               EpicProgressionHalt
	ExpectedActivation time.Time
}

// PreparedEpicProgressionUpdate binds enablement to one exact prepared child
// Flow. Callers hold that Flow's launch/close reservation around this update.
type PreparedEpicProgressionUpdate struct {
	FlowID string
	Key    EpicProgressionKey
	Bead   BeadLink
}

// EpicProgressionSuccessorOutcome classifies the authoritative relationship
// between an already-enabled epic and one prepared sequential successor.
type EpicProgressionSuccessorOutcome string

const (
	EpicProgressionSuccessorInactive         EpicProgressionSuccessorOutcome = "inactive"
	EpicProgressionSuccessorReleased         EpicProgressionSuccessorOutcome = "released"
	EpicProgressionSuccessorOwnedObstruction EpicProgressionSuccessorOutcome = "owned_obstruction"
	EpicProgressionSuccessorAccepted         EpicProgressionSuccessorOutcome = "accepted"
	EpicProgressionSuccessorRetryable        EpicProgressionSuccessorOutcome = "retryable"
)

// EpicProgressionSuccessorUpdate names the exact prepared Flow that sequential
// progression owns. Callers hold its launch/close reservation while reconciling.
type EpicProgressionSuccessorUpdate struct {
	FlowID             string
	Key                EpicProgressionKey
	Bead               BeadLink
	ExpectedActivation time.Time
}

// EpicProgressionSuccessorResult returns the records observed in the same
// writer transaction that produced Outcome. Flow is empty for inactive and for
// an absent released Flow.
type EpicProgressionSuccessorResult struct {
	Outcome     EpicProgressionSuccessorOutcome
	Progression EpicProgression
	Flow        FlowRecord
}

type storedEpicProgressionDTO struct {
	SchemaVersion int                  `json:"schema_version"`
	RepoPath      string               `json:"repo_path"`
	EpicID        string               `json:"epic_id"`
	Enabled       *bool                `json:"enabled"`
	Done          *bool                `json:"done"`
	Halt          *EpicProgressionHalt `json:"halt,omitempty"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
}

type epicProgressionBackend interface {
	readEpicProgression(EpicProgressionKey) (EpicProgression, bool, error)
	setEpicProgression(EpicProgressionUpdate, func() time.Time) (EpicProgression, error)
	haltEpicProgression(EpicProgressionHaltUpdate, func() time.Time) (EpicProgression, error)
}

// ReadEpicProgression distinguishes a missing row from a malformed or
// unreadable row. Missing means normal disabled to callers that project state.
func (s *Store) ReadEpicProgression(key EpicProgressionKey) (EpicProgression, bool, error) {
	key, err := normalizeEpicProgressionKey(key)
	if err != nil {
		return EpicProgression{}, false, err
	}
	backend, ok := s.backend.(epicProgressionBackend)
	if !ok {
		return EpicProgression{}, false, errors.New("flow backend does not support epic progression")
	}
	return backend.readEpicProgression(key)
}

// SetEpicProgression atomically applies one active, normal-off, or done target.
// Redundant writes preserve both timestamps and perform no row update.
func (s *Store) SetEpicProgression(update EpicProgressionUpdate) (EpicProgression, error) {
	key, err := normalizeEpicProgressionKey(update.Key)
	if err != nil {
		return EpicProgression{}, err
	}
	update.Key = key
	if update.Done {
		update.ExpectedActivation, err = normalizeEpicProgressionActivation(update.ExpectedActivation)
		if err != nil {
			return EpicProgression{}, err
		}
	}
	backend, ok := s.backend.(epicProgressionBackend)
	if !ok {
		return EpicProgression{}, errors.New("flow backend does not support epic progression")
	}
	return backend.setEpicProgression(update, s.now)
}

// HaltEpicProgression atomically moves authoritative active progression to
// halted. Only active state may halt; an already-halted record retains its
// first halt tuple and both timestamps, so the original cause always wins.
func (s *Store) HaltEpicProgression(update EpicProgressionHaltUpdate) (EpicProgression, error) {
	key, err := normalizeEpicProgressionKey(update.Key)
	if err != nil {
		return EpicProgression{}, err
	}
	update.Key = key
	update.ExpectedActivation, err = normalizeEpicProgressionActivation(update.ExpectedActivation)
	if err != nil {
		return EpicProgression{}, err
	}
	if err := validateEpicProgressionHalt(update.Halt); err != nil {
		return EpicProgression{}, err
	}
	backend, ok := s.backend.(epicProgressionBackend)
	if !ok {
		return EpicProgression{}, errors.New("flow backend does not support epic progression")
	}
	return backend.haltEpicProgression(update, s.now)
}

// EnableEpicProgressionForPreparedFlow revalidates the exact child Flow and
// enables progression in one SQLite writer transaction.
func (s *Store) EnableEpicProgressionForPreparedFlow(update PreparedEpicProgressionUpdate) (EpicProgression, FlowRecord, error) {
	if err := validateFlowID(update.FlowID); err != nil {
		return EpicProgression{}, FlowRecord{}, err
	}
	key, err := normalizeEpicProgressionKey(update.Key)
	if err != nil {
		return EpicProgression{}, FlowRecord{}, err
	}
	if err := validateBeadLink(update.Bead); err != nil {
		return EpicProgression{}, FlowRecord{}, err
	}
	if update.Bead.ID == "" || update.Bead.EpicID == "" {
		return EpicProgression{}, FlowRecord{}, errors.New("prepared epic progression requires an exact child and epic Bead link")
	}
	if update.Bead.ID != strings.TrimSpace(update.Bead.ID) || update.Bead.EpicID != strings.TrimSpace(update.Bead.EpicID) {
		return EpicProgression{}, FlowRecord{}, errors.New("prepared epic progression Bead link must be canonical")
	}
	if update.Bead.EpicID != key.EpicID {
		return EpicProgression{}, FlowRecord{}, fmt.Errorf("prepared epic progression key epic %q does not match Flow link epic %q", key.EpicID, update.Bead.EpicID)
	}
	backend, ok := s.backend.(*sqliteBackend)
	if !ok {
		return EpicProgression{}, FlowRecord{}, errors.New("flow backend does not support atomic prepared epic progression")
	}
	tx, err := backend.beginTx(context.Background())
	if err != nil {
		return EpicProgression{}, FlowRecord{}, fmt.Errorf("begin prepared epic progression update %q: %w", update.FlowID, err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, found, err := queryStoredFlow(tx.QueryRow(
		"SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows WHERE flow_id = ?", update.FlowID,
	), update.FlowID)
	if err != nil {
		return EpicProgression{}, FlowRecord{}, err
	}
	if !found {
		return EpicProgression{}, FlowRecord{}, flowNotFoundError(update.FlowID)
	}
	flow := stored.record
	if filepath.Clean(flow.RepoPath) != key.RepoPath || flow.Bead != update.Bead {
		return EpicProgression{}, FlowRecord{}, fmt.Errorf("flow %q no longer has the required repository and Bead link", flow.FlowID)
	}
	if flow.PreparedAt == nil {
		return EpicProgression{}, FlowRecord{}, fmt.Errorf("flow %q preparation is incomplete", flow.FlowID)
	}
	if FlowClosed(flow) || DeriveStatus(flow) != StatusPending {
		return EpicProgression{}, FlowRecord{}, fmt.Errorf("flow %q is %s; prepared progression requires an open pending Flow", flow.FlowID, DeriveStatus(flow))
	}

	progression, progressionFound, err := queryEpicProgression(tx.QueryRow(
		"SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions WHERE repo_path = ? AND epic_id = ?",
		key.RepoPath, key.EpicID,
	), key)
	if err != nil {
		return EpicProgression{}, FlowRecord{}, err
	}
	if !progressionFound || !progression.Enabled || progression.Done || progression.Halt != nil {
		stamp := s.now().UTC()
		if !progressionFound {
			progression = EpicProgression{
				SchemaVersion: epicProgressionSchemaVersion,
				RepoPath:      key.RepoPath,
				EpicID:        key.EpicID,
				CreatedAt:     stamp,
			}
		} else {
			stamp = epicProgressionMutationTime(progression, stamp)
		}
		progression.Enabled = true
		progression.Done = false
		progression.Halt = nil
		progression.UpdatedAt = stamp
		data, updatedAt, err := encodeEpicProgression(progression)
		if err != nil {
			return EpicProgression{}, FlowRecord{}, err
		}
		if _, err := tx.Exec(`
INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record)
VALUES(?, ?, 1, ?, ?)
ON CONFLICT(repo_path, epic_id) DO UPDATE SET
    enabled=1,
    updated_at=excluded.updated_at,
    record=excluded.record`, progression.RepoPath, progression.EpicID, updatedAt, data); err != nil {
			return EpicProgression{}, FlowRecord{}, fmt.Errorf("enable epic progression %q/%q: %w", key.RepoPath, key.EpicID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return progression, flow, errors.Join(
			errPreparedEpicProgressionCommitUnknown,
			fmt.Errorf("commit prepared epic progression update %q: %w", update.FlowID, err),
		)
	}
	return progression, flow, nil
}

// ReconcileEpicProgressionSuccessor authoritatively classifies a prepared
// sequential successor without enabling or otherwise mutating progression.
// Inactive progression wins before any Flow condition is inspected.
func (s *Store) ReconcileEpicProgressionSuccessor(update EpicProgressionSuccessorUpdate) (EpicProgressionSuccessorResult, error) {
	retryable := func(err error) (EpicProgressionSuccessorResult, error) {
		return EpicProgressionSuccessorResult{Outcome: EpicProgressionSuccessorRetryable}, err
	}
	if err := validateFlowID(update.FlowID); err != nil {
		return retryable(err)
	}
	key, err := normalizeEpicProgressionKey(update.Key)
	if err != nil {
		return retryable(err)
	}
	if err := validateBeadLink(update.Bead); err != nil {
		return retryable(err)
	}
	if update.Bead.ID == "" || update.Bead.EpicID == "" ||
		update.Bead.ID != strings.TrimSpace(update.Bead.ID) ||
		update.Bead.EpicID != strings.TrimSpace(update.Bead.EpicID) {
		return retryable(errors.New("sequential epic progression requires a canonical exact child and epic Bead link"))
	}
	if update.Bead.EpicID != key.EpicID {
		return retryable(fmt.Errorf("sequential epic progression key epic %q does not match Flow link epic %q", key.EpicID, update.Bead.EpicID))
	}
	update.ExpectedActivation, err = normalizeEpicProgressionActivation(update.ExpectedActivation)
	if err != nil {
		return retryable(err)
	}
	backend, ok := s.backend.(*sqliteBackend)
	if !ok {
		return retryable(errors.New("flow backend does not support atomic sequential epic progression reconciliation"))
	}
	tx, err := backend.beginTx(context.Background())
	if err != nil {
		return retryable(fmt.Errorf("begin sequential epic progression reconciliation %q: %w", update.FlowID, err))
	}
	defer func() { _ = tx.Rollback() }()

	progression, found, err := queryEpicProgression(tx.QueryRow(
		"SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions WHERE repo_path = ? AND epic_id = ?",
		key.RepoPath, key.EpicID,
	), key)
	if err != nil {
		return retryable(err)
	}
	result := EpicProgressionSuccessorResult{Progression: progression}
	if !found || !progression.Enabled || progression.Done || progression.Halt != nil || !progression.UpdatedAt.Equal(update.ExpectedActivation) {
		result.Outcome = EpicProgressionSuccessorInactive
	} else {
		stored, flowFound, readErr := queryStoredFlow(tx.QueryRow(
			"SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, preparation_nonce, record FROM flows WHERE flow_id = ?", update.FlowID,
		), update.FlowID)
		if readErr != nil {
			return retryable(readErr)
		}
		if !flowFound {
			result.Outcome = EpicProgressionSuccessorReleased
		} else {
			result.Flow = stored.record
			switch {
			case filepath.Clean(result.Flow.RepoPath) != key.RepoPath || result.Flow.Bead != update.Bead:
				result.Outcome = EpicProgressionSuccessorReleased
			case result.Flow.PreparedAt == nil || FlowClosed(result.Flow) || DeriveStatus(result.Flow) != StatusPending:
				result.Outcome = EpicProgressionSuccessorOwnedObstruction
			default:
				result.Outcome = EpicProgressionSuccessorAccepted
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return retryable(fmt.Errorf("commit sequential epic progression reconciliation %q: %w", update.FlowID, err))
	}
	return result, nil
}

func normalizeEpicProgressionKey(key EpicProgressionKey) (EpicProgressionKey, error) {
	repoPath := strings.TrimSpace(key.RepoPath)
	if repoPath == "" {
		return EpicProgressionKey{}, errors.New("epic progression repo path is required")
	}
	if !filepath.IsAbs(repoPath) {
		return EpicProgressionKey{}, fmt.Errorf("epic progression repo path must be absolute: %s", repoPath)
	}
	epicID := strings.TrimSpace(key.EpicID)
	if epicID == "" {
		return EpicProgressionKey{}, errors.New("epic progression epic id is required")
	}
	return EpicProgressionKey{RepoPath: filepath.Clean(repoPath), EpicID: epicID}, nil
}

func normalizeEpicProgressionActivation(value time.Time) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, errors.New("epic progression expected activation is required")
	}
	encoded, err := formatStorageTime(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("epic progression expected activation is invalid: %w", err)
	}
	parsed, err := parseCanonicalStorageTime(encoded)
	if err != nil {
		return time.Time{}, fmt.Errorf("epic progression expected activation is invalid: %w", err)
	}
	return parsed, nil
}

func validateEpicProgression(record EpicProgression) error {
	key, err := normalizeEpicProgressionKey(EpicProgressionKey{RepoPath: record.RepoPath, EpicID: record.EpicID})
	if err != nil {
		return err
	}
	if key.RepoPath != record.RepoPath || key.EpicID != record.EpicID {
		return errors.New("epic progression key is not canonical")
	}
	if record.SchemaVersion != epicProgressionSchemaVersion {
		return fmt.Errorf("unsupported epic progression schema version %d", record.SchemaVersion)
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return errors.New("epic progression timestamps are invalid")
	}
	if record.Enabled && record.Halt != nil {
		return errors.New("enabled epic progression cannot be halted")
	}
	if record.Enabled && record.Done {
		return errors.New("enabled epic progression cannot be done")
	}
	if record.Done && record.Halt != nil {
		return errors.New("done epic progression cannot be halted")
	}
	if record.Halt == nil {
		return nil
	}
	return validateEpicProgressionHalt(*record.Halt)
}

func validateEpicProgressionHalt(halt EpicProgressionHalt) error {
	if strings.TrimSpace(halt.ChildBeadID) == "" || strings.TrimSpace(halt.Message) == "" ||
		halt.ChildBeadID != strings.TrimSpace(halt.ChildBeadID) || halt.Message != strings.TrimSpace(halt.Message) {
		return errors.New("epic progression halt tuple is incomplete or not canonical")
	}
	switch halt.Status {
	case StatusBlocked, StatusNeedsAttention, StatusClosed, StatusAbandoned:
		return nil
	default:
		return fmt.Errorf("epic progression halt status %q is invalid", halt.Status)
	}
}

func encodeEpicProgression(record EpicProgression) ([]byte, string, error) {
	if err := validateEpicProgression(record); err != nil {
		return nil, "", err
	}
	createdAt, err := formatStorageTime(record.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	updatedAt, err := formatStorageTime(record.UpdatedAt)
	if err != nil {
		return nil, "", err
	}
	enabled := record.Enabled
	done := record.Done
	data, err := json.MarshalIndent(storedEpicProgressionDTO{
		SchemaVersion: record.SchemaVersion,
		RepoPath:      record.RepoPath,
		EpicID:        record.EpicID,
		Enabled:       &enabled,
		Done:          &done,
		Halt:          record.Halt,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("encode epic progression: %w", err)
	}
	return data, updatedAt, nil
}

func decodeEpicProgression(repoPath, epicID string, enabled int, updatedAt string, data []byte) (EpicProgression, error) {
	if err := validateUniqueJSONFields(data); err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if err := validateEpicProgressionJSONFieldNames(data); err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: %w", repoPath, epicID, err)
	}
	var dto storedEpicProgressionDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if dto.Enabled == nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: enabled is required", repoPath, epicID)
	}
	if dto.Done == nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: done is required", repoPath, epicID)
	}
	created, err := parseCanonicalStorageTime(dto.CreatedAt)
	if err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q created_at: %w", repoPath, epicID, err)
	}
	updated, err := parseCanonicalStorageTime(dto.UpdatedAt)
	if err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q updated_at: %w", repoPath, epicID, err)
	}
	record := EpicProgression{
		SchemaVersion: dto.SchemaVersion,
		RepoPath:      dto.RepoPath,
		EpicID:        dto.EpicID,
		Enabled:       *dto.Enabled,
		Done:          *dto.Done,
		Halt:          dto.Halt,
		CreatedAt:     created,
		UpdatedAt:     updated,
	}
	if err := validateEpicProgression(record); err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if record.RepoPath != repoPath || record.EpicID != epicID {
		return EpicProgression{}, fmt.Errorf("epic progression projection %q/%q disagrees with record %q/%q", repoPath, epicID, record.RepoPath, record.EpicID)
	}
	if enabled != 0 && enabled != 1 {
		return EpicProgression{}, fmt.Errorf("epic progression %q/%q has invalid enabled projection %d", repoPath, epicID, enabled)
	}
	if record.Enabled != (enabled == 1) {
		return EpicProgression{}, fmt.Errorf("epic progression %q/%q enabled projection disagrees with record", repoPath, epicID)
	}
	wantUpdated, _ := formatStorageTime(record.UpdatedAt)
	if updatedAt != wantUpdated {
		return EpicProgression{}, fmt.Errorf("epic progression %q/%q updated_at projection %q disagrees with record %q", repoPath, epicID, updatedAt, wantUpdated)
	}
	if _, err := parseCanonicalStorageTime(updatedAt); err != nil {
		return EpicProgression{}, fmt.Errorf("epic progression %q/%q projection: %w", repoPath, epicID, err)
	}
	return record, nil
}

func validateUniqueJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := validateUniqueJSONValue(decoder); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("unexpected JSON object delimiter %q", closing)
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("unexpected JSON array delimiter %q", closing)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func validateEpicProgressionJSONFieldNames(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	if err := validateCanonicalJSONFieldNames(root, []string{
		"schema_version",
		"repo_path",
		"epic_id",
		"enabled",
		"done",
		"halt",
		"created_at",
		"updated_at",
	}); err != nil {
		return err
	}
	haltData, ok := root["halt"]
	if !ok || bytes.Equal(bytes.TrimSpace(haltData), []byte("null")) {
		return nil
	}
	var halt map[string]json.RawMessage
	if err := json.Unmarshal(haltData, &halt); err != nil {
		return err
	}
	return validateCanonicalJSONFieldNames(halt, []string{"child_bead_id", "status", "message"})
}

func validateCanonicalJSONFieldNames(fields map[string]json.RawMessage, canonical []string) error {
	for field := range fields {
		for _, want := range canonical {
			if strings.EqualFold(field, want) && field != want {
				return fmt.Errorf("JSON field %q must use canonical spelling %q", field, want)
			}
		}
	}
	return nil
}

func parseCanonicalStorageTime(value string) (time.Time, error) {
	parsed, err := time.Parse(storageTimeLayout, value)
	if err != nil || formatStorageTimeUnchecked(parsed) != value {
		return time.Time{}, fmt.Errorf("invalid canonical timestamp %q", value)
	}
	return parsed, nil
}

func (b *sqliteBackend) readEpicProgression(key EpicProgressionKey) (EpicProgression, bool, error) {
	return queryEpicProgression(b.db.QueryRow(
		"SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions WHERE repo_path = ? AND epic_id = ?",
		key.RepoPath, key.EpicID,
	), key)
}

func queryEpicProgression(row interface{ Scan(...any) error }, key EpicProgressionKey) (EpicProgression, bool, error) {
	var repoPath, epicID, updatedAt string
	var enabled int
	var data []byte
	if err := row.Scan(&repoPath, &epicID, &enabled, &updatedAt, &data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EpicProgression{}, false, nil
		}
		return EpicProgression{}, false, fmt.Errorf("read epic progression %q/%q: %w", key.RepoPath, key.EpicID, err)
	}
	record, err := decodeEpicProgression(repoPath, epicID, enabled, updatedAt, data)
	if err != nil {
		return EpicProgression{}, false, err
	}
	return record, true, nil
}

func (b *sqliteBackend) setEpicProgression(update EpicProgressionUpdate, now func() time.Time) (EpicProgression, error) {
	if update.Enabled && update.Done {
		return EpicProgression{}, errors.New("epic progression update cannot be both enabled and done")
	}
	tx, err := b.beginTx(context.Background())
	if err != nil {
		return EpicProgression{}, fmt.Errorf("begin epic progression update %q/%q: %w", update.Key.RepoPath, update.Key.EpicID, err)
	}
	defer func() { _ = tx.Rollback() }()
	current, found, err := queryEpicProgression(tx.QueryRow(
		"SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions WHERE repo_path = ? AND epic_id = ?",
		update.Key.RepoPath, update.Key.EpicID,
	), update.Key)
	if err != nil {
		return EpicProgression{}, err
	}
	if found {
		halt := current.Halt
		if update.Enabled {
			halt = nil
		}
		if update.Done {
			halt = nil
			if current.Done {
				if err := tx.Commit(); err != nil {
					return EpicProgression{}, fmt.Errorf("commit epic progression update %q/%q: %w", update.Key.RepoPath, update.Key.EpicID, err)
				}
				return current, nil
			}
			if !current.Done && (!current.Enabled || current.Halt != nil) {
				return EpicProgression{}, fmt.Errorf("epic progression %q/%q can only become done from active state", update.Key.RepoPath, update.Key.EpicID)
			}
			if !current.UpdatedAt.Equal(update.ExpectedActivation) {
				return EpicProgression{}, fmt.Errorf("%w for %q/%q", ErrEpicProgressionActivationChanged, update.Key.RepoPath, update.Key.EpicID)
			}
		}
		if current.Enabled == update.Enabled && current.Done == update.Done && current.Halt == halt {
			if err := tx.Commit(); err != nil {
				return EpicProgression{}, fmt.Errorf("commit epic progression update %q/%q: %w", update.Key.RepoPath, update.Key.EpicID, err)
			}
			return current, nil
		}
		current.Enabled = update.Enabled
		current.Done = update.Done
		current.Halt = halt
		current.UpdatedAt = epicProgressionMutationTime(current, now())
	} else {
		if update.Done {
			return EpicProgression{}, fmt.Errorf("epic progression %q/%q can only become done from active state", update.Key.RepoPath, update.Key.EpicID)
		}
		stamp := now().UTC()
		current = EpicProgression{
			SchemaVersion: epicProgressionSchemaVersion,
			RepoPath:      update.Key.RepoPath,
			EpicID:        update.Key.EpicID,
			Enabled:       update.Enabled,
			Done:          false,
			CreatedAt:     stamp,
			UpdatedAt:     stamp,
		}
	}
	data, updatedAt, err := encodeEpicProgression(current)
	if err != nil {
		return EpicProgression{}, err
	}
	_, err = tx.Exec(`
INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(repo_path, epic_id) DO UPDATE SET
    enabled=excluded.enabled,
    updated_at=excluded.updated_at,
    record=excluded.record`, current.RepoPath, current.EpicID, boolToSQLite(current.Enabled), updatedAt, data)
	if err != nil {
		return EpicProgression{}, fmt.Errorf("save epic progression %q/%q: %w", current.RepoPath, current.EpicID, err)
	}
	if err := tx.Commit(); err != nil {
		return EpicProgression{}, fmt.Errorf("commit epic progression update %q/%q: %w", current.RepoPath, current.EpicID, err)
	}
	return current, nil
}

func (b *sqliteBackend) haltEpicProgression(update EpicProgressionHaltUpdate, now func() time.Time) (EpicProgression, error) {
	tx, err := b.beginTx(context.Background())
	if err != nil {
		return EpicProgression{}, fmt.Errorf("begin epic progression halt %q/%q: %w", update.Key.RepoPath, update.Key.EpicID, err)
	}
	defer func() { _ = tx.Rollback() }()
	current, found, err := queryEpicProgression(tx.QueryRow(
		"SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions WHERE repo_path = ? AND epic_id = ?",
		update.Key.RepoPath, update.Key.EpicID,
	), update.Key)
	if err != nil {
		return EpicProgression{}, err
	}
	if !found {
		return EpicProgression{}, fmt.Errorf("epic progression %q/%q is off; only active progression can halt", update.Key.RepoPath, update.Key.EpicID)
	}
	if current.Halt != nil {
		// Halt is sticky: the first cause wins and neither timestamp moves, so
		// the row is left untouched rather than rewritten.
		if err := tx.Commit(); err != nil {
			return EpicProgression{}, fmt.Errorf("commit epic progression halt %q/%q: %w", update.Key.RepoPath, update.Key.EpicID, err)
		}
		return current, nil
	}
	if !current.Enabled || current.Done {
		state := "off"
		if current.Done {
			state = "done"
		}
		return EpicProgression{}, fmt.Errorf("epic progression %q/%q is %s; only active progression can halt", update.Key.RepoPath, update.Key.EpicID, state)
	}
	if !current.UpdatedAt.Equal(update.ExpectedActivation) {
		return EpicProgression{}, fmt.Errorf("%w for %q/%q", ErrEpicProgressionActivationChanged, update.Key.RepoPath, update.Key.EpicID)
	}
	halt := update.Halt
	current.Enabled = false
	current.Done = false
	current.Halt = &halt
	current.UpdatedAt = epicProgressionMutationTime(current, now())
	data, updatedAt, err := encodeEpicProgression(current)
	if err != nil {
		return EpicProgression{}, err
	}
	if _, err := tx.Exec(`
INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(repo_path, epic_id) DO UPDATE SET
    enabled=excluded.enabled,
    updated_at=excluded.updated_at,
    record=excluded.record`, current.RepoPath, current.EpicID, boolToSQLite(current.Enabled), updatedAt, data); err != nil {
		return EpicProgression{}, fmt.Errorf("halt epic progression %q/%q: %w", current.RepoPath, current.EpicID, err)
	}
	if err := tx.Commit(); err != nil {
		return EpicProgression{}, fmt.Errorf("commit epic progression halt %q/%q: %w", current.RepoPath, current.EpicID, err)
	}
	return current, nil
}

// decodeLegacyV3EpicProgression decodes the exact record shape written by the
// v3 database generation. It deliberately does not infer completion from the
// compatibility enabled projection; every migrated predecessor becomes
// Done=false.
func decodeLegacyV3EpicProgression(repoPath, epicID string, enabled int, updatedAt string, data []byte) (EpicProgression, error) {
	type legacyDTO struct {
		SchemaVersion int                  `json:"schema_version"`
		RepoPath      string               `json:"repo_path"`
		EpicID        string               `json:"epic_id"`
		Enabled       *bool                `json:"enabled"`
		Halt          *EpicProgressionHalt `json:"halt,omitempty"`
		CreatedAt     string               `json:"created_at"`
		UpdatedAt     string               `json:"updated_at"`
	}
	if err := validateUniqueJSONFields(data); err != nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if err := validateEpicProgressionJSONFieldNames(data); err != nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q: %w", repoPath, epicID, err)
	}
	var dto legacyDTO
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dto); err != nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if dto.Enabled == nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q: enabled is required", repoPath, epicID)
	}
	created, err := parseCanonicalStorageTime(dto.CreatedAt)
	if err != nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q created_at: %w", repoPath, epicID, err)
	}
	updated, err := parseCanonicalStorageTime(dto.UpdatedAt)
	if err != nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q updated_at: %w", repoPath, epicID, err)
	}
	record := EpicProgression{
		SchemaVersion: dto.SchemaVersion,
		RepoPath:      dto.RepoPath,
		EpicID:        dto.EpicID,
		Enabled:       *dto.Enabled,
		Done:          false,
		Halt:          dto.Halt,
		CreatedAt:     created,
		UpdatedAt:     updated,
	}
	if err := validateEpicProgression(record); err != nil {
		return EpicProgression{}, fmt.Errorf("decode legacy epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if record.RepoPath != repoPath || record.EpicID != epicID {
		return EpicProgression{}, fmt.Errorf("legacy epic progression projection %q/%q disagrees with record %q/%q", repoPath, epicID, record.RepoPath, record.EpicID)
	}
	if enabled != 0 && enabled != 1 {
		return EpicProgression{}, fmt.Errorf("legacy epic progression %q/%q has invalid enabled projection %d", repoPath, epicID, enabled)
	}
	if record.Enabled != (enabled == 1) {
		return EpicProgression{}, fmt.Errorf("legacy epic progression %q/%q enabled projection disagrees with record", repoPath, epicID)
	}
	wantUpdated, _ := formatStorageTime(record.UpdatedAt)
	if updatedAt != wantUpdated {
		return EpicProgression{}, fmt.Errorf("legacy epic progression %q/%q updated_at projection %q disagrees with record %q", repoPath, epicID, updatedAt, wantUpdated)
	}
	return record, nil
}

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}

func epicProgressionMutationTime(record EpicProgression, candidate time.Time) time.Time {
	return monotonicMutationTime(candidate, record.CreatedAt, record.UpdatedAt)
}
