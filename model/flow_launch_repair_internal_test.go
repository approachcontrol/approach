package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// These tests own the Flow repair path at the launch lifecycle boundary. Repair
// is Flow-scoped and phase-untracked: it never writes a phase, so the only
// persistence it touches is the cross-process repair reservation.
//
// The fixtures use real directories because repair resolves its launch paths
// through os.Stat, which is the one place it differs from the tracked kinds.

func repairLaunchFlowRecord(t *testing.T) flowstore.FlowRecord {
	t.Helper()
	dir := t.TempDir()
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     dir,
		WorktreePath: dir,
		Branch:       "flow/one",
		Commit:       "abc123",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Kind:    flowstore.KindImplementation,
			Status:  flowstore.PhaseBlocked,
			Outcome: flowstore.OutcomeBlocked,
			Notes:   "persisted metadata is inconsistent",
			Order:   1,
		}},
	}
}

// repairModel selects the record's own repo. Every prepare-stage refusal now
// reaches the user through the repo-gated ActionFailedMsg, so a harness whose
// selected repo did not match would assert against a status no user sees.
func (h *manualLaunchHarness) repairModel() Model {
	h.t.Helper()
	return h.modelWith([]scanner.Repo{{Path: h.record.RepoPath, DisplayName: "alpha"}}, h.options())
}

// repair presses R and drains the resulting command chain the way the bubbletea
// runtime would.
func (h *manualLaunchHarness) repair(m Model) Model {
	h.t.Helper()
	next, cmd := m.handleRepairSelectedFlow()
	return h.drain(next.(Model), cmd, 0)
}

func liveRepairSessionRecord(flowID, launchID string) sessions.SessionRecord {
	return sessions.SessionRecord{
		SessionID: "live-session",
		Provider:  "codex",
		LaunchID:  launchID,
		FlowID:    flowID,
		Status:    "last_seen",
	}
}

func TestFlowRepairLaunchOpensOneUntrackedSlot(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)

	m := h.repair(h.repairModel())

	if len(h.launchUpdates) != 0 {
		t.Fatalf("repair wrote %#v, want no phase launch at all", h.launchUpdates)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("repair wrote phase status %#v, want none", h.phaseUpdates)
	}
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if !ctx.FlowRepair ||
		ctx.FlowPhaseID != "" ||
		ctx.FlowLaunchTracked ||
		ctx.FlowID != record.FlowID ||
		ctx.RepoPath != record.RepoPath ||
		ctx.WorktreePath != record.WorktreePath ||
		ctx.Branch != record.Branch ||
		ctx.Commit != record.Commit ||
		!ctx.Embedded ||
		ctx.InitialPrompt == "" {
		t.Fatalf("repair launch context = %#v", ctx)
	}
	if len(h.agentContexts) != 0 {
		t.Fatalf("a repair opens an embedded terminal, never an external handoff: %#v", h.agentContexts)
	}
	if h.repairReservations != 1 || h.repairReleases != 1 {
		t.Fatalf("repair reservations = %d, releases = %d, want one held then released",
			h.repairReservations, h.repairReleases)
	}
	if h.launchReservations != 0 {
		t.Fatalf("repair took %d tracked launch reservations, want only the repair one", h.launchReservations)
	}
	if _, held := m.flowLaunchAttempt(record.FlowID); held {
		t.Fatal("the installed repair slot owns the Flow; the attempt must be released")
	}
	if m.status.Text != "" {
		t.Fatalf("successful repair set status %q", m.status.Text)
	}
}

// The slot identity is what AC3's "nondetachable" means in model/: a repair
// terminal is its own kind of slot, carrying no phase. Nothing here refuses a
// detach — handleEmbeddedTerminalDetachPrefix gates only on tmux backing, and
// the TUI guide documents repair terminals as cleared by dismiss or detach.
func TestFlowRepairLaunchSlotKeepsRepairIdentity(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)

	m := h.repair(h.repairModel())

	if len(m.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %#v, want exactly one repair slot", m.embeddedTerminals)
	}
	slot := m.embeddedTerminals[0]
	if !slot.FlowRepair || slot.FlowID != record.FlowID || slot.FlowPhaseID != "" || slot.Identity != "repair" {
		t.Fatalf("repair slot = %#v, want an explicit Flow-scoped repair identity", slot)
	}
}

