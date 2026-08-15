package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

func genericFlowAgentRecord(t *testing.T) flowstore.FlowRecord {
	t.Helper()
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: t.TempDir(),
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Status: flowstore.PhaseCompleted},
		},
	}
}

func TestGenericWorktreeAgentHintUsesCachedReadiness(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.activePane = m.contentPane
	m.width = 180
	if !m.selectedFlowWorktreeAgentReady() {
		t.Fatal("eligible cached Flow should advertise the generic agent")
	}
	if view := m.View(); !strings.Contains(view, "start agent") {
		t.Fatalf("eligible Flow did not render the generic shortcut:\n%s", view)
	}
	m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{FlowID: "  " + record.FlowID + "  ", Status: "active"}})
	if m.selectedFlowWorktreeAgentReady() {
		t.Fatal("canonical exact-Flow active session should withdraw generic agent readiness")
	}
	m.sessions = m.sessions.SetItems(nil)

	record.Phases[0].Status = flowstore.PhaseRunning
	h = newManualLaunchHarness(t, record)
	m = h.model()
	m.activePane = m.contentPane
	m.width = 180
	if m.selectedFlowWorktreeAgentReady() {
		t.Fatal("running phase should withdraw generic agent readiness")
	}
	if view := m.View(); strings.Contains(view, "start agent") {
		t.Fatalf("occupied Flow rendered the generic shortcut:\n%s", view)
	}
}

func TestGenericWorktreeAgentReceiptlessPreparationDoesNotAdvertiseOrStart(t *testing.T) {
	record := genericFlowAgentRecord(t)
	record.PreparationNonce = "nonce-unprepared"
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.activePane = m.contentPane
	m.width = 180

	if m.selectedFlowWorktreeAgentReady() {
		t.Fatal("receipt-less preparation should not advertise the generic agent")
	}
	if view := m.View(); strings.Contains(view, "start agent") {
		t.Fatalf("receipt-less preparation rendered the generic shortcut:\n%s", view)
	}
	next, cmd := m.handleStartSelectedFlowWorktreeAgent()
	if cmd != nil {
		t.Fatal("receipt-less preparation started the generic-agent lifecycle")
	}
	if next.(Model).flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("receipt-less preparation retained a generic-agent lifecycle attempt")
	}
}

func TestGenericWorktreeAgentClosedFlowDoesNotAdvertiseOrStart(t *testing.T) {
	record := genericFlowAgentRecord(t)
	closedAt := time.Now()
	record.Closed = flowstore.Closure{Reason: "completed", ClosedAt: &closedAt}
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.activePane = m.contentPane
	m.width = 180

	if m.selectedFlowWorktreeAgentReady() {
		t.Fatal("closed Flow should not advertise the generic agent")
	}
	if view := m.View(); strings.Contains(view, "start agent") {
		t.Fatalf("closed Flow rendered the generic shortcut:\n%s", view)
	}
	next, cmd := m.handleStartSelectedFlowWorktreeAgent()
	if cmd != nil {
		t.Fatal("closed Flow started the generic-agent lifecycle")
	}
	if next.(Model).flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("closed Flow retained a generic-agent lifecycle attempt")
	}
}

func TestGenericWorktreeAgentAuthoritativeReadRejectsNewlyClosedFlow(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	next, readCmd := h.model().handleStartSelectedFlowWorktreeAgent()
	m := next.(Model)

	closedAt := time.Now()
	h.record.Closed = flowstore.Closure{Reason: "completed", ClosedAt: &closedAt}
	readMsg, ok := runCommandWithoutWaiting(readCmd)
	if !ok {
		t.Fatal("authoritative read did not settle")
	}
	next, cmd := m.Update(readMsg)
	m = next.(Model)

	if m.status.Text != flowWorktreeAgentStaleStatus {
		t.Fatalf("status = %q, want %q", m.status.Text, flowWorktreeAgentStaleStatus)
	}
	m = h.drain(m, cmd, 0)
	if h.sessionListCalls != 0 || h.launchReservations != 0 || len(h.launchContexts) != 0 {
		t.Fatalf("closed authoritative Flow reached later work: session reads=%d reservations=%d launches=%#v", h.sessionListCalls, h.launchReservations, h.launchContexts)
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("closed authoritative Flow retained a generic-agent lifecycle attempt")
	}
}

