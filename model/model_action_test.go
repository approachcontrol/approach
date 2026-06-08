package model_test

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

// --- Worktree diff (enter key in ModeWorktrees) ---

func TestModel_EnterOnDirtyWorktreeOpensDiffOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true, FilesChanged: 3},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeDiff {
		t.Errorf("expected OverlayWorktreeDiff, got %d", m.Overlay())
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

func TestModel_EnterOnLockedDirtyWorktreeOpensDiffOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Locked: true, Dirty: true, FilesChanged: 2},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeDiff {
		t.Errorf("expected OverlayWorktreeDiff for locked dirty worktree, got %d", m.Overlay())
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

func TestModel_WorktreeDiffResultStoresDiff(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "diff --git a/f.txt",
	})
	if m.OverlayDiff() != "diff --git a/f.txt" {
		t.Errorf("expected diff stored, got %q", m.OverlayDiff())
	}
}

func TestModel_WorktreeDiffFetchFailureCarriesIdentity(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/does-not-exist", BranchName: "main", Dirty: true},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeDiff {
		t.Fatalf("expected OverlayWorktreeDiff, got %d", m.Overlay())
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

func TestModel_MatchingWorktreeDiffFetchFailureShowsStatusWithoutOverwritingOverlay(t *testing.T) {
	m := model.New(testRepos())
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

	if m.Overlay() != ui.OverlayWorktreeDiff {
		t.Fatalf("expected overlay to remain open, got %d", m.Overlay())
	}
	if m.OverlayDiff() != "" {
		t.Fatalf("expected fetch failure not to overwrite overlay diff, got %q", m.OverlayDiff())
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
	if m.OverlayDiff() != "" {
		t.Fatalf("expected stale failure not to overwrite overlay diff, got %q", m.OverlayDiff())
	}
}

func TestModel_StaleWorktreeDiffResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Diff:         "stale",
	})
	if m.OverlayDiff() != "" {
		t.Errorf("expected stale worktree diff discarded, got %q", m.OverlayDiff())
	}
}

func TestModel_WorktreeDiffResultDiscardedIfWorktreePathChanged(t *testing.T) {
	m := model.New(testRepos())
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
		{Path: "/dev/alpha-feat", BranchName: "feat", Dirty: true},
	}
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Diff:         "wrong worktree",
	})
	if m.OverlayDiff() != "" {
		t.Errorf("expected diff discarded for wrong worktree path, got %q", m.OverlayDiff())
	}
}

func TestModel_WorktreeDiffResultAfterClosedOverlayIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})

	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "stale worktree diff",
	})

	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected overlay to remain closed, got %d", m.Overlay())
	}
	if m.OverlayDiff() != "" {
		t.Fatalf("expected closed overlay to ignore stale diff, got %q", m.OverlayDiff())
	}
}

func TestModel_WorktreeDiffResultFromOlderRequestIgnoredAfterReopen(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})  // request 1
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape}) // close before result arrives
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})  // request 2 for same target

	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  2,
		Diff:         "new diff",
	})
	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "stale old diff",
	})

	if m.OverlayDiff() != "new diff" {
		t.Fatalf("expected stale request ignored after reopen, got %q", m.OverlayDiff())
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
	if cmd != nil {
		t.Fatal("expected no command when no repos are visible")
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

	m, _ = update(m, model.VisibleRepoFetchStatusExpiredMsg{Request: 1, Text: "Fetched 3 visible repos"})
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

	m, _ = update(m, model.VisibleRepoFetchStatusFadeMsg{Request: 1, Text: "Fetched 3 visible repos", Step: 1})
	if m.TransientErrorFadeStep() != 1 {
		t.Fatalf("expected fade step 1, got %d", m.TransientErrorFadeStep())
	}
	if !strings.Contains(m.View(), "Fetched 3 visible repos") {
		t.Fatalf("fade should keep status visible, got:\n%s", m.View())
	}

	m, _ = update(m, model.VisibleRepoFetchStatusFadeMsg{Request: 1, Text: "Fetched 3 visible repos", Step: 2})
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
	m, _ = update(m, model.VisibleRepoFetchStatusFadeMsg{Request: 1, Text: "Fetched 3 visible repos", Step: 1})

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

	m, _ = update(m, model.VisibleRepoFetchStatusExpiredMsg{Request: 1, Text: "Fetched 3 visible repos"})
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
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0' + rune(tc.mode)}})

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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})

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

