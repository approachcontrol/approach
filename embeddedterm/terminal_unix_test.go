//go:build !windows

package embeddedterm

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminalTerminateKillsChildProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pidFile := t.TempDir() + "/child.pid"
	script := "trap '' HUP; sleep 30 & echo $! > " + strconv.Quote(pidFile) + "; wait"
	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", script},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	pid := waitForPIDFile(t, ctx, pidFile)
	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("child process %d is still alive after terminal termination", pid)
	}
}

func waitForPIDFile(t *testing.T, ctx context.Context, path string) int {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for child pid file: %v", ctx.Err())
		default:
		}
		body, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(body)) != "" {
			pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err != nil {
				t.Fatalf("invalid child pid %q: %v", strings.TrimSpace(string(body)), err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
