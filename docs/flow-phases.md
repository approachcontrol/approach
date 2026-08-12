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

- `pending` and `ready` are derived bookkeeping owned entirely by Approach. They
  are what let the TUI identify whether a selected phase row is launchable
  without agents or the UI re-deriving gate rules.
- `needs_attention` is distinct from `blocked` on purpose: it marks
  non-blocking concerns a human should review, while `blocked` stops the
  pipeline.
- Agents never see or reason about the derived statuses; they set exactly five
  statuses and Approach does the rest.

The simplification happened in *ownership*, not in the enum: Approach owns
readiness, agents own honest reporting of their own phase.

## Statuses and who sets them

| Status | Set by | Meaning |
| --- | --- | --- |
| `pending` | Approach (derived) | Predecessor gates are not yet satisfied. |
| `ready` | Approach (derived) | All predecessor gates satisfied; launchable. |
| `running` | agent / TUI launch | Work on the phase has started. |
| `needs_attention` | agent | Non-blocking concern for a human to review. |
| `completed` | agent | Phase work finished. |
| `blocked` | agent | Phase cannot proceed; requires intervention. |
| `skipped` | agent | Phase intentionally bypassed (requires notes). |

Agents may set only `running`, `needs_attention`, `completed`, `blocked`, and
`skipped` through `approach flow phase set`. The high-level wrappers
`approach flow phase complete`, `approach flow phase block`, and
`approach flow phase needs-attention` use the same validation and persistence path
for the common `completed`, `blocked`, and `needs_attention` outcomes. The
`approach flow phase restart` wrapper records `running` with a rerun note.
`approach flow phase reset` is an Approach-owned recovery operation for stale running
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
  high-level `approach flow phase restart` wrapper supplies a standard note when
  `--notes` is omitted.
- Invalid transitions fail with the allowed next statuses, e.g.
  `invalid phase transition pending -> completed; allowed from pending: skipped`.
- TUI launches mark the phase `running`, with one exception: resuming an
  attached CLI provider session of a `completed` or `skipped` phase records the
  launch ID (so the resumed session can re-link) without reopening the phase,
  and a failed resume launch never regresses such a phase to `needs_attention`.
  `codex-app` resume deep links are untracked app navigation because they cannot
  carry approach launch metadata. Reopening a finished phase deliberately remains
  `approach flow phase restart`.
- The TUI and `approach flow phase reset` can recover selected stale `running`
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
`refreshPhaseReadiness`, regardless of graph shape. Agents never need to know
which phase becomes ready next; they only report their own phase.

Newly written Flow records persist explicit top-level dependency edges in each
phase's `depends_on` field. Legacy standard records that do not have the key are
backfilled as the same linear graph on read; explicit empty `depends_on` means a
root phase. Implementation child phases remain structurally ordered under their
parent rather than declaring their own dependencies.

Walking phases in topological dependency order, a `pending` phase becomes
`ready` once every prerequisite satisfies its downstream gate. Gates are keyed by
semantic phase `kind`, not by literal phase ID:

- **Default gate**: the phase is `completed`, or `skipped` with notes.
- **Plan Review (`plan_review`)**: `completed` with outcome `approved` or
  `approved_with_concerns`, or `skipped` with notes. Any other outcome keeps
  Implementation `pending`. The high-level Plan Review wrappers fill the
  unambiguous outcomes when omitted: `complete` uses `approved`,
  `needs-attention` uses `changes_requested`, and `block` uses `blocked`.
- **Autoreview (`autoreview`)**: the high-level wrappers fill the unambiguous outcomes when
  omitted: `complete` uses `passed`, `needs-attention` uses
  `needs_attention`, and `block` uses `blocked`.
- **PR Creation (`pr_creation`)**: `completed` *and* structured PR metadata recorded via
  `approach flow pr set` (provider, positive number, valid URL, head/base
  branches). Completion alone does not unlock Autoreview; a skipped PR
  Creation never unlocks it.
- **Plan (`plan`)**: may record optional GitHub issue metadata with
  `approach flow issue set` when the task references an issue. Issue metadata is
  informational and does not gate downstream phases.
- **Implementation children**: every child phase under an implementation-kind
  parent must be
  `completed` or `skipped` with notes before phases after Implementation can
  become ready.

When a gate stops holding (for example Plan Review is reopened), downstream
phases that had advanced are reset to `pending` and their outcomes cleared, so
stale readiness never survives a regression upstream. Downstream `blocked`
phases are an exception: they are reset only when Plan Review's gate is
unsatisfied — whether Plan Review itself regressed or an earlier gate broke —
and keep their blocked state under any other gate regression.

In branched graphs, reset propagation follows dependency edges. A regression in
one branch resets only transitive successors of that branch; independent sibling
branches keep their stored state.

## Custom phase graphs

The built-in `default` preset is the familiar linear graph: Plan → Plan Review
→ Implementation → Review loop → PR Creation → Autoreview → Merge. Config can
declare additional named presets under `[flow]`, and `approach flow create
--preset NAME` or `[flow].preset` can select them for new Flows. CLI and TUI
"next phase" surfaces remain display-order based: with fan-out, "next" means
the first ready launchable branch in `OrderedPhases` order, not a scheduling
guarantee.

Custom preset validation is strict at config/create time:

- Phase IDs must be unique after normalization.
- Top-level `depends_on` edges must reference top-level phases in the preset.
- The graph must be acyclic and include at least one root.
- At most one phase may have kind `merge`.
- Unknown kinds are rejected in presets. Existing hand-authored records with an
  unknown kind still load and use the default gate.

