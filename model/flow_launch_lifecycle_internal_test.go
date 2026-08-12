package model

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// These tests characterize the manual Flow phase launch path. Every invariant
// asserted here holds both before and after the launch lifecycle module takes
// the path over, so the module can only move policy, never change it.

type manualLaunchHarness struct {
	t *testing.T

	record flowstore.FlowRecord

	launchContexts []actions.AgentLaunchContext
	phaseUpdates   []flowstore.PhaseUpdate
	launchUpdates  []flowstore.PhaseLaunchUpdate
	agentContexts  []actions.AgentLaunchContext

	startTerminalErr error
	setPhaseErr      error
	addLaunchIDErr   error
	planBodyErr      error
	launchAgentErr   error
	reserveLaunchErr error

	agentCommand   string
	persistedFlows []flowstore.FlowRecord
	readErr        error
	sessionRecords []sessions.SessionRecord
	sessionsErr    error

	sessionListCalls   int
	launchReservations int
	launchReleases     int

	persistedRecord   flowstore.FlowRecord
	persistedRecordOK bool
}

// persistedFlow is what the authoritative read returns. It defaults to the
// record the pane holds so a test only describes the divergence it cares about.
func (h *manualLaunchHarness) persistedFlow(flowID string) (flowstore.FlowRecord, error) {
	if h.readErr != nil {
		return flowstore.FlowRecord{}, h.readErr
	}
	for _, record := range h.persistedFlows {
		if record.FlowID == flowID {
			return record, nil
		}
	}
	if flowID == h.record.FlowID {
		return h.record, nil
	}
	// Wrapped like the store's own miss so callers that distinguish a missing
	// Flow from a failed read see the same thing here.
	return flowstore.FlowRecord{}, fmt.Errorf("flow %q not found: %w", flowID, flowstore.ErrFlowNotFound)
}

func newManualLaunchHarness(t *testing.T, record flowstore.FlowRecord) *manualLaunchHarness {
	t.Helper()
	return &manualLaunchHarness{t: t, record: record}
}

func (h *manualLaunchHarness) options() Options {
	command := h.agentCommand
	if command == "" {
		command = "codex"
	}
	return Options{
		AgentCommand: command,
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{h.record}, nil
		},
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			// Counted because the real seam lists the entire session store and
			// filters client-side, so "how often is this reached" is a
			// behavioural property, not an implementation detail.
			h.sessionListCalls++
			if h.sessionsErr != nil {
				return nil, h.sessionsErr
			}
			return h.sessionRecords, nil
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return h.persistedFlow(strings.TrimSpace(flowID))
		},
		ReserveFlowLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			if h.reserveLaunchErr != nil {
				return flowstore.FlowRecord{}, nil, h.reserveLaunchErr
			}
			record, err := h.persistedFlow(strings.TrimSpace(flowID))
			if err == nil && flowstore.FlowClosed(record) {
				// The real reservation refuses a closed Flow, and callers tell
				// that refusal apart from a missing record by the sentinel.
				return flowstore.FlowRecord{}, nil, fmt.Errorf(
					"cannot launch an agent for flow %q because it is closed: %w", record.FlowID, flowstore.ErrFlowClosed)
			}
			h.launchReservations++
			return record, func() { h.launchReleases++ }, nil
		},
		ReadPlan: func(planID string) (string, error) {
			if h.planBodyErr != nil {
				return "", h.planBodyErr
			}
			return "plan body", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			h.agentContexts = append(h.agentContexts, ctx)
			if h.launchAgentErr != nil {
				return actions.TerminalLaunchSpec{}, h.launchAgentErr
			}
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			h.launchUpdates = append(h.launchUpdates, update)
			if h.addLaunchIDErr != nil {
				return flowstore.FlowRecord{}, h.addLaunchIDErr
			}
			if h.persistedRecordOK {
				return h.persistedRecord, nil
			}
			updated := h.record
			updated.UpdatedAt = time.Now()
			phases := make([]flowstore.FlowPhase, len(updated.Phases))
			copy(phases, updated.Phases)
			for i := range phases {
				if phases[i].PhaseID == update.PhaseID {
					phases[i].Status = flowstore.PhaseRunning
					phases[i].LaunchIDs = append(append([]string{}, phases[i].LaunchIDs...), update.LaunchID)
				}
			}
			updated.Phases = phases
			return updated, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			h.phaseUpdates = append(h.phaseUpdates, update)
			if h.setPhaseErr != nil {
				return flowstore.FlowRecord{}, h.setPhaseErr
			}
			return h.record, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
			h.launchContexts = append(h.launchContexts, ctx)
			if h.startTerminalErr != nil {
				return nil, h.startTerminalErr
			}
			return flowPhaseLaunchTestTerminal{state: "running"}, nil
		},
	}
}

func (h *manualLaunchHarness) model() Model {
	h.t.Helper()
	return h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, h.options())
}

func (h *manualLaunchHarness) modelWith(repos []scanner.Repo, opts Options) Model {
	h.t.Helper()
	m := NewWithOptions(repos, opts)
	m.contentPane = ui.PaneBottom
	m.bottomMode = ui.ModeFlows
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{h.record})
	return m
}

// launch presses g and drains the resulting command chain, feeding every
// message back through Update the way the bubbletea runtime does. The chain is
// deliberately message-shape agnostic so the same driver works before and after
// the lifecycle refactor.
func (h *manualLaunchHarness) launch(m Model) Model {
	h.t.Helper()
	next, cmd := m.handleLaunchNextFlowPhase()
	m = next.(Model)
	return h.drain(m, cmd, 0)
}

func (h *manualLaunchHarness) drain(m Model, cmd tea.Cmd, depth int) Model {
	h.t.Helper()
	if cmd == nil {
		return m
	}
	if depth > 16 {
		h.t.Fatal("launch command chain did not settle")
	}
	msg, ok := runCommandWithoutWaiting(cmd)
	if !ok {
		// Timer commands (terminal repaint, Flow refresh tick) only resume the
		// same chain later; the launch itself never depends on them.
		return m
	}
	return h.drainMsg(m, msg, depth)
}

func runCommandWithoutWaiting(cmd tea.Cmd) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(250 * time.Millisecond):
		return nil, false
	}
}

func (h *manualLaunchHarness) drainMsg(m Model, msg tea.Msg, depth int) Model {
	h.t.Helper()
	if msg == nil {
		return m
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, batched := range msg {
			m = h.drain(m, batched, depth+1)
		}
		return m
	case embeddedTerminalTickMsg, flowRefreshTickMsg, autoAdvanceTickMsg:
		// Periodic repaint and refresh ticks re-arm themselves forever and are
		// not part of the launch chain.
		return m
	}
	next, cmd := m.Update(msg)
	m = next.(Model)
	return h.drain(m, cmd, depth+1)
}

func manualLaunchFlowRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 1},
		},
	}
}

// --- AutoMode characterization ---
//
// These pin the AutoMode behaviour the lifecycle has to preserve when it takes
// the path over. They are written against the drain as it exists today and must
// survive the migration unchanged.

func autoLaunchFlowRecord() flowstore.FlowRecord {
	record := manualLaunchFlowRecord()
	record.AutoMode = true
	return record
}

// autoDrain arms the drain for this record and runs one advance poll, draining
// every command the poll returns. Drain state is only meaningful once those
// commands settle: the drain is disarmed at admission and re-armed when the
// asynchronous failure lands, so an assertion taken before then reads a state
// no user ever observes.
func (h *manualLaunchHarness) autoDrain(m Model, record flowstore.FlowRecord) Model {
	h.t.Helper()
	m = m.armAutoAdvanceDrain(record.FlowID)
	next, cmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{record})
	return h.drain(next, cmd, 0)
}

func (h *manualLaunchHarness) drainArmed(m Model, flowID string) bool {
	h.t.Helper()
	_, ok := m.autoAdvanceDrainFlows[flowID]
	return ok
}

func TestAutoFlowLaunchIsHeadlessAndLeavesFocusAlone(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored_headless_%v", stored), func(t *testing.T) {
			record := autoLaunchFlowRecord()
			record.Headless = stored
			h := newManualLaunchHarness(t, record)
			m := h.model()
			m.terminalFocus = terminalFocusList

			m = h.autoDrain(m, record)

			if len(h.launchContexts) != 1 {
				t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
			}
			ctx := h.launchContexts[0]
			if !ctx.Headless {
				t.Fatalf("ctx.Headless = false, want AutoMode to ignore the persisted %v", stored)
			}
			if !ctx.FlowAutoLaunch {
				t.Fatalf("ctx = %#v, want FlowAutoLaunch", ctx)
			}
			if len(h.launchUpdates) != 1 || !h.launchUpdates[0].AutoLaunch {
				t.Fatalf("launch updates = %#v, want one AutoLaunch update", h.launchUpdates)
			}
			if m.terminalFocus != terminalFocusList {
				t.Fatalf("terminal focus = %v, want AutoMode to leave the list focused", m.terminalFocus)
			}
		})
	}
}

func TestAutoFlowLaunchSkipsOutdatedAutoLaunch(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.addLaunchIDErr = fmt.Errorf("auto launch target %q is not eligible: %w", "implementation", flowstore.ErrAutoLaunchOutdated)

	m := h.autoDrain(h.model(), record)

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("an outdated auto launch must not write the phase: %#v", h.phaseUpdates)
	}
	if len(h.launchContexts) != 0 {
		t.Fatal("an outdated auto launch must not open a terminal")
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("an outdated auto launch must release its reservation")
	}
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("an outdated auto launch must not re-arm the drain")
	}
	// The queued transient may already have fired — preflight did pass — but an
	// outdated launch is an intentional no-op and must never report a failure.
	if m.status.Source == statusOther {
		t.Fatalf("status = %#v, want no sticky failure status", m.status)
	}
}

