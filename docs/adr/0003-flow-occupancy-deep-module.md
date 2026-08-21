# ADR 0003: Flow occupancy as a deep module

Status: **proposed — pending user approval** (2026-08-21)

`approach-x0r.1` is a HITL bead. This Flow ran headless in auto mode, so the
bead's "grilled with the user before slice 2 starts" criterion cannot be
satisfied from inside it. Every decision below is a *proposal* with its
evidence; the "Decision requests" section lists the open questions. Approval
happens out of band, and `approach-x0r.2` must not start before it does.

## Context

"Is something already working in this Flow?" is answered sixteen different ways
in `model/`, by thirteen distinct representations and two composites over them,
read at roughly forty call sites. The evidence base is
`docs/flow-occupancy-matrix.md`, pinned to commit `3d147d4`: sixteen sources
(§1), consumers grouped by mutating/preview/poll/non-launch (§2), ten
deliberate divergences (§3), and four independent ordered refusal ladders (§4).

Four facts from that matrix drive this design.

**C1 — consumers disagree on purpose, not on truth.** The one composite that
exists, `flowLaunchAdmissionOccupied` (`model/flow_launch_lifecycle.go:404`),
has six call sites that take it as-is and five admissions that deliberately
unroll it. Autofix refuses it because it "would collapse a held or unreadable
lease into the generic in-flight status"
(`model/flow_launch_autofix.go:152-154`); phase resume refuses it because it
"could turn a peer race into a silent refusal before the reservation-protected
recheck reports the real cause" (`model/flow_launch_resume.go:129-131`); create
uses the narrower runtime check because a brand-new Flow "remains embedded and
unleased" (`model/flow_launch_lifecycle.go:414-416`). None of these is wrong. A
single boolean cannot serve them, because they are not asking the same question.

**C2 — three consumers need an ordered named holder, not a boolean.**
`flowRepairOccupancyRefusal` (`model/flow_launch_repair.go:201`) ranks five
holders; `flowLaunchEmbeddedBackstop` (`model/flow_launch_lifecycle.go:1269`)
ranks per kind with seven distinct strings; autofix inlines a fourth ladder
(`model/flow_launch_autofix.go:155-168`); the worktree agent a fifth
(`model/flow_launch_generic_agent.go:100-119`). They overlap, they use four
vocabularies, and nothing keeps them consistent (matrix F2).

**C3 — the cached/authoritative split is real but is enforced only by comments.**
The AutoMode drain answers from mirrors because otherwise it "would re-admit and
call `ListFlowSessions` again at 1 Hz" (`model/flow_phase_launch.go:765-772`);
the tmux probe never runs from a poll because "a poll on a timer must not shell
out" (`model/tmux_mode.go:425-427`); the session-release footer is mirror-only
so that a store-only stall stays reachable
(`model/flow_session_release.go:80-82`). Nothing structural stops a new preview
from walking the session store per frame (matrix F4).

**C4 — two source pairs are open-coded everywhere and neither nests.** The Flow
terminal slot and the repair terminal slot "overlap rather than nest — one
requires a live terminal, the other a repair slot"
(`model/flow_launch_lifecycle.go:1261`), and that fact is re-derived at eleven
sites (matrix F3). The same is true of the phase's mirrored sessions versus the
store listing, unioned by `flowLaunchPhaseSessionOccupied`
(`model/flow_launch_lifecycle.go:1488`) and re-scoped four different ways by its
callers (matrix D5).

## Decision

### D1 — a new top-level package `flowoccupancy/`

`flowoccupancy` is a peer of `flowstore/`, `sessions/`, and `actions/`. It is
imported by `model`; it must never import `model`.

Top-level rather than `internal/`, because `internal/` in this repo holds
cross-process and infrastructure concerns — `internal/flowlease`,
`internal/launchcontrol`, `internal/dblease`, `internal/artifacts` — while this
is *domain policy* over `flowstore` records, `sessions` records, and injected
in-process runtime state. It reads the lease through a seam; it does not
reimplement it. Being a real package with a real import edge is also what makes
`approach-x0r.11`'s architecture test expressible: "no file under `model/`
open-codes an occupancy predicate" is checkable only if there is somewhere else
for the predicate to live.

The dependency edge is `model → flowoccupancy → {actions, flowstore, sessions}`.
No cycle: `actions` does not import `model`
(`actions/flow_launch_role.go:10-12`).

### D2 — `Purpose` is a `(Role, Stage)` pair, not a flat enum

`Role` is `actions.FlowLaunchRole` (`actions/flow_launch_role.go:13`), reused
verbatim from ADR 0002. It already names exactly the seven launches the TUI can
start, and every occupancy consumer in matrix §2.1–§2.2 belongs to exactly one
of them.

