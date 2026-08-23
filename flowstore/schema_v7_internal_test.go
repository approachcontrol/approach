package flowstore

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createParentReleaseV6DatabaseAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	schema := flowTableSchemaV6 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + epicProgressionTableSchema + `;
` + flowBeadCompatibilityTrigger + `;
` + flowPreparedCompatibilityTrigger + `;
` + epicProgressionDoneInsertCompatibilityTrigger + `;
` + epicProgressionDoneUpdateCompatibilityTrigger + `;
` + flowProgressionClaimCompatibilityTrigger + `;
` + flowPreparationNonceCompatibilityTrigger + `;
PRAGMA user_version = 6;`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

// downgradeCurrentDatabaseToV6ForTest reconstructs the exact parent-release
// shape. Changing user_version alone would leave v7's column and trigger in a
// database that advertises itself as v6, which predecessor validation must
// reject.
func downgradeCurrentDatabaseToV6ForTest(t *testing.T, root string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := db.Exec(`
DROP TRIGGER guard_recovered_launch_state_update;
ALTER TABLE flows DROP COLUMN recovery_generation;
PRAGMA user_version = 6;`); err != nil {
		t.Fatal(err)
	}
	if err := validateSQLiteSchemaVersion(db, 6); err != nil {
		t.Fatalf("test downgrade did not reconstruct exact v6: %v", err)
	}
}

func TestSQLiteV6DatabaseMigratesToV7WithoutRewritingRecoveredCapabilities(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV6DatabaseAt(t, filepath.Join(root, databaseFilename))
	stamp := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        "v6-recovered",
		Title:         "Recovered capability",
		Instructions:  "Test.",
		Status:        StatusPending,
		RepoPath:      filepath.Join(root, "repo"),
		Headless:      true,
		Phases: []FlowPhase{{
			PhaseID: "plan", Title: "Plan", Kind: KindPlan, Status: PhaseReady,
			RecoveredLaunchIDs: []string{"launch-stale"}, CreatedAt: stamp, UpdatedAt: stamp,
		}},
		CreatedAt: stamp,
		UpdatedAt: stamp,
	}
	blob, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO flows(
		flow_id, repo_path, status, updated_at, record, bead_id, epic_id, prepared_at, preparation_nonce
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, projection.flowID, projection.repoPath,
		projection.status, projection.updatedAt, blob, projection.beadID, projection.epicID,
		projection.preparedAt, projection.preparationNonce); err != nil {
		t.Fatal(err)
	}
	if err := validateSQLiteSchemaVersion(db, 6); err != nil {
		t.Fatalf("parent-release fixture is not exact v6: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	predecessor, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := predecessor.Ping(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = predecessor.Close() })

	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("migrate v6 database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 7 {
		t.Fatalf("user_version = %d, want 7", version)
	}
	var migratedBlob []byte
	if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", record.FlowID).Scan(&migratedBlob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(migratedBlob, blob) {
		t.Fatal("v6 to v7 migration rewrote the Flow record")
	}
	if _, err := predecessor.Exec("UPDATE flows SET record = record WHERE flow_id = ?", record.FlowID); err == nil ||
		!strings.Contains(err.Error(), "older approach version cannot remove persisted recovered launch state") {
		t.Fatalf("already-open v6 writer error = %v, want compatibility-trigger refusal", err)
	}
	got, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Phases) != 1 || len(got.Phases[0].RecoveredLaunchIDs) != 1 || got.Phases[0].RecoveredLaunchIDs[0] != "launch-stale" {
		t.Fatalf("recovered launch ids after v7 migration = %#v", got.Phases)
	}
}
