package modal

type textEditState struct {
	content string
	cursor  int
	column  int
}

func (s textEditState) clamped() textEditState {
	s.cursor = clampInputCursor(s.content, s.cursor)
	return s
}

func (s textEditState) insert(runes []rune) textEditState {
	s.content, s.cursor = insertRunes(s.content, s.cursor, runes)
	s.column = -1
	return s
}

func (s textEditState) deleteBefore() textEditState {
	s.content, s.cursor = deleteRuneBefore(s.content, s.cursor)
	s.column = -1
	return s
}

func (s textEditState) deleteAt() textEditState {
	s.content, s.cursor = deleteRuneAt(s.content, s.cursor)
	s.column = -1
	return s
}

func (s textEditState) left() textEditState {
	if s.cursor > 0 {
		s.cursor--
	}
	s.column = -1
	return s
}

func (s textEditState) right() textEditState {
	if s.cursor < inputLength(s.content) {
		s.cursor++
	}
	s.column = -1
	return s
}

func (s textEditState) homeBuffer() textEditState {
	s.cursor = 0
	s.column = -1
	return s
}

func (s textEditState) endBuffer() textEditState {
	s.cursor = inputLength(s.content)
	s.column = -1
	return s
}

func (s textEditState) homeLine() textEditState {
	s.cursor = editorLineStart(s.content, s.cursor)
	s.column = -1
	return s
}

func (s textEditState) endLine() textEditState {
	s.cursor = editorLineEnd(s.content, s.cursor)
	s.column = -1
	return s
}

func (s textEditState) vertical(delta int) textEditState {
	s.cursor, s.column = moveCursorVertically(s.content, s.cursor, delta, s.column)
	return s
}

func (s textEditState) clear() textEditState {
	s.content = ""
	s.cursor = 0
	s.column = -1
	return s
}
