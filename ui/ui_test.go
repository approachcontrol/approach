package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
)

func TestClearDarkThemeUsesSemanticTruecolorPalette(t *testing.T) {
	requireStyleColor(t, "repo foreground", repoStyle.GetForeground(), clearDarkPalette.success)
	requireStyleColor(t, "branch foreground", branchStyle.GetForeground(), clearDarkPalette.fgStrong)
	requireStyleColor(t, "status foreground", statusStyle.GetForeground(), clearDarkPalette.muted)
	requireStyleColor(t, "root foreground", rootStyle.GetForeground(), clearDarkPalette.focus)
	requireStyleColor(t, "locked foreground", lockedStyle.GetForeground(), clearDarkPalette.info)
	requireStyleColor(t, "ahead behind foreground", aheadBehindStyle.GetForeground(), clearDarkPalette.warning)
	requireStyleColor(t, "dirty foreground", dirtyRedStyle.GetForeground(), clearDarkPalette.danger)
	requireStyleColor(t, "no upstream foreground", noUpstreamStyle.GetForeground(), clearDarkPalette.special)
	requireStyleColor(t, "diff add foreground", diffAddStyle.GetForeground(), clearDarkPalette.success)
	requireStyleColor(t, "diff del foreground", diffDelStyle.GetForeground(), clearDarkPalette.danger)
	requireStyleColor(t, "diff header foreground", diffHdrStyle.GetForeground(), clearDarkPalette.info)
}

func TestClearDarkThemeSelectedRowsUseExplicitSelectionColors(t *testing.T) {
	selectedStyles := map[string]lipgloss.Style{
		"repo":   selectedStyle,
		"stash":  stashSelStyle,
		"branch": branchSelStyle,
	}
	for name, style := range selectedStyles {
		if style.GetReverse() {
			t.Fatalf("%s selected style should not use reverse video", name)
		}
		requireStyleColor(t, name+" foreground", style.GetForeground(), clearDarkPalette.selectionFg)
		requireStyleColor(t, name+" background", style.GetBackground(), clearDarkPalette.selectionBg)
		if !style.GetBold() {
			t.Fatalf("%s selected style should be bold", name)
		}
	}
}

func TestClearDarkThemeBordersUseFocusAndMutedTokens(t *testing.T) {
	if clearDarkTheme.activeBorder != clearDarkPalette.focus {
		t.Fatalf("active border = %v, want %v", clearDarkTheme.activeBorder, clearDarkPalette.focus)
	}
	if clearDarkTheme.inactiveBorder != clearDarkPalette.borderMuted {
		t.Fatalf("inactive border = %v, want %v", clearDarkTheme.inactiveBorder, clearDarkPalette.borderMuted)
	}
	if clearDarkTheme.destructiveBorder != clearDarkPalette.danger {
		t.Fatalf("destructive border = %v, want %v", clearDarkTheme.destructiveBorder, clearDarkPalette.danger)
	}
}

func requireStyleColor(t *testing.T, name string, got lipgloss.TerminalColor, want lipgloss.Color) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestEmbeddedTerminalWidthHelpersReserveSidePadding(t *testing.T) {
	tests := []struct {
		outerWidth int
		wantRender int
		wantPTY    int
	}{
		{outerWidth: 0, wantRender: 0, wantPTY: 1},
		{outerWidth: 1, wantRender: 0, wantPTY: 1},
		{outerWidth: 2, wantRender: 0, wantPTY: 1},
		{outerWidth: 3, wantRender: 1, wantPTY: 1},
		{outerWidth: 4, wantRender: 2, wantPTY: 2},
		{outerWidth: 5, wantRender: 1, wantPTY: 1},
		{outerWidth: 6, wantRender: 2, wantPTY: 2},
		{outerWidth: 120, wantRender: 116, wantPTY: 116},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("outer_width_%d", tt.outerWidth), func(t *testing.T) {
			if got := EmbeddedTerminalRenderContentWidth(tt.outerWidth); got != tt.wantRender {
				t.Fatalf("render content width = %d, want %d", got, tt.wantRender)
			}
			if got := EmbeddedTerminalPTYWidth(tt.outerWidth); got != tt.wantPTY {
				t.Fatalf("PTY width = %d, want %d", got, tt.wantPTY)
			}
		})
	}
}

func forceTrueColor(t *testing.T) {
	t.Helper()
	previousProfile := lipgloss.ColorProfile()
	previousDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDarkBackground)
	})
}

func TestStatusBar_BranchesModeContainsIndicatorLegend(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 1, true, false, false)
	for _, legend := range []string{"✔ clean", "● ahead/behind", "● dirty", "● no upstream", "merged"} {
		if !strings.Contains(bar, legend) {
			t.Errorf("branches mode status bar should contain legend %q", legend)
		}
	}
}

func TestStatusBar_IndicatorLegendSpacing(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 1, true, false, false)
	for _, pair := range [][2]string{
		{"clean", "●"},
	} {
		a := strings.Index(bar, pair[0])
		b := strings.Index(bar[a+len(pair[0]):], pair[1])
		if a == -1 || b == -1 {
			t.Errorf("expected both %q and %q in bar", pair[0], pair[1])
			continue
		}
		gap := bar[a+len(pair[0]) : a+len(pair[0])+b]
		if gap != "  " {
			t.Errorf("expected 2 spaces between legend items, got %q", gap)
		}
	}
}

func TestStatusBar_StashesModeOmitsIndicatorLegend(t *testing.T) {
	bar := RenderStatusBar(120, 3, 0, 1, true, false, false)
	if strings.Contains(bar, "clean") {
		t.Error("stashes mode status bar should not contain indicator legend")
	}
}

func TestStatusBar_PipeSeparatesLegendAndHints(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 1, true, false, false)
	upstreamIdx := strings.Index(bar, "no upstream")
	paneIdx := strings.Index(bar, "bksp: pane")
	if upstreamIdx == -1 || paneIdx == -1 {
		t.Fatalf("expected both 'no upstream' and 'bksp: pane' in bar: %q", bar)
	}
	between := bar[upstreamIdx+len("no upstream") : paneIdx]
	if !strings.Contains(between, "|") {
		t.Errorf("expected pipe separator between legend and hints, got %q", between)
	}
}

func TestStatusBar_TabAndQuitBeforeOtherHints(t *testing.T) {
	bar := RenderStatusBar(160, 2, 0, 1, true, false, false)
	paneIdx := strings.Index(bar, "bksp: pane")
	tIdx := strings.Index(bar, "t: terminal")
	if paneIdx == -1 || tIdx == -1 {
		t.Fatalf("expected both hints in bar: %q", bar)
	}
	if paneIdx > tIdx {
		t.Error("bksp: pane should appear before t: terminal")
	}
	qIdx := strings.Index(bar, "q/esc: quit")
	if qIdx > tIdx {
		t.Error("q/esc: quit should appear before t: terminal")
	}
}

func TestStatusBar_ActionHintsHiddenWhenLeftPaneActive(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 0, true, false, false) // activePane=0 (left), destructive=true
	for _, hint := range []string{"f: fetch", "F: pull", "t: terminal", "c: code", "d: delete"} {
		if strings.Contains(bar, hint) {
			t.Errorf("hint %q should be hidden when left pane is active", hint)
		}
	}
	// Pane switching and q/esc should still appear.
	for _, hint := range []string{"tab: pane", "q/esc: quit"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("hint %q should appear even when left pane is active", hint)
		}
	}
}

func TestStatusBar_ActionHintsShownWhenRightPaneActive(t *testing.T) {
	bar := RenderStatusBar(160, 2, 0, 1, true, false, false) // activePane=1 (right)
	for _, hint := range []string{"n: new branch", "f: fetch", "t: terminal", "c: code", "d: delete"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("hint %q should be shown when right pane is active", hint)
		}
	}
	if strings.Contains(bar, "F: pull") {
		t.Error("public status bar helper should not show pull for branches mode without a selected worktree branch")
	}
}

func TestRender_BranchesModeShowsPullWhenAvailable(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    180,
		Height:   16,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "main", IsWorktree: true}, WorktreePath: "/a"},
		},
		ActivePane:     1,
		FetchAvailable: true,
		PullAvailable:  true,
	})
	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "f/F") || !strings.Contains(pane, "fetch / pull") {
		t.Errorf("branches render should contain grouped fetch/pull shortcut, got:\n%s", pane)
	}
}

func TestRender_LeftPaneShowsFetchVisibleWhenReposExist(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 120,
		Height:                10,
		Mode:                  1,
		ActivePane:            0,
		FetchVisibleAvailable: true,
	})
	if !strings.Contains(shortcutPaneText(view), "f      fetch visible") {
		t.Fatalf("left-pane render should expose fetch-visible hint, got:\n%s", view)
	}
}

func TestRender_LeftPaneShowsNewRepoWhenAvailable(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 120,
		Height:                10,
		Mode:                  ModeWorktrees,
		ActivePane:            0,
		FetchVisibleAvailable: true,
		RepoCreateAvailable:   true,
	})
	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "n      new repo") {
		t.Fatalf("left-pane render should expose repo creation hint, got:\n%s", pane)
	}
}

func TestRender_LeftPaneHidesNewRepoWhenUnavailable(t *testing.T) {
	view := Render(RenderParams{
		Repos:               []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:            0,
		Width:               120,
		Height:              10,
		Mode:                ModeWorktrees,
		ActivePane:          0,
		RepoCreateAvailable: false,
	})
	if strings.Contains(shortcutPaneText(view), "new repo") {
		t.Fatalf("left-pane render should hide repo creation hint when unavailable, got:\n%s", view)
	}
}

func TestRenderRepoCreateFormOverlayShowsFieldsDefaultsFocusAndError(t *testing.T) {
	view := Render(RenderParams{
		Width:   72,
		Height:  12,
		Overlay: OverlayForm,
		Form: FormView{
			Title:      "New repo",
			FocusIndex: 0,
			Error:      "repo name cannot be empty",
			Fields: []FormField{
				{ID: "name", Kind: FormText, Label: "Repo name", Placeholder: "my-repo", Value: "app", Cursor: 3},
				{ID: "github", Kind: FormCheckbox, Label: "Create GitHub repo", Checked: true},
				{ID: "visibility", Kind: FormChoice, Label: "Visibility", Options: []SelectItem{
					{Label: "Public", Value: "public"},
					{Label: "Private", Value: "private"},
				}, SelectedIndex: 0},
			},
		},
	})
	text := ansi.Strip(view)
	for _, want := range []string{
		"New repo",
		"> Repo name",
		"app",
		"[x] Create GitHub repo",
		"(o) Public",
		"( ) Private",
		"repo name cannot be empty",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("form overlay missing %q:\n%s", want, text)
		}
	}
	requireLinesWithinWidth(t, strippedLines(view), 72)
}

func TestRenderRepoCreateFormStatusBarUsesFormControls(t *testing.T) {
	view := Render(RenderParams{
		Width:   90,
		Height:  8,
		Overlay: OverlayForm,
		Form: FormView{
			Title: "New repo",
			Fields: []FormField{
				{ID: "name", Kind: FormText, Label: "Repo name"},
			},
		},
	})
	lines := strippedLines(view)
	status := lines[len(lines)-1]
	for _, want := range []string{"tab/shift+tab", "space", "enter: submit", "esc: cancel"} {
		if !strings.Contains(status, want) {
			t.Fatalf("form status missing %q: %q", want, status)
		}
	}
	if strings.Contains(status, "scroll") || strings.Contains(status, "bksp/del") {
		t.Fatalf("form status should not inherit generic overlay controls: %q", status)
	}
}

func TestRenderFlowCreateFormOverlayIsCompactAndLeavesBackgroundVisible(t *testing.T) {
	form := FormView{
		Purpose:    "flow-create",
		Title:      "New flow",
		FocusIndex: 1,
		Fields: []FormField{
			{ID: "title", Kind: FormText, Label: "Title", Placeholder: FlowTitleInputPlaceholder, Value: "Add Flow Mode", Cursor: len([]rune("Add Flow Mode"))},
			{
				ID:          "instructions",
				Kind:        FormMultilineText,
				Label:       "Instructions",
				Placeholder: FlowInstructionsInputPlaceholder,
				Value: strings.Join([]string{
					"Start with the saved plan.",
					"Keep the center line near the cursor.",
					"Implement the form.",
					"Run tests.",
					"Finish with a local commit.",
				}, "\n"),
				Cursor: len([]rune(strings.Join([]string{
					"Start with the saved plan.",
					"Keep the center line near the cursor.",
					"Implement the form.",
				}, "\n"))),
			},
			{ID: "base-ref", Kind: FormText, Label: "Base ref", Placeholder: FlowBaseRefInputPlaceholder, Value: "main", Cursor: len([]rune("main"))},
		},
	}
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   18,
		Mode:     ModeFlows,
		Overlay:  OverlayForm,
		Form:     form,
	})
	text := ansi.Strip(view)
	for _, want := range []string{"alpha", "New flow", "Title", "Instructions", "Base ref", "main", shortcutOverflowMarker, "alt+enter: newline"} {
		if !strings.Contains(text, want) {
			t.Fatalf("flow form overlay missing %q:\n%s", want, text)
		}
	}
	if got := formPanelWidth(form, 120); got > flowCreateFormMaxWidth {
		t.Fatalf("flow form panel width = %d, want <= %d", got, flowCreateFormMaxWidth)
	}

	lines := strippedLines(view)
	panelTop, panelBottom := -1, -1
	titleRow := ""
	for i, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "New flow") && panelTop == -1 {
			panelTop = i - 1
			titleRow = line
		}
		if strings.Contains(trimmed, "Base ref") {
			panelBottom = i + 1
		}
		requireLinesWithinWidth(t, []string{line}, 120)
	}
	if panelTop < 0 || panelBottom < panelTop {
		t.Fatalf("could not locate compact flow form panel:\n%s", text)
	}
	if !strings.Contains(titleRow, "destructive mode") {
		t.Fatalf("flow form should preserve same-row background content beside the panel:\n%s", titleRow)
	}
	if panelHeight := panelBottom - panelTop + 1; panelHeight >= 12 {
		t.Fatalf("flow form panel height = %d, want compact panel below 12 rows:\n%s", panelHeight, text)
	}
}

func TestRenderFlowCreateFormOverlayFitsNarrowTerminal(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Selected: 0,
		Width:    38,
		Height:   14,
		Mode:     ModeFlows,
		Overlay:  OverlayForm,
		Form: FormView{
			Purpose:    "flow-create",
			Title:      "New flow",
			FocusIndex: 1,
			Error:      "flow instructions must explain the work to perform",
			Fields: []FormField{
				{ID: "title", Kind: FormText, Label: "Title", Placeholder: FlowTitleInputPlaceholder, Value: "Very long flow title that wraps", Cursor: len([]rune("Very long flow title that wraps"))},
				{ID: "instructions", Kind: FormMultilineText, Label: "Instructions", Placeholder: FlowInstructionsInputPlaceholder, Value: "Implement a compact single form with multiline instructions and narrow terminal wrapping.", Cursor: len([]rune("Implement a compact single form with multiline instructions and narrow terminal wrapping."))},
				{ID: "base-ref", Kind: FormText, Label: "Base ref", Placeholder: FlowBaseRefInputPlaceholder, Value: "feature/some-long-base-ref", Cursor: len([]rune("feature/some-long-base-ref"))},
			},
		},
	})

	requireLinesWithinWidth(t, strippedLines(view), 38)
}

func TestRender_SessionsModeShowsHeaderAndRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:     sessions.ProviderCodex,
			SessionID:    "codex-session-1",
			Status:       "ended",
			RepoPath:     "/dev/wtui",
			WorktreePath: "/dev/wtui-worktrees/sessions",
			Branch:       "feature/headers",
			Summary:      "Implement session capture",
		}},
		ActivePane:      1,
		SessionSelected: 0,
	})

	for _, want := range []string{"[6] sessions", "Provider", "Branch", "Worktree", "Status", "Summary", "codex", "feature/headers", "sessions", "ended", "Implement session capture"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sessions view missing %q:\n%s", want, view)
		}
	}

	headerLine := lineContaining(view, "Provider")
	rowLine := lineContaining(view, "feature/headers")
	for _, pair := range [][2]string{
		{"Provider", "codex"},
		{"Branch", "feature/headers"},
		{"Worktree", "sessions"},
		{"Status", "ended"},
		{"Summary", "Implement session capture"},
	} {
		headerColumn := strings.Index(headerLine, pair[0])
		rowColumn := strings.Index(rowLine, pair[1])
		if headerColumn != rowColumn {
			t.Fatalf("%s header starts at column %d, row value %q starts at column %d:\n%s\n%s", pair[0], headerColumn, pair[1], rowColumn, headerLine, rowLine)
		}
	}
}

func TestRender_SessionsModeShowsEmbeddedTerminalInsteadOfSessionRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:  sessions.ProviderCodex,
			SessionID: "codex-session-1",
			Branch:    "feature/saved",
			Summary:   "saved session row",
		}},
		EmbeddedTerminals: []EmbeddedTerminalTab{{
			Number:   1,
			Provider: "codex",
			Identity: "feature/api",
			State:    "running",
			Active:   true,
		}},
		EmbeddedTerminalLines: []string{"agent output"},
		ActivePane:            1,
		SessionSelected:       0,
	})

	for _, want := range []string{"[6] sessions", "1 codex feature/api running", "agent output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("embedded sessions view missing %q:\n%s", want, view)
		}
	}
	for _, hidden := range []string{"Provider", "Summary", "saved session row"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("embedded sessions view should hide saved session table %q:\n%s", hidden, view)
		}
	}
	lines := strippedLines(view)
	if len(lines) != 10 {
		t.Fatalf("rendered line count = %d, want 10:\n%s", len(lines), view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > 180 {
			t.Fatalf("rendered line %d width = %d, want <= 180:\n%s", i, width, view)
		}
	}
	header := lineIndexContaining(lines, "1 codex feature/api running")
	body := lineIndexContaining(lines, "agent output")
	if header < 1 || body <= header {
		t.Fatalf("embedded terminal header/body should render in order, got indexes header=%d body=%d:\n%s", header, body, view)
	}
	top := header - 1
	bottom := -1
	for i := body + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "└") {
			bottom = i
			break
		}
	}
	if !strings.Contains(lines[top], "┌") || bottom <= body {
		t.Fatalf("embedded terminal frame should wrap header and body in order, got indexes top=%d header=%d body=%d bottom=%d:\n%s", top, header, body, bottom, view)
	}
	if !strings.Contains(lines[header], "│ 1 codex feature/api running") || !strings.Contains(lines[body], "│ agent output") {
		t.Fatalf("embedded terminal header/body should be inside inner border:\n%s\n%s", lines[header], lines[body])
	}
}

