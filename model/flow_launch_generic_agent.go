package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowownership"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

const (
	flowWorktreeAgentChooseStatus  = "Press A to choose codex, claude, or cursor-agent before starting a Flow worktree agent"
	flowWorktreeAgentPathStatus    = "Flow worktree agent requires the Flow's exact existing worktree directory"
	flowWorktreeAgentPendingStatus = "Another launch or session is already pending for this Flow"
	flowWorktreeAgentChangedStatus = "Configured agent changed while the Flow worktree launch was pending; try again"
	flowWorktreeAgentStaleStatus   = "Flow worktree launch is stale; refresh and try again"
	flowWorktreeAgentSessionStatus = "An active persisted session already occupies this Flow"
	flowWorktreeAgentSlotStatus    = "An embedded terminal already occupies this Flow"
)

// selectedFlowWorktreeAgentReady is a cached rendering predicate. Filesystem
// and persisted-store checks stay at press time and at both authoritative
// lifecycle boundaries.
func (m Model) selectedFlowWorktreeAgentReady() bool {
	if !m.flowSurfaceVisible() {
		return false
	}
	command := agent.Normalize(m.agentCommand)
	if !agent.Supported(command) {
		return false
	}
	record, ok := m.selectedFlow()
	if !ok || !genericWorktreeAgentFlowEligible(record) {
		return false
	}
	return !m.worktreeAgentFooterAdvice(record).Defer()
}

func (m Model) hasKnownActiveFlowSession(flowID string) bool {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return false
	}
	for _, records := range [][]sessions.SessionRecord{m.sessions.Items(), m.worktreeSessions.Items()} {
		for _, record := range records {
			if strings.TrimSpace(record.FlowID) == flowID && sessions.IsActive(record.Status, record.EndedAt) {
				return true
			}
		}
	}
	return false
}

func (m Model) handleStartSelectedFlowWorktreeAgent() (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	record, ok := m.selectedFlow()
	if !ok {
		return m, nil
	}
	next, cmd, _ := m.requestFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindWorktreeAgent,
		FlowID: strings.TrimSpace(record.FlowID),
		Origin: m.flowLaunchOrigin(),
	})
	return next, cmd
}

