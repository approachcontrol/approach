package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// The prompt-template editor's ideal geometry. Unlike the picker's constants
// these stay unexported: the model builds no editor layout, so nothing outside
// ui reads them.
const (
	promptEditorWidth         = 72
	promptEditorViewportLines = 12
	promptEditorHeight        = 21
	promptEditorMinWidth      = 32
	promptEditorMinTextWidth  = 8
)

const (
	promptEditorEmptyHint    = "using the built-in default — type to override"
	promptEditorEmptyWarning = "empty: saving restores the built-in default"
)

// Hint copy, widest first. The renderer picks the widest variant that fits the
// panel's content column so all three keys survive a clamped panel.
var promptEditorHints = []string{
	"ctrl+s/ctrl+enter: save   enter: newline   esc: cancel",
	"ctrl+s/^M save esc cancel",
	"^s save ^[ cancel",
}

// Status-bar copy, used when the panel is too short to carry its own hint row.
// Dropping the hint row is a height decision and truncation is a width
// decision, so the two cannot share one string.
const (
	promptEditorStatusCompact  = "  ctrl+s/ctrl+enter save  esc cancel  enter nl"
	promptEditorStatusFallback = "  ^s save ^[ cancel"
)

// promptEditorLayout is the fully resolved panel shape. The renderer emits
// exactly what it describes, so nothing in the panel is content-dependent.
type promptEditorLayout struct {
	width         int
	height        int
	viewportLines int
	noteRows      int
	frame         bool
	hasIdentity   bool
	hasHint       bool
}

// fixedRows counts every row the panel spends outside the text viewport.
func (l promptEditorLayout) fixedRows() int {
	rows := 2 + 1 + l.noteRows // outer border, title, note block
	if l.hasIdentity {
		rows++
	}
	if l.hasHint {
		rows++
	}
	if l.frame {
		rows += 2
	}
	return rows
}

// contentWidth is the column count between the outer border's padding.
func (l promptEditorLayout) contentWidth() int {
	return max(1, l.width-4)
}

// textWidth is the column count available to the template itself.
func (l promptEditorLayout) textWidth() int {
	if l.frame {
		return max(1, l.contentWidth()-2)
	}
	return l.contentWidth()
}

// normalizePromptEditorLayout resolves width first — it can drop the viewport
// frame, which also changes the height budget — then walks the height ladder,
// shedding one optional row at a time until the panel fits. The cursor line is
// the last thing standing.
func normalizePromptEditorLayout(terminalWidth, bodyHeight int) promptEditorLayout {
	layout := promptEditorLayout{
		viewportLines: promptEditorViewportLines,
		noteRows:      2,
		frame:         true,
		hasIdentity:   true,
		hasHint:       true,
	}

	width := terminalWidth - 4
	if width > promptEditorWidth {
		width = promptEditorWidth
	}
	if width < promptEditorMinWidth {
		width = terminalWidth
	}
	if width < 1 {
		width = 1
	}
	layout.width = width
	if width-4-2 < promptEditorMinTextWidth {
		layout.frame = false
	}

	// Step 1 is continuous: the viewport takes whatever the fixed rows leave.
	viewport := bodyHeight - layout.fixedRows()
	if viewport > promptEditorViewportLines {
		viewport = promptEditorViewportLines
	}
	if viewport < 3 {
		viewport = 3
	}
	layout.viewportLines = viewport

	fits := func() bool { return layout.fixedRows()+layout.viewportLines <= bodyHeight }
	if !fits() && layout.noteRows == 2 {
		layout.noteRows = 1 // step 2: shrink the note block before dropping hints
	}
	if !fits() && layout.hasHint {
		layout.hasHint = false // step 3: the status bar carries the keys instead
	}
	if !fits() && layout.hasIdentity {
		layout.hasIdentity = false // step 4
	}
	if !fits() && layout.noteRows == 1 {
		layout.noteRows = 0 // step 5
	}
	if !fits() && layout.frame {
		layout.frame = false // step 6
	}
	if !fits() {
		layout.viewportLines = 1 // step 7: the cursor line
	}

	layout.height = layout.fixedRows() + layout.viewportLines
	return layout
}

