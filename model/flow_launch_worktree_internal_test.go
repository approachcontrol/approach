package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// worktreelessFlowRecord is manualLaunchFlowRecord without the worktree every
// other launch test takes for granted — the routine product of
// `approach flow create` with no --worktree-path.
func worktreelessFlowRecord() flowstore.FlowRecord {
	record := manualLaunchFlowRecord()
	record.WorktreePath = ""
	record.Branch = ""
	record.Commit = ""
	record.BaseRef = "main"
	return record
}

func TestManualFlowLaunchCreatesTheMissingWorktreeAndUsesIt(t *testing.T) {
	record := worktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)

	m := h.launch(h.model())

	if len(h.ensureRecords) != 1 || h.ensureRecords[0].FlowID != record.FlowID {
		t.Fatalf("ensure calls = %#v, want exactly one for this Flow", h.ensureRecords)
	}
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
	}
	ctx := h.launchContexts[0]
	if ctx.WorktreePath != "/dev/alpha-worktrees/flow-one" {
		t.Fatalf("launch worktree = %q, want the created worktree and never the repo root", ctx.WorktreePath)
	}
	if ctx.Branch != "flow/flow-one" || ctx.Commit != "abc123" {
		t.Fatalf("launch context = %#v, want the ensured branch and commit", ctx)
	}
	if !strings.Contains(m.status.Text, "Set up worktree /dev/alpha-worktrees/flow-one on branch flow/flow-one") {
		t.Fatalf("status = %q, want the creation announced", m.status.Text)
	}
}

func TestManualFlowLaunchLeavesAWorktreedFlowUntouched(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)

	m := h.launch(h.model())

	if len(h.ensureRecords) != 0 {
		t.Fatalf("ensure calls = %#v, want none for a Flow that already has a worktree", h.ensureRecords)
	}
	if len(h.launchContexts) != 1 || h.launchContexts[0].WorktreePath != record.WorktreePath {
		t.Fatalf("launch contexts = %#v, want the record's own worktree", h.launchContexts)
	}
	if strings.Contains(m.status.Text, "Set up worktree") {
		t.Fatalf("status = %q, want no creation announcement", m.status.Text)
	}
}

// A keypress that is refused must not mutate Flow state as a side effect.
func TestManualFlowLaunchWorktreeFailureRefusesWithoutWriting(t *testing.T) {
	record := worktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = errors.New("branch already checked out")

	m := h.launch(h.model())

	if m.status.Text != "Worktree creation failed: branch already checked out" {
		t.Fatalf("status = %q, want the wrapped creation failure", m.status.Text)
	}
	if len(h.phaseUpdates) != 0 || len(h.launchUpdates) != 0 {
		t.Fatalf("a refused manual launch persisted something: phases=%#v launches=%#v", h.phaseUpdates, h.launchUpdates)
	}
	if len(h.launchContexts) != 0 || len(h.agentContexts) != 0 {
		t.Fatal("a refused manual launch must not start an agent")
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("a refused manual launch must release its attempt")
	}
}

// A bootstrap-hook failure persists the worktree before it reports, so the
// refusal must not claim creation failed — the directory is there — and the
// Flows pane must stop rendering the record as worktree-less.
func TestManualFlowLaunchReportsAWorktreeCreatedBeforeTheRefusal(t *testing.T) {
	record := worktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	ensured := record
	ensured.WorktreePath = "/dev/alpha-worktrees/flow-one"
	ensured.Branch = "flow/flow-one"
	h.ensured = ensured
	h.ensureErr = errors.New("bootstrap hook failed: missing env file")

	m := h.launch(h.model())

	if m.status.Text != "Worktree created but the launch was refused: bootstrap hook failed: missing env file" {
		t.Fatalf("status = %q, want the created-but-refused wording", m.status.Text)
	}
	if len(h.launchUpdates) != 0 || len(h.launchContexts) != 0 || len(h.agentContexts) != 0 {
		t.Fatal("a refused manual launch must not start an agent or record a launch")
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("a refused manual launch must release its attempt")
	}
}

// The store write is the one refusal where creation is precisely what did not
// fail, so it gets its own headline and names the directory left behind.
func TestManualFlowLaunchReportsAnUnrecordedWorktreeSeparately(t *testing.T) {
	record := worktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = fmt.Errorf("%w: /dev/alpha-worktrees/flow-one: %w", ErrFlowWorktreeUnrecorded, errors.New("flow store locked"))

	m := h.launch(h.model())

	want := "Worktree could not be recorded: worktree not recorded: " +
		"/dev/alpha-worktrees/flow-one: flow store locked"
	if m.status.Text != want {
		t.Fatalf("status = %q, want %q", m.status.Text, want)
	}
	if len(h.phaseUpdates) != 0 || len(h.launchUpdates) != 0 {
		t.Fatalf("a refused manual launch persisted something: phases=%#v launches=%#v", h.phaseUpdates, h.launchUpdates)
	}
}

