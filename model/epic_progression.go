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
	target              beadExpansionTarget
	progression         flowstore.EpicProgression
	flow                flowstore.FlowRecord
	baselineDisposition epicProgressionBaselineDisposition
	enabled             bool
	known               bool
	status              string
	release             func()
	preparationKind     flowPreparationKind
	preparationToken    uint64
}

type epicProgressionBaselineDisposition uint8

const (
	epicProgressionBaselinePreserve epicProgressionBaselineDisposition = iota
	epicProgressionBaselineReplace
	epicProgressionBaselineRemove
)

type flowPreparationKind uint8

const (
	flowPreparationNone flowPreparationKind = iota
	flowPreparationReadyBead
	flowPreparationEpicToggle
	flowPreparationEpicAdvance
)

type flowPreparationOwner struct {
	Kind  flowPreparationKind
	Token uint64
}

type epicProgressionOwnedSuccessor struct {
	SourceFlowID string
	ChildID      string
	FlowID       string
}

type epicProgressionAdvanceRequest struct {
	Request      uint64
	OwnerToken   uint64
	EpicKey      string
	SourceFlowID string
}

type epicProgressionAdvanceDisposition uint8

const (
	epicProgressionAdvanceRetryable epicProgressionAdvanceDisposition = iota
	epicProgressionAdvanceInactive
	epicProgressionAdvanceReleased
	epicProgressionAdvanceOwnedObstruction
	epicProgressionAdvanceAccepted
	epicProgressionAdvanceExhausted
	epicProgressionAdvanceExhaustedActive
	epicProgressionAdvanceExhaustedUnknown
)

type epicProgressionAdvanceResultMsg struct {
	request      uint64
	ownerToken   uint64
	epicKey      string
	repoPath     string
	epicID       string
	sourceFlowID string
	disposition  epicProgressionAdvanceDisposition
	owned        epicProgressionOwnedSuccessor
	hasOwned     bool
	flow         flowstore.FlowRecord
	status       string
	release      func()
}

func (m Model) acquireFlowPreparation(kind flowPreparationKind) (Model, uint64, bool) {
	if kind == flowPreparationNone || m.flowPreparationAdmission {
		return m, 0, false
	}
	m.flowPreparationSeq++
	if m.flowPreparationSeq == 0 {
		m.flowPreparationSeq++
	}
	m.flowPreparationAdmission = true
	m.flowPreparationOwner = flowPreparationOwner{Kind: kind, Token: m.flowPreparationSeq}
	return m, m.flowPreparationSeq, true
}

func (m Model) flowPreparationMatches(kind flowPreparationKind, token uint64) bool {
	if !m.flowPreparationAdmission {
		return false
	}
	// Zero-token state exists only in focused legacy tests that construct a
	// Model directly. Production acquisitions always allocate a non-zero token.
	if token == 0 {
		return m.flowPreparationOwner.Token == 0
	}
	return m.flowPreparationOwner == (flowPreparationOwner{Kind: kind, Token: token})
}

func (m Model) releaseFlowPreparation(kind flowPreparationKind, token uint64) Model {
	if !m.flowPreparationMatches(kind, token) {
		return m
	}
	m.flowPreparationAdmission = false
	m.flowPreparationOwner = flowPreparationOwner{}
	return m
}

func (m Model) startEpicProgressionAdvance(epicKey string, baseline flowstore.FlowRecord) (Model, tea.Cmd) {
	var ownerToken uint64
	var admitted bool
	m, ownerToken, admitted = m.acquireFlowPreparation(flowPreparationEpicAdvance)
	if !admitted {
		return m, nil
	}
	m.epicProgressionAdvanceSeq++
	request := epicProgressionAdvanceRequest{
		Request: m.epicProgressionAdvanceSeq, OwnerToken: ownerToken,
		EpicKey: epicKey, SourceFlowID: baseline.FlowID,
	}
	m.activeEpicProgressionAdvance = request
	owned, hasOwned := m.epicProgressionOwnedSuccessors[epicKey]
	return m, m.advanceEpicProgressionCmd(request, baseline, owned, hasOwned)
}