func TestRenderEmbeddedTerminalPaneWindowsLinesInsideBorder(t *testing.T) {
	lines := renderEmbeddedTerminalPane([]EmbeddedTerminalTab{{
		Number:   1,
		Provider: "codex",
		Identity: "implementation",
		State:    "running",
		Active:   true,
	}}, []string{
		"terminal line 1",
		"terminal line 2",
		"terminal line 3",
		"terminal line 4",
		"terminal line 5",
	}, false, true, 28, 5)
	stripped := stripLines(lines)

	if len(stripped) != 5 {
		t.Fatalf("line count = %d, want 5:\n%s", len(stripped), strings.Join(stripped, "\n"))
	}
	requireLinesWithinWidth(t, stripped, 28)
	for index, want := range map[int]string{
		0: "┌",
		1: "│ 1 codex implementation",
		2: "│ terminal line 4",
		3: "│ terminal line 5",
		4: "└",
	} {
		if !strings.Contains(stripped[index], want) {
			t.Fatalf("line %d = %q, want to contain %q:\n%s", index, stripped[index], want, strings.Join(stripped, "\n"))
		}
	}
	for _, hidden := range []string{"terminal line 1", "terminal line 2", "terminal line 3"} {
		if strings.Contains(strings.Join(stripped, "\n"), hidden) {
			t.Fatalf("old live line %q should be windowed out:\n%s", hidden, strings.Join(stripped, "\n"))
		}
	}
}

func TestRenderEmbeddedTerminalFramePreservesBorderWidthWithSidePadding(t *testing.T) {
	for _, tt := range []struct {
		width           int
		wantContentLine string
		wantTop         string
		wantBottom      string
	}{
		{width: 1, wantContentLine: "│", wantTop: "┌", wantBottom: "└"},
		{width: 2, wantContentLine: "││", wantTop: "┌┐", wantBottom: "└┘"},
		{width: 3, wantContentLine: "│a│", wantTop: "┌─┐", wantBottom: "└─┘"},
		{width: 4, wantContentLine: "│ab│", wantTop: "┌──┐", wantBottom: "└──┘"},
		{width: 5, wantContentLine: "│ a │", wantTop: "┌───┐", wantBottom: "└───┘"},
	} {
		t.Run(fmt.Sprintf("width_%d", tt.width), func(t *testing.T) {
			contentLine := stripLines([]string{renderEmbeddedTerminalFrameContentLine("abc", lipgloss.NewStyle(), EmbeddedTerminalRenderContentWidth(tt.width), tt.width)})[0]
			if contentLine != tt.wantContentLine {
				t.Fatalf("content line = %q, want %q", contentLine, tt.wantContentLine)
			}

			frame := stripLines(renderEmbeddedTerminalFrame([]string{"abc"}, false, tt.width, 3))
			if len(frame) != 3 {
				t.Fatalf("frame lines = %#v, want 3 lines", frame)
			}
			if frame[0] != tt.wantTop {
				t.Fatalf("top border = %q, want %q", frame[0], tt.wantTop)
			}
			if frame[2] != tt.wantBottom {
				t.Fatalf("bottom border = %q, want %q", frame[2], tt.wantBottom)
			}
		})
	}
}

func TestRenderEmbeddedTerminalPaneSmallAllocations(t *testing.T) {
	tabs := []EmbeddedTerminalTab{{Number: 1, Provider: "codex", State: "running", Active: true}}
	for _, tc := range []struct {
		height int
		want   []string
	}{
		{height: 0, want: nil},
		{height: 1, want: []string{"┌──────────┐"}},
		{height: 2, want: []string{"┌──────────┐", "└──────────┘"}},
		{height: 3, want: []string{"┌──────────┐", "│ 1 codex  │", "└──────────┘"}},
	} {
		t.Run(fmt.Sprintf("height_%d", tc.height), func(t *testing.T) {
			got := stripLines(renderEmbeddedTerminalPane(tabs, []string{"body"}, false, false, 12, tc.height))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("lines = %#v, want %#v", got, tc.want)
			}
			requireLinesWithinWidth(t, got, 12)
		})
	}

	for _, width := range []int{2, 3} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			got := stripLines(renderEmbeddedTerminalPane(tabs, []string{"body"}, false, false, width, 4))
			if len(got) != 4 {
				t.Fatalf("line count = %d, want 4: %#v", len(got), got)
			}
			requireLinesWithinWidth(t, got, width)
		})
	}
}

func TestRenderEmbeddedTerminalPaneBorderUsesFocusColor(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDarkBackground)
	})

	tabs := []EmbeddedTerminalTab{{Number: 1, Provider: "codex", Active: true}}
	focused := renderEmbeddedTerminalPane(tabs, nil, false, true, 12, 3)
	unfocused := renderEmbeddedTerminalPane(tabs, nil, false, false, 12, 3)

	activeTop := embeddedTerminalBorderStyle(true).Render("┌──────────┐")
	mutedTop := embeddedTerminalBorderStyle(false).Render("┌──────────┐")
	if focused[0] != activeTop {
		t.Fatalf("focused top border = %q, want %q", focused[0], activeTop)
	}
	if unfocused[0] != mutedTop {
		t.Fatalf("unfocused top border = %q, want %q", unfocused[0], mutedTop)
	}
	if focused[0] == unfocused[0] {
		t.Fatalf("focused and unfocused border styles should differ: %q", focused[0])
	}
}

func TestRender_SessionsEmbeddedTerminalShowsPrefixCue(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     ModeSessions,
		EmbeddedTerminals: []EmbeddedTerminalTab{{
			Number:   1,
			Provider: "codex",
			Identity: "feature/api",
			State:    "running",
			Active:   true,
		}},
		EmbeddedTerminalLines:  []string{"agent output"},
		EmbeddedTerminalPrefix: true,
		ActivePane:             1,
	})

	if !strings.Contains(view, "ctrl+]") {
		t.Fatalf("embedded terminal prefix cue missing:\n%s", view)
	}
}

func TestRender_SessionsEmbeddedTerminalShortcutsDimUntilPrefix(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDarkBackground)
	})

	normal := renderShortcutPane(statusBarParams{
		Mode:                   ModeSessions,
		ActivePane:             1,
		EmbeddedTerminalActive: true,
	}, 26, 12)
	normalText := ansi.Strip(normal)
	if !strings.Contains(normalText, "ctrl+] commands") {
		t.Fatalf("embedded terminal should expose prefix shortcut:\n%s", normalText)
	}
	if strings.Contains(normalText, "x      close") {
		t.Fatalf("embedded terminal should hide prefix-only close shortcut until ctrl+] mode:\n%s", normalText)
	}
	if want := statusStyle.Render("ctrl+]"); !strings.Contains(normal, want) {
		t.Fatalf("embedded terminal shortcut key should render muted while terminal input is active:\n%q\nmissing %q", normal, want)
	}

	prefix := renderShortcutPane(statusBarParams{
		Mode:                   ModeSessions,
		ActivePane:             1,
		EmbeddedTerminalActive: true,
		EmbeddedTerminalPrefix: true,
	}, 26, 12)
	prefixText := ansi.Strip(prefix)
	for _, want := range []string{"ctrl+] send", "l      sessions", "d      detach", "x      close", "q/esc  quit", "1-9    switch"} {
		if !strings.Contains(prefixText, want) {
			t.Fatalf("embedded terminal prefix shortcuts missing %q:\n%s", want, prefixText)
		}
	}
	if want := shortcutKeyStyle.Render("x"); !strings.Contains(prefix, want) {
		t.Fatalf("embedded terminal prefix shortcut key should render active:\n%q\nmissing %q", prefix, want)
	}
}

func TestRender_SessionsModeKeepsSummaryOnOneLine(t *testing.T) {
	params := RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   12,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{
			{
				Provider:  sessions.ProviderCodex,
				SessionID: "codex-above",
				Status:    "ended",
				RepoPath:  "/dev/wtui",
				Branch:    "above",
				Summary:   "above summary",
			},
			{
				Provider:  sessions.ProviderCodex,
				SessionID: "codex-selected",
				Status:    "ended",
				RepoPath:  "/dev/wtui",
				Branch:    "selected",
				Summary:   "selected first line\n\nselected third line",
			},
			{
				Provider:  sessions.ProviderClaude,
				SessionID: "claude-below",
				Status:    "ended",
				RepoPath:  "/dev/wtui",
				Branch:    "below",
				Summary:   "below summary",
			},
		},
		ActivePane:      1,
		SessionSelected: 1,
	}

	baseline := strippedLines(Render(params))
	view := Render(params)
	lines := strippedLines(view)

	baselineAbove := lineIndexContaining(baseline, "above summary")
	above := lineIndexContaining(lines, "above summary")
	row := lineIndexContaining(lines, "selected first line")
	below := lineIndexContaining(lines, "below summary")

	if above != baselineAbove {
		t.Fatalf("summary should not force preceding content up, above row index=%d baseline=%d:\n%s", above, baselineAbove, view)
	}
	if row < 0 || below != row+1 {
		t.Fatalf("summary should occupy only the selected row, row=%d below=%d:\n%s", row, below, view)
	}
	if !strings.Contains(lines[row], "selected first line selected third line") {
		t.Fatalf("summary whitespace should collapse onto one display row:\n%s", view)
	}
}

func TestRender_SessionsModeTruncatesSummaryToPaneWidth(t *testing.T) {
	const rightContentWidth = 58
	lines := renderSessionPane([]sessions.SessionRecord{{
		Provider:  sessions.ProviderCodex,
		SessionID: "codex-selected",
		Status:    "ended",
		RepoPath:  "/dev/wtui",
		Branch:    "selected",
		Summary:   "selected first line " + strings.Repeat("very long summary ", 20),
	}}, 0, 0, rightContentWidth, 4)
	row := ""
	for _, line := range lines {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, "selected") {
			row = stripped
			break
		}
	}

	if row == "" {
		t.Fatalf("expected session row in view:\n%s", strings.Join(lines, "\n"))
	}
	if lipgloss.Width(row) > rightContentWidth {
		t.Fatalf("session row width = %d, want <= %d:\n%s", lipgloss.Width(row), rightContentWidth, strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if strings.Contains(line, "very long summary very long summary very long summary very long summary") {
			t.Fatalf("summary should be truncated to pane width, got overlong visible text:\n%s", strings.Join(lines, "\n"))
		}
	}
}

func lineContaining(view, needle string) string {
	for _, line := range strings.Split(view, "\n") {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, needle) {
			return stripped
		}
	}
	return ""
}

func strippedLines(view string) []string {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = ansi.Strip(line)
	}
	return lines
}

func stripLines(lines []string) []string {
	stripped := make([]string, len(lines))
	for i, line := range lines {
		stripped[i] = ansi.Strip(line)
	}
	return stripped
}

func requireLinesWithinWidth(t *testing.T, lines []string, width int) {
	t.Helper()
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func lineIndexContaining(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func shortcutPaneLines(view string) []string {
	start := -1
	for _, line := range strings.Split(view, "\n") {
		stripped := ansi.Strip(line)
		if idx := strings.Index(stripped, "Shortcuts"); idx >= 0 {
			start = idx
			break
		}
	}
	if start < 0 {
		return nil
	}

	var lines []string
	for _, line := range strings.Split(view, "\n") {
		stripped := ansi.Strip(line)
		if len(stripped) <= start {
			continue
		}
		text := strings.Trim(stripped[start:], " │")
		if text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

func shortcutPaneText(view string) string {
	return strings.Join(shortcutPaneLines(view), "\n")
}

func TestRender_SessionsModeEmptyMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
	}{
		{name: "empty", message: "No sessions"},
		{name: "fetch failure", message: "Could not load sessions; see status bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:             []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
				Selected:          0,
				Width:             120,
				Height:            10,
				Mode:              ModeSessions,
				RightEmptyMessage: tc.message,
			})
			if !strings.Contains(view, tc.message) {
				t.Fatalf("sessions empty view missing %q:\n%s", tc.message, view)
			}
		})
	}
}

func TestRender_SessionsModeShowsSelectedSessionShortcuts(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    120,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:  sessions.ProviderCodex,
			SessionID: "codex-session-1",
			Status:    "ended",
			RepoPath:  "/dev/wtui",
			Summary:   "Implement session capture",
		}},
		ActivePane:      1,
		SessionSelected: 0,
	})
	pane := shortcutPaneText(view)
	for _, want := range []string{"o      transcript", "r      resume", "s      summary", "y      copy id"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("sessions view should expose selected session shortcut %q:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "s      copy id") {
		t.Fatalf("sessions view should not expose old copy-id shortcut:\n%s", pane)
	}
}

func TestRender_SessionsModeHidesSessionActionsWithoutSelection(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    120,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:  sessions.ProviderCodex,
			SessionID: "codex-session-1",
			Status:    "ended",
			RepoPath:  "/dev/wtui",
		}},
		ActivePane:      1,
		SessionSelected: -1,
	})
	pane := shortcutPaneText(view)
	for _, hidden := range []string{"o      transcript", "r      resume", "s      summary", "y      copy id", "s      copy id"} {
		if strings.Contains(pane, hidden) {
			t.Fatalf("sessions view should hide %q without selected session:\n%s", hidden, pane)
		}
	}
}

func TestRender_LeftPaneShortcutPaneShowsFetchVisibleWhenReposExist(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 120,
		Height:                24,
		Mode:                  1,
		ActivePane:            0,
		FetchVisibleAvailable: true,
	})
	if !strings.Contains(shortcutPaneText(view), "f      fetch visible") {
		t.Fatalf("left-pane shortcut pane should expose fetch-visible hint, got:\n%s", view)
	}
}

func TestRender_LeftPaneHidesFetchVisibleWhenNoReposVisible(t *testing.T) {
	view := Render(RenderParams{
		Width:      120,
		Height:     10,
		Mode:       1,
		ActivePane: 0,
	})
	if strings.Contains(view, "f: fetch visible") {
		t.Fatalf("empty left-pane render should hide fetch-visible hint, got:\n%s", view)
	}
}

func TestStatusBar_WorktreesModeShowsNewWorktreeHint(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	if !strings.Contains(bar, "n: new worktree") {
		t.Fatalf("expected new worktree hint in worktrees mode, got %q", bar)
	}
	if !strings.Contains(bar, "P: PR") {
		t.Fatalf("expected PR worktree hint in worktrees mode, got %q", bar)
	}
}

func TestStatusBar_ShowsSetAgentHint(t *testing.T) {
	for _, activePane := range []int{0, 1} {
		bar := RenderStatusBar(160, 1, 0, activePane, false, false, false)
		if !strings.Contains(bar, "A: set agent") {
			t.Fatalf("expected set agent hint for activePane %d, got %q", activePane, bar)
		}
	}
}

func TestRender_WorktreesModeShowsAgentHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:             []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:          0,
		Width:             140,
		Height:            20,
		Mode:              1,
		ActivePane:        1,
		Worktrees:         []gitquery.Worktree{{Path: "/a", BranchName: "main", IsMain: true}},
		AgentAvailable:    true,
		NewAgentAvailable: true,
	})
	pane := shortcutPaneText(view)
	for _, hint := range []string{"A      set agent", "a      agent", "N      new+agent"} {
		if !strings.Contains(pane, hint) {
			t.Fatalf("expected worktrees view to contain %q, got %q", hint, view)
		}
	}
}

func TestRender_WorktreesModeShowsInlineSessionHints(t *testing.T) {
	base := RenderParams{
		Repos: []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Worktrees: []gitquery.Worktree{
			{Path: "/dev/wtui-worktrees/inline", BranchName: "feature/inline"},
		},
		Selected:          0,
		Width:             140,
		Height:            12,
		Mode:              ModeWorktrees,
		ActivePane:        1,
		WorktreeSelected:  0,
		NewAgentAvailable: true,
	}

	closed := shortcutPaneText(Render(base))
	if !strings.Contains(closed, "x      sessions") {
		t.Fatalf("closed inline sessions should expose sessions shortcut, got:\n%s", closed)
	}

	base.InlineWorktreeSessions = true
	base.WorktreeSessionsOpen = true
	base.WorktreeSessions = []sessions.SessionRecord{{
		Provider:  sessions.ProviderCodex,
		SessionID: "codex-inline-1",
		Branch:    "feature/inline",
	}}
	base.WorktreeSessionSelected = 0
	open := shortcutPaneText(Render(base))
	if !strings.Contains(open, "enter  resume") {
		t.Fatalf("open inline sessions should expose resume shortcut, got:\n%s", open)
	}
	if strings.Contains(open, "x      sessions") {
		t.Fatalf("open inline sessions should not expose closed sessions shortcut, got:\n%s", open)
	}
}

