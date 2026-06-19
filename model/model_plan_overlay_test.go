package model_test

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/ui"
)

func plansInRightPane(t *testing.T, m model.Model, records []planstore.PlanRecord) model.Model {
	t.Helper()
	return plansInRightPaneAtSize(t, m, records, 140, 18)
}

func plansInRightPaneAtSize(t *testing.T, m model.Model, records []planstore.PlanRecord, width, height int) model.Model {
	t.Helper()
	m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: height})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: records, ListRequest: m.ListRequest(ui.ModePlans)})
	return m
}

func TestModel_EnterOnPlanExpandsPhasesWithoutReadingPlan(t *testing.T) {
	readCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadPlan: func(planID string) (string, error) {
			readCalled = true
			return "# Persist plans\n\nfull body\n", nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("plans-mode enter should not return a plan read command")
	}
	if readCalled {
		t.Fatal("plans-mode enter should not call ReadPlan")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	view := m.View()
	for _, want := range []string{"Tracer bullet", "completed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded plan view missing %q:\n%s", want, view)
		}
	}
}

func TestModel_OKeyOnPlanOpensPlanText(t *testing.T) {
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
			return "# Persist plans\n\nfull body\n", nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("plans-mode o should return a plan read command")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	_, cmd = update(m, cmd())
	if cmd == nil {
		t.Fatal("expected plan text pager command")
	}
	for _, want := range []string{"# Persist plans", "full body"} {
		if len(paged) != 1 || !strings.Contains(paged[0], want) {
			t.Fatalf("paged plan text missing %q: %#v", want, paged)
		}
	}
}

func TestModel_PlansFilterMatchesPlanAndPhaseFields(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft", Branch: "feature/plans",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Unrelated", Status: "blocked", Branch: "main"},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("tracer") {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	got := m.Plans()
	if len(got) != 1 || got[0].PlanID != "plan-1" {
		t.Fatalf("filtered plans by phase title = %#v", got)
	}
}

func TestModel_EnterOnExpandedPlanCollapsesPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("plans-mode second enter should not return a command")
	}
	if strings.Contains(m.View(), "Tracer bullet") {
		t.Fatalf("second enter should collapse phases:\n%s", m.View())
	}
}

func TestModel_DownOnExpandedPlanSelectsFirstPhase(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Other plan", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p2", Title: "Other phase", Status: "pending", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.PlanSelected(); got != 0 {
		t.Fatalf("phase selection should keep selected plan, got %d", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "p1" {
		t.Fatalf("selected phase = %q, want p1", got)
	}
	if view := m.View(); !strings.Contains(view, "Tracer bullet") || strings.Contains(view, "Other phase") {
		t.Fatalf("expanded selected plan should stay visible and not expand another plan:\n%s", view)
	}
}

func TestModel_NoOpPlanMovementKeepsExpandedPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.View(), "Tracer bullet") {
		t.Fatalf("no-op movement should keep phases expanded:\n%s", m.View())
	}
}

func TestModel_ExpandedPlanAtViewportBottomScrollsPhasesIntoView(t *testing.T) {
	m := model.New(testRepos())
	records := []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft"},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Plan 2", Status: "draft"},
		{PlanID: "plan-3", RepoPath: "/dev/alpha", Title: "Plan 3", Status: "draft"},
		{PlanID: "plan-4", RepoPath: "/dev/alpha", Title: "Plan 4", Status: "draft"},
		{PlanID: "plan-5", RepoPath: "/dev/alpha", Title: "Plan 5", Status: "draft",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Bottom phase one", Status: "pending", Order: 1},
				{PhaseID: "p2", Title: "Bottom phase two", Status: "completed", Order: 2},
			}},
	}
	m = plansInRightPaneAtSize(t, m, records, 140, ui.BranchContentOverhead+4)
	for i := 0; i < 4; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.PlanScroll(); got != 4 {
		t.Fatalf("expanded bottom plan should scroll phase block into view, got scroll %d", got)
	}
	view := m.View()
	for _, want := range []string{"Bottom phase one", "Bottom phase two"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded bottom plan missing %q:\n%s", want, view)
		}
	}
}

func TestModel_ExpandedSinglePlanScrollsWithinManyPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPaneAtSize(t, m, []planstore.PlanRecord{{
		PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1},
			{PhaseID: "p2", Title: "Phase 2", Status: "completed", Order: 2},
			{PhaseID: "p3", Title: "Phase 3", Status: "pending", Order: 3},
			{PhaseID: "p4", Title: "Phase 4", Status: "pending", Order: 4},
			{PhaseID: "p5", Title: "Phase 5", Status: "pending", Order: 5},
		},
	}}, 140, ui.BranchContentOverhead+4)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 3; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	if got := m.PlanSelected(); got != 0 {
		t.Fatalf("scrolling inside expanded single plan should not move selection, got %d", got)
	}
	if got := m.PlanScroll(); got != 1 {
		t.Fatalf("expected expanded phase block to scroll to 1, got %d", got)
	}
	view := m.View()
	if !strings.Contains(view, "Phase 3") {
		t.Fatalf("selected expanded phase should be reachable:\n%s", view)
	}
	if strings.Contains(view, "Plan 1") {
		t.Fatalf("scrolling within an oversized expanded plan should move past the plan row:\n%s", view)
	}
}

