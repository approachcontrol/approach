package flowstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSQLiteFreshRootUsesSecureWALDatabase(t *testing.T) {
	oldUmask := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(oldUmask) })
	root := filepath.Join(t.TempDir(), "state root #100%?")
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: 1500 * time.Microsecond})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b := store.backend.(*sqliteBackend)
	t.Cleanup(func() { _ = b.db.Close() })

	assertFileModeInternal(t, root, 0o700)
	assertFileModeInternal(t, filepath.Join(root, databaseFilename), 0o600)
	assertFileModeInternal(t, filepath.Join(root, bootstrapLockFilename), 0o600)
	for _, absent := range []string{stageFilename, "flows", "flows.legacy"} {
		if _, err := os.Lstat(filepath.Join(root, absent)); !os.IsNotExist(err) {
			t.Fatalf("fresh root reserved path %q exists or failed unexpectedly: %v", absent, err)
		}
	}

	var mode string
	if err := b.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, err=%v; want wal", mode, err)
	}
	connections := make([]*sql.Conn, 0, 2)
	for i := 0; i < 2; i++ {
		conn, err := b.db.Conn(t.Context())
		if err != nil {
			t.Fatalf("Conn(%d) error = %v", i, err)
		}
		var timeout int
		if err := conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatalf("busy_timeout(%d) error = %v", i, err)
		}
		if timeout != 2 {
			t.Fatalf("busy_timeout(%d) = %d, want rounded-up 2ms", i, timeout)
		}
		connections = append(connections, conn)
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func TestSQLiteMigrationIsOneTimeAndLeavesLegacySourceInPlace(t *testing.T) {
	root := t.TempDir()
	flowID := "20260811T120000Z-legacy"
	legacyPath := writeLegacyFixtureInternal(t, root, "flows", flowID)
	legacyBefore, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	var warnings []string
	restoreWarn := bootstrapWarnf
	bootstrapWarnf = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { bootstrapWarnf = restoreWarn })

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() migration error = %v", err)
	}
	t.Cleanup(func() { _ = store.backend.(*sqliteBackend).db.Close() })
	if _, err := os.Lstat(filepath.Join(root, "flows.legacy")); !os.IsNotExist(err) {
		t.Fatalf("migration moved production files to flows.legacy: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "left in place and is no longer read") {
		t.Fatalf("migration warnings = %#v, want one retained-source warning", warnings)
	}
	legacyAfter, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("read retained legacy source: %v", err)
	}
	if string(legacyAfter) != string(legacyBefore) {
		t.Fatal("migration mutated the legacy source it left in place")
	}
	first, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if err := os.WriteFile(legacyPath, []byte(`{"schema_version":1,"flow_id":"changed"}`), 0o600); err != nil {
		t.Fatalf("mutate retained legacy fixture: %v", err)
	}
	reopened, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore(reopen) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.backend.(*sqliteBackend).db.Close() })
	second, err := reopened.Read(flowID)
	if err != nil {
		t.Fatalf("Read(reopen) error = %v", err)
	}
	if second.Title != first.Title || second.FlowID != first.FlowID {
		t.Fatalf("reopen read retained legacy changes: first=%#v second=%#v", first, second)
	}
}

