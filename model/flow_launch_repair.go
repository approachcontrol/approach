package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// The repair refusals. Two of these are shared for the same load-bearing
// reason: a user who presses R twice must not see one condition worded two
// ways. flowRepairNotRepairableStatus is raised by both the authoritative read
// and the post-reservation revalidation, and flowRepairTerminalStatus by both
// the admission rung below and the install-stage backstop
// (flowLaunchEmbeddedBackstop), which are the same condition observed one
// asynchronous hop apart. The rest have a single emission site each and are
// constants only so repair's refusals read in one place.
const (
	flowRepairPendingStatus       = "A repair launch is already pending for this Flow"
	flowRepairResumePendingStatus = "A phase resume is already pending for this Flow"
	flowRepairPhasePendingStatus  = "A phase launch is already pending for this Flow"
	flowRepairTerminalStatus      = "Close, detach, or dismiss the existing Flow terminal before repairing this Flow"
	flowRepairNotRepairableStatus = "Flow is no longer repairable"
	flowRepairNoDirectoryStatus   = "Cannot find a usable worktree or repository directory for this Flow repair"
	flowRepairNoPlanPathStatus    = "Cannot determine linked plan path for this Flow repair"
)

// flowRepairLiveSessionStatus names the phase whose launch the live session
// belongs to. It deliberately does not reuse flowPhaseResumeLiveSessionStatus's
// wording even though the underlying condition is the same, because the two are
// not the same situation for the user: resume exempts the session it is
// reattaching to, so a stalled phase still has a working key, while repair has
// no in-app move left at all.
//
// The remedy is therefore part of the message, and it is spelled out in full:
// `approach flow phase reset` requires both --flow-id and --phase-id and reads
// neither from the environment (cmd/approach/flow.go), so a message that named
// only one of them would hand the user a command that fails. The phase in
// particular cannot be inferred — repair's rule is Flow-scoped, so the phase
// holding the live session is usually not the one the user pressed R to unblock.
//
// The remedy is also branched on the phase's status, because reset alone is only
// correct for one of the three cases. The occupancy rule deliberately does not
// filter by status, so this message is raised for phases that have already moved
// off running, and flowstore.ResetRecoverableRunningPhase refuses every one of
// them with `flow phase reset requires running recoverable phase`.
//
// The branch for those is `phase restart`, not `phase set --status running`,
// and the two are not interchangeable here: validatePhaseUpdate rejects a bare
// set out of blocked or needs_attention with `restarting <status> phase requires
// notes`, while the restart command defaults the note, so only restart runs as
// printed. Its accepted statuses are exactly blocked and needs_attention, which
// is why this switch enumerates them instead of asking the transition table
// which statuses can reach running: that table also allows completed -> running
// and skipped -> running, and telling a user to reopen a finished phase to clear
// a stale session record would trade a real result for a session cleanup.
// Terminal and pending phases get no command at all, because none of them has
// one that both runs and is non-destructive.
//
// What this message does not try to predict is whether a legal reset will also
// be an effective one. Reset clears the phase's newest launch while occupancy
// matches every launch the phase has recorded, so a live session stranded on an
// older launch survives a reset that reported success — and reset has further
// preconditions of its own (session/launch agreement, predecessors) that live in
// flowstore. Restating those here would put a second copy of the store's
// admission rules in a status string, where they would drift. docs/tui-guide.md
// documents that residue, and this branch covers all three commands.
func flowRepairLiveSessionStatus(flowID string, phase flowstore.FlowPhase) string {
	head := fmt.Sprintf("Flow phase %s already has a running session", phase.PhaseID)
	reset := fmt.Sprintf("approach flow phase reset --flow-id %s --phase-id %s", flowID, phase.PhaseID)
	switch phase.Status {
	case flowstore.PhaseRunning:
		return fmt.Sprintf("%s; if its agent is gone, clear it with %s", head, reset)
	case flowstore.PhaseBlocked, flowstore.PhaseNeedsAttention:
		return fmt.Sprintf(
			"%s and is %s; if its agent is gone, clear it with approach flow phase restart --flow-id %s --phase-id %s, then %s",
			head, phase.Status, flowID, phase.PhaseID, reset)
	default:
		return fmt.Sprintf(
			"%s and is %s; reset needs a running phase, so this phase's session metadata has to be corrected directly",
			head, phase.Status)
	}
}