func TestFlowRepairLaunchReadRefusals(t *testing.T) {
	awaitingPhase := func(record flowstore.FlowRecord) flowstore.FlowRecord {
		record.Phases = []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Kind:      flowstore.KindImplementation,
			Status:    flowstore.PhaseRunning,
			LaunchIDs: []string{"launch-1"},
			Order:     1,
		}}
		return record
	}
	tests := []struct {
		name string
		// persisted diverges from the record the pane holds, which is what the
		// authoritative read exists to catch.
		persisted func(flowstore.FlowRecord) flowstore.FlowRecord
		sessions  []sessions.SessionRecord
		wantErr   string
	}{
		{
			name: "no longer repairable",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
				record.Phases[0].Status = flowstore.PhaseReady
				return record
			},
			wantErr: flowRepairNotRepairableStatus,
		},
		{
			// The classifier cannot see this: the phase has mirrored no session
			// for launch-1 yet, so rule 1 passes and only the session store
			// knows an agent holds this Flow.
			name: "live store session for a phase launch",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
				record.Phases[0].LaunchIDs = []string{"launch-1"}
				return record
			},
			sessions: []sessions.SessionRecord{liveRepairSessionRecord("flow-1", "launch-1")},
			wantErr:  flowRepairLiveSessionStatus,
		},
		{
			// The named trap, asserted as intended behaviour. A running phase
			// awaiting session capture whose newest launch has a live store
			// record is either an agent that is starting or an agent that died
			// between the session write and the phase attach, and nothing in
			// sessions distinguishes them. Repair refuses both; `approach flow
			// phase reset` is the documented way out of the second.
			name: "awaiting session capture with a live store session",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				return awaitingPhase(record)
			},
			sessions: []sessions.SessionRecord{liveRepairSessionRecord("flow-1", "launch-1")},
			wantErr:  flowRepairLiveSessionStatus,
		},
		{
			// An ended store record is not occupancy, so the same stale launch
			// stays repairable once its session finishes.
			name: "awaiting session capture with an ended store session",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				return awaitingPhase(record)
			},
			sessions: []sessions.SessionRecord{{
				SessionID: "dead-session",
				Provider:  "codex",
				LaunchID:  "launch-1",
				FlowID:    "flow-1",
				Status:    "ended",
				EndedAt:   time.Now(),
			}},
		},
		{
			// Repair's primary use case. Refusing it would delete the reason the
			// key exists.
			name: "awaiting session capture with no store session",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				return awaitingPhase(record)
			},
		},
		{
			// Previous repair and worktree-agent sessions carry a Flow ID but no
			// phase launch ID, so a never-finalized one cannot permanently block
			// future repairs.
			name: "live untracked session for the same Flow",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				return record
			},
			sessions: []sessions.SessionRecord{liveRepairSessionRecord("flow-1", "repair-launch")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := repairLaunchFlowRecord(t)
			h := newManualLaunchHarness(t, record)
			h.persistedFlows = []flowstore.FlowRecord{tc.persisted(record)}
			h.sessionRecords = tc.sessions

			m := h.repair(h.repairModel())

			if tc.wantErr == "" {
				if len(h.launchContexts) != 1 {
					t.Fatalf("embedded launches = %#v, want the repair to be admitted", h.launchContexts)
				}
				return
			}
			if len(h.launchContexts) != 0 {
				t.Fatalf("a refused repair started %#v", h.launchContexts)
			}
			if got := m.status.Text; got != tc.wantErr {
				t.Fatalf("status = %q, want %q", got, tc.wantErr)
			}
			if _, held := m.flowLaunchAttempt(record.FlowID); held {
				t.Fatal("a refused repair stranded its attempt")
			}
			if h.repairReservations != 0 {
				t.Fatalf("a read-stage refusal took %d repair reservations, want zero", h.repairReservations)
			}
		})
	}
}

// The path refusal is raised by the read stage now, one hop later than the key
// handler used to raise it, and only when the current repo cannot stand in
// either — the intent carries that fallback precisely so it can.
func TestFlowRepairRefusesWhenNoDirectoryIsUsable(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	persisted := record
	persisted.RepoPath = "/dev/null/missing-repo"
	persisted.WorktreePath = "/dev/null/missing-worktree"
	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{persisted}
	m := h.modelWith([]scanner.Repo{{Path: "/dev/null/missing-current-repo"}}, h.options())

	m = h.repair(m)

	if len(h.launchContexts) != 0 {
		t.Fatalf("a repair with no usable directory started %#v", h.launchContexts)
	}
	if got := m.status.Text; got != flowRepairNoDirectoryStatus {
		t.Fatalf("status = %q, want %q", got, flowRepairNoDirectoryStatus)
	}
	if h.repairReservations != 0 {
		t.Fatalf("a read-stage refusal took %d repair reservations, want zero", h.repairReservations)
	}
}

