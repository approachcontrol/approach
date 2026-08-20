# ADR 0002: Flow launch context as a deep module

Status: **proposed — pending user approval** (2026-08-18)

`approach-hyl.1` is a HITL bead. This Flow ran headless in auto mode, so the
bead's "approved by the user" criterion cannot be satisfied from inside it.
Every decision below is a *proposal* with its evidence; the final section lists the open
questions as explicit decision requests. Approval happens out of band.

## Context

`actions.AgentLaunchContext` (`actions/actions.go:1055`) is a 30-field struct
that any caller can compose by hand. Ten of its fields are Flow role markers
whose legal combinations are implicit. The evidence base is
`docs/flow-launch-variant-matrix.md`: eight launch kinds
(`model/flow_launch_intent.go:14`), seven construction sites, 17 reachable
variants, and 19 consumers.

Three facts from the matrix drive this design.

1. **Construction is not the only place role fields are set.**
   `FlowLaunchTracked` is stamped at `model/flow_launch_lifecycle.go:1105`;
   `Embedded` is forced true in four places and false in five. An interface that
   owns only construction does not own the values consumers read (matrix §3).
2. **Three consumers already hand-write a full role predicate and disagree.**
   `actions.resumeSessionIDForContext` (`actions/actions.go:1450`, 12 negated
   fields), `actions.validateTrackedRepoTmuxRole` (`actions/tmux_mode.go:454`,
   10 fields), and the autofix arm of `flowEmbeddedTerminalIdentity`
   (`model/embedded_terminal.go:746`, 9 fields) each assert "this context is
   role X" with a different field set (matrix F2).
3. **The lifecycle asks a question the context cannot answer.** Both
   `model/flow_launch_lifecycle.go:1104` and `:958` enumerate the same four
   kinds by hand, because `attempt.Kind` is available there and the context
   carries no equivalent (matrix F6).

## Decision

### D1 — a closed `flowLaunchRole` enum of six roles

The 17 variants map to six roles. Each role is a distinct *policy class*: at
least one consumer changes what it does, not merely how it renders.

| Role | Variants | Distinguished by |
| --- | --- | --- |
| `RoleTrackedPhase` | V1–V6 (manual, auto, create) | tracked + phase, no resume session |
| `RolePhaseResume` | V7–V10 | tracked + phase + `ResumeSessionID` |
| `RoleRepair` | V11–V12 | `FlowRepair` |
| `RoleAutofix` | V13–V15 | `FlowAutofix` |
| `RoleWorktreeAgent` | V16 | `FlowAgent` |
| `RoleSavedSessionResume` | V17 | `FlowSavedSessionResume` |

Collapses, each argued from the matrix:

- **manual + auto collapse.** No consumer changes policy class on
  `FlowAutoLaunch`; the three that read it — dock visibility
  (`model/embedded_terminal.go:458`), focus
  (`model/model_keys.go:3010`), and `validateTrackedRepoTmuxRole`
  (`actions/tmux_mode.go:458`) — change *presentation* or assert a
  reachability invariant. `FlowAutoLaunch` therefore becomes payload, not role.
- **create collapses into `RoleTrackedPhase`.** No predicate anywhere
  distinguishes it. Its two apparent differences are payload
  (`PlanPhaseID`/`Title`/`Status`, read only by the env export at
  `actions/actions.go:1424`) and a pipeline artifact (its tracked stamp arriving
  at install time, matrix F4), which D3 removes.
- **resume does not collapse into `RoleTrackedPhase`.** `ResumeSessionID`
  suppresses prefill (`actions/actions.go:1352`), changes the `launchKind`
  label (`model/flow_launch_pin.go:132`), and `FlowPhaseTerminal` — which only
  resume sets — flips `flowLaunchFailureUpdate` from "regress the phase" to
  "leave it alone" (`model/model_keys.go:3158`). That is a policy class.
- **autofix does not collapse into `RoleWorktreeAgent`.** They differ in three
  consumers: detach policy (`model/embedded_terminal.go:989` — agent never
  detaches, autofix does), route eligibility (autofix reaches tmux, V15; the
  worktree agent has no tmux call site), and slot stamping
  (`model/embedded_terminal.go:488` stamps `FlowAgent` but not `FlowAutofix`).