func TestAutoFlowLaunchPreflightFailuresUseTransientAutoStatus(t *testing.T) {
	planReview := autoLaunchFlowRecord()
	planReview.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Title: "Plan review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 1},
	}

	planPathFailure := autoLaunchFlowRecord()
	planPathFailure.PlanID = "plan-1"

	pathless := autoLaunchFlowRecord()
	pathless.RepoPath = ""
	pathless.WorktreePath = ""

	tests := []struct {
		name         string
		record       flowstore.FlowRecord
		repoless     bool
		agentCommand string
		planPath     func(string) (string, error)
		want         string
	}{
		{
			name:         "missing agent command",
			record:       autoLaunchFlowRecord(),
			agentCommand: " ",
			want:         "Press A to choose " + ui.AgentInputPlaceholder + " before launching an agent",
		},
		{
			name:     "undeterminable launch path",
			record:   pathless,
			repoless: true,
			want:     "Cannot determine launch path for this flow",
		},
		{
			name:     "unresolvable plan path",
			record:   planPathFailure,
			planPath: func(string) (string, error) { return "", errors.New("plan path unavailable") },
			want:     "plan path unavailable",
		},
		{
			name:   "plan review without a linked plan",
			record: planReview,
			want:   "Plan Review needs a linked plan before launch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, tc.record)
			opts := h.options()
			if tc.agentCommand != "" {
				opts.AgentCommand = strings.TrimSpace(tc.agentCommand)
			}
			if tc.planPath != nil {
				opts.PlanMarkdownPath = tc.planPath
			}
			repos := []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}
			if tc.repoless {
				repos = nil
			}
			m := h.modelWith(repos, opts)

			m = h.autoDrain(m, tc.record)

			wantStatus := "Flow " + flowTitleForStatus(tc.record) + ": " + tc.want
			if m.status.Text != wantStatus {
				t.Fatalf("status = %q, want %q", m.status.Text, wantStatus)
			}
			if m.status.Source != statusFlowAutoAdvance {
				t.Fatalf("status source = %v, want the transient AutoMode source", m.status.Source)
			}
			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatalf("a preflight failure persisted something: launches=%#v phases=%#v", h.launchUpdates, h.phaseUpdates)
			}
			if !h.drainArmed(m, tc.record.FlowID) {
				t.Fatal("a preflight failure must leave the drain armed for the next poll")
			}
			if _, ok := m.flowLaunchAttempt(tc.record.FlowID); ok {
				t.Fatal("a preflight failure must not hold a reservation")
			}
		})
	}
}

func TestAutoFlowLaunchFailureYieldsToDisplayStatus(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	opts := h.options()
	opts.AgentCommand = ""
	m := h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
	m = m.setStatus(statusOther, "worktree fetch failed")

	m = h.autoDrain(m, record)

	if m.status.Text != "worktree fetch failed" {
		t.Fatalf("status = %q, want the display status AutoMode may never stomp", m.status.Text)
	}
	if m.status.Source != statusOther {
		t.Fatalf("status source = %v, want statusOther", m.status.Source)
	}
}

func TestAutoFlowLaunchNeverLaunchesMergePhases(t *testing.T) {
	record := autoLaunchFlowRecord()
	record.PR = flowstore.PullRequest{
		Provider:   "github",
		Number:     7,
		URL:        "https://github.com/approachcontrol/approach/pull/7",
		HeadBranch: "flow/one",
		BaseBranch: "main",
	}
	record.Phases = []flowstore.FlowPhase{
		{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseCompleted, Order: 1},
		{PhaseID: "merge", Title: "Merge", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, Order: 2},
	}
	h := newManualLaunchHarness(t, record)

	m := h.autoDrain(h.model(), record)

	if len(h.launchUpdates) != 0 || len(h.launchContexts) != 0 {
		t.Fatalf("AutoMode launched a merge phase: updates=%#v contexts=%#v", h.launchUpdates, h.launchContexts)
	}
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("a Flow with nothing auto-launchable must disarm its drain")
	}
	// The same phase is still a manual launch target, which is what makes the
	// AutoMode-only refusal a real distinction rather than an unreachable one.
	if _, _, ok := m.previewFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: record.FlowID}); !ok {
		t.Fatal("a ready merge phase should still be manually launchable")
	}
}

// TestAutoFlowLaunchNeverLaunchesAutoreviewRecovery covers the second half of
// flowLaunchCandidatePhase's manual-only rule. Autoreview recovery is the one
// launch target that is not `ready`: a needs_attention or blocked autoreview
// phase on a Flow with a PR target. Re-running it costs a review cycle and the
// human asked for attention, so AutoMode must leave it alone even though the
// phase satisfies flowPhaseCanLaunch.
func TestAutoFlowLaunchNeverLaunchesAutoreviewRecovery(t *testing.T) {
	for _, status := range []string{flowstore.PhaseNeedsAttention, flowstore.PhaseBlocked} {
		t.Run(status, func(t *testing.T) {
			record := autoLaunchFlowRecord()
			record.PR = flowstore.PullRequest{
				Provider:   "github",
				Number:     7,
				URL:        "https://github.com/approachcontrol/approach/pull/7",
				HeadBranch: "flow/one",
				BaseBranch: "main",
			}
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "pr-creation", Title: "PR creation", Kind: flowstore.KindPRCreation, Status: flowstore.PhaseCompleted, Order: 1},
				{PhaseID: "autoreview", Title: "Autoreview", Kind: flowstore.KindAutoreview, Status: status, Order: 2, DependsOn: []string{"pr-creation"}},
			}
			h := newManualLaunchHarness(t, record)

			m := h.autoDrain(h.model(), record)

			if len(h.launchUpdates) != 0 || len(h.launchContexts) != 0 || len(h.agentContexts) != 0 {
				t.Fatalf("AutoMode relaunched an autoreview phase in %s: updates=%#v embedded=%#v external=%#v",
					status, h.launchUpdates, h.launchContexts, h.agentContexts)
			}
			if h.drainArmed(m, record.FlowID) {
				t.Fatal("a Flow with nothing auto-launchable must disarm its drain")
			}
			// Naming the phase explicitly bypasses the drain's own first-ready
			// selection, so this is the resolver refusing rather than the
			// candidate never being reached.
			if _, ok := flowLaunchCandidatePhase(flowLaunchKindAutoPhase, record, "autoreview"); ok {
				t.Fatal("the auto resolver accepted an autoreview recovery target")
			}
			if _, _, ok := m.previewFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: record.FlowID}); !ok {
				t.Fatal("autoreview recovery should still be manually launchable")
			}
		})
	}
}

// TestAutoFlowLaunchSuccessReleasesOwnership pins the failure that would be
// worst and quietest: an auto launch that works but keeps its reservation. The
// Flow would then be permanently occupied for admission, so AutoMode and manual
// launch would both refuse it forever with no status and nothing to see in the
// UI. The manual kind is covered by TestFlowLaunchEmbeddedInstallTransfersOwnership
// and TestFlowLaunchExternalRouteRetainsOwnershipUntilResult; AutoMode reaches
// the same two handoffs through a different admission and read stage, so it
// needs its own pins.
func TestAutoFlowLaunchSuccessReleasesOwnership(t *testing.T) {
	t.Run("embedded route hands the Flow to the slot", func(t *testing.T) {
		record := autoLaunchFlowRecord()
		h := newManualLaunchHarness(t, record)

		m := h.autoDrain(h.model(), record)

		if len(h.launchContexts) != 1 {
			t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
		}
		if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
			t.Fatal("a successful auto launch must release its reservation")
		}
		if !m.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
			t.Fatal("the slot must own the Flow the instant the attempt is dropped")
		}
		if !m.flowLaunchAdmissionOccupied(record.FlowID) {
			t.Fatal("the Flow should stay occupied by the slot, not by a stranded attempt")
		}
		if h.drainArmed(m, record.FlowID) {
			t.Fatal("a successful auto launch must leave the drain disarmed")
		}
	})

	t.Run("external route releases on the agent result", func(t *testing.T) {
		record := autoLaunchFlowRecord()
		h := newManualLaunchHarness(t, record)
		h.agentCommand = "codex-app"

		m := h.autoDrain(h.model(), record)

		if len(h.agentContexts) != 1 {
			t.Fatalf("external launches = %d, want 1", len(h.agentContexts))
		}
		if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
			t.Fatal("the agent result must release the auto attempt; otherwise the Flow is unlaunchable forever")
		}
		if m.flowLaunchAdmissionOccupied(record.FlowID) {
			t.Fatal("a detached external auto launch leaves nothing owning the Flow")
		}
		if h.drainArmed(m, record.FlowID) {
			t.Fatal("a successful auto launch must leave the drain disarmed")
		}
	})
}

// --- AutoMode lifecycle matrix ---

// autoPoll arms the drain and runs one advance poll without draining what it
// returns, so a test can inspect the synchronous admission decision on its own.
func (h *manualLaunchHarness) autoPoll(m Model, record flowstore.FlowRecord) (Model, tea.Cmd) {
	h.t.Helper()
	m = m.armAutoAdvanceDrain(record.FlowID)
	return m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{record})
}

func autoLaunchGatedRecord() flowstore.FlowRecord {
	record := autoLaunchFlowRecord()
	record.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseCompleted, Order: 1, DependsOn: []string{}},
		{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 2, DependsOn: []string{"plan"}},
	}
	return record
}

func TestAutoFlowLaunchStaleCandidateIsDroppedNotRetried(t *testing.T) {
	tests := []struct {
		name    string
		record  flowstore.FlowRecord
		persist func(flowstore.FlowRecord) flowstore.FlowRecord
	}{
		{
			name:   "candidate completed between poll and read",
			record: autoLaunchFlowRecord(),
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Status = flowstore.PhaseCompleted
				return record
			},
		},
		{
			// Another source took the candidate. That source produces its own
			// completion or stop edge, which is what re-arms the drain.
			name:   "candidate started by another source",
			record: autoLaunchFlowRecord(),
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Status = flowstore.PhaseRunning
				return record
			},
		},
		{
			name:   "candidate gate reopened",
			record: autoLaunchGatedRecord(),
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Status = flowstore.PhaseRunning
				return record
			},
		},
		{
			name:   "auto mode turned off between poll and read",
			record: autoLaunchFlowRecord(),
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.AutoMode = false
				return record
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := tc.record
			persisted := record
			persisted.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
			persisted = tc.persist(persisted)

			h := newManualLaunchHarness(t, record)
			h.persistedFlows = []flowstore.FlowRecord{persisted}

			m := h.autoDrain(h.model(), record)

			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatalf("a stale candidate persisted something: launches=%#v phases=%#v", h.launchUpdates, h.phaseUpdates)
			}
			if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
				t.Fatal("a stale candidate must release its reservation")
			}
			if h.drainArmed(m, record.FlowID) {
				t.Fatal("a stale candidate must not re-arm the drain")
			}
			if m.status.Source == statusOther {
				t.Fatalf("status = %#v, want no sticky status from a stale candidate", m.status)
			}
		})
	}
}

