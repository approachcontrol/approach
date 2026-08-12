package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// noLaunchableFlowPhaseStatus covers every reason a launch is refused before it
// reaches preflight: no eligible phase, an occupied Flow, or a live session on
// the phase. Occupancy deliberately reuses this text so the migration adds no
// user-visible strings.
const noLaunchableFlowPhaseStatus = "No launchable Flow phase"

// flowLaunchStage names the two asynchronous hops the lifecycle emits. Handoff
// results and failure persistence arrive on messages that already exist, so
// they are not stages.
type flowLaunchStage int

const (
	flowLaunchStageRead flowLaunchStage = iota + 1
	flowLaunchStagePrepared
)

// flowLaunchEventMsg carries one asynchronous hop back into Update. Token,
// Kind, and From together fence it against the attempt that started it.
type flowLaunchEventMsg struct {
	Token   string
	Kind    flowLaunchKind
	From    flowLaunchState
	FlowID  string
	PhaseID string
	Stage   flowLaunchStage
	Record  flowstore.FlowRecord
	Context actions.AgentLaunchContext
	Route   flowLaunchRoute
	Skipped bool
	// Preflight-resolved paths threaded from read to prepare. RepoPath is not
	// Record.RepoPath: it falls back to the current repo, and ActionFailedMsg
	// gates its status on it.
	RepoPath     string
	WorktreePath string
	PlanPath     string
	Err          string
	Release      func()
}

// flowLaunchAgentSettingsSnapshot freezes the mutable settings that admission
// validated. A user may change them while the authoritative read is in flight;
// that must affect the next launch, not change this launch's route or prompt.
type flowLaunchAgentSettingsSnapshot struct {
	Command          string
	Model            string
	ReasoningEffort  string
	SessionStateRoot string
	PromptTemplates  FlowPromptTemplates
}

func snapshotFlowLaunchAgentSettings(launcher FlowPhaseLauncher) flowLaunchAgentSettingsSnapshot {
	return flowLaunchAgentSettingsSnapshot{
		Command:          launcher.AgentCommand,
		Model:            launcher.Model,
		ReasoningEffort:  launcher.ReasoningEffort,
		SessionStateRoot: launcher.SessionStateRoot,
		PromptTemplates:  launcher.PromptTemplates,
	}
}

func (snapshot flowLaunchAgentSettingsSnapshot) apply(launcher FlowPhaseLauncher) FlowPhaseLauncher {
	launcher.AgentCommand = snapshot.Command
	launcher.Model = snapshot.Model
	launcher.ReasoningEffort = snapshot.ReasoningEffort
	launcher.SessionStateRoot = snapshot.SessionStateRoot
	launcher.PromptTemplates = snapshot.PromptTemplates
	return launcher
}