// repairFlowLaunchIntent is what the R key submits. An empty Flow ID is the
// normal case and is deliberate: cachedFlowRecord("") falls back to
// selectedFlow(), which reproduces the selection semantics the key handler had
// before the lifecycle owned it. Admission stamps the resolved ID back onto the
// intent so the read stage names one exact Flow.
//
// FallbackRepoPath is not optional. The read stage is a free function with no
// Model, and repair's path resolution has always taken the current repo as its
// last candidate; resume solved the identical problem with this same field.
// FlowTitle is deliberately left empty — only the autoPhase read reads it.
func (m Model) repairFlowLaunchIntent(flowID string) flowLaunchIntent {
	currentRepoPath, _ := m.currentRepoPath()
	return flowLaunchIntent{
		Kind:             flowLaunchKindRepair,
		FlowID:           flowID,
		Origin:           flowLaunchOriginRepair,
		FallbackRepoPath: currentRepoPath,
	}
}

// cachedRepairTarget is the launchability half of repair's preview: is there a
// repairable Flow here at all? A false answer is the silent refusal the R key
// has always had. The surface gate is load-bearing — neither cachedFlowRecord
// nor selectedFlow has one, so dropping it would arm R from panes that show no
// Flow.
//
// The classified obstruction is deliberately not returned. Nothing cached ever
// reaches the prompt: the prompt is rendered in the prepare stage from the
// record the reservation returned, so a preview-stage obstruction could only be
// a stale duplicate of it.
func (m Model) cachedRepairTarget(intent flowLaunchIntent) (flowstore.FlowRecord, bool) {
	if !m.flowSurfaceVisible() {
		return flowstore.FlowRecord{}, false
	}
	record, ok := m.cachedFlowRecord(intent.FlowID)
	if !ok || strings.TrimSpace(record.FlowID) == "" {
		return flowstore.FlowRecord{}, false
	}
	if _, repairable := flowRepairObstructionForRecord(record); !repairable {
		return flowstore.FlowRecord{}, false
	}
	return record, true
}

// previewRepairLaunch is cachedRepairTarget conjoined with occupancy, mirroring
// previewFlowLaunch over cachedFlowLaunchTarget. Admission needs the two halves
// separately so it can name whichever one is blocking; the footer only needs
// their conjunction.
func (m Model) previewRepairLaunch(intent flowLaunchIntent) (flowstore.FlowRecord, bool) {
	record, ok := m.cachedRepairTarget(intent)
	if !ok || m.flowLaunchAdmissionOccupied(record.FlowID) {
		return flowstore.FlowRecord{}, false
	}
	return record, true
}

// admitRepairFlowLaunch is repair's half of the lifecycle's admission. The
// refusal ladder below is transcribed from the key handler it replaces, in the
// same order: durable obstacles before the transient one, because a headless
// write clears on its own and an open terminal does not.
func (m Model) admitRepairFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	record, ok := m.cachedRepairTarget(intent)
	if !ok {
		// Nothing repairable is selected. Silent, exactly as before.
		return m, nil, false
	}
	flowID := strings.TrimSpace(record.FlowID)
	intent.FlowID = flowID
	leaseOccupied, leaseErr := m.trackedFlowLeaseOccupied(flowID)
	if leaseErr != nil {
		return m.setStatus(statusOther, flowLeaseSetupErrorStatus(leaseErr)), nil, false
	}
	if leaseOccupied {
		return m.setStatus(statusOther, flowLeaseOccupiedStatus), nil, false
	}
	if m.flowLaunchRuntimeOccupied(flowID) || m.flowHeadlessWritePending(flowID) {
		return m.setStatus(statusOther, m.flowRepairOccupancyRefusal(flowID)), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		// Silent, unlike admitManualFlowLaunch's noLaunchableFlowPhaseStatus on
		// the same condition: repair has no equivalent generic refusal text, and
		// the seam falls back to newLaunchID, so this is unreachable in
		// production. Do not "fix" it by borrowing manual's string — it names a
		// phase, and repair has none.
		return m, nil, false
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	// No phase is named: repair is Flow-scoped and stays phase-untracked, so
	// the attempt reserves the Flow and nothing else.
	next, reserved := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:    token,
		Kind:     intent.Kind,
		FlowID:   flowID,
		Origin:   intent.Origin,
		Settings: settings,
	}, flowLaunchStateReserved)
	if !reserved {
		return m, nil, false
	}
	m = next
	next, advanced := m.transitionFlowLaunchAttempt(flowID, token, flowLaunchStateReserved, flowLaunchStateReading)
	if !advanced {
		return m.releaseFlowLaunchAttempt(flowID, token), nil, false
	}
	m = next
	return m, m.flowLaunchReadCmd(intent, token, settings), true
}

