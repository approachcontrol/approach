package model_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/ui"
)

func plansInRightPane(t *testing.T, m model.Model, records []planstore.PlanRecord) model.Model {
	t.Helper()
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 18})
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

func TestModel_MovingToDifferentPlanCollapsesExpandedPhases(t *testing.T) {
	m := model.New(testRepos())
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}}},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Other plan", Status: "draft",
			Phases: []planstore.PlanPhase{{PhaseID: "p2", Title: "Other phase", Status: "pending", Order: 1}}},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	view := m.View()
	if strings.Contains(view, "Tracer bullet") || strings.Contains(view, "Other phase") {
		t.Fatalf("moving to another plan should collapse expanded phases:\n%s", view)
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