// The Flow-scoped effect of the session rule: one phase with a live phase-launch
// session refuses repair of the whole Flow, including an unrelated gated phase.
// That follows from repair being Flow-scoped — its prompt authorizes phase reset,
// phase set, and plan set across the record.
func TestFlowRepairRefusesWhenAnyPhaseHasALiveSession(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	record.Phases = []flowstore.FlowPhase{
		{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}, Order: 1},
		{PhaseID: "review-loop", Kind: flowstore.KindReviewLoop, Status: flowstore.PhaseBlocked, DependsOn: []string{"implementation"}, Order: 2},
	}
	h := newManualLaunchHarness(t, record)
	h.sessionRecords = []sessions.SessionRecord{liveRepairSessionRecord(record.FlowID, "launch-1")}

	m := h.repair(h.repairModel())

	if len(h.launchContexts) != 0 {
		t.Fatalf("repair started alongside a live phase session: %#v", h.launchContexts)
	}
	if got := m.status.Text; got != flowRepairLiveSessionStatus {
		t.Fatalf("status = %q, want %q", got, flowRepairLiveSessionStatus)
	}
}

// The read stage resolves the linked plan path when the record carries a plan ID
// with no path. The nil-seam guard is exercised from a zero-value Model, which
// is how several repair tests build theirs and where an unguarded call panics.
func TestFlowRepairReadResolvesLinkedPlanPath(t *testing.T) {
	t.Run("resolved", func(t *testing.T) {
		record := repairLaunchFlowRecord(t)
		record.PlanID = "plan-1"
		h := newManualLaunchHarness(t, record)
		opts := h.options()
		opts.PlanMarkdownPath = func(planID string) (string, error) {
			return "/state/plans/" + planID + "/plan.md", nil
		}
		m := h.modelWith([]scanner.Repo{{Path: record.RepoPath}}, opts)

		h.repair(m)

		if len(h.launchContexts) != 1 {
			t.Fatalf("embedded launches = %#v, want one", h.launchContexts)
		}
		if got := h.launchContexts[0].PlanPath; got != "/state/plans/plan-1/plan.md" {
			t.Fatalf("repair plan path = %q, want the resolved linked plan", got)
		}
	})

	t.Run("nil seam", func(t *testing.T) {
		record := repairLaunchFlowRecord(t)
		record.PlanID = "plan-1"
		intent := flowLaunchIntent{Kind: flowLaunchKindRepair, FlowID: record.FlowID}
		seams := flowLaunchSeams{
			ReadFlow: func(string) (flowstore.FlowRecord, error) { return record, nil },
			ListFlowSessions: func(string) ([]sessions.SessionRecord, error) {
				return nil, nil
			},
		}

		event, ok := repairFlowLaunchReadCmd(seams, intent, "token-1")().(flowLaunchEventMsg)
		if !ok {
			t.Fatal("repair read returned the wrong message type")
		}
		if event.Err != flowRepairNoPlanPathStatus {
			t.Fatalf("event.Err = %q, want %q", event.Err, flowRepairNoPlanPathStatus)
		}
	})
}

// The fallback the intent carries is the current repo, snapshotted at the key
// press because the read stage has no Model. Losing that threading is what this
// guards: a record whose worktree and repo are both gone still repairs from the
// selected repository.
func TestFlowRepairFallsBackToCurrentRepoWhenRecordedPathsAreMissing(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	fallback := record.RepoPath
	persisted := record
	persisted.RepoPath = "/dev/null/deleted-repo"
	persisted.WorktreePath = "/dev/null/deleted-worktree"
	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{persisted}
	h.repairReserved = persisted
	h.repairReservedOK = true

	h.repair(h.repairModel())

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want one", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if ctx.RepoPath != fallback || ctx.WorktreePath != fallback {
		t.Fatalf("repair paths = repo %q worktree %q, want the current-repo fallback %q",
			ctx.RepoPath, ctx.WorktreePath, fallback)
	}
}

