# Flow launch variant matrix

Repair, autofix, and worktreeAgent are phase-untracked but still claim durable
Flow ownership. Protected preparation writes `FlowRecord.UntrackedOwner` with
the launch ID, role, and launcher PID while holding the launch/close reservation.
Successful embedded install or tmux handoff activates that exact owner with its
direct PID, isolated socket/session, or repo session/window. Startup failures
release it by launch ID. Embedded detach changes presentation only and does not
release a live tmux-backed agent; process exit or termination does. Transport
probe failures remain occupied. The agent wrapper runs the pinned exact-owner
release callback after process exit. A stale result cannot clear a replacement
owner.

Evidence base for `approach-hyl` (typed Flow launch intent). Every cell cites
`file:line` against commit `e1dd62e`, the state of the code before the ADR 0002
migration began; the proposed redesign is ADR 0002. Sections 1-3 are that
frozen survey. What has moved since is recorded in "Migration status" at the end
of section 2, which is the authority for where a kind is constructed and routed
today.

Scope: the eight launch kinds in `model/flow_launch_intent.go:14`, crossed with
route (embedded/tmux), headless, and phase-terminal resume, and every consumer
of the Flow-scoped fields of `actions.AgentLaunchContext`
(`actions/actions.go:1055`).

## 1. Construction sites

There are eight kinds but only seven Flow-scoped construction sites: manual and
automatic phase launches share one builder and differ by `req.AutoLaunch`.
The `autoPhase` kind now carries either ordinary AutoMode policy or the distinct
auto-merge policy; both resolve to the same tracked, headless launch variant.

| Kind | Builder | Route decision |
| --- | --- | --- |
| `manualPhase` | `model/flow_phase_launch.go:371` | `model/flow_phase_launch.go:402` |
| `autoPhase` | `model/flow_phase_launch.go:371` (same literal, `req.AutoLaunch` true) | `model/flow_phase_launch.go:402` |
| `createPhase` | `newFlowLaunchContext` (`model/flow_launch_context.go:619`, `createPhaseTarget`) | none — the builder returns a constant embedded route |
| `phaseResume` | `model/flow_launch_resume.go:360` | `model/flow_launch_resume.go:299` (taken on the Model, before the closure) |
| `repair` | `model/flow_launch_repair.go:420`, refreshed at `model/flow_repair.go:182` | none — hardcoded embedded at `model/flow_launch_repair.go:440` |
| `autofix` | `model/flow_launch_autofix.go:366` | `model/flow_launch_autofix.go:395` |
| `worktreeAgent` | `model/flow_launch_generic_agent.go:292` | none — hardcoded embedded at `model/flow_launch_generic_agent.go:299` |
| `savedSessionResume` | `model/flow_launch_saved_session_resume.go:300` | none — hardcoded embedded at `model/flow_launch_saved_session_resume.go:226` |

Four further `actions.AgentLaunchContext` literals exist outside the Flow
lifecycle and set no Flow marker at all — the non-Flow session resume
(`model/model_keys.go:2773`), plan implementation (`model/model_keys.go:2847`),
open-agent-in-worktree (`model/model_keys.go:3308`), and slice-epic
(`model/ready_bead_slice.go:171`). Two more are probe-only values used purely to
ask a routing question: `model/flow_launch_resume.go:299` and
`model/tmux_mode.go:392`; they too set no marker.

Two literals do set Flow marker fields without being launches at all. Both
carry `FlowID` + `FlowPhaseID` purely to address an existing launch record: the
session-release finalizer (`model/flow_session_release.go:407`) and the
auto-advance failure persister in `blockAutoFlowLaunchPhase`
(`model/flow_launch_lifecycle.go:1051`). Neither is routed, neither builds a
command, and neither has a launch kind. They matter to Phase 4: any enforcement
mechanism must tolerate all of the above, and must distinguish "sets a Flow
marker" from "constructs a Flow launch".

## 2. Reachable variants

17 of the 64 cells in kind × route × headless × phase-terminal are reachable. Pruning rules, each with its gate:

