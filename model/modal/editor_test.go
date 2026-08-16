package modal_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/model/modal"
)

func newEditor(initial string, spec modal.EditorSpec, submit func(string) tea.Cmd) modal.Modal {
	return modal.OpenRawMultiLineInput("Edit Plan launch", "prompt template", initial, nil, submit).AsEditor(spec)
}

func TestAsEditorSnapshotsSpecInView(t *testing.T) {
	view := newEditor("alpha\nbeta", modal.EditorSpec{
		Title:    "Plan launch",
		Identity: "agent.plan_prompt   custom",
		Original: "alpha\nbeta",
		Cursor:   3,
		Note:     "Saved",
		NoteKind: modal.NoteSuccess,
	}, nil).View()

	if view.Kind != modal.Input {
		t.Fatalf("kind = %v, want Input", view.Kind)
	}
	if !view.Editor.Enabled {
		t.Fatal("expected Editor.Enabled")
	}
	if view.Editor.Title != "Plan launch" {
		t.Fatalf("title = %q", view.Editor.Title)
	}
	if view.Editor.Identity != "agent.plan_prompt   custom" {
		t.Fatalf("identity = %q", view.Editor.Identity)
	}
	if view.Editor.Note != "Saved" || view.Editor.NoteKind != modal.NoteSuccess {
		t.Fatalf("note = %q/%v", view.Editor.Note, view.Editor.NoteKind)
	}
	if view.InputCursor != 3 {
		t.Fatalf("cursor = %d, want 3", view.InputCursor)
	}
	if view.Editor.Dirty {
		t.Fatal("fresh editor should be clean")
	}
}

func TestAsEditorClampsSpecCursor(t *testing.T) {
	view := newEditor("abc", modal.EditorSpec{Original: "abc", Cursor: 99}, nil).View()
	if view.InputCursor != 3 {
		t.Fatalf("cursor = %d, want clamped to 3", view.InputCursor)
	}

	view = newEditor("abc", modal.EditorSpec{Original: "abc", Cursor: -4}, nil).View()
	if view.InputCursor != 0 {
		t.Fatalf("cursor = %d, want clamped to 0", view.InputCursor)
	}
}

func TestAsEditorOriginalIsIndependentOfInitialValue(t *testing.T) {
	// The reconstructed-after-failure editor opens with a draft that differs
	// from the persisted original and must report itself dirty immediately.
	view := newEditor("draft", modal.EditorSpec{Original: "persisted"}, nil).View()

	if !view.Editor.Dirty {
		t.Fatal("expected editor reopened with a differing draft to be dirty at open")
	}
}

func TestEditorEnterInsertsNewlineAndNeverSubmits(t *testing.T) {
	submitted := false
	m := newEditor("ab", modal.EditorSpec{Original: "ab", Cursor: 1}, func(string) tea.Cmd {
		submitted = true
		return nil
	})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Consumed {
		t.Fatalf("outcome = %v, want Consumed", out)
	}
	if cmd != nil {
		t.Fatal("enter should not produce a submit command in the editor")
	}
	if submitted {
		t.Fatal("enter must not submit in the editor")
	}
	view := next.View()
	if view.Input != "a\nb" {
		t.Fatalf("input = %q, want newline inserted at the cursor", view.Input)
	}
	if view.InputCursor != 2 {
		t.Fatalf("cursor = %d, want 2", view.InputCursor)
	}
	if !view.Editor.Dirty {
		t.Fatal("expected editor dirty after mutation")
	}
}

func TestEditorCtrlSSubmitsRawValue(t *testing.T) {
	var submitted string
	m := newEditor("  raw\n\nvalue  \n", modal.EditorSpec{Original: "  raw\n\nvalue  \n"}, func(input string) tea.Cmd {
		submitted = input
		return nil
	})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	if out != modal.Accepted {
		t.Fatalf("outcome = %v, want Accepted", out)
	}
	if next.IsOpen() {
		t.Fatal("expected modal closed after accept")
	}
	if cmd == nil {
		t.Fatal("expected deferred submit command")
	}
	cmd()
	if submitted != "  raw\n\nvalue  \n" {
		t.Fatalf("submitted = %q, want raw value preserved", submitted)
	}
}

