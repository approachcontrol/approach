package model

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

type tmuxResumeSpy struct {
	t             *testing.T
	tmuxContexts  []actions.AgentLaunchContext
	embeddedStart int
	// launchErr makes the tmux builder fail, which is the only way to reach the
	// build-error branch of the tmux routes.
	launchErr error
}

func (s *tmuxResumeSpy) model(backend string, tmuxAvailable bool) Model {
	return NewWithOptions(nil, Options{
		AgentCommand:        "codex",
		LaunchBackend:       backend,
		TmuxLaunchAvailable: func() bool { return tmuxAvailable },
		LaunchRepoTmuxAgent: func(ctx actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error) {
			s.tmuxContexts = append(s.tmuxContexts, ctx)
			if s.launchErr != nil {
				return actions.RepoTmuxAgentSpec{}, s.launchErr
			}
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

// modelWithRepos is model() for the handlers that gate on a selected repo.
func (s *tmuxResumeSpy) modelWithRepos(backend string, tmuxAvailable bool) Model {
	m := s.model(backend, tmuxAvailable)
	m.repos = m.repos.SetItems(flowRefreshTestRepos())
	return m
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

// TestFlowPhaseResumeReleasesReservationExactlyOnce counts releases rather than
// recording a bool: the resume paths converted a deferred release into explicit
// calls on every branch, so a double release has to fail as loudly as a leak.
func TestFlowPhaseResumeReleasesReservationExactlyOnce(t *testing.T) {
	tests := []struct {
		name      string
		available bool
		launchErr error
		pending   bool
	}{
		{name: "tmux route", available: true, pending: true},
		{name: "tmux build error", available: true, launchErr: errTmuxSpyLaunch, pending: true},
		{name: "embedded fallback", pending: true},
		// The early return for a resume no longer pending: nothing launches, and
		// the reservation still has to be handed back.
		{name: "stale resume", available: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy := &tmuxResumeSpy{t: t, launchErr: tc.launchErr}
			m := spy.model("tmux", tc.available)
			ctx := flowPhaseResumeTmuxContext()
			if tc.pending {
				key, ok := newFlowPhaseResumeKey(ctx.FlowID, ctx.FlowPhaseID)
				if !ok {
					t.Fatal("expected a valid resume key")
				}
				m = m.withPendingFlowPhaseResume(key, ctx.LaunchID)
			}
			releases := 0

			_, cmd := m.handleFlowPhaseResumePersisted(flowPhaseResumePersistedMsg{
				LaunchContext: ctx,
				LaunchRelease: func() { releases++ },
			})
			if cmd != nil {
				drainResumeCmd(t, cmd)
			}

			if releases != 1 {
				t.Fatalf("releases = %d, want exactly one", releases)
			}
		})
	}
}

var errTmuxSpyLaunch = errors.New("tmux server refused the window")

// TestAttachKeyReachesEveryBoundPaneAndIsInertOtherwise covers all three T
// bindings. The footer advertises the affordance globally in tmux mode, so each
// bound handler has to reach the attach path; on the default backend each has to
// fall through the switch without consuming the key or setting a status.
func TestAttachKeyReachesEveryBoundPaneAndIsInertOtherwise(t *testing.T) {
	panes := map[string]func(Model) (tea.Model, tea.Cmd){
		"repos pane": func(m Model) (tea.Model, tea.Cmd) { return m.handleLeftPaneKey("T") },
		"content pane": func(m Model) (tea.Model, tea.Cmd) {
			m.activeFlowSurface = false
			return m.handleRightPaneKey("T")
		},
		"active flow surface": func(m Model) (tea.Model, tea.Cmd) {
			// handleRightPaneKey delegates here when the surface is visible.
			m.activeFlowSurface = true
			m.contentPane = ui.PaneTop
			m.topMode = ui.ModeFlows
			return m.handleRightPaneKey("T")
		},
	}
	for name, press := range panes {
		t.Run(name, func(t *testing.T) {
			spy := &tmuxResumeSpy{t: t}
			// No repos are selected, so reaching the attach path is observable as
			// its own refusal rather than as an actual attach.
			next, _ := press(spy.model("tmux", true))
			if got := next.(Model).status.Text; !strings.Contains(got, "Select a repository") {
				t.Fatalf("tmux mode status = %q, want T to reach the attach path", got)
			}

			inert, cmd := press(spy.model("", true))
			if got := inert.(Model).status.Text; got != "" {
				t.Fatalf("default backend status = %q, want T inert", got)
			}
			if cmd != nil {
				t.Fatal("default backend must not act on T")
			}
		})
	}
}

// TestLiveTmuxWindowRefusesResetAndResume pins the live-agent guard that
// replaces the embedded slot in tmux mode. Persisted state cannot answer the
// question — a Claude window records nothing until it exits and a Codex window
// records an `ended` session after its first turn — so both hazardous
// transitions probe the window instead, and only here.
func TestLiveTmuxWindowRefusesResetAndResume(t *testing.T) {
	// Two launches, because the probe must ask about every launch the phase made:
	// an earlier window can outlive a later launch that already exited, and asking
	// about the newest alone would admit a reset while that agent is still running.
	phase := flowstore.FlowPhase{
		PhaseID:   "implementation",
		Kind:      flowstore.KindImplementation,
		Status:    flowstore.PhaseRunning,
		LaunchIDs: []string{"launch-1", "launch-2"},
	}
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Status:   flowstore.StatusInProgress,
		Phases:   []flowstore.FlowPhase{phase},
	}

	tests := []struct {
		name       string
		backend    string
		windowLive bool
		wantReset  bool
	}{
		{name: "tmux mode with a live window", backend: "tmux", windowLive: true},
		{name: "tmux mode with no live window", backend: "tmux", wantReset: true},
		// The probe is tmux-mode only: the embedded backend keeps using its slot.
		{name: "embedded backend never probes", backend: "", windowLive: true, wantReset: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy := &tmuxResumeSpy{t: t}
			probes := 0
			m := spy.modelWithRepos(tc.backend, true)
			m.repoTmuxLaunchWindowLive = func(repoPath string, launchIDs ...string) bool {
				probes++
				if repoPath != "/dev/alpha" || strings.Join(launchIDs, ",") != "launch-1,launch-2" {
					t.Fatalf("probe args = %q %q, want the phase's repo and every launch it made", repoPath, launchIDs)
				}
				return tc.windowLive
			}
			m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})

			next, cmd := m.handleFlowPhaseResetConfirmed(flowPhaseResetConfirmedMsg{
				FlowID:   record.FlowID,
				PhaseID:  phase.PhaseID,
				RepoPath: record.RepoPath,
			})
			if reset := cmd != nil; reset != tc.wantReset {
				t.Fatalf("reset issued = %t, want %t (status %q)", reset, tc.wantReset, next.status.Text)
			}
			if !tc.wantReset && !strings.Contains(next.status.Text, "running in tmux") {
				t.Fatalf("status = %q, want the live-window refusal", next.status.Text)
			}
			if tc.backend == "" && probes != 0 {
				t.Fatalf("probes = %d, want none on the embedded backend", probes)
			}
		})
	}
}

