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

func TestSQLiteV3ToV4PreservesFlowBlobAndFencesPreparationNonce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, databaseFilename)
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(flowTableSchemaV3 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + epicProgressionTableSchema + `;
` + flowBeadCompatibilityTrigger + `;
` + flowPreparedCompatibilityTrigger + `;
PRAGMA user_version = 3;`); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v3-preparation", Title: "V3", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Phases: []FlowPhase{}, PreparationNonce: "0123456789abcdef0123456789abcdef", CreatedAt: stamp, UpdatedAt: stamp,
	}
	blob, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record, bead_id, epic_id, prepared_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		projection.flowID, projection.repoPath, projection.status, projection.updatedAt, blob, projection.beadID, projection.epicID, projection.preparedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record, bead_id, epic_id, prepared_at) VALUES(?, ?, ?, ?, ?, '', '', '')",
		"malformed", filepath.Join(root, "repo"), StatusPending, projection.updatedAt, []byte("{not-json")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	var gotBlob []byte
	var nonce string
	if err := backend.db.QueryRow("SELECT record, preparation_nonce FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob, &nonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) || nonce != record.PreparationNonce {
		t.Fatalf("v3 Flow migration = blobEqual %t, nonce %q", bytes.Equal(gotBlob, blob), nonce)
	}
	if err := backend.db.QueryRow("SELECT preparation_nonce FROM flows WHERE flow_id = 'malformed'").Scan(&nonce); err != nil || nonce != "" {
		t.Fatalf("malformed row nonce = %q, err %v", nonce, err)
	}
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 4 {
		t.Fatalf("user_version = %d, err %v", version, err)
	}
	listed, listErr := store.List(FlowFilter{})
	if len(listed) != 1 {
		t.Fatalf("List() healthy rows = %#v, err %v", listed, listErr)
	}
	partial, ok := AsPartialList(listErr)
	if !ok || len(partial.Entries) != 1 || partial.Entries[0].FlowID != "malformed" {
		t.Fatalf("List() partial error = %#v, %v", partial, listErr)
	}

	var old map[string]json.RawMessage
	if err := json.Unmarshal(blob, &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "preparation_nonce")
	oldBlob, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", oldBlob, record.FlowID)
	if err == nil || !strings.Contains(err.Error(), "older approach version cannot remove persisted preparation nonce") {
		t.Fatalf("v3-style update error = %v", err)
	}
}
