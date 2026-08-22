# ADR 0003: Flow occupancy as a deep module

Status: **accepted** (2026-08-20)

`approach-x0r.1` is a HITL bead. The Flow that wrote this ADR ran headless in
auto mode, so the bead's "grilled with the user before slice 2 starts" criterion
was carried by `approach-x0r.13` and satisfied out of band on 2026-08-20. All
seven decision requests are answered in "Decisions from the grill" below, and
five of them changed the design. Where a decision below contradicts the D-section
above it, the decision wins and the D-section records the original proposal.

`approach-x0r.2` is unblocked.

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
| `StagePreview` | matrix §2.3 launch previews | Cached only. Omits terms added only by the footer. |
| `StageFooter` | matrix §2.3 footer predicates | Runs per frame. Cached only. |
| `StageAdmission` | matrix §2.1, keypress rows | Runs on a keypress. In-process sources plus the lease; no session-store walk. |
| `StageAutoAdvance` | matrix §2.1 "Auto phase", §2.2 "AutoMode read" | The AutoMode advance poll. Never reads S14, refuses in silence, and its read adds S10-minus-the-candidate. |
| `StageAuthoritative` | matrix §2.2 read stages | Runs in a `tea.Cmd`. Full store access. |
| `StageReserved` | matrix §2.2 prepare stages | Runs under the cross-process reservation. Re-checks the lease. |
| `StageInstall` | `flowLaunchEmbeddedBackstop` | Last check before a slot is allocated. Slot sources only. |
| `StageDrain` | matrix §2.4 drain gate and session pre-filter | Runs at 1 Hz. Cached only, and must never shell out. |
| `StageDrainControl` | matrix §2.4 arm/disarm pass | Applies repair-slot and repair-marker state without widening the drain gate. |

The non-launch session-release consumers use `RoleNone`: `StageFooter` for its
cached affordance and `StageSessionRelease` for the authoritative gesture
(matrix §2.5, `model/flow_session_release.go:88,115`).

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
> poll's `StageAutoAdvance`, `StageDrain`, and `StageDrainControl` — never
> consults it.

### D4 — `Verdict` exposes policy results, never sources or presentation text

