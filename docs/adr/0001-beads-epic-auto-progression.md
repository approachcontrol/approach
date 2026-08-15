# ADR 0001: Beads epic auto-progression boundaries

Status: accepted (2026-08-13)

## Context

Epic auto-progression needs durable state, a future Beads claim operation, and
clear restart semantics. Those decisions must not widen the existing
read-only `beadsquery` boundary or turn Flow creation into an implicit agent
launch.

## Decision

- `beadsquery` remains strictly read-only. The later claim slice will add a
  separate `beadsmutate` package whose only operation is `bd update --claim`,
  with its own runner. Claim failure will halt progression and create no Flow.
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
- This first toggle slice prepares a child through `FlowStarter.PrepareFlow`
  only. It does not claim the Bead, start a phase, or launch an agent. Child
  launching is deferred to `approach-y7g.9`; selecting and advancing subsequent
  children is deferred to `approach-y7g.5`.

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