func TestModel_EnterOpensBranchDiffOverlayForDirtyWorktree(t *testing.T) {
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
	if m.Overlay() != ui.OverlayBranchDiff {
		t.Errorf("expected OverlayBranchDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchBranchDiff cmd, got nil")
	}
}

func TestModel_BranchDiffResultForWrongWorktreePathIgnored(t *testing.T) {
	m := model.New(testRepos())
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

	m, _ = update(m, model.BranchDiffResultMsg{
		RepoPath:    "/dev/alpha",
		BranchName:  "feat",
		DiffRequest: 1,
		Diff:        "missing path diff",
	})
	if m.OverlayDiff() != "" {
		t.Fatalf("expected missing worktree path diff ignored, got %q", m.OverlayDiff())
	}

	m, _ = update(m, model.BranchDiffResultMsg{
		RepoPath:     "/dev/alpha",
		BranchName:   "feat",
		WorktreePath: "/dev/elsewhere",
		DiffRequest:  1,
		Diff:         "wrong path diff",
	})
	if m.OverlayDiff() != "" {
		t.Fatalf("expected wrong worktree path diff ignored, got %q", m.OverlayDiff())
	}

	m, _ = update(m, model.BranchDiffResultMsg{
		RepoPath:     "/dev/alpha",
		BranchName:   "feat",
		WorktreePath: "/dev/alpha",
		DiffRequest:  1,
		Diff:         "matching path diff",
	})
	if m.OverlayDiff() != "matching path diff" {
		t.Fatalf("expected matching worktree path diff stored, got %q", m.OverlayDiff())
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
	if m.OverlayDiff() != "" {
		t.Fatalf("branch diff failure should not overwrite overlay diff, got %q", m.OverlayDiff())
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	return m
}

func TestModel_EnterInHistoryOpensCommitDiffOverlay(t *testing.T) {
	m := modelInHistoryWithCommits()
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayCommitDiff {
		t.Errorf("expected OverlayCommitDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchCommitDiff cmd, got nil")
	}
}

func TestModel_EnterInHistoryNoCommitsIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
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
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.CommitDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: "diff --git a/f.txt"})
	if m.OverlayDiff() != "diff --git a/f.txt" {
		t.Errorf("expected diff stored, got %q", m.OverlayDiff())
	}
}

func TestModel_StaleCommitDiffResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	m, _ = update(m, model.CommitDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "stale"})
	if m.OverlayDiff() != "" {
		t.Errorf("expected stale commit diff discarded, got %q", m.OverlayDiff())
	}
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
	if m.OverlayDiff() != "" {
		t.Fatalf("commit diff failure should not overwrite overlay diff, got %q", m.OverlayDiff())
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
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

// --- Stash overlay ---

func TestModel_EnterOpensOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayStashDiff {
		t.Errorf("expected OverlayStashDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchStashDiff cmd, got nil")
	}
}

func TestModel_StashDiffResultStoresDiff(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       0,
		DiffRequest: 1,
		Diff:        "missing identity diff",
	})
	if m.OverlayDiff() != "" {
		t.Fatalf("expected missing stash identity diff ignored, got %q", m.OverlayDiff())
	}
	stash := testStashes()[0]
	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       stash.Index,
		Date:        stash.Date,
		Message:     stash.Message,
		DiffRequest: 1,
		Diff:        "diff --git a/f.txt",
	})
	if m.OverlayDiff() != "diff --git a/f.txt" {
		t.Errorf("expected diff stored, got %q", m.OverlayDiff())
	}
}

