package model

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

func TestStartupPaneStateForEveryConfiguredMode(t *testing.T) {
	tests := []struct {
		name          string
		configured    ui.Mode
		wantDefault   ui.Mode
		wantTop       ui.Mode
		wantBottom    ui.Mode
		wantContent   ui.Pane
		wantDisplayed ui.Mode
		wantTakeover  bool
	}{
		{name: "zero", wantDefault: ui.ModeWorktrees, wantTop: ui.ModeWorktrees, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeWorktrees},
		{name: "worktrees", configured: ui.ModeWorktrees, wantDefault: ui.ModeWorktrees, wantTop: ui.ModeWorktrees, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeWorktrees},
		{name: "branches", configured: ui.ModeBranches, wantDefault: ui.ModeBranches, wantTop: ui.ModeBranches, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeBranches},
		{name: "stashes", configured: ui.ModeStashes, wantDefault: ui.ModeStashes, wantTop: ui.ModeStashes, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeStashes},
		{name: "history", configured: ui.ModeHistory, wantDefault: ui.ModeHistory, wantTop: ui.ModeHistory, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeHistory},
		{name: "reflog", configured: ui.ModeReflog, wantDefault: ui.ModeReflog, wantTop: ui.ModeReflog, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeReflog},
		{name: "sessions", configured: ui.ModeSessions, wantDefault: ui.ModeSessions, wantTop: ui.ModeWorktrees, wantBottom: ui.ModeSessions, wantContent: ui.PaneBottom, wantDisplayed: ui.ModeSessions},
		{name: "plans", configured: ui.ModePlans, wantDefault: ui.ModePlans, wantTop: ui.ModeWorktrees, wantBottom: ui.ModePlans, wantContent: ui.PaneBottom, wantDisplayed: ui.ModePlans},
		{name: "flows", configured: ui.ModeFlows, wantDefault: ui.ModeFlows, wantTop: ui.ModeWorktrees, wantBottom: ui.ModeFlows, wantContent: ui.PaneBottom, wantDisplayed: ui.ModeFlows},
		{name: "active flows", configured: ui.ModeActiveFlows, wantDefault: ui.ModeActiveFlows, wantTop: ui.ModeWorktrees, wantBottom: ui.ModeFlows, wantContent: ui.PaneBottom, wantDisplayed: ui.ModeActiveFlows, wantTakeover: true},
		{name: "beads ready", configured: ui.ModeBeadsReady, wantDefault: ui.ModeBeadsReady, wantTop: ui.ModeBeadsReady, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeBeadsReady},
		{name: "beads blocked", configured: ui.ModeBeadsBlocked, wantDefault: ui.ModeBeadsBlocked, wantTop: ui.ModeBeadsBlocked, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeBeadsBlocked},
		{name: "beads open", configured: ui.ModeBeadsOpen, wantDefault: ui.ModeBeadsOpen, wantTop: ui.ModeBeadsOpen, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeBeadsOpen},
		{name: "beads in progress", configured: ui.ModeBeadsInProgress, wantDefault: ui.ModeBeadsInProgress, wantTop: ui.ModeBeadsInProgress, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeBeadsInProgress},
		{name: "beads closed", configured: ui.ModeBeadsClosed, wantDefault: ui.ModeBeadsClosed, wantTop: ui.ModeBeadsClosed, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeBeadsClosed},
		{name: "after accepted range", configured: ui.ModeBeadsClosed + 1, wantDefault: ui.ModeWorktrees, wantTop: ui.ModeWorktrees, wantBottom: ui.ModeFlows, wantContent: ui.PaneTop, wantDisplayed: ui.ModeWorktrees},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{StartupMode: tt.configured})
			if m.activePane != ui.PaneRepos {
				t.Fatalf("activePane = %d, want repos", m.activePane)
			}
			if m.contentPane != tt.wantContent || m.topMode != tt.wantTop || m.bottomMode != tt.wantBottom {
				t.Fatalf("stored state = content %d top %d bottom %d, want content %d top %d bottom %d", m.contentPane, m.topMode, m.bottomMode, tt.wantContent, tt.wantTop, tt.wantBottom)
			}
			if m.activeFlowSurface != tt.wantTakeover {
				t.Fatalf("activeFlowSurface = %t, want %t", m.activeFlowSurface, tt.wantTakeover)
			}
			if got := m.DefaultView(); got != tt.wantDefault {
				t.Fatalf("DefaultView() = %d, want %d", got, tt.wantDefault)
			}
			if got := m.Mode(); got != tt.wantDisplayed {
				t.Fatalf("Mode() = %d, want %d", got, tt.wantDisplayed)
			}
		})
	}
}

func TestStoredPaneModesSurviveFocusAndTakeoverTransitions(t *testing.T) {
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{StartupMode: ui.ModeWorktrees})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.activePane != ui.PaneTop || m.contentPane != ui.PaneTop {
		t.Fatalf("initial content focus = active %d content %d, want top/top", m.activePane, m.contentPane)
	}

	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
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
	m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
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
		m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{StartupMode: ui.ModeWorktrees})
		request := m.currentListRequest(ui.ModeWorktrees)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
		m = updatePaneStateTestModel(t, m, WorktreeResultMsg{RepoPath: "/repo", ListRequest: request, Worktrees: []gitquery.Worktree{{Path: "/repo", BranchName: "main"}}})
		if len(m.Worktrees()) != 1 || m.Mode() != ui.ModePlans {
			t.Fatalf("off-screen Git result = rows %d mode %d, want cached row with Plans still focused", len(m.Worktrees()), m.Mode())
		}
	})

	t.Run("bottom cache may fill while top pane is focused", func(t *testing.T) {
		m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{StartupMode: ui.ModeSessions})
		request := m.currentListRequest(ui.ModeSessions)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
		m = updatePaneStateTestModel(t, m, SessionResultMsg{RepoPath: "/repo", ListRequest: request, Sessions: []sessions.SessionRecord{{SessionID: "session-1"}}})
		if len(m.Sessions()) != 1 || m.Mode() != ui.ModeWorktrees {
			t.Fatalf("off-screen Sessions result = rows %d mode %d, want cached row with Worktrees still focused", len(m.Sessions()), m.Mode())
		}
	})

	t.Run("Beads requires the exact focused subview", func(t *testing.T) {
		m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{StartupMode: ui.ModeBeadsOpen})
		request := m.currentListRequest(ui.ModeBeadsOpen)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
		m = updatePaneStateTestModel(t, m, BeadsOpenResultMsg{RepoPath: "/repo", ListRequest: request, Available: true, Beads: []beadsquery.Bead{{ID: "bd-1"}}})
		if len(m.Beads(ui.ModeBeadsOpen)) != 0 || m.Mode() != ui.ModePlans {
			t.Fatalf("off-screen Beads result = rows %d mode %d, want rejected result with Plans focused", len(m.Beads(ui.ModeBeadsOpen)), m.Mode())
		}
	})

	t.Run("off-screen list errors stay hidden", func(t *testing.T) {
		m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{StartupMode: ui.ModeWorktrees})
		request := m.currentListRequest(ui.ModeWorktrees)
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
		m = updatePaneStateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
		m = updatePaneStateTestModel(t, m, FetchErrorMsg{RepoPath: "/repo", Pane: "worktrees", Err: "boom", Kind: FetchList, Mode: ui.ModeWorktrees, ListRequest: request})
		if m.TransientError() != "" || m.Mode() != ui.ModePlans {
			t.Fatalf("off-screen error = %q mode %d, want hidden error with Plans focused", m.TransientError(), m.Mode())
		}
	})
}
