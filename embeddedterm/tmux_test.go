package embeddedterm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/actions"
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
		t.Fatalf("detach should not kill owned tmux session, calls: %#v", runner.calls)
	}
	if got := *cleanupCount; got != 1 {
		t.Fatalf("cleanup count = %d, want 1", got)
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
		t.Fatalf("terminate should kill owned tmux session, calls: %#v", runner.calls)
	}
	if got := *cleanupCount; got != 1 {
		t.Fatalf("cleanup count = %d, want 1", got)
	}
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
		t.Fatalf("existing unowned session should not be created or killed, calls: %#v", runner.calls)
	}
	if got := *cleanupCount; got != 1 {
		t.Fatalf("cleanup count = %d, want unused script cleaned once", got)
	}
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
		t.Fatalf("start failure should kill newly-created tmux session, calls: %#v", runner.calls)
	}
	if got := *cleanupCount; got != 1 {
		t.Fatalf("cleanup count = %d, want 1", got)
	}
}

func TestTmuxBackedTerminalCreateFailureCleansScript(t *testing.T) {
	spec, cleanupCount := tmuxTestSpec()
	createErr := errors.New("tmux new-session failed")
	runner := &tmuxTestRunner{failHasSession: true, failNewSession: createErr}
	_, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if !errors.Is(err, createErr) {
		t.Fatalf("error = %v, want %v", err, createErr)
	}
	if got := *cleanupCount; got != 1 {
		t.Fatalf("cleanup count = %d, want 1", got)
	}
}

func tmuxTestSpec() (actions.EmbeddedTmuxAgentSpec, *int) {
	cleanupCount := 0
	return actions.EmbeddedTmuxAgentSpec{
		SessionName:        "wtui-test-agent",
		HasSessionCommand:  exec.Command("tmux", "has-session", "-t", "wtui-test-agent"),
		NewSessionCommand:  exec.Command("tmux", "new-session", "-d", "-s", "wtui-test-agent"),
		AttachCommand:      exec.Command("tmux", "attach-session", "-t", "wtui-test-agent"),
		KillSessionCommand: exec.Command("tmux", "kill-session", "-t", "wtui-test-agent"),
		Cleanup: func() {
			cleanupCount++
		},
	}, &cleanupCount
}

func startSleepTerminal(ctx context.Context, _ *exec.Cmd, width, height int) (*Terminal, error) {
	return NewManager().StartCommand(ctx, exec.Command("sh", "-c", "sleep 10"), width, height)
}

type tmuxTestRunner struct {
	failHasSession bool
	failNewSession error
	calls          [][]string
}

func (r *tmuxTestRunner) run(cmd *exec.Cmd) error {
	r.calls = append(r.calls, append([]string(nil), cmd.Args...))
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
	for _, call := range r.calls {
		if len(call) > 1 && call[1] == subcommand {
			return true
		}
	}
	return false
}

func TestTmuxBackedTerminalDetachTarget(t *testing.T) {
	spec, _ := tmuxTestSpec()
	runner := &tmuxTestRunner{}
	term, err := startTmuxBackedAgent(context.Background(), spec, 20, 3, runner.run, startSleepTerminal)
	if err != nil {
		t.Fatalf("startTmuxBackedAgent returned error: %v", err)
	}
	defer term.Terminate()

	if got := term.DetachTarget(); got != "wtui-test-agent" {
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
			if got := *cleanupCount; got != 1 {
				t.Fatalf("cleanup count = %d, want 1", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("unexpected attach exit did not kill owned tmux session, calls: %#v", runner.calls)
}

func TestTmuxBackedTerminalRealTmuxDetachLeavesSessionAlive(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir := t.TempDir()
	sessionName := "wtui-test-detach-" + strings.ReplaceAll(filepath.Base(dir), ".", "-")
	scriptPath := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	})

	spec := actions.EmbeddedTmuxAgentSpec{
		SessionName:        sessionName,
		ScriptPath:         scriptPath,
		HasSessionCommand:  exec.Command("tmux", "has-session", "-t", sessionName),
		NewSessionCommand:  exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-c", dir, "exec sh "+scriptPath),
		AttachCommand:      exec.Command("tmux", "attach-session", "-t", sessionName),
		KillSessionCommand: exec.Command("tmux", "kill-session", "-t", sessionName),
		Cleanup: func() {
			_ = os.Remove(scriptPath)
		},
	}

	term, err := StartTmuxBackedAgent(context.Background(), spec, 40, 10)
	if err != nil {
		t.Fatalf("StartTmuxBackedAgent returned error: %v", err)
	}
	if err := term.Detach(); err != nil {
		t.Fatalf("Detach returned error: %v", err)
	}
	if err := exec.Command("tmux", "has-session", "-t", sessionName).Run(); err != nil {
		t.Fatalf("detached tmux session is not alive: %v", err)
	}
}