// TestLiveTmuxWindowRefusesRepeatResumeOfTerminalPhase covers the resume half of
// the guard. A completed phase stays resumable, and resuming it twice would run
// the same provider session in two windows.
func TestLiveTmuxWindowRefusesRepeatResumeOfTerminalPhase(t *testing.T) {
	phase := flowstore.FlowPhase{
		PhaseID:   "implementation",
		Kind:      flowstore.KindImplementation,
		Status:    flowstore.PhaseCompleted,
		LaunchIDs: []string{"launch-1"},
		Sessions: []flowstore.Session{
			{Provider: "codex", SessionID: "session-1", LaunchID: "launch-1", Status: "ended"},
		},
	}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Status:       flowstore.StatusInProgress,
		Phases:       []flowstore.FlowPhase{phase},
	}

	for _, windowLive := range []bool{true, false} {
		name := "no live window"
		if windowLive {
			name = "live window"
		}
		t.Run(name, func(t *testing.T) {
			spy := &tmuxResumeSpy{t: t}
			m := spy.modelWithRepos("tmux", true)
			m.repoTmuxLaunchWindowLive = func(string, ...string) bool { return windowLive }
			m = modelWithModeForTest(m, ui.ModeFlows)
			m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
			m.expandedFlowID = record.FlowID
			m.selectedFlowPhaseID = phase.PhaseID

			nextModel, _ := m.handleResumeFlowPhaseSession()
			next := nextModel.(Model)

			if windowLive {
				if next.hasPendingFlowPhaseResumeForFlow(record.FlowID) {
					t.Fatal("a live window must not admit a second resume")
				}
				if !strings.Contains(next.status.Text, "running in tmux") {
					t.Fatalf("status = %q, want the live-window refusal", next.status.Text)
				}
				return
			}
			if !next.hasPendingFlowPhaseResumeForFlow(record.FlowID) {
				t.Fatalf("no live window must admit the resume, status = %q", next.status.Text)
			}
		})
	}
}

