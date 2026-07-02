package model

import (
	"errors"
	"testing"
	"time"

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

func flowResultFromCommand(t *testing.T, cmd tea.Cmd) FlowResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		if len(batch) == 0 {
			t.Fatal("batch command was empty")
		}
		msg = batch[0]()
	}
	result, ok := msg.(FlowResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowResultMsg", msg)
	}
	return result
}

func activeFlowResultFromRefreshCommand(t *testing.T, cmd tea.Cmd) ActiveFlowResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected active Flow command")
	}
	msg := cmd()
	result, ok := msg.(ActiveFlowResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want ActiveFlowResultMsg", msg)
	}
	return result
}

func flowResultFromBatchCommand(t *testing.T, cmd tea.Cmd) FlowResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if result, ok := msg.(FlowResultMsg); ok {
		return result
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowResultMsg or BatchMsg containing FlowResultMsg", msg)
	}
	for _, batchCmd := range batch {
		if result, ok := batchCmd().(FlowResultMsg); ok {
			return result
		}
	}
	t.Fatalf("batch command returned no FlowResultMsg")
	return FlowResultMsg{}
}

func fetchErrorFromCommand(t *testing.T, cmd tea.Cmd) FetchErrorMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if errMsg, ok := msg.(FetchErrorMsg); ok {
		return errMsg
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, subcmd := range batch {
			if errMsg, ok := subcmd().(FetchErrorMsg); ok {
				return errMsg
			}
		}
	}
	t.Fatalf("command returned %T, want FetchErrorMsg", msg)
	return FetchErrorMsg{}
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

func assertFlowRefreshTickSuppressed(t *testing.T, m Model) Model {
	t.Helper()
	if m.flowRefreshInFlight == 0 {
		t.Fatal("expected an in-flight flow refresh request")
	}
	inFlight := m.flowRefreshInFlight
	before := m.ListRequest(ui.ModeFlows)
	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	if cmd != nil {
		t.Fatalf("tick during in-flight refresh returned command %T, want nil", cmd)
	}
	if m.flowRefreshInFlight != inFlight {
		t.Fatalf("flow refresh in-flight request = %d, want unchanged %d", m.flowRefreshInFlight, inFlight)
	}
	if got := m.ListRequest(ui.ModeFlows); got != before {
		t.Fatalf("flows list request = %d, want unchanged %d", got, before)
	}
	return m
}

func TestModel_FlowRefreshTickIntervalIsOneSecond(t *testing.T) {
	if flowRefreshTickInterval != time.Second {
		t.Fatalf("flowRefreshTickInterval = %s, want %s", flowRefreshTickInterval, time.Second)
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
	if m.flowRefreshInFlight != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("flow refresh in-flight request = %d, want startup request %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeFlows))
	}

	msg := flowResultFromCommand(t, m.Init())
	if msg.RepoPath != "/dev/alpha" {
		t.Fatalf("FlowResultMsg.RepoPath = %q, want /dev/alpha", msg.RepoPath)
	}
	if msg.ListRequest != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("FlowResultMsg.ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModeFlows))
	}
	m, cmd := updateFlowRefreshTest(m, msg)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if cmd == nil {
		t.Fatal("expected startup fetch completion to schedule the first refresh tick")
	}
}

func TestModel_FlowRefreshTickScheduledWhenEnteringFlowsModePaths(t *testing.T) {
	tests := []struct {
		name  string
		setup func(Model) Model
		key   tea.KeyMsg
	}{
		{
			name: "4",
			setup: func(m Model) Model {
				m.activePane = 1
				return m
			},
			key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}},
		},
		{
			name: "left-arrow",
			setup: func(m Model) Model {
				m.activePane = 1
				m.mode = ui.ModeActiveFlows
				return m
			},
			key: tea.KeyMsg{Type: tea.KeyLeft},
		},
		{
			name: "right-arrow",
			setup: func(m Model) Model {
				m.activePane = 1
				m.mode = ui.ModePlans
				return m
			},
			key: tea.KeyMsg{Type: tea.KeyRight},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewWithOptions(flowRefreshTestRepos(), Options{
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
				},
			})
			m = tc.setup(m)
			beforeGen := m.flowRefreshTickGen
			beforeRequest := m.ListRequest(ui.ModeFlows)

			m, cmd := updateFlowRefreshTest(m, tc.key)
			if m.mode != ui.ModeFlows {
				t.Fatalf("mode = %d, want flows", m.mode)
			}
			if m.activePane != 1 {
				t.Fatalf("activePane = %d, want right pane", m.activePane)
			}
			if m.flowRefreshTickGen != beforeGen+1 {
				t.Fatalf("flow refresh generation = %d, want %d", m.flowRefreshTickGen, beforeGen+1)
			}
			if m.ListRequest(ui.ModeFlows) == beforeRequest {
				t.Fatalf("flows list request = %d, want changed from %d", m.ListRequest(ui.ModeFlows), beforeRequest)
			}
			if m.flowRefreshInFlight != m.ListRequest(ui.ModeFlows) {
				t.Fatalf("flow refresh in-flight request = %d, want %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeFlows))
			}

			msg := flowResultFromCommand(t, cmd)
			if msg.ListRequest != m.ListRequest(ui.ModeFlows) {
				t.Fatalf("FlowResultMsg.ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModeFlows))
			}
			m, cmd = updateFlowRefreshTest(m, msg)
			if m.flowRefreshInFlight != 0 {
				t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
			}
			if cmd == nil {
				t.Fatal("expected entering flows to schedule next refresh tick after fetch result")
			}
		})
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
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)
	calls = 0
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	if got := m.ListRequest(ui.ModeFlows); got == before {
		t.Fatalf("flows list request = %d, want changed from %d", got, before)
	}
	if m.flowRefreshInFlight != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("flow refresh in-flight request = %d, want %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeFlows))
	}
	msg := flowResultFromCommand(t, cmd)
	if msg.ListRequest != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("FlowResultMsg.ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModeFlows))
	}
	if calls != 1 {
		t.Fatalf("ListFlows calls = %d, want 1 after executing fetch command", calls)
	}
	m, cmd = updateFlowRefreshTest(m, msg)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if cmd == nil {
		t.Fatal("expected completed refresh fetch to schedule next tick")
	}
}

