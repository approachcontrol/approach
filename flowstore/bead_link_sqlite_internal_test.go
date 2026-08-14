package flowstore

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteBeadLinkRoundTripsAndValidatesProjections(t *testing.T) {
	for _, tt := range []struct {
		name string
		bead BeadLink
	}{
		{name: "linked child", bead: BeadLink{ID: "child", EpicID: "epic"}},
		{name: "child without epic", bead: BeadLink{ID: "child"}},
		{name: "unlinked"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })

			created, err := store.Create(FlowRecord{
				Title: "Bead link", Instructions: "Persist it.", RepoPath: filepath.Join(root, "repo"), Bead: tt.bead,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			read, err := store.Read(created.FlowID)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if read.Bead != tt.bead {
				t.Fatalf("Read().Bead = %#v, want %#v", read.Bead, tt.bead)
			}
			listed, err := store.List(FlowFilter{})
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(listed) != 1 || listed[0].Bead != tt.bead {
				t.Fatalf("List() = %#v, want Bead %#v", listed, tt.bead)
			}

			backend := store.backend.(*sqliteBackend)
			var beadID, epicID string
			if err := backend.db.QueryRow("SELECT bead_id, epic_id FROM flows WHERE flow_id = ?", created.FlowID).Scan(&beadID, &epicID); err != nil {
				t.Fatalf("read Bead projections: %v", err)
			}
			if beadID != tt.bead.ID || epicID != tt.bead.EpicID {
				t.Fatalf("Bead projections = (%q, %q), want (%q, %q)", beadID, epicID, tt.bead.ID, tt.bead.EpicID)
			}
		})
	}
}

func TestSQLiteBeadProjectionMismatchesAreStrictAndPartial(t *testing.T) {
	for _, column := range []string{"bead_id", "epic_id"} {
		t.Run(column, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			created, err := store.Create(FlowRecord{
				Title: "Projection", Instructions: "Validate it.", RepoPath: filepath.Join(root, "repo"),
				Bead: BeadLink{ID: "child", EpicID: "epic"},
			})
			if err != nil {
				t.Fatal(err)
			}
			backend := store.backend.(*sqliteBackend)
			if _, err := backend.db.Exec("UPDATE flows SET "+column+" = ? WHERE flow_id = ?", "wrong", created.FlowID); err != nil {
				t.Fatalf("corrupt %s: %v", column, err)
			}

			if _, err := store.Read(created.FlowID); err == nil || !strings.Contains(err.Error(), column) || !strings.Contains(err.Error(), "projection") {
				t.Fatalf("Read() error = %v, want %s projection mismatch", err, column)
			}
			listed, err := store.List(FlowFilter{})
			if len(listed) != 0 {
				t.Fatalf("List() records = %#v, want corrupt row omitted", listed)
			}
			var partial *PartialListError
			if !errors.As(err, &partial) || len(partial.Entries) != 1 || partial.Entries[0].FlowID != created.FlowID {
				t.Fatalf("List() error = %#v, want one row-local partial diagnostic", err)
			}
		})
	}
}

