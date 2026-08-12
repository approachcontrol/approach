package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
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

// --- Lifecycle-routed resume ---

func (h *manualLaunchHarness) resumeModelWithOptions(mutate func(*Options)) Model {
	h.t.Helper()
	opts := h.options()
	if mutate != nil {
		mutate(&opts)
	}
	m := h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
	m.expandedFlowID = h.record.FlowID
	m.selectedFlowPhaseID = h.record.Phases[0].PhaseID
	return m
}

// resumePhase returns the persisted record with its single phase replaced, so a
// test names only the divergence between the pane snapshot and the store.
func resumePersistedFlow(mutate func(*flowstore.FlowPhase)) flowstore.FlowRecord {
	record := resumeLaunchFlowRecord()
	phase := record.Phases[0]
	mutate(&phase)
	record.Phases = []flowstore.FlowPhase{phase}
	return record
}

func TestPhaseResumeReadStageRefusals(t *testing.T) {
	liveSession := func(sessionID, launchID string) flowstore.Session {
		return flowstore.Session{Provider: "codex", SessionID: sessionID, LaunchID: launchID, Status: "last_seen"}
	}
	endedSession := func(sessionID, launchID string) flowstore.Session {
		return flowstore.Session{Provider: "codex", SessionID: sessionID, LaunchID: launchID, Status: "ended"}
	}
	tests := []struct {
		name     string
		persist  func(*manualLaunchHarness)
		wantErr  string
		wantSkip bool
	}{
		{
			name: "phase vanished",
			persist: func(h *manualLaunchHarness) {
				record := resumeLaunchFlowRecord()
				record.Phases = nil
				h.persistedFlows = []flowstore.FlowRecord{record}
			},
			wantErr: flowPhaseResumeDriftStatus,
		},
		{
			name: "recoverable await-session running phase",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.Status = flowstore.PhaseRunning
					phase.LaunchIDs = []string{"launch-old", "launch-new"}
					phase.Sessions = []flowstore.Session{endedSession("codex-session", "launch-old")}
				})}
			},
			wantErr: flowPhaseResumeAwaitingSessionStatus,
		},
		{
			name: "recoverable ended-session running phase",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.Status = flowstore.PhaseRunning
				})}
			},
			wantErr: flowPhaseResumeResettableStatus,
		},
		{
			name: "running phase awaiting the newest launch's session",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.Status = flowstore.PhaseRunning
					phase.LaunchIDs = []string{"launch-old", "launch-new"}
					phase.Sessions = []flowstore.Session{liveSession("codex-session", "launch-old")}
				})}
			},
			wantErr: flowPhaseResumeAwaitingSessionStatus,
		},
		{
			name: "running phase whose newest launch already ended",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.Status = flowstore.PhaseRunning
					phase.LaunchIDs = []string{"launch-old", "launch-new"}
					phase.Sessions = []flowstore.Session{
						liveSession("codex-session", "launch-old"),
						endedSession("codex-new", "launch-new"),
					}
				})}
			},
			wantErr: flowPhaseResumeEndedSessionStatus,
		},
		{
			// The newest launch attached a session with no ID, so the
			// ID-requiring lookup would silently fall back to the older one.
			name: "newest session has no id",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.LaunchIDs = []string{"launch-old", "launch-new"}
					phase.Sessions = []flowstore.Session{
						endedSession("codex-session", "launch-old"),
						endedSession("", "launch-new"),
					}
				})}
			},
			wantErr: flowPhaseResumeMissingSessionIDStatus,
		},
		{
			name: "no session left to resume",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.LaunchIDs = nil
					phase.Sessions = nil
				})}
			},
			wantErr: flowPhaseResumeNoSessionStatus,
		},
		{
			name: "session id changed since the key press",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.Sessions = []flowstore.Session{endedSession("codex-other", "launch-old")}
				})}
			},
			wantErr: flowPhaseResumeDriftStatus,
		},
		{
			name: "provider changed since the key press",
			persist: func(h *manualLaunchHarness) {
				h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
					phase.Sessions = []flowstore.Session{{
						Provider: "claude", SessionID: "codex-session", LaunchID: "launch-old", Status: "ended",
					}}
				})}
			},
			wantErr: flowPhaseResumeDriftStatus,
		},
		{
			name: "persisted record lost its worktree",
			persist: func(h *manualLaunchHarness) {
				record := resumeLaunchFlowRecord()
				record.WorktreePath = ""
				h.persistedFlows = []flowstore.FlowRecord{record}
			},
			wantErr: flowPhaseResumeNoWorktreeStatus,
		},
		{
			// A different session on the same phase is still running, so this
			// phase is effectively occupied even though the target is not.
			name: "competing live session on the phase",
			persist: func(h *manualLaunchHarness) {
				h.sessionRecords = []sessions.SessionRecord{{
					SessionID: "codex-other", LaunchID: "launch-old", FlowID: "flow-1", Status: "last_seen",
				}}
			},
			wantErr: flowPhaseResumeLiveSessionStatus,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
			tc.persist(h)

			m := h.resume(h.resumeModel())

			if got := m.status.Text; got != tc.wantErr {
				t.Fatalf("status = %q, want %q", got, tc.wantErr)
			}
			if len(h.launchUpdates) != 0 {
				t.Fatalf("a refused resume wrote the phase: %#v", h.launchUpdates)
			}
			if len(h.phaseUpdates) != 0 {
				t.Fatalf("a refused resume changed phase status: %#v", h.phaseUpdates)
			}
			if len(h.launchContexts) != 0 {
				t.Fatalf("a refused resume opened a terminal: %#v", h.launchContexts)
			}
			if _, ok := m.flowLaunchAttempt("flow-1"); ok {
				t.Fatal("a refused resume must release the Flow")
			}
		})
	}
}

