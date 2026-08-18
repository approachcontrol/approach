package launchcontrol

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/flowlease"
)

// ExitSource names where exit evidence came from.
type ExitSource string

const (
	SourceTerminalExit    ExitSource = "terminal_exit"
	SourceLeaseRunnerExit ExitSource = "lease_runner_exit"
	SourceSessionEnd      ExitSource = "session_end"
	SourceStartupSweep    ExitSource = "startup_sweep"
	SourcePeriodicSweep   ExitSource = "periodic_sweep"
)

// ExitEvidence is what a caller knows about a launch's end. It enters this
// package as a value: the embedded terminal (model), the session hook
// (sessions), and the lease runner's exit.json each produce one.
type ExitEvidence struct {
	Source    ExitSource
	Code      int
	CodeKnown bool
	EndedAt   time.Time
	Detail    string
}

// SessionEndGrace is how long after a session record ended Reconcile and
// the sweep wait before treating that record alone as exit evidence. Codex
// Stop is per-turn and Claude's SessionEnd fires on /clear with the agent
// still alive; the lease veto covers tracked tmux launches, and the grace
// covers the rest — including the default embedded backend, which never
// acquires a lease.
const SessionEndGrace = 10 * time.Minute

// Outcome reports what Reconcile did.
type Outcome struct {
	// Action is one of: none, demoted, vetoed.
	Action string
	// Status is the phase status after the call.
	Status string
	Reason string
	// Replayed counts spooled requests applied first.
	Replayed int
	Notices  []string
}

const (
	ActionNone    = "none"
	ActionDemoted = "demoted"
	ActionVetoed  = "vetoed"
)

// Reconcile is the exit-evidence entry point. It replays the launch's
// pending requests first (a landed result means no demotion), then demotes
// the phase only when it is still `running` under this exact launch and the
// Flow lease is not held. Session-end evidence additionally waits
// SessionEndGrace: a hook is not a death certificate, and ordinary tmux
// plus the default embedded backend never hold the lease that would
// otherwise veto them. Everything goes through Execute.
func (c *Controller) Reconcile(flowID, phaseID, launchID string, ev ExitEvidence) (Outcome, error) {
	if !artifacts.IsSafeID(launchID) {
		return Outcome{Action: ActionNone}, fmt.Errorf("launch control refuses unsafe launch id %q", launchID)
	}
	lock := c.launchLock(launchID)
	lock.Lock()
	defer lock.Unlock()
	log, err := OpenLog(c.root, launchID)
	if err != nil {
		return Outcome{Action: ActionNone}, err
	}
	phaseID = artifacts.NormalizePhaseID(phaseID)
	if ev.Source == SourceTerminalExit {
		// Authoritative evidence is durable before anything that can fail
		// transiently — the launch lock (which the SessionEnd hook may hold),
		// the store — so a reconciliation that does not finish is retried by
		// the sweep from exit.json rather than lost with the terminal slot.
		endedAt := ev.EndedAt
		if endedAt.IsZero() {
			endedAt = c.now()
		}
		if err := log.WriteExit(ExitRecord{
			FlowID: flowID, PhaseID: phaseID, ExitCode: ev.Code, CodeUnknown: !ev.CodeKnown,
			EndedAt: endedAt.UTC(), Source: string(ev.Source),
		}); err != nil {
			return Outcome{Action: ActionNone}, err
		}
	}
	unlock, err := log.Lock(LaunchLockTimeout)
	if err != nil {
		return Outcome{Action: ActionNone}, err
	}
	defer unlock()
	return c.reconcileLocked(log, flowID, phaseID, launchID, ev)
}

