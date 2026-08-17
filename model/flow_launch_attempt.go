package model

import (
	"strings"

	"github.com/approachcontrol/approach/flowstore"
)

// flowLaunchState is the position of one launch attempt in the lifecycle. The
// states are ordered but not all reachable for every kind: handoffPending only
// exists on the external route, and failurePersisting is reachable from any
// state on a token match alone.
type flowLaunchState int

const (
	flowLaunchStateReserved flowLaunchState = iota + 1
	flowLaunchStateReadingSession
	flowLaunchStateReading
	flowLaunchStatePreparing
	flowLaunchStateHandoffPending
	flowLaunchStateFailurePersisting
	flowLaunchStateCreateSessionReading
	flowLaunchStateCreateWriting
	flowLaunchStateCreateReserving
	flowLaunchStateCreateWorktree
	flowLaunchStateCreateBootstrap
	flowLaunchStateCreateLaunchID
	flowLaunchStateCreateMetadata
)

func (state flowLaunchState) String() string {
	switch state {
	case flowLaunchStateReserved:
		return "reserved"
	case flowLaunchStateReadingSession:
		return "readingSession"
	case flowLaunchStateReading:
		return "reading"
	case flowLaunchStatePreparing:
		return "preparing"
	case flowLaunchStateHandoffPending:
		return "handoffPending"
	case flowLaunchStateFailurePersisting:
		return "failurePersisting"
	case flowLaunchStateCreateSessionReading:
		return "createSessionReading"
	case flowLaunchStateCreateWriting:
		return "createWriting"
	case flowLaunchStateCreateReserving:
		return "createReserving"
	case flowLaunchStateCreateWorktree:
		return "createWorktree"
	case flowLaunchStateCreateBootstrap:
		return "createBootstrap"
	case flowLaunchStateCreateLaunchID:
		return "createLaunchID"
	case flowLaunchStateCreateMetadata:
		return "createMetadata"
	default:
		return "unknown"
	}
}

// flowLaunchAttempt reserves one Flow for one launch. It is the only per-Flow
// launch reservation the lifecycle owns; a retained embedded terminal slot owns
// the Flow instead once installed, so a successful launch's attempt does not
// outlive slot installation. The prefill-failure path is the one exception: it
// re-reserves while the slot it is about to dismiss is still installed, which
// is what keeps the Flow owned across the correction.
type flowLaunchAttempt struct {
	Token string
	Kind  flowLaunchKind
	State flowLaunchState
	// FlowID is an exact persisted Flow ID except while a saved-session resume
	// owns only its provider/session key during the authoritative session read.
	// That state uses an internal non-persistable provisional ID, then transfers
	// atomically to the refreshed Flow ID or releases into the non-Flow route.
	FlowID              string
	PhaseID             string
	Origin              flowLaunchOrigin
	Settings            flowLaunchAgentSettingsSnapshot
	AutoRetrySuppressed bool
	SessionKey          flowLaunchSavedSessionKey
	// MutatedPhase records that AddPhaseLaunchID succeeded, so a later failure
	// has a persisted running phase to correct. Without it a failure between
	// phase resolution and persistence would clobber a still-ready phase.
	MutatedPhase bool
	Create       flowLaunchCreateRequest
	StartupRoots []flowstore.FlowPhase
}

// flowLaunchAttemptOccupied reports whether a lifecycle attempt currently holds
// this Flow. Matching is on the exact canonical Flow ID so prefix-like IDs
// never collide.
func (m Model) flowLaunchAttemptOccupied(flowID string) bool {
	_, ok := m.flowLaunchAttempt(flowID)
	return ok
}

func (m Model) flowLaunchHandoffPending() bool {
	for _, attempt := range m.flowLaunchAttempts {
		if attempt.State == flowLaunchStateHandoffPending {
			return true
		}
	}
	return false
}

// flowLaunchAttemptKind names what is holding this Flow, for the callers that
// have to tell launch sources apart rather than just detect occupancy: an
// admission that refuses one competing kind, and the headless toggle, which
// fences only the kind whose headless read it could race. It returns the zero
// kind when nothing holds it.
func (m Model) flowLaunchAttemptKind(flowID string) flowLaunchKind {
	attempt, ok := m.flowLaunchAttempt(flowID)
	if !ok {
		return 0
	}
	return attempt.Kind
}

func (m Model) flowLaunchAttempt(flowID string) (flowLaunchAttempt, bool) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return flowLaunchAttempt{}, false
	}
	attempt, ok := m.flowLaunchAttempts[flowID]
	if !ok || strings.TrimSpace(attempt.Token) == "" {
		return flowLaunchAttempt{}, false
	}
	return attempt, true
}

