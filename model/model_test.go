package model_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

// --- Shared helpers ---

func testRepos() []scanner.Repo {
	return []scanner.Repo{
		{Path: "/dev/alpha", DisplayName: "alpha"},
		{Path: "/dev/bravo", DisplayName: "bravo"},
		{Path: "/dev/charlie", DisplayName: "charlie"},
	}
}

func testStashes() []gitquery.Stash {
	return []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00 -0700", Message: "WIP: feature A"},
		{Index: 1, Date: "2026-03-17 09:00:00 -0700", Message: "backup: old approach"},
		{Index: 2, Date: "2026-03-16 08:00:00 -0700", Message: "experiment"},
	}
}

// update sends a message and returns the concrete Model.
func update(m model.Model, msg tea.Msg) (model.Model, tea.Cmd) {
	msg = stampListRequest(m, msg)
	tm, cmd := m.Update(msg)
	return tm.(model.Model), cmd
}

func stampListRequest(m model.Model, msg tea.Msg) tea.Msg {
	switch msg := msg.(type) {
	case model.WorktreeResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeWorktrees)
		}
		return msg
	case model.BranchResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeBranches)
		}
		return msg
	case model.StashResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeStashes)
		}
		return msg
	case model.CommitResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeHistory)
		}
		return msg
	case model.ReflogResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeReflog)
		}
		return msg
	case model.FlowResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeFlows)
		}
		return msg
	case model.ActiveFlowResultMsg:
		if msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(ui.ModeActiveFlows)
		}
		return msg
	case model.FetchErrorMsg:
		if msg.Kind == model.FetchList && msg.ListRequest == 0 {
			msg.ListRequest = m.ListRequest(msg.Mode)
		}
		return msg
	default:
		return msg
	}
}

func listRequests(m model.Model) map[ui.Mode]uint64 {
	requests := make(map[ui.Mode]uint64, int(ui.ModeActiveFlows))
	for mode := ui.ModeWorktrees; mode <= ui.ModeActiveFlows; mode++ {
		requests[mode] = m.ListRequest(mode)
	}
	return requests
}

func assertListRequestsUnchanged(t *testing.T, before map[ui.Mode]uint64, after model.Model) {
	t.Helper()
	for mode, request := range before {
		if got := after.ListRequest(mode); got != request {
			t.Fatalf("ListRequest(%d) = %d, want unchanged %d", mode, got, request)
		}
	}
}

func assertOnlyListRequestAdvanced(t *testing.T, before map[ui.Mode]uint64, after model.Model, advanced ui.Mode) {
	t.Helper()
	for mode, request := range before {
		got := after.ListRequest(mode)
		if mode == advanced {
			if got != request+1 {
				t.Fatalf("ListRequest(%d) = %d, want %d", mode, got, request+1)
			}
			continue
		}
		if got != request {
			t.Fatalf("ListRequest(%d) = %d, want unchanged %d", mode, got, request)
		}
	}
}

func assertOnlyListRequestChanged(t *testing.T, before map[ui.Mode]uint64, after model.Model, changed ui.Mode) {
	t.Helper()
	for mode, request := range before {
		got := after.ListRequest(mode)
		if mode == changed {
			if got == request {
				t.Fatalf("ListRequest(%d) = %d, want changed from previous request", mode, got)
			}
			continue
		}
		if got != request {
			t.Fatalf("ListRequest(%d) = %d, want unchanged %d", mode, got, request)
		}
	}
}

// inRightPane switches focus to the right pane.
func inRightPane(m model.Model) model.Model {
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

// selectBravo navigates to repo index 1 (bravo) in the left pane.
func selectBravo(m model.Model) model.Model {
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	return m
}

// inBranchesMode switches to right pane and selects the branches subview.
func inBranchesMode(m model.Model) model.Model {
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	return m
}

// keyForMode returns the key that reaches mode from the startup worktrees
// view: git subviews by their direct letters, other top-level views by number.
func keyForMode(mode ui.Mode) rune {
	switch mode {
	case ui.ModeWorktrees:
		return 'w'
	case ui.ModeBranches:
		return 'b'
	case ui.ModeStashes:
		return 's'
	case ui.ModeHistory:
		return 'h'
	case ui.ModeReflog:
		return 'r'
	case ui.ModeSessions:
		return '2'
	case ui.ModePlans:
		return '3'
	case ui.ModeFlows:
		return '4'
	default:
		return '5'
	}
}

// --- Init & basics ---

func TestModel_InitialActivePaneIsLeft(t *testing.T) {
	m := model.New(testRepos())
	if m.ActivePane() != 0 {
		t.Errorf("expected left pane (0) active initially, got %d", m.ActivePane())
	}
}

func TestModel_InitialSelection(t *testing.T) {
	m := model.New(testRepos())
	if m.Selected() != 0 {
		t.Errorf("expected initial selected 0, got %d", m.Selected())
	}
}

func TestModel_BareRepoShowsCheckedOutWorktreeBranches(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: repo.Path, Branches: []gitquery.Branch{
		{Name: "main"},
		{
			Name:          "feature",
			IsWorktree:    true,
			WorktreePaths: []string{"/dev/project-worktrees/feature"},
		},
	}})

	rows := m.Rows()
	if len(rows) != 2 {
		t.Fatalf("expected main and feature rows, got %+v", rows)
	}
	var sawMain, sawFeature bool
	for _, row := range rows {
		switch row.Branch.Name {
		case "main":
			sawMain = true
		case "feature":
			sawFeature = row.WorktreePath == "/dev/project-worktrees/feature"
		}
	}
	if !sawMain || !sawFeature {
		t.Fatalf("expected main and annotated feature rows, got %+v", rows)
	}
}

func TestModel_BareRepoDoesNotPinRootBranch(t *testing.T) {
	repo := scanner.Repo{Path: "/dev/project.git", DisplayName: "project.git", IsBare: true}
	m := model.New([]scanner.Repo{repo})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: repo.Path, Branches: []gitquery.Branch{
		{Name: "alpha"},
		{Name: "main"},
		{
			Name:          "zeta",
			IsWorktree:    true,
			WorktreePaths: []string{"/dev/project-worktrees/zeta"},
		},
	}})

	rows := m.Rows()
	if len(rows) != 3 {
		t.Fatalf("expected all branches, got %+v", rows)
	}
	if rows[0].Branch.Name != "alpha" {
		t.Fatalf("expected original branch order to remain unpinned, got %+v", rows)
	}
	for _, row := range rows {
		if row.WorktreePath == repo.Path {
			t.Fatalf("bare repo should not have a root branch row, got %+v", rows)
		}
	}
}

func TestModel_NormalRepoStillFiltersNonRootWorktreeBranches(t *testing.T) {
	m := model.New([]scanner.Repo{{Path: "/dev/project", DisplayName: "project"}})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/project", Branches: []gitquery.Branch{
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/project"}},
		{Name: "feature", IsWorktree: true, WorktreePaths: []string{"/dev/project-worktrees/feature"}},
		{Name: "topic"},
	}})

	rows := m.Rows()
	if len(rows) != 2 {
		t.Fatalf("expected root worktree branch plus topic, got %+v", rows)
	}
	if rows[0].Branch.Name != "main" || rows[0].WorktreePath != "/dev/project" {
		t.Fatalf("expected root branch pinned first, got %+v", rows)
	}
	if rows[1].Branch.Name != "topic" {
		t.Fatalf("expected non-worktree branch to remain, got %+v", rows)
	}
}

func TestModel_NormalRepoRootBranchAllowsCleanedRepoPath(t *testing.T) {
	m := model.New([]scanner.Repo{{Path: "/dev/project/", DisplayName: "project"}})
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/project/", Branches: []gitquery.Branch{
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/project"}},
		{Name: "feature", IsWorktree: true, WorktreePaths: []string{"/dev/project-worktrees/feature"}},
	}})

	rows := m.Rows()
	if len(rows) != 1 {
		t.Fatalf("expected only root branch row, got %+v", rows)
	}
	if rows[0].Branch.Name != "main" || rows[0].WorktreePath != "/dev/project" {
		t.Fatalf("expected cleaned root branch to remain, got %+v", rows)
	}
}

