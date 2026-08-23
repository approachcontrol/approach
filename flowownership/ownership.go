package flowownership

import "strings"

// State is a lifecycle position held by one Flow launch owner.
type State uint8

const (
	StateReserved State = iota + 1
	StateReadingSession
	StateReading
	StatePreparing
	StateHandoffPending
	StateFailurePersisting
	StateCreateSessionReading
	StateCreateWriting
	StateCreateReserving
	StateCreateWorktree
	StateCreateBootstrap
	StateCreateLaunchID
	StateCreateMetadata
)

func (state State) String() string {
	switch state {
	case StateReserved:
		return "reserved"
	case StateReadingSession:
		return "readingSession"
	case StateReading:
		return "reading"
	case StatePreparing:
		return "preparing"
	case StateHandoffPending:
		return "handoffPending"
	case StateFailurePersisting:
		return "failurePersisting"
	case StateCreateSessionReading:
		return "createSessionReading"
	case StateCreateWriting:
		return "createWriting"
	case StateCreateReserving:
		return "createReserving"
	case StateCreateWorktree:
		return "createWorktree"
	case StateCreateBootstrap:
		return "createBootstrap"
	case StateCreateLaunchID:
		return "createLaunchID"
	case StateCreateMetadata:
		return "createMetadata"
	default:
		return "unknown"
	}
}

// Record is one token-fenced Flow owner and its caller-owned payload.
type Record[P any, K comparable] struct {
	flowID     string
	token      string
	state      State
	payload    P
	sessionKey K
	hasSession bool
}

func (record Record[P, K]) FlowID() string   { return record.flowID }
func (record Record[P, K]) Token() string    { return record.token }
func (record Record[P, K]) State() State     { return record.state }
func (record Record[P, K]) Payload() P       { return record.payload }
func (record Record[P, K]) SessionKey() K    { return record.sessionKey }
func (record Record[P, K]) HasSession() bool { return record.hasSession }

// SessionOwner exposes the identity stored in the saved-session index.
type SessionOwner struct {
	flowID string
	token  string
}

func (owner SessionOwner) FlowID() string { return owner.flowID }
func (owner SessionOwner) Token() string  { return owner.token }

// Ownership owns the two in-process indexes that reserve Flows and saved
// sessions. Its methods return copies so Bubble Tea Model values keep their
// existing value semantics.
type Ownership[P any, K comparable] struct {
	flows    map[string]Record[P, K]
	sessions map[K]SessionOwner
}

func (ownership Ownership[P, K]) Occupied(flowID string) bool {
	_, ok := ownership.Lookup(flowID)
	return ok
}

func (ownership Ownership[P, K]) Lookup(flowID string) (Record[P, K], bool) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return Record[P, K]{}, false
	}
	record, ok := ownership.flows[flowID]
	if !ok || strings.TrimSpace(record.token) == "" {
		return Record[P, K]{}, false
	}
	return record, true
}

func (ownership Ownership[P, K]) Match(flowID, token string, state State) (Record[P, K], bool) {
	record, ok := ownership.Lookup(flowID)
	if !ok || record.token != strings.TrimSpace(token) || record.state != state {
		return Record[P, K]{}, false
	}
	return record, true
}

func (ownership Ownership[P, K]) Reserve(flowID, token string, state State, payload P, sessionKey *K) (Ownership[P, K], bool) {
	flowID = strings.TrimSpace(flowID)
	token = strings.TrimSpace(token)
	if flowID == "" || token == "" || state == 0 || ownership.Occupied(flowID) {
		return ownership, false
	}
	if sessionKey != nil {
		if ownership.SessionOccupied(*sessionKey) {
			return ownership, false
		}
	}
	record := Record[P, K]{flowID: flowID, token: token, state: state, payload: payload}
	if sessionKey != nil {
		record.sessionKey = *sessionKey
		record.hasSession = true
	}
	next := ownership.clone()
	if next.flows == nil {
		next.flows = make(map[string]Record[P, K])
	}
	next.flows[flowID] = record
	if sessionKey != nil {
		if next.sessions == nil {
			next.sessions = make(map[K]SessionOwner)
		}
		next.sessions[*sessionKey] = SessionOwner{flowID: flowID, token: token}
	}
	return next, true
}

