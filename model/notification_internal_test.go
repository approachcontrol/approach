package model

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
)

func TestNotificationCmdUsesOSC9ThroughRenderer(t *testing.T) {
	event := flowPhaseNotification("Plan", "OSC 9 desktop notifications", "completed")

	if cmd := (Model{}).notificationCmd(event); cmd != nil {
		t.Fatal("disabled notifications returned a command")
	}

	cmd := (Model{notificationsEnabled: true}).notificationCmd(event)
	if cmd == nil {
		t.Fatal("enabled notifications returned nil")
	}
	msg, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("notification command returned %T, want tea.RawMsg", cmd())
	}
	const want = "\x1b]9;Approach: Plan completed for OSC 9 desktop notifications\x07"
	if msg.Msg != want {
		t.Fatalf("raw notification = %#v, want %#v", msg.Msg, want)
	}
}

func TestNotificationCmdUsesTmuxPassthrough(t *testing.T) {
	event := flowPhaseNotification("Plan", "OSC 9 desktop notifications", "completed")
	m := Model{notificationsEnabled: true, insideTmux: func() bool { return true }}

	msg, ok := m.notificationCmd(event)().(tea.RawMsg)
	if !ok {
		t.Fatalf("notification command returned unexpected message")
	}
	const inner = "\x1b]9;Approach: Plan completed for OSC 9 desktop notifications\x07"
	want := "\x1bPtmux;" + strings.ReplaceAll(inner, "\x1b", "\x1b\x1b") + "\x1b\\"
	if msg.Msg != want {
		t.Fatalf("tmux notification = %#v, want %#v", msg.Msg, want)
	}
}

type notificationTestTerminal struct {
	state string
	err   error
}

func (t notificationTestTerminal) VisibleLines(int, int) []string { return nil }
func (t notificationTestTerminal) Write(p []byte) (int, error)    { return len(p), nil }
func (t notificationTestTerminal) Resize(int, int) error          { return nil }
func (t notificationTestTerminal) Terminate() error               { return nil }
func (t notificationTestTerminal) Wait(context.Context) error     { return t.err }
func (t notificationTestTerminal) State() string                  { return t.state }

func TestEmbeddedExitNotificationsReportEachProcessOnce(t *testing.T) {
	failed := exec.Command("sh", "-c", "exit 3").Run()
	m := Model{notificationsEnabled: true}
	m.embeddedTerminals = []embeddedTerminalSlot{
		{Provider: "codex", RepoPath: "/dev/approach", Terminal: notificationTestTerminal{state: "exited"}},
		{Provider: "claude", RepoPath: "/dev/client", Terminal: notificationTestTerminal{state: "failed", err: failed}},
		{Provider: "cursor-agent", RepoPath: "/dev/live", Terminal: notificationTestTerminal{state: "running"}},
	}

	next, cmds := m.collectEmbeddedExitNotifications()
	if len(cmds) != 2 {
		t.Fatalf("notification commands = %d, want 2", len(cmds))
	}
	wants := []string{
		"\x1b]9;Approach: codex session finished in approach\x07",
		"\x1b]9;Approach: claude session failed (exit 3) in client\x07",
	}
	for i, cmd := range cmds {
		msg := cmd().(tea.RawMsg)
		if msg.Msg != wants[i] {
			t.Fatalf("notification %d = %#v, want %#v", i, msg.Msg, wants[i])
		}
	}
	if _, again := next.collectEmbeddedExitNotifications(); len(again) != 0 {
		t.Fatalf("second pass emitted %d notifications", len(again))
	}
}

