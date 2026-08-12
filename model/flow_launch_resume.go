package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

// The resume refusals. They are package-level constants because the snapshot
// resolver is a Model method and the authoritative read stage is a free
// function, and both have to emit byte-identical text: the snapshot decides
// whether to submit at all, the read re-decides against the fresh record, and a
// user who presses r twice must not see the same condition worded two ways.
//
// Three of them are new, against noLaunchableFlowPhaseStatus's convention that
// a migration adds no strings. Each is here because reuse would be actively
// wrong on a resume: drift and a competing live session are conditions that
// cannot arise today because nothing re-reads, and "No launchable Flow phase"
// would tell a user their completed phase is unlaunchable — true, and useless
// when they pressed r on it.
const (
	flowPhaseResumeAwaitingSessionStatus  = "Flow phase is awaiting session capture"
	flowPhaseResumeResettableStatus       = "Flow phase has an ended session; reset it to ready"
	flowPhaseResumeEndedSessionStatus     = "Flow phase has an ended session and cannot be resumed"
	flowPhaseResumeMissingSessionIDStatus = "Flow phase has missing session id"
	flowPhaseResumeNoSessionStatus        = "Flow phase has no session to resume"
	flowPhaseResumeNoProviderStatus       = "Flow phase session has no provider"
	flowPhaseResumeNoWorktreeStatus       = "Flow phase has no worktree path to resume from"
	flowPhaseResumeDriftStatus            = "Flow phase changed; refresh and try again"
	flowPhaseResumeLiveSessionStatus      = "Flow phase already has a running session"
	flowPhaseResumeTerminalStatus         = "Close, detach, or dismiss the existing Flow terminal before resuming this phase"
)

// flowPhaseResumeRequest turns the selected phase into an intent, or into the
// refusal the snapshot already justifies. It cannot be a free function:
// resolving codex → codex-app reads the agent preference, the repo fallback
// reads the current repo, and the codex-app route needs a fully built launch
// context that no authoritative read produces.
//
// The returned status is empty when the refusal is silent; today every snapshot
// refusal here has text, and the empty case exists so the caller does not have
// to distinguish "refused" from "refused loudly".
func (m Model) flowPhaseResumeRequest(record flowstore.FlowRecord, phase flowstore.FlowPhase) (flowLaunchIntent, string, bool) {
	session, status, ok := resumableFlowPhaseSession(phase)
	if !ok {
		return flowLaunchIntent{}, status, false
	}
	provider := agent.Normalize(strings.TrimSpace(session.Provider))
	if provider == "" {
		return flowLaunchIntent{}, flowPhaseResumeNoProviderStatus, false
	}
	command := flowPhaseResumeCommand(provider, m.agentCommand)
	if err := agent.Validate(command); err != nil {
		return flowLaunchIntent{}, err.Error(), false
	}
	// Carried verbatim, not trimmed: both stores key a session by its raw ID, so
	// canonicalizing here would name a session neither store has. The blank
	// check is an absence test. actions applies the launch boundary's own
	// trimming when it builds the command line.
	if strings.TrimSpace(session.SessionID) == "" {
		return flowLaunchIntent{}, flowPhaseResumeMissingSessionIDStatus, false
	}
	sessionID := session.SessionID
	fallbackRepoPath, _ := m.currentRepoPath()
	repoPath := record.RepoPath
	if repoPath == "" {
		repoPath = fallbackRepoPath
	}
	workingDir := record.WorktreePath
	if workingDir == "" && command != agent.CommandCodexApp {
		return flowLaunchIntent{}, flowPhaseResumeNoWorktreeStatus, false
	}
	intent := flowLaunchIntent{
		Kind:              flowLaunchKindPhaseResume,
		FlowID:            record.FlowID,
		PhaseID:           phase.PhaseID,
		Provider:          provider,
		ProviderSessionID: sessionID,
		ResumeCommand:     command,
		FallbackRepoPath:  fallbackRepoPath,
		ResumeContext: actions.AgentLaunchContext{
			Command:           command,
			RepoPath:          repoPath,
			WorktreePath:      record.WorktreePath,
			WorkingDir:        workingDir,
			Branch:            record.Branch,
			Commit:            record.Commit,
			SessionStateRoot:  m.sessionStateRoot,
			ResumeSessionID:   sessionID,
			PlanID:            record.PlanID,
			PlanPath:          record.PlanPath,
			FlowID:            record.FlowID,
			FlowPhaseID:       phase.PhaseID,
			FlowPhaseKind:     flowstore.SemanticKind(phase),
			FlowPhaseTerminal: flowstore.PhaseStatusTerminal(phase.Status),
		},
	}
	return intent, "", true
}

