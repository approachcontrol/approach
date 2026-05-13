package model_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
)

func TestModel_ViewShowsBranchData(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inBranchesMode(m)
	branches := []gitquery.Branch{
		{Name: "main", HasUpstream: true},
		{Name: "feature/auth", HasUpstream: true, Ahead: 2, Behind: 1,
			Unpushed: []string{"abc1234 Fix login bug", "def5678 Add profile page"}},
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	view := m.View()
	if !strings.Contains(view, "main") {
		t.Error("view should contain branch 'main'")
	}
	if !strings.Contains(view, "feature/auth") {
		t.Error("view should contain branch 'feature/auth'")
	}
	if !strings.Contains(view, "Fix login bug") {
		t.Error("view should contain unpushed commit message")
	}
	if !strings.Contains(view, "+2/-1") {
		t.Error("view should contain ahead/behind counts")
	}
}

func TestModel_ViewContainsExpectedContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	view := m.View()

	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if !strings.Contains(view, name) {
			t.Errorf("view should contain repo name %q", name)
		}
	}
	if !strings.Contains(view, "q/esc: quit") {
		t.Error("view should contain quit keybinding")
	}
}

func TestModel_ViewWorktreesModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Load branch data — worktrees mode should still show placeholder, not branches
	branches := []gitquery.Branch{{Name: "main", HasUpstream: true}}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	view := m.View()
	if !strings.Contains(view, "nothing here yet") {
		t.Error("ModeWorktrees should show placeholder even when branch data is loaded")
	}
	if strings.Contains(view, "main") {
		t.Error("ModeWorktrees should NOT show branch data")
	}
}

func TestModel_ViewStashesModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	view := m.View()
	if !strings.Contains(view, "nothing here yet") {
		t.Error("ModeStashes with no data should show placeholder")
	}
}

func TestModel_ViewModeHeaderShowsFiveModes(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	view := m.View()
	// Mode 1 (worktrees) active
	if !strings.Contains(view, "[1] worktrees") {
		t.Error("mode 1 active: right pane header should contain '[1] worktrees'")
	}
	if !strings.Contains(view, "2 branches") {
		t.Error("mode 1 active: right pane header should show inactive '2 branches'")
	}
	if !strings.Contains(view, "3 stashes") {
		t.Error("mode 1 active: right pane header should show inactive '3 stashes'")
	}
	if !strings.Contains(view, "4 history") {
		t.Error("mode 1 active: right pane header should show inactive '4 history'")
	}

	// Switch to mode 2 (branches)
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	view = m.View()
	if !strings.Contains(view, "[2] branches") {
		t.Error("mode 2 active: right pane header should contain '[2] branches'")
	}
	if !strings.Contains(view, "1 worktrees") {
		t.Error("mode 2 active: right pane header should show inactive '1 worktrees'")
	}

	// Switch to mode 3 (stashes)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	view = m.View()
	if !strings.Contains(view, "[3] stashes") {
		t.Error("mode 3 active: right pane header should contain '[3] stashes'")
	}

	// Switch to mode 4 (history)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	view = m.View()
	if !strings.Contains(view, "[4] history") {
		t.Error("mode 4 active: right pane header should contain '[4] history'")
	}
	if !strings.Contains(view, "1 worktrees") {
		t.Error("mode 4 active: right pane header should show inactive '1 worktrees'")
	}
	if !strings.Contains(view, "2 branches") {
		t.Error("mode 4 active: right pane header should show inactive '2 branches'")
	}
	if !strings.Contains(view, "3 stashes") {
		t.Error("mode 4 active: right pane header should show inactive '3 stashes'")
	}
	if !strings.Contains(view, "5 reflog") {
		t.Error("mode 4 active: right pane header should show inactive '5 reflog'")
	}

	// Switch to mode 5 (reflog)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	view = m.View()
	if !strings.Contains(view, "[5] reflog") {
		t.Error("mode 5 active: right pane header should contain '[5] reflog'")
	}
	if !strings.Contains(view, "4 history") {
		t.Error("mode 5 active: right pane header should show inactive '4 history'")
	}
}

func TestModel_ViewStatusBarShowsKeyHints(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	view := m.View()
	if !strings.Contains(view, "tab: pane") {
		t.Error("status bar should contain 'tab: pane' hint")
	}
}

func TestModel_ViewStashesModeShowsStashContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})

	view := m.View()
	if !strings.Contains(view, "WIP: feature A") {
		t.Error("view should contain stash message 'WIP: feature A'")
	}
	if !strings.Contains(view, "backup: old approach") {
		t.Error("view should contain stash message 'backup: old approach'")
	}
}

