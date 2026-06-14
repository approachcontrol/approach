package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/embeddedterm"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

const embeddedTerminalTerminatePrompt = "Terminate embedded terminal?"

const (
	embeddedPromptPasteStart = "\x1b[200~"
	embeddedPromptPasteEnd   = "\x1b[201~"
)

var (
	embeddedPromptPrefillTimeout      = 2 * time.Second
	embeddedPromptPrefillPollInterval = 25 * time.Millisecond
)

// terminalCommandKey toggles wtui command handling inside embedded terminals.
// It must stay off keys interactive agents bind themselves (Claude Code and
// Codex both use ctrl+g, ctrl+b, ctrl+r, ctrl+t, ...); ctrl+] is the
// telnet-style escape neither claims.
const (
	terminalCommandKey         = "ctrl+]"
	terminalCommandLiteralByte = 0x1d
)

type EmbeddedTerminal interface {
	VisibleLines(width, height int) []string
	Write([]byte) (int, error)
	Resize(width, height int) error
	Terminate() error
	Wait(context.Context) error
	State() string
}

type detachableEmbeddedTerminal interface {
	Detach() error
	DetachTarget() string
}

var errEmbeddedTerminalDetachUnavailable = errors.New("detach unavailable: tmux was not available when this terminal started")

type EmbeddedTerminalStarter func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error)

type embeddedTerminalScope string

const (
	embeddedTerminalScopeSession embeddedTerminalScope = "session"
	embeddedTerminalScopeFlow    embeddedTerminalScope = "flow"
)

type flowFocus int

const (
	flowFocusList flowFocus = iota
	flowFocusTerminal
)

type embeddedTerminalID int

type embeddedTerminalSlot struct {
	Number       int
	Scope        embeddedTerminalScope
	Provider     string
	Identity     string
	RepoPath     string
	WorktreePath string
	WorkingDir   string
	FlowID       string
	FlowPhaseID  string
	LaunchID     string
	Terminal     EmbeddedTerminal
	ID           embeddedTerminalID
}

type embeddedSessionPickerSelectedMsg struct {
	Index int
}

type terminateEmbeddedTerminalMsg struct {
	ID embeddedTerminalID
}

type quitEmbeddedTerminalsMsg struct{}

type embeddedTerminalTickMsg struct {
	Generation uint64
}

type realEmbeddedTerminal struct {
	term realEmbeddedTerminalRuntime
}

type realEmbeddedTerminalRuntime interface {
	VisibleLines(width, height int) []string
	Write([]byte) (int, error)
	Resize(width, height int) error
	Terminate() error
	Wait(context.Context) error
	State() embeddedterm.State
}

func defaultEmbeddedTerminalStarter(ctx actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
	ctx.Embedded = true
	tmuxSpec, err := actions.EmbeddedTmuxAgentCommand(ctx)
	if err == nil {
		term, err := embeddedterm.StartTmuxBackedAgent(context.Background(), tmuxSpec, width, height)
		if err != nil {
			return nil, err
		}
		return realEmbeddedTerminal{term: term}, nil
	}
	if !errors.Is(err, actions.ErrEmbeddedTmuxUnavailable) {
		return nil, err
	}
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
func (t realEmbeddedTerminal) Detach() error {
	detachable, ok := t.term.(interface{ Detach() error })
	if !ok {
		return errEmbeddedTerminalDetachUnavailable
	}
	return detachable.Detach()
}
func (t realEmbeddedTerminal) DetachTarget() string {
	detachable, ok := t.term.(interface{ DetachTarget() string })
	if !ok {
		return ""
	}
	return detachable.DetachTarget()
}

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

func (m Model) activeEmbeddedTerminalForScope(scope embeddedTerminalScope) (embeddedTerminalSlot, int, bool) {
	activeNum := m.activeEmbeddedTerminalNumber(scope)
	for i, slot := range m.embeddedTerminals {
		if slot.Scope == scope && slot.Number == activeNum {
			return slot, i, true
		}
	}
	for i, slot := range m.embeddedTerminals {
		if slot.Scope == scope {
			return slot, i, true
		}
	}
	return embeddedTerminalSlot{}, -1, false
}

func (m Model) activeEmbeddedTerminalNumber(scope embeddedTerminalScope) int {
	if scope == embeddedTerminalScopeFlow {
		return m.activeFlowTerminalNum
	}
	return m.activeEmbeddedTerminalNum
}

func (m Model) embeddedTerminalTabs() []ui.EmbeddedTerminalTab {
	return m.embeddedTerminalTabsForScope(embeddedTerminalScopeSession)
}

func (m Model) flowEmbeddedTerminalTabs() []ui.EmbeddedTerminalTab {
	return m.embeddedTerminalTabsForScope(embeddedTerminalScopeFlow)
}

func (m Model) flowTerminalActivity() []ui.FlowTerminalActivity {
	activity := make([]ui.FlowTerminalActivity, 0, len(m.embeddedTerminals))
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || !embeddedTerminalRunning(slot.Terminal) || slot.FlowID == "" {
			continue
		}
		activity = append(activity, ui.FlowTerminalActivity{
			FlowID:  slot.FlowID,
			PhaseID: slot.FlowPhaseID,
		})
	}
	return activity
}

