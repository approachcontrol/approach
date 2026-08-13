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

// repairModel selects the record's own repo, which is what a user pressing R on
// a Flow row is looking at. Repair's refusals are not repo-gated — see
// TestFlowRepairFailuresAreReportedForAnUnselectedRepo — so this is realism,
// not a precondition for seeing a status.
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
			// The fixture phase is blocked, so the remedy is the two-step one.
			wantErr: flowRepairLiveSessionStatus("flow-1", flowstore.FlowPhase{
				PhaseID: "implementation",
				Status:  flowstore.PhaseBlocked,
			}),
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
			wantErr: flowRepairLiveSessionStatus("flow-1", flowstore.FlowPhase{
				PhaseID: "implementation",
				Status:  flowstore.PhaseRunning,
			}),
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
	// The refusal names the phase the live session belongs to, not the gated
	// phase the user is trying to unblock, because `approach flow phase reset`
	// takes that phase ID.
	if want := flowRepairLiveSessionStatus(record.FlowID, record.Phases[0]); m.status.Text != want {
		t.Fatalf("status = %q, want %q", m.status.Text, want)
	}
	// Runnability is the point of the remedy, and `approach flow phase reset`
	// requires both flags and reads neither from the environment, so both are
	// asserted with their values rather than as bare flag names.
	if !strings.Contains(m.status.Text, "--flow-id "+record.FlowID+" --phase-id implementation") {
		t.Fatalf("status = %q, want a remedy that runs as printed", m.status.Text)
	}
	// This is the one repair refusal the footer cannot anticipate: the preview
	// answers from the cached record, and the evidence lives in the session
	// store one asynchronous hop later. R stays advertised and every press
	// refuses. Pinned because the docs describe this exact asymmetry.
	if !m.selectedFlowRepairReady() {
		t.Fatal("the footer answers from the cached record; a session-store refusal cannot withdraw R")
	}
}

// The remedy has to be legal for the status it is printed against, which is the
// whole reason this refusal names a command at all. The occupancy rule does not
// filter by status, so all three branches are reachable, and the literal strings
// are pinned rather than round-tripped through the formatter: the bug this
// guards against was a runnable-looking command that flowstore always refuses.
func TestFlowRepairLiveSessionRemedyMatchesPhaseStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{
			// ResetRecoverableRunningPhase accepts only running, so this is the
			// one status where reset alone is the whole remedy.
			name:   "running resets directly",
			status: flowstore.PhaseRunning,
			want:   "Flow phase implementation already has a running session; if its agent is gone, clear it with approach flow phase reset --flow-id flow-1 --phase-id implementation",
		},
		{
			// The agent moved the phase off running before it died. Reset would
			// refuse with `flow phase reset requires running recoverable phase`,
			// so the message has to carry the transition back first — as
			// `restart`, because a bare `phase set --status running` out of
			// blocked is refused for missing notes.
			name:   "blocked restarts first",
			status: flowstore.PhaseBlocked,
			want:   "Flow phase implementation already has a running session and is blocked; if its agent is gone, clear it with approach flow phase restart --flow-id flow-1 --phase-id implementation, then approach flow phase reset --flow-id flow-1 --phase-id implementation",
		},
		{
			name:   "needs attention restarts first",
			status: flowstore.PhaseNeedsAttention,
			want:   "Flow phase implementation already has a running session and is needs_attention; if its agent is gone, clear it with approach flow phase restart --flow-id flow-1 --phase-id implementation, then approach flow phase reset --flow-id flow-1 --phase-id implementation",
		},
		{
			// Where a reset that stranded an older launch's live session lands
			// the phase. There is no non-destructive route back to running from
			// here, so the message must not invent a command.
			name:   "pending has no command remedy",
			status: flowstore.PhasePending,
			want:   "Flow phase implementation already has a running session and is pending; reset needs a running phase, so this phase's session metadata has to be corrected directly",
		},
		{
			// completed -> running is a legal transition, which is exactly why
			// the branch enumerates statuses instead of consulting the table:
			// reopening a finished phase to clear a session record trades a real
			// result for cleanup, and reset would then derive it back to ready.
			name:   "completed is never reopened to clear a session",
			status: flowstore.PhaseCompleted,
			want:   "Flow phase implementation already has a running session and is completed; reset needs a running phase, so this phase's session metadata has to be corrected directly",
		},
		{
			name:   "skipped is never reopened to clear a session",
			status: flowstore.PhaseSkipped,
			want:   "Flow phase implementation already has a running session and is skipped; reset needs a running phase, so this phase's session metadata has to be corrected directly",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase := flowstore.FlowPhase{PhaseID: "implementation", Status: tc.status}
			if got := flowRepairLiveSessionStatus("flow-1", phase); got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
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

	t.Run("lookup error", func(t *testing.T) {
		record := repairLaunchFlowRecord(t)
		record.PlanID = "plan-1"
		h := newManualLaunchHarness(t, record)
		opts := h.options()
		opts.PlanMarkdownPath = func(string) (string, error) {
			return "", errors.New("plan store unreadable")
		}
		m := h.modelWith([]scanner.Repo{{Path: record.RepoPath}}, opts)

		m = h.repair(m)

		if len(h.launchContexts) != 0 {
			t.Fatalf("a repair with an unresolvable plan started %#v", h.launchContexts)
		}
		if got := m.status.Text; got != "plan store unreadable" {
			t.Fatalf("status = %q, want the plan lookup error", got)
		}
		if h.repairReservations != 0 {
			t.Fatalf("a read-stage refusal took %d repair reservations, want zero", h.repairReservations)
		}
	})
}

