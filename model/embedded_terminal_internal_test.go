package model

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

type internalFakeEmbeddedTerminal struct {
	state string
}

func (t internalFakeEmbeddedTerminal) VisibleLines(int, int) []string { return nil }
func (t internalFakeEmbeddedTerminal) Write(p []byte) (int, error)    { return len(p), nil }
func (t internalFakeEmbeddedTerminal) Resize(int, int) error          { return nil }
func (t internalFakeEmbeddedTerminal) Terminate() error               { return nil }
func (t internalFakeEmbeddedTerminal) Wait(context.Context) error     { return nil }
func (t internalFakeEmbeddedTerminal) State() string {
	if t.state == "" {
		return "running"
	}
	return t.state
}

type internalFakeDetachableEmbeddedTerminal struct {
	internalFakeEmbeddedTerminal
	target   string
	detached bool
	writes   [][]byte
}

func (t *internalFakeDetachableEmbeddedTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (t *internalFakeDetachableEmbeddedTerminal) Detach() error {
	t.detached = true
	t.state = "exited"
	return nil
}

func (t *internalFakeDetachableEmbeddedTerminal) DetachTarget() string { return t.target }

type prefillReadyFakeEmbeddedTerminal struct {
	lines       [][]string
	visibleHits int
	writes      []string
}

func (t *prefillReadyFakeEmbeddedTerminal) VisibleLines(int, int) []string {
	t.visibleHits++
	if len(t.lines) == 0 {
		return nil
	}
	index := t.visibleHits - 1
	if index >= len(t.lines) {
		index = len(t.lines) - 1
	}
	return t.lines[index]
}

func (t *prefillReadyFakeEmbeddedTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, string(p))
	return len(p), nil
}

func (t *prefillReadyFakeEmbeddedTerminal) Resize(int, int) error      { return nil }
func (t *prefillReadyFakeEmbeddedTerminal) Terminate() error           { return nil }
func (t *prefillReadyFakeEmbeddedTerminal) Wait(context.Context) error { return nil }
func (t *prefillReadyFakeEmbeddedTerminal) State() string              { return "running" }

type delayedPrefillFakeEmbeddedTerminal struct {
	mu               sync.Mutex
	probeStarted     chan struct{}
	releaseReady     chan struct{}
	commandCompleted chan struct{}
	probeOnce        sync.Once
	releaseOnce      sync.Once
	completeOnce     sync.Once
	visibleHits      int
	writes           []string
}

func newDelayedPrefillFakeEmbeddedTerminal() *delayedPrefillFakeEmbeddedTerminal {
	return &delayedPrefillFakeEmbeddedTerminal{
		probeStarted:     make(chan struct{}),
		releaseReady:     make(chan struct{}),
		commandCompleted: make(chan struct{}),
	}
}

func (t *delayedPrefillFakeEmbeddedTerminal) VisibleLines(int, int) []string {
	t.mu.Lock()
	t.visibleHits++
	t.mu.Unlock()
	t.probeOnce.Do(func() { close(t.probeStarted) })
	<-t.releaseReady
	return []string{"Codex ready"}
}

func (t *delayedPrefillFakeEmbeddedTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.writes = append(t.writes, string(p))
	t.mu.Unlock()
	t.completeOnce.Do(func() { close(t.commandCompleted) })
	return len(p), nil
}

func (t *delayedPrefillFakeEmbeddedTerminal) Resize(int, int) error      { return nil }
func (t *delayedPrefillFakeEmbeddedTerminal) Terminate() error           { return nil }
func (t *delayedPrefillFakeEmbeddedTerminal) Wait(context.Context) error { return nil }
func (t *delayedPrefillFakeEmbeddedTerminal) State() string              { return "running" }

func (t *delayedPrefillFakeEmbeddedTerminal) release() {
	t.releaseOnce.Do(func() { close(t.releaseReady) })
}

func (t *delayedPrefillFakeEmbeddedTerminal) snapshot() (int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visibleHits, append([]string(nil), t.writes...)
}

func TestPrefillEmbeddedPromptWaitsForVisibleTerminalOutput(t *testing.T) {
	originalInterval := embeddedPromptPrefillPollInterval
	embeddedPromptPrefillPollInterval = 0
	t.Cleanup(func() {
		embeddedPromptPrefillPollInterval = originalInterval
	})

	term := &prefillReadyFakeEmbeddedTerminal{
		lines: [][]string{
			{"", "   "},
			{"Codex ready", ""},
		},
	}

	err := prefillEmbeddedPromptIfNeeded(term, actions.AgentLaunchContext{
		Command:           "codex",
		Embedded:          true,
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		InitialPrompt:     "Build it",
	})
	if err != nil {
		t.Fatalf("prefill returned error: %v", err)
	}

	if term.visibleHits != 2 {
		t.Fatalf("visible calls = %d, want wait until second ready frame", term.visibleHits)
	}
	wantWrite := embeddedPromptPasteStart + "Build it" + embeddedPromptPasteEnd
	if len(term.writes) != 1 || term.writes[0] != wantWrite {
		t.Fatalf("writes = %#v, want %q", term.writes, wantWrite)
	}
}

