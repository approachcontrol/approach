package ui

import (
	"fmt"
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

func TestRender_BeadsErrorsTakePrecedenceOverRowsAndQuietStates(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			view := ansi.Strip(Render(RenderParams{
				Repos:              []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:           0,
				Width:              150,
				Height:             12,
				Mode:               tt.mode,
				BeadsOpen:          []beadsquery.Bead{{ID: "bd-retained", Title: "Retained"}},
				BeadsOpenAvailable: true,
				BeadsError:         "listing query: invalid character 'x' in JSON",
			}))

			want := "Could not load " + tt.name + " beads: listing query: invalid character 'x' in JSON"
			if !strings.Contains(view, want) {
				t.Fatalf("error view missing %q:\n%s", want, view)
			}
			for _, unwanted := range []string{"bd-retained", "no " + tt.name + " beads", "beads not configured"} {
				if strings.Contains(view, unwanted) {
					t.Fatalf("error view also rendered %q:\n%s", unwanted, view)
				}
			}
		})
	}
}

func TestRender_BeadsPendingTakesPrecedenceOverError(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:              []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:           0,
		Width:              100,
		Height:             12,
		Mode:               ModeBeadsOpen,
		BeadsOpen:          []beadsquery.Bead{{ID: "bd-retained", Title: "Retained"}},
		BeadsOpenAvailable: true,
		BeadsOpenPending:   true,
		BeadsError:         "old failure",
	}))

	if !strings.Contains(view, "loading open beads") {
		t.Fatalf("pending/error view missing loading state:\n%s", view)
	}
	for _, unwanted := range []string{"Could not load", "old failure", "bd-retained", "no open beads", "beads not configured"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("pending/error view also rendered %q:\n%s", unwanted, view)
		}
	}
}

func TestRender_BeadsCommandErrorShowsUsefulDetail(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      150,
		Height:     12,
		Mode:       ModeBeadsOpen,
		BeadsError: "listing open beads: Error: corrupt database: exit status 1",
	}))
	want := "Could not load open beads: listing open beads: Error: corrupt database: exit status 1"
	if !strings.Contains(view, want) {
		t.Fatalf("command error view missing %q:\n%s", want, view)
	}
}

func TestRender_BeadsErrorSanitizesControlsAndBoundsOneDetailLine(t *testing.T) {
	const width = 82
	hostile := "\x1b[31mbad JSON\x1b[0m\x1b]52;c;dGVzdA==\a\nsecond\rthird\x00\x01 " + strings.Repeat("overlong-", 30)
	raw := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      width,
		Height:     12,
		Mode:       ModeBeadsOpen,
		BeadsError: hostile,
	})
	view := ansi.Strip(raw)

	detailLines := 0
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > width {
			t.Fatalf("rendered line width = %d, want <= %d: %q", ansi.StringWidth(line), width, line)
		}
		if strings.Contains(line, "Could not load open beads:") {
			detailLines++
			if !strings.Contains(line, "bad JSON second third") {
				t.Fatalf("error line lost sanitized detail: %q", line)
			}
		}
	}
	if detailLines != 1 {
		t.Fatalf("error detail lines = %d, want exactly one:\n%s", detailLines, view)
	}
	for _, r := range view {
		if r != '\n' && (r < ' ' || r == '\u007f') {
			t.Fatalf("rendered view retained control rune %U: %q", r, view)
		}
	}
	for _, unwanted := range []string{"[31m", "]52;", "beads not configured", "no open beads", "overlong-overlong-overlong"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("sanitized error view retained %q:\n%s", unwanted, view)
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
	lines := strings.Split(renderModeHeaderWithBeads(ModeBeadsClosed, 32, 100, 1432, true, false), "\n")
	if got := ansi.Strip(lines[1]); !strings.Contains(got, "[c] closed 100 of 1432") {
		t.Fatalf("constrained Beads subview row lost active item: %q", got)
	}
}

