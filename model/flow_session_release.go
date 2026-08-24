package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/modal"
)

// Session release finishes what a crash interrupted. FinalizeAgentSession is
// exactly the call that was skipped when Approach died mid-phase, so release
// makes it by hand for the launches nothing can end any more, and the phase
// comes back to the state a clean exit would have left it in.
//
// Both halves of that call no-op silently when they find nothing — MarkLaunchEnded
// on a launch the session store never recorded, MarkPhaseLaunchEnded on a mirror
// row that does not match — which is what makes one call per launch correct for
// a stall in the mirror, in the store, or in both. It also means success proves
// nothing was written, which is why the result reports how many launches were
// selected rather than claiming a write.
const (
	flowPhaseSessionReleaseNoneStatus     = "Flow phase has no unfinished session to release"
	flowPhaseSessionReleaseTerminalStatus = "Dismiss this phase's Flow terminal before releasing its session"
	flowPhaseSessionReleaseTmuxStatus     = "Exit this phase's agent in tmux (T attaches) before releasing its session"
	flowPhaseSessionReleaseClosedStatus   = "Flow is closed; reopen it with C before releasing a session"
	// Cause-neutral because it covers two of them: a launch the lifecycle is
	// holding, and a headless mode toggle still being written.
	flowPhaseSessionReleaseBusyStatus = "Flow has a write in flight; release the session once it settles"
)

// flowPhaseSessionReleasePromptBudget is what renderConfirmDialog can show at
// the narrowest width the renderer assumes. It centres one line without
// wrapping or truncating it, so a longer prompt is wrapped by the terminal and
// pushes the status bar out of the frame.
//
// ui.Render defaults an unset width to 80, which is the only width floor the
// renderer states; 76 leaves two columns of padding on each side of a prompt
// centred in it.
const flowPhaseSessionReleasePromptBudget = 76

// flowPhaseSessionReleaseLaunchSuffixLen matches the trailing-ID form tmux
// window names already use, so a launch named in the prompt is recognizable in
// the window list.
const flowPhaseSessionReleaseLaunchSuffixLen = 8

// flowPhaseSessionReleasable is the footer's half of the gesture, and it is
// deliberately much narrower than the action. The hint is a heuristic, not a
// proof: nothing cheap can tell a stalled session from a working one outside
// the embedded backend, so the question it answers is only "is a live session
// here surprising enough to point at".
//
// It is answered by status, and `ready` is the only status that answers yes. A
// live session is unsurprising on every other one:
//
//   - running is the state a working agent is supposed to be in.
//   - completed, blocked, and needs_attention are all set by the agent itself,
//     from inside its own live session, by `approach flow phase complete`,
//     `block`, and `needs-attention`. The window between that call and the
//     agent exiting is ordinary — it is what every Flow phase passes through —
//     and flow_repair.go says so where it treats a matched live session as
//     active work whatever the persisted status says.
//   - skipped is settable the same way through `approach flow phase set`.
//
// On a ready phase a live session is at least inconsistent: the phase says
// nothing has started. Even that is not impossible — a provider whose hook
// records `ended` per turn can have its phase reset to ready under a still-open
// agent, and the next turn re-marks the record live — which is why the press
// path probes tmux and the confirmation calls the sessions unverified rather
// than trusting this. Advertising is not the guard; it is the invitation, and
// this keeps it off the states where the invitation would be wrong.
//
// Stalls on the other statuses are still recoverable — x probes on demand —
// they are just not advertised.
//
// It is mirror-only, and cheap enough to run per render: an in-memory walk of
// the phase's sessions and no store I/O. Variant B — a live record the mirror
// never saw — is therefore invisible here, and reachable only through the
// press-time probe.
//
// "Not resettable today" needs no clause of its own: reset routes through
// RecoverableRunningPhaseResetReason, which is false unless the phase is
// running, so on a ready phase reset is never eligible and the two gestures
// cannot both apply.
func (m Model) flowPhaseSessionReleasable(record flowstore.FlowRecord, phase flowstore.FlowPhase) bool {
	if phase.Status != flowstore.PhaseReady {
		return false
	}
	return phaseHasMatchingLiveSession(phase) && m.flowPhaseSessionReleaseAllowed(record, phase)
}

// flowPhaseSessionReleaseAllowed is the safety half, shared by the footer, the
// press gate, and both asynchronous hops so a modal is never opened only to be
// refused one keypress later. Everything in it is model-local and free.
func (m Model) flowPhaseSessionReleaseAllowed(record flowstore.FlowRecord, phase flowstore.FlowPhase) bool {
	return m.flowPhaseSessionReleaseRefusal(record, phase) == ""
}

