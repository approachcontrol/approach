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

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/claudestream"
	"github.com/approachcontrol/approach/embeddedterm"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
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

// terminalCommandKey toggles approach command handling inside embedded terminals.
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

// errEmbeddedTerminalDetachUnavailable marks a terminal that is not tmux-backed
// and so cannot be detached: either tmux was unavailable when it started, or it
// is a headless claude stream-json terminal that intentionally runs inline.
var errEmbeddedTerminalDetachUnavailable = errors.New("detach unavailable: this terminal is not tmux-backed")

type EmbeddedTerminalStarter func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error)

type embeddedTerminalScope string

const (
	embeddedTerminalScopeSession embeddedTerminalScope = "session"
	embeddedTerminalScopeFlow    embeddedTerminalScope = "flow"
)

type terminalFocus int

const (
	terminalFocusList terminalFocus = iota
	terminalFocusTerminal
)

type embeddedTerminalID int

type embeddedTerminalRemovalReason int

const (
	embeddedTerminalRemovalUserClose embeddedTerminalRemovalReason = iota
	embeddedTerminalRemovalAutoClose
	embeddedTerminalRemovalDetach
	embeddedTerminalRemovalTerminate
	embeddedTerminalRemovalPrefillFailure
)

type embeddedTerminalSlot struct {
	Number             int
	Scope              embeddedTerminalScope
	Provider           string
	Identity           string
	RepoPath           string
	WorktreePath       string
	WorkingDir         string
	FlowID             string
	FlowPhaseID        string
	FlowRepair         bool
	FlowWorktreeAgent  bool
	FlowLaunchTracked  bool
	FlowDetachBlocked  bool
	DetachRequestToken string
	LaunchID           string
	Terminal           EmbeddedTerminal
	ID                 embeddedTerminalID
	PrefillPending     bool
}

type embeddedTerminalDetachValidatedMsg struct {
	SlotID embeddedTerminalID
	Token  string
	Flow   flowstore.FlowRecord
	Err    error
}

type embeddedSessionPickerSelectedMsg struct {
	Record sessions.SessionRecord
	OK     bool
}

type embeddedSessionPickerLoadedMsg struct {
	RepoPath string
	Records  []sessions.SessionRecord
	Err      error
}

type savedSessionResumeRefreshedMsg struct {
	Key          string
	Token        string
	ReservedFlow string
	Origin       savedSessionResumeOrigin
	Record       sessions.SessionRecord
	Found        bool
	Err          error
}

type savedSessionResumeOrigin uint8

const (
	savedSessionResumeEmbedded savedSessionResumeOrigin = iota
	savedSessionResumeInlineExternal
)

type terminateEmbeddedTerminalMsg struct {
	ID embeddedTerminalID
}

type quitEmbeddedTerminalsMsg struct{}

type embeddedTerminalTickMsg struct {
	Generation uint64
}

type embeddedPromptPrefillResultMsg struct {
	ID                embeddedTerminalID
	LaunchContext     actions.AgentLaunchContext
	Err               error
	TerminationFailed bool
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
	// Headless claude streams stream-json, which is only readable once
	// translated. Run it directly (not via the tmux transport) so its output
	// can be piped through the renderer before it reaches the emulator.
	if actions.UsesStreamJSONOutput(ctx) {
		return startStreamJSONClaudeTerminal(ctx, width, height)
	}
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

func startStreamJSONClaudeTerminal(ctx actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
	cmd, err := actions.AgentCommand(ctx)
	if err != nil {
		return nil, err
	}
	term, err := embeddedterm.NewManager().StartCommandWithOptions(
		context.Background(), cmd, width, height,
		embeddedterm.StartOptions{Transform: claudestream.NewRenderer()},
	)
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

func (m Model) activeTerminal() (embeddedTerminalSlot, int, bool) {
	for i, slot := range m.embeddedTerminals {
		if !slot.PrefillPending && slot.Number == m.activeTerminalNum {
			return slot, i, true
		}
	}
	for i, slot := range m.embeddedTerminals {
		if !slot.PrefillPending {
			return slot, i, true
		}
	}
	return embeddedTerminalSlot{}, -1, false
}

func (m Model) embeddedTerminalTabs() []ui.EmbeddedTerminalTab {
	return m.embeddedTerminalTabsFiltered()
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
	if !m.flowSurfaceVisible() {
		return m
	}
	flowID := m.selectedFlowID()
	if flowID == "" {
		return m
	}
	activeNum := m.activeTerminalNum
	newestMatchingNum := 0
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || slot.PrefillPending || slot.FlowID != flowID || !embeddedTerminalRunning(slot.Terminal) {
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
		m.activeTerminalNum = newestMatchingNum
	}
	return m
}

func (m Model) embeddedTerminalTabsFiltered() []ui.EmbeddedTerminalTab {
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
			Active:   slot.Number == m.activeTerminalNum,
		})
	}
	return tabs
}