// Dropping a stale candidate disarms the drain rather than disabling it: the
// source that took the candidate produces its own completion edge, and that is
// what starts the next phase.
func TestAutoFlowLaunchStaleCandidateResumesOnTheNextCompletionEdge(t *testing.T) {
	record := autoLaunchGatedRecord()
	record.Phases = append(record.Phases, flowstore.FlowPhase{
		PhaseID: "review-loop", Title: "Review loop", Kind: flowstore.KindReviewLoop,
		Status: flowstore.PhasePending, Order: 3, DependsOn: []string{"implementation"},
	})
	taken := record
	taken.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	taken.Phases[1].Status = flowstore.PhaseRunning

	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{taken}

	m := h.autoDrain(h.model(), record)
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("a stale candidate must leave the drain disarmed")
	}

	finished := record
	finished.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	finished.Phases[1].Status = flowstore.PhaseCompleted
	finished.Phases[2].Status = flowstore.PhaseReady
	h.persistedFlows = []flowstore.FlowRecord{finished}

	m, cmd, _ := m.prepareAutoFlowPhaseLaunch([]flowstore.FlowRecord{taken}, []flowstore.FlowRecord{finished})
	if cmd == nil {
		t.Fatal("the completion edge that superseded the candidate should drain the successor")
	}
	h.drain(m, cmd, 0)
	if len(h.launchUpdates) != 1 || h.launchUpdates[0].PhaseID != "review-loop" {
		t.Fatalf("launch updates = %#v, want the successor phase", h.launchUpdates)
	}
}

// An earlier phase becoming ready between poll and read leaves the candidate
// perfectly launchable, and the store would accept it. Requiring the candidate
// to still be first would strand the launch until the next completion edge.
func TestAutoFlowLaunchCandidateNeedNotStillBeFirst(t *testing.T) {
	record := autoLaunchFlowRecord()
	record.Phases = []flowstore.FlowPhase{
		{PhaseID: "alpha", Title: "Alpha", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 1, DependsOn: []string{}},
		{PhaseID: "beta", Title: "Beta", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 2, DependsOn: []string{}},
	}
	persisted := record
	persisted.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	persisted.Phases[0].Status = flowstore.PhaseReady

	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{persisted}

	h.autoDrain(h.model(), record)

	if len(h.launchUpdates) != 1 || h.launchUpdates[0].PhaseID != "beta" {
		t.Fatalf("launch updates = %#v, want the original beta candidate", h.launchUpdates)
	}
}

func TestAutoFlowLaunchRefusedSilentlyWhileAnotherSourceHoldsFlow(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m = m.setStatus(statusOther, "keep this")

	manual, manualCmd := h.press(m)
	if manualCmd == nil {
		t.Fatal("the manual launch this test races should have been admitted")
	}
	m = manual

	next, autoCmd := h.autoPoll(m, record)
	if autoCmd != nil {
		t.Fatal("AutoMode must not start work while a manual attempt holds the Flow")
	}
	// Unlike the manual refusal, this one is silent: the poll runs at 1 Hz and
	// would otherwise repaint over the display every second.
	if next.status.Text != "keep this" {
		t.Fatalf("status = %q, want a silent AutoMode refusal", next.status.Text)
	}
	if !h.drainArmed(next, record.FlowID) {
		t.Fatal("a refused AutoMode poll must leave the drain armed")
	}
	if len(h.launchUpdates) != 0 {
		t.Fatalf("a refused AutoMode poll persisted %#v", h.launchUpdates)
	}

	// The window is bounded: once the other source releases the Flow, the very
	// next poll launches.
	attempt, _ := next.flowLaunchAttempt(record.FlowID)
	released := next.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	if _, autoCmd = h.autoPoll(released, record); autoCmd == nil {
		t.Fatal("the next poll should launch once the Flow is free")
	}
}

func TestAutoFlowLaunchOccupancyRefusedAtAdmission(t *testing.T) {
	record := autoLaunchFlowRecord()
	tests := []struct {
		name    string
		occupy  func(Model) Model
		release func(Model) Model
	}{
		{
			name: "pending repair launch",
			occupy: func(m Model) Model {
				return m.withPendingFlowRepairLaunch(record.FlowID, "repair-1")
			},
			release: func(m Model) Model {
				return m.withoutPendingFlowRepairLaunch(record.FlowID)
			},
		},
		{
			// Registered synchronously on the r key press, before its write
			// lands, so AutoMode defers rather than racing a resume mid-write.
			name: "pending phase resume",
			occupy: func(m Model) Model {
				key, _ := newFlowPhaseResumeKey(record.FlowID, record.Phases[0].PhaseID)
				return m.withPendingFlowPhaseResume(key, "resume-1")
			},
			release: func(m Model) Model {
				key, _ := newFlowPhaseResumeKey(record.FlowID, record.Phases[0].PhaseID)
				return m.withoutPendingFlowPhaseResume(key)
			},
		},
		{
			name: "flow embedded terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:    embeddedTerminalScopeFlow,
					FlowID:   record.FlowID,
					Terminal: flowPhaseLaunchTestTerminal{state: "running"},
				})
				return m
			},
			release: func(m Model) Model {
				m.embeddedTerminals = nil
				return m
			},
		},
		{
			name: "flow repair terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:      embeddedTerminalScopeFlow,
					FlowID:     record.FlowID,
					FlowRepair: true,
					Terminal:   flowPhaseLaunchTestTerminal{state: "running"},
				})
				return m
			},
			release: func(m Model) Model {
				m.embeddedTerminals = nil
				return m
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			m := tc.occupy(h.model())
			m = m.setStatus(statusOther, "keep this")

			m, autoCmd := h.autoPoll(m, record)
			if autoCmd != nil {
				t.Fatal("an occupied Flow must not start AutoMode work")
			}
			if m.status.Text != "keep this" {
				t.Fatalf("status = %q, want a silent refusal", m.status.Text)
			}
			if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
				t.Fatal("a refused AutoMode poll must not reserve an attempt")
			}
			if !h.drainArmed(m, record.FlowID) {
				t.Fatal("a refused AutoMode poll must leave the drain armed")
			}

			m, autoCmd = h.autoPoll(tc.release(m), record)
			if autoCmd == nil {
				t.Fatal("the next poll should launch once the blocker clears")
			}
			h.drain(m, autoCmd, 0)
			if len(h.launchUpdates) != 1 {
				t.Fatalf("launch updates = %#v, want exactly one", h.launchUpdates)
			}
		})
	}
}

func TestAutoFlowLaunchOccupancyRefusedAtRead(t *testing.T) {
	sessionBlocked := autoLaunchFlowRecord()
	sessionBlocked.Phases[0].LaunchIDs = []string{"launch-source"}

	tests := []struct {
		name    string
		record  flowstore.FlowRecord
		persist func(flowstore.FlowRecord) flowstore.FlowRecord
		stored  []sessions.SessionRecord
	}{
		{
			name:   "live session on the candidate",
			record: sessionBlocked,
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases[0].Sessions = []flowstore.Session{
					{SessionID: "s-1", LaunchID: "launch-source", Status: "running"},
				}
				return record
			},
		},
		{
			name:    "live session known only to the store",
			record:  sessionBlocked,
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord { return record },
			stored: []sessions.SessionRecord{
				{SessionID: "s-1", LaunchID: "launch-source", FlowID: "flow-1", Status: "running"},
			},
		},
		{
			name:   "another phase started between poll and read",
			record: autoLaunchGatedRecord(),
			persist: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Phases = append(record.Phases, flowstore.FlowPhase{
					PhaseID: "review-loop", Title: "Review loop", Kind: flowstore.KindReviewLoop,
					Status: flowstore.PhaseRunning, Order: 3, DependsOn: []string{"plan"},
				})
				return record
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := tc.record
			persisted := record
			persisted.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
			persisted = tc.persist(persisted)

			h := newManualLaunchHarness(t, record)
			h.persistedFlows = []flowstore.FlowRecord{persisted}
			h.sessionRecords = tc.stored
			m := h.model().setStatus(statusOther, "keep this")

			m = h.autoDrain(m, record)

			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatalf("a read-stage refusal persisted something: launches=%#v phases=%#v", h.launchUpdates, h.phaseUpdates)
			}
			if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
				t.Fatal("a read-stage refusal must release the attempt")
			}
			if m.status.Text != "keep this" {
				t.Fatalf("status = %q, want a silent read-stage refusal", m.status.Text)
			}
			// The blocker clears on its own, so this re-arms rather than
			// dropping the launch until another completion edge.
			if !h.drainArmed(m, record.FlowID) {
				t.Fatal("a read-stage refusal must re-arm the drain")
			}

			h.persistedFlows = []flowstore.FlowRecord{record}
			h.sessionRecords = nil
			m, autoCmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{record})
			if autoCmd == nil {
				t.Fatal("the re-armed drain should launch on the next poll")
			}
			h.drain(m, autoCmd, 0)
			if len(h.launchUpdates) != 1 {
				t.Fatalf("launch updates = %#v, want exactly one", h.launchUpdates)
			}
		})
	}
}

// A crashed agent whose session record never reaches "ended" stalls the Flow's
// AutoMode indefinitely and silently. That is accepted for now, and pinned here
// so a future change to liveness has a test to answer to.
func TestAutoFlowLaunchStallsSilentlyOnAnUnendedSession(t *testing.T) {
	record := autoLaunchFlowRecord()
	record.Phases[0].LaunchIDs = []string{"launch-source"}
	persisted := record
	persisted.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	persisted.Phases[0].Sessions = []flowstore.Session{
		{SessionID: "s-1", LaunchID: "launch-source", Status: "running"},
	}

	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{persisted}
	m := h.model()

	for poll := range 3 {
		m = h.autoDrain(m, record)
		if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
			t.Fatalf("poll %d persisted something: launches=%#v phases=%#v", poll, h.launchUpdates, h.phaseUpdates)
		}
		if m.status.Text != "" {
			t.Fatalf("poll %d set status %q, want silence", poll, m.status.Text)
		}
		if !h.drainArmed(m, record.FlowID) {
			t.Fatalf("poll %d disarmed the drain, want the Flow still waiting", poll)
		}
	}
}