// Prompt, headless preference, worktree, and branch all come from the reserved
// record, not from the cached snapshot the key press saw.
func TestFlowRepairContextIsRefreshedFromTheReservedRecord(t *testing.T) {
	for _, tt := range []struct {
		name          string
		cached        bool
		authoritative bool
	}{
		{name: "cached on authoritative off", cached: true, authoritative: false},
		{name: "cached off authoritative on", cached: false, authoritative: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := repairLaunchFlowRecord(t)
			record.Headless = tt.cached
			reserved := record
			reserved.Headless = tt.authoritative
			reserved.Branch = "flow/reserved"
			reserved.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
			reserved.Phases[0].Notes = "reserved obstruction wording"
			h := newManualLaunchHarness(t, record)
			h.repairReserved = reserved
			h.repairReservedOK = true

			h.repair(h.repairModel())

			if len(h.launchContexts) != 1 {
				t.Fatalf("embedded launches = %#v, want one", h.launchContexts)
			}
			ctx := h.launchContexts[0]
			if ctx.Headless != tt.authoritative {
				t.Fatalf("repair headless = %v, want the reserved record's %v", ctx.Headless, tt.authoritative)
			}
			if ctx.Branch != reserved.Branch {
				t.Fatalf("repair branch = %q, want the reserved record's %q", ctx.Branch, reserved.Branch)
			}
			if !strings.Contains(ctx.InitialPrompt, "is blocked") {
				t.Fatalf("repair prompt does not describe the reserved obstruction:\n%s", ctx.InitialPrompt)
			}
		})
	}
}

func TestFlowRepairReservationIsHeldUntilTheTerminalStarts(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	released := 0
	h.repairReserved = record
	h.repairReservedOK = true
	opts := h.options()
	baseReserve := opts.ReserveFlowRepairLaunch
	opts.ReserveFlowRepairLaunch = func(flowID string) (flowstore.FlowRecord, func(), error) {
		reserved, release, err := baseReserve(flowID)
		if err != nil {
			return reserved, release, err
		}
		return reserved, func() { released++; release() }, nil
	}
	startedWhileHeld := false
	opts.StartEmbeddedTerminal = func(ctx actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
		startedWhileHeld = released == 0
		h.launchContexts = append(h.launchContexts, ctx)
		return flowPhaseLaunchTestTerminal{state: "running"}, nil
	}

	h.repair(h.modelWith([]scanner.Repo{{Path: record.RepoPath}}, opts))

	if !startedWhileHeld {
		t.Fatal("repair reservation was released before the terminal started")
	}
	if released != 1 {
		t.Fatalf("repair reservation releases = %d, want exactly one", released)
	}
}

func TestFlowRepairReservationFailureRefusesWithoutMutatingThePhase(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	h.reserveRepairErr = errors.New("cannot reserve repair because flow is closed")

	m := h.repair(h.repairModel())

	const wantPrefix = "Reserve persisted Flow for repair: "
	if got := m.status.Text; !strings.HasPrefix(got, wantPrefix) || !strings.Contains(got, "closed") {
		t.Fatalf("status = %q, want %q plus the store's own error", got, wantPrefix)
	}
	if len(h.launchContexts) != 0 || len(h.phaseUpdates) != 0 || len(h.launchUpdates) != 0 {
		t.Fatalf("a refused reservation launched or persisted: terminals=%#v phases=%#v launches=%#v",
			h.launchContexts, h.phaseUpdates, h.launchUpdates)
	}
	if _, held := m.flowLaunchAttempt(record.FlowID); held {
		t.Fatal("a refused reservation stranded its attempt")
	}
}

// The prepare-stage refusal reaches the user through ActionFailedMsg, which is
// repo-gated. A user who changes the selected repo while the reservation is in
// flight, with Active Flows closed, now sees nothing — the same trade the
// tracked resume migration accepted.
func TestFlowRepairReservationFailureStatusIsRepoGated(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	h.reserveRepairErr = errors.New("cannot reserve repair because flow is closed")
	m := h.modelWith([]scanner.Repo{{Path: "/dev/elsewhere"}, {Path: record.RepoPath}}, h.options())
	m.status.Text = "keep this"

	m = h.repair(m)

	if m.status.Text != "keep this" {
		t.Fatalf("status = %q, want the failure suppressed for an unselected repo", m.status.Text)
	}
}