### D2 — one builder, with role-specific payloads

```go
// package actions
type FlowLaunchRole int

type TrackedPhaseTarget struct {
    PhaseID, PhaseKind string
    Auto               bool
    PlanPhase          *PlanPhaseTarget // create only
}
type PhaseResumeTarget struct {
    PhaseID, PhaseKind string
    SessionID          string // required, non-blank
    PhaseTerminal      bool
}
type AutofixTarget struct{ PRNumber int }
type SavedSessionResumeTarget struct{ SessionID string }
// RoleRepair and RoleWorktreeAgent carry no payload beyond the Flow ID.
```

```go
// package model
func newFlowLaunchContext(target flowLaunchTarget, settings flowLaunchAgentSettingsSnapshot,
    route flowLaunchRouting) (actions.AgentLaunchContext, flowLaunchRoute, error)
```

`target` is a sum over the payload structs above, so only the autofix payload
carries a PR number and only the two resume payloads carry a session ID: the
invalid combinations the matrix prunes become unrepresentable rather than
merely unreached.

### D3 — the builder owns the route, writing `Embedded` and `FlowLaunchTracked` exactly once

Open question (a) from the plan, decided: **builder-computed with the route as
an input**, not a post-construction transition function.

The evidence is that every tmux-capable kind already computes the route
immediately next to its literal and then rewrites `Embedded`
(`model/flow_phase_launch.go:402–406`, `model/flow_launch_resume.go:299`+`:388`,
`model/flow_launch_autofix.go:395–399`). Folding `tmuxLaunchRouteFor` into the
builder and returning `(ctx, route)` removes all three rewrites and makes
`Embedded` a derived value — `route == embedded` — rather than a mutable flag.
`FlowLaunchTracked` is set at construction for `RoleTrackedPhase` and
`RolePhaseResume`, which makes the install-time stamp at
`model/flow_launch_lifecycle.go:1105` redundant for every kind including create
(matrix F4).

A post-construction transition function was rejected because it keeps two
writers for one field and preserves the very ordering hazard the matrix
documents: `Headless` must be resolved *before* the route is decided
(`model/flow_phase_launch.go:396–402`), and only a builder that owns both can
enforce that ordering by construction.

The remaining forced writes stay, reclassified as assertions rather than
mutations: `model/flow_launch_lifecycle.go:1097`,
`model/embedded_terminal.go:479`, `model/embedded_terminal.go:200`,
`actions/actions.go:1248`, `actions/tmux_mode.go:313`, `model/tmux_mode.go:132`.
Each becomes a check that the context already agrees with the transport it
reached, failing loudly if not.

### D4 — the classifier is partial; a Flow-marked context that fails to classify is an error

Open question (b), decided:

```go
// package actions
func FlowLaunchRoleOf(ctx AgentLaunchContext) (FlowLaunchRole, bool)
func ValidateFlowLaunchRole(ctx AgentLaunchContext) error
```

`FlowLaunchRoleOf` returns `false` for the four non-Flow launch contexts the
matrix lists (plan implementation, non-Flow session resume,
open-agent-in-worktree, slice-epic), and for the two non-launch literals that
carry Flow IDs without a role (`model/flow_session_release.go:407`,
`model/flow_launch_lifecycle.go:1051`). Those are legitimate and must keep
working, so totality is not available. The two Flow-ID-carrying ones are also
why `ValidateFlowLaunchRole` belongs on the launch path rather than on every
context: neither is ever routed or turned into a command.

`ValidateFlowLaunchRole` is the seam check: if the context carries any Flow
marker but `FlowLaunchRoleOf` returns `false`, it carries them in a combination
no role admits — an invariant violation, and an error. That preserves today's
behaviour at the one place that already errors (`actions/actions.go:1467`)
while extending it to the two predicates that currently fail silently or by
exclusion.

