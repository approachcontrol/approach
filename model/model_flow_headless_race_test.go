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
	if first == nil {
		t.Fatal("the first h press should return a persistence command")
	}
	// Only one write may be outstanding per Flow, so the second press records
	// intent instead of starting a command that could reach the store first.
	m, second := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if second != nil {
		t.Fatalf("second h press returned command %T while a write was in flight, want none", second)
	}

	// Completing the in-flight write must persist the coalesced intent rather
	// than release the fence.
	m, followUp := update(m, first())
	if followUp == nil {
		t.Fatal("completing the first write should persist the queued toggle")
	}
	m, launchCmd := update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 0 {
		t.Fatalf("reserved %#v while the queued headless write was outstanding, want no launch", reservations)
	}

	m, _ = update(m, followUp())
	m, launchCmd = update(m, flowLaunchKey())
	m = settleModelCommands(t, m, launchCmd, 4)
	if len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want one launch after every headless write completed", reservations)
	}
}

func TestModel_QueuedHeadlessTogglesAlternateFromThePendingValue(t *testing.T) {
	flow := flowWithPhaseDetails()
	if !flow.Headless {
		t.Fatal("fixture should start headless so the toggles alternate off then on")
	}

	var writes []flowstore.HeadlessUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			writes = append(writes, update)
			result := flow
			result.Headless = update.Enabled
			return result, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	// Both presses land before the first result is handled, so the second must
	// alternate from the value the first one queued rather than the stale row.
	// Writes are serialized, so the store observes them in the pressed order.
	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if first == nil {
		t.Fatal("the first h press should return a persistence command")
	}
	m, second := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if second != nil {
		t.Fatalf("second h press returned command %T while a write was in flight, want none", second)
	}
	m, followUp := update(m, first())
	if followUp == nil {
		t.Fatal("completing the first write should persist the queued toggle")
	}
	m, _ = update(m, followUp())

	want := []flowstore.HeadlessUpdate{
		{FlowID: flow.FlowID, Enabled: false},
		{FlowID: flow.FlowID, Enabled: true},
	}
	if len(writes) != 2 || writes[0] != want[0] || writes[1] != want[1] {
		t.Fatalf("headless writes = %#v, want %#v", writes, want)
	}
	if got := m.Flows(); len(got) != 1 || !got[0].Headless {
		t.Fatalf("Flows() = %#v, want the two toggles to return headless mode to on", got)
	}
	if m.TransientError() != "" {
		t.Fatalf("status = %q, want no error after the queued toggle settled", m.TransientError())
	}
}

func TestModel_QueuedHeadlessToggleKeepsItsOwnSurfaceScope(t *testing.T) {
	flow := flowWithPhaseDetails()

	var writes []flowstore.HeadlessUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			writes = append(writes, update)
			result := flow
			result.Headless = update.Enabled
			return result, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	// The write starts from the repository Flow list, so it is repo-scoped.
	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if first == nil {
		t.Fatal("the first h press should return a persistence command")
	}

	// The queued toggle is entered from Active Flows, which is global. The
	// follow-up must carry that scope so its result is not rejected as off-repo.
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, second := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if second != nil {
		t.Fatalf("second h press returned command %T while a write was in flight, want none", second)
	}

	m, followUp := update(m, first())
	if followUp == nil {
		t.Fatal("completing the first write should persist the queued toggle")
	}
	followUpMsg, ok := followUp().(model.FlowHeadlessSetMsg)
	if !ok {
		t.Fatalf("follow-up command returned %T, want FlowHeadlessSetMsg", followUp())
	}
	if !followUpMsg.AllRepositories {
		t.Fatalf("follow-up msg = %#v, want the Active Flows scope of the queued toggle", followUpMsg)
	}

	m, _ = update(m, followUpMsg)
	if len(writes) != 2 || writes[1].Enabled != true {
		t.Fatalf("headless writes = %#v, want the queued toggle persisted", writes)
	}
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || !got[0].Headless {
		t.Fatalf("ActiveFlows = %#v, want the queued toggle applied to the active row", got)
	}
}

