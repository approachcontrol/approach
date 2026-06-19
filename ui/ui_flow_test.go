package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

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
	header := lineContaining(view, "Status")
	if strings.Contains(header, "Repo") {
		t.Fatalf("normal flows header should not include active-flows Repo column:\n%s", header)
	}
}

func TestRender_ActiveFlowsHeaderAndShortcutLabels(t *testing.T) {
	header := ansi.Strip(renderActiveFlowsHeader(80))
	if !strings.Contains(header, "active flows") {
		t.Fatalf("active-flow header missing lowercase title:\n%s", header)
	}
	for _, notWant := range []string{"F3", "Active flows", "current repo"} {
		if strings.Contains(header, notWant) {
			t.Fatalf("active-flow header should not contain %q:\n%s", notWant, header)
		}
	}

	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:    0,
		Width:       180,
		Height:      24,
		Mode:        ModeSessions,
		ActiveFlows: true,
		ActivePane:  1,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Active flow",
			Status: flowstore.StatusPending,
		}},
		FlowSelected: 0,
	})
	pane := shortcutPaneText(ansi.Strip(view))
	if !strings.Contains(pane, "f3") || !strings.Contains(pane, "active flows") {
		t.Fatalf("active-flow shortcut pane should keep f3 active flows hint:\n%s", pane)
	}
	if !strings.Contains(pane, "⌫      pane") {
		t.Fatalf("active-flow shortcut pane should expose backspace pane hint:\n%s", pane)
	}
}

func TestRender_ActiveFlowsShowsRepoColumnBetweenStatusAndBranch(t *testing.T) {
	view := Render(RenderParams{
		Repos: []scanner.Repo{
			{Path: "/dev/wtui", DisplayName: "wtui"},
			{Path: "/dev/client/api", DisplayName: "client/api"},
		},
		Selected:    0,
		Width:       220,
		Height:      12,
		Mode:        ModeSessions,
		ActiveFlows: true,
		ActivePane:  1,
		Flows: []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			Title:    "Active repo flow",
			Status:   flowstore.StatusInProgress,
			RepoPath: "/dev/wtui",
			Branch:   "flow/active-repo",
		}, {
			FlowID:   "flow-2",
			Title:    "Nested active repo flow",
			Status:   flowstore.StatusInProgress,
			RepoPath: "/dev/client/api",
			Branch:   "flow/nested-repo",
		}},
		FlowSelected: 0,
	})

	header := lineContaining(view, "Status")
	row := lineContaining(view, "flow/active-repo")
	nestedRow := lineContaining(view, "flow/nested-repo")
	requireOrderedColumns(t, header, "Status", "Repo", "Branch")
	requireOrderedColumns(t, row, flowstore.StatusInProgress, "wtui", "flow/active-repo")
	requireOrderedColumns(t, nestedRow, flowstore.StatusInProgress, "client/api", "flow/nested-repo")
	if strings.Contains(nestedRow, " api ") {
		t.Fatalf("nested active-flow repo label should preserve scanner display context, got basename row: %q", nestedRow)
	}
}

func TestRender_FlowsModeSplitsListAndEmbeddedTerminal(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    260,
		Height:   18,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Add embedded Flow terminal",
			Status: flowstore.StatusInProgress,
			Branch: "flow/embedded-terminal",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			},
		}},
		FlowSelected: 0,
		FlowEmbeddedTerminals: []EmbeddedTerminalTab{{
			Number:   1,
			Provider: "codex",
			Identity: "implementation",
			State:    "running",
			Active:   true,
		}},
		FlowEmbeddedTerminalLines: []string{
			"terminal line 1",
			"terminal line 2",
			"terminal line 3",
			"terminal line 4",
			"terminal line 5",
			"terminal line 6",
			"terminal line 7",
			"terminal line 8",
		},
		ActivePane: 1,
	})

	for _, want := range []string{
		"Add embedded Flow terminal",
		"1 codex implementation running",
		"terminal line 3",
		"terminal line 8",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("split Flow terminal view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "terminal line 1") || strings.Contains(view, "terminal line 2") {
		t.Fatalf("split Flow terminal should window old lines after border/header rows:\n%s", view)
	}
}

func TestRender_ActiveFlowsSplitPaneShowsRepoColumn(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:    0,
		Width:       240,
		Height:      14,
		Mode:        ModeSessions,
		ActiveFlows: true,
		ActivePane:  1,
		Flows: []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			Title:    "Split active repo flow",
			Status:   flowstore.StatusInProgress,
			RepoPath: "/dev/wtui",
			Branch:   "flow/split-repo",
		}},
		FlowSelected: 0,
		FlowEmbeddedTerminals: []EmbeddedTerminalTab{{
			Number:   1,
			Provider: "codex",
			Identity: "implementation",
			State:    "running",
			Active:   true,
		}},
		FlowEmbeddedTerminalLines: []string{"terminal line"},
	})

	header := lineContaining(view, "Status")
	row := lineContaining(view, "flow/split-repo")
	requireOrderedColumns(t, header, "Status", "Repo", "Branch")
	requireOrderedColumns(t, row, flowstore.StatusInProgress, "wtui", "flow/split-repo")
	if !strings.Contains(view, "terminal line") {
		t.Fatalf("active-flow split pane should still render terminal content:\n%s", view)
	}
}

func TestRender_FlowsModeSplitTerminalTinyViewportDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tiny Flow split terminal render should not panic: %v", r)
		}
	}()

	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    120,
		Height:   BranchContentOverhead - 1,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Tiny split terminal",
			Status: flowstore.StatusInProgress,
			Branch: "flow/tiny",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			},
		}},
		FlowEmbeddedTerminals: []EmbeddedTerminalTab{{
			Number:   1,
			Provider: "codex",
			Identity: "implementation",
			State:    "running",
			Active:   true,
		}},
		FlowEmbeddedTerminalLines: []string{"terminal output"},
		ActivePane:                1,
	})

	requireLinesWithinWidth(t, strippedLines(view), 120)
}