func TestRender_WorktreesInlineSessionsVisibleWhenSelectedAtViewportBottom(t *testing.T) {
	view := Render(RenderParams{
		Repos: []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Worktrees: []gitquery.Worktree{
			{Path: "/dev/wtui-worktrees/row-0", BranchName: "row-0"},
			{Path: "/dev/wtui-worktrees/row-1", BranchName: "row-1"},
			{Path: "/dev/wtui-worktrees/row-2", BranchName: "row-2"},
			{Path: "/dev/wtui-worktrees/row-3", BranchName: "row-3"},
			{Path: "/dev/wtui-worktrees/row-4", BranchName: "row-4"},
		},
		Selected:                0,
		Width:                   140,
		Height:                  10,
		Mode:                    ModeWorktrees,
		ActivePane:              1,
		WorktreeSelected:        4,
		WorktreeScroll:          0,
		InlineWorktreeSessions:  true,
		WorktreeSessionsOpen:    true,
		WorktreeSessionSelected: 0,
		WorktreeSessions: []sessions.SessionRecord{{
			Provider:  sessions.ProviderCodex,
			SessionID: "codex-inline-bottom",
			Branch:    "bottom-inline-session",
		}},
	})

	if !strings.Contains(view, "row-4") {
		t.Fatalf("selected worktree should stay visible:\n%s", view)
	}
	if !strings.Contains(view, "bottom-inline-session") {
		t.Fatalf("inline sessions should be visible below a bottom-row worktree:\n%s", view)
	}
}

func TestRender_WorktreesModeShowsEmptyInlineSessions(t *testing.T) {
	view := Render(RenderParams{
		Repos: []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Worktrees: []gitquery.Worktree{
			{Path: "/dev/wtui-worktrees/inline", BranchName: "feature/inline"},
		},
		Selected:               0,
		Width:                  140,
		Height:                 12,
		Mode:                   ModeWorktrees,
		ActivePane:             1,
		WorktreeSelected:       0,
		InlineWorktreeSessions: true,
		WorktreeSessionsOpen:   true,
	})

	if !strings.Contains(view, "Sessions: none") {
		t.Fatalf("empty inline sessions should render in-pane empty copy:\n%s", view)
	}
	if strings.Contains(shortcutPaneText(view), "enter  resume") {
		t.Fatalf("empty inline sessions should not expose resume shortcut:\n%s", view)
	}
}

func TestRender_StaleWorktreeShowsSetAgentHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:             []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:          0,
		Width:             140,
		Height:            16,
		Mode:              1,
		ActivePane:        1,
		Worktrees:         []gitquery.Worktree{{Path: "/gone", BranchName: "gone", Stale: true}},
		NewAgentAvailable: true,
	})
	pane := shortcutPaneText(view)
	for _, hint := range []string{"A      set agent", "N      new+agent"} {
		if !strings.Contains(pane, hint) {
			t.Fatalf("expected stale worktree view to contain %q, got %q", hint, view)
		}
	}
}

func TestRender_BranchesModeShowsAgentHintOnlyWhenTargetAvailable(t *testing.T) {
	params := RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      140,
		Height:     18,
		Mode:       2,
		ActivePane: 1,
		Branches:   []gitquery.BranchRow{{Branch: gitquery.Branch{Name: "feat"}}},
	}
	view := Render(params)
	if strings.Contains(shortcutPaneText(view), "a      agent") {
		t.Fatalf("bare branch should not show agent hint, got %q", view)
	}

	params.AgentAvailable = true
	params.Branches = []gitquery.BranchRow{{Branch: gitquery.Branch{Name: "main", IsWorktree: true}, WorktreePath: "/a"}}
	view = Render(params)
	if !strings.Contains(shortcutPaneText(view), "a      agent") {
		t.Fatalf("checked-out branch should show agent hint, got %q", view)
	}
}

func TestRender_ShortcutPaneShowsDefaultViewSetting(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:         0,
		Width:            140,
		Height:           18,
		Mode:             ModeWorktrees,
		ActivePane:       1,
		DefaultViewLabel: "8 flows",
		Worktrees:        []gitquery.Worktree{{Path: "/a", BranchName: "main", IsMain: true}},
		WorktreeSelected: 0,
	})
	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "V      default 8 flows") {
		t.Fatalf("shortcut pane should advertise default view setting:\n%s", pane)
	}
}

func TestRender_FlowShortcutPaneShowsDefaultViewSetting(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:         0,
		Width:            160,
		Height:           18,
		Mode:             ModeFlows,
		ActivePane:       1,
		DefaultViewLabel: "2 branches",
		Flows:            []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: "/a", Status: flowstore.StatusInProgress}},
		FlowSelected:     0,
	})
	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "V      default 2 branches") {
		t.Fatalf("flow shortcut pane should advertise default view setting:\n%s", pane)
	}
}

func TestRender_DefaultViewFooterHintFitsNarrowWidth(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:         0,
		Width:            80,
		Height:           10,
		Mode:             ModeWorktrees,
		ActivePane:       1,
		DefaultViewLabel: "8 flows",
		Worktrees:        []gitquery.Worktree{{Path: "/a", BranchName: "main", IsMain: true}},
		WorktreeSelected: 0,
	})
	lines := strings.Split(ansi.Strip(view), "\n")
	status := lines[len(lines)-1]
	if strings.Contains(status, "\n") {
		t.Fatalf("status footer should stay on one line, got %q", status)
	}
	if got := lipgloss.Width(status); got > 80 {
		t.Fatalf("status footer width = %d, want <= 80: %q", got, status)
	}
}

func TestRender_TerminalFocusedShortcutPaneOmitsDefaultViewSetting(t *testing.T) {
	pane := shortcutPaneText(renderShortcutPane(statusBarParams{
		Mode:                   ModeFlows,
		ActivePane:             1,
		EmbeddedTerminalActive: true,
		DefaultViewLabel:       "8 flows",
	}, 26, 12))
	if strings.Contains(pane, "default 8 flows") || strings.Contains(pane, "V      ") {
		t.Fatalf("terminal-focused shortcuts should not advertise default-view key:\n%s", pane)
	}
	if !strings.Contains(pane, "ctrl+] commands") {
		t.Fatalf("terminal-focused shortcuts should advertise terminal command prefix:\n%s", pane)
	}
}

func TestStatusBar_InputOverlayShowsSingleLineHints(t *testing.T) {
	bar := Render(RenderParams{
		Width:       120,
		Height:      8,
		Mode:        ModeWorktrees,
		Overlay:     OverlayInput,
		InputPrompt: "Create worktree from",
		InputMode:   InputSingleLine,
	})
	status := strings.Split(bar, "\n")[7]
	for _, hint := range []string{"enter: submit", "esc: cancel", "bksp/del: edit"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("expected hint %q in input overlay bar %q", hint, status)
		}
	}
	if strings.Contains(status, "alt+enter") {
		t.Fatalf("single-line input status should not show multi-line hint: %q", status)
	}
}

func TestStatusBar_InputOverlayShowsMultiLineHints(t *testing.T) {
	view := Render(RenderParams{
		Width:       120,
		Height:      8,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: LaunchInstructionsPrompt,
		InputMode:   InputMultiLine,
	})
	status := strings.Split(view, "\n")[7]
	for _, hint := range []string{"enter: submit", "alt+enter: newline", "esc: cancel"} {
		if !strings.Contains(status, hint) {
			t.Errorf("expected hint %q in multi-line input status %q", hint, status)
		}
	}
}

func TestStatusBar_SelectOverlayShowsSelectHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, OverlaySelect, 1, false, false, false)
	for _, hint := range []string{"up/down select", "enter: confirm", "esc: cancel"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("expected hint %q in select overlay bar %q", hint, bar)
		}
	}
	for _, forbidden := range []string{"bksp", "backspace", "left/right"} {
		if strings.Contains(bar, forbidden) {
			t.Fatalf("select status should not show text-input hint %q: %q", forbidden, bar)
		}
	}
}

func TestStatusBar_LaunchInstructionsOverlayShowsLaunchHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            120,
		Height:           12,
		Mode:             ModePlans,
		Overlay:          OverlayInput,
		InputPrompt:      LaunchInstructionsPrompt,
		InputPlaceholder: "launch instructions",
		InputValue:       "Implement the selected plan",
		InputMode:        InputMultiLine,
	})
	status := strings.Split(view, "\n")[11]
	for _, hint := range []string{"enter: submit", "alt+enter: newline", "esc: cancel"} {
		if !strings.Contains(status, hint) {
			t.Errorf("expected hint %q in launch overlay bar %q", hint, status)
		}
	}
	if strings.Contains(status, "enter: create") {
		t.Fatalf("launch instructions status should not show create hint: %q", status)
	}
}

func TestStatusBar_KeyHintSpacingIs2(t *testing.T) {
	bar := RenderStatusBar(160, 2, 0, 1, true, false, false)
	for _, pair := range [][2]string{
		{"bksp: pane", "q/esc: quit"},
		{"d: delete", "f: fetch"},
		{"t: terminal", "c: code"},
	} {
		a := strings.Index(bar, pair[0])
		b := strings.Index(bar, pair[1])
		if a == -1 || b == -1 {
			t.Errorf("expected both %q and %q in bar", pair[0], pair[1])
			continue
		}
		gap := bar[a+len(pair[0]) : b]
		if gap != "  " {
			t.Errorf("expected 2 spaces between %q and %q, got %q", pair[0], pair[1], gap)
		}
	}
}

func TestModeHeader_ShowsActiveMode(t *testing.T) {
	header := renderModeHeader(1, 60)
	if !strings.Contains(header, "[1] worktrees") {
		t.Error("mode header should show active mode 1 bracketed")
	}
	if strings.Contains(header, "[2]") {
		t.Error("inactive mode 2 should not be bracketed")
	}
	header = renderModeHeader(3, 60)
	if !strings.Contains(header, "[3] stashes") {
		t.Error("mode header should show active mode 3 bracketed")
	}
}

func TestModeHeader_HasSeparatorLine(t *testing.T) {
	header := renderModeHeader(1, 40)
	lines := strings.Split(header, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected mode header to have at least 2 lines, got %d", len(lines))
	}
	// Second line should be a separator (dashes or similar)
	separator := lines[1]
	if !strings.Contains(separator, "─") {
		t.Errorf("expected separator line with ─ chars, got %q", separator)
	}
}

func TestRender_ModeHeaderInRightPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     1,
	})
	if !strings.Contains(view, "[1] worktrees") {
		t.Error("render should contain mode header '[1] worktrees' in right pane")
	}
}

func TestRender_WideLayoutShowsShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   18,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		FetchAvailable:   true,
		PullAvailable:    true,
	})

	for _, want := range []string{"Shortcuts", "Worktrees", "Global", "Navigate", "Actions", "n", "new worktree", "F", "pull"} {
		if !strings.Contains(view, want) {
			t.Errorf("wide render should include shortcut pane text %q", want)
		}
	}
}

func TestRender_WideLayoutReplacesFooterHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 1,
	})

	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if strings.Contains(footer, "tab: pane") || strings.Contains(footer, "q/esc: quit") {
		t.Fatalf("wide render footer should not carry shortcut hints, got %q", footer)
	}
	if !strings.Contains(shortcutPaneText(view), "bksp   pane") {
		t.Fatal("wide right-pane render should expose backspace pane shortcut")
	}
}

func TestRender_NarrowLayoutKeepsFooterHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   18,
		Mode:     ModeWorktrees,
	})

	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "tab: pane") {
		t.Fatalf("narrow render should expose tab pane hint, got %q", footer)
	}
	if strings.Contains(view, "Shortcuts") {
		t.Fatal("narrow render should not reserve a shortcut pane")
	}
}

func TestRender_NarrowRightPaneLayoutKeepsBackspaceAndQuitFooterHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      80,
		Height:     18,
		Mode:       ModeBranches,
		ActivePane: 1,
	})

	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	for _, want := range []string{"bksp: pane", "q/esc: quit"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("narrow right-pane footer should expose %q, got %q", want, footer)
		}
	}
	if strings.Contains(footer, "⌫") {
		t.Fatalf("narrow right-pane footer should use bksp label, got %q", footer)
	}
}

func TestRender_ShortWideLayoutKeepsClippedShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   12,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		FetchAvailable:   true,
		PullAvailable:    true,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide but short render should keep a clipped shortcut pane")
	}
	if !strings.Contains(view, "new worktree") {
		t.Fatalf("short wide shortcut pane should keep high-priority actions, got:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	for _, forbidden := range []string{"tab: pane", "n: new worktree", "F: pull", "c: code"} {
		if strings.Contains(footer, forbidden) {
			t.Fatalf("short wide footer should not carry shortcut hint %q, got %q", forbidden, footer)
		}
	}
}

func TestRender_ShortcutPaneUsesUsableHeightGate(t *testing.T) {
	params := RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
	}

	params.Height = 5 // two usable pane rows after borders/status.
	if view := Render(params); strings.Contains(view, "Shortcuts") {
		t.Fatalf("shortcut pane should stay hidden below three usable rows, got:\n%s", view)
	}

	params.Height = 6 // three usable pane rows after borders/status.
	view := Render(params)
	if !strings.Contains(view, "Shortcuts") || !strings.Contains(shortcutPaneText(view), shortcutOverflowMarker) {
		t.Fatalf("shortcut pane should render and clip at three usable rows, got:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != params.Height {
		t.Fatalf("shortcut pane render should not exceed terminal height, got %d lines want %d:\n%s", got, params.Height, view)
	}
}

func TestRenderShortcutPane_ClipsOverflowAtEdgeHeights(t *testing.T) {
	sp := statusBarParams{
		Mode:                      ModeWorktrees,
		ActivePane:                1,
		RepoSelected:              true,
		WorktreeSelected:          true,
		WorktreeOpenableSelected:  true,
		WorktreeDeletableSelected: true,
		FetchAvailable:            true,
		PullAvailable:             true,
	}

	if got := ansi.Strip(renderShortcutPane(sp, 26, 1)); strings.TrimSpace(got) != shortcutOverflowMarker {
		t.Fatalf("height 1 should render only overflow marker, got %q", got)
	}
	if got := ansi.Strip(renderShortcutPane(sp, 26, 2)); !strings.Contains(got, "Shortcuts") || !strings.Contains(got, shortcutOverflowMarker) {
		t.Fatalf("height 2 should render title plus overflow marker, got %q", got)
	}
	if got := ansi.Strip(renderShortcutPane(sp, 26, 3)); !strings.Contains(got, "D      destructive mode") || !strings.Contains(got, shortcutOverflowMarker) {
		t.Fatalf("height 3 should render first shortcut plus overflow marker, got %q", got)
	}
}

func TestRender_ShortcutPanePrioritizesActions(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   18,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		FetchAvailable:   true,
		PullAvailable:    true,
	})
	pane := shortcutPaneText(view)
	actions := strings.Index(pane, "Actions")
	navigate := strings.Index(pane, "Navigate")
	global := strings.Index(pane, "Global")
	newWorktree := strings.Index(pane, "new worktree")
	globalHint := strings.Index(pane, "A      set agent")
	if actions < 0 || navigate < 0 || global < 0 || !(actions < navigate && navigate < global) {
		t.Fatalf("shortcut pane should order Actions, Navigate, Global, got:\n%s", pane)
	}
	if newWorktree < 0 || globalHint < 0 || newWorktree > globalHint {
		t.Fatalf("shortcut pane should show contextual actions before global hints, got:\n%s", pane)
	}
}

func TestRender_ShortcutPaneSeparatesSectionsWithBlankRows(t *testing.T) {
	sp := statusBarParams{
		Mode:                     ModeWorktrees,
		ActivePane:               1,
		RepoSelected:             true,
		WorktreeSelected:         true,
		WorktreeOpenableSelected: true,
		FetchAvailable:           true,
		PullAvailable:            true,
	}

	lines := strings.Split(ansi.Strip(renderShortcutPane(sp, 26, 18)), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Actions") {
			if i == 0 || strings.TrimSpace(lines[i-1]) != "" {
				t.Fatalf("shortcut pane should leave a blank row below the title, got:\n%s", strings.Join(lines, "\n"))
			}
			break
		}
	}
	for i, line := range lines {
		if strings.Contains(line, "Navigate") {
			if i == 0 || strings.TrimSpace(lines[i-1]) != "" {
				t.Fatalf("shortcut pane should leave a blank row before Navigate, got:\n%s", strings.Join(lines, "\n"))
			}
			return
		}
	}
	t.Fatalf("shortcut pane should include Navigate section, got:\n%s", strings.Join(lines, "\n"))
}

func TestRender_ShortcutPaneStylesModeTitleSeparately(t *testing.T) {
	pane := renderShortcutPane(statusBarParams{Mode: ModeWorktrees}, 26, 6)
	want := shortcutTitleStyle.Render("Shortcuts") + "  " + shortcutModeStyle.Render("Worktrees")
	if !strings.Contains(pane, want) {
		t.Fatalf("shortcut pane title should style mode separately, got %q want fragment %q", pane, want)
	}
}

func TestRender_StatusBarShowsGlobalRefreshShortcut(t *testing.T) {
	bar := ansi.Strip(RenderStatusBar(180, ModeFlows, OverlayNone, 1, false, false, false))
	if !strings.Contains(bar, "f5: refresh") {
		t.Fatalf("status bar should expose refresh shortcut, got %q", bar)
	}
}

func TestRender_ShortcutPaneShowsGlobalRefreshShortcut(t *testing.T) {
	pane := shortcutPaneText(renderShortcutPane(statusBarParams{Mode: ModeWorktrees}, 26, 20))
	if !strings.Contains(pane, "f5     refresh") {
		t.Fatalf("shortcut pane should expose refresh shortcut, got:\n%s", pane)
	}
}

func TestRender_ShortcutPaneShowsPromptTemplatesInGlobalSection(t *testing.T) {
	pane := shortcutPaneText(renderShortcutPane(statusBarParams{Mode: ModeWorktrees}, 26, 20))
	global := strings.Index(pane, "Global")
	promptTemplates := strings.Index(pane, "f2     edit prompts")
	if global < 0 || promptTemplates < 0 || promptTemplates < global {
		t.Fatalf("shortcut pane should expose prompt templates in Global section, got:\n%s", pane)
	}
}