func TestEmbeddedExitNotificationFollowsCurrentTickGenerationBeforeAutoDismiss(t *testing.T) {
	m := NewWithOptions(nil, Options{NotificationsEnabled: true})
	m.embeddedTerminalTickGen = 4
	m.embeddedTerminals = []embeddedTerminalSlot{{
		Scope: embeddedTerminalScopeFlow, Provider: "codex", RepoPath: "/dev/approach",
		FlowID: "flow-1", FlowPhaseID: "plan", LaunchID: "launch-1",
		Terminal: notificationTestTerminal{state: "exited"},
	}}

	stale, staleCmd := m.Update(embeddedTerminalTickMsg{Generation: 3})
	if staleCmd != nil || stale.(Model).embeddedTerminals[0].NotificationReported {
		t.Fatal("stale tick changed notification state")
	}

	next, cmd := m.Update(embeddedTerminalTickMsg{Generation: 4})
	if len(next.(Model).embeddedTerminals) != 0 {
		t.Fatal("exited Flow terminal was not auto-dismissed")
	}
	if got := rawNotificationStrings(cmd); len(got) != 1 || !strings.Contains(got[0], "codex session finished") {
		t.Fatalf("tick raw notifications = %#v, want one agent exit", got)
	}
}

func rawNotificationStrings(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var values []string
		for _, nested := range batch {
			values = append(values, rawNotificationStrings(nested)...)
			if len(values) > 0 {
				return values
			}
		}
		return values
	}
	if raw, ok := msg.(tea.RawMsg); ok {
		if value, ok := raw.Msg.(string); ok {
			return []string{value}
		}
	}
	return nil
}

func TestSuccessfulTmuxLaunchRegistersNotificationWatch(t *testing.T) {
	ctx := actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/approach"}
	m := NewWithOptions(nil, Options{NotificationsEnabled: true})

	next, _ := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: ctx, Detached: true, Tmux: true}, nil)
	if watch, ok := next.tmuxNotificationWatches[ctx.LaunchID]; !ok || watch.RepoPath != ctx.RepoPath {
		t.Fatalf("tmux notification watch = %#v, want launch-1 for /dev/approach", next.tmuxNotificationWatches)
	}

	failed, _ := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: ctx, Detached: true, Tmux: true, Err: "spawn failed"}, nil)
	if len(failed.tmuxNotificationWatches) != 0 {
		t.Fatalf("failed launch registered watches: %#v", failed.tmuxNotificationWatches)
	}
}

func TestInteractiveAgentExitEmitsNotification(t *testing.T) {
	ctx := actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/approach"}
	m := NewWithOptions(nil, Options{NotificationsEnabled: true})

	_, cmd := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: ctx}, nil)
	got := rawNotificationStrings(cmd)
	if len(got) != 1 || !strings.Contains(got[0], "codex session finished in approach") {
		t.Fatalf("interactive exit notifications = %#v, want one successful codex notification", got)
	}
}

func TestInteractiveAgentFailureEmitsNotification(t *testing.T) {
	ctx := actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1", RepoPath: "/dev/approach"}
	m := NewWithOptions(nil, Options{NotificationsEnabled: true})

	_, cmd := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: ctx, Err: "exit status 7"}, nil)
	got := rawNotificationStrings(cmd)
	if len(got) != 1 || !strings.Contains(got[0], "codex session failed in approach") {
		t.Fatalf("interactive failure notifications = %#v, want one failed codex notification", got)
	}
}

func TestTmuxLaunchSweepNotifiesOnWindowDisappearance(t *testing.T) {
	var probes int
	m := NewWithOptions(nil, Options{
		NotificationsEnabled: true,
		SweepLaunches:        func() {},
		RepoTmuxLaunchStatus: func(repoPath string, launchIDs ...string) (bool, error) {
			probes++
			return launchIDs[0] == "live", nil
		},
	})
	m.tmuxNotificationWatches = map[string]tmuxNotificationWatch{
		"gone": {LaunchID: "gone", RepoPath: "/dev/approach", Provider: "codex"},
		"live": {LaunchID: "live", RepoPath: "/dev/client", Provider: "claude"},
	}

	next, sweepCmd := m.startLaunchSweep()
	done := sweepCmd().(launchSweepDoneMsg)
	next, notifyCmd := next.handleLaunchSweepDone(done)
	if probes != 2 {
		t.Fatalf("tmux probes = %d, want 2", probes)
	}
	if _, ok := next.tmuxNotificationWatches["gone"]; ok {
		t.Fatal("disappeared tmux watch was retained")
	}
	if _, ok := next.tmuxNotificationWatches["live"]; !ok {
		t.Fatal("live tmux watch was discarded")
	}
	if got := rawNotificationStrings(notifyCmd); len(got) != 1 || !strings.Contains(got[0], "codex session finished in approach") {
		t.Fatalf("sweep raw notifications = %#v", got)
	}
}

