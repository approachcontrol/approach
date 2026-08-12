package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
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

// flowLaunchOutcome classifies what an autoPhase read stage decided. It exists
// so the handler can release, re-arm, or drop without matching on error text.
// The zero value is inert on purpose, so that an auto read event built without
// explicitly classifying itself falls into handleAutoFlowLaunchRead's default
// branch and releases the attempt. Starting the enum at "ok" would make that
// same omission launch instead, which is the one outcome a misclassified event
// must never reach. What keeps the field from being read on the prepared hop is
// the Stage switch in handleFlowLaunchEvent, not this ordering.
type flowLaunchOutcome int

const (
	flowLaunchOutcomeNone flowLaunchOutcome = iota
	flowLaunchOutcomeOK
	flowLaunchOutcomeRetry
	flowLaunchOutcomeStale
	flowLaunchOutcomeFailed
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
	// FallbackNote is set only when tmux mode wanted the tmux route and tmux
	// was missing. It is attached to a successful embedded install's status; a
	// failed install's own message wins instead.
	FallbackNote string
	// Outcome is read-stage-only and autoPhase-only; every dispatch on it is
	// gated on Stage for that reason.
	Outcome flowLaunchOutcome
	// FlowTitle is seeded from the intent so a failed read still has a title,
	// then overwritten from the fresh record once the read succeeds.
	FlowTitle string
	// Headless and AutoLaunch are resolved once in the read stage and carried,
	// so prepare cannot re-derive headless from the record and launch an
	// AutoMode phase interactively.
	Headless   bool
	AutoLaunch bool
	// Preflight-resolved paths threaded from read to prepare. RepoPath is not
	// Record.RepoPath: it falls back to the current repo, and ActionFailedMsg
	// gates its status on it.
	RepoPath     string
	WorktreePath string
	PlanPath     string
	// ProviderSessionID and ResumeCommand are phase-resume only. Session
	// identity is resolved once, at the key press, and re-validated by the read
	// stage against its own intent; what the read forwards here is what prepare
	// must use verbatim. Prepare may never re-derive it, because
	// AddPhaseLaunchID appends a launch ID with no session yet and
	// LatestPhaseSession would then fall through to a different session than
	// the read authorized. The provider itself is not carried: it is consumed
	// entirely by the read stage's drift check and nothing downstream reads it.
	ProviderSessionID string
	ResumeCommand     string
	Err               string
	Release           func()
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
// work starts, and returns the authoritative read command. The bool is the
// admission verdict: AutoMode needs it synchronously to decide whether to
// disarm its drain, and a refusal's text never has to reach a caller because
// any status it produces is already applied to the returned Model.
//
// One kind breaks that shape. A codex-app phase resume returns the untracked
// navigation command with admitted = false, because no attempt holds the Flow:
// the command is not a read command, and a caller that batches only on
// admitted — as AutoMode's drain does — would drop it. Only phaseResume
// submits it, and its caller returns the command regardless of the verdict.
func (m Model) requestFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	switch intent.Kind {
	case flowLaunchKindManualPhase:
		return m.admitManualFlowLaunch(intent)
	case flowLaunchKindAutoPhase:
		return m.admitAutoFlowLaunch(intent)
	case flowLaunchKindPhaseResume:
		return m.admitPhaseResumeFlowLaunch(intent)
	case flowLaunchKindRepair:
		return m.admitRepairFlowLaunch(intent)
	default:
		// Later beads route the remaining kinds; nothing submits them yet.
		return m, nil, false
	}
}

func (m Model) admitManualFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	flowID := strings.TrimSpace(intent.FlowID)
	intent.FlowID = flowID
	if flowID != "" && m.flowHeadlessWritePending(flowID) {
		return m.setStatus(statusOther, flowHeadlessWritePendingStatus), nil, false
	}
	if flowID == "" {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil, false
	}
	record, phase, ok := m.previewFlowLaunch(intent)
	if !ok {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil, false
	}
	// Kept synchronous, and after launchability, so the order statuses appear in
	// does not change.
	if command, _, _ := m.flowLaunchAgentSettings(); agent.Normalize(command) == "" {
		return m.setStatus(statusOther, "Press A to choose "+ui.AgentInputPlaceholder+" before launching an agent"), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil, false
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
		return m.setStatus(statusOther, noLaunchableFlowPhaseStatus), nil, false
	}
	m = next
	// A failure here would leave the attempt in reserved, where no event fence
	// matches, and the Flow would be held forever. Release rather than continue.
	next, advanced := m.transitionFlowLaunchAttempt(record.FlowID, token, flowLaunchStateReserved, flowLaunchStateReading)
	if !advanced {
		return m.releaseFlowLaunchAttempt(record.FlowID, token).setStatus(statusOther, noLaunchableFlowPhaseStatus), nil, false
	}
	m = next
	return m, m.flowLaunchReadCmd(intent, token, settings), true
}

