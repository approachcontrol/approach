package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

func autoAdvanceTestFlow(flowID, repoPath string, autoMode bool, statuses map[string]string) flowstore.FlowRecord {
	phaseDefs := []struct {
		id    string
		title string
		order int
	}{
		{"plan", "Plan", 1},
		{"plan-review", "Plan Review", 2},
		{"implementation", "Implementation", 3},
		{"review-loop", "Review loop", 4},
		{"pr-creation", "PR creation", 5},
		{"autoreview", "Autoreview", 6},
		{"merge", "Merge", 7},
	}
	phases := make([]flowstore.FlowPhase, 0, len(phaseDefs))
	for _, def := range phaseDefs {
		status := statuses[def.id]
		if status == "" {
			status = flowstore.PhasePending
		}
		phase := flowstore.FlowPhase{PhaseID: def.id, Title: def.title, Status: status, Order: def.order}
		if def.id == "plan-review" && status == flowstore.PhaseCompleted {
			phase.Outcome = flowstore.OutcomeApproved
		}
		phases = append(phases, phase)
	}
	return flowstore.FlowRecord{
		FlowID:       flowID,
		Title:        "Bravo Flow",
		RepoPath:     repoPath,
		WorktreePath: repoPath + "-worktrees/flow-auto",
		Branch:       "flow/auto",
		Status:       flowstore.StatusInProgress,
		AutoMode:     autoMode,
		Phases:       phases,
	}
}

func autoAdvanceCustomFlow(statuses map[string]string) flowstore.FlowRecord {
	phases := []flowstore.FlowPhase{
		{PhaseID: "root", Title: "Root", Kind: flowstore.KindPlan, Status: statuses["root"], Order: 1, DependsOn: []string{}},
		{PhaseID: "branch-a", Title: "Branch A", Kind: flowstore.KindImplementation, Status: statuses["branch-a"], Order: 2, DependsOn: []string{"root"}},
		{PhaseID: "branch-b", Title: "Branch B", Kind: flowstore.KindReviewLoop, Status: statuses["branch-b"], Order: 3, DependsOn: []string{"root"}},
		{PhaseID: "summary", Title: "Summary", Kind: "", Status: statuses["summary"], Order: 4, DependsOn: []string{"branch-a", "branch-b"}},
	}
	for i := range phases {
		if phases[i].Status == "" {
			phases[i].Status = flowstore.PhasePending
		}
	}
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Custom Flow",
		RepoPath:     "/dev/bravo",
		WorktreePath: "/dev/bravo-worktrees/custom",
		Branch:       "flow/custom",
		Status:       flowstore.StatusInProgress,
		AutoMode:     true,
		Phases:       phases,
	}
}

func runAutoAdvanceResultForTest(t *testing.T, m Model, flows []flowstore.FlowRecord) (Model, tea.Cmd) {
	t.Helper()
	m.autoAdvanceInFlight = 1
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: flows, Request: 1})
	return next, cmd
}

func firstFlowEmbeddedLaunchFromAutoAdvance(t *testing.T, cmd tea.Cmd) FlowEmbeddedLaunchRequestedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected auto-advance command")
	}
	msg := cmd()
	if launch, ok := msg.(FlowEmbeddedLaunchRequestedMsg); ok {
		return launch
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("auto-advance command returned %T, want FlowEmbeddedLaunchRequestedMsg or BatchMsg", msg)
	}
	for _, subcmd := range batch {
		raw := subcmd()
		if launch, ok := raw.(FlowEmbeddedLaunchRequestedMsg); ok {
			return launch
		}
	}
	t.Fatalf("auto-advance batch returned no FlowEmbeddedLaunchRequestedMsg")
	return FlowEmbeddedLaunchRequestedMsg{}
}

func TestAutoModeFanOutDrainsSequentially(t *testing.T) {
	previous := autoAdvanceCustomFlow(map[string]string{
		"root": flowstore.PhaseRunning,
	})
	rootCompleted := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseReady,
		"branch-b": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return rootCompleted, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	m, cmd := runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{rootCompleted})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "branch-a" {
		t.Fatalf("first drain launch = %q, want branch-a", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "branch-a" {
		t.Fatalf("updates = %#v, want branch-a launch", updates)
	}

	branchARunning := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseRunning,
		"branch-b": flowstore.PhaseReady,
	})
	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{branchARunning})
	if len(updates) != 1 {
		t.Fatalf("updates while branch-a running = %#v, want no additional launch", updates)
	}

	branchACompleted := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseCompleted,
		"branch-b": flowstore.PhaseReady,
	})
	m, cmd = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{branchACompleted})
	launch = firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "branch-b" {
		t.Fatalf("second drain launch = %q, want branch-b", launch.LaunchContext.FlowPhaseID)
	}
}

func TestAutoModeSkipDoesNotArmDrain(t *testing.T) {
	previous := autoAdvanceCustomFlow(map[string]string{
		"root": flowstore.PhaseRunning,
	})
	skipped := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseSkipped,
		"branch-a": flowstore.PhaseReady,
	})
	skipped.Phases[0].Notes = "Skipped by user"
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return skipped, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{skipped})
	if len(updates) != 0 {
		t.Fatalf("updates after skip = %#v, want none", updates)
	}
	if len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want skip not to arm drain", m.autoAdvanceDrainFlows)
	}
}

func TestAutoModeSkipDisarmsExistingDrain(t *testing.T) {
	rootCompleted := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseReady,
		"branch-b": flowstore.PhaseReady,
	})
	branchARunning := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseRunning,
		"branch-b": flowstore.PhaseReady,
	})
	branchASkipped := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseSkipped,
		"branch-b": flowstore.PhaseReady,
	})
	branchASkipped.Phases[1].Notes = "Skipped by user"
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return branchARunning, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{autoAdvanceCustomFlow(map[string]string{
		"root": flowstore.PhaseRunning,
	})}

	m, cmd := runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{rootCompleted})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "branch-a" {
		t.Fatalf("first drain launch = %q, want branch-a", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "branch-a" {
		t.Fatalf("updates after first launch = %#v, want branch-a", updates)
	}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{branchASkipped})
	if len(updates) != 1 {
		t.Fatalf("updates after skipped branch = %#v, want no successor launch", updates)
	}
	if len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want skipped branch to disarm drain", m.autoAdvanceDrainFlows)
	}
}