func (m Model) embeddedTerminalLines() []string {
	slot, _, ok := m.activeTerminal()
	if !ok || slot.Terminal == nil {
		return nil
	}
	height := m.embeddedTerminalContentHeight()
	return slot.Terminal.VisibleLines(m.embeddedTerminalWidth(), height)
}

func (m Model) embeddedTerminalOuterWidth() int {
	return m.width
}

func (m Model) embeddedTerminalWidth() int {
	return ui.EmbeddedTerminalPTYWidth(m.embeddedTerminalOuterWidth())
}

// embeddedTerminalOuterHeight is the height the top-level dock partition works
// from. It is mode-independent so the terminal keeps one size across views.
func (m Model) embeddedTerminalOuterHeight() int {
	height := m.height - ui.BranchContentOverhead
	if height > 0 {
		return height
	}
	return 0
}

func (m Model) embeddedTerminalContentHeight() int {
	_, terminalHeight := ui.EmbeddedTerminalDockHeights(m.embeddedTerminalOuterHeight(), ui.EmbeddedTerminalDockExpanded)
	return ui.EmbeddedTerminalPTYHeight(terminalHeight)
}

func (m Model) nextEmbeddedTerminalNumber() (int, bool) {
	if len(m.embeddedTerminals) >= 9 {
		return 0, false
	}
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
	next, opened, err, _ := m.openEmbeddedTerminalWithLabel(ctx, embeddedTerminalScopeSession, string(record.Provider), embeddedTerminalIdentity(record), ctx.FlowID, ctx.FlowPhaseID, m.embeddedTerminalWidth(), m.embeddedTerminalContentHeight())
	return next, opened, err
}

func (m Model) openFlowEmbeddedTerminal(ctx actions.AgentLaunchContext) (Model, bool, error, tea.Cmd) {
	activeNum := m.activeTerminalNum
	next, opened, err, prefillCmd := m.openEmbeddedTerminalWithLabel(ctx, embeddedTerminalScopeFlow, ctx.Command, flowEmbeddedTerminalIdentity(ctx), ctx.FlowID, ctx.FlowPhaseID, m.embeddedTerminalWidth(), m.embeddedTerminalContentHeight())
	if opened && prefillCmd == nil && ctx.FlowAutoLaunch && activeNum != 0 {
		next.activeTerminalNum = activeNum
	}
	return next, opened, err, prefillCmd
}

func (m Model) openEmbeddedTerminalWithLabel(ctx actions.AgentLaunchContext, scope embeddedTerminalScope, provider, identity, flowID, flowPhaseID string, width, height int) (Model, bool, error, tea.Cmd) {
	number, ok := m.nextEmbeddedTerminalNumber()
	if !ok {
		m = m.setStatus(statusOther, "Maximum embedded terminals reached")
		return m, false, nil, nil
	}
	ctx.Embedded = true
	term, err := m.startEmbeddedTerminal(ctx, width, height)
	if err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, false, err, nil
	}
	wasEmpty := len(m.embeddedTerminals) == 0
	m.nextEmbeddedTerminalID++
	id := embeddedTerminalID(m.nextEmbeddedTerminalID)
	m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
		Number:            number,
		Scope:             scope,
		Provider:          provider,
		Identity:          identity,
		RepoPath:          cleanEmbeddedTerminalRepoPath(ctx.RepoPath),
		WorktreePath:      cleanEmbeddedTerminalPath(ctx.WorktreePath),
		WorkingDir:        cleanEmbeddedTerminalPath(ctx.WorkingDir),
		FlowID:            flowID,
		FlowPhaseID:       flowPhaseID,
		FlowRepair:        ctx.FlowRepair,
		FlowWorktreeAgent: ctx.FlowWorktreeAgent,
		FlowLaunchTracked: ctx.FlowLaunchTracked,
		FlowDetachBlocked: ctx.FlowWorktreeAgent || ctx.FlowRepair || (ctx.ResumeSessionID != "" && ctx.FlowID != "" && !ctx.FlowLaunchTracked) || ctx.FlowPhaseTerminal,
		LaunchID:          strings.TrimSpace(ctx.LaunchID),
		Terminal:          term,
		ID:                id,
		PrefillPending:    actions.ShouldPrefillEmbeddedPrompt(ctx),
	})
	if wasEmpty {
		m = m.reflowForTerminalDock()
	}
	if actions.ShouldPrefillEmbeddedPrompt(ctx) {
		return m, true, nil, func() tea.Msg {
			err := prefillEmbeddedPromptIfNeeded(term, ctx)
			terminationFailed := false
			if err != nil {
				if terminateErr := term.Terminate(); terminateErr != nil {
					terminationFailed = true
					err = errors.Join(err, fmt.Errorf("terminate embedded terminal after prefill failure: %w", terminateErr))
				}
			}
			return embeddedPromptPrefillResultMsg{ID: id, LaunchContext: ctx, Err: err, TerminationFailed: terminationFailed}
		}
	}
	m = m.activateEmbeddedTerminal(id)
	return m, true, nil, nil
}