`Stage` is a new closed enum, one value per consumer class in matrix §2:

| Stage | Consumers | Constraint |
| --- | --- | --- |
| `StagePreview` | matrix §2.3 | Runs per frame. Cached only. |
| `StageAdmission` | matrix §2.1, keypress rows | Runs on a keypress. In-process sources plus the lease; no session-store walk. |
| `StageAutoAdvance` | matrix §2.1 "Auto phase", §2.2 "AutoMode read" | The AutoMode advance poll. Never reads S14, refuses in silence, and its read adds S10-minus-the-candidate. |
| `StageAuthoritative` | matrix §2.2 read stages | Runs in a `tea.Cmd`. Full store access. |
| `StageReserved` | matrix §2.2 prepare stages | Runs under the cross-process reservation. Re-checks the lease. |
| `StageInstall` | `flowLaunchEmbeddedBackstop` | Last check before a slot is allocated. Slot sources only. |
| `StageDrain` | matrix §2.4 | Runs at 1 Hz. Cached only, and must never shell out. |

One non-launch purpose does not fit the role axis and gets `RoleNone` with its
own stage: `StageSessionRelease` (matrix §2.5,
`model/flow_session_release.go:115`).

The quit deferral (`model/embedded_terminal.go:920,942`) is deliberately *not*
a purpose, even though matrix §2.5 lists it. It reads S5 alone, and D5 keeps S5
out of `Sources` because handoff-pending is a process-wide quit policy rather
than a per-Flow question — nothing in `Sources` could answer it, and a
`Query` that ignored it or deferred quit on any attempt would change today's
behavior.

Why a pair and not a flat sixteen-value enum: the flat form makes the source set
per consumer unrelatable, so nothing catches "repair's preview and repair's
admission disagree about the lease". As a pair, the source set is a function of
`Stage` and the *text* is a function of `(Role, Stage)`, which is exactly the
shape matrix §3 describes. See decision request Q1.

### D3 — `Freshness` is explicit, with a `Stage`-derived default

```
Query{FlowID string, Role actions.FlowLaunchRole, Stage Stage, Freshness Freshness}
```

`Freshness` is `FreshnessDefault | FreshnessCached | FreshnessAuthoritative`.
`FreshnessDefault` resolves from `Stage` per the table in D2 and is what almost
every caller passes.

It is not simply *implied* by `Stage`, because one real pair disagrees:
`StageAutoAdvance`'s admission gate (`model/flow_phase_launch.go:773`) and its
read (`model/flow_launch_lifecycle.go:705`) ask the same role the same question
and must get answers of different freshness — the first from the poll's own
record, the second from `ListFlowSessions`. Making it a field keeps that visible
instead of encoding it as two stages that differ in nothing else.

`StageAutoAdvance` itself is a separate stage on the opposite grounds. The
AutoMode poll is not `StageAdmission` at a different freshness: matrix §2.1
records that manual admission reads S14 and refuses loudly where auto reads
neither, and §2.2 records that the AutoMode read adds
`flowRecordHasOtherRunningPhase` where the tracked-phase read does not. Since
`RoleTrackedPhase` covers both consumers — ADR 0002 deliberately kept
auto-launch out of the role axis — collapsing them into `StageAdmission` would
leave `Purpose` unable to name two consumers whose source sets differ, and any
implementation would have to change AutoMode's behavior or drop manual's
headless-write refusal.

The rule, stated once so it stops living in comments:

> **Cached mirrors answer questions that only render. Authoritative sources
> answer questions that mutate or reserve.**
>
> Cached: the display mirror (`m.sessions.Items()`,
> `m.worktreeSessions.Items()`), the poll's own `FlowRecord.Phases`, and every
> in-process runtime source. Authoritative: `ReadFlow`, `ListFlowSessions`, and
> a live lease inspect.
>
> In-process runtime sources (the attempt map, the terminal slots, the pending
> headless write, the repair drain marker) are *always* authoritative — they are
> the state, not a mirror of it — and are therefore read at every freshness.
>
> Lease-read failure is occupancy under every purpose, cached and authoritative
> alike. This is fail-closed in both directions and matches
> `model/flow_launch_lifecycle.go:410` today.
>
> The tmux window probe is read only at `StageAdmission` and
> `StageAuthoritative`, because it forks a subprocess
> (`model/tmux_mode.go:425-427`). Every other stage — including the AutoMode
> poll's `StageAutoAdvance` and `StageDrain` — never consults it.

### D4 — `Verdict` exposes a named `Holder` and a `Reason`, never the sources

```
Verdict.Occupied() bool
Verdict.Holder()   Holder
Verdict.Reason()   string
Verdict.PhaseID()  string
```