The marker-presence half must stay independent of classification. Today's
`flowLaunchContextRequiresLifecycle` (`model/tmux_mode.go:329`) is exactly that
predicate — a disjunction over the ten fields it already names — so it
moves to `actions` as `HasFlowLaunchMarkers`, gaining one clause so that its
field set matches the boundary test's eleven exactly (`FlowAutofixPRNumber != 0`
is today the only Flow-specific value neither predicate covers), and becomes the
check's left operand. Re-expressing it as `_, ok := FlowLaunchRoleOf(ctx)`
would be circular: the two halves could never disagree, and the runtime net
would accept every malformed hand-assembled context it exists to reject.

The defining test is the round trip:
`FlowLaunchRoleOf(newFlowLaunchContext(target, …)) == target.Role()` for all
six roles across all 17 variants.

### D5 — the role type lives in `actions`, the builder in `model`

Open question (c), decided against the plan's working sketch, on a hard
constraint: **`model` imports `actions`, so `actions` cannot import `model`.**
Three of the consumers that need the role — `resumeSessionIDForContext`,
`validateTrackedRepoTmuxRole`, and the tracked-lease branch
(`actions/tmux_mode.go:332`) — are in `actions`. A role defined in `model` is
unreachable from them, and mirroring the enum in both packages reintroduces the
two-definitions-of-one-concept problem this ADR exists to remove.

So: `FlowLaunchRole`, the payload structs, `FlowLaunchRoleOf` and
`ValidateFlowLaunchRole` go in a new `actions/flow_launch_role.go`, alongside
`HasFlowLaunchMarkers` relocated from `model/tmux_mode.go:329`;
`newFlowLaunchContext` goes in a new `model/flow_launch_context.go`, because it
needs prompts, records, settings snapshots and the routing probe, none of which
`actions` has. `flowLaunchKind` stays in `model` unchanged: it is the
*submission* intent, and it legitimately outlives the launch (the lifecycle
keys attempts and fences on it).

### D6 — post-migration form of every consumer

| Consumer | After |
| --- | --- |
| `flowEmbeddedTerminalIdentity` | `switch FlowLaunchRoleOf(ctx)`; the 9-field autofix arm collapses to one case reading `ctx.FlowAutofixPRNumber` — the classifier returns a role, not a payload, and `FlowAutofixPRNumber` stays on the context as `RoleAutofix`'s payload |
| `flowEmbeddedTerminalDetachPolicy` | `switch` on role: `RoleWorktreeAgent`, `RoleSavedSessionResume` → never |
| slot stamping | stamps the role, not four booleans |
| dock visibility, `updateFlowTerminalFocusAfterLaunch` | unchanged — they read `FlowAutoLaunch`/`Headless`, which stay payload |
| `reserveFlowSpawn` | `role == RoleTrackedPhase \|\| role == RolePhaseResume` |
| `flowLaunchFailureUpdate` | `RoleTrackedPhase`, or `RolePhaseResume` with `!PhaseTerminal`; all other roles refuse |
| `tmuxRouteEligible` | folded into the builder (D3); survives as the eligibility rule it already is |
| `flowLaunchContextRequiresLifecycle` | moves to `actions` as `HasFlowLaunchMarkers`, plus a `FlowAutofixPRNumber` clause — it must stay marker-based, not role-based (see D4) |
| `ShouldPrefillEmbeddedPrompt` | four hand-written arms → `role != RolePhaseResume && role != RoleSavedSessionResume`, plus the existing transport conditions |
| `resumeSessionIDForContext` | `ValidateFlowLaunchRole` + `role == RoleSavedSessionResume` |
| `validateTrackedRepoTmuxRole` | `ValidateFlowLaunchRole` + role ∈ {`RoleTrackedPhase`, `RolePhaseResume`}; the dead `FlowAutoLaunch` clause (matrix F1) becomes the explicit invariant `Auto ⇒ Headless ⇒ embedded`, checked in the builder |
| tracked lease branch, window namers | unchanged — they read `LaunchID`/`FlowPhaseKind`, which stay payload |
| `launchKind` | `switch` on role; **fixes F3** by separating `RolePhaseResume` from `RoleSavedSessionResume` |
| `registerLaunchControl` | `_, ok := FlowLaunchRoleOf(ctx); ok && LaunchID != ""` — every Flow role registers, phase-less roles still as unowned |
| `reconcileInteractiveLaunchExitCmd` | `role.MutatesPhase()` (i.e. `RoleTrackedPhase`/`RolePhaseResume`, V1–V10) instead of `FlowLaunchTracked` + non-empty phase ID |
| lifecycle kind enumerations (`:1103`, `:958`) | `role.MutatesPhase()` — removes both hand-written four-kind lists (F6) |