// requestFlowLaunch is the lifecycle's only entry point. It admits or refuses
// the intent synchronously, installs the reservation before any asynchronous
// work starts, and returns the authoritative read command.
func (m Model) requestFlowLaunch(intent flowLaunchIntent) (tea.Model, tea.Cmd) {
	if intent.Kind != flowLaunchKindManualPhase {
		// Later beads route the remaining kinds; nothing submits them yet.
		return m, nil
	}
	flowID := strings.TrimSpace(intent.FlowID)
	intent.FlowID = flowID
	if flowID != "" && m.flowHeadlessWritePending(flowID) {
		return m.setStatus(statusOther, flowHeadlessWritePendingStatus), nil
	}
	if flowID == "" {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	record, phase, ok := m.previewFlowLaunch(intent)
	if !ok {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	// Kept synchronous, and after launchability, so the order statuses appear in
	// does not change.
	if command, _, _ := m.flowLaunchAgentSettings(); agent.Normalize(command) == "" {
		return m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching an agent"), nil
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	next, reserved := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:    token,
		Kind:     intent.Kind,
		FlowID:   record.FlowID,
		PhaseID:  phase.PhaseID,
		Origin:   intent.Origin,
		Settings: settings,
	}, flowLaunchStateReserved)
	if !reserved {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	m = next
	// A failure here would leave the attempt in reserved, where no event fence
	// matches, and the Flow would be held forever. Release rather than continue.
	next, advanced := m.transitionFlowLaunchAttempt(record.FlowID, token, flowLaunchStateReserved, flowLaunchStateReading)
	if !advanced {
		return m.releaseFlowLaunchAttempt(record.FlowID, token).setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	m = next
	return m, m.flowLaunchReadCmd(intent, token, settings)
}

// previewFlowLaunch answers the cached, non-authoritative question the footer
// asks: could this intent launch right now? It resolves the intent's Flow ID
// rather than whatever happens to be selected, so a preview and an admission
// always speak about the same Flow.
func (m Model) previewFlowLaunch(intent flowLaunchIntent) (flowstore.FlowRecord, flowstore.FlowPhase, bool) {
	record, phase, ok := m.cachedFlowLaunchTarget(intent)
	if !ok || m.flowLaunchAdmissionOccupied(record.FlowID) {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
	}
	return record, phase, true
}

// cachedFlowLaunchTarget is the launchability half of the preview, without the
// occupancy half. Admission needs the two separately so it can name whichever
// one is actually blocking; the footer only needs their conjunction.
func (m Model) cachedFlowLaunchTarget(intent flowLaunchIntent) (flowstore.FlowRecord, flowstore.FlowPhase, bool) {
	record, ok := m.cachedFlowRecord(intent.FlowID)
	if !ok || strings.TrimSpace(record.FlowID) == "" {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
	}
	phase, ok := flowLaunchablePhase(record, intent.PhaseID)
	if !ok {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
	}
	return record, phase, true
}

// flowLaunchAdmissionOccupied reports whether anything already owns this Flow.
// It spans the lifecycle's own attempts and every launch source that has not
// been migrated yet, which is what keeps the two mutually exclusive per Flow.
func (m Model) flowLaunchAdmissionOccupied(flowID string) bool {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return false
	}
	return m.flowLaunchAttemptOccupied(flowID) ||
		m.hasFlowEmbeddedTerminalForFlow(flowID) ||
		m.hasFlowRepairEmbeddedTerminalForFlow(flowID) ||
		m.hasPendingFlowRepairLaunch(flowID) ||
		m.hasPendingFlowPhaseResumeForFlow(flowID)
}

func (m Model) cachedFlowRecord(flowID string) (flowstore.FlowRecord, bool) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return m.selectedFlow()
	}
	if record, ok := m.selectedFlow(); ok && record.FlowID == flowID {
		return record, true
	}
	for _, record := range m.flowLookupRecords() {
		if record.FlowID == flowID {
			return record, true
		}
	}
	return flowstore.FlowRecord{}, false
}

