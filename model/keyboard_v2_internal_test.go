package model

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

type recordingKeyboardTerminal struct {
	writes [][]byte
}

func (t *recordingKeyboardTerminal) VisibleLines(int, int) []string { return nil }
func (t *recordingKeyboardTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, append([]byte(nil), p...))
	return len(p), nil
}
func (t *recordingKeyboardTerminal) Resize(int, int) error      { return nil }
func (t *recordingKeyboardTerminal) Terminate() error           { return nil }
func (t *recordingKeyboardTerminal) Wait(context.Context) error { return nil }
func (t *recordingKeyboardTerminal) State() string              { return "running" }

func TestPasteFollowsSearchAndListOwnership(t *testing.T) {
	m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{})
	m.width, m.height = 120, 24
	m.searchActive = true

	next, _ := m.Update(tea.PasteMsg{Content: "feature/123"})
	searched := next.(Model)
	if got := searched.activeSearchQuery(); got != "feature/123" {
		t.Fatalf("search paste query = %q, want feature/123", got)
	}

	searched.searchActive = false
	before := searched.activeSearchQuery()
	next, cmd := searched.Update(tea.PasteMsg{Content: "must-not-run"})
	listed := next.(Model)
	if cmd != nil || listed.activeSearchQuery() != before {
		t.Fatalf("list paste command/query = %T/%q, want nil/%q", cmd, listed.activeSearchQuery(), before)
	}
}

func TestPasteWritesVerbatimToFocusedEmbeddedTerminal(t *testing.T) {
	term := &recordingKeyboardTerminal{}
	m := modelWithModeForTest(Model{
		width:       120,
		height:      30,
		activePane:  ui.PaneBottom,
		contentPane: ui.PaneBottom,
		embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			terminalFocus:       terminalFocusTerminal,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number:   1,
				ID:       1,
				Terminal: term,
			}},
		},
	}, ui.ModeSessions)

	next, cmd := m.Update(tea.PasteMsg{Content: "echo α\n"})
	if cmd != nil {
		t.Fatalf("terminal paste returned command %T", cmd)
	}
	if got, want := term.writes, [][]byte{[]byte("echo α\n")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal paste writes = %#v, want %#v", got, want)
	}
	if next.(Model).terminalPrefixActive {
		t.Fatal("terminal paste must not activate the command prefix")
	}
}

func TestCtrlShiftCloseBracketIsDistinctFromTerminalPrefix(t *testing.T) {
	term := &recordingKeyboardTerminal{}
	m := Model{embeddedTerminalState: embeddedTerminalState{
		embeddedTerminals: []embeddedTerminalSlot{{Number: 1, ID: 1, Terminal: term}},
	}}

	prefixed, cmd, handled := m.handleFocusedEmbeddedTerminalKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	if !handled || cmd != nil || !prefixed.terminalPrefixActive || len(term.writes) != 0 {
		t.Fatalf("ctrl+] handled/cmd/prefix/writes = %v/%T/%v/%#v", handled, cmd, prefixed.terminalPrefixActive, term.writes)
	}

	direct, cmd, handled := m.handleFocusedEmbeddedTerminalKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModShift})
	if !handled || cmd != nil || direct.terminalPrefixActive {
		t.Fatalf("ctrl+shift+] handled/cmd/prefix = %v/%T/%v", handled, cmd, direct.terminalPrefixActive)
	}
	if got, want := term.writes, [][]byte{{0x1d}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ctrl+shift+] writes = %#v, want %#v", got, want)
	}
}

func TestEmbeddedTerminalSpaceEncodingPreservesControlNUL(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want []byte
	}{
		{name: "space", key: tea.KeyPressMsg{Code: tea.KeySpace}, want: []byte{' '}},
		{name: "ctrl+space", key: tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}, want: []byte{0x00}},
		{name: "alt+ctrl+space", key: tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModAlt | tea.ModCtrl}, want: []byte{0x1b, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyBytes(tt.key); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("keyBytes(%s) = %#v, want %#v", tt.name, got, tt.want)
			}
		})
	}
}

func TestEmbeddedTerminalAltPrintableEncodingUsesKeyCode(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want []byte
	}{
		{name: "ascii", key: tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}, want: []byte{0x1b, 'b'}},
		{name: "unicode", key: tea.KeyPressMsg{Code: '☃', Mod: tea.ModAlt}, want: []byte("\x1b☃")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyBytes(tt.key); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("keyBytes(%s) = %#v, want %#v", tt.name, got, tt.want)
			}
		})
	}
}

func TestCtrlEnterDoesNothingAtListFocus(t *testing.T) {
	m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("list ctrl+enter returned command %T", cmd)
	}
	if got := next.(Model); got.modal.IsOpen() || got.searchActive {
		t.Fatal("list ctrl+enter changed modal or search state")
	}
}