// admitAutoFlowLaunch refuses in silence, without exception. The advance poll
// runs at 1 Hz and is view-independent, so any status a refusal set would be
// repainted every second over whatever the user is actually looking at. It also
// skips previewFlowLaunch: that resolves through the display caches, which the
// poll's Flows are frequently absent from, so launchability is left entirely to
// the authoritative read. No agent-command check happens here either — with no
// admission-time announcement to withhold, an unconfigured agent simply fails
// Preflight in the read stage and produces today's transient status.
func (m Model) admitAutoFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	flowID := strings.TrimSpace(intent.FlowID)
	intent.FlowID = flowID
	if flowID == "" || m.flowLaunchAdmissionOccupied(flowID) {
		return m, nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m, nil, false
	}
	// The settings snapshot is still taken. Only the refusal on an empty agent
	// command is dropped: a zero-value snapshot would make every auto launch
	// fail Preflight with "Press A to choose …".
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	next, reserved := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:    token,
		Kind:     intent.Kind,
		FlowID:   flowID,
		PhaseID:  intent.PhaseID,
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
		m.hasFlowRepairEmbeddedTerminalForFlow(flowID)
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

// flowLaunchCandidatePhase resolves the phase a kind is allowed to launch. The
// two rules deliberately differ and must not be merged: manual launch also
// offers ready merge phases and autoreview recovery on a PR target, neither of
// which AutoMode may ever start on its own.
func flowLaunchCandidatePhase(kind flowLaunchKind, record flowstore.FlowRecord, phaseID string) (flowstore.FlowPhase, bool) {
	if kind != flowLaunchKindAutoPhase {
		return flowLaunchablePhase(record, phaseID)
	}
	if strings.TrimSpace(phaseID) == "" {
		return nextAutoLaunchPhase(record)
	}
	// A named candidate is validated on its own merits rather than required to
	// still be first: an earlier phase becoming ready between poll and read
	// leaves this one perfectly launchable, and the store would accept it.
	ordered := flowstore.OrderedPhases(record.Phases)
	orderedRecord := record
	orderedRecord.Phases = ordered
	want := artifacts.NormalizePhaseID(phaseID)
	for i, phase := range ordered {
		if artifacts.NormalizePhaseID(phase.PhaseID) != want {
			continue
		}
		if !flowstore.PhaseLaunchEligible(orderedRecord, i) {
			return flowstore.FlowPhase{}, false
		}
		return phase, true
	}
	return flowstore.FlowPhase{}, false
}

// flowRecordHasOtherRunningPhase is flowAutoAdvanceOccupied's running-phase
// signal applied to the fresh record, minus the candidate itself. A running
// candidate means another source took it, which is staleness rather than
// occupancy, and the candidate check classifies that first.
func flowRecordHasOtherRunningPhase(record flowstore.FlowRecord, phaseID string) bool {
	candidate := artifacts.NormalizePhaseID(phaseID)
	for _, phase := range record.Phases {
		if phase.Status != flowstore.PhaseRunning {
			continue
		}
		if artifacts.NormalizePhaseID(phase.PhaseID) != candidate {
			return true
		}
	}
	return false
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
	// Resume and repair dispatch before the launcher is built: neither runs
	// Preflight, whose new-launch rules would reject the very phases they exist
	// for.
	if intent.Kind == flowLaunchKindPhaseResume {
		return phaseResumeFlowLaunchReadCmd(seams, intent, token)
	}
	if intent.Kind == flowLaunchKindRepair {
		return repairFlowLaunchReadCmd(seams, intent, token)
	}
	launcher := settings.apply(m.flowLaunchLauncher(token))
	if intent.Kind == flowLaunchKindAutoPhase {
		return autoFlowLaunchReadCmd(seams, launcher, intent, token)
	}
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
		phase, ok := flowLaunchCandidatePhase(intent.Kind, record, phaseID)
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
		event.Headless = record.Headless
		event.RepoPath = prepared.RepoPath
		event.WorktreePath = prepared.WorktreePath
		event.PlanPath = prepared.PlanPath
		return event
	}
}

