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
		Width:    230,
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
	if strings.Contains(view, "Review loop") {
		t.Fatalf("flow phase detail rows should be collapsed by default:\n%s", view)
	}
}

func TestStatusBar_FlowsModeShowsNewFlowHint(t *testing.T) {
	bar := RenderStatusBar(120, ModeFlows, OverlayNone, 1, false, false, false)
	if !strings.Contains(bar, "n: new flow") {
		t.Fatalf("expected new flow hint in flows mode, got %q", bar)
	}
}

func TestStatusBar_FlowsModeShowsPhaseToggleHintForSelectedFlow(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:        120,
		Mode:         ModeFlows,
		ActivePane:   1,
		RepoSelected: true,
		FlowSelected: true,
	})
	if !strings.Contains(bar, "x: phases") {
		t.Fatalf("expected phase toggle hint for selected flow, got %q", bar)
	}
}

func TestRender_FlowsModeShowsExpandedPhaseRowsWithFullPhaseIDs(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   10,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Add Flow mode",
			Status: flowstore.StatusInProgress,
			Branch: "flow/add-flow-mode",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Order: 1},
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 2},
			},
		}},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: "flow-1",
	})

	for _, want := range []string{"plan-review:completed", "Plan Review", "implementation:ready", "Implementation"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded flows view missing %q:\n%s", want, view)
		}
	}
	for _, clipped := range []string{"plan-re ", "impleme "} {
		if strings.Contains(view, clipped) {
			t.Fatalf("expanded phase ID appears clipped as %q:\n%s", clipped, view)
		}
	}
}

func TestRender_FlowsModeGroupsChildImplementationPhasesUnderParent(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Child phases",
			Status: flowstore.StatusInProgress,
			Branch: "flow/children",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved", Order: 2},
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 3},
				{PhaseID: "review-loop", Title: "Review Loop", Status: flowstore.PhasePending, Order: 4},
				{PhaseID: "implementation-api", ParentPhaseID: "implementation", Title: "API integration", Status: flowstore.PhaseReady, Order: 10},
			},
		}},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: "flow-1",
	})

	implementation := strings.Index(view, "implementation:completed")
	child := strings.LastIndex(view, "implementation-api:ready")
	review := strings.Index(view, "review-loop:pending")
	if implementation < 0 || child < 0 || review < 0 {
		t.Fatalf("expanded flows view missing expected phases:\n%s", view)
	}
	if !(implementation < child && child < review) {
		t.Fatalf("child phase should render under implementation before review-loop:\n%s", view)
	}
	if !strings.Contains(view, "  API integration") {
		t.Fatalf("child title should be visibly indented:\n%s", view)
	}
}

func TestRender_FlowsModeShowsUpdatedPhaseDrivenStates(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    230,
		Height:   12,
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

func TestRender_FlowsModeShowsMergedFlowsAsInspectableRows(t *testing.T) {
	mergedAt := time.Date(2026, 6, 8, 15, 4, 5, 0, time.UTC)
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "merged-flow",
			Title:  "Merged flow",
			Status: flowstore.StatusMerged,
			Branch: "flow/merged",
			PlanID: "plan-merged",
			PR:     flowstore.PullRequest{Number: 116, URL: "https://github.com/brian-bell/wtui/pull/116"},
			Merge: flowstore.Merge{
				Status:   flowstore.MergeMerged,
				Commit:   "0123456789abcdef",
				MergedAt: &mergedAt,
			},
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
				{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhaseCompleted, Outcome: "passed"},
				{PhaseID: "merge", Title: "Merge", Status: flowstore.PhaseCompleted, Outcome: "merged"},
			},
		}},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: "merged-flow",
	})

	for _, want := range []string{"merged", "flow/merged", "3/3", "plan-merged", "#116", "Merged flow", "merge:merged", "Merge"} {
		if !strings.Contains(view, want) {
			t.Fatalf("merged flows view missing %q:\n%s", want, view)
		}
	}
}

func TestRender_FlowsModeShowsPlanReviewGateState(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "review-flow",
			Title:  "Plan needs revision",
			Status: flowstore.StatusNeedsAttention,
			Branch: "flow/review",
			PlanID: "plan-1",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
				{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseNeedsAttention, Outcome: "changes_requested"},
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending},
			},
		}},
		ActivePane:     1,
		FlowSelected:   0,
		AgentAvailable: true,
	})

	for _, want := range []string{"plan-review", "changes_requested", "1/3", "a", "launch phase"} {
		if !strings.Contains(view, want) {
			t.Fatalf("flows gate view missing %q:\n%s", want, view)
		}
	}
}

func TestRender_FlowsModeShowsAutoreviewMissingPRMetadata(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "missing-pr-flow",
			Title:  "Needs PR metadata",
			Status: flowstore.StatusInProgress,
			Branch: "flow/missing-pr",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted},
				{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved},
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted},
				{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseCompleted},
				{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
				{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhasePending},
			},
		}},
		ActivePane:   1,
		FlowSelected: 0,
	})

	for _, want := range []string{"autoreview:missing-pr", "missing", "Needs PR metadata"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing PR metadata view missing %q:\n%s", want, view)
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