func repairAutoDrainTestFlow(status, kind string, autoMode bool) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-repair",
		Title:        "Repair drain",
		RepoPath:     "/dev/bravo",
		WorktreePath: "/dev/bravo-worktrees/flow-repair",
		Branch:       "flow/repair",
		AutoMode:     autoMode,
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "successor",
			Title:     "Successor",
			Kind:      kind,
			Status:    status,
			DependsOn: []string{},
		}},
	}
}

func repairCompletionEdgeTestFlow(repairStatus, successorStatus string) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-repair",
		Title:        "Repair completion edge",
		RepoPath:     "/dev/bravo",
		WorktreePath: "/dev/bravo-worktrees/flow-repair",
		Branch:       "flow/repair",
		AutoMode:     true,
		Phases: []flowstore.FlowPhase{
			{
				PhaseID:   "repair-work",
				Title:     "Repair work",
				Kind:      flowstore.KindImplementation,
				Status:    repairStatus,
				DependsOn: []string{},
				Order:     1,
			},
			{
				PhaseID:   "successor",
				Title:     "Successor",
				Kind:      flowstore.KindReviewLoop,
				Status:    successorStatus,
				DependsOn: []string{"repair-work"},
				Order:     2,
			},
		},
	}
}

func cleanRepairAutoDrainMarker(minimumRequest uint64) repairAutoDrainMarker {
	return repairAutoDrainMarker{MinimumRequest: minimumRequest, CleanExit: true}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestRepairExitAutoDrainWaitsForPostExitPollGenerationAndLaunchesOnce(t *testing.T) {
	previous := repairAutoDrainTestFlow(flowstore.PhasePending, flowstore.KindImplementation, true)
	ready := repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, true)
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return ready, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.autoAdvanceRequestSeq = 7
	m.autoAdvanceInFlight = 7
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{{
		Number:     1,
		Scope:      embeddedTerminalScopeFlow,
		FlowID:     previous.FlowID,
		FlowRepair: true,
		ID:         1,
		Terminal:   internalFakeEmbeddedTerminal{state: "exited"},
	}}

	m = m.handleEmbeddedTerminalClosePrefix()
	if got := m.pendingRepairAutoDrainFlowIDs[previous.FlowID]; got.MinimumRequest != 8 || !got.CleanExit {
		t.Fatalf("repair marker = %#v, want clean generation 8", got)
	}
	if len(m.embeddedTerminals) != 0 {
		t.Fatalf("repair terminal retained after user close: %#v", m.embeddedTerminals)
	}

	// This request began before the repair exited. Even though it is still the
	// current request and succeeds, it cannot observe the repair result.
	var cmd tea.Cmd
	m, cmd = m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{previous}, Request: 7})
	if len(updates) != 0 {
		t.Fatalf("pre-exit poll launched updates %#v", updates)
	}
	if got := m.pendingRepairAutoDrainFlowIDs[previous.FlowID]; got.MinimumRequest != 8 || !got.CleanExit {
		t.Fatalf("pre-exit poll changed marker to %#v", got)
	}

	m.autoAdvanceRequestSeq = 8
	m.autoAdvanceInFlight = 8
	m, cmd = m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{ready}, Request: 8})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if len(updates) != 1 || updates[0].PhaseID != "successor" || !updates[0].AutoLaunch {
		t.Fatalf("post-exit updates = %#v, want one automatic successor", updates)
	}
	if !launch.LaunchContext.Headless || launch.LaunchContext.FlowRepair || launch.LaunchContext.FlowPhaseID != "successor" {
		t.Fatalf("repair-origin successor launch = %#v", launch.LaunchContext)
	}
	if len(m.pendingRepairAutoDrainFlowIDs) != 0 {
		t.Fatalf("repair marker not consumed: %#v", m.pendingRepairAutoDrainFlowIDs)
	}

	// Repeating the same successful snapshot cannot queue another successor.
	m.autoAdvanceRequestSeq = 9
	m.autoAdvanceInFlight = 9
	m, _ = m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{ready}, Request: 9})
	if len(updates) != 1 {
		t.Fatalf("repeated snapshot queued duplicate updates %#v", updates)
	}
}

func TestCleanRepairMarkerSuppressesPreExitCompletionEdgeUntilEligiblePoll(t *testing.T) {
	previous := repairCompletionEdgeTestFlow(flowstore.PhaseRunning, flowstore.PhasePending)
	current := repairCompletionEdgeTestFlow(flowstore.PhaseCompleted, flowstore.PhaseReady)
	updates := 0
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates++
			return current, nil
		},
	})
	m.autoAdvanceRequestSeq = 7
	m.pendingRepairAutoDrainFlowIDs = map[string]repairAutoDrainMarker{
		current.FlowID: cleanRepairAutoDrainMarker(8),
	}

	m, cmd, _ := m.prepareAutoFlowPhaseLaunchForRequest(
		[]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current}, 7,
	)
	if cmd != nil || updates != 0 || len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("pre-exit completion edge escaped fence: cmd=%T updates=%d drains=%#v", cmd, updates, m.autoAdvanceDrainFlows)
	}
	if marker := m.pendingRepairAutoDrainFlowIDs[current.FlowID]; marker != cleanRepairAutoDrainMarker(8) {
		t.Fatalf("pre-exit poll changed clean marker to %#v", marker)
	}

	m, cmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(
		[]flowstore.FlowRecord{current}, []flowstore.FlowRecord{current}, 8,
	)
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if updates != 1 || launch.LaunchContext.FlowPhaseID != "successor" {
		t.Fatalf("eligible clean handoff: updates=%d launch=%#v", updates, launch.LaunchContext)
	}
}

