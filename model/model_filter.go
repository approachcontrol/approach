package model

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model/pane"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func fixedHeight[T any](T, int) int {
	return 1
}

func newRepoPane() pane.Pane[scanner.Repo] {
	return pane.New(func(repo scanner.Repo) string {
		return repo.DisplayName + " " + repo.Path
	}, fixedHeight[scanner.Repo])
}

func newWorktreePane() pane.Pane[gitquery.Worktree] {
	return pane.New(func(wt gitquery.Worktree) string {
		return strings.Join([]string{wt.BranchName, wt.Path, wt.LockReason}, " ")
	}, fixedHeight[gitquery.Worktree])
}

func newBranchPane() pane.Pane[gitquery.BranchRow] {
	return pane.New(branchSearchText, func(row gitquery.BranchRow, _ int) int {
		if row.IsExpansion {
			return 1
		}
		n := len(row.Branch.Unpushed)
		if n > 5 {
			n = 6
		}
		return 1 + n
	})
}

func newStashPane() pane.Pane[gitquery.Stash] {
	return pane.New(func(stash gitquery.Stash) string {
		return fmt.Sprintf("stash@{%d} %s %s", stash.Index, stash.Date, stash.Message)
	}, func(stash gitquery.Stash, width int) int {
		return ui.StashLineCount(stash.Message, width)
	})
}

func newCommitPane() pane.Pane[gitquery.Commit] {
	return pane.New(func(commit gitquery.Commit) string {
		return strings.Join([]string{commit.Hash, commit.Author, commit.Date, commit.Subject}, " ")
	}, fixedHeight[gitquery.Commit])
}

func newReflogPane() pane.Pane[gitquery.ReflogEntry] {
	return pane.New(func(entry gitquery.ReflogEntry) string {
		return strings.Join([]string{entry.Hash, entry.Selector, entry.Date, entry.Subject}, " ")
	}, fixedHeight[gitquery.ReflogEntry])
}

func newSessionPane() pane.Pane[sessions.SessionRecord] {
	return pane.New(sessionSearchText, fixedHeight[sessions.SessionRecord])
}

func newPlanPane() pane.Pane[planstore.PlanRecord] {
	return pane.New(planSearchText, planItemHeight(""))
}

func newFlowPane() pane.Pane[flowstore.FlowRecord] {
	return pane.New(flowSearchText, fixedHeight[flowstore.FlowRecord])
}

func planItemHeight(expandedPlanID string) pane.ItemHeight[planstore.PlanRecord] {
	return func(record planstore.PlanRecord, _ int) int {
		return planVisualHeight(record, expandedPlanID)
	}
}

func planVisualHeight(record planstore.PlanRecord, expandedPlanID string) int {
	if expandedPlanID == "" || record.PlanID != expandedPlanID {
		return 1
	}
	if len(record.Phases) == 0 {
		return 2
	}
	return 1 + len(record.Phases)
}

func (m Model) activeSearchQuery() string {
	if m.activePane == 0 {
		return m.repos.Query()
	}
	return m.activeItemPaneQuery()
}

func (m Model) activeItemPaneQuery() string {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.worktrees.Query()
	case ui.ModeBranches:
		return m.rows.Query()
	case ui.ModeStashes:
		return m.stashes.Query()
	case ui.ModeHistory:
		return m.commits.Query()
	case ui.ModeReflog:
		return m.reflogs.Query()
	case ui.ModeSessions:
		return m.sessions.Query()
	case ui.ModePlans:
		return m.plans.Query()
	case ui.ModeFlows:
		return m.flows.Query()
	default:
		return ""
	}
}