func (m Model) activateEmbeddedTerminal(id embeddedTerminalID) Model {
	for i := range m.embeddedTerminals {
		slot := &m.embeddedTerminals[i]
		if slot.ID != id {
			continue
		}
		slot.PrefillPending = false
		m.activeTerminalNum = slot.Number
		break
	}
	return m
}

func (m Model) hasEmbeddedTerminalID(id embeddedTerminalID) bool {
	for _, slot := range m.embeddedTerminals {
		if slot.ID == id {
			return true
		}
	}
	return false
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
	if len(m.embeddedTerminals) == 0 || !m.terminalDockVisible {
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
		if err := slot.Terminal.Resize(width, m.embeddedTerminalContentHeight()); err != nil {
			m = m.setStatus(statusOther, err.Error())
		}
	}
	return m
}

func (m Model) focusEmbeddedTerminalInput() Model {
	m.activePane = m.contentPane
	m.terminalFocus = terminalFocusTerminal
	m.terminalPrefixActive = false
	if m.terminalDockVisible {
		return m
	}
	m.terminalDockVisible = true
	m = m.resizeEmbeddedTerminals()
	return m.reflowForTerminalDock()
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
	if ctx.FlowRepair {
		return "repair"
	}
	if ctx.FlowWorktreeAgent {
		return "agent"
	}
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

func (m Model) switchEmbeddedTerminal(number int) Model {
	for _, slot := range m.embeddedTerminals {
		if !slot.PrefillPending && slot.Number == number {
			m.activeTerminalNum = number
			return m
		}
	}
	return m.setStatus(statusOther, fmt.Sprintf("No embedded terminal %d", number))
}

func (m Model) cycleEmbeddedTerminal(direction int) Model {
	if direction == 0 {
		return m
	}
	numbers := make([]int, 0, len(m.embeddedTerminals))
	activeIndex := -1
	for _, slot := range m.embeddedTerminals {
		if slot.PrefillPending {
			continue
		}
		if slot.Number == m.activeTerminalNum {
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
	return m.switchEmbeddedTerminal(numbers[nextIndex])
}

func (m Model) handleEmbeddedTerminalKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.terminalDockVisible && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal && m.hasActiveEmbeddedTerminal() {
		return m.handleFocusedEmbeddedTerminalKey(msg)
	}
	return m, nil, false
}

func (m Model) handleEmbeddedTerminalKeyForScope(msg tea.KeyMsg, _ embeddedTerminalScope) (Model, tea.Cmd, bool) {
	return m.handleFocusedEmbeddedTerminalKey(msg)
}

func (m Model) handleFocusedEmbeddedTerminalKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	if key == "tab" {
		return m.cyclePaneFocusForward(), nil, true
	}
	if m.terminalPrefixActive {
		switch key {
		case "left":
			return m.cycleEmbeddedTerminal(-1), nil, true
		case "right":
			return m.cycleEmbeddedTerminal(1), nil, true
		case terminalCommandKey:
			return m.writeToActiveTerminal([]byte{terminalCommandLiteralByte}), nil, true
		case "l":
			if records := m.sessions.Items(); len(records) > 0 {
				return m.openEmbeddedSessionPicker(records), nil, true
			}
			next, cmd := m.loadEmbeddedSessionPicker()
			return next, cmd, true
		case "i":
			m.terminalPrefixActive = false
			return m, nil, true
		case "t":
			return m.toggleEmbeddedTerminalDock(), nil, true
		case "x":
			return m.handleEmbeddedTerminalClosePrefix(), nil, true
		case "d":
			next, cmd := m.handleEmbeddedTerminalDetachPrefix()
			return next, cmd, true
		case "q", "esc":
			next, cmd := m.handleEmbeddedTerminalQuitPrefix()
			return next, cmd, true
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			return m.switchEmbeddedTerminal(int(key[0] - '0')), nil, true
		default:
			return m.setStatus(statusOther, "Unknown terminal prefix command"), nil, true
		}
	}
	if key == terminalCommandKey {
		m.terminalPrefixActive = true
		return m, nil, true
	}
	return m.writeToActiveTerminal(keyBytes(msg)), nil, true
}

func (m Model) hasEmbeddedTerminalForScope(scope embeddedTerminalScope) bool {
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == scope {
			return true
		}
	}
	return false
}

