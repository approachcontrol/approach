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

func TestRender_BeadsOpenPendingShowsNeutralLoadingMessage(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:              []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:           0,
		Width:              80,
		Height:             12,
		Mode:               ModeBeadsOpen,
		BeadsOpen:          []beadsquery.Bead{{ID: "bd-old", Priority: 1, Title: "Old result"}},
		BeadsOpenAvailable: true,
		BeadsOpenPending:   true,
	}))

	if !strings.Contains(view, "loading open beads") {
		t.Fatalf("pending Beads view missing neutral loading message:\n%s", view)
	}
	for _, unwanted := range []string{"bd-old", "no open beads", "beads not configured"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("pending Beads view also showed %q:\n%s", unwanted, view)
		}
	}
}

func TestModeHeader_BeadsShowsGroupedFiveSubviewRow(t *testing.T) {
	lines := strings.Split(renderModeHeader(ModeBeadsOpen, 120), "\n")
	if len(lines) != 3 {
		t.Fatalf("Beads header lines = %d, want top-level row, subview row, and separator", len(lines))
	}
	top := ansi.Strip(lines[0])
	for _, want := range []string{"1 git", "2 sessions", "3 plans", "4 flows", "[5] beads", "^a active flows"} {
		if !strings.Contains(top, want) {
			t.Fatalf("Beads header missing %q: %q", want, top)
		}
	}
	subviews := ansi.Strip(lines[1])
	wants := []string{"r ready", "b blocked", "[o] open", "i in-progress", "c closed"}
	last := -1
	for _, want := range wants {
		index := strings.Index(subviews, want)
		if index < 0 {
			t.Fatalf("Beads subview header missing %q: %q", want, subviews)
		}
		if index <= last {
			t.Fatalf("Beads subview %q out of order: %q", want, subviews)
		}
		last = index
	}
}

func TestModeHeader_BeadsStylesEachActiveSubview(t *testing.T) {
	for _, tt := range []struct {
		mode Mode
		want string
	}{
		{mode: ModeBeadsReady, want: "[r] ready"},
		{mode: ModeBeadsBlocked, want: "[b] blocked"},
		{mode: ModeBeadsOpen, want: "[o] open"},
		{mode: ModeBeadsInProgress, want: "[i] in-progress"},
		{mode: ModeBeadsClosed, want: "[c] closed"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			lines := strings.Split(renderModeHeader(tt.mode, 120), "\n")
			if got := ansi.Strip(lines[1]); !strings.Contains(got, tt.want) {
				t.Fatalf("header = %q, want active %q", got, tt.want)
			}
			if top := ansi.Strip(lines[0]); !strings.Contains(top, "[5] beads") {
				t.Fatalf("top-level header = %q, want active Beads group", top)
			}
		})
	}
}

func TestModeHeader_BeadsOpenKeepsActiveItemAtNarrowWidth(t *testing.T) {
	top := ansi.Strip(strings.Split(renderModeHeader(ModeBeadsOpen, 48), "\n")[0])
	for _, want := range []string{"[5] beads", "^a active flows"} {
		if !strings.Contains(top, want) {
			t.Fatalf("narrow Beads header missing %q: %q", want, top)
		}
	}
}

