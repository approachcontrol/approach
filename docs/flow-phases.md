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
`wtui flow phase restart` wrapper records `running` with a rerun note.
`wtui flow phase reset` is a wtui-owned recovery operation for stale running
phases, not an agent-facing transition. These wrappers print JSON with the
updated phase and next actionable phase state.
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
- The TUI and `wtui flow phase reset` can recover selected stale `running`
  phases after confirmation or command invocation. `await-session` means the
  latest launch has no attached session record. `ended-session` means every
  session attached to the latest launch has `status: ended` or a non-zero
  `ended_at`. The reset removes the newest stale launch attempt, removes
  sessions tied to that launch, persists the phase as `pending`, and re-derives
  readiness. Any non-ended attached session on the merged logical phase,
  session launch mismatches, and unsatisfied predecessor gates are rejected.
  The TUI reset is unavailable while a running or starting embedded Flow
  terminal is attached to the same Flow phase.

## Derived readiness

The phase-affecting mutations (`SetPhase`, `AddChildPhase`, `SetPR`,
`AddPhaseLaunchID`, and `ResetRecoverableRunningPhase`) re-derive readiness with
`refreshPhaseReadiness`, regardless of graph shape. Loads and the remaining
mutations normalize only records containing a `plan-review` phase — the
standard graph; hand-authored records without one keep their stored statuses
until a phase-affecting mutation touches them. Agents never need to know which
phase becomes ready next; they only report their own phase.

Newly written Flow records persist explicit top-level dependency edges in each
phase's `depends_on` field. Legacy standard records that do not have the key are
backfilled as the same linear graph on read; explicit empty `depends_on` means a
root phase. Implementation child phases remain structurally ordered under their
parent rather than declaring their own dependencies.

Walking phases in topological dependency order, a `pending` phase becomes
`ready` once every prerequisite satisfies its downstream gate:

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
- **Plan**: may record optional GitHub issue metadata with
  `wtui flow issue set` when the task references an issue. Issue metadata is
  informational and does not gate downstream phases.
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

The TUI can also record a manual GitHub merge for a Flow whose PR metadata is
present and whose Merge phase is eligible. That operation verifies the PR is
already merged in GitHub, completes the Merge phase, records structured merge
metadata, and lets the derived Flow status become `merged` without launching a
Merge phase agent.

## Linked plan sync

When a Flow has a linked saved plan, transitioning a Flow phase to `completed`
also marks a saved-plan phase with the same normalized phase ID as `completed`.
Missing saved-plan phases are ignored, and already-completed saved-plan phases
are left unchanged. If the linked plan cannot be read or updated during that
transition, wtui marks the Flow phase `needs_attention` with a sync-failure note
and returns the persistence error so the agent can report it. Repeating
`completed` for an already-completed Flow phase preserves that completed state
even if the linked-plan sync later fails.
Manual GitHub merge recording is stricter: if syncing the linked plan's Merge
phase fails while terminal merge metadata is being recorded, wtui restores the
previous PR status, clears that terminal metadata, marks the Merge phase
`needs_attention`, and keeps the Flow recoverable instead of deriving it as
`merged`.

## Compatibility and migration

- The persisted schema is unchanged: `schema_version` stays `1` and no status
  strings were added, removed, or renamed. Existing Flow JSON needs no
  migration.
- Derived state is self-healing: phase-affecting mutations (`SetPhase`,
  `AddChildPhase`, `SetPR`, `AddPhaseLaunchID`,
  `ResetRecoverableRunningPhase`) re-derive readiness for any graph, and records
  containing a `plan-review` phase (the standard graph) are additionally
  normalized on load, so records written before a gate rule existed converge to
  correct `pending`/`ready` values. Records without a `plan-review` phase keep
  their stored statuses until a phase-affecting mutation touches them.
- Completed plan-review phases persisted before outcomes existed are
  normalized to `approved` on read.

## TUI rendering

The flows pane renders the persisted status, or the phase outcome when one is
recorded (for example `plan-review:approved`). Recovery labels for partial
states (`recover-worktree`, `await-session`, `ended-session`,
`session-mismatch`, `missing-session-id`, `missing-pr`) are layered on top, rendered prefixed
with the phase ID like any phase state (for example `autoreview:missing-pr`),
and are display-only; they never change persisted phase status. See
`docs/config.md` for the pane behavior.

When `await-session` or `ended-session` is recoverable and predecessor gates
still hold, the selected phase row can be reset with `x` after confirmation or
with `wtui flow phase reset`. The reset removes the stale latest launch and
matching latest-launch sessions, persists the phase as `pending`, then lets
derived readiness promote it to `ready`; if readiness cannot be derived, the
mutation is rejected and the record is left unchanged. Preserved attached
session history on the merged logical phase must already be ended; any live or
unknown-status session must finish before reset is allowed. Session mismatches
are higher priority than `ended-session` and must be resolved instead of reset.
