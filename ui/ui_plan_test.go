package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
)

func TestRender_PlansModeShowsHeaderAndRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
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
		ActivePane:   PaneBottom,
		PlanSelected: 0,
	})

	for _, want := range []string{"[3] plans", "Status", "Branch", "Phase", "Updated", "Title", "in_progress", "feature/plans", "1/2", "2026-06-06", "Persist plans"} {
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
				Repos:             []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
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
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    120,
		Height:   14,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "draft",
		}},
		ActivePane:   PaneBottom,
		PlanSelected: 0,
	})
	pane := shortcutPaneText(view)
	for _, want := range []string{"x      phases", "o      open", "e      edit", "a      implement", "y      copy path"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("plans view should expose shortcut %q:\n%s", want, view)
		}
	}
	for _, old := range []string{"enter  phases", "i      implement"} {
		if strings.Contains(pane, old) {
			t.Fatalf("plans view should not advertise old shortcut %q:\n%s", old, view)
		}
	}
}

func TestRender_PlansModeShowsPhaseShortcutWhenPhaseSelected(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    120,
		Height:   14,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "draft",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
			},
		}},
		ActivePane:          PaneBottom,
		PlanSelected:        0,
		ExpandedPlanID:      "plan-1",
		SelectedPlanPhaseID: "p1",
	})
	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "a      implement phase") {
		t.Fatalf("selected phase shortcut should expose phase implementation:\n%s", view)
	}
	if strings.Contains(pane, "a      implement\n") {
		t.Fatalf("selected phase shortcut should not expose whole-plan implementation:\n%s", view)
	}
}

func TestRender_PlansModeIgnoresStaleSelectedPhaseForShortcut(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    120,
		Height:   14,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "draft",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
			},
		}},
		ActivePane:          PaneBottom,
		PlanSelected:        0,
		ExpandedPlanID:      "plan-1",
		SelectedPlanPhaseID: "missing",
	})
	pane := shortcutPaneText(view)
	if strings.Contains(pane, "a      implement phase") {
		t.Fatalf("stale phase selection should not expose phase implementation:\n%s", view)
	}
	if !strings.Contains(pane, "a      implement") {
		t.Fatalf("stale phase selection should fall back to whole-plan implementation:\n%s", view)
	}
}

func TestRender_PlansModeOmitsPlanShortcutsWhenNoPlanSelected(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected:   0,
		Width:      120,
		Height:     10,
		Mode:       ModePlans,
		ActivePane: PaneBottom,
	})

	pane := shortcutPaneText(view)
	for _, forbidden := range []string{"x      phases", "o      open", "e      edit", "a      implement", "y      copy path", "i      implement"} {
		if strings.Contains(pane, forbidden) {
			t.Fatalf("empty plans view should omit %q:\n%s", forbidden, view)
		}
	}
}

func TestRender_PlansModeShowsExpandedPhasesForSelectedPlanOnly(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{
			{
				PlanID: "plan-1",
				Title:  "Persist plans",
				Status: "in_progress",
				Phases: []planstore.PlanPhase{
					{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
					{PhaseID: "p2", Title: "CLI", Status: "pending", Order: 2},
				},
			},
			{
				PlanID: "plan-2",
				Title:  "Other plan",
				Status: "draft",
				Phases: []planstore.PlanPhase{
					{PhaseID: "p3", Title: "Other phase", Status: "blocked", Order: 1},
				},
			},
		},
		ActivePane:     PaneBottom,
		PlanSelected:   0,
		ExpandedPlanID: "plan-1",
	})

	for _, want := range []string{"Store", "completed", "CLI", "pending"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded plan view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Other phase") || strings.Contains(view, "blocked") {
		t.Fatalf("only the selected expanded plan should show phase rows:\n%s", view)
	}
}

func TestRender_PlansModeHighlightsSelectedPhaseRow(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "in_progress",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
				{PhaseID: "p2", Title: "CLI", Status: "pending", Order: 2},
			},
		}},
		ActivePane:          PaneBottom,
		PlanSelected:        0,
		ExpandedPlanID:      "plan-1",
		SelectedPlanPhaseID: "p2",
	})

	if !strings.Contains(view, "> pending") || !strings.Contains(view, "CLI") {
		t.Fatalf("selected phase row should be highlighted:\n%s", view)
	}
	if strings.Contains(view, "> in_progress") {
		t.Fatalf("plan row should not be highlighted while a phase is selected:\n%s", view)
	}
}

func TestRender_PlansModeIgnoresStaleSelectedPhaseForHighlight(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "in_progress",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
			},
		}},
		ActivePane:          PaneBottom,
		PlanSelected:        0,
		ExpandedPlanID:      "plan-1",
		SelectedPlanPhaseID: "missing",
	})

	if strings.Contains(view, "> completed") {
		t.Fatalf("stale phase selection should not highlight phase rows:\n%s", view)
	}
	if !strings.Contains(view, "> in_progress") {
		t.Fatalf("stale phase selection should leave plan row highlighted:\n%s", view)
	}
}

func TestRender_PlansModeKeepsExpandedPhasesWhenRightPaneInactive(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "in_progress",
			Phases: []planstore.PlanPhase{
				{PhaseID: "p1", Title: "Store", Status: "completed", Order: 1},
			},
		}},
		ActivePane:     PaneRepos,
		PlanSelected:   0,
		ExpandedPlanID: "plan-1",
	})

	if !strings.Contains(view, "Store") || !strings.Contains(view, "completed") {
		t.Fatalf("expanded phases should stay visible when focus leaves plans pane:\n%s", view)
	}
}

func TestRender_PlansModeShowsNoPhasesOnlyWhenExpanded(t *testing.T) {
	params := RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
		Selected: 0,
		Width:    140,
		Height:   10,
		Mode:     ModePlans,
		Plans: []planstore.PlanRecord{{
			PlanID: "plan-1",
			Title:  "Persist plans",
			Status: "draft",
		}},
		ActivePane:   PaneBottom,
		PlanSelected: 0,
	}

	if view := Render(params); strings.Contains(view, "No phases") {
		t.Fatalf("collapsed plan should not show no-phases fallback:\n%s", view)
	}
	params.ExpandedPlanID = "plan-1"
	if view := Render(params); !strings.Contains(view, "No phases") {
		t.Fatalf("expanded plan without phases should show fallback:\n%s", view)
	}
}

func TestRender_PlanTextOverlayShowsBody(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}},
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
