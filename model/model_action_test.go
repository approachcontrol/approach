package model_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/embeddedterm"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// --- Worktree diff (enter key in ModeWorktrees) ---

func recordPageText(paged *[]string) func(string) (actions.TerminalLaunchSpec, error) {
	return func(body string) (actions.TerminalLaunchSpec, error) {
		*paged = append(*paged, body)
		return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
	}
}

func TestModel_EnterOnDirtyWorktreeFetchesDiffWithoutOverlay(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			t.Fatalf("PageText should not run until the diff result arrives, got %q", body)
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true, FilesChanged: 3},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktreeDiff cmd, got nil")
	}
}

func TestModel_EnterOnCleanWorktreeIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone for clean worktree, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Error("expected nil cmd for clean worktree")
	}
}

func TestModel_EnterOnStaleWorktreeIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-gone", BranchName: "gone", Stale: true, Dirty: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone for stale worktree, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Error("expected nil cmd for stale worktree")
	}
}

func TestModel_EnterOnLockedDirtyWorktreeFetchesDiffWithoutOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Locked: true, Dirty: true, FilesChanged: 2},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected no overlay for locked dirty worktree, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktreeDiff cmd for locked dirty worktree")
	}
}

func TestModel_EnterOnEmptyWorktreeListIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone with no worktrees, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Error("expected nil cmd with no worktrees")
	}
}

func TestModel_MoveWorktreeOpensInputForMovableLinkedWorktree(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/feat", BranchName: "feat"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd != nil {
		t.Fatal("expected opening move input to return no command")
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.ConfirmPrompt() != ui.WorktreeMovePrompt {
		t.Fatalf("expected move prompt %q, got %q", ui.WorktreeMovePrompt, m.ConfirmPrompt())
	}
	if !strings.Contains(m.View(), ui.WorktreeMoveInputPlaceholder) {
		t.Fatalf("expected move placeholder in view, got:\n%s", m.View())
	}
	if m.WorktreeInput() != "" {
		t.Fatalf("expected empty initial move input, got %q", m.WorktreeInput())
	}
	if got := m.InputMode(); got != modal.InputSingleLine {
		t.Fatalf("move input mode = %v, want single-line", got)
	}
}

func TestModel_MoveWorktreeInputNoOpsWhenUnavailable(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(model.Model) model.Model
		wantOverlay ui.OverlayState
	}{
		{
			name: "main worktree",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
					{Path: "/dev/alpha", BranchName: "main", IsMain: true},
				}})
				return m
			},
		},
		{
			name: "stale worktree",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
					{Path: "/dev/alpha-worktrees/feat", BranchName: "feat", Stale: true},
				}})
				return m
			},
		},
		{
			name: "locked worktree",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
					{Path: "/dev/alpha-worktrees/feat", BranchName: "feat", Locked: true},
				}})
				return m
			},
		},
		{
			name: "dirty worktree is movable",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
					{Path: "/dev/alpha-worktrees/feat", BranchName: "feat", Dirty: true},
				}})
				return m
			},
			wantOverlay: ui.OverlayWorktreeInput,
		},
		{
			name: "empty list",
			setup: func(m model.Model) model.Model {
				return inRightPane(m)
			},
		},
		{
			name: "left pane",
			setup: func(m model.Model) model.Model {
				m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
					{Path: "/dev/alpha-worktrees/feat", BranchName: "feat"},
				}})
				return m
			},
		},
		{
			name: "non-worktrees mode",
			setup: func(m model.Model) model.Model {
				return inBranchesMode(m)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.setup(model.New(testRepos()))
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
			if cmd != nil {
				t.Fatal("expected no command")
			}
			if m.Overlay() != tt.wantOverlay {
				t.Fatalf("expected overlay %d, got %d", tt.wantOverlay, m.Overlay())
			}
		})
	}
}

func TestModel_MoveWorktreeSubmitReturnsCommandAndFailureReopensInput(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	oldPath := "/dev/alpha-worktrees/feat"
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: oldPath, BranchName: "feat"},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat-renamed")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected move overlay to close on submit, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected move command")
	}
	msg, ok := cmd().(model.WorktreeMoveFailedMsg)
	if !ok {
		t.Fatalf("expected WorktreeMoveFailedMsg, got %T", msg)
	}
	if msg.RepoPath != "/dev/alpha" || msg.OldPath != oldPath || msg.Input != "feat-renamed" || msg.Err == "" {
		t.Fatalf("unexpected failure message: %+v", msg)
	}

	m, _ = update(m, msg)
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected move input to reopen, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "feat-renamed" {
		t.Fatalf("expected original input preserved, got %q", m.WorktreeInput())
	}
	if m.WorktreeInputErr() == "" {
		t.Fatal("expected move error to be shown")
	}
}

func TestModel_StaleWorktreeMoveFailureIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	m, _ = update(m, model.WorktreeMoveFailedMsg{
		RepoPath: "/dev/alpha",
		OldPath:  "/dev/alpha-worktrees/feat",
		Input:    "feat-renamed",
		Err:      "boom",
	})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected stale move failure to be ignored, got overlay %d", m.Overlay())
	}
}

func TestModel_WorktreeMovedRefreshesAndSelectsMovedPath(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/feat", BranchName: "feat"},
	}})

	m, cmd := update(m, model.WorktreeMovedMsg{
		RepoPath: "/dev/alpha",
		OldPath:  "/dev/alpha-worktrees/feat",
		NewPath:  "/dev/alpha-worktrees/feat-renamed",
	})
	if cmd == nil {
		t.Fatal("expected worktree refresh command after move")
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/feat-renamed", BranchName: "feat"},
	}})
	if m.WorktreeSelected() != 1 {
		t.Fatalf("expected moved worktree selected, got index %d", m.WorktreeSelected())
	}
}

func TestModel_StaleWorktreeMovedIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	m, cmd := update(m, model.WorktreeMovedMsg{
		RepoPath: "/dev/alpha",
		OldPath:  "/dev/alpha-worktrees/feat",
		NewPath:  "/dev/alpha-worktrees/feat-renamed",
	})
	if cmd != nil {
		t.Fatal("expected stale move success to return no command")
	}
	if m.WorktreeSelected() != 0 {
		t.Fatalf("expected selection unchanged, got %d", m.WorktreeSelected())
	}
}

func TestModel_WorktreeMovePendingSelectionClampsWhenPathMissing(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/feat", BranchName: "feat"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, _ = update(m, model.WorktreeMovedMsg{
		RepoPath: "/dev/alpha",
		OldPath:  "/dev/alpha-worktrees/feat",
		NewPath:  "/dev/alpha-worktrees/feat-renamed",
	})
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	if m.WorktreeSelected() != 0 {
		t.Fatalf("expected selection to clamp when moved path is missing, got %d", m.WorktreeSelected())
	}

	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/feat-renamed", BranchName: "feat"},
	}})
	if m.WorktreeSelected() != 0 {
		t.Fatalf("expected missing-path pending selection to be cleared, got %d", m.WorktreeSelected())
	}
}

func TestModel_WorktreeDiffResultPagesDiff(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "diff --git a/f.txt",
	})
	if cmd == nil {
		t.Fatal("expected pager launch command")
	}
	if len(paged) != 1 || paged[0] != "diff --git a/f.txt" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_WorktreeDiffFetchFailureCarriesIdentity(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/does-not-exist", BranchName: "main", Dirty: true},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected diff fetch command")
	}
	msg, ok := cmd().(model.FetchErrorMsg)
	if !ok {
		t.Fatalf("expected FetchErrorMsg, got %T", msg)
	}
	if msg.Kind != model.FetchWorktreeDiff {
		t.Fatalf("expected FetchWorktreeDiff kind, got %d", msg.Kind)
	}
	if msg.DiffRequest != 1 {
		t.Fatalf("expected diff request 1, got %d", msg.DiffRequest)
	}
	if msg.WorktreePath != "/dev/does-not-exist" {
		t.Fatalf("expected worktree identity, got %q", msg.WorktreePath)
	}
}

func TestModel_MatchingWorktreeDiffFetchFailureShowsStatusWithoutPaging(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "failed to load diff: boom",
		Kind:         model.FetchWorktreeDiff,
		Mode:         ui.ModeWorktrees,
		DiffRequest:  1,
		WorktreePath: "/dev/alpha",
	})

	if len(paged) != 0 {
		t.Fatalf("fetch failure should not page text, got %#v", paged)
	}
	if !strings.Contains(m.View(), "failed to load diff: boom") {
		t.Fatal("expected matching diff fetch failure in status bar")
	}
}

func TestModel_StaleWorktreeDiffFetchFailureIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})  // request 1
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape}) // close overlay
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})  // request 2

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "old diff failure",
		Kind:         model.FetchWorktreeDiff,
		Mode:         ui.ModeWorktrees,
		DiffRequest:  1,
		WorktreePath: "/dev/alpha",
	})

	if strings.Contains(m.View(), "old diff failure") {
		t.Fatal("expected stale same-target diff failure to be ignored")
	}
}

func TestModel_StaleWorktreeDiffResultDiscarded(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = selectBravo(m)
	_, cmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "stale",
	})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected stale worktree diff discarded, cmd=%T paged=%#v", cmd, paged)
	}
}

func TestModel_WorktreeDiffResultDiscardedIfWorktreePathChanged(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
		{Path: "/dev/alpha-feat", BranchName: "feat", Dirty: true},
	}
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "wrong worktree",
	})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected wrong-target diff discarded, cmd=%T paged=%#v", cmd, paged)
	}
}

func TestModel_WorktreeDiffResultAfterEscapeStillPages(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})

	_, cmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "worktree diff",
	})

	if cmd == nil {
		t.Fatal("expected pager launch command")
	}
	if len(paged) != 1 || paged[0] != "worktree diff" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_WorktreeDiffResultFromOlderRequestIgnoredAfterReopen(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(body string) (actions.TerminalLaunchSpec, error) {
			paged = append(paged, body)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})  // request 1
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape}) // close before result arrives
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})  // request 2 for same target

	m, firstCmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  2,
		Diff:         "new diff",
	})
	m, staleCmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "stale old diff",
	})

	if firstCmd == nil {
		t.Fatal("expected latest request to launch pager")
	}
	if staleCmd != nil {
		t.Fatalf("expected stale request to return nil command, got %T", staleCmd)
	}
	if len(paged) != 1 || paged[0] != "new diff" {
		t.Fatalf("expected stale request ignored after reopen, paged=%#v", paged)
	}
}

func TestModel_ViewResultAfterModeSwitchIgnored(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})

	_, cmd := update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "old worktree diff",
	})

	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected mode switch to invalidate old view result, cmd=%T paged=%#v", cmd, paged)
	}
}

// --- Worktree terminal/code actions ---

func TestModel_TKey_Worktree_FiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for t key on worktree")
	}
}

func TestModel_CKey_Worktree_FiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for c key on worktree")
	}
}

func TestModel_TKey_LockedWorktree_FiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Locked: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for t key on locked worktree")
	}
}

func TestModel_CKey_LockedWorktree_FiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Locked: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for c key on locked worktree")
	}
}

func TestModel_TKey_StaleWorktree_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-gone", BranchName: "gone", Stale: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd != nil {
		t.Error("expected nil cmd for t key on stale worktree")
	}
}

func TestModel_CKey_StaleWorktree_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-gone", BranchName: "gone", Stale: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Error("expected nil cmd for c key on stale worktree")
	}
}

func TestModel_TAndCKeys_LockedStaleWorktree_NoCmd(t *testing.T) {
	for _, key := range []rune{'t', 'c'} {
		m := model.New(testRepos())
		m = inRightPane(m)
		wts := []gitquery.Worktree{
			{Path: "/dev/alpha-gone", BranchName: "gone", Locked: true, Stale: true},
		}
		m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil {
			t.Errorf("expected nil cmd for %q key on locked stale worktree", key)
		}
	}
}

func TestModel_TKey_EmptyWorktrees_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd != nil {
		t.Error("expected nil cmd for t key with no worktrees")
	}
}

func TestModel_CKey_EmptyWorktrees_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Error("expected nil cmd for c key with no worktrees")
	}
}

// --- Fetch/pull actions ---

func TestModel_FKey_Worktree_FiresFetchCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for f key on worktree")
	}
	msg := cmd()
	if _, ok := msg.(model.GitFetchFailedMsg); !ok {
		t.Fatalf("expected GitFetchFailedMsg for nonexistent test path, got %T", msg)
	}
}

func TestModel_FKey_BareRepoWithoutWorktree_FiresFetchCmd(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inRightPane(m)

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for fetch on bare repo without worktrees")
	}
	msg := cmd()
	if _, ok := msg.(model.GitFetchFailedMsg); !ok {
		t.Fatalf("expected GitFetchFailedMsg for nonexistent bare repo path, got %T", msg)
	}
}

func TestModel_FKey_BareRepoBranchesWithoutSelection_FiresFetchCmd(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inBranchesMode(m)

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for branch-pane fetch on bare repo without rows")
	}
	msg := cmd()
	if _, ok := msg.(model.GitFetchFailedMsg); !ok {
		t.Fatalf("expected GitFetchFailedMsg for nonexistent bare repo path, got %T", msg)
	}
}

func TestModel_LeftPaneFKeyFetchesFilteredReposOnly(t *testing.T) {
	var fetched []string
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(path string) error {
			fetched = append(fetched, path)
			return nil
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bravo")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected batch fetch command")
	}
	if !strings.Contains(m.View(), "Fetching 0/1 visible repo...") {
		t.Fatalf("expected initial batch fetch status, got:\n%s", m.View())
	}

	msgs := runBatchCmd(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("expected one fetch result, got %d", len(msgs))
	}
	if len(fetched) != 1 || fetched[0] != "/dev/bravo" {
		t.Fatalf("expected only filtered repo to be fetched, got %v", fetched)
	}
	result, ok := msgs[0].(model.VisibleRepoFetchResultMsg)
	if !ok {
		t.Fatalf("expected VisibleRepoFetchResultMsg, got %T", msgs[0])
	}
	if result.RepoPath != "/dev/bravo" || result.DisplayName != "bravo" || result.Err != "" {
		t.Fatalf("unexpected fetch result: %#v", result)
	}
}

func TestModel_LeftPaneFKeyWithNoVisibleReposShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("missing")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected status expiry command when no repos are visible")
	}
	if !strings.Contains(m.View(), "No visible repos to fetch") {
		t.Fatalf("expected no-visible-repos status, got:\n%s", m.View())
	}
}

func TestModel_LeftPaneFKeyDuringVisibleRepoFetchDoesNotStartAnotherBatch(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, firstCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if firstCmd == nil {
		t.Fatal("expected first batch fetch command")
	}
	m, secondCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if secondCmd != nil {
		t.Fatal("expected no command when batch fetch is already in progress")
	}
	if !strings.Contains(m.View(), "Fetching 0/3 visible repos...") {
		t.Fatalf("expected original batch progress to remain visible, got:\n%s", m.View())
	}
}

func TestModel_VisibleRepoFetchProgressSurvivesOrdinaryKeypress(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	msgs := runBatchCmd(t, cmd)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if !strings.Contains(m.View(), "Fetching 0/3 visible repos...") {
		t.Fatalf("active progress should survive ordinary keypress, got:\n%s", m.View())
	}
	m, _ = update(m, msgs[0])
	if !strings.Contains(m.View(), "Fetching 1/3 visible repos...") {
		t.Fatalf("active progress should continue after result, got:\n%s", m.View())
	}
}

func TestModel_VisibleRepoFetchProgressSuccessAndRefresh(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	msgs := runBatchCmd(t, cmd)
	for i, msg := range msgs {
		m, cmd = update(m, msg)
		if i < len(msgs)-1 {
			want := fmt.Sprintf("Fetching %d/3 visible repos...", i+1)
			if !strings.Contains(m.View(), want) {
				t.Fatalf("expected progress %q, got:\n%s", want, m.View())
			}
			if cmd != nil {
				t.Fatal("did not expect refresh before batch completion")
			}
		}
	}
	if !strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("expected final success status, got:\n%s", m.View())
	}
	if cmd == nil {
		t.Fatal("expected one selected-repo refresh when batch completes")
	}
	requireBatchCommandCount(t, cmd, 4)
}

func TestModel_VisibleRepoFetchFinalStatusExpires(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	for _, msg := range runBatchCmd(t, cmd) {
		m, _ = update(m, msg)
	}
	if !strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("expected final success status before expiry, got:\n%s", m.View())
	}

	m, _ = update(m, model.StatusExpiredMsg{Seq: 1})
	if strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("expected final success status to expire, got:\n%s", m.View())
	}
}

func TestModel_VisibleRepoFetchFinalStatusFadesBeforeExpiry(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	for _, msg := range runBatchCmd(t, cmd) {
		m, _ = update(m, msg)
	}
	if m.TransientErrorFadeStep() != 0 {
		t.Fatalf("expected fresh status to start unfaded, got step %d", m.TransientErrorFadeStep())
	}

	m, _ = update(m, model.StatusFadeMsg{Seq: 1, Step: 1})
	if m.TransientErrorFadeStep() != 1 {
		t.Fatalf("expected fade step 1, got %d", m.TransientErrorFadeStep())
	}
	if !strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("fade should keep status visible, got:\n%s", m.View())
	}

	m, _ = update(m, model.StatusFadeMsg{Seq: 1, Step: 2})
	if m.TransientErrorFadeStep() != 2 {
		t.Fatalf("expected fade step 2, got %d", m.TransientErrorFadeStep())
	}
}

func TestModel_VisibleRepoFetchFinalStatusStillClearsOnKeypress(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	for _, msg := range runBatchCmd(t, cmd) {
		m, _ = update(m, msg)
	}
	m, _ = update(m, model.StatusFadeMsg{Seq: 1, Step: 1})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("expected keypress to clear faded status immediately, got:\n%s", m.View())
	}
}

func TestModel_VisibleRepoFetchStatusExpiryDoesNotClearNewerStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	for _, msg := range runBatchCmd(t, cmd) {
		m, _ = update(m, msg)
	}
	m, _ = update(m, model.GitFetchFailedMsg{RepoPath: "/dev/alpha", Err: "fetch failed: newer"})

	m, _ = update(m, model.StatusExpiredMsg{Seq: 1})
	view := m.View()
	if !strings.Contains(view, "fetch failed: newer") {
		t.Fatalf("expiry should not clear a newer git status, got:\n%s", view)
	}
}