func (m Model) hasActiveEmbeddedTerminalForScope(scope embeddedTerminalScope) bool {
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == scope && !slot.PrefillPending {
			return true
		}
	}
	return false
}

func (m Model) hasActiveEmbeddedTerminal() bool {
	_, _, ok := m.activeTerminal()
	return ok
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
	slot, _, ok := m.activeTerminal()
	if !ok {
		return m
	}
	if !embeddedTerminalRunning(slot.Terminal) {
		return m.dismissEmbeddedTerminal(slot.ID)
	}
	m.terminalConfirmID = slot.ID
	m.modal = modal.OpenConfirm(embeddedTerminalTerminatePrompt, func() tea.Cmd {
		return func() tea.Msg { return terminateEmbeddedTerminalMsg{ID: slot.ID} }
	})
	return m
}

func (m Model) handleEmbeddedTerminalDetachPrefix() (Model, tea.Cmd) {
	slot, _, ok := m.activeTerminal()
	if !ok || slot.Terminal == nil {
		return m, nil
	}
	if slot.FlowDetachBlocked {
		return m.setStatus(statusOther, "Detach unavailable: this Flow terminal must remain attached to preserve occupancy"), nil
	}
	if slot.FlowID != "" && slot.FlowLaunchTracked && slot.FlowPhaseID != "" {
		token := newLaunchID()
		for i := range m.embeddedTerminals {
			if m.embeddedTerminals[i].ID == slot.ID {
				m.embeddedTerminals[i].DetachRequestToken = token
				break
			}
		}
		listFlows := m.listFlows
		return m, func() tea.Msg {
			msg := embeddedTerminalDetachValidatedMsg{SlotID: slot.ID, Token: token}
			records, err := listFlows(flowstore.FlowFilter{})
			if err != nil {
				msg.Err = fmt.Errorf("refresh Flow before detach: %w", err)
				return msg
			}
			for _, record := range records {
				if strings.TrimSpace(record.FlowID) == strings.TrimSpace(slot.FlowID) {
					msg.Flow = record
					return msg
				}
			}
			msg.Err = fmt.Errorf("Flow %s no longer exists", slot.FlowID)
			return msg
		}
	}
	return m.detachEmbeddedTerminal(slot)
}

func (m Model) handleEmbeddedTerminalDetachValidated(msg embeddedTerminalDetachValidatedMsg) (Model, tea.Cmd) {
	var slot embeddedTerminalSlot
	found := false
	for i := range m.embeddedTerminals {
		candidate := &m.embeddedTerminals[i]
		if candidate.ID != msg.SlotID || candidate.DetachRequestToken != msg.Token {
			continue
		}
		candidate.DetachRequestToken = ""
		slot = *candidate
		found = true
		break
	}
	if !found {
		return m, nil
	}
	if msg.Err != nil {
		return m.setStatus(statusOther, msg.Err.Error()), nil
	}
	if strings.TrimSpace(msg.Flow.FlowID) != strings.TrimSpace(slot.FlowID) {
		return m.setStatus(statusOther, "Detach unavailable: Flow validation is stale"), nil
	}
	phase, ok := flowPhaseByID(msg.Flow, slot.FlowPhaseID)
	if !ok || phase.Status != flowstore.PhaseRunning {
		return m.setStatus(statusOther, "Detach unavailable: the matching Flow phase is no longer running"), nil
	}
	for _, current := range m.embeddedTerminals {
		if current.ID == slot.ID && current.FlowID == slot.FlowID && current.FlowPhaseID == slot.FlowPhaseID {
			return m.detachEmbeddedTerminal(current)
		}
	}
	return m, nil
}

