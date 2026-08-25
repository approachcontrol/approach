package launchcontrol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

// ReplayResult summarizes one launch's replay.
type ReplayResult struct {
	LaunchID   string
	Applied    int
	Rejected   int
	Reconciled bool
	Notices    []string
}

// phaseIdentity is all the comparator may see of the live phase: its status
// and which launch owns it. UpdatedAt is out of reach by construction — a
// timestamp bumped by an unrelated write (a session attach, a readiness
// refresh) must never make a spooled result look stale.
type phaseIdentity struct {
	LaunchID string
	Status   string
}

func identityOf(phase flowstore.FlowPhase) phaseIdentity {
	return phaseIdentity{LaunchID: flowstore.LatestPhaseLaunchID(phase), Status: string(phase.Status)}
}

// replayLocked replays a launch's pending requests. The caller holds both the
// in-process launch lock and the file lock.
//
// Four cases, decided per request in sequence order once the latest-launch
// gate has passed for the launch as a whole:
//
//  1. a durable response proves the request completed before its applied
//     marker was written, so the marker is advanced without executing again;
//  2. the live phase already shows the request's target — a request that was
//     applied but never marked — so it is marked applied and nothing is written;
//  3. the live status equals the comparison state (applied.json's status, else
//     baseline.json's) — nothing else has moved the phase since this launch
//     last touched it — so the request is applied through Execute;
//  4. anything else — the phase moved underneath the launch — so the whole
//     remaining batch is rejected as phase_result_stale, and only a phase that
//     is still `running` under this launch is demoted, loudly.
//
// A refusal that case 3 admitted is a client error (request_invalid), not
// staleness: it is recorded and skipped, and never demotes.
func (c *Controller) replayLocked(log *Log) (ReplayResult, error) {
	result := ReplayResult{LaunchID: log.LaunchID()}
	pending, err := log.Pending()
	if err != nil {
		return result, err
	}
	if len(pending) == 0 {
		return result, nil
	}
	now := c.now()
	notice := func(format string, args ...any) {
		result.Notices = append(result.Notices, fmt.Sprintf(format, args...))
	}
	last := pending[len(pending)-1].Seq
	rejectBatch := func(batch []RequestEnvelope, reason, intended, observed, errText string) error {
		if len(batch) == 0 {
			return nil
		}
		result.Rejected += len(batch)
		if err := log.AppendRejected(RejectedBatch{
			RejectedAt: now, Reason: reason, IntendedStatus: intended, ObservedStatus: observed,
			Error: errText, Requests: batch,
		}); err != nil {
			return err
		}
		return log.WriteApplied(AppliedState{AppliedSeq: batch[len(batch)-1].Seq, Status: observed, Result: ResultRejected, AppliedAt: now})
	}

	info, _, err := log.Launch()
	if err != nil {
		return result, err
	}
	flowID := strings.TrimSpace(info.FlowID)
	phaseID := info.PhaseID
	if flowID == "" {
		flowID = pending[0].FlowID
	}
	if phaseID == "" {
		phaseID = pending[0].PhaseID
	}
	unowned := info.FlowID != "" && info.PhaseID == "" || pending[0].Unowned
	if unowned || phaseID == "" {
		notice("launch %s: %d spooled request(s) rejected: launch has no owned phase (%s)", log.LaunchID(), len(pending), ReasonBaselineMissing)
		return result, rejectBatch(pending, ReasonBaselineMissing, intendedStatus(pending[0]), "", "launch has no owned phase")
	}

	record, err := c.store.Read(flowID)
	if err != nil {
		if flowstore.IsNotFound(err) {
			notice("launch %s: %d spooled request(s) rejected: %v", log.LaunchID(), len(pending), err)
			return result, rejectBatch(pending, ReasonRequestInvalid, intendedStatus(pending[0]), "", err.Error())
		}
		return result, err
	}
	phase, ok := PhaseByID(record, phaseID)
	if !ok {
		notice("launch %s: %d spooled request(s) rejected: phase %q not found in flow %q", log.LaunchID(), len(pending), phaseID, flowID)
		return result, rejectBatch(pending, ReasonRequestInvalid, intendedStatus(pending[0]), "", fmt.Sprintf("phase %q not found in flow %q", phaseID, flowID))
	}
	live := identityOf(phase)
	if live.LaunchID != log.LaunchID() {
		notice("launch %s: %d spooled request(s) rejected as %s: phase %s of flow %s is now owned by launch %s (observed %s)",
			log.LaunchID(), len(pending), ReasonPhaseResultStale, phaseID, flowID, live.LaunchID, live.Status)
		return result, rejectBatch(pending, ReasonPhaseResultStale, intendedStatus(pending[0]), live.Status,
			fmt.Sprintf("phase is now owned by launch %s", live.LaunchID))
	}
	comparison := ""
	if applied, ok, err := log.Applied(); err != nil {
		return result, err
	} else if ok && applied.Status != "" {
		comparison = applied.Status
	} else if baseline, ok, err := log.Baseline(); err != nil {
		return result, err
	} else if ok {
		comparison = baseline.BaselineStatus
	}
	if comparison == "" {
		notice("launch %s: %d spooled request(s) rejected: no baseline recorded for this launch (%s)", log.LaunchID(), len(pending), ReasonBaselineMissing)
		return result, rejectBatch(pending, ReasonBaselineMissing, intendedStatus(pending[0]), live.Status, "no baseline.json for this launch")
	}

	for i, env := range pending {
		resp, completed, err := log.Response(env.RequestID)
		if err != nil {
			return result, err
		}
		if completed {
			appliedResult := ResultApplied
			if !resp.OK {
				appliedResult = ResultRefused
			}
			postState, err := savedResponsePhaseState(env, phaseID, resp)
			if err != nil {
				return result, err
			}
			if postState.Status == "" {
				postState.Status = comparison
			}
			if err := log.WriteApplied(AppliedState{
				AppliedSeq: env.Seq, Status: postState.Status, Result: appliedResult,
				ObservedUpdatedAt: postState.UpdatedAt, AppliedAt: now,
			}); err != nil {
				return result, err
			}
			result.Applied++
			comparison = postState.Status
			continue
		}
		if !env.Replayable {
			notice("launch %s: request %d (%s) is not replayable and was dropped", log.LaunchID(), env.Seq, env.Verb)
			if err := rejectBatch([]RequestEnvelope{env}, ReasonRequestInvalid, "", live.Status, "verb is not replayable"); err != nil {
				return result, err
			}
			continue
		}
		if targetReached(record, phase, env) {
			if err := log.WriteApplied(AppliedState{AppliedSeq: env.Seq, Status: string(phase.Status), Result: ResultApplied, ObservedUpdatedAt: phase.UpdatedAt, AppliedAt: now}); err != nil {
				return result, err
			}
			result.Applied++
			comparison = string(phase.Status)
			continue
		}
		if live.Status == comparison {
			req := requestFromEnvelope(env)
			resp, err := Execute(c.store, req)
			if err != nil {
				return result, err
			}
			if err := log.WriteResponse(env.RequestID, resp); err != nil {
				return result, err
			}
			if err := applyMarkerHook(); err != nil {
				return result, err
			}
			if !resp.OK {
				notice("launch %s: request %d (%s) refused on replay: %s", log.LaunchID(), env.Seq, env.Verb, resp.Error)
				if err := rejectBatch([]RequestEnvelope{env}, ReasonRequestInvalid, intendedStatus(env), live.Status, resp.Error); err != nil {
					return result, err
				}
				continue
			}
			record, err = c.store.Read(flowID)
			if err != nil {
				return result, err
			}
			phase, _ = PhaseByID(record, phaseID)
			live = identityOf(phase)
			if err := log.WriteApplied(AppliedState{AppliedSeq: env.Seq, Status: string(phase.Status), Result: ResultApplied, ObservedUpdatedAt: phase.UpdatedAt, AppliedAt: now}); err != nil {
				return result, err
			}
			result.Applied++
			comparison = string(phase.Status)
			continue
		}
		// Case 3: the phase moved underneath the launch.
		intended := intendedStatus(env)
		remaining := pending[i:]
		notice("launch %s: %d spooled request(s) rejected as %s: intended %s, observed %s (expected %s) on phase %s of flow %s",
			log.LaunchID(), len(remaining), ReasonPhaseResultStale, intended, live.Status, comparison, phaseID, flowID)
		if err := rejectBatch(remaining, ReasonPhaseResultStale, intended, live.Status,
			fmt.Sprintf("phase status %s did not match the launch's last known status %s", live.Status, comparison)); err != nil {
			return result, err
		}
		if live.Status == string(flowstore.PhaseRunning) {
			update := reconcileUpdate(phase, ReasonPhaseResultStale, nil, flowID, log.LaunchID(), intended)
			resp, err := c.demote(log, flowID, log.LaunchID(), ReasonPhaseResultStale, update, last, now)
			if err != nil {
				return result, err
			}
			if !resp.OK {
				notice("launch %s: could not mark phase %s: %s", log.LaunchID(), phaseID, resp.Error)
			} else {
				result.Reconciled = true
			}
		}
		break
	}
	return result, nil
}