func TestModel_ActiveFlowRefreshTickUsesGlobalFetchAndPreservesNormalFlowCache(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/dev/alpha", DisplayName: "alpha"},
		{Path: "/dev/bravo", DisplayName: "bravo"},
	}
	alphaFlow := flowForRefreshTest("alpha-flow")
	bravoFlow := flowForRefreshTest("bravo-flow")
	bravoFlow.RepoPath = "/dev/bravo"
	var filters []flowstore.FlowFilter
	m := NewWithOptions(repos, Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			if filter.RepoPath != "" {
				return []flowstore.FlowRecord{alphaFlow}, nil
			}
			return []flowstore.FlowRecord{alphaFlow, bravoFlow}, nil
		},
	})
	m, _ = updateFlowRefreshTest(m, flowResultFromCommand(t, m.Init()))
	m.activePane = 1

	m, cmd := updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, cmd = updateFlowRefreshTest(m, activeFlowResultFromRefreshCommand(t, cmd))
	if cmd == nil {
		t.Fatal("expected active Flow result to schedule refresh tick")
	}
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "alpha-flow" {
		t.Fatalf("normal Flows() cache before tick = %#v, want repo-scoped alpha-flow", got)
	}
	filters = nil

	m, cmd = updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	result := activeFlowResultFromRefreshCommand(t, cmd)
	if result.ListRequest != m.ListRequest(ui.ModeActiveFlows) {
		t.Fatalf("ActiveFlowResultMsg.ListRequest = %d, want %d", result.ListRequest, m.ListRequest(ui.ModeActiveFlows))
	}
	if len(filters) != 1 || filters[0].RepoPath != "" {
		t.Fatalf("active Flow tick filters = %#v, want one global fetch", filters)
	}
	m, _ = updateFlowRefreshTest(m, result)
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "alpha-flow" {
		t.Fatalf("normal Flows() cache after active Flow tick = %#v, want unchanged alpha-flow", got)
	}
	if len(m.activeFlowRecords) != 2 {
		t.Fatalf("active Flow global cache has %d records, want 2", len(m.activeFlowRecords))
	}
}

