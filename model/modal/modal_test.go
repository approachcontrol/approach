package modal_test

import (
	"errors"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/model/modal"
)

type sentinelMsg string

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestConfirmAcceptReturnsActionCommandAndCloses(t *testing.T) {
	calls := 0
	m := modal.OpenConfirm("Delete branch feat? (y/n)", func() tea.Cmd {
		calls++
		return func() tea.Msg { return sentinelMsg("deleted") }
	})

	next, out, cmd := m.Update(keyRunes("y"))

	if out != modal.Accepted {
		t.Fatalf("expected Accepted, got %v", out)
	}
	if next.IsOpen() {
		t.Fatal("expected modal closed after accept")
	}
	if cmd == nil {
		t.Fatal("expected action command")
	}
	if calls != 0 {
		t.Fatalf("expected action factory deferred until command runs, got %d calls", calls)
	}
	if got := cmd(); got != sentinelMsg("deleted") {
		t.Fatalf("expected sentinel message, got %T %[1]v", got)
	}
	if calls != 1 {
		t.Fatalf("expected action factory called once, got %d", calls)
	}

	_, _, cmd = next.Update(keyRunes("y"))
	if cmd != nil {
		t.Fatal("closed modal should not return another action command")
	}
	if calls != 1 {
		t.Fatalf("expected action factory still called once, got %d", calls)
	}
}

func TestConfirmCancelClosesWithoutCommand(t *testing.T) {
	for _, key := range []tea.KeyMsg{keyRunes("n"), keyRunes("q"), {Type: tea.KeyEscape}} {
		m := modal.OpenConfirm("Drop stash? (y/n)", func() tea.Cmd {
			t.Fatal("cancel must not invoke action")
			return nil
		})

		next, out, cmd := m.Update(key)

		if out != modal.Cancelled {
			t.Fatalf("expected Cancelled for %q, got %v", key.String(), out)
		}
		if next.IsOpen() {
			t.Fatalf("expected modal closed for %q", key.String())
		}
		if cmd != nil {
			t.Fatalf("expected nil command for %q, got %T", key.String(), cmd)
		}
	}
}

func TestInputEditsValidatesAndSubmitsTrimmedValue(t *testing.T) {
	var submitted string
	m := modal.OpenInput(
		"New worktree",
		"branch, tag, or new branch name",
		"",
		func(input string) error {
			if input == "" {
				return errors.New("enter a name")
			}
			return nil
		},
		func(input string) tea.Cmd {
			submitted = input
			return func() tea.Msg { return sentinelMsg("created") }
		},
	)

	m, _, _ = m.Update(keyRunes("featx"))
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if got := m.View().Input; got != "feat" {
		t.Fatalf("expected input %q, got %q", "feat", got)
	}

	m, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Accepted {
		t.Fatalf("expected Accepted, got %v", out)
	}
	if m.IsOpen() {
		t.Fatal("expected input modal closed after valid submit")
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if submitted != "" {
		t.Fatalf("expected submit deferred until command runs, got %q", submitted)
	}
	if got := cmd(); got != sentinelMsg("created") {
		t.Fatalf("expected sentinel message, got %T %[1]v", got)
	}
	if submitted != "feat" {
		t.Fatalf("expected trimmed submitted value %q, got %q", "feat", submitted)
	}
}

func TestInputAcceptsSpaceKey(t *testing.T) {
	m := modal.OpenInput(
		"Instructions",
		"task instructions",
		"",
		nil,
		nil,
	)

	m, _, _ = m.Update(keyRunes("Build"))
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m, _, _ = m.Update(keyRunes("flow"))

	if got := m.View().Input; got != "Build flow" {
		t.Fatalf("input = %q, want space preserved", got)
	}
}

func TestInputInvalidSubmitStaysOpenWithError(t *testing.T) {
	m := modal.OpenInput(
		"New worktree",
		"branch, tag, or new branch name",
		"   ",
		func(input string) error {
			if input == "" {
				return errors.New("enter a name")
			}
			return nil
		},
		func(string) tea.Cmd {
			t.Fatal("invalid input must not submit")
			return nil
		},
	)

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Consumed {
		t.Fatalf("expected Consumed, got %v", out)
	}
	if !next.IsOpen() {
		t.Fatal("expected input modal to remain open")
	}
	if got := next.View().InputErr; got != "enter a name" {
		t.Fatalf("expected validation error, got %q", got)
	}
	if cmd != nil {
		t.Fatalf("expected nil command, got %T", cmd)
	}
}

func TestSingleLineInputOpensWithCursorAtEndAndInsertsAtCursor(t *testing.T) {
	m := modal.OpenSingleLineInput(
		"New branch",
		"branch name",
		"feat",
		nil,
		nil,
	)

	view := m.View()
	if view.InputMode != modal.InputSingleLine {
		t.Fatalf("input mode = %v, want single-line", view.InputMode)
	}
	if view.InputCursor != 4 {
		t.Fatalf("cursor = %d, want end of initial input", view.InputCursor)
	}

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _, _ = m.Update(keyRunes("x"))

	view = m.View()
	if view.Input != "fexat" {
		t.Fatalf("input = %q, want middle insertion", view.Input)
	}
	if view.InputCursor != 3 {
		t.Fatalf("cursor = %d, want after inserted rune", view.InputCursor)
	}
}

func TestSingleLineInputMovesCursorWithoutChangingText(t *testing.T) {
	m := modal.OpenSingleLineInput("New branch", "branch name", "abcd", nil, nil)

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyLeft},
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
		{Type: tea.KeyHome},
		{Type: tea.KeyEnd},
		{Type: tea.KeyCtrlA},
		{Type: tea.KeyCtrlE},
	} {
		var out modal.Outcome
		m, out, _ = m.Update(key)
		if out != modal.Consumed {
			t.Fatalf("expected %q consumed, got %v", key.String(), out)
		}
	}

	view := m.View()
	if view.Input != "abcd" {
		t.Fatalf("input = %q, want unchanged", view.Input)
	}
	if view.InputCursor != 4 {
		t.Fatalf("cursor = %d, want end", view.InputCursor)
	}
}

