package model

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
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
}

func TestOpenFlowEmbeddedTerminalStoresFlowRepoPath(t *testing.T) {
	m := Model{
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		},
	}

	next, opened, err := m.openFlowEmbeddedTerminal(actions.AgentLaunchContext{
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