func TestModel_ViewOverlayShowsDiff(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.StashDiffResultMsg{RepoPath: "/dev/alpha", Index: 0, Diff: "diff --git a/f.txt\n--- a/f.txt\n+++ b/f.txt"})

	view := m.View()
	if !strings.Contains(view, "diff --git") {
		t.Error("overlay should show diff content")
	}
	if !strings.Contains(view, "esc") {
		t.Error("overlay should show esc hint")
	}
}

func TestModel_StatusBarStashesModeShowsStashKeys(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // stashes

	view := m.View()
	if !strings.Contains(view, "enter") {
		t.Error("stashes status bar should mention 'enter'")
	}
	if !strings.Contains(view, "↑/↓") {
		t.Error("stashes status bar should mention '↑/↓'")
	}
}

func TestModel_StatusBarStashesModeShowsDropHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}}) // enable destructive
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // stashes

	view := m.View()
	if !strings.Contains(view, "d: drop") {
		t.Error("stashes status bar should mention 'd: drop' in destructive mode")
	}
}

// --- Destructive mode view tests ---

func TestModel_ViewReadOnlyHidesDeleteHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inBranchesMode(m)

	view := m.View()
	if strings.Contains(view, "d: delete") {
		t.Error("read-only mode should NOT show 'd: delete'")
	}
}

func TestModel_ViewReadOnlyHidesDropHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // stashes

	view := m.View()
	if strings.Contains(view, "d: drop") {
		t.Error("read-only mode should NOT show 'd: drop'")
	}
}

func TestModel_ViewReadOnlyShowsDestructiveModeHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inBranchesMode(m)

	view := m.View()
	if !strings.Contains(view, "D: destructive mode") {
		t.Error("read-only mode should show 'D: destructive mode' hint")
	}
}

func TestModel_ViewDestructiveModeShowsDeleteHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inBranchesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	view := m.View()
	if !strings.Contains(view, "d: delete") {
		t.Error("destructive mode should show 'd: delete'")
	}
}

func TestModel_ViewHistoryModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})

	view := m.View()
	if !strings.Contains(view, "nothing here yet") {
		t.Error("history mode with no commits should show placeholder")
	}
}

func TestModel_ViewHistoryModeShowsCommitContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})

	view := m.View()
	if !strings.Contains(view, "Fix login bug") {
		t.Error("view should contain commit subject 'Fix login bug'")
	}
	if !strings.Contains(view, "alice") {
		t.Error("view should contain author 'alice'")
	}
}

func TestModel_StatusBarHistoryModeShowsHistoryKeys(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})

	view := m.View()
	if !strings.Contains(view, "enter: diff") {
		t.Error("mode 3 status bar should mention 'enter: diff'")
	}
	if !strings.Contains(view, "y: copy hash") {
		t.Error("mode 3 status bar should mention 'y: copy hash'")
	}
	if !strings.Contains(view, "t: terminal") {
		t.Error("mode 3 status bar should mention 't: terminal'")
	}
	if !strings.Contains(view, "c: code") {
		t.Error("mode 3 status bar should mention 'c: code'")
	}
}

func TestModel_ViewDestructiveModeHidesDestructiveHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inBranchesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	view := m.View()
	if strings.Contains(view, "D: destructive mode") {
		t.Error("destructive mode should NOT show 'D: destructive mode' hint")
	}
}

func TestModel_ViewWorktreesModeDestructiveShowsDeleteHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}}) // enable destructive
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	// Navigate to non-root worktree
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := m.View()
	if !strings.Contains(view, "d: delete") {
		t.Error("worktrees mode destructive non-stale should show 'd: delete'")
	}
	if strings.Contains(view, "p: prune") {
		t.Error("worktrees mode destructive non-stale should NOT show 'p: prune'")
	}
}

func TestModel_ViewWorktreesModeLockedHidesDeleteHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-locked", BranchName: "locked", Locked: true},
	}})

	view := m.View()
	if strings.Contains(view, "d: delete") {
		t.Error("locked worktree should not show 'd: delete'")
	}
}

func TestModel_ViewWorktreesModeLockedShowsUnlockHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-locked", BranchName: "locked", Locked: true},
	}})

	view := m.View()
	if !strings.Contains(view, "u: unlock") {
		t.Error("locked worktree should show 'u: unlock'")
	}
}

