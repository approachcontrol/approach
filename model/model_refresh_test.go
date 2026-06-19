package model_test

import (
	"errors"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func TestModel_F5RefreshesReposAndCurrentFetchBackedMode(t *testing.T) {
	modes := []ui.Mode{
		ui.ModeWorktrees,
		ui.ModeBranches,
		ui.ModeStashes,
		ui.ModeHistory,
		ui.ModeReflog,
		ui.ModeSessions,
		ui.ModePlans,
		ui.ModeFlows,
		ui.ModeActiveFlows,
	}

	for _, mode := range modes {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			scans := 0
			m := model.NewWithOptions(testRepos(), model.Options{
				StartupMode: mode,
				ScanRepos: func() ([]scanner.Repo, error) {
					scans++
					return testRepos(), nil
				},
				ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
					return []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: "s1", RepoPath: filter.RepoPath}}, nil
				},
				ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
					return []planstore.PlanRecord{{PlanID: "p1", RepoPath: filter.RepoPath, Title: "Plan"}}, nil
				},
				ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return []flowstore.FlowRecord{{FlowID: "f1", RepoPath: filter.RepoPath, Title: "Flow"}}, nil
				},
			})
			m = inRightPane(m)
			beforeRequest := m.ListRequest(mode)

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
			if scans != 0 {
				t.Fatalf("scanner ran before command execution: %d call(s)", scans)
			}
			if got := m.ListRequest(mode); got == beforeRequest {
				t.Fatalf("ListRequest(%v) did not advance after f5", mode)
			}

			msgs := runBatchCmd(t, cmd)
			if scans != 1 {
				t.Fatalf("scanner ran %d times, want 1", scans)
			}
			if !hasRepoRefreshResult(msgs) {
				t.Fatalf("refresh command messages = %#v, want RepoRefreshResultMsg", msgs)
			}
			if !hasListFetchForMode(msgs, mode, m.ListRequest(mode)) {
				t.Fatalf("refresh command messages = %#v, want list fetch for mode %v request %d", msgs, mode, m.ListRequest(mode))
			}
		})
	}
}

func TestModel_F5WithNoScannerReportsUnavailable(t *testing.T) {
	m := model.New(testRepos())

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})

	if cmd != nil {
		t.Fatal("expected no command without refresh scanner")
	}
	if got := m.TransientError(); got != "refresh unavailable" {
		t.Fatalf("TransientError() = %q, want refresh unavailable", got)
	}
}

func TestModel_F5DuringModalOrSearchDoesNotRefresh(t *testing.T) {
	scans := 0
	opts := model.Options{
		ScanRepos: func() ([]scanner.Repo, error) {
			scans++
			return testRepos(), nil
		},
	}

	searchModel := model.NewWithOptions(testRepos(), opts)
	searchModel, _ = update(searchModel, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	searchModel, cmd := update(searchModel, tea.KeyMsg{Type: tea.KeyF5})
	if cmd != nil {
		t.Fatal("search f5 should not start refresh command")
	}
	if scans != 0 {
		t.Fatalf("scanner ran during search: %d", scans)
	}
	if !searchModel.SearchActive() {
		t.Fatal("f5 should leave search input active")
	}

	modalModel := model.NewWithOptions(testRepos(), opts)
	modalModel = inRightPane(modalModel)
	modalModel, _ = update(modalModel, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if modalModel.Overlay() == ui.OverlayNone {
		t.Fatal("expected modal to open")
	}
	modalModel, cmd = update(modalModel, tea.KeyMsg{Type: tea.KeyF5})
	if cmd != nil {
		t.Fatal("modal f5 should not start refresh command")
	}
	if scans != 0 {
		t.Fatalf("scanner ran during modal: %d", scans)
	}
}

func TestModel_RepoRefreshFailureLeavesReposAndShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) {
			return nil, errors.New("scan failed")
		},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
	msgs := runBatchCmd(t, cmd)
	var failure tea.Msg
	for _, msg := range msgs {
		if _, ok := msg.(model.RepoRefreshFailedMsg); ok {
			failure = msg
			break
		}
	}
	if failure == nil {
		t.Fatalf("refresh messages = %#v, want scan failure", msgs)
	}
	m, _ = update(m, failure)

	if got := m.TransientError(); got != "failed to refresh repos: scan failed" {
		t.Fatalf("TransientError() = %q, want scan failure", got)
	}
	if got := m.Selected(); got != 0 {
		t.Fatalf("Selected() = %d, want existing selection intact", got)
	}
}

