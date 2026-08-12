package model

import (
	"errors"
	"testing"
	"time"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

// These tests characterize the tracked Flow phase resume path. Every invariant
// asserted here holds both before and after the launch lifecycle takes resume
// over, so the migration can only move policy, never change it. They run
// against the seam-wired harness rather than a bare Model: once resume reads
// authoritatively, an unwired ReadFlow would fall back to the real store at the
// default state root.

func resumeLaunchFlowRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Branch:       "flow/one",
		Commit:       "abc123",
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Kind:      flowstore.KindImplementation,
			Status:    flowstore.PhaseCompleted,
			Order:     1,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{{
				Provider:  "codex",
				SessionID: "codex-session",
				LaunchID:  "launch-old",
				Status:    "ended",
			}},
		}},
	}
}

// resumeModel seeds the expansion and phase selection the Flows surface would
// hold when the user presses r on a phase row.
func (h *manualLaunchHarness) resumeModel() Model {
	h.t.Helper()
	m := h.model()
	m.expandedFlowID = h.record.FlowID
	m.selectedFlowPhaseID = h.record.Phases[0].PhaseID
	return m
}

// resume presses r and drains the resulting command chain, exactly as the
// bubbletea runtime would. It is deliberately message-shape agnostic so the
// same driver works before and after the lifecycle refactor.
func (h *manualLaunchHarness) resume(m Model) Model {
	h.t.Helper()
	next, cmd := m.handleResumeFlowPhaseSession()
	return h.drain(next.(Model), cmd, 0)
}

func TestTrackedFlowPhaseResumePersistsResumeAndOpensOneSlot(t *testing.T) {
	record := resumeLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)

	m := h.resume(h.resumeModel())

	if len(h.launchUpdates) != 1 {
		t.Fatalf("launch updates = %#v, want exactly one resume write", h.launchUpdates)
	}
	update := h.launchUpdates[0]
	if update.FlowID != record.FlowID || update.PhaseID != record.Phases[0].PhaseID {
		t.Fatalf("launch update = %#v, want the exact resumed target", update)
	}
	if !update.Resume {
		t.Fatalf("launch update = %#v, want Resume so a terminal phase keeps its status", update)
	}
	if update.AutoLaunch {
		t.Fatalf("launch update = %#v, want a manual resume", update)
	}
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if ctx.Command != agent.CommandCodex ||
		ctx.ResumeSessionID != "codex-session" ||
		ctx.LaunchID != update.LaunchID ||
		ctx.FlowID != record.FlowID ||
		ctx.FlowPhaseID != record.Phases[0].PhaseID ||
		ctx.RepoPath != record.RepoPath ||
		ctx.WorktreePath != record.WorktreePath ||
		ctx.WorkingDir != record.WorktreePath ||
		ctx.Branch != record.Branch ||
		ctx.Commit != record.Commit ||
		!ctx.Embedded ||
		!ctx.FlowLaunchTracked ||
		ctx.Headless ||
		ctx.InitialPrompt != "" ||
		ctx.Model != "" ||
		ctx.ReasoningEffort != "" {
		t.Fatalf("resume launch context = %#v", ctx)
	}
	if len(h.agentContexts) != 0 {
		t.Fatalf("a CLI resume must open an embedded terminal, not hand off: %#v", h.agentContexts)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("a successful resume must not write phase status: %#v", h.phaseUpdates)
	}
	if got := embeddedFlowSlots(m, record.FlowID); got != 1 {
		t.Fatalf("embedded Flow slots = %d, want exactly one", got)
	}
	if h.launchReservations != 1 || h.launchReleases != 1 {
		t.Fatalf("launch reservations = %d, releases = %d, want one held then released",
			h.launchReservations, h.launchReleases)
	}
}

func embeddedFlowSlots(m Model, flowID string) int {
	count := 0
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == embeddedTerminalScopeFlow && slot.FlowID == flowID {
			count++
		}
	}
	return count
}

func TestCodexAppFlowPhaseResumeClearsFlowIdentityAndSkipsPhaseWrite(t *testing.T) {
	record := resumeLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.agentCommand = agent.CommandCodexApp

	m := h.resume(h.resumeModel())

	if len(h.agentContexts) != 1 {
		t.Fatalf("codex-app resume launches = %#v, want exactly one navigation", h.agentContexts)
	}
	ctx := h.agentContexts[0]
	if ctx.Command != agent.CommandCodexApp || ctx.ResumeSessionID != "codex-session" {
		t.Fatalf("codex-app resume context = %#v, want the resumed session", ctx)
	}
	if ctx.LaunchID != "" || ctx.FlowID != "" || ctx.FlowPhaseID != "" ||
		ctx.FlowLaunchTracked || ctx.Embedded {
		t.Fatalf("codex-app resume context = %#v, want Flow identity cleared", ctx)
	}
	if len(h.launchUpdates) != 0 {
		t.Fatalf("codex-app resume wrote the phase: %#v", h.launchUpdates)
	}
	if len(h.launchContexts) != 0 {
		t.Fatalf("codex-app resume opened an embedded terminal: %#v", h.launchContexts)
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("codex-app resume must not hold the Flow")
	}
	if h.launchReservations != 1 || h.launchReleases != 1 {
		t.Fatalf("codex-app reservations = %d, releases = %d, want one held then released",
			h.launchReservations, h.launchReleases)
	}
}

