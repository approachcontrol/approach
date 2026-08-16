---
name: approach-flow
description: Participate in an Approach Flow phase by reading the active flow, using approach plan/flow commands for persistence, and reporting any persistence failure instead of silently advancing workflow state.
---

# Approach Flow

Use this skill whenever both `APPROACH_FLOW_ID` and `APPROACH_FLOW_PHASE_ID` are set.
The persisted Flow record and the `$APPROACH_BIN flow` CLI are the source of truth; this
skill only explains how an agent should update them.

## Start Every Phase

Build reusable state-root arguments before running commands. `$APPROACH_BIN flow` reads
`APPROACH_FLOW_STATE_ROOT`, but `$APPROACH_BIN plan` does not; passing the same explicit root
keeps Flow and plan artifacts together when a launch prompt provides a shared
artifact root.

```bash
# Resolve the approach binary once. APPROACH_EXECUTABLE pins the build that
# launched this agent; without it the launcher and this agent can be different
# builds, and a phase result may then be unpersistable. The -x retest matters:
# `:-` only substitutes when the variable is *unset*, so a pinned path that was
# evicted or never materialized would hard-fail instead of degrading to PATH.
APPROACH_BIN="${APPROACH_EXECUTABLE:-approach}"
[ -x "$APPROACH_BIN" ] || APPROACH_BIN=approach

APPROACH_ARTIFACT_ROOT="${APPROACH_FLOW_STATE_ROOT:-${APPROACH_PLAN_STATE_ROOT:-${APPROACH_SESSION_STATE_ROOT:-}}}"
FLOW_STATE_ARGS=()
PLAN_STATE_ARGS=()
if [ -n "$APPROACH_ARTIFACT_ROOT" ]; then
  FLOW_STATE_ARGS=(--state-root "$APPROACH_ARTIFACT_ROOT")
  PLAN_STATE_ARGS=(--state-root "$APPROACH_ARTIFACT_ROOT")
fi

if ! FLOW_JSON=$("$APPROACH_BIN" flow read --flow-id "$APPROACH_FLOW_ID" "${FLOW_STATE_ARGS[@]}"); then
  echo "approach flow read failed; report the command error to the user." >&2
  exit 1
fi

APPROACH_CURRENT_PHASE_ID="${APPROACH_FLOW_PHASE_ID:-${APPROACH_PLAN_PHASE_ID:-}}"
if [ -z "$APPROACH_CURRENT_PHASE_ID" ]; then
  echo "APPROACH_FLOW_PHASE_ID is required; report the missing launch metadata to the user." >&2
  exit 1
fi
```

Also use the launch metadata when present: `APPROACH_FLOW_PHASE_ID`,
`APPROACH_PLAN_ID`, `APPROACH_PLAN_PATH`, `APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`,
`APPROACH_BRANCH`, `APPROACH_COMMIT`, `APPROACH_SESSION_STATE_ROOT`, and
`APPROACH_PLAN_STATE_ROOT`.

When mutating your own Flow phase, use `APPROACH_CURRENT_PHASE_ID` rather than a
hardcoded default preset phase ID. The default preset phase IDs are `plan`,
`plan-review`, `implementation`, `review-loop`, `pr-creation`, `autoreview`,
and `merge`, but custom presets may use different IDs for the same semantic
phase kinds.

Agent-facing phase statuses are `running`, `needs_attention`, `completed`,
`blocked`, and `skipped`. Report only the status of your own phase honestly;
Approach derives all phase readiness and ordering, so never reason about which
phase becomes ready next. It is fine to read the `next_phase` field returned by
the high-level phase action commands; do not infer that state yourself. Agents
cannot set `ready`. Skipped phases require `--notes`, and restarting a blocked
or needs-attention phase as `running` requires a rerun note; prefer
`$APPROACH_BIN flow phase restart`, which supplies a standard note when `--notes` is
omitted. Invalid transitions fail with the allowed next statuses; fix the
reported state rather than retrying blindly.

For plan-review-kind phases (the default preset phase ID is `plan-review`),
Approach accepts only these review outcomes:
`approved`, `approved_with_concerns`, `changes_requested`, and `blocked`.
`approved_with_concerns`, `changes_requested`, and `blocked` require
`--notes`.

Prefer the high-level phase action commands for common outcomes. They use the
same validation as `phase set`, persist the update, and print JSON with the
updated phase plus the next actionable phase state:

```bash
"$APPROACH_BIN" flow phase complete \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_FLOW_PHASE_ID" \
  --summary "What changed in this phase." \
  --notes "Optional audit notes." \
  "${FLOW_STATE_ARGS[@]}"
```

