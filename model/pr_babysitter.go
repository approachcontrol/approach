package model

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

const (
	prBabysitterPollInterval = 30 * time.Second
	prBabysitterQueryTimeout = 10 * time.Second
	prBabysitterWorkers      = 4
)

type prBabysitterPollMsg struct {
	Generation uint64
}

type PRBabysitterResultMsg struct {
	Flows       []flowstore.FlowRecord
	Statuses    map[string]actions.PullRequestStatus
	Degradation *flowstore.PartialListError
	ListRequest uint64
	Error       string
}

func unknownPullRequestStatus() actions.PullRequestStatus {
	return actions.PullRequestStatus{Mergeability: actions.PRStatusUnknown, Checks: actions.PRStatusUnknown}
}

func (m Model) prBabysitterRows(records []flowstore.FlowRecord) []ui.PRBabysitterRow {
	names := make(map[string]string)
	for _, repo := range m.repos.Items() {
		if path := strings.TrimSpace(repo.Path); path != "" {
			names[filepath.Clean(path)] = strings.TrimSpace(repo.DisplayName)
		}
	}
	rows := make([]ui.PRBabysitterRow, 0, len(records))
	for _, record := range records {
		repoPath := strings.TrimSpace(record.RepoPath)
		repo := names[filepath.Clean(repoPath)]
		if repo == "" && repoPath != "" {
			repo = filepath.Base(filepath.Clean(repoPath))
		}
		status, ok := m.prBabysitterStatuses[record.FlowID]
		if !ok {
			status = unknownPullRequestStatus()
		}
		rows = append(rows, ui.PRBabysitterRow{
			Flow:         record,
			Repo:         repo,
			Title:        record.Title,
			BeadID:       record.Bead.ID,
			Mergeability: status.Mergeability,
			Checks:       status.Checks,
		})
	}
	return rows
}

func (m Model) startPRBabysitterRefresh() (Model, tea.Cmd) {
	m = m.cancelPRBabysitterRefresh()
	m, request := m.nextListFetchRequest(ui.ModePRBabysitter)
	ctx, cancel := context.WithCancel(context.Background())
	m.prBabysitterCancel = cancel
	listFlows := m.listFlows
	lookup := m.lookupPRStatus
	return m, func() tea.Msg {
		records, err := listFlows(flowstore.FlowFilter{})
		var degradation *flowstore.PartialListError
		if partial, ok := flowstore.AsPartialList(err); ok {
			degradation = partial
		} else if err != nil {
			return PRBabysitterResultMsg{ListRequest: request, Error: fmt.Sprintf("failed to load PR babysitter flows: %v", err)}
		}
		eligible := make([]flowstore.FlowRecord, 0, len(records))
		for _, record := range records {
			if prBabysitterEligible(record) {
				eligible = append(eligible, record)
			}
		}
		statuses := lookupPRStatuses(ctx, eligible, lookup)
		return PRBabysitterResultMsg{
			Flows:       eligible,
			Statuses:    statuses,
			Degradation: degradation,
			ListRequest: request,
		}
	}
}

