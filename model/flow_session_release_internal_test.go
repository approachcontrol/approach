package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// The stall these tests are about: an agent died without its provider hook ever
// writing an end-of-session event, so the phase keeps a "last_seen" record
// forever. The phase itself is ready — AddPhaseLaunchID set it running, then a
// blocked-downstream reset put it back — which is why nothing but release gets
// it moving again.
const stalledReleaseLaunchID = "launch-crashed-1a2b3c4d"

var (
	errListSessionsFailed = errors.New("list sessions: state root locked")
	errFinalizeFailed     = errors.New("finalize session: state root locked")
)

func stalledReleaseFlowRecord() flowstore.FlowRecord {
	record := manualLaunchFlowRecord()
	record.Phases[0].LaunchIDs = []string{stalledReleaseLaunchID}
	record.Phases[0].Sessions = []flowstore.Session{
		{Provider: "codex", SessionID: "s-1", LaunchID: stalledReleaseLaunchID, Status: "last_seen"},
	}
	return record
}

func stalledReleaseSessionRecords() []sessions.SessionRecord {
	return []sessions.SessionRecord{
		{SessionID: "s-1", LaunchID: stalledReleaseLaunchID, FlowID: "flow-1", FlowPhaseID: "implementation", Status: "last_seen"},
	}
}

// releaseHarness wires the persisted half to the pane half, because release
// reads the Flow through the launch seams while the key press reads the pane.
func releaseHarness(t *testing.T, record flowstore.FlowRecord, stored []sessions.SessionRecord) *manualLaunchHarness {
	t.Helper()
	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{record}
	h.sessionRecords = stored
	return h
}

// selectPhase puts the pane where the x handler expects it: the Flow expanded,
// one of its phases selected.
func (h *manualLaunchHarness) selectPhase(m Model, phaseID string) Model {
	h.t.Helper()
	m.expandedFlowID = h.record.FlowID
	m.selectedFlowPhaseID = phaseID
	return m
}

// pressX runs the key handler and drains everything it produces, stopping at
// the confirm modal because a modal is state, not a command.
func (h *manualLaunchHarness) pressX(m Model) Model {
	h.t.Helper()
	next, cmd := m.handleResetSelectedFlowPhase()
	m = next.(Model)
	return h.drain(m, cmd, 0)
}

// confirm answers the open modal the way the runtime does.
func (h *manualLaunchHarness) confirm(m Model) Model {
	h.t.Helper()
	if !m.modal.IsOpen() {
		h.t.Fatal("confirm called without an open modal")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(Model)
	return h.drain(m, cmd, 0)
}

func TestFlowPhaseSessionReleasable(t *testing.T) {
	runningEmbedded := func(m Model) Model {
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Number:      1,
			Scope:       embeddedTerminalScopeFlow,
			FlowID:      "flow-1",
			FlowPhaseID: "implementation",
			LaunchID:    stalledReleaseLaunchID,
			Terminal:    flowPhaseLaunchTestTerminal{state: "running"},
		})
		return m
	}

	tests := []struct {
		name   string
		mutate func(flowstore.FlowRecord) flowstore.FlowRecord
		model  func(Model) Model
		want   bool
	}{
		{name: "stalled ready phase", want: true},
		{
			// Advertising is narrower than the action: on a running phase a live
			// session is the normal state, and outside the embedded backend
			// nothing cheap tells a working agent from a stalled one.
			name: "running phase",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Status = flowstore.PhaseRunning
				return record
			},
			want: false,
		},
		{
			name: "reset-eligible phase",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Status = flowstore.PhaseRunning
				record.Phases[0].Sessions[0].Status = "ended"
				return record
			},
			want: false,
		},
		{
			name: "closed Flow",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				closedAt := time.Now()
				record.Closed = flowstore.Closure{ClosedAt: &closedAt, Reason: "abandoned"}
				return record
			},
			want: false,
		},
		{
			name: "phase with no launch IDs",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].LaunchIDs = nil
				return record
			},
			want: false,
		},
		{
			name: "ended session",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Sessions[0].Status = "ended"
				return record
			},
			want: false,
		},
		{name: "running embedded terminal for the phase", model: runningEmbedded, want: false},
		{
			name: "launch lifecycle holds the Flow",
			model: func(m Model) Model {
				next, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:  "token-1",
					Kind:   flowLaunchKindManualPhase,
					FlowID: "flow-1",
				}, flowLaunchStateReading)
				if !ok {
					t.Fatal("could not reserve a launch attempt")
				}
				return next
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := stalledReleaseFlowRecord()
			if tc.mutate != nil {
				record = tc.mutate(record)
			}
			h := releaseHarness(t, record, stalledReleaseSessionRecords())
			m := h.selectPhase(h.model(), "implementation")
			if tc.model != nil {
				m = tc.model(m)
			}

			if got := m.flowPhaseSessionReleasable(record, record.Phases[0]); got != tc.want {
				t.Fatalf("flowPhaseSessionReleasable = %v, want %v", got, tc.want)
			}
			if got := m.selectedFlowPhaseSessionReleasable(); got != tc.want {
				t.Fatalf("selectedFlowPhaseSessionReleasable = %v, want %v", got, tc.want)
			}
		})
	}
}

