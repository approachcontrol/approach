package model_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

func flowsInRightPane(t *testing.T, m model.Model, records []flowstore.FlowRecord) model.Model {
	t.Helper()
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: records, ListRequest: m.ListRequest(ui.ModeFlows)})
	return m
}

func activeFlowResultFromCommand(t *testing.T, cmd tea.Cmd) model.ActiveFlowResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected active Flow fetch command")
	}
	msg := cmd()
	if result, ok := msg.(model.ActiveFlowResultMsg); ok {
		return result
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, subcmd := range batch {
			if result, ok := subcmd().(model.ActiveFlowResultMsg); ok {
				return result
			}
		}
	}
	t.Fatalf("command returned %T, want ActiveFlowResultMsg", msg)
	return model.ActiveFlowResultMsg{}
}

func settleModelCommands(t *testing.T, m model.Model, cmd tea.Cmd, rounds int) model.Model {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for range rounds {
		var nextPending []tea.Cmd
		for _, current := range pending {
			if current == nil {
				continue
			}
			messages := immediateTestCommandMessages(current)
			for _, msg := range messages {
				var next tea.Cmd
				m, next = update(m, msg)
				if next != nil {
					nextPending = append(nextPending, next)
				}
			}
		}
		pending = nextPending
		if len(pending) == 0 {
			break
		}
	}
	return m
}

func immediateTestCommandMessages(cmd tea.Cmd) []tea.Msg {
	raw, ok := runImmediateTestCommand(cmd)
	if !ok {
		return nil
	}
	batch, ok := raw.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{raw}
	}
	var messages []tea.Msg
	for _, child := range batch {
		messages = append(messages, immediateTestCommandMessages(child)...)
	}
	return messages
}

func runImmediateTestCommand(cmd tea.Cmd) (tea.Msg, bool) {
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		return msg, true
	case <-time.After(20 * time.Millisecond):
		return nil, false
	}
}

func flowStartRequestHeadless(req model.FlowStartRequest) bool {
	if req.Headless == nil {
		return true
	}
	return *req.Headless
}

func enterActiveFlowsWithRecords(t *testing.T, m model.Model, records []flowstore.FlowRecord) model.Model {
	t.Helper()
	if m.ActivePane() == ui.PaneRepos {
		m = inRightPane(m)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(m, model.ActiveFlowResultMsg{Flows: records, ListRequest: m.ListRequest(ui.ModeActiveFlows)})
	return m
}

func flowWithPhaseDetails() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		// An in-progress Flow has a worktree. Leaving it out would send every
		// launch in this file through the worktree-creation step, which is its
		// own path with its own tests.
		WorktreePath: "/dev/alpha-worktrees/flow-with-phases",
		Title:        "Flow with phases",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/with-phases",
		Headless:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}
}

func manualMergeEligibleFlow() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Manual merge Flow",
		Status:   flowstore.StatusInProgress,
		Branch:   "flow/manual-merge",
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     116,
			URL:        "https://github.com/approachcontrol/approach/pull/116",
			HeadBranch: "flow/manual-merge",
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

func TestModel_CtrlAShowsGlobalActiveFlowsWithoutPollutingFlowsCache(t *testing.T) {
	alpha := flowWithPhaseDetails()
	alpha.FlowID = "alpha-flow"
	alpha.Title = "Alpha Flow"
	bravo := flowWithPhaseDetails()
	bravo.FlowID = "bravo-flow"
	bravo.RepoPath = "/dev/bravo"
	bravo.Title = "Bravo Flow"
	merged := flowWithPhaseDetails()
	merged.FlowID = "merged-flow"
	merged.RepoPath = "/dev/bravo"
	merged.Title = "Merged Flow"
	merged.Status = flowstore.StatusMerged

	var gotFilter flowstore.FlowFilter
	m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return []flowstore.FlowRecord{alpha, bravo, merged}, nil
		},
	}), []flowstore.FlowRecord{alpha})
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 30})
	m, _ = switchTestMode(m, ui.ModeWorktrees)
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %v, want worktrees before active flows toggle", m.Mode())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd == nil {
		t.Fatal("active flows toggle should start or continue a Flow refresh")
	}
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	if gotFilter.RepoPath != "" {
		t.Fatalf("active Flow filter RepoPath = %q, want global", gotFilter.RepoPath)
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("mode = %v, want active flows", m.Mode())
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"active flows", "Alpha Flow", "Bravo Flow"} {
		if !strings.Contains(view, want) {
			t.Fatalf("active-flow view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "f3") {
		t.Fatalf("active-flow view should not advertise f3:\n%s", view)
	}
	if strings.Contains(view, "Merged Flow") {
		t.Fatalf("active-flow view should filter merged flows:\n%s", view)
	}
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "alpha-flow" {
		t.Fatalf("normal Flows() cache = %#v, want original repo-scoped alpha flow", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "worktrees") || strings.Contains(view, "Bravo Flow") {
		t.Fatalf("ctrl+a should restore worktrees pane:\n%s", view)
	}
}

func TestModel_ActiveFlowsGlobalResultSurvivesRepoMoveAndUsesLeftPaneFilter(t *testing.T) {
	alpha := flowWithPhaseDetails()
	alpha.FlowID = "alpha-flow"
	alpha.Title = "Alpha Flow"
	bravo := flowWithPhaseDetails()
	bravo.FlowID = "bravo-flow"
	bravo.RepoPath = "/dev/bravo"
	bravo.Title = "Bravo Flow"

	var filters []flowstore.FlowFilter
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{alpha, bravo}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 24})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	result := activeFlowResultFromCommand(t, cmd)
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatalf("switching active flows to repo pane returned command %T, want nil", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("repo movement should refresh both stored content panes")
	}

	m, _ = update(m, result)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Bravo Flow") || strings.Contains(view, "Alpha Flow") {
		t.Fatalf("left-pane active-flow filter should show only bravo after stale-safe result:\n%s", view)
	}
	if len(filters) != 1 || filters[0].RepoPath != "" {
		t.Fatalf("active Flow filters = %#v, want one global fetch", filters)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "Alpha Flow") || !strings.Contains(view, "Bravo Flow") {
		t.Fatalf("returning focus to content pane should restore global active flows:\n%s", view)
	}
}

func TestModel_ActiveFlowsLeftPaneFilterCleansRepoPath(t *testing.T) {
	alpha := flowWithPhaseDetails()
	alpha.FlowID = "alpha-flow"
	alpha.Title = "Alpha Flow"
	bravo := flowWithPhaseDetails()
	bravo.FlowID = "bravo-flow"
	bravo.RepoPath = "/dev/bravo/"
	bravo.Title = "Bravo Flow"

	m := inRightPane(model.New(testRepos()))
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{alpha, bravo})
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 24})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Bravo Flow") || strings.Contains(view, "Alpha Flow") {
		t.Fatalf("left-pane active-flow filter should clean repo paths:\n%s", view)
	}
}

func TestModel_ActiveFlowsGlobalFetchErrorSurvivesRepoMove(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("state unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 18})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd == nil {
		t.Fatal("expected active Flow fetch command")
	}
	msg := cmd()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, repoMoveCmd := update(m, tea.KeyMsg{Type: tea.KeyDown})
	if repoMoveCmd == nil {
		t.Fatal("repo movement should refresh both stored content panes")
	}

	m, _ = update(m, msg)
	if got := m.TransientError(); !strings.Contains(got, "failed to load active flows") || !strings.Contains(got, "state unavailable") {
		t.Fatalf("status = %q, want active-flow fetch error after repo move", got)
	}
}

func TestModel_ActiveFlowsLeftPaneEnterShowsGlobalActiveFlows(t *testing.T) {
	alpha := flowWithPhaseDetails()
	alpha.FlowID = "alpha-flow"
	alpha.Title = "Alpha Flow"
	bravo := flowWithPhaseDetails()
	bravo.FlowID = "bravo-flow"
	bravo.RepoPath = "/dev/bravo"
	bravo.Title = "Bravo Flow"

	var filters []flowstore.FlowFilter
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{alpha, bravo}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 18})
	m, _ = switchTestMode(m, ui.ModePlans)
	m, cmd := switchTestMode(m, ui.ModeFlows)
	m, _ = update(m, flowResultFromCommand(t, cmd))

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].FlowID != "bravo-flow" {
		t.Fatalf("left-pane active flows = %#v, want repo-filtered bravo", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("left-pane enter in active flows returned command %T, want nil", cmd)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Shortcuts  Active flows") {
		t.Fatalf("left-pane enter should keep active flows visible:\n%s", view)
	}
	if got := model.ActiveFlowsForTest(m); len(got) != 2 || got[0].FlowID != "alpha-flow" || got[1].FlowID != "bravo-flow" {
		t.Fatalf("left-pane enter active flows = %#v, want global alpha and bravo", got)
	}
	if len(filters) != 2 || filters[len(filters)-1].RepoPath != "" {
		t.Fatalf("flow filters = %#v, want no repo-scoped fetch on left-pane enter", filters)
	}
	if m.ActivePane() == ui.PaneRepos || m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("active pane/mode = %d/%d, want right pane in active flows", m.ActivePane(), m.Mode())
	}
}

func TestModel_ActiveFlowsUsesSeparateActiveFlowSelectionForActions(t *testing.T) {
	activeOne := flowWithPhaseDetails()
	activeOne.FlowID = "active-one"
	activeOne.Title = "Active One"
	activeTwo := flowWithPhaseDetails()
	activeTwo.FlowID = "active-two"
	activeTwo.Title = "Active Two"
	merged := flowWithPhaseDetails()
	merged.FlowID = "merged-flow"
	merged.Title = "Merged Flow"
	merged.Status = flowstore.StatusMerged

	var launched flowstore.PhaseLaunchUpdate
	m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launched = update
			updated := activeTwo
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Status = flowstore.PhaseRunning
					updated.Phases[i].LaunchIDs = append(updated.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return updated, nil
		},
	}), []flowstore.FlowRecord{activeOne, merged, activeTwo})
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 18})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.FlowSelected(); got != 1 {
		t.Fatalf("normal Flow selection = %d, want merged row index 1", got)
	}

	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{activeOne, merged, activeTwo})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Active Two") || strings.Contains(view, "Merged Flow") {
		t.Fatalf("active-flow surface should select among non-merged rows only:\n%s", view)
	}

	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected active Flow launch command")
	}
	if _, ok := runImmediateTestCommand(cmd); !ok {
		t.Fatal("active Flow launch should prepare its phase")
	}
	if launched.FlowID != "active-two" || launched.PhaseID != "implementation" {
		t.Fatalf("launch update = %#v, want active-two implementation", launched)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	view = ansi.Strip(m.View())
	if m.Mode() != ui.ModeFlows || m.FlowSelected() != 1 || !strings.Contains(view, "Merged Flow") {
		t.Fatalf("normal Flow mode should retain merged selection after toggling back; selected=%d view:\n%s", m.FlowSelected(), view)
	}
}

func TestModel_ActiveFlowsRefreshDoesNotPrepareAutoLaunch(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{previous})

	_, cmd := update(m, model.ActiveFlowResultMsg{
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeActiveFlows),
	})
	if cmd != nil {
		t.Fatalf("active Flow refresh returned command %T, want display-only nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none from active Flow display refresh", updates)
	}
}

func TestModel_ActiveFlowsHorizontalNavigationIsInert(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	before := listRequests(m)

	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "left arrow", key: tea.KeyMsg{Type: tea.KeyLeft}},
		{name: "right arrow", key: tea.KeyMsg{Type: tea.KeyRight}},
		{name: "l", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cmd tea.Cmd
			m, cmd = update(m, tc.key)
			if cmd != nil {
				t.Fatalf("%s from active Flow surface returned command %T, want nil", tc.name, cmd)
			}
			if m.ActivePane() == ui.PaneRepos {
				t.Fatalf("%s from active Flow surface active pane = %d, want right pane", tc.name, m.ActivePane())
			}
			if m.Mode() != ui.ModeActiveFlows {
				t.Fatalf("%s from active Flow surface mode = %d, want active flows", tc.name, m.Mode())
			}
			if view := ansi.Strip(m.View()); !strings.Contains(view, "Active flows") {
				t.Fatalf("%s from active Flow surface should keep active Flow view:\n%s", tc.name, view)
			}
		})
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_ActiveFlowsNewFlowKeyIsIgnored(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("StartFlowPlan should not be called from active flows")
			return model.FlowStartResult{}, nil
		},
	}), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatalf("n from active Flow surface returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("n from active Flow surface opened overlay %d, want none", m.Overlay())
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("n from active Flow surface mode = %d, want active flows", m.Mode())
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Active flows") {
		t.Fatalf("n from active Flow surface should keep active Flow view visible:\n%s", view)
	}
	if strings.Contains(view, "New flow") || strings.Contains(view, ui.FlowTitleInputPlaceholder) {
		t.Fatalf("n from active Flow surface should not open new Flow form:\n%s", view)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_ActiveFlowsTabWithoutTerminalCyclesBetweenLeftAndList(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("tab from active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("tab from active Flow surface active pane = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("tab from active Flow surface mode = %d, want active flows", m.Mode())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Active flows") {
		t.Fatalf("tab from active Flow surface should keep active Flow view visible:\n%s", view)
	}
	assertListRequestsUnchanged(t, before, m)

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("tab back to active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() == ui.PaneRepos {
		t.Fatalf("second tab active pane = %d, want active Flow list", m.ActivePane())
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_ActiveFlowsTabWithTerminalCyclesListTerminalLeft(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	flowOne := flowWithPhaseDetails()
	flowTwo := flowWithPhaseDetails()
	flowTwo.FlowID = "flow-2"
	flowTwo.Title = "Second active flow"
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flowOne, flowTwo}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updated := flowOne
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Status = flowstore.PhaseRunning
					updated.Phases[i].LaunchIDs = append(updated.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return updated, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowOne, flowTwo})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flowOne, flowTwo})
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g on active Flow should prepare an embedded launch")
	}
	m, asyncCmd := update(m, cmd())
	m = settleModelCommands(t, m, asyncCmd, 2)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("active Flow terminal command mode should not forward unknown command bytes: %#v", fakeTerm.writes)
	}
	if got := m.TransientError(); !strings.Contains(got, "Unknown terminal prefix command") {
		t.Fatalf("status = %q, want unknown terminal command", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x1d" {
		t.Fatalf("ctrl+] in active Flow terminal command mode should send literal ctrl+], writes = %#v", fakeTerm.writes)
	}
	before := listRequests(m)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("tab from active Flow terminal active pane = %d, want left pane", m.ActivePane())
	}
	assertListRequestsUnchanged(t, before, m)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.ActivePane() == ui.PaneRepos {
		t.Fatalf("tab from left pane active pane = %d, want active Flow list", m.ActivePane())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "agent output") {
		t.Fatalf("returning to active Flow list should keep the selected Flow terminal associated:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 1 {
		t.Fatalf("active Flow list focus should not forward j to terminal: %#v", fakeTerm.writes)
	}
	if m.FlowSelected() != 0 {
		t.Fatalf("normal Flow selection = %d, want unchanged while active Flow list moves", m.FlowSelected())
	}
	if got := model.ActiveFlowSelectedForTest(m); got != 1 {
		t.Fatalf("active Flow selection = %d, want list focus to move to second flow", got)
	}
}

func TestModel_FlowsTabWithoutTerminalCyclesBetweenLeftAndList(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("tab from Flow list returned command %T, want nil", cmd)
	}
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("tab from Flow list active pane = %d, want left pane", m.ActivePane())
	}
	assertListRequestsUnchanged(t, before, m)

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("tab back to Flow list returned command %T, want nil", cmd)
	}
	if m.ActivePane() == ui.PaneRepos {
		t.Fatalf("second tab active pane = %d, want Flow list", m.ActivePane())
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_CollapsedFlowPaneWithoutTerminalIgnoresTab(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	for i := 0; i < 3; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
		if !m.RepoPaneCollapsed() || m.ActivePane() == ui.PaneRepos {
			t.Fatalf("tab %d collapsed=%t activePane=%d, want collapsed Flow list", i+1, m.RepoPaneCollapsed(), m.ActivePane())
		}
	}
}

func TestModel_CollapsedFlowPaneTabsBetweenListAndTerminalAndForwardsCtrlR(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:     "codex",
		FlowID:      "flow-1",
		FlowPhaseID: "implementation",
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.RepoPaneCollapsed() || m.ActivePane() == ui.PaneRepos {
		t.Fatalf("setup collapsed=%t activePane=%d, want collapsed Flow list", m.RepoPaneCollapsed(), m.ActivePane())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.RepoPaneCollapsed() || m.ActivePane() == ui.PaneRepos {
		t.Fatalf("tab to Flow terminal collapsed=%t activePane=%d, want collapsed content pane", m.RepoPaneCollapsed(), m.ActivePane())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x12" {
		t.Fatalf("Flow terminal ctrl+r writes = %#v, want ctrl+r byte", fakeTerm.writes)
	}
	if !m.RepoPaneCollapsed() || m.ActivePane() == ui.PaneRepos {
		t.Fatalf("Flow terminal ctrl+r collapsed=%t activePane=%d, want unchanged collapsed content pane", m.RepoPaneCollapsed(), m.ActivePane())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.RepoPaneCollapsed() || m.ActivePane() == ui.PaneRepos {
		t.Fatalf("tab to Flow list collapsed=%t activePane=%d, want collapsed content pane", m.RepoPaneCollapsed(), m.ActivePane())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.RepoPaneCollapsed() || m.ActivePane() == ui.PaneRepos {
		t.Fatalf("second tab to Flow terminal collapsed=%t activePane=%d, want collapsed content pane", m.RepoPaneCollapsed(), m.ActivePane())
	}
}

func TestModel_ActiveFlowsBackspaceClearsSelectedPhaseWhenLeavingRightPane(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "plan" {
		t.Fatalf("selected active Flow phase = %q, want plan before leaving right pane", got)
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatalf("Backspace from active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("Backspace from active Flow surface active pane = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("Backspace from active Flow surface mode = %d, want active flows", m.Mode())
	}
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "" {
		t.Fatalf("Backspace from active Flow surface left selected phase = %q, want cleared", got)
	}
}

func TestModel_ActiveFlowsLeftPaneNumberedKeysAreNoOps(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		t.Fatalf("left-pane numbered key on active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("left-pane numbered key activePane = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("left-pane numbered key changed mode = %d, want active flows", m.Mode())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Active flows") {
		t.Fatalf("left-pane numbered key should keep active Flow view visible:\n%s", view)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_ActiveFlowsFetchErrorUsesActiveFetchMode(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), nil)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 18})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, nil)

	m, _ = update(m, model.FetchErrorMsg{
		Pane:        "active-flows",
		Err:         "failed to load active flows: boom",
		Kind:        model.FetchList,
		Mode:        ui.ModeActiveFlows,
		ListRequest: m.ListRequest(ui.ModeActiveFlows),
	})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "failed to load active flows: boom") {
		t.Fatalf("active Flow surface should show Flow fetch failure in status bar:\n%s", view)
	}
	if !strings.Contains(view, "Could not load active flows; see status bar") {
		t.Fatalf("empty active Flow surface should show its fetch failure:\n%s", view)
	}
}

func TestModel_ActiveFlowsSearchAcceptsDigits(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.Title = "Release Flow 123"
	flow.Branch = "release/123"
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd != nil {
		t.Fatalf("digit in active Flow search returned command %T, want nil", cmd)
	}
	if got := m.ItemSearch(); got != "1" {
		t.Fatalf("active Flow search query = %q, want 1", got)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "active flows") || !strings.Contains(view, "release/123") {
		t.Fatalf("digit search should keep active Flow surface visible and filtered:\n%s", view)
	}
}

func TestModel_ActiveFlowsPullDoesNotTargetHiddenWorktree(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.WorktreePath = "/dev/alpha-worktrees/visible-flow"
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.WorktreeResultMsg{
		RepoPath: "/dev/alpha",
		Worktrees: []gitquery.Worktree{{
			Path:       "/dev/alpha-worktrees/hidden-worktree",
			BranchName: "hidden",
		}},
		ListRequest: m.ListRequest(ui.ModeWorktrees),
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if cmd != nil {
		t.Fatalf("F from active Flow surface returned command %T, want nil to avoid hidden worktree pull", cmd)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Active flows") {
		t.Fatalf("F from active Flow surface should leave active Flow view visible:\n%s", view)
	}
}

func TestModel_EnterTogglesActiveFlowPhaseRows(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 18})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter on active Flow row returned command %T, want nil", cmd)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Plan Review") || !strings.Contains(view, "Implementation") {
		t.Fatalf("enter should expand active Flow phase rows:\n%s", view)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("second enter on active Flow row returned command %T, want nil", cmd)
	}
	view = ansi.Strip(m.View())
	if strings.Contains(view, "Plan Review") || strings.Contains(view, "Implementation") {
		t.Fatalf("second enter should collapse active Flow phase rows:\n%s", view)
	}
}

func TestModel_ActiveFlowRefreshPreservesNormalFlowSelection(t *testing.T) {
	activeOne := flowWithPhaseDetails()
	activeOne.FlowID = "active-one"
	activeOne.Title = "Active One"
	activeTwo := flowWithPhaseDetails()
	activeTwo.FlowID = "active-two"
	activeTwo.Title = "Active Two"

	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{activeOne, activeTwo})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.FlowSelected(); got != 1 {
		t.Fatalf("normal Flow selection = %d, want active-two", got)
	}
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{activeOne, activeTwo})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})

	m, _ = update(m, model.ActiveFlowResultMsg{
		Flows:       []flowstore.FlowRecord{activeOne, activeTwo},
		ListRequest: m.ListRequest(ui.ModeActiveFlows),
	})
	m, _ = switchTestMode(m, ui.ModeFlows)
	if got := m.FlowSelected(); got != 1 {
		t.Fatalf("normal Flow selection after active refresh = %d, want preserved active-two", got)
	}
}

func TestModel_RKeyResumesActiveFlowPhaseSession(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	flow := flowWithPhaseDetails()
	flow.WorktreePath = "/dev/alpha-worktrees/active-resume"
	flow.Phases = []flowstore.FlowPhase{{
		PhaseID: "review-loop",
		Title:   "Review loop",
		Status:  flowstore.PhaseCompleted,
		Sessions: []flowstore.Session{
			{Provider: "codex", SessionID: "codex-review", Status: "ended", StartedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)},
		},
	}}
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected active Flow phase resume command")
	}
	m = settleModelCommands(t, m, cmd, 2)
	if started.ResumeSessionID != "codex-review" ||
		started.FlowID != "flow-1" ||
		started.FlowPhaseID != "review-loop" ||
		started.WorktreePath != "/dev/alpha-worktrees/active-resume" ||
		!started.Embedded ||
		started.Headless ||
		!started.FlowLaunchTracked {
		t.Fatalf("unexpected active Flow phase resume context: %#v", started)
	}
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || !launchUpdate.Resume {
		t.Fatalf("launch update = %#v, want tracked resume for review-loop", launchUpdate)
	}
}

func TestModel_TrackedFlowPhaseResumeRechecksCloseAfterPersistence(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.WorktreePath = "/dev/alpha-worktrees/tracked-resume"
	flow.Phases = []flowstore.FlowPhase{{
		PhaseID: "review-loop",
		Title:   "Review loop",
		Status:  flowstore.PhaseCompleted,
		Sessions: []flowstore.Session{{
			Provider: "codex", SessionID: "codex-review", Status: "ended",
		}},
	}}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flow, nil
		},
		ReserveFlowLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{}, nil, errors.New("flow is closed")
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, persistCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if persistCmd == nil {
		t.Fatal("expected tracked resume persistence command")
	}
	m, launchCmd := update(m, persistCmd())
	if launchCmd != nil {
		m = settleModelCommands(t, m, launchCmd, 2)
	}
	if starts != 0 {
		t.Fatalf("closed Flow started %d tracked resume terminals after persistence, want zero", starts)
	}
	if !strings.Contains(m.TransientError(), "closed") {
		t.Fatalf("status = %q, want final-spawn closed rejection", m.TransientError())
	}
}

func TestModel_InteractiveActiveFlowLaunchFocusesEmbeddedTerminal(t *testing.T) {
	var started actions.AgentLaunchContext
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	flow := flowWithPhaseDetails()
	flow.Headless = false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updated := flow
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Status = flowstore.PhaseRunning
					updated.Phases[i].LaunchIDs = append(updated.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return updated, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected active Flow launch command")
	}
	m = settleModelCommands(t, m, cmd, 3)
	if started.FlowID != "flow-1" || started.FlowPhaseID != "implementation" || started.Headless {
		t.Fatalf("unexpected active Flow launch context: %#v", started)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) == 0 || fakeTerm.writes[len(fakeTerm.writes)-1] != "z" {
		t.Fatalf("interactive active Flow launch should focus terminal input and forward z: %#v", fakeTerm.writes)
	}
}

func TestModel_F3PassesThroughFocusedActiveFlowTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	flow := flowWithPhaseDetails()
	flow.Headless = false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updated := flow
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Status = flowstore.PhaseRunning
					updated.Phases[i].LaunchIDs = append(updated.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return updated, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected active Flow launch command")
	}
	m = settleModelCommands(t, m, cmd, 3)
	writeCount := len(fakeTerm.writes)

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyF3})
	if cmd != nil {
		t.Fatalf("F3 in focused active Flow terminal returned command %T, want nil", cmd)
	}
	if len(fakeTerm.writes) != writeCount+1 || fakeTerm.writes[len(fakeTerm.writes)-1] != "\x1bOR" {
		t.Fatalf("focused active Flow terminal F3 writes = %#v, want F3 escape sequence", fakeTerm.writes)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Active flows") {
		t.Fatalf("F3 in focused active Flow terminal should not toggle active Flow surface:\n%s", view)
	}
}

func TestModel_ActiveFlowLaunchOverSessionsUsesFlowTerminal(t *testing.T) {
	var flowTerm *fakeEmbeddedTerminal
	flow := flowWithPhaseDetails()
	flow.Headless = false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updated := flow
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Status = flowstore.PhaseRunning
					updated.Phases[i].LaunchIDs = append(updated.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return updated, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			flowTerm = &fakeEmbeddedTerminal{state: "running"}
			return flowTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "flow/with-phases") || strings.Contains(view, "codex-session-1") {
		t.Fatalf("active flows should visually override sessions mode:\n%s", view)
	}
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected active Flow launch command over Sessions")
	}
	m = settleModelCommands(t, m, cmd, 3)
	if flowTerm == nil {
		t.Fatal("expected active Flow launch to start a flow embedded terminal")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(flowTerm.writes) == 0 || flowTerm.writes[len(flowTerm.writes)-1] != "z" {
		t.Fatalf("active Flow input over Sessions should go to flow terminal: %#v", flowTerm.writes)
	}
}

func flowWithAwaitingImplementation() flowstore.FlowRecord {
	flow := flowWithPhaseDetails()
	for i := range flow.Phases {
		if flow.Phases[i].PhaseID == "implementation" {
			flow.Phases[i].Status = flowstore.PhaseRunning
			flow.Phases[i].LaunchIDs = []string{"launch-orphan"}
			flow.Phases[i].Sessions = nil
		}
	}
	return flow
}

func flowWithEndedRunningImplementation() flowstore.FlowRecord {
	flow := flowWithPhaseDetails()
	for i := range flow.Phases {
		if flow.Phases[i].PhaseID == "implementation" {
			flow.Phases[i].Status = flowstore.PhaseRunning
			flow.Phases[i].LaunchIDs = []string{"launch-ended"}
			flow.Phases[i].Sessions = []flowstore.Session{
				{Provider: "codex", SessionID: "codex-ended", LaunchID: "launch-ended", Status: "ended"},
			}
		}
	}
	return flow
}

func autoFlowWithPhaseStatuses(statuses map[string]string) flowstore.FlowRecord {
	defs := []struct {
		id    string
		title string
		order int
	}{
		{id: "plan", title: "Plan", order: 1},
		{id: "plan-review", title: "Plan Review", order: 2},
		{id: "implementation", title: "Implementation", order: 3},
		{id: "review-loop", title: "Review loop", order: 4},
		{id: "pr-creation", title: "PR creation", order: 5},
		{id: "autoreview", title: "Autoreview", order: 6},
		{id: "merge", title: "Merge", order: 7},
	}
	phases := make([]flowstore.FlowPhase, 0, len(statuses))
	for _, def := range defs {
		status, ok := statuses[def.id]
		if !ok {
			continue
		}
		phase := flowstore.FlowPhase{
			PhaseID: def.id,
			Title:   def.title,
			Status:  status,
			Order:   def.order,
		}
		if def.id == "plan-review" && status == flowstore.PhaseCompleted {
			phase.Outcome = flowstore.OutcomeApproved
		}
		phases = append(phases, phase)
	}
	return autoFlowWithPhases(phases...)
}

func autoFlowWithPhases(phases ...flowstore.FlowPhase) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		Title:        "Auto Flow",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/auto",
		AutoMode:     true,
		Headless:     true,
		Phases:       phases,
	}
}

// autoAdvanceLaunchCommand drives one advance-poll drain and registers the
// snapshot it feeds in as the authoritative read. AutoMode reads the exact Flow
// it is about to launch, and these records never reach a pane, so without this
// the read would fall through to the developer's live flow store.
func autoAdvanceLaunchCommand(m model.Model, flows []flowstore.FlowRecord) (model.Model, tea.Cmd) {
	recordTestFlowRecords(flows)
	return model.AutoAdvanceLaunchCommandForTest(m, flows)
}

func autoLaunchFromFlowRefresh(t *testing.T, previous, current flowstore.FlowRecord) (model.FlowEmbeddedLaunchRequestedMsg, []flowstore.PhaseLaunchUpdate) {
	t.Helper()
	m, cmd, updates := autoLaunchCommandFromFlowRefresh(t, previous, current)
	if cmd == nil {
		t.Fatal("Flow refresh should return auto-launch command")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("auto-launch produced %d embedded launches, want 1", len(launches))
	}
	return launches[0], *updates
}

func autoLaunchCommandFromFlowRefresh(t *testing.T, previous, current flowstore.FlowRecord) (model.Model, tea.Cmd, *[]flowstore.PhaseLaunchUpdate) {
	t.Helper()
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			if !update.AutoLaunch {
				t.Fatalf("auto launch update = %#v, want AutoLaunch true", update)
			}
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
	})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	m, cmd := autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	return m, cmd, &updates
}

// flowEmbeddedLaunchesFromCommand drives the AutoMode launch lifecycle to its
// prepared launch contexts. It needs the Model because the authoritative read
// event has to go back through Update before any prepare command exists.
func flowEmbeddedLaunchesFromCommand(t *testing.T, m model.Model, cmd tea.Cmd) []model.FlowEmbeddedLaunchRequestedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	_, prepared := model.AutoFlowLaunchesForTest(m, cmd)
	launches := make([]model.FlowEmbeddedLaunchRequestedMsg, 0)
	for _, launch := range prepared {
		if launch.Embedded {
			launches = append(launches, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: launch.Context})
		}
	}
	return launches
}

func TestModel_FlowAutoLaunchUsesConfiguredCLIAgentAndEffort(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var launchUpdate flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeModel:           "claude-sonnet-5",
		ClaudeReasoningEffort: "max",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
	})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	m, cmd := autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("auto-advance should return auto-launch command")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("auto-advance produced %d embedded launches, want 1", len(launches))
	}
	launch := launches[0]
	if !launchUpdate.AutoLaunch || launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID == "" {
		t.Fatalf("auto launch update = %#v", launchUpdate)
	}
	if launch.LaunchContext.Command != "claude" ||
		launch.LaunchContext.Model != "claude-sonnet-5" ||
		launch.LaunchContext.ReasoningEffort != "max" ||
		launch.LaunchContext.FlowID != "flow-1" ||
		launch.LaunchContext.FlowPhaseID != "implementation" ||
		!launch.LaunchContext.Embedded ||
		!launch.LaunchContext.Headless ||
		!launch.LaunchContext.FlowLaunchTracked {
		t.Fatalf("auto launch context = %#v", launch.LaunchContext)
	}
}

func TestModel_FlowAutoLaunchUsesStampedCrossProviderSettings(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan": flowstore.PhaseCompleted, "plan-review": flowstore.PhaseRunning, "implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan": flowstore.PhaseCompleted, "plan-review": flowstore.PhaseCompleted, "implementation": flowstore.PhaseReady,
	})
	for i := range current.Phases {
		if current.Phases[i].PhaseID == "implementation" {
			current.Phases[i].Agent = agent.CommandClaude
			current.Phases[i].Model = agent.ModelClaudeOpus5
		}
	}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:          agent.CommandCodex,
		CodexModel:            agent.ModelGPT55,
		CodexReasoningEffort:  agent.ReasoningEffortMedium,
		ClaudeModel:           agent.ModelClaudeSonnet5,
		ClaudeReasoningEffort: agent.ReasoningEffortMax,
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
	})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	m, cmd := autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("auto-advance produced %d launches, want 1", len(launches))
	}
	ctx := launches[0].LaunchContext
	if ctx.Command != agent.CommandClaude || ctx.Model != agent.ModelClaudeOpus5 || ctx.ReasoningEffort != agent.ReasoningEffortMax {
		t.Fatalf("stamped auto launch context = %#v", ctx)
	}
}

func TestModel_FlowAutoLaunchIsAlwaysHeadlessRegardlessOfStoredManualPreference(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored_%v", stored), func(t *testing.T) {
			previous := autoFlowWithPhaseStatuses(map[string]string{
				"plan": flowstore.PhaseRunning, "implementation": flowstore.PhasePending,
			})
			current := autoFlowWithPhaseStatuses(map[string]string{
				"plan": flowstore.PhaseCompleted, "implementation": flowstore.PhaseReady,
			})
			previous.Headless = stored
			current.Headless = stored
			launch, _ := autoLaunchFromFlowRefresh(t, previous, current)
			if !launch.LaunchContext.Headless || !launch.LaunchContext.FlowAutoLaunch {
				t.Fatalf("auto launch context = %#v, want always-headless auto launch", launch.LaunchContext)
			}
		})
	}
}

func phaseByID(record flowstore.FlowRecord, phaseID string) flowstore.FlowPhase {
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase
		}
	}
	return flowstore.FlowPhase{}
}

func expandSelectedFlowWithEnter(t *testing.T, m model.Model) model.Model {
	t.Helper()
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter on Flow row returned command %T, want nil", cmd)
	}
	if m.ExpandedFlowID() == "" {
		t.Fatal("enter on Flow row should expand phase rows")
	}
	if got := m.SelectedFlowPhaseID(); got != "" {
		t.Fatalf("enter on Flow row selected phase %q, want Flow row selected", got)
	}
	return m
}

// flowRecordsForTerminals builds one Flow per embedded terminal a test needs.
// The launch lifecycle refuses a second launch into a Flow whose terminal is
// still installed, so simultaneous terminals need distinct Flows.
func flowRecordsForTerminals(count int) []flowstore.FlowRecord {
	records := make([]flowstore.FlowRecord, 0, count)
	for i := range count {
		record := flowWithPhaseDetails()
		record.FlowID = fmt.Sprintf("flow-%d", i+1)
		record.Title = fmt.Sprintf("Flow %d", i+1)
		records = append(records, record)
	}
	return records
}

// selectFlowRow moves the Flow cursor to a row index.
func selectFlowRow(t *testing.T, m model.Model, index int) model.Model {
	t.Helper()
	for i := 0; i < 50 && m.FlowSelected() > index; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	for i := 0; i < 50 && m.FlowSelected() < index; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := m.FlowSelected(); got != index {
		t.Fatalf("selected Flow row = %d, want %d", got, index)
	}
	return m
}

func selectFlowPhaseByID(t *testing.T, m model.Model, phaseID string) model.Model {
	t.Helper()
	if m.ExpandedFlowID() == "" {
		m = expandSelectedFlowWithEnter(t, m)
	}
	for i := 0; i < 50 && m.SelectedFlowPhaseID() != phaseID; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := m.SelectedFlowPhaseID(); got != phaseID {
		t.Fatalf("selected Flow phase = %q, want %q", got, phaseID)
	}
	return m
}

func flowFetchMsgFromCommand(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected flow fetch command")
	}
	for _, msg := range immediateTestCommandMessages(cmd) {
		switch typed := msg.(type) {
		case model.FlowResultMsg:
			return typed
		case model.FetchErrorMsg:
			if typed.Mode == ui.ModeFlows {
				return typed
			}
		}
	}
	t.Fatal("command did not produce a Flow result")
	return nil
}

func flowResultFromCommand(t *testing.T, cmd tea.Cmd) model.FlowResultMsg {
	t.Helper()
	msg := flowFetchMsgFromCommand(t, cmd)
	result, ok := msg.(model.FlowResultMsg)
	if !ok {
		t.Fatalf("flow fetch command returned %T, want FlowResultMsg", msg)
	}
	return result
}

func applyFlowResultFollowup(t *testing.T, m model.Model, cmd tea.Cmd) model.Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	result, ok := msg.(model.FlowResultMsg)
	if !ok {
		t.Fatalf("follow-up command returned %T, want FlowResultMsg", msg)
	}
	m, _ = update(m, result)
	return m
}

func prepareSelectedFlowPhaseLaunch(t *testing.T, m model.Model, phaseID string) (model.Model, tea.Cmd) {
	t.Helper()
	m = selectFlowPhaseByID(t, m, phaseID)
	return flowLaunchFromKey(t, m)
}

// advanceFlowLaunchRead runs the lifecycle's authoritative read hop and returns
// the command that prepares the launch, so callers keep working against a
// single command that installs the terminal when its message is applied.
func advanceFlowLaunchRead(t *testing.T, m model.Model, cmd tea.Cmd) (model.Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return m, nil
	}
	msg, ok := runImmediateTestCommand(cmd)
	if !ok {
		// A refused launch returns no asynchronous work, only the status timer.
		return m, cmd
	}
	return update(m, msg)
}

func prepareSelectedFlowPhaseHeadlessOffLaunch(t *testing.T, m model.Model, phaseID string) (model.Model, tea.Cmd) {
	t.Helper()
	m = selectFlowPhaseByID(t, m, phaseID)
	return flowLaunchFromKey(t, m)
}

// runPreparedFlowEmbeddedLaunch drives everything g returns: the lifecycle's
// authoritative read, its prepare hop, and the embedded install that follows.
func runPreparedFlowEmbeddedLaunch(t *testing.T, m model.Model, cmd tea.Cmd) model.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("Flow phase launch should return a command")
	}
	return settleModelCommands(t, m, cmd, 4)
}

func prepareSelectedFlowPhaseEmbeddedLaunch(t *testing.T, m model.Model, phaseID string) (model.Model, tea.Cmd) {
	t.Helper()
	m = selectFlowPhaseByID(t, m, phaseID)
	return flowLaunchFromKey(t, m)
}

func flowLaunchKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
}

// flowLaunchFromKey presses g and runs the launch lifecycle's authoritative
// read hop, returning the command whose message completes the launch.
func flowLaunchFromKey(t *testing.T, m model.Model) (model.Model, tea.Cmd) {
	t.Helper()
	m, cmd := update(m, flowLaunchKey())
	return advanceFlowLaunchRead(t, m, cmd)
}

func TestModel_AKeyTogglesFlowAutoModeFromFlowRow(t *testing.T) {
	flow := flowWithPhaseDetails()
	updated := flow
	updated.AutoMode = true
	var calls []flowstore.AutoModeUpdate
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMode: func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("a on selected Flow row should persist auto-mode toggle")
	}
	m, follow := update(m, cmd())
	if follow != nil {
		t.Fatalf("auto-mode result returned follow-up command %T, want nil", follow)
	}
	if len(calls) != 1 || calls[0].FlowID != "flow-1" || !calls[0].Enabled {
		t.Fatalf("auto-mode calls = %#v, want enable flow-1", calls)
	}
	if got := m.Flows(); len(got) != 1 || !got[0].AutoMode {
		t.Fatalf("Flows() = %#v, want auto mode enabled", got)
	}
	if m.SelectedFlowPhaseID() != "" {
		t.Fatalf("selected phase = %q, want Flow row selected", m.SelectedFlowPhaseID())
	}
}

func TestModel_HKeyPersistsSelectedFlowFromPhaseRowWithoutAffectingAnotherFlow(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	first := flowWithPhaseDetails()
	first.UpdatedAt = now
	second := flowWithPhaseDetails()
	second.FlowID = "flow-2"
	second.Title = "Second Flow"
	second.UpdatedAt = now
	updated := first
	updated.Headless = false
	updated.UpdatedAt = now.Add(time.Minute)
	var calls []flowstore.HeadlessUpdate
	m := newTestModel(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{first, second})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd == nil {
		t.Fatal("h on selected phase returned nil command")
	}
	m, _ = update(m, cmd())
	if len(calls) != 1 || calls[0] != (flowstore.HeadlessUpdate{FlowID: first.FlowID, Enabled: false}) {
		t.Fatalf("headless calls = %#v", calls)
	}
	flows := m.Flows()
	if len(flows) != 2 || flows[0].Headless || !flows[1].Headless {
		t.Fatalf("Flows() = %#v, want only first Flow disabled", flows)
	}
	if m.ExpandedFlowID() != first.FlowID || m.SelectedFlowPhaseID() != "implementation" {
		t.Fatalf("expanded/phase = %q/%q, want %s/implementation", m.ExpandedFlowID(), m.SelectedFlowPhaseID(), first.FlowID)
	}
}

func TestModel_HKeyPersistenceFailureAndNoSelectionLeaveVisibleStateUnchanged(t *testing.T) {
	t.Run("persistence failure", func(t *testing.T) {
		flow := flowWithPhaseDetails()
		m := newTestModel(testRepos(), model.Options{
			SetFlowHeadless: func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
				return flowstore.FlowRecord{}, errors.New("state root locked")
			},
		})
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
		if cmd == nil {
			t.Fatal("h returned nil persistence command")
		}
		m, _ = update(m, cmd())
		if got := m.Flows(); len(got) != 1 || !got[0].Headless {
			t.Fatalf("Flows() = %#v, want unchanged headless preference", got)
		}
		if got := m.TransientError(); !strings.Contains(got, "failed to set Flow headless mode") || !strings.Contains(got, "state root locked") {
			t.Fatalf("status = %q, want clear persistence error", got)
		}
	})

	t.Run("no selection", func(t *testing.T) {
		called := false
		m := newTestModel(testRepos(), model.Options{
			SetFlowHeadless: func(flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
				called = true
				return flowstore.FlowRecord{}, nil
			},
		})
		m = flowsInRightPane(t, m, nil)
		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
		if cmd != nil || called {
			t.Fatalf("h without selection returned %T, adapter called=%v", cmd, called)
		}
		if strings.Contains(m.View(), "headless on") || strings.Contains(m.View(), "headless off") {
			t.Fatalf("headless control should be hidden without a selected Flow:\n%s", m.View())
		}
	})
}

func TestModel_HKeyTogglesSelectedActiveFlowPhaseAndPreservesExpansion(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.UpdatedAt = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	updated := flow
	updated.Headless = false
	updated.UpdatedAt = flow.UpdatedAt.Add(time.Minute)
	m := newTestModel(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			if update.FlowID != flow.FlowID || update.Enabled {
				t.Fatalf("SetFlowHeadless(%#v), want disable %s", update, flow.FlowID)
			}
			return updated, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 20 && model.SelectedActiveFlowPhaseIDForTest(m) != "implementation"; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd == nil {
		t.Fatal("h on selected active phase returned nil command")
	}
	msg, ok := cmd().(model.FlowHeadlessSetMsg)
	if !ok || !msg.AllRepositories {
		t.Fatalf("active h command returned %#v, want all-repositories result", msg)
	}
	m, _ = update(m, msg)
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].Headless {
		t.Fatalf("active Flows = %#v, want disabled headless", got)
	}
	if model.SelectedActiveFlowPhaseIDForTest(m) != "implementation" {
		t.Fatalf("selected active phase = %q, want implementation", model.SelectedActiveFlowPhaseIDForTest(m))
	}
}

func TestModel_FlowHeadlessResultValidatesScopeIdentityAndCacheFreshness(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	normal := flowWithPhaseDetails()
	normal.UpdatedAt = t0.Add(2 * time.Minute)
	active := normal
	active.UpdatedAt = t0
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{normal})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{active})

	between := normal
	between.Headless = false
	between.UpdatedAt = t0.Add(time.Minute)
	m, _ = update(m, model.FlowHeadlessSetMsg{
		RepoPath: "/dev/alpha", FlowID: normal.FlowID, Flow: between, AllRepositories: true,
	})
	if got := m.Flows(); len(got) != 1 || !got[0].Headless {
		t.Fatalf("newer normal cache was downgraded: %#v", got)
	}
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].Headless {
		t.Fatalf("older active cache did not accept between result: %#v", got)
	}

	newer := between
	newer.FlowID = "different-flow"
	newer.UpdatedAt = t0.Add(3 * time.Minute)
	m, _ = update(m, model.FlowHeadlessSetMsg{
		RepoPath: "/dev/alpha", FlowID: normal.FlowID, Flow: newer, AllRepositories: true,
	})
	if got := m.Flows(); !got[0].Headless {
		t.Fatalf("mismatched message/record IDs changed normal cache: %#v", got)
	}

	offRepo := normal
	offRepo.Headless = false
	offRepo.UpdatedAt = t0.Add(4 * time.Minute)
	m, _ = update(m, model.FlowHeadlessSetMsg{RepoPath: "/dev/bravo", FlowID: normal.FlowID, Flow: offRepo})
	if got := m.Flows(); !got[0].Headless {
		t.Fatalf("off-repo normal result changed current cache: %#v", got)
	}

	bravo := flowWithPhaseDetails()
	bravo.FlowID = "bravo-flow"
	bravo.RepoPath = "/dev/bravo"
	bravo.UpdatedAt = t0
	m = flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{normal})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{bravo})
	bravoUpdated := bravo
	bravoUpdated.Headless = false
	bravoUpdated.UpdatedAt = t0.Add(time.Minute)
	m, _ = update(m, model.FlowHeadlessSetMsg{
		RepoPath: "/dev/bravo", FlowID: bravo.FlowID, Flow: bravoUpdated, AllRepositories: true,
	})
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].FlowID != bravo.FlowID || got[0].Headless {
		t.Fatalf("valid cross-repo active result was not accepted: %#v", got)
	}

	older := bravoUpdated
	older.Headless = true
	older.UpdatedAt = t0
	m, _ = update(m, model.FlowHeadlessSetMsg{
		RepoPath: "/dev/bravo", FlowID: bravo.FlowID, Flow: older, AllRepositories: true,
	})
	if got := model.ActiveFlowsForTest(m); got[0].Headless {
		t.Fatalf("out-of-order older result downgraded active cache: %#v", got)
	}
}

func TestModel_NewerHeadlessResultSurvivesOlderWholeRecordResults(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	older := flowWithPhaseDetails()
	older.UpdatedAt = t0
	newer := older
	newer.Headless = false
	newer.UpdatedAt = t0.Add(time.Minute)

	t.Run("repository Flow list", func(t *testing.T) {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{older})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: newer.FlowID, Flow: newer, Enabled: false,
		})
		m, _ = update(m, model.FlowResultMsg{
			RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{older}, ListRequest: m.ListRequest(ui.ModeFlows),
		})
		if got := m.Flows(); len(got) != 1 || got[0].Headless {
			t.Fatalf("older Flow list replaced newer headless record: %#v", got)
		}
	})

	t.Run("Active Flows list", func(t *testing.T) {
		m := enterActiveFlowsWithRecords(t, model.New(testRepos()), []flowstore.FlowRecord{older})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: newer.FlowID, Flow: newer, Enabled: false, AllRepositories: true,
		})
		m, _ = update(m, model.ActiveFlowResultMsg{
			Flows: []flowstore.FlowRecord{older}, ListRequest: m.ListRequest(ui.ModeActiveFlows),
		})
		if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].Headless {
			t.Fatalf("older Active Flows list replaced newer headless record: %#v", got)
		}
	})

	t.Run("repository Flow list after Active Flows toggle", func(t *testing.T) {
		m := flowsInRightPane(t, model.New(testRepos()), nil)
		m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{older})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: newer.FlowID, Flow: newer, Enabled: false, AllRepositories: true,
		})
		m, _ = update(m, model.FlowResultMsg{
			RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{older}, ListRequest: m.ListRequest(ui.ModeFlows),
		})
		if got := m.Flows(); len(got) != 1 || got[0].Headless {
			t.Fatalf("older repository Flow list replaced newer Active Flows result: %#v", got)
		}
	})

	t.Run("Active Flows list after repository Flow toggle", func(t *testing.T) {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{older})
		m = enterActiveFlowsWithRecords(t, m, nil)
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: newer.FlowID, Flow: newer, Enabled: false,
		})
		m, _ = update(m, model.ActiveFlowResultMsg{
			Flows: []flowstore.FlowRecord{older}, ListRequest: m.ListRequest(ui.ModeActiveFlows),
		})
		if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].Headless {
			t.Fatalf("older Active Flows list replaced newer repository Flow result: %#v", got)
		}
	})

	t.Run("auto-mode result", func(t *testing.T) {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{older})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: newer.FlowID, Flow: newer, Enabled: false,
		})
		autoModeResult := older
		autoModeResult.AutoMode = true
		m, _ = update(m, model.FlowAutoModeSetMsg{
			RepoPath: "/dev/alpha", FlowID: older.FlowID, Flow: autoModeResult, Enabled: true,
		})
		if got := m.Flows(); len(got) != 1 || got[0].Headless || !got[0].UpdatedAt.Equal(newer.UpdatedAt) {
			t.Fatalf("older auto-mode result replaced newer headless record: %#v", got)
		}
	})

	t.Run("authoritative deletion", func(t *testing.T) {
		second := older
		second.FlowID = "flow-2"
		second.Title = "Second Flow"
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{newer, second})
		m, _ = update(m, model.FlowResultMsg{
			RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{second}, ListRequest: m.ListRequest(ui.ModeFlows),
		})
		if got := m.Flows(); len(got) != 1 || got[0].FlowID != second.FlowID || m.FlowSelected() != 0 {
			t.Fatalf("authoritative deletion did not remove cached Flow and select fallback: flows=%#v selected=%d", got, m.FlowSelected())
		}
	})
}

func TestModel_FlowHeadlessResultRejectsMissingReturnedRepoPath(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.UpdatedAt = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	malformed := flow
	malformed.RepoPath = ""
	malformed.Headless = false
	malformed.UpdatedAt = flow.UpdatedAt.Add(time.Minute)

	t.Run("repository Flows", func(t *testing.T) {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: flow.FlowID, Flow: malformed, Enabled: false,
		})
		if got := m.Flows(); len(got) != 1 || !got[0].Headless {
			t.Fatalf("malformed result changed repository Flow cache: %#v", got)
		}
	})

	t.Run("Active Flows", func(t *testing.T) {
		m := enterActiveFlowsWithRecords(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
		m, _ = update(m, model.FlowHeadlessSetMsg{
			RepoPath: "/dev/alpha", FlowID: flow.FlowID, Flow: malformed, Enabled: false, AllRepositories: true,
		})
		if got := model.ActiveFlowsForTest(m); len(got) != 1 || !got[0].Headless {
			t.Fatalf("malformed result changed Active Flows cache: %#v", got)
		}
	})
}

func TestModel_AKeyTogglesFlowAutoModeFromPhaseRowAndPreservesSelection(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.AutoMode = true
	updated := flow
	updated.AutoMode = false
	var calls []flowstore.AutoModeUpdate
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMode: func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("a on selected Flow phase should persist auto-mode toggle")
	}
	m, follow := update(m, cmd())
	if follow != nil {
		t.Fatalf("auto-mode result returned follow-up command %T, want nil", follow)
	}
	if len(calls) != 1 || calls[0].FlowID != "flow-1" || calls[0].Enabled {
		t.Fatalf("auto-mode calls = %#v, want disable flow-1", calls)
	}
	if got := m.Flows(); len(got) != 1 || got[0].AutoMode {
		t.Fatalf("Flows() = %#v, want auto mode disabled", got)
	}
	if m.ExpandedFlowID() != "flow-1" || m.SelectedFlowPhaseID() != "implementation" {
		t.Fatalf("expanded/phase = %q/%q, want flow-1/implementation", m.ExpandedFlowID(), m.SelectedFlowPhaseID())
	}
}

func TestModel_AKeyFlowAutoModeFailureReportsStatus(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMode: func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("a on selected Flow row should return persistence command")
	}
	for _, refreshMsg := range runLeadingBatchCmd(t, cmd) {
		var statusCmd tea.Cmd
		m, statusCmd = update(m, refreshMsg)
		if statusCmd == nil {
			t.Fatal("expected status expiry command")
		}
	}
	if got := m.TransientError(); !strings.Contains(got, "failed to set Flow auto mode") || !strings.Contains(got, "state root locked") {
		t.Fatalf("status = %q, want persistence failure", got)
	}
	if got := m.Flows(); len(got) != 1 || got[0].AutoMode {
		t.Fatalf("Flows() = %#v, want unchanged auto mode", got)
	}
}

func TestModel_AKeyFlowAutoModeNoopsWithoutSelectedFlow(t *testing.T) {
	called := false
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMode: func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			called = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, nil)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("a without selected Flow returned command %T, want nil", cmd)
	}
	if called {
		t.Fatal("SetFlowAutoMode should not be called without a selected Flow")
	}
}

func TestModel_AKeyTogglesFlowAutoModeFromActiveFlowRow(t *testing.T) {
	flow := flowWithPhaseDetails()
	updated := flow
	updated.AutoMode = true
	var calls []flowstore.AutoModeUpdate
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMode: func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("a on selected active Flow row should persist auto-mode toggle")
	}
	m, follow := update(m, cmd())
	if follow != nil {
		t.Fatalf("auto-mode result returned follow-up command %T, want nil", follow)
	}
	if len(calls) != 1 || calls[0].FlowID != "flow-1" || !calls[0].Enabled {
		t.Fatalf("auto-mode calls = %#v, want enable flow-1", calls)
	}
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || !got[0].AutoMode {
		t.Fatalf("active Flows = %#v, want auto mode enabled", got)
	}
}

func TestModel_AKeyTogglesFlowAutoModeFromActiveFlowPhaseRowAndPreservesSelection(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.AutoMode = true
	updated := flow
	updated.AutoMode = false
	var calls []flowstore.AutoModeUpdate
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMode: func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 50 && model.SelectedActiveFlowPhaseIDForTest(m) != "implementation"; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "implementation" {
		t.Fatalf("selected active Flow phase = %q, want implementation", got)
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("a on selected active Flow phase should persist auto-mode toggle")
	}
	m, follow := update(m, cmd())
	if follow != nil {
		t.Fatalf("auto-mode result returned follow-up command %T, want nil", follow)
	}
	if len(calls) != 1 || calls[0].FlowID != "flow-1" || calls[0].Enabled {
		t.Fatalf("auto-mode calls = %#v, want disable flow-1", calls)
	}
	if got := model.ActiveFlowsForTest(m); len(got) != 1 || got[0].AutoMode {
		t.Fatalf("active Flows = %#v, want auto mode disabled", got)
	}
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "implementation" {
		t.Fatalf("selected active Flow phase = %q, want preserved implementation", got)
	}
}

func TestModel_MKeyMarksSelectedFlowAsManuallyMerged(t *testing.T) {
	flow := manualMergeEligibleFlow()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	updated := flow
	updated.Status = flowstore.StatusMerged
	updated.PR.Status = flowstore.MergeMerged
	updated.Merge = flowstore.Merge{Status: flowstore.MergeMerged, Commit: "0123456789abcdef", MergedAt: &mergedAt}
	updated.Phases[len(updated.Phases)-1].Status = flowstore.PhaseCompleted
	updated.Phases[len(updated.Phases)-1].Outcome = flowstore.MergeMerged

	var lookupCalls int
	var markCalls []flowstore.ManualMergeUpdate
	m := newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(number int, url string) (actions.PullRequestMerge, error) {
			lookupCalls++
			if number != 116 || url != "https://github.com/approachcontrol/approach/pull/116" {
				t.Fatalf("LookupPRMerge(%d, %q), want stored PR", number, url)
			}
			return actions.PullRequestMerge{Commit: "0123456789abcdef", MergedAt: mergedAt}, nil
		},
		MarkFlowManualMerge: func(update flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error) {
			markCalls = append(markCalls, update)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatalf("opening manual merge confirmation returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm || !strings.Contains(m.ConfirmPrompt(), "Mark PR #116 as merged?") {
		t.Fatalf("manual merge confirmation prompt = %q overlay=%d", m.ConfirmPrompt(), m.Overlay())
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming manual merge should return command")
	}
	raw := cmd()
	if _, ok := raw.(model.FlowEmbeddedLaunchRequestedMsg); ok {
		t.Fatal("manual merge must not launch a Flow phase")
	}
	m, cmd = update(m, raw)
	if cmd != nil {
		t.Fatalf("manual merge result returned follow-up command %T, want nil", cmd)
	}

	if lookupCalls != 1 {
		t.Fatalf("lookup calls = %d, want 1", lookupCalls)
	}
	if len(markCalls) != 1 ||
		markCalls[0].FlowID != "flow-1" ||
		markCalls[0].PRNumber != 116 ||
		markCalls[0].PRURL != "https://github.com/approachcontrol/approach/pull/116" ||
		markCalls[0].Commit != "0123456789abcdef" ||
		!markCalls[0].MergedAt.Equal(mergedAt) {
		t.Fatalf("manual merge calls = %#v", markCalls)
	}
	if got := m.Flows(); len(got) != 1 || got[0].Status != flowstore.StatusMerged {
		t.Fatalf("Flows() = %#v, want merged replacement", got)
	}
}

func TestModel_ShiftMOpensModelPickerOnSelectedFlowRow(t *testing.T) {
	flow := manualMergeEligibleFlow()
	called := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			called = true
			return actions.PullRequestMerge{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd != nil {
		t.Fatalf("M on selected Flow row returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("M on selected Flow row overlay = %d, want model select", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "Choose codex model") {
		t.Fatalf("M on selected Flow row did not open model picker:\n%s", view)
	}
	if called {
		t.Fatal("LookupPRMerge should not run for uppercase M on selected Flow row")
	}
}

func TestModel_ShiftMOpensModelPickerOnSelectedFlowPhaseRow(t *testing.T) {
	flow := manualMergeEligibleFlow()
	m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "merge")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd != nil {
		t.Fatalf("M on selected Flow phase returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("M on selected Flow phase overlay = %d, want model select", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "Choose model for phase merge") || !strings.Contains(view, "inherit global") {
		t.Fatalf("M on selected Flow phase did not open model picker:\n%s", view)
	}
}

func TestModel_PhaseAgentPickerPersistsWithoutChangingGlobals(t *testing.T) {
	flow := manualMergeEligibleFlow()
	var got flowstore.PhaseAgentSettingsUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		CodexModel:   "gpt-5.5",
		ClaudeModel:  "claude-sonnet-5",
		SetFlowPhaseAgentSettings: func(update flowstore.PhaseAgentSettingsUpdate) (flowstore.FlowRecord, error) {
			got = update
			updated := flow
			updated.Phases = append([]flowstore.FlowPhase(nil), flow.Phases...)
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Agent = update.Settings.Agent
					updated.Phases[i].Model = update.Settings.Model
					updated.Phases[i].ReasoningEffort = update.Settings.ReasoningEffort
				}
			}
			updated.UpdatedAt = time.Now()
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "merge")

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	view := m.View()
	if !strings.Contains(view, "inherit global settings") || !strings.Contains(view, "Choose agent for phase merge") {
		t.Fatalf("phase agent picker contents:\n%s", view)
	}
	for range 2 {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("phase agent selection returned no persistence command")
	}
	m, _ = update(m, cmd())
	if got.FlowID != flow.FlowID || got.PhaseID != "merge" || got.Settings != (flowstore.PhaseAgentSettings{Agent: "claude"}) {
		t.Fatalf("phase settings update = %#v", got)
	}
	if m.AgentCommand() != "codex" {
		t.Fatalf("global agent changed to %q", m.AgentCommand())
	}
	if view := m.View(); !strings.Contains(view, "A      claude") || !strings.Contains(view, "M      sonnet-5") {
		t.Fatalf("phase effective labels were not refreshed:\n%s", view)
	}
}

func TestModel_PhaseAgentPickerTargetsExactWhitespaceDuplicate(t *testing.T) {
	flow := manualMergeEligibleFlow()
	canonical := flow.Phases[0]
	canonical.PhaseID = "Implementation"
	canonical.Title = "Canonical"
	canonical.Agent = agent.CommandCodex
	duplicate := canonical
	duplicate.PhaseID = " Implementation "
	duplicate.Title = "Whitespace duplicate"
	duplicate.Agent = agent.CommandClaude
	flow.Phases = []flowstore.FlowPhase{canonical, duplicate}

	var got flowstore.PhaseAgentSettingsUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		SetFlowPhaseAgentSettings: func(update flowstore.PhaseAgentSettingsUpdate) (flowstore.FlowRecord, error) {
			got = update
			updated := flow
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Agent = update.Settings.Agent
					updated.Phases[i].Model = update.Settings.Model
					updated.Phases[i].ReasoningEffort = update.Settings.ReasoningEffort
				}
			}
			updated.UpdatedAt = time.Now()
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, duplicate.PhaseID)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if view := m.View(); !strings.Contains(view, "> claude") {
		t.Fatalf("picker targeted the wrong normalized duplicate:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("phase agent selection returned no persistence command")
	}
	m, _ = update(m, cmd())
	if got.PhaseID != duplicate.PhaseID {
		t.Fatalf("phase settings targeted %q, want exact raw id %q", got.PhaseID, duplicate.PhaseID)
	}
	if selected := m.SelectedFlowPhaseID(); selected != duplicate.PhaseID {
		t.Fatalf("selected phase after settle = %q, want %q", selected, duplicate.PhaseID)
	}
	items := m.Flows()
	if len(items) != 1 || items[0].Phases[0].Agent != agent.CommandCodex || items[0].Phases[1].Agent != agent.CommandCodex {
		t.Fatalf("exact duplicate cache update = %#v", items)
	}
}

func TestModel_PhaseAgentPickerRejectsSettleAfterSwitchingExactDuplicate(t *testing.T) {
	flow := manualMergeEligibleFlow()
	canonical := flow.Phases[0]
	canonical.PhaseID = "Implementation"
	canonical.Agent = agent.CommandCodex
	duplicate := canonical
	duplicate.PhaseID = " Implementation "
	duplicate.Agent = agent.CommandClaude
	flow.Phases = []flowstore.FlowPhase{canonical, duplicate}

	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		SetFlowPhaseAgentSettings: func(update flowstore.PhaseAgentSettingsUpdate) (flowstore.FlowRecord, error) {
			updated := flow
			updated.Phases = append([]flowstore.FlowPhase(nil), flow.Phases...)
			for i := range updated.Phases {
				if updated.Phases[i].PhaseID == update.PhaseID {
					updated.Phases[i].Agent = update.Settings.Agent
				}
			}
			updated.UpdatedAt = time.Now()
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, duplicate.PhaseID)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp}) // codex
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("phase agent selection returned no persistence command")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.SelectedFlowPhaseID(); got != canonical.PhaseID {
		t.Fatalf("selected phase before settle = %q, want %q", got, canonical.PhaseID)
	}
	m, _ = update(m, cmd())
	if view := m.View(); !strings.Contains(view, "selection changed before agent settings were applied") {
		t.Fatalf("stale exact-duplicate settle was accepted:\n%s", view)
	}
	items := m.Flows()
	if len(items) != 1 || items[0].Phases[1].Agent != agent.CommandClaude {
		t.Fatalf("stale settle changed cached duplicate: %#v", items)
	}
}

func TestModel_MalformedPhaseAgentStampLabelsAreExplicitlyInvalid(t *testing.T) {
	flow := manualMergeEligibleFlow()
	flow.Phases = []flowstore.FlowPhase{{
		PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady,
		Model: agent.ModelGPT55, // Invalid raw model-only stamp must not look like global fallback.
	}}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:         agent.CommandCodex,
		CodexModel:           agent.ModelGPT55,
		CodexReasoningEffort: agent.ReasoningEffortHigh,
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")
	view := m.View()
	for _, want := range []string{"A      invalid settings", "M      invalid", "E      effort: invalid"} {
		if !strings.Contains(view, want) {
			t.Fatalf("malformed phase stamp did not render %q:\n%s", want, view)
		}
	}
}

func TestModel_MKeyManualMergeNoopsOnSelectedPhaseRow(t *testing.T) {
	flow := manualMergeEligibleFlow()
	called := false
	m := newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			called = true
			return actions.PullRequestMerge{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "merge")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatalf("m on selected phase returned command %T, want nil", cmd)
	}
	if m.Overlay() == ui.OverlayConfirm {
		t.Fatalf("m on selected phase opened confirmation %q", m.ConfirmPrompt())
	}
	if called {
		t.Fatal("LookupPRMerge should not run for selected phase row")
	}
}

func TestModel_MKeyManualMergeNoopsOnActiveFlowPhaseRow(t *testing.T) {
	flow := manualMergeEligibleFlow()
	called := false
	m := newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			called = true
			return actions.PullRequestMerge{}, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 50 && model.SelectedActiveFlowPhaseIDForTest(m) != "merge"; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "merge" {
		t.Fatalf("selected active Flow phase = %q, want merge", got)
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatalf("m on selected active Flow phase returned command %T, want nil", cmd)
	}
	if m.Overlay() == ui.OverlayConfirm {
		t.Fatalf("m on selected active Flow phase opened confirmation %q", m.ConfirmPrompt())
	}
	if called {
		t.Fatal("LookupPRMerge should not run for selected active Flow phase row")
	}
}

func TestModel_MKeyManualMergeNoopsWhenMergePhaseRunning(t *testing.T) {
	flow := manualMergeEligibleFlow()
	flow.Phases[len(flow.Phases)-1].Status = flowstore.PhaseRunning
	called := false
	m := newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			called = true
			return actions.PullRequestMerge{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatalf("m on running merge phase returned command %T, want nil", cmd)
	}
	if m.Overlay() == ui.OverlayConfirm {
		t.Fatalf("m on running merge phase opened confirmation %q", m.ConfirmPrompt())
	}
	if called {
		t.Fatal("LookupPRMerge should not run when merge phase is running")
	}
}

func TestModel_MKeyManualMergeFailureReplacesRecoveredFlowState(t *testing.T) {
	flow := manualMergeEligibleFlow()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	recovered := flow
	recovered.Phases = append([]flowstore.FlowPhase(nil), flow.Phases...)
	recovered.Phases[len(recovered.Phases)-1].Status = flowstore.PhaseNeedsAttention
	recovered.Phases[len(recovered.Phases)-1].Notes = "sync linked plan phase: missing plan"
	recovered.Merge = flowstore.Merge{Status: flowstore.MergePending}
	m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			return actions.PullRequestMerge{Commit: "0123456789abcdef", MergedAt: mergedAt}, nil
		},
		MarkFlowManualMerge: func(flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error) {
			return recovered, errors.New("sync linked plan phase: missing plan")
		},
	}), []flowstore.FlowRecord{flow})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming manual merge should return command")
	}
	for _, refreshMsg := range runLeadingBatchCmd(t, cmd) {
		m, _ = update(m, refreshMsg)
	}

	got := m.Flows()
	if len(got) != 1 {
		t.Fatalf("Flows() length = %d, want 1", len(got))
	}
	mergePhase := phaseByID(got[0], "merge")
	if mergePhase.Status != flowstore.PhaseNeedsAttention || !strings.Contains(mergePhase.Notes, "missing plan") {
		t.Fatalf("manual merge failure did not replace recovered flow state: %#v", got[0])
	}
	if strings.Contains(m.View(), "m      mark merged") {
		t.Fatalf("recovered needs_attention Flow should not advertise manual merge:\n%s", m.View())
	}
}

func TestModel_MKeyManualMergeRemovesMergedRecordFromActiveFlows(t *testing.T) {
	flow := manualMergeEligibleFlow()
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	merged := flow
	merged.Status = flowstore.StatusMerged
	merged.PR.Status = flowstore.MergeMerged
	merged.Merge = flowstore.Merge{Status: flowstore.MergeMerged, Commit: "0123456789abcdef", MergedAt: &mergedAt}
	m := enterActiveFlowsWithRecords(t, newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			return actions.PullRequestMerge{Commit: "0123456789abcdef", MergedAt: mergedAt}, nil
		},
		MarkFlowManualMerge: func(flowstore.ManualMergeUpdate) (flowstore.FlowRecord, error) {
			return merged, nil
		},
	}), []flowstore.FlowRecord{flow})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming manual merge should return command")
	}
	m, _ = update(m, cmd())

	if strings.Contains(m.View(), "Manual merge Flow") {
		t.Fatalf("merged Flow should be filtered from active view:\n%s", m.View())
	}
}

func TestModel_ShiftMManualMergeNoopsOnActiveFlowRow(t *testing.T) {
	flow := manualMergeEligibleFlow()
	called := false
	m := newTestModel(testRepos(), model.Options{
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			called = true
			return actions.PullRequestMerge{}, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd == nil {
		t.Fatal("expected status expiry command on selected active Flow row")
	}
	if m.Overlay() == ui.OverlayConfirm {
		t.Fatalf("M on selected active Flow row opened confirmation %q", m.ConfirmPrompt())
	}
	if called {
		t.Fatal("LookupPRMerge should not run for uppercase M on selected active Flow row")
	}
}

func TestModel_ShiftMOpensModelPickerOnSelectedActiveFlowPhaseRow(t *testing.T) {
	flow := manualMergeEligibleFlow()
	m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 50 && model.SelectedActiveFlowPhaseIDForTest(m) != "merge"; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "merge" {
		t.Fatalf("selected active Flow phase = %q, want merge", got)
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd != nil {
		t.Fatalf("M on selected active Flow phase returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("M on selected active Flow phase overlay = %d, want model select", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "Choose model for phase merge") || !strings.Contains(view, "inherit global") {
		t.Fatalf("M on selected active Flow phase did not open model picker:\n%s", view)
	}
}

func TestModel_SelectedAwaitingFlowPhaseAdvertisesResetShortcut(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithAwaitingImplementation()})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := m.View()
	if !strings.Contains(view, "x      reset ready") {
		t.Fatalf("await-session Flow phase should expose reset shortcut:\n%s", m.View())
	}
	if strings.Contains(view, "r      resume") {
		t.Fatalf("await-session Flow phase should not expose resume shortcut:\n%s", m.View())
	}
}

func TestModel_SelectedEndedSessionFlowPhaseAdvertisesResetShortcut(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithEndedRunningImplementation()})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "implementation:ended-session") {
		t.Fatalf("ended-session Flow phase should render recovery label:\n%s", view)
	}
	if !strings.Contains(view, "x      reset ready") {
		t.Fatalf("ended-session Flow phase should expose reset shortcut:\n%s", view)
	}
	if strings.Contains(view, "r      resume") {
		t.Fatalf("running ended-session Flow phase should not expose resume shortcut:\n%s", view)
	}
}

func TestModel_SelectedSessionMismatchFlowPhaseHidesResetShortcut(t *testing.T) {
	flow := flowWithAwaitingImplementation()
	flow.Phases[2].LaunchIDs = []string{"launch-orphan"}
	flow.Phases[2].Sessions = []flowstore.Session{
		{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"},
	}
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
		ReadFlow:     func(string) (flowstore.FlowRecord, error) { return flow, nil },
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) { return nil, nil },
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "implementation:session-mismatch") {
		t.Fatalf("mismatched selected Flow phase should render session-mismatch:\n%s", view)
	}
	if strings.Contains(view, "reset ready") {
		t.Fatalf("session-mismatch Flow phase should hide reset shortcut:\n%s", view)
	}
	if strings.Contains(view, "release session") {
		t.Fatalf("session-mismatch Flow phase has no live session to release:\n%s", view)
	}
	// The phase carries a launch ID, so x is no longer silent: it probes, and
	// the probe reports what it found rather than leaving the press unanswered.
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("x on a phase carrying launch IDs should probe for releasable sessions")
	}
	m, _ = update(m, cmd())
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("session-mismatch probe opened overlay=%d, want none", m.Overlay())
	}
	if got := m.TransientError(); !strings.Contains(got, "no unfinished session to release") {
		t.Fatalf("status after probe = %q, want the named refusal", got)
	}
	if resetCalled {
		t.Fatal("reset should not be called for session-mismatch phase")
	}
}

// The advertised case: a live session on a ready phase contradicts the phase's
// own status — nothing has started — so the footer names the release. Every
// other status can legitimately hold one, which is why the hint stops there.
// Reset never applies to a ready phase either — it requires a running one — so
// the two x labels cannot both show.
func TestModel_StalledFlowPhaseAdvertisesSessionRelease(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.Phases[2].LaunchIDs = []string{"launch-crashed"}
	flow.Phases[2].Sessions = []flowstore.Session{
		{Provider: "codex", SessionID: "codex-crashed", LaunchID: "launch-crashed", Status: "last_seen"},
	}
	m := newTestModel(testRepos(), model.Options{})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "release session") {
		t.Fatalf("stalled Flow phase should advertise session release:\n%s", view)
	}
	if strings.Contains(view, "reset ready") {
		t.Fatalf("release and reset hints must not both show:\n%s", view)
	}
}

// A running phase whose session never ended is the other stall variant. The
// footer stays quiet — on a running phase a live session is normal, and nothing
// cheap tells a working agent from a crashed one — but x still probes, so the
// recovery is reachable on demand.
func TestModel_SelectedLiveSessionFlowPhaseOffersSessionRelease(t *testing.T) {
	flow := flowWithEndedRunningImplementation()
	flow.Phases[2].Sessions[0].Status = "running"
	resetCalled := false
	var finalized []actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
		ReadFlow:     func(string) (flowstore.FlowRecord, error) { return flow, nil },
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) { return nil, nil },
		FinalizeAgentSession: func(ctx actions.AgentLaunchContext) error {
			finalized = append(finalized, ctx)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := ansi.Strip(m.View())
	if strings.Contains(view, "reset ready") {
		t.Fatalf("live attached Flow phase should hide reset shortcut:\n%s", view)
	}
	if strings.Contains(view, "release session") {
		t.Fatalf("running Flow phase should not advertise session release:\n%s", view)
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("x on a live-session phase should probe for releasable sessions")
	}
	m, _ = update(m, cmd())
	if m.Overlay() != ui.OverlayConfirm || !strings.Contains(m.ConfirmPrompt(), "unverified session") {
		t.Fatalf("release confirmation prompt = %q overlay=%d", m.ConfirmPrompt(), m.Overlay())
	}
	if len(finalized) != 0 {
		t.Fatalf("release finalized before confirmation: %#v", finalized)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("accepting the release confirmation should return a command")
	}
	m, releaseCmd := update(m, cmd())
	if releaseCmd == nil {
		t.Fatal("confirmed release should return a finalization command")
	}
	m, _ = update(m, releaseCmd())
	if len(finalized) != 1 || finalized[0].LaunchID != "launch-ended" {
		t.Fatalf("finalized = %#v, want the phase's unfinished launch", finalized)
	}
	if got := m.TransientError(); !strings.Contains(got, "Released 1") {
		t.Fatalf("status after release = %q, want the released count", got)
	}
	if resetCalled {
		t.Fatal("reset should not be called for live attached phase")
	}
}

func TestModel_XKeyOnResettableFlowPhaseConfirmsAndResets(t *testing.T) {
	awaiting := flowWithAwaitingImplementation()
	reset := flowWithPhaseDetails()
	var resetCalls []flowstore.PhaseResetUpdate
	listCalls := 0
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(update flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalls = append(resetCalls, update)
			return reset, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return []flowstore.FlowRecord{reset}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{awaiting})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("opening reset confirmation returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm || !strings.Contains(m.ConfirmPrompt(), "Reset Flow phase implementation") {
		t.Fatalf("reset confirmation prompt = %q overlay=%d", m.ConfirmPrompt(), m.Overlay())
	}
	if len(resetCalls) != 0 {
		t.Fatalf("reset called before confirmation: %#v", resetCalls)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("accepting reset confirmation should return reset command")
	}
	m, resetCmd := update(m, cmd())
	if resetCmd == nil {
		t.Fatal("confirmed reset should return persistence command")
	}
	m, fetchCmd := update(m, resetCmd())
	if len(resetCalls) != 1 || resetCalls[0].FlowID != "flow-1" || resetCalls[0].PhaseID != "implementation" {
		t.Fatalf("reset calls = %#v", resetCalls)
	}
	if got := m.TransientError(); !strings.Contains(got, "Reset Flow phase implementation to ready") {
		t.Fatalf("status after reset = %q, want success", got)
	}
	if fetchCmd == nil {
		t.Fatal("successful reset should refresh Flow rows")
	}
	m, _ = update(m, flowResultFromCommand(t, fetchCmd))
	if listCalls == 0 {
		t.Fatal("ListFlows should run during reset refresh")
	}
	if got := m.SelectedFlowPhaseID(); got != "implementation" {
		t.Fatalf("selected phase after reset refresh = %q, want implementation", got)
	}
	if phase := m.Flows()[0].Phases[2]; phase.Status != flowstore.PhaseReady {
		t.Fatalf("implementation after reset refresh = %#v, want ready", phase)
	}
}

func TestModel_XKeyOnEndedSessionFlowPhaseConfirmsAndResets(t *testing.T) {
	ended := flowWithEndedRunningImplementation()
	reset := flowWithPhaseDetails()
	var resetCalls []flowstore.PhaseResetUpdate
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(update flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalls = append(resetCalls, update)
			return reset, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{reset}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{ended})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("opening reset confirmation returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("reset overlay = %d, want confirm", m.Overlay())
	}
	if len(resetCalls) != 0 {
		t.Fatalf("reset called before confirmation: %#v", resetCalls)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("accepting reset confirmation should return reset command")
	}
	m, resetCmd := update(m, cmd())
	if resetCmd == nil {
		t.Fatal("confirmed reset should return persistence command")
	}
	m, _ = update(m, resetCmd())
	if len(resetCalls) != 1 || resetCalls[0].FlowID != "flow-1" || resetCalls[0].PhaseID != "implementation" {
		t.Fatalf("reset calls = %#v", resetCalls)
	}
}

func TestModel_ResetFlowPhaseRefreshKeepsSelectionAfterPhaseIDNormalization(t *testing.T) {
	awaiting := flowWithAwaitingImplementation()
	awaiting.FlowID = "flow-legacy"
	awaiting.Phases[2].PhaseID = "Implementation"
	reset := awaiting
	reset.Phases = append([]flowstore.FlowPhase(nil), awaiting.Phases...)
	reset.Phases[2].PhaseID = "implementation"
	reset.Phases[2].Status = flowstore.PhaseReady
	reset.Phases[2].LaunchIDs = nil
	var resetCalls []flowstore.PhaseResetUpdate
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(update flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalls = append(resetCalls, update)
			return reset, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{reset}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{awaiting})
	m = selectFlowPhaseByID(t, m, "Implementation")

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("accepting reset confirmation should return reset command")
	}
	m, resetCmd := update(m, cmd())
	if resetCmd == nil {
		t.Fatal("confirmed reset should return persistence command")
	}
	m, fetchCmd := update(m, resetCmd())
	if len(resetCalls) != 1 || resetCalls[0].PhaseID != "Implementation" {
		t.Fatalf("reset calls = %#v", resetCalls)
	}
	if fetchCmd == nil {
		t.Fatal("successful reset should refresh Flow rows")
	}
	m, _ = update(m, flowResultFromCommand(t, fetchCmd))

	if got := m.ExpandedFlowID(); got != "flow-legacy" {
		t.Fatalf("expanded flow after reset refresh = %q, want flow-legacy", got)
	}
	if got := m.SelectedFlowPhaseID(); got != "implementation" {
		t.Fatalf("selected phase after reset refresh = %q, want normalized implementation", got)
	}
}

func TestModel_XKeyOnResettableFlowPhaseCancelDoesNotPersist(t *testing.T) {
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithAwaitingImplementation()})
	m = selectFlowPhaseByID(t, m, "implementation")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatalf("canceling reset confirmation returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("overlay after cancel = %d, want none", m.Overlay())
	}
	if resetCalled {
		t.Fatal("reset should not be called after cancellation")
	}
}

func TestModel_XKeyOnResettableFlowPhaseReportsResetFailure(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			t.Fatal("ListFlows should not run after reset failure")
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithAwaitingImplementation()})
	m = selectFlowPhaseByID(t, m, "implementation")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("accepting reset confirmation should return reset command")
	}
	m, resetCmd := update(m, cmd())
	if resetCmd == nil {
		t.Fatal("confirmed reset should return persistence command")
	}
	m, fetchCmd := update(m, resetCmd())
	if fetchCmd == nil {
		t.Fatal("expected status expiry command for reset failure")
	}
	if got := m.TransientError(); !strings.Contains(got, "state root locked") {
		t.Fatalf("status after reset failure = %q, want persistence error", got)
	}
}

func TestModel_XKeyIgnoredWhenSelectedFlowPhaseIsNotAwaitingSession(t *testing.T) {
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m = selectFlowPhaseByID(t, m, "implementation")
	if strings.Contains(m.View(), "reset ready") {
		t.Fatalf("ready Flow phase should hide reset shortcut:\n%s", m.View())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || m.Overlay() != ui.OverlayNone {
		t.Fatalf("x on non-awaiting phase returned cmd=%T overlay=%d", cmd, m.Overlay())
	}
	if resetCalled {
		t.Fatal("reset should not be called for non-awaiting phase")
	}
}

func TestModel_ResetShortcutHiddenWhenMatchingFlowTerminalIsRunning(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g on ready Flow should prepare embedded terminal launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{flowWithAwaitingImplementation()}, ListRequest: m.ListRequest(ui.ModeFlows)})
	m = selectFlowPhaseByID(t, m, "implementation")

	if strings.Contains(m.View(), "reset ready") {
		t.Fatalf("matching running Flow terminal should hide reset shortcut:\n%s", m.View())
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || m.Overlay() != ui.OverlayNone {
		t.Fatalf("x with matching running Flow terminal returned cmd=%T overlay=%d", cmd, m.Overlay())
	}
	if resetCalled {
		t.Fatal("reset should not be called while matching Flow terminal is running")
	}
}

func TestModel_EndedSessionResetShortcutHiddenWhenMatchingFlowTerminalIsRunning(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g on ready Flow should prepare embedded terminal launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{flowWithEndedRunningImplementation()}, ListRequest: m.ListRequest(ui.ModeFlows)})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := ansi.Strip(m.View())
	if strings.Contains(view, "reset ready") {
		t.Fatalf("matching running Flow terminal should hide ended-session reset shortcut:\n%s", view)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || m.Overlay() != ui.OverlayNone {
		t.Fatalf("x with matching running Flow terminal returned cmd=%T overlay=%d", cmd, m.Overlay())
	}
	if resetCalled {
		t.Fatal("reset should not be called while matching Flow terminal is running")
	}
}

func TestModel_ResetShortcutHiddenAfterLegacyPhaseIDLaunchNormalizes(t *testing.T) {
	legacy := flowWithPhaseDetails()
	legacy.Phases[2].PhaseID = "Implementation"
	awaiting := flowWithAwaitingImplementation()
	var started actions.AgentLaunchContext
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			if update.PhaseID != "Implementation" {
				t.Fatalf("launch update phase id = %q, want legacy Implementation", update.PhaseID)
			}
			return awaiting, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{legacy})
	m = selectFlowPhaseByID(t, m, "Implementation")

	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	if started.FlowPhaseID != "implementation" {
		t.Fatalf("embedded launch phase id = %q, want canonical implementation", started.FlowPhaseID)
	}
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{awaiting}, ListRequest: m.ListRequest(ui.ModeFlows)})
	if got := m.SelectedFlowPhaseID(); got != "implementation" {
		t.Fatalf("selected phase after normalized refresh = %q, want implementation", got)
	}

	view := ansi.Strip(m.View())
	if strings.Contains(view, "reset ready") {
		t.Fatalf("running normalized Flow terminal should hide reset shortcut:\n%s", view)
	}
	if !strings.Contains(view, "   >● running") {
		t.Fatalf("normalized Flow terminal should mark selected phase active:\n%s", view)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || m.Overlay() != ui.OverlayNone {
		t.Fatalf("x with normalized running Flow terminal returned cmd=%T overlay=%d", cmd, m.Overlay())
	}
	if resetCalled {
		t.Fatal("reset should not be called while normalized Flow terminal is running")
	}
}

func TestModel_XKeyKeepsFlowTerminalFocusCloseBehavior(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	resetCalled := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g on ready Flow should prepare embedded terminal launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("terminal x close prompt returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm || m.ConfirmPrompt() != "Terminate embedded terminal?" {
		t.Fatalf("terminal x prompt = %q overlay=%d", m.ConfirmPrompt(), m.Overlay())
	}
	if resetCalled {
		t.Fatal("terminal-focused x should not call flow phase reset")
	}
}

func TestModel_Key8SwitchesToFlowsAndFetches(t *testing.T) {
	var gotFilter flowstore.FlowFilter
	want := []flowstore.FlowRecord{
		{FlowID: "flow-1", Title: "Add Flow mode", RepoPath: "/dev/alpha", Branch: "flow/add-flow-mode", Status: flowstore.StatusPending},
	}
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModePlans)

	m, cmd := switchTestMode(m, ui.ModeFlows)
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want flows", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected flows fetch command")
	}
	if gotFilter.RepoPath != "" {
		t.Fatalf("flow lister ran before command execution: %#v", gotFilter)
	}
	msg := flowResultFromCommand(t, cmd)
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	got := m.Flows()
	if len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("Flows() = %#v, want %#v", got, want)
	}
}

func TestModel_FlowFetchUsesSelectedRepoRequestAndIgnoresStaleResults(t *testing.T) {
	var gotFilter flowstore.FlowFilter
	want := []flowstore.FlowRecord{
		{FlowID: "flow-current", Title: "Current Flow", RepoPath: "/dev/alpha", Status: flowstore.StatusPending},
	}
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModePlans)

	m, cmd := switchTestMode(m, ui.ModeFlows)
	if cmd == nil {
		t.Fatal("expected flows fetch command")
	}
	request := m.ListRequest(ui.ModeFlows)
	firstCmd := cmd

	m, _ = switchTestMode(m, ui.ModePlans)
	m, cmd = switchTestMode(m, ui.ModeFlows)
	if cmd == nil {
		t.Fatal("expected second flows fetch command")
	}
	nextRequest := m.ListRequest(ui.ModeFlows)
	if nextRequest == request {
		t.Fatalf("second flow request = %d, want a new request", nextRequest)
	}

	msg := flowResultFromCommand(t, firstCmd)
	if msg.ListRequest != request {
		t.Fatalf("FlowResultMsg.ListRequest = %d, want original request %d", msg.ListRequest, request)
	}
	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("FlowFilter.RepoPath = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	m, _ = update(m, msg)
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("stale FlowResultMsg populated flows: %#v", got)
	}

	nextMsg := flowResultFromCommand(t, cmd)
	if nextMsg.ListRequest != nextRequest {
		t.Fatalf("second FlowResultMsg.ListRequest = %d, want current request %d", nextMsg.ListRequest, nextRequest)
	}
	m, _ = update(m, nextMsg)
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-current" {
		t.Fatalf("Flows() = %#v, want current flow", got)
	}
}

func TestModel_FlowAutoModeLaunchesImplementationAfterPlanReviewCompletes(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
		"review-loop":    flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
		"review-loop":    flowstore.PhasePending,
	})
	current.Phases[1].Outcome = flowstore.OutcomeApproved
	launchMsg, updates := autoLaunchFromFlowRefresh(t, previous, current)

	if len(updates) != 1 || updates[0].FlowID != "flow-1" || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates = %#v, want implementation", updates)
	}
	if launchMsg.LaunchContext.FlowID != "flow-1" ||
		launchMsg.LaunchContext.FlowPhaseID != "implementation" ||
		!launchMsg.LaunchContext.FlowLaunchTracked ||
		!launchMsg.LaunchContext.Embedded {
		t.Fatalf("launch context = %#v, want embedded implementation launch", launchMsg.LaunchContext)
	}
}

// A tracked resume is registered synchronously on the key press, before its
// persistence lands, so AutoMode now defers rather than racing a write that is
// still in flight. The deferral lasts exactly as long as the resume owns the
// Flow: the drain stays armed, and the next free poll launches.
func TestModel_PendingTrackedResumeDefersFlowAutoAdvanceUntilItClears(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	previous.Phases[1].LaunchIDs = []string{"launch-source"}
	current.Phases[1].LaunchIDs = []string{"launch-source"}
	current.Phases[1].Sessions = []flowstore.Session{{
		Provider: "codex", SessionID: "codex-source", LaunchID: "launch-source", Status: "ended",
	}}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			persisted := current
			persisted.Phases = slices.Clone(current.Phases)
			for i := range persisted.Phases {
				if persisted.Phases[i].PhaseID == update.PhaseID {
					persisted.Phases[i].Status = flowstore.PhaseRunning
					persisted.Phases[i].LaunchIDs = append(slices.Clone(persisted.Phases[i].LaunchIDs), update.LaunchID)
				}
			}
			return persisted, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{current})
	m = selectFlowPhaseByID(t, m, "plan-review")
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	pending, manualCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if manualCmd == nil {
		t.Fatal("manual tracked resume should return a persistence command")
	}

	deferred, autoCmd := autoAdvanceLaunchCommand(pending, []flowstore.FlowRecord{current})
	if autoCmd != nil {
		t.Fatalf("auto poll raced a resume that is still mid-write: %T", autoCmd)
	}
	if len(updates) != 0 {
		t.Fatalf("deferred auto poll persisted %#v, want nothing", updates)
	}
	if got := deferred.TransientError(); got != "" {
		t.Fatalf("deferred auto poll set status %q, want a silent refusal", got)
	}

	// Once the resume has persisted and its terminal is gone, nothing owns the
	// Flow and the still-armed drain launches on the next poll.
	settled := settleModelCommands(t, deferred, manualCmd, 4)
	settled = model.ClearFlowEmbeddedTerminalsForTest(settled)
	updates = nil
	settled, autoCmd = autoAdvanceLaunchCommand(settled, []flowstore.FlowRecord{current})
	launches := flowEmbeddedLaunchesFromCommand(t, settled, autoCmd)
	if len(launches) != 1 || launches[0].LaunchContext.FlowPhaseID != "implementation" || !launches[0].LaunchContext.Headless {
		t.Fatalf("auto launch after the resume cleared = %#v, want headless implementation", launches)
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("persistence updates = %#v, want only the auto successor", updates)
	}
}

// The two sources can no longer overlap: a tracked resume owns the Flow from
// the key press until its terminal is gone, and AutoMode defers for exactly
// that long. The invariant the ordering permutations used to protect — each
// terminal starts exactly once, against one real store — is what is kept here.
func TestModel_TrackedResumeAndAutoAdvanceStartEachTerminalOnce(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	current, err := store.Create(flowstore.FlowRecord{
		FlowID:       "flow-race",
		Title:        "Manual resume and auto launch race",
		Instructions: "exercise both persistence and continuation orderings",
		RepoPath:     "/dev/alpha",
		WorktreePath: filepath.Join(root, "worktree"),
		Branch:       "flow/race",
		Phases: []flowstore.FlowPhase{
			{
				PhaseID:   "plan-review",
				Title:     "Plan Review",
				Kind:      flowstore.KindPlanReview,
				DependsOn: []string{},
				Status:    flowstore.PhaseCompleted,
				Outcome:   flowstore.OutcomeApproved,
				Order:     1,
				LaunchIDs: []string{"launch-source"},
				Sessions: []flowstore.Session{{
					Provider: "codex", SessionID: "codex-source", LaunchID: "launch-source", Status: "ended",
				}},
			},
			{
				PhaseID:   "implementation",
				Title:     "Implementation",
				Kind:      flowstore.KindImplementation,
				DependsOn: []string{"plan-review"},
				Status:    flowstore.PhaseReady,
				Order:     2,
			},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	previous := current
	previous.Phases = slices.Clone(current.Phases)
	previous.Phases[0].Status = flowstore.PhaseRunning
	previous.Phases[1].Status = flowstore.PhasePending

	var terminalStarts []actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:         "codex",
		SessionStateRoot:     root,
		ReadFlow:             store.Read,
		AddFlowPhaseLaunchID: store.AddPhaseLaunchID,
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			terminalStarts = append(terminalStarts, ctx)
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{current})
	m = selectFlowPhaseByID(t, m, "plan-review")
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	pending, manualCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if manualCmd == nil {
		t.Fatal("manual resume should return a persistence command")
	}

	// The completion edge arms the drain, and the in-flight resume refuses this
	// poll silently rather than racing the write.
	scheduled, autoCmd := autoAdvanceLaunchCommand(pending, []flowstore.FlowRecord{current})
	if autoCmd != nil {
		t.Fatalf("auto poll ran while the resume was mid-write: %T", autoCmd)
	}

	manualResult := manualCmd()
	scheduled, manualFollowUp := update(scheduled, manualResult)
	scheduled = settleModelCommands(t, scheduled, manualFollowUp, 4)
	if len(terminalStarts) != 1 || terminalStarts[0].FlowPhaseID != "plan-review" {
		t.Fatalf("terminal starts after the resume = %#v, want one plan-review resume", terminalStarts)
	}

	scheduled = model.ClearFlowEmbeddedTerminalsForTest(scheduled)
	scheduled, autoCmd = autoAdvanceLaunchCommand(scheduled, []flowstore.FlowRecord{current})
	if autoCmd == nil {
		t.Fatal("the still-armed drain should launch once the resume releases the Flow")
	}
	scheduled, prepared := model.AutoFlowLaunchesForTest(scheduled, autoCmd)
	if len(prepared) != 1 {
		t.Fatalf("prepared auto launches = %#v, want exactly one", prepared)
	}
	scheduled, autoFollowUp := update(scheduled, prepared[0].Prepared)
	scheduled = settleModelCommands(t, scheduled, autoFollowUp, 4)

	if len(terminalStarts) != 2 {
		t.Fatalf("terminal starts = %#v, want exactly 2", terminalStarts)
	}
	var manualLaunch, autoLaunch actions.AgentLaunchContext
	for _, started := range terminalStarts {
		switch started.FlowPhaseID {
		case "plan-review":
			manualLaunch = started
		case "implementation":
			autoLaunch = started
		}
	}
	if manualLaunch.LaunchID == "" || manualLaunch.ResumeSessionID != "codex-source" || manualLaunch.Headless {
		t.Fatalf("manual terminal = %#v", manualLaunch)
	}
	if autoLaunch.LaunchID == "" || !autoLaunch.Headless || autoLaunch.ResumeSessionID != "" || autoLaunch.LaunchID == manualLaunch.LaunchID {
		t.Fatalf("auto terminal = %#v, manual = %#v", autoLaunch, manualLaunch)
	}
	persisted, err := store.Read(current.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	phaseByID := func(phaseID string) flowstore.FlowPhase {
		for _, phase := range persisted.Phases {
			if phase.PhaseID == phaseID {
				return phase
			}
		}
		t.Fatalf("persisted Flow missing phase %q: %#v", phaseID, persisted.Phases)
		return flowstore.FlowPhase{}
	}
	source := phaseByID("plan-review")
	successor := phaseByID("implementation")
	if len(source.LaunchIDs) != 2 || source.LaunchIDs[1] != manualLaunch.LaunchID {
		t.Fatalf("source launch ids = %#v, want one resume", source.LaunchIDs)
	}
	if successor.Status != flowstore.PhaseRunning || len(successor.LaunchIDs) != 1 || successor.LaunchIDs[0] != autoLaunch.LaunchID {
		t.Fatalf("successor = %#v, want one running auto launch", successor)
	}

	statusBeforeReplay := scheduled.TransientError()
	scheduled, replayCmd := update(scheduled, manualResult)
	if replayCmd != nil || len(terminalStarts) != 2 || scheduled.TransientError() != statusBeforeReplay {
		t.Fatalf("manual result replay changed state: cmd=%T terminals=%#v status=%q", replayCmd, terminalStarts, scheduled.TransientError())
	}
}

func TestModel_TrackedResumeTerminalDefersLaterAutoAdvancePollUntilExit(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	previous.Phases[1].LaunchIDs = []string{"launch-source"}
	current.Phases[1].LaunchIDs = []string{"launch-source"}
	current.Phases[1].Sessions = []flowstore.Session{{
		Provider: "codex", SessionID: "codex-source", LaunchID: "launch-source", Status: "ended",
	}}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			persisted := current
			for i := range persisted.Phases {
				if persisted.Phases[i].PhaseID == update.PhaseID {
					persisted.Phases[i].LaunchIDs = append(persisted.Phases[i].LaunchIDs, update.LaunchID)
					if !update.Resume {
						persisted.Phases[i].Status = flowstore.PhaseRunning
					}
				}
			}
			return persisted, nil
		},
		StartEmbeddedTerminal: func(_ actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{current})
	m = selectFlowPhaseByID(t, m, "plan-review")
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	pending, manualCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if manualCmd == nil {
		t.Fatal("manual tracked resume should return the authoritative read command")
	}
	resumed := settleModelCommands(t, pending, manualCmd, 2)
	if !model.HasRunningFlowEmbeddedTerminalForPhaseForTest(resumed, current.FlowID, "plan-review") {
		t.Fatal("manual persistence success did not register the resumed source terminal")
	}
	if len(updates) != 1 || !updates[0].Resume {
		t.Fatalf("updates after manual resume = %#v", updates)
	}

	deferred, autoCmd := autoAdvanceLaunchCommand(resumed, []flowstore.FlowRecord{current})
	if autoCmd != nil || len(updates) != 1 {
		t.Fatalf("auto poll while resumed terminal runs: cmd=%T updates=%#v", autoCmd, updates)
	}
	deferred = model.ClearFlowEmbeddedTerminalsForTest(deferred)
	deferred, autoCmd = autoAdvanceLaunchCommand(deferred, []flowstore.FlowRecord{current})
	launches := flowEmbeddedLaunchesFromCommand(t, deferred, autoCmd)
	if len(launches) != 1 || launches[0].LaunchContext.FlowPhaseID != "implementation" || !launches[0].LaunchContext.Headless {
		t.Fatalf("auto launch after resumed terminal exits = %#v", launches)
	}
	if len(updates) != 2 || !updates[1].AutoLaunch || updates[1].PhaseID != "implementation" {
		t.Fatalf("updates after terminal exit = %#v", updates)
	}
}

func TestModel_FlowAutoModeDefersLaunchWhileCompletedPhaseTerminalRuns(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
		"review-loop":    flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
		"review-loop":    flowstore.PhasePending,
	})
	current.Phases[1].Outcome = flowstore.OutcomeApproved
	var updates []flowstore.PhaseLaunchUpdate
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"source output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       "flow-1",
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
	}
	if !model.HasRunningFlowEmbeddedTerminalForPhaseForTest(m, "flow-1", "plan-review") {
		t.Fatal("test setup should attach a running Flow terminal to plan-review")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd != nil {
		t.Fatalf("completed phase with running terminal returned auto-launch command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none while source terminal runs", updates)
	}

	sourceTerm.state = "exited"
	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd != nil {
		t.Fatalf("advance before exited terminal auto-closes returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates before exited terminal auto-closes = %#v, want none", updates)
	}

	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd == nil {
		t.Fatal("exiting the deferred source terminal should schedule a refresh")
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates before exit-triggered refresh = %#v, want none", updates)
	}
	if strings.Contains(m.View(), "source output") || model.HasRunningFlowEmbeddedTerminalForPhaseForTest(m, "flow-1", "plan-review") {
		t.Fatalf("source terminal should be dismissed after exited tick:\n%s", m.View())
	}

	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("advance poll after source terminal exit should prepare the deferred auto launch")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("deferred launch command returned %d embedded launches, want 1", len(launches))
	}
	launchMsg := launches[0]
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].FlowID != "flow-1" || updates[0].PhaseID != "implementation" || updates[0].LaunchID == "" {
		t.Fatalf("launch updates after source terminal exit = %#v, want implementation auto launch", updates)
	}
	if launchMsg.LaunchContext.FlowID != "flow-1" ||
		launchMsg.LaunchContext.FlowPhaseID != "implementation" ||
		!launchMsg.LaunchContext.Embedded ||
		!launchMsg.LaunchContext.Headless ||
		!launchMsg.LaunchContext.FlowLaunchTracked {
		t.Fatalf("deferred launch context = %#v", launchMsg.LaunchContext)
	}
}

func TestModel_FlowAutoModeRefreshesOnSourceTerminalExitBeforeCompletionObserved(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	current.Phases[1].Outcome = flowstore.OutcomeApproved
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"source output"}, state: "running"}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       "flow-1",
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	sourceTerm.state = "exited"
	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd == nil {
		t.Fatal("exiting a Flow terminal should schedule a Flow refresh")
	}
	refresh := flowResultFromCommand(t, cmd)

	m, cmd = update(m, refresh)
	if len(updates) != 0 {
		t.Fatalf("exit-triggered display refresh launch updates = %#v, want none", updates)
	}

	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("advance poll should prepare auto launch after observing completion")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("advance poll returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates = %#v, want implementation auto launch", updates)
	}
	if launches[0].LaunchContext.FlowID != "flow-1" ||
		launches[0].LaunchContext.FlowPhaseID != "implementation" ||
		!launches[0].LaunchContext.Embedded ||
		!launches[0].LaunchContext.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", launches[0].LaunchContext)
	}
}

func TestModel_ActiveFlowAutoCloseRefreshUsesGlobalFetch(t *testing.T) {
	normalFlow := flowWithPhaseDetails()
	normalFlow.FlowID = "alpha-flow"
	normalFlow.Title = "Alpha Flow"
	activeFlow := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	activeFlow.FlowID = "bravo-flow"
	activeFlow.RepoPath = "/dev/bravo"
	activeFlow.Title = "Bravo Flow"
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"source output"}, state: "running"}
	var filters []flowstore.FlowFilter
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{activeFlow}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{normalFlow})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{activeFlow})

	m, cmd := update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/bravo",
		WorktreePath: "/dev/bravo-worktrees/flow-auto",
		FlowID:       "bravo-flow",
		FlowPhaseID:  "plan-review",
	}})
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	filters = nil
	sourceTerm.state = "exited"

	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd == nil {
		t.Fatal("active Flow source terminal auto-close should refresh active flows")
	}
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	if len(filters) != 1 || filters[0].RepoPath != "" {
		t.Fatalf("active Flow auto-close filters = %#v, want one global fetch", filters)
	}
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "alpha-flow" {
		t.Fatalf("normal Flows() cache after active Flow auto-close = %#v, want unchanged alpha-flow", got)
	}
}

func TestModel_FlowAutoModeDefersWhenCompletionObservedAfterSourceTerminalExits(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	current.Phases[1].Outcome = flowstore.OutcomeApproved
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"source output"}, state: "running"}
	listCalls := 0
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			if filter.RepoPath != "/dev/alpha" {
				t.Fatalf("FlowFilter.RepoPath = %q, want /dev/alpha", filter.RepoPath)
			}
			return []flowstore.FlowRecord{current}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       "flow-1",
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	sourceTerm.state = "exited"
	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd != nil {
		t.Fatalf("completion observed before exited terminal auto-closes returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates before exited terminal auto-closes = %#v, want none", updates)
	}

	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd == nil {
		t.Fatal("auto-closing exited source terminal should schedule refresh")
	}
	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("advance poll after source terminal auto-close should prepare deferred launch")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("deferred launch command returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after auto-close refresh = %#v, want implementation auto launch", updates)
	}
	if launches[0].LaunchContext.FlowPhaseID != "implementation" || !launches[0].LaunchContext.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", launches[0].LaunchContext)
	}
}

func TestModel_FlowAutoModeSuppressesWhenCompletionObservedWithNonAutoClosingSourceTerminal(t *testing.T) {
	for _, state := range []string{"failed", "terminated"} {
		t.Run(state, func(t *testing.T) {
			previous := autoFlowWithPhaseStatuses(map[string]string{
				"plan":           flowstore.PhaseCompleted,
				"plan-review":    flowstore.PhaseRunning,
				"implementation": flowstore.PhasePending,
			})
			current := autoFlowWithPhaseStatuses(map[string]string{
				"plan":           flowstore.PhaseCompleted,
				"plan-review":    flowstore.PhaseCompleted,
				"implementation": flowstore.PhaseReady,
			})
			current.Phases[1].Outcome = flowstore.OutcomeApproved
			sourceTerm := &fakeEmbeddedTerminal{lines: []string{"source output"}, state: "running"}
			var updates []flowstore.PhaseLaunchUpdate
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					updates = append(updates, update)
					return current, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					return sourceTerm, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
			var cmd tea.Cmd
			m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
				Command:      "codex",
				RepoPath:     "/dev/alpha",
				WorktreePath: "/dev/alpha-worktrees/flow-auto",
				FlowID:       "flow-1",
				FlowPhaseID:  "plan-review",
			}})
			if cmd == nil {
				t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
			}
			m, _ = update(m, model.FlowResultMsg{
				RepoPath:    "/dev/alpha",
				Flows:       []flowstore.FlowRecord{previous},
				ListRequest: m.ListRequest(ui.ModeFlows),
			})

			sourceTerm.state = state
			m, cmd = update(m, model.FlowResultMsg{
				RepoPath:    "/dev/alpha",
				Flows:       []flowstore.FlowRecord{current},
				ListRequest: m.ListRequest(ui.ModeFlows),
			})
			if cmd != nil {
				t.Fatalf("completion observed with %s source terminal returned command %T, want nil", state, cmd)
			}
			if len(updates) != 0 {
				t.Fatalf("launch updates with %s source terminal = %#v, want none", state, updates)
			}
		})
	}
}

func TestModel_FlowAutoModeDeferredLaunchNoopsAfterAutoModeDisabled(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	current.Title = "Stale deferred auto command"
	current.Instructions = "do not launch after auto mode off"
	current.RepoPath = "/dev/alpha"
	current.AutoMode = true
	current, err = store.Create(current)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	previous := current
	previous.Phases = append([]flowstore.FlowPhase(nil), current.Phases...)
	for i := range previous.Phases {
		switch previous.Phases[i].PhaseID {
		case "plan-review":
			previous.Phases[i].Status = flowstore.PhaseRunning
		case "implementation":
			previous.Phases[i].Status = flowstore.PhasePending
		}
	}
	sourceTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddFlowPhaseLaunchID() should not run after auto mode is disabled: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			if filter.RepoPath != "/dev/alpha" {
				t.Fatalf("FlowFilter.RepoPath = %q, want /dev/alpha", filter.RepoPath)
			}
			flow, err := store.Read(current.FlowID)
			if err != nil {
				t.Fatalf("Read(%q) error = %v", current.FlowID, err)
			}
			return []flowstore.FlowRecord{flow}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       current.FlowID,
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd != nil {
		t.Fatalf("deferred completion returned command %T, want nil", cmd)
	}
	if _, err := store.SetAutoMode(flowstore.AutoModeUpdate{FlowID: current.FlowID, Enabled: false}); err != nil {
		t.Fatalf("SetAutoMode(false) error = %v", err)
	}

	sourceTerm.state = "exited"
	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd == nil {
		t.Fatal("exiting the deferred source terminal should schedule refresh")
	}
	refresh := flowResultFromCommand(t, cmd)
	m, cmd = update(m, refresh)
	read, err := store.Read(current.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(read, "implementation").Status; got != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready after stale deferred command", got)
	}
}

func TestModel_FlowAutoModeDeferredLaunchWaitsOnFailedSourceTerminal(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"failed output"}, state: "running"}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       "flow-1",
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	sourceTerm.state = "failed"
	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd != nil {
		t.Fatalf("failed source terminal returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none after failed source terminal", updates)
	}
	if !strings.Contains(m.View(), "failed output") || !strings.Contains(m.View(), "plan-review failed") {
		t.Fatalf("failed source terminal should remain visible:\n%s", m.View())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("closing failed source terminal returned command %T, want nil", cmd)
	}
	if strings.Contains(m.View(), "failed output") {
		t.Fatalf("failed source terminal should be dismissed before refresh:\n%s", m.View())
	}
	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd != nil {
		t.Fatalf("refresh while source terminal is failed returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates after failed-terminal refresh = %#v, want none", updates)
	}
}

func TestModel_FlowAutoModeSuppressionDoesNotBlockLaterLaunchID(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	previous.Phases[1].LaunchIDs = []string{"launch-old"}
	rerun := previous
	rerun.Phases = append([]flowstore.FlowPhase(nil), previous.Phases...)
	rerun.Phases[1].LaunchIDs = []string{"launch-old", "launch-new"}
	completed := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	completed.Phases[1].Outcome = flowstore.OutcomeApproved
	completed.Phases[1].LaunchIDs = []string{"launch-old", "launch-new"}
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"old failed output"}, state: "running"}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := completed
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return sourceTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		LaunchID:     "launch-old",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       "flow-1",
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the source Flow terminal should schedule refresh and repaint")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	sourceTerm.state = "failed"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("closing old failed source terminal returned command %T, want nil", cmd)
	}

	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{rerun})
	if cmd != nil {
		t.Fatalf("new launch running advance returned command %T, want nil", cmd)
	}
	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{completed})
	if cmd == nil {
		t.Fatal("completion for newer launch ID should auto-launch next phase")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("new launch completion returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after newer completion = %#v, want implementation auto launch", updates)
	}
}

func TestModel_FlowAutoModeStaleTerminalBlocksDrainUntilDismissed(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	previous.Phases[1].LaunchIDs = []string{"launch-old"}
	rerun := previous
	rerun.Phases = append([]flowstore.FlowPhase(nil), previous.Phases...)
	rerun.Phases[1].LaunchIDs = []string{"launch-old", "launch-new"}
	completed := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	completed.Phases[1].Outcome = flowstore.OutcomeApproved
	completed.Phases[1].LaunchIDs = []string{"launch-old", "launch-new"}
	staleTerm := &fakeEmbeddedTerminal{lines: []string{"old failed output"}, state: "running"}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := completed
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return staleTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	var cmd tea.Cmd
	m, cmd = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		LaunchID:     "launch-old",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		FlowID:       "flow-1",
		FlowPhaseID:  "plan-review",
	}})
	if cmd == nil {
		t.Fatal("starting the stale Flow terminal should schedule refresh and repaint")
	}
	m, _ = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{previous},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	staleTerm.state = "failed"

	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{rerun})
	if cmd != nil {
		t.Fatalf("new launch running advance returned command %T, want nil", cmd)
	}
	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{completed})
	if cmd != nil {
		t.Fatalf("completion with stale flow terminal returned command %T, want nil", cmd)
	}
	if !strings.Contains(m.View(), "old failed output") {
		t.Fatalf("stale failed terminal should remain visible while it blocks the drain:\n%s", m.View())
	}
	m = model.ClearFlowEmbeddedTerminalsForTest(m)
	m, cmd = autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{completed})
	if cmd == nil {
		t.Fatal("completion should drain after stale terminal is dismissed")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 1 {
		t.Fatalf("new launch completion returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after newer completion = %#v, want implementation auto launch", updates)
	}
}

func TestModel_FlowAutoModeLaunchesNextImplementationChildOrReviewLoop(t *testing.T) {
	for _, tc := range []struct {
		name        string
		current     flowstore.FlowRecord
		wantPhaseID string
	}{
		{
			name: "next child",
			current: autoFlowWithPhases(
				flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
				flowstore.FlowPhase{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2},
				flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3},
				flowstore.FlowPhase{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API", Status: flowstore.PhaseCompleted, Order: 10},
				flowstore.FlowPhase{PhaseID: "implementation-ui", ParentPhaseID: "implementation", Title: "UI", Status: flowstore.PhaseReady, Order: 20},
				flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4},
			),
			wantPhaseID: "implementation-ui",
		},
		{
			name: "review loop",
			current: autoFlowWithPhases(
				flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
				flowstore.FlowPhase{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2},
				flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3},
				flowstore.FlowPhase{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API", Status: flowstore.PhaseCompleted, Order: 10},
				flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 4},
			),
			wantPhaseID: "review-loop",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := tc.current
			previous.Phases = append([]flowstore.FlowPhase(nil), tc.current.Phases...)
			for i := range previous.Phases {
				if previous.Phases[i].PhaseID == "implementation-api" {
					previous.Phases[i].Status = flowstore.PhaseRunning
				}
			}

			launchMsg, updates := autoLaunchFromFlowRefresh(t, previous, tc.current)
			if len(updates) != 1 || updates[0].PhaseID != tc.wantPhaseID {
				t.Fatalf("launch updates = %#v, want %s", updates, tc.wantPhaseID)
			}
			if launchMsg.LaunchContext.FlowPhaseID != tc.wantPhaseID {
				t.Fatalf("launched phase = %q, want %q", launchMsg.LaunchContext.FlowPhaseID, tc.wantPhaseID)
			}
		})
	}
}

func TestModel_FlowAutoModeLaunchesAutoreviewAfterPRCreationCompletesWithPRMetadata(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhaseCompleted,
		"pr-creation":    flowstore.PhaseRunning,
		"autoreview":     flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhaseCompleted,
		"pr-creation":    flowstore.PhaseCompleted,
		"autoreview":     flowstore.PhaseReady,
	})
	current.Branch = "flow/auto"
	current.PR = flowstore.PullRequest{
		Provider:   "github",
		Number:     115,
		URL:        "https://github.com/approachcontrol/approach/pull/115",
		HeadBranch: "flow/auto",
		BaseBranch: "main",
	}

	launchMsg, updates := autoLaunchFromFlowRefresh(t, previous, current)
	if len(updates) != 1 || updates[0].PhaseID != "autoreview" {
		t.Fatalf("launch updates = %#v, want autoreview", updates)
	}
	if launchMsg.LaunchContext.FlowPhaseID != "autoreview" {
		t.Fatalf("launched phase = %q, want autoreview", launchMsg.LaunchContext.FlowPhaseID)
	}
}

func TestModel_FlowAutoModeLaunchesEveryEligibleFlowFromOneRefresh(t *testing.T) {
	previousOne := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	currentOne := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	previousTwo := previousOne
	previousTwo.FlowID = "flow-2"
	previousTwo.Branch = "flow/auto-two"
	previousTwo.WorktreePath = "/dev/alpha-worktrees/flow-auto-two"
	currentTwo := currentOne
	currentTwo.FlowID = "flow-2"
	currentTwo.Branch = "flow/auto-two"
	currentTwo.WorktreePath = "/dev/alpha-worktrees/flow-auto-two"

	currentByFlowID := map[string]flowstore.FlowRecord{
		currentOne.FlowID: currentOne,
		currentTwo.FlowID: currentTwo,
	}
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			if !update.AutoLaunch {
				t.Fatalf("auto launch update = %#v, want AutoLaunch true", update)
			}
			launched := currentByFlowID[update.FlowID]
			for i := range launched.Phases {
				if launched.Phases[i].PhaseID == update.PhaseID {
					launched.Phases[i].Status = flowstore.PhaseRunning
					launched.Phases[i].LaunchIDs = append(launched.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			return launched, nil
		},
	})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previousOne, previousTwo})
	m, cmd := autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{currentOne, currentTwo})
	if cmd == nil {
		t.Fatal("two eligible auto-mode flows should return a batched launch command")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, m, cmd)
	if len(launches) != 2 {
		t.Fatalf("auto launches = %d, want one per eligible Flow", len(launches))
	}
	var launchedFlowIDs []string
	for _, launch := range launches {
		launchedFlowIDs = append(launchedFlowIDs, launch.LaunchContext.FlowID)
		if launch.LaunchContext.FlowPhaseID != "implementation" {
			t.Fatalf("launched phase = %q, want implementation", launch.LaunchContext.FlowPhaseID)
		}
	}
	if len(updates) != 2 {
		t.Fatalf("launch updates = %#v, want two updates", updates)
	}
	for _, flowID := range []string{"flow-1", "flow-2"} {
		if !slices.Contains(launchedFlowIDs, flowID) {
			t.Fatalf("launched flow ids = %#v, missing %s", launchedFlowIDs, flowID)
		}
	}
}

func TestModel_FlowAutoModeStaleCommandNoopsAfterAutoModeDisabled(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	current.Title = "Stale auto command"
	current.Instructions = "do not launch after auto mode off"
	current.RepoPath = "/dev/alpha"
	current.AutoMode = true
	current, err = store.Create(current)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	previous := current
	previous.Phases = append([]flowstore.FlowPhase(nil), current.Phases...)
	for i := range previous.Phases {
		switch previous.Phases[i].PhaseID {
		case "plan-review":
			previous.Phases[i].Status = flowstore.PhaseRunning
		case "implementation":
			previous.Phases[i].Status = flowstore.PhasePending
		}
	}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:         "codex",
		AddFlowPhaseLaunchID: store.AddPhaseLaunchID,
	})
	m = model.WithAutoAdvanceSnapshotForTest(m, []flowstore.FlowRecord{previous})
	m, cmd := autoAdvanceLaunchCommand(m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("auto mode completion should prepare a launch command before auto mode is disabled")
	}
	if _, err := store.SetAutoMode(flowstore.AutoModeUpdate{FlowID: current.FlowID, Enabled: false}); err != nil {
		t.Fatalf("SetAutoMode(false) error = %v", err)
	}

	if msg := cmd(); msg != nil {
		m, _ = update(m, msg)
	}
	read, err := store.Read(current.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := phaseByID(read, "implementation").Status; got != flowstore.PhaseReady {
		t.Fatalf("implementation status = %q, want ready after stale auto command", got)
	}
}

func TestModel_FlowAutoModeDoesNotLaunchMergeAfterAutoreviewCompletes(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhaseCompleted,
		"pr-creation":    flowstore.PhaseCompleted,
		"autoreview":     flowstore.PhaseRunning,
		"merge":          flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseCompleted,
		"review-loop":    flowstore.PhaseCompleted,
		"pr-creation":    flowstore.PhaseCompleted,
		"autoreview":     flowstore.PhaseCompleted,
		"merge":          flowstore.PhaseReady,
	})

	_, cmd, updates := autoLaunchCommandFromFlowRefresh(t, previous, current)
	if cmd != nil {
		t.Fatalf("autoreview completion returned auto-launch command %T, want nil", cmd)
	}
	if len(*updates) != 0 {
		t.Fatalf("launch updates = %#v, want none", updates)
	}
	if got := current.AutoMode; !got {
		t.Fatal("test setup should keep auto mode on after autoreview")
	}
}

func TestModel_FlowAutoModeDoesNotLaunchForNonCompletedTransitions(t *testing.T) {
	for _, status := range []string{flowstore.PhaseSkipped, flowstore.PhaseBlocked, flowstore.PhaseNeedsAttention} {
		t.Run(status, func(t *testing.T) {
			previous := autoFlowWithPhaseStatuses(map[string]string{
				"plan":           flowstore.PhaseCompleted,
				"plan-review":    flowstore.PhaseRunning,
				"implementation": flowstore.PhaseReady,
			})
			current := autoFlowWithPhaseStatuses(map[string]string{
				"plan":           flowstore.PhaseCompleted,
				"plan-review":    status,
				"implementation": flowstore.PhaseReady,
			})
			if status == flowstore.PhaseSkipped {
				current.Phases[1].Notes = "Skipped by operator."
			}

			_, cmd, updates := autoLaunchCommandFromFlowRefresh(t, previous, current)
			if cmd != nil {
				t.Fatalf("%s transition returned auto-launch command %T, want nil", status, cmd)
			}
			if len(*updates) != 0 {
				t.Fatalf("launch updates = %#v, want none", updates)
			}
		})
	}
}

func TestModel_FlowAutoModeIgnoresStaleFlowResults(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})

	m, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: 999999,
	})
	if cmd != nil {
		t.Fatalf("stale FlowResultMsg returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none for stale result", updates)
	}
	if got := m.Flows(); len(got) != 1 || phaseByID(got[0], "plan-review").Status != flowstore.PhaseRunning {
		t.Fatalf("Flows() = %#v, want previous snapshot unchanged", got)
	}
}

func TestModel_FlowAutoModeDoesNotLaunchAfterLeavingFlowsMode(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	request := m.ListRequest(ui.ModeFlows)
	m, _ = switchTestMode(m, ui.ModeWorktrees)

	m, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: request,
	})
	if cmd != nil {
		t.Fatalf("accepted FlowResultMsg outside flows mode returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none after leaving flows mode", updates)
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("Mode() = %d, want worktrees", m.Mode())
	}
	if got := m.Flows(); len(got) != 1 || phaseByID(got[0], "implementation").Status != flowstore.PhaseReady {
		t.Fatalf("Flows() = %#v, want accepted refresh without auto launch", got)
	}
}

func TestModel_FlowAutoModeDoesNotLaunchAfterSwitchingToActiveFlowsMode(t *testing.T) {
	previous := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseRunning,
		"implementation": flowstore.PhasePending,
	})
	current := autoFlowWithPhaseStatuses(map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	var updates []flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{current}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	request := m.ListRequest(ui.ModeFlows)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd == nil {
		t.Fatal("switching to active flows returned nil command, want active flows fetch")
	}

	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: request,
	})
	if cmd != nil {
		t.Fatalf("accepted FlowResultMsg in active flows mode returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none after switching to active flows mode", updates)
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("Mode() = %d, want active flows", m.Mode())
	}
	if got := m.Flows(); len(got) != 1 || phaseByID(got[0], "implementation").Status != flowstore.PhaseReady {
		t.Fatalf("Flows() = %#v, want accepted refresh without auto launch", got)
	}
}

func TestModel_FlowPhasesCollapsedByDefaultAndToggleWithEnter(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})

	if m.ExpandedFlowID() != "" {
		t.Fatalf("expanded flow = %q, want collapsed by default", m.ExpandedFlowID())
	}
	if strings.Contains(m.View(), "plan-review:approved") {
		t.Fatalf("collapsed flow should not render phase detail rows:\n%s", m.View())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("toggle phases returned command %T, want nil", cmd)
	}
	if got := m.ExpandedFlowID(); got != flow.FlowID {
		t.Fatalf("expanded flow = %q, want %q", got, flow.FlowID)
	}
	if !strings.Contains(m.View(), "plan-review:approved") {
		t.Fatalf("expanded flow should render phase detail rows:\n%s", m.View())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ExpandedFlowID() != "" || strings.Contains(m.View(), "plan-review:approved") {
		t.Fatalf("second toggle should collapse phase detail rows:\n%s", m.View())
	}
}

func TestModel_EnterOnSelectedFlowPhaseCollapsesWithoutLaunching(t *testing.T) {
	addLaunchRan := false
	launchAgentRan := false
	startEmbeddedRan := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addLaunchRan = true
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAgentRan = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			startEmbeddedRan = true
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter on selected Flow phase returned command %T, want nil", cmd)
	}
	if m.ExpandedFlowID() != "" || m.SelectedFlowPhaseID() != "" {
		t.Fatalf("enter on selected Flow phase should collapse and clear phase selection, got expanded=%q phase=%q", m.ExpandedFlowID(), m.SelectedFlowPhaseID())
	}
	if addLaunchRan || launchAgentRan || startEmbeddedRan {
		t.Fatalf("enter on selected Flow phase launched: add=%v launch=%v embedded=%v", addLaunchRan, launchAgentRan, startEmbeddedRan)
	}
}

func TestModel_GOnSelectedFlowPhaseLaunchesFirstLaunchablePhaseByDefaultHeadless(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	launchAgentRan := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeModel:           "claude-sonnet-5",
		ClaudeReasoningEffort: "max",
		SessionStateRoot:      "/state/approach/sessions/v1",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAgentRan = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Branch:       "flow/selected",
		Commit:       "abc123",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 2},
		},
	}})
	m = selectFlowPhaseByID(t, m, "review-loop")

	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare a headless launch")
	}
	m, cmd = update(m, cmd())
	if launchAgentRan {
		t.Fatal("default headless Flow launch should not call LaunchAgent for CLI providers")
	}
	if cmd == nil {
		t.Fatal("expected embedded Flow launch to return repaint/fetch command")
	}
	if started.FlowPhaseID != "implementation" || launchUpdate.PhaseID != "implementation" {
		t.Fatalf("g launched %#v with update %#v, want first launchable implementation", started, launchUpdate)
	}
	if started.Command != "claude" || started.Model != "claude-sonnet-5" || started.ReasoningEffort != "max" {
		t.Fatalf("Flow phase launch agent settings = command %q model %q effort %q, want claude/claude-sonnet-5/max", started.Command, started.Model, started.ReasoningEffort)
	}
	if !started.Embedded || !started.Headless || !started.FlowLaunchTracked {
		t.Fatalf("Flow launch should be embedded, headless, and tracked: %#v", started)
	}
	if started.LaunchID == "" || started.LaunchID != launchUpdate.LaunchID {
		t.Fatalf("launch IDs = context %q update %#v", started.LaunchID, launchUpdate)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("headless Flow launch should keep list focus and not forward input: %#v", fakeTerm.writes)
	}
}

func TestModel_HeadlessFlowLaunchFromTerminalInputReturnsFocusToList(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{state: "running"},
		{state: "running"},
	}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Status:       flowstore.StatusInProgress,
	}})

	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:     "codex",
		FlowID:      "flow-1",
		FlowPhaseID: "implementation",
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(terms[0].writes) != 1 || terms[0].writes[0] != "z" {
		t.Fatalf("interactive Flow launch should focus terminal input, writes = %#v", terms[0].writes)
	}

	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:     "codex",
		FlowID:      "flow-1",
		FlowPhaseID: "review-loop",
		Headless:    true,
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(terms[0].writes) != 1 {
		t.Fatalf("headless Flow launch should leave previous terminal writes unchanged, got %#v", terms[0].writes)
	}
	if len(terms[1].writes) != 0 {
		t.Fatalf("headless Flow launch should not inherit terminal input focus, got writes %#v", terms[1].writes)
	}
}

func TestModel_AutoHeadlessFlowLaunchPreservesTerminalFocus(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{state: "running"},
		{state: "running"},
	}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Status:       flowstore.StatusInProgress,
	}})

	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:     "codex",
		FlowID:      "flow-1",
		FlowPhaseID: "implementation",
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(terms[0].writes) != 1 || terms[0].writes[0] != "z" {
		t.Fatalf("interactive Flow launch should focus terminal input, writes = %#v", terms[0].writes)
	}

	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:        "codex",
		FlowID:         "flow-1",
		FlowPhaseID:    "review-loop",
		Headless:       true,
		FlowAutoLaunch: true,
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(terms[0].writes) != 2 || terms[0].writes[1] != "y" {
		t.Fatalf("auto headless Flow launch should preserve focused terminal input, writes = %#v", terms[0].writes)
	}
	if len(terms[1].writes) != 0 {
		t.Fatalf("auto headless Flow launch should not focus the new terminal, got writes %#v", terms[1].writes)
	}
}

func TestModel_GWithFocusedFlowTerminalWritesInputWithoutLaunchingPhase(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	addLaunchRan := false
	launchAgentRan := false
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addLaunchRan = true
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAgentRan = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
		},
	}})
	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:     "codex",
		FlowID:      "flow-1",
		FlowPhaseID: "implementation",
	}})

	m, cmd := flowLaunchFromKey(t, m)
	if cmd != nil {
		t.Fatalf("terminal-focused g returned command %T, want nil", cmd)
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "g" {
		t.Fatalf("terminal-focused g writes = %#v, want forwarded input", fakeTerm.writes)
	}
	if addLaunchRan || launchAgentRan || starts != 1 {
		t.Fatalf("terminal-focused g launched phase: add=%v launch=%v starts=%d", addLaunchRan, launchAgentRan, starts)
	}
}

func TestModel_GOnFlowPhaseWithHeadlessOffLaunchesEmbeddedInteractiveCLI(t *testing.T) {
	for _, command := range []string{"codex", "claude"} {
		t.Run(command, func(t *testing.T) {
			var launchUpdate flowstore.PhaseLaunchUpdate
			var started actions.AgentLaunchContext
			fakeTerm := &fakeEmbeddedTerminal{state: "running"}
			m := newTestModel(testRepos(), model.Options{
				AgentCommand:     command,
				SessionStateRoot: "/state/approach/sessions/v1",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					launchUpdate = update
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					t.Fatalf("headless-off Flow CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
					return actions.TerminalLaunchSpec{}, nil
				},
				StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
					started = ctx
					return fakeTerm, nil
				},
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return nil, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
				FlowID:       "flow-1",
				RepoPath:     "/dev/alpha",
				WorktreePath: "/dev/alpha-worktrees/flow-interactive",
				Branch:       "flow/interactive",
				Commit:       "abc123",
				Status:       flowstore.StatusInProgress,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
				},
			}})
			m = selectFlowPhaseByID(t, m, "implementation")
			m, cmd := flowLaunchFromKey(t, m)
			if cmd == nil {
				t.Fatal("g should prepare an embedded interactive launch")
			}
			m, cmd = update(m, cmd())
			if cmd == nil {
				t.Fatal("expected embedded Flow launch to return repaint/fetch command")
			}
			m = settleModelCommands(t, m, cmd, 2)
			if started.Command != command ||
				started.FlowID != "flow-1" ||
				started.FlowPhaseID != "implementation" ||
				started.WorktreePath != "/dev/alpha-worktrees/flow-interactive" ||
				started.Branch != "flow/interactive" ||
				started.Commit != "abc123" ||
				started.SessionStateRoot != "/state/approach/sessions/v1" {
				t.Fatalf("embedded launch context = %#v", started)
			}
			if !started.Embedded || started.Headless || !started.FlowLaunchTracked {
				t.Fatalf("headless-off Flow launch should be embedded, interactive, and tracked: %#v", started)
			}
			if started.LaunchID == "" || started.LaunchID != launchUpdate.LaunchID {
				t.Fatalf("launch IDs = context %q update %#v", started.LaunchID, launchUpdate)
			}
			wantWrite := "\x1b[200~" + started.InitialPrompt + "\x1b[201~"
			if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != wantWrite {
				t.Fatalf("prefill writes = %#v, want exact bracketed paste %q", fakeTerm.writes, wantWrite)
			}
			if strings.HasSuffix(fakeTerm.writes[0], "\r") || strings.HasSuffix(fakeTerm.writes[0], "\n") {
				t.Fatalf("prefill write should not append submit byte, got %q", fakeTerm.writes[0])
			}
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
			if len(fakeTerm.writes) != 2 || fakeTerm.writes[1] != "z" {
				t.Fatalf("interactive Flow launch should focus terminal input and forward z: %#v", fakeTerm.writes)
			}
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			if len(fakeTerm.writes) != 2 {
				t.Fatalf("terminal close prefix should not forward x: %#v", fakeTerm.writes)
			}
			if view := m.View(); !strings.Contains(view, "Terminate embedded terminal?") {
				t.Fatalf("ctrl+] x should open terminal close confirmation:\n%s", view)
			}
		})
	}
}

func TestModel_GOnFlowPhaseEmbeddedInteractivePrefillSanitizesTerminalControls(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		FlowPromptTemplates: model.FlowPromptTemplates{
			Implementation: "Alpha\x1b[201~\nBeta\x1b[200~\x1b[31m\a\tOmega\rDone",
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-interactive",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded interactive launch")
	}
	m, cmd = update(m, cmd())
	if cmd == nil {
		t.Fatal("expected embedded Flow launch to return repaint/fetch command")
	}
	m = settleModelCommands(t, m, cmd, 2)

	wantWrite := "\x1b[200~" + appendFlowDoneInstructionForTest("Alpha\nBeta\tOmegaDone") + "\x1b[201~"
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != wantWrite {
		t.Fatalf("prefill writes = %#v, want exact sanitized bracketed paste %q", fakeTerm.writes, wantWrite)
	}
}

func TestModel_GOnSelectedNonLaunchableFlowPhaseLaunchesFirstReadySibling(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow CLI launch should start an embedded terminal, not LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending, Order: 1},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 2},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter on pending selected Flow phase returned command %T, want nil", cmd)
	}
	if m.ExpandedFlowID() != "" || m.SelectedFlowPhaseID() != "" {
		t.Fatalf("enter should collapse pending selected phase, got expanded=%q phase=%q", m.ExpandedFlowID(), m.SelectedFlowPhaseID())
	}
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd = flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should launch the first launchable sibling")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)
	if launchUpdate.PhaseID != "review-loop" || started.FlowPhaseID != "review-loop" {
		t.Fatalf("g launched update %#v context %#v, want review-loop", launchUpdate, started)
	}
}

func TestModel_GWithNoLaunchableFlowPhaseDoesNotMutateOrLaunch(t *testing.T) {
	addLaunchRan := false
	launchAgentRan := false
	startEmbeddedRan := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addLaunchRan = true
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAgentRan = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			startEmbeddedRan = true
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending, Order: 1},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 2},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected status expiry command without a launchable phase")
	}
	if addLaunchRan || launchAgentRan || startEmbeddedRan {
		t.Fatalf("g without launchable phase launched: add=%v launch=%v embedded=%v", addLaunchRan, launchAgentRan, startEmbeddedRan)
	}
	if got := m.TransientError(); got != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", got)
	}
}

// repairableFlowForShortcut names the selected repo as its own, the way a user
// pressing R on a Flow row is looking at it. The worktree is a real directory
// because repair resolves its launch paths through os.Stat.
func repairableFlowForShortcut() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-repair",
		Title:        "Repair shortcut",
		RepoPath:     "/dev/alpha",
		WorktreePath: os.TempDir(),
		Branch:       "flow/repair",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/sessions/v1/plans/plan-1/plan.md",
		AutoMode:     true,
		Headless:     true,
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Kind:    flowstore.KindImplementation,
			Status:  flowstore.PhaseBlocked,
			Outcome: "blocked",
			Notes:   "persisted metadata is inconsistent",
		}},
	}
}

func repairKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}
}

// repairFromKey presses R and settles the launch lifecycle's asynchronous hops.
// R only submits an intent now: the authoritative read, the cross-process
// reservation, and the terminal install all land on later messages.
func repairFromKey(t *testing.T, m model.Model) model.Model {
	t.Helper()
	m, cmd := update(m, repairKey())
	if cmd == nil {
		t.Fatal("R should submit a repair launch intent")
	}
	return settleModelCommands(t, m, cmd, 6)
}

func TestModel_RLaunchesUntrackedEmbeddedRepairFromBothFlowSurfaces(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("active=%v", active), func(t *testing.T) {
			var started actions.AgentLaunchContext
			launchUpdates := 0
			record := repairableFlowForShortcut()
			m := newTestModel(testRepos(), model.Options{
				AgentCommand:          "claude",
				ClaudeModel:           "claude-sonnet-5",
				ClaudeReasoningEffort: "max",
				SessionStateRoot:      "/state/approach/sessions/v1",
				AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					launchUpdates++
					return flowstore.FlowRecord{}, nil
				},
				StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
					started = ctx
					return &fakeEmbeddedTerminal{state: "running"}, nil
				},
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return []flowstore.FlowRecord{record}, nil
				},
			})
			if active {
				m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{record})
			} else {
				m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
			}
			if !active {
				m = selectFlowPhaseByID(t, m, "implementation")
			}

			m = repairFromKey(t, m)

			ctx := started
			if !ctx.FlowRepair || ctx.FlowID != record.FlowID || ctx.FlowPhaseID != "" || ctx.FlowLaunchTracked {
				t.Fatalf("repair launch classification = %#v", ctx)
			}
			if ctx.Command != "claude" || ctx.Model != "claude-sonnet-5" || ctx.ReasoningEffort != "max" || !ctx.Headless || !ctx.Embedded {
				t.Fatalf("repair launch settings = %#v", ctx)
			}
			if ctx.PlanID != record.PlanID || ctx.PlanPath != record.PlanPath || ctx.WorktreePath != record.WorktreePath || ctx.Branch != record.Branch || ctx.Commit != record.Commit || ctx.SessionStateRoot != "/state/approach/sessions/v1" {
				t.Fatalf("repair launch metadata = %#v", ctx)
			}
			for _, want := range []string{record.FlowID, "blocked", "persisted metadata is inconsistent", "approach flow read", "phase reset", "phase restart", "Do not launch the next phase"} {
				if !strings.Contains(ctx.InitialPrompt, want) {
					t.Fatalf("repair prompt missing %q:\n%s", want, ctx.InitialPrompt)
				}
			}
			if strings.Contains(ctx.InitialPrompt, model.FlowPhaseDoneInstructionForTest()) {
				t.Fatalf("repair prompt contains ordinary phase completion instruction:\n%s", ctx.InitialPrompt)
			}
			if launchUpdates != 0 {
				t.Fatalf("repair recorded %d phase launch updates, want zero", launchUpdates)
			}
			if m.TransientError() != "" {
				t.Fatalf("successful repair set status %q", m.TransientError())
			}
		})
	}
}

// A Flow that closes between the authoritative read and the cross-process
// reservation is the race this names: the read still sees an open record, and
// only the reservation refuses. A close landing before the read is a different
// refusal now — "Flow is no longer repairable" — because the classifier rejects
// a closed record.
func TestModel_RepairReservationRejectsCloseBeforeTerminalStart(t *testing.T) {
	record := repairableFlowForShortcut()
	started := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadFlow: func(string) (flowstore.FlowRecord, error) {
			return record, nil
		},
		ReserveFlowRepairLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{}, nil, errors.New("cannot reserve repair because flow is closed")
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			started++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	m = repairFromKey(t, m)
	if started != 0 {
		t.Fatalf("closed Flow started %d repair terminals, want zero", started)
	}
	if got := m.TransientError(); !strings.Contains(got, "closed") || !strings.Contains(got, "Reserve persisted Flow for repair: ") {
		t.Fatalf("status = %q, want the wrapped closed reservation error", got)
	}
	if model.FlowLaunchAttemptHeldForTest(m, record.FlowID) {
		t.Fatal("a refused repair reservation stranded its attempt")
	}
}

func TestModel_RIsAvailableAgainAfterAFailedRepair(t *testing.T) {
	record := repairableFlowForShortcut()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadFlow: func(string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("read failed")
		},
		// A refused repair refreshes the Flow surface, so the retry below is
		// pressed against whatever that refresh returns. The store still has
		// the Flow after a failed read; a fake without this would empty the
		// pane and withdraw R for a reason this test is not about.
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{record}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	m = repairFromKey(t, m)
	if got := m.TransientError(); !strings.Contains(got, "read failed") {
		t.Fatalf("fresh-read failure status = %q", got)
	}
	m, retryCmd := update(m, repairKey())
	if retryCmd == nil || strings.Contains(m.TransientError(), "already pending") {
		t.Fatalf("fresh-read failure retained its attempt: cmd=%T status=%q", retryCmd, m.TransientError())
	}
}

func TestModel_ManualPhaseLaunchWaitsForPendingOrRetainedRepair(t *testing.T) {
	for _, retained := range []bool{false, true} {
		name := "pending repair attempt"
		if retained {
			name = "retained terminal"
		}
		t.Run(name, func(t *testing.T) {
			record := repairableFlowForShortcut()
			launchUpdates := 0
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return []flowstore.FlowRecord{record}, nil
				},
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					launchUpdates++
					return record, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					return &fakeEmbeddedTerminal{state: "running"}, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
			if retained {
				m = repairFromKey(t, m)
			} else {
				// Stop after the key press: the repair attempt holds the Flow
				// from admission onward, before any terminal exists.
				var repairCmd tea.Cmd
				m, repairCmd = update(m, repairKey())
				if repairCmd == nil {
					t.Fatal("repair should reserve a launch attempt")
				}
			}

			repaired := record
			repaired.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
			repaired.Phases[0].Status = flowstore.PhaseReady
			m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{repaired}})
			m, _ = flowLaunchFromKey(t, m)
			if launchUpdates != 0 {
				t.Fatalf("manual launch persisted %d phase updates during repair", launchUpdates)
			}
			if got := m.TransientError(); got != "No launchable Flow phase" {
				t.Fatalf("manual launch status = %q, want compatibility refusal", got)
			}
		})
	}
}

func TestModel_RReservesRepairLaunchAndRejectsDuplicatePresses(t *testing.T) {
	starts := 0
	record := repairableFlowForShortcut()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{record}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	m, firstCmd := update(m, repairKey())
	if firstCmd == nil {
		t.Fatal("first repair command is nil")
	}
	// The second press is refused at admission; the only command it returns is
	// the status expiry timer.
	m, _ = update(m, repairKey())
	if got := m.TransientError(); !strings.Contains(got, "already pending") {
		t.Fatalf("duplicate repair status = %q", got)
	}
	m = settleModelCommands(t, m, firstCmd, 6)
	if starts != 1 {
		t.Fatalf("duplicate repair press started %d terminals, want one", starts)
	}
}

func TestModel_RRefusesWhenPersistedFlowIsNoLongerRepairable(t *testing.T) {
	starts := 0
	record := repairableFlowForShortcut()
	persisted := record
	persisted.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	persisted.Phases[0].Status = flowstore.PhaseReady
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
		ReadFlow: func(string) (flowstore.FlowRecord, error) {
			return persisted, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	m = repairFromKey(t, m)
	if starts != 0 {
		t.Fatalf("stale repair request started %d terminals, want zero", starts)
	}
	if got := m.TransientError(); !strings.Contains(got, "no longer repairable") {
		t.Fatalf("stale repair status = %q", got)
	}
}

func TestModel_RIsUnavailableForBlockedFlowWithLiveSession(t *testing.T) {
	for _, status := range []string{flowstore.PhaseBlocked, flowstore.PhaseNeedsAttention} {
		t.Run(status, func(t *testing.T) {
			starts := 0
			record := repairableFlowForShortcut()
			record.Phases[0].Status = status
			record.Phases[0].LaunchIDs = []string{"launch-1"}
			record.Phases[0].Sessions = []flowstore.Session{{
				SessionID: "live-session",
				LaunchID:  "launch-1",
				Status:    "last_seen",
			}}
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					starts++
					return &fakeEmbeddedTerminal{state: "running"}, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
			if strings.Contains(m.View(), "R      repair") {
				t.Fatalf("live %s Flow advertised repair:\n%s", status, m.View())
			}
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
			if cmd != nil || starts != 0 || m.TransientError() != "" {
				t.Fatalf("live %s repair: cmd=%T starts=%d status=%q", status, cmd, starts, m.TransientError())
			}
		})
	}
}

func TestModel_RRejectsNonEmbeddedProvidersBeforeLaunch(t *testing.T) {
	for _, tt := range []struct {
		name    string
		command string
		want    string
	}{
		{name: "missing", command: "", want: "Press A"},
		{name: "malformed", command: "not a provider", want: "codex or claude"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			starts := 0
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: tt.command,
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					starts++
					return &fakeEmbeddedTerminal{}, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{repairableFlowForShortcut()})
			m = repairFromKey(t, m)
			if starts != 0 {
				t.Fatalf("repair started %d terminals for provider %q", starts, tt.command)
			}
			if got := m.TransientError(); !strings.Contains(got, tt.want) {
				t.Fatalf("status = %q, want containing %q", got, tt.want)
			}
		})
	}
}

func TestModel_RIsNoopForHealthyFlowAndExplainsOccupiedRepairSlot(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		starts := 0
		record := repairableFlowForShortcut()
		record.Phases[0].Status = flowstore.PhaseReady
		m := newTestModel(testRepos(), model.Options{
			AgentCommand: "codex",
			StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
				starts++
				return &fakeEmbeddedTerminal{}, nil
			},
		})
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
		if cmd != nil || starts != 0 || m.TransientError() != "" {
			t.Fatalf("healthy R result: cmd=%T starts=%d status=%q", cmd, starts, m.TransientError())
		}
	})

	t.Run("occupied", func(t *testing.T) {
		starts := 0
		m := newTestModel(testRepos(), model.Options{
			AgentCommand: "codex",
			StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
				starts++
				return &fakeEmbeddedTerminal{state: "failed"}, nil
			},
		})
		record := repairableFlowForShortcut()
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
		m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
			Command:     "codex",
			FlowID:      record.FlowID,
			FlowPhaseID: "implementation",
			Headless:    true,
		}})
		m, _ = update(m, repairKey())
		if starts != 1 {
			t.Fatalf("occupied repair started %d terminals, want only existing slot", starts)
		}
		if got := m.TransientError(); !strings.Contains(got, "dismiss the existing Flow terminal") {
			t.Fatalf("occupied repair status = %q", got)
		}
	})
}

func TestModel_CtrlJWithLaunchableFlowPhaseDoesNotMutateOrLaunch(t *testing.T) {
	addLaunchRan := false
	launchAgentRan := false
	startEmbeddedRan := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addLaunchRan = true
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAgentRan = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			startEmbeddedRan = true
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-selected",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if cmd != nil {
		t.Fatalf("ctrl+j with a launchable phase returned command %T, want nil", cmd)
	}
	if addLaunchRan || launchAgentRan || startEmbeddedRan {
		t.Fatalf("ctrl+j launched phase: add=%v launch=%v embedded=%v", addLaunchRan, launchAgentRan, startEmbeddedRan)
	}
}

func TestModel_GOutsideFlowsModesDoesNotMutateOrLaunch(t *testing.T) {
	tests := []struct {
		name string
		mode ui.Mode
	}{
		{name: "worktrees", mode: ui.ModeWorktrees},
		{name: "branches", mode: ui.ModeBranches},
		{name: "plans", mode: ui.ModePlans},
		{name: "sessions", mode: ui.ModeSessions},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addLaunchRan := false
			launchAgentRan := false
			startEmbeddedRan := false
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					addLaunchRan = true
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					launchAgentRan = true
					return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					startEmbeddedRan = true
					return &fakeEmbeddedTerminal{}, nil
				},
			})
			m, _ = switchTestMode(m, tc.mode)
			if m.Mode() != tc.mode {
				t.Fatalf("starting mode = %d, want %d", m.Mode(), tc.mode)
			}

			m, cmd := flowLaunchFromKey(t, m)
			if cmd != nil {
				t.Fatalf("g outside flows mode returned command %T, want nil", cmd)
			}
			if addLaunchRan || launchAgentRan || startEmbeddedRan {
				t.Fatalf("g outside flows mode launched phase: add=%v launch=%v embedded=%v", addLaunchRan, launchAgentRan, startEmbeddedRan)
			}
			if got := m.TransientError(); got != "" {
				t.Fatalf("g outside flows mode set status %q, want none", got)
			}
		})
	}
}

func TestModel_HKeyInFlowsTogglesHeadlessWithoutNavigatingModes(t *testing.T) {
	record := flowWithPhaseDetails()
	record.UpdatedAt = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	var updates []flowstore.HeadlessUpdate
	m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{
		SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			record.Headless = update.Enabled
			record.UpdatedAt = record.UpdatedAt.Add(time.Minute)
			return record, nil
		},
	}), []flowstore.FlowRecord{record})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd == nil {
		t.Fatal("flows-mode h returned nil command")
	}
	m, _ = update(m, cmd())
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("flows-mode h changed mode to %d, want flows", m.Mode())
	}
	if view := m.View(); !strings.Contains(view, "headless off") {
		t.Fatalf("flows-mode h should show headless off:\n%s", view)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd == nil {
		t.Fatal("second flows-mode h returned nil command")
	}
	m, _ = update(m, cmd())
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("second flows-mode h changed mode to %d, want flows", m.Mode())
	}
	if view := m.View(); !strings.Contains(view, "headless on") {
		t.Fatalf("second flows-mode h should show headless on:\n%s", view)
	}
	if len(updates) != 2 || updates[0].FlowID != record.FlowID || updates[0].Enabled || !updates[1].Enabled {
		t.Fatalf("headless updates = %#v", updates)
	}
}

func TestModel_LegacyFlowShortcutsDoNotToggleOrLaunch(t *testing.T) {
	for _, key := range []rune{'x', 'i'} {
		t.Run(string(key), func(t *testing.T) {
			addLaunchRan := false
			launchAgentRan := false
			startEmbeddedRan := false
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					addLaunchRan = true
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					launchAgentRan = true
					return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					startEmbeddedRan = true
					return &fakeEmbeddedTerminal{}, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			if cmd != nil {
				t.Fatalf("legacy Flow key %q returned command %T, want nil", key, cmd)
			}
			if got := m.ExpandedFlowID(); got != "" {
				t.Fatalf("legacy Flow key %q expanded flow %q, want collapsed", key, got)
			}
			if addLaunchRan || launchAgentRan || startEmbeddedRan {
				t.Fatalf("legacy Flow key %q launched: add=%v launch=%v embedded=%v", key, addLaunchRan, launchAgentRan, startEmbeddedRan)
			}
		})
	}
}

func TestModel_FlowPhasesAutoCollapseWhenSelectionChanges(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{
		flowWithPhaseDetails(),
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusPending, Phases: []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhasePending}}},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ExpandedFlowID() == "" {
		t.Fatal("expected selected flow to expand")
	}

	for range flowstore.OrderedPhases(flowWithPhaseDetails().Phases) {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.FlowSelected(); got != 1 {
		t.Fatalf("flow selection = %d, want second flow", got)
	}
	if m.ExpandedFlowID() != "" {
		t.Fatalf("expanded flow = %q, want collapsed after selecting another flow", m.ExpandedFlowID())
	}
}

func TestModel_YKeyCopiesSelectedFlowWorktreePath(t *testing.T) {
	var copied []string
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		Title:        "Copy flow path",
		Status:       flowstore.StatusInProgress,
		WorktreePath: "/dev/alpha-worktrees/flow-1",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected flow worktree path copy command")
	}
	_ = cmd()
	if got := copied[len(copied)-1]; got != "/dev/alpha-worktrees/flow-1" {
		t.Fatalf("copied flow worktree path = %q, want /dev/alpha-worktrees/flow-1", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected selected phase to copy parent Flow worktree path")
	}
	_ = cmd()
	if got := copied[len(copied)-1]; got != "/dev/alpha-worktrees/flow-1" {
		t.Fatalf("copied selected phase parent worktree path = %q, want /dev/alpha-worktrees/flow-1", got)
	}
}

func TestModel_YKeyDoesNothingWhenSelectedFlowWorktreePathIsBlank(t *testing.T) {
	copied := false
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(string) error {
			copied = true
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		Title:        "Blank flow path",
		Status:       flowstore.StatusInProgress,
		WorktreePath: "  ",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd != nil {
		t.Fatalf("blank Flow worktree path returned copy command %T, want nil", cmd)
	}
	if copied {
		t.Fatal("blank Flow worktree path should not copy to clipboard")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd != nil {
		t.Fatalf("blank parent Flow worktree path from phase row returned copy command %T, want nil", cmd)
	}
	if copied {
		t.Fatal("blank parent Flow worktree path should not copy to clipboard")
	}
}

func TestModel_CKeyCopiesSelectedFlowID(t *testing.T) {
	var copied []string
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Copy flow ID",
		Status: flowstore.StatusInProgress,
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("expected flow ID copy command")
	}
	_ = cmd()
	if got := copied[len(copied)-1]; got != "flow-1" {
		t.Fatalf("copied Flow ID = %q, want flow-1", got)
	}
}

func TestModel_CKeyCopiesParentFlowIDFromSelectedPhaseRow(t *testing.T) {
	var copied []string
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("expected selected phase to copy parent Flow ID")
	}
	_ = cmd()
	if got := copied[len(copied)-1]; got != "flow-1" {
		t.Fatalf("copied selected phase parent Flow ID = %q, want flow-1", got)
	}
}

func TestModel_CKeyCopiesSelectedActiveFlowID(t *testing.T) {
	var copied []string
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{{
		FlowID: "active-flow",
		Title:  "Copy active Flow ID",
		Status: flowstore.StatusInProgress,
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("expected active Flow ID copy command")
	}
	_ = cmd()
	if got := copied[len(copied)-1]; got != "active-flow" {
		t.Fatalf("copied active Flow ID = %q, want active-flow", got)
	}
}

func TestModel_CKeyCopiesParentActiveFlowIDFromSelectedPhaseRow(t *testing.T) {
	var copied []string
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	flow := flowWithPhaseDetails()
	flow.FlowID = "active-flow"
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("expected selected active Flow phase to copy parent Flow ID")
	}
	_ = cmd()
	if got := copied[len(copied)-1]; got != "active-flow" {
		t.Fatalf("copied selected active Flow phase parent Flow ID = %q, want active-flow", got)
	}
}

func TestModel_CKeyDoesNothingWhenSelectedFlowIDIsBlank(t *testing.T) {
	copied := false
	m := newTestModel(testRepos(), model.Options{
		CopyToClipboard: func(string) error {
			copied = true
			return nil
		},
	})

	m = flowsInRightPane(t, m, nil)
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd != nil {
		t.Fatalf("empty Flow pane returned copy command %T, want nil", cmd)
	}

	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID: "  ",
		Title:  "Blank flow ID",
		Status: flowstore.StatusInProgress,
	}})
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd != nil {
		t.Fatalf("blank Flow ID returned copy command %T, want nil", cmd)
	}

	m = enterActiveFlowsWithRecords(t, m, nil)
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd != nil {
		t.Fatalf("empty active Flow pane returned copy command %T, want nil", cmd)
	}

	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{{
		FlowID: "  ",
		Title:  "Blank active Flow ID",
		Status: flowstore.StatusInProgress,
	}})
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd != nil {
		t.Fatalf("blank active Flow ID returned copy command %T, want nil", cmd)
	}
	if copied {
		t.Fatal("blank Flow ID should not copy to clipboard")
	}
}

func TestModel_CKeyStillOpensCodeOutsideFlowSurfaces(t *testing.T) {
	var opened []string
	m := newTestModel(testRepos(), model.Options{
		OpenCode: func(path string) error {
			opened = append(opened, path)
			return nil
		},
	})
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 18})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{
		RepoPath: "/dev/alpha",
		Worktrees: []gitquery.Worktree{{
			Path:       "/dev/alpha",
			BranchName: "main",
		}},
	})

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd == nil {
		t.Fatal("expected non-flow c key to keep open-code command path")
	} else {
		_ = cmd()
	}
	if got := opened; !slices.Equal(got, []string{"/dev/alpha"}) {
		t.Fatalf("opened code paths = %#v, want /dev/alpha", got)
	}
}

func TestModel_PKeyOpensSelectedFlowPullRequest(t *testing.T) {
	const prURL = "https://github.com/approachcontrol/approach/pull/123"
	var opened []string
	m := newTestModel(testRepos(), model.Options{
		OpenURL: func(url string) error {
			opened = append(opened, url)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPullRequestTarget("flow-1", prURL)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("p on selected Flow with PR target should return open command")
	}
	if m.Mode() != ui.ModeFlows || m.FlowSelected() != 0 || m.SelectedFlowPhaseID() != "" || m.Overlay() != ui.OverlayNone {
		t.Fatalf("p changed Flow selection state: mode=%v selected=%d phase=%q overlay=%v", m.Mode(), m.FlowSelected(), m.SelectedFlowPhaseID(), m.Overlay())
	}
	msg, ok := cmd().(model.OpenURLResultMsg)
	if !ok {
		t.Fatalf("open PR command returned %T, want OpenURLResultMsg", msg)
	}
	if msg.Err != "" || msg.Label != "Opened PR #123 in browser" {
		t.Fatalf("open PR command returned %#v, want success label", msg)
	}
	if got := opened; !slices.Equal(got, []string{prURL}) {
		t.Fatalf("opened URLs = %#v, want %q", got, prURL)
	}
	m, _ = update(m, msg)
	if got := m.TransientError(); got != "Opened PR #123 in browser" {
		t.Fatalf("status = %q, want PR open success", got)
	}
}

func TestModel_PKeyOpensSelectedActiveFlowPullRequest(t *testing.T) {
	const prURL = "https://github.com/approachcontrol/approach/pull/123"
	var opened []string
	m := newTestModel(testRepos(), model.Options{
		OpenURL: func(url string) error {
			opened = append(opened, url)
			return nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flowWithPullRequestTarget("flow-1", prURL)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("p on selected Active Flow with PR target should return open command")
	}
	msg, ok := cmd().(model.OpenURLResultMsg)
	if !ok {
		t.Fatalf("open active Flow PR command returned %T, want OpenURLResultMsg", msg)
	}
	if msg.Err != "" || msg.Label != "Opened PR #123 in browser" {
		t.Fatalf("open active Flow PR command returned %#v, want success label", msg)
	}
	if got := opened; !slices.Equal(got, []string{prURL}) {
		t.Fatalf("opened URLs = %#v, want %q", got, prURL)
	}
}

func TestModel_IKeyOpensSelectedFlowIssue(t *testing.T) {
	const issueURL = "https://github.com/approachcontrol/approach/issues/123"
	var opened []string
	m := newTestModel(testRepos(), model.Options{
		OpenURL: func(url string) error {
			opened = append(opened, url)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithIssueTarget("flow-1", issueURL)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("i on selected Flow with issue target should return open command")
	}
	if m.Mode() != ui.ModeFlows || m.FlowSelected() != 0 || m.SelectedFlowPhaseID() != "" || m.Overlay() != ui.OverlayNone {
		t.Fatalf("i changed Flow selection state: mode=%v selected=%d phase=%q overlay=%v", m.Mode(), m.FlowSelected(), m.SelectedFlowPhaseID(), m.Overlay())
	}
	msg, ok := cmd().(model.OpenURLResultMsg)
	if !ok {
		t.Fatalf("open issue command returned %T, want OpenURLResultMsg", msg)
	}
	if msg.Err != "" || msg.Label != "Opened issue #123 in browser" {
		t.Fatalf("open issue command returned %#v, want success label", msg)
	}
	if got := opened; !slices.Equal(got, []string{issueURL}) {
		t.Fatalf("opened URLs = %#v, want %q", got, issueURL)
	}
}

func TestModel_IKeyOpensSelectedActiveFlowIssue(t *testing.T) {
	const issueURL = "https://github.com/approachcontrol/approach/issues/123"
	var opened []string
	m := newTestModel(testRepos(), model.Options{
		OpenURL: func(url string) error {
			opened = append(opened, url)
			return nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flowWithIssueTarget("flow-1", issueURL)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("i on selected Active Flow with issue target should return open command")
	}
	msg, ok := cmd().(model.OpenURLResultMsg)
	if !ok {
		t.Fatalf("open active Flow issue command returned %T, want OpenURLResultMsg", msg)
	}
	if msg.Err != "" || msg.Label != "Opened issue #123 in browser" {
		t.Fatalf("open active Flow issue command returned %#v, want success label", msg)
	}
	if got := opened; !slices.Equal(got, []string{issueURL}) {
		t.Fatalf("opened URLs = %#v, want %q", got, issueURL)
	}
}

func TestModel_IKeyFlowIssueGuards(t *testing.T) {
	const issueURL = "https://github.com/approachcontrol/approach/issues/123"
	valid := flowWithIssueTarget("flow-1", issueURL)
	incomplete := valid
	incomplete.Issue.URL = ""
	var terminalInputTerm *fakeEmbeddedTerminal

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, openURL func(string) error) model.Model
		check func(t *testing.T, m model.Model)
	}{
		{
			name: "no selected Flow",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				return flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), nil)
			},
		},
		{
			name: "incomplete issue metadata",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				return flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{incomplete})
			},
		},
		{
			name: "selected phase row",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{valid})
				return selectFlowPhaseByID(t, m, "implementation")
			},
		},
		{
			name: "Flow terminal input forwards i",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				fakeTerm := &fakeEmbeddedTerminal{state: "running"}
				terminalInputTerm = fakeTerm
				m := newTestModel(testRepos(), model.Options{
					AgentCommand: "codex",
					OpenURL:      openURL,
					AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
						return flowstore.FlowRecord{FlowID: update.FlowID}, nil
					},
					StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
						return fakeTerm, nil
					},
				})
				m = flowsInRightPane(t, m, []flowstore.FlowRecord{valid})
				m = selectFlowPhaseByID(t, m, "implementation")
				m, cmd := flowLaunchFromKey(t, m)
				if cmd == nil {
					t.Fatal("g should prepare an embedded Flow launch")
				}
				m, _ = update(m, cmd())
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
				return m
			},
			check: func(t *testing.T, m model.Model) {
				if terminalInputTerm == nil {
					t.Fatal("terminal input test did not capture fake terminal")
				}
				if !slices.Equal(terminalInputTerm.writes, []string{"i"}) {
					t.Fatalf("terminal input writes = %#v, want i", terminalInputTerm.writes)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opened []string
			m := tc.setup(t, func(url string) error {
				opened = append(opened, url)
				return nil
			})
			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			if cmd != nil {
				t.Fatalf("i returned command %T, want nil", cmd)
			}
			if len(opened) != 0 {
				t.Fatalf("opened URLs = %#v, want none", opened)
			}
			if tc.check != nil {
				tc.check(t, m)
			}
		})
	}
}

func TestModel_FlowFilterMatchesIssueMetadata(t *testing.T) {
	m := model.New(testRepos())
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		flowWithIssueTarget("flow-1", "https://github.com/approachcontrol/approach/issues/123"),
		{
			FlowID: "flow-2",
			Title:  "Unrelated flow",
			Status: flowstore.StatusInProgress,
			Branch: "flow/unrelated",
		},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "#123" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	view := m.View()
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("filtered flows = %#v, want only flow-1", got)
	}
	if !strings.Contains(view, "#123") {
		t.Fatalf("issue filter should keep matching Flow:\n%s", view)
	}
	if strings.Contains(view, "Unrelated flow") {
		t.Fatalf("issue filter should hide non-matching Flow:\n%s", view)
	}
}

func TestModel_PKeyOpenFlowPullRequestFailureSetsStatus(t *testing.T) {
	const prURL = "https://github.com/approachcontrol/approach/pull/123"
	m := newTestModel(testRepos(), model.Options{
		OpenURL: func(string) error {
			return errors.New("browser unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPullRequestTarget("flow-1", prURL)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("p on selected Flow with PR target should return open command")
	}
	msg, ok := cmd().(model.OpenURLResultMsg)
	if !ok {
		t.Fatalf("open PR command returned %T, want OpenURLResultMsg", msg)
	}
	if msg.Err != "browser unavailable" {
		t.Fatalf("open PR command error = %q, want browser unavailable", msg.Err)
	}
	m, _ = update(m, msg)
	if got := m.TransientError(); got != "browser unavailable" {
		t.Fatalf("status = %q, want browser failure", got)
	}
}

func TestModel_UppercasePDoesNotOpenSelectedFlowPullRequest(t *testing.T) {
	const prURL = "https://github.com/approachcontrol/approach/pull/123"
	var opened []string
	m := newTestModel(testRepos(), model.Options{
		OpenURL: func(url string) error {
			opened = append(opened, url)
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPullRequestTarget("flow-1", prURL)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	if cmd != nil {
		t.Fatalf("uppercase P on selected Flow with PR target returned command %T, want nil", cmd)
	}
	if len(opened) != 0 {
		t.Fatalf("opened URLs = %#v, want none", opened)
	}
}

func TestModel_PKeyFlowPullRequestGuards(t *testing.T) {
	const prURL = "https://github.com/approachcontrol/approach/pull/123"
	valid := flowWithPullRequestTarget("flow-1", prURL)
	incomplete := valid
	incomplete.PR.BaseBranch = ""
	var terminalInputTerm *fakeEmbeddedTerminal

	tests := []struct {
		name  string
		setup func(t *testing.T, openURL func(string) error) model.Model
		check func(t *testing.T, m model.Model)
	}{
		{
			name: "no selected Flow",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				return flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), nil)
			},
		},
		{
			name: "incomplete PR metadata",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				return flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{incomplete})
			},
		},
		{
			name: "selected phase row",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{valid})
				return selectFlowPhaseByID(t, m, "implementation")
			},
			check: func(t *testing.T, m model.Model) {
				if got := m.SelectedFlowPhaseID(); got != "implementation" {
					t.Fatalf("selected phase = %q, want implementation", got)
				}
			},
		},
		{
			name: "active search",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{valid})
				return model.SetSearchActiveForTest(m, true)
			},
			check: func(t *testing.T, m model.Model) {
				if !m.SearchActive() {
					t.Fatal("search should remain active")
				}
			},
		},
		{
			name: "open modal",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{valid})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
				if m.Overlay() == ui.OverlayNone {
					t.Fatal("expected new Flow modal to open")
				}
				return m
			},
			check: func(t *testing.T, m model.Model) {
				if m.Overlay() == ui.OverlayNone {
					t.Fatal("modal should remain open")
				}
			},
		},
		{
			name: "Flow terminal focus forwards p",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				fakeTerm := &fakeEmbeddedTerminal{state: "running"}
				m := newTestModel(testRepos(), model.Options{
					AgentCommand: "codex",
					OpenURL:      openURL,
					AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
						return flowstore.FlowRecord{FlowID: update.FlowID}, nil
					},
					StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
						return fakeTerm, nil
					},
				})
				m = flowsInRightPane(t, m, []flowstore.FlowRecord{valid})
				m = selectFlowPhaseByID(t, m, "implementation")
				m, cmd := flowLaunchFromKey(t, m)
				if cmd == nil {
					t.Fatal("g should prepare an embedded Flow launch")
				}
				m, _ = update(m, cmd())
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				return m
			},
		},
		{
			name: "Flow terminal input forwards p",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				fakeTerm := &fakeEmbeddedTerminal{state: "running"}
				terminalInputTerm = fakeTerm
				m := newTestModel(testRepos(), model.Options{
					AgentCommand: "codex",
					OpenURL:      openURL,
					AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
						return flowstore.FlowRecord{FlowID: update.FlowID}, nil
					},
					StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
						return fakeTerm, nil
					},
				})
				m = flowsInRightPane(t, m, []flowstore.FlowRecord{valid})
				m = selectFlowPhaseByID(t, m, "implementation")
				m, cmd := flowLaunchFromKey(t, m)
				if cmd == nil {
					t.Fatal("g should prepare an embedded Flow launch")
				}
				m, _ = update(m, cmd())
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
				return m
			},
			check: func(t *testing.T, m model.Model) {
				if terminalInputTerm == nil {
					t.Fatal("terminal input test did not capture fake terminal")
				}
				if !slices.Equal(terminalInputTerm.writes, []string{"p"}) {
					t.Fatalf("terminal input writes = %#v, want p", terminalInputTerm.writes)
				}
			},
		},
		{
			name: "Flow terminal command prefix",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				fakeTerm := &fakeEmbeddedTerminal{state: "running"}
				m := newTestModel(testRepos(), model.Options{
					AgentCommand: "codex",
					OpenURL:      openURL,
					AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
						return flowstore.FlowRecord{FlowID: update.FlowID}, nil
					},
					StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
						return fakeTerm, nil
					},
				})
				m = flowsInRightPane(t, m, []flowstore.FlowRecord{valid})
				m = selectFlowPhaseByID(t, m, "implementation")
				m, cmd := flowLaunchFromKey(t, m)
				if cmd == nil {
					t.Fatal("g should prepare an embedded Flow launch")
				}
				m, _ = update(m, cmd())
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
				return m
			},
		},
		{
			name: "active Flow incomplete PR",
			setup: func(t *testing.T, openURL func(string) error) model.Model {
				return enterActiveFlowsWithRecords(t, newTestModel(testRepos(), model.Options{OpenURL: openURL}), []flowstore.FlowRecord{incomplete})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened []string
			m := tt.setup(t, func(url string) error {
				opened = append(opened, url)
				return nil
			})
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
			_ = cmd
			if len(opened) != 0 {
				t.Fatalf("opened URLs = %#v, want none", opened)
			}
			if m.Overlay() == ui.OverlayConfirm {
				t.Fatal("p in Flow surfaces should not open destructive confirmation")
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

func flowWithPullRequestTarget(flowID, prURL string) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       flowID,
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/" + flowID,
		Title:        "Flow with PR",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/add-pr-shortcut",
		Headless:     true,
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     123,
			URL:        prURL,
			HeadBranch: "flow/add-pr-shortcut",
			BaseBranch: "main",
			Status:     "open",
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}
}

func flowWithIssueTarget(flowID, issueURL string) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       flowID,
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/" + flowID,
		Title:        "Flow with issue",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/add-issue-shortcut",
		Headless:     true,
		Issue: flowstore.Issue{
			Provider: "github",
			Number:   123,
			URL:      issueURL,
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}
}

func TestModel_ExpandedFlowArrowKeysSelectPhaseRows(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.SelectedFlowPhaseID(); got != "" {
		t.Fatalf("expanded flow should keep flow row selected, got phase %q", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.FlowSelected(); got != 0 {
		t.Fatalf("flow selection changed while entering phases: %d", got)
	}
	if got := m.SelectedFlowPhaseID(); got != "plan" {
		t.Fatalf("selected flow phase = %q, want plan", got)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "   >  completed") {
		t.Fatalf("selected phase row should be visually marked:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedFlowPhaseID(); got != "plan-review" {
		t.Fatalf("selected flow phase = %q, want plan-review", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.SelectedFlowPhaseID(); got != "" {
		t.Fatalf("up from first phase should return to flow row, got phase %q", got)
	}
}

func TestModel_SelectedFlowPhaseClearsWhenFlowSelectionChanges(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{
		flowWithPhaseDetails(),
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusPending, Phases: []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhasePending}}},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedFlowPhaseID(); got == "" {
		t.Fatal("expected selected flow phase before changing flows")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.FlowSelected(); got != 1 {
		t.Fatalf("flow selection = %d, want second flow", got)
	}
	if got := m.SelectedFlowPhaseID(); got != "" {
		t.Fatalf("selected flow phase after changing flows = %q, want cleared", got)
	}
	if got := m.ExpandedFlowID(); got != "" {
		t.Fatalf("expanded flow after changing flows = %q, want collapsed", got)
	}
}

func TestModel_SelectedFlowPhasePersistsWhenFocusLeavesBottomPane(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyBackspace},
	} {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
		if got := m.SelectedFlowPhaseID(); got == "" {
			t.Fatal("expected selected flow phase before leaving right pane")
		}

		m, _ = update(m, key)
		if got := m.SelectedFlowPhaseID(); got != "plan" {
			t.Fatalf("%s left selected flow phase = %q, want preserved", key.String(), got)
		}
		if m.ActivePane() != ui.PaneTop {
			t.Fatalf("%s active pane = %d, want top", key.String(), m.ActivePane())
		}
	}
}

func TestModel_ExpandedFlowAtViewportBottomScrollsPhasesIntoView(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.FlowID = "flow-6"
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{
		{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "First flow", Status: flowstore.StatusPending},
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusPending},
		{FlowID: "flow-3", RepoPath: "/dev/alpha", Title: "Third flow", Status: flowstore.StatusPending},
		{FlowID: "flow-4", RepoPath: "/dev/alpha", Title: "Fourth flow", Status: flowstore.StatusPending},
		{FlowID: "flow-5", RepoPath: "/dev/alpha", Title: "Fifth flow", Status: flowstore.StatusPending},
		flow,
	})
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 12 + ui.TerminalChipRows})
	for i := 0; i < 5; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.FlowScroll(); got != 2 {
		t.Fatalf("flow scroll = %d, want 2", got)
	}
	view := m.View()
	if !strings.Contains(view, "implementation:ready") {
		t.Fatalf("expanded flow should scroll phase detail rows into view:\n%s", view)
	}
}

func TestModel_ExpandedSingleFlowScrollsWithinManyPhases(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.Phases = append(flow.Phases,
		flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop", Status: flowstore.PhasePending},
		flowstore.FlowPhase{PhaseID: "pr-creation", Title: "PR Creation", Status: flowstore.PhasePending},
		flowstore.FlowPhase{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhasePending},
	)
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 10 + ui.TerminalChipRows})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	for i := 0; i < 4; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	if got := m.FlowSelected(); got != 0 {
		t.Fatalf("single expanded flow should remain selected, got %d", got)
	}
	if got := m.FlowScroll(); got != 0 {
		t.Fatalf("flow scroll = %d, want 0", got)
	}
	view := m.View()
	if !strings.Contains(view, "review-loop:pending") {
		t.Fatalf("expanded flow should scroll within phase detail rows:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if got := m.FlowScroll(); got != 2 {
		t.Fatalf("flow scroll should stay at bottom after extra down, got %d", got)
	}
	view = m.View()
	if !strings.Contains(view, "autoreview:pending") {
		t.Fatalf("expanded flow should stay scrolled to the last phase:\n%s", view)
	}
}

func TestModel_SelectedFlowPhaseStaysVisibleAfterResize(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.Phases = append(flow.Phases,
		flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop", Status: flowstore.PhasePending},
		flowstore.FlowPhase{PhaseID: "pr-creation", Title: "PR Creation", Status: flowstore.PhasePending},
		flowstore.FlowPhase{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhasePending},
	)
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 6; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := m.SelectedFlowPhaseID(); got != "autoreview" {
		t.Fatalf("selected flow phase = %q, want autoreview", got)
	}

	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 10})

	view := m.View()
	if !strings.Contains(view, "autoreview:pending") {
		t.Fatalf("selected flow phase should stay visible after resize:\n%s", view)
	}
}

func TestModel_ChangingRepoRefetchesFlowsMode(t *testing.T) {
	var filters []flowstore.FlowFilter
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{{FlowID: filepath.Base(filter.RepoPath), RepoPath: filter.RepoPath, Title: "T", Status: flowstore.StatusPending}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModePlans)
	m, cmd := switchTestMode(m, ui.ModeFlows)
	if cmd == nil {
		t.Fatal("expected initial flows fetch")
	}
	m, _ = update(m, flowFetchMsgFromCommand(t, cmd))
	if got := m.Flows(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Flows() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil {
		t.Fatalf("expected nil cmd focusing repo pane, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected flows refetch after repo change")
	}
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("expected flows cleared before refetch, got %#v", got)
	}
	m, _ = update(m, flowFetchMsgFromCommand(t, cmd))
	if got := m.Flows(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("refetched Flows() = %#v", got)
	}
	if len(filters) != 2 || filters[0].RepoPath != "/dev/alpha" || filters[1].RepoPath != "/dev/bravo" {
		t.Fatalf("flow filters = %#v", filters)
	}
}

func TestModel_StaleFlowResultIgnored(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{
		{FlowID: "stale", RepoPath: "/dev/alpha", Title: "T", Status: flowstore.StatusPending},
	}, ListRequest: 999999})
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("stale flow result should be ignored, got %#v", got)
	}
}

func TestModel_FlowListErrorShowsStatus(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("flows unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModePlans)
	m, cmd := switchTestMode(m, ui.ModeFlows)
	if cmd == nil {
		t.Fatal("expected flows fetch command")
	}
	m, _ = update(m, flowFetchMsgFromCommand(t, cmd))
	if got := m.View(); !strings.Contains(got, "failed to load flows") || !strings.Contains(got, "Could not load flows") {
		t.Fatalf("expected flow load error in view:\n%s", got)
	}
}

func TestModel_FlowDeleteRequiresDestructiveMode(t *testing.T) {
	deleted := false
	m := newTestModel(testRepos(), model.Options{
		DeleteFlow: func(string) error {
			deleted = true
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Delete me",
		Status:   flowstore.StatusPending,
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if cmd != nil {
		t.Fatalf("d without destructive mode returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("d without destructive mode opened overlay %d", m.Overlay())
	}
	if deleted {
		t.Fatal("DeleteFlow should not run while destructive mode is disabled")
	}
}

func TestModel_ActiveFlowDeleteUsesVisibleFlowOverUnderlyingStash(t *testing.T) {
	flow := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Delete visible Flow",
		Status:   flowstore.StatusPending,
	}
	m := newTestModel(testRepos(), model.Options{})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{
		RepoPath: "/dev/alpha",
		Stashes: []gitquery.Stash{{
			Index:   0,
			Date:    "2026-06-14 12:00:00 -0400",
			Message: "hidden stash",
		}},
		ListRequest: m.ListRequest(ui.ModeStashes),
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil {
		t.Fatalf("opening active Flow delete confirm returned command %T, want nil", cmd)
	}
	prompt := m.ConfirmPrompt()
	if !strings.Contains(prompt, "Delete Flow Delete visible Flow (flow-1)") {
		t.Fatalf("active Flow delete prompt = %q, want visible Flow delete confirmation", prompt)
	}
	if strings.Contains(prompt, "hidden stash") {
		t.Fatalf("active Flow delete should not target hidden stash: %q", prompt)
	}
}

func TestModel_ActiveFlowActionFailureShowsCrossRepoErrorAndRefreshesGlobally(t *testing.T) {
	bravoFlow := flowstore.FlowRecord{
		FlowID:   "bravo-flow",
		RepoPath: "/dev/bravo",
		Title:    "Bravo Flow",
		Status:   flowstore.StatusPending,
	}
	var filters []flowstore.FlowFilter
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{bravoFlow}, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{bravoFlow})

	m, cmd := update(m, model.ActionFailedMsg{RepoPath: "/dev/bravo", Err: "failed to launch bravo Flow"})
	if cmd == nil {
		t.Fatal("active Flow action failure should refresh global active flows")
	}
	if got := m.TransientError(); !strings.Contains(got, "failed to launch bravo Flow") {
		t.Fatalf("active Flow cross-repo action failure status = %q, want error text", got)
	}
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	if len(filters) != 1 || filters[0].RepoPath != "" {
		t.Fatalf("active Flow action failure filters = %#v, want one global fetch", filters)
	}
}

func TestModel_ActiveFlowDeleteWithNoVisibleFlowIgnoresUnderlyingStash(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{})
	m = flowsInRightPane(t, m, nil)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{
		RepoPath: "/dev/alpha",
		Stashes: []gitquery.Stash{{
			Index:   0,
			Date:    "2026-06-14 12:00:00 -0400",
			Message: "hidden stash",
		}},
		ListRequest: m.ListRequest(ui.ModeStashes),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil {
		t.Fatalf("d with no visible active Flow returned command %T, want nil", cmd)
	}
	if prompt := m.ConfirmPrompt(); prompt != "" {
		t.Fatalf("d with no visible active Flow opened hidden-pane confirmation %q", prompt)
	}
}

func TestModel_FlowDeleteCancelDoesNotCallDeleteAdapter(t *testing.T) {
	deleted := false
	m := newTestModel(testRepos(), model.Options{
		DeleteFlow: func(string) error {
			deleted = true
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Delete me",
		Status:   flowstore.StatusPending,
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil {
		t.Fatalf("opening Flow delete confirm returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected Flow delete confirm overlay, got %d", m.Overlay())
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("canceling Flow delete returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("canceling Flow delete left overlay %d", m.Overlay())
	}
	if deleted {
		t.Fatal("DeleteFlow should not run when delete confirmation is canceled")
	}
}

func TestModel_FlowDeleteConfirmDeletesCapturedFlowAndRefreshes(t *testing.T) {
	deleted := []string{}
	listCalls := 0
	m := newTestModel(testRepos(), model.Options{
		DeleteFlow: func(flowID string) error {
			deleted = append(deleted, flowID)
			return nil
		},
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return []flowstore.FlowRecord{{
				FlowID:   "flow-2",
				RepoPath: filter.RepoPath,
				Title:    "Keep me",
				Status:   flowstore.StatusPending,
			}}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Delete me", Status: flowstore.StatusPending},
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Keep me", Status: flowstore.StatusPending},
	})
	m = expandSelectedFlowWithEnter(t, m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd != nil {
		t.Fatalf("opening Flow delete confirm returned command %T, want nil", cmd)
	}
	prompt := m.ConfirmPrompt()
	for _, want := range []string{"Delete Flow Delete me (flow-1)", "Flow data only", "worktrees/code stay"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("delete prompt %q missing %q", prompt, want)
		}
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirming Flow delete should return command")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(model.FlowDeletedMsg)
	if !ok {
		t.Fatalf("delete command returned %T, want FlowDeletedMsg", rawMsg)
	}
	if msg.RepoPath != "/dev/alpha" || msg.FlowID != "flow-1" || msg.Title != "Delete me" {
		t.Fatalf("FlowDeletedMsg = %#v, want captured repo/flow/title", msg)
	}
	if len(deleted) != 1 || deleted[0] != "flow-1" {
		t.Fatalf("deleted flow ids = %#v, want [flow-1]", deleted)
	}

	m, cmd = update(m, msg)
	if cmd == nil {
		t.Fatal("FlowDeletedMsg should refresh flows")
	}
	if m.ExpandedFlowID() != "" || m.SelectedFlowPhaseID() != "" {
		t.Fatalf("deleted expanded Flow state = expanded %q phase %q, want cleared", m.ExpandedFlowID(), m.SelectedFlowPhaseID())
	}
	m, _ = update(m, cmd())
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-2" {
		t.Fatalf("flows after delete refresh = %#v, want only flow-2", got)
	}
	if listCalls != 1 {
		t.Fatalf("ListFlows calls = %d, want 1 refresh", listCalls)
	}
}

func TestModel_FlowDeleteIgnoresSelectedPhaseRows(t *testing.T) {
	deleted := false
	m := newTestModel(testRepos(), model.Options{
		DeleteFlow: func(string) error {
			deleted = true
			return nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m = selectFlowPhaseByID(t, m, "plan")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if cmd != nil {
		t.Fatalf("d on selected Flow phase returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("d on selected Flow phase opened overlay %d", m.Overlay())
	}
	if deleted {
		t.Fatal("DeleteFlow should not run for selected phase rows")
	}
}

func TestModel_FlowDeleteFailureHandling(t *testing.T) {
	t.Run("not found refreshes stale list", func(t *testing.T) {
		root := t.TempDir()
		store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		m := newTestModel(testRepos(), model.Options{
			DeleteFlow: func(string) error {
				return store.Delete("missing-flow")
			},
			ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
				return nil, nil
			},
		})
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			RepoPath: "/dev/alpha",
			Title:    "Already gone",
			Status:   flowstore.StatusPending,
		}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("confirming Flow delete should return command")
		}
		rawMsg := cmd()
		msg, ok := rawMsg.(model.FlowDeleteFailedMsg)
		if !ok {
			t.Fatalf("delete command returned %T, want FlowDeleteFailedMsg", rawMsg)
		}
		if !msg.NotFound || msg.FlowID != "flow-1" || msg.RepoPath != "/dev/alpha" {
			t.Fatalf("FlowDeleteFailedMsg = %#v, want not-found for captured Flow", msg)
		}

		m, cmd = update(m, msg)
		if cmd == nil {
			t.Fatal("not-found Flow delete should refresh flows")
		}
		if !strings.Contains(m.TransientError(), "Flow already deleted") {
			t.Fatalf("status after not-found delete = %q, want stale-list warning", m.TransientError())
		}
		for _, refreshMsg := range runLeadingBatchCmdWithCount(t, cmd, 2) {
			m, _ = update(m, refreshMsg)
		}
		if got := m.Flows(); len(got) != 0 {
			t.Fatalf("flows after not-found refresh = %#v, want empty", got)
		}
	})

	t.Run("other errors do not refresh as completed work", func(t *testing.T) {
		m := newTestModel(testRepos(), model.Options{
			ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
				t.Fatal("ListFlows should not run for non-not-found delete failure")
				return nil, nil
			},
		})
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			RepoPath: "/dev/alpha",
			Title:    "Delete me",
			Status:   flowstore.StatusPending,
		}})

		m, cmd := update(m, model.FlowDeleteFailedMsg{
			RepoPath: "/dev/alpha",
			FlowID:   "flow-1",
			Title:    "Delete me",
			Err:      "disk full",
		})

		if cmd == nil {
			t.Fatal("expected status expiry command for non-not-found FlowDeleteFailedMsg")
		}
		if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
			t.Fatalf("flows after failed delete = %#v, want unchanged", got)
		}
		if !strings.Contains(m.TransientError(), "disk full") {
			t.Fatalf("status after failed delete = %q, want error text", m.TransientError())
		}
	})

	t.Run("stale repo result is ignored", func(t *testing.T) {
		m := flowsInRightPane(t, newTestModel(testRepos(), model.Options{}), []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			RepoPath: "/dev/alpha",
			Title:    "Delete me",
			Status:   flowstore.StatusPending,
		}})

		m, cmd := update(m, model.FlowDeletedMsg{RepoPath: "/dev/bravo", FlowID: "flow-1", Title: "Delete me"})

		if cmd != nil {
			t.Fatalf("stale FlowDeletedMsg returned command %T, want nil", cmd)
		}
		if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
			t.Fatalf("flows after stale delete result = %#v, want unchanged", got)
		}
	})
}

func TestModel_FlowDeleteDoesNotTerminateEmbeddedTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"flow output"}, state: "running"}
	deleted := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		DeleteFlow: func(string) error {
			deleted = true
			return nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	for i := 0; i < 10 && m.SelectedFlowPhaseID() != ""; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.SelectedFlowPhaseID() != "" {
		t.Fatalf("selected Flow phase = %q, want top-level Flow row", m.SelectedFlowPhaseID())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirming Flow delete should return command")
	}
	msg := cmd()
	m, cmd = update(m, msg)
	if cmd == nil {
		t.Fatal("Flow delete result should refresh flows")
	}
	m, _ = update(m, cmd())

	if !deleted {
		t.Fatal("DeleteFlow should run")
	}
	if fakeTerm.State() != "running" {
		t.Fatalf("embedded terminal state = %q, want running", fakeTerm.State())
	}
	if !strings.Contains(m.View(), "flow output") {
		t.Fatalf("active Flow terminal output disappeared after Flow delete:\n%s", m.View())
	}
}

func TestModel_FlowSearchIncludesPhasesAndMetadata(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		Title:        "Add Flow mode",
		Status:       flowstore.StatusInProgress,
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-mode",
		Branch:       "flow/add-flow-mode",
		PR:           flowstore.PullRequest{URL: "https://github.com/approachcontrol/approach/pull/123"},
		UpdatedAt:    time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady},
		},
	}}, ListRequest: m.ListRequest(ui.ModeFlows)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Review")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("flow search by phase title should keep row, got %#v", got)
	}
}

func TestModel_FlowSearchIncludesMergeMetadata(t *testing.T) {
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	m := newTestModel(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Merged flow",
		Status: flowstore.StatusMerged,
		Merge: flowstore.Merge{
			Status:   flowstore.MergeMerged,
			Commit:   "0123456789abcdef",
			MergedAt: &mergedAt,
		},
	}}, ListRequest: m.ListRequest(ui.ModeFlows)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0123456789abcdef")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("flow search by merge commit should keep row, got %#v", got)
	}
}

func TestModel_OKeyOnFlowOpensLinkedPlanText(t *testing.T) {
	var paged []string
	m := newTestModel(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		ReadPlan: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("ReadPlan called with %q", planID)
			}
			return "# Flow plan\n\nfull body\n", nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:    "flow-1",
		RepoPath:  "/dev/alpha",
		Title:     "Linked flow",
		Status:    flowstore.StatusInProgress,
		PlanID:    "plan-1",
		PlanPath:  "/state/approach/sessions/v1/plans/plan-1/plan.md",
		UpdatedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("flows-mode o should return a plan read command for linked plan")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	_, cmd = update(m, cmd())
	if cmd == nil {
		t.Fatal("expected linked flow plan pager command")
	}
	for _, want := range []string{"# Flow plan", "full body"} {
		if len(paged) != 1 || !strings.Contains(paged[0], want) {
			t.Fatalf("paged linked flow plan missing %q: %#v", want, paged)
		}
	}
}

func TestModel_OKeyOnFlowWithoutPlanShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Unlinked flow",
		Status:   flowstore.StatusPending,
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for unlinked flow")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	if got := m.TransientError(); !strings.Contains(got, "Flow has no linked plan") {
		t.Fatalf("status = %q, want missing linked plan message", got)
	}
}

func TestModel_RKeyOnSelectedFlowPhaseResumesLatestSession(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:         "codex",
		CodexModel:           "gpt-5.5",
		CodexReasoningEffort: "high",
		SessionStateRoot:     "/state/approach",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI resume should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume sessions",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-sessions",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-sessions",
		Commit:       "abc123",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-old", "launch-new"},
			Sessions: []flowstore.Session{
				{Provider: "claude", SessionID: "claude-old", LaunchID: "launch-old", Status: "ended", StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
				{Provider: "codex", SessionID: "codex-new", LaunchID: "launch-new", Status: "ended", StartedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected selected Flow phase resume command")
	}
	if launchUpdate.FlowID != "" || started.Command != "" {
		t.Fatalf("resume persistence or terminal start ran inside Update: update=%#v started=%#v", launchUpdate, started)
	}
	// The key press now buys an authoritative read first; nothing is written
	// until the read has re-validated the phase and its session.
	read := cmd()
	if launchUpdate.FlowID != "" || started.Command != "" {
		t.Fatalf("authoritative read did work: update=%#v started=%#v", launchUpdate, started)
	}
	m, persistCmd := update(m, read)
	if persistCmd == nil {
		t.Fatal("expected resume persistence command after the authoritative read")
	}
	persisted := persistCmd()
	if launchUpdate.FlowID != "flow-1" || started.Command != "" {
		t.Fatalf("resume command sequencing = update %#v started %#v; want persistence only", launchUpdate, started)
	}
	m, _ = update(m, persisted)
	if started.Command != "codex" ||
		started.ResumeSessionID != "codex-new" ||
		started.FlowID != "flow-1" ||
		started.FlowPhaseID != "implementation" ||
		started.RepoPath != "/dev/alpha" ||
		started.WorktreePath != "/dev/alpha-worktrees/flow-resume-sessions" ||
		started.WorkingDir != "/dev/alpha-worktrees/flow-resume-sessions" ||
		started.Branch != "flow/resume-sessions" ||
		started.Commit != "abc123" ||
		started.SessionStateRoot != "/state/approach" ||
		started.Model != "" ||
		started.ReasoningEffort != "" ||
		!started.Embedded ||
		started.Headless ||
		!started.FlowLaunchTracked {
		t.Fatalf("unexpected Flow phase embedded resume context: %#v", started)
	}
	if started.LaunchID == "" || started.LaunchID == "launch-new" {
		t.Fatalf("expected Flow phase resume to use a fresh launch id, got %#v", started.LaunchID)
	}
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID != started.LaunchID {
		t.Fatalf("launch update = %#v, want fresh resume launch id %#v", launchUpdate, started.LaunchID)
	}
	if !launchUpdate.Resume {
		t.Fatalf("launch update = %#v, want resume launch so terminal phases keep their status", launchUpdate)
	}
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("Flow phase resume should not prefill embedded terminal, got writes %#v", fakeTerm.writes)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "z" {
		t.Fatalf("interactive Flow phase resume should focus terminal input and forward z: %#v", fakeTerm.writes)
	}
}

func TestModel_RKeyOnSelectedFlowPhaseDoesNotWaitForSQLiteWriterInUpdate(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "flow-resume-lock",
		Title:        "Resume while locked",
		Instructions: "prove resume persistence stays outside Update",
		RepoPath:     "/dev/alpha",
		WorktreePath: filepath.Join(root, "worktree"),
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "review-loop",
			Title:     "Review loop",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{{
				Provider:  "codex",
				SessionID: "codex-session",
				LaunchID:  "launch-old",
				Status:    "ended",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	enteredPersistence := make(chan flowstore.PhaseLaunchUpdate, 1)
	terminalStarts := make(chan actions.AgentLaunchContext, 2)
	m := newTestModel(testRepos(), model.Options{
		SessionStateRoot: root,
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			enteredPersistence <- update
			return store.AddPhaseLaunchID(update)
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			terminalStarts <- ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
	m = selectFlowPhaseByID(t, m, "review-loop")

	contender, err := sql.Open("sqlite", "file:"+filepath.Join(root, "approach.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open contending SQLite handle: %v", err)
	}
	lockHeld := true
	heldTx, err := contender.Begin()
	if err != nil {
		t.Fatalf("begin contending SQLite writer: %v", err)
	}
	var updateDone <-chan struct{}
	var persistenceDone <-chan tea.Msg
	t.Cleanup(func() {
		if lockHeld {
			_ = heldTx.Rollback()
		}
		_ = contender.Close()
		if updateDone != nil {
			select {
			case <-updateDone:
			case <-time.After(10 * time.Second):
			}
		}
		if persistenceDone != nil {
			select {
			case <-persistenceDone:
			case <-time.After(10 * time.Second):
			}
		}
	})
	type updateResult struct {
		model model.Model
		cmd   tea.Cmd
	}
	updateResults := make(chan updateResult, 1)
	updateFinished := make(chan struct{})
	updateDone = updateFinished
	go func() {
		defer close(updateFinished)
		next, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		updateResults <- updateResult{model: next, cmd: cmd}
	}()

	var result updateResult
	select {
	case call := <-enteredPersistence:
		t.Fatalf("AddPhaseLaunchID entered before Update returned: %#v", call)
	case result = <-updateResults:
	case <-time.After(3 * time.Second):
		t.Fatal("Update did not return while the Flow lock was held")
	}
	if result.cmd == nil {
		t.Fatal("resume Update returned nil authoritative read command")
	}
	// The read hop reaches the Flow read seam, never the writer, so it settles
	// even while the contending transaction is held.
	readEvent := result.cmd()
	select {
	case call := <-enteredPersistence:
		t.Fatalf("AddPhaseLaunchID entered during the authoritative read: %#v", call)
	default:
	}
	resumed, persistCmd := update(result.model, readEvent)
	if persistCmd == nil {
		t.Fatal("the authoritative read returned no persistence command")
	}
	select {
	case call := <-enteredPersistence:
		t.Fatalf("AddPhaseLaunchID entered before the returned command ran: %#v", call)
	default:
	}
	before, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() before persistence error = %v", err)
	}
	if got := before.Phases[0].LaunchIDs; !slices.Equal(got, []string{"launch-old"}) {
		t.Fatalf("launch ids before command = %#v, want unchanged", got)
	}
	if len(terminalStarts) != 0 {
		t.Fatal("terminal started before resume persistence")
	}

	persistenceResults := make(chan tea.Msg, 1)
	persistenceDone = persistenceResults
	go func() { persistenceResults <- persistCmd() }()
	var launchUpdate flowstore.PhaseLaunchUpdate
	select {
	case launchUpdate = <-enteredPersistence:
	case <-time.After(3 * time.Second):
		t.Fatal("returned command did not enter AddPhaseLaunchID")
	}
	whileLocked, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() while locked error = %v", err)
	}
	if got := whileLocked.Phases[0].LaunchIDs; !slices.Equal(got, []string{"launch-old"}) {
		t.Fatalf("launch ids while command waits = %#v, want unchanged", got)
	}
	select {
	case msg := <-persistenceResults:
		t.Fatalf("persistence command returned while lock was held: %#v", msg)
	default:
	}
	if len(terminalStarts) != 0 {
		t.Fatal("terminal started while resume persistence waited for the lock")
	}

	if err := heldTx.Rollback(); err != nil {
		t.Fatalf("release contending SQLite writer: %v", err)
	}
	lockHeld = false
	var persisted tea.Msg
	select {
	case persisted = <-persistenceResults:
		persistenceDone = nil
	case <-time.After(10 * time.Second):
		t.Fatal("resume persistence did not finish after releasing the lock")
	}
	resumed, _ = update(resumed, persisted)
	_ = resumed
	if len(terminalStarts) != 1 {
		t.Fatalf("terminal starts after persistence = %d, want 1", len(terminalStarts))
	}
	started := <-terminalStarts
	if started.LaunchID == "" || started.LaunchID != launchUpdate.LaunchID {
		t.Fatalf("started launch id = %q, persistence update = %#v", started.LaunchID, launchUpdate)
	}
	updated, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() after persistence error = %v", err)
	}
	if got := updated.Phases[0].LaunchIDs; len(got) != 2 || got[0] != "launch-old" || got[1] != started.LaunchID {
		t.Fatalf("launch ids after persistence = %#v, want one new resume id", got)
	}
}

func TestModel_RKeyOnSelectedFlowPhaseResumeIsSingleShotBeforePersistenceCompletes(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/resume-single-shot",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "Review-Loop",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{{
				Provider: "codex", SessionID: "codex-session", LaunchID: "launch-old", Status: "ended",
			}},
		}},
	}
	var launchUpdates []flowstore.PhaseLaunchUpdate
	var terminalStarts []actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdates = append(launchUpdates, update)
			persisted := record
			persisted.Phases[0].LaunchIDs = append(slices.Clone(record.Phases[0].LaunchIDs), update.LaunchID)
			return persisted, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			terminalStarts = append(terminalStarts, ctx)
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
	m = selectFlowPhaseByID(t, m, "Review-Loop")

	pending, firstCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if firstCmd == nil {
		t.Fatal("first resume should return a persistence command")
	}
	pendingAfterDuplicate, duplicateCmd := update(pending, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if duplicateCmd != nil {
		t.Fatal("second resume for the same pending target should not enqueue a command")
	}
	if len(launchUpdates) != 0 || len(terminalStarts) != 0 {
		t.Fatalf("work ran before persistence command: updates=%#v terminals=%#v", launchUpdates, terminalStarts)
	}

	read := firstCmd()
	if len(launchUpdates) != 0 || len(terminalStarts) != 0 {
		t.Fatalf("authoritative read did work: updates=%#v terminals=%#v", launchUpdates, terminalStarts)
	}
	pendingAfterDuplicate, persistCmd := update(pendingAfterDuplicate, read)
	if persistCmd == nil {
		t.Fatal("the authoritative read should hand off to persistence")
	}
	persisted := persistCmd()
	if len(launchUpdates) != 1 || launchUpdates[0].PhaseID != "Review-Loop" || launchUpdates[0].LaunchID == "" {
		t.Fatalf("launch updates = %#v, want one normalized-target request identity", launchUpdates)
	}
	started, _ := update(pendingAfterDuplicate, persisted)
	if len(terminalStarts) != 1 || terminalStarts[0].LaunchID != launchUpdates[0].LaunchID {
		t.Fatalf("terminal starts = %#v, want one matching persisted resume", terminalStarts)
	}

	statusBeforeReplay := started.TransientError()
	replayed, replayCmd := update(started, persisted)
	if replayCmd != nil || len(terminalStarts) != 1 || replayed.TransientError() != statusBeforeReplay {
		t.Fatalf("replayed success changed state: cmd=%T terminals=%#v status=%q", replayCmd, terminalStarts, replayed.TransientError())
	}
	replayed, _ = update(replayed, tea.KeyMsg{Type: tea.KeyTab})
	_, runningTerminalCmd := update(replayed, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if runningTerminalCmd != nil || len(launchUpdates) != 1 || len(terminalStarts) != 1 {
		t.Fatalf("resume with a running target terminal enqueued work: cmd=%T updates=%#v terminals=%#v", runningTerminalCmd, launchUpdates, terminalStarts)
	}

	_, retainedPendingCmd := update(pending, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if retainedPendingCmd != nil {
		t.Fatal("clearing a derived model mutated the retained model's pending request")
	}
}

func TestModel_FlowRepairAndPhaseResumeReservationsAreMutuallyExclusive(t *testing.T) {
	launchPath := t.TempDir()
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     launchPath,
		WorktreePath: launchPath,
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Status:    flowstore.PhaseBlocked,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{{
				Provider: "codex", SessionID: "codex-session", LaunchID: "launch-old", Status: "ended",
			}},
		}},
	}
	newModel := func() model.Model {
		m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
		return selectFlowPhaseByID(t, m, "implementation")
	}

	t.Run("pending resume blocks repair", func(t *testing.T) {
		pending, resumeCmd := update(newModel(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if resumeCmd == nil {
			t.Fatal("resume should reserve a persistence command")
		}
		if !model.FlowLaunchAttemptHeldForTest(pending, record.FlowID) {
			t.Fatal("resume should hold the Flow through a lifecycle attempt")
		}
		blocked, _ := update(pending, repairKey())
		if got := blocked.TransientError(); !strings.Contains(got, "phase resume is already pending") {
			t.Fatalf("repair status = %q, want pending phase-resume guidance", got)
		}
	})

	t.Run("pending repair blocks resume", func(t *testing.T) {
		pending, repairCmd := update(newModel(), repairKey())
		if repairCmd == nil {
			t.Fatal("repair should reserve a launch attempt")
		}
		if !model.FlowLaunchAttemptHeldForTest(pending, record.FlowID) {
			t.Fatal("repair should hold the Flow through a lifecycle attempt")
		}
		_, resumeCmd := update(pending, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if resumeCmd != nil {
			t.Fatal("phase resume should not reserve persistence while a repair is pending")
		}
	})
}

func TestModel_TrackedFlowPhaseResumeIgnoresStaleResultsWhileRetryIsPending(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/resume-retry",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{{
				Provider: "codex", SessionID: "codex-session", LaunchID: "launch-old", Status: "ended",
			}},
		}},
	}
	addCalls := 0
	var terminalStarts []actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addCalls++
			if addCalls == 2 {
				return flowstore.FlowRecord{}, errors.New("state root locked")
			}
			persisted := record
			persisted.Phases[0].LaunchIDs = append(slices.Clone(record.Phases[0].LaunchIDs), update.LaunchID)
			return persisted, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			terminalStarts = append(terminalStarts, ctx)
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})
	m = selectFlowPhaseByID(t, m, "implementation")

	// A resume now takes two hops: an authoritative read, then persistence. The
	// prepared result of the second hop is the message a later replay has to be
	// fenced against, so it is what each of these helpers hands back.
	resumeToPrepared := func(source model.Model) (model.Model, tea.Msg) {
		t.Helper()
		pending, readCmd := update(source, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		if readCmd == nil {
			t.Fatal("resume should return the authoritative read command")
		}
		pending, prepareCmd := update(pending, readCmd())
		if prepareCmd == nil {
			t.Fatal("the authoritative read should hand off to persistence")
		}
		return pending, prepareCmd()
	}

	firstPending, oldSuccess := resumeToPrepared(m)
	firstStarted, _ := update(firstPending, oldSuccess)
	if len(terminalStarts) != 1 {
		t.Fatalf("terminal starts after first success = %#v, want 1", terminalStarts)
	}
	firstStarted, _ = update(firstStarted, tea.KeyMsg{Type: tea.KeyTab})
	firstStarted = model.ClearFlowEmbeddedTerminalsForTest(firstStarted)
	firstStarted, _ = switchTestMode(firstStarted, ui.ModeFlows)
	firstStarted = selectFlowPhaseByID(t, firstStarted, "implementation")

	secondPending, oldFailure := resumeToPrepared(firstStarted)
	// A pre-persistence failure now reports through the lifecycle's shared
	// failure message, which is what carries the status and the Flow surface
	// refresh a hop later.
	afterFailure, failureCmd := update(secondPending, oldFailure)
	if failureCmd == nil {
		t.Fatal("a pre-persistence resume failure should report a failure")
	}
	afterFailure, refreshCmd := update(afterFailure, failureCmd())
	if refreshCmd == nil || !strings.Contains(afterFailure.TransientError(), "failed to mark flow phase resume: state root locked") {
		t.Fatalf("persistence failure = cmd %T status %q", refreshCmd, afterFailure.TransientError())
	}
	retryPending, retryCmd := update(afterFailure, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if retryCmd == nil {
		t.Fatal("persistence failure should admit a retry")
	}
	statusBeforeStaleResults := retryPending.TransientError()

	afterOldSuccess, staleSuccessCmd := update(retryPending, oldSuccess)
	if staleSuccessCmd != nil || len(terminalStarts) != 1 || afterOldSuccess.TransientError() != statusBeforeStaleResults {
		t.Fatalf("old success disturbed retry: cmd=%T terminals=%#v status=%q", staleSuccessCmd, terminalStarts, afterOldSuccess.TransientError())
	}
	afterOldFailure, staleFailureCmd := update(afterOldSuccess, oldFailure)
	if staleFailureCmd != nil || len(terminalStarts) != 1 || afterOldFailure.TransientError() != statusBeforeStaleResults {
		t.Fatalf("old failure disturbed retry: cmd=%T terminals=%#v status=%q", staleFailureCmd, terminalStarts, afterOldFailure.TransientError())
	}

	completed := settleModelCommands(t, afterOldFailure, retryCmd, 2)
	if addCalls != 3 || len(terminalStarts) != 2 || terminalStarts[0].LaunchID == terminalStarts[1].LaunchID {
		t.Fatalf("retry completion: add calls=%d terminals=%#v", addCalls, terminalStarts)
	}
	statusBeforeReplay := completed.TransientError()
	completed, replayFailureCmd := update(completed, oldFailure)
	if replayFailureCmd != nil || completed.TransientError() != statusBeforeReplay || len(terminalStarts) != 2 {
		t.Fatalf("consumed failure replay changed state: cmd=%T status=%q terminals=%#v", replayFailureCmd, completed.TransientError(), terminalStarts)
	}
}

func TestModel_TrackedFlowPhaseResumesForIndependentTargetsCompleteOutOfOrder(t *testing.T) {
	flowA := flowstore.FlowRecord{
		FlowID:       "flow-a",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/resume-a",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "Review-Loop",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-a-old"},
			Sessions: []flowstore.Session{{
				Provider: "codex", SessionID: "codex-a", LaunchID: "launch-a-old", Status: "ended",
			}},
		}},
	}
	flowB := flowstore.FlowRecord{
		FlowID:       "flow-b",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/resume-b",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "review-loop",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-b-old"},
			Sessions: []flowstore.Session{{
				Provider: "codex", SessionID: "codex-b", LaunchID: "launch-b-old", Status: "ended",
			}},
		}},
	}
	flows := map[string]flowstore.FlowRecord{flowA.FlowID: flowA, flowB.FlowID: flowB}
	var launchUpdates []flowstore.PhaseLaunchUpdate
	var terminalStarts []actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdates = append(launchUpdates, update)
			persisted := flows[update.FlowID]
			persisted.Phases[0].LaunchIDs = append(slices.Clone(persisted.Phases[0].LaunchIDs), update.LaunchID)
			return persisted, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			terminalStarts = append(terminalStarts, ctx)
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowA, flowB})
	m = selectFlowPhaseByID(t, m, "Review-Loop")
	pendingA, cmdA := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmdA == nil {
		t.Fatal("target A should return a persistence command")
	}

	selectTargetB := func(source model.Model) model.Model {
		next, _ := update(source, tea.KeyMsg{Type: tea.KeyEnter})
		if next.ExpandedFlowID() != "" {
			t.Fatalf("enter on selected target A did not collapse it: expanded=%q", next.ExpandedFlowID())
		}
		next, _ = update(next, tea.KeyMsg{Type: tea.KeyDown})
		if next.FlowSelected() != 1 {
			t.Fatalf("selected Flow index = %d, want target B", next.FlowSelected())
		}
		return selectFlowPhaseByID(t, next, "review-loop")
	}
	pendingAB, cmdB := update(selectTargetB(pendingA), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmdB == nil {
		t.Fatal("independent target B should remain admissible while target A is pending")
	}
	_, retainedBCommand := update(selectTargetB(pendingA), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if retainedBCommand == nil {
		t.Fatal("adding target B to a derived model mutated the retained A-only model")
	}

	// Each target reads authoritatively before it persists, and the two chains
	// are independent all the way through.
	pendingAB, prepareA := update(pendingAB, cmdA())
	if prepareA == nil {
		t.Fatal("target A's read should hand off to persistence")
	}
	pendingAB, prepareB := update(pendingAB, cmdB())
	if prepareB == nil {
		t.Fatal("target B's read should hand off to persistence")
	}
	msgA := prepareA()
	msgB := prepareB()
	if len(launchUpdates) != 2 || launchUpdates[0].FlowID != "flow-a" || launchUpdates[1].FlowID != "flow-b" {
		t.Fatalf("launch updates = %#v, want independent A then B persistence", launchUpdates)
	}
	afterB, _ := update(pendingAB, msgB)
	if len(terminalStarts) != 1 || terminalStarts[0].FlowID != "flow-b" {
		t.Fatalf("terminal starts after B result = %#v", terminalStarts)
	}
	afterA, _ := update(afterB, msgA)
	if len(terminalStarts) != 2 || terminalStarts[1].FlowID != "flow-a" {
		t.Fatalf("terminal starts after out-of-order A result = %#v", terminalStarts)
	}
	statusBeforeReplay := afterA.TransientError()
	afterReplay, replayCmd := update(afterA, msgB)
	if replayCmd != nil || len(terminalStarts) != 2 || afterReplay.TransientError() != statusBeforeReplay {
		t.Fatalf("replayed B result changed state: cmd=%T terminals=%#v status=%q", replayCmd, terminalStarts, afterReplay.TransientError())
	}
}

func TestModel_RKeyOnSelectedFlowPhasePersistenceFailureDoesNotStartTerminal(t *testing.T) {
	persisted := false
	started := false
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			persisted = true
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			started = true
			return &fakeEmbeddedTerminal{}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/resume-failure",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{{
				Provider: "codex", SessionID: "codex-session", Status: "ended",
			}},
		}},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil || persisted || started {
		t.Fatalf("resume Update sequencing: cmd=%T persisted=%v started=%v", cmd, persisted, started)
	}
	read := cmd()
	if persisted || started {
		t.Fatalf("authoritative read sequencing: persisted=%v started=%v", persisted, started)
	}
	m, persistCmd := update(m, read)
	if persistCmd == nil {
		t.Fatal("the authoritative read should hand off to persistence")
	}
	result := persistCmd()
	if !persisted || started {
		t.Fatalf("resume persistence command sequencing: persisted=%v started=%v", persisted, started)
	}
	m, failureCmd := update(m, result)
	if started || failureCmd == nil {
		t.Fatalf("persistence failure state: started=%v failure=%T", started, failureCmd)
	}
	// The status and the Flow surface refresh arrive on the lifecycle's shared
	// failure message rather than on a resume-specific one.
	m, refreshCmd := update(m, failureCmd())
	if started || refreshCmd == nil || !strings.Contains(m.TransientError(), "failed to mark flow phase resume: state root locked") {
		t.Fatalf("persistence failure state: started=%v refresh=%T status=%q", started, refreshCmd, m.TransientError())
	}
}

func TestModel_SkippedFlowPhaseWithEndedSessionAdvertisesResume(t *testing.T) {
	flow := flowWithEndedRunningImplementation()
	flow.Phases[2].Status = flowstore.PhaseSkipped
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "r      resume") {
		t.Fatalf("skipped Flow phase with ended session should expose resume shortcut:\n%s", view)
	}
	if strings.Contains(view, "x      reset ready") {
		t.Fatalf("skipped Flow phase should not expose reset shortcut:\n%s", view)
	}
}

func TestModel_FlowPhaseWithUnsupportedProviderDoesNotAdvertiseResume(t *testing.T) {
	launchRan := false
	m := newTestModel(testRepos(), model.Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchRan = true
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Unsupported provider",
		Status:       flowstore.StatusInProgress,
		WorktreePath: "/dev/alpha-worktrees/flow-unsupported-provider",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{
				{Provider: "unsupported-agent", SessionID: "session-1", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if strings.Contains(m.View(), "r      resume") {
		t.Fatalf("unsupported provider should not advertise Flow phase resume:\n%s", m.View())
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for unsupported provider")
	}
	if launchRan {
		t.Fatal("LaunchAgent ran for unsupported Flow phase provider")
	}
	if got := m.TransientError(); !strings.Contains(got, "unsupported") {
		t.Fatalf("status = %q, want unsupported provider validation", got)
	}
}

func TestModel_RKeyOnFlowPhaseAwaitingLatestSessionDoesNotResumeOlderSession(t *testing.T) {
	launchRan := false
	m := newTestModel(testRepos(), model.Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchRan = true
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Await latest",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/await-latest",
		WorktreePath: "/dev/alpha-worktrees/flow-await-latest",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Status:    flowstore.PhaseRunning,
			LaunchIDs: []string{"launch-old", "launch-new"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-old", LaunchID: "launch-old", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for awaiting latest session")
	}
	if launchRan {
		t.Fatal("LaunchAgent ran for stale Flow phase session")
	}
	if got := m.TransientError(); !strings.Contains(got, "awaiting session") {
		t.Fatalf("status = %q, want awaiting session", got)
	}
}

func TestModel_RKeyOnFlowPhaseAwaitingLatestSessionDoesNotResumeOlderLiveSession(t *testing.T) {
	launchRan := false
	m := newTestModel(testRepos(), model.Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchRan = true
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			launchRan = true
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Await latest with live older session",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/await-latest-live",
		WorktreePath: "/dev/alpha-worktrees/flow-await-latest-live",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Status:    flowstore.PhaseRunning,
			LaunchIDs: []string{"launch-old", "launch-new"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-old", LaunchID: "launch-old", Status: "last_seen"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if strings.Contains(m.View(), "r      resume") {
		t.Fatalf("awaiting latest session with older live session should not advertise resume:\n%s", m.View())
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for awaiting latest session")
	}
	if launchRan {
		t.Fatal("launch ran for stale Flow phase session")
	}
	if got := m.TransientError(); !strings.Contains(got, "awaiting session") {
		t.Fatalf("status = %q, want awaiting session", got)
	}
}

func TestModel_RKeyOnRunningEndedSessionFlowPhaseDoesNotResume(t *testing.T) {
	launchRan := false
	m := newTestModel(testRepos(), model.Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchRan = true
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			launchRan = true
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithEndedRunningImplementation()})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for running ended-session phase")
	}
	if launchRan {
		t.Fatal("launcher ran for running ended-session Flow phase")
	}
	if got := m.TransientError(); !strings.Contains(got, "ended session") || !strings.Contains(got, "reset") {
		t.Fatalf("status = %q, want ended session reset guidance", got)
	}
}

func TestModel_RKeyOnRunningEndedLatestSessionDoesNotResumeWhenOlderSessionIsLive(t *testing.T) {
	launchRan := false
	flow := flowWithEndedRunningImplementation()
	flow.Phases[2].LaunchIDs = []string{"launch-old", "launch-ended"}
	flow.Phases[2].Sessions = append([]flowstore.Session{
		{Provider: "claude", SessionID: "claude-old", LaunchID: "launch-old", Status: "last_seen"},
	}, flow.Phases[2].Sessions...)
	m := newTestModel(testRepos(), model.Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchRan = true
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			launchRan = true
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	if strings.Contains(m.View(), "r      resume") {
		t.Fatalf("running ended latest session should not advertise resume:\n%s", m.View())
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for running ended latest session")
	}
	if launchRan {
		t.Fatal("launcher ran for running ended latest session")
	}
	if got := m.TransientError(); !strings.Contains(got, "ended session") || strings.Contains(got, "reset it to ready") {
		t.Fatalf("status = %q, want non-resumable ended session guidance", got)
	}
}

func TestModel_RKeyOnFlowPhaseNeedsAttentionCanResumeOlderValidSession(t *testing.T) {
	var started actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI resume should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Attention latest",
		Status:       flowstore.StatusNeedsAttention,
		Branch:       "flow/attention-latest",
		WorktreePath: "/dev/alpha-worktrees/flow-attention-latest",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Status:    flowstore.PhaseNeedsAttention,
			LaunchIDs: []string{"launch-old", "launch-new"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-old", LaunchID: "launch-old", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if !strings.Contains(m.View(), "r      resume") {
		t.Fatalf("needs_attention phase should advertise Flow phase resume:\n%s", m.View())
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected needs_attention phase to resume older valid session")
	}
	m = settleModelCommands(t, m, cmd, 2)
	if started.ResumeSessionID != "codex-old" || !started.Embedded || started.Headless || !started.FlowLaunchTracked {
		t.Fatalf("embedded resume context = %#v, want older valid interactive session", started)
	}
}

func TestModel_RKeyOnFlowPhaseResumeSetupFailureKeepsCompletedPhase(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{
				PhaseID: update.PhaseID,
				Status:  flowstore.PhaseCompleted,
			}}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow phase CLI resume should not call LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "review-loop",
			Title:   "Review loop",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("resume start failure should refresh flows")
	}
	m = settleModelCommands(t, m, cmd, 3)
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if len(phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want none; a failed resume must not regress a completed phase", phaseUpdates)
	}
	if got := m.TransientError(); !strings.Contains(got, "terminal unavailable") {
		t.Fatalf("status = %q, want launch failure", got)
	}
}

func TestModel_RKeyOnFlowPhaseResumeSetupFailureStillFlagsNonTerminalPhase(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{
				PhaseID: update.PhaseID,
				Status:  flowstore.PhaseRunning,
			}}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow phase CLI resume should not call LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume flagged phase failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-flagged-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-flagged-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "review-loop",
			Title:   "Review loop",
			Status:  flowstore.PhaseNeedsAttention,
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = settleModelCommands(t, m, cmd, 3)
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one launch failure update", phaseUpdates)
	}
	if update := phaseUpdates[0]; update.FlowID != "flow-1" ||
		update.PhaseID != "review-loop" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "terminal unavailable") {
		t.Fatalf("phase update = %#v", update)
	}
}

func TestModel_RKeyOnFlowPhaseResumeFailureFlagsPhaseReopenedByStore(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			// The persisted phase changed to non-terminal since the list was
			// fetched, so the store reopened it as running.
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{
				PhaseID: update.PhaseID,
				Status:  flowstore.PhaseRunning,
			}}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow phase CLI resume should not call LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume reopened phase failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-reopened-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-reopened-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "review-loop",
			Title:   "Review loop",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = settleModelCommands(t, m, cmd, 3)
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one; the store reopened the phase, so a failed launch must flag it", phaseUpdates)
	}
	if update := phaseUpdates[0]; update.FlowID != "flow-1" ||
		update.PhaseID != "review-loop" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "terminal unavailable") {
		t.Fatalf("phase update = %#v", update)
	}
}

func TestModel_RKeyOnFlowPhaseResumeFailureUsesNormalizedPersistedPhaseID(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{
				PhaseID: "review-loop",
				Status:  flowstore.PhaseRunning,
			}}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow phase CLI resume should not call LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume normalized phase failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-normalized-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-normalized-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "Review-Loop",
			Title:   "Review loop",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = settleModelCommands(t, m, cmd, 3)
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one; normalized persisted running phase must override terminal snapshot", phaseUpdates)
	}
	if update := phaseUpdates[0]; update.FlowID != "flow-1" ||
		update.PhaseID != "Review-Loop" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "terminal unavailable") {
		t.Fatalf("phase update = %#v", update)
	}
}

func TestModel_RKeyOnFlowPhaseResumeFailurePrefersExactPersistedDuplicate(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{
				{PhaseID: "review-loop", Status: flowstore.PhaseCompleted},
				{PhaseID: "Review-Loop", Status: flowstore.PhaseRunning},
			}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI resume should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume duplicate phase failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-duplicate-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-duplicate-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "Review-Loop",
			Title:   "Review loop",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = settleModelCommands(t, m, cmd, 3)
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one; exact persisted running phase must override stale terminal duplicate", phaseUpdates)
	}
	if update := phaseUpdates[0]; update.FlowID != "flow-1" ||
		update.PhaseID != "Review-Loop" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "terminal unavailable") {
		t.Fatalf("phase update = %#v", update)
	}
}

func TestModel_RKeyOnFlowPhaseResumeFailureKeepsPhaseCompletedInStore(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			// An agent completed the phase since the list was fetched, so the
			// store preserved its terminal status.
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{
				PhaseID: update.PhaseID,
				Status:  flowstore.PhaseCompleted,
			}}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow phase CLI resume should not call LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume completed-in-store failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-completed-in-store",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-completed-in-store",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "review-loop",
			Title:   "Review loop",
			Status:  flowstore.PhaseNeedsAttention,
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = settleModelCommands(t, m, cmd, 3)
	if len(phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want none; the store preserved the completed phase, so a failed launch must not regress it", phaseUpdates)
	}
}

func TestModel_RKeyOnFlowPhaseResumeStartFailureKeepsSkippedPhase(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{
				PhaseID: update.PhaseID,
				Status:  flowstore.PhaseSkipped,
			}}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Flow phase CLI resume should not call LaunchAgent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("terminal unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume skipped start failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-skipped-start-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-skipped-start-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "review-loop",
			Title:     "Review loop",
			Status:    flowstore.PhaseSkipped,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", LaunchID: "launch-old", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected resume launch command")
	}
	m = settleModelCommands(t, m, cmd, 3)

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if len(phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want none; a failed resume must not regress a skipped phase", phaseUpdates)
	}
}

func TestModel_GLaunchesFlowPhaseReadyPlanReviewWithLinkedPlanContext(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach/sessions/v1",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Plan Review launch should pass the plan path without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-review",
		Branch:       "flow/review",
		Commit:       "abc123",
		Title:        "Review saved plan",
		Instructions: "Custom flow instructions from the user.",
		Status:       flowstore.StatusInProgress,
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/sessions/v1/plans/plan-1/plan.md",
		UpdatedAt:    time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Outcome: "plan_saved", Summary: "Saved and linked plan-1.", Notes: "Plan author noted a migration risk."},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseReady},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "plan-review")
	if cmd == nil {
		t.Fatal("g should prepare a plan-review launch")
	}
	m = runPreparedFlowEmbeddedLaunch(t, m, cmd)

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "plan-review" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.FlowID != "flow-1" ||
		launched.FlowPhaseID != "plan-review" ||
		launched.PlanID != "plan-1" ||
		launched.PlanPath != "/state/approach/sessions/v1/plans/plan-1/plan.md" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-review" ||
		launched.Branch != "flow/review" ||
		launched.Commit != "abc123" ||
		launched.SessionStateRoot != "/state/approach/sessions/v1" ||
		!launched.Embedded ||
		launched.Headless ||
		!launched.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Use the review-loop skill to review the saved plan, max 6 loops.",
		"Use the approach-flow skill to record the Plan Review verdict before finishing; the phase is not done until the verdict is persisted.",
		"",
		"Plan: /state/approach/sessions/v1/plans/plan-1/plan.md",
		"Worktree: /dev/alpha-worktrees/flow-review",
		"Branch: flow/review",
		"Start commit: abc123",
	}, "\n"))
	if launched.InitialPrompt != wantPrompt {
		t.Fatalf("plan-review prompt = %q, want %q", launched.InitialPrompt, wantPrompt)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, unwanted := range []string{
		"custom flow instructions from the user",
		"# saved plan",
		"implement issue 112 with tests",
		"saved and linked plan-1",
		"plan author noted a migration risk",
		"approach flow phase set",
		"approved_with_concerns",
		"changes_requested",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("minimum reliable prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_FlowPlanReviewPromptTemplateOverridesBuiltInPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		FlowPromptTemplates: model.FlowPromptTemplates{
			PlanReview: "Custom {phase_id} for {flow_id}: {plan_path} on {branch}; keep {unknown}",
		},
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("templated Plan Review launch should not pre-read %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-template",
		Branch:       "flow/template",
		Commit:       "c0ffee",
		PlanID:       "plan-1",
		PlanPath:     "/state/plans/plan-1/plan.md",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseReady},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "plan-review")
	if cmd == nil {
		t.Fatal("g should prepare a plan-review launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	want := appendFlowDoneInstructionForTest("Custom plan-review for flow-1: /state/plans/plan-1/plan.md on flow/template; keep {unknown}")
	if launched.InitialPrompt != want {
		t.Fatalf("templated plan-review prompt = %q, want %q", launched.InitialPrompt, want)
	}
}

func TestModel_GLaunchesFlowPhaseImplementationWithMinimalPrompt(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach/sessions/v1",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Implementation launch should pass the plan path without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Branch:       "flow/implementation",
		Commit:       "fed321",
		Title:        "Implement saved plan",
		Instructions: "Custom flow instructions from the user.",
		Status:       flowstore.StatusInProgress,
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/sessions/v1/plans/plan-1/plan.md",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Summary: "Saved and linked plan-1."},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved", Summary: "Plan approved."},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an implementation launch")
	}
	m = runPreparedFlowEmbeddedLaunch(t, m, cmd)

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.FlowID != "flow-1" ||
		launched.FlowPhaseID != "implementation" ||
		launched.PlanID != "plan-1" ||
		launched.PlanPath != "/state/approach/sessions/v1/plans/plan-1/plan.md" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-implementation" ||
		launched.Branch != "flow/implementation" ||
		launched.Commit != "fed321" ||
		launched.SessionStateRoot != "/state/approach/sessions/v1" ||
		!launched.Embedded ||
		launched.Headless ||
		!launched.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Implement the approved plan.",
		"Use the commit skill before completing this phase.",
		"",
		"Plan: /state/approach/sessions/v1/plans/plan-1/plan.md",
		"Worktree: /dev/alpha-worktrees/flow-implementation",
		"Branch: flow/implementation",
		"Start commit: fed321",
	}, "\n"))
	if launched.InitialPrompt != wantPrompt {
		t.Fatalf("implementation prompt = %q, want %q", launched.InitialPrompt, wantPrompt)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, unwanted := range []string{
		"custom flow instructions from the user",
		"saved and linked plan-1",
		"plan approved",
		"plan review gate",
		"approach flow phase set",
		"verify the target behavior",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("implementation prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GLaunchesFlowPhaseImplementationInEmbeddedHeadlessTerminalByDefault(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	var startWidth, startHeight int
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	launchAgentRan := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:         "codex",
		CodexReasoningEffort: "high",
		SessionStateRoot:     "/state/approach/sessions/v1",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Implementation launch should pass the plan path without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAgentRan = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			startWidth = width
			startHeight = height
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Branch:       "flow/implementation",
		Commit:       "fed321",
		Title:        "Implement saved plan",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/sessions/v1/plans/plan-1/plan.md",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded implementation launch")
	}
	m, cmd = update(m, cmd())
	if launchAgentRan {
		t.Fatal("default headless g should not call LaunchAgent for CLI providers")
	}
	if cmd == nil {
		t.Fatal("expected embedded launch to return repaint/fetch command")
	}
	if batch, ok := cmd().(tea.BatchMsg); !ok || len(batch) < 2 {
		t.Fatalf("embedded Flow launch command = %T, %#v; want batched repaint and refresh", cmd, batch)
	}

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if started.Command != "codex" ||
		started.FlowID != "flow-1" ||
		started.FlowPhaseID != "implementation" ||
		started.PlanID != "plan-1" ||
		started.PlanPath != "/state/approach/sessions/v1/plans/plan-1/plan.md" ||
		started.WorktreePath != "/dev/alpha-worktrees/flow-implementation" ||
		started.Branch != "flow/implementation" ||
		started.Commit != "fed321" ||
		started.SessionStateRoot != "/state/approach/sessions/v1" ||
		started.ReasoningEffort != "high" ||
		!started.Embedded ||
		!started.Headless {
		t.Fatalf("embedded launch context = %#v", started)
	}
	if started.LaunchID == "" || started.LaunchID != launchUpdate.LaunchID {
		t.Fatalf("embedded launch ID = %q, launch update = %#v", started.LaunchID, launchUpdate)
	}
	wantStartWidth := ui.EmbeddedTerminalPTYWidth(140)
	wantStartHeight := ui.ResolveEmbeddedTerminalDock(24, true).BackendPTYRows
	if startWidth != wantStartWidth || startHeight != wantStartHeight {
		t.Fatalf("embedded terminal start size = %dx%d, want %dx%d", startWidth, startHeight, wantStartWidth, wantStartHeight)
	}
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("headless Flow launch should not prefill embedded terminal, got writes %#v", fakeTerm.writes)
	}
	_ = m.View()
	wantStartSize := [2]int{wantStartWidth, wantStartHeight}
	if len(fakeTerm.visibleCalls) == 0 || fakeTerm.visibleCalls[len(fakeTerm.visibleCalls)-1] != wantStartSize {
		t.Fatalf("embedded terminal visible calls = %#v, want latest %dx%d", fakeTerm.visibleCalls, wantStartWidth, wantStartHeight)
	}
	m, _ = update(m, tea.WindowSizeMsg{Width: 160, Height: 23})
	wantResizeWidth := ui.EmbeddedTerminalPTYWidth(160)
	wantResizeHeight := ui.ResolveEmbeddedTerminalDock(23, true).BackendPTYRows
	wantResizeSize := [2]int{wantResizeWidth, wantResizeHeight}
	if len(fakeTerm.resizes) == 0 || fakeTerm.resizes[len(fakeTerm.resizes)-1] != wantResizeSize {
		t.Fatalf("embedded terminal resize calls = %#v, want latest %dx%d", fakeTerm.resizes, wantResizeWidth, wantResizeHeight)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Implement the approved plan.",
		"Use the commit skill before completing this phase.",
		"",
		"Plan: /state/approach/sessions/v1/plans/plan-1/plan.md",
		"Worktree: /dev/alpha-worktrees/flow-implementation",
		"Branch: flow/implementation",
		"Start commit: fed321",
	}, "\n"))
	if started.InitialPrompt != wantPrompt {
		t.Fatalf("embedded prompt = %q, want %q", started.InitialPrompt, wantPrompt)
	}
}

func TestModel_FlowEmbeddedTerminalAutoClosesOnExitedTick(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	flow := flowWithPhaseDetails()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flow}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, tickBatch := update(m, cmd())
	if tickBatch == nil {
		t.Fatal("embedded launch should schedule repaint tick")
	}
	if view := m.View(); !strings.Contains(view, "agent output") {
		t.Fatalf("running Flow terminal should render output before exit:\n%s", view)
	}

	fakeTerm.state = "exited"
	for _, msg := range runBatchCmd(t, tickBatch) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if _, ok := msg.(model.FlowResultMsg); ok {
			continue
		}
		if followup != nil {
			m = applyFlowResultFollowup(t, m, followup)
		}
	}

	view := m.View()
	if strings.Contains(view, "agent output") || strings.Contains(view, "1 codex implementation exited") {
		t.Fatalf("exited Flow terminal should auto-close on repaint tick:\n%s", view)
	}
	if !strings.Contains(view, "implementation:ready") {
		t.Fatalf("Flow list should be visible after the terminal auto-closes:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalAutoCloseKeepsRunningSessionTickAlive(t *testing.T) {
	sessionTerm := &fakeEmbeddedTerminal{lines: []string{"session output"}, state: "running"}
	flowTerm := &fakeEmbeddedTerminal{lines: []string{"flow output"}, state: "running"}
	flow := flowWithPhaseDetails()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			if ctx.ResumeSessionID != "" {
				return sessionTerm, nil
			}
			return flowTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flow}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, flowTick := update(m, cmd())
	if flowTick == nil {
		t.Fatal("first Flow terminal should schedule repaint tick")
	}
	if view := m.View(); !strings.Contains(view, "flow output") {
		t.Fatalf("running Flow terminal should render output before exit:\n%s", view)
	}
	m, _ = switchTestMode(m, ui.ModeSessions)
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session", Branch: "feature/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, sessionTick := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if sessionTick != nil {
		t.Fatal("opening a session while a Flow terminal is running should reuse the active repaint loop")
	}

	flowTerm.state = "exited"
	gotFollowup := false
	for _, msg := range runBatchCmd(t, flowTick) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if followup != nil {
			gotFollowup = true
		}
	}
	if !gotFollowup {
		t.Fatal("running session terminal should keep repaint loop alive after Flow auto-close")
	}

	view := m.View()
	if !strings.Contains(view, "session output") || !strings.Contains(view, "1 codex feature/session running") {
		t.Fatalf("running session terminal should remain visible after Flow auto-close:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalAutoClosePreservesExitedSessionTerminal(t *testing.T) {
	sessionTerm := &fakeEmbeddedTerminal{lines: []string{"session done"}, state: "exited"}
	flowTerm := &fakeEmbeddedTerminal{lines: []string{"flow done"}, state: "running"}
	flow := flowWithPhaseDetails()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			if ctx.ResumeSessionID != "" {
				return sessionTerm, nil
			}
			return flowTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flow}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, tickBatch := update(m, cmd())
	if tickBatch == nil {
		t.Fatal("embedded launch should schedule repaint tick when no PTY is running")
	}
	m, _ = switchTestMode(m, ui.ModeSessions)
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session", Branch: "feature/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, sessionTick := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if sessionTick != nil {
		t.Fatal("opening an exited session while a Flow terminal is running should reuse the active repaint loop")
	}

	flowTerm.state = "exited"
	for _, msg := range runBatchCmd(t, tickBatch) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if followup != nil {
			m = applyFlowResultFollowup(t, m, followup)
		}
	}

	view := m.View()
	if !strings.Contains(view, "session done") || !strings.Contains(view, "1 codex feature/session exited") {
		t.Fatalf("exited session terminal should remain visible after Flow auto-close:\n%s", view)
	}
	if strings.Contains(view, "flow done") {
		t.Fatalf("Flow terminal output should not remain after auto-close:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	view = m.View()
	if strings.Contains(view, "session done") || strings.Contains(view, "1 codex feature/session") {
		t.Fatalf("exited session terminal should still be dismissible:\n%s", view)
	}
	if !strings.Contains(view, "Provider") {
		t.Fatalf("sessions table should return after dismissing saved-session terminal:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalAutoCloseRenumbersAndKeepsActiveFlowTerminal(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{lines: []string{"flow first output"}, state: "running"},
		{lines: []string{"flow second output"}, state: "running"},
		{lines: []string{"flow third output"}, state: "running"},
	}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
	})
	m = flowsInRightPane(t, m, flowRecordsForTerminals(len(terms)))

	var firstTick tea.Cmd
	for i := 0; i < len(terms); i++ {
		m = selectFlowRow(t, m, i)
		var cmd tea.Cmd
		m, cmd = flowLaunchFromKey(t, m)
		if cmd == nil {
			t.Fatal("g should prepare an embedded launch")
		}
		var tickBatch tea.Cmd
		m, tickBatch = update(m, cmd())
		if i == 0 {
			firstTick = tickBatch
		}
	}
	if starts != 3 {
		t.Fatalf("embedded terminal starts = %d, want 3", starts)
	}
	if firstTick == nil {
		t.Fatal("first Flow terminal should schedule repaint tick")
	}
	if view := m.View(); !strings.Contains(view, "flow third output") {
		t.Fatalf("third Flow terminal should be active before auto-close:\n%s", view)
	}

	terms[1].state = "exited"
	var gotFollowup bool
	for _, msg := range runBatchCmd(t, firstTick) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if followup != nil {
			gotFollowup = true
		}
	}
	if !gotFollowup {
		t.Fatal("remaining running Flow terminals should keep repaint loop alive")
	}

	view := m.View()
	for _, want := range []string{"1 codex implementation running", "2 codex implementation running", "flow third output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("renumbered Flow terminal view missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"3 codex", "flow second output", "2 codex implementation exited"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("auto-closed Flow terminal should not remain visible with %q:\n%s", unwanted, view)
		}
	}
	if strings.Contains(view, "flow first output") {
		t.Fatalf("former third terminal should stay active after renumbering:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalAutoCloseKeepsCommandModeForPromotedTerminal(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{lines: []string{"flow first output"}, state: "running"},
		{lines: []string{"flow second output"}, state: "running"},
	}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
	})
	m = flowsInRightPane(t, m, flowRecordsForTerminals(len(terms)))

	var tickBatch tea.Cmd
	for i := range terms {
		m = selectFlowRow(t, m, i)
		var cmd tea.Cmd
		m, cmd = flowLaunchFromKey(t, m)
		if cmd == nil {
			t.Fatal("g should prepare an embedded launch")
		}
		var nextTick tea.Cmd
		m, nextTick = update(m, cmd())
		if tickBatch == nil {
			tickBatch = nextTick
		}
	}
	if tickBatch == nil {
		t.Fatal("first Flow terminal should schedule repaint tick")
	}
	if view := m.View(); !strings.Contains(view, "flow second output") {
		t.Fatalf("second Flow terminal should be active before auto-close:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	terms[1].state = "exited"
	gotFollowup := false
	for _, msg := range runBatchCmd(t, tickBatch) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if followup != nil {
			gotFollowup = true
		}
	}
	if !gotFollowup {
		t.Fatal("remaining running Flow terminal should keep repaint loop alive")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if len(terms[0].writes) != 0 {
		t.Fatalf("x after active Flow auto-close should stay in command mode, got writes %#v", terms[0].writes)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("x after active Flow auto-close should confirm promoted terminal close, got overlay %d", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "Terminate embedded terminal?") {
		t.Fatalf("close confirmation should be visible for promoted Flow terminal:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if view := m.View(); !strings.Contains(view, "flow first output") || strings.Contains(view, "flow second output") {
		t.Fatalf("promoted Flow terminal should remain visible after canceling close confirmation:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalAutoCloseClearsStaleTerminateConfirm(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{lines: []string{"flow first output"}, state: "running"},
		{lines: []string{"flow second output"}, state: "running"},
	}
	starts := 0
	flow := flowWithPhaseDetails()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flow, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flow}, nil
		},
	})
	m = flowsInRightPane(t, m, flowRecordsForTerminals(len(terms)))
	var tickBatch tea.Cmd
	for i := range terms {
		m = selectFlowRow(t, m, i)
		var cmd tea.Cmd
		m, cmd = flowLaunchFromKey(t, m)
		if cmd == nil {
			t.Fatal("g should prepare an embedded launch")
		}
		var nextTick tea.Cmd
		m, nextTick = update(m, cmd())
		if tickBatch == nil {
			tickBatch = nextTick
		}
	}
	if tickBatch == nil {
		t.Fatal("first Flow terminal should schedule repaint tick")
	}
	if view := m.View(); !strings.Contains(view, "flow second output") {
		t.Fatalf("second Flow terminal should be active before auto-close:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected terminate confirmation, got %d", m.Overlay())
	}

	terms[1].state = "exited"
	gotFollowup := false
	for _, msg := range runBatchCmd(t, tickBatch) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if followup != nil {
			gotFollowup = true
		}
	}
	if !gotFollowup {
		t.Fatal("remaining running Flow terminal should keep repaint loop alive")
	}

	if m.Overlay() == ui.OverlayConfirm || strings.Contains(m.View(), "Terminate embedded terminal?") {
		t.Fatalf("auto-closing the confirmed Flow terminal should clear stale confirmation:\n%s", m.View())
	}
	view := m.View()
	if !strings.Contains(view, "flow first output") || strings.Contains(view, "flow second output") {
		t.Fatalf("promoted Flow terminal should be visible after auto-close clears confirmation:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalTickKeepsFailedTerminalVisible(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"failure output"}, state: "running"}
	flow := flowWithPhaseDetails()
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{flow}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, tickBatch := update(m, cmd())
	if tickBatch == nil {
		t.Fatal("embedded launch should schedule repaint tick")
	}

	fakeTerm.state = "failed"
	for _, msg := range runBatchCmd(t, tickBatch) {
		var followup tea.Cmd
		m, followup = update(m, msg)
		if _, ok := msg.(model.FlowResultMsg); ok {
			continue
		}
		if followup != nil {
			t.Fatalf("failed Flow terminal should stop repaint loop, got %T", followup)
		}
	}

	view := m.View()
	if !strings.Contains(view, "failure output") || !strings.Contains(view, "1 codex implementation failed") {
		t.Fatalf("failed Flow terminal should remain visible for error review:\n%s", view)
	}
}

func TestModel_FlowEmbeddedLaunchMarksActiveFlowAndPhaseRows(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-active",
		Branch:       "flow/active",
		Title:        "Active terminal flow",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, ">● in_progress") {
		t.Fatalf("active selected Flow row should show selection and marker:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view = ansi.Strip(m.View())
	if !strings.Contains(view, "   >● ready") {
		t.Fatalf("active selected Flow phase should show phase indent, selection, and marker:\n%s", view)
	}
}

func TestModel_FlowTerminalActivityFiltersActiveStates(t *testing.T) {
	for _, tc := range []struct {
		state      string
		wantMarker bool
	}{
		{state: "running", wantMarker: true},
		{state: "starting", wantMarker: true},
		{state: "exited", wantMarker: false},
		{state: "failed", wantMarker: false},
		{state: "terminated", wantMarker: false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					return &fakeEmbeddedTerminal{state: tc.state}, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
				FlowID:       "flow-1",
				RepoPath:     "/dev/alpha",
				WorktreePath: "/dev/alpha-worktrees/flow-active",
				Branch:       "flow/active",
				Title:        "Active terminal flow",
				Status:       flowstore.StatusInProgress,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
				},
			}})

			m = selectFlowPhaseByID(t, m, "implementation")
			m, cmd := flowLaunchFromKey(t, m)
			if cmd == nil {
				t.Fatal("g should prepare an embedded launch")
			}
			m, _ = update(m, cmd())
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})

			view := ansi.Strip(m.View())
			gotMarker := strings.Contains(view, ">● in_progress")
			if gotMarker != tc.wantMarker {
				t.Fatalf("state %q marker = %t, want %t:\n%s", tc.state, gotMarker, tc.wantMarker, view)
			}
		})
	}
}

func TestModel_DismissedFlowTerminalRemovesActiveMarker(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-dismiss",
		Branch:       "flow/dismiss",
		Title:        "Dismiss terminal flow",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if view := ansi.Strip(m.View()); !strings.Contains(view, ">● in_progress") {
		t.Fatalf("running terminal should mark selected Flow row before dismissal:\n%s", view)
	}

	fakeTerm.state = "exited"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	view := ansi.Strip(m.View())
	if strings.Contains(view, "● in_progress") {
		t.Fatalf("dismissed terminal should remove active marker:\n%s", view)
	}
	if strings.Contains(view, "1 codex implementation") {
		t.Fatalf("dismissed terminal should remove flow terminal tab:\n%s", view)
	}
}

func TestModel_FlowTerminalActivityMatchesStructuredFlowAndPhaseIDs(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		{
			FlowID:       "flow-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-one",
			Branch:       "flow/one",
			Title:        "Flow one",
			Status:       flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
			},
		},
		{
			FlowID:       "flow-2",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-two",
			Branch:       "flow/two",
			Title:        "Flow two",
			Status:       flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Same phase ID", Status: flowstore.PhaseReady},
			},
		},
	})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, " ● in_progress      flow/one") {
		t.Fatalf("source flow should remain marked:\n%s", view)
	}
	if strings.Contains(view, ">● in_progress      flow/two") || strings.Contains(view, "   >● ready") {
		t.Fatalf("same phase ID from another Flow must not mark selected target Flow or phase:\n%s", view)
	}
	if !strings.Contains(view, "   in_progress      flow/two") || !strings.Contains(view, "   >  ready") {
		t.Fatalf("target Flow and selected phase should remain unmarked:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalKeepsFullWidthWhenSearchTogglesShortcutPane(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(_ actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Title:        "Implement saved plan",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	wantHeight := flowTerminalPTYHeightForViewport(24)
	wantSearchSize := [2]int{
		ui.EmbeddedTerminalPTYWidth(140),
		wantHeight,
	}
	wantInactiveSize := [2]int{
		ui.EmbeddedTerminalPTYWidth(140),
		wantHeight,
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.SearchActive() {
		t.Fatal("slash should activate search while Flow terminal is visible but list-focused")
	}
	requireLatestResize(t, fakeTerm, wantSearchSize)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.SearchActive() {
		t.Fatal("enter should leave search mode")
	}
	requireLatestResize(t, fakeTerm, wantInactiveSize)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	requireLatestResize(t, fakeTerm, wantSearchSize)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.SearchActive() {
		t.Fatal("escape should leave search mode")
	}
	requireLatestResize(t, fakeTerm, wantInactiveSize)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	requireLatestResize(t, fakeTerm, wantSearchSize)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.SearchActive() {
		t.Fatal("empty backspace should leave search mode")
	}
	requireLatestResize(t, fakeTerm, wantInactiveSize)
}

func requireLatestResize(t *testing.T, fakeTerm *fakeEmbeddedTerminal, want [2]int) {
	t.Helper()
	if len(fakeTerm.resizes) == 0 {
		t.Fatalf("expected resize call %dx%d, got none", want[0], want[1])
	}
	if got := fakeTerm.resizes[len(fakeTerm.resizes)-1]; got != want {
		t.Fatalf("latest resize = %dx%d, want %dx%d; all resizes = %#v", got[0], got[1], want[0], want[1], fakeTerm.resizes)
	}
}

func flowTerminalPTYHeightForViewport(height int) int {
	return ui.ResolveEmbeddedTerminalDock(height, true).BackendPTYRows
}

func TestModel_FlowEmbeddedTerminalTinyAllocationClampsPTYSize(t *testing.T) {
	var started [2]int
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(_ actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = [2]int{width, height}
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Title:        "Implement saved plan",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})
	const width = ui.LeftPaneWidth + 1
	const height = ui.BranchContentOverhead
	m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: height})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	_, terminalOuterHeight := ui.EmbeddedTerminalDockHeights(height-ui.BranchContentOverhead, ui.EmbeddedTerminalDockExpanded)
	want := [2]int{
		ui.EmbeddedTerminalPTYWidth(width),
		ui.EmbeddedTerminalPTYHeight(terminalOuterHeight),
	}
	if started != want {
		t.Fatalf("embedded terminal start size = %dx%d, want %dx%d", started[0], started[1], want[0], want[1])
	}
}

func TestModel_GFlowPhaseEmbeddedTerminalStartFailureMarksPhaseNeedsAttention(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("pty unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-failure",
		Title:        "Embedded failure",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, asyncCmd := update(m, cmd())
	m = settleModelCommands(t, m, asyncCmd, 2)

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "implementation" ||
		phaseUpdate.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(phaseUpdate.Notes, "pty unavailable") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
	if got := m.TransientError(); !strings.Contains(got, "pty unavailable") {
		t.Fatalf("status = %q, want PTY error", got)
	}
}

func TestModel_ActiveFlowCustomPlanReviewLaunchFailureMarksBlocked(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			if ctx.FlowPhaseKind != flowstore.KindPlanReview {
				t.Fatalf("FlowPhaseKind = %q, want %q", ctx.FlowPhaseKind, flowstore.KindPlanReview)
			}
			return nil, errors.New("pty unavailable")
		},
	})
	flow := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-failure",
		PlanID:       "plan-1",
		PlanPath:     "/tmp/plan.md",
		Title:        "Custom review failure",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "design-review", Title: "Design Review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady},
		},
	}
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 50 && model.SelectedActiveFlowPhaseIDForTest(m) != "design-review"; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "design-review" {
		t.Fatalf("selected active Flow phase = %q, want design-review", got)
	}

	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, asyncCmd := update(m, cmd())
	m = settleModelCommands(t, m, asyncCmd, 2)

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "design-review" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		phaseUpdate.Outcome != flowstore.OutcomeBlocked ||
		!strings.Contains(phaseUpdate.Notes, "pty unavailable") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_GFlowPhaseEmbeddedInteractivePrefillFailureCleansUpTerminal(t *testing.T) {
	tests := []struct {
		name           string
		term           *fakeEmbeddedTerminal
		wantStatusText []string
	}{
		{
			name: "write error",
			term: &fakeEmbeddedTerminal{
				lines:    []string{"agent output"},
				state:    "running",
				writeErr: errors.New("input rejected"),
			},
			wantStatusText: []string{"prefill embedded prompt", "input rejected"},
		},
		{
			name: "short write",
			term: &fakeEmbeddedTerminal{
				lines:       []string{"agent output"},
				state:       "running",
				forceWriteN: true,
				writeN:      3,
			},
			wantStatusText: []string{"prefill embedded prompt", "short write"},
		},
		{
			name: "cleanup failure",
			term: &fakeEmbeddedTerminal{
				lines:        []string{"agent output"},
				state:        "running",
				writeErr:     errors.New("input rejected"),
				terminateErr: errors.New("terminate failed"),
			},
			wantStatusText: []string{"prefill embedded prompt", "input rejected", "terminate failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var phaseUpdate flowstore.PhaseUpdate
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
					phaseUpdate = update
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					return tt.term, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
				FlowID:       "flow-1",
				RepoPath:     "/dev/alpha",
				WorktreePath: "/dev/alpha-worktrees/flow-prefill-failure",
				Branch:       "flow/prefill-failure",
				Status:       flowstore.StatusInProgress,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
				},
			}})

			m = selectFlowPhaseByID(t, m, "implementation")
			m, cmd := flowLaunchFromKey(t, m)
			if cmd == nil {
				t.Fatal("g should prepare an embedded launch")
			}
			m, cmd = update(m, cmd())
			if cmd == nil {
				t.Fatal("failed embedded prefill should still refresh Flow state")
			}
			m = settleModelCommands(t, m, cmd, 3)

			if tt.term.terminates != 1 || tt.term.State() != "terminated" {
				t.Fatalf("terminal cleanup = terminates %d state %q, want one terminated cleanup", tt.term.terminates, tt.term.State())
			}
			if phaseUpdate.FlowID != "flow-1" ||
				phaseUpdate.PhaseID != "implementation" ||
				phaseUpdate.Status != flowstore.PhaseNeedsAttention {
				t.Fatalf("phase update = %#v", phaseUpdate)
			}
			for _, want := range tt.wantStatusText {
				if !strings.Contains(phaseUpdate.Notes, want) {
					t.Fatalf("phase notes = %q, want to contain %q", phaseUpdate.Notes, want)
				}
				if !strings.Contains(m.TransientError(), want) {
					t.Fatalf("status = %q, want to contain %q", m.TransientError(), want)
				}
			}
			if strings.Contains(m.View(), "agent output") {
				t.Fatalf("failed prefill should not append a terminal slot:\n%s", m.View())
			}
		})
	}
}

func TestModel_FlowTabCyclesListTerminalLeft(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		{
			FlowID:       "flow-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-one",
			Title:        "Flow one",
			Status:       flowstore.StatusInProgress,
			Headless:     true,
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
			},
		},
		{
			FlowID:       "flow-2",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-two",
			Title:        "Flow two",
			Status:       flowstore.StatusInProgress,
		},
	})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("Flow terminal command mode should not forward unknown command bytes: %#v", fakeTerm.writes)
	}
	if got := m.TransientError(); !strings.Contains(got, "Unknown terminal prefix command") {
		t.Fatalf("status = %q, want unknown terminal command", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x1d" {
		t.Fatalf("ctrl+] in Flow command mode should send a literal ctrl+], writes = %#v", fakeTerm.writes)
	}
	before := listRequests(m)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("tab from Flow terminal active pane = %d, want left pane", m.ActivePane())
	}
	assertListRequestsUnchanged(t, before, m)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.ActivePane() == ui.PaneRepos {
		t.Fatalf("tab from left pane active pane = %d, want Flow list", m.ActivePane())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 1 {
		t.Fatalf("list focus should not forward j to terminal: %#v", fakeTerm.writes)
	}
	if got := m.SelectedFlowPhaseID(); got != "implementation" {
		t.Fatalf("selected Flow phase = %q, want list focus to move to first phase", got)
	}
}

func TestModel_BackKeysFromFlowListReturnToLeftPaneAndClearSelectedPhase(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{name: "ctrl-h", key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
			m = selectFlowPhaseByID(t, m, "implementation")
			before := listRequests(m)

			m, cmd := update(m, tt.key)

			if m.ActivePane() != ui.PaneTop {
				t.Fatalf("active pane = %d, want top pane after reverse focus", m.ActivePane())
			}
			if got := m.SelectedFlowPhaseID(); got != "implementation" {
				t.Fatalf("selected Flow phase = %q, want preserved", got)
			}
			if cmd != nil {
				t.Fatalf("backspace from Flow list returned cmd %T, want nil", cmd)
			}
			assertListRequestsUnchanged(t, before, m)
		})
	}
}

func TestModel_BackKeysFromActiveFlowsReturnToLeftPaneAndClearSelectedPhase(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{name: "ctrl-h", key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flow := flowWithPhaseDetails()
			m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
			m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
			for i := 0; i < 50 && model.SelectedActiveFlowPhaseIDForTest(m) != "implementation"; i++ {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
			}
			if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "implementation" {
				t.Fatalf("selected active Flow phase = %q, want implementation before %s", got, tt.name)
			}
			before := listRequests(m)

			m, cmd := update(m, tt.key)

			if m.ActivePane() != ui.PaneRepos {
				t.Fatalf("active pane = %d, want left pane after %s", m.ActivePane(), tt.name)
			}
			if got := model.SelectedActiveFlowPhaseIDForTest(m); got != "" {
				t.Fatalf("selected active Flow phase = %q, want cleared", got)
			}
			if cmd != nil {
				t.Fatalf("%s from active Flow list returned cmd %T, want nil", tt.name, cmd)
			}
			assertListRequestsUnchanged(t, before, m)
		})
	}
}

func TestModel_BackKeysForwardWhenFlowTerminalInputOwnsKeys(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{name: "ctrl-h", key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					return fakeTerm, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
			m = selectFlowPhaseByID(t, m, "implementation")
			m, cmd := flowLaunchFromKey(t, m)
			if cmd == nil {
				t.Fatal("g should prepare an embedded launch")
			}
			m, _ = update(m, cmd())
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})

			m, cmd = update(m, tt.key)

			if m.ActivePane() == ui.PaneRepos {
				t.Fatalf("terminal-owned %s activePane = %d, want right pane", tt.name, m.ActivePane())
			}
			if cmd != nil {
				t.Fatalf("terminal-owned %s returned cmd %T, want nil", tt.name, cmd)
			}
			if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x7f" {
				t.Fatalf("terminal input %s writes = %#v, want delete byte", tt.name, fakeTerm.writes)
			}
		})
	}
}

func TestModel_BackspaceSwitchesPaneWithoutFocusingInactiveFlowTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		flowWithPhaseDetails(),
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusInProgress},
	})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.ActivePane() != ui.PaneTop {
		t.Fatalf("backspace with inactive Flow terminal activePane = %d, want top pane", m.ActivePane())
	}
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("inactive Flow terminal should not receive backspace writes: %#v", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("returning from backspace should keep Flow list focus, not terminal focus: %#v", fakeTerm.writes)
	}
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want Flow list focus", m.Mode())
	}
}

func TestModel_F2ForwardsWhenFlowTerminalInputOwnsKeys(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})

	if m.ActivePane() == ui.PaneRepos {
		t.Fatalf("terminal-owned f2 activePane = %d, want right pane", m.ActivePane())
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x1bOQ" {
		t.Fatalf("terminal input f2 writes = %#v, want F2 escape sequence", fakeTerm.writes)
	}
}

func TestModel_FlowSettingsKeysDoNotOpenPickersWhileFlowTerminalFocused(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	autoModeCalled := false
	manualMergeLookupCalled := false
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowAutoMode: func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			autoModeCalled = true
			return flowstore.FlowRecord{}, nil
		},
		LookupPRMerge: func(int, string) (actions.PullRequestMerge, error) {
			manualMergeLookupCalled = true
			return actions.PullRequestMerge{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-one",
		Title:        "Flow one",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("terminal command-mode E opened overlay %d", m.Overlay())
	}
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("terminal command-mode E should not write to PTY: %#v", fakeTerm.writes)
	}
	if got := m.TransientError(); !strings.Contains(got, "Unknown terminal prefix command") {
		t.Fatalf("status = %q, want unknown terminal command", got)
	}

	for _, key := range []rune{'a', 'm', 'M'} {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if m.Overlay() != ui.OverlayNone {
			t.Fatalf("terminal command-mode %c opened overlay %d", key, m.Overlay())
		}
		if len(fakeTerm.writes) != 0 {
			t.Fatalf("terminal command-mode %c should not write to PTY: %#v", key, fakeTerm.writes)
		}
	}
	if autoModeCalled {
		t.Fatal("terminal command mode should not toggle Flow auto mode")
	}
	if manualMergeLookupCalled {
		t.Fatal("terminal command mode should not start manual merge lookup")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("terminal input-mode E opened overlay %d", m.Overlay())
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "E" {
		t.Fatalf("terminal input-mode E writes = %#v, want E", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'V'}})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("terminal input-mode V opened overlay %d", m.Overlay())
	}
	if len(fakeTerm.writes) != 2 || fakeTerm.writes[1] != "V" {
		t.Fatalf("terminal input-mode V writes = %#v, want E then V", fakeTerm.writes)
	}

	for _, key := range []rune{'a', 'm', 'M'} {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if m.Overlay() != ui.OverlayNone {
			t.Fatalf("terminal input-mode %c opened overlay %d", key, m.Overlay())
		}
	}
	if !slices.Equal(fakeTerm.writes, []string{"E", "V", "a", "m", "M"}) {
		t.Fatalf("terminal input-mode writes = %#v, want E, V, a, m, M", fakeTerm.writes)
	}
	if autoModeCalled {
		t.Fatal("terminal input mode should not toggle Flow auto mode")
	}
	if manualMergeLookupCalled {
		t.Fatal("terminal input mode should not start manual merge lookup")
	}
}

func TestModel_FlowTerminalCommandModeCanEnterInputMode(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		{
			FlowID:       "flow-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-one",
			Title:        "Flow one",
			Status:       flowstore.StatusInProgress,
			Headless:     true,
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
			},
		},
		{
			FlowID:       "flow-2",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-two",
			Title:        "Flow two",
			Status:       flowstore.StatusInProgress,
		},
	})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("input-mode command should not be forwarded to terminal: %#v", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fakeTerm.writes) != 2 || fakeTerm.writes[0] != "z" || fakeTerm.writes[1] != "\r" {
		t.Fatalf("Flow terminal input mode writes = %#v, want z and enter", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if len(fakeTerm.writes) != 2 {
		t.Fatalf("command mode should let tab leave focus without writing: %#v", fakeTerm.writes)
	}
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("tab from command-mode terminal active pane = %d, want left pane", m.ActivePane())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want Flow list focus", m.Mode())
	}
}

func TestModel_FlowTerminalInputModeTabIsForwardedToTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:     "codex",
		FlowID:      "flow-1",
		FlowPhaseID: "implementation",
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "z" {
		t.Fatalf("interactive Flow launch should focus terminal input, writes = %#v", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.ActivePane() == ui.PaneRepos {
		t.Fatalf("tab from input-mode Flow terminal active pane = %d, want terminal pane", m.ActivePane())
	}
	if !slices.Equal(fakeTerm.writes, []string{"z", "\t"}) {
		t.Fatalf("tab from input-mode Flow terminal writes = %#v, want z and tab", fakeTerm.writes)
	}
}

func TestModel_FlowTerminalInputModeForwardsCtrlGToAgent(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\a" {
		t.Fatalf("input mode should forward ctrl+g to the agent, writes = %#v", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if len(fakeTerm.writes) != 2 || fakeTerm.writes[1] != "z" {
		t.Fatalf("ctrl+g should leave the terminal in input mode, writes = %#v", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if len(fakeTerm.writes) != 2 {
		t.Fatalf("ctrl+] should return to command mode without forwarding, writes = %#v", fakeTerm.writes)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected terminate confirmation from command-mode x, got %d", m.Overlay())
	}
}

func TestModel_FlowTerminalCommandModeSendsLiteralCommandKey(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x1d" {
		t.Fatalf("ctrl+] in Flow command mode should send a literal ctrl+], writes = %#v", fakeTerm.writes)
	}
}

func TestModel_FlowTerminalTabLeavesFocusEvenAfterCommandKey(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{
		flowWithPhaseDetails(),
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusInProgress},
	})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x1d" {
		t.Fatalf("ctrl+] should send literal byte before tab leaves focus, writes = %#v", fakeTerm.writes)
	}
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("tab from command-mode terminal active pane = %d, want left pane", m.ActivePane())
	}
	if got := m.TransientError(); strings.Contains(got, "Unknown terminal prefix command") {
		t.Fatalf("tab should not be treated as an unknown terminal command, status = %q", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 1 {
		t.Fatalf("list focus should not forward j to terminal: %#v", fakeTerm.writes)
	}
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want Flow list focus", m.Mode())
	}
}

func TestModel_FlowTerminalFocusCyclesLeftRightWithoutWritingPTY(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{lines: []string{"flow first output"}, state: "running"},
		{lines: []string{"flow second output"}, state: "running"},
		{lines: []string{"flow third output"}, state: "running"},
	}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	for _, phaseID := range []string{"one", "two", "three"} {
		m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
			Command:      "codex",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha",
			FlowID:       "flow-1",
			FlowPhaseID:  phaseID,
			Headless:     true,
		}})
	}
	if starts != 3 {
		t.Fatalf("embedded terminal starts = %d, want 3", starts)
	}
	if view := m.View(); !strings.Contains(view, "flow third output") {
		t.Fatalf("newest Flow terminal should be active before cycling:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if view := m.View(); !strings.Contains(view, "flow second output") || strings.Contains(view, "flow third output") {
		t.Fatalf("left should cycle to previous Flow terminal:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if view := m.View(); !strings.Contains(view, "flow first output") || strings.Contains(view, "flow second output") {
		t.Fatalf("left should cycle to previous Flow terminal again:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if view := m.View(); !strings.Contains(view, "flow third output") || strings.Contains(view, "flow first output") {
		t.Fatalf("left should wrap to last Flow terminal:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if view := m.View(); !strings.Contains(view, "flow first output") || strings.Contains(view, "flow third output") {
		t.Fatalf("right should wrap to first Flow terminal:\n%s", view)
	}

	for i, term := range terms {
		if len(term.writes) != 0 {
			t.Fatalf("Flow terminal %d received arrow writes: %#v", i+1, term.writes)
		}
	}
}

func TestModel_FlowTerminalLeftRightNoOpWithOneTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"only flow output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		FlowID:       "flow-1",
		FlowPhaseID:  "implementation",
		Headless:     true,
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})

	if len(fakeTerm.writes) != 0 {
		t.Fatalf("single Flow terminal should not receive left/right writes: %#v", fakeTerm.writes)
	}
	if got := m.TransientError(); strings.Contains(got, "No embedded terminal") {
		t.Fatalf("single Flow terminal left/right should not report missing terminal: %q", got)
	}
	if view := m.View(); !strings.Contains(view, "only flow output") {
		t.Fatalf("single Flow terminal should remain active after left/right:\n%s", view)
	}
}

func TestModel_FlowEmbeddedTerminalDismissRenumbersTabs(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{lines: []string{"flow first output"}, state: "running"},
		{lines: []string{"flow second output"}, state: "running"},
		{lines: []string{"flow third output"}, state: "running"},
	}
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			if starts >= len(terms) {
				t.Fatalf("unexpected embedded terminal start %d", starts+1)
			}
			term := terms[starts]
			starts++
			return term, nil
		},
	})
	m = flowsInRightPane(t, m, flowRecordsForTerminals(len(terms)))

	launch := func(row int) {
		t.Helper()
		m = selectFlowRow(t, m, row)
		var cmd tea.Cmd
		m, cmd = flowLaunchFromKey(t, m)
		if cmd == nil {
			t.Fatal("g should prepare an embedded launch")
		}
		m, _ = update(m, cmd())
	}
	launch(0)
	launch(1)
	launch(2)
	if starts != 3 {
		t.Fatalf("embedded terminal starts = %d, want 3", starts)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	terms[1].state = "exited"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	view := m.View()
	for _, want := range []string{"1 codex implementation running", "2 codex implementation running", "flow first output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("renumbered Flow terminal view missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"3 codex", "flow second output"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("dismissed Flow terminal should not remain visible with %q:\n%s", unwanted, view)
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if view = m.View(); !strings.Contains(view, "flow third output") || strings.Contains(view, "flow first output") {
		t.Fatalf("switching to renumbered Flow terminal 2 should show former third terminal:\n%s", view)
	}
}

func TestModel_EmbeddedTerminalCloseUsesStableIdentityAcrossScopes(t *testing.T) {
	sessionTerm := &fakeEmbeddedTerminal{lines: []string{"session output"}, state: "exited"}
	flowTerm := &fakeEmbeddedTerminal{lines: []string{"flow output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			if ctx.ResumeSessionID != "" {
				return sessionTerm, nil
			}
			return flowTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())
	if view := m.View(); !strings.Contains(view, "1 codex implementation running") {
		t.Fatalf("Flow terminal should start with global tab 1:\n%s", view)
	}

	m, _ = switchTestMode(m, ui.ModeSessions)
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session", Branch: "feature/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if view := m.View(); !strings.Contains(view, "2 codex feature/session exited") {
		t.Fatalf("session terminal should use the next global tab number:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if strings.Contains(m.View(), "session output") {
		t.Fatalf("dismissed session terminal should be removed:\n%s", m.View())
	}

	m, _ = switchTestMode(m, ui.ModeFlows)
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{flowWithPhaseDetails()}, ListRequest: m.ListRequest(ui.ModeFlows)})
	view := m.View()
	for _, want := range []string{"1 codex implementation running", "flow output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Flow terminal should survive session close and global renumbering, missing %q:\n%s", want, view)
		}
	}
}

func TestModel_EmbeddedTerminalTerminateUsesStableIdentityAcrossScopes(t *testing.T) {
	sessionTerm := &fakeEmbeddedTerminal{lines: []string{"session output"}, state: "running"}
	flowTerm := &fakeEmbeddedTerminal{lines: []string{"flow output"}, state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			if ctx.ResumeSessionID != "" {
				return sessionTerm, nil
			}
			return flowTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, cmd := prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = switchTestMode(m, ui.ModeSessions)
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session", Branch: "feature/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("running session close should open confirmation, got command %T", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected terminate confirmation overlay, got %d", m.Overlay())
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected terminate confirmation command")
	}
	m, _ = update(m, cmd())

	if sessionTerm.State() != "terminated" {
		t.Fatalf("session terminal state = %q, want terminated", sessionTerm.State())
	}
	if flowTerm.State() != "running" {
		t.Fatalf("flow terminal state = %q, want running", flowTerm.State())
	}

	m, _ = switchTestMode(m, ui.ModeFlows)
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{flowWithPhaseDetails()}, ListRequest: m.ListRequest(ui.ModeFlows)})
	view := m.View()
	for _, want := range []string{"1 codex implementation running", "flow output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Flow terminal with matching display number should survive session terminate, missing %q:\n%s", want, view)
		}
	}
}

func TestModel_FlowListQuitWithRunningEmbeddedTerminalConfirmsAndTerminates(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Fatalf("quit with running flow terminal should open confirmation, got command %T", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected quit confirmation overlay, got %d", m.Overlay())
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit confirmation command")
	}
	_, cmd = update(m, cmd())
	if cmd == nil {
		t.Fatal("expected tea.Quit after confirmed flow terminal quit")
	}
	if fakeTerm.State() != "terminated" {
		t.Fatalf("terminal state = %q, want terminated", fakeTerm.State())
	}
}

func TestModel_LeftPaneQuitWithRunningEmbeddedTerminalConfirms(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Fatalf("left-pane quit with running terminal should open confirmation, got command %T", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected quit confirmation overlay, got %d", m.Overlay())
	}
}

func TestModel_GOnFlowPhaseAtEmbeddedTerminalCapMarksPhaseNeedsAttention(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	// Each terminal owns its Flow for as long as it is installed, so filling
	// the cap takes one Flow per terminal.
	m = flowsInRightPane(t, m, flowRecordsForTerminals(10))

	for i := 0; i < 9; i++ {
		m = selectFlowRow(t, m, i)
		var cmd tea.Cmd
		m, cmd = flowLaunchFromKey(t, m)
		if cmd == nil {
			t.Fatalf("launch %d should prepare an embedded launch", i+1)
		}
		m, _ = update(m, cmd())
	}
	if starts != 9 {
		t.Fatalf("embedded terminal starts = %d, want 9", starts)
	}
	if len(phaseUpdates) != 0 {
		t.Fatalf("phase updates before cap = %#v, want none", phaseUpdates)
	}

	m = selectFlowRow(t, m, 9)
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g at cap should still prepare a launch attempt")
	}
	m, asyncCmd := update(m, cmd())
	m = settleModelCommands(t, m, asyncCmd, 2)
	if starts != 9 {
		t.Fatalf("embedded terminal starts after cap = %d, want 9", starts)
	}
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one needs_attention update", phaseUpdates)
	}
	if got := phaseUpdates[0]; got.FlowID != "flow-10" ||
		got.PhaseID != "implementation" ||
		got.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(got.Notes, "Maximum embedded terminals") {
		t.Fatalf("phase update = %#v", got)
	}
	if got := m.TransientError(); !strings.Contains(got, "Maximum embedded terminals") {
		t.Fatalf("status = %q, want terminal cap message", got)
	}
}

func TestModel_EmbeddedTerminalCapCountsAcrossScopes(t *testing.T) {
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, flowRecordsForTerminals(4))
	for i := 0; i < 4; i++ {
		m = selectFlowRow(t, m, i)
		var cmd tea.Cmd
		m, cmd = flowLaunchFromKey(t, m)
		if cmd == nil {
			t.Fatalf("flow launch %d should prepare an embedded launch", i+1)
		}
		m, _ = update(m, cmd())
	}

	m, _ = switchTestMode(m, ui.ModeSessions)
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/one", Branch: "feature/one"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-2", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/two", Branch: "feature/two"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-3", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/three", Branch: "feature/three"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-4", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/four", Branch: "feature/four"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-5", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/five", Branch: "feature/five"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-6", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/six", Branch: "feature/six"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	openSessionIndex := func(index int, label string) {
		t.Helper()
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
		for range index {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
		}
		var cmd tea.Cmd
		m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("%s should return a command", label)
		}
		m, _ = update(m, cmd())
	}
	for i := 1; i < 5; i++ {
		openSessionIndex(i, "session picker selection")
	}
	if starts != 9 {
		t.Fatalf("embedded terminal starts = %d, want 9", starts)
	}

	openSessionIndex(5, "session picker selection at mixed-scope cap")
	if starts != 9 {
		t.Fatalf("embedded terminal starts after mixed-scope cap = %d, want 9", starts)
	}
	if got := m.TransientError(); !strings.Contains(got, "Maximum embedded terminals") {
		t.Fatalf("status = %q, want terminal cap message", got)
	}
}

func TestModel_FlowSplitListScrollKeepsSelectionVisible(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	flows := []flowstore.FlowRecord{
		{FlowID: "flow-1", RepoPath: "/dev/alpha", Branch: "flow/alpha", Title: "Flow alpha", Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}}},
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Branch: "flow/beta", Title: "Flow beta", Status: flowstore.StatusInProgress},
		{FlowID: "flow-3", RepoPath: "/dev/alpha", Branch: "flow/gamma", Title: "Flow gamma", Status: flowstore.StatusInProgress},
		{FlowID: "flow-4", RepoPath: "/dev/alpha", Branch: "flow/delta", Title: "Flow delta", Status: flowstore.StatusInProgress},
	}
	m = flowsInRightPane(t, m, flows)

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	for i := 0; i < 3; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.FlowSelected() != 3 {
		t.Fatalf("flow selection = %d, want 3", m.FlowSelected())
	}
	view := m.View()
	if !strings.Contains(view, "flow/delta") {
		t.Fatalf("selected flow should be visible in the split list:\n%s", view)
	}
}

func TestModel_GLaunchesFlowPhaseImplementationWithoutLinkedPlanContext(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Implementation without a linked plan should not read plan %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-no-plan",
		Branch:       "flow/no-plan",
		Commit:       "112233",
		Title:        "Implement without linked plan",
		Instructions: "Ship the requested tiny CLI flag.",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseSkipped, Notes: "User asked to skip a saved plan."},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseSkipped, Notes: "User approved direct implementation without a saved plan."},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an implementation launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	for _, want := range []string{
		"Implement the Flow instructions.",
		"Worktree: /dev/alpha-worktrees/flow-no-plan",
		"Branch: flow/no-plan",
		"Start commit: 112233",
		"Ship the requested tiny CLI flag.",
		"Prior Plan context:",
		"User asked to skip a saved plan.",
		"Plan Review context:",
		"User approved direct implementation without a saved plan.",
	} {
		if !strings.Contains(launched.InitialPrompt, want) {
			t.Fatalf("implementation prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
	if strings.Contains(launched.InitialPrompt, "Plan: \n") {
		t.Fatalf("implementation prompt should not include an empty plan path:\n%s", launched.InitialPrompt)
	}
}

func TestModel_FlowPromptTemplateReplacesSupportedPlaceholders(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		FlowPromptTemplates: model.FlowPromptTemplates{
			Implementation: "Custom {phase_id} for {flow_id}: {plan_path} @ {worktree_path} on {branch} from {commit}; keep {unknown}",
		},
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("templated Implementation launch should not pre-read %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-template",
		Branch:       "flow/template",
		Commit:       "c0ffee",
		PlanID:       "plan-1",
		PlanPath:     "/state/plans/plan-1/plan.md",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "implementation")
	if cmd == nil {
		t.Fatal("g should prepare an implementation launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	want := appendFlowDoneInstructionForTest("Custom implementation for flow-1: /state/plans/plan-1/plan.md @ /dev/alpha-worktrees/flow-template on flow/template from c0ffee; keep {unknown}")
	if launched.InitialPrompt != want {
		t.Fatalf("templated flow prompt = %q, want %q", launched.InitialPrompt, want)
	}
}

func TestModel_SelectedReadyFlowPhaseAdvertisesLaunchPhaseAction(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-ready",
		Title:        "Ready implementation",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	view := m.View()
	if strings.Contains(view, "launch phase") {
		t.Fatalf("Flow row should not expose phase launch action:\n%s", view)
	}
	if !strings.Contains(view, "phases") {
		t.Fatalf("Flow row should expose phase expansion action:\n%s", view)
	}

	m = selectFlowPhaseByID(t, m, "implementation")
	view = m.View()
	if !strings.Contains(view, "g      launch next") && !strings.Contains(view, "g: launch next") {
		t.Fatalf("selected ready Flow phase should expose next launch action:\n%s", view)
	}
}

func TestModel_GWithoutReadyFlowPhaseShowsNoLaunchableStatus(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-review",
		Title:        "Review requested changes",
		Status:       flowstore.StatusNeedsAttention,
		PlanID:       "plan-1",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseNeedsAttention, Outcome: "changes_requested", Notes: "Clarify rollout steps."},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")
	if view := m.View(); strings.Contains(view, "launch phase") || strings.Contains(view, "launch next") {
		t.Fatalf("gated selected phase should not expose launch action:\n%s", view)
	}

	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected status expiry command for not-ready flow launch")
	}
	if status := m.TransientError(); status != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", status)
	}
}

func TestModel_GLaunchesFlowPhaseReviewLoopWithFirstLevelPrompt(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach/sessions/v1",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Review Loop launch should pass metadata without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-review-loop",
		Branch:       "flow/review-loop",
		Commit:       "def456",
		Title:        "Review implementation",
		Instructions: "Custom flow instructions from the user.",
		Status:       flowstore.StatusInProgress,
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/sessions/v1/plans/plan-1/plan.md",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Summary: "Implemented the main slice."},
			{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API integration", Status: flowstore.PhaseCompleted, Summary: "Added child API."},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "review-loop")
	if cmd == nil {
		t.Fatal("g should prepare a review-loop launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.FlowID != "flow-1" || launched.FlowPhaseID != "review-loop" || launched.PlanID != "plan-1" {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Use the review-loop workflow with goal: review-and-revise.",
		"Use the commit skill when revisions are made.",
		"Use the approach-flow skill to record the Review Loop result before finishing; the phase is not done until the result is persisted.",
		"",
		"Worktree: /dev/alpha-worktrees/flow-review-loop",
		"Branch: flow/review-loop",
		"Start commit: def456",
	}, "\n"))
	if launched.InitialPrompt != wantPrompt {
		t.Fatalf("review-loop prompt = %q, want %q", launched.InitialPrompt, wantPrompt)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"/dev/alpha-worktrees/flow-review-loop",
		"flow/review-loop",
		"def456",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-loop prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
	for _, unwanted := range []string{
		"custom flow instructions from the user",
		"first-level implementation review",
		"implementation-api",
		"api integration",
		"# saved plan",
		"approach flow phase set",
		"--status completed",
		"--status needs_attention",
		"--status blocked",
	} {
		if strings.Contains(strings.ToLower(launched.InitialPrompt), unwanted) {
			t.Fatalf("review-loop prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_FlowReviewLoopPromptTemplateOverridesBuiltInPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		FlowPromptTemplates: model.FlowPromptTemplates{
			ReviewLoop: "Custom {phase_id} for {flow_id}: {worktree_path} on {branch} from {commit}; plan {plan_path}; keep {unknown}",
		},
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("templated Review Loop launch should not pre-read %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-review-template",
		Branch:       "flow/review-template",
		Commit:       "baddad",
		PlanID:       "plan-1",
		PlanPath:     "/state/plans/plan-1/plan.md",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "review-loop")
	if cmd == nil {
		t.Fatal("g should prepare a review-loop launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	want := appendFlowDoneInstructionForTest("Custom review-loop for flow-1: /dev/alpha-worktrees/flow-review-template on flow/review-template from baddad; plan /state/plans/plan-1/plan.md; keep {unknown}")
	if launched.InitialPrompt != want {
		t.Fatalf("templated review-loop prompt = %q, want %q", launched.InitialPrompt, want)
	}
	if strings.Contains(launched.InitialPrompt, "review-and-revise") {
		t.Fatalf("templated review-loop prompt should not include built-in goal wording:\n%s", launched.InitialPrompt)
	}
}

func TestModel_GLaunchesFlowPhasePRCreationWithMinimalPrompt(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach/sessions/v1",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("PR Creation launch should pass metadata without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
		Commit:       "abc789",
		Title:        "Create PR",
		Instructions: "Custom flow instructions from the user.",
		Status:       flowstore.StatusInProgress,
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/sessions/v1/plans/plan-1/plan.md",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Summary: "Implemented the main slice."},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted, Summary: "No blocking findings."},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "pr-creation")
	if cmd == nil {
		t.Fatal("g should prepare a pr-creation launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "pr-creation" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.FlowID != "flow-1" || launched.FlowPhaseID != "pr-creation" || launched.PlanID != "plan-1" {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Use the ship skill to create a PR for the changes.",
		"After the PR exists, run `approach flow pr set --flow-id flow-1 --provider github --number <number> --url <url> --head flow/pr --base <base>` before completing this phase.",
		"",
		"Worktree: /dev/alpha-worktrees/flow-pr",
		"Branch: flow/pr",
		"Start commit: abc789",
	}, "\n"))
	if launched.InitialPrompt != wantPrompt {
		t.Fatalf("pr-creation prompt = %q, want %q", launched.InitialPrompt, wantPrompt)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, unwanted := range []string{
		"custom flow instructions from the user",
		"implemented the main slice",
		"no blocking findings",
		"# saved plan",
		"approach flow phase set",
		"advance this phase",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("pr-creation prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GLaunchesFlowPhasePRCreationWithStructuredMetadataPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("PR Creation launch should pass metadata without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
		Commit:       "abc789",
		PlanID:       "plan-1",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "pr-creation")
	if cmd == nil {
		t.Fatal("g should prepare a pr-creation launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Use the ship skill to create a PR for the changes.",
		"After the PR exists, run `approach flow pr set --flow-id flow-1 --provider github --number <number> --url <url> --head flow/pr --base <base>` before completing this phase.",
		"",
		"Worktree: /dev/alpha-worktrees/flow-pr",
		"Branch: flow/pr",
		"Start commit: abc789",
	}, "\n"))
	if launched.InitialPrompt != wantPrompt {
		t.Fatalf("pr-creation prompt = %q, want %q", launched.InitialPrompt, wantPrompt)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, unwanted := range []string{
		"flow phase: pr creation",
		"approach flow phase set",
		"--status completed",
		"--status blocked",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("pr-creation prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GLaunchesFlowPhaseAutoreviewWithPRContext(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Autoreview launch should pass PR metadata without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
		Commit:       "ghi789",
		PlanID:       "plan-1",
		Status:       flowstore.StatusInProgress,
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     115,
			URL:        "https://github.com/approachcontrol/approach/pull/115",
			HeadBranch: "flow/pr",
			BaseBranch: "main",
			Status:     "open",
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "autoreview")
	if cmd == nil {
		t.Fatal("g should prepare an autoreview launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"second-level review",
		"use the ship skill when fixes require commits or pushes",
		"use the approach-flow skill to record the autoreview result before finishing",
		"worktree: /dev/alpha-worktrees/flow-pr",
		"branch: flow/pr",
		"start commit: ghi789",
		"pr target:",
		"github #115",
		"https://github.com/approachcontrol/approach/pull/115",
		"head: flow/pr",
		"base: main",
		"status: open",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("autoreview prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
	for _, unwanted := range []string{"saved plan body", "approach flow phase", "--status completed", "--status needs_attention", "--status blocked"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("autoreview prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GLaunchesFlowPhaseAutoreviewWithRecoveryPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("Autoreview recovery launch should pass PR metadata without pre-reading %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
		Commit:       "ghi789",
		PlanID:       "plan-1",
		Status:       flowstore.StatusNeedsAttention,
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     115,
			URL:        "https://github.com/approachcontrol/approach/pull/115",
			HeadBranch: "flow/pr",
			BaseBranch: "main",
			Status:     "open",
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseNeedsAttention, Outcome: "needs_attention", Notes: "Non-blocking concern remains."},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "autoreview")
	if cmd == nil {
		t.Fatal("g should prepare an autoreview relaunch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"second-level review",
		"use the ship skill when fixes require commits or pushes",
		"use the approach-flow skill to record the autoreview result before finishing",
		"worktree: /dev/alpha-worktrees/flow-pr",
		"branch: flow/pr",
		"start commit: ghi789",
		"github #115",
		"head: flow/pr",
		"base: main",
		"status: open",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("autoreview recovery prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
	for _, unwanted := range []string{"restart required", "--status running", "rerunning autoreview", "approach flow phase"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("autoreview recovery prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GDoesNotRelaunchFlowPhaseAutoreviewWithoutPRTarget(t *testing.T) {
	var launchAttempted bool
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddFlowPhaseLaunchID() should not run without PR metadata: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchAttempted = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
		Status:       flowstore.StatusBlocked,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseBlocked, Outcome: "blocked", Notes: "PR target missing."},
		},
	}})

	m = selectFlowPhaseByID(t, m, "autoreview")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected status expiry command for blocked autoreview without PR metadata")
	}
	if launchAttempted {
		t.Fatal("LaunchAgent() ran without PR metadata")
	}
	if got := m.TransientError(); got != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", got)
	}
}

func TestModel_GDoesNotRelaunchFlowPhaseAutoreviewWhenPredecessorsAreUnsatisfied(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddFlowPhaseLaunchID() should not run while predecessors are unsatisfied: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("LaunchAgent() should not run while predecessors are unsatisfied: %#v", ctx)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
		Status:       flowstore.StatusBlocked,
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     115,
			URL:        "https://github.com/approachcontrol/approach/pull/115",
			HeadBranch: "flow/pr",
			BaseBranch: "main",
			Status:     "open",
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhasePending},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseBlocked, Outcome: "blocked", Notes: "Needs another review."},
		},
	}})

	m = selectFlowPhaseByID(t, m, "autoreview")
	m, cmd := flowLaunchFromKey(t, m)
	if cmd == nil {
		t.Fatal("expected status expiry command for blocked autoreview with unsatisfied predecessors")
	}
	if got := m.TransientError(); got != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", got)
	}
}

func TestModel_GLaunchesFlowPhaseMergeWithStructuredReportingPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			t.Fatalf("merge launch should not read plan body for %q", planID)
			return "", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			launched = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-merge",
		Branch:       "flow/merge",
		Commit:       "abc123",
		PlanID:       "plan-1",
		Status:       flowstore.StatusInProgress,
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     116,
			URL:        "https://github.com/approachcontrol/approach/pull/116",
			HeadBranch: "flow/merge",
			BaseBranch: "main",
			Status:     "open",
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseCompleted, Outcome: "passed"},
			{PhaseID: "merge", Title: "Merge", Status: flowstore.PhaseReady},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "merge")
	if cmd == nil {
		t.Fatal("g should prepare a merge launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	if launched.FlowPhaseID != "merge" {
		t.Fatalf("launched flow phase = %q, want merge", launched.FlowPhaseID)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"merge the pr deliberately",
		"github #116",
		"https://github.com/approachcontrol/approach/pull/116",
		"approach flow merge set --flow-id flow-1 --status merged",
		"--commit <merge-commit>",
		"--merged-at <rfc3339>",
		"approach flow phase set --flow-id flow-1 --phase-id merge --status completed",
		"blocked",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("merge prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
}

func TestModel_GUsesFlowPhaseOrderingForReadyLaunch(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			return "# Saved plan\n", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("Flow phase CLI launch should start an embedded terminal, not LaunchAgent: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-child",
		PlanID:       "plan-1",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 4},
			{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API integration", Status: flowstore.PhaseReady, Order: 10},
		},
	}})

	m, cmd := prepareSelectedFlowPhaseHeadlessOffLaunch(t, m, "implementation-api")
	if cmd == nil {
		t.Fatal("g should prepare a child phase launch")
	}
	runPreparedFlowEmbeddedLaunch(t, m, cmd)

	if launchUpdate.PhaseID != "implementation-api" {
		t.Fatalf("launched phase = %q, want child phase before review-loop", launchUpdate.PhaseID)
	}
}

func TestModel_OffViewCustomPlanReviewLaunchFailureMarksBlocked(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
	})

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{
			FlowID:        "flow-1",
			FlowPhaseID:   "design-review",
			FlowPhaseKind: flowstore.KindPlanReview,
			RepoPath:      "/dev/alpha",
		},
		Err: "terminal failed",
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	_ = cmd()

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "design-review" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		phaseUpdate.Outcome != flowstore.OutcomeBlocked ||
		!strings.Contains(phaseUpdate.Notes, "terminal failed") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_FlowAgentResultFailureMarksPlanReviewBlocked(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: filter.RepoPath, Title: "T", Status: flowstore.StatusBlocked}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "T"}})

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan-review", RepoPath: "/dev/alpha"},
		Err:           "terminal failed",
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	m = settleModelCommands(t, m, cmd, 2)

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan-review" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		phaseUpdate.Outcome != flowstore.OutcomeBlocked ||
		!strings.Contains(phaseUpdate.Notes, "terminal failed") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_NewFlowOpensSingleCreationForm(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("expected opening new-flow title prompt to return no command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("overlay = %d, want form overlay", m.Overlay())
	}
	form := m.FormView()
	if form.Purpose != "flow-create" || form.Title != "New flow" {
		t.Fatalf("form identity = %#v", form)
	}
	if form.FocusIndex != 0 {
		t.Fatalf("focus index = %d, want title field", form.FocusIndex)
	}
	if len(form.Fields) != 5 {
		t.Fatalf("fields = %#v, want title, instructions, base ref, headless, plan now", form.Fields)
	}
	want := []struct {
		id          string
		kind        ui.FormFieldKind
		label       string
		placeholder string
		checked     bool
	}{
		{id: "title", kind: ui.FormText, label: "Title", placeholder: ui.FlowTitleInputPlaceholder},
		{id: "instructions", kind: ui.FormMultilineText, label: "Instructions", placeholder: ui.FlowInstructionsInputPlaceholder},
		{id: "base-ref", kind: ui.FormText, label: "Base ref", placeholder: ui.FlowBaseRefInputPlaceholder},
		{id: "headless", kind: ui.FormCheckbox, label: "Headless", checked: true},
		{id: "plan-now", kind: ui.FormCheckbox, label: "Plan Now", checked: true},
	}
	for i, wantField := range want {
		field := form.Fields[i]
		if field.ID != wantField.id || field.Kind != wantField.kind || field.Label != wantField.label || field.Placeholder != wantField.placeholder {
			t.Fatalf("field %d = %#v, want %#v", i, field, wantField)
		}
		if field.Kind == ui.FormCheckbox {
			if field.Checked != wantField.checked {
				t.Fatalf("field %d initial checked = %v, want %v", i, field.Checked, wantField.checked)
			}
			continue
		}
		if field.Value != "" || field.Cursor != 0 {
			t.Fatalf("field %d initial state = value %q cursor %d, want empty at start", i, field.Value, field.Cursor)
		}
	}
}

func TestModel_NewFlowDelegatesStartAndLaunchesPlanAgent(t *testing.T) {
	for _, command := range []string{"codex", "claude"} {
		t.Run(command, func(t *testing.T) {
			wantEffort := "xhigh"
			if command == "claude" {
				wantEffort = "max"
			}
			var startRequest model.FlowStartRequest
			var started actions.AgentLaunchContext
			var startWidth, startHeight int
			var calls []string
			m := newTestModel(testRepos(), model.Options{
				AgentCommand:          command,
				CodexReasoningEffort:  "xhigh",
				ClaudeReasoningEffort: "max",
				SessionStateRoot:      "/state/approach/sessions/v1",
				StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					calls = append(calls, "start-flow")
					startRequest = req
					if req.RepoPath != "/dev/alpha" || req.Title != "Add Flow Mode" || req.Instructions != "Build\nthe thing" || req.BaseRef != "main" {
						t.Fatalf("StartFlowPlan request = %#v", req)
					}
					if req.ReasoningEffort != wantEffort {
						t.Fatalf("StartFlowPlan reasoning effort = %q, want %q", req.ReasoningEffort, wantEffort)
					}
					return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
						Command:          req.AgentCommand,
						LaunchID:         "launch-1",
						RepoPath:         req.RepoPath,
						WorktreePath:     "/dev/alpha-worktrees/flow-add-flow-mode",
						Branch:           "flow/add-flow-mode",
						Commit:           "abc123",
						SessionStateRoot: req.SessionStateRoot,
						PlanPhaseID:      "plan",
						PlanPhaseTitle:   "Plan",
						PlanPhaseStatus:  flowstore.PhaseRunning,
						FlowID:           "flow-1",
						FlowPhaseID:      "plan",
						Headless:         flowStartRequestHeadless(req),
						ReasoningEffort:  req.ReasoningEffort,
						InitialPrompt:    "Use the approach-flow skill for this launch.\n\nBuild\nthe thing\n\nCreate and persist the plan with approach plan save, link it back with approach flow plan set.",
					}}, nil
				},
				LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					t.Fatal("new Flow CLI launch should start an embedded terminal, not external launcher")
					return actions.TerminalLaunchSpec{}, nil
				},
				StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
					calls = append(calls, "start-embedded")
					started = ctx
					startWidth = width
					startHeight = height
					return &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}, nil
				},
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return nil, nil
				},
			})
			m = inRightPane(m)
			m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 24})
			m, _ = switchTestMode(m, ui.ModeFlows)

			m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build\nthe thing", "main")
			if cmd == nil {
				t.Fatal("expected flow creation command")
			}
			msg := cmd()
			launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
			if !ok {
				t.Fatalf("creation command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
			}
			if launchMsg.Request == 0 {
				t.Fatal("embedded launch message should be tagged with the active create request")
			}
			m, cmd = update(m, launchMsg)
			if cmd == nil {
				t.Fatal("expected embedded launch repaint/fetch command")
			}
			if batch, ok := cmd().(tea.BatchMsg); !ok || len(batch) < 2 {
				t.Fatalf("embedded Flow launch command = %T, %#v; want batched repaint and refresh", cmd, batch)
			}

			if strings.Join(calls, ",") != "start-flow,start-embedded" {
				t.Fatalf("call order = %#v", calls)
			}
			if startRequest.AgentCommand != command ||
				startRequest.SessionStateRoot != "/state/approach/sessions/v1" ||
				startRequest.Headless == nil || !*startRequest.Headless ||
				startRequest.PlanPhaseID != "" ||
				startRequest.PlanPhaseTitle != "" ||
				startRequest.PlanPhaseStatus != "" ||
				startRequest.ReasoningEffort != wantEffort {
				t.Fatalf("start request metadata = %#v", startRequest)
			}
			if started.Command != command ||
				started.RepoPath != "/dev/alpha" ||
				started.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
				started.Branch != "flow/add-flow-mode" ||
				started.Commit != "abc123" ||
				started.SessionStateRoot != "/state/approach/sessions/v1" ||
				started.FlowID != "flow-1" ||
				started.FlowPhaseID != "plan" ||
				started.PlanPhaseID != "plan" ||
				started.PlanPhaseTitle != "Plan" ||
				started.PlanPhaseStatus != flowstore.PhaseRunning ||
				started.LaunchID != "launch-1" ||
				started.ReasoningEffort != wantEffort ||
				!started.Embedded ||
				!started.Headless ||
				!started.FlowLaunchTracked {
				t.Fatalf("embedded launch context = %#v", started)
			}
			if startWidth <= 0 || startHeight <= 0 {
				t.Fatalf("embedded terminal size = %dx%d, want positive", startWidth, startHeight)
			}
			prompt := strings.ToLower(started.InitialPrompt)
			for _, want := range []string{"approach-flow", "build\nthe thing", "create and persist the plan", "approach plan save", "approach flow plan set"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("launch prompt missing %q: %q", want, started.InitialPrompt)
				}
			}
			for _, unwanted := range []string{"flow-1", "flow/add-flow-mode", "/dev/alpha-worktrees/flow-add-flow-mode", "base ref", "add flow mode"} {
				if strings.Contains(prompt, strings.ToLower(unwanted)) {
					t.Fatalf("launch prompt should not include metadata %q: %q", unwanted, started.InitialPrompt)
				}
			}
			if view := m.View(); !strings.Contains(view, "agent output") {
				t.Fatalf("flow terminal view missing agent output:\n%s", view)
			}
		})
	}
}

func TestModel_NewFlowPlanNowLetsStarterChooseCustomRootPhase(t *testing.T) {
	var startRequest model.FlowStartRequest
	var started actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			startRequest = req
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-1",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-research",
				Branch:       "flow/research",
				FlowID:       "flow-1",
				FlowPhaseID:  "research",
				PlanPhaseID:  "research",
			}}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("CLI Flow launch should use embedded terminal")
			return actions.TerminalLaunchSpec{}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPrompts(t, m, "Research Flow", "Plan research", "main")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	launchMsg, ok := cmd().(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", launchMsg)
	}
	m, _ = update(m, launchMsg)

	if startRequest.PlanPhaseID != "" || startRequest.PlanPhaseTitle != "" || startRequest.PlanPhaseStatus != "" {
		t.Fatalf("TUI create path forced phase metadata: %#v", startRequest)
	}
	if started.FlowPhaseID != "research" || started.PlanPhaseID != "research" {
		t.Fatalf("started phase IDs = flow %q plan %q, want research", started.FlowPhaseID, started.PlanPhaseID)
	}
}

func TestModel_NewFlowPlanNowOffCreatesFlowWithoutLaunch(t *testing.T) {
	var createRequest model.FlowStartRequest
	embeddedCalls := 0
	externalCalls := 0
	listed := false
	m := newTestModel(testRepos(), model.Options{
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createRequest = req
			return model.FlowStartResult{Flow: flowstore.FlowRecord{
				FlowID:       "flow-parked",
				Title:        req.Title,
				Instructions: req.Instructions,
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-parked",
				Branch:       "flow/parked",
				Commit:       "abc123",
				Phases:       []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}},
			}}, nil
		},
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Plan Now off should not start the plan phase")
			return model.FlowStartResult{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			externalCalls++
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			embeddedCalls++
			return &fakeEmbeddedTerminal{}, nil
		},
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listed = true
			if filter.RepoPath != "/dev/alpha" {
				t.Fatalf("flow filter = %#v", filter)
			}
			return []flowstore.FlowRecord{{FlowID: "flow-parked", RepoPath: filter.RepoPath, Title: "Parked Flow"}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPromptsWithCreateOptions(t, m, "Parked Flow", "Plan later", "main", false, false)
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	created, ok := msg.(model.FlowCreatedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreatedMsg", msg)
	}
	if created.Request == 0 {
		t.Fatal("created message should be tagged with active create request")
	}
	m, cmd = update(m, created)
	if cmd == nil {
		t.Fatal("expected flow refresh command after parked Flow creation")
	}
	_ = runLeadingBatchCmd(t, cmd)

	if createRequest.RepoPath != "/dev/alpha" ||
		createRequest.Title != "Parked Flow" ||
		createRequest.Instructions != "Plan later" ||
		createRequest.BaseRef != "main" ||
		createRequest.AgentCommand != "" ||
		createRequest.PlanPhaseStatus != "" ||
		createRequest.Headless == nil || *createRequest.Headless {
		t.Fatalf("create request = %#v", createRequest)
	}
	if embeddedCalls != 0 || externalCalls != 0 {
		t.Fatalf("Plan Now off should not launch; embedded=%d external=%d", embeddedCalls, externalCalls)
	}
	if got := model.ActiveFlowCreateForTest(m); got != 0 {
		t.Fatalf("active create request = %d, want cleared", got)
	}
	if !listed {
		t.Fatal("expected Flow surface refresh")
	}
}

func TestModel_NewFlowLaunchNormalizesConfiguredAgentCommandForPlanAndTerminal(t *testing.T) {
	var startRequest model.FlowStartRequest
	var started actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:          "codex",
		ClaudeReasoningEffort: "max",
		SessionStateRoot:      "/state/approach/sessions/v1",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			startRequest = req
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:          req.AgentCommand,
				LaunchID:         "launch-1",
				RepoPath:         req.RepoPath,
				WorktreePath:     "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:           "flow/add-flow-mode",
				SessionStateRoot: req.SessionStateRoot,
				FlowID:           "flow-1",
				FlowPhaseID:      "plan",
				Headless:         flowStartRequestHeadless(req),
				ReasoningEffort:  req.ReasoningEffort,
			}}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("new Flow CLI launch should start an embedded terminal, not external launcher")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m, _ = update(m, model.AgentSetMsg{Command: " CLAUDE "})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPromptsWithOptions(t, m, "Add Flow Mode", "Build the thing", "main", true)
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected embedded launch repaint/fetch command")
	}

	if startRequest.AgentCommand != "claude" || startRequest.ReasoningEffort != "max" {
		t.Fatalf("start request agent settings = command %q effort %q, want claude/max", startRequest.AgentCommand, startRequest.ReasoningEffort)
	}
	if started.Command != "claude" || started.ReasoningEffort != "max" || !started.Embedded || !started.Headless {
		t.Fatalf("embedded launch context = %#v, want normalized claude/max headless launch", started)
	}
}

func TestModel_NewFlowCLIPlanLaunchUsesCheckedHeadlessOption(t *testing.T) {
	for _, command := range []string{"codex", "claude"} {
		t.Run(command, func(t *testing.T) {
			var started actions.AgentLaunchContext
			m := newTestModel(testRepos(), model.Options{
				AgentCommand:     command,
				SessionStateRoot: "/state/approach/sessions/v1",
				StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
						Command:          req.AgentCommand,
						LaunchID:         "launch-1",
						RepoPath:         req.RepoPath,
						WorktreePath:     "/dev/alpha-worktrees/flow-interactive-plan",
						Branch:           "flow/interactive-plan",
						SessionStateRoot: req.SessionStateRoot,
						FlowID:           "flow-1",
						FlowPhaseID:      "plan",
						Headless:         flowStartRequestHeadless(req),
					}}, nil
				},
				LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					t.Fatalf("new Flow CLI launch should start an embedded terminal, not external launcher: %#v", ctx)
					return actions.TerminalLaunchSpec{}, nil
				},
				StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
					started = ctx
					return &fakeEmbeddedTerminal{state: "running"}, nil
				},
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return nil, nil
				},
			})
			m = inRightPane(m)
			m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
			m, _ = switchTestMode(m, ui.ModeFlows)
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			if cmd != nil {
				t.Fatalf("h before new Flow launch returned command %T, want nil", cmd)
			}

			m, cmd = submitNewFlowPromptsWithOptions(t, m, "Headless Plan", "Write the plan", "main", true)
			if cmd == nil {
				t.Fatal("expected flow creation command")
			}
			msg := cmd()
			launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
			if !ok {
				t.Fatalf("creation command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
			}
			m, cmd = update(m, launchMsg)
			if cmd == nil {
				t.Fatal("expected embedded launch repaint/fetch command")
			}
			if started.Command != command ||
				started.FlowID != "flow-1" ||
				started.FlowPhaseID != "plan" ||
				!started.Embedded ||
				!started.Headless ||
				!started.FlowLaunchTracked {
				t.Fatalf("headless new Flow plan launch context = %#v", started)
			}
		})
	}
}

func TestModel_NewFlowInteractiveCLIPlanLaunchFocusesTerminalInput(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	var started actions.AgentLaunchContext
	m := newTestModel(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach/sessions/v1",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:          req.AgentCommand,
				LaunchID:         "launch-1",
				RepoPath:         req.RepoPath,
				WorktreePath:     "/dev/alpha-worktrees/flow-interactive-plan",
				Branch:           "flow/interactive-plan",
				SessionStateRoot: req.SessionStateRoot,
				FlowID:           "flow-1",
				FlowPhaseID:      "plan",
				Headless:         flowStartRequestHeadless(req),
				InitialPrompt:    "Create and persist the plan.",
			}}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("new Flow CLI launch should start an embedded terminal, not external launcher: %#v", ctx)
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = ctx
			return fakeTerm, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 24})
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPromptsWithOptions(t, m, "Interactive Plan", "Write the plan", "main", false)
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("creation command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected embedded launch repaint/fetch command")
	}
	m = settleModelCommands(t, m, cmd, 2)
	if started.FlowPhaseID != "plan" || started.Headless || !started.Embedded || !started.FlowLaunchTracked {
		t.Fatalf("interactive new Flow plan launch context = %#v", started)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	wantPrefill := "\x1b[200~Create and persist the plan.\x1b[201~"
	if len(fakeTerm.writes) != 2 || fakeTerm.writes[0] != wantPrefill || fakeTerm.writes[1] != "z" {
		t.Fatalf("interactive new Flow plan launch should focus terminal input: %#v", fakeTerm.writes)
	}
}

func TestModel_NewFlowFormCancelDoesNotStartOrLeaveActiveCreateRequest(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		t.Run(key.String(), func(t *testing.T) {
			startCalls := 0
			embeddedCalls := 0
			externalCalls := 0
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
					startCalls++
					return model.FlowStartResult{}, nil
				},
				LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					externalCalls++
					return actions.TerminalLaunchSpec{}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					embeddedCalls++
					return &fakeEmbeddedTerminal{}, nil
				},
			})
			m = inRightPane(m)
			m, _ = switchTestMode(m, ui.ModeFlows)
			m = openNewFlowForm(t, m, "Cancel Flow", "Write the plan", "main")

			m, cmd := update(m, key)
			if cmd != nil {
				t.Fatalf("cancel returned command %T, want nil", cmd)
			}
			if m.Overlay() != ui.OverlayNone {
				t.Fatalf("overlay after cancel = %d, want none", m.Overlay())
			}
			if startCalls != 0 || embeddedCalls != 0 || externalCalls != 0 {
				t.Fatalf("cancel should not start work; start=%d embedded=%d external=%d", startCalls, embeddedCalls, externalCalls)
			}
			if got := model.ActiveFlowCreateForTest(m); got != 0 {
				t.Fatalf("active Flow create request after cancel = %d, want 0", got)
			}
		})
	}
}

func TestModel_NewFlowLaunchesAfterStartPlanReturns(t *testing.T) {
	var calls []string
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			calls = append(calls, "start-flow")
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-1",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
				FlowID:       "flow-1",
				FlowPhaseID:  "plan",
			}}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("new Flow CLI launch should start an embedded terminal, not external launcher")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			calls = append(calls, "start-embedded")
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "main")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, _ = update(m, launchMsg)

	if strings.Join(calls, ",") != "start-flow,start-embedded" {
		t.Fatalf("call order = %#v", calls)
	}
}

func TestModel_NewFlowPlanNowParksFlowWhenLaunchSkipped(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{
				Flow:          flowstore.FlowRecord{FlowID: "flow-parked", RepoPath: req.RepoPath, Title: req.Title},
				LaunchSkipped: true,
			}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("launch-skipped Flow should not launch an external agent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			t.Fatal("launch-skipped Flow should not start an embedded terminal")
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = switchTestMode(m, ui.ModeFlows)

	_, cmd := submitNewFlowPrompts(t, m, "Merge-only Flow", "Track the merge", "main")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	raw := cmd()
	msg, ok := raw.(model.FlowCreatedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreatedMsg", raw)
	}
	if msg.FlowID != "flow-parked" || msg.Title != "Merge-only Flow" || msg.Request == 0 {
		t.Fatalf("FlowCreatedMsg = %#v", msg)
	}
}

func TestModel_NewFlowStaleLaunchRecoversPhaseAfterRepoChange(t *testing.T) {
	embeddedStarts := 0
	externalLaunches := 0
	releases := 0
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReserveFlowLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{FlowID: flowID}, func() {}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			if req.RepoPath != "/dev/alpha" {
				t.Fatalf("StartFlowPlan repo = %q, want /dev/alpha", req.RepoPath)
			}
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-1",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-stale",
				Branch:       "flow/stale",
				FlowID:       "flow-1",
				FlowPhaseID:  "plan",
			}, LaunchRelease: func() { releases++ }}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			externalLaunches++
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			embeddedStarts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, createCmd := submitNewFlowPrompts(t, m, "Stale Flow", "Do the stale thing", "main")
	if createCmd == nil {
		t.Fatal("expected flow creation command")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	staleMsg := createCmd()
	if launchMsg, ok := staleMsg.(model.FlowEmbeddedLaunchRequestedMsg); !ok || launchMsg.Request == 0 {
		t.Fatalf("creation command returned %#v, want tagged FlowEmbeddedLaunchRequestedMsg", staleMsg)
	}
	m, cmd := update(m, staleMsg)
	if cmd == nil {
		t.Fatal("stale launch returned no phase recovery command")
	}
	if releases != 0 {
		t.Fatalf("reservation released before stale phase recovery: %d", releases)
	}
	m, _ = update(m, cmd())
	if embeddedStarts != 0 || externalLaunches != 0 {
		t.Fatalf("stale flow creation launch should be ignored after repo change; embedded=%d external=%d", embeddedStarts, externalLaunches)
	}
	if releases != 1 || len(phaseUpdates) != 1 || phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("stale embedded recovery: releases=%d updates=%#v", releases, phaseUpdates)
	}

	requestless := model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
		Command:      "codex",
		LaunchID:     "launch-requestless",
		RepoPath:     "/dev/bravo",
		WorktreePath: "/dev/bravo-worktrees/flow-ready",
		Branch:       "flow/ready",
		FlowID:       "flow-ready",
		FlowPhaseID:  "implementation",
	}}
	_, cmd = update(m, requestless)
	if cmd == nil {
		t.Fatal("requestless embedded launch should still start while a create request is pending")
	}
	if embeddedStarts != 1 {
		t.Fatalf("requestless embedded launch starts = %d, want 1", embeddedStarts)
	}
	if externalLaunches != 0 {
		t.Fatalf("external launches = %d, want 0", externalLaunches)
	}
}

func TestModel_NewFlowStaleParkedCreateIgnoredAfterRepoChange(t *testing.T) {
	listCalls := 0
	m := newTestModel(testRepos(), model.Options{
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			if req.RepoPath != "/dev/alpha" {
				t.Fatalf("CreateFlow repo = %q, want /dev/alpha", req.RepoPath)
			}
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-parked", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return nil, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("stale parked Flow creation should not launch an external agent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			t.Fatal("stale parked Flow creation should not start an embedded terminal")
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, createCmd := submitNewFlowPromptsWithCreateOptions(t, m, "Stale Parked", "Plan later", "main", false, false)
	if createCmd == nil {
		t.Fatal("expected parked Flow creation command")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	staleMsg := createCmd()
	if created, ok := staleMsg.(model.FlowCreatedMsg); !ok || created.Request == 0 {
		t.Fatalf("creation command returned %#v, want tagged FlowCreatedMsg", staleMsg)
	}
	next, cmd := update(m, staleMsg)
	if cmd != nil {
		t.Fatalf("stale parked create returned command %T, want nil", cmd)
	}
	if got := model.ActiveFlowCreateForTest(next); got == 0 {
		t.Fatal("stale parked create should leave current create request untouched")
	}
	if listCalls != 0 {
		t.Fatalf("list calls = %d, want stale result ignored", listCalls)
	}
}

func TestModel_NewFlowPlanNowOnRequiresAgentAtSubmit(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("expected no command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("overlay = %d, want form", m.Overlay())
	}

	m = fillNewFlowForm(t, m, "Needs Agent", "Plan now", "main")
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("submit without agent returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("overlay after validation = %d, want form", m.Overlay())
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatalf("expected missing-agent guidance in form:\n%s", m.View())
	}
}

func TestModel_NewFlowPlanNowOffAllowsNoAgentConfigured(t *testing.T) {
	var createRequest model.FlowStartRequest
	m := newTestModel(testRepos(), model.Options{
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createRequest = req
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-parked", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Plan Now off should not validate or start an agent")
			return model.FlowStartResult{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPromptsWithCreateOptions(t, m, "No Agent Parked", "Plan later", "main", false, false)
	if cmd == nil {
		t.Fatal("expected parked Flow creation command")
	}
	msg := cmd()
	created, ok := msg.(model.FlowCreatedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreatedMsg", msg)
	}
	if created.FlowID != "flow-parked" {
		t.Fatalf("created FlowID = %q, want flow-parked", created.FlowID)
	}
	if createRequest.AgentCommand != "" || createRequest.ReasoningEffort != "" {
		t.Fatalf("create request should not include agent settings: %#v", createRequest)
	}
}

func TestModel_NewFlowStartFailureReportsError(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{}, errors.New("Bootstrap hook failed: missing env file")
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("agent should not launch after start failure")
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	_, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg, ok := cmd().(model.FlowCreateFailedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreateFailedMsg", msg)
	}

	if !strings.Contains(msg.Err, "Bootstrap hook failed") || !strings.Contains(msg.Err, "missing env file") {
		t.Fatalf("error = %q, want bootstrap failure", msg.Err)
	}
}

func TestModel_NewFlowStaleStartFailureIgnoredAfterRepoChange(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			if req.RepoPath != "/dev/alpha" {
				t.Fatalf("StartFlowPlan repo = %q, want /dev/alpha", req.RepoPath)
			}
			return model.FlowStartResult{}, errors.New("Bootstrap hook failed: missing env file")
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("agent should not launch after start failure")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			t.Fatal("embedded terminal should not start after start failure")
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, createCmd := submitNewFlowPrompts(t, m, "Stale Failure", "Build the stale thing", "main")
	if createCmd == nil {
		t.Fatal("expected flow creation command")
	}
	staleMsg := createCmd()
	failed, ok := staleMsg.(model.FlowCreateFailedMsg)
	if !ok {
		t.Fatalf("creation command returned %T, want FlowCreateFailedMsg", staleMsg)
	}
	if failed.Request == 0 {
		t.Fatal("flow create failure should be tagged with the active create request")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	next, cmd := update(m, staleMsg)
	if cmd != nil {
		t.Fatalf("stale flow create failure returned command %T, want nil", cmd)
	}
	if got := next.TransientError(); got != "" {
		t.Fatalf("stale flow create failure set status %q, want empty", got)
	}
}

func TestModel_NewFlowWorktreeFailureReportsStartFailure(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{}, errors.New("branch exists")
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	_, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	failed, ok := msg.(model.FlowCreateFailedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreateFailedMsg", msg)
	}

	if !strings.Contains(failed.Err, "branch exists") {
		t.Fatalf("error = %q, want worktree failure", failed.Err)
	}
}

func TestModel_NewFlowWorktreeFailureReportsBlockedPhaseUpdateFailure(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{}, errors.New("branch exists; mark flow blocked: disk full")
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	_, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg, ok := cmd().(model.FlowCreateFailedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreateFailedMsg", msg)
	}
	if !strings.Contains(msg.Err, "branch exists") || !strings.Contains(msg.Err, "mark flow blocked: disk full") {
		t.Fatalf("error = %q, want worktree and flow-update failures", msg.Err)
	}
}

func TestModel_NewFlowLaunchFailureMarksPlanNeedsAttention(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-1",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
				FlowID:       "flow-1",
				FlowPhaseID:  "plan",
			}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("new Flow CLI launch should start an embedded terminal, not external launcher")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("pty unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, asyncCmd := update(m, launchMsg)
	m = settleModelCommands(t, m, asyncCmd, 2)

	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one launch failure update", phaseUpdates)
	}
	update := phaseUpdates[0]
	if update.FlowID != "flow-1" ||
		update.PhaseID != "plan" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "pty unavailable") {
		t.Fatalf("phase update = %#v", update)
	}
	if got := m.TransientError(); !strings.Contains(got, "pty unavailable") {
		t.Fatalf("status = %q, want PTY error", got)
	}
}

func TestModel_NewFlowAtEmbeddedTerminalCapMarksPlanNeedsAttention(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	starts := 0
	m := newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ReserveFlowLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{FlowID: flowID}, func() {}, nil
		},
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-new-flow",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
				FlowID:       "flow-1",
				FlowPhaseID:  "plan",
			}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("new Flow CLI launch should start an embedded terminal, not external launcher")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = switchTestMode(m, ui.ModeFlows)

	for i := 0; i < 9; i++ {
		m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
			Command:      "codex",
			LaunchID:     "launch-existing",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/flow-existing",
			FlowID:       "flow-existing",
			FlowPhaseID:  "implementation",
			Headless:     true,
		}})
	}
	if starts != 9 {
		t.Fatalf("embedded terminal starts = %d, want 9", starts)
	}
	if len(phaseUpdates) != 0 {
		t.Fatalf("phase updates before cap = %#v, want none", phaseUpdates)
	}

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, asyncCmd := update(m, launchMsg)
	m = settleModelCommands(t, m, asyncCmd, 2)

	if starts != 9 {
		t.Fatalf("embedded terminal starts after cap = %d, want 9", starts)
	}
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one needs_attention update", phaseUpdates)
	}
	if got := phaseUpdates[0]; got.FlowID != "flow-1" ||
		got.PhaseID != "plan" ||
		got.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(got.Notes, "Maximum embedded terminals") {
		t.Fatalf("phase update = %#v", got)
	}
	if got := m.TransientError(); !strings.Contains(got, "Maximum embedded terminals") {
		t.Fatalf("status = %q, want terminal cap message", got)
	}
}

func TestModel_FlowAgentResultFailureMarksPhaseAndRefreshesFlows(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	var listed bool
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listed = true
			if filter.RepoPath != "/dev/alpha" {
				t.Fatalf("flow filter = %#v", filter)
			}
			return []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: filter.RepoPath, Title: "T", Status: flowstore.StatusNeedsAttention}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan"},
		Err:           "agent exited",
		Detached:      true,
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	m = settleModelCommands(t, m, cmd, 2)
	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan" ||
		phaseUpdate.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(phaseUpdate.Notes, "agent exited") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
	if !listed {
		t.Fatal("expected ListFlows to run")
	}
	if got := m.Flows(); len(got) != 1 || got[0].Status != flowstore.StatusNeedsAttention {
		t.Fatalf("flows = %#v", got)
	}
}

func TestModel_FlowAgentResultFailureReportsPhaseUpdateFailure(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: filter.RepoPath, Title: "T"}}, nil
		},
		SetFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan"},
		Err:           "agent exited",
		Detached:      true,
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	m = settleModelCommands(t, m, cmd, 2)
	if got := m.TransientError(); !strings.Contains(got, "agent exited") || !strings.Contains(got, "update flow phase: state root locked") {
		t.Fatalf("status = %q, want launch and phase-update failures", got)
	}
}

func submitNewFlowPrompts(t *testing.T, m model.Model, title, instructions, baseRef string) (model.Model, tea.Cmd) {
	t.Helper()
	return submitNewFlowPromptsWithOptions(t, m, title, instructions, baseRef, true)
}

func submitNewFlowPromptsWithOptions(t *testing.T, m model.Model, title, instructions, baseRef string, headless bool) (model.Model, tea.Cmd) {
	t.Helper()
	return submitNewFlowPromptsWithCreateOptions(t, m, title, instructions, baseRef, headless, true)
}

func submitNewFlowPromptsWithCreateOptions(t *testing.T, m model.Model, title, instructions, baseRef string, headless, planNow bool) (model.Model, tea.Cmd) {
	t.Helper()
	m = openNewFlowForm(t, m, title, instructions, baseRef)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !headless {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeySpace})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !planNow {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeySpace})
	}
	return update(m, tea.KeyMsg{Type: tea.KeyEnter})
}

func openNewFlowForm(t *testing.T, m model.Model, title, instructions, baseRef string) model.Model {
	t.Helper()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("overlay = %d, want new Flow form", m.Overlay())
	}
	return fillNewFlowForm(t, m, title, instructions, baseRef)
}

func fillNewFlowForm(t *testing.T, m model.Model, title, instructions, baseRef string) model.Model {
	t.Helper()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(title)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	lines := strings.Split(instructions, "\n")
	for i, line := range lines {
		if line != "" {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(line)})
		}
		if i < len(lines)-1 {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
		}
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if baseRef != "" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(baseRef)})
	}
	return m
}

func TestModel_NewFlowParkedCreateCapturesAgentSettings(t *testing.T) {
	var createRequest model.FlowStartRequest
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeModel:           "claude-opus-5",
		ClaudeReasoningEffort: "high",
		CodexModel:            "gpt-5.5",
		CodexReasoningEffort:  "medium",
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createRequest = req
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-parked", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Plan Now off should not start an agent")
			return model.FlowStartResult{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = switchTestMode(m, ui.ModeFlows)

	_, cmd := submitNewFlowPromptsWithCreateOptions(t, m, "Parked With Agent", "Plan later", "main", false, false)
	if cmd == nil {
		t.Fatal("expected parked Flow creation command")
	}
	cmd()
	if createRequest.AgentCommand != "claude" {
		t.Fatalf("AgentCommand = %q, want claude", createRequest.AgentCommand)
	}
	if createRequest.Model != "claude-opus-5" {
		t.Fatalf("Model = %q, want claude-opus-5", createRequest.Model)
	}
	if createRequest.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", createRequest.ReasoningEffort)
	}
}