// renderPromptEditorDialog draws the editor into a fixed-height buffer,
// clipping rather than drawing out of bounds on a terminal too short to hold
// even the floor panel.
func renderPromptEditorDialog(p RenderParams, width, height int) []string {
	lines := make([]string, height)
	if width <= 0 || height <= 0 {
		return lines
	}
	layout := normalizePromptEditorLayout(width, height)
	panel := promptEditorPanel(p, layout)
	top := (height - len(panel)) / 2
	if top < 0 {
		top = 0
	}
	for i, line := range panel {
		row := top + i
		if row >= len(lines) {
			break
		}
		lines[row] = centeredLine(line, width)
	}
	return lines
}

func promptEditorPanel(p RenderParams, layout promptEditorLayout) []string {
	width := layout.width
	contentWidth := layout.contentWidth()
	textWidth := layout.textWidth()

	body, cursorLine := promptEditorBodyLines(p.InputValue, p.InputCursor, textWidth)
	window, first, more := promptEditorViewportWindow(body, layout.viewportLines, cursorLine)

	lines := make([]string, 0, layout.height)
	lines = append(lines, selectPanelBorderLine("┌", "─", "┐", width))

	title := strings.TrimSpace(p.Editor.Title)
	if title == "" {
		title = "prompt template"
	}
	lines = append(lines, promptEditorContentLine(
		activeModeStyle.Render(truncateToWidth("Edit "+title, contentWidth)), width))

	if layout.hasIdentity {
		lines = append(lines, promptEditorContentLine(
			statusStyle.Render(truncateToWidth(promptEditorIdentityText(p.Editor), contentWidth)), width))
	}

	if layout.frame {
		up := ""
		if first > 0 {
			up = "▲"
		}
		lines = append(lines, promptEditorContentLine(
			promptEditorFrameLine("┌", "┐", contentWidth, up, ""), width))
	}
	border := lipgloss.NewStyle().Foreground(clearDarkTheme.activeBorder)
	for i := 0; i < layout.viewportLines; i++ {
		content := ""
		if i < len(window) {
			content = window[i]
		}
		if layout.frame {
			content = border.Render("│") + fitSessionColumn(content, textWidth) + border.Render("│")
		}
		lines = append(lines, promptEditorContentLine(content, width))
	}
	if layout.frame {
		down := ""
		if more {
			down = "▼"
		}
		lines = append(lines, promptEditorContentLine(
			promptEditorFrameLine("└", "┘", contentWidth, down,
				promptEditorPositionLabel(p.InputValue, p.InputCursor)), width))
	}

	for _, note := range promptEditorNoteRows(p, layout.noteRows, contentWidth) {
		lines = append(lines, promptEditorContentLine(note, width))
	}

	if layout.hasHint {
		lines = append(lines, promptEditorContentLine(
			statusStyle.Render(promptEditorHintText(contentWidth)), width))
	}

	lines = append(lines, selectPanelBorderLine("└", "─", "┘", width))
	return lines
}

func promptEditorIdentityText(editor EditorParams) string {
	identity := strings.TrimSpace(editor.Identity)
	if editor.Dirty {
		if identity == "" {
			return "modified"
		}
		return identity + "  modified"
	}
	return identity
}

// promptEditorPositionLabel counts logical (newline-separated) lines, not
// width-dependent wrapped lines, so the indicator does not change with the
// terminal width. The re-slice through []rune is load-bearing: InputCursor is
// a rune index.
func promptEditorPositionLabel(value string, cursor int) string {
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	line := strings.Count(string(runes[:cursor]), "\n") + 1
	total := strings.Count(value, "\n") + 1
	return "line " + strconv.Itoa(line) + "/" + strconv.Itoa(total)
}

// promptEditorBodyLines is the editor's analogue of inputDialogBodyLines: it
// wraps the value with the cursor glyph in place and reports the wrapped line
// holding the cursor. It deliberately does not prefix line 0 with a prompt
// label — the title has its own row, so column 0 of every rendered line is the
// template's own first column.
func promptEditorBodyLines(value string, cursor, contentWidth int) ([]string, int) {
	if contentWidth < 1 {
		contentWidth = 1
	}
	if value == "" {
		// Eight of the nine targets open empty, so the built-in-default
		// affordance is the common path, not an edge case. The hint is not
		// part of the value.
		line := activeModeStyle.Render("█")
		if contentWidth > 1 {
			line += placeholderStyle.Render(truncateToWidth(" "+promptEditorEmptyHint, contentWidth-1))
		}
		return []string{line}, 0
	}
	logical := strings.Split(insertCursorGlyph(value, cursor), "\n")
	lines := make([]string, 0, len(logical))
	for _, line := range logical {
		lines = append(lines, wrapEditableInputLine(line, contentWidth)...)
	}
	if len(lines) == 0 {
		return []string{activeModeStyle.Render("█")}, 0
	}
	return lines, lineIndexContainingCursor(lines)
}