// The session resume reattaches to is expected to look live: a phase whose
// agent died without writing an end marker is exactly the state r exists for,
// and manual launch, repair, and reset are all closed for it. Scoping the
// occupancy predicate to competing sessions is what keeps r open.
func TestPhaseResumeAdmitsItsOwnLiveSession(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "terminal phase"
		if running {
			name = "orphaned running phase"
		}
		t.Run(name, func(t *testing.T) {
			h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
			h.persistedFlows = []flowstore.FlowRecord{resumePersistedFlow(func(phase *flowstore.FlowPhase) {
				if running {
					phase.Status = flowstore.PhaseRunning
				}
				phase.Sessions = []flowstore.Session{{
					Provider: "codex", SessionID: "codex-session", LaunchID: "launch-old", Status: "last_seen",
				}}
			})}
			h.sessionRecords = []sessions.SessionRecord{{
				SessionID: "codex-session", LaunchID: "launch-old", FlowID: "flow-1", Status: "last_seen",
			}}

			m := h.resume(h.resumeModel())

			if got := m.status.Text; got != "" {
				t.Fatalf("status = %q, want the target session not to count as occupancy", got)
			}
			if len(h.launchUpdates) != 1 || !h.launchUpdates[0].Resume {
				t.Fatalf("launch updates = %#v, want one resume write", h.launchUpdates)
			}
			if len(h.launchContexts) != 1 || h.launchContexts[0].ResumeSessionID != "codex-session" {
				t.Fatalf("embedded launches = %#v, want the target session resumed", h.launchContexts)
			}
		})
	}
}

// AddPhaseLaunchID appends a launch ID with no session attached yet, so
// LatestPhaseSession would fall through to a newest-by-timestamp scan and could
// pick a session the read stage never authorized. Identity is resolved once and
// threaded, never re-derived.
func TestPhaseResumeKeepsTheSessionItsReadAuthorized(t *testing.T) {
	h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
	h.persistedRecordOK = true
	h.persistedRecord = resumePersistedFlow(func(phase *flowstore.FlowPhase) {
		phase.LaunchIDs = []string{"launch-old", "resume-token"}
		phase.Sessions = []flowstore.Session{
			{Provider: "codex", SessionID: "codex-session", LaunchID: "launch-old", Status: "ended",
				StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
			{Provider: "codex", SessionID: "codex-newer", LaunchID: "launch-other", Status: "ended",
				StartedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)},
		}
	})

	h.resume(h.resumeModel())

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	if got := h.launchContexts[0].ResumeSessionID; got != "codex-session" {
		t.Fatalf("ResumeSessionID = %q, want the session the read stage validated", got)
	}
}

