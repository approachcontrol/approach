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
		ActivePane: PaneTop,
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

func TestRenderBeadsPaneMarksEpicsAndExpandsSelectedEpicReadyFirst(t *testing.T) {
	t.Parallel()

	beads := []beadsquery.Bead{
		{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: " ePiC "},
		{ID: "bd-task", Priority: 2, Title: "Ordinary", IssueType: "task"},
	}
	expansion := BeadExpansion{
		EpicID:         "bd-epic",
		State:          BeadExpansionLoaded,
		Children:       []beadsquery.Bead{{ID: "bd-child-2", Priority: 1, Title: "Ready"}, {ID: "bd-child-1", Priority: 2, Title: "Neutral"}},
		ReadyIDs:       map[string]bool{"bd-child-2": true},
		ReadinessKnown: true,
	}
	lines := renderBeadsPane(beads, 0, 0, 80, 5, expansion)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"bd-epic  P1  Parent  [epic]", "bd-child-2  P1  Ready  [ready]", "bd-child-1  P2  Neutral", "bd-task  P2  Ordinary"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("render missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Ordinary  [epic]") || strings.Contains(plain, "Neutral  [ready]") {
		t.Fatalf("render invented epic/readiness markers:\n%s", plain)
	}
	if got := BeadVisualHeight(beads[0], expansion); got != 3 {
		t.Fatalf("BeadVisualHeight(epic) = %d, want 3", got)
	}
}

func TestRenderBeadRowPreservesEpicMarkerAtNarrowWidths(t *testing.T) {
	t.Parallel()

	const width = 24
	epic := beadsquery.Bead{
		ID: "bd-epic", Priority: 1, Title: "A very long epic title",
		Assignee: "a-very-long-assignee", IssueType: "epic",
	}
	for _, selected := range []bool{false, true} {
		line := renderBeadRow(epic, selected, width)
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "[epic]") {
			t.Fatalf("selected=%t narrow row lost epic marker: %q", selected, plain)
		}
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("selected=%t row width = %d, want <= %d: %q", selected, got, width, plain)
		}
	}
}

func TestRenderBeadRowPreservesEpicAndAutoMarkersAtMinimumWidth(t *testing.T) {
	t.Parallel()

	const width = 19 // selection prefix plus "  [epic]  [auto]"
	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "A very long epic title", IssueType: "epic"}
	line := renderBeadRow(epic, true, width, true)
	plain := ansi.Strip(line)
	if !strings.Contains(plain, "[epic]  [auto]") {
		t.Fatalf("minimum-width row lost persistent markers: %q", plain)
	}
	if got := ansi.StringWidth(line); got != width {
		t.Fatalf("row width = %d, want %d: %q", got, width, plain)
	}
}

func TestRenderBeadsPaneShowsProgressionFailureWithoutHidingChildren(t *testing.T) {
	t.Parallel()

	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: "epic"}
	expansion := BeadExpansion{
		EpicID: epic.ID, State: BeadExpansionLoaded,
		Children:          []beadsquery.Bead{{ID: "bd-child", Priority: 2, Title: "Child"}},
		ReadinessKnown:    true,
		ProgressionDetail: "database failed\n\x1b]8;;https://example.invalid\aunsafe",
	}
	lines := expansionVisualLines(epic, true, 60, expansion)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if len(lines) != 3 || !strings.Contains(plain, "bd-child") || !strings.Contains(plain, "Auto-progression unavailable: database failed unsafe") {
		t.Fatalf("progression warning/children render = %d lines:\n%s", len(lines), plain)
	}
	if got := BeadVisualHeight(epic, expansion); got != len(lines) {
		t.Fatalf("BeadVisualHeight() = %d, rendered lines = %d", got, len(lines))
	}
}

