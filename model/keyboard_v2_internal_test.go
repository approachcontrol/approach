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

func TestPastePreservesBracketedPasteForFocusedEmbeddedTerminal(t *testing.T) {
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
	if got, want := term.writes, [][]byte{[]byte("\x1b[200~echo α\n\x1b[201~")}; !reflect.DeepEqual(got, want) {
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
		{name: "shifted letter", key: tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt | tea.ModShift}, want: []byte{0x1b, 'A'}},
		{name: "shifted punctuation", key: tea.KeyPressMsg{Code: '4', Mod: tea.ModAlt | tea.ModShift}, want: []byte{0x1b, '$'}},
		{name: "reported shifted code", key: tea.KeyPressMsg{Code: '1', ShiftedCode: '¡', Mod: tea.ModAlt | tea.ModShift}, want: []byte("\x1b¡")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyBytes(tt.key); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("keyBytes(%s) = %#v, want %#v", tt.name, got, tt.want)
			}
		})
	}
}

func TestEmbeddedTerminalCtrlEncodingMatchesLegacyProtocol(t *testing.T) {
	tests := []struct {
		name string
		code rune
		want byte
	}{
		{name: "space", code: tea.KeySpace, want: 0x00},
		{name: "at", code: '@', want: 0x00},
		{name: "slash", code: '/', want: 0x1f},
		{name: "zero", code: '0', want: '0'},
		{name: "one", code: '1', want: '1'},
		{name: "two", code: '2', want: 0x00},
		{name: "three", code: '3', want: 0x1b},
		{name: "four", code: '4', want: 0x1c},
		{name: "five", code: '5', want: 0x1d},
		{name: "six", code: '6', want: 0x1e},
		{name: "seven", code: '7', want: 0x1f},
		{name: "eight", code: '8', want: 0x7f},
		{name: "nine", code: '9', want: '9'},
		{name: "question", code: '?', want: 0x7f},
		{name: "left bracket", code: '[', want: 0x1b},
		{name: "backslash", code: '\\', want: 0x1c},
		{name: "right bracket", code: ']', want: 0x1d},
		{name: "caret", code: '^', want: 0x1e},
		{name: "underscore", code: '_', want: 0x1f},
		{name: "letter", code: 'a', want: 0x01},
		{name: "approach ctrl-h delete override", code: 'h', want: 0x7f},
		{name: "tilde", code: '~', want: 0x1e},
		{name: "unmapped punctuation", code: ';', want: ';'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := keyBytes(tea.KeyPressMsg{Code: tt.code, Mod: tea.ModCtrl}), []byte{tt.want}; !reflect.DeepEqual(got, want) {
				t.Fatalf("ctrl+%s bytes = %#v, want %#v", tt.name, got, want)
			}
		})
	}

	if got, want := keyBytes(tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModCtrl}), []byte{0x08}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ctrl+backspace bytes = %#v, want %#v", got, want)
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