// The write's record can be phase-less — seams in this repo routinely return
// one — and an unguarded lookup would silently answer "not terminal", which is
// exactly the regression terminal-phase protection exists to prevent.
func TestPhaseResumeKeepsTerminalFlagWhenTheWriteReturnsNoPhase(t *testing.T) {
	h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
	h.startTerminalErr = errors.New("terminal unavailable")
	h.persistedRecordOK = true
	h.persistedRecord = flowstore.FlowRecord{FlowID: "flow-1"}

	m := h.resume(h.resumeModel())

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want a completed phase left alone", h.phaseUpdates)
	}
	if got := m.status.Text; got != "terminal unavailable" {
		t.Fatalf("status = %q, want the launch failure", got)
	}
}

func TestPhaseResumeContextCarriesSettingsAndPaths(t *testing.T) {
	record := resumeLaunchFlowRecord()
	record.PlanID = "plan-1"
	record.PlanPath = "/state/approach/plans/plan-1/plan.md"
	h := newManualLaunchHarness(t, record)

	h.resume(h.resumeModelWithOptions(func(opts *Options) {
		opts.SessionStateRoot = "/state/approach"
		opts.CodexModel = agent.ModelGPT55
		opts.CodexReasoningEffort = agent.ReasoningEffortHigh
	}))

	if len(h.launchContexts) != 1 || len(h.launchUpdates) != 1 {
		t.Fatalf("launches = %#v updates = %#v, want one of each", h.launchContexts, h.launchUpdates)
	}
	ctx := h.launchContexts[0]
	if ctx.LaunchID != h.launchUpdates[0].LaunchID {
		t.Fatalf("ctx.LaunchID = %q, want the admission token %q", ctx.LaunchID, h.launchUpdates[0].LaunchID)
	}
	if ctx.SessionStateRoot != "/state/approach" {
		t.Fatalf("ctx.SessionStateRoot = %q, want the settings snapshot's root", ctx.SessionStateRoot)
	}
	if ctx.PlanID != record.PlanID || ctx.PlanPath != record.PlanPath {
		t.Fatalf("ctx plan = %q %q, want the record's plan verbatim", ctx.PlanID, ctx.PlanPath)
	}
	// A resume reattaches to a session that already chose these; setting them
	// would silently change the resumed command line.
	if ctx.Model != "" || ctx.ReasoningEffort != "" || ctx.InitialPrompt != "" {
		t.Fatalf("ctx = %#v, want no model, effort, or prefill on a resume", ctx)
	}
}

func TestPhaseResumeReleasesItsReservationOnAWriteFailure(t *testing.T) {
	h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
	h.addLaunchIDErr = errors.New("state root locked")

	m := h.resume(h.resumeModel())

	if h.launchReservations != 1 {
		t.Fatalf("launch reservations = %d, want the write to be attempted under one", h.launchReservations)
	}
	if h.launchReleases != 1 {
		t.Fatalf("launch releases = %d, want the reservation released after the failed write", h.launchReleases)
	}
	if got := m.status.Text; got != "failed to mark flow phase resume: state root locked" {
		t.Fatalf("status = %q", got)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("a failed resume write regressed the phase: %#v", h.phaseUpdates)
	}
}

func TestPhaseResumeReportsEmbeddedTerminalCapacity(t *testing.T) {
	h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
	m := h.resumeModel()
	for i := 1; i <= 9; i++ {
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Number:   i,
			Scope:    embeddedTerminalScopeSession,
			Terminal: flowPhaseLaunchTestTerminal{state: "running"},
		})
	}

	m = h.resume(m)

	if got := m.status.Text; got != "Maximum embedded terminals reached" {
		t.Fatalf("status = %q, want the capacity refusal", got)
	}
	if len(h.launchContexts) != 0 {
		t.Fatalf("a full dock started a terminal: %#v", h.launchContexts)
	}
}