func TestModel_VisibleRepoFetchPartialFailureSummaryIsCapped(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/dev/alpha", DisplayName: "alpha"},
		{Path: "/dev/bravo", DisplayName: "bravo"},
		{Path: "/dev/charlie", DisplayName: "charlie"},
		{Path: "/dev/delta", DisplayName: "delta"},
		{Path: "/dev/echo", DisplayName: "echo"},
	}
	m := model.NewWithOptions(repos, model.Options{
		FetchRepo: func(path string) error {
			if path == "/dev/alpha" {
				return nil
			}
			return errors.New("nope")
		},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	for _, msg := range runBatchCmd(t, cmd) {
		m, cmd = update(m, msg)
	}
	view := m.View()
	if !strings.Contains(view, "Fetched 1/5 visible repos; failed: bravo, charlie, delta +1 more") {
		t.Fatalf("expected capped failure summary, got:\n%s", view)
	}
	if cmd == nil {
		t.Fatal("expected refresh for current selected repo after completion")
	}
}

func TestModel_VisibleRepoFetchStaleResultIgnoredByRequest(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m, cmd := update(m, model.VisibleRepoFetchResultMsg{Request: 999, RepoPath: "/dev/alpha"})
	if cmd != nil {
		t.Fatal("stale batch result should not trigger refresh")
	}
	if !strings.Contains(m.View(), "Fetching 0/3 visible repos...") {
		t.Fatalf("stale result should not advance batch progress, got:\n%s", m.View())
	}
}

func TestModel_VisibleRepoFetchRefreshesOnlyIfCurrentSelectionWasCaptured(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bravo")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	msgs := runBatchCmd(t, cmd)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, _ = update(m, msgs[0])
	if !strings.Contains(m.View(), "Fetched 1 visible repo") {
		t.Fatalf("expected final success status, got:\n%s", m.View())
	}
}

func TestModel_VisibleRepoFetchRefreshesChangedSelectionInsideCapturedBatch(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(string) error { return nil },
	})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	msgs := runBatchCmd(t, cmd)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	for _, msg := range msgs {
		m, cmd = update(m, msg)
	}
	if cmd == nil {
		t.Fatal("expected refresh when changed selection is part of captured batch")
	}
	if !strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("expected final success status, got:\n%s", m.View())
	}
}

func TestModel_RightPaneFetchUsesInjectedFetchRepo(t *testing.T) {
	var fetched []string
	m := model.NewWithOptions(testRepos(), model.Options{
		FetchRepo: func(path string) error {
			fetched = append(fetched, path)
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected fetch command")
	}
	if msg := cmd(); msg != (model.GitFetchedMsg{RepoPath: "/dev/alpha"}) {
		t.Fatalf("expected GitFetchedMsg, got %#v", msg)
	}
	if len(fetched) != 1 || fetched[0] != "/dev/alpha" {
		t.Fatalf("expected injected fetch for selected worktree path, got %v", fetched)
	}
}

func TestModel_ShiftFKey_Worktree_FiresPullCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for F key on worktree")
	}
	msg := cmd()
	if _, ok := msg.(model.GitPullFailedMsg); !ok {
		t.Fatalf("expected GitPullFailedMsg for nonexistent test path, got %T", msg)
	}
}

func TestModel_FAndShiftFKeys_StaleWorktree_NoCmd(t *testing.T) {
	for _, key := range []rune{'f', 'F'} {
		m := model.New(testRepos())
		m = inRightPane(m)
		m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
			{Path: "/dev/alpha-gone", BranchName: "gone", Stale: true},
		}})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil {
			t.Errorf("expected nil cmd for %q key on stale worktree", key)
		}
	}
}

func TestModel_ShiftFKey_NonWorktreeBranch_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "feature"},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd for pull on non-worktree branch, got %T", cmd)
	}
}

func TestModel_ShiftFKey_BareRepoWithoutWorktree_NoCmd(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inRightPane(m)

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd for pull on bare repo without selected worktree, got %T", cmd)
	}
}

func TestModel_FAndShiftFKeys_NonWorktreeAndBranchModes_NoCmd(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  rune
		mode ui.Mode
	}{
		{name: "fetch stashes", key: 'f', mode: ui.ModeStashes},
		{name: "pull stashes", key: 'F', mode: ui.ModeStashes},
		{name: "fetch history", key: 'f', mode: ui.ModeHistory},
		{name: "pull history", key: 'F', mode: ui.ModeHistory},
		{name: "fetch reflog", key: 'f', mode: ui.ModeReflog},
		{name: "pull reflog", key: 'F', mode: ui.ModeReflog},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model.New(testRepos())
			m = inRightPane(m)
			switch tc.mode {
			case ui.ModeStashes:
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			case ui.ModeHistory:
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			case ui.ModeReflog:
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			}

			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			if cmd != nil {
				t.Fatalf("expected nil cmd for %q in mode %d, got %T", tc.key, tc.mode, cmd)
			}
		})
	}
}

func TestModel_BareRepoCheckedOutBranchDeleteNoCmd(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: repo.Path, Branches: []gitquery.Branch{
		{
			Name:          "feature",
			IsWorktree:    true,
			WorktreePaths: []string{"/dev/project-worktrees/feature"},
		},
	}})
	m = enableDestructive(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no delete confirm for checked-out bare repo branch, got overlay %d", m.Overlay())
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd for checked-out bare repo branch delete, got %T", cmd)
	}
}

func TestModel_RootBranchDeleteAllowsCleanedRepoPath_NoCmd(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project/", DisplayName: "project"}
	m := model.New([]scanner.Repo{repo})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: repo.Path, Branches: []gitquery.Branch{
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/project"}},
	}})
	m = enableDestructive(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no delete confirm for cleaned root branch path, got overlay %d", m.Overlay())
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd for cleaned root branch delete, got %T", cmd)
	}
}

func TestModel_CKey_BareRepoHistory_NoCmd(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd for opening code from bare repo history, got %T", cmd)
	}
}

func TestModel_GitFetchedRefetchesCurrentMode(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)

	_, cmd := update(m, model.GitFetchedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected refetch cmd after fetch success")
	}
}

func TestModel_GitPulledRefetchesCurrentMode(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)

	_, cmd := update(m, model.GitPulledMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected refetch cmd after pull success")
	}
}

func TestModel_StaleGitFetchedMsgIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)

	_, cmd := update(m, model.GitFetchedMsg{RepoPath: "/dev/alpha"})
	if cmd != nil {
		t.Fatal("expected stale fetch result to be ignored")
	}
}

func TestModel_StaleGitPullFailedMsgIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	m, _ = update(m, model.GitPullFailedMsg{RepoPath: "/dev/alpha", Err: "pull failed"})

	if strings.Contains(m.View(), "pull failed") {
		t.Fatal("expected stale pull failure to be ignored")
	}
}

func runBatchCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	msgs := make([]tea.Msg, 0, len(batch))
	for _, subcmd := range batch {
		msgs = append(msgs, subcmd())
	}
	return msgs
}

func runLeadingBatchCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	if len(batch) == 0 {
		return nil
	}
	return runBatchCmd(t, batch[0])
}

func requireBatchCommandCount(t *testing.T, cmd tea.Cmd, wantAtLeast int) {
	t.Helper()
	requireBatchMsg(t, cmd, wantAtLeast)
}

func runLeadingBatchCmdWithCount(t *testing.T, cmd tea.Cmd, wantAtLeast int) []tea.Msg {
	t.Helper()
	batch := requireBatchMsg(t, cmd, wantAtLeast)
	return runBatchCmd(t, batch[0])
}

func requireBatchMsg(t *testing.T, cmd tea.Cmd, wantAtLeast int) tea.BatchMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("command returned %T, want batch", msg)
	}
	if len(batch) < wantAtLeast {
		t.Fatalf("batch has %d command(s), want at least %d", len(batch), wantAtLeast)
	}
	return batch
}

// --- Branch diff (enter key) ---

func TestModel_EnterStillRequiresDirtyWorktree(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "clean-1"},
		{Name: "dirty-root", IsWorktree: true, Dirty: true, WorktreePaths: []string{"/dev/alpha"}},
		{Name: "clean-2"},
	}
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	// Root branch (dirty-root) is pinned to index 0: enter opens diff
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on dirty root branch should open diff")
	}
	// The diff payload (branch name, contents) is verified against a real repo
	// in TestModel_BranchDiffPayloadAgainstRealRepo.

	// Navigate to clean-1 (index 1): enter is no-op
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter on clean-1 should be no-op")
	}

	// Navigate to clean-2 (index 2): enter is no-op
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter on clean-2 should be no-op")
	}
}

func TestModel_EnterFetchesBranchDiffWithoutOverlayForDirtyWorktree(t *testing.T) {
	m := model.New(testRepos())
	branches := []gitquery.Branch{
		{
			Name:          "feat",
			IsWorktree:    true,
			Dirty:         true,
			WorktreePaths: []string{"/dev/alpha"},
		},
		{Name: "main"},
	}
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchBranchDiff cmd, got nil")
	}
}

func TestModel_BranchDiffResultForWrongWorktreePathIgnored(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{
				Name:          "feat",
				IsWorktree:    true,
				Dirty:         true,
				WorktreePaths: []string{"/dev/alpha"},
			},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	_, cmd := update(m, model.BranchDiffResultMsg{
		RepoPath:    "/dev/alpha",
		BranchName:  "feat",
		DiffRequest: 1,
		Diff:        "missing path diff",
	})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected missing worktree path diff ignored, cmd=%T paged=%#v", cmd, paged)
	}

	_, cmd = update(m, model.BranchDiffResultMsg{
		RepoPath:     "/dev/alpha",
		BranchName:   "feat",
		WorktreePath: "/dev/elsewhere",
		DiffRequest:  1,
		Diff:         "wrong path diff",
	})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected wrong worktree path diff ignored, cmd=%T paged=%#v", cmd, paged)
	}

	_, cmd = update(m, model.BranchDiffResultMsg{
		RepoPath:     "/dev/alpha",
		BranchName:   "feat",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "matching path diff",
	})
	if cmd == nil {
		t.Fatal("expected matching branch diff to launch pager")
	}
	if len(paged) != 1 || paged[0] != "matching path diff" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_BranchDiffFetchFailureMatchesBranchAndWorktreePath(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{{
			Name:          "feat",
			IsWorktree:    true,
			Dirty:         true,
			WorktreePaths: []string{"/dev/alpha"},
		}},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "branch diff failed",
		Kind:        model.FetchBranchDiff,
		Mode:        ui.ModeBranches,
		DiffRequest: 1,
		BranchName:  "feat",
	})
	if strings.Contains(m.View(), "branch diff failed") {
		t.Fatal("missing worktree path branch diff failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "branch diff failed",
		Kind:         model.FetchBranchDiff,
		Mode:         ui.ModeBranches,
		DiffRequest:  1,
		BranchName:   "feat",
		WorktreePath: "/dev/elsewhere",
	})
	if strings.Contains(m.View(), "branch diff failed") {
		t.Fatal("wrong worktree path branch diff failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "branch diff failed",
		Kind:         model.FetchBranchDiff,
		Mode:         ui.ModeBranches,
		DiffRequest:  1,
		BranchName:   "feat",
		WorktreePath: "/dev/alpha",
	})
	if !strings.Contains(m.View(), "branch diff failed") {
		t.Fatal("matching branch diff failure should show in status bar")
	}
}

func TestModel_EnterDoesNothingForCleanBranch(t *testing.T) {
	m := model.New(testRepos())
	branches := []gitquery.Branch{
		{
			Name:  "feat",
			Dirty: false,
		},
	}
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Fatalf("expected no command for clean branch, got %T", cmd)
	}
}

// --- History (mode 3) actions ---

func modelInHistoryWithCommits() model.Model {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	return m
}

func TestModel_EnterInHistoryFetchesCommitDiffWithoutOverlay(t *testing.T) {
	m := modelInHistoryWithCommits()
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchCommitDiff cmd, got nil")
	}
}

func TestModel_EnterInHistoryNoCommitsIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	// No commits loaded
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}

func TestModel_CommitDiffResultStoresDiff(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := update(m, model.CommitDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: "diff --git a/f.txt"})
	if cmd == nil {
		t.Fatal("expected commit diff to launch pager")
	}
	if len(paged) != 1 || paged[0] != "diff --git a/f.txt" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_StaleCommitDiffResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	m, _ = update(m, model.CommitDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "stale"})
}

func TestModel_CommitDiffFetchFailureMatchesHashAndRequest(t *testing.T) {
	m := modelInHistoryWithCommits()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "commit diff failed",
		Kind:        model.FetchCommitDiff,
		Mode:        ui.ModeHistory,
		DiffRequest: 1,
		Hash:        "wrong",
	})
	if strings.Contains(m.View(), "commit diff failed") {
		t.Fatal("wrong-hash commit diff failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "commit diff failed",
		Kind:        model.FetchCommitDiff,
		Mode:        ui.ModeHistory,
		DiffRequest: 99,
		Hash:        "abc1234",
	})
	if strings.Contains(m.View(), "commit diff failed") {
		t.Fatal("wrong-request commit diff failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "commit diff failed",
		Kind:        model.FetchCommitDiff,
		Mode:        ui.ModeHistory,
		DiffRequest: 1,
		Hash:        "abc1234",
	})
	if !strings.Contains(m.View(), "commit diff failed") {
		t.Fatal("matching commit diff failure should show in status bar")
	}
}

func TestModel_YKeyCopiesHashInHistoryMode(t *testing.T) {
	var copied []string
	m := model.NewWithOptions(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for y key in mode 3")
	}
	m, _ = update(m, cmd())
	if len(copied) != 1 || copied[0] != "abc1234" {
		t.Fatalf("copied = %#v, want selected commit hash", copied)
	}
}

func TestModel_YKeyNoOpInWorktreesMode(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Errorf("expected nil cmd for y key in mode 1, got %T", cmd)
	}
}

func TestModel_YKeyNoOpWithNoCommits(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Errorf("expected nil cmd for y key with no commits, got %T", cmd)
	}
}

func TestModel_ClipboardResultShowsError(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = update(m, model.ClipboardResultMsg{Err: "no supported clipboard command installed; install wl-copy, xclip, or xsel"})

	view := m.View()
	if !strings.Contains(view, "no supported clipboard command installed") {
		t.Fatalf("expected clipboard error in view, got:\n%s", view)
	}
}

func TestModel_TerminalResultShowsError(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = update(m, model.TerminalResultMsg{Err: "TERMINAL is set to \"ghostterm\", but that command was not found"})

	view := m.View()
	if !strings.Contains(view, "ghostterm") {
		t.Fatalf("expected terminal error in view, got:\n%s", view)
	}
}

func TestModel_DKeyNoOpInHistoryMode(t *testing.T) {
	m := modelInHistoryWithCommits()
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone in history mode, got %d", m.Overlay())
	}
}

func TestModel_TKeyInHistoryFiresCmd(t *testing.T) {
	m := modelInHistoryWithCommits()
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for t key in history mode")
	}
}

func TestModel_CKeyInHistoryFiresCmd(t *testing.T) {
	m := modelInHistoryWithCommits()
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for c key in history mode")
	}
}

// --- Stash view ---

func TestModel_EnterOnStashFetchesDiffWithoutOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchStashDiff cmd, got nil")
	}
}

func TestModel_StashDiffResultStoresDiff(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       0,
		DiffRequest: 1,
		Diff:        "missing identity diff",
	})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected missing stash identity diff ignored, cmd=%T paged=%#v", cmd, paged)
	}
	stash := testStashes()[0]
	_, cmd = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       stash.Index,
		Date:        stash.Date,
		Message:     stash.Message,
		DiffRequest: 1,
		Diff:        "diff --git a/f.txt",
	})
	if cmd == nil {
		t.Fatal("expected matching stash diff to launch pager")
	}
	if len(paged) != 1 || paged[0] != "diff --git a/f.txt" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_StaleStashDiffDoesNotLaunchAfterModeChange(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, DiffRequest: 1, Diff: "stale stash diff"})

	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected stale stash diff ignored after mode change, cmd=%T paged=%#v", cmd, paged)
	}
}

func TestModel_StaleStashDiffForOldIndexIgnored(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	_, cmd := update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, DiffRequest: 1, Diff: "old index diff"})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected old-index stash diff ignored, cmd=%T paged=%#v", cmd, paged)
	}

	currentStash := testStashes()[1]
	_, cmd = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       currentStash.Index,
		Date:        currentStash.Date,
		Message:     currentStash.Message,
		DiffRequest: 2,
		Diff:        "current index diff",
	})
	if cmd == nil {
		t.Fatal("expected current-index stash diff to launch pager")
	}
	if len(paged) != 1 || paged[0] != "current index diff" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_StaleStashDiffForChangedIdentityIgnored(t *testing.T) {
	oldStash := gitquery.Stash{Index: 0, Date: "2026-03-18 10:00:00 -0700", Message: "old stash"}
	newStash := gitquery.Stash{Index: 0, Date: "2026-03-19 10:00:00 -0700", Message: "new stash"}

	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{oldStash}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{newStash}})

	_, cmd := update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       oldStash.Index,
		Date:        oldStash.Date,
		Message:     oldStash.Message,
		DiffRequest: 1,
		Diff:        "old stash diff",
	})
	if cmd != nil || len(paged) != 0 {
		t.Fatalf("expected changed stash identity to reject stale diff, cmd=%T paged=%#v", cmd, paged)
	}

	_, cmd = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       newStash.Index,
		Date:        newStash.Date,
		Message:     newStash.Message,
		DiffRequest: 1,
		Diff:        "new stash diff",
	})
	if cmd == nil {
		t.Fatal("expected matching stash identity diff to launch pager")
	}
	if len(paged) != 1 || paged[0] != "new stash diff" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_StashDiffFetchFailureMatchesFullIdentity(t *testing.T) {
	oldStash := gitquery.Stash{Index: 0, Date: "2026-03-18 10:00:00 -0700", Message: "old stash"}
	newStash := gitquery.Stash{Index: 0, Date: "2026-03-19 10:00:00 -0700", Message: "new stash"}

	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{oldStash}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{newStash}})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "missing stash identity failed",
		Kind:        model.FetchStashDiff,
		Mode:        ui.ModeStashes,
		DiffRequest: 1,
		StashIndex:  newStash.Index,
	})
	if strings.Contains(m.View(), "missing stash identity failed") {
		t.Fatal("missing stash date/message failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "old stash diff failed",
		Kind:         model.FetchStashDiff,
		Mode:         ui.ModeStashes,
		DiffRequest:  1,
		StashIndex:   oldStash.Index,
		StashDate:    oldStash.Date,
		StashMessage: oldStash.Message,
	})
	if strings.Contains(m.View(), "old stash diff failed") {
		t.Fatal("stale stash identity failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "new stash diff failed",
		Kind:         model.FetchStashDiff,
		Mode:         ui.ModeStashes,
		DiffRequest:  1,
		StashIndex:   newStash.Index,
		StashDate:    newStash.Date,
		StashMessage: newStash.Message,
	})
	if !strings.Contains(m.View(), "new stash diff failed") {
		t.Fatal("matching stash identity failure should show in status bar")
	}
}