func (ownership Ownership[P, K]) Transition(flowID, token string, from, to State) (Ownership[P, K], bool) {
	record, ok := ownership.Match(flowID, token, from)
	if !ok || to == 0 {
		return ownership, false
	}
	record.state = to
	next := ownership.clone()
	next.flows[record.flowID] = record
	return next, true
}

func (ownership Ownership[P, K]) UpdatePayload(flowID, token string, update func(P) P) (Ownership[P, K], bool) {
	record, ok := ownership.Lookup(flowID)
	if !ok || record.token != strings.TrimSpace(token) || update == nil {
		return ownership, false
	}
	record.payload = update(record.payload)
	next := ownership.clone()
	next.flows[record.flowID] = record
	return next, true
}

func (ownership Ownership[P, K]) TransferSession(fromFlowID, token string, key K, destinationFlowID string, from, to State) (Ownership[P, K], bool) {
	fromFlowID = strings.TrimSpace(fromFlowID)
	destinationFlowID = strings.TrimSpace(destinationFlowID)
	record, ok := ownership.Match(fromFlowID, token, from)
	if !ok || !record.hasSession || record.sessionKey != key || destinationFlowID == "" || to == 0 {
		return ownership, false
	}
	owner, ok := ownership.sessions[key]
	if !ok || owner.flowID != fromFlowID || owner.token != record.token {
		return ownership, false
	}
	if destinationFlowID != fromFlowID && ownership.Occupied(destinationFlowID) {
		return ownership, false
	}
	next := ownership.clone()
	delete(next.flows, fromFlowID)
	record.flowID = destinationFlowID
	record.state = to
	next.flows[destinationFlowID] = record
	next.sessions[key] = SessionOwner{flowID: destinationFlowID, token: record.token}
	return next, true
}

func (ownership Ownership[P, K]) Release(flowID, token string) (Ownership[P, K], bool) {
	record, ok := ownership.Lookup(flowID)
	if !ok || record.token != strings.TrimSpace(token) {
		return ownership, false
	}
	next := ownership.clone()
	delete(next.flows, record.flowID)
	if len(next.flows) == 0 {
		next.flows = nil
	}
	if record.hasSession {
		if owner, ok := next.sessions[record.sessionKey]; ok && owner.flowID == record.flowID && owner.token == record.token {
			delete(next.sessions, record.sessionKey)
		}
		if len(next.sessions) == 0 {
			next.sessions = nil
		}
	}
	return next, true
}

func (ownership Ownership[P, K]) SessionOccupied(key K) bool {
	_, ok := ownership.sessions[key]
	return ok
}

func (ownership Ownership[P, K]) SessionOwner(key K) (SessionOwner, bool) {
	owner, ok := ownership.sessions[key]
	return owner, ok
}

func (ownership Ownership[P, K]) AnyState(state State) bool {
	for _, record := range ownership.flows {
		if record.state == state {
			return true
		}
	}
	return false
}

func (ownership Ownership[P, K]) Len() int        { return len(ownership.flows) }
func (ownership Ownership[P, K]) SessionLen() int { return len(ownership.sessions) }

func (ownership Ownership[P, K]) clone() Ownership[P, K] {
	next := Ownership[P, K]{
		flows:    make(map[string]Record[P, K], len(ownership.flows)),
		sessions: make(map[K]SessionOwner, len(ownership.sessions)),
	}
	for flowID, record := range ownership.flows {
		next.flows[flowID] = record
	}
	for key, owner := range ownership.sessions {
		next.sessions[key] = owner
	}
	return next
}
