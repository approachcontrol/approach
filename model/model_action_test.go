package model_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
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
	if m.Overlay() != model.OverlayWorktreeDiff {
		t.Errorf("expected OverlayWorktreeDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktreeDiff cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.WorktreeDiffResultMsg); !ok {
		t.Errorf("expected WorktreeDiffResultMsg, got %T", msg)
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected OverlayNone for stale worktree, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Error("expected nil cmd for stale worktree")
	}
}

func TestModel_EnterOnEmptyWorktreeListIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected OverlayNone with no worktrees, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Error("expected nil cmd with no worktrees")
	}
}

func TestModel_WorktreeDiffResultStoresDiff(t *testing.T) {
	m := model.New(testRepos())
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", Dirty: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Diff:         "diff --git a/f.txt",
	})
	if m.OverlayDiff() != "diff --git a/f.txt" {
		t.Errorf("expected diff stored, got %q", m.OverlayDiff())
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
	msg := cmd()
	diffMsg, ok := msg.(model.BranchDiffResultMsg)
	if !ok {
		t.Fatalf("expected BranchDiffResultMsg, got %T", msg)
	}
	if diffMsg.BranchName != "dirty-root" {
		t.Errorf("expected dirty-root, got %q", diffMsg.BranchName)
	}

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
	if m.Overlay() != model.OverlayBranchDiff {
		t.Errorf("expected OverlayBranchDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchBranchDiff cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.BranchDiffResultMsg); !ok {
		t.Errorf("expected BranchDiffResultMsg, got %T", msg)
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayCommitDiff {
		t.Errorf("expected OverlayCommitDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchCommitDiff cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.CommitDiffResultMsg); !ok {
		t.Errorf("expected CommitDiffResultMsg, got %T", msg)
	}
}

func TestModel_EnterInHistoryNoCommitsIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	// No commits loaded
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}

func TestModel_CommitDiffResultStoresDiff(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, model.CommitDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "diff --git a/f.txt"})
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

func TestModel_YKeyCopiesHashInHistoryMode(t *testing.T) {
	m := modelInHistoryWithCommits()
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Error("expected non-nil cmd for y key in mode 3")
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

func TestModel_DKeyNoOpInHistoryMode(t *testing.T) {
	m := modelInHistoryWithCommits()
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayStashDiff {
		t.Errorf("expected OverlayStashDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchStashDiff cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.StashDiffResultMsg); !ok {
		t.Errorf("expected StashDiffResultMsg, got %T", msg)
	}
}

func TestModel_StashDiffResultStoresDiff(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, Diff: "diff --git a/f.txt"})
	if m.OverlayDiff() != "diff --git a/f.txt" {
		t.Errorf("expected diff stored, got %q", m.OverlayDiff())
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	m, _ = update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, Diff: "line1\nline2\nline3"})

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
	if m.Overlay() != model.OverlayStashDiff {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayConfirm {
		t.Errorf("expected OverlayConfirm in destructive mode, got %d", m.Overlay())
	}
}

func TestModel_DKeyNoOpInReadOnlyModeStashes(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() == model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("detached removal should not show branch confirm, got overlay %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd after detached removal, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.WorktreeResultMsg); !ok {
		t.Errorf("expected WorktreeResultMsg from refetch, got %T", msg)
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
	if m.Overlay() != model.OverlayConfirm {
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
	msg := cmd()
	if _, ok := msg.(model.WorktreeResultMsg); !ok {
		t.Errorf("expected WorktreeResultMsg from background refetch, got %T", msg)
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("p without destructive mode should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_PKeyNoOpOnNonStaleWorktree(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != model.OverlayNone {
		t.Errorf("p on non-stale worktree should be no-op, got overlay %d", m.Overlay())
	}
}

func TestModel_PKeyOnStaleWorktreeShowsConfirm(t *testing.T) {
	m := modelWithWorktrees([]gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale-branch", Stale: true},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != model.OverlayConfirm {
		t.Errorf("p on stale worktree should open confirm, got overlay %d", m.Overlay())
	}
	if !strings.Contains(m.ConfirmPrompt(), "Prune") {
		t.Errorf("confirm prompt should mention Prune, got %q", m.ConfirmPrompt())
	}
}

func TestModel_PKeyNoOpInBranchesMode(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m = enableDestructive(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m.Overlay() != model.OverlayNone {
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
	m, cmd := update(m, model.WorktreePrunedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd after prune, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.WorktreeResultMsg); !ok {
		t.Errorf("expected WorktreeResultMsg from refetch, got %T", msg)
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
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
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected OverlayNone when no branches, got %d", m.Overlay())
	}
}

func TestModel_ConfirmCancelEsc(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected overlay closed on n, got %d", m.Overlay())
	}
}

func TestModel_ConfirmYClosesOverlayAndReturnsCmd(t *testing.T) {
	m := modelWithDeletableBranch()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Mode() != model.ModeBranches {
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
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() != model.OverlayNone {
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
	msg := cmd()
	if _, ok := msg.(model.StashDroppedMsg); !ok {
		t.Errorf("expected StashDroppedMsg, got %T", msg)
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
	if m.Overlay() != model.OverlayNone {
		t.Errorf("d on root branch should be no-op, got overlay %d", m.Overlay())
	}

	// Navigate to feat (index 1) — d should open confirm
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayConfirm {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayNone {
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
	msg := cmd()
	if _, ok := msg.(model.WorktreeResultMsg); !ok {
		t.Errorf("expected WorktreeResultMsg from refetch, got %T", msg)
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
	if m.Overlay() != model.OverlayNone {
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
	if m.Overlay() != model.OverlayReflogDiff {
		t.Errorf("expected OverlayReflogDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchReflogDiff cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(model.ReflogDiffResultMsg); !ok {
		t.Errorf("expected ReflogDiffResultMsg, got %T", msg)
	}
}

func TestModel_ReflogDiffResultStoresDiff(t *testing.T) {
	m := modelInReflogWithEntries()
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "diff --git a/f.txt"})
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

func TestModel_EnterInReflogNoEntriesIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != model.OverlayNone {
		t.Errorf("expected OverlayNone, got %d", m.Overlay())
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd)
	}
}