func lookupPRStatuses(
	ctx context.Context,
	records []flowstore.FlowRecord,
	lookup func(context.Context, int, string) (actions.PullRequestStatus, error),
) map[string]actions.PullRequestStatus {
	statuses := make([]actions.PullRequestStatus, len(records))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workerCount := min(prBabysitterWorkers, len(records))
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				queryCtx, cancel := context.WithTimeout(ctx, prBabysitterQueryTimeout)
				status, err := lookup(queryCtx, records[index].PR.Number, records[index].PR.URL)
				cancel()
				if err != nil {
					status = unknownPullRequestStatus()
				}
				statuses[index] = status
			}
		}()
	}
	for index := range records {
		select {
		case <-ctx.Done():
			break
		case jobs <- index:
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	result := make(map[string]actions.PullRequestStatus, len(records))
	for index, record := range records {
		status := statuses[index]
		if status.Mergeability == "" || status.Checks == "" {
			status = unknownPullRequestStatus()
		}
		result[record.FlowID] = status
	}
	return result
}

func (m Model) cancelPRBabysitterRefresh() Model {
	if m.prBabysitterCancel != nil {
		m.prBabysitterCancel()
		m.prBabysitterCancel = nil
	}
	if m.currentListRequest(ui.ModePRBabysitter) != 0 {
		m, _ = m.nextListFetchRequest(ui.ModePRBabysitter)
	}
	return m
}

func (m Model) prBabysitterPollCmd(generation uint64) tea.Cmd {
	if generation == 0 {
		return nil
	}
	return tea.Tick(prBabysitterPollInterval, func(time.Time) tea.Msg {
		return prBabysitterPollMsg{Generation: generation}
	})
}

func (m Model) acceptPRBabysitterResult(request uint64) bool {
	return m.prBabysitterSurfaceVisible() && m.isCurrentListRequest(ui.ModePRBabysitter, request)
}

func (m Model) handlePRBabysitterResult(msg PRBabysitterResultMsg) (Model, tea.Cmd) {
	if !m.acceptPRBabysitterResult(msg.ListRequest) {
		return m, nil
	}
	m.prBabysitterCancel = nil
	if msg.Error != "" {
		m = m.setCurrentListError(ui.ModePRBabysitter, msg.Error)
		m = m.setFetchStatus(FetchErrorMsg{
			Pane: "PR babysitter", Err: msg.Error, Kind: FetchList,
			Mode: ui.ModePRBabysitter, ListRequest: msg.ListRequest,
		})
		return m, m.prBabysitterPollCmd(msg.ListRequest)
	}
	m = m.setCurrentListError(ui.ModePRBabysitter, "")
	m = m.clearFetchListStatus(ui.ModePRBabysitter)
	m = m.setFlowDegradation(ui.ModePRBabysitter, "", msg.Degradation)
	records := preferNewerCachedFlowRecords(msg.Flows, msg.ListRequest, m.latestFlowMutations)
	accepted := make([]flowstore.FlowRecord, 0, len(records))
	statuses := make(map[string]actions.PullRequestStatus, len(records))
	for _, record := range records {
		if !prBabysitterEligible(record) {
			continue
		}
		accepted = append(accepted, record)
		status, ok := msg.Statuses[record.FlowID]
		if !ok {
			status = unknownPullRequestStatus()
		}
		statuses[record.FlowID] = status
	}
	m.prBabysitterRecords = accepted
	m.prBabysitterStatuses = statuses
	m = m.syncPRBabysitterFromCache()
	m = m.clampSelectionsAfterFilter()
	if m.terminalFocus != terminalFocusTerminal {
		m = m.syncActiveFlowTerminalToSelectedFlow()
	}
	return m, m.prBabysitterPollCmd(msg.ListRequest)
}

func (m Model) visiblePRBabysitterRecords() []flowstore.FlowRecord {
	if m.prBabysitterSurfaceVisible() && m.activePane == ui.PaneRepos {
		repoPath, ok := m.currentRepoPath()
		if !ok {
			return nil
		}
		filtered := make([]flowstore.FlowRecord, 0, len(m.prBabysitterRecords))
		for _, record := range m.prBabysitterRecords {
			if sameRepoPath(record.RepoPath, repoPath) {
				filtered = append(filtered, record)
			}
		}
		return filtered
	}
	return m.prBabysitterRecords
}

func (m Model) syncPRBabysitterFromCache() Model {
	selectedFlowID := ""
	if record, ok := m.prBabysitterFlows.Selected(); ok {
		selectedFlowID = record.FlowID
	}
	expandedFlowID := m.expandedPRBabysitterFlowID
	selectedPhaseID := m.selectedPRBabysitterPhaseID
	m.prBabysitterFlows = m.prBabysitterFlows.SetItems(m.visiblePRBabysitterRecords())
	if selectedFlowID != "" {
		m.prBabysitterFlows = m.prBabysitterFlows.SelectFunc(func(record flowstore.FlowRecord) bool {
			return record.FlowID == selectedFlowID
		})
	}
	return m.restorePRBabysitterExpandedSelection(expandedFlowID, selectedPhaseID).reflowPRBabysitter()
}

func (m Model) replacePRBabysitterRepoCache(repoPath string, records []flowstore.FlowRecord) Model {
	retained := make([]flowstore.FlowRecord, 0, len(m.prBabysitterRecords)+len(records))
	statuses := make(map[string]actions.PullRequestStatus, len(m.prBabysitterStatuses)+len(records))
	for _, record := range m.prBabysitterRecords {
		if sameRepoPath(record.RepoPath, repoPath) {
			continue
		}
		retained = append(retained, record)
		if status, ok := m.prBabysitterStatuses[record.FlowID]; ok {
			statuses[record.FlowID] = status
		}
	}
	for _, record := range records {
		if !prBabysitterEligible(record) {
			continue
		}
		retained = append(retained, record)
		status, ok := m.prBabysitterStatuses[record.FlowID]
		if !ok {
			status = unknownPullRequestStatus()
		}
		statuses[record.FlowID] = status
	}
	m.prBabysitterRecords = retained
	m.prBabysitterStatuses = statuses
	return m.syncPRBabysitterFromCache()
}

func (m Model) restorePRBabysitterExpandedSelection(flowID, phaseID string) Model {
	if flowID == "" {
		m.expandedPRBabysitterFlowID = ""
		m.selectedPRBabysitterPhaseID = ""
		m.prBabysitterFlows = m.prBabysitterFlows.SetItemHeight(flowItemHeight(""))
		return m
	}
	record, ok := m.prBabysitterFlows.Selected()
	if !ok || record.FlowID != flowID {
		m.expandedPRBabysitterFlowID = ""
		m.selectedPRBabysitterPhaseID = ""
		m.prBabysitterFlows = m.prBabysitterFlows.SetItemHeight(flowItemHeight(""))
		return m
	}
	if phaseID != "" {
		phase, ok := flowRecordPhaseByID(record, phaseID)
		if !ok {
			m.expandedPRBabysitterFlowID = ""
			m.selectedPRBabysitterPhaseID = ""
			m.prBabysitterFlows = m.prBabysitterFlows.SetItemHeight(flowItemHeight(""))
			return m
		}
		phaseID = phase.PhaseID
	}
	m.expandedPRBabysitterFlowID = flowID
	m.selectedPRBabysitterPhaseID = phaseID
	m.prBabysitterFlows = m.prBabysitterFlows.SetItemHeight(flowItemHeight(flowID))
	return m
}

func (m Model) reflowPRBabysitter() Model {
	m.prBabysitterFlows = m.prBabysitterFlows.Reflow(m.paneContentHeight(ui.ModePRBabysitter), m.contentWidth())
	if m.prBabysitterSurfaceVisible() {
		if m.selectedPRBabysitterPhaseID != "" {
			return m.ensureSelectedFlowPhaseVisible()
		}
		if m.expandedPRBabysitterFlowID != "" {
			return m.reflowExpandedFlow()
		}
	}
	return m
}
