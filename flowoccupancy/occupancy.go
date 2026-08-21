package flowoccupancy

import (
	"errors"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// Stage names the consumer class asking the question. It is a closed enum: one
// value per group in docs/flow-occupancy-matrix.md section 2, and the value is
// what selects the source set the query is allowed to read.
type Stage int

const (
	// StageUnknown is the zero value and is never a legal query. A caller that
	// forgot its stage must not silently get the cheapest answer.
	StageUnknown Stage = iota
	// StagePreview renders a footer affordance or a preview, every frame.
	// Cached sources only: no store walk, no subprocess.
	StagePreview
	// StageAdmission decides a keypress admission. In-process sources plus a
	// lease inspect; still no session-store walk.
	StageAdmission
	// StageAutoAdvance is the AutoMode advance poll's own admission and read. It
	// is a stage of its own rather than StageAdmission with a flag because the
	// poll's source set genuinely differs: it never reads the pending headless
	// write, it refuses in silence, and its read adds the other-running-phase
	// check. Freshness splits its two halves — FreshnessCached is the admission
	// gate, FreshnessAuthoritative the read.
	StageAutoAdvance
	// StageAuthoritative runs inside a tea.Cmd read stage and may read the Flow
	// store and the session store.
	StageAuthoritative
	// StageReserved runs under the cross-process launch reservation and
	// re-inspects the lease before anything is written.
	StageReserved
	// StageInstall is the last check before an embedded terminal slot is
	// allocated. Slot sources only, and deliberately per role.
	StageInstall
	// StageDrain runs at 1 Hz from the AutoMode poll. Cached only, and it must
	// never fork a subprocess.
	StageDrain
	// StageSessionRelease is the non-launch release gesture, which asks about
	// the launch lifecycle rather than about a launch of its own.
	StageSessionRelease
)

// String reports the stage's name for diagnostics.
func (stage Stage) String() string {
	switch stage {
	case StagePreview:
		return "preview"
	case StageAdmission:
		return "admission"
	case StageAutoAdvance:
		return "autoAdvance"
	case StageAuthoritative:
		return "authoritative"
	case StageReserved:
		return "reserved"
	case StageInstall:
		return "install"
	case StageDrain:
		return "drain"
	case StageSessionRelease:
		return "sessionRelease"
	default:
		return "unknown"
	}
}

// Freshness selects between the cached mirrors and the authoritative stores.
// Almost every caller passes FreshnessDefault and lets the stage decide; the
// field exists because one real pair of consumers disagrees at the same stage:
// StageAutoAdvance's admission gate and its read ask the same role the same
// question and need answers of different freshness.
type Freshness int

const (
	// FreshnessDefault resolves from the query's Stage.
	FreshnessDefault Freshness = iota
	// FreshnessCached reads display mirrors and in-process runtime state only.
	FreshnessCached
	// FreshnessAuthoritative reads the Flow store, the session store, and the
	// lease.
	FreshnessAuthoritative
)

// Purpose is why the caller is asking. Role selects the refusal vocabulary and
// the ordering; Stage selects the source set. Keeping them as one pair rather
// than a flat enum is what lets a rule be stated about a stage across every
// role — see decision request Q1 in ADR 0003.
type Purpose struct {
	Role  actions.FlowLaunchRole
	Stage Stage
}

// Valid reports whether this purpose names a consumer that exists.
// StageSessionRelease, the one non-launch stage, carries actions.RoleNone;
// every other stage requires a real role. There is deliberately no quit stage:
// the handoff-pending check quit defers on is a process-wide policy rather than
// a per-Flow question, so it stays in model. See ADR 0003 D5.
func (purpose Purpose) Valid() bool {
	// implemented in approach-x0r.3
	return false
}

// SessionIdentity exempts one provider session from the session-occupancy rule.
// Phase resume is the only consumer that passes one: the session it is
// reattaching to is expected to look live, so counting it would refuse every
// resume. The zero value exempts nothing.
type SessionIdentity struct {
	Provider  string
	SessionID string
}

// Query is one occupancy question.
type Query struct {
	// FlowID is an exact canonical Flow ID. Prefix-like IDs never collide,
	// matching the attempt map's own rule.
	FlowID  string
	Purpose Purpose
	// Freshness overrides the stage's default. Leave it zero unless the stage
	// genuinely serves two consumers of different freshness.
	Freshness Freshness
	// PhaseID scopes a phase-scoped question. Blank means Flow-scoped, which is
	// what repair, autofix, the worktree agent, and creation ask.
	PhaseID string
	// SkipSession is phase resume's exemption. Zero for every other caller.
	SkipSession SessionIdentity
}

// Holder names what is occupying the Flow. It is the only vocabulary callers
// see; the sixteen underlying representations stay inside this package.
type Holder int

const (
	// HolderNone means the Flow is free.
	HolderNone Holder = iota
	// HolderLeaseUnreadable means occupancy could not be determined. It is
	// occupancy, fail-closed, under every purpose.
	HolderLeaseUnreadable
	// HolderPeerLease means another process holds the tracked tmux lease.
	HolderPeerLease
	// HolderRepairAttempt means an in-process repair launch holds the Flow.
	HolderRepairAttempt
	// HolderPhaseResumeAttempt means an in-process phase resume holds it.
	HolderPhaseResumeAttempt
	// HolderPhaseAttempt means an in-process manual, auto, or create phase
	// launch holds it.
	HolderPhaseAttempt
	// HolderOtherAttempt means an in-process launch of some other role holds
	// it.
	HolderOtherAttempt
	// HolderTmuxAgent means a phase-untracked agent still has a live tmux
	// window in this Flow's worktree.
	HolderTmuxAgent
	// HolderRepairTerminal means a retained repair terminal slot holds the
	// Flow, whether or not its terminal is live.
	HolderRepairTerminal
	// HolderFlowTerminal means a retained Flow terminal slot with a live
	// terminal holds it.
	HolderFlowTerminal
	// HolderRunningPhase means the persisted record says a phase is running.
	HolderRunningPhase
	// HolderPhaseSession means a live session record is attached to a phase of
	// this Flow.
	HolderPhaseSession
	// HolderFlowSession means a live session record names this Flow without
	// naming a phase of it.
	HolderFlowSession
	// HolderRepairDrain means a repair outcome has not been consumed by the
	// AutoMode poll yet.
	HolderRepairDrain
	// HolderHeadlessWrite means a headless toggle is in flight. It is the one
	// transient holder and is ranked last everywhere.
	HolderHeadlessWrite
)

// String reports the holder's name for diagnostics. It is not the user-facing
// text; that is Verdict.Reason, which varies by purpose for the same holder.
func (holder Holder) String() string {
	switch holder {
	case HolderLeaseUnreadable:
		return "leaseUnreadable"
	case HolderPeerLease:
		return "peerLease"
	case HolderRepairAttempt:
		return "repairAttempt"
	case HolderPhaseResumeAttempt:
		return "phaseResumeAttempt"
	case HolderPhaseAttempt:
		return "phaseAttempt"
	case HolderOtherAttempt:
		return "otherAttempt"
	case HolderTmuxAgent:
		return "tmuxAgent"
	case HolderRepairTerminal:
		return "repairTerminal"
	case HolderFlowTerminal:
		return "flowTerminal"
	case HolderRunningPhase:
		return "runningPhase"
	case HolderPhaseSession:
		return "phaseSession"
	case HolderFlowSession:
		return "flowSession"
	case HolderRepairDrain:
		return "repairDrain"
	case HolderHeadlessWrite:
		return "headlessWrite"
	default:
		return "none"
	}
}

// Verdict is the answer. Its fields are unexported so a consumer cannot pattern
// match on a representation this package chose not to expose.
type Verdict struct {
	holder  Holder
	reason  string
	phaseID string
	err     error
}

// Occupied reports whether anything holds the Flow for this purpose.
func (verdict Verdict) Occupied() bool { return verdict.holder != HolderNone }

// Holder names what holds it, or HolderNone.
func (verdict Verdict) Holder() Holder { return verdict.holder }

// Reason is the user-facing refusal for this purpose, or "" when the Flow is
// free or the purpose refuses in silence. The same holder yields different text
// for different roles by design.
func (verdict Verdict) Reason() string { return verdict.reason }

// PhaseID names the phase the holder occupies, when the holder is phase-scoped
// and the refusal names it. Blank otherwise.
func (verdict Verdict) PhaseID() string { return verdict.phaseID }

// Err reports a source failure that was not itself occupancy. A lease that
// could not be read is HolderLeaseUnreadable rather than an error, because
// fail-closed occupancy is the answer; a Flow store or session store read that
// failed has no safe answer and surfaces here.
func (verdict Verdict) Err() error { return verdict.err }

// FlowReader reads a Flow authoritatively.
type FlowReader interface {
	ReadFlow(flowID string) (flowstore.FlowRecord, error)
}

// FlowCache answers from the display mirror the poll already carries.
type FlowCache interface {
	CachedFlow(flowID string) (flowstore.FlowRecord, bool)
}

// SessionStore lists this Flow's session records authoritatively.
type SessionStore interface {
	ListFlowSessions(flowID string) ([]sessions.SessionRecord, error)
}

// SessionCache answers from the mirrored session panes.
type SessionCache interface {
	ActiveFlowSessions(flowID string) []sessions.SessionRecord
}

// LeaseInspector reports the cross-process tracked tmux lease. An error is
// occupancy, not a failure.
type LeaseInspector interface {
	FlowLeaseOccupied(flowID string) (bool, error)
}

// Runtime is the in-process launch state. It is one interface rather than five
// because a single value implements all of it, and splitting it would produce
// five one-method adapters over the same receiver.
type Runtime interface {
	// AttemptHolder names the role of the in-process launch attempt holding
	// this Flow, if any.
	AttemptHolder(flowID string) (actions.FlowLaunchRole, bool)
	// HasFlowTerminal reports a retained Flow terminal slot with a live
	// terminal.
	HasFlowTerminal(flowID string) bool
	// HasRepairTerminal reports a retained repair slot. It overlaps
	// HasFlowTerminal rather than nesting inside it: one requires a live
	// terminal, the other a repair slot.
	HasRepairTerminal(flowID string) bool
	// HeadlessWritePending reports an in-flight headless toggle.
	HeadlessWritePending(flowID string) bool
	// RepairDrainPending reports a repair outcome the AutoMode poll has not
	// consumed.
	RepairDrainPending(flowID string) bool
}

// AgentProbe reports a live tmux window for a phase-untracked agent. It forks a
// subprocess, so it is nil-able and this package consults it only at
// StageAdmission and StageAuthoritative.
type AgentProbe interface {
	TmuxAgentRunning(flowID string) bool
}

// Sources is the set of adapters an Occupancy answers from. Every field is
// optional in the sense that a stage that does not read it may leave it nil;
// a stage that does read it and finds nil yields Verdict.Err.
type Sources struct {
	Flows     FlowReader
	FlowCache FlowCache
	Sessions  SessionStore
	Cache     SessionCache
	Lease     LeaseInspector
	Runtime   Runtime
	Probe     AgentProbe
}

// Occupancy answers occupancy queries against one set of sources.
type Occupancy struct {
	sources Sources
}

// New binds an Occupancy to its sources.
func New(sources Sources) Occupancy {
	return Occupancy{sources: sources}
}

// ErrUnimplemented is the error every Query carries until approach-x0r.3 lands
// the source reads. It exists so a caller migrated ahead of the implementation
// fails loudly instead of being told the Flow is free.
var ErrUnimplemented = errors.New("flowoccupancy: Query is not implemented yet")

// Query answers one occupancy question. A query whose purpose is not Valid
// yields an occupied fail-closed verdict rather than a free one.
func (occupancy Occupancy) Query(query Query) Verdict {
	// implemented in approach-x0r.3. Until then every purpose is invalid, so the
	// fail-closed branch above is the whole behavior: occupied, with an error.
	return Verdict{holder: HolderLeaseUnreadable, err: ErrUnimplemented}
}

// Free is the verdict for an unoccupied Flow. It exists so callers and tests
// can express "nothing holds it" without constructing a zero Verdict literal
// and depending on the zero value staying free.
func Free() Verdict { return Verdict{} }