func TestRepairAutoDrainMarkerSurvivesStaleAndFailedEligiblePolls(t *testing.T) {
	ready := repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, true)
	updates := 0
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates++
			return ready, nil
		},
	})
	m.pendingRepairAutoDrainFlowIDs = map[string]repairAutoDrainMarker{ready.FlowID: cleanRepairAutoDrainMarker(4)}
	m.autoAdvanceRequestSeq = 4
	m.autoAdvanceInFlight = 4

	m, _ = m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{ready}, Request: 3})
	if m.pendingRepairAutoDrainFlowIDs[ready.FlowID] != cleanRepairAutoDrainMarker(4) {
		t.Fatal("stale poll consumed repair marker")
	}
	m, _ = m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Err: "temporary failure", Request: 4})
	if m.pendingRepairAutoDrainFlowIDs[ready.FlowID] != cleanRepairAutoDrainMarker(4) {
		t.Fatal("failed eligible poll consumed repair marker")
	}

	m.autoAdvanceRequestSeq = 5
	m.autoAdvanceInFlight = 5
	m, cmd := m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{ready}, Request: 5})
	_ = firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if updates != 1 || len(m.pendingRepairAutoDrainFlowIDs) != 0 {
		t.Fatalf("successful retry: updates=%d markers=%#v", updates, m.pendingRepairAutoDrainFlowIDs)
	}
}

func TestRepairAutoDrainClearsWithoutLaunchingWhenAutoOffOrOnlyMergeReady(t *testing.T) {
	merged := repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, true)
	merged.Merge.Status = flowstore.MergeMerged
	abandoned := repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, true)
	abandoned.Status = flowstore.StatusAbandoned
	for _, tt := range []struct {
		name   string
		record flowstore.FlowRecord
	}{
		{name: "auto off", record: repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, false)},
		{name: "merge boundary", record: repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindMerge, true)},
		{name: "no successor", record: repairAutoDrainTestFlow(flowstore.PhasePending, flowstore.KindImplementation, true)},
		{name: "completed flow", record: repairAutoDrainTestFlow(flowstore.PhaseCompleted, flowstore.KindImplementation, true)},
		{name: "merged flow", record: merged},
		{name: "abandoned flow", record: abandoned},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updates := 0
			m := NewWithOptions(flowRefreshTestRepos(), Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					updates++
					return tt.record, nil
				},
			})
			m.pendingRepairAutoDrainFlowIDs = map[string]repairAutoDrainMarker{tt.record.FlowID: cleanRepairAutoDrainMarker(1)}
			var cmd tea.Cmd
			m, cmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(nil, []flowstore.FlowRecord{tt.record}, 1)
			if cmd != nil {
				_ = cmd()
			}
			if updates != 0 || len(m.pendingRepairAutoDrainFlowIDs) != 0 || len(m.autoAdvanceDrainFlows) != 0 {
				t.Fatalf("updates=%d repair=%#v drain=%#v", updates, m.pendingRepairAutoDrainFlowIDs, m.autoAdvanceDrainFlows)
			}
		})
	}
}

func TestRepairTerminalRemovalRecordsCleanAndSuppressingOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		repair    bool
		reason    embeddedTerminalRemovalReason
		wantClean *bool
	}{
		{name: "user closes exited repair", state: "exited", repair: true, reason: embeddedTerminalRemovalUserClose, wantClean: boolPointer(true)},
		{name: "tick closes exited repair", state: "exited", repair: true, reason: embeddedTerminalRemovalAutoClose, wantClean: boolPointer(true)},
		{name: "ordinary phase exits", state: "exited", reason: embeddedTerminalRemovalAutoClose},
		{name: "failed repair dismissed", state: "failed", repair: true, reason: embeddedTerminalRemovalUserClose, wantClean: boolPointer(false)},
		{name: "terminated repair dismissed", state: "terminated", repair: true, reason: embeddedTerminalRemovalUserClose, wantClean: boolPointer(false)},
		{name: "running repair detached", state: "running", repair: true, reason: embeddedTerminalRemovalDetach, wantClean: boolPointer(false)},
		{name: "exited repair detached", state: "exited", repair: true, reason: embeddedTerminalRemovalDetach, wantClean: boolPointer(false)},
		{name: "repair prefill failed", state: "terminated", repair: true, reason: embeddedTerminalRemovalPrefillFailure, wantClean: boolPointer(false)},
		{name: "repair terminated", state: "terminated", repair: true, reason: embeddedTerminalRemovalTerminate, wantClean: boolPointer(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				autoAdvanceRequestSeq: 11,
				embeddedTerminals: []embeddedTerminalSlot{{
					FlowID:     "flow-repair",
					FlowRepair: tt.repair,
					Scope:      embeddedTerminalScopeFlow,
					ID:         1,
					Terminal:   internalFakeEmbeddedTerminal{state: tt.state},
				}},
			}
			m = m.dismissEmbeddedTerminalForReason(1, tt.reason)
			marker, got := m.pendingRepairAutoDrainFlowIDs["flow-repair"]
			if tt.wantClean == nil {
				if got {
					t.Fatalf("ordinary phase recorded repair marker %#v", marker)
				}
				return
			}
			if !got || marker.MinimumRequest != 12 || marker.CleanExit != *tt.wantClean {
				t.Fatalf("repair marker = %#v, present=%v, want clean=%v generation 12", marker, got, *tt.wantClean)
			}
		})
	}
}