func TestSidebarShortcutHintsGroupsOnlyAdjacentPairs(t *testing.T) {
	grouped := sidebarShortcutHints([]shortcutHint{
		{Key: "f", Label: "fetch"},
		{Key: "F", Label: "pull"},
		{Key: "t", Label: "terminal"},
		{Key: "x", Label: "extra"},
		{Key: "c", Label: "code"},
	})

	want := []shortcutHint{
		{Key: "f/F", Label: "fetch / pull"},
		{Key: "t", Label: "terminal"},
		{Key: "x", Label: "extra"},
		{Key: "c", Label: "code"},
	}
	if fmt.Sprint(grouped) != fmt.Sprint(want) {
		t.Fatalf("sidebar grouping should only combine adjacent key pairs, got %#v want %#v", grouped, want)
	}
}

func TestRender_ShortcutPaneGroupsRelatedRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 140,
		Height:                18,
		Mode:                  ModeWorktrees,
		Worktrees:             []gitquery.Worktree{{Path: "/a-worktrees/feat", BranchName: "feat"}},
		WorktreeSelected:      0,
		ActivePane:            1,
		FetchAvailable:        true,
		PullAvailable:         true,
		WorktreeMoveAvailable: true,
	})
	lines := shortcutPaneLines(view)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"f/F    fetch / pull", "t/c    terminal / code"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("shortcut pane should include grouped row %q, got:\n%s", want, joined)
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "f      fetch") || strings.HasPrefix(trimmed, "F      pull") ||
			strings.HasPrefix(trimmed, "t      terminal") || strings.HasPrefix(trimmed, "c      code") {
			t.Fatalf("shortcut pane should not include ungrouped related row %q:\n%s", line, joined)
		}
	}
}

func TestRender_ShortcutPaneAlignsLabels(t *testing.T) {
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:       0,
		Width:          140,
		Height:         18,
		Mode:           ModeWorktrees,
		Worktrees:      []gitquery.Worktree{{Path: "/a-worktrees/feat", BranchName: "feat"}},
		ActivePane:     1,
		FetchAvailable: true,
		PullAvailable:  true,
	})
	pane := shortcutPaneText(view)
	rows := []struct {
		key   string
		label string
	}{
		{key: "n", label: "new worktree"},
		{key: "P", label: "PR"},
		{key: "f/F", label: "fetch / pull"},
		{key: "t/c", label: "terminal / code"},
	}

	labelColumn := -1
	for _, row := range rows {
		line := lineContaining(pane, row.label)
		if line == "" || !strings.HasPrefix(strings.TrimSpace(line), row.key) {
			t.Fatalf("missing shortcut row %s %s in:\n%s", row.key, row.label, pane)
		}
		column := strings.Index(line, row.label)
		if labelColumn == -1 {
			labelColumn = column
			continue
		}
		if column != labelColumn {
			t.Fatalf("label %q starts at column %d, want %d in:\n%s", row.label, column, labelColumn, pane)
		}
	}
}

func TestRender_ShortBranchPaneClipsLegendAfterActions(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   12,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "feature", HasUpstream: false, Dirty: true, IsWorktree: true}, WorktreePath: "/a-feature"},
		},
		BranchSelected: 0,
		ActivePane:     1,
		Destructive:    true,
		FetchAvailable: true,
		PullAvailable:  true,
	})
	pane := shortcutPaneText(view)
	for _, want := range []string{"Actions", "n      new branch", "enter  diff", shortcutOverflowMarker} {
		if !strings.Contains(pane, want) {
			t.Fatalf("short branch shortcut pane should keep action %q, got:\n%s", want, pane)
		}
	}
	if strings.Contains(pane, "Legend") {
		t.Fatalf("short branch shortcut pane should clip lower-priority legend first, got:\n%s", pane)
	}
}

func TestRender_ShortSessionPaneKeepsSelectedSessionActions(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    120,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:  sessions.ProviderCodex,
			SessionID: "codex-session-1",
			Status:    "ended",
			RepoPath:  "/dev/wtui",
		}},
		ActivePane:      1,
		SessionSelected: 0,
	})
	pane := shortcutPaneText(view)
	for _, want := range []string{"o      transcript", "r      resume", "s      summary", "y      copy id", shortcutOverflowMarker} {
		if !strings.Contains(pane, want) {
			t.Fatalf("short session shortcut pane should keep selected session action %q, got:\n%s", want, pane)
		}
	}
}

func TestRender_ShortLeftPaneOmitsRightPaneActions(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 120,
		Height:                8,
		Mode:                  ModeHistory,
		ActivePane:            0,
		FetchVisibleAvailable: true,
	})
	pane := shortcutPaneText(view)
	if !strings.Contains(pane, "f      fetch visible") {
		t.Fatalf("short left-pane shortcut pane should keep left-pane action, got:\n%s", pane)
	}
	for _, forbidden := range []string{"enter  diff", "y      copy hash", "t/c    terminal / code"} {
		if strings.Contains(pane, forbidden) {
			t.Fatalf("short left-pane shortcut pane should omit right-pane action %q, got:\n%s", forbidden, pane)
		}
	}
}

func TestRender_HistoryShortcutPaneOmitsDestructiveMode(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeHistory,
		ActivePane: 1,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("history wide render should use shortcut pane")
	}
	if strings.Contains(view, "D: destructive mode") {
		t.Fatal("history shortcut pane should not advertise destructive mode")
	}
}

func TestRender_LeftPaneShortcutPaneOmitsModeNumberHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 0,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide render should use shortcut pane")
	}
	if strings.Contains(view, "1-5: switch views") {
		t.Fatal("left-pane shortcut pane should not advertise right-pane-only mode number keys")
	}
}

func TestRender_ShortcutPaneShowsArrowViewHintOnlyForRightPane(t *testing.T) {
	leftPane := shortcutPaneText(renderShortcutPane(statusBarParams{
		Mode:       ModeFlows,
		ActivePane: 0,
	}, 26, 18))
	if !strings.Contains(leftPane, "tab") || !strings.Contains(leftPane, "pane") {
		t.Fatalf("left-pane shortcut pane should include tab pane hint, got:\n%s", leftPane)
	}
	if strings.Contains(leftPane, "←/→") || strings.Contains(leftPane, "pane/view") {
		t.Fatalf("left-pane shortcut pane should not include arrow view hint, got:\n%s", leftPane)
	}

	rightPane := shortcutPaneText(renderShortcutPane(statusBarParams{
		Mode:       ModeFlows,
		ActivePane: 1,
	}, 26, 18))
	if !strings.Contains(rightPane, "bksp") || !strings.Contains(rightPane, "pane") {
		t.Fatalf("right-pane shortcut pane should include bksp pane hint, got:\n%s", rightPane)
	}
	if !strings.Contains(rightPane, "←/→") || !strings.Contains(rightPane, "view") {
		t.Fatalf("right-pane shortcut pane should include arrow view hint, got:\n%s", rightPane)
	}
	if strings.Contains(rightPane, "pane/view") || strings.Contains(rightPane, "select/pane/view") {
		t.Fatalf("right-pane shortcut pane should not describe arrows as pane navigation, got:\n%s", rightPane)
	}
}

func TestRender_BranchShortcutPaneKeepsLegend(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   26,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "feature", HasUpstream: false, Dirty: true, IsWorktree: true}, WorktreePath: "/a-feature"},
		},
		BranchSelected: 0,
		ActivePane:     1,
		Destructive:    true,
		FetchAvailable: true,
		PullAvailable:  true,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("branch wide render should use shortcut pane when the full shortcut list fits")
	}
	pane := shortcutPaneText(view)
	for _, want := range []string{"Legend", "clean", "ahead/behind", "dirty", "no upstream", "merged", "d      delete", "f/F    fetch / pull"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("branch shortcut pane should include %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(pane, "merged merged") {
		t.Fatalf("branch shortcut pane should render merged legend once, got:\n%s", pane)
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if strings.Contains(footer, "tab: pane") || strings.Contains(footer, "no upstream") {
		t.Fatalf("branch wide footer should not duplicate shortcut or legend hints, got %q", footer)
	}
}

func TestRender_SearchActiveSuppressesShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:        []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:     0,
		Width:        120,
		Height:       18,
		Mode:         ModeWorktrees,
		ActivePane:   1,
		SearchActive: true,
		ItemSearch:   "feat",
	})

	if strings.Contains(view, "Shortcuts") {
		t.Fatal("active search should suppress normal shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "/ items: feat") || !strings.Contains(footer, "enter: keep") {
		t.Fatalf("active search footer should show search controls, got %q", footer)
	}
}

func TestRender_FilteredItemStateKeepsShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 1,
		ItemSearch: "feat",
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("filtered item state should keep normal shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "filtered items: feat") || !strings.Contains(footer, "esc: clear") {
		t.Fatalf("filtered footer should show filter controls, got %q", footer)
	}
}

func TestRender_FilteredRepoStateKeepsShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 0,
		RepoSearch: "alp",
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("filtered repo state should keep normal shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "filtered repos: alp") || !strings.Contains(footer, "esc: clear") {
		t.Fatalf("filtered repo footer should show filter controls, got %q", footer)
	}
}

func TestRender_LeftPaneOmitsRightPaneActions(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		forbidden []string
	}{
		{name: "stashes", mode: ModeStashes, forbidden: []string{"enter: diff", "d: drop"}},
		{name: "history", mode: ModeHistory, forbidden: []string{"enter: diff", "y: copy hash", "t: terminal", "c: code"}},
		{name: "reflog", mode: ModeReflog, forbidden: []string{"enter: diff", "y: copy hash"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:       []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:    0,
				Width:       120,
				Height:      18,
				Mode:        tt.mode,
				ActivePane:  0,
				Destructive: true,
			})

			if !strings.Contains(view, "Shortcuts") {
				t.Fatal("wide left-pane render should still show global/navigation shortcuts")
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(view, forbidden) {
					t.Fatalf("left-pane %s render should not advertise %q, got:\n%s", tt.name, forbidden, view)
				}
			}
		})
	}
}

func TestRender_WorktreeRootOmitsDeleteHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   18,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		Destructive:      true,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide worktree render should use shortcut pane")
	}
	if strings.Contains(shortcutPaneText(view), "d      delete") {
		t.Fatalf("root worktree should not advertise delete, got:\n%s", view)
	}
}

func TestRender_WorktreeMoveHintAvailability(t *testing.T) {
	tests := []struct {
		name     string
		worktree gitquery.Worktree
		canMove  bool
		wantMove bool
	}{
		{
			name:     "movable linked worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat"},
			canMove:  true,
			wantMove: true,
		},
		{
			name:     "dirty linked worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat", Dirty: true},
			canMove:  true,
			wantMove: true,
		},
		{
			name:     "main worktree",
			worktree: gitquery.Worktree{Path: "/a", BranchName: "main", IsMain: true},
		},
		{
			name:     "stale worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat", Stale: true},
		},
		{
			name:     "locked worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat", Locked: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:              0,
				Width:                 120,
				Height:                18,
				Mode:                  ModeWorktrees,
				Worktrees:             []gitquery.Worktree{tt.worktree},
				WorktreeSelected:      0,
				ActivePane:            1,
				WorktreeMoveAvailable: tt.canMove,
			})
			hasMove := strings.Contains(shortcutPaneText(view), "m      move")
			if hasMove != tt.wantMove {
				t.Fatalf("move hint visibility mismatch, want %v got %v:\n%s", tt.wantMove, hasMove, view)
			}
		})
	}
}

func TestRender_NarrowWorktreeFooterIncludesMoveWhenAvailable(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 100,
		Height:                18,
		Mode:                  ModeWorktrees,
		Worktrees:             []gitquery.Worktree{{Path: "/a-worktrees/feat", BranchName: "feat"}},
		WorktreeSelected:      0,
		ActivePane:            1,
		WorktreeMoveAvailable: true,
	})
	if strings.Contains(view, "Shortcuts") {
		t.Fatal("narrow render should keep shortcuts in footer")
	}
	footer := strings.Split(view, "\n")[len(strings.Split(view, "\n"))-1]
	if !strings.Contains(footer, "m: move") {
		t.Fatalf("narrow footer should include move hint, got %q", footer)
	}
}

func TestRender_BranchShortcutPaneShowsDiffButOmitsRootDelete(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   24,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "main", HasUpstream: true, Dirty: true, IsWorktree: true}, WorktreePath: "/a"},
		},
		BranchSelected: 0,
		ActivePane:     1,
		Destructive:    true,
	})

	if !strings.Contains(shortcutPaneText(view), "enter  diff") {
		t.Fatalf("dirty branch worktree should advertise diff action, got:\n%s", view)
	}
	if strings.Contains(shortcutPaneText(view), "d      delete") {
		t.Fatalf("root branch should not advertise delete action, got:\n%s", view)
	}
}

func TestRender_BranchWithoutWorktreeOmitsOpenHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   24,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "feature", HasUpstream: true}},
		},
		BranchSelected: 0,
		ActivePane:     1,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide branch render should use shortcut pane")
	}
	for _, forbidden := range []string{"t/c    terminal / code", "t      terminal", "c      code"} {
		if strings.Contains(shortcutPaneText(view), forbidden) {
			t.Fatalf("non-worktree branch should not advertise %q, got:\n%s", forbidden, view)
		}
	}
}

func TestRender_EmptyItemPanesOmitItemActions(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		forbidden []string
	}{
		{name: "stashes", mode: ModeStashes, forbidden: []string{"enter: diff", "d: drop"}},
		{name: "history", mode: ModeHistory, forbidden: []string{"enter: diff", "y: copy hash"}},
		{name: "reflog", mode: ModeReflog, forbidden: []string{"enter: diff", "y: copy hash"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:       []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:    0,
				Width:       120,
				Height:      18,
				Mode:        tt.mode,
				ActivePane:  1,
				Destructive: true,
			})

			if !strings.Contains(view, "Shortcuts") {
				t.Fatal("wide empty-pane render should still show global/navigation shortcuts")
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(view, forbidden) {
					t.Fatalf("empty %s render should not advertise %q, got:\n%s", tt.name, forbidden, view)
				}
			}
		})
	}
}

func TestRender_NonWorktreeModesShowSyncHintsWhenAvailable(t *testing.T) {
	tests := []struct {
		name   string
		params RenderParams
	}{
		{
			name: "stashes",
			params: RenderParams{
				Mode:          ModeStashes,
				Stashes:       []gitquery.Stash{{Index: 0, Date: "2026-01-01", Message: "wip"}},
				StashSelected: 0,
			},
		},
		{
			name: "history",
			params: RenderParams{
				Mode:           ModeHistory,
				Commits:        []gitquery.Commit{{Hash: "abc1234", Author: "alice", Date: "today", Subject: "commit"}},
				CommitSelected: 0,
			},
		},
		{
			name: "reflog",
			params: RenderParams{
				Mode:           ModeReflog,
				Reflogs:        []gitquery.ReflogEntry{{Hash: "abc1234", Selector: "HEAD@{0}", Date: "today", Subject: "checkout"}},
				ReflogSelected: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := tt.params
			params.Repos = []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}
			params.Selected = 0
			params.Width = 120
			params.Height = 18
			params.ActivePane = 1
			params.FetchAvailable = true
			params.PullAvailable = true

			view := Render(params)
			if !strings.Contains(view, "Shortcuts") {
				t.Fatal("wide render should use shortcut pane")
			}
			pane := shortcutPaneText(view)
			for _, want := range []string{"f/F    fetch / pull"} {
				if !strings.Contains(pane, want) {
					t.Fatalf("%s render should include %q when available, got:\n%s", tt.name, want, view)
				}
			}
		})
	}
}

func TestRepoList_ScrollsWhenSelectionExceedsHeight(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/a", DisplayName: "alpha"},
		{Path: "/b", DisplayName: "bravo"},
		{Path: "/c", DisplayName: "charlie"},
		{Path: "/d", DisplayName: "delta"},
		{Path: "/e", DisplayName: "echo"},
	}
	// Height of 3 means only 3 visible at a time; scroll=2 shows repos 2-4
	lines := renderRepoList(repos, 4, 2, LeftPaneWidth-2, 3, "", nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "echo") {
		t.Error("selected item 'echo' should be visible")
	}
	if strings.Contains(joined, "alpha") {
		t.Error("'alpha' should be scrolled off the top")
	}
}

func TestRepoList_TruncatesLongNames(t *testing.T) {
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{
		{Path: "/a", DisplayName: "this-is-a-very-long-repository-name-that-exceeds-width"},
	}
	lines := renderRepoList(repos, 0, 0, width, 3, "", nil)
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			t.Errorf("line %d width %d exceeds pane width %d", i, lipgloss.Width(line), width)
		}
	}
}

func TestRepoList_RendersActiveTerminalMarkersWithStableSpacing(t *testing.T) {
	forceTrueColor(t)
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{
		{Path: "/alpha", DisplayName: "alpha"},
		{Path: "/bravo", DisplayName: "bravo"},
		{Path: "/charlie", DisplayName: "charlie"},
		{Path: "/delta", DisplayName: "delta"},
	}
	activeRepos := map[string]bool{
		"/alpha":   true,
		"/charlie": true,
	}

	lines := renderRepoList(repos, 0, 0, width, len(repos), "", activeRepos)
	stripped := stripLines(lines)

	wantPrefixes := []string{
		" > ● alpha",
		"     bravo",
		"   ● charlie",
		"     delta",
	}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(stripped[i], want) {
			t.Fatalf("line %d = %q, want prefix %q", i, stripped[i], want)
		}
	}
	for i, line := range lines {
		if i == 0 && !strings.Contains(line, selectedSegment(cleanStyle, "●")) {
			t.Fatalf("selected active line should render marker with selected clean style: %q", line)
		}
		if i == 2 && !strings.Contains(line, cleanStyle.Render("●")) {
			t.Fatalf("active line %d should render marker with clean style: %q", i, line)
		}
		if lipgloss.Width(line) != width {
			t.Fatalf("line %d width = %d, want %d: %q", i, lipgloss.Width(line), width, stripped[i])
		}
	}

	lines = renderRepoList(repos, 1, 0, width, len(repos), "", activeRepos)
	stripped = stripLines(lines)
	if !strings.HasPrefix(stripped[1], " >   bravo") {
		t.Fatalf("selected inactive line = %q, want prefix %q", stripped[1], " >   bravo")
	}
}