func TestPhaseResumeOccupancyRefusals(t *testing.T) {
	record := resumeLaunchFlowRecord()
	flowSlot := func(mutate func(*embeddedTerminalSlot)) embeddedTerminalSlot {
		slot := embeddedTerminalSlot{
			Scope:       embeddedTerminalScopeFlow,
			FlowID:      record.FlowID,
			FlowPhaseID: record.Phases[0].PhaseID,
			Terminal:    flowPhaseLaunchTestTerminal{state: "running"},
		}
		mutate(&slot)
		return slot
	}
	tests := []struct {
		name       string
		occupy     func(Model) Model
		wantStatus string
		wantFooter bool
	}{
		{
			name: "lifecycle attempt",
			occupy: func(m Model) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:  "token-1",
					Kind:   flowLaunchKindManualPhase,
					FlowID: record.FlowID,
				}, flowLaunchStateReading)
				return next
			},
			// Silent today, and this bead does not change it — so the footer
			// must keep advertising, or advertisement and admission disagree in
			// the other direction.
			wantFooter: true,
		},
		{
			name: "pending repair launch",
			occupy: func(m Model) Model {
				return m.withPendingFlowRepairLaunch(record.FlowID, "repair-1")
			},
			wantFooter: true,
		},
		{
			// Satisfies both disjuncts of admission occupancy, which is the one
			// place the terminal status's conjunction is load-bearing.
			name: "open repair terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, flowSlot(func(slot *embeddedTerminalSlot) {
					slot.FlowRepair = true
					slot.FlowPhaseID = ""
				}))
				return m
			},
			wantFooter: true,
		},
		{
			name: "running terminal on another phase of the same Flow",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, flowSlot(func(slot *embeddedTerminalSlot) {
					slot.FlowPhaseID = "review-loop"
				}))
				return m
			},
			wantStatus: flowPhaseResumeTerminalStatus,
		},
		{
			// The behavior change an ordinary user meets: launch, let the agent
			// exit, press r while the slot is still in the dock.
			name: "retained exited terminal on the target phase",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, flowSlot(func(slot *embeddedTerminalSlot) {
					slot.Terminal = flowPhaseLaunchTestTerminal{state: "exited"}
				}))
				return m
			},
			wantStatus: flowPhaseResumeTerminalStatus,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			m := tc.occupy(h.resumeModel())

			if got := m.selectedFlowPhaseResumable(); got != tc.wantFooter {
				t.Fatalf("footer advertises resume = %v, want %v", got, tc.wantFooter)
			}
			next, cmd := m.handleResumeFlowPhaseSession()
			if cmd != nil {
				t.Fatalf("an occupied Flow started resume work: %T", cmd)
			}
			if got := next.(Model).status.Text; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if len(h.launchUpdates) != 0 || len(h.launchContexts) != 0 {
				t.Fatalf("an occupied Flow did work: updates=%#v launches=%#v", h.launchUpdates, h.launchContexts)
			}
		})
	}
}

// codex-app bypasses occupancy entirely, so withdrawing the key for it would
// manufacture the advertisement/admission disagreement the preview exists to
// prevent.
func TestPhaseResumeFooterKeepsAdvertisingCodexAppOverARetainedTerminal(t *testing.T) {
	record := resumeLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.agentCommand = agent.CommandCodexApp
	m := h.resumeModel()
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Scope:    embeddedTerminalScopeFlow,
		FlowID:   record.FlowID,
		Terminal: flowPhaseLaunchTestTerminal{state: "exited"},
	})

	if !m.selectedFlowPhaseResumable() {
		t.Fatal("codex-app resume is exempt from occupancy, so the footer must keep advertising r")
	}
	next, cmd := m.handleResumeFlowPhaseSession()
	if cmd == nil {
		t.Fatal("codex-app resume should still navigate over a retained Flow terminal")
	}
	if got := next.(Model).status.Text; got != "" {
		t.Fatalf("status = %q, want no refusal", got)
	}
}

func TestCodexAppPhaseResumeReportsAReservationFailure(t *testing.T) {
	h := newManualLaunchHarness(t, resumeLaunchFlowRecord())
	h.agentCommand = agent.CommandCodexApp
	h.reserveLaunchErr = errors.New("flow is closed")

	m := h.resume(h.resumeModel())

	if len(h.agentContexts) != 0 {
		t.Fatalf("a failed reservation navigated anyway: %#v", h.agentContexts)
	}
	if got := m.status.Text; got != "Resume Flow phase session: flow is closed" {
		t.Fatalf("status = %q, want the reservation failure", got)
	}
}

