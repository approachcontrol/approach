package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowownership"
	"github.com/approachcontrol/approach/flowstore"
)

// The autofix refusals. Each is new against
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
	flowAutofixTerminalStatus    = "Close or dismiss the existing Flow terminal before running autofix"
	flowAutofixInFlightStatus    = "A Flow launch is already in flight"
	flowAutofixDriftStatus       = "Flow changed; refresh and try again"
	flowAutofixLiveSessionStatus = "Flow already has a running agent session"
	flowAutofixCanceledStatus    = "Flow agent launch canceled because a repair terminal is already open for this Flow"
)

// defaultAutofixPromptTemplate is the built-in U prompt. The `{pr_number}`
// placeholder is what keeps the bead's literal `autofix pr #<num>` text when
// no `[flow_prompts].autofix` override is set. Nothing on this path may ever
// emit `autofix pr #0`: the eligibility gate requires a PR target, and the
// authoritative read re-checks the number before this is called.
const defaultAutofixPromptTemplate = "autofix pr #{pr_number}"

// autofixPromptForPR composes the built-in prompt for a known PR number.
func autofixPromptForPR(number int) string {
	return autofixPrompt(flowstore.FlowRecord{
		PR: flowstore.PullRequest{Number: number},
	}, FlowPromptTemplates{}, "")
}

// autofixPrompt renders the U launch prompt. A blank or whitespace-only
// `[flow_prompts].autofix` override falls back to the built-in template.
// Unlike phase prompts, this path never appends the phase-done instruction:
// the launch is phase-untracked.
func autofixPrompt(record flowstore.FlowRecord, templates FlowPromptTemplates, binary string) string {
	template := templates.Autofix
	if strings.TrimSpace(template) == "" {
		template = defaultAutofixPromptTemplate
	}
	return renderFlowPromptTemplate(template, record, flowstore.FlowPhase{}, record.PlanPath, "", flowPromptBinary(binary))
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
	if !ok || strings.TrimSpace(record.WorktreePath) == "" || flowstore.PreparationLaunchBlocked(record) {
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
// exist; and admission's unusable-agent-command refusal is left advertised on
// purpose, exactly as repair leaves R advertised, because its
// wording names the key that fixes it — "press A to choose one" teaches more
// than a hint that silently disappears.
func (m Model) selectedFlowAutofixReady() bool {
	record, _, ok := m.selectedFlowAutofixTarget()
	return ok && !m.autofixFooterAdvice(record.FlowID).Defer()
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
		Kind:             flowLaunchKindAutofix,
		FlowID:           record.FlowID,
		Origin:           m.flowLaunchOrigin(),
		FallbackRepoPath: repoPath,
	})
	return next, cmd
}

