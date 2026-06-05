package model_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/ui"
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

func TestModel_ViewNoReposShowsEmptyMessage(t *testing.T) {
	m := model.New(nil)
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	view := m.View()
	if !strings.Contains(view, "No repositories found") {
		t.Fatalf("view with no repos should explain that no repositories were found, got:\n%s", view)
	}
}

func TestModel_ViewWorktreesModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Load branch data — worktrees mode should still show placeholder, not branches
	branches := []gitquery.Branch{{Name: "main", HasUpstream: true}}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	view := m.View()
	if !strings.Contains(view, "No worktrees to show") {
		t.Error("ModeWorktrees should show worktree-specific empty state even when branch data is loaded")
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
	if !strings.Contains(view, "No stashes") {
		t.Error("ModeStashes with no data should show stash-specific empty state")
	}
}

func TestModel_ViewDistinguishesFilteredEmptyRepos(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})

	view := m.View()
	if !strings.Contains(view, "No repo results for zzz") {
		t.Fatalf("filtered repo pane should explain that the repo filter has no matches, got:\n%s", view)
	}
	if !strings.Contains(view, "No matching repo") {
		t.Fatalf("right pane should explain that the repo filter leaves no selected repo, got:\n%s", view)
	}
	if strings.Contains(view, "No selected repo") {
		t.Fatal("filtered-empty repo view should not use generic no-selected-repo copy")
	}
}

func TestModel_ViewDistinguishesFilteredEmptyItemsInEveryMode(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(model.Model) model.Model
		want      string
		notWanted string
	}{
		{
			name: "worktrees",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
					{Path: "/dev/alpha", BranchName: "main", IsMain: true},
				}})
				return m
			},
			want:      "No worktree results for zzz",
			notWanted: "No worktrees to show",
		},
		{
			name: "branches",
			setup: func(m model.Model) model.Model {
				m = inBranchesMode(m)
				m, _ = update(m, model.BranchResultMsg{
					RepoPath: "/dev/alpha",
					Branches: []gitquery.Branch{
						{Name: "main", HasUpstream: true},
						{Name: "feature/auth", HasUpstream: true},
					},
				})
				return m
			},
			want:      "No branch results for zzz",
			notWanted: "No branches to show",
		},
		{
			name: "stashes",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
				m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()[:1]})
				return m
			},
			want:      "No stash results for zzz",
			notWanted: "No stashes",
		},
		{
			name: "history",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
				m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()[:1]})
				return m
			},
			want:      "No commit results for zzz",
			notWanted: "No commits",
		},
		{
			name: "reflog",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
				m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()[:1]})
				return m
			},
			want:      "No reflog results for zzz",
			notWanted: "No reflog entries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model.New(testRepos())
			m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
			m = tt.setup(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})

			view := m.View()
			if !strings.Contains(view, tt.want) {
				t.Fatalf("filtered %s pane should explain that the filter has no matches, got:\n%s", tt.name, view)
			}
			if strings.Contains(view, tt.notWanted) {
				t.Fatalf("filtered-empty %s pane should not look like an unfiltered empty pane", tt.name)
			}
		})
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
	stash := testStashes()[0]
	m, _ = update(m, model.StashDiffResultMsg{
		RepoPath:    "/dev/alpha",
		Index:       stash.Index,
		Date:        stash.Date,
		Message:     stash.Message,
		DiffRequest: 1,
		Diff:        "diff --git a/f.txt\n--- a/f.txt\n+++ b/f.txt",
	})

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
	if !strings.Contains(view, "No commits") {
		t.Error("history mode with no commits should show history-specific empty state")
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
		DiffRequest:  1,
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

func TestModel_ViewWorktreeUnlockFailureShowsStatusError(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeUnlockFailedMsg{RepoPath: "/dev/alpha", Err: "unlock failed"})

	view := m.View()
	if !strings.Contains(view, "unlock failed") {
		t.Error("view should show unlock failure in status bar")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("unlock failure should not open overlay, got %d", m.Overlay())
	}
}

func TestModel_ViewWorktreeUnlockFailureClearsOnNavigation(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}})
	m, _ = update(m, model.WorktreeUnlockFailedMsg{RepoPath: "/dev/alpha", Err: "unlock failed"})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := m.View()
	if strings.Contains(view, "unlock failed") {
		t.Error("unlock failure should clear on navigation")
	}
}

func TestModel_ViewWorktreeUnlockFailureIgnoredForStaleRepo(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = selectBravo(m)
	m, _ = update(m, model.WorktreeUnlockFailedMsg{RepoPath: "/dev/alpha", Err: "unlock failed"})

	view := m.View()
	if strings.Contains(view, "unlock failed") {
		t.Error("stale unlock failure should not render status error")
	}
}

