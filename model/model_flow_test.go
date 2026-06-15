package model_test

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func flowsInRightPane(t *testing.T, m model.Model, records []flowstore.FlowRecord) model.Model {
	t.Helper()
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 18})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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

func enterActiveFlowsWithRecords(t *testing.T, m model.Model, records []flowstore.FlowRecord) model.Model {
	t.Helper()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	m, _ = update(m, model.ActiveFlowResultMsg{Flows: records, ListRequest: m.ListRequest(ui.ModeFlows)})
	return m
}

func flowWithPhaseDetails() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Flow with phases",
		Status:   flowstore.StatusInProgress,
		Branch:   "flow/with-phases",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}
}

func TestModel_F3ShowsGlobalActiveFlowsWithoutPollutingFlowsCache(t *testing.T) {
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
	m := flowsInRightPane(t, model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return []flowstore.FlowRecord{alpha, bravo, merged}, nil
		},
	}), []flowstore.FlowRecord{alpha})
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 30})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %v, want worktrees before F3", m.Mode())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF3})
	if cmd == nil {
		t.Fatal("F3 should start or continue a Flow refresh")
	}
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	if gotFilter.RepoPath != "" {
		t.Fatalf("active Flow filter RepoPath = %q, want global", gotFilter.RepoPath)
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %v, want preserved worktrees mode", m.Mode())
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"active flows", "Alpha Flow", "Bravo Flow", "f3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("active-flow view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Merged Flow") {
		t.Fatalf("active-flow view should filter merged flows:\n%s", view)
	}
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "alpha-flow" {
		t.Fatalf("normal Flows() cache = %#v, want original repo-scoped alpha flow", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "worktrees") || strings.Contains(view, "Bravo Flow") {
		t.Fatalf("second F3 should restore worktrees pane:\n%s", view)
	}
}

func TestModel_F3GlobalResultSurvivesRepoMoveAndUsesLeftPaneFilter(t *testing.T) {
	alpha := flowWithPhaseDetails()
	alpha.FlowID = "alpha-flow"
	alpha.Title = "Alpha Flow"
	bravo := flowWithPhaseDetails()
	bravo.FlowID = "bravo-flow"
	bravo.RepoPath = "/dev/bravo"
	bravo.Title = "Bravo Flow"

	var filters []flowstore.FlowFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{alpha, bravo}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 220, Height: 24})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF3})
	result := activeFlowResultFromCommand(t, cmd)
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("switching active flows to repo pane returned command %T, want nil", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("repo movement in active flows returned command %T, want local filter only", cmd)
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

func TestModel_F3GlobalFetchErrorSurvivesRepoMove(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("state unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 18})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF3})
	if cmd == nil {
		t.Fatal("expected active Flow fetch command")
	}
	msg := cmd()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, repoMoveCmd := update(m, tea.KeyMsg{Type: tea.KeyDown})
	if repoMoveCmd != nil {
		t.Fatalf("repo movement in active flows returned command %T, want nil", repoMoveCmd)
	}

	m, _ = update(m, msg)
	if got := m.TransientError(); !strings.Contains(got, "failed to load active flows") || !strings.Contains(got, "state unavailable") {
		t.Fatalf("status = %q, want active-flow fetch error after repo move", got)
	}
}

func TestModel_F3LeftPaneEnterExitsToSelectedRepoMode(t *testing.T) {
	var filters []flowstore.FlowFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{{FlowID: "flow", RepoPath: filter.RepoPath, Title: "Repo Flow", Status: flowstore.StatusPending}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 18})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	m, _ = update(m, flowResultFromCommand(t, cmd))

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyF3})
	m, _ = update(m, activeFlowResultFromCommand(t, cmd))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected normal Flow-mode fetch after left-pane enter")
	}
	m, _ = update(m, flowResultFromCommand(t, cmd))
	view := ansi.Strip(m.View())
	if strings.Contains(view, "Shortcuts  Active flows") {
		t.Fatalf("left-pane enter should exit active flows:\n%s", view)
	}
	if len(filters) < 3 || filters[len(filters)-1].RepoPath != "/dev/bravo" {
		t.Fatalf("flow filters = %#v, want final normal fetch for selected bravo repo", filters)
	}
	if m.ActivePane() != 0 || m.Mode() != ui.ModeFlows {
		t.Fatalf("active pane/mode = %d/%d, want left pane in flows mode", m.ActivePane(), m.Mode())
	}
}

