package flowstore

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteV2ToV4PreservesFlowBlobAndAddsProgressionObjects(t *testing.T) {
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
	var preparedAt, preparationNonce string
	if err := backend.db.QueryRow("SELECT record, prepared_at, preparation_nonce FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob, &preparedAt, &preparationNonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) || preparedAt != "" || preparationNonce != "" {
		t.Fatalf("v2 Flow changed during migration: blobEqual=%t prepared_at=%q nonce=%q", bytes.Equal(gotBlob, blob), preparedAt, preparationNonce)
	}
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != databaseSchemaVersion {
		t.Fatalf("user_version = %d, err %v; want %d", version, err, databaseSchemaVersion)
	}
	if _, _, err := store.ReadEpicProgression(EpicProgressionKey{RepoPath: record.RepoPath, EpicID: "epic"}); err != nil {
		t.Fatalf("new progression table unreadable: %v", err)
	}
}

func TestSQLiteV3ToV4PreservesMarkedFlowAndAddsClaimCompatibility(t *testing.T) {
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
	stamp := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v3-marked", Title: "Marked", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Bead: BeadLink{ID: "epic.1", EpicID: "epic"}, ProgressionClaim: true,
		Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
	}
	blob, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record, bead_id, epic_id, prepared_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		projection.flowID, projection.repoPath, projection.status, projection.updatedAt, blob, projection.beadID, projection.epicID, projection.preparedAt); err != nil {
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
	if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Fatal("v3 marked Flow changed during migration")
	}
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != databaseSchemaVersion {
		t.Fatalf("user_version = %d, err %v; want %d", version, err, databaseSchemaVersion)
	}
	if got, err := store.Read(record.FlowID); err != nil || !got.ProgressionClaim {
		t.Fatalf("marked Flow after migration = marker %t, err %v", got.ProgressionClaim, err)
	}
}

func TestSQLiteV0AndV1ToV4AddEveryProjectionWithoutReencodingFlows(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, databaseFilename)
			db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(flowTableSchemaV1 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + fmt.Sprintf("PRAGMA user_version = %d;", version)); err != nil {
				t.Fatal(err)
			}
			stamp := time.Date(2026, 8, 14, 16, version, 0, 0, time.UTC)
			record := FlowRecord{
				SchemaVersion: schemaVersion, FlowID: fmt.Sprintf("v%d-flow", version), Title: "Legacy", Instructions: "Test.",
				Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
			}
			blob, projection, err := encodeStoredFlow(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record) VALUES(?, ?, ?, ?, ?)",
				projection.flowID, projection.repoPath, projection.status, projection.updatedAt, blob); err != nil {
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
			var beadID, epicID, preparedAt, preparationNonce string
			if err := backend.db.QueryRow("SELECT record, bead_id, epic_id, prepared_at, preparation_nonce FROM flows WHERE flow_id = ?", record.FlowID).
				Scan(&gotBlob, &beadID, &epicID, &preparedAt, &preparationNonce); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotBlob, blob) || beadID != "" || epicID != "" || preparedAt != "" || preparationNonce != "" {
				t.Fatalf("v%d Flow migration changed legacy content: blobEqual=%t bead=%q epic=%q prepared=%q nonce=%q",
					version, bytes.Equal(gotBlob, blob), beadID, epicID, preparedAt, preparationNonce)
			}
		})
	}
}

func TestSQLiteV2ToV4RejectsInvalidPredecessorWithoutPartialObjects(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, databaseFilename)
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately omit one required v2 index. Predecessor validation must fail
	// before any v4 ALTER/CREATE statement is allowed to land.
	if _, err := db.Exec(flowTableSchemaV2 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
` + flowBeadCompatibilityTrigger + `; PRAGMA user_version = 2;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(StoreOptions{Root: root}); err == nil {
		t.Fatal("NewStore() accepted invalid v2 predecessor")
	}

	db, err = sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, preparedColumns, progressionTables int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('flows') WHERE name = 'prepared_at'").Scan(&preparedColumns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'epic_progressions'").Scan(&progressionTables); err != nil {
		t.Fatal(err)
	}
	if version != 2 || preparedColumns != 0 || progressionTables != 0 {
		t.Fatalf("failed migration left version=%d prepared_columns=%d progression_tables=%d", version, preparedColumns, progressionTables)
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

func TestProgressionClaimTriggerRejectsV3StyleMarkerRemoval(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	flow, _, err := store.CreatePreparation(FlowRecord{
		FlowID: "claim-protected", Title: "Protected", Instructions: "Test.", RepoPath: filepath.Join(root, "repo"),
		Bead: BeadLink{ID: "epic.1", EpicID: "epic"}, ProgressionClaim: true,
	}, CreateOptions{})
	if err != nil {
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
	delete(old, "progression_claim")
	oldBlob, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", oldBlob, flow.FlowID)
	if err == nil || !strings.Contains(err.Error(), "older approach version cannot remove persisted progression claim marker") {
		t.Fatalf("v3-style update error = %v", err)
	}
	if got, err := store.Read(flow.FlowID); err != nil || !got.ProgressionClaim {
		t.Fatalf("protected Flow after rejected update = marker %t, err %v", got.ProgressionClaim, err)
	}
}
