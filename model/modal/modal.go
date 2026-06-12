package modal

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Kind int

const (
	None Kind = iota
	Confirm
	Input
	Select
	Diff
	Text
)

type DiffKind int

const (
	DiffStash DiffKind = iota + 1
	DiffBranch
	DiffCommit
	DiffWorktree
	DiffReflog
	DiffSessionTranscript
)

type Outcome int

const (
	Ignored Outcome = iota
	Consumed
	Accepted
	Cancelled
)

type SelectItem struct {
	Label string
	Value string
}

type InputMode int

const (
	InputSingleLine InputMode = iota
	InputMultiLine
)

// Modal is the single in-process state machine for transient modal UI. Its
// zero value is closed.
type Modal struct {
	kind        Kind
	prompt      string
	placeholder string
	force       bool
	action      func() tea.Cmd
	input       string
	inputMode   InputMode
	inputCursor int
	inputColumn int
	inputErr    string
	validate    func(string) error
	submit      func(string) tea.Cmd
	selectItems []SelectItem
	selectIndex int
	diffKind    DiffKind
	diff        string
	text        string
	scroll      int
	request     uint64
}

type View struct {
	Kind        Kind
	Prompt      string
	Placeholder string
	Force       bool
	Input       string
	InputMode   InputMode
	InputCursor int
	InputErr    string
	SelectItems []SelectItem
	SelectIndex int
	DiffKind    DiffKind
	Diff        string
	Text        string
	Scroll      int
	Request     uint64
}

func OpenConfirm(prompt string, action func() tea.Cmd) Modal {
	return Modal{kind: Confirm, prompt: prompt, action: action}
}

func OpenForce(prompt string, action func() tea.Cmd) Modal {
	return Modal{kind: Confirm, prompt: prompt, force: true, action: action}
}

func OpenInput(prompt, placeholder, initial string, validate func(string) error, submit func(string) tea.Cmd) Modal {
	return OpenSingleLineInput(prompt, placeholder, initial, validate, submit)
}

func OpenSingleLineInput(prompt, placeholder, initial string, validate func(string) error, submit func(string) tea.Cmd) Modal {
	return Modal{
		kind:        Input,
		prompt:      prompt,
		placeholder: placeholder,
		input:       initial,
		inputMode:   InputSingleLine,
		inputCursor: inputLength(initial),
		inputColumn: -1,
		validate:    validate,
		submit:      submit,
	}
}

func OpenMultiLineInput(prompt, placeholder, initial string, validate func(string) error, submit func(string) tea.Cmd) Modal {
	return Modal{
		kind:        Input,
		prompt:      prompt,
		placeholder: placeholder,
		input:       initial,
		inputMode:   InputMultiLine,
		inputCursor: inputLength(initial),
		inputColumn: -1,
		validate:    validate,
		submit:      submit,
	}
}

func OpenSelect(prompt string, items []SelectItem, selectedIndex int, submit func(string) tea.Cmd) Modal {
	if selectedIndex < 0 || selectedIndex >= len(items) {
		selectedIndex = 0
	}
	copiedItems := append([]SelectItem(nil), items...)
	return Modal{
		kind:        Select,
		prompt:      prompt,
		selectItems: copiedItems,
		selectIndex: selectedIndex,
		submit:      submit,
	}
}

func OpenDiff(kind DiffKind, body string) Modal {
	return Modal{kind: Diff, diffKind: kind, diff: body}
}

// OpenText opens a scrollable plain-text overlay (distinct from diff overlays;
// no diff coloring is applied).
func OpenText(body string) Modal {
	return Modal{kind: Text, text: body}
}

func (m Modal) WithRequest(request uint64) Modal {
	if m.kind == Diff || m.kind == Text {
		m.request = request
	}
	return m
}

// SetTextForRequest fills the text overlay body when the request matches the
// one captured when the overlay was opened.
func (m Modal) SetTextForRequest(request uint64, body string) Modal {
	if request != 0 && m.kind == Text && m.request == request {
		m.text = body
		if m.scroll > maxDiffScroll(body) {
			m.scroll = maxDiffScroll(body)
		}
	}
	return m
}

func (m Modal) SetDiffForRequest(kind DiffKind, request uint64, body string) Modal {
	if request != 0 && m.kind == Diff && m.diffKind == kind && m.request == request {
		m.diff = body
		if m.scroll > maxDiffScroll(body) {
			m.scroll = maxDiffScroll(body)
		}
	}
	return m
}

func (m Modal) SetInputError(err string) Modal {
	if m.kind == Input {
		m.inputErr = err
	}
	return m
}

func (m Modal) IsOpen() bool {
	return m.kind != None
}