// The write's record decides whether this resume preserved a terminal phase or
// reopened a running one. The pane snapshot the key press read may be stale, so
// failure handling has to follow the persisted status in both directions.
func TestFlowPhaseResumeTerminalProtectionFollowsPersistedRecord(t *testing.T) {
	tests := []struct {
		name            string
		snapshotStatus  string
		persistedStatus string
		wantUpdates     int
	}{
		{
			name:            "persisted terminal overrides nonterminal snapshot",
			snapshotStatus:  flowstore.PhaseNeedsAttention,
			persistedStatus: flowstore.PhaseCompleted,
			wantUpdates:     0,
		},
		{
			name:            "persisted running overrides terminal snapshot",
			snapshotStatus:  flowstore.PhaseCompleted,
			persistedStatus: flowstore.PhaseRunning,
			wantUpdates:     1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := resumeLaunchFlowRecord()
			record.Phases[0].Status = tc.snapshotStatus
			h := newManualLaunchHarness(t, record)
			h.startTerminalErr = errors.New("terminal unavailable")
			h.persistedRecordOK = true
			h.persistedRecord = flowstore.FlowRecord{
				FlowID: record.FlowID,
				Phases: []flowstore.FlowPhase{{
					PhaseID: record.Phases[0].PhaseID,
					Kind:    record.Phases[0].Kind,
					Status:  tc.persistedStatus,
				}},
			}

			m := h.resume(h.resumeModel())

			if len(h.launchUpdates) != 1 {
				t.Fatalf("launch updates = %#v, want the resume write to happen first", h.launchUpdates)
			}
			if len(h.phaseUpdates) != tc.wantUpdates {
				t.Fatalf("phase updates = %#v, want %d", h.phaseUpdates, tc.wantUpdates)
			}
			if tc.wantUpdates == 1 {
				update := h.phaseUpdates[0]
				if update.FlowID != record.FlowID ||
					update.PhaseID != record.Phases[0].PhaseID ||
					update.Status != flowstore.PhaseNeedsAttention {
					t.Fatalf("phase update = %#v, want needs_attention on the resumed phase", update)
				}
			}
			if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
				t.Fatal("a failed resume must not strand its hold on the Flow")
			}
			if h.launchReleases != h.launchReservations {
				t.Fatalf("reservations = %d, releases = %d, want every hold released",
					h.launchReservations, h.launchReleases)
			}
		})
	}
}

// A Plan Review phase blocks rather than needing attention, and the choice is
// made from the kind the write's record carries.
func TestFlowPhaseResumeFailureBlocksPlanReview(t *testing.T) {
	record := resumeLaunchFlowRecord()
	record.Phases[0].PhaseID = "plan-review"
	record.Phases[0].Kind = flowstore.KindPlanReview
	record.Phases[0].Status = flowstore.PhaseNeedsAttention
	h := newManualLaunchHarness(t, record)
	h.startTerminalErr = errors.New("terminal unavailable")
	h.persistedRecordOK = true
	h.persistedRecord = flowstore.FlowRecord{
		FlowID: record.FlowID,
		Phases: []flowstore.FlowPhase{{
			PhaseID: "plan-review",
			Kind:    flowstore.KindPlanReview,
			Status:  flowstore.PhaseRunning,
		}},
	}

	h.resume(h.resumeModel())

	if len(h.phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one Plan Review failure", h.phaseUpdates)
	}
	update := h.phaseUpdates[0]
	if update.Status != flowstore.PhaseBlocked || update.Outcome != flowstore.OutcomeBlocked {
		t.Fatalf("phase update = %#v, want a blocked Plan Review", update)
	}
}

// The reservation is taken before the write, so a reservation failure has to
// leave the phase exactly as it was.
func TestFlowPhaseResumeReservationFailureWritesNothing(t *testing.T) {
	record := resumeLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.reserveLaunchErr = errors.New("state root locked")

	m := h.resume(h.resumeModel())

	if len(h.launchUpdates) != 0 {
		t.Fatalf("a failed reservation wrote the phase: %#v", h.launchUpdates)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("a failed reservation regressed the phase: %#v", h.phaseUpdates)
	}
	if len(h.launchContexts) != 0 {
		t.Fatalf("a failed reservation opened a terminal: %#v", h.launchContexts)
	}
	if got := m.status.Text; got != "failed to mark flow phase resume: state root locked" {
		t.Fatalf("status = %q, want the reservation failure to read like a write failure", got)
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("a failed reservation must not strand its hold on the Flow")
	}
}
