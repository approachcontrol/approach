# ADR 0001: Beads epic auto-progression boundaries

Status: accepted (2026-08-13)

## Context

Epic auto-progression needs durable state, a Beads claim operation, and
clear restart semantics. Those decisions must not widen the existing
read-only `beadsquery` boundary or turn Flow creation into an implicit agent
launch.

## Decision

- `beadsquery` remains strictly read-only. The separate `beadsmutate` package
  is the sole sanctioned Beads write boundary; its only operation is
  `bd update --claim -- <id>`, with its own runner. The positional separator keeps
  flag-shaped IDs from being interpreted as options. The runner isolates repository and
  database selectors like the query runner while deliberately retaining
  `BEADS_ACTOR`, which controls assignment and same-actor retry idempotency.
  Claim failure halts the attempt before Flow preparation and surfaces its
  cause.
- Progression is a separate `flowstore` entity keyed by canonical repository
  path and epic Bead ID. It persists enabled state, an authoritative `done`
  completion bit, an optional complete halt tuple, and timestamps in the shared
  SQLite store. Disabled state never implies completion.
- Enabled, done, and halted state survive restart. The four valid states are
  active `(enabled, !done, no halt)`, normal off `(!enabled, !done, no halt)`,
  successfully exhausted `(!enabled, done, no halt)`, and halted `(!enabled,
  !done, halt)`. Enabled+done, done+halt, and enabled+halt are forbidden.
  Advancement is edge-triggered only
  from success-terminal transitions observed while the TUI is running. Startup
  performs no catch-up for completions that occurred while Approach was down.
- A progression-created child Flow persists its receipt-less exact-link identity
  through `flowCreator.Create` before synchronously claiming the freshly
  revalidated direct-and-Ready child; claim admission runs before worktree
  creation and retains the marked identity on a claim error. After a successful
  claim, the identity remains discoverable across later preparation or
  enablement failure. Retry prioritizes an open marked receipt-less or
  prepared-pending exact-link Flow over a later Ready sibling and repeats the
  same-actor idempotent claim before adoption. There is no cross-system
  transaction and no automatic unclaim. Prepared exact-link adoption and manual
  Ready Flow creation remain claim-free. This slice does not start a phase or
  launch an agent. Child launching is deferred to `approach-y7g.9`; selecting
  and advancing subsequent children is deferred to `approach-y7g.5`.

## Concurrency boundary

The TUI admits one Ready-Bead creation or epic progression toggle at a time and
revalidates a prepared Flow while holding its launch/close reservation. The
progression write and final Flow check share one SQLite writer transaction.
This is a single-Model guarantee. Read-then-create uniqueness across separate
Approach processes is not provided.

Establishing `done` is an atomic active→done transition. A concurrent manual
disable or halt therefore wins by making a queued completion fail rather than
letting it overwrite authoritative state. Repeating done is a timestamp-
preserving no-op. Explicit manual off clears prior completion while retaining a
sticky halt; ordinary and prepared enable clear both done and halt.