// matchingFlowLaunchAttempt is the one fence every lifecycle-owned handler
// applies: the attempt must exist for this Flow, carry this token, and sit in
// the expected state. Kind is checked only when the caller knows it.
func (m Model) matchingFlowLaunchAttempt(flowID, token string, kind flowLaunchKind, want flowLaunchState) (flowLaunchAttempt, bool) {
	attempt, ok := m.flowLaunchAttempt(flowID)
	if !ok {
		return flowLaunchAttempt{}, false
	}
	if attempt.Token != strings.TrimSpace(token) || attempt.State != want {
		return flowLaunchAttempt{}, false
	}
	if kind != 0 && attempt.Kind != kind {
		return flowLaunchAttempt{}, false
	}
	return attempt, true
}

// reserveFlowLaunchAttempt installs an attempt for this Flow. It fails when the
// attempt map already holds the Flow; every other occupancy signal is checked
// by lifecycle admission, not here, because the prefill-failure re-reservation
// runs while the embedded slot it is correcting is still installed.
func (m Model) reserveFlowLaunchAttempt(attempt flowLaunchAttempt, state flowLaunchState) (Model, bool) {
	attempt.FlowID = strings.TrimSpace(attempt.FlowID)
	attempt.Token = strings.TrimSpace(attempt.Token)
	if attempt.FlowID == "" || attempt.Token == "" {
		return m, false
	}
	if m.flowLaunchAttemptOccupied(attempt.FlowID) {
		return m, false
	}
	if attempt.Kind == flowLaunchKindSavedSessionResume {
		if !attempt.SessionKey.valid() {
			return m, false
		}
		if _, occupied := m.flowLaunchSessionOwners[attempt.SessionKey]; occupied {
			return m, false
		}
	}
	attempt.State = state
	m = m.withFlowLaunchAttempt(attempt)
	if attempt.Kind == flowLaunchKindSavedSessionResume {
		owners := make(map[flowLaunchSavedSessionKey]flowLaunchSavedSessionOwner, len(m.flowLaunchSessionOwners)+1)
		for key, owner := range m.flowLaunchSessionOwners {
			owners[key] = owner
		}
		owners[attempt.SessionKey] = flowLaunchSavedSessionOwner{Token: attempt.Token, FlowID: attempt.FlowID}
		m.flowLaunchSessionOwners = owners
	}
	return m, true
}

type flowLaunchSavedSessionOwner struct {
	Token  string
	FlowID string
}

// transferSavedSessionFlowLaunchAttempt moves both ownership indexes in one
// returned Model after the authoritative session read changes Flow identity.
func (m Model) transferSavedSessionFlowLaunchAttempt(fromFlowID, token string, key flowLaunchSavedSessionKey, destinationFlowID string) (Model, bool) {
	fromFlowID = strings.TrimSpace(fromFlowID)
	destinationFlowID = strings.TrimSpace(destinationFlowID)
	attempt, ok := m.matchingFlowLaunchAttempt(fromFlowID, token, flowLaunchKindSavedSessionResume, flowLaunchStateReadingSession)
	if !ok || destinationFlowID == "" || attempt.SessionKey != key {
		return m, false
	}
	owner, ok := m.flowLaunchSessionOwners[key]
	if !ok || owner.Token != attempt.Token || owner.FlowID != fromFlowID {
		return m, false
	}
	if destinationFlowID != fromFlowID && m.flowLaunchAdmissionOccupied(destinationFlowID) {
		return m, false
	}
	attempt.FlowID = destinationFlowID
	attempt.State = flowLaunchStateReading
	attempts := make(map[string]flowLaunchAttempt, len(m.flowLaunchAttempts))
	for flowID, existing := range m.flowLaunchAttempts {
		if flowID != fromFlowID {
			attempts[flowID] = existing
		}
	}
	attempts[destinationFlowID] = attempt
	owners := make(map[flowLaunchSavedSessionKey]flowLaunchSavedSessionOwner, len(m.flowLaunchSessionOwners))
	for existingKey, existingOwner := range m.flowLaunchSessionOwners {
		owners[existingKey] = existingOwner
	}
	owners[key] = flowLaunchSavedSessionOwner{Token: attempt.Token, FlowID: destinationFlowID}
	m.flowLaunchAttempts = attempts
	m.flowLaunchSessionOwners = owners
	return m, true
}

