---
name: wtui-flow
description: Participate in a wtui Flow phase by reading the active flow, using wtui plan/flow commands for persistence, and reporting any persistence failure instead of silently advancing workflow state.
---

# wtui Flow

Use this skill whenever both `WTUI_FLOW_ID` and `WTUI_FLOW_PHASE_ID` are set.
The persisted Flow record and the `wtui flow` CLI are the source of truth; this
skill only explains how an agent should update them.

## Start Every Phase

Build reusable state-root arguments before running commands. `wtui flow` reads
`WTUI_FLOW_STATE_ROOT`, but `wtui plan` does not; passing the same explicit root
keeps Flow and plan artifacts together when a launch prompt provides a shared
artifact root.

```bash
WTUI_ARTIFACT_ROOT="${WTUI_FLOW_STATE_ROOT:-${WTUI_PLAN_STATE_ROOT:-${WTUI_SESSION_STATE_ROOT:-}}}"
FLOW_STATE_ARGS=()
PLAN_STATE_ARGS=()
if [ -n "$WTUI_ARTIFACT_ROOT" ]; then
  FLOW_STATE_ARGS=(--state-root "$WTUI_ARTIFACT_ROOT")
  PLAN_STATE_ARGS=(--state-root "$WTUI_ARTIFACT_ROOT")
fi

if ! wtui flow read --flow-id "$WTUI_FLOW_ID" "${FLOW_STATE_ARGS[@]}" >/dev/null; then
  echo "wtui flow read failed; report the command error to the user." >&2
  exit 1
fi
```

Also use the launch metadata when present: `WTUI_FLOW_PHASE_ID`,
`WTUI_PLAN_ID`, `WTUI_PLAN_PATH`, `WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`,
`WTUI_BRANCH`, `WTUI_COMMIT`, `WTUI_SESSION_STATE_ROOT`, and
`WTUI_PLAN_STATE_ROOT`.

Agent-facing phase statuses are `running`, `needs_attention`, `completed`,
`blocked`, and `skipped`. Agents cannot set `ready`; readiness is derived
by wtui. Skipped phases require `--notes`, and restarting a blocked or
needs-attention phase as `running` requires `--notes`.

Use the current implemented phase update command:

```bash
wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id "$WTUI_FLOW_PHASE_ID" \
  --status completed \
  --outcome "approved" \
  --summary "What changed and where the next phase should begin." \
  --notes "Optional audit notes." \
  "${FLOW_STATE_ARGS[@]}"
```

## Persistence Failures

If any `wtui flow` or `wtui plan` command exits non-zero, report the error to
the user. These persistence failures must not be treated as successful phase
progression. Do not say a phase advanced, a plan was saved, a PR was recorded,
or a merge was recorded unless the corresponding command succeeded.

The current Flow CLI exposes `create`, `list`, `read`, `phase set`, and
`plan set`. Dedicated structured commands for child phases, PR metadata, and
merge metadata are not part of the implemented contract yet. Until those
commands exist, record the best available details in the current phase
`--summary`, `--outcome`, and `--notes`, and explicitly tell the user that the
richer metadata was not persisted as first-class Flow fields.

## Plan Phase

Goal: produce a saved wtui plan artifact.

1. Read the flow.
2. Save or update the plan through `wtui plan save`.
3. Link the saved plan artifact back to the Flow with `wtui flow plan set`.
4. Record plan progress with `wtui plan phase set` when the plan has phases.
5. Complete or block the Flow phase with `wtui flow phase set`.

```bash
if ! PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | wtui plan save \
    --title "$FLOW_TITLE" \
    --status approved \
    --repo-path "$WTUI_REPO_PATH" \
    --worktree-path "$WTUI_WORKTREE_PATH" \
    --branch "$WTUI_BRANCH" \
    "${PLAN_STATE_ARGS[@]}"); then
  wtui flow phase set \
    --flow-id "$WTUI_FLOW_ID" \
    --phase-id plan \
    --status blocked \
    --outcome "plan_save_failed" \
    --notes "wtui plan save failed; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! wtui flow plan set \
    --flow-id "$WTUI_FLOW_ID" \
    --plan-id "$PLAN_ID" \
    "${FLOW_STATE_ARGS[@]}"; then
  wtui flow phase set \
    --flow-id "$WTUI_FLOW_ID" \
    --phase-id plan \
    --status blocked \
    --outcome "plan_link_failed" \
    --notes "wtui flow plan set failed for saved plan $PLAN_ID; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! wtui plan phase set \
    --plan-id "$PLAN_ID" \
    --phase-id implementation \
    --title "Implementation" \
    --status pending \
    --order 1 \
    "${PLAN_STATE_ARGS[@]}"; then
  wtui flow phase set \
    --flow-id "$WTUI_FLOW_ID" \
    --phase-id plan \
    --status blocked \
    --outcome "plan_phase_save_failed" \
    --notes "wtui plan phase set failed for saved plan $PLAN_ID; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! wtui plan read --plan-id "$PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null; then
  wtui flow phase set \
    --flow-id "$WTUI_FLOW_ID" \
    --phase-id plan \
    --status blocked \
    --outcome "plan_read_failed" \
    --notes "Saved plan $PLAN_ID could not be read back; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id plan \
  --status completed \
  --outcome "plan_saved" \
  --summary "Saved and linked plan $PLAN_ID." \
  "${FLOW_STATE_ARGS[@]}"
```

If plan persistence fails, mark the Flow phase `blocked` only if the blocking
phase update itself succeeds; otherwise report both failures.

## Plan Review Phase

Goal: review the saved plan before implementation.