// The ensure step is the only filesystem work in preparation, so it runs last:
// a phase a live session already owns must never cost a `git worktree add`.
func TestManualFlowLaunchSkipsEnsureWhenASessionOwnsThePhase(t *testing.T) {
	record := worktreelessFlowRecord()
	record.Phases[0].LaunchIDs = []string{"launch-live"}
	h := newManualLaunchHarness(t, record)
	h.sessionRecords = []sessions.SessionRecord{{
		SessionID: "session-live",
		LaunchID:  "launch-live",
		FlowID:    record.FlowID,
		Status:    "running",
	}}

	m := h.launch(h.model())

	if len(h.ensureRecords) != 0 {
		t.Fatalf("ensure calls = %#v, want none when a live session already owns the phase", h.ensureRecords)
	}
	want := flowLaunchPhaseSessionLiveStatus(record.Phases[0].PhaseID)
	if m.status.Text != want {
		t.Fatalf("status = %q, want %q", m.status.Text, want)
	}
}

func TestManualFlowLaunchAnnouncesTheWorktreeOnEveryRoute(t *testing.T) {
	const note = "Set up worktree /dev/alpha-worktrees/flow-one on branch flow/flow-one for this Flow"
	for _, tt := range []struct {
		name          string
		agentCommand  string
		launchBackend string
		tmuxAvailable bool
		want          string
	}{
		{
			name:         "embedded",
			agentCommand: "codex",
			want:         note,
		},
		{
			name:         "external",
			agentCommand: "codex-app",
			want:         "Launched codex-app in a terminal session (" + note + ")",
		},
		{
			name:          "tmux",
			agentCommand:  "codex",
			launchBackend: "tmux",
			tmuxAvailable: true,
			want:          "Launched implementation-abcd1234 in tmux session approach-alpha-0001 — tmux attach -t 'approach-alpha-0001' (" + note + ")",
		},
		{
			name:          "embedded with a tmux fallback",
			agentCommand:  "codex",
			launchBackend: "tmux",
			want:          note + " (" + tmuxFallbackNote + ")",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := worktreelessFlowRecord()
			h := newManualLaunchHarness(t, record)
			h.agentCommand = tt.agentCommand
			h.launchBackend = tt.launchBackend
			h.tmuxAvailable = tt.tmuxAvailable

			m := h.launch(h.model())

			if m.status.Text != tt.want {
				t.Fatalf("status = %q, want %q", m.status.Text, tt.want)
			}
		})
	}
}

func autoWorktreelessFlowRecord() flowstore.FlowRecord {
	record := worktreelessFlowRecord()
	record.AutoMode = true
	return record
}

func TestAutoFlowLaunchCreatesTheMissingWorktreeAndLaunches(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)

	m := h.autoDrain(h.model(), record)

	if len(h.ensureRecords) != 1 {
		t.Fatalf("ensure calls = %#v, want exactly one", h.ensureRecords)
	}
	if len(h.launchContexts) != 1 || h.launchContexts[0].WorktreePath != "/dev/alpha-worktrees/flow-one" {
		t.Fatalf("launch contexts = %#v, want the created worktree", h.launchContexts)
	}
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("a successful auto launch must leave the drain disarmed")
	}
}

// AutoMode has nobody to read a status, so a permanent refusal blocks the phase
// instead of retrying at 1 Hz forever.
func TestAutoFlowLaunchBlocksThePhaseWhenTheWorktreeCannotBeCreated(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = errors.New("branch already checked out")

	m := h.autoDrain(h.model(), record)

	if len(h.phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want exactly one blocked write", h.phaseUpdates)
	}
	update := h.phaseUpdates[0]
	if update.FlowID != record.FlowID ||
		update.PhaseID != "implementation" ||
		update.Status != flowstore.PhaseBlocked ||
		update.Notes != "Auto-advance blocked: Worktree creation failed: branch already checked out" {
		t.Fatalf("phase update = %#v", update)
	}
	if update.Outcome != "" {
		t.Fatalf("phase update outcome = %q, want it scoped to plan review", update.Outcome)
	}
	if len(h.launchUpdates) != 0 {
		t.Fatalf("a blocked launch persisted a launch ID: %#v", h.launchUpdates)
	}
	// Without the synthesized launch context this fence misses and the Flow is
	// held in failurePersisting forever — no manual launch, no AutoMode, no repair.
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("the blocked write must release the attempt once it lands")
	}
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("a blocked phase must not re-arm the drain")
	}
	if m.status.Text != "Worktree creation failed: branch already checked out" {
		t.Fatalf("status = %q, want the refusal reported once", m.status.Text)
	}

	// The blocked phase is what makes the refusal permanent: even an armed drain
	// finds nothing to launch, so there is no second `git worktree add`.
	blocked := record
	blocked.Phases = []flowstore.FlowPhase{{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseBlocked,
		Order:   1,
	}}
	m = h.autoDrain(m, blocked)
	if len(h.ensureRecords) != 1 {
		t.Fatalf("ensure calls = %d, want the refusal to be permanent", len(h.ensureRecords))
	}
}