// TestAutoFlowLaunchStalledPhaseCostsNoSessionScan is the bound on the previous
// test's stall. Because the lifecycle's session refusal re-arms the drain, a
// stalled phase is re-admitted on every poll, and the authoritative session
// check lists the whole session store each time. When the mirrored sessions the
// poll already carries answer the question, the drain must refuse before
// admission so a Flow that stalls for hours does not walk the session store
// once a second for the duration.
func TestAutoFlowLaunchStalledPhaseCostsNoSessionScan(t *testing.T) {
	record := autoLaunchFlowRecord()
	record.Phases[0].LaunchIDs = []string{"launch-source"}
	record.Phases[0].Sessions = []flowstore.Session{
		{SessionID: "s-1", LaunchID: "launch-source", Status: "running"},
	}
	h := newManualLaunchHarness(t, record)
	m := h.model()

	for poll := range 3 {
		m = h.autoDrain(m, record)
		if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
			t.Fatalf("poll %d persisted something: launches=%#v phases=%#v", poll, h.launchUpdates, h.phaseUpdates)
		}
		if m.status.Text != "" {
			t.Fatalf("poll %d set status %q, want silence", poll, m.status.Text)
		}
		if !h.drainArmed(m, record.FlowID) {
			t.Fatalf("poll %d disarmed the drain, want the Flow still waiting", poll)
		}
		if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
			t.Fatalf("poll %d reserved the Flow for a launch it never made", poll)
		}
	}
	if h.sessionListCalls != 0 {
		t.Fatalf("session store listed %d times, want the snapshot to answer for free", h.sessionListCalls)
	}

	// The refusal is a deferral, not a disarm: once the mirrored session ends,
	// the very next poll launches without needing a fresh completion edge.
	ended := record
	ended.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	ended.Phases[0].Sessions = []flowstore.Session{
		{SessionID: "s-1", LaunchID: "launch-source", Status: "ended"},
	}
	h.record = ended
	h.persistedFlows = []flowstore.FlowRecord{ended}

	m = h.autoDrain(m, ended)

	if len(h.launchUpdates) != 1 {
		t.Fatalf("launch updates = %#v, want the deferred launch to resume", h.launchUpdates)
	}
	if h.drainArmed(m, record.FlowID) {
		t.Fatal("the resumed launch must disarm the drain")
	}
}

func TestAutoFlowLaunchQueuedAnnouncement(t *testing.T) {
	titleless := autoLaunchFlowRecord()
	titleless.Title = ""

	tests := []struct {
		name   string
		record flowstore.FlowRecord
		want   string
	}{
		{
			name:   "titled Flow announces once preflight has passed",
			record: autoLaunchFlowRecord(),
			want:   "Flow Flow one: implementation queued",
		},
		{
			// The event's title has already fallen back to the Flow ID, so a
			// guard written against it would ship a status this Flow never had.
			name:   "titleless Flow announces nothing",
			record: titleless,
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, tc.record)
			m := h.model()

			m, autoCmd := h.autoPoll(m, tc.record)
			if autoCmd == nil {
				t.Fatal("AutoMode should have been admitted")
			}
			if m.status.Text != "" {
				t.Fatalf("admission announced %q before preflight ran", m.status.Text)
			}

			m = h.drain(m, autoCmd, 0)
			if m.status.Text != tc.want {
				t.Fatalf("status = %q, want %q", m.status.Text, tc.want)
			}
			if tc.want != "" && m.status.Source != statusFlowAutoAdvance {
				t.Fatalf("status source = %v, want the transient AutoMode source", m.status.Source)
			}
		})
	}
}

// TestAutoFlowLaunchQueuedAnnouncementYieldsToTransitions pins the precedence
// the drain used to get from ordering alone. The announcement now arrives a hop
// after the transition statuses computed by the same poll, so if it replaced
// its own source a routine launch on one Flow would erase a needs_attention
// raised by a different one — the only prompt the user gets that a Flow wants a
// human, and it is not repeated.
func TestAutoFlowLaunchQueuedAnnouncementYieldsToTransitions(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)

	const alert = "Flow two: needs attention"
	m, _ := h.model().setAutoAdvanceStatus(alert)
	if m.status.Text != alert {
		t.Fatalf("precondition: status = %q, want the transition status", m.status.Text)
	}

	m, autoCmd := h.autoPoll(m, record)
	if autoCmd == nil {
		t.Fatal("AutoMode should have been admitted")
	}
	m = h.drain(m, autoCmd, 0)

	if m.status.Text != alert {
		t.Fatalf("status = %q, want the needs-attention alert to survive the launch", m.status.Text)
	}
	// Yielding the announcement must not cost the launch itself.
	if len(h.launchUpdates) != 1 || len(h.launchContexts) != 1 {
		t.Fatalf("launch updates = %d, contexts = %d, want the launch to proceed", len(h.launchUpdates), len(h.launchContexts))
	}
}

// TestAutoFlowLaunchQueuedAnnouncementReplacesItsOwnKind is the bound on the
// test above. Yielding is a rank rule, not a first-writer-wins rule: the queued
// announcement is emitted once per launch and never re-reported, because
// admission disarms the drain and the read's success branch never re-arms it.
// If it also yielded to a live announcement, a second Flow launching inside the
// same 3 s window would never be announced at all.
func TestAutoFlowLaunchQueuedAnnouncementReplacesItsOwnKind(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)

	const earlier = "Flow zero: implementation queued"
	m, _ := h.model().setAutoAdvanceLaunchStatus(earlier)
	if m.status.Text != earlier {
		t.Fatalf("precondition: status = %q, want the earlier announcement", m.status.Text)
	}

	m, autoCmd := h.autoPoll(m, record)
	if autoCmd == nil {
		t.Fatal("AutoMode should have been admitted")
	}
	m = h.drain(m, autoCmd, 0)

	if m.status.Text != "Flow Flow one: implementation queued" {
		t.Fatalf("status = %q, want this launch to replace the earlier announcement", m.status.Text)
	}
}

func TestAutoFlowLaunchDelayedEventsAreIgnored(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	base := h.model()

	live, ok := base.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:   "token-2",
		Kind:    flowLaunchKindAutoPhase,
		FlowID:  record.FlowID,
		PhaseID: record.Phases[0].PhaseID,
	}, flowLaunchStateReading)
	if !ok {
		t.Fatal("reservation should succeed")
	}

	tests := []struct {
		name  string
		model Model
		event flowLaunchEventMsg
	}{
		{
			name:  "read failure for a released attempt",
			model: base,
			event: flowLaunchEventMsg{
				Token: "token-1", Kind: flowLaunchKindAutoPhase, From: flowLaunchStateReading,
				FlowID: record.FlowID, Stage: flowLaunchStageRead,
				Outcome: flowLaunchOutcomeFailed, FlowTitle: "Flow one", Err: "store unavailable",
			},
		},
		{
			name:  "read failure from a superseded attempt",
			model: live,
			event: flowLaunchEventMsg{
				Token: "token-1", Kind: flowLaunchKindAutoPhase, From: flowLaunchStateReading,
				FlowID: record.FlowID, Stage: flowLaunchStageRead,
				Outcome: flowLaunchOutcomeFailed, FlowTitle: "Flow one", Err: "store unavailable",
			},
		},
		{
			// Retry is the branch that actually re-arms, so a superseded one is
			// the only delayed event that could hand a drain back to a Flow a
			// newer attempt is already launching.
			name:  "retry from a superseded attempt",
			model: live,
			event: flowLaunchEventMsg{
				Token: "token-1", Kind: flowLaunchKindAutoPhase, From: flowLaunchStateReading,
				FlowID: record.FlowID, Stage: flowLaunchStageRead,
				Outcome: flowLaunchOutcomeRetry, FlowTitle: "Flow one",
			},
		},
		{
			name:  "retry for a released attempt",
			model: base,
			event: flowLaunchEventMsg{
				Token: "token-1", Kind: flowLaunchKindAutoPhase, From: flowLaunchStateReading,
				FlowID: record.FlowID, Stage: flowLaunchStageRead,
				Outcome: flowLaunchOutcomeRetry, FlowTitle: "Flow one",
			},
		},
		{
			name:  "prepared failure from a superseded attempt",
			model: live,
			event: flowLaunchEventMsg{
				Token: "token-1", Kind: flowLaunchKindAutoPhase, From: flowLaunchStatePreparing,
				FlowID: record.FlowID, PhaseID: record.Phases[0].PhaseID, Stage: flowLaunchStagePrepared,
				Record: record, Err: "failed to mark flow phase running: store unavailable",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next, cmd := tc.model.handleFlowLaunchEvent(tc.event)
			if cmd != nil {
				t.Fatalf("a delayed event produced a command %T", cmd)
			}
			if h.drainArmed(next, record.FlowID) {
				t.Fatal("a delayed event re-armed a drain it does not own")
			}
			if next.status.Text != "" {
				t.Fatalf("a delayed event set status %q", next.status.Text)
			}
			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatal("a delayed event persisted something")
			}
		})
	}
}

func TestAutoFlowLaunchExternalRouteKeepsAutoLaunchContext(t *testing.T) {
	record := autoLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.agentCommand = "codex-app"

	h.autoDrain(h.model(), record)

	if len(h.agentContexts) != 1 {
		t.Fatalf("external launches = %d, want 1", len(h.agentContexts))
	}
	ctx := h.agentContexts[0]
	if !ctx.FlowAutoLaunch {
		t.Fatalf("external auto context = %#v, want FlowAutoLaunch", ctx)
	}
	if ctx.Embedded || ctx.FlowLaunchTracked || ctx.Headless {
		t.Fatalf("external auto context = %#v, want embedded/tracked/headless unset", ctx)
	}
	if len(h.launchContexts) != 0 {
		t.Fatal("the external route must not open an embedded terminal")
	}
}

