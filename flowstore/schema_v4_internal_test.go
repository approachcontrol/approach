package flowstore

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func createV3Database(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	schema := flowTableSchemaV3 + `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
` + epicProgressionTableSchema + `;
` + flowBeadCompatibilityTrigger + `;
` + flowPreparedCompatibilityTrigger + `;
PRAGMA user_version = 3;`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

func legacyV3EpicProgressionBlob(t *testing.T, record EpicProgression) []byte {
	t.Helper()
	data, _, err := encodeEpicProgression(record)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.Replace(string(data), "  \"done\": false,\n", "", 1))
}

func TestSQLiteV3ToV4MigratesLegacyProgressionsConservatively(t *testing.T) {
	root := t.TempDir()
	db := createV3Database(t, root)
	stamp := time.Date(2026, 8, 14, 18, 0, 0, 123000000, time.UTC)
	records := []EpicProgression{
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "active", Enabled: true, CreatedAt: stamp, UpdatedAt: stamp},
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "off", CreatedAt: stamp, UpdatedAt: stamp.Add(time.Minute)},
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "halted", Halt: &EpicProgressionHalt{ChildBeadID: "halted.1", Status: StatusBlocked, Message: "blocked"}, CreatedAt: stamp, UpdatedAt: stamp.Add(2 * time.Minute)},
	}
	original := make(map[string][]byte, len(records))
	for _, record := range records {
		blob := legacyV3EpicProgressionBlob(t, record)
		original[record.EpicID] = append([]byte(nil), blob...)
		updatedAt, _ := formatStorageTime(record.UpdatedAt)
		if _, err := db.Exec("INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, ?, ?, ?)", record.RepoPath, record.EpicID, boolToSQLite(record.Enabled), updatedAt, blob); err != nil {
			t.Fatal(err)
		}
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
	var version int
	if err := backend.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 4 {
		t.Fatalf("user_version = %d, err %v", version, err)
	}
	for _, want := range records {
		var repoPath, epicID, updatedAt string
		var enabled int
		var gotBlob []byte
		if err := backend.db.QueryRow("SELECT repo_path, epic_id, enabled, updated_at, record FROM epic_progressions WHERE repo_path=? AND epic_id=?", want.RepoPath, want.EpicID).Scan(&repoPath, &epicID, &enabled, &updatedAt, &gotBlob); err != nil {
			t.Fatal(err)
		}
		wantBlob, wantUpdatedAt, err := encodeEpicProgression(want)
		if err != nil {
			t.Fatal(err)
		}
		if repoPath != want.RepoPath || epicID != want.EpicID || enabled != boolToSQLite(want.Enabled) || updatedAt != wantUpdatedAt || !bytes.Equal(gotBlob, wantBlob) {
			t.Fatalf("migrated %s projections/blob changed: repo=%q epic=%q enabled=%d updated=%q blob=%s", want.EpicID, repoPath, epicID, enabled, updatedAt, gotBlob)
		}
		legacy, err := decodeLegacyV3EpicProgression(repoPath, epicID, enabled, updatedAt, original[want.EpicID])
		if err != nil || !reflect.DeepEqual(legacy, want) {
			t.Fatalf("legacy %s = %#v, err %v; want %#v", want.EpicID, legacy, err, want)
		}
		got, found, err := store.ReadEpicProgression(EpicProgressionKey{RepoPath: want.RepoPath, EpicID: want.EpicID})
		if err != nil || !found || got.Done || !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadEpicProgression(%s) = %#v, found %t, err %v", want.EpicID, got, found, err)
		}
	}
}