| Pruned | Why unreachable |
| --- | --- |
| any kind × tmux × headless | `tmuxRouteEligible` refuses `ctx.Headless` (`model/tmux_mode.go:64`). The route is decided *after* headless resolution (`model/flow_launch_context.go:286–309`), so this holds for every kind. |
| `autoPhase` × interactive | Both AutoMode read commands hardcode `Headless: true` (`model/flow_launch_lifecycle.go:665`, `:692`), and the tracked-phase builder skips the reservation override when `target.AutoLaunch` (`model/flow_launch_context.go:286`). |
| `autoPhase` × tmux | Follows from the previous two rows: auto ⇒ headless ⇒ embedded. |

Within `autoPhase`, ordinary AutoMode accepts ready non-merge phases after a
completion-edge drain. Auto-merge accepts only ready semantic merge phases
when `FlowRecord.AutoMerge ?? globalAutoMerge` is true. The store repeats that
kind and effective-policy check when it records the launch ID.
| `repair` × tmux | Refused twice: `tmuxRouteEligible` rejects `ctx.FlowRepair` (`model/tmux_mode.go:64`), and prepare has no tmux call site (`model/flow_launch_repair.go:440`). |
| `createPhase` × tmux | The builder returns a constant embedded route (`model/flow_launch_context.go:619`); creation has no tmux call site either. |
| `worktreeAgent` × tmux | No call site (`model/flow_launch_generic_agent.go:299`). |
| `savedSessionResume` × tmux | No call site (`model/flow_launch_saved_session_resume.go:226`). |
| `phaseResume` × headless | `Headless` is never assigned by the resume builder (`model/flow_launch_resume.go:360–383`). |
| `worktreeAgent` × headless | `Headless: false` is hardcoded (`model/flow_launch_generic_agent.go:296`). |
| `savedSessionResume` × headless | `Headless` is never assigned (`model/flow_launch_saved_session_resume.go:300–314`). |
| `FlowPhaseTerminal` = true for any kind but `phaseResume` | Only the resume builder assigns it (`model/flow_launch_resume.go:381`). |

The reachable variants:

| # | Kind | Route | Headless | PhaseTerminal |
| --- | --- | --- | --- | --- |
| V1 | manualPhase | embedded | false | false |
| V2 | manualPhase | embedded | true | false |
| V3 | manualPhase | tmux | false | false |
| V4 | autoPhase | embedded | true | false |
| V5 | createPhase | embedded | false | false |
| V6 | createPhase | embedded | true | false |
| V7 | phaseResume | embedded | false | false |
| V8 | phaseResume | embedded | false | true |
| V9 | phaseResume | tmux | false | false |
| V10 | phaseResume | tmux | false | true |
| V11 | repair | embedded | false | false |
| V12 | repair | embedded | true | false |
| V13 | autofix | embedded | false | false |
| V14 | autofix | embedded | true | false |
| V15 | autofix | tmux | false | false |
| V16 | worktreeAgent | embedded | false | false |
| V17 | savedSessionResume | embedded | false | false |

### Migration status

Every reachable variant is now built by `newFlowLaunchContext`
(`model/flow_launch_context.go`) rather than by a literal at its prepare stage:
V16 (worktreeAgent), V11–V12 (repair), V13–V15 (autofix), V17
(savedSessionResume), V7–V10 (phaseResume), V1–V4 (manual and auto phase) and
V5–V6 (createPhase), in that migration order. The `C:` line numbers in section 3
therefore read as the pre-migration snapshot throughout; section 1's builder
column is the authority for where a kind is constructed today.

Two of those roles decide their own *route*. V13–V15 were the first: the
builder takes the snapshotted backend and tmux probe and returns the route with
the fallback note, so V15's cleared `Embedded` is set where the rule lives
rather than after construction. V7–V10 followed — the probe the call site used
to run on the Model (formerly `model/flow_launch_resume.go:299`) is now the same
decision taken against the finished context, and V9–V10's cleared `Embedded`
moved with it. The remaining builder arms return a constant embedded route,
which keeps the pruning rules at rows `repair × tmux`, `worktreeAgent × tmux`
and `savedSessionResume × tmux` true by construction rather than by a missing
call site. The `phaseResume × headless` and `savedSessionResume × headless`
rules likewise now hold because neither builder arm assigns `Headless`.