// autoFlowLaunchReadCmd is the authoritative read for AutoMode. The check order
// is normative: two checks can both hold and they yield different outcomes, and
// Preflight runs before the session list because it is in-memory while
// ListFlowSessions scans the whole session store once per poll.
func autoFlowLaunchReadCmd(seams flowLaunchSeams, launcher FlowPhaseLauncher, intent flowLaunchIntent, token string) tea.Cmd {
	return func() tea.Msg {
		event := flowLaunchEventMsg{
			Token:   token,
			Kind:    intent.Kind,
			From:    flowLaunchStateReading,
			FlowID:  intent.FlowID,
			PhaseID: intent.PhaseID,
			Stage:   flowLaunchStageRead,
			// Seeded so a failed read still renders "Flow <title>: <err>";
			// overwritten from the fresh record the moment one exists.
			FlowTitle:  intent.FlowTitle,
			AutoLaunch: true,
			Headless:   true,
			Outcome:    flowLaunchOutcomeOK,
		}
		record, err := seams.ReadFlow(intent.FlowID)
		if err != nil {
			event.Outcome = flowLaunchOutcomeFailed
			event.Err = err.Error()
			return event
		}
		event.FlowTitle = flowTitleForStatus(record)
		if !record.AutoMode {
			event.Outcome = flowLaunchOutcomeStale
			return event
		}
		phase, ok := flowLaunchCandidatePhase(intent.Kind, record, intent.PhaseID)
		if !ok {
			event.Outcome = flowLaunchOutcomeStale
			return event
		}
		if flowRecordHasOtherRunningPhase(record, phase.PhaseID) {
			event.Outcome = flowLaunchOutcomeRetry
			return event
		}
		prepared, err := launcher.Preflight(FlowPhaseLaunchRequest{
			Record:     record,
			Phase:      phase,
			AutoLaunch: true,
			Headless:   true,
		})
		if err != nil {
			event.Outcome = flowLaunchOutcomeFailed
			event.Err = err.Error()
			return event
		}
		records, err := seams.ListFlowSessions(intent.FlowID)
		if err != nil {
			event.Outcome = flowLaunchOutcomeFailed
			event.Err = err.Error()
			return event
		}
		if flowLaunchPhaseSessionOccupied(phase, records) {
			// The previous run of this phase is still alive. It clears on its
			// own, so this re-arms rather than dropping the launch.
			event.Outcome = flowLaunchOutcomeRetry
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
	// Resume and repair dispatch before the candidate lookup below, whose
	// failure path emits noLaunchableFlowPhaseStatus — a string neither may
	// ever show.
	if msg.Kind == flowLaunchKindPhaseResume {
		return m.phaseResumeFlowLaunchPrepareCmd(msg, settings)
	}
	if msg.Kind == flowLaunchKindRepair {
		return m.repairFlowLaunchPrepareCmd(msg, settings)
	}
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
			Record: msg.Record,
			Phase:  phase,
			// Resolved once in the read stage. Re-deriving it from the record
			// here would launch an AutoMode Flow with persisted headless=false
			// as an interactive, focus-stealing terminal.
			Headless:   msg.Headless,
			AutoLaunch: msg.AutoLaunch,
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
		event.FallbackNote = result.FallbackNote
		switch result.Route {
		case FlowPhaseLaunchEmbedded:
			event.Route = flowLaunchRouteEmbedded
		case FlowPhaseLaunchTmux:
			event.Route = flowLaunchRouteTmux
		default:
			event.Route = flowLaunchRouteExternal
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
		if msg.Kind == flowLaunchKindAutoPhase {
			return m.handleAutoFlowLaunchRead(attempt, msg)
		}
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
		switch msg.Route {
		case flowLaunchRouteEmbedded:
			return m.installFlowLaunchEmbedded(attempt, msg)
		case flowLaunchRouteTmux:
			return m.handoffFlowLaunchTmux(attempt, msg)
		}
		return m.handoffFlowLaunchExternal(attempt, msg)
	}
	return m, nil
}

// handleAutoFlowLaunchRead splits by stage, not by error class, so no error
// type has to survive the string-only Err hop. Every branch here is forbidden
// from calling setStatus(statusOther, …): the poll runs at 1 Hz, so a sticky
// status would be re-set every second over whatever the user is looking at.
func (m Model) handleAutoFlowLaunchRead(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	switch msg.Outcome {
	case flowLaunchOutcomeOK:
	case flowLaunchOutcomeFailed:
		// The re-arm sits behind matchingFlowLaunchAttempt, so a superseded
		// attempt cannot re-arm a drain a newer one owns. The status is the
		// 3 s transient today's synchronous preflight failure already sets;
		// its expiry command has to be returned or it never fires. The re-arm
		// is also what makes this failure repeat on the next poll, which is why
		// it reports through the yielding setter: it will be back, and the
		// transition it would otherwise overwrite will not.
		m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).armAutoAdvanceDrain(attempt.FlowID)
		return m.setAutoAdvanceLaunchStatus("Flow " + msg.FlowTitle + ": " + msg.Err)
	case flowLaunchOutcomeRetry:
		// The blocker clears on its own, so re-arming is what makes the launch
		// resume without waiting for another completion edge.
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).armAutoAdvanceDrain(attempt.FlowID), nil
	default:
		// stale, and the inert zero value: drop the attempt and leave the drain
		// disarmed. Whatever superseded the candidate produces its own edge.
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token), nil
	}
	next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, flowLaunchStateReading, flowLaunchStatePreparing)
	if !ok {
		return m, nil
	}
	m = next.withFlowLaunchAttemptPhase(attempt.FlowID, attempt.Token, msg.PhaseID)
	prepareCmd := m.flowLaunchPrepareCmd(msg, attempt.Settings)
	// The queued announcement lands here rather than at admission, so it still
	// means "preflight passed" and cannot repeat every poll for a Flow that is
	// refused before it launches. Arriving a hop after the transition statuses
	// its own poll computed is why it announces through the yielding setter
	// rather than the replacing one. The blank-title guard is on the record: the
	// event's title has already fallen back to the Flow ID, so guarding on it
	// would be dead code that ships a status a titleless Flow never had.
	var statusCmd tea.Cmd
	if strings.TrimSpace(msg.Record.Title) != "" {
		m, statusCmd = m.setAutoAdvanceLaunchStatus("Flow " + msg.FlowTitle + ": " + autoAdvancePhaseLabel(msg.PhaseID) + " queued")
	}
	return m, batchNonNil(statusCmd, prepareCmd)
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
	// A repair is Flow-scoped and phase-untracked: it names no phase and must
	// never be stamped tracked, or its failures would look for a phase to
	// regress.
	if attempt.Kind != flowLaunchKindRepair {
		ctx.FlowLaunchTracked = true
	}
	if m.hasFlowRepairEmbeddedTerminalForFlow(ctx.FlowID) {
		// Admission makes this unreachable, but dropping the backstop would be
		// a regression against a future unguarded source. The wording comes
		// from the attempt's own kind, never from the prefill-failure
		// re-reservation, which labels every source manualPhase.
		canceled := "Flow phase launch canceled because a repair terminal is already open for this Flow"
		switch attempt.Kind {
		case flowLaunchKindPhaseResume:
			canceled = "Flow phase resume canceled because a repair terminal is already open for this Flow"
		case flowLaunchKindRepair:
			canceled = flowRepairTerminalStatus
		}
		return m.failFlowLaunch(attempt, ctx, msg.RepoPath, canceled)
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
	// Only a successful install reports the fallback. A failed one has already
	// returned above with its own message, which is the more useful one.
	if strings.TrimSpace(msg.FallbackNote) != "" {
		m = m.setStatus(statusOther, msg.FallbackNote)
	}
	return m, batchNonNil(prefillCmd, tickCmd, fetchCmd)
}