// --- Destructive mode ---

func TestModel_DKeyNoOpInReadOnlyMode(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{{Name: "feat"}},
	})
	// d should be no-op in read-only mode (default)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone in read-only mode, got %d", m.Overlay())
	}
}

func TestModel_ShiftDTogglesDestructiveOn(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	if m.Destructive() {
		t.Fatal("expected destructive=false initially")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !m.Destructive() {
		t.Error("expected destructive=true after Shift+D")
	}
}

func TestModel_DKeyWorksInDestructiveMode(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{{Name: "feat"}},
	})
	// Enable destructive mode
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	// Now d should work
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("expected OverlayConfirm in destructive mode, got %d", m.Overlay())
	}
}

func TestModel_DKeyNoOpInReadOnlyModeStashes(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone for stash drop in read-only mode, got %d", m.Overlay())
	}
}

func TestModel_ShiftDTogglesDestructiveOff(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if m.Destructive() {
		t.Error("expected destructive=false after second Shift+D")
	}
}

func TestModel_ShiftDWorksFromLeftPane(t *testing.T) {
	m := model.New(testRepos())
	// Left pane is active by default
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !m.Destructive() {
		t.Error("expected destructive=true from left pane")
	}
}

func TestModel_DestructivePersistsAcrossRepoSwitch(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	// Switch to left pane and navigate to a different repo
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if !m.Destructive() {
		t.Error("expected destructive to persist after repo switch")
	}
}

func TestModel_ShiftDNoOpDuringConfirmOverlay(t *testing.T) {
	m := modelWithDeletableBranch()
	// Open confirm dialog
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatal("expected OverlayConfirm")
	}
	// Shift+D should be ignored while confirm is active
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !m.Destructive() {
		t.Error("expected destructive to remain true during confirm overlay")
	}
}

func TestModel_WorktreeRemovedDetachedSkipsBranchConfirm(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/detached", Detached: true},
	}})
	// Send WorktreeRemovedMsg with empty BranchName (detached)
	m, cmd := update(m, model.WorktreeRemovedMsg{RepoPath: "/dev/alpha", BranchName: ""})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("detached removal should not show branch confirm, got overlay %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd after detached removal, got nil")
	}
}

func TestModel_WorktreeRemovedShowsBranchConfirm(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}})
	// Send WorktreeRemovedMsg with branch name
	m, cmd := update(m, model.WorktreeRemovedMsg{RepoPath: "/dev/alpha", BranchName: "feat"})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("non-detached removal should show branch confirm, got overlay %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "feat") {
		t.Errorf("branch confirm prompt should contain branch name, got %q", m.ConfirmPrompt())
	}
	if !strings.Contains(m.ConfirmPrompt(), "Also delete branch") {
		t.Errorf("branch confirm prompt should contain 'Also delete branch', got %q", m.ConfirmPrompt())
	}
	// Should also return a fetchWorktrees cmd (background refresh)
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd alongside branch confirm, got nil")
	}
}

func TestModel_CombinedCleanupConfirmYReturnsCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}})
	// Trigger branch confirm
	m, _ = update(m, model.WorktreeRemovedMsg{RepoPath: "/dev/alpha", BranchName: "feat"})
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected OverlayConfirm, got %d", m.Overlay())
	}
	// Confirm branch deletion
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed after confirm, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected branch delete cmd after confirm, got nil")
	}
	// Fake path causes DeleteBranch to fail → DeleteFailedMsg
	msg := cmd()
	if _, ok := msg.(model.DeleteFailedMsg); !ok {
		t.Errorf("expected DeleteFailedMsg from branch delete on fake path, got %T", msg)
	}
}

func TestModel_CombinedCleanupConfirmNClosesDialog(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}})
	m, _ = update(m, model.WorktreeRemovedMsg{RepoPath: "/dev/alpha", BranchName: "feat"})
	// Decline branch deletion
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed after cancel, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd after cancel, got %T", cmd)
	}
}

func TestModel_CombinedCleanupForceDeleteFailureSurfacesError(t *testing.T) {
	// Full chain on a fake path: worktree removed → "Also delete branch?"
	// confirmed → DeleteBranch fails → "Force delete?" shown → force confirmed →
	// the force delete also fails, which must surface as ForceDeleteFailedMsg
	// rather than a false success. The success path (force delete succeeds and
	// the threaded WorktreeDeleteCompletedMsg is returned) is covered against a
	// real repo by TestModel_CombinedCleanupForceDeleteSucceedsAgainstRealRepo.
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}})
	// Worktree removed → branch confirm dialog
	m, _ = update(m, model.WorktreeRemovedMsg{RepoPath: "/dev/alpha", BranchName: "feat"})
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected branch confirm overlay, got %d", m.Overlay())
	}
	// Confirm branch deletion → DeleteBranch fails on fake path → DeleteFailedMsg
	_, branchDeleteCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if branchDeleteCmd == nil {
		t.Fatal("expected branch delete cmd, got nil")
	}
	deleteFailedMsg := branchDeleteCmd()
	if _, ok := deleteFailedMsg.(model.DeleteFailedMsg); !ok {
		t.Fatalf("expected DeleteFailedMsg from fake-path branch delete, got %T", deleteFailedMsg)
	}
	// Process DeleteFailedMsg → force confirm shown
	m, _ = update(m, deleteFailedMsg)
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected force confirm overlay, got %d", m.Overlay())
	}
	if !m.ConfirmForce() {
		t.Fatal("expected ConfirmForce=true for force confirm")
	}
	// Confirm force-delete
	_, forceCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if forceCmd == nil {
		t.Fatal("expected force cmd, got nil")
	}
	if _, ok := forceCmd().(model.ForceDeleteFailedMsg); !ok {
		t.Fatalf("expected ForceDeleteFailedMsg from fake-path force delete, got %T", forceCmd())
	}
}

// --- Worktree prune ---

func TestModel_PKeyRequiresDestructiveMode(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	// destructive NOT enabled
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale", Stale: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("p without destructive mode should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_PKeyNoOpOnNonStaleWorktree(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("p on non-stale worktree should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_PKeyOnStaleWorktreeShowsConfirm(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale-branch", Stale: true},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("p on stale worktree should open confirm, got overlay %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "Prune") {
		t.Errorf("confirm prompt should mention Prune, got %q", m.ConfirmPrompt())
	}
}

func TestModel_PKeyNoOpOnLockedStaleWorktree(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "offline", Locked: true, Stale: true},
	})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("p on locked stale worktree should be no-op, got overlay %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("p on locked stale worktree should not return a cmd, got %T", cmd)
	}
}

func TestModel_PKeyNoOpInBranchesMode(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("p in branches mode should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_WorktreePrunedRefetchesWorktrees(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/gone", BranchName: "stale", Stale: true},
	}})
	_, cmd := update(m, model.WorktreePrunedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd after prune, got nil")
	}
}

// --- Worktree t/c actions ---

func TestModel_TKeyInWorktreesModeFiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for t key in worktrees mode")
	}
}

func TestModel_CKeyInWorktreesModeFiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for c key in worktrees mode")
	}
}

func TestModel_TKeyOnStaleWorktreeIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale", Stale: true},
	}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd != nil {
		t.Error("expected nil cmd for t key on stale worktree")
	}
}

func TestModel_WorktreeDeleteCompletedMsgIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m, cmd := update(m, model.WorktreeDeleteCompletedMsg{RepoPath: "/dev/alpha"})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}

// --- Worktree creation ---

func TestModel_NKeyOpensWorktreeInput(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "" {
		t.Errorf("expected empty worktree input, got %q", m.WorktreeInput())
	}
	if got := m.InputMode(); got != modal.InputSingleLine {
		t.Errorf("worktree input mode = %v, want single-line", got)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd opening input, got %T", cmd)
	}
}

func TestModel_PKeyOpensPullRequestWorktreeInput(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.ConfirmPrompt() != ui.PRWorktreePrompt {
		t.Errorf("expected PR worktree prompt, got %q", m.ConfirmPrompt())
	}
	if m.WorktreeInput() != "" {
		t.Errorf("expected empty PR input, got %q", m.WorktreeInput())
	}
	if got := m.InputMode(); got != modal.InputSingleLine {
		t.Errorf("PR input mode = %v, want single-line", got)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd opening PR input, got %T", cmd)
	}
}

func TestModel_NKeyNoOpOutsideCreationModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  rune
		mode ui.Mode
	}{
		{name: "stashes", key: 's', mode: ui.ModeStashes},
		{name: "history", key: 'h', mode: ui.ModeHistory},
		{name: "reflog", key: 'r', mode: ui.ModeReflog},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model.New(testRepos())
			m = inWorktreesMode(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			if m.Mode() != tc.mode {
				t.Fatalf("expected mode %d, got %d", tc.mode, m.Mode())
			}

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
			if m.Overlay() != ui.OverlayNone {
				t.Errorf("expected OverlayNone, got %d", m.Overlay())
			}
			if cmd != nil {
				t.Errorf("expected nil cmd, got %T", cmd)
			}
		})
	}
}

func TestModel_PKeyNoOpOutsideWorktreesMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  rune
		mode ui.Mode
	}{
		{name: "branches", key: 'b', mode: ui.ModeBranches},
		{name: "stashes", key: 's', mode: ui.ModeStashes},
		{name: "history", key: 'h', mode: ui.ModeHistory},
		{name: "reflog", key: 'r', mode: ui.ModeReflog},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model.New(testRepos())
			m = inWorktreesMode(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			if m.Mode() != tc.mode {
				t.Fatalf("expected mode %d, got %d", tc.mode, m.Mode())
			}

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
			if m.Overlay() != ui.OverlayNone {
				t.Errorf("expected OverlayNone, got %d", m.Overlay())
			}
			if cmd != nil {
				t.Errorf("expected nil cmd, got %T", cmd)
			}
		})
	}
}

func TestModel_PKeyNoOpWithoutSelectedRepo(t *testing.T) {
	m := model.New(nil)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}

func TestModel_WorktreeInputCapturesRunesAndBackspace(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.WorktreeInput() != "fea" {
		t.Errorf("expected input %q, got %q", "fea", m.WorktreeInput())
	}
}

func TestModel_WorktreeInputEscCancels(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "" {
		t.Errorf("expected input cleared, got %q", m.WorktreeInput())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd on cancel, got %T", cmd)
	}
}

func TestModel_WorktreeInputCtrlCCancels(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "" {
		t.Errorf("expected input cleared, got %q", m.WorktreeInput())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd on cancel, got %T", cmd)
	}
}

func TestModel_WorktreeInputEnterRequiresText(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected input overlay to remain, got %d", m.Overlay())
	}
	if m.WorktreeInputErr() == "" {
		t.Fatal("expected validation error")
	}
	if cmd != nil {
		t.Errorf("expected nil cmd for empty input, got %T", cmd)
	}
}

func TestModel_PullRequestWorktreeInputEnterRequiresText(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected input overlay to remain, got %d", m.Overlay())
	}
	if m.ConfirmPrompt() != ui.PRWorktreePrompt {
		t.Errorf("expected PR worktree prompt, got %q", m.ConfirmPrompt())
	}
	if m.WorktreeInputErr() == "" {
		t.Fatal("expected validation error")
	}
	if cmd != nil {
		t.Errorf("expected nil cmd for empty PR input, got %T", cmd)
	}
}

func TestModel_PullRequestWorktreeInputRejectsUnsupportedURL(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("https://gitlab.com/acme/project/-/merge_requests/123")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected input overlay to remain, got %d", m.Overlay())
	}
	if m.ConfirmPrompt() != ui.PRWorktreePrompt {
		t.Errorf("expected PR worktree prompt, got %q", m.ConfirmPrompt())
	}
	if !strings.Contains(m.WorktreeInputErr(), "unsupported PR URL host") {
		t.Fatalf("expected unsupported host validation error, got %q", m.WorktreeInputErr())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd for invalid PR URL, got %T", cmd)
	}
}

func TestModel_WorktreeInputEnterCreatesWorktree(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected create worktree cmd")
	}
	msg := cmd()
	if _, ok := msg.(model.WorktreeCreateFailedMsg); !ok {
		t.Fatalf("expected WorktreeCreateFailedMsg from fake repo, got %T", msg)
	}
}

func TestModel_PullRequestWorktreeInputEnterCreatesWorktree(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("123")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected create PR worktree cmd")
	}
	msg := cmd()
	failed, ok := msg.(model.WorktreeCreateFailedMsg)
	if !ok {
		t.Fatalf("expected WorktreeCreateFailedMsg from fake repo, got %T", msg)
	}
	if failed.Kind != model.WorktreeCreatePullRequest {
		t.Fatalf("expected pull request create kind, got %d", failed.Kind)
	}
}

func TestModel_WorktreeCreatedRefetchesWorktrees(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, cmd := update(m, model.WorktreeCreatedMsg{RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"})
	if m.Mode() != ui.ModeWorktrees {
		t.Errorf("expected mode worktrees after create, got %d", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd after create")
	}
}

func TestModel_WorktreeCreatedExitsActiveFlows(t *testing.T) {
	m := inWorktreesMode(model.New(testRepos()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("mode before create completion = %d, want active flows", m.Mode())
	}

	m, _ = update(m, model.WorktreeCreatedMsg{RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode after create completion = %d, want worktrees", m.Mode())
	}
}

func TestModel_WorktreeCreateFailedReopensInput(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.WorktreeCreateFailedMsg{RepoPath: "/dev/alpha", Input: "feat", Err: "boom"})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "feat" {
		t.Errorf("expected input restored, got %q", m.WorktreeInput())
	}
	if m.WorktreeInputErr() != "boom" {
		t.Errorf("expected error restored, got %q", m.WorktreeInputErr())
	}
}

func TestModel_PullRequestWorktreeCreateFailedReopensPRInput(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.WorktreeCreateFailedMsg{
		RepoPath: "/dev/alpha",
		Input:    "123",
		Err:      "boom",
		Kind:     model.WorktreeCreatePullRequest,
	})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.ConfirmPrompt() != ui.PRWorktreePrompt {
		t.Errorf("expected PR worktree prompt, got %q", m.ConfirmPrompt())
	}
	if m.WorktreeInput() != "123" {
		t.Errorf("expected input restored, got %q", m.WorktreeInput())
	}
	if m.WorktreeInputErr() != "boom" {
		t.Errorf("expected error restored, got %q", m.WorktreeInputErr())
	}
}

func TestModel_WorktreeCreateFailedUsesFallbackError(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.WorktreeCreateFailedMsg{RepoPath: "/dev/alpha", Input: "feat"})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "feat" {
		t.Errorf("expected input restored, got %q", m.WorktreeInput())
	}
	if m.WorktreeInputErr() != "Unable to create worktree" {
		t.Errorf("expected fallback error, got %q", m.WorktreeInputErr())
	}
}

// --- Branch creation ---

func TestModel_NKeyInBranchesModeOpensBranchInput(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "" {
		t.Errorf("expected empty branch input, got %q", m.WorktreeInput())
	}
	if !strings.Contains(m.View(), "Create branch:") {
		t.Errorf("expected branch prompt in view, got %q", m.View())
	}
	if got := m.InputMode(); got != modal.InputSingleLine {
		t.Errorf("branch input mode = %v, want single-line", got)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd opening input, got %T", cmd)
	}
}

func TestModel_NKeyInBranchesModeWithNoRepoIsNoOp(t *testing.T) {
	m := model.New(nil)
	m = inBranchesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("no-repo branch creation returned cmd %T, want nil", cmd)
	}
}

func TestModel_BranchInputEnterRequiresText(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected input overlay to remain, got %d", m.Overlay())
	}
	if m.WorktreeInputErr() == "" {
		t.Fatal("expected validation error")
	}
	if cmd != nil {
		t.Errorf("expected nil cmd for empty input, got %T", cmd)
	}
}

func TestModel_BranchInputEnterCreatesBranch(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature/one")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected create branch cmd")
	}
	msg := cmd()
	if _, ok := msg.(model.BranchCreateFailedMsg); !ok {
		t.Fatalf("expected BranchCreateFailedMsg from fake repo, got %T", msg)
	}
}

func TestModel_BranchInputUsesSelectedBranchAsStartPoint(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main"},
			{Name: "base"},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature/from-base")})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create branch cmd")
	}
	msg, ok := cmd().(model.BranchCreateFailedMsg)
	if !ok {
		t.Fatalf("expected BranchCreateFailedMsg from fake repo, got %T", msg)
	}
	if msg.StartPoint != "refs/heads/base" {
		t.Fatalf("expected start point refs/heads/base, got %q", msg.StartPoint)
	}
}

func TestModel_BranchInputUsesFullRefForHeadsPrefixedBranch(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main", FullRef: "refs/heads/main"},
			{Name: "heads/base", FullRef: "refs/heads/heads/base"},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature/from-heads-base")})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create branch cmd")
	}
	msg, ok := cmd().(model.BranchCreateFailedMsg)
	if !ok {
		t.Fatalf("expected BranchCreateFailedMsg from fake repo, got %T", msg)
	}
	if msg.StartPoint != "refs/heads/heads/base" {
		t.Fatalf("expected start point refs/heads/heads/base, got %q", msg.StartPoint)
	}
}

