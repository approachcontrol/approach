package model

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// autofixFlowRecord is the merge-eligible, PR-bearing, worktree-backed Flow the
// U shortcut exists for. Every ineligibility test below starts from it and
// removes exactly one condition, so the gate's rules stay individually pinned.
func autofixFlowRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha/worktrees/flow-1",
		Branch:       "flow/one",
		Commit:       "abc123",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     116,
			URL:        "https://github.com/approachcontrol/approach/pull/116",
			HeadBranch: "flow/one",
			BaseBranch: "main",
			Status:     "open",
		},
		Merge: flowstore.Merge{Status: flowstore.MergePending},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseCompleted, Outcome: "passed"},
			{PhaseID: "merge", Title: "Merge", Status: flowstore.PhaseReady},
		},
	}
}

func TestAutofixPromptForPRIsTheBeadsLiteralText(t *testing.T) {
	if got := autofixPromptForPR(116); got != "autofix pr #116" {
		t.Fatalf("autofix prompt = %q, want the bead's literal text", got)
	}
	if got := autofixPromptForPR(7); got != "autofix pr #7" {
		t.Fatalf("autofix prompt = %q", got)
	}
}

func TestAutofixUsesItsOwnLifecycleIntentKind(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)

	next, cmd := h.model().handleAutofixSelectedFlowPR()
	if cmd == nil {
		t.Fatal("U did not submit an autofix lifecycle intent")
	}
	attempt, ok := next.(Model).flowLaunchAttempt(record.FlowID)
	if !ok {
		t.Fatal("U did not reserve the selected Flow")
	}
	if attempt.Kind != flowLaunchKindAutofix {
		t.Fatalf("U intent kind = %v, want autofix", attempt.Kind)
	}
}

func TestSelectedFlowAutofixTargetGate(t *testing.T) {
	mergeCompleted := func(record flowstore.FlowRecord, mergeStatus string) flowstore.FlowRecord {
		phases := append([]flowstore.FlowPhase{}, record.Phases...)
		phases[len(phases)-1].Status = flowstore.PhaseCompleted
		record.Phases = phases
		record.Merge = flowstore.Merge{Status: mergeStatus}
		return record
	}
	closedAt := time.Now()

	cases := []struct {
		name    string
		record  func(flowstore.FlowRecord) flowstore.FlowRecord
		prepare func(Model) Model
		want    bool
	}{
		{
			name: "merge phase ready",
			want: true,
		},
		{
			name: "merge completed with pending merge",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				return mergeCompleted(record, flowstore.MergePending)
			},
			want: true,
		},
		{
			name: "merge completed with merged merge",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				return mergeCompleted(record, flowstore.MergeMerged)
			},
			want: true,
		},
		{
			name: "no PR target",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.PR = flowstore.PullRequest{}
				return record
			},
		},
		{
			name: "closed Flow",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Closed = flowstore.Closure{Reason: "superseded", ClosedAt: &closedAt}
				return record
			},
		},
		{
			name: "already merged Flow",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Status = flowstore.StatusMerged
				return record
			},
		},
		{
			name: "merge phase not ready",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				phases := append([]flowstore.FlowPhase{}, record.Phases...)
				phases[len(phases)-1].Status = flowstore.PhaseBlocked
				record.Phases = phases
				return record
			},
		},
		{
			name: "merge predecessors unsatisfied",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				phases := append([]flowstore.FlowPhase{}, record.Phases...)
				for i := range phases {
					if phases[i].PhaseID == "autoreview" {
						phases[i].Status = flowstore.PhaseReady
						phases[i].Outcome = ""
					}
				}
				record.Phases = phases
				return record
			},
		},
		{
			name: "no worktree path",
			record: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.WorktreePath = ""
				return record
			},
		},
		{
			name: "phase row selected",
			prepare: func(m Model) Model {
				m.expandedFlowID = "flow-1"
				m.selectedFlowPhaseID = "merge"
				return m
			},
		},
		{
			name: "Flow surface not visible",
			prepare: func(m Model) Model {
				m.bottomMode = 0
				return m
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := autofixFlowRecord()
			if tc.record != nil {
				record = tc.record(record)
			}
			h := newManualLaunchHarness(t, record)
			m := h.model()
			if tc.prepare != nil {
				m = tc.prepare(m)
			}
			got, repoPath, ok := m.selectedFlowAutofixTarget()
			if ok != tc.want {
				t.Fatalf("selectedFlowAutofixTarget ok = %v, want %v", ok, tc.want)
			}
			if !tc.want {
				return
			}
			if got.FlowID != record.FlowID {
				t.Fatalf("target Flow = %q, want %q", got.FlowID, record.FlowID)
			}
			if repoPath != record.RepoPath {
				t.Fatalf("target repo path = %q, want %q", repoPath, record.RepoPath)
			}
		})
	}
}

// autofixOccupancyCase names one way a Flow can already be owned, the status
// admission must answer with, and how to install it. The footer predicate and
// admission are asserted from the same table because their agreement is the
// property that keeps U from being advertised while the key refuses it.
type autofixOccupancyCase struct {
	name    string
	install func(Model, flowstore.FlowRecord) Model
	status  string
}

func autofixOccupancyCases() []autofixOccupancyCase {
	return []autofixOccupancyCase{
		{
			name: "lifecycle attempt holds the Flow",
			install: func(m Model, record flowstore.FlowRecord) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:  "attempt-token",
					Kind:   flowLaunchKindManualPhase,
					FlowID: record.FlowID,
				}, flowLaunchStateReading)
				return next
			},
			status: flowAutofixInFlightStatus,
		},
		{
			name: "phase resume holds the Flow",
			install: func(m Model, record flowstore.FlowRecord) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:  "resume-token",
					Kind:   flowLaunchKindPhaseResume,
					FlowID: record.FlowID,
				}, flowLaunchStateReading)
				return next
			},
			status: "A phase resume is already pending for this Flow",
		},
		{
			name: "embedded Flow terminal is retained",
			install: func(m Model, record flowstore.FlowRecord) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:    embeddedTerminalScopeFlow,
					FlowID:   record.FlowID,
					Terminal: flowPhaseLaunchTestTerminal{state: "running"},
				})
				return m
			},
			status: flowAutofixTerminalStatus,
		},
		{
			name: "repair terminal is retained",
			install: func(m Model, record flowstore.FlowRecord) Model {
				m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
					Scope:      embeddedTerminalScopeFlow,
					FlowID:     record.FlowID,
					FlowRepair: true,
					Terminal:   flowPhaseLaunchTestTerminal{state: "running"},
				})
				return m
			},
			status: flowAutofixTerminalStatus,
		},
		{
			name: "repair launch is pending",
			install: func(m Model, record flowstore.FlowRecord) Model {
				next, _ := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
					Token:  "repair-launch",
					Kind:   flowLaunchKindRepair,
					FlowID: record.FlowID,
				}, flowLaunchStateReading)
				return next
			},
			status: flowRepairPendingStatus,
		},
		{
			name: "headless write is in flight",
			install: func(m Model, record flowstore.FlowRecord) Model {
				return m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
					flowID:   record.FlowID,
					repoPath: record.RepoPath,
					enabled:  true,
				})
			},
			status: flowHeadlessWritePendingStatus,
		},
	}
}

