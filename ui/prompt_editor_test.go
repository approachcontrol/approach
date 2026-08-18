package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/scanner"
)

func editorParams(width, height int, value string, cursor int) RenderParams {
	return RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       width,
		Height:      height,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Edit Plan launch",
		InputValue:  value,
		InputCursor: cursor,
		InputMode:   InputMultiLine,
		Editor: EditorParams{
			Enabled:  true,
			Title:    "Plan launch",
			Identity: "agent.plan_prompt  custom",
		},
	}
}

// panelBounds reports the rendered editor panel's outer width and row count by
// finding the bordered block in the stripped view.
func panelBounds(t *testing.T, view string) (int, int) {
	t.Helper()
	rows := 0
	width := 0
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		trimmed := strings.TrimRight(line, " ")
		idx := strings.IndexAny(trimmed, "┌│└")
		if idx < 0 {
			continue
		}
		rows++
		if w := lipgloss.Width(trimmed[idx:]); w > width {
			width = w
		}
	}
	return width, rows
}

// overlaySelectPanelRows locates the composited select panel of the given
// outer width and returns its row count.
func overlaySelectPanelRows(t *testing.T, view string, width int) int {
	t.Helper()
	top := "┌" + strings.Repeat("─", width-2) + "┐"
	bottom := "└" + strings.Repeat("─", width-2) + "┘"
	lines := strings.Split(ansi.Strip(view), "\n")
	first, last := -1, -1
	for i, line := range lines {
		if first < 0 && strings.Contains(line, top) {
			first = i
		}
		if strings.Contains(line, bottom) {
			last = i
		}
	}
	if first < 0 || last < first {
		t.Fatalf("no %d-wide select panel found in:\n%s", width, view)
	}
	return last - first + 1
}

func TestNormalizePromptEditorLayoutWalksTheClampLadder(t *testing.T) {
	cases := []struct {
		name         string
		width, body  int
		wantWidth    int
		wantHeight   int
		wantViewport int
		wantNoteRows int
		wantFrame    bool
		wantIdentity bool
		wantHint     bool
	}{
		{"80x24 ideal", 80, 23, promptEditorWidth, promptEditorHeight, 12, 2, true, true, true},
		{"80x20 continuous viewport", 80, 19, promptEditorWidth, 19, 10, 2, true, true, true},
		{"40x12 shrinks the note block first", 40, 11, 36, 11, 3, 1, true, true, true},
		{"20x6 degenerate floor", 20, 5, 20, 4, 1, 0, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePromptEditorLayout(tc.width, tc.body)
			if got.width != tc.wantWidth || got.height != tc.wantHeight {
				t.Fatalf("layout = %dx%d, want %dx%d", got.width, got.height, tc.wantWidth, tc.wantHeight)
			}
			if got.viewportLines != tc.wantViewport {
				t.Fatalf("viewport = %d, want %d", got.viewportLines, tc.wantViewport)
			}
			if got.noteRows != tc.wantNoteRows {
				t.Fatalf("note rows = %d, want %d", got.noteRows, tc.wantNoteRows)
			}
			if got.frame != tc.wantFrame || got.hasIdentity != tc.wantIdentity || got.hasHint != tc.wantHint {
				t.Fatalf("frame/identity/hint = %v/%v/%v, want %v/%v/%v",
					got.frame, got.hasIdentity, got.hasHint, tc.wantFrame, tc.wantIdentity, tc.wantHint)
			}
			if got.height > tc.body {
				t.Fatalf("layout height %d exceeds body height %d", got.height, tc.body)
			}
		})
	}
}

func TestNormalizePromptEditorLayoutNeverGoesBelowTheFloor(t *testing.T) {
	for body := 0; body <= 24; body++ {
		for _, width := range []int{4, 10, 20, 34, 40, 80, 200} {
			layout := normalizePromptEditorLayout(width, body)
			if layout.height < 4 {
				t.Fatalf("%dx%d height = %d, want at least the 4-row floor", width, body, layout.height)
			}
			if layout.viewportLines < 1 {
				t.Fatalf("%dx%d viewport = %d, want at least the cursor line", width, body, layout.viewportLines)
			}
			if layout.width < 1 || layout.width > max(1, width) {
				t.Fatalf("%dx%d width = %d, want within the terminal", width, body, layout.width)
			}
			if layout.height > body && layout.height != 4 {
				t.Fatalf("%dx%d height %d exceeds body without being the floor", width, body, layout.height)
			}
		}
	}
}