func TestModel_BranchCreatedRefetchesBranches(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, cmd := update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feature/one"})
	if m.Mode() != ui.ModeBranches {
		t.Errorf("expected mode branches after create, got %d", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected fetchBranches cmd after create")
	}
}

func TestModel_BranchCreatedExitsActiveFlows(t *testing.T) {
	m := inBranchesMode(model.New(testRepos()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("mode before branch creation = %d, want active flows", m.Mode())
	}

	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feature/one"})
	if m.Mode() != ui.ModeBranches {
		t.Fatalf("mode after branch creation = %d, want branches", m.Mode())
	}
}

func TestModel_BranchCreateFailedReopensBranchInput(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.BranchCreateFailedMsg{RepoPath: "/dev/alpha", Input: "feature/one", Err: "boom"})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "feature/one" {
		t.Errorf("expected input restored, got %q", m.WorktreeInput())
	}
	if m.WorktreeInputErr() != "boom" {
		t.Errorf("expected error restored, got %q", m.WorktreeInputErr())
	}
	if !strings.Contains(m.View(), "Create branch:") {
		t.Errorf("expected branch prompt in view, got %q", m.View())
	}
}

func TestModel_BranchCreateFailedRetryPreservesStartPoint(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main"},
			{Name: "base"},
		},
	})
	m, _ = update(m, model.BranchCreateFailedMsg{
		RepoPath:   "/dev/alpha",
		Input:      "bad name",
		Err:        "bad branch name",
		StartPoint: "refs/heads/base",
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature/from-base")})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected retry create branch cmd")
	}
	msg, ok := cmd().(model.BranchCreateFailedMsg)
	if !ok {
		t.Fatalf("expected BranchCreateFailedMsg from fake repo, got %T", msg)
	}
	if msg.StartPoint != "refs/heads/base" {
		t.Fatalf("expected retry to preserve start point refs/heads/base, got %q", msg.StartPoint)
	}
}

func TestModel_BranchCreateFailedUsesFallbackError(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.BranchCreateFailedMsg{RepoPath: "/dev/alpha", Input: "feature/one"})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Errorf("expected OverlayWorktreeInput, got %d", m.Overlay())
	}
	if m.WorktreeInputErr() != "Unable to create branch" {
		t.Errorf("expected fallback error, got %q", m.WorktreeInputErr())
	}
}

func TestModel_BranchCreatedClearsFilterBeforeSelectingNewBranch(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main"},
			{Name: "base"},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("base")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feature/one"})
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main"},
			{Name: "base"},
			{Name: "feature/one"},
		},
	})
	if m.ItemSearch() != "" {
		t.Fatalf("expected branch filter cleared after create, got %q", m.ItemSearch())
	}
	if m.BranchSelected() != 2 {
		t.Fatalf("expected new branch selected at index 2, got %d", m.BranchSelected())
	}
}

func TestModel_BranchCreatedSelectsNewBranchAfterRefresh(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feature/one"})
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main"},
			{Name: "feature/one"},
		},
	})
	if m.BranchSelected() != 1 {
		t.Fatalf("expected new branch selected at index 1, got %d", m.BranchSelected())
	}
}

func TestModel_BranchCreatedSelectsNewBranchByFullRef(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "base"})
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main", FullRef: "refs/heads/main"},
			{Name: "heads/base", FullRef: "refs/heads/base"},
		},
	})
	if m.BranchSelected() != 1 {
		t.Fatalf("expected new branch selected by full ref at index 1, got %d", m.BranchSelected())
	}
}

func TestModel_BranchCreatedPendingSelectionClearsOnRepoSwitch(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feature/one"})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // repo bravo
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/bravo",
		Branches: []gitquery.Branch{
			{Name: "main"},
			{Name: "feature/one"},
		},
	})

	if m.BranchSelected() != 0 {
		t.Fatalf("expected repo switch to clear pending branch selection, got index %d", m.BranchSelected())
	}
}

// --- Confirmation dialog + delete ---

// enableDestructive presses Shift+D to enter destructive mode.
func enableDestructive(m model.Model) model.Model {
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	return m
}

func modelWithDeletableBranch() model.Model {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{{Name: "feat"}},
	})
	m = enableDestructive(m)
	return m
}

func TestModel_DKeyOpensConfirmOverlay(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("expected OverlayConfirm, got %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "feat") {
		t.Errorf("expected confirm prompt to contain branch name, got %q", m.ConfirmPrompt())
	}
}

func TestModel_DKeyOnNonWorktreeBranchOpensDeleteConfirm(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{{Name: "main"}},
	})
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("expected OverlayConfirm for non-worktree branch, got %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "main") {
		t.Errorf("expected confirm prompt to contain branch name, got %q", m.ConfirmPrompt())
	}
	if !strings.Contains(m.ConfirmPrompt(), "Delete branch") {
		t.Errorf("expected 'Delete branch' in prompt, got %q", m.ConfirmPrompt())
	}
}

func TestModel_DKeyNoOpWithNoBranches(t *testing.T) {
	m := model.New(testRepos())
	// No branches loaded
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone when no branches, got %d", m.Overlay())
	}
}

func TestModel_ConfirmCancelEsc(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed on esc, got %d", m.Overlay())
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("esc in confirm dialog should not quit")
		}
	}
}

func TestModel_ConfirmCancelQ(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed on q, got %d", m.Overlay())
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("q in confirm dialog should not quit")
		}
	}
}

func TestModel_ConfirmCancelN(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed on n, got %d", m.Overlay())
	}
}

func TestModel_ConfirmYClosesOverlayAndReturnsCmd(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed after confirm, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected action cmd after confirm, got nil")
	}
}

func TestModel_ConfirmEnterExecutesAction(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed after enter, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected action cmd after enter confirm, got nil")
	}
}

func TestModel_BranchDeleteFailReturnsDeleteFailedMsg(t *testing.T) {
	// With a fake repo path, DeleteBranch will fail → returns DeleteFailedMsg
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{{Name: "feat"}},
	})
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.DeleteFailedMsg); !ok {
		t.Fatalf("expected DeleteFailedMsg on fake-path failure, got %T", msg)
	}
}

func TestModel_DeleteFailedMsgOpensForceConfirm(t *testing.T) {
	m := model.New(testRepos())
	forceActionCalled := false
	m, _ = update(m, model.DeleteFailedMsg{
		RepoPath: "/dev/alpha",
		Target:   "feat",
		ForceAction: func() error {
			forceActionCalled = true
			return nil
		},
	})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("expected OverlayConfirm after DeleteFailedMsg, got %d", m.Overlay())
	}
	if !m.ConfirmForce() {
		t.Error("expected ConfirmForce=true after DeleteFailedMsg")
	}
	if !strings.Contains(m.ConfirmPrompt(), "Force delete") {
		t.Errorf("expected 'Force delete' in prompt, got %q", m.ConfirmPrompt())
	}
	if !strings.Contains(m.ConfirmPrompt(), "feat") {
		t.Errorf("expected target in prompt, got %q", m.ConfirmPrompt())
	}
	_ = forceActionCalled
}

func TestModel_ForceConfirmCancelClearsForce(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.DeleteFailedMsg{
		RepoPath:    "/dev/alpha",
		Target:      "feat",
		ForceAction: func() error { return nil },
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed after cancel, got %d", m.Overlay())
	}
	if m.ConfirmForce() {
		t.Error("expected ConfirmForce cleared after cancel")
	}
}

func TestModel_ForceConfirmYExecutesForceAction(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.DeleteFailedMsg{
		RepoPath:    "/dev/alpha",
		Target:      "feat",
		ForceAction: func() error { return nil },
	})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed after force confirm, got %d", m.Overlay())
	}
	if m.ConfirmForce() {
		t.Error("expected ConfirmForce cleared after confirm")
	}
	if cmd == nil {
		t.Fatal("expected cmd from force action, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.BranchDeletedMsg); !ok {
		t.Fatalf("expected BranchDeletedMsg from force action, got %T", msg)
	}
}

func TestModel_ConfirmDialogBlocksModeSwitch(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.Mode() != ui.ModeBranches {
		t.Errorf("confirm dialog should block mode switch, mode changed to %d", m.Mode())
	}
}

// --- Stash drop ---

func modelInStashesWithStashes() model.Model {
	m := model.New(testRepos())
	m = inRightPane(m)
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	return m
}

func TestModel_DKeyInStashesModeOpensConfirmDialog(t *testing.T) {
	m := modelInStashesWithStashes()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("expected OverlayConfirm, got %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "stash@{0}") {
		t.Errorf("expected prompt to contain 'stash@{0}', got %q", m.ConfirmPrompt())
	}
}

func TestModel_DKeyInStashesModeWithNoStashesDoesNothing(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	// No stashes loaded
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone when no stashes, got %d", m.Overlay())
	}
}

func TestModel_StashDropConfirmReturnsStashDroppedMsg(t *testing.T) {
	m := modelInStashesWithStashes()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected cmd after stash drop confirm, got nil")
	}
}

// --- Open terminal / code ---

func TestModel_TKey_WorktreeBranch_FiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
		},
	})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Error("expected non-nil cmd when pressing t on a worktree branch")
	}
}

func TestModel_TKey_UsesInjectedLaunchTerminal(t *testing.T) {
	var gotPath string
	m := model.NewWithOptions(testRepos(), model.Options{
		LaunchTerminal: func(path string) (actions.TerminalLaunchSpec, error) {
			gotPath = path
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-worktrees/feature", BranchName: "feature"},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Fatal("expected terminal launch command")
	}
	if gotPath != "/dev/alpha-worktrees/feature" {
		t.Fatalf("expected launch path /dev/alpha-worktrees/feature, got %q", gotPath)
	}
}

func TestModel_CKey_WorktreeBranch_FiresCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
		},
	})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Error("expected non-nil cmd when pressing c on a worktree branch")
	}
}

func TestModel_TKey_NonWorktreeBranch_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "stale-branch"},
		},
	})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd != nil {
		t.Error("expected nil cmd when pressing t on a non-worktree branch")
	}
}

func TestModel_CKey_NonWorktreeBranch_NoCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "stale-branch"},
		},
	})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Error("expected nil cmd when pressing c on a non-worktree branch")
	}
}

// --- Coding agent actions ---

func TestModel_NewHasUnsetAgent(t *testing.T) {
	m := model.New(testRepos())
	if m.AgentCommand() != "" {
		t.Fatalf("expected default model agent unset, got %q", m.AgentCommand())
	}
}

func TestModel_NewWithOptionsStoresAgent(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	if m.AgentCommand() != "codex" {
		t.Fatalf("expected configured agent codex, got %q", m.AgentCommand())
	}
}

func TestModel_NewWithOptionsStoresCodexAppAgent(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: " CoDeX-App "})
	if m.AgentCommand() != "codex-app" {
		t.Fatalf("expected configured agent codex-app, got %q", m.AgentCommand())
	}
}

func TestModel_ShiftAOpensAgentSelectFromBothPanes(t *testing.T) {
	for _, setup := range []struct {
		name string
		fn   func(model.Model) model.Model
	}{
		{name: "left", fn: func(m model.Model) model.Model { return m }},
		{name: "right", fn: inRightPane},
	} {
		t.Run(setup.name, func(t *testing.T) {
			m := setup.fn(model.New(testRepos()))
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
			if m.Overlay() != ui.OverlaySelect {
				t.Fatalf("expected select overlay, got %d", m.Overlay())
			}
			view := m.View()
			for _, want := range []string{"Choose interactive helper", "codex", "codex-app", "claude"} {
				if !strings.Contains(view, want) {
					t.Fatalf("expected agent select view to contain %q", want)
				}
			}
			assertRenderedSelectPanel(t, view, "Choose interactive helper", 32, 6, 24, 8)
			if cmd != nil {
				t.Fatalf("expected nil cmd opening agent select, got %T", cmd)
			}
		})
	}
}

func TestModel_ShiftAAgentSelectPreselectsCurrentAgent(t *testing.T) {
	for _, tt := range []struct {
		name         string
		agent        string
		wantSelected string
	}{
		{name: "unset", wantSelected: "codex"},
		{name: "invalid", agent: "unsupported", wantSelected: "codex"},
		{name: "codex-app", agent: "codex-app", wantSelected: "codex-app"},
		{name: "claude", agent: "claude", wantSelected: "claude"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: tt.agent})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
			view := m.View()

			if !strings.Contains(view, "> "+tt.wantSelected) {
				t.Fatalf("expected %s selected in view:\n%s", tt.wantSelected, view)
			}
		})
	}
}

type renderedSelectPanelBounds struct {
	x      int
	y      int
	width  int
	height int
}

func assertRenderedSelectPanel(t *testing.T, view, prompt string, wantWidth, wantHeight, wantX, wantY int) {
	t.Helper()
	bounds := renderedSelectPanelBoundsForPrompt(t, view, prompt)
	if bounds.width != wantWidth || bounds.height != wantHeight || bounds.x != wantX || bounds.y != wantY {
		t.Fatalf("select panel bounds = x:%d y:%d w:%d h:%d, want x:%d y:%d w:%d h:%d:\n%s",
			bounds.x, bounds.y, bounds.width, bounds.height,
			wantX, wantY, wantWidth, wantHeight,
			ansi.Strip(view))
	}
}

func renderedSelectPanelBoundsForPrompt(t *testing.T, view, prompt string) renderedSelectPanelBounds {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	var found bool
	var best renderedSelectPanelBounds
	for y, line := range lines {
		runes := []rune(line)
		for x, r := range runes {
			if r != '┌' {
				continue
			}
			for right := x + 1; right < len(runes); right++ {
				if runes[right] != '┐' {
					continue
				}
				for bottom := y + 1; bottom < len(lines); bottom++ {
					bottomRunes := []rune(lines[bottom])
					if x >= len(bottomRunes) || right >= len(bottomRunes) {
						continue
					}
					if bottomRunes[x] != '└' || bottomRunes[right] != '┘' {
						continue
					}
					candidate := renderedSelectPanelBounds{
						x:      x,
						y:      y,
						width:  right - x + 1,
						height: bottom - y + 1,
					}
					if !renderedPanelContainsPrompt(lines, candidate, prompt) {
						continue
					}
					if !found || candidate.width < best.width {
						found = true
						best = candidate
					}
				}
			}
		}
	}
	if found {
		return best
	}
	t.Fatalf("select panel for prompt %q not found:\n%s", prompt, ansi.Strip(view))
	return renderedSelectPanelBounds{}
}

func renderedPanelContainsPrompt(lines []string, bounds renderedSelectPanelBounds, prompt string) bool {
	for row := bounds.y; row < bounds.y+bounds.height && row < len(lines); row++ {
		runes := []rune(lines[row])
		if bounds.x >= len(runes) {
			continue
		}
		right := bounds.x + bounds.width
		if right > len(runes) {
			right = len(runes)
		}
		if strings.Contains(string(runes[bounds.x:right]), prompt) {
			return true
		}
	}
	return false
}

func TestModel_AgentSelectSavesAndSetsCodex(t *testing.T) {
	var saved string
	m := model.NewWithOptions(testRepos(), model.Options{
		SaveAgentCommand: func(command string) error {
			saved = command
			return nil
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected save-agent command")
	}
	m, _ = update(m, cmd())
	if saved != "codex" {
		t.Fatalf("expected saved codex, got %q", saved)
	}
	if m.AgentCommand() != "codex" {
		t.Fatalf("expected session agent codex, got %q", m.AgentCommand())
	}
}

func TestModel_AgentSelectDownSavesAndSetsClaude(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save-agent command")
	}
	m, _ = update(m, cmd())
	if m.AgentCommand() != "claude" {
		t.Fatalf("expected session agent claude, got %q", m.AgentCommand())
	}
}

func TestModel_AgentSelectDownSavesAndSetsCodexApp(t *testing.T) {
	var saved string
	m := model.NewWithOptions(testRepos(), model.Options{
		SaveAgentCommand: func(command string) error {
			saved = command
			return nil
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save-agent command")
	}
	m, _ = update(m, cmd())
	if saved != "codex-app" {
		t.Fatalf("expected saved codex-app, got %q", saved)
	}
	if m.AgentCommand() != "codex-app" {
		t.Fatalf("expected session agent codex-app, got %q", m.AgentCommand())
	}
}

func TestModel_AgentSelectUpWrapSavesAndSetsClaude(t *testing.T) {
	var saved string
	m := model.NewWithOptions(testRepos(), model.Options{
		SaveAgentCommand: func(command string) error {
			saved = command
			return nil
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save-agent command")
	}
	m, _ = update(m, cmd())
	if saved != "claude" {
		t.Fatalf("expected saved claude, got %q", saved)
	}
	if m.AgentCommand() != "claude" {
		t.Fatalf("expected session agent claude, got %q", m.AgentCommand())
	}
}

func TestModel_AgentSelectEscCancelsWithoutSaving(t *testing.T) {
	saveCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "claude",
		SaveAgentCommand: func(string) error {
			saveCalled = true
			return nil
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})

	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd for cancel, got %T", cmd)
	}
	if saveCalled {
		t.Fatal("cancel should not call SaveAgentCommand")
	}
	if m.AgentCommand() != "claude" {
		t.Fatalf("expected agent unchanged, got %q", m.AgentCommand())
	}
}

func TestModel_AgentSaveFailureKeepsSessionChoiceAndShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		SaveAgentCommand: func(string) error { return errors.New("disk full") },
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save-agent command")
	}
	m, _ = update(m, cmd())
	if m.AgentCommand() != "codex" {
		t.Fatalf("expected failed save to keep session agent, got %q", m.AgentCommand())
	}
	if !strings.Contains(m.View(), "disk full") {
		t.Fatal("expected save failure in status bar")
	}
}

func TestModel_F2OpensPromptTemplatePicker(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanPromptTemplate: "custom plan prompt",
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "custom flow plan",
		},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF2})

	if cmd != nil {
		t.Fatalf("F2 returned cmd %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("overlay = %v, want select", m.Overlay())
	}
	view := m.View()
	for _, want := range []string{
		"Prompt templates",
		"Plan launch",
		"Flow plan",
		"Plan review",
		"custom",
		"default",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected prompt picker to contain %q:\n%s", want, view)
		}
	}
}