func TestModel_ReflowKeepsSelectedPlanPhaseVisible(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPaneAtSize(t, m, []planstore.PlanRecord{{
		PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1},
			{PhaseID: "p2", Title: "Phase 2", Status: "completed", Order: 2},
			{PhaseID: "p3", Title: "Phase 3", Status: "pending", Order: 3},
			{PhaseID: "p4", Title: "Phase 4", Status: "pending", Order: 4},
			{PhaseID: "p5", Title: "Phase 5", Status: "pending", Order: 5},
		},
	}}, 140, ui.BranchContentOverhead+4)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 5; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := m.SelectedPlanPhaseID(); got != "p5" {
		t.Fatalf("selected phase = %q, want p5", got)
	}
	if got := m.PlanScroll(); got != 3 {
		t.Fatalf("expected selected phase scroll before reflow to be 3, got %d", got)
	}

	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: ui.BranchContentOverhead + 4})
	if got := m.SelectedPlanPhaseID(); got != "p5" {
		t.Fatalf("selected phase after reflow = %q, want p5", got)
	}
	if got := m.PlanScroll(); got != 3 {
		t.Fatalf("expected reflow to keep selected phase visible at scroll 3, got %d", got)
	}
	view := m.View()
	if !strings.Contains(view, "Phase 5") {
		t.Fatalf("selected phase should remain visible after reflow:\n%s", view)
	}
	if strings.Contains(view, "Plan 1") {
		t.Fatalf("reflow should not snap back to the plan row while a phase is selected:\n%s", view)
	}
}

func TestModel_TallExpandedPlanAtViewportBottomShowsFirstPhases(t *testing.T) {
	m := model.New(testRepos())
	records := []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft"},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Plan 2", Status: "draft"},
		{PlanID: "plan-3", RepoPath: "/dev/alpha", Title: "Plan 3", Status: "draft"},
		{PlanID: "plan-4", RepoPath: "/dev/alpha", Title: "Plan 4", Status: "draft"},
		{PlanID: "plan-5", RepoPath: "/dev/alpha", Title: "Plan 5", Status: "draft",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1},
				{PhaseID: "p2", Title: "Phase 2", Status: "completed", Order: 2},
				{PhaseID: "p3", Title: "Phase 3", Status: "pending", Order: 3},
				{PhaseID: "p4", Title: "Phase 4", Status: "pending", Order: 4},
				{PhaseID: "p5", Title: "Phase 5", Status: "pending", Order: 5},
			}},
	}
	m = plansInRightPaneAtSize(t, m, records, 140, ui.BranchContentOverhead+4)
	for i := 0; i < 4; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.PlanScroll(); got != 4 {
		t.Fatalf("tall expanded bottom plan should move plan row to top, got scroll %d", got)
	}
	view := m.View()
	for _, want := range []string{"Plan 5", "Phase 1", "Phase 2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tall expanded bottom plan should reveal initial phases, missing %q:\n%s", want, view)
		}
	}
}

func TestModel_ExpandedPlanPhaseSelectionMovesToNextPlanAfterLastPhase(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPaneAtSize(t, m, []planstore.PlanRecord{
		{
			PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1},
				{PhaseID: "p2", Title: "Phase 2", Status: "completed", Order: 2},
				{PhaseID: "p3", Title: "Phase 3", Status: "pending", Order: 3},
				{PhaseID: "p4", Title: "Phase 4", Status: "pending", Order: 4},
				{PhaseID: "p5", Title: "Phase 5", Status: "pending", Order: 5},
			},
		},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Plan 2", Status: "draft"},
	}, 140, ui.BranchContentOverhead+4)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 6; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	if got := m.PlanSelected(); got != 1 {
		t.Fatalf("expected movement to next plan after last phase, got %d", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("phase selection should clear after moving to next plan, got %q", got)
	}
	view := m.View()
	if strings.Contains(view, "Phase 5") {
		t.Fatalf("moving to next plan should collapse expanded phases:\n%s", view)
	}
	if !strings.Contains(view, "Plan 2") {
		t.Fatalf("next plan should be selected and visible:\n%s", view)
	}
}

