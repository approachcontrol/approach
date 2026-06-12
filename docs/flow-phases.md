# Flow Phase Transition Semantics

This document is the canonical reference for Flow phase statuses, the
transition table, derived readiness, and the on-disk compatibility story.
The code-level source of truth is the `phaseTransitions` table in
`flowstore/transitions.go`, exported through
`flowstore.AllowedNextPhaseStatuses` and
`flowstore.AgentSettablePhaseStatuses`.

## Design decision

Flow phases keep the persisted seven-status model rather than collapsing to a
smaller `ready`/`running`/`done`/`blocked` set:

- `pending` and `ready` are derived bookkeeping owned entirely by wtui. They
  are what let the TUI identify whether a selected phase row is launchable
  without agents or the UI re-deriving gate rules.
- `needs_attention` is distinct from `blocked` on purpose: it marks
  non-blocking concerns a human should review, while `blocked` stops the
  pipeline.
- Agents never see or reason about the derived statuses; they set exactly five
  statuses and wtui does the rest.

The simplification happened in *ownership*, not in the enum: wtui owns
readiness, agents own honest reporting of their own phase.

## Statuses and who sets them

| Status | Set by | Meaning |
| --- | --- | --- |
| `pending` | wtui (derived) | Predecessor gates are not yet satisfied. |
| `ready` | wtui (derived) | All predecessor gates satisfied; launchable. |
| `running` | agent / TUI launch | Work on the phase has started. |
| `needs_attention` | agent | Non-blocking concern for a human to review. |
| `completed` | agent | Phase work finished. |
| `blocked` | agent | Phase cannot proceed; requires intervention. |
| `skipped` | agent | Phase intentionally bypassed (requires notes). |

Agents may set only `running`, `needs_attention`, `completed`, `blocked`, and
`skipped` through `wtui flow phase set`. The high-level wrappers
`wtui flow phase complete`, `wtui flow phase block`, and
`wtui flow phase needs-attention` use the same validation and persistence path
for the common `completed`, `blocked`, and `needs_attention` outcomes. The
`wtui flow phase restart` wrapper records `running` with a rerun note. These
wrappers print JSON with the updated phase and next actionable phase state.
They do not add separate notes requirements; store validation remains the
source of truth. Setting `ready` is rejected with "readiness is derived"; the
CLI rejects unknown statuses with the valid list, and the store rejects them as
`invalid phase status`.

## Canonical transition table

| From \ To | running | needs_attention | completed | blocked | skipped |
| --- | --- | --- | --- | --- | --- |
| `pending` | – | – | – | – | yes |
| `ready` | yes | yes | yes | yes | yes |
| `running` | – | yes | yes | yes | yes |
| `needs_attention` | yes (notes) | – | – | – | yes |
| `blocked` | yes (notes) | – | – | – | yes |
| `completed` | yes | – | – | – | – |
| `skipped` | yes | – | – | – | – |

Additional rules:

- Same-status updates are idempotent no-ops (allowed, used to refresh
  outcome/summary/notes on the current status).
- `skipped` always requires `--notes`, from any state.
- Restarting a `needs_attention` or `blocked` phase as `running` requires
  `--notes`; completing one directly is invalid — restart first. The
  high-level `wtui flow phase restart` wrapper supplies a standard note when
  `--notes` is omitted.
- Invalid transitions fail with the allowed next statuses, e.g.
  `invalid phase transition pending -> completed; allowed from pending: skipped`.
- TUI launches mark the phase `running`, with one exception: resuming an
  attached CLI provider session of a `completed` or `skipped` phase records the
  launch ID (so the resumed session can re-link) without reopening the phase,
  and a failed resume launch never regresses such a phase to `needs_attention`.
  `codex-app` resume deep links are untracked app navigation because they cannot
  carry wtui launch metadata. Reopening a finished phase deliberately remains
  `wtui flow phase restart`.
- The TUI can also recover a selected `await-session` phase after confirmation
  by removing the newest orphan launch attempt and re-deriving readiness. This
  is a UI-owned recovery mutation, not an agent-settable transition, and it is
  unavailable while a running or starting embedded Flow terminal is attached to
  the same Flow phase.

## Derived readiness

The phase-affecting mutations (`SetPhase`, `AddChildPhase`, `SetPR`,
`AddPhaseLaunchID`, and `ResetAwaitingSessionPhase`) re-derive readiness with
`refreshPhaseReadiness`, regardless of graph shape. Loads and the remaining
mutations normalize only records containing a `plan-review` phase — the
standard graph; hand-authored records without one keep their stored statuses
until a phase-affecting mutation touches them. Agents never need to know which
phase becomes ready next; they only report their own phase.