// flowPhaseSessionReleaseRefusal names the model-local condition that stops a
// release, or "" when none does. The asynchronous hops re-check it and say what
// they found: a user who answered a confirmation is owed an answer, and a hop
// that returned silently there looked exactly like a dropped keypress.
func (m Model) flowPhaseSessionReleaseRefusal(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	switch {
	case m.hasRunningFlowEmbeddedTerminalForPhase(record.FlowID, phase.PhaseID):
		return flowPhaseSessionReleaseTerminalStatus
	case flowstore.FlowClosed(record):
		return flowPhaseSessionReleaseClosedStatus
	// A release must not act while the launch lifecycle holds this Flow, or
	// while a headless write is in flight: both can be about to persist a
	// launch ID the probe has not seen.
	case m.sessionReleaseRuntimeAdvice(record.FlowID).Defer():
		return flowPhaseSessionReleaseBusyStatus
	}
	return ""
}

// flowPhaseSessionReleaseProbeAllowed gates the store walk the key press costs.
// It deliberately omits the mirror-only predicate — that is what would make a
// store-only stall unreachable — and adds the one clause the predicate implies
// rather than states: a phase with no launch IDs can own no session, and
// ListFlowSessions lists every record in the store before filtering.
func (m Model) flowPhaseSessionReleaseProbeAllowed(record flowstore.FlowRecord, phase flowstore.FlowPhase) bool {
	return phaseHasLaunchID(phase) && m.flowPhaseSessionReleaseAllowed(record, phase)
}

// flowPhaseSessionReleasePressRefusal is what a gated press says out loud. A
// press on a phase nothing is visibly wrong with stays silent, exactly as it did
// before this gesture existed — only the probe can judge that case, and it has
// not run. But when the mirror already shows the stall the user is reacting to,
// silence is the press looking dropped, so the blocker is named instead.
func (m Model) flowPhaseSessionReleasePressRefusal(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	if !phaseHasMatchingLiveSession(phase) {
		return ""
	}
	refusal := m.flowPhaseSessionReleaseRefusal(record, phase)
	// An attached terminal on a running phase is the healthy state, not a
	// blocker worth naming: "dismiss this phase's Flow terminal" beside a
	// working agent reads as a nudge to kill it. The refusal still holds — the
	// worth-announcing case is the resume one, where a stale launch sits beside
	// a live one on a phase that is no longer running.
	if refusal == flowPhaseSessionReleaseTerminalStatus && phase.Status == flowstore.PhaseRunning {
		return ""
	}
	return refusal
}

func phaseHasLaunchID(phase flowstore.FlowPhase) bool {
	for _, launchID := range phase.LaunchIDs {
		if strings.TrimSpace(launchID) != "" {
			return true
		}
	}
	return false
}

// handleReleaseSelectedFlowPhaseSession is where x lands when the phase is not
// resettable. A press that was never a release candidate stays silent, exactly
// as it did before this gesture existed; the named refusal is for the other
// case, where the probe ran and found nothing.
func (m Model) handleReleaseSelectedFlowPhaseSession() (tea.Model, tea.Cmd) {
	record, phase, repoPath, ok := m.selectedFlowPhaseTarget()
	if !ok {
		return m, nil
	}
	if !m.flowPhaseSessionReleaseProbeAllowed(record, phase) {
		if refusal := m.flowPhaseSessionReleasePressRefusal(record, phase); refusal != "" {
			return m.setStatus(statusOther, refusal), nil
		}
		return m, nil
	}
	return m, m.flowPhaseSessionReleaseProbeCmd(repoPath, record.FlowID, phase.PhaseID)
}

// flowPhaseSessionReleaseProbeCmd reads the authoritative both-halves answer.
// It goes through the launch seams rather than the 1 Hz-refreshed pane cache so
// the mirror half it unions is as fresh as the store half.
func (m Model) flowPhaseSessionReleaseProbeCmd(repoPath, flowID, phaseID string) tea.Cmd {
	seams := m.launchSeams
	return func() tea.Msg {
		msg := flowPhaseSessionReleaseProbedMsg{RepoPath: repoPath, FlowID: flowID, PhaseID: phaseID}
		launchIDs, err := liveFlowPhaseLaunchIDs(seams, flowID, phaseID)
		if err != nil {
			msg.Err = err.Error()
			return msg
		}
		msg.LaunchIDs = launchIDs
		return msg
	}
}

func liveFlowPhaseLaunchIDs(seams flowLaunchSeams, flowID, phaseID string) ([]string, error) {
	record, err := seams.ReadFlow(flowID)
	if err != nil {
		return nil, err
	}
	phase, ok := flowRecordPhaseByID(record, phaseID)
	if !ok {
		return nil, nil
	}
	records, err := seams.ListFlowSessions(flowID)
	if err != nil {
		return nil, err
	}
	return liveLaunchIDsForPhase(phase, records), nil
}