func TestNormalizePromptEditorLayoutDropsFrameForNarrowText(t *testing.T) {
	layout := normalizePromptEditorLayout(12, 23)
	if layout.frame {
		t.Fatalf("expected the viewport frame dropped to recover columns at width 12: %#v", layout)
	}
	if layout.textWidth() < 1 {
		t.Fatalf("text width = %d, want at least 1", layout.textWidth())
	}
}

func TestRender_PromptEditorGeometryIsFixedRegardlessOfContent(t *testing.T) {
	long := make([]string, 400)
	for i := range long {
		long[i] = "line"
	}
	cases := []struct {
		name   string
		mutate func(RenderParams) RenderParams
	}{
		{"empty template", func(p RenderParams) RenderParams { return p }},
		{"three lines", func(p RenderParams) RenderParams {
			p.InputValue = "a\nb\nc"
			p.InputCursor = 5
			return p
		}},
		{"400 lines", func(p RenderParams) RenderParams {
			p.InputValue = strings.Join(long, "\n")
			p.InputCursor = len([]rune(p.InputValue))
			return p
		}},
		{"single 400-column line", func(p RenderParams) RenderParams {
			p.InputValue = strings.Repeat("x", 400)
			p.InputCursor = 400
			return p
		}},
		{"validation error", func(p RenderParams) RenderParams {
			p.InputError = "template must not contain a null byte"
			return p
		}},
		{"feedback note", func(p RenderParams) RenderParams {
			p.Editor.Note = "open /etc/approach/config.toml: permission denied"
			p.Editor.NoteKind = NoteError
			return p
		}},
		{"dirty", func(p RenderParams) RenderParams {
			p.Editor.Dirty = true
			return p
		}},
	}

	var wantWidth, wantRows int
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := Render(tc.mutate(editorParams(80, 24, "", 0)))
			width, rows := panelBounds(t, view)
			if i == 0 {
				wantWidth, wantRows = width, rows
				if wantWidth != promptEditorWidth || wantRows != promptEditorHeight {
					t.Fatalf("panel = %dx%d, want %dx%d", wantWidth, wantRows, promptEditorWidth, promptEditorHeight)
				}
				return
			}
			if width != wantWidth || rows != wantRows {
				t.Fatalf("panel = %dx%d, want fixed %dx%d", width, rows, wantWidth, wantRows)
			}
		})
	}
}

func TestRender_PromptEditorShowsTitleIdentityStateAndHints(t *testing.T) {
	p := editorParams(80, 24, "custom body", 11)
	p.Editor.Dirty = true
	view := ansi.Strip(Render(p))

	for _, want := range []string{
		"Edit Plan launch",
		"agent.plan_prompt",
		"custom",
		"modified",
		"ctrl+s",
		"enter",
		"esc",
		"line 1/1",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected editor to show %q:\n%s", want, view)
		}
	}
}

func TestRender_PromptEditorEmptyTemplateShowsBuiltInDefaultAffordance(t *testing.T) {
	view := ansi.Strip(Render(editorParams(80, 24, "", 0)))
	if !strings.Contains(view, promptEditorEmptyHint) {
		t.Fatalf("expected the built-in-default placeholder:\n%s", view)
	}
	if !strings.Contains(view, "█") {
		t.Fatalf("expected a cursor in the empty viewport:\n%s", view)
	}
}

