package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/ui"
)

func plansInRightPane(t *testing.T, m model.Model, records []planstore.PlanRecord) model.Model {
	t.Helper()
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: records, ListRequest: m.ListRequest(ui.ModePlans)})
	return m
}

func TestModel_EnterOnPlanOpensTextOverlay(t *testing.T) {
	var gotPlanID string
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadPlan: func(planID string) (string, error) {
			gotPlanID = planID
			return "# Persist plans\n\nfull body\n", nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft"},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayPlanText {
		t.Fatalf("expected OverlayPlanText, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected plan read command")
	}
	msg, ok := cmd().(model.PlanReadResultMsg)
	if !ok {
		t.Fatalf("expected PlanReadResultMsg, got %T", msg)
	}
	m, _ = update(m, msg)

	if gotPlanID != "plan-1" {
		t.Fatalf("reader got plan id %q, want plan-1", gotPlanID)
	}
	if got := m.OverlayText(); !strings.Contains(got, "# Persist plans") || !strings.Contains(got, "full body") {
		t.Fatalf("unexpected plan overlay text: %q", got)
	}
}

func TestModel_PlanReadErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadPlan: func(string) (string, error) { return "", errors.New("missing plan file") },
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "Persist plans", Status: "draft"},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected plan read command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "missing plan file") {
		t.Fatalf("expected missing plan error in view:\n%s", m.View())
	}
	if m.OverlayText() != "" {
		t.Fatalf("expected empty overlay text on error, got %q", m.OverlayText())
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
