package flowlease_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/approachcontrol/approach/internal/flowlease"
)

func TestLeaseHeldUntilOwnerCloses(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	flowID := "flow-1"

	state, err := flowlease.Inspect(root, flowID)
	if err != nil {
		t.Fatalf("Inspect(free) error = %v", err)
	}
	if state != flowlease.Free {
		t.Fatalf("Inspect(free) = %v, want Free", state)
	}

	lease, err := flowlease.Acquire(root, flowID)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	state, err = flowlease.Inspect(root, flowID)
	if err != nil {
		t.Fatalf("Inspect(held) error = %v", err)
	}
	if state != flowlease.Held {
		t.Fatalf("Inspect(held) = %v, want Held", state)
	}

	if err := lease.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
	state, err = flowlease.Inspect(root, flowID)
	if err != nil {
		t.Fatalf("Inspect(released) error = %v", err)
	}
	if state != flowlease.Free {
		t.Fatalf("Inspect(released) = %v, want Free", state)
	}

	lockPath := filepath.Join(root, "flow-leases", flowID+".lock")
	if info, err := os.Lstat(lockPath); err != nil {
		t.Fatalf("Lstat(stable lock) error = %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("stable lock mode = %v, want regular", info.Mode())
	}
}

func TestLeaseCrossProcessContention(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseHelperProcess$")
	cmd.Env = append(os.Environ(), "APPROACH_TEST_FLOW_LEASE_ROOT="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start(helper) error = %v", err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper readiness = %q, %v", line, err)
	}
	if state, err := flowlease.Inspect(root, "flow-1"); err != nil || state != flowlease.Held {
		t.Fatalf("Inspect(cross-process) = %v, %v, want Held", state, err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("Close(helper stdin) error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait(helper) error = %v", err)
	}
	if state, err := flowlease.Inspect(root, "flow-1"); err != nil || state != flowlease.Free {
		t.Fatalf("Inspect(after helper) = %v, %v, want Free", state, err)
	}
}

func TestLeaseHelperProcess(t *testing.T) {
	root := os.Getenv("APPROACH_TEST_FLOW_LEASE_ROOT")
	if root == "" {
		t.Skip("helper only")
	}
	lease, err := flowlease.Acquire(root, "flow-1")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(os.Stdin).ReadByte(); err == nil {
		t.Fatal("helper stdin unexpectedly produced data")
	}
}

func TestLeaseRejectsUnsafeArtifactPaths(t *testing.T) {
	newRoot := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		if err := os.Chmod(root, 0o700); err != nil {
			t.Fatalf("Chmod(root) error = %v", err)
		}
		return root
	}

	t.Run("relative root", func(t *testing.T) {
		if _, err := flowlease.Inspect("relative", "flow-1"); err == nil {
			t.Fatal("Inspect(relative root) error = nil, want error")
		}
	})
	t.Run("unsafe flow id", func(t *testing.T) {
		if _, err := flowlease.Inspect(newRoot(t), "../flow"); err == nil {
			t.Fatal("Inspect(unsafe id) error = nil, want error")
		}
	})
	t.Run("symlink collection", func(t *testing.T) {
		root := newRoot(t)
		target := newRoot(t)
		if err := os.Symlink(target, filepath.Join(root, "flow-leases")); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}
		if _, err := flowlease.Inspect(root, "flow-1"); err == nil {
			t.Fatal("Inspect(symlink collection) error = nil, want error")
		}
	})
	t.Run("symlink lock", func(t *testing.T) {
		root := newRoot(t)
		dir := filepath.Join(root, "flow-leases")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v", err)
		}
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "flow-1.lock")); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}
		if _, err := flowlease.Inspect(root, "flow-1"); err == nil {
			t.Fatal("Inspect(symlink lock) error = nil, want error")
		}
	})
	t.Run("non-regular lock", func(t *testing.T) {
		root := newRoot(t)
		dir := filepath.Join(root, "flow-leases")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("Mkdir() error = %v", err)
		}
		if err := os.Mkdir(filepath.Join(dir, "flow-1.lock"), 0o700); err != nil {
			t.Fatalf("Mkdir(lock) error = %v", err)
		}
		if _, err := flowlease.Inspect(root, "flow-1"); err == nil {
			t.Fatal("Inspect(non-regular lock) error = nil, want error")
		}
	})
	t.Run("unsafe permissions", func(t *testing.T) {
		root := newRoot(t)
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatalf("Chmod(root) error = %v", err)
		}
		if _, err := flowlease.Inspect(root, "flow-1"); err == nil {
			t.Fatal("Inspect(public root) error = nil, want error")
		}
	})
}

func TestLeaseConcurrentInspectionReportsHeld(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	lease, err := flowlease.Acquire(root, "flow-1")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lease.Close()

	const readers = 32
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := flowlease.Inspect(root, "flow-1")
			if err != nil {
				errs <- err
				return
			}
			if state != flowlease.Held {
				errs <- errors.New("inspection did not report Held")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := flowlease.Acquire(root, "flow-1"); !errors.Is(err, flowlease.ErrHeld) {
		t.Fatalf("Acquire(contended) error = %v, want ErrHeld", err)
	}
}

func TestLeaseConcurrentInspectionDoesNotInventOwnership(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	// Create the stable file before the fan-out so this test isolates lock
	// contention from directory/file allocation races.
	if state, err := flowlease.Inspect(root, "flow-1"); err != nil || state != flowlease.Free {
		t.Fatalf("Inspect(setup) = %v, %v", state, err)
	}

	const readers = 64
	start := make(chan struct{})
	errCh := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			state, err := flowlease.Inspect(root, "flow-1")
			if err != nil {
				errCh <- err
			} else if state != flowlease.Free {
				errCh <- errors.New("concurrent inspector invented Held state")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}
