package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

func flowRefreshTestRepos() []scanner.Repo {
	return []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}
}

func updateFlowRefreshTest(m Model, msg tea.Msg) (Model, tea.Cmd) {
	tm, cmd := m.Update(msg)
	return tm.(Model), cmd
}

func firstFlowResultFromBatch(t *testing.T, cmd tea.Cmd) FlowResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("command returned %T, want BatchMsg", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("batch length = %d, want fetch plus tick", len(batch))
	}
	result, ok := batch[0]().(FlowResultMsg)
	if !ok {
		t.Fatalf("first batch command returned %T, want FlowResultMsg", result)
	}
	return result
}

func flowForRefreshTest(flowID string, phases ...flowstore.FlowPhase) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:   flowID,
		RepoPath: "/dev/alpha",
		Title:    flowID,
		Status:   flowstore.StatusInProgress,
		Phases:   phases,
	}
}

func TestModel_FlowRefreshTickScheduledOnStartupInFlowsMode(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	if m.flowRefreshTickGen == 0 {
		t.Fatal("flow refresh generation should be seeded for startup scheduling")
	}

	msg := firstFlowResultFromBatch(t, m.Init())
	if msg.RepoPath != "/dev/alpha" {
		t.Fatalf("FlowResultMsg.RepoPath = %q, want /dev/alpha", msg.RepoPath)
	}
	if msg.ListRequest != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("FlowResultMsg.ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModeFlows))
	}
}

func TestModel_FlowRefreshTickScheduledWhenEnteringFlowsMode(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	m.activePane = 1
	beforeGen := m.flowRefreshTickGen

	m, cmd := updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if m.mode != ui.ModeFlows {
		t.Fatalf("mode = %d, want flows", m.mode)
	}
	if m.flowRefreshTickGen != beforeGen+1 {
		t.Fatalf("flow refresh generation = %d, want %d", m.flowRefreshTickGen, beforeGen+1)
	}
	msg := firstFlowResultFromBatch(t, cmd)
	if msg.ListRequest != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("FlowResultMsg.ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModeFlows))
	}
}

func TestModel_FlowRefreshTickFetchesAndSchedulesNextTick(t *testing.T) {
	var calls int
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			calls++
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	if got := m.ListRequest(ui.ModeFlows); got != before+1 {
		t.Fatalf("flows list request = %d, want %d", got, before+1)
	}
	msg := firstFlowResultFromBatch(t, cmd)
	if msg.ListRequest != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("FlowResultMsg.ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModeFlows))
	}
	if calls != 1 {
		t.Fatalf("ListFlows calls = %d, want 1 after executing fetch command", calls)
	}
}

func TestModel_FlowRefreshTickIgnoresStaleGeneration(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{StartupMode: ui.ModeFlows})
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen + 1})
	if cmd != nil {
		t.Fatalf("stale tick returned command %T, want nil", cmd)
	}
	if got := m.ListRequest(ui.ModeFlows); got != before {
		t.Fatalf("flows list request = %d, want unchanged %d", got, before)
	}
}

func TestModel_FlowRefreshTickIgnoresOldLoopAfterReenteringFlows(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{})
	m.activePane = 1

	m, _ = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	oldGeneration := m.flowRefreshTickGen
	m, _ = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if m.flowRefreshTickGen == oldGeneration {
		t.Fatal("re-entering flows should advance the refresh generation")
	}
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: oldGeneration})
	if cmd != nil {
		t.Fatalf("old loop tick returned command %T, want nil", cmd)
	}
	if got := m.ListRequest(ui.ModeFlows); got != before {
		t.Fatalf("flows list request = %d, want unchanged %d", got, before)
	}
}