func TestManualFlowLaunchStatusPrecedence(t *testing.T) {
	launchable := manualLaunchFlowRecord()
	blocked := manualLaunchFlowRecord()
	blocked.Phases[0].Status = flowstore.PhaseCompleted

	tests := []struct {
		name          string
		record        flowstore.FlowRecord
		agentCommand  string
		headlessWait  bool
		occupy        bool
		wantStatus    string
		wantAdvertise bool
	}{
		{
			// Dismissing the terminal would reveal nothing to launch, so
			// naming it as the obstacle would send the user to do nothing.
			name:         "no launchable phase wins over occupancy",
			record:       blocked,
			agentCommand: "",
			occupy:       true,
			wantStatus:   "No launchable Flow phase",
		},
		{
			name:         "occupancy reuses the existing launchability refusal",
			record:       launchable,
			agentCommand: "",
			occupy:       true,
			wantStatus:   noLaunchableFlowPhaseStatus,
		},
		{
			name:         "pending headless write wins over occupancy",
			record:       launchable,
			agentCommand: "",
			occupy:       true,
			headlessWait: true,
			wantStatus:   flowHeadlessWritePendingStatus,
		},
		{
			name:         "pending headless write wins over no launchable phase",
			record:       blocked,
			agentCommand: "",
			headlessWait: true,
			wantStatus:   flowHeadlessWritePendingStatus,
		},
		{
			name:         "no launchable phase wins over unset agent",
			record:       blocked,
			agentCommand: "",
			wantStatus:   "No launchable Flow phase",
		},
		{
			name:         "pending headless write reported once a phase is launchable",
			record:       launchable,
			agentCommand: "",
			headlessWait: true,
			wantStatus:   flowHeadlessWritePendingStatus,
		},
		{
			name:          "unset agent reported once a phase is launchable",
			record:        launchable,
			agentCommand:  "",
			wantStatus:    "Press A to choose " + ui.AgentInputPlaceholder + " before launching an agent",
			wantAdvertise: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, tc.record)
			opts := h.options()
			opts.AgentCommand = tc.agentCommand
			m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
			m.contentPane = ui.PaneBottom
			m.bottomMode = ui.ModeFlows
			m.flows = m.flows.SetItems([]flowstore.FlowRecord{tc.record})
			if tc.headlessWait {
				m = m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
					flowID:   tc.record.FlowID,
					repoPath: tc.record.RepoPath,
					enabled:  true,
				})
			}
			if tc.occupy {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:    embeddedTerminalScopeFlow,
					FlowID:   tc.record.FlowID,
					Terminal: flowPhaseLaunchTestTerminal{state: "running"},
				})
			}

			// Whatever the reason, the footer must not advertise a launch the
			// key press is about to refuse. Only the unset-agent case is
			// advertised: pressing A then g is the documented recovery.
			if got := m.selectedFlowHasLaunchablePhase(); got != tc.wantAdvertise {
				t.Fatalf("footer advertised launch = %v, want %v", got, tc.wantAdvertise)
			}
			next, _ := m.handleLaunchNextFlowPhase()
			if got := next.(Model).status.Text; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if len(h.launchUpdates) != 0 {
				t.Fatalf("rejected launch persisted a launch ID: %#v", h.launchUpdates)
			}
		})
	}
}

func TestManualFlowLaunchPreservesAdmissionAgentSettings(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()

	next, readCmd := m.handleLaunchNextFlowPhase()
	m = next.(Model)
	if readCmd == nil {
		t.Fatal("manual launch should schedule the authoritative read")
	}

	// Settings may change while the store read is in flight. This launch must
	// keep the snapshot that admission validated instead of changing route.
	m.agentCommand = agent.CommandCodexApp
	readMsg := readCmd()
	nextModel, prepareCmd := m.Update(readMsg)
	m = nextModel.(Model)
	if prepareCmd == nil {
		t.Fatal("authoritative read should schedule preparation")
	}
	preparedMsg := prepareCmd()
	nextModel, handoffCmd := m.Update(preparedMsg)
	_ = nextModel
	_ = handoffCmd

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
	}
	if len(h.agentContexts) != 0 {
		t.Fatalf("external launches = %d, want 0", len(h.agentContexts))
	}
	if got := h.launchContexts[0].Command; got != agent.CommandCodex {
		t.Fatalf("launch command = %q, want admission snapshot %q", got, agent.CommandCodex)
	}
}

func TestManualFlowLaunchPreservesSubmittingSurface(t *testing.T) {
	tests := []struct {
		name   string
		active bool
		want   flowLaunchOrigin
	}{
		{name: "flows", want: flowLaunchOriginFlows},
		{name: "active flows", active: true, want: flowLaunchOriginActiveFlows},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := manualLaunchFlowRecord()
			h := newManualLaunchHarness(t, record)
			m := h.model()
			if tc.active {
				m.activeFlowSurface = true
				m.activeFlows = m.activeFlows.SetItems([]flowstore.FlowRecord{record})
			}

			next, cmd := m.handleLaunchNextFlowPhase()
			m = next.(Model)
			if cmd == nil {
				t.Fatal("manual launch should schedule the authoritative read")
			}
			attempt, ok := m.flowLaunchAttempt(record.FlowID)
			if !ok || attempt.Origin != tc.want {
				t.Fatalf("attempt origin = %v, want %v (attempt=%#v)", attempt.Origin, tc.want, attempt)
			}
		})
	}
}

func TestManualFlowLaunchHeadlessComesFromPersistedRecord(t *testing.T) {
	tests := []struct {
		name            string
		paneHeadless    bool
		persisted       flowstore.FlowRecord
		persistedSet    bool
		wantHeadless    bool
		wantPersistUsed bool
	}{
		{
			name:         "persisted record overrides the cached preference",
			paneHeadless: false,
			persisted: func() flowstore.FlowRecord {
				record := manualLaunchFlowRecord()
				record.Headless = true
				record.UpdatedAt = time.Now()
				return record
			}(),
			persistedSet: true,
			wantHeadless: true,
		},
		{
			name:         "zero-time persisted record keeps the requested preference",
			paneHeadless: true,
			persisted: func() flowstore.FlowRecord {
				record := manualLaunchFlowRecord()
				record.Headless = false
				record.UpdatedAt = time.Time{}
				return record
			}(),
			persistedSet: true,
			wantHeadless: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := manualLaunchFlowRecord()
			record.Headless = tc.paneHeadless
			h := newManualLaunchHarness(t, record)
			h.persistedRecord = tc.persisted
			h.persistedRecordOK = tc.persistedSet

			h.launch(h.model())

			if len(h.launchContexts) != 1 {
				t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
			}
			if got := h.launchContexts[0].Headless; got != tc.wantHeadless {
				t.Fatalf("ctx.Headless = %v, want %v", got, tc.wantHeadless)
			}
		})
	}
}

func TestManualFlowLaunchFailureClassification(t *testing.T) {
	implementation := manualLaunchFlowRecord()

	planReview := manualLaunchFlowRecord()
	planReview.PlanID = "plan-1"
	planReview.PlanPath = "/dev/alpha/plan.md"
	planReview.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Title: "Plan review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 1},
	}

	tests := []struct {
		name        string
		record      flowstore.FlowRecord
		wantStatus  string
		wantOutcome string
	}{
		{
			name:       "non plan review failure needs attention",
			record:     implementation,
			wantStatus: flowstore.PhaseNeedsAttention,
		},
		{
			name:        "plan review failure is blocked",
			record:      planReview,
			wantStatus:  flowstore.PhaseBlocked,
			wantOutcome: flowstore.OutcomeBlocked,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, tc.record)
			h.startTerminalErr = errors.New("terminal unavailable")

			m := h.launch(h.model())

			if len(h.phaseUpdates) != 1 {
				t.Fatalf("phase updates = %#v, want exactly one failure update", h.phaseUpdates)
			}
			update := h.phaseUpdates[0]
			if update.FlowID != tc.record.FlowID || update.PhaseID != tc.record.Phases[0].PhaseID {
				t.Fatalf("failure update targeted %s/%s", update.FlowID, update.PhaseID)
			}
			if update.Status != tc.wantStatus || update.Outcome != tc.wantOutcome {
				t.Fatalf("failure update status/outcome = %s/%s, want %s/%s", update.Status, update.Outcome, tc.wantStatus, tc.wantOutcome)
			}
			if !strings.Contains(update.Notes, "terminal unavailable") {
				t.Fatalf("failure notes = %q, want the launch error", update.Notes)
			}
			if got := m.status.Text; !strings.Contains(got, "terminal unavailable") {
				t.Fatalf("status = %q, want the launch error", got)
			}
		})
	}
}

func TestManualFlowLaunchFailureRefreshesFlowSurface(t *testing.T) {
	record := manualLaunchFlowRecord()
	m := newManualLaunchHarness(t, record).model()
	ctx := actions.AgentLaunchContext{
		FlowID:            record.FlowID,
		FlowPhaseID:       record.Phases[0].PhaseID,
		FlowLaunchTracked: true,
		LaunchID:          "launch-1",
	}

	next, cmd := m.handleFlowLaunchFailurePersisted(flowLaunchFailurePersistedMsg{
		LaunchContext: ctx,
		OriginalErr:   "boom",
		PersistErr:    errors.New("disk full"),
	})
	if got := next.status.Text; !strings.Contains(got, "boom") || !strings.Contains(got, "disk full") {
		t.Fatalf("status = %q, want both the launch and persistence errors", got)
	}
	if cmd == nil {
		t.Fatal("failure persistence should refresh the visible Flow surface")
	}
}

// --- Lifecycle module matrix ---

func (h *manualLaunchHarness) press(m Model) (Model, tea.Cmd) {
	h.t.Helper()
	next, cmd := m.handleLaunchNextFlowPhase()
	return next.(Model), cmd
}

// step runs one asynchronous hop and applies its message, exactly as the
// runtime would.
func (h *manualLaunchHarness) step(m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		h.t.Fatal("expected a launch command")
	}
	msg, ok := runCommandWithoutWaiting(cmd)
	if !ok || msg == nil {
		h.t.Fatal("launch hop produced no message")
	}
	next, nextCmd := m.Update(msg)
	return next.(Model), nextCmd
}

func (h *manualLaunchHarness) attempt(m Model, flowID string) (flowLaunchAttempt, bool) {
	h.t.Helper()
	return m.flowLaunchAttempt(flowID)
}

func TestFlowLaunchDuplicateIntentIsRefused(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()

	m, readCmd := h.press(m)
	if readCmd == nil {
		t.Fatal("first launch should start the authoritative read")
	}
	attempt, ok := h.attempt(m, record.FlowID)
	if !ok || attempt.State != flowLaunchStateReading {
		t.Fatalf("attempt after admission = %#v, ok=%v; want reading", attempt, ok)
	}

	second, secondCmd := h.press(m)
	if secondCmd != nil {
		t.Fatal("second launch should not start any work")
	}
	if got := second.status.Text; got != noLaunchableFlowPhaseStatus {
		t.Fatalf("second launch status = %q, want %q", got, noLaunchableFlowPhaseStatus)
	}
	if again, _ := h.attempt(second, record.FlowID); again.Token != attempt.Token {
		t.Fatalf("second launch replaced the live attempt: %#v", again)
	}

	m = h.drain(m, readCmd, 0)
	if len(h.launchUpdates) != 1 {
		t.Fatalf("launch ID persistence calls = %d, want exactly one", len(h.launchUpdates))
	}
}

