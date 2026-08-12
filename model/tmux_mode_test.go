package model_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/ui"
)

type tmuxModeSpy struct {
	tmuxContexts     []actions.AgentLaunchContext
	externalContexts []actions.AgentLaunchContext
	attachCommands   []string
	attachCwds       []string
	tmuxAvailable    bool
	sessionExists    bool
	tmuxErr          error
}

func (s *tmuxModeSpy) options(backend string) model.Options {
	return model.Options{
		AgentCommand:          "codex",
		LaunchBackend:         backend,
		TmuxLaunchAvailable:   func() bool { return s.tmuxAvailable },
		RepoTmuxSessionExists: func(string) bool { return s.sessionExists },
		LaunchRepoTmuxAgent: func(ctx actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error) {
			s.tmuxContexts = append(s.tmuxContexts, ctx)
			if s.tmuxErr != nil {
				return actions.RepoTmuxAgentSpec{}, s.tmuxErr
			}
			return actions.RepoTmuxAgentSpec{
				SessionName:   "approach-alpha-0001",
				WindowName:    "codex-abcd1234",
				AttachCommand: "tmux attach -t 'approach-alpha-0001'",
				Launch:        actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true},
			}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			s.externalContexts = append(s.externalContexts, ctx)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
		},
		LaunchDetachedTerminal: func(shellCommand, cwd string) (actions.TerminalLaunchSpec, error) {
			s.attachCommands = append(s.attachCommands, shellCommand)
			s.attachCwds = append(s.attachCwds, cwd)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
		},
	}
}

func worktreeModel(t *testing.T, opts model.Options) model.Model {
	t.Helper()
	m := newTestModel(testRepos(), opts)
	m, _ = update(m, tea.WindowSizeMsg{Width: 400, Height: 40})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	return m
}

func drainTmuxLaunch(t *testing.T, m model.Model, cmd tea.Cmd) model.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a launch command")
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	next, _ := update(m, msg)
	return next
}

func TestModel_WorktreeAgentLaunchUsesRepoTmuxSessionInTmuxMode(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true}
	m := worktreeModel(t, spy.options("tmux"))

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next = drainTmuxLaunch(t, next, cmd)

	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want one", len(spy.tmuxContexts))
	}
	if len(spy.externalContexts) != 0 {
		t.Fatalf("tmux mode must not open an external terminal, got %#v", spy.externalContexts)
	}
	if spy.tmuxContexts[0].WorktreePath != "/dev/alpha" {
		t.Fatalf("worktree path = %q, want /dev/alpha", spy.tmuxContexts[0].WorktreePath)
	}
	if got := next.TransientError(); !strings.Contains(got, "tmux attach -t") {
		t.Fatalf("status = %q, want the attach command", got)
	}
}

func TestModel_WorktreeAgentLaunchStaysExternalOnEmbeddedBackend(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true}
	m := worktreeModel(t, spy.options(""))

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next = drainTmuxLaunch(t, next, cmd)

	if len(spy.tmuxContexts) != 0 {
		t.Fatalf("default backend must not use tmux mode, got %#v", spy.tmuxContexts)
	}
	if len(spy.externalContexts) != 1 {
		t.Fatalf("external launches = %d, want one", len(spy.externalContexts))
	}
	if got := next.TransientError(); strings.Contains(got, "tmux") {
		t.Fatalf("status = %q, want no tmux note", got)
	}
}

func TestModel_WorktreeAgentLaunchFallsBackToExternalWithoutTmux(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: false}
	m := worktreeModel(t, spy.options("tmux"))

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next = drainTmuxLaunch(t, next, cmd)

	if len(spy.tmuxContexts) != 0 {
		t.Fatalf("tmux is unavailable, so no tmux launch may be attempted, got %#v", spy.tmuxContexts)
	}
	if len(spy.externalContexts) != 1 {
		t.Fatalf("external launches = %d, want the fallback launch", len(spy.externalContexts))
	}
	if got := next.TransientError(); !strings.Contains(got, "tmux unavailable") {
		t.Fatalf("status = %q, want the fallback note", got)
	}
}

func TestModel_WorktreeAgentTmuxLaunchFailureReportsError(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, tmuxErr: errors.New("tmux server refused")}
	m := worktreeModel(t, spy.options("tmux"))

	next, _ := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if got := next.TransientError(); !strings.Contains(got, "tmux server refused") {
		t.Fatalf("status = %q, want the launch error", got)
	}
}

func TestModel_AttachKeyOpensExistingRepoTmuxSession(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true}
	m := worktreeModel(t, spy.options("tmux"))

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	if cmd == nil {
		t.Fatal("expected an attach command")
	}
	if len(spy.attachCommands) != 1 {
		t.Fatalf("attach launches = %d, want one", len(spy.attachCommands))
	}
	attach := spy.attachCommands[0]
	// new-session -A would create the session the missing-session branch is
	// supposed to report, so the attach command must never contain it.
	if strings.Contains(attach, "new-session") {
		t.Fatalf("attach command = %q, want attach-only", attach)
	}
	if !strings.Contains(attach, actions.RepoAgentSessionName("/dev/alpha")) {
		t.Fatalf("attach command = %q, want the repo session name", attach)
	}
	if got := next.TransientError(); !strings.Contains(got, "Attaching to tmux session") {
		t.Fatalf("status = %q", got)
	}
}

func TestModel_AttachKeyReportsMissingRepoTmuxSession(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: false}
	m := worktreeModel(t, spy.options("tmux"))

	next, _ := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	if len(spy.attachCommands) != 0 {
		t.Fatalf("a missing session must not open a terminal, got %#v", spy.attachCommands)
	}
	if got := next.TransientError(); !strings.Contains(got, "No tmux session") {
		t.Fatalf("status = %q, want the missing-session error", got)
	}
}

func TestModel_AttachKeyIsInertOnEmbeddedBackend(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true}
	m := worktreeModel(t, spy.options(""))

	next, _ := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	if len(spy.attachCommands) != 0 {
		t.Fatalf("T must do nothing on the default backend, got %#v", spy.attachCommands)
	}
	if got := next.TransientError(); got != "" {
		t.Fatalf("status = %q, want none", got)
	}
}

// TestModel_AttachHintFollowsBackend pins the plumbing between the Model's
// backend and the rendered shortcut bar; ui_test owns the hint's own rules.
func TestModel_AttachHintFollowsBackend(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true}
	if !strings.Contains(worktreeModel(t, spy.options("tmux")).View(), "attach tmux") {
		t.Fatal("expected tmux mode to offer the attach affordance")
	}
	if strings.Contains(worktreeModel(t, spy.options("")).View(), "attach tmux") {
		t.Fatal("the default backend has no per-repo tmux session to attach to")
	}

	noTmux := &tmuxModeSpy{tmuxAvailable: false}
	if strings.Contains(worktreeModel(t, noTmux.options("tmux")).View(), "attach tmux") {
		t.Fatal("tmux mode without tmux installed cannot attach")
	}
}

var _ = ui.PaneRepos
