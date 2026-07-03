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
- `WTUI_REPO_PATH`: absolute repository path, as wtui's scanner lists the repo
  (its **main worktree**, not a linked worktree). Derive it automatically when
  launch metadata does not provide it.

A normal repo-backed Flow must ship with worktree metadata. The next section
creates a dedicated branch and git worktree and populates `WTUI_WORKTREE_PATH`,
`WTUI_BRANCH`, `WTUI_BASE_REF`, and `WTUI_COMMIT` so the new Flow is
implementation-ready without manual repair. Worktree metadata is not optional
for normal repo-backed creation.

Derive `WTUI_REPO_PATH` from the current git checkout when launch metadata does
not provide it. wtui's scanner lists a repository by its main worktree, so a Flow
only surfaces under a repo when its `repo_path` equals that main-worktree path.
Resolve it that way — never a linked worktree path — so Flows created from any
repo (or from inside one of its worktrees) appear in wtui for that repo.

```bash
if [ -z "${WTUI_REPO_PATH:-}" ]; then
  # The first `git worktree list` entry is always the main worktree, which is the
  # path wtui's scanner discovers, even when run from inside a linked worktree.
  WTUI_REPO_PATH=$(git -C "${WTUI_WORKTREE_PATH:-$PWD}" worktree list --porcelain 2>/dev/null \
    | sed -n 's/^worktree //p' | head -n1) || WTUI_REPO_PATH=""
fi
case "${WTUI_REPO_PATH:-}" in
  /*) ;;
  *)
    echo "Could not resolve an absolute WTUI_REPO_PATH (the repo as wtui's scanner lists it); ask the user." >&2
    exit 1
    ;;
esac
```

`WTUI_BASE_REF` defaults to the repository's own default branch, detected from
`origin/HEAD` rather than a hard-coded `origin/main`, so repos on `master` or any
other default branch work; repos without a usable `origin` fall back to the local
HEAD branch. Set `WTUI_BASE_REF` explicitly only when the user asks to base the
Flow on a different ref. If `WTUI_REPO_PATH` is still missing, relative, or
unclear, ask the user instead of creating a malformed Flow.

Skip worktree creation only in two cases:

- The user explicitly asks for a metadata-only Flow (set `WTUI_FLOW_METADATA_ONLY=1`).
- The user provides an existing worktree to reuse (set `WTUI_WORKTREE_PATH`).
  `WTUI_BRANCH` and `WTUI_COMMIT` are derived from that worktree when unset, and
  `WTUI_BASE_REF` still defaults to the repository's default branch, so the reuse
  path also ships complete metadata.

## Create Or Reuse A Worktree

For a normal repo-backed Flow, fetch the base ref and create a dedicated branch
and worktree before running `wtui flow create`. This mirrors wtui's own
`flow/<slug>` branch at `<repo>-worktrees/flow-<slug>` convention. If worktree
creation fails, stop and report the command error instead of creating a partial
Flow.