func TestModel_ExpandedSinglePlanKeepsLastPhaseSelectedAtBottomBoundary(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPaneAtSize(t, m, []planstore.PlanRecord{{
		PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1},
			{PhaseID: "p2", Title: "Phase 2", Status: "pending", Order: 2},
		},
	}}, 140, ui.BranchContentOverhead+4)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedPlanPhaseID(); got != "p2" {
		t.Fatalf("selected phase = %q, want p2", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedPlanPhaseID(); got != "p2" {
		t.Fatalf("single-plan bottom boundary should keep last phase selected, got %q", got)
	}
	if got := m.PlanSelected(); got != 0 {
		t.Fatalf("single-plan bottom boundary should keep selected plan, got %d", got)
	}
	if view := m.View(); !strings.Contains(view, "Phase 2") {
		t.Fatalf("last selected phase should remain visible:\n%s", view)
	}
}

func TestModel_ExpandedPlanScrollsUpWithinManyPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPaneAtSize(t, m, []planstore.PlanRecord{
		{PlanID: "plan-0", RepoPath: "/dev/alpha", Title: "Plan 0", Status: "draft"},
		{
			PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1},
				{PhaseID: "p2", Title: "Phase 2", Status: "completed", Order: 2},
				{PhaseID: "p3", Title: "Phase 3", Status: "pending", Order: 3},
				{PhaseID: "p4", Title: "Phase 4", Status: "pending", Order: 4},
				{PhaseID: "p5", Title: "Phase 5", Status: "pending", Order: 5},
			},
		},
	}, 140, ui.BranchContentOverhead+4)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 2; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})

	if got := m.PlanSelected(); got != 1 {
		t.Fatalf("scrolling up inside expanded plan should not move selection, got %d", got)
	}
	if got := m.PlanScroll(); got != 1 {
		t.Fatalf("expected expanded phase block to scroll up to 1, got %d", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.PlanSelected(); got != 1 {
		t.Fatalf("returning from first phase should keep selected plan, got %d", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("phase selection should clear when returning to plan row, got %q", got)
	}
	view := m.View()
	if !strings.Contains(view, "Phase 1") {
		t.Fatalf("expanded phases should remain visible after returning to plan row:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.PlanSelected(); got != 0 {
		t.Fatalf("expected movement to previous plan after returning to plan row, got %d", got)
	}
	view = m.View()
	if strings.Contains(view, "Phase 1") {
		t.Fatalf("moving to previous plan should collapse expanded phases:\n%s", view)
	}
	if !strings.Contains(view, "Plan 0") {
		t.Fatalf("previous plan should be selected and visible:\n%s", view)
	}
}

func TestModel_CollapsingExpandedPlanResumesPlanMovement(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1}}},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Plan 2", Status: "draft"},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedPlanPhaseID(); got != "p1" {
		t.Fatalf("selected phase = %q, want p1", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.PlanSelected(); got != 1 {
		t.Fatalf("expected plan movement after collapse, got %d", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("selected phase should clear after collapse, got %q", got)
	}
	if view := m.View(); strings.Contains(view, "Phase 1") {
		t.Fatalf("collapsed plan should not show phase rows:\n%s", view)
	}
}

func TestModel_BackspaceAwayFromPlansAndTabBackKeepsSelectedPhaseCleared(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Plan 1", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Phase 1", Status: "completed", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedPlanPhaseID(); got != "p1" {
		t.Fatalf("selected phase = %q, want p1", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.ActivePane(); got != 0 {
		t.Fatalf("backspace should move focus to repo pane, got active pane %d", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("selected phase should clear when focus leaves plans pane, got %q", got)
	}
	if view := m.View(); !strings.Contains(view, "Phase 1") {
		t.Fatalf("leaving plans should preserve phase expansion:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.ActivePane(); got != 1 {
		t.Fatalf("tab should return focus to plans pane, got active pane %d", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("selected phase should not restore when focus returns, got %q", got)
	}
}

func TestModel_PlanListReplacementClearsExpandedPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	}, ListRequest: m.ListRequest(ui.ModePlans)})
	if strings.Contains(m.View(), "Tracer bullet") {
		t.Fatalf("plan replacement should clear expanded phases:\n%s", m.View())
	}
}

func TestModel_PlanRefetchStartClearsExpandedPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd := update(m, model.GitFetchedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected plans refetch command")
	}
	if strings.Contains(m.View(), "Tracer bullet") {
		t.Fatalf("plan refetch start should clear expanded phases:\n%s", m.View())
	}
}

func TestModel_FilterSelectionChangeClearsExpandedPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Needle plan", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p2", Title: "Needle phase", Status: "pending", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("needle") {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view := m.View()
	if strings.Contains(view, "Tracer bullet") || strings.Contains(view, "Needle phase") {
		t.Fatalf("filter changing selected plan should clear expanded phases:\n%s", view)
	}
}

func TestModel_PlanFilterChangeClearsExpandedPhasesEvenWhenSelectionStays(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("persist") {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if strings.Contains(m.View(), "Tracer bullet") {
		t.Fatalf("plan filtering should collapse expanded phases:\n%s", m.View())
	}
}
