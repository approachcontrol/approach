package model

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

func TestFixedStackedPaneStartupState(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	if m.activePane != ui.PaneRepos {
		t.Fatalf("activePane = %d, want repositories", m.activePane)
	}
	if m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModeFlows || m.contentPane != ui.PaneTop {
		t.Fatalf("startup panes = top %d bottom %d remembered %d, want Beads Open / Flows / top", m.topMode, m.bottomMode, m.contentPane)
	}
	if m.activeFlowSurface {
		t.Fatal("startup unexpectedly entered Active Flows takeover")
	}
}

func TestInitFetchesBothStoredPanesWithoutActiveFlows(t *testing.T) {
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1"}}, nil
		},
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			if filter.RepoPath == "" {
				t.Fatal("Init fetched global Active Flows")
			}
			return []flowstore.FlowRecord{{FlowID: "flow-1"}}, nil
		},
	})

	var beadsResults, flowResults int
	for _, msg := range collectInitMessages(m.Init()) {
		switch msg.(type) {
		case BeadsOpenResultMsg:
			beadsResults++
		case FlowResultMsg:
			flowResults++
		case ActiveFlowResultMsg:
			t.Fatal("Init emitted an Active Flows result")
		}
	}
	if beadsResults != 1 || flowResults != 1 {
		t.Fatalf("Init results = Beads %d Flows %d, want one of each", beadsResults, flowResults)
	}
}

func TestRepositorySwitchStartsBothStoredPaneRequests(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/one"}, {Path: "/two"}})
	beforeTop := m.currentListRequest(ui.ModeBeadsOpen)
	beforeBottom := m.currentListRequest(ui.ModeFlows)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("repository switch returned no stored-pane fetch command")
	}
	if m.currentListRequest(ui.ModeBeadsOpen) == beforeTop || m.currentListRequest(ui.ModeFlows) == beforeBottom {
		t.Fatalf("request tokens = Beads %d Flows %d, want both advanced from %d/%d", m.currentListRequest(ui.ModeBeadsOpen), m.currentListRequest(ui.ModeFlows), beforeTop, beforeBottom)
	}
}

func TestPaneLocalNumbersAndArrowNavigation(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})

	// Repository focus suppresses pane-local numbers.
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModeFlows {
		t.Fatalf("repository number mutated stored modes to %d/%d", m.topMode, m.bottomMode)
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.topMode != ui.ModeWorktrees || m.bottomMode != ui.ModeFlows || m.activePane != ui.PaneTop {
		t.Fatalf("top 1 = top %d bottom %d focus %d, want Git / Flows / top", m.topMode, m.bottomMode, m.activePane)
	}
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModeFlows {
		t.Fatalf("top right = top %d bottom %d, want sticky Beads / unchanged Flows", m.topMode, m.bottomMode)
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModeSessions || m.activePane != ui.PaneBottom {
		t.Fatalf("bottom 1 = top %d bottom %d focus %d, want unchanged Beads / Sessions / bottom", m.topMode, m.bottomMode, m.activePane)
	}
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.bottomMode != ui.ModeFlows || m.topMode != ui.ModeBeadsOpen {
		t.Fatalf("bottom left = top %d bottom %d, want unchanged Beads / wrapped Flows", m.topMode, m.bottomMode)
	}
}

func TestPaneFocusCyclesForwardAndBackward(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	for i, want := range []ui.Pane{ui.PaneTop, ui.PaneBottom, ui.PaneRepos} {
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.activePane != want {
			t.Fatalf("forward step %d focus = %d, want %d", i, m.activePane, want)
		}
	}
	for i, want := range []ui.Pane{ui.PaneBottom, ui.PaneTop, ui.PaneRepos} {
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
		if m.activePane != want {
			t.Fatalf("backward step %d focus = %d, want %d", i, m.activePane, want)
		}
	}
}

func TestRepositoryEnterFocusesTopAfterBottomPaneWasRemembered(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})

	if m.activePane != ui.PaneRepos || m.contentPane != ui.PaneBottom {
		t.Fatalf("precondition focus = active %d remembered %d, want repos/bottom", m.activePane, m.contentPane)
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activePane != ui.PaneTop || m.contentPane != ui.PaneTop || !m.repoPaneCollapsed {
		t.Fatalf("Enter focus = active %d remembered %d collapsed %t, want top/top/true", m.activePane, m.contentPane, m.repoPaneCollapsed)
	}
}