func TestModel_PromptTemplateEditSavesRawValueAndReopensPicker(t *testing.T) {
	var savedSection, savedKey, savedValue string
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanPromptTemplate: "  custom plan\n",
		SavePromptTemplate: func(section, key, value string) error {
			savedSection = section
			savedKey = key
			savedValue = value
			return nil
		},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected prompt template edit request")
	}
	m, _ = update(m, cmd())
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("overlay = %v, want input editor", m.Overlay())
	}
	if got := m.WorktreeInput(); got != "  custom plan\n" {
		t.Fatalf("editor initial input = %q, want raw template", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save prompt template command")
	}
	m, _ = update(m, cmd())

	if savedSection != "agent" || savedKey != "plan_prompt" || savedValue != "  custom plan\n" {
		t.Fatalf("saved template = %q/%q %q, want agent/plan_prompt raw value", savedSection, savedKey, savedValue)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("overlay = %v, want picker reopened", m.Overlay())
	}
	if !strings.Contains(m.View(), "Plan launch") || !strings.Contains(m.View(), "custom") {
		t.Fatalf("expected refreshed picker with custom status:\n%s", m.View())
	}
}

func TestModel_PromptTemplateEditUsesTallEditor(t *testing.T) {
	template := strings.Join([]string{
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
		"line 06",
		"line 07",
		"line 08",
		"line 09",
		"line 10",
		"line 11",
		"line 12",
	}, "\n")
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanPromptTemplate: template,
	})
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected prompt template edit request")
	}
	m, _ = update(m, cmd())

	view := ansi.Strip(m.View())
	for _, want := range []string{"line 01", "line 12█"} {
		if !strings.Contains(view, want) {
			t.Fatalf("prompt template editor should show %q with tall editor:\n%s", want, view)
		}
	}
}

func TestModel_PromptTemplateSaveFailurePreservesCurrentLaunchPrompt(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:       "codex",
		PlanPromptTemplate: "old {title}",
		PlanMarkdownPath:   func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		SavePromptTemplate: func(string, string, string) error { return errors.New("read-only config") },
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected prompt template edit request")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new {title}")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save prompt template command")
	}
	m, _ = update(m, cmd())

	view := m.View()
	if !strings.Contains(view, "read-only config") {
		t.Fatalf("expected save failure status in view:\n%s", view)
	}
	if !strings.Contains(view, "Plan launch      custom") {
		t.Fatalf("expected failed save to keep old custom picker status:\n%s", view)
	}
	if strings.Contains(view, "Plan launch      default") {
		t.Fatalf("failed save should not mark existing custom template as default:\n%s", view)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, model.PlanResultMsg{
		RepoPath: "/dev/alpha",
		Plans: []planstore.PlanRecord{{
			PlanID:   "plan-1",
			Title:    "Implement plans",
			Status:   "approved",
			RepoPath: "/dev/alpha",
		}},
		ListRequest: m.ListRequest(ui.ModePlans),
	})

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	if got, want := m.WorktreeInput(), "old Implement plans"; got != want {
		t.Fatalf("launch prompt after failed save = %q, want %q", got, want)
	}
}

func TestModel_PromptTemplateResetClearsCustomValueAndReopensPicker(t *testing.T) {
	var resetSection, resetKey string
	m := model.NewWithOptions(testRepos(), model.Options{
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "custom flow plan",
		},
		ResetPromptTemplate: func(section, key string) error {
			resetSection = section
			resetKey = key
			return nil
		},
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected reset prompt template command")
	}
	m, _ = update(m, cmd())

	if resetSection != "flow_prompts" || resetKey != "plan" {
		t.Fatalf("reset template = %q/%q, want flow_prompts/plan", resetSection, resetKey)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("overlay = %v, want picker reopened", m.Overlay())
	}
	view := m.View()
	if strings.Contains(view, "Flow plan        custom") {
		t.Fatalf("expected Flow plan reset to default:\n%s", view)
	}
	if !strings.Contains(view, "Flow plan") || !strings.Contains(view, "default") {
		t.Fatalf("expected refreshed picker with default status:\n%s", view)
	}
}

func TestModel_PromptTemplateViewDefaultRendersBuiltInWithPlaceholders(t *testing.T) {
	m := model.New(testRepos())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})

	if m.Overlay() != ui.OverlayPlanText {
		t.Fatalf("overlay = %v, want text preview", m.Overlay())
	}
	preview := m.OverlayText()
	for _, want := range []string{"Implement the saved approach plan", "{title}", "{plan_id}", "{plan_path}"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("expected plan prompt preview to contain %q:\n%s", want, preview)
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})

	preview = m.OverlayText()
	for _, want := range []string{"Use the approach-flow skill", "{instructions}", "After completing this phase goal"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("expected Flow plan preview to contain %q:\n%s", want, preview)
		}
	}
}

func TestModel_FlowEffortPickerUsesCodexChoicesAndPersists(t *testing.T) {
	var savedCommand, savedEffort string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SaveAgentReasoningEffort: func(command, effort string) error {
			savedCommand = command
			savedEffort = effort
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening effort picker, got %T", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected effort select overlay, got %d", m.Overlay())
	}
	view := m.View()
	for _, want := range []string{"Choose codex reasoning effort", "default", "minimal", "low", "medium", "high", "xhigh"} {
		if !strings.Contains(view, want) {
			t.Fatalf("codex effort picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "max") {
		t.Fatalf("codex effort picker should not include max:\n%s", view)
	}

	for range 4 {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save effort command")
	}
	m, _ = update(m, cmd())
	if savedCommand != "codex" || savedEffort != "high" {
		t.Fatalf("saved command/effort = %q/%q, want codex/high", savedCommand, savedEffort)
	}
	if got := m.ReasoningEffortFor("codex"); got != "high" {
		t.Fatalf("session codex effort = %q, want high", got)
	}
}

func TestModel_FlowModelPickerUsesCodexChoicesAndPersists(t *testing.T) {
	var savedCommand, savedModel string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SaveAgentModel: func(command, selectedModel string) error {
			savedCommand = command
			savedModel = selectedModel
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening model picker, got %T", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected model select overlay, got %d", m.Overlay())
	}
	view := m.View()
	for _, want := range []string{"Choose codex model", "default", "gpt-5.5"} {
		if !strings.Contains(view, want) {
			t.Fatalf("codex model picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "claude-sonnet-5") {
		t.Fatalf("codex model picker should not include claude models:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save model command")
	}
	m, _ = update(m, cmd())
	if savedCommand != "codex" || savedModel != "gpt-5.5" {
		t.Fatalf("saved command/model = %q/%q, want codex/gpt-5.5", savedCommand, savedModel)
	}
	if got := m.ModelFor("codex"); got != "gpt-5.5" {
		t.Fatalf("session codex model = %q, want gpt-5.5", got)
	}
}

func TestModel_FlowModelPickerOpensFromLeftPane(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
	})
	m, _ = switchTestMode(m, ui.ModeFlows)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.ActivePane() != ui.PaneRepos {
		t.Fatalf("test setup active pane = %d, want left pane", m.ActivePane())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening model picker, got %T", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected model select overlay from left pane, got %d", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "Choose codex model") {
		t.Fatalf("codex model picker did not open from left pane:\n%s", view)
	}
}

func TestModel_FlowsModeLabelsAgentAndEffortSeparately(t *testing.T) {
	tests := []struct {
		name       string
		options    model.Options
		wantAgent  string
		wantModel  string
		wantEffort string
		notWant    []string
	}{
		{
			name:       "codex",
			options:    model.Options{AgentCommand: "codex", CodexModel: "gpt-5.5", CodexReasoningEffort: "high"},
			wantAgent:  "A      codex",
			wantModel:  "M      gpt-5.5",
			wantEffort: "E      effort: high",
			notWant:    []string{"A      set agent", "M      model: gpt-5.5", "E      codex effort: high"},
		},
		{
			name:       "codex model with claude prefix",
			options:    model.Options{AgentCommand: "codex", CodexModel: "claude-compatible", CodexReasoningEffort: "high"},
			wantAgent:  "A      codex",
			wantModel:  "M      claude-compatible",
			wantEffort: "E      effort: high",
			notWant:    []string{"M      compatible", "M      model: claude-compatible"},
		},
		{
			name:       "codex app",
			options:    model.Options{AgentCommand: "codex-app"},
			wantAgent:  "A      codex-app",
			wantModel:  "M      app",
			wantEffort: "E      effort: app",
			notWant:    []string{"M      model: app", "M      app default", "E      app default", "E      codex-app default"},
		},
		{
			name:       "claude",
			options:    model.Options{AgentCommand: "claude", ClaudeModel: "claude-fable-5", ClaudeReasoningEffort: "max"},
			wantAgent:  "A      claude",
			wantModel:  "M      fable-5",
			wantEffort: "E      effort: max",
			notWant:    []string{"M      claude-fable-5", "M      model: fable-5", "E      claude effort: max"},
		},
		{
			name:       "codex default model",
			options:    model.Options{AgentCommand: "codex"},
			wantAgent:  "A      codex",
			wantModel:  "M      default",
			wantEffort: "E      effort: default",
			notWant:    []string{"M      model: default"},
		},
		{
			name:      "unset",
			wantAgent: "A      choose agent",
			notWant:   []string{"M      ", "E      choose agent", "E      effort:", "E      app default"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model.NewWithOptions(testRepos(), tt.options)
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 12})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

			view := ansi.Strip(m.View())
			agentIndex := strings.Index(view, tt.wantAgent)
			if agentIndex < 0 {
				t.Fatalf("Flow shortcuts missing agent label %q:\n%s", tt.wantAgent, view)
			}
			modelIndex := -1
			if tt.wantModel != "" {
				modelIndex = strings.Index(view, tt.wantModel)
				if modelIndex < 0 || agentIndex > modelIndex {
					t.Fatalf("Flow shortcuts should group agent before model %q:\n%s", tt.wantModel, view)
				}
			}
			if tt.wantEffort != "" {
				effortIndex := strings.Index(view, tt.wantEffort)
				if effortIndex < 0 || agentIndex > effortIndex || (modelIndex >= 0 && modelIndex > effortIndex) {
					t.Fatalf("Flow shortcuts should group agent before effort %q:\n%s", tt.wantEffort, view)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(view, notWant) {
					t.Fatalf("Flow shortcuts should not include %q:\n%s", notWant, view)
				}
			}
		})
	}
}

func TestModel_FlowEffortPickerUsesClaudeChoices(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "claude"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected effort select overlay, got %d", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "max") {
		t.Fatalf("claude effort picker should include max:\n%s", view)
	}
	if view := m.View(); !strings.Contains(view, "Choose claude reasoning effort") {
		t.Fatalf("claude effort picker should name provider:\n%s", view)
	}
}

func TestModel_FlowEffortPickerDoesNotOpenDuringSearchOrModal(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if cmd != nil {
		t.Fatalf("search E returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("search E opened overlay %d", m.Overlay())
	}
	if got := m.ItemSearch(); got != "E" {
		t.Fatalf("search query after E = %q, want E", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected agent select overlay, got %d", m.Overlay())
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if cmd != nil {
		t.Fatalf("modal E returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("modal E changed overlay to %d, want select", m.Overlay())
	}
	view := m.View()
	if !strings.Contains(view, "Choose interactive helper") || strings.Contains(view, "reasoning effort") {
		t.Fatalf("modal E should keep existing agent picker:\n%s", view)
	}
}

func TestModel_FlowEffortSaveFailureKeepsSessionChoiceAndShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		SaveAgentReasoningEffort: func(string, string) error {
			return errors.New("disk full")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	for range 2 {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save effort command")
	}
	m, _ = update(m, cmd())
	if got := m.ReasoningEffortFor("codex"); got != "low" {
		t.Fatalf("expected failed save to keep session effort low, got %q", got)
	}
	if !strings.Contains(m.View(), "disk full") {
		t.Fatal("expected save failure in status bar")
	}
}

func TestModel_FlowEffortPickerRequiresSelectedAgent(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if cmd == nil {
		t.Fatal("expected status expiry command without selected agent")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay without agent, got %d", m.Overlay())
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatal("expected unset-agent status")
	}
}

func TestModel_FlowEffortPickerReportsCodexAppDefault(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex-app"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for codex-app effort")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay for codex-app effort, got %d", m.Overlay())
	}
	if got := m.TransientError(); !strings.Contains(got, "Codex App uses app default") {
		t.Fatalf("status = %q, want app default message", got)
	}
}

func TestModel_FlowModelPickerReportsCodexAppDefault(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex-app"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for codex-app model")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay for codex-app model, got %d", m.Overlay())
	}
	if got := m.TransientError(); !strings.Contains(got, "Codex App uses app default model") {
		t.Fatalf("status = %q, want app default model message", got)
	}
}

func TestModel_AKeyLaunchesAgentFromWorktree(t *testing.T) {
	var gotPath, gotCommand, gotModel, gotEffort string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "codex",
		CodexModel:            "gpt-5.5",
		CodexReasoningEffort:  "high",
		ClaudeReasoningEffort: "max",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			gotPath = ctx.WorktreePath
			gotCommand = ctx.Command
			gotModel = ctx.Model
			gotEffort = ctx.ReasoningEffort
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	if gotPath != "/dev/alpha" || gotCommand != "codex" {
		t.Fatalf("expected launch /dev/alpha with codex, got path=%q command=%q", gotPath, gotCommand)
	}
	if gotModel != "gpt-5.5" {
		t.Fatalf("expected codex launch model gpt-5.5, got %q", gotModel)
	}
	if gotEffort != "high" {
		t.Fatalf("expected codex launch effort high, got %q", gotEffort)
	}
}

func TestModel_AKeyLaunchesCodexAppFromWorktree(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Commit: "abc123", IsMain: true},
	}, ListRequest: m.ListRequest(ui.ModeWorktrees)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	if got.Command != "codex-app" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha" ||
		got.Branch != "main" ||
		got.Commit != "abc123" {
		t.Fatalf("unexpected codex-app launch context: %#v", got)
	}
}

func TestModel_AKeyLaunchesAgentWithSessionMetadata(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/approach/sessions/v1",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Commit: "abc123", IsMain: true},
	}, ListRequest: m.ListRequest(ui.ModeWorktrees)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	if got.Command != "codex" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha" ||
		got.Branch != "main" ||
		got.Commit != "abc123" ||
		got.SessionStateRoot != "/state/approach/sessions/v1" {
		t.Fatalf("unexpected launch context: %#v", got)
	}
	if got.LaunchID == "" {
		t.Fatalf("expected launch ID in context: %#v", got)
	}
}

func TestModel_AKeyLaunchesAgentFromCheckedOutBranch(t *testing.T) {
	var gotPath, gotEffort string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeReasoningEffort: "max",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			gotPath = ctx.WorktreePath
			gotEffort = ctx.ReasoningEffort
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
		},
	})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	if gotPath != "/dev/alpha" {
		t.Fatalf("expected launch from branch worktree path, got %q", gotPath)
	}
	if gotEffort != "max" {
		t.Fatalf("expected claude launch effort max, got %q", gotEffort)
	}
}

func TestModel_AKeyNoOpsForBareOrStaleTargets(t *testing.T) {
	t.Run("bare branch", func(t *testing.T) {
		m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
		m = inBranchesMode(m)
		m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{{Name: "feat"}}})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		if cmd != nil {
			t.Fatal("expected nil command for bare branch")
		}
	})
	t.Run("stale worktree", func(t *testing.T) {
		m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
		m = inRightPane(m)
		m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
			{Path: "/dev/gone", BranchName: "gone", Stale: true},
		}})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		if cmd != nil {
			t.Fatal("expected nil command for stale worktree")
		}
	})
	t.Run("stale branch row", func(t *testing.T) {
		m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
		m = inBranchesMode(m)
		m, _ = update(m, model.BranchResultMsg{
			RepoPath: "/dev/alpha",
			Branches: []gitquery.Branch{
				{Name: "gone", IsWorktree: true, WorktreePaths: []string{"/dev/gone"}, WorktreeStale: []bool{true}},
			},
		})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		if cmd != nil {
			t.Fatal("expected nil command for stale branch row")
		}
	})
}

func TestModel_AKeyWithNoSelectedAgentShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected status expiry command without selected agent")
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatal("expected unset-agent status")
	}
}

func TestModel_AgentLaunchBuildErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("agent unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected status expiry command when launch cannot be built")
	}
	if !strings.Contains(m.View(), "agent unavailable") {
		t.Fatal("expected launch build error in status bar")
	}
}

func TestModel_AgentProcessErrorShowsStatus(t *testing.T) {
	cleanupCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{
				Cmd: exec.Command("false"),
				Cleanup: func() {
					cleanupCalled = true
				},
			}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "exit status") {
		t.Fatal("expected agent process error in status bar")
	}
	if !cleanupCalled {
		t.Fatal("expected failed detached launch to run cleanup")
	}
}

func TestModel_AgentResultFinalizesLaunchedSession(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		FinalizeAgentSession: func(ctx actions.AgentLaunchContext) error {
			got = ctx
			return nil
		},
	})
	ctx := actions.AgentLaunchContext{
		Command:      "codex",
		LaunchID:     "launch-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Branch:       "main",
	}

	m, cmd := update(m, model.AgentResultMsg{LaunchContext: ctx})
	if got.LaunchID != "" {
		t.Fatalf("FinalizeAgentSession ran inside Update with %#v", got)
	}
	if cmd == nil {
		t.Fatal("AgentResultMsg returned nil command, want asynchronous finalization")
	}
	finalized := cmd()
	if got != ctx {
		t.Fatalf("finalize command got context %#v, want %#v", got, ctx)
	}
	m, _ = update(m, finalized)
	if got != ctx {
		t.Fatalf("finalized context = %#v, want %#v", got, ctx)
	}
}

func TestModel_AgentResultShowsFinalizeError(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		FinalizeAgentSession: func(actions.AgentLaunchContext) error {
			return errors.New("state unavailable")
		},
	})
	ctx := actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1"}

	m, cmd := update(m, model.AgentResultMsg{LaunchContext: ctx})
	if cmd == nil {
		t.Fatal("AgentResultMsg returned nil command, want asynchronous finalization")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "finalize session: state unavailable") {
		t.Fatal("expected finalize error in status bar")
	}
}

