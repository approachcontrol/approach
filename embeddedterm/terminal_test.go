package embeddedterm

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	bubbleemulator "github.com/taigrr/bubbleterm/emulator"
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

func TestTerminalRendersCursorUpdatesAndClears(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'hello\\rbye\\033[K'"},
		Width:   20,
		Height:  3,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := strings.Join(term.VisibleLines(20, 3, Viewport{Mode: ViewportLive}), "\n")
	if !strings.Contains(got, "bye") {
		t.Fatalf("visible lines = %q, want cursor-updated text", got)
	}
	if strings.Contains(got, "hello") {
		t.Fatalf("visible lines = %q, should not show stale overwritten text", got)
	}
}

func TestTerminalPreservesANSIStyleInVisibleLines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf '\\033[31mred\\033[0m'"},
		Width:   20,
		Height:  3,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := strings.Join(term.VisibleLines(20, 3, Viewport{Mode: ViewportLive}), "\n")
	if !strings.Contains(ansi.Strip(got), "red") {
		t.Fatalf("visible lines = %q, want stripped output containing red", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("visible lines = %q, want ANSI style preserved", got)
	}
}

func TestTerminalVisibleLinesFitRequestedWidth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf '\\033[31mabcdef\\033[0m'"},
		Width:   20,
		Height:  3,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	lines := term.VisibleLines(5, 3, Viewport{Mode: ViewportLive})
	for _, line := range lines {
		if width := ansi.StringWidth(line); width != 5 {
			t.Fatalf("line width = %d, want 5 in %#v", width, lines)
		}
	}
	if got := strings.Join(lines, "\n"); !strings.Contains(ansi.Strip(got), "abcde") {
		t.Fatalf("visible lines = %#v, want output truncated to requested width", lines)
	}
}

func TestTerminalVisibleLinesAfterExitCanGrowViewport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'one\\ntwo\\nthree\\nfour\\nfive\\n'"},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := ansi.Strip(strings.Join(term.VisibleLines(20, 5, Viewport{Mode: ViewportLive}), "\n"))
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Fatalf("visible lines = %q, want retained output %q after viewport grows", got, want)
		}
	}
}

func TestTerminalRetainedOutputRestoresNormalScreenAfterAltScreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'one\\ntwo\\n\\033[?1049halt screen\\033[?1049lthree\\nfour\\nfive\\n'"},
		Width:   30,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := ansi.Strip(strings.Join(term.VisibleLines(30, 5, Viewport{Mode: ViewportLive}), "\n"))
	if strings.Contains(got, "alt screen") {
		t.Fatalf("visible lines = %q, want alternate screen content hidden after exit", got)
	}
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Fatalf("visible lines = %q, want normal screen output %q after alt-screen restore", got, want)
		}
	}
}

func TestTerminalRetainedOutputAppliesCursorClears(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'stale\\rnew\\033[K\\ntwo\\nthree\\nfour\\nfive\\n'"},
		Width:   30,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := ansi.Strip(strings.Join(term.VisibleLines(30, 5, Viewport{Mode: ViewportLive}), "\n"))
	if strings.Contains(got, "stale") {
		t.Fatalf("visible lines = %q, want stale overwritten text hidden", got)
	}
	if !strings.Contains(got, "new") {
		t.Fatalf("visible lines = %q, want cursor-updated text retained", got)
	}
}

func TestTerminalRetainedLongLineIsBoundedToTerminalGrid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf " + shellQuote(strings.Repeat("x", 20000))},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	for _, line := range term.VisibleLines(20, 5, Viewport{Mode: ViewportLive}) {
		if width := ansi.StringWidth(line); width != 20 {
			t.Fatalf("line width = %d, want fitted width 20 in %#v", width, line)
		}
	}
}

func TestTerminalWaitDrainsFinalOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload := strings.Repeat("x", 5000) + "done"
	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf " + shellQuote(payload)},
		Width:   80,
		Height:  80,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := strings.Join(term.VisibleLines(80, 80, Viewport{Mode: ViewportLive}), "\n")
	if !strings.Contains(got, "done") {
		t.Fatalf("visible lines missing final output marker")
	}
}

func TestTerminalStartCommandCleansUpProcessWhenEmulatorCreationFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	originalFactory := newEmulatorFromPipes
	t.Cleanup(func() {
		newEmulatorFromPipes = originalFactory
	})
	newEmulatorFromPipes = func(int, int, io.Reader, io.WriteCloser) (*bubbleemulator.Emulator, error) {
		return nil, errors.New("emulator failed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30")
	term, err := NewManager().StartCommand(ctx, cmd, 20, 2)
	if err == nil {
		_ = term.Close()
		t.Fatal("StartCommand error = nil, want emulator creation failure")
	}
	if !strings.Contains(err.Error(), "emulator failed") {
		t.Fatalf("StartCommand error = %v, want emulator failure", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("command process state is nil, want process waited after emulator creation failure")
	}
}

func TestTerminalAddsTermOnlyWhenAbsentOrDumb(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}

	tests := []struct {
		name string
		env  []string
		want string
	}{
		{name: "absent", env: []string{"PATH=" + os.Getenv("PATH")}, want: "xterm-256color"},
		{name: "dumb", env: []string{"PATH=" + os.Getenv("PATH"), "TERM=dumb"}, want: "xterm-256color"},
		{name: "explicit", env: []string{"PATH=" + os.Getenv("PATH"), "TERM=screen-256color"}, want: "screen-256color"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			term, err := NewManager().Start(ctx, StartRequest{
				Command: "sh",
				Args:    []string{"-c", "printf \"$TERM\""},
				Env:     tt.env,
				Width:   40,
				Height:  3,
			})
			if err != nil {
				t.Fatalf("Start returned error: %v", err)
			}
			defer term.Close()

			if err := term.Wait(ctx); err != nil {
				t.Fatalf("Wait returned error: %v", err)
			}
			got := strings.Join(term.VisibleLines(40, 3, Viewport{Mode: ViewportLive}), "\n")
			if !strings.Contains(got, tt.want) {
				t.Fatalf("visible lines = %q, want TERM %q", got, tt.want)
			}
		})
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestTerminalWaitIsRepeatableAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "exit 0"},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("first Wait returned error: %v", err)
	}
	if err := term.Wait(ctx); err != nil {
		t.Fatalf("second Wait returned error: %v", err)
	}
}

func TestTerminalWriteAfterExitReturnsClosedError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "exit 0"},
		Width:   20,
		Height:  2,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if _, err := term.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after exit error = %v, want os.ErrClosed", err)
	}
}

func TestTerminalWriteAfterCloseReturnsClosedError(t *testing.T) {
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
	defer term.Terminate()

	if err := term.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if _, err := term.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after close error = %v, want os.ErrClosed", err)
	}
}

func TestTerminalResizeClosedPTYIsNoop(t *testing.T) {
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
	defer term.Terminate()

	if err := term.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := term.Resize(40, 10); err != nil {
		t.Fatalf("Resize after Close returned error: %v", err)
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
