package gitquery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerRunTimesOutHungGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test git shim is a shell script")
	}
	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := (execRunner{timeout: 20 * time.Millisecond}).Run(t.TempDir(), "status", "--porcelain")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "git status timed out after 20ms") {
		t.Fatalf("Run() error = %q, want operation and timeout", err)
	}
}

func TestExecRunnerPredicateTimesOutHungGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test git shim is a shell script")
	}
	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	matched, err := (execRunner{timeout: 20 * time.Millisecond}).Predicate(t.TempDir(), "merge-base", "--is-ancestor", "a", "b")
	if matched || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Predicate() = %t, %v, want false and deadline exceeded", matched, err)
	}
	if !strings.Contains(err.Error(), "git merge-base timed out after 20ms") {
		t.Fatalf("Predicate() error = %q, want operation and timeout", err)
	}
}

func TestExecRunnerWaitDelayUnblocksOrphanedStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test git shim is a shell script")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
sleep 30 &
exec sleep 30
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := execRunner{timeout: 200 * time.Millisecond}
	start := time.Now()
	_, err := r.Run(t.TempDir(), "status")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run() error = nil, want timeout after the parent git process is killed")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Run hung for %s; WaitDelay should unblock orphaned stdout", elapsed)
	}
}