V1–V4 were the last route-deciding arm to move. The tracked-phase builder
resolves the reservation-vs-requested headless rule and *then* decides the
route against the finished context, which is what keeps the
`any kind × tmux × headless` pruning rule true by construction rather than by
call-site ordering. `prepare` carries the builder's `flowLaunchRoute` directly
to lifecycle dispatch, whose final route switch rejects unsupported values.

V5–V6 moved last. Their arm returns a constant embedded route like the repair,
worktree-agent and saved-session-resume arms, so the `createPhase × tmux`
pruning rule now holds by construction rather than by a missing call site, and
the create call site takes the builder's route instead of assigning
`flowLaunchRouteEmbedded` itself. Its tracked-ness moved too: `FlowLaunchTracked`
and `Embedded` are now set in the arm rather than stamped at install, so no Flow
context is rewritten between prepare and the terminal open (see F4).

## 3. Field values, with the pipeline point that sets them

`C` = construction, `L` = lifecycle install (`installFlowLaunchEmbedded`),
`T` = terminal open, `R` = tmux route, `X` = actions transport.

### Marker fields

| Field | V1–V3 manual | V4 auto | V5–V6 create | V7–V10 resume | V11–V12 repair | V13–V15 autofix | V16 agent | V17 saved |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `FlowID` | record `C:383` | same | `msg.FlowID` `C:813` | record `C:377` | `msg.FlowID` `C:430` | record `C:383` | record `C:297` | record `C:312` |
| `FlowPhaseID` | phase `C:384` | same | phase `C:813` | `msg.PhaseID` `C:378` | **empty** (deliberate, `model/flow_launch_repair.go:436`) | empty | empty | empty |
| `FlowPhaseKind` | `SemanticKind` `C:385` | same | `SemanticKind` `C:813` | `SemanticKind` `C:379` | empty | empty | empty | empty |
| `FlowLaunchTracked` | true `C:392` | true `C:392` | true `C:656` | true `C:382` | false (never) | false | false | false |
| `FlowAutoLaunch` | false `C:386` | true `C:386` | false | false | false | false | false | false |
| `FlowRepair` | false | false | false | false | true `C:431` | false | false | false |
| `FlowAgent` | false | false | false | false | false | false | true `C:297` | false |
| `FlowSavedSessionResume` | false | false | false | false | false | false | false | true `C:313` |
| `FlowAutofix` | false | false | false | false | false | true `C:387` | false | false |
| `FlowAutofixPRNumber` | 0 | 0 | 0 | 0 | 0 | `record.PR.Number` `C:388` | 0 | 0 |
| `FlowPhaseTerminal` | false | false | false | `PhaseStatusTerminal` `C:380` | false | false | false | false |

### Transport fields

| Field | V1–V3 manual | V4 auto | V5–V6 create | V7–V10 resume | V11–V12 repair | V13–V15 autofix | V16 agent | V17 saved |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `Embedded` | true `C:393`; false on tmux `R:406` | true `C:393` | true `C:656` | true `C:381`; false on tmux `R:388` | true `C:432` | true `C:389`; false on tmux `R:399` | true `C:297` | true `C:314` |
| `Headless` | `req.Headless` `C:394`, replaced by the persisted reservation `C:398` | `req.Headless` (always true) `C:394` | `msg.Record.Headless` `C:814` | unset (false) | unset at C, then `record.Headless` at refresh (`model/flow_repair.go:199`) | `record`/`reserved.Headless` `C:390` | false `C:297` | unset (false) |
| `Command` | `settings.Command` `C:372` | same | `settings.Command` `C:809` | `msg.ResumeCommand` `C:361` | `resolved.Command`, restricted to codex/claude/cursor (`model/flow_launch_repair.go:414`) | `settings.Command` `C:367` | `settings.Command` `C:293` | `string(refreshed.Provider)` `C:301` |
| `ResumeSessionID` | "" | "" | "" | `msg.ProviderSessionID` `C:374` | "" | "" | "" | `refreshed.SessionID` `C:309` |
| `InitialPrompt` | `flowPhasePrompt` `C:387` | same | `initialFlowLaunchPrompt` `C:815` | unset (deliberate; the resume carries no prompt) | set at refresh, `flowRepairPrompt` (`model/flow_repair.go:200`) | `autofixPrompt` `C:391` | "" `C:297` | "" |
| `WorkingDir` | **unset** | unset | **unset** | `msg.Record.WorktreePath` `C:367` | **unset** | `record.WorktreePath` `C:375` | `record.WorktreePath` `C:294` | `refreshed.CWD` ‖ worktree `C:305` |
| `PlanPhaseID` / `Title` / `Status` | unset | unset | `phase.PhaseID` / title / `PhaseRunning` `C:812` | unset | unset | unset | unset | unset |