func TestSQLiteCutoverRecoveryMatrix(t *testing.T) {
	t.Run("flows and tombstone conflict", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "flows"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "flows.legacy"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(StoreOptions{Root: root}); err == nil || !strings.Contains(err.Error(), "both flows and flows.legacy") {
			t.Fatalf("NewStore() error = %v, want conflict", err)
		}
	})

	t.Run("tombstone without stage is preserved", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "flows.legacy"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(StoreOptions{Root: root}); err == nil || !strings.Contains(err.Error(), "without a staged database") {
			t.Fatalf("NewStore() error = %v, want incomplete cutover", err)
		}
		if _, err := os.Stat(filepath.Join(root, "flows.legacy")); err != nil {
			t.Fatalf("tombstone was not preserved: %v", err)
		}
	})

	t.Run("interrupted fresh stage promotes only zero rows", func(t *testing.T) {
		root := t.TempDir()
		if _, err := secureCanonicalRoot(root); err != nil {
			t.Fatal(err)
		}
		if err := buildStagedDatabase(filepath.Join(root, stageFilename), nil); err != nil {
			t.Fatal(err)
		}
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.backend.(*sqliteBackend).db.Close() })
		if _, err := os.Stat(filepath.Join(root, databaseFilename)); err != nil {
			t.Fatalf("stage was not promoted: %v", err)
		}
	})

	// This build never creates flows.legacy/ — it leaves flows/ where it is. The
	// directory can only come from a root migrated by a build that did rename,
	// so this covers that inherited state, not a state the current cutover
	// produces. Naming it "post-rename" previously implied otherwise.
	t.Run("stage left beside an inherited flows.legacy tombstone is promoted", func(t *testing.T) {
		root := t.TempDir()
		flowID := "20260811T120000Z-resume"
		writeLegacyFixtureInternal(t, root, "flows.legacy", flowID)
		imported, err := readAndCanonicalizeLegacy(root, "flows.legacy", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := buildStagedDatabase(filepath.Join(root, stageFilename), imported.records); err != nil {
			t.Fatal(err)
		}
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.backend.(*sqliteBackend).db.Close() })
		if _, err := store.Read(flowID); err != nil {
			t.Fatalf("Read(resumed) error = %v", err)
		}
	})
}

func TestSQLiteAuthoritativeDatabaseWinsAndCorruptionIsNotNotFound(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	b := store.backend.(*sqliteBackend)
	record, err := store.Create(FlowRecord{FlowID: "authority", Title: "Authority", Instructions: "Use SQLite.", RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "flows")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("authoritative reopen inspected ignored legacy symlink: %v", err)
	}
	t.Cleanup(func() { _ = reopened.backend.(*sqliteBackend).db.Close() })
	if _, err := reopened.Read(record.FlowID); err != nil {
		t.Fatalf("Read(authoritative) error = %v", err)
	}

	if _, err := b.db.Exec("UPDATE flows SET repo_path = ? WHERE flow_id = ?", "/wrong/projection", record.FlowID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(record.FlowID); err == nil || IsNotFound(err) || !strings.Contains(err.Error(), "projection") {
		t.Fatalf("Read(projection mismatch) error = %v", err)
	}
	if _, err := store.List(FlowFilter{}); err == nil || !strings.Contains(err.Error(), "projection") {
		t.Fatalf("List(projection mismatch) error = %v", err)
	}
	if _, err := b.db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", []byte("{"), record.FlowID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(record.FlowID); err == nil || IsNotFound(err) {
		t.Fatalf("Read(corrupt) error = %v, want non-not-found corruption", err)
	}
	if _, err := store.List(FlowFilter{}); err == nil {
		t.Fatal("List() error = nil for malformed authoritative row")
	}
}

func TestSQLiteRejectsUnsafeFixedPathsAndCorruptDatabaseBeforePresets(t *testing.T) {
	t.Run("bootstrap lock symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, bootstrapLockFilename)); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(StoreOptions{Root: root}); err == nil || !strings.Contains(err.Error(), "without following symlinks") {
			t.Fatalf("NewStore() error = %v, want no-follow rejection", err)
		}
	})

	t.Run("zero database precedes invalid preset", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, databaseFilename), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewStore(StoreOptions{Root: root, Presets: []Preset{{Name: "bad"}}})
		if err == nil || !strings.Contains(err.Error(), "database") || strings.Contains(err.Error(), "preset") {
			t.Fatalf("NewStore() error = %v, want authoritative database error precedence", err)
		}
		info, statErr := os.Stat(filepath.Join(root, databaseFilename))
		if statErr != nil || info.Size() != 0 {
			t.Fatalf("zero database was mutated: info=%v err=%v", info, statErr)
		}
	})

	t.Run("invalid preset closes database and releases bootstrap lease", func(t *testing.T) {
		root := t.TempDir()
		initial, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if err := initial.backend.(*sqliteBackend).db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(StoreOptions{Root: root, LockTimeout: 20 * time.Millisecond, Presets: []Preset{{Name: "bad"}}}); err == nil {
			t.Fatal("NewStore(invalid preset) error = nil")
		}
		reopened, err := NewStore(StoreOptions{Root: root, LockTimeout: 20 * time.Millisecond})
		if err != nil {
			t.Fatalf("NewStore(after invalid preset) error = %v", err)
		}
		_ = reopened.backend.(*sqliteBackend).db.Close()
	})
}

