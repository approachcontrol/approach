package model_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

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

func TestModel_ViewKeepsSelectedSessionVisibleBelowTableHeader(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 8})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, model.SessionResultMsg{RepoPath: "/dev/alpha", Sessions: []sessions.SessionRecord{
		{Provider: sessions.ProviderCodex, SessionID: "codex-0", RepoPath: "/dev/alpha", Branch: "session-row-0"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-1", RepoPath: "/dev/alpha", Branch: "session-row-1"},
		{Provider: sessions.ProviderCodex, SessionID: "codex-2", RepoPath: "/dev/alpha", Branch: "session-row-2"},
	}, ListRequest: m.ListRequest(ui.ModeSessions)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := m.View()
	if !strings.Contains(view, "session-row-2") {
		t.Fatalf("selected session should be visible below table header:\n%s", view)
	}
	if strings.Contains(view, "session-row-0") {
		t.Fatalf("first session row should have scrolled off:\n%s", view)
	}
}

func TestModel_ViewKeepsExpandedSelectedPlanVisibleBelowTableHeader(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 8 + ui.TerminalChipRows})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-0", RepoPath: "/dev/alpha", Branch: "plan-row-0", Status: "draft", Title: "Plan zero"},
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Branch: "plan-row-1", Status: "draft", Title: "Plan one"},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Branch: "plan-row-2", Status: "draft", Title: "Plan two", Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Bottom phase", Status: "phase-mark", Order: 1},
		}},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if !strings.Contains(view, "plan-row-2") {
		t.Fatalf("selected plan should be visible below table header:\n%s", view)
	}
	if !strings.Contains(view, "phase-mark") {
		t.Fatalf("expanded phase row should be visible below table header:\n%s", view)
	}
	if strings.Contains(view, "plan-row-0") {
		t.Fatalf("first plan row should have scrolled off:\n%s", view)
	}
}

func TestModel_ViewRestoresShortcutPaneAfterKeepingRepoFilter(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alp")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if !strings.Contains(view, "Shortcuts") {
		t.Fatalf("kept repo filter should restore shortcut pane, got:\n%s", view)
	}
	if !strings.Contains(view, "filtered repos: alp") {
		t.Fatalf("kept repo filter should keep filter footer, got:\n%s", view)
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
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
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
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
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
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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

func TestModel_ViewModeHeaderShowsGroupedGitSubviews(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	view := m.View()
	// Worktrees subview active: grouped header shows the Git group plus the
	// letter-labelled subview row.
	if !strings.Contains(view, "[1] git") {
		t.Error("worktrees active: header should bracket the Git group '[1] git'")
	}
	if !strings.Contains(view, "[w] worktrees") {
		t.Error("worktrees active: header should contain '[w] worktrees'")
	}
	for _, want := range []string{"b branches", "s stashes", "h history", "r reflog", "2 sessions", "3 plans", "4 flows", "^a active flows"} {
		if !strings.Contains(view, want) {
			t.Errorf("worktrees active: header should show inactive %q", want)
		}
	}
	if strings.Contains(view, "5 active flows") {
		t.Fatalf("worktrees active: header should not label active flows with 5")
	}

	// Cycle the git subviews with the right arrow.
	m = inRightPane(m)
	for _, want := range []string{"[b] branches", "[s] stashes", "[h] history", "[r] reflog"} {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
		view = m.View()
		if !strings.Contains(view, want) {
			t.Errorf("header should contain %q after right arrow", want)
		}
		if !strings.Contains(view, "[1] git") {
			t.Errorf("header should keep the Git group bracketed while %q is active", want)
		}
	}

	// A non-git view renders the single top-level row without subviews.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	view = m.View()
	if !strings.Contains(view, "[2] sessions") {
		t.Error("sessions active: header should contain '[2] sessions'")
	}
	if strings.Contains(view, "w worktrees") {
		t.Error("sessions active: header should not list git subviews")
	}
}

func TestModel_ViewStatusBarShowsKeyHints(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})

	view := m.View()
	if !strings.Contains(view, "tab") || !strings.Contains(view, "pane") {
		t.Error("view should contain tab pane shortcut")
	}
}

func TestModel_ViewStashesModeShowsStashContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})

	view := m.View()
	if !strings.Contains(view, "WIP: feature A") {
		t.Error("view should contain stash message 'WIP: feature A'")
	}
	if !strings.Contains(view, "backup: old approach") {
		t.Error("view should contain stash message 'backup: old approach'")
	}
}

func TestModel_StatusBarStashesModeShowsStashKeys(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // stashes
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()[:1]})

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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // stashes
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()[:1]})

	view := m.View()
	if !strings.Contains(view, "d      drop") {
		t.Error("stashes view should mention 'd      drop' in destructive mode")
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // stashes

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
	if !strings.Contains(view, "D      destructive mode") {
		t.Error("read-only mode should show 'D      destructive mode' hint")
	}
}

func TestModel_ViewDestructiveModeShowsDeleteHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{
		RepoPath: "/dev/alpha",
		Branches: []gitquery.Branch{
			{Name: "feature", HasUpstream: true},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	view := m.View()
	if !strings.Contains(view, "d      delete") {
		t.Error("destructive mode should show 'd      delete'")
	}
}

func TestModel_ViewHistoryModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})

	view := m.View()
	if !strings.Contains(view, "No commits") {
		t.Error("history mode with no commits should show history-specific empty state")
	}
}

func TestModel_ViewHistoryModeShowsCommitContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()[:1]})

	view := m.View()
	if !strings.Contains(view, "enter  diff") {
		t.Error("mode 3 view should mention 'enter  diff'")
	}
	if !strings.Contains(view, "y      copy hash") {
		t.Error("mode 3 view should mention 'y      copy hash'")
	}
	if !strings.Contains(view, "t/c    terminal / code") {
		t.Error("mode 3 view should mention 't/c    terminal / code'")
	}
}

func TestModel_ViewDestructiveModeHidesDestructiveHint(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inBranchesMode(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})

	view := m.View()
	if strings.Contains(view, "D      destructive mode") {
		t.Error("destructive mode should NOT show 'D      destructive mode' hint")
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
	if !strings.Contains(view, "d      delete") {
		t.Error("worktrees mode destructive non-stale should show 'd      delete'")
	}
	if strings.Contains(view, "p      prune") {
		t.Error("worktrees mode destructive non-stale should NOT show 'p      prune'")
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
	if !strings.Contains(view, "u      unlock") {
		t.Error("locked worktree should show 'u      unlock'")
	}
}

func TestModel_ViewWorktreesModeShowsMoveHintForMovableWorktree(t *testing.T) {
	tests := []struct {
		name     string
		worktree gitquery.Worktree
	}{
		{"clean", gitquery.Worktree{Path: "/dev/alpha-worktrees/feat", BranchName: "feat"}},
		{"dirty", gitquery.Worktree{Path: "/dev/alpha-worktrees/feat", BranchName: "feat", Dirty: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model.New(testRepos())
			m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
			m = inRightPane(m)
			m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
				{Path: "/dev/alpha", BranchName: "main", IsMain: true},
				tt.worktree,
			}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

			view := m.View()
			if !strings.Contains(view, "m      move") {
				t.Fatalf("movable %s worktree should show move hint, got:\n%s", tt.name, view)
			}
		})
	}
}

func TestModel_ViewWorktreesModeHidesMoveHintForIneligibleWorktrees(t *testing.T) {
	tests := []struct {
		name     string
		worktree gitquery.Worktree
	}{
		{"main", gitquery.Worktree{Path: "/dev/alpha", BranchName: "main", IsMain: true}},
		{"stale", gitquery.Worktree{Path: "/dev/alpha-worktrees/feat", BranchName: "feat", Stale: true}},
		{"locked", gitquery.Worktree{Path: "/dev/alpha-worktrees/feat", BranchName: "feat", Locked: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model.New(testRepos())
			m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
			m = inRightPane(m)
			m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{tt.worktree}})

			view := m.View()
			if strings.Contains(view, "m      move") {
				t.Fatalf("%s worktree should not show move hint, got:\n%s", tt.name, view)
			}
		})
	}
}