// handleFlowPhaseSessionReleaseProbed opens the confirmation, or says why it
// will not. The staleness fence is the one handleEmbeddedSessionPickerLoaded
// already establishes for a read that ends in a modal: a probe is a store walk,
// and by the time it lands the user may have navigated to another phase or
// opened another modal, or left the Flow surface altogether.
//
// That last one needs its own clause. selectedFlowPhaseTarget reads the
// retained Flow pane, which keeps resolving the old selection after the user
// switches the stored mode away from Flows, so the target check alone would
// still match and open a release confirmation over an unrelated view. The
// press is gated on flowSurfaceVisible; its result has to land there too.
func (m Model) handleFlowPhaseSessionReleaseProbed(msg flowPhaseSessionReleaseProbedMsg) (tea.Model, tea.Cmd) {
	if !m.flowSurfaceVisible() {
		return m, nil
	}
	if !m.takeoverVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if m.modal.IsOpen() {
		return m, nil
	}
	record, phase, _, ok := m.selectedFlowPhaseTarget()
	if !ok || record.FlowID != msg.FlowID || phase.PhaseID != msg.PhaseID {
		return m, nil
	}
	if strings.TrimSpace(msg.Err) != "" {
		return m.setStatus(statusOther, msg.Err), nil
	}
	if refusal := m.flowPhaseSessionReleaseRefusal(record, phase); refusal != "" {
		return m.setStatus(statusOther, refusal), nil
	}
	if len(msg.LaunchIDs) == 0 {
		return m.setStatus(statusOther, flowPhaseSessionReleaseNoneStatus), nil
	}
	launchIDs := append([]string(nil), msg.LaunchIDs...)
	prompt := flowPhaseSessionReleasePrompt(len(launchIDs), launchIDs[0], m.autoAdvanceDrainArmed(record.FlowID))
	m.modal = modal.OpenConfirm(prompt, func() tea.Cmd {
		return func() tea.Msg {
			return flowPhaseSessionReleaseConfirmedMsg{
				RepoPath:  msg.RepoPath,
				FlowID:    msg.FlowID,
				PhaseID:   msg.PhaseID,
				LaunchIDs: launchIDs,
			}
		}
	})
	return m, nil
}

// flowPhaseSessionReleasePrompt states what Approach knows and what it does not.
// Neither probe can see an externally launched agent, so the user's assertion is
// the last guard — and AC3 asks that it be informed: the count, one launch, and
// the consequence. The relaunch clause is gated on the drain actually being
// armed for this Flow, not on the record's AutoMode flag, because the drain is
// disarmed in every case but the one where release really does hand the phase
// straight back to the next poll.
//
// Armed is still Flow-wide: the drain picks its own candidate when it runs, and
// on a phase that was not blocking one — x on a completed phase whose successor
// the drain is already queued for — the launch that follows is not this phase's.
// So the clause claims neither the phase nor the certainty.
func flowPhaseSessionReleasePrompt(count int, launchID string, autoRelaunch bool) string {
	noun := "sessions"
	if count == 1 {
		noun = "session"
	}
	var named string
	if suffix := flowPhaseSessionReleaseLaunchSuffix(launchID); suffix != "" {
		named = fmt.Sprintf(" (…%s)", suffix)
	}
	tail := " Recorded as ended."
	if autoRelaunch {
		// A phase, not the session: the drain launches whichever phase it finds
		// launchable, and "it" beside a plural count binds to the wrong noun
		// anyway.
		tail = " AutoMode may launch a phase."
	}
	return fmt.Sprintf("Release %d unverified %s%s?%s", count, noun, named, tail)
}

func flowPhaseSessionReleaseLaunchSuffix(launchID string) string {
	launchID = strings.TrimSpace(launchID)
	if len(launchID) > flowPhaseSessionReleaseLaunchSuffixLen {
		launchID = launchID[len(launchID)-flowPhaseSessionReleaseLaunchSuffixLen:]
	}
	return launchID
}

