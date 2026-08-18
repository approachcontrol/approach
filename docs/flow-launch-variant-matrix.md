# Flow launch variant matrix

Evidence base for `approach-hyl` (typed Flow launch intent). Every cell cites
`file:line` against commit `e1dd62e`. This document describes the code as it is
today; the proposed redesign is ADR 0002.

Scope: the eight launch kinds in `model/flow_launch_intent.go:14`, crossed with
route (embedded/tmux), headless, and phase-terminal resume, and every consumer
of the Flow-scoped fields of `actions.AgentLaunchContext`
(`actions/actions.go:1055`).

## 1. Construction sites

There are eight kinds but only seven Flow-scoped construction sites: manual and
automatic phase launches share one builder and differ only by `req.AutoLaunch`.

| Kind | Builder | Route decision |
| --- | --- | --- |
| `manualPhase` | `model/flow_phase_launch.go:371` | `model/flow_phase_launch.go:402` |
| `autoPhase` | `model/flow_phase_launch.go:371` (same literal, `req.AutoLaunch` true) | `model/flow_phase_launch.go:402` |
| `createPhase` | `model/flow_launch_create.go:808` (`createFlowLaunchContext`) | none — hardcoded embedded at `model/flow_launch_create.go:373` |
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
| any kind × tmux × headless | `tmuxRouteEligible` refuses `ctx.Headless` (`model/tmux_mode.go:64`). The route is decided *after* headless resolution (`model/flow_phase_launch.go:396–402`), so this holds for every kind. |
| `autoPhase` × interactive | Both AutoMode read commands hardcode `Headless: true` (`model/flow_launch_lifecycle.go:665`, `:692`), and prepare's reservation override is skipped when `req.AutoLaunch` (`model/flow_phase_launch.go:396`). |
| `autoPhase` × tmux | Follows from the previous two rows: auto ⇒ headless ⇒ embedded. |
| `repair` × tmux | Refused twice: `tmuxRouteEligible` rejects `ctx.FlowRepair` (`model/tmux_mode.go:64`), and prepare has no tmux call site (`model/flow_launch_repair.go:440`). |
| `createPhase` × tmux | No call site; route is hardcoded embedded (`model/flow_launch_create.go:373`). |
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

## 3. Field values, with the pipeline point that sets them

`C` = construction, `L` = lifecycle install (`installFlowLaunchEmbedded`),
`T` = terminal open, `R` = tmux route, `X` = actions transport.

### Marker fields

| Field | V1–V3 manual | V4 auto | V5–V6 create | V7–V10 resume | V11–V12 repair | V13–V15 autofix | V16 agent | V17 saved |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `FlowID` | record `C:383` | same | `msg.FlowID` `C:813` | record `C:377` | `msg.FlowID` `C:430` | record `C:383` | record `C:297` | record `C:312` |
| `FlowPhaseID` | phase `C:384` | same | phase `C:813` | `msg.PhaseID` `C:378` | **empty** (deliberate, `model/flow_launch_repair.go:436`) | empty | empty | empty |
| `FlowPhaseKind` | `SemanticKind` `C:385` | same | `SemanticKind` `C:813` | `SemanticKind` `C:379` | empty | empty | empty | empty |
| `FlowLaunchTracked` | true `C:392` | true `C:392` | **false at C; true at `L:1105`** | true `C:382` | false (never) | false | false | false |
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
| `Embedded` | true `C:393`; false on tmux `R:406` | true `C:393` | **unset at C; true at `L:1097`, again `T:479`** | true `C:381`; false on tmux `R:388` | true `C:432` | true `C:389`; false on tmux `R:399` | true `C:297` | true `C:314` |
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

1. `FlowLaunchTracked` is stamped at `model/flow_launch_lifecycle.go:1105` for
   every kind except repair, autofix, worktreeAgent and savedSessionResume. For
   manual, auto and resume that stamp is redundant — they already set it at
   construction. **`createPhase` is the only kind for which it is load-bearing**,
   and only on the embedded route, which is the only route it has.