// Exact Flow and phase identity: nothing prefix-like may match.
func TestPhaseResumeMatchesExactIdentity(t *testing.T) {
	record := resumeLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.resumeModel()
	// A neighbouring Flow whose ID is a prefix of, and an extension of, the
	// resumed one must not own it.
	next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:  "token-1",
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID + "-2",
	}, flowLaunchStateReading)

	next = h.resume(next)

	if len(h.launchUpdates) != 1 || h.launchUpdates[0].FlowID != record.FlowID {
		t.Fatalf("launch updates = %#v, want the exact Flow resumed", h.launchUpdates)
	}
	if _, ok := next.flowLaunchAttempt(record.FlowID + "-2"); !ok {
		t.Fatal("the neighbouring Flow's attempt must survive untouched")
	}
}

func TestPhaseResumeIgnoresStaleLifecycleEvents(t *testing.T) {
	record := resumeLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.resumeModel()
	next, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:   "live-token",
		Kind:    flowLaunchKindPhaseResume,
		FlowID:  record.FlowID,
		PhaseID: record.Phases[0].PhaseID,
	}, flowLaunchStatePreparing)
	if !ok {
		t.Fatal("reservation should succeed on a free Flow")
	}

	released := 0
	stale := flowLaunchEventMsg{
		Token:   "stale-token",
		Kind:    flowLaunchKindPhaseResume,
		From:    flowLaunchStatePreparing,
		Stage:   flowLaunchStagePrepared,
		FlowID:  record.FlowID,
		PhaseID: record.Phases[0].PhaseID,
		Record:  record,
		Route:   flowLaunchRouteEmbedded,
		Release: func() { released++ },
	}
	after, cmd := next.handleFlowLaunchEvent(stale)
	if cmd != nil {
		t.Fatalf("a stale prepared event started work: %T", cmd)
	}
	if released != 1 {
		t.Fatalf("stale event releases = %d, want its reservation dropped once", released)
	}
	if len(h.launchContexts) != 0 || len(h.phaseUpdates) != 0 {
		t.Fatalf("a stale event mutated state: launches=%#v phases=%#v", h.launchContexts, h.phaseUpdates)
	}
	attempt, ok := after.flowLaunchAttempt(record.FlowID)
	if !ok || attempt.Token != "live-token" {
		t.Fatalf("attempt = %#v, want the live one untouched", attempt)
	}
}

func TestFlowLaunchCancelTextNamesTheAttemptKind(t *testing.T) {
	record := resumeLaunchFlowRecord()
	tests := []struct {
		kind flowLaunchKind
		want string
	}{
		{flowLaunchKindPhaseResume, "Flow phase resume canceled because a repair terminal is already open for this Flow"},
		{flowLaunchKindManualPhase, "Flow phase launch canceled because a repair terminal is already open for this Flow"},
	}
	for _, tc := range tests {
		h := newManualLaunchHarness(t, record)
		m := h.resumeModel()
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Scope:      embeddedTerminalScopeFlow,
			FlowID:     record.FlowID,
			FlowRepair: true,
			Terminal:   flowPhaseLaunchTestTerminal{state: "running"},
		})
		attempt := flowLaunchAttempt{
			Token:        "token-1",
			Kind:         tc.kind,
			FlowID:       record.FlowID,
			PhaseID:      record.Phases[0].PhaseID,
			MutatedPhase: true,
		}
		m, _ = m.reserveFlowLaunchAttempt(attempt, flowLaunchStatePreparing)
		attempt.State = flowLaunchStatePreparing
		next, cmd := m.installFlowLaunchEmbedded(attempt, flowLaunchEventMsg{
			FlowID:  record.FlowID,
			PhaseID: record.Phases[0].PhaseID,
			Context: actions.AgentLaunchContext{
				FlowID:      record.FlowID,
				FlowPhaseID: record.Phases[0].PhaseID,
			},
		})
		if cmd == nil {
			t.Fatalf("%v: a canceled launch should persist its failure", tc.kind)
		}
		_ = next
		_ = cmd()
		if len(h.phaseUpdates) != 1 || !strings.Contains(h.phaseUpdates[0].Notes, tc.want) {
			t.Fatalf("%v: phase updates = %#v, want %q", tc.kind, h.phaseUpdates, tc.want)
		}
	}
}
