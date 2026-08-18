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

// beadSlotSeedRecord builds a record shaped for slot tests. Phases are set
// explicitly because DeriveStatus ignores FlowRecord.Status except for
// abandoned, so a fixture that only sets Status silently derives pending.
func beadSlotSeedRecord(flowID, repoPath, beadID string, updatedAt time.Time) FlowRecord {
	return FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        flowID,
		Title:         "Seeded",
		Instructions:  "Seeded by test.",
		Status:        StatusPending,
		RepoPath:      repoPath,
		Bead:          BeadLink{ID: beadID},
		CreatedAt:     updatedAt,
		UpdatedAt:     updatedAt,
		Phases:        []FlowPhase{{PhaseID: "plan", Title: "Plan", Status: PhasePending, CreatedAt: updatedAt, UpdatedAt: updatedAt}},
	}
}

func TestBeadLinkedFlowsScopesToRepositoryAndOrdersDeterministically(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoPath := filepath.Join(root, "repo")
	otherRepo := filepath.Join(root, "other")
	base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)

	seed := []FlowRecord{
		beadSlotSeedRecord("20260816T030000Z-older", repoPath, "bead-older", base),
		beadSlotSeedRecord("20260816T030001Z-newer", repoPath, "bead-newer", base.Add(time.Minute)),
		beadSlotSeedRecord("20260816T030002Z-tied", repoPath, "bead-tied", base.Add(time.Minute)),
		beadSlotSeedRecord("20260816T030003Z-other-repo", otherRepo, "bead-elsewhere", base.Add(2*time.Minute)),
	}
	unlinked := beadSlotSeedRecord("20260816T030004Z-unlinked", repoPath, "", base.Add(3*time.Minute))
	unlinked.Bead = BeadLink{}
	seed = append(seed, unlinked)
	for _, record := range seed {
		if err := store.write(record); err != nil {
			t.Fatalf("write(%q) error = %v", record.FlowID, err)
		}
	}

	var got []string
	if _, err := store.backend.update("20260816T030099Z-probe", func(sess flowSession) (FlowRecord, error) {
		flows, err := sess.beadLinkedFlows(repoPath)
		if err != nil {
			return FlowRecord{}, err
		}
		for _, flow := range flows {
			got = append(got, flow.flowID)
		}
		return FlowRecord{}, nil
	}); err != nil {
		t.Fatalf("update() error = %v", err)
	}

	want := []string{
		"20260816T030001Z-newer",
		"20260816T030002Z-tied",
		"20260816T030000Z-older",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("beadLinkedFlows() = %v, want %v", got, want)
	}
}

func TestCreateRefusesUntrimmedStoredBeadIDForTrimmedRequest(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoPath := filepath.Join(root, "repo")
	existing := beadSlotSeedRecord("20260816T030000Z-untrimmed", repoPath, " child ", time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC))
	if err := store.write(existing); err != nil {
		t.Fatalf("write() error = %v", err)
	}

	_, err = store.Create(FlowRecord{
		Title: "Second", Instructions: "Duplicate.", RepoPath: repoPath, Bead: BeadLink{ID: "child"},
	})
	if !IsBeadFlowActive(err) {
		t.Fatalf("Create() error = %v, want IsBeadFlowActive", err)
	}
	conflict, ok := ActiveBeadFlow(err)
	if !ok || conflict.FlowID != existing.FlowID {
		t.Fatalf("ActiveBeadFlow() = (%q, %v), want (%q, true)", conflict.FlowID, ok, existing.FlowID)
	}
}