## Enforcement

Chosen: **(2) a boundary test as the enforceable seam now, plus (3) a runtime
invariant check as the safety net. (1) unexported-field discipline is deferred
out of this epic.**

**The boundary test's exact scope.** An AST walk, in the style of the existing
`model/flow_launch_boundary_test.go`, over `model/` and `actions/`
non-`_test.go` files, failing on any composite literal of
`actions.AgentLaunchContext` that sets *any* of the eleven Flow marker fields
(`FlowID`, `FlowPhaseID`, `FlowPhaseKind`, `FlowLaunchTracked`,
`FlowAutoLaunch`, `FlowRepair`, `FlowAgent`, `FlowSavedSessionResume`,
`FlowAutofix`, `FlowPhaseTerminal`, `FlowAutofixPRNumber`) outside
`model/flow_launch_context.go`,
with a file:line allowlist for the two non-launch literals below.

The scope is chosen from the matrix. It needs no exemption for the four
non-Flow literals or the two probe-only literals
(`model/flow_launch_resume.go:299`, `model/tmux_mode.go:392`) — none of them
sets a marker. It does need an explicit allowlist for the two literals that set
`FlowID`/`FlowPhaseID` without being launches: the session-release finalizer
(`model/flow_session_release.go:407`) and `blockAutoFlowLaunchPhase`
(`model/flow_launch_lifecycle.go:1051`). Both address an existing launch record
rather than constructing one, so routing them through a launch builder would be
wrong; the allowlist is the honest encoding of that, and it is two entries the
test names and pins. With those exempted, the rule catches exactly the seven
Flow builders.

**The runtime net.** `ValidateFlowLaunchRole` rejects any context that carries
Flow markers (`HasFlowLaunchMarkers`) but classifies to no role. It belongs in
`actions.agentCommandSpec`, the one function every transport funnels through —
external terminal (`AgentLaunchWithOptions`), repo tmux
(`repoTmuxAgentLaunchWithExecutable`), embedded tmux
(`EmbeddedTmuxAgentCommand`), and embedded direct (`AgentCommand`) all reach it.
Placing it at the two terminal entry points alone would leave the embedded path
— which is the hardcoded route for four of the seven builders — unchecked, i.e.
exactly the future-caller case the net exists for.

Rejection reasons for the alternatives:

- **(1) unexported fields.** The matrix shows four non-Flow launch literals, two
  probes, and two non-launch Flow-ID literals that legitimately compose
  `AgentLaunchContext` directly. Making the Flow fields
  unexported forces a constructor for those paths too, which is a strictly
  larger change than this epic scopes, and it cannot be done incrementally —
  the field either is exported or is not. Deferred, not dismissed: it is the
  only mechanism that makes hand-assembly *impossible* rather than *detected*,
  and it becomes cheap once D2's builder is the only Flow-marking writer.
- **(3) runtime check alone.** It fires only on paths that execute. The matrix
  has variants that a test suite reaches rarely (V10, V15) and one predicate
  whose failure mode is already dead code (F1). A check that only speaks when
  exercised would not have caught F1 at all.
- **(2) alone.** A boundary test constrains the repository, not the package's
  API; a future caller in a new package, or a test helper promoted to
  production, escapes it. Hence (3) as the net.

**Migration order for downstream slices**, with reasons:

1. `approach-hyl.2` — tracer bullet. Use `RoleRepair`: one marker field, one
   route, no route decision, no phase writes, and its refresh step
   (`model/flow_repair.go:182`) exercises the "field set after the literal"
   problem in its simplest form.
