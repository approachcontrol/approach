package model

import (
	"fmt"
	"os"
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
	case flowstore.StatusCompleted, flowstore.StatusMerged, flowstore.StatusAbandoned, flowstore.StatusClosed:
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
		sessionMismatch := flowstore.PhaseSessionLaunchMismatch(phase)
		if sessionMismatch {
			if sessionObstruction.Description == "" {
				sessionObstruction = phaseRepairObstruction(phase, fmt.Sprintf("phase %s has session-mismatch metadata", phase.PhaseID))
			}
		}
		if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
			if sessionObstruction.Description == "" {
				sessionObstruction = phaseRepairObstruction(phase, fmt.Sprintf("phase %s has missing-session-id metadata", phase.PhaseID))
			}
		}
		if phaseHasMatchingLiveSession(phase) {
			// Phase status can be changed before the provider session ends.
			// Treat any matched non-ended session as active work even when the
			// persisted phase already says blocked or needs_attention.
			return flowRepairObstruction{}, false
		}
		if sessionMismatch {
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
		// Matching live sessions were rejected above. Any remaining running
		// metadata is malformed or otherwise inconsistent and is repairable.
		continue
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

func phaseHasMatchingLiveSession(phase flowstore.FlowPhase) bool {
	return phaseHasMatchingLiveSessionExcept(phase, flowSessionIdentity{})
}

// phaseHasMatchingLiveSessionExcept is the same rule with one session exempted.
// See flowLaunchPhaseSessionOccupiedExcept for why resume is the only caller
// that passes an identity, and why the exemption is keyed on provider and ID
// together; every other call site uses the zero-skip wrapper and is unchanged.
func phaseHasMatchingLiveSessionExcept(phase flowstore.FlowPhase, skip flowSessionIdentity) bool {
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID = strings.TrimSpace(launchID); launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, session := range phase.Sessions {
		if strings.TrimSpace(session.SessionID) == "" || skip.matches(session.Provider, session.SessionID) {
			continue
		}
		if _, ok := launches[strings.TrimSpace(session.LaunchID)]; !ok {
			continue
		}
		if flowSessionLive(session.Status, session.EndedAt) {
			return true
		}
	}
	return false
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
	return ok &&
		!m.hasFlowEmbeddedTerminalForFlow(record.FlowID) &&
		!m.hasPendingFlowRepairLaunch(record.FlowID) &&
		// A repair must not arm while the launch lifecycle holds this Flow.
		!m.flowLaunchAttemptOccupied(record.FlowID) &&
		// Repair refuses while a headless write is in flight, so the footer
		// must stop advertising it for as long as one is.
		!m.flowHeadlessWritePending(record.FlowID)
}

func (m Model) handleRepairSelectedFlow() (tea.Model, tea.Cmd) {
	record, obstruction, ok := m.selectedFlowRepairObstruction()
	if !ok {
		return m, nil
	}
	// Every unready reason is named explicitly, ordered so the durable
	// obstacles come before the transient one: a headless write clears on its
	// own, an open terminal does not.
	if !m.selectedFlowRepairReady() {
		if m.hasPendingFlowRepairLaunch(record.FlowID) {
			return m.setStatus(statusOther, "A repair launch is already pending for this Flow"), nil
		}
		if m.flowLaunchAttemptKind(record.FlowID) == flowLaunchKindPhaseResume {
			return m.setStatus(statusOther, "A phase resume is already pending for this Flow"), nil
		}
		if m.hasFlowEmbeddedTerminalForFlow(record.FlowID) || m.flowLaunchAttemptOccupied(record.FlowID) {
			return m.setStatus(statusOther, "Close, detach, or dismiss the existing Flow terminal before repairing this Flow"), nil
		}
		// Repair reads the persisted headless preference asynchronously, so it
		// must wait for an in-flight toggle exactly as a phase launch does.
		return m.setStatus(statusOther, flowHeadlessWritePendingStatus), nil
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

	currentRepoPath, _ := m.currentRepoPath()
	// Repair's live-agent fence is hasFlowEmbeddedTerminalForFlow, and in tmux
	// mode that slot does not exist: a phase running in a tmux window records no
	// session until the provider hook fires, so it reads as recoverable and arms
	// repair while its agent is mid-run. Without this, R would put an untracked
	// second agent in the same worktree and instruct it to rewrite the running
	// phase's persisted state. Probed here rather than in selectedFlowRepairReady
	// for the same reason as reset and resume: that predicate feeds the footer.
	if m.tmuxFlowAgentStillRunning(record, currentRepoPath) {
		return m.setStatus(statusOther, tmuxFlowLiveWindowRefusal), nil
	}

	repoPath, worktreePath, pathOK := flowRepairLaunchPaths(record.RepoPath, record.WorktreePath, currentRepoPath)
	if !pathOK {
		return m.setStatus(statusOther, "Cannot find a usable worktree or repository directory for this Flow repair"), nil
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
		Headless:         record.Headless,
		Model:            modelName,
		ReasoningEffort:  reasoningEffort,
		InitialPrompt:    flowRepairPrompt(record, obstruction),
	}
	m = m.withPendingFlowRepairLaunch(record.FlowID, ctx.LaunchID)
	reserveRepairLaunch := m.reserveFlowRepairLaunch
	return m, func() tea.Msg {
		msg := FlowEmbeddedLaunchRequestedMsg{LaunchContext: ctx}
		current, release, err := reserveRepairLaunch(ctx.FlowID)
		if err != nil {
			msg.RepairValidationErr = "Reserve persisted Flow for repair: " + err.Error()
			return msg
		}
		msg.RepairRecord = current
		msg.RepairRelease = release
		return msg
	}
}

func (m Model) hasPendingFlowRepairLaunch(flowID string) bool {
	flowID = strings.TrimSpace(flowID)
	return flowID != "" && strings.TrimSpace(m.pendingFlowRepairLaunchIDs[flowID]) != ""
}

func (m Model) withPendingFlowRepairLaunch(flowID, launchID string) Model {
	flowID = strings.TrimSpace(flowID)
	launchID = strings.TrimSpace(launchID)
	if flowID == "" || launchID == "" {
		return m
	}
	pending := make(map[string]string, len(m.pendingFlowRepairLaunchIDs)+1)
	for existingFlowID, existingLaunchID := range m.pendingFlowRepairLaunchIDs {
		pending[existingFlowID] = existingLaunchID
	}
	pending[flowID] = launchID
	m.pendingFlowRepairLaunchIDs = pending
	return m
}

func (m Model) withoutPendingFlowRepairLaunch(flowID string) Model {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return m
	}
	if _, ok := m.pendingFlowRepairLaunchIDs[flowID]; !ok {
		return m
	}
	pending := make(map[string]string, len(m.pendingFlowRepairLaunchIDs)-1)
	for existingFlowID, launchID := range m.pendingFlowRepairLaunchIDs {
		if existingFlowID != flowID {
			pending[existingFlowID] = launchID
		}
	}
	if len(pending) == 0 {
		pending = nil
	}
	m.pendingFlowRepairLaunchIDs = pending
	return m
}

func (m Model) flowRecordForRepairLaunch(flowID string) (flowstore.FlowRecord, bool) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return flowstore.FlowRecord{}, false
	}
	var found flowstore.FlowRecord
	ok := false
	consider := func(records []flowstore.FlowRecord) {
		for _, record := range records {
			if strings.TrimSpace(record.FlowID) != flowID {
				continue
			}
			if !ok || !record.UpdatedAt.Before(found.UpdatedAt) {
				found = record
				ok = true
			}
		}
	}
	consider(m.autoAdvanceSnapshot)
	consider(m.activeFlowRecords)
	consider(m.flows.Items())
	consider(m.activeFlows.Items())
	return found, ok
}