func TestModel_ViewWorktreeUnlockFailureClearsOnSuccess(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeUnlockFailedMsg{RepoPath: "/dev/alpha", Err: "unlock failed"})

	m, _ = update(m, model.WorktreeUnlockedMsg{RepoPath: "/dev/alpha"})

	view := m.View()
	if strings.Contains(view, "unlock failed") {
		t.Error("unlock failure should clear after successful unlock")
	}
}

func TestModel_ViewGitFetchFailureShowsStatusError(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.GitFetchFailedMsg{RepoPath: "/dev/alpha", Err: "fetch failed"})

	view := m.View()
	if !strings.Contains(view, "fetch failed") {
		t.Error("view should show fetch failure in status bar")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Errorf("fetch failure should not open overlay, got %d", m.Overlay())
	}
}

func TestModel_ViewGitPullFailureClearsOnSuccess(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.GitPullFailedMsg{RepoPath: "/dev/alpha", Err: "pull failed"})

	m, _ = update(m, model.GitPulledMsg{RepoPath: "/dev/alpha"})

	view := m.View()
	if strings.Contains(view, "pull failed") {
		t.Error("pull failure should clear after successful pull")
	}
}

func TestModel_ListFetchErrorShowsStatusWithoutClearingPane(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Pane:     "worktrees",
		Err:      "failed to load worktrees: boom",
		Kind:     model.FetchList,
		Mode:     ui.ModeWorktrees,
	})

	view := m.View()
	if !strings.Contains(view, "failed to load worktrees: boom") {
		t.Error("view should show list fetch failure in status bar")
	}
	if !strings.Contains(view, "main") {
		t.Error("list fetch failure should not clear existing pane data")
	}
	if strings.Contains(view, "Could not load worktrees; see status bar") {
		t.Error("list fetch failure should not show a failure placeholder while existing pane data is visible")
	}
}

func TestModel_ListFetchErrorOnEmptyPaneDoesNotLookLikeNoData(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Pane:     "worktrees",
		Err:      "failed to load worktrees: boom",
		Kind:     model.FetchList,
		Mode:     ui.ModeWorktrees,
	})

	view := m.View()
	if !strings.Contains(view, "failed to load worktrees: boom") {
		t.Error("view should show list fetch failure in status bar")
	}
	if !strings.Contains(view, "Could not load worktrees; see status bar") {
		t.Fatalf("empty failed pane should direct the user to the status error, got:\n%s", view)
	}
	if strings.Contains(view, "No worktrees to show") {
		t.Fatal("failed load should not look like successful empty data")
	}
}

func TestModel_FilteredEmptyItemsTakePrecedenceOverListFetchErrorPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Pane:     "worktrees",
		Err:      "failed to load worktrees: boom",
		Kind:     model.FetchList,
		Mode:     ui.ModeWorktrees,
	})

	view := m.View()
	if !strings.Contains(view, "failed to load worktrees: boom") {
		t.Error("status bar should still show the fetch failure")
	}
	if !strings.Contains(view, "No worktree results for zzz") {
		t.Fatalf("filtered-empty message should explain the visible pane emptiness, got:\n%s", view)
	}
	if strings.Contains(view, "Could not load worktrees; see status bar") {
		t.Fatal("filtered-empty pane should not be replaced by fetch-failure placeholder")
	}
}

func TestModel_InitFetchUsesNonZeroListRequest(t *testing.T) {
	m := model.New(testRepos())
	msg := m.Init()()
	fetchErr, ok := msg.(model.FetchErrorMsg)
	if !ok {
		t.Fatalf("expected fake repo init to return FetchErrorMsg, got %T", msg)
	}
	if fetchErr.ListRequest == 0 {
		t.Fatal("initial fetch should carry a non-zero list request")
	}
}

func TestModel_ZeroListRequestFetchErrorFailsClosed(t *testing.T) {
	m := model.New(testRepos())
	tm, _ := m.Update(model.FetchErrorMsg{
		RepoPath:    "/dev/alpha",
		Err:         "zero request failure",
		Kind:        model.FetchList,
		Mode:        ui.ModeWorktrees,
		ListRequest: 0,
	})
	m = tm.(model.Model)

	if strings.Contains(m.View(), "zero request failure") {
		t.Fatal("zero-request list failure should be ignored")
	}
}

func TestModel_ZeroListRequestResultFailsClosed(t *testing.T) {
	m := model.New(testRepos())
	tm, _ := m.Update(model.BranchResultMsg{
		RepoPath:    "/dev/alpha",
		Branches:    []gitquery.Branch{{Name: "zero-request-branch"}},
		ListRequest: 0,
	})
	m = tm.(model.Model)

	if strings.Contains(m.View(), "zero-request-branch") {
		t.Fatal("zero-request list result should be ignored")
	}
}