func TestModel_CoalescedHeadlessToggleKeepsTheLatestSurfaceScope(t *testing.T) {
	flow := flowWithPhaseDetails()

	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			result := flow
			result.Headless = update.Enabled
			return result, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	// A repo-scoped write starts from the repository Flow list.
	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if first == nil {
		t.Fatal("the first h press should return a persistence command")
	}

	// Two Active Flows toggles bring the intent back to the in-flight value, so
	// the completion needs no follow-up but still belongs to the global surface.
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})

	// The user moves to another repository before the write lands, so a
	// repo-scoped result would be discarded as off-repo.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // repo bravo

	m, followUp := update(m, first())
	if followUp != nil {
		t.Fatalf("follow-up command %T issued although the coalesced intent matched, want none", followUp)
	}
	if got := model.ActiveFlowRecordsForTest(m); len(got) != 1 || got[0].Headless {
		t.Fatalf("Active Flow cache = %#v, want the global result applied", got)
	}
}

func TestModel_QueuedHeadlessToggleKeepsGlobalScopeAcrossTheChain(t *testing.T) {
	flow := flowWithPhaseDetails()

	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			result := flow
			result.Headless = update.Enabled
			return result, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	// The chain starts global, from Active Flows.
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if first == nil {
		t.Fatal("the first h press should return a persistence command")
	}

	// A later toggle from the repository Flow list must not narrow the chain's
	// scope: the follow-up still has to reach the cross-repository cache.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, second := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if second != nil {
		t.Fatalf("second h press returned command %T while a write was in flight, want none", second)
	}

	m, followUp := update(m, first())
	if followUp == nil {
		t.Fatal("completing the first write should persist the queued toggle")
	}
	followUpMsg, ok := followUp().(model.FlowHeadlessSetMsg)
	if !ok {
		t.Fatalf("follow-up command returned %T, want FlowHeadlessSetMsg", followUp())
	}
	if !followUpMsg.AllRepositories {
		t.Fatalf("follow-up msg = %#v, want the chain to stay globally scoped", followUpMsg)
	}

	// The user selects another repository before the follow-up lands.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // repo bravo
	m, _ = update(m, followUpMsg)

	if got := model.ActiveFlowRecordsForTest(m); len(got) != 1 || !got[0].Headless {
		t.Fatalf("Active Flow cache = %#v, want the final result applied", got)
	}
}

func TestModel_FailedWriteReportsDroppedQueuedHeadlessToggles(t *testing.T) {
	flow := flowWithPhaseDetails()

	var writes []flowstore.HeadlessUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			writes = append(writes, update)
			return flowstore.FlowRecord{}, errFlowHeadlessWrite
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	// Three presses queue headless-off behind the in-flight headless-off write.
	// The failure leaves the store on, so that intent can no longer be reached.
	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, followUp := update(m, first())
	m = settleModelCommands(t, m, followUp, 4)

	if len(writes) != 1 {
		t.Fatalf("headless writes = %#v, want the failure to stop after one attempt", writes)
	}
	got := m.TransientError()
	if !strings.Contains(got, "state root locked") || !strings.Contains(got, "headless mode is unchanged") {
		t.Fatalf("status = %q, want the failure and the dropped queued toggle reported", got)
	}
	if flows := m.Flows(); len(flows) != 1 || !flows[0].Headless {
		t.Fatalf("Flows() = %#v, want the stored preference unchanged", flows)
	}
	if _, cmd := update(m, flowLaunchKey()); cmd == nil {
		t.Fatal("launch stayed fenced after the failed write")
	}
}

