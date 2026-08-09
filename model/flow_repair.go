package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

// flowRepairObstruction describes the persisted condition an untracked repair
// agent should investigate. Phase is populated when the obstruction belongs to
// one phase rather than the Flow graph or its metadata as a whole.
type flowRepairObstruction struct {
	Description string
	Phase       flowstore.FlowPhase
	HasPhase    bool
}

func phaseRepairObstruction(phase flowstore.FlowPhase, description string) flowRepairObstruction {
	return flowRepairObstruction{Description: description, Phase: phase, HasPhase: true}
}

// flowRepairObstructionForRecord is the shared repair classifier. It uses
// canonical derived status and launchability, never the record's potentially
// stale display status, so rendering and key handling agree about whether a
// repair can help.
func flowRepairObstructionForRecord(record flowstore.FlowRecord) (flowRepairObstruction, bool) {
	switch flowstore.DeriveStatus(record) {
	case flowstore.StatusCompleted, flowstore.StatusMerged, flowstore.StatusAbandoned:
		return flowRepairObstruction{}, false
	}

	ordered := flowstore.OrderedPhases(record.Phases)
	orderedRecord := record
	orderedRecord.Phases = ordered
	for i := range ordered {
		if flowPhaseCanLaunchAtIndex(orderedRecord, i) {
			return flowRepairObstruction{}, false
		}
	}
	if flowManualMergeEligible(record) {
		return flowRepairObstruction{}, false
	}

	var sessionObstruction flowRepairObstruction
	for _, phase := range ordered {
		if flowstore.PhaseSessionLaunchMismatch(phase) {
			if sessionObstruction.Description == "" {
				sessionObstruction = phaseRepairObstruction(phase, fmt.Sprintf("phase %s has session-mismatch metadata", phase.PhaseID))
			}
			continue
		}
		if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
			if sessionObstruction.Description == "" {
				sessionObstruction = phaseRepairObstruction(phase, fmt.Sprintf("phase %s has missing-session-id metadata", phase.PhaseID))
			}
			continue
		}
		if phase.Status != flowstore.PhaseRunning {
			continue
		}
		if reason, ok := flowstore.RecoverableRunningPhaseResetReason(phase); ok {
			if sessionObstruction.Description == "" {
				sessionObstruction = phaseRepairObstruction(phase, fmt.Sprintf("phase %s is recoverable from %s", phase.PhaseID, reason))
			}
			continue
		}
		if flowstore.LatestPhaseLaunchID(phase) == "" {
			if sessionObstruction.Description == "" {
				sessionObstruction = phaseRepairObstruction(phase, fmt.Sprintf("running phase %s has no launch record", phase.PhaseID))
			}
			continue
		}
		// A matching, non-ended provider session is active work. Do not launch
		// an untracked repair agent into the same Flow while it is healthy.
		return flowRepairObstruction{}, false
	}
	if sessionObstruction.Description != "" {
		return sessionObstruction, true
	}

	if flowAutoreviewMissingPRTarget(record) {
		if phase, ok := flowstore.FindPhaseByKind(record, flowstore.KindAutoreview); ok {
			return phaseRepairObstruction(phase, fmt.Sprintf("phase %s is gated by missing PR metadata", phase.PhaseID)), true
		}
		return flowRepairObstruction{Description: "Flow is gated by missing PR metadata"}, true
	}

	for _, phase := range ordered {
		switch phase.Status {
		case flowstore.PhaseBlocked, flowstore.PhaseNeedsAttention:
			return phaseRepairObstruction(phase, fmt.Sprintf("phase %s is %s", phase.PhaseID, phase.Status)), true
		}
	}

	return flowRepairObstruction{Description: "the persisted Flow graph is gated and no phase is launchable"}, true
}

func (m Model) selectedFlowRepairObstruction() (flowstore.FlowRecord, flowRepairObstruction, bool) {
	if !m.flowSurfaceVisible() {
		return flowstore.FlowRecord{}, flowRepairObstruction{}, false
	}
	record, ok := m.selectedFlow()
	if !ok || strings.TrimSpace(record.FlowID) == "" {
		return flowstore.FlowRecord{}, flowRepairObstruction{}, false
	}
	obstruction, ok := flowRepairObstructionForRecord(record)
	return record, obstruction, ok
}