func TestCreateRefusalNamesMostRecentlyUpdatedOccupyingFlow(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repoPath := filepath.Join(root, "repo")
	base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
	for _, record := range []FlowRecord{
		beadSlotSeedRecord("20260816T030000Z-first", repoPath, "child", base),
		beadSlotSeedRecord("20260816T030001Z-second", repoPath, "child", base.Add(time.Minute)),
		beadSlotSeedRecord("20260816T030002Z-third", repoPath, "child", base.Add(30*time.Second)),
	} {
		if err := store.write(record); err != nil {
			t.Fatalf("write(%q) error = %v", record.FlowID, err)
		}
	}

	_, err = store.Create(FlowRecord{
		Title: "Fourth", Instructions: "Duplicate.", RepoPath: repoPath, Bead: BeadLink{ID: "child"},
	})
	conflict, ok := ActiveBeadFlow(err)
	if !ok {
		t.Fatalf("Create() error = %v, want a bead-slot refusal", err)
	}
	if conflict.FlowID != "20260816T030001Z-second" {
		t.Fatalf("refusal named %q, want the most recently updated %q", conflict.FlowID, "20260816T030001Z-second")
	}
}

// TestBeadSlotGuardHandlesUndecodableRowsByBead pins the two halves of the
// unreadable-row rule. A row that cannot be decoded reports no derived status,
// so the guard cannot tell whether it still holds its Bead. Dropping every such
// row would admit the duplicate Flow and duplicate worktree this guard exists to
// refuse; refusing on every such row would make one unrelated corrupt record a
// repository-wide outage. The bead_id projection stays readable either way, so
// it decides which of the two applies.
func TestBeadSlotGuardHandlesUndecodableRowsByBead(t *testing.T) {
	repoPath := func(root string) string { return filepath.Join(root, "repo") }
	corrupt := func(t *testing.T, store *Store, flowID string) {
		t.Helper()
		backend := store.backend.(*sqliteBackend)
		var data []byte
		if err := backend.db.QueryRow("SELECT record FROM flows WHERE flow_id = ?", flowID).Scan(&data); err != nil {
			t.Fatalf("read record: %v", err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		raw["schema_version"] = json.RawMessage("9999")
		patched, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("encode record: %v", err)
		}
		if _, err := backend.db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", patched, flowID); err != nil {
			t.Fatalf("corrupt record: %v", err)
		}
	}

	t.Run("healthy occupying row still refuses past an unrelated bad row", func(t *testing.T) {
		root := t.TempDir()
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
		// The bad row is newer, so a fatal decode would be hit first.
		if err := store.write(beadSlotSeedRecord("20260816T030000Z-healthy", repoPath(root), "child", base)); err != nil {
			t.Fatalf("write() error = %v", err)
		}
		if err := store.write(beadSlotSeedRecord("20260816T030001Z-bad", repoPath(root), "unrelated", base.Add(time.Minute))); err != nil {
			t.Fatalf("write() error = %v", err)
		}
		corrupt(t, store, "20260816T030001Z-bad")

		_, err = store.Create(FlowRecord{
			Title: "Second", Instructions: "Duplicate.", RepoPath: repoPath(root), Bead: BeadLink{ID: "child"},
		})
		conflict, ok := ActiveBeadFlow(err)
		if !ok {
			t.Fatalf("Create() error = %v, want a bead-slot refusal naming the healthy row", err)
		}
		if conflict.FlowID != "20260816T030000Z-healthy" {
			t.Fatalf("refusal named %q, want %q", conflict.FlowID, "20260816T030000Z-healthy")
		}
	})

	t.Run("an unreadable row for another bead never blocks creation", func(t *testing.T) {
		root := t.TempDir()
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
		if err := store.write(beadSlotSeedRecord("20260816T030001Z-bad", repoPath(root), "unrelated", base)); err != nil {
			t.Fatalf("write() error = %v", err)
		}
		corrupt(t, store, "20260816T030001Z-bad")

		created, err := store.Create(FlowRecord{
			Title: "Second", Instructions: "Allowed.", RepoPath: repoPath(root), Bead: BeadLink{ID: "child"},
		})
		if err != nil {
			t.Fatalf("Create() error = %v, want success when the unreadable row tracks another bead", err)
		}
		if created.FlowID == "" {
			t.Fatal("Create() returned an empty flow id")
		}
	})

	t.Run("an unreadable row for the requested bead refuses and names it", func(t *testing.T) {
		root := t.TempDir()
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
		if err := store.write(beadSlotSeedRecord("20260816T030001Z-bad", repoPath(root), "child", base)); err != nil {
			t.Fatalf("write() error = %v", err)
		}
		corrupt(t, store, "20260816T030001Z-bad")

		_, err = store.Create(FlowRecord{
			Title: "Second", Instructions: "Duplicate.", RepoPath: repoPath(root), Bead: BeadLink{ID: "child"},
		})
		if !IsBeadFlowUnreadable(err) {
			t.Fatalf("Create() error = %v, want IsBeadFlowUnreadable", err)
		}
		if !IsBeadFlowRefusal(err) {
			t.Fatalf("IsBeadFlowRefusal(%v) = false, want true", err)
		}
		if IsBeadFlowActive(err) {
			t.Fatalf("IsBeadFlowActive(%v) = true, want false: no readable Flow can be named", err)
		}
		var unreadable *BeadFlowUnreadableError
		if !errors.As(err, &unreadable) {
			t.Fatalf("Create() error = %v, want a *BeadFlowUnreadableError", err)
		}
		if unreadable.FlowID != "20260816T030001Z-bad" {
			t.Fatalf("refusal named %q, want %q", unreadable.FlowID, "20260816T030001Z-bad")
		}
		if unreadable.BeadID != "child" {
			t.Fatalf("refusal bead = %q, want %q", unreadable.BeadID, "child")
		}

		// The refusal must write nothing, exactly like the readable one.
		var rows int
		if err := store.backend.(*sqliteBackend).db.QueryRow("SELECT COUNT(*) FROM flows").Scan(&rows); err != nil {
			t.Fatalf("count rows: %v", err)
		}
		if rows != 1 {
			t.Fatalf("flows table holds %d rows, want the single unreadable row", rows)
		}
	})

	t.Run("an unreadable untrimmed projection still matches a trimmed request", func(t *testing.T) {
		root := t.TempDir()
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
		if err := store.write(beadSlotSeedRecord("20260816T030001Z-bad", repoPath(root), " child ", base)); err != nil {
			t.Fatalf("write() error = %v", err)
		}
		corrupt(t, store, "20260816T030001Z-bad")

		_, err = store.Create(FlowRecord{
			Title: "Second", Instructions: "Duplicate.", RepoPath: repoPath(root), Bead: BeadLink{ID: "child"},
		})
		if !IsBeadFlowUnreadable(err) {
			t.Fatalf("Create() error = %v, want IsBeadFlowUnreadable", err)
		}
	})
}