func savedResponsePhaseState(env RequestEnvelope, phaseID string, resp Response) (ObservedPhase, error) {
	if !resp.OK {
		return env.Observed, nil
	}
	var action PhaseActionResult
	if err := json.Unmarshal(resp.Result, &action); err == nil &&
		artifacts.NormalizePhaseID(action.UpdatedPhase.PhaseID) == artifacts.NormalizePhaseID(phaseID) &&
		action.UpdatedPhase.Status != "" {
		return ObservedPhase{Status: string(action.UpdatedPhase.Status), UpdatedAt: action.UpdatedPhase.UpdatedAt}, nil
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(resp.Result, &record); err == nil {
		if phase, ok := PhaseByID(record, phaseID); ok {
			return ObservedPhase{Status: string(phase.Status), UpdatedAt: phase.UpdatedAt}, nil
		}
	}
	return ObservedPhase{}, fmt.Errorf("saved response for request %s does not contain phase %s post-state", env.RequestID, phaseID)
}

// demote applies the controller-only reconciliation mutation and records the
// demoted status as the launch's comparison state, so a later replay measures
// against the reconciled phase. Agent-facing phase.set cannot create the stamp.
func (c *Controller) demote(log *Log, flowID, launchID, reason string, update flowstore.PhaseUpdate, seq int, now time.Time) (Response, error) {
	record, err := c.store.DemoteReconciledPhase(flowstore.ReconciliationDemotionUpdate{
		PhaseUpdate: update, Reason: reason, LaunchID: launchID,
	})
	if err != nil {
		return refuse(err), nil
	}
	result, warning, err := phaseActionResult(record, update.PhaseID)
	if err != nil {
		return Response{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return Response{}, fmt.Errorf("encode reconciled phase result: %w", err)
	}
	resp := Response{SchemaVersion: ProtocolSchemaVersion, OK: true, Result: data, Warning: warning}
	if err := log.WriteApplied(AppliedState{AppliedSeq: seq, Status: string(update.Status), Result: ResultReconciled, AppliedAt: now}); err != nil {
		return resp, err
	}
	c.notify(AppliedEvent{FlowID: flowID, PhaseID: update.PhaseID, LaunchID: launchID})
	return resp, nil
}

func requestFromEnvelope(env RequestEnvelope) Request {
	ownerPhaseID := ""
	if !env.Unowned {
		ownerPhaseID = env.PhaseID
	}
	return Request{
		SchemaVersion: ProtocolSchemaVersion,
		RequestID:     env.RequestID,
		LaunchID:      env.LaunchID,
		FlowID:        env.FlowID,
		PhaseID:       env.PhaseID,
		OwnerPhaseID:  ownerPhaseID,
		Verb:          env.Verb,
		Payload:       env.Payload,
	}
}

// intendedStatus is the phase status a request means to reach, or "" for
// Flow-level verbs.
func intendedStatus(env RequestEnvelope) string {
	switch env.Verb {
	case VerbPhaseSet:
		var payload PhaseSetPayload
		_ = json.Unmarshal(env.Payload, &payload)
		return payload.Status
	case VerbPhaseComplete, VerbPhaseBlock, VerbPhaseNeedsAttention:
		return phaseActions[env.Verb].status
	}
	return ""
}

// targetReached reports whether the live record already shows everything the
// request would write: the status, every field the payload names, and the
// effect Execute derives for what it leaves blank. It is case 1 of replay —
// the request landed but its marker did not — and it errs toward false, since
// a false negative only re-executes an idempotent request while a false
// positive loses a write for good.
func targetReached(record flowstore.FlowRecord, phase flowstore.FlowPhase, env RequestEnvelope) bool {
	fieldsMatch := func(outcome, notes, summary string) bool {
		if outcome != "" && phase.Outcome != outcome {
			return false
		}
		if notes != "" && phase.Notes != notes {
			return false
		}
		if summary != "" && phase.Summary != summary {
			return false
		}
		return true
	}
	switch env.Verb {
	case VerbPhaseSet:
		var payload PhaseSetPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return false
		}
		return string(phase.Status) == payload.Status && fieldsMatch(payload.Outcome, payload.Notes, payload.Summary)
	case VerbPhaseComplete, VerbPhaseBlock, VerbPhaseNeedsAttention:
		var payload PhaseActionPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return false
		}
		// The effect Execute would have, not the payload's spelling of it: an
		// omitted outcome derives the kind's default, and only a kind with no
		// default leaves the outcome a wildcard.
		action := phaseActions[env.Verb]
		outcome := strings.TrimSpace(payload.Outcome)
		if outcome == "" {
			outcome = defaultPhaseActionOutcome(string(flowstore.SemanticKind(phase)), action)
		}
		return string(phase.Status) == action.status && fieldsMatch(outcome, payload.Notes, payload.Summary)
	case VerbPlanSet:
		var payload PlanSetPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return false
		}
		// An omitted path is resolved by the store to the saved plan's own,
		// which the log cannot know; the request is executed (idempotently)
		// rather than assumed reached.
		if payload.PlanPath == "" {
			return false
		}
		return record.PlanID == payload.PlanID && record.PlanPath == payload.PlanPath
	// The Flow-level verbs replace a whole struct, so every field the store
	// would write is compared, normalized the way the store normalizes it. A
	// request that differs in any field has not landed, and marking it applied
	// would lose that field for good.
	case VerbIssueSet:
		var payload IssueSetPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return false
		}
		return record.Issue == flowstore.Issue{
			Provider: strings.ToLower(strings.TrimSpace(payload.Provider)),
			Number:   payload.Number,
			URL:      strings.TrimSpace(payload.URL),
		}
	case VerbPRSet:
		var payload PRSetPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return false
		}
		return record.PR == flowstore.PullRequest{
			Provider:   strings.ToLower(strings.TrimSpace(payload.Provider)),
			Number:     payload.Number,
			URL:        strings.TrimSpace(payload.URL),
			HeadBranch: strings.TrimSpace(payload.Head),
			BaseBranch: strings.TrimSpace(payload.Base),
			Status:     strings.TrimSpace(payload.Status),
		}
	case VerbMergeSet:
		var payload MergeSetPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return false
		}
		status := strings.TrimSpace(payload.Status)
		if status == "" || record.Merge.Status != status {
			return false
		}
		if status != flowstore.MergeMerged {
			// A non-merged status carries no commit or timestamp.
			return record.Merge.Commit == "" && record.Merge.MergedAt == nil
		}
		mergedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.MergedAt))
		if err != nil {
			return false
		}
		return record.Merge.Commit == strings.TrimSpace(payload.Commit) &&
			record.Merge.MergedAt != nil && record.Merge.MergedAt.Equal(mergedAt)
	}
	return false
}