func TestModel_DefaultModeIsWorktrees(t *testing.T) {
	m := model.New(testRepos())
	if m.Mode() != 1 {
		t.Errorf("expected default mode ModeWorktrees (1), got %d", m.Mode())
	}
}

func TestModel_InitFiresWorktreeFetch(t *testing.T) {
	m := model.New(testRepos())
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd from Init, got nil")
	}
}

func TestModel_WorktreeResultUpdatesState(t *testing.T) {
	m := model.New(testRepos())
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	if len(m.Worktrees()) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(m.Worktrees()))
	}
	if m.WorktreeSelected() != 0 {
		t.Errorf("expected worktreeSelected 0, got %d", m.WorktreeSelected())
	}
}

func TestModel_StaleWorktreeResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	wts := []gitquery.Worktree{{Path: "/dev/alpha", BranchName: "main"}}
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	if len(m.Worktrees()) != 0 {
		t.Errorf("expected stale worktree result discarded, got %d", len(m.Worktrees()))
	}
}

func TestModel_WorktreeCursorWraps(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
		{Path: "/dev/alpha-fix", BranchName: "fix"},
	}
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	// Wrap backward from 0 to last
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.WorktreeSelected() != 2 {
		t.Errorf("expected WorktreeSelected to wrap to 2, got %d", m.WorktreeSelected())
	}
	// Wrap forward from last to 0
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.WorktreeSelected() != 0 {
		t.Errorf("expected WorktreeSelected to wrap to 0, got %d", m.WorktreeSelected())
	}
}

func TestModel_LockedWorktreeParticipatesInNavigation(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-locked", BranchName: "locked", Locked: true},
	}
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.WorktreeSelected() != 1 {
		t.Errorf("expected locked worktree to be selectable, got index %d", m.WorktreeSelected())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.WorktreeSelected() != 0 {
		t.Errorf("expected navigation away from locked worktree, got index %d", m.WorktreeSelected())
	}
}

func TestModel_WorktreeScrollFollowsCursor(t *testing.T) {
	wts := make([]gitquery.Worktree, 10)
	for i := range wts {
		wts[i] = gitquery.Worktree{Path: fmt.Sprintf("/dev/wt-%d", i), BranchName: fmt.Sprintf("branch-%d", i)}
	}
	contentHeight := 3
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BranchContentOverhead + contentHeight})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})

	// Move cursor past viewport
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.WorktreeSelected() != 9 {
		t.Errorf("expected cursor at 9, got %d", m.WorktreeSelected())
	}
	if m.WorktreeScroll() == 0 {
		t.Error("expected scroll to advance when cursor moves past viewport")
	}
	// Cursor must be within [scroll, scroll+contentHeight)
	if m.WorktreeSelected() < m.WorktreeScroll() || m.WorktreeSelected() >= m.WorktreeScroll()+contentHeight {
		t.Errorf("cursor %d not in scroll viewport [%d, %d)", m.WorktreeSelected(), m.WorktreeScroll(), m.WorktreeScroll()+contentHeight)
	}
}

func TestModel_ModeSwitchPreservesWorktreeCursors(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main"},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
		{Path: "/dev/alpha-fix", BranchName: "fix"},
	}
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: wts})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.WorktreeSelected() != 2 {
		t.Fatalf("expected WorktreeSelected 2, got %d", m.WorktreeSelected())
	}
	// Switch away and back
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.WorktreeSelected() != 2 {
		t.Errorf("expected WorktreeSelected preserved at 2, got %d", m.WorktreeSelected())
	}
}

func TestModel_SwitchToWorktreesModeFiresFetch(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m) // switch to the branches subview
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if cmd == nil {
		t.Fatal("expected fetchWorktrees cmd on switch to worktrees, got nil")
	}
}

func TestModel_SwitchToBranchesFiresFetch(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if cmd == nil {
		t.Fatal("expected fetchBranches cmd from switch to mode 2, got nil")
	}
}

func TestModel_WindowSizeUpdates(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.Width() != 120 || m.Height() != 40 {
		t.Errorf("expected 120x40, got %dx%d", m.Width(), m.Height())
	}
}

func TestModel_EmptyReposNoPanic(t *testing.T) {
	m := model.New(nil)
	_ = m.View()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	_, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
}

func TestModel_QuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEscape},
	} {
		m := model.New(testRepos())
		_, cmd := update(m, key)
		if cmd == nil {
			t.Fatalf("key %v: expected quit command, got nil", key)
		}
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Errorf("key %v: expected tea.QuitMsg, got %T", key, msg)
		}
	}
}

// --- Pane switching ---

