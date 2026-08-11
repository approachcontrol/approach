package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

// The advance poll is a session-long 1 Hz loop that fetches the unscoped flow
// set and drives AutoMode phase launches from its own snapshot, independent of
// which view is displayed. It never touches display state.
const autoAdvanceTickInterval = time.Second

type autoAdvanceTickMsg struct{}

// AutoAdvanceResultMsg carries the advance poll's unscoped flow snapshot. A
// listFlows failure arrives as Err instead of a FetchErrorMsg so the loop
// always reschedules and never stomps display status.
type AutoAdvanceResultMsg struct {
	Flows   []flowstore.FlowRecord
	Err     string
	Request uint64
}

func autoAdvanceTickCmd() tea.Cmd {
	return tea.Tick(autoAdvanceTickInterval, func(time.Time) tea.Msg {
		return autoAdvanceTickMsg{}
	})
}

func (m Model) fetchAutoAdvanceFlows(request uint64) tea.Cmd {
	return func() tea.Msg {
		records, err := m.listFlows(flowstore.FlowFilter{})
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
	return m, autoAdvanceTickCmd()
}

func (m Model) handleAutoAdvanceResult(msg AutoAdvanceResultMsg) (Model, tea.Cmd) {
	if msg.Request == 0 || msg.Request != m.autoAdvanceInFlight {
		return m.finishAutoAdvanceFetch(msg.Request)
	}
	if msg.Err != "" {
		return m.finishAutoAdvanceFetch(msg.Request)
	}

	previous := cloneFlowRecords(m.autoAdvanceSnapshot)
	current := cloneFlowRecords(msg.Flows)

	var cmds []tea.Cmd
	var autoCmd tea.Cmd
	m, autoCmd, _ = m.prepareAutoFlowPhaseLaunchForRequest(previous, current, msg.Request)
	cmds = append(cmds, autoCmd)
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
