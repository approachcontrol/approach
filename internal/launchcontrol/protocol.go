package launchcontrol

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// ProtocolSchemaVersion is the wire and log schema. Bumping it is a
// compatibility event for every spooled request on disk.
const ProtocolSchemaVersion = 1

// Verb names one `approach flow` leaf on the wire.
type Verb string

const (
	VerbFlowRead            Verb = "flow.read"
	VerbFlowList            Verb = "flow.list"
	VerbPhaseSet            Verb = "phase.set"
	VerbPhaseComplete       Verb = "phase.complete"
	VerbPhaseBlock          Verb = "phase.block"
	VerbPhaseNeedsAttention Verb = "phase.needs_attention"
	VerbPhaseRestart        Verb = "phase.restart"
	VerbPhaseAddChild       Verb = "phase.add_child"
	VerbPhaseAgentSet       Verb = "phase.agent_set"
	VerbPlanSet             Verb = "plan.set"
	VerbIssueSet            Verb = "issue.set"
	VerbPRSet               Verb = "pr.set"
	VerbMergeSet            Verb = "merge.set"
	VerbFlowCreate          Verb = "flow.create"
	VerbPhaseReset          Verb = "phase.reset"
	VerbPhaseRecover        Verb = "phase.recover"
)

// VerbClass decides what happens to a verb when the control endpoint is set.
type VerbClass int

const (
	// ClassProxiedReplayable verbs travel over the socket, are logged before
	// they are acknowledged, and may be spooled and replayed by a later
	// controller when neither the socket nor a direct store open is possible.
	ClassProxiedReplayable VerbClass = iota
	// ClassProxiedNonReplayable verbs travel over the socket and fall back to a
	// direct store open, but are never spooled: a spooled request that no
	// replay may apply would sit unapplied forever.
	ClassProxiedNonReplayable
	// ClassDirect verbs ignore the endpoint and open the store with their
	// existing role. The store's own compatibility gate makes any incompatible
	// open a loud refusal.
	ClassDirect
)

// verbTable is the one classification. A leaf missing from it is caught by
// the cmd/approach test that walks the CLI's switch statements.
var verbTable = map[Verb]VerbClass{
	VerbFlowRead:            ClassProxiedNonReplayable,
	VerbFlowList:            ClassProxiedNonReplayable,
	VerbPhaseSet:            ClassProxiedReplayable,
	VerbPhaseComplete:       ClassProxiedReplayable,
	VerbPhaseBlock:          ClassProxiedReplayable,
	VerbPhaseNeedsAttention: ClassProxiedReplayable,
	VerbPlanSet:             ClassProxiedReplayable,
	VerbIssueSet:            ClassProxiedReplayable,
	VerbPRSet:               ClassProxiedReplayable,
	VerbMergeSet:            ClassProxiedReplayable,
	VerbPhaseRestart:        ClassProxiedNonReplayable,
	VerbPhaseAddChild:       ClassProxiedNonReplayable,
	VerbPhaseAgentSet:       ClassProxiedNonReplayable,
	VerbFlowCreate:          ClassDirect,
	VerbPhaseReset:          ClassDirect,
	VerbPhaseRecover:        ClassProxiedNonReplayable,
}

// Classify reports the class of v and whether v is a known verb.
func Classify(v Verb) (VerbClass, bool) {
	class, ok := verbTable[v]
	return class, ok
}

// Replayable reports whether v may be spooled and replayed.
func Replayable(v Verb) bool {
	class, ok := verbTable[v]
	return ok && class == ClassProxiedReplayable
}

// IsRead reports whether v reads without mutating. Reads are never logged and
// never spool.
func IsRead(v Verb) bool {
	return v == VerbFlowRead || v == VerbFlowList
}

// AllVerbs lists every known verb in a stable order.
func AllVerbs() []Verb {
	verbs := make([]Verb, 0, len(verbTable))
	for verb := range verbTable {
		verbs = append(verbs, verb)
	}
	slices.Sort(verbs)
	return verbs
}