Use `$APPROACH_BIN flow phase block --notes "..."` for blockers and
`$APPROACH_BIN flow phase needs-attention --notes "..."` for non-blocking concerns.
For plan-review-kind phases, those wrappers fill default outcomes when omitted:
`complete` => `approved`, `needs-attention` => `changes_requested`, and
`block` => `blocked`. The `complete` wrapper can still take an explicit
Plan Review outcome such as `approved_with_concerns`. For Autoreview, the
wrappers fill `complete` => `passed`, `needs-attention` =>
`needs_attention`, and `block` => `blocked`. Use `$APPROACH_BIN flow phase restart`
for reruns, and use the lower-level `$APPROACH_BIN flow phase set` command for
`skipped` or other explicit status updates. `$APPROACH_BIN flow phase reset` is an
Approach-owned stale running recovery command for `await-session` or
`ended-session`; do not use it to report normal agent progress.

## Persistence Failures

If any `$APPROACH_BIN flow` or `$APPROACH_BIN plan` command exits non-zero, report the error to
the user. These persistence failures must not be treated as successful phase
progression. Do not say a phase advanced, a plan was saved, a PR was recorded,
or a merge was recorded unless the corresponding command succeeded.

The current Flow CLI exposes `create`, `list`, `read`, `phase complete`,
`phase block`, `phase needs-attention`, `phase restart`, `phase reset`, `phase set`,
`phase add-child`,
`plan set`, `issue set`, `pr set`, and `merge set`. Record merge metadata with
`$APPROACH_BIN flow merge set`; do not claim a merge was recorded unless that structured
command succeeds.

## Plan Phase

Goal: produce a saved approach plan artifact.

1. Read the flow.
2. Save or update the plan through `$APPROACH_BIN plan save`.
3. Link the saved plan artifact back to the Flow with `$APPROACH_BIN flow plan set`.
4. If the task references a GitHub issue, link it with `$APPROACH_BIN flow issue set`.
5. Record plan progress with `$APPROACH_BIN plan phase set` when the plan has phases.

When instructions give a full GitHub issue URL, use that URL directly. When
instructions give only `#N`, derive
`https://github.com/<owner>/<repo>/issues/<N>` only from an unambiguous GitHub
`origin` remote in the worktree repository. If no GitHub origin can be
established, do not persist a guessed issue URL; mention the ambiguity in the
Plan phase summary or notes instead.

```bash
"$APPROACH_BIN" flow issue set \
  --flow-id "$APPROACH_FLOW_ID" \
  --provider github \
  --number 123 \
  --url "https://github.com/owner/repo/issues/123" \
  "${FLOW_STATE_ARGS[@]}"
```
6. Complete or block the Flow phase with `$APPROACH_BIN flow phase complete` or
   `$APPROACH_BIN flow phase block`.