func TestRepoList_ActiveTerminalMarkerRestoresRowStyle(t *testing.T) {
	forceTrueColor(t)
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{
		{Path: "/alpha", DisplayName: "alpha"},
		{Path: "/bravo", DisplayName: "bravo"},
	}
	activeRepos := map[string]bool{"/alpha": true}

	selectedActive := renderRepoList(repos, 0, 0, width, len(repos), "", activeRepos)[0]
	if !strings.Contains(selectedActive, selectedSegment(cleanStyle, "●")) {
		t.Fatalf("selected active marker should keep marker color with selected row styling: %q", selectedActive)
	}
	if !strings.Contains(selectedActive, selectedStyle.Render("alpha")) {
		t.Fatalf("selected active row should restore selected styling after marker: %q", selectedActive)
	}

	unselectedActive := renderRepoList(repos, 1, 0, width, len(repos), "", activeRepos)[0]
	if !strings.Contains(unselectedActive, cleanStyle.Render("●")) {
		t.Fatalf("unselected active marker should keep marker color: %q", unselectedActive)
	}
	if !strings.Contains(unselectedActive, repoStyle.Render("alpha")) {
		t.Fatalf("unselected active row should restore repo styling after marker: %q", unselectedActive)
	}
}

func TestRepoList_ReservesActiveTerminalColumnWhenActiveRepoIsOffscreen(t *testing.T) {
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{
		{Path: "/active", DisplayName: "active"},
		{Path: "/bravo", DisplayName: "bravo"},
		{Path: "/charlie", DisplayName: "charlie"},
		{Path: "/delta", DisplayName: "delta"},
	}

	lines := renderRepoList(repos, 2, 1, width, 2, "", map[string]bool{"/active": true})
	stripped := stripLines(lines)

	if !strings.HasPrefix(stripped[0], "     bravo") {
		t.Fatalf("inactive row should reserve marker spacing for offscreen active repo, got %q", stripped[0])
	}
	if !strings.HasPrefix(stripped[1], " >   charlie") {
		t.Fatalf("selected inactive row should reserve marker spacing for offscreen active repo, got %q", stripped[1])
	}
}

func TestRepoList_ActiveTerminalMarkerDoesNotExpandTruncatedRows(t *testing.T) {
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{
		{Path: "/active", DisplayName: "active-repository-name-that-exceeds-the-pane-width"},
		{Path: "/inactive", DisplayName: "inactive-repository-name-that-exceeds-the-pane-width"},
	}

	lines := renderRepoList(repos, 0, 0, width, len(repos), "", map[string]bool{"/active": true})

	requireLinesWithinWidth(t, stripLines(lines), width)
	if got := lipgloss.Width(lines[0]); got != width {
		t.Fatalf("active row width = %d, want %d", got, width)
	}
	if got := lipgloss.Width(lines[1]); got != width {
		t.Fatalf("inactive row width = %d, want %d", got, width)
	}
}

func TestRepoList_ActiveTerminalMarkerMatchesCleanedRepoPath(t *testing.T) {
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{{Path: "/alpha/", DisplayName: "alpha"}}

	lines := renderRepoList(repos, 0, 0, width, len(repos), "", map[string]bool{"/alpha": true})

	if got := stripLines(lines)[0]; !strings.HasPrefix(got, " > ● alpha") {
		t.Fatalf("line = %q, want active marker for cleaned repo path", got)
	}
}

func TestStashPane_LongMessageAlwaysShowsTwoLines(t *testing.T) {
	width := 50
	longMsg := "this is a very long stash message that should wrap to a second line always"
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00", Message: longMsg},
	}
	// Not selected (selected=-1): should still show 2 lines for the long message
	lines := renderStashPane(stashes, -1, 0, width, 10)
	// Count non-empty lines
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty < 2 {
		t.Errorf("expected at least 2 non-empty lines for long stash message, got %d", nonEmpty)
	}
}

func TestStashPane_ShortMessageShowsOneLine(t *testing.T) {
	width := 50
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00", Message: "short"},
	}
	lines := renderStashPane(stashes, -1, 0, width, 10)
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Errorf("expected 1 non-empty line for short stash message, got %d", nonEmpty)
	}
}

func TestStashPane_SelectedLongMessageHighlightsBothLines(t *testing.T) {
	width := 50
	// Message wraps to 2 lines but the remainder is short, so selected
	// (padded to full width) and unselected (unpadded) must differ.
	longMsg := "this is a long stash message that wraps ok"
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00", Message: longMsg},
	}
	// Render with stash selected vs not selected
	selLines := renderStashPane(stashes, 0, 0, width, 10)
	unselLines := renderStashPane(stashes, -1, 0, width, 10)

	// The continuation line (index 1) should differ between selected and
	// unselected renders — stashSelStyle.Width(width) pads the selected
	// continuation to full width, while the unselected one is unpadded.
	if selLines[1] == unselLines[1] {
		t.Error("continuation line should be styled differently when stash is selected")
	}
}

func TestStashPane_ScrollOffset(t *testing.T) {
	width := 50
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18", Message: "first"},
		{Index: 1, Date: "2026-03-17", Message: "second"},
		{Index: 2, Date: "2026-03-16", Message: "third"},
	}
	// scroll=1 should skip the first stash line
	lines := renderStashPane(stashes, 1, 1, width, 3)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "first") {
		t.Error("'first' should be scrolled off the top")
	}
	if !strings.Contains(joined, "second") {
		t.Error("'second' should be visible")
	}
}

func TestBranchPane_CleanBranchShowsGreenCheck(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "main", HasUpstream: true, Ahead: 0, Behind: 0, Dirty: false}},
	}
	lines := renderBranchPane(rows, 50, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "main") {
		t.Error("should contain branch name 'main'")
	}
	if !strings.Contains(joined, "✔") {
		t.Error("clean branch with upstream should show ✔")
	}
	if strings.Contains(joined, "●") {
		t.Error("clean branch with upstream should not show ●")
	}
}

func TestBranchPane_AheadBehindShowsYellowDotWithCounts(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feature/auth", HasUpstream: true, Ahead: 3, Behind: 1, Dirty: false}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("ahead/behind branch should show ● indicator")
	}
	if !strings.Contains(joined, "+3/-1") {
		t.Error("should show ahead/behind counts as +3/-1")
	}
	if strings.Contains(joined, "✔") {
		t.Error("ahead/behind branch should not show ✔")
	}
}

func TestBranchPane_DirtyShowsRedDotWithFileStats(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feature/wip", HasUpstream: true, Dirty: true, IsWorktree: true,
			FilesChanged: 3, LinesAdded: 10, LinesDeleted: 5}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("dirty branch should show ● indicator")
	}
	if !strings.Contains(joined, "3 files") {
		t.Error("dirty branch should show file count")
	}
	if !strings.Contains(joined, "+10") {
		t.Error("dirty branch should show lines added")
	}
	if !strings.Contains(joined, "-5") {
		t.Error("dirty branch should show lines deleted")
	}
}

func TestBranchPane_NoUpstreamShowsPurpleDot(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "local-only", HasUpstream: false}},
	}
	lines := renderBranchPane(rows, 50, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("no-upstream branch should show ● indicator")
	}
	if strings.Contains(joined, "✔") {
		t.Error("no-upstream branch should not show ✔")
	}
}

func TestBranchPane_UpstreamGoneShowsPurpleDot(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "stale", HasUpstream: true, UpstreamGone: true}},
	}
	lines := renderBranchPane(rows, 50, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("upstream-gone branch should show ● indicator")
	}
	if strings.Contains(joined, "✔") {
		t.Error("upstream-gone branch should not show ✔")
	}
}

func TestBranchPane_StacksAheadAndDirtyIndicators(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 2, Behind: 0, Dirty: true, IsWorktree: true,
			FilesChanged: 1, LinesAdded: 5, LinesDeleted: 2}},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	// Should have both +2/-0 (ahead) and 1 files (dirty)
	if !strings.Contains(joined, "+2/-0") {
		t.Error("stacked: should show ahead/behind counts")
	}
	if !strings.Contains(joined, "1 files") {
		t.Error("stacked: should show dirty file count")
	}
	// Should have two ● indicators
	if strings.Count(joined, "●") < 2 {
		t.Errorf("stacked: expected at least 2 dot indicators, got %d", strings.Count(joined, "●"))
	}
	if strings.Contains(joined, "✔") {
		t.Error("stacked: should not show ✔ when there are indicators")
	}
}

func TestBranchPane_WorktreeAnnotation(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/proj-feat"},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[/dev/proj-feat]") {
		t.Error("worktree branch should show [<path>] annotation")
	}
}

func TestBranchPane_DuplicateWorktreeAnnotation(t *testing.T) {
	b := gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: true}
	rows := []gitquery.BranchRow{
		{Branch: b, WorktreePath: "/dev/proj-feat"},
		{Branch: b, WorktreePath: "/tmp/proj-feat-copy", IsExpansion: true},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/dev/proj-feat") || !strings.Contains(joined, "/tmp/proj-feat-copy") {
		t.Error("duplicate worktree branch should show both paths")
	}
	if strings.Contains(joined, "duplicate") || strings.Contains(joined, "wt:") {
		t.Error("duplicate worktree branch should not show labels")
	}
}

func TestBranchPane_DetachedWorktreeRow(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "(detached)", IsWorktree: true}, WorktreePath: "/tmp/wt-detached"},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(detached)") {
		t.Error("detached worktree should render as a detached row")
	}
	if !strings.Contains(joined, "[/tmp/wt-detached]") {
		t.Error("detached worktree should show its path annotation")
	}
}

func TestBranchPane_RootAnnotationUsesBlueStyle(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "main", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/alpha"},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	blueRoot := rootStyle.Render("[root]")
	if !strings.Contains(joined, blueRoot) {
		t.Error("root label in branch pane should use blue rootStyle")
	}
}

func TestBranchPane_SelectedRowPreservesSemanticIndicatorStyles(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 2, Dirty: true, IsWorktree: true,
			FilesChanged: 1, LinesAdded: 5, LinesDeleted: 2}},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "")
	joined := strings.Join(lines, "\n")
	for _, styled := range []string{
		selectedSegment(aheadBehindStyle, " ●"),
		selectedStyle.Render(" +2/-0"),
		selectedSegment(dirtyRedStyle, " ●"),
		selectedStyle.Render(" 1 files "),
		selectedSegment(diffAddStyle, "+5"),
		selectedSegment(diffDelStyle, "-2"),
	} {
		if !strings.Contains(joined, styled) {
			t.Fatalf("selected branch row should preserve semantic style %q in:\n%s", styled, joined)
		}
	}
}

func TestBranchPane_NonWorktreeNoAnnotation(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: false}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "[wt:") || strings.Contains(joined, "[duplicate:") {
		t.Error("non-worktree branch should not show worktree annotation")
	}
}

func TestRender_HighlightsSelectedBranch(t *testing.T) {
	// BranchSelected: 0 highlights first branch (clean), not the dirty one
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "clean"}},
			{Branch: gitquery.Branch{Name: "dirty", IsWorktree: true, Dirty: true}, WorktreePath: "/a"},
		},
		BranchSelected: 0,
		ActivePane:     1,
	})
	if !strings.Contains(view, "> clean") {
		t.Error("first branch should be highlighted when BranchSelected=0")
	}
	if strings.Contains(view, "> dirty") {
		t.Error("dirty branch should not be highlighted when BranchSelected=0")
	}
}

func TestRender_HighlightsSecondBranch(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "clean"}},
			{Branch: gitquery.Branch{Name: "dirty", IsWorktree: true, Dirty: true}, WorktreePath: "/a"},
		},
		BranchSelected: 1,
		ActivePane:     1,
	})
	if !strings.Contains(view, "> dirty") {
		t.Error("dirty branch should be highlighted when BranchSelected=1")
	}
	if strings.Contains(view, "> clean") {
		t.Error("clean branch should not be highlighted when BranchSelected=1")
	}
}

func TestRender_HidesCursorWhenLeftPaneActive(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "main"}},
		},
		BranchSelected: 0,
		ActivePane:     0,
	})
	if strings.Contains(view, "> main") {
		t.Error("branch cursor should be hidden when left pane is active")
	}
}

func TestBranchPane_CursorDoesNotShiftBranchName(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "first", HasUpstream: true}},
		{Branch: gitquery.Branch{Name: "second", HasUpstream: true}},
	}
	// Render with no selection (selected = -1)
	unselected := renderBranchPaneSelected(rows, -1, 0, 80, 10, "/dev/alpha")
	// Render with first selected
	selected := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")

	// Find position of "first" in both renders — should be at the same column
	unselIdx := strings.Index(unselected[0], "first")
	selIdx := strings.Index(selected[0], "first")
	if unselIdx == -1 || selIdx == -1 {
		t.Fatalf("branch name 'first' not found in output: unsel=%q sel=%q", unselected[0], selected[0])
	}
	if unselIdx != selIdx {
		t.Errorf("branch name shifts when selected: unselected col %d, selected col %d", unselIdx, selIdx)
	}
}

func TestBranchPane_UnpushedCommitsShown(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 2,
			Unpushed: []string{"abc1234 Fix bug", "def5678 Add feature"}}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Fix bug") {
		t.Error("should show unpushed commit message")
	}
	if !strings.Contains(joined, "Add feature") {
		t.Error("should show second unpushed commit message")
	}
}

func TestBranchPane_MergedBranchShowsCleanupCandidate(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "merged-feat", HasUpstream: true, Merged: true, MergedInto: "main"}},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "merged-feat") {
		t.Error("should show merged branch name")
	}
	if !strings.Contains(joined, "merged") {
		t.Errorf("should show merged cleanup indicator, got %q", joined)
	}
	if strings.Contains(joined, "✔") {
		t.Errorf("merged branch should not also render clean-only indicator, got %q", joined)
	}
}

func TestBranchPane_UnpushedCapsAt5WithOverflow(t *testing.T) {
	msgs := make([]string, 8)
	for i := range msgs {
		msgs[i] = fmt.Sprintf("abc%d commit message %d", i, i)
	}
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 8, Unpushed: msgs}},
	}
	lines := renderBranchPane(rows, 60, 20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "and 3 more") {
		t.Error("should show 'and 3 more' overflow for 8 commits with cap of 5")
	}
	// Count lines that contain commit content
	var commitLines int
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.Contains(trimmed, "commit message") || strings.Contains(trimmed, "and 3 more") {
			commitLines++
		}
	}
	if commitLines != 6 {
		t.Errorf("expected 6 commit-related lines (5 + overflow), got %d", commitLines)
	}
}

func TestBranchPane_ScrollsToSelectedBranch(t *testing.T) {
	rows := make([]gitquery.BranchRow, 10)
	for i := range rows {
		rows[i] = gitquery.BranchRow{Branch: gitquery.Branch{Name: fmt.Sprintf("branch-%d", i)}}
	}
	// BranchScroll=8 with height=3 means we see branches 8 and 9
	lines := renderBranchPaneSelected(rows, 9, 8, 60, 3, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "branch-9") {
		t.Error("should show branch-9 when scrolled to see it")
	}
	if strings.Contains(joined, "branch-0") {
		t.Error("branch-0 should be scrolled out of view")
	}
}

func TestRender_CombinesPanesWithDivider(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     1,
	})
	if !strings.Contains(view, "│") {
		t.Error("view should contain divider")
	}
	if !strings.Contains(view, "alpha") {
		t.Error("view should contain repo name")
	}
}

func TestRender_ConfirmDialogShowsPrompt(t *testing.T) {
	view := Render(RenderParams{
		Repos:         []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:         80,
		Height:        24,
		Mode:          1,
		Overlay:       OverlayConfirm,
		ConfirmPrompt: "Remove worktree /dev/alpha/feat? (y/n)",
	})
	if !strings.Contains(view, "Remove worktree /dev/alpha/feat") {
		t.Error("confirm dialog should show prompt text")
	}
	if !strings.Contains(view, "y/n") {
		t.Error("confirm dialog should show y/n hint")
	}
}

func TestRender_ForceConfirmDialogShowsPrompt(t *testing.T) {
	view := Render(RenderParams{
		Repos:         []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:         80,
		Height:        24,
		Mode:          1,
		Overlay:       OverlayConfirm,
		ConfirmPrompt: "Force delete /dev/alpha/feat? (y/n)",
		ConfirmForce:  true,
	})
	if !strings.Contains(view, "Force delete /dev/alpha/feat") {
		t.Error("force confirm dialog should show prompt text")
	}
}

func TestRender_WorktreeInputDialogShowsInputAndError(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            80,
		Height:           24,
		Mode:             1,
		Overlay:          OverlayInput,
		InputPrompt:      "Create worktree from",
		InputPlaceholder: WorktreeInputPlaceholder,
		InputValue:       "feature/new",
		InputCursor:      len([]rune("feature/new")),
		InputError:       "already exists",
	})
	if !strings.Contains(view, "Create worktree from: feature/new") {
		t.Error("worktree input dialog should show typed input")
	}
	if !strings.Contains(view, "already exists") {
		t.Error("worktree input dialog should show error")
	}
}

func TestRender_WorktreeInputDialogShowsPlaceholder(t *testing.T) {
	forceTrueColor(t)

	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            80,
		Height:           24,
		Mode:             1,
		Overlay:          OverlayInput,
		InputPrompt:      "Create worktree from",
		InputPlaceholder: WorktreeInputPlaceholder,
	})
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "Create worktree from: "+WorktreeInputPlaceholder) {
		t.Fatalf("worktree input dialog should show prompt and placeholder when input is empty:\n%s", stripped)
	}
	if !strings.Contains(view, placeholderStyle.Render(WorktreeInputPlaceholder)) {
		t.Fatalf("worktree input placeholder should use placeholder style:\n%q", view)
	}
	if !strings.Contains(view, activeModeStyle.Render("█")) {
		t.Fatalf("worktree input cursor should use active style:\n%q", view)
	}
	if strings.Contains(view, placeholderStyle.Render("Create worktree from: ")) {
		t.Fatalf("prompt label should not use placeholder style:\n%q", view)
	}
	if strings.Contains(view, placeholderStyle.Render("█")) {
		t.Fatalf("cursor should not use placeholder style:\n%q", view)
	}
}