func TestModel_StaleStashDiffDoesNotPopulateCommitOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, Diff: "stale stash diff"})

	if m.Overlay() != ui.OverlayCommitDiff {
		t.Fatalf("expected commit diff overlay to remain open, got %d", m.Overlay())
	}
	if m.OverlayDiff() != "" {
		t.Fatalf("expected stale stash diff ignored by commit overlay, got %q", m.OverlayDiff())
	}
}

func TestModel_StaleStashDiffForOldIndexIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEscape})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, _ = update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, Diff: "old index diff"})
	if m.OverlayDiff() != "" {
		t.Fatalf("expected old-index stash diff ignored, got %q", m.OverlayDiff())
	}

	currentStash := testStashes()[1]
	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       currentStash.Index,
		Date:        currentStash.Date,
		Message:     currentStash.Message,
		DiffRequest: 2,
		Diff:        "current index diff",
	})
	if m.OverlayDiff() != "current index diff" {
		t.Fatalf("expected current-index stash diff stored, got %q", m.OverlayDiff())
	}
}

func TestModel_StaleStashDiffForChangedIdentityIgnored(t *testing.T) {
	oldStash := gitquery.Stash{Index: 0, Date: "2026-03-18 10:00:00 -0700", Message: "old stash"}
	newStash := gitquery.Stash{Index: 0, Date: "2026-03-19 10:00:00 -0700", Message: "new stash"}

	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{oldStash}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{newStash}})

	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       oldStash.Index,
		Date:        oldStash.Date,
		Message:     oldStash.Message,
		DiffRequest: 1,
		Diff:        "old stash diff",
	})
	if m.OverlayDiff() != "" {
		t.Fatalf("expected changed stash identity to reject stale diff, got %q", m.OverlayDiff())
	}

	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       newStash.Index,
		Date:        newStash.Date,
		Message:     newStash.Message,
		DiffRequest: 1,
		Diff:        "new stash diff",
	})
	if m.OverlayDiff() != "new stash diff" {
		t.Fatalf("expected matching stash identity diff stored, got %q", m.OverlayDiff())
	}
}

func TestModel_StashDiffFetchFailureMatchesFullIdentity(t *testing.T) {
	oldStash := gitquery.Stash{Index: 0, Date: "2026-03-18 10:00:00 -0700", Message: "old stash"}
	newStash := gitquery.Stash{Index: 0, Date: "2026-03-19 10:00:00 -0700", Message: "new stash"}

	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
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
	if m.OverlayDiff() != "" {
		t.Fatalf("stash diff failure should not overwrite overlay diff, got %q", m.OverlayDiff())
	}
}

func TestModel_EscClosesOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Now close overlay with esc
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("esc with overlay open should not quit")
		}
	}
}

func TestModel_QClosesOverlayNotQuit(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Close with q
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected overlay closed, got %d", m.Overlay())
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("q with overlay open should not quit")
		}
	}
}