func TestModel_TabFromRightPaneSwitchesToLeft(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyTab})

	if m.ActivePane() != 0 {
		t.Errorf("expected left pane after tab, got %d", m.ActivePane())
	}
	if cmd != nil {
		t.Fatalf("tab from right pane produced cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_TabFromLeftPaneSwitchesToRightWithoutChangingSelectionOrMode(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyTab})

	if m.ActivePane() != 1 {
		t.Errorf("expected right pane after tab, got %d", m.ActivePane())
	}
	if m.Selected() != 1 {
		t.Errorf("expected selected unchanged at 1, got %d", m.Selected())
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %d, want unchanged worktrees", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("tab from left pane produced cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_EnterFromLeftPaneSwitchesToRightWithoutChangingSelectionOrMode(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.ActivePane() != 1 {
		t.Fatalf("expected right pane after enter, got %d", m.ActivePane())
	}
	if m.Selected() != 1 {
		t.Fatalf("selected repo = %d, want unchanged 1", m.Selected())
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("mode = %d, want unchanged worktrees", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("enter from left pane produced cmd %T, want nil", cmd)
	}
}

func TestModel_TabDoesNotChangeMode(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // mode 3 (stashes)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.Mode() != 3 {
		t.Errorf("expected mode unchanged at 3, got %d", m.Mode())
	}
	if m.ActivePane() != 0 {
		t.Errorf("expected active pane cycled to left pane, got %d", m.ActivePane())
	}
}

func TestModel_BackspaceFromRightPaneSwitchesToLeftWithoutChangingMode(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // mode 3 (stashes)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})                 // right to left

	if m.ActivePane() != 0 {
		t.Fatalf("expected left pane after backspace, got %d", m.ActivePane())
	}
	if m.Mode() != ui.ModeStashes {
		t.Fatalf("mode = %d, want unchanged stashes", m.Mode())
	}
}

func TestModel_BackspaceFromRightPaneSwitchesToLeftAcrossModes(t *testing.T) {
	for _, tt := range []struct {
		mode ui.Mode
		key  tea.KeyMsg
	}{
		{mode: ui.ModeWorktrees, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeBranches, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeStashes, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeHistory, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeReflog, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeSessions, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModePlans, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeFlows, key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{mode: ui.ModeWorktrees, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModeBranches, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModeStashes, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModeHistory, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModeReflog, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModeSessions, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModePlans, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
		{mode: ui.ModeFlows, key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(fmt.Sprintf("mode_%d_%s", tt.mode, tt.key.String()), func(t *testing.T) {
			m := model.New(testRepos())
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{keyForMode(tt.mode)}})
			if m.ActivePane() != 1 || m.Mode() != tt.mode {
				t.Fatalf("setup activePane=%d mode=%d, want right pane mode %d", m.ActivePane(), m.Mode(), tt.mode)
			}
			before := listRequests(m)

			m, cmd := update(m, tt.key)

			if m.ActivePane() != 0 {
				t.Fatalf("active pane = %d, want left pane", m.ActivePane())
			}
			if m.Mode() != tt.mode {
				t.Fatalf("mode = %d, want unchanged %d", m.Mode(), tt.mode)
			}
			if cmd != nil {
				t.Fatalf("back key returned cmd %T, want nil", cmd)
			}
			assertListRequestsUnchanged(t, before, m)
		})
	}
}

func TestModel_BackspaceFromPlansPaneClearsSelectedPhase(t *testing.T) {
	m := plansInRightPane(t, model.New(testRepos()), []planstore.PlanRecord{{
		PlanID:   "plan-1",
		RepoPath: "/dev/alpha",
		Title:    "Persist plans",
		Status:   "draft",
		Phases:   []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.SelectedPlanPhaseID(); got != "p1" {
		t.Fatalf("selected plan phase = %q, want p1 before backspace", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.ActivePane() != 0 {
		t.Fatalf("expected left pane after backspace, got %d", m.ActivePane())
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("selected plan phase = %q, want cleared", got)
	}
	if m.Mode() != ui.ModePlans {
		t.Fatalf("mode = %d, want unchanged plans", m.Mode())
	}
}

func TestModel_BackKeysFromPlansPaneClearSelectedPhase(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{name: "ctrl-h", key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := plansInRightPane(t, model.New(testRepos()), []planstore.PlanRecord{{
				PlanID:   "plan-1",
				RepoPath: "/dev/alpha",
				Title:    "Persist plans",
				Status:   "draft",
				Phases:   []planstore.PlanPhase{{PhaseID: "p1", Title: "Tracer bullet", Status: "completed", Order: 1}},
			}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
			if got := m.SelectedPlanPhaseID(); got != "p1" {
				t.Fatalf("selected plan phase = %q, want p1 before %s", got, tt.name)
			}

			m, cmd := update(m, tt.key)

			if m.ActivePane() != 0 {
				t.Fatalf("expected left pane after %s, got %d", tt.name, m.ActivePane())
			}
			if got := m.SelectedPlanPhaseID(); got != "" {
				t.Fatalf("selected plan phase = %q, want cleared", got)
			}
			if m.Mode() != ui.ModePlans {
				t.Fatalf("mode = %d, want unchanged plans", m.Mode())
			}
			if cmd != nil {
				t.Fatalf("%s from plans pane returned cmd %T, want nil", tt.name, cmd)
			}
		})
	}
}

func TestModel_BackspaceFromFlowsPaneClearsSelectedPhase(t *testing.T) {
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flowWithPhaseDetails()})
	m = selectFlowPhaseByID(t, m, "implementation")

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.ActivePane() != 0 {
		t.Fatalf("expected left pane after backspace, got %d", m.ActivePane())
	}
	if got := m.SelectedFlowPhaseID(); got != "" {
		t.Fatalf("selected flow phase = %q, want cleared", got)
	}
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want unchanged flows", m.Mode())
	}
}

func TestModel_TabFromLeftPaneReturnsToActiveFlowsWithoutChangingMode(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := flowsInRightPane(t, model.New(testRepos()), []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyTab})

	if m.ActivePane() != 1 {
		t.Fatalf("active pane = %d, want right pane after tab", m.ActivePane())
	}
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("mode = %d, want active flows", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("tab from left pane with active flows returned cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_F2DoesNotSwitchPanesWhileSearchIsActive(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF2})

	if m.ActivePane() != 0 {
		t.Fatalf("active pane = %d, want left pane while search handles f2", m.ActivePane())
	}
}

func TestModel_BackKeysEditRightPaneSearchInsteadOfSwitchingPanes(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyBackspace}, {Type: tea.KeyCtrlH}} {
		t.Run(key.String(), func(t *testing.T) {
			m := model.New(testRepos())
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})

			m, cmd := update(m, key)

			if cmd != nil {
				t.Fatalf("search back key returned cmd %T, want nil", cmd)
			}
			if m.ActivePane() != 1 {
				t.Fatalf("active pane = %d, want right pane while search owns back key", m.ActivePane())
			}
			if m.ItemSearch() != "" || !m.SearchActive() {
				t.Fatalf("item search query=%q active=%v, want empty active search", m.ItemSearch(), m.SearchActive())
			}
		})
	}
}

func TestModel_BackKeysEditModalInputInsteadOfSwitchingPanes(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "backspace", key: tea.KeyMsg{Type: tea.KeyBackspace}},
		{name: "ctrl-h", key: tea.KeyMsg{Type: tea.KeyCtrlH}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model.New(testRepos())
			m = inRightPane(m)
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
			if cmd != nil {
				t.Fatalf("opening worktree input produced cmd %T, want nil", cmd)
			}
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f', 'e', 'a', 't'}})

			m, cmd = update(m, tt.key)

			if cmd != nil {
				t.Fatalf("modal %s returned cmd %T, want nil", tt.name, cmd)
			}
			if m.ActivePane() != 1 {
				t.Fatalf("active pane = %d, want right pane while modal owns %s", m.ActivePane(), tt.name)
			}
			if got := m.WorktreeInput(); got != "fea" {
				t.Fatalf("worktree input = %q, want edited value", got)
			}
		})
	}
}

// --- Left pane navigation ---

func TestModel_LeftPaneDownNavigatesRepos(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.Selected() != 1 {
		t.Errorf("expected selected 1 after down in left pane, got %d", m.Selected())
	}
}

func TestModel_LeftPaneDownFiresFetchInBranchMode(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}}) // branches
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})                 // back to left pane
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Error("expected fetch cmd after repo navigation in branches mode, got nil")
	}
}

func TestModel_LeftPaneUpNavigatesRepos(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // selected=1
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})   // selected=0
	if m.Selected() != 0 {
		t.Errorf("expected selected 0 after up, got %d", m.Selected())
	}
}

func TestModel_LeftPaneDownWrapsToFirst(t *testing.T) {
	m := model.New(testRepos()) // 3 repos
	// Move to last repo
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // 1
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // 2
	// One more should wrap to 0
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.Selected() != 0 {
		t.Errorf("expected selected to wrap to 0, got %d", m.Selected())
	}
}

func TestModel_LeftPaneUpWrapsToLast(t *testing.T) {
	m := model.New(testRepos()) // 3 repos
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.Selected() != 2 {
		t.Errorf("expected selected to wrap to 2, got %d", m.Selected())
	}
}

func TestModel_RepoSwitchClearsRightPaneData(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
	}})
	if len(m.Rows()) != 1 {
		t.Fatal("expected 1 row before switching repos")
	}
	// Switch to next repo — old data should be cleared immediately
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if len(m.Rows()) != 0 {
		t.Errorf("expected rows cleared on repo switch, got %d", len(m.Rows()))
	}
	if len(m.Stashes()) != 0 {
		t.Errorf("expected stashes cleared on repo switch, got %d", len(m.Stashes()))
	}
}

func TestModel_RepoSwitchClearsWorktrees(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}})
	if len(m.Worktrees()) != 1 {
		t.Fatal("expected 1 worktree before switching repos")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if len(m.Worktrees()) != 0 {
		t.Errorf("expected worktrees cleared on repo switch, got %d", len(m.Worktrees()))
	}
}

func TestModel_LeftPaneDownResetsRightPaneCursors(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // move branch cursor
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // branchSelected=2
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown}) // navigate to bravo
	if m.BranchSelected() != 0 {
		t.Errorf("expected branchSelected reset to 0, got %d", m.BranchSelected())
	}
}

func TestModel_LeftPaneModeKeysAreNoOps(t *testing.T) {
	m := model.New(testRepos())
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'b'}},
		{Type: tea.KeyRunes, Runes: []rune{'h'}},
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
	} {
		before := listRequests(m)
		m2, cmd := update(m, key)
		if m2.Mode() != 1 {
			t.Errorf("key %v changed mode in left pane: got %d", key, m2.Mode())
		}
		if m2.ActivePane() != 0 {
			t.Errorf("key %v changed active pane in left pane: got %d", key, m2.ActivePane())
		}
		if cmd != nil {
			t.Errorf("key %v produced cmd in left pane: %T", key, cmd)
		}
		assertListRequestsUnchanged(t, before, m2)
	}
}

func TestModel_RightArrowFromLeftPaneIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRight})

	if m.ActivePane() != 0 {
		t.Fatalf("ActivePane() = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("Mode() = %d, want worktrees", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("right from left pane produced cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_LeftArrowFromLeftPaneIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyLeft})

	if m.ActivePane() != 0 {
		t.Fatalf("ActivePane() = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("Mode() = %d, want worktrees", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("left from left pane produced cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_RightArrowFromLeftPaneInNonEdgeModeIsNoOp(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "main"}, {Name: "feature"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRight})

	if m.ActivePane() != 0 {
		t.Fatalf("ActivePane() = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeBranches {
		t.Fatalf("Mode() = %d, want branches", m.Mode())
	}
	if m.BranchSelected() != 1 {
		t.Fatalf("BranchSelected() = %d, want preserved cursor 1", m.BranchSelected())
	}
	if cmd != nil {
		t.Fatalf("right from left pane in branches produced cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_LeftArrowFromLeftPaneAtFlowsIsNoOp(t *testing.T) {
	flow := flowWithPhaseDetails()
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{
		flow,
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusPending},
	}, ListRequest: m.ListRequest(ui.ModeFlows)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyLeft})

	if m.ActivePane() != 0 {
		t.Fatalf("ActivePane() = %d, want left pane", m.ActivePane())
	}
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("Mode() = %d, want flows", m.Mode())
	}
	if m.FlowSelected() != 1 || m.ExpandedFlowID() != "flow-2" {
		t.Fatalf("flow state selected=%d expanded=%q, want selected 1 expanded flow-2", m.FlowSelected(), m.ExpandedFlowID())
	}
	if cmd != nil {
		t.Fatalf("left from left pane at flows produced cmd %T, want nil", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_LeftPaneActionKeysAreNoOps(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "feat", IsWorktree: true, Dirty: true, WorktreePaths: []string{"/dev/alpha"}},
	}})
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
		{Type: tea.KeyRunes, Runes: []rune{'t'}},
		{Type: tea.KeyRunes, Runes: []rune{'c'}},
	} {
		_, cmd := update(m, key)
		if cmd != nil {
			t.Errorf("key %v produced cmd in left pane: %T", key, cmd)
		}
	}
}

// --- Fuzzy filtering ---

func TestModel_SlashFiltersReposInLeftPane(t *testing.T) {
	m := model.New(testRepos())

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	if m.RepoSearch() != "c" {
		t.Fatalf("expected repo search query c, got %q", m.RepoSearch())
	}
	if cmd == nil {
		t.Fatal("expected fetch command when repo filter changes selection")
	}
	// The fetch targets the newly selected repo; both the success result and
	// the error message (fake repo paths fail) carry the repo path.
	var repoPath string
	switch msg := cmd().(type) {
	case model.WorktreeResultMsg:
		repoPath = msg.RepoPath
	case model.FetchErrorMsg:
		repoPath = msg.RepoPath
	default:
		t.Fatalf("expected WorktreeResultMsg or FetchErrorMsg, got %T", msg)
	}
	if repoPath != "/dev/charlie" {
		t.Fatalf("expected filtered selection to fetch charlie, got %q", repoPath)
	}

	view := m.View()
	if !strings.Contains(view, "charlie") {
		t.Error("filtered repo view should contain charlie")
	}
	for _, name := range []string{"alpha", "bravo"} {
		if strings.Contains(view, name) {
			t.Errorf("filtered repo view should not contain %s", name)
		}
	}
}

func TestModel_SearchActiveSuppressesHorizontalArrowNavigation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     func(model.Model) model.Model
		wantPane  int
		wantMode  ui.Mode
		wantQuery string
	}{
		{
			name: "left pane repo search",
			setup: func(m model.Model) model.Model {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
				return m
			},
			wantPane:  0,
			wantMode:  ui.ModeWorktrees,
			wantQuery: "a",
		},
		{
			name: "right pane item search",
			setup: func(m model.Model) model.Model {
				m = inRightPane(m)
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
				return m
			},
			wantPane:  1,
			wantMode:  ui.ModeWorktrees,
			wantQuery: "w",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(model.New(testRepos()))
			for _, key := range []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyRight}} {
				before := listRequests(m)
				m2, cmd := update(m, key)
				if cmd != nil {
					t.Fatalf("key %v produced cmd %T, want nil", key, cmd)
				}
				if !m2.SearchActive() {
					t.Fatalf("key %v deactivated search", key)
				}
				if m2.ActivePane() != tc.wantPane || m2.Mode() != tc.wantMode {
					t.Fatalf("key %v activePane=%d mode=%d, want pane=%d mode=%d", key, m2.ActivePane(), m2.Mode(), tc.wantPane, tc.wantMode)
				}
				query := m2.RepoSearch()
				if tc.wantPane == 1 {
					query = m2.ItemSearch()
				}
				if query != tc.wantQuery {
					t.Fatalf("key %v query=%q, want %q", key, query, tc.wantQuery)
				}
				assertListRequestsUnchanged(t, before, m2)
			}
		})
	}
}

func TestModel_ModalSuppressesPaneNavigationKeys(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatalf("opening worktree input produced cmd %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("Overlay() = %d, want worktree input", m.Overlay())
	}

	for _, key := range []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyF2}} {
		before := listRequests(m)
		m2, cmd := update(m, key)
		if cmd != nil {
			t.Fatalf("key %v produced cmd %T, want nil", key, cmd)
		}
		if m2.Overlay() != ui.OverlayWorktreeInput {
			t.Fatalf("key %v overlay=%d, want worktree input", key, m2.Overlay())
		}
		if m2.ActivePane() != 1 || m2.Mode() != ui.ModeWorktrees {
			t.Fatalf("key %v activePane=%d mode=%d, want right pane worktrees", key, m2.ActivePane(), m2.Mode())
		}
		if m2.WorktreeInput() != "" {
			t.Fatalf("key %v input=%q, want empty", key, m2.WorktreeInput())
		}
		assertListRequestsUnchanged(t, before, m2)
	}
}

func TestModel_EscapeClearsRepoFilterWithoutQuitting(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("expected esc to clear search without quitting or refetching, got cmd %T", cmd)
	}
	if m.RepoSearch() != "" || m.SearchActive() {
		t.Fatalf("expected repo filter cleared and inactive, got query=%q active=%v", m.RepoSearch(), m.SearchActive())
	}
	if m.Selected() != 2 {
		t.Fatalf("expected clearing filter to preserve charlie selection at index 2, got %d", m.Selected())
	}
}

func TestModel_EscapeClearsKeptRepoFilterWithoutChangingRepo(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("expected esc to clear kept search without refetching, got cmd %T", cmd)
	}
	if m.RepoSearch() != "" || m.SearchActive() {
		t.Fatalf("expected repo filter cleared and inactive, got query=%q active=%v", m.RepoSearch(), m.SearchActive())
	}
	if m.Selected() != 2 {
		t.Fatalf("expected clearing kept filter to preserve charlie selection at index 2, got %d", m.Selected())
	}
}

func TestModel_BackspaceOnEmptyRepoFilterInputClearsFilter(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.RepoSearch() != "" || !m.SearchActive() {
		t.Fatalf("expected empty active repo search before final backspace, got query=%q active=%v", m.RepoSearch(), m.SearchActive())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatalf("expected clearing empty repo search to produce no command, got %T", cmd)
	}
	if m.RepoSearch() != "" || m.SearchActive() {
		t.Fatalf("expected empty backspace to clear and deactivate repo search, got query=%q active=%v", m.RepoSearch(), m.SearchActive())
	}
	if m.Selected() != 2 {
		t.Fatalf("expected clearing empty repo filter to preserve charlie selection at index 2, got %d", m.Selected())
	}
}

func TestModel_ZeroRepoFilterHidesStaleRightPaneItems(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha-feature", BranchName: "feature/alpha", Dirty: true},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if cmd != nil {
		t.Fatalf("expected starting search to produce no command, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if cmd != nil {
		t.Fatalf("expected no fetch when repo filter has no matches, got %T", cmd)
	}

	view := m.View()
	if strings.Contains(view, "feature/alpha") || strings.Contains(view, "/dev/alpha-feature") {
		t.Fatal("zero-match repo filter should not render stale worktree rows")
	}

	m = inRightPane(m)
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected enter on stale hidden worktree to be a no-op, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay for stale hidden worktree, got %v", m.Overlay())
	}
}