2. `Embedded` is forced true at `model/flow_launch_lifecycle.go:1097` and again
   at `model/embedded_terminal.go:479`, and a third time inside the terminal
   starter (`model/embedded_terminal.go:200`) and the embedded tmux command
   (`actions/actions.go:1248`).
3. `Embedded` is forced false on the tmux route in four places:
   `model/flow_phase_launch.go:406`, `model/flow_launch_resume.go:388`,
   `model/flow_launch_autofix.go:399`, `model/tmux_mode.go:132`, and once more
   inside actions at `actions/tmux_mode.go:313`.
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
| `flowEmbeddedTerminalIdentity` (`model/embedded_terminal.go:736`) | `FlowRepair`, `FlowAgent`, `FlowSavedSessionResume`, `ResumeSessionID`, `FlowAutofix`, `FlowAutofixPRNumber`, `Embedded`, `Headless`, `FlowID`, `FlowPhaseID`, `FlowLaunchTracked`, `WorktreePath` | V11–12 (repair) ‖ V16 (agent) ‖ V17 (session id) ‖ V13–15 (autofix + PR) ‖ rest | V1–V10 all render as the phase ID |
| `flowEmbeddedTerminalDetachPolicy` (`model/embedded_terminal.go:988`) | `FlowAgent`, `FlowSavedSessionResume` | V16, V17 (never detachable) ‖ rest | every tracked and untracked-repair/autofix variant |
| slot stamping (`model/embedded_terminal.go:488`) | `RepoPath`, `WorktreePath`, `WorkingDir`, `FlowID`, `FlowPhaseID`, `FlowRepair`, `FlowAgent`, `FlowSavedSessionResume`, `LaunchID`, `Command`; forces `Embedded` true at `:479` | repair/agent/saved-resume slots | autofix is *not* stamped — it has no slot marker of its own |
| dock visibility (`model/embedded_terminal.go:458`, `:467`) | `FlowAutoLaunch`, `Headless` | V4 (no dock, keeps the active slot) ‖ V2/V6/V12/V14 (headless) ‖ rest | manual vs create vs resume |
| `updateFlowTerminalFocusAfterLaunch` (`model/model_keys.go:3003`) | `FlowAgent`, `FlowSavedSessionResume`, `FlowAutoLaunch`, `Headless` | V16/V17 (always focus input) ‖ V4 (no focus change) ‖ headless (focus list) ‖ rest | manual/create/resume/repair/autofix at equal headlessness |
| `reserveFlowSpawn` (`model/model_keys.go:3145`) | `FlowID`, `FlowLaunchTracked`, `FlowRepair` | tracked non-repair (V1–V10) ‖ rest | all untracked kinds are no-ops |
| `flowLaunchFailureUpdate` (`model/model_keys.go:3154`) | `FlowID`, `FlowPhaseID`, `ResumeSessionID`, `FlowLaunchTracked`, `FlowPhaseTerminal`, `FlowPhaseKind` | V8/V10 (terminal resume: refuse) ‖ V11–V17 (no phase: refuse) ‖ plan-review kind (blocked) ‖ rest | manual/auto/create/non-terminal resume |
| `tmuxRouteEligible` (`model/tmux_mode.go:63`) | `Headless`, `FlowRepair`, `Command` | V3/V9/V10/V15 eligible ‖ rest | every embedded variant, for whichever of the two reasons |
| `flowLaunchContextRequiresLifecycle` (`model/tmux_mode.go:329`) | all ten marker fields (`FlowID`, `FlowPhaseID`, `FlowPhaseKind`, plus the seven booleans; not `FlowAutofixPRNumber`) | Flow launches ‖ the four non-Flow literals and the two probes | all 17 variants alike; the two non-launch Flow-ID literals would also classify as Flow, but never reach it |
| `actions.ShouldPrefillEmbeddedPrompt` (`actions/actions.go:1338`) | `Command`, `Embedded`, `Headless`, `ResumeSessionID`, `InitialPrompt`, `FlowID`, `FlowPhaseID`, `FlowLaunchTracked`, `FlowRepair`, `FlowAgent`, `FlowAutofix` | V1/V5 (prefill) ‖ V11/V13/V16 (untracked prefill arms) ‖ resume and headless and tmux (no prefill) | the three untracked arms are separate clauses with identical effect |
| `actions.resumeSessionIDForContext` (`actions/actions.go:1450`) | `FlowSavedSessionResume` + 12 negated fields + `ResumeSessionID` | V17 ‖ everything else | asserts *one* variant; all others take the plain path |
| `actions.validateTrackedRepoTmuxRole` (`actions/tmux_mode.go:454`) | `FlowLaunchTracked`, `FlowID`, `FlowPhaseID`, `FlowAutoLaunch`, `Headless`, `FlowRepair`, `FlowAgent`, `FlowSavedSessionResume`, `FlowAutofix`, `FlowAutofixPRNumber` | V3/V9/V10 accepted | manual and resume tmux launches are identical to it |
| tracked lease branch (`actions/tmux_mode.go:332`) | `FlowLaunchTracked`, `SessionStateRoot`, `Executable`, `FlowID`, `FlowPhaseID`, `LaunchID` | V3/V9/V10 (leased window) ‖ V15 (plain window) | — |
| `repoTmuxWindowName` / `repoTmuxLeasedWindowName` (`actions/tmux_mode.go:535`, `:549`) | `FlowPhaseKind`, `Command`, `LaunchID` | phase variants (kind-named) ‖ V15 (command-named) | manual vs resume |
| `launchKind` (`model/flow_launch_pin.go:123`) | `FlowRepair`, `FlowAutofix`, `FlowAgent`, `FlowSavedSessionResume`, `ResumeSessionID` | repair ‖ autofix ‖ generic ‖ resume (V7–V10 **and** V17) ‖ phase | V1–V6 all report "phase" |
| `registerLaunchControl` (`model/flow_launch_pin.go:102`) | `FlowID`, `LaunchID`, `FlowPhaseID` | phase-owning ‖ unowned (V11–V17) | — |
| `reconcileInteractiveLaunchExitCmd` (`model/flow_launch_control.go:149`) | `FlowLaunchTracked`, `FlowID`, `FlowPhaseID`, `LaunchID` | V1–V10 ‖ rest | — |
| `actions.UsesStreamJSONOutput` (`actions/actions.go:1333`) | `Command`, `Embedded`, `Headless` | V2/V4/V6/V12/V14 on claude/cursor | — |
| provider env export (`actions/actions.go:1424`) | `PlanPhaseID`, `PlanPhaseTitle`, `PlanPhaseStatus` | V5/V6 only | every other variant exports empty strings |

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