func (m Model) advanceEpicProgressionCmd(request epicProgressionAdvanceRequest, baseline flowstore.FlowRecord, owned epicProgressionOwnedSuccessor, hasOwned bool) tea.Cmd {
	readProgression := m.readEpicProgression
	listChildren := m.listChildrenBeads
	listReady := m.listReadyBeads
	listFlows := m.listFlows
	createFlow := m.createFlow
	reserveFlow := m.reserveEpicSuccessor
	reconcile := m.reconcileEpicSuccessor
	setProgression := m.setEpicProgression
	command, launchModel, reasoningEffort := m.flowLaunchAgentSettings()
	preferences := m.agentPreferences()
	repoPath := filepath.Clean(baseline.RepoPath)
	epicID := strings.TrimSpace(baseline.Bead.EpicID)
	result := func(disposition epicProgressionAdvanceDisposition, status string) epicProgressionAdvanceResultMsg {
		return epicProgressionAdvanceResultMsg{
			request: request.Request, ownerToken: request.OwnerToken, epicKey: request.EpicKey,
			repoPath: repoPath, epicID: epicID, sourceFlowID: request.SourceFlowID,
			disposition: disposition, status: status,
		}
	}
	return func() tea.Msg {
		key := flowstore.EpicProgressionKey{RepoPath: repoPath, EpicID: epicID}
		progression, found, err := readProgression(key)
		if err != nil {
			return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", epicID, err))
		}
		if !found || !progression.Enabled || progression.Halt != nil {
			return result(epicProgressionAdvanceInactive, fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID))
		}

		candidate := owned
		if hasOwned && owned.SourceFlowID == request.SourceFlowID {
			candidate = owned
		} else {
			hasOwned = false
			children, childrenErr := listChildren(repoPath, epicID)
			if childrenErr != nil {
				return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not list children for epic %s: %v", epicID, childrenErr))
			}
			ready, readyErr := listReady(repoPath)
			if readyErr != nil {
				return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not list ready children for epic %s: %v", epicID, readyErr))
			}
			flows, flowsErr := listFlows(flowstore.FlowFilter{RepoPath: repoPath})
			if flowsErr != nil {
				return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not check existing child Flows for epic %s: %v", epicID, flowsErr))
			}
			direct := make(map[string]struct{}, len(children))
			for _, child := range children {
				if id := strings.TrimSpace(child.ID); id != "" {
					direct[id] = struct{}{}
				}
			}
			exactLinked := make(map[string]struct{})
			for _, flow := range flows {
				if filepath.Clean(flow.RepoPath) == repoPath && flow.Bead.EpicID == epicID && flow.Bead.ID != "" {
					exactLinked[flow.Bead.ID] = struct{}{}
				}
			}
			intersection, linked := 0, 0
			var childTitle string
			for _, readyChild := range ready {
				childID := strings.TrimSpace(readyChild.ID)
				if _, ok := direct[childID]; !ok {
					continue
				}
				intersection++
				if _, ok := exactLinked[childID]; ok {
					linked++
					continue
				}
				candidate = epicProgressionOwnedSuccessor{SourceFlowID: request.SourceFlowID, ChildID: childID}
				childTitle = strings.TrimSpace(readyChild.Title)
				break
			}
			if candidate.ChildID == "" {
				status := fmt.Sprintf("Auto-progression complete for epic %s; no ready children remain", epicID)
				if intersection > 0 && linked == intersection {
					status = fmt.Sprintf("Auto-progression complete for epic %s; every ready child already has a Flow", epicID)
				}
				_, disableErr := setProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
				if disableErr == nil {
					return result(epicProgressionAdvanceExhausted, status)
				}
				authoritative, stillFound, readErr := readProgression(key)
				if readErr != nil {
					return result(epicProgressionAdvanceExhaustedUnknown, fmt.Sprintf("Could not confirm auto-progression completion for epic %s: %v", epicID, readErr))
				}
				if !stillFound || !authoritative.Enabled || authoritative.Halt != nil {
					return result(epicProgressionAdvanceExhausted, status)
				}
				return result(epicProgressionAdvanceExhaustedActive, fmt.Sprintf("Could not disable exhausted auto-progression for epic %s: %v", epicID, disableErr))
			}

			link := flowstore.BeadLink{ID: candidate.ChildID, EpicID: epicID}
			flowResult, createErr := createFlow(FlowStartRequest{
				RepoPath: repoPath, Title: candidate.ChildID + ": " + childTitle,
				Instructions: fmt.Sprintf("Use Bead %s as the durable source of requirements. Read it with `bd show %s` before planning or implementation.", candidate.ChildID, candidate.ChildID),
				Bead:         link, AgentCommand: command, Model: launchModel, ReasoningEffort: reasoningEffort,
				AgentPreferences: preferences, AgentPreferencesProvided: true,
			})
			candidate.FlowID = strings.TrimSpace(flowResult.Flow.FlowID)
			if createErr != nil && candidate.FlowID == "" {
				return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not prepare Flow for child %s: %v", candidate.ChildID, createErr))
			}
			if candidate.FlowID == "" {
				return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not prepare Flow for child %s: preparation returned no Flow ID", candidate.ChildID))
			}
			if candidate.FlowID != "" {
				hasOwned = true
			}
		}

		release, reserveErr := reserveFlow(candidate.FlowID)
		if reserveErr != nil {
			msg := result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not reserve owned successor Flow %s: %v", candidate.FlowID, reserveErr))
			msg.owned, msg.hasOwned = candidate, true
			return msg
		}
		link := flowstore.BeadLink{ID: candidate.ChildID, EpicID: epicID}
		reconciled, reconcileErr := reconcile(flowstore.EpicProgressionSuccessorUpdate{FlowID: candidate.FlowID, Key: key, Bead: link})
		msg := result(epicProgressionAdvanceRetryable, "")
		msg.release, msg.owned, msg.hasOwned = release, candidate, true
		if reconcileErr != nil {
			msg.status = fmt.Sprintf("Could not reconcile owned successor Flow %s: %v", candidate.FlowID, reconcileErr)
			return msg
		}
		msg.flow = reconciled.Flow
		switch reconciled.Outcome {
		case flowstore.EpicProgressionSuccessorInactive:
			msg.disposition = epicProgressionAdvanceInactive
			msg.status = fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID)
		case flowstore.EpicProgressionSuccessorReleased:
			msg.disposition = epicProgressionAdvanceReleased
			msg.status = fmt.Sprintf("Owned successor Flow %s was released; auto-progression will retry selection", candidate.FlowID)
		case flowstore.EpicProgressionSuccessorOwnedObstruction:
			msg.disposition = epicProgressionAdvanceOwnedObstruction
			msg.status = rejectOwnedEpicProgressionSuccessor(reconciled.Flow)
		case flowstore.EpicProgressionSuccessorAccepted:
			msg.disposition = epicProgressionAdvanceAccepted
			msg.status = fmt.Sprintf("Prepared next child %s for epic %s as Flow %s", candidate.ChildID, epicID, reconciled.Flow.FlowID)
		default:
			msg.status = fmt.Sprintf("Could not reconcile owned successor Flow %s; auto-progression will retry", candidate.FlowID)
		}
		return msg
	}
}

