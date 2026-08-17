package embeddedterm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
)

func TestTmuxBackedTerminalDetachLeavesOwnedSessionRunning(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	runner := &tmuxTestRunner{failHasSession: true}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}

	if err := term.Detach(); err != nil {
		t.Fatalf("Detach returned error: %v", err)
	}

	if runner.called("kill-session") {
		t.Fatalf("detach should not kill owned tmux session, calls: %#v", runner.snapshot())
	}
	if got := cleanupCount.Load(); got != 0 {
		t.Fatalf("cleanup count = %d, want owned running session to rely on script self-cleanup", got)
	}
}

func TestTmuxBackedTerminalTerminateAfterDetachDoesNotKillOwnedSession(t *testing.T) {
	spec, _ := tmuxTestSpec()
	runner := &tmuxTestRunner{failHasSession: true}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}

	if err := term.Detach(); err != nil {
		t.Fatalf("Detach returned error: %v", err)
	}
	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate after detach returned error: %v", err)
	}

	if runner.called("kill-session") {
		t.Fatalf("terminate after detach should not kill owned tmux session, calls: %#v", runner.snapshot())
	}
}

func TestTmuxBackedTerminalTerminateKillsOwnedSession(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	runner := &tmuxTestRunner{failHasSession: true}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}

	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}

	if !runner.called("kill-session") {
		t.Fatalf("terminate should kill owned tmux session, calls: %#v", runner.snapshot())
	}
	waitCleanupCount(t, cleanupCount, 1)
}

func TestTmuxBackedTerminalExistingSessionIsUnowned(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	runner := &tmuxTestRunner{}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}

	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate returned error: %v", err)
	}

	if runner.called("new-session") || runner.called("kill-session") {
		t.Fatalf("existing unowned session should not be created or killed, calls: %#v", runner.snapshot())
	}
	waitCleanupCount(t, cleanupCount, 1)
}

func TestTmuxBackedTerminalStartFailureCleansOwnedSession(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	runner := &tmuxTestRunner{failHasSession: true}
	startErr := errors.New("pty start failed")
	_, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, func(context.Context, *exec.Cmd, int, int) (*Terminal, error) {
		return nil, startErr
	})
	if !errors.Is(err, startErr) {
		t.Fatalf("error = %v, want %v", err, startErr)
	}
	if !runner.called("kill-session") {
		t.Fatalf("start failure should kill newly-created tmux session, calls: %#v", runner.snapshot())
	}
	waitCleanupCount(t, cleanupCount, 1)
}

func TestTmuxBackedTerminalCreateFailureCleansScript(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	createErr := errors.New("tmux new-session failed")
	runner := &tmuxTestRunner{failHasSession: true, failNewSession: createErr}
	_, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if !errors.Is(err, createErr) {
		t.Fatalf("error = %v, want %v", err, createErr)
	}
	waitCleanupCount(t, cleanupCount, 1)
}

func tmuxTestSpec() (actions.EmbeddedTmuxAgentSpec, *atomic.Int32) {
	cleanupCount := &atomic.Int32{}
	return actions.EmbeddedTmuxAgentSpec{
		SessionName:        "approach-test-agent",
		HasSessionCommand:  exec.Command("tmux", "has-session", "-t", "approach-test-agent"),
		NewSessionCommand:  exec.Command("tmux", "new-session", "-d", "-s", "approach-test-agent"),
		AttachCommand:      exec.Command("tmux", "attach-session", "-t", "approach-test-agent"),
		KillSessionCommand: exec.Command("tmux", "kill-session", "-t", "approach-test-agent"),
		Cleanup: func() {
			cleanupCount.Add(1)
		},
	}, cleanupCount
}

func startSleepTerminal(ctx context.Context, _ *exec.Cmd, width, height int) (*Terminal, error) {
	return NewManager().StartCommand(ctx, exec.Command("sh", "-c", "sleep 10"), width, height)
}

type tmuxTestRunner struct {
	mu             sync.Mutex
	failHasSession bool
	failNewSession error
	calls          [][]string
}

func (r *tmuxTestRunner) run(cmd *exec.Cmd) error {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), cmd.Args...))
	r.mu.Unlock()
	switch {
	case reflect.DeepEqual(cmd.Args[:2], []string{"tmux", "has-session"}) && r.failHasSession:
		return errors.New("missing")
	case reflect.DeepEqual(cmd.Args[:2], []string{"tmux", "new-session"}) && r.failNewSession != nil:
		return r.failNewSession
	default:
		return nil
	}
}

func (r *tmuxTestRunner) called(subcommand string) bool {
	for _, call := range r.snapshot() {
		if len(call) > 1 && call[1] == subcommand {
			return true
		}
	}
	return false
}

func (r *tmuxTestRunner) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i := range r.calls {
		out[i] = append([]string(nil), r.calls[i]...)
	}
	return out
}

func TestTmuxBackedTerminalDetachTarget(t *testing.T) {
	spec, _ := tmuxTestSpec()
	runner := &tmuxTestRunner{}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}
	defer term.Terminate()

	if got := term.DetachTarget(); got != "approach-test-agent" {
		t.Fatalf("DetachTarget = %q, want session name", got)
	}
}

