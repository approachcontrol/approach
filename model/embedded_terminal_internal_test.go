package model

import (
	"context"
	"testing"

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