func TestEditorCtrlSValidationFailureKeepsModalOpen(t *testing.T) {
	m := modal.OpenRawMultiLineInput("Edit Plan launch", "prompt template", "bad", func(string) error {
		return errors.New("nope")
	}, func(string) tea.Cmd { return nil }).AsEditor(modal.EditorSpec{Original: "bad"})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	if out != modal.Consumed {
		t.Fatalf("outcome = %v, want Consumed", out)
	}
	if cmd != nil {
		t.Fatal("expected no submit command on validation failure")
	}
	if got := next.View().InputErr; got != "nope" {
		t.Fatalf("input error = %q, want %q", got, "nope")
	}
	if !next.IsOpen() {
		t.Fatal("expected modal to stay open on validation failure")
	}
}

func TestEditorHomeAndEndAreLineAware(t *testing.T) {
	const value = "alpha\nbeta\ngamma"
	for _, key := range []tea.KeyType{tea.KeyHome, tea.KeyCtrlA} {
		m := newEditor(value, modal.EditorSpec{Original: value, Cursor: 8}, nil) // inside "beta"
		next, _, _ := m.Update(tea.KeyMsg{Type: key})
		if got := next.View().InputCursor; got != 6 {
			t.Fatalf("%v cursor = %d, want line start 6", key, got)
		}
	}
	for _, key := range []tea.KeyType{tea.KeyEnd, tea.KeyCtrlE} {
		m := newEditor(value, modal.EditorSpec{Original: value, Cursor: 8}, nil)
		next, _, _ := m.Update(tea.KeyMsg{Type: key})
		if got := next.View().InputCursor; got != 10 {
			t.Fatalf("%v cursor = %d, want line end 10", key, got)
		}
	}
}

func TestEditorAltEnterStillInsertsNewline(t *testing.T) {
	m := newEditor("ab", modal.EditorSpec{Original: "ab", Cursor: 2}, nil)
	next, out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\n'}, Alt: true})
	if out != modal.Consumed {
		t.Fatalf("outcome = %v, want Consumed", out)
	}
	if got := next.View().Input; got != "ab\n" {
		t.Fatalf("input = %q, want alt+enter newline", got)
	}
}

func TestEditorEditingKeysAndUnicodeRemainRuneCorrect(t *testing.T) {
	const value = "héllo wörld"
	m := newEditor(value, modal.EditorSpec{Original: value, Cursor: 11}, nil)

	next, _, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := next.View().InputCursor; got != 10 {
		t.Fatalf("left cursor = %d, want 10", got)
	}

	next, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := next.View().Input; got != "héllo wörl" {
		t.Fatalf("backspace input = %q", got)
	}

	next, _, _ = newEditor(value, modal.EditorSpec{Original: value, Cursor: 1}, nil).Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := next.View().Input; got != "hllo wörld" {
		t.Fatalf("delete input = %q", got)
	}

	next, _, _ = newEditor(value, modal.EditorSpec{Original: value, Cursor: 5}, nil).
		Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("日本\nテキスト")})
	if got := next.View().Input; got != "héllo日本\nテキスト wörld" {
		t.Fatalf("paste input = %q", got)
	}
	if got := next.View().InputCursor; got != 12 {
		t.Fatalf("paste cursor = %d, want 12", got)
	}
}

func TestEditorCtrlUClearsBufferAndRaisesEmptyWarning(t *testing.T) {
	m := newEditor("custom", modal.EditorSpec{Original: "custom", Note: "boom", NoteKind: modal.NoteError}, nil)

	next, _, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})

	view := next.View()
	if view.Input != "" {
		t.Fatalf("input = %q, want cleared", view.Input)
	}
	if !view.Editor.EmptyWarning {
		t.Fatal("expected empty-buffer warning after ctrl+u")
	}
	if !view.Editor.Dirty {
		t.Fatal("expected dirty after ctrl+u")
	}
}

func TestEditorEmptyWarningTracksBufferAndOriginal(t *testing.T) {
	cases := []struct {
		name     string
		initial  string
		original string
		want     bool
	}{
		{"blank buffer over custom original", "", "custom", true},
		{"whitespace buffer over custom original", "  \n\t ", "custom", true},
		{"blank buffer over default original", "", "", false},
		{"non-empty buffer", "x", "custom", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := newEditor(tc.initial, modal.EditorSpec{Original: tc.original}, nil).View()
			if view.Editor.EmptyWarning != tc.want {
				t.Fatalf("EmptyWarning = %v, want %v", view.Editor.EmptyWarning, tc.want)
			}
		})
	}
}

