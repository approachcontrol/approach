package embeddedterm

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type screenBuffer struct {
	width         int
	height        int
	maxScrollback int
	history       []string
	lines         [][]rune
	row           int
	col           int
	alt           bool
}

func newScreenBuffer(width, height, maxScrollback int) *screenBuffer {
	width, height = normalizeSize(width, height)
	if maxScrollback < 0 {
		maxScrollback = 0
	}
	s := &screenBuffer{
		width:         width,
		height:        height,
		maxScrollback: maxScrollback,
	}
	s.lines = makeBlankLines(height, width)
	return s
}

func (s *screenBuffer) Write(p []byte) {
	text := strings.ToValidUTF8(string(p), "")
	for i := 0; i < len(text); {
		switch text[i] {
		case '\x1b':
			next := s.applyEscape(text, i)
			if next <= i {
				i++
			} else {
				i = next
			}
		case '\r':
			s.col = 0
			i++
		case '\n':
			s.newLine()
			i++
		case '\b':
			if s.col > 0 {
				s.col--
			}
			i++
		case '\t':
			nextTab := ((s.col / 8) + 1) * 8
			spaces := nextTab - s.col
			if remaining := s.width - s.col; spaces > remaining {
				spaces = remaining
			}
			for j := 0; j < spaces; j++ {
				s.putRune(' ')
			}
			i++
		default:
			r := rune(text[i])
			size := 1
			if text[i] >= 0x80 {
				r, size = decodeRune(text[i:])
			}
			if r >= ' ' {
				s.putRune(r)
			}
			i += size
		}
	}
}

func (s *screenBuffer) Resize(width, height int) {
	width, height = normalizeSize(width, height)
	s.width = width
	if height == s.height {
		for i := range s.lines {
			s.lines[i] = fitRunes(s.lines[i], width)
		}
		s.col = clamp(s.col, 0, width-1)
		return
	}
	if height > s.height {
		for len(s.lines) < height {
			s.lines = append(s.lines, blankRunes(width))
		}
	} else {
		for len(s.lines) > height {
			if !s.alt {
				s.appendHistory(runesToString(s.lines[0]))
			}
			s.lines = s.lines[1:]
		}
	}
	for i := range s.lines {
		s.lines[i] = fitRunes(s.lines[i], width)
	}
	s.height = height
	s.row = clamp(s.row, 0, height-1)
	s.col = clamp(s.col, 0, width-1)
}

func (s *screenBuffer) VisibleLines(width, height int) []string {
	width, height = normalizeSize(width, height)
	lines := make([]string, 0, len(s.history)+len(s.lines))
	if !s.alt {
		lines = append(lines, s.history...)
	}
	for _, line := range s.lines {
		lines = append(lines, runesToString(line))
	}
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, fitVisibleLine(line, width))
	}
	return out
}

func (s *screenBuffer) retainedLineCount() int {
	return len(s.history) + len(s.lines)
}

func (s *screenBuffer) putRune(r rune) {
	if s.width <= 0 || s.height <= 0 {
		return
	}
	cellWidth := ansi.StringWidth(string(r))
	if cellWidth <= 0 {
		return
	}
	if cellWidth > s.width {
		cellWidth = 1
	}
	if s.col+cellWidth > s.width {
		s.newLine()
	}
	s.ensureCursor()
	s.lines[s.row][s.col] = r
	for i := 1; i < cellWidth && s.col+i < s.width; i++ {
		s.lines[s.row][s.col+i] = ' '
	}
	s.col += cellWidth
	if s.col >= s.width {
		s.col = s.width
	}
}

func (s *screenBuffer) newLine() {
	s.row++
	s.col = 0
	if s.row < s.height {
		return
	}
	if len(s.lines) > 0 {
		if !s.alt {
			s.appendHistory(runesToString(s.lines[0]))
		}
		s.lines = append(s.lines[1:], blankRunes(s.width))
	}
	s.row = s.height - 1
}

func (s *screenBuffer) appendHistory(line string) {
	s.history = append(s.history, line)
	if len(s.history) > s.maxScrollback {
		copy(s.history, s.history[len(s.history)-s.maxScrollback:])
		s.history = s.history[:s.maxScrollback]
	}
}

func (s *screenBuffer) applyEscape(text string, start int) int {
	if start+1 >= len(text) {
		return start + 1
	}
	switch text[start+1] {
	case '[':
		return s.applyCSI(text, start+2)
	case ']':
		return skipUntilTerminator(text, start+2)
	case 'P', '^', '_', 'X':
		return skipUntilST(text, start+2)
	case 'c':
		s.resetScreen()
		return start + 2
	case 'D':
		s.newLine()
		return start + 2
	case 'M':
		if s.row > 0 {
			s.row--
		}
		return start + 2
	default:
		return start + 2
	}
}