```bash
if ! PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | "$APPROACH_BIN" plan save \
    --title "$FLOW_TITLE" \
    --status approved \
    --repo-path "$APPROACH_REPO_PATH" \
    --worktree-path "$APPROACH_WORKTREE_PATH" \
    --branch "$APPROACH_BRANCH" \
    "${PLAN_STATE_ARGS[@]}"); then
  "$APPROACH_BIN" flow phase set \
    --flow-id "$APPROACH_FLOW_ID" \
    --phase-id "$APPROACH_CURRENT_PHASE_ID" \
    --status blocked \
    --outcome "plan_save_failed" \
    --notes "approach plan save failed; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! "$APPROACH_BIN" flow plan set \
    --flow-id "$APPROACH_FLOW_ID" \
    --plan-id "$PLAN_ID" \
    "${FLOW_STATE_ARGS[@]}"; then
  "$APPROACH_BIN" flow phase set \
    --flow-id "$APPROACH_FLOW_ID" \
    --phase-id "$APPROACH_CURRENT_PHASE_ID" \
    --status blocked \
    --outcome "plan_link_failed" \
    --notes "approach flow plan set failed for saved plan $PLAN_ID; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! APPROACH_PLAN_PHASE_EXPORTS=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, shlex, sys
record = json.load(sys.stdin)
def semantic_kind(phase):
    kind = (phase.get("kind") or "").strip().lower()
    if kind:
        return kind
    return "implementation" if (phase.get("phase_id") or "").strip().lower() == "implementation" else ""
phase = next((phase for phase in record.get("phases", []) if semantic_kind(phase) == "implementation"), {})
print("APPROACH_PLAN_NEXT_PHASE_ID=" + shlex.quote((phase.get("phase_id") or "").strip()))
print("APPROACH_PLAN_NEXT_PHASE_TITLE=" + shlex.quote((phase.get("title") or phase.get("phase_id") or "").strip()))'); then
  "$APPROACH_BIN" flow phase set \
    --flow-id "$APPROACH_FLOW_ID" \
    --phase-id "$APPROACH_CURRENT_PHASE_ID" \
    --status blocked \
    --outcome "plan_phase_discovery_failed" \
    --notes "approach flow read JSON could not be parsed for the next saved-plan phase; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi
eval "$APPROACH_PLAN_PHASE_EXPORTS"

if [ -n "${APPROACH_PLAN_NEXT_PHASE_ID:-}" ]; then
  if ! "$APPROACH_BIN" plan phase set \
      --plan-id "$PLAN_ID" \
      --phase-id "$APPROACH_PLAN_NEXT_PHASE_ID" \
      --title "${APPROACH_PLAN_NEXT_PHASE_TITLE:-$APPROACH_PLAN_NEXT_PHASE_ID}" \
      --status pending \
      --order 1 \
      "${PLAN_STATE_ARGS[@]}"; then
    "$APPROACH_BIN" flow phase set \
      --flow-id "$APPROACH_FLOW_ID" \
      --phase-id "$APPROACH_CURRENT_PHASE_ID" \
      --status blocked \
      --outcome "plan_phase_save_failed" \
      --notes "approach plan phase set failed for saved plan $PLAN_ID; report the command error to the user." \
      "${FLOW_STATE_ARGS[@]}"
    exit 1
  fi
fi

if ! "$APPROACH_BIN" plan read --plan-id "$PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null; then
  "$APPROACH_BIN" flow phase set \
    --flow-id "$APPROACH_FLOW_ID" \
    --phase-id "$APPROACH_CURRENT_PHASE_ID" \
    --status blocked \
    --outcome "plan_read_failed" \
    --notes "Saved plan $PLAN_ID could not be read back; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

"$APPROACH_BIN" flow phase complete \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
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

Read the Flow first. Use `APPROACH_PLAN_ID` when present; otherwise read the
`plan_id` field from `$APPROACH_BIN flow read --flow-id "$APPROACH_FLOW_ID"`. If you cannot
recover a plan ID, mark Plan Review `needs_attention` or `blocked` instead of
running `$APPROACH_BIN plan read --plan-id ""`.

```bash
if ! FLOW_JSON=$("$APPROACH_BIN" flow read --flow-id "$APPROACH_FLOW_ID" "${FLOW_STATE_ARGS[@]}"); then
  echo "approach flow read failed; report the command error to the user." >&2
  exit 1
fi

if [ -z "$APPROACH_PLAN_ID" ]; then
  if ! APPROACH_PLAN_ID=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, sys; print(json.load(sys.stdin).get("plan_id", ""))'); then
    "$APPROACH_BIN" flow phase set \
      --flow-id "$APPROACH_FLOW_ID" \
      --phase-id "$APPROACH_CURRENT_PHASE_ID" \
      --status blocked \
      --outcome "blocked" \
      --notes "approach flow read returned JSON that could not be parsed for plan_id; report the command error to the user." \
      "${FLOW_STATE_ARGS[@]}"
    exit 1
  fi
fi

if [ -z "$APPROACH_PLAN_ID" ]; then
  "$APPROACH_BIN" flow phase set \
    --flow-id "$APPROACH_FLOW_ID" \
    --phase-id "$APPROACH_CURRENT_PHASE_ID" \
    --status blocked \
    --outcome "blocked" \
    --notes "Plan Review needs the plan ID from the completed Plan phase." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

if ! "$APPROACH_BIN" plan read --plan-id "$APPROACH_PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null; then
  "$APPROACH_BIN" flow phase set \
    --flow-id "$APPROACH_FLOW_ID" \
    --phase-id "$APPROACH_CURRENT_PHASE_ID" \
    --status blocked \
    --outcome "blocked" \
    --notes "approach plan read failed for $APPROACH_PLAN_ID; report the command error to the user." \
    "${FLOW_STATE_ARGS[@]}"
  exit 1
fi

"$APPROACH_BIN" flow phase complete \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  --summary "Plan is ready for implementation." \
  "${FLOW_STATE_ARGS[@]}"
```

Use `$APPROACH_BIN flow phase needs-attention --notes "..."` when the plan needs
revision; the Plan Review outcome defaults to `changes_requested`. Use
`$APPROACH_BIN flow phase complete --outcome "approved_with_concerns" --notes "..."`
when implementation may proceed but should carry the noted concern forward. Use
`$APPROACH_BIN flow phase block --notes "..."` when human input, missing plan context,
or an external dependency prevents review; the Plan Review outcome defaults to
`blocked`.

## Implementation Phase

Goal: implement the reviewed plan in the Flow worktree.

TUI-launched implementation-kind phases provide `APPROACH_FLOW_ID`,
`APPROACH_FLOW_PHASE_ID` (default preset: `implementation`), `APPROACH_PLAN_ID`,
`APPROACH_PLAN_PATH`, `APPROACH_WORKTREE_PATH`, and the shared state roots. Use
`$APPROACH_BIN plan read` when `APPROACH_PLAN_ID` is available, then implement and verify the
requested behavior in the Flow worktree. If the work splits into follow-up
child phases under an implementation-kind parent, create stable ordered children
before advancing downstream phases:

```bash
"$APPROACH_BIN" flow phase add-child \
  --flow-id "$APPROACH_FLOW_ID" \
  --parent-phase-id "$APPROACH_FLOW_PHASE_ID" \
  --phase-id implementation-api \
  --title "API integration" \
  --order 10 \
  "${FLOW_STATE_ARGS[@]}"