// Request is one proxied invocation. Payload is the verb's typed payload
// encoded as JSON; Token authenticates the launch to the controller and never
// reaches the log.
type Request struct {
	SchemaVersion int    `json:"schema_version"`
	RequestID     string `json:"request_id"`
	LaunchID      string `json:"launch_id,omitempty"`
	FlowID        string `json:"flow_id,omitempty"`
	PhaseID       string `json:"phase_id,omitempty"`
	// OwnerPhaseID is controller-local context. The authenticated registration
	// or durable launch log supplies it; clients cannot choose it on the wire.
	OwnerPhaseID string          `json:"-"`
	Token        string          `json:"token,omitempty"`
	Verb         Verb            `json:"verb"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// Response is the controller's (or the shared executor's) answer. Refused
// distinguishes a store or validation refusal — final, nothing to retry — from
// a transport failure, which the client reports as ErrUnreachable instead.
type Response struct {
	SchemaVersion int             `json:"schema_version"`
	OK            bool            `json:"ok"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	Refused       bool            `json:"refused,omitempty"`
	// Warning carries a non-fatal diagnostic alongside a successful result,
	// such as a partial `flow list`.
	Warning string `json:"warning,omitempty"`
}

// PhaseSetPayload mirrors `flow phase set`.
type PhaseSetPayload struct {
	Status  string `json:"status"`
	Outcome string `json:"outcome,omitempty"`
	Summary string `json:"summary,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// PhaseActionPayload mirrors `flow phase complete|block|needs-attention`.
type PhaseActionPayload struct {
	Outcome string `json:"outcome,omitempty"`
	Summary string `json:"summary,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// PhaseRestartPayload mirrors `flow phase restart`.
type PhaseRestartPayload struct {
	Notes string `json:"notes,omitempty"`
}

// PhaseResetPayload mirrors `flow phase reset`; the phase is the request's.
type PhaseResetPayload struct{}

// PhaseRecoverPayload is the phase snapshot observed by the command before it
// requests the compare-and-set recovery mutation.
type PhaseRecoverPayload struct {
	ExpectedStatus    string    `json:"expected_status"`
	ExpectedOutcome   string    `json:"expected_outcome"`
	ExpectedLaunchID  string    `json:"expected_launch_id"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

// AddChildPayload mirrors `flow phase add-child`.
type AddChildPayload struct {
	ParentPhaseID string `json:"parent_phase_id"`
	PhaseID       string `json:"phase_id"`
	Title         string `json:"title"`
	Order         int    `json:"order"`
}

// AgentSetPayload mirrors `flow phase agent set`.
type AgentSetPayload struct {
	Agent           string `json:"agent,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Clear           bool   `json:"clear,omitempty"`
}

// PlanSetPayload mirrors `flow plan set`.
type PlanSetPayload struct {
	PlanID   string `json:"plan_id"`
	PlanPath string `json:"plan_path,omitempty"`
}

// IssueSetPayload mirrors `flow issue set`.
type IssueSetPayload struct {
	Provider string `json:"provider"`
	Number   int    `json:"number"`
	URL      string `json:"url"`
}

// PRSetPayload mirrors `flow pr set`.
type PRSetPayload struct {
	Provider string `json:"provider"`
	Number   int    `json:"number"`
	URL      string `json:"url"`
	Head     string `json:"head"`
	Base     string `json:"base"`
	Status   string `json:"status,omitempty"`
}

// MergeSetPayload mirrors `flow merge set`. MergedAt is RFC3339 text so the
// wire form is exactly what the operator typed.
type MergeSetPayload struct {
	Status   string `json:"status"`
	Commit   string `json:"commit,omitempty"`
	MergedAt string `json:"merged_at,omitempty"`
}

// ListPayload mirrors `flow list`.
type ListPayload struct {
	RepoPath string `json:"repo_path,omitempty"`
}

// ReadPayload mirrors `flow read`; the Flow is the request's.
type ReadPayload struct{}

// NewRequest builds a Request for verb with payload encoded as JSON and a fresh
// request ID. Identity fields are left to the caller.
func NewRequest(verb Verb, payload any) (Request, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Request{}, fmt.Errorf("encode %s payload: %w", verb, err)
	}
	return Request{
		SchemaVersion: ProtocolSchemaVersion,
		RequestID:     NewRequestID(),
		Verb:          verb,
		Payload:       data,
	}, nil
}

// NewRequestID mints a random request identifier that is safe as a path
// segment of the launch log.
func NewRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("launchcontrol: random request id: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

func decodePayload(req Request, into any) error {
	if len(req.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Payload, into); err != nil {
		return fmt.Errorf("decode %s payload: %w", req.Verb, err)
	}
	return nil
}
