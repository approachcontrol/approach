package model_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model"
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
	msg, ok := cmd().(model.FlowResultMsg)
	if !ok {
		t.Fatalf("expected FlowResultMsg, got %T", msg)
	}
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

	msg, ok := firstCmd().(model.FlowResultMsg)
	if !ok {
		t.Fatalf("expected FlowResultMsg, got %T", msg)
	}
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

	nextMsg, ok := cmd().(model.FlowResultMsg)
	if !ok {
		t.Fatalf("expected second FlowResultMsg, got %T", nextMsg)
	}
	if nextMsg.ListRequest != nextRequest {
		t.Fatalf("second FlowResultMsg.ListRequest = %d, want current request %d", nextMsg.ListRequest, nextRequest)
	}
	m, _ = update(m, nextMsg)
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-current" {
		t.Fatalf("Flows() = %#v, want current flow", got)
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
	msg, ok := cmd().(model.FlowResultMsg)
	if !ok {
		t.Fatalf("startup command returned %T, want FlowResultMsg", msg)
	}
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("Flows() = %#v, want %#v", got, want)
	}
}

func TestModel_FlowPhasesCollapsedByDefaultAndToggleWithX(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})

	if m.ExpandedFlowID() != "" {
		t.Fatalf("expanded flow = %q, want collapsed by default", m.ExpandedFlowID())
	}
	if strings.Contains(m.View(), "plan-review:approved") {
		t.Fatalf("collapsed flow should not render phase detail rows:\n%s", m.View())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("toggle phases returned command %T, want nil", cmd)
	}
	if got := m.ExpandedFlowID(); got != flow.FlowID {
		t.Fatalf("expanded flow = %q, want %q", got, flow.FlowID)
	}
	if !strings.Contains(m.View(), "plan-review:approved") {
		t.Fatalf("expanded flow should render phase detail rows:\n%s", m.View())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.ExpandedFlowID() != "" || strings.Contains(m.View(), "plan-review:approved") {
		t.Fatalf("second toggle should collapse phase detail rows:\n%s", m.View())
	}
}

func TestModel_FlowPhasesAutoCollapseWhenSelectionChanges(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{
		flowWithPhaseDetails(),
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusPending, Phases: []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhasePending}}},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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

func TestModel_ExpandedFlowArrowKeysSelectPhaseRows(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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
	if view := m.View(); !strings.Contains(view, "> completed") {
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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
		{Type: tea.KeyRight},
	} {
		m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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
	m, _ = update(m, cmd())
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
	m, _ = update(m, cmd())
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
	m, _ = update(m, cmd())
	if got := m.View(); !strings.Contains(got, "failed to load flows") || !strings.Contains(got, "Could not load flows") {
		t.Fatalf("expected flow load error in view:\n%s", got)
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
	if m.ActivePane() != 0 {
		t.Fatalf("right from flows active pane = %d, want left pane", m.ActivePane())
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
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: "/state/wtui",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected selected Flow phase resume command")
	}
	var msg model.AgentResultMsg
	switch got := cmd().(type) {
	case tea.BatchMsg:
		for _, batched := range got {
			if agentResult, ok := batched().(model.AgentResultMsg); ok {
				msg = agentResult
			}
		}
	case model.AgentResultMsg:
		msg = got
	default:
		t.Fatalf("expected AgentResultMsg or BatchMsg from resume command, got %T", got)
	}
	if msg.Err != "" || msg.LaunchContext.ResumeSessionID == "" {
		t.Fatalf("expected successful AgentResultMsg from resume command, got %#v", msg)
	}
	if launched.Command != "codex" ||
		launched.ResumeSessionID != "codex-new" ||
		launched.FlowID != "flow-1" ||
		launched.FlowPhaseID != "implementation" ||
		launched.RepoPath != "/dev/alpha" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-resume-sessions" ||
		launched.WorkingDir != "/dev/alpha-worktrees/flow-resume-sessions" ||
		launched.Branch != "flow/resume-sessions" ||
		launched.Commit != "abc123" ||
		launched.SessionStateRoot != "/state/wtui" {
		t.Fatalf("unexpected Flow phase resume context: %#v", launched)
	}
	if launched.LaunchID == "" || launched.LaunchID == "launch-new" {
		t.Fatalf("expected Flow phase resume to use a fresh launch id, got %#v", launched.LaunchID)
	}
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID != launched.LaunchID {
		t.Fatalf("launch update = %#v, want fresh resume launch id %#v", launchUpdate, launched.LaunchID)
	}
	if !launched.FlowLaunchTracked {
		t.Fatalf("expected Flow phase resume context to be marked tracked: %#v", launched)
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Detached: true}, nil
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if !strings.Contains(m.View(), "r      resume") {
		t.Fatalf("needs_attention phase should advertise Flow phase resume:\n%s", m.View())
	}
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected needs_attention phase to resume older valid session")
	}
	_ = cmd()
	if launched.ResumeSessionID != "codex-old" {
		t.Fatalf("resume session = %#v, want older valid session", launched.ResumeSessionID)
	}
}

func TestModel_RKeyOnFlowPhaseResumeSetupFailureMarksTrackedLaunchNeedsAttention(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var phaseUpdates []flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("terminal unavailable")
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		t.Fatalf("immediate resume failure should not return command, got %T", cmd)
	}
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one launch failure update", phaseUpdates)
	}
	if update := phaseUpdates[0]; update.FlowID != "flow-1" ||
		update.PhaseID != "review-loop" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "terminal unavailable") {
		t.Fatalf("phase update = %#v", update)
	}
	if got := m.TransientError(); !strings.Contains(got, "terminal unavailable") {
		t.Fatalf("status = %q, want launch failure", got)
	}
}