func TestModel_StaleListFetchErrorIgnored(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = selectBravo(m)
	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Pane:     "worktrees",
		Err:      "failed to load worktrees: stale",
		Kind:     model.FetchList,
		Mode:     ui.ModeWorktrees,
	})

	view := m.View()
	if strings.Contains(view, "failed to load worktrees: stale") {
		t.Error("stale list fetch failure should not show in status bar")
	}
}

func TestModel_WrongModeListFetchErrorIgnored(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Pane:     "branches",
		Err:      "failed to load branches: stale",
		Kind:     model.FetchList,
		Mode:     ui.ModeBranches,
	})

	if strings.Contains(m.View(), "failed to load branches: stale") {
		t.Error("same-repo list failure from another mode should not show in status bar")
	}
}

func TestModel_FetchErrorWithUnknownKindIgnored(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Pane:     "worktrees",
		Err:      "missing kind",
	})

	if strings.Contains(m.View(), "missing kind") {
		t.Error("fetch error without kind should fail closed")
	}
}

func TestModel_ListSuccessClearsOnlyFetchStatus(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Err:      "failed to load worktrees: boom",
		Kind:     model.FetchList,
		Mode:     ui.ModeWorktrees,
	})

	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	if strings.Contains(m.View(), "failed to load worktrees: boom") {
		t.Error("current list success should clear fetch status")
	}

	m, _ = update(m, model.WorktreeUnlockFailedMsg{RepoPath: "/dev/alpha", Err: "unlock failed"})
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	if !strings.Contains(m.View(), "unlock failed") {
		t.Error("list success should not clear non-fetch status")
	}
}

func TestModel_ListSuccessDoesNotClearDiffFetchStatus(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "diff failed",
		Kind:         model.FetchWorktreeDiff,
		Mode:         ui.ModeWorktrees,
		DiffRequest:  1,
		WorktreePath: "/dev/alpha",
	})

	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true},
	}})

	if !strings.Contains(m.View(), "diff failed") {
		t.Error("list success should not clear an active diff fetch failure")
	}
}

func TestModel_OldListFetchErrorIgnoredAfterNewerListRequest(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)

	m, cmd := update(m, model.GitFetchedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected first refresh command")
	}
	oldErr, ok := cmd().(model.FetchErrorMsg)
	if !ok {
		t.Fatalf("expected first refresh to fail against fake repo, got %T", cmd())
	}

	m, cmd = update(m, model.GitFetchedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected second refresh command")
	}
	newErr, ok := cmd().(model.FetchErrorMsg)
	if !ok {
		t.Fatalf("expected second refresh to fail against fake repo, got %T", cmd())
	}
	if oldErr.ListRequest == newErr.ListRequest {
		t.Fatal("expected refreshes to carry distinct list requests")
	}

	m, _ = update(m, model.WorktreeResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: newErr.ListRequest,
		Worktrees:   []gitquery.Worktree{{Path: "/dev/alpha", BranchName: "main", IsMain: true}},
	})
	m, _ = update(m, oldErr)

	if strings.Contains(m.View(), oldErr.Err) {
		t.Error("older same-mode list failure should be ignored after newer request succeeds")
	}
}

func TestModel_WrongModeListSuccessDoesNotClearFetchStatus(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.FetchErrorMsg{
		RepoPath: "/dev/alpha",
		Err:      "worktree fetch failed",
		Kind:     model.FetchList,
		Mode:     ui.ModeWorktrees,
	})

	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{{Name: "main"}}})

	if !strings.Contains(m.View(), "worktree fetch failed") {
		t.Error("success for another list mode should not clear current fetch status")
	}
}

func TestModel_NonModalKeyClearsStatusButModalKeyDoesNot(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m, _ = update(m, model.FetchErrorMsg{RepoPath: "/dev/alpha", Err: "fetch failed", Kind: model.FetchList, Mode: ui.ModeWorktrees})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if strings.Contains(m.View(), "fetch failed") {
		t.Error("non-modal keypress should clear transient status")
	}

	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true, Dirty: true},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, model.FetchErrorMsg{
		RepoPath:     "/dev/alpha",
		Err:          "diff failed",
		Kind:         model.FetchWorktreeDiff,
		Mode:         ui.ModeWorktrees,
		DiffRequest:  1,
		WorktreePath: "/dev/alpha",
	})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.View(), "diff failed") {
		t.Error("modal keypress should not clear status by itself")
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
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: ""})

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
	m, _ = update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: "diff --git a/f.txt\n+added line"})

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
	if !strings.Contains(view, "No reflog entries") {
		t.Error("reflog mode with no data should show reflog-specific empty state")
	}
}