func (m Model) admitWorktreeAgentFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	intent.FlowID = strings.TrimSpace(intent.FlowID)
	if intent.FlowID == "" {
		return m, nil, false
	}
	record, ok := m.cachedGenericWorktreeAgentRecord(intent.FlowID)
	if !ok || strings.TrimSpace(record.FlowID) != intent.FlowID || flowstore.FlowClosed(record) || flowstore.PreparationLaunchBlocked(record) {
		return m, nil, false
	}
	command, _, _ := m.flowLaunchAgentSettings()
	command = agent.Normalize(command)
	switch command {
	case "":
		return m.setStatus(statusOther, flowWorktreeAgentChooseStatus), nil, false
	case agent.CommandCodex, agent.CommandClaude, agent.CommandCursor:
	default:
		return m.setStatus(statusOther, fmt.Sprintf("Flow worktree agents do not support agent %q; press A to choose codex, claude, or cursor-agent", command)), nil, false
	}
	if strings.TrimSpace(record.WorktreePath) == "" {
		return m.setStatus(statusOther, flowWorktreeAgentPathStatus), nil, false
	}
	if err := m.launchSeams.inspectWorktreeDirectory(record.WorktreePath); err != nil {
		return m.setStatus(statusOther, flowWorktreeAgentPathStatus), nil, false
	}
	advice := m.worktreeAgentFooterAdvice(record)
	switch advice.Holder() {
	case flowownership.HolderLeaseUnreadable:
		return m.setStatus(statusOther, flowLeaseSetupErrorStatus(advice.Err())), nil, false
	case flowownership.HolderPeerLease:
		return m.setStatus(statusOther, flowLeaseOccupiedStatus), nil, false
	case flowownership.HolderRepairAttempt,
		flowownership.HolderPhaseResumeAttempt,
		flowownership.HolderPhaseAttempt,
		flowownership.HolderOtherAttempt:
		return m.setStatus(statusOther, flowWorktreeAgentPendingStatus), nil, false
	}
	if m.tmuxAutofixAgentStillRunning(record, record.WorktreePath) {
		return m.setStatus(statusOther, flowWorktreeAgentPendingStatus), nil, false
	}
	switch advice.Holder() {
	case flowownership.HolderFlowTerminal, flowownership.HolderRepairTerminal:
		return m.setStatus(statusOther, flowWorktreeAgentSlotStatus), nil, false
	case flowownership.HolderFlowSession:
		return m.setStatus(statusOther, flowWorktreeAgentSessionStatus), nil, false
	case flowownership.HolderPhaseSession:
		return m.setStatus(statusOther, fmt.Sprintf("an active persisted session on phase %s already occupies this Flow", advice.PhaseID())), nil, false
	case flowownership.HolderRunningPhase:
		return m.setStatus(statusOther, fmt.Sprintf("a running phase %s already occupies this Flow", advice.PhaseID())), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m, nil, false
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	settings.Command = command
	next, reserved := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token: token, Kind: intent.Kind, FlowID: intent.FlowID, Origin: intent.Origin, Settings: settings,
	}, flowLaunchStateReserved)
	if !reserved {
		return m.setStatus(statusOther, flowWorktreeAgentPendingStatus), nil, false
	}
	next, advanced := next.transitionFlowLaunchAttempt(intent.FlowID, token, flowLaunchStateReserved, flowLaunchStateReading)
	if !advanced {
		return next.releaseFlowLaunchAttempt(intent.FlowID, token), nil, false
	}
	return next, next.flowLaunchReadCmd(intent, token, settings), true
}

func (m Model) cachedGenericWorktreeAgentRecord(flowID string) (flowstore.FlowRecord, bool) {
	flowID = strings.TrimSpace(flowID)
	if selected, ok := m.selectedFlow(); ok && strings.TrimSpace(selected.FlowID) == flowID {
		return selected, true
	}
	return flowstore.FlowRecord{}, false
}

func genericWorktreeAgentFlowEligible(record flowstore.FlowRecord) bool {
	return strings.TrimSpace(record.FlowID) != "" &&
		strings.TrimSpace(record.WorktreePath) != "" &&
		!flowstore.FlowClosed(record) &&
		!flowstore.PreparationLaunchBlocked(record)
}

func worktreeAgentAuthoritativeOccupancyStatus(flowID string, record flowstore.FlowRecord, verdict flowownership.Verdict) string {
	if strings.TrimSpace(record.FlowID) != flowID {
		return flowWorktreeAgentStaleStatus
	}
	if verdict.Err() != nil {
		return verdict.Err().Error()
	}
	switch verdict.Holder() {
	case flowownership.HolderUntrackedOwner:
		return flowWorktreeAgentPendingStatus
	case flowownership.HolderFlowSession:
		return flowWorktreeAgentSessionStatus
	case flowownership.HolderPhaseSession:
		return fmt.Sprintf("an active persisted session on phase %s already occupies this Flow", verdict.PhaseID())
	case flowownership.HolderRunningPhase:
		return fmt.Sprintf("a running phase %s already occupies this Flow", verdict.PhaseID())
	default:
		return flowWorktreeAgentStaleStatus
	}
}