func TestSQLiteAcceptsSymlinkedRootAndReservedURICharacters(t *testing.T) {
	realRoot := filepath.Join(t.TempDir(), "real #100%? root")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	flowID := "20260811T120000Z-uri"
	writeLegacyFixtureInternal(t, realRoot, "flows", flowID)
	store, err := NewStore(StoreOptions{Root: link})
	if err != nil {
		t.Fatalf("NewStore(symlink root) error = %v", err)
	}
	t.Cleanup(func() { _ = store.backend.(*sqliteBackend).db.Close() })
	if _, err := store.Read(flowID); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if store.backend.(*sqliteBackend).root != canonicalRoot {
		t.Fatalf("backend root = %q, want canonical %q", store.backend.(*sqliteBackend).root, canonicalRoot)
	}
}

func TestSQLiteSerializesIndependentProcessWriters(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	b := store.backend.(*sqliteBackend)
	t.Cleanup(func() { _ = b.db.Close() })
	record, err := store.Create(FlowRecord{FlowID: "process-contention", Title: "Contention", Instructions: "Serialize writers.", RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := b.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	runHelper := func(mode string) []byte {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteWriterContentionHelper$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(),
			"APPROACH_SQLITE_CONTENTION_ROOT="+root,
			"APPROACH_SQLITE_CONTENTION_FLOW="+record.FlowID,
			"APPROACH_SQLITE_CONTENTION_MODE="+mode,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("contention helper (%s) error = %v\n%s", mode, err, output)
		}
		return output
	}
	if output := runHelper("blocked"); !strings.Contains(strings.ToLower(string(output)), "locked") {
		t.Fatalf("blocked helper output = %s, want SQLite locked error", output)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if output := runHelper("success"); !strings.Contains(string(output), "writer succeeded") {
		t.Fatalf("successful helper output = %s", output)
	}
}

func TestSQLiteWriterContentionHelper(t *testing.T) {
	root := os.Getenv("APPROACH_SQLITE_CONTENTION_ROOT")
	if root == "" {
		t.Skip("helper process only")
	}
	flowID := os.Getenv("APPROACH_SQLITE_CONTENTION_FLOW")
	mode := os.Getenv("APPROACH_SQLITE_CONTENTION_MODE")
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: 25 * time.Millisecond})
	if err != nil {
		if mode == "blocked" && strings.Contains(strings.ToLower(err.Error()), "locked") {
			t.Logf("database locked before writer callback: %v", err)
			return
		}
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.backend.(*sqliteBackend).db.Close()
	_, err = store.SetPhase(PhaseUpdate{FlowID: flowID, PhaseID: "plan", Status: PhaseRunning})
	if mode == "blocked" {
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
			t.Fatalf("SetPhase() error = %v, want locked", err)
		}
		t.Logf("database locked before callback: %v", err)
		return
	}
	if err != nil {
		t.Fatalf("SetPhase() after release error = %v", err)
	}
	t.Log("writer succeeded")
}

func writeLegacyFixtureInternal(t *testing.T, root, collection, flowID string) string {
	t.Helper()
	dir := filepath.Join(root, collection, flowID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Legacy fixture",
  "instructions": "Migrate once.",
  "status": "pending",
  "repo_path": "/tmp/repo",
  "phases": [
    {"phase_id":"plan","title":"Plan","status":"ready","order":1,"created_at":"2026-08-11T12:00:00Z","updated_at":"2026-08-11T12:00:00Z"}
  ],
  "created_at": "2026-08-11T12:00:00Z",
  "updated_at": "2026-08-11T12:00:00Z"
}`
	path := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileModeInternal(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