// TestCreateAllowsSecondFlowAfterMergedOrAbandoned covers the two terminal
// statuses no public API can construct: Merge.Status is only reachable through
// merge-phase validation, and nothing sets Status abandoned any more. Both are
// still decodable states, so the guard must release the slot for them.
func TestCreateAllowsSecondFlowAfterMergedOrAbandoned(t *testing.T) {
	base := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		mutate     func(record *FlowRecord)
		wantStatus string
	}{
		{
			name: "merged",
			mutate: func(record *FlowRecord) {
				mergedAt := base
				record.Merge = Merge{Status: MergeMerged, Commit: "abc1234", MergedAt: &mergedAt}
			},
			wantStatus: StatusMerged,
		},
		{
			name:       "abandoned",
			mutate:     func(record *FlowRecord) { record.Status = StatusAbandoned },
			wantStatus: StatusAbandoned,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })

			repoPath := filepath.Join(root, "repo")
			existing := beadSlotSeedRecord("20260816T030000Z-terminal", repoPath, "child", base)
			tt.mutate(&existing)
			if got := DeriveStatus(existing); got != tt.wantStatus {
				t.Fatalf("DeriveStatus() = %q, want %q", got, tt.wantStatus)
			}
			if err := store.write(existing); err != nil {
				t.Fatalf("write() error = %v", err)
			}

			if _, err := store.Create(FlowRecord{
				Title: "Follow-up", Instructions: "Legitimate follow-up.", RepoPath: repoPath, Bead: BeadLink{ID: "child"},
			}); err != nil {
				t.Fatalf("Create() error = %v, want success once the first Flow is %s", err, tt.wantStatus)
			}
		})
	}
}
