# Flow occupancy matrix

Evidence base for `approach-x0r` (concentrate Flow occupancy in one deep
module). Every cell cites `file:line` against commit `3d147d4`, the state of the
code before any `approach-x0r` migration began; the proposed redesign is
ADR 0003. This is a frozen survey: later slices of the epic move these call
sites, and when they do this document stays pinned rather than being rewritten,
exactly as `docs/flow-launch-variant-matrix.md` does for `approach-hyl`.

Scope: every representation the TUI uses to answer "is something already
working in this Flow?", and every place that reads one. Not in scope: the
cross-process reservation itself (`internal/launchcontrol`,
`internal/flowlease`), which this module reads through a seam rather than
reimplements.

## 1. Sources

Thirteen distinct representations. "Cached" means answered from a display
mirror or an in-process map with no store I/O; "authoritative" means it reads
the Flow store, the session store, or the lease file at the moment of the call.

| # | Source | Definition | What it means | Lifetime | Freshness |
| --- | --- | --- | --- | --- | --- |
| S1 | Cross-process tmux lease | `model/flow_launch_lifecycle.go:433` `trackedFlowLeaseOccupied` → `internal/flowlease` | Another process holds a tracked tmux phase agent for this Flow | Until the peer's lease file clears | Authoritative (file read per call) |
| S2 | Lease unreadable | `model/flow_launch_lifecycle.go:439,445,455` (blank artifact root, nil inspector, inspect error); status text at `:429` `flowLeaseSetupErrorStatus` | Occupancy cannot be determined | Until the artifact root or inspector is usable | Authoritative, fail-closed |
| S3 | In-process attempt map | `model/flow_launch_attempt.go:95` `flowLaunchAttemptOccupied`, backed by `m.flowLaunchAttempts` (`:118`) | A launch attempt in this process holds the Flow | Reserve → release, one per exact Flow ID | Cached (in-process, but authoritative for this process) |
| S4 | Attempt kind | `model/flow_launch_attempt.go:110` `flowLaunchAttemptKind` | *Which* launch holds it (repair, phase resume, manual/auto phase, …) | Same as S3 | Same as S3 |
| S5 | Handoff-pending attempt state | `model/flow_launch_attempt.go:100` `flowLaunchHandoffPending`, state constant at `:19` | A private tmux handoff owns a reservation; quit must not race it | Between prepare and handoff consumption | In-process |
| S6 | Retained Flow terminal slot | `model/flow_phase_launch.go:811` `hasFlowEmbeddedTerminalForFlow` (scope Flow, `Terminal != nil`) | An embedded terminal with a live object occupies the Flow | Slot install → dismiss | In-process |
| S7 | Retained repair terminal slot | `model/flow_phase_launch.go:823` `hasFlowRepairEmbeddedTerminalForFlow` (scope Flow, `FlowRepair`, *terminal may be nil*) | A repair slot occupies the Flow | Slot install → dismiss | In-process |
| S8 | Prefill-pending slot | `model/embedded_terminal.go:136` `PrefillPending`, set at `:524`, cleared at `:552,565` | A slot exists but its prompt has not been delivered; excluded from selection and dock scans (`:309,314,362,795,810,902,1194`) | Install → first prefill tick | In-process |
| S9 | Exited-but-retained slot | `model/embedded_terminal.go:1048` `dismissExitedFlowEmbeddedTerminals`, `:1064` `hasExitedFlowEmbeddedTerminalAutoClose`, state test at `:1036` `embeddedTerminalRunning`, auto-close rule at `:1076` | A slot whose process ended still holds S6/S7 until the auto-close sweep runs | Process exit → sweep | In-process |
| S10 | Persisted running phase | `model/flow_phase_launch.go:804` (`phase.Status == flowstore.PhaseRunning` inside `flowAutoAdvanceOccupied`); also `model/flow_launch_generic_agent.go:159`, `model/flow_launch_saved_session_resume.go:192` | The store says a phase of this Flow is running | Until a phase write clears it | Cached when read from a poll record, authoritative when read from `ReadFlow` |
| S11 | Persisted session-attached phase | `model/flow_repair.go:114` `phaseHasMatchingLiveSession`, `:122` `…Except` | A phase's own mirrored sessions include a live one whose launch ID the phase owns | Until the session record ends | Same as the record it is applied to |
| S12 | Cached session mirror | `model/flow_launch_generic_agent.go:45` `hasKnownActiveFlowSession` over `m.sessions.Items()` + `m.worktreeSessions.Items()` | The display mirror shows an active session for this Flow | Refreshed by the session poll | Cached |
| S13 | Authoritative session listing | seam at `model/flow_launch_adapters.go:22`, wired at `:57` `ListFlowSessions`; unioned with S11 by `model/flow_launch_lifecycle.go:1488` `flowLaunchPhaseSessionOccupied` and `:1525` `…Except`; whole-Flow variant `model/flow_launch_generic_agent.go:166` `activeFlowSession` | A live session record exists in the store for this Flow | Store state | Authoritative (walks the whole session store) |
| S14 | Pending headless write | `model/model_keys.go:1070` `flowHeadlessWritePending` over `m.pendingFlowHeadlessWrites`; status at `:1012` | A headless toggle is in flight and the persisted preference is about to change | Enqueue → resolve (`:1055` `clearFlowHeadlessWritePending`) | In-process, transient |
| S15 | tmux autofix agent probe | `model/tmux_mode.go:446` `tmuxAutofixAgentStillRunning`; registry written at `model/model.go:2731` `withFlowAutofixTmuxLaunch` and read at `model/tmux_mode.go:462` `flowAutofixTmuxLaunchIDs` | A phase-untracked autofix agent still has a live tmux window in this Flow's worktree | Registry entry lifetime, in-process only | Authoritative but *shells out*; never called from a poll (`model/tmux_mode.go:426`) |
| S16 | Pending repair auto-drain marker | `model/flow_phase_launch.go:555` `hasPendingRepairAutoDrainMarker`, written at `:551` | A repair terminal was removed and its outcome has not been consumed by the AutoMode poll yet | Removal → poll consumption (`:564` `withoutRepairAutoDrainMarker`) | In-process |
| S17 | Durable phase-untracked owner | `FlowRecord.UntrackedOwner`, mutated by `flowstore.ClaimUntrackedOwner`, `ActivateUntrackedOwner`, `ReplaceUntrackedOwner`, and `ReleaseUntrackedOwner` | The exact repair, autofix, or generic worktree-agent launch owns the Flow worktree | Reserved → live → ended, fenced by launch ID | Persisted and cross-process; cached reads only defer, authoritative reads may reclaim proven-dead owners |

