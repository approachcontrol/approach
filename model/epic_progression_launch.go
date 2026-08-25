package model

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/flowstore"
)

// The advance edge submits one create-then-launch intent per selected child and
// lets the create pipeline own everything after that: the record, the worktree,
// the preparation receipt, successor reconciliation, and the first phase. This
// file is the progression half of that pipeline — the fence it is admitted
// under, the ownership it installs, the reconciliation outcome it applies, and
// the halt a terminal failure turns into.

// isCurrentEpicProgressionAdvanceRequest is the create-phase presentation fence
// for progression children. Unlike the two key-press origins it does not
// consult the displayed repository: the poll that submitted it runs unscoped.
func (m Model) isCurrentEpicProgressionAdvanceRequest(request uint64) bool {
	active := m.activeEpicProgressionAdvance
	return request != 0 && active.Request == request &&
		m.flowPreparationMatches(flowPreparationEpicAdvance, active.OwnerToken)
}

// clearEpicProgressionFlowCreateRequest ends the advance that owns request. It
// is the single release point for both the advance's preparation admission and
// its in-flight ownership marker, so every pipeline exit — success, failure,
// cancellation — frees the epic for the next poll exactly once.
func (m Model) clearEpicProgressionFlowCreateRequest(request uint64) Model {
	active := m.activeEpicProgressionAdvance
	if request == 0 || active.Request != request {
		return m
	}
	m.activeEpicProgressionAdvance = epicProgressionAdvanceRequest{}
	delete(m.epicProgressionOwnedSuccessors, active.EpicKey)
	return m.releaseFlowPreparation(flowPreparationEpicAdvance, active.OwnerToken)
}

// trackEpicProgressionCreatedSuccessor records the child this advance owns. It
// runs in Update at the write stage, before the worktree exists, so the marker
// is durable in runtime state for the whole remainder of the pipeline.
func (m Model) trackEpicProgressionCreatedSuccessor(attempt flowLaunchAttempt) Model {
	if attempt.Origin != flowLaunchOriginEpicProgression {
		return m
	}
	active := m.activeEpicProgressionAdvance
	if active.Request == 0 || active.Request != attempt.Create.Presentation.Request {
		return m
	}
	if m.epicProgressionOwnedSuccessors == nil {
		m.epicProgressionOwnedSuccessors = make(map[string]epicProgressionOwnedSuccessor)
	}
	m.epicProgressionOwnedSuccessors[active.EpicKey] = epicProgressionOwnedSuccessor{
		SourceFlowID: active.SourceFlowID,
		ChildID:      strings.TrimSpace(attempt.Create.Bead.ID),
		FlowID:       attempt.FlowID,
	}
	return m
}

// applyEpicProgressionSuccessor turns the authoritative successor
// classification into either a pipeline that continues to its first phase
// (accepted) or an aborted attempt. It reports stop=true when the caller must
// return the returned Model and command instead of launching.
//
// An abort here is past the preparation receipt, so the one-shot finalizer is
// already consumed and compensation is not available: the attempt blocks the
// child's startup roots instead, exactly as every other post-receipt create
// failure does. That leaves a blocked exact-linked Flow behind, which later
// selection counts as "this child already has a Flow", so every abort except
// already-inactive progression halts the epic rather than silently skipping the
// child it just consumed.
func (m Model) applyEpicProgressionSuccessor(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd, bool) {
	create := attempt.Create
	epicID := strings.TrimSpace(create.Bead.EpicID)
	key := epicProgressionBaselineKey(create.RepoPath, epicID)
	abort := func(reason string, retry bool) (Model, tea.Cmd, bool) {
		delete(m.epicProgressionOwnedSuccessors, key)
		msg.ProgressionRetry = retry
		next, cmd := m.beginCreateFlowRecovery(attempt, msg, []string{reason}, false, true)
		return next, cmd, true
	}
	if msg.SuccessorErr != "" {
		return abort(fmt.Sprintf("could not reconcile owned successor Flow %s: %s", msg.FlowID, msg.SuccessorErr), false)
	}
	switch msg.Successor.Outcome {
	case flowstore.EpicProgressionSuccessorAccepted:
		if m.epicProgressionBaselines == nil {
			m.epicProgressionBaselines = make(map[string]flowstore.FlowRecord)
		}
		if m.epicProgressionBaselineMinimumRequests == nil {
			m.epicProgressionBaselineMinimumRequests = make(map[string]uint64)
		}
		// The baseline is the accepted child as the store just returned it:
		// prepared and pending. Installing it here rather than after the launch
		// keeps the advance edge level-triggered on a record that exists, and a
		// launch that then fails tears this baseline down through the halt.
		m.epicProgressionBaselines[key] = cloneFlowRecord(msg.Record)
		m.epicProgressionBaselineMinimumRequests[key] = m.autoAdvanceRequestSeq + 1
		delete(m.epicProgressionOwnedSuccessors, key)
		return m, nil, false
	case flowstore.EpicProgressionSuccessorInactive:
		// Progression is already off, done, or halted. There is nothing left to
		// halt and no chain waiting on an announcement.
		delete(m.epicProgressionBaselines, key)
		delete(m.epicProgressionBaselineMinimumRequests, key)
		return abort(fmt.Sprintf("auto-progression for epic %s is no longer active", epicID), true)
	case flowstore.EpicProgressionSuccessorReleased:
		return abort(fmt.Sprintf("owned successor Flow %s was released before its first phase", msg.FlowID), false)
	case flowstore.EpicProgressionSuccessorOwnedObstruction:
		reason := rejectOwnedEpicProgressionSuccessor(msg.Successor.Flow)
		if strings.TrimSpace(reason) == "" {
			reason = fmt.Sprintf("Owned successor Flow %s blocks auto-progression", msg.FlowID)
		}
		return abort(reason, false)
	default:
		return abort(fmt.Sprintf("could not reconcile owned successor Flow %s", msg.FlowID), false)
	}
}

