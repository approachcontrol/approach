package beadsmutate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExecRunnerTargetsSelectedRepositoryAndPreservesActor(t *testing.T) {
	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	script := `#!/bin/sh
if [ -n "${BEADS_DIR:-}" ] || [ -n "${BEADS_DB:-}" ] || [ -n "${BD_DB:-}" ] || [ -n "${BEADS_DOLT_SERVER_DATABASE:-}" ]; then
  printf 'database selector leaked\n' >&2
  exit 42
fi
if [ "${BEADS_ACTOR:-}" != "approach-agent" ] || [ "${BD_JSON_ENVELOPE:-}" != "1" ]; then
  printf 'required environment removed\n' >&2
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
	t.Setenv("BEADS_ACTOR", "approach-agent")
	t.Setenv("BD_JSON_ENVELOPE", "1")
	root := t.TempDir()
	t.Chdir(root)
	repoPath := filepath.Join("repos", "project")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	absRepoPath := filepath.Join(root, repoPath)

	out, err := (execRunner{}).Run(repoPath, "update", "child-42", "--claim")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	wantArgs := []string{"-C", absRepoPath, "update", "child-42", "--claim"}
	if len(lines) != len(wantArgs)+1 || !reflect.DeepEqual(lines[:len(wantArgs)], wantArgs) {
		t.Fatalf("bd output = %#v, want args %#v plus working directory", lines, wantArgs)
	}
	gotInfo, gotErr := os.Stat(lines[len(lines)-1])
	wantInfo, wantErr := os.Stat(absRepoPath)
	if gotErr != nil || wantErr != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("Run() directory = %q, want same directory as %q", lines[len(lines)-1], absRepoPath)
	}
}

func TestExecRunnerRejectsSuccessfulOversizedOutput(t *testing.T) {
	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte("#!/bin/sh\nprintf '123456789'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	out, err := (execRunner{maxOutputBytes: 8}).Run(t.TempDir(), "update", "child-42", "--claim")
	if err == nil || !strings.Contains(err.Error(), "exceeded 8-byte limit") {
		t.Fatalf("Run() error = %v, want output limit error", err)
	}
	if out != "" {
		t.Fatalf("Run() output = %q, want no partial output", out)
	}
}

func TestExecRunnerCapsFailingStderrAndPreservesExitError(t *testing.T) {
	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	script := "#!/bin/sh\n/usr/bin/yes x | /usr/bin/head -c 1048577 >&2\nexit 42\n"
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":/usr/bin:/bin")

	_, err := (execRunner{}).Run(t.TempDir(), "update", "child-42", "--claim")
	if err == nil {
		t.Fatal("Run() error = nil, want command failure")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run() error = %v, want *exec.ExitError", err)
	}
	if len(err.Error()) > defaultBDErrorLimit+128 {
		t.Fatalf("Run() diagnostic length = %d, want capped near %d", len(err.Error()), defaultBDErrorLimit)
	}
}

func TestExecRunnerPreservesMissingExecutableError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (execRunner{}).Run(t.TempDir(), "update", "child-42", "--claim")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Run() error = %v, want exec.ErrNotFound", err)
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		t.Fatalf("Run() error = %v, want *exec.Error", err)
	}
}

func TestIsolatedBeadsEnvironmentPreservesClaimActorAndAllowedSettings(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	env := []string{
		"BEADS_DIR=/other/.beads",
		"BEADS_DB=/other/beads.db",
		"BD_DB=/other/legacy.db",
		"BEADS_DOLT_SERVER_DATABASE=other_database",
		"BEADS_FUTURE_TARGET_OVERRIDE=/other/future",
		"BD_JSON_ENVELOPE=1",
		"BD_DISABLE_EVENT_FLUSH=1",
		"BD_DISABLE_METRICS=1",
		"BD_OTEL_LOGS_URL=https://logs.example",
		"BD_OTEL_METRICS_URL=https://metrics.example",
		"BEADS_ACTOR=approach-agent",
		"BEADS_CREDENTIALS_FILE=credentials.json",
		"BEADS_DOLT_PASSWORD=secret",
		"BEADS_DOLT_SERVER_TLS=1",
		"BEADS_DOLT_SERVER_USER=writer",
	}

	got, err := isolatedBeadsEnvironment(env)
	if err != nil {
		t.Fatalf("isolatedBeadsEnvironment() error = %v", err)
	}
	want := []string{
		"BD_JSON_ENVELOPE=1",
		"BD_DISABLE_EVENT_FLUSH=1",
		"BD_DISABLE_METRICS=1",
		"BD_OTEL_LOGS_URL=https://logs.example",
		"BD_OTEL_METRICS_URL=https://metrics.example",
		"BEADS_ACTOR=approach-agent",
		"BEADS_CREDENTIALS_FILE=" + filepath.Join(root, "credentials.json"),
		"BEADS_DOLT_PASSWORD=secret",
		"BEADS_DOLT_SERVER_TLS=1",
		"BEADS_DOLT_SERVER_USER=writer",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("isolatedBeadsEnvironment() = %#v, want %#v", got, want)
	}
}