func TestModel_SlashFiltersRightPaneItems(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "main"},
		{Name: "feature/auth"},
		{Name: "bugfix"},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	if m.ItemSearch() != "fa" {
		t.Fatalf("expected item search query fa, got %q", m.ItemSearch())
	}
	view := m.View()
	if !strings.Contains(view, "feature/auth") {
		t.Error("filtered branch view should contain feature/auth")
	}
	for _, name := range []string{"main", "bugfix"} {
		if strings.Contains(view, name) {
			t.Errorf("filtered branch view should not contain %s", name)
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.BranchSelected() != 0 {
		t.Errorf("single filtered item should wrap to index 0, got %d", m.BranchSelected())
	}
}

func TestModel_BackspaceOnEmptyItemFilterInputClearsFilter(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "main"},
		{Name: "feature/auth"},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.ItemSearch() != "" || !m.SearchActive() {
		t.Fatalf("expected empty active item search before final backspace, got query=%q active=%v", m.ItemSearch(), m.SearchActive())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatalf("expected clearing empty item search to produce no command, got %T", cmd)
	}
	if m.ItemSearch() != "" || m.SearchActive() {
		t.Fatalf("expected empty backspace to clear and deactivate item search, got query=%q active=%v", m.ItemSearch(), m.SearchActive())
	}
}

func TestModel_RightPaneSearchIsPerSubviewAndEscapeClearsIt(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "main"},
		{Name: "feature/auth"},
		{Name: "bugfix"},
	}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: []gitquery.Stash{
		{Index: 0, Date: "2026-03-18", Message: "zebra work"},
		{Index: 1, Date: "2026-03-18", Message: "bugfix stash"},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "auth" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if rows := m.Rows(); len(rows) != 1 || rows[0].Branch.Name != "feature/auth" {
		t.Fatalf("expected branch filter to leave feature/auth, got %#v", rows)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.ItemSearch() != "" {
		t.Fatalf("expected stashes to keep their own empty filter, got %q", m.ItemSearch())
	}
	if stashes := m.Stashes(); len(stashes) != 2 {
		t.Fatalf("expected stashes unfiltered by the branch query, got %#v", stashes)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.ItemSearch() != "auth" {
		t.Fatalf("expected branch filter restored on subview re-entry, got %q", m.ItemSearch())
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("expected esc to clear right-pane search without quitting, got %T", cmd)
	}
	if m.ItemSearch() != "" || m.SearchActive() {
		t.Fatalf("expected right-pane filter cleared and inactive, got query=%q active=%v", m.ItemSearch(), m.SearchActive())
	}
	view := m.View()
	for _, name := range []string{"main", "feature/auth", "bugfix"} {
		if !strings.Contains(view, name) {
			t.Fatalf("clearing the filter should restore branch %s", name)
		}
	}
}

func TestModel_RightPaneFilterAppliesToAsyncReplacement(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "feature/auth"},
		{Name: "bugfix"},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "api" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: []gitquery.Branch{
		{Name: "feature/api"},
		{Name: "release"},
		{Name: "bugfix"},
	}})

	if m.ItemSearch() != "api" {
		t.Fatalf("expected async replacement to keep item search api, got %q", m.ItemSearch())
	}
	view := m.View()
	if !strings.Contains(view, "feature/api") {
		t.Fatal("active filter should apply to replacement branch result")
	}
	for _, name := range []string{"release", "bugfix"} {
		if strings.Contains(view, name) {
			t.Fatalf("active filter should hide replacement branch %s", name)
		}
	}
	if m.BranchSelected() != 0 {
		t.Fatalf("expected filtered replacement to clamp selection to top, got %d", m.BranchSelected())
	}
}

// --- Right pane navigation ---

func TestModel_RightPaneUpDownDoesNotMoveRepoSelection(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=1
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.Selected() != 1 {
		t.Errorf("expected selected unchanged at 1 in right pane, got %d", m.Selected())
	}
}

func TestModel_UpDownNavigatesAllBranches(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "clean-1"},
		{Name: "dirty-1", IsWorktree: true, Dirty: true, WorktreePaths: []string{"/dev/alpha"}},
		{Name: "clean-2"},
		{Name: "dirty-2", IsWorktree: true, Dirty: true, WorktreePaths: []string{"/dev/alpha"}},
	}
	m := model.New(testRepos())
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	if m.BranchSelected() != 0 {
		t.Errorf("expected cursor at 0, got %d", m.BranchSelected())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.BranchSelected() != 1 {
		t.Errorf("expected cursor at 1, got %d", m.BranchSelected())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.BranchSelected() != 2 {
		t.Errorf("expected cursor at 2, got %d", m.BranchSelected())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.BranchSelected() != 3 {
		t.Errorf("expected cursor at 3, got %d", m.BranchSelected())
	}
	// Wrap to first
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.BranchSelected() != 0 {
		t.Errorf("expected cursor to wrap to 0, got %d", m.BranchSelected())
	}
	// Wrap backward to last
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.BranchSelected() != 3 {
		t.Errorf("expected cursor to wrap to 3, got %d", m.BranchSelected())
	}
}

func TestModel_StashCursorWraps(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	// Wrap backward from 0 to last
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.StashSelected() != 2 {
		t.Errorf("expected StashSelected to wrap to 2, got %d", m.StashSelected())
	}
	// Wrap forward from last to 0
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.StashSelected() != 0 {
		t.Errorf("expected StashSelected to wrap to 0, got %d", m.StashSelected())
	}
}

func TestModel_BranchScrollFollowsCursor(t *testing.T) {
	// Create 10 branches, terminal height only shows 3
	branches := make([]gitquery.Branch, 10)
	for i := range branches {
		branches[i] = gitquery.Branch{Name: fmt.Sprintf("branch-%d", i)}
	}
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BranchContentOverhead + 3}) // 3 content lines
	m = inBranchesMode(m)
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	// Cursor starts at 0, scroll at 0
	if m.BranchScroll() != 0 {
		t.Errorf("expected scroll 0 at start, got %d", m.BranchScroll())
	}
	// Move cursor down past the viewport
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.BranchSelected() != 9 {
		t.Errorf("expected cursor at 9, got %d", m.BranchSelected())
	}
	// Scroll should have advanced to show cursor
	if m.BranchScroll() == 0 {
		t.Error("expected scroll to advance when cursor moves past viewport")
	}
	// Cursor must be within [scroll, scroll+contentHeight)
	contentHeight := 3
	if m.BranchSelected() < m.BranchScroll() || m.BranchSelected() >= m.BranchScroll()+contentHeight {
		t.Errorf("cursor %d not in scroll viewport [%d, %d)", m.BranchSelected(), m.BranchScroll(), m.BranchScroll()+contentHeight)
	}

	// Move back up to 0
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.BranchSelected() != 0 {
		t.Errorf("expected cursor back at 0, got %d", m.BranchSelected())
	}
	if m.BranchScroll() != 0 {
		t.Errorf("expected scroll back to 0, got %d", m.BranchScroll())
	}
}

func TestModel_RepoScrollFollowsCursor(t *testing.T) {
	// Create 10 repos, terminal height only shows 3
	repos := make([]scanner.Repo, 10)
	for i := range repos {
		repos[i] = scanner.Repo{Path: fmt.Sprintf("/dev/repo-%d", i), DisplayName: fmt.Sprintf("repo-%d", i)}
	}
	contentHeight := 3
	m := model.New(repos)
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.RepoContentOverhead + contentHeight})

	// Cursor starts at 0, scroll at 0
	if m.RepoScroll() != 0 {
		t.Errorf("expected scroll 0 at start, got %d", m.RepoScroll())
	}

	// Move cursor down past the viewport
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.Selected() != 9 {
		t.Errorf("expected cursor at 9, got %d", m.Selected())
	}
	// Scroll should have advanced to show cursor
	if m.RepoScroll() == 0 {
		t.Error("expected scroll to advance when cursor moves past viewport")
	}
	// Cursor must be within [scroll, scroll+contentHeight)
	if m.Selected() < m.RepoScroll() || m.Selected() >= m.RepoScroll()+contentHeight {
		t.Errorf("cursor %d not in scroll viewport [%d, %d)", m.Selected(), m.RepoScroll(), m.RepoScroll()+contentHeight)
	}

	// Move back up to 0
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.Selected() != 0 {
		t.Errorf("expected cursor back at 0, got %d", m.Selected())
	}
	if m.RepoScroll() != 0 {
		t.Errorf("expected scroll back to 0, got %d", m.RepoScroll())
	}
}