func TestTmuxBackedTerminalUnexpectedAttachExitKillsOwnedSession(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	runner := &tmuxTestRunner{failHasSession: true}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, func(ctx context.Context, _ *exec.Cmd, width, height int) (*Terminal, error) {
		return NewManager().StartCommand(ctx, exec.Command("sh", "-c", "exit 7"), width, height)
	})
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = term.Wait(waitCtx)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runner.called("kill-session") {
			waitCleanupCount(t, cleanupCount, 1)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("unexpected attach exit did not kill owned tmux session, calls: %#v", runner.snapshot())
}

func waitCleanupCount(t *testing.T, cleanupCount *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := cleanupCount.Load(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cleanup count = %d, want %d", cleanupCount.Load(), want)
}

func TestTmuxBackedTerminalCleanAttachExitLeavesOwnedSessionRunning(t *testing.T) {
	spec, _ := tmuxTestSpec()
	runner := &tmuxTestRunner{failHasSession: true}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, func(ctx context.Context, _ *exec.Cmd, width, height int) (*Terminal, error) {
		return NewManager().StartCommand(ctx, exec.Command("sh", "-c", "exit 0"), width, height)
	})
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := term.Wait(waitCtx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	if runner.called("kill-session") {
		t.Fatalf("clean attach exit should not kill owned tmux session, calls: %#v", runner.snapshot())
	}
	if err := term.Terminate(); err != nil {
		t.Fatalf("Terminate after clean attach exit returned error: %v", err)
	}
	if runner.called("kill-session") {
		t.Fatalf("terminate after clean attach exit should not kill owned tmux session, calls: %#v", runner.snapshot())
	}
}

func TestTmuxBackedTerminalRealTmuxDetachLeavesSessionAlive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir := t.TempDir()
	sessionName := "approach-test-detach-" + strings.ReplaceAll(filepath.Base(dir), ".", "-")
	scriptPath := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	tmux := func(args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-f", "/dev/null", "-L", sessionName}, args...)...)
	}
	t.Cleanup(func() {
		_ = tmux("kill-session", "-t", sessionName).Run()
	})

	spec := actions.EmbeddedTmuxAgentSpec{
		SessionName:        sessionName,
		ScriptPath:         scriptPath,
		HasSessionCommand:  tmux("has-session", "-t", sessionName),
		NewSessionCommand:  tmux("new-session", "-d", "-s", sessionName, "-c", dir, "exec sh "+scriptPath),
		AttachCommand:      tmux("attach-session", "-t", sessionName),
		KillSessionCommand: tmux("kill-session", "-t", sessionName),
		Cleanup: func() {
			_ = os.Remove(scriptPath)
		},
	}

	term, err := StartTmuxBackedAgent(context.Background(), spec, 40, 10)
	if err != nil {
		t.Fatalf("StartTmuxBackedAgent returned error: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- term.Detach() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Detach returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Detach timed out; Close must not block on a contended emulator snapshot")
	}
	if err := tmux("has-session", "-t", sessionName).Run(); err != nil {
		t.Fatalf("detached tmux session is not alive: %v", err)
	}
}

func TestTmuxBackedTerminalRealTmuxPropagatesAgentFailure(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir := t.TempDir()
	sessionName := "approach-test-fail-" + strings.ReplaceAll(filepath.Base(dir), ".", "-")
	statusPath := filepath.Join(dir, "status.txt")
	scriptPath := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '7\\n' > "+shellQuoteForTest(statusPath)+"\nexit 7\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-f", "/dev/null", "-L", sessionName, "kill-session", "-t", sessionName).Run()
	})

	spec := actions.EmbeddedTmuxAgentSpec{
		SessionName:        sessionName,
		ScriptPath:         scriptPath,
		StatusPath:         statusPath,
		HasSessionCommand:  exec.Command("tmux", "-f", "/dev/null", "-L", sessionName, "has-session", "-t", sessionName),
		NewSessionCommand:  exec.Command("tmux", "-f", "/dev/null", "-L", sessionName, "new-session", "-d", "-s", sessionName, "-c", dir, "exec sh "+shellQuoteForTest(scriptPath)),
		AttachCommand:      exec.Command("/bin/sh", "-c", tmuxAttachStatusScriptForTest, "approach", sessionName, sessionName, statusPath),
		KillSessionCommand: exec.Command("tmux", "-f", "/dev/null", "-L", sessionName, "kill-session", "-t", sessionName),
		Cleanup: func() {
			_ = os.Remove(scriptPath)
			_ = os.Remove(statusPath)
		},
	}
	term, err := StartTmuxBackedAgent(context.Background(), spec, 40, 10)
	if err != nil {
		t.Fatalf("StartTmuxBackedAgent returned error: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := term.Wait(waitCtx); err == nil {
		t.Fatal("Wait returned nil, want propagated agent failure")
	}
	if got := term.State(); got != StateFailed {
		t.Fatalf("State = %q, want %q", got, StateFailed)
	}
}

const tmuxAttachStatusScriptForTest = `tmux -f /dev/null -L "$1" attach-session -t "$2"
tmux_status=$?
if [ -r "$3" ]; then
	IFS= read -r agent_status < "$3"
	rm -f "$3"
	case "$agent_status" in
		""|*[!0-9]*) exit "$tmux_status" ;;
		*) exit "$agent_status" ;;
	esac
fi
exit "$tmux_status"`

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