func TestRenderFlowSplitPaneWrapsOnlyTerminalPanelInBorder(t *testing.T) {
	records := []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Flow with split terminal",
		Status: flowstore.StatusInProgress,
		Branch: "flow/split-terminal",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
		},
	}}
	terminalLines := []string{
		"terminal line 1",
		"terminal line 2",
		"terminal line 3",
		"terminal line 4",
		"terminal line 5",
		"terminal line 6",
	}

	const width = 70
	const height = 10
	listHeight, terminalHeight := FlowSplitPanelHeights(height)
	lines := stripLines(renderFlowSplitPane(records, 0, 0, width, height, "", "", nil, []EmbeddedTerminalTab{{
		Number:   1,
		Provider: "codex",
		Identity: "implementation",
		State:    "running",
		Active:   true,
	}}, terminalLines, false, true, false, nil))

	if listHeight != 4 || terminalHeight != 6 {
		t.Fatalf("split heights = %d/%d, want 4/6", listHeight, terminalHeight)
	}
	if len(lines) != height {
		t.Fatalf("line count = %d, want %d:\n%s", len(lines), height, strings.Join(lines, "\n"))
	}
	requireLinesWithinWidth(t, lines, width)
	for i := 0; i < listHeight; i++ {
		if strings.ContainsAny(lines[i], "┌┐└┘│") {
			t.Fatalf("flow list allocation should not contain terminal frame at line %d: %q", i, lines[i])
		}
	}
	if !strings.Contains(lines[0], "Status") || !strings.Contains(lines[1], "flow/split-terminal") {
		t.Fatalf("flow list should remain visible above terminal:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[listHeight], "┌") {
		t.Fatalf("terminal top border should start at index %d:\n%s", listHeight, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[listHeight+1], "│ 1 codex implementation running") {
		t.Fatalf("terminal header should be first framed content row:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[listHeight+terminalHeight-1], "└") {
		t.Fatalf("terminal bottom border should land at index %d:\n%s", listHeight+terminalHeight-1, strings.Join(lines, "\n"))
	}
	if strings.Contains(strings.Join(lines, "\n"), "terminal line 1") ||
		strings.Contains(strings.Join(lines, "\n"), "terminal line 2") ||
		strings.Contains(strings.Join(lines, "\n"), "terminal line 3") {
		t.Fatalf("terminal body should show only latest lines after frame/header subtraction:\n%s", strings.Join(lines, "\n"))
	}
	for _, want := range []string{"terminal line 4", "terminal line 5", "terminal line 6"} {
		if !strings.Contains(strings.Join(lines, "\n"), want) {
			t.Fatalf("terminal body missing latest line %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

func TestRender_ActiveFlowsExpandedPhaseRowsKeepRepoColumnAlignment(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:    0,
		Width:       240,
		Height:      12,
		Mode:        ModeSessions,
		ActiveFlows: true,
		ActivePane:  1,
		Flows: []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			Title:    "Expanded active repo flow",
			Status:   flowstore.StatusInProgress,
			RepoPath: "/dev/wtui",
			Branch:   "flow/expanded",
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
				Order:   1,
			}},
		}},
		FlowSelected:        0,
		ExpandedFlowID:      "flow-1",
		SelectedFlowPhaseID: "implementation",
	})

	flowRow := lineContaining(view, "flow/expanded")
	phaseRow := lineContaining(view, "implementation:ready")
	requireOrderedColumns(t, flowRow, flowstore.StatusInProgress, "wtui", "flow/expanded")
	if repoColumn := visibleColumn(flowRow, "wtui"); repoColumn >= visibleColumn(phaseRow, "implementation:ready") {
		t.Fatalf("phase detail should render after the empty repo and branch cells, flow=%q phase=%q", flowRow, phaseRow)
	}
	if visibleColumn(phaseRow, "implementation:ready") <= visibleColumn(flowRow, "flow/expanded") {
		t.Fatalf("phase detail should stay aligned after the branch column, flow=%q phase=%q", flowRow, phaseRow)
	}
}

func TestRender_ActiveFlowsExpandedNoPhasesKeepsRepoColumnAlignment(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:    0,
		Width:       240,
		Height:      12,
		Mode:        ModeSessions,
		ActiveFlows: true,
		ActivePane:  1,
		Flows: []flowstore.FlowRecord{{
			FlowID:   "flow-1",
			Title:    "Expanded active repo flow",
			Status:   flowstore.StatusInProgress,
			RepoPath: "/dev/wtui",
			Branch:   "flow/empty-phases",
		}},
		FlowSelected:   0,
		ExpandedFlowID: "flow-1",
	})

	flowRow := lineContaining(view, "flow/empty-phases")
	noPhasesRow := lineContaining(view, "No phases")
	requireOrderedColumns(t, flowRow, flowstore.StatusInProgress, "wtui", "flow/empty-phases")
	if visibleColumn(noPhasesRow, "No phases") <= visibleColumn(flowRow, "flow/empty-phases") {
		t.Fatalf("no-phases detail should stay aligned after the active-flow branch column, flow=%q noPhases=%q", flowRow, noPhasesRow)
	}
}

func TestRender_FlowsModeMarksActiveTerminalFlowRows(t *testing.T) {
	flows := []flowstore.FlowRecord{
		{
			FlowID: "flow-1",
			Title:  "Active flow",
			Status: flowstore.StatusInProgress,
			Branch: "flow/active",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			},
		},
		{
			FlowID: "flow-2",
			Title:  "Inactive flow",
			Status: flowstore.StatusInProgress,
			Branch: "flow/inactive",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			},
		},
	}
	view := strings.Join(renderFlowPane(flows, 1, 0, 200, 5, "", "", []FlowTerminalActivity{
		{FlowID: "flow-1", PhaseID: "implementation"},
	}, false, nil), "\n")

	activeRow := ansi.Strip(lineContaining(view, "flow/active"))
	inactiveRow := ansi.Strip(lineContaining(view, "flow/inactive"))
	if !strings.HasPrefix(activeRow, " ● in_progress") {
		t.Fatalf("active flow row prefix = %q, want active marker before Status:\n%s", activeRow, view)
	}
	if !strings.HasPrefix(inactiveRow, ">  in_progress") {
		t.Fatalf("selected inactive flow row prefix = %q, want selection without marker:\n%s", inactiveRow, view)
	}
	if strings.Contains(inactiveRow, "●") {
		t.Fatalf("inactive flow row should not be marked:\n%s", view)
	}
	if visibleColumn(activeRow, "in_progress") != visibleColumn(inactiveRow, "in_progress") {
		t.Fatalf("status column shifted, active=%q inactive=%q", activeRow, inactiveRow)
	}

	view = strings.Join(renderFlowPane(flows, 0, 0, 200, 5, "", "", []FlowTerminalActivity{
		{FlowID: "flow-1", PhaseID: "implementation"},
	}, false, nil), "\n")
	selectedActiveRow := ansi.Strip(lineContaining(view, "flow/active"))
	if !strings.HasPrefix(selectedActiveRow, ">● in_progress") {
		t.Fatalf("selected active flow row prefix = %q, want selection and marker:\n%s", selectedActiveRow, view)
	}
}