// promptEditorViewportWindow returns at most rows real content lines with the
// cursor line guaranteed in-window. Unlike compactInputDialogLines it never
// overwrites a boundary line with an overflow marker — that would destroy a
// real line of the user's template, and at two rows it can hide the cursor
// itself. Overflow lives in the viewport frame instead.
func promptEditorViewportWindow(lines []string, rows, cursorLine int) ([]string, int, bool) {
	if rows <= 0 {
		return nil, 0, len(lines) > 0
	}
	if len(lines) <= rows {
		return lines, 0, false
	}
	if cursorLine < 0 {
		cursorLine = 0
	}
	if cursorLine >= len(lines) {
		cursorLine = len(lines) - 1
	}
	start := cursorLine - rows/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(lines) - rows; start > maxStart {
		start = maxStart
	}
	return lines[start : start+rows], start, start+rows < len(lines)
}

// promptEditorNoteRows fills exactly rows rows, blank when there is nothing to
// say. Precedence, highest first: validation error, stored note, derived
// empty-buffer warning. A message longer than the block is truncated with an
// ellipsis rather than allowed to grow the panel.
func promptEditorNoteRows(p RenderParams, rows, width int) []string {
	if rows <= 0 {
		return nil
	}
	out := make([]string, rows)
	text := ""
	kind := NoteNeutral
	switch {
	case p.InputError != "":
		text, kind = p.InputError, NoteError
	case p.Editor.Note != "":
		text, kind = p.Editor.Note, p.Editor.NoteKind
	case p.Editor.EmptyWarning:
		text, kind = promptEditorEmptyWarning, NoteWarning
	}
	if text == "" {
		return out
	}
	wrapped := wrapPlainText(terminalSafeSingleLine(text), width)
	style := noteStyle(kind)
	for i := 0; i < rows && i < len(wrapped); i++ {
		line := wrapped[i]
		if i == rows-1 && len(wrapped) > rows {
			line = truncateWithEllipsis(line+" "+strings.Join(wrapped[rows:], " "), width)
		}
		out[i] = style.Render(line)
	}
	return out
}

func promptEditorHintText(contentWidth int) string {
	for _, hint := range promptEditorHints {
		if lipgloss.Width(hint) <= contentWidth {
			return hint
		}
	}
	return truncateToWidth(promptEditorHints[len(promptEditorHints)-1], contentWidth)
}

// promptEditorStatusText names the save key explicitly at every width the
// terminal can still show it.
func promptEditorStatusText(width int) string {
	if width >= lipgloss.Width(promptEditorStatusCompact) {
		return promptEditorStatusCompact
	}
	return promptEditorStatusFallback
}

func promptEditorContentLine(content string, width int) string {
	if width <= 0 {
		return ""
	}
	border := lipgloss.NewStyle().Foreground(clearDarkTheme.activeBorder)
	if width == 1 {
		return truncateToWidth(border.Render("│"), width)
	}
	if width < 4 {
		return border.Render("│") + fitSessionColumn(content, width-2) + border.Render("│")
	}
	return border.Render("│") + " " + fitSessionColumn(content, width-4) + " " + border.Render("│")
}

// promptEditorFrameLine draws the viewport's own border, carrying the overflow
// arrow and the logical-line indicator so neither ever replaces content.
func promptEditorFrameLine(left, right string, width int, marker, label string) string {
	border := lipgloss.NewStyle().Foreground(clearDarkTheme.activeBorder)
	if width <= 2 {
		return border.Render(truncateToWidth(strings.Repeat("─", max(0, width)), width))
	}
	inner := []rune(strings.Repeat("─", width-2))
	if marker != "" {
		copy(inner, []rune(marker))
	}
	if label != "" {
		if labelRunes := []rune(label); len(labelRunes)+2 <= len(inner) {
			copy(inner[len(inner)-len(labelRunes)-1:], labelRunes)
		}
	}
	return border.Render(left + string(inner) + right)
}

func noteStyle(kind NoteKind) lipgloss.Style {
	switch kind {
	case NoteSuccess:
		return cleanStyle
	case NoteWarning:
		return aheadBehindStyle
	case NoteError:
		return dirtyRedStyle
	default:
		return statusStyle
	}
}

func truncateWithEllipsis(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return truncateToWidth(s, width-1) + "…"
}
