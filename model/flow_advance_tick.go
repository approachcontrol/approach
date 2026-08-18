package model

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

// The advance poll is a session-long 1 Hz loop that fetches the unscoped flow
// set and drives AutoMode phase launches from its own snapshot, independent of
// which view is displayed. It never touches display state.
//
// Options may override the cadence; tests drive the loop far faster so they
// never wait on wall time.
const defaultAutoAdvanceTickInterval = time.Second

// autoAdvanceTickDelay is the cadence this Model polls at. A zero field means
// "unset" — a directly constructed Model still ticks at the production rate.
func (m Model) autoAdvanceTickDelay() time.Duration {
	if m.autoAdvanceTickInterval > 0 {
		return m.autoAdvanceTickInterval
	}
	return defaultAutoAdvanceTickInterval
}

type autoAdvanceTickMsg struct{}

// AutoAdvanceResultMsg carries the advance poll's unscoped flow snapshot. A
// listFlows failure arrives as Err instead of a FetchErrorMsg so the loop
// always reschedules and never stomps display status.
type AutoAdvanceResultMsg struct {
	Flows       []flowstore.FlowRecord
	Degradation *flowstore.PartialListError
	Err         string
	Request     uint64
}

func (m Model) autoAdvanceTickCmd() tea.Cmd {
	return tea.Tick(m.autoAdvanceTickDelay(), func(time.Time) tea.Msg {
		return autoAdvanceTickMsg{}
	})
}

func (m Model) fetchAutoAdvanceFlows(request uint64) tea.Cmd {
	return func() tea.Msg {
		records, err := m.listFlows(flowstore.FlowFilter{})
		if partial, ok := flowstore.AsPartialList(err); ok {
			return AutoAdvanceResultMsg{Flows: records, Degradation: partial, Request: request}
		}
		if err != nil {
			return AutoAdvanceResultMsg{Err: err.Error(), Request: request}
		}
		return AutoAdvanceResultMsg{Flows: records, Request: request}
	}
}

func (m Model) startAutoAdvanceFetch() (Model, tea.Cmd) {
	if m.autoAdvanceInFlight != 0 {
		return m, nil
	}
	m.autoAdvanceRequestSeq++
	m.autoAdvanceInFlight = m.autoAdvanceRequestSeq
	return m, m.fetchAutoAdvanceFlows(m.autoAdvanceInFlight)
}

func (m Model) finishAutoAdvanceFetch(request uint64) (Model, tea.Cmd) {
	if request == 0 || request != m.autoAdvanceInFlight {
		return m, nil
	}
	m.autoAdvanceInFlight = 0
	return m, m.autoAdvanceTickCmd()
}

func (m Model) handleAutoAdvanceResult(msg AutoAdvanceResultMsg) (Model, tea.Cmd) {
	if msg.Request == 0 || msg.Request != m.autoAdvanceInFlight {
		return m.finishAutoAdvanceFetch(msg.Request)
	}
	if msg.Err != "" || msg.Degradation != nil {
		return m.finishAutoAdvanceFetch(msg.Request)
	}

	previous := cloneFlowRecords(m.autoAdvanceSnapshot)
	current := cloneFlowRecords(msg.Flows)

	var cmds []tea.Cmd
	var autoCmd tea.Cmd
	m, autoCmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(previous, current, msg.Request)
	cmds = append(cmds, autoCmd)
	var progressionCmd tea.Cmd
	m, progressionCmd = m.prepareEpicProgressionAdvance(current, msg.Request)
	cmds = append(cmds, progressionCmd)
	m.autoAdvanceSnapshot = current

	statuses := autoAdvanceStatusEvents(previous, current)
	if len(statuses) > 0 {
		var statusCmd tea.Cmd
		m, statusCmd = m.setAutoAdvanceStatus(statuses[len(statuses)-1])
		cmds = append(cmds, statusCmd)
	}

	var tickCmd tea.Cmd
	m, tickCmd = m.finishAutoAdvanceFetch(msg.Request)
	cmds = append(cmds, tickCmd)
	return m, batchNonNil(cmds...)
}