func TestNonCleanRepairRemovalSuppressesRepairCompletionEdge(t *testing.T) {
	previous := repairCompletionEdgeTestFlow(flowstore.PhaseRunning, flowstore.PhasePending)
	current := repairCompletionEdgeTestFlow(flowstore.PhaseCompleted, flowstore.PhaseReady)
	tests := []struct {
		name   string
		state  string
		reason embeddedTerminalRemovalReason
	}{
		{name: "failed", state: "failed", reason: embeddedTerminalRemovalUserClose},
		{name: "terminated", state: "terminated", reason: embeddedTerminalRemovalUserClose},
		{name: "detached", state: "running", reason: embeddedTerminalRemovalDetach},
	}
	for _, tt := range tests {
		for _, removalFirst := range []bool{false, true} {
			name := "completion before removal"
			if removalFirst {
				name = "removal before completion poll"
			}
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				updates := 0
				m := NewWithOptions(flowRefreshTestRepos(), Options{
					AgentCommand: "codex",
					AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
						updates++
						return current, nil
					},
				})
				m.autoAdvanceRequestSeq = 1
				m.embeddedTerminals = []embeddedTerminalSlot{{
					Scope:      embeddedTerminalScopeFlow,
					FlowID:     current.FlowID,
					FlowRepair: true,
					ID:         1,
					Terminal:   internalFakeEmbeddedTerminal{state: tt.state},
				}}

				var cmd tea.Cmd
				if removalFirst {
					m = m.dismissEmbeddedTerminalForReason(1, tt.reason)
					m, cmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(
						[]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current}, 1,
					)
				} else {
					m, _, _ = m.prepareAutoFlowPhaseLaunchForRequest(
						[]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current}, 1,
					)
					m = m.dismissEmbeddedTerminalForReason(1, tt.reason)
					m.autoAdvanceRequestSeq = 2
					m, cmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(
						[]flowstore.FlowRecord{current}, []flowstore.FlowRecord{current}, 2,
					)
				}
				if cmd != nil {
					if message := cmd(); message != nil {
						if _, ok := message.(FlowEmbeddedLaunchRequestedMsg); ok {
							t.Fatalf("non-clean repair returned successor launch message %#v", message)
						}
					}
				}

				if updates != 0 || len(m.autoAdvanceDrainFlows) != 0 {
					t.Fatalf("non-clean repair queued successor: updates=%d drains=%#v", updates, m.autoAdvanceDrainFlows)
				}
			})
		}
	}
}

func TestDetachedRepairSuppressionWaitsForLateMutation(t *testing.T) {
	before := repairCompletionEdgeTestFlow(flowstore.PhaseBlocked, flowstore.PhasePending)
	after := repairCompletionEdgeTestFlow(flowstore.PhaseCompleted, flowstore.PhaseReady)
	updates := 0
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates++
			return after, nil
		},
	})
	m.embeddedTerminals = []embeddedTerminalSlot{{
		Scope:      embeddedTerminalScopeFlow,
		FlowID:     before.FlowID,
		FlowRepair: true,
		ID:         1,
		Terminal:   internalFakeEmbeddedTerminal{state: "running"},
	}}
	m = m.dismissEmbeddedTerminalForReason(1, embeddedTerminalRemovalDetach)

	m.autoAdvanceRequestSeq = 1
	m, cmd, _ := m.prepareAutoFlowPhaseLaunchForRequest(
		[]flowstore.FlowRecord{before}, []flowstore.FlowRecord{before}, 1,
	)
	if cmd != nil {
		t.Fatalf("unchanged post-detach poll returned command %T", cmd)
	}
	if _, ok := m.pendingRepairAutoDrainFlowIDs[before.FlowID]; !ok {
		t.Fatal("unchanged post-detach poll consumed suppression marker")
	}

	m.autoAdvanceRequestSeq = 2
	m, cmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(
		[]flowstore.FlowRecord{before}, []flowstore.FlowRecord{after}, 2,
	)
	if cmd != nil {
		t.Fatalf("late detached repair mutation returned command %T", cmd)
	}
	if updates != 0 || len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("late detached mutation: updates=%d drains=%#v", updates, m.autoAdvanceDrainFlows)
	}
	if _, ok := m.pendingRepairAutoDrainFlowIDs[after.FlowID]; ok {
		t.Fatal("launchable detached mutation did not consume suppression marker")
	}
}

func TestSuccessfulRepairRetryOverridesEarlierSuppressingOutcome(t *testing.T) {
	ready := repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, true)
	updates := 0
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates++
			return ready, nil
		},
	})
	m.autoAdvanceRequestSeq = 4
	m = m.suppressRepairAutoDrain(ready.FlowID)
	m.autoAdvanceRequestSeq = 5
	m = m.armRepairAutoDrain(ready.FlowID)
	marker := m.pendingRepairAutoDrainFlowIDs[ready.FlowID]
	if !marker.CleanExit || marker.MinimumRequest != 6 {
		t.Fatalf("successful retry marker = %#v, want clean generation 6", marker)
	}

	m, cmd, _ := m.prepareAutoFlowPhaseLaunchForRequest(nil, []flowstore.FlowRecord{ready}, 6)
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if updates != 1 || launch.LaunchContext.FlowPhaseID != "successor" {
		t.Fatalf("successful retry handoff: updates=%d launch=%#v", updates, launch.LaunchContext)
	}
}

func TestRepairAutoDrainRearmsAfterRunningToReadyStopTransition(t *testing.T) {
	previous := repairAutoDrainTestFlow(flowstore.PhaseRunning, flowstore.KindImplementation, true)
	ready := repairAutoDrainTestFlow(flowstore.PhaseReady, flowstore.KindImplementation, true)
	updates := 0
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates++
			return ready, nil
		},
	})
	m.autoAdvanceDrainFlows = map[string]struct{}{ready.FlowID: {}}
	m.pendingRepairAutoDrainFlowIDs = map[string]repairAutoDrainMarker{ready.FlowID: cleanRepairAutoDrainMarker(1)}
	m, cmd, _ := m.prepareAutoFlowPhaseLaunchForRequest([]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{ready}, 1)
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if updates != 1 || launch.LaunchContext.FlowPhaseID != "successor" {
		t.Fatalf("running-to-ready repair handoff: updates=%d launch=%#v", updates, launch.LaunchContext)
	}
}

func TestRepairAutoDrainUsesCustomDAGOrdering(t *testing.T) {
	record := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseReady,
		"branch-b": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return record, nil
		},
	})
	m.pendingRepairAutoDrainFlowIDs = map[string]repairAutoDrainMarker{record.FlowID: cleanRepairAutoDrainMarker(1)}
	m, cmd, _ := m.prepareAutoFlowPhaseLaunchForRequest(nil, []flowstore.FlowRecord{record}, 1)
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if len(updates) != 1 || updates[0].PhaseID != "branch-a" || launch.LaunchContext.FlowPhaseID != "branch-a" {
		t.Fatalf("custom DAG repair handoff: updates=%#v launch=%#v", updates, launch.LaunchContext)
	}
}