func TestRender_PromptEditorClampsWithoutDrawingOutOfBounds(t *testing.T) {
	value := strings.Repeat("some template line\n", 40)
	for _, size := range []struct{ w, h int }{{80, 24}, {80, 20}, {40, 12}, {20, 6}, {18, 5}, {12, 4}} {
		t.Run(strings.Join([]string{"size", itoaTest(size.w), itoaTest(size.h)}, "-"), func(t *testing.T) {
			p := editorParams(size.w, size.h, value, len([]rune(value))/2)
			view := Render(p)
			lines := strings.Split(view, "\n")
			if len(lines) > size.h {
				t.Fatalf("rendered %d lines, want at most %d", len(lines), size.h)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > size.w {
					t.Fatalf("line %d width %d exceeds terminal width %d", i, w, size.w)
				}
			}
			if !strings.Contains(ansi.Strip(view), "█") {
				t.Fatalf("cursor must stay visible at %dx%d:\n%s", size.w, size.h, view)
			}
		})
	}
}

func TestRender_PromptEditorHintKeysSurviveClamping(t *testing.T) {
	value := "template"
	view := ansi.Strip(Render(editorParams(40, 12, value, 8)))
	for _, want := range []string{"ctrl+s", "esc", "enter"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in the 40x12 hint row:\n%s", want, view)
		}
	}

	// At widths that still fit it, the status bar names all three keys.
	if got := promptEditorStatusText(80); !strings.Contains(got, "ctrl+s") ||
		!strings.Contains(got, "esc") || !strings.Contains(got, "enter") {
		t.Fatalf("wide status text = %q", got)
	}
	if got := promptEditorStatusText(35); got != promptEditorStatusCompact {
		t.Fatalf("status text at width 35 = %q, want the compact form", got)
	}
	// Below that the compact form no longer fits; the fallback still names the
	// save and cancel keys inside 20 columns.
	got := promptEditorStatusText(20)
	if got != promptEditorStatusFallback {
		t.Fatalf("status text at width 20 = %q, want the fallback", got)
	}
	if lipgloss.Width(got) > 20 {
		t.Fatalf("fallback status text is %d columns, want at most 20", lipgloss.Width(got))
	}
}

func TestPromptEditorViewportWindowFollowsTheCursor(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line"
	}
	lines[0] = "first"
	lines[29] = "last"

	window, first, more := promptEditorViewportWindow(lines, 12, 29)
	if len(window) != 12 {
		t.Fatalf("window = %d rows, want 12", len(window))
	}
	if window[len(window)-1] != "last" {
		t.Fatal("expected the last line in view when the cursor is on it")
	}
	if first == 0 || more {
		t.Fatalf("first = %d, more = %v, want a scrolled window at the end", first, more)
	}

	window, first, more = promptEditorViewportWindow(lines, 12, 0)
	if first != 0 || !more || window[0] != "first" {
		t.Fatalf("expected the top window: first=%d more=%v", first, more)
	}

	for cursor := 0; cursor < len(lines); cursor++ {
		window, first, _ := promptEditorViewportWindow(lines, 5, cursor)
		if cursor < first || cursor >= first+len(window) {
			t.Fatalf("cursor %d fell outside window [%d,%d)", cursor, first, first+len(window))
		}
	}
}

func TestPromptEditorViewportWindowNeverReplacesContentWithAMarker(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, rows := range []int{1, 2, 3, 4, 5, 6} {
		for cursor := range lines {
			window, first, _ := promptEditorViewportWindow(lines, rows, cursor)
			for i, line := range window {
				if line == shortcutOverflowMarker {
					t.Fatalf("rows=%d cursor=%d: window row %d was replaced by an overflow marker", rows, cursor, i)
				}
				if line != lines[first+i] {
					t.Fatalf("rows=%d cursor=%d: window row %d = %q, want %q", rows, cursor, i, line, lines[first+i])
				}
			}
		}
	}
}

func TestRender_PromptEditorShowsOverflowArrowsAndLogicalPosition(t *testing.T) {
	// Non-ASCII on purpose: InputCursor is a rune index, so a byte-slice
	// regression in the position label fails loudly here.
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "テンプレート"
	}
	value := strings.Join(lines, "\n")
	cursorLine := 20
	cursor := len([]rune(strings.Join(lines[:cursorLine], "\n"))) + 1

	view := ansi.Strip(Render(editorParams(80, 24, value, cursor)))
	if !strings.Contains(view, "line 21/40") {
		t.Fatalf("expected logical position line 21/40:\n%s", view)
	}
	if !strings.Contains(view, "▲") || !strings.Contains(view, "▼") {
		t.Fatalf("expected overflow arrows on both frame edges:\n%s", view)
	}
}