func TestSelectedFlowAutofixReadyWithdrawsForEveryOccupancySignal(t *testing.T) {
	record := autofixFlowRecord()
	for _, tc := range autofixOccupancyCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			base := h.model()
			if !base.selectedFlowAutofixReady() {
				t.Fatal("fixture should advertise autofix before anything holds the Flow")
			}
			occupied := tc.install(base, record)
			if occupied.selectedFlowAutofixReady() {
				t.Fatalf("footer must withdraw U while %s", tc.name)
			}
			// The m hint deliberately stays: occupancy is not part of its gate,
			// so U and m are not advertised identically.
			if !occupied.selectedFlowManualMergeReady() {
				t.Fatal("occupancy must not withdraw the mark-merged hint")
			}
		})
	}
}

func TestAutofixAdmissionRefusesEveryOccupancySignal(t *testing.T) {
	record := autofixFlowRecord()
	for _, tc := range autofixOccupancyCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			m := tc.install(h.model(), record)
			next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindAutofix,
				FlowID: record.FlowID,
			})
			if admitted || cmd != nil {
				t.Fatalf("admitted = %v, cmd != nil = %v, want a refusal that starts no work", admitted, cmd != nil)
			}
			if got := next.status.Text; got != tc.status {
				t.Fatalf("status = %q, want %q", got, tc.status)
			}
		})
	}
}

func TestAutofixAdmissionRefusesReceiptlessPreparationSilently(t *testing.T) {
	record := autofixFlowRecord()
	record.PreparationNonce = "nonce-unprepared"
	h := newManualLaunchHarness(t, record)
	m := h.model()

	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindAutofix,
		FlowID: record.FlowID,
	})
	if admitted || cmd != nil {
		t.Fatalf("admitted = %v, cmd != nil = %v, want a silent refusal", admitted, cmd != nil)
	}
	if got := next.status.Text; got != "" {
		t.Fatalf("status = %q, want silence: the hint is absent and the key inert", got)
	}
	if next.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("a refused admission must reserve nothing")
	}
	if next.selectedFlowAutofixReady() {
		t.Fatal("receipt-less preparation should not advertise autofix")
	}
}

func TestAutofixAdmissionRefusesTheRecordShapedGateSilently(t *testing.T) {
	record := autofixFlowRecord()
	record.WorktreePath = ""
	h := newManualLaunchHarness(t, record)
	m := h.model()

	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindAutofix,
		FlowID: record.FlowID,
	})
	if admitted || cmd != nil {
		t.Fatalf("admitted = %v, cmd != nil = %v, want a silent refusal", admitted, cmd != nil)
	}
	if got := next.status.Text; got != "" {
		t.Fatalf("status = %q, want silence: the hint is absent and the key inert", got)
	}
	if next.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("a refused admission must reserve nothing")
	}
}

func TestAutofixAdmissionRefusesUnusableAgentCommands(t *testing.T) {
	record := autofixFlowRecord()
	cases := []struct {
		command string
		status  string
	}{
		{command: "", status: flowLaunchNoAgentCommandStatus},
		{command: "codex-app", status: `unsupported agent "codex-app"; choose codex or claude`},
	}
	for _, tc := range cases {
		t.Run("agent_"+tc.command, func(t *testing.T) {
			h := newManualLaunchHarness(t, record)
			opts := h.options()
			opts.AgentCommand = tc.command
			m := h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
			// Construction maps retired stored spellings to their supported
			// replacement. Set the raw value after construction to exercise the
			// launch admission input boundary itself.
			m.agentCommand = tc.command

			next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
				Kind:   flowLaunchKindAutofix,
				FlowID: record.FlowID,
			})
			if admitted || cmd != nil {
				t.Fatalf("admitted = %v, cmd != nil = %v, want a refusal", admitted, cmd != nil)
			}
			if got := next.status.Text; got != tc.status {
				t.Fatalf("status = %q, want %q", got, tc.status)
			}
		})
	}
}

func TestAutofixAdmissionReservesAnUntrackedAttempt(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()

	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindAutofix,
		FlowID: record.FlowID,
		Origin: flowLaunchOriginActiveFlows,
	})
	if !admitted || cmd == nil {
		t.Fatalf("admitted = %v, cmd != nil = %v, want an admitted launch", admitted, cmd != nil)
	}
	attempt, ok := next.flowLaunchAttempt(record.FlowID)
	if !ok {
		t.Fatal("an admitted launch must hold the Flow")
	}
	if attempt.Kind != flowLaunchKindAutofix {
		t.Fatalf("attempt kind = %v, want worktreeAgent", attempt.Kind)
	}
	if attempt.PhaseID != "" {
		t.Fatalf("attempt phase = %q, want an untracked attempt that names no phase", attempt.PhaseID)
	}
	if attempt.Origin != flowLaunchOriginActiveFlows {
		t.Fatalf("attempt origin = %v, want the submitting surface preserved", attempt.Origin)
	}
	if attempt.MutatedPhase {
		t.Fatal("nothing has been written, so the attempt must not claim a mutated phase")
	}
	if attempt.State != flowLaunchStateReading {
		t.Fatalf("attempt state = %v, want reading", attempt.State)
	}
}

// autofix presses U and drains the resulting command chain the way the
// bubbletea runtime would.
func (h *manualLaunchHarness) autofix(m Model) Model {
	h.t.Helper()
	next, cmd := m.handleAutofixSelectedFlowPR()
	return h.drain(next.(Model), cmd, 0)
}

// autofixModel pins the admission token so a test can name the launch ID the
// read stage must not let this launch block itself on.
func (h *manualLaunchHarness) autofixModel(token string) Model {
	h.t.Helper()
	m := h.model()
	m.launchSeams.NewLaunchID = func() string { return token }
	return m
}

