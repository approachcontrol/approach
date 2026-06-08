package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/scanner"
)

func TestRender_FlowsModeShowsHeaderAndRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Add Flow mode",
			Status:       flowstore.StatusInProgress,
			Branch:       "flow/add-flow-mode",
			WorktreePath: "/dev/wtui-worktrees/flow-add-flow-mode",
			PlanID:       "plan-1",
			PR:           flowstore.PullRequest{Number: 123, URL: "https://github.com/brian-bell/wtui/pull/123"},
			UpdatedAt:    time.Date(2026, 6, 7, 14, 0, 0, 0, time.UTC),
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
				{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 2},
			},
		}},
		ActivePane:   1,
		FlowSelected: 0,
	})

	for _, want := range []string{"[8] flows", "Status", "Branch", "Phase", "Plan", "PR", "Updated", "Title", "in_progress", "flow/add-flow-mode", "1/2", "plan-1", "#123", "2026-06-07", "Add Flow mode"} {
		if !strings.Contains(view, want) {
			t.Fatalf("flows view missing %q:\n%s", want, view)
		}
	}
}

func TestStatusBar_FlowsModeShowsNewFlowHint(t *testing.T) {
	bar := RenderStatusBar(120, ModeFlows, OverlayNone, 1, false, false, false)
	if !strings.Contains(bar, "n: new flow") {
		t.Fatalf("expected new flow hint in flows mode, got %q", bar)
	}
}

func TestRender_FlowsModeShowsUpdatedPhaseDrivenStates(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   18,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{
			{
				FlowID: "blocked-flow",
				Title:  "Blocked implementation",
				Status: flowstore.StatusBlocked,
				Branch: "flow/blocked",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Status: flowstore.PhaseCompleted},
					{PhaseID: "implementation", Status: flowstore.PhaseBlocked},
				},
			},
			{
				FlowID: "attention-flow",
				Title:  "Needs review input",
				Status: flowstore.StatusNeedsAttention,
				Branch: "flow/needs-attention",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Status: flowstore.PhaseCompleted},
					{PhaseID: "review-loop", Status: flowstore.PhaseNeedsAttention},
				},
			},
			{
				FlowID: "completed-flow",
				Title:  "Completed flow",
				Status: flowstore.StatusCompleted,
				Branch: "flow/completed",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Status: flowstore.PhaseCompleted},
					{PhaseID: "review-loop", Status: flowstore.PhaseSkipped},
				},
			},
		},
		ActivePane:   1,
		FlowSelected: 0,
	})

	for _, want := range []string{
		"blocked", "flow/blocked", "Blocked implementation",
		"needs_attention", "flow/needs-attention", "Needs review input",
		"completed", "flow/completed", "Completed flow",
		"1/2", "2/2",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("updated flows view missing %q:\n%s", want, view)
		}
	}
}

func TestFlowPhaseProgressShowsDashWhenNoPhases(t *testing.T) {
	got := flowPhaseProgress(flowstore.FlowRecord{})
	if got != "-" {
		t.Fatalf("want dash for flow with no phases, got %q", got)
	}
}

func TestRender_FlowsModeEmptyMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
	}{
		{name: "empty", message: "No flows"},
		{name: "fetch failure", message: "Could not load flows; see status bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:             []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
				Selected:          0,
				Width:             120,
				Height:            10,
				Mode:              ModeFlows,
				RightEmptyMessage: tc.message,
			})
			if !strings.Contains(view, tc.message) {
				t.Fatalf("flows empty view missing %q:\n%s", tc.message, view)
			}
		})
	}
}