func TestRender_InputDialogWrappedPlaceholderUsesPlaceholderStyle(t *testing.T) {
	forceTrueColor(t)

	placeholder := "alpha beta gamma delta epsilon zeta"
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            36,
		Height:           18,
		Mode:             ModeWorktrees,
		Overlay:          OverlayInput,
		InputPrompt:      "Create worktree from",
		InputPlaceholder: placeholder,
	})

	stripped := ansi.Strip(view)
	for _, want := range []string{
		"Create worktree from: alpha",
		"beta gamma delta epsilon",
		"zeta█",
	} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("wrapped placeholder missing %q:\n%s", want, stripped)
		}
	}
	for _, segment := range []string{"alpha", "beta gamma delta epsilon", "zeta"} {
		if !strings.Contains(view, placeholderStyle.Render(segment)) {
			t.Fatalf("wrapped placeholder segment %q should use placeholder style:\n%q", segment, view)
		}
	}
	if !strings.Contains(view, activeModeStyle.Render("█")) {
		t.Fatalf("wrapped placeholder cursor should use active style:\n%q", view)
	}
	if strings.Contains(view, placeholderStyle.Render("Create worktree from: ")) {
		t.Fatalf("wrapped prompt label should not use placeholder style:\n%q", view)
	}
	if strings.Contains(view, placeholderStyle.Render("█")) {
		t.Fatalf("wrapped cursor should not use placeholder style:\n%q", view)
	}
	requireLinesWithinWidth(t, strippedLines(view), 36)
}

func TestRender_InputDialogNonEmptyDoesNotRenderPlaceholderStyle(t *testing.T) {
	forceTrueColor(t)

	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            80,
		Height:           24,
		Mode:             ModeWorktrees,
		Overlay:          OverlayInput,
		InputPrompt:      "Create worktree from",
		InputPlaceholder: WorktreeInputPlaceholder,
		InputValue:       "feature/new",
		InputCursor:      len([]rune("feature/new")),
	})

	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "Create worktree from: feature/new") {
		t.Fatalf("input dialog should show typed input:\n%s", stripped)
	}
	if strings.Contains(stripped, WorktreeInputPlaceholder) {
		t.Fatalf("non-empty input should not render placeholder text:\n%s", stripped)
	}
	if strings.Contains(view, placeholderStyle.Render("feature/new")) {
		t.Fatalf("typed input should not use placeholder style:\n%q", view)
	}
}

func TestRender_InputDialogWrapsLongSingleLineAndShowsCursorInPlace(t *testing.T) {
	longWord := strings.Repeat("x", 90)
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       60,
		Height:      18,
		Mode:        ModeWorktrees,
		Overlay:     OverlayInput,
		InputPrompt: "New branch",
		InputValue:  "feature/" + longWord,
		InputCursor: len([]rune("feature/xxx")),
		InputMode:   InputSingleLine,
	})
	stripped := ansi.Strip(view)
	if strings.Contains(stripped, "feature/"+longWord) {
		t.Fatalf("long input should wrap instead of rendering on one line:\n%s", view)
	}
	if !strings.Contains(stripped, "feature/xxx█") {
		t.Fatalf("cursor should render at logical cursor position:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimLeft(ansi.Strip(line), " ")
		if trimmed == "" {
			continue
		}
		if got := lipgloss.Width(trimmed); got > 60 {
			t.Fatalf("modal line %d width = %d, want <= terminal width: %q", i, got, trimmed)
		}
	}
}

func TestRender_InputDialogPreservesMultiLineBreaksAndWrapsError(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            72,
		Height:           18,
		Mode:             ModePlans,
		Overlay:          OverlayInput,
		InputPrompt:      LaunchInstructionsPrompt,
		InputPlaceholder: "launch instructions",
		InputValue:       "first line\nsecond line with detail",
		InputCursor:      len([]rune("first line\nsecond")),
		InputMode:        InputMultiLine,
		InputError:       "this validation message is intentionally long enough to wrap inside the input modal",
	})
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "first line") || !strings.Contains(stripped, "second█ line with detail") {
		t.Fatalf("multi-line input should preserve breaks and cursor position:\n%s", view)
	}
	if strings.Contains(stripped, "this validation message is intentionally long enough to wrap inside the input modal") {
		t.Fatalf("validation error should wrap inside the modal:\n%s", view)
	}
	if !strings.Contains(stripped, "this validation message is") || !strings.Contains(stripped, "inside the input modal") {
		t.Fatalf("wrapped validation error missing expected text:\n%s", view)
	}
}

func TestRender_InputDialogPreservesEditableSpacing(t *testing.T) {
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       90,
		Height:      18,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: LaunchInstructionsPrompt,
		InputValue:  "first  line\n  - second  item",
		InputCursor: len([]rune("first  line\n  - second")),
		InputMode:   InputMultiLine,
	})
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "first  line") {
		t.Fatalf("input dialog collapsed repeated spaces on first line:\n%s", stripped)
	}
	if !strings.Contains(stripped, "  - second█  item") {
		t.Fatalf("input dialog should preserve leading and repeated spaces around cursor:\n%s", stripped)
	}
}

func TestRender_InputDialogOverflowKeepsCursorVisible(t *testing.T) {
	value := strings.Join([]string{
		"line one",
		"line two",
		"line three",
		"line four",
		"line five",
		"line six",
		"line seven",
		"target line",
	}, "\n")
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       64,
		Height:      10,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Instructions",
		InputValue:  value,
		InputCursor: len([]rune(value)) - len([]rune(" line")),
		InputMode:   InputMultiLine,
	})
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "target█ line") {
		t.Fatalf("overflow window should keep cursor line visible:\n%s", view)
	}
	if !strings.Contains(stripped, shortcutOverflowMarker) {
		t.Fatalf("overflowed input should show overflow marker:\n%s", view)
	}
}

func TestRender_InputDialogConfiguredHeightShowsMoreText(t *testing.T) {
	value := strings.Join([]string{
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
		"line 06",
		"line 07",
		"line 08",
		"line 09",
		"line 10",
		"line 11",
		"line 12",
	}, "\n")

	defaultView := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       72,
		Height:      22,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Edit Plan launch",
		InputValue:  value,
		InputCursor: len([]rune(value)),
		InputMode:   InputMultiLine,
	})
	tallView := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       72,
		Height:      22,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Edit Plan launch",
		InputValue:  value,
		InputCursor: len([]rune(value)),
		InputMode:   InputMultiLine,
		InputHeight: 16,
	})

	if strings.Contains(ansi.Strip(defaultView), "line 01") {
		t.Fatalf("default input height unexpectedly shows the first line:\n%s", ansi.Strip(defaultView))
	}
	strippedTall := ansi.Strip(tallView)
	for _, want := range []string{"line 01", "line 12█"} {
		if !strings.Contains(strippedTall, want) {
			t.Fatalf("configured input height should show %q:\n%s", want, strippedTall)
		}
	}
}

func TestRender_InputDialogTinyHeightKeepsCursorVisible(t *testing.T) {
	value := strings.Join([]string{
		"line one",
		"line two",
		"line three",
	}, "\n")
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       64,
		Height:      4,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Instructions",
		InputValue:  value,
		InputCursor: len([]rune("line one\nline")),
		InputMode:   InputMultiLine,
	})
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "line█ two") {
		t.Fatalf("tiny input dialog should keep the cursor line visible:\n%s", stripped)
	}
}

func TestRender_InputDialogTinyHeightWithErrorKeepsCursorVisible(t *testing.T) {
	value := strings.Join([]string{
		"line one",
		"line two",
		"line three",
	}, "\n")
	view := Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       64,
		Height:      4,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Instructions",
		InputValue:  value,
		InputCursor: len([]rune("line one\nline two\nline")),
		InputMode:   InputMultiLine,
		InputError:  "validation failed",
	})
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "line█ three") {
		t.Fatalf("tiny input dialog with error should keep the cursor line visible:\n%s", stripped)
	}
}

func TestRender_WorktreeMoveInputDialogShowsPromptAndPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            80,
		Height:           24,
		Mode:             1,
		Overlay:          OverlayInput,
		InputPrompt:      WorktreeMovePrompt,
		InputPlaceholder: WorktreeMoveInputPlaceholder,
	})
	if !strings.Contains(view, "Move worktree to:") {
		t.Error("move input dialog should show move prompt")
	}
	if !strings.Contains(view, WorktreeMoveInputPlaceholder) {
		t.Error("move input dialog should show move placeholder")
	}
}

func TestRender_BranchInputDialogShowsPromptAndPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            80,
		Height:           24,
		Mode:             2,
		Overlay:          OverlayInput,
		InputPrompt:      BranchPrompt,
		InputPlaceholder: BranchInputPlaceholder,
	})
	if !strings.Contains(view, "Create branch:") {
		t.Error("branch input dialog should show prompt")
	}
	if !strings.Contains(view, "branch name") {
		t.Error("branch input dialog should show placeholder")
	}
}

func TestRender_PullRequestWorktreeInputDialogShowsPromptAndPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            80,
		Height:           24,
		Mode:             1,
		Overlay:          OverlayInput,
		InputPrompt:      PRWorktreePrompt,
		InputPlaceholder: PRWorktreeInputPlaceholder,
	})
	if !strings.Contains(view, "Create PR worktree from:") {
		t.Error("PR input dialog should show prompt")
	}
	if !strings.Contains(view, "PR number or URL") {
		t.Error("PR input dialog should show placeholder")
	}
}

func TestRender_SelectDialogShowsPromptItemsAndSelection(t *testing.T) {
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:          80,
		Height:         24,
		Mode:           1,
		Overlay:        OverlaySelect,
		SelectPrompt:   "Choose interactive helper",
		SelectItems:    []SelectItem{{Label: "codex", Value: "codex"}, {Label: "codex-app", Value: "codex-app"}, {Label: "claude", Value: "claude"}},
		SelectSelected: 2,
	})
	stripped := ansi.Strip(view)
	for _, want := range []string{"Choose interactive helper", "codex", "codex-app", "claude"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("select dialog should show %q:\n%s", want, stripped)
		}
	}
	if !strings.Contains(stripped, "> claude") {
		t.Fatalf("select dialog should mark selected choice:\n%s", stripped)
	}
}

func TestRender_PromptTemplateSelectShowsPromptSpecificFooter(t *testing.T) {
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:          90,
		Height:         16,
		Mode:           ModeWorktrees,
		Overlay:        OverlaySelect,
		SelectPrompt:   "Prompt templates",
		SelectItems:    []SelectItem{{Label: "Plan launch     default", Value: "agent.plan_prompt"}},
		SelectSelected: 0,
	})

	stripped := ansi.Strip(view)
	for _, want := range []string{"enter: edit", "r: reset", "v: preview", "esc: cancel"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("prompt template select footer should contain %q:\n%s", want, stripped)
		}
	}
	if strings.Contains(stripped, "enter: confirm") {
		t.Fatalf("prompt template select footer should not use generic confirm copy:\n%s", stripped)
	}
}

func TestRender_SelectOverlayUsesBoundedPanelAndKeepsBaseVisible(t *testing.T) {
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Selected:         0,
		Width:            80,
		Height:           16,
		Mode:             ModeWorktrees,
		ActivePane:       1,
		Worktrees:        []gitquery.Worktree{{Path: "/dev/alpha-worktrees/feature", BranchName: "feature/picker"}},
		WorktreeSelected: 0,
		Overlay:          OverlaySelect,
		SelectPrompt:     "Choose helper",
		SelectItems:      []SelectItem{{Label: "codex", Value: "codex"}, {Label: "claude", Value: "claude"}},
		SelectWidth:      32,
		SelectHeight:     6,
		SelectPlacement:  SelectPlacementTopCenter,
	})

	bounds := requireSelectPanelBounds(t, view, "Choose helper")
	if bounds.width != 32 || bounds.height != 6 {
		t.Fatalf("select panel dimensions = %dx%d, want 32x6:\n%s", bounds.width, bounds.height, ansi.Strip(view))
	}
	if bounds.x != 24 || bounds.y != 0 {
		t.Fatalf("select panel position = (%d,%d), want (24,0):\n%s", bounds.x, bounds.y, ansi.Strip(view))
	}
	stripped := ansi.Strip(view)
	for _, want := range []string{"alpha", "alpha-worktrees/feature"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("compact select overlay should keep base UI text %q visible:\n%s", want, stripped)
		}
	}
	if !strings.Contains(stripped, "up/down select") || strings.Contains(stripped, "bksp") {
		t.Fatalf("select overlay status bar should use select hints only:\n%s", stripped)
	}
	if !strings.Contains(stripped, "enter: confirm") {
		t.Fatalf("generic select overlay should keep confirm footer copy:\n%s", stripped)
	}
	for _, notWant := range []string{"enter: edit", "r: reset", "v: preview"} {
		if strings.Contains(stripped, notWant) {
			t.Fatalf("generic select overlay should not show prompt-template hint %q:\n%s", notWant, stripped)
		}
	}
}

func TestRender_SelectOverlayAutoSizesFromPromptAndItems(t *testing.T) {
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:          40,
		Height:         10,
		Mode:           ModeWorktrees,
		Overlay:        OverlaySelect,
		SelectPrompt:   "Pick",
		SelectItems:    []SelectItem{{Label: "", Value: "fallback-value"}, {Label: "tiny", Value: "tiny"}},
		SelectSelected: 0,
	})

	bounds := requireSelectPanelBounds(t, view, "Pick")
	if bounds.width != len("fallback-value")+4 {
		t.Fatalf("auto select width = %d, want %d:\n%s", bounds.width, len("fallback-value")+4, ansi.Strip(view))
	}
	if bounds.height != 5 {
		t.Fatalf("auto select height = %d, want 5:\n%s", bounds.height, ansi.Strip(view))
	}
	if !strings.Contains(ansi.Strip(view), "> fallback-value") {
		t.Fatalf("empty select label should fall back to value:\n%s", ansi.Strip(view))
	}
}

func TestRender_SelectOverlayAutoWidthFitsLongestUnselectedItem(t *testing.T) {
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:          40,
		Height:         10,
		Mode:           ModeWorktrees,
		Overlay:        OverlaySelect,
		SelectPrompt:   "Pick",
		SelectItems:    []SelectItem{{Label: "tiny", Value: "tiny"}, {Label: "", Value: "fallback-value"}},
		SelectSelected: 0,
	})

	bounds := requireSelectPanelBounds(t, view, "Pick")
	if bounds.width != len("fallback-value")+4 {
		t.Fatalf("auto select width = %d, want %d:\n%s", bounds.width, len("fallback-value")+4, ansi.Strip(view))
	}
	if !strings.Contains(ansi.Strip(view), "fallback-value") {
		t.Fatalf("auto-sized select panel should not truncate longest unselected item:\n%s", ansi.Strip(view))
	}
}

func TestRender_SelectOverlayPlacementsUseTerminalBody(t *testing.T) {
	tests := []struct {
		name      string
		placement SelectPlacement
		wantY     int
	}{
		{name: "center", placement: SelectPlacementCenter, wantY: 3},
		{name: "top", placement: SelectPlacementTopCenter, wantY: 0},
		{name: "bottom", placement: SelectPlacementBottomCenter, wantY: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:           []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
				Width:           40,
				Height:          13,
				Mode:            ModeWorktrees,
				Overlay:         OverlaySelect,
				SelectPrompt:    "Pick",
				SelectItems:     []SelectItem{{Label: "one", Value: "1"}},
				SelectWidth:     20,
				SelectHeight:    5,
				SelectPlacement: tt.placement,
			})
			bounds := requireSelectPanelBounds(t, view, "Pick")
			if bounds.x != 10 || bounds.y != tt.wantY {
				t.Fatalf("select panel position = (%d,%d), want (10,%d):\n%s", bounds.x, bounds.y, tt.wantY, ansi.Strip(view))
			}
		})
	}
}

func TestRender_SelectOverlayPreservesStyledBaseRowsAroundPanel(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousDarkBackground)
	})

	view := Render(RenderParams{
		Repos:           []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Selected:        0,
		Width:           72,
		Height:          12,
		Mode:            ModeBranches,
		ActivePane:      1,
		Branches:        []gitquery.BranchRow{{Branch: gitquery.Branch{Name: "main", IsWorktree: true}, WorktreePath: "/dev/alpha"}},
		BranchSelected:  0,
		Overlay:         OverlaySelect,
		SelectPrompt:    "Pick",
		SelectItems:     []SelectItem{{Label: "codex", Value: "codex"}},
		SelectWidth:     24,
		SelectHeight:    4,
		SelectPlacement: SelectPlacementBottomCenter,
	})

	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected styled base rows to remain styled:\n%s", view)
	}
	strippedLines := strings.Split(ansi.Strip(view), "\n")
	for i, line := range strippedLines {
		if got := lipgloss.Width(line); got > 72 {
			t.Fatalf("visible line %d width = %d, want <= 72: %q", i, got, line)
		}
	}
	if !strings.Contains(strings.Join(strippedLines[:4], "\n"), "main") {
		t.Fatalf("styled base branch row above panel should remain visible:\n%s", strings.Join(strippedLines, "\n"))
	}
}