func TestRender_ActiveFlowsSelectedActiveRowsPreserveSelectionAfterRepoColumn(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDarkBackground)
	})

	flows := []flowstore.FlowRecord{{
		FlowID:   "flow-1",
		Title:    "Active repo flow",
		Status:   flowstore.StatusInProgress,
		RepoPath: "/dev/wtui",
		Branch:   "flow/active-repo",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
		},
	}}
	activity := []FlowTerminalActivity{
		{FlowID: "flow-1", PhaseID: "implementation"},
	}

	view := strings.Join(renderFlowPane(flows, 0, 0, 240, 6, "", "", activity, true, nil), "\n")
	flowRow := rawLineContaining(view, "flow/active-repo")
	if flowRow == "" {
		t.Fatalf("active flow row missing:\n%s", view)
	}
	strippedFlowRow := ansi.Strip(flowRow)
	if !strings.HasPrefix(strippedFlowRow, ">● in_progress") {
		t.Fatalf("selected active flow row prefix = %q, want selection and marker:\n%s", strippedFlowRow, view)
	}
	requireOrderedColumns(t, strippedFlowRow, flowstore.StatusInProgress, "wtui", "flow/active-repo")
	if !strings.Contains(flowRow, selectedSegment(flowTerminalStyle, "●")) {
		t.Fatalf("selected active flow marker should keep semantic marker style:\n%q", flowRow)
	}
	if want := selectedStyle.Render(fitSessionColumn("wtui", flowRepoWidth)); !strings.Contains(flowRow, want) {
		t.Fatalf("selected active flow row should style repo column after marker:\n%q\nmissing %q", flowRow, want)
	}

	view = strings.Join(renderFlowPane(flows, 0, 0, 240, 6, "flow-1", "implementation", activity, true, nil), "\n")
	phaseRow := rawLineContaining(view, "Implementation")
	if phaseRow == "" {
		t.Fatalf("active phase row missing:\n%s", view)
	}
	strippedPhaseRow := ansi.Strip(phaseRow)
	if !strings.HasPrefix(strippedPhaseRow, "   >● running") {
		t.Fatalf("selected active phase row prefix = %q, want phase indent, selection, marker:\n%s", strippedPhaseRow, view)
	}
	if visibleColumn(strippedPhaseRow, "implementation:running") <= visibleColumn(strippedFlowRow, "flow/active-repo") {
		t.Fatalf("selected active phase should stay aligned after active-flow repo and branch columns, flow=%q phase=%q", strippedFlowRow, strippedPhaseRow)
	}
	if !strings.Contains(phaseRow, selectedSegment(flowTerminalStyle, "●")) {
		t.Fatalf("selected active phase marker should keep semantic marker style:\n%q", phaseRow)
	}
}

func visibleColumn(line, needle string) int {
	index := strings.Index(line, needle)
	if index < 0 {
		return -1
	}
	return lipgloss.Width(line[:index])
}

func requireOrderedColumns(t *testing.T, line string, columns ...string) {
	t.Helper()
	if line == "" {
		t.Fatalf("missing line for ordered columns %v", columns)
	}
	previous := -1
	for _, column := range columns {
		current := visibleColumn(line, column)
		if current < 0 {
			t.Fatalf("line missing column %q: %q", column, line)
		}
		if current <= previous {
			t.Fatalf("line columns out of order %v: %q", columns, line)
		}
		previous = current
	}
}

func lineWithPrefix(view, prefix string) string {
	for _, line := range strings.Split(view, "\n") {
		stripped := ansi.Strip(line)
		if strings.HasPrefix(stripped, prefix) {
			return stripped
		}
	}
	return ""
}

func rawLineContaining(view, needle string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(line), needle) {
			return line
		}
	}
	return ""
}

func TestRender_FlowsModeSelectedActiveRowsPreserveSelectionAfterMarker(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDarkBackground)
	})

	flows := []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Active flow",
		Status: flowstore.StatusInProgress,
		Branch: "flow/active",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
		},
	}}
	activity := []FlowTerminalActivity{
		{FlowID: "flow-1", PhaseID: "implementation"},
	}

	view := strings.Join(renderFlowPane(flows, 0, 0, 220, 6, "", "", activity, false, nil), "\n")

	flowRow := rawLineContaining(view, "flow/active")
	if flowRow == "" {
		t.Fatalf("active flow row missing:\n%s", view)
	}
	if !strings.Contains(flowRow, selectedSegment(flowTerminalStyle, "●")) {
		t.Fatalf("selected active flow marker should keep semantic marker style:\n%q", flowRow)
	}
	if want := selectedStyle.Render(fitSessionColumn(flowstore.StatusInProgress, flowStatusWidth)); !strings.Contains(flowRow, want) {
		t.Fatalf("selected active flow row should restore selection style after marker:\n%q\nmissing %q", flowRow, want)
	}

	view = strings.Join(renderFlowPane(flows, 0, 0, 220, 6, "flow-1", "implementation", activity, false, nil), "\n")
	phaseRow := rawLineContaining(view, "Implementation")
	if phaseRow == "" {
		t.Fatalf("active phase row missing:\n%s", view)
	}
	if !strings.Contains(phaseRow, selectedSegment(flowTerminalStyle, "●")) {
		t.Fatalf("selected active phase marker should keep semantic marker style:\n%q", phaseRow)
	}
	if want := selectedStyle.Render(fitSessionColumn(flowstore.PhaseRunning, flowStatusWidth)); !strings.Contains(phaseRow, want) {
		t.Fatalf("selected active phase row should restore selection style after marker:\n%q\nmissing %q", phaseRow, want)
	}
}

