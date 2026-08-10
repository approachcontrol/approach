package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/ui"
)

var errFlowHeadlessWrite = errors.New("state root locked")

// refetchFlowsMode leaves and re-enters the Flows mode so the next Flow result
// carries a list request issued after any preceding mutation.
func refetchFlowsMode(t *testing.T, m model.Model) model.Model {
	t.Helper()
	before := m.ListRequest(ui.ModeFlows)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.ListRequest(ui.ModeFlows) <= before {
		t.Fatalf("list request = %d, want a newer request than %d", m.ListRequest(ui.ModeFlows), before)
	}
	return m
}

// launchableFlow is a Flow whose Implementation phase can actually be reserved
// and launched, so the launch fence is observed through the store seam.
func launchableFlow() flowstore.FlowRecord {
	flow := flowWithPhaseDetails()
	flow.WorktreePath = "/dev/alpha-worktrees/flow-with-phases"
	return flow
}

func TestModel_LaunchIsFencedBehindPendingHeadlessWrite(t *testing.T) {
	flow := launchableFlow()
	updated := flow
	updated.Headless = false
	updated.UpdatedAt = time.Date(2026, 8, 10, 12, 1, 0, 0, time.UTC)

	var reservations []flowstore.PhaseLaunchUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SetFlowHeadless: func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			return updated, nil
		},
		AddFlowPhaseLaunchID: func(reservation flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			reservations = append(reservations, reservation)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, headlessCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if headlessCmd == nil {
		t.Fatal("h returned nil persistence command")
	}

	// The persistence command has not run yet, so the store still holds the old
	// preference. A launch now would reserve the phase and start the agent in
	// the stale mode.
	m, launchCmd := update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 0 {
		t.Fatalf("reserved %#v while the headless write was in flight, want no launch", reservations)
	}
	if got := m.TransientError(); !strings.Contains(got, "Applying headless mode change") {
		t.Fatalf("status = %q, want the fenced launch to explain the wait", got)
	}

	// Once the write lands the launch proceeds with the persisted preference.
	m, _ = update(m, headlessCmd())
	m, launchCmd = update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want one launch after the headless write completed", reservations)
	}
	if got := m.TransientError(); strings.Contains(got, "Applying headless mode change") {
		t.Fatalf("status = %q, want the fence released after the write completed", got)
	}
}

func TestModel_FailedHeadlessWriteReleasesTheLaunchFence(t *testing.T) {
	flow := launchableFlow()
	var reservations []flowstore.PhaseLaunchUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SetFlowHeadless: func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errFlowHeadlessWrite
		},
		AddFlowPhaseLaunchID: func(reservation flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			reservations = append(reservations, reservation)
			return flow, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, headlessCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if headlessCmd == nil {
		t.Fatal("h returned nil persistence command")
	}
	m, _ = update(m, headlessCmd())

	m, launchCmd := update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want the fence released after the write failed", reservations)
	}
	if got := m.TransientError(); strings.Contains(got, "Applying headless mode change") {
		t.Fatalf("status = %q, want the fence released after the write failed", got)
	}
}

func TestModel_OverlappingHeadlessWritesKeepTheLaunchFenced(t *testing.T) {
	flow := launchableFlow()
	updated := flow
	updated.Headless = false

	var reservations []flowstore.PhaseLaunchUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			result := updated
			result.Headless = update.Enabled
			return result, nil
		},
		AddFlowPhaseLaunchID: func(reservation flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			reservations = append(reservations, reservation)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, second := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if first == nil || second == nil {
		t.Fatal("both h presses should return a persistence command")
	}

	// The first completion must not release the fence the second write holds.
	m, _ = update(m, first())
	m, launchCmd := update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 0 {
		t.Fatalf("reserved %#v while the second headless write was in flight, want no launch", reservations)
	}

	m, _ = update(m, second())
	m, launchCmd = update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want one launch after every headless write completed", reservations)
	}
}

func TestModel_RepairIsFencedBehindPendingHeadlessWrite(t *testing.T) {
	repoPath := t.TempDir()
	record := repairableFlowForShortcut()
	record.RepoPath = repoPath
	updated := record
	updated.Headless = false

	var listCalls int
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SetFlowHeadless: func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			return updated, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return []flowstore.FlowRecord{record}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	m, headlessCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if headlessCmd == nil {
		t.Fatal("h returned nil persistence command")
	}

	// Repair reads the persisted preference asynchronously and would otherwise
	// win the race against the in-flight headless write.
	m, repairCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = settleModelCommands(t, m, repairCmd, 4)
	if listCalls != 0 {
		t.Fatalf("repair read the Flow list %d times while the headless write was in flight, want none", listCalls)
	}
	if got := m.TransientError(); !strings.Contains(got, "Applying headless mode change") {
		t.Fatalf("status = %q, want the fenced repair to explain the wait", got)
	}

	m, _ = update(m, headlessCmd())
	m, repairCmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if repairCmd == nil {
		t.Fatal("repair stayed fenced after the headless write completed")
	}
}

func TestModel_FetchIssuedAfterMutationWinsRegardlessOfWallClock(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	stored := flowWithPhaseDetails()
	stored.UpdatedAt = t0

	mutated := stored
	mutated.Headless = false
	mutated.UpdatedAt = t0.Add(time.Minute)

	// A causally newer record whose wall clock ran backwards: another writer
	// completed the implementation phase but stamped an earlier UpdatedAt.
	authoritative := mutated
	authoritative.UpdatedAt = t0.Add(-time.Hour)
	authoritative.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
		{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
		{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
	}

	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{stored})
	m, _ = update(m, model.FlowHeadlessSetMsg{
		RepoPath: "/dev/alpha", FlowID: mutated.FlowID, Flow: mutated, Enabled: false,
	})

	m = refetchFlowsMode(t, m)
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{authoritative},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	got := m.Flows()
	if len(got) != 1 {
		t.Fatalf("Flows() = %#v, want one record", got)
	}
	if got[0].Phases[2].Status != flowstore.PhaseCompleted {
		t.Fatalf("phase status = %q, want the fetched completion to be visible", got[0].Phases[2].Status)
	}
	if !got[0].UpdatedAt.Equal(authoritative.UpdatedAt) {
		t.Fatalf("UpdatedAt = %s, want the fetched record %s", got[0].UpdatedAt, authoritative.UpdatedAt)
	}

	// The acknowledged mutation must not be retained and shadow later fetches.
	m = refetchFlowsMode(t, m)
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{authoritative},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if got := m.Flows(); got[0].Phases[2].Status != flowstore.PhaseCompleted {
		t.Fatalf("phase status = %q on the second fetch, want the retained mutation to be gone", got[0].Phases[2].Status)
	}
}
