package model

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/embeddedterm"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

type EmbeddedTerminal interface {
	VisibleLines(width, height int) []string
	Write([]byte) (int, error)
	Resize(width, height int) error
	Terminate() error
	Wait(context.Context) error
	State() string
}

type EmbeddedTerminalStarter func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error)

type embeddedTerminalSlot struct {
	Number   int
	Provider string
	Identity string
	Terminal EmbeddedTerminal
}

type embeddedSessionPickerSelectedMsg struct {
	Index int
}

type terminateEmbeddedTerminalMsg struct {
	Number int
}

type quitEmbeddedTerminalsMsg struct{}

type embeddedTerminalTickMsg struct {
	Generation uint64
}

type realEmbeddedTerminal struct {
	term *embeddedterm.Terminal
}

func defaultEmbeddedTerminalStarter(ctx actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
	ctx.Embedded = true
	cmd, err := actions.AgentCommand(ctx)
	if err != nil {
		return nil, err
	}
	term, err := embeddedterm.NewManager().StartCommand(context.Background(), cmd, width, height)
	if err != nil {
		return nil, err
	}
	return realEmbeddedTerminal{term: term}, nil
}

func (t realEmbeddedTerminal) VisibleLines(width, height int) []string {
	return t.term.VisibleLines(width, height)
}

func (t realEmbeddedTerminal) Write(p []byte) (int, error) { return t.term.Write(p) }
func (t realEmbeddedTerminal) Resize(width, height int) error {
	return t.term.Resize(width, height)
}
func (t realEmbeddedTerminal) Terminate() error { return t.term.Terminate() }
func (t realEmbeddedTerminal) Wait(ctx context.Context) error {
	return t.term.Wait(ctx)
}
func (t realEmbeddedTerminal) State() string { return string(t.term.State()) }

const embeddedTerminalRepaintInterval = time.Second / 30

func (m Model) startEmbeddedTerminalTick() (Model, tea.Cmd) {
	m.embeddedTerminalTickGen++
	return m, m.embeddedTerminalTickCmd()
}

func (m Model) embeddedTerminalTickCmd() tea.Cmd {
	generation := m.embeddedTerminalTickGen
	return tea.Tick(embeddedTerminalRepaintInterval, func(time.Time) tea.Msg {
		return embeddedTerminalTickMsg{Generation: generation}
	})
}

func (m Model) activeEmbeddedTerminal() (embeddedTerminalSlot, int, bool) {
	for i, slot := range m.embeddedTerminals {
		if slot.Number == m.activeEmbeddedTerminalNum {
			return slot, i, true
		}
	}
	return embeddedTerminalSlot{}, -1, false
}

func (m Model) embeddedTerminalTabs() []ui.EmbeddedTerminalTab {
	tabs := make([]ui.EmbeddedTerminalTab, 0, len(m.embeddedTerminals))
	for _, slot := range m.embeddedTerminals {
		state := ""
		if slot.Terminal != nil {
			state = slot.Terminal.State()
		}
		tabs = append(tabs, ui.EmbeddedTerminalTab{
			Number:   slot.Number,
			Provider: slot.Provider,
			Identity: slot.Identity,
			State:    state,
			Active:   slot.Number == m.activeEmbeddedTerminalNum,
		})
	}
	return tabs
}

func (m Model) embeddedTerminalLines() []string {
	slot, _, ok := m.activeEmbeddedTerminal()
	if !ok || slot.Terminal == nil {
		return nil
	}
	height := m.embeddedTerminalContentHeight()
	return slot.Terminal.VisibleLines(m.embeddedTerminalWidth(), height)
}

func (m Model) embeddedTerminalWidth() int {
	width := m.contentWidth()
	if m.width >= ui.LeftPaneWidth+ui.ShortcutPaneWidth+ui.MinContentPaneWidth && m.height >= 3 {
		width -= ui.ShortcutPaneWidth
	}
	if width < 0 {
		return 0
	}
	return width
}

func (m Model) embeddedTerminalContentHeight() int {
	height := m.height - ui.BranchContentOverhead - 1
	if height <= 0 {
		return 1
	}
	return height
}

func (m Model) nextEmbeddedTerminalNumber() (int, bool) {
	used := make(map[int]struct{}, len(m.embeddedTerminals))
	for _, slot := range m.embeddedTerminals {
		used[slot.Number] = struct{}{}
	}
	for n := 1; n <= 9; n++ {
		if _, ok := used[n]; !ok {
			return n, true
		}
	}
	return 0, false
}

func (m Model) openEmbeddedTerminal(ctx actions.AgentLaunchContext, record sessions.SessionRecord) (Model, bool, error) {
	number, ok := m.nextEmbeddedTerminalNumber()
	if !ok {
		m = m.setStatus(statusOther, "Maximum embedded terminals reached")
		return m, false, nil
	}
	ctx.Embedded = true
	term, err := m.startEmbeddedTerminal(ctx, m.embeddedTerminalWidth(), m.embeddedTerminalContentHeight())
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, false, err
	}
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Number:   number,
		Provider: string(record.Provider),
		Identity: embeddedTerminalIdentity(record),
		Terminal: term,
	})
	m.activeEmbeddedTerminalNum = number
	return m, true, nil
}