Flow auto mode is layered on top of this derived readiness. It is a persisted
per-Flow TUI setting, not a status and not a transition-table rule. When enabled,
the TUI watches accepted Flow refreshes for a phase that newly became
`completed`, then launches the first currently `ready` non-Merge phase in that
same Flow through the normal launch path. It does not launch on
`skipped`, `blocked`, `needs_attention`, failed launches, or missing PR metadata,
and it deliberately stops before Merge even when Autoreview completion makes
Merge `ready`.

Walking phases in order, a `pending` phase becomes `ready` once every
predecessor satisfies its downstream gate:

- **Default gate**: the phase is `completed`, or `skipped` with notes.
- **Plan Review**: `completed` with outcome `approved` or
  `approved_with_concerns`, or `skipped` with notes. Any other outcome keeps
  Implementation `pending`. The high-level Plan Review wrappers fill the
  unambiguous outcomes when omitted: `complete` uses `approved`,
  `needs-attention` uses `changes_requested`, and `block` uses `blocked`.
- **Autoreview**: the high-level wrappers fill the unambiguous outcomes when
  omitted: `complete` uses `passed`, `needs-attention` uses
  `needs_attention`, and `block` uses `blocked`.
- **PR Creation**: `completed` *and* structured PR metadata recorded via
  `wtui flow pr set` (provider, positive number, valid URL, head/base
  branches). Completion alone does not unlock Autoreview; a skipped PR
  Creation never unlocks it.
- **Implementation children**: every child phase under Implementation must be
  `completed` or `skipped` with notes before phases after Implementation can
  become ready.

When a gate stops holding (for example Plan Review is reopened), downstream
phases that had advanced are reset to `pending` and their outcomes cleared, so
stale readiness never survives a regression upstream. Downstream `blocked`
phases are an exception: they are reset only when Plan Review's gate is
unsatisfied — whether Plan Review itself regressed or an earlier gate broke —
and keep their blocked state under any other gate regression.

## Derived Flow status

The Flow-level `status` field is always derived, in priority order: abandoned
record → `abandoned`; merge recorded merged → `merged`; merge blocked or any
phase blocked → `blocked`; any phase needs_attention → `needs_attention`; all
phases completed/skipped → `completed`; any phase started → `in_progress`;
otherwise `pending`.

## Linked plan sync

When a Flow has a linked saved plan, transitioning a Flow phase to `completed`
also marks a saved-plan phase with the same normalized phase ID as `completed`.
Missing saved-plan phases are ignored, and already-completed saved-plan phases
are left unchanged. If the linked plan cannot be read or updated during that
transition, wtui marks the Flow phase `needs_attention` with a sync-failure note
and returns the persistence error so the agent can report it. Repeating
`completed` for an already-completed Flow phase preserves that completed state
even if the linked-plan sync later fails.

## Compatibility and migration

- The persisted schema is unchanged: `schema_version` stays `1` and no status
  strings were added, removed, or renamed. Existing Flow JSON needs no
  migration.
- `auto_mode` is additive and omitted when false. Existing Flow JSON without the
  field reads as auto mode off.
- Derived state is self-healing: phase-affecting mutations (`SetPhase`,
  `AddChildPhase`, `SetPR`, `AddPhaseLaunchID`,
  `ResetAwaitingSessionPhase`) re-derive readiness for any graph, and records
  containing a `plan-review` phase (the standard graph) are additionally
  normalized on load, so records written before a gate rule existed converge to
  correct `pending`/`ready` values. Records without a `plan-review` phase keep
  their stored statuses until a phase-affecting mutation touches them.
- Completed plan-review phases persisted before outcomes existed are
  normalized to `approved` on read.

## TUI rendering

The flows pane renders the persisted status, or the phase outcome when one is
recorded (for example `plan-review:approved`). Recovery labels for partial
states (`recover-worktree`, `await-session`, `session-mismatch`,
`missing-session-id`, `missing-pr`) are layered on top, rendered prefixed
with the phase ID like any phase state (for example `autoreview:missing-pr`),
and are display-only; they never change persisted phase status. See
`docs/config.md` for the pane behavior. Flows with auto mode enabled receive a
subtle non-selected row highlight, while selected-row styling remains dominant.

When `await-session` is caused by an orphaned latest launch and predecessor
gates still hold, the selected phase row can be reset with `x` after
confirmation. The reset removes that orphan launch, persists the phase as
`pending`, then lets derived readiness promote it to `ready`; if readiness
cannot be derived, the mutation is rejected and the record is left unchanged.
