package model

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

const flowImplementationPhaseID = "implementation"

func (m Model) handleFlowEnter() (tea.Model, tea.Cmd) {
	record, ok := m.selectedFlow()
	if !ok {
		return m, nil
	}
	phase, explicit, ok := m.flowImplementationPhaseForAction(record)
	if !ok {
		m = m.setStatus(statusOther, "Implementation is not ready for this flow")
		return m, nil
	}
	if err := validateFlowImplementationReady(record, phase); err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	if m.agentCommand == "" {
		m = m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching a flow phase")
		return m, nil
	}
	return m, m.requestFlowImplementationLaunch(record, phase, explicit, m.currentListRequest(ui.ModeFlows))
}

func (m Model) flowImplementationPhaseForAction(record flowstore.FlowRecord) (flowstore.FlowPhase, bool, bool) {
	if phase, ok := m.selectedFlowPhase(); ok {
		if phase.PhaseID != flowImplementationPhaseID {
			return flowstore.FlowPhase{}, true, false
		}
		return phase, true, true
	}
	for _, phase := range record.Phases {
		if phase.PhaseID == flowImplementationPhaseID {
			return phase, false, true
		}
	}
	return flowstore.FlowPhase{}, false, false
}

func validateFlowImplementationReady(record flowstore.FlowRecord, phase flowstore.FlowPhase) error {
	if phase.PhaseID != flowImplementationPhaseID {
		return fmt.Errorf("Only the Implementation phase can be launched from Flow mode")
	}
	switch phase.Status {
	case flowstore.PhaseReady, flowstore.PhaseRunning:
	default:
		return fmt.Errorf("Implementation is %s, not ready to launch or resume", phase.Status)
	}
	if strings.TrimSpace(record.WorktreePath) == "" {
		return fmt.Errorf("Flow has no worktree path for Implementation")
	}
	if strings.TrimSpace(record.PlanID) == "" || strings.TrimSpace(record.PlanPath) == "" {
		return fmt.Errorf("Flow needs a linked plan before Implementation")
	}
	if !planReviewAllowsImplementation(record) {
		return fmt.Errorf("Implementation requires an approved Plan Review")
	}
	return nil
}

func planReviewAllowsImplementation(record flowstore.FlowRecord) bool {
	for _, phase := range record.Phases {
		if phase.PhaseID != "plan-review" {
			continue
		}
		switch phase.Status {
		case flowstore.PhaseCompleted:
			outcome := strings.TrimSpace(phase.Outcome)
			return outcome == "approved" || outcome == "approved_with_concerns"
		case flowstore.PhaseSkipped:
			return strings.TrimSpace(phase.Notes) != ""
		default:
			return false
		}
	}
	return false
}

func (m Model) requestFlowImplementationLaunch(record flowstore.FlowRecord, phase flowstore.FlowPhase, requireSelectedPhase bool, listRequest uint64) tea.Cmd {
	launchID := newLaunchID()
	resumeSession, hasResume := m.latestFlowPhaseResumeSession(phase)
	return func() tea.Msg {
		return FlowImplementationLaunchRequestedMsg{
			RepoPath:             record.RepoPath,
			FlowID:               record.FlowID,
			FlowPhaseID:          phase.PhaseID,
			RequireSelectedPhase: requireSelectedPhase,
			ListRequest:          listRequest,
			Record:               record,
			Phase:                phase,
			LaunchID:             launchID,
			ResumeSession:        resumeSession,
			HasResumeSession:     hasResume,
		}
	}
}

func (m Model) startFlowImplementationLaunch(msg FlowImplementationLaunchRequestedMsg) (tea.Model, tea.Cmd) {
	if _, err := m.addFlowPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:                    msg.FlowID,
		PhaseID:                   msg.FlowPhaseID,
		LaunchID:                  msg.LaunchID,
		RequirePlanReviewApproved: true,
	}); err != nil {
		m = m.setStatus(statusOther, err.Error())
		return m, nil
	}
	ctx := m.flowImplementationLaunchContext(msg.Record, msg.Phase, msg.LaunchID)
	if msg.HasResumeSession {
		ctx = m.flowImplementationResumeContext(ctx, msg.ResumeSession)
	}
	next, launchCmd := m.launchAgentWithContext(ctx)
	if next.mode == ui.ModeFlows {
		next, fetchCmd := next.startFetchFlows()
		return next, tea.Batch(fetchCmd, launchCmd)
	}
	return next, launchCmd
}

