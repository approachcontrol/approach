package flowstore

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func createParentReleaseV4Database(t *testing.T, root string) *sql.DB {
	t.Helper()
	return createParentReleaseV4DatabaseAt(t, filepath.Join(root, databaseFilename))
}

func createParentReleaseV4DatabaseAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	schema := flowTableSchemaV4 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + epicProgressionTableSchema + `;
` + flowBeadCompatibilityTrigger + `;
` + flowPreparedCompatibilityTrigger + `;
` + epicProgressionDoneInsertCompatibilityTrigger + `;
` + epicProgressionDoneUpdateCompatibilityTrigger + `;
PRAGMA user_version = 4;`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

func TestSQLiteParentReleaseV4MigratesToV5AndInstallsClaimTrigger(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV4Database(t, root)
	stamp := time.Date(2026, 8, 15, 5, 0, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v4-parent", Title: "Parent v4", Instructions: "Test.",
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
	if err := validateSQLiteSchemaVersion(db, 4); err != nil {
		t.Fatalf("parent-release fixture is not exact v4: %v", err)
	}
	var claimTriggers int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name='guard_progression_claim_record_update'").Scan(&claimTriggers); err != nil || claimTriggers != 0 {
		t.Fatalf("parent-release v4 already has claim trigger = %d, err %v", claimTriggers, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() rejected parent-release v4 store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatalf("user_version = %d, err %v; want 5", version, err)
	}
	if err := validateSQLiteSchema(backend.db); err != nil {
		t.Fatalf("migrated v5 schema validation failed: %v", err)
	}
	var gotBlob []byte
	if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Fatal("parent-release v4 Flow blob changed during v5 migration")
	}
	if got, err := store.Read(record.FlowID); err != nil || !got.ProgressionClaim {
		t.Fatalf("marked Flow after v5 migration = marker %t, err %v", got.ProgressionClaim, err)
	}
}

func TestSQLiteInPlaceV4WithClaimTriggerStampsV5WithoutRewritingFlows(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV4Database(t, root)
	if _, err := db.Exec(flowProgressionClaimCompatibilityTrigger); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 15, 5, 10, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v4-inplace", Title: "In-place v4", Instructions: "Test.",
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
		t.Fatalf("NewStore() rejected in-place v4 store that already has the claim trigger: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatalf("user_version = %d, err %v; want 5", version, err)
	}
	var gotBlob []byte
	if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Fatal("in-place v4 Flow blob changed while stamping v5")
	}
}

func TestSQLiteParentReleaseV4InterruptedStagePromotesWithoutRebuild(t *testing.T) {
	root := t.TempDir()
	if _, err := secureCanonicalRoot(root); err != nil {
		t.Fatal(err)
	}
	db := createParentReleaseV4DatabaseAt(t, filepath.Join(root, stageFilename))
	if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 15, 5, 20, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v4-staged", Title: "Staged v4", Instructions: "Test.",
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
		t.Fatalf("NewStore() discarded parent-release v4 stage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.Read(record.FlowID)
	if err != nil || !got.ProgressionClaim {
		t.Fatalf("promoted staged Flow = marker %t, err %v", got.ProgressionClaim, err)
	}
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatalf("promoted user_version = %d, err %v; want 5", version, err)
	}
}