func (m Model) detachEmbeddedTerminal(slot embeddedTerminalSlot) (Model, tea.Cmd) {
	detachable, ok := slot.Terminal.(detachableEmbeddedTerminal)
	if !ok {
		return m.setStatus(statusOther, "Detach unavailable: this terminal is not tmux-backed"), nil
	}
	target := strings.TrimSpace(detachable.DetachTarget())
	if err := detachable.Detach(); err != nil {
		if errors.Is(err, errEmbeddedTerminalDetachUnavailable) {
			return m.setStatus(statusOther, "Detach unavailable: this terminal is not tmux-backed"), nil
		}
		return m.setStatus(statusOther, err.Error()), nil
	}
	m = m.dismissEmbeddedTerminalForReason(slot.ID, embeddedTerminalRemovalDetach)
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
		m = m.dismissEmbeddedTerminalForReason(id, embeddedTerminalRemovalAutoClose)
	}
	return m
}

func (m Model) hasExitedFlowEmbeddedTerminalAutoClose() bool {
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || slot.Terminal == nil {
			continue
		}
		if flowEmbeddedTerminalAutoCloses(slot.Terminal.State()) {
			return true
		}
	}
	return false
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
		return m.dismissEmbeddedTerminalForReason(msg.ID, embeddedTerminalRemovalTerminate), nil
	}
	return m, nil
}

func (m Model) dismissEmbeddedTerminal(id embeddedTerminalID) Model {
	return m.dismissEmbeddedTerminalForReason(id, embeddedTerminalRemovalUserClose)
}

func (m Model) dismissEmbeddedTerminalForReason(id embeddedTerminalID, reason embeddedTerminalRemovalReason) Model {
	removed := false
	activeID := m.activeTerminalID()
	terminalFocused := m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal
	next := m.embeddedTerminals[:0]
	for _, slot := range m.embeddedTerminals {
		if slot.ID != id {
			next = append(next, slot)
		} else {
			removed = true
			m = m.recordRepairTerminalRemoval(slot, reason)
		}
	}
	if !removed {
		return m
	}
	m.embeddedTerminals = next
	m.renumberEmbeddedTerminals()
	m = m.clearEmbeddedTerminalConfirmFor(id)
	if len(m.embeddedTerminals) == 0 {
		m.activeTerminalNum = 0
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
		m.embeddedTerminalTickGen++
		return m.reflowForTerminalDock()
	}
	m.activeTerminalNum = m.activeEmbeddedTerminalNumberAfterRenumber(activeID, id)
	if m.activeTerminalNum == 0 {
		m.terminalFocus = terminalFocusList
		m.terminalPrefixActive = false
	} else if terminalFocused && activeID == id {
		m.terminalPrefixActive = true
	}
	return m
}

func (m Model) recordRepairTerminalRemoval(slot embeddedTerminalSlot, reason embeddedTerminalRemovalReason) Model {
	if !slot.FlowRepair {
		return m
	}
	cleanExit := (reason == embeddedTerminalRemovalUserClose || reason == embeddedTerminalRemovalAutoClose) &&
		slot.Terminal != nil && slot.Terminal.State() == "exited"
	if cleanExit {
		return m.armRepairAutoDrain(slot.FlowID)
	}
	return m.suppressRepairAutoDrain(slot.FlowID)
}

func (m Model) clearEmbeddedTerminalConfirmFor(id embeddedTerminalID) Model {
	if m.terminalConfirmID != id {
		return m
	}
	if view := m.modal.View(); view.Kind == modal.Confirm && view.Prompt == embeddedTerminalTerminatePrompt {
		m.modal = modal.Modal{}
	}
	return m.clearEmbeddedTerminalConfirm()
}

func (m Model) clearEmbeddedTerminalConfirm() Model {
	m.terminalConfirmID = 0
	return m
}

func (m Model) activeTerminalID() embeddedTerminalID {
	slot, _, ok := m.activeTerminal()
	if !ok {
		return 0
	}
	return slot.ID
}

func (m *Model) renumberEmbeddedTerminals() {
	for i := range m.embeddedTerminals {
		m.embeddedTerminals[i].Number = i + 1
	}
}

