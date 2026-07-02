package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
)

func TestModeHeader_GitActiveShowsGroupedTwoRows(t *testing.T) {
	header := renderModeHeader(ModeStashes, 120)
	lines := strings.Split(header, "\n")
	if len(lines) != 3 {
		t.Fatalf("git-active header lines = %d, want top-level row, subview row, separator", len(lines))
	}

	top := ansi.Strip(lines[0])
	for _, want := range []string{"[1] git", "2 sessions", "3 plans", "4 flows", "^a active flows"} {
		if !strings.Contains(top, want) {
			t.Errorf("top-level row missing %q: %q", want, top)
		}
	}
	if strings.Contains(top, "5 active flows") {
		t.Fatalf("top-level row should not label active flows with 5: %q", top)
	}
	if !strings.HasSuffix(top, "^a active flows") || ansi.StringWidth(top) != 120 {
		t.Fatalf("active flows item should be right-pinned at width 120: %q width=%d", top, ansi.StringWidth(top))
	}

	sub := ansi.Strip(lines[1])
	for _, want := range []string{"w worktrees", "b branches", "[s] stashes", "h history", "r reflog"} {
		if !strings.Contains(sub, want) {
			t.Errorf("subview row missing %q: %q", want, sub)
		}
	}

	if !strings.Contains(lines[2], "─") {
		t.Errorf("expected separator line, got %q", lines[2])
	}
}

func TestModeHeader_NonGitShowsSingleTopLevelRow(t *testing.T) {
	header := renderModeHeader(ModeSessions, 120)
	lines := strings.Split(header, "\n")
	if len(lines) != 2 {
		t.Fatalf("non-git header lines = %d, want top-level row and separator", len(lines))
	}

	top := ansi.Strip(lines[0])
	for _, want := range []string{"1 git", "[2] sessions", "3 plans", "4 flows", "^a active flows"} {
		if !strings.Contains(top, want) {
			t.Errorf("top-level row missing %q: %q", want, top)
		}
	}
	if strings.Contains(top, "5 active flows") {
		t.Fatalf("top-level row should not label active flows with 5: %q", top)
	}
	if !strings.HasSuffix(top, "^a active flows") || ansi.StringWidth(top) != 120 {
		t.Fatalf("active flows item should be right-pinned at width 120: %q width=%d", top, ansi.StringWidth(top))
	}
	if strings.Contains(top, "worktrees") {
		t.Errorf("non-git header should not list git subviews: %q", top)
	}
}

func TestModeHeader_ActiveFlowsRightItemShowsActiveCtrlA(t *testing.T) {
	top := ansi.Strip(strings.Split(renderModeHeader(ModeActiveFlows, 80), "\n")[0])
	if !strings.HasSuffix(top, "[^a] active flows") {
		t.Fatalf("active flows item should be active and right-pinned: %q", top)
	}
	for _, notWant := range []string{"[1] git", "[2] sessions", "[3] plans", "[4] flows", "5 active flows"} {
		if strings.Contains(top, notWant) {
			t.Fatalf("active flows header should not contain %q: %q", notWant, top)
		}
	}
}

func TestModeHeader_RightItemSurvivesConstrainedWidth(t *testing.T) {
	top := ansi.Strip(strings.Split(renderModeHeader(ModePlans, 42), "\n")[0])
	if !strings.Contains(top, "^a active flows") {
		t.Fatalf("constrained top-level row should keep active flows visible: %q", top)
	}
}

func TestRender_GitHeaderDoesNotGrowPane(t *testing.T) {
	params := RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   14,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
	}

	params.Mode = ModeSessions
	sessionsLines := len(strings.Split(Render(params), "\n"))

	params.Mode = ModeWorktrees
	gitLines := len(strings.Split(Render(params), "\n"))

	if gitLines != sessionsLines {
		t.Fatalf("git render = %d lines, sessions render = %d lines; the subview row must come out of the list height", gitLines, sessionsLines)
	}
}

func TestRender_ShortcutRailListsGitSubviewKeys(t *testing.T) {
	params := RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      140,
		Height:     20,
		Mode:       ModeHistory,
		ActivePane: 1,
	}
	view := Render(params)
	if !strings.Contains(view, "w/b/s/h/r") || !strings.Contains(view, "subview") {
		t.Fatalf("git-active shortcut rail should list w/b/s/h/r subview hint:\n%s", view)
	}

	params.Mode = ModeSessions
	view = Render(params)
	if strings.Contains(view, "w/b/s/h/r") {
		t.Fatalf("non-git shortcut rail should not list the subview hint:\n%s", view)
	}
}
