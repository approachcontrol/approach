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
