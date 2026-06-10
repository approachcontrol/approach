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
		Width:          120,
		Mode:           ModeFlows,
		ActivePane:     1,
		RepoSelected:   true,
		FlowSelected:   true,
		FlowPlanLinked: true,
	})
	for _, want := range []string{"x: phases", "o: open", "y: copy id"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("expected selected flow hint %q, got %q", want, bar)
		}
	}
}

func TestStatusBar_FlowsModeDistinguishesPhaseStatusFromLaunch(t *testing.T) {
	base := statusBarParams{
		Width:          120,
		Mode:           ModeFlows,
		ActivePane:     1,
		RepoSelected:   true,
		FlowSelected:   true,
		AgentAvailable: true,
	}

	gated := renderStatusBarWithState(base)
	if !strings.Contains(gated, "a: phase status") || strings.Contains(gated, "a: launch phase") {
		t.Fatalf("gated Flow should expose status action, got %q", gated)
	}

	base.FlowPhaseLaunchReady = true
	ready := renderStatusBarWithState(base)
	if !strings.Contains(ready, "a: launch phase") || strings.Contains(ready, "a: phase status") {
		t.Fatalf("ready Flow should expose launch action, got %q", ready)
	}
}

func TestRender_FlowsModeShowsResumeShortcutForResumableSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Resumable flow",
			Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseCompleted,
				Sessions: []flowstore.Session{
					{Provider: "codex", SessionID: "codex-1", Status: "ended"},
				},
			}},
		}},
		ActivePane:                 1,
		FlowSelected:               0,
		ExpandedFlowID:             "flow-1",
		SelectedFlowPhaseID:        "implementation",
		FlowPhaseResumableSelected: true,
	})

	if !strings.Contains(shortcutPaneText(view), "r      resume") {
		t.Fatalf("resumable Flow phase should expose resume shortcut:\n%s", view)
	}
}

func TestRender_FlowsModeShowsCopyPhaseIDShortcutForSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Phase copy flow",
			Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
			}},
		}},
		ActivePane:          1,
		FlowSelected:        0,
		ExpandedFlowID:      "flow-1",
		SelectedFlowPhaseID: "implementation",
	})

	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "y      copy phase id") {
		t.Fatalf("selected Flow phase should expose phase-id copy shortcut:\n%s", view)
	}
	if strings.Contains(pane, "y      copy id") {
		t.Fatalf("selected Flow phase should not expose whole-flow copy shortcut:\n%s", view)
	}
}

func TestRender_FlowsModeIgnoresStaleSelectedPhaseForCopyShortcut(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Stale phase copy flow",
			Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
			}},
		}},
		ActivePane:          1,
		FlowSelected:        0,
		ExpandedFlowID:      "flow-1",
		SelectedFlowPhaseID: "missing",
	})

	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "y      copy id") {
		t.Fatalf("stale Flow phase selection should fall back to whole-flow copy shortcut:\n%s", view)
	}
	if strings.Contains(pane, "y      copy phase id") {
		t.Fatalf("stale Flow phase selection should not expose phase-id copy shortcut:\n%s", view)
	}
}

func TestRender_FlowsModeHidesResumeShortcutWithoutResumableSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Awaiting flow",
			Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{
				PhaseID:   "implementation",
				Title:     "Implementation",
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-new"},
			}},
		}},
		ActivePane:          1,
		FlowSelected:        0,
		ExpandedFlowID:      "flow-1",
		SelectedFlowPhaseID: "implementation",
	})

	if strings.Contains(shortcutPaneText(view), "r      resume") {
		t.Fatalf("non-resumable Flow phase should not expose resume shortcut:\n%s", view)
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

func TestRender_FlowsModeExpandedPhaseRowsShowSessionSummary(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   10,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Flow sessions",
			Status:       flowstore.StatusInProgress,
			Branch:       "flow/sessions",
			WorktreePath: "/dev/wtui-worktrees/flow-sessions",
			Phases: []flowstore.FlowPhase{{
				PhaseID:   "implementation",
				Title:     "Implementation",
				Status:    flowstore.PhaseCompleted,
				LaunchIDs: []string{"launch-1", "launch-2"},
				Sessions: []flowstore.Session{
					{Provider: "claude", SessionID: "claude-old", LaunchID: "launch-1", Status: "ended", StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
					{Provider: "codex", SessionID: "codex-new", LaunchID: "launch-2", Status: "ended", StartedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)},
				},
			}},
		}},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: "flow-1",
	})

	for _, want := range []string{"implementation:completed", "2 sessions", "codex", "ended"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded phase session summary missing %q:\n%s", want, view)
		}
	}
}

