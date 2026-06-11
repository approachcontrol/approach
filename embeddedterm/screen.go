package embeddedterm

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const maxPendingSequenceBytes = 4096

type screenBuffer struct {
	width         int
	height        int
	maxScrollback int
	history       []string
	lines         [][]rune
	row           int
	col           int
	alt           bool
	pending       []byte
	normalLines   [][]rune
	normalRow     int
	normalCol     int
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
	if len(s.pending) > 0 {
		next := make([]byte, 0, len(s.pending)+len(p))
		next = append(next, s.pending...)
		next = append(next, p...)
		p = next
		s.pending = nil
	}
	for i := 0; i < len(p); {
		switch p[i] {
		case '\x1b':
			next, complete := s.applyEscape(p, i)
			if !complete {
				s.storePending(p[i:])
				return
			}
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
			if p[i] >= 0x80 && !utf8.FullRune(p[i:]) {
				s.storePending(p[i:])
				return
			}
			r := rune(p[i])
			size := 1
			if p[i] >= 0x80 {
				r, size = utf8.DecodeRune(p[i:])
				if r == utf8.RuneError && size == 1 {
					i++
					continue
				}
			}
			if r >= ' ' {
				s.putRune(r)
			}
			i += size
		}
	}
}

func (s *screenBuffer) storePending(p []byte) {
	if len(p) > maxPendingSequenceBytes {
		s.pending = nil
		return
	}
	s.pending = append(s.pending[:0], p...)
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

func (s *screenBuffer) applyEscape(text []byte, start int) (int, bool) {
	if start+1 >= len(text) {
		return start, false
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
		return start + 2, true
	case 'D':
		s.newLine()
		return start + 2, true
	case 'M':
		if s.row > 0 {
			s.row--
		}
		return start + 2, true
	default:
		return start + 2, true
	}
}

func (s *screenBuffer) applyCSI(text []byte, start int) (int, bool) {
	end := start
	for end < len(text) {
		b := text[end]
		if b >= 0x40 && b <= 0x7e {
			break
		}
		end++
	}
	if end >= len(text) {
		return start - 2, false
	}
	params := string(text[start:end])
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
			s.enterAltScreen()
		}
	case 'l':
		if strings.Contains(params, "?1049") || strings.Contains(params, "?1047") {
			s.exitAltScreen()
		}
	}
	return end + 1, true
}

func (s *screenBuffer) enterAltScreen() {
	if s.alt {
		return
	}
	s.normalLines = cloneLines(s.lines)
	s.normalRow = s.row
	s.normalCol = s.col
	s.alt = true
	s.resetVisibleScreen()
}

func (s *screenBuffer) exitAltScreen() {
	if !s.alt {
		return
	}
	s.alt = false
	if len(s.normalLines) > 0 {
		s.lines = fitLines(s.normalLines, s.height, s.width)
		s.row = clamp(s.normalRow, 0, s.height-1)
		s.col = clamp(s.normalCol, 0, s.width)
	}
	s.normalLines = nil
	s.normalRow = 0
	s.normalCol = 0
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

func skipUntilTerminator(text []byte, start int) (int, bool) {
	for i := start; i < len(text); i++ {
		if text[i] == '\a' {
			return i + 1, true
		}
		if text[i] == '\x1b' && i+1 < len(text) && text[i+1] == '\\' {
			return i + 2, true
		}
	}
	return start - 2, false
}

func skipUntilST(text []byte, start int) (int, bool) {
	for i := start; i+1 < len(text); i++ {
		if text[i] == '\x1b' && text[i+1] == '\\' {
			return i + 2, true
		}
	}
	return start - 2, false
}

func makeBlankLines(height, width int) [][]rune {
	lines := make([][]rune, height)
	for i := range lines {
		lines[i] = blankRunes(width)
	}
	return lines
}

func cloneLines(lines [][]rune) [][]rune {
	next := make([][]rune, len(lines))
	for i := range lines {
		next[i] = append([]rune(nil), lines[i]...)
	}
	return next
}

func fitLines(lines [][]rune, height, width int) [][]rune {
	next := makeBlankLines(height, width)
	for i := 0; i < len(next) && i < len(lines); i++ {
		copy(next[i], lines[i])
	}
	return next
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
