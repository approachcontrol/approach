package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/ui"
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
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].Phases[1].Status != flowstore.PhaseRunning {
		t.Fatalf("autoAdvanceSnapshot = %#v, want previous running edge preserved", m.autoAdvanceSnapshot)
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
	previous := autoAdvanceTestFlow("flow-1", "/dev/bravo", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	m := NewWithOptions(flowRefreshTestRepos(), Options{})
	m.mode = ui.ModeWorktrees

	m, cmd := updateFlowRefreshTest(m, ActionFailedMsg{
		RepoPath:               "/dev/bravo",
		Err:                    "failed to mark flow phase running: store unavailable",
		AutoAdvanceRetryRecord: previous,
	})
	if cmd == nil {
		t.Fatal("off-repo auto-advance failure should schedule status expiry")
	}
	if m.status.Source != statusFlowAutoAdvance ||
		m.status.Text != "Flow Bravo Flow: failed to mark flow phase running: store unavailable" {
		t.Fatalf("status = %#v, want off-repo auto-advance failure", m.status)
	}
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].FlowID != "flow-1" ||
		m.autoAdvanceSnapshot[0].Phases[1].Status != flowstore.PhaseRunning {
		t.Fatalf("autoAdvanceSnapshot = %#v, want previous running edge restored", m.autoAdvanceSnapshot)
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
	if len(m.autoAdvanceSnapshot) != 1 || m.autoAdvanceSnapshot[0].Phases[1].Status != flowstore.PhaseRunning {
		t.Fatalf("autoAdvanceSnapshot = %#v, want previous running edge restored", m.autoAdvanceSnapshot)
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
	key := deferredAutoFlowLaunchKey{FlowID: "flow-1", PhaseID: "plan-review"}
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m.autoAdvanceSnapshot = []flowstore.FlowRecord{current}
	m.deferredAutoFlowLaunches = map[deferredAutoFlowLaunchKey]struct{}{key: {}}
	m.autoAdvanceInFlight = 1

	m, _ = updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{current}, Request: 1})
	if _, ok := m.deferredAutoFlowLaunches[key]; !ok {
		t.Fatalf("deferredAutoFlowLaunches = %#v, want retry preserved after preflight failure", m.deferredAutoFlowLaunches)
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
	if len(m.deferredAutoFlowLaunches) != 0 {
		t.Fatalf("deferredAutoFlowLaunches after launch = %#v, want empty", m.deferredAutoFlowLaunches)
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
	if _, ok := m.deferredAutoFlowLaunches[deferredAutoFlowLaunchKey{FlowID: "flow-1", PhaseID: "plan-review"}]; !ok {
		t.Fatalf("deferredAutoFlowLaunches = %#v, want plan-review deferred", m.deferredAutoFlowLaunches)
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