func (m Model) setActiveSearchQuery(query string) Model {
	if m.activePane == 0 {
		m.repos = m.repos.SetQuery(query)
		return m.reflowRepos()
	}

	m.worktrees = m.worktrees.SetQueryPreserveIndex(query)
	m.rows = m.rows.SetQueryPreserveIndex(query)
	m.stashes = m.stashes.SetQueryPreserveIndex(query)
	m.commits = m.commits.SetQueryPreserveIndex(query)
	m.reflogs = m.reflogs.SetQueryPreserveIndex(query)
	m.sessions = m.sessions.SetQueryPreserveIndex(query)
	m.plans = m.plans.SetQueryPreserveIndex(query)
	m.flows = m.flows.SetQueryPreserveIndex(query)

	switch m.mode {
	case ui.ModeWorktrees:
		m.worktrees = m.worktrees.SetQuery(query)
		m = m.reflowWorktrees()
	case ui.ModeBranches:
		m.rows = m.rows.SetQuery(query)
		m = m.reflowBranches()
	case ui.ModeStashes:
		m.stashes = m.stashes.SetQuery(query)
		m = m.reflowStashes()
	case ui.ModeHistory:
		m.commits = m.commits.SetQuery(query)
		m = m.reflowCommits()
	case ui.ModeReflog:
		m.reflogs = m.reflogs.SetQuery(query)
		m = m.reflowReflogs()
	case ui.ModeSessions:
		m.sessions = m.sessions.SetQuery(query)
		m = m.reflowSessions()
	case ui.ModePlans:
		m.plans = m.plans.SetQuery(query)
		m = m.setExpandedPlanID("")
	case ui.ModeFlows:
		m.flows = m.flows.SetQuery(query)
		m = m.reflowFlows()
	}
	return m
}

func (m Model) clampSelectionsAfterFilter() Model {
	m = m.reflowRepos()
	m = m.reflowWorktrees()
	m = m.reflowBranches()
	m = m.reflowStashes()
	m = m.reflowCommits()
	m = m.reflowReflogs()
	m = m.reflowSessions()
	m = m.reflowPlans()
	m = m.reflowFlows()
	return m
}

func (m Model) selectFilteredRepo(repoPath string) Model {
	m.repos = m.repos.SelectFunc(func(repo scanner.Repo) bool {
		return repo.Path == repoPath
	})
	return m.reflowRepos()
}

func (m Model) filteredRepos() []scanner.Repo {
	repos, _, _ := m.repos.View()
	return repos
}

func (m Model) filteredStashes() []gitquery.Stash {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	stashes, _, _ := m.stashes.View()
	return stashes
}

func (m Model) filteredWorktrees() []gitquery.Worktree {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	worktrees, _, _ := m.worktrees.View()
	return worktrees
}

func (m Model) filteredCommits() []gitquery.Commit {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	commits, _, _ := m.commits.View()
	return commits
}

func (m Model) filteredReflogs() []gitquery.ReflogEntry {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	reflogs, _, _ := m.reflogs.View()
	return reflogs
}

func (m Model) filteredSessions() []sessions.SessionRecord {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	sessions, _, _ := m.sessions.View()
	return sessions
}

func (m Model) filteredPlans() []planstore.PlanRecord {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	plans, _, _ := m.plans.View()
	return plans
}

func (m Model) filteredFlows() []flowstore.FlowRecord {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	flows, _, _ := m.flows.View()
	return flows
}

func planSearchText(record planstore.PlanRecord) string {
	parts := []string{
		record.Title,
		record.Summary,
		record.Status,
		record.Branch,
		record.WorktreePath,
		filepath.Base(record.WorktreePath),
		record.Provider,
		record.SessionID,
		record.LaunchID,
	}
	for _, phase := range record.Phases {
		parts = append(parts, phase.Title, phase.Status)
	}
	return strings.Join(parts, " ")
}

func flowSearchText(record flowstore.FlowRecord) string {
	parts := []string{
		record.Title,
		record.Instructions,
		record.Status,
		record.Branch,
		record.WorktreePath,
		filepath.Base(record.WorktreePath),
		record.PlanID,
		record.PlanPath,
		record.PR.URL,
		record.PR.HeadBranch,
		record.PR.BaseBranch,
	}
	if record.PR.Number > 0 {
		parts = append(parts, fmt.Sprintf("#%d", record.PR.Number))
	}
	for _, phase := range record.Phases {
		parts = append(parts, phase.Title, phase.Status, phase.Summary)
		for _, session := range phase.Sessions {
			parts = append(parts, session.Provider, session.SessionID, session.Status)
		}
	}
	return strings.Join(parts, " ")
}

func branchSearchText(row gitquery.BranchRow) string {
	parts := []string{row.Branch.Name, row.WorktreePath}
	parts = append(parts, row.Branch.Unpushed...)
	return strings.Join(parts, " ")
}

func sessionSearchText(record sessions.SessionRecord) string {
	return strings.Join([]string{
		string(record.Provider),
		record.SessionID,
		record.LaunchID,
		record.Branch,
		record.WorktreePath,
		filepath.Base(record.WorktreePath),
		record.PlanID,
		record.PlanPath,
		record.Model,
		record.Status,
		record.Summary,
	}, " ")
}