func TestRenderBeadsPanePreservesReadyMarkerAtNarrowWidths(t *testing.T) {
	t.Parallel()

	const width = 48
	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: "epic"}
	child := beadsquery.Bead{
		ID: "bd-child-with-a-long-identifier", Priority: 1,
		Title: "A child title that also needs truncation",
	}
	expansion := BeadExpansion{
		EpicID: epic.ID, State: BeadExpansionLoaded,
		Children: []beadsquery.Bead{child}, ReadyIDs: map[string]bool{child.ID: true},
		ReadinessKnown: true,
	}
	lines := expansionVisualLines(epic, true, width, expansion)
	if len(lines) != 2 {
		t.Fatalf("rendered lines = %d, want parent and child", len(lines))
	}
	plain := ansi.Strip(lines[1])
	if !strings.Contains(plain, "[ready]") {
		t.Fatalf("narrow child row lost ready marker: %q", plain)
	}
	if got := ansi.StringWidth(lines[1]); got > width {
		t.Fatalf("child row width = %d, want <= %d: %q", got, width, plain)
	}
}

func TestRenderBeadsPaneStylesSelectedExpansionLines(t *testing.T) {
	t.Parallel()

	const width = 60
	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: "epic"}
	expansion := BeadExpansion{
		EpicID: epic.ID, State: BeadExpansionLoaded,
		Children: []beadsquery.Bead{{ID: "bd-child", Priority: 1, Title: "Child"}},
		Detail:   "bd ready failed",
	}
	selectedLines := expansionVisualLines(epic, true, width, expansion)
	plainLines := []string{
		"     ↳ bd-child  P1  Child",
		"     Readiness unavailable: bd ready failed",
	}
	if len(selectedLines) != 3 {
		t.Fatalf("selected expansion lines = %d, want parent, child, and warning", len(selectedLines))
	}
	for i, plain := range plainLines {
		want := renderStyledRow(stashSelStyle.Render(plain), stashSelStyle, width)
		if got := selectedLines[i+1]; got != want {
			t.Fatalf("selected expansion line %d = %q, want selection-styled %q", i, got, want)
		}
	}

	unfocusedLines := expansionVisualLines(epic, false, width, expansion)
	for i, want := range plainLines {
		if got := unfocusedLines[i+1]; got != want {
			t.Fatalf("unfocused expansion line %d = %q, want plain %q", i, got, want)
		}
	}
}

func TestRenderBeadsPaneEmptyChildrenStillShowsReadinessFailure(t *testing.T) {
	t.Parallel()

	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: "epic"}
	expansion := BeadExpansion{
		EpicID: epic.ID, State: BeadExpansionLoaded,
		Detail: "bd ready failed",
	}
	lines := expansionVisualLines(epic, true, 60, expansion)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"no direct children", "Readiness unavailable: bd ready failed"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("empty partial failure missing %q:\n%s", want, plain)
		}
	}
	if got := BeadVisualHeight(epic, expansion); got != 3 || got != len(lines) {
		t.Fatalf("height = %d, rendered lines = %d, want 3", got, len(lines))
	}
}

func TestRenderBeadsPaneExpansionStatesAreBoundedAndSanitized(t *testing.T) {
	t.Parallel()

	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: "epic"}
	tests := []struct {
		name      string
		expansion BeadExpansion
		want      string
	}{
		{name: "loading", expansion: BeadExpansion{EpicID: epic.ID, State: BeadExpansionLoading}, want: "loading direct children"},
		{name: "empty", expansion: BeadExpansion{EpicID: epic.ID, State: BeadExpansionLoaded, ReadinessKnown: true}, want: "no direct children"},
		{name: "error", expansion: BeadExpansion{EpicID: epic.ID, State: BeadExpansionError, Detail: "bad\n\x1b[31mtracker"}, want: "Could not load children: bad tracker"},
		{name: "readiness unavailable", expansion: BeadExpansion{EpicID: epic.ID, State: BeadExpansionLoaded, Children: []beadsquery.Bead{{ID: "bd-child", Title: "Child\nrow"}}, Detail: "bd\x1b]52;c;eA==\a failed"}, want: "Readiness unavailable: bd failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const width = 48
			lines := renderBeadsPane([]beadsquery.Bead{epic}, 0, 0, width, 4, tt.expansion)
			plain := ansi.Strip(strings.Join(lines, "\n"))
			if !strings.Contains(plain, tt.want) {
				t.Fatalf("render missing %q:\n%s", tt.want, plain)
			}
			for _, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("line width = %d, want <= %d: %q", got, width, ansi.Strip(line))
				}
			}
			if got := BeadVisualHeight(epic, tt.expansion); got != len(expansionVisualLines(epic, true, width, tt.expansion)) {
				t.Fatalf("height = %d, rendered expansion lines = %d", got, len(expansionVisualLines(epic, true, width, tt.expansion)))
			}
		})
	}
}

