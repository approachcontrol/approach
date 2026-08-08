package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/scanner"
)

func TestRender_BeadsOpenRowsPreserveOrderAndOptionalAssignee(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      100,
		Height:     16,
		Mode:       ModeBeadsOpen,
		ActivePane: 1,
		BeadsOpen: []beadsquery.Bead{
			{ID: "bd-124", Priority: 2, Title: "Document cache"},
			{ID: "bd-123", Priority: 1, Title: "Fix cache", Assignee: "alice"},
		},
		BeadsOpenSelected:  1,
		BeadsOpenAvailable: true,
	}))

	unassigned := "   bd-124  P2  Document cache"
	assigned := " > bd-123  P1  Fix cache  alice"
	if !strings.Contains(view, unassigned) {
		t.Fatalf("render missing exact unassigned row %q:\n%s", unassigned, view)
	}
	if !strings.Contains(view, assigned) {
		t.Fatalf("render missing exact selected assigned row %q:\n%s", assigned, view)
	}
	if strings.Index(view, unassigned) > strings.Index(view, assigned) {
		t.Fatalf("renderer reordered Beads rows:\n%s", view)
	}
}

func TestRender_BeadsOpenAvailableEmptyShowsExactQuietMessage(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:              []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:           0,
		Width:              80,
		Height:             12,
		Mode:               ModeBeadsOpen,
		BeadsOpenAvailable: true,
	}))

	if !strings.Contains(view, "no open beads") {
		t.Fatalf("available empty Beads view missing exact message:\n%s", view)
	}
	if strings.Contains(view, "beads not configured") {
		t.Fatalf("available empty Beads view also showed unavailable message:\n%s", view)
	}
}

func TestRender_BeadsOpenUnavailableShowsExactBlanketMessage(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   12,
		Mode:     ModeBeadsOpen,
	}))

	if !strings.Contains(view, "beads not configured") {
		t.Fatalf("unavailable Beads view missing exact message:\n%s", view)
	}
	for _, unwanted := range []string{"no open beads", "Could not load", "failed"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("unavailable Beads view also showed %q:\n%s", unwanted, view)
		}
	}
}

func TestModeHeader_BeadsOpenShowsActiveTopLevelEntryWithoutSubviewRow(t *testing.T) {
	lines := strings.Split(renderModeHeader(ModeBeadsOpen, 120), "\n")
	if len(lines) != 2 {
		t.Fatalf("Beads header lines = %d, want top-level row and separator", len(lines))
	}
	top := ansi.Strip(lines[0])
	for _, want := range []string{"1 git", "2 sessions", "3 plans", "4 flows", "[5] beads", "^a active flows"} {
		if !strings.Contains(top, want) {
			t.Fatalf("Beads header missing %q: %q", want, top)
		}
	}
	for _, unwanted := range []string{"w worktrees", "o open"} {
		if strings.Contains(top, unwanted) {
			t.Fatalf("Beads tracer header exposed deferred subview %q: %q", unwanted, top)
		}
	}
}

func TestRenderBeadsOpenPaneTruncatesLongRowsToWidth(t *testing.T) {
	const width = 24
	lines := renderBeadsOpenPane([]beadsquery.Bead{{
		ID:       "bd-123",
		Priority: 1,
		Title:    "A very long cache title",
		Assignee: "a-very-long-assignee",
	}}, 0, 0, width, 1)

	if len(lines) != 1 {
		t.Fatalf("rendered lines = %d, want 1", len(lines))
	}
	if got := ansi.StringWidth(lines[0]); got > width {
		t.Fatalf("rendered row width = %d, want <= %d: %q", got, width, ansi.Strip(lines[0]))
	}
	if strings.Contains(ansi.Strip(lines[0]), "a-very-long-assignee") {
		t.Fatalf("long assignee was not truncated: %q", ansi.Strip(lines[0]))
	}
}

func TestRender_BeadsOpenShortcutsDoNotAdvertiseDeferredArrowNavigation(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      140,
		Height:     20,
		Mode:       ModeBeadsOpen,
		ActivePane: 1,
	})
	pane := ansi.Strip(shortcutPaneText(view))
	if !strings.Contains(pane, "Beads Open") {
		t.Fatalf("shortcut pane missing Beads Open title:\n%s", pane)
	}
	if strings.Contains(pane, "←/→") || strings.Contains(pane, "view") {
		t.Fatalf("shortcut pane advertised deferred Beads arrow navigation:\n%s", pane)
	}
}