2. `approach-hyl.11` — classifier. Pure addition plus the deletion of the three
   hand-written predicates (F2). Lands before any consumer moves, so each later
   slice deletes rather than rewrites.
3. `approach-hyl.10` — model consumers. The largest surface, but every change is
   mechanical once the classifier exists.
4. `approach-hyl.8` — `createPhase` last. It is the only kind whose tracked
   stamp moves from install to construction (F4, D3), so it is the only slice
   that changes lifecycle behaviour rather than just its expression.

## Grill dossier

For each collapse, "what breaks if these two become the same value?"

- **manual ≡ auto.** Nothing in policy; `FlowAutoLaunch` survives as payload. But
  the invariant `Auto ⇒ Headless` (F1) is currently enforced by two unrelated
  code paths. If the role collapses them and the builder does *not* assert that
  invariant, an interactive auto launch becomes representable and reaches
  `validateTrackedRepoTmuxRole`'s rejection. **The builder must assert it.**
- **manual ≡ create.** Breaks nothing observable *provided* `PlanPhase*` stays
  in the payload; those three fields reach providers as env
  (`actions/actions.go:1424`) and a Flow agent that lost them would see an empty
  `APPROACH_PLAN_PHASE_ID`.
- **resume ≡ tracked phase.** Breaks three things: prefill would fire on a
  resume, `launchKind` would mislabel it, and a failed resume of a completed
  phase would regress it to `needs_attention`.
- **autofix ≡ worktree agent.** Breaks detach policy (the agent's terminal is
  deliberately nondetachable), and would make the worktree agent tmux-eligible
  with no call site to serve it.
- **saved-session resume ≡ phase resume.** Breaks the only assertion in the
  codebase that currently returns an error for a malformed role
  (`actions/actions.go:1467`) and would let a phase-untracked session resume
  claim a phase.

For each field the role does *not* carry, "which consumer proves it is payload?"

| Field | Proof it is payload |
| --- | --- |
| `FlowAutoLaunch` | dock visibility (`model/embedded_terminal.go:458`) and focus (`model/model_keys.go:3010`) read it *within* a single role |
| `Headless` | read by `UsesStreamJSONOutput` and focus across five of six roles |
| `FlowPhaseTerminal` | read only inside `RolePhaseResume`, by `flowLaunchFailureUpdate` |
| `FlowAutofixPRNumber` | read only inside `RoleAutofix`, by `flowEmbeddedTerminalIdentity` |
| `ResumeSessionID` | distinguishes `RolePhaseResume` from `RoleTrackedPhase` — **role-determining**, hence in the payload *and* in the classifier |
| `PlanPhase*` | export-only (`actions/actions.go:1424`); no predicate reads it |
| `WorkingDir` | set by four roles and unset by three (matrix F5) — payload, but its emptiness is currently load-bearing for manual/auto/create/repair |

## Open questions requiring approval

1. **D5 reverses the bead's working sketch.** The bead proposes the role in
   `model/flow_launch_context.go`; the import direction forbids it. Confirm the
   role type moving to `actions`.
2. **F1 is a latent bug, not just a design smell.** Should `approach-hyl` fix it
   (assert `Auto ⇒ Headless` in the builder) or file it separately?
3. **F3 (`launchKind` conflating the two resume roles) changes `launch.json`
   output.** Confirm that is acceptable, or the label must be preserved.
4. **D3 makes `newFlowLaunchContext` take the routing probe**, which means the
   builder does I/O-adjacent work (`actions.TmuxAvailable`). Confirm, or keep
   routing outside and accept a two-step `(build, route)` with a documented
   ordering contract.
5. **D1's six roles vs. seven** — splitting `RoleTrackedPhase` into
   manual/auto/create would make the enum mirror `flowLaunchKind` exactly, at
   the cost of three roles no consumer distinguishes. Confirm the collapse.
6. **Enforcement scope**: the boundary test as specified covers `model/` and
   `actions/` only. Confirm no other package should be in scope.

## Implementation note — tracer bullet (`approach-hyl.2`)

