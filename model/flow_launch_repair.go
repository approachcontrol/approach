package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// The repair refusals. They are constants for the same reason resume's are:
// admission decides on the display cache, the authoritative read and the
// prepare stage re-decide against fresh records, and a user who presses R twice
// must not see one condition worded two ways.
//
// Only flowRepairLiveSessionStatus is new, and its text is deliberately
// identical to flowPhaseResumeLiveSessionStatus: repair now rejects a live
// session held in the session store, which is the same condition resume already
// names, so the app's user-visible string vocabulary does not grow. It stays a
// separate constant so repair's refusals read in one file.
const (
	flowRepairPendingStatus       = "A repair launch is already pending for this Flow"
	flowRepairResumePendingStatus = "A phase resume is already pending for this Flow"
	flowRepairTerminalStatus      = "Close, detach, or dismiss the existing Flow terminal before repairing this Flow"
	flowRepairNotRepairableStatus = "Flow is no longer repairable"
	flowRepairNoDirectoryStatus   = "Cannot find a usable worktree or repository directory for this Flow repair"
	flowRepairNoPlanPathStatus    = "Cannot determine linked plan path for this Flow repair"
	flowRepairLiveSessionStatus   = "Flow phase already has a running session"
)

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
func (m Model) cachedRepairTarget(intent flowLaunchIntent) (flowstore.FlowRecord, flowRepairObstruction, bool) {
	if !m.flowSurfaceVisible() {
		return flowstore.FlowRecord{}, flowRepairObstruction{}, false
	}
	record, ok := m.cachedFlowRecord(intent.FlowID)
	if !ok || strings.TrimSpace(record.FlowID) == "" {
		return flowstore.FlowRecord{}, flowRepairObstruction{}, false
	}
	obstruction, ok := flowRepairObstructionForRecord(record)
	if !ok {
		return flowstore.FlowRecord{}, flowRepairObstruction{}, false
	}
	return record, obstruction, true
}

// previewRepairLaunch is cachedRepairTarget conjoined with occupancy, mirroring
// previewFlowLaunch over cachedFlowLaunchTarget. Admission needs the two halves
// separately so it can name whichever one is blocking; the footer only needs
// their conjunction.
func (m Model) previewRepairLaunch(intent flowLaunchIntent) (flowstore.FlowRecord, flowRepairObstruction, bool) {
	record, obstruction, ok := m.cachedRepairTarget(intent)
	if !ok || m.flowLaunchAdmissionOccupied(record.FlowID) {
		return flowstore.FlowRecord{}, flowRepairObstruction{}, false
	}
	return record, obstruction, true
}

// admitRepairFlowLaunch is repair's half of the lifecycle's admission. The
// refusal ladder below is transcribed from the key handler it replaces, in the
// same order: durable obstacles before the transient one, because a headless
// write clears on its own and an open terminal does not.
func (m Model) admitRepairFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	record, _, ok := m.cachedRepairTarget(intent)
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
		if flowRepairPhaseSessionOccupied(record, records) {
			event.Err = flowRepairLiveSessionStatus
			return event
		}
		repoPath, worktreePath, ok := flowRepairLaunchPaths(record.RepoPath, record.WorktreePath, intent.FallbackRepoPath)
		if !ok {
			event.Err = flowRepairNoDirectoryStatus
			return event
		}
		planPath := strings.TrimSpace(record.PlanPath)
		if record.PlanID != "" && planPath == "" {
			// The guard is not defensive dressing: planMarkdownPath is defaulted
			// non-nil in NewWithOptions, but a zero-value Model has a nil seam
			// and several repair tests build their models that way.
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
func flowRepairPhaseSessionOccupied(record flowstore.FlowRecord, records []sessions.SessionRecord) bool {
	for _, phase := range record.Phases {
		if flowLaunchPhaseSessionOccupied(phase, records) {
			return true
		}
	}
	return false
}

// repairFlowLaunchPrepareCmd takes the cross-process repair reservation and
// builds the launch context from the record that reservation returned.
//
// The reservation seam is repair's own, not reserveTrackedFlowLaunch: both wrap
// the same advisory lock, but their error verbs differ ("reserve a repair
// launch for" vs "launch an agent for") and that text reaches the user. The
// wrapper prefix is preserved verbatim for the same reason.
//
// There is no phase write here and no second session listing. Repair never
// calls AddPhaseLaunchID, so there is no residual race for a re-listing to
// close; the reservation makes close and repair mutually exclusive, and it was
// never a session fence.
func (m Model) repairFlowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	reserve := m.reserveFlowRepairLaunch
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
