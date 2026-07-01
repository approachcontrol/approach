package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/internal/artifacts"
)

// The advance poll is a session-long 1 Hz loop that fetches the unscoped flow
// set and drives AutoMode phase launches from its own snapshot, independent of
// which view is displayed. It never touches display state.
const autoAdvanceTickInterval = time.Second

type autoAdvanceTickMsg struct{}

type autoAdvanceLaunchedPhase struct {
	FlowTitle string
	PhaseID   string
}

// AutoAdvanceResultMsg carries the advance poll's unscoped flow snapshot. A
// listFlows failure arrives as Err instead of a FetchErrorMsg so the loop
// always reschedules and never stomps display status.
type AutoAdvanceResultMsg struct {
	Flows   []flowstore.FlowRecord
	Err     string
	Request uint64
}

type AutoAdvanceStatusExpiredMsg struct {
	Seq  uint64
	Text string
}

func autoAdvanceTickCmd() tea.Cmd {
	return tea.Tick(autoAdvanceTickInterval, func(time.Time) tea.Msg {
		return autoAdvanceTickMsg{}
	})
}

func expireAutoAdvanceStatus(seq uint64, text string) tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return AutoAdvanceStatusExpiredMsg{Seq: seq, Text: text}
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
	m.autoAdvanceSnapshot = current
	m.autoAdvanceLaunchedPhases = nil

	var cmds []tea.Cmd
	var autoCmd tea.Cmd
	m, autoCmd = m.prepareAutoFlowPhaseLaunch(previous, current)
	cmds = append(cmds, autoCmd)

	var deferredCmd tea.Cmd
	m, deferredCmd = m.prepareDeferredAutoFlowPhaseLaunchesFrom(m.autoAdvanceSnapshot)
	cmds = append(cmds, deferredCmd)

	statuses := autoAdvanceStatusEvents(previous, m.autoAdvanceLaunchedPhases, current)
	if len(statuses) > 0 {
		var statusCmd tea.Cmd
		m, statusCmd = m.setAutoAdvanceStatus(statuses[len(statuses)-1])
		cmds = append(cmds, statusCmd)
	}
	m.autoAdvanceLaunchedPhases = nil

	var tickCmd tea.Cmd
	m, tickCmd = m.finishAutoAdvanceFetch(msg.Request)
	cmds = append(cmds, tickCmd)
	return m, batchNonNil(cmds...)
}

func (m Model) setAutoAdvanceStatus(text string) (Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, nil
	}
	if m.status.Text != "" && m.status.Source != statusFlowAutoAdvance {
		return m, nil
	}
	m.autoAdvanceStatusSeq++
	seq := m.autoAdvanceStatusSeq
	m.status = statusError{Text: text, Source: statusFlowAutoAdvance}
	return m, expireAutoAdvanceStatus(seq, text)
}

func (m Model) handleAutoAdvanceStatusExpired(msg AutoAdvanceStatusExpiredMsg) Model {
	if msg.Seq == 0 || msg.Seq != m.autoAdvanceStatusSeq {
		return m
	}
	if m.status.Source == statusFlowAutoAdvance && m.status.Text == msg.Text {
		m.status = statusError{}
	}
	return m
}

func autoAdvanceStatusEvents(previous []flowstore.FlowRecord, launched []autoAdvanceLaunchedPhase, current []flowstore.FlowRecord) []string {
	var events []string
	for _, launch := range launched {
		title := strings.TrimSpace(launch.FlowTitle)
		if title == "" {
			continue
		}
		events = append(events, "Flow "+title+": "+autoAdvancePhaseLabel(launch.PhaseID)+" started")
	}

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
			case phaseID == "merge" && phase.Status == flowstore.PhaseReady && previousPhase.Status != flowstore.PhaseReady:
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

func cloneFlowRecords(records []flowstore.FlowRecord) []flowstore.FlowRecord {
	if len(records) == 0 {
		return nil
	}
	out := append([]flowstore.FlowRecord(nil), records...)
	for i := range out {
		out[i].Phases = append([]flowstore.FlowPhase(nil), out[i].Phases...)
	}
	return out
}