// The bead's own scenario, end to end and for both halves of the stall: the
// mirrored variant, the store-only variant the phase mirror never saw, and both
// at once.
func TestFlowPhaseSessionReleaseRecoversStalledPhase(t *testing.T) {
	tests := []struct {
		name     string
		mirrored []flowstore.Session
		stored   []sessions.SessionRecord
	}{
		{
			name:     "variant A mirrored and stored",
			mirrored: stalledReleaseFlowRecord().Phases[0].Sessions,
			stored:   stalledReleaseSessionRecords(),
		},
		{
			name:   "variant B store only",
			stored: stalledReleaseSessionRecords(),
		},
		{
			name:     "variant A mirror only",
			mirrored: stalledReleaseFlowRecord().Phases[0].Sessions,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := stalledReleaseFlowRecord()
			record.Phases[0].Sessions = tc.mirrored
			h := releaseHarness(t, record, tc.stored)
			m := h.selectPhase(h.model(), "implementation")

			// The refusal that sends a user here in the first place.
			blocked := h.launch(m)
			if blocked.status.Text != flowLaunchPhaseSessionLiveStatus {
				t.Fatalf("stalled launch status = %q, want %q", blocked.status.Text, flowLaunchPhaseSessionLiveStatus)
			}
			if len(h.launchUpdates) != 0 {
				t.Fatalf("stalled phase launched anyway: %#v", h.launchUpdates)
			}

			m = h.pressX(m)
			if m.Overlay() != ui.OverlayConfirm {
				t.Fatalf("x on a stalled phase overlay = %d, want a confirmation (status %q)", m.Overlay(), m.status.Text)
			}
			if len(h.finalizeContexts) != 0 {
				t.Fatalf("release finalized before confirmation: %#v", h.finalizeContexts)
			}

			m = h.confirm(m)
			if len(h.finalizeContexts) != 1 {
				t.Fatalf("finalize contexts = %#v, want exactly one per stale launch", h.finalizeContexts)
			}
			ctx := h.finalizeContexts[0]
			if ctx.LaunchID != stalledReleaseLaunchID || ctx.FlowID != "flow-1" || ctx.FlowPhaseID != "implementation" {
				t.Fatalf("finalize context = %#v, want the stalled launch", ctx)
			}
			if !strings.Contains(m.status.Text, "Released 1") {
				t.Fatalf("status after release = %q, want the released count", m.status.Text)
			}

			// What finalization would have written on a clean exit, so the same
			// read stage that refused now admits the launch.
			released := stalledReleaseFlowRecord()
			released.Phases[0].Sessions = nil
			h.record = released
			h.persistedFlows = []flowstore.FlowRecord{released}
			h.sessionRecords = nil
			h.launchUpdates = nil

			relaunched := h.launch(h.selectPhase(h.model(), "implementation"))
			if len(h.launchUpdates) != 1 {
				t.Fatalf("released phase still refuses to launch: status %q updates %#v", relaunched.status.Text, h.launchUpdates)
			}
		})
	}
}

