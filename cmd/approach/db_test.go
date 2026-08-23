package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/dblease"
)

func dbDeps(t *testing.T, stdout, stderr *bytes.Buffer, env map[string]string) runDeps {
	t.Helper()
	return noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		getenv: func(key string) string {
			return env[key]
		},
		stdout: stdout,
		stderr: stderr,
	})
}

func seedCurrentRoot(t *testing.T, root string) {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Role: flowstore.RoleMigrator})
	if err != nil {
		t.Fatalf("seed root: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunDBInspectEmitsTheDocumentedJSON(t *testing.T) {
	root := t.TempDir()
	seedCurrentRoot(t, root)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "db", "inspect", "--json", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if report["tier"] != "open" || report["readable"] != true {
		t.Fatalf("tier = %v readable = %v", report["tier"], report["readable"])
	}
	if got := report["user_version"].(float64); int(got) != flowstore.DatabaseSchemaVersion() {
		t.Fatalf("user_version = %v", got)
	}
}

// Acceptance criterion 3, at the command surface: `db inspect` must report a
// future schema rather than refusing on it. The compatibility refusal belongs
// to the commands that OPEN the store, which is why the second half runs
// `flow list` against the same root.
func TestRunDBInspectReportsAFutureSchemaWhileFlowListRefusesIt(t *testing.T) {
	root := t.TempDir()
	seedCurrentRoot(t, root)
	setTestDatabaseVersion(t, filepath.Join(root, "approach.db"), 99)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "db", "inspect", "--json", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatalf("db inspect refused a future schema: %v", err)
	}
	if !strings.Contains(stdout.String(), `"tier": "open"`) ||
		!strings.Contains(stdout.String(), `"user_version": 99`) {
		t.Fatalf("db inspect output = %s", stdout.String())
	}

	var listOut, listErr bytes.Buffer
	err := run([]string{"approach", "flow", "list", "--json", "--state-root", root},
		dbDeps(t, &listOut, &listErr, nil))
	if err == nil {
		t.Fatal("flow list opened a database from a newer build")
	}
	if !strings.Contains(err.Error(), "newer version of approach") ||
		!strings.Contains(err.Error(), "upgrade approach") {
		t.Fatalf("flow list error = %v", err)
	}
}

func TestRunDBInspectPrintsAHumanReportWithoutJSON(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "db", "inspect", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	for _, want := range []string{"tier:      missing", "reason:    no flow database", "next:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output %q is missing %q", stdout.String(), want)
		}
	}
}

func TestRunDBMigrateAdvancesTheSchemaAndWritesABackupAndSidecar(t *testing.T) {
	root := t.TempDir()
	seedCurrentRoot(t, root)
	setTestDatabaseVersion(t, filepath.Join(root, "approach.db"), flowstore.DatabaseSchemaVersion()-1)

	var stdout, stderr bytes.Buffer
	backupDir := filepath.Join(t.TempDir(), "backups")
	if err := run([]string{"approach", "db", "migrate", "--state-root", root, "--backup-dir", backupDir},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatalf("db migrate returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("is at schema %d", flowstore.DatabaseSchemaVersion())) {
		t.Fatalf("db migrate output = %q", stdout.String())
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("--backup-dir was not honoured: %v", err)
	}
	// The source-root fingerprint that keeps a shared --backup-dir's roots apart
	// sits between the migrated file and its schema, so this is not one prefix.
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "approach.db-") ||
		!strings.Contains(entries[0].Name(), fmt.Sprintf("-v%d-", flowstore.DatabaseSchemaVersion()-1)) {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("backups = %v, want one named for the migrated file and its schema", names)
	}
	// The default location stays empty when --backup-dir names another.
	if _, err := os.Stat(filepath.Join(root, "backups")); !os.IsNotExist(err) {
		t.Fatalf("db migrate also wrote to the default backup directory: %v", err)
	}

	// And the migration stamped its provenance.
	var inspectOut, inspectErr bytes.Buffer
	if err := run([]string{"approach", "db", "inspect", "--json", "--state-root", root},
		dbDeps(t, &inspectOut, &inspectErr, nil)); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(inspectOut.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["generation_id"] == nil {
		t.Fatalf("db inspect reports no generation after a migration: %s", inspectOut.String())
	}
	if got := report["min_writer_generation"]; got == nil || int(got.(float64)) != flowstore.DatabaseSchemaVersion() {
		t.Fatalf("min_writer_generation = %v", got)
	}
}

func TestRunDBMigrateHonorsTheDevLiveAcknowledgement(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := filepath.Join(stateHome, "approach", "sessions", "v1")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	seedParentReleaseV4Database(t, root)

	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "db", "migrate", "--state-root", root}, dbDeps(t, &stdout, &stderr, nil))
	if err == nil || !strings.Contains(err.Error(), "APPROACH_ALLOW_DEV_LIVE_MIGRATION") {
		t.Fatalf("db migrate error = %v, want the dev-live refusal", err)
	}

	stdout.Reset()
	stderr.Reset()
	env := map[string]string{"APPROACH_ALLOW_DEV_LIVE_MIGRATION": "1"}
	err = run([]string{"approach", "db", "migrate", "--state-root", root}, dbDeps(t, &stdout, &stderr, env))
	if err != nil && strings.Contains(err.Error(), "APPROACH_ALLOW_DEV_LIVE_MIGRATION") {
		t.Fatalf("the acknowledgement did not reach db migrate: %v", err)
	}
}