func (s *screenBuffer) applyCSI(text string, start int) int {
	end := start
	for end < len(text) {
		b := text[end]
		if b >= 0x40 && b <= 0x7e {
			break
		}
		end++
	}
	if end >= len(text) {
		return len(text)
	}
	params := text[start:end]
	final := text[end]
	values := csiParams(params)
	switch final {
	case 'A':
		s.row = clamp(s.row-param(values, 0, 1), 0, s.height-1)
	case 'B':
		s.row = clamp(s.row+param(values, 0, 1), 0, s.height-1)
	case 'C':
		s.col = clamp(s.col+param(values, 0, 1), 0, s.width-1)
	case 'D':
		s.col = clamp(s.col-param(values, 0, 1), 0, s.width-1)
	case 'H', 'f':
		s.row = clamp(param(values, 0, 1)-1, 0, s.height-1)
		s.col = clamp(param(values, 1, 1)-1, 0, s.width-1)
	case 'J':
		s.eraseDisplay(param(values, 0, 0))
	case 'K':
		s.eraseLine(param(values, 0, 0))
	case 'h':
		if strings.Contains(params, "?1049") || strings.Contains(params, "?1047") {
			s.alt = true
			s.resetVisibleScreen()
		}
	case 'l':
		if strings.Contains(params, "?1049") || strings.Contains(params, "?1047") {
			s.alt = false
			s.resetVisibleScreen()
		}
	}
	return end + 1
}

func (s *screenBuffer) eraseDisplay(mode int) {
	switch mode {
	case 2:
		s.resetVisibleScreen()
	case 0:
		s.eraseLineRight(s.row, s.col)
		for row := s.row + 1; row < len(s.lines); row++ {
			s.lines[row] = blankRunes(s.width)
		}
	case 1:
		for row := 0; row < s.row; row++ {
			s.lines[row] = blankRunes(s.width)
		}
		if s.row < len(s.lines) {
			for col := 0; col <= s.col && col < s.width; col++ {
				s.lines[s.row][col] = ' '
			}
		}
	}
}

func (s *screenBuffer) eraseLine(mode int) {
	switch mode {
	case 2:
		s.lines[s.row] = blankRunes(s.width)
	case 1:
		for col := 0; col <= s.col && col < s.width; col++ {
			s.lines[s.row][col] = ' '
		}
	default:
		s.eraseLineRight(s.row, s.col)
	}
}

func (s *screenBuffer) eraseLineRight(row, col int) {
	if row < 0 || row >= len(s.lines) {
		return
	}
	for col < s.width {
		s.lines[row][col] = ' '
		col++
	}
}

func (s *screenBuffer) resetScreen() {
	s.history = nil
	s.resetVisibleScreen()
}

func (s *screenBuffer) resetVisibleScreen() {
	s.lines = makeBlankLines(s.height, s.width)
	s.row = 0
	s.col = 0
}

func (s *screenBuffer) ensureCursor() {
	s.row = clamp(s.row, 0, s.height-1)
	s.col = clamp(s.col, 0, s.width-1)
}

func csiParams(params string) []int {
	params = strings.TrimPrefix(params, "?")
	if params == "" {
		return nil
	}
	parts := strings.Split(params, ";")
	values := make([]int, len(parts))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err == nil {
			values[i] = value
		}
	}
	return values
}

func param(values []int, index, fallback int) int {
	if index >= len(values) || values[index] == 0 {
		return fallback
	}
	return values[index]
}

func skipUntilTerminator(text string, start int) int {
	for i := start; i < len(text); i++ {
		if text[i] == '\a' {
			return i + 1
		}
		if text[i] == '\x1b' && i+1 < len(text) && text[i+1] == '\\' {
			return i + 2
		}
	}
	return len(text)
}

func skipUntilST(text string, start int) int {
	for i := start; i+1 < len(text); i++ {
		if text[i] == '\x1b' && text[i+1] == '\\' {
			return i + 2
		}
	}
	return len(text)
}

func decodeRune(s string) (rune, int) {
	for size := 1; size <= len(s) && size <= 4; size++ {
		runes := []rune(s[:size])
		if len(runes) == 1 && string(runes[0]) == s[:size] {
			return runes[0], size
		}
	}
	return rune(s[0]), 1
}

func makeBlankLines(height, width int) [][]rune {
	lines := make([][]rune, height)
	for i := range lines {
		lines[i] = blankRunes(width)
	}
	return lines
}

func blankRunes(width int) []rune {
	line := make([]rune, width)
	for i := range line {
		line[i] = ' '
	}
	return line
}

func fitRunes(line []rune, width int) []rune {
	next := blankRunes(width)
	copy(next, line)
	return next
}

func runesToString(line []rune) string {
	return strings.TrimRight(string(line), " ")
}

func fitVisibleLine(line string, width int) string {
	if ansi.StringWidth(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "")
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
