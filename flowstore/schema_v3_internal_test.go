package flowstore

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteV2ToV3PreservesFlowBlobAndAddsProgressionObjects(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, databaseFilename)
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(flowTableSchemaV2 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + flowBeadCompatibilityTrigger + `; PRAGMA user_version = 2;`); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v2-flow", Title: "V2", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Bead: BeadLink{ID: "epic.1", EpicID: "epic"}, Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
	}
	blob, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record, bead_id, epic_id) VALUES(?, ?, ?, ?, ?, ?, ?)",
		projection.flowID, projection.repoPath, projection.status, projection.updatedAt, blob, projection.beadID, projection.epicID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	defer store.Close()
	backend := store.backend.(*sqliteBackend)
	var gotBlob []byte
	var preparedAt string
	if err := backend.db.QueryRow("SELECT record, prepared_at FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob, &preparedAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) || preparedAt != "" {
		t.Fatalf("v2 Flow changed during migration: blobEqual=%t prepared_at=%q", bytes.Equal(gotBlob, blob), preparedAt)
	}
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("user_version = %d, err %v", version, err)
	}
	if _, _, err := store.ReadEpicProgression(EpicProgressionKey{RepoPath: record.RepoPath, EpicID: "epic"}); err != nil {
		t.Fatalf("new progression table unreadable: %v", err)
	}
}

func TestPreparationTriggerRejectsV2StyleReceiptRemoval(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "protected", Title: "Protected", Instructions: "Test.", RepoPath: filepath.Join(root, "repo"),
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{FlowID: flow.FlowID, WorktreePath: filepath.Join(root, "worktree"), Branch: "flow/protected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*sqliteBackend)
	var blob []byte
	if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", flow.FlowID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	var old map[string]json.RawMessage
	if err := json.Unmarshal(blob, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "prepared_at")
	oldBlob, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", oldBlob, flow.FlowID)
	if err == nil || !strings.Contains(err.Error(), "older approach version cannot remove persisted preparation receipt") {
		t.Fatalf("v2-style update error = %v", err)
	}
	if got, err := store.Read(flow.FlowID); err != nil || got.PreparedAt == nil {
		t.Fatalf("protected Flow after rejected update = receipt %v, err %v", got.PreparedAt, err)
	}
}