// transitionFlowLaunchAttempt advances an attempt from one state to another.
// Any transition whose source state does not match is a fenced no-op, so a
// stale event cannot move a superseded attempt.
func (m Model) transitionFlowLaunchAttempt(flowID, token string, from, to flowLaunchState) (Model, bool) {
	attempt, ok := m.matchingFlowLaunchAttempt(flowID, token, 0, from)
	if !ok {
		return m, false
	}
	attempt.State = to
	return m.withFlowLaunchAttempt(attempt), true
}

// updateFlowLaunchAttempt edits the attempt this Flow and token name. Unlike
// transitionFlowLaunchAttempt it does not fence on state, so it is only for
// fields that are true regardless of where the attempt has reached.
func (m Model) updateFlowLaunchAttempt(flowID, token string, mutate func(*flowLaunchAttempt)) Model {
	attempt, ok := m.flowLaunchAttempt(flowID)
	if !ok || attempt.Token != strings.TrimSpace(token) {
		return m
	}
	mutate(&attempt)
	return m.withFlowLaunchAttempt(attempt)
}

// markFlowLaunchAttemptMutatedPhase records that this attempt persisted a
// launch ID, which is what makes its failure path responsible for correcting
// the phase status.
func (m Model) markFlowLaunchAttemptMutatedPhase(flowID, token string) Model {
	return m.updateFlowLaunchAttempt(flowID, token, func(attempt *flowLaunchAttempt) {
		attempt.MutatedPhase = true
	})
}

// releaseFlowLaunchAttempt frees the Flow. It matches on the token alone and is
// a no-op on mismatch, so a late release from a superseded attempt can never
// free a live one.
func (m Model) releaseFlowLaunchAttempt(flowID, token string) Model {
	flowID = strings.TrimSpace(flowID)
	attempt, ok := m.flowLaunchAttempt(flowID)
	if !ok || attempt.Token != strings.TrimSpace(token) {
		return m
	}
	attempts := make(map[string]flowLaunchAttempt, len(m.flowLaunchAttempts))
	for existingFlowID, existing := range m.flowLaunchAttempts {
		if existingFlowID != flowID {
			attempts[existingFlowID] = existing
		}
	}
	if len(attempts) == 0 {
		attempts = nil
	}
	m.flowLaunchAttempts = attempts
	if attempt.SessionKey.valid() {
		if owner, owned := m.flowLaunchSessionOwners[attempt.SessionKey]; owned && owner.Token == attempt.Token && owner.FlowID == flowID {
			owners := make(map[flowLaunchSavedSessionKey]flowLaunchSavedSessionOwner, len(m.flowLaunchSessionOwners)-1)
			for key, existing := range m.flowLaunchSessionOwners {
				if key != attempt.SessionKey {
					owners[key] = existing
				}
			}
			if len(owners) == 0 {
				owners = nil
			}
			m.flowLaunchSessionOwners = owners
		}
	}
	return m
}

// suppressUnpersistedAutoFlowLaunchRetry lets a newer AutoMode stop edge win
// without discarding prepare work that may already have persisted a launch ID.
func (m Model) suppressUnpersistedAutoFlowLaunchRetry(flowID string) Model {
	attempt, ok := m.flowLaunchAttempt(flowID)
	if !ok || attempt.Kind != flowLaunchKindAutoPhase || attempt.MutatedPhase {
		return m
	}
	switch attempt.State {
	case flowLaunchStateReading:
		// The read command persists nothing about this launch, so its eventual
		// event can be invalidated by releasing the attempt. It is no longer
		// side-effect free: a stop edge landing inside the ensure window leaves
		// the worktree it created and the start metadata it persisted behind.
		// Nothing needs cleaning up either way — the next launch takes
		// EnsureWorktree's passthrough and reuses both. A launch started before
		// the write lands does not allocate a second pair: EnsureWorktree holds
		// the Flow's launch reservation across its creation and its write, and
		// the record it re-reads under that fence already names the worktree.
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	case flowLaunchStatePreparing:
		// Prepare may be blocked before or after its phase write. Keep ownership
		// so a successful event can finish the handoff; only suppress its retry
		// if the command reports a pre-persistence failure.
		attempt.AutoRetrySuppressed = true
		return m.withFlowLaunchAttempt(attempt)
	default:
		return m
	}
}

func (m Model) withFlowLaunchAttempt(attempt flowLaunchAttempt) Model {
	attempts := make(map[string]flowLaunchAttempt, len(m.flowLaunchAttempts)+1)
	for flowID, existing := range m.flowLaunchAttempts {
		attempts[flowID] = existing
	}
	attempts[attempt.FlowID] = attempt
	m.flowLaunchAttempts = attempts
	return m
}