func TestSingleLineInputDeletesAroundCursorAndClearsInput(t *testing.T) {
	m := modal.OpenSingleLineInput("New branch", "branch name", "abcd", nil, nil)

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.View().Input; got != "abcd" {
		t.Fatalf("backspace at start changed input to %q", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := m.View().Input; got != "bcd" {
		t.Fatalf("delete at cursor input = %q, want %q", got, "bcd")
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := m.View().Input; got != "bcd" {
		t.Fatalf("delete at end changed input to %q", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.View().Input; got != "bc" {
		t.Fatalf("backspace input = %q, want %q", got, "bc")
	}

	m = m.SetInputError("bad input")
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	view := m.View()
	if view.Input != "" || view.InputErr != "" || view.InputCursor != 0 {
		t.Fatalf("ctrl+u view = input %q err %q cursor %d, want cleared", view.Input, view.InputErr, view.InputCursor)
	}
}

func TestSingleLineInputSubmitTrimsAndPreservesCursorOnInvalidSubmit(t *testing.T) {
	m := modal.OpenSingleLineInput(
		"New branch",
		"branch name",
		"   ",
		func(input string) error {
			if input == "" {
				return errors.New("enter a branch name")
			}
			return nil
		},
		func(string) tea.Cmd {
			t.Fatal("invalid input must not submit")
			return nil
		},
	)
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Consumed {
		t.Fatalf("expected Consumed, got %v", out)
	}
	if cmd != nil {
		t.Fatalf("expected nil command, got %T", cmd)
	}
	view := next.View()
	if view.InputErr != "enter a branch name" {
		t.Fatalf("input error = %q, want validation error", view.InputErr)
	}
	if view.InputCursor != 2 {
		t.Fatalf("cursor = %d, want preserved", view.InputCursor)
	}

	var submitted string
	next = modal.OpenSingleLineInput("New branch", "branch name", "  feature/x  ", nil, func(input string) tea.Cmd {
		submitted = input
		return nil
	})
	next, out, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out != modal.Accepted {
		t.Fatalf("expected Accepted, got %v", out)
	}
	if next.IsOpen() {
		t.Fatal("expected modal closed after valid submit")
	}
	if cmd == nil {
		t.Fatal("expected deferred submit command")
	}
	cmd()
	if submitted != "feature/x" {
		t.Fatalf("submitted = %q, want trimmed value", submitted)
	}
}

func TestMultiLineInputOpensWithModeAndInsertsNewlineWithAltEnter(t *testing.T) {
	var submitted string
	m := modal.OpenMultiLineInput(
		"Instructions",
		"task instructions",
		"hello",
		nil,
		func(input string) tea.Cmd {
			submitted = input
			return func() tea.Msg { return sentinelMsg("submitted") }
		},
	)

	view := m.View()
	if view.InputMode != modal.InputMultiLine {
		t.Fatalf("input mode = %v, want multi-line", view.InputMode)
	}
	if view.Prompt != "Instructions" || view.Placeholder != "task instructions" || view.Input != "hello" {
		t.Fatalf("unexpected input view: %#v", view)
	}
	if view.InputCursor != 5 {
		t.Fatalf("cursor = %d, want end of initial input", view.InputCursor)
	}

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	var out modal.Outcome
	var cmd tea.Cmd
	m, out, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if out != modal.Consumed {
		t.Fatalf("alt+enter outcome = %v, want Consumed", out)
	}
	if cmd != nil {
		t.Fatalf("alt+enter returned command %T", cmd)
	}
	view = m.View()
	if view.Input != "hel\nlo" {
		t.Fatalf("input = %q, want newline inserted at cursor", view.Input)
	}
	if view.InputCursor != 4 {
		t.Fatalf("cursor = %d, want after inserted newline", view.InputCursor)
	}

	m, out, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out != modal.Accepted {
		t.Fatalf("enter outcome = %v, want Accepted", out)
	}
	if m.IsOpen() {
		t.Fatal("plain enter should submit and close")
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if got := cmd(); got != sentinelMsg("submitted") {
		t.Fatalf("submit command returned %T %[1]v", got)
	}
	if submitted != "hel\nlo" {
		t.Fatalf("submitted = %q, want internal newline preserved", submitted)
	}
}

func TestMultiLineInputMovesVerticallyPreservingPreferredColumn(t *testing.T) {
	m := modal.OpenMultiLineInput("Instructions", "task instructions", "abc\ndefgh\nxy", nil, nil)

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.View().InputCursor; got != 6 {
		t.Fatalf("cursor after up = %d, want same column on previous line", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.View().InputCursor; got != 2 {
		t.Fatalf("cursor after second up = %d, want same column on first line", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.View().InputCursor; got != 6 {
		t.Fatalf("cursor after down = %d, want same preferred column", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.View().InputCursor; got != 12 {
		t.Fatalf("cursor after clamped down = %d, want end of shorter line", got)
	}
}

func TestMultiLineInputLeftRightAndDeletesCrossLineBoundaries(t *testing.T) {
	m := modal.OpenMultiLineInput("Instructions", "task instructions", "ab\ncd", nil, nil)

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	view := m.View()
	if view.Input != "abcd" {
		t.Fatalf("backspace at line start input = %q, want joined lines", view.Input)
	}
	if view.InputCursor != 2 {
		t.Fatalf("cursor after join = %d, want previous line end", view.InputCursor)
	}

	m = modal.OpenMultiLineInput("Instructions", "task instructions", "ab\ncd", nil, nil)
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	view = m.View()
	if view.Input != "abcd" {
		t.Fatalf("delete at line end input = %q, want joined lines", view.Input)
	}
	if view.InputCursor != 2 {
		t.Fatalf("cursor after delete join = %d, want unchanged at join", view.InputCursor)
	}

	m = modal.OpenMultiLineInput("Instructions", "task instructions", "a\nb", nil, nil)
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := m.View().InputCursor; got != 2 {
		t.Fatalf("left across newline cursor = %d, want before second-line rune", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := m.View().InputCursor; got != 3 {
		t.Fatalf("right across newline cursor = %d, want end", got)
	}
}

func TestMultiLineInputSubmitTrimsOuterWhitespaceOnly(t *testing.T) {
	var submitted string
	m := modal.OpenMultiLineInput("Instructions", "task instructions", "  first\n\nsecond  ", nil, func(input string) tea.Cmd {
		submitted = input
		return nil
	})

	_, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Accepted {
		t.Fatalf("outcome = %v, want Accepted", out)
	}
	if cmd == nil {
		t.Fatal("expected deferred submit command")
	}
	cmd()
	if submitted != "first\n\nsecond" {
		t.Fatalf("submitted = %q, want outer whitespace trimmed with internal newlines preserved", submitted)
	}
}

func TestSelectSnapshotsPromptItemsAndInitialSelection(t *testing.T) {
	items := []modal.SelectItem{
		{Label: "Codex", Value: "codex"},
		{Label: "Claude", Value: "claude"},
	}

	view := modal.OpenSelect("Choose agent", items, 1, nil).View()

	if view.Kind != modal.Select {
		t.Fatalf("expected Select kind, got %v", view.Kind)
	}
	if view.Prompt != "Choose agent" {
		t.Fatalf("expected prompt snapshot, got %q", view.Prompt)
	}
	if !reflect.DeepEqual(view.SelectItems, items) {
		t.Fatalf("expected select items %#v, got %#v", items, view.SelectItems)
	}
	if view.SelectIndex != 1 {
		t.Fatalf("expected selected index 1, got %d", view.SelectIndex)
	}
}

func TestSelectDefaultsToAutoCenteredLayout(t *testing.T) {
	view := modal.OpenSelect("Choose agent", []modal.SelectItem{
		{Label: "codex", Value: "codex"},
	}, 0, nil).View()

	want := modal.Layout{Placement: modal.PlacementCenter}
	if view.SelectLayout != want {
		t.Fatalf("select layout = %#v, want %#v", view.SelectLayout, want)
	}
}

func TestSelectCarriesExplicitLayoutWithoutChangingSelectionBehavior(t *testing.T) {
	items := []modal.SelectItem{
		{Label: "Codex", Value: "codex"},
		{Label: "Claude", Value: "claude"},
	}
	layout := modal.Layout{Width: 32, Height: 6, Placement: modal.PlacementBottomCenter}
	var submitted string
	m := modal.OpenSelectWithLayout("Choose agent", items, 99, layout, func(value string) tea.Cmd {
		submitted = value
		return func() tea.Msg { return sentinelMsg("saved") }
	})
	items[0] = modal.SelectItem{Label: "mutated", Value: "mutated"}

	view := m.View()
	if view.SelectLayout != layout {
		t.Fatalf("select layout = %#v, want %#v", view.SelectLayout, layout)
	}
	if view.SelectIndex != 0 {
		t.Fatalf("out-of-range selected index = %d, want clamped 0", view.SelectIndex)
	}
	if got := view.SelectItems[0]; got.Label != "Codex" || got.Value != "codex" {
		t.Fatalf("select items were not copied before caller mutation: %#v", view.SelectItems)
	}

	m, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if out != modal.Consumed || cmd != nil {
		t.Fatalf("down outcome=%v cmd=%T, want consumed nil cmd", out, cmd)
	}
	view = m.View()
	if view.SelectIndex != 1 {
		t.Fatalf("down selected index = %d, want 1", view.SelectIndex)
	}
	if view.SelectLayout != layout {
		t.Fatalf("layout changed after movement: %#v", view.SelectLayout)
	}

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out != modal.Accepted {
		t.Fatalf("enter outcome = %v, want Accepted", out)
	}
	if next.IsOpen() {
		t.Fatal("expected modal closed after enter")
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if got := cmd(); got != sentinelMsg("saved") {
		t.Fatalf("submit command returned %T %[1]v", got)
	}
	if submitted != "claude" {
		t.Fatalf("submitted = %q, want claude", submitted)
	}
}

func TestSelectMovesWithWrapping(t *testing.T) {
	m := modal.OpenSelect("Choose agent", []modal.SelectItem{
		{Label: "codex", Value: "codex"},
		{Label: "claude", Value: "claude"},
	}, 0, nil)

	m, out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if out != modal.Consumed {
		t.Fatalf("expected Consumed for down, got %v", out)
	}
	if got := m.View().SelectIndex; got != 1 {
		t.Fatalf("down selected index = %d, want 1", got)
	}

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.View().SelectIndex; got != 0 {
		t.Fatalf("down should wrap to 0, got %d", got)
	}

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.View().SelectIndex; got != 1 {
		t.Fatalf("up should wrap to 1, got %d", got)
	}
}

func TestSelectMovesWithJKAliases(t *testing.T) {
	m := modal.OpenSelect("Choose agent", []modal.SelectItem{
		{Label: "codex", Value: "codex"},
		{Label: "claude", Value: "claude"},
	}, 0, nil)

	m, out, _ := m.Update(keyRunes("j"))
	if out != modal.Consumed {
		t.Fatalf("expected Consumed for j, got %v", out)
	}
	if got := m.View().SelectIndex; got != 1 {
		t.Fatalf("j selected index = %d, want 1", got)
	}

	m, out, _ = m.Update(keyRunes("k"))
	if out != modal.Consumed {
		t.Fatalf("expected Consumed for k, got %v", out)
	}
	if got := m.View().SelectIndex; got != 0 {
		t.Fatalf("k selected index = %d, want 0", got)
	}
}

func TestSelectEnterSubmitsSelectedValueAndCloses(t *testing.T) {
	var submitted string
	m := modal.OpenSelect("Choose agent", []modal.SelectItem{
		{Label: "codex", Value: "codex"},
		{Label: "claude", Value: "claude"},
	}, 1, func(value string) tea.Cmd {
		submitted = value
		return func() tea.Msg { return sentinelMsg("saved") }
	})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if out != modal.Accepted {
		t.Fatalf("expected Accepted, got %v", out)
	}
	if next.IsOpen() {
		t.Fatal("expected select modal closed after enter")
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if submitted != "" {
		t.Fatalf("expected submit deferred until command runs, got %q", submitted)
	}
	if got := cmd(); got != sentinelMsg("saved") {
		t.Fatalf("expected sentinel message, got %T %[1]v", got)
	}
	if submitted != "claude" {
		t.Fatalf("expected submitted value %q, got %q", "claude", submitted)
	}
}

func TestSelectCancelClosesWithoutSubmit(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEscape}, {Type: tea.KeyCtrlC}} {
		m := modal.OpenSelect("Choose agent", []modal.SelectItem{
			{Label: "codex", Value: "codex"},
		}, 0, func(string) tea.Cmd {
			t.Fatal("cancel must not invoke submit")
			return nil
		})

		next, out, cmd := m.Update(key)

		if out != modal.Cancelled {
			t.Fatalf("expected Cancelled for %q, got %v", key.String(), out)
		}
		if next.IsOpen() {
			t.Fatalf("expected modal closed for %q", key.String())
		}
		if cmd != nil {
			t.Fatalf("expected nil command for %q, got %T", key.String(), cmd)
		}
	}
}

func TestFormSnapshotsPurposeAndDefaultFieldState(t *testing.T) {
	m := modal.OpenForm(modal.FormSpec{
		Purpose: "repo-create",
		Title:   "New repo",
		Fields: []modal.FormField{
			{ID: "name", Kind: modal.FormText, Label: "Repo name", Placeholder: "repo-name"},
			{ID: "github", Kind: modal.FormCheckbox, Label: "Create GitHub repo", Checked: true},
			{ID: "visibility", Kind: modal.FormChoice, Label: "Visibility", Options: []modal.SelectItem{
				{Label: "Public", Value: "public"},
				{Label: "Private", Value: "private"},
			}},
		},
	})

	view := m.View()
	if view.Kind != modal.Form {
		t.Fatalf("kind = %v, want Form", view.Kind)
	}
	if view.Form.Purpose != "repo-create" || view.Form.Title != "New repo" {
		t.Fatalf("unexpected form identity: %#v", view.Form)
	}
	if view.Form.FocusIndex != 0 {
		t.Fatalf("focus index = %d, want 0", view.Form.FocusIndex)
	}
	if len(view.Form.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %#v", view.Form.Fields)
	}
	if view.Form.Fields[0].Value != "" || view.Form.Fields[0].Placeholder != "repo-name" {
		t.Fatalf("unexpected text field: %#v", view.Form.Fields[0])
	}
	if !view.Form.Fields[1].Checked {
		t.Fatalf("GitHub checkbox should default checked: %#v", view.Form.Fields[1])
	}
	if view.Form.Fields[2].SelectedIndex != 0 || view.Form.Fields[2].Options[0].Value != "public" {
		t.Fatalf("visibility should default public: %#v", view.Form.Fields[2])
	}
}

func TestFormEditsNavigatesTogglesAndSubmitsStructuredValues(t *testing.T) {
	var submitted modal.FormValues
	m := modal.OpenForm(modal.FormSpec{
		Purpose: "repo-create",
		Title:   "New repo",
		Fields: []modal.FormField{
			{ID: "name", Kind: modal.FormText, Label: "Repo name"},
			{ID: "github", Kind: modal.FormCheckbox, Label: "Create GitHub repo", Checked: true},
			{ID: "visibility", Kind: modal.FormChoice, Label: "Visibility", Options: []modal.SelectItem{
				{Label: "Public", Value: "public"},
				{Label: "Private", Value: "private"},
			}},
		},
		Submit: func(values modal.FormValues) tea.Cmd {
			submitted = values
			return func() tea.Msg { return sentinelMsg("created") }
		},
	})

	m, _, _ = m.Update(keyRunes("  project  "))
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if view := m.View().Form; view.FocusIndex != 1 {
		t.Fatalf("tab focus = %d, want checkbox", view.FocusIndex)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if view := m.View().Form; view.FocusIndex != 2 {
		t.Fatalf("down focus = %d, want visibility", view.FocusIndex)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out != modal.Accepted {
		t.Fatalf("enter outcome = %v, want Accepted", out)
	}
	if next.IsOpen() {
		t.Fatal("expected form closed after valid submit")
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if submitted.Text != nil {
		t.Fatalf("submit should be deferred until command runs, got %#v", submitted)
	}
	if got := cmd(); got != sentinelMsg("created") {
		t.Fatalf("submit command returned %T %[1]v", got)
	}
	if submitted.Text["name"] != "project" {
		t.Fatalf("submitted name = %q, want trimmed project", submitted.Text["name"])
	}
	if submitted.Checked["github"] {
		t.Fatalf("submitted github = true, want false")
	}
	if submitted.Choice["visibility"] != "private" {
		t.Fatalf("submitted visibility = %q, want private", submitted.Choice["visibility"])
	}
}

func TestFormShiftTabNavigatesBackwards(t *testing.T) {
	m := modal.OpenForm(modal.FormSpec{
		Purpose: "repo-create",
		Title:   "New repo",
		Fields: []modal.FormField{
			{ID: "name", Kind: modal.FormText, Label: "Repo name"},
			{ID: "github", Kind: modal.FormCheckbox, Label: "Create GitHub repo"},
			{ID: "visibility", Kind: modal.FormChoice, Label: "Visibility", Options: []modal.SelectItem{
				{Label: "Public", Value: "public"},
				{Label: "Private", Value: "private"},
			}},
		},
	})

	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := m.View().Form.FocusIndex; got != 2 {
		t.Fatalf("shift+tab focus = %d, want wrap to last field", got)
	}
	m, _, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := m.View().Form.FocusIndex; got != 1 {
		t.Fatalf("shift+tab focus = %d, want checkbox", got)
	}
}

func TestFormInvalidSubmitStaysOpenWithError(t *testing.T) {
	m := modal.OpenForm(modal.FormSpec{
		Purpose: "repo-create",
		Title:   "New repo",
		Fields: []modal.FormField{
			{ID: "name", Kind: modal.FormText, Label: "Repo name"},
			{ID: "github", Kind: modal.FormCheckbox, Label: "Create GitHub repo", Checked: true},
		},
		Validate: func(values modal.FormValues) error {
			if values.Text["name"] == "" {
				return errors.New("enter a repo name")
			}
			return nil
		},
		Submit: func(modal.FormValues) tea.Cmd {
			t.Fatal("invalid form must not submit")
			return nil
		},
	})

	next, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if out != modal.Consumed {
		t.Fatalf("enter outcome = %v, want Consumed", out)
	}
	if !next.IsOpen() {
		t.Fatal("expected form to stay open")
	}
	if got := next.View().Form.Error; got != "enter a repo name" {
		t.Fatalf("form error = %q, want validation error", got)
	}
	if cmd != nil {
		t.Fatalf("expected nil command, got %T", cmd)
	}
}

func TestDiffScrollsClampsAndCloses(t *testing.T) {
	m := modal.OpenDiff(modal.DiffWorktree, "line 1\nline 2")

	m, out, _ := m.Update(keyRunes("j"))
	if out != modal.Consumed {
		t.Fatalf("expected Consumed for scroll, got %v", out)
	}
	if got := m.View().Scroll; got != 1 {
		t.Fatalf("expected scroll 1, got %d", got)
	}

	m, _, _ = m.Update(keyRunes("j"))
	if got := m.View().Scroll; got != 1 {
		t.Fatalf("expected scroll clamped at max line index 1, got %d", got)
	}

	m, _, _ = m.Update(keyRunes("k"))
	m, _, _ = m.Update(keyRunes("k"))
	if got := m.View().Scroll; got != 0 {
		t.Fatalf("expected scroll clamped at 0, got %d", got)
	}

	m, out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if out != modal.Cancelled {
		t.Fatalf("expected Cancelled, got %v", out)
	}
	if m.IsOpen() {
		t.Fatal("expected diff modal closed")
	}
	if cmd != nil {
		t.Fatalf("expected nil command, got %T", cmd)
	}
}

func TestDiffSetForRequestIgnoresWrongKind(t *testing.T) {
	m := modal.OpenDiff(modal.DiffCommit, "").WithRequest(7)
	m = m.SetDiffForRequest(modal.DiffStash, 7, "stale stash diff")

	if got := m.View().Diff; got != "" {
		t.Fatalf("expected wrong-kind diff ignored, got %q", got)
	}

	m = m.SetDiffForRequest(modal.DiffCommit, 7, "commit diff")
	if got := m.View().Diff; got != "commit diff" {
		t.Fatalf("expected matching diff stored, got %q", got)
	}
}

func TestDiffSetForRequestIgnoresWrongRequest(t *testing.T) {
	m := modal.OpenDiff(modal.DiffWorktree, "").WithRequest(7)

	m = m.SetDiffForRequest(modal.DiffWorktree, 0, "missing request diff")
	if got := m.View().Diff; got != "" {
		t.Fatalf("expected zero-request diff ignored, got %q", got)
	}

	m = m.SetDiffForRequest(modal.DiffWorktree, 6, "stale diff")
	if got := m.View().Diff; got != "" {
		t.Fatalf("expected wrong-request diff ignored, got %q", got)
	}

	m = m.SetDiffForRequest(modal.DiffWorktree, 7, "current diff")
	if got := m.View().Diff; got != "current diff" {
		t.Fatalf("expected matching request diff stored, got %q", got)
	}
}

func TestWeakDiffSettersAreNotPublicAPI(t *testing.T) {
	modalType := reflect.TypeOf(modal.Modal{})
	for _, name := range []string{"SetDiff", "SetDiffFor"} {
		if _, ok := modalType.MethodByName(name); ok {
			t.Fatalf("%s should not be exported; use SetDiffForRequest for async diff results", name)
		}
	}
}

func TestViewSnapshotsKindSpecificState(t *testing.T) {
	force := modal.OpenForce("Force delete feat? (y/n)", func() tea.Cmd { return nil }).View()
	if force.Kind != modal.Confirm || !force.Force || force.Prompt == "" {
		t.Fatalf("unexpected force confirm view: %#v", force)
	}

	input := modal.OpenInput("New worktree", "branch, tag, or new branch name", "feat", nil, func(string) tea.Cmd { return nil }).View()
	if input.Kind != modal.Input || input.Input != "feat" {
		t.Fatalf("unexpected input view: %#v", input)
	}
	if input.Placeholder != "branch, tag, or new branch name" {
		t.Fatalf("unexpected input placeholder: %q", input.Placeholder)
	}

	diff := modal.OpenDiff(modal.DiffReflog, "body").View()
	if diff.Kind != modal.Diff || diff.DiffKind != modal.DiffReflog || diff.Diff != "body" {
		t.Fatalf("unexpected diff view: %#v", diff)
	}
}