func TestRenderBeadsPaneHonorsVisualScrollOffset(t *testing.T) {
	t.Parallel()

	epic := beadsquery.Bead{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: "epic"}
	expansion := BeadExpansion{EpicID: epic.ID, State: BeadExpansionLoaded, ReadinessKnown: true, Children: []beadsquery.Bead{{ID: "bd-child-1", Title: "One"}, {ID: "bd-child-2", Title: "Two"}}}
	lines := renderBeadsPane([]beadsquery.Bead{epic, {ID: "bd-after", Title: "After"}}, -1, 2, 60, 2, expansion)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(plain, "bd-epic") || strings.Contains(plain, "bd-child-1") || !strings.Contains(plain, "bd-child-2") || !strings.Contains(plain, "bd-after") {
		t.Fatalf("visual scroll did not slice flattened expansion lines:\n%s", plain)
	}
}

func TestRenderBeadExpansionProjectionMatchesFullAndStackedDispatch(t *testing.T) {
	t.Parallel()

	expansion := BeadExpansion{
		EpicID: "epic", State: BeadExpansionLoaded, ReadinessKnown: true,
		Children: []beadsquery.Bead{{ID: "child-1", Title: "One"}, {ID: "child-2", Title: "Two"}},
	}
	base := RenderParams{
		Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
		Width: 100, Height: 24, ActivePane: PaneTop,
		BeadsOpen:          []beadsquery.Bead{{ID: "epic", Title: "Epic", IssueType: "epic"}, {ID: "after", Title: "After"}},
		BeadsOpenAvailable: true, BeadsOpenSelected: -1, BeadsOpenScroll: 1, BeadExpansion: expansion,
	}
	full := base
	full.Mode = ModeBeadsOpen
	stacked := base
	stacked.Mode = ModeFlows
	stacked.TopMode = ModeBeadsOpen
	stacked.BottomMode = ModeFlows
	stacked.ContentPane = PaneTop

	for name, params := range map[string]RenderParams{"full": full, "stacked": stacked} {
		t.Run(name, func(t *testing.T) {
			view := ansi.Strip(Render(params))
			if strings.Contains(view, "epic  P0") || !strings.Contains(view, "child-1") || !strings.Contains(view, "child-2") || !strings.Contains(view, "after") {
				t.Fatalf("%s dispatch did not use the shared scrolled expansion projection:\n%s", name, view)
			}
		})
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

func TestRender_BackgroundBeadsEmptyDoesNotReuseFocusedBottomMessage(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:              []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:           0,
		Width:              100,
		Height:             21,
		Mode:               ModeFlows,
		TopMode:            ModeBeadsOpen,
		BottomMode:         ModeFlows,
		ActivePane:         PaneBottom,
		ContentPane:        PaneBottom,
		BeadsOpenAvailable: true,
		RightEmptyMessage:  "No flows",
	}))

	if !strings.Contains(view, "no open beads") {
		t.Fatalf("background Beads pane reused focused bottom empty message:\n%s", view)
	}
}

