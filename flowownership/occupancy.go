package flowownership

import (
	"errors"
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
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
	// StagePreview answers a launch preview from cached sources only. Footer-only
	// occupancy belongs to StageFooter.
	StagePreview
	// StageFooter renders a footer affordance every frame. It is separate from
	// StagePreview because some footers add occupancy that their launch preview
	// deliberately ignores.
	StageFooter
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
	// StageLeasePreflight is the lease-only check before a lifecycle has enough
	// authoritative Flow context to enter its reservation-protected recheck.
	StageLeasePreflight
	// StageReserved runs under the cross-process launch reservation and
	// re-inspects the lease before anything is written.
	StageReserved
	// StageInstall is the last check before an embedded terminal slot is
	// allocated. Slot sources only, and deliberately per role.
	StageInstall
	// StageDrain runs at 1 Hz from the AutoMode poll. Cached only, and it must
	// never fork a subprocess.
	StageDrain
	// StageDrainControl decides whether repair state disarms the AutoMode drain.
	// It stays separate from StageDrain's launch gate because repair state does
	// not occupy that gate.
	StageDrainControl
	// StageSessionRelease is the non-launch release gesture, which asks about
	// the launch lifecycle rather than about a launch of its own.
	StageSessionRelease
)

// String reports the stage's name for diagnostics.
func (stage Stage) String() string {
	switch stage {
	case StagePreview:
		return "preview"
	case StageFooter:
		return "footer"
	case StageAdmission:
		return "admission"
	case StageAutoAdvance:
		return "autoAdvance"
	case StageAuthoritative:
		return "authoritative"
	case StageLeasePreflight:
		return "leasePreflight"
	case StageReserved:
		return "reserved"
	case StageInstall:
		return "install"
	case StageDrain:
		return "drain"
	case StageDrainControl:
		return "drainControl"
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

// Purpose is why the caller is asking. Role selects the ordering, Stage selects
// the source set. They stay a pair rather than one flat enum because that is
// what lets a rule be stated about a stage across every role: "no StagePreview
// query reaches ListFlowSessions" is expressible only if Stage is a value this
// package can quantify over. ADR 0003, decision Q1.
type Purpose struct {
	Role  actions.FlowLaunchRole
	Stage Stage
}

// Valid reports whether this purpose names a consumer that exists. It is backed
// by the purpose registry: one row per real consumer policy in
// docs/flow-occupancy-matrix.md section 2, each carrying the source set that
// purpose may read and the probe method, if any, it may call.
//
// The registry rather than the type system is what closes phantom pairs like
// (RoleAutofix, StageDrain). An invalid purpose yields a fail-closed occupied
// verdict carrying Err, never a panic: a TUI must not crash on a programming
// error. approach-x0r.11 asserts the registry both ways, so a consumer with no
// purpose and a purpose with no caller are both build failures.
//
// The session-release footer and gesture carry actions.RoleNone; every launch
// purpose requires a real role. There is deliberately no quit stage: the
// handoff-pending check quit defers on is a process-wide policy rather than a
// per-Flow question, so it stays in model. See ADR 0003 D5.
func (purpose Purpose) Valid() bool {
	_, ok := purposeRegistry[purpose]
	return ok
}

type sourcePermission uint16

const (
	readRuntime sourcePermission = 1 << iota
	readLease
	readFlowCache
	readFlowStore
	readSessionCache
	readSessionStore
)

type probeChoice uint8

const (
	probeNone probeChoice = iota
	probeAutofixAgent
	probeFlowAgent
)

type runtimePermission uint8

const (
	readAttempt runtimePermission = 1 << iota
	readFlowTerminal
	readRepairTerminal
	readFlowTerminalWithoutRepair
	readHeadlessWrite
	readRepairDrain
)

const readAdmissionRuntime = readAttempt | readFlowTerminal | readRepairTerminal

type freshnessPermission uint8

const (
	allowCached freshnessPermission = 1 << iota
	allowAuthoritative
)

type purposePolicy struct {
	sources   sourcePermission
	runtime   runtimePermission
	probe     probeChoice
	freshness freshnessPermission
}

// purposeRegistry is the executable transcription of the consumers in
// docs/flow-occupancy-matrix.md section 2. A row records every source family a
// purpose may eventually read, including families implemented by later slices.
var purposeRegistry = map[Purpose]purposePolicy{
	// Matrix section 2.1: manual phase admission and its g-key tmux probe.
	{Role: actions.RoleTrackedPhase, Stage: StageAdmission}: {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime | readHeadlessWrite, probe: probeAutofixAgent, freshness: allowAuthoritative},
	// Matrix section 2.1: creation-time Plan Now admission is runtime-only.
	{Role: actions.RoleCreatePhase, Stage: StageAdmission}: {sources: readRuntime, runtime: readAdmissionRuntime, freshness: allowAuthoritative},
	// Matrix section 2.1: phase-resume admission and its keypress probe.
	{Role: actions.RolePhaseResume, Stage: StageAdmission}: {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime, probe: probeAutofixAgent, freshness: allowAuthoritative},
	// Matrix section 2.1: repair admission. The whole-Flow tmux probe remains a
	// separate keypress check because it shells out and is not part of admission.
	{Role: actions.RoleRepair, Stage: StageAdmission}: {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime | readHeadlessWrite, freshness: allowAuthoritative},
	// Matrix section 2.1: autofix admission and its whole-Flow agent probe.
	{Role: actions.RoleAutofix, Stage: StageAdmission}: {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime | readHeadlessWrite, probe: probeFlowAgent, freshness: allowAuthoritative},
	// Matrix section 2.1: worktree-agent admission reads every source family.
	{Role: actions.RoleWorktreeAgent, Stage: StageAdmission}: {sources: readRuntime | readLease | readFlowCache | readSessionCache, runtime: readAdmissionRuntime, probe: probeAutofixAgent, freshness: allowAuthoritative},
	// Matrix section 2.1: saved-session resume admits after resolving its Flow.
	{Role: actions.RoleSavedSessionResume, Stage: StageAdmission}: {sources: readRuntime | readLease, runtime: readAdmissionRuntime, freshness: allowAuthoritative},

	// Matrix section 2.2: authoritative lifecycle reads.
	{Role: actions.RoleTrackedPhase, Stage: StageAuthoritative}:       {sources: readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleCreatePhase, Stage: StageAuthoritative}:        {sources: readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RolePhaseResume, Stage: StageAuthoritative}:        {sources: readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleRepair, Stage: StageAuthoritative}:             {sources: readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleAutofix, Stage: StageAuthoritative}:            {sources: readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleWorktreeAgent, Stage: StageAuthoritative}:      {sources: readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleSavedSessionResume, Stage: StageAuthoritative}: {sources: readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleTrackedPhase, Stage: StageLeasePreflight}:      {sources: readLease, freshness: allowAuthoritative},

	// Matrix section 2.2: prepare stages recheck under the reservation.
	{Role: actions.RoleTrackedPhase, Stage: StageReserved}:       {sources: readLease | readFlowStore, freshness: allowAuthoritative},
	{Role: actions.RolePhaseResume, Stage: StageReserved}:        {sources: readLease | readFlowStore, freshness: allowAuthoritative},
	{Role: actions.RoleRepair, Stage: StageReserved}:             {sources: readLease | readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleAutofix, Stage: StageReserved}:            {sources: readLease | readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleWorktreeAgent, Stage: StageReserved}:      {sources: readLease | readFlowStore | readSessionStore, freshness: allowAuthoritative},
	{Role: actions.RoleSavedSessionResume, Stage: StageReserved}: {sources: readLease | readFlowStore | readSessionStore, freshness: allowAuthoritative},

	// Matrix section 2.2 and section 4.2: every installed launch kind has a
	// terminal-slot backstop.
	{Role: actions.RoleTrackedPhase, Stage: StageInstall}:       {sources: readRuntime, runtime: readRepairTerminal, freshness: allowAuthoritative},
	{Role: actions.RoleCreatePhase, Stage: StageInstall}:        {sources: readRuntime, runtime: readFlowTerminal | readRepairTerminal, freshness: allowAuthoritative},
	{Role: actions.RolePhaseResume, Stage: StageInstall}:        {sources: readRuntime, runtime: readRepairTerminal, freshness: allowAuthoritative},
	{Role: actions.RoleRepair, Stage: StageInstall}:             {sources: readRuntime, runtime: readFlowTerminal | readRepairTerminal, freshness: allowAuthoritative},
	{Role: actions.RoleAutofix, Stage: StageInstall}:            {sources: readRuntime, runtime: readRepairTerminal, freshness: allowAuthoritative},
	{Role: actions.RoleWorktreeAgent, Stage: StageInstall}:      {sources: readRuntime, runtime: readFlowTerminal | readRepairTerminal, freshness: allowAuthoritative},
	{Role: actions.RoleSavedSessionResume, Stage: StageInstall}: {sources: readRuntime, runtime: readFlowTerminal, freshness: allowAuthoritative},

	// Matrix section 2.3: launch previews omit footer-only occupancy terms.
	// Phase resume reuses its preview purpose as the no-I/O admission advisory;
	// the source handler retains the separate tmux probe.
	{Role: actions.RoleTrackedPhase, Stage: StagePreview}: {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime, freshness: allowCached},
	{Role: actions.RolePhaseResume, Stage: StagePreview}:  {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime, freshness: allowCached},
	{Role: actions.RoleRepair, Stage: StagePreview}:       {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime, freshness: allowCached},
	// Session release uses the RoleNone preview to fence its local checks before
	// the authoritative session-store probe.
	{Role: actions.RoleNone, Stage: StagePreview}: {sources: readRuntime, runtime: readAttempt | readHeadlessWrite, freshness: allowCached},
	// Matrix section 2.3: rendered affordances use mirrors only. Tracked phase
	// and repair add the headless-write term their launch previews omit.
	{Role: actions.RoleTrackedPhase, Stage: StageFooter}:  {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime | readHeadlessWrite, freshness: allowCached},
	{Role: actions.RolePhaseResume, Stage: StageFooter}:   {sources: readRuntime | readLease | readFlowCache, runtime: readFlowTerminalWithoutRepair, freshness: allowCached},
	{Role: actions.RoleRepair, Stage: StageFooter}:        {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime | readHeadlessWrite, freshness: allowCached},
	{Role: actions.RoleAutofix, Stage: StageFooter}:       {sources: readRuntime | readLease | readFlowCache, runtime: readAdmissionRuntime | readHeadlessWrite, freshness: allowCached},
	{Role: actions.RoleWorktreeAgent, Stage: StageFooter}: {sources: readRuntime | readLease | readFlowCache | readSessionCache, runtime: readAdmissionRuntime, freshness: allowCached},
	{Role: actions.RoleNone, Stage: StageFooter}:          {sources: readFlowCache, freshness: allowCached},

	// Matrix sections 2.1 and 2.2: AutoMode has cached and authoritative halves.
	{Role: actions.RoleTrackedPhase, Stage: StageAutoAdvance}: {sources: readRuntime | readLease | readFlowStore | readSessionStore, runtime: readAdmissionRuntime, freshness: allowCached | allowAuthoritative},
	// Matrix section 2.4: the 1 Hz drain gate reads runtime state and its cached
	// Flow record. Repair state belongs to the separate arm/disarm consumer.
	{Role: actions.RoleTrackedPhase, Stage: StageDrain}:        {sources: readRuntime | readLease | readFlowCache, runtime: readAttempt | readFlowTerminal, freshness: allowCached},
	{Role: actions.RoleTrackedPhase, Stage: StageDrainControl}: {sources: readRuntime, runtime: readRepairTerminal | readRepairDrain, freshness: allowCached},
	// Matrix section 2.5: the release gesture adds launch-lifecycle and
	// authoritative session checks to the footer's cached Flow check.
	{Role: actions.RoleNone, Stage: StageSessionRelease}: {sources: readRuntime | readFlowCache | readSessionStore, runtime: readAttempt | readHeadlessWrite, freshness: allowAuthoritative},
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
	// FallbackRepoPath locates the worktree when the record does not. Only the
	// purposes that may reach AgentProbe need it; it rides on the query rather
	// than on the adapter because it varies per call site, and re-deriving it
	// inside this package would mean a store read at a stage that forbids one.
	FallbackRepoPath string
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
	// HolderUntrackedOwner is the durable phase-untracked Flow-level owner.
	HolderUntrackedOwner
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

// String reports the holder's name for diagnostics. It is never user-facing
// text. This package owns which holder wins and in what order, as one flat
// table with an ordered row per Role and no default; model owns the copy, as
// one table keyed by (Role, Holder). ADR 0003, decisions Q2 and Q3.
func (holder Holder) String() string {
	switch holder {
	case HolderNone:
		return "none"
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
	case HolderUntrackedOwner:
		return "untrackedOwner"
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
		return "unknown"
	}
}

// Verdict is the answer. Its fields are unexported so a consumer cannot pattern
// match on a representation this package chose not to expose.
type Verdict struct {
	holder  Holder
	phaseID string
	err     error
}

// Occupied reports whether anything holds the Flow for this purpose.
func (verdict Verdict) Occupied() bool { return verdict.holder != HolderNone || verdict.err != nil }

// Holder names what holds it, or HolderNone.
func (verdict Verdict) Holder() Holder { return verdict.holder }

// PhaseID names the phase the holder occupies, when the holder is phase-scoped
// and the refusal names it. Blank otherwise.
func (verdict Verdict) PhaseID() string { return verdict.phaseID }

// Err reports the source failure behind a fail-closed answer. Lease inspection
// failures retain their original error so presentation code can explain the
// refusal while HolderLeaseUnreadable remains the module decision.
func (verdict Verdict) Err() error { return verdict.err }

// Advisory is cached evidence that may only defer work. It deliberately has no
// inverse operation: a caller can filter on Defer, but cannot turn a cache miss
// into permission to mutate state.
type Advisory struct {
	verdict Verdict
}

// Defer reports whether cached evidence says the caller should wait.
func (advisory Advisory) Defer() bool { return advisory.verdict.Occupied() }

// Holder names the cached holder that caused the deferral.
func (advisory Advisory) Holder() Holder { return advisory.verdict.Holder() }

// PhaseID names the cached phase holder, when one was found.
func (advisory Advisory) PhaseID() string { return advisory.verdict.PhaseID() }

// Err reports a fail-closed cached-source failure.
func (advisory Advisory) Err() error { return advisory.verdict.Err() }

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
	// HasNonRepairFlowTerminal reports a retained live Flow terminal that is
	// not itself a repair slot.
	HasNonRepairFlowTerminal(flowID string) bool
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
// subprocess, so it is nil-able and the purpose registry decides which method,
// if any, a purpose may reach.
//
// The two methods are not one method with a flag. FlowAgentRunning unions the
// record's phase launch IDs with the autofix registry and serves repair;
// AutofixAgentRunning is the registry half alone and serves tracked-phase,
// phase-resume, and worktree-agent admission. Widening those probes to every
// phase of the record would newly refuse a tracked launch for a finished agent
// whose window the user merely left open (model/tmux_mode.go:435-438), so
// collapsing them is a behavior change, not a simplification.
//
// Both take the record and a fallback repo path because both must locate the
// worktree, and a flow ID alone cannot without a store read.
type AgentProbe interface {
	FlowAgentRunning(record flowstore.FlowRecord, fallbackRepoPath string) bool
	AutofixAgentRunning(record flowstore.FlowRecord, fallbackRepoPath string) bool
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

// Evaluate answers one query against a short-lived source set. It is the
// compact form for callers that do not retain an Occupancy between queries.
func Evaluate(sources Sources, query Query) Verdict {
	return New(sources).Query(query)
}

// EvaluateAdvisory answers one cached advisory query against a short-lived
// source set.
func EvaluateAdvisory(sources Sources, query Query) Advisory {
	return New(sources).Advise(query)
}

var (
	// ErrInvalidQuery marks a programming error in a query. Query fails closed
	// instead of panicking because it runs inside the TUI update loop.
	ErrInvalidQuery = errors.New("flowownership: invalid query")
	// ErrMissingRuntime marks a purpose that needs the in-process adapter but
	// received none.
	ErrMissingRuntime = errors.New("flowownership: runtime source is required")
	// ErrMissingLease marks a purpose that needs the cross-process lease adapter
	// but received none.
	ErrMissingLease = errors.New("flowownership: lease source is required")
	// ErrMissingFlowCache marks a cached purpose without its Flow mirror.
	ErrMissingFlowCache = errors.New("flowownership: cached Flow source is required")
	// ErrMissingSessionCache marks a cached purpose without its session panes.
	ErrMissingSessionCache = errors.New("flowownership: cached session source is required")
	// ErrMissingFlowStore marks an authoritative purpose without its Flow reader.
	ErrMissingFlowStore = errors.New("flowownership: authoritative Flow source is required")
	// ErrMissingSessionStore marks an authoritative purpose without its session reader.
	ErrMissingSessionStore = errors.New("flowownership: authoritative session source is required")
	errPendingSourceFamily = errors.New("flowownership: a required source family is not implemented yet")
)

// Advise answers a cached query that may only defer its caller.
func (occupancy Occupancy) Advise(query Query) Advisory {
	flowID := strings.TrimSpace(query.FlowID)
	if flowID == "" {
		return Advisory{verdict: failedVerdict(fmt.Errorf("%w: Flow ID is required", ErrInvalidQuery))}
	}
	policy, ok := purposeRegistry[query.Purpose]
	if !ok {
		return Advisory{verdict: failedVerdict(fmt.Errorf("%w: purpose (%s, %s) is not registered", ErrInvalidQuery, query.Purpose.Role, query.Purpose.Stage))}
	}
	freshness, ok := resolveFreshness(query.Purpose.Stage, query.Freshness)
	if !ok || freshness != FreshnessCached || !policy.freshness.allows(freshness) {
		return Advisory{verdict: failedVerdict(fmt.Errorf("%w: purpose (%s, %s) is not a cached advisory query", ErrInvalidQuery, query.Purpose.Role, query.Purpose.Stage))}
	}
	if policy.sources&(readFlowStore|readSessionStore) != 0 || policy.probe != probeNone {
		return Advisory{verdict: failedVerdict(fmt.Errorf("%w: cached advisory query requests an authoritative source", ErrInvalidQuery))}
	}

	lease := Free()
	if policy.sources&readLease != 0 {
		lease = queryLease(occupancy.sources.Lease, flowID)
	}
	runtime := Free()
	if policy.sources&readRuntime != 0 {
		if occupancy.sources.Runtime == nil {
			return Advisory{verdict: failedVerdict(ErrMissingRuntime)}
		}
		if query.Purpose.Role == actions.RoleAutofix && query.Purpose.Stage == StageFooter {
			runtime = queryAutofixRuntime(policy.runtime, occupancy.sources.Runtime, flowID)
		} else if query.Purpose.Role == actions.RolePhaseResume && query.Purpose.Stage == StagePreview &&
			occupancy.sources.Runtime.HasFlowTerminal(flowID) && !occupancy.sources.Runtime.HasRepairTerminal(flowID) {
			runtime = Verdict{holder: HolderFlowTerminal}
		} else {
			runtime = queryRuntime(policy.runtime, occupancy.sources.Runtime, flowID)
		}
	}
	flowCache := Free()
	if policy.sources&readFlowCache != 0 {
		if occupancy.sources.FlowCache == nil {
			return Advisory{verdict: failedVerdict(ErrMissingFlowCache)}
		}
		flowCache = queryFlowCache(occupancy.sources.FlowCache, flowID, strings.TrimSpace(query.PhaseID), query.Purpose)
	}
	sessionCache := Free()
	if policy.sources&readSessionCache != 0 {
		if occupancy.sources.Cache == nil {
			return Advisory{verdict: failedVerdict(ErrMissingSessionCache)}
		}
		sessionCache = querySessionCache(occupancy.sources.Cache, flowID)
	}

	return Advisory{verdict: chooseAdvisory(query.Purpose, lease, runtime, flowCache, sessionCache)}
}

func queryFlowCache(cache FlowCache, flowID, phaseID string, purpose Purpose) Verdict {
	record, ok := cache.CachedFlow(flowID)
	if !ok {
		return Free()
	}
	if strings.TrimSpace(record.FlowID) != flowID {
		return failedVerdict(fmt.Errorf("%w: cached Flow identity does not match %q", ErrInvalidQuery, flowID))
	}
	if untrackedOwnerActive(record.UntrackedOwner) {
		return Verdict{holder: HolderUntrackedOwner}
	}
	if purpose.Stage == StagePreview || purpose.Stage == StageFooter || purpose.Stage == StageAdmission {
		switch purpose.Role {
		case actions.RoleTrackedPhase, actions.RolePhaseResume, actions.RoleRepair, actions.RoleAutofix:
			return Free()
		}
	}
	for _, phase := range record.Phases {
		currentPhaseID := strings.TrimSpace(phase.PhaseID)
		if phaseID != "" && currentPhaseID != phaseID {
			continue
		}
		readPhaseSessions := purpose.Role == actions.RoleWorktreeAgent || phaseID != ""
		if readPhaseSessions && phaseHasLiveSession(phase) {
			return Verdict{holder: HolderPhaseSession, phaseID: currentPhaseID}
		}
		readRunningPhases := purpose.Stage != StageDrain || phaseID == ""
		if readRunningPhases && phase.Status == flowstore.PhaseRunning {
			return Verdict{holder: HolderRunningPhase, phaseID: currentPhaseID}
		}
	}
	return Free()
}

func phaseHasLiveSession(phase flowstore.FlowPhase) bool {
	return phaseHasLiveSessionExcept(phase, SessionIdentity{})
}

func phaseHasLiveSessionExcept(phase flowstore.FlowPhase, skip SessionIdentity) bool {
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID = strings.TrimSpace(launchID); launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, session := range phase.Sessions {
		if strings.TrimSpace(session.SessionID) == "" || skip.matches(session.Provider, session.SessionID) {
			continue
		}
		if _, ok := launches[strings.TrimSpace(session.LaunchID)]; !ok {
			continue
		}
		if sessions.IsActive(session.Status, session.EndedAt) {
			return true
		}
	}
	return false
}

func querySessionCache(cache SessionCache, flowID string) Verdict {
	for _, record := range cache.ActiveFlowSessions(flowID) {
		if strings.TrimSpace(record.FlowID) == flowID && sessions.IsActive(record.Status, record.EndedAt) {
			return Verdict{holder: HolderFlowSession}
		}
	}
	return Free()
}

func chooseAdvisory(purpose Purpose, lease, runtime, flowCache, sessionCache Verdict) Verdict {
	if purpose.Role == actions.RoleTrackedPhase && purpose.Stage == StageFooter && runtime.Holder() == HolderHeadlessWrite {
		return runtime
	}
	if lease.Occupied() {
		return lease
	}
	if purpose.Role == actions.RoleWorktreeAgent {
		if runtime.Occupied() {
			return runtime
		}
		if sessionCache.Occupied() {
			return sessionCache
		}
		return flowCache
	}
	switch runtime.Holder() {
	case HolderRepairAttempt, HolderPhaseResumeAttempt, HolderPhaseAttempt, HolderOtherAttempt:
		return runtime
	}
	if flowCache.Occupied() {
		return flowCache
	}
	return runtime
}

// Query answers one occupancy question. A query whose purpose is not Valid
// yields an occupied fail-closed verdict rather than a free one.
func (occupancy Occupancy) Query(query Query) Verdict {
	flowID := strings.TrimSpace(query.FlowID)
	if flowID == "" {
		return failedVerdict(fmt.Errorf("%w: Flow ID is required", ErrInvalidQuery))
	}
	policy, ok := purposeRegistry[query.Purpose]
	if !ok {
		return failedVerdict(fmt.Errorf("%w: purpose (%s, %s) is not registered", ErrInvalidQuery, query.Purpose.Role, query.Purpose.Stage))
	}
	freshness, ok := resolveFreshness(query.Purpose.Stage, query.Freshness)
	if !ok || !policy.freshness.allows(freshness) {
		return failedVerdict(fmt.Errorf("%w: freshness %d is unsupported for purpose (%s, %s)", ErrInvalidQuery, query.Freshness, query.Purpose.Role, query.Purpose.Stage))
	}
	if freshness == FreshnessAuthoritative && policy.sources&(readFlowStore|readSessionStore) != 0 {
		return occupancy.queryAuthoritative(query, policy, flowID)
	}
	if policy.sources&^(readRuntime|readLease|readFlowCache) != 0 || policy.probe != probeNone {
		return failedVerdict(errPendingSourceFamily)
	}

	runtimeVerdict := Free()
	if policy.sources&readRuntime != 0 {
		if occupancy.sources.Runtime == nil {
			return failedVerdict(ErrMissingRuntime)
		}
		if query.Purpose.Role == actions.RoleTrackedPhase && query.Purpose.Stage == StageFooter &&
			policy.runtime&readHeadlessWrite != 0 && occupancy.sources.Runtime.HeadlessWritePending(flowID) {
			runtimeVerdict = Verdict{holder: HolderHeadlessWrite}
		} else if query.Purpose.Role == actions.RoleTrackedPhase && query.Purpose.Stage == StageFooter &&
			policy.runtime&readFlowTerminal != 0 && occupancy.sources.Runtime.HasNonRepairFlowTerminal(flowID) {
			runtimeVerdict = Verdict{holder: HolderFlowTerminal}
		} else {
			runtimeVerdict = queryRuntime(policy.runtime, occupancy.sources.Runtime, flowID)
		}
	}
	leaseVerdict := Free()
	if policy.sources&readLease != 0 {
		leaseVerdict = queryLease(occupancy.sources.Lease, flowID)
	}
	flowCacheVerdict := Free()
	if policy.sources&readFlowCache != 0 {
		if occupancy.sources.FlowCache == nil {
			return failedVerdict(ErrMissingFlowCache)
		}
		flowCacheVerdict = queryFlowCache(occupancy.sources.FlowCache, flowID, strings.TrimSpace(query.PhaseID), query.Purpose)
	}
	return chooseAdvisory(query.Purpose, leaseVerdict, runtimeVerdict, flowCacheVerdict, Free())
}

func (occupancy Occupancy) queryAuthoritative(query Query, policy purposePolicy, flowID string) Verdict {
	if query.Purpose.Stage != StageAuthoritative && query.Purpose.Stage != StageAutoAdvance && query.Purpose.Stage != StageReserved {
		return failedVerdict(errPendingSourceFamily)
	}
	if query.Purpose.Stage == StageReserved && policy.sources&readLease != 0 {
		if verdict := queryLease(occupancy.sources.Lease, flowID); verdict.Occupied() {
			return verdict
		}
	}
	if policy.sources&readFlowStore == 0 {
		return failedVerdict(errPendingSourceFamily)
	}
	if occupancy.sources.Flows == nil {
		return failedVerdict(ErrMissingFlowStore)
	}
	record, err := occupancy.sources.Flows.ReadFlow(flowID)
	if err != nil {
		return failedVerdict(err)
	}
	if record.FlowID != flowID {
		return failedVerdict(fmt.Errorf("%w: authoritative Flow identity %q does not match %q", ErrInvalidQuery, record.FlowID, flowID))
	}
	if untrackedOwnerActive(record.UntrackedOwner) {
		return Verdict{holder: HolderUntrackedOwner}
	}
	if query.Purpose.Stage == StageReserved &&
		(query.Purpose.Role == actions.RoleTrackedPhase || query.Purpose.Role == actions.RolePhaseResume) {
		return Free()
	}
	if query.Purpose.Stage == StageAutoAdvance {
		candidate := artifacts.NormalizePhaseID(query.PhaseID)
		for _, phase := range record.Phases {
			if phase.Status == flowstore.PhaseRunning && artifacts.NormalizePhaseID(phase.PhaseID) != candidate {
				return Verdict{holder: HolderRunningPhase, phaseID: phase.PhaseID}
			}
		}
	}
	if occupancy.sources.Sessions == nil {
		return failedVerdict(ErrMissingSessionStore)
	}
	records, err := occupancy.sources.Sessions.ListFlowSessions(flowID)
	if err != nil {
		return failedVerdict(err)
	}
	if query.Purpose.Role == actions.RoleRepair {
		for _, phase := range flowstore.OrderedPhases(record.Phases) {
			if phaseHasLiveSession(phase) || phaseHasLiveStoredSession(flowID, phase, records) {
				return Verdict{holder: HolderPhaseSession, phaseID: phase.PhaseID}
			}
		}
		return Free()
	}
	if query.Purpose.Role == actions.RoleAutofix {
		for _, phase := range record.Phases {
			if flowstore.PhaseStatusTerminal(string(phase.Status)) {
				continue
			}
			if phaseHasLiveSession(phase) || phaseHasLiveStoredSession(flowID, phase, records) {
				return Verdict{holder: HolderPhaseSession, phaseID: phase.PhaseID}
			}
		}
		return Free()
	}
	if query.Purpose.Role == actions.RoleWorktreeAgent {
		for _, stored := range records {
			if strings.TrimSpace(stored.FlowID) == flowID && sessions.IsActive(stored.Status, stored.EndedAt) {
				return Verdict{holder: HolderFlowSession}
			}
		}
		for _, phase := range record.Phases {
			if phaseHasLiveSession(phase) || phaseHasLiveStoredSession(flowID, phase, records) {
				return Verdict{holder: HolderPhaseSession, phaseID: phase.PhaseID}
			}
			if phase.Status == flowstore.PhaseRunning {
				return Verdict{holder: HolderRunningPhase, phaseID: phase.PhaseID}
			}
		}
		return Free()
	}
	if query.Purpose.Role == actions.RoleSavedSessionResume {
		for _, phase := range record.Phases {
			if phase.Status == flowstore.PhaseRunning {
				return Verdict{holder: HolderRunningPhase, phaseID: phase.PhaseID}
			}
			if phaseHasLiveSession(phase) || phaseHasLiveStoredSession(flowID, phase, records) {
				return Verdict{holder: HolderPhaseSession, phaseID: phase.PhaseID}
			}
		}
		for _, stored := range records {
			if strings.TrimSpace(stored.FlowID) == flowID && strings.TrimSpace(stored.SessionID) != "" && sessions.IsActive(stored.Status, stored.EndedAt) {
				return Verdict{holder: HolderFlowSession}
			}
		}
		return Free()
	}
	if query.Purpose.Role == actions.RolePhaseResume {
		for _, phase := range record.Phases {
			if phase.PhaseID != query.PhaseID {
				continue
			}
			if phaseHasLiveSessionExcept(phase, query.SkipSession) || phaseHasLiveStoredSessionExcept(flowID, phase, records, query.SkipSession) {
				return Verdict{holder: HolderPhaseSession, phaseID: phase.PhaseID}
			}
			break
		}
		return Free()
	}
	if query.Purpose.Role != actions.RoleTrackedPhase {
		return failedVerdict(errPendingSourceFamily)
	}
	phaseID := query.PhaseID
	for _, phase := range record.Phases {
		if phase.PhaseID != phaseID {
			continue
		}
		if phaseHasLiveSession(phase) || phaseHasLiveStoredSession(flowID, phase, records) {
			return Verdict{holder: HolderPhaseSession, phaseID: phase.PhaseID}
		}
		break
	}
	return Free()
}

func phaseHasLiveStoredSession(flowID string, phase flowstore.FlowPhase, records []sessions.SessionRecord) bool {
	return phaseHasLiveStoredSessionExcept(flowID, phase, records, SessionIdentity{})
}

func untrackedOwnerActive(owner *flowstore.UntrackedOwner) bool {
	return owner != nil && strings.TrimSpace(owner.LaunchID) != "" && owner.State != flowstore.UntrackedOwnerEnded
}

func phaseHasLiveStoredSessionExcept(flowID string, phase flowstore.FlowPhase, records []sessions.SessionRecord, skip SessionIdentity) bool {
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID = strings.TrimSpace(launchID); launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, record := range records {
		if record.FlowID != flowID || strings.TrimSpace(record.SessionID) == "" || skip.matches(string(record.Provider), record.SessionID) {
			continue
		}
		if _, ok := launches[strings.TrimSpace(record.LaunchID)]; !ok {
			continue
		}
		if sessions.IsActive(record.Status, record.EndedAt) {
			return true
		}
	}
	return false
}

func (identity SessionIdentity) matches(provider, sessionID string) bool {
	return strings.TrimSpace(identity.SessionID) != "" &&
		agent.Normalize(provider) == agent.Normalize(identity.Provider) &&
		sessionID == identity.SessionID
}

func queryLease(inspector LeaseInspector, flowID string) Verdict {
	if inspector == nil {
		return Verdict{holder: HolderLeaseUnreadable, err: ErrMissingLease}
	}
	occupied, err := inspector.FlowLeaseOccupied(flowID)
	if err != nil {
		return Verdict{holder: HolderLeaseUnreadable, err: err}
	}
	if occupied {
		return Verdict{holder: HolderPeerLease}
	}
	return Free()
}

// chooseVerdict owns purpose ordering independently of adapter call order.
// Tracked-phase footers include the transient headless write and rank it above
// the lease. Preview excludes that source through its runtime permission. In
// both cases a lease outranks attempts and terminal slots.
func chooseVerdict(purpose Purpose, lease, runtime Verdict) Verdict {
	if purpose.Role == actions.RoleTrackedPhase && purpose.Stage == StageFooter &&
		runtime.Holder() == HolderHeadlessWrite {
		return runtime
	}
	if lease.Occupied() {
		return lease
	}
	return runtime
}

func queryRuntime(permission runtimePermission, runtime Runtime, flowID string) Verdict {
	if permission&readAttempt != 0 {
		if role, occupied := runtime.AttemptHolder(flowID); occupied {
			return Verdict{holder: attemptHolder(role)}
		}
	}
	if permission&readFlowTerminalWithoutRepair != 0 &&
		runtime.HasFlowTerminal(flowID) && !runtime.HasRepairTerminal(flowID) {
		return Verdict{holder: HolderFlowTerminal}
	}
	// A repair slot and a live Flow terminal deliberately overlap. Prefer the
	// more specific repair holder so the result stays deterministic.
	if permission&readRepairTerminal != 0 && runtime.HasRepairTerminal(flowID) {
		return Verdict{holder: HolderRepairTerminal}
	}
	if permission&readFlowTerminal != 0 && runtime.HasFlowTerminal(flowID) {
		return Verdict{holder: HolderFlowTerminal}
	}
	if permission&readRepairDrain != 0 && runtime.RepairDrainPending(flowID) {
		return Verdict{holder: HolderRepairDrain}
	}
	if permission&readHeadlessWrite != 0 && runtime.HeadlessWritePending(flowID) {
		return Verdict{holder: HolderHeadlessWrite}
	}
	return Free()
}

// queryAutofixRuntime preserves autofix's deliberate ladder: repair and resume
// attempts, terminal slots, other attempts, then the transient headless write.
func queryAutofixRuntime(permission runtimePermission, runtime Runtime, flowID string) Verdict {
	role, occupied := runtime.AttemptHolder(flowID)
	if occupied {
		holder := attemptHolder(role)
		if holder == HolderRepairAttempt || holder == HolderPhaseResumeAttempt {
			return Verdict{holder: holder}
		}
	}
	if permission&readRepairTerminal != 0 && runtime.HasRepairTerminal(flowID) {
		return Verdict{holder: HolderRepairTerminal}
	}
	if permission&readFlowTerminal != 0 && runtime.HasFlowTerminal(flowID) {
		return Verdict{holder: HolderFlowTerminal}
	}
	if occupied {
		return Verdict{holder: attemptHolder(role)}
	}
	if permission&readHeadlessWrite != 0 && runtime.HeadlessWritePending(flowID) {
		return Verdict{holder: HolderHeadlessWrite}
	}
	return Free()
}

func failedVerdict(err error) Verdict {
	return Verdict{err: err}
}

func resolveFreshness(stage Stage, freshness Freshness) (Freshness, bool) {
	switch freshness {
	case FreshnessCached, FreshnessAuthoritative:
		return freshness, true
	case FreshnessDefault:
		switch stage {
		case StagePreview, StageFooter, StageAutoAdvance, StageDrain, StageDrainControl:
			return FreshnessCached, true
		case StageAdmission, StageAuthoritative, StageLeasePreflight, StageReserved, StageInstall, StageSessionRelease:
			return FreshnessAuthoritative, true
		default:
			return FreshnessDefault, false
		}
	default:
		return FreshnessDefault, false
	}
}

func (permission freshnessPermission) allows(freshness Freshness) bool {
	switch freshness {
	case FreshnessCached:
		return permission&allowCached != 0
	case FreshnessAuthoritative:
		return permission&allowAuthoritative != 0
	default:
		return false
	}
}

func attemptHolder(role actions.FlowLaunchRole) Holder {
	switch role {
	case actions.RoleRepair:
		return HolderRepairAttempt
	case actions.RolePhaseResume:
		return HolderPhaseResumeAttempt
	case actions.RoleTrackedPhase, actions.RoleCreatePhase:
		return HolderPhaseAttempt
	default:
		return HolderOtherAttempt
	}
}

// Free is the verdict for an unoccupied Flow. It exists so callers and tests
// can express "nothing holds it" without constructing a zero Verdict literal
// and depending on the zero value staying free.
func Free() Verdict { return Verdict{} }

// Failed is the verdict for an authoritative source failure.
func Failed(err error) Verdict { return failedVerdict(err) }