S17 replaces the in-process tmux registry as authority. Every authoritative
launch read checks it before admitting tracked phases, resumes, repair, autofix,
generic worktree agents, saved-session resumes, or AutoMode work. A reservation
stores the launcher's PID until activation replaces it with the exact transport:
repo-tmux session/window, isolated embedded-tmux socket/session, or direct child
PID. Probes return live, dead, or unknown. Missing tools, timeouts, and probe
errors stay occupied; only proven death permits an identity-fenced release.
Provider hooks are not exit evidence because Codex Stop is a turn boundary and
Claude SessionEnd can be `/clear`. The agent wrapper invokes the pinned
`untracked-owner-release` callback on process exit, so detached tmux completion
updates the mirror without waiting for another launch attempt. Cached footer and
drain queries do not probe processes or walk stores.

Two composites are built from the above and are what most consumers actually
call:

| Composite | Definition | Terms |
| --- | --- | --- |
| `flowLaunchAdmissionOccupied` | `model/flow_launch_lifecycle.go:404` | S2 ∨ S1 ∨ `flowLaunchRuntimeOccupied` |
| `flowLaunchRuntimeOccupied` | `model/flow_launch_lifecycle.go:417` | S3 ∨ S6 ∨ S7 |

S6 and S7 overlap rather than nest, which is load-bearing and stated twice in
the code: `model/flow_launch_lifecycle.go:1261` ("the two predicates overlap
rather than nest — one requires a live terminal, the other a repair slot") and
`model/flow_launch_resume.go:135`.

## 2. Consumers

One row per call site. "Reads" lists source IDs; "Deliberately omits" records a
source the site could have read and does not, with the comment that justifies
it — those comments are load-bearing and are what §3 turns into `Purpose`
values.

### 2.1 Admission (mutating; each reserves an attempt on success)

| Consumer | Site | Reads | Deliberately omits | Why |
| --- | --- | --- | --- | --- |
| Manual phase | `model/flow_launch_lifecycle.go:285` `admitManualFlowLaunch` | S14 (`:288`), S1/S2 typed (`:294`), S3∨S6∨S7 via runtime (`:304`) | `flowLaunchAdmissionOccupied` | "The typed lease check above owns the actionable cross-process result. Repeating it through `previewFlowLaunch` would collapse a peer race into the generic no-phase status" (`:297-300`) |
| Auto phase | `model/flow_launch_lifecycle.go:344` `admitAutoFlowLaunch` | S1∨S2∨S3∨S6∨S7 via `flowLaunchAdmissionOccupied` | any status text | "refuses in silence, without exception. The advance poll runs at 1 Hz and is view-independent, so any status a refusal set would be repainted every second" (`:331-333`) |
| Create phase | `model/flow_launch_create.go:424` `handleCreateFlowAllocated` | S3∨S6∨S7 via runtime | S1/S2 | "Creation-time Plan Now allocates a brand-new Flow and remains embedded and unleased, so it uses this narrower check" (`model/flow_launch_lifecycle.go:414-416`) |
| Phase resume | `model/flow_launch_resume.go:118` `admitPhaseResumeFlowLaunch` | S1/S2 typed (`:124`), runtime (`:132`), S6∧¬S7 for the status only (`:138`) | `flowLaunchAdmissionOccupied` | "Repeating it through `flowLaunchAdmissionOccupied` could turn a peer race into a silent refusal before the reservation-protected recheck reports the real cause" (`:129-131`) |
| Repair | `model/flow_launch_repair.go:149` `admitRepairFlowLaunch` | S1/S2 typed (`:157`), runtime (`:164`), S14 (`:164`), then S4/S6/S7/S3 for naming (`:201`) | `flowLaunchAdmissionOccupied` | "durable obstacles before the transient one, because a headless write clears on its own and an open terminal does not" (`:146-148`) |
| Autofix | `model/flow_launch_autofix.go:134` `admitAutofixFlowLaunch` | S1/S2 typed (`:144`), S4 (`:155,158`), S6∨S7 (`:161`), runtime (`:164`), S14 (`:167`) | `flowLaunchAdmissionOccupied` | "repeating it through `flowLaunchAdmissionOccupied` would collapse a held or unreadable lease into the generic in-flight status" (`:152-154`) |
| Worktree agent | `model/flow_launch_generic_agent.go:76` `admitWorktreeAgentFlowLaunch` | S1/S2 (`:100`), S3 (`:105`), S15 (`:108`), S6∨S7 (`:111`), S12 (`:114`), S10∨S11 via `genericFlowRuntimeOccupancyReason` (`:117`) | — | The only admission that reads every family; it is Flow-scoped and phase-untracked |
| Saved-session resume | `model/flow_launch_saved_session_resume.go:136` (post-read), transfer at `model/flow_launch_attempt.go:203` | S1/S2 (`:136`), `flowLaunchAdmissionOccupied` on the *destination* Flow (`flow_launch_attempt.go:203`) | — | Flow identity is not known until the authoritative session read, so admission happens after it |

### 2.2 Authoritative read and prepare stages (mutating)

| Consumer | Site | Reads |
| --- | --- | --- |
| Tracked-phase read | `model/flow_launch_lifecycle.go:610` | S11∪S13 via `flowLaunchPhaseSessionOccupied` |
| AutoMode read | `model/flow_launch_lifecycle.go:684`, `:705` | S10 minus the candidate (`flowRecordHasOtherRunningPhase`, `:534`), then S11∪S13 |
| Phase-resume read | `model/flow_launch_resume.go:230` | S11∪S13 with the target session exempted (`flowLaunchPhaseSessionOccupiedExcept`) |
| Repair read | `model/flow_launch_repair.go:325` `flowRepairPhaseSessionOccupied` | S11∪S13 across *every* phase of the record |
| Autofix read | `model/flow_launch_autofix.go:277` `flowRecordHasLivePhaseSession` | S11∪S13 across every non-terminal phase |
| Worktree-agent read | `model/flow_launch_generic_agent.go:196`, `:200` | S13 whole-Flow (`activeFlowSession`), then S10∨S11 |
| Worktree-agent prepare | `model/flow_launch_generic_agent.go:244`, `:263`, `:267` | S1/S2 under the reservation, then S13 and S10∨S11 again |
| Create sessions read | `model/flow_launch_create.go:468` | S13 whole-Flow (any record at all, then any active one) |
| Saved-session Flow read | `model/flow_launch_saved_session_resume.go:170` `validateSavedSessionResumeFlow` (`:184`) | S10 (`:192`), S11 (`:195`), S13 (`:200`) |
| Generic prepare (tracked) | `model/flow_launch_lifecycle.go:801` | S1/S2 under the cross-process reservation |
| Repair prepare | `model/flow_launch_repair.go:379` | S1/S2 under repair's own reservation |
| Autofix prepare | `model/flow_launch_autofix.go:330` | S1/S2 under the reservation |
| Resume prepare | `model/flow_launch_resume.go:325` | S1/S2 under the reservation |
| Saved-session prepare | `model/flow_launch_saved_session_resume.go:248` | S1/S2 under the reservation |
| Saved-session slot recheck | `model/flow_launch_lifecycle.go:889` | S6 |
| Install backstop | `model/flow_launch_lifecycle.go:1269` `flowLaunchEmbeddedBackstop` | S6, S7, per kind — see §4.2 |

### 2.3 Previews and footer affordances (non-mutating; must not do I/O per frame)

| Consumer | Site | Reads | Note |
| --- | --- | --- | --- |
| `previewFlowLaunch` | `model/flow_launch_lifecycle.go:380` | `flowLaunchAdmissionOccupied` | Conjoins launchability with occupancy; `cachedFlowLaunchTarget` (`:389`) is the launchability half alone, split so "admission needs the two separately so it can name whichever one is actually blocking; the footer only needs their conjunction" (`:386-388`) |
| Footer `g` | `model/model.go:2705` `selectedFlowHasLaunchablePhase` | S14 (`:2712`) + `previewFlowLaunch` | "what the footer advertises and what `g` accepts can never disagree" (`:2702-2703`) |
| `previewPhaseResume` | `model/flow_launch_resume.go:181` | S1/S2 fail-closed (`:183`), S6∧¬S7 (`:186`) | "deliberately narrower than `flowLaunchAdmissionOccupied`: a competing lifecycle attempt and an open repair terminal both refuse a resume silently by design, and withdrawing the key for them would be a behavior change" (`:175-180`) |
| `previewRepairLaunch` | `model/flow_launch_repair.go:137` | `flowLaunchAdmissionOccupied` | Mirrors `previewFlowLaunch` over `cachedRepairTarget` |
| Footer `R` | `model/flow_repair.go:154` `selectedFlowRepairReady` | `previewRepairLaunch` + S14 | "The headless term stays outside the preview because admission's occupancy set has no headless notion and `flowLaunchAdmissionOccupied` is shared with kinds that must not inherit one" (`:149-153`) |
| Footer `U` | `model/flow_launch_autofix.go:90` `selectedFlowAutofixReady` | `flowLaunchAdmissionOccupied` + S14 | Also gates the `U` keypress's tmux probe so an already-owned Flow "neither forks tmux nor answers with the live-window refusal when admission has a more specific one to give" (`:104-107`) |
| Footer worktree agent | `model/flow_launch_generic_agent.go:39,42` `selectedFlowWorktreeAgentReady` | `flowLaunchAdmissionOccupied`, S12, S10∨S11 | The only footer predicate that reads the session mirror |

### 2.4 Poll-driven (AutoMode drain; must never shell out or walk the session store per tick)

| Consumer | Site | Reads | Note |
| --- | --- | --- | --- |
| Drain arm/disarm | `model/flow_phase_launch.go:502` | S7, S16 | A removed-but-unconsumed repair outcome disarms the drain |
| Drain gate | `model/flow_phase_launch.go:755` `flowAutoAdvanceOccupied` (`:794`) | S1/S2 (`:795`), S3 (`:800`), S10 (`:804`), S6 (`:808`) | Deliberately *not* S7 and not S15: "AutoMode never reaches this function … a poll on a timer must not shell out" (`model/tmux_mode.go:425-427`) |
| Drain session pre-filter | `model/flow_phase_launch.go:773` | S11 only | "the snapshot half of the lifecycle's session check, and the reason a stalled phase does not cost a session-store walk every second … the mirrored sessions the poll already carries answer the common case for free" (`:765-772`) |
| Auto-merge level gate | `model/flow_phase_launch.go` `prepareAutoMergeLaunches` | S1/S2, S3, S10, S6, then S11 | Reuses the drain's cheap occupancy and session mirrors but is armed by effective merge policy and ready state rather than a completion edge; contention retries on a later poll. |

### 2.5 Non-launch consumers

| Consumer | Site | Reads | Note |
| --- | --- | --- | --- |
| Session release footer | `model/flow_session_release.go:88,92` `flowPhaseSessionReleasable` | S11 | Mirror-only by design; "Variant B — a live record the mirror never saw — is therefore invisible here" (`:80-82`) |
| Session release refusal | `model/flow_session_release.go:115` | S3, S14 | "A release must not act while the launch lifecycle holds this Flow, or while a headless write is in flight: both can be about to persist a launch ID the probe has not seen" (`:111-114`) |
| Session release press refusal | `model/flow_session_release.go:136` | S11 | Names the blocker only when the mirror already shows the stall |
| Session release probe | `model/flow_session_release.go:204` | S13 | The store walk the keypress pays for |
| Repair classifier | `model/flow_repair.go:65` | S11 | "Treat any matched non-ended session as active work even when the persisted phase already says blocked or needs_attention" (`:66-68`) |
| Quit deferral | `model/embedded_terminal.go:170`, `:920`, `:942` | S5 | Quit must not land mid-handoff |
| Detach policy | `model/embedded_terminal.go:976`, policy at `:1002` | — (role, not occupancy) | Worktree-agent and saved-session-resume terminals are `embeddedTerminalDetachNever` "to preserve occupancy" (`:977`): detaching would destroy S6 while the agent lives |
| `g` keypress probe | `model/model_keys.go:2308` | S15 | "an autofix agent (U) writes no phase, so in tmux mode nothing admission can see reports that its agent is still working in this Flow's worktree" (`:2303-2307`) |
| Resume keypress probe | `model/model_keys.go:2573` | S15 | Same gap, from the resume keystroke |
| Repair keypress probe | `model/flow_repair.go:163` (`tmuxFlowAgentStillRunning`) | S15 + phase launch IDs | "Repair's live-agent fence is `hasFlowEmbeddedTerminalForFlow`, and in tmux mode that slot does not exist" (`:163-165`) |
| Autofix keypress probe | `model/flow_launch_autofix.go:112` | S15 + phase launch IDs | Gated on the footer predicate first |

## 3. Divergences

Each row is a place two consumers read the same underlying question and answer
it differently — on purpose. Each becomes a `Purpose` value in ADR 0003.

| # | Divergence | Sites | Justification (verbatim from the code) |
| --- | --- | --- | --- |
| D1 | The composite collapses S1 and S2 into one boolean; four consumers refuse it and check the lease typed instead | `model/flow_launch_lifecycle.go:294` vs `:344`; `model/flow_launch_resume.go:124`; `model/flow_launch_repair.go:157`; `model/flow_launch_autofix.go:144` | "would collapse a held or unreadable lease into the generic in-flight status" (`flow_launch_autofix.go:153`) |
| D2 | Create uses `flowLaunchRuntimeOccupied`, every other existing-Flow route uses the admission composite | `model/flow_launch_create.go:424` vs `model/flow_launch_lifecycle.go:344` | "a brand-new Flow … remains embedded and unleased" (`flow_launch_lifecycle.go:414-416`) |
| D3 | Refusal is silent for auto, create, and phase resume; loud for manual, repair, autofix, and worktree agent | `flow_launch_lifecycle.go:331` (silent), `:288` (loud) | "any status a refusal set would be repainted every second over whatever the user is actually looking at" |
| D4 | The footer's occupancy set is narrower than admission's for resume, and wider for repair and autofix (both add S14) | `flow_launch_resume.go:175`; `flow_repair.go:149`; `flow_launch_autofix.go:93` | "withdrawing the key for them would be a behavior change this bead does not make"; "admission's occupancy set has no headless notion" |
| D5 | Session occupancy is phase-scoped for launch and resume, Flow-scoped for repair, non-terminal-phase-scoped for autofix, whole-Flow for the worktree agent and create | `flow_launch_lifecycle.go:1488`; `flow_launch_repair.go:325`; `flow_launch_autofix.go:277`; `flow_launch_generic_agent.go:166`; `flow_launch_create.go:468` | "the repair prompt authorizes phase reset, phase set, and plan set across the whole record" (`flow_launch_repair.go:294-297`); "a wider rule would let one crashed agent make the Flow permanently unlaunchable" (`flow_launch_autofix.go:265-268`) |
| D6 | Resume exempts one session identity from S11/S13; every other caller passes the zero identity | `flow_launch_resume.go:230` vs `flow_launch_lifecycle.go:610` | "the session it is reattaching to is expected to look live … so counting it would refuse every resume" (`flow_launch_lifecycle.go:1518-1521`) |
| D7 | The AutoMode drain answers from mirrors (S11, S10 on the poll record) where the lifecycle answers from the store (S13) | `flow_phase_launch.go:773` vs `flow_launch_lifecycle.go:705` | "without this the next poll would re-admit and call `ListFlowSessions` again at 1 Hz" |
| D8 | S15 (the tmux probe) lives on keystrokes and never in admission or the poll | `model_keys.go:2308,2573`; `flow_repair.go:163`; `flow_launch_autofix.go:112` | "it shells out, and admission must not"; "AutoMode never reaches this function" (`tmux_mode.go:425-426`) |
| D9 | S12 (the session mirror) is read by exactly one family — the worktree agent — and by nothing else | `flow_launch_generic_agent.go:39,114` | The worktree agent is Flow-scoped and phase-untracked, so no phase record can speak for it |
| D10 | S6 and S7 are combined differently per consumer: `∨` for admission, `∧¬` for resume's status, per-kind for the backstop | `flow_launch_lifecycle.go:422`; `flow_launch_resume.go:138`; `flow_launch_lifecycle.go:1271-1294` | "the two predicates overlap rather than nest" (`flow_launch_lifecycle.go:1261`) |

## 4. Ordered refusals

Two consumers need more than a boolean: they need to name the holder, in a
fixed order. This is the concrete requirement the verdict type must satisfy.

### 4.1 Repair's refusal ladder

`model/flowRepairOccupancyRefusal` — `model/flow_launch_repair.go:201`,
documented at `:198-200` as "repair attempt, phase-resume attempt, manual/auto
phase attempt, actual terminal or other-attempt fallback, then pending headless
write".

| Rank | Condition | Source | Status constant |
| --- | --- | --- | --- |
| 1 | attempt kind is repair | S4 | `flowRepairPendingStatus` |
| 2 | attempt kind is phase resume | S4 | `flowRepairResumePendingStatus` |
| 3 | attempt kind is manual or auto phase | S4 | `flowRepairPhasePendingStatus` |
| 4 | any Flow terminal, repair slot, or other attempt | S6 ∨ S7 ∨ S3 | `flowRepairTerminalStatus` |
| 5 | fallthrough | S14 | `flowHeadlessWritePendingStatus` |

The lease is ranked *above* all of these and answered separately, with its own
two outcomes (`model/flow_launch_repair.go:157-163`).

### 4.2 The install backstop's per-kind ladder

`flowLaunchEmbeddedBackstop` — `model/flow_launch_lifecycle.go:1269`. Every kind
refuses S7; repair and three others also refuse S6, and each returns different
text.

| Kind | Condition | Status |
| --- | --- | --- |
| `createPhase` | S6 ∨ S7 | "Flow creation launch canceled because a terminal is already open for this Flow" (`:1272`) |
| `worktreeAgent` | S6 ∨ S7 | `flowWorktreeAgentSlotStatus` (`:1278`) |
| `savedSessionResume` | S6 only | "Saved session resume canceled because an embedded terminal already occupies this Flow" (`:1284`) |
| `repair` | S7 ∨ S6 | `flowRepairTerminalStatus` (`:1290`) |
| `phaseResume` | S7 | "Flow phase resume canceled because a repair terminal is already open for this Flow" (`:1298`) |
| `autofix` | S7 | `flowAutofixCanceledStatus` (`:1302`) — "This launch targets no phase, so naming one would be false" |
| manual / auto phase | S7 | "Flow phase launch canceled because a repair terminal is already open for this Flow" (`:1304`) |

The backstop's own comment states why it must survive the migration:
"Admission makes every branch here unreachable, but dropping the backstop would
be a regression against a future unguarded source" (`:1248-1250`), and "keeping
it here is what stops the migration from quietly narrowing repair's last line of
defense" (`:1256-1258`).

### 4.3 Autofix's ordered refusal

Not a named function, but the same shape, inline at
`model/flow_launch_autofix.go:155-168`: repair attempt (S4) → phase-resume
attempt (S4) → terminal or repair slot (S6 ∨ S7) → any other runtime holder
(S3) → pending headless write (S14), "ordered durable before transient, as
repair's are" (`:132-133`).

### 4.4 The worktree agent's ordered refusal

`model/flow_launch_generic_agent.go:100-119`: lease (S1/S2) → attempt (S3) →
tmux autofix probe (S15) → terminal or repair slot (S6 ∨ S7) → session mirror
(S12) → persisted running or session-attached phase (S10 ∨ S11, and this one
names the phase ID: `genericFlowRuntimeOccupancyReason`, `:154`).

## 5. Findings

Recorded as observations against `3d147d4`, not as work items for this slice.

- **F1 — the composite is read by seven consumers and refused by five of
  them.** `flowLaunchAdmissionOccupied` has four call sites that use it as-is
  (`flow_launch_lifecycle.go:344,380`, `flow_launch_repair.go:139`,
  `flow_launch_autofix.go:93`, `flow_launch_generic_agent.go:39`,
  `flow_launch_attempt.go:203`) and five admissions that deliberately unroll it.
  That ratio is the epic's whole case: the boolean is not the wrong answer, it
  is an answer to a question only some callers are asking.
- **F2 — four ordered ladders exist and none shares a table.** §4.1–§4.4 rank
  overlapping source sets in three different orders with four different
  vocabularies. Any of them can drift from the others silently.
- **F3 — S6/S7 combination is open-coded at eleven sites.** Every one has to
  re-derive that the two predicates overlap rather than nest. `resume`'s
  `∧¬` (`flow_launch_resume.go:138`) is the only site whose correctness depends
  on that overlap being understood, and it carries a five-line comment saying so.
- **F4 — the cached/authoritative split is real but undocumented as a rule.**
  It is currently enforced by convention plus per-site comments
  (`flow_phase_launch.go:765-772`, `tmux_mode.go:425-427`,
  `flow_session_release.go:80-82`). Nothing prevents a new preview from calling
  `ListFlowSessions` per frame, or a poll from shelling out.
- **F5 — S15 is invisible to every non-keystroke consumer.** The tmux autofix
  registry is in-process only and never consulted by admission or the drain, so
  a tracked phase launch admitted by AutoMode can land in a worktree an autofix
  agent still owns. This is stated as a known limit
  (`tmux_mode.go:421-427`), not a bug this epic introduces.

  *Follow-up, 2026-08-20 (`approach-x0r.13` grill, decision Q6).* Frozen in
  `approach-x0r` and filed separately. A registry-only check at `StageDrain`
  looked free — `flowAutofixTmuxLaunchIDs` is a map lookup with no I/O — but is
  not viable: registry entries are never removed (`model/model.go:236-244`, "It
  needs no expiry"), so it would permanently block AutoMode on any Flow that ran
  a single autofix in the session.
- **F6 — S9 keeps a Flow occupied after its process exits.** An exited Flow
  terminal still satisfies S6 until `dismissExitedFlowEmbeddedTerminals`
  (`embedded_terminal.go:1048`) sweeps it, so occupancy briefly outlives the
  agent. Consumers do not distinguish the two states today.

  *Correction, 2026-08-20 (`approach-x0r.13` grill, decision Q4).* As written
  this finding conflates two phenomena with very different lifetimes. For
  auto-closing slots — phase, repair, createPhase, resume — the sweep runs on
  `embeddedTerminalTickMsg` (`model/model.go:2104-2105`) and
  `embeddedTerminalRepaintInterval = time.Second / 30`
  (`embedded_terminal.go:293`), so the window is 33ms, one frame. For
  `FlowAgent` and `FlowSavedSessionResume` slots the sweep skips them entirely
  (`:1051`, `:1067`) and the exited terminal is retained until the user
  dismisses it — not a race window but the `embeddedTerminalDetachNever` policy
  of §2.5, which exists to preserve occupancy. Only the first case is a window,
  and 33ms is not worth closing. `approach-x0r` freezes both.