func TestFlowRepairCapacityFailureRefusesWithoutMutatingThePhase(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.repairModel()
	for i := 1; i <= 9; i++ {
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Number:   i,
			Scope:    embeddedTerminalScopeSession,
			Terminal: flowPhaseLaunchTestTerminal{state: "running"},
		})
	}

	m = h.repair(m)

	if got := m.status.Text; got != "Maximum embedded terminals reached" {
		t.Fatalf("status = %q, want the capacity refusal", got)
	}
	if len(h.phaseUpdates) != 0 || len(h.launchUpdates) != 0 {
		t.Fatalf("a capacity refusal persisted phases=%#v launches=%#v", h.phaseUpdates, h.launchUpdates)
	}
	if _, held := m.flowLaunchAttempt(record.FlowID); held {
		t.Fatal("a capacity refusal stranded its attempt")
	}
	if h.repairReleases != 1 {
		t.Fatalf("repair reservation releases = %d, want the lock dropped on failure too", h.repairReleases)
	}
}

func TestFlowRepairAdmissionRefusals(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	tests := []struct {
		name       string
		occupy     func(Model) Model
		agent      string
		wantStatus string
		wantFooter bool
	}{
		{
			name: "repair attempt",
			occupy: func(m Model) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:  "repair-1",
					Kind:   flowLaunchKindRepair,
					FlowID: record.FlowID,
				}, flowLaunchStateReading)
				return next
			},
			wantStatus: flowRepairPendingStatus,
		},
		{
			name: "phase resume attempt",
			occupy: func(m Model) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:   "resume-1",
					Kind:    flowLaunchKindPhaseResume,
					FlowID:  record.FlowID,
					PhaseID: record.Phases[0].PhaseID,
				}, flowLaunchStateReading)
				return next
			},
			wantStatus: flowRepairResumePendingStatus,
		},
		{
			// The rung that has to be written as "terminal or any attempt": a
			// manual or automatic launch in flight reaches it, and naming only
			// the terminals would fall through to the headless message and tell
			// the user something false.
			name: "manual phase attempt",
			occupy: func(m Model) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:   "manual-1",
					Kind:    flowLaunchKindManualPhase,
					FlowID:  record.FlowID,
					PhaseID: record.Phases[0].PhaseID,
				}, flowLaunchStateReading)
				return next
			},
			wantStatus: flowRepairTerminalStatus,
		},
		{
			name: "auto phase attempt",
			occupy: func(m Model) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:   "auto-1",
					Kind:    flowLaunchKindAutoPhase,
					FlowID:  record.FlowID,
					PhaseID: record.Phases[0].PhaseID,
				}, flowLaunchStateReading)
				return next
			},
			wantStatus: flowRepairTerminalStatus,
		},
		{
			name: "retained flow terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:    embeddedTerminalScopeFlow,
					FlowID:   record.FlowID,
					Terminal: flowPhaseLaunchTestTerminal{state: "exited"},
				})
				return m
			},
			wantStatus: flowRepairTerminalStatus,
		},
		{
			// A repair slot with no terminal is unreachable through
			// openEmbeddedTerminalWithLabel, which appends a slot only once a
			// terminal exists. It is named anyway so the refusal can never fall
			// through to the headless message, which would be false for it.
			name: "repair slot without a terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:      embeddedTerminalScopeFlow,
					FlowID:     record.FlowID,
					FlowRepair: true,
				})
				return m
			},
			wantStatus: flowRepairTerminalStatus,
		},
		{
			name: "pending headless write",
			occupy: func(m Model) Model {
				return m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
					flowID:   record.FlowID,
					repoPath: record.RepoPath,
					enabled:  false,
				})
			},
			wantStatus: flowHeadlessWritePendingStatus,
		},
		{
			name:       "no agent chosen",
			agent:      " ",
			wantStatus: "Press A to choose codex or claude before repairing a Flow",
			wantFooter: true,
		},
		{
			name:       "codex app",
			agent:      "codex-app",
			wantStatus: "Flow repair requires an embedded CLI agent; press A to choose codex or claude",
			wantFooter: true,
		},
		{
			name:       "unsupported agent",
			agent:      "not a provider",
			wantStatus: `Flow repair does not support agent "not a provider"; press A to choose codex or claude`,
			wantFooter: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			h.agentCommand = tc.agent
			m := h.repairModel()
			if tc.occupy != nil {
				m = tc.occupy(m)
			}
			// The footer and the key must never disagree: an occupied Flow
			// withdraws R, while an unconfigured agent keeps advertising it and
			// explains itself on the press, exactly as before.
			if got := m.selectedFlowRepairReady(); got != tc.wantFooter {
				t.Fatalf("selectedFlowRepairReady() = %v, want %v", got, tc.wantFooter)
			}

			m = h.repair(m)

			if len(h.launchContexts) != 0 {
				t.Fatalf("a refused repair started %#v", h.launchContexts)
			}
			if got := m.status.Text; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if h.repairReservations != 0 {
				t.Fatalf("an admission refusal took %d repair reservations, want zero", h.repairReservations)
			}
		})
	}
}