func TestTmuxLaunchSweepRetainsWatchWhenProbeFails(t *testing.T) {
	m := NewWithOptions(nil, Options{
		NotificationsEnabled: true,
		SweepLaunches:        func() {},
		RepoTmuxLaunchStatus: func(string, ...string) (bool, error) {
			return false, errors.New("tmux probe timed out")
		},
	})
	m.tmuxNotificationWatches = map[string]tmuxNotificationWatch{
		"launch-1": {LaunchID: "launch-1", RepoPath: "/dev/approach", Provider: "codex"},
	}

	next, sweepCmd := m.startLaunchSweep()
	done := sweepCmd().(launchSweepDoneMsg)
	next, notifyCmd := next.handleLaunchSweepDone(done)
	if _, ok := next.tmuxNotificationWatches["launch-1"]; !ok {
		t.Fatal("inconclusive tmux probe discarded the notification watch")
	}
	if got := rawNotificationStrings(notifyCmd); len(got) != 0 {
		t.Fatalf("inconclusive tmux probe emitted notifications: %#v", got)
	}
}

func TestDisabledNotificationsDoNotProbeTmuxWindows(t *testing.T) {
	var probes int
	m := NewWithOptions(nil, Options{
		SweepLaunches: func() {},
		RepoTmuxLaunchStatus: func(string, ...string) (bool, error) {
			probes++
			return false, nil
		},
	})
	m.tmuxNotificationWatches = map[string]tmuxNotificationWatch{
		"launch-1": {LaunchID: "launch-1", RepoPath: "/dev/approach", Provider: "codex"},
	}

	_, sweepCmd := m.startLaunchSweep()
	_ = sweepCmd()
	if probes != 0 {
		t.Fatalf("disabled notifications made %d tmux probes", probes)
	}
}

func TestFlowPhaseNotificationEventsReportSelectedOutcomeEdges(t *testing.T) {
	previous := []flowstore.FlowRecord{{
		FlowID: "flow-1", Title: "OSC 9 desktop notifications",
		Phases: []flowstore.FlowPhase{
			{PhaseID: " PLAN-REVIEW ", Title: "Plan review", Status: flowstore.PhaseRunning},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseRunning},
		},
	}}
	current := []flowstore.FlowRecord{{
		FlowID: "flow-1", Title: "OSC 9 desktop notifications",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseNeedsAttention},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseBlocked},
			{PhaseID: "plan-review", Title: "Plan review", Status: flowstore.PhaseCompleted},
		},
	}}

	events := flowPhaseNotificationEvents(previous, current)
	if len(events) != 3 {
		t.Fatalf("notification events = %#v, want three", events)
	}
	wants := []string{
		"Approach: Autoreview needs attention for OSC 9 desktop notifications",
		"Approach: Implementation blocked for OSC 9 desktop notifications",
		"Approach: Plan review completed for OSC 9 desktop notifications",
	}
	for i, event := range events {
		if got := notificationMessage(event); got != wants[i] {
			t.Fatalf("event %d message = %q, want %q", i, got, wants[i])
		}
	}
}