Every kind additionally passes through `applyLaunchStamp`
(`model/flow_launch_pin.go:85`), which sets `Executable`, `BuildVersion`,
`DBSchemaVersion`, `ControlEndpoint` and `ControlToken`, and through
`registerLaunchControl` (`model/flow_launch_pin.go:102`).

### The four fields that change value mid-flight

1. `FlowLaunchTracked` was stamped at install for every kind except repair,
   autofix, worktreeAgent and savedSessionResume. That stamp is gone: every kind
   that is tracked now says so at construction, `createPhase` included, and
   install no longer writes the field.
2. `Embedded` was forced true at install as well; that write is gone too, and so
   are the two that outlived it in `model/embedded_terminal.go` (the shared open
   at `:493` and the terminal starter at `:214`). The one non-Flow construction
   site, `sessionResumeLaunchContext`, now sets the field in its own literal
   like every Flow builder does, so nothing below the embedded open repairs a
   context after the fact. The remaining true is in the embedded tmux command
   (`actions/actions.go:1351`), an actions-local normalisation on a value copy.
3. `Embedded` is forced false on the tmux route in three places:
   `model/flow_phase_launch.go:406`, `model/flow_launch_resume.go:388` and
   `model/flow_launch_autofix.go:399`, and once more inside actions at
   `actions/tmux_mode.go:313`. Two of those three have since moved inside the
   builder with their roles: the autofix and phase-resume clears now happen in
   the arm that decides the route. The fourth model-side clear, in
   `model/tmux_mode.go`, is gone — the argv-vs-dock rule for that spawn is held
   one layer down by `actions.RepoTmuxAgentLaunch`. The external terminal window
   is the second transport that clears the bit, in
   `actions.agentLaunchWithOptions`, for the same reason: a window has no dock
   to prefill and an alt screen of its own to leave alone.
4. `Headless` is resolved twice for manual launches — from the record at
   `model/flow_launch_lifecycle.go:620` and again from the persisted reservation
   at `model/flow_phase_launch.go:398` — and for repair only at refresh time
   (`model/flow_repair.go:199`), i.e. *after* its literal.

Consequence for the design: **an interface that only owns construction does not
own the values these consumers read.** The builder must own the route decision,
or the late mutations must become an explicit, narrow transition.

## 4. Consumer map

Each entry lists the fields read, the variants separated, and the variants
deliberately treated alike.