```bash
# Resolve the base ref. Default to the repository's own default branch
# (origin/HEAD), falling back to the local HEAD branch when there is no usable
# origin, so repos on master or with no remote still ship complete metadata. Set
# WTUI_BASE_REF explicitly to override.
if [ -z "${WTUI_BASE_REF:-}" ]; then
  WTUI_BASE_REF=$(git -C "${WTUI_REPO_PATH:-}" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null) || WTUI_BASE_REF=""
  if [ -z "$WTUI_BASE_REF" ] && git -C "${WTUI_REPO_PATH:-}" remote get-url origin >/dev/null 2>&1; then
    git -C "${WTUI_REPO_PATH:-}" remote set-head origin --auto >/dev/null 2>&1 || true
    WTUI_BASE_REF=$(git -C "${WTUI_REPO_PATH:-}" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null) || WTUI_BASE_REF=""
  fi
  if [ -z "$WTUI_BASE_REF" ]; then
    WTUI_BASE_REF=$(git -C "${WTUI_REPO_PATH:-}" rev-parse --abbrev-ref HEAD 2>/dev/null) || WTUI_BASE_REF=""
  fi
fi

if [ -n "${WTUI_FLOW_METADATA_ONLY:-}" ]; then
  echo "Metadata-only Flow requested; skipping worktree creation." >&2
elif [ -n "${WTUI_WORKTREE_PATH:-}" ]; then
  echo "Reusing existing worktree ${WTUI_WORKTREE_PATH:-}." >&2
  # Derive any unset branch/commit from the reused worktree so the readback
  # gate below still finds complete metadata.
  if [ -z "${WTUI_BRANCH:-}" ]; then
    if ! WTUI_BRANCH=$(git -C "${WTUI_WORKTREE_PATH:-}" rev-parse --abbrev-ref HEAD); then
      echo "git rev-parse --abbrev-ref HEAD failed in ${WTUI_WORKTREE_PATH:-}; report the command error and do not create a partial Flow." >&2
      exit 1
    fi
  fi
  if [ -z "${WTUI_COMMIT:-}" ]; then
    if ! WTUI_COMMIT=$(git -C "${WTUI_WORKTREE_PATH:-}" rev-parse HEAD); then
      echo "git rev-parse HEAD failed in ${WTUI_WORKTREE_PATH:-}; report the command error and do not create a partial Flow." >&2
      exit 1
    fi
  fi
else
  case "${WTUI_REPO_PATH:-}" in
    /*) ;;
    *)
      echo "Worktree creation requires an absolute WTUI_REPO_PATH; ask the user for the repository path." >&2
      exit 1
      ;;
  esac

  # Fetch the base ref when it is a remote-tracking ref so the worktree starts
  # from current upstream state. A local base ref (no matching remote) skips the
  # fetch and branches from the local ref, so local-only repos still work.
  case "$WTUI_BASE_REF" in
    */*)
      BASE_REMOTE="${WTUI_BASE_REF%%/*}"
      BASE_BRANCH="${WTUI_BASE_REF#*/}"
      if git -C "${WTUI_REPO_PATH:-}" remote get-url "$BASE_REMOTE" >/dev/null 2>&1; then
        if ! git -C "${WTUI_REPO_PATH:-}" fetch "$BASE_REMOTE" "$BASE_BRANCH"; then
          echo "git fetch $BASE_REMOTE $BASE_BRANCH failed; report the command error and do not create a partial Flow." >&2
          exit 1
        fi
      fi
      ;;
  esac

  # Allocate a unique flow branch/worktree pair, bumping the suffix on collision.
  SLUG=$(printf '%s' "${FLOW_TITLE:-flow}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-' | sed 's/^-*//; s/-*$//')
  SLUG="${SLUG:-flow}"
  WORKTREES_DIR="$(dirname "${WTUI_REPO_PATH:-}")/$(basename "${WTUI_REPO_PATH:-}")-worktrees"
  WTUI_BRANCH="flow/$SLUG"
  WTUI_WORKTREE_PATH="$WORKTREES_DIR/flow-$SLUG"
  for i in $(seq 2 999); do
    if ! git -C "${WTUI_REPO_PATH:-}" show-ref --verify --quiet "refs/heads/$WTUI_BRANCH" && [ ! -e "$WTUI_WORKTREE_PATH" ]; then
      break
    fi
    WTUI_BRANCH="flow/$SLUG-$i"
    WTUI_WORKTREE_PATH="$WORKTREES_DIR/flow-$SLUG-$i"
  done

  mkdir -p "$WORKTREES_DIR"
  if ! git -C "${WTUI_REPO_PATH:-}" worktree add -b "$WTUI_BRANCH" "$WTUI_WORKTREE_PATH" "$WTUI_BASE_REF"; then
    echo "git worktree add failed; report the command error and do not create a partial Flow." >&2
    exit 1
  fi
  if ! WTUI_COMMIT=$(git -C "$WTUI_WORKTREE_PATH" rev-parse HEAD); then
    echo "git rev-parse HEAD failed in the new worktree; report the command error and do not create a partial Flow." >&2
    exit 1
  fi
fi
```

## Create And Verify The Flow

Prefer `--instructions-file` when the instructions are more than a short
sentence; use `--instructions` for compact task text. `wtui flow create --json`
prints machine-readable output containing `flow_id`. Set `FLOW_PRESET` only
when the user or surrounding workflow explicitly asks for a configured custom
phase graph; omitted uses `[flow].preset` or the built-in `default` preset.

```bash
if [ -z "${FLOW_TITLE:-}" ] || [ -z "${WTUI_REPO_PATH:-}" ]; then
  echo "Flow creation requires FLOW_TITLE and absolute WTUI_REPO_PATH; ask the user for missing values." >&2
  exit 1
fi
case "${WTUI_REPO_PATH:-}" in
  /*) ;;
  *)
    echo "Flow creation requires an absolute WTUI_REPO_PATH; ask the user for the repository path." >&2
    exit 1
    ;;
esac
if [ -z "${FLOW_INSTRUCTIONS_FILE:-}" ] && [ -z "${FLOW_INSTRUCTIONS:-}" ]; then
  echo "Flow creation requires FLOW_INSTRUCTIONS or FLOW_INSTRUCTIONS_FILE; ask the user for the task instructions." >&2
  exit 1
fi
FLOW_PRESET_ARGS=()
if [ -n "${FLOW_PRESET:-}" ]; then
  FLOW_PRESET_ARGS=(--preset "$FLOW_PRESET")
fi

if [ -n "${FLOW_INSTRUCTIONS_FILE:-}" ]; then
  if ! FLOW_JSON=$(wtui flow create \
    --title "${FLOW_TITLE:-}" \
    --instructions-file "${FLOW_INSTRUCTIONS_FILE:-}" \
    --repo-path "${WTUI_REPO_PATH:-}" \
    --worktree-path "${WTUI_WORKTREE_PATH:-}" \
    --branch "${WTUI_BRANCH:-}" \
    --base-ref "${WTUI_BASE_REF:-}" \
    --commit "${WTUI_COMMIT:-}" \
    "${FLOW_PRESET_ARGS[@]}" \
    --json \
    "${FLOW_STATE_ARGS[@]}"); then
    echo "wtui flow create failed; report the command error to the user." >&2
    exit 1
  fi
else
  if ! FLOW_JSON=$(wtui flow create \
    --title "${FLOW_TITLE:-}" \
    --instructions "${FLOW_INSTRUCTIONS:-}" \
    --repo-path "${WTUI_REPO_PATH:-}" \
    --worktree-path "${WTUI_WORKTREE_PATH:-}" \
    --branch "${WTUI_BRANCH:-}" \
    --base-ref "${WTUI_BASE_REF:-}" \
    --commit "${WTUI_COMMIT:-}" \
    "${FLOW_PRESET_ARGS[@]}" \
    --json \
    "${FLOW_STATE_ARGS[@]}"); then
    echo "wtui flow create failed; report the command error to the user." >&2
    exit 1
  fi
fi
if ! FLOW_ID=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, sys; print(json.load(sys.stdin)["flow_id"])'); then
  echo "wtui flow create returned JSON that could not be parsed for flow_id; report the command error to the user." >&2
  exit 1
fi
if ! wtui flow read --flow-id "$FLOW_ID" "${FLOW_STATE_ARGS[@]}" >/dev/null; then
  echo "wtui flow read failed for $FLOW_ID; report the command error to the user." >&2
  exit 1
fi
```