func TestRender_BeadsClosedHeaderShowsAcceptedSourceCountAndTruncation(t *testing.T) {
	tests := []struct {
		name    string
		fetched int
		total   int
		want    string
	}{
		{name: "truncated", fetched: 100, total: 1432, want: "[c] closed 100 of 1432"},
		{name: "exact cap", fetched: 100, total: 100, want: "[c] closed 100"},
		{name: "under cap", fetched: 42, total: 42, want: "[c] closed 42"},
		{name: "zero", fetched: 0, total: 0, want: "[c] closed 0"},
		{name: "count race below fetched", fetched: 100, total: 99, want: "[c] closed 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := ansi.Strip(Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 180, Height: 12, Mode: ModeBeadsClosed,
				BeadsOpenAvailable: true, BeadsSourceCount: tt.fetched, BeadsClosedTotal: tt.total,
			}))
			if !strings.Contains(view, tt.want) {
				t.Fatalf("Closed header missing %q:\n%s", tt.want, view)
			}
			if tt.total <= tt.fetched && strings.Contains(view, fmt.Sprintf("%d of %d", tt.fetched, tt.total)) {
				t.Fatalf("Closed header showed a false truncation marker:\n%s", view)
			}
		})
	}
}

func TestRender_BeadsClosedHeaderSuppressesCountWhileLoadingOrUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name      string
		available bool
		pending   bool
	}{
		{name: "loading", pending: true},
		{name: "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view := ansi.Strip(Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 180, Height: 12, Mode: ModeBeadsClosed,
				BeadsOpenAvailable: tt.available, BeadsOpenPending: tt.pending,
				BeadsSourceCount: 100, BeadsClosedTotal: 1432,
			}))
			if !strings.Contains(view, "[c] closed") || strings.Contains(view, "closed 100") {
				t.Fatalf("%s Closed header exposed a count:\n%s", tt.name, view)
			}
		})
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

func TestRender_BeadsReadyFlowCreateShortcutUsesAvailabilitySnapshot(t *testing.T) {
	base := RenderParams{
		Repos:                        []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:                     0,
		Height:                       20,
		Mode:                         ModeBeadsReady,
		ActivePane:                   1,
		BeadsOpen:                    []beadsquery.Bead{{ID: "bd-1", Title: "One"}},
		ReadyBeadFlowCreateAvailable: true,
	}

	base.Width = 80
	footerView := ansi.Strip(Render(base))
	if !strings.Contains(footerView, "f: new flow") {
		t.Fatalf("Ready footer missing executable Flow shortcut:\n%s", footerView)
	}

	base.Width = 140
	pane := ansi.Strip(shortcutPaneText(Render(base)))
	if !strings.Contains(pane, "f      new flow") {
		t.Fatalf("Ready shortcut pane missing executable Flow shortcut:\n%s", pane)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*RenderParams)
	}{
		{name: "left focused", mutate: func(p *RenderParams) { p.ActivePane = 0 }},
		{name: "loading", mutate: func(p *RenderParams) { p.BeadsOpenPending = true }},
		{name: "unavailable", mutate: func(p *RenderParams) { p.BeadsOpenAvailable = false }},
		{name: "empty", mutate: func(p *RenderParams) { p.BeadsOpen = nil }},
		{name: "filtered empty", mutate: func(p *RenderParams) { p.BeadsOpen = nil; p.ItemSearch = "no-match" }},
		{name: "non ready", mutate: func(p *RenderParams) { p.Mode = ModeBeadsBlocked }},
		{name: "creating"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			params := base
			params.Width = 140
			params.ReadyBeadFlowCreateAvailable = false
			if tt.mutate != nil {
				tt.mutate(&params)
			}
			view := ansi.Strip(Render(params))
			if strings.Contains(view, "new flow") {
				t.Fatalf("unavailable Ready action advertised:\n%s", view)
			}
		})
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
		Width: 90, Height: BeadsContentOverhead + TerminalChipRows + 2, Mode: ModeBeadsOpen, ActivePane: 1,
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
	if got := len(strings.Split(view, "\n")); got != BeadsContentOverhead+TerminalChipRows+2 {
		t.Fatalf("rendered height = %d, want unchanged outer height %d", got, BeadsContentOverhead+TerminalChipRows+2)
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