func TestRender_FlowsModeExpandedPhaseRowsShowMissingSessionID(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   10,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Legacy sessions",
			Status:       flowstore.StatusNeedsAttention,
			Branch:       "flow/legacy-sessions",
			WorktreePath: "/dev/wtui-worktrees/flow-legacy-sessions",
			Phases: []flowstore.FlowPhase{{
				PhaseID:   "review-loop",
				Title:     "Review loop",
				Status:    flowstore.PhaseNeedsAttention,
				LaunchIDs: []string{"launch-old", "launch-1"},
				Sessions: []flowstore.Session{
					{Provider: "codex", LaunchID: "launch-1", Status: "ended"},
					{Provider: "claude", SessionID: "claude-old", LaunchID: "launch-old", Status: "ended", StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
				},
			}},
		}},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: "flow-1",
	})

	if !strings.Contains(view, "review-loop:missing-session-id") {
		t.Fatalf("malformed attached session should render missing-session-id:\n%s", view)
	}
	if strings.Contains(view, "1 session codex ended") {
		t.Fatalf("malformed attached session should not render as resumable metadata:\n%s", view)
	}
	if strings.Contains(view, "1 session claude ended") {
		t.Fatalf("malformed latest session should not render older session metadata:\n%s", view)
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

	for _, want := range []string{"plan-review", "changes_requested", "1/3", "a", "phase status"} {
		if !strings.Contains(view, want) {
			t.Fatalf("flows gate view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "launch phase") {
		t.Fatalf("gated flows view should not advertise launchable phase:\n%s", view)
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

func TestRender_FlowsModeShowsRecoveryWarnings(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    240,
		Height:   14,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{
			{
				FlowID: "missing-worktree",
				Title:  "Saved flow needs worktree metadata",
				Status: flowstore.StatusBlocked,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseBlocked, LaunchIDs: []string{"launch-1"}},
				},
			},
			{
				FlowID:       "awaiting-session",
				Title:        "Launch has not attached a session",
				Status:       flowstore.StatusInProgress,
				Branch:       "flow/awaiting-session",
				WorktreePath: "/dev/wtui-worktrees/flow-awaiting-session",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-2"}},
				},
			},
			{
				FlowID:       "mismatched-session",
				Title:        "Session launch mismatch",
				Status:       flowstore.StatusNeedsAttention,
				Branch:       "flow/session-mismatch",
				WorktreePath: "/dev/wtui-worktrees/flow-session-mismatch",
				Phases: []flowstore.FlowPhase{
					{
						PhaseID:   "review-loop",
						Title:     "Review Loop",
						Status:    flowstore.PhaseNeedsAttention,
						LaunchIDs: []string{"launch-3"},
						Sessions: []flowstore.Session{
							{Provider: "codex", SessionID: "codex-1", LaunchID: "other-launch", Status: "ended"},
						},
					},
				},
			},
		},
		ActivePane: 1,
	})

	for _, want := range []string{"plan:recover-worktree", "implementation:await-session", "review-loop:session-mismatch"} {
		if !strings.Contains(view, want) {
			t.Fatalf("recovery view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "missing-worktree") {
		t.Fatalf("flow with missing worktree metadata should show a recoverable branch marker:\n%s", view)
	}
}

func TestRender_FlowRecoveryWarningsFlagLatestRelaunchWithoutSession(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "relaunched-flow",
		Title:        "Relaunched flow",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/relaunch",
		WorktreePath: "/dev/wtui-worktrees/flow-relaunch",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Title:     "Implementation",
			Status:    flowstore.PhaseRunning,
			LaunchIDs: []string{"launch-old", "launch-new"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-old", LaunchID: "launch-old", Status: "ended"},
			},
		}},
	}

	if got := flowPhaseProgress(record); got != "0/1 implementation:await-session" {
		t.Fatalf("phase progress = %q, want latest relaunch without session to await session", got)
	}
	view := Render(RenderParams{
		Repos:        []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:     0,
		Width:        240,
		Height:       10,
		Mode:         ModeFlows,
		Flows:        []flowstore.FlowRecord{record},
		ActivePane:   1,
		FlowSelected: 0,
	})
	if !strings.Contains(view, "implementation:await-session") {
		t.Fatalf("rendered relaunch without session should await session:\n%s", view)
	}
	if strings.Contains(view, "session-mismatch") {
		t.Fatalf("rendered relaunch with healthy older session should not show mismatch:\n%s", view)
	}
}