func TestFlowEmbeddedInteractivePrefillRunsAfterUpdateAndActivatesByStableID(t *testing.T) {
	term := newDelayedPrefillFakeEmbeddedTerminal()
	t.Cleanup(term.release)
	startCalls := 0
	m := Model{
		mode:       ui.ModeFlows,
		activePane: 1,
		flowFocus:  flowFocusList,
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			startCalls++
			return term, nil
		},
	}
	ctx := actions.AgentLaunchContext{
		Command:           "codex",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		InitialPrompt:     "Build it",
	}

	type updateResult struct {
		model tea.Model
		cmd   tea.Cmd
	}
	updateDone := make(chan updateResult, 1)
	go func() {
		nextModel, cmd := m.Update(FlowEmbeddedLaunchRequestedMsg{LaunchContext: ctx})
		updateDone <- updateResult{model: nextModel, cmd: cmd}
	}()

	deadlockGuard := time.After(5 * time.Second)
	var result updateResult
	select {
	case result = <-updateDone:
	case <-term.probeStarted:
		term.release()
		select {
		case <-updateDone:
		case <-deadlockGuard:
			t.Fatal("Update did not return after releasing unexpected synchronous readiness probe")
		}
		t.Fatal("Update started terminal readiness work before returning its command")
	case <-deadlockGuard:
		term.release()
		t.Fatal("Update did not return before the deadlock guard expired")
	}

	next := result.model.(Model)
	cmd := result.cmd
	if cmd == nil {
		t.Fatal("interactive Flow launch returned nil command, want asynchronous prefill command")
	}
	select {
	case <-term.probeStarted:
		t.Fatal("Update started terminal readiness work before its command was invoked")
	default:
	}
	visibleHits, writes := term.snapshot()
	if visibleHits != 0 || len(writes) != 0 {
		t.Fatalf("Update performed prefill work: visible calls=%d writes=%#v", visibleHits, writes)
	}
	if startCalls != 1 {
		t.Fatalf("terminal starts = %d, want exactly 1", startCalls)
	}
	if len(next.embeddedTerminals) != 1 || next.embeddedTerminals[0].ID == 0 || !next.embeddedTerminals[0].PrefillPending {
		t.Fatalf("pending terminal slots = %#v, want one PrefillPending slot with stable ID", next.embeddedTerminals)
	}
	stableID := next.embeddedTerminals[0].ID
	if next.activeFlowTerminalNum != 0 || next.flowFocus != flowFocusList {
		t.Fatalf("pending terminal stole focus: active=%d focus=%v", next.activeFlowTerminalNum, next.flowFocus)
	}
	if number, ok := next.nextEmbeddedTerminalNumber(embeddedTerminalScopeFlow); !ok || number != 2 {
		t.Fatalf("next Flow terminal number = %d, %v; want reserved slot 1 and next number 2", number, ok)
	}

	prefillCmd := isolatedFlowPrefillCommandFromLaunchBatch(t, cmd)
	prefillResult := make(chan tea.Msg, 1)
	go func() {
		prefillResult <- prefillCmd()
	}()

	select {
	case <-term.probeStarted:
	case <-deadlockGuard:
		t.Fatal("prefill command did not begin its readiness probe")
	}
	select {
	case msg := <-prefillResult:
		t.Fatalf("prefill command returned while readiness was withheld: %T", msg)
	default:
	}

	beforeReadyModel, tabCmd := next.Update(tea.KeyMsg{Type: tea.KeyTab})
	beforeReady := beforeReadyModel.(Model)
	beforeReadyModel, keyCmd := beforeReady.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	beforeReady = beforeReadyModel.(Model)
	_, writes = term.snapshot()
	if tabCmd != nil || keyCmd != nil || len(writes) != 0 || beforeReady.activePane != 0 || beforeReady.flowFocus != flowFocusList {
		t.Fatalf("pending terminal accepted focus/input before prefill: tab cmd=%T key cmd=%T writes=%#v pane=%d focus=%v", tabCmd, keyCmd, writes, beforeReady.activePane, beforeReady.flowFocus)
	}

	term.release()
	var rawMsg tea.Msg
	select {
	case rawMsg = <-prefillResult:
	case <-deadlockGuard:
		t.Fatal("prefill command did not return after readiness was released")
	}
	select {
	case <-term.commandCompleted:
	default:
		t.Fatal("prefill command returned before completing its terminal write")
	}
	msg, ok := rawMsg.(embeddedPromptPrefillResultMsg)
	if !ok {
		t.Fatalf("prefill command returned %T, want embeddedPromptPrefillResultMsg", rawMsg)
	}
	if msg.ID != stableID || msg.Err != nil {
		t.Fatalf("prefill result = %#v, want successful result for stable terminal ID %d", msg, stableID)
	}
	if next.activeFlowTerminalNum != 0 || next.flowFocus != flowFocusList || !next.embeddedTerminals[0].PrefillPending {
		t.Fatalf("prefill command activated terminal before result Update: active=%d focus=%v pending=%v", next.activeFlowTerminalNum, next.flowFocus, next.embeddedTerminals[0].PrefillPending)
	}

	activatedModel, _ := next.Update(msg)
	activated := activatedModel.(Model)
	wantWrite := embeddedPromptPasteStart + "Build it" + embeddedPromptPasteEnd
	visibleHits, writes = term.snapshot()
	if startCalls != 1 || visibleHits != 1 || len(writes) != 1 || writes[0] != wantWrite {
		t.Fatalf("async prefill starts=%d calls=%d writes=%#v, want one start, one probe, and %q", startCalls, visibleHits, writes, wantWrite)
	}
	if len(activated.embeddedTerminals) != 1 || activated.embeddedTerminals[0].ID != stableID || activated.embeddedTerminals[0].PrefillPending || activated.activeFlowTerminalNum != 1 || activated.flowFocus != flowFocusTerminal {
		t.Fatalf("successful prefill did not activate stable terminal: slots=%#v active=%d focus=%v", activated.embeddedTerminals, activated.activeFlowTerminalNum, activated.flowFocus)
	}
}

