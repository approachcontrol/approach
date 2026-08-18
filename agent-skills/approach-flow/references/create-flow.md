# Create a Flow

Use this workflow when the current session was not launched as a Flow and the
user asks to create, add, or make an Approach Flow.

## Gather inputs

Determine:

- a concise title;
- task instructions, preferably in a file for multiline text;
- the repository's absolute main-worktree path;
- an optional configured preset;
- whether to prepare a dedicated branch and worktree now.

If the task is already written as a plan, retain that Markdown for the plan
save after Flow creation.

## Create and prepare

Run from anywhere inside the target repository to let Approach infer its main
worktree, create the Flow, reserve a branch, prepare a dedicated worktree, and
persist the result:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow create --prepare-worktree \
  --title "$FLOW_TITLE" \
  --instructions-file "$FLOW_INSTRUCTIONS_FILE" \
  --json
```

Pass `--repo-path "$REPO"` when running outside the repository. Add
`--preset "$PRESET"` when the user selected a non-default preset.

For a metadata-only Flow whose worktree should be prepared later, omit
`--prepare-worktree` and pass an absolute `--repo-path`. Do not manually
reimplement the preparation lifecycle with `git worktree` commands.

Parse `flow_id` from the JSON response, then verify it:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow read --flow-id "$FLOW_ID"
```

Treat either command failure, invalid JSON, or failed readback as a persistence
failure and stop.

## Import an existing plan

Save, verify, and link plan Markdown in one operation:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow plan save --flow-id "$FLOW_ID" \
  --title "$PLAN_TITLE" \
  --status draft \
  --file "$PLAN_FILE"
```

The JSON response contains `plan_id`, `plan_path`, and `linked`. Verify the
Flow and plan with explicit IDs:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow read --flow-id "$FLOW_ID"
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan read --plan-id "$PLAN_ID" --json
```

`flow plan save` seeds missing top-level implementation phases without
regressing existing plan progress. It deliberately does not complete the
Flow's plan phase. Complete that phase explicitly only when the imported plan
actually satisfies its goal:

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow phase complete --flow-id "$FLOW_ID" \
  --phase-id "$FLOW_PLAN_PHASE_ID" \
  --outcome approved \
  --summary "Imported and linked the implementation plan"
```

Report the created Flow ID, worktree/branch when prepared, linked plan ID, and
whether the phase was intentionally left pending.