func rejectOwnedEpicProgressionSuccessor(flow flowstore.FlowRecord) string {
	flowID := strings.TrimSpace(flow.FlowID)
	if flow.PreparedAt == nil {
		return fmt.Sprintf("Owned successor Flow %s is missing its preparation receipt and blocks auto-progression", flowID)
	}
	if flowstore.FlowClosed(flow) {
		return fmt.Sprintf("Owned successor Flow %s is closed and blocks auto-progression", flowID)
	}
	status := strings.TrimSpace(flow.Status)
	if status == "" {
		status = flowstore.DeriveStatus(flow)
	}
	if status != flowstore.StatusPending {
		return fmt.Sprintf("Owned successor Flow %s is %s and blocks auto-progression", flowID, status)
	}
	return ""
}

func (m Model) handleEpicProgressionAdvanceResult(msg epicProgressionAdvanceResultMsg) (Model, tea.Cmd) {
	active := m.activeEpicProgressionAdvance
	baseline, baselineFound := m.epicProgressionBaselines[msg.epicKey]
	current := msg.request != 0 && active.Request == msg.request && active.OwnerToken == msg.ownerToken &&
		active.EpicKey == msg.epicKey && active.SourceFlowID == msg.sourceFlowID &&
		baselineFound && baseline.FlowID == msg.sourceFlowID &&
		m.flowPreparationMatches(flowPreparationEpicAdvance, msg.ownerToken)
	if !current {
		if msg.release != nil {
			msg.release()
		}
		return m, nil
	}
	if m.epicProgressionOwnedSuccessors == nil {
		m.epicProgressionOwnedSuccessors = make(map[string]epicProgressionOwnedSuccessor)
	}
	if msg.hasOwned {
		m.epicProgressionOwnedSuccessors[msg.epicKey] = msg.owned
	}
	switch msg.disposition {
	case epicProgressionAdvanceAccepted:
		m.epicProgressionBaselines[msg.epicKey] = cloneFlowRecord(msg.flow)
		if m.epicProgressionBaselineMinimumRequests == nil {
			m.epicProgressionBaselineMinimumRequests = make(map[string]uint64)
		}
		m.epicProgressionBaselineMinimumRequests[msg.epicKey] = m.autoAdvanceRequestSeq + 1
		delete(m.epicProgressionOwnedSuccessors, msg.epicKey)
	case epicProgressionAdvanceInactive, epicProgressionAdvanceExhausted, epicProgressionAdvanceExhaustedUnknown:
		delete(m.epicProgressionBaselines, msg.epicKey)
		delete(m.epicProgressionBaselineMinimumRequests, msg.epicKey)
		delete(m.epicProgressionOwnedSuccessors, msg.epicKey)
	case epicProgressionAdvanceReleased:
		delete(m.epicProgressionOwnedSuccessors, msg.epicKey)
	}
	m.activeEpicProgressionAdvance = epicProgressionAdvanceRequest{}
	if msg.release != nil {
		msg.release()
	}
	m = m.releaseFlowPreparation(flowPreparationEpicAdvance, msg.ownerToken)
	var statusCmd tea.Cmd
	if strings.TrimSpace(msg.status) != "" {
		m, statusCmd = m.setAutoAdvanceLaunchStatus(msg.status)
	}
	var flowRefreshCmd, progressionRefreshCmd tea.Cmd
	hasPersistedFlow := strings.TrimSpace(msg.flow.FlowID) != "" ||
		(msg.hasOwned && strings.TrimSpace(msg.owned.FlowID) != "")
	if hasPersistedFlow && m.flowRefreshSurfaceVisible() {
		m, flowRefreshCmd = m.startFlowSurfaceFetch()
	}
	target := m.beadExpansion.target
	if target.token != 0 && filepath.Clean(target.repoPath) == msg.repoPath && strings.TrimSpace(target.epicID) == msg.epicID {
		progressionRefreshCmd = m.readEpicProgressionCmd(target)
	}
	return m, batchNonNil(statusCmd, flowRefreshCmd, progressionRefreshCmd)
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
	var token uint64
	var admitted bool
	m, token, admitted = m.acquireFlowPreparation(flowPreparationEpicToggle)
	if !admitted {
		return m, nil
	}
	ownedCmd := func(cmd tea.Cmd) tea.Cmd {
		return func() tea.Msg {
			msg := cmd()
			if result, ok := msg.(epicProgressionToggleResultMsg); ok {
				result.preparationKind = flowPreparationEpicToggle
				result.preparationToken = token
				return result
			}
			return msg
		}
	}
	if projection.ProgressionEnabled {
		return m, ownedCmd(m.disableEpicProgressionCmd(target))
	}
	return m, ownedCmd(m.enableEpicProgressionCmd(target, projection))
}

