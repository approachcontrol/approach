package model

import (
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
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
				if partial, ok := flowstore.AsPartialList(err); ok {
					return FlowResultMsg{RepoPath: repoPath, Flows: records, Degradation: partial, ListRequest: request}, nil
				}
				if err != nil {
					return nil, err
				}
				return FlowResultMsg{RepoPath: repoPath, Flows: records, ListRequest: request}, nil
			},
		}, true
	case ui.ModeBeadsReady:
		return beadsListFetchDescriptor(ui.ModeBeadsReady, "beads ready"), true
	case ui.ModeBeadsBlocked:
		return beadsListFetchDescriptor(ui.ModeBeadsBlocked, "beads blocked"), true
	case ui.ModeBeadsOpen:
		return beadsListFetchDescriptor(ui.ModeBeadsOpen, "beads open"), true
	case ui.ModeBeadsInProgress:
		return beadsListFetchDescriptor(ui.ModeBeadsInProgress, "beads in-progress"), true
	case ui.ModeBeadsClosed:
		return beadsClosedFetchDescriptor(), true
	default:
		return listFetchDescriptor{}, false
	}
}

func beadsClosedFetchDescriptor() listFetchDescriptor {
	desc := beadsListFetchDescriptor(ui.ModeBeadsClosed, "beads closed")
	desc.load = func(m Model, repoPath string, request uint64) (tea.Msg, error) {
		index, _ := beadSubviewIndex(ui.ModeBeadsClosed)
		beads, err := m.listBeads[index](repoPath)
		if err != nil {
			return beadsResultMessage(ui.ModeBeadsClosed, repoPath, request, nil, err), nil
		}
		total, err := m.countClosedBeads(repoPath)
		if err != nil {
			return beadsResultMessage(ui.ModeBeadsClosed, repoPath, request, nil, err), nil
		}
		return BeadsClosedResultMsg{
			RepoPath: repoPath, Beads: beads, Total: total,
			ListRequest: request, Available: true,
		}, nil
	}
	return desc
}

func beadsListFetchDescriptor(mode ui.Mode, paneName string) listFetchDescriptor {
	return listFetchDescriptor{
		mode: mode,
		pane: paneName,
		beforeStart: func(m Model) Model {
			m = m.clearBeadExpansion()
			if m.activeViewKind == FetchBeadDetail {
				m = m.invalidateViewRequest()
			}
			index, _ := beadSubviewIndex(mode)
			// Rows and cursor are retained across a same-repo refetch so the
			// accepted replacement can refilter and clamp them. Rows belonging
			// to another repository are dropped instead; the query is view
			// state and survives either way.
			if repoPath, _ := m.currentRepoPath(); repoPath != m.beads[index].repoPath {
				m.beads[index].pane = m.beads[index].pane.SetItems(nil).ResetSelection()
				m.beads[index].repoPath = ""
			}
			m.beads[index].available = false
			m.beads[index].pending = true
			m.beads[index].error = ""
			m.beads[index].total = 0
			return m.reflowBeads(mode)
		},
		load: func(m Model, repoPath string, request uint64) (tea.Msg, error) {
			index, _ := beadSubviewIndex(mode)
			beads, err := m.listBeads[index](repoPath)
			return beadsResultMessage(mode, repoPath, request, beads, err), nil
		},
	}
}

func beadsResultMessage(mode ui.Mode, repoPath string, request uint64, beads []beadsquery.Bead, err error) tea.Msg {
	available := err == nil
	errorDetail := ""
	if err != nil && !errors.Is(err, beadsquery.ErrNotConfigured) {
		errorDetail = err.Error()
	}
	if !available {
		beads = nil
	}
	switch mode {
	case ui.ModeBeadsReady:
		return BeadsReadyResultMsg{RepoPath: repoPath, Beads: beads, ListRequest: request, Available: available, Error: errorDetail}
	case ui.ModeBeadsBlocked:
		return BeadsBlockedResultMsg{RepoPath: repoPath, Beads: beads, ListRequest: request, Available: available, Error: errorDetail}
	case ui.ModeBeadsOpen:
		return BeadsOpenResultMsg{RepoPath: repoPath, Beads: beads, ListRequest: request, Available: available, Error: errorDetail}
	case ui.ModeBeadsInProgress:
		return BeadsInProgressResultMsg{RepoPath: repoPath, Beads: beads, ListRequest: request, Available: available, Error: errorDetail}
	case ui.ModeBeadsClosed:
		return BeadsClosedResultMsg{RepoPath: repoPath, Beads: beads, ListRequest: request, Available: available, Error: errorDetail}
	default:
		return nil
	}
}

func (m Model) startFetchMode(mode ui.Mode) (Model, tea.Cmd) {
	if mode == ui.ModeActiveFlows {
		return m.startFetchActiveFlows()
	}
	desc, ok := listFetchDescriptorForMode(mode)
	if !ok {
		return m, nil
	}
	m, request := m.nextListFetchRequest(desc.mode)
	if desc.beforeStart != nil {
		m = desc.beforeStart(m)
	}
	cmd := m.fetchList(desc, request)
	if desc.mode == ui.ModeFlows && !m.activeFlowSurfaceVisible() && m.modeStored(ui.ModeFlows) {
		m.flowRefreshTickGen++
		m.flowRefreshInFlight = 0
		m.flowRefreshInFlightMode = 0
		if cmd != nil {
			m.flowRefreshInFlight = request
			m.flowRefreshInFlightMode = desc.mode
		}
	}
	return m, cmd
}

func (m Model) startFetchActiveFlows() (Model, tea.Cmd) {
	m, request := m.nextListFetchRequest(ui.ModeActiveFlows)
	cmd := m.fetchActiveFlows(request)
	if m.activeFlowSurfaceVisible() {
		m.flowRefreshTickGen++
		m.flowRefreshInFlight = 0
		m.flowRefreshInFlightMode = 0
		if cmd != nil {
			m.flowRefreshInFlight = request
			m.flowRefreshInFlightMode = ui.ModeActiveFlows
		}
	}
	return m, cmd
}

func (m Model) fetchMode(mode ui.Mode, request uint64) tea.Cmd {
	if mode == ui.ModeActiveFlows {
		return m.fetchActiveFlows(request)
	}
	desc, ok := listFetchDescriptorForMode(mode)
	if !ok {
		return nil
	}
	return m.fetchList(desc, request)
}

func (m Model) fetchActiveFlows(request uint64) tea.Cmd {
	return func() tea.Msg {
		records, err := m.listFlows(flowstore.FlowFilter{})
		if partial, ok := flowstore.AsPartialList(err); ok {
			return ActiveFlowResultMsg{Flows: records, Degradation: partial, ListRequest: request}
		}
		if err != nil {
			return FetchErrorMsg{
				Pane:        "active-flows",
				Err:         fmt.Sprintf("failed to load active flows: %v", err),
				Kind:        FetchList,
				Mode:        ui.ModeActiveFlows,
				ListRequest: request,
			}
		}
		return ActiveFlowResultMsg{Flows: records, ListRequest: request}
	}
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