func TestModel_DetachedAgentResultDoesNotFinalize(t *testing.T) {
	finalized := false
	m := model.NewWithOptions(testRepos(), model.Options{
		FinalizeAgentSession: func(actions.AgentLaunchContext) error {
			finalized = true
			return nil
		},
	})
	ctx := actions.AgentLaunchContext{
		Command:      "codex",
		LaunchID:     "launch-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Branch:       "main",
	}

	m, _ = update(m, model.AgentResultMsg{LaunchContext: ctx, Detached: true})
	if finalized {
		t.Fatal("detached launch must not finalize the captured session; provider hooks own that")
	}
}

func TestModel_DetachedAgentResultShowsLaunchedStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	ctx := actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1"}

	m, _ = update(m, model.AgentResultMsg{LaunchContext: ctx, Detached: true})
	view := m.View()
	if !strings.Contains(view, "Launched codex") {
		t.Fatalf("expected detached launch status mentioning the agent, got view:\n%s", view)
	}
	if strings.Contains(view, "complete") || strings.Contains(view, "finished") {
		t.Fatalf("detached launch status should not imply the agent finished, got view:\n%s", view)
	}
}

func TestModel_DetachedAgentResultErrorTakesPrecedence(t *testing.T) {
	finalized := false
	m := model.NewWithOptions(testRepos(), model.Options{
		FinalizeAgentSession: func(actions.AgentLaunchContext) error {
			finalized = true
			return nil
		},
	})
	ctx := actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1"}

	m, _ = update(m, model.AgentResultMsg{LaunchContext: ctx, Detached: true, Err: "exit status 1"})
	if finalized {
		t.Fatal("detached launch must not finalize even on error")
	}
	view := m.View()
	if !strings.Contains(view, "exit status 1") {
		t.Fatalf("expected detached launch error in status bar, got view:\n%s", view)
	}
	if strings.Contains(view, "Launched codex") {
		t.Fatalf("error should take precedence over the launched-status message, got view:\n%s", view)
	}
}

func TestModel_SixKeyFetchesSessionsForSelectedRepo(t *testing.T) {
	var gotFilter sessions.SessionFilter
	want := []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Branch: "main", Summary: "Implement sessions"},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.Mode() != ui.ModeSessions {
		t.Fatalf("mode = %d, want sessions", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected sessions fetch command")
	}
	if gotFilter.RepoPath != "" {
		t.Fatalf("session lister ran before command execution: %#v", gotFilter)
	}
	msg, ok := cmd().(model.SessionResultMsg)
	if !ok {
		t.Fatalf("expected SessionResultMsg, got %T", msg)
	}
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	got := m.Sessions()
	if len(got) != 1 || got[0].SessionID != "codex-1" {
		t.Fatalf("Sessions() = %#v, want %#v", got, want)
	}
}

func TestModel_ChangingRepoRefetchesSessionsMode(t *testing.T) {
	var filters []sessions.SessionFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			filters = append(filters, filter)
			return []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: filepath.Base(filter.RepoPath), RepoPath: filter.RepoPath}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd == nil {
		t.Fatal("expected initial sessions fetch")
	}
	m, _ = update(m, cmd())
	if got := m.Sessions(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Sessions() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil {
		t.Fatalf("expected nil cmd switching to repo pane, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected sessions refetch after repo change")
	}
	if got := m.Sessions(); len(got) != 0 {
		t.Fatalf("expected sessions cleared before refetch, got %#v", got)
	}
	for _, msg := range immediateTestCommandMessages(cmd) {
		m, _ = update(m, msg)
	}
	if got := m.Sessions(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("refetched Sessions() = %#v", got)
	}
	if len(filters) != 2 || filters[0].RepoPath != "/dev/alpha" || filters[1].RepoPath != "/dev/bravo" {
		t.Fatalf("session filters = %#v", filters)
	}
}

func TestModel_EnterOnSessionOpensTranscriptOverlay(t *testing.T) {
	var paged []string
	var gotProvider sessions.Provider
	var gotSessionID string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: recordPageText(&paged),
		ReadTranscript: func(provider sessions.Provider, sessionID string) ([]sessions.TranscriptEvent, error) {
			gotProvider = provider
			gotSessionID = sessionID
			return []sessions.TranscriptEvent{
				{Role: "user", Kind: "message", Text: "Implement sessions"},
				{Role: "assistant", Kind: "message", Text: "Done"},
			}, nil
		},
	})
	m, _ = switchTestMode(m, ui.ModeSessions)
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected transcript fetch command")
	}
	msg, ok := cmd().(model.SessionTranscriptResultMsg)
	if !ok {
		t.Fatalf("expected SessionTranscriptResultMsg, got %T", msg)
	}
	m, cmd = update(m, msg)
	if cmd == nil {
		t.Fatal("expected transcript pager command")
	}

	if gotProvider != sessions.ProviderCodex || gotSessionID != "codex-1" {
		t.Fatalf("reader got provider=%q session=%q", gotProvider, gotSessionID)
	}
	if len(paged) != 1 || !strings.Contains(paged[0], "user: Implement sessions") || !strings.Contains(paged[0], "assistant: Done") {
		t.Fatalf("unexpected paged transcript: %#v", paged)
	}
}

func TestModel_OKeyOnSessionOpensTranscriptOverlay(t *testing.T) {
	var gotProvider sessions.Provider
	var gotSessionID string
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadTranscript: func(provider sessions.Provider, sessionID string) ([]sessions.TranscriptEvent, error) {
			gotProvider = provider
			gotSessionID = sessionID
			return []sessions.TranscriptEvent{{Role: "user", Kind: "message", Text: "Implement sessions"}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected transcript fetch command")
	}
	msg, ok := cmd().(model.SessionTranscriptResultMsg)
	if !ok {
		t.Fatalf("expected SessionTranscriptResultMsg, got %T", msg)
	}
	if gotProvider != sessions.ProviderCodex || gotSessionID != "codex-1" {
		t.Fatalf("reader got provider=%q session=%q", gotProvider, gotSessionID)
	}
}

func TestModel_SKeyShowsSelectedSessionSummary(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Summary: "first line\nsecond line\nthird line"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("expected summary pager command")
	}
	if len(paged) != 1 || paged[0] != "first line\nsecond line\nthird line" {
		t.Fatalf("paged summary = %#v", paged)
	}
}

func TestModel_SKeyEmptySessionSummaryShowsFallback(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("expected empty summary pager command")
	}
	if len(paged) != 1 || paged[0] != "No summary" {
		t.Fatalf("paged empty summary = %#v", paged)
	}
}

func TestModel_SKeySessionSummaryPagerFailureShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: func(string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("less not found")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Summary: "summary"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for pager construction failure")
	}
	if got := m.TransientError(); !strings.Contains(got, "less not found") {
		t.Fatalf("status = %q, want pager failure", got)
	}
}

func TestModel_SKeySessionSummaryInvalidatesPendingTranscript(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{
		PageText: recordPageText(&paged),
		ReadTranscript: func(sessions.Provider, string) ([]sessions.TranscriptEvent, error) {
			return []sessions.TranscriptEvent{{Role: "assistant", Text: "old transcript"}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Summary: "summary"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, transcriptCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if transcriptCmd == nil {
		t.Fatal("expected transcript fetch command")
	}
	m, summaryCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if summaryCmd == nil {
		t.Fatal("expected summary pager command")
	}
	_, staleCmd := update(m, transcriptCmd())

	if staleCmd != nil {
		t.Fatalf("expected stale transcript result to return nil command, got %T", staleCmd)
	}
	if len(paged) != 1 || paged[0] != "summary" {
		t.Fatalf("expected only summary to page, paged=%#v", paged)
	}
}

func TestModel_SKeySessionSummaryNoOpsOutsideSessionSelection(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // plans: s has no meaning here

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}); cmd != nil {
		t.Fatalf("expected s outside sessions to no-op, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay outside sessions, got %d", m.Overlay())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}); cmd != nil {
		t.Fatalf("expected s with no selected session to no-op, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay without selected session, got %d", m.Overlay())
	}
}

func TestModel_YKeyCopiesSelectedSessionID(t *testing.T) {
	var copied []string
	m := model.NewWithOptions(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "raw-codex-session-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy session id command")
	}
	m, _ = update(m, cmd())

	if len(copied) != 1 || copied[0] != "raw-codex-session-1" {
		t.Fatalf("copied = %#v, want raw session id", copied)
	}
	if strings.Contains(m.View(), "raw-codex-session-1") {
		t.Fatal("session id copy should not render copied id as an error")
	}
}

func TestModel_YKeySessionCopyNoOpsOutsideSessionSelection(t *testing.T) {
	var copied []string
	m := model.NewWithOptions(testRepos(), model.Options{
		CopyToClipboard: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd != nil {
		t.Fatalf("expected y outside copyable modes to no-op, got %T", cmd)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd != nil {
		t.Fatalf("expected y with no selected session to no-op, got %T", cmd)
	}
	if len(copied) != 0 {
		t.Fatalf("expected no clipboard calls, got %#v", copied)
	}
}

func TestModel_YKeySessionCopyErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		CopyToClipboard: func(string) error {
			return errors.New("clipboard unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "clipboard unavailable") {
		t.Fatal("expected clipboard error in status bar")
	}
}

func TestModel_SessionScrollTreatsMultilineSummariesAsOneRow(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: ui.BranchContentOverhead + 3 + ui.TerminalChipRows})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Branch: "one", Summary: "one first\none second"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-2", RepoPath: "/dev/alpha", Branch: "two", Summary: "two first\ntwo second"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-3", RepoPath: "/dev/alpha", Branch: "three", Summary: "three only"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := m.View()
	if !strings.Contains(view, "> codex     three") {
		t.Fatalf("expected selected third session to stay visible:\n%s", view)
	}
	if !strings.Contains(view, "two first two second") {
		t.Fatalf("expected multiline summaries to collapse whitespace within one row:\n%s", view)
	}
}

type fakeEmbeddedTerminal struct {
	lines        []string
	visibleCalls [][2]int
	writes       []string
	writeErr     error
	writeN       int
	forceWriteN  bool
	resizes      [][2]int
	state        string
	terminateErr error
	terminates   int
}

func (t *fakeEmbeddedTerminal) VisibleLines(width, height int) []string {
	t.visibleCalls = append(t.visibleCalls, [2]int{width, height})
	return t.lines
}
func (t *fakeEmbeddedTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, string(p))
	if t.forceWriteN {
		return t.writeN, t.writeErr
	}
	if t.writeErr != nil {
		return 0, t.writeErr
	}
	return len(p), nil
}
func (t *fakeEmbeddedTerminal) Resize(width, height int) error {
	t.resizes = append(t.resizes, [2]int{width, height})
	return nil
}
func (t *fakeEmbeddedTerminal) Terminate() error {
	t.terminates++
	t.state = "terminated"
	return t.terminateErr
}
func (t *fakeEmbeddedTerminal) Wait(context.Context) error {
	return nil
}
func (t *fakeEmbeddedTerminal) State() string {
	if t.state == "" {
		return "running"
	}
	return t.state
}

func TestModel_RKeyResumeCLIEmbeddedTerminalShowsTerminalView(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}}
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: "/state/approach/sessions/v1",
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			got = ctx
			return fakeTerm, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("CLI session resume should use embedded terminal, not external launcher")
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{
			Provider:     sessions.ProviderCodex,
			SessionID:    "codex-session-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/feat",
			CWD:          "/dev/alpha-worktrees/feat/subdir",
			Branch:       "feature/api",
			Commit:       "abc123",
			PlanID:       "plan-1",
			PlanPath:     "/state/approach/plans/plan-1/plan.md",
		},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("embedded session resume should schedule terminal repaint ticks")
	}
	if got.Command != "codex" ||
		got.ResumeSessionID != "codex-session-1" ||
		got.WorkingDir != "/dev/alpha-worktrees/feat/subdir" ||
		got.SessionStateRoot != "/state/approach/sessions/v1" ||
		got.PlanID != "plan-1" ||
		got.PlanPath != "/state/approach/plans/plan-1/plan.md" {
		t.Fatalf("unexpected embedded resume context: %#v", got)
	}
	view := m.View()
	for _, want := range []string{"1 codex feature/api running", "agent output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("embedded terminal view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "Provider") || !strings.Contains(view, "Summary") {
		t.Fatalf("embedded terminal dock should preserve the saved-session table:\n%s", view)
	}
}

func TestModel_BackKeysForwardWhenSessionTerminalOwnsKeys(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{name: "ctrl-h", key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
			m := model.NewWithOptions(testRepos(), model.Options{
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					return fakeTerm, nil
				},
			})
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
			m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{{
				Provider:     sessions.ProviderCodex,
				SessionID:    "codex-session-1",
				RepoPath:     "/dev/alpha",
				WorktreePath: "/dev/alpha-worktrees/feat",
				CWD:          "/dev/alpha-worktrees/feat",
				Branch:       "feature/api",
			}}, ListRequest: m.ListRequest(ui.ModeSessions)})
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			if cmd == nil {
				t.Fatal("embedded session resume should schedule terminal repaint ticks")
			}

			m, cmd = update(m, tt.key)

			if m.ActivePane() == ui.PaneRepos {
				t.Fatalf("terminal-owned %s activePane = %d, want right pane", tt.name, m.ActivePane())
			}
			if cmd != nil {
				t.Fatalf("terminal-owned %s returned cmd %T, want nil", tt.name, cmd)
			}
			if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\x7f" {
				t.Fatalf("terminal input %s writes = %#v, want delete byte", tt.name, fakeTerm.writes)
			}
		})
	}
}

func TestModel_TabCyclesPaneFocusWhenSessionTerminalOwnsKeys(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"agent output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{{
		Provider:     sessions.ProviderCodex,
		SessionID:    "codex-session-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/feat",
		CWD:          "/dev/alpha-worktrees/feat",
		Branch:       "feature/api",
	}}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("embedded session resume should schedule terminal repaint ticks")
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("tab from session terminal returned cmd %T, want nil", cmd)
	}
	if m.ActivePane() != ui.PaneBottom {
		t.Fatalf("terminal-owned tab active pane = %d, want unchanged bottom pane", m.ActivePane())
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "\t" {
		t.Fatalf("terminal-owned tab writes = %#v, want tab byte", fakeTerm.writes)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatalf("backspace after returning to session terminal returned cmd %T, want nil", cmd)
	}
	if len(fakeTerm.writes) != 2 || fakeTerm.writes[1] != "\x7f" {
		t.Fatalf("terminal-owned backspace writes = %#v, want tab then delete byte", fakeTerm.writes)
	}
}

func TestModel_EmbeddedTerminalViewRendersRealPTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty tests require a Unix-like platform")
	}
	var term *embeddedterm.Terminal
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(_ actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			var err error
			term, err = embeddedterm.NewManager().Start(context.Background(), embeddedterm.StartRequest{
				Command: "sh",
				Args:    []string{"-c", "printf real-pty-output; sleep 1"},
				Width:   width,
				Height:  height,
			})
			if err != nil {
				return nil, err
			}
			return model.NewRealEmbeddedTerminalForTest(term), nil
		},
	})
	t.Cleanup(func() {
		if term != nil {
			_ = term.Terminate()
		}
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.View(), "real-pty-output") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("embedded terminal view never showed real PTY output:\n%s", m.View())
}

func TestModel_RKeyResumeCLIFallsBackWhenEmbeddedTerminalUnsupported(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, pty.ErrUnsupported
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{
			Provider:     sessions.ProviderCodex,
			SessionID:    "codex-session-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/feat",
			Branch:       "feature/api",
		},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected external resume command when embedded PTY is unsupported")
	}
	if got.Command != "codex" || got.ResumeSessionID != "codex-session-1" || got.WorktreePath != "/dev/alpha-worktrees/feat" {
		t.Fatalf("unexpected fallback resume context: %#v", got)
	}
}

func TestModel_EmbeddedTerminalKeysRouteToActivePTY(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("plain terminal key should not return approach command, got %T", cmd)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeySpace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyHome})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnd})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDelete})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyPgUp})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyPgDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlLeft})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlRight})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})

	want := []string{
		"a", " ",
		"\x01", "\x05", "\x12", "\x17",
		"\x1b[H", "\x1b[F", "\x1b[3~", "\x1b[5~", "\x1b[6~", "\x1b[D", "\x1b[C", "\x1b[1;5D", "\x1b[1;5C",
		"\x1bf", "\x1b\x1b[D",
		"\x03", "\a", "\x1d",
	}
	if !reflect.DeepEqual(fakeTerm.writes, want) {
		t.Fatalf("terminal writes = %#v, want %#v", fakeTerm.writes, want)
	}
}