// A press that was never a release candidate stays silent, exactly as it does
// today; only a probe that ran and found nothing gets a refusal.
func TestFlowPhaseSessionReleaseProbeGate(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(flowstore.FlowRecord) flowstore.FlowRecord
		wantProbe  bool
		wantStatus string
	}{
		{name: "stalled phase probes", wantProbe: true},
		{
			name: "no launch IDs stays silent",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].LaunchIDs = nil
				record.Phases[0].Sessions = nil
				return record
			},
		},
		{
			// Variant B: the mirror is clean, so the cheap predicate says no —
			// and the probe must run anyway or the store-only stall is
			// unreachable.
			name: "clean mirror still probes",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Sessions = nil
				return record
			},
			wantProbe: true,
		},
		{
			name: "closed Flow stays silent",
			mutate: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				closedAt := time.Now()
				record.Closed = flowstore.Closure{ClosedAt: &closedAt, Reason: "abandoned"}
				return record
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := stalledReleaseFlowRecord()
			if tc.mutate != nil {
				record = tc.mutate(record)
			}
			h := releaseHarness(t, record, stalledReleaseSessionRecords())
			m := h.selectPhase(h.model(), "implementation")

			next, cmd := m.handleResetSelectedFlowPhase()
			m = next.(Model)
			if (cmd != nil) != tc.wantProbe {
				t.Fatalf("probe dispatched = %v, want %v", cmd != nil, tc.wantProbe)
			}
			if cmd == nil {
				if m.status.Text != "" {
					t.Fatalf("gated press set status %q, want silence", m.status.Text)
				}
				if h.sessionListCalls != 0 {
					t.Fatalf("gated press walked the session store %d times", h.sessionListCalls)
				}
			}
		})
	}
}

func TestFlowPhaseSessionReleaseRefusesWhenNothingIsLive(t *testing.T) {
	record := stalledReleaseFlowRecord()
	record.Phases[0].LaunchIDs = []string{"launch-orphan"}
	record.Phases[0].Sessions = nil
	h := releaseHarness(t, record, nil)
	m := h.selectPhase(h.model(), "implementation")

	m = h.pressX(m)

	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("overlay = %d, want no confirmation", m.Overlay())
	}
	if m.status.Text != flowPhaseSessionReleaseNoneStatus {
		t.Fatalf("status = %q, want %q", m.status.Text, flowPhaseSessionReleaseNoneStatus)
	}
}

func TestFlowPhaseSessionReleaseReportsProbeFailure(t *testing.T) {
	h := releaseHarness(t, stalledReleaseFlowRecord(), stalledReleaseSessionRecords())
	h.sessionsErr = errListSessionsFailed
	m := h.selectPhase(h.model(), "implementation")

	m = h.pressX(m)

	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("overlay = %d, want no confirmation after a failed probe", m.Overlay())
	}
	if !strings.Contains(m.status.Text, errListSessionsFailed.Error()) {
		t.Fatalf("status = %q, want the probe error", m.status.Text)
	}
}