func TestFlowPhaseNotificationEventsPrimeAndReenterWithoutRepeats(t *testing.T) {
	terminal := notificationFlow("flow-1", flowstore.PhaseCompleted)
	running := notificationFlow("flow-1", flowstore.PhaseRunning)

	if got := flowPhaseNotificationEvents(nil, []flowstore.FlowRecord{terminal}); len(got) != 0 {
		t.Fatalf("startup emitted %d events", len(got))
	}
	if got := flowPhaseNotificationEvents([]flowstore.FlowRecord{terminal}, []flowstore.FlowRecord{terminal}); len(got) != 0 {
		t.Fatalf("unchanged phase emitted %d events", len(got))
	}
	if got := flowPhaseNotificationEvents([]flowstore.FlowRecord{terminal}, []flowstore.FlowRecord{running}); len(got) != 0 {
		t.Fatalf("restart emitted %d events", len(got))
	}
	if got := flowPhaseNotificationEvents([]flowstore.FlowRecord{running}, []flowstore.FlowRecord{terminal}); len(got) != 1 {
		t.Fatalf("terminal re-entry emitted %d events, want 1", len(got))
	}
	newFlow := notificationFlow("flow-2", flowstore.PhaseBlocked)
	if got := flowPhaseNotificationEvents([]flowstore.FlowRecord{terminal}, []flowstore.FlowRecord{terminal, newFlow}); len(got) != 0 {
		t.Fatalf("newly discovered Flow emitted %d events", len(got))
	}
}

func TestAutoAdvancePollEmitsFlowPhaseNotificationAndPreservesPartialBaseline(t *testing.T) {
	previous := notificationFlow("flow-1", flowstore.PhaseRunning)
	current := notificationFlow("flow-1", flowstore.PhaseNeedsAttention)
	m := NewWithOptions(nil, Options{NotificationsEnabled: true})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.autoAdvanceInFlight = 1

	next, cmd := m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	if got := rawNotificationStrings(cmd); len(got) != 1 || !strings.Contains(got[0], "Implementation needs attention") {
		t.Fatalf("auto-advance notifications = %#v", got)
	}

	next.autoAdvanceInFlight = 2
	before := cloneFlowRecords(next.autoAdvanceSnapshot)
	partial := &flowstore.PartialListError{}
	after, _ := next.handleAutoAdvanceResult(AutoAdvanceResultMsg{
		Flows:       []flowstore.FlowRecord{notificationFlow("flow-1", flowstore.PhaseBlocked)},
		Degradation: partial, Request: 2,
	})
	if got := after.autoAdvanceSnapshot[0].Phases[0].Status; got != before[0].Phases[0].Status {
		t.Fatalf("partial poll baseline status = %q, want %q", got, before[0].Phases[0].Status)
	}
}

func notificationFlow(id string, status flowstore.PhaseStatus) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID: id,
		Title:  "OSC 9 desktop notifications",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Status:  status,
		}},
	}
}

func TestNotificationCmdSuppressesEmptyMessage(t *testing.T) {
	if cmd := (Model{notificationsEnabled: true}).notificationCmd(notificationEvent{}); cmd != nil {
		t.Fatal("empty notification returned a command")
	}
}

func TestNotificationTextStripsControlsAndNormalizesWhitespace(t *testing.T) {
	input := "  codex\a\x1b]9;injected\u009b\tfinished\r\nin   approach  "
	const want = "codex ]9;injected finished in approach"
	if got := sanitizeNotificationText(input); got != want {
		t.Fatalf("sanitized text = %q, want %q", got, want)
	}
}

func TestNotificationTextHasDeterministicRuneLimit(t *testing.T) {
	input := strings.Repeat("é", notificationMessageMaxRunes+20)
	want := strings.Repeat("é", notificationMessageMaxRunes)
	if got := sanitizeNotificationText(input); got != want {
		t.Fatalf("sanitized text has %d runes, want %d", len([]rune(got)), notificationMessageMaxRunes)
	}
}
