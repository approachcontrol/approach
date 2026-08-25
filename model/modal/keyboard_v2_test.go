package modal_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/model/modal"
)

func modifiedEnter(mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: mod}
}

func TestMultilineInputDistinguishesShiftEnterFromEnter(t *testing.T) {
	var submitted string
	m := modal.OpenMultiLineInput("Prompt", "", "ab", nil, func(value string) tea.Cmd {
		submitted = value
		return nil
	})

	next, outcome, cmd := m.Update(modifiedEnter(tea.ModShift))
	if outcome != modal.Consumed || cmd != nil || submitted != "" {
		t.Fatalf("shift+enter outcome/cmd/submitted = %v/%T/%q", outcome, cmd, submitted)
	}
	if got := next.View().Input; got != "ab\n" {
		t.Fatalf("shift+enter input = %q, want newline", got)
	}

	_, outcome, cmd = next.Update(modifiedEnter(0))
	if outcome != modal.Accepted || cmd == nil {
		t.Fatalf("plain enter outcome/cmd = %v/%T, want accepted command", outcome, cmd)
	}
	cmd()
	if submitted != "ab" {
		t.Fatalf("plain enter submitted = %q, want trimmed multiline value", submitted)
	}
}

func TestEditorCtrlEnterSavesWithoutInsertingNewline(t *testing.T) {
	var submitted string
	m := newEditor("draft", modal.EditorSpec{Original: "old", Cursor: 5}, func(value string) tea.Cmd {
		submitted = value
		return nil
	})

	next, outcome, cmd := m.Update(modifiedEnter(tea.ModCtrl))
	if outcome != modal.Accepted || next.IsOpen() || cmd == nil {
		t.Fatalf("ctrl+enter outcome/open/cmd = %v/%v/%T", outcome, next.IsOpen(), cmd)
	}
	cmd()
	if submitted != "draft" {
		t.Fatalf("ctrl+enter submitted = %q, want draft", submitted)
	}
}

func TestMultilineFormUsesShiftEnterForNewlineAndCtrlEnterForSubmit(t *testing.T) {
	var submitted modal.FormValues
	m := modal.OpenForm(modal.FormSpec{
		Fields: []modal.FormField{{ID: "body", Kind: modal.FormMultilineText, Value: "line", Cursor: 4}},
		Submit: func(values modal.FormValues) tea.Cmd {
			submitted = values
			return nil
		},
	})

	next, outcome, cmd := m.Update(modifiedEnter(tea.ModShift))
	if outcome != modal.Consumed || cmd != nil || next.View().Form.Fields[0].Value != "line\n" {
		t.Fatalf("shift+enter did not insert a multiline form newline: %#v", next.View().Form.Fields[0])
	}

	_, outcome, cmd = next.Update(modifiedEnter(tea.ModCtrl))
	if outcome != modal.Accepted || cmd == nil {
		t.Fatalf("ctrl+enter outcome/cmd = %v/%T, want accepted command", outcome, cmd)
	}
	cmd()
	if got := submitted.Text["body"]; got != "line" {
		t.Fatalf("ctrl+enter submitted body = %q, want trimmed line", got)
	}
}

func TestCtrlEnterDoesNotSubmitSingleLineForm(t *testing.T) {
	submitted := false
	m := modal.OpenForm(modal.FormSpec{
		Fields: []modal.FormField{{ID: "name", Kind: modal.FormText, Value: "value"}},
		Submit: func(modal.FormValues) tea.Cmd {
			submitted = true
			return nil
		},
	})

	next, outcome, cmd := m.Update(modifiedEnter(tea.ModCtrl))
	if outcome != modal.Consumed || cmd != nil || !next.IsOpen() || submitted {
		t.Fatalf("single-line ctrl+enter outcome/cmd/open/submitted = %v/%T/%v/%v", outcome, cmd, next.IsOpen(), submitted)
	}
}

func TestPasteInsertsAtModalCursor(t *testing.T) {
	m := modal.OpenMultiLineInput("Prompt", "", "ab", nil, nil)
	m, _, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	next, outcome, cmd := m.Update(tea.PasteMsg{Content: "X\nY"})
	if outcome != modal.Consumed || cmd != nil {
		t.Fatalf("paste outcome/cmd = %v/%T", outcome, cmd)
	}
	if got := next.View().Input; got != "aX\nYb" {
		t.Fatalf("pasted input = %q, want cursor insertion", got)
	}
}
