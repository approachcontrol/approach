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
	Form
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

type Placement int

const (
	PlacementCenter Placement = iota
	PlacementTopCenter
	PlacementBottomCenter
)

type Layout struct {
	Width     int
	Height    int
	Placement Placement
}

type InputMode int

const (
	InputSingleLine InputMode = iota
	InputMultiLine
)

type FormFieldKind int

const (
	FormText FormFieldKind = iota
	FormMultilineText
	FormCheckbox
	FormChoice
)

type FormField struct {
	ID            string
	Kind          FormFieldKind
	Label         string
	Placeholder   string
	Value         string
	Cursor        int
	Checked       bool
	Options       []SelectItem
	SelectedIndex int
}

type FormValues struct {
	Text    map[string]string
	Checked map[string]bool
	Choice  map[string]string
}

type FormSpec struct {
	Purpose  string
	Title    string
	Fields   []FormField
	Validate func(FormValues) error
	Submit   func(FormValues) tea.Cmd
}

type FormView struct {
	Purpose    string
	Title      string
	Fields     []FormField
	FocusIndex int
	Error      string
}

// Modal is the single in-process state machine for transient modal UI. Its
// zero value is closed.
type Modal struct {
	kind         Kind
	prompt       string
	placeholder  string
	force        bool
	action       func() tea.Cmd
	input        string
	inputMode    InputMode
	inputRaw     bool
	inputHeight  int
	inputCursor  int
	inputColumn  int
	inputErr     string
	validate     func(string) error
	submit       func(string) tea.Cmd
	selectItems  []SelectItem
	selectIndex  int
	selectLayout Layout
	formPurpose  string
	formTitle    string
	formFields   []FormField
	formFocus    int
	formErr      string
	formValidate func(FormValues) error
	formSubmit   func(FormValues) tea.Cmd
	diffKind     DiffKind
	diff         string
	text         string
	scroll       int
	request      uint64
}