// TestLiveTmuxWindowRefusesSessionRecordResume covers the resumes that start
// from a session record rather than a Flow phase — sessions view, the inline
// worktree session list, and the dock's session picker. They mint a fresh launch
// ID and drop Flow identity, so the phase-scoped guard can never see them; the
// record's own LaunchID is what ties it back to a live window. Codex makes this
// routine: its Stop hook records an `ended` session after each turn while the
// CLI stays open, so the record offered for resume often still has an agent.
func TestLiveTmuxWindowRefusesSessionRecordResume(t *testing.T) {
	record := sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "session-1",
		LaunchID:  "launch-1",
		RepoPath:  "/dev/alpha",
		CWD:       "/dev/alpha",
		Status:    "ended",
	}

	t.Run("refuses while the record's own window is live", func(t *testing.T) {
		spy := &tmuxResumeSpy{t: t}
		m := spy.modelWithRepos("tmux", true)
		probed := ""
		var probedLaunchIDs []string
		m.repoTmuxLaunchWindowLive = func(repoPath string, launchIDs ...string) bool {
			probed = repoPath
			probedLaunchIDs = launchIDs
			return true
		}

		_, release, ok, next := m.sessionResumeLaunchContext(record)
		if ok {
			t.Fatal("a live window must not admit a second resume of the same session")
		}
		if release != nil {
			t.Fatal("a refusal must not hand back a reservation to release")
		}
		if next.status.Text != tmuxSessionLiveWindowRefusal {
			t.Fatalf("status = %q, want the live-window refusal", next.status.Text)
		}
		// The record's launch, not the resume's new one: only the launch that
		// opened the window can be matched against a window name.
		if probed != "/dev/alpha" || len(probedLaunchIDs) != 1 || probedLaunchIDs[0] != "launch-1" {
			t.Fatalf("probed repo %q launches %#v, want /dev/alpha and the record's launch-1", probed, probedLaunchIDs)
		}
	})

	t.Run("admits the resume when no window is live", func(t *testing.T) {
		spy := &tmuxResumeSpy{t: t}
		m := spy.modelWithRepos("tmux", true)
		m.repoTmuxLaunchWindowLive = func(string, ...string) bool { return false }

		ctx, _, ok, next := m.sessionResumeLaunchContext(record)
		if !ok {
			t.Fatalf("no live window must admit the resume, status = %q", next.status.Text)
		}
		if ctx.ResumeSessionID != "session-1" {
			t.Fatalf("resume session = %q, want session-1", ctx.ResumeSessionID)
		}
	})

	t.Run("the default backend never probes", func(t *testing.T) {
		spy := &tmuxResumeSpy{t: t}
		m := spy.modelWithRepos("embedded", true)
		m.repoTmuxLaunchWindowLive = func(string, ...string) bool {
			t.Fatal("the embedded backend routes no launch to tmux and must not probe")
			return true
		}

		if _, _, ok, next := m.sessionResumeLaunchContext(record); !ok {
			t.Fatalf("the default backend must admit the resume, status = %q", next.status.Text)
		}
	})
}