| Consumer | Fields read | Separates | Treats alike |
| --- | --- | --- | --- |
| `flowEmbeddedTerminalIdentity` (`model/embedded_terminal.go:736`) | `actions.FlowLaunchRoleOf`, plus `ResumeSessionID`, `FlowAutofixPRNumber`, `Embedded`, `Headless`, `FlowPhaseID`, `FlowID`, `WorktreePath` as payload | V11–12 (repair) ‖ V16 (agent) ‖ V17 (session id) ‖ V13–15 (autofix + PR) ‖ rest | V1–V10 all render as the phase ID |
| `flowEmbeddedTerminalDetachPolicy` (`model/embedded_terminal.go:988`) | `actions.FlowLaunchRoleOf` | V16, V17 (never detachable) ‖ rest | every tracked and untracked-repair/autofix variant |
| slot stamping (`model/embedded_terminal.go:502`) | `RepoPath`, `WorktreePath`, `WorkingDir`, `FlowID`, `FlowPhaseID`, `LaunchID`, `Command`, and `actions.FlowLaunchRoleOf` for the slot's three Flow markers | repair/agent/saved-resume slots | autofix is *not* stamped — it has no slot marker of its own |
| dock visibility (`model/embedded_terminal.go:458`, `:467`) | `FlowAutoLaunch`, `Headless` | V4 (no dock, keeps the active slot) ‖ V2/V6/V12/V14 (headless) ‖ rest | manual vs create vs resume |
| `updateFlowTerminalFocusAfterLaunch` (`model/model_keys.go:3003`) | `actions.FlowLaunchRoleOf`, then `FlowAutoLaunch`, `Headless` | V16/V17 (always focus input) ‖ V4 (no focus change) ‖ headless (focus list) ‖ rest | manual/create/resume/repair/autofix at equal headlessness |
| `reserveFlowSpawn` (`model/model_keys.go:3145`) | `actions.FlowLaunchRole.Tracked` | tracked non-repair (V1–V10) ‖ rest | all untracked kinds are no-ops |
| `flowLaunchFailureUpdate` (`model/model_keys.go:3154`) | `actions.FlowLaunchRole.Tracked`, then `FlowPhaseTerminal`, `FlowPhaseKind` | V8/V10 (terminal resume: refuse) ‖ V11–V17 (no phase: refuse) ‖ plan-review kind (blocked) ‖ rest | manual/auto/create/non-terminal resume |
| `tmuxRouteEligible` (`model/tmux_mode.go:63`) | `Headless`, `actions.FlowLaunchRoleOf`, `Command` | V3/V9/V10/V15 eligible ‖ rest | every embedded variant, for whichever of the two reasons |
| `flowLaunchContextRequiresLifecycle` (`model/tmux_mode.go:329`) | `actions.IsFlowLaunchContext`, which reads all ten marker fields (`FlowID`, `FlowPhaseID`, `FlowPhaseKind`, plus the seven booleans; not `FlowAutofixPRNumber`) | Flow launches ‖ the four non-Flow literals and the two probes | all 17 variants alike; the two non-launch Flow-ID literals would also classify as Flow, but never reach it |
| `actions.ShouldPrefillEmbeddedPrompt` (`actions/actions.go:1441`) | `FlowLaunchRole.Prefills` + `validateFlowLaunchRole`, plus `Command`, `Embedded`, `Headless`, `ResumeSessionID`, `InitialPrompt` as transport and payload | V1/V5 (prefill) ‖ V11/V13/V16 (untracked prefill roles) ‖ resume and headless and tmux (no prefill) | the three untracked roles answer through one `Prefills` case list |
| `actions.resumeSessionIDForContext` (`actions/actions.go:1553`) | `FlowSavedSessionResume` as the claim, `validateFlowLaunchRole(ctx, RoleSavedSessionResume)` for the markers, plus `Embedded`, `Headless`, `InitialPrompt`, `ResumeSessionID` | V17 ‖ everything else | asserts *one* variant; all others take the plain path |
| `actions.validateTrackedRepoTmuxRole` (`actions/tmux_mode.go:464`) | `FlowLaunchRole.Tracked` + `validateFlowLaunchRole`, plus `FlowAutoLaunch`, `Headless`, `FlowAutofixPRNumber` | V3/V9/V10 accepted | manual and resume tmux launches are identical to it |
| tracked lease branch (`actions/tmux_mode.go:341`) | `FlowLaunchTracked`, `SessionStateRoot`, `Executable`, `FlowID`, `FlowPhaseID`, `LaunchID` | V3/V9/V10 (leased window) ‖ V15 (plain window) | — |
| `repoTmuxWindowName` / `repoTmuxLeasedWindowName` (`actions/tmux_mode.go:543`, `:557`) | `FlowPhaseKind`, `Command`, `LaunchID` | phase variants (kind-named) ‖ V15 (command-named) | manual vs resume |
| `launchKind` (`model/flow_launch_pin.go:123`) | `FlowRepair`, `FlowAutofix`, `FlowAgent`, `FlowSavedSessionResume`, `ResumeSessionID` | repair ‖ autofix ‖ generic ‖ resume (V7–V10 **and** V17) ‖ phase | V1–V6 all report "phase" |
| `registerLaunchControl` (`model/flow_launch_pin.go:102`) | `FlowID`, `LaunchID`, `FlowPhaseID` | phase-owning ‖ unowned (V11–V17) | — |
| `reconcileInteractiveLaunchExitCmd` (`model/flow_launch_control.go:149`) | `FlowLaunchTracked`, `FlowID`, `FlowPhaseID`, `LaunchID` | V1–V10 ‖ rest | — |
| `actions.UsesStreamJSONOutput` (`actions/actions.go:1333`) | `Command`, `Embedded`, `Headless` | V2/V4/V6/V12/V14 on claude/cursor | — |
| provider env export (`actions/actions.go:1424`) | `PlanPhaseID`, `PlanPhaseTitle`, `PlanPhaseStatus` | V5/V6 only | every other variant exports empty strings |

