package model_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/ui"
)

// pressKey sends a single-rune key to the model.
func pressKey(m model.Model, r rune) model.Model {
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return m
}

func TestGitSubview_LetterSwitchesWorktreesToBranches(t *testing.T) {
	m := inRightPane(model.New(testRepos()))

	m = pressKey(m, 'b')

	if m.Mode() != ui.ModeBranches {
		t.Fatalf("mode = %d, want ModeBranches", m.Mode())
	}
}

func TestGitSubview_CurrentSubviewLetterIsNoOp(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	before := listRequests(m)

	m = pressKey(m, 'w')

	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %d, want unchanged ModeWorktrees", m.Mode())
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestGitSubview_LetterSwitchRefetchesTargetSubview(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	before := listRequests(m)

	m = pressKey(m, 'h')

	assertOnlyListRequestChanged(t, before, m, ui.ModeHistory)
}

func TestGitSubview_LettersInertWhileSearchBoxOpen(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, '/')

	m = pressKey(m, 'b')

	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %d, want ModeWorktrees while searching", m.Mode())
	}
}

func TestTopLevelKeys_NumbersSelectTopLevelViews(t *testing.T) {
	steps := []struct {
		key  rune
		want ui.Mode
	}{
		{'2', ui.ModeSessions},
		{'3', ui.ModePlans},
		{'4', ui.ModeFlows},
		{'1', ui.ModeWorktrees},
	}
	m := inRightPane(model.New(testRepos()))
	for _, step := range steps {
		m = pressKey(m, step.key)
		if m.Mode() != step.want {
			t.Fatalf("after %q mode = %d, want %d", step.key, m.Mode(), step.want)
		}
	}
}

func TestTopLevelKeys_SixThroughNineAreSilentNoOps(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	before := listRequests(m)

	for _, key := range []rune{'6', '7', '8', '9'} {
		var cmd tea.Cmd
		m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if m.Mode() != ui.ModeWorktrees {
			t.Fatalf("after %q mode = %d, want unchanged ModeWorktrees", key, m.Mode())
		}
		if cmd != nil {
			t.Fatalf("after %q got cmd %T, want nil", key, cmd)
		}
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestTopLevelKeys_GitEntryIsStickyToLastUsedSubview(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, 'h') // history subview
	m = pressKey(m, '2') // sessions
	if m.Mode() != ui.ModeSessions {
		t.Fatalf("mode = %d, want ModeSessions", m.Mode())
	}

	m = pressKey(m, '1')

	if m.Mode() != ui.ModeHistory {
		t.Fatalf("mode = %d, want sticky ModeHistory on Git re-entry", m.Mode())
	}
}

func TestTopLevelKeys_GitKeyWhileInGitIsNoOp(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, 's') // stashes subview
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})

	if m.Mode() != ui.ModeStashes {
		t.Fatalf("mode = %d, want unchanged ModeStashes", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("got cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestActiveFlowsToggle_OpensAndReturnsToPreviousView(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(model.Model) model.Model
		wantReturn ui.Mode
		wantFetch  ui.Mode
	}{
		{
			name:       "sessions",
			setup:      func(m model.Model) model.Model { return pressKey(m, '2') },
			wantReturn: ui.ModeSessions,
			wantFetch:  ui.ModeSessions,
		},
		{
			name:       "plans",
			setup:      func(m model.Model) model.Model { return pressKey(m, '3') },
			wantReturn: ui.ModePlans,
			wantFetch:  ui.ModePlans,
		},
		{
			name:       "flows",
			setup:      func(m model.Model) model.Model { return pressKey(m, '4') },
			wantReturn: ui.ModeFlows,
			wantFetch:  ui.ModeFlows,
		},
		{
			name: "git subview",
			setup: func(m model.Model) model.Model {
				return pressKey(m, 's')
			},
			wantReturn: ui.ModeStashes,
			wantFetch:  ui.ModeStashes,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := inRightPane(model.New(testRepos()))
			m = tc.setup(m)

			before := listRequests(m)
			var cmd tea.Cmd
			m, cmd = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
			if m.Mode() != ui.ModeActiveFlows {
				t.Fatalf("ctrl+a mode = %d, want active flows", m.Mode())
			}
			if cmd == nil {
				t.Fatal("ctrl+a opening active flows returned nil command")
			}
			assertOnlyListRequestChanged(t, before, m, ui.ModeActiveFlows)

			before = listRequests(m)
			m, cmd = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
			if m.Mode() != tc.wantReturn {
				t.Fatalf("second ctrl+a mode = %d, want %d", m.Mode(), tc.wantReturn)
			}
			if cmd == nil {
				t.Fatal("ctrl+a returning from active flows returned nil command")
			}
			assertOnlyListRequestChanged(t, before, m, tc.wantFetch)
		})
	}
}