func (m Model) prepareEpicProgressionAdvance(current []flowstore.FlowRecord, request uint64) (Model, tea.Cmd) {
	if len(m.epicProgressionBaselines) == 0 {
		return m, nil
	}
	currentByID := make(map[string]flowstore.FlowRecord, len(current))
	for _, flow := range current {
		if flow.FlowID != "" {
			currentByID[flow.FlowID] = flow
		}
	}
	keys := make([]string, 0, len(m.epicProgressionBaselines))
	for key := range m.epicProgressionBaselines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	start := 0
	if m.epicProgressionAdvanceCursor != "" {
		start = sort.Search(len(keys), func(i int) bool {
			return keys[i] > m.epicProgressionAdvanceCursor
		})
		if start == len(keys) {
			start = 0
		}
	}
	for offset := range keys {
		key := keys[(start+offset)%len(keys)]
		if minimum := m.epicProgressionBaselineMinimumRequests[key]; minimum != 0 {
			if request < minimum {
				continue
			}
			delete(m.epicProgressionBaselineMinimumRequests, key)
		}
		baseline := m.epicProgressionBaselines[key]
		observed, ok := currentByID[baseline.FlowID]
		if !ok {
			continue
		}
		if !sameRepoPath(baseline.RepoPath, observed.RepoPath) || baseline.Bead != observed.Bead {
			continue
		}
		// Both edges are level-triggered on the observed record, never latched:
		// neither branch refreshes the baseline, so an edge that loses the
		// single-flight admission simply refires on the next poll while the
		// condition holds. An edge whose child recovered before admission freed
		// is meant to be forgotten — latching it would halt a healthy child with
		// a stale cause, which is strictly worse than the transient-halt cost
		// this design already accepts.
		switch {
		case epicProgressionSuccessTerminal(observed):
			if epicProgressionSuccessTerminal(baseline) {
				continue
			}
			if owned, ok := m.epicProgressionOwnedSuccessors[key]; ok && owned.SourceFlowID == baseline.FlowID {
				// An advance for this source already owns a created child Flow.
				// The create pipeline clears that marker at every terminal, so
				// this is skipped only while the child's own attempt is running.
				// The halt edge below is deliberately not fenced this way: a
				// failing child must be able to stop the chain regardless.
				continue
			}
			var cmd tea.Cmd
			m, cmd = m.startEpicProgressionAdvance(key, baseline)
			if cmd != nil {
				m.epicProgressionAdvanceCursor = key
				return m, cmd
			}
		case epicProgressionFailureTerminal(observed):
			if epicProgressionFailureTerminal(baseline) {
				continue
			}
			// The halt tuple is composed from the observed record, never from
			// the baseline: a baseline is only ever installed from a pending
			// record, and a pending halt status is rejected by the store.
			var cmd tea.Cmd
			m, cmd = m.startEpicProgressionHalt(key, baseline, observed)
			if cmd != nil {
				m.epicProgressionAdvanceCursor = key
				return m, cmd
			}
		default:
			m.epicProgressionBaselines[key] = cloneFlowRecord(observed)
		}
	}
	return m, nil
}

// epicProgressionResolvedStatus is the single status resolver both progression
// edges classify, so advance and halt are disjoint by construction rather than
// by argument. A deliberate close outranks any recorded or derived status.
func epicProgressionResolvedStatus(flow flowstore.FlowRecord) string {
	if flowstore.FlowClosed(flow) {
		return flowstore.StatusClosed
	}
	if status := strings.TrimSpace(flow.Status); status != "" {
		return status
	}
	return flowstore.DeriveStatus(flow)
}

func epicProgressionSuccessTerminal(flow flowstore.FlowRecord) bool {
	switch epicProgressionResolvedStatus(flow) {
	case flowstore.StatusCompleted, flowstore.StatusMerged:
		return true
	default:
		return false
	}
}

// epicProgressionFailureTerminal covers abandoned for completeness only: no
// code path writes that status, so it is reachable only by hand-editing a
// record. Such a Flow is already refused by rejectEpicProgressionCandidate and
// by Store.CloseFlow, both predating this edge, so halting on it replaces a
// silent permanent stall with a named one rather than creating a new trap.
func epicProgressionFailureTerminal(flow flowstore.FlowRecord) bool {
	switch epicProgressionResolvedStatus(flow) {
	case flowstore.StatusBlocked, flowstore.StatusNeedsAttention, flowstore.StatusClosed, flowstore.StatusAbandoned:
		return true
	default:
		return false
	}
}