func worktreeAgentFlowLaunchReadCmd(seams flowLaunchSeams, intent flowLaunchIntent, token string) tea.Cmd {
	return func() tea.Msg {
		event := flowLaunchEventMsg{Token: token, Kind: intent.Kind, From: flowLaunchStateReading, FlowID: intent.FlowID, Stage: flowLaunchStageRead}
		record, err := seams.ReadFlow(intent.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if record.FlowID != intent.FlowID || !genericWorktreeAgentFlowEligible(record) {
			event.Err = flowWorktreeAgentStaleStatus
			return event
		}
		if err := seams.inspectWorktreeDirectory(record.WorktreePath); err != nil {
			event.Err = flowWorktreeAgentPathStatus
			return event
		}
		if verdict := flowAuthoritativeOccupancy(seams, intent.FlowID, record, actions.RoleWorktreeAgent); verdict.Occupied() {
			event.Err = worktreeAgentAuthoritativeOccupancyStatus(intent.FlowID, record, verdict)
			return event
		}
		planPath := strings.TrimSpace(record.PlanPath)
		if strings.TrimSpace(record.PlanID) != "" && planPath == "" {
			if seams.PlanMarkdownPath == nil {
				event.Err = "Cannot determine linked plan path for this Flow"
				return event
			}
			planPath, err = seams.PlanMarkdownPath(record.PlanID)
			if err != nil {
				event.Err = err.Error()
				return event
			}
		}
		event.Record = record
		event.RepoPath = record.RepoPath
		event.WorktreePath = record.WorktreePath
		event.PlanPath = planPath
		return event
	}
}

func (m Model) worktreeAgentFlowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	reserve := m.reserveTrackedFlowLaunch
	seams := m.launchSeams
	return func() tea.Msg {
		event := msg
		event.Stage = flowLaunchStagePrepared
		event.From = flowLaunchStatePreparing
		// Ahead of the reservation: this agent's argv carries the pinned path
		// into a session that outlives the launch, so an unusable pin is refused
		// before anything is written.
		if refusal := refuseUnverifiedLaunchPin(settings.Pin); refusal != "" {
			event.Err = refusal
			return event
		}
		record, release, err := reserve(msg.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		event.Release = release
		verdict := m.flowReservedOccupancy(seams, msg.FlowID, record, actions.RoleWorktreeAgent)
		if verdict.Holder() == flowownership.HolderLeaseUnreadable {
			event.LeaseDeferred = true
			event.LeaseSetupError = true
			event.Err = flowLeaseSetupErrorStatus(verdict.Err())
			return event
		} else if verdict.Holder() == flowownership.HolderPeerLease {
			event.LeaseDeferred = true
			event.Err = flowLeaseOccupiedStatus
			return event
		} else if verdict.Occupied() {
			event.Err = worktreeAgentAuthoritativeOccupancyStatus(msg.FlowID, record, verdict)
			return event
		}
		if record.FlowID != msg.FlowID || !genericWorktreeAgentFlowEligible(record) {
			event.Err = flowWorktreeAgentStaleStatus
			return event
		}
		if err := seams.inspectWorktreeDirectory(record.WorktreePath); err != nil {
			event.Err = flowWorktreeAgentPathStatus
			return event
		}
		planPath := strings.TrimSpace(record.PlanPath)
		if strings.TrimSpace(record.PlanID) != "" && planPath == "" {
			if seams.PlanMarkdownPath == nil {
				event.Err = "Cannot determine linked plan path for this Flow"
				return event
			}
			planPath, err = seams.PlanMarkdownPath(record.PlanID)
			if err != nil {
				event.Err = err.Error()
				return event
			}
		}
		event.Record = record
		event.RepoPath = record.RepoPath
		event.WorktreePath = record.WorktreePath
		event.PlanPath = planPath
		ctx, decision, err := newFlowLaunchContext(worktreeAgentTarget{
			LaunchID: msg.Token, Record: record, PlanPath: planPath,
		}, settings, flowLaunchRouting{})
		if err != nil {
			event.Err = err.Error()
			return event
		}
		event.Context = ctx
		event.Route = decision.Route
		event.FallbackNote = decision.FallbackNote
		if err := claimUntrackedOwner(seams, msg.FlowID, msg.Token, msg.Kind); err != nil {
			event.Err = err.Error()
			return event
		}
		event.UntrackedOwnerClaimed = true
		return event
	}
}