func (m Model) activeEmbeddedTerminalNumberAfterRenumber(previousActiveID, removedID embeddedTerminalID) int {
	if previousActiveID != 0 && previousActiveID != removedID {
		if number := m.embeddedTerminalNumberForID(previousActiveID); number != 0 {
			return number
		}
	}
	return m.firstEmbeddedTerminalNumber()
}

func (m Model) embeddedTerminalNumberForID(id embeddedTerminalID) int {
	for _, slot := range m.embeddedTerminals {
		if slot.ID == id {
			return slot.Number
		}
	}
	return 0
}

func (m Model) firstEmbeddedTerminalNumber() int {
	for _, slot := range m.embeddedTerminals {
		if !slot.PrefillPending {
			return slot.Number
		}
	}
	return 0
}

// loadEmbeddedSessionPicker fetches the selected repo's saved sessions before
// deciding the picker is empty: the sessions pane cache is only warm after
// visiting the sessions view, but the picker command is available in every
// view.
func (m Model) loadEmbeddedSessionPicker() (Model, tea.Cmd) {
	repoPath, ok := m.currentRepoPath()
	if !ok || m.listSessions == nil {
		return m.setStatus(statusOther, "No saved sessions available"), nil
	}
	listSessions := m.listSessions
	return m, func() tea.Msg {
		records, err := listSessions(sessions.SessionFilter{RepoPath: repoPath})
		return embeddedSessionPickerLoadedMsg{RepoPath: repoPath, Records: records, Err: err}
	}
}

func (m Model) handleEmbeddedSessionPickerLoaded(msg embeddedSessionPickerLoadedMsg) (Model, tea.Cmd) {
	repoPath, ok := m.currentRepoPath()
	if !ok || repoPath != msg.RepoPath || m.modal.IsOpen() {
		return m, nil
	}
	if msg.Err != nil {
		return m.setStatus(statusOther, "failed to load sessions: "+msg.Err.Error()), nil
	}
	if len(msg.Records) == 0 {
		return m.setStatus(statusOther, "No saved sessions available"), nil
	}
	return m.openEmbeddedSessionPicker(msg.Records), nil
}

func (m Model) openEmbeddedSessionPicker(records []sessions.SessionRecord) Model {
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
			if err != nil || index < 0 || index >= len(records) {
				return embeddedSessionPickerSelectedMsg{}
			}
			return embeddedSessionPickerSelectedMsg{Record: records[index], OK: true}
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
	if !msg.OK {
		return m.setStatus(statusOther, "Selected session is unavailable"), nil
	}
	return m.resumeSavedSession(msg.Record, savedSessionResumeEmbedded)
}

// resumeSavedSession refreshes the selected record before routing it. Flow
// association can change after a picker or list is rendered, and launch policy
// must be based on the authoritative record rather than that cached snapshot.
func (m Model) resumeSavedSession(record sessions.SessionRecord, origin savedSessionResumeOrigin) (Model, tea.Cmd) {
	if m.listSessions == nil {
		return m.setStatus(statusOther, "Session storage is unavailable"), nil
	}
	ctx, ok, next := m.sessionResumeLaunchContext(record)
	if !ok {
		return next, nil
	}
	token := ctx.LaunchID
	reservedFlow := strings.TrimSpace(ctx.FlowID)
	if reservedFlow != "" {
		if m.hasFlowEmbeddedTerminalForFlow(reservedFlow) {
			return m.setStatus(statusOther, "An embedded terminal already occupies this Flow"), nil
		}
		var acquired bool
		m, acquired = m.acquireFlowLaunchLease(reservedFlow, token, flowLaunchSourceSessionResume)
		if !acquired {
			return m.setStatus(statusOther, "Another launch or session already occupies this Flow"), nil
		}
	}
	key := savedSessionResumeKey(record)
	pending := make(map[string]string, len(m.pendingSavedSessionResumes)+1)
	for pendingKey, pendingToken := range m.pendingSavedSessionResumes {
		pending[pendingKey] = pendingToken
	}
	pending[key] = token
	m.pendingSavedSessionResumes = pending
	listSessions := m.listSessions
	authoritative := m.listSessionsAuthoritative
	return m, func() tea.Msg {
		msg := savedSessionResumeRefreshedMsg{Key: key, Token: token, ReservedFlow: reservedFlow, Origin: origin}
		records, err := listSessions(sessions.SessionFilter{Provider: record.Provider})
		if err != nil {
			msg.Err = fmt.Errorf("refresh selected session before resuming: %w", err)
			return msg
		}
		for _, current := range records {
			if current.Provider == record.Provider && strings.TrimSpace(current.SessionID) == strings.TrimSpace(record.SessionID) {
				msg.Record = current
				msg.Found = true
				break
			}
		}
		if !msg.Found && !authoritative {
			msg.Record = record
			msg.Found = true
		}
		return msg
	}
}