func (m Model) selectedFlowRepairReady() bool {
	record, _, ok := m.selectedFlowRepairObstruction()
	return ok && !m.hasFlowEmbeddedTerminalForFlow(record.FlowID)
}

func (m Model) handleRepairSelectedFlow() (tea.Model, tea.Cmd) {
	record, obstruction, ok := m.selectedFlowRepairObstruction()
	if !ok {
		return m, nil
	}
	if !m.selectedFlowRepairReady() {
		return m.setStatus(statusOther, "Close, detach, or dismiss the existing Flow terminal before repairing this Flow"), nil
	}

	command, modelName, reasoningEffort := m.flowLaunchAgentSettings()
	switch command {
	case "":
		return m.setStatus(statusOther, "Press A to choose codex or claude before repairing a Flow"), nil
	case agent.CommandCodexApp:
		return m.setStatus(statusOther, "Flow repair requires an embedded CLI agent; press A to choose codex or claude"), nil
	case agent.CommandCodex, agent.CommandClaude:
		// Supported below.
	default:
		return m.setStatus(statusOther, fmt.Sprintf("Flow repair does not support agent %q; press A to choose codex or claude", command)), nil
	}

	repoPath := strings.TrimSpace(record.RepoPath)
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	worktreePath := strings.TrimSpace(record.WorktreePath)
	if worktreePath == "" {
		worktreePath = repoPath
	}
	if worktreePath == "" {
		return m.setStatus(statusOther, "Cannot determine launch path for this Flow repair"), nil
	}
	planPath := strings.TrimSpace(record.PlanPath)
	if record.PlanID != "" && planPath == "" {
		if m.planMarkdownPath == nil {
			return m.setStatus(statusOther, "Cannot determine linked plan path for this Flow repair"), nil
		}
		var err error
		planPath, err = m.planMarkdownPath(record.PlanID)
		if err != nil {
			return m.setStatus(statusOther, err.Error()), nil
		}
	}

	ctx := actions.AgentLaunchContext{
		Command:          command,
		LaunchID:         newLaunchID(),
		RepoPath:         repoPath,
		WorktreePath:     worktreePath,
		Branch:           record.Branch,
		Commit:           record.Commit,
		SessionStateRoot: m.sessionStateRoot,
		PlanID:           record.PlanID,
		PlanPath:         planPath,
		FlowID:           record.FlowID,
		FlowRepair:       true,
		Embedded:         true,
		Headless:         m.flowHeadless,
		Model:            modelName,
		ReasoningEffort:  reasoningEffort,
		InitialPrompt:    flowRepairPrompt(record, obstruction),
	}
	return m, func() tea.Msg { return FlowEmbeddedLaunchRequestedMsg{LaunchContext: ctx} }
}

func flowRepairPrompt(record flowstore.FlowRecord, obstruction flowRepairObstruction) string {
	autoMode := "off"
	if record.AutoMode {
		autoMode = "on"
	}
	phaseContext := ""
	if obstruction.HasPhase {
		phaseContext = fmt.Sprintf("\nRelevant phase: %s (%q), status %s", obstruction.Phase.PhaseID, obstruction.Phase.Title, flowPhaseStatusDetail(obstruction.Phase))
	}
	return fmt.Sprintf(`Repair persisted Flow %s so it is safely rerunnable or can advance. Auto mode is %s.
Obstruction: %s%s

First read the persisted record with:
approach flow read --flow-id "$APPROACH_FLOW_ID" --state-root "$APPROACH_FLOW_STATE_ROOT"

Diagnose the persisted Flow and use only structured approach flow commands such as phase reset, phase restart, phase complete/phase set, plan set, pr set, or the corresponding high-level actions. Never edit Flow artifact JSON directly, fabricate success, or weaken honest phase semantics. Use phase reset only for an eligible stale launch. Restart a blocked or needs_attention phase only if you can do that phase's work and report its terminal result honestly in this same session; never leave an untracked repair stranded as running. If safe repair is impossible, leave a truthful blocked or needs_attention state and explain why. Do not launch the next phase yourself; Approach owns that handoff.`,
		record.FlowID, autoMode, obstruction.Description, phaseContext)
}