func TestModel_RepoScrollWrapsFromTopToBottom(t *testing.T) {
	repos := make([]scanner.Repo, 10)
	for i := range repos {
		repos[i] = scanner.Repo{Path: fmt.Sprintf("/dev/repo-%d", i), DisplayName: fmt.Sprintf("repo-%d", i)}
	}
	contentHeight := 3
	m := model.New(repos)
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.RepoContentOverhead + contentHeight})

	// Press Up from index 0 — should wrap to last repo
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.Selected() != 9 {
		t.Errorf("expected cursor at 9 after wrap, got %d", m.Selected())
	}
	// Scroll should position last repo in viewport
	if m.Selected() < m.RepoScroll() || m.Selected() >= m.RepoScroll()+contentHeight {
		t.Errorf("cursor %d not in scroll viewport [%d, %d)", m.Selected(), m.RepoScroll(), m.RepoScroll()+contentHeight)
	}
}

func TestModel_RepoScrollWrapsFromBottomToTop(t *testing.T) {
	repos := make([]scanner.Repo, 10)
	for i := range repos {
		repos[i] = scanner.Repo{Path: fmt.Sprintf("/dev/repo-%d", i), DisplayName: fmt.Sprintf("repo-%d", i)}
	}
	contentHeight := 3
	m := model.New(repos)
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.RepoContentOverhead + contentHeight})

	// Navigate to last repo
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	// Press Down — should wrap to first repo
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.Selected() != 0 {
		t.Errorf("expected cursor at 0 after wrap, got %d", m.Selected())
	}
	if m.RepoScroll() != 0 {
		t.Errorf("expected scroll at 0 after wrap to top, got %d", m.RepoScroll())
	}
}

func TestModel_StashScrollFollowsCursor(t *testing.T) {
	// Create 10 stashes, terminal height only shows 3 content lines
	stashes := make([]gitquery.Stash, 10)
	for i := range stashes {
		stashes[i] = gitquery.Stash{Index: i, Date: "2026-03-18", Message: fmt.Sprintf("stash-%d", i)}
	}
	contentHeight := 3
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BranchContentOverhead + contentHeight})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: stashes})

	if m.StashScroll() != 0 {
		t.Errorf("expected scroll 0 at start, got %d", m.StashScroll())
	}

	// Move cursor down past the viewport
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.StashSelected() != 9 {
		t.Errorf("expected cursor at 9, got %d", m.StashSelected())
	}
	if m.StashScroll() == 0 {
		t.Error("expected scroll to advance when cursor moves past viewport")
	}
	// Compute the visual line of the selected stash (sum of line counts for all preceding stashes)
	visLine := 0
	for i, s := range stashes {
		if i == m.StashSelected() {
			break
		}
		visLine += ui.StashLineCount(s.Message, 80-ui.LeftPaneWidth-2)
	}
	if visLine < m.StashScroll() || visLine >= m.StashScroll()+contentHeight {
		t.Errorf("visual line %d not in scroll viewport [%d, %d)", visLine, m.StashScroll(), m.StashScroll()+contentHeight)
	}

	// Move back up to 0
	for i := 0; i < 9; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.StashScroll() != 0 {
		t.Errorf("expected scroll back to 0, got %d", m.StashScroll())
	}
}

func TestModel_StashScrollAccountsForLongMessages(t *testing.T) {
	// Stashes with long messages take 2 lines each
	longMsg := "this is a very long stash message that will definitely wrap to two lines in a narrow pane"
	stashes := make([]gitquery.Stash, 5)
	for i := range stashes {
		stashes[i] = gitquery.Stash{Index: i, Date: "2026-03-18", Message: longMsg}
	}
	// Width 50: prefix is 15 chars, message gets 35 chars, longMsg overflows → 2 lines each
	// 3 content lines → only ~1.5 stashes visible at a time
	contentHeight := 3
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 50 + ui.LeftPaneWidth + 2, Height: ui.BranchContentOverhead + contentHeight})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: stashes})

	// Move to stash 2 (each takes 2 lines, so stash 2 starts at visual line 4)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.StashSelected() != 2 {
		t.Errorf("expected cursor at 2, got %d", m.StashSelected())
	}
	// Scroll should have advanced since stash 2 starts at line 4, viewport is only 3 lines
	if m.StashScroll() == 0 {
		t.Error("expected scroll to advance for long-message stashes")
	}
}

func TestModel_StashCursorUsesStashViewportAtTinyHeight(t *testing.T) {
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18", Message: "first"},
		{Index: 1, Date: "2026-03-18", Message: "second"},
		{Index: 2, Date: "2026-03-18", Message: "third"},
	}
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.StashContentOverhead})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: stashes})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.StashSelected() != 1 {
		t.Fatalf("expected stash cursor at 1, got %d", m.StashSelected())
	}
	if m.StashScroll() != 1 {
		t.Fatalf("expected one-line stash viewport to scroll to 1, got %d", m.StashScroll())
	}
}

// --- Mode switching ---

func TestModel_ModeSwitchOnKeyPress(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.Mode() != 3 {
		t.Errorf("expected mode 3 (stashes), got %d", m.Mode())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.Mode() != 1 {
		t.Errorf("expected mode 1 (worktrees), got %d", m.Mode())
	}
}

func TestModel_Key4SwitchesToHistoryMode(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.Mode() != 4 {
		t.Errorf("expected mode 4, got %d", m.Mode())
	}
}

func TestModel_SwitchToHistoryFiresFetchCommits(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if cmd == nil {
		t.Fatal("expected fetchCommits cmd on switch to mode 4, got nil")
	}
}

func TestModel_NumberKeysSwitchToCorrectModes(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)

	// Key 2 → ModeBranches
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.Mode() != 2 {
		t.Errorf("key 2: expected mode 2 (ModeBranches), got %d", m.Mode())
	}

	// Key 3 → ModeStashes
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.Mode() != 3 {
		t.Errorf("key 3: expected mode 3 (ModeStashes), got %d", m.Mode())
	}

	// Key 4 → ModeHistory
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.Mode() != 4 {
		t.Errorf("key 4: expected mode 4 (ModeHistory), got %d", m.Mode())
	}

	// Key w → ModeWorktrees
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.Mode() != 1 {
		t.Errorf("key w: expected mode 1 (ModeWorktrees), got %d", m.Mode())
	}
}

func TestModel_Key5SwitchesToReflog(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.Mode() != 5 {
		t.Errorf("expected mode 5 (reflog), got %d", m.Mode())
	}
}

func TestModel_Key6SwitchesToSessions(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.Mode() != ui.ModeSessions {
		t.Errorf("expected sessions mode, got %d", m.Mode())
	}
}

func TestModel_PressingCurrentModeKeyNoFetch(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	// Already in mode 1 (worktrees); pressing 1 should not fire a redundant fetch
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd != nil {
		t.Error("pressing 1 while already in mode 1 should not fire fetch")
	}
}

func TestModel_ModeSwitchPreservesSelection(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})                      // select bravo (left pane)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})                     // switch to right pane
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // mode 3 (stashes)
	if m.Selected() != 1 {
		t.Errorf("expected selection preserved at 1, got %d", m.Selected())
	}
}

func TestModel_RightFromWorktreesSwitchesToBranches(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != 2 {
		t.Errorf("expected mode 2 (branches), got %d", m.Mode())
	}
}

func TestModel_LeftFromBranchesSwitchesToWorktrees(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight}) // branches
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})  // worktrees
	if m.Mode() != 1 {
		t.Errorf("expected mode 1 (worktrees), got %d", m.Mode())
	}
}