// The session listing is what the whole live-session rule is decided from, so a
// failed listing must refuse rather than fall through to an empty slice. An
// implementation that swallowed the error would silently disable the rule and
// every other repair test would still pass.
func TestFlowRepairRefusesWhenTheSessionListingFails(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	h := newManualLaunchHarness(t, record)
	h.sessionsErr = errors.New("session store unreadable")

	m := h.repair(h.repairModel())

	if len(h.launchContexts) != 0 {
		t.Fatalf("a repair with an unreadable session store started %#v", h.launchContexts)
	}
	if got := m.status.Text; got != "session store unreadable" {
		t.Fatalf("status = %q, want the session listing error", got)
	}
	if _, held := m.flowLaunchAttempt(record.FlowID); held {
		t.Fatal("a refused repair stranded its attempt")
	}
	if h.repairReservations != 0 {
		t.Fatalf("a read-stage refusal took %d repair reservations, want zero", h.repairReservations)
	}
}

// The substitution this migration made in flowAutoAdvanceOccupied: the pending
// repair map was deleted and the lifecycle attempt now stands in for it. The
// attempt is taken at the key press and held until the slot is installed, so
// AutoMode must defer for a repair that has not reached its terminal yet — the
// window the deleted map used to cover.
func TestFlowRepairAttemptDefersTheAutoAdvanceDrain(t *testing.T) {
	record := repairLaunchFlowRecord(t)
	record.AutoMode = true
	h := newManualLaunchHarness(t, record)
	m := h.repairModel()

	next, _, admitted := m.requestFlowLaunch(m.repairFlowLaunchIntent(""))
	if !admitted {
		t.Fatal("repair was not admitted; the rest of this test asserts nothing")
	}
	m = next
	attempt, held := m.flowLaunchAttempt(record.FlowID)
	if !held {
		t.Fatal("admission must hold the attempt from the key press onward")
	}

	if !m.flowAutoAdvanceOccupied(record) {
		t.Fatal("an in-flight repair attempt must defer the auto-advance drain")
	}
	if m.releaseFlowLaunchAttempt(record.FlowID, attempt.Token).flowAutoAdvanceOccupied(record) {
		t.Fatal("occupancy must be the attempt itself, not some other property of this record")
	}
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

// Every refusal decided against the authoritative record refreshes the Flow
// surface, because the row the user is looking at is the stale one that
// advertised R in the first place — "Flow is no longer repairable" is exactly
// that case. Both stages that read it owe the refresh, which is why the read
// stage takes failFlowLaunch's exit rather than the generic release-and-status
// one. An admission refusal is decided against the cached record itself, so it
// owes nothing and must not fetch.
func TestFlowRepairRefusalRefreshesTheFlowSurface(t *testing.T) {
	notRepairable := func(record flowstore.FlowRecord) flowstore.FlowRecord {
		record.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
		record.Phases[0].Status = flowstore.PhaseReady
		return record
	}
	for _, tt := range []struct {
		name      string
		occupy    func(Model, flowstore.FlowRecord) Model
		setup     func(*manualLaunchHarness, flowstore.FlowRecord)
		wantFetch bool
	}{
		{
			name: "read stage",
			setup: func(h *manualLaunchHarness, record flowstore.FlowRecord) {
				h.persistedFlows = []flowstore.FlowRecord{notRepairable(record)}
			},
			wantFetch: true,
		},
		{
			// The read stage passes and only the reserved record disagrees, so
			// the same refusal lands one stage later.
			name: "prepare stage",
			setup: func(h *manualLaunchHarness, record flowstore.FlowRecord) {
				h.repairReserved = notRepairable(record)
				h.repairReservedOK = true
			},
			wantFetch: true,
		},
		{
			name: "admission",
			occupy: func(m Model, record flowstore.FlowRecord) Model {
				return m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
					flowID:   record.FlowID,
					repoPath: record.RepoPath,
				})
			},
			wantFetch: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := repairLaunchFlowRecord(t)
			h := newManualLaunchHarness(t, record)
			if tt.setup != nil {
				tt.setup(h, record)
			}
			m := h.repairModel()
			if tt.occupy != nil {
				m = tt.occupy(m, record)
			}
			// Model construction and pane seeding both list; only what the
			// refusal itself adds is the behaviour under test.
			before := h.flowListCalls

			m = h.repair(m)

			if len(h.launchContexts) != 0 {
				t.Fatalf("a refused repair started %#v", h.launchContexts)
			}
			if m.status.Text == "" {
				t.Fatal("a refused repair must report a status")
			}
			if fetched := h.flowListCalls > before; fetched != tt.wantFetch {
				t.Fatalf("Flow surface fetched = %v, want %v (list calls %d -> %d)",
					fetched, tt.wantFetch, before, h.flowListCalls)
			}
			if _, held := m.flowLaunchAttempt(record.FlowID); held {
				t.Fatal("a refused repair stranded its attempt")
			}
		})
	}
}