// admitAutofixFlowLaunch is this kind's half of the lifecycle's admission.
// Occupancy lives here rather than in the key handler so requestFlowLaunch stays
// the lifecycle's only entry point: reserveFlowLaunchAttempt checks the attempt
// map alone, so an admission that delegated occupancy to its caller would admit
// an occupied Flow whenever it is called directly.
//
// It re-applies the record-shaped half of the eligibility gate against the
// cached record rather than trusting the handler, and refuses that silently
// exactly as the handler does. The remaining refusals are ordered durable before
// transient, as repair's are.
func (m Model) admitAutofixFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	flowID := strings.TrimSpace(intent.FlowID)
	intent.FlowID = flowID
	if flowID == "" {
		return m, nil, false
	}
	record, ok := m.cachedFlowRecord(flowID)
	if !ok || !autofixFlowEligible(record) {
		return m, nil, false
	}
	advice := m.autofixFooterAdvice(flowID)
	switch advice.Holder() {
	case flowownership.HolderLeaseUnreadable:
		return m.setStatus(statusOther, flowLeaseSetupErrorStatus(advice.Err())), nil, false
	case flowownership.HolderPeerLease:
		return m.setStatus(statusOther, flowLeaseOccupiedStatus), nil, false
	case flowownership.HolderRepairAttempt:
		return m.setStatus(statusOther, flowRepairPendingStatus), nil, false
	case flowownership.HolderPhaseResumeAttempt:
		return m.setStatus(statusOther, "A phase resume is already pending for this Flow"), nil, false
	case flowownership.HolderFlowTerminal, flowownership.HolderRepairTerminal:
		return m.setStatus(statusOther, flowAutofixTerminalStatus), nil, false
	case flowownership.HolderPhaseAttempt, flowownership.HolderOtherAttempt:
		return m.setStatus(statusOther, flowAutofixInFlightStatus), nil, false
	case flowownership.HolderHeadlessWrite:
		return m.setStatus(statusOther, flowHeadlessWritePendingStatus), nil, false
	}
	command, _, _ := m.flowLaunchAgentSettings()
	command = agent.Normalize(command)
	if command == "" {
		return m.setStatus(statusOther, flowLaunchNoAgentCommandStatus), nil, false
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

// autofixFlowEligible is the record-shaped half of the eligibility gate,
// the only half a stage without a Model can re-check. Surface visibility and row
// selection are deliberately not threaded into the read command.
func autofixFlowEligible(record flowstore.FlowRecord) bool {
	return strings.TrimSpace(record.FlowID) != "" &&
		flowManualMergeEligible(record) &&
		strings.TrimSpace(record.WorktreePath) != "" &&
		!flowstore.PreparationLaunchBlocked(record)
}

// autofixFlowLaunchReadCmd is the authoritative read. Like resume's it
// skips Preflight entirely: that gates on a launchable phase, renders phase
// prompt templates, and resolves paths by new-launch rules, none of which apply
// to a phase-untracked Flow-scoped agent.
func autofixFlowLaunchReadCmd(seams flowLaunchSeams, intent flowLaunchIntent, token string) tea.Cmd {
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
		if !autofixFlowEligible(record) || record.PR.Number <= 0 {
			event.Err = flowAutofixDriftStatus
			return event
		}
		if verdict := flowAuthoritativeOccupancy(seams, intent.FlowID, record, actions.RoleAutofix); verdict.Occupied() {
			event.Err = autofixAuthoritativeOccupancyStatus(intent.FlowID, record, verdict)
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

// autofixAuthoritativeOccupancyStatus keeps autofix's existing refusal text in
// model. The module applies the phase-scoped live-session rule across every
// non-terminal phase of the record. It stays phase-scoped, and
// skips terminal phases, for the same reason flowLaunchPhaseSessionOccupied's
// own doc comment gives: a wider rule would let one crashed agent make the Flow
// permanently unlaunchable, and here that would be worse than elsewhere —
// repair reports no obstruction for a merge-eligible Flow, so U would be dead
// with no in-TUI recovery. A merge-eligible Flow has every predecessor
// completed, so counting terminal phases would be exactly that wider rule.
func autofixAuthoritativeOccupancyStatus(flowID string, record flowstore.FlowRecord, verdict flowownership.Verdict) string {
	if strings.TrimSpace(record.FlowID) != flowID {
		return flowAutofixDriftStatus
	}
	if verdict.Err() != nil {
		return verdict.Err().Error()
	}
	if verdict.Holder() == flowownership.HolderUntrackedOwner {
		return flowAutofixInFlightStatus
	}
	if verdict.Holder() == flowownership.HolderPhaseSession {
		return flowAutofixLiveSessionStatus
	}
	return flowAutofixDriftStatus
}

// autofixFlowLaunchPrepareCmd takes the cross-process launch/close
// reservation and builds the launch context. It never calls AddPhaseLaunchID:
// the launch is untracked, so it writes no phase state at all, and the closed-
// Flow race is caught authoritatively by the reservation under the launch/close
// lock.
//
// Because it writes nothing, the reservation's own record is the only fresh
// state this launch ever sees, and it is the last read of the Flow before the
// spawn. Every tracked kind resolves the persisted headless preference from the
// record its write returns (flowLaunchPreparation.prepare); this kind resolves it —
// and re-checks eligibility — from the reserved record for exactly that reason.
// Without it a headless toggle landing between the read stage and this
// reservation would launch in the previous mode, and on the wrong route with it,
// since a headless launch is never tmux-eligible.
//
// The prompt is rendered by the builder from the reserved record and the
// snapshotted `[flow_prompts].autofix` template, so prepare still cannot compose
// a prompt no stage authorized. Headless and tmux send that text on argv;
// interactive embedded launches prefill the dock with it.
func (m Model) autofixFlowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	reserve := m.reserveTrackedFlowLaunch
	// Snapshotted at admission, as flowLaunchPreparation snapshots them, so the route
	// is still decided against what the attempt was admitted with. Only the
	// decision moves into the closure, where Headless is finally resolved.
	backend := m.launchBackend
	tmuxAvailable := m.tmuxLaunchAvailable
	return func() tea.Msg {
		event := msg
		event.Stage = flowLaunchStagePrepared
		event.From = flowLaunchStatePreparing
		// Ahead of the reservation, for the same reason the phase preflight puts
		// it ahead of prepare: a refusal must not leave a reservation or a
		// half-started launch behind.
		if refusal := refuseUnverifiedLaunchPin(settings.Pin); refusal != "" {
			event.Err = refusal
			return event
		}
		reserved, release, reserveErr := reserve(msg.FlowID)
		if reserveErr != nil {
			event.Err = reserveErr.Error()
			return event
		}
		// Handing the release to the event is what lets the handler drop the
		// advisory launch/close lock; losing it would block every later launch,
		// close, repair, or resume on this Flow until it timed out.
		event.Release = release
		record := msg.Record
		headless := msg.Headless
		repoPath := msg.RepoPath
		// A zero UpdatedAt means no store answered — an injected persistence seam,
		// or none at all — so it cannot authoritatively replace what the read
		// stage validated. flowLaunchPreparation.prepare guards its own refresh the
		// same way.
		if !reserved.UpdatedAt.IsZero() {
			record = reserved
			headless = reserved.Headless
			if reservedRepoPath := strings.TrimSpace(reserved.RepoPath); reservedRepoPath != "" {
				repoPath = reservedRepoPath
			}
			event.Record = record
			event.Headless = headless
			event.RepoPath = repoPath
			event.WorktreePath = record.WorktreePath
			// The record's plan path verbatim, exactly as the read stage passes it
			// through: this agent needs no plan body.
			event.PlanPath = record.PlanPath
		}
		verdict := m.flowReservedOccupancy(m.launchSeams, msg.FlowID, record, actions.RoleAutofix)
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
			event.Err = autofixAuthoritativeOccupancyStatus(msg.FlowID, record, verdict)
			return event
		}
		if !autofixFlowEligible(record) || record.PR.Number <= 0 {
			event.Err = flowAutofixDriftStatus
			return event
		}
		// The read stage's repo path is submitted as a fallback rather than
		// applied here: the builder owns the precedence between it and the record,
		// and it is the builder that sets autofix's markers and decides the route
		// from the headless preference resolved just above.
		ctx, decision, err := newFlowLaunchContext(autofixTarget{
			LaunchID:         msg.Token,
			Record:           record,
			PlanPath:         event.PlanPath,
			FallbackRepoPath: repoPath,
		}, settings, flowLaunchRouting{Backend: backend, TmuxAvailable: tmuxAvailable})
		if err != nil {
			event.Err = err.Error()
			return event
		}
		event.Context = ctx
		event.Route = decision.Route
		event.FallbackNote = decision.FallbackNote
		if err := claimUntrackedOwner(m.launchSeams, msg.FlowID, msg.Token, msg.Kind); err != nil {
			event.Err = err.Error()
			return event
		}
		event.UntrackedOwnerClaimed = true
		return event
	}
}