// A replayed lifecycle message is fenced by the attempt token, so it is a silent
// no-op rather than the "no longer current" status the pending map used to set.
func TestFlowRepairLaunchFencesReplayedEvents(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.repairModel()

	next, cmd := m.handleRepairSelectedFlow()
	m = next.(Model)
	if cmd == nil {
		t.Fatal("R should submit a repair intent")
	}
	readMsg, ok := runCommandWithoutWaiting(cmd)
	if !ok {
		t.Fatal("the repair read command did not settle")
	}
	m = h.drainMsg(m, readMsg, 0)
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want one", h.launchContexts)
	}
	before := m.status.Text

	replayed, replayCmd := m.Update(readMsg)
	m = replayed.(Model)
	if replayCmd != nil {
		t.Fatal("a replayed repair read must be a fenced no-op")
	}
	if len(h.launchContexts) != 1 {
		t.Fatalf("a replayed repair read started %#v", h.launchContexts)
	}
	if m.status.Text != before {
		t.Fatalf("a replayed repair read changed the status to %q", m.status.Text)
	}
}

// AC4: a dismissed repair terminal still arms the ordinary auto-advance drain,
// and the relaunch that follows is a fresh autoPhase intent — not a repair.
func TestFlowRepairTerminalRemovalRelaunchesThroughAutoPhaseIntent(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	record.AutoMode = true
	h := newManualLaunchHarness(t, record)
	m := h.repair(h.repairModel())
	if len(m.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %#v, want the repair slot", m.embeddedTerminals)
	}

	// The drain only arms behind a clean exit, which is the agent finishing on
	// its own before the user closes the slot.
	m.embeddedTerminals[0].Terminal = flowPhaseLaunchTestTerminal{state: "exited"}
	m = m.dismissEmbeddedTerminalForReason(m.embeddedTerminals[0].ID, embeddedTerminalRemovalUserClose)
	marker, armed := m.pendingRepairAutoDrainFlowIDs[record.FlowID]
	if !armed || !marker.CleanExit {
		t.Fatalf("repair auto-drain marker = %#v armed=%v, want a clean-exit marker", marker, armed)
	}

	// The Flow the poll sees has advanced past the obstruction the repair fixed.
	repaired := record
	repaired.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	repaired.Phases[0].Status = flowstore.PhaseReady
	repaired.Phases[0].Outcome = ""
	h.record = repaired
	h.persistedFlows = []flowstore.FlowRecord{repaired}

	m = m.consumeRepairAutoDrainMarkers([]flowstore.FlowRecord{repaired}, marker.MinimumRequest)
	next, cmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{repaired})
	if cmd == nil {
		t.Fatal("the post-repair drain should schedule an auto launch")
	}
	if kind := next.flowLaunchAttemptKind(record.FlowID); kind != flowLaunchKindAutoPhase {
		t.Fatalf("post-repair attempt kind = %v, want autoPhase", kind)
	}
	m = h.drain(next, cmd, 0)
	if len(h.launchUpdates) != 1 || !h.launchUpdates[0].AutoLaunch {
		t.Fatalf("post-repair launch updates = %#v, want one AutoLaunch write", h.launchUpdates)
	}
	if len(h.launchContexts) != 2 || !h.launchContexts[1].FlowLaunchTracked || h.launchContexts[1].FlowRepair {
		t.Fatalf("post-repair launch contexts = %#v, want a tracked phase launch", h.launchContexts)
	}
}

// The footer's predicate and admission agree on every occupancy signal, and the
// two terms admission cannot carry — the headless write and the surface gate —
// stay outside the preview boundary on purpose.
func TestSelectedFlowRepairReadyMatchesAdmission(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	base := h.repairModel()
	if !base.selectedFlowRepairReady() {
		t.Fatal("the fixture should be repairable before anything holds the Flow")
	}
	hidden := base
	hidden.contentPane = ui.PaneTop
	hidden.topMode = ui.ModeWorktrees
	if hidden.selectedFlowRepairReady() {
		t.Fatal("R must not arm from a pane that shows no Flow")
	}
}