func TestGenericWorktreeAgentPressTimeRefusalsUseGenericContract(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	filePath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		command string
		mutate  func(*flowstore.FlowRecord, *Model)
		status  string
	}{
		{name: "unconfigured", command: "__empty__", status: flowWorktreeAgentChooseStatus},
		{name: "retired codex app", command: "codex-app", status: `Flow worktree agents do not support agent "codex-app"; press A to choose codex or claude`},
		{name: "unsupported", command: "other", status: `Flow worktree agents do not support agent "other"; press A to choose codex or claude`},
		{name: "blank worktree", mutate: func(r *flowstore.FlowRecord, _ *Model) { r.WorktreePath = "" }, status: flowWorktreeAgentPathStatus},
		{name: "missing worktree", mutate: func(r *flowstore.FlowRecord, _ *Model) { r.WorktreePath = missing }, status: flowWorktreeAgentPathStatus},
		{name: "nondirectory worktree", mutate: func(r *flowstore.FlowRecord, _ *Model) { r.WorktreePath = filePath }, status: flowWorktreeAgentPathStatus},
		{name: "known active session", mutate: func(r *flowstore.FlowRecord, m *Model) {
			m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{FlowID: r.FlowID, Status: "active"}})
		}, status: flowWorktreeAgentSessionStatus},
		{name: "running phase", mutate: func(r *flowstore.FlowRecord, _ *Model) {
			r.Phases[0].Status = flowstore.PhaseRunning
		}, status: "a running phase implementation already occupies this Flow"},
		{name: "retained terminal", mutate: func(r *flowstore.FlowRecord, m *Model) {
			m.embeddedTerminals = []embeddedTerminalSlot{{Scope: embeddedTerminalScopeFlow, FlowID: r.FlowID, Terminal: internalFakeEmbeddedTerminal{}}}
		}, status: flowWorktreeAgentSlotStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := genericFlowAgentRecord(t)
			h := newManualLaunchHarness(t, record)
			h.inspectFunc = inspectWorktreeDirectory
			m := h.model()
			if tc.command == "__empty__" {
				m.agentCommand = ""
			} else if tc.command != "" {
				m.agentCommand = tc.command
			}
			if tc.mutate != nil {
				tc.mutate(&record, &m)
				m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
			}
			next, cmd := m.handleStartSelectedFlowWorktreeAgent()
			if cmd != nil {
				t.Fatal("refusal queued lifecycle work")
			}
			if got := next.(Model).status.Text; got != tc.status {
				t.Fatalf("status = %q, want %q", got, tc.status)
			}
		})
	}
}

func TestGenericWorktreeAgentRejectsLiveAutofixTmuxOccupancyAtBothBoundaries(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "admission", true: "protected install"}[protected], func(t *testing.T) {
			record := genericFlowAgentRecord(t)
			h := newManualLaunchHarness(t, record)
			h.launchBackend = config.LaunchBackendTmux
			h.tmuxAvailable = true
			h.windowLive = func(_ string, launchIDs []string) bool {
				return len(launchIDs) == 1 && launchIDs[0] == "autofix-live"
			}
			m := h.model()

			if protected {
				next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
				m = next.(Model)
				readMsg, _ := runCommandWithoutWaiting(readCmd)
				next, prepareCmd := m.Update(readMsg)
				m = next.(Model)
				preparedMsg, _ := runCommandWithoutWaiting(prepareCmd)
				m = m.withFlowAutofixTmuxLaunch(record.FlowID, "autofix-live")
				next, _ = m.Update(preparedMsg)
				m = next.(Model)
			} else {
				m = m.withFlowAutofixTmuxLaunch(record.FlowID, "autofix-live")
				next, cmd := m.handleStartSelectedFlowWorktreeAgent()
				m = next.(Model)
				if cmd != nil {
					t.Fatal("live autofix tmux occupancy reached the authoritative read")
				}
			}

			if m.status.Text != flowWorktreeAgentPendingStatus || len(h.launchContexts) != 0 {
				t.Fatalf("live autofix refusal = %q, launches=%#v", m.status.Text, h.launchContexts)
			}
			if m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatal("live autofix refusal retained the generic lifecycle attempt")
			}
			if len(h.tmuxWindowProbes) != 1 || len(h.tmuxWindowProbes[0]) != 1 || h.tmuxWindowProbes[0][0] != "autofix-live" {
				t.Fatalf("tmux probes = %#v, want the exact autofix launch", h.tmuxWindowProbes)
			}
			if protected && (h.launchReservations != 1 || h.launchReleases != 1) {
				t.Fatalf("reservation/release = %d/%d", h.launchReservations, h.launchReleases)
			}
		})
	}
}