func (m Model) consumePendingFlowRepairLaunch(ctx actions.AgentLaunchContext, authoritative *flowstore.FlowRecord, validationErr string) (Model, bool) {
	flowID := strings.TrimSpace(ctx.FlowID)
	launchID := strings.TrimSpace(ctx.LaunchID)
	if flowID == "" || launchID == "" || m.pendingFlowRepairLaunchIDs[flowID] != launchID {
		return m.setStatus(statusOther, "Flow repair request is no longer current"), false
	}
	m = m.withoutPendingFlowRepairLaunch(flowID)
	if validationErr != "" {
		return m.setStatus(statusOther, validationErr), false
	}
	var record flowstore.FlowRecord
	ok := false
	if authoritative != nil {
		record = *authoritative
		ok = strings.TrimSpace(record.FlowID) == flowID
	} else {
		record, ok = m.flowRecordForRepairLaunch(flowID)
	}
	if !ok {
		return m.setStatus(statusOther, "Flow repair request is stale; refresh and try again"), false
	}
	if _, repairable := flowRepairObstructionForRecord(record); !repairable {
		return m.setStatus(statusOther, "Flow is no longer repairable"), false
	}
	if m.flowLaunchAttemptKind(flowID) == flowLaunchKindPhaseResume {
		return m.setStatus(statusOther, "A phase resume is already pending for this Flow"), false
	}
	if m.hasFlowEmbeddedTerminalForFlow(flowID) {
		return m.setStatus(statusOther, "Close, detach, or dismiss the existing Flow terminal before repairing this Flow"), false
	}
	return m, true
}

func refreshFlowRepairLaunchContext(ctx actions.AgentLaunchContext, record flowstore.FlowRecord) actions.AgentLaunchContext {
	repoPath, worktreePath, ok := flowRepairLaunchPaths(record.RepoPath, record.WorktreePath, ctx.WorktreePath, ctx.RepoPath)
	if !ok {
		repoPath = ctx.RepoPath
		worktreePath = ctx.WorktreePath
	}
	planPath := strings.TrimSpace(record.PlanPath)
	if planPath == "" && record.PlanID == ctx.PlanID {
		planPath = ctx.PlanPath
	}
	obstruction, _ := flowRepairObstructionForRecord(record)
	ctx.RepoPath = repoPath
	ctx.WorktreePath = worktreePath
	ctx.Branch = record.Branch
	ctx.Commit = record.Commit
	ctx.PlanID = record.PlanID
	ctx.PlanPath = planPath
	ctx.Headless = record.Headless
	ctx.InitialPrompt = flowRepairPrompt(record, obstruction)
	return ctx
}

func flowRepairLaunchPaths(repoPath, worktreePath string, fallbacks ...string) (string, string, bool) {
	repoPath = strings.TrimSpace(repoPath)
	worktreePath = strings.TrimSpace(worktreePath)
	if flowRepairDirectoryUsable(worktreePath) {
		return repoPath, worktreePath, true
	}
	candidates := append([]string{repoPath}, fallbacks...)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !flowRepairDirectoryUsable(candidate) {
			continue
		}
		if !flowRepairDirectoryUsable(repoPath) {
			repoPath = candidate
		}
		return repoPath, candidate, true
	}
	return repoPath, worktreePath, false
}

func flowRepairDirectoryUsable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
