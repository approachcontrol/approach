package model

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// The worktree-agent refusals. Each is new against
// noLaunchableFlowPhaseStatus's no-new-strings convention because this launch
// targets no phase, so "No launchable Flow phase" would be actively false, and
// because repair's and resume's equivalents each name their own action — reusing
// one would tell the user to press the wrong key.
//
// The terminal refusal deliberately omits repair's "detach": an untracked launch
// writes no phase, so a detached slot drops the Flow's only in-process owner
// while its agent runs on. The in-flight refusal is separate from it because an
// attempt still in reading or preparing has no terminal to close.
const (
	flowWorktreeAgentTerminalStatus    = "Close or dismiss the existing Flow terminal before running autofix"
	flowWorktreeAgentInFlightStatus    = "A Flow launch is already in flight"
	flowWorktreeAgentCodexAppStatus    = "Flow autofix requires codex or claude; press A to choose one"
	flowWorktreeAgentDriftStatus       = "Flow changed; refresh and try again"
	flowWorktreeAgentLiveSessionStatus = "Flow already has a running agent session"
	flowWorktreeAgentCanceledStatus    = "Flow agent launch canceled because a repair terminal is already open for this Flow"
)

// autofixPromptForPR composes the bead's literal prompt. Nothing on this path
// may ever emit `autofix pr #0`: the eligibility gate requires a PR target, and
// the authoritative read re-checks the number before this is called.
func autofixPromptForPR(number int) string {
	return "autofix pr #" + strconv.Itoa(number)
}

// selectedFlowAutofixTarget is the U shortcut's eligibility gate. It reuses the
// m (mark merged) gate verbatim so hint parity is structural rather than
// restated, and adds exactly one condition: the Flow must have a worktree.
//
// The worktree is required rather than falling back to the repository root
// (approach-c35): the agent's cwd is the whole point of the shortcut, so a
// worktree-less Flow simply does not offer it.
func (m Model) selectedFlowAutofixTarget() (flowstore.FlowRecord, string, bool) {
	record, repoPath, ok := m.selectedManualMergeFlow()
	if !ok || strings.TrimSpace(record.WorktreePath) == "" {
		return flowstore.FlowRecord{}, "", false
	}
	return record, repoPath, true
}

// selectedFlowAutofixReady is the footer's predicate: the eligibility gate plus
// every occupancy signal admission refuses on, so the footer does not advertise
// U while the Flow is already owned. That inclusion is also what makes U and m
// diverge — occupancy and pending headless writes are in this predicate and in
// no part of m's gate, so an occupied Flow keeps m and loses U.
//
// It is load-bearing beyond the footer: handleAutofixSelectedFlowPR gates the
// tmux probe on it, which is sound only while every state it excludes is one
// admission refuses too. A display-only condition must not be added here.
//
// Four refusals stay out of it deliberately, from three different places. The
// tmux live-window probe is in the key handler because it shells out and may not
// run in a predicate the renderer evaluates; the live-session and drift refusals
// are in the read stage, the only place a fresh record and the session store
// exist; and admission's unusable-agent-command refusal (unset, or codex-app) is
// left advertised on purpose, exactly as repair leaves R advertised, because its
// wording names the key that fixes it — "press A to choose one" teaches more
// than a hint that silently disappears.
func (m Model) selectedFlowAutofixReady() bool {
	record, _, ok := m.selectedFlowAutofixTarget()
	return ok &&
		!m.flowLaunchAdmissionOccupied(record.FlowID) &&
		!m.flowHeadlessWritePending(record.FlowID)
}

// handleAutofixSelectedFlowPR binds U. The tmux probe runs here rather than in
// admission for repair's reason — it shells out, and admission must not — and
// before requestFlowLaunch rather than after, because admission reserves the
// attempt and returns the read command in one call and so cannot be followed by
// a refusal.
//
// It is gated on the footer's own predicate so an already-owned Flow neither
// forks tmux nor answers with the live-window refusal when admission has a more
// specific one to give. What survives that gate is the press that could really
// launch, which is the only one the probe exists to stop.
func (m Model) handleAutofixSelectedFlowPR() (tea.Model, tea.Cmd) {
	record, repoPath, ok := m.selectedFlowAutofixTarget()
	if !ok {
		return m, nil
	}
	if m.selectedFlowAutofixReady() && m.tmuxFlowAgentStillRunning(record, repoPath) {
		return m.setStatus(statusOther, tmuxFlowLiveWindowRefusal), nil
	}
	next, cmd, _ := m.requestFlowLaunch(flowLaunchIntent{
		Kind:             flowLaunchKindWorktreeAgent,
		FlowID:           record.FlowID,
		Origin:           m.flowLaunchOrigin(),
		FallbackRepoPath: repoPath,
	})
	return next, cmd
}