func TestSQLiteSettingsOnlySavePreservesBeadProjections(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, err := store.Create(FlowRecord{
		Title: "Settings", Instructions: "Preserve projections.", RepoPath: filepath.Join(root, "repo"),
		Bead: BeadLink{ID: "child", EpicID: "epic"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPhaseAgentSettings(PhaseAgentSettingsUpdate{
		FlowID: created.FlowID, PhaseID: created.Phases[0].PhaseID,
		Settings: PhaseAgentSettings{Agent: "codex"},
	}); err != nil {
		t.Fatalf("SetPhaseAgentSettings() error = %v", err)
	}

	backend := store.backend.(*sqliteBackend)
	var beadID, epicID string
	if err := backend.db.QueryRow("SELECT bead_id, epic_id FROM flows WHERE flow_id = ?", created.FlowID).Scan(&beadID, &epicID); err != nil {
		t.Fatal(err)
	}
	if beadID != "child" || epicID != "epic" {
		t.Fatalf("Bead projections after settings-only save = (%q, %q)", beadID, epicID)
	}
	if _, err := store.Read(created.FlowID); err != nil {
		t.Fatalf("Read() after settings-only save = %v", err)
	}
}

func TestSQLiteMigrationFencesAlreadyOpenPredecessorWriterFromStrippingBeadLink(t *testing.T) {
	root := t.TempDir()
	seedPredecessorDatabase(t, root, 1)
	path := filepath.Join(root, databaseFilename)
	legacyDB, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{
		"mode":    {"rw"},
		"_pragma": {"busy_timeout(5000)"},
		"_txlock": {"immediate"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	legacyDB.SetMaxOpenConns(1)
	legacyDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = legacyDB.Close() })
	var journalMode string
	if err := legacyDB.QueryRow("PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("legacy journal mode = %q, want WAL", journalMode)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, err := store.Create(FlowRecord{
		Title: "Linked after migration", Instructions: "Keep the link.", RepoPath: filepath.Join(root, "repo"),
		Bead: BeadLink{ID: "child", EpicID: "epic"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var before []byte
	if err := legacyDB.QueryRow("SELECT record FROM flows WHERE flow_id = ?", created.FlowID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var predecessorView map[string]any
	if err := json.Unmarshal(before, &predecessorView); err != nil {
		t.Fatal(err)
	}
	delete(predecessorView, "bead")
	stripped, err := json.Marshal(predecessorView)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(
		"UPDATE flows SET updated_at = ?, record = ? WHERE flow_id = ?",
		created.UpdatedAt.Add(time.Minute).Format(time.RFC3339Nano), stripped, created.FlowID,
	); err == nil || !strings.Contains(err.Error(), "older approach version cannot remove persisted Bead link") {
		t.Fatalf("legacy UPDATE error = %v, want persisted Bead-link fence", err)
	}

	var after []byte
	if err := legacyDB.QueryRow("SELECT record FROM flows WHERE flow_id = ?", created.FlowID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("rejected legacy UPDATE changed the stored record")
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() after rejected legacy UPDATE error = %v", err)
	}
	if read.Bead != (BeadLink{ID: "child", EpicID: "epic"}) {
		t.Fatalf("Read().Bead = %#v, want preserved link", read.Bead)
	}
}

func TestSQLiteRejectsMissingOrAlteredBeadCompatibilityTrigger(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  string
		wantErr string
	}{
		{
			name:    "missing",
			mutate:  "DROP TRIGGER guard_linked_flow_record_update",
			wantErr: "incompatible schema objects",
		},
		{
			name: "altered",
			mutate: `
DROP TRIGGER guard_linked_flow_record_update;
CREATE TRIGGER guard_linked_flow_record_update
BEFORE UPDATE OF record ON flows
BEGIN
    SELECT 1;
END;`,
			wantErr: "incompatible Bead compatibility trigger",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rw"}}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tt.mutate); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := NewStore(StoreOptions{Root: root}); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewStore() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func readDatabaseVersionForBeadTest(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var version int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

const predecessorFlowSchemaSQL = `
CREATE TABLE flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL
);
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
`

type predecessorFlowRow struct {
	repoPath  string
	status    string
	updatedAt string
	record    []byte
}

func seedPredecessorDatabase(t *testing.T, root string, version int64) map[string]predecessorFlowRow {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(predecessorFlowSchemaSQL); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	want := map[string]predecessorFlowRow{}
	for i, id := range []string{"pre-bead-a", "pre-bead-b"} {
		stamp := time.Date(2026, time.August, 14, 10+i, 0, 0, i, time.UTC)
		record := FlowRecord{
			SchemaVersion: schemaVersion, FlowID: id, Title: id, Instructions: "Legacy row.",
			Status: StatusPending, RepoPath: filepath.Join(root, "repo", id), Headless: true,
			Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
		}
		data, projection, err := encodeStoredFlow(record)
		if err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(`"bead"`)) {
			_ = db.Close()
			t.Fatalf("predecessor fixture unexpectedly contains Bead field:\n%s", data)
		}
		if _, err := db.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record) VALUES(?, ?, ?, ?, ?)",
			projection.flowID, projection.repoPath, projection.status, projection.updatedAt, data); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		want[id] = predecessorFlowRow{projection.repoPath, projection.status, projection.updatedAt, append([]byte(nil), data...)}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return want
}

func TestSQLiteUpgradesV0AndV1WithoutRewritingRows(t *testing.T) {
	for _, version := range []int64{0, 1} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			root := t.TempDir()
			want := seedPredecessorDatabase(t, root, version)

			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			backend := store.backend.(*sqliteBackend)
			if got := readDatabaseVersionForBeadTest(t, backend.db); got != databaseSchemaVersion {
				t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
			}
			var count int
			if err := backend.db.QueryRow("SELECT count(*) FROM flows").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != len(want) {
				t.Fatalf("row count = %d, want %d", count, len(want))
			}
			for id, before := range want {
				var repoPath, status, updatedAt, beadID, epicID string
				var record []byte
				if err := backend.db.QueryRow("SELECT repo_path, status, updated_at, record, bead_id, epic_id FROM flows WHERE flow_id = ?", id).
					Scan(&repoPath, &status, &updatedAt, &record, &beadID, &epicID); err != nil {
					t.Fatal(err)
				}
				if repoPath != before.repoPath || status != before.status || updatedAt != before.updatedAt || !bytes.Equal(record, before.record) {
					t.Fatalf("row %q changed during migration", id)
				}
				if beadID != "" || epicID != "" {
					t.Fatalf("row %q Bead projections = (%q, %q), want empty", id, beadID, epicID)
				}
				got, err := store.Read(id)
				if err != nil {
					t.Fatalf("Read(%q) error = %v", id, err)
				}
				if got.Bead != (BeadLink{}) {
					t.Fatalf("Read(%q).Bead = %#v", id, got.Bead)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore(reopen) error = %v", err)
			}
			if got := readDatabaseVersionForBeadTest(t, reopened.backend.(*sqliteBackend).db); got != databaseSchemaVersion {
				t.Fatalf("reopened user_version = %d", got)
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLiteRejectsMalformedV0AndV1WithoutPartialUpgrade(t *testing.T) {
	for _, version := range []int64{0, 1} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			root := t.TempDir()
			seedPredecessorDatabase(t, root, version)
			path := filepath.Join(root, databaseFilename)
			db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("DROP INDEX idx_flows_status_updated"); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := NewStore(StoreOptions{Root: root}); err == nil || !strings.Contains(err.Error(), "idx_flows_status_updated") {
				t.Fatalf("NewStore() error = %v, want predecessor schema rejection", err)
			}
			db, err = sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if got := readDatabaseVersionForBeadTest(t, db); got != version {
				t.Fatalf("user_version = %d, want unchanged %d", got, version)
			}
			rows, err := db.Query("PRAGMA table_info(flows)")
			if err != nil {
				t.Fatal(err)
			}
			var columns []string
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
					t.Fatal(err)
				}
				columns = append(columns, name)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.Join(columns, ","), "bead_id") || strings.Contains(strings.Join(columns, ","), "epic_id") {
				t.Fatalf("columns changed after rejected migration: %v", columns)
			}
		})
	}
}

func TestSQLiteRejectsNonExactV0AndV1SchemaWithoutPartialUpgrade(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate string
	}{
		{
			name: "partial index",
			mutate: `
DROP INDEX idx_flows_status_updated;
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC) WHERE status <> '';`,
		},
		{
			name: "unique index",
			mutate: `
DROP INDEX idx_flows_status_updated;
CREATE UNIQUE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);`,
		},
		{
			name: "non-binary collation",
			mutate: `
DROP INDEX idx_flows_status_updated;
CREATE INDEX idx_flows_status_updated ON flows(status COLLATE NOCASE, updated_at DESC, flow_id ASC);`,
		},
		{
			name:   "hidden generated column",
			mutate: `ALTER TABLE flows ADD COLUMN hidden_status TEXT GENERATED ALWAYS AS (status) VIRTUAL;`,
		},
		{
			name:   "unexpected table",
			mutate: `CREATE TABLE unexpected_metadata (value TEXT);`,
		},
		{
			name:   "unexpected table resembling reserved SQLite prefix",
			mutate: `CREATE TABLE sqliteXextra (value TEXT);`,
		},
		{
			name: "unexpected table constraint",
			mutate: `
ALTER TABLE flows RENAME TO original_flows;
CREATE TABLE flows (
    flow_id TEXT PRIMARY KEY,
    repo_path TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status = 'pending'),
    updated_at TEXT NOT NULL,
    record BLOB NOT NULL
);
INSERT INTO flows(flow_id, repo_path, status, updated_at, record)
    SELECT flow_id, repo_path, status, updated_at, record FROM original_flows;
DROP TABLE original_flows;
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, version := range []int64{0, 1} {
				t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
					root := t.TempDir()
					seedPredecessorDatabase(t, root, version)
					path := filepath.Join(root, databaseFilename)
					db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(tt.mutate); err != nil {
						_ = db.Close()
						t.Fatal(err)
					}
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}

					if _, err := NewStore(StoreOptions{Root: root}); err == nil {
						t.Fatal("NewStore() error = nil, want exact predecessor schema rejection")
					}

					db, err = sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if got := readDatabaseVersionForBeadTest(t, db); got != version {
						t.Fatalf("user_version = %d, want unchanged %d", got, version)
					}
					rows, err := db.Query("PRAGMA table_xinfo(flows)")
					if err != nil {
						t.Fatal(err)
					}
					var columns []string
					for rows.Next() {
						var cid, notNull, primaryKey, hidden int
						var name, columnType string
						var defaultValue any
						if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
							t.Fatal(err)
						}
						columns = append(columns, name)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(strings.Join(columns, ","), "bead_id") || strings.Contains(strings.Join(columns, ","), "epic_id") {
						t.Fatalf("columns changed after rejected migration: %v", columns)
					}
				})
			}
		})
	}
}