func liveFlowSession(flowID, launchID, sessionID string) sessions.SessionRecord {
	// An empty status with a zero EndedAt is what a never-finalized run looks
	// like, and it is what flowSessionLive treats as live.
	return sessions.SessionRecord{
		FlowID:    flowID,
		LaunchID:  launchID,
		SessionID: sessionID,
		Provider:  sessions.Provider("codex"),
	}
}

func TestAutofixLaunchOpensOneUntrackedEmbeddedSlot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
	}{
		{name: "codex", provider: agent.CommandCodex},
		{name: "claude", provider: agent.CommandClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := autofixFlowRecord()
			h := newManualLaunchHarness(t, record)
			opts := h.options()
			opts.AgentCommand = tc.provider
			m := h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
			m.width = 160
			m.height = 40

			m, prepareCmd := h.stageAutofixLaunch(m)
			preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
			if !ok {
				t.Fatal("autofix prepare did not settle")
			}
			next, prefillCmd := m.Update(preparedMsg)
			m = next.(Model)
			if prefillCmd == nil {
				t.Fatal("interactive autofix install returned no prefill command")
			}

			if len(h.launchContexts) != 1 {
				t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
			}
			ctx := h.launchContexts[0]
			if ctx.Command != tc.provider ||
				ctx.FlowID != record.FlowID ||
				ctx.RepoPath != record.RepoPath ||
				ctx.WorktreePath != record.WorktreePath ||
				ctx.WorkingDir != record.WorktreePath ||
				ctx.Branch != record.Branch ||
				ctx.Commit != record.Commit ||
				ctx.InitialPrompt != "autofix pr #116" ||
				ctx.FlowAutofixPRNumber != 116 ||
				!ctx.FlowAutofix || ctx.FlowAgent ||
				!ctx.Embedded ||
				ctx.Headless ||
				ctx.FlowPhaseID != "" ||
				ctx.FlowLaunchTracked ||
				ctx.FlowRepair ||
				ctx.ResumeSessionID != "" {
				t.Fatalf("autofix launch context = %#v", ctx)
			}
			if len(h.launchUpdates) != 0 {
				t.Fatalf("an untracked launch must write no phase launch ID: %#v", h.launchUpdates)
			}
			if len(h.phaseUpdates) != 0 {
				t.Fatalf("an untracked launch must write no phase status: %#v", h.phaseUpdates)
			}
			if len(h.agentContexts) != 0 {
				t.Fatalf("a CLI autofix must open an embedded terminal, not hand off: %#v", h.agentContexts)
			}
			if got := embeddedFlowSlots(m, record.FlowID); got != 1 {
				t.Fatalf("embedded Flow slots = %d, want exactly one", got)
			}
			if len(m.embeddedTerminals) != 1 {
				t.Fatalf("embedded terminals = %#v, want exactly one slot", m.embeddedTerminals)
			}
			slot := m.embeddedTerminals[0]
			if slot.Number != 1 || slot.Provider != tc.provider || slot.Identity != "autofix pr 116" ||
				slot.Terminal.State() != "running" || !slot.PrefillPending {
				t.Fatalf("autofix terminal slot = %#v", slot)
			}
			wantLabel := "1 " + tc.provider + " autofix pr 116 running"
			view := ansi.Strip(m.View())
			if !strings.Contains(view, wantLabel) {
				t.Fatalf("dock does not contain %q:\n%s", wantLabel, view)
			}
			if strings.Contains(view, "1 "+tc.provider+" "+record.FlowID+" running") {
				t.Fatalf("dock retained the Flow ID identity:\n%s", view)
			}
			if h.launchReservations != 1 || h.launchReleases != 1 {
				t.Fatalf("launch reservations = %d, releases = %d, want one held then released",
					h.launchReservations, h.launchReleases)
			}
			if m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatal("the installed slot owns the Flow, so the attempt must be released")
			}
		})
	}
}

func TestAutofixLaunchHonorsModelAndEffortAndHeadless(t *testing.T) {
	record := autofixFlowRecord()
	record.Headless = true
	h := newManualLaunchHarness(t, record)
	opts := h.options()
	opts.AgentCommand = agent.CommandClaude
	m := h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts)
	m.claudeModel = "claude-opus-4-1"

	h.autofix(m)

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if ctx.Command != agent.CommandClaude {
		t.Fatalf("command = %q, want the configured agent", ctx.Command)
	}
	if ctx.Model != "claude-opus-4-1" {
		t.Fatalf("model = %q, want the admission-time settings snapshot", ctx.Model)
	}
	if !ctx.Headless {
		t.Fatal("a headless Flow must launch its autofix agent headless")
	}
}

func TestAutofixReadStageRefusals(t *testing.T) {
	cases := []struct {
		name      string
		persisted func(flowstore.FlowRecord) flowstore.FlowRecord
		arrange   func(*manualLaunchHarness)
		status    string
	}{
		{
			name:    "read error",
			arrange: func(h *manualLaunchHarness) { h.readErr = errors.New("read boom") },
			status:  "read boom",
		},
		{
			name:    "session list error",
			arrange: func(h *manualLaunchHarness) { h.sessionsErr = errors.New("list boom") },
			status:  "list boom",
		},
		{
			name: "drift: eligibility lost",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.Status = flowstore.StatusMerged
				return record
			},
			status: flowAutofixDriftStatus,
		},
		{
			name: "drift: worktree lost",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.WorktreePath = ""
				return record
			},
			status: flowAutofixDriftStatus,
		},
		{
			name: "drift: PR number lost",
			persisted: func(record flowstore.FlowRecord) flowstore.FlowRecord {
				record.PR.Number = 0
				return record
			},
			status: flowAutofixDriftStatus,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := autofixFlowRecord()
			h := newManualLaunchHarness(t, record)
			if tc.persisted != nil {
				h.persistedFlows = []flowstore.FlowRecord{tc.persisted(autofixFlowRecord())}
			}
			if tc.arrange != nil {
				tc.arrange(h)
			}

			m := h.autofix(h.model())

			if got := m.status.Text; got != tc.status {
				t.Fatalf("status = %q, want %q", got, tc.status)
			}
			if len(h.launchContexts) != 0 || len(h.agentContexts) != 0 {
				t.Fatal("a refused read must start no agent")
			}
			if m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatal("a refused read must release the attempt")
			}
		})
	}
}