**F2 — the three hand-written role predicates disagree about which fields
constitute a role.** `resumeSessionIDForContext` checks `FlowPhaseTerminal` and
`InitialPrompt == ""`; `validateTrackedRepoTmuxRole` checks neither, but does
check `FlowAutofixPRNumber != 0`; the autofix arm of
`flowEmbeddedTerminalIdentity` (`model/embedded_terminal.go:746`) checks neither
`FlowPhaseTerminal` nor `FlowAutoLaunch`. Three predicates, three different
field sets, one concept.

**F3 — `launchKind` classifies V17 and V7–V10 identically as `"resume"`**
(`model/flow_launch_pin.go:132`), because it falls through on `ResumeSessionID`.
A tracked phase resume and an untracked saved-session resume are different
policy classes everywhere else in the matrix; `launch.json` cannot tell them
apart.

**F4 — `createPhase` is the only kind whose tracked-ness is established outside
its builder**, and it is also the only kind that sets `PlanPhase*`. Both are
consequences of it being the one kind whose builder runs before the Flow record
exists.

**F5 — `WorkingDir` is unset for manual, auto, create and repair launches**, so
those four rely on the actions-side fallback chain while resume, autofix,
worktree agent and saved-session resume set it explicitly. The role payload has
to keep this distinction or change behaviour.

**F6 — the lifecycle branches on `attempt.Kind`, not on the context.** Both the
tracked stamp (`model/flow_launch_lifecycle.go:1104`) and the mutated-phase mark
(`model/flow_launch_lifecycle.go:958`) enumerate the same four kinds by hand.
The context cannot answer the question those branches ask, which is the clearest
evidence that a role belongs *on* the context.
