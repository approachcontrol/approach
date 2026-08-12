package flowstore

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

const storageTimeLayout = "2006-01-02T15:04:05.000000000Z"

type flowProjection struct {
	flowID    string
	repoPath  string
	status    string
	updatedAt string
}

type storageGraphRecovery struct {
	Status string `json:"status"`
}

// storedFlowDTO declares the byte-stable storage field order. It deliberately
// mirrors FlowRecord and appends the storage-only recovery marker last.
type storedFlowDTO struct {
	SchemaVersion int         `json:"schema_version"`
	FlowID        string      `json:"flow_id"`
	Title         string      `json:"title"`
	Instructions  string      `json:"instructions"`
	Status        string      `json:"status"`
	RepoPath      string      `json:"repo_path"`
	WorktreePath  string      `json:"worktree_path,omitempty"`
	Branch        string      `json:"branch,omitempty"`
	BaseRef       string      `json:"base_ref,omitempty"`
	Commit        string      `json:"commit,omitempty"`
	PresetName    string      `json:"preset_name,omitempty"`
	PlanID        string      `json:"plan_id,omitempty"`
	PlanPath      string      `json:"plan_path,omitempty"`
	Issue         Issue       `json:"issue,omitempty"`
	PR            PullRequest `json:"pr,omitempty"`
	Merge         Merge       `json:"merge,omitempty"`
	// Closed is omitzero, deliberately unlike its omitempty neighbours:
	// omitempty does not omit a zero struct, and this struct is what the
	// default_seed golden is marshalled from.
	Closed        Closure               `json:"closed,omitzero"`
	AutoMode      bool                  `json:"auto_mode,omitempty"`
	Headless      bool                  `json:"headless"`
	Phases        []FlowPhase           `json:"phases"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	GraphRecovery *storageGraphRecovery `json:"graph_recovery,omitempty"`
}

// storageGraphRecoveryStatusEncodable lists the statuses encode understands.
// PresetEdgesRestored is knowingly dropped rather than persisted — it means the
// edges were rebuilt, so there is nothing left to mark. An UNRECOGNIZED status
// is different: decode errors on one, so letting encode drop it silently would
// make a future status vanish on the next save with no diagnostic anywhere.
func storageGraphRecoveryStatusEncodable(status string) bool {
	return storageGraphRecoveryStatusPersisted(status) || status == GraphRecoveryPresetEdgesRestored
}

// storageGraphRecoveryStatusPersisted is the set that survives a round trip.
func storageGraphRecoveryStatusPersisted(status string) bool {
	return status == "" || status == GraphRecoveryMissingEdgesUnresolved
}

func storageDTOFromRecord(record FlowRecord) storedFlowDTO {
	dto := storedFlowDTO{
		SchemaVersion: record.SchemaVersion,
		FlowID:        record.FlowID,
		Title:         record.Title,
		Instructions:  record.Instructions,
		Status:        record.Status,
		RepoPath:      record.RepoPath,
		WorktreePath:  record.WorktreePath,
		Branch:        record.Branch,
		BaseRef:       record.BaseRef,
		Commit:        record.Commit,
		PresetName:    record.PresetName,
		PlanID:        record.PlanID,
		PlanPath:      record.PlanPath,
		Issue:         record.Issue,
		PR:            record.PR,
		Merge:         record.Merge,
		Closed:        record.Closed,
		AutoMode:      record.AutoMode,
		Headless:      record.Headless,
		Phases:        record.Phases,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}
	if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved {
		dto.GraphRecovery = &storageGraphRecovery{Status: record.GraphRecovery.Status}
	}
	return dto
}

func (dto storedFlowDTO) record() FlowRecord {
	record := FlowRecord{
		SchemaVersion: dto.SchemaVersion,
		FlowID:        dto.FlowID,
		Title:         dto.Title,
		Instructions:  dto.Instructions,
		Status:        dto.Status,
		RepoPath:      dto.RepoPath,
		WorktreePath:  dto.WorktreePath,
		Branch:        dto.Branch,
		BaseRef:       dto.BaseRef,
		Commit:        dto.Commit,
		PresetName:    dto.PresetName,
		PlanID:        dto.PlanID,
		PlanPath:      dto.PlanPath,
		Issue:         dto.Issue,
		PR:            dto.PR,
		Merge:         dto.Merge,
		Closed:        dto.Closed,
		AutoMode:      dto.AutoMode,
		Headless:      dto.Headless,
		Phases:        dto.Phases,
		CreatedAt:     dto.CreatedAt,
		UpdatedAt:     dto.UpdatedAt,
	}
	if dto.GraphRecovery != nil {
		record.GraphRecovery.Status = dto.GraphRecovery.Status
	}
	return record
}

func encodeStoredFlow(record FlowRecord) ([]byte, flowProjection, error) {
	if !storageGraphRecoveryStatusEncodable(record.GraphRecovery.Status) {
		return nil, flowProjection{}, fmt.Errorf("encode flow %q: unknown graph recovery status %q",
			record.FlowID, record.GraphRecovery.Status)
	}
	if err := validateClosure(record.Closed); err != nil {
		return nil, flowProjection{}, fmt.Errorf("encode flow %q: %w", record.FlowID, err)
	}
	updatedAt, err := formatStorageTime(record.UpdatedAt)
	if err != nil {
		return nil, flowProjection{}, fmt.Errorf("encode flow %q: %w", record.FlowID, err)
	}
	data, err := json.MarshalIndent(storageDTOFromRecord(record), "", "  ")
	if err != nil {
		return nil, flowProjection{}, fmt.Errorf("encode flow %q: %w", record.FlowID, err)
	}
	return data, flowProjection{
		flowID:    record.FlowID,
		repoPath:  filepath.Clean(record.RepoPath),
		status:    record.Status,
		updatedAt: updatedAt,
	}, nil
}

func decodeStoredFlow(flowID, repoPath, status, updatedAt string, data []byte) (storedFlow, error) {
	var dto storedFlowDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return storedFlow{}, fmt.Errorf("decode flow %q record: %w", flowID, err)
	}
	// A present marker must carry the one value encode ever writes; an explicitly
	// empty or unrecognized one is corruption, not a tolerable variant.
	if dto.GraphRecovery != nil && dto.GraphRecovery.Status != GraphRecoveryMissingEdgesUnresolved {
		return storedFlow{}, fmt.Errorf("flow %q has unknown graph recovery status %q", flowID, dto.GraphRecovery.Status)
	}
	record := dto.record()
	if err := validateClosure(record.Closed); err != nil {
		return storedFlow{}, fmt.Errorf("decode flow %q record: %w", flowID, err)
	}
	if record.SchemaVersion != schemaVersion {
		return storedFlow{}, fmt.Errorf("flow %q has unsupported schema version %d", flowID, record.SchemaVersion)
	}
	if record.FlowID != flowID {
		return storedFlow{}, fmt.Errorf("flow row %q contains record id %q", flowID, record.FlowID)
	}
	if filepath.Clean(record.RepoPath) != repoPath {
		return storedFlow{}, fmt.Errorf("flow %q repo projection %q disagrees with record %q", flowID, repoPath, record.RepoPath)
	}
	if record.Status != status {
		return storedFlow{}, fmt.Errorf("flow %q status projection %q disagrees with record %q", flowID, status, record.Status)
	}
	wantUpdatedAt, err := formatStorageTime(record.UpdatedAt)
	if err != nil {
		return storedFlow{}, fmt.Errorf("decode flow %q: %w", flowID, err)
	}
	if wantUpdatedAt != updatedAt {
		return storedFlow{}, fmt.Errorf("flow %q updated_at projection %q disagrees with record %q", flowID, updatedAt, wantUpdatedAt)
	}
	parsed, err := time.Parse(storageTimeLayout, updatedAt)
	if err != nil || formatStorageTimeUnchecked(parsed) != updatedAt {
		return storedFlow{}, fmt.Errorf("flow %q has invalid updated_at projection %q", flowID, updatedAt)
	}
	return storedFlow{record: record}, nil
}

func formatStorageTime(value time.Time) (string, error) {
	value = value.UTC()
	if value.Year() < 0 || value.Year() > 9999 {
		return "", fmt.Errorf("timestamp year %d is outside 0000-9999", value.Year())
	}
	return formatStorageTimeUnchecked(value), nil
}

func formatStorageTimeUnchecked(value time.Time) string {
	value = value.UTC()
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%09dZ",
		value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), value.Nanosecond())
}