func (m Model) seedAutoAdvanceSnapshot(flows []flowstore.FlowRecord) Model {
	if len(m.autoAdvanceSnapshot) != 0 {
		seen := make(map[string]int, len(m.autoAdvanceSnapshot))
		for i, record := range m.autoAdvanceSnapshot {
			if record.FlowID != "" {
				seen[record.FlowID] = i
			}
		}
		for _, record := range flows {
			if record.FlowID == "" {
				continue
			}
			if i, ok := seen[record.FlowID]; ok {
				if autoAdvanceDisplayRecordRefreshesBaseline(m.autoAdvanceSnapshot[i], record) {
					m.autoAdvanceSnapshot[i] = cloneFlowRecord(record)
				}
				continue
			}
			m.autoAdvanceSnapshot = append(m.autoAdvanceSnapshot, cloneFlowRecord(record))
			seen[record.FlowID] = len(m.autoAdvanceSnapshot) - 1
		}
		return m
	}
	m.autoAdvanceSnapshot = cloneFlowRecords(flows)
	return m
}

func autoAdvanceDisplayRecordRefreshesBaseline(previous, current flowstore.FlowRecord) bool {
	previousByPhaseID := make(map[string]flowstore.FlowPhase, len(previous.Phases))
	for _, phase := range previous.Phases {
		if phaseID := artifacts.NormalizePhaseID(phase.PhaseID); phaseID != "" {
			previousByPhaseID[phaseID] = phase
		}
	}
	for _, phase := range current.Phases {
		phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
		if phaseID == "" || phase.Status != flowstore.PhaseRunning {
			continue
		}
		previousPhase, ok := previousByPhaseID[phaseID]
		if !ok {
			return true
		}
		if previousPhase.Status == flowstore.PhaseCompleted || previousPhase.Status == flowstore.PhaseSkipped {
			return true
		}
	}
	return false
}

// autoAdvanceStatusEvents reports the transitions the poll observed. The queued
// announcement is not among them: it is emitted by the launch lifecycle's read
// success branch, which is the hop that knows preflight passed.
func autoAdvanceStatusEvents(previous, current []flowstore.FlowRecord) []string {
	var events []string
	previousByFlowID := make(map[string]flowstore.FlowRecord, len(previous))
	for _, record := range previous {
		if record.FlowID != "" {
			previousByFlowID[record.FlowID] = record
		}
	}
	for _, record := range current {
		previousRecord, ok := previousByFlowID[record.FlowID]
		if !ok {
			continue
		}
		previousPhases := make(map[string]flowstore.FlowPhase, len(previousRecord.Phases))
		for _, phase := range previousRecord.Phases {
			if phaseID := artifacts.NormalizePhaseID(phase.PhaseID); phaseID != "" {
				previousPhases[phaseID] = phase
			}
		}
		for _, phase := range flowstore.OrderedPhases(record.Phases) {
			phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
			if phaseID == "" {
				continue
			}
			previousPhase, ok := previousPhases[phaseID]
			if !ok {
				continue
			}
			switch {
			case phase.Status == flowstore.PhaseNeedsAttention && previousPhase.Status != flowstore.PhaseNeedsAttention:
				events = append(events, "Flow "+flowTitleForStatus(record)+": needs attention")
			case flowstore.SemanticKind(phase) == flowstore.KindMerge && phase.Status == flowstore.PhaseReady && previousPhase.Status != flowstore.PhaseReady:
				events = append(events, "Flow "+flowTitleForStatus(record)+": ready to merge")
			}
		}
	}
	return events
}

func autoAdvancePhaseLabel(phaseID string) string {
	phaseID = artifacts.NormalizePhaseID(phaseID)
	if phaseID == "" {
		return "phase"
	}
	return strings.ReplaceAll(phaseID, "-", " ")
}

func flowTitleForStatus(record flowstore.FlowRecord) string {
	title := strings.TrimSpace(record.Title)
	if title == "" {
		return record.FlowID
	}
	return title
}

func cloneFlowRecord(record flowstore.FlowRecord) flowstore.FlowRecord {
	out := record
	out.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
	return out
}

func cloneFlowRecords(records []flowstore.FlowRecord) []flowstore.FlowRecord {
	if len(records) == 0 {
		return nil
	}
	out := append([]flowstore.FlowRecord(nil), records...)
	for i := range out {
		out[i] = cloneFlowRecord(out[i])
	}
	return out
}
