// Package flowoccupancy answers one question: is something already working in
// this Flow, and if so, what?
//
// It exists because that question is currently answered sixteen ways in
// model/, by thirteen representations and two composites over them, read at
// roughly forty call sites that deliberately disagree with each other. The
// evidence is docs/flow-occupancy-matrix.md; the design and its open questions
// are docs/adr/0003-flow-occupancy-deep-module.md.
//
// # The contract
//
// A caller states its Flow, its purpose, and how fresh an answer it needs, and
// gets back a verdict that names the holder, the occupied phase when relevant,
// and any source error. Model owns user-visible refusal text so each migrated
// caller can preserve its existing wording. Callers never see the underlying
// representations such as the lease, attempt map, terminal slots, or session
// mirror. Adding a new source is a change inside this package instead of every
// call site that could have read it.
//
// Purpose is a (Role, Stage) pair. Role is actions.FlowLaunchRole, reused from
// ADR 0002, and selects the vocabulary and the refusal order. Stage names the
// consumer class and selects the source set:
//
//	StagePreview         answers a launch preview from cached sources
//	StageFooter          renders an affordance with footer-only occupancy
//	StageAdmission       runs on a keypress; in-process sources plus the lease
//	StageAutoAdvance     the AutoMode advance poll's admission and read
//	StageAuthoritative   runs in a command; full store access
//	StageReserved        runs under the cross-process reservation
//	StageInstall         the last check before a terminal slot is allocated
//	StageDrain           gates an AutoMode launch at 1 Hz
//	StageDrainControl    applies repair state to drain arm and disarm
//	StageSessionRelease  runs the authoritative non-launch release gesture
//
// # The freshness rule
//
// Cached mirrors answer questions that only render. Authoritative sources
// answer questions that mutate or reserve.
//
// In-process runtime sources — the attempt map, the terminal slots, the pending
// headless write, the repair drain marker — are always authoritative, because
// they are the state rather than a mirror of it, and are read at every
// freshness. A lease that cannot be read is occupancy under every purpose, in
// both directions, fail-closed. The tmux window probe forks a subprocess and is
// therefore consulted only at StageAdmission and StageAuthoritative, never from
// the cached rendering or AutoMode poll stages.
//
// # What this package must never do
//
// It must never import model. The dependency edge is
// model -> flowoccupancy -> {actions, flowstore, sessions}, and a need to
// reverse it means the boundary was drawn wrong.
//
// # Status
//
// The in-process runtime source family is implemented. It classifies launch
// attempts, Flow terminals, repair slots, pending headless writes, and repair
// drain markers according to the purpose registry. Creation-time Plan Now is
// the first migrated caller and asks the runtime-only create-admission purpose.
// Purposes that require a later source family still fail closed with an error;
// the lease, store, cache, session, and tmux-probe slices remain pending.
package flowoccupancy