func TestModel_QueuedHeadlessTogglesCoalesceToTheLastIntent(t *testing.T) {
	flow := flowWithPhaseDetails()

	var writes []flowstore.HeadlessUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			writes = append(writes, update)
			result := flow
			result.Headless = update.Enabled
			return result, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	// Three presses from headless-on: the middle intent never has to reach the
	// store because the in-flight write already persists the final value.
	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, followUp := update(m, first())
	if followUp != nil {
		t.Fatalf("follow-up command %T issued although the in-flight write matched the last intent", followUp)
	}

	want := []flowstore.HeadlessUpdate{{FlowID: flow.FlowID, Enabled: false}}
	if len(writes) != 1 || writes[0] != want[0] {
		t.Fatalf("headless writes = %#v, want %#v", writes, want)
	}
	if got := m.Flows(); len(got) != 1 || got[0].Headless {
		t.Fatalf("Flows() = %#v, want three toggles to leave headless mode off", got)
	}
	if _, cmd := update(m, flowLaunchKey()); cmd == nil {
		t.Fatal("launch stayed fenced after the coalesced toggle settled")
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

// UpdatedAt selects the base record and the toggle is re-applied on top, so the
// field this process persisted survives either choice of base while the newer
// record's other metadata is kept.
func TestModel_StaleFetchReconcilesAgainstTheNewerRecord(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	stored := flowWithPhaseDetails()
	stored.UpdatedAt = t0

	toggled := stored
	toggled.Headless = false
	toggled.UpdatedAt = t0.Add(time.Minute)

	completedPhases := []flowstore.FlowPhase{
		{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
		{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
		{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
	}

	t.Run("incoming record is newer", func(t *testing.T) {
		// A peer completed the phase after the toggle, so its work must survive
		// and only the toggled field is re-applied over it.
		peer := stored
		peer.Phases = completedPhases
		peer.UpdatedAt = t0.Add(2 * time.Minute)

		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{stored})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: toggled.FlowID, Flow: toggled, Enabled: false,
		})
		m, _ = update(m, model.FlowResultMsg{
			RepoPath:    "/dev/alpha",
			Flows:       []flowstore.FlowRecord{peer},
			ListRequest: m.ListRequest(ui.ModeFlows),
		})

		got := m.Flows()
		if len(got) != 1 || got[0].Phases[2].Status != flowstore.PhaseCompleted {
			t.Fatalf("Flows() = %#v, want the peer completion preserved", got)
		}
		if got[0].Headless {
			t.Fatal("Headless = true, want the local toggle re-applied over the peer record")
		}
	})

	t.Run("cached record is newer", func(t *testing.T) {
		// The write result was read under the Flow lock and carries a phase
		// completion the genuinely stale list response predates.
		cached := toggled
		cached.Phases = completedPhases

		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{stored})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: cached.FlowID, Flow: cached, Enabled: false,
		})
		m, _ = update(m, model.FlowResultMsg{
			RepoPath:    "/dev/alpha",
			Flows:       []flowstore.FlowRecord{stored},
			ListRequest: m.ListRequest(ui.ModeFlows),
		})

		got := m.Flows()
		if len(got) != 1 || got[0].Phases[2].Status != flowstore.PhaseCompleted {
			t.Fatalf("Flows() = %#v, want the write result's completion kept", got)
		}
		if got[0].Headless {
			t.Fatal("Headless = true, want the local toggle preserved")
		}
	})
}

// Successive writes to different fields of one Flow are independent. A stale
// fetch must not revert an earlier field just because a later write to a
// different field was cached after it.
func TestModel_StaleFetchKeepsEveryUnacknowledgedFieldMutation(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	stored := flowWithPhaseDetails()
	stored.UpdatedAt = t0

	headlessOff := stored
	headlessOff.Headless = false
	headlessOff.UpdatedAt = t0.Add(time.Minute)

	autoModeOn := headlessOff
	autoModeOn.AutoMode = true
	autoModeOn.UpdatedAt = t0.Add(2 * time.Minute)

	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{stored})
	m, _ = update(m, model.FlowHeadlessSetMsg{
		RepoPath: "/dev/alpha", FlowID: headlessOff.FlowID, Flow: headlessOff, Enabled: false,
	})
	m, _ = update(m, model.FlowAutoModeSetMsg{
		RepoPath: "/dev/alpha", FlowID: autoModeOn.FlowID, Flow: autoModeOn, Enabled: true,
	})

	// This result belongs to the request that was in flight before either write.
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{stored},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	got := m.Flows()
	if len(got) != 1 {
		t.Fatalf("Flows() = %#v, want one record", got)
	}
	if got[0].Headless {
		t.Fatal("Headless = true, want the earlier headless write preserved")
	}
	if !got[0].AutoMode {
		t.Fatal("AutoMode = false, want the later auto-mode write preserved")
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