func TestModel_RightCyclesGitSubviewsAndWrapsToWorktrees(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("expected starting mode worktrees, got %d", m.Mode())
	}

	for want := ui.ModeBranches; want <= ui.ModeReflog; want++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
		if m.Mode() != want {
			t.Fatalf("mode after right = %d, want %d", m.Mode(), want)
		}
		if m.ActivePane() != 1 {
			t.Fatalf("ActivePane() = %d, want right pane while moving through subviews", m.ActivePane())
		}
	}

	before := listRequests(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("Mode() = %d, want worktrees after wrapping", m.Mode())
	}
	if m.ActivePane() != 1 {
		t.Fatalf("ActivePane() = %d, want right pane", m.ActivePane())
	}
	if cmd == nil {
		t.Fatal("right from reflog produced nil cmd, want worktrees fetch")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeWorktrees)
	msgs := runBatchCmd(t, cmd)
	if !hasListFetchForMode(msgs, ui.ModeWorktrees, m.ListRequest(ui.ModeWorktrees)) {
		t.Fatalf("right from reflog command messages = %#v, want worktrees fetch for request %d", msgs, m.ListRequest(ui.ModeWorktrees))
	}
}

func TestModel_NumberedModeSwitchClearsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.ActionFailedMsg{RepoPath: "/dev/alpha", Err: "operation failed"})
	if got := m.TransientError(); got != "operation failed" {
		t.Fatalf("TransientError() = %q, want operation failed", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if got := m.TransientError(); got != "" {
		t.Fatalf("numbered mode switch left status %q, want cleared", got)
	}
}

func TestModel_LeftCyclesTopLevelViewsIntoGitThenWrapsGitSubviews(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	for _, want := range []ui.Mode{ui.ModeFlows, ui.ModePlans, ui.ModeSessions, ui.ModeWorktrees} {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
		if m.Mode() != want {
			t.Fatalf("mode after left = %d, want %d", m.Mode(), want)
		}
		if m.ActivePane() != 1 {
			t.Fatalf("ActivePane() = %d, want right pane while moving through views", m.ActivePane())
		}
	}

	before := listRequests(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.Mode() != ui.ModeReflog {
		t.Fatalf("Mode() = %d, want reflog: arrows wrap inside the Git view without spilling out", m.Mode())
	}
	if m.ActivePane() != 1 {
		t.Fatalf("ActivePane() = %d, want right pane", m.ActivePane())
	}
	if cmd == nil {
		t.Fatal("left from worktrees produced nil cmd, want reflog fetch")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeReflog)
	msgs := runBatchCmd(t, cmd)
	if !hasListFetchForMode(msgs, ui.ModeReflog, m.ListRequest(ui.ModeReflog)) {
		t.Fatalf("left from worktrees command messages = %#v, want reflog fetch for request %d", msgs, m.ListRequest(ui.ModeReflog))
	}
}

func TestModel_ArrowNavigationWrapsAtModeEdges(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{RepoPath: "/dev/alpha", Worktrees: []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-worktrees/feature", BranchName: "feature"},
	}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.Mode() != ui.ModeReflog {
		t.Fatalf("Mode() = %d, want reflog after wrapping inside the Git view", m.Mode())
	}
	if m.ActivePane() != 1 {
		t.Fatalf("ActivePane() = %d, want right pane", m.ActivePane())
	}
	if m.WorktreeSelected() != 1 {
		t.Fatalf("WorktreeSelected() = %d, want preserved cursor 1", m.WorktreeSelected())
	}
	if cmd == nil {
		t.Fatal("left from worktrees produced nil cmd, want reflog fetch")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeReflog)
	msgs := runBatchCmd(t, cmd)
	if !hasListFetchForMode(msgs, ui.ModeReflog, m.ListRequest(ui.ModeReflog)) {
		t.Fatalf("left from worktrees command messages = %#v, want reflog fetch for request %d", msgs, m.ListRequest(ui.ModeReflog))
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	flow := flowWithPhaseDetails()
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{
		flow,
		{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Second flow", Status: flowstore.StatusPending},
	}, ListRequest: m.ListRequest(ui.ModeFlows)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	before = listRequests(m)

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("Mode() = %d, want active flows after right from flows", m.Mode())
	}
	if m.ActivePane() != 1 {
		t.Fatalf("ActivePane() = %d, want right pane", m.ActivePane())
	}
	if cmd == nil {
		t.Fatal("right from flows produced nil cmd, want active flows fetch")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeActiveFlows)
	msgs = runBatchCmd(t, cmd)
	if !hasListFetchForMode(msgs, ui.ModeActiveFlows, m.ListRequest(ui.ModeActiveFlows)) {
		t.Fatalf("right from flows command messages = %#v, want active flows fetch for request %d", msgs, m.ListRequest(ui.ModeActiveFlows))
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeReflog {
		t.Fatalf("Mode() = %d, want reflog: Git re-entry lands on the last-used subview", m.Mode())
	}
	if m.FlowSelected() != 0 || m.ExpandedFlowID() != "" {
		t.Fatalf("flow state selected=%d expanded=%q, want reset after leaving the flow surface", m.FlowSelected(), m.ExpandedFlowID())
	}
}

func TestModel_HKeyOpensHistorySubviewAndLAliasesRightInFlows(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.ActivePane() != 1 || m.Mode() != ui.ModeHistory {
		t.Fatalf("h at worktrees activePane=%d mode=%d, want right pane history", m.ActivePane(), m.Mode())
	}
	if cmd == nil {
		t.Fatal("h at worktrees produced nil cmd, want history fetch")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeHistory)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	before = listRequests(m)
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if m.ActivePane() != 1 || m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("l at flows activePane=%d mode=%d, want right pane active flows", m.ActivePane(), m.Mode())
	}
	if cmd == nil {
		t.Fatal("l at flows produced nil cmd, want active flows fetch")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeActiveFlows)
}

func TestModel_RightFromStashesGoesToHistory(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // stashes
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})                     // history
	if m.Mode() != 4 {
		t.Errorf("expected mode 4, got %d", m.Mode())
	}
}

func TestModel_LeftFromHistoryGoesToStashes(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}) // history
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})                      // stashes
	if m.Mode() != 3 {
		t.Errorf("expected mode 3, got %d", m.Mode())
	}
}

func TestModel_ModeSwitchViaArrowFiresFetch(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	// Right to mode 2 (branches) should fetch branches
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRight})
	if cmd == nil {
		t.Fatal("expected fetch cmd on mode switch to branches, got nil")
	}
	// Right to mode 3 (stashes) should fetch stashes
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if cmd == nil {
		t.Fatal("expected fetch cmd on mode switch to stashes, got nil")
	}
}

func TestModel_SwitchToBranchesFiresFetchBranches(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) // stashes
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if cmd == nil {
		t.Fatal("expected fetch cmd on switch to mode 2, got nil")
	}
}

func TestModel_SwitchToStashesFiresFetchStashes(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("expected fetchStashes cmd on switch to mode 3, got nil")
	}
}

// --- Message handlers ---

func TestModel_BranchResultUpdatesState(t *testing.T) {
	m := model.New(testRepos())
	branches := []gitquery.Branch{
		{Name: "main", HasUpstream: true},
		{Name: "feature", HasUpstream: true, Ahead: 1},
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})
	if len(m.Rows()) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(m.Rows()))
	}
	if m.Rows()[0].Branch.Name != "main" {
		t.Errorf("expected first branch 'main', got %q", m.Rows()[0].Branch.Name)
	}
}

func TestModel_StaleBranchResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	branches := []gitquery.Branch{{Name: "main"}}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})
	if len(m.Rows()) != 0 {
		t.Errorf("expected stale result discarded, got %d rows", len(m.Rows()))
	}
}

func TestModel_StashResultUpdatesState(t *testing.T) {
	m := model.New(testRepos())
	stashes := testStashes()
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: stashes})
	if len(m.Stashes()) != 3 {
		t.Fatalf("expected 3 stashes, got %d", len(m.Stashes()))
	}
}

func TestModel_StaleStashResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	m, _ = update(m, model.StashResultMsg{RepoPath: "/dev/alpha", Stashes: testStashes()})
	if len(m.Stashes()) != 0 {
		t.Errorf("expected stale stash result discarded, got %d stashes", len(m.Stashes()))
	}
}

func TestModel_StaleBranchDiffResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo

	m, _ = update(m, model.BranchDiffResultMsg{
		RepoPath:   "/dev/alpha",
		BranchName: "feat",
		Diff:       "diff --git a/f.txt b/f.txt",
	})

	if m.OverlayDiff() != "" {
		t.Errorf("expected stale branch diff discarded, got %q", m.OverlayDiff())
	}
}

