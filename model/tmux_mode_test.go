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
	sessionAttached  bool
	insideMux        bool
	attachedProbes   int
	tmuxErr          error
	detachedErr      error
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
		RepoTmuxSessionAttached: func(string) bool {
			s.attachedProbes++
			return s.sessionAttached
		},
		InsideMultiplexer: func() bool { return s.insideMux },
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
			if s.detachedErr != nil {
				return actions.TerminalLaunchSpec{}, s.detachedErr
			}
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

// drainTmuxLaunch delivers a launch's own result. A tmux-mode launch batches
// the spawn with the per-repo terminal attempt, so the batch is flattened here;
// the commands each message returns in turn are deliberately not followed,
// since one of them is the status-expiry tick that would clear what the caller
// is about to assert on.
func drainTmuxLaunch(t *testing.T, m model.Model, cmd tea.Cmd) model.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a launch command")
	}
	return deliverLaunchMessages(m, cmd)
}

func deliverLaunchMessages(m model.Model, cmd tea.Cmd) model.Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			m = deliverLaunchMessages(m, sub)
		}
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

func launchInTmuxMode(t *testing.T, spy *tmuxModeSpy) model.Model {
	t.Helper()
	m := worktreeModel(t, spy.options("tmux"))
	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	return deliverLaunchMessages(next, cmd)
}

// TestModel_FirstTmuxLaunchOpensRepoTerminal is the feature: a tmux-mode launch
// into a repo nobody is watching opens the terminal attached to that repo's
// session, instead of only printing the attach command.
func TestModel_FirstTmuxLaunchOpensRepoTerminal(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true}
	next := launchInTmuxMode(t, spy)

	if len(spy.attachCommands) != 1 {
		t.Fatalf("terminal launches = %d, want one", len(spy.attachCommands))
	}
	attach := spy.attachCommands[0]
	// new-session -A would create a session this terminal is only meant to
	// join, which would leave an empty session behind if it lost the race.
	if strings.Contains(attach, "new-session") {
		t.Fatalf("terminal command = %q, want attach-only", attach)
	}
	if !strings.Contains(attach, "=approach-alpha-0001") {
		t.Fatalf("terminal command = %q, want the launch's own session, exactly targeted", attach)
	}
	if len(spy.attachCwds) != 1 || spy.attachCwds[0] != "/dev/alpha" {
		t.Fatalf("terminal cwds = %#v, want the repo path", spy.attachCwds)
	}
	// A client that inherits TMUX refuses to nest.
	if spy.attachCmd == nil {
		t.Fatal("expected the terminal command captured")
	}
	for _, entry := range spy.attachCmd.Env {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "ZELLIJ=") {
			t.Fatalf("terminal command must not inherit %q", entry)
		}
	}
	// The launch's own status stands: it already names window, session, and the
	// attach command.
	if got := next.TransientError(); !strings.Contains(got, "tmux attach -t") {
		t.Fatalf("status = %q, want the launch status left alone", got)
	}
}

// TestModel_SecondTmuxLaunchAddsWindowWithoutSecondTerminal covers the other
// half of the requirement: once a terminal is watching the repo's session, a
// launch adds a tmux window to it and opens nothing.
func TestModel_SecondTmuxLaunchAddsWindowWithoutSecondTerminal(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true, sessionAttached: true}
	launchInTmuxMode(t, spy)

	if spy.attachedProbes == 0 {
		t.Fatal("expected the attached probe to decide this")
	}
	if len(spy.attachCommands) != 0 {
		t.Fatalf("an attached session must open no second terminal, got %#v", spy.attachCommands)
	}
	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want the window still launched", len(spy.tmuxContexts))
	}
}

