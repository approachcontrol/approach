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
	Diff
)

type DiffKind int

const (
	DiffStash DiffKind = iota + 1
	DiffBranch
	DiffCommit
	DiffWorktree
	DiffReflog
)

type Outcome int

const (
	Ignored Outcome = iota
	Consumed
	Accepted
	Cancelled
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
	inputErr    string
	validate    func(string) error
	submit      func(string) tea.Cmd
	diffKind    DiffKind
	diff        string
	scroll      int
	request     uint64
}

type View struct {
	Kind        Kind
	Prompt      string
	Placeholder string
	Force       bool
	Input       string
	InputErr    string
	DiffKind    DiffKind
	Diff        string
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
	return Modal{
		kind:        Input,
		prompt:      prompt,
		placeholder: placeholder,
		input:       initial,
		validate:    validate,
		submit:      submit,
	}
}

func OpenDiff(kind DiffKind, body string) Modal {
	return Modal{kind: Diff, diffKind: kind, diff: body}
}

func (m Modal) WithRequest(request uint64) Modal {
	if m.kind == Diff {
		m.request = request
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
		InputErr:    m.inputErr,
		DiffKind:    m.diffKind,
		Diff:        m.diff,
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
	case Diff:
		return m.updateDiff(msg)
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
	switch msg.String() {
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
		runes := []rune(m.input)
		if len(runes) > 0 {
			m.input = string(runes[:len(runes)-1])
			m.inputErr = ""
		}
		return m, Consumed, nil
	case "ctrl+u":
		m.input = ""
		m.inputErr = ""
		return m, Consumed, nil
	default:
		if msg.Type == tea.KeyRunes {
			m.input += string(msg.Runes)
			m.inputErr = ""
			return m, Consumed, nil
		}
		return m, Consumed, nil
	}
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
