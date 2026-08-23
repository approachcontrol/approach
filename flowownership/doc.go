// Package flowownership owns in-process Flow reservations, retained-slot
// policy, and occupancy queries.
//
// # The contract
//
// Lifecycle callers reserve an exact Flow ID with a launch token. The same
// token fences transitions, saved-session transfer, and release. Query callers
// state a Flow, purpose, and freshness requirement, then receive a verdict that
// names the winning holder and any source error. Model owns user-visible text
// and runtime transport objects.
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
// Cached mirrors answer rendering questions. Authoritative sources answer
// questions that mutate or reserve. In-process ownership and retained slots
// are authoritative at every freshness. Lease read failures fail closed.
//
// # What this package must never do
//
// It must never import model. The dependency edge is
// model -> flowownership -> {actions, flowstore, sessions}, and a need to
// reverse it means the boundary was drawn wrong.
//
// # Status
//
// The package owns token-fenced lifecycle state, the saved-session owner index,
// retained-slot and detach policy, and the occupancy query policy tables. It
// never persists terminals or process state.
package flowownership