func TestRender_FlowsModeMarksActiveTerminalExpandedPhaseRows(t *testing.T) {
	flows := []flowstore.FlowRecord{
		{
			FlowID: "flow-1",
			Title:  "Active flow",
			Status: flowstore.StatusInProgress,
			Branch: "flow/active",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning, Order: 1},
				{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 2},
			},
		},
		{
			FlowID: "flow-2",
			Title:  "Other flow",
			Status: flowstore.StatusInProgress,
			Branch: "flow/other",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Same phase name", Status: flowstore.PhaseRunning, Order: 1},
			},
		},
	}
	view := strings.Join(renderFlowPane(flows, 0, 0, 220, 8, "flow-1", "implementation", []FlowTerminalActivity{
		{FlowID: "flow-1", PhaseID: "Implementation"},
		{FlowID: "flow-2", PhaseID: "review-loop"},
	}, false, nil), "\n")

	flowRow := ansi.Strip(lineContaining(view, "flow/active"))
	activePhaseRow := ansi.Strip(lineContaining(view, "Implementation"))
	inactivePhaseRow := ansi.Strip(lineContaining(view, "review-loop:ready"))
	if !strings.HasPrefix(activePhaseRow, "   >● running") {
		t.Fatalf("selected active phase row prefix = %q, want phase indent, selection, marker:\n%s", activePhaseRow, view)
	}
	if !strings.HasPrefix(inactivePhaseRow, "      ready") {
		t.Fatalf("inactive phase row prefix = %q, want phase indent without marker:\n%s", inactivePhaseRow, view)
	}
	if strings.Contains(inactivePhaseRow, "●") {
		t.Fatalf("phase row with non-matching activity should not be marked:\n%s", view)
	}
	if visibleColumn(activePhaseRow, "running") != visibleColumn(inactivePhaseRow, "ready") {
		t.Fatalf("phase status column shifted, active=%q inactive=%q", activePhaseRow, inactivePhaseRow)
	}
	if visibleColumn(activePhaseRow, "running") <= visibleColumn(flowRow, "in_progress") {
		t.Fatalf("phase row should remain indented from flow row, phase=%q flow=%q", activePhaseRow, flowRow)
	}
}

func TestRender_FlowsModeSplitPaneMarksActiveTerminalRows(t *testing.T) {
	flows := []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Active split flow",
		Status: flowstore.StatusInProgress,
		Branch: "flow/split-active",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
		},
	}}
	lines := renderFlowSplitPane(flows, 0, 0, 100, 9, "flow-1", "implementation", []FlowTerminalActivity{
		{FlowID: "flow-1", PhaseID: "implementation"},
	}, []EmbeddedTerminalTab{{
		Number:   1,
		Provider: "codex",
		Identity: "misleading-label",
		State:    "running",
		Active:   true,
	}}, []string{"terminal line"}, false, false, false, nil)
	view := strings.Join(lines, "\n")

	flowRow := ansi.Strip(lineContaining(view, "flow/split-active"))
	phaseRow := lineWithPrefix(view, "   >●")
	if !strings.HasPrefix(flowRow, " ● in_progress") {
		t.Fatalf("split active flow row prefix = %q, want active marker:\n%s", flowRow, view)
	}
	if !strings.HasPrefix(phaseRow, "   >● running") {
		t.Fatalf("split selected active phase row prefix = %q, want phase marker:\n%s", phaseRow, view)
	}
	if !strings.Contains(view, "misleading-label") || !strings.Contains(view, "terminal line") {
		t.Fatalf("split terminal content missing:\n%s", view)
	}
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if got := lipgloss.Width(line); got > 100 {
			t.Fatalf("split pane line width = %d, want <= 100: %q", got, line)
		}
	}
}

func TestStatusBar_FlowsModeShowsNewFlowHint(t *testing.T) {
	bar := RenderStatusBar(120, ModeFlows, OverlayNone, 1, false, false, false)
	if !strings.Contains(bar, "n: new flow") {
		t.Fatalf("expected new flow hint in flows mode, got %q", bar)
	}
}

func TestRender_FlowsModeShowsReasoningEffortShortcut(t *testing.T) {
	view := Render(RenderParams{
		Repos:               []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:            0,
		Width:               180,
		Height:              12,
		Mode:                ModeFlows,
		ActivePane:          1,
		FlowAgentLabel:      "codex",
		FlowReasoningEffort: "effort: high",
	})

	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "A      codex\nE      effort: high") {
		t.Fatalf("flows shortcut pane should group agent before reasoning effort:\n%s", pane)
	}
	if strings.Contains(pane, "A      set agent") {
		t.Fatalf("flows shortcut pane should not show generic set-agent label:\n%s", pane)
	}
	if strings.Contains(pane, "E      codex effort: high") {
		t.Fatalf("flows shortcut pane should not duplicate agent in effort label:\n%s", pane)
	}
}