func (m Model) resizeEmbeddedTerminals() Model {
	if len(m.embeddedTerminals) == 0 {
		return m
	}
	width := m.embeddedTerminalWidth()
	height := m.embeddedTerminalContentHeight()
	for _, slot := range m.embeddedTerminals {
		if slot.Terminal == nil {
			continue
		}
		if !embeddedTerminalRunning(slot.Terminal) {
			continue
		}
		if err := slot.Terminal.Resize(width, height); err != nil {
			m = m.setStatus(statusOther, err.Error())
		}
	}
	return m
}

func embeddedTerminalIdentity(record sessions.SessionRecord) string {
	for _, value := range []string{
		record.Branch,
		filepath.Base(record.WorktreePath),
		shortSessionID(record.SessionID),
	} {
		value = strings.TrimSpace(value)
		if value != "" && value != "." && value != string(filepath.Separator) {
			return value
		}
	}
	return "session"
}

func shortSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

func (m Model) switchEmbeddedTerminal(number int) Model {
	for _, slot := range m.embeddedTerminals {
		if slot.Number == number {
			m.activeEmbeddedTerminalNum = number
			return m
		}
	}
	return m.setStatus(statusOther, fmt.Sprintf("No embedded terminal %d", number))
}

func (m Model) handleEmbeddedTerminalKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.mode != ui.ModeSessions || len(m.embeddedTerminals) == 0 {
		return m, nil, false
	}
	key := msg.String()
	if m.terminalPrefixActive {
		m.terminalPrefixActive = false
		switch key {
		case "ctrl+g":
			return m.writeToActiveTerminal([]byte{0x07}), nil, true
		case "l":
			return m.openEmbeddedSessionPicker(), nil, true
		case "x":
			return m.handleEmbeddedTerminalClosePrefix(), nil, true
		case "q", "esc":
			next, cmd := m.handleEmbeddedTerminalQuitPrefix()
			return next, cmd, true
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			return m.switchEmbeddedTerminal(int(key[0] - '0')), nil, true
		default:
			return m.setStatus(statusOther, "Unknown terminal prefix command"), nil, true
		}
	}
	if key == "ctrl+g" {
		m.terminalPrefixActive = true
		return m, nil, true
	}
	return m.writeToActiveTerminal(keyBytes(msg)), nil, true
}

func (m Model) handleEmbeddedTerminalQuitPrefix() (Model, tea.Cmd) {
	if !m.hasRunningEmbeddedTerminal() {
		return m, tea.Quit
	}
	m.modal = modal.OpenConfirm("Terminate embedded terminals and quit?", func() tea.Cmd {
		return func() tea.Msg { return quitEmbeddedTerminalsMsg{} }
	})
	return m, nil
}

func (m Model) hasRunningEmbeddedTerminal() bool {
	for _, slot := range m.embeddedTerminals {
		if embeddedTerminalRunning(slot.Terminal) {
			return true
		}
	}
	return false
}

func (m Model) handleQuitEmbeddedTerminals() (Model, tea.Cmd) {
	for _, slot := range m.embeddedTerminals {
		if !embeddedTerminalRunning(slot.Terminal) {
			continue
		}
		if err := slot.Terminal.Terminate(); err != nil {
			return m.setStatus(statusOther, err.Error()), nil
		}
	}
	return m, tea.Quit
}

func (m Model) handleEmbeddedTerminalClosePrefix() Model {
	slot, _, ok := m.activeEmbeddedTerminal()
	if !ok {
		return m
	}
	if !embeddedTerminalRunning(slot.Terminal) {
		return m.dismissEmbeddedTerminal(slot.Number)
	}
	m.modal = modal.OpenConfirm("Terminate embedded terminal?", func() tea.Cmd {
		return func() tea.Msg { return terminateEmbeddedTerminalMsg{Number: slot.Number} }
	})
	return m
}

func embeddedTerminalRunning(term EmbeddedTerminal) bool {
	if term == nil {
		return false
	}
	switch term.State() {
	case "running", "starting":
		return true
	default:
		return false
	}
}

func (m Model) handleTerminateEmbeddedTerminal(msg terminateEmbeddedTerminalMsg) (Model, tea.Cmd) {
	for _, slot := range m.embeddedTerminals {
		if slot.Number != msg.Number || slot.Terminal == nil {
			continue
		}
		if err := slot.Terminal.Terminate(); err != nil {
			return m.setStatus(statusOther, err.Error()), nil
		}
		return m.dismissEmbeddedTerminal(msg.Number), nil
	}
	return m, nil
}