func TestModel_FlowRefreshTickIgnoredOutsideFlowsMode(t *testing.T) {
	var calls int
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			calls++
			return nil, nil
		},
	})
	m.activePane = 1
	m.mode = ui.ModePlans
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	if cmd != nil {
		t.Fatalf("tick outside flows returned command %T, want nil", cmd)
	}
	if got := m.ListRequest(ui.ModeFlows); got != before {
		t.Fatalf("flows list request = %d, want unchanged %d", got, before)
	}
	if calls != 0 {
		t.Fatalf("ListFlows calls = %d, want 0", calls)
	}
}

func TestModel_FlowRefreshStaleResultStillIgnored(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{StartupMode: ui.ModeFlows})
	staleRequest := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	freshRequest := m.ListRequest(ui.ModeFlows)
	if freshRequest == staleRequest {
		t.Fatal("tick should advance flows list request")
	}

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: staleRequest,
		Flows:       []flowstore.FlowRecord{flowForRefreshTest("stale-flow")},
	})
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("stale FlowResultMsg populated flows: %#v", got)
	}

	fresh := firstFlowResultFromBatch(t, cmd)
	fresh.Flows = []flowstore.FlowRecord{flowForRefreshTest("fresh-flow")}
	m, _ = updateFlowRefreshTest(m, fresh)
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "fresh-flow" {
		t.Fatalf("flows after fresh result = %#v, want fresh-flow", got)
	}
}

func TestModel_FlowRefreshPreservesExpandedPhaseSelection(t *testing.T) {
	implementation := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning}
	m := NewWithOptions(flowRefreshTestRepos(), Options{StartupMode: ui.ModeFlows})
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeFlows),
		Flows:       []flowstore.FlowRecord{flowForRefreshTest("flow-1", implementation)},
	})
	m = m.setExpandedFlowID("flow-1")
	m.selectedFlowPhaseID = "implementation"

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeFlows),
		Flows: []flowstore.FlowRecord{flowForRefreshTest("flow-1", flowstore.FlowPhase{
			PhaseID: "implementation",
			Title:   "Implementation",
			Status:  flowstore.PhaseCompleted,
		})},
	})

	if m.expandedFlowID != "flow-1" {
		t.Fatalf("expandedFlowID = %q, want flow-1", m.expandedFlowID)
	}
	if m.selectedFlowPhaseID != "implementation" {
		t.Fatalf("selectedFlowPhaseID = %q, want implementation", m.selectedFlowPhaseID)
	}
}

func TestModel_FlowRefreshClearsExpansionWhenSelectedPhaseDisappears(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{StartupMode: ui.ModeFlows})
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeFlows),
		Flows:       []flowstore.FlowRecord{flowForRefreshTest("flow-1", flowstore.FlowPhase{PhaseID: "implementation"})},
	})
	m = m.setExpandedFlowID("flow-1")
	m.selectedFlowPhaseID = "implementation"

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeFlows),
		Flows:       []flowstore.FlowRecord{flowForRefreshTest("flow-1", flowstore.FlowPhase{PhaseID: "review-loop"})},
	})

	if m.expandedFlowID != "" || m.selectedFlowPhaseID != "" {
		t.Fatalf("flow expansion = %q phase = %q, want both cleared", m.expandedFlowID, m.selectedFlowPhaseID)
	}
}

func TestModel_FlowRefreshClearsExpansionWhenExpandedFlowDisappears(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{StartupMode: ui.ModeFlows})
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeFlows),
		Flows:       []flowstore.FlowRecord{flowForRefreshTest("flow-1", flowstore.FlowPhase{PhaseID: "implementation"})},
	})
	m = m.setExpandedFlowID("flow-1")
	m.selectedFlowPhaseID = "implementation"

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeFlows),
		Flows:       []flowstore.FlowRecord{flowForRefreshTest("flow-2", flowstore.FlowPhase{PhaseID: "implementation"})},
	})

	if m.expandedFlowID != "" || m.selectedFlowPhaseID != "" {
		t.Fatalf("flow expansion = %q phase = %q, want both cleared", m.expandedFlowID, m.selectedFlowPhaseID)
	}
}