func TestModel_OverlayScrollUpDown(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	scrollStash := testStashes()[0]
	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       scrollStash.Index,
		Date:        scrollStash.Date,
		Message:     scrollStash.Message,
		DiffRequest: 1,
		Diff:        "line1\nline2\nline3",
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.OverlayScroll() != 1 {
		t.Errorf("expected scroll 1, got %d", m.OverlayScroll())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.OverlayScroll() != 0 {
		t.Errorf("expected scroll 0, got %d", m.OverlayScroll())
	}
	// Up at 0 stays 0
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.OverlayScroll() != 0 {
		t.Errorf("expected scroll clamped at 0, got %d", m.OverlayScroll())
	}
}

func TestModel_ModeKeysIgnoredInOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Press "1" — should not change mode
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.Mode() != 3 {
		t.Errorf("expected mode unchanged at 3 (stashes), got %d", m.Mode())
	}
	if m.Overlay() != ui.OverlayStashDiff {
		t.Errorf("expected overlay still open, got %d", m.Overlay())
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
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

func TestModel_ShiftDNoOpDuringDiffOverlay(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "main", IsWorktree: true, Dirty: true, WorktreePaths: []string{"/dev/alpha"}},
		},
	})
	// Open diff overlay
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() == ui.OverlayNone {
		t.Fatal("expected a diff overlay")
	}
	// Not in destructive mode; Shift+D should be ignored
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if m.Destructive() {
		t.Error("expected destructive to remain false during diff overlay")
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
		{name: "stashes", key: '3', mode: ui.ModeStashes},
		{name: "history", key: '4', mode: ui.ModeHistory},
		{name: "reflog", key: '5', mode: ui.ModeReflog},
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
		{name: "branches", key: '2', mode: ui.ModeBranches},
		{name: "stashes", key: '3', mode: ui.ModeStashes},
		{name: "history", key: '4', mode: ui.ModeHistory},
		{name: "reflog", key: '5', mode: ui.ModeReflog},
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
		t.Errorf("expected nil cmd, got %T", cmd)
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

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})  // left pane
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.Mode() != ui.ModeBranches {
		t.Errorf("confirm dialog should block mode switch, mode changed to %d", m.Mode())
	}
}

// --- Stash drop ---

func modelInStashesWithStashes() model.Model {
	m := model.New(testRepos())
	m = inRightPane(m)
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
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
			if m.Overlay() != ui.OverlayAgentSelect {
				t.Fatalf("expected agent select overlay, got %d", m.Overlay())
			}
			view := m.View()
			for _, want := range []string{"Choose interactive helper", "codex", "claude"} {
				if !strings.Contains(view, want) {
					t.Fatalf("expected agent select view to contain %q", want)
				}
			}
			if cmd != nil {
				t.Fatalf("expected nil cmd opening agent select, got %T", cmd)
			}
		})
	}
}

