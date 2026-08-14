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
  `bd update <id> --claim`, with its own runner. The runner isolates repository and
  database selectors like the query runner while deliberately retaining
  `BEADS_ACTOR`, which controls assignment and same-actor retry idempotency.
  Claim failure halts the attempt before Flow preparation and surfaces its
  cause.
- Progression is a separate `flowstore` entity keyed by canonical repository
  path and epic Bead ID. It persists enabled state, an optional complete halt
  tuple, and timestamps in the shared SQLite store.
- Enabled and halted state survive restart. Advancement is edge-triggered only
  from success-terminal transitions observed while the TUI is running. Startup
  performs no catch-up for completions that occurred while Approach was down.
- A progression-created child Flow is claimed synchronously immediately before
  `FlowStarter.PrepareFlow`. There is no cross-system transaction: an uncertain
  claim error or a later Approach failure is never compensated with an
  automatic unclaim. Already-prepared exact-link adoption and manual Ready Flow
  creation remain claim-free. This slice does not start a phase or launch an
  agent. Child launching is deferred to `approach-y7g.9`; selecting and
  advancing subsequent children is deferred to `approach-y7g.5`.

## Concurrency boundary

The TUI admits one Ready-Bead creation or epic progression toggle at a time and
revalidates a prepared Flow while holding its launch/close reservation. The
progression write and final Flow check share one SQLite writer transaction.
This is a single-Model guarantee. Read-then-create uniqueness across separate
Approach processes is not provided.