func TestEditorDirtyTracksValueAgainstOriginal(t *testing.T) {
	m := newEditor("abc", modal.EditorSpec{Original: "abc", Cursor: 3}, nil)
	if m.View().Editor.Dirty {
		t.Fatal("expected clean at open")
	}

	m, _, _ = m.Update(keyRunes("d"))
	if !m.View().Editor.Dirty {
		t.Fatal("expected dirty after typing")
	}

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.View().Editor.Dirty {
		t.Fatal("expected clean again once the value matches the original")
	}
}

func TestNonEditorInputIsNeverDirtyOrWarning(t *testing.T) {
	view := modal.OpenRawMultiLineInput("Template", "prompt template", "content", nil, nil).View()
	if view.Editor.Enabled || view.Editor.Dirty || view.Editor.EmptyWarning {
		t.Fatalf("non-editor input leaked editor state: %#v", view.Editor)
	}

	empty := modal.OpenSingleLineInput("New branch", "branch name", "", nil, nil).View()
	if empty.Editor.Dirty || empty.Editor.EmptyWarning {
		t.Fatalf("non-editor empty input leaked editor state: %#v", empty.Editor)
	}
}

func TestEditorNoteIsClearedByOrdinaryTypingButWarningSurvives(t *testing.T) {
	m := newEditor("", modal.EditorSpec{Original: "custom", Note: "permission denied", NoteKind: modal.NoteError}, nil)
	if got := m.View().Editor.Note; got != "permission denied" {
		t.Fatalf("note = %q, want stored", got)
	}
	if !m.View().Editor.EmptyWarning {
		t.Fatal("expected empty warning while buffer is blank")
	}

	next, _, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if got := next.View().Editor.Note; got != "" {
		t.Fatalf("note after space = %q, want cleared", got)
	}
	if !next.View().Editor.EmptyWarning {
		t.Fatal("whitespace-only buffer should keep the derived empty warning")
	}

	next, _, _ = m.Update(keyRunes("x"))
	if got := next.View().Editor.Note; got != "" {
		t.Fatalf("note after rune = %q, want cleared", got)
	}
	if next.View().Editor.EmptyWarning {
		t.Fatal("non-empty buffer should not warn")
	}
}