// TestRenderPathNeverProbesTmux guards the placement rule the probe depends on.
// flowPhaseResettable feeds the footer hint, so it is evaluated on every frame;
// probing there would fork a tmux process per render.
func TestRenderPathNeverProbesTmux(t *testing.T) {
	phase := flowstore.FlowPhase{
		PhaseID:   "implementation",
		Kind:      flowstore.KindImplementation,
		Status:    flowstore.PhaseRunning,
		LaunchIDs: []string{"launch-1"},
	}
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Status:   flowstore.StatusInProgress,
		Phases:   []flowstore.FlowPhase{phase},
	}
	spy := &tmuxResumeSpy{t: t}
	m := spy.modelWithRepos("tmux", true)
	m.repoTmuxLaunchWindowLive = func(string, ...string) bool {
		t.Fatal("a render-path predicate must never probe tmux")
		return false
	}
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.expandedFlowID = record.FlowID
	m.selectedFlowPhaseID = phase.PhaseID
	m.width, m.height = 200, 40

	if !m.flowPhaseResettable(record, phase) {
		t.Fatal("fixture must be resettable, or this proves nothing about probing")
	}
	m.View()
}

// TestAttachUsesTheSelectedFlowsRepoOnFlowSurfaces covers the repo-independent
// surface: active flows list every repo's Flows, so attaching to the left pane's
// selection would open an unrelated repo's agents.
func TestAttachUsesTheSelectedFlowsRepoOnFlowSurfaces(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/beta",
		Status:   flowstore.StatusInProgress,
	}
	spy := &tmuxResumeSpy{t: t}
	m := spy.modelWithRepos("tmux", true)
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})

	if repoPath, ok := m.tmuxAttachRepoPath(); !ok || repoPath != "/dev/beta" {
		t.Fatalf("attach repo = %q (%t), want the selected Flow's /dev/beta", repoPath, ok)
	}

	// Off a Flow surface the left pane's selection is the only answer.
	worktrees := modelWithModeForTest(spy.modelWithRepos("tmux", true), ui.ModeWorktrees)
	if repoPath, ok := worktrees.tmuxAttachRepoPath(); !ok || repoPath != "/dev/alpha" {
		t.Fatalf("attach repo = %q (%t), want the selected repo /dev/alpha", repoPath, ok)
	}
}

