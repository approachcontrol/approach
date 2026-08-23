package flowstore

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func createParentReleaseV5Database(t *testing.T, root string) *sql.DB {
	t.Helper()
	return createParentReleaseV5DatabaseAt(t, filepath.Join(root, databaseFilename))
}

func createParentReleaseV5DatabaseAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	schema := flowTableSchemaV5 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + epicProgressionTableSchema + `;
` + flowBeadCompatibilityTrigger + `;
` + flowPreparedCompatibilityTrigger + `;
` + epicProgressionDoneInsertCompatibilityTrigger + `;
` + epicProgressionDoneUpdateCompatibilityTrigger + `;
` + flowProgressionClaimCompatibilityTrigger + `;
PRAGMA user_version = 5;`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

func insertV5Flow(t *testing.T, db *sql.DB, record FlowRecord) []byte {
	t.Helper()
	blob, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record, bead_id, epic_id, prepared_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		projection.flowID, projection.repoPath, projection.status, projection.updatedAt, blob, projection.beadID, projection.epicID, projection.preparedAt); err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestSQLiteParentReleaseV5MigratesThroughV6AndInstallsNonceProjection(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV5Database(t, root)
	stamp := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	marked := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v5-parent", Title: "Parent v5", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Bead: BeadLink{ID: "epic.1", EpicID: "epic"}, ProgressionClaim: true,
		Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
	}
	withNonce := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v5-nonce", Title: "Parent v5 nonce", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Bead: BeadLink{ID: "epic.2", EpicID: "epic"}, ProgressionClaim: true,
		Phases: []FlowPhase{}, PreparationNonce: "0123456789abcdef0123456789abcdef",
		CreatedAt: stamp, UpdatedAt: stamp,
	}
	markedBlob := insertV5Flow(t, db, marked)
	nonceBlob := insertV5Flow(t, db, withNonce)
	if err := validateSQLiteSchemaVersion(db, 5); err != nil {
		t.Fatalf("parent-release fixture is not exact v5: %v", err)
	}
	var nonceColumns int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('flows') WHERE name = 'preparation_nonce'").Scan(&nonceColumns); err != nil || nonceColumns != 0 {
		t.Fatalf("parent-release v5 already has nonce column = %d, err %v", nonceColumns, err)
	}
	var nonceTriggers int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name='guard_preparation_nonce_update'").Scan(&nonceTriggers); err != nil || nonceTriggers != 0 {
		t.Fatalf("parent-release v5 already has nonce trigger = %d, err %v", nonceTriggers, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() rejected parent-release v5 store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != databaseSchemaVersion {
		t.Fatalf("user_version = %d, err %v; want %d", version, err, databaseSchemaVersion)
	}
	if err := validateSQLiteSchema(backend.db); err != nil {
		t.Fatalf("migrated current schema validation failed: %v", err)
	}
	var gotMarked []byte
	var markedNonce string
	if err := backend.db.QueryRow("SELECT record, preparation_nonce FROM flows WHERE flow_id = ?", marked.FlowID).Scan(&gotMarked, &markedNonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotMarked, markedBlob) || markedNonce != "" {
		t.Fatalf("parent-release v5 marked Flow migration = blobEqual %t, nonce %q", bytes.Equal(gotMarked, markedBlob), markedNonce)
	}
	var gotNonce []byte
	var nonce string
	if err := backend.db.QueryRow("SELECT record, preparation_nonce FROM flows WHERE flow_id = ?", withNonce.FlowID).Scan(&gotNonce, &nonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotNonce, nonceBlob) || nonce != withNonce.PreparationNonce {
		t.Fatalf("parent-release v5 nonce Flow migration = blobEqual %t, nonce %q", bytes.Equal(gotNonce, nonceBlob), nonce)
	}
	if got, err := store.Read(marked.FlowID); err != nil || !got.ProgressionClaim || got.PreparationNonce != "" {
		t.Fatalf("marked Flow after migration = marker %t, nonce %q, err %v", got.ProgressionClaim, got.PreparationNonce, err)
	}
	if got, err := store.Read(withNonce.FlowID); err != nil || !got.ProgressionClaim || got.PreparationNonce != withNonce.PreparationNonce {
		t.Fatalf("nonce Flow after migration = marker %t, nonce %q, err %v", got.ProgressionClaim, got.PreparationNonce, err)
	}
}

func TestSQLiteInPlaceV5WithNonceStampsCurrentSchemaWithoutRewritingFlows(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV5Database(t, root)
	if _, err := db.Exec("ALTER TABLE flows ADD COLUMN preparation_nonce TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(flowPreparationNonceCompatibilityTrigger); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 16, 13, 10, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v5-inplace", Title: "In-place v5", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Bead: BeadLink{ID: "epic.1", EpicID: "epic"}, ProgressionClaim: true,
		Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
	}
	blob := insertV5Flow(t, db, record)
	if err := validateSQLiteSchemaVersion(db, 5); err == nil {
		t.Fatal("in-place v5+nonce fixture unexpectedly validated as exact v5")
	}
	if err := validateSQLiteSchemaVersion(db, 6); err != nil {
		t.Fatalf("in-place v5+nonce fixture is not exact v6: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() rejected in-place v5 store that already has the nonce projection: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != databaseSchemaVersion {
		t.Fatalf("user_version = %d, err %v; want %d", version, err, databaseSchemaVersion)
	}
	var gotBlob []byte
	if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", record.FlowID).Scan(&gotBlob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBlob, blob) {
		t.Fatal("in-place v5 Flow blob changed while stamping the current schema")
	}
	if got, err := store.Read(record.FlowID); err != nil || !got.ProgressionClaim {
		t.Fatalf("marked Flow after current schema stamp = marker %t, err %v", got.ProgressionClaim, err)
	}
}

func TestSQLiteParentReleaseV5InterruptedStagePromotesWithoutRebuild(t *testing.T) {
	root := t.TempDir()
	if _, err := secureCanonicalRoot(root); err != nil {
		t.Fatal(err)
	}
	db := createParentReleaseV5DatabaseAt(t, filepath.Join(root, stageFilename))
	if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 8, 16, 13, 20, 0, 0, time.UTC)
	record := FlowRecord{
		SchemaVersion: schemaVersion, FlowID: "v5-staged", Title: "Staged v5", Instructions: "Test.",
		Status: StatusPending, RepoPath: filepath.Join(root, "repo"), Headless: true,
		Bead: BeadLink{ID: "epic.1", EpicID: "epic"}, ProgressionClaim: true,
		Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
	}
	insertV5Flow(t, db, record)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() discarded parent-release v5 stage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.Read(record.FlowID)
	if err != nil || !got.ProgressionClaim {
		t.Fatalf("promoted staged Flow = marker %t, err %v", got.ProgressionClaim, err)
	}
	backend := store.backend.(*sqliteBackend)
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != databaseSchemaVersion {
		t.Fatalf("promoted user_version = %d, err %v; want %d", version, err, databaseSchemaVersion)
	}
	if err := validateSQLiteSchema(backend.db); err != nil {
		t.Fatalf("promoted current schema validation failed: %v", err)
	}
}