func TestFlowEmbeddedInteractivePrefillWritesAfterReadinessTimeout(t *testing.T) {
	originalTimeout := embeddedPromptPrefillTimeout
	originalInterval := embeddedPromptPrefillPollInterval
	embeddedPromptPrefillTimeout = time.Nanosecond
	embeddedPromptPrefillPollInterval = 0
	t.Cleanup(func() {
		embeddedPromptPrefillTimeout = originalTimeout
		embeddedPromptPrefillPollInterval = originalInterval
	})

	term := &prefillReadyFakeEmbeddedTerminal{lines: [][]string{{"", "   "}}}
	startCalls := 0
	m := Model{
		mode:       ui.ModeFlows,
		activePane: 1,
		flowFocus:  flowFocusList,
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			startCalls++
			return term, nil
		},
	}
	ctx := actions.AgentLaunchContext{
		Command:           "codex",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		InitialPrompt:     "Build \x1b[31mit\x1b[0m",
	}

	nextModel, parentCmd := m.Update(FlowEmbeddedLaunchRequestedMsg{LaunchContext: ctx})
	next := nextModel.(Model)
	prefillCmd := isolatedFlowPrefillCommandFromLaunchBatch(t, parentCmd)
	rawMsg := prefillCmd()
	msg, ok := rawMsg.(embeddedPromptPrefillResultMsg)
	if !ok {
		t.Fatalf("prefill command returned %T, want embeddedPromptPrefillResultMsg", rawMsg)
	}
	if msg.Err != nil {
		t.Fatalf("readiness timeout returned launch error: %v", msg.Err)
	}
	if startCalls != 1 || len(next.embeddedTerminals) != 1 || !next.embeddedTerminals[0].PrefillPending || next.activeFlowTerminalNum != 0 || next.flowFocus != flowFocusList {
		t.Fatalf("timeout prefill activated before result Update: starts=%d slots=%#v active=%d focus=%v", startCalls, next.embeddedTerminals, next.activeFlowTerminalNum, next.flowFocus)
	}
	wantWrite := embeddedPromptPasteStart + "Build it" + embeddedPromptPasteEnd
	if term.visibleHits == 0 || len(term.writes) != 1 || term.writes[0] != wantWrite {
		t.Fatalf("timeout prefill calls=%d writes=%#v, want readiness probes and %q", term.visibleHits, term.writes, wantWrite)
	}

	activatedModel, _ := next.Update(msg)
	activated := activatedModel.(Model)
	if len(activated.embeddedTerminals) != 1 || activated.embeddedTerminals[0].PrefillPending || activated.activeFlowTerminalNum != 1 || activated.flowFocus != flowFocusTerminal {
		t.Fatalf("successful timeout prefill did not activate terminal on result Update: slots=%#v active=%d focus=%v", activated.embeddedTerminals, activated.activeFlowTerminalNum, activated.flowFocus)
	}
}

func TestStaleEmbeddedPromptPrefillResultIsIgnoredAfterSlotRemoval(t *testing.T) {
	phaseMutationRan := false
	m := Model{
		mode:       ui.ModeFlows,
		activePane: 1,
		flowFocus:  flowFocusList,
		setFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseMutationRan = true
			return flowstore.FlowRecord{}, nil
		},
	}

	nextModel, cmd := m.Update(embeddedPromptPrefillResultMsg{
		ID:            42,
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "implementation"},
		Err:           errors.New("late failure"),
	})
	next := nextModel.(Model)
	if cmd != nil || phaseMutationRan || next.activeFlowTerminalNum != 0 || next.flowFocus != flowFocusList {
		t.Fatalf("stale prefill result changed model: cmd=%T mutation=%v active=%d focus=%v", cmd, phaseMutationRan, next.activeFlowTerminalNum, next.flowFocus)
	}
}

func commandMessageOfType[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var zero T
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if typed, ok := msg.(T); ok {
		return typed
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if typed, ok := child().(T); ok {
				return typed
			}
		}
	}
	t.Fatalf("command returned %T, want message %T", msg, zero)
	return zero
}

// isolatedFlowPrefillCommandFromLaunchBatch extracts the prefill command from
// the single-level batch returned by a minimal Flow model with no refresh cmd.
func isolatedFlowPrefillCommandFromLaunchBatch(t *testing.T, parentCmd tea.Cmd) tea.Cmd {
	t.Helper()
	if parentCmd == nil {
		t.Fatal("interactive Flow launch returned nil parent command")
	}
	raw := parentCmd()
	batch, ok := raw.(tea.BatchMsg)
	if !ok {
		t.Fatalf("interactive Flow launch parent command returned %T, want tea.BatchMsg", raw)
	}
	if len(batch) != 2 || batch[0] == nil || batch[1] == nil {
		t.Fatalf("interactive Flow launch batch = %#v, want prefill and repaint commands", batch)
	}
	return batch[0]
}

func internalFlowsModel(records ...flowstore.FlowRecord) Model {
	return Model{
		mode:       ui.ModeFlows,
		activePane: 1,
		repos: newRepoPane().SetItems([]scanner.Repo{
			{Path: "/dev/alpha", DisplayName: "alpha"},
		}),
		flows: newFlowPane().SetItems(records),
	}
}

