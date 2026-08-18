# Work an active Flow phase

Use this workflow when `APPROACH_FLOW_ID` and `APPROACH_FLOW_PHASE_ID` identify
the launched phase.

## Start from persisted state

Read the Flow before doing work:

```bash
approach flow read
```

Confirm the active phase goal, status, dependencies, Flow instructions,
repository/worktree metadata, linked plan, and any previous outcome or notes.

If a plan is linked, read it using the launch default:

```bash
approach plan read --json
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
  `approach flow pr set`, then complete the phase.
- **Autoreview:** persist approved or revision-required outcomes truthfully.
- **Merge:** merge only when authorized, persist with `approach flow merge set`,
  and complete only after merge state is verified.

Use the phase's persisted semantic kind rather than assuming its ID is one of
the default names.

## Record the outcome

Commands default both IDs from the launch:

```bash
approach flow phase complete --outcome approved --summary "$SUMMARY" --notes "$NOTES"
approach flow phase needs-attention --outcome changes_requested --notes "$NOTES"
approach flow phase block --outcome blocked --notes "$NOTES"
```

Use `needs-attention` when more agent or user work could resolve the issue. Use
`block` for a genuine external blocker. Approach derives downstream readiness;
do not manually mark later phases ready.

For recovery requested by the user or supported by persisted state:

```bash
approach flow phase restart
approach flow phase reset
```

After any transition, run `approach flow read` and confirm the active phase's
status and outcome. A command or readback failure is a persistence failure and
must not be treated as successful phase progression.

## Related metadata

Persist related artifacts when this phase creates or changes them:

```bash
approach flow issue set --provider github --number "$ISSUE_NUMBER" --url "$ISSUE_URL"
approach flow pr set --provider github --number "$PR_NUMBER" --url "$PR_URL" --head "$HEAD" --base "$BASE"
approach flow merge set --status merged --commit "$MERGE_COMMIT" --merged-at "$MERGED_AT"
```

Pass `--flow-id` explicitly only when updating a Flow other than the launched
one.