func TestModel_BranchDeletedMsgTriggersFetch(t *testing.T) {
	m := model.New(testRepos())
	_, cmd := update(m, model.BranchDeletedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected fetchBranches cmd after BranchDeletedMsg, got nil")
	}
}

func TestModel_StaleBranchDeletedMsgIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	_, cmd := update(m, model.BranchDeletedMsg{RepoPath: "/dev/alpha"})
	if cmd != nil {
		t.Error("expected stale BranchDeletedMsg to be ignored")
	}
}

func TestModel_StashDroppedMsgTriggersStashFetch(t *testing.T) {
	m := model.New(testRepos())
	_, cmd := update(m, model.StashDroppedMsg{RepoPath: "/dev/alpha"})
	if cmd == nil {
		t.Fatal("expected fetchStashes cmd after StashDroppedMsg, got nil")
	}
}

// --- Commit result handlers ---

func testCommits() []gitquery.Commit {
	return []gitquery.Commit{
		{Hash: "abc1234", Author: "alice", Date: "2 hours ago", Subject: "Fix login bug"},
		{Hash: "def5678", Author: "bob", Date: "3 days ago", Subject: "Add profile page"},
		{Hash: "ghi9012", Author: "alice", Date: "1 week ago", Subject: "Refactor DB layer"},
	}
}

func TestModel_CommitResultUpdatesState(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	if len(m.Commits()) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(m.Commits()))
	}
}

func TestModel_StaleCommitResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	if len(m.Commits()) != 0 {
		t.Errorf("expected stale commit result discarded, got %d commits", len(m.Commits()))
	}
}

func TestModel_CommitCursorWraps(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	// Wrap backward from 0 to last
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.CommitSelected() != 2 {
		t.Errorf("expected CommitSelected to wrap to 2, got %d", m.CommitSelected())
	}
	// Wrap forward from last to 0
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.CommitSelected() != 0 {
		t.Errorf("expected CommitSelected to wrap to 0, got %d", m.CommitSelected())
	}
}

func TestModel_CommitScrollFollowsCursor(t *testing.T) {
	commits := make([]gitquery.Commit, 20)
	for i := range commits {
		commits[i] = gitquery.Commit{Hash: fmt.Sprintf("abc%04d", i), Author: "test", Date: "now", Subject: fmt.Sprintf("commit %d", i)}
	}
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BranchContentOverhead + 3})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: commits})

	// Move cursor past viewport
	for i := 0; i < 10; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.CommitScroll() == 0 {
		t.Error("expected scroll to advance when cursor moves past viewport")
	}
}

func TestModel_ModeSwitchPreservesCommitCursors(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.CommitSelected() != 2 {
		t.Fatalf("expected CommitSelected 2, got %d", m.CommitSelected())
	}
	// Switch away and back
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.CommitSelected() != 2 {
		t.Errorf("expected CommitSelected preserved at 2, got %d", m.CommitSelected())
	}
}

func TestModel_RepoSwitchClearsCommits(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()})
	if len(m.Commits()) != 3 {
		t.Fatal("expected 3 commits")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if len(m.Commits()) != 0 {
		t.Errorf("expected commits cleared on repo switch, got %d", len(m.Commits()))
	}
}

func TestModel_StaleStashDroppedMsgIgnored(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	_, cmd := update(m, model.StashDroppedMsg{RepoPath: "/dev/alpha"})
	if cmd != nil {
		t.Error("expected stale StashDroppedMsg to be ignored")
	}
}

// --- Branch filtering ---

func TestModel_WorktreeBranchesFilteredFromBranchView(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	branches := []gitquery.Branch{
		{Name: "feat-a"},
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
		{Name: "wt-branch", Merged: true, MergedInto: "main", IsWorktree: true, WorktreePaths: []string{"/dev/alpha-wt"}},
		{Name: "feat-b"},
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	rows := m.Rows()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (root + 2 non-worktree), got %d", len(rows))
	}
	for _, row := range rows {
		if row.Branch.Name == "wt-branch" {
			t.Error("non-root worktree branch should be filtered out")
		}
	}
}

func TestModel_RootBranchPinnedToPositionZero(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	branches := []gitquery.Branch{
		{Name: "aaa-branch"},
		{Name: "mmm-branch"},
		{Name: "zzz-root", IsWorktree: true, WorktreePaths: []string{"/dev/alpha"}},
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	rows := m.Rows()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Branch.Name != "zzz-root" {
		t.Errorf("expected root branch pinned to position 0, got %q", rows[0].Branch.Name)
	}
	if rows[0].WorktreePath != "/dev/alpha" {
		t.Errorf("expected root row WorktreePath=/dev/alpha, got %q", rows[0].WorktreePath)
	}
}

func TestModel_NoRootBranchDoesNotPanic(t *testing.T) {
	m := model.New(testRepos())
	m = inBranchesMode(m)
	branches := []gitquery.Branch{
		{Name: "feat-a"},
		{Name: "feat-b"},
	}
	m, _ = update(m, model.BranchResultMsg{RepoPath: "/dev/alpha", Branches: branches})

	rows := m.Rows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Branch.Name != "feat-a" {
		t.Errorf("expected original order preserved, got %q first", rows[0].Branch.Name)
	}
}

// --- Reflog tests ---

func testReflogs() []gitquery.ReflogEntry {
	return []gitquery.ReflogEntry{
		{Hash: "abc1234", Selector: "HEAD@{0}", Date: "2 hours ago", Subject: "commit: Fix login bug"},
		{Hash: "def5678", Selector: "HEAD@{1}", Date: "3 days ago", Subject: "checkout: moving from main to feature"},
		{Hash: "ghi9012", Selector: "HEAD@{2}", Date: "1 week ago", Subject: "commit (initial): init"},
	}
}

func TestModel_ReflogResultUpdatesState(t *testing.T) {
	m := model.New(testRepos())
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()})
	if len(m.Reflogs()) != 3 {
		t.Fatalf("expected 3 reflogs, got %d", len(m.Reflogs()))
	}
}

func TestModel_SwitchToReflogFiresFetch(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.Mode() != 5 {
		t.Fatalf("expected mode 5, got %d", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
}

func TestModel_StaleReflogResultDiscarded(t *testing.T) {
	m := model.New(testRepos())
	m = selectBravo(m) // selected=bravo
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()})
	if len(m.Reflogs()) != 0 {
		t.Errorf("expected stale reflog result discarded, got %d reflogs", len(m.Reflogs()))
	}
}

func TestModel_ReflogCursorWraps(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()})
	// Wrap backward from 0 to last
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.ReflogSelected() != 2 {
		t.Errorf("expected ReflogSelected to wrap to 2, got %d", m.ReflogSelected())
	}
	// Wrap forward from last to 0
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.ReflogSelected() != 0 {
		t.Errorf("expected ReflogSelected to wrap to 0, got %d", m.ReflogSelected())
	}
}

func TestModel_ReflogScrollFollowsCursor(t *testing.T) {
	var entries []gitquery.ReflogEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, gitquery.ReflogEntry{
			Hash:     fmt.Sprintf("abc%04d", i),
			Selector: fmt.Sprintf("HEAD@{%d}", i),
			Date:     "2 hours ago",
			Subject:  fmt.Sprintf("commit: change %d", i),
		})
	}
	m := model.New(testRepos())
	m, _ = update(m, tea.WindowSizeMsg{Width: 120, Height: ui.BranchContentOverhead + 3})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: entries})
	for i := 0; i < 10; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.ReflogScroll() == 0 {
		t.Error("expected reflog scroll to advance, got 0")
	}
}

func TestModel_RepoSwitchClearsReflogs(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()})
	if len(m.Reflogs()) != 3 {
		t.Fatalf("expected 3 reflogs loaded, got %d", len(m.Reflogs()))
	}
	// Switch to left pane and navigate down
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if len(m.Reflogs()) != 0 {
		t.Errorf("expected reflogs cleared on repo switch, got %d", len(m.Reflogs()))
	}
}
