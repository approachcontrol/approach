package embeddedterm

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTerminalRunsCommandAndRendersLiveOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	manager := NewManager()
	term, err := manager.Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf hello"},
		Width:   40,
		Height:  5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	lines := term.VisibleLines(40, 5, Viewport{Mode: ViewportLive})
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "hello") {
		t.Fatalf("visible lines = %#v, want output containing hello", lines)
	}
}

func TestTerminalReportsFailedForNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "exit 7"},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err == nil {
		t.Fatal("Wait error = nil, want non-zero command error")
	}
	if got := term.State(); got != StateFailed {
		t.Fatalf("State = %q, want %q", got, StateFailed)
	}
}

func TestTerminalTerminateStopsRunningCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "sleep 30"},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}
	if err := term.Terminate(); err != nil {
		t.Fatalf("second Terminate returned error: %v", err)
	}
	if err := term.Wait(ctx); err == nil {
		t.Fatal("Wait error = nil, want terminated command error")
	}
	if got := term.State(); got != StateTerminated {
		t.Fatalf("State = %q, want %q", got, StateTerminated)
	}
}