func TestRender_SelectOverlayShortHeightKeepsSelectedItemVisible(t *testing.T) {
	view := Render(RenderParams{
		Repos:          []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:          64,
		Height:         10,
		Mode:           ModeWorktrees,
		Overlay:        OverlaySelect,
		SelectPrompt:   "Pick",
		SelectItems:    []SelectItem{{Label: "first", Value: "1"}, {Label: "second", Value: "2"}, {Label: "third", Value: "3"}, {Label: "fourth", Value: "4"}, {Label: "fifth", Value: "5"}},
		SelectSelected: 4,
		SelectWidth:    20,
		SelectHeight:   5,
	})

	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "> fifth") {
		t.Fatalf("short select panel should keep selected item visible:\n%s", stripped)
	}
	if strings.Contains(stripped, "first") {
		t.Fatalf("short select panel should viewport past first item when selected is last:\n%s", stripped)
	}
}

func TestRender_SelectOverlayTinyTerminalsClampWithoutOverflow(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{
		{width: 1, height: 1},
		{width: 2, height: 2},
		{width: 3, height: 3},
		{width: 8, height: 2},
		{width: 8, height: 4},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			view := Render(RenderParams{
				Repos:          []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
				Width:          size.width,
				Height:         size.height,
				Mode:           ModeWorktrees,
				Overlay:        OverlaySelect,
				SelectPrompt:   "Pick a helper",
				SelectItems:    []SelectItem{{Label: "codex", Value: "codex"}},
				SelectSelected: 0,
				SelectWidth:    32,
				SelectHeight:   6,
			})
			lines := strings.Split(ansi.Strip(view), "\n")
			if len(lines) != size.height {
				t.Fatalf("line count = %d, want %d:\n%s", len(lines), size.height, ansi.Strip(view))
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got > size.width {
					t.Fatalf("line %d width = %d, want <= %d: %q", i, got, size.width, line)
				}
			}
		})
	}
}

type selectPanelBounds struct {
	x      int
	y      int
	width  int
	height int
}

func requireSelectPanelBounds(t *testing.T, view, prompt string) selectPanelBounds {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	var found bool
	var best selectPanelBounds
	for y, line := range lines {
		runes := []rune(line)
		for x, r := range runes {
			if r != '┌' {
				continue
			}
			for right := x + 1; right < len(runes); right++ {
				if runes[right] != '┐' {
					continue
				}
				for bottom := y + 1; bottom < len(lines); bottom++ {
					bottomRunes := []rune(lines[bottom])
					if x >= len(bottomRunes) || right >= len(bottomRunes) {
						continue
					}
					if bottomRunes[x] != '└' || bottomRunes[right] != '┘' {
						continue
					}
					candidate := selectPanelBounds{
						x:      x,
						y:      y,
						width:  right - x + 1,
						height: bottom - y + 1,
					}
					if !selectPanelCandidateContainsPrompt(lines, candidate, prompt) {
						continue
					}
					if !found || candidate.width < best.width {
						found = true
						best = candidate
					}
				}
			}
		}
	}
	if found {
		return best
	}
	t.Fatalf("select panel bounds not found:\n%s", ansi.Strip(view))
	return selectPanelBounds{}
}

func selectPanelCandidateContainsPrompt(lines []string, bounds selectPanelBounds, prompt string) bool {
	if prompt == "" {
		return true
	}
	for row := bounds.y; row < bounds.y+bounds.height && row < len(lines); row++ {
		runes := []rune(lines[row])
		if bounds.x >= len(runes) {
			continue
		}
		right := bounds.x + bounds.width
		if right > len(runes) {
			right = len(runes)
		}
		if strings.Contains(string(runes[bounds.x:right]), prompt) {
			return true
		}
	}
	return false
}

func TestRender_LaunchInstructionsInputDialogWrapsInCompactPanel(t *testing.T) {
	longInput := `Implement the saved wtui plan "Persist custom launch instructions" at /state/wtui/plans/plan-1/plan.md. Read the plan file, then begin implementation.`
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            120,
		Height:           24,
		Mode:             ModePlans,
		Overlay:          OverlayInput,
		InputPrompt:      LaunchInstructionsPrompt,
		InputPlaceholder: "launch instructions",
		InputValue:       longInput,
		InputCursor:      len([]rune(longInput)),
		InputMode:        InputMultiLine,
	})

	if !strings.Contains(view, LaunchInstructionsPrompt) {
		t.Fatalf("launch instructions dialog should show prompt:\n%s", view)
	}
	if strings.Contains(view, longInput) {
		t.Fatalf("launch instructions should wrap instead of rendering on one line:\n%s", view)
	}
	for _, want := range []string{"Implement the saved wtui plan", "Read the", "plan file", "then begin"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wrapped launch instructions missing %q:\n%s", want, view)
		}
	}

	lines := strings.Split(view, "\n")
	for i, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimLeft(ansi.Strip(line), " ")
		if trimmed == "" {
			continue
		}
		if got := lipgloss.Width(trimmed); got > launchInstructionsMaxWidth {
			t.Fatalf("modal line %d width = %d, want <= %d: %q", i, got, launchInstructionsMaxWidth, trimmed)
		}
	}
}

func TestRender_LaunchInstructionsInputDialogMarksOverflow(t *testing.T) {
	longInput := strings.Join([]string{
		"Start with the saved wtui plan title and repository path.",
		"Keep this middle instruction one.",
		"Keep this middle instruction two.",
		"Keep this middle instruction three.",
		"Keep this middle instruction four.",
		"Finish by launching the selected agent with the edited instructions.",
	}, " ")
	view := Render(RenderParams{
		Repos:            []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:            52,
		Height:           20,
		Mode:             ModePlans,
		Overlay:          OverlayInput,
		InputPrompt:      LaunchInstructionsPrompt,
		InputPlaceholder: "launch instructions",
		InputValue:       longInput,
		InputCursor:      len([]rune(longInput)),
		InputMode:        InputMultiLine,
	})

	for _, want := range []string{shortcutOverflowMarker, "Finish by launching"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overflowed launch instructions missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "middle instruction three") {
		t.Fatalf("overflowed launch instructions should compact middle content:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimLeft(ansi.Strip(line), " ")
		if trimmed == "" {
			continue
		}
		if got := lipgloss.Width(trimmed); got > 52 {
			t.Fatalf("modal line %d width = %d, want <= terminal width: %q", i, got, trimmed)
		}
	}
}

func TestStatusBar_StashesModeHintsSpacing(t *testing.T) {
	bar := RenderStatusBar(120, 3, 0, 1, true, false, false)
	for _, hint := range []string{"f: fetch", "F: pull"} {
		if strings.Contains(bar, hint) {
			t.Errorf("stashes mode status bar should not contain %q", hint)
		}
	}
	for _, pair := range [][2]string{
		{"bksp: pane", "q/esc: quit"},
		{"↑/↓ select", "←/→ view"},
		{"←/→ view", "enter: diff"},
		{"enter: diff", "d: drop"},
	} {
		a := strings.Index(bar, pair[0])
		b := strings.Index(bar, pair[1])
		if a == -1 || b == -1 {
			t.Errorf("expected both %q and %q in bar", pair[0], pair[1])
			continue
		}
		gap := bar[a+len(pair[0]) : b]
		if gap != "  " {
			t.Errorf("expected 2 spaces between %q and %q, got %q", pair[0], pair[1], gap)
		}
	}
}

func TestBranchPane_MultiWorktreeExpandsRows(t *testing.T) {
	b := gitquery.Branch{Name: "feat", HasUpstream: true, Unpushed: []string{"abc1234 Fix thing"}}
	rows := []gitquery.BranchRow{
		{Branch: b, WorktreePath: "/dev/feat-A"},
		{Branch: b, WorktreePath: "/dev/feat-B", IsExpansion: true},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	// Both paths should appear
	if !strings.Contains(joined, "/dev/feat-A") {
		t.Error("should show first worktree path /dev/feat-A")
	}
	if !strings.Contains(joined, "/dev/feat-B") {
		t.Error("should show second worktree path /dev/feat-B")
	}
	// Unpushed commit should appear once (on first row), not on expansion row
	if strings.Count(joined, "Fix thing") != 1 {
		t.Errorf("unpushed commit should appear exactly once, got %d", strings.Count(joined, "Fix thing"))
	}
}

func TestBranchPane_MainWorktreeShowsRootLabelAfterIndicators(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "main", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/alpha"},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[root]") {
		t.Errorf("main worktree branch should show [root] label, got: %q", joined)
	}
	// [root] should appear after the branch name, not before
	mainIdx := strings.Index(joined, "main")
	rootIdx := strings.Index(joined, "[root]")
	if mainIdx == -1 || rootIdx == -1 || rootIdx < mainIdx {
		t.Errorf("expected [root] after branch name 'main', got: %q", joined)
	}
}

func TestBranchPane_AdditionalWorktreeShowsPath(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/alpha-worktrees/feat"},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/dev/alpha-worktrees/feat") {
		t.Errorf("additional worktree branch should show path, got: %q", joined)
	}
	if strings.Contains(joined, "[root]") {
		t.Error("additional worktree branch should not show [root]")
	}
}

func TestBranchPane_NonWorktreeBranchShowsNoLabel(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "stale", HasUpstream: true}, WorktreePath: ""},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "[root]") {
		t.Error("non-worktree branch should not show [root]")
	}
}

// --- History (mode 3) tests ---

func TestModeHeader_ShowsFiveModes(t *testing.T) {
	header := renderModeHeader(4, 80)
	if !strings.Contains(header, "[4] history") {
		t.Error("expected active '[4] history' in header")
	}
	if !strings.Contains(header, "1 worktrees") {
		t.Error("expected inactive '1 worktrees' in header")
	}
	if !strings.Contains(header, "2 branches") {
		t.Error("expected inactive '2 branches' in header")
	}
	if !strings.Contains(header, "3 stashes") {
		t.Error("expected inactive '3 stashes' in header")
	}
	if !strings.Contains(header, "5 reflog") {
		t.Error("expected inactive '5 reflog' in header")
	}
	// Test mode 5 active
	header5 := renderModeHeader(5, 80)
	if !strings.Contains(header5, "[5] reflog") {
		t.Error("expected active '[5] reflog' in header")
	}
	if !strings.Contains(header5, "4 history") {
		t.Error("expected inactive '4 history' in header when mode 5 active")
	}
}

func TestStatusBar_HistoryModeShowsHistoryHints(t *testing.T) {
	bar := RenderStatusBar(120, 4, 0, 1, false, false, false)
	for _, hint := range []string{"enter: diff", "y: copy hash", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("mode 3 status bar should contain %q", hint)
		}
	}
}

func TestStatusBar_HistoryModeOmitsDeleteHint(t *testing.T) {
	bar := RenderStatusBar(120, 4, 0, 1, true, false, false)
	if strings.Contains(bar, "d: delete") {
		t.Error("mode 3 status bar should not contain 'd: delete'")
	}
	if strings.Contains(bar, "d: drop") {
		t.Error("mode 3 status bar should not contain 'd: drop'")
	}
}

func TestStatusBar_HistoryModeOmitsDestructiveHint(t *testing.T) {
	bar := RenderStatusBar(120, 4, 0, 1, false, false, false)
	if strings.Contains(bar, "D: destructive mode") {
		t.Error("mode 3 status bar should not contain 'D: destructive mode'")
	}
}

func TestCommitPane_ShowsCommitDetails(t *testing.T) {
	commits := []gitquery.Commit{
		{Hash: "abc1234", Author: "alice", Date: "2 hours ago", Subject: "Fix login bug"},
		{Hash: "def5678", Author: "bob", Date: "3 days ago", Subject: "Add profile page"},
	}
	lines := renderCommitPane(commits, 0, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "abc1234") {
		t.Error("expected hash 'abc1234' in output")
	}
	if !strings.Contains(joined, "alice") {
		t.Error("expected author 'alice' in output")
	}
	if !strings.Contains(joined, "2 hours ago") {
		t.Error("expected date '2 hours ago' in output")
	}
	if !strings.Contains(joined, "Fix login bug") {
		t.Error("expected subject 'Fix login bug' in output")
	}
}

func TestCommitPane_ScrollsToSelectedCommit(t *testing.T) {
	commits := make([]gitquery.Commit, 20)
	for i := range commits {
		commits[i] = gitquery.Commit{
			Hash:    fmt.Sprintf("abc%04d", i),
			Author:  "test",
			Date:    "now",
			Subject: fmt.Sprintf("commit-%d", i),
		}
	}
	// Scroll past first 10, show 5 lines
	lines := renderCommitPane(commits, 12, 10, 80, 5)
	joined := strings.Join(lines, "\n")
	// commit-10 should be visible (it's at offset 0 after scroll)
	if !strings.Contains(joined, "commit-10") {
		t.Error("expected 'commit-10' visible after scroll")
	}
	// commit-9 should not be visible (before scroll)
	if strings.Contains(joined, "commit-9") {
		t.Error("expected 'commit-9' not visible after scroll")
	}
}

// --- Worktree pane ---

func TestWorktreePane_ShowsBranchName(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feature"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "main") {
		t.Error("expected 'main' branch name in worktree pane")
	}
	if !strings.Contains(joined, "feature") {
		t.Error("expected 'feature' branch name in worktree pane")
	}
}

func TestWorktreePane_DetachedLabel(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/detached", Detached: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(detached)") {
		t.Error("expected '(detached)' label for detached worktree")
	}
}

func TestWorktreePane_RootAnnotation(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[root]") {
		t.Error("expected '[root]' annotation for main worktree")
	}
}

func TestWorktreePane_ShowsPath(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/dev/alpha-feat") {
		t.Error("expected worktree path in output")
	}
}

func TestWorktreePane_DirtyIndicators(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "dirty", Dirty: true, FilesChanged: 3, LinesAdded: 10, LinesDeleted: 5},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "3 files") {
		t.Error("expected '3 files' in dirty indicator")
	}
	if !strings.Contains(joined, "+10") {
		t.Error("expected '+10' lines added in dirty indicator")
	}
	if !strings.Contains(joined, "-5") {
		t.Error("expected '-5' lines deleted in dirty indicator")
	}
}

func TestWorktreePane_CleanCheckmark(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "clean"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✔") {
		t.Error("expected checkmark for clean worktree")
	}
}

func TestWorktreePane_StaleIndicator(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale-branch", Stale: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✗") {
		t.Error("expected cross mark for stale worktree")
	}
	if !strings.Contains(joined, "stale") {
		t.Error("expected 'stale' label for stale worktree")
	}
}

func TestWorktreePane_LockedIndicator(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/locked", BranchName: "locked-branch", Locked: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "🔒") {
		t.Error("expected lock icon for locked worktree")
	}
	if !strings.Contains(joined, "locked") {
		t.Error("expected 'locked' label for locked worktree")
	}
}

func TestWorktreePane_LockedReason(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/locked", BranchName: "locked-branch", Locked: true, LockReason: "on external drive"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "on external drive") {
		t.Error("expected lock reason for locked worktree")
	}
}

func TestWorktreePane_LongLockReasonTruncated(t *testing.T) {
	longReason := strings.Repeat("x", 100)
	wts := []gitquery.Worktree{
		{Path: "/dev/locked", BranchName: "locked-branch", Locked: true, LockReason: longReason},
	}
	lines := renderWorktreePane(wts, -1, 0, 200, 10)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, longReason) {
		t.Error("expected long lock reason to be truncated, not rendered in full")
	}
	if !strings.Contains(joined, "…") {
		t.Error("expected ellipsis marker for truncated lock reason")
	}
}

func TestTruncateReason_UsesVisibleWidth(t *testing.T) {
	reason := strings.Repeat("界", MaxLockReasonWidth)
	truncated := truncateReason(reason, MaxLockReasonWidth)
	if lipgloss.Width(truncated) > MaxLockReasonWidth {
		t.Errorf("truncated reason width %d exceeds max %d", lipgloss.Width(truncated), MaxLockReasonWidth)
	}
	if !strings.Contains(truncated, "…") {
		t.Error("expected ellipsis marker for wide truncated lock reason")
	}
}

func TestWorktreePane_LockedStalePrefersLockedIndicator(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "offline", Locked: true, Stale: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "🔒") || !strings.Contains(joined, "locked") {
		t.Error("expected locked indicator for locked stale worktree")
	}
	if strings.Contains(joined, "✗") || strings.Contains(joined, "stale") {
		t.Error("locked stale worktree should not render stale indicator")
	}
}

func TestWorktreePane_CursorHighlight(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	lines := renderWorktreePane(wts, 1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "> feat") {
		t.Error("expected '> feat' cursor on second item")
	}
}

func TestWorktreePane_SelectedRowPreservesSemanticIndicatorStyles(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Locked: true, LockReason: "review"},
		{Path: "/dev/alpha-feat", BranchName: "feat", Dirty: true, FilesChanged: 2, LinesAdded: 7, LinesDeleted: 3},
	}
	lines := renderWorktreePane(wts, 1, 0, 100, 10)
	joined := strings.Join(lines, "\n")
	for _, styled := range []string{
		selectedSegment(dirtyRedStyle, " ●"),
		selectedStyle.Render(" 2 files "),
		selectedSegment(diffAddStyle, "+7"),
		selectedSegment(diffDelStyle, "-3"),
	} {
		if !strings.Contains(joined, styled) {
			t.Fatalf("selected worktree row should preserve semantic style %q in:\n%s", styled, joined)
		}
	}
}

func TestWorktreePane_ScrollOffset(t *testing.T) {
	wts := make([]gitquery.Worktree, 10)
	for i := range wts {
		wts[i] = gitquery.Worktree{Path: fmt.Sprintf("/dev/wt-%d", i), BranchName: fmt.Sprintf("branch-%d", i)}
	}
	lines := renderWorktreePane(wts, 9, 8, 80, 3)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "branch-9") {
		t.Error("expected 'branch-9' visible at scroll=8")
	}
	if strings.Contains(joined, "branch-0") {
		t.Error("expected 'branch-0' not visible at scroll=8")
	}
}

func TestStatusBar_GenericFooterKeepsQuitBeforeNavigationWhenTight(t *testing.T) {
	bar := ansi.Strip(RenderStatusBar(52, 3, 0, 1, true, false, false))
	for _, want := range []string{"bksp: pane", "q/esc: quit"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("tight generic footer should keep %q, got %q", want, bar)
		}
	}
	for _, notWant := range []string{"↑/↓ select", "←/→ view"} {
		if strings.Contains(bar, notWant) {
			t.Fatalf("tight generic footer should drop navigation hint %q before quit, got %q", notWant, bar)
		}
	}
}