// handoffFlowLaunchTmux is handoffFlowLaunchExternal for the tmux route: the
// window it opens is not an embedded slot, so the attempt is released at
// handoff, the result is detached, and provider hooks own completion.
//
// Releasing here means the window stops counting as Flow occupancy, which an
// embedded slot would have provided until its process exited. What still covers
// the agent's actual work is the persisted phase status: a `running` phase
// occupies the Flow for both manual and automatic launches, so the gap opens
// only after the agent has declared its own phase complete and its CLI happens
// to stay at a prompt. Closing that too would mean polling tmux from the
// auto-advance drain — a subprocess on a timer, which is exactly what the probe
// rule forbids — or tracking window liveness in the background, which is the
// ownership this route exists to give up. See docs/tui-guide.md.
func (m Model) handoffFlowLaunchTmux(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	ctx := msg.Context
	spec, err := m.buildRepoTmuxAgentLaunch(ctx)
	if err != nil {
		releaseFlowLaunchReservation(msg.Release)
		return m.failFlowLaunch(attempt, ctx, msg.RepoPath, err.Error())
	}
	if next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, flowLaunchStatePreparing, flowLaunchStateHandoffPending); ok {
		m = next
	} else {
		// Same reasoning as the external handoff: the window is already being
		// created, so there is nothing to undo, but no AgentResultMsg fence
		// matches any other state and the attempt would be stranded.
		m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	}
	m, launchCmd := m.runAgentLaunchWithStatus(ctx, spec.Launch, msg.Release, tmuxLaunchStatus(spec))
	var fetchCmd tea.Cmd
	if ctx.FlowID != "" && m.flowSurfaceVisible() {
		m, fetchCmd = m.startFlowSurfaceFetch()
	}
	return m, batchNonNil(fetchCmd, launchCmd)
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
		failure := ActionFailedMsg{RepoPath: repoPath, Err: errText}
		if attempt.Kind == flowLaunchKindAutoPhase {
			// Keep the attempt until ActionFailedMsg is consumed. A newer stop
			// edge can cancel it first, making that delayed message a fenced no-op.
			failure.AutoAdvanceRetryFlowID = attempt.FlowID
			failure.AutoAdvanceRetryPhaseID = attempt.PhaseID
			failure.AutoAdvanceLaunchID = attempt.Token
		} else {
			m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
		}
		return m, func() tea.Msg {
			return failure
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
	return flowLaunchPhaseSessionOccupiedExcept(phase, records, flowSessionIdentity{})
}