func TestAutoModeResetToReadyDisarmsExistingDrain(t *testing.T) {
	branchARunning := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseRunning,
		"branch-b": flowstore.PhaseReady,
	})
	branchAReset := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseReady,
		"branch-b": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return branchAReset, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{branchARunning}
	m.autoAdvanceDrainFlows = map[string]struct{}{"flow-1": {}}

	m, cmd, _ := m.prepareAutoFlowPhaseLaunch([]flowstore.FlowRecord{branchARunning}, []flowstore.FlowRecord{branchAReset})
	if cmd != nil {
		t.Fatal("reset-to-ready queued successor launch, want drain disarmed")
	}
	if len(updates) != 0 {
		t.Fatalf("updates after reset-to-ready = %#v, want none", updates)
	}
	if len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want reset-to-ready to disarm drain", m.autoAdvanceDrainFlows)
	}
}

func TestAutoModeAutoreviewSuccessorLaunches(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"pr-creation": flowstore.PhaseCompleted,
		"autoreview":  flowstore.PhaseRunning,
	})
	current := previous
	current.Phases = append([]flowstore.FlowPhase(nil), previous.Phases...)
	current.Phases[5].Status = flowstore.PhaseCompleted
	current.Phases[6] = flowstore.FlowPhase{PhaseID: "summary", Title: "Summary", Kind: "", Status: flowstore.PhaseReady, Order: 7, DependsOn: []string{"autoreview"}}
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	_, cmd := runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "summary" {
		t.Fatalf("autoreview successor launch = %q, want summary", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "summary" {
		t.Fatalf("updates = %#v, want summary", updates)
	}
}

func TestAutoModeDisableDisarmsDrain(t *testing.T) {
	current := autoAdvanceCustomFlow(map[string]string{
		"root":     flowstore.PhaseCompleted,
		"branch-a": flowstore.PhaseReady,
	})
	current.AutoMode = false
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{current}
	m.autoAdvanceDrainFlows = map[string]struct{}{"flow-1": {}}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if len(updates) != 0 {
		t.Fatalf("updates after disabling AutoMode = %#v, want none", updates)
	}
	if len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want disabled flow disarmed", m.autoAdvanceDrainFlows)
	}
}

func TestAutoModeDrainDisarmsWhenSchedulingLaunch(t *testing.T) {
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddFlowPhaseLaunchID() should not run until the queued command executes: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
	})
	m.autoAdvanceDrainFlows = map[string]struct{}{"flow-1": {}}

	m, cmd := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("prepareAutoAdvanceDrainLaunches() returned nil, want queued launch command")
	}
	if len(m.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want scheduling launch to disarm drain", m.autoAdvanceDrainFlows)
	}
	_, duplicate := m.prepareAutoAdvanceDrainLaunches([]flowstore.FlowRecord{current})
	if duplicate != nil {
		t.Fatal("second drain pass queued duplicate launch before first command executed")
	}
}

func collectMsgsFromCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		msgs := make([]tea.Msg, 0, len(batch))
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			msgs = append(msgs, sub())
		}
		return msgs
	}
	return []tea.Msg{msg}
}

func hasAutoAdvanceTickMsg(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	for _, msg := range collectMsgsFromCmd(t, cmd) {
		if _, ok := msg.(autoAdvanceTickMsg); ok {
			return true
		}
	}
	return false
}

func TestModel_AutoAdvanceTickIntervalIsOneSecond(t *testing.T) {
	if autoAdvanceTickInterval != time.Second {
		t.Fatalf("autoAdvanceTickInterval = %s, want %s", autoAdvanceTickInterval, time.Second)
	}
}

func TestModel_AutoAdvanceTickScheduledFromInitInFlowsMode(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	if !hasAutoAdvanceTickMsg(t, m.Init()) {
		t.Fatal("Init() in flows mode should schedule the auto-advance tick alongside the fetch")
	}
}

func TestModel_AutoAdvanceTickScheduledFromInitWithoutRepos(t *testing.T) {
	m := NewWithOptions(nil, Options{StartupMode: ui.ModeFlows})
	if !hasAutoAdvanceTickMsg(t, m.Init()) {
		t.Fatal("Init() with no flows fetch should still schedule the auto-advance tick")
	}
}

func TestModel_AutoAdvanceTickDoesNotOverlapInFlightFetch(t *testing.T) {
	var calls int
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			calls++
			return nil, nil
		},
	})
	m.mode = ui.ModeWorktrees

	m, cmd := updateFlowRefreshTest(m, autoAdvanceTickMsg{})
	if cmd == nil {
		t.Fatal("first tick should start an auto-advance fetch")
	}
	inFlight := m.autoAdvanceInFlight

	m, duplicateCmd := updateFlowRefreshTest(m, autoAdvanceTickMsg{})
	if duplicateCmd != nil {
		t.Fatalf("tick during in-flight fetch returned command %T, want nil", duplicateCmd)
	}
	if m.autoAdvanceInFlight != inFlight {
		t.Fatalf("auto-advance in-flight request = %d, want unchanged %d", m.autoAdvanceInFlight, inFlight)
	}
	if m.autoAdvanceRequestSeq != inFlight {
		t.Fatalf("auto-advance request seq = %d, want unchanged %d", m.autoAdvanceRequestSeq, inFlight)
	}
}

func TestModel_AutoAdvanceStaleResultDoesNotSpawnDuplicateTickChain(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m.mode = ui.ModeWorktrees
	m, _ = updateFlowRefreshTest(m, autoAdvanceTickMsg{})
	inFlight := m.autoAdvanceInFlight

	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Request: inFlight + 100})
	if cmd != nil {
		t.Fatalf("stale auto-advance result returned command %T, want nil", cmd)
	}
	if m.autoAdvanceInFlight != inFlight {
		t.Fatalf("auto-advance in-flight request = %d, want unchanged %d", m.autoAdvanceInFlight, inFlight)
	}
}