func TestPaneFocusCyclesSkipReposWhenCollapsed(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	m.repoPaneCollapsed = true
	m.activePane = ui.PaneTop
	m.contentPane = ui.PaneTop
	for i, want := range []ui.Pane{ui.PaneBottom, ui.PaneTop} {
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		if m.activePane != want {
			t.Fatalf("collapsed forward step %d focus = %d, want %d", i, m.activePane, want)
		}
	}
	for i, want := range []ui.Pane{ui.PaneBottom, ui.PaneTop} {
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
		if m.activePane != want {
			t.Fatalf("collapsed backward step %d focus = %d, want %d", i, m.activePane, want)
		}
	}
}

func TestPaneModeSwitchPreservesOtherPaneState(t *testing.T) {
	t.Run("top switch preserves bottom Flow state", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		m.topMode = ui.ModeWorktrees
		m.bottomMode = ui.ModeFlows
		m.activePane = ui.PaneTop
		m.contentPane = ui.PaneTop
		m.flows = newFlowPane().SetItems([]flowstore.FlowRecord{{FlowID: "one"}, {FlowID: "two"}}).Move(1, 1, 80)
		m.expandedFlowID = "two"
		m.selectedFlowPhaseID = "implementation"

		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})

		if m.FlowSelected() != 1 || m.expandedFlowID != "two" || m.selectedFlowPhaseID != "implementation" {
			t.Fatalf("top switch reset bottom Flow state: selected=%d expanded=%q phase=%q", m.FlowSelected(), m.expandedFlowID, m.selectedFlowPhaseID)
		}
	})

	t.Run("bottom switch preserves top inline sessions", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		m.topMode = ui.ModeWorktrees
		m.bottomMode = ui.ModeFlows
		m.activePane = ui.PaneBottom
		m.contentPane = ui.PaneBottom
		m.inlineWorktreeSessionPath = "/repo-worktree"

		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})

		if m.inlineWorktreeSessionPath != "/repo-worktree" {
			t.Fatalf("bottom switch cleared top inline session path: %q", m.inlineWorktreeSessionPath)
		}
	})

	t.Run("Active Flows preserves both stored panes", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		m.topMode = ui.ModeWorktrees
		m.bottomMode = ui.ModePlans
		m.activePane = ui.PaneBottom
		m.contentPane = ui.PaneBottom
		m.inlineWorktreeSessionPath = "/repo-worktree"
		m.plans = newPlanPane().SetItems([]planstore.PlanRecord{{PlanID: "one"}, {PlanID: "two"}}).Move(1, 1, 80)

		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})

		if m.inlineWorktreeSessionPath != "/repo-worktree" || m.PlanSelected() != 1 {
			t.Fatalf("Active Flows reset stored state: inline=%q plan selected=%d", m.inlineWorktreeSessionPath, m.PlanSelected())
		}
	})
}

func TestActiveFlowsReverseCycleActivatesContentBeforeTerminal(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	m.activeFlowSurface = true
	m.activePane = ui.PaneRepos
	m.contentPane = ui.PaneBottom
	m.terminalDockVisible = true
	m.embeddedTerminals = []embeddedTerminalSlot{{Number: 1, Terminal: internalFakeEmbeddedTerminal{state: "running"}}}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.activePane != ui.PaneBottom || m.terminalFocus != terminalFocusTerminal || !m.terminalPrefixActive {
		t.Fatalf("reverse terminal focus = pane %d focus %d prefix %t, want bottom/terminal/true", m.activePane, m.terminalFocus, m.terminalPrefixActive)
	}
}