func TestStatusBar_WorktreesModeShowsNavHints(t *testing.T) {
	bar := RenderStatusBar(160, 1, 0, 1, false, false, false)
	for _, hint := range []string{"bksp: pane", "q/esc: quit", "↑/↓ select", "←/→ view"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode status bar should contain %q", hint)
		}
	}
}

func TestStatusBar_HidesArrowViewHintWhenLeftPaneActive(t *testing.T) {
	bar := RenderStatusBar(80, ModeFlows, OverlayNone, 0, false, false, false)
	if strings.Contains(bar, "←/→") || strings.Contains(bar, "pane/view") {
		t.Fatalf("left-pane status bar should not advertise arrow view navigation, got %q", bar)
	}
}

func TestStatusBar_ShowsArrowViewHintForActiveFlows(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:        120,
		Mode:         ModeActiveFlows,
		ActivePane:   1,
		RepoSelected: true,
		FlowSelected: true,
	})
	if !strings.Contains(bar, "←/→ view") {
		t.Fatalf("active Flow status bar should advertise arrow view navigation, got %q", bar)
	}
	if !strings.Contains(bar, "bksp: pane") {
		t.Fatalf("active Flow status bar should advertise backspace pane navigation, got %q", bar)
	}
}

func TestStatusBar_RightPaneShowsBackspacePaneHint(t *testing.T) {
	bar := RenderStatusBar(120, ModeBranches, OverlayNone, 1, false, false, false)
	for _, want := range []string{"bksp: pane", "q/esc: quit"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("right-pane status bar should advertise %q, got %q", want, bar)
		}
	}
	if strings.Contains(bar, "f2: pane") {
		t.Fatalf("right-pane status bar should not duplicate tab pane hint, got %q", bar)
	}
	if strings.Contains(bar, "⌫") {
		t.Fatalf("right-pane status bar should use bksp label, got %q", bar)
	}
}

func TestStatusBar_BranchesModeShowsArrowViewHint(t *testing.T) {
	bar := RenderStatusBar(120, ModeBranches, OverlayNone, 1, false, false, false)
	if !strings.Contains(bar, "←/→ view") {
		t.Fatalf("branches status bar should advertise arrow view navigation, got %q", bar)
	}
	if strings.Contains(bar, "pane/view") {
		t.Fatalf("branches status bar should not describe arrows as pane navigation, got %q", bar)
	}
}

func TestStatusBar_BranchesModeDoesNotWrapNarrowFooter(t *testing.T) {
	for _, tc := range []struct {
		name        string
		width       int
		destructive bool
	}{
		{name: "compact navigation", width: 80},
		{name: "legend navigation", width: 120},
		{name: "action heavy", width: 160, destructive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bar := RenderStatusBar(tc.width, ModeBranches, OverlayNone, 1, tc.destructive, false, false)
			stripped := ansi.Strip(bar)
			if strings.Contains(stripped, "\n") {
				t.Fatalf("branch status bar width %d should stay one line, got %q", tc.width, stripped)
			}
			if got := lipgloss.Width(stripped); got > tc.width {
				t.Fatalf("branch status bar width = %d, want <= %d: %q", got, tc.width, stripped)
			}
		})
	}
}

func TestStatusBar_BranchesModePrefersActionsOverLegendWhenTight(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:                   130,
		Mode:                    ModeBranches,
		Overlay:                 OverlayNone,
		ActivePane:              1,
		Destructive:             true,
		RepoSelected:            true,
		BranchDirtySelected:     true,
		BranchDeletableSelected: true,
		BranchOpenableSelected:  true,
		FetchAvailable:          true,
	})
	stripped := ansi.Strip(bar)
	if strings.Contains(stripped, "\n") {
		t.Fatalf("branch status bar should stay one line, got %q", stripped)
	}
	if got := lipgloss.Width(stripped); got > 130 {
		t.Fatalf("branch status bar width = %d, want <= 130: %q", got, stripped)
	}
	for _, hint := range []string{"n: new branch", "enter: diff", "d: delete", "f: fetch", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Fatalf("branch status bar should keep action hint %q when it fits without legend, got %q", hint, bar)
		}
	}
	if strings.Contains(bar, "no upstream") {
		t.Fatalf("branch status bar should drop legend before action hints when tight, got %q", bar)
	}
}

func TestStatusBar_ArrowViewHintFitsNarrowFooter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      Mode
		pane      int
		wantArrow bool
	}{
		{name: "worktrees left pane", mode: ModeWorktrees, pane: 0},
		{name: "branches", mode: ModeBranches, pane: 1, wantArrow: true},
		{name: "flows", mode: ModeFlows, pane: 1, wantArrow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bar := RenderStatusBar(80, tc.mode, OverlayNone, tc.pane, false, false, false)
			stripped := ansi.Strip(bar)
			if strings.Contains(stripped, "\n") {
				t.Fatalf("status bar should stay one line, got %q", stripped)
			}
			if got := lipgloss.Width(stripped); got > 80 {
				t.Fatalf("status bar width = %d, want <= 80: %q", got, bar)
			}
			if tc.wantArrow && !strings.Contains(bar, "←/→ view") {
				t.Fatalf("status bar should include arrow view hint, got %q", bar)
			}
			if !tc.wantArrow && strings.Contains(bar, "←/→") {
				t.Fatalf("status bar should hide arrow view hint, got %q", bar)
			}
		})
	}
}

func TestStatusBar_GenericActionModesDoNotWrapNarrowFooter(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        Mode
		destructive bool
		wantHints   []string
	}{
		{name: "stashes destructive", mode: ModeStashes, destructive: true, wantHints: []string{"←/→ view", "enter: diff", "d: drop"}},
		{name: "reflog", mode: ModeReflog, wantHints: []string{"q/esc: quit", "enter: diff", "y: copy hash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bar := RenderStatusBar(80, tc.mode, OverlayNone, 1, tc.destructive, false, false)
			stripped := ansi.Strip(bar)
			if strings.Contains(stripped, "\n") {
				t.Fatalf("status bar should stay one line, got %q", stripped)
			}
			if got := lipgloss.Width(stripped); got > 80 {
				t.Fatalf("status bar width = %d, want <= 80: %q", got, stripped)
			}
			for _, hint := range tc.wantHints {
				if !strings.Contains(bar, hint) {
					t.Fatalf("status bar should contain %q, got %q", hint, bar)
				}
			}
		})
	}
}

func TestStatusBar_PlanPhaseFooterPreservesActionsAtNarrowWidth(t *testing.T) {
	bar := renderStatusBarWithState(statusBarParams{
		Width:             80,
		Mode:              ModePlans,
		Overlay:           OverlayNone,
		ActivePane:        1,
		PlanSelected:      true,
		PlanPhaseSelected: true,
	})
	stripped := ansi.Strip(bar)
	if strings.Contains(stripped, "\n") {
		t.Fatalf("plan phase status bar should stay one line, got %q", stripped)
	}
	if got := lipgloss.Width(stripped); got > 80 {
		t.Fatalf("plan phase status bar width = %d, want <= 80: %q", got, stripped)
	}
	for _, hint := range []string{"x: phases", "o: open", "a: implement phase", "y: copy path"} {
		if !strings.Contains(bar, hint) {
			t.Fatalf("plan phase status bar should keep action hint %q, got %q", hint, bar)
		}
	}
}

func TestStatusBar_WorktreesModeShowsDiffHintWhenDirty(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, true)
	for _, hint := range []string{"enter: diff", "f: fetch", "F: pull", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode should show %q when dirty", hint)
		}
	}
}

func TestStatusBar_WorktreesModeHidesDiffHintWhenClean(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	if strings.Contains(bar, "enter: diff") {
		t.Error("worktrees mode should NOT show 'enter: diff' when clean")
	}
	for _, hint := range []string{"f: fetch", "F: pull", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode should show %q when clean and not stale", hint)
		}
	}
}

func TestStatusBar_WorktreesModeStaleHidesAllActionHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, true, true)
	for _, hint := range []string{"enter: diff", "f: fetch", "F: pull", "t: terminal", "c: code"} {
		if strings.Contains(bar, hint) {
			t.Errorf("worktrees mode should NOT show %q when stale", hint)
		}
	}
}

func TestStatusBar_WorktreesModeDestructiveNonStaleShowsDelete(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, true, false, false) // destructive, not stale
	if !strings.Contains(bar, "d: delete") {
		t.Error("worktrees mode destructive non-stale should show 'd: delete'")
	}
	if strings.Contains(bar, "p: prune") {
		t.Error("worktrees mode destructive non-stale should NOT show 'p: prune'")
	}
}

func TestStatusBar_WorktreesModeDestructiveStaleShowsPrune(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, true, true, false) // destructive, stale
	if !strings.Contains(bar, "p: prune") {
		t.Error("worktrees mode destructive stale should show 'p: prune'")
	}
	if strings.Contains(bar, "d: delete") {
		t.Error("worktrees mode destructive stale should NOT show 'd: delete'")
	}
}

func TestStatusBar_WorktreesModeReadOnlyShowsDestructiveHint(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false) // read-only
	if !strings.Contains(bar, "D: destructive mode") {
		t.Error("worktrees mode read-only should show 'D: destructive mode'")
	}
	if strings.Contains(bar, "d: delete") {
		t.Error("worktrees mode read-only should NOT show 'd: delete'")
	}
	if strings.Contains(bar, "p: prune") {
		t.Error("worktrees mode read-only should NOT show 'p: prune'")
	}
}

func TestStatusBar_WorktreesModeReadOnlyShowsDestructiveHintBeforeActions(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	dIdx := strings.Index(bar, "D: destructive mode")
	nIdx := strings.Index(bar, "n: new worktree")
	if dIdx == -1 || nIdx == -1 {
		t.Fatalf("expected destructive and new-worktree hints in %q", bar)
	}
	if dIdx > nIdx {
		t.Fatalf("expected destructive hint before worktree actions, got %q", bar)
	}
}

func TestStatusBar_WorktreesModeDoesNotWrapNarrowFooter(t *testing.T) {
	for _, tc := range []struct {
		name        string
		destructive bool
		stale       bool
		dirty       bool
		wantHints   []string
	}{
		{name: "clean", wantHints: []string{"←/→ view", "n: new worktree", "f: fetch", "F: pull"}},
		{name: "dirty", dirty: true, wantHints: []string{"←/→ view", "enter: diff"}},
		{name: "destructive", destructive: true, wantHints: []string{"←/→ view", "d: delete"}},
		{name: "stale", destructive: true, stale: true, wantHints: []string{"←/→ view", "p: prune"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bar := RenderStatusBar(80, ModeWorktrees, OverlayNone, 1, tc.destructive, tc.stale, tc.dirty)
			stripped := ansi.Strip(bar)
			if strings.Contains(stripped, "\n") {
				t.Fatalf("worktree status bar should stay one line, got %q", stripped)
			}
			if got := lipgloss.Width(stripped); got > 80 {
				t.Fatalf("worktree status bar width = %d, want <= 80: %q", got, stripped)
			}
			for _, hint := range tc.wantHints {
				if !strings.Contains(bar, hint) {
					t.Fatalf("worktree status bar should contain %q, got %q", hint, bar)
				}
			}
		})
	}
}

func TestStatusBar_WorktreesModeRightPaneShowsActionHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, true, false, false) // right pane active
	for _, hint := range []string{"f: fetch", "F: pull", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode right pane should show %q", hint)
		}
	}
}

func TestStatusBar_WorktreesModeLeftPaneHidesActionHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 0, true, false, true) // left pane active, destructive
	for _, hint := range []string{"enter: diff", "f: fetch", "F: pull", "t: terminal", "c: code", "d: delete", "p: prune"} {
		if strings.Contains(bar, hint) {
			t.Errorf("worktrees mode left pane should hide %q", hint)
		}
	}
}

func TestRender_WorktreesModeShowsData(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     1,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
			{Path: "/a-feat", BranchName: "feat"},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
	})
	if !strings.Contains(view, "main") {
		t.Error("render should contain worktree branch name 'main'")
	}
	if !strings.Contains(view, "feat") {
		t.Error("render should contain worktree branch name 'feat'")
	}
	if strings.Contains(view, "nothing here yet") {
		t.Error("render should not show placeholder when worktree data exists")
	}
}

func TestRender_UsesProvidedEmptyStateMessages(t *testing.T) {
	view := Render(RenderParams{
		Repos:             []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:          0,
		Width:             80,
		Height:            10,
		Mode:              ModeWorktrees,
		RightEmptyMessage: "No worktrees to show",
	})
	if !strings.Contains(view, "No worktrees to show") {
		t.Fatalf("render should show provided right-pane empty message, got:\n%s", view)
	}
	if strings.Contains(view, "nothing here yet") {
		t.Fatal("render should not fall back to generic placeholder when an empty message is provided")
	}

	view = Render(RenderParams{
		Width:            80,
		Height:           10,
		Mode:             ModeWorktrees,
		RepoEmptyMessage: "No repo results for zzz",
	})
	if !strings.Contains(view, "No repo results for zzz") {
		t.Fatalf("render should show provided repo empty message, got:\n%s", view)
	}
}

func TestRender_EmptyStateMessagesDoNotPanicAtTinyHeights(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("height_%d", height), func(t *testing.T) {
			_ = Render(RenderParams{
				Width:             80,
				Height:            height,
				Mode:              ModeWorktrees,
				RepoEmptyMessage:  "No repo results for zzz",
				RightEmptyMessage: "No selected repo",
			})
		})
	}
}

func TestRender_EmptyStateMessagesFitPaneWidth(t *testing.T) {
	longMessage := "No repo results for " + strings.Repeat("z", 80)

	repoLines := renderRepoList(nil, 0, 0, 12, 3, longMessage, nil)
	for i, line := range repoLines {
		if lipgloss.Width(line) > 12 {
			t.Fatalf("repo empty line %d width %d exceeds pane width 12: %q", i, lipgloss.Width(line), line)
		}
	}

	rightLines := renderPlaceholderPane(16, 3, longMessage)
	for i, line := range rightLines {
		if lipgloss.Width(line) > 16 {
			t.Fatalf("right empty line %d width %d exceeds pane width 16: %q", i, lipgloss.Width(line), line)
		}
	}
}

func TestRepoList_EmptyMessageIsVerticallyCentered(t *testing.T) {
	lines := renderRepoList(nil, 0, 0, 20, 5, "No repos", nil)
	for i, line := range lines {
		hasMessage := strings.Contains(line, "No repos")
		if i == 2 && !hasMessage {
			t.Fatalf("expected empty message centered on line 2, got %#v", lines)
		}
		if i != 2 && hasMessage {
			t.Fatalf("empty message should only appear on centered line, got line %d in %#v", i, lines)
		}
	}
}

// --- Reflog pane ---

func TestReflogPane_ShowsEntryDetails(t *testing.T) {
	entries := []gitquery.ReflogEntry{
		{Hash: "abc1234", Selector: "HEAD@{0}", Date: "2 hours ago", Subject: "commit: Fix login bug"},
		{Hash: "def5678", Selector: "HEAD@{1}", Date: "3 days ago", Subject: "checkout: moving from main to feature"},
	}
	lines := renderReflogPane(entries, 0, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "abc1234") {
		t.Error("expected hash 'abc1234' in output")
	}
	if !strings.Contains(joined, "HEAD@{0}") {
		t.Error("expected selector 'HEAD@{0}' in output")
	}
	if !strings.Contains(joined, "2 hours ago") {
		t.Error("expected date '2 hours ago' in output")
	}
	if !strings.Contains(joined, "commit: Fix login bug") {
		t.Error("expected subject 'commit: Fix login bug' in output")
	}
}

func TestReflogPane_SelectedHighlighted(t *testing.T) {
	entries := []gitquery.ReflogEntry{
		{Hash: "abc1234", Selector: "HEAD@{0}", Date: "2 hours ago", Subject: "commit: Fix login bug"},
		{Hash: "def5678", Selector: "HEAD@{1}", Date: "3 days ago", Subject: "checkout: main to feature"},
	}
	lines := renderReflogPane(entries, 1, 0, 80, 10)
	if !strings.Contains(lines[1], " > ") {
		t.Error("expected selected row to contain ' > ' prefix")
	}
}

func TestReflogDiffOverlay_EmptyDiffShowsMessage(t *testing.T) {
	view := Render(RenderParams{
		Width:   80,
		Height:  24,
		Mode:    5,
		Overlay: OverlayReflogDiff,
		// OverlayDiff is empty
	})
	if !strings.Contains(view, "No changes at this reflog entry") {
		t.Error("expected 'No changes at this reflog entry' in empty reflog diff overlay")
	}
}

func TestReflogDiffOverlay_NonEmptyDiffShowsContent(t *testing.T) {
	view := Render(RenderParams{
		Width:       80,
		Height:      24,
		Mode:        5,
		Overlay:     OverlayReflogDiff,
		OverlayDiff: "diff --git a/f.txt\n+added line",
	})
	if !strings.Contains(view, "diff --git") {
		t.Error("expected diff content in reflog diff overlay")
	}
	if strings.Contains(view, "No changes") {
		t.Error("should not show 'No changes' when diff has content")
	}
}

func TestStatusBar_ReflogModeHints(t *testing.T) {
	bar := RenderStatusBar(120, 5, 0, 1, false, false, false)
	for _, hint := range []string{"enter: diff", "y: copy hash", "bksp: pane", "q/esc: quit"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("reflog status bar should contain %q", hint)
		}
	}
	for _, forbidden := range []string{"d: delete", "d: drop", "D: destructive mode", "t: terminal", "c: code"} {
		if strings.Contains(bar, forbidden) {
			t.Errorf("reflog status bar should not contain %q", forbidden)
		}
	}
}
