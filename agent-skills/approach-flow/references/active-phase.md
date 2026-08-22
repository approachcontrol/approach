# Work an active Flow phase

Use this workflow when `APPROACH_FLOW_ID` and `APPROACH_FLOW_PHASE_ID` identify
the launched phase.

## Start from persisted state

Read the Flow before doing work:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow read
```

Confirm the active phase goal, status, dependencies, Flow instructions,
repository/worktree metadata, linked plan, and any previous outcome or notes.

If a plan is linked, read it using the launch default:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan read --json
```

## Execute by semantic phase

- **Plan:** create or revise the plan, then follow `plans.md`. After verified
  plan persistence, explicitly complete the Flow phase.
- **Plan review:** read the saved plan, review it against the Flow goal, and
  record `approved`, `approved_with_concerns`, `changes_requested`, or
  `blocked`. Changes requested normally use `needs-attention`.
- **Implementation:** set the plan and relevant plan phase to `in_progress`,
  implement and validate, then record their final statuses before completing
  the Flow phase.
- **Review loop:** run the requested review workflow. Complete only when its
  quality gate passes; use `needs-attention` for actionable revision.
- **PR creation:** create or update the PR only when authorized, persist it with
  the pinned PR metadata command shown below, then complete the phase.
- **Autoreview:** persist approved or revision-required outcomes truthfully.
- **Merge:** merge only when authorized and verify the external result. Then
  complete or block the Flow phase, persist the matching merge metadata, and
  read the Flow back. The metadata command requires the phase transition first.

Use the phase's persisted semantic kind rather than assuming its ID is one of
the default names.

## Record the outcome

Commands default both IDs from the launch:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase complete --outcome approved --summary "$SUMMARY" --notes "$NOTES"
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase needs-attention --outcome changes_requested --notes "$NOTES"
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase block --outcome blocked --notes "$NOTES"
```

Use `needs-attention` when more agent or user work could resolve the issue. Use
`block` for a genuine external blocker. Approach derives downstream readiness;
do not manually mark later phases ready.

For recovery requested by the user or supported by persisted state:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase restart
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase reset
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase recover
```

Use `restart` to rerun blocked or needs-attention work. Use `reset` only for a
stale `running` phase whose latest launch has no live session. Use `recover`
only for `needs_attention` with `phase_result_missing` or
`phase_result_stale`; it atomically removes the observed stale launch and
derives `ready`. Recovery is non-replayable and does not release a retained
embedded terminal. The phase must carry the launch-control-owned reconciliation
stamp; ordinary phase commands cannot create it. Recovery also accepts a
stamped plan-review phase in the reconciliation-specific `blocked`/`blocked`
form; manually blocked reviews still use `restart`. The recovered launch ID is
fenced, so late phase writes are refused and late session hooks are ignored.

After any transition, rerun the pinned Flow read command shown at the start and
confirm the active phase's status and outcome. A command or readback failure is
a persistence failure and must not be treated as successful phase progression.

## Related metadata

Persist related artifacts when this phase creates or changes them:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow issue set --provider github --number "$ISSUE_NUMBER" --url "$ISSUE_URL"
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow pr set --provider github --number "$PR_NUMBER" --url "$PR_URL" --head "$HEAD" --base "$BASE"
```

After the Merge phase transition succeeds, persist its matching metadata:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow merge set --status merged --commit "$MERGE_COMMIT" --merged-at "$MERGED_AT"
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow read
```

Pass `--flow-id` explicitly only when updating a Flow other than the launched
one.
