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
)

type tmuxModeSpy struct {
	tmuxContexts     []actions.AgentLaunchContext
	externalContexts []actions.AgentLaunchContext
	attachCommands   []string
	attachCwds       []string
	attachCmd        *exec.Cmd
	tmuxAvailable    bool
	sessionExists    bool
	tmuxErr          error
	// interactiveFallback makes the default-backend launch take over the TTY
	// instead of detaching, which is what a Linux box with no TERMINAL does.
	interactiveFallback bool
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
			if s.interactiveFallback {
				return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
			}
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
		},
		LaunchDetachedTerminal: func(shellCommand, cwd string) (actions.TerminalLaunchSpec, error) {
			s.attachCommands = append(s.attachCommands, shellCommand)
			s.attachCwds = append(s.attachCwds, cwd)
			// Env stays nil, the way the real seam leaves it, so the caller's own
			// environment hygiene is what the test observes.
			s.attachCmd = exec.Command("true")
			return actions.TerminalLaunchSpec{Cmd: s.attachCmd, Detached: true}, nil
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

// TestModel_WorktreeAgentFallbackNoteReportedOnInteractiveLaunch guards the note
// on the transport that does not detach. Its AgentResultMsg lands only when the
// user's agent session ends, so the note has to be set before the TTY handover
// or it is either lost or announced hours late.
func TestModel_WorktreeAgentFallbackNoteReportedOnInteractiveLaunch(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: false, interactiveFallback: true}
	m := worktreeModel(t, spy.options("tmux"))

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected an interactive launch command")
	}
	if len(spy.externalContexts) != 1 {
		t.Fatalf("external launches = %d, want the fallback launch", len(spy.externalContexts))
	}
	if got := next.TransientError(); !strings.Contains(got, "tmux unavailable") {
		t.Fatalf("status = %q, want the fallback note at launch time", got)
	}
	// The note decorates the launched status rather than repeating its wording.
	if got := next.TransientError(); strings.Count(got, "terminal session") != 1 {
		t.Fatalf("status = %q, want the transport named once", got)
	}

	// An interactive result announces nothing of its own, so the note is not
	// repeated when the session ends hours later. Asserted on a fresh model so a
	// status left over from the launch cannot be mistaken for one set here.
	fresh := worktreeModel(t, (&tmuxModeSpy{tmuxAvailable: false, interactiveFallback: true}).options("tmux"))
	ended := model.AgentResultMsg{LaunchContext: spy.externalContexts[0]}
	after, finalizeCmd := update(fresh, ended)
	if finalizeCmd != nil {
		if msg := finalizeCmd(); msg != nil {
			after, _ = update(after, msg)
		}
	}
	if got := after.TransientError(); got != "" {
		t.Fatalf("status from an interactive result = %q, want none", got)
	}
}

func TestModel_AutofixTmuxLaunchFailureReportsError(t *testing.T) {
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
	// Running approach inside tmux must not hand TMUX to the attaching client:
	// tmux refuses to nest and the attach would fail.
	if spy.attachCmd == nil {
		t.Fatal("expected the attach command captured")
	}
	if len(spy.attachCmd.Env) == 0 {
		t.Fatal("attach command must carry an explicit environment, not inherit one")
	}
	for _, entry := range spy.attachCmd.Env {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "ZELLIJ=") {
			t.Fatalf("attach command must not inherit %q", entry)
		}
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