func TestAutoModePreparationReservesAgainstManualLaunch(t *testing.T) {
	record := manualLaunchFlowRecord()
	record.AutoMode = true
	h := newManualLaunchHarness(t, record)
	m := h.model().armAutoAdvanceDrain(record.FlowID)

	m, autoCmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{record})
	if autoCmd == nil {
		t.Fatal("AutoMode drain should schedule the authoritative read")
	}
	// AutoMode now reserves before the read rather than after preflight, so the
	// window a manual g has to lose is the whole lifecycle, not just prepare.
	attempt, ok := m.flowLaunchAttempt(record.FlowID)
	if !ok || attempt.Kind != flowLaunchKindAutoPhase || attempt.State != flowLaunchStateReading {
		t.Fatalf("AutoMode read attempt = %#v ok=%v", attempt, ok)
	}

	manual, manualCmd := m.handleLaunchNextFlowPhase()
	m = manual.(Model)
	if manualCmd != nil {
		t.Fatal("manual launch should be refused while AutoMode holds the Flow")
	}
	if got := m.status.Text; got != noLaunchableFlowPhaseStatus {
		t.Fatalf("manual refusal = %q, want %q", got, noLaunchableFlowPhaseStatus)
	}

	m = h.drain(m, autoCmd, 0)
	if len(h.launchUpdates) != 1 || len(h.launchContexts) != 1 {
		t.Fatalf("AutoMode launch updates=%d terminals=%d, want one each", len(h.launchUpdates), len(h.launchContexts))
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("embedded slot should replace the AutoMode preparation attempt")
	}
	if !m.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatal("embedded slot should own the Flow after AutoMode handoff")
	}
}

func TestAutoModePreparationFailureReleasesReservation(t *testing.T) {
	record := manualLaunchFlowRecord()
	record.AutoMode = true
	h := newManualLaunchHarness(t, record)
	h.addLaunchIDErr = errors.New("store unavailable")
	m := h.model().armAutoAdvanceDrain(record.FlowID)

	m, autoCmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{record})
	if autoCmd == nil {
		t.Fatal("AutoMode drain should schedule the authoritative read")
	}
	m = h.drain(m, autoCmd, 0)
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("failed AutoMode preparation stranded its reservation")
	}
	if !h.drainArmed(m, record.FlowID) {
		t.Fatal("a pre-mutation AutoMode failure must re-arm the drain")
	}
}

func TestFlowLaunchAdmissionRejectsLegacyOccupancy(t *testing.T) {
	record := manualLaunchFlowRecord()
	tests := []struct {
		name   string
		occupy func(Model) Model
	}{
		{
			name: "pending repair launch",
			occupy: func(m Model) Model {
				return m.withPendingFlowRepairLaunch(record.FlowID, "repair-1")
			},
		},
		{
			name: "pending phase resume",
			occupy: func(m Model) Model {
				key, _ := newFlowPhaseResumeKey(record.FlowID, record.Phases[0].PhaseID)
				return m.withPendingFlowPhaseResume(key, "resume-1")
			},
		},
		{
			name: "flow embedded terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:    embeddedTerminalScopeFlow,
					FlowID:   record.FlowID,
					Terminal: flowPhaseLaunchTestTerminal{state: "running"},
				})
				return m
			},
		},
		{
			name: "flow repair terminal",
			occupy: func(m Model) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:      embeddedTerminalScopeFlow,
					FlowID:     record.FlowID,
					FlowRepair: true,
					Terminal:   flowPhaseLaunchTestTerminal{state: "running"},
				})
				return m
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			m := tc.occupy(h.model())

			if m.selectedFlowHasLaunchablePhase() {
				t.Fatal("footer should not advertise a launch into an occupied Flow")
			}
			next, cmd := h.press(m)
			if cmd != nil {
				t.Fatal("occupied Flow should not start any launch work")
			}
			if got := next.status.Text; got != noLaunchableFlowPhaseStatus {
				t.Fatalf("status = %q, want %q", got, noLaunchableFlowPhaseStatus)
			}
			if _, ok := h.attempt(next, record.FlowID); ok {
				t.Fatal("occupied Flow must not reserve an attempt")
			}
		})
	}
}

func TestLegacySourcesRejectWhileLifecycleHoldsFlow(t *testing.T) {
	record := manualLaunchFlowRecord()
	record.Phases[0].Status = flowstore.PhaseNeedsAttention
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.activeFlowSurface = false
	held, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:   "token-1",
		Kind:    flowLaunchKindManualPhase,
		FlowID:  record.FlowID,
		PhaseID: record.Phases[0].PhaseID,
	}, flowLaunchStateReading)
	if !ok {
		t.Fatal("reservation should succeed on a free Flow")
	}

	if held.selectedFlowRepairReady() {
		t.Fatal("repair must not arm while a launch attempt holds the Flow")
	}
	if !held.flowAutoAdvanceOccupied(record) {
		t.Fatal("AutoMode must treat a launch attempt as occupancy")
	}
	resumed, resumeCmd := held.launchTrackedFlowPhaseResumeWithContext(actions.AgentLaunchContext{
		FlowID:      record.FlowID,
		FlowPhaseID: record.Phases[0].PhaseID,
	})
	if resumeCmd != nil || len(resumed.pendingFlowPhaseResumes) != 0 {
		t.Fatal("phase resume must not start while a launch attempt holds the Flow")
	}
}

// Repair reads the persisted headless preference, so it refuses while a write
// is in flight. The footer has to withdraw for exactly as long, and the refusal
// has to name the durable obstacle when there is one.
func TestRepairFooterAndAdmissionAgreeOnPendingHeadlessWrite(t *testing.T) {
	record := manualLaunchFlowRecord()
	record.Phases[0].Status = flowstore.PhaseNeedsAttention
	h := newManualLaunchHarness(t, record)
	base := h.model()
	base.activeFlowSurface = false
	if !base.selectedFlowRepairReady() {
		t.Fatal("fixture should be repairable before anything holds the Flow")
	}

	pending := base.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
		flowID:   record.FlowID,
		repoPath: record.RepoPath,
		enabled:  false,
	})
	if pending.selectedFlowRepairReady() {
		t.Fatal("footer must withdraw repair while a headless write is in flight")
	}
	next, cmd := pending.handleRepairSelectedFlow()
	if cmd != nil {
		t.Fatal("a refused repair should start no work")
	}
	if got := next.(Model).status.Text; got != flowHeadlessWritePendingStatus {
		t.Fatalf("status = %q, want %q", got, flowHeadlessWritePendingStatus)
	}

	occupied := pending
	occupied.embeddedTerminals = append(occupied.embeddedTerminals, embeddedTerminalSlot{
		Scope:    embeddedTerminalScopeFlow,
		FlowID:   record.FlowID,
		Terminal: flowPhaseLaunchTestTerminal{state: "running"},
	})
	next, _ = occupied.handleRepairSelectedFlow()
	const wantTerminal = "Close, detach, or dismiss the existing Flow terminal before repairing this Flow"
	if got := next.(Model).status.Text; got != wantTerminal {
		t.Fatalf("status = %q, want the terminal obstacle %q", got, wantTerminal)
	}
}

func TestFlowLaunchOccupancyMatchesExactFlowID(t *testing.T) {
	shortRecord := manualLaunchFlowRecord()
	shortRecord.FlowID = "flow-a"
	longRecord := manualLaunchFlowRecord()
	longRecord.FlowID = "flow-abc"

	h := newManualLaunchHarness(t, longRecord)
	m := h.model()
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{longRecord, shortRecord})
	held, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:  "token-1",
		Kind:   flowLaunchKindManualPhase,
		FlowID: "flow-a",
	}, flowLaunchStateReading)
	if !ok {
		t.Fatal("reservation should succeed")
	}

	if held.flowLaunchAttemptOccupied("flow-abc") {
		t.Fatal("flow-a must not occupy flow-abc")
	}
	if !held.flowLaunchAttemptOccupied("flow-a") {
		t.Fatal("flow-a should occupy itself")
	}
	if _, _, ok := held.previewFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: "flow-abc"}); !ok {
		t.Fatal("prefix-like Flow ID should still be launchable")
	}
}

func TestFlowStarterPlanLaunchUsesFreshlyCreatedFlowID(t *testing.T) {
	// gzs.1 leaves FlowStarter.StartPlan unguarded because the Flow it launches
	// was created by the same call and cannot collide with a live attempt.
	var launched flowstore.PhaseLaunchUpdate
	starter := NewFlowStarter(FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "created-flow"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady, Order: 1}}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/created", Branch: "flow/created"}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launched = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		NewLaunchID:   func() string { return "launch-1" },
	})

	result, err := starter.StartPlan(FlowStartRequest{RepoPath: "/dev/alpha", Title: "New flow"})
	if err != nil {
		t.Fatalf("StartPlan() error = %v", err)
	}
	if launched.FlowID != "created-flow" || result.Flow.FlowID != "created-flow" {
		t.Fatalf("StartPlan launched %q, want the Flow it just created", launched.FlowID)
	}
}

func TestFlowLaunchStaleEventsAreIgnored(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	base := h.model()
	held, ok := base.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:  "token-1",
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID,
	}, flowLaunchStateReading)
	if !ok {
		t.Fatal("reservation should succeed")
	}

	tests := []struct {
		name  string
		event flowLaunchEventMsg
	}{
		{
			name:  "wrong token",
			event: flowLaunchEventMsg{Token: "token-2", Kind: flowLaunchKindManualPhase, From: flowLaunchStateReading, FlowID: record.FlowID, Stage: flowLaunchStageRead, PhaseID: record.Phases[0].PhaseID, Record: record},
		},
		{
			name:  "wrong kind",
			event: flowLaunchEventMsg{Token: "token-1", Kind: flowLaunchKindAutoPhase, From: flowLaunchStateReading, FlowID: record.FlowID, Stage: flowLaunchStageRead, PhaseID: record.Phases[0].PhaseID, Record: record},
		},
		{
			name:  "wrong from state",
			event: flowLaunchEventMsg{Token: "token-1", Kind: flowLaunchKindManualPhase, From: flowLaunchStatePreparing, FlowID: record.FlowID, Stage: flowLaunchStagePrepared, PhaseID: record.Phases[0].PhaseID, Record: record},
		},
		{
			name:  "read stage with preparing state",
			event: flowLaunchEventMsg{Token: "token-1", Kind: flowLaunchKindManualPhase, From: flowLaunchStatePreparing, FlowID: record.FlowID, Stage: flowLaunchStageRead, PhaseID: record.Phases[0].PhaseID, Record: record},
		},
		{
			name:  "prepared stage with reading state",
			event: flowLaunchEventMsg{Token: "token-1", Kind: flowLaunchKindManualPhase, From: flowLaunchStateReading, FlowID: record.FlowID, Stage: flowLaunchStagePrepared, PhaseID: record.Phases[0].PhaseID, Record: record},
		},
		{
			name:  "unknown flow",
			event: flowLaunchEventMsg{Token: "token-1", Kind: flowLaunchKindManualPhase, From: flowLaunchStateReading, FlowID: "flow-other", Stage: flowLaunchStageRead},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next, cmd := held.handleFlowLaunchEvent(tc.event)
			if cmd != nil {
				t.Fatalf("stale event produced a command %T", cmd)
			}
			if next.status.Text != "" {
				t.Fatalf("stale event replaced the status with %q", next.status.Text)
			}
			attempt, ok := next.flowLaunchAttempt(record.FlowID)
			if !ok || attempt.Token != "token-1" || attempt.State != flowLaunchStateReading {
				t.Fatalf("stale event disturbed the live attempt: %#v ok=%v", attempt, ok)
			}
			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 || len(h.launchContexts) != 0 {
				t.Fatal("stale event caused persistence or a terminal")
			}
		})
	}
}