// A positional token must never reach the store open. `db migrate` holds
// RoleMigrator, and Go stops flag processing at the first non-flag argument, so
// `approach db migrate help --state-root <scratch>` would otherwise migrate the
// DEFAULT root while the operator was asking for help about a scratch one.
func TestRunDBRejectsPositionalArgumentsBeforeOpeningTheStore(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	defaultRoot := filepath.Join(stateHome, "approach", "sessions", "v1")

	for _, command := range []string{"inspect", "migrate", "restore"} {
		t.Run(command+" help", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := run([]string{"approach", "db", command, "help"},
				dbDeps(t, &stdout, &stderr, nil)); err != nil {
				t.Fatalf("db %s help returned an error: %v", command, err)
			}
			if !strings.Contains(stdout.String(), "approach db <inspect|migrate|restore>") {
				t.Fatalf("db %s help = %s", command, stdout.String())
			}
		})

		t.Run(command+" stray argument", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run([]string{"approach", "db", command, "oops", "--state-root", t.TempDir()},
				dbDeps(t, &stdout, &stderr, nil))
			if err == nil || !strings.Contains(err.Error(), `unexpected argument "oops"`) {
				t.Fatalf("db %s error = %v, want the unexpected-argument refusal", command, err)
			}
		})
	}

	// Neither spelling may have created — let alone migrated — the default root.
	if _, err := os.Stat(defaultRoot); !os.IsNotExist(err) {
		t.Fatalf("db touched the default root %q: stat err = %v", defaultRoot, err)
	}
}

func TestDBIsRegisteredInTheDispatchAndHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "--help"}, dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "db            Inspect, migrate, and restore the flow database.") {
		t.Fatalf("main help does not list db:\n%s", stdout.String())
	}

	stdout.Reset()
	err := run([]string{"approach", "dbb"}, dbDeps(t, &stdout, &stderr, nil))
	if err == nil || !strings.Contains(err.Error(), `did you mean "db"`) {
		t.Fatalf("unknown-command suggestion = %v", err)
	}

	stdout.Reset()
	if err := run([]string{"approach", "db", "--help"}, dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "approach db <inspect|migrate|restore>") {
		t.Fatalf("db help = %s", stdout.String())
	}
}

func setTestDatabaseVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if version == flowstore.DatabaseSchemaVersion()-1 {
		// Reconstruct the exact v6 parent-release shape. Stamping a v7 database
		// down while retaining its column and trigger must fail strict
		// predecessor validation.
		if _, err := db.Exec(`
DROP TRIGGER guard_recovered_launch_state_update;
ALTER TABLE flows DROP COLUMN recovery_generation;`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = " + itoa(version)); err != nil {
		t.Fatal(err)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// C8's contract at the command surface: a schema-compatibility failure in the
// hook's Flow attachment is reported on stderr and the command still exits
// zero. agent-skills/approach-flow tells every agent that a non-zero exit is a
// persistence failure, and this is not one — the session record was captured.
func TestRunSessionHookWarnsOnStderrAndStillExitsZero(t *testing.T) {
	root := t.TempDir()
	seedCurrentRoot(t, root)
	setTestDatabaseVersion(t, filepath.Join(root, "approach.db"), 99)

	var stdout, stderr bytes.Buffer
	deps := dbDeps(t, &stdout, &stderr, map[string]string{
		"APPROACH_FLOW_ID":       "20260607T120000Z-hook-warning",
		"APPROACH_FLOW_PHASE_ID": "implementation",
	})
	deps.stdin = strings.NewReader(`{"session_id":"codex-hook-warning","cwd":"/repo/worktree"}`)
	if err := run([]string{"approach", "session-hook", "--provider", "codex", "--state-root", root}, deps); err != nil {
		t.Fatalf("session-hook exited non-zero on a schema warning: %v", err)
	}
	if !strings.Contains(stderr.String(), "newer version of approach") {
		t.Fatalf("stderr = %q, want the compatibility warning", stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not attach this session to Flow") {
		t.Fatalf("stderr = %q, want the attachment failure named", stderr.String())
	}
}

// TestRunDBMigrateRefusesWhileLiveOwnersHoldTheDatabase is the acceptance
// criterion at the command surface: the refusal an operator actually sees names
// every process they have to close, not just the first one found.
func TestRunDBMigrateRefusesWhileLiveOwnersHoldTheDatabase(t *testing.T) {
	root := t.TempDir()
	seedCurrentRoot(t, root)
	setTestDatabaseVersion(t, filepath.Join(root, "approach.db"), flowstore.DatabaseSchemaVersion()-1)

	first := holdOwnerLease(t, root, flowstore.DatabaseSchemaVersion()-1, "v0.10.2")
	second := holdOwnerLease(t, root, flowstore.DatabaseSchemaVersion()-1, "v0.10.1")
	_ = first
	_ = second

	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "db", "migrate", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil))
	if err == nil {
		t.Fatal("db migrate proceeded with two live incompatible owners")
	}
	if !flowstore.IsMigrationBlockedByOwners(err) {
		t.Fatalf("err = %v, want the owners refusal", err)
	}
	for _, want := range []string{"v0.10.2", "v0.10.1", "stop these processes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q is missing %q", err.Error(), want)
		}
	}
	// Nothing was migrated, so the operator can close the processes and re-run.
	report := inspectReport(t, root)
	if got := report["user_version"].(float64); int(got) != flowstore.DatabaseSchemaVersion()-1 {
		t.Fatalf("user_version = %v after a refused migration", got)
	}
	if owners, ok := report["owners"].([]any); !ok || len(owners) != 2 {
		t.Fatalf("db inspect reports owners = %v, want both live holders", report["owners"])
	}
}

func holdOwnerLease(t *testing.T, root string, schema int, build string) *dblease.Holder {
	t.Helper()
	holder, err := dblease.Acquire(root, dblease.Identity{
		BuildVersion:  build,
		Executable:    "/opt/approach/" + build,
		SchemaVersion: schema,
		StartedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Release() })
	return holder
}

func inspectReport(t *testing.T, root string) map[string]any {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "db", "inspect", "--json", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// TestRunDBRestorePutsABackupBackAndReportsIt is `db restore` at the command
// surface: the leaf that makes "reversible migration" actually reversible.
func TestRunDBRestorePutsABackupBackAndReportsIt(t *testing.T) {
	root := t.TempDir()
	seedCurrentRoot(t, root)
	setTestDatabaseVersion(t, filepath.Join(root, "approach.db"), flowstore.DatabaseSchemaVersion()-1)
	backupDir := filepath.Join(root, "backups")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "db", "migrate", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatalf("db migrate: %v", err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v, err %v", entries, err)
	}
	backup := filepath.Join(backupDir, entries[0].Name())

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"approach", "db", "restore", "--backup", backup, "--json", "--state-root", root},
		dbDeps(t, &stdout, &stderr, nil)); err != nil {
		t.Fatalf("db restore: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("db restore --json is not JSON: %v\n%s", err, stdout.String())
	}
	if got := int(result["user_version"].(float64)); got != flowstore.DatabaseSchemaVersion()-1 {
		t.Fatalf("user_version = %d, want the backup's %d", got, flowstore.DatabaseSchemaVersion()-1)
	}
	if result["restored_from"] != backup {
		t.Fatalf("restored_from = %v, want %q", result["restored_from"], backup)
	}
	if result["pre_restore_backup"] == "" {
		t.Fatal("db restore recorded no pre-restore copy")
	}
	// And the restored database is reported by the diagnostic.
	report := inspectReport(t, root)
	if got := int(report["user_version"].(float64)); got != flowstore.DatabaseSchemaVersion()-1 {
		t.Fatalf("db inspect user_version = %d after the restore", got)
	}
}

func TestRunDBRestoreWithoutABackupFlagFailsWithUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "db", "restore", "--state-root", t.TempDir()},
		dbDeps(t, &stdout, &stderr, nil))
	if err == nil {
		t.Fatal("db restore with no --backup succeeded")
	}
	if !strings.Contains(err.Error(), "--backup") {
		t.Fatalf("err = %v, want the usage refusal", err)
	}
}