func TestModel_ViewWorktreesModeLockedStaleHidesDeleteAndPruneHints(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-gone", BranchName: "offline", Locked: true, Stale: true},
	}})

	view := m.View()
	for _, hint := range []string{"d: delete", "p: prune"} {
		if strings.Contains(view, hint) {
			t.Errorf("locked stale worktree should not show %q", hint)
		}
	}
}

func TestModel_ViewWorktreesModeDestructiveStaleShowsPruneHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}}) // enable destructive
	wts := []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale-branch", Stale: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	view := m.View()
	if !strings.Contains(view, "p: prune") {
		t.Error("worktrees mode destructive stale should show 'p: prune'")
	}
	if strings.Contains(view, "d: delete") {
		t.Error("worktrees mode destructive stale should NOT show 'd: delete'")
	}
}

func TestModel_ViewWorktreesModeReadOnlyShowsDestructiveHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	// Destructive mode NOT enabled
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	view := m.View()
	if !strings.Contains(view, "D: destructive mode") {
		t.Error("worktrees mode read-only should show 'D: destructive mode'")
	}
	if strings.Contains(view, "d: delete") {
		t.Error("worktrees mode read-only should NOT show 'd: delete'")
	}
}

func TestModel_ViewWorktreesModeShowsWorktreeContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feature-x"},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	view := m.View()
	if !strings.Contains(view, "main") {
		t.Error("view should contain worktree branch 'main'")
	}
	if !strings.Contains(view, "feature-x") {
		t.Error("view should contain worktree branch 'feature-x'")
	}
	if !strings.Contains(view, "[root]") {
		t.Error("view should contain '[root]' annotation for main worktree")
	}
}

func TestModel_ViewWorktreesDirtyShowsDiffHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true, FilesChanged: 2},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	view := m.View()
	for _, hint := range []string{"enter: diff", "t: terminal", "c: code"} {
		if !strings.Contains(view, hint) {
			t.Errorf("view should show %q for dirty worktree", hint)
		}
	}
}

func TestModel_ViewWorktreesCleanHidesEnterDiff(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	view := m.View()
	if strings.Contains(view, "enter: diff") {
		t.Error("view should NOT show 'enter: diff' for clean worktree")
	}
	if !strings.Contains(view, "t: terminal") {
		t.Error("view should show 't: terminal' for clean worktree")
	}
}

func TestModel_ViewWorktreesStaleHidesAllActions(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-gone", BranchName: "gone", Stale: true},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	view := m.View()
	for _, hint := range []string{"enter: diff", "t: terminal", "c: code"} {
		if strings.Contains(view, hint) {
			t.Errorf("view should NOT show %q for stale worktree", hint)
		}
	}
}

func TestModel_ViewWorktreeDiffOverlayShowsDiff(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true, FilesChanged: 1},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.WorktreeDiffResultMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Diff:         "diff --git a/f.txt\n--- a/f.txt\n+++ b/f.txt",
	})

	view := m.View()
	if !strings.Contains(view, "diff --git") {
		t.Error("overlay should show diff content")
	}
	if !strings.Contains(view, "esc") {
		t.Error("overlay should show esc hint")
	}
}

func TestModel_ViewReflogModeShowsReflogContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, model.ReflogResultMsg{
		RepoPath: "/dev/alpha",
		Reflogs:  testReflogs(),
	})

	view := m.View()
	if !strings.Contains(view, "commit: Fix login bug") {
		t.Error("reflog mode should show reflog subject")
	}
	if !strings.Contains(view, "HEAD@{0}") {
		t.Error("reflog mode should show reflog selector")
	}
	if strings.Contains(view, "nothing here yet") {
		t.Error("reflog mode should not show placeholder when data exists")
	}
}

func TestModel_ViewReflogDiffOverlayShowsEmptyMessage(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, model.ReflogResultMsg{
		RepoPath: "/dev/alpha",
		Reflogs:  testReflogs(),
	})
	// Open overlay and receive empty diff (e.g. checkout entry)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: ""})

	view := m.View()
	if !strings.Contains(view, "No changes at this reflog entry") {
		t.Error("reflog diff overlay with empty diff should show 'No changes at this reflog entry'")
	}
}

func TestModel_ViewReflogDiffOverlayShowsDiff(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, model.ReflogResultMsg{
		RepoPath: "/dev/alpha",
		Reflogs:  testReflogs(),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", Diff: "diff --git a/f.txt\n+added line"})

	view := m.View()
	if !strings.Contains(view, "diff --git") {
		t.Error("reflog diff overlay should show diff content")
	}
}

func TestModel_ViewReflogModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	view := m.View()
	if !strings.Contains(view, "nothing here yet") {
		t.Error("reflog mode with no data should show placeholder")
	}
}