func TestModel_RKeyOnFlowPhaseResumeRunFailureMarksTrackedLaunchNeedsAttention(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var phaseUpdate flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{Cmd: exec.Command("false"), Detached: true}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		Title:        "Resume run failure",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/resume-run-failure",
		WorktreePath: "/dev/alpha-worktrees/flow-resume-run-failure",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "review-loop",
			Title:     "Review loop",
			Status:    flowstore.PhaseCompleted,
			LaunchIDs: []string{"launch-old"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-review", LaunchID: "launch-old", Status: "ended"},
			},
		}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected resume launch command")
	}
	var result model.AgentResultMsg
	switch got := cmd().(type) {
	case tea.BatchMsg:
		for _, batched := range got {
			if agentResult, ok := batched().(model.AgentResultMsg); ok {
				result = agentResult
			}
		}
	case model.AgentResultMsg:
		result = got
	default:
		t.Fatalf("expected AgentResultMsg or BatchMsg from resume command, got %T", got)
	}
	if result.Err == "" {
		t.Fatalf("expected launch command failure, got %#v", result)
	}
	m, _ = update(m, result)

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "review-loop" ||
		phaseUpdate.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(phaseUpdate.Notes, "exit status") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_AKeyOnFlowLaunchesReadyPlanReviewWithLinkedPlanContext(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare a plan-review launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

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
		launched.SessionStateRoot != "/state/wtui/sessions/v1" {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := strings.Join([]string{
		"Use the review loop skill to review the saved plan.",
		"",
		"Plan: /state/wtui/sessions/v1/plans/plan-1/plan.md",
		"Worktree: /dev/alpha-worktrees/flow-review",
		"Branch: flow/review",
		"Start commit: abc123",
	}, "\n")
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

func TestModel_AKeyOnFlowLaunchesImplementationWithMinimalPrompt(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare an implementation launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

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
		launched.SessionStateRoot != "/state/wtui/sessions/v1" {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := strings.Join([]string{
		"Implement the approved plan.",
		"",
		"Plan: /state/wtui/sessions/v1/plans/plan-1/plan.md",
		"Worktree: /dev/alpha-worktrees/flow-implementation",
		"Branch: flow/implementation",
		"Start commit: fed321",
	}, "\n")
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

func TestModel_AKeyOnFlowLaunchesImplementationWithoutLinkedPlanContext(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare an implementation launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

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

func TestModel_AKeyOnFlowExplainsWhyImplementationIsNotReady(t *testing.T) {
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
	if view := m.View(); !strings.Contains(view, "launch phase") {
		t.Fatalf("gated flow view should still expose launch/status action:\n%s", view)
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("not-ready flow launch returned command %T, want nil", cmd)
	}
	status := m.TransientError()
	for _, want := range []string{"Implementation is not ready", "Plan Review", "changes_requested", "Clarify rollout steps"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q: %q", want, status)
		}
	}
}

func TestModel_AKeyOnFlowLaunchesReviewLoopWithFirstLevelPrompt(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare a review-loop launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "review-loop" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.FlowID != "flow-1" || launched.FlowPhaseID != "review-loop" || launched.PlanID != "plan-1" {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := strings.Join([]string{
		"Use the review loop skill to review the changes.",
		"",
		"Worktree: /dev/alpha-worktrees/flow-review-loop",
		"Branch: flow/review-loop",
		"Start commit: def456",
	}, "\n")
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

func TestModel_AKeyOnFlowLaunchesPRCreationWithMinimalPrompt(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare a pr-creation launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "pr-creation" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.FlowID != "flow-1" || launched.FlowPhaseID != "pr-creation" || launched.PlanID != "plan-1" {
		t.Fatalf("launch context = %#v", launched)
	}
	wantPrompt := strings.Join([]string{
		"Create a PR for the changes.",
		"After the PR exists, run `wtui flow pr set --flow-id flow-1 --provider github --number <number> --url <url> --head flow/pr --base <base>` before completing this phase.",
		"",
		"Worktree: /dev/alpha-worktrees/flow-pr",
		"Branch: flow/pr",
		"Start commit: abc789",
	}, "\n")
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

func TestModel_AKeyOnFlowLaunchesPRCreationWithStructuredMetadataPrompt(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare a pr-creation launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

	wantPrompt := strings.Join([]string{
		"Create a PR for the changes.",
		"After the PR exists, run `wtui flow pr set --flow-id flow-1 --provider github --number <number> --url <url> --head flow/pr --base <base>` before completing this phase.",
		"",
		"Worktree: /dev/alpha-worktrees/flow-pr",
		"Branch: flow/pr",
		"Start commit: abc789",
	}, "\n")
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

func TestModel_AKeyOnFlowLaunchesAutoreviewWithPRContext(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			return "# Saved plan\n", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare an autoreview launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"second-level review",
		"github #115",
		"https://github.com/brian-bell/wtui/pull/115",
		"head: flow/pr",
		"base: main",
		"wtui flow phase set --flow-id flow-1 --phase-id autoreview",
		"--status completed",
		"--status needs_attention",
		"--status blocked",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("autoreview prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
}

func TestModel_AKeyOnFlowLaunchesAutoreviewWithRecoveryPrompt(t *testing.T) {
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		ReadPlan: func(planID string) (string, error) {
			return "# Saved plan\n", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-pr",
		Branch:       "flow/pr",
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare an autoreview relaunch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"restart required",
		"--status running",
		"rerunning autoreview after addressing prior findings",
		"--status completed",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("autoreview recovery prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
}

func TestModel_AKeyOnFlowDoesNotRelaunchAutoreviewWithoutPRTarget(t *testing.T) {
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatal("flows-mode a should not launch blocked autoreview without PR metadata")
	}
	if launchAttempted {
		t.Fatal("LaunchAgent() ran without PR metadata")
	}
	if got := m.TransientError(); got != "No ready Flow phase to launch" {
		t.Fatalf("status = %q, want no ready phase message", got)
	}
}

func TestModel_AKeyOnFlowDoesNotRelaunchAutoreviewWhenPredecessorsAreUnsatisfied(t *testing.T) {
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatal("flows-mode a should not launch blocked autoreview while predecessors are unsatisfied")
	}
	if got := m.TransientError(); got != "No ready Flow phase to launch" {
		t.Fatalf("status = %q, want no ready phase message", got)
	}
}

func TestModel_AKeyOnFlowLaunchesMergeWithStructuredReportingPrompt(t *testing.T) {
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
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare a merge launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

	if launched.FlowPhaseID != "merge" {
		t.Fatalf("launched flow phase = %q, want merge", launched.FlowPhaseID)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{
		"merge the reviewed pr deliberately",
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

func TestModel_AKeyOnFlowUsesChildPhaseOrderingForReadyLaunch(t *testing.T) {
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
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("flows-mode a should prepare a child phase launch")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}
	_ = cmd()

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

func TestModel_NewFlowPromptsForTitle(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("expected opening new-flow title prompt to return no command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("overlay = %d, want input overlay", m.Overlay())
	}
	if got := m.ConfirmPrompt(); got != ui.FlowTitlePrompt {
		t.Fatalf("prompt = %q, want %q", got, ui.FlowTitlePrompt)
	}
	if got := m.WorktreeInput(); got != "" {
		t.Fatalf("initial title input = %q, want empty", got)
	}
}

func TestModel_NewFlowDelegatesStartAndLaunchesPlanAgent(t *testing.T) {
	var startRequest model.FlowStartRequest
	var launched actions.AgentLaunchContext
	var calls []string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			calls = append(calls, "start-flow")
			startRequest = req
			if req.RepoPath != "/dev/alpha" || req.Title != "Add Flow Mode" || req.Instructions != "Build the thing" || req.BaseRef != "main" {
				t.Fatalf("StartFlowPlan request = %#v", req)
			}
			return model.FlowStartResult{LaunchContext: actions.AgentLaunchContext{
				Command:          req.AgentCommand,
				LaunchID:         "launch-1",
				RepoPath:         req.RepoPath,
				WorktreePath:     "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:           "flow/add-flow-mode",
				SessionStateRoot: req.SessionStateRoot,
				PlanPhaseID:      req.PlanPhaseID,
				PlanPhaseTitle:   req.PlanPhaseTitle,
				PlanPhaseStatus:  req.PlanPhaseStatus,
				FlowID:           "flow-1",
				FlowPhaseID:      req.PlanPhaseID,
				InitialPrompt:    "Use the wtui-flow skill for this launch.\n\nBuild the thing\n\nCreate and persist the plan with wtui plan save, link it back with wtui flow plan set.",
			}}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			calls = append(calls, "launch-agent")
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Add Flow Mode")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected title submit command")
	}
	m, _ = update(m, cmd())
	if got := m.ConfirmPrompt(); got != ui.FlowInstructionsPrompt {
		t.Fatalf("prompt = %q, want %q", got, ui.FlowInstructionsPrompt)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Build the thing")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected instructions submit command")
	}
	m, _ = update(m, cmd())
	if got := m.ConfirmPrompt(); got != ui.FlowBaseRefPrompt {
		t.Fatalf("prompt = %q, want %q", got, ui.FlowBaseRefPrompt)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("main")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("creation command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}

	if strings.Join(calls, ",") != "start-flow,launch-agent" {
		t.Fatalf("call order = %#v", calls)
	}
	if startRequest.AgentCommand != "codex" ||
		startRequest.SessionStateRoot != "/state/wtui/sessions/v1" ||
		startRequest.PlanPhaseID != "plan" ||
		startRequest.PlanPhaseTitle != "Plan" ||
		startRequest.PlanPhaseStatus != flowstore.PhaseRunning {
		t.Fatalf("start request metadata = %#v", startRequest)
	}
	if launched.Command != "codex" ||
		launched.RepoPath != "/dev/alpha" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		launched.Branch != "flow/add-flow-mode" ||
		launched.SessionStateRoot != "/state/wtui/sessions/v1" ||
		launched.FlowID != "flow-1" ||
		launched.FlowPhaseID != "plan" ||
		launched.PlanPhaseID != "plan" ||
		launched.LaunchID != "launch-1" {
		t.Fatalf("launch context = %#v", launched)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{"wtui-flow", "build the thing", "create and persist the plan", "wtui plan save", "wtui flow plan set"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("launch prompt missing %q: %q", want, launched.InitialPrompt)
		}
	}
	for _, unwanted := range []string{"flow-1", "flow/add-flow-mode", "/dev/alpha-worktrees/flow-add-flow-mode", "base ref", "add flow mode"} {
		if strings.Contains(prompt, strings.ToLower(unwanted)) {
			t.Fatalf("launch prompt should not include metadata %q: %q", unwanted, launched.InitialPrompt)
		}
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
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			calls = append(calls, "launch-agent")
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "main")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, _ = update(m, launchMsg)

	if strings.Join(calls, ",") != "start-flow,launch-agent" {
		t.Fatalf("call order = %#v", calls)
	}
}

func TestModel_NewFlowStaleLaunchIgnoredAfterRepoChange(t *testing.T) {
	launched := false
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
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Stale Flow")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected title submit command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Do the stale thing")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected instructions submit command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("main")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if createCmd == nil {
		t.Fatal("expected flow creation command")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	staleMsg := createCmd()
	if launchMsg, ok := staleMsg.(model.PlanLaunchRequestedMsg); !ok || launchMsg.Request == 0 {
		t.Fatalf("creation command returned %#v, want tagged PlanLaunchRequestedMsg", staleMsg)
	}
	_, cmd = update(m, staleMsg)
	if cmd != nil {
		t.Fatalf("stale launch returned command %T, want nil", cmd)
	}
	if launched {
		t.Fatal("stale flow creation launch should be ignored after repo change")
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
			return actions.TerminalLaunchSpec{}, errors.New("no terminal")
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
	_, _ = update(m, launchMsg)

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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(title)})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected title submit command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(instructions)})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected instructions submit command")
	}
	m, _ = update(m, cmd())
	if baseRef != "" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(baseRef)})
	}
	return update(m, tea.KeyMsg{Type: tea.KeyEnter})
}
