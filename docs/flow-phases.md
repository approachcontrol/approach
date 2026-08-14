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
  Reopening a finished phase deliberately remains
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
- The TUI can release an unfinished session on a selected phase, which is what
  recovers a phase blocked by a session that never reached `ended`. `x` runs the
  reset above when the phase is resettable and otherwise probes the session
  store and the phase mirror for launches that still look live; a confirmation
  then records each of them as ended, exactly as a clean exit would. It is
  TUI-only — `approach flow phase reset` has no release counterpart yet — and it
  changes no phase status, outcome, or note. It refuses while any agent is
  provably live on the phase (a running embedded Flow terminal, or a live tmux
  window for any of the phase's launches), including when a resume started a
  second agent beside the crashed one: dismiss the terminal, or attach with `T`
  and exit the agent in its tmux window, before releasing. Outside those two
  backends nothing can be probed, so the confirmation names the sessions as
  unverified and the user's answer is the guard.
- **Limitation.** A `running` phase that also carries a session launch mismatch
  does not fully recover. Release ends the stale launch and the mismatch then
  surfaces as a repair obstruction, but `R` is not a route out of it either:
  `ResetRecoverableRunningPhase` is the only operation that removes sessions and
  it rejects a mismatch first, `approach flow phase restart` requires
  `needs_attention` or `blocked` and never touches sessions, and a repair agent
  is forbidden from editing Flow artifact JSON. This is a pre-existing gap that
  release makes visible rather than one it creates.

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

Records created from named custom presets persist `preset_name`. Edge recovery
uses it as a hint when a legacy record's `depends_on` keys are missing — but
that recovery runs **only during the one-time migration into `approach.db`**,
not on every read as it did under the file store. Records are canonical at write
time now, so what the migration decided is what the record carries from then on.

One recovery marker is persisted: `missing_edges_unresolved`, meaning Approach
refused to invent a graph because the named preset was unavailable or its phase
IDs no longer matched. It fences phase mutation on that record, and it does not
clear when the preset is restored later. The migration notice names every Flow
in this state at the one moment it is cheap to fix — see `docs/config.md`. After
that, the remedies are to recreate the Flow or to repair it directly in the
database, which requires setting `depends_on` on the phases **and** deleting the
`graph_recovery` marker: the fence keys off the marker, not off the edges, so
editing `depends_on` alone leaves the Flow blocked. Re-running the cutover is
**not** a safe remedy once the database has been in use, because `flows/` is a
snapshot frozen at migration time rather than a live mirror.

Default-preset records still use legacy linear backfill when `depends_on` is
absent. Edge-less custom records without a recoverable preset remain readable
but degraded; their stored statuses are preserved except where a later
phase-affecting mutation can safely re-derive readiness from explicit edges.

AutoMode is drain-based for DAGs. A successful phase completion arms an
in-memory drain for that Flow; each poll picks the first ready non-merge
launchable phase from the poll snapshot and launches it only when no phase in
that Flow is `running`, no flow-scoped embedded terminal is still open or
auto-closing, no manual resume or repair on that Flow is mid-write, and no
session recorded against the candidate phase is still live. The candidate is
then re-validated — not re-selected — against the authoritative record before
it launches, so an earlier phase becoming ready in that window does not steal
the launch; the already-chosen candidate proceeds as long as it is still
launchable on its own merits, which is the same rule the store enforces. This
serializes branches so parallel agents do not collide in one worktree. Skipped phases do
not arm the drain, even when skip-with-notes readies successors. Resetting a
phase back to `ready` also does not arm the drain. Completing an
`autoreview`-kind phase may launch a custom non-merge successor; in the default
preset it still stops because the only successor is merge-kind.

## Derived Flow status

The Flow-level `status` field is always derived, in priority order: closed
record → `closed`; abandoned record → `abandoned`; merge recorded merged →
`merged`; merge blocked or any phase blocked → `blocked`; any phase
needs_attention → `needs_attention`; all phases completed/skipped → `completed`;
any phase started → `in_progress`; otherwise `pending`.

## Closing a Flow

`C` in the TUI closes a Flow, which records a required human-entered reason and
a close timestamp on the record. An explicit close outranks everything derived
from phase or merge state, so a closed Flow derives `closed` no matter what its
phases say. Closing is deliberately distinct from `d` delete: the record, its
phases, its plan link, and its history all survive.

Closing does **not** rewrite phases. A Flow closed mid-implementation keeps its
`running` and `ready` phases exactly as they were, so the record still explains
where the work stopped, and a running session is not killed. Terminality is
enforced at the launch gates instead: a closed Flow accepts no phase launch
(manual, auto-mode, or store-side auto-launch validation), no repair, no
mark-merged, no auto-mode arming, no phase reset, and no session resume, and it
drops out of the Active Flows view the way merged Flows already do.
The store repeats those checks inside the mutation transaction so a close that
wins a stale launch/reset/merge request cannot be overwritten. Repair's final
read and terminal start share a short cross-process advisory lock with close,
giving concurrent repair and close operations an explicit order.

`C` on a closed Flow reopens it through a `y/n` confirm, which clears the
closure. Because phases were never touched, reopening restores exactly the
launchability the Flow had before the close.

The close reason is visible in the flows pane row, indexed by `/` search, and
present in `approach flow read` JSON as `closed.reason` and `closed.closed_at`.

One compatibility caveat: a build predating this feature reads the record,
ignores the unknown `closed` field, and derives whatever the phases imply — so
its auto-mode could launch a phase on a Flow you closed. This is inherent to the
additive-field approach and resolves as builds update.

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

The plan write happens **after** the Flow phase change commits, so there is a
brief window in which the phase reads as completed before a failed sync demotes
it. The demotion can also fail to land at all — on a crash inside the window, on
a second write that cannot acquire the writer, or when the demotion is
deliberately declined rather than allowed to overwrite a concurrent writer:
because that writer re-completed the phase in the meantime, or because it
recorded a merge against a completed Merge phase, which a demotion would leave in
a state `approach flow merge set` itself refuses to create. The Flow then keeps a completed phase
beside a stale plan, with no `needs_attention` marker and only the returned error
as a signal. Writes that do not change the completion — attaching an agent
session, recording a resume launch — do not trigger that decline; only a
different completion does. The manual-merge rollback has the same exception:
when a concurrent write already owns that state, the Flow stays `merged` and the
error is the only signal.

That window is externally visible, not just a crash hazard. While it is open the
phase reads as legitimately completed and its successor as legitimately ready, so
auto-advance can launch that successor; the failed sync then demotes the
predecessor and readiness resets the just-launched successor to `pending` while
its launch attempt stays recorded against it. The launched agent is not wedged —
its session ends normally, and readiness re-promotes the row — but the agent's
own completion write is rejected while its phase sits at `pending`. A successor
that gets all the way to `completed` or `skipped` inside the window is reset the
same way, and its outcome is cleared: demoting a phase resets every downstream
phase whose dependency gate it satisfied, so repeating the predecessor's
completion makes the successor ready again but does not restore its outcome. The
same states are reachable without any sync failure, by restarting a completed
predecessor.

Recovery is always the same idempotent plan write, but which command reaches it
depends on the state the Flow was left in.

- **The phase is still `completed` and carries no marker** — the crash, lost
  writer, and declined-guard windows above. Repeat the phase completion, or
  re-record the manual merge with the same metadata. `completed` → `completed`
  and an already-merged repeat are both accepted as idempotent no-ops that re-run
  the plan write, and neither demotes state that is already correct.
- **The phase was demoted to `needs_attention`** — the compensation landed, which
  is the ordinary sync failure. Repeating the completion is rejected here:
  `needs_attention → completed` is not a legal transition. Restart the phase
  first (`approach flow phase restart`), then complete it; that completion runs
  the plan write.
- **The Flow already reads as `merged`** — a manual merge whose compensation did
  not land. The store accepts the already-merged repeat, but the TUI offers its
  manual-merge action only while the Flow is not yet derived as `merged`, so that
  repeat is not reachable from the TUI once the merge is durable. The linked plan
  is the only stale artifact, so repair it directly:
  `approach plan phase set --plan-id "$PLAN_ID" --phase-id merge --status completed`.

One caveat on both retries: because neither may demote state that is already
durable, each **discards** a second sync failure and returns success. A repeated
completion of an already-completed phase reports success even when the plan write
failed again, and so does an already-merged manual merge. Confirm the linked
plan's own phase status rather than trusting either return value.

## Prepared child Flows and epic progression

`FlowStarter.PrepareFlow` creates the Flow and worktree, records start metadata,
runs the repository bootstrap hook, and only then consumes its one-shot store
finalizer. A successful finalizer stamps `PreparedAt`; callback failure keeps
the existing startup-phase blocking behavior. If receipt persistence reports a
commit error, the store reads the Flow back: a matching receipt is success, a
confirmed nil receipt is incomplete and blocks launchable phases, and an
unreadable result is unknown and is not compensated because the receipt may be
durable.

Epic enablement accepts only one open `pending` exact-link Flow with that
receipt. The TUI holds its launch/close reservation while a single SQLite
writer transaction re-reads the Flow and enables the epic progression row.
This slice records the pending Flow as a runtime baseline before releasing the
reservation, but does not dispatch a phase or advance to another child. A
restart installs no baseline and performs no catch-up; live-edge advancement is
owned by the later sequential-advance slice.

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
- The TUI creation mapping drops before it stores. The shared capture helper
  used by parked creation and the `createPhase` lifecycle discards an unusable
  triple rather than passing it down, so Flow creation
  cannot start failing on an agent selection that used to be accepted. A
  dropped triple stamps nothing, which reads as "resolve from the global
  setting at launch".

In practice neither path is reachable from normal use: config loading already
validates the `[agent]` block, and the TUI picks models and efforts from fixed
choice lists.

Each field means something on its own:

- Empty — nothing was captured; resolve that field from the global setting in
  effect at launch.
- `default` — captured, and means the provider default. It is stored verbatim.
- Anything else — captured and concrete.

New phase launches resolve these fields independently. An empty `agent` uses
the globally selected agent. A non-empty phase agent selects that provider's
current global model and effort, even when another provider is selected
globally; each non-empty phase model or effort then overrides its corresponding
fallback. The raw triple is normalized and validated before fallback, so an
invalid model-only stamp or provider-incompatible value cannot be repaired by
an otherwise valid global preference. A literal `default` is non-empty and
remains an explicit provider-default choice.

Manual launches, AutoMode, and the initial Plan launch all use the target
phase's effective settings. The launch-ID write is the linearization point: a
settings edit committed before that write is honored, while one committed
after it affects the next launch. Phase-scoped repair uses the obstructing
phase's effective settings; a graph-wide obstruction has no target phase and
therefore resolves entirely from globals. Session resume and the generic Flow
worktree agent keep their existing provider rules because neither is a new
target-phase launch.

Replace the complete stamp with:

```bash
approach flow phase agent set --flow-id "$FLOW_ID" --phase-id implementation \
  --agent claude --model claude-opus-5 --reasoning-effort high
```

Omitted model and effort flags intentionally remain empty fallbacks. Use
`--clear` instead of `--agent` to clear the whole stamp. This settings-only
mutation is permitted for every phase and Flow status, including closed Flows;
it changes no phase status, outcome, dependencies, launch/session history,
readiness, or linked plan. An identical replacement preserves timestamps.

## Compatibility and migration

- The persisted schema gains three optional phase fields (`agent`, `model`,
  `reasoning_effort`); `schema_version` stays `1`, `closed` is an additive
  status, and no existing status strings were removed or renamed. Records
  written without those phase fields keep reading and writing without them.
  New records also always persist the
  metadata field `headless`, and a legacy record that omitted it migrates to
  `true`.
- Records themselves are migrated once, on first open: `flows/<id>/meta.json`
  is imported into `approach.db` and the old tree is left untouched in place.
  See `docs/config.md` for the cutover's guarantees. Phase and
  status semantics are unchanged by that move — only the storage medium is.
- Preset-based `depends_on` recovery is part of that one-time migration and no
  longer re-runs on read. A record whose preset was missing at migration keeps
  its `missing_edges_unresolved` marker permanently, and restoring the preset
  afterwards does not clear it; the migration notice names such Flows while
  re-running the cutover is still safe, and the mutation-fence error afterwards
  offers only remedies that do not discard post-migration Flows.
- Records are canonical at write time rather than re-derived on every read, so
  a rejected `approach flow phase reset` now leaves the record untouched
  instead of demoting the phase to `pending` as a side effect. A reset blocked
  by unsatisfied predecessors reports `requires satisfied predecessors` rather
  than the older, less precise `requires running recoverable phase`.
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