func TestModel_EmbeddedTerminalUsesFullAppWidth(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{}
	var started [2]int
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(_ actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = [2]int{width, height}
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	wantStartWidth := ui.EmbeddedTerminalPTYWidth(180)
	_, wantStartOuterHeight := ui.EmbeddedTerminalDockHeights(14-ui.BranchContentOverhead, ui.EmbeddedTerminalDockExpanded)
	wantStartHeight := ui.EmbeddedTerminalPTYHeight(wantStartOuterHeight)
	wantStartSize := [2]int{wantStartWidth, wantStartHeight}
	if started != wantStartSize {
		t.Fatalf("embedded terminal start size = %dx%d, want %dx%d", started[0], started[1], wantStartWidth, wantStartHeight)
	}
	wantPaddedStartWidth := 180 - ui.EmbeddedTerminalFrameColumns - 2*ui.EmbeddedTerminalSidePadding
	if started[0] != wantPaddedStartWidth {
		t.Fatalf("embedded terminal start width = %d, want padded width %d", started[0], wantPaddedStartWidth)
	}
	_ = m.View()
	if len(fakeTerm.visibleCalls) == 0 || fakeTerm.visibleCalls[len(fakeTerm.visibleCalls)-1] != wantStartSize {
		t.Fatalf("embedded terminal visible calls = %#v, want latest %dx%d", fakeTerm.visibleCalls, wantStartWidth, wantStartHeight)
	}

	m, _ = update(m, tea.WindowSizeMsg{Width: 160, Height: 12})
	wantResizeWidth := ui.EmbeddedTerminalPTYWidth(160)
	_, wantResizeOuterHeight := ui.EmbeddedTerminalDockHeights(12-ui.BranchContentOverhead, ui.EmbeddedTerminalDockExpanded)
	wantResizeHeight := ui.EmbeddedTerminalPTYHeight(wantResizeOuterHeight)
	wantResizeSize := [2]int{wantResizeWidth, wantResizeHeight}
	if len(fakeTerm.resizes) == 0 || fakeTerm.resizes[len(fakeTerm.resizes)-1] != wantResizeSize {
		t.Fatalf("embedded terminal resize calls = %#v, want latest %dx%d", fakeTerm.resizes, wantResizeWidth, wantResizeHeight)
	}
}

func TestModel_EmbeddedTerminalWidthIgnoresShortcutAndSearchState(t *testing.T) {
	t.Run("active search", func(t *testing.T) {
		m, fakeTerm, _ := openEmbeddedSessionForSizingTest(t, 180, 14)

		m = model.SetSearchActiveForTest(m, true)
		_ = m.View()
		_, wantOuterHeight := ui.EmbeddedTerminalDockHeights(14-ui.BranchContentOverhead, ui.EmbeddedTerminalDockExpanded)
		want := [2]int{
			ui.EmbeddedTerminalPTYWidth(180),
			ui.EmbeddedTerminalPTYHeight(wantOuterHeight),
		}
		if len(fakeTerm.visibleCalls) == 0 || fakeTerm.visibleCalls[len(fakeTerm.visibleCalls)-1] != want {
			t.Fatalf("visible calls = %#v, want latest %dx%d", fakeTerm.visibleCalls, want[0], want[1])
		}

		m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
		if len(fakeTerm.resizes) == 0 || fakeTerm.resizes[len(fakeTerm.resizes)-1] != want {
			t.Fatalf("resize calls = %#v, want latest %dx%d", fakeTerm.resizes, want[0], want[1])
		}
	})

	for _, tc := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "height five suppresses shortcut", width: ui.LeftPaneWidth + ui.ShortcutPaneWidth + ui.MinContentPaneWidth, height: 5},
		{name: "height six shows shortcut", width: ui.LeftPaneWidth + ui.ShortcutPaneWidth + ui.MinContentPaneWidth, height: 6},
		{name: "tiny allocation clamps PTY size", width: ui.LeftPaneWidth + 1, height: ui.BranchContentOverhead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, started := openEmbeddedSessionForSizingTest(t, tc.width, tc.height)
			_, wantOuterHeight := ui.EmbeddedTerminalDockHeights(tc.height-ui.BranchContentOverhead, ui.EmbeddedTerminalDockExpanded)
			want := [2]int{
				ui.EmbeddedTerminalPTYWidth(tc.width),
				ui.EmbeddedTerminalPTYHeight(wantOuterHeight),
			}
			if started != want {
				t.Fatalf("embedded terminal start size = %dx%d, want %dx%d", started[0], started[1], want[0], want[1])
			}
		})
	}
}

func TestModel_EmbeddedTerminalKeepsFullWidthWhenRepoPaneCollapsesAndExpands(t *testing.T) {
	const width, height = 180, 14
	m, fakeTerm, _ := openEmbeddedSessionForSizingTest(t, width, height)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	baseline := len(fakeTerm.resizes)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.RepoPaneCollapsed() {
		t.Fatal("enter on selected repo should collapse the repos pane")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.RepoPaneCollapsed() {
		t.Fatal("ctrl+r should expand the repos pane")
	}
	for _, resize := range fakeTerm.resizes[baseline:] {
		want := [2]int{ui.EmbeddedTerminalPTYWidth(width), resize[1]}
		if resize != want {
			t.Fatalf("collapse/expand resized full-width terminal: %#v", fakeTerm.resizes[baseline:])
		}
	}
}

func openEmbeddedSessionForSizingTest(t *testing.T, width, height int) (model.Model, *fakeEmbeddedTerminal, [2]int) {
	t.Helper()
	fakeTerm := &fakeEmbeddedTerminal{}
	var started [2]int
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(_ actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = [2]int{width, height}
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: height})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	return m, fakeTerm, started
}

func TestModel_TerminalPickerLoadsSessionsAfterRepoSwitchClearsCache(t *testing.T) {
	terms := map[string]*fakeEmbeddedTerminal{
		"alpha-session": {lines: []string{"alpha output"}, state: "running"},
		"bravo-session": {lines: []string{"bravo output"}, state: "running"},
	}
	var started []string
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (model.EmbeddedTerminal, error) {
			started = append(started, ctx.ResumeSessionID)
			return terms[ctx.ResumeSessionID], nil
		},
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			if filter.RepoPath != "/dev/bravo" {
				return nil, nil
			}
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderClaude,
				SessionID:    "bravo-session",
				RepoPath:     "/dev/bravo",
				WorktreePath: "/dev/bravo-worktrees/docs",
				Branch:       "docs",
			}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 20})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{{
		Provider:     sessions.ProviderCodex,
		SessionID:    "alpha-session",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/feat",
		Branch:       "feature",
	}}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Switching the selected repo clears the sessions cache while the dock
	// and its terminal persist.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if len(m.Sessions()) != 0 {
		t.Fatalf("repo switch should clear the sessions cache, got %d records", len(m.Sessions()))
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if cmd == nil {
		t.Fatal("picker with a cold cache should load sessions on demand")
	}
	m, _ = update(m, cmd())
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected picker overlay after on-demand load, got overlay %d:\n%s", m.Overlay(), m.View())
	}
	if view := m.View(); !strings.Contains(view, "Resume session") || !strings.Contains(view, "claude docs") {
		t.Fatalf("picker view missing loaded session:\n%s", view)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected picker submit command")
	}
	m, _ = update(m, cmd())
	if want := []string{"alpha-session", "bravo-session"}; !reflect.DeepEqual(started, want) {
		t.Fatalf("started sessions = %#v, want %#v", started, want)
	}
	_ = m
}

func TestModel_EmbeddedTerminalPrefixPickerOpensSecondSession(t *testing.T) {
	terms := map[string]*fakeEmbeddedTerminal{
		"codex-session-1":  {lines: []string{"first output"}},
		"claude-session-2": {lines: []string{"second output"}},
	}
	var started []string
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			started = append(started, ctx.ResumeSessionID)
			return terms[ctx.ResumeSessionID], nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat", Branch: "feature/api"},
		{Provider: sessions.ProviderClaude, SessionID: "claude-session-2", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/docs", Branch: "docs"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if cmd != nil {
		t.Fatalf("opening picker should not return command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlaySelect {
		t.Fatalf("expected picker overlay, got %d", m.Overlay())
	}
	if view := m.View(); !strings.Contains(view, "Resume session") || !strings.Contains(view, "claude docs") {
		t.Fatalf("picker view missing sessions:\n%s", view)
	} else {
		assertRenderedSelectPanel(t, view, "Resume session", 72, 12, 54, 0)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected picker submit command")
	}
	m, _ = update(m, cmd())

	if want := []string{"codex-session-1", "claude-session-2"}; !reflect.DeepEqual(started, want) {
		t.Fatalf("started = %#v, want %#v", started, want)
	}
	view := m.View()
	for _, want := range []string{"1 codex feature/api running", "2 claude docs running", "second output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("embedded terminal view missing %q:\n%s", want, view)
		}
	}
}

func TestModel_EmbeddedTerminalPickerRestartsTickAfterAllPTYsExit(t *testing.T) {
	terms := map[string]*fakeEmbeddedTerminal{
		"codex-session-1":  {lines: []string{"first output"}},
		"claude-session-2": {lines: []string{"second output"}},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			return terms[ctx.ResumeSessionID], nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat", Branch: "feature/api"},
		{Provider: sessions.ProviderClaude, SessionID: "claude-session-2", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/docs", Branch: "docs"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, tick := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if tick == nil {
		t.Fatal("expected first embedded terminal to schedule repaint tick")
	}

	terms["codex-session-1"].state = "exited"
	m, stopped := update(m, tick())
	if stopped != nil {
		t.Fatalf("exited terminal should stop repaint loop, got %T", stopped)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected picker submit command")
	}
	_, restarted := update(m, cmd())
	if restarted == nil {
		t.Fatal("opening a new running terminal after all PTYs exited should restart repaint tick")
	}
}

func TestModel_EmbeddedTerminalPrefixSwitchesActiveTerminal(t *testing.T) {
	terms := map[string]*fakeEmbeddedTerminal{
		"codex-session-1":  {lines: []string{"first output"}},
		"claude-session-2": {lines: []string{"second output"}},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			return terms[ctx.ResumeSessionID], nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat", Branch: "feature/api"},
		{Provider: sessions.ProviderClaude, SessionID: "claude-session-2", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/docs", Branch: "docs"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})

	view := m.View()
	if !strings.Contains(view, "first output") {
		t.Fatalf("switch to terminal 1 should render first terminal:\n%s", view)
	}
	if strings.Contains(view, "second output") {
		t.Fatalf("switch to terminal 1 should hide second terminal body:\n%s", view)
	}
}

func TestModel_EmbeddedTerminalDismissRenumbersSessionTabs(t *testing.T) {
	terms := map[string]*fakeEmbeddedTerminal{
		"codex-session-1": {lines: []string{"first output"}},
		"codex-session-2": {lines: []string{"second output"}},
		"codex-session-3": {lines: []string{"third output"}},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			return terms[ctx.ResumeSessionID], nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/one", Branch: "feature/one"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-2", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/two", Branch: "feature/two"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-3", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/three", Branch: "feature/three"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	terms["codex-session-2"].state = "exited"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	view := m.View()
	for _, want := range []string{"1 codex feature/one running", "2 codex feature/three running", "first output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("renumbered session terminal view missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"3 codex", "second output"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("dismissed session terminal should not remain visible with %q:\n%s", unwanted, view)
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if view = m.View(); !strings.Contains(view, "third output") || strings.Contains(view, "first output") {
		t.Fatalf("switching to renumbered terminal 2 should show former third terminal:\n%s", view)
	}

	terms["codex-session-1"].state = "exited"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	view = m.View()
	for _, want := range []string{"1 codex feature/three running", "third output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("closing first session terminal should promote former second tab to 1:\n%s", view)
		}
	}
	if strings.Contains(view, "2 codex") {
		t.Fatalf("session tabs should remain contiguous after closing first tab:\n%s", view)
	}
}

func TestModel_EmbeddedTerminalPrefixDismissesExitedTerminal(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"done"}, state: "exited"}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	view := m.View()
	if strings.Contains(view, "done") || strings.Contains(view, "1 codex") {
		t.Fatalf("dismissed terminal should be removed:\n%s", view)
	}
	if !strings.Contains(view, "Provider") {
		t.Fatalf("with no embedded terminals, sessions table should return:\n%s", view)
	}
}

func TestModel_EmbeddedTerminalPrefixConfirmsRunningTerminate(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{lines: []string{"pty-live-output"}, state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected terminate confirmation, got %d", m.Overlay())
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected terminate confirmation command")
	}
	m, _ = update(m, cmd())

	if fakeTerm.State() != "terminated" {
		t.Fatalf("terminal state = %q, want terminated", fakeTerm.State())
	}
	if view := m.View(); strings.Contains(view, "pty-live-output") || strings.Contains(view, "1 codex") {
		t.Fatalf("terminated terminal should be dismissed:\n%s", view)
	}
}

func TestModel_EmbeddedTerminalQuitConfirmsAndTerminatesRunningPTYs(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatalf("ctrl+c should go to the PTY, got command %T", cmd)
	}
	if !reflect.DeepEqual(fakeTerm.writes, []string{"\x03"}) {
		t.Fatalf("terminal writes = %#v, want ctrl-c", fakeTerm.writes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Fatalf("running terminal quit should open confirmation, got command %T", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected quit confirmation, got %d", m.Overlay())
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit confirmation command")
	}
	m, cmd = update(m, cmd())
	if cmd == nil {
		t.Fatal("expected tea.Quit after confirmed embedded terminal quit")
	}
	if fakeTerm.State() != "terminated" {
		t.Fatalf("terminal state = %q, want terminated", fakeTerm.State())
	}
}

func TestModel_EmbeddedTerminalResizeUpdatesAllPTYs(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 30})

	if len(fakeTerm.resizes) == 0 {
		t.Fatal("expected resize call on embedded terminal")
	}
	got := fakeTerm.resizes[len(fakeTerm.resizes)-1]
	if got[0] <= 0 || got[1] <= 0 {
		t.Fatalf("resize dimensions = %#v, want positive", got)
	}
}

func TestModel_EmbeddedTerminalResizeSkipsExitedPTYs(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "exited"}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 30})

	if len(fakeTerm.resizes) != 0 {
		t.Fatalf("exited terminal should not receive resize calls, got %#v", fakeTerm.resizes)
	}
}

func TestModel_EmbeddedTerminalStaleTickDoesNotDuplicateRepaintLoop(t *testing.T) {
	first := &fakeEmbeddedTerminal{}
	second := &fakeEmbeddedTerminal{}
	starts := 0
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			if starts == 1 {
				return first, nil
			}
			return second, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, firstTick := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if firstTick == nil {
		t.Fatal("expected first embedded terminal to schedule repaint tick")
	}

	first.state = "exited"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, secondTick := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if secondTick == nil {
		t.Fatal("expected reopened embedded terminal to schedule repaint tick")
	}

	m, staleCmd := update(m, firstTick())
	if staleCmd != nil {
		t.Fatalf("stale repaint tick should not reschedule, got %T", staleCmd)
	}
	_, liveCmd := update(m, secondTick())
	if liveCmd == nil {
		t.Fatal("current repaint tick should continue while terminal is active")
	}
}

func TestModel_EmbeddedTerminalTickStopsWhenAllPTYsExit(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{}
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})
	m, tick := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if tick == nil {
		t.Fatal("expected embedded terminal to schedule repaint tick")
	}

	fakeTerm.state = "exited"
	_, cmd := update(m, tick())
	if cmd != nil {
		t.Fatalf("exited embedded terminal should stop repaint loop, got %T", cmd)
	}
}

func TestModel_RKeyResumePrefersSessionCWD(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: "/state/approach/sessions/v1",
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			got = ctx
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{
			Provider:     sessions.ProviderClaude,
			SessionID:    "claude-session-1",
			LaunchID:     "old-launch",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/feat",
			CWD:          "/dev/alpha-worktrees/feat/subdir",
			Branch:       "feat",
			Commit:       "abc123",
			PlanID:       "plan-1",
			PlanPath:     "/state/approach/plans/plan-1/plan.md",
			FlowID:       "flow-1",
			FlowPhaseID:  "review-loop",
		},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("embedded session resume should schedule terminal repaint ticks")
	}

	if got.Command != "claude" ||
		got.ResumeSessionID != "claude-session-1" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha-worktrees/feat" ||
		got.WorkingDir != "/dev/alpha-worktrees/feat/subdir" ||
		got.Branch != "feat" ||
		got.Commit != "abc123" ||
		got.SessionStateRoot != "/state/approach/sessions/v1" ||
		got.PlanID != "plan-1" ||
		got.PlanPath != "/state/approach/plans/plan-1/plan.md" ||
		!got.Embedded {
		t.Fatalf("unexpected resume launch context: %#v", got)
	}
	if got.FlowID != "" || got.FlowPhaseID != "" {
		t.Fatalf("ordinary session resume should not export Flow metadata: %#v", got)
	}
	if got.LaunchID == "" || got.LaunchID == "old-launch" {
		t.Fatalf("expected fresh launch id, got %#v", got)
	}
}

func TestModel_RKeySessionResumeWithFlowMetadataRunFailureDoesNotUpdateFlow(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("embedded start failed")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{
			Provider:     sessions.ProviderCodex,
			SessionID:    "codex-session-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/feat",
			FlowID:       "flow-1",
			FlowPhaseID:  "review-loop",
		},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for embedded start failure")
	}
	if !strings.Contains(m.View(), "embedded start failed") {
		t.Fatalf("expected embedded start failure in status:\n%s", m.View())
	}
	if len(phaseUpdates) != 0 {
		t.Fatalf("ordinary session resume should not update Flow phase, got %#v", phaseUpdates)
	}
}

func TestModel_RKeyResumesSessionFromCWDWhenWorktreePathMissing(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			got = ctx
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", CWD: "/dev/alpha/subdir"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("embedded session resume should schedule terminal repaint ticks")
	}

	if got.Command != "codex" || got.ResumeSessionID != "codex-session-1" || got.WorktreePath != "" || got.WorkingDir != "/dev/alpha/subdir" || !got.Embedded {
		t.Fatalf("unexpected cwd fallback resume context: %#v", got)
	}
}

func TestModel_RKeyUsesCodexAppPreferenceForCodexSessionResume(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "9a0c8d4e-1111-2222-3333-abcdefabcdef", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected codex-app resume command")
	}
	_ = cmd()

	if got.Command != "codex-app" ||
		got.ResumeSessionID != "9a0c8d4e-1111-2222-3333-abcdefabcdef" ||
		got.WorktreePath != "" ||
		got.WorkingDir != "" {
		t.Fatalf("unexpected codex-app resume context: %#v", got)
	}
}

func TestModel_RKeyKeepsClaudeProviderWhenCodexAppPreferenceSelected(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, width, height int) (model.EmbeddedTerminal, error) {
			got = ctx
			return &fakeEmbeddedTerminal{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{
			Provider:     sessions.ProviderClaude,
			SessionID:    "claude-session-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha-worktrees/docs",
		},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("embedded claude resume should schedule terminal repaint ticks")
	}

	if got.Command != "claude" ||
		got.ResumeSessionID != "claude-session-1" ||
		got.WorktreePath != "/dev/alpha-worktrees/docs" ||
		got.WorkingDir != "/dev/alpha-worktrees/docs" {
		t.Fatalf("unexpected claude resume context with codex-app preference: %#v", got)
	}
}