func TestSQLiteV3ToV4MalformedProgressionRollsBackEveryRewrite(t *testing.T) {
	root := t.TempDir()
	db := createV3Database(t, root)
	stamp := time.Date(2026, 8, 14, 19, 0, 0, 0, time.UTC)
	valid := EpicProgression{SchemaVersion: 1, RepoPath: "/repo", EpicID: "a-valid", Enabled: true, CreatedAt: stamp, UpdatedAt: stamp}
	validBlob := legacyV3EpicProgressionBlob(t, valid)
	updatedAt, _ := formatStorageTime(stamp)
	if _, err := db.Exec("INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 1, ?, ?)", valid.RepoPath, valid.EpicID, updatedAt, validBlob); err != nil {
		t.Fatal(err)
	}
	badBlob := []byte(`{"schema_version":1,"repo_path":"/repo","epic_id":"z-bad","created_at":"2026-08-14T19:00:00Z","updated_at":"2026-08-14T19:00:00Z"}`)
	if _, err := db.Exec("INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES('/repo', 'z-bad', 0, ?, ?)", updatedAt, badBlob); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := NewStore(StoreOptions{Root: root}); err == nil {
		t.Fatal("NewStore() accepted malformed v3 progression")
	}
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("user_version after rollback = %d, err %v", version, err)
	}
	if err := validateSQLiteSchemaVersion(db, 3); err != nil {
		t.Fatalf("rolled-back database is not exact v3: %v", err)
	}
	for epicID, want := range map[string][]byte{"a-valid": validBlob, "z-bad": badBlob} {
		var got []byte
		if err := db.QueryRow("SELECT record FROM epic_progressions WHERE epic_id=?", epicID).Scan(&got); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("row %s after rollback = %s, err %v; want %s", epicID, got, err, want)
		}
	}
	var triggers int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name GLOB 'guard_epic_progression_done*'").Scan(&triggers); err != nil || triggers != 0 {
		t.Fatalf("v4 triggers after rollback = %d, err %v", triggers, err)
	}
}

func TestEpicProgressionDoneCompatibilityTriggersRejectLegacyWrites(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	stamp := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	record := EpicProgression{SchemaVersion: 1, RepoPath: "/repo", EpicID: "existing", CreatedAt: stamp, UpdatedAt: stamp}
	valid, updatedAt, err := encodeEpicProgression(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.db.Exec("INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)", record.RepoPath, record.EpicID, updatedAt, valid); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		name string
		from string
		to   string
	}{
		{name: "missing", from: "  \"done\": false,\n", to: ""},
		{name: "null", from: "\"done\": false", to: "\"done\": null"},
		{name: "string", from: "\"done\": false", to: "\"done\": \"false\""},
		{name: "number", from: "\"done\": false", to: "\"done\": 0"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			invalid := []byte(strings.Replace(string(valid), mutation.from, mutation.to, 1))
			_, insertErr := backend.db.Exec("INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES('/repo', ?, 0, ?, ?)", "insert-"+mutation.name, updatedAt, invalid)
			if insertErr == nil {
				t.Fatal("legacy-shaped insert succeeded")
			}
			_, updateErr := backend.db.Exec("UPDATE epic_progressions SET record=? WHERE repo_path=? AND epic_id=?", invalid, record.RepoPath, record.EpicID)
			if updateErr == nil {
				t.Fatal("legacy-shaped update succeeded")
			}
			_, upsertErr := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)
ON CONFLICT(repo_path, epic_id) DO UPDATE SET record=excluded.record`, record.RepoPath, record.EpicID, updatedAt, invalid)
			if upsertErr == nil {
				t.Fatal("legacy-shaped upsert succeeded")
			}
		})
	}
	if err := validateSQLiteSchema(backend.db); err != nil {
		t.Fatalf("fresh v4 schema validation failed: %v", err)
	}
	var count int
	if err := backend.db.QueryRow("SELECT count(*) FROM epic_progressions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("trigger tests changed row count = %d, err %v", count, err)
	}
	if got, found, err := store.ReadEpicProgression(EpicProgressionKey{RepoPath: record.RepoPath, EpicID: record.EpicID}); err != nil || !found || !reflect.DeepEqual(got, record) {
		t.Fatalf("existing progression changed = %#v, found %t, err %v", got, found, err)
	}
}

func TestSQLiteV0ThroughV2MigrateDirectlyToV4(t *testing.T) {
	for _, version := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			root := t.TempDir()
			db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rwc"}}))
			if err != nil {
				t.Fatal(err)
			}
			schema := flowTableSchemaV1
			if version == 2 {
				schema = flowTableSchemaV2
			}
			schema += `;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);`
			if version == 2 {
				schema += flowBeadCompatibilityTrigger + `;`
			}
			if _, err := db.Exec(schema + fmt.Sprintf(" PRAGMA user_version = %d;", version)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			backend := store.backend.(*sqliteBackend)
			var got int
			if err := backend.db.QueryRow("PRAGMA user_version").Scan(&got); err != nil || got != 4 {
				t.Fatalf("user_version = %d, err %v", got, err)
			}
			if err := validateSQLiteSchema(backend.db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