func TestActiveFlowsToggle_FallsBackToFlowsWhenNoReturnModeRecorded(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{StartupMode: ui.ModeActiveFlows}))
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	if m.Mode() != ui.ModeFlows {
		t.Fatalf("ctrl+a from startup active flows mode = %d, want flows", m.Mode())
	}
	if cmd == nil {
		t.Fatal("ctrl+a fallback returned nil command")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeFlows)
}

func TestActiveFlowsToggle_WorksFromLeftPane(t *testing.T) {
	m := model.New(testRepos())
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	if m.ActivePane() != 0 {
		t.Fatalf("ctrl+a from left pane active pane = %d, want left pane preserved", m.ActivePane())
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("ctrl+a from left pane mode = %d, want active flows", m.Mode())
	}
	if cmd == nil {
		t.Fatal("ctrl+a from left pane returned nil command")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeActiveFlows)
}

func TestActiveFlowsToggle_InertWhileSearchActive(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, '/')
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("ctrl+a while searching mode = %d, want unchanged worktrees", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("ctrl+a while searching returned cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestTopLevelKeys_StartupGitDefaultSeedsStickySubview(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{StartupMode: ui.ModeReflog})
	m = inRightPane(m)
	m = pressKey(m, '3') // plans
	if m.Mode() != ui.ModePlans {
		t.Fatalf("mode = %d, want ModePlans", m.Mode())
	}

	m = pressKey(m, '1')

	if m.Mode() != ui.ModeReflog {
		t.Fatalf("mode = %d, want ModeReflog seeded from startup default", m.Mode())
	}
}

func TestGitSubview_ArrowsCycleSubviewsWithWrap(t *testing.T) {
	m := inRightPane(model.New(testRepos()))

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.Mode() != ui.ModeReflog {
		t.Fatalf("left from worktrees mode = %d, want wrap to ModeReflog", m.Mode())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("right from reflog mode = %d, want wrap to ModeWorktrees", m.Mode())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeBranches {
		t.Fatalf("right from worktrees mode = %d, want ModeBranches", m.Mode())
	}
}

func TestTopLevelArrows_CycleTopLevelViewsWithWrap(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, '2') // sessions

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModePlans {
		t.Fatalf("right from sessions mode = %d, want ModePlans", m.Mode())
	}

	m = pressKey(m, '4') // flows
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("right from flows mode = %d, want wrap to Git (worktrees)", m.Mode())
	}
}

func TestTopLevelArrows_InertInsideActiveFlows(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	before := listRequests(m)

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
	} {
		var cmd tea.Cmd
		m, cmd = update(m, key)
		if m.Mode() != ui.ModeActiveFlows {
			t.Fatalf("%q in active flows mode = %d, want unchanged active flows", key.String(), m.Mode())
		}
		if cmd != nil {
			t.Fatalf("%q in active flows returned cmd %T, want nil", key.String(), cmd)
		}
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestTopLevelNumberedKeys_InertInsideActiveFlows(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	before := listRequests(m)

	for _, key := range []rune{'1', '2', '3', '4', '5', '6', '7', '8', '9'} {
		var cmd tea.Cmd
		m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if m.Mode() != ui.ModeActiveFlows {
			t.Fatalf("%q in active flows mode = %d, want unchanged active flows", string(key), m.Mode())
		}
		if cmd != nil {
			t.Fatalf("%q in active flows returned cmd %T, want nil", string(key), cmd)
		}
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestTopLevelArrows_EnteringGitLandsOnLastUsedSubview(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, 'h') // history subview
	m = pressKey(m, '2') // sessions

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})

	if m.Mode() != ui.ModeHistory {
		t.Fatalf("left from sessions mode = %d, want last-used git subview ModeHistory", m.Mode())
	}
}

func TestRetiredKeys_HLInertInSessionsAndPlans(t *testing.T) {
	for _, view := range []struct {
		key  rune
		mode ui.Mode
	}{
		{'2', ui.ModeSessions},
		{'3', ui.ModePlans},
	} {
		m := inRightPane(model.New(testRepos()))
		m = pressKey(m, view.key)
		before := listRequests(m)
		for _, key := range []rune{'h', 'l'} {
			var cmd tea.Cmd
			m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			if m.Mode() != view.mode {
				t.Fatalf("%q in mode %d switched to %d, want inert", key, view.mode, m.Mode())
			}
			if cmd != nil {
				t.Fatalf("%q in mode %d produced cmd %T, want nil", key, view.mode, cmd)
			}
		}
		assertListRequestsUnchanged(t, before, m)
	}
}

func TestRetiredKeys_LInFlowsAliasesRightArrowToGit(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, '4') // flows

	m = pressKey(m, 'l')

	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("l in flows mode = %d, want wrap to ModeWorktrees (right alias)", m.Mode())
	}
}

func testHistoryWithCommits(t *testing.T) model.Model {
	t.Helper()
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, 'h')
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.CommitSelected() != 2 {
		t.Fatalf("CommitSelected = %d, want 2", m.CommitSelected())
	}
	return m
}