func (c *Controller) reconcileLocked(log *Log, flowID, phaseID, launchID string, ev ExitEvidence) (Outcome, error) {
	outcome := Outcome{Action: ActionNone}
	replay, err := c.replayLocked(log)
	if err != nil {
		return outcome, err
	}
	outcome.Replayed = replay.Applied
	outcome.Notices = append(outcome.Notices, replay.Notices...)
	if len(replay.Notices) > 0 {
		_ = log.WriteNotice(replay.Notices)
	}
	record, err := c.store.Read(flowID)
	if err != nil {
		if flowstore.IsNotFound(err) {
			return outcome, nil
		}
		return outcome, err
	}
	phase, ok := PhaseByID(record, phaseID)
	if !ok {
		return outcome, nil
	}
	outcome.Status = phase.Status
	if phase.Status != flowstore.PhaseRunning {
		return outcome, nil
	}
	if flowstore.LatestPhaseLaunchID(phase) != launchID {
		return outcome, nil
	}
	if ev.Source != SourceTerminalExit {
		state, err := c.inspectLease(c.root, flowID)
		if err == nil && state == flowlease.Held {
			outcome.Action = ActionVetoed
			return outcome, nil
		}
	}
	if ev.Source == SourceSessionEnd && !sessionEndAged(c.now(), ev.EndedAt) {
		return outcome, nil
	}
	update := reconcileUpdate(phase, ReasonPhaseResultMissing, &ev, flowID, launchID, "")
	seq := 0
	if all, err := log.Requests(); err == nil && len(all) > 0 {
		seq = all[len(all)-1].Seq
	}
	resp, err := c.demote(log, flowID, launchID, update, seq, c.now())
	if err != nil {
		return outcome, err
	}
	if !resp.OK {
		return outcome, errors.New(resp.Error)
	}
	outcome.Action = ActionDemoted
	outcome.Status = update.Status
	outcome.Reason = ReasonPhaseResultMissing
	return outcome, nil
}

// RecoveryCommand is the one command every demotion's notes end with.
func RecoveryCommand(flowID, phaseID, reason string) string {
	return fmt.Sprintf("approach flow phase set --flow-id %s --phase-id %s --status running --notes %q", flowID, phaseID, reason)
}

// reconcileUpdate builds the demotion write for a phase. Plan-review kinds
// take the tree's existing "the agent did not run" convention — blocked with
// outcome blocked and the reason leading the notes — because blocked is an
// accepted review outcome whose meaning matches and is not a verdict. Every
// other kind takes needs_attention with the reason as the outcome.
func reconcileUpdate(phase flowstore.FlowPhase, reason string, ev *ExitEvidence, flowID, launchID, intended string) flowstore.PhaseUpdate {
	var detail strings.Builder
	fmt.Fprintf(&detail, "%s: launch %s ", reason, launchID)
	if ev != nil {
		code := "exit code unknown"
		if ev.CodeKnown {
			code = fmt.Sprintf("exit code %d", ev.Code)
		}
		fmt.Fprintf(&detail, "exited (%s, %s) without a valid result for phase %s", ev.Source, code, phase.PhaseID)
	} else {
		fmt.Fprintf(&detail, "reported a result for phase %s that no longer matches the phase", phase.PhaseID)
	}
	if intended != "" {
		fmt.Fprintf(&detail, "; intended %s, observed %s", intended, phase.Status)
	} else {
		fmt.Fprintf(&detail, "; observed %s", phase.Status)
	}
	fmt.Fprintf(&detail, ". Recover with: %s", RecoveryCommand(flowID, phase.PhaseID, reason))
	update := flowstore.PhaseUpdate{FlowID: flowID, PhaseID: phase.PhaseID, Notes: detail.String()}
	if flowstore.SemanticKind(phase) == flowstore.KindPlanReview {
		update.Status = flowstore.PhaseBlocked
		update.Outcome = flowstore.OutcomeBlocked
		return update
	}
	update.Status = flowstore.PhaseNeedsAttention
	update.Outcome = reason
	return update
}

// SweepReport summarizes one sweep.
type SweepReport struct {
	Launches   int
	Replayed   int
	Reconciled int
	Notices    []string
}

// Sweep is the periodic backstop: for every launch directory, replay first,
// then demote on positive exit evidence — exit.json, or a session record the
// probe says ended more than SessionEndGrace ago — never while the Flow lease
// is held. It also runs retention once a day.
func (c *Controller) Sweep() SweepReport {
	report := c.sweep(SourcePeriodicSweep)
	c.mu.Lock()
	due := c.lastRetain.IsZero() || c.now().Sub(c.lastRetain) >= retentionInterval
	c.mu.Unlock()
	if due {
		if _, err := c.Retain(); err != nil {
			c.logf("retention: %v", err)
		}
	}
	return report
}

func (c *Controller) sweep(source ExitSource) SweepReport {
	var report SweepReport
	ids, err := ListLaunchIDs(c.root)
	if err != nil {
		c.logf("sweep: %v", err)
		return report
	}
	for _, id := range ids {
		report.Launches++
		notices, replayed, reconciled, err := c.sweepLaunch(id, source)
		report.Notices = append(report.Notices, notices...)
		report.Replayed += replayed
		if reconciled {
			report.Reconciled++
		}
		if err != nil {
			c.logf("sweep launch %s: %v", id, err)
		}
	}
	for _, notice := range report.Notices {
		c.logf("%s", notice)
	}
	return report
}