func TestRender_FlowsModeShortcutSectionsUseFlowGroups(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   28,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Grouped shortcuts",
			Status:       flowstore.StatusInProgress,
			Branch:       "flow/grouped-shortcuts",
			WorktreePath: "/dev/wtui-worktrees/flow-grouped-shortcuts",
			PlanID:       "plan-1",
			AutoMode:     true,
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
			}},
		}},
		ActivePane:                 1,
		Destructive:                true,
		FlowSelected:               0,
		FlowHeadless:               true,
		FlowAutoModeSelected:       true,
		FlowNextLaunchReady:        true,
		FlowAgentLabel:             "codex",
		FlowReasoningEffort:        "effort: high",
		FlowPhaseResumableSelected: true,
	})

	pane := shortcutPaneText(view)
	if got, want := shortcutSectionTitles(pane), []string{"Actions", "Mode", "Agent", "Global"}; !slices.Equal(got, want) {
		t.Fatalf("Flow shortcut sections = %v, want %v:\n%s", got, want, pane)
	}
	for _, want := range []string{
		"n      new flow",
		"enter  phases",
		"g      launch next",
		"o      open",
		"y      copy path",
		"d      delete",
		"h      headless on",
		"m      auto: on",
		"A      codex",
		"E      effort: high",
		"⌫      pane",
		"f2/tab pane",
		"q/esc  quit",
		"f5     refresh",
	} {
		if !strings.Contains(pane, want) {
			t.Fatalf("Flow shortcut pane missing %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "Navigate") {
		t.Fatalf("Flow shortcut pane should not include Navigate section:\n%s", pane)
	}
}

func shortcutSectionTitles(pane string) []string {
	known := map[string]bool{
		"Actions":  true,
		"Mode":     true,
		"Agent":    true,
		"Global":   true,
		"Navigate": true,
		"Terminal": true,
		"Legend":   true,
	}
	var titles []string
	for _, line := range strings.Split(pane, "\n") {
		line = strings.TrimSpace(line)
		if known[line] {
			titles = append(titles, line)
		}
	}
	return titles
}

func TestRender_FlowsModeReasoningEffortShortcutHandlesSpecialLabels(t *testing.T) {
	tests := []struct {
		name      string
		agent     string
		effort    string
		want      string
		wantNoKey string
	}{
		{name: "codex app", agent: "codex-app", effort: "app default", want: "A      codex-app\nE      app default"},
		{name: "unset", agent: "choose agent", effort: "", want: "A      choose agent", wantNoKey: "E"},
		{name: "missing agent label", effort: "effort: high", want: "A      choose agent", wantNoKey: "E"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:               []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
				Selected:            0,
				Width:               180,
				Height:              12,
				Mode:                ModeFlows,
				ActivePane:          1,
				FlowAgentLabel:      tt.agent,
				FlowReasoningEffort: tt.effort,
			})
			pane := shortcutPaneText(view)
			if !strings.Contains(pane, tt.want) {
				t.Fatalf("flows shortcut pane should expose special labels %q:\n%s", tt.want, pane)
			}
			if tt.wantNoKey != "" && strings.Contains(pane, tt.wantNoKey+"      ") {
				t.Fatalf("flows shortcut pane should omit %s hint without configured agent:\n%s", tt.wantNoKey, pane)
			}
		})
	}
}

func TestStatusBar_FlowsModeShowsPhaseToggleHintForSelectedFlow(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:                    120,
		Mode:                     ModeFlows,
		ActivePane:               1,
		RepoSelected:             true,
		FlowSelected:             true,
		FlowWorktreePathSelected: true,
		FlowPlanLinked:           true,
		FlowHeadless:             true,
	})
	for _, want := range []string{"enter: phases", "h: headless on", "o: open", "y: copy path"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("expected selected flow hint %q, got %q", want, bar)
		}
	}
	for _, notWant := range []string{"x: phases", "a: launch phase", "i: embed phase"} {
		if strings.Contains(bar, notWant) {
			t.Fatalf("selected flow hint should not include %q, got %q", notWant, bar)
		}
	}
}

func TestStatusBar_FlowsModeFullFooterPreservesSectionOrder(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:                    240,
		Mode:                     ModeFlows,
		ActivePane:               1,
		RepoSelected:             true,
		FlowSelected:             true,
		FlowWorktreePathSelected: true,
		FlowPlanLinked:           true,
		FlowHeadless:             true,
		FlowAutoModeSelected:     true,
		FlowAgentLabel:           "codex",
		FlowReasoningEffort:      "effort: high",
		FlowNextLaunchReady:      true,
	})

	enterIndex := strings.Index(bar, "enter: phases")
	headlessIndex := strings.Index(bar, "h: headless on")
	agentIndex := strings.Index(bar, "A: codex")
	backspaceIndex := strings.Index(bar, "⌫ pane")
	tabIndex := strings.Index(bar, "f2/tab: pane")
	if enterIndex < 0 || headlessIndex < 0 || agentIndex < 0 || backspaceIndex < 0 || tabIndex < 0 {
		t.Fatalf("full Flow footer missing expected hints, got %q", bar)
	}
	if !(enterIndex < headlessIndex && headlessIndex < agentIndex && agentIndex < backspaceIndex && backspaceIndex < tabIndex) {
		t.Fatalf("full Flow footer should order Actions, Mode, Agent, Global, got %q", bar)
	}
}

func TestStatusBar_FlowsModeShowsAutoModeToggleForSelectedFlow(t *testing.T) {
	flowRow := renderStatusBarWithState(statusBarParams{
		Width:                180,
		Mode:                 ModeFlows,
		ActivePane:           1,
		RepoSelected:         true,
		FlowSelected:         true,
		FlowAutoModeSelected: false,
	})
	if !strings.Contains(flowRow, "m: auto: off") {
		t.Fatalf("selected Flow row should expose auto-mode off toggle, got %q", flowRow)
	}

	phaseRow := renderStatusBarWithState(statusBarParams{
		Width:                180,
		Mode:                 ModeFlows,
		ActivePane:           1,
		RepoSelected:         true,
		FlowSelected:         true,
		FlowPhaseSelected:    true,
		FlowAutoModeSelected: true,
	})
	if !strings.Contains(phaseRow, "m: auto: on") {
		t.Fatalf("selected Flow phase should expose auto-mode on toggle, got %q", phaseRow)
	}
}

func TestStatusBar_FlowsModeCompactFooterKeepsAutoModeToggle(t *testing.T) {
	flowRow := renderStatusBarWithState(statusBarParams{
		Width:                120,
		Mode:                 ModeFlows,
		ActivePane:           1,
		RepoSelected:         true,
		FlowSelected:         true,
		FlowAutoModeSelected: false,
	})
	if !strings.Contains(flowRow, "m: auto: off") {
		t.Fatalf("compact selected Flow row should keep auto-mode off toggle, got %q", flowRow)
	}

	phaseRow := renderStatusBarWithState(statusBarParams{
		Width:                120,
		Mode:                 ModeFlows,
		ActivePane:           1,
		RepoSelected:         true,
		FlowSelected:         true,
		FlowPhaseSelected:    true,
		FlowAutoModeSelected: true,
	})
	if !strings.Contains(phaseRow, "m: auto: on") {
		t.Fatalf("compact selected Flow phase should keep auto-mode on toggle, got %q", phaseRow)
	}
}