// TestLiveTmuxWindowProbeIsAdvisoryOnly keeps a failing or unavailable probe from
// blocking recovery: it may only ever report "no evidence of a live window".
func TestLiveTmuxWindowProbeIsAdvisoryOnly(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.model("tmux", false)
	m.repoTmuxLaunchWindowLive = func(string, ...string) bool {
		t.Fatal("an unavailable tmux must not be probed")
		return true
	}
	if m.tmuxLaunchWindowLive("/dev/alpha", []string{"launch-1"}) {
		t.Fatal("tmux unavailable must report no live window")
	}

	available := spy.model("tmux", true)
	available.repoTmuxLaunchWindowLive = func(string, ...string) bool { return false }
	if available.tmuxLaunchWindowLive("", []string{"launch-1"}) {
		t.Fatal("an unknown repo must report no live window")
	}
	if available.tmuxLaunchWindowLive("/dev/alpha", nil) {
		t.Fatal("an unknown launch must report no live window")
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

// TestLiveTmuxWindowRefusesFlowRepair covers the third hazardous transition. On
// the embedded backend the running slot is what stops R from putting an
// untracked second agent in a worktree that already has one; in tmux mode there
// is no slot, and a phase running in a window records no session, so it reads as
// recoverable and arms repair while its agent is mid-run.
func TestLiveTmuxWindowRefusesFlowRepair(t *testing.T) {
	// A running phase with launches but no session: exactly what a live Claude
	// window looks like, since its hook only fires at SessionEnd. A completed
	// earlier phase carries its own launches, which a Flow-wide repair must also
	// ask about — a phase-scoped probe would miss a window still open on it.
	phases := []flowstore.FlowPhase{
		{
			PhaseID:   "plan",
			Kind:      flowstore.KindPlan,
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-0"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "session-0", LaunchID: "launch-0", Status: "ended"},
			},
		},
		{
			PhaseID:   "implementation",
			Kind:      flowstore.KindImplementation,
			Status:    flowstore.PhaseRunning,
			LaunchIDs: []string{"launch-1", "launch-2"},
		},
	}
	// A real directory: repair refuses to launch into a path it cannot resolve.
	repoPath := t.TempDir()
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     repoPath,
		WorktreePath: repoPath,
		Status:       flowstore.StatusInProgress,
		Phases:       phases,
	}

	tests := []struct {
		name       string
		backend    string
		windowLive bool
		wantRepair bool
	}{
		{name: "tmux mode with a live window", backend: "tmux", windowLive: true},
		{name: "tmux mode with no live window", backend: "tmux", wantRepair: true},
		// The probe is tmux-mode only: the embedded backend keeps its slot fence.
		{name: "embedded backend never probes", backend: "", windowLive: true, wantRepair: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy := &tmuxResumeSpy{t: t}
			m := spy.modelWithRepos(tc.backend, true)
			m.repoTmuxLaunchWindowLive = func(probed string, launchIDs ...string) bool {
				if probed != repoPath {
					t.Fatalf("probe repo = %q, want the Flow's repo %q", probed, repoPath)
				}
				// Repair is Flow-wide, so it asks about every launch the Flow made,
				// across every phase — not just the one the obstruction names.
				if strings.Join(launchIDs, ",") != "launch-0,launch-1,launch-2" {
					t.Fatalf("probe launches = %#v, want every launch of the Flow", launchIDs)
				}
				return tc.windowLive
			}
			m = modelWithModeForTest(m, ui.ModeFlows)
			m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
			m.activeFlowSurface = false
			if !m.selectedFlowRepairReady() {
				t.Fatal("fixture must be repair-ready, or this proves nothing about the probe")
			}

			nextModel, cmd := m.handleRepairSelectedFlow()
			next := nextModel.(Model)
			if tc.wantRepair {
				if cmd == nil {
					t.Fatal("expected the repair to start")
				}
				return
			}
			if cmd != nil {
				t.Fatal("a live tmux window must start no repair agent")
			}
			if next.status.Text != tmuxFlowLiveWindowRefusal {
				t.Fatalf("status = %q, want %q", next.status.Text, tmuxFlowLiveWindowRefusal)
			}
		})
	}
}

// TestFlowRepairReadinessNeverProbesTmux keeps the subprocess off the render
// path: selectedFlowRepairReady feeds the footer and runs every frame.
func TestFlowRepairReadinessNeverProbesTmux(t *testing.T) {
	phase := flowstore.FlowPhase{
		PhaseID:   "implementation",
		Kind:      flowstore.KindImplementation,
		Status:    flowstore.PhaseRunning,
		LaunchIDs: []string{"launch-1"},
	}
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Status:   flowstore.StatusInProgress,
		Phases:   []flowstore.FlowPhase{phase},
	}
	spy := &tmuxResumeSpy{t: t}
	m := spy.modelWithRepos("tmux", true)
	m.repoTmuxLaunchWindowLive = func(string, ...string) bool {
		t.Fatal("a render-path predicate must never probe tmux")
		return false
	}
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.activeFlowSurface = false
	if !m.selectedFlowRepairReady() {
		t.Fatal("fixture must be repair-ready, or this proves nothing about probing")
	}
}

// TestWorktreeSessionResumeRoutesToTmux covers the inline session list's enter
// resume. On the default backend it opens an external terminal rather than the
// embedded dock, so it belongs with the other external-terminal launches that
// tmux mode moves into the repo's session.
func TestWorktreeSessionResumeRoutesToTmux(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.model("tmux", true)

	next, cmd := m.launchAgentForBackend(resumeContext(), nil)

	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want one", len(spy.tmuxContexts))
	}
	if spy.tmuxContexts[0].Embedded {
		t.Fatal("a tmux launch context must not be embedded")
	}
	if next.hasRunningEmbeddedTerminal() {
		t.Fatal("a tmux launch must not install an embedded slot")
	}
	if cmd == nil {
		t.Fatal("expected a launch command")
	}
}