func TestModel_AutoAdvanceTickRunsOffTheFlowSurface(t *testing.T) {
	var filters []flowstore.FlowFilter
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	m.mode = ui.ModeWorktrees

	var tick tea.Msg
	for _, msg := range collectMsgsFromCmd(t, m.Init()) {
		if _, ok := msg.(autoAdvanceTickMsg); ok {
			tick = msg
		}
	}
	if tick == nil {
		t.Fatal("Init() outside flows mode should schedule the auto-advance tick")
	}

	m, cmd := updateFlowRefreshTest(m, tick)
	if cmd == nil {
		t.Fatal("auto-advance tick should start an unscoped flow fetch")
	}
	if m.autoAdvanceInFlight == 0 {
		t.Fatal("auto-advance fetch should be tracked in flight")
	}
	filters = nil
	msg := cmd()
	result, ok := msg.(AutoAdvanceResultMsg)
	if !ok {
		t.Fatalf("auto-advance fetch returned %T, want AutoAdvanceResultMsg", msg)
	}
	if len(filters) != 1 || filters[0].RepoPath != "" {
		t.Fatalf("auto-advance fetch filters = %#v, want one unscoped fetch", filters)
	}
	if result.Request != m.autoAdvanceInFlight {
		t.Fatalf("AutoAdvanceResultMsg.Request = %d, want in-flight request %d", result.Request, m.autoAdvanceInFlight)
	}
	if len(result.Flows) != 1 || result.Flows[0].FlowID != "flow-1" {
		t.Fatalf("AutoAdvanceResultMsg.Flows = %#v, want flow-1", result.Flows)
	}

	m, cmd = updateFlowRefreshTest(m, result)
	if m.autoAdvanceInFlight != 0 {
		t.Fatalf("auto-advance in-flight request = %d, want cleared", m.autoAdvanceInFlight)
	}
	if cmd == nil {
		t.Fatal("auto-advance result should reschedule the advance tick")
	}
}

func TestModel_AutoAdvanceLaunchesOffViewForUnscopedFlowCompletion(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.mode = ui.ModeWorktrees
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	m, cmd := runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].FlowID != "flow-1" {
		t.Fatalf("autoAdvanceSnapshot = %#v, want current flow", m.autoAdvanceSnapshot)
	}
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowID != "flow-1" ||
		launch.LaunchContext.RepoPath != "/dev/bravo" ||
		launch.LaunchContext.FlowPhaseID != "implementation" ||
		!launch.LaunchContext.Headless ||
		!launch.LaunchContext.FlowLaunchTracked {
		t.Fatalf("auto-advance launch context = %#v", launch.LaunchContext)
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates = %#v, want implementation auto launch", updates)
	}
	if m.status.Source != statusFlowAutoAdvance || m.status.Text != "Flow Bravo Flow: implementation queued" {
		t.Fatalf("status = %#v, want auto-advance queued status", m.status)
	}
}

func TestModel_AutoAdvanceQueuedStatusDoesNotClaimStartedBeforeCommandRuns(t *testing.T) {
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	previous := current
	previous.Phases = append([]flowstore.FlowPhase(nil), current.Phases...)
	previous.Phases[1].Status = flowstore.PhaseRunning
	previous.Phases[2].Status = flowstore.PhasePending

	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	m, cmd := runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("auto-advance should queue a launch command")
	}
	if m.status.Source != statusFlowAutoAdvance || m.status.Text != "Flow Bravo Flow: implementation queued" {
		t.Fatalf("status = %#v, want queued status before async launch command runs", m.status)
	}
	if m.status.Text == "Flow Bravo Flow: implementation started" {
		t.Fatalf("status = %#v, should not claim async launch started before command runs", m.status)
	}
}

func TestModel_AutoAdvancePreflightFailureDoesNotStompExistingStatus(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	m := NewWithOptions(flowRefreshTestRepos(), Options{})
	m.mode = ui.ModeWorktrees
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.status = statusError{Source: statusOther, Text: "keep this"}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if m.status.Source != statusOther || m.status.Text != "keep this" {
		t.Fatalf("status = %#v, want existing status preserved after auto preflight failure", m.status)
	}
}

func TestModel_AutoAdvancePreflightFailureSetsAutoAdvanceStatus(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	m := NewWithOptions(flowRefreshTestRepos(), Options{})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.autoAdvanceInFlight = 1

	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	if cmd == nil {
		t.Fatal("preflight failure should still reschedule status/tick commands")
	}
	if m.status.Source != statusFlowAutoAdvance || !strings.Contains(m.status.Text, "choose codex") {
		t.Fatalf("status = %#v, want auto-advance preflight guidance", m.status)
	}
}

func TestModel_AutoAdvancePreflightFailurePreservesCompletionEdge(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.autoAdvanceInFlight = 1

	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	if cmd == nil {
		t.Fatal("preflight failure should still reschedule the auto-advance tick")
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates after preflight failure = %#v, want none", updates)
	}
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].Phases[2].Status != flowstore.PhaseReady {
		t.Fatalf("autoAdvanceSnapshot = %#v, want current ready snapshot", m.autoAdvanceSnapshot)
	}
	if _, ok := m.autoAdvanceDrainFlows["flow-1"]; !ok {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want flow-1 armed", m.autoAdvanceDrainFlows)
	}

	m.agentCommand = "codex"
	m.autoAdvanceInFlight = 2
	m, cmd = updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 2})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("auto-advance launch phase = %q, want implementation", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates after config fix = %#v, want implementation auto launch", updates)
	}
}

func TestModel_AutoAdvanceActionFailureSetsStatusOffRepo(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{})
	m.mode = ui.ModeWorktrees

	m, cmd := updateFlowRefreshTest(m, ActionFailedMsg{
		RepoPath:                "/dev/bravo",
		Err:                     "failed to mark flow phase running: store unavailable",
		AutoAdvanceRetryFlowID:  "flow-1",
		AutoAdvanceRetryPhaseID: "implementation",
	})
	if cmd == nil {
		t.Fatal("off-repo auto-advance failure should schedule status expiry")
	}
	if m.status.Source != statusFlowAutoAdvance ||
		m.status.Text != "Flow flow-1: failed to mark flow phase running: store unavailable" {
		t.Fatalf("status = %#v, want off-repo auto-advance failure", m.status)
	}
	if _, ok := m.autoAdvanceDrainFlows["flow-1"]; !ok {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want flow-1 rearmed", m.autoAdvanceDrainFlows)
	}
}