```

Re-running the same `phase add-child` command updates the existing child phase
instead of duplicating it. Complete or skip (with notes) each child phase when
its work is done.

```bash
"$APPROACH_BIN" flow phase set \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
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
"$APPROACH_BIN" flow phase set \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  --status completed \
  --outcome "completed" \
  --summary "Review loop passed after revisions." \
  "${FLOW_STATE_ARGS[@]}"
```

## PR Creation Phase

Goal: commit, push, and open or update the pull request.

After the PR exists, record the PR provider, positive PR number, URL, head
branch, base branch, and status through `$APPROACH_BIN flow pr set`. Recording this
structured PR metadata is a required part of PR Creation, not optional
bookkeeping. The command currently supports `--provider github`; the PR head
branch must match the Flow branch.

```bash
"$APPROACH_BIN" flow pr set \
  --flow-id "$APPROACH_FLOW_ID" \
  --provider github \
  --number 123 \
  --url "https://github.com/owner/repo/pull/123" \
  --head "$APPROACH_BRANCH" \
  --base main \
  --status open \
  "${FLOW_STATE_ARGS[@]}"

"$APPROACH_BIN" flow phase set \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  --status completed \
  --outcome "pr_open" \
  --summary "Opened GitHub PR #123." \
  "${FLOW_STATE_ARGS[@]}"
```

If `$APPROACH_BIN flow pr set` fails, do not mark PR Creation completed; report the
command error. If a PR cannot be created, use `blocked` with notes explaining
what failed.

## Autoreview Phase

Goal: perform a second-level review against the PR or pushed branch.

Read the Flow first and verify the top-level `pr` object contains provider,
number, URL, head branch, and base branch. If PR metadata is missing, do not run
Autoreview and do not try to advance the pending Autoreview phase. Return to PR
Creation by recording the missing metadata with `$APPROACH_BIN flow pr set`; if a PR does
not exist or cannot be recovered, rerun PR Creation as `running` with notes and
then mark PR Creation `blocked` with notes.

If Autoreview is already `needs_attention` or `blocked`, do not mark it
`completed` directly. First restart the phase as `running`, then
complete it after the rerun succeeds:

```bash
"$APPROACH_BIN" flow phase restart \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  "${FLOW_STATE_ARGS[@]}"
```

```bash
"$APPROACH_BIN" flow phase complete \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  --summary "Autoreview passed; no blocking findings remain." \
  "${FLOW_STATE_ARGS[@]}"
```

## Merge Phase

Goal: resolve conflicts and merge a single pr deliberately.

Do not merge silently. Read the Flow first and verify the top-level `pr` object
contains provider, number, URL, head branch, and base branch. After the explicit
merge action succeeds, complete the Merge phase, then record the structured
merge status, commit, and RFC3339 timestamp through `$APPROACH_BIN flow merge set`. Both
commands must succeed before reporting the Flow as merged.

```bash
"$APPROACH_BIN" flow phase set \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  --status completed \
  --outcome "merged" \
  --summary "Merged PR github#123 at commit $MERGE_COMMIT." \
  "${FLOW_STATE_ARGS[@]}"

"$APPROACH_BIN" flow merge set \
  --flow-id "$APPROACH_FLOW_ID" \
  --status merged \
  --commit "$MERGE_COMMIT" \
  --merged-at "$MERGED_AT_RFC3339" \
  "${FLOW_STATE_ARGS[@]}"
```

If merge is unsafe, rejected, or waiting on CI, use `blocked` with notes, then
record the structured blocked merge status:

```bash
"$APPROACH_BIN" flow phase set \
  --flow-id "$APPROACH_FLOW_ID" \
  --phase-id "$APPROACH_CURRENT_PHASE_ID" \
  --status blocked \
  --outcome "blocked" \
  --notes "Explain why merge is blocked." \
  "${FLOW_STATE_ARGS[@]}"

"$APPROACH_BIN" flow merge set \
  --flow-id "$APPROACH_FLOW_ID" \
  --status blocked \
  "${FLOW_STATE_ARGS[@]}"
```