func (m Model) activeTerminalRepoPaths() map[string]bool {
	active := make(map[string]bool)
	for _, slot := range m.embeddedTerminals {
		repoPath := cleanEmbeddedTerminalRepoPath(slot.RepoPath)
		if repoPath == "" || !embeddedTerminalRunning(slot.Terminal) {
			continue
		}
		active[repoPath] = true
	}
	return active
}

func (m Model) syncActiveFlowTerminalToSelectedFlow() Model {
	if m.mode != ui.ModeFlows {
		return m
	}
	flowID := m.selectedFlowID()
	if flowID == "" {
		return m
	}
	activeNum := m.activeFlowTerminalNum
	newestMatchingNum := 0
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || slot.FlowID != flowID || !embeddedTerminalRunning(slot.Terminal) {
			continue
		}
		if slot.Number == activeNum {
			return m
		}
		if slot.Number > newestMatchingNum {
			newestMatchingNum = slot.Number
		}
	}
	if newestMatchingNum != 0 {
		m.activeFlowTerminalNum = newestMatchingNum
	}
	return m
}

func (m Model) embeddedTerminalTabsForScope(scope embeddedTerminalScope) []ui.EmbeddedTerminalTab {
	tabs := make([]ui.EmbeddedTerminalTab, 0, len(m.embeddedTerminals))
	activeNum := m.activeEmbeddedTerminalNumber(scope)
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != scope {
			continue
		}
		state := ""
		if slot.Terminal != nil {
			state = slot.Terminal.State()
		}
		tabs = append(tabs, ui.EmbeddedTerminalTab{
			Number:   slot.Number,
			Provider: slot.Provider,
			Identity: slot.Identity,
			State:    state,
			Active:   slot.Number == activeNum,
		})
	}
	return tabs
}

func (m Model) embeddedTerminalLines() []string {
	return m.embeddedTerminalLinesForScope(embeddedTerminalScopeSession)
}

func (m Model) flowEmbeddedTerminalLines() []string {
	return m.embeddedTerminalLinesForScope(embeddedTerminalScopeFlow)
}

func (m Model) embeddedTerminalLinesForScope(scope embeddedTerminalScope) []string {
	slot, _, ok := m.activeEmbeddedTerminalForScope(scope)
	if !ok || slot.Terminal == nil {
		return nil
	}
	height := m.embeddedTerminalContentHeightForScope(scope)
	return slot.Terminal.VisibleLines(m.embeddedTerminalWidth(), height)
}

func (m Model) embeddedTerminalOuterWidth() int {
	return ui.RightContentWidth(m.width, m.height, m.searchActive)
}

func (m Model) embeddedTerminalWidth() int {
	return ui.EmbeddedTerminalPTYWidth(m.embeddedTerminalOuterWidth())
}

func (m Model) embeddedTerminalOuterHeight() int {
	height := m.height - ui.BranchContentOverhead
	if height > 0 {
		return height
	}
	return 0
}

func (m Model) embeddedTerminalContentHeight() int {
	return ui.EmbeddedTerminalPTYHeight(m.embeddedTerminalOuterHeight())
}

func (m Model) embeddedTerminalContentHeightForScope(scope embeddedTerminalScope) int {
	if scope == embeddedTerminalScopeFlow {
		return m.flowEmbeddedTerminalContentHeight()
	}
	return m.embeddedTerminalContentHeight()
}

