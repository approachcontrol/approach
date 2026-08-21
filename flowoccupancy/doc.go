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
// gets back a verdict that names the holder and, when the caller renders
// refusals, the exact text to show. Callers never see the underlying
// representations — not the lease, not the attempt map, not the terminal slots,
// not the session mirror. Adding a new source is a change inside this package;
// today it is a change at every call site that could have read it.
//
// Purpose is a (Role, Stage) pair. Role is actions.FlowLaunchRole, reused from
// ADR 0002, and selects the vocabulary and the refusal order. Stage names the
// consumer class and selects the source set:
//
//	StagePreview         renders per frame; cached sources only
//	StageAdmission       runs on a keypress; in-process sources plus the lease
//	StageAutoAdvance     the AutoMode advance poll's admission and read
//	StageAuthoritative   runs in a command; full store access
//	StageReserved        runs under the cross-process reservation
//	StageInstall         the last check before a terminal slot is allocated
//	StageDrain           runs at 1 Hz; cached only, and never forks a subprocess
//	StageSessionRelease  the non-launch release gesture
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
// therefore consulted only at StageAdmission and StageAuthoritative — never at
// any other stage, and in particular never from the AutoMode poll path
// (StageAutoAdvance, StageDrain).
//
// # What this package must never do
//
// It must never import model. The dependency edge is
// model -> flowoccupancy -> {actions, flowstore, sessions}, and a need to
// reverse it means the boundary was drawn wrong.
//
// # Status
//
// This is the interface skeleton landed by approach-x0r.1. Every method body
// is a stub; the implementation is approach-x0r.3, behind the characterization
// tests approach-x0r.2 writes first. Query fails closed until then: it reports
// occupancy with ErrUnimplemented, so a caller migrated early breaks loudly
// rather than being told the Flow is free. No caller in model/ has been
// migrated, no existing predicate has been deleted, and no behavior has
// changed.
package flowoccupancy