func TestModel_RepoRefreshPreservesSelectionAndKeepsCurrentListWhileFetchPending(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		StartupMode: ui.ModeSessions,
		ScanRepos: func() ([]scanner.Repo, error) {
			return []scanner.Repo{
				{Path: "/dev/alpha", DisplayName: "alpha"},
				{Path: "/dev/bravo", DisplayName: "bravo-renamed"},
				{Path: "/dev/delta", DisplayName: "delta"},
			}, nil
		},
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: "fresh", RepoPath: filter.RepoPath}}, nil
		},
	})
	m = selectBravo(m)
	m, _ = update(m, model.SessionResultMsg{
		RepoPath:    "/dev/bravo",
		ListRequest: m.ListRequest(ui.ModeSessions),
		Sessions: []sessions.SessionRecord{
			{Provider: sessions.ProviderCodex, SessionID: "existing", RepoPath: "/dev/bravo"},
		},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
	scanMsg := repoRefreshResultFromBatch(t, runBatchCmd(t, cmd))
	m, followup := update(m, scanMsg)
	if followup != nil {
		t.Fatal("unchanged selected repo should not start a second post-scan fetch")
	}
	if got := m.Sessions(); len(got) != 1 || got[0].SessionID != "existing" {
		t.Fatalf("Sessions() after scan = %#v, want existing list while refresh fetch is pending", got)
	}
}

func TestModel_F5RefreshesOpenInlineWorktreeSessionsAfterWorktreeListRefresh(t *testing.T) {
	var gotFilters []sessions.SessionFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		StartupMode: ui.ModeWorktrees,
		ScanRepos: func() ([]scanner.Repo, error) {
			return testRepos(), nil
		},
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			gotFilters = append(gotFilters, filter)
			return []sessions.SessionRecord{{
				Provider:     sessions.ProviderCodex,
				SessionID:    fmt.Sprintf("inline-refresh-%d", len(gotFilters)),
				RepoPath:     filter.RepoPath,
				WorktreePath: filter.WorktreePath,
				Branch:       "feature/inline",
			}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.WorktreeResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeWorktrees),
		Worktrees: []gitquery.Worktree{
			{Path: "/dev/alpha", BranchName: "main", IsMain: true},
			{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd == nil {
		t.Fatal("expected initial inline session fetch")
	}
	m, _ = update(m, cmd())

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd == nil {
		t.Fatal("expected f5 refresh command")
	}
	refreshRequest := m.ListRequest(ui.ModeWorktrees)
	m, followup := update(m, model.WorktreeResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: refreshRequest,
		Worktrees: []gitquery.Worktree{
			{Path: "/dev/alpha", BranchName: "main", IsMain: true},
			{Path: "/dev/alpha-worktrees/inline", BranchName: "feature/inline"},
		},
	})
	if followup == nil {
		t.Fatal("expected refreshed worktree list to fetch inline sessions")
	}
	m, _ = update(m, followup())

	if len(gotFilters) != 2 {
		t.Fatalf("ListSessions called %d times, want initial open plus f5 refresh", len(gotFilters))
	}
	if got := gotFilters[1]; got.RepoPath != "/dev/alpha" || got.WorktreePath != "/dev/alpha-worktrees/inline" {
		t.Fatalf("refreshed SessionFilter = %#v, want repo and inline worktree", got)
	}
	if got := m.WorktreeSessions(); len(got) != 1 || got[0].SessionID != "inline-refresh-2" {
		t.Fatalf("WorktreeSessions() = %#v, want refreshed inline sessions", got)
	}
}

func TestModel_RepoRefreshClampsSelectionAndFetchesNewRepoWhenOldDisappears(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		StartupMode: ui.ModeSessions,
		ScanRepos: func() ([]scanner.Repo, error) {
			return []scanner.Repo{
				{Path: "/dev/bravo", DisplayName: "bravo"},
				{Path: "/dev/charlie", DisplayName: "charlie"},
			}, nil
		},
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: filter.RepoPath, RepoPath: filter.RepoPath}}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, model.SessionResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeSessions),
		Sessions: []sessions.SessionRecord{
			{Provider: sessions.ProviderCodex, SessionID: "old-alpha", RepoPath: "/dev/alpha"},
		},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
	msgs := runBatchCmd(t, cmd)
	preScanFetch := sessionResultFromMessages(t, msgs, "/dev/alpha")
	scanMsg := repoRefreshResultFromBatch(t, msgs)

	m, followup := update(m, scanMsg)
	if got := m.Sessions(); len(got) != 0 {
		t.Fatalf("Sessions() after selected repo changed = %#v, want stale list cleared", got)
	}
	if followup == nil {
		t.Fatal("expected post-scan fetch for new selected repo")
	}
	postScanMsg, ok := followup().(model.SessionResultMsg)
	if !ok {
		t.Fatalf("post-scan fetch = %T, want SessionResultMsg", postScanMsg)
	}
	if postScanMsg.RepoPath != "/dev/bravo" {
		t.Fatalf("post-scan fetch repo = %q, want /dev/bravo", postScanMsg.RepoPath)
	}

	m, _ = update(m, preScanFetch)
	if got := m.Sessions(); len(got) != 0 {
		t.Fatalf("stale pre-scan fetch applied after repo change: %#v", got)
	}
	m, _ = update(m, postScanMsg)
	if got := m.Sessions(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("Sessions() after post-scan fetch = %#v, want bravo session", got)
	}
}

