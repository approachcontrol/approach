package flowstore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteV7MigrationBackfillsAndFencesUntrackedOwner(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, databaseFilename)
	db := createParentReleaseV7DatabaseAt(t, path)
	stamp := time.Date(2026, 8, 25, 20, 0, 0, 0, time.UTC)
	record := FlowRecord{SchemaVersion: schemaVersion, FlowID: "v7-owner", Title: "owner", Instructions: "test", Status: StatusInProgress, RepoPath: filepath.Join(root, "repo"), Headless: true, Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
		UntrackedOwner: &UntrackedOwner{LaunchID: "launch-live", Role: UntrackedOwnerAutofix, State: UntrackedOwnerLive, Transport: UntrackedOwnerTransport{Kind: UntrackedTransportRepoTmux, Session: "repo", Window: "launch-live"}, ReservedAt: stamp, ActivatedAt: stamp}}
	blob, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO flows(flow_id,repo_path,status,updated_at,record,bead_id,epic_id,prepared_at,preparation_nonce) VALUES(?,?,?,?,?,?,?,?,?)`, projection.flowID, projection.repoPath, projection.status, projection.updatedAt, blob, projection.beadID, projection.epicID, projection.preparedAt, projection.preparationNonce); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	older, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = older.Close() })
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var projected string
	if err := store.backend.(*sqliteBackend).db.QueryRow("SELECT untracked_owner_launch_id FROM flows WHERE flow_id=?", record.FlowID).Scan(&projected); err != nil || projected != "launch-live" {
		t.Fatalf("projection=%q err=%v", projected, err)
	}
	withoutOwner := record
	withoutOwner.UntrackedOwner = nil
	olderBlob, _, err := encodeStoredFlow(withoutOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := older.Exec("UPDATE flows SET record=? WHERE flow_id=?", olderBlob, record.FlowID); err == nil || !strings.Contains(err.Error(), "cannot remove persisted phase-untracked owner") {
		t.Fatalf("older writer error=%v", err)
	}
	if _, err := older.Exec("DELETE FROM flows WHERE flow_id=?", record.FlowID); err == nil || !strings.Contains(err.Error(), "cannot delete a Flow with a persisted phase-untracked owner") {
		t.Fatalf("older writer delete error=%v", err)
	}
}