func (m Model) flowEmbeddedTerminalContentHeight() int {
	_, terminalHeight := ui.FlowSplitPanelHeights(m.embeddedTerminalOuterHeight())
	return ui.EmbeddedTerminalPTYHeight(terminalHeight)
}

func (m Model) nextEmbeddedTerminalNumber(scope embeddedTerminalScope) (int, bool) {
	if len(m.embeddedTerminals) >= 9 {
		return 0, false
	}
	used := make(map[int]struct{}, len(m.embeddedTerminals))
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != scope {
			continue
		}
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
	return m.openEmbeddedTerminalWithLabel(ctx, embeddedTerminalScopeSession, string(record.Provider), embeddedTerminalIdentity(record), "", "", m.embeddedTerminalWidth(), m.embeddedTerminalContentHeight())
}

func (m Model) openFlowEmbeddedTerminal(ctx actions.AgentLaunchContext) (Model, bool, error) {
	return m.openEmbeddedTerminalWithLabel(ctx, embeddedTerminalScopeFlow, ctx.Command, flowEmbeddedTerminalIdentity(ctx), ctx.FlowID, ctx.FlowPhaseID, m.embeddedTerminalWidth(), m.flowEmbeddedTerminalContentHeight())
}

func (m Model) openEmbeddedTerminalWithLabel(ctx actions.AgentLaunchContext, scope embeddedTerminalScope, provider, identity, flowID, flowPhaseID string, width, height int) (Model, bool, error) {
	number, ok := m.nextEmbeddedTerminalNumber(scope)
	if !ok {
		m = m.setStatus(statusOther, "Maximum embedded terminals reached")
		return m, false, nil
	}
	ctx.Embedded = true
	term, err := m.startEmbeddedTerminal(ctx, width, height)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, false, err
	}
	if err := prefillEmbeddedPromptIfNeeded(term, ctx); err != nil {
		if terminateErr := term.Terminate(); terminateErr != nil {
			err = errors.Join(err, fmt.Errorf("terminate embedded terminal after prefill failure: %w", terminateErr))
		}
		m = m.setStatus(statusOther, err.Error())
		return m, false, err
	}
	m.nextEmbeddedTerminalID++
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Number:       number,
		Scope:        scope,
		Provider:     provider,
		Identity:     identity,
		RepoPath:     cleanEmbeddedTerminalRepoPath(ctx.RepoPath),
		WorktreePath: cleanEmbeddedTerminalPath(ctx.WorktreePath),
		WorkingDir:   cleanEmbeddedTerminalPath(ctx.WorkingDir),
		FlowID:       flowID,
		FlowPhaseID:  flowPhaseID,
		LaunchID:     strings.TrimSpace(ctx.LaunchID),
		Terminal:     term,
		ID:           embeddedTerminalID(m.nextEmbeddedTerminalID),
	})
	if scope == embeddedTerminalScopeFlow {
		m.activeFlowTerminalNum = number
	} else {
		m.activeEmbeddedTerminalNum = number
	}
	return m, true, nil
}

func prefillEmbeddedPromptIfNeeded(term EmbeddedTerminal, ctx actions.AgentLaunchContext) error {
	if !actions.ShouldPrefillEmbeddedPrompt(ctx) {
		return nil
	}
	waitForEmbeddedPromptPrefillReady(term)
	payload := []byte(embeddedPromptPasteStart + sanitizeEmbeddedPromptPaste(ctx.InitialPrompt) + embeddedPromptPasteEnd)
	n, err := term.Write(payload)
	if err != nil {
		return fmt.Errorf("prefill embedded prompt: %w", err)
	}
	if n != len(payload) {
		return fmt.Errorf("prefill embedded prompt: %w: wrote %d of %d bytes", io.ErrShortWrite, n, len(payload))
	}
	return nil
}

func waitForEmbeddedPromptPrefillReady(term EmbeddedTerminal) {
	if term == nil || embeddedPromptPrefillTimeout <= 0 {
		return
	}
	deadline := time.Now().Add(embeddedPromptPrefillTimeout)
	for {
		lines := term.VisibleLines(80, 24)
		if len(lines) == 0 || embeddedTerminalHasVisibleOutput(lines) {
			return
		}
		if !embeddedTerminalRunning(term) || !time.Now().Before(deadline) {
			return
		}
		if embeddedPromptPrefillPollInterval > 0 {
			time.Sleep(embeddedPromptPrefillPollInterval)
		}
	}
}