Allowed outcomes are `approved`, `approved_with_concerns`,
`changes_requested`, and `blocked`.

Read the Flow first. Use `WTUI_PLAN_ID` when present; otherwise read the
`plan_id` field from `wtui flow read --flow-id "$WTUI_FLOW_ID"`. If you cannot
recover a plan ID, mark Plan Review `needs_attention` or `blocked` instead of
running `wtui plan read --plan-id ""`.

```bash
if ! FLOW_JSON=$(wtui flow read --flow-id "$WTUI_FLOW_ID" "${FLOW_STATE_ARGS[@]}"); then
  echo "wtui flow read failed; report the command error to the user." >&2
  exit 1
fi

if [ -z "$WTUI_PLAN_ID" ]; then
  if ! WTUI_PLAN_ID=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, sys; print(json.load(sys.stdin).get("plan_id", ""))'); then
    wtui flow phase set \
      --flow-id "$WTUI_FLOW_ID" \
      --phase-id plan-review \
      --status blocked \
      --outcome "plan_review_read_failed" \
      --notes "wtui flow read returned JSON that could not be parsed for plan_id; report the command error to the user." \
      "${FLOW_STATE_ARGS[@]}"
    exit 1
  fi
fi

if [ -z "$WTUI_PLAN_ID" ]; then
  wtui flow phase set \
    --flow-id "$WTUI_FLOW_ID" \
    --phase-id plan-review \
    --status needs_attention \
    --outcome "missing_plan_id" \
    --notes "Plan Review needs the plan ID from the completed Plan phase." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! wtui plan read --plan-id "$WTUI_PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null; then
  wtui flow phase set \
    --flow-id "$WTUI_FLOW_ID" \
    --phase-id plan-review \
    --status blocked \
    --outcome "plan_review_read_failed" \
    --notes "wtui plan read failed for $WTUI_PLAN_ID; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id plan-review \
  --status completed \
  --outcome "approved" \
  --summary "Plan is ready for implementation." \
  "${FLOW_STATE_ARGS[@]}"
```

Use `--status needs_attention --outcome "changes_requested"` when the plan
needs revision. Use `--status blocked --outcome "blocked" --notes "..."` when
human input or an external dependency is required.

## Implementation Phase

Goal: implement the reviewed plan in the Flow worktree.

TUI-launched Implementation phases provide `WTUI_FLOW_ID`,
`WTUI_FLOW_PHASE_ID=implementation`, `WTUI_PLAN_ID`, `WTUI_PLAN_PATH`,
`WTUI_WORKTREE_PATH`, and the shared state roots. Use `wtui plan read` when
`WTUI_PLAN_ID` is available, then implement and verify the requested behavior in
the Flow worktree. If the work splits into follow-up child phases, the current
CLI cannot add child phases yet; record the child phase IDs and instructions in
`--summary` or `--notes`, mark the phase `needs_attention` if the split needs
user orchestration, and tell the user structured child phase persistence is not
available in this CLI version.

```bash
wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id implementation \
  --status completed \
  --outcome "implemented" \
  --summary "Implemented the accepted plan and verified the target tests." \
  "${FLOW_STATE_ARGS[@]}"
```

Use `blocked` for missing requirements or unavailable services. Use
`needs_attention` for implementation concerns that should be reviewed before the
workflow proceeds. If verification or persistence fails, do not report
Implementation as completed; use `needs_attention` or `blocked` and include the
failure in `--summary` or `--notes`.

## Review Loop Phase

Goal: critique the implementation and drive revisions before PR creation.

Run the requested review loop. Record `completed` when blocking findings are
fixed, `needs_attention` when non-blocking concerns remain for the user, and
`blocked` when the branch cannot be reviewed or fixed.

```bash
wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id review-loop \
  --status completed \
  --outcome "passed" \
  --summary "Review loop passed after revisions." \
  "${FLOW_STATE_ARGS[@]}"
```

## PR Creation Phase

Goal: commit, push, and open or update the pull request.

After the PR exists, record the PR provider, number, URL, head branch, base
branch, and status in the phase `--summary` or `--notes` because the current
implemented CLI does not expose `wtui flow pr set`.

```bash
wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id pr-creation \
  --status completed \
  --outcome "pr_open" \
  --summary "PR github#123 https://github.com/owner/repo/pull/123 head=branch base=main status=open." \
  "${FLOW_STATE_ARGS[@]}"
```

If a PR cannot be created, use `blocked` with notes explaining what failed.

## Autoreview Phase

Goal: perform a second-level review against the PR or pushed branch.

Read the Flow first and verify PR metadata is present in the PR creation phase
summary or notes. If it is missing, use `blocked` or `needs_attention` and say
PR creation did not record enough metadata for autoreview.

```bash
wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id autoreview \
  --status completed \
  --outcome "passed" \
  --summary "Autoreview passed; no blocking findings remain." \
  "${FLOW_STATE_ARGS[@]}"
```

## Merge Phase

Goal: merge deliberately after review approval.

Do not merge silently. After the explicit merge action succeeds, record merge
status, commit, timestamp, and PR URL in `--summary` or `--notes` because the
current implemented CLI does not expose `wtui flow merge set`.

```bash
wtui flow phase set \
  --flow-id "$WTUI_FLOW_ID" \
  --phase-id merge \
  --status completed \
  --outcome "merged" \
  --summary "Merged PR github#123 at commit abc123 on 2026-06-07T00:00:00Z." \
  "${FLOW_STATE_ARGS[@]}"
```

If merge is unsafe, rejected, or waiting on CI, use `blocked` with notes.
