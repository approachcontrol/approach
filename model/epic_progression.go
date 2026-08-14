package model

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

type epicProgressionToggleResultMsg struct {
	target      beadExpansionTarget
	progression flowstore.EpicProgression
	flow        flowstore.FlowRecord
	enabled     bool
	known       bool
	status      string
	release     func()
}

func (m Model) epicProgressionKeysOwned() bool {
	terminalInputFocused := m.terminalEffectivelyExpanded() && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal && m.hasActiveEmbeddedTerminal()
	if m.modal.IsOpen() || m.searchActive || terminalInputFocused || !m.contentListInputEligible() || !ui.IsBeadsMode(m.focusedMode()) {
		return false
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return false
	}
	bead, ok := m.selectedVisibleBead()
	if !ok || !isEpicBead(bead) {
		return false
	}
	target := m.beadExpansion.target
	return target.token != 0 && target.repoPath == repoPath && target.mode == m.focusedMode() && target.epicID == bead.ID
}

func (m Model) canDisableEpicProgression() bool {
	projection := m.beadExpansion.projection
	return m.epicProgressionKeysOwned() && !m.flowPreparationAdmission && projection.ProgressionKnown && projection.ProgressionEnabled
}

func (m Model) canEnableEpicProgression() bool {
	projection := m.beadExpansion.projection
	return m.epicProgressionKeysOwned() && !m.flowPreparationAdmission && projection.ProgressionKnown && !projection.ProgressionEnabled &&
		projection.State == ui.BeadExpansionLoaded && projection.ReadinessKnown
}

func (m Model) handleToggleEpicProgression() (Model, tea.Cmd) {
	if !m.canDisableEpicProgression() && !m.canEnableEpicProgression() {
		return m, nil
	}
	target := m.beadExpansion.target
	projection := cloneBeadExpansion(m.beadExpansion.projection)
	m.flowPreparationAdmission = true
	if projection.ProgressionEnabled {
		return m, m.disableEpicProgressionCmd(target)
	}
	return m, m.enableEpicProgressionCmd(target, projection)
}

func (m Model) disableEpicProgressionCmd(target beadExpansionTarget) tea.Cmd {
	setProgression := m.setEpicProgression
	readProgression := m.readEpicProgression
	return func() tea.Msg {
		key := flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID}
		progression, err := setProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
		if err == nil {
			return epicProgressionToggleResultMsg{target: target, progression: progression, known: true,
				status: fmt.Sprintf("Disabled auto-progression for epic %s", target.epicID)}
		}
		authoritative, found, readErr := readProgression(key)
		if readErr != nil {
			return epicProgressionToggleResultMsg{target: target, known: false,
				status: fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", target.epicID, readErr)}
		}
		if !found || !authoritative.Enabled {
			return epicProgressionToggleResultMsg{target: target, progression: authoritative, known: true,
				status: fmt.Sprintf("Disabled auto-progression for epic %s", target.epicID)}
		}
		return epicProgressionToggleResultMsg{target: target, progression: authoritative, enabled: true, known: true,
			status: fmt.Sprintf("Could not disable auto-progression for epic %s: %v", target.epicID, err)}
	}
}