// flowPhaseResumeCommand applies the one preference that can move a resume off
// the session's own provider. Everything downstream reads the result, never the
// provider, so this mapping happens exactly once.
func flowPhaseResumeCommand(provider, agentCommand string) string {
	if provider == agent.CommandCodex && agent.Normalize(agentCommand) == agent.CommandCodexApp {
		return agent.CommandCodexApp
	}
	return provider
}

// resumableFlowPhaseSession applies the phase-shaped resumability rules and
// returns the session a resume would reattach to. The read stage re-runs it
// against the fresh phase, which is why it takes only a phase.
func resumableFlowPhaseSession(phase flowstore.FlowPhase) (flowstore.Session, string, bool) {
	if reason, ok := flowstore.RecoverableRunningPhaseResetReason(phase); ok {
		if reason == flowstore.PhaseResetReasonAwaitSession {
			return flowstore.Session{}, flowPhaseResumeAwaitingSessionStatus, false
		}
		return flowstore.Session{}, flowPhaseResumeResettableStatus, false
	}
	if phase.Status == flowstore.PhaseRunning && flowstore.PhaseAwaitingSession(phase) {
		return flowstore.Session{}, flowPhaseResumeAwaitingSessionStatus, false
	}
	if phase.Status == flowstore.PhaseRunning && flowstore.PhaseLatestLaunchEnded(phase) {
		return flowstore.Session{}, flowPhaseResumeEndedSessionStatus, false
	}
	// Deliberately re-asked without requiring a session ID. LatestPhaseSession
	// skips ID-less sessions, so a phase whose newest launch attached one would
	// otherwise silently resume the older, ID-bearing session instead of saying
	// so.
	if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
		return flowstore.Session{}, flowPhaseResumeMissingSessionIDStatus, false
	}
	session, ok := flowstore.LatestPhaseSession(phase, true)
	if !ok {
		return flowstore.Session{}, flowPhaseResumeNoSessionStatus, false
	}
	return session, "", true
}

// admitPhaseResumeFlowLaunch is resume's half of the lifecycle's admission.
// Unlike manual launch it never emits noLaunchableFlowPhaseStatus: the phases
// resume exists for are exactly the ones that string describes as unlaunchable.
// Every residual failure — an empty token, a lost race for the attempt map, a
// transition that no longer matches — refuses in silence, because none of them
// names a state the user could act on and the terminal status below would be
// actively false for them.
func (m Model) admitPhaseResumeFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	flowID := strings.TrimSpace(intent.FlowID)
	intent.FlowID = flowID
	if flowID == "" {
		return m, nil, false
	}
	if intent.ResumeCommand == agent.CommandCodexApp {
		// Codex App resume deep links cannot carry approach launch metadata, so
		// they stay app navigation rather than a tracked launch: no attempt, no
		// read, no phase write. admitted stays false because nothing holds the
		// Flow, and the caller returns the command regardless.
		return m, m.untrackedCodexAppFlowPhaseResumeCmd(intent.ResumeContext), false
	}
	if m.flowLaunchAdmissionOccupied(flowID) {
		// The two predicates overlap rather than nest: the broad one requires a
		// non-nil Terminal and ignores FlowRepair, the repair one requires
		// FlowRepair and ignores Terminal. A repair terminal satisfies both
		// disjuncts of admission occupancy and refuses silently today, so the
		// status is scoped to the non-repair case with the conjunction below.
		if m.hasFlowEmbeddedTerminalForFlow(flowID) && !m.hasFlowRepairEmbeddedTerminalForFlow(flowID) {
			return m.setStatus(statusOther, flowPhaseResumeTerminalStatus), nil, false
		}
		return m, nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m, nil, false
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	// The reservation names the phase up front, unlike manual launch, which
	// defers it because its read is what resolves the phase. Resume knows the
	// exact phase at the key press, and repair's attempt-kind query reads an
	// attempt that already names it.
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

// previewPhaseResume is the footer's half of D1, and it is deliberately
// narrower than flowLaunchAdmissionOccupied: a manual attempt, a pending
// repair, and an open repair terminal all refuse a resume silently by design,
// and withdrawing the key for them would be a behavior change this bead does
// not make. It gates on the retained-slot conjunction only, which is the case
// resume newly refuses out loud.
//
// It takes the resolved command rather than only the Flow ID because the
// codex-app bypass depends on the selected phase's session provider combined
// with the agent preference, which is not a function of the Flow.
func (m Model) previewPhaseResume(flowID, command string) bool {
	if agent.Normalize(command) == agent.CommandCodexApp {
		// codex-app is exempt from occupancy entirely, so withdrawing the key
		// would manufacture the advertisement/admission disagreement this
		// predicate exists to prevent.
		return true
	}
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return true
	}
	return !m.hasFlowEmbeddedTerminalForFlow(flowID) || m.hasFlowRepairEmbeddedTerminalForFlow(flowID)
}