func TestDefaultEmbeddedTerminalStarterUsesTmuxWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	writeInternalFakeExecutable(t, dir, "codex", "#!/bin/sh\n/bin/sleep 30\n")
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("APPROACH_TMUX_LOG", logPath)
	writeInternalFakeExecutable(t, dir, "tmux", `#!/bin/sh
echo "$@" >> "$APPROACH_TMUX_LOG"
for arg in "$@"; do
  case "$arg" in
    has-session) exit 1 ;;
    new-session)
      rm -f "$TMPDIR"/approach-agent-*.sh
      exit 0
      ;;
    attach-session) /bin/sleep 30 ;;
    kill-session) exit 0 ;;
  esac
done
exit 0
	`)
	t.Setenv("PATH", dir)

	term, err := defaultEmbeddedTerminalStarter(actions.AgentLaunchContext{
		Command:       "codex",
		LaunchID:      "launch-1",
		WorktreePath:  dir,
		InitialPrompt: "run",
	}, 20, 3)
	if err != nil {
		t.Fatalf("defaultEmbeddedTerminalStarter returned error: %v", err)
	}
	defer term.Terminate()

	detachable, ok := term.(detachableEmbeddedTerminal)
	if !ok {
		t.Fatalf("default starter returned %T, want detachable tmux-backed terminal", term)
	}
	if target := detachable.DetachTarget(); !strings.Contains(target, "env -u TMUX tmux -f /dev/null -L 'approach-agent-") || !strings.Contains(target, "attach-session -t") || !strings.Contains(target, "agent-launch-1") {
		t.Fatalf("detach target = %q, want reattach command for per-launch agent tmux session", target)
	}
	waitInternalFileContains(t, logPath, "attach-session")
	if err := detachable.Detach(); err != nil {
		t.Fatalf("Detach returned error: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read tmux log: %v", err)
	}
	log := string(logBytes)
	for _, want := range []string{"has-session", "new-session", "attach-session"} {
		if !strings.Contains(log, want) {
			t.Fatalf("tmux log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "kill-session") {
		t.Fatalf("detach should not kill the tmux session:\n%s", log)
	}
}

func TestDefaultEmbeddedTerminalStarterFallsBackWhenTmuxUnavailable(t *testing.T) {
	dir := t.TempDir()
	writeInternalFakeExecutable(t, dir, "codex", "#!/bin/sh\n/bin/sleep 30\n")
	t.Setenv("PATH", dir)

	term, err := defaultEmbeddedTerminalStarter(actions.AgentLaunchContext{
		Command:       "codex",
		LaunchID:      "launch-1",
		WorktreePath:  dir,
		InitialPrompt: "run",
	}, 20, 3)
	if err != nil {
		t.Fatalf("defaultEmbeddedTerminalStarter returned error: %v", err)
	}
	defer term.Terminate()

	detachable, ok := term.(detachableEmbeddedTerminal)
	if !ok {
		t.Fatalf("default starter returned %T, want model-facing detach method", term)
	}
	if err := detachable.Detach(); !errors.Is(err, errEmbeddedTerminalDetachUnavailable) {
		t.Fatalf("Detach error = %v, want unavailable sentinel", err)
	}
}

func TestDefaultEmbeddedTerminalStarterRendersHeadlessClaudeStreamJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	// A stream-json assistant text event; the renderer must turn it into a
	// readable line, never raw JSON.
	writeInternalFakeExecutable(t, dir, "claude", "#!/bin/sh\n"+
		`printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"hello from claude"}]}}'`+"\n")
	// tmux is available but must NOT be used for headless claude.
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("APPROACH_TMUX_LOG", logPath)
	writeInternalFakeExecutable(t, dir, "tmux", "#!/bin/sh\necho \"$@\" >> \"$APPROACH_TMUX_LOG\"\nexit 0\n")
	t.Setenv("PATH", dir)

	term, err := defaultEmbeddedTerminalStarter(actions.AgentLaunchContext{
		Command:       "claude",
		Headless:      true,
		LaunchID:      "launch-1",
		WorktreePath:  dir,
		InitialPrompt: "run",
	}, 40, 6)
	if err != nil {
		t.Fatalf("defaultEmbeddedTerminalStarter returned error: %v", err)
	}
	defer term.Terminate()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	got := strings.Join(term.VisibleLines(40, 6), "\n")
	if !strings.Contains(got, "hello from claude") {
		t.Fatalf("headless claude stream-json not rendered, got:\n%s", got)
	}
	if strings.Contains(got, `"type":`) {
		t.Fatalf("raw stream-json leaked into the panel, got:\n%s", got)
	}
	// Headless claude bypasses tmux: it runs inline and is not detachable.
	detachable, ok := term.(detachableEmbeddedTerminal)
	if !ok {
		t.Fatalf("starter returned %T, want model-facing detach method", term)
	}
	if err := detachable.Detach(); !errors.Is(err, errEmbeddedTerminalDetachUnavailable) {
		t.Fatalf("headless claude Detach error = %v, want unavailable sentinel", err)
	}
	if body, _ := os.ReadFile(logPath); strings.TrimSpace(string(body)) != "" {
		t.Fatalf("tmux must not be invoked for headless claude, log:\n%s", body)
	}
}

func writeInternalFakeExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake %s executable: %v", name, err)
	}
}

func waitInternalFileContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		body, _ := os.ReadFile(path)
		if strings.Contains(string(body), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	body, _ := os.ReadFile(path)
	t.Fatalf("%s did not contain %q:\n%s", path, want, body)
}

