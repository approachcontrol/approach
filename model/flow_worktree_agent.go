package model

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

type flowWorktreeAgentPreflightMsg struct {
	FlowID   string
	Token    string
	Command  string
	Flow     flowstore.FlowRecord
	Sessions []sessions.SessionRecord
	Err      error
}

func (m Model) selectedFlowWorktreeAgentReady() bool {
	if !m.flowSurfaceVisible() {
		return false
	}
	command := agent.Normalize(m.agentCommand)
	if command != agent.CommandCodex && command != agent.CommandClaude {
		return false
	}
	record, ok := m.selectedFlow()
	if !ok || strings.TrimSpace(record.FlowID) == "" || !flowWorktreeDirectoryUsable(record.WorktreePath) {
		return false
	}
	if _, occupied := m.flowLaunchLease(record.FlowID); occupied || m.hasFlowEmbeddedTerminalForFlow(record.FlowID) {
		return false
	}
	if m.hasKnownActiveFlowSession(record.FlowID) {
		return false
	}
	for _, phase := range record.Phases {
		if phase.Status == flowstore.PhaseRunning || phaseHasMatchingLiveSession(phase) {
			return false
		}
	}
	return true
}

func (m Model) hasKnownActiveFlowSession(flowID string) bool {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return false
	}
	for _, records := range [][]sessions.SessionRecord{m.sessions.Items(), m.worktreeSessions.Items()} {
		for _, record := range records {
			if strings.TrimSpace(record.FlowID) == flowID && sessions.IsActive(record) {
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
	if !ok || strings.TrimSpace(record.FlowID) == "" {
		return m, nil
	}
	command := agent.Normalize(m.agentCommand)
	switch command {
	case "":
		return m.setStatus(statusOther, "Press A to choose codex or claude before starting a Flow worktree agent"), nil
	case agent.CommandCodexApp:
		return m.setStatus(statusOther, "Flow worktree agents require an embedded CLI; press A to choose codex or claude"), nil
	case agent.CommandCodex, agent.CommandClaude:
	default:
		return m.setStatus(statusOther, fmt.Sprintf("Flow worktree agents do not support agent %q; press A to choose codex or claude", command)), nil
	}
	if !flowWorktreeDirectoryUsable(record.WorktreePath) {
		return m.setStatus(statusOther, "Flow worktree agent requires the Flow's exact existing worktree directory"), nil
	}
	token := newLaunchID()
	var acquired bool
	m, acquired = m.acquireFlowLaunchLease(record.FlowID, token, flowLaunchSourceWorktreeAgent)
	if !acquired {
		return m.setStatus(statusOther, "Another launch or session is already pending for this Flow"), nil
	}
	flowID := strings.TrimSpace(record.FlowID)
	listFlows := m.listFlows
	listSessions := m.listSessions
	return m, func() tea.Msg {
		msg := flowWorktreeAgentPreflightMsg{FlowID: flowID, Token: token, Command: command}
		var err error
		msg.Sessions, err = listSessions(sessions.SessionFilter{FlowID: flowID})
		if err != nil {
			msg.Err = fmt.Errorf("list sessions for Flow %s: %w", flowID, err)
			return msg
		}
		flows, err := listFlows(flowstore.FlowFilter{})
		if err != nil {
			msg.Err = fmt.Errorf("refresh Flow before starting agent: %w", err)
			return msg
		}
		found := false
		for _, current := range flows {
			if strings.TrimSpace(current.FlowID) == flowID {
				msg.Flow = current
				found = true
				break
			}
		}
		if !found {
			msg.Err = fmt.Errorf("Flow %s no longer exists", flowID)
			return msg
		}
		return msg
	}
}

func (m Model) handleFlowWorktreeAgentPreflight(msg flowWorktreeAgentPreflightMsg) (Model, tea.Cmd) {
	lease, current := m.flowLaunchLease(msg.FlowID)
	if !current || lease.Token != strings.TrimSpace(msg.Token) || lease.Source != flowLaunchSourceWorktreeAgent {
		return m, nil
	}
	fail := func(text string) (Model, tea.Cmd) {
		m = m.releaseFlowLaunchLease(msg.FlowID, msg.Token)
		return m.setStatus(statusOther, text), nil
	}
	if msg.Err != nil {
		return fail(msg.Err.Error())
	}
	command, modelName, reasoningEffort := m.flowLaunchAgentSettings()
	if command != msg.Command || (command != agent.CommandCodex && command != agent.CommandClaude) {
		return fail("Configured agent changed while the Flow worktree launch was pending; try again")
	}
	if strings.TrimSpace(msg.Flow.FlowID) != strings.TrimSpace(msg.FlowID) {
		return fail("Flow worktree launch is stale; refresh and try again")
	}
	if !flowWorktreeDirectoryUsable(msg.Flow.WorktreePath) {
		return fail("Flow worktree agent requires the Flow's exact existing worktree directory")
	}
	for _, record := range msg.Sessions {
		if sessions.IsActive(record) {
			return fail("An active persisted session already occupies this Flow")
		}
	}
	for _, phase := range msg.Flow.Phases {
		if phase.Status == flowstore.PhaseRunning {
			return fail("A running phase already occupies this Flow")
		}
	}
	if m.hasFlowEmbeddedTerminalForFlow(msg.FlowID) {
		return fail("An embedded terminal already occupies this Flow")
	}
	planPath := strings.TrimSpace(msg.Flow.PlanPath)
	if msg.Flow.PlanID != "" && planPath == "" {
		if m.planMarkdownPath == nil {
			return fail("Cannot determine linked plan path for this Flow")
		}
		var err error
		planPath, err = m.planMarkdownPath(msg.Flow.PlanID)
		if err != nil {
			return fail(err.Error())
		}
	}
	ctx := actions.AgentLaunchContext{
		Command:           command,
		LaunchID:          msg.Token,
		RepoPath:          msg.Flow.RepoPath,
		WorktreePath:      msg.Flow.WorktreePath,
		WorkingDir:        msg.Flow.WorktreePath,
		Branch:            msg.Flow.Branch,
		Commit:            msg.Flow.Commit,
		SessionStateRoot:  m.sessionStateRoot,
		PlanID:            msg.Flow.PlanID,
		PlanPath:          planPath,
		FlowID:            msg.FlowID,
		FlowWorktreeAgent: true,
		Embedded:          true,
		Model:             modelName,
		ReasoningEffort:   reasoningEffort,
	}
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err, prefillCmd := m.openFlowEmbeddedTerminal(ctx)
	if err != nil {
		m = next.releaseFlowLaunchLease(msg.FlowID, msg.Token)
		return m.setStatus(statusOther, err.Error()), nil
	}
	if !opened {
		m = next.releaseFlowLaunchLease(msg.FlowID, msg.Token)
		return m.setStatus(statusOther, "Maximum embedded terminals reached"), nil
	}
	next = next.releaseFlowLaunchLease(msg.FlowID, msg.Token)
	next = next.focusEmbeddedTerminalInput()
	var tickCmd tea.Cmd
	if needsTick {
		next, tickCmd = next.startEmbeddedTerminalTick()
	}
	return next, batchNonNil(prefillCmd, tickCmd)
}

func flowWorktreeDirectoryUsable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
