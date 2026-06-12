---
name: wtui-flow-create
description: Create a new persisted wtui Flow from an ad hoc planning session or already-written plan, then optionally save and link the plan artifact.
---

# Creating wtui Flows

Use this skill when the user explicitly asks to turn the current work into a
wtui Flow, with phrasing such as "make a flow from this", "add this as a wtui
flow", or "create a wtui flow for this plan".

This skill is for ad hoc sessions. It does not require `WTUI_FLOW_ID` or
`WTUI_FLOW_PHASE_ID`; those belong to the `wtui-flow` skill for agents already
launched inside an existing Flow phase. This skill can create a Flow and link a
saved plan, but v1 cannot attach the current provider session to the new Flow.
Do not claim the session is attached, and do not run `wtui flow session attach`
unless a future CLI implements it.

## Start With Shared State

Build reusable state-root arguments before running commands. `wtui flow` reads
`WTUI_FLOW_STATE_ROOT`, and `wtui plan` reads `WTUI_PLAN_STATE_ROOT`, but passing
the same explicit root keeps created flows and imported plans together.
Codex App prompt-only metadata is used instead of inherited shell
environment, so copy the provided `--state-root` value manually when needed.

```bash
WTUI_ARTIFACT_ROOT="${WTUI_FLOW_STATE_ROOT:-${WTUI_PLAN_STATE_ROOT:-${WTUI_SESSION_STATE_ROOT:-}}}"
FLOW_STATE_ARGS=()
PLAN_STATE_ARGS=()
if [ -n "$WTUI_ARTIFACT_ROOT" ]; then
  FLOW_STATE_ARGS=(--state-root "$WTUI_ARTIFACT_ROOT")
  PLAN_STATE_ARGS=(--state-root "$WTUI_ARTIFACT_ROOT")
fi
```

## Resolve Required Metadata

Resolve Flow metadata from launch metadata first, then from the current git
checkout. Required values:

- `FLOW_TITLE`: short task title.
- `FLOW_INSTRUCTIONS` or `FLOW_INSTRUCTIONS_FILE`: the task instructions used to
  seed the Flow.
- `WTUI_REPO_PATH`: absolute repository path.

Use `WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, and `WTUI_COMMIT` when known. If
`WTUI_REPO_PATH` is missing, relative, or unclear, ask the user instead of
creating a malformed Flow. If git metadata cannot be recovered, create the Flow
with the known required values and report which optional fields were omitted.

## Create And Verify The Flow

Prefer `--instructions-file` when the instructions are more than a short
sentence; use `--instructions` for compact task text. `wtui flow create --json`
prints machine-readable output containing `flow_id`.

```bash
if [ -n "$FLOW_INSTRUCTIONS_FILE" ]; then
  FLOW_JSON=$(wtui flow create \
    --title "$FLOW_TITLE" \
    --instructions-file "$FLOW_INSTRUCTIONS_FILE" \
    --repo-path "$WTUI_REPO_PATH" \
    --worktree-path "$WTUI_WORKTREE_PATH" \
    --branch "$WTUI_BRANCH" \
    --commit "$WTUI_COMMIT" \
    --json \
    "${FLOW_STATE_ARGS[@]}")
else
  FLOW_JSON=$(wtui flow create \
    --title "$FLOW_TITLE" \
    --instructions "$FLOW_INSTRUCTIONS" \
    --repo-path "$WTUI_REPO_PATH" \
    --worktree-path "$WTUI_WORKTREE_PATH" \
    --branch "$WTUI_BRANCH" \
    --commit "$WTUI_COMMIT" \
    --json \
    "${FLOW_STATE_ARGS[@]}")
fi
FLOW_ID=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, sys; print(json.load(sys.stdin)["flow_id"])')
wtui flow read --flow-id "$FLOW_ID" "${FLOW_STATE_ARGS[@]}" >/dev/null
```

If any command in this section fails, report the command error and stop. Do not
say a Flow was created unless `wtui flow create` succeeded and `wtui flow read
--flow-id "$FLOW_ID"` verified it.

## Import An Existing Plan

If the current session already has a concrete plan body, save it and link it to
the new Flow. Do not invent a plan body just to satisfy this path.

```bash
PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | wtui plan save \
  --title "$FLOW_TITLE" \
  --status approved \
  --repo-path "$WTUI_REPO_PATH" \
  --worktree-path "$WTUI_WORKTREE_PATH" \
  --branch "$WTUI_BRANCH" \
  --commit "$WTUI_COMMIT" \
  "${PLAN_STATE_ARGS[@]}")

wtui flow plan set \
  --flow-id "$FLOW_ID" \
  --plan-id "$PLAN_ID" \
  "${FLOW_STATE_ARGS[@]}"

wtui plan read --plan-id "$PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null

wtui flow phase complete \
  --flow-id "$FLOW_ID" \
  --phase-id plan \
  --summary "Imported plan $PLAN_ID." \
  "${FLOW_STATE_ARGS[@]}"
```

Only complete the new Flow's `plan` phase after all four steps succeed: plan
save, flow-plan link, plan readback, and phase completion. If there is no
concrete plan body, report the new Flow ID and leave Plan ready for a normal
Flow Plan launch.

## Persistence Failures

If any `wtui flow` or `wtui plan` command exits non-zero, report the command
error. These persistence failures must not be treated as success. Do not say a
Flow was created, a plan was saved, a plan was linked, or a phase was completed
unless the corresponding command succeeded.

When the Flow exists but imported-plan persistence fails, attempt to record the
failed import on the new Flow Plan phase and report whether that persistence
also succeeded:

```bash
wtui flow phase block \
  --flow-id "$FLOW_ID" \
  --phase-id plan \
  --notes "Plan import failed; report the wtui command error to the user." \
  "${FLOW_STATE_ARGS[@]}"
```

Use `wtui flow phase needs-attention --flow-id "$FLOW_ID" --phase-id plan
--notes "..." "${FLOW_STATE_ARGS[@]}"` instead when the Flow can continue but
the imported plan should be reviewed before implementation. If this recovery
update fails too, report both the original command error and the recovery
command error.