func TestRender_FlowRecoveryWarningsPreservePhaseSpecificStates(t *testing.T) {
	flow := flowstore.FlowRecord{
		FlowID: "missing-worktree-with-history",
		Title:  "Missing worktree with history",
		Status: flowstore.StatusNeedsAttention,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "pr-creation", Title: "PR Creation", Status: flowstore.PhaseCompleted, Order: 2},
			{PhaseID: "autoreview", Title: "Autoreview", Status: flowstore.PhasePending, Order: 3},
		},
	}
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:       0,
		Width:          240,
		Height:         12,
		Mode:           ModeFlows,
		Flows:          []flowstore.FlowRecord{flow},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: flow.FlowID,
	})

	for _, want := range []string{"autoreview:missing-pr", "plan:completed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("recovery precedence view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "plan:recover-worktree") {
		t.Fatalf("expanded phase history should not be overwritten by flow-level recovery:\n%s", view)
	}

	flow.Phases[2].Status = flowstore.PhaseCompleted
	view = Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:       0,
		Width:          240,
		Height:         12,
		Mode:           ModeFlows,
		Flows:          []flowstore.FlowRecord{flow},
		ActivePane:     1,
		FlowSelected:   0,
		ExpandedFlowID: flow.FlowID,
	})
	if !strings.Contains(view, "autoreview:completed") || strings.Contains(view, "autoreview:missing-pr") {
		t.Fatalf("completed autoreview history should not be overwritten by missing PR recovery:\n%s", view)
	}
}

func TestFlowRecoveryLabelsDoNotFlagHealthySessionOrBranchOnlyRecord(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID: "branch-only",
		Title:  "Branch-only fixture",
		Status: flowstore.StatusNeedsAttention,
		Branch: "flow/branch-only",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "review-loop",
			Title:     "Review Loop",
			Status:    flowstore.PhaseNeedsAttention,
			LaunchIDs: []string{"launch-1"},
			Sessions: []flowstore.Session{
				{Provider: "codex", SessionID: "codex-1", LaunchID: "launch-1", Status: "ended"},
			},
		}},
	}

	got := flowPhaseProgress(record)
	if got != "0/1 review-loop:needs_attention" {
		t.Fatalf("phase progress = %q, want healthy session and branch-only state preserved", got)
	}
	view := Render(RenderParams{
		Repos:        []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:     0,
		Width:        240,
		Height:       10,
		Mode:         ModeFlows,
		Flows:        []flowstore.FlowRecord{record},
		ActivePane:   1,
		FlowSelected: 0,
	})
	if !strings.Contains(view, "review-loop:needs_attention") {
		t.Fatalf("rendered branch-only healthy session should preserve phase state:\n%s", view)
	}
	for _, notWant := range []string{"session-mismatch", "recover-worktree"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("rendered branch-only healthy session should not contain %q:\n%s", notWant, view)
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