func TestAutofixReadStageLiveSessionRule(t *testing.T) {
	cases := []struct {
		name     string
		phaseID  string
		refuses  bool
		launchID string
	}{
		{
			// The case worth having: an agent still live on a phase a second
			// agent in the same worktree would collide with.
			name:     "live session on the ready merge phase",
			phaseID:  "merge",
			launchID: "launch-live",
			refuses:  true,
		},
		{
			// Deliberately not caught. A merge-eligible Flow has every
			// predecessor completed, so refusing here would let one crashed run
			// kill U for that Flow permanently: R reports no obstruction for a
			// merge-eligible Flow and x does not apply to a completed phase.
			name:     "stale never-finalized session on a completed phase",
			phaseID:  "implementation",
			launchID: "launch-stale",
			refuses:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := autofixFlowRecord()
			for i := range record.Phases {
				if record.Phases[i].PhaseID == tc.phaseID {
					record.Phases[i].LaunchIDs = []string{tc.launchID}
				}
			}
			h := newManualLaunchHarness(t, record)
			h.sessionRecords = []sessions.SessionRecord{liveFlowSession(record.FlowID, tc.launchID, "session-1")}

			m := h.autofix(h.model())

			if tc.refuses {
				if got := m.status.Text; got != flowAutofixLiveSessionStatus {
					t.Fatalf("status = %q, want %q", got, flowAutofixLiveSessionStatus)
				}
				if len(h.launchContexts) != 0 {
					t.Fatal("a refused read must start no agent")
				}
				return
			}
			if len(h.launchContexts) != 1 {
				t.Fatalf("embedded launches = %#v, want the launch to proceed", h.launchContexts)
			}
		})
	}
}

func TestAutofixReadStageIgnoresSessionsOutsideEveryPhase(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	// One live session from a previous autofix run and one carrying this
	// launch's own token. Neither launch ID belongs to a phase, so the
	// phase-scoped rule is blind to both — which is what stops the launch from
	// blocking on its own token, and what keeps a crashed autofix agent from
	// making U permanently dead.
	h.sessionRecords = []sessions.SessionRecord{
		liveFlowSession(record.FlowID, "previous-autofix", "session-old"),
		liveFlowSession(record.FlowID, "autofix-token", "session-self"),
	}

	h.autofix(h.autofixModel("autofix-token"))

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want the launch to proceed", h.launchContexts)
	}
}

// stageAutofixLaunch presses U and stops between the read stage and
// prepare, so a test can land a store write in exactly the window the
// reservation exists to close.
func (h *manualLaunchHarness) stageAutofixLaunch(m Model) (Model, tea.Cmd) {
	h.t.Helper()
	next, readCmd := m.handleAutofixSelectedFlowPR()
	m = next.(Model)
	if readCmd == nil {
		h.t.Fatal("an admitted press must return the read command")
	}
	readMsg, ok := runCommandWithoutWaiting(readCmd)
	if !ok {
		h.t.Fatal("the read stage did not settle")
	}
	next, prepareCmd := m.Update(readMsg)
	m = next.(Model)
	if prepareCmd == nil {
		h.t.Fatal("a successful read must return the prepare command")
	}
	return m, prepareCmd
}

// TestAutofixPrepareResolvesHeadlessFromTheReservation pins the reason
// prepare uses the reservation's record rather than the read stage's: this kind
// writes no phase, so that record is its only fresh read of the Flow before the
// spawn. A headless toggle landing in between would otherwise launch in the
// previous mode — and on the wrong route with it, since a headless launch is
// never tmux-eligible.
func TestAutofixPrepareResolvesHeadlessFromTheReservation(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	m, prepareCmd := h.stageAutofixLaunch(h.model())
	// h lands while the launch is between stages: the store now says headless.
	h.record.Headless = true
	preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
	if !ok {
		t.Fatal("prepare did not settle")
	}
	m = h.drainMsg(m, preparedMsg, 0)

	if len(h.tmuxContexts) != 0 {
		t.Fatalf("a headless launch is not tmux-eligible: %#v", h.tmuxContexts)
	}
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	if !h.launchContexts[0].Headless {
		t.Fatalf("ctx = %#v, want the reservation's headless preference", h.launchContexts[0])
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("the installed slot owns the Flow, so the attempt must be released")
	}
}

func TestAutofixPrepareUsesReservedPRNumber(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.width = 160
	m.height = 40

	m, prepareCmd := h.stageAutofixLaunch(m)
	h.record.PR.Number = 204
	h.record.PR.URL = "https://github.com/approachcontrol/approach/pull/204"
	preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
	if !ok {
		t.Fatal("prepare did not settle")
	}
	next, prefillCmd := m.Update(preparedMsg)
	m = next.(Model)
	if prefillCmd == nil {
		t.Fatal("interactive autofix install returned no prefill command")
	}

	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %#v, want exactly one", h.launchContexts)
	}
	ctx := h.launchContexts[0]
	if ctx.FlowAutofixPRNumber != 204 || ctx.InitialPrompt != "autofix pr #204" {
		t.Fatalf("reserved PR launch metadata = number %d prompt %q, want 204 and %q",
			ctx.FlowAutofixPRNumber, ctx.InitialPrompt, "autofix pr #204")
	}
	if len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].Identity != "autofix pr 204" {
		t.Fatalf("reserved PR terminal slot = %#v", m.embeddedTerminals)
	}
	wantLabel := "1 codex autofix pr 204 running"
	if view := ansi.Strip(m.View()); !strings.Contains(view, wantLabel) {
		t.Fatalf("dock does not contain %q:\n%s", wantLabel, view)
	}
}

// TestAutofixPrepareRefusesDriftSeenOnlyByTheReservation is the same
// window as above for eligibility: nothing else re-reads the Flow after the read
// stage, so a Flow that stops qualifying in it would launch anyway.
func TestAutofixPrepareRefusesDriftSeenOnlyByTheReservation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drift func(*flowstore.FlowRecord)
	}{
		{name: "merged", drift: func(r *flowstore.FlowRecord) { r.Status = flowstore.StatusMerged }},
		{name: "worktree removed", drift: func(r *flowstore.FlowRecord) { r.WorktreePath = "" }},
		{name: "pr target dropped", drift: func(r *flowstore.FlowRecord) { r.PR = flowstore.PullRequest{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := autofixFlowRecord()
			h := newManualLaunchHarness(t, record)

			m, prepareCmd := h.stageAutofixLaunch(h.model())
			tc.drift(&h.record)
			preparedMsg, ok := runCommandWithoutWaiting(prepareCmd)
			if !ok {
				t.Fatal("prepare did not settle")
			}
			m = h.drainMsg(m, preparedMsg, 0)

			if len(h.launchContexts) != 0 || len(h.tmuxContexts) != 0 {
				t.Fatalf("a drifted Flow must start no agent: %#v %#v", h.launchContexts, h.tmuxContexts)
			}
			if got := m.status.Text; got != flowAutofixDriftStatus {
				t.Fatalf("status = %q, want %q", got, flowAutofixDriftStatus)
			}
			if len(h.phaseUpdates) != 0 {
				t.Fatalf("a prepare failure of an untracked launch must persist nothing: %#v", h.phaseUpdates)
			}
			if h.launchReleases != 1 {
				t.Fatalf("launch releases = %d, want the cross-process reservation released", h.launchReleases)
			}
			if m.flowLaunchAttemptOccupied(record.FlowID) {
				t.Fatal("a refused prepare must release the attempt")
			}
		})
	}
}

