// Package launchcontrol makes Flow phase results controller-owned and
// crash-recoverable.
//
// A launched agent reports its phase result through the `approach flow` CLI.
// Before this package the CLI opened the Flow database directly, which meant a
// result was only as durable as the agent's own store open, and a phase whose
// agent exited without a result stayed `running` forever. Now every launch has
// a launch directory under <root>/launches/<launch-id>/, the TUI hosts one Unix
// socket per state root, and:
//
//   - proxied verbs travel over the socket to the controller, which appends the
//     request to the launch's durable log BEFORE acknowledging it, applies it
//     through the process's one Flow store, and records an applied marker;
//   - when the socket is unreachable the CLI falls back to a direct store open
//     under the same log discipline, or, when this build cannot open the
//     database at all, spools the request for the next controller start;
//   - replay applies spooled or unmarked requests exactly once, under the
//     latest-launch gate, and never demotes a phase another launch now owns;
//   - reconciliation demotes a `running` phase to needs_attention (blocked on a
//     plan-review kind) only on positive exit evidence, and never while the
//     Flow lease is held.
//
// Import rules, asserted by doc_test.go: this package imports flowstore,
// internal/artifacts, internal/flowlease, and the standard library. It never
// imports sessions, model, actions, or internal/controlplane. Liveness enters
// as an injected probe, exit evidence as a value, and the applied notifier as
// a callback, so the packages that own those facts can depend on this one
// without a cycle.
package launchcontrol