type View struct {
	Kind         Kind
	Prompt       string
	Placeholder  string
	Force        bool
	Input        string
	InputMode    InputMode
	InputHeight  int
	InputCursor  int
	InputErr     string
	SelectItems  []SelectItem
	SelectIndex  int
	SelectLayout Layout
	Form         FormView
	DiffKind     DiffKind
	Diff         string
	Text         string
	Scroll       int
	Request      uint64
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

func OpenRawMultiLineInput(prompt, placeholder, initial string, validate func(string) error, submit func(string) tea.Cmd) Modal {
	m := OpenMultiLineInput(prompt, placeholder, initial, validate, submit)
	m.inputRaw = true
	return m
}

func (m Modal) WithInputHeight(height int) Modal {
	if height < 0 {
		height = 0
	}
	m.inputHeight = height
	return m
}

func OpenSelect(prompt string, items []SelectItem, selectedIndex int, submit func(string) tea.Cmd) Modal {
	return OpenSelectWithLayout(prompt, items, selectedIndex, Layout{Placement: PlacementCenter}, submit)
}

func OpenSelectWithLayout(prompt string, items []SelectItem, selectedIndex int, layout Layout, submit func(string) tea.Cmd) Modal {
	if selectedIndex < 0 || selectedIndex >= len(items) {
		selectedIndex = 0
	}
	copiedItems := append([]SelectItem(nil), items...)
	return Modal{
		kind:         Select,
		prompt:       prompt,
		selectItems:  copiedItems,
		selectIndex:  selectedIndex,
		selectLayout: normalizeLayout(layout),
		submit:       submit,
	}
}

func OpenForm(spec FormSpec) Modal {
	return Modal{
		kind:         Form,
		formPurpose:  spec.Purpose,
		formTitle:    spec.Title,
		formFields:   normalizeFormFields(spec.Fields),
		formValidate: spec.Validate,
		formSubmit:   spec.Submit,
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

func (m Modal) SetFormError(err string) Modal {
	if m.kind == Form {
		m.formErr = err
	}
	return m
}

func (m Modal) IsOpen() bool {
	return m.kind != None
}

func (m Modal) View() View {
	return View{
		Kind:         m.kind,
		Prompt:       m.prompt,
		Placeholder:  m.placeholder,
		Force:        m.force,
		Input:        m.input,
		InputMode:    m.inputMode,
		InputHeight:  m.inputHeight,
		InputCursor:  clampInputCursor(m.input, m.inputCursor),
		InputErr:     m.inputErr,
		SelectItems:  append([]SelectItem(nil), m.selectItems...),
		SelectIndex:  m.selectIndex,
		SelectLayout: m.selectLayout,
		Form: FormView{
			Purpose:    m.formPurpose,
			Title:      m.formTitle,
			Fields:     copyFormFields(m.formFields),
			FocusIndex: clampFormFocus(m.formFocus, len(m.formFields)),
			Error:      m.formErr,
		},
		DiffKind: m.diffKind,
		Diff:     m.diff,
		Text:     m.text,
		Scroll:   m.scroll,
		Request:  m.request,
	}
}

func normalizeFormFields(fields []FormField) []FormField {
	out := copyFormFields(fields)
	for i := range out {
		field := &out[i]
		switch field.Kind {
		case FormText, FormMultilineText:
			field.Cursor = clampInputCursor(field.Value, field.Cursor)
			if field.Value != "" && field.Cursor == 0 {
				field.Cursor = inputLength(field.Value)
			}
		case FormChoice:
			if field.SelectedIndex < 0 || field.SelectedIndex >= len(field.Options) {
				field.SelectedIndex = 0
			}
		}
	}
	return out
}

func copyFormFields(fields []FormField) []FormField {
	out := append([]FormField(nil), fields...)
	for i := range out {
		out[i].Options = append([]SelectItem(nil), out[i].Options...)
	}
	return out
}

func normalizeLayout(layout Layout) Layout {
	if layout.Width < 0 {
		layout.Width = 0
	}
	if layout.Height < 0 {
		layout.Height = 0
	}
	switch layout.Placement {
	case PlacementCenter, PlacementTopCenter, PlacementBottomCenter:
	default:
		layout.Placement = PlacementCenter
	}
	return layout
}

func (m Modal) Update(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	switch m.kind {
	case Confirm:
		return m.updateConfirm(msg)
	case Input:
		return m.updateInput(msg)
	case Select:
		return m.updateSelect(msg)
	case Form:
		return m.updateForm(msg)
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
		input := m.input
		if !m.inputRaw {
			input = strings.TrimSpace(input)
		}
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

func (m Modal) updateForm(msg tea.KeyMsg) (Modal, Outcome, tea.Cmd) {
	m.formFocus = clampFormFocus(m.formFocus, len(m.formFields))
	switch msg.String() {
	case "enter":
		values := m.formValues()
		if m.formValidate != nil {
			if err := m.formValidate(values); err != nil {
				m.formErr = err.Error()
				return m, Consumed, nil
			}
		}
		cmd := deferFormSubmit(m.formSubmit, values)
		if cmd == nil {
			return Modal{}, Accepted, nil
		}
		return Modal{}, Accepted, cmd
	case "esc", "ctrl+c":
		return Modal{}, Cancelled, nil
	case "tab":
		m.formFocus = nextSelectIndex(m.formFocus, len(m.formFields))
		return m, Consumed, nil
	case "shift+tab":
		m.formFocus = previousSelectIndex(m.formFocus, len(m.formFields))
		return m, Consumed, nil
	}

	if len(m.formFields) == 0 {
		return m, Consumed, nil
	}
	field := &m.formFields[m.formFocus]
	switch field.Kind {
	case FormText, FormMultilineText:
		return m.updateFormTextField(msg, field)
	case FormCheckbox:
		switch msg.String() {
		case "down":
			m.formFocus = nextSelectIndex(m.formFocus, len(m.formFields))
			return m, Consumed, nil
		case "up":
			m.formFocus = previousSelectIndex(m.formFocus, len(m.formFields))
			return m, Consumed, nil
		}
		if msg.Type == tea.KeySpace {
			field.Checked = !field.Checked
			m.formErr = ""
		}
	case FormChoice:
		switch msg.String() {
		case "down":
			m.formFocus = nextSelectIndex(m.formFocus, len(m.formFields))
			return m, Consumed, nil
		case "up":
			m.formFocus = previousSelectIndex(m.formFocus, len(m.formFields))
			return m, Consumed, nil
		case "left", "h":
			field.SelectedIndex = previousSelectIndex(field.SelectedIndex, len(field.Options))
			m.formErr = ""
		case "right", "l":
			field.SelectedIndex = nextSelectIndex(field.SelectedIndex, len(field.Options))
			m.formErr = ""
		default:
			if msg.Type == tea.KeySpace {
				field.SelectedIndex = nextSelectIndex(field.SelectedIndex, len(field.Options))
				m.formErr = ""
			}
		}
	}
	return m, Consumed, nil
}

func (m Modal) updateFormTextField(msg tea.KeyMsg, field *FormField) (Modal, Outcome, tea.Cmd) {
	field.Cursor = clampInputCursor(field.Value, field.Cursor)
	switch msg.String() {
	case "alt+enter":
		if field.Kind == FormMultilineText {
			field.Value, field.Cursor = insertRunes(field.Value, field.Cursor, []rune{'\n'})
		}
	case "backspace", "ctrl+h":
		field.Value, field.Cursor = deleteRuneBefore(field.Value, field.Cursor)
	case "delete":
		field.Value, field.Cursor = deleteRuneAt(field.Value, field.Cursor)
	case "left":
		if field.Cursor > 0 {
			field.Cursor--
		}
	case "right":
		if field.Cursor < inputLength(field.Value) {
			field.Cursor++
		}
	case "up":
		if field.Kind == FormMultilineText {
			field.Cursor, _ = moveCursorVertically(field.Value, field.Cursor, -1, -1)
		} else {
			m.formFocus = previousSelectIndex(m.formFocus, len(m.formFields))
		}
	case "down":
		if field.Kind == FormMultilineText {
			field.Cursor, _ = moveCursorVertically(field.Value, field.Cursor, 1, -1)
		} else {
			m.formFocus = nextSelectIndex(m.formFocus, len(m.formFields))
		}
	case "home", "ctrl+a":
		field.Cursor = 0
	case "end", "ctrl+e":
		field.Cursor = inputLength(field.Value)
	case "ctrl+u":
		field.Value = ""
		field.Cursor = 0
	default:
		if msg.Type == tea.KeySpace {
			field.Value, field.Cursor = insertRunes(field.Value, field.Cursor, []rune{' '})
		} else if msg.Type == tea.KeyRunes {
			field.Value, field.Cursor = insertRunes(field.Value, field.Cursor, msg.Runes)
		}
	}
	m.formErr = ""
	return m, Consumed, nil
}

func (m Modal) formValues() FormValues {
	values := FormValues{
		Text:    make(map[string]string),
		Checked: make(map[string]bool),
		Choice:  make(map[string]string),
	}
	for _, field := range m.formFields {
		switch field.Kind {
		case FormText, FormMultilineText:
			values.Text[field.ID] = strings.TrimSpace(field.Value)
		case FormCheckbox:
			values.Checked[field.ID] = field.Checked
		case FormChoice:
			value := ""
			if field.SelectedIndex >= 0 && field.SelectedIndex < len(field.Options) {
				value = field.Options[field.SelectedIndex].Value
			}
			values.Choice[field.ID] = value
		}
	}
	return values
}

func clampFormFocus(index, length int) int {
	if length == 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
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

func deferFormSubmit(submit func(FormValues) tea.Cmd, values FormValues) tea.Cmd {
	if submit == nil {
		return nil
	}
	return func() tea.Msg {
		cmd := submit(values)
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