func TestStatusBar_FlowsModeHidesCopyPathWithoutWorktreePath(t *testing.T) {
	for _, tt := range []struct {
		name  string
		phase bool
	}{
		{name: "flow row"},
		{name: "phase row", phase: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bar := renderStatusBarWithState(statusBarParams{
				Width:             140,
				Mode:              ModeFlows,
				ActivePane:        1,
				RepoSelected:      true,
				FlowSelected:      true,
				FlowPhaseSelected: tt.phase,
			})
			if strings.Contains(bar, "y: copy") {
				t.Fatalf("Flow footer without worktree path should hide copy shortcut, got %q", bar)
			}
		})
	}
}

func TestStatusBar_FlowsModeShowsNextLaunchOnlyWhenFlowHasLaunchablePhase(t *testing.T) {
	base := statusBarParams{
		Width:        120,
		Mode:         ModeFlows,
		ActivePane:   1,
		RepoSelected: true,
		FlowSelected: true,
	}

	flowRow := renderStatusBarWithState(base)
	if strings.Contains(flowRow, "launch next") {
		t.Fatalf("Flow row should not expose launch action, got %q", flowRow)
	}

	base.FlowPhaseSelected = true
	gated := renderStatusBarWithState(base)
	if strings.Contains(gated, "launch next") {
		t.Fatalf("gated Flow phase should not expose launch action, got %q", gated)
	}

	base.FlowNextLaunchReady = true
	ready := renderStatusBarWithState(base)
	if !strings.Contains(ready, "g: launch next") {
		t.Fatalf("ready selected Flow phase should expose launch action, got %q", ready)
	}
	for _, notWant := range []string{"a: launch phase", "a: phase status", "i: embed phase"} {
		if strings.Contains(ready, notWant) {
			t.Fatalf("ready selected Flow phase should not include legacy hint %q, got %q", notWant, ready)
		}
	}
}

func TestRender_FlowsModeShowsResumeShortcutForResumableSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   14,
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

func TestRender_FlowsModeShowsCopyPathShortcutForSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   16,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Phase copy flow",
			Status:       flowstore.StatusInProgress,
			WorktreePath: "/dev/wtui-worktrees/phase-copy-flow",
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
	if !strings.Contains(pane, "y      copy path") {
		t.Fatalf("selected Flow phase should expose worktree-path copy shortcut:\n%s", view)
	}
	if strings.Contains(pane, "y      copy id") || strings.Contains(pane, "y      copy phase id") {
		t.Fatalf("selected Flow phase should not expose id copy shortcuts:\n%s", view)
	}
}

func TestRender_FlowsModeShowsResetShortcutForResettableSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   16,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Resettable flow",
			Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{
				PhaseID:   "implementation",
				Title:     "Implementation",
				Status:    flowstore.PhaseRunning,
				LaunchIDs: []string{"launch-orphan"},
			}},
		}},
		ActivePane:                  1,
		FlowSelected:                0,
		ExpandedFlowID:              "flow-1",
		SelectedFlowPhaseID:         "implementation",
		FlowPhaseResetReadySelected: true,
		FlowPhaseResumableSelected:  false,
	})

	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "x      reset ready") {
		t.Fatalf("resettable selected Flow phase should expose reset shortcut:\n%s", view)
	}
	if strings.Contains(pane, "x      phases") {
		t.Fatalf("selected Flow phase should not expose top-level phases shortcut:\n%s", view)
	}
}

func TestRender_FlowsModeShowsLaunchAndHeadlessShortcutForLaunchableSelectedPhase(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   16,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Launch phase flow",
			Status:       flowstore.StatusInProgress,
			WorktreePath: "/dev/wtui-worktrees/launch-phase-flow",
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
		FlowNextLaunchReady: true,
		FlowHeadless:        false,
	})

	pane := shortcutPaneText(view)
	for _, want := range []string{"enter  phases", "g      launch next", "h      headless off", "y      copy path"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("launchable selected Flow phase shortcut pane missing %q:\n%s", want, pane)
		}
	}
	for _, notWant := range []string{"enter  launch phase", "x      phases", "a      launch phase", "i      embed phase", "y      copy id"} {
		if strings.Contains(pane, notWant) {
			t.Fatalf("launchable selected Flow phase shortcut pane should not include %q:\n%s", notWant, pane)
		}
	}
}

func TestRender_FlowsModeShowsDestructiveModeAndDeleteShortcuts(t *testing.T) {
	base := RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "Delete flow",
			Status: flowstore.StatusPending,
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
			}},
		}},
		ActivePane:   1,
		FlowSelected: 0,
	}

	readOnlyPane := shortcutPaneText(Render(base))
	if !strings.Contains(readOnlyPane, "D      destructive mode") {
		t.Fatalf("flows view should expose destructive-mode toggle before delete is enabled:\n%s", readOnlyPane)
	}
	if strings.Contains(readOnlyPane, "d      delete") {
		t.Fatalf("read-only flows view should not expose delete shortcut:\n%s", readOnlyPane)
	}

	destructive := base
	destructive.Destructive = true
	destructivePane := shortcutPaneText(Render(destructive))
	if !strings.Contains(destructivePane, "d      delete") {
		t.Fatalf("destructive top-level Flow row should expose delete shortcut:\n%s", destructivePane)
	}
	if strings.Contains(destructivePane, "D      destructive mode") {
		t.Fatalf("destructive flows view should not show read-only toggle hint:\n%s", destructivePane)
	}

	phase := destructive
	phase.ExpandedFlowID = "flow-1"
	phase.SelectedFlowPhaseID = "implementation"
	if pane := shortcutPaneText(Render(phase)); strings.Contains(pane, "d      delete") {
		t.Fatalf("selected Flow phase should not expose delete shortcut:\n%s", pane)
	}

	stalePhase := destructive
	stalePhase.ExpandedFlowID = "flow-1"
	stalePhase.SelectedFlowPhaseID = "old-phase"
	if pane := shortcutPaneText(Render(stalePhase)); !strings.Contains(pane, "d      delete") {
		t.Fatalf("stale non-empty Flow phase selection should fall back to whole-flow delete shortcut:\n%s", pane)
	}

	terminalFocused := destructive
	terminalFocused.FlowEmbeddedTerminals = []EmbeddedTerminalTab{{Number: 1, Provider: "codex", Identity: "implementation", State: "running", Active: true}}
	terminalFocused.FlowTerminalFocused = true
	if pane := shortcutPaneText(Render(terminalFocused)); strings.Contains(pane, "d      delete") {
		t.Fatalf("focused Flow terminal should not expose delete shortcut:\n%s", pane)
	}
}