func TestGenericWorktreeAgentSKeyUsesBothFlowSurfaces(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "flows", true: "active flows"}[active], func(t *testing.T) {
			record := genericFlowAgentRecord(t)
			h := newManualLaunchHarness(t, record)
			h.inspectFunc = inspectWorktreeDirectory
			m := h.model()
			m.activePane = m.contentPane
			if active {
				m.activeFlowSurface = true
				m.activeFlows = m.activeFlows.SetItems([]flowstore.FlowRecord{record})
			}

			next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			h.drain(next.(Model), cmd, 0)
			if len(h.launchContexts) != 1 || h.launchContexts[0].FlowID != record.FlowID {
				t.Fatalf("s launch contexts = %#v", h.launchContexts)
			}
		})
	}
}

func TestGenericWorktreeAgentLaunchesEmbeddedWithoutPhaseMutation(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)

	next, cmd := h.model().handleStartSelectedFlowWorktreeAgent()
	m := h.drain(next.(Model), cmd, 0)

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if ctx.Command != agent.CommandCodex || ctx.FlowID != record.FlowID ||
		ctx.FlowPhaseID != "" || ctx.FlowLaunchTracked || ctx.FlowRepair ||
		!ctx.FlowAgent || !ctx.Embedded || ctx.Headless || ctx.InitialPrompt != "" ||
		ctx.RepoPath != record.RepoPath || ctx.WorktreePath != record.WorktreePath ||
		ctx.WorkingDir != record.WorktreePath || ctx.Branch != record.Branch ||
		ctx.Commit != record.Commit || ctx.PlanID != record.PlanID || ctx.PlanPath != record.PlanPath {
		t.Fatalf("generic launch context = %#v", ctx)
	}
	if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
		t.Fatalf("generic launch mutated phase history: launches=%#v phases=%#v", h.launchUpdates, h.phaseUpdates)
	}
	if len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].FlowID != record.FlowID ||
		m.embeddedTerminals[0].FlowPhaseID != "" || m.embeddedTerminals[0].Identity != "agent" ||
		m.embeddedTerminals[0].PrefillPending {
		t.Fatalf("generic retained terminals = %#v", m.embeddedTerminals)
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("retained terminal did not replace lifecycle attempt ownership")
	}
}

func TestGenericWorktreeAgentIgnoresHeadlessAndTmuxRouting(t *testing.T) {
	record := genericFlowAgentRecord(t)
	record.Headless = true
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	next, cmd := h.model().handleStartSelectedFlowWorktreeAgent()
	h.drain(next.(Model), cmd, 0)

	if len(h.launchContexts) != 1 || len(h.tmuxContexts) != 0 || len(h.agentContexts) != 0 {
		t.Fatalf("generic routes: embedded=%#v tmux=%#v external=%#v", h.launchContexts, h.tmuxContexts, h.agentContexts)
	}
	ctx := h.launchContexts[0]
	if !ctx.Embedded || ctx.Headless || ctx.InitialPrompt != "" {
		t.Fatalf("generic embedded policy = %#v", ctx)
	}
}