func (c *Controller) sweepLaunch(launchID string, source ExitSource) (notices []string, replayed int, reconciled bool, err error) {
	lock := c.launchLock(launchID)
	lock.Lock()
	defer lock.Unlock()
	log, err := OpenLog(c.root, launchID)
	if err != nil {
		return nil, 0, false, err
	}
	unlock, err := log.Lock(LaunchLockTimeout)
	if err != nil {
		return []string{fmt.Sprintf("launch %s: sequence lock is busy; retrying next sweep", launchID)}, 0, false, nil
	}
	defer unlock()
	replay, err := c.replayLocked(log)
	if err != nil {
		return nil, 0, false, err
	}
	notices = append(notices, replay.Notices...)
	if len(replay.Notices) > 0 {
		_ = log.WriteNotice(replay.Notices)
	}
	replayed = replay.Applied
	info, ok, err := log.Launch()
	if err != nil {
		return notices, replayed, replay.Reconciled, err
	}
	flowID, phaseID := info.FlowID, artifacts.NormalizePhaseID(info.PhaseID)
	if !ok || flowID == "" {
		requests, _ := log.Requests()
		if len(requests) == 0 {
			return notices, replayed, replay.Reconciled, nil
		}
		flowID, phaseID = requests[0].FlowID, requests[0].PhaseID
	}
	if flowID == "" || phaseID == "" {
		return notices, replayed, replay.Reconciled, nil
	}
	// The phase first, the evidence second: the liveness probe walks every
	// session record, and the retained history of finished launches must not
	// pay for it every tick. Only a phase still running under this launch
	// can be demoted, so only that launch is worth probing.
	record, err := c.store.Read(flowID)
	if err != nil {
		if flowstore.IsNotFound(err) {
			return notices, replayed, replay.Reconciled, nil
		}
		return notices, replayed, replay.Reconciled, err
	}
	phase, ok := PhaseByID(record, phaseID)
	if !ok || phase.Status != flowstore.PhaseRunning || flowstore.LatestPhaseLaunchID(phase) != launchID {
		return notices, replayed, replay.Reconciled, nil
	}
	ev, ok := c.exitEvidence(log, launchID, source)
	if !ok {
		return notices, replayed, replay.Reconciled, nil
	}
	if state, err := c.inspectLease(c.root, flowID); err == nil && state == flowlease.Held {
		return notices, replayed, replay.Reconciled, nil
	}
	update := reconcileUpdate(phase, ReasonPhaseResultMissing, &ev, flowID, launchID, "")
	seq := 0
	if all, err := log.Requests(); err == nil && len(all) > 0 {
		seq = all[len(all)-1].Seq
	}
	resp, err := c.demote(log, flowID, launchID, update, seq, c.now())
	if err != nil {
		return notices, replayed, replay.Reconciled, err
	}
	if !resp.OK {
		notices = append(notices, fmt.Sprintf("launch %s: could not mark phase %s after exit: %s", launchID, phaseID, resp.Error))
		return notices, replayed, replay.Reconciled, nil
	}
	notices = append(notices, fmt.Sprintf("launch %s: phase %s of flow %s marked %s (%s)", launchID, phaseID, flowID, update.Status, ReasonPhaseResultMissing))
	return notices, replayed, true, nil
}

// exitEvidence applies the sweep's evidence rules in precedence order.
func (c *Controller) exitEvidence(log *Log, launchID string, source ExitSource) (ExitEvidence, bool) {
	if exit, ok, err := log.Exit(); err == nil && ok {
		return ExitEvidence{Source: source, Code: exit.ExitCode, CodeKnown: !exit.CodeUnknown, EndedAt: exit.EndedAt,
			Detail: "exit.json from " + exit.Source}, true
	}
	if c.liveness == nil {
		return ExitEvidence{}, false
	}
	liveness, err := c.liveness(launchID)
	if err != nil || !liveness.RecordKnown || !liveness.Ended || liveness.EndedAt.IsZero() {
		return ExitEvidence{}, false
	}
	if !sessionEndAged(c.now(), liveness.EndedAt) {
		return ExitEvidence{}, false
	}
	return ExitEvidence{Source: source, CodeKnown: false, EndedAt: liveness.EndedAt, Detail: "session record ended"}, true
}

// sessionEndAged reports whether a session-end timestamp is old enough to
// treat as exit evidence. A zero EndedAt is not evidence: Codex Stop and
// Claude SessionEnd can fire while the agent is still alive, and a missing
// timestamp cannot distinguish those from a real exit.
func sessionEndAged(now, endedAt time.Time) bool {
	return !endedAt.IsZero() && now.Sub(endedAt) >= SessionEndGrace
}
