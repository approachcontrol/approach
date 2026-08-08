package embeddedterm

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/approachcontrol/approach/actions"
)

type commandRunner func(*exec.Cmd) error

// TmuxBackedTerminal embeds a tmux client while the agent process runs inside
// the tmux session. Detach closes only approach's client; Terminate remains
// destructive for sessions this launch created.
type TmuxBackedTerminal struct {
	term        *Terminal
	target      string
	owned       bool
	killCommand *exec.Cmd
	cleanup     func()
	run         commandRunner

	mu          sync.Mutex
	detached    bool
	terminating bool
	cleaned     bool
}

// StartTmuxBackedAgent starts a detachable embedded agent terminal from spec.
func StartTmuxBackedAgent(ctx context.Context, spec actions.EmbeddedTmuxAgentSpec, width, height int) (*TmuxBackedTerminal, error) {
	return startTmuxBackedAgent(ctx, spec, width, height, runCommand, NewManager().StartCommand)
}

func startTmuxBackedAgent(ctx context.Context, spec actions.EmbeddedTmuxAgentSpec, width, height int, run commandRunner, start func(context.Context, *exec.Cmd, int, int) (*Terminal, error)) (*TmuxBackedTerminal, error) {
	if spec.HasSessionCommand == nil || spec.NewSessionCommand == nil || spec.AttachCommand == nil {
		if spec.Cleanup != nil {
			spec.Cleanup()
		}
		return nil, fmt.Errorf("tmux embedded terminal spec is incomplete")
	}
	owned := false
	if err := run(spec.HasSessionCommand); err != nil {
		if err := run(spec.NewSessionCommand); err != nil {
			if spec.Cleanup != nil {
				spec.Cleanup()
			}
			return nil, err
		}
		owned = true
	} else if spec.Cleanup != nil {
		spec.Cleanup()
		spec.Cleanup = nil
	}

	term, err := start(ctx, spec.AttachCommand, width, height)
	if err != nil {
		if owned && spec.KillSessionCommand != nil {
			_ = run(spec.KillSessionCommand)
		}
		if spec.Cleanup != nil {
			spec.Cleanup()
		}
		return nil, err
	}

	target := spec.DetachTarget
	if target == "" {
		target = spec.SessionName
	}
	t := &TmuxBackedTerminal{
		term:        term,
		target:      target,
		owned:       owned,
		killCommand: spec.KillSessionCommand,
		cleanup:     spec.Cleanup,
		run:         run,
	}
	go t.monitor()
	return t, nil
}

func runCommand(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return err
	}
	return fmt.Errorf("%s: %w", msg, err)
}

func (t *TmuxBackedTerminal) VisibleLines(width, height int) []string {
	return t.term.VisibleLines(width, height)
}

func (t *TmuxBackedTerminal) Write(p []byte) (int, error) { return t.term.Write(p) }

func (t *TmuxBackedTerminal) Resize(width, height int) error {
	return t.term.Resize(width, height)
}

func (t *TmuxBackedTerminal) Wait(ctx context.Context) error {
	err := t.term.Wait(ctx)
	if err == nil {
		t.handleExit(nil)
	}
	return err
}

func (t *TmuxBackedTerminal) State() State { return t.term.State() }

func (t *TmuxBackedTerminal) DetachTarget() string { return t.target }

func (t *TmuxBackedTerminal) Detach() error {
	t.mu.Lock()
	if t.detached {
		t.mu.Unlock()
		return nil
	}
	t.detached = true
	t.owned = false
	t.cleanup = nil
	t.cleaned = true
	t.mu.Unlock()
	err := t.term.Close()
	return err
}

func (t *TmuxBackedTerminal) Terminate() error {
	t.mu.Lock()
	if t.detached {
		t.mu.Unlock()
		return nil
	}
	t.terminating = true
	owned := t.owned && !t.detached
	killCommand := t.killCommand
	run := t.run
	t.mu.Unlock()
	var killErr error
	if owned && killCommand != nil {
		killErr = run(killCommand)
	}
	termErr := t.term.Terminate()
	t.cleanupOnce()
	if killErr != nil {
		return killErr
	}
	return termErr
}

func (t *TmuxBackedTerminal) monitor() {
	err := t.term.Wait(context.Background())
	t.handleExit(err)
	t.cleanupOnce()
}

func (t *TmuxBackedTerminal) handleExit(err error) {
	t.mu.Lock()
	shouldKill := err != nil && t.owned && !t.detached && !t.terminating
	if err == nil && !t.terminating {
		t.detached = true
		t.owned = false
		t.cleanup = nil
		t.cleaned = true
	}
	killCommand := t.killCommand
	run := t.run
	t.mu.Unlock()
	if shouldKill && killCommand != nil {
		_ = run(killCommand)
	}
}

func (t *TmuxBackedTerminal) cleanupOnce() {
	t.mu.Lock()
	if t.cleaned {
		t.mu.Unlock()
		return
	}
	t.cleaned = true
	cleanup := t.cleanup
	t.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
