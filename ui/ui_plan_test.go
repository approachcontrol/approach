package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
)

func TestRender_PlansModeShowsHeaderAndRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID:    "plan-1",
			Title:     "Persist plans",
			Status:    "in_progress",
			Branch:    "feature/plans",
			UpdatedAt: time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC),
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
				{PhaseID: "p2", Title: "CLI", Status: "pending", Order: 2},
			},
		}},
		ActivePane:   1,
		PlanSelected: 0,
	})

	for _, want := range []string{"[7] plans", "Status", "Branch", "Phase", "Updated", "Title", "in_progress", "feature/plans", "1/2", "2026-06-06", "Persist plans"} {
		if !strings.Contains(view, want) {
			t.Fatalf("plans view missing %q:\n%s", want, view)
		}
	}
}

func TestPlanPhaseProgressShowsDashWhenNoPhases(t *testing.T) {
	got := planPhaseProgress(planstore.PlanRecord{})
	if got != "-" {
		t.Fatalf("want dash for plan with no phases, got %q", got)
	}
}

func TestRender_PlansModeEmptyMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
	}{
		{name: "empty", message: "No plans"},
		{name: "fetch failure", message: "Could not load plans; see status bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:             []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
				Selected:          0,
				Width:             120,
				Height:            10,
				Mode:              ModePlans,
				RightEmptyMessage: tc.message,
			})
			if !strings.Contains(view, tc.message) {
				t.Fatalf("plans empty view missing %q:\n%s", tc.message, view)
			}
		})
	}
}

func TestRender_PlansModeShowsPlanShortcut(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    120,
		Height:   10,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "draft",
		}},
		ActivePane:   1,
		PlanSelected: 0,
	})
	if !strings.Contains(shortcutPaneText(view), "enter  plan") {
		t.Fatalf("plans view should expose plan shortcut:\n%s", view)
	}
}

func TestRender_PlanTextOverlayShowsBody(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:    0,
		Width:       120,
		Height:      10,
		Mode:        ModePlans,
		Overlay:     OverlayPlanText,
		OverlayText: "# Persist plans\n\nfull body line\n",
	})
	for _, want := range []string{"# Persist plans", "full body line", "esc: close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("plan text overlay missing %q:\n%s", want, view)
		}
	}
}