func TestFlowLaunchRejectsFreshRecordThatIsNoLongerLaunchable(t *testing.T) {
	record := manualLaunchFlowRecord()
	persisted := manualLaunchFlowRecord()
	persisted.Phases[0].Status = flowstore.PhaseCompleted

	h := newManualLaunchHarness(t, record)
	h.persistedFlows = []flowstore.FlowRecord{persisted}

	m := h.launch(h.model())

	if got := m.status.Text; got != noLaunchableFlowPhaseStatus {
		t.Fatalf("status = %q, want %q", got, noLaunchableFlowPhaseStatus)
	}
	if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
		t.Fatalf("stale launchability persisted something: launches=%#v phases=%#v", h.launchUpdates, h.phaseUpdates)
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("refused launch left an attempt behind")
	}
}

func TestFlowLaunchLiveSessionScope(t *testing.T) {
	tests := []struct {
		name       string
		mirrored   []flowstore.Session
		stored     []sessions.SessionRecord
		wantLaunch bool
	}{
		{
			name:       "live session mirrored on the phase blocks",
			mirrored:   []flowstore.Session{{SessionID: "s-1", LaunchID: "launch-1", Status: "running"}},
			wantLaunch: false,
		},
		{
			name:       "live session known only to the store blocks",
			stored:     []sessions.SessionRecord{{SessionID: "s-1", LaunchID: "launch-1", FlowID: "flow-1", Status: "running"}},
			wantLaunch: false,
		},
		{
			name:       "live session for another launch does not block",
			stored:     []sessions.SessionRecord{{SessionID: "s-2", LaunchID: "launch-stray", FlowID: "flow-1", Status: "running"}},
			wantLaunch: true,
		},
		{
			name:       "ended session does not block",
			mirrored:   []flowstore.Session{{SessionID: "s-1", LaunchID: "launch-1", Status: "ended", EndedAt: time.Now()}},
			stored:     []sessions.SessionRecord{{SessionID: "s-1", LaunchID: "launch-1", FlowID: "flow-1", Status: "ended", EndedAt: time.Now()}},
			wantLaunch: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := manualLaunchFlowRecord()
			// A ready phase can still carry launch IDs from an earlier attempt;
			// only a live session on one of them blocks the relaunch.
			record.Phases[0].LaunchIDs = []string{"launch-1"}
			persisted := record
			persisted.Phases = []flowstore.FlowPhase{record.Phases[0]}
			persisted.Phases[0].Sessions = tc.mirrored

			h := newManualLaunchHarness(t, record)
			h.persistedFlows = []flowstore.FlowRecord{persisted}
			h.sessionRecords = tc.stored

			m := h.launch(h.model())

			launched := len(h.launchUpdates) == 1
			if launched != tc.wantLaunch {
				t.Fatalf("launch persisted = %v, want %v (status %q)", launched, tc.wantLaunch, m.status.Text)
			}
			// Live-session occupancy preserves the existing launch-refusal text.
			if !tc.wantLaunch && m.status.Text != noLaunchableFlowPhaseStatus {
				t.Fatalf("status = %q, want %q", m.status.Text, noLaunchableFlowPhaseStatus)
			}
		})
	}
}

func TestFlowLaunchReadFailuresReleaseWithoutPersisting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manualLaunchHarness)
	}{
		{name: "flow read error", mutate: func(h *manualLaunchHarness) { h.readErr = errors.New("read flow: disk gone") }},
		{name: "session read error", mutate: func(h *manualLaunchHarness) { h.sessionsErr = errors.New("list sessions: disk gone") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := manualLaunchFlowRecord()
			h := newManualLaunchHarness(t, record)
			tc.mutate(h)

			m := h.launch(h.model())

			if got := m.status.Text; !strings.Contains(got, "disk gone") {
				t.Fatalf("status = %q, want the raw read error", got)
			}
			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatal("a failed read must not persist anything")
			}
			if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
				t.Fatal("a failed read must release the attempt")
			}
		})
	}
}

func TestFlowLaunchPrepareFailureLeavesPhaseAlone(t *testing.T) {
	// A custom phase kind is the one that pre-reads the plan body.
	planBodyRecord := manualLaunchFlowRecord()
	planBodyRecord.PlanID = "plan-1"
	planBodyRecord.PlanPath = "/state/plans/plan-1/plan.md"
	planBodyRecord.Phases = []flowstore.FlowPhase{
		{PhaseID: "custom-step", Title: "Custom step", Status: flowstore.PhaseReady, Order: 1},
	}

	tests := []struct {
		name   string
		record flowstore.FlowRecord
		mutate func(*manualLaunchHarness)
		want   string
	}{
		{
			name:   "plan body read fails",
			record: planBodyRecord,
			mutate: func(h *manualLaunchHarness) { h.planBodyErr = errors.New("plan unreadable") },
			want:   "plan unreadable",
		},
		{
			name:   "launch ID persistence fails",
			record: manualLaunchFlowRecord(),
			mutate: func(h *manualLaunchHarness) { h.addLaunchIDErr = errors.New("store locked") },
			want:   "store locked",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := tc.record
			h := newManualLaunchHarness(t, record)
			tc.mutate(h)

			m := h.launch(h.model())

			if len(h.phaseUpdates) != 0 {
				t.Fatalf("a failed prepare must not write the phase: %#v", h.phaseUpdates)
			}
			if got := m.status.Text; !strings.Contains(got, tc.want) {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
			if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
				t.Fatal("a failed prepare must release the attempt")
			}
		})
	}
}

func TestFlowLaunchCloseBetweenReadAndPrepareStartsNoAgent(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()

	next, readCmd := m.handleLaunchNextFlowPhase()
	m = next.(Model)
	if readCmd == nil {
		t.Fatal("manual launch should schedule the authoritative read")
	}
	readMsg := readCmd()
	nextModel, prepareCmd := m.Update(readMsg)
	m = nextModel.(Model)
	if prepareCmd == nil {
		t.Fatal("authoritative read should schedule preparation")
	}

	// The close lands after the lifecycle's authoritative read but before the
	// transactional launch-ID reservation performed by Prepare.
	h.addLaunchIDErr = errors.New("flow is closed")
	preparedMsg := prepareCmd()
	nextModel, handoffCmd := m.Update(preparedMsg)
	m = nextModel.(Model)
	if handoffCmd != nil {
		m = h.drain(m, handoffCmd, 0)
	}

	if len(h.launchUpdates) != 1 {
		t.Fatalf("launch reservations = %d, want one rejected reservation", len(h.launchUpdates))
	}
	if len(h.launchContexts) != 0 || len(h.agentContexts) != 0 {
		t.Fatalf("closed Flow started an agent: embedded=%d external=%d", len(h.launchContexts), len(h.agentContexts))
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("rejected prepare must release the launch attempt")
	}
	if got := m.status.Text; !strings.Contains(got, "closed") {
		t.Fatalf("status = %q, want closed rejection", got)
	}
}

func TestFlowLaunchCloseBeforeEmbeddedPersistenceStartsNoAgent(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()

	next, readCmd := m.handleLaunchNextFlowPhase()
	m = next.(Model)
	readMsg := readCmd()
	nextModel, prepareCmd := m.Update(readMsg)
	m = nextModel.(Model)
	// The close wins before the command can persist launch bookkeeping. The
	// reservation rejects the whole persistence-to-spawn operation atomically.
	h.reserveLaunchErr = errors.New("flow is closed")
	preparedMsg := prepareCmd()
	nextModel, handoffCmd := m.Update(preparedMsg)
	m = nextModel.(Model)
	if handoffCmd != nil {
		m = h.drain(m, handoffCmd, 0)
	}

	if len(h.launchContexts) != 0 {
		t.Fatalf("closed Flow started %d embedded agents after persistence, want zero", len(h.launchContexts))
	}
	if len(h.launchUpdates) != 0 {
		t.Fatalf("closed Flow recorded launch updates %#v, want none", h.launchUpdates)
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("rejected final spawn must release the launch attempt")
	}
	if got := m.status.Text; !strings.Contains(got, "closed") {
		t.Fatalf("status = %q, want final-spawn closed rejection", got)
	}
}

func TestFlowLaunchCloseBeforeExternalPersistenceStartsNoAgent(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.agentCommand = "codex-app"
	m := h.model()

	next, readCmd := m.handleLaunchNextFlowPhase()
	m = next.(Model)
	readMsg := readCmd()
	nextModel, prepareCmd := m.Update(readMsg)
	m = nextModel.(Model)
	h.reserveLaunchErr = errors.New("flow is closed")
	preparedMsg := prepareCmd()
	nextModel, handoffCmd := m.Update(preparedMsg)
	m = nextModel.(Model)
	if handoffCmd == nil {
		t.Fatal("external handoff should return a command that performs final admission")
	}
	m = h.drain(m, handoffCmd, 0)

	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("rejected external spawn must release the launch attempt")
	}
	if len(h.launchUpdates) != 0 || len(h.agentContexts) != 0 {
		t.Fatalf("closed Flow external launch state = updates %#v contexts %#v, want none", h.launchUpdates, h.agentContexts)
	}
	if got := m.status.Text; !strings.Contains(got, "closed") {
		t.Fatalf("status = %q, want final-spawn closed rejection", got)
	}
}