func TestModel_RepoRefreshKeepsFilterAndHandlesZeroVisibleRepos(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) {
			return []scanner.Repo{{Path: "/dev/delta", DisplayName: "delta"}}, nil
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "zzz" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
	scanMsg := repoRefreshResultFromBatch(t, runBatchCmd(t, cmd))
	m, followup := update(m, scanMsg)

	if followup != nil {
		t.Fatal("zero visible repos should not fetch right pane")
	}
	if got := m.RepoSearch(); got != "zzz" {
		t.Fatalf("RepoSearch() = %q, want preserved query zzz", got)
	}
	if got := m.TransientError(); got != "No repositories match filter" {
		t.Fatalf("TransientError() = %q, want zero-match status", got)
	}
}

func TestModel_PlanResultPreservesSelectedPlanWhenResultsReorder(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{
		RepoPath: "/dev/alpha",
		Plans: []planstore.PlanRecord{
			{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "One"},
			{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Two"},
		},
		ListRequest: m.ListRequest(ui.ModePlans),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.Plans()[m.PlanSelected()].PlanID; got != "plan-2" {
		t.Fatalf("selected plan before reorder = %q, want plan-2", got)
	}

	m, _ = update(m, model.PlanResultMsg{
		RepoPath: "/dev/alpha",
		Plans: []planstore.PlanRecord{
			{PlanID: "plan-2", RepoPath: "/dev/alpha", Title: "Two updated"},
			{PlanID: "plan-1", RepoPath: "/dev/alpha", Title: "One"},
		},
		ListRequest: m.ListRequest(ui.ModePlans),
	})

	if got := m.Plans()[m.PlanSelected()].PlanID; got != "plan-2" {
		t.Fatalf("selected plan after reorder = %q, want plan-2", got)
	}
	if got := m.PlanSelected(); got != 0 {
		t.Fatalf("PlanSelected() after reorder = %d, want updated plan index 0", got)
	}
}

func TestModel_FlowResultPreservesSelectedFlowWhenResultsReorder(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	m, _ = update(m, model.FlowResultMsg{
		RepoPath: "/dev/alpha",
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "One"},
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Two"},
		},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.Flows()[m.FlowSelected()].FlowID; got != "flow-2" {
		t.Fatalf("selected flow before reorder = %q, want flow-2", got)
	}

	m, _ = update(m, model.FlowResultMsg{
		RepoPath: "/dev/alpha",
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Two updated"},
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "One"},
		},
		ListRequest: m.ListRequest(ui.ModeFlows),
	})

	if got := m.Flows()[m.FlowSelected()].FlowID; got != "flow-2" {
		t.Fatalf("selected flow after reorder = %q, want flow-2", got)
	}
	if got := m.FlowSelected(); got != 0 {
		t.Fatalf("FlowSelected() after reorder = %d, want updated flow index 0", got)
	}
}

func TestModel_RepoSelectionResetInvalidatesStaleNonCurrentPaneResults(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	stalePlanRequest := m.ListRequest(ui.ModePlans)
	stalePlan := model.PlanResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: stalePlanRequest,
		Plans:       []planstore.PlanRecord{{PlanID: "stale-plan", RepoPath: "/dev/alpha", Title: "Stale"}},
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = update(m, stalePlan)

	if got := m.Plans(); len(got) != 0 {
		t.Fatalf("stale non-current plan result repopulated cleared pane: %#v", got)
	}
}