func TestModeHeader_BeadsKeepsActiveSubviewAtConstrainedWidth(t *testing.T) {
	lines := strings.Split(renderModeHeader(ModeBeadsClosed, 32), "\n")
	if got := ansi.Strip(lines[1]); !strings.Contains(got, "[c] closed") {
		t.Fatalf("constrained Beads subview row lost active item: %q", got)
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

func TestRenderBeadsOpenPaneSanitizesTrackerText(t *testing.T) {
	lines := renderBeadsOpenPane([]beadsquery.Bead{{
		ID:       "bd-\x1b[31m1",
		Priority: 1,
		Title:    "first\nsecond\x1b]52;c;dGVzdA==\a",
		Assignee: "alice\r\nbob\x00",
	}}, -1, 0, 80, 1)

	if len(lines) != 1 {
		t.Fatalf("rendered lines = %d, want 1", len(lines))
	}
	if got, want := lines[0], "   bd-1  P1  first second  alice bob"; got != want {
		t.Fatalf("rendered row = %q, want %q", got, want)
	}
}

func TestRender_BeadsOpenShortcutsAdvertiseArrowAndSubviewNavigation(t *testing.T) {
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
	if strings.Count(pane, "←/→") != 1 || !strings.Contains(pane, "view") {
		t.Fatalf("shortcut pane did not advertise exactly one Beads arrow hint:\n%s", pane)
	}
	if strings.Count(pane, "r/b/o/i/c") != 1 || !strings.Contains(pane, "subview") {
		t.Fatalf("shortcut pane did not advertise exactly one Beads subview hint:\n%s", pane)
	}
}

func TestRender_BeadsQuietStatesAreSubviewSpecific(t *testing.T) {
	for _, tt := range []struct {
		mode Mode
		name string
	}{
		{mode: ModeBeadsReady, name: "ready"},
		{mode: ModeBeadsBlocked, name: "blocked"},
		{mode: ModeBeadsOpen, name: "open"},
		{mode: ModeBeadsInProgress, name: "in-progress"},
		{mode: ModeBeadsClosed, name: "closed"},
	} {
		t.Run(tt.name+"/empty", func(t *testing.T) {
			view := ansi.Strip(Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 90, Height: 12, Mode: tt.mode, BeadsOpenAvailable: true,
			}))
			if !strings.Contains(view, "no "+tt.name+" beads") {
				t.Fatalf("empty view missing subview message:\n%s", view)
			}
		})
		t.Run(tt.name+"/loading", func(t *testing.T) {
			view := ansi.Strip(Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 90, Height: 12, Mode: tt.mode, BeadsOpenPending: true,
			}))
			if !strings.Contains(view, "loading "+tt.name+" beads") || strings.Contains(view, "beads not configured") {
				t.Fatalf("loading view has wrong quiet state:\n%s", view)
			}
		})
		t.Run(tt.name+"/unavailable", func(t *testing.T) {
			view := ansi.Strip(Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 90, Height: 12, Mode: tt.mode,
			}))
			if !strings.Contains(view, "beads not configured") {
				t.Fatalf("unavailable view missing blanket message:\n%s", view)
			}
		})
	}
}

func TestRender_BeadsGroupedHeaderConsumesOneListRow(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
		Width: 90, Height: BeadsContentOverhead + 2, Mode: ModeBeadsOpen, ActivePane: 1,
		BeadsOpen: []beadsquery.Bead{
			{ID: "bd-0", Title: "Zero"},
			{ID: "bd-1", Title: "One"},
			{ID: "bd-2", Title: "Two"},
		},
		BeadsOpenAvailable: true,
	}))
	if !strings.Contains(view, "bd-0") || !strings.Contains(view, "bd-1") {
		t.Fatalf("height-2 Beads viewport omitted visible rows:\n%s", view)
	}
	if strings.Contains(view, "bd-2") {
		t.Fatalf("Beads second header failed to reduce list capacity to two rows:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != BeadsContentOverhead+2 {
		t.Fatalf("rendered height = %d, want unchanged outer height %d", got, BeadsContentOverhead+2)
	}
}

func TestRender_AllBeadsShortcutNavigationRespectsPaneFocus(t *testing.T) {
	for _, tt := range []struct {
		mode  Mode
		title string
	}{
		{ModeBeadsReady, "Beads Ready"},
		{ModeBeadsBlocked, "Beads Blocked"},
		{ModeBeadsOpen, "Beads Open"},
		{ModeBeadsInProgress, "Beads In-progr"},
		{ModeBeadsClosed, "Beads Closed"},
	} {
		t.Run(tt.title+"/right", func(t *testing.T) {
			view := Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 140, Height: 20, Mode: tt.mode, ActivePane: 1,
			})
			pane := ansi.Strip(shortcutPaneText(view))
			if !strings.Contains(pane, tt.title) || strings.Count(pane, "r/b/o/i/c") != 1 || !strings.Contains(pane, "subview") || strings.Count(pane, "←/→") != 1 || !strings.Contains(pane, "view") {
				t.Fatalf("right-focused shortcut pane for %v has wrong navigation:\n%s", tt.mode, pane)
			}
		})
		t.Run(tt.title+"/left", func(t *testing.T) {
			view := Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 140, Height: 20, Mode: tt.mode, ActivePane: 0,
			})
			pane := ansi.Strip(shortcutPaneText(view))
			if strings.Contains(pane, "r/b/o/i/c") || strings.Contains(pane, "←/→") {
				t.Fatalf("left-focused shortcut pane for %v advertised right-pane navigation:\n%s", tt.mode, pane)
			}
		})
	}
}