func embeddedTerminalHasVisibleOutput(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

func sanitizeEmbeddedPromptPaste(prompt string) string {
	var b strings.Builder
	b.Grow(len(prompt))
	for i := 0; i < len(prompt); {
		c := prompt[i]
		if c == 0x1b {
			i = skipTerminalEscape(prompt, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(prompt[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		i += size
		switch r {
		case '\n', '\t':
			b.WriteRune(r)
		default:
			if !unicode.IsControl(r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func skipTerminalEscape(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
		return len(s)
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default:
		return i + 2
	}
}

func cleanEmbeddedTerminalRepoPath(repoPath string) string {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return ""
	}
	return filepath.Clean(repoPath)
}

func cleanEmbeddedTerminalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func (m Model) resizeEmbeddedTerminals() Model {
	if len(m.embeddedTerminals) == 0 {
		return m
	}
	width := m.embeddedTerminalWidth()
	for _, slot := range m.embeddedTerminals {
		if slot.Terminal == nil {
			continue
		}
		if !embeddedTerminalRunning(slot.Terminal) {
			continue
		}
		height := m.embeddedTerminalContentHeightForScope(slot.Scope)
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

func flowEmbeddedTerminalIdentity(ctx actions.AgentLaunchContext) string {
	for _, value := range []string{
		ctx.FlowPhaseID,
		ctx.FlowID,
		filepath.Base(ctx.WorktreePath),
	} {
		value = strings.TrimSpace(value)
		if value != "" && value != "." && value != string(filepath.Separator) {
			return value
		}
	}
	return "flow"
}

func shortSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

func (m Model) switchEmbeddedTerminalForScope(scope embeddedTerminalScope, number int) Model {
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == scope && slot.Number == number {
			if scope == embeddedTerminalScopeFlow {
				m.activeFlowTerminalNum = number
				return m
			}
			m.activeEmbeddedTerminalNum = number
			return m
		}
	}
	return m.setStatus(statusOther, fmt.Sprintf("No embedded terminal %d", number))
}

func (m Model) cycleEmbeddedTerminalForScope(scope embeddedTerminalScope, direction int) Model {
	if direction == 0 {
		return m
	}
	activeNum := m.activeEmbeddedTerminalNumber(scope)
	numbers := make([]int, 0, len(m.embeddedTerminals))
	activeIndex := -1
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != scope {
			continue
		}
		if slot.Number == activeNum {
			activeIndex = len(numbers)
		}
		numbers = append(numbers, slot.Number)
	}
	if len(numbers) < 2 {
		return m
	}
	if activeIndex < 0 {
		activeIndex = 0
	}
	nextIndex := activeIndex + direction
	if nextIndex < 0 {
		nextIndex = len(numbers) - 1
	}
	if nextIndex >= len(numbers) {
		nextIndex = 0
	}
	return m.switchEmbeddedTerminalForScope(scope, numbers[nextIndex])
}

func (m Model) handleEmbeddedTerminalKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.mode == ui.ModeSessions && m.hasEmbeddedTerminalForScope(embeddedTerminalScopeSession) {
		return m.handleEmbeddedTerminalKeyForScope(msg, embeddedTerminalScopeSession)
	}
	if m.mode == ui.ModeFlows && m.activePane == 1 && m.flowFocus == flowFocusTerminal && m.hasEmbeddedTerminalForScope(embeddedTerminalScopeFlow) {
		return m.handleEmbeddedTerminalKeyForScope(msg, embeddedTerminalScopeFlow)
	}
	return m, nil, false
}

func (m Model) handleEmbeddedTerminalKeyForScope(msg tea.KeyMsg, scope embeddedTerminalScope) (Model, tea.Cmd, bool) {
	key := msg.String()
	if scope == embeddedTerminalScopeFlow {
		if m.terminalPrefixActive {
			switch key {
			case "tab":
				m.flowFocus = flowFocusList
				m.terminalPrefixActive = false
				return m, nil, true
			case "left":
				return m.cycleEmbeddedTerminalForScope(scope, -1), nil, true
			case "right":
				return m.cycleEmbeddedTerminalForScope(scope, 1), nil, true
			case terminalCommandKey:
				return m.writeToActiveTerminalForScope(scope, []byte{terminalCommandLiteralByte}), nil, true
			case "i":
				m.terminalPrefixActive = false
				return m, nil, true
			case "x":
				return m.handleEmbeddedTerminalClosePrefix(scope), nil, true
			case "d":
				next, cmd := m.handleEmbeddedTerminalDetachPrefix(scope)
				return next, cmd, true
			case "q", "esc":
				next, cmd := m.handleEmbeddedTerminalQuitPrefix()
				return next, cmd, true
			case "1", "2", "3", "4", "5", "6", "7", "8", "9":
				return m.switchEmbeddedTerminalForScope(scope, int(key[0]-'0')), nil, true
			default:
				return m.setStatus(statusOther, "Unknown terminal prefix command"), nil, true
			}
		}
		if key == terminalCommandKey {
			m.terminalPrefixActive = true
			return m, nil, true
		}
		return m.writeToActiveTerminalForScope(scope, keyBytes(msg)), nil, true
	}
	if m.terminalPrefixActive {
		m.terminalPrefixActive = false
		switch key {
		case terminalCommandKey:
			return m.writeToActiveTerminalForScope(scope, []byte{terminalCommandLiteralByte}), nil, true
		case "l":
			if scope == embeddedTerminalScopeSession {
				return m.openEmbeddedSessionPicker(), nil, true
			}
			return m.setStatus(statusOther, "Unknown terminal prefix command"), nil, true
		case "x":
			return m.handleEmbeddedTerminalClosePrefix(scope), nil, true
		case "d":
			next, cmd := m.handleEmbeddedTerminalDetachPrefix(scope)
			return next, cmd, true
		case "q", "esc":
			next, cmd := m.handleEmbeddedTerminalQuitPrefix()
			return next, cmd, true
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			return m.switchEmbeddedTerminalForScope(scope, int(key[0]-'0')), nil, true
		default:
			return m.setStatus(statusOther, "Unknown terminal prefix command"), nil, true
		}
	}
	if key == terminalCommandKey {
		m.terminalPrefixActive = true
		return m, nil, true
	}
	return m.writeToActiveTerminalForScope(scope, keyBytes(msg)), nil, true
}

func (m Model) hasEmbeddedTerminalForScope(scope embeddedTerminalScope) bool {
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == scope {
			return true
		}
	}
	return false
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

func (m Model) handleEmbeddedTerminalClosePrefix(scope embeddedTerminalScope) Model {
	slot, _, ok := m.activeEmbeddedTerminalForScope(scope)
	if !ok {
		return m
	}
	if !embeddedTerminalRunning(slot.Terminal) {
		return m.dismissEmbeddedTerminal(slot.ID)
	}
	m.terminalConfirmID = slot.ID
	m.terminalConfirmScope = slot.Scope
	m.modal = modal.OpenConfirm(embeddedTerminalTerminatePrompt, func() tea.Cmd {
		return func() tea.Msg { return terminateEmbeddedTerminalMsg{ID: slot.ID} }
	})
	return m
}

func (m Model) handleEmbeddedTerminalDetachPrefix(scope embeddedTerminalScope) (Model, tea.Cmd) {
	slot, _, ok := m.activeEmbeddedTerminalForScope(scope)
	if !ok || slot.Terminal == nil {
		return m, nil
	}
	detachable, ok := slot.Terminal.(detachableEmbeddedTerminal)
	if !ok {
		return m.setStatus(statusOther, "Detach unavailable: tmux was not available when this terminal started"), nil
	}
	target := strings.TrimSpace(detachable.DetachTarget())
	if err := detachable.Detach(); err != nil {
		if errors.Is(err, errEmbeddedTerminalDetachUnavailable) {
			return m.setStatus(statusOther, "Detach unavailable: tmux was not available when this terminal started"), nil
		}
		return m.setStatus(statusOther, err.Error()), nil
	}
	m = m.dismissEmbeddedTerminal(slot.ID)
	if target == "" {
		target = "tmux"
	}
	cwd := slot.detachHandoffCWD()
	launch, err := m.launchDetachedTerminal(target, cwd)
	if err != nil {
		return m.setStatus(statusOther, "Detached embedded terminal, but failed to open terminal: "+err.Error()), nil
	}
	return m.setStatus(statusOther, "Detached embedded terminal; opening terminal: "+target), runEmbeddedTerminalDetachHandoff(target, launch)
}

func (slot embeddedTerminalSlot) detachHandoffCWD() string {
	if slot.WorkingDir != "" {
		return slot.WorkingDir
	}
	if slot.WorktreePath != "" {
		return slot.WorktreePath
	}
	return slot.RepoPath
}

func runEmbeddedTerminalDetachHandoff(target string, launch actions.TerminalLaunchSpec) tea.Cmd {
	return func() tea.Msg {
		if launch.Cmd == nil {
			return EmbeddedTerminalDetachHandoffResultMsg{Target: target, Err: "detached terminal handoff command is nil"}
		}
		if err := launch.Cmd.Run(); err != nil {
			if launch.Cleanup != nil {
				launch.Cleanup()
			}
			return EmbeddedTerminalDetachHandoffResultMsg{Target: target, Err: err.Error()}
		}
		return EmbeddedTerminalDetachHandoffResultMsg{Target: target}
	}
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

func (m Model) dismissExitedFlowEmbeddedTerminals() Model {
	ids := make([]embeddedTerminalID, 0)
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || slot.Terminal == nil {
			continue
		}
		if flowEmbeddedTerminalAutoCloses(slot.Terminal.State()) {
			ids = append(ids, slot.ID)
		}
	}
	for _, id := range ids {
		m = m.dismissEmbeddedTerminal(id)
	}
	return m
}

func (m Model) exitedFlowEmbeddedTerminalAutoCloseKeys() []deferredAutoFlowLaunchKey {
	keys := make([]deferredAutoFlowLaunchKey, 0)
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || slot.Terminal == nil {
			continue
		}
		if !flowEmbeddedTerminalAutoCloses(slot.Terminal.State()) {
			continue
		}
		key, ok := newDeferredAutoFlowLaunchKey(slot.FlowID, slot.FlowPhaseID)
		if ok {
			keys = append(keys, key)
		}
	}
	return keys
}

func flowEmbeddedTerminalAutoCloses(state string) bool {
	return state == "exited"
}

func (m Model) handleTerminateEmbeddedTerminal(msg terminateEmbeddedTerminalMsg) (Model, tea.Cmd) {
	for _, slot := range m.embeddedTerminals {
		if slot.ID != msg.ID || slot.Terminal == nil {
			continue
		}
		if err := slot.Terminal.Terminate(); err != nil {
			return m.setStatus(statusOther, err.Error()), nil
		}
		return m.dismissEmbeddedTerminal(msg.ID), nil
	}
	return m, nil
}

func (m Model) dismissEmbeddedTerminal(id embeddedTerminalID) Model {
	var removedScope embeddedTerminalScope
	removed := false
	prefixScope, prefixActive := m.embeddedTerminalPrefixScope()
	activeID := m.activeEmbeddedTerminalIDForScope(embeddedTerminalScopeSession)
	activeFlowID := m.activeEmbeddedTerminalIDForScope(embeddedTerminalScopeFlow)
	flowTerminalFocused := m.mode == ui.ModeFlows && m.activePane == 1 && m.flowFocus == flowFocusTerminal
	next := m.embeddedTerminals[:0]
	for _, slot := range m.embeddedTerminals {
		if slot.ID != id {
			next = append(next, slot)
		} else {
			m = m.clearDeferredAutoFlowLaunchForTerminal(slot)
			removedScope = slot.Scope
			removed = true
		}
	}
	if !removed {
		return m
	}
	m.embeddedTerminals = next
	m.renumberEmbeddedTerminalsForScope(removedScope)
	m = m.clearEmbeddedTerminalConfirmFor(id, removedScope)
	if len(m.embeddedTerminals) == 0 {
		m.activeEmbeddedTerminalNum = 0
		m.activeFlowTerminalNum = 0
		m.flowFocus = flowFocusList
		m.terminalPrefixActive = false
		m.embeddedTerminalTickGen++
		return m
	}
	if removedScope == embeddedTerminalScopeFlow {
		m.activeFlowTerminalNum = m.activeEmbeddedTerminalNumberAfterRenumber(embeddedTerminalScopeFlow, activeFlowID, id)
		if m.activeFlowTerminalNum == 0 {
			m.flowFocus = flowFocusList
			if flowTerminalFocused {
				m.terminalPrefixActive = false
			}
		} else if flowTerminalFocused {
			m.terminalPrefixActive = true
		}
	} else {
		if prefixActive && prefixScope == embeddedTerminalScopeSession && activeID == id {
			m.terminalPrefixActive = false
		}
		m.activeEmbeddedTerminalNum = m.activeEmbeddedTerminalNumberAfterRenumber(embeddedTerminalScopeSession, activeID, id)
	}
	return m
}

func (m Model) clearEmbeddedTerminalConfirmFor(id embeddedTerminalID, scope embeddedTerminalScope) Model {
	if m.terminalConfirmID != id || m.terminalConfirmScope != scope {
		return m
	}
	if view := m.modal.View(); view.Kind == modal.Confirm && view.Prompt == embeddedTerminalTerminatePrompt {
		m.modal = modal.Modal{}
	}
	return m.clearEmbeddedTerminalConfirm()
}

func (m Model) clearEmbeddedTerminalConfirm() Model {
	m.terminalConfirmID = 0
	m.terminalConfirmScope = ""
	return m
}

func (m Model) embeddedTerminalPrefixScope() (embeddedTerminalScope, bool) {
	if !m.terminalPrefixActive {
		return "", false
	}
	if m.mode == ui.ModeSessions && m.hasEmbeddedTerminalForScope(embeddedTerminalScopeSession) {
		return embeddedTerminalScopeSession, true
	}
	if m.mode == ui.ModeFlows && m.activePane == 1 && m.flowFocus == flowFocusTerminal && m.hasEmbeddedTerminalForScope(embeddedTerminalScopeFlow) {
		return embeddedTerminalScopeFlow, true
	}
	return "", false
}

func (m Model) activeEmbeddedTerminalIDForScope(scope embeddedTerminalScope) embeddedTerminalID {
	slot, _, ok := m.activeEmbeddedTerminalForScope(scope)
	if !ok {
		return 0
	}
	return slot.ID
}

func (m *Model) renumberEmbeddedTerminalsForScope(scope embeddedTerminalScope) {
	nextNumber := 1
	for i := range m.embeddedTerminals {
		if m.embeddedTerminals[i].Scope != scope {
			continue
		}
		m.embeddedTerminals[i].Number = nextNumber
		nextNumber++
	}
}

func (m Model) activeEmbeddedTerminalNumberAfterRenumber(scope embeddedTerminalScope, previousActiveID, removedID embeddedTerminalID) int {
	if previousActiveID != 0 && previousActiveID != removedID {
		if number := m.embeddedTerminalNumberForID(previousActiveID); number != 0 {
			return number
		}
	}
	return m.firstEmbeddedTerminalNumberForScope(scope)
}

func (m Model) embeddedTerminalNumberForID(id embeddedTerminalID) int {
	for _, slot := range m.embeddedTerminals {
		if slot.ID == id {
			return slot.Number
		}
	}
	return 0
}

func (m Model) firstEmbeddedTerminalNumberForScope(scope embeddedTerminalScope) int {
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == scope {
			return slot.Number
		}
	}
	return 0
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
	m.modal = modal.OpenSelectWithLayout("Resume session", items, 0, modal.Layout{Width: 72, Height: 12, Placement: modal.PlacementCenter}, func(value string) tea.Cmd {
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
	return next.resumeSessionInEmbeddedTerminal(ctx, record)
}

func (m Model) resumeSessionInEmbeddedTerminal(ctx actions.AgentLaunchContext, record sessions.SessionRecord) (Model, tea.Cmd) {
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err := m.openEmbeddedTerminal(ctx, record)
	if err != nil && embeddedterm.IsUnsupported(err) {
		return next.launchAgentWithContext(ctx)
	}
	if opened && needsTick {
		return next.startEmbeddedTerminalTick()
	}
	return next, nil
}

func (m Model) writeToActiveTerminalForScope(scope embeddedTerminalScope, p []byte) Model {
	if len(p) == 0 {
		return m
	}
	slot, _, ok := m.activeEmbeddedTerminalForScope(scope)
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
