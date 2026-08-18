# Persist plans

Use Flow-aware plan persistence when `APPROACH_FLOW_ID` is present. Use the
standalone plan commands for ad hoc plans not owned by a Flow.

## Plan owned by a Flow

Write the plan Markdown to a file, then save, verify, and link it atomically at
the CLI workflow level:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow plan save --title "$PLAN_TITLE" --status draft --file "$PLAN_FILE"
```

The command defaults the Flow and existing plan IDs from the launch, preserves
existing phase progress, creates missing top-level implementation phase rows,
verifies plan readback, links the plan, and prints JSON. It does not complete
the active Flow phase. If linking fails after the plan has persisted, report
the plan ID and path from the command error as a partial result; do not claim
the plan was linked or discard an existing plan revision to simulate rollback.

For later plan lifecycle changes:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan status set --status in_progress
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan phase set --phase-id "$PLAN_PHASE_ID" --title "$PHASE_TITLE" --status completed --order "$PHASE_ORDER"
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan read --json
```

Use statuses that truthfully describe the artifact, such as `draft`,
`approved`, `in_progress`, `completed`, `blocked`, or `superseded`.

## Standalone plan

Create a plan and request structured output:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan save --json --title "$PLAN_TITLE" --status draft --file "$PLAN_FILE"
```

Capture `plan_id` from the JSON response. For subsequent commands outside an
Approach launch, pass `--plan-id "$PLAN_ID"` explicitly:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan read --plan-id "$PLAN_ID" --json
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan status set --plan-id "$PLAN_ID" --status approved
```

Updating Markdown uses `plan save` with the same plan ID and title. Updating
only lifecycle state uses `plan status set`, so the Markdown need not be piped
again.

## Progress recording

Record a plan phase after the corresponding work state changes, not before:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan phase set \
  --phase-id "$PLAN_PHASE_ID" \
  --title "$PHASE_TITLE" \
  --status in_progress \
  --order "$PHASE_ORDER"
```

Verify with the pinned plan read command shown above. If persistence fails,
report it and do not claim the plan or phase was updated.