func TestModel_RKeySessionResumeNoOpsOutsideSessionSelection(t *testing.T) {
	called := false
	m := model.NewWithOptions(testRepos(), model.Options{
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			called = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // plans: r has no meaning here

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd != nil {
		t.Fatalf("expected r outside sessions to no-op, got %T", cmd)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd != nil {
		t.Fatalf("expected r with no selected session to no-op, got %T", cmd)
	}
	if called {
		t.Fatal("expected no launcher calls")
	}
}

func TestModel_RKeyResumeMissingPathShowsStatus(t *testing.T) {
	called := false
	m := model.NewWithOptions(testRepos(), model.Options{
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			called = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for missing resume path")
	}
	if called {
		t.Fatal("expected missing path not to call launcher")
	}
	if !strings.Contains(m.View(), "Session has no worktree path or cwd") {
		t.Fatal("expected missing resume path status")
	}
}

func TestModel_RKeyResumeBlankSessionIDShowsStatus(t *testing.T) {
	called := false
	m := model.NewWithOptions(testRepos(), model.Options{
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			called = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderClaude, SessionID: "   ", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for blank session id")
	}
	if called {
		t.Fatal("expected blank session id not to call launcher")
	}
	if !strings.Contains(m.View(), "Session has no provider session ID") {
		t.Fatal("expected missing session id status")
	}
}

func TestModel_InlineSessionResumeBlankSessionIDShowsStatus(t *testing.T) {
	called := false
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderClaude,
				SessionID:    "   ",
				RepoPath:     filter.RepoPath,
				WorktreePath: filter.WorktreePath,
				Branch:       "feature/inline",
			}}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			called = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, cmd())

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected status expiry command for blank inline session id")
	}
	if called {
		t.Fatal("expected blank inline session id not to call launcher")
	}
	if !strings.Contains(m.View(), "Session has no provider session ID") {
		t.Fatal("expected missing session id status")
	}
}

func TestModel_XKeyOpensInlineSessionsForSelectedWorktree(t *testing.T) {
	var gotFilter sessions.SessionFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			gotFilter = filter
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderCodex,
				SessionID:    "codex-inline-1",
				RepoPath:     filter.RepoPath,
				WorktreePath: filter.WorktreePath,
				Branch:       "feature/inline",
				Status:       "ended",
				Summary:      "Inline worktree sessions",
			}}, nil
		},
	})
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("expected inline session fetch command")
	}
	msg := cmd()
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" || gotFilter.WorktreePath != "/dev/alpha-worktrees/inline" {
		t.Fatalf("SessionFilter = %#v, want repo and selected worktree", gotFilter)
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("Mode() = %d, want worktrees", m.Mode())
	}
	view := m.View()
	for _, want := range []string{"Sessions", "codex", "feature/inline", "ended", "Inline worktree sessions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("inline sessions view missing %q:\n%s", want, view)
		}
	}
	if got := m.Sessions(); len(got) != 0 {
		t.Fatalf("full sessions pane should stay empty, got %#v", got)
	}
}

func TestModel_ArrowKeysSelectInlineWorktreeSessions(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{
				{Provider: sessions.ProviderCodex, SessionID: "codex-inline-1", RepoPath: filter.RepoPath, WorktreePath: filter.WorktreePath, Branch: "first"},
				{Provider: sessions.ProviderClaude, SessionID: "claude-inline-2", RepoPath: filter.RepoPath, WorktreePath: filter.WorktreePath, Branch: "second"},
			}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, cmd())

	worktreeSelected := m.WorktreeSelected()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	if m.WorktreeSelected() != worktreeSelected {
		t.Fatalf("WorktreeSelected() = %d, want %d", m.WorktreeSelected(), worktreeSelected)
	}
	if m.WorktreeSessionSelected() != 1 {
		t.Fatalf("WorktreeSessionSelected() = %d, want 1", m.WorktreeSessionSelected())
	}
	got := m.WorktreeSessions()
	if len(got) != 2 || got[1].SessionID != "claude-inline-2" {
		t.Fatalf("WorktreeSessions() = %#v, want second session selectable", got)
	}
}

func TestModel_EnterResumesInlineWorktreeSession(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: "/state/approach/sessions/v1",
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderClaude,
				SessionID:    "claude-inline-1",
				LaunchID:     "old-launch",
				RepoPath:     filter.RepoPath,
				WorktreePath: filter.WorktreePath,
				CWD:          filter.WorktreePath + "/subdir",
				Branch:       "feature/inline",
				Commit:       "abc123",
				PlanID:       "plan-1",
				PlanPath:     "/state/approach/plans/plan-1/plan.md",
				FlowID:       "flow-1",
				FlowPhaseID:  "implementation",
			}}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, cmd())

	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected inline session resume command")
	}
	msg, ok := cmd().(model.AgentResultMsg)
	if !ok {
		t.Fatalf("expected AgentResultMsg from inline resume command, got %T", msg)
	}
	if msg.Err != "" {
		t.Fatalf("expected successful inline resume command, got %q", msg.Err)
	}
	if got.Command != "claude" ||
		got.ResumeSessionID != "claude-inline-1" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha-worktrees/inline" ||
		got.WorkingDir != "/dev/alpha-worktrees/inline/subdir" ||
		got.Branch != "feature/inline" ||
		got.Commit != "abc123" ||
		got.SessionStateRoot != "/state/approach/sessions/v1" ||
		got.PlanID != "plan-1" ||
		got.PlanPath != "/state/approach/plans/plan-1/plan.md" {
		t.Fatalf("unexpected inline resume launch context: %#v", got)
	}
	if got.FlowID != "" || got.FlowPhaseID != "" {
		t.Fatalf("inline session resume should not export Flow metadata: %#v", got)
	}
	if got.LaunchID == "" || got.LaunchID == "old-launch" {
		t.Fatalf("expected fresh launch id, got %#v", got)
	}
}

func TestModel_FilteringWorktreesClosesInlineSessions(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderCodex,
				SessionID:    "codex-inline-1",
				RepoPath:     filter.RepoPath,
				WorktreePath: filter.WorktreePath,
				Branch:       "feature/inline",
				Summary:      "Inline worktree sessions",
			}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, cmd())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("main") {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if got := m.WorktreeSessions(); len(got) != 0 {
		t.Fatalf("inline sessions should close after filtering worktrees, got %#v", got)
	}
	if strings.Contains(m.View(), "Inline worktree sessions") {
		t.Fatalf("inline session row should disappear after filtering:\n%s", m.View())
	}
}

func TestModel_InlineWorktreeSessionFetchErrorShowsStatus(t *testing.T) {
	calls := 0
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			calls++
			if calls == 1 {
				return nil, nil
			}
			return nil, errors.New("session store offline")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("expected inline session fetch command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("expected second inline session fetch command")
	}
	m, _ = update(m, cmd())

	if !strings.Contains(m.View(), "failed to load worktree sessions: session store offline") {
		t.Fatalf("expected inline fetch error in status:\n%s", m.View())
	}
}

func TestModel_StaleInlineWorktreeSessionResultIsIgnored(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderCodex,
				SessionID:    "codex-stale",
				RepoPath:     filter.RepoPath,
				WorktreePath: filter.WorktreePath,
				Summary:      "stale inline result",
			}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
	}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("expected inline session fetch command")
	}
	staleMsg := cmd()

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, staleMsg)

	if got := m.WorktreeSessions(); len(got) != 0 {
		t.Fatalf("stale inline result should be ignored, got %#v", got)
	}
	if strings.Contains(m.View(), "stale inline result") {
		t.Fatalf("stale inline row should not render after mode switch:\n%s", m.View())
	}
}

func TestModel_SessionsFilterMatchesSessionFields(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/approach-worktrees/sessions", Branch: "main", Model: "gpt-5", Status: "ended", Summary: "Implement capture"},
		{Provider: sessions.ProviderClaude, SessionID: "claude-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha", Branch: "docs", Model: "opus", Status: "last_seen", Summary: "Write docs"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range []rune("gpt ended capture") {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	got := m.Sessions()
	if len(got) != 1 || got[0].SessionID != "codex-1" {
		t.Fatalf("filtered sessions = %#v", got)
	}
}

func TestModel_SessionTranscriptReadErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadTranscript: func(sessions.Provider, string) ([]sessions.TranscriptEvent, error) {
			return nil, errors.New("missing transcript")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected transcript fetch command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "missing transcript") {
		t.Fatalf("expected missing transcript status, got:\n%s", m.View())
	}
}

func TestModel_ShiftNOpensAgentWorktreeInput(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inWorktreesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected worktree input overlay, got %d", m.Overlay())
	}
	if !strings.Contains(m.View(), "launch agent") {
		t.Fatalf("expected agent worktree prompt in view")
	}
	if !strings.Contains(m.View(), "branch, tag") || !strings.Contains(m.View(), "new branch") {
		t.Fatalf("expected worktree input placeholder in view")
	}
	if got := m.InputMode(); got != modal.InputSingleLine {
		t.Fatalf("agent worktree input mode = %v, want single-line", got)
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd opening input, got %T", cmd)
	}
}

func TestModel_ShiftNWithNoSelectedAgentShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if cmd == nil {
		t.Fatal("expected status expiry command without selected agent")
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatal("expected unset-agent status")
	}
}

func TestModel_AgentWorktreeInputRequestsLaunch(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inWorktreesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat")})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create worktree command")
	}
	msg, ok := cmd().(model.WorktreeCreateFailedMsg)
	if !ok {
		t.Fatalf("expected fake repo create failure, got %T", msg)
	}
	if !msg.LaunchAgent {
		t.Fatal("expected create failure to preserve launch-agent mode")
	}
}

func TestModel_WorktreeCreatedWithLaunchRequestsAgent(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m, cmd := update(m, model.WorktreeCreatedMsg{RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/feat", Branch: "feat", LaunchAgent: true})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("expected mode worktrees after create, got %d", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected batch command after create+launch")
	}
	if got.WorktreePath != "/dev/alpha-worktrees/feat" || got.Branch != "feat" {
		t.Fatalf("expected launch from created worktree on feat, got %#v", got)
	}
}

func TestModel_WorktreeCreatedWithLaunchDoesNotReuseOldBranchForDetachedRef(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	_, cmd := update(m, model.WorktreeCreatedMsg{RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktrees/v1.0.0", LaunchAgent: true})
	if cmd == nil {
		t.Fatal("expected batch command after create+launch")
	}
	if got.WorktreePath != "/dev/alpha-worktrees/v1.0.0" || got.Branch != "" {
		t.Fatalf("expected detached launch without stale branch, got %#v", got)
	}
}

func TestModel_AgentWorktreeCreateFailedReopensAgentPrompt(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.WorktreeCreateFailedMsg{RepoPath: "/dev/alpha", Input: "feat", Err: "boom", LaunchAgent: true})
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected input overlay, got %d", m.Overlay())
	}
	if !strings.Contains(m.View(), "launch agent") {
		t.Fatal("expected agent prompt after create failure")
	}
	if m.WorktreeInput() != "feat" || m.WorktreeInputErr() != "boom" {
		t.Fatalf("expected restored input/error, got input=%q err=%q", m.WorktreeInput(), m.WorktreeInputErr())
	}
}

// --- Root branch undeletable ---

func TestModel_DKeyNoOpOnRootBranch(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	branches := []gitquery.Branch{
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
		{Name: "feat"},
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})
	m = enableDestructive(m)

	// Cursor at root branch (pinned to index 0) — d should be no-op
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("d on root branch should be no-op, got overlay %d", m.Overlay())
	}

	// Navigate to feat (index 1) — d should open confirm
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("d on non-root branch should open confirm, got overlay %d", m.Overlay())
	}
}

// --- Worktree delete ---

// inWorktreesMode switches to right pane in worktrees mode (mode 1, the default).
func inWorktreesMode(m model.Model) model.Model {
	return inRightPane(m)
}

func modelWithWorktrees(wts []gitquery.Worktree) model.Model {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m = enableDestructive(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	return m
}

func TestModel_DKeyNoOpOnRootWorktree(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	})
	// Cursor at root worktree (index 0) — d should be no-op
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("d on root worktree should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_DKeyNoOpOnStaleWorktree(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/gone", BranchName: "stale-branch", Stale: true},
	})
	// Navigate to stale worktree (index 1)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("d on stale worktree should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_DKeyNoOpOnLockedWorktree(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-locked", BranchName: "locked", Locked: true},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("d on locked worktree should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_DKeyOnWorktreeRequiresDestructiveMode(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	// destructive mode NOT enabled
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("d without destructive mode should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_WorktreeRemoveFailReturnsDeleteFailedMsg(t *testing.T) {
	// Fake repo path → RemoveWorktree will fail → should return DeleteFailedMsg
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected cmd from confirm, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.DeleteFailedMsg); !ok {
		t.Fatalf("expected DeleteFailedMsg on fake-path failure, got %T", msg)
	}
}

func TestModel_WorktreeForceRemoveReturnsWorktreeRemovedMsg(t *testing.T) {
	// DeleteFailedMsg with SuccessMsg set → force confirm → returns SuccessMsg type
	m := model.New(testRepos())
	m, _ = update(m, model.DeleteFailedMsg{
		RepoPath:    "/dev/alpha",
		Target:      "/dev/alpha-feat",
		ForceAction: func() error { return nil },
		SuccessMsg:  model.WorktreeRemovedMsg{RepoPath: "/dev/alpha", BranchName: "feat"},
	})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected cmd from force confirm, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.WorktreeRemovedMsg); !ok {
		t.Fatalf("expected WorktreeRemovedMsg after force remove, got %T", msg)
	}
}

func TestModel_DKeyOnWorktreeShowsConfirm(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	})
	// Navigate to non-root worktree (index 1)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayConfirm {
		t.Errorf("d on non-root worktree should open confirm, got overlay %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "/dev/alpha-feat") {
		t.Errorf("confirm prompt should contain worktree path, got %q", m.ConfirmPrompt())
	}
}

func TestModel_UKeyOnLockedWorktreeFiresUnlockCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-locked", BranchName: "locked", Locked: true},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("u should not open an overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected unlock cmd for locked worktree")
	}
}

func TestModel_UKeyUnlockFailureReturnsFailureMsg(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-locked", BranchName: "locked", Locked: true},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("expected unlock cmd")
	}
	msg := cmd()
	if _, ok := msg.(model.WorktreeUnlockFailedMsg); !ok {
		t.Fatalf("expected WorktreeUnlockFailedMsg for failed unlock, got %T", msg)
	}
}

func TestModel_UKeyOnUnlockedWorktreeIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("u on unlocked worktree should not open overlay, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Fatal("expected nil cmd for unlocked worktree")
	}
}

func TestModel_UKeyOutsideWorktreesModeIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("u outside worktrees mode should not open overlay, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Fatal("expected nil cmd outside worktrees mode")
	}
}

func TestModel_UKeyOnLockedMainWorktreeFiresUnlockCmd(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Locked: true},
	}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("expected unlock cmd for locked main worktree")
	}
}

func TestModel_WorktreeUnlockedMsgRefetchesWorktrees(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)

	_, cmd := update(m, model.WorktreeUnlockedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd after unlock")
	}
}

func TestModel_StaleWorktreeUnlockedMsgIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)

	_, cmd := update(m, model.WorktreeUnlockedMsg{RepoPath: "/dev/alpha"})
	if cmd != nil {
		t.Fatal("expected stale unlock result to be ignored")
	}
}

// --- Reflog mode actions ---

func modelInReflogWithEntries() model.Model {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()})
	return m
}

func TestModel_DKeyNoOpInReflogMode(t *testing.T) {
	m := modelInReflogWithEntries()
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone in reflog mode, got %d", m.Overlay())
	}
}

func TestModel_YKeyCopiesHashInReflogMode(t *testing.T) {
	m := modelInReflogWithEntries()
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for y key in reflog mode")
	}
}

func TestModel_YKeyNoOpWithNoReflogs(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Errorf("expected nil cmd for y key with no reflogs, got %T", cmd)
	}
}

func TestModel_EnterInReflogFetchesDiffWithoutOverlay(t *testing.T) {
	m := modelInReflogWithEntries()
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected no overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchReflogDiff cmd, got nil")
	}
}

func TestModel_ReflogDiffResultStoresDiff(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: "diff --git a/f.txt"})
	if cmd == nil {
		t.Fatal("expected reflog diff pager command")
	}
	if len(paged) != 1 || paged[0] != "diff --git a/f.txt" {
		t.Fatalf("paged = %#v", paged)
	}
}

func TestModel_StaleReflogDiffResultDiscarded(t *testing.T) {
	m := modelInReflogWithEntries()
	m = selectBravo(m)
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "stale"})
}

func TestModel_TKeyNoOpInReflogMode(t *testing.T) {
	m := modelInReflogWithEntries()
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd != nil {
		t.Errorf("expected nil cmd for t key in reflog mode, got %T", cmd)
	}
}

func TestModel_CKeyNoOpInReflogMode(t *testing.T) {
	m := modelInReflogWithEntries()
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Errorf("expected nil cmd for c key in reflog mode, got %T", cmd)
	}
}

func TestModel_ReflogDiffResultWrongHashDiscarded(t *testing.T) {
	m := modelInReflogWithEntries()
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "wrong", Diff: "wrong diff"})
}

func TestModel_ReflogDiffFetchFailureMatchesHashAndRequest(t *testing.T) {
	m := modelInReflogWithEntries()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "reflog diff failed",
		Kind:        model.FetchReflogDiff,
		Mode:        ui.ModeReflog,
		DiffRequest: 1,
		Hash:        "wrong",
	})
	if strings.Contains(m.View(), "reflog diff failed") {
		t.Fatal("wrong-hash reflog diff failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "reflog diff failed",
		Kind:        model.FetchReflogDiff,
		Mode:        ui.ModeReflog,
		DiffRequest: 99,
		Hash:        "abc1234",
	})
	if strings.Contains(m.View(), "reflog diff failed") {
		t.Fatal("wrong-request reflog diff failure should be ignored")
	}

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "reflog diff failed",
		Kind:        model.FetchReflogDiff,
		Mode:        ui.ModeReflog,
		DiffRequest: 1,
		Hash:        "abc1234",
	})
	if !strings.Contains(m.View(), "reflog diff failed") {
		t.Fatal("matching reflog diff failure should show in status bar")
	}
}

func TestModel_EnterInReflogNoEntriesIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}
