package model

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/sessions"
)

type tmuxResumeSpy struct {
	t             *testing.T
	tmuxContexts  []actions.AgentLaunchContext
	embeddedStart int
}

func (s *tmuxResumeSpy) model(backend string, tmuxAvailable bool) Model {
	return NewWithOptions(nil, Options{
		AgentCommand:        "codex",
		LaunchBackend:       backend,
		TmuxLaunchAvailable: func() bool { return tmuxAvailable },
		LaunchRepoTmuxAgent: func(ctx actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error) {
			s.tmuxContexts = append(s.tmuxContexts, ctx)
			return actions.RepoTmuxAgentSpec{
				SessionName:   "approach-alpha-0001",
				WindowName:    "codex-abcd1234",
				AttachCommand: "tmux attach -t 'approach-alpha-0001'",
				Launch:        actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true},
			}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			s.embeddedStart++
			return flowPhaseLaunchTestTerminal{state: "running"}, nil
		},
	})
}

func resumeContext() actions.AgentLaunchContext {
	return actions.AgentLaunchContext{
		Command:         "codex",
		LaunchID:        "launch-1",
		RepoPath:        "/dev/alpha",
		WorktreePath:    "/dev/alpha",
		ResumeSessionID: "session-1",
	}
}

func TestSessionResumeRunsInRepoTmuxSessionInTmuxMode(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.model("tmux", true)
	released := false

	next, cmd := m.resumeSessionForBackend(resumeContext(), sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "session-1",
	}, func() { released = true })

	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want one", len(spy.tmuxContexts))
	}
	if spy.embeddedStart != 0 {
		t.Fatal("tmux mode must not open an embedded terminal for a session resume")
	}
	if spy.tmuxContexts[0].Embedded {
		t.Fatal("tmux resume context must not be embedded")
	}
	if next.hasRunningEmbeddedTerminal() {
		t.Fatal("tmux resume must not install an embedded slot")
	}
	// The tmux branch takes over the resume reservation, so it releases when
	// the spawn returns rather than at the branch point.
	if released {
		t.Fatal("reservation must cover the spawn, not be released before it")
	}
	if cmd == nil {
		t.Fatal("expected a launch command")
	}
	msg, ok := cmd().(AgentResultMsg)
	if !ok {
		t.Fatalf("launch message = %#v, want AgentResultMsg", msg)
	}
	if !released {
		t.Fatal("expected the reservation released once the spawn returned")
	}
	if !msg.Detached {
		t.Fatal("tmux resume result must be detached so hooks own completion")
	}
	if !strings.Contains(msg.LaunchedStatus, "tmux attach -t") {
		t.Fatalf("launched status = %q, want the attach command", msg.LaunchedStatus)
	}
}

func TestSessionResumeFallsBackToEmbeddedWithoutTmux(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.model("tmux", false)

	next, _ := m.resumeSessionForBackend(resumeContext(), sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "session-1",
	}, nil)

	if len(spy.tmuxContexts) != 0 {
		t.Fatalf("tmux is unavailable, so no tmux launch may be attempted, got %#v", spy.tmuxContexts)
	}
	if spy.embeddedStart != 1 {
		t.Fatalf("embedded starts = %d, want the fallback resume", spy.embeddedStart)
	}
	if !strings.Contains(next.status.Text, "tmux unavailable") {
		t.Fatalf("status = %q, want the fallback note", next.status.Text)
	}
}

func flowPhaseResumeTmuxContext() actions.AgentLaunchContext {
	ctx := resumeContext()
	ctx.FlowID = "flow-1"
	ctx.FlowPhaseID = "implementation"
	ctx.FlowLaunchTracked = true
	// launchTrackedFlowPhaseResumeWithContext hardcodes this; the tmux handoff
	// has to clear it or the prompt would wait for a dock prefill.
	ctx.Embedded = true
	return ctx
}

func TestFlowPhaseResumeRunsInRepoTmuxSessionInTmuxMode(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.model("tmux", true)
	ctx := flowPhaseResumeTmuxContext()
	key, ok := newFlowPhaseResumeKey(ctx.FlowID, ctx.FlowPhaseID)
	if !ok {
		t.Fatal("expected a valid resume key")
	}
	m = m.withPendingFlowPhaseResume(key, ctx.LaunchID)
	released := false

	next, cmd := m.handleFlowPhaseResumePersisted(flowPhaseResumePersistedMsg{
		LaunchContext: ctx,
		LaunchRelease: func() { released = true },
	})

	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want one", len(spy.tmuxContexts))
	}
	if spy.tmuxContexts[0].Embedded {
		t.Fatal("the tmux handoff must clear the hardcoded Embedded flag")
	}
	if spy.embeddedStart != 0 {
		t.Fatal("tmux mode must not open an embedded terminal for a phase resume")
	}
	if next.hasPendingFlowPhaseResumeForFlow("flow-1") {
		t.Fatal("the pending resume must be consumed")
	}
	if cmd == nil {
		t.Fatal("expected a launch command")
	}
	drainResumeCmd(t, cmd)
	if !released {
		t.Fatal("expected the reservation released once the spawn returned")
	}
}

func TestFlowPhaseResumeFallsBackToEmbeddedWithoutTmux(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.model("tmux", false)
	ctx := flowPhaseResumeTmuxContext()
	key, _ := newFlowPhaseResumeKey(ctx.FlowID, ctx.FlowPhaseID)
	m = m.withPendingFlowPhaseResume(key, ctx.LaunchID)
	released := false

	next, _ := m.handleFlowPhaseResumePersisted(flowPhaseResumePersistedMsg{
		LaunchContext: ctx,
		LaunchRelease: func() { released = true },
	})

	if len(spy.tmuxContexts) != 0 {
		t.Fatalf("tmux is unavailable, so no tmux launch may be attempted, got %#v", spy.tmuxContexts)
	}
	if spy.embeddedStart != 1 {
		t.Fatalf("embedded starts = %d, want the fallback resume", spy.embeddedStart)
	}
	if !released {
		t.Fatal("the embedded branch releases the reservation on return")
	}
	if !strings.Contains(next.status.Text, "tmux unavailable") {
		t.Fatalf("status = %q, want the fallback note", next.status.Text)
	}
}

func drainResumeCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, batched := range batch {
		if batched != nil {
			batched()
		}
	}
}