func TestModel_StaleRepoRefreshResultAndFailureIgnored(t *testing.T) {
	scans := 0
	m := model.NewWithOptions(testRepos(), model.Options{
		StartupMode: ui.ModeSessions,
		ScanRepos: func() ([]scanner.Repo, error) {
			scans++
			if scans == 1 {
				return []scanner.Repo{{Path: "/dev/charlie", DisplayName: "charlie"}}, nil
			}
			return []scanner.Repo{{Path: "/dev/bravo", DisplayName: "bravo"}}, nil
		},
		ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: filter.RepoPath, RepoPath: filter.RepoPath}}, nil
		},
	})

	m, cmdA := update(m, tea.KeyMsg{Type: tea.KeyF5})
	msgA := repoRefreshResultFromBatch(t, runBatchCmd(t, cmdA))
	m, cmdB := update(m, tea.KeyMsg{Type: tea.KeyF5})
	msgB := repoRefreshResultFromBatch(t, runBatchCmd(t, cmdB))

	m, followup := update(m, msgB)
	if followup == nil {
		t.Fatal("fresh refresh should fetch selected repo after clamping")
	}
	freshMsg, ok := followup().(model.SessionResultMsg)
	if !ok {
		t.Fatalf("fresh post-scan fetch = %T, want SessionResultMsg", freshMsg)
	}
	if freshMsg.RepoPath != "/dev/bravo" {
		t.Fatalf("fresh post-scan fetch repo = %q, want /dev/bravo", freshMsg.RepoPath)
	}

	m, _ = update(m, model.RepoRefreshFailedMsg{Request: msgA.Request, Err: "old failure"})
	if got := m.TransientError(); got != "" {
		t.Fatalf("stale refresh failure set status %q", got)
	}
	m, _ = update(m, msgA)
	m, _ = update(m, freshMsg)
	if got := m.Sessions(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("stale refresh result changed selected repo or blocked fresh result: %#v", got)
	}
}

func hasRepoRefreshResult(msgs []tea.Msg) bool {
	for _, msg := range msgs {
		if _, ok := msg.(model.RepoRefreshResultMsg); ok {
			return true
		}
	}
	return false
}

func repoRefreshResultFromBatch(t *testing.T, msgs []tea.Msg) model.RepoRefreshResultMsg {
	t.Helper()
	for _, msg := range msgs {
		if got, ok := msg.(model.RepoRefreshResultMsg); ok {
			return got
		}
	}
	t.Fatalf("messages = %#v, want RepoRefreshResultMsg", msgs)
	return model.RepoRefreshResultMsg{}
}

func sessionResultFromMessages(t *testing.T, msgs []tea.Msg, repoPath string) model.SessionResultMsg {
	t.Helper()
	for _, msg := range msgs {
		if got, ok := msg.(model.SessionResultMsg); ok && got.RepoPath == repoPath {
			return got
		}
	}
	t.Fatalf("messages = %#v, want SessionResultMsg for %s", msgs, repoPath)
	return model.SessionResultMsg{}
}

func hasListFetchForMode(msgs []tea.Msg, mode ui.Mode, request uint64) bool {
	for _, msg := range msgs {
		switch msg := msg.(type) {
		case model.WorktreeResultMsg:
			return mode == ui.ModeWorktrees && msg.ListRequest == request
		case model.BranchResultMsg:
			return mode == ui.ModeBranches && msg.ListRequest == request
		case model.StashResultMsg:
			return mode == ui.ModeStashes && msg.ListRequest == request
		case model.CommitResultMsg:
			return mode == ui.ModeHistory && msg.ListRequest == request
		case model.ReflogResultMsg:
			return mode == ui.ModeReflog && msg.ListRequest == request
		case model.SessionResultMsg:
			return mode == ui.ModeSessions && msg.ListRequest == request
		case model.PlanResultMsg:
			return mode == ui.ModePlans && msg.ListRequest == request
		case model.FlowResultMsg:
			return mode == ui.ModeFlows && msg.ListRequest == request
		case model.ActiveFlowResultMsg:
			return mode == ui.ModeActiveFlows && msg.ListRequest == request
		case model.FetchErrorMsg:
			return msg.Kind == model.FetchList && msg.Mode == mode && msg.ListRequest == request
		}
	}
	return false
}