// admitWorktreeAgentFlowLaunch is this kind's half of the lifecycle's admission.
// Occupancy lives here rather than in the key handler so requestFlowLaunch stays
// the lifecycle's only entry point: reserveFlowLaunchAttempt checks the attempt
// map alone, so an admission that delegated occupancy to its caller would admit
// an occupied Flow whenever it is called directly.
//
// It re-applies the record-shaped half of the eligibility gate against the
// cached record rather than trusting the handler, and refuses that silently
// exactly as the handler does. The remaining refusals are ordered durable before
// transient, as repair's are.
func (m Model) admitWorktreeAgentFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	flowID := strings.TrimSpace(intent.FlowID)
	intent.FlowID = flowID
	if flowID == "" {
		return m, nil, false
	}
	record, ok := m.cachedFlowRecord(flowID)
	if !ok || !worktreeAgentFlowEligible(record) {
		return m, nil, false
	}
	// Repair and phase resume are named before the generic in-flight refusal so
	// the durable obstacle is the one reported. Both are lifecycle attempts now,
	// so the kind is what tells them apart from any other attempt.
	if m.flowLaunchAttemptKind(flowID) == flowLaunchKindRepair {
		return m.setStatus(statusOther, flowRepairPendingStatus), nil, false
	}
	if m.flowLaunchAttemptKind(flowID) == flowLaunchKindPhaseResume {
		return m.setStatus(statusOther, "A phase resume is already pending for this Flow"), nil, false
	}
	if m.hasFlowEmbeddedTerminalForFlow(flowID) || m.hasFlowRepairEmbeddedTerminalForFlow(flowID) {
		return m.setStatus(statusOther, flowWorktreeAgentTerminalStatus), nil, false
	}
	if m.flowLaunchAdmissionOccupied(flowID) {
		return m.setStatus(statusOther, flowWorktreeAgentInFlightStatus), nil, false
	}
	if m.flowHeadlessWritePending(flowID) {
		return m.setStatus(statusOther, flowHeadlessWritePendingStatus), nil, false
	}
	command, _, _ := m.flowLaunchAgentSettings()
	command = agent.Normalize(command)
	switch {
	case command == "":
		return m.setStatus(statusOther, flowLaunchNoAgentCommandStatus), nil, false
	case command == agent.CommandCodexApp:
		// A codex-app deep link carries neither a prompt nor approach launch
		// metadata, so it cannot run autofix at all.
		return m.setStatus(statusOther, flowWorktreeAgentCodexAppStatus), nil, false
	}
	if err := agent.Validate(command); err != nil {
		return m.setStatus(statusOther, err.Error()), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m, nil, false
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	// The attempt names no phase: this launch is Flow-scoped and phase-untracked,
	// so nothing on this path writes phase state or attaches a session to phase
	// history.
	next, reserved := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:    token,
		Kind:     intent.Kind,
		FlowID:   record.FlowID,
		Origin:   intent.Origin,
		Settings: settings,
	}, flowLaunchStateReserved)
	if !reserved {
		return m, nil, false
	}
	m = next
	next, advanced := m.transitionFlowLaunchAttempt(record.FlowID, token, flowLaunchStateReserved, flowLaunchStateReading)
	if !advanced {
		return m.releaseFlowLaunchAttempt(record.FlowID, token), nil, false
	}
	m = next
	return m, m.flowLaunchReadCmd(intent, token, settings), true
}

// worktreeAgentFlowEligible is the record-shaped half of the eligibility gate,
// the only half a stage without a Model can re-check. Surface visibility and row
// selection are deliberately not threaded into the read command.
func worktreeAgentFlowEligible(record flowstore.FlowRecord) bool {
	return strings.TrimSpace(record.FlowID) != "" &&
		flowManualMergeEligible(record) &&
		strings.TrimSpace(record.WorktreePath) != ""
}

