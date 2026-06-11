package model

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

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

type realEmbeddedTerminal struct {
	term *embeddedterm.Terminal
}

func defaultEmbeddedTerminalStarter(ctx actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
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
	return t.term.VisibleLines(width, height, embeddedterm.Viewport{Mode: embeddedterm.ViewportLive})
}

func (t realEmbeddedTerminal) Write(p []byte) (int, error) { return t.term.Write(p) }
func (t realEmbeddedTerminal) Resize(width, height int) error {
	return t.term.Resize(width, height)
}
func (t realEmbeddedTerminal) Terminate() error { return t.term.Terminate() }
func (t realEmbeddedTerminal) State() string    { return string(t.term.State()) }

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
	return slot.Terminal.VisibleLines(m.contentWidth(), height)
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

func (m Model) openEmbeddedTerminal(ctx actions.AgentLaunchContext, record sessions.SessionRecord) (Model, bool) {
	number, ok := m.nextEmbeddedTerminalNumber()
	if !ok {
		m = m.setStatus(statusOther, "Maximum embedded terminals reached")
		return m, false
	}
	term, err := m.startEmbeddedTerminal(ctx, m.contentWidth(), m.embeddedTerminalContentHeight())
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, false
	}
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Number:   number,
		Provider: string(record.Provider),
		Identity: embeddedTerminalIdentity(record),
		Terminal: term,
	})
	m.activeEmbeddedTerminalNum = number
	return m, true
}

func (m Model) resizeEmbeddedTerminals() Model {
	if len(m.embeddedTerminals) == 0 {
		return m
	}
	width := m.contentWidth()
	height := m.embeddedTerminalContentHeight()
	for _, slot := range m.embeddedTerminals {
		if slot.Terminal == nil {
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
	next, _ = next.openEmbeddedTerminal(ctx, record)
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
	if msg.Type == tea.KeyRunes {
		return []byte(string(msg.Runes))
	}
	switch msg.String() {
	case "enter":
		return []byte{'\r'}
	case "backspace", "ctrl+h":
		return []byte{0x7f}
	case "tab":
		return []byte{'\t'}
	case "esc":
		return []byte{0x1b}
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "ctrl+c":
		return []byte{0x03}
	case "ctrl+d":
		return []byte{0x04}
	case "ctrl+u":
		return []byte{0x15}
	default:
		return nil
	}
}