func (m Model) flowImplementationLaunchContext(record flowstore.FlowRecord, phase flowstore.FlowPhase, launchID string) actions.AgentLaunchContext {
	return actions.AgentLaunchContext{
		Command:          m.agentCommand,
		LaunchID:         launchID,
		RepoPath:         record.RepoPath,
		WorktreePath:     record.WorktreePath,
		WorkingDir:       record.WorktreePath,
		Branch:           record.Branch,
		Commit:           record.Commit,
		SessionStateRoot: m.sessionStateRoot,
		PlanID:           record.PlanID,
		PlanPath:         record.PlanPath,
		PlanPhaseID:      phase.PhaseID,
		PlanPhaseTitle:   phase.Title,
		PlanPhaseStatus:  flowstore.PhaseRunning,
		FlowID:           record.FlowID,
		FlowPhaseID:      phase.PhaseID,
		InitialPrompt:    flowImplementationPrompt(record),
	}
}

func (m Model) flowImplementationResumeContext(ctx actions.AgentLaunchContext, session flowstore.Session) actions.AgentLaunchContext {
	provider := sessions.Provider(session.Provider)
	command := string(provider)
	if provider == sessions.ProviderCodex && agent.Normalize(m.agentCommand) == agent.CommandCodexApp {
		command = agent.CommandCodexApp
	}
	ctx.Command = command
	ctx.ResumeSessionID = session.SessionID
	ctx.WorkingDir = session.CWD
	if ctx.WorkingDir == "" {
		ctx.WorkingDir = ctx.WorktreePath
	}
	ctx.InitialPrompt = ""
	return ctx
}

func (m Model) latestFlowPhaseResumeSession(phase flowstore.FlowPhase) (flowstore.Session, bool) {
	want := flowProviderForAgent(m.agentCommand)
	if want == "" {
		return flowstore.Session{}, false
	}
	var best flowstore.Session
	found := false
	for _, session := range phase.Sessions {
		if strings.TrimSpace(session.SessionID) == "" || session.Provider != string(want) {
			continue
		}
		if !found || flowSessionSortTime(session).After(flowSessionSortTime(best)) || (flowSessionSortTime(session).Equal(flowSessionSortTime(best)) && session.SessionID > best.SessionID) {
			best = session
			found = true
		}
	}
	return best, found
}

func flowProviderForAgent(command string) sessions.Provider {
	switch agent.Normalize(command) {
	case agent.CommandCodex, agent.CommandCodexApp:
		return sessions.ProviderCodex
	case agent.CommandClaude:
		return sessions.ProviderClaude
	default:
		return ""
	}
}

func flowSessionSortTime(session flowstore.Session) time.Time {
	tm := session.StartedAt
	if session.LastSeenAt.After(tm) {
		tm = session.LastSeenAt
	}
	if session.EndedAt.After(tm) {
		tm = session.EndedAt
	}
	return tm
}

func flowImplementationPrompt(record flowstore.FlowRecord) string {
	var b strings.Builder
	b.WriteString("Use the wtui-flow skill for this launch.\n\n")
	b.WriteString("Implement the approved Flow plan.\n\n")
	b.WriteString("Flow instructions:\n")
	b.WriteString(record.Instructions)
	b.WriteString("\n\nApproved plan context:\n")
	b.WriteString("- Flow ID: ")
	b.WriteString(record.FlowID)
	b.WriteString("\n- Phase ID: implementation")
	b.WriteString("\n- Plan ID: ")
	b.WriteString(record.PlanID)
	b.WriteString("\n- Plan path: ")
	b.WriteString(record.PlanPath)
	b.WriteString("\n\nRead the plan, implement only the Implementation phase in the Flow worktree, verify the requested behavior, and finish by running `wtui flow phase set --flow-id \"$WTUI_FLOW_ID\" --phase-id implementation` with `completed`, `needs_attention`, or `blocked` plus the same state-root arguments described by the wtui-flow skill. Report any wtui persistence failure explicitly.")
	return b.String()
}

func (m Model) handleOpenFlowPhaseTranscript() (tea.Model, tea.Cmd) {
	record, ok := m.selectedFlow()
	if !ok {
		return m, nil
	}
	phase, ok := m.selectedFlowPhase()
	if !ok {
		return m.handleOpenFlowPlanText()
	}
	if phase.PhaseID != flowImplementationPhaseID {
		m = m.setStatus(statusOther, "Only Implementation phase transcripts open from Flow mode")
		return m, nil
	}
	session, ok := latestFlowPhaseTranscriptSession(phase)
	if !ok {
		m = m.setStatus(statusOther, "Implementation phase has no transcript")
		return m, nil
	}
	m = m.openDiff(modal.DiffSessionTranscript)
	return m, m.fetchFlowPhaseTranscript(record, phase, session)
}

func latestFlowPhaseTranscriptSession(phase flowstore.FlowPhase) (flowstore.Session, bool) {
	var best flowstore.Session
	found := false
	for _, session := range phase.Sessions {
		if strings.TrimSpace(session.Provider) == "" || strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.TranscriptPath) == "" {
			continue
		}
		if !found || flowSessionSortTime(session).After(flowSessionSortTime(best)) || (flowSessionSortTime(session).Equal(flowSessionSortTime(best)) && session.SessionID > best.SessionID) {
			best = session
			found = true
		}
	}
	return best, found
}