func (m Model) handleSavedSessionResumeRefreshed(msg savedSessionResumeRefreshedMsg) (Model, tea.Cmd) {
	if m.pendingSavedSessionResumes[msg.Key] != msg.Token {
		return m, nil
	}
	finish := func() Model {
		pending := make(map[string]string, len(m.pendingSavedSessionResumes)-1)
		for key, token := range m.pendingSavedSessionResumes {
			if key != msg.Key {
				pending[key] = token
			}
		}
		if len(pending) == 0 {
			pending = nil
		}
		m.pendingSavedSessionResumes = pending
		return m
	}
	fail := func(text string) (Model, tea.Cmd) {
		m = finish()
		m = m.releaseFlowLaunchLease(msg.ReservedFlow, msg.Token)
		return m.setStatus(statusOther, text), nil
	}
	if msg.Err != nil {
		return fail(msg.Err.Error())
	}
	if !msg.Found {
		return fail("Selected session is no longer available")
	}
	ctx, ok, next := m.sessionResumeLaunchContext(msg.Record)
	if !ok {
		m = next
		m = finish()
		m = m.releaseFlowLaunchLease(msg.ReservedFlow, msg.Token)
		return m, nil
	}
	ctx.LaunchID = msg.Token
	currentFlow := strings.TrimSpace(ctx.FlowID)
	if currentFlow != msg.ReservedFlow {
		m = m.releaseFlowLaunchLease(msg.ReservedFlow, msg.Token)
	}
	if ctx.Command == agent.CommandCodexApp {
		m = finish()
		return m.launchAgentWithContext(ctx)
	}
	if currentFlow != "" {
		if m.hasFlowEmbeddedTerminalForFlow(currentFlow) {
			return fail("An embedded terminal already occupies this Flow")
		}
		if currentFlow != msg.ReservedFlow {
			var acquired bool
			m, acquired = m.acquireFlowLaunchLease(currentFlow, msg.Token, flowLaunchSourceSessionResume)
			if !acquired {
				return fail("Another launch or session already occupies this Flow")
			}
		} else if !m.matchingFlowLaunchLease(currentFlow, msg.Token, flowLaunchSourceSessionResume) {
			return fail("Saved-session resume no longer owns this Flow")
		}
		m = finish()
		return m, m.flowSessionResumePreflightCmd(ctx, msg.Record)
	}
	m = finish()
	if msg.Origin == savedSessionResumeInlineExternal {
		return m.launchAgentWithContext(ctx)
	}
	return m.resumeSessionInEmbeddedTerminalNow(ctx, msg.Record, true)
}

func savedSessionResumeKey(record sessions.SessionRecord) string {
	return string(record.Provider) + "\x00" + strings.TrimSpace(record.SessionID)
}

func (m Model) resumeSessionInEmbeddedTerminal(ctx actions.AgentLaunchContext, record sessions.SessionRecord) (Model, tea.Cmd) {
	if strings.TrimSpace(ctx.FlowID) != "" {
		if m.hasFlowEmbeddedTerminalForFlow(ctx.FlowID) {
			return m.setStatus(statusOther, "An embedded terminal already occupies this Flow"), nil
		}
		var acquired bool
		m, acquired = m.acquireFlowLaunchLease(ctx.FlowID, ctx.LaunchID, flowLaunchSourceSessionResume)
		if !acquired {
			return m.setStatus(statusOther, "Another launch or session already occupies this Flow"), nil
		}
		return m, m.flowSessionResumePreflightCmd(ctx, record)
	}
	return m.resumeSessionInEmbeddedTerminalNow(ctx, record, true)
}