func TestPlainRawMultiLineInputStillSubmitsOnEnter(t *testing.T) {
	var submitted string
	m := modal.OpenRawMultiLineInput("Template", "prompt template", " raw ", nil, func(input string) tea.Cmd {
		submitted = input
		return nil
	})

	_, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Accepted {
		t.Fatalf("outcome = %v, want Accepted", out)
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	cmd()
	if submitted != " raw " {
		t.Fatalf("submitted = %q", submitted)
	}
}

func TestNonEditorInputIgnoresCtrlSAndKeepsBufferHomeEnd(t *testing.T) {
	m := modal.OpenRawMultiLineInput("Template", "prompt template", "alpha\nbeta", nil, func(string) tea.Cmd {
		return func() tea.Msg { return sentinelMsg("submitted") }
	})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if out != modal.Consumed {
		t.Fatalf("ctrl+s outcome = %v, want Consumed", out)
	}
	if cmd != nil {
		t.Fatal("ctrl+s should not submit a non-editor input")
	}
	if got := next.View().Input; got != "alpha\nbeta" {
		t.Fatalf("ctrl+s mutated the buffer: %q", got)
	}

	next, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if got := next.View().InputCursor; got != 0 {
		t.Fatalf("non-editor home cursor = %d, want buffer start 0", got)
	}
}

func TestWithCancelReceivesPreCancelViewForEachKind(t *testing.T) {
	t.Run("dirty editor cancel", func(t *testing.T) {
		var got modal.View
		m := newEditor("abc", modal.EditorSpec{Title: "Plan launch", Original: "abc", Cursor: 3}, nil).
			WithCancel(func(view modal.View) tea.Cmd {
				got = view
				return func() tea.Msg { return sentinelMsg("cancelled") }
			})
		m, _, _ = m.Update(keyRunes("d"))

		next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if out != modal.Cancelled {
			t.Fatalf("outcome = %v, want Cancelled", out)
		}
		if next.IsOpen() {
			t.Fatal("expected modal closed")
		}
		if cmd == nil {
			t.Fatal("expected cancel command")
		}
		if got.Kind != modal.None {
			t.Fatal("cancel hook should be deferred, not run during Update")
		}
		if msg := cmd(); msg != sentinelMsg("cancelled") {
			t.Fatalf("cancel msg = %#v", msg)
		}
		if !got.Editor.Dirty {
			t.Fatal("cancel hook should observe the dirty editor")
		}
		if got.Editor.Title != "Plan launch" {
			t.Fatalf("cancel hook title = %q", got.Editor.Title)
		}
		if got.Input != "abcd" {
			t.Fatalf("cancel hook input = %q, want the live draft", got.Input)
		}
	})

	t.Run("clean editor cancel", func(t *testing.T) {
		var got modal.View
		m := newEditor("abc", modal.EditorSpec{Original: "abc"}, nil).
			WithCancel(func(view modal.View) tea.Cmd {
				got = view
				return nil
			})
		_, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if out != modal.Cancelled {
			t.Fatalf("outcome = %v, want Cancelled", out)
		}
		if cmd == nil {
			t.Fatal("expected cancel command")
		}
		cmd()
		if !got.Editor.Enabled {
			t.Fatal("cancel hook should observe the editor view")
		}
		if got.Editor.Dirty {
			t.Fatal("clean cancel should report a clean editor")
		}
	})

	t.Run("confirm", func(t *testing.T) {
		for _, key := range []string{"n", "q", "esc"} {
			fired := 0
			m := modal.OpenConfirm("Reset?", nil).WithCancel(func(modal.View) tea.Cmd {
				fired++
				return func() tea.Msg { return sentinelMsg("cancelled") }
			})
			_, out, cmd := m.Update(keyOf(key))
			if out != modal.Cancelled {
				t.Fatalf("%s outcome = %v, want Cancelled", key, out)
			}
			if cmd == nil || fired != 0 {
				t.Fatalf("%s: expected a deferred cancel command", key)
			}
			cmd()
			if fired != 1 {
				t.Fatalf("%s: cancel hook fired %d times", key, fired)
			}
		}
	})

	t.Run("select", func(t *testing.T) {
		for _, key := range []string{"esc", "ctrl+c"} {
			var got modal.View
			m := modal.OpenSelect("Prompt templates", []modal.SelectItem{{Label: "a", Value: "a"}}, 0, nil).
				WithCancel(func(view modal.View) tea.Cmd {
					got = view
					return nil
				})
			_, out, cmd := m.Update(keyOf(key))
			if out != modal.Cancelled {
				t.Fatalf("%s outcome = %v, want Cancelled", key, out)
			}
			if cmd == nil {
				t.Fatalf("%s: expected cancel command", key)
			}
			cmd()
			if got.Kind != modal.Select {
				t.Fatalf("%s: cancel hook saw kind %v", key, got.Kind)
			}
		}
	})

	t.Run("text", func(t *testing.T) {
		for _, key := range []string{"q", "esc"} {
			var got modal.View
			m := modal.OpenText("preview body").WithCancel(func(view modal.View) tea.Cmd {
				got = view
				return nil
			})
			_, out, cmd := m.Update(keyOf(key))
			if out != modal.Cancelled {
				t.Fatalf("%s outcome = %v, want Cancelled", key, out)
			}
			if cmd == nil {
				t.Fatalf("%s: expected cancel command", key)
			}
			cmd()
			if got.Text != "preview body" {
				t.Fatalf("%s: cancel hook text = %q", key, got.Text)
			}
		}
	})
}

func TestCancelWithoutHookReturnsNilCommandForEveryKind(t *testing.T) {
	cases := []struct {
		name  string
		modal modal.Modal
		key   string
	}{
		{"confirm", modal.OpenConfirm("Delete?", nil), "esc"},
		{"input", modal.OpenSingleLineInput("New branch", "branch name", "", nil, nil), "esc"},
		{"multiline input", modal.OpenMultiLineInput("Launch instructions", "instructions", "", nil, nil), "ctrl+c"},
		{"editor", newEditor("a", modal.EditorSpec{Original: "a"}, nil), "esc"},
		{"select", modal.OpenSelect("Choose", []modal.SelectItem{{Value: "a"}}, 0, nil), "esc"},
		{"text", modal.OpenText("body"), "q"},
		{"diff", modal.OpenDiff(modal.DiffStash, "body"), "esc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, cmd := tc.modal.Update(keyOf(tc.key))
			if out != modal.Cancelled {
				t.Fatalf("outcome = %v, want Cancelled", out)
			}
			if cmd != nil {
				t.Fatal("expected nil cmd when no cancel hook is set")
			}
		})
	}
}