func TestAutofixPrepareRefusesAFlowClosedBetweenReadAndPrepare(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.reserveLaunchErr = errors.New("cannot launch an agent for flow \"flow-1\" because it is closed")

	m := h.autofix(h.model())

	if len(h.launchContexts) != 0 {
		t.Fatalf("a refused reservation must start no agent: %#v", h.launchContexts)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("a prepare failure of an untracked launch must persist nothing: %#v", h.phaseUpdates)
	}
	if got := m.status.Text; got != h.reserveLaunchErr.Error() {
		t.Fatalf("status = %q, want the reservation error", got)
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("a failed prepare must release the attempt")
	}
}

func TestAutofixPostPrepareFailureLeavesThePhaseAloneAndReportsIt(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.startTerminalErr = errors.New("terminal boom")

	m := h.autofix(h.model())

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("a post-prepare failure of an untracked launch must persist nothing: %#v", h.phaseUpdates)
	}
	// The failure reaches ActionFailedMsg rather than a bare status, which is
	// what keeps main's repo gate and the Flow surface refresh. That only holds
	// because the attempt never claimed a mutated phase.
	if got := m.status.Text; got != "terminal boom" {
		t.Fatalf("status = %q, want the install failure", got)
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("a failed install must release the attempt")
	}
	if h.launchReleases != 1 {
		t.Fatalf("launch releases = %d, want the cross-process reservation released", h.launchReleases)
	}
}

func TestAutofixInstallBackstopNamesNoPhase(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Scope:      embeddedTerminalScopeFlow,
		FlowID:     record.FlowID,
		FlowRepair: true,
		Terminal:   flowPhaseLaunchTestTerminal{state: "running"},
	})
	attempt := flowLaunchAttempt{
		Token:  "autofix-token",
		Kind:   flowLaunchKindAutofix,
		FlowID: record.FlowID,
		State:  flowLaunchStatePreparing,
	}
	m = m.withFlowLaunchAttempt(attempt)

	next, cmd := m.installFlowLaunchEmbedded(attempt, flowLaunchEventMsg{
		Token:    attempt.Token,
		Kind:     attempt.Kind,
		FlowID:   record.FlowID,
		RepoPath: record.RepoPath,
		Context:  actions.AgentLaunchContext{Command: agent.CommandCodex, FlowID: record.FlowID, LaunchID: attempt.Token},
	})
	m = h.drain(next, cmd, 0)

	if got := m.status.Text; got != flowAutofixCanceledStatus {
		t.Fatalf("status = %q, want %q", got, flowAutofixCanceledStatus)
	}
}

func TestAutofixKeyIsBoundOnBothFlowSurfacesAndInertElsewhere(t *testing.T) {
	record := autofixFlowRecord()

	t.Run("flows surface", func(t *testing.T) {
		h := newManualLaunchHarness(t, record)
		m := h.model()
		m.activePane = ui.PaneBottom
		next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'U'}})
		m = h.drain(next.(Model), cmd, 0)
		if len(h.launchContexts) != 1 {
			t.Fatalf("embedded launches = %#v, want exactly one from the Flows surface", h.launchContexts)
		}
		if h.launchContexts[0].InitialPrompt != "autofix pr #116" {
			t.Fatalf("prompt = %q", h.launchContexts[0].InitialPrompt)
		}
	})

	t.Run("active flows surface", func(t *testing.T) {
		h := newManualLaunchHarness(t, record)
		m := h.model()
		m.activeFlowSurface = true
		m.activeFlows = m.activeFlows.SetItems([]flowstore.FlowRecord{record})
		next, cmd := m.handleActiveFlowSurfaceKey("U")
		m = h.drain(next.(Model), cmd, 0)
		if len(h.launchContexts) != 1 {
			t.Fatalf("embedded launches = %#v, want exactly one from the Active Flows surface", h.launchContexts)
		}
	})

	t.Run("repo pane focus", func(t *testing.T) {
		h := newManualLaunchHarness(t, record)
		m := h.model()
		m.activePane = ui.PaneRepos
		next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'U'}})
		h.drain(next.(Model), cmd, 0)
		if len(h.launchContexts) != 0 {
			t.Fatalf("U must be inert while the repo pane holds focus: %#v", h.launchContexts)
		}
	})

	t.Run("launch already in flight", func(t *testing.T) {
		h := newManualLaunchHarness(t, record)
		base := h.model()
		base.activePane = ui.PaneBottom
		m, _ := base.reserveFlowLaunchAttempt(flowLaunchAttempt{
			Token:  "other-token",
			Kind:   flowLaunchKindManualPhase,
			FlowID: record.FlowID,
		}, flowLaunchStateReading)
		next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'U'}})
		m = h.drain(next.(Model), cmd, 0)
		if len(h.launchContexts) != 0 {
			t.Fatalf("U must refuse while a launch holds the Flow: %#v", h.launchContexts)
		}
		if got := m.status.Text; got != flowAutofixInFlightStatus {
			t.Fatalf("status = %q, want %q", got, flowAutofixInFlightStatus)
		}
	})
}

func TestAutofixTmuxHandoffIsProbeableBySubsequentPresses(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	m := h.autofix(h.model())

	if len(h.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %#v, want exactly one", h.tmuxContexts)
	}
	ctx := h.tmuxContexts[0]
	if ctx.Embedded {
		t.Fatal("a tmux launch must clear Embedded so the prompt reaches argv")
	}
	if ctx.InitialPrompt != "autofix pr #116" {
		t.Fatalf("prompt = %q", ctx.InitialPrompt)
	}
	if len(h.launchContexts) != 0 {
		t.Fatalf("a tmux launch must open no embedded slot: %#v", h.launchContexts)
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("a completed tmux handoff releases the attempt")
	}

	// No phase carries this launch ID, so a phase-IDs-only probe would be blind
	// to the window that is still open. The registry is what closes that.
	var probed []string
	h.windowLive = func(repoPath string, launchIDs []string) bool {
		probed = append([]string{}, launchIDs...)
		return true
	}
	next, cmd := m.handleAutofixSelectedFlowPR()
	blocked := next.(Model)
	if cmd != nil {
		t.Fatal("a refused press must start no work")
	}
	if got := blocked.status.Text; got != tmuxFlowLiveWindowRefusal {
		t.Fatalf("status = %q, want %q", got, tmuxFlowLiveWindowRefusal)
	}
	if len(probed) != 1 || probed[0] != h.tmuxContexts[0].LaunchID {
		t.Fatalf("probed launch IDs = %#v, want the autofix agent's own launch", probed)
	}

	// Once the window is gone the shortcut is available again, with no expiry
	// needed.
	h.windowLive = func(string, []string) bool { return false }
	m = h.autofix(m)
	if len(h.tmuxContexts) != 2 {
		t.Fatalf("tmux launches = %d, want a second launch once the window closed", len(h.tmuxContexts))
	}
}