// flowSessionIdentity names one session the way both stores key it: provider
// plus ID. The resume exemption below matches on the pair rather than the ID
// alone, because two providers can hand out the same session ID and exempting
// by ID would then also exempt a live competing agent.
type flowSessionIdentity struct {
	Provider  string
	SessionID string
}

// matches reuses the read stage's drift comparison: providers compare
// normalized, so a record spelling one "Codex" still matches. Session IDs
// compare byte-exact, because that is the identity both stores enforce —
// sessions.safeSessionDirName hashes the raw ID and flowstore.sameSession
// compares it, and neither writer canonicalizes before applying those rules, so
// IDs differing only by surrounding whitespace are two distinct agents. Callers
// still use TrimSpace to ask whether a session has an ID at all; that is an
// absence test, never a comparison. A zero identity — every caller but resume —
// matches nothing.
func (id flowSessionIdentity) matches(provider, sessionID string) bool {
	if strings.TrimSpace(id.SessionID) == "" {
		return false
	}
	return agent.Normalize(provider) == agent.Normalize(id.Provider) && sessionID == id.SessionID
}

// flowLaunchPhaseSessionOccupiedExcept is the same rule with one session
// exempted. Resume is the only caller that passes an identity: the session it
// is reattaching to is expected to look live — a never-finalized record is the
// common case resume exists for — so counting it would refuse every resume.
// Competing sessions on the same phase still occupy it, including one from
// another provider that shares the target's session ID. The exemption applies
// to both halves, because the target session appears in the store listing as
// well as in the phase's own mirror.
func flowLaunchPhaseSessionOccupiedExcept(phase flowstore.FlowPhase, records []sessions.SessionRecord, skip flowSessionIdentity) bool {
	if phaseHasMatchingLiveSessionExcept(phase, skip) {
		return true
	}
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID = strings.TrimSpace(launchID); launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, record := range records {
		if strings.TrimSpace(record.SessionID) == "" || skip.matches(string(record.Provider), record.SessionID) {
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