All eight model-side consumers above now derive the role from
`actions.FlowLaunchRoleOf` (`actions/flow_launch_role.go`) — the inverse of the
builder's `role()` — rather than re-deriving it from raw marker fields, and the
lifecycle guard delegates to the deliberately wider `actions.IsFlowLaunchContext`.
The fields still listed beside the role are payload and transport, not role:
the PR number and the dock/headless pair that decide whether autofix has a label
to render, the resumed session ID, the phase kind the failure ladder reads, and
the fallback ladder's own strings. Their behavior across V1–V17, the four
non-Flow literals and the two probes is pinned by
`model/flow_launch_role_matrix_internal_test.go`, and every builder arm asserts
`FlowLaunchRoleOf(built) == target.role()` so the classifier stays the builder's
inverse rather than a parallel guess. `FlowLaunchTracked` is decisive in exactly
one place there: a resume carrying a phase but not the marker names no role, so
an explicitly untracked launch cannot be promoted into one that takes the Flow
lease and marks a phase. The other phase-attached shapes keep answering as
their consumers always did, including the untracked failure contexts that still
mark their phase. Two malformed shapes no builder arm emits do answer
differently than the old predicates — a repair context that also sets the agent
or saved-resume marker is detachable, and a phase-attached context without
`FlowLaunchTracked` takes the Flow lease — and `FlowLaunchRoleOf`'s doc comment
names both.

The four `actions`-side consumers read the same role
(`approach-hyl.11`). `FlowLaunchRoleOf` answers *which* role a context names;
`validateFlowLaunchRole` answers whether the context is a well-formed instance
of it, from per-role marker rows transcribed from the "Marker fields" table of
section 3. Between them they replace the four prefill predicates, the resume
ladder's 13 conjuncts and the tmux ladder's 10. What stays beside the role at
each call site is transport and payload the role deliberately does not carry:
the three docked providers, `Embedded`/`Headless`, the prompt and the resumed
session ID for the prefill and the resume, and `FlowAutoLaunch`,
`Headless` and `FlowAutofixPRNumber` for the tmux route (F1's dead clause is
still dead, and still explicit). Both tmux gates keep reading
`FlowLaunchTracked` rather than `role.Tracked()`: the marker is the launch's
*claim* on the Flow lease, and the classifier names a phase-attached context a
phase role even when it declared itself untracked, so gating on the role would
hand such a launch a lease it never asked for. The role decides whether the
claim is well formed. `validateFlowLaunchRole` accepts any `Tracked()` role
there, `RoleCreatePhase` included, because V7–V10 reach the tmux route as
`RolePhaseResume` and naming `RoleTrackedPhase` alone would break them.

Requiring well-formedness rather than the role alone is what keeps the
migration behavior-preserving on the malformed shapes: a repair carrying phase
markers, a phase-attached context that never set `FlowLaunchTracked`, and a
repair that also sets the agent marker each name a role whose marker row they
violate, so none of them prefills — which is what all four hand-written
predicates answered by failing a conjunct. One shape does move: a repair or
worktree agent whose `FlowID` is whitespace no longer prefills, because the role
requires a Flow it can name where the old conjunct was an untrimmed
`FlowID != ""`. No builder arm emits any of these. Their behavior at the seam,
alongside all 17 reachable variants, the four non-Flow literals and the two
probes, is pinned by `actions/flow_launch_role_seam_internal_test.go`.

### Field coverage check

Every field in the matrix has at least one named consumer:

- `FlowID`, `FlowPhaseID`, `FlowLaunchTracked`, `FlowRepair`, `FlowAgent`,
  `FlowSavedSessionResume`, `FlowAutofix`, `FlowAutoLaunch`, `Headless`,
  `Embedded`, `ResumeSessionID`, `InitialPrompt`, `Command`, `LaunchID`:
  multiple consumers each.
