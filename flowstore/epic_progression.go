package flowstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const epicProgressionSchemaVersion = 1

var errPreparedEpicProgressionCommitUnknown = errors.New("prepared epic progression commit outcome is unknown")

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

// EpicProgressionHalt is the durable reason progression stopped. Halt writes
// are reserved for later epic slices, but v1 pins their validation now.
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
	Halt          *EpicProgressionHalt
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// EpicProgressionUpdate enables or disables one epic. Enabling clears a sticky
// halt; disabling retains an existing halt and otherwise writes normal off.
type EpicProgressionUpdate struct {
	Key     EpicProgressionKey
	Enabled bool
}

// PreparedEpicProgressionUpdate binds enablement to one exact prepared child
// Flow. Callers hold that Flow's launch/close reservation around this update.
type PreparedEpicProgressionUpdate struct {
	FlowID string
	Key    EpicProgressionKey
	Bead   BeadLink
}

type storedEpicProgressionDTO struct {
	SchemaVersion int                  `json:"schema_version"`
	RepoPath      string               `json:"repo_path"`
	EpicID        string               `json:"epic_id"`
	Enabled       *bool                `json:"enabled"`
	Halt          *EpicProgressionHalt `json:"halt,omitempty"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
}

type epicProgressionBackend interface {
	readEpicProgression(EpicProgressionKey) (EpicProgression, bool, error)
	setEpicProgression(EpicProgressionUpdate, func() time.Time) (EpicProgression, error)
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

// SetEpicProgression atomically upserts one normal enabled/disabled state.
// Redundant writes preserve both timestamps and perform no row update.
func (s *Store) SetEpicProgression(update EpicProgressionUpdate) (EpicProgression, error) {
	key, err := normalizeEpicProgressionKey(update.Key)
	if err != nil {
		return EpicProgression{}, err
	}
	update.Key = key
	backend, ok := s.backend.(epicProgressionBackend)
	if !ok {
		return EpicProgression{}, errors.New("flow backend does not support epic progression")
	}
	return backend.setEpicProgression(update, s.now)
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
		"SELECT flow_id, repo_path, status, updated_at, bead_id, epic_id, prepared_at, record FROM flows WHERE flow_id = ?", update.FlowID,
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
	if !progressionFound || !progression.Enabled || progression.Halt != nil {
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
	if record.Halt == nil {
		return nil
	}
	halt := record.Halt
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
	data, err := json.MarshalIndent(storedEpicProgressionDTO{
		SchemaVersion: record.SchemaVersion,
		RepoPath:      record.RepoPath,
		EpicID:        record.EpicID,
		Enabled:       &enabled,
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
	var dto storedEpicProgressionDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: %w", repoPath, epicID, err)
	}
	if dto.Enabled == nil {
		return EpicProgression{}, fmt.Errorf("decode epic progression %q/%q: enabled is required", repoPath, epicID)
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
		if current.Enabled == update.Enabled && current.Halt == halt {
			if err := tx.Commit(); err != nil {
				return EpicProgression{}, fmt.Errorf("commit epic progression update %q/%q: %w", update.Key.RepoPath, update.Key.EpicID, err)
			}
			return current, nil
		}
		current.Enabled = update.Enabled
		current.Halt = halt
		current.UpdatedAt = epicProgressionMutationTime(current, now())
	} else {
		stamp := now().UTC()
		current = EpicProgression{
			SchemaVersion: epicProgressionSchemaVersion,
			RepoPath:      update.Key.RepoPath,
			EpicID:        update.Key.EpicID,
			Enabled:       update.Enabled,
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

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}

func epicProgressionMutationTime(record EpicProgression, candidate time.Time) time.Time {
	return monotonicMutationTime(candidate, record.CreatedAt, record.UpdatedAt)
}
