package model

import (
	"errors"
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

	agentCommand   string
	persistedFlows []flowstore.FlowRecord
	readErr        error
	sessionRecords []sessions.SessionRecord
	sessionsErr    error

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
	return flowstore.FlowRecord{}, errors.New("flow not found")
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
			if h.sessionsErr != nil {
				return nil, h.sessionsErr
			}
			return h.sessionRecords, nil
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return h.persistedFlow(strings.TrimSpace(flowID))
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
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, h.options())
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