func (m Model) disableEpicProgressionCmd(target beadExpansionTarget) tea.Cmd {
	setProgression := m.setEpicProgression
	readProgression := m.readEpicProgression
	return func() tea.Msg {
		key := flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID}
		progression, err := setProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
		if err == nil {
			return epicProgressionToggleResultMsg{target: target, progression: progression, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Disabled auto-progression for epic %s", target.epicID)}
		}
		authoritative, found, readErr := readProgression(key)
		if readErr != nil {
			return epicProgressionToggleResultMsg{target: target, known: false,
				status: fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", target.epicID, readErr)}
		}
		if !found || !authoritative.Enabled {
			return epicProgressionToggleResultMsg{target: target, progression: authoritative, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Disabled auto-progression for epic %s", target.epicID)}
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
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("No ready child for epic %s; auto-progression remains off", target.epicID)}
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
			return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
				status: fmt.Sprintf("Could not check existing child Flows: %v", err)}
		}
		matches := make([]flowstore.FlowRecord, 0, 1)
		for _, flow := range flows {
			if filepath.Clean(flow.RepoPath) == filepath.Clean(target.repoPath) && flow.Bead == link {
				matches = append(matches, flow)
			}
		}
		if len(matches) > 1 {
			return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
				status: fmt.Sprintf("Multiple Flows exist for child %s; auto-progression remains off", childID)}
		}
		var flow flowstore.FlowRecord
		if len(matches) == 1 {
			flow = matches[0]
			if detail := rejectEpicProgressionCandidate(flow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove, status: detail}
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
					return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not prepare Flow for child %s: %v", childID, createErr)}
				}
				authoritative, readErr := readFlow(flow.FlowID)
				if readErr != nil {
					return epicProgressionToggleResultMsg{target: target, flow: flow, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not confirm preparation for Flow %s; auto-progression state is unknown", flow.FlowID)}
				}
				if authoritative.PreparedAt == nil {
					return epicProgressionToggleResultMsg{target: target, flow: authoritative, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Flow %s exists but preparation is incomplete; auto-progression remains off", flow.FlowID)}
				}
				flow = authoritative
			}
			if detail := rejectEpicProgressionCandidate(flow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove, status: detail}
			}
		}

		authoritative, release, err := reserveFlow(flow.FlowID)
		if err != nil {
			return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
				status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", flow.FlowID, err)}
		}
		progression, enabledFlow, err := enableProgression(flowstore.PreparedEpicProgressionUpdate{
			FlowID: authoritative.FlowID,
			Key:    flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID},
			Bead:   link,
		})
		if err == nil {
			return epicProgressionToggleResultMsg{target: target, progression: progression, flow: enabledFlow,
				baselineDisposition: epicProgressionBaselineReplace, enabled: true, known: true, release: release,
				status: fmt.Sprintf("Enabled auto-progression for epic %s; Flow %s is prepared", target.epicID, enabledFlow.FlowID)}
		}
		resultFlow := enabledFlow
		if strings.TrimSpace(resultFlow.FlowID) == "" {
			resultFlow = authoritative
		}
		confirmed, found, readErr := readProgression(flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID})
		if readErr != nil {
			return epicProgressionToggleResultMsg{target: target, flow: resultFlow, release: release,
				status: fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", target.epicID, readErr)}
		}
		if found && confirmed.Enabled {
			if !flowstore.IsPreparedEpicProgressionCommitUnknown(err) {
				return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: resultFlow,
					enabled: true, known: true, release: release,
					status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", resultFlow.FlowID, err)}
			}
			return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: resultFlow,
				baselineDisposition: epicProgressionBaselineReplace, enabled: true, known: true, release: release,
				status: fmt.Sprintf("Enabled auto-progression for epic %s; Flow %s is prepared", target.epicID, resultFlow.FlowID)}
		}
		return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: resultFlow,
			baselineDisposition: epicProgressionBaselineRemove, known: true, release: release,
			status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", resultFlow.FlowID, err)}
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
	currentOwner := (msg.preparationToken == 0 && m.flowPreparationOwner.Token == 0) ||
		m.flowPreparationMatches(msg.preparationKind, msg.preparationToken)
	if currentOwner {
		switch msg.baselineDisposition {
		case epicProgressionBaselineReplace:
			if m.epicProgressionBaselines == nil {
				m.epicProgressionBaselines = make(map[string]flowstore.FlowRecord)
			}
			if m.epicProgressionBaselineMinimumRequests == nil {
				m.epicProgressionBaselineMinimumRequests = make(map[string]uint64)
			}
			m.epicProgressionBaselines[key] = msg.flow
			m.epicProgressionBaselineMinimumRequests[key] = m.autoAdvanceRequestSeq + 1
			delete(m.epicProgressionOwnedSuccessors, key)
		case epicProgressionBaselineRemove:
			delete(m.epicProgressionBaselines, key)
			delete(m.epicProgressionBaselineMinimumRequests, key)
			delete(m.epicProgressionOwnedSuccessors, key)
		}
	}
	if msg.release != nil {
		msg.release()
	}
	m = m.releaseFlowPreparation(msg.preparationKind, msg.preparationToken)
	if !currentOwner {
		var flowRefreshCmd, progressionRefreshCmd tea.Cmd
		if m.flowRefreshSurfaceVisible() && msg.flow.FlowID != "" {
			m, flowRefreshCmd = m.startFlowSurfaceFetch()
		}
		current := m.beadExpansion.target
		if current.token != 0 && filepath.Clean(current.repoPath) == filepath.Clean(msg.target.repoPath) && current.epicID == msg.target.epicID {
			progressionRefreshCmd = m.readEpicProgressionCmd(current)
		}
		return m, batchNonNil(flowRefreshCmd, progressionRefreshCmd)
	}
	var flowRefreshCmd tea.Cmd
	if m.flowRefreshSurfaceVisible() && msg.flow.FlowID != "" {
		m, flowRefreshCmd = m.startFlowSurfaceFetch()
	}
	if msg.target != m.beadExpansion.target {
		var progressionRefreshCmd tea.Cmd
		current := m.beadExpansion.target
		if current.token != 0 && current.repoPath == msg.target.repoPath && current.epicID == msg.target.epicID {
			progressionRefreshCmd = m.readEpicProgressionCmd(current)
		}
		return m, batchNonNil(flowRefreshCmd, progressionRefreshCmd)
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
	m = m.reflowBeadExpansionPane()
	m = m.setStatus(statusOther, msg.status)
	return m, flowRefreshCmd
}

func (m Model) readEpicProgressionCmd(target beadExpansionTarget) tea.Cmd {
	readProgression := m.readEpicProgression
	return func() tea.Msg {
		progression, found, err := readProgression(flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID})
		return beadProgressionResultMsg{target: target, progression: progression, found: found, err: err}
	}
}

func epicProgressionBaselineKey(repoPath, epicID string) string {
	return filepath.Clean(repoPath) + "\x00" + strings.TrimSpace(epicID)
}