func TestActiveFlowsTerminalReturnPreservesRememberedContentPane(t *testing.T) {
	tests := []struct {
		name       string
		key        tea.KeyType
		remembered ui.Pane
	}{
		{name: "forward returns to remembered bottom", key: tea.KeyTab, remembered: ui.PaneBottom},
		{name: "backward returns to remembered top", key: tea.KeyBackspace, remembered: ui.PaneTop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New([]scanner.Repo{{Path: "/repo"}})
			m.activeFlowSurface = true
			m.repoPaneCollapsed = true
			m.activePane = tt.remembered
			m.contentPane = tt.remembered
			m.terminalFocus = terminalFocusTerminal
			m.terminalPrefixActive = true

			m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tt.key})

			if m.activePane != tt.remembered || m.contentPane != tt.remembered || m.terminalFocus != terminalFocusList || m.terminalPrefixActive {
				t.Fatalf("terminal return = active %d remembered %d focus %d prefix %t, want %d/%d/list/false", m.activePane, m.contentPane, m.terminalFocus, m.terminalPrefixActive, tt.remembered, tt.remembered)
			}
		})
	}
}

func TestDegradedPaneFocusSwapReflowsNewlyVisibleList(t *testing.T) {
	records := make([]sessions.SessionRecord, 20)
	for i := range records {
		records[i].SessionID = fmt.Sprintf("session-%02d", i)
	}
	m := New([]scanner.Repo{{Path: "/repo"}})
	m.width = 120
	m.height = 19
	m.topMode = ui.ModeWorktrees
	m.bottomMode = ui.ModeSessions
	m.activePane = ui.PaneTop
	m.contentPane = ui.PaneTop
	m.sessions = newSessionPane().SetItems(records).Move(15, 1, 80)
	if m.sessions.Scroll() != 15 {
		t.Fatalf("precondition hidden Session scroll = %d, want 15", m.sessions.Scroll())
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})

	wantScroll := len(records) - m.paneContentHeight(ui.ModeSessions)
	if m.sessions.SelectedIndex() != 15 || m.sessions.Scroll() != wantScroll {
		t.Fatalf("focused Session geometry = selected %d scroll %d, want 15/%d", m.sessions.SelectedIndex(), m.sessions.Scroll(), wantScroll)
	}
}

func TestRefreshAndActiveFlowsUseIndependentRequestSlots(t *testing.T) {
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{
		ScanRepos: func() ([]scanner.Repo, error) { return []scanner.Repo{{Path: "/repo"}}, nil },
	})
	beadsBefore := m.currentListRequest(ui.ModeBeadsOpen)
	flowsBefore := m.currentListRequest(ui.ModeFlows)
	activeBefore := m.currentListRequest(ui.ModeActiveFlows)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m = next.(Model)
	if m.currentListRequest(ui.ModeBeadsOpen) == beadsBefore || m.currentListRequest(ui.ModeFlows) == flowsBefore {
		t.Fatal("f5 did not advance both stored-pane requests")
	}
	if m.currentListRequest(ui.ModeActiveFlows) != activeBefore {
		t.Fatal("f5 outside takeover advanced Active Flows")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = next.(Model)
	activeBefore = m.currentListRequest(ui.ModeActiveFlows)
	beadsBefore = m.currentListRequest(ui.ModeBeadsOpen)
	flowsBefore = m.currentListRequest(ui.ModeFlows)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyF5})
	m = next.(Model)
	if m.currentListRequest(ui.ModeBeadsOpen) == beadsBefore || m.currentListRequest(ui.ModeFlows) == flowsBefore || m.currentListRequest(ui.ModeActiveFlows) == activeBefore {
		t.Fatal("f5 during takeover did not independently advance both stored panes and Active Flows")
	}
}