// flowRepairLaunchPaths is the one piece of repair that touches the filesystem,
// and both stages run it: the read stage with the intent's current-repo
// fallback, the refresh with the already-resolved context paths. The
// end-to-end tests above cover the case where every recorded path is gone; this
// covers the rest of the ladder directly, in particular the recorded repo
// standing in as the worktree, which no lifecycle test reaches.
func TestFlowRepairLaunchPathsFallbackLadder(t *testing.T) {
	usable := t.TempDir()
	other := t.TempDir()
	missing := "/dev/null/deleted"

	for _, tt := range []struct {
		name         string
		repoPath     string
		worktreePath string
		fallbacks    []string
		wantRepo     string
		wantWorktree string
		wantResolved bool
	}{
		{
			name:         "recorded worktree wins and the repo is left alone",
			repoPath:     usable,
			worktreePath: other,
			fallbacks:    []string{missing},
			wantRepo:     usable,
			wantWorktree: other,
			wantResolved: true,
		},
		{
			// The branch the deleted pre-lifecycle test owned: the worktree is
			// gone but the recorded repo is not, so the repo becomes both.
			name:         "recorded repo stands in for a missing worktree",
			repoPath:     usable,
			worktreePath: missing,
			fallbacks:    nil,
			wantRepo:     usable,
			wantWorktree: usable,
			wantResolved: true,
		},
		{
			// Repo unusable too, so the fallback supplies the worktree and is
			// also promoted into the repo slot — an unusable RepoPath must not
			// survive onto the launch context.
			name:         "fallback supplies both when the recorded paths are gone",
			repoPath:     missing,
			worktreePath: missing,
			fallbacks:    []string{"", missing, usable},
			wantRepo:     usable,
			wantWorktree: usable,
			wantResolved: true,
		},
		{
			// Nothing usable anywhere: the recorded paths are returned unchanged
			// so the caller can report its own refusal rather than launch into a
			// directory it invented.
			name:         "no candidate resolves",
			repoPath:     missing,
			worktreePath: missing,
			fallbacks:    []string{missing},
			wantRepo:     missing,
			wantWorktree: missing,
			wantResolved: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repoPath, worktreePath, ok := flowRepairLaunchPaths(tt.repoPath, tt.worktreePath, tt.fallbacks...)
			if ok != tt.wantResolved {
				t.Fatalf("flowRepairLaunchPaths() resolved = %v, want %v", ok, tt.wantResolved)
			}
			if repoPath != tt.wantRepo || worktreePath != tt.wantWorktree {
				t.Fatalf("flowRepairLaunchPaths() = repo %q worktree %q, want repo %q worktree %q",
					repoPath, worktreePath, tt.wantRepo, tt.wantWorktree)
			}
		})
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
			// The prompt renders the obstruction phase's title, and only the
			// reserved record carries this one. Asserting on the shared blocked
			// status instead would pass whichever record the prompt came from,
			// which is the one thing this test exists to distinguish.
			reserved.Phases[0].Title = "Reserved phase heading"
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
			if !strings.Contains(ctx.InitialPrompt, reserved.Phases[0].Title) {
				t.Fatalf("repair prompt was rendered from the cached record, not the reserved one:\n%s", ctx.InitialPrompt)
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

// Repair failures are never repo-gated. The tracked kinds report a
// nothing-persisted failure through ActionFailedMsg, which main shows only when
// the failing repo is selected or Active Flows is open; repair reports with a
// bare status instead, exactly as the pre-lifecycle path did. A user who moves
// the repos-pane selection during the asynchronous reservation hop must still
// be told why the R they pressed did nothing.
//
// Both stages that can refuse after admission are covered, because they take
// different branches of failFlowLaunch: prepare is the one that used to be
// gated, install is the backstop.
func TestFlowRepairFailuresAreReportedForAnUnselectedRepo(t *testing.T) {
	for _, tt := range []struct {
		name             string
		setUp            func(h *manualLaunchHarness)
		occupy           func(Model) Model
		want             string
		wantReservations int
	}{
		{
			name: "reservation error",
			setUp: func(h *manualLaunchHarness) {
				h.reserveRepairErr = errors.New("cannot reserve repair because flow is closed")
			},
			want: "Reserve persisted Flow for repair: cannot reserve repair because flow is closed",
		},
		{
			// The post-reservation revalidation, which is repair's last check
			// and the one that closes the read → reserve race. The reserved
			// record's phase is launchable again, so the classifier withdraws
			// repairability after the read stage already allowed it.
			name: "post-reservation revalidation",
			setUp: func(h *manualLaunchHarness) {
				reserved := h.record
				reserved.Phases = append([]flowstore.FlowPhase(nil), h.record.Phases...)
				reserved.Phases[0].Status = flowstore.PhaseReady
				h.repairReserved, h.repairReservedOK = reserved, true
			},
			want:             flowRepairNotRepairableStatus,
			wantReservations: 1,
		},
		{
			// A reservation that answers with a record for some other Flow is
			// refused rather than launched against, because the prepare stage
			// rebuilds the whole context from it.
			name: "reservation returns a foreign record",
			setUp: func(h *manualLaunchHarness) {
				reserved := h.record
				reserved.FlowID = "flow-other"
				h.repairReserved, h.repairReservedOK = reserved, true
			},
			want:             flowRepairNotRepairableStatus,
			wantReservations: 1,
		},
		{
			// The install stage. Capacity is the one install-stage refusal
			// reachable end to end — the backstop beside it is unreachable by
			// admission — and it lands after the reservation has been taken and
			// released, which is the furthest a repair can get and still refuse.
			name: "capacity at the install stage",
			occupy: func(m Model) Model {
				for i := 1; i <= 9; i++ {
					m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
						Number:   i,
						Scope:    embeddedTerminalScopeSession,
						Terminal: flowPhaseLaunchTestTerminal{state: "running"},
					})
				}
				return m
			},
			want:             "Maximum embedded terminals reached",
			wantReservations: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := repairLaunchFlowRecord(t)
			h := newManualLaunchHarness(t, record)
			if tt.setUp != nil {
				tt.setUp(h)
			}
			// The record's own repo is present but not selected, and Active
			// Flows is closed, so ActionFailedMsg would show nothing here.
			m := h.modelWith([]scanner.Repo{{Path: "/dev/elsewhere"}, {Path: record.RepoPath}}, h.options())
			if tt.occupy != nil {
				m = tt.occupy(m)
			}

			m = h.repair(m)

			if m.status.Text != tt.want {
				t.Fatalf("status = %q, want %q", m.status.Text, tt.want)
			}
			if len(h.launchContexts) != 0 {
				t.Fatalf("a refused repair opened %#v", h.launchContexts)
			}
			if len(h.phaseUpdates) != 0 || len(h.launchUpdates) != 0 {
				t.Fatalf("a refused repair persisted phases=%#v launches=%#v", h.phaseUpdates, h.launchUpdates)
			}
			if _, held := m.flowLaunchAttempt(record.FlowID); held {
				t.Fatal("a refused repair stranded its attempt")
			}
			if h.repairReservations != tt.wantReservations || h.repairReleases != tt.wantReservations {
				t.Fatalf("repair reservations = %d, releases = %d, want %d held then released",
					h.repairReservations, h.repairReleases, tt.wantReservations)
			}
		})
	}
}