func TestGenericWorktreeAgentPreservesTerminalFocusAfterNavigationDuringPreparation(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.width = 160
	m.height = 24
	m.terminalDockVisible = false
	m.terminalFocus = terminalFocusList

	next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	readMsg, _ := runCommandWithoutWaiting(readCmd)
	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	preparedMsg, _ := runCommandWithoutWaiting(prepareCmd)
	m = modelWithModeForTest(m, ui.ModeWorktrees)
	next, _ = m.Update(preparedMsg)
	m = next.(Model)

	if len(h.launchContexts) != 1 || !m.terminalDockVisible || m.terminalFocus != terminalFocusTerminal {
		t.Fatalf("navigated generic focus: launches=%#v visible=%v focus=%v", h.launchContexts, m.terminalDockVisible, m.terminalFocus)
	}
}

func TestGenericWorktreeAgentRetainedSlotRejectsDetachBeforeCallingTerminal(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	next, cmd := h.model().handleStartSelectedFlowWorktreeAgent()
	m := h.drain(next.(Model), cmd, 0)

	terminal := &internalFakeDetachableEmbeddedTerminal{target: "tmux attach -t generic"}
	m.embeddedTerminals[0].Terminal = terminal
	m.activeTerminalNum = m.embeddedTerminals[0].Number
	if !m.embeddedTerminals[0].FlowAgent || m.embeddedTerminals[0].DetachPolicy != embeddedTerminalDetachNever {
		t.Fatalf("generic slot policy = %#v", m.embeddedTerminals[0])
	}

	nextModel, detachCmd := m.handleEmbeddedTerminalDetachPrefix()
	if detachCmd != nil || terminal.detached {
		t.Fatalf("nondetachable generic slot detached: cmd=%v detached=%v", detachCmd != nil, terminal.detached)
	}
	if len(nextModel.embeddedTerminals) != 1 || nextModel.embeddedTerminals[0].FlowID != record.FlowID {
		t.Fatalf("detach refusal changed retained ownership: %#v", nextModel.embeddedTerminals)
	}
}

func TestGenericWorktreeAgentExitedSlotRetainsOccupancyUntilDismissed(t *testing.T) {
	record := genericFlowAgentRecord(t)
	m := Model{embeddedTerminals: []embeddedTerminalSlot{
		{
			Number:    1,
			ID:        1,
			Scope:     embeddedTerminalScopeFlow,
			FlowID:    record.FlowID,
			FlowAgent: true,
			Terminal:  internalFakeEmbeddedTerminal{state: "exited"},
		},
	}}

	if m.hasExitedFlowEmbeddedTerminalAutoClose() {
		t.Fatal("exited generic Flow-agent slot should not schedule automatic dismissal")
	}
	m = m.dismissExitedFlowEmbeddedTerminals()
	if len(m.embeddedTerminals) != 1 || !m.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatalf("exited generic slot lost retained occupancy: %#v", m.embeddedTerminals)
	}

	m = m.dismissEmbeddedTerminal(m.embeddedTerminals[0].ID)
	if len(m.embeddedTerminals) != 0 || m.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatalf("explicit dismissal did not release generic occupancy: %#v", m.embeddedTerminals)
	}
}

func TestGenericWorktreeAgentUsesExactFlowIdentityAcrossBothReads(t *testing.T) {
	record := genericFlowAgentRecord(t)
	record.FlowID = "flow-10"
	h := newManualLaunchHarness(t, record)
	h.sessionRecords = []sessions.SessionRecord{{FlowID: "flow-1", Status: "active", SessionID: "prefix-session"}}
	m := h.model()
	cached := record
	cached.FlowID = "  flow-10  "
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{cached})
	var readIDs, sessionIDs []string
	read := m.launchSeams.ReadFlow
	list := m.launchSeams.ListFlowSessions
	m.launchSeams.ReadFlow = func(flowID string) (flowstore.FlowRecord, error) {
		readIDs = append(readIDs, flowID)
		return read(flowID)
	}
	m.launchSeams.ListFlowSessions = func(flowID string) ([]sessions.SessionRecord, error) {
		sessionIDs = append(sessionIDs, flowID)
		return list(flowID)
	}

	next, cmd := m.handleStartSelectedFlowWorktreeAgent()
	h.drain(next.(Model), cmd, 0)

	if len(h.launchContexts) != 1 || h.launchContexts[0].FlowID != "flow-10" {
		t.Fatalf("exact Flow launch = %#v", h.launchContexts)
	}
	if len(readIDs) != 1 || readIDs[0] != "flow-10" {
		t.Fatalf("authoritative read IDs = %#v", readIDs)
	}
	if len(sessionIDs) != 2 || sessionIDs[0] != "flow-10" || sessionIDs[1] != "flow-10" {
		t.Fatalf("session read IDs = %#v, want two exact flow-10 reads", sessionIDs)
	}
}

