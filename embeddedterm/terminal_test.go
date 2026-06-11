package embeddedterm

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
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
	lines := term.VisibleLines(40, 5)
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "hello") {
		t.Fatalf("visible lines = %#v, want output containing hello", lines)
	}
}

func TestTerminalRendersOutputWhileCommandIsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf live-output; sleep 1"},
		Width:   40,
		Height:  5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		lines := term.VisibleLines(40, 5)
		if strings.Contains(strings.Join(lines, "\n"), "live-output") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("visible lines never showed live output: %#v", term.VisibleLines(40, 5))
}

func TestTerminalBridgesTerminalQueryResponses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "bash",
		Args: []string{
			"-lc",
			"printf '\\033[6n'; IFS= read -r -s -n 6 response; printf 'query-answered'",
		},
		Width:  40,
		Height: 5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := strings.Join(term.VisibleLines(40, 5), "\n")
	if !strings.Contains(got, "query-answered") {
		t.Fatalf("visible lines = %q, want output after terminal query response", got)
	}
}

func TestTerminalQueryFilterAnswersAndStrips(t *testing.T) {
	var responses strings.Builder
	filter := terminalQueryFilter{writer: &responses}

	got := filter.Filter([]byte("before\x1b[?uafter"), false)
	want := "beforeafter"
	if string(got) != want {
		t.Fatalf("filtered = %q, want %q", string(got), want)
	}
	if got := responses.String(); got != "\x1b[?0u" {
		t.Fatalf("responses = %q, want kitty keyboard fallback", got)
	}
}

func TestTerminalQueryFilterHandlesSplitSequence(t *testing.T) {
	var responses strings.Builder
	filter := terminalQueryFilter{writer: &responses}

	got := filter.Filter([]byte("before\x1b[?"), false)
	if string(got) != "before" {
		t.Fatalf("filtered = %q, want prefix only", string(got))
	}
	if got := string(filter.pending); got != "\x1b[?" {
		t.Fatalf("pending = %q, want partial kitty query", got)
	}

	got = filter.Filter([]byte("uafter"), false)
	if string(got) != "after" {
		t.Fatalf("filtered after completion = %q, want trailing output", string(got))
	}
	if got := responses.String(); got != "\x1b[?0u" {
		t.Fatalf("responses = %q, want kitty keyboard fallback", got)
	}
}

func TestTerminalQueryFilterCapsPendingBytes(t *testing.T) {
	filter := terminalQueryFilter{
		writer:  io.Discard,
		pending: []byte(strings.Repeat("\x1b", maxPendingSequenceBytes+1)),
	}

	got := filter.Filter([]byte("tail"), false)
	if len(filter.pending) != 0 {
		t.Fatalf("pending length = %d, want capped and flushed", len(filter.pending))
	}
	if !strings.Contains(string(got), "tail") {
		t.Fatalf("filtered = %q, want stream to recover after capped pending", string(got))
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
	got := strings.Join(term.VisibleLines(20, 3), "\n")
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
	got := strings.Join(term.VisibleLines(20, 3), "\n")
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
	lines := term.VisibleLines(5, 3)
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
	got := ansi.Strip(strings.Join(term.VisibleLines(20, 5), "\n"))
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Fatalf("visible lines = %q, want post-exit output %q after viewport grows", got, want)
		}
	}
}

func TestTerminalSurvivesInBandResizeModeSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf '\\033[?2048hstill-alive'"},
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
	got := strings.Join(term.VisibleLines(20, 3), "\n")
	if !strings.Contains(got, "still-alive") {
		t.Fatalf("visible lines = %q, want output after in-band resize mode set", got)
	}
}

func TestTerminalAnswersCursorPositionWithinScreenHeight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "bash",
		Args: []string{
			"-lc",
			"for i in $(seq 1 30); do printf 'line%02d\\n' \"$i\"; done; printf '\\033[6n'; IFS= read -r -s -d R response; response=${response#*$'\\033['}; printf 'ROW=%s' \"${response%;*}\"",
		},
		Width:  40,
		Height: 5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := ansi.Strip(strings.Join(term.VisibleLines(40, 5), "\n"))
	idx := strings.LastIndex(got, "ROW=")
	if idx == -1 {
		t.Fatalf("visible lines = %q, want echoed cursor row", got)
	}
	rowText, ok := strings.CutPrefix(got[idx:], "ROW=")
	if !ok {
		t.Fatalf("visible lines = %q, want echoed cursor row", got)
	}
	rowText = strings.Fields(rowText)[0]
	row, err := strconv.Atoi(rowText)
	if err != nil {
		t.Fatalf("cursor row = %q, want number", rowText)
	}
	if row > 5 {
		t.Fatalf("cursor row = %d, want within screen height 5", row)
	}
}

func TestTerminalAnswersDECRQMWithModeReport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "bash",
		Args: []string{
			"-lc",
			"printf '\\033[?2026$p'; IFS= read -r -s -d y response; printf 'mode-report'",
		},
		Width:  40,
		Height: 5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := strings.Join(term.VisibleLines(40, 5), "\n")
	if !strings.Contains(got, "mode-report") {
		t.Fatalf("visible lines = %q, want output after DECRQM response", got)
	}
}