// AC3: while an agent is provably live on the phase, release refuses entirely
// and finalizes nothing — including the crashed launch beside it. A resume
// persists a second launch ID, so this is the shape a user reaches by pressing
// r first, which is what the bead's own workaround does.
func TestFlowPhaseSessionReleaseRefusesWhileAnAgentIsLive(t *testing.T) {
	const resumedLaunchID = "launch-resumed-9f8e7d6c"

	resumedRecord := func() flowstore.FlowRecord {
		record := stalledReleaseFlowRecord()
		record.Phases[0].LaunchIDs = append(record.Phases[0].LaunchIDs, resumedLaunchID)
		record.Phases[0].Sessions = append(record.Phases[0].Sessions, flowstore.Session{
			Provider: "codex", SessionID: "s-2", LaunchID: resumedLaunchID, Status: "running",
		})
		return record
	}

	t.Run("running embedded slot", func(t *testing.T) {
		h := releaseHarness(t, resumedRecord(), stalledReleaseSessionRecords())
		m := h.selectPhase(h.model(), "implementation")
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Number:      1,
			Scope:       embeddedTerminalScopeFlow,
			FlowID:      "flow-1",
			FlowPhaseID: "implementation",
			LaunchID:    resumedLaunchID,
			Terminal:    flowPhaseLaunchTestTerminal{state: "running"},
		})

		m = h.pressX(m)

		if m.Overlay() != ui.OverlayNone {
			t.Fatalf("overlay = %d, want no confirmation while a slot is running", m.Overlay())
		}
		if len(h.finalizeContexts) != 0 {
			t.Fatalf("finalize contexts = %#v, want none", h.finalizeContexts)
		}
		// The confirmation is re-checked too, so a slot that starts between the
		// probe and the answer still refuses — with the action to take.
		next, cmd := m.handleFlowPhaseSessionReleaseConfirmed(flowPhaseSessionReleaseConfirmedMsg{
			RepoPath:  "/dev/alpha",
			FlowID:    "flow-1",
			PhaseID:   "implementation",
			LaunchIDs: []string{stalledReleaseLaunchID},
		})
		next = h.drain(next, cmd, 0)
		if len(h.finalizeContexts) != 0 {
			t.Fatalf("confirmed release finalized past a running embedded slot: %#v", h.finalizeContexts)
		}
		if next.status.Text != flowPhaseSessionReleaseTerminalStatus {
			t.Fatalf("status = %q, want %q", next.status.Text, flowPhaseSessionReleaseTerminalStatus)
		}
	})

	t.Run("live tmux window", func(t *testing.T) {
		h := releaseHarness(t, resumedRecord(), stalledReleaseSessionRecords())
		h.launchBackend = config.LaunchBackendTmux
		h.tmuxAvailable = true
		h.tmuxWindowLive = true
		m := h.selectPhase(h.model(), "implementation")

		m = h.pressX(m)
		if m.Overlay() != ui.OverlayConfirm {
			t.Fatalf("overlay = %d, want the confirmation before the tmux probe runs", m.Overlay())
		}
		m = h.confirm(m)

		if len(h.finalizeContexts) != 0 {
			t.Fatalf("release finalized past a live tmux window: %#v", h.finalizeContexts)
		}
		if m.status.Text != flowPhaseSessionReleaseTmuxStatus {
			t.Fatalf("status = %q, want %q", m.status.Text, flowPhaseSessionReleaseTmuxStatus)
		}
		// One list-windows answers for every launch the phase made.
		if len(h.tmuxWindowProbes) != 1 || len(h.tmuxWindowProbes[0]) != 2 {
			t.Fatalf("tmux probes = %#v, want one probe carrying both launches", h.tmuxWindowProbes)
		}
	})

	t.Run("no probe can see it", func(t *testing.T) {
		h := releaseHarness(t, resumedRecord(), stalledReleaseSessionRecords())
		m := h.selectPhase(h.model(), "implementation")

		m = h.pressX(m)

		// The residual: external-terminal launches are invisible to both
		// probes, so the confirmation is the guard, and it has to say that the
		// sessions are unverified rather than assert they are dead.
		if m.Overlay() != ui.OverlayConfirm {
			t.Fatalf("overlay = %d, want a confirmation", m.Overlay())
		}
		prompt := m.ConfirmPrompt()
		if !strings.Contains(prompt, "2 unverified sessions") {
			t.Fatalf("prompt = %q, want the count and that the sessions are unverified", prompt)
		}
		if !strings.Contains(prompt, "1a2b3c4d") {
			t.Fatalf("prompt = %q, want a launch identified", prompt)
		}
	})
}