The first slice landed the seam and migrated one launch kind. Three things
about it diverge from the text above and stay true until a later slice changes
them:

- **The tracer migrated `RoleWorktreeAgent`, not `RoleRepair`.** The migration
  order above nominates repair; the worktree agent is the strictly simpler
  tracer — one marker, one hardcoded route, no phase writes, no post-literal
  field mutation — so it went first. Repair still owns its own slice, where
  the post-literal refresh write at `model/flow_repair.go` gets handled.
- **D5 was followed.** `FlowLaunchRole` lives in `actions/flow_launch_role.go`;
  `newFlowLaunchContext` and the `flowLaunchTarget` sum live in
  `model/flow_launch_context.go`. Open question 1 is therefore answered in the
  code, not the ADR's status.
- **The builder takes no routing probe yet (D3 deferred).** The worktree agent
  has no tmux call site and no route to decide, so the builder returns
  `flowLaunchRoute` without taking a probe. The routing input arrives with the
  first tmux-capable kind. Open question 4 remains open.

`TestEveryFlowLaunchRouteRefusesAnUnverifiedPin` now follows
`newFlowLaunchContext` call sites as well as `applyLaunchStamp` ones, so a
route that delegates construction still has to refuse an unverified pin before
it reserves. The builder itself is exempt: it never reserves or writes, and the
refusal has to happen earlier than it runs.

The ADR's status stays `proposed`; the remaining open questions are unanswered.

## Implementation note — repair (`approach-hyl.3`)

The second slice migrated `RoleRepair`, the kind the migration order nominated
first and the tracer deferred. It is the first migration where the call site was
doing real work after the literal, so it is the first that tests the seam's
central claim.

- **Post-literal mutation was the whole point.** `refreshFlowRepairLaunchContext`
  ran after the composed literal and overwrote seven fields from the reserved
  record, treating the literal's `RepoPath`/`WorktreePath`/`PlanPath` as a
  fallback chain rather than as values. That made the literal's fields mean two
  different things at two different instants, and it is why the call site's own
  comment had to explain the seeding order. Under the builder the read stage's
  resolutions are named for what they are — `FallbackRepoPath`,
  `FallbackWorktreePath`, `PlanID`, `PlanPath` — and the precedence between them
  and the record lives in one function. The refresh is deleted, not shimmed.
- **Stamp-last forced the prompt off `ctx.Executable`.** `flowRepairPrompt` read
  the stamped executable, which only had a value because the old call site
  stamped *before* refreshing. The builder stamps last — registration reads the
  finished context to name the launch — so the prompt renders from
  `settings.Pin.ExecutablePath` instead. It is the same string
  `applyLaunchStamp` writes, and the empty-pin case is what `flowPromptBinary`
  already falls back from. This regression would have been silent (a well-formed
  prompt naming ambient `approach`), so it has its own test.
- **Repair's payload validation is deliberately looser than the worktree
  agent's.** The builder requires a launch ID and a flow ID but *not* a worktree
  path: repair exists for Flows whose recorded directories are gone, so copying
  the tracer's check would have refused exactly the Flows repair is for. The
  no-usable-directory refusal stays in the read stage, where it can name what it
  refused.
- **Resolved agent settings ride on the target, not the snapshot.** Repair's
  command, model and effort come from the obstruction phase's persisted settings,
  and resolving them needs the reserved record. Overwriting the snapshot's three
  fields before calling was rejected: it would make the snapshot mean one thing
  for repair and another for every other kind.
- **D3 is still deferred.** `docs/flow-launch-variant-matrix.md:54` records that
  `repair × tmux` is unreachable, so repair has no route to decide and the
  builder still returns `flowLaunchRoute` without taking a routing probe. Open
  question 4 remains open, now for the second slice running.

The acceptance evidence is that `model/flow_launch_repair_internal_test.go`
passes unedited, as `model/flow_launch_generic_agent_internal_test.go` did for
the tracer, and that the module-interface variant test's whole-struct comparison
now has a repair row pinning the empty `FlowPhaseID`, `FlowLaunchTracked` false,
and the empty `WorkingDir`.

The ADR's status stays `proposed`.