func TestPromptEditorBodyLinesPreserveRawWhitespace(t *testing.T) {
	value := "    indented\n\n  two  interior   spaces  \nlast"
	lines, cursorLine := promptEditorBodyLines(value, 0, 60)

	if cursorLine != 0 {
		t.Fatalf("cursor line = %d, want 0", cursorLine)
	}
	if got := lines[0]; got != "█    indented" {
		t.Fatalf("line 0 = %q, want no label prefix and preserved leading spaces", got)
	}
	if got := lines[1]; got != "" {
		t.Fatalf("interior blank line = %q, want preserved", got)
	}
	if got := lines[2]; got != "  two  interior   spaces  " {
		t.Fatalf("line 2 = %q, want repeated spaces preserved verbatim", got)
	}
	if got := lines[3]; got != "last" {
		t.Fatalf("line 3 = %q", got)
	}
}

func TestPromptEditorNoteBlockPrecedenceAndTruncation(t *testing.T) {
	base := editorParams(80, 24, "body", 4)
	base.InputError = "validation failed"
	base.Editor.Note = "persistence failed"
	base.Editor.EmptyWarning = true

	rows := promptEditorNoteRows(base, 2, 60)
	if len(rows) != 2 {
		t.Fatalf("note rows = %d, want 2", len(rows))
	}
	if !strings.Contains(ansi.Strip(rows[0]), "validation failed") {
		t.Fatalf("expected the validation error to win: %q", rows[0])
	}

	base.InputError = ""
	rows = promptEditorNoteRows(base, 2, 60)
	if !strings.Contains(ansi.Strip(rows[0]), "persistence failed") {
		t.Fatalf("expected the stored note next: %q", rows[0])
	}

	base.Editor.Note = ""
	rows = promptEditorNoteRows(base, 2, 60)
	if !strings.Contains(ansi.Strip(rows[0]), promptEditorEmptyWarning) {
		t.Fatalf("expected the derived empty warning last: %q", rows[0])
	}

	base.Editor.EmptyWarning = false
	rows = promptEditorNoteRows(base, 2, 60)
	for i, row := range rows {
		if row != "" {
			t.Fatalf("row %d = %q, want a present-but-blank note block", i, row)
		}
	}
}

func TestPromptEditorLongErrorIsTruncatedInsideTheFixedNoteBlock(t *testing.T) {
	p := editorParams(80, 24, "body", 4)
	p.Editor.Note = "open /Users/someone/very/long/path/to/.config/approach/config.toml: " +
		"permission denied while writing the agent section for this prompt template"
	p.Editor.NoteKind = NoteError

	rows := promptEditorNoteRows(p, 2, 66)
	if len(rows) != 2 {
		t.Fatalf("note rows = %d, want exactly 2", len(rows))
	}
	// The leading, specific part of the error — the path — survives; a
	// pathological tail is elided rather than allowed to grow the panel.
	if !strings.Contains(ansi.Strip(rows[0])+ansi.Strip(rows[1]), "config.toml") {
		t.Fatalf("expected the leading, specific part of the error to survive:\n%q\n%q", rows[0], rows[1])
	}
	if !strings.HasSuffix(ansi.Strip(rows[1]), "…") {
		t.Fatalf("expected the overflowing last row truncated with an ellipsis: %q", rows[1])
	}
	for i, row := range rows {
		if w := lipgloss.Width(ansi.Strip(row)); w > 66 {
			t.Fatalf("note row %d is %d columns, want at most 66", i, w)
		}
	}
}

