package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRootViewUsesAltScreen(t *testing.T) {
	view := (Model{}).View()
	if !view.AltScreen {
		t.Fatal("root view must keep the TUI in the alternate screen")
	}
}

func TestDeferQuitDuringFlowLaunchPassesQuitWithoutPendingHandoff(t *testing.T) {
	msg := DeferQuitDuringFlowLaunch(Model{}, tea.QuitMsg{})
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("quit without a pending Flow handoff = %T, want tea.QuitMsg", msg)
	}
}