func TestActiveFlowsToggleRestoresStoredStateWithoutTopFetch(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	topMode, bottomMode, remembered, focus := m.topMode, m.bottomMode, m.contentPane, m.activePane
	topRequest := m.currentListRequest(topMode)
	next, entryCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = next.(Model)
	if entryCmd == nil || !m.activeFlowSurfaceVisible() {
		t.Fatal("Active Flows entry did not start its takeover request")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = next.(Model)
	if m.activeFlowSurfaceVisible() || m.topMode != topMode || m.bottomMode != bottomMode || m.contentPane != remembered || m.activePane != focus {
		t.Fatalf("takeover exit restored top/bottom/remembered/focus as %d/%d/%d/%d, want %d/%d/%d/%d", m.topMode, m.bottomMode, m.contentPane, m.activePane, topMode, bottomMode, remembered, focus)
	}
	if m.currentListRequest(topMode) != topRequest {
		t.Fatal("takeover exit unnecessarily refreshed the top stored pane")
	}
}

func TestActiveFlowsToggleRestoresFocusChangedDuringTakeover(t *testing.T) {
	tests := []struct {
		name  string
		setup func(Model) Model
		move  tea.KeyMsg
	}{
		{
			name: "content entry restores content after takeover moved to repos",
			setup: func(m Model) Model {
				return m.focusContentPane(ui.PaneTop)
			},
			move: tea.KeyMsg{Type: tea.KeyTab},
		},
		{
			name:  "repo entry restores repos after takeover moved to content",
			setup: func(m Model) Model { return m.focusRepoPane() },
			move:  tea.KeyMsg{Type: tea.KeyTab},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.setup(New([]scanner.Repo{{Path: "/repo"}}))
			wantActive, wantContent := m.activePane, m.contentPane
			m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
			m = updatePaneStateTestModel(t, m, tt.move)
			if m.activePane == wantActive {
				t.Fatalf("takeover focus did not move from precondition pane %d", wantActive)
			}

			m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})

			if m.activeFlowSurfaceVisible() || m.activePane != wantActive || m.contentPane != wantContent {
				t.Fatalf("takeover exit = activeFlow %t active %d content %d, want false/%d/%d", m.activeFlowSurfaceVisible(), m.activePane, m.contentPane, wantActive, wantContent)
			}
		})
	}
}

func TestStoredPaneListErrorsRemainIndependent(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	topRequest := m.currentListRequest(ui.ModeWorktrees)
	bottomRequest := m.currentListRequest(ui.ModeFlows)

	next, _ := m.Update(FetchErrorMsg{RepoPath: "/repo", Pane: "worktrees", Err: "worktrees failed", Kind: FetchList, Mode: ui.ModeWorktrees, ListRequest: topRequest})
	m = next.(Model)
	next, _ = m.Update(FetchErrorMsg{RepoPath: "/repo", Pane: "flows", Err: "flows failed", Kind: FetchList, Mode: ui.ModeFlows, ListRequest: bottomRequest})
	m = next.(Model)
	if got := m.currentListError(ui.ModeWorktrees); got != "worktrees failed" {
		t.Fatalf("top list error = %q", got)
	}
	if got := m.currentListError(ui.ModeFlows); got != "flows failed" {
		t.Fatalf("bottom list error = %q", got)
	}

	next, _ = m.Update(FlowResultMsg{RepoPath: "/repo", ListRequest: bottomRequest})
	m = next.(Model)
	if got := m.currentListError(ui.ModeWorktrees); got != "worktrees failed" {
		t.Fatalf("bottom success cleared top list error: %q", got)
	}
	if got := m.currentListError(ui.ModeFlows); got != "" {
		t.Fatalf("bottom success retained bottom list error: %q", got)
	}
}

func TestRemovedDefaultViewKeyIsUnbound(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'V'}})
	m = next.(Model)
	if cmd != nil || m.modal.IsOpen() || m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModeFlows {
		t.Fatalf("V changed state: cmd=%T modal=%t top=%d bottom=%d", cmd, m.modal.IsOpen(), m.topMode, m.bottomMode)
	}
}

func TestFlowsLKeyNoLongerAliasesBeads(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = next.(Model)
	if cmd != nil || m.activePane != ui.PaneBottom || m.bottomMode != ui.ModeFlows || m.topMode != ui.ModeBeadsOpen {
		t.Fatalf("Flows l changed pane state: cmd=%T focus=%d top=%d bottom=%d", cmd, m.activePane, m.topMode, m.bottomMode)
	}
}

func collectInitMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var messages []tea.Msg
	for _, child := range batch {
		messages = append(messages, collectInitMessages(child)...)
	}
	return messages
}

