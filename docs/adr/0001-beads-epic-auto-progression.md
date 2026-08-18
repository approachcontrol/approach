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
  from success-terminal transitions observed while the TUI is running, and the
  same poll halts progression on the failure-terminal edge (`blocked`,
  `needs_attention`, `closed`, `abandoned`) of the tracked child. Startup
  performs no catch-up for either edge while Approach was down.
- A progression-created child Flow persists its receipt-less exact-link identity
  through `flowCreator.Create` before synchronously claiming the freshly
  revalidated direct-and-Ready child; claim admission runs before worktree
  creation and retains the marked identity on a claim error. After a successful
  claim, the identity remains discoverable across later preparation or
  enablement failure. Retry prioritizes an open marked receipt-less or
  prepared-pending exact-link Flow over a later Ready sibling and repeats the
  same-actor idempotent claim before adoption. There is no cross-system
  transaction and no automatic unclaim. Prepared exact-link adoption and manual
  Ready Flow creation remain claim-free.
- The advance edge selects the next child and submits it to the create-phase
  launch lifecycle as one create-then-launch intent. That single admitted
  attempt creates the child, prepares it, reconciles it as the epic's successor
  under the launch/close reservation it already holds, and launches its first
  phase agent, so a chain runs unattended after the first child. Children are
  written headless and keep the store's default AutoMode, which is what drains
  their later phases. Any terminal failure of that attempt halts the epic with a
  `blocked` tuple naming the child and the cause, so a chain that cannot start
  its next child stops with a name instead of stalling silently. The enable edge
  still only prepares its first child: that one is started by hand.

## Concurrency boundary

The TUI admits one Ready-Bead creation or epic progression toggle at a time and
revalidates a prepared Flow while holding its launch/close reservation. The
progression write and final Flow check share one SQLite writer transaction.
This is a single-Model guarantee. Read-then-create uniqueness across separate
Approach processes is not provided.

Establishing `done` and establishing a halt are both atomic active→terminal
transitions. A concurrent manual
disable or halt therefore wins by making a queued completion fail rather than
letting it overwrite authoritative state. Repeating done is a timestamp-
preserving no-op. Explicit manual off clears prior completion while retaining a
sticky halt; ordinary and prepared enable clear both done and halt. Repeating a
halt is likewise a timestamp-preserving no-op that retains the first cause, so
the halt a user sees is the one that stopped progression.