// flowLaunchablePhase resolves the phase an intent targets. An explicit phase
// ID is validated with flowPhaseCanLaunch; otherwise the first eligible ordered
// phase wins. The ordered record is what indexes, so it is what is passed.
func flowLaunchablePhase(record flowstore.FlowRecord, phaseID string) (flowstore.FlowPhase, bool) {
	if strings.TrimSpace(phaseID) != "" {
		phase, ok := flowPhaseByID(record, phaseID)
		if !ok || !flowPhaseCanLaunch(record, phase) {
			return flowstore.FlowPhase{}, false
		}
		return phase, true
	}
	ordered := flowstore.OrderedPhases(record.Phases)
	orderedRecord := record
	orderedRecord.Phases = ordered
	for i, phase := range ordered {
		if flowPhaseCanLaunchAtIndex(orderedRecord, i) {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

// flowLaunchLauncher borrows the preflight and prepare steps through the
// lifecycle's own seams. NewLaunchID is pinned to the admission token: a second
// generated ID would make every LaunchID-keyed fence miss and strand the
// attempt.
func (m Model) flowLaunchLauncher(token string) FlowPhaseLauncher {
	launcher := m.flowPhaseLauncher()
	launcher.PlanMarkdownPath = m.launchSeams.PlanMarkdownPath
	launcher.ReadPlan = m.launchSeams.ReadPlan
	launcher.AddFlowPhaseLaunchID = m.launchSeams.AddPhaseLaunchID
	launcher.NewLaunchID = func() string { return token }
	return launcher
}

func (m Model) flowLaunchReadCmd(intent flowLaunchIntent, token string, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	seams := m.launchSeams
	launcher := settings.apply(m.flowLaunchLauncher(token))
	phaseID := intent.PhaseID
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
		phase, ok := flowLaunchablePhase(record, phaseID)
		if !ok {
			event.Err = noLaunchableFlowPhaseStatus
			return event
		}
		records, err := seams.ListFlowSessions(intent.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if flowLaunchPhaseSessionOccupied(phase, records) {
			// A live session on the phase means it is effectively still
			// running, which is what the existing text already says.
			event.Err = noLaunchableFlowPhaseStatus
			return event
		}
		prepared, err := launcher.Preflight(FlowPhaseLaunchRequest{
			Record:   record,
			Phase:    phase,
			Headless: record.Headless,
		})
		if err != nil {
			event.Err = err.Error()
			return event
		}
		event.PhaseID = phase.PhaseID
		event.Record = record
		event.RepoPath = prepared.RepoPath
		event.WorktreePath = prepared.WorktreePath
		event.PlanPath = prepared.PlanPath
		return event
	}
}

func (m Model) flowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	launcher := settings.apply(m.flowLaunchLauncher(msg.Token))
	phase, ok := flowPhaseByID(msg.Record, msg.PhaseID)
	if !ok {
		return func() tea.Msg {
			event := msg
			event.Stage = flowLaunchStagePrepared
			event.From = flowLaunchStatePreparing
			event.Err = noLaunchableFlowPhaseStatus
			return event
		}
	}
	prepared := FlowPhaseLaunchPreparedRequest{
		FlowPhaseLaunchRequest: FlowPhaseLaunchRequest{
			Record:   msg.Record,
			Phase:    phase,
			Headless: msg.Record.Headless,
		},
		RepoPath:     msg.RepoPath,
		WorktreePath: msg.WorktreePath,
		PlanPath:     msg.PlanPath,
		LaunchID:     msg.Token,
	}
	return func() tea.Msg {
		event := msg
		event.Stage = flowLaunchStagePrepared
		event.From = flowLaunchStatePreparing
		_, release, reserveErr := m.reserveTrackedFlowLaunch(msg.FlowID)
		if reserveErr != nil {
			event.Err = reserveErr.Error()
			return event
		}
		event.Release = release
		result, err := launcher.Prepare(prepared)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if result.Skipped {
			event.Skipped = true
			return event
		}
		event.Context = result.Context
		event.Route = flowLaunchRouteExternal
		if result.Route == FlowPhaseLaunchEmbedded {
			event.Route = flowLaunchRouteEmbedded
		}
		return event
	}
}

// handleFlowLaunchEvent is the only handler for lifecycle-emitted events. A
// fence mismatch returns without touching anything: no phase write, no
// terminal, no release, and no status replacement.
func (m Model) handleFlowLaunchEvent(msg flowLaunchEventMsg) (Model, tea.Cmd) {
	want, validStage := flowLaunchStageState(msg.Stage)
	if !validStage || msg.From != want {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	attempt, ok := m.matchingFlowLaunchAttempt(msg.FlowID, msg.Token, msg.Kind, want)
	if !ok {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	switch msg.Stage {
	case flowLaunchStageRead:
		if msg.Err != "" {
			// Nothing has been persisted at this point and no context exists,
			// so the attempt simply goes away with a status.
			return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).setStatus(statusOther, msg.Err), nil
		}
		next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, flowLaunchStateReading, flowLaunchStatePreparing)
		if !ok {
			return m, nil
		}
		m = next.withFlowLaunchAttemptPhase(attempt.FlowID, attempt.Token, msg.PhaseID)
		return m, m.flowLaunchPrepareCmd(msg, attempt.Settings)
	case flowLaunchStagePrepared:
		if msg.Skipped {
			releaseFlowLaunchReservation(msg.Release)
			return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token), nil
		}
		if msg.Err != "" {
			releaseFlowLaunchReservation(msg.Release)
			return m.failFlowLaunch(attempt, msg.Context, msg.RepoPath, msg.Err)
		}
		m = m.markFlowLaunchAttemptMutatedPhase(attempt.FlowID, attempt.Token)
		attempt.MutatedPhase = true
		if msg.Route == flowLaunchRouteEmbedded {
			return m.installFlowLaunchEmbedded(attempt, msg)
		}
		return m.handoffFlowLaunchExternal(attempt, msg)
	}
	return m, nil
}

func flowLaunchStageState(stage flowLaunchStage) (flowLaunchState, bool) {
	switch stage {
	case flowLaunchStageRead:
		return flowLaunchStateReading, true
	case flowLaunchStagePrepared:
		return flowLaunchStatePreparing, true
	default:
		return 0, false
	}
}

// installFlowLaunchEmbedded reproduces everything the
// FlowEmbeddedLaunchRequestedMsg path does, including the Flow surface refresh
// that lives in its Update case, and only removes the attempt once the slot
// that replaces it as the Flow's owner exists.
func (m Model) installFlowLaunchEmbedded(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	defer releaseFlowLaunchReservation(msg.Release)
	ctx := msg.Context
	ctx.Embedded = true
	ctx.FlowLaunchTracked = true
	if m.hasFlowRepairEmbeddedTerminalForFlow(ctx.FlowID) {
		// Admission makes this unreachable, but dropping the backstop would be
		// a regression against a future unguarded source.
		return m.failFlowLaunch(attempt, ctx, msg.RepoPath, "Flow phase launch canceled because a repair terminal is already open for this Flow")
	}
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err, prefillCmd := m.openFlowEmbeddedTerminalReserved(ctx)
	if err != nil || !opened {
		errText := "Maximum embedded terminals reached"
		if err != nil {
			errText = err.Error()
		}
		return next.failFlowLaunch(attempt, ctx, msg.RepoPath, errText)
	}
	m = next
	if prefillCmd == nil {
		m = m.updateFlowTerminalFocusAfterLaunch(ctx)
	}
	var tickCmd tea.Cmd
	if needsTick {
		m, tickCmd = m.startEmbeddedTerminalTick()
	}
	var fetchCmd tea.Cmd
	if ctx.FlowID != "" && m.flowSurfaceVisible() {
		m, fetchCmd = m.startFlowSurfaceFetch()
	}
	m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	return m, batchNonNil(prefillCmd, tickCmd, fetchCmd)
}

// handoffFlowLaunchExternal calls launchAgent directly rather than through
// launchAgentWithContext: that helper swallows a synchronous launch error
// without ever emitting an AgentResultMsg. Splitting the call lets the error
// reach failFlowLaunch instead of stranding the attempt in preparing, which is
// where it would sit with no message left to move it.
func (m Model) handoffFlowLaunchExternal(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	ctx := msg.Context
	launch, err := m.launchAgent(ctx)
	if err != nil {
		releaseFlowLaunchReservation(msg.Release)
		return m.failFlowLaunch(attempt, ctx, msg.RepoPath, err.Error())
	}
	if next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, flowLaunchStatePreparing, flowLaunchStateHandoffPending); ok {
		m = next
	} else {
		// The attempt moved on between prepare and handoff. The agent is already
		// launched so there is nothing to undo, but leaving the attempt behind
		// would strand it: no AgentResultMsg fence matches any other state.
		m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	}
	m, launchCmd := m.runAgentLaunchWithReservation(ctx, launch, msg.Release)
	var fetchCmd tea.Cmd
	if ctx.FlowID != "" && m.flowSurfaceVisible() {
		m, fetchCmd = m.startFlowSurfaceFetch()
	}
	return m, batchNonNil(fetchCmd, launchCmd)
}