// worktreeAgentFlowLaunchReadCmd is the authoritative read. Like resume's it
// skips Preflight entirely: that gates on a launchable phase, renders phase
// prompt templates, and resolves paths by new-launch rules, none of which apply
// to a phase-untracked Flow-scoped agent.
func worktreeAgentFlowLaunchReadCmd(seams flowLaunchSeams, intent flowLaunchIntent, token string) tea.Cmd {
	return func() tea.Msg {
		event := flowLaunchEventMsg{
			Token:  token,
			Kind:   intent.Kind,
			From:   flowLaunchStateReading,
			FlowID: intent.FlowID,
			Stage:  flowLaunchStageRead,
		}
		record, err := seams.ReadFlow(intent.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		// PR.Number > 0 is implied by HasPRTarget inside the eligibility gate;
		// it is re-asserted as belt-and-braces before any prompt is composed.
		if !worktreeAgentFlowEligible(record) || record.PR.Number <= 0 {
			event.Err = flowWorktreeAgentDriftStatus
			return event
		}
		records, err := seams.ListFlowSessions(intent.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if flowRecordHasLivePhaseSession(record, records) {
			event.Err = flowWorktreeAgentLiveSessionStatus
			return event
		}
		repoPath := record.RepoPath
		if repoPath == "" {
			repoPath = intent.FallbackRepoPath
		}
		event.Record = record
		event.Headless = record.Headless
		event.RepoPath = repoPath
		event.WorktreePath = record.WorktreePath
		// The record's plan path verbatim, with no PlanMarkdownPath resolution:
		// this agent needs no plan body, so a plan-linked Flow launching with an
		// empty PlanPath here is expected. Resume does the same.
		event.PlanPath = record.PlanPath
		return event
	}
}

// flowRecordHasLivePhaseSession applies the phase-scoped live-session rule
// across every non-terminal phase of the record. It stays phase-scoped, and
// skips terminal phases, for the same reason flowLaunchPhaseSessionOccupied's
// own doc comment gives: a wider rule would let one crashed agent make the Flow
// permanently unlaunchable, and here that would be worse than elsewhere —
// repair reports no obstruction for a merge-eligible Flow, so U would be dead
// with no in-TUI recovery. A merge-eligible Flow has every predecessor
// completed, so counting terminal phases would be exactly that wider rule.
func flowRecordHasLivePhaseSession(record flowstore.FlowRecord, records []sessions.SessionRecord) bool {
	for _, phase := range record.Phases {
		if flowstore.PhaseStatusTerminal(phase.Status) {
			continue
		}
		if flowLaunchPhaseSessionOccupied(phase, records) {
			return true
		}
	}
	return false
}

// worktreeAgentFlowLaunchPrepareCmd takes the cross-process launch/close
// reservation and builds the launch context. It never calls AddPhaseLaunchID:
// the launch is untracked, so it writes no phase state at all, and the closed-
// Flow race is caught authoritatively by the reservation under the launch/close
// lock.
//
// The prompt is composed here from the event's record — the same record the read
// stage validated and passed through unmodified — so prepare cannot compose a
// prompt the read did not authorize.
func (m Model) worktreeAgentFlowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	reserve := m.reserveTrackedFlowLaunch
	ctx := actions.AgentLaunchContext{
		Command: settings.Command,
		// The admission token, never a fresh ID: every LaunchID-keyed fence and
		// the tmux window registry key on it.
		LaunchID:     msg.Token,
		RepoPath:     msg.RepoPath,
		WorktreePath: msg.WorktreePath,
		// WorkingDir is what actions turns into the agent's cwd, and it is the
		// worktree by construction: the gate refuses a worktree-less Flow.
		WorkingDir:       msg.WorktreePath,
		Branch:           msg.Record.Branch,
		Commit:           msg.Record.Commit,
		Model:            settings.Model,
		ReasoningEffort:  settings.ReasoningEffort,
		SessionStateRoot: settings.SessionStateRoot,
		PlanID:           msg.Record.PlanID,
		PlanPath:         msg.PlanPath,
		FlowID:           msg.Record.FlowID,
		// No FlowPhaseID, no FlowLaunchTracked, no FlowRepair: this is the
		// generic Flow-worktree agent, and FlowAgent is the explicit signal the
		// prefill boundary reads rather than inferring it from their absence.
		FlowAgent:     true,
		Embedded:      true,
		Headless:      msg.Headless,
		InitialPrompt: autofixPromptForPR(msg.Record.PR.Number),
	}
	// Taken on the Model before the closure runs, as resume does: the route this
	// launch takes must be the one admission snapshotted.
	tmuxRoute, tmuxFellBack := m.tmuxLaunchRoute(ctx)
	return func() tea.Msg {
		event := msg
		event.Stage = flowLaunchStagePrepared
		event.From = flowLaunchStatePreparing
		_, release, reserveErr := reserve(msg.FlowID)
		if reserveErr != nil {
			event.Err = reserveErr.Error()
			return event
		}
		// Handing the release to the event is what lets the handler drop the
		// advisory launch/close lock; losing it would block every later launch,
		// close, repair, or resume on this Flow until it timed out.
		event.Release = release
		event.Context = ctx
		event.Route = flowLaunchRouteEmbedded
		if tmuxRoute {
			// A tmux window has no dock to prefill and renders its own output,
			// so clearing Embedded is what sends the prompt to argv instead.
			event.Context.Embedded = false
			event.Route = flowLaunchRouteTmux
		} else if tmuxFellBack {
			event.FallbackNote = tmuxFallbackNote
		}
		return event
	}
}