func TestModel_FlowRefreshTickDoesNotOverlapInFlightFetch(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)

	m, cmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	if cmd == nil {
		t.Fatal("expected first tick to start a flow refresh fetch")
	}
	inFlight := m.flowRefreshInFlight
	before := m.ListRequest(ui.ModeFlows)

	m, duplicateCmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	if duplicateCmd != nil {
		t.Fatalf("duplicate tick during in-flight refresh returned command %T, want nil", duplicateCmd)
	}
	if m.flowRefreshInFlight != inFlight {
		t.Fatalf("flow refresh in-flight request = %d, want unchanged %d", m.flowRefreshInFlight, inFlight)
	}
	if got := m.ListRequest(ui.ModeFlows); got != before {
		t.Fatalf("flows list request = %d, want unchanged %d", got, before)
	}

	result := flowResultFromCommand(t, cmd)
	m, nextTick := updateFlowRefreshTest(m, result)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if nextTick == nil {
		t.Fatal("expected first refresh completion to schedule next tick")
	}
}

func TestModel_FlowRefreshTracksF5RefetchBeforePendingTick(t *testing.T) {
	var scans int
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ScanRepos: func() ([]scanner.Repo, error) {
			scans++
			return flowRefreshTestRepos(), nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd == nil {
		t.Fatal("expected F5 to start a global refresh batch")
	}
	if got := m.ListRequest(ui.ModeFlows); got == before {
		t.Fatalf("flows list request = %d, want changed from %d", got, before)
	}
	if m.flowRefreshInFlight != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("flow refresh in-flight request = %d, want F5 request %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeFlows))
	}
	m = assertFlowRefreshTickSuppressed(t, m)

	result := flowResultFromBatchCommand(t, cmd)
	if scans != 1 {
		t.Fatalf("ScanRepos calls = %d, want 1 after executing F5 command", scans)
	}
	m, nextTick := updateFlowRefreshTest(m, result)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if nextTick == nil {
		t.Fatal("expected F5 flow fetch completion to schedule next tick")
	}
}

func TestModel_FlowRefreshTracksRepoChangeRefetchBeforePendingTick(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/dev/alpha", DisplayName: "alpha"},
		{Path: "/dev/bravo", DisplayName: "bravo"},
	}
	m := NewWithOptions(repos, Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-" + filter.RepoPath)}, nil
		},
	})
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)
	m.activePane = 0
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected repo change to fetch flows for the new repo")
	}
	if got := m.ListRequest(ui.ModeFlows); got == before {
		t.Fatalf("flows list request = %d, want changed from %d", got, before)
	}
	if m.flowRefreshInFlight != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("flow refresh in-flight request = %d, want repo-change request %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeFlows))
	}
	m = assertFlowRefreshTickSuppressed(t, m)

	result := flowResultFromCommand(t, cmd)
	if result.RepoPath != "/dev/bravo" {
		t.Fatalf("FlowResultMsg.RepoPath = %q, want /dev/bravo", result.RepoPath)
	}
	m, nextTick := updateFlowRefreshTest(m, result)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if nextTick == nil {
		t.Fatal("expected repo-change flow fetch completion to schedule next tick")
	}
}

func TestModel_ActiveFlowRefreshRepoChangeKeepsInFlightGlobalFetch(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/dev/alpha", DisplayName: "alpha"},
		{Path: "/dev/bravo", DisplayName: "bravo"},
	}
	m := NewWithOptions(repos, Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-" + filter.RepoPath)}, nil
		},
	})
	m.activePane = 1
	var activeCmd tea.Cmd
	m, activeCmd = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if activeCmd == nil {
		t.Fatal("expected active Flow surface entry to fetch flows")
	}
	globalRequest := m.flowRefreshInFlight
	if globalRequest == 0 {
		t.Fatal("expected global active Flow fetch to be in flight")
	}
	m.activePane = 0

	var cmd tea.Cmd
	m, cmd = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("repo change returned command %T, want nil local filter", cmd)
	}
	if got := m.ListRequest(ui.ModeActiveFlows); got != globalRequest {
		t.Fatalf("active flows list request = %d, want unchanged global request %d", got, globalRequest)
	}
	if m.flowRefreshInFlight != globalRequest {
		t.Fatalf("flow refresh in-flight request = %d, want global request %d", m.flowRefreshInFlight, globalRequest)
	}
	msg := activeCmd()
	result, ok := msg.(ActiveFlowResultMsg)
	if !ok {
		t.Fatalf("active Flow command returned %T, want ActiveFlowResultMsg", msg)
	}
	if result.ListRequest != globalRequest {
		t.Fatalf("ActiveFlowResultMsg.ListRequest = %d, want %d", result.ListRequest, globalRequest)
	}
}