// phaseResumeFlowLaunchReadCmd is the authoritative read for a phase resume. It
// deliberately does not call Preflight: that renders prompts, gates on
// launchable phases, and resolves paths by new-launch rules, none of which
// apply. Every value prepare needs is resolved here instead.
//
// There is no closed-Flow check either. The key handler still refuses on the
// snapshot, and the authoritative refusal belongs to the prepare stage, which
// already has it: the cross-process reservation reads under the launch/close
// lock and AddPhaseLaunchID re-checks. A third, non-authoritative check here
// would only change what the close-between-press-and-read race shows the user.
func phaseResumeFlowLaunchReadCmd(seams flowLaunchSeams, intent flowLaunchIntent, token string) tea.Cmd {
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
		phase, ok := flowPhaseByID(record, intent.PhaseID)
		if !ok {
			// A vanished phase and a changed session are one refusal class to
			// the user, so the string is cause-neutral rather than
			// session-flavored.
			event.Err = flowPhaseResumeDriftStatus
			return event
		}
		if status, ok := phaseResumeRefusal(phase, intent); !ok {
			event.Err = status
			return event
		}
		records, err := seams.ListFlowSessions(intent.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if flowLaunchPhaseSessionOccupiedExcept(phase, records, intent.resumeSessionIdentity()) {
			event.Err = flowPhaseResumeLiveSessionStatus
			return event
		}
		// Paths resolve the way resume does, not the way Preflight does: no
		// worktree fallback to the repo path, and no PlanMarkdownPath
		// resolution. The worktree re-check is unconditional here, unlike the
		// resolver's, which is gated on the command — codex-app short-circuits
		// at admission and never reaches this stage.
		if record.WorktreePath == "" {
			event.Err = flowPhaseResumeNoWorktreeStatus
			return event
		}
		repoPath := record.RepoPath
		if repoPath == "" {
			repoPath = intent.FallbackRepoPath
		}
		event.PhaseID = phase.PhaseID
		event.Record = record
		event.RepoPath = repoPath
		event.WorktreePath = record.WorktreePath
		event.PlanPath = record.PlanPath
		event.ProviderSessionID = intent.ProviderSessionID
		event.ResumeCommand = intent.ResumeCommand
		return event
	}
}

// phaseResumeRefusal re-runs the resumability rules against the fresh phase and
// adds the one check the snapshot cannot make: that the session the intent
// named is still the session this phase would resume.
func phaseResumeRefusal(phase flowstore.FlowPhase, intent flowLaunchIntent) (string, bool) {
	session, status, ok := resumableFlowPhaseSession(phase)
	if !ok {
		return status, false
	}
	// Drift and occupancy ask the same question — is this the session the intent
	// named — so they share one identity rule rather than restating it: providers
	// compare normalized, session IDs byte-exact.
	if !intent.resumeSessionIdentity().matches(session.Provider, session.SessionID) {
		return flowPhaseResumeDriftStatus, false
	}
	return "", true
}