// TestResumeFallbackNoteSurvivesTheExternalFallthrough covers the one resume
// path that leaves the embedded terminal: an agent with no embedded slot falls
// through to a detached external launch whose own result message would
// otherwise overwrite the fallback note with the generic launch text.
func TestResumeFallbackNoteSurvivesTheExternalFallthrough(t *testing.T) {
	m := NewWithOptions(nil, Options{
		AgentCommand:        "codex",
		LaunchBackend:       "tmux",
		TmuxLaunchAvailable: func() bool { return false },
		LaunchRepoTmuxAgent: func(actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error) {
			t.Fatal("tmux is unavailable, so no tmux launch may be attempted")
			return actions.RepoTmuxAgentSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return nil, pty.ErrUnsupported
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
		},
	})

	next, cmd := m.resumeSessionForBackend(resumeContext(), sessions.SessionRecord{
		Provider:  sessions.ProviderCodex,
		SessionID: "session-1",
	}, nil)

	// The eagerly set note is gone here: openEmbeddedTerminal reports its own
	// failure first. That is exactly why the note also has to travel on the
	// launch, which is the assertion that follows.
	if cmd == nil {
		t.Fatal("expected the external fallthrough to produce a launch command")
	}
	msg, ok := cmd().(AgentResultMsg)
	if !ok {
		t.Fatalf("launch message = %#v, want AgentResultMsg", msg)
	}
	if !strings.Contains(msg.LaunchedStatus, "tmux unavailable") {
		t.Fatalf("launched status = %q, want it to carry the fallback note", msg.LaunchedStatus)
	}
	applied, _ := next.handleAgentResultAfterFinalization(msg, nil)
	if !strings.Contains(applied.status.Text, "tmux unavailable") {
		t.Fatalf("status after the result = %q, want the note to survive", applied.status.Text)
	}
}

// TestLaunchErrorMessageKeepsTheTransportDiagnostic keeps a failed tmux spawn
// actionable. A Flow persists this string into the phase's needs_attention note,
// so "exit status 1" alone is the whole durable record of the failure.
func TestLaunchErrorMessageKeepsTheTransportDiagnostic(t *testing.T) {
	err := errors.New("exit status 1")
	bare := actions.TerminalLaunchSpec{}
	if got := launchErrorMessage(err, bare); got != "exit status 1" {
		t.Fatalf("without a diagnostic, message = %q, want the process error alone", got)
	}
	empty := actions.TerminalLaunchSpec{ErrorDetail: func() string { return "  " }}
	if got := launchErrorMessage(err, empty); got != "exit status 1" {
		t.Fatalf("with an empty diagnostic, message = %q, want the process error alone", got)
	}
	detailed := actions.TerminalLaunchSpec{
		ErrorDetail: func() string { return "no server running on /tmp/tmux-501/default\n" },
	}
	got := launchErrorMessage(err, detailed)
	if !strings.Contains(got, "exit status 1") || !strings.Contains(got, "no server running") {
		t.Fatalf("message = %q, want both the exit status and tmux's own message", got)
	}
}

// TestAttachReportsMissingTmuxWithoutProbing covers the T affordance when the
// backend is tmux but tmux is not installed: the session probe would be
// meaningless, so it must not run.
func TestAttachReportsMissingTmuxWithoutProbing(t *testing.T) {
	spy := &tmuxResumeSpy{t: t}
	m := spy.modelWithRepos("tmux", false)
	m.repoTmuxSessionExists = func(string) bool {
		t.Fatal("a missing tmux must not be probed for sessions")
		return true
	}

	nextModel, cmd := m.handleAttachRepoTmuxSession()
	if cmd != nil {
		t.Fatal("a refused attach must start no work")
	}
	if got := nextModel.(Model).status.Text; got != "tmux is not installed" {
		t.Fatalf("status = %q, want the missing-tmux message", got)
	}
	if m.tmuxModeAttachAvailable() {
		t.Fatal("the T affordance must not be advertised without tmux")
	}
}