// TestAutofixTmuxWindowBlocksRepairToo pins the reason the registry is
// unioned into the shared Flow-wide probe rather than a probe of U's own: a
// repair agent lands in the same worktree as the autofix agent, so it has to see
// that window too.
func TestAutofixTmuxWindowBlocksRepairToo(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	m := h.autofix(h.model())
	if len(h.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %#v, want exactly one", h.tmuxContexts)
	}

	var probed []string
	h.windowLive = func(repoPath string, launchIDs []string) bool {
		probed = append([]string{}, launchIDs...)
		return true
	}
	// No phase carries the autofix launch ID, so a phase-IDs-only probe here
	// would let R start a second agent in the worktree the first is mid-run in.
	if !m.tmuxFlowAgentStillRunning(record, record.RepoPath) {
		t.Fatal("the Flow-wide probe must see the autofix agent's window")
	}
	if len(probed) != 1 || probed[0] != h.tmuxContexts[0].LaunchID {
		t.Fatalf("probed launch IDs = %#v, want the autofix agent's own launch", probed)
	}
}

// TestAutofixTmuxWindowBlocksThePhaseLaunchToo covers the other half of
// the same hazard: the autofix agent holds the worktree but no phase, and the
// Flow's merge phase stays ready, so without the probe g would put a second
// agent into the worktree the first is mid-run in.
func TestAutofixTmuxWindowBlocksThePhaseLaunchToo(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	m := h.autofix(h.model())
	if len(h.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %#v, want exactly one", h.tmuxContexts)
	}

	var probed []string
	h.windowLive = func(repoPath string, launchIDs []string) bool {
		probed = append([]string{}, launchIDs...)
		return true
	}
	next, cmd := m.handleLaunchNextFlowPhase()
	blocked := next.(Model)
	if cmd != nil {
		t.Fatal("a refused press must start no work")
	}
	if got := blocked.status.Text; got != tmuxFlowLiveWindowRefusal {
		t.Fatalf("status = %q, want %q", got, tmuxFlowLiveWindowRefusal)
	}
	// Only the autofix agent's own launch is asked about. Probing every phase's
	// launch ID here would refuse g for a finished agent whose window was left
	// open, which is not this rule's business.
	if len(probed) != 1 || probed[0] != h.tmuxContexts[0].LaunchID {
		t.Fatalf("probed launch IDs = %#v, want the autofix agent's own launch", probed)
	}
	if len(h.tmuxContexts) != 1 || len(h.launchUpdates) != 0 {
		t.Fatalf("a refused phase launch must persist and spawn nothing: %#v %#v", h.tmuxContexts, h.launchUpdates)
	}

	// The refusal lasts exactly as long as the window does.
	h.windowLive = func(string, []string) bool { return false }
	next, cmd = m.handleLaunchNextFlowPhase()
	h.drain(next.(Model), cmd, 0)
	if len(h.tmuxContexts) != 2 {
		t.Fatalf("tmux launches = %d, want the phase launch once the window closed", len(h.tmuxContexts))
	}
}

// TestAutofixProbeIgnoresPhaseWindows pins the split between the two
// probes: the Flow-wide one repair uses covers every phase, and the one the
// phase launch and resume use covers only the autofix agent, whose window no
// phase knows about.
func TestAutofixProbeIgnoresPhaseWindows(t *testing.T) {
	record := autofixFlowRecord()
	for i := range record.Phases {
		if record.Phases[i].PhaseID == "implementation" {
			record.Phases[i].LaunchIDs = []string{"phase-launch"}
		}
	}
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true
	m := h.model()
	h.windowLive = func(string, []string) bool { return true }

	if m.tmuxAutofixAgentStillRunning(record, record.RepoPath) {
		t.Fatal("a Flow with no autofix agent must not probe, let alone refuse")
	}
	if !m.tmuxFlowAgentStillRunning(record, record.RepoPath) {
		t.Fatal("the Flow-wide probe still covers a phase's own live window")
	}
}

// TestAutofixOwnedFlowRefusesWithoutProbingTmux pins the probe's gate: an
// already-owned Flow is refused by admission, which names the obstacle, instead
// of by the live-window probe, which would name the wrong one and fork tmux to
// do it. The gate is sound only because every state the footer predicate
// withdraws for is one admission refuses too, so this also guards against a
// display-only condition being added to that predicate.
func TestAutofixOwnedFlowRefusesWithoutProbingTmux(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	m := h.autofix(h.model())
	if len(h.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %#v, want exactly one", h.tmuxContexts)
	}

	probes := 0
	h.windowLive = func(string, []string) bool {
		probes++
		return true
	}
	// An embedded Flow terminal is an occupancy signal the footer withdraws for,
	// so the press must never reach the probe.
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Scope:    embeddedTerminalScopeFlow,
		FlowID:   record.FlowID,
		Terminal: flowPhaseLaunchTestTerminal{state: "running"},
	})
	if m.selectedFlowAutofixReady() {
		t.Fatal("the fixture must be an owned Flow")
	}

	next, cmd := m.handleAutofixSelectedFlowPR()
	blocked := next.(Model)
	if cmd != nil {
		t.Fatal("a refused press must start no work")
	}
	if probes != 0 {
		t.Fatalf("tmux probes = %d, want none for a press a cheaper refusal answers", probes)
	}
	if got := blocked.status.Text; got != flowAutofixTerminalStatus {
		t.Fatalf("status = %q, want the obstacle that actually holds the Flow", got)
	}
	if len(h.tmuxContexts) != 1 {
		t.Fatalf("tmux launches = %d, want no second launch", len(h.tmuxContexts))
	}
}