func (m Modal) View() View {
	return View{
		Kind:        m.kind,
		Prompt:      m.prompt,
		Placeholder: m.placeholder,
		Force:       m.force,
		Input:       m.input,
		InputMode:   m.inputMode,
		InputCursor: clampInputCursor(m.input, m.inputCursor),
		InputErr:    m.inputErr,
		SelectItems: append([]SelectItem(nil), m.selectItems...),
		SelectIndex: m.selectIndex,
		DiffKind:    m.diffKind,
		Diff:        m.diff,
		Text:        m.text,
		Scroll:      m.scroll,
		Request:     m.request,
	}
}

func (m Modal) Update(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	switch m.kind {
	case Confirm:
		return m.updateConfirm(msg)
	case Input:
		return m.updateInput(msg)
	case Select:
		return m.updateSelect(msg)
	case Diff:
		return m.updateDiff(msg)
	case Text:
		return m.updateText(msg)
	default:
		return m, Ignored, nil
	}
}

func (m Modal) updateConfirm(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		cmd := deferAction(m.action)
		if cmd == nil {
			return Modal{}, Accepted, nil
		}
		return Modal{}, Accepted, cmd
	case "n", "q", "esc":
		return Modal{}, Cancelled, nil
	default:
		return m, Consumed, nil
	}
}

func (m Modal) updateInput(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	m.inputCursor = clampInputCursor(m.input, m.inputCursor)
	switch msg.String() {
	case "alt+enter":
		if m.inputMode == InputMultiLine {
			m.input, m.inputCursor = insertRunes(m.input, m.inputCursor, []rune{'\n'})
			m.inputColumn = -1
			m.inputErr = ""
		}
		return m, Consumed, nil
	case "enter":
		input := strings.TrimSpace(m.input)
		if m.validate != nil {
			if err := m.validate(input); err != nil {
				m.inputErr = err.Error()
				return m, Consumed, nil
			}
		}
		cmd := deferSubmit(m.submit, input)
		if cmd == nil {
			return Modal{}, Accepted, nil
		}
		return Modal{}, Accepted, cmd
	case "esc", "ctrl+c":
		return Modal{}, Cancelled, nil
	case "backspace", "ctrl+h":
		m.input, m.inputCursor = deleteRuneBefore(m.input, m.inputCursor)
		m.inputColumn = -1
		m.inputErr = ""
		return m, Consumed, nil
	case "delete":
		m.input, m.inputCursor = deleteRuneAt(m.input, m.inputCursor)
		m.inputColumn = -1
		m.inputErr = ""
		return m, Consumed, nil
	case "left":
		if m.inputCursor > 0 {
			m.inputCursor--
		}
		m.inputColumn = -1
		return m, Consumed, nil
	case "right":
		if m.inputCursor < inputLength(m.input) {
			m.inputCursor++
		}
		m.inputColumn = -1
		return m, Consumed, nil
	case "up":
		if m.inputMode == InputMultiLine {
			m.inputCursor, m.inputColumn = moveCursorVertically(m.input, m.inputCursor, -1, m.inputColumn)
		}
		return m, Consumed, nil
	case "down":
		if m.inputMode == InputMultiLine {
			m.inputCursor, m.inputColumn = moveCursorVertically(m.input, m.inputCursor, 1, m.inputColumn)
		}
		return m, Consumed, nil
	case "home", "ctrl+a":
		m.inputCursor = 0
		m.inputColumn = -1
		return m, Consumed, nil
	case "end", "ctrl+e":
		m.inputCursor = inputLength(m.input)
		m.inputColumn = -1
		return m, Consumed, nil
	case "ctrl+u":
		m.input = ""
		m.inputCursor = 0
		m.inputColumn = -1
		m.inputErr = ""
		return m, Consumed, nil
	default:
		if msg.Type == tea.KeySpace {
			m.input, m.inputCursor = insertRunes(m.input, m.inputCursor, []rune{' '})
			m.inputColumn = -1
			m.inputErr = ""
			return m, Consumed, nil
		}
		if msg.Type == tea.KeyRunes {
			m.input, m.inputCursor = insertRunes(m.input, m.inputCursor, msg.Runes)
			m.inputColumn = -1
			m.inputErr = ""
			return m, Consumed, nil
		}
		return m, Consumed, nil
	}
}

func inputLength(input string) int {
	return len([]rune(input))
}

func clampInputCursor(input string, cursor int) int {
	if cursor < 0 {
		return 0
	}
	length := inputLength(input)
	if cursor > length {
		return length
	}
	return cursor
}

func insertRunes(input string, cursor int, inserted []rune) (string, int) {
	if len(inserted) == 0 {
		return input, clampInputCursor(input, cursor)
	}
	runes := []rune(input)
	cursor = clampInputCursor(input, cursor)
	out := make([]rune, 0, len(runes)+len(inserted))
	out = append(out, runes[:cursor]...)
	out = append(out, inserted...)
	out = append(out, runes[cursor:]...)
	return string(out), cursor + len(inserted)
}

