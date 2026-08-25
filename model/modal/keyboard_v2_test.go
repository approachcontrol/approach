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

func TestCtrlSpaceDoesNotMutateModalValues(t *testing.T) {
	ctrlSpace := tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}

	t.Run("input", func(t *testing.T) {
		m := modal.OpenInput("Prompt", "", "ab", nil, nil)
		next, _, _ := m.Update(ctrlSpace)
		if got := next.View().Input; got != "ab" {
			t.Fatalf("ctrl+space input = %q, want unchanged", got)
		}
	})

	tests := []struct {
		name  string
		field modal.FormField
		check func(t *testing.T, field modal.FormField)
	}{
		{
			name:  "text",
			field: modal.FormField{ID: "text", Kind: modal.FormText, Value: "ab", Cursor: 2},
			check: func(t *testing.T, field modal.FormField) {
				if field.Value != "ab" {
					t.Fatalf("ctrl+space text = %q, want unchanged", field.Value)
				}
			},
		},
		{
			name:  "checkbox",
			field: modal.FormField{ID: "check", Kind: modal.FormCheckbox},
			check: func(t *testing.T, field modal.FormField) {
				if field.Checked {
					t.Fatal("ctrl+space toggled checkbox")
				}
			},
		},
		{
			name: "choice",
			field: modal.FormField{ID: "choice", Kind: modal.FormChoice, Options: []modal.SelectItem{
				{Label: "First", Value: "first"},
				{Label: "Second", Value: "second"},
			}},
			check: func(t *testing.T, field modal.FormField) {
				if field.SelectedIndex != 0 {
					t.Fatalf("ctrl+space choice index = %d, want 0", field.SelectedIndex)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := modal.OpenForm(modal.FormSpec{Fields: []modal.FormField{tt.field}})
			next, _, _ := m.Update(ctrlSpace)
			tt.check(t, next.View().Form.Fields[0])
		})
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