// renderConfirmDialog centres one unwrapped line, so an over-wide prompt is
// wrapped by the terminal and pushes the status bar out of frame. Nothing in
// the renderer enforces that; this does.
func TestFlowPhaseSessionReleasePromptFitsOneLine(t *testing.T) {
	tests := []struct {
		name         string
		count        int
		launchID     string
		autoRelaunch bool
	}{
		{name: "single", count: 1, launchID: stalledReleaseLaunchID},
		{name: "many", count: 128, launchID: stalledReleaseLaunchID},
		{name: "auto relaunch", count: 128, launchID: stalledReleaseLaunchID, autoRelaunch: true},
		{name: "long launch id", count: 12, launchID: strings.Repeat("f", 64), autoRelaunch: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := flowPhaseSessionReleasePrompt(tc.count, tc.launchID, tc.autoRelaunch)
			if n := len([]rune(prompt)); n > flowPhaseSessionReleasePromptBudget {
				t.Fatalf("prompt %q is %d runes, want at most %d", prompt, n, flowPhaseSessionReleasePromptBudget)
			}
			if !strings.Contains(prompt, "unverified") {
				t.Fatalf("prompt = %q, want it to say the sessions are unverified", prompt)
			}
			if tc.autoRelaunch != strings.Contains(prompt, "AutoMode") {
				t.Fatalf("prompt = %q, want AutoMode clause = %v", prompt, tc.autoRelaunch)
			}
		})
	}
}

// The confirm hop carries the launch IDs the probe selected, and the release
// intersects rather than recomputing, so a launch started after the modal
// opened is never finalized.
func TestFlowPhaseSessionReleaseIntersectsTheConfirmedSet(t *testing.T) {
	h := releaseHarness(t, stalledReleaseFlowRecord(), stalledReleaseSessionRecords())
	m := h.selectPhase(h.model(), "implementation")

	newcomer := stalledReleaseFlowRecord()
	newcomer.Phases[0].LaunchIDs = append(newcomer.Phases[0].LaunchIDs, "launch-fresh")
	newcomer.Phases[0].Sessions = append(newcomer.Phases[0].Sessions, flowstore.Session{
		Provider: "codex", SessionID: "s-3", LaunchID: "launch-fresh", Status: "running",
	})

	next, cmd := m.handleFlowPhaseSessionReleaseConfirmed(flowPhaseSessionReleaseConfirmedMsg{
		RepoPath:  "/dev/alpha",
		FlowID:    "flow-1",
		PhaseID:   "implementation",
		LaunchIDs: []string{stalledReleaseLaunchID},
	})
	h.persistedFlows = []flowstore.FlowRecord{newcomer}
	m = h.drain(next, cmd, 0)

	if len(h.finalizeContexts) != 1 || h.finalizeContexts[0].LaunchID != stalledReleaseLaunchID {
		t.Fatalf("finalize contexts = %#v, want only the confirmed launch", h.finalizeContexts)
	}
	if !strings.Contains(m.status.Text, "Released 1") {
		t.Fatalf("status = %q, want the released count", m.status.Text)
	}
}

func TestFlowPhaseSessionReleaseReportsFinalizeFailure(t *testing.T) {
	h := releaseHarness(t, stalledReleaseFlowRecord(), stalledReleaseSessionRecords())
	h.finalizeErr = errFinalizeFailed
	m := h.selectPhase(h.model(), "implementation")

	m = h.confirm(h.pressX(m))

	if !strings.Contains(m.status.Text, errFinalizeFailed.Error()) {
		t.Fatalf("status = %q, want the finalize error", m.status.Text)
	}
}

func TestLiveLaunchIDsForPhase(t *testing.T) {
	phase := flowstore.FlowPhase{
		LaunchIDs: []string{" launch-1 ", "launch-2", "launch-3", ""},
		Sessions: []flowstore.Session{
			{SessionID: "s-1", LaunchID: "launch-1", Status: "last_seen"},
			{SessionID: "", LaunchID: "launch-2", Status: "last_seen"},
			{SessionID: "s-3", LaunchID: "launch-3", Status: "ended"},
		},
	}
	records := []sessions.SessionRecord{
		{SessionID: "s-4", LaunchID: " launch-2 ", Status: "running"},
		{SessionID: "s-5", LaunchID: "launch-stray", Status: "running"},
		{SessionID: "s-1", LaunchID: "launch-1", Status: "last_seen"},
	}

	got := liveLaunchIDsForPhase(phase, records)

	want := []string{"launch-1", "launch-2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("liveLaunchIDsForPhase = %#v, want %#v", got, want)
	}
}