func TestActiveTerminalRepoPathsIncludesOnlyRunningOwnedTerminals(t *testing.T) {
	m := Model{
		embeddedTerminals: []embeddedTerminalSlot{
			{RepoPath: "/dev/alpha", Terminal: internalFakeEmbeddedTerminal{}},
			{RepoPath: "/dev/beta", Terminal: internalFakeEmbeddedTerminal{state: "exited"}},
			{RepoPath: "/dev/gamma", Terminal: internalFakeEmbeddedTerminal{state: "starting"}},
			{RepoPath: "", Terminal: internalFakeEmbeddedTerminal{}},
			{RepoPath: "/dev/delta", Terminal: nil},
		},
	}

	active := m.activeTerminalRepoPaths()

	for _, repoPath := range []string{"/dev/alpha", "/dev/gamma"} {
		if !active[repoPath] {
			t.Fatalf("active terminal repo paths missing %s: %#v", repoPath, active)
		}
	}
	for _, repoPath := range []string{"/dev/beta", "", "/dev/delta"} {
		if active[repoPath] {
			t.Fatalf("active terminal repo paths unexpectedly includes %q: %#v", repoPath, active)
		}
	}
}

func TestActiveTerminalRepoPathsClearsWhenLastRunningSlotIsDismissed(t *testing.T) {
	m := Model{
		embeddedTerminals: []embeddedTerminalSlot{
			{ID: 1, Number: 1, Scope: embeddedTerminalScopeSession, RepoPath: "/dev/alpha", Terminal: internalFakeEmbeddedTerminal{state: "exited"}},
			{ID: 2, Number: 2, Scope: embeddedTerminalScopeSession, RepoPath: "/dev/alpha", Terminal: internalFakeEmbeddedTerminal{}},
		},
	}

	if !m.activeTerminalRepoPaths()["/dev/alpha"] {
		t.Fatalf("repo should remain active while one matching slot is running")
	}

	m = m.dismissEmbeddedTerminal(2)

	if m.activeTerminalRepoPaths()["/dev/alpha"] {
		t.Fatalf("repo should stop being active after last running slot is dismissed")
	}
}

func TestOpenEmbeddedTerminalStoresSessionRepoPath(t *testing.T) {
	m := Model{
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		},
	}

	next, opened, err := m.openEmbeddedTerminal(actions.AgentLaunchContext{
		RepoPath:     "/dev/alpha/",
		WorktreePath: "/dev/alpha-worktrees/feature",
		WorkingDir:   "/dev/alpha-worktrees/feature/subdir",
	}, sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1"})
	if err != nil {
		t.Fatalf("open embedded terminal returned error: %v", err)
	}
	if !opened {
		t.Fatal("open embedded terminal should open a slot")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminal count = %d, want 1", len(next.embeddedTerminals))
	}
	if got := next.embeddedTerminals[0].RepoPath; got != "/dev/alpha" {
		t.Fatalf("slot repo path = %q, want cleaned repo path %q", got, "/dev/alpha")
	}
	if got := next.embeddedTerminals[0].WorktreePath; got != "/dev/alpha-worktrees/feature" {
		t.Fatalf("slot worktree path = %q, want cleaned worktree path", got)
	}
	if got := next.embeddedTerminals[0].WorkingDir; got != "/dev/alpha-worktrees/feature/subdir" {
		t.Fatalf("slot working dir = %q, want cleaned working dir", got)
	}
}

func TestOpenFlowEmbeddedTerminalStoresFlowRepoPath(t *testing.T) {
	m := Model{
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		},
	}

	next, opened, err, _ := m.openFlowEmbeddedTerminal(actions.AgentLaunchContext{
		Command:       "codex",
		RepoPath:      "/dev/alpha",
		WorktreePath:  "/dev/alpha-worktrees/feature",
		FlowID:        "flow-1",
		FlowPhaseID:   "implementation",
		InitialPrompt: "Build it",
	})
	if err != nil {
		t.Fatalf("open Flow embedded terminal returned error: %v", err)
	}
	if !opened {
		t.Fatal("open Flow embedded terminal should open a slot")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminal count = %d, want 1", len(next.embeddedTerminals))
	}
	if got := next.embeddedTerminals[0].RepoPath; got != "/dev/alpha" {
		t.Fatalf("slot repo path = %q, want repo path %q", got, "/dev/alpha")
	}
}

func TestViewMarksRepoWithRunningTerminalByCleanRepoPath(t *testing.T) {
	m := Model{
		width:      80,
		height:     12,
		mode:       ui.ModeWorktrees,
		activePane: 0,
		repos: newRepoPane().SetItems([]scanner.Repo{
			{Path: "/dev/alpha", DisplayName: "alpha"},
			{Path: "/dev/alpha-worktrees/feature", DisplayName: "feature"},
		}),
		embeddedTerminals: []embeddedTerminalSlot{
			{
				RepoPath: "/dev/alpha/",
				Terminal: internalFakeEmbeddedTerminal{},
			},
		},
	}

	view := ansi.Strip(m.View())

	if !strings.Contains(view, " > ● alpha") {
		t.Fatalf("view should mark repo with running terminal:\n%s", view)
	}
	if !strings.Contains(view, "     feature") {
		t.Fatalf("view should reserve inactive marker spacing for worktree row without marking it:\n%s", view)
	}
	if strings.Contains(view, "● feature") {
		t.Fatalf("view should not mark worktree path as active repo:\n%s", view)
	}
}

func TestSyncActiveFlowTerminalToSelectedFlowSelectsNewestMatchingTerminal(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   3,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{state: "starting"},
		},
	}
	m.flows = m.flows.Move(1, 20, 80)

	m = m.syncActiveFlowTerminalToSelectedFlow()

	if m.activeFlowTerminalNum != 3 {
		t.Fatalf("active Flow terminal = %d, want newest matching terminal 3", m.activeFlowTerminalNum)
	}
}