// The install-stage backstop, which admission makes unreachable in production.
// Repair's rung is broader than every other kind's: it refuses a non-repair
// Flow terminal too, because repair is Flow-scoped and any terminal on the Flow
// is a competing owner of the same record. That breadth is what the pre-
// lifecycle consume step checked immediately before allocating, so losing it
// here would narrow repair's last line of defense against a future unguarded
// launch source.
func TestFlowRepairInstallBackstopRefusesAnyFlowTerminal(t *testing.T) {
	for _, tt := range []struct {
		name string
		slot embeddedTerminalSlot
	}{
		{
			name: "repair terminal",
			slot: embeddedTerminalSlot{FlowRepair: true},
		},
		{
			// The broad half. hasFlowRepairEmbeddedTerminalForFlow does not
			// match this slot, so only the added disjunct refuses it.
			name: "phase terminal",
			slot: embeddedTerminalSlot{FlowPhaseID: "implementation"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := repairLaunchFlowRecord(t)
			slot := tt.slot
			slot.Scope = embeddedTerminalScopeFlow
			slot.FlowID = record.FlowID
			slot.Terminal = flowPhaseLaunchTestTerminal{state: "running"}

			attempt := flowLaunchAttempt{Token: "token-1", Kind: flowLaunchKindRepair, FlowID: record.FlowID}
			m := Model{embeddedTerminals: []embeddedTerminalSlot{slot}}

			canceled, blocked := m.flowLaunchEmbeddedBackstop(attempt.Kind, record.FlowID)
			if !blocked {
				t.Fatal("a repair must never install beside another terminal on the same Flow")
			}
			if canceled != flowRepairTerminalStatus {
				t.Fatalf("backstop refusal = %q, want repair's own terminal wording %q", canceled, flowRepairTerminalStatus)
			}
		})
	}

	// The other kinds keep their narrower rung: a non-repair Flow terminal is
	// their ordinary one-per-Flow case, refused at admission, not here.
	record := repairLaunchFlowRecord(t)
	m := Model{embeddedTerminals: []embeddedTerminalSlot{{
		Scope:       embeddedTerminalScopeFlow,
		FlowID:      record.FlowID,
		FlowPhaseID: "implementation",
		Terminal:    flowPhaseLaunchTestTerminal{state: "running"},
	}}}
	for _, kind := range []flowLaunchKind{flowLaunchKindManualPhase, flowLaunchKindAutoPhase, flowLaunchKindPhaseResume} {
		if _, blocked := m.flowLaunchEmbeddedBackstop(kind, record.FlowID); blocked {
			t.Fatalf("kind %v must not inherit repair's broad backstop", kind)
		}
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

// The surface gate is the term neither cachedFlowRecord nor selectedFlow
// carries, so dropping it would arm R from panes that show no Flow. The wider
// invariant — that the footer and admission agree on every occupancy signal —
// is pinned by the wantFooter column of TestFlowRepairAdmissionRefusals, which
// exercises both predicates against the same Model.
func TestSelectedFlowRepairReadyRequiresAVisibleFlowSurface(t *testing.T) {
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
