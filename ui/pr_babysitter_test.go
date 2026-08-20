package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/flowstore"
)

func TestRenderPRBabysitterPaneShowsDashboardColumnsAndStatuses(t *testing.T) {
	statuses := []struct {
		mergeability string
		checks       string
	}{
		{mergeability: "mergeable", checks: "passing"},
		{mergeability: "conflicting", checks: "failing"},
		{mergeability: "unknown", checks: "pending"},
	}
	rows := make([]PRBabysitterRow, 0, len(statuses))
	for index, status := range statuses {
		beadID := ""
		if index != 1 {
			beadID = "approach-123"
		}
		rows = append(rows, PRBabysitterRow{
			Flow:         flowstore.FlowRecord{FlowID: "flow-" + string(rune('a'+index))},
			Repo:         "approach",
			Title:        "Flow title",
			BeadID:       beadID,
			Mergeability: status.mergeability,
			Checks:       status.checks,
		})
	}
	plain := ansi.Strip(strings.Join(renderPRBabysitterPane(rows, 1, 0, 120, 8, "", "", nil), "\n"))
	for _, want := range []string{"Repo", "Flow", "Bead", "Mergeability", "Checks", "mergeable", "conflicting", "unknown", "passing", "failing", "pending", ">"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, plain)
		}
	}
	lines := strings.Split(plain, "\n")
	if len(lines) < 3 || !strings.Contains(lines[2], "Flow title") || strings.Contains(lines[2], "approach-123") {
		t.Fatalf("blank Bead cell did not stay blank on second row:\n%s", plain)
	}
}

func TestRenderPRBabysitterPaneKeepsLiveStatusVisibleAtOrdinaryWidth(t *testing.T) {
	rows := []PRBabysitterRow{{
		Flow:         flowstore.FlowRecord{FlowID: "flow-1"},
		Repo:         "approachcontrol/approach",
		Title:        "PR babysitter dashboard",
		BeadID:       "approach-dpn",
		Mergeability: "conflicting",
		Checks:       "failing",
	}}

	plain := ansi.Strip(strings.Join(renderPRBabysitterPane(rows, 0, 0, 60, 3, "", "", nil), "\n"))
	for _, want := range []string{"Mergeability", "Checks", "conflicting", "failing"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("ordinary-width dashboard missing live status %q:\n%s", want, plain)
		}
	}
}

func TestRenderPRBabysitterPaneExpandsPhasesAndTruncatesNarrowRows(t *testing.T) {
	flow := flowstore.FlowRecord{
		FlowID: "flow-1",
		Phases: []flowstore.FlowPhase{{PhaseID: "merge", Title: "Merge", Kind: flowstore.KindMerge, Status: flowstore.PhaseBlocked}},
	}
	rows := []PRBabysitterRow{{
		Flow: flow, Repo: "repository-with-a-long-name", Title: "A very long Flow title", Mergeability: "conflicting", Checks: "failing",
	}}
	lines := renderPRBabysitterPane(rows, 0, 0, 100, 5, flow.FlowID, "merge", nil)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "merge") || !strings.Contains(plain, "blocked") {
		t.Fatalf("expanded phase missing:\n%s", plain)
	}
	lines = renderPRBabysitterPane(rows, 0, 0, 42, 5, flow.FlowID, "merge", nil)
	for index, line := range lines {
		if ansi.StringWidth(line) > 42 {
			t.Fatalf("line %d width = %d, want at most 42: %q", index, ansi.StringWidth(line), line)
		}
	}
}

func TestRenderPRBabysitterTakeoverKeepsHeadersEmptyStatesWarningsAndTerminalDock(t *testing.T) {
	flow := flowstore.FlowRecord{FlowID: "flow-1", Title: "Watch me", RepoPath: "/dev/approach"}
	view := ansi.Strip(Render(RenderParams{
		Width:       150,
		Height:      30,
		Mode:        ModePRBabysitter,
		TopMode:     ModeWorktrees,
		BottomMode:  ModeFlows,
		ContentPane: PaneBottom,
		ActivePane:  PaneBottom,
		Selected:    0, FlowParams: FlowParams{
			PRBabysitter:                   true,
			Flows:                          []flowstore.FlowRecord{flow},
			PRBabysitterRows:               []PRBabysitterRow{{Flow: flow, Repo: "approach", Title: "Watch me", Mergeability: "unknown", Checks: "pending"}},
			PRBabysitterListError:          "temporary failure",
			PRBabysitterDegradationWarning: "Skipped 1 unreadable Flow",
			FlowSelected:                   0}, EmbeddedTerminalParams: EmbeddedTerminalParams{
			EmbeddedTerminals:       []EmbeddedTerminalTab{{Number: 1, Provider: "codex", Active: true}},
			EmbeddedTerminalVisible: true,
			EmbeddedTerminalLines:   []string{"terminal output"}}}))
	for _, want := range []string{"[^p] PR babysitter", "^a active flows", "showing cached data", "Skipped 1 unreadable Flow", "Watch me", "terminal output", "ctrl+p"} {
		if !strings.Contains(view, want) {
			t.Fatalf("takeover missing %q:\n%s", want, view)
		}
	}

	empty := ansi.Strip(Render(RenderParams{
		Width: 100, Height: 20, Mode: ModePRBabysitter, TopMode: ModeWorktrees, BottomMode: ModeFlows, RightEmptyMessage: "loading PR babysitter", Selected: 0, FlowParams: FlowParams{
			PRBabysitter: true}}))
	if !strings.Contains(empty, "loading PR babysitter") {
		t.Fatalf("initial loading state missing:\n%s", empty)
	}
}