func TestModel_ActiveFlowEntrySupersedesStaleInFlightFetch(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/dev/alpha", DisplayName: "alpha"},
		{Path: "/dev/bravo", DisplayName: "bravo"},
	}
	m := NewWithOptions(repos, Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-" + filter.RepoPath)}, nil
		},
	})
	alphaRequest := m.flowRefreshInFlight
	if alphaRequest == 0 {
		t.Fatal("expected startup Flow fetch to be in flight")
	}
	m.mode = ui.ModeWorktrees
	m.activePane = 0
	var cmd tea.Cmd
	m, cmd = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected repo change in worktrees mode to fetch worktrees")
	}
	m.activePane = 1

	m, cmd = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if cmd == nil {
		t.Fatal("expected view 9 entry to supersede stale in-flight Flow fetch")
	}
	if got := m.ListRequest(ui.ModeActiveFlows); got == 0 {
		t.Fatalf("active flows list request = %d, want non-zero", got)
	}
	if m.flowRefreshInFlight != m.ListRequest(ui.ModeActiveFlows) {
		t.Fatalf("flow refresh in-flight request = %d, want active flows entry request %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeActiveFlows))
	}
	msg := cmd()
	result, ok := msg.(ActiveFlowResultMsg)
	if !ok {
		t.Fatalf("active Flow command returned %T, want ActiveFlowResultMsg", msg)
	}
	if result.ListRequest != m.ListRequest(ui.ModeActiveFlows) {
		t.Fatalf("ActiveFlowResultMsg.ListRequest = %d, want %d", result.ListRequest, m.ListRequest(ui.ModeActiveFlows))
	}
}

func TestModel_FlowRefreshTracksActionRefetchBeforePendingTick(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)
	before := m.ListRequest(ui.ModeFlows)

	m, cmd := updateFlowRefreshTest(m, ActionFailedMsg{RepoPath: "/dev/alpha", Err: "launch failed"})
	if cmd == nil {
		t.Fatal("expected action failure in flows mode to refetch flows")
	}
	if got := m.ListRequest(ui.ModeFlows); got == before {
		t.Fatalf("flows list request = %d, want changed from %d", got, before)
	}
	if m.flowRefreshInFlight != m.ListRequest(ui.ModeFlows) {
		t.Fatalf("flow refresh in-flight request = %d, want action refetch request %d", m.flowRefreshInFlight, m.ListRequest(ui.ModeFlows))
	}
	m = assertFlowRefreshTickSuppressed(t, m)

	result := flowResultFromCommand(t, cmd)
	m, nextTick := updateFlowRefreshTest(m, result)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if nextTick == nil {
		t.Fatal("expected action refetch completion to schedule next tick")
	}
}

func TestModel_FlowRefreshFastRefetchInvalidatesPendingTick(t *testing.T) {
	tests := []struct {
		name    string
		model   func() Model
		prepare func(Model) Model
		trigger func(Model) (Model, tea.Cmd)
		result  func(*testing.T, tea.Cmd) FlowResultMsg
	}{
		{
			name: "f5",
			model: func() Model {
				return NewWithOptions(flowRefreshTestRepos(), Options{
					StartupMode: ui.ModeFlows,
					ScanRepos: func() ([]scanner.Repo, error) {
						return flowRefreshTestRepos(), nil
					},
					ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
						return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
					},
				})
			},
			trigger: func(m Model) (Model, tea.Cmd) {
				return updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyF5})
			},
			result: flowResultFromBatchCommand,
		},
		{
			name: "repo-change",
			model: func() Model {
				repos := []scanner.Repo{
					{Path: "/dev/alpha", DisplayName: "alpha"},
					{Path: "/dev/bravo", DisplayName: "bravo"},
				}
				return NewWithOptions(repos, Options{
					StartupMode: ui.ModeFlows,
					ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
						return []flowstore.FlowRecord{flowForRefreshTest("flow-" + filter.RepoPath)}, nil
					},
				})
			},
			prepare: func(m Model) Model {
				m.activePane = 0
				return m
			},
			trigger: func(m Model) (Model, tea.Cmd) {
				return updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyDown})
			},
			result: flowResultFromCommand,
		},
		{
			name: "action-refetch",
			model: func() Model {
				return NewWithOptions(flowRefreshTestRepos(), Options{
					StartupMode: ui.ModeFlows,
					ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
						return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
					},
				})
			},
			trigger: func(m Model) (Model, tea.Cmd) {
				return updateFlowRefreshTest(m, ActionFailedMsg{RepoPath: "/dev/alpha", Err: "launch failed"})
			},
			result: flowResultFromCommand,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model()
			startup := flowResultFromCommand(t, m.Init())
			m, _ = updateFlowRefreshTest(m, startup)
			if tc.prepare != nil {
				m = tc.prepare(m)
			}
			oldGeneration := m.flowRefreshTickGen
			before := m.ListRequest(ui.ModeFlows)

			m, fetchCmd := tc.trigger(m)
			if fetchCmd == nil {
				t.Fatal("expected refetch command")
			}
			if m.flowRefreshTickGen == oldGeneration {
				t.Fatalf("flow refresh generation = %d, want advanced from %d", m.flowRefreshTickGen, oldGeneration)
			}
			if got := m.ListRequest(ui.ModeFlows); got == before {
				t.Fatalf("flows list request = %d, want changed from %d", got, before)
			}
			result := tc.result(t, fetchCmd)
			m, nextTick := updateFlowRefreshTest(m, result)
			if nextTick == nil {
				t.Fatal("expected completed refetch to schedule next tick")
			}
			if m.flowRefreshInFlight != 0 {
				t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
			}

			before = m.ListRequest(ui.ModeFlows)
			m, oldTickCmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: oldGeneration})
			if oldTickCmd != nil {
				t.Fatalf("old pending tick returned command %T, want nil", oldTickCmd)
			}
			if got := m.ListRequest(ui.ModeFlows); got != before {
				t.Fatalf("flows list request = %d, want unchanged %d after old tick", got, before)
			}
		})
	}
}