```
Verdict.Occupied() bool
Verdict.Holder()   Holder
Verdict.PhaseID()  string
Verdict.Err()      error
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

Presentation text stays in `model`, as confirmed by Q3 below. The module owns
which holder wins and returns the phase identity needed by model's copy table.

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
| Each admission | one `Occupancy.Query` call; refuse on `Occupied()`, render model-owned text or stay silent per role |
| `previewFlowLaunch`, `previewRepairLaunch` | one `StagePreview` query, conjoined with the existing launchability half |
| Footer predicates, including phase resume | one `StageFooter` query, preserving footer-only terms |
| `flowAutoAdvanceOccupied` and the drain's session pre-filter | one `StageDrain` query |
| Drain arm/disarm repair checks | one `StageDrainControl` query |
| Session release gesture | one authoritative `StageSessionRelease` query after its `RoleNone` footer check |
| `flowRepairOccupancyRefusal` | deleted; the ladder is the repair rows of the priority and reason tables |
| `flowLaunchEmbeddedBackstop` | one `StageInstall` query per kind; model keeps the per-kind strings |
| `flowLaunchAdmissionOccupied`, `flowLaunchRuntimeOccupied` | deleted once the last caller migrates |
| `flowLaunchPhaseSessionOccupied(Except)`, `phaseHasMatchingLiveSession(Except)` | move into `flowoccupancy` as the phase-session rule; the resume exemption becomes a `Query` field |
| tmux keypress probes | unchanged in placement, but reached through `AgentProbe` so the module can refuse to call them at the wrong stage |

## Consequences

- The four ladders of matrix F2 become one table, and `approach-x0r.11` can
  assert that no new one appears.
- The C3 rule becomes checkable: a `StagePreview` or `StageFooter` query that
  reached `ListFlowSessions` is a bug the module can catch, not a review comment.
- Behavior is preserved exactly, including the per-purpose ordering differences
  D4 names. `approach-x0r.2` pins that with characterization tests *before* any
  caller moves; this ADR is written on the assumption that those tests exist
  first.
- User-facing strings stay in one exhaustive table in `model/`; the module owns
  holder selection and ordering.
- `flowoccupancy` will import `actions`, `flowstore`, and `sessions`, and
  nothing else in this repo. Any future need to import `model` means the
  boundary was drawn wrong.

Migration order lives in the beads (`approach-x0r.3` through `approach-x0r.12`),
not here. An earlier draft of this ADR proposed a competing order sliced by
consumer; the grill cut it in favour of the filed beads, which slice by source
family. See decision G8.

## Decisions from the grill

Answered with the user on 2026-08-20 under `approach-x0r.13`. Numbered Q1-Q7 to
match the original requests, plus G8, which the grill raised and the ADR had not.

### Q7 - package name and location: **confirmed, with a scheduled rename**

`flowoccupancy/` at top level, per D1. The repo splits top level (domain:
`actions`, `flowstore`, `sessions`, `planstore`, `scanner`, `model`, `ui`) from
`internal/` (infrastructure: `flowlease`, `launchcontrol`, `dblease`,
`artifacts`). Occupancy is domain policy, so it goes at top level. A
`flowstore/` subpackage was rejected because half the module's inputs are
in-process TUI runtime state that `flowstore` has no access to.

The name is provisional. See G1.

### G1 - scope: this ADR is phase one of a wider module

The epic's stated outcome is a module that owns occupancy **and manages
reservation, adoption, rejection, handoff, failure retention, and release**.
This ADR designs a read-only oracle and D5 pushes handoff out to `model`.
`approach-x0r.9` as filed contradicts that, making handoff and failure retention
module-owned and fencing release behind the owning token.

Ruled: staged. The read-only query is the right first build, because reservation
cannot move safely until the query is characterized and green, and `x0r.9`
already sits behind `x0r.8` in the dependency chain. `x0r.9` is the explicit
widening. The package is renamed at `x0r.9` to match its enlarged scope rather
than guessing a destination name now.

### Q1 - `Purpose` as a `(Role, Stage)` pair: **confirmed, with a `Valid()` registry**

The pair stands. The axes are sparse and irregular rather than orthogonal:
`StageSessionRelease` admits only `RoleNone`; `StageAutoAdvance`, `StageDrain`,
and `StageDrainControl` admit only `RoleTrackedPhase`. That is a real argument
for a flat enum and the ADR did not make it against itself.

The pair wins anyway, on a stronger ground than D2 gives. The rule most worth
making machine-checkable is C3, "no cached rendering query ever reaches
`ListFlowSessions`". That is expressible only if `Stage` is a value the module
can quantify over. Flatten it and C3 goes back to being a review comment, which
is what `approach-x0r.11` exists to end.

Phantom combinations are closed by the `Purpose.Valid()` table rather than by the
type system. That table becomes the single registry of real consumer policies,
each `(Role, Stage)` annotated with its matrix section 2 citation and carrying
the source set that purpose may read. An invalid purpose yields a
fail-closed occupied verdict with `Err`, never a panic: a TUI must not crash on a
programming error. `approach-x0r.11` asserts the registry both ways, so a
consumer without a purpose and a purpose without a caller are both build
failures.

### Q3 - the strings do **not** move into `flowoccupancy`

Rejected, but not for the reason the ADR anticipated. The ADR framed the
alternative as "each caller keeps its own switch, which preserves four
vocabularies". That is a false choice. The four vocabularies collapse because
there is one table keyed by `(Role, Holder)`, and which package holds that table
is a separate question.

Split by concern:

- The module owns policy. Which holder wins, and in what order, per role. That is
  `Holder()` and `PhaseID()` on the `Verdict`, and it carries all of the
  behavior risk.
- `model` owns the copy. One table, `(Role, Holder) -> string`, replacing four
  ladders in four files.

`Verdict.Reason()` comes off the interface.

`Stage` drops out of the text function entirely, which D4's `(Purpose, Holder)`
keying missed: previews gray the footer rather than rendering a reason, and the
drain refuses in silence, so only admission-class stages ever render text.

Two facts drove this. The move is larger than the ADR's "roughly a dozen": about
18 occupancy constants would move and about 20 launchability constants
(`NoWorktree`, `NoPlanPath`, `NotRepairable`, `Drift`, `Canceled`, `Stale`,
`Changed`, `NoProvider`, `EndedSession`, `Resettable`) would stay, splitting
sibling constants that today sit in one block per file. And no domain package in
this repo carries user-facing copy: `actions`, `flowstore` and `sessions` contain
zero status strings. Making `flowoccupancy` the first would be a precedent break,
and it matters more once G1 grows the module into a reservation fence. A fence
that owns PTY reservation and button copy is a worse module than one that owns
only the fence.

Cost accepted: the module cannot guarantee every `Holder` has copy. An
exhaustiveness test over `Holder x Role` against the `Valid()` registry covers
it, and that test is the same either way.

### Q2 - priority is preserved, as a flat per-`Role` table with no default

Behavior is preserved. Unification is rejected.

Q3 shrank this question to "which `Holder` does the module report when several
hold", and repair and autofix genuinely disagree on that. With a manual or auto
phase attempt and a Flow terminal both holding, repair's rank 3 reports the
attempt while autofix falls through to the terminal. Both can hold at once via
the prefill-failure re-reservation (`model/flow_launch_attempt.go:64-68`), so the
divergence is observable.

Two corrections to D4's framing:

Section 4.2 is not a priority override. The install backstop's kinds read
different **sources**: `savedSessionResume` reads S6 only, `phaseResume` and
`autofix` read S7 only, and `repair`, `createPhase` and `worktreeAgent` read
S6 or S7. That belongs in the `(Role, Stage) -> source set` half of the `Valid()`
registry, not the priority table. Counting it as an override inflated the
exception count and filed the data in the wrong place.

"Three overrides on day one" is the wrong model. An override implies a default
that is usually right, which invites "when do we delete them", which is how four
ladders drifted apart in the first place. There is no default and no exception:
one flat table, one ordered `[]Holder` row per `Role`, seven rows, repair's and
autofix's sitting side by side where a diff makes the disagreement obvious. That
is the whole fix for F2. Not "they now agree", but "they now disagree visibly, in
one place, and `x0r.2` pins it".

If repair and autofix should later agree, deleting a row against a green
characterization suite is a two-line diff. Spending this epic's one
behavior-change budget on the least interesting difference on the board is not
worth it.

### Q5 - `AgentProbe` stays inside the module, with a corrected shape

Confirmed, but the drafted interface was wrong in two ways and both were behavior
changes.

There are two probes, deliberately different. `tmuxFlowAgentStillRunning`
(`model/tmux_mode.go:428`) unions phase launch IDs with the autofix registry and
serves repair. `tmuxAutofixAgentStillRunning` (`:446`) is the registry half alone
and serves the `g` keypress, the resume keypress, and autofix. The narrowing is
load-bearing: widening the keystroke probes to every phase of the record "would
newly refuse `g` for a finished agent whose window the user merely left open"
(`:435-438`). A single `TmuxAgentRunning(flowID) bool` collapses both and
silently makes `g` stricter.

Both probes also need `record` and `fallbackRepoPath` to locate the worktree via
`tmuxProbeRepoPath`. A `flowID`-only signature cannot answer either without
re-reading the store, which at `StageAdmission` is the walk that stage forbids.

Corrected:

```go
type AgentProbe interface {
    FlowAgentRunning(record flowstore.FlowRecord, fallbackRepoPath string) bool
    AutofixAgentRunning(record flowstore.FlowRecord, fallbackRepoPath string) bool
}
```

`fallbackRepoPath` moves onto the `Query`. The `(Role, Stage)` registry decides
which method a purpose may call: repair gets the union, `g`, resume and autofix
get the registry half, every other purpose gets neither.

The "surprising for a query to fork a subprocess" objection is mostly answered by
the existing short-circuit. `tmuxAutofixAgentStillRunning` returns false without
forking unless this process actually launched an autofix agent into that Flow, so
the subprocess is a property of the Flow's registry entry, not of the query. And
F4 is the epic's thesis: "a poll on a timer must not shell out" is a comment in
`tmux_mode.go` that nothing enforces. Leaving the probe in `model` leaves it a
comment forever.

### Q4 - S8 and S9 stay hidden; F6 is frozen and its wording corrected

Confirmed. No bug filed. F6 as written conflates two phenomena with very
different lifetimes:

- Auto-closing slots (phase, repair, createPhase, resume). The sweep runs on
  `embeddedTerminalTickMsg` (`model/model.go:2104-2105`) and
  `embeddedTerminalRepaintInterval = time.Second / 30`
  (`model/embedded_terminal.go:293`). The window is 33ms, one frame. A fix would
  mean sweeping synchronously on exit, which carries more risk than the window it
  closes.
- `FlowAgent` and `FlowSavedSessionResume` slots. The sweep skips them
  (`:1051`, `:1067`), so an exited terminal is retained until the user dismisses
  it. That is not a race window. It is the `embeddedTerminalDetachNever` policy
  matrix section 2.5 records, which exists precisely to preserve occupancy.

S9 stays hidden because exposing it would let a caller treat an exited
worktree-agent slot as free, which is the regression detach-never prevents. S8
stays hidden because the prefill-failure re-reservation depends on the slot
reading occupied while it is being dismissed. A `Holder` that can say
`HolderFlowTerminal` but not "whose process exited" is the right amount of
information.

The matrix stays pinned to `3d147d4`, but F6 carries a correction note, so
`x0r.9` does not inherit "occupancy outlives the agent" as an open concern when
only the deliberate half is real.

### Q6 - F5 is frozen in this epic **and** filed

The ADR offered "confirm the freeze" and "file it" as alternatives. They are not.

Frozen here because the fix is new machinery rather than a migration, and this
epic preserves behavior.

A cheap fix was investigated and rejected. `StageDrain` cannot run the window
probe, but `flowAutofixTmuxLaunchIDs` is a map lookup with no I/O, so a
registry-only check looked free and fail-closed. It is not viable: registry
entries are never removed. `model/model.go:236-244` states "It needs no expiry:
the probe asks whether those windows are still live, so closed ones re-enable the
shortcut on their own." A registry-only check would permanently block AutoMode on
any Flow that ran a single autofix anywhere in the session, which is worse than
the gap.

Filed anyway, outside the epic and not blocking it, because after this epic the
module is the right home for the fix: it owns the probe (Q5) and it owns
`StageDrain`'s source set. The bead carries F5 plus the registry-expiry finding
so the next person does not re-derive it.

### G8 - the filed beads own the migration order; the ADR's list is cut

The filed beads slice by source family: each adds one source to the module, then
migrates the consumers that source unblocks. The ADR's draft order sliced by
consumer: build the module whole in `x0r.3`, then move call sites cheapest first.
Same IDs, different contents, which would have handed whoever picked up `x0r.4`
two contradictory briefs.

The beads win. A slice that adds one source and migrates only its readers has a
small checkable blast radius that `x0r.2`'s characterization tests bracket
exactly. Building all sixteen sources at once against no callers puts the first
real proof that any of it is right in `x0r.4`, which is a large unverified step
in the middle of the epic.

The filed order also already agrees with the decisions above. `x0r.5`
("named-holder occupancy verdicts") is where Q2's priority table lands, and
`x0r.6` is where Q3's `(Role, Holder)` copy table gets its first row. The ADR put
the drain first because "no strings move yet"; after Q3 no strings move in any
slice, so that argument is gone.

`approach-x0r.3` additionally carries the `Valid()` registry from Q1.