func deleteRuneBefore(input string, cursor int) (string, int) {
	runes := []rune(input)
	cursor = clampInputCursor(input, cursor)
	if cursor == 0 {
		return input, cursor
	}
	out := make([]rune, 0, len(runes)-1)
	out = append(out, runes[:cursor-1]...)
	out = append(out, runes[cursor:]...)
	return string(out), cursor - 1
}

func deleteRuneAt(input string, cursor int) (string, int) {
	runes := []rune(input)
	cursor = clampInputCursor(input, cursor)
	if cursor >= len(runes) {
		return input, cursor
	}
	out := make([]rune, 0, len(runes)-1)
	out = append(out, runes[:cursor]...)
	out = append(out, runes[cursor+1:]...)
	return string(out), cursor
}

func moveCursorVertically(input string, cursor, delta, preferredColumn int) (int, int) {
	runes := []rune(input)
	cursor = clampInputCursor(input, cursor)
	starts := lineStarts(runes)
	line, column := lineColumn(runes, starts, cursor)
	if preferredColumn >= 0 {
		column = preferredColumn
	}
	targetLine := line + delta
	if targetLine < 0 {
		targetLine = 0
	}
	if targetLine >= len(starts) {
		targetLine = len(starts) - 1
	}
	return cursorForLineColumn(runes, starts, targetLine, column), column
}

func lineStarts(runes []rune) []int {
	starts := []int{0}
	for i, r := range runes {
		if r == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineColumn(runes []rune, starts []int, cursor int) (int, int) {
	for i, start := range starts {
		nextStart := len(runes) + 1
		if i+1 < len(starts) {
			nextStart = starts[i+1]
		}
		if cursor < nextStart || i == len(starts)-1 {
			column := cursor - start
			length := lineLength(runes, starts, i)
			if column > length {
				column = length
			}
			if column < 0 {
				column = 0
			}
			return i, column
		}
	}
	return 0, 0
}

func cursorForLineColumn(runes []rune, starts []int, line, column int) int {
	if line < 0 {
		line = 0
	}
	if line >= len(starts) {
		line = len(starts) - 1
	}
	if column < 0 {
		column = 0
	}
	length := lineLength(runes, starts, line)
	if column > length {
		column = length
	}
	return starts[line] + column
}

func lineLength(runes []rune, starts []int, line int) int {
	start := starts[line]
	if line+1 < len(starts) {
		return starts[line+1] - start - 1
	}
	return len(runes) - start
}

func (m Modal) updateSelect(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if len(m.selectItems) == 0 {
			return Modal{}, Accepted, nil
		}
		cmd := deferSubmit(m.submit, m.selectItems[m.selectIndex].Value)
		if cmd == nil {
			return Modal{}, Accepted, nil
		}
		return Modal{}, Accepted, cmd
	case "esc", "ctrl+c":
		return Modal{}, Cancelled, nil
	case "down", "j":
		m.selectIndex = nextSelectIndex(m.selectIndex, len(m.selectItems))
		return m, Consumed, nil
	case "up", "k":
		m.selectIndex = previousSelectIndex(m.selectIndex, len(m.selectItems))
		return m, Consumed, nil
	default:
		return m, Consumed, nil
	}
}

func nextSelectIndex(index, length int) int {
	if length == 0 {
		return 0
	}
	return (index + 1) % length
}

func previousSelectIndex(index, length int) int {
	if length == 0 {
		return 0
	}
	index--
	if index < 0 {
		return length - 1
	}
	return index
}

func deferAction(action func() tea.Cmd) tea.Cmd {
	if action == nil {
		return nil
	}
	return func() tea.Msg {
		cmd := action()
		if cmd == nil {
			return nil
		}
		return cmd()
	}
}

func deferSubmit(submit func(string) tea.Cmd, input string) tea.Cmd {
	if submit == nil {
		return nil
	}
	return func() tea.Msg {
		cmd := submit(input)
		if cmd == nil {
			return nil
		}
		return cmd()
	}
}

func (m Modal) updateDiff(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return Modal{}, Cancelled, nil
	case "up", "k":
		if m.scroll > 0 {
			m.scroll--
		}
		return m, Consumed, nil
	case "down", "j":
		if m.scroll < maxDiffScroll(m.diff) {
			m.scroll++
		}
		return m, Consumed, nil
	default:
		return m, Consumed, nil
	}
}

func (m Modal) updateText(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return Modal{}, Cancelled, nil
	case "up", "k":
		if m.scroll > 0 {
			m.scroll--
		}
		return m, Consumed, nil
	case "down", "j":
		if m.scroll < maxDiffScroll(m.text) {
			m.scroll++
		}
		return m, Consumed, nil
	default:
		return m, Consumed, nil
	}
}

func maxDiffScroll(body string) int {
	if body == "" {
		return 0
	}
	lines := strings.Count(body, "\n") + 1
	if lines <= 1 {
		return 0
	}
	return lines - 1
}