func TestStoredPaneModesSurviveFocusAndTakeoverTransitions(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo"}})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.activePane != ui.PaneTop || m.contentPane != ui.PaneTop {
		t.Fatalf("initial content focus = active %d content %d, want top/top", m.activePane, m.contentPane)
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModePlans || m.activePane != ui.PaneBottom || m.contentPane != ui.PaneBottom {
		t.Fatalf("top -> bottom state = top %d bottom %d active %d content %d", m.topMode, m.bottomMode, m.activePane, m.contentPane)
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.activePane != ui.PaneRepos || m.contentPane != ui.PaneBottom || m.Mode() != ui.ModePlans {
		t.Fatalf("repo focus = active %d content %d mode %d, want repos/bottom/plans", m.activePane, m.contentPane, m.Mode())
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if !m.activeFlowSurface || m.activePane != ui.PaneRepos || m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModePlans || m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("takeover entry changed stored state: takeover %t active %d top %d bottom %d mode %d", m.activeFlowSurface, m.activePane, m.topMode, m.bottomMode, m.Mode())
	}
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.activeFlowSurface || m.activePane != ui.PaneRepos || m.contentPane != ui.PaneBottom || m.Mode() != ui.ModePlans {
		t.Fatalf("takeover exit = takeover %t active %d content %d mode %d, want false/repos/bottom/plans", m.activeFlowSurface, m.activePane, m.contentPane, m.Mode())
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.topMode != ui.ModeBeadsOpen || m.bottomMode != ui.ModePlans || m.activePane != ui.PaneTop || m.contentPane != ui.PaneTop || m.Mode() != ui.ModeBeadsOpen {
		t.Fatalf("bottom -> top restore = top %d bottom %d active %d content %d mode %d", m.topMode, m.bottomMode, m.activePane, m.contentPane, m.Mode())
	}
}

func updatePaneStateTestModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestCrossPaneListResultCompatibility(t *testing.T) {
	t.Run("git cache may fill while bottom pane is focused", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		m.topMode = ui.ModeWorktrees
		request := m.currentListRequest(ui.ModeWorktrees)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m = updatePaneStateTestModel(t, m, WorktreeResultMsg{RepoPath: "/repo", ListRequest: request, Worktrees: []gitquery.Worktree{{Path: "/repo", BranchName: "main"}}})
		if len(m.Worktrees()) != 1 || m.Mode() != ui.ModePlans {
			t.Fatalf("off-screen Git result = rows %d mode %d, want cached row with Plans still focused", len(m.Worktrees()), m.Mode())
		}
	})

	t.Run("bottom cache may fill while top pane is focused", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		m.bottomMode = ui.ModeSessions
		request := m.currentListRequest(ui.ModeSessions)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
		m = updatePaneStateTestModel(t, m, SessionResultMsg{RepoPath: "/repo", ListRequest: request, Sessions: []sessions.SessionRecord{{SessionID: "session-1"}}})
		if len(m.Sessions()) != 1 || m.Mode() != ui.ModeWorktrees {
			t.Fatalf("off-screen Sessions result = rows %d mode %d, want cached row with Worktrees still focused", len(m.Sessions()), m.Mode())
		}
	})

	t.Run("stored Beads cache may fill while bottom pane is focused", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		request := m.currentListRequest(ui.ModeBeadsOpen)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m = updatePaneStateTestModel(t, m, BeadsOpenResultMsg{RepoPath: "/repo", ListRequest: request, Available: true, Beads: []beadsquery.Bead{{ID: "bd-1"}}})
		if len(m.Beads(ui.ModeBeadsOpen)) != 1 || m.Mode() != ui.ModePlans {
			t.Fatalf("background Beads result = rows %d mode %d, want cached row with Plans focused", len(m.Beads(ui.ModeBeadsOpen)), m.Mode())
		}
	})

	t.Run("stored background list errors are accepted", func(t *testing.T) {
		m := New([]scanner.Repo{{Path: "/repo"}})
		m.topMode = ui.ModeWorktrees
		request := m.currentListRequest(ui.ModeWorktrees)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m = updatePaneStateTestModel(t, m, FetchErrorMsg{RepoPath: "/repo", Pane: "worktrees", Err: "boom", Kind: FetchList, Mode: ui.ModeWorktrees, ListRequest: request})
		if m.TransientError() != "boom" || m.Mode() != ui.ModePlans {
			t.Fatalf("background error = %q mode %d, want accepted error with Plans focused", m.TransientError(), m.Mode())
		}
	})
}
