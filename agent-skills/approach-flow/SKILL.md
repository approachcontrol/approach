---
name: approach-flow
description: Participate in an active Approach Flow phase, create a persisted Approach Flow from an ad hoc task or plan, and persist or update an Approach plan. Use when Approach launch variables identify a Flow or plan, when the user asks to create/add/make an Approach Flow, or whenever an agent plan is created, revised, approved, started, completed, blocked, or superseded.
---

# Approach Flow

Persist workflow state through the Approach CLI. Treat successful readback, not
the attempted command, as proof that persistence succeeded.

## Select the workflow

- For an active Approach Flow phase, read
  [references/active-phase.md](references/active-phase.md) completely before
  doing phase work.
- To create a Flow from an ad hoc task or an existing plan, read
  [references/create-flow.md](references/create-flow.md) completely.
- To create or update a standalone saved plan, read
  [references/plans.md](references/plans.md) completely.
- Before any workflow that writes Approach state, read
  [references/persistence.md](references/persistence.md) completely.

Read every reference selected above. An active plan phase needs
`active-phase.md`, `plans.md`, and `persistence.md`; creating a Flow from an
existing plan needs `create-flow.md`, `plans.md`, and `persistence.md`.

## Core rules

1. Prefer launch environment defaults. Active commands normally omit
   `--flow-id`, `--phase-id`, `--plan-id`, and `--state-root`.
2. Pass explicit IDs and `--state-root` for ad hoc work outside a launch.
3. Keep plan persistence and Flow phase progression separate. Saving a plan
   does not complete a Flow phase; record completion explicitly only after the
   phase goal is satisfied.
4. Report every persistence failure. Never claim that a plan, link, phase,
   issue, PR, or merge update succeeded when its command or readback failed.
5. Do not invent session attachment. The CLI does not attach the current ad hoc
   provider session to a newly created Flow.