func TestStatusBar_FlowsModeNarrowFooterShowsEnterWithHeadlessHint(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:               80,
		Mode:                ModeFlows,
		ActivePane:          1,
		RepoSelected:        true,
		FlowSelected:        true,
		FlowHeadless:        true,
		FlowNextLaunchReady: true,
	})
	for _, want := range []string{"h: headless on", "enter: phases", "g: launch next"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("narrow Flow footer missing %q: %q", want, bar)
		}
	}
	for _, notWant := range []string{"x: phases", "a: launch phase", "i: embed phase"} {
		if strings.Contains(bar, notWant) {
			t.Fatalf("narrow Flow footer should not include %q: %q", notWant, bar)
		}
	}
}

func TestStatusBar_FlowsModeFooterGroupsAgentAndEffort(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:               180,
		Mode:                ModeFlows,
		ActivePane:          1,
		RepoSelected:        true,
		FlowAgentLabel:      "codex",
		FlowReasoningEffort: "effort: high",
	})
	agentIndex := strings.Index(bar, "A: codex")
	effortIndex := strings.Index(bar, "E: effort: high")
	if agentIndex < 0 || effortIndex < 0 || agentIndex > effortIndex {
		t.Fatalf("Flow footer should group agent before effort, got %q", bar)
	}
	if strings.Contains(bar, "A: set agent") || strings.Contains(bar, "E: codex effort: high") {
		t.Fatalf("Flow footer should not show generic or duplicated labels, got %q", bar)
	}
}

func TestStatusBar_FlowsModeFooterShowsAgentOutsideFlowPane(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:               180,
		Mode:                ModeFlows,
		ActivePane:          0,
		FlowAgentLabel:      "codex",
		FlowReasoningEffort: "effort: high",
	})
	agentIndex := strings.Index(bar, "A: codex")
	effortIndex := strings.Index(bar, "E: effort: high")
	if agentIndex < 0 || effortIndex < 0 || agentIndex > effortIndex {
		t.Fatalf("Flow footer should show grouped agent and effort outside Flow pane, got %q", bar)
	}
	if strings.Contains(bar, "A: set agent") {
		t.Fatalf("Flow footer should not fall back to generic agent hint, got %q", bar)
	}
}

func TestStatusBar_FlowsModeCompressedFooterDoesNotKeepEffortAfterDroppingAgent(t *testing.T) {
	for width := 70; width <= 150; width++ {
		bar := renderStatusBarWithState(statusBarParams{
			Width:               width,
			Mode:                ModeFlows,
			ActivePane:          1,
			RepoSelected:        true,
			FlowSelected:        true,
			FlowHeadless:        true,
			FlowAgentLabel:      "codex",
			FlowReasoningEffort: "effort: high",
		})
		if strings.Contains(bar, "E: effort: high") && !strings.Contains(bar, "A: codex") {
			t.Fatalf("compressed Flow footer width %d should not keep effort after dropping agent, got %q", width, bar)
		}
	}
}

func TestStatusBar_FlowsModeCompressedFooterKeepsAgentWhenOnlyEffortIsDropped(t *testing.T) {
	for width := 70; width <= 150; width++ {
		bar := renderStatusBarWithState(statusBarParams{
			Width:               width,
			Mode:                ModeFlows,
			ActivePane:          1,
			RepoSelected:        true,
			FlowSelected:        true,
			FlowHeadless:        true,
			FlowAgentLabel:      "codex",
			FlowReasoningEffort: "effort: high",
		})
		if strings.Contains(bar, "A: codex") && !strings.Contains(bar, "E: effort: high") {
			return
		}
	}
	t.Fatal("compressed Flow footer never kept the agent hint while dropping only effort")
}

func TestStatusBar_FlowsModeNarrowFooterPreservesDeleteSafetyHints(t *testing.T) {
	readOnly := renderStatusBarWithState(statusBarParams{
		Width:        80,
		Mode:         ModeFlows,
		ActivePane:   1,
		RepoSelected: true,
		FlowSelected: true,
	})
	if !strings.Contains(readOnly, "D: destructive mode") {
		t.Fatalf("narrow read-only Flow footer should keep destructive-mode state, got %q", readOnly)
	}
	if strings.Contains(readOnly, "d: delete") {
		t.Fatalf("narrow read-only Flow footer should not expose delete, got %q", readOnly)
	}

	destructive := renderStatusBarWithState(statusBarParams{
		Width:                 80,
		Mode:                  ModeFlows,
		ActivePane:            1,
		Destructive:           true,
		RepoSelected:          true,
		FlowSelected:          true,
		FlowDeletableSelected: true,
	})
	if !strings.Contains(destructive, "d: delete") {
		t.Fatalf("narrow destructive Flow footer should keep delete action, got %q", destructive)
	}
	if strings.Contains(destructive, "D: destructive mode") {
		t.Fatalf("narrow destructive Flow footer should not show read-only toggle state, got %q", destructive)
	}
}

func TestStatusBar_FlowsModeNarrowTerminalFooterKeepsPrefixHint(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:                  14,
		Mode:                   ModeFlows,
		ActivePane:             1,
		EmbeddedTerminalActive: true,
	})
	if !strings.Contains(bar, "ctrl+]") {
		t.Fatalf("narrow Flow terminal footer should keep prefix hint, got %q", bar)
	}
}