// flowRepairOccupancyRefusal names what is holding the Flow in this exact
// order: repair attempt, phase-resume attempt, manual/auto phase attempt,
// actual terminal or other-attempt fallback, then pending headless write.
func (m Model) flowRepairOccupancyRefusal(flowID string) string {
	switch m.flowLaunchAttemptKind(flowID) {
	case flowLaunchKindRepair:
		return flowRepairPendingStatus
	case flowLaunchKindPhaseResume:
		return flowRepairResumePendingStatus
	case flowLaunchKindManualPhase, flowLaunchKindAutoPhase:
		return flowRepairPhasePendingStatus
	}
	if m.hasFlowEmbeddedTerminalForFlow(flowID) ||
		m.hasFlowRepairEmbeddedTerminalForFlow(flowID) ||
		m.flowLaunchAttemptOccupied(flowID) {
		return flowRepairTerminalStatus
	}
	// Repair reads the persisted headless preference asynchronously, so it must
	// wait for an in-flight toggle exactly as a phase launch does.
	return flowHeadlessWritePendingStatus
}

// repairFlowLaunchReadCmd is the authoritative read for a repair. It does not
// call Preflight: those new-launch rules reject exactly the gated phases repair
// exists for.
//
// The event carries no obstruction description and does not set Headless. Both
// are recomputed from the reserved record in the prepare stage, which is the
// authoritative record repair renders its prompt against; carrying them here
// would be dead payload. That diverges from the tracked kinds' resolve-headless-
// once rule, which exists to stop an AutoMode launch opening an interactive
// terminal — repair is never AutoMode-launched.
func repairFlowLaunchReadCmd(seams flowLaunchSeams, intent flowLaunchIntent, token string) tea.Cmd {
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
		if _, repairable := flowRepairObstructionForRecord(record); !repairable {
			event.Err = flowRepairNotRepairableStatus
			return event
		}
		records, err := seams.ListFlowSessions(intent.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if phase, occupied := flowRepairPhaseSessionOccupied(record, records); occupied {
			// The intent's Flow ID, not the record's: admission stamped it, the
			// read resolved this record from it, and it is the exact string
			// `approach flow read --flow-id` already takes.
			event.Err = flowRepairLiveSessionStatus(intent.FlowID, phase)
			return event
		}
		repoPath, worktreePath, ok := flowRepairLaunchPaths(record.RepoPath, record.WorktreePath, intent.FallbackRepoPath)
		if !ok {
			event.Err = flowRepairNoDirectoryStatus
			return event
		}
		planPath := strings.TrimSpace(record.PlanPath)
		if record.PlanID != "" && planPath == "" {
			// This one seam is guarded and its neighbours are not, because this
			// is the only one whose absence has a meaningful answer. Every seam
			// here is defaulted non-nil in NewWithOptions, so none of them is
			// nil in production; a hand-assembled seams value that omits
			// ReadFlow or ListFlowSessions has no repair to describe and would
			// only panic, while one that omits the plan lookup has a specific
			// refusal to report — and reporting it is what the pre-lifecycle
			// path did for a nil m.planMarkdownPath.
			if seams.PlanMarkdownPath == nil {
				event.Err = flowRepairNoPlanPathStatus
				return event
			}
			planPath, err = seams.PlanMarkdownPath(record.PlanID)
			if err != nil {
				event.Err = err.Error()
				return event
			}
		}
		event.Record = record
		event.RepoPath = repoPath
		event.WorktreePath = worktreePath
		event.PlanPath = planPath
		return event
	}
}

