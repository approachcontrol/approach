package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

type listFetchDescriptor struct {
	mode        ui.Mode
	pane        string
	errorPrefix string
	load        func(Model, string, uint64) (tea.Msg, error)
	beforeStart func(Model) Model
}

func listFetchDescriptorForMode(mode ui.Mode) (listFetchDescriptor, bool) {
	switch mode {
	case ui.ModeWorktrees:
		return listFetchDescriptor{
			mode:        ui.ModeWorktrees,
			pane:        "worktrees",
			errorPrefix: "failed to load worktrees",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				worktrees, err := gitquery.ListWorktrees(repoPath)
				if err != nil {
					return nil, err
				}
				return WorktreeResultMsg{RepoPath: repoPath, Worktrees: worktrees, ListRequest: request}, nil
			},
		}, true
	case ui.ModeBranches:
		return listFetchDescriptor{
			mode:        ui.ModeBranches,
			pane:        "branches",
			errorPrefix: "failed to load branches",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				branches, err := gitquery.ListBranches(repoPath)
				if err != nil {
					return nil, err
				}
				return BranchResultMsg{RepoPath: repoPath, Branches: branches, ListRequest: request}, nil
			},
		}, true
	case ui.ModeStashes:
		return listFetchDescriptor{
			mode:        ui.ModeStashes,
			pane:        "stashes",
			errorPrefix: "failed to load stashes",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				stashes, err := gitquery.ListStashes(repoPath)
				if err != nil {
					return nil, err
				}
				return StashResultMsg{RepoPath: repoPath, Stashes: stashes, ListRequest: request}, nil
			},
		}, true
	case ui.ModeHistory:
		return listFetchDescriptor{
			mode:        ui.ModeHistory,
			pane:        "history",
			errorPrefix: "failed to load commits",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				commits, err := gitquery.ListCommits(repoPath)
				if err != nil {
					return nil, err
				}
				return CommitResultMsg{RepoPath: repoPath, Commits: commits, ListRequest: request}, nil
			},
		}, true
	case ui.ModeReflog:
		return listFetchDescriptor{
			mode:        ui.ModeReflog,
			pane:        "reflog",
			errorPrefix: "failed to load reflog",
			load: func(_ Model, repoPath string, request uint64) (tea.Msg, error) {
				reflogs, err := gitquery.ListReflog(repoPath)
				if err != nil {
					return nil, err
				}
				return ReflogResultMsg{RepoPath: repoPath, Reflogs: reflogs, ListRequest: request}, nil
			},
		}, true
	case ui.ModeSessions:
		return listFetchDescriptor{
			mode:        ui.ModeSessions,
			pane:        "sessions",
			errorPrefix: "failed to load sessions",
			load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
				records, err := m.listSessions(sessions.SessionFilter{RepoPath: repoPath})
				if err != nil {
					return nil, err
				}
				return SessionResultMsg{RepoPath: repoPath, Sessions: records, ListRequest: request}, nil
			},
		}, true
	case ui.ModePlans:
		return listFetchDescriptor{
			mode:        ui.ModePlans,
			pane:        "plans",
			errorPrefix: "failed to load plans",
			beforeStart: func(m Model) Model {
				return m.setExpandedPlanID("")
			},
			load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
				records, err := m.listPlans(planstore.PlanFilter{RepoPath: repoPath})
				if err != nil {
					return nil, err
				}
				return PlanResultMsg{RepoPath: repoPath, Plans: records, ListRequest: request}, nil
			},
		}, true
	case ui.ModeFlows:
		return listFetchDescriptor{
			mode:        ui.ModeFlows,
			pane:        "flows",
			errorPrefix: "failed to load flows",
			load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
				records, err := m.listFlows(flowstore.FlowFilter{RepoPath: repoPath})
				if err != nil {
					return nil, err
				}
				return FlowResultMsg{RepoPath: repoPath, Flows: records, ListRequest: request}, nil
			},
		}, true
	default:
		return listFetchDescriptor{}, false
	}
}

func (m Model) startFetchMode(mode ui.Mode) (Model, tea.Cmd) {
	desc, ok := listFetchDescriptorForMode(mode)
	if !ok {
		return m, nil
	}
	m, request := m.nextListFetchRequest(desc.mode)
	if desc.beforeStart != nil {
		m = desc.beforeStart(m)
	}
	cmd := m.fetchList(desc, request)
	if desc.mode == ui.ModeFlows && m.mode == ui.ModeFlows {
		m.flowRefreshTickGen++
		m.flowRefreshInFlight = 0
		if cmd != nil {
			m.flowRefreshInFlight = request
		}
	}
	return m, cmd
}

func (m Model) fetchMode(mode ui.Mode, request uint64) tea.Cmd {
	desc, ok := listFetchDescriptorForMode(mode)
	if !ok {
		return nil
	}
	return m.fetchList(desc, request)
}

func (m Model) fetchList(desc listFetchDescriptor, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		msg, err := desc.load(m, repoPath, request)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        desc.pane,
				Err:         fmt.Sprintf("%s: %v", desc.errorPrefix, err),
				Kind:        FetchList,
				Mode:        desc.mode,
				ListRequest: request,
			}
		}
		return msg
	}
}