func TestGitSubview_IntraGitSwitchPreservesSiblingCursor(t *testing.T) {
	m := testHistoryWithCommits(t)

	m = pressKey(m, 'b') // branches
	m = pressKey(m, 'h') // back to history

	if m.CommitSelected() != 2 {
		t.Fatalf("CommitSelected = %d, want preserved 2", m.CommitSelected())
	}
}

func TestGitSubview_LeavingGitAndReturningPreservesCursor(t *testing.T) {
	m := testHistoryWithCommits(t)

	m = pressKey(m, '2') // sessions
	m = pressKey(m, '1') // back to Git, lands on history

	if m.Mode() != ui.ModeHistory {
		t.Fatalf("mode = %d, want ModeHistory", m.Mode())
	}
	if m.CommitSelected() != 2 {
		t.Fatalf("CommitSelected = %d, want preserved 2", m.CommitSelected())
	}
}

func TestGitSubview_PreservedCursorClampsWhenRefetchShrinksList(t *testing.T) {
	m := testHistoryWithCommits(t)

	m = pressKey(m, 'b')
	m = pressKey(m, 'h')
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()[:2]})

	if m.CommitSelected() != 1 {
		t.Fatalf("CommitSelected = %d, want clamped to 1", m.CommitSelected())
	}
}

func TestGitSubview_SessionsPlansKeepResetOnEntry(t *testing.T) {
	m := plansInRightPane(t, model.New(testRepos()), []planstore.PlanRecord{
		{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "First", Status: "draft"},
		{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Second", Status: "draft"},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.PlanSelected() != 1 {
		t.Fatalf("PlanSelected = %d, want 1", m.PlanSelected())
	}

	m = pressKey(m, '2') // sessions
	m = pressKey(m, '3') // back to plans

	if m.PlanSelected() != 0 {
		t.Fatalf("PlanSelected = %d, want reset 0", m.PlanSelected())
	}
}

func TestGitSubview_AsyncModeForcingUpdatesStickySubview(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, 'h') // history; sticky subview = history

	// A completed branch creation forces the branches subview open.
	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feat"})
	if m.Mode() != ui.ModeBranches {
		t.Fatalf("mode = %d, want ModeBranches after branch creation", m.Mode())
	}

	m = pressKey(m, '2') // sessions
	m = pressKey(m, '1') // back to Git

	if m.Mode() != ui.ModeBranches {
		t.Fatalf("mode = %d, want sticky ModeBranches from the forced subview switch", m.Mode())
	}
}

func TestGitSubview_FilterIsPerSubview(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/zebra", BranchName: "zebra"},
		{Path: "/dev/alpha-worktrees/two", BranchName: "two"},
	}})

	m = pressKey(m, '/')
	for _, r := range "zebra" {
		m = pressKey(m, r)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := len(m.Worktrees()); got != 1 {
		t.Fatalf("filtered worktrees = %d, want 1", got)
	}

	m = pressKey(m, 'h') // history
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	if got := len(m.Commits()); got != len(testCommits()) {
		t.Fatalf("history rows = %d, want unfiltered %d", got, len(testCommits()))
	}

	m = pressKey(m, 'w') // back to worktrees
	if got := len(m.Worktrees()); got != 1 {
		t.Fatalf("worktree filter not restored: rows = %d, want 1", got)
	}
}

func TestGitSubview_SessionKeysKeepSessionsMeaning(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, '2')
	if m.Mode() != ui.ModeSessions {
		t.Fatalf("mode = %d, want ModeSessions", m.Mode())
	}

	for _, key := range []rune{'s', 'r', 'w', 'b'} {
		m = pressKey(m, key)
		if m.Mode() != ui.ModeSessions {
			t.Fatalf("after %q mode = %d, want ModeSessions", key, m.Mode())
		}
	}
}

func TestGitSubview_HeadlessToggleKeepsFlowsMeaning(t *testing.T) {
	m := inRightPane(model.New(testRepos()))
	m = pressKey(m, '4')
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want ModeFlows", m.Mode())
	}

	m = pressKey(m, 'h')

	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want ModeFlows after h", m.Mode())
	}
	if model.FlowHeadlessForTest(m) {
		t.Fatalf("flow headless = true, want toggled off")
	}
}

func TestGitSubview_EachLetterSelectsItsSubview(t *testing.T) {
	steps := []struct {
		key  rune
		want ui.Mode
	}{
		{'s', ui.ModeStashes},
		{'h', ui.ModeHistory},
		{'r', ui.ModeReflog},
		{'b', ui.ModeBranches},
		{'w', ui.ModeWorktrees},
	}
	m := inRightPane(model.New(testRepos()))
	for _, step := range steps {
		m = pressKey(m, step.key)
		if m.Mode() != step.want {
			t.Fatalf("after %q mode = %d, want %d", step.key, m.Mode(), step.want)
		}
	}
}