func (m Model) flowSessionResumePreflightCmd(ctx actions.AgentLaunchContext, record sessions.SessionRecord) tea.Cmd {
	listFlows := m.listFlows
	listSessions := m.listSessions
	return func() tea.Msg {
		msg := flowSessionResumePreflightMsg{Context: ctx, Record: record}
		currentRecords, err := listSessions(sessions.SessionFilter{Provider: record.Provider})
		if err != nil {
			msg.Err = fmt.Errorf("refresh selected session before resuming: %w", err)
			return msg
		}
		for _, current := range currentRecords {
			if current.Provider == record.Provider && strings.TrimSpace(current.SessionID) == strings.TrimSpace(record.SessionID) {
				msg.CurrentRecord = current
				msg.CurrentRecordFound = true
				break
			}
		}
		if !msg.CurrentRecordFound {
			msg.Err = fmt.Errorf("selected session %s no longer exists", record.SessionID)
			return msg
		}
		msg.Sessions, err = listSessions(sessions.SessionFilter{FlowID: ctx.FlowID})
		if err != nil {
			msg.Err = fmt.Errorf("list sessions for Flow %s: %w", ctx.FlowID, err)
			return msg
		}
		flows, err := listFlows(flowstore.FlowFilter{})
		if err != nil {
			msg.Err = fmt.Errorf("refresh Flow before resuming session: %w", err)
			return msg
		}
		for _, flow := range flows {
			if strings.TrimSpace(flow.FlowID) == strings.TrimSpace(ctx.FlowID) {
				msg.Flow = flow
				break
			}
		}
		if msg.Flow.FlowID == "" {
			msg.Err = fmt.Errorf("Flow %s no longer exists", ctx.FlowID)
			return msg
		}
		return msg
	}
}

type flowSessionResumePreflightMsg struct {
	Context            actions.AgentLaunchContext
	Record             sessions.SessionRecord
	CurrentRecord      sessions.SessionRecord
	CurrentRecordFound bool
	Flow               flowstore.FlowRecord
	Sessions           []sessions.SessionRecord
	Err                error
}

func (m Model) handleFlowSessionResumePreflight(msg flowSessionResumePreflightMsg) (Model, tea.Cmd) {
	ctx := msg.Context
	if !m.matchingFlowLaunchLease(ctx.FlowID, ctx.LaunchID, flowLaunchSourceSessionResume) {
		return m, nil
	}
	fail := func(text string) (Model, tea.Cmd) {
		m = m.releaseFlowLaunchLease(ctx.FlowID, ctx.LaunchID)
		return m.setStatus(statusOther, text), nil
	}
	if msg.Err != nil {
		return fail(msg.Err.Error())
	}
	if !msg.CurrentRecordFound {
		return fail("Selected session is no longer available")
	}
	if strings.TrimSpace(msg.CurrentRecord.FlowID) != strings.TrimSpace(ctx.FlowID) ||
		strings.TrimSpace(msg.CurrentRecord.LaunchID) != strings.TrimSpace(msg.Record.LaunchID) {
		return fail("Selected session changed while the resume was pending; refresh and try again")
	}
	refreshedCtx, ok, next := m.sessionResumeLaunchContext(msg.CurrentRecord)
	if !ok {
		next = next.releaseFlowLaunchLease(ctx.FlowID, ctx.LaunchID)
		return next, nil
	}
	if refreshedCtx.Command != ctx.Command {
		return fail("Configured agent changed while the session resume was pending; try again")
	}
	refreshedCtx.LaunchID = ctx.LaunchID
	ctx = refreshedCtx
	for _, record := range msg.Sessions {
		if sessions.IsActive(record) {
			return fail("An active persisted session already occupies this Flow")
		}
	}
	for _, phase := range msg.Flow.Phases {
		if phase.Status == flowstore.PhaseRunning {
			return fail("A running phase already occupies this Flow")
		}
	}
	if m.hasFlowEmbeddedTerminalForFlow(ctx.FlowID) {
		return fail("An embedded terminal already occupies this Flow")
	}
	next, cmd := m.resumeSessionInEmbeddedTerminalNow(ctx, msg.CurrentRecord, false)
	next = next.releaseFlowLaunchLease(ctx.FlowID, ctx.LaunchID)
	return next, cmd
}

func (m Model) resumeSessionInEmbeddedTerminalNow(ctx actions.AgentLaunchContext, record sessions.SessionRecord, allowExternalFallback bool) (Model, tea.Cmd) {
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err := m.openEmbeddedTerminal(ctx, record)
	if err != nil && embeddedterm.IsUnsupported(err) {
		if allowExternalFallback {
			return next.launchAgentWithContext(ctx)
		}
		return next.setStatus(statusOther, "Flow-associated session resume requires embedded terminal support"), nil
	}
	if opened {
		next = next.focusEmbeddedTerminalInput()
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
	slot, _, ok := m.activeTerminal()
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