Readiness is best-effort for existing on-disk records. If Kahn's algorithm
cannot release a node because of a cycle, dangling dependency, duplicate
normalized ID beyond the first occurrence, or a transitive dependency on one of
those nodes, Approach leaves that node's stored status untouched. This avoids
destroying recorded work on a malformed hand-authored record. Index-aware launch
eligibility prevents stale duplicate rows from being selected for manual or
AutoMode launches.

Records created from named custom presets persist `preset_name`. If a record is
later read with missing `depends_on` keys, Approach uses the named preset as a
recovery hint when the preset is available and its phase IDs still match. The
non-persisted `GraphRecovery.Status` values are:

- `preset_edges_restored`: missing edges were restored from the named preset.
- `missing_edges_unresolved`: Approach refused to invent a graph. Re-select or
  recreate the preset, or repair the record manually.

Default-preset records still use legacy linear backfill when `depends_on` is
absent. Edge-less custom records without a recoverable preset remain readable
but degraded; their stored statuses are preserved except where a later
phase-affecting mutation can safely re-derive readiness from explicit edges.

AutoMode is drain-based for DAGs. A successful phase completion arms an
in-memory drain for that Flow; each poll launches the first ready non-merge
launchable phase only when no phase in that Flow is `running` and no
flow-scoped embedded terminal is still open or auto-closing. This serializes
branches so parallel agents do not collide in one worktree. Skipped phases do
not arm the drain, even when skip-with-notes readies successors. Resetting a
phase back to `ready` also does not arm the drain. Completing an
`autoreview`-kind phase may launch a custom non-merge successor; in the default
preset it still stops because the only successor is merge-kind.

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
transition, Approach marks the Flow phase `needs_attention` with a sync-failure note
and returns the persistence error so the agent can report it. Repeating
`completed` for an already-completed Flow phase preserves that completed state
even if the linked-plan sync later fails.
Manual GitHub merge recording is stricter: if syncing the linked plan's Merge
phase fails while terminal merge metadata is being recorded, Approach restores the
previous PR status, clears that terminal metadata, marks the Merge phase
`needs_attention`, and keeps the Flow recoverable instead of deriving it as
`merged`.

## Per-phase agent settings

Every phase persists three optional fields — `agent`, `model`, and
`reasoning_effort` — captured from the agent settings in effect when the Flow is
created. All Flow creation paths stamp them: the TUI "create Flow" and "create
Flow and plan now" actions, the Ready-Beads shortcut, and `approach flow create`
(from the `[agent]` config). Implementation child phases inherit their parent
implementation phase's values when they are first created; re-running
`approach flow phase add-child` never overwrites them.

The fields are validated against the same rules as the `[agent]` config, with
one addition: the stored model and reasoning effort are checked against the
phase's own agent. The agent is required whenever a model or reasoning effort is
set, so a model with no agent is rejected; an agent on its own is fine.

Two layers enforce that, and they behave differently on purpose:

- The store is the invariant. `flowstore.Store.CreateWithOptions` rejects an
  unusable triple and writes no record, for both the create-time default and any
  phase that declares its own settings.
- The TUI request mapping drops before it stores. `FlowStarter` discards an
  unusable triple rather than passing it down, so Flow creation cannot start
  failing on an agent selection that used to be accepted. A dropped triple
  stamps nothing, which reads as "resolve from the global setting at launch".

In practice neither path is reachable from normal use: config loading already
validates the `[agent]` block, and the TUI picks models and efforts from fixed
choice lists.

Each field means something on its own:

- Empty — nothing was captured; resolve that field from the global setting in
  effect at launch.
- `default` — captured, and means the provider default. It is stored verbatim.
- Anything else — captured and concrete.

Nothing consumes these values at launch yet: `FlowPhaseLauncher`,
`FlowStarter.StartPlan`, and Flow repair still resolve the agent, model, and
reasoning effort from the current global settings. The fields are recorded for
the follow-up that surfaces and then honors them.

## Compatibility and migration

- The persisted schema gains three optional phase fields (`agent`, `model`,
  `reasoning_effort`); `schema_version` stays `1` and no status strings were
  added, removed, or renamed. Existing Flow JSON needs no migration, and records
  written without those fields keep reading and writing without them.
- Derived state is self-healing: phase-affecting mutations (`SetPhase`,
  `AddChildPhase`, `SetPR`, `AddPhaseLaunchID`,
  `ResetRecoverableRunningPhase`) re-derive readiness for any graph. Records
  with explicit persisted edges opt into load-time readiness normalization, and
  records containing a plan-review-kind phase are also normalized on load.
  Edge-less, kind-less custom records keep stored statuses instead of receiving
  guessed edges.
- Completed plan-review phases persisted before outcomes existed are
  normalized to `approved` on read.

## TUI rendering

The flows pane renders the persisted status, or the phase outcome when one is
recorded (for example `plan-review:approved`). Recovery labels for partial
states (`recover-worktree`, `await-session`, `ended-session`,
`session-mismatch`, `missing-session-id`, `missing-pr`) are layered on top, rendered prefixed
with the phase ID like any phase state (for example `autoreview:missing-pr`),
and are display-only; they never change persisted phase status. See
`docs/tui-guide.md` for the pane behavior.

When `await-session` or `ended-session` is recoverable and predecessor gates
still hold, the selected phase row can be reset with `x` after confirmation or
with `approach flow phase reset`. The reset removes the stale latest launch and
matching latest-launch sessions, persists the phase as `pending`, then lets
derived readiness promote it to `ready`; if readiness cannot be derived, the
mutation is rejected and the record is left unchanged. Preserved attached
session history on the merged logical phase must already be ended; any live or
unknown-status session must finish before reset is allowed. Session mismatches
are higher priority than `ended-session` and must be resolved instead of reset.