`Holder` is a closed enum over matrix §1, collapsed to what consumers actually
name: `HolderNone`, `HolderLeaseUnreadable`, `HolderPeerLease`,
`HolderRepairAttempt`, `HolderPhaseResumeAttempt`, `HolderPhaseAttempt`,
`HolderOtherAttempt`, `HolderTmuxAgent`, `HolderRepairTerminal`,
`HolderFlowTerminal`, `HolderRunningPhase`, `HolderPhaseSession`,
`HolderFlowSession`, `HolderRepairDrain`, `HolderHeadlessWrite`.

`PhaseID` is on the verdict because two existing refusals name a phase and would
otherwise have to reach back into the record:
`genericFlowRuntimeOccupancyReason` ("a running phase %s already occupies this
Flow", `model/flow_launch_generic_agent.go:154-163`) and
`flowRepairLiveSessionStatus`, whose phase argument comes from
`flowRepairPhaseSessionOccupied` returning the matching phase rather than a bool
"because the refusal names its ID" (`model/flow_launch_repair.go:315-318`).

**Priority is a module-owned table keyed by `Purpose`, not one global order.**
A single global order would be a behavior change, and the matrix says where:
repair ranks a competing attempt *with* the terminals under one shared string
(§4.1 rank 4), while autofix ranks the terminals *above* a competing attempt and
gives them different strings (§4.3). Both orders are observable, because the
attempt map and a terminal slot can hold simultaneously — the prefill-failure
path "re-reserves while the slot it is about to dismiss is still installed"
(`model/flow_launch_attempt.go:64-68`). So: one default order, durable before
transient, with per-purpose overrides for repair, autofix, and the install
backstop. Three overrides in one table beats four ladders in four files.

`Reason()` is likewise a table keyed by `(Purpose, Holder)`. The same holder
gets different text per role by design — `flowRepairTerminalStatus`,
`flowAutofixTerminalStatus`, `flowWorktreeAgentSlotStatus`, and
`flowPhaseResumeTerminalStatus` are four strings for one slot — so the strings
move into `flowoccupancy` with the table. Callers that refuse in silence (auto
phase, create, phase resume: matrix D3) simply do not read `Reason()`; loudness
stays the caller's decision, because it is a presentation concern and the poll's
1 Hz repaint is the reason for it.

### D5 — six adapter interfaces, one per source family

Minimal by construction: a method exists only where matrix §2 shows a consumer.

| Adapter | Method | Sources |
| --- | --- | --- |
| `FlowReader` | `ReadFlow(flowID) (flowstore.FlowRecord, error)` | S10, S11 authoritative |
| `FlowCache` | `CachedFlow(flowID) (flowstore.FlowRecord, bool)` | S10, S11 cached |
| `SessionStore` | `ListFlowSessions(flowID) ([]sessions.SessionRecord, error)` | S13 |
| `SessionCache` | `ActiveFlowSessions(flowID) []sessions.SessionRecord` | S12 |
| `LeaseInspector` | `FlowLeaseOccupied(flowID) (bool, error)` | S1, S2 |
| `Runtime` | `AttemptHolder(flowID) (actions.FlowLaunchRole, bool)`, `HasFlowTerminal(flowID) bool`, `HasRepairTerminal(flowID) bool`, `HeadlessWritePending(flowID) bool`, `RepairDrainPending(flowID) bool` | S3, S4, S6, S7, S14, S16 |
| `AgentProbe` | `TmuxAgentRunning(flowID) bool` | S15 |

`AgentProbe` is nil-able and is consulted only at `StageAdmission` and
`StageAuthoritative`, which is how "admission must not shell out" becomes a
property of the module rather than of every caller's comment. `Runtime` is one
interface rather than five because the `Model` implements all of it and
splitting it would produce five one-method adapters over the same receiver.

Three sources are deliberately *not* adapters. S5 (handoff-pending) is a
process-wide quit policy, not a per-Flow question, and stays in `model`. S8
(prefill-pending) and S9 (exited-but-retained) are internal to how `Runtime`
answers `HasFlowTerminal`; exposing them would export the representation D4
exists to hide. See decision request Q4.

### D6 — post-migration form of every consumer

| Consumer | After |
| --- | --- |
| Each admission | one `Occupancy.Query` call; refuse on `Occupied()`, render `Reason()` or stay silent per role |
| `previewFlowLaunch`, `previewRepairLaunch`, `previewPhaseResume`, the three footer predicates | one `StagePreview` query, conjoined with the existing launchability half, which is unchanged |
| `flowAutoAdvanceOccupied` and the drain's session pre-filter | one `StageDrain` query |
| `flowRepairOccupancyRefusal` | deleted; the ladder is the repair rows of the priority and reason tables |
| `flowLaunchEmbeddedBackstop` | one `StageInstall` query per kind; the per-kind strings move into the reason table |
| `flowLaunchAdmissionOccupied`, `flowLaunchRuntimeOccupied` | deleted once the last caller migrates |
| `flowLaunchPhaseSessionOccupied(Except)`, `phaseHasMatchingLiveSession(Except)` | move into `flowoccupancy` as the phase-session rule; the resume exemption becomes a `Query` field |
| tmux keypress probes | unchanged in placement, but reached through `AgentProbe` so the module can refuse to call them at the wrong stage |

## Consequences

- The four ladders of matrix F2 become one table, and `approach-x0r.11` can
  assert that no new one appears.
- The C3 rule becomes checkable: a `StagePreview` query that reached
  `ListFlowSessions` is a bug the module can catch, not a review comment.
- Behavior is preserved exactly, including the per-purpose ordering differences
  D4 names. `approach-x0r.2` pins that with characterization tests *before* any
  caller moves; this ADR is written on the assumption that those tests exist
  first.
- The user-facing strings move out of `model/`. That is a large, mechanical, and
  reviewable diff, and it is the point: today's four vocabularies for one holder
  are only visible once they sit in one table.
- `flowoccupancy` will import `actions`, `flowstore`, and `sessions`, and
  nothing else in this repo. Any future need to import `model` means the
  boundary was drawn wrong.

Proposed migration order for the rest of the epic, cheapest and most-isolated
first, each slice ending green:

1. `x0r.2` — characterization tests against the current predicates.
2. `x0r.3` — implement `flowoccupancy` against those tests, still with no callers.
3. `x0r.4` — the AutoMode drain (`StageDrain`): the smallest source set, and the
   one consumer whose refusals are all silent, so no strings move yet.
4. `x0r.5` — the previews and footer predicates (`StagePreview`).
5. `x0r.6` — repair (admission + the ladder), which is where the priority table
   earns its overrides.
6. `x0r.7` — autofix, the second override.
7. `x0r.8` — the worktree agent and saved-session resume, the two roles that
   read the session mirror and the whole-Flow session listing.
8. `x0r.9` — manual, auto, create, and phase-resume admission; delete both
   composites at the end of it.
9. `x0r.10` — the install backstop (`StageInstall`) and session release.
10. `x0r.11` — the architecture test. `x0r.12` — ownership docs.

## Decision requests

These are the grill questions. They are recorded, not resolved.

1. **Q1 — `Purpose` as a `(Role, Stage)` pair vs. a flat enum.** The pair reuses
   ADR 0002's role and makes the source set a function of `Stage` alone. The
   flat form is simpler to read at a call site and makes illegal combinations
   unrepresentable — there is no `(RoleAutofix, StageDrain)` consumer, and the
   pair admits one. Confirm the pair, or accept a flat enum plus a
   `Role()`/`Stage()` accessor pair.
2. **Q2 — per-purpose priority overrides vs. one global order.** Preserving
   repair's and autofix's differing ranks preserves behavior exactly, at the
   cost of the module having three exceptions on day one. Unifying them is a
   deliberate, small, user-visible behavior change (which of two true refusals
   is shown when an attempt and a terminal both hold). Confirm preservation, or
   authorize the unification as part of `approach-x0r.6`.
3. **Q3 — the strings move into `flowoccupancy`.** Roughly a dozen
   user-facing status constants leave `model/`. The alternative is that the
   module returns a `Holder` and each caller keeps its own switch, which
   preserves four vocabularies and gives up most of D4's benefit. Confirm the
   move.
4. **Q4 — S8/S9 stay hidden.** A slot that is prefill-pending, or whose process
   has exited but has not been swept, still reads as occupied (matrix F6). That
   is today's behavior and this ADR preserves it. Confirm, or file the
   exited-slot window as a separate bug for the epic to fix rather than freeze.
5. **Q5 — `AgentProbe` inside the module.** Putting the tmux probe behind an
   adapter lets the module enforce "never at `StageDrain`", but it also means a
   query can fork a subprocess, which is a surprising property for a package
   named `flowoccupancy`. Confirm, or keep the probe entirely in `model` at the
   keystrokes and accept that the stage rule stays a comment.
6. **Q6 — F5 is a pre-existing gap, not this epic's to close.** An AutoMode
   launch can be admitted into a worktree a tmux autofix agent still owns,
   because the registry is in-process and the drain must not shell out. Confirm
   that `approach-x0r` freezes this rather than fixing it, or file it.
7. **Q7 — package name and location.** `flowoccupancy` at top level, per D1.
   Confirm, or place it under `flowstore/` as a subpackage, which would put the
   policy next to the records it reads at the cost of implying the Flow store
   owns runtime state it has no access to.