// TestModel_TmuxTerminalPendingSuppressesASecondSpawn covers the window the
// probe cannot see: two launches seconds apart, where the second would ask
// `list-clients` before the first terminal's client has registered.
func TestModel_TmuxTerminalPendingSuppressesASecondSpawn(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true}
	m := worktreeModel(t, spy.options("tmux"))

	// The first launch's terminal command is held rather than run, which is
	// exactly the state a real in-flight spawn is in.
	first, firstCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if firstCmd == nil {
		t.Fatal("expected a launch command")
	}
	second, secondCmd := update(first, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	second = deliverLaunchMessages(second, secondCmd)
	if len(spy.attachCommands) != 0 {
		t.Fatalf("a pending terminal must suppress the second spawn, got %#v", spy.attachCommands)
	}

	// Once the pending attempt reports back, a later launch may spawn again —
	// the flag is a debounce, not a permanent latch.
	third := deliverLaunchMessages(second, firstCmd)
	if len(spy.attachCommands) != 1 {
		t.Fatalf("terminal launches after the first result = %d, want one", len(spy.attachCommands))
	}
	fourth, fourthCmd := update(third, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	deliverLaunchMessages(fourth, fourthCmd)
	if len(spy.attachCommands) != 2 {
		t.Fatalf("terminal launches after the flag cleared = %d, want a fresh spawn", len(spy.attachCommands))
	}
}

// TestModel_TmuxLaunchInsideMultiplexerOpensNothing preserves today's behavior
// for a user already running approach inside tmux or Zellij, where a nested
// attach would refuse to run anyway.
func TestModel_TmuxLaunchInsideMultiplexerOpensNothing(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true, insideMux: true}
	next := launchInTmuxMode(t, spy)

	if len(spy.attachCommands) != 0 {
		t.Fatalf("inside a multiplexer nothing may be opened, got %#v", spy.attachCommands)
	}
	if spy.attachedProbes != 0 {
		t.Fatalf("inside a multiplexer the probe is not worth running, ran %d times", spy.attachedProbes)
	}
	if got := next.TransientError(); !strings.Contains(got, "tmux attach -t") {
		t.Fatalf("status = %q, want today's attach-command status", got)
	}
}

// TestModel_TmuxLaunchSurvivesAnUnavailableTerminal is the degradation
// requirement: the agent is already running by the time the terminal is tried,
// so a machine with no terminal configured loses the window and nothing else.
func TestModel_TmuxLaunchSurvivesAnUnavailableTerminal(t *testing.T) {
	spy := &tmuxModeSpy{
		tmuxAvailable: true,
		sessionExists: true,
		detachedErr:   errors.New("set $TERMINAL or [terminal].command"),
	}
	next := launchInTmuxMode(t, spy)

	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want the agent launched anyway", len(spy.tmuxContexts))
	}
	got := next.TransientError()
	if !strings.Contains(got, "tmux attach -t") {
		t.Fatalf("status = %q, want the attach command the user now needs", got)
	}
	if !strings.Contains(got, "set $TERMINAL or [terminal].command") {
		t.Fatalf("status = %q, want the terminal error named", got)
	}
}

// TestModel_TmuxLaunchWithoutAVisibleSessionOpensNothing covers the spawn that
// returns before tmux lists the session. Giving up is quiet: the launch already
// reported its own outcome.
func TestModel_TmuxLaunchWithoutAVisibleSessionOpensNothing(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: false}
	next := launchInTmuxMode(t, spy)

	if len(spy.attachCommands) != 0 {
		t.Fatalf("no visible session means no terminal, got %#v", spy.attachCommands)
	}
	if got := next.TransientError(); !strings.Contains(got, "tmux attach -t") {
		t.Fatalf("status = %q, want the launch status unchanged", got)
	}
	if got := next.TransientError(); strings.Contains(got, "terminal unavailable") {
		t.Fatalf("status = %q, want no error noise for a session that never appeared", got)
	}
}

// TestModel_EmbeddedBackendOpensNoRepoTerminal keeps the feature inside tmux
// mode: the default backend's launch path is untouched.
func TestModel_EmbeddedBackendOpensNoRepoTerminal(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, sessionExists: true}
	m := worktreeModel(t, spy.options(""))
	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	deliverLaunchMessages(next, cmd)

	if len(spy.attachCommands) != 0 {
		t.Fatalf("the embedded backend must open no tmux terminal, got %#v", spy.attachCommands)
	}
	if spy.attachedProbes != 0 {
		t.Fatalf("the embedded backend must run no tmux probe, ran %d times", spy.attachedProbes)
	}
}