- `FlowPhaseKind`: `flowLaunchFailureUpdate`, both tmux window namers,
  `flowLaunchContextRequiresLifecycle`.
- `FlowPhaseTerminal`: `flowLaunchFailureUpdate`, `resumeSessionIDForContext`,
  `flowLaunchContextRequiresLifecycle`.
- `FlowAutofixPRNumber`: `flowEmbeddedTerminalIdentity`,
  `validateTrackedRepoTmuxRole`.
- `WorkingDir`: slot stamping and `detachHandoffCWD`
  (`model/embedded_terminal.go:995`), plus the agent cwd in actions.
- `PlanPhaseID`/`PlanPhaseTitle`/`PlanPhaseStatus`: **export-only** — read
  solely at `actions/actions.go:1424–1426` to populate provider env. No
  predicate branches on them.

## 5. Findings (candidate latent bugs and design constraints)

**F1 — `validateTrackedRepoTmuxRole`'s `FlowAutoLaunch` rejection is currently
dead, and only by an undocumented two-step coupling.** The predicate refuses any
auto launch on the tracked tmux route (`actions/tmux_mode.go:458`). That refusal
never fires today only because AutoMode hardcodes `Headless: true`
(`model/flow_launch_lifecycle.go:665`, `:692`) and `tmuxRouteEligible` refuses
headless (`model/tmux_mode.go:64`). Nothing states "auto ⇒ headless" as an
invariant. Making auto launches interactive — a plausible future change — would
turn every tmux-mode auto-advance into `invalid tracked Flow tmux launch role`.

**F2 — closed.** The model side closed with `approach-hyl.10` and the actions
side with `approach-hyl.11`. Neither the eight model-side consumers nor the four
actions-side ones hand-write the role any more: they read
`actions.FlowLaunchRoleOf`, whose precedence is stated once and tested against
the builder, and `actions.validateFlowLaunchRole`, whose per-role marker rows
are section 3's table made executable. The original finding was that **the three
hand-written role predicates disagreed about which fields constitute a role** —
`resumeSessionIDForContext` checked `FlowPhaseTerminal` and
`InitialPrompt == ""`, `validateTrackedRepoTmuxRole` checked neither but did
check `FlowAutofixPRNumber != 0`, and the autofix arm of
`flowEmbeddedTerminalIdentity` checked neither `FlowPhaseTerminal` nor
`FlowAutoLaunch`. The marker half of that disagreement is now one answer; what
each consumer still asks alone is transport and payload, which is a difference
in what they need rather than a difference about what a role is.

**F3 — `launchKind` classifies V17 and V7–V10 identically as `"resume"`**
(`model/flow_launch_pin.go:132`), because it falls through on `ResumeSessionID`.
A tracked phase resume and an untracked saved-session resume are different
policy classes everywhere else in the matrix; `launch.json` cannot tell them
apart.

**F4 — closed by `approach-hyl.9`.** `createPhase` was the last kind whose
tracked-ness was established outside its builder: `FlowLaunchTracked` and
`Embedded` were left false at construction and stamped at install, because
between the two the create call site can take `failCreateFlowLaunchEmbedded`,
whose `flowLaunchFailureUpdate` reads `FlowLaunchTracked`. That window turned
out to be safe — the flag is consulted only for a resume
(`ctx.ResumeSessionID != "" && !ctx.FlowLaunchTracked`) and a createPhase
context carries no `ResumeSessionID`, so the same phase update is persisted
either way. Both fields now ship from the builder
(`model/flow_launch_context.go:656`) and install stamps nothing; the `PlanPhase*`
trio it also owns (`model/flow_launch_context.go:651`) is still createPhase's
alone.

**F5 — `WorkingDir` is unset for manual, auto, create and repair launches**, so
those four rely on the actions-side fallback chain while resume, autofix,
worktree agent and saved-session resume set it explicitly. The role payload has
to keep this distinction or change behaviour.

**F6 — the lifecycle branches on `attempt.Kind`, not on the context.** The
tracked stamp that used to enumerate four kinds by hand is gone with F4, but the
mutated-phase mark (`model/flow_launch_lifecycle.go:958`) still asks the same
question of `attempt.Kind`. The context cannot answer it, which is the clearest
evidence that a role belongs *on* the context.