func TestModel_FlowRefreshOldTrackedResultDoesNotScheduleAfterNewerRefetch(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ScanRepos: func() ([]scanner.Repo, error) {
			return flowRefreshTestRepos(), nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)

	m, autoCmd := updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: m.flowRefreshTickGen})
	autoRequest := m.flowRefreshInFlight
	if autoRequest == 0 {
		t.Fatal("expected auto refresh request to be in flight")
	}

	m, f5Cmd := updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyF5})
	f5Request := m.flowRefreshInFlight
	if f5Request == 0 || f5Request == autoRequest {
		t.Fatalf("F5 request = %d, want nonzero request distinct from auto request %d", f5Request, autoRequest)
	}

	autoResult := flowResultFromCommand(t, autoCmd)
	m, cmd := updateFlowRefreshTest(m, autoResult)
	if cmd != nil {
		t.Fatalf("stale auto refresh result returned command %T, want nil", cmd)
	}
	if m.flowRefreshInFlight != f5Request {
		t.Fatalf("flow refresh in-flight request = %d, want F5 request %d", m.flowRefreshInFlight, f5Request)
	}

	f5Result := flowResultFromBatchCommand(t, f5Cmd)
	m, cmd = updateFlowRefreshTest(m, f5Result)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if cmd == nil {
		t.Fatal("expected current F5 result to schedule next tick")
	}
}

func TestModel_FlowRefreshFetchErrorClearsInFlightAndSchedulesNextTick(t *testing.T) {
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("boom")
		},
	})

	errMsg := fetchErrorFromCommand(t, m.Init())
	if errMsg.ListRequest != m.flowRefreshInFlight {
		t.Fatalf("FetchErrorMsg.ListRequest = %d, want in-flight request %d", errMsg.ListRequest, m.flowRefreshInFlight)
	}

	m, cmd := updateFlowRefreshTest(m, errMsg)
	if m.flowRefreshInFlight != 0 {
		t.Fatalf("flow refresh in-flight request = %d, want cleared", m.flowRefreshInFlight)
	}
	if cmd == nil {
		t.Fatal("expected failed refresh fetch to schedule next tick")
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

	m, cmd := updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	oldGeneration := m.flowRefreshTickGen
	m, _ = updateFlowRefreshTest(m, flowResultFromCommand(t, cmd))
	m, _ = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, cmd = updateFlowRefreshTest(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if m.flowRefreshTickGen == oldGeneration {
		t.Fatal("re-entering flows should advance the refresh generation")
	}
	m, _ = updateFlowRefreshTest(m, flowResultFromCommand(t, cmd))
	before := m.ListRequest(ui.ModeFlows)

	m, cmd = updateFlowRefreshTest(m, flowRefreshTickMsg{Generation: oldGeneration})
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
	startup := flowResultFromCommand(t, m.Init())
	m, _ = updateFlowRefreshTest(m, startup)
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

	fresh := flowResultFromCommand(t, cmd)
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