func TestGenericWorktreeAgentUsesFinalMetadataAndPreparationSettings(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	planReads := 0
	m.launchSeams.ReadPlan = func(string) (string, error) {
		planReads++
		return "body", nil
	}
	resolvedPlans := []string{}
	m.launchSeams.PlanMarkdownPath = func(planID string) (string, error) {
		resolvedPlans = append(resolvedPlans, planID)
		return "/resolved/" + planID + "/plan.md", nil
	}

	next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	readMsg, ok := runCommandWithoutWaiting(readCmd)
	if !ok {
		t.Fatal("authoritative read did not settle")
	}
	final := record
	final.RepoPath = "/dev/final"
	final.WorktreePath = t.TempDir()
	final.Branch = "flow/final"
	final.Commit = "final-commit"
	final.PlanID = "plan-final"
	final.PlanPath = ""
	h.record = final

	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	m.codexModel = "gpt-final"
	m.codexReasoningEffort = "high"
	preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
	if !ok {
		t.Fatal("protected preparation did not settle")
	}
	m = h.drainMsg(m, preparedMsg, 0)

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if ctx.RepoPath != final.RepoPath || ctx.WorktreePath != final.WorktreePath || ctx.WorkingDir != final.WorktreePath ||
		ctx.Branch != final.Branch || ctx.Commit != final.Commit || ctx.PlanID != final.PlanID ||
		ctx.PlanPath != "/resolved/plan-final/plan.md" || ctx.Model != "gpt-final" || ctx.ReasoningEffort != "high" {
		t.Fatalf("final launch context = %#v", ctx)
	}
	if planReads != 0 || len(resolvedPlans) != 1 || resolvedPlans[0] != "plan-final" {
		t.Fatalf("plan body reads = %d, path resolutions = %#v", planReads, resolvedPlans)
	}
}

func TestGenericWorktreeAgentCancelsWhenCommandChangesBeforePreparation(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	readMsg, ok := runCommandWithoutWaiting(readCmd)
	if !ok {
		t.Fatal("authoritative read did not settle")
	}
	m.agentCommand = agent.CommandClaude

	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	m = h.drain(m, prepareCmd, 0)
	if len(h.launchContexts) != 0 {
		t.Fatal("changed command reached preparation")
	}
	if m.status.Text != flowWorktreeAgentChangedStatus || m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatalf("changed-command refusal = %q, attempt occupied=%v", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID))
	}
}

func TestGenericWorktreeAgentCancelsWhenCommandChangesAfterReadIsAccepted(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	readMsg, ok := runCommandWithoutWaiting(readCmd)
	if !ok {
		t.Fatal("authoritative read did not settle")
	}

	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	m.agentCommand = agent.CommandClaude
	preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
	if !ok {
		t.Fatal("protected preparation did not settle")
	}
	m = h.drainMsg(m, preparedMsg, 0)

	if len(h.launchContexts) != 0 {
		t.Fatal("changed command reached terminal installation")
	}
	if m.status.Text != flowWorktreeAgentChangedStatus || m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatalf("changed-command refusal = %q, attempt occupied=%v", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID))
	}
	if h.launchReservations != 1 || h.launchReleases != 1 {
		t.Fatalf("reservation/release = %d/%d", h.launchReservations, h.launchReleases)
	}
}