// handleFlowPhaseSessionReleaseConfirmed re-checks the safety guards, never the
// eligibility. The mirror-only predicate is false for a store-only stall the
// probe just proved releasable, so authority over what is releasable belongs to
// the release command's own mirror ∪ store read.
//
// Refusal is phase-wide by design. A phase can legitimately carry a live launch
// beside a stale one — resume persists a new launch ID — and finalizing "every
// live launch" would record the working agent as ended and free its worktree for
// a second one. While any agent is provably live on the phase, release refuses
// entirely.
func (m Model) handleFlowPhaseSessionReleaseConfirmed(msg flowPhaseSessionReleaseConfirmedMsg) (Model, tea.Cmd) {
	if !m.takeoverVisible() && !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	record, phase, ok := m.flowPhaseByID(msg.FlowID, msg.PhaseID)
	if !ok || len(msg.LaunchIDs) == 0 {
		return m.setStatus(statusOther, flowPhaseSessionReleaseNoneStatus), nil
	}
	if refusal := m.flowPhaseSessionReleaseRefusal(record, phase); refusal != "" {
		return m.setStatus(statusOther, refusal), nil
	}
	// Release makes the phase launchable again, so in tmux mode this is where
	// the live-window probe belongs: it is a subprocess, and it runs once, on a
	// confirmation, never in a render-path predicate.
	if m.tmuxPhaseAgentStillRunning(record, releaseTmuxProbePhase(phase, msg.LaunchIDs), msg.RepoPath) {
		return m.setStatus(statusOther, flowPhaseSessionReleaseTmuxStatus), nil
	}
	return m, m.releaseFlowPhaseSessionsCmd(msg)
}

// releaseTmuxProbePhase widens the tmux probe to cover every launch release
// could finalize. The confirmed set came from the probe's store read; the phase
// comes from the pane, which can be a refresh behind it, so probing the pane's
// launch IDs alone would let a launch that landed in between be finalized
// without its window ever being checked — the working agent the phase-wide
// refusal exists to protect.
//
// It is a union rather than a replacement because the pane half carries launches
// the probe deliberately dropped: a launch whose session records say ended can
// still own a live window, and one list-windows answers for all of them.
func releaseTmuxProbePhase(phase flowstore.FlowPhase, confirmed []string) flowstore.FlowPhase {
	seen := make(map[string]struct{}, len(phase.LaunchIDs)+len(confirmed))
	launchIDs := make([]string, 0, len(phase.LaunchIDs)+len(confirmed))
	add := func(values []string) {
		for _, launchID := range values {
			if launchID = strings.TrimSpace(launchID); launchID == "" {
				continue
			}
			if _, ok := seen[launchID]; ok {
				continue
			}
			seen[launchID] = struct{}{}
			launchIDs = append(launchIDs, launchID)
		}
	}
	add(phase.LaunchIDs)
	add(confirmed)
	phase.LaunchIDs = launchIDs
	return phase
}

// releaseFlowPhaseSessionsCmd intersects the confirmed set with what is still
// live; it never recomputes. A recompute would pick up a launch that started
// after the modal opened — including one the user started themselves — which is
// exactly the displacement the phase-wide refusal exists to prevent. A launch
// that ended meanwhile simply falls out of the intersection.
func (m Model) releaseFlowPhaseSessionsCmd(msg flowPhaseSessionReleaseConfirmedMsg) tea.Cmd {
	seams := m.launchSeams
	finalize := m.finalizeAgentSession
	confirmed := append([]string(nil), msg.LaunchIDs...)
	return func() tea.Msg {
		released := 0
		// Released rides along on the failure too. Finalization is one call per
		// launch and the loop stops at the first error, so a partial release is
		// reachable, and a user told only "failed" would retry against a phase
		// that has already changed underneath them.
		//
		// Finalized says which failure it was, because Released cannot: the
		// finalizer is two writes — the session store, then the phase mirror —
		// and a failure on the second one leaves the launch ended in the store
		// with Released still on its old value. Only a failure before any
		// finalize call is provably inert.
		fail := func(err error, finalized bool) tea.Msg {
			return flowPhaseSessionReleaseFailedMsg{
				RepoPath:  msg.RepoPath,
				FlowID:    msg.FlowID,
				PhaseID:   msg.PhaseID,
				Released:  released,
				Finalized: finalized,
				Err:       fmt.Sprintf("failed to release Flow phase session: %v", err),
			}
		}
		live, err := liveFlowPhaseLaunchIDs(seams, msg.FlowID, msg.PhaseID)
		if err != nil {
			return fail(err, false)
		}
		stillLive := make(map[string]struct{}, len(live))
		for _, launchID := range live {
			stillLive[launchID] = struct{}{}
		}
		for _, launchID := range confirmed {
			// The same trimmed value the intersection matched on. Both halves of
			// finalization compare trimmed, so this is what makes the ID that is
			// finalized and the ID that was found live provably the same one.
			launchID = strings.TrimSpace(launchID)
			if _, ok := stillLive[launchID]; !ok {
				continue
			}
			if err := finalize(actions.AgentLaunchContext{
				LaunchID:    launchID,
				FlowID:      msg.FlowID,
				FlowPhaseID: msg.PhaseID,
			}); err != nil {
				return fail(err, true)
			}
			released++
		}
		return flowPhaseSessionReleasedMsg{
			RepoPath: msg.RepoPath,
			FlowID:   msg.FlowID,
			PhaseID:  msg.PhaseID,
			Released: released,
		}
	}
}
