package beadsquery

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExecRunnerIncludesStderrAndPreservesExitError(t *testing.T) {
	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte("#!/bin/sh\nprintf 'missing beads database\\n' >&2\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	_, err := (execRunner{}).Run(t.TempDir(), "list", "-s", "open", "--json")
	if err == nil {
		t.Fatal("Run() error = nil, want command failure")
	}
	if !strings.Contains(err.Error(), "missing beads database") {
		t.Fatalf("Run() error = %q, want stderr diagnostic", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run() error = %v, want *exec.ExitError", err)
	}
}

func TestExecRunnerPreservesMissingExecutableError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := (execRunner{}).Run(t.TempDir(), "list", "-s", "open", "--json")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Run() error = %v, want exec.ErrNotFound", err)
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		t.Fatalf("Run() error = %v, want *exec.Error", err)
	}
}

func TestExecRunnerIsolatesAmbientDatabaseSelectors(t *testing.T) {
	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	script := `#!/bin/sh
if [ -n "${BEADS_DIR:-}" ] || [ -n "${BEADS_DB:-}" ] || [ -n "${BD_DB:-}" ] || [ -n "${BEADS_DOLT_SERVER_DATABASE:-}" ]; then
	printf 'database selector leaked\n' >&2
	exit 42
fi
if [ "${BD_JSON_ENVELOPE:-}" != "1" ]; then
	printf 'unrelated environment was removed\n' >&2
	exit 43
fi
	printf '%s\n' "$@"
	pwd
`
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("BEADS_DIR", "/other/.beads")
	t.Setenv("BEADS_DB", "/other/beads.db")
	t.Setenv("BD_DB", "/other/legacy.db")
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "other_database")
	t.Setenv("BD_JSON_ENVELOPE", "1")
	root := t.TempDir()
	t.Chdir(root)
	repoPath := filepath.Join("repos", "project")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	absRepoPath := filepath.Join(root, repoPath)

	out, err := (execRunner{}).Run(repoPath, "list", "-s", "closed", "--json", "--limit", "100", "--sort", "closed", "--readonly")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	wantArgs := []string{"-C", absRepoPath, "list", "-s", "closed", "--json", "--limit", "100", "--sort", "closed", "--readonly"}
	if len(lines) != len(wantArgs)+1 {
		t.Fatalf("Run() output lines = %#v, want args plus working directory", lines)
	}
	if gotArgs := lines[:len(wantArgs)]; !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("bd args = %#v, want %#v", gotArgs, wantArgs)
	}
	got := lines[len(lines)-1]
	gotInfo, gotErr := os.Stat(got)
	wantInfo, wantErr := os.Stat(absRepoPath)
	if gotErr != nil || wantErr != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("Run() directory = %q, want same directory as %q", got, absRepoPath)
	}
}

func TestExecRunnerRejectsOversizedOutput(t *testing.T) {
	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte("#!/bin/sh\nprintf '123456789'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	out, err := (execRunner{maxOutputBytes: 8}).Run(t.TempDir(), "list", "-s", "open", "--json")
	if err == nil || !strings.Contains(err.Error(), "exceeded 8-byte limit") {
		t.Fatalf("Run() error = %v, want output limit error", err)
	}
	if out != "" {
		t.Fatalf("Run() output = %q, want no partial data", out)
	}
}

func TestIsolatedBeadsEnvironmentRemovesTargetOverrides(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	targetOverrides := []string{
		"BEADS_DIR=/other/.beads",
		"BEADS_DB=/other/beads.db",
		"BD_DB=/other/legacy.db",
		"BEADS_CENTRAL_CONFIG=/other/central.yaml",
		"BEADS_DOLT_DATA_DIR=/other/dolt",
		"BEADS_DOLT_PORT=3308",
		"BEADS_DOLT_PROXIED_SERVER=1",
		"BEADS_DOLT_SERVER_DATABASE=other_database",
		"BEADS_DOLT_SERVER_HOST=other.example",
		"BEADS_DOLT_SERVER_MODE=1",
		"BEADS_DOLT_SERVER_PORT=3309",
		"BEADS_DOLT_SERVER_SOCKET=/other/dolt.sock",
		"BEADS_DOLT_SHARED_SERVER=1",
		"BEADS_MYSQL_URL=mysql://other",
		"BEADS_POSTGRES_URL=postgres://other",
		"BEADS_PROXIED_SERVER_CONFIG=/other/proxied.json",
		"BEADS_PROXIED_SERVER_ROOT_PATH=/other/proxied",
		"BEADS_SHARED_SERVER_DIR=/other/shared",
		"BD_OTEL_STDOUT=true",
		"BEADS_FUTURE_TARGET_OVERRIDE=/other/future",
	}
	env := append(targetOverrides,
		"BD_JSON_ENVELOPE=1",
		"BD_DISABLE_METRICS=1",
		"BEADS_CREDENTIALS_FILE=credentials.json",
		"BEADS_DOLT_PASSWORD=secret",
		"BEADS_DOLT_SERVER_TLS=1",
		"BEADS_DOLT_SERVER_USER=reader",
	)

	got, err := isolatedBeadsEnvironment(env)
	if err != nil {
		t.Fatalf("isolatedBeadsEnvironment() error = %v", err)
	}
	want := []string{
		"BD_JSON_ENVELOPE=1",
		"BD_DISABLE_METRICS=1",
		"BEADS_CREDENTIALS_FILE=" + filepath.Join(root, "credentials.json"),
		"BEADS_DOLT_PASSWORD=secret",
		"BEADS_DOLT_SERVER_TLS=1",
		"BEADS_DOLT_SERVER_USER=reader",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("isolatedBeadsEnvironment() = %#v, want %#v", got, want)
	}
}