func TestRender_PromptTemplatePickerReservesANoteRow(t *testing.T) {
	items := make([]SelectItem, 9)
	for i := range items {
		items[i] = SelectItem{Label: "Template", Value: "t"}
	}
	base := RenderParams{
		Repos:           []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:           80,
		Height:          24,
		Mode:            ModePlans,
		Overlay:         OverlaySelect,
		SelectPrompt:    PromptTemplateSelectPrompt,
		SelectItems:     items,
		SelectWidth:     PromptPickerWidth,
		SelectHeight:    len(items) + 3 + PromptPickerNoteRows,
		SelectPlacement: SelectPlacementCenter,
	}

	wantRows := len(items) + 3 + PromptPickerNoteRows
	plainRows := overlaySelectPanelRows(t, Render(base), PromptPickerWidth)
	if plainRows != wantRows {
		t.Fatalf("picker panel rows = %d, want %d", plainRows, wantRows)
	}

	noted := base
	noted.SelectNote = "Saved Plan launch"
	noted.SelectNoteKind = NoteSuccess
	notedView := Render(noted)
	if notedRows := overlaySelectPanelRows(t, notedView, PromptPickerWidth); notedRows != plainRows {
		t.Fatalf("note changed the picker geometry: %d rows vs %d", notedRows, plainRows)
	}
	if !strings.Contains(ansi.Strip(notedView), "Saved Plan launch") {
		t.Fatalf("expected the picker note rendered:\n%s", notedView)
	}
	// Every item stays visible even with the note row reserved.
	if strings.Count(ansi.Strip(notedView), "Template") < len(items) {
		t.Fatalf("note row displaced picker items:\n%s", notedView)
	}

	errored := base
	errored.SelectNote = "read-only config"
	errored.SelectNoteKind = NoteError
	if !strings.Contains(ansi.Strip(Render(errored)), "read-only config") {
		t.Fatal("expected the picker note row to carry errors too")
	}
}

func TestRenderSelectPanelDropsNoteBeforeStealingItemRows(t *testing.T) {
	items := []SelectItem{{Label: "alpha", Value: "a"}, {Label: "bravo", Value: "b"}}
	width, height := selectPanelDimensions("Prompt templates", items, 20, 6, 20, 5)
	if height != 5 {
		t.Fatalf("clamped height = %d, want 5", height)
	}

	lines := renderSelectPanel(selectPanelSpec{
		prompt:   "Prompt templates",
		items:    items,
		note:     "Saved",
		noteKind: NoteSuccess,
		width:    width,
		height:   height,
	})
	got := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "bravo") {
		t.Fatalf("clamped note stole an item row:\n%s", got)
	}
}

func TestRender_SelectPanelWithoutANoteKeepsItsAutoGeometry(t *testing.T) {
	items := []SelectItem{{Label: "codex", Value: "codex"}, {Label: "claude", Value: "claude"}}
	view := Render(RenderParams{
		Repos:        []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:        80,
		Height:       24,
		Mode:         ModeWorktrees,
		Overlay:      OverlaySelect,
		SelectPrompt: "Choose agent",
		SelectItems:  items,
	})
	if rows := overlaySelectPanelRows(t, view, autoSelectPanelWidth("Choose agent", items)); rows != 2+1+len(items) {
		t.Fatalf("auto select panel rows = %d, want %d", rows, 2+1+len(items))
	}
}

func TestRender_PromptEditorStatusBarReplacesTheGenericMultilineHints(t *testing.T) {
	view := ansi.Strip(Render(editorParams(80, 24, "body", 4)))
	if strings.Contains(view, "alt+enter: newline") {
		t.Fatalf("editor should not show the generic multiline hints:\n%s", view)
	}
	if !strings.Contains(view, "ctrl+s save") {
		t.Fatalf("expected the editor status hints:\n%s", view)
	}

	generic := ansi.Strip(Render(RenderParams{
		Repos:       []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:       80,
		Height:      24,
		Mode:        ModePlans,
		Overlay:     OverlayInput,
		InputPrompt: "Launch instructions",
		InputValue:  "body",
		InputCursor: 4,
		InputMode:   InputMultiLine,
	}))
	if !strings.Contains(generic, "alt+enter: newline") {
		t.Fatalf("generic multiline input lost its hints:\n%s", generic)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}