// phaseResumeFlowLaunchPrepareCmd takes the cross-process reservation, marks
// the resume, and builds the launch context. It is a Model method because it
// needs the reservation and the phase-write seam.
//
// Both failures here reach the user through failFlowLaunch's nothing-written
// branch, which is repo-gated: the old plumbing set the status unconditionally,
// so a user who changes the selected repo while the write is in flight now sees
// nothing where they once saw "failed to mark flow phase resume". That is the
// price of one failure path rather than two, and it is the same gate manual and
// automatic launches have always used.
func (m Model) phaseResumeFlowLaunchPrepareCmd(msg flowLaunchEventMsg, settings flowLaunchAgentSettingsSnapshot) tea.Cmd {
	readPhase, _ := flowPhaseByID(msg.Record, msg.PhaseID)
	reserve := m.reserveTrackedFlowLaunch
	addPhaseLaunchID := m.launchSeams.AddPhaseLaunchID
	sessionStateRoot := settings.SessionStateRoot
	// A resume does not go through FlowPhaseLauncher.Prepare, so the tmux
	// decision is made here instead. It is taken on the Model, before the
	// closure runs, for the same reason the launcher snapshots it at admission:
	// the route this launch takes must be the one it was admitted with. A resume
	// is interactive by construction and never a repair, so the command is the
	// whole input.
	tmuxRoute, tmuxFellBack := m.tmuxLaunchRoute(actions.AgentLaunchContext{Command: msg.ResumeCommand})
	return func() tea.Msg {
		event := msg
		event.Stage = flowLaunchStagePrepared
		event.From = flowLaunchStatePreparing
		// The reserved record is discarded, as it is for every tracked launch:
		// the read stage's drift check and this write are ordered but not one
		// transaction, so a peer process can still land a resume in between.
		// Closing that window belongs to AddPhaseLaunchID, which owns the
		// expected-session condition for manual and automatic launches too;
		// re-checking here would only move the race, not remove it.
		_, release, reserveErr := reserve(msg.FlowID)
		if reserveErr != nil {
			event.Err = fmt.Sprintf("failed to mark flow phase resume: %v", reserveErr)
			return event
		}
		// Handing the release to the event is what lets the handler drop the
		// advisory launch/close lock. Losing it fails silently: every later
		// launch, close, repair, or resume on this Flow would block until the
		// lock timed out, in this process and in peers.
		event.Release = release
		updated, err := addPhaseLaunchID(flowstore.PhaseLaunchUpdate{
			FlowID:   msg.FlowID,
			PhaseID:  msg.PhaseID,
			LaunchID: msg.Token,
			Resume:   true,
		})
		if err != nil {
			event.Err = fmt.Sprintf("failed to mark flow phase resume: %v", err)
			return event
		}
		// The write's record decides whether this resume preserved a terminal
		// phase or reopened a running one, and that flag is what stops a failed
		// resume from regressing a completed phase. The kind is taken from the
		// same phase rather than the key press's, which is wider than the old
		// plumbing and deliberate: both values feed failure handling, and
		// splitting their sources would let one describe a phase the other no
		// longer does. The guard matters: seams routinely return phase-less
		// records, and an unguarded lookup would silently answer "not terminal"
		// for every one of them.
		launchPhase := readPhase
		if persistedPhase, ok := flowPhaseByID(updated, msg.PhaseID); ok {
			launchPhase = persistedPhase
		}
		event.Context = actions.AgentLaunchContext{
			Command: msg.ResumeCommand,
			// The admission token, never a fresh ID: the prefill-failure
			// re-reservation and the failure-persisted fence both key on it.
			LaunchID:     msg.Token,
			RepoPath:     msg.RepoPath,
			WorktreePath: msg.WorktreePath,
			WorkingDir:   msg.Record.WorktreePath,
			Branch:       msg.Record.Branch,
			Commit:       msg.Record.Commit,
			// Model and ReasoningEffort stay empty, as they are today. The
			// settings snapshot is in scope, and setting them would silently
			// change the resumed command line.
			SessionStateRoot:  sessionStateRoot,
			ResumeSessionID:   msg.ProviderSessionID,
			PlanID:            msg.Record.PlanID,
			PlanPath:          msg.PlanPath,
			FlowID:            msg.Record.FlowID,
			FlowPhaseID:       msg.PhaseID,
			FlowPhaseKind:     flowstore.SemanticKind(launchPhase),
			FlowPhaseTerminal: flowstore.PhaseStatusTerminal(launchPhase.Status),
			Embedded:          true,
			FlowLaunchTracked: true,
		}
		event.Route = flowLaunchRouteEmbedded
		if tmuxRoute {
			// A tmux window has no dock to prefill and renders its own output,
			// so clearing Embedded is what sends the resume to argv instead.
			event.Context.Embedded = false
			event.Route = flowLaunchRouteTmux
		} else if tmuxFellBack {
			event.FallbackNote = tmuxFallbackNote
		}
		return event
	}
}