func TestSyncActiveFlowTerminalToSelectedFlowPreservesActiveTerminalWhenSelectedFlowHasNoMatch(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{state: "exited"},
		},
	}
	m.flows = m.flows.Move(1, 20, 80)

	m = m.syncActiveFlowTerminalToSelectedFlow()

	if m.activeFlowTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want unchanged terminal 1", m.activeFlowTerminalNum)
	}
}

func TestSyncActiveFlowTerminalToSelectedFlowPreservesCurrentMatchingTerminal(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
	)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}

	m = m.syncActiveFlowTerminalToSelectedFlow()

	if m.activeFlowTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want current matching terminal 1", m.activeFlowTerminalNum)
	}
}

func TestMoveCursorSyncsActiveFlowTerminalToSelectedFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}

	m = m.moveCursor(1)

	if m.activeFlowTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeFlowTerminalNum)
	}
	if m.flowFocus != flowFocusList {
		t.Fatalf("flow focus = %d, want list focus", m.flowFocus)
	}
	if m.terminalPrefixActive {
		t.Fatal("list navigation should not enable terminal command mode")
	}
}

func TestFlowFilterSyncsActiveTerminalToSelectedFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Alpha"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Bravo"},
	)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	m = m.setSearchActive(true)

	next, _ := m.handleSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Bravo")})
	m = next.(Model)

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeFlowTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeFlowTerminalNum)
	}
	if m.flowFocus != flowFocusList {
		t.Fatalf("flow focus = %d, want list focus", m.flowFocus)
	}
}

func TestMoveSelectedFlowPhaseSyncsTerminalWhenCrossingToNextFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{
			FlowID:   "flow-1",
			RepoPath: "/dev/alpha",
			Title:    "Flow one",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			},
		},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.expandedFlowID = "flow-1"
	m.selectedFlowPhaseID = "implementation"
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:      1,
			Scope:       embeddedTerminalScopeFlow,
			FlowID:      "flow-1",
			FlowPhaseID: "implementation",
			Terminal:    internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}

	m = m.moveCursor(1)

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeFlowTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeFlowTerminalNum)
	}
}

func TestHandleFlowResultSyncsTerminalAfterPreservingSelectedFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.flows = m.flows.Move(1, 20, 80)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 42
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two updated"},
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeFlowTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeFlowTerminalNum)
	}
}

func TestHandleFlowResultPreservesActiveTerminalWhileTerminalFocused(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeFlowTerminalNum = 2
	m.flowFocus = flowFocusTerminal
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 44
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one updated"},
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two updated"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-1" {
		t.Fatalf("selected Flow = %q, want flow-1", got)
	}
	if m.activeFlowTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want explicitly selected terminal 2", m.activeFlowTerminalNum)
	}
}

func TestHandleFlowResultOffFlowsModeDoesNotRetargetActiveTerminal(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.flows = m.flows.Move(1, 20, 80)
	m.mode = ui.ModeWorktrees
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 45
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two updated"},
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeFlowTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want unchanged terminal 1 off flows mode", m.activeFlowTerminalNum)
	}
}

func TestHandleFlowResultPreservesActiveTerminalWhenClampedSelectionHasNoMatch(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.flows = m.flows.Move(1, 20, 80)
	m.activeFlowTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 43
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-3", RepoPath: "/dev/alpha", Title: "Flow three"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-3" {
		t.Fatalf("selected Flow = %q, want clamped flow-3", got)
	}
	if m.activeFlowTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want unchanged terminal 1", m.activeFlowTerminalNum)
	}
}

func TestDismissLastFlowTerminalClearsFlowCommandStateOnly(t *testing.T) {
	m := Model{
		mode:                      ui.ModeFlows,
		activePane:                1,
		activeEmbeddedTerminalNum: 1,
		activeFlowTerminalNum:     1,
		flowFocus:                 flowFocusTerminal,
		terminalPrefixActive:      true,
		embeddedTerminals: []embeddedTerminalSlot{
			{
				Number:   1,
				Scope:    embeddedTerminalScopeSession,
				Provider: "codex",
				Identity: "session",
				Terminal: internalFakeEmbeddedTerminal{},
				ID:       1,
			},
			{
				Number:      1,
				Scope:       embeddedTerminalScopeFlow,
				Provider:    "codex",
				Identity:    "implementation",
				FlowID:      "flow-1",
				FlowPhaseID: "implementation",
				Terminal:    internalFakeEmbeddedTerminal{state: "exited"},
				ID:          2,
			},
		},
	}

	m = m.dismissEmbeddedTerminal(2)

	if len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].Scope != embeddedTerminalScopeSession {
		t.Fatalf("remaining terminals = %#v, want only session terminal", m.embeddedTerminals)
	}
	if m.activeEmbeddedTerminalNum != 1 {
		t.Fatalf("active session terminal = %d, want 1", m.activeEmbeddedTerminalNum)
	}
	if m.activeFlowTerminalNum != 0 {
		t.Fatalf("active Flow terminal = %d, want 0", m.activeFlowTerminalNum)
	}
	if m.flowFocus != flowFocusList {
		t.Fatalf("flow focus = %d, want list", m.flowFocus)
	}
	if m.terminalPrefixActive {
		t.Fatal("terminal command state should clear after the last Flow terminal is dismissed")
	}
}

