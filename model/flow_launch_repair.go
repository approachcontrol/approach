package model

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
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
// them with `flow phase reset requires running recoverable phase`. Where the
// transition table can still reach running the message prepends the `phase set`
// that makes reset legal; where it cannot — pending, which is where a reset that
// left an older launch's live session behind lands the phase — no command
// clears it, and claiming otherwise would be the same broken remedy one step
// further along. docs/tui-guide.md covers all three.
func flowRepairLiveSessionStatus(flowID string, phase flowstore.FlowPhase) string {
	reset := fmt.Sprintf("approach flow phase reset --flow-id %s --phase-id %s", flowID, phase.PhaseID)
	switch {
	case phase.Status == flowstore.PhaseRunning:
		return fmt.Sprintf(
			"Flow phase %s already has a running session; if its agent is gone, clear it with %s",
			phase.PhaseID, reset)
	case slices.Contains(flowstore.AllowedNextPhaseStatuses(phase.Status), flowstore.PhaseRunning):
		return fmt.Sprintf(
			"Flow phase %s already has a running session and is %s; if its agent is gone, clear it with approach flow phase set --flow-id %s --phase-id %s --status running, then %s",
			phase.PhaseID, phase.Status, flowID, phase.PhaseID, reset)
	default:
		return fmt.Sprintf(
			"Flow phase %s already has a running session and is %s; reset needs a running phase, so this phase's session metadata has to be corrected directly",
			phase.PhaseID, phase.Status)
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
	if m.flowLaunchAdmissionOccupied(flowID) || m.flowHeadlessWritePending(flowID) {
		return m.setStatus(statusOther, m.flowRepairOccupancyRefusal(flowID)), nil, false
	}
	command, _, _ := m.flowLaunchAgentSettings()
	switch command {
	case "":
		return m.setStatus(statusOther, "Press A to choose codex or claude before repairing a Flow"), nil, false
	case agent.CommandCodexApp:
		return m.setStatus(statusOther, "Flow repair requires an embedded CLI agent; press A to choose codex or claude"), nil, false
	case agent.CommandCodex, agent.CommandClaude:
		// Supported below.
	default:
		return m.setStatus(statusOther, fmt.Sprintf("Flow repair does not support agent %q; press A to choose codex or claude", command)), nil, false
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

// flowRepairOccupancyRefusal names what is holding the Flow. The terminal rung
// is written as the terminal predicates disjoined with attempt occupancy, not
// as the terminals alone: a manualPhase or autoPhase attempt in flight reaches
// it, and a rung that named only the terminals would fall through to the
// headless message and tell the user something false.
func (m Model) flowRepairOccupancyRefusal(flowID string) string {
	switch m.flowLaunchAttemptKind(flowID) {
	case flowLaunchKindRepair:
		return flowRepairPendingStatus
	case flowLaunchKindPhaseResume:
		return flowRepairResumePendingStatus
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
		current, release, err := reserve(msg.FlowID)
		if err != nil {
			event.Err = "Reserve persisted Flow for repair: " + err.Error()
			return event
		}
		// Handed over before the revalidation below can refuse, so the handler
		// drops the advisory launch/close lock on every path out of here.
		event.Release = release
		if strings.TrimSpace(current.FlowID) != msg.FlowID {
			// The reservation is supposed to return the Flow it locked. A seam
			// that returns a zero or foreign record would otherwise reach
			// refreshFlowRepairLaunchContext, which rebuilds branch, commit,
			// plan, and the prompt from whatever it is handed — producing a
			// repair agent pointed at the wrong record, or at none.
			event.Err = flowRepairNotRepairableStatus
			return event
		}
		if _, repairable := flowRepairObstructionForRecord(current); !repairable {
			event.Err = flowRepairNotRepairableStatus
			return event
		}
		// Seeded before the refresh, not after: refreshFlowRepairLaunchContext
		// treats ctx.WorktreePath/ctx.RepoPath as its fallback chain and keeps
		// ctx.PlanPath only when the record's plan ID still matches, so an
		// unseeded context would silently discard the read stage's repo
		// fallback and its PlanMarkdownPath lookup.
		ctx := actions.AgentLaunchContext{
			Command: settings.Command,
			// The admission token, never a fresh ID: every LaunchID-keyed fence
			// downstream is on it.
			LaunchID:         msg.Token,
			RepoPath:         msg.RepoPath,
			WorktreePath:     msg.WorktreePath,
			SessionStateRoot: settings.SessionStateRoot,
			PlanID:           msg.Record.PlanID,
			PlanPath:         msg.PlanPath,
			FlowID:           msg.FlowID,
			FlowRepair:       true,
			Embedded:         true,
			Model:            settings.Model,
			ReasoningEffort:  settings.ReasoningEffort,
		}
		// FlowPhaseID stays empty and FlowLaunchTracked stays false. The empty
		// phase ID is what makes flowLaunchFailureUpdate refuse, which is what
		// keeps a failed repair from ever mutating a phase.
		event.Context = refreshFlowRepairLaunchContext(ctx, current)
		event.Route = flowLaunchRouteEmbedded
		return event
	}
}