func TestModel_ShiftAAgentSelectPreselectsCurrentAgent(t *testing.T) {
	for _, tt := range []struct {
		name      string
		agent     string
		wantCodex bool
	}{
		{name: "unset", wantCodex: true},
		{name: "invalid", agent: "codex-app", wantCodex: true},
		{name: "claude", agent: "claude", wantCodex: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: tt.agent})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
			view := m.View()

			if tt.wantCodex {
				if !strings.Contains(view, "> codex") {
					t.Fatalf("expected codex selected in view:\n%s", view)
				}
				return
			}
			if !strings.Contains(view, "> claude") {
				t.Fatalf("expected claude selected in view:\n%s", view)
			}
		})
	}
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
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save-agent command")
	}
	m, _ = update(m, cmd())
	if m.AgentCommand() != "claude" {
		t.Fatalf("expected session agent claude, got %q", m.AgentCommand())
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

func TestModel_AKeyLaunchesAgentFromWorktree(t *testing.T) {
	var gotPath, gotCommand string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			gotPath = ctx.WorktreePath
			gotCommand = ctx.Command
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
		SessionStateRoot: "/state/wtui/sessions/v1",
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
		got.SessionStateRoot != "/state/wtui/sessions/v1" {
		t.Fatalf("unexpected launch context: %#v", got)
	}
	if got.LaunchID == "" {
		t.Fatalf("expected launch ID in context: %#v", got)
	}
}

func TestModel_AKeyLaunchesAgentFromCheckedOutBranch(t *testing.T) {
	var gotPath string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "claude",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			gotPath = ctx.WorktreePath
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
	if cmd != nil {
		t.Fatalf("expected nil cmd without selected agent, got %T", cmd)
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
	if cmd != nil {
		t.Fatalf("expected nil cmd when launch cannot be built, got %T", cmd)
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

	m, _ = update(m, model.AgentResultMsg{LaunchContext: ctx})
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

	m, _ = update(m, model.AgentResultMsg{LaunchContext: ctx})
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

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	if cmd == nil {
		t.Fatal("expected initial sessions fetch")
	}
	m, _ = update(m, cmd())
	if got := m.Sessions(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Sessions() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
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
	m, _ = update(m, cmd())
	if got := m.Sessions(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("refetched Sessions() = %#v", got)
	}
	if len(filters) != 2 || filters[0].RepoPath != "/dev/alpha" || filters[1].RepoPath != "/dev/bravo" {
		t.Fatalf("session filters = %#v", filters)
	}
}

func TestModel_EnterOnSessionOpensTranscriptOverlay(t *testing.T) {
	var gotProvider sessions.Provider
	var gotSessionID string
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadTranscript: func(provider sessions.Provider, sessionID string) ([]sessions.TranscriptEvent, error) {
			gotProvider = provider
			gotSessionID = sessionID
			return []sessions.TranscriptEvent{
				{Role: "user", Kind: "message", Text: "Implement sessions"},
				{Role: "assistant", Kind: "message", Text: "Done"},
			}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlaySessionTranscript {
		t.Fatalf("expected OverlaySessionTranscript, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected transcript fetch command")
	}
	msg, ok := cmd().(model.SessionTranscriptResultMsg)
	if !ok {
		t.Fatalf("expected SessionTranscriptResultMsg, got %T", msg)
	}
	m, _ = update(m, msg)

	if gotProvider != sessions.ProviderCodex || gotSessionID != "codex-1" {
		t.Fatalf("reader got provider=%q session=%q", gotProvider, gotSessionID)
	}
	if diff := m.OverlayDiff(); !strings.Contains(diff, "user: Implement sessions") || !strings.Contains(diff, "assistant: Done") {
		t.Fatalf("unexpected transcript overlay text: %q", diff)
	}
}

func TestModel_SKeyShowsSelectedSessionSummary(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Summary: "first line\nsecond line\nthird line"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd != nil {
		t.Fatalf("expected summary overlay to open without command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayPlanText {
		t.Fatalf("expected text overlay for session summary, got %d", m.Overlay())
	}
	if got := m.OverlayText(); got != "first line\nsecond line\nthird line" {
		t.Fatalf("summary overlay text = %q", got)
	}
}

func TestModel_SKeyEmptySessionSummaryShowsFallback(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd != nil {
		t.Fatalf("expected summary overlay to open without command, got %T", cmd)
	}
	if got := m.OverlayText(); got != "No summary" {
		t.Fatalf("empty summary overlay text = %q", got)
	}
}

func TestModel_SKeySessionSummaryNoOpsOutsideSessionSelection(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}); cmd != nil {
		t.Fatalf("expected s outside sessions to no-op, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay outside sessions, got %d", m.Overlay())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd != nil {
		t.Fatalf("expected y outside copyable modes to no-op, got %T", cmd)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: ui.BranchContentOverhead + 3})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	if strings.Contains(view, "one first") {
		t.Fatalf("expected first session row to scroll offscreen:\n%s", view)
	}
	if !strings.Contains(view, "two first two second") {
		t.Fatalf("expected multiline summaries to collapse whitespace within one row:\n%s", view)
	}
}

func TestModel_RKeyResumePrefersSessionCWD(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: "/state/wtui/sessions/v1",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
			PlanPath:     "/state/wtui/plans/plan-1/plan.md",
			FlowID:       "flow-1",
			FlowPhaseID:  "review-loop",
		},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected session resume command")
	}
	msg, ok := cmd().(model.AgentResultMsg)
	if !ok {
		t.Fatalf("expected AgentResultMsg from resume command, got %T", msg)
	}
	if msg.Err != "" {
		t.Fatalf("expected successful resume command, got %q", msg.Err)
	}
	if msg.LaunchContext != got {
		t.Fatalf("AgentResultMsg context = %#v, want launched context %#v", msg.LaunchContext, got)
	}

	if got.Command != "claude" ||
		got.ResumeSessionID != "claude-session-1" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha-worktrees/feat" ||
		got.WorkingDir != "/dev/alpha-worktrees/feat/subdir" ||
		got.Branch != "feat" ||
		got.Commit != "abc123" ||
		got.SessionStateRoot != "/state/wtui/sessions/v1" ||
		got.PlanID != "plan-1" ||
		got.PlanPath != "/state/wtui/plans/plan-1/plan.md" ||
		got.FlowID != "flow-1" ||
		got.FlowPhaseID != "review-loop" {
		t.Fatalf("unexpected resume launch context: %#v", got)
	}
	if got.LaunchID == "" || got.LaunchID == "old-launch" {
		t.Fatalf("expected fresh launch id, got %#v", got)
	}
}

func TestModel_RKeyResumesSessionFromCWDWhenWorktreePathMissing(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha", CWD: "/dev/alpha/subdir"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected session resume command")
	}
	_ = cmd()

	if got.Command != "codex" || got.ResumeSessionID != "codex-session-1" || got.WorktreePath != "" || got.WorkingDir != "/dev/alpha/subdir" {
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
		t.Fatal("expected claude resume command")
	}
	_ = cmd()

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

	if _, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd != nil {
		t.Fatalf("expected r outside sessions to no-op, got %T", cmd)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-session-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		t.Fatalf("expected no command for missing resume path, got %T", cmd)
	}
	if called {
		t.Fatal("expected missing path not to call launcher")
	}
	if !strings.Contains(m.View(), "Session has no worktree path or cwd") {
		t.Fatal("expected missing resume path status")
	}
}