func TestModel_ViewWorktreeMoveInputShowsPromptAndPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-worktrees/feat", BranchName: "feat"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})

	view := m.View()
	if !strings.Contains(view, "Move worktree to:") {
		t.Fatalf("move input should show prompt, got:\n%s", view)
	}
	if !strings.Contains(view, ui.WorktreeMoveInputPlaceholder) {
		t.Fatalf("move input should show placeholder, got:\n%s", view)
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
	for _, hint := range []string{"d      delete", "p      prune"} {
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
	if !strings.Contains(view, "p      prune") {
		t.Error("worktrees mode destructive stale should show 'p      prune'")
	}
	if strings.Contains(view, "d      delete") {
		t.Error("worktrees mode destructive stale should NOT show 'd      delete'")
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
	if !strings.Contains(view, "D      destructive mode") {
		t.Error("worktrees mode read-only should show 'D      destructive mode'")
	}
	if strings.Contains(view, "d      delete") {
		t.Error("worktrees mode read-only should NOT show 'd      delete'")
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
	for _, hint := range []string{"enter  diff", "t/c    terminal / code"} {
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
	if strings.Contains(view, "enter  diff") {
		t.Error("view should NOT show 'enter  diff' for clean worktree")
	}
	if !strings.Contains(view, "t/c    terminal / code") {
		t.Error("view should show 't/c    terminal / code' for clean worktree")
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

func TestModel_ListFetchErrorOnEmptyPaneStillLooksFailedAfterStatusExpires(t *testing.T) {
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
	m, _ = update(m, model.StatusExpiredMsg{Seq: 1})

	view := m.View()
	if strings.Contains(view, "failed to load worktrees: boom") {
		t.Fatalf("status bar text should expire, got:\n%s", view)
	}
	if !strings.Contains(view, "Could not load worktrees") {
		t.Fatalf("empty failed pane should still show load failure after status expiry, got:\n%s", view)
	}
	if strings.Contains(view, "see status bar") {
		t.Fatalf("expired status should not direct the user to an empty status bar, got:\n%s", view)
	}
	if strings.Contains(view, "No worktrees to show") {
		t.Fatal("expired status should not make failed load look like successful empty data")
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
	msgs := []tea.Msg{msg}
	if batch, ok := msg.(tea.BatchMsg); ok {
		msgs = msgs[:0]
		for _, subcmd := range batch {
			msgs = append(msgs, subcmd())
		}
	}
	var fetchErr model.FetchErrorMsg
	found := false
	for _, msg := range msgs {
		if err, ok := msg.(model.FetchErrorMsg); ok {
			fetchErr = err
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fake repo init to return FetchErrorMsg, got %#v", msgs)
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

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

func TestModel_NonModalKeysClearFetchStatus(t *testing.T) {
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
	if strings.Contains(m.View(), "diff failed") {
		t.Error("non-modal keypress should clear diff fetch status")
	}
}

func TestModel_ViewReflogModeShowsReflogContent(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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

func TestModel_ReflogEmptyDiffPagesMessage(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{
		RepoPath: "/dev/alpha",
		Reflogs:  testReflogs(),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: ""})

	if cmd == nil {
		t.Fatal("expected empty reflog diff pager command")
	}
	if len(paged) != 1 || paged[0] != "No changes at this reflog entry" {
		t.Fatalf("paged empty reflog diff = %#v", paged)
	}
}

func TestModel_ReflogDiffPagesContent(t *testing.T) {
	var paged []string
	m := model.NewWithOptions(testRepos(), model.Options{PageText: recordPageText(&paged)})
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{
		RepoPath: "/dev/alpha",
		Reflogs:  testReflogs(),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := update(m, model.ReflogDiffResultMsg{RepoPath: "/dev/alpha", Hash: "abc1234", DiffRequest: 1, Diff: "diff --git a/f.txt\n+added line"})

	if cmd == nil {
		t.Fatal("expected reflog diff pager command")
	}
	if len(paged) != 1 || !strings.Contains(paged[0], "diff --git") {
		t.Fatalf("paged reflog diff = %#v", paged)
	}
}

func TestModel_ViewReflogModeShowsPlaceholder(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	view := m.View()
	if !strings.Contains(view, "No reflog entries") {
		t.Error("reflog mode with no data should show reflog-specific empty state")
	}
}