// flowRepairPhaseSessionOccupied is repair's Flow-scoped occupancy rule: a live
// session whose launch ID belongs to any phase of this Flow refuses the repair.
// The per-phase evidence is the same scoping a phase launch uses, but the
// effect is Flow-wide on purpose — the repair prompt authorizes phase reset,
// phase set, and plan set across the whole record, so a repair agent running
// beside a live phase agent can rewrite that phase's state underneath it.
// Resume's narrower phase scoping does not transfer.
//
// The trap this accepts, named: nothing finalizes the store record of an agent
// that died between sessions.ingest's session write and its phase attach, so
// "an agent is starting" and "the agent already died" are the same observation
// here, and the second leaves the Flow unrepairable from the TUI until the
// record ends. That is not a new class of trap — the classifier's mirrored
// -session branch already withdraws R forever for an identical never-ended
// session once attached — and `approach flow phase reset` is the documented way
// out. Distinguishing the two needs a session liveness signal `sessions` does
// not have.
//
// Deliberately not occupancy: an awaiting-session phase with no live session
// record anywhere. That is exactly the stale-launch state repair's classifier
// names as an obstruction, and refusing it would delete repair's primary use
// case. Untracked sessions carry a Flow ID but no phase launch ID and are
// likewise excluded, so a never-finalized repair session cannot permanently
// block future repairs.
//
// The matching phase is returned, not just the fact of a match: the refusal
// names its ID, because the CLI escape is per phase, and branches on its status,
// because which CLI escape is even legal depends on it. Iteration follows the
// record's own phase order so a Flow with two occupied phases reports the same
// one every press.
func flowRepairPhaseSessionOccupied(record flowstore.FlowRecord, records []sessions.SessionRecord) (flowstore.FlowPhase, bool) {
	for _, phase := range flowstore.OrderedPhases(record.Phases) {
		if flowLaunchPhaseSessionOccupied(phase, records) {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

// repairFlowLaunchPrepareCmd takes the cross-process repair reservation and
// builds the launch context from the record that reservation returned.
//
// The reservation seam is repair's own, not reserveTrackedFlowLaunch: both wrap
// the same advisory lock, but their error verbs differ ("reserve a repair
// launch for" vs "launch an agent for") and that text reaches the user. The
// wrapper prefix is preserved verbatim for the same reason.
//
// There is no phase write here and no second session listing. What a re-listing
// would close is not the self-inflicted race the tracked kinds have — repair
// never calls AddPhaseLaunchID, so it publishes no launch ID of its own to race
// against. What stays open is a peer approach process starting a phase session
// between the read stage's listing and this reservation, and a second listing
// would only narrow that window, not close it: the reservation excludes
// CloseFlow and other repairs, and was never a session fence. Resume accepts
// the same window for the same reason.
func (m Model) repairFlowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	reserve := m.reserveFlowRepairLaunch
	if reserve == nil {
		// The same guard reserveTrackedFlowLaunch has, for the same reason:
		// NewWithOptions defaults this seam, so only a hand-assembled Model
		// reaches here, and such a Model should refuse rather than panic in the
		// command goroutine. The zero record it yields is caught by the identity
		// check below and reported as a refusal.
		reserve = func(string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{}, func() {}, nil
		}
	}
	return func() tea.Msg {
		event := msg
		event.Stage = flowLaunchStagePrepared
		event.From = flowLaunchStatePreparing
		// Ahead of the reservation. A repair agent is the one that runs `flow
		// phase` commands against a Flow already in trouble, so it is the last
		// launch kind that should be allowed to run an unverified build.
		if refusal := refuseUnverifiedLaunchPin(settings.Pin); refusal != "" {
			event.Err = refusal
			return event
		}
		current, release, err := reserve(msg.FlowID)
		if err != nil {
			event.Err = "Reserve persisted Flow for repair: " + err.Error()
			return event
		}
		// Handed over before the revalidation below can refuse, so the handler
		// drops the advisory launch/close lock on every path out of here.
		event.Release = release
		if occupied, inspectErr := m.trackedFlowLeaseOccupied(msg.FlowID); inspectErr != nil {
			event.LeaseDeferred = true
			event.LeaseSetupError = true
			event.Err = flowLeaseSetupErrorStatus(inspectErr)
			return event
		} else if occupied {
			event.LeaseDeferred = true
			event.Err = flowLeaseOccupiedStatus
			return event
		}
		if strings.TrimSpace(current.FlowID) != msg.FlowID {
			// The reservation is supposed to return the Flow it locked. A seam
			// that returns a zero or foreign record would otherwise reach the
			// repair builder, which takes branch, commit, plan, and the prompt
			// from the record it is handed — producing a repair agent pointed at
			// the wrong record, or at none.
			event.Err = flowRepairNotRepairableStatus
			return event
		}
		if _, repairable := flowRepairObstructionForRecord(current); !repairable {
			event.Err = flowRepairNotRepairableStatus
			return event
		}
		resolved, err := resolveFlowRepairAgentSettings(current, settings.Preferences)
		if err != nil {
			event.Err = flowRepairAgentSettingsError(current, settings.Preferences, err)
			return event
		}
		switch resolved.Command {
		case agent.CommandCodex, agent.CommandClaude, agent.CommandCursor:
			// Embedded repair providers.
		default:
			event.Err = fmt.Sprintf("Flow repair does not support agent %q; press A to choose codex, claude, or cursor-agent", resolved.Command)
			return event
		}
		// The read stage's paths and plan resolution are submitted as fallbacks
		// rather than applied here: the builder owns the precedence between them
		// and the reserved record, and it is the builder that sets repair's
		// markers. FlowPhaseID stays empty and FlowLaunchTracked stays false
		// there — the empty phase ID is what makes flowLaunchFailureUpdate
		// refuse, which is what keeps a failed repair from ever mutating a phase.
		ctx, decision, err := newFlowLaunchContext(repairTarget{
			LaunchID:             msg.Token,
			Record:               current,
			Agent:                resolved,
			FallbackRepoPath:     msg.RepoPath,
			FallbackWorktreePath: msg.WorktreePath,
			PlanID:               msg.Record.PlanID,
			PlanPath:             msg.PlanPath,
		}, settings, flowLaunchRouting{})
		if err != nil {
			event.Err = "Prepare Flow repair launch: " + err.Error()
			return event
		}
		event.Context = ctx
		event.Route = decision.Route
		event.FallbackNote = decision.FallbackNote
		return event
	}
}

func resolveFlowRepairAgentSettings(record flowstore.FlowRecord, prefs agent.Preferences) (agent.Settings, error) {
	obstruction, _ := flowRepairObstructionForRecord(record)
	raw := flowstore.PhaseAgentSettings{}
	if obstruction.HasPhase {
		raw = obstruction.Phase.AgentSettings()
	}
	return flowstore.ResolvePhaseAgentSettings(prefs, raw)
}

func flowRepairAgentSettingsError(record flowstore.FlowRecord, prefs agent.Preferences, err error) string {
	obstruction, _ := flowRepairObstructionForRecord(record)
	if agent.Normalize(prefs.Command) == "" && (!obstruction.HasPhase || agent.Normalize(obstruction.Phase.Agent) == "") {
		return "Press A to choose codex, claude, or cursor-agent before repairing a Flow"
	}
	if !obstruction.HasPhase || obstruction.Phase.AgentSettings().IsZero() {
		return fmt.Sprintf("Flow repair does not support agent %q; press A to choose codex, claude, or cursor-agent", agent.Normalize(prefs.Command))
	}
	return err.Error()
}