func TestAutoFlowLaunchBlockedWriteTargetsTheResolvedPhaseID(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = errors.New("branch already checked out")
	m := h.model()

	// The intent carries the spelling an earlier poll captured; the record's own
	// spelling is what the store accepts.
	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
		Kind:      flowLaunchKindAutoPhase,
		FlowID:    record.FlowID,
		PhaseID:   " Implementation ",
		FlowTitle: record.Title,
		Origin:    flowLaunchOriginAutoMode,
	})
	if !admitted {
		t.Fatal("AutoMode should have been admitted")
	}
	m = h.drain(next, cmd, 0)

	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].PhaseID != "implementation" {
		t.Fatalf("phase updates = %#v, want the record's own phase spelling", h.phaseUpdates)
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("the blocked write must release the attempt")
	}
}

func TestAutoFlowLaunchBlocksPlanReviewWithTheBlockedOutcome(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	record.PlanID = "plan-1"
	record.PlanPath = "/state/approach/plans/plan-1/plan.md"
	record.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Title: "Plan review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 1},
	}
	h := newManualLaunchHarness(t, record)
	h.ensureErr = errors.New("branch already checked out")

	h.autoDrain(h.model(), record)

	if len(h.phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want exactly one", h.phaseUpdates)
	}
	if h.phaseUpdates[0].Outcome != flowstore.OutcomeBlocked {
		t.Fatalf("phase update = %#v, want the plan-review blocked outcome", h.phaseUpdates[0])
	}
}

// The reservation is held across a bootstrap hook whose budget is 120 s by
// default, against the store's 5 s lock timeout, so the launch that loses it is
// usually waiting on another one provisioning this very Flow. Blocking the
// phase for losing that wait would answer a wait with a stop — and nothing was
// created, so there is no orphan to argue against coming back.
func TestAutoFlowLaunchRetriesAContendedReservationInsteadOfBlocking(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = fmt.Errorf("%w: %w", ErrFlowWorktreeUnreserved, errors.New("acquire launch/close reservation for flow \"flow-one\": timeout"))

	m := h.autoDrain(h.model(), record)

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want a contended reservation to write nothing", h.phaseUpdates)
	}
	if !h.drainArmed(m, record.FlowID) {
		t.Fatal("a contended reservation must re-arm the drain so the launch resumes")
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("a retried launch must release its attempt")
	}
}

// A Flow closed while this launch was reading has no candidate left: nothing to
// retry and nothing to write about it.
func TestAutoFlowLaunchTreatsAClosedFlowReservationAsStale(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = fmt.Errorf("%w: %w", ErrFlowWorktreeUnreserved,
		fmt.Errorf("cannot launch an agent for flow %q because it is closed: %w", record.FlowID, flowstore.ErrFlowClosed))

	m := h.autoDrain(h.model(), record)

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want a closed Flow to block nothing", h.phaseUpdates)
	}
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("a closed Flow must not re-arm the drain")
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("a stale launch must release its attempt")
	}
}

// The manual route has a user to read the reason, so it reports the wait rather
// than reporting it as a creation that failed.
func TestManualFlowLaunchReportsAContendedReservationAsAWait(t *testing.T) {
	record := worktreelessFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.ensureErr = fmt.Errorf("%w: %w", ErrFlowWorktreeUnreserved, errors.New("lock timeout"))

	m := h.launch(h.model())

	if !strings.HasPrefix(m.status.Text, "Another launch is setting up this Flow's worktree: ") {
		t.Fatalf("status = %q, want the wait named rather than a creation failure", m.status.Text)
	}
	if len(h.phaseUpdates) != 0 || len(h.launchUpdates) != 0 {
		t.Fatalf("a refused manual launch persisted something: phases=%#v launches=%#v", h.phaseUpdates, h.launchUpdates)
	}
	if len(h.agentContexts) != 0 {
		t.Fatal("a refused manual launch must not start an agent")
	}
}

func TestAutoFlowLaunchSkipsEnsureWhenASessionOwnsThePhase(t *testing.T) {
	record := autoWorktreelessFlowRecord()
	record.Phases[0].LaunchIDs = []string{"launch-live"}
	h := newManualLaunchHarness(t, record)
	h.sessionRecords = []sessions.SessionRecord{{
		SessionID: "session-live",
		LaunchID:  "launch-live",
		FlowID:    record.FlowID,
		Status:    "running",
	}}
	m := h.model()

	next, cmd := h.autoPoll(m, record)
	if cmd == nil {
		t.Fatal("AutoMode should have been admitted")
	}
	event, ok := runCommandWithoutWaiting(cmd)
	if !ok {
		t.Fatal("the read hop produced no message")
	}
	read, ok := event.(flowLaunchEventMsg)
	if !ok {
		t.Fatalf("read hop message = %T, want a launch event", event)
	}
	if read.Outcome != flowLaunchOutcomeRetry {
		t.Fatalf("outcome = %v, want retry so the drain re-arms", read.Outcome)
	}
	if len(h.ensureRecords) != 0 {
		t.Fatalf("ensure calls = %#v, want none behind the session check", h.ensureRecords)
	}
	_ = next
}