func TestModel_SessionsFilterMatchesSessionFields(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/wtui-worktrees/sessions", Branch: "main", Model: "gpt-5", Status: "ended", Summary: "Implement capture"},
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlaySessionTranscript {
		t.Fatalf("expected transcript overlay, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected transcript fetch command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "missing transcript") {
		t.Fatalf("expected missing transcript status, got:\n%s", m.View())
	}
	if m.OverlayDiff() != "" {
		t.Fatalf("expected blank transcript overlay on error, got %q", m.OverlayDiff())
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
	if !strings.Contains(m.View(), "branch, tag, or new branch name") {
		t.Fatalf("expected worktree input placeholder in view")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd opening input, got %T", cmd)
	}
}

func TestModel_ShiftNWithNoSelectedAgentShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = inWorktreesMode(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd without selected agent, got %T", cmd)
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Errorf("expected nil cmd for y key with no reflogs, got %T", cmd)
	}
}

func TestModel_EnterInReflogOpensReflogDiffOverlay(t *testing.T) {
	m := modelInReflogWithEntries()
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayReflogDiff {
		t.Errorf("expected OverlayReflogDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchReflogDiff cmd, got nil")
	}
}

func TestModel_ReflogDiffResultStoresDiff(t *testing.T) {
	m := modelInReflogWithEntries()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: "diff --git a/f.txt"})
	if m.OverlayDiff() != "diff --git a/f.txt" {
		t.Errorf("expected diff stored, got %q", m.OverlayDiff())
	}
}

func TestModel_StaleReflogDiffResultDiscarded(t *testing.T) {
	m := modelInReflogWithEntries()
	m = selectBravo(m)
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "stale"})
	if m.OverlayDiff() != "" {
		t.Errorf("expected stale reflog diff discarded, got %q", m.OverlayDiff())
	}
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
	if m.OverlayDiff() != "" {
		t.Errorf("expected wrong-hash reflog diff discarded, got %q", m.OverlayDiff())
	}
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
	if m.OverlayDiff() != "" {
		t.Fatalf("reflog diff failure should not overwrite overlay diff, got %q", m.OverlayDiff())
	}
}

func TestModel_EnterInReflogNoEntriesIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}