func TestModel_AutoAdvanceAsyncLaunchFailurePreservesCompletionEdge(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	failLaunchUpdate := true
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			if failLaunchUpdate {
				return flowstore.FlowRecord{}, errors.New("store unavailable")
			}
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.autoAdvanceInFlight = 1

	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	if cmd == nil {
		t.Fatal("auto-advance should queue launch command before async failure")
	}
	failed := false
	raw := cmd()
	if batch, ok := raw.(tea.BatchMsg); ok {
		for _, subcmd := range batch {
			if msg, ok := subcmd().(ActionFailedMsg); ok {
				failed = true
				m, _ = updateFlowRefreshTest(m, msg)
				break
			}
		}
	} else if msg, ok := raw.(ActionFailedMsg); ok {
		failed = true
		m, _ = updateFlowRefreshTest(m, msg)
	}
	if !failed {
		t.Fatal("queued auto-advance command should report ActionFailedMsg")
	}
	if _, ok := m.autoAdvanceDrainFlows["flow-1"]; !ok {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want flow-1 rearmed", m.autoAdvanceDrainFlows)
	}

	failLaunchUpdate = false
	m.autoAdvanceInFlight = 2
	m, cmd = updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 2})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("auto-advance launch phase = %q, want implementation", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates after async failure = %#v, want retried implementation auto launch", updates)
	}
}

func TestModel_AutoAdvanceDeferredPreflightFailurePreservesRetry(t *testing.T) {
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{current}
	m.autoAdvanceDrainFlows = map[string]struct{}{"flow-1": {}}
	m.autoAdvanceInFlight = 1

	m, _ = updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	if _, ok := m.autoAdvanceDrainFlows["flow-1"]; !ok {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want retry preserved after preflight failure", m.autoAdvanceDrainFlows)
	}
	if m.status.Source != statusFlowAutoAdvance || !strings.Contains(m.status.Text, "choose codex") {
		t.Fatalf("status = %#v, want auto-advance preflight guidance", m.status)
	}

	m.agentCommand = "codex"
	m.autoAdvanceInFlight = 2
	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 2})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("deferred auto-advance launch phase = %q, want implementation", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates after deferred preflight fix = %#v, want implementation auto launch", updates)
	}
}

func TestModel_AutoAdvancePrimesSnapshotWithoutStartupLaunch(t *testing.T) {
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none on first sighting", updates)
	}
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].FlowID != "flow-1" {
		t.Fatalf("autoAdvanceSnapshot = %#v, want primed current flow", m.autoAdvanceSnapshot)
	}
}

func TestModel_AutoAdvanceDisplayFetchSeedsFirstSnapshotForStartupEdge(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/alpha", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoAdvanceTestFlow("flow-1", "/dev/alpha", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode:  ui.ModeFlows,
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if len(updates) != 0 {
		t.Fatalf("display seed launch updates = %#v, want none", updates)
	}
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].Phases[1].Status != flowstore.PhaseRunning {
		t.Fatalf("autoAdvanceSnapshot = %#v, want display fetch running baseline", m.autoAdvanceSnapshot)
	}

	m.autoAdvanceInFlight = 1
	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("auto-advance launch phase = %q, want implementation", launch.LaunchContext.FlowPhaseID)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates = %#v, want implementation auto launch", updates)
	}
}

func TestModel_AutoAdvanceDisplayFetchMergesNewFlowIntoExistingSnapshot(t *testing.T) {
	existing := autoAdvanceTestFlow("flow-1", "/dev/alpha", true, map[string]string{
		"plan":        flowstore.PhaseCompleted,
		"plan-review": flowstore.PhaseRunning,
	})
	newRunning := autoAdvanceTestFlow("flow-2", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	newCompleted := autoAdvanceTestFlow("flow-2", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode:  ui.ModeActiveFlows,
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return newCompleted, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{existing}

	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{
		Flows:       []flowstore.FlowRecord{existing, newRunning},
		ListRequest: m.ListRequest(ui.ModeActiveFlows),
	})
	if len(m.autoAdvanceSnapshot) != 2 {
		t.Fatalf("autoAdvanceSnapshot = %#v, want existing baseline plus new display flow", m.autoAdvanceSnapshot)
	}

	m.autoAdvanceInFlight = 1
	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{
		Flows:   []flowstore.FlowRecord{existing, newCompleted},
		Request: 1,
	})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowID != "flow-2" || launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("auto-advance launch context = %#v, want flow-2 implementation", launch.LaunchContext)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates = %#v, want implementation auto launch", updates)
	}
}

func TestModel_AutoAdvanceDisplayFetchRefreshesExistingFlowRerunBaseline(t *testing.T) {
	completedBaseline := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhasePending,
	})
	rerunRunning := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	rerunCompleted := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode:  ui.ModeActiveFlows,
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return rerunCompleted, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{completedBaseline}

	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{
		Flows:       []flowstore.FlowRecord{rerunRunning},
		ListRequest: m.ListRequest(ui.ModeActiveFlows),
	})
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].Phases[1].Status != flowstore.PhaseRunning {
		t.Fatalf("autoAdvanceSnapshot = %#v, want display rerun running baseline", m.autoAdvanceSnapshot)
	}

	m.autoAdvanceInFlight = 1
	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{
		Flows:   []flowstore.FlowRecord{rerunCompleted},
		Request: 1,
	})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowID != "flow-1" || launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("auto-advance launch context = %#v, want implementation after rerun", launch.LaunchContext)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates = %#v, want implementation auto launch after rerun", updates)
	}
}