func TestRender_FlowsEmbeddedTerminalShortcutsAreActiveByDefault(t *testing.T) {
	pane := renderShortcutPane(statusBarParams{
		Mode:                   ModeFlows,
		ActivePane:             1,
		EmbeddedTerminalActive: true,
		EmbeddedTerminalPrefix: true,
	}, 34, 12)
	text := ansi.Strip(pane)
	for _, want := range []string{"ctrl+] send", "i      input", "left/right terminal", "d      detach", "x      close", "q/esc  quit", "1-9    switch"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Flow terminal shortcut pane missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "l          sessions") || strings.Contains(text, "ctrl+] commands") {
		t.Fatalf("Flow terminal shortcut pane should not show sessions or muted command hints:\n%s", text)
	}
}

func TestRender_ActiveFlowsOverSessionsUsesFlowTerminalPrefixShortcuts(t *testing.T) {
	pane := renderShortcutPane(statusBarParams{
		Mode:                   ModeSessions,
		ActiveFlows:            true,
		ActivePane:             1,
		EmbeddedTerminalActive: true,
		EmbeddedTerminalPrefix: true,
	}, 34, 12)
	text := ansi.Strip(pane)
	for _, want := range []string{"ctrl+] send", "i      input", "left/right terminal", "d      detach", "x      close", "q/esc  quit", "1-9    switch"} {
		if !strings.Contains(text, want) {
			t.Fatalf("active Flow terminal shortcut pane missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "l      sessions") {
		t.Fatalf("active Flow terminal shortcut pane should not show session prefix command:\n%s", text)
	}
}

func TestRender_ActiveFlowsIgnoreHiddenSessionTerminalForShortcuts(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected:    0,
		Width:       180,
		Height:      18,
		Mode:        ModeSessions,
		ActiveFlows: true,
		Flows: []flowstore.FlowRecord{{
			FlowID:       "flow-1",
			Title:        "Active Flow",
			Status:       flowstore.StatusInProgress,
			WorktreePath: "/dev/wtui-worktrees/active-flow",
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
				Order:   1,
			}},
		}},
		EmbeddedTerminals: []EmbeddedTerminalTab{{
			Number:   1,
			Provider: "codex",
			Identity: "hidden-session-terminal",
			State:    "running",
			Active:   true,
		}},
		ActivePane:          1,
		FlowSelected:        0,
		FlowNextLaunchReady: true,
		FlowHeadless:        true,
	})

	pane := shortcutPaneText(view)
	for _, want := range []string{"Actions", "g      launch next", "Mode", "h      headless on"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("active Flow shortcut pane missing %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "←/→") {
		t.Fatalf("active Flow shortcut pane should not advertise clamped arrow navigation:\n%s", pane)
	}
	if strings.Contains(pane, "ctrl+] commands") || strings.Contains(pane, "Terminal") {
		t.Fatalf("active Flow shortcut pane should ignore hidden session terminal:\n%s", pane)
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
			FlowID:       "flow-1",
			Title:        "Stale phase copy flow",
			Status:       flowstore.StatusInProgress,
			WorktreePath: "/dev/wtui-worktrees/stale-phase-copy-flow",
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
	if !strings.Contains(pane, "y      copy path") {
		t.Fatalf("stale Flow phase selection should fall back to worktree-path copy shortcut:\n%s", view)
	}
	if strings.Contains(pane, "y      copy id") || strings.Contains(pane, "y      copy phase id") {
		t.Fatalf("stale Flow phase selection should not expose id copy shortcuts:\n%s", view)
	}
}

func TestRender_FlowsModeHidesCopyPathShortcutWithoutWorktreePath(t *testing.T) {
	base := RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   16,
		Mode:     ModeFlows,
		Flows: []flowstore.FlowRecord{{
			FlowID: "flow-1",
			Title:  "No worktree path",
			Status: flowstore.StatusInProgress,
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation",
				Title:   "Implementation",
				Status:  flowstore.PhaseReady,
			}},
		}},
		ActivePane:   1,
		FlowSelected: 0,
	}

	if pane := shortcutPaneText(Render(base)); strings.Contains(pane, "y      copy") {
		t.Fatalf("selected Flow without worktree path should hide copy shortcut:\n%s", pane)
	}

	phase := base
	phase.ExpandedFlowID = "flow-1"
	phase.SelectedFlowPhaseID = "implementation"
	if pane := shortcutPaneText(Render(phase)); strings.Contains(pane, "y      copy") {
		t.Fatalf("selected Flow phase without parent worktree path should hide copy shortcut:\n%s", pane)
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
		ActivePane:   1,
		FlowSelected: 0,
		FlowHeadless: true,
	})

	for _, want := range []string{"plan-review", "changes_requested", "1/3", "enter", "phases", "headless on"} {
		if !strings.Contains(view, want) {
			t.Fatalf("flows gate view missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"headless off", "launch phase", "phase status", "embed phase", "x      phases", "a      launch"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("gated flows view should not advertise %q:\n%s", notWant, view)
		}
	}
}

func TestRender_FlowsModeShowsMergeMissingPRMetadata(t *testing.T) {
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
				{PhaseID: "review-loop-1", Title: "Review loop 1", Status: flowstore.PhaseCompleted},
				{PhaseID: "review-loop-2", Title: "Review loop 2", Status: flowstore.PhaseCompleted},
				{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseCompleted},
				{PhaseID: "merge", Title: "Merge", Status: flowstore.PhasePending},
			},
		}},
		ActivePane:   1,
		FlowSelected: 0,
	})

	for _, want := range []string{"merge:missing-pr", "missing", "Needs PR metadata"} {
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
			{PhaseID: "merge", Title: "Merge", Status: flowstore.PhasePending, Order: 3},
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

	for _, want := range []string{"merge:missing-pr", "plan:completed"} {
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
	if !strings.Contains(view, "merge:completed") || strings.Contains(view, "merge:missing-pr") {
		t.Fatalf("completed merge history should not be overwritten by missing PR recovery:\n%s", view)
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