func TestGenericWorktreeAgentProtectedRevalidationRejectsFreshOccupancyAndPaths(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*manualLaunchHarness)
		status string
	}{
		{name: "phase starts running", mutate: func(h *manualLaunchHarness) {
			h.record.Phases[0].Status = flowstore.PhaseRunning
		}, status: "a running phase implementation already occupies this Flow"},
		{name: "active session appears", mutate: func(h *manualLaunchHarness) {
			h.sessionRecords = []sessions.SessionRecord{{FlowID: h.record.FlowID, Status: "active", SessionID: "late-session"}}
		}, status: flowWorktreeAgentSessionStatus},
		{name: "worktree path disappears", mutate: func(h *manualLaunchHarness) {
			h.record.WorktreePath = filepath.Join(t.TempDir(), "gone")
		}, status: flowWorktreeAgentPathStatus},
		{name: "worktree becomes a file", mutate: func(h *manualLaunchHarness) {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
				t.Fatal(err)
			}
			h.record.WorktreePath = path
		}, status: flowWorktreeAgentPathStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := genericFlowAgentRecord(t)
			h := newManualLaunchHarness(t, record)
			h.inspectFunc = inspectWorktreeDirectory
			m := h.model()
			next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
			m = next.(Model)
			readMsg, ok := runCommandWithoutWaiting(readCmd)
			if !ok {
				t.Fatal("authoritative read did not settle")
			}
			next, prepareCmd := m.Update(readMsg)
			m = next.(Model)
			tc.mutate(h)
			preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
			if !ok {
				t.Fatal("protected preparation did not settle")
			}
			m = h.drainMsg(m, preparedMsg, 0)

			if len(h.launchContexts) != 0 || len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatalf("refused final state launched or mutated: contexts=%#v launches=%#v phases=%#v", h.launchContexts, h.launchUpdates, h.phaseUpdates)
			}
			if m.status.Text != tc.status || m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatalf("status = %q, occupied=%v, want %q and released", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID), tc.status)
			}
			if h.launchReservations != 1 || h.launchReleases != 1 {
				t.Fatalf("reservation/release = %d/%d", h.launchReservations, h.launchReleases)
			}
		})
	}
}

func TestGenericWorktreeAgentFinalTerminalBackstopPreservesCompetingOwner(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	readMsg, _ := runCommandWithoutWaiting(readCmd)
	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	preparedMsg, _ := runCommandWithoutWaiting(prepareCmd)
	competing := embeddedTerminalSlot{Number: 7, Scope: embeddedTerminalScopeFlow, FlowID: record.FlowID, Terminal: internalFakeEmbeddedTerminal{}}
	m.embeddedTerminals = append(m.embeddedTerminals, competing)
	m = h.drainMsg(m, preparedMsg, 0)

	if len(h.launchContexts) != 0 || len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].Number != competing.Number {
		t.Fatalf("terminal backstop changed owner: launches=%#v terminals=%#v", h.launchContexts, m.embeddedTerminals)
	}
	if m.status.Text != flowWorktreeAgentSlotStatus || m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatalf("backstop status = %q, occupied attempt=%v", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID))
	}
}

func TestGenericWorktreeAgentInstallFailuresAreStatusOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setUp  func(*manualLaunchHarness, *Model)
		status string
	}{
		{name: "terminal start", setUp: func(h *manualLaunchHarness, _ *Model) { h.startTerminalErr = errors.New("terminal boom") }, status: "terminal boom"},
		{name: "capacity", setUp: func(_ *manualLaunchHarness, m *Model) {
			for i := 1; i <= 9; i++ {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{Number: i, Scope: embeddedTerminalScopeSession, Terminal: internalFakeEmbeddedTerminal{}})
			}
		}, status: "Maximum embedded terminals reached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := genericFlowAgentRecord(t)
			h := newManualLaunchHarness(t, record)
			m := h.model()
			tc.setUp(h, &m)
			next, cmd := m.handleStartSelectedFlowWorktreeAgent()
			m = h.drain(next.(Model), cmd, 0)
			if m.status.Text != tc.status || m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatalf("status = %q, occupied=%v", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID))
			}
			if len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatalf("install failure mutated phase history: %#v %#v", h.launchUpdates, h.phaseUpdates)
			}
		})
	}
}