func TestRender_BackgroundBeadsFilterUsesPaneLocalQuery(t *testing.T) {
	view := ansi.Strip(Render(RenderParams{
		Repos:              []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:           0,
		Width:              100,
		Height:             21,
		Mode:               ModeFlows,
		TopMode:            ModeBeadsOpen,
		BottomMode:         ModeFlows,
		ActivePane:         PaneBottom,
		ContentPane:        PaneBottom,
		BeadsOpenAvailable: true,
		BeadsQuery:         "zzz",
		BeadsSourceCount:   1,
		RightEmptyMessage:  "No flows",
	}))

	if !strings.Contains(view, "No bead results for zzz") {
		t.Fatalf("background Beads pane did not use its own filter query:\n%s", view)
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
		ActivePane: PaneTop,
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

func TestRender_BeadsReadySliceEpicShortcutFollowsAvailability(t *testing.T) {
	base := RenderParams{
		Repos:                  []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:               0,
		Height:                 20,
		Mode:                   ModeBeadsReady,
		ActivePane:             PaneTop,
		BeadsOpen:              []beadsquery.Bead{{ID: "bd-1", Title: "One", IssueType: "epic"}},
		BeadSliceEpicAvailable: true,
	}

	base.Width = 80
	if footer := ansi.Strip(Render(base)); !strings.Contains(footer, "S: slice epic") {
		t.Fatalf("Ready footer missing the slice shortcut:\n%s", footer)
	}

	base.Width = 140
	if pane := ansi.Strip(shortcutPaneText(Render(base))); !strings.Contains(pane, "S      slice epic") {
		t.Fatalf("Ready shortcut pane missing the slice shortcut:\n%s", pane)
	}

	unavailable := base
	unavailable.BeadSliceEpicAvailable = false
	if view := ansi.Strip(Render(unavailable)); strings.Contains(view, "slice epic") {
		t.Fatalf("unavailable slice shortcut was still advertised:\n%s", view)
	}

	// The hint is Ready-only; other Beads subviews never show it.
	other := base
	other.Mode = ModeBeadsOpen
	if view := ansi.Strip(Render(other)); strings.Contains(view, "slice epic") {
		t.Fatalf("slice shortcut leaked into the Open subview:\n%s", view)
	}
}

func TestRender_BeadsReadyFlowShortcutsUseIndependentAvailabilitySnapshots(t *testing.T) {
	base := RenderParams{
		Repos:                        []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:                     0,
		Height:                       20,
		Mode:                         ModeBeadsReady,
		ActivePane:                   PaneTop,
		BeadsOpen:                    []beadsquery.Bead{{ID: "bd-1", Title: "One"}},
		ReadyBeadFlowCreateAvailable: true,
		ReadyBeadFlowStartAvailable:  true,
		ReadyBeadFlowKeysOwned:       true,
		PullAvailable:                true,
	}

	base.Width = 80
	footerView := ansi.Strip(Render(base))
	if !strings.Contains(footerView, "f: new flow") {
		t.Fatalf("Ready footer missing executable Flow shortcut:\n%s", footerView)
	}
	if !strings.Contains(footerView, "F: new flow + start") || strings.Contains(footerView, "F: pull") {
		t.Fatalf("Ready footer did not replace pull with start:\n%s", footerView)
	}

	base.Width = 140
	pane := ansi.Strip(shortcutPaneText(Render(base)))
	if !strings.Contains(pane, "f      new flow") {
		t.Fatalf("Ready shortcut pane missing executable Flow shortcut:\n%s", pane)
	}
	if !strings.Contains(pane, "F      new flow + start") || strings.Contains(pane, "F      pull") {
		t.Fatalf("Ready shortcut pane did not replace pull with start:\n%s", pane)
	}

	createOnly := base
	createOnly.ReadyBeadFlowStartAvailable = false
	createOnlyView := ansi.Strip(Render(createOnly))
	if !strings.Contains(createOnlyView, "new flow") || strings.Contains(createOnlyView, "new flow + start") || strings.Contains(createOnlyView, "pull") {
		t.Fatalf("owned Ready create-only shortcuts are wrong:\n%s", createOnlyView)
	}

	pull := base
	pull.ReadyBeadFlowCreateAvailable = false
	pull.ReadyBeadFlowStartAvailable = false
	pull.ReadyBeadFlowKeysOwned = false
	pullView := ansi.Strip(Render(pull))
	if strings.Contains(pullView, "new flow") || !strings.Contains(pullView, "pull") {
		t.Fatalf("unowned Ready context did not retain pull:\n%s", pullView)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*RenderParams)
	}{
		{name: "left focused", mutate: func(p *RenderParams) { p.ActivePane = PaneRepos }},
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
			params.ReadyBeadFlowStartAvailable = false
			params.ReadyBeadFlowKeysOwned = false
			params.PullAvailable = false
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

func TestRender_SelectedEpicShowsPersistentAutoMarkerAndToggleHints(t *testing.T) {
	base := RenderParams{
		Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
		Width: 90, Height: 12, Mode: ModeBeadsOpen, ActivePane: PaneTop,
		BeadsOpen:          []beadsquery.Bead{{ID: "epic-1", Title: "A deliberately long epic title", IssueType: "epic"}},
		BeadsOpenAvailable: true,
		BeadExpansion: BeadExpansion{
			EpicID: "epic-1", State: BeadExpansionLoaded, ReadinessKnown: true,
			ProgressionKnown: true, ProgressionEnabled: true,
		},
		EpicAutoOffAvailable: true,
		EpicAutoKeyOwned:     true,
	}
	view := ansi.Strip(Render(base))
	if !strings.Contains(view, "[epic]  [auto]") {
		t.Fatalf("selected enabled epic missing both markers:\n%s", view)
	}
	if !strings.Contains(view, "a: auto off") {
		t.Fatalf("enabled epic footer missing disable hint:\n%s", view)
	}

	base.Width = 140
	pane := ansi.Strip(shortcutPaneText(Render(base)))
	if !strings.Contains(pane, "a      auto off") {
		t.Fatalf("enabled epic shortcut pane missing disable hint:\n%s", pane)
	}

	base.BeadExpansion.ProgressionEnabled = false
	base.EpicAutoOffAvailable = false
	base.EpicAutoOnAvailable = true
	view = ansi.Strip(Render(base))
	if strings.Contains(view, "[auto]") || !strings.Contains(view, "auto on") {
		t.Fatalf("disabled epic projection/hint is wrong:\n%s", view)
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
		Width: 90, Height: BeadsContentOverhead + TerminalChipRows + 2, Mode: ModeBeadsOpen, ActivePane: PaneTop,
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
				Width: 140, Height: 20, Mode: tt.mode, ActivePane: PaneTop,
			})
			pane := ansi.Strip(shortcutPaneText(view))
			if !strings.Contains(pane, tt.title) || strings.Count(pane, "r/b/o/i/c") != 1 || !strings.Contains(pane, "subview") || strings.Count(pane, "←/→") != 1 || !strings.Contains(pane, "view") {
				t.Fatalf("right-focused shortcut pane for %v has wrong navigation:\n%s", tt.mode, pane)
			}
		})
		t.Run(tt.title+"/left", func(t *testing.T) {
			view := Render(RenderParams{
				Repos: []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}, Selected: 0,
				Width: 140, Height: 20, Mode: tt.mode, ActivePane: PaneRepos,
			})
			pane := ansi.Strip(shortcutPaneText(view))
			if strings.Contains(pane, "r/b/o/i/c") || strings.Contains(pane, "←/→") {
				t.Fatalf("left-focused shortcut pane for %v advertised right-pane navigation:\n%s", tt.mode, pane)
			}
		})
	}
}