func TestModel_F3UsesSeparateActiveFlowSelectionForActions(t *testing.T) {
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
	m := flowsInRightPane(t, model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd == nil {
		t.Fatal("expected active Flow launch command")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
	if len(launches) != 1 || launches[0].LaunchContext.FlowID != "active-two" {
		t.Fatalf("launches = %#v, want active-two", launches)
	}
	if launched.FlowID != "active-two" || launched.PhaseID != "implementation" {
		t.Fatalf("launch update = %#v, want active-two implementation", launched)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	view = ansi.Strip(m.View())
	if m.Mode() != ui.ModeFlows || m.FlowSelected() != 1 || !strings.Contains(view, "Merged Flow") {
		t.Fatalf("normal Flow mode should retain merged selection after F3 exit; selected=%d view:\n%s", m.FlowSelected(), view)
	}
}

func TestModel_F3ActiveFlowRefreshPreparesAutoLaunch(t *testing.T) {
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("active Flow refresh should return auto-launch command")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
	if len(launches) != 1 {
		t.Fatalf("active Flow refresh returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].FlowID != "flow-1" || updates[0].PhaseID != "implementation" || updates[0].LaunchID == "" {
		t.Fatalf("launch updates = %#v, want implementation auto launch", updates)
	}
	launch := launches[0]
	if launch.LaunchContext.FlowID != "flow-1" ||
		launch.LaunchContext.FlowPhaseID != "implementation" ||
		!launch.LaunchContext.Embedded ||
		!launch.LaunchContext.Headless ||
		!launch.LaunchContext.FlowLaunchTracked {
		t.Fatalf("active Flow launch context = %#v", launch.LaunchContext)
	}
}

func TestModel_F3ActiveFlowLKeyClampsAtFlowSurface(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if cmd != nil {
		t.Fatalf("l from active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() != 1 {
		t.Fatalf("l from active Flow surface active pane = %d, want right pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("l from active Flow surface changed underlying mode = %d, want worktrees", m.Mode())
	}
}

func TestModel_F3ActiveFlowLeftKeyClampsAndPreservesUnderlyingMode(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if cmd != nil {
		t.Fatalf("left from active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() != 1 {
		t.Fatalf("left from active Flow surface active pane = %d, want right pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeSessions {
		t.Fatalf("left from active Flow surface changed underlying mode = %d, want sessions", m.Mode())
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Active flows") {
		t.Fatalf("left from active Flow surface should keep active Flow view visible:\n%s", view)
	}
}

func TestModel_F3ActiveFlowLeftPaneNumberedKeysAreNoOps(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		t.Fatalf("left-pane numbered key on active Flow surface returned command %T, want nil", cmd)
	}
	if m.ActivePane() != 0 {
		t.Fatalf("left-pane numbered key activePane = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeSessions {
		t.Fatalf("left-pane numbered key changed underlying mode = %d, want sessions", m.Mode())
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Active flows") {
		t.Fatalf("left-pane numbered key should keep active Flow view visible:\n%s", view)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_F3ActiveFlowFetchErrorUsesActiveFetchMode(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), nil)
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 18})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, nil)

	m, _ = update(m, model.FetchErrorMsg{
		Pane:        "active-flows",
		Err:         "failed to load active flows: boom",
		Kind:        model.FetchList,
		Mode:        ui.ModeFlows,
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "failed to load active flows: boom") {
		t.Fatalf("active Flow surface should show Flow fetch failure in status bar:\n%s", view)
	}
	if !strings.Contains(view, "Could not load flows; see status bar") {
		t.Fatalf("empty active Flow surface should show Flow fetch failure placeholder:\n%s", view)
	}
}

func TestModel_F3ActiveFlowSearchAcceptsDigits(t *testing.T) {
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

func TestModel_F3ActiveFlowPullDoesNotTargetHiddenWorktree(t *testing.T) {
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
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
	if got := m.FlowSelected(); got != 1 {
		t.Fatalf("normal Flow selection after active refresh = %d, want preserved active-two", got)
	}
}

func TestModel_RKeyResumesActiveFlowPhaseSession(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui",
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

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected active Flow phase resume command")
	}
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

func TestModel_InteractiveActiveFlowLaunchFocusesEmbeddedTerminal(t *testing.T) {
	var started actions.AgentLaunchContext
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	flow := flowWithPhaseDetails()
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd == nil {
		t.Fatal("expected active Flow launch command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, _ = update(m, launchMsg)
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd == nil {
		t.Fatal("expected active Flow launch command")
	}
	launchMsg, ok := cmd().(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned non-launch message")
	}
	m, _ = update(m, launchMsg)
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "flow/with-phases") || strings.Contains(view, "codex-session-1") {
		t.Fatalf("active flows should visually override sessions mode:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd == nil {
		t.Fatal("expected active Flow launch command over Sessions")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, _ = update(m, launchMsg)
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
		Phases:       phases,
	}
}

func autoLaunchFromFlowRefresh(t *testing.T, previous, current flowstore.FlowRecord) (model.FlowEmbeddedLaunchRequestedMsg, []flowstore.PhaseLaunchUpdate) {
	t.Helper()
	cmd, updates := autoLaunchCommandFromFlowRefresh(t, previous, current)
	if cmd == nil {
		t.Fatal("Flow refresh should return auto-launch command")
	}
	msg := cmd()
	launch, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("auto-launch command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	return launch, *updates
}

func autoLaunchCommandFromFlowRefresh(t *testing.T, previous, current flowstore.FlowRecord) (tea.Cmd, *[]flowstore.PhaseLaunchUpdate) {
	t.Helper()
	var updates []flowstore.PhaseLaunchUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	_, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	return cmd, &updates
}

func flowEmbeddedLaunchesFromCommand(t *testing.T, cmd tea.Cmd) []model.FlowEmbeddedLaunchRequestedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	msgs := []tea.Msg{msg}
	if batch, ok := msg.(tea.BatchMsg); ok {
		msgs = msgs[:0]
		for _, subcmd := range batch {
			msgs = append(msgs, subcmd())
		}
	}
	launches := make([]model.FlowEmbeddedLaunchRequestedMsg, 0)
	for _, msg := range msgs {
		if launch, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg); ok {
			launches = append(launches, launch)
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "claude",
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})

	_, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("Flow refresh should return auto-launch command")
	}
	msg := cmd()
	launch, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("auto-launch command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	if !launchUpdate.AutoLaunch || launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID == "" {
		t.Fatalf("auto launch update = %#v", launchUpdate)
	}
	if launch.LaunchContext.Command != "claude" ||
		launch.LaunchContext.ReasoningEffort != "max" ||
		launch.LaunchContext.FlowID != "flow-1" ||
		launch.LaunchContext.FlowPhaseID != "implementation" ||
		!launch.LaunchContext.Embedded ||
		!launch.LaunchContext.Headless ||
		!launch.LaunchContext.FlowLaunchTracked {
		t.Fatalf("auto launch context = %#v", launch.LaunchContext)
	}
}

func TestModel_FlowAutoLaunchWithCodexAppUsesExternalRouteWithoutEffort(t *testing.T) {
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
	var launched actions.AgentLaunchContext
	startEmbeddedRan := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:         "codex-app",
		CodexReasoningEffort: "high",
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
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			startEmbeddedRan = true
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})

	m, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("Flow refresh should return auto-launch command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("auto-launch command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	if !launchUpdate.AutoLaunch || launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID == "" {
		t.Fatalf("auto launch update = %#v", launchUpdate)
	}
	if launchMsg.LaunchContext.Command != "codex-app" ||
		launchMsg.LaunchContext.ReasoningEffort != "" ||
		launchMsg.LaunchContext.FlowID != "flow-1" ||
		launchMsg.LaunchContext.FlowPhaseID != "implementation" ||
		launchMsg.LaunchContext.Embedded ||
		launchMsg.LaunchContext.Headless ||
		launchMsg.LaunchContext.FlowLaunchTracked {
		t.Fatalf("codex-app auto launch context = %#v", launchMsg.LaunchContext)
	}

	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected external codex-app agent result command")
	}
	_ = cmd()
	if startEmbeddedRan {
		t.Fatal("codex-app auto launch should not start an embedded terminal")
	}
	if launched.Command != "codex-app" ||
		launched.ReasoningEffort != "" ||
		launched.FlowID != "flow-1" ||
		launched.FlowPhaseID != "implementation" ||
		launched.Embedded ||
		launched.Headless ||
		launched.FlowLaunchTracked {
		t.Fatalf("codex-app external launch context = %#v", launched)
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
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		if len(batch) == 0 {
			t.Fatal("flow fetch batch was empty")
		}
		return batch[0]()
	}
	return msg
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
	m, cmd := update(m, flowLaunchKey())
	return m, cmd
}

func prepareSelectedFlowPhaseHeadlessOffLaunch(t *testing.T, m model.Model, phaseID string) (model.Model, tea.Cmd) {
	t.Helper()
	m = selectFlowPhaseByID(t, m, phaseID)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd != nil {
		t.Fatalf("h before Flow phase launch returned command %T, want nil", cmd)
	}
	m, cmd = update(m, flowLaunchKey())
	return m, cmd
}

func runPreparedFlowEmbeddedLaunch(t *testing.T, m model.Model, cmd tea.Cmd) model.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("Flow phase launch should return a command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected embedded Flow launch command")
	}
	_ = cmd()
	return m
}

func prepareSelectedFlowPhaseEmbeddedLaunch(t *testing.T, m model.Model, phaseID string) (model.Model, tea.Cmd) {
	t.Helper()
	m = selectFlowPhaseByID(t, m, phaseID)
	m, cmd := update(m, flowLaunchKey())
	return m, cmd
}

func flowLaunchKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
}

func TestModel_MKeyTogglesFlowAutoModeFromFlowRow(t *testing.T) {
	flow := flowWithPhaseDetails()
	updated := flow
	updated.AutoMode = true
	var calls []flowstore.AutoModeUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowAutoMode: func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on selected Flow row should persist auto-mode toggle")
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

func TestModel_MKeyTogglesFlowAutoModeFromPhaseRowAndPreservesSelection(t *testing.T) {
	flow := flowWithPhaseDetails()
	flow.AutoMode = true
	updated := flow
	updated.AutoMode = false
	var calls []flowstore.AutoModeUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowAutoMode: func(update flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, update)
			return updated, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on selected Flow phase should persist auto-mode toggle")
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

func TestModel_MKeyFlowAutoModeFailureReportsStatus(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowAutoMode: func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("m on selected Flow row should return persistence command")
	}
	m, _ = update(m, cmd())
	if got := m.TransientError(); !strings.Contains(got, "failed to set Flow auto mode") || !strings.Contains(got, "state root locked") {
		t.Fatalf("status = %q, want persistence failure", got)
	}
	if got := m.Flows(); len(got) != 1 || got[0].AutoMode {
		t.Fatalf("Flows() = %#v, want unchanged auto mode", got)
	}
}

func TestModel_MKeyFlowAutoModeNoopsWithoutSelectedFlow(t *testing.T) {
	called := false
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowAutoMode: func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
			called = true
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, nil)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatalf("m without selected Flow returned command %T, want nil", cmd)
	}
	if called {
		t.Fatal("SetFlowAutoMode should not be called without a selected Flow")
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

func TestModel_SelectedSessionMismatchFlowPhaseHidesResetShortcut(t *testing.T) {
	flow := flowWithAwaitingImplementation()
	flow.Phases[2].LaunchIDs = []string{"launch-orphan"}
	flow.Phases[2].Sessions = []flowstore.Session{
		{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"},
	}
	resetCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
		ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
			resetCalled = true
			return flowstore.FlowRecord{}, nil
		},
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
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || m.Overlay() != ui.OverlayNone {
		t.Fatalf("x on session-mismatch phase returned cmd=%T overlay=%d", cmd, m.Overlay())
	}
	if resetCalled {
		t.Fatal("reset should not be called for session-mismatch phase")
	}
}

func TestModel_XKeyOnResettableFlowPhaseConfirmsAndResets(t *testing.T) {
	awaiting := flowWithAwaitingImplementation()
	reset := flowWithPhaseDetails()
	var resetCalls []flowstore.PhaseResetUpdate
	listCalls := 0
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	if fetchCmd != nil {
		t.Fatalf("reset failure returned refresh command %T, want nil", fetchCmd)
	}
	if got := m.TransientError(); !strings.Contains(got, "state root locked") {
		t.Fatalf("status after reset failure = %q, want persistence error", got)
	}
}

func TestModel_XKeyIgnoredWhenSelectedFlowPhaseIsNotAwaitingSession(t *testing.T) {
	resetCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

func TestModel_ResetShortcutHiddenAfterLegacyPhaseIDLaunchNormalizes(t *testing.T) {
	legacy := flowWithPhaseDetails()
	legacy.Phases[2].PhaseID = "Implementation"
	awaiting := flowWithAwaitingImplementation()
	var started actions.AgentLaunchContext
	resetCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if cmd == nil {
		t.Fatal("expected flows fetch command")
	}
	request := m.ListRequest(ui.ModeFlows)
	firstCmd := cmd

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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
	listCalls := 0
	sourceTerm := &fakeEmbeddedTerminal{lines: []string{"source output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd != nil {
		t.Fatalf("completed phase with running terminal returned auto-launch command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates = %#v, want none while source terminal runs", updates)
	}

	sourceTerm.state = "exited"
	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd != nil {
		t.Fatalf("refresh before exited terminal auto-closes returned command %T, want nil", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates before exited terminal auto-closes = %#v, want none", updates)
	}

	m, cmd = update(m, model.EmbeddedTerminalTickMsgForTest(m))
	if cmd == nil {
		t.Fatal("exiting the deferred source terminal should schedule a refresh")
	}
	refresh := flowResultFromCommand(t, cmd)
	if listCalls != 1 {
		t.Fatalf("ListFlows calls after exit tick command = %d, want 1", listCalls)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates before exit-triggered refresh = %#v, want none", updates)
	}
	if strings.Contains(m.View(), "source output") || model.HasRunningFlowEmbeddedTerminalForPhaseForTest(m, "flow-1", "plan-review") {
		t.Fatalf("source terminal should be dismissed after exited tick:\n%s", m.View())
	}

	m, cmd = update(m, refresh)
	if cmd == nil {
		t.Fatal("exit-triggered refresh should prepare the deferred auto launch")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
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
	listCalls := 0
	var updates []flowstore.PhaseLaunchUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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
	if listCalls != 1 {
		t.Fatalf("ListFlows calls after exit tick command = %d, want 1", listCalls)
	}

	m, cmd = update(m, refresh)
	if cmd == nil {
		t.Fatal("exit-triggered refresh should prepare auto launch after observing completion")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
	if len(launches) != 1 {
		t.Fatalf("exit-triggered refresh returned %d embedded launches, want 1", len(launches))
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
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
	refresh := flowResultFromCommand(t, cmd)
	if listCalls != 1 {
		t.Fatalf("ListFlows calls after exit tick command = %d, want 1", listCalls)
	}
	m, cmd = update(m, refresh)
	if cmd == nil {
		t.Fatal("refresh after source terminal auto-close should prepare deferred launch")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
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
			m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{rerun},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd != nil {
		t.Fatalf("new launch running refresh returned command %T, want nil", cmd)
	}
	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{completed},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("completion for newer launch ID should auto-launch next phase")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
	if len(launches) != 1 {
		t.Fatalf("new launch completion returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after newer completion = %#v, want implementation auto launch", updates)
	}
}

func TestModel_FlowAutoModeStaleTerminalDoesNotBlockLaterLaunchID(t *testing.T) {
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{rerun},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd != nil {
		t.Fatalf("new launch running refresh returned command %T, want nil", cmd)
	}
	m, cmd = update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{completed},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("completion for newer launch ID should auto-launch next phase despite stale terminal")
	}
	launches := flowEmbeddedLaunchesFromCommand(t, cmd)
	if len(launches) != 1 {
		t.Fatalf("new launch completion returned %d embedded launches, want 1", len(launches))
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after newer completion = %#v, want implementation auto launch", updates)
	}
	if !strings.Contains(m.View(), "old failed output") {
		t.Fatalf("stale failed terminal should remain visible:\n%s", m.View())
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
		URL:        "https://github.com/brian-bell/wtui/pull/115",
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previousOne, previousTwo})

	_, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{currentOne, currentTwo},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("two eligible auto-mode flows should return a batched launch command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("auto launch command returned %T len=%d, want BatchMsg with two commands", msg, len(batch))
	}
	var launchedFlowIDs []string
	for _, subcmd := range batch {
		raw := subcmd()
		launch, ok := raw.(model.FlowEmbeddedLaunchRequestedMsg)
		if !ok {
			t.Fatalf("batched auto launch returned %T, want FlowEmbeddedLaunchRequestedMsg", raw)
		}
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:         "codex",
		AddFlowPhaseLaunchID: store.AddPhaseLaunchID,
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	_, cmd := update(m, model.FlowResultMsg{
		RepoPath:    "/dev/alpha",
		Flows:       []flowstore.FlowRecord{current},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	if cmd == nil {
		t.Fatal("auto mode completion should prepare a launch command before auto mode is disabled")
	}
	if _, err := store.SetAutoMode(flowstore.AutoModeUpdate{FlowID: current.FlowID, Enabled: false}); err != nil {
		t.Fatalf("SetAutoMode(false) error = %v", err)
	}

	if msg := cmd(); msg != nil {
		t.Fatalf("stale auto launch command returned %T, want nil", msg)
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

	cmd, updates := autoLaunchCommandFromFlowRefresh(t, previous, current)
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

			cmd, updates := autoLaunchCommandFromFlowRefresh(t, previous, current)
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{previous})
	request := m.ListRequest(ui.ModeFlows)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})

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

func TestModel_StartsInFlowsModeAndFetchesSelectedRepoFlows(t *testing.T) {
	var gotFilter flowstore.FlowFilter
	want := []flowstore.FlowRecord{
		{FlowID: "flow-1", Title: "Default Flow mode", RepoPath: "/dev/alpha", Branch: "flow/default", Status: flowstore.StatusPending},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartupMode: ui.ModeFlows,
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})

	if m.Mode() != ui.ModeFlows {
		t.Fatalf("startup mode = %d, want flows", m.Mode())
	}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected startup flows fetch command")
	}
	msg := flowResultFromCommand(t, cmd)
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("Flows() = %#v, want %#v", got, want)
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeReasoningEffort: "max",
		SessionStateRoot:      "/state/wtui/sessions/v1",
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
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 2},
		},
	}})
	m = selectFlowPhaseByID(t, m, "review-loop")

	m, cmd := update(m, flowLaunchKey())
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
	if started.Command != "claude" || started.ReasoningEffort != "max" {
		t.Fatalf("Flow phase launch agent settings = command %q effort %q, want claude/max", started.Command, started.ReasoningEffort)
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

func TestModel_GWithFocusedFlowTerminalWritesInputWithoutLaunchingPhase(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	addLaunchRan := false
	launchAgentRan := false
	starts := 0
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd := update(m, flowLaunchKey())
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
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand:     command,
				SessionStateRoot: "/state/wtui/sessions/v1",
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
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			if cmd != nil {
				t.Fatalf("h before Flow phase launch returned command %T, want nil", cmd)
			}

			m, cmd = update(m, flowLaunchKey())
			if cmd == nil {
				t.Fatal("g should prepare an embedded interactive launch")
			}
			m, cmd = update(m, cmd())
			if cmd == nil {
				t.Fatal("expected embedded Flow launch to return repaint/fetch command")
			}
			if started.Command != command ||
				started.FlowID != "flow-1" ||
				started.FlowPhaseID != "implementation" ||
				started.WorktreePath != "/dev/alpha-worktrees/flow-interactive" ||
				started.Branch != "flow/interactive" ||
				started.Commit != "abc123" ||
				started.SessionStateRoot != "/state/wtui/sessions/v1" {
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd != nil {
		t.Fatalf("h before Flow phase launch returned command %T, want nil", cmd)
	}
	m, cmd = update(m, flowLaunchKey())
	if cmd == nil {
		t.Fatal("g should prepare an embedded interactive launch")
	}
	m, cmd = update(m, cmd())
	if cmd == nil {
		t.Fatal("expected embedded Flow launch to return repaint/fetch command")
	}

	wantWrite := "\x1b[200~" + appendFlowDoneInstructionForTest("Alpha\nBeta\tOmegaDone") + "\x1b[201~"
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != wantWrite {
		t.Fatalf("prefill writes = %#v, want exact sanitized bracketed paste %q", fakeTerm.writes, wantWrite)
	}
}

func TestModel_GOnSelectedNonLaunchableFlowPhaseLaunchesFirstReadySibling(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var started actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd = update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, cmd := update(m, flowLaunchKey())
	if cmd != nil {
		t.Fatalf("g without a launchable phase returned command %T, want nil", cmd)
	}
	if addLaunchRan || launchAgentRan || startEmbeddedRan {
		t.Fatalf("g without launchable phase launched: add=%v launch=%v embedded=%v", addLaunchRan, launchAgentRan, startEmbeddedRan)
	}
	if got := m.TransientError(); got != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", got)
	}
}

func TestModel_CtrlJWithLaunchableFlowPhaseDoesNotMutateOrLaunch(t *testing.T) {
	addLaunchRan := false
	launchAgentRan := false
	startEmbeddedRan := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
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
		key  rune
		mode ui.Mode
	}{
		{name: "worktrees", key: '1', mode: ui.ModeWorktrees},
		{name: "branches", key: '2', mode: ui.ModeBranches},
		{name: "plans", key: '7', mode: ui.ModePlans},
		{name: "sessions", key: '6', mode: ui.ModeSessions},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addLaunchRan := false
			launchAgentRan := false
			startEmbeddedRan := false
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand: "codex-app",
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
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			if m.Mode() != tc.mode {
				t.Fatalf("starting mode = %d, want %d", m.Mode(), tc.mode)
			}

			m, cmd := update(m, flowLaunchKey())
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
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd != nil {
		t.Fatalf("flows-mode h returned command %T, want nil", cmd)
	}
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("flows-mode h changed mode to %d, want flows", m.Mode())
	}
	if view := m.View(); !strings.Contains(view, "headless off") {
		t.Fatalf("flows-mode h should show headless off:\n%s", view)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd != nil {
		t.Fatalf("second flows-mode h returned command %T, want nil", cmd)
	}
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("second flows-mode h changed mode to %d, want flows", m.Mode())
	}
	if view := m.View(); !strings.Contains(view, "headless on") {
		t.Fatalf("second flows-mode h should show headless on:\n%s", view)
	}
}

func TestModel_LegacyFlowShortcutsDoNotToggleOrLaunch(t *testing.T) {
	for _, key := range []rune{'x', 'a', 'i'} {
		t.Run(string(key), func(t *testing.T) {
			addLaunchRan := false
			launchAgentRan := false
			startEmbeddedRan := false
			m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

func TestModel_SelectedFlowPhaseClearsWhenLeavingRightPane(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyTab},
	} {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
		if got := m.SelectedFlowPhaseID(); got == "" {
			t.Fatal("expected selected flow phase before leaving right pane")
		}

		m, _ = update(m, key)
		if got := m.SelectedFlowPhaseID(); got != "" {
			t.Fatalf("%s left selected flow phase = %q, want cleared", key.String(), got)
		}
	}
}

func TestModel_SelectedFlowPhasePreservedWhenRightArrowClampsAtFlows(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	before := m.SelectedFlowPhaseID()
	if before == "" {
		t.Fatal("expected selected flow phase before right arrow")
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRight})
	if cmd != nil {
		t.Fatalf("right from flows returned command %T, want nil", cmd)
	}
	if got := m.SelectedFlowPhaseID(); got != before {
		t.Fatalf("right from flows selected flow phase = %q, want preserved %q", got, before)
	}
	if m.ActivePane() != 1 {
		t.Fatalf("right from flows active pane = %d, want right pane", m.ActivePane())
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
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 12})
	for i := 0; i < 5; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.FlowScroll(); got != 3 {
		t.Fatalf("flow scroll = %d, want 3", got)
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
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 10})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	for i := 0; i < 4; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	if got := m.FlowSelected(); got != 0 {
		t.Fatalf("single expanded flow should remain selected, got %d", got)
	}
	if got := m.FlowScroll(); got != 1 {
		t.Fatalf("flow scroll = %d, want 1", got)
	}
	view := m.View()
	if !strings.Contains(view, "review-loop:pending") {
		t.Fatalf("expanded flow should scroll within phase detail rows:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if got := m.FlowScroll(); got != 3 {
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
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{{FlowID: filepath.Base(filter.RepoPath), RepoPath: filter.RepoPath, Title: "T", Status: flowstore.StatusPending}}, nil
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if cmd == nil {
		t.Fatal("expected initial flows fetch")
	}
	m, _ = update(m, flowFetchMsgFromCommand(t, cmd))
	if got := m.Flows(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Flows() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("expected nil cmd switching to repo pane, got %T", cmd)
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
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{
		{FlowID: "stale", RepoPath: "/dev/alpha", Title: "T", Status: flowstore.StatusPending},
	}, ListRequest: 999999})
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("stale flow result should be ignored, got %#v", got)
	}
}

func TestModel_FlowListErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("flows unavailable")
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = flowsInRightPane(t, m, nil)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{
		RepoPath: "/dev/alpha",
		Stashes: []gitquery.Stash{{
			Index:   0,
			Date:    "2026-06-14 12:00:00 -0400",
			Message: "hidden stash",
		}},
		ListRequest: m.ListRequest(ui.ModeStashes),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF3})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
		m := model.NewWithOptions(testRepos(), model.Options{
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
		m, _ = update(m, cmd())
		if got := m.Flows(); len(got) != 0 {
			t.Fatalf("flows after not-found refresh = %#v, want empty", got)
		}
	})

	t.Run("other errors do not refresh as completed work", func(t *testing.T) {
		m := model.NewWithOptions(testRepos(), model.Options{
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

		if cmd != nil {
			t.Fatalf("non-not-found FlowDeleteFailedMsg returned command %T, want nil", cmd)
		}
		if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
			t.Fatalf("flows after failed delete = %#v, want unchanged", got)
		}
		if !strings.Contains(m.TransientError(), "disk full") {
			t.Fatalf("status after failed delete = %q, want error text", m.TransientError())
		}
	})

	t.Run("stale repo result is ignored", func(t *testing.T) {
		m := flowsInRightPane(t, model.NewWithOptions(testRepos(), model.Options{}), []flowstore.FlowRecord{{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

func TestModel_RightNavigationReachesFlowsWithoutChangingExistingModeNumbers(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	for _, key := range []rune{'1', '2', '3', '4', '5', '6', '7'} {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if got := int(m.Mode()); got != int(key-'0') {
			t.Fatalf("key %c set mode %d, want %c", key, got, key)
		}
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if m.Mode() != ui.ModeFlows || cmd == nil {
		t.Fatalf("key 8 mode=%d cmd=%v, want flows fetch", m.Mode(), cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("right from flows mode = %d, want still flows", m.Mode())
	}
	if m.ActivePane() != 1 {
		t.Fatalf("right from flows active pane = %d, want right pane", m.ActivePane())
	}
	if cmd != nil {
		t.Fatalf("right from flows mode should not fetch, got %T", cmd)
	}
}

func TestModel_FlowSearchIncludesPhasesAndMetadata(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		Title:        "Add Flow mode",
		Status:       flowstore.StatusInProgress,
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-mode",
		Branch:       "flow/add-flow-mode",
		PR:           flowstore.PullRequest{URL: "https://github.com/brian-bell/wtui/pull/123"},
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
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
		PlanPath:  "/state/wtui/sessions/v1/plans/plan-1/plan.md",
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
	if cmd != nil {
		t.Fatalf("unlinked flow o returned command %T, want nil", cmd)
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:         "codex",
		CodexReasoningEffort: "high",
		SessionStateRoot:     "/state/wtui",
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
	_ = cmd()
	if started.Command != "codex" ||
		started.ResumeSessionID != "codex-new" ||
		started.FlowID != "flow-1" ||
		started.FlowPhaseID != "implementation" ||
		started.RepoPath != "/dev/alpha" ||
		started.WorktreePath != "/dev/alpha-worktrees/flow-resume-sessions" ||
		started.WorkingDir != "/dev/alpha-worktrees/flow-resume-sessions" ||
		started.Branch != "flow/resume-sessions" ||
		started.Commit != "abc123" ||
		started.SessionStateRoot != "/state/wtui" ||
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

func TestModel_RKeyOnSelectedFlowPhaseUsesCodexAppPreferenceForCodexSession(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume codex app",
		Status:       flowstore.StatusInProgress,
		WorktreePath: "/dev/alpha-worktrees/flow-resume-codex-app",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Status:  flowstore.PhaseCompleted,
			Sessions: []flowstore.Session{
				{Provider: " CoDeX ", SessionID: "codex-new", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected selected Flow phase resume command")
	}
	msg, ok := cmd().(model.AgentResultMsg)
	if !ok || msg.Err != "" {
		t.Fatalf("expected successful AgentResultMsg from resume command, got %#v", msg)
	}
	if launched.Command != "codex-app" || launched.ResumeSessionID != "codex-new" {
		t.Fatalf("unexpected Flow phase codex-app resume context: %#v", launched)
	}
}

func TestModel_FlowPhaseWithUnsupportedProviderDoesNotAdvertiseResume(t *testing.T) {
	launchRan := false
	m := model.NewWithOptions(testRepos(), model.Options{
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
	if cmd != nil {
		t.Fatalf("unsupported provider should not launch, got %T", cmd)
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	if cmd != nil {
		t.Fatalf("awaiting latest session should not launch, got %T", cmd)
	}
	if launchRan {
		t.Fatal("LaunchAgent ran for stale Flow phase session")
	}
	if got := m.TransientError(); !strings.Contains(got, "awaiting session") {
		t.Fatalf("status = %q, want awaiting session", got)
	}
}

func TestModel_RKeyOnFlowPhaseNeedsAttentionCanResumeOlderValidSession(t *testing.T) {
	var started actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
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
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected needs_attention phase to resume older valid session")
	}
	_ = cmd()
	if started.ResumeSessionID != "codex-old" || !started.Embedded || started.Headless || !started.FlowLaunchTracked {
		t.Fatalf("embedded resume context = %#v, want older valid interactive session", started)
	}
}

func TestModel_RKeyOnFlowPhaseResumeSetupFailureKeepsCompletedPhase(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var phaseUpdates []flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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
	_ = cmd()
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if len(phaseUpdates) != 0 {
		t.Fatalf("phase updates = %#v, want none; the store preserved the completed phase, so a failed launch must not regress it", phaseUpdates)
	}
}

func TestModel_RKeyOnFlowPhaseResumeStartFailureKeepsSkippedPhase(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var phaseUpdates []flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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
	_ = cmd()

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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
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
		PlanPath:     "/state/wtui/sessions/v1/plans/plan-1/plan.md",
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
		launched.PlanPath != "/state/wtui/sessions/v1/plans/plan-1/plan.md" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-review" ||
		launched.Branch != "flow/review" ||
		launched.Commit != "abc123" ||
		launched.SessionStateRoot != "/state/wtui/sessions/v1" ||
		!launched.Embedded ||
		launched.Headless ||
		!launched.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Use the review-loop skill to review the saved plan, max 6 loops.",
		"Use the wtui-flow skill to record the Plan Review verdict before finishing; the phase is not done until the verdict is persisted.",
		"",
		"Plan: /state/wtui/sessions/v1/plans/plan-1/plan.md",
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
		"wtui flow phase set",
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
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
		PlanPath:     "/state/wtui/sessions/v1/plans/plan-1/plan.md",
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
		launched.PlanPath != "/state/wtui/sessions/v1/plans/plan-1/plan.md" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-implementation" ||
		launched.Branch != "flow/implementation" ||
		launched.Commit != "fed321" ||
		launched.SessionStateRoot != "/state/wtui/sessions/v1" ||
		!launched.Embedded ||
		launched.Headless ||
		!launched.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Implement the approved plan.",
		"Use the commit skill before completing this phase.",
		"",
		"Plan: /state/wtui/sessions/v1/plans/plan-1/plan.md",
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
		"wtui flow phase set",
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:         "codex",
		CodexReasoningEffort: "high",
		SessionStateRoot:     "/state/wtui/sessions/v1",
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
		PlanID:       "plan-1",
		PlanPath:     "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved"},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := update(m, flowLaunchKey())
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
		started.PlanPath != "/state/wtui/sessions/v1/plans/plan-1/plan.md" ||
		started.WorktreePath != "/dev/alpha-worktrees/flow-implementation" ||
		started.Branch != "flow/implementation" ||
		started.Commit != "fed321" ||
		started.SessionStateRoot != "/state/wtui/sessions/v1" ||
		started.ReasoningEffort != "high" ||
		!started.Embedded ||
		!started.Headless {
		t.Fatalf("embedded launch context = %#v", started)
	}
	if started.LaunchID == "" || started.LaunchID != launchUpdate.LaunchID {
		t.Fatalf("embedded launch ID = %q, launch update = %#v", started.LaunchID, launchUpdate)
	}
	_, terminalOuterHeight := ui.FlowSplitPanelHeights(18 - ui.BranchContentOverhead)
	wantStartWidth := ui.EmbeddedTerminalPTYWidth(ui.RightContentWidth(140, 18, false))
	wantStartHeight := ui.EmbeddedTerminalPTYHeight(terminalOuterHeight)
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
	m, _ = update(m, tea.WindowSizeMsg{Width: 160, Height: 20})
	_, terminalResizeOuterHeight := ui.FlowSplitPanelHeights(20 - ui.BranchContentOverhead)
	wantResizeWidth := ui.EmbeddedTerminalPTYWidth(ui.RightContentWidth(160, 20, false))
	wantResizeHeight := ui.EmbeddedTerminalPTYHeight(terminalResizeOuterHeight)
	wantResizeSize := [2]int{wantResizeWidth, wantResizeHeight}
	if len(fakeTerm.resizes) == 0 || fakeTerm.resizes[len(fakeTerm.resizes)-1] != wantResizeSize {
		t.Fatalf("embedded terminal resize calls = %#v, want latest %dx%d", fakeTerm.resizes, wantResizeWidth, wantResizeHeight)
	}
	wantPrompt := appendFlowDoneInstructionForTest(strings.Join([]string{
		"Implement the approved plan.",
		"Use the commit skill before completing this phase.",
		"",
		"Plan: /state/wtui/sessions/v1/plans/plan-1/plan.md",
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	var firstTick tea.Cmd
	for i := 0; i < len(terms); i++ {
		var cmd tea.Cmd
		m, cmd = prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	var tickBatch tea.Cmd
	for range terms {
		var cmd tea.Cmd
		m, cmd = prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	var tickBatch tea.Cmd
	for range terms {
		var cmd tea.Cmd
		m, cmd = prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
			m := model.NewWithOptions(testRepos(), model.Options{
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
			m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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

func TestModel_FlowEmbeddedTerminalResizesWhenSearchTogglesShortcutPane(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	wantHeight := flowTerminalPTYHeightForViewport(18)
	wantSearchSize := [2]int{
		ui.EmbeddedTerminalPTYWidth(ui.RightContentWidth(140, 18, true)),
		wantHeight,
	}
	wantInactiveSize := [2]int{
		ui.EmbeddedTerminalPTYWidth(ui.RightContentWidth(140, 18, false)),
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
	_, terminalOuterHeight := ui.FlowSplitPanelHeights(height - ui.BranchContentOverhead)
	return ui.EmbeddedTerminalPTYHeight(terminalOuterHeight)
}

func TestModel_FlowEmbeddedTerminalTinyAllocationClampsPTYSize(t *testing.T) {
	var started [2]int
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	_, terminalOuterHeight := ui.FlowSplitPanelHeights(height - ui.BranchContentOverhead)
	want := [2]int{
		ui.EmbeddedTerminalPTYWidth(ui.RightContentWidth(width, height, false)),
		ui.EmbeddedTerminalPTYHeight(terminalOuterHeight),
	}
	if started != want {
		t.Fatalf("embedded terminal start size = %dx%d, want %dx%d", started[0], started[1], want[0], want[1])
	}
}

func TestModel_GOnFlowPhaseWithCodexAppUsesExternalLaunchRoute(t *testing.T) {
	var launched actions.AgentLaunchContext
	startEmbeddedRan := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex-app",
		SessionStateRoot: "/state/wtui/sessions/v1",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
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
		WorktreePath: "/dev/alpha-worktrees/flow-codex-app",
		Title:        "Codex app flow",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady},
		},
	}})

	m = selectFlowPhaseByID(t, m, "plan")
	m, cmd := update(m, flowLaunchKey())
	if cmd == nil {
		t.Fatal("g should prepare an external codex-app launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("codex-app i command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected external codex-app agent result command")
	}
	_ = cmd()
	if startEmbeddedRan {
		t.Fatal("codex-app g should not start an embedded terminal")
	}
	if launched.Command != "codex-app" || launched.FlowID != "flow-1" || launched.Headless || launched.Embedded {
		t.Fatalf("codex-app launch context = %#v", launched)
	}
	if launched.ReasoningEffort != "" {
		t.Fatalf("codex-app launch reasoning effort = %q, want empty", launched.ReasoningEffort)
	}
}

func TestModel_GFlowPhaseEmbeddedTerminalStartFailureMarksPhaseNeedsAttention(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

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
			m := model.NewWithOptions(testRepos(), model.Options{
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
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			if cmd != nil {
				t.Fatalf("h before Flow phase launch returned command %T, want nil", cmd)
			}
			m, cmd = update(m, flowLaunchKey())
			if cmd == nil {
				t.Fatal("g should prepare an embedded launch")
			}
			m, cmd = update(m, cmd())
			if cmd == nil {
				t.Fatal("failed embedded prefill should still refresh Flow state")
			}

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

func TestModel_FlowTerminalFocusUsesPersistentCommandModeAndTabReturnsToList(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 1 {
		t.Fatalf("list focus should not forward j to terminal: %#v", fakeTerm.writes)
	}
	if m.FlowSelected() != 1 {
		t.Fatalf("flow selection = %d, want list focus to move to second flow", m.FlowSelected())
	}
}

func TestModel_F2SwitchesPaneWithoutFocusingInactiveFlowTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	if m.ActivePane() != 0 {
		t.Fatalf("f2 with inactive Flow terminal activePane = %d, want left pane", m.ActivePane())
	}
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("inactive Flow terminal should not receive f2 writes: %#v", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 0 {
		t.Fatalf("returning from f2 should keep Flow list focus, not terminal focus: %#v", fakeTerm.writes)
	}
	if got := m.SelectedFlowPhaseID(); got != "plan" {
		t.Fatalf("selected Flow phase = %q, want list focus to move to first phase", got)
	}
}

func TestModel_F2ForwardsWhenFlowTerminalInputOwnsKeys(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd == nil {
		t.Fatal("g should prepare an embedded launch")
	}
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})

	if m.ActivePane() != 1 {
		t.Fatalf("terminal-owned f2 activePane = %d, want right pane", m.ActivePane())
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x1bOQ" {
		t.Fatalf("terminal input f2 writes = %#v, want F2 escape sequence", fakeTerm.writes)
	}
}

func TestModel_FlowEffortKeyDoesNotOpenPickerWhileFlowTerminalFocused(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
		WorktreePath: "/dev/alpha-worktrees/flow-one",
		Title:        "Flow one",
		Status:       flowstore.StatusInProgress,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady},
		},
	}})
	m = selectFlowPhaseByID(t, m, "implementation")
	m, cmd := update(m, flowLaunchKey())
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("terminal input-mode E opened overlay %d", m.Overlay())
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "E" {
		t.Fatalf("terminal input-mode E writes = %#v, want E", fakeTerm.writes)
	}
}

func TestModel_FlowTerminalCommandModeCanEnterInputMode(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 2 {
		t.Fatalf("command mode should let tab leave focus without writing: %#v", fakeTerm.writes)
	}
	if m.FlowSelected() != 1 {
		t.Fatalf("flow selection = %d, want list focus to move to second flow", m.FlowSelected())
	}
}

func TestModel_FlowTerminalInputModeForwardsCtrlGToAgent(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	if got := m.TransientError(); strings.Contains(got, "Unknown terminal prefix command") {
		t.Fatalf("tab should not be treated as an unknown terminal command, status = %q", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if len(fakeTerm.writes) != 1 {
		t.Fatalf("list focus should not forward j to terminal: %#v", fakeTerm.writes)
	}
	if m.FlowSelected() != 1 {
		t.Fatalf("flow selection = %d, want list focus to move to second flow", m.FlowSelected())
	}
}

func TestModel_FlowTerminalFocusCyclesLeftRightWithoutWritingPTY(t *testing.T) {
	terms := []*fakeEmbeddedTerminal{
		{lines: []string{"flow first output"}, state: "running"},
		{lines: []string{"flow second output"}, state: "running"},
		{lines: []string{"flow third output"}, state: "running"},
	}
	starts := 0
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})

	launch := func() {
		t.Helper()
		var cmd tea.Cmd
		m, cmd = prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
		if cmd == nil {
			t.Fatal("g should prepare an embedded launch")
		}
		m, _ = update(m, cmd())
	}
	launch()
	launch()
	launch()
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
		t.Fatalf("Flow terminal should start with scope-local tab 1:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/session", Branch: "feature/session"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if view := m.View(); !strings.Contains(view, "1 codex feature/session exited") {
		t.Fatalf("session terminal should also use scope-local tab 1:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if strings.Contains(m.View(), "session output") {
		t.Fatalf("dismissed session terminal should be removed:\n%s", m.View())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{flowWithPhaseDetails()}, ListRequest: m.ListRequest(ui.ModeFlows)})
	view := m.View()
	for _, want := range []string{"1 codex implementation running", "flow output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Flow terminal with matching display number should survive session close, missing %q:\n%s", want, view)
		}
	}
}

func TestModel_EmbeddedTerminalTerminateUsesStableIdentityAcrossScopes(t *testing.T) {
	sessionTerm := &fakeEmbeddedTerminal{lines: []string{"session output"}, state: "running"}
	flowTerm := &fakeEmbeddedTerminal{lines: []string{"flow output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	m = selectFlowPhaseByID(t, m, "implementation")

	for i := 0; i < 9; i++ {
		var cmd tea.Cmd
		m, cmd = update(m, flowLaunchKey())
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

	m, cmd := update(m, flowLaunchKey())
	if cmd == nil {
		t.Fatal("g at cap should still prepare a launch attempt")
	}
	m, _ = update(m, cmd())
	if starts != 9 {
		t.Fatalf("embedded terminal starts after cap = %d, want 9", starts)
	}
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one needs_attention update", phaseUpdates)
	}
	if got := phaseUpdates[0]; got.FlowID != "flow-1" ||
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flowWithPhaseDetails()})
	for i := 0; i < 4; i++ {
		var cmd tea.Cmd
		m, cmd = prepareSelectedFlowPhaseEmbeddedLaunch(t, m, "implementation")
		if cmd == nil {
			t.Fatalf("flow launch %d should prepare an embedded launch", i+1)
		}
		m, _ = update(m, cmd())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
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
	if strings.Contains(view, "flow/alpha") {
		t.Fatalf("first flow should scroll offscreen in the split list:\n%s", view)
	}
}

func TestModel_GLaunchesFlowPhaseImplementationWithoutLinkedPlanContext(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
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
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
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

	m, cmd := update(m, flowLaunchKey())
	if cmd != nil {
		t.Fatalf("not-ready flow launch returned command %T, want nil", cmd)
	}
	if status := m.TransientError(); status != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", status)
	}
}

func TestModel_GLaunchesFlowPhaseReviewLoopWithFirstLevelPrompt(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
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
		PlanPath:     "/state/wtui/sessions/v1/plans/plan-1/plan.md",
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
		"Use the wtui-flow skill to record the Review Loop result before finishing; the phase is not done until the result is persisted.",
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
		"wtui flow phase set",
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
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
		PlanPath:     "/state/wtui/sessions/v1/plans/plan-1/plan.md",
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
		"After the PR exists, run `wtui flow pr set --flow-id flow-1 --provider github --number <number> --url <url> --head flow/pr --base <base>` before completing this phase.",
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
		"wtui flow phase set",
		"advance this phase",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("pr-creation prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GLaunchesFlowPhasePRCreationWithStructuredMetadataPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
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
		"After the PR exists, run `wtui flow pr set --flow-id flow-1 --provider github --number <number> --url <url> --head flow/pr --base <base>` before completing this phase.",
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
		"wtui flow phase set",
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
	m := model.NewWithOptions(testRepos(), model.Options{
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
			URL:        "https://github.com/brian-bell/wtui/pull/115",
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
		"use the wtui-flow skill to record the autoreview result before finishing",
		"worktree: /dev/alpha-worktrees/flow-pr",
		"branch: flow/pr",
		"start commit: ghi789",
		"pr target:",
		"github #115",
		"https://github.com/brian-bell/wtui/pull/115",
		"head: flow/pr",
		"base: main",
		"status: open",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("autoreview prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
	for _, unwanted := range []string{"saved plan body", "wtui flow phase", "--status completed", "--status needs_attention", "--status blocked"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("autoreview prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GLaunchesFlowPhaseAutoreviewWithRecoveryPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
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
			URL:        "https://github.com/brian-bell/wtui/pull/115",
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
		"use the wtui-flow skill to record the autoreview result before finishing",
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
	for _, unwanted := range []string{"restart required", "--status running", "rerunning autoreview", "wtui flow phase"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("autoreview recovery prompt should not include %q:\n%s", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_GDoesNotRelaunchFlowPhaseAutoreviewWithoutPRTarget(t *testing.T) {
	var launchAttempted bool
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, cmd := update(m, flowLaunchKey())
	if cmd != nil {
		t.Fatal("g should not launch blocked autoreview without PR metadata")
	}
	if launchAttempted {
		t.Fatal("LaunchAgent() ran without PR metadata")
	}
	if got := m.TransientError(); got != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", got)
	}
}

func TestModel_GDoesNotRelaunchFlowPhaseAutoreviewWhenPredecessorsAreUnsatisfied(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
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
			URL:        "https://github.com/brian-bell/wtui/pull/115",
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
	m, cmd := update(m, flowLaunchKey())
	if cmd != nil {
		t.Fatal("g should not launch blocked autoreview while predecessors are unsatisfied")
	}
	if got := m.TransientError(); got != "No launchable Flow phase" {
		t.Fatalf("status = %q, want no-launchable message", got)
	}
}

func TestModel_GLaunchesFlowPhaseMergeWithStructuredReportingPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
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
			URL:        "https://github.com/brian-bell/wtui/pull/116",
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
		"https://github.com/brian-bell/wtui/pull/116",
		"wtui flow merge set --flow-id flow-1 --status merged",
		"--commit <merge-commit>",
		"--merged-at <rfc3339>",
		"wtui flow phase set --flow-id flow-1 --phase-id merge --status completed",
		"blocked",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("merge prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
}

func TestModel_GUsesFlowPhaseOrderingForReadyLaunch(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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

func TestModel_FlowAgentResultFailureMarksPlanReviewBlocked(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
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
	_ = cmd()

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan-review" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		phaseUpdate.Outcome != flowstore.OutcomeBlocked ||
		!strings.Contains(phaseUpdate.Notes, "terminal failed") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_NewFlowOpensSingleCreationForm(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
	if len(form.Fields) != 4 {
		t.Fatalf("fields = %#v, want title, instructions, base ref, headless", form.Fields)
	}
	want := []struct {
		id          string
		kind        ui.FormFieldKind
		label       string
		placeholder string
	}{
		{id: "title", kind: ui.FormText, label: "Title", placeholder: ui.FlowTitleInputPlaceholder},
		{id: "instructions", kind: ui.FormMultilineText, label: "Instructions", placeholder: ui.FlowInstructionsInputPlaceholder},
		{id: "base-ref", kind: ui.FormText, label: "Base ref", placeholder: ui.FlowBaseRefInputPlaceholder},
		{id: "headless", kind: ui.FormCheckbox, label: "Headless"},
	}
	for i, wantField := range want {
		field := form.Fields[i]
		if field.ID != wantField.id || field.Kind != wantField.kind || field.Label != wantField.label || field.Placeholder != wantField.placeholder {
			t.Fatalf("field %d = %#v, want %#v", i, field, wantField)
		}
		if field.Kind == ui.FormCheckbox {
			if field.Checked {
				t.Fatalf("field %d initial checked = true, want false", i)
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
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand:          command,
				CodexReasoningEffort:  "xhigh",
				ClaudeReasoningEffort: "max",
				SessionStateRoot:      "/state/wtui/sessions/v1",
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
						PlanPhaseID:      req.PlanPhaseID,
						PlanPhaseTitle:   req.PlanPhaseTitle,
						PlanPhaseStatus:  req.PlanPhaseStatus,
						FlowID:           "flow-1",
						FlowPhaseID:      req.PlanPhaseID,
						ReasoningEffort:  req.ReasoningEffort,
						InitialPrompt:    "Use the wtui-flow skill for this launch.\n\nBuild\nthe thing\n\nCreate and persist the plan with wtui plan save, link it back with wtui flow plan set.",
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
			m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
				startRequest.SessionStateRoot != "/state/wtui/sessions/v1" ||
				startRequest.PlanPhaseID != "plan" ||
				startRequest.PlanPhaseTitle != "Plan" ||
				startRequest.PlanPhaseStatus != flowstore.PhaseRunning ||
				startRequest.ReasoningEffort != wantEffort {
				t.Fatalf("start request metadata = %#v", startRequest)
			}
			if started.Command != command ||
				started.RepoPath != "/dev/alpha" ||
				started.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
				started.Branch != "flow/add-flow-mode" ||
				started.Commit != "abc123" ||
				started.SessionStateRoot != "/state/wtui/sessions/v1" ||
				started.FlowID != "flow-1" ||
				started.FlowPhaseID != "plan" ||
				started.PlanPhaseID != "plan" ||
				started.PlanPhaseTitle != "Plan" ||
				started.PlanPhaseStatus != flowstore.PhaseRunning ||
				started.LaunchID != "launch-1" ||
				started.ReasoningEffort != wantEffort ||
				!started.Embedded ||
				started.Headless ||
				!started.FlowLaunchTracked {
				t.Fatalf("embedded launch context = %#v", started)
			}
			if startWidth <= 0 || startHeight <= 0 {
				t.Fatalf("embedded terminal size = %dx%d, want positive", startWidth, startHeight)
			}
			prompt := strings.ToLower(started.InitialPrompt)
			for _, want := range []string{"wtui-flow", "build\nthe thing", "create and persist the plan", "wtui plan save", "wtui flow plan set"} {
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

func TestModel_NewFlowLaunchNormalizesConfiguredAgentCommandForPlanAndTerminal(t *testing.T) {
	var startRequest model.FlowStartRequest
	var started actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "codex",
		ClaudeReasoningEffort: "max",
		SessionStateRoot:      "/state/wtui/sessions/v1",
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
				FlowPhaseID:      req.PlanPhaseID,
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand:     command,
				SessionStateRoot: "/state/wtui/sessions/v1",
				StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
						Command:          req.AgentCommand,
						LaunchID:         "launch-1",
						RepoPath:         req.RepoPath,
						WorktreePath:     "/dev/alpha-worktrees/flow-interactive-plan",
						Branch:           "flow/interactive-plan",
						SessionStateRoot: req.SessionStateRoot,
						FlowID:           "flow-1",
						FlowPhaseID:      req.PlanPhaseID,
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
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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
			if model.FlowHeadlessForTest(m) {
				t.Fatal("checked create-form headless option should not mutate flows-mode headless toggle")
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:          req.AgentCommand,
				LaunchID:         "launch-1",
				RepoPath:         req.RepoPath,
				WorktreePath:     "/dev/alpha-worktrees/flow-interactive-plan",
				Branch:           "flow/interactive-plan",
				SessionStateRoot: req.SessionStateRoot,
				FlowID:           "flow-1",
				FlowPhaseID:      req.PlanPhaseID,
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
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Interactive Plan", "Write the plan", "main")
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
	if started.FlowPhaseID != "plan" || started.Headless || !started.Embedded || !started.FlowLaunchTracked {
		t.Fatalf("interactive new Flow plan launch context = %#v", started)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	wantPrefill := "\x1b[200~Create and persist the plan.\x1b[201~"
	if len(fakeTerm.writes) != 2 || fakeTerm.writes[0] != wantPrefill || fakeTerm.writes[1] != "z" {
		t.Fatalf("interactive new Flow plan launch should focus terminal input: %#v", fakeTerm.writes)
	}
}

func TestModel_NewFlowWithCodexAppUsesExternalLaunchRoute(t *testing.T) {
	for _, headless := range []bool{false, true} {
		t.Run(fmt.Sprintf("headless_%v", headless), func(t *testing.T) {
			var launched actions.AgentLaunchContext
			startEmbeddedRan := false
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand:     "codex-app",
				SessionStateRoot: "/state/wtui/sessions/v1",
				StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					if req.RepoPath != "/dev/alpha" ||
						req.AgentCommand != "codex-app" ||
						req.Title != "Add Flow Mode" ||
						req.Instructions != "Build\nthe thing" ||
						req.BaseRef != "main" {
						t.Fatalf("StartFlowPlan request = %#v", req)
					}
					return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
						Command:          req.AgentCommand,
						LaunchID:         "launch-1",
						RepoPath:         req.RepoPath,
						WorktreePath:     "/dev/alpha-worktrees/flow-add-flow-mode",
						Branch:           "flow/add-flow-mode",
						Commit:           "abc123",
						SessionStateRoot: req.SessionStateRoot,
						PlanPhaseID:      req.PlanPhaseID,
						PlanPhaseTitle:   req.PlanPhaseTitle,
						PlanPhaseStatus:  req.PlanPhaseStatus,
						FlowID:           "flow-1",
						FlowPhaseID:      req.PlanPhaseID,
						InitialPrompt:    "Use the wtui-flow skill for this launch.\n\nBuild\nthe thing\n\nCreate and persist the plan with wtui plan save, link it back with wtui flow plan set.",
					}}, nil
				},
				LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					launched = ctx
					return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					startEmbeddedRan = true
					return &fakeEmbeddedTerminal{}, nil
				},
			})
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

			m, cmd := submitNewFlowPromptsWithOptions(t, m, "Add Flow Mode", "Build\nthe thing", "main", headless)
			if cmd == nil {
				t.Fatal("expected flow creation command")
			}
			msg := cmd()
			launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
			if !ok {
				t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
			}
			if launchMsg.Request == 0 {
				t.Fatal("external launch message should be tagged with the active create request")
			}
			_, cmd = update(m, launchMsg)
			if cmd == nil {
				t.Fatal("expected external codex-app agent result command")
			}

			if startEmbeddedRan {
				t.Fatal("codex-app new Flow launch should not start an embedded terminal")
			}
			if launched.Command != "codex-app" ||
				launched.LaunchID != "launch-1" ||
				launched.RepoPath != "/dev/alpha" ||
				launched.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
				launched.Branch != "flow/add-flow-mode" ||
				launched.Commit != "abc123" ||
				launched.SessionStateRoot != "/state/wtui/sessions/v1" ||
				launched.FlowID != "flow-1" ||
				launched.FlowPhaseID != "plan" ||
				launched.PlanPhaseID != "plan" ||
				launched.PlanPhaseTitle != "Plan" ||
				launched.PlanPhaseStatus != flowstore.PhaseRunning ||
				launched.ReasoningEffort != "" ||
				launched.InitialPrompt == "" ||
				launched.Embedded ||
				launched.Headless ||
				launched.FlowLaunchTracked {
				t.Fatalf("codex-app launch context = %#v", launched)
			}
			prompt := strings.ToLower(launched.InitialPrompt)
			for _, want := range []string{"wtui-flow", "build\nthe thing", "create and persist the plan", "wtui plan save", "wtui flow plan set"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("launch prompt missing %q: %q", want, launched.InitialPrompt)
				}
			}
		})
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
			m := model.NewWithOptions(testRepos(), model.Options{
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
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
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

func TestModel_NewFlowCodexAppStaleLaunchIgnoredAfterRepoChange(t *testing.T) {
	externalLaunches := 0
	startEmbeddedRan := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
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
				FlowPhaseID:  req.PlanPhaseID,
			}}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			externalLaunches++
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			startEmbeddedRan = true
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, createCmd := submitNewFlowPrompts(t, m, "Stale Flow", "Do the stale thing", "main")
	if createCmd == nil {
		t.Fatal("expected flow creation command")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	staleMsg := createCmd()
	if launchMsg, ok := staleMsg.(model.PlanLaunchRequestedMsg); !ok || launchMsg.Request == 0 {
		t.Fatalf("creation command returned %#v, want tagged PlanLaunchRequestedMsg", staleMsg)
	}
	_, cmd := update(m, staleMsg)
	if cmd != nil {
		t.Fatalf("stale codex-app launch returned command %T, want nil", cmd)
	}
	if externalLaunches != 0 {
		t.Fatalf("stale codex-app launch count = %d, want 0", externalLaunches)
	}
	if startEmbeddedRan {
		t.Fatal("stale codex-app launch should not start an embedded terminal")
	}
}

func TestModel_NewFlowLaunchesAfterStartPlanReturns(t *testing.T) {
	var calls []string
	m := model.NewWithOptions(testRepos(), model.Options{
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
				FlowPhaseID:  req.PlanPhaseID,
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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

func TestModel_NewFlowStaleLaunchIgnoredAfterRepoChange(t *testing.T) {
	embeddedStarts := 0
	externalLaunches := 0
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
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
				FlowPhaseID:  req.PlanPhaseID,
			}}, nil
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, createCmd := submitNewFlowPrompts(t, m, "Stale Flow", "Do the stale thing", "main")
	if createCmd == nil {
		t.Fatal("expected flow creation command")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	staleMsg := createCmd()
	if launchMsg, ok := staleMsg.(model.FlowEmbeddedLaunchRequestedMsg); !ok || launchMsg.Request == 0 {
		t.Fatalf("creation command returned %#v, want tagged FlowEmbeddedLaunchRequestedMsg", staleMsg)
	}
	_, cmd := update(m, staleMsg)
	if cmd != nil {
		t.Fatalf("stale launch returned command %T, want nil", cmd)
	}
	if embeddedStarts != 0 || externalLaunches != 0 {
		t.Fatalf("stale flow creation launch should be ignored after repo change; embedded=%d external=%d", embeddedStarts, externalLaunches)
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

func TestModel_NewFlowWarnsWhenNoAgentConfigured(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("expected no command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("overlay = %d, want none", m.Overlay())
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatalf("expected missing-agent warning in view:\n%s", m.View())
	}
}

func TestModel_NewFlowStartFailureReportsError(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{}, errors.New("branch exists")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{}, errors.New("branch exists; mark flow blocked: disk full")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-1",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
				FlowID:       "flow-1",
				FlowPhaseID:  req.PlanPhaseID,
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	m, _ = update(m, launchMsg)

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

func TestModel_NewFlowCodexAppLaunchFailureMarksPlanNeedsAttention(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:      req.AgentCommand,
				LaunchID:     "launch-1",
				RepoPath:     req.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
				FlowID:       "flow-1",
				FlowPhaseID:  req.PlanPhaseID,
			}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("no terminal")
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			t.Fatal("codex-app new Flow launch should use external launcher, not embedded terminal")
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, _ = update(m, launchMsg)

	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one launch failure update", phaseUpdates)
	}
	update := phaseUpdates[0]
	if update.FlowID != "flow-1" ||
		update.PhaseID != "plan" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "no terminal") {
		t.Fatalf("phase update = %#v", update)
	}
	if got := m.TransientError(); !strings.Contains(got, "no terminal") {
		t.Fatalf("status = %q, want external launch error", got)
	}
}

func TestModel_NewFlowAtEmbeddedTerminalCapMarksPlanNeedsAttention(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	starts := 0
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
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
				FlowPhaseID:  req.PlanPhaseID,
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

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
	m, _ = update(m, launchMsg)

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
	m := model.NewWithOptions(testRepos(), model.Options{
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan"},
		Err:           "agent exited",
		Detached:      true,
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan" ||
		phaseUpdate.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(phaseUpdate.Notes, "agent exited") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
	m, _ = update(m, cmd())
	if !listed {
		t.Fatal("expected ListFlows to run")
	}
	if got := m.Flows(); len(got) != 1 || got[0].Status != flowstore.StatusNeedsAttention {
		t.Fatalf("flows = %#v", got)
	}
}

func TestModel_FlowAgentResultFailureReportsPhaseUpdateFailure(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: filter.RepoPath, Title: "T"}}, nil
		},
		SetFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan"},
		Err:           "agent exited",
		Detached:      true,
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	if got := m.TransientError(); !strings.Contains(got, "agent exited") || !strings.Contains(got, "update flow phase: state root locked") {
		t.Fatalf("status = %q, want launch and phase-update failures", got)
	}
}

func submitNewFlowPrompts(t *testing.T, m model.Model, title, instructions, baseRef string) (model.Model, tea.Cmd) {
	t.Helper()
	return submitNewFlowPromptsWithOptions(t, m, title, instructions, baseRef, false)
}

func submitNewFlowPromptsWithOptions(t *testing.T, m model.Model, title, instructions, baseRef string, headless bool) (model.Model, tea.Cmd) {
	t.Helper()
	m = openNewFlowForm(t, m, title, instructions, baseRef)
	if headless {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
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