func TestDismissLastFlowTerminalPreservesSessionCommandState(t *testing.T) {
	m := Model{
		mode:                      ui.ModeSessions,
		activeEmbeddedTerminalNum: 1,
		activeFlowTerminalNum:     1,
		flowFocus:                 flowFocusTerminal,
		terminalPrefixActive:      true,
		embeddedTerminals: []embeddedTerminalSlot{
			{
				Number:   1,
				Scope:    embeddedTerminalScopeSession,
				Provider: "codex",
				Identity: "session",
				Terminal: internalFakeEmbeddedTerminal{},
				ID:       1,
			},
			{
				Number:      1,
				Scope:       embeddedTerminalScopeFlow,
				Provider:    "codex",
				Identity:    "implementation",
				FlowID:      "flow-1",
				FlowPhaseID: "implementation",
				Terminal:    internalFakeEmbeddedTerminal{state: "exited"},
				ID:          2,
			},
		},
	}

	m = m.dismissEmbeddedTerminal(2)

	if len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].Scope != embeddedTerminalScopeSession {
		t.Fatalf("remaining terminals = %#v, want only session terminal", m.embeddedTerminals)
	}
	if m.activeEmbeddedTerminalNum != 1 {
		t.Fatalf("active session terminal = %d, want 1", m.activeEmbeddedTerminalNum)
	}
	if !m.terminalPrefixActive {
		t.Fatal("session terminal command state should survive background Flow terminal dismissal")
	}
}

func TestSessionTerminalPrefixDDetachesActiveTerminal(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-agent-session"}
	var gotTarget, gotCWD string
	m := Model{
		mode:                      ui.ModeSessions,
		activeEmbeddedTerminalNum: 1,
		terminalPrefixActive:      true,
		launchDetachedTerminal: func(target, cwd string) (actions.TerminalLaunchSpec, error) {
			gotTarget = target
			gotCWD = cwd
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		finalizeAgentSession: func(actions.AgentLaunchContext) error {
			t.Fatal("detach should not finalize the agent session")
			return nil
		},
		embeddedTerminals: []embeddedTerminalSlot{{
			Number:       1,
			Scope:        embeddedTerminalScopeSession,
			Provider:     "codex",
			Identity:     "session",
			RepoPath:     "/dev/repo",
			WorktreePath: "/dev/worktree",
			WorkingDir:   "/dev/worktree/subdir",
			Terminal:     term,
			ID:           1,
		}},
	}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if !term.detached {
		t.Fatal("terminal was not detached")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatalf("embedded terminals = %#v, want detached terminal dismissed", next.embeddedTerminals)
	}
	if gotTarget != "approach-agent-session" {
		t.Fatalf("handoff target = %q, want approach-agent-session", gotTarget)
	}
	if gotCWD != "/dev/worktree/subdir" {
		t.Fatalf("handoff cwd = %q, want /dev/worktree/subdir", gotCWD)
	}
	if cmd == nil {
		t.Fatal("detach should return a handoff command")
	}
	if !strings.Contains(next.status.Text, "opening terminal: approach-agent-session") {
		t.Fatalf("status = %q, want opening-terminal detach target", next.status.Text)
	}
	msg, ok := cmd().(EmbeddedTerminalDetachHandoffResultMsg)
	if !ok {
		t.Fatalf("handoff command message = %#v, want EmbeddedTerminalDetachHandoffResultMsg", msg)
	}
	if msg.Target != "approach-agent-session" || msg.Err != "" {
		t.Fatalf("handoff message = %#v, want successful target", msg)
	}
	updated, _ := next.Update(msg)
	next = updated.(Model)
	if !strings.Contains(next.status.Text, "Detached embedded terminal and opened terminal: approach-agent-session") {
		t.Fatalf("status = %q, want opened-terminal detach target", next.status.Text)
	}
}

func TestFlowTerminalPrefixDDetachesActiveTerminalAndRenumbers(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-flow-agent"}
	var gotTarget, gotCWD string
	m := Model{
		mode:                  ui.ModeFlows,
		activePane:            1,
		flowFocus:             flowFocusTerminal,
		activeFlowTerminalNum: 1,
		terminalPrefixActive:  true,
		terminalConfirmID:     1,
		terminalConfirmScope:  embeddedTerminalScopeFlow,
		modal:                 modal.OpenConfirm(embeddedTerminalTerminatePrompt, nil),
		launchDetachedTerminal: func(target, cwd string) (actions.TerminalLaunchSpec, error) {
			gotTarget = target
			gotCWD = cwd
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		finalizeAgentSession: func(actions.AgentLaunchContext) error {
			t.Fatal("detach should not finalize the agent session")
			return nil
		},
		embeddedTerminals: []embeddedTerminalSlot{
			{
				Number:       1,
				Scope:        embeddedTerminalScopeFlow,
				Provider:     "codex",
				Identity:     "implementation",
				RepoPath:     "/dev/repo",
				WorktreePath: "/dev/worktree",
				FlowID:       "flow-1",
				FlowPhaseID:  "implementation",
				Terminal:     term,
				ID:           1,
			},
			{
				Number:      2,
				Scope:       embeddedTerminalScopeFlow,
				Provider:    "claude",
				Identity:    "review",
				FlowID:      "flow-1",
				FlowPhaseID: "review",
				Terminal:    internalFakeEmbeddedTerminal{},
				ID:          2,
			},
			{
				Number:   1,
				Scope:    embeddedTerminalScopeSession,
				Provider: "codex",
				Identity: "session",
				Terminal: internalFakeEmbeddedTerminal{},
				ID:       3,
			},
		},
	}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, embeddedTerminalScopeFlow)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if !term.detached {
		t.Fatal("terminal was not detached")
	}
	if len(next.embeddedTerminals) != 2 {
		t.Fatalf("embedded terminals = %#v, want detached Flow terminal removed only", next.embeddedTerminals)
	}
	if next.embeddedTerminals[0].Scope != embeddedTerminalScopeFlow || next.embeddedTerminals[0].Number != 1 {
		t.Fatalf("remaining Flow terminal not renumbered to 1: %#v", next.embeddedTerminals)
	}
	if next.embeddedTerminals[1].Scope != embeddedTerminalScopeSession || next.embeddedTerminals[1].Number != 1 {
		t.Fatalf("session terminal should be preserved: %#v", next.embeddedTerminals)
	}
	if next.terminalConfirmID != 0 || next.modal.View().Kind != modal.None {
		t.Fatalf("stale confirmation was not cleared: confirm=%d modal=%#v", next.terminalConfirmID, next.modal.View())
	}
	if activity := next.flowTerminalActivity(); len(activity) != 1 || activity[0].PhaseID != "review" {
		t.Fatalf("flow terminal activity = %#v, want only remaining Flow terminal", activity)
	}
	if gotTarget != "approach-flow-agent" {
		t.Fatalf("handoff target = %q, want approach-flow-agent", gotTarget)
	}
	if gotCWD != "/dev/worktree" {
		t.Fatalf("handoff cwd = %q, want /dev/worktree", gotCWD)
	}
	if cmd == nil {
		t.Fatal("detach should return a handoff command")
	}
}

func TestTerminalPrefixDReportsHandoffConstructionFailureAfterDetach(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-agent-session"}
	m := Model{
		mode:                      ui.ModeSessions,
		activeEmbeddedTerminalNum: 1,
		terminalPrefixActive:      true,
		launchDetachedTerminal: func(string, string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("no external terminal")
		},
		embeddedTerminals: []embeddedTerminalSlot{{
			Number:   1,
			Scope:    embeddedTerminalScopeSession,
			Provider: "codex",
			Identity: "session",
			Terminal: term,
			ID:       1,
		}},
	}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if !term.detached {
		t.Fatal("terminal was not detached")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatalf("embedded terminals = %#v, want detached terminal dismissed", next.embeddedTerminals)
	}
	if cmd != nil {
		t.Fatal("construction failure should not return a handoff command")
	}
	if !strings.Contains(next.status.Text, "Detached embedded terminal, but failed to open terminal: no external terminal") {
		t.Fatalf("status = %q, want detach-success handoff failure", next.status.Text)
	}
}

func TestTerminalPrefixDReportsHandoffRunFailureAfterDetach(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-agent-session"}
	cleaned := false
	m := Model{
		mode:                      ui.ModeSessions,
		activeEmbeddedTerminalNum: 1,
		terminalPrefixActive:      true,
		launchDetachedTerminal: func(string, string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{
				Cmd:     exec.Command("sh", "-c", "exit 7"),
				Cleanup: func() { cleaned = true },
			}, nil
		},
		embeddedTerminals: []embeddedTerminalSlot{{
			Number:   1,
			Scope:    embeddedTerminalScopeSession,
			Provider: "codex",
			Identity: "session",
			Terminal: term,
			ID:       1,
		}},
	}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatalf("embedded terminals = %#v, want detached terminal dismissed", next.embeddedTerminals)
	}
	if cmd == nil {
		t.Fatal("detach should return a handoff command")
	}
	msg, ok := cmd().(EmbeddedTerminalDetachHandoffResultMsg)
	if !ok {
		t.Fatalf("handoff command message = %#v, want EmbeddedTerminalDetachHandoffResultMsg", msg)
	}
	if msg.Err == "" {
		t.Fatalf("handoff message = %#v, want run error", msg)
	}
	if !cleaned {
		t.Fatal("handoff cleanup should run when handoff command fails")
	}
	updated, _ := next.Update(msg)
	next = updated.(Model)
	if !strings.Contains(next.status.Text, "Detached embedded terminal, but failed to open terminal:") {
		t.Fatalf("status = %q, want detach-success handoff failure", next.status.Text)
	}
}

