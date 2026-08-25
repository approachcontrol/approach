package model

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/flowownership"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
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
	if flowstore.PreparationLaunchBlocked(record) {
		return flowRepairObstruction{}, false
	}
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
		if phase, ok := flowstore.FindPhaseByKind(record, string(flowstore.KindAutoreview)); ok {
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
		if sessions.IsActive(session.Status, session.EndedAt) {
			return true
		}
	}
	return false
}

// selectedFlowRepairReady is the footer's answer. StageFooter is the preview
// policy plus the transient headless-write holder, so evaluating it once avoids
// a second filesystem-backed lease inspection per render. The intent has a
// blank Flow ID because ui.FlowRepairReady takes no arguments: cachedFlowRecord
// falls back to selectedFlow(), preserving selection semantics.
func (m Model) selectedFlowRepairReady() bool {
	record, ok := m.cachedRepairTarget(m.repairFlowLaunchIntent(""))
	return ok && !m.repairOccupancy(record.FlowID, flowownership.StageFooter).Occupied()
}

// handleRepairSelectedFlow is submit-only. Every refusal, path resolution, and
// context build now belongs to the lifecycle.
func (m Model) handleRepairSelectedFlow() (tea.Model, tea.Cmd) {
	intent := m.repairFlowLaunchIntent("")
	// The one refusal that cannot move into admission. Repair's live-agent fence
	// is hasFlowEmbeddedTerminalForFlow, and in tmux mode that slot does not
	// exist: a phase running in a tmux window records no session until the
	// provider hook fires, so it reads as recoverable and arms repair while its
	// agent is mid-run. Without this, R would put an untracked second agent in
	// the same worktree and instruct it to rewrite the running phase's persisted
	// state. It stays on the keystroke because it runs a tmux subprocess, and
	// admission's preview is what the footer evaluates every frame.
	//
	// previewRepairLaunch, not cachedRepairTarget: an occupied Flow is refused by
	// admission with the name of what holds it, and probing first would answer a
	// different question with a worse message.
	if record, ok := m.previewRepairLaunch(intent); ok && m.tmuxFlowAgentStillRunning(record, intent.FallbackRepoPath) {
		return m.setStatus(statusOther, tmuxFlowLiveWindowRefusal), nil
	}
	next, cmd, _ := m.requestFlowLaunch(intent)
	return next, cmd
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

func flowRepairPrompt(record flowstore.FlowRecord, obstruction flowRepairObstruction, binary string) string {
	autoMode := "off"
	if record.AutoMode {
		autoMode = "on"
	}
	bin := flowPromptBinary(binary)
	phaseContext := ""
	if obstruction.HasPhase {
		phaseContext = fmt.Sprintf("\nRelevant phase: %s (%q), status %s", obstruction.Phase.PhaseID, obstruction.Phase.Title, flowPhaseStatusDetail(obstruction.Phase))
	}
	return fmt.Sprintf(`Repair persisted Flow %s so it is safely rerunnable or can advance. Auto mode is %s.
Obstruction: %s%s

First read the persisted record with:
%s flow read --flow-id "$APPROACH_FLOW_ID" --state-root "$APPROACH_FLOW_STATE_ROOT"

Diagnose the persisted Flow and use only structured %s flow commands such as phase reset, phase restart, phase complete/phase set, plan set, pr set, or the corresponding high-level actions. Never edit Flow artifact JSON directly, fabricate success, or weaken honest phase semantics. Use phase reset only for an eligible stale launch. Restart a blocked or needs_attention phase only if you can do that phase's work and report its terminal result honestly in this same session; never leave an untracked repair stranded as running. If safe repair is impossible, leave a truthful blocked or needs_attention state and explain why. Do not launch the next phase yourself; Approach owns that handoff.`,
		record.FlowID, autoMode, obstruction.Description, phaseContext, bin, bin)
}
