package flowstore

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEpicProgressionCodecRejectsInvalidHaltStates(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	validHalt := &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked, Message: "child blocked"}
	base := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      "/repo",
		EpicID:        "epic",
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
	tests := []struct {
		name   string
		record EpicProgression
		want   string
	}{
		{name: "enabled with halt", record: func() EpicProgression { r := base; r.Enabled = true; r.Halt = validHalt; return r }(), want: "cannot be halted"},
		{name: "incomplete halt", record: func() EpicProgression {
			r := base
			r.Halt = &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked}
			return r
		}(), want: "incomplete"},
		{name: "unknown halt", record: func() EpicProgression {
			r := base
			r.Halt = &EpicProgressionHalt{ChildBeadID: "epic.1", Status: "paused", Message: "unknown"}
			return r
		}(), want: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := encodeEpicProgression(tt.record); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("encodeEpicProgression() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestEpicProgressionStickyHaltRetentionAndClearing(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := EpicProgressionKey{RepoPath: "/repo", EpicID: "epic"}
	halted := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      key.RepoPath,
		EpicID:        key.EpicID,
		Halt:          &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusNeedsAttention, Message: "needs a decision"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	data, updatedAt, err := encodeEpicProgression(halted)
	if err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*sqliteBackend)
	if _, err := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)`,
		key.RepoPath, key.EpicID, updatedAt, data); err != nil {
		t.Fatal(err)
	}
	disabled, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Halt == nil || *disabled.Halt != *halted.Halt || !disabled.UpdatedAt.Equal(halted.UpdatedAt) {
		t.Fatalf("disabled halted progression = %#v, want sticky no-op", disabled)
	}
	now = now.Add(time.Minute)
	enabled, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || enabled.Halt != nil || !enabled.UpdatedAt.Equal(now) {
		t.Fatalf("enabled progression = %#v, want cleared halt", enabled)
	}
}

func TestReconcileEpicProgressionSuccessorHaltedPrecedesFlowState(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := EpicProgressionKey{RepoPath: "/repo", EpicID: "epic"}
	halted := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      key.RepoPath,
		EpicID:        key.EpicID,
		Halt:          &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked, Message: "blocked"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	data, updatedAt, err := encodeEpicProgression(halted)
	if err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*sqliteBackend)
	if _, err := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)`,
		key.RepoPath, key.EpicID, updatedAt, data); err != nil {
		t.Fatal(err)
	}

	link := BeadLink{ID: "epic.2", EpicID: key.EpicID}
	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "prepared", RepoPath: key.RepoPath, Title: "Prepared", Instructions: "Test.", Bead: link,
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{
		FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/prepared",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatal(err)
	}

	for _, flowID := range []string{"absent", flow.FlowID} {
		result, err := store.ReconcileEpicProgressionSuccessor(EpicProgressionSuccessorUpdate{
			FlowID: flowID, Key: key, Bead: link,
		})
		if err != nil || result.Outcome != EpicProgressionSuccessorInactive {
			t.Fatalf("flow %q result = %#v, err %v; want inactive", flowID, result, err)
		}
	}
}

func TestReadEpicProgressionReportsCorruptRowsAsErrors(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	valid := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      "/repo",
		EpicID:        "epic",
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
	validData, validUpdatedAt, err := encodeEpicProgression(valid)
	if err != nil {
		t.Fatal(err)
	}
	missingEnabled := []byte(strings.Replace(string(validData), "  \"enabled\": false,\n", "", 1))
	nullEnabled := []byte(strings.Replace(string(validData), "\"enabled\": false", "\"enabled\": null", 1))
	for _, tt := range []struct {
		name      string
		enabled   int
		updatedAt string
		data      []byte
	}{
		{name: "malformed blob", enabled: 0, updatedAt: validUpdatedAt, data: []byte("{")},
		{name: "missing enabled", enabled: 0, updatedAt: validUpdatedAt, data: missingEnabled},
		{name: "null enabled", enabled: 0, updatedAt: validUpdatedAt, data: nullEnabled},
		{name: "projection mismatch", enabled: 1, updatedAt: validUpdatedAt, data: validData},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			backend := store.backend.(*sqliteBackend)
			if _, err := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, ?, ?, ?)`,
				valid.RepoPath, valid.EpicID, tt.enabled, tt.updatedAt, tt.data); err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.ReadEpicProgression(EpicProgressionKey{RepoPath: valid.RepoPath, EpicID: valid.EpicID}); err == nil || found {
				t.Fatalf("ReadEpicProgression() = found %t, err %v; want corruption error", found, err)
			}
		})
	}
}