func TestModel_AutoAdvanceDisplayFetchRefreshesNewRunningChildBaseline(t *testing.T) {
	baseline := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhasePending,
	})
	childRunning := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhasePending,
	})
	childRunning.Phases = append(childRunning.Phases, flowstore.FlowPhase{
		PhaseID:       "implementation-api",
		ParentPhaseID: "implementation",
		Title:         "API integration",
		Kind:          "implementation_child",
		Status:        flowstore.PhaseRunning,
		Order:         10,
	})
	childCompleted := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhaseReady,
	})
	childCompleted.Phases = append(childCompleted.Phases, flowstore.FlowPhase{
		PhaseID:       "implementation-api",
		ParentPhaseID: "implementation",
		Title:         "API integration",
		Kind:          "implementation_child",
		Status:        flowstore.PhaseCompleted,
		Order:         10,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode:  ui.ModeActiveFlows,
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return childCompleted, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{baseline}

	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{
		Flows:       []flowstore.FlowRecord{childRunning},
		ListRequest: m.ListRequest(ui.ModeActiveFlows),
	})
	if len(m.autoAdvanceSnapshot) != 1 || len(m.autoAdvanceSnapshot[0].Phases) != len(childRunning.Phases) {
		t.Fatalf("autoAdvanceSnapshot = %#v, want display baseline with running child", m.autoAdvanceSnapshot)
	}

	m.autoAdvanceInFlight = 1
	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{
		Flows:   []flowstore.FlowRecord{childCompleted},
		Request: 1,
	})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowID != "flow-1" || launch.LaunchContext.FlowPhaseID != "review-loop" {
		t.Fatalf("auto-advance launch context = %#v, want review-loop after child completion", launch.LaunchContext)
	}
	if len(updates) != 1 || updates[0].PhaseID != "review-loop" || !updates[0].AutoLaunch {
		t.Fatalf("launch updates = %#v, want review-loop auto launch after child completion", updates)
	}
}

func TestModel_AutoAdvanceRequiresCompletionEdgeAndAutoMode(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	current := previous
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if len(updates) != 0 {
		t.Fatalf("ready phase without completion edge launched: %#v", updates)
	}

	previous.Phases[1].Status = flowstore.PhaseRunning
	current.Phases[1].Status = flowstore.PhaseCompleted
	current.AutoMode = false
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if len(updates) != 0 {
		t.Fatalf("non-auto flow launched: %#v", updates)
	}
}

func TestModel_AutoAdvanceDeferredLaunchResolvesFromPrivateSnapshotOffView(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	previous.Phases[1].LaunchIDs = []string{"source-launch"}
	current := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	current.Phases[1].LaunchIDs = []string{"source-launch"}
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.mode = ui.ModeWorktrees
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.embeddedTerminals = []embeddedTerminalSlot{{
		Scope:       embeddedTerminalScopeFlow,
		FlowID:      "flow-1",
		FlowPhaseID: "plan-review",
		LaunchID:    "source-launch",
		Terminal:    flowPhaseLaunchTestTerminal{state: "running"},
	}}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	if len(updates) != 0 {
		t.Fatalf("launch updates while source terminal runs = %#v, want none", updates)
	}
	if _, ok := m.autoAdvanceDrainFlows["flow-1"]; !ok {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want flow-1 armed while source terminal runs", m.autoAdvanceDrainFlows)
	}

	m.embeddedTerminals = nil
	m.flows = m.flows.SetItems(nil)
	m, cmd := runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{current})
	launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, cmd)
	if launch.LaunchContext.FlowID != "flow-1" || launch.LaunchContext.FlowPhaseID != "implementation" {
		t.Fatalf("deferred launch context = %#v", launch.LaunchContext)
	}
	if len(updates) != 1 || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after source terminal closes = %#v, want implementation", updates)
	}
}

func TestModel_AutoAdvanceFetchErrorKeepsLoopAliveWithoutConsumingSnapshotOrStatus(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan-review": flowstore.PhaseRunning,
	})
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("boom")
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m.autoAdvanceInFlight = 7

	m, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Err: "boom", Request: 7})
	if m.autoAdvanceInFlight != 0 {
		t.Fatalf("autoAdvanceInFlight = %d, want cleared", m.autoAdvanceInFlight)
	}
	if cmd == nil {
		t.Fatal("error result should reschedule the advance tick")
	}
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].Phases[1].Status != flowstore.PhaseRunning {
		t.Fatalf("autoAdvanceSnapshot = %#v, want unchanged previous snapshot", m.autoAdvanceSnapshot)
	}
	if m.status.Text != "" {
		t.Fatalf("status = %#v, want no display status for invisible fetch error", m.status)
	}
}

func TestModel_AutoAdvanceStatusEventsExpireAndDoNotStompOtherStatus(t *testing.T) {
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"implementation": flowstore.PhaseRunning,
		"merge":          flowstore.PhasePending,
	})
	needsAttention := previous
	needsAttention.Phases = append([]flowstore.FlowPhase(nil), previous.Phases...)
	needsAttention.Phases[2].Status = flowstore.PhaseNeedsAttention
	m := New(flowRefreshTestRepos())
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}

	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{needsAttention})
	if m.status.Source != statusFlowAutoAdvance || m.status.Text != "Flow Bravo Flow: needs attention" {
		t.Fatalf("status = %#v, want needs attention auto-advance status", m.status)
	}
	seq := m.status.Seq
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: seq + 1})
	if m.status.Text == "" {
		t.Fatal("stale auto-advance status expiry should not clear status")
	}
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: seq})
	if m.status.Text != "" {
		t.Fatalf("status after matching expiry = %#v, want cleared", m.status)
	}

	mergeReady := previous
	mergeReady.Phases = append([]flowstore.FlowPhase(nil), previous.Phases...)
	mergeReady.Phases[6].Status = flowstore.PhaseReady
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{mergeReady})
	if m.status.Source != statusFlowAutoAdvance || m.status.Text != "Flow Bravo Flow: ready to merge" {
		t.Fatalf("status = %#v, want merge-ready auto-advance status", m.status)
	}

	m.status = statusError{Source: statusOther, Text: "keep this"}
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{previous}
	m, _ = runAutoAdvanceResultForTest(t, m, []flowstore.FlowRecord{needsAttention})
	if m.status.Source != statusOther || m.status.Text != "keep this" {
		t.Fatalf("status = %#v, want existing non-auto status preserved", m.status)
	}
}