// TestAutofixTmuxSpecFailureRecordsNoWindow pins the ordering claim in the
// handoff: the registry entry is written after the spec builds, so a build that
// never produced a window leaves nothing for a later probe to ask about.
func TestAutofixTmuxSpecFailureRecordsNoWindow(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true
	h.tmuxLaunchErr = errors.New("tmux boom")

	m := h.autofix(h.model())

	if got := m.status.Text; got != "tmux boom" {
		t.Fatalf("status = %q, want the spec-build failure", got)
	}
	if len(m.flowAutofixTmuxLaunches) != 0 {
		t.Fatalf("registry = %#v, want no entry for a window that was never opened", m.flowAutofixTmuxLaunches)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("an untracked launch must write no phase status: %#v", h.phaseUpdates)
	}
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("a failed tmux handoff must release the attempt")
	}
	if h.launchReleases != 1 {
		t.Fatalf("launch releases = %d, want the cross-process reservation released", h.launchReleases)
	}
	// With nothing recorded the shortcut stays available, so the next press is
	// admitted rather than answered with the live-window refusal.
	m = h.autofix(m)
	if len(h.tmuxContexts) != 2 {
		t.Fatalf("tmux launches = %d, want the next press still admitted", len(h.tmuxContexts))
	}
}

// TestWithFlowAutofixTmuxLaunchClonesTheRegistry pins the copy-on-write
// rule every per-Flow map on the value-typed Model follows: a Model taken before
// the write must not observe it, or a launch that failed after being copied
// would still block the next press through the older copy.
func TestWithFlowAutofixTmuxLaunchClonesTheRegistry(t *testing.T) {
	base := newManualLaunchHarness(t, autofixFlowRecord()).model()

	first := base.withFlowAutofixTmuxLaunch("flow-1", "launch-1")
	second := first.withFlowAutofixTmuxLaunch("flow-2", "launch-2")

	if len(base.flowAutofixTmuxLaunches) != 0 {
		t.Fatalf("the pre-write Model saw the write: %#v", base.flowAutofixTmuxLaunches)
	}
	if _, ok := first.flowAutofixTmuxLaunches["flow-2"]; ok {
		t.Fatalf("the first copy saw the second write: %#v", first.flowAutofixTmuxLaunches)
	}
	if got := second.flowAutofixTmuxLaunches["flow-1"]; !slices.Equal(got, []string{"launch-1"}) {
		t.Fatalf("flow-1 entry = %#v, want the earlier write carried forward", got)
	}
	// Retention, not replacement: a second launch is appended, and the copy taken
	// before it must still see only the first — an append through a shared
	// backing array would reach back into it.
	third := second.withFlowAutofixTmuxLaunch("flow-1", "launch-3")
	if got := third.flowAutofixTmuxLaunches["flow-1"]; !slices.Equal(got, []string{"launch-1", "launch-3"}) {
		t.Fatalf("flow-1 entry = %#v, want both launches retained oldest first", got)
	}
	if got := second.flowAutofixTmuxLaunches["flow-1"]; !slices.Equal(got, []string{"launch-1"}) {
		t.Fatalf("the append reached an older copy: %#v", got)
	}
	// A launch already retained adds nothing: the same token can reach a handoff
	// twice, and a duplicate would only widen the probe's argument list.
	if got := third.withFlowAutofixTmuxLaunch("flow-1", "launch-3").
		flowAutofixTmuxLaunches["flow-1"]; !slices.Equal(got, []string{"launch-1", "launch-3"}) {
		t.Fatalf("flow-1 entry = %#v, want a duplicate launch ignored", got)
	}
	if next := third.withFlowAutofixTmuxLaunch("", "launch-4"); len(next.flowAutofixTmuxLaunches) != 2 {
		t.Fatalf("an empty Flow ID must record nothing: %#v", next.flowAutofixTmuxLaunches)
	}
	if next := third.withFlowAutofixTmuxLaunch("flow-4", " "); len(next.flowAutofixTmuxLaunches) != 2 {
		t.Fatalf("a blank launch ID must record nothing: %#v", next.flowAutofixTmuxLaunches)
	}
	// The accessor must not hand out the registry's own array either: the Flow-wide
	// probe appends the autofix launch IDs onto the phase ones.
	ids := third.flowAutofixTmuxLaunchIDs("flow-1")
	ids[0] = "launch-5"
	if got := third.flowAutofixTmuxLaunches["flow-1"]; !slices.Equal(got, []string{"launch-1", "launch-3"}) {
		t.Fatalf("writing to the accessor's result reached the registry: %#v", got)
	}
}

// TestAutofixPrefillFailureCorrectsNoPhase covers the path FlowAutofix
// reaches in ShouldPrefillEmbeddedPrompt: the dock is filled for this launch, so
// its prefill can fail, and the correction that follows must stay as untracked
// as the launch was.
func TestAutofixPrefillFailureCorrectsNoPhase(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)

	m := h.autofix(h.model())
	if len(m.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %d, want the autofix slot", len(m.embeddedTerminals))
	}
	slot := m.embeddedTerminals[0]
	ctx := h.launchContexts[0]
	if !ctx.FlowAutofix || ctx.FlowAgent || ctx.FlowPhaseID != "" || ctx.FlowLaunchTracked {
		t.Fatalf("the launch under test must be the untracked Flow agent: %#v", ctx)
	}
	if !actions.ShouldPrefillEmbeddedPrompt(ctx) {
		t.Fatal("an interactive embedded Flow agent must prefill the dock")
	}

	next, cmd := m.Update(embeddedPromptPrefillResultMsg{
		ID:            slot.ID,
		LaunchContext: ctx,
		Err:           errors.New("prefill failed"),
	})
	after := h.drain(next.(Model), cmd, 0)

	if after.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		t.Fatal("the failed slot should have been dismissed")
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("an untracked launch has no phase to correct: %#v", h.phaseUpdates)
	}
	if got := after.status.Text; got != "prefill failed" {
		t.Fatalf("status = %q, want the prefill failure verbatim", got)
	}
	if after.flowLaunchAttemptOccupied(record.FlowID) {
		t.Fatal("no attempt may be re-reserved for a launch with no phase to persist")
	}
	// Nothing owns the Flow any more, so the shortcut is usable again.
	if !after.selectedFlowAutofixReady() {
		t.Fatal("a dismissed slot must leave U advertised again")
	}
}