func TestFlowLaunchEmbeddedInstallTransfersOwnership(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	before := m.ListRequest(ui.ModeFlows)

	m = h.launch(m)

	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("the installed slot owns the Flow; no attempt may remain")
	}
	if len(m.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %d, want 1", len(m.embeddedTerminals))
	}
	slot := m.embeddedTerminals[0]
	if slot.Scope != embeddedTerminalScopeFlow || slot.FlowID != record.FlowID || slot.FlowPhaseID != record.Phases[0].PhaseID {
		t.Fatalf("slot = %#v, want the launched Flow phase", slot)
	}
	if !m.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatal("the slot must occupy the Flow the instant the attempt is dropped")
	}
	if len(h.launchUpdates) != 1 || len(h.launchContexts) != 1 {
		t.Fatalf("launch updates = %d, contexts = %d, want one each", len(h.launchUpdates), len(h.launchContexts))
	}
	ctx := h.launchContexts[0]
	if ctx.LaunchID == "" || ctx.LaunchID != h.launchUpdates[0].LaunchID || ctx.LaunchID != slot.LaunchID {
		t.Fatalf("launch ID diverged: ctx=%q update=%q slot=%q", ctx.LaunchID, h.launchUpdates[0].LaunchID, slot.LaunchID)
	}
	if !ctx.FlowLaunchTracked || !ctx.Embedded {
		t.Fatalf("embedded context = %#v, want tracked and embedded", ctx)
	}
	if m.ListRequest(ui.ModeFlows) == before {
		t.Fatal("a successful embedded launch should refresh the visible Flow surface")
	}
}

func TestFlowLaunchExternalRouteRetainsOwnershipUntilResult(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.agentCommand = "codex-app"
	m := h.model()

	m, cmd := h.press(m)
	m, cmd = h.step(m, cmd) // read -> preparing
	m, _ = h.step(m, cmd)   // prepared -> external handoff

	attempt, ok := m.flowLaunchAttempt(record.FlowID)
	if !ok || attempt.State != flowLaunchStateHandoffPending {
		t.Fatalf("attempt after external handoff = %#v ok=%v, want handoffPending", attempt, ok)
	}
	if len(h.agentContexts) != 1 {
		t.Fatalf("external launches = %d, want 1", len(h.agentContexts))
	}
	ctx := h.agentContexts[0]
	if ctx.Embedded || ctx.FlowLaunchTracked || ctx.Headless {
		t.Fatalf("external context = %#v, want embedded/tracked/headless unset", ctx)
	}
	if len(h.launchContexts) != 0 {
		t.Fatal("the external route must not open an embedded terminal")
	}
	if ctx.LaunchID != attempt.Token {
		t.Fatalf("external launch ID %q != attempt token %q", ctx.LaunchID, attempt.Token)
	}

	stale := ctx
	stale.LaunchID = "superseded"
	next, _ := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: stale, Detached: true}, nil)
	if _, ok := next.flowLaunchAttempt(record.FlowID); !ok {
		t.Fatal("a result for a superseded launch ID must not release the attempt")
	}

	done, _ := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: ctx, Detached: true}, nil)
	if _, ok := done.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("the matching result must release the attempt")
	}
}

func TestFlowLaunchExternalSynchronousErrorReleasesAttempt(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.agentCommand = "codex-app"
	h.launchAgentErr = errors.New("agent binary missing")

	m := h.launch(h.model())

	if attempt, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatalf("a failed external launch must release the Flow, got %#v", attempt)
	}
	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("phase updates = %#v, want one needs_attention update", h.phaseUpdates)
	}
	if got := m.status.Text; !strings.Contains(got, "agent binary missing") {
		t.Fatalf("status = %q, want the launch error", got)
	}
	// The Flow must be launchable again.
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	if _, _, ok := m.previewFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindManualPhase, FlowID: record.FlowID}); !ok {
		t.Fatal("the Flow should be launchable again after a released failure")
	}
}

func TestFlowLaunchFailureWithNothingToPersistReleasesImmediately(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	held, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:        "token-1",
		Kind:         flowLaunchKindManualPhase,
		FlowID:       record.FlowID,
		PhaseID:      record.Phases[0].PhaseID,
		MutatedPhase: true,
	}, flowLaunchStatePreparing)
	if !ok {
		t.Fatal("reservation should succeed")
	}
	attempt, _ := held.flowLaunchAttempt(record.FlowID)

	// A terminal phase is exactly the case flowLaunchFailureUpdate refuses: no
	// phase write happens, so no persistence message will ever arrive.
	next, cmd := held.failFlowLaunch(attempt, actions.AgentLaunchContext{
		FlowID:            record.FlowID,
		FlowPhaseID:       record.Phases[0].PhaseID,
		FlowLaunchTracked: true,
		FlowPhaseTerminal: true,
		LaunchID:          "token-1",
	}, record.RepoPath, "boom")

	if cmd != nil {
		t.Fatal("an unpersistable failure must not schedule persistence")
	}
	if _, ok := next.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("an unpersistable failure must release the attempt immediately")
	}
	if got := next.status.Text; got != "boom" {
		t.Fatalf("status = %q, want the launch error", got)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("nothing should have been persisted: %#v", h.phaseUpdates)
	}
}

func TestFlowLaunchCapacityFailurePersistsThenReleases(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	// nextEmbeddedTerminalNumber only counts slots, so empty ones fill the cap.
	m.embeddedTerminals = make([]embeddedTerminalSlot, 9)

	m = h.launch(m)

	if got := m.status.Text; !strings.Contains(got, "Maximum embedded terminals reached") {
		t.Fatalf("status = %q, want the capacity message", got)
	}
	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("phase updates = %#v, want one needs_attention update", h.phaseUpdates)
	}
	if _, ok := m.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("the attempt must be released once the failure is persisted")
	}
}

func TestFlowLaunchPrefillFailureNeverLeavesTheFlowUnowned(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.launch(h.model())
	if len(m.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %d, want 1", len(m.embeddedTerminals))
	}
	slot := m.embeddedTerminals[0]
	ctx := h.launchContexts[0]
	ctx.FlowLaunchTracked = true

	next, cmd := m.Update(embeddedPromptPrefillResultMsg{
		ID:            slot.ID,
		LaunchContext: ctx,
		Err:           errors.New("prefill failed"),
	})
	after := next.(Model)

	if after.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatal("the failed slot should have been dismissed")
	}
	attempt, ok := after.flowLaunchAttempt(record.FlowID)
	if !ok || attempt.State != flowLaunchStateFailurePersisting || attempt.Token != ctx.LaunchID {
		t.Fatalf("attempt after prefill failure = %#v ok=%v, want failurePersisting for this launch", attempt, ok)
	}
	if cmd == nil {
		t.Fatal("prefill failure should persist the phase status")
	}
	settled := h.drain(after, cmd, 0)
	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("phase updates = %#v, want one needs_attention update", h.phaseUpdates)
	}
	if _, ok := settled.flowLaunchAttempt(record.FlowID); ok {
		t.Fatal("the re-reserved attempt must be released once persistence lands")
	}
}

func TestFlowLaunchPrefillFailureYieldsToAnotherAttempt(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.launch(h.model())
	slot := m.embeddedTerminals[0]
	ctx := h.launchContexts[0]
	ctx.FlowLaunchTracked = true

	held, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:  "other-token",
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID,
	}, flowLaunchStateReading)
	if !ok {
		t.Fatal("the test needs a competing attempt")
	}

	next, _ := held.Update(embeddedPromptPrefillResultMsg{
		ID:            slot.ID,
		LaunchContext: ctx,
		Err:           errors.New("prefill failed"),
	})
	after := next.(Model)

	attempt, ok := after.flowLaunchAttempt(record.FlowID)
	if !ok || attempt.Token != "other-token" {
		t.Fatalf("competing attempt was replaced: %#v ok=%v", attempt, ok)
	}
	if after.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatal("the failed slot should still be dismissed")
	}
}

func TestFlowLaunchPreflightMessagesSurfaceVerbatim(t *testing.T) {
	planReviewWithoutPlan := manualLaunchFlowRecord()
	planReviewWithoutPlan.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Title: "Plan review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 1},
	}

	planPathFailure := manualLaunchFlowRecord()
	planPathFailure.PlanID = "plan-1"

	tests := []struct {
		name       string
		record     flowstore.FlowRecord
		planPath   func(string) (string, error)
		wantStatus string
	}{
		{
			name:       "plan review without a linked plan",
			record:     planReviewWithoutPlan,
			wantStatus: "Plan Review needs a linked plan before launch",
		},
		{
			name:       "plan markdown path error",
			record:     planPathFailure,
			planPath:   func(string) (string, error) { return "", errors.New("plan path unavailable") },
			wantStatus: "plan path unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, tc.record)
			opts := h.options()
			if tc.planPath != nil {
				opts.PlanMarkdownPath = tc.planPath
			}
			m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
			m.contentPane = ui.PaneBottom
			m.bottomMode = ui.ModeFlows
			m.flows = m.flows.SetItems([]flowstore.FlowRecord{tc.record})

			m = h.launch(m)

			if got := m.status.Text; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if len(h.launchUpdates) != 0 {
				t.Fatal("a preflight failure must not persist a launch ID")
			}
			if _, ok := m.flowLaunchAttempt(tc.record.FlowID); ok {
				t.Fatal("a preflight failure must release the attempt")
			}
		})
	}
}

func TestFlowLaunchSharedHandlersKeepWorkingWithoutAnAttempt(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	ctx := actions.AgentLaunchContext{
		FlowID:            record.FlowID,
		FlowPhaseID:       record.Phases[0].PhaseID,
		FlowLaunchTracked: true,
		LaunchID:          "untracked-launch",
	}

	persisted, cmd := m.handleFlowLaunchFailurePersisted(flowLaunchFailurePersistedMsg{LaunchContext: ctx, OriginalErr: "boom"})
	if persisted.status.Text != "boom" || cmd == nil {
		t.Fatalf("status = %q cmd = %v, want main's status and Flow surface refresh", persisted.status.Text, cmd != nil)
	}

	failed, failCmd := m.handleAgentResultAfterFinalization(AgentResultMsg{LaunchContext: ctx, Err: "agent crashed"}, nil)
	if failCmd == nil {
		t.Fatal("an agent failure without an attempt must still persist the phase status")
	}
	h.drain(failed, failCmd, 0)
	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("phase updates = %#v, want main's needs_attention update", h.phaseUpdates)
	}
}