func TestDiffCancelIgnoresCancelHook(t *testing.T) {
	fired := false
	m := modal.OpenDiff(modal.DiffStash, "body").WithCancel(func(modal.View) tea.Cmd {
		fired = true
		return func() tea.Msg { return sentinelMsg("cancelled") }
	})

	_, out, cmd := m.Update(keyOf("esc"))
	if out != modal.Cancelled {
		t.Fatalf("outcome = %v, want Cancelled", out)
	}
	if cmd != nil || fired {
		t.Fatal("diff overlays should not use the cancel hook")
	}
}

func TestSetSelectNoteCarriesFeedbackWithoutDisturbingSelection(t *testing.T) {
	items := []modal.SelectItem{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}}
	layout := modal.Layout{Width: 42, Height: 13, Placement: modal.PlacementCenter}
	m := modal.OpenSelectWithLayout("Prompt templates", items, 1, layout, nil).
		SetSelectNote("Saved Plan launch", modal.NoteSuccess)

	view := m.View()
	if view.SelectNote != "Saved Plan launch" || view.SelectNoteKind != modal.NoteSuccess {
		t.Fatalf("select note = %q/%v", view.SelectNote, view.SelectNoteKind)
	}
	if view.SelectIndex != 1 {
		t.Fatalf("select index = %d, want 1 preserved", view.SelectIndex)
	}
	if view.SelectLayout != layout {
		t.Fatalf("layout = %#v, want %#v", view.SelectLayout, layout)
	}
	if len(view.SelectItems) != 2 {
		t.Fatalf("items = %#v", view.SelectItems)
	}

	if got := m.SetSelectNote("", modal.NoteNeutral).View().SelectNote; got != "" {
		t.Fatalf("cleared note = %q", got)
	}
}

func TestSetSelectNoteIsInertForNonSelectModals(t *testing.T) {
	view := modal.OpenSingleLineInput("New branch", "branch name", "", nil, nil).
		SetSelectNote("nope", modal.NoteError).
		View()
	if view.SelectNote != "" {
		t.Fatalf("select note leaked onto an input modal: %q", view.SelectNote)
	}
}

func TestSelectNoteIsClearedByConsumedKeys(t *testing.T) {
	items := []modal.SelectItem{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}}
	for _, key := range []string{"down", "up", "j", "k", "x"} {
		m := modal.OpenSelect("Prompt templates", items, 0, nil).
			SetSelectNote("Saved", modal.NoteSuccess)
		next, out, _ := m.Update(keyOf(key))
		if out != modal.Consumed {
			t.Fatalf("%s outcome = %v, want Consumed", key, out)
		}
		if got := next.View().SelectNote; got != "" {
			t.Fatalf("%s left the note in place: %q", key, got)
		}
	}
}

func TestNonEditorViewsAreUnchangedByTheEditorFields(t *testing.T) {
	confirm := modal.OpenConfirm("Delete?", nil).View()
	if confirm.Editor.Enabled || confirm.SelectNote != "" {
		t.Fatalf("confirm view leaked editor state: %#v", confirm)
	}
	form := modal.OpenForm(modal.FormSpec{Title: "Create", Fields: []modal.FormField{{ID: "a", Label: "A"}}}).View()
	if form.Editor.Enabled || form.SelectNote != "" {
		t.Fatalf("form view leaked editor state: %#v", form)
	}
	if strings.TrimSpace(form.Form.Title) != "Create" {
		t.Fatalf("form title = %q", form.Form.Title)
	}
}

func keyOf(key string) tea.KeyMsg {
	switch key {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return keyRunes(key)
	}
}