func TestTerminalPrefixDReportsUnavailableForDirectPTY(t *testing.T) {
	m := Model{
		mode:                      ui.ModeSessions,
		activeEmbeddedTerminalNum: 1,
		terminalPrefixActive:      true,
		embeddedTerminals: []embeddedTerminalSlot{{
			Number:   1,
			Scope:    embeddedTerminalScopeSession,
			Provider: "codex",
			Identity: "session",
			Terminal: internalFakeEmbeddedTerminal{},
			ID:       1,
		}},
	}

	next, _, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("non-detachable terminal should remain, got %#v", next.embeddedTerminals)
	}
	if !strings.Contains(next.status.Text, "Detach unavailable") {
		t.Fatalf("status = %q, want unavailable message", next.status.Text)
	}
}

func TestFlowTerminalInputModeDPassesThrough(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-flow-agent"}
	m := Model{
		mode:                  ui.ModeFlows,
		activePane:            1,
		flowFocus:             flowFocusTerminal,
		activeFlowTerminalNum: 1,
		terminalPrefixActive:  false,
		embeddedTerminals: []embeddedTerminalSlot{{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			Provider: "codex",
			Identity: "implementation",
			Terminal: term,
			ID:       1,
		}},
	}

	next, _, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, embeddedTerminalScopeFlow)

	if !consumed {
		t.Fatal("terminal input key should be consumed")
	}
	if term.detached {
		t.Fatal("input-mode d should not detach")
	}
	if len(term.writes) != 1 || string(term.writes[0]) != "d" {
		t.Fatalf("writes = %#v, want d passed through", term.writes)
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("terminal should remain, got %#v", next.embeddedTerminals)
	}
}
