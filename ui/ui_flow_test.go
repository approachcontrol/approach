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

	for _, want := range []string{"[8] flows", "Status", "Branch", "Phase", "PR", "Updated", "Title", "in_progress", "flow/add-flow-mode", "1/2", "#123", "2026-06-07", "Add Flow mode"} {
		if !strings.Contains(view, want) {
			t.Fatalf("flows view missing %q:\n%s", want, view)
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