// failFlowLaunch classifies before it transitions. A failure with nothing
// persistable produces no flowLaunchFailurePersistedMsg, so entering
// failurePersisting first would strand the attempt and block the Flow forever.
func (m Model) failFlowLaunch(attempt flowLaunchAttempt, ctx actions.AgentLaunchContext, repoPath, errText string) (Model, tea.Cmd) {
	if !attempt.MutatedPhase {
		// Nothing was written, so the phase must stay as it is. ActionFailedMsg
		// keeps main's repo gate and Flow surface refresh; a bare status drops
		// both.
		m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
		return m, func() tea.Msg {
			return ActionFailedMsg{RepoPath: repoPath, Err: errText}
		}
	}
	update, ok := m.flowLaunchFailureUpdate(ctx, errText)
	if !ok {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).setStatus(statusOther, errText), nil
	}
	next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, attempt.State, flowLaunchStateFailurePersisting)
	if !ok {
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).setStatus(statusOther, errText), nil
	}
	return next, flowLaunchFailurePersistCmd(next.launchSeams.SetPhase, update, ctx, errText)
}

func flowLaunchFailurePersistCmd(
	setPhase func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error),
	update flowstore.PhaseUpdate,
	ctx actions.AgentLaunchContext,
	errText string,
) tea.Cmd {
	return func() tea.Msg {
		_, err := setPhase(update)
		return flowLaunchFailurePersistedMsg{
			LaunchContext: ctx,
			OriginalErr:   errText,
			PersistErr:    err,
		}
	}
}

