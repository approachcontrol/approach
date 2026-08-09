package beadsquery

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
if [ -n "${BEADS_DIR:-}" ] || [ -n "${BEADS_DB:-}" ] || [ -n "${BD_DB:-}" ]; then
	printf 'database selector leaked\n' >&2
	exit 42
fi
if [ "${BD_JSON_ENVELOPE:-}" != "1" ]; then
	printf 'unrelated environment was removed\n' >&2
	exit 43
fi
pwd
`
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("BEADS_DIR", "/other/.beads")
	t.Setenv("BEADS_DB", "/other/beads.db")
	t.Setenv("BD_DB", "/other/legacy.db")
	t.Setenv("BD_JSON_ENVELOPE", "1")
	repoPath := t.TempDir()

	out, err := (execRunner{}).Run(repoPath, "list", "-s", "open", "--json")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := strings.TrimSpace(out)
	gotInfo, gotErr := os.Stat(got)
	wantInfo, wantErr := os.Stat(repoPath)
	if gotErr != nil || wantErr != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("Run() directory = %q, want same directory as %q", got, repoPath)
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