// TestAutofixHintReachesTheRenderedView pins the model → ui plumbing: the flag
// is built inside View, so rendering is the only place the wiring is
// observable.
func TestAutofixHintReachesTheRenderedView(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.activePane = ui.PaneBottom
	m.width, m.height = 200, 24
	m = m.reflowForTerminalDock()

	if !strings.Contains(m.View(), "autofix PR") {
		t.Fatalf("an eligible Flow row should render the autofix hint:\n%s", m.View())
	}

	occupied := m
	occupied.embeddedTerminals = append(occupied.embeddedTerminals, embeddedTerminalSlot{
		Scope:    embeddedTerminalScopeFlow,
		FlowID:   record.FlowID,
		Terminal: flowPhaseLaunchTestTerminal{state: "running"},
	})
	if strings.Contains(occupied.View(), "autofix PR") {
		t.Fatalf("an occupied Flow must not render the autofix hint:\n%s", occupied.View())
	}
}

// TestAutofixTmuxRegistryRetainsEveryLaunchID pins why the registry keeps
// every launch a Flow made rather than the newest alone. The probe is advisory
// in one direction only — a timed-out `list-windows` answers false for a window
// that is still open — so a second U press is admitted while the first agent
// runs. Dropping the older ID there would leave that agent unprobed the moment
// the newer window exits, and the next U, g, r, or R would put a third agent in
// a worktree two are already editing.
func TestAutofixTmuxRegistryRetainsEveryLaunchID(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)
	h.launchBackend = config.LaunchBackendTmux
	h.tmuxAvailable = true

	tokens := []string{"launch-1", "launch-2"}
	issued := 0
	m := h.model()
	m.launchSeams.NewLaunchID = func() string {
		token := tokens[issued]
		issued++
		return token
	}
	// False for both presses: the first because nothing has launched yet, the
	// second as the transient probe failure this retention exists to survive.
	h.windowLive = func(string, []string) bool { return false }

	m = h.autofix(m)
	m = h.autofix(m)

	if len(h.tmuxContexts) != 2 {
		t.Fatalf("tmux launches = %d, want both presses admitted", len(h.tmuxContexts))
	}

	var probed []string
	h.windowLive = func(_ string, launchIDs []string) bool {
		probed = append(probed, launchIDs...)
		return false
	}
	if m.tmuxAutofixAgentStillRunning(record, "") {
		t.Fatal("the probe answered false, so nothing must read as live")
	}
	for _, want := range tokens {
		if !slices.Contains(probed, want) {
			t.Fatalf("probed = %#v, want %q retained", probed, want)
		}
	}

	// The Flow-wide probe unions the registry with every phase launch, so it has
	// to carry the same retained set.
	probed = nil
	if m.tmuxFlowAgentStillRunning(record, "") {
		t.Fatal("the probe answered false, so nothing must read as live")
	}
	for _, want := range tokens {
		if !slices.Contains(probed, want) {
			t.Fatalf("Flow-wide probed = %#v, want %q retained", probed, want)
		}
	}
}

// TestToggleFlowHeadlessRefusedWhileAutofixAttemptHoldsTheFlow pins the
// half of the headless fence admission cannot cover. Admission refuses U while a
// headless write is outstanding, but the user can press h after the attempt is
// already reading. This kind resolves headless from ReserveAgentLaunch's record,
// read under the launch/close lock, while SetHeadless writes under the Flow
// store's own — the two are not serialized, so a toggle started here would leave
// the reservation free to read either value, and a headless launch is never
// tmux-eligible. Refuse the toggle for as long as the attempt holds the Flow.
func TestToggleFlowHeadlessRefusedWhileAutofixAttemptHoldsTheFlow(t *testing.T) {
	record := autofixFlowRecord()
	h := newManualLaunchHarness(t, record)

	m, prepareCmd := h.stageAutofixLaunch(h.model())
	if prepareCmd == nil {
		t.Fatal("the staged press must have produced the prepare command")
	}
	if _, ok := h.attempt(m, record.FlowID); !ok {
		t.Fatal("the staged press must still hold the Flow")
	}

	next, cmd := m.handleToggleFlowHeadless()
	toggled := next.(Model)

	if cmd != nil {
		t.Fatal("the toggle must not start a write that races the reservation's read")
	}
	if got := toggled.status.Text; got != flowHeadlessLaunchInFlightStatus {
		t.Fatalf("status = %q, want %q", got, flowHeadlessLaunchInFlightStatus)
	}
	if toggled.flowHeadlessWritePending(record.FlowID) {
		t.Fatal("a refused toggle must leave no pending write")
	}
}

// TestToggleFlowHeadlessRefusedWhilePendingRepairHoldsTheFlow covers repair's
// half of the same fence. A repair takes its lifecycle attempt at the key press
// and only reads the Flow later, in prepare, through ReserveRepairLaunch — the
// same launch/close lock, and the same lack of ordering against SetHeadless —
// before refreshFlowRepairLaunchContext resolves ctx.Headless from that record.
// So the toggle has to stand off for a repair attempt exactly as it does for a
// autofix agent, or the repair runs interactively or headlessly depending on
// which command finished first.
func TestToggleFlowHeadlessRefusedWhilePendingRepairHoldsTheFlow(t *testing.T) {
	record := manualLaunchFlowRecord()
	record.Phases[0].Status = flowstore.PhaseNeedsAttention
	// Repair resolves its launch directory by stat, so the fixture needs a real
	// one or admission stops before it ever marks the Flow pending.
	dir := t.TempDir()
	record.RepoPath = dir
	record.WorktreePath = dir
	h := newManualLaunchHarness(t, record)
	m := h.model()
	m.activeFlowSurface = false
	if !m.selectedFlowRepairReady() {
		t.Fatal("fixture should be repairable before anything holds the Flow")
	}

	next, repairCmd := m.handleRepairSelectedFlow()
	repairing := next.(Model)
	if repairCmd == nil {
		t.Fatal("an admitted repair must return the read command")
	}
	// The read and prepare stages are deliberately left unrun: this is the
	// window between admission taking the attempt and prepare's reservation
	// reading the record it resolves headless from.
	if got := repairing.flowLaunchAttemptKind(record.FlowID); got != flowLaunchKindRepair {
		t.Fatalf("attempt kind = %v, want the admitted repair holding the Flow", got)
	}

	toggled, cmd := repairing.handleToggleFlowHeadless()
	toggledModel := toggled.(Model)

	if cmd != nil {
		t.Fatal("the toggle must not start a write that races the repair's read")
	}
	if got := toggledModel.status.Text; got != flowHeadlessLaunchInFlightStatus {
		t.Fatalf("status = %q, want %q", got, flowHeadlessLaunchInFlightStatus)
	}
	if toggledModel.flowHeadlessWritePending(record.FlowID) {
		t.Fatal("a refused toggle must leave no pending write")
	}
}