func TestGenericWorktreeAgentPlanPathResolutionFailuresReleaseOnlyItsAttempt(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(map[bool]string{false: "first read", true: "protected read"}[final], func(t *testing.T) {
			record := genericFlowAgentRecord(t)
			record.PlanPath = ""
			h := newManualLaunchHarness(t, record)
			m := h.model()
			m.launchSeams.PlanMarkdownPath = func(planID string) (string, error) {
				if !final || planID == "plan-final" {
					return "", errors.New("resolve plan path boom")
				}
				return "/resolved/" + planID + "/plan.md", nil
			}
			next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
			m = next.(Model)
			readMsg, _ := runCommandWithoutWaiting(readCmd)
			if final {
				next, prepareCmd := m.Update(readMsg)
				m = next.(Model)
				h.record.PlanID = "plan-final"
				preparedMsg, _ := runCommandWithoutWaiting(prepareCmd)
				m = h.drainMsg(m, preparedMsg, 0)
			} else {
				m = h.drainMsg(m, readMsg, 0)
			}
			if m.status.Text != "resolve plan path boom" || m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatalf("plan-path refusal = %q, occupied=%v", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID))
			}
			if len(h.launchContexts) != 0 || len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
				t.Fatalf("plan-path refusal launched or mutated: %#v %#v %#v", h.launchContexts, h.launchUpdates, h.phaseUpdates)
			}
		})
	}
}

func TestGenericWorktreeAgentProtectedFailureSurfacesSynchronouslyWithoutFollowup(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.launchSeams.PlanMarkdownPath = func(planID string) (string, error) {
		return "", errors.New("protected plan path boom")
	}

	next, readCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	readMsg, ok := runCommandWithoutWaiting(readCmd)
	if !ok {
		t.Fatal("authoritative read did not settle")
	}
	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	h.record.PlanID = "plan-final"
	h.record.PlanPath = ""
	preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
	if !ok {
		t.Fatal("protected preparation did not settle")
	}
	preparedEvent, ok := preparedMsg.(flowLaunchEventMsg)
	if !ok {
		t.Fatalf("protected preparation result = %T, want flowLaunchEventMsg", preparedMsg)
	}
	// The failure must remain visible even if the user leaves both Flow
	// surfaces while the protected preparation command is in flight.
	m = modelWithModeForTest(m, ui.ModeWorktrees)
	m, followup := m.handleFlowLaunchEvent(preparedEvent)

	if followup != nil {
		t.Fatal("protected generic failure queued an unfenced follow-up message")
	}
	if m.status.Text != "protected plan path boom" || m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatalf("protected failure = %q, occupied=%v", m.status.Text, m.flowLaunchAttemptOccupied(record.FlowID))
	}
	if len(h.launchContexts) != 0 || len(h.launchUpdates) != 0 || len(h.phaseUpdates) != 0 {
		t.Fatalf("protected failure launched or mutated: %#v %#v %#v", h.launchContexts, h.launchUpdates, h.phaseUpdates)
	}
}

func TestGenericWorktreeAgentStaleAndCrossKindEventsAreNoOps(t *testing.T) {
	record := genericFlowAgentRecord(t)
	h := newManualLaunchHarness(t, record)
	m := h.model()
	next, _ := m.handleStartSelectedFlowWorktreeAgent()
	m = next.(Model)
	attempt, ok := m.flowLaunchAttempt(record.FlowID)
	if !ok {
		t.Fatal("generic admission did not reserve the Flow")
	}

	for _, event := range []flowLaunchEventMsg{
		{Token: "stale-token", Kind: flowLaunchKindWorktreeAgent, From: flowLaunchStateReading, FlowID: record.FlowID, Stage: flowLaunchStageRead, Err: "stale"},
		{Token: attempt.Token, Kind: flowLaunchKindAutofix, From: flowLaunchStateReading, FlowID: record.FlowID, Stage: flowLaunchStageRead, Err: "cross-kind"},
	} {
		before := m
		got, cmd := m.handleFlowLaunchEvent(event)
		if cmd != nil || got.status.Text != before.status.Text {
			t.Fatalf("stale event changed status or queued work: %#v", event)
		}
		current, held := got.flowLaunchAttempt(record.FlowID)
		if !held || current.Token != attempt.Token || current.Kind != flowLaunchKindWorktreeAgent {
			t.Fatalf("stale event changed owner: %#v", current)
		}
	}
}
