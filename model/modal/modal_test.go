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