func TestTerminalAnswersDSRStatusWithANSIForm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "bash",
		Args: []string{
			"-lc",
			"printf '\\033[5n'; IFS= read -r -s -n 4 response; if [[ \"$response\" == $'\\033[0n' ]]; then printf 'dsr-ansi'; else printf 'bad-dsr'; fi",
		},
		Width:  40,
		Height: 5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := strings.Join(term.VisibleLines(40, 5), "\n")
	if !strings.Contains(got, "dsr-ansi") {
		t.Fatalf("visible lines = %q, want ANSI DSR status response", got)
	}
	if strings.Contains(got, "bad-dsr") {
		t.Fatalf("visible lines = %q, got non-ANSI DSR status response", got)
	}
}

func TestTerminalPostExitViewSurvivesClearScreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'one\\ntwo\\nthree\\nfour\\nfive\\n\\033[2J'"},
		Width:   20,
		Height:  5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := ansi.Strip(strings.Join(term.VisibleLines(20, 8), "\n"))
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Fatalf("visible lines = %q, want clear-screen scrollback line %q", got, want)
		}
	}
}

func TestTerminalPostExitRowsUseResizedWidth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	marker := strings.Repeat("x", 40)
	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "sleep 0.1; printf " + shellQuote(marker)},
		Width:   20,
		Height:  3,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	if err := term.Resize(60, 3); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}
	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	got := ansi.Strip(strings.Join(term.VisibleLines(60, 3), "\n"))
	if !strings.Contains(got, marker) {
		t.Fatalf("visible lines = %q, want marker on one resized-width row", got)
	}
}

func TestTerminalScrollbackIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "i=0; while [ $i -lt 5105 ]; do printf 'line%04d\\n' $i; i=$((i+1)); done"},
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
	got := ansi.Strip(strings.Join(term.VisibleLines(40, defaultScrollbackLines+100), "\n"))
	nonblank := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) != "" {
			nonblank++
		}
	}
	if nonblank > defaultScrollbackLines+3 {
		t.Fatalf("visible nonblank rows = %d, want bounded to scrollback cap plus screen height", nonblank)
	}
}

func TestTerminalPostExitSnapshotRestoresNormalScreenAfterAltScreen(t *testing.T) {
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
	got := ansi.Strip(strings.Join(term.VisibleLines(30, 5), "\n"))
	if strings.Contains(got, "alt screen") {
		t.Fatalf("visible lines = %q, want alternate screen content hidden after exit", got)
	}
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Fatalf("visible lines = %q, want normal screen output %q after alt-screen restore", got, want)
		}
	}
}

func TestTerminalPostExitSnapshotAppliesCursorClears(t *testing.T) {
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
	got := ansi.Strip(strings.Join(term.VisibleLines(30, 5), "\n"))
	if strings.Contains(got, "stale") {
		t.Fatalf("visible lines = %q, want stale overwritten text hidden", got)
	}
	if !strings.Contains(got, "new") {
		t.Fatalf("visible lines = %q, want cursor-updated text in post-exit output", got)
	}
}

func TestTerminalPostExitSnapshotLongLineIsBoundedToTerminalGrid(t *testing.T) {
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
	for _, line := range term.VisibleLines(20, 5) {
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

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "dd if=/dev/zero bs=1024 count=256 2>/dev/null | tr '\\000' x; printf done"},
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
	got := strings.Join(term.VisibleLines(80, 80), "\n")
	if !strings.Contains(got, "done") {
		t.Fatalf("visible lines missing final output marker")
	}
}

func TestTerminalStateExitsPromptlyAfterFastCommand(t *testing.T) {
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

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if term.State() != StateRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("terminal state still running after fast command; state=%s", term.State())
}

func TestTerminalStartCommandCleansUpProcessWhenEmulatorCreationFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	originalFactory := newEmulator
	t.Cleanup(func() {
		newEmulator = originalFactory
	})
	newEmulator = func(int, int) (*vt.SafeEmulator, error) {
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
			got := strings.Join(term.VisibleLines(40, 3), "\n")
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

func TestTerminalTerminatePreservesVisibleOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "printf 'before-terminate'; sleep 30"},
		Width:   40,
		Height:  3,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(term.VisibleLines(40, 3), "\n"), "before-terminate") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}
	if err := term.Wait(ctx); err == nil {
		t.Fatal("Wait error = nil, want terminated command error")
	}
	got := strings.Join(term.VisibleLines(40, 3), "\n")
	if !strings.Contains(got, "before-terminate") {
		t.Fatalf("visible lines after terminate = %q, want post-exit output", got)
	}
}

func TestTerminalTerminateWhileOutputIsActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	term, err := NewManager().Start(ctx, StartRequest{
		Command: "sh",
		Args:    []string{"-c", "i=0; while :; do printf 'line%04d\\n' $i; i=$((i+1)); done"},
		Width:   40,
		Height:  5,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer term.Close()

	time.Sleep(25 * time.Millisecond)
	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}
	if err := term.Wait(ctx); err == nil {
		t.Fatal("Wait error = nil, want terminated command error")
	}
	if got := term.State(); got != StateTerminated {
		t.Fatalf("State = %q, want %q", got, StateTerminated)
	}
}