// handleFlowLaunchPrefillFailure closes the window main leaves open: it
// dismisses the slot before persisting the failure, so the Flow is briefly
// unowned while its phase is still persisted running. Classify, re-reserve,
// then dismiss.
//
// This runs for every phase-tracked prefill failure, not only lifecycle ones,
// because any tracked context has a persisted running phase to correct. The
// re-reservation is therefore labelled manualPhase even when the launch came
// from AutoMode or the initial Plan launch; nothing fences on Kind here, and
// the attempt is released by the unconditional persistence message, so the only
// effect is that repair and AutoMode defer for the length of one write. Repair
// contexts never reach it: their empty phase ID makes flowLaunchFailureUpdate
// return false.
func (m Model) handleFlowLaunchPrefillFailure(msg embeddedPromptPrefillResultMsg) (Model, tea.Cmd) {
	ctx := msg.LaunchContext
	errText := msg.Err.Error()
	update, ok := m.flowLaunchFailureUpdate(ctx, errText)
	if !ok {
		m = m.dismissEmbeddedTerminalForReason(msg.ID, embeddedTerminalRemovalPrefillFailure)
		return m.startFlowLaunchFailure(ctx, errText)
	}
	// The reservation consults the attempt map only. The slot being corrected
	// is still installed, so full admission would refuse every time and the gap
	// would never close.
	next, reserved := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:        ctx.LaunchID,
		Kind:         flowLaunchKindManualPhase,
		FlowID:       ctx.FlowID,
		PhaseID:      ctx.FlowPhaseID,
		MutatedPhase: true,
	}, flowLaunchStateFailurePersisting)
	if !reserved {
		m = m.dismissEmbeddedTerminalForReason(msg.ID, embeddedTerminalRemovalPrefillFailure)
		return m.startFlowLaunchFailure(ctx, errText)
	}
	m = next.dismissEmbeddedTerminalForReason(msg.ID, embeddedTerminalRemovalPrefillFailure)
	return m, flowLaunchFailurePersistCmd(m.launchSeams.SetPhase, update, ctx, errText)
}

func (m Model) withFlowLaunchAttemptPhase(flowID, token, phaseID string) Model {
	return m.updateFlowLaunchAttempt(flowID, token, func(attempt *flowLaunchAttempt) {
		attempt.PhaseID = phaseID
	})
}

func (seams flowLaunchSeams) newLaunchID() string {
	if seams.NewLaunchID == nil {
		return newLaunchID()
	}
	return seams.NewLaunchID()
}

// flowLaunchPhaseSessionOccupied unions the phase's mirrored sessions with the
// authoritative session store, scoped identically: only sessions whose launch
// ID belongs to this phase count. A wider rule would let one crashed agent make
// the Flow permanently unlaunchable.
func flowLaunchPhaseSessionOccupied(phase flowstore.FlowPhase, records []sessions.SessionRecord) bool {
	if phaseHasMatchingLiveSession(phase) {
		return true
	}
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID = strings.TrimSpace(launchID); launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, record := range records {
		if strings.TrimSpace(record.SessionID) == "" {
			continue
		}
		if _, ok := launches[strings.TrimSpace(record.LaunchID)]; !ok {
			continue
		}
		if flowSessionLive(record.Status, record.EndedAt) {
			return true
		}
	}
	return false
}

// flowSessionLive is main's liveness half, extracted so the lifecycle and
// phaseHasMatchingLiveSession cannot drift apart.
func flowSessionLive(status string, endedAt time.Time) bool {
	if status = strings.TrimSpace(status); status != "" {
		return status != "ended"
	}
	return endedAt.IsZero()
}