func (m Model) enableEpicProgressionCmd(target beadExpansionTarget, projection ui.BeadExpansion) tea.Cmd {
	var childID, childTitle string
	for _, child := range projection.Children {
		if projection.ReadyIDs[child.ID] {
			childID = strings.TrimSpace(child.ID)
			childTitle = strings.TrimSpace(child.Title)
			break
		}
	}
	if childID == "" {
		return func() tea.Msg {
			return epicProgressionToggleResultMsg{target: target, known: true,
				status: fmt.Sprintf("No ready child for epic %s; auto-progression remains off", target.epicID)}
		}
	}
	listFlows := m.listFlows
	createFlow := m.createFlow
	reserveFlow := m.reserveFlowLaunch
	enableProgression := m.enableEpicProgression
	readProgression := m.readEpicProgression
	readFlow := m.launchSeams.ReadFlow
	command, launchModel, reasoningEffort := m.flowLaunchAgentSettings()
	preferences := m.agentPreferences()
	return func() tea.Msg {
		link := flowstore.BeadLink{ID: childID, EpicID: target.epicID}
		flows, err := listFlows(flowstore.FlowFilter{RepoPath: target.repoPath})
		if err != nil {
			return epicProgressionToggleResultMsg{target: target, status: fmt.Sprintf("Could not check existing child Flows: %v", err)}
		}
		matches := make([]flowstore.FlowRecord, 0, 1)
		for _, flow := range flows {
			if filepath.Clean(flow.RepoPath) == filepath.Clean(target.repoPath) && flow.Bead == link {
				matches = append(matches, flow)
			}
		}
		if len(matches) > 1 {
			return epicProgressionToggleResultMsg{target: target,
				status: fmt.Sprintf("Multiple Flows exist for child %s; auto-progression remains off", childID)}
		}
		var flow flowstore.FlowRecord
		if len(matches) == 1 {
			flow = matches[0]
			if detail := rejectEpicProgressionCandidate(flow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, status: detail}
			}
		} else {
			title := childID + ": " + childTitle
			instructions := fmt.Sprintf("Use Bead %s as the durable source of requirements. Read it with `bd show %s` before planning or implementation.", childID, childID)
			result, createErr := createFlow(FlowStartRequest{
				RepoPath: target.repoPath, Title: title, Instructions: instructions, Bead: link,
				AgentCommand: command, Model: launchModel, ReasoningEffort: reasoningEffort,
				AgentPreferences: preferences, AgentPreferencesProvided: true,
			})
			flow = result.Flow
			if createErr != nil {
				if strings.TrimSpace(flow.FlowID) == "" {
					return epicProgressionToggleResultMsg{target: target, status: fmt.Sprintf("Could not prepare Flow for child %s: %v", childID, createErr)}
				}
				authoritative, readErr := readFlow(flow.FlowID)
				if readErr != nil {
					return epicProgressionToggleResultMsg{target: target, status: fmt.Sprintf("Could not confirm preparation for Flow %s; auto-progression state is unknown", flow.FlowID)}
				}
				if authoritative.PreparedAt == nil {
					return epicProgressionToggleResultMsg{target: target, flow: authoritative, status: fmt.Sprintf("Flow %s exists but preparation is incomplete; auto-progression remains off", flow.FlowID)}
				}
				flow = authoritative
			}
			if detail := rejectEpicProgressionCandidate(flow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, flow: flow, status: detail}
			}
		}

		authoritative, release, err := reserveFlow(flow.FlowID)
		if err != nil {
			return epicProgressionToggleResultMsg{target: target, flow: flow,
				status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", flow.FlowID, err)}
		}
		progression, enabledFlow, err := enableProgression(flowstore.PreparedEpicProgressionUpdate{
			FlowID: authoritative.FlowID,
			Key:    flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID},
			Bead:   link,
		})
		if err == nil {
			return epicProgressionToggleResultMsg{target: target, progression: progression, flow: enabledFlow,
				enabled: true, known: true, release: release,
				status: fmt.Sprintf("Enabled auto-progression for epic %s; Flow %s is prepared", target.epicID, enabledFlow.FlowID)}
		}
		confirmed, found, readErr := readProgression(flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID})
		if readErr != nil {
			return epicProgressionToggleResultMsg{target: target, flow: authoritative, release: release,
				status: fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", target.epicID, readErr)}
		}
		if found && confirmed.Enabled {
			return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: authoritative,
				enabled: true, known: true, release: release,
				status: fmt.Sprintf("Enabled auto-progression for epic %s; Flow %s is prepared", target.epicID, authoritative.FlowID)}
		}
		return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: authoritative,
			known: true, release: release,
			status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", authoritative.FlowID, err)}
	}
}

func rejectEpicProgressionCandidate(flow flowstore.FlowRecord) string {
	flowID := strings.TrimSpace(flow.FlowID)
	if flowstore.FlowClosed(flow) {
		return fmt.Sprintf("Flow %s is closed; auto-progression remains off", flowID)
	}
	status := strings.TrimSpace(flow.Status)
	if status == "" {
		status = flowstore.DeriveStatus(flow)
	}
	switch status {
	case flowstore.StatusNeedsAttention, flowstore.StatusBlocked, flowstore.StatusAbandoned,
		flowstore.StatusCompleted, flowstore.StatusMerged, flowstore.StatusInProgress:
		return fmt.Sprintf("Flow %s is %s; auto-progression remains off", flowID, status)
	case flowstore.StatusPending:
		if flow.PreparedAt == nil {
			return fmt.Sprintf("Flow %s exists but preparation is incomplete; auto-progression remains off", flowID)
		}
		return ""
	default:
		return fmt.Sprintf("Flow %s is %s; auto-progression remains off", flowID, status)
	}
}

func (m Model) handleEpicProgressionToggleResult(msg epicProgressionToggleResultMsg) (Model, tea.Cmd) {
	key := epicProgressionBaselineKey(msg.target.repoPath, msg.target.epicID)
	if msg.known && msg.enabled {
		if m.epicProgressionBaselines == nil {
			m.epicProgressionBaselines = make(map[string]flowstore.FlowRecord)
		}
		m.epicProgressionBaselines[key] = msg.flow
	} else if msg.known {
		delete(m.epicProgressionBaselines, key)
	}
	if msg.release != nil {
		msg.release()
	}
	m.flowPreparationAdmission = false
	if msg.target != m.beadExpansion.target {
		return m, nil
	}
	projection := cloneBeadExpansion(m.beadExpansion.projection)
	projection.ProgressionKnown = msg.known
	projection.ProgressionEnabled = msg.known && msg.enabled
	if !msg.known {
		projection.ProgressionDetail = msg.status
	} else {
		projection.ProgressionDetail = ""
	}
	m.beadExpansion.projection = projection
	m = m.setStatus(statusOther, msg.status)
	if m.flowRefreshSurfaceVisible() && msg.flow.FlowID != "" {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func epicProgressionBaselineKey(repoPath, epicID string) string {
	return filepath.Clean(repoPath) + "\x00" + strings.TrimSpace(epicID)
}