If any command in this section fails, report the command error and stop. Do not
say a Flow was created unless `wtui flow create` succeeded and `wtui flow read
--flow-id "$FLOW_ID"` verified it.

For a repo-backed Flow, also confirm the readback carries the worktree metadata
so you do not report an implementation-ready Flow that still needs manual
repair. Skip this check only for an explicit metadata-only Flow.

```bash
if [ -z "${WTUI_FLOW_METADATA_ONLY:-}" ]; then
  if ! wtui flow read --flow-id "${FLOW_ID:-}" "${FLOW_STATE_ARGS[@]}" \
    | python3 -c 'import json, sys
record = json.load(sys.stdin)
missing = [field for field in ("worktree_path", "branch", "base_ref", "commit") if not record.get(field)]
if missing:
    sys.stderr.write("flow read missing metadata: " + ", ".join(missing) + "\n")
    sys.exit(1)'; then
    echo "Flow ${FLOW_ID:-} is missing worktree metadata (worktree_path, branch, base_ref, commit); report this instead of claiming an implementation-ready Flow." >&2
    exit 1
  fi
fi
```

## Import An Existing Plan

If the current session already has a concrete plan body, save it and link it to
the new Flow. Do not invent a plan body just to satisfy this path.

```bash
if [ -z "${FLOW_ID:-}" ]; then
  echo "Plan import requires FLOW_ID from a verified wtui flow create result." >&2
  exit 1
fi
if [ -z "${PLAN_MARKDOWN:-}" ]; then
  echo "No concrete PLAN_MARKDOWN is available; report Flow ${FLOW_ID:-} and leave Plan ready." >&2
  exit 0
fi

record_plan_import_failure() {
  notes="${1:-}"
  if ! wtui flow phase block \
    --flow-id "$FLOW_ID" \
    --phase-id plan \
    --notes "$notes" \
    "${FLOW_STATE_ARGS[@]}"; then
    echo "wtui flow phase block failed after plan import failure; report both command errors to the user." >&2
  fi
}

if ! PLAN_ID=$(printf '%s' "${PLAN_MARKDOWN:-}" | wtui plan save \
  --title "${FLOW_TITLE:-}" \
  --status approved \
  --repo-path "${WTUI_REPO_PATH:-}" \
  --worktree-path "${WTUI_WORKTREE_PATH:-}" \
  --branch "${WTUI_BRANCH:-}" \
  --commit "${WTUI_COMMIT:-}" \
  "${PLAN_STATE_ARGS[@]}"); then
  record_plan_import_failure "wtui plan save failed; report the command error to the user."
  exit 1
fi

if ! wtui flow plan set \
  --flow-id "$FLOW_ID" \
  --plan-id "$PLAN_ID" \
  "${FLOW_STATE_ARGS[@]}"; then
  record_plan_import_failure "wtui flow plan set failed for plan $PLAN_ID; report the command error to the user."
  exit 1
fi

if ! wtui plan read --plan-id "$PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null; then
  record_plan_import_failure "wtui plan read failed for $PLAN_ID; report the command error to the user."
  exit 1
fi

if ! wtui flow phase complete \
  --flow-id "$FLOW_ID" \
  --phase-id plan \
  --summary "Imported plan $PLAN_ID." \
  "${FLOW_STATE_ARGS[@]}"; then
  record_plan_import_failure "wtui flow phase complete failed after importing plan $PLAN_ID; report the command error to the user."
  exit 1
fi
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
also succeeded. The import snippet above uses this recovery command:

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