// failEpicProgressionCreate is the one route from a terminal create-pipeline
// failure to the epic's halt. An epic whose next child cannot be created or
// started must stop with a named cause: the alternative is a pending child no
// one will ever launch and a chain that looks alive but never moves.
func (m Model) failEpicProgressionCreate(create flowLaunchCreateRequest, flowID, cause string) (Model, tea.Cmd) {
	if create.Presentation.Origin != flowLaunchOriginEpicProgression {
		return m, nil
	}
	repoPath := filepath.Clean(create.RepoPath)
	epicID := strings.TrimSpace(create.Bead.EpicID)
	childID := strings.TrimSpace(create.Bead.ID)
	if epicID == "" || childID == "" {
		return m, nil
	}
	key := epicProgressionBaselineKey(repoPath, epicID)
	baseline, tracked := m.epicProgressionBaselines[key]
	if !tracked {
		// Nothing is tracking this epic any more, so there is no chain left to
		// halt and no announcement anyone is waiting for.
		return m, nil
	}
	var ownerToken uint64
	var admitted bool
	m, ownerToken, admitted = m.acquireFlowPreparation(flowPreparationEpicHalt)
	if !admitted {
		return m, nil
	}
	m.epicProgressionHaltSeq++
	request := epicProgressionHaltRequest{
		Request: m.epicProgressionHaltSeq, OwnerToken: ownerToken,
		EpicKey: key, SourceFlowID: baseline.FlowID,
	}
	m.activeEpicProgressionHalt = request
	halt := flowstore.EpicProgressionHalt{
		ChildBeadID: childID,
		Status:      flowstore.StatusBlocked,
		Message:     epicProgressionLaunchFailureMessage(flowID, cause),
	}
	return m, m.haltEpicProgressionCauseCmd(request, repoPath, epicID, halt)
}

// epicProgressionLaunchFailureMessage is rendered verbatim beside the child by
// the web viewer, so it names the Flow the chain stopped on and why.
func epicProgressionLaunchFailureMessage(flowID, cause string) string {
	flowID = strings.TrimSpace(flowID)
	cause = strings.TrimSpace(cause)
	subject := "child Flow could not be created"
	if flowID != "" {
		subject = fmt.Sprintf("child Flow %s could not launch its first phase", flowID)
	}
	if cause == "" {
		return subject
	}
	return subject + ": " + cause
}

// requestEpicProgressionChildLaunch is the create-phase source for progression.
// It touches no persistence and no runtime: it emits the intent and lets
// requestFlowLaunch admit it, exactly like the Ready-Bead create-and-start key.
func (m Model) requestEpicProgressionChildLaunch(msg epicProgressionAdvanceResultMsg) (Model, tea.Cmd) {
	childID := strings.TrimSpace(msg.owned.ChildID)
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(""))
	create := flowLaunchCreateRequest{
		Presentation: flowLaunchCreatePresentation{Origin: flowLaunchOriginEpicProgression, Request: msg.request},
		RepoPath:     msg.repoPath,
		Title:        childID + ": " + strings.TrimSpace(msg.childTitle),
		Instructions: epicProgressionChildInstructions(childID),
		Bead:         flowstore.BeadLink{ID: childID, EpicID: msg.epicID},
		// Progression children are unattended by construction: nothing focuses
		// their terminal and no one types into it, so the record is written
		// headless and its phases run with AutoMode, which the store enables on
		// every new Flow. Together those are what make the rest of the child's
		// phases drain without a key press.
		Headless: true,
	}
	return m, func() tea.Msg {
		return flowLaunchCreateRequestedMsg{Create: create, Settings: settings}
	}
}

func epicProgressionChildInstructions(childID string) string {
	return fmt.Sprintf("Use Bead %s as the durable source of requirements. Read it with `bd show %s` before planning or implementation.", childID, childID)
}
