package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func TestModel_ListFetchModesShareRequestAndStaleResultBehavior(t *testing.T) {
	const selectedRepo = "/dev/bravo"

	cases := []struct {
		name          string
		mode          ui.Mode
		pane          string
		errorPrefix   string
		result        func(string, uint64) tea.Msg
		assertApplied func(t *testing.T, m model.Model)
	}{
		{
			name:        "worktrees",
			mode:        ui.ModeWorktrees,
			pane:        "worktrees",
			errorPrefix: "failed to load worktrees",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.WorktreeResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Worktrees:   []gitquery.Worktree{{Path: repoPath, BranchName: "main"}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Worktrees(); len(got) != 1 || got[0].Path != selectedRepo {
					t.Fatalf("Worktrees() = %#v, want selected repo worktree", got)
				}
			},
		},
		{
			name:        "branches",
			mode:        ui.ModeBranches,
			pane:        "branches",
			errorPrefix: "failed to load branches",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.BranchResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Branches:    []gitquery.Branch{{Name: "feature/list-fetch", IsWorktree: true, WorktreePaths: []string{repoPath}}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Rows(); len(got) != 1 || got[0].Branch.Name != "feature/list-fetch" {
					t.Fatalf("Rows() = %#v, want fetched branch row", got)
				}
			},
		},
		{
			name:        "stashes",
			mode:        ui.ModeStashes,
			pane:        "stashes",
			errorPrefix: "failed to load stashes",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.StashResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Stashes:     []gitquery.Stash{{Index: 0, Date: "2026-06-08", Message: "WIP list fetch"}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Stashes(); len(got) != 1 || got[0].Message != "WIP list fetch" {
					t.Fatalf("Stashes() = %#v, want fetched stash", got)
				}
			},
		},
		{
			name:        "history",
			mode:        ui.ModeHistory,
			pane:        "history",
			errorPrefix: "failed to load commits",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.CommitResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Commits:     []gitquery.Commit{{Hash: "abc1234", Subject: "deepen list fetching"}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Commits(); len(got) != 1 || got[0].Hash != "abc1234" {
					t.Fatalf("Commits() = %#v, want fetched commit", got)
				}
			},
		},
		{
			name:        "reflog",
			mode:        ui.ModeReflog,
			pane:        "reflog",
			errorPrefix: "failed to load reflog",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.ReflogResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Reflogs:     []gitquery.ReflogEntry{{Hash: "def5678", Selector: "HEAD@{0}", Subject: "checkout"}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Reflogs(); len(got) != 1 || got[0].Hash != "def5678" {
					t.Fatalf("Reflogs() = %#v, want fetched reflog", got)
				}
			},
		},
		{
			name:        "sessions",
			mode:        ui.ModeSessions,
			pane:        "sessions",
			errorPrefix: "failed to load sessions",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.SessionResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Sessions:    []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: "session-1", RepoPath: repoPath}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Sessions(); len(got) != 1 || got[0].SessionID != "session-1" {
					t.Fatalf("Sessions() = %#v, want fetched session", got)
				}
			},
		},
		{
			name:        "plans",
			mode:        ui.ModePlans,
			pane:        "plans",
			errorPrefix: "failed to load plans",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.PlanResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Plans:       []planstore.PlanRecord{{PlanID: "plan-1", RepoPath: repoPath, Title: "Deepen list fetching"}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Plans(); len(got) != 1 || got[0].PlanID != "plan-1" {
					t.Fatalf("Plans() = %#v, want fetched plan", got)
				}
			},
		},
		{
			name:        "flows",
			mode:        ui.ModeFlows,
			pane:        "flows",
			errorPrefix: "failed to load flows",
			result: func(repoPath string, request uint64) tea.Msg {
				return model.FlowResultMsg{
					RepoPath:    repoPath,
					ListRequest: request,
					Flows:       []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: repoPath, Title: "Deepen list fetching"}},
				}
			},
			assertApplied: func(t *testing.T, m model.Model) {
				t.Helper()
				if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
					t.Fatalf("Flows() = %#v, want fetched flow", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loadCount := 0
			m := model.NewWithOptions(testRepos(), model.Options{
				StartupMode: tc.mode,
				ListSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
					loadCount++
					if filter.RepoPath != selectedRepo {
						t.Fatalf("SessionFilter.RepoPath = %q, want %q", filter.RepoPath, selectedRepo)
					}
					return nil, errors.New("session store unavailable")
				},
				ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
					loadCount++
					if filter.RepoPath != selectedRepo {
						t.Fatalf("PlanFilter.RepoPath = %q, want %q", filter.RepoPath, selectedRepo)
					}
					return nil, errors.New("plan store unavailable")
				},
				ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					loadCount++
					if filter.RepoPath != selectedRepo {
						t.Fatalf("FlowFilter.RepoPath = %q, want %q", filter.RepoPath, selectedRepo)
					}
					return nil, errors.New("flow store unavailable")
				},
			})
			beforeRequest := m.ListRequest(tc.mode)

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyDown})
			started := m
			request := m.ListRequest(tc.mode)
			if request == 0 || request == beforeRequest {
				t.Fatalf("ListRequest(%v) = %d, want new nonzero request after %d", tc.mode, request, beforeRequest)
			}
			if cmd == nil {
				t.Fatal("expected fetch command")
			}
			if loadCount != 0 {
				t.Fatalf("list adapter ran before command execution: %d call(s)", loadCount)
			}

			switched, _ := update(m, tea.KeyMsg{Type: tea.KeyDown})
			msg := cmd()
			assertListFetchCommandError(t, msg, tc.mode, request, selectedRepo, tc.pane, tc.errorPrefix)
			switched, _ = update(switched, msg)
			if got := switched.TransientError(); got != "" {
				t.Fatalf("captured command for old repo changed current repo status: TransientError() = %q", got)
			}

			m = started
			m, _ = update(m, model.FetchErrorMsg{
				RepoPath:    selectedRepo,
				Pane:        tc.name,
				Err:         "matching list fetch failed",
				Kind:        model.FetchList,
				Mode:        tc.mode,
				ListRequest: request,
			})
			if got := m.TransientError(); got != "matching list fetch failed" {
				t.Fatalf("TransientError() after fetch error = %q, want matching list fetch failed", got)
			}

			m, _ = update(m, tc.result("/dev/charlie", request))
			if got := m.TransientError(); got != "matching list fetch failed" {
				t.Fatalf("wrong-repo result cleared status: TransientError() = %q", got)
			}

			m, _ = update(m, tc.result(selectedRepo, request-1))
			if got := m.TransientError(); got != "matching list fetch failed" {
				t.Fatalf("stale result cleared status: TransientError() = %q", got)
			}

			m, _ = update(m, tc.result(selectedRepo, request))
			if got := m.TransientError(); got != "" {
				t.Fatalf("matching result did not clear status: TransientError() = %q", got)
			}
			tc.assertApplied(t, m)
		})
	}
}

func assertListFetchCommandError(t *testing.T, msg tea.Msg, mode ui.Mode, request uint64, repoPath, pane, errorPrefix string) {
	t.Helper()

	got, ok := msg.(model.FetchErrorMsg)
	if !ok {
		t.Fatalf("expected FetchErrorMsg, got %T", msg)
	}
	if got.Pane != pane ||
		got.Kind != model.FetchList ||
		got.Mode != mode ||
		got.ListRequest != request ||
		got.RepoPath != repoPath ||
		!strings.HasPrefix(got.Err, errorPrefix+": ") {
		t.Fatalf("FetchErrorMsg = %#v, want pane=%q kind=FetchList mode=%v request=%d repo=%q err prefix %q", got, pane, mode, request, repoPath, errorPrefix+": ")
	}
}