func (m Model) dismissEmbeddedTerminal(number int) Model {
	next := m.embeddedTerminals[:0]
	for _, slot := range m.embeddedTerminals {
		if slot.Number != number {
			next = append(next, slot)
		}
	}
	m.embeddedTerminals = next
	if len(m.embeddedTerminals) == 0 {
		m.activeEmbeddedTerminalNum = 0
		m.terminalPrefixActive = false
		m.embeddedTerminalTickGen++
		return m
	}
	if m.activeEmbeddedTerminalNum == number {
		m.activeEmbeddedTerminalNum = m.embeddedTerminals[0].Number
	}
	return m
}

func (m Model) openEmbeddedSessionPicker() Model {
	records := m.sessions.Items()
	items := make([]modal.SelectItem, 0, len(records))
	for i, record := range records {
		items = append(items, modal.SelectItem{
			Label: embeddedSessionPickerLabel(record),
			Value: strconv.Itoa(i),
		})
	}
	m.modal = modal.OpenSelect("Resume session", items, 0, func(value string) tea.Cmd {
		return func() tea.Msg {
			index, err := strconv.Atoi(value)
			if err != nil {
				index = -1
			}
			return embeddedSessionPickerSelectedMsg{Index: index}
		}
	})
	return m
}

func embeddedSessionPickerLabel(record sessions.SessionRecord) string {
	return strings.Join([]string{
		string(record.Provider),
		embeddedTerminalIdentity(record),
		strings.TrimSpace(record.Status),
	}, " ")
}

func (m Model) handleEmbeddedSessionPickerSelected(msg embeddedSessionPickerSelectedMsg) (Model, tea.Cmd) {
	records := m.sessions.Items()
	if msg.Index < 0 || msg.Index >= len(records) {
		return m.setStatus(statusOther, "Selected session is unavailable"), nil
	}
	record := records[msg.Index]
	ctx, ok, next := m.sessionResumeLaunchContext(record)
	if !ok {
		return next, nil
	}
	if ctx.Command == agent.CommandCodexApp {
		return next.launchAgentWithContext(ctx)
	}
	needsTick := !next.hasRunningEmbeddedTerminal()
	var opened bool
	var err error
	next, opened, err = next.openEmbeddedTerminal(ctx, record)
	if err != nil && embeddedterm.IsUnsupported(err) {
		return next.launchAgentWithContext(ctx)
	}
	if opened && needsTick {
		return next.startEmbeddedTerminalTick()
	}
	return next, nil
}

func (m Model) writeToActiveTerminal(p []byte) Model {
	if len(p) == 0 {
		return m
	}
	slot, _, ok := m.activeEmbeddedTerminal()
	if !ok || slot.Terminal == nil {
		return m
	}
	if _, err := slot.Terminal.Write(p); err != nil {
		return m.setStatus(statusOther, err.Error())
	}
	return m
}

func keyBytes(msg tea.KeyMsg) []byte {
	p := baseKeyBytes(msg)
	if len(p) == 0 || !msg.Alt {
		return p
	}
	return append([]byte{0x1b}, p...)
}

func baseKeyBytes(msg tea.KeyMsg) []byte {
	if msg.Type == tea.KeyRunes {
		return []byte(string(msg.Runes))
	}
	switch msg.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyBackspace, tea.KeyCtrlH:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyCtrlUp:
		return []byte("\x1b[1;5A")
	case tea.KeyCtrlDown:
		return []byte("\x1b[1;5B")
	case tea.KeyCtrlRight:
		return []byte("\x1b[1;5C")
	case tea.KeyCtrlLeft:
		return []byte("\x1b[1;5D")
	case tea.KeyCtrlHome:
		return []byte("\x1b[1;5H")
	case tea.KeyCtrlEnd:
		return []byte("\x1b[1;5F")
	case tea.KeyCtrlPgUp:
		return []byte("\x1b[5;5~")
	case tea.KeyCtrlPgDown:
		return []byte("\x1b[6;5~")
	case tea.KeyShiftUp:
		return []byte("\x1b[1;2A")
	case tea.KeyShiftDown:
		return []byte("\x1b[1;2B")
	case tea.KeyShiftRight:
		return []byte("\x1b[1;2C")
	case tea.KeyShiftLeft:
		return []byte("\x1b[1;2D")
	case tea.KeyShiftHome:
		return []byte("\x1b[1;2H")
	case tea.KeyShiftEnd:
		return []byte("\x1b[1;2F")
	case tea.KeyCtrlShiftUp:
		return []byte("\x1b[1;6A")
	case tea.KeyCtrlShiftDown:
		return []byte("\x1b[1;6B")
	case tea.KeyCtrlShiftRight:
		return []byte("\x1b[1;6C")
	case tea.KeyCtrlShiftLeft:
		return []byte("\x1b[1;6D")
	case tea.KeyCtrlShiftHome:
		return []byte("\x1b[1;6H")
	case tea.KeyCtrlShiftEnd:
		return []byte("\x1b[1;6F")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	default:
		if msg.Type >= 0 && msg.Type <= 31 {
			return []byte{byte(msg.Type)}
		}
		if msg.Type == tea.KeyCtrlQuestionMark {
			return []byte{0x7f}
		}
		return nil
	}
}
