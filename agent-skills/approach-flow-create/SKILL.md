---
name: approach-flow-create
description: Create a new persisted Approach Flow from an ad hoc planning session or already-written plan, then optionally save and link the plan artifact.
---

# Creating Approach Flows

Use this skill when the user explicitly asks to turn the current work into an
Approach Flow, with phrasing such as "make a flow from this", "add this as an Approach
flow", or "create an Approach flow for this plan".

This skill is for ad hoc sessions. It does not require `APPROACH_FLOW_ID` or
`APPROACH_FLOW_PHASE_ID`; those belong to the `approach-flow` skill for agents already
launched inside an existing Flow phase. This skill can create a Flow and link a
saved plan, but v1 cannot attach the current provider session to the new Flow.
Do not claim the session is attached, and do not run `$APPROACH_BIN flow session attach`
unless a future CLI implements it.

## Start With Shared State

Build reusable state-root arguments before running commands. `$APPROACH_BIN flow` reads
`APPROACH_FLOW_STATE_ROOT`, and `$APPROACH_BIN plan` reads `APPROACH_PLAN_STATE_ROOT`, but passing
the same explicit root keeps created flows and imported plans together.

```bash
# Resolve the approach binary. APPROACH_EXECUTABLE pins the build that launched
# this agent; without it the launcher and this agent can be different builds, and
# a phase result may then be unpersistable.
#
# APPROACH_BIN is a shell variable, NOT an exported one, so it does not survive
# into a separate command invocation. That is why every approach call below
# spells `${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}` rather than
# `$APPROACH_BIN`: the `:=` re-resolves and assigns on first use, so the COMMAND
# WORD is always a real binary even in a fresh shell.
#
# That covers the command word only. The blocks below still share FLOW_ID,
# FLOW_STATE_ARGS, PLAN_STATE_ARGS and friends, so run them in one shell, or
# re-establish those first. And the executability test lives here, not in the
# expansion: a later block in a fresh shell whose APPROACH_EXECUTABLE names an
# evicted pin fails loudly on a missing binary rather than degrading to PATH.
# Report that error like any other; do not retry with a bare `approach`, which
# is how a wrong-build result gets persisted.
#
# A pin that WAS supplied but is unusable stops the workflow. Falling back to
# PATH there would run whatever build happens to be installed against a database
# the launcher owns, which is precisely the mixed-schema failure the pin exists
# to prevent, so an unusable pin is a persistence failure and is reported as one.
# Only a session that never received a pin at all uses PATH.
if [ -n "${APPROACH_EXECUTABLE:-}" ] && [ ! -x "$APPROACH_EXECUTABLE" ]; then
  echo "APPROACH_EXECUTABLE ($APPROACH_EXECUTABLE) is not runnable. Report this as a persistence failure and stop; do not fall back to approach on PATH, which may be a different build than the launcher." >&2
  exit 1
fi
APPROACH_BIN="${APPROACH_EXECUTABLE:-approach}"

APPROACH_ARTIFACT_ROOT="${APPROACH_FLOW_STATE_ROOT:-${APPROACH_PLAN_STATE_ROOT:-${APPROACH_SESSION_STATE_ROOT:-}}}"
FLOW_STATE_ARGS=()
PLAN_STATE_ARGS=()
if [ -n "$APPROACH_ARTIFACT_ROOT" ]; then
  FLOW_STATE_ARGS=(--state-root "$APPROACH_ARTIFACT_ROOT")
  PLAN_STATE_ARGS=(--state-root "$APPROACH_ARTIFACT_ROOT")
fi
```

## Resolve Required Metadata

Resolve Flow metadata from launch metadata first, then from the current git
checkout. Required values:

- `FLOW_TITLE`: short task title.
- `FLOW_INSTRUCTIONS` or `FLOW_INSTRUCTIONS_FILE`: the task instructions used to
  seed the Flow.
- `APPROACH_REPO_PATH`: absolute repository path, as Approach's scanner lists the repo
  (its **main worktree**, not a linked worktree). Derive it automatically when
  launch metadata does not provide it.

A normal repo-backed Flow must ship with worktree metadata. The next section
creates a dedicated branch and git worktree and populates `APPROACH_WORKTREE_PATH`,
`APPROACH_BRANCH`, `APPROACH_BASE_REF`, and `APPROACH_COMMIT` so the new Flow is
implementation-ready without manual repair. Worktree metadata is not optional
for normal repo-backed creation.

Derive `APPROACH_REPO_PATH` from the current git checkout when launch metadata does
not provide it. Approach's scanner lists a repository by its main worktree, so a Flow
only surfaces under a repo when its `repo_path` equals that main-worktree path.
Resolve it that way — never a linked worktree path — so Flows created from any
repo (or from inside one of its worktrees) appear in Approach for that repo.

```bash
if [ -z "${APPROACH_REPO_PATH:-}" ]; then
  # The first `git worktree list` entry is always the main worktree, which is the
  # path approach's scanner discovers, even when run from inside a linked worktree.
  APPROACH_REPO_PATH=$(git -C "${APPROACH_WORKTREE_PATH:-$PWD}" worktree list --porcelain 2>/dev/null \
    | sed -n 's/^worktree //p' | head -n1) || APPROACH_REPO_PATH=""
fi
case "${APPROACH_REPO_PATH:-}" in
  /*) ;;
  *)
    echo "Could not resolve an absolute APPROACH_REPO_PATH (the repo as approach's scanner lists it); ask the user." >&2
    exit 1
    ;;
esac
```

`APPROACH_BASE_REF` defaults to the repository's own default branch, detected from
`origin/HEAD` rather than a hard-coded `origin/main`, so repos on `master` or any
other default branch work; repos without a usable `origin` fall back to the local
HEAD branch. Set `APPROACH_BASE_REF` explicitly only when the user asks to base the
Flow on a different ref. If `APPROACH_REPO_PATH` is still missing, relative, or
unclear, ask the user instead of creating a malformed Flow.

Skip worktree creation only in two cases:

- The user explicitly asks for a metadata-only Flow (set `APPROACH_FLOW_METADATA_ONLY=1`).
  Such a Flow becomes worktree-backed on its first phase launch rather than
  running the agent in the repository root. Approach creates a `flow/<slug>` pair
  from the recorded base ref, or from the repository's current HEAD when no base
  ref was recorded. Set `APPROACH_BRANCH` only for a local branch that already
  exists and is not checked out in the repository itself: such a branch keeps its
  name and gets a worktree, or the Flow adopts the linked worktree it already
  has. A branch that does not exist yet is silently replaced by the
  `flow/<slug>` name; a branch checked out in the repository's own working tree,
  and a tag, remote-tracking ref, or raw commit, each refuse the launch outright.
- The user provides an existing worktree to reuse (set `APPROACH_WORKTREE_PATH`).
  `APPROACH_BRANCH` and `APPROACH_COMMIT` are derived from that worktree when unset, and
  `APPROACH_BASE_REF` still defaults to the repository's default branch, so the reuse
  path also ships complete metadata.

## Create Or Reuse A Worktree

For a normal repo-backed Flow, fetch the base ref and create a dedicated branch
and worktree before running `$APPROACH_BIN flow create`. This mirrors Approach's own
`flow/<slug>` branch at `<repo>-worktrees/flow-<slug>` convention. If worktree
creation fails, stop and report the command error instead of creating a partial
Flow.

```bash
# Resolve the base ref. Default to the repository's own default branch
# (origin/HEAD), falling back to the local HEAD branch when there is no usable
# origin, so repos on master or with no remote still ship complete metadata. Set
# APPROACH_BASE_REF explicitly to override.
if [ -z "${APPROACH_BASE_REF:-}" ]; then
  APPROACH_BASE_REF=$(git -C "${APPROACH_REPO_PATH:-}" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null) || APPROACH_BASE_REF=""
  if [ -z "$APPROACH_BASE_REF" ] && git -C "${APPROACH_REPO_PATH:-}" remote get-url origin >/dev/null 2>&1; then
    git -C "${APPROACH_REPO_PATH:-}" remote set-head origin --auto >/dev/null 2>&1 || true
    APPROACH_BASE_REF=$(git -C "${APPROACH_REPO_PATH:-}" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null) || APPROACH_BASE_REF=""
  fi
  if [ -z "$APPROACH_BASE_REF" ]; then
    APPROACH_BASE_REF=$(git -C "${APPROACH_REPO_PATH:-}" rev-parse --abbrev-ref HEAD 2>/dev/null) || APPROACH_BASE_REF=""
  fi
fi

if [ -n "${APPROACH_FLOW_METADATA_ONLY:-}" ]; then
  echo "Metadata-only Flow requested; skipping worktree creation." >&2
elif [ -n "${APPROACH_WORKTREE_PATH:-}" ]; then
  echo "Reusing existing worktree ${APPROACH_WORKTREE_PATH:-}." >&2
  # Derive any unset branch/commit from the reused worktree so the readback
  # gate below still finds complete metadata.
  if [ -z "${APPROACH_BRANCH:-}" ]; then
    if ! APPROACH_BRANCH=$(git -C "${APPROACH_WORKTREE_PATH:-}" rev-parse --abbrev-ref HEAD); then
      echo "git rev-parse --abbrev-ref HEAD failed in ${APPROACH_WORKTREE_PATH:-}; report the command error and do not create a partial Flow." >&2
      exit 1
    fi
  fi
  if [ -z "${APPROACH_COMMIT:-}" ]; then
    if ! APPROACH_COMMIT=$(git -C "${APPROACH_WORKTREE_PATH:-}" rev-parse HEAD); then
      echo "git rev-parse HEAD failed in ${APPROACH_WORKTREE_PATH:-}; report the command error and do not create a partial Flow." >&2
      exit 1
    fi
  fi
else
  case "${APPROACH_REPO_PATH:-}" in
    /*) ;;
    *)
      echo "Worktree creation requires an absolute APPROACH_REPO_PATH; ask the user for the repository path." >&2
      exit 1
      ;;
  esac

  # Fetch the base ref when it is a remote-tracking ref so the worktree starts
  # from current upstream state. A local base ref (no matching remote) skips the
  # fetch and branches from the local ref, so local-only repos still work.
  case "$APPROACH_BASE_REF" in
    */*)
      BASE_REMOTE="${APPROACH_BASE_REF%%/*}"
      BASE_BRANCH="${APPROACH_BASE_REF#*/}"
      if git -C "${APPROACH_REPO_PATH:-}" remote get-url "$BASE_REMOTE" >/dev/null 2>&1; then
        if ! git -C "${APPROACH_REPO_PATH:-}" fetch "$BASE_REMOTE" "$BASE_BRANCH"; then
          echo "git fetch $BASE_REMOTE $BASE_BRANCH failed; report the command error and do not create a partial Flow." >&2
          exit 1
        fi
      fi
      ;;
  esac

  # Allocate a unique flow branch/worktree pair, bumping the suffix on collision.
  SLUG=$(printf '%s' "${FLOW_TITLE:-flow}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-' | sed 's/^-*//; s/-*$//')
  SLUG="${SLUG:-flow}"
  WORKTREES_DIR="$(dirname "${APPROACH_REPO_PATH:-}")/$(basename "${APPROACH_REPO_PATH:-}")-worktrees"
  APPROACH_BRANCH="flow/$SLUG"
  APPROACH_WORKTREE_PATH="$WORKTREES_DIR/flow-$SLUG"
  for i in $(seq 2 999); do
    if ! git -C "${APPROACH_REPO_PATH:-}" show-ref --verify --quiet "refs/heads/$APPROACH_BRANCH" && [ ! -e "$APPROACH_WORKTREE_PATH" ]; then
      break
    fi
    APPROACH_BRANCH="flow/$SLUG-$i"
    APPROACH_WORKTREE_PATH="$WORKTREES_DIR/flow-$SLUG-$i"
  done

  mkdir -p "$WORKTREES_DIR"
  if ! git -C "${APPROACH_REPO_PATH:-}" worktree add -b "$APPROACH_BRANCH" "$APPROACH_WORKTREE_PATH" "$APPROACH_BASE_REF"; then
    echo "git worktree add failed; report the command error and do not create a partial Flow." >&2
    exit 1
  fi
  if ! APPROACH_COMMIT=$(git -C "$APPROACH_WORKTREE_PATH" rev-parse HEAD); then
    echo "git rev-parse HEAD failed in the new worktree; report the command error and do not create a partial Flow." >&2
    exit 1
  fi
fi
```

## Create And Verify The Flow

Prefer `--instructions-file` when the instructions are more than a short
sentence; use `--instructions` for compact task text. `$APPROACH_BIN flow create --json`
prints machine-readable output containing `flow_id`. Set `FLOW_PRESET` only
when the user or surrounding workflow explicitly asks for a configured custom
phase graph; omitted uses `[flow].preset` or the built-in `default` preset.

```bash
if [ -z "${FLOW_TITLE:-}" ] || [ -z "${APPROACH_REPO_PATH:-}" ]; then
  echo "Flow creation requires FLOW_TITLE and absolute APPROACH_REPO_PATH; ask the user for missing values." >&2
  exit 1
fi
case "${APPROACH_REPO_PATH:-}" in
  /*) ;;
  *)
    echo "Flow creation requires an absolute APPROACH_REPO_PATH; ask the user for the repository path." >&2
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
  if ! FLOW_JSON=$("${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow create \
    --title "${FLOW_TITLE:-}" \
    --instructions-file "${FLOW_INSTRUCTIONS_FILE:-}" \
    --repo-path "${APPROACH_REPO_PATH:-}" \
    --worktree-path "${APPROACH_WORKTREE_PATH:-}" \
    --branch "${APPROACH_BRANCH:-}" \
    --base-ref "${APPROACH_BASE_REF:-}" \
    --commit "${APPROACH_COMMIT:-}" \
    "${FLOW_PRESET_ARGS[@]}" \
    --json \
    "${FLOW_STATE_ARGS[@]}"); then
    echo "approach flow create failed; report the command error to the user." >&2
    exit 1
  fi
else
  if ! FLOW_JSON=$("${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow create \
    --title "${FLOW_TITLE:-}" \
    --instructions "${FLOW_INSTRUCTIONS:-}" \
    --repo-path "${APPROACH_REPO_PATH:-}" \
    --worktree-path "${APPROACH_WORKTREE_PATH:-}" \
    --branch "${APPROACH_BRANCH:-}" \
    --base-ref "${APPROACH_BASE_REF:-}" \
    --commit "${APPROACH_COMMIT:-}" \
    "${FLOW_PRESET_ARGS[@]}" \
    --json \
    "${FLOW_STATE_ARGS[@]}"); then
    echo "approach flow create failed; report the command error to the user." >&2
    exit 1
  fi
fi
if ! FLOW_ID=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, sys; print(json.load(sys.stdin)["flow_id"])'); then
  echo "approach flow create returned JSON that could not be parsed for flow_id; report the command error to the user." >&2
  exit 1
fi
if ! "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow read --flow-id "$FLOW_ID" "${FLOW_STATE_ARGS[@]}" >/dev/null; then
  echo "approach flow read failed for $FLOW_ID; report the command error to the user." >&2
  exit 1
fi
```

If any command in this section fails, report the command error and stop. Do not
say a Flow was created unless `$APPROACH_BIN flow create` succeeded and `$APPROACH_BIN flow read
--flow-id "$FLOW_ID"` verified it.

For a repo-backed Flow, also confirm the readback carries the worktree metadata
so you do not report an implementation-ready Flow that still needs manual
repair. Skip this check only for an explicit metadata-only Flow.

```bash
if [ -z "${APPROACH_FLOW_METADATA_ONLY:-}" ]; then
  if ! "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow read --flow-id "${FLOW_ID:-}" "${FLOW_STATE_ARGS[@]}" \
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
  echo "Plan import requires FLOW_ID from a verified 'approach flow create' result." >&2
  exit 1
fi
if [ -z "${PLAN_MARKDOWN:-}" ]; then
  echo "No concrete PLAN_MARKDOWN is available; report Flow ${FLOW_ID:-} and leave Plan ready." >&2
  exit 0
fi

if ! FLOW_PLAN_PHASE_ID=$(printf '%s' "$FLOW_JSON" | python3 -c 'import json, sys
record = json.load(sys.stdin)
phases = record.get("phases", [])
def semantic_kind(phase):
    kind = (phase.get("kind") or "").strip().lower()
    if kind:
        return kind
    return "plan" if (phase.get("phase_id") or "").strip().lower() == "plan" else ""
candidates = [phase for phase in phases if semantic_kind(phase) == "plan"]
ready = [phase for phase in candidates if phase.get("status") == "ready"]
picked = (ready or [{}])[0]
print(picked.get("phase_id", ""))'); then
  echo "approach flow create returned JSON that could not be parsed for a plan-kind phase; report the command error to the user." >&2
  exit 1
fi

record_plan_import_failure() {
  notes="${1:-}"
  if [ -z "${FLOW_PLAN_PHASE_ID:-}" ]; then
    echo "Plan import failed, and Flow $FLOW_ID has no plan-kind phase to mark blocked; report both facts to the user." >&2
    return 0
  fi
  if ! "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow phase block \
    --flow-id "$FLOW_ID" \
    --phase-id "$FLOW_PLAN_PHASE_ID" \
    --notes "$notes" \
    "${FLOW_STATE_ARGS[@]}"; then
    echo "approach flow phase block failed after plan import failure; report both command errors to the user." >&2
  fi
}

if ! PLAN_ID=$(printf '%s' "${PLAN_MARKDOWN:-}" | "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" plan save \
  --title "${FLOW_TITLE:-}" \
  --status approved \
  --repo-path "${APPROACH_REPO_PATH:-}" \
  --worktree-path "${APPROACH_WORKTREE_PATH:-}" \
  --branch "${APPROACH_BRANCH:-}" \
  --commit "${APPROACH_COMMIT:-}" \
  "${PLAN_STATE_ARGS[@]}"); then
  record_plan_import_failure "approach plan save failed; report the command error to the user."
  exit 1
fi

if ! "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow plan set \
  --flow-id "$FLOW_ID" \
  --plan-id "$PLAN_ID" \
  "${FLOW_STATE_ARGS[@]}"; then
  record_plan_import_failure "approach flow plan set failed for plan $PLAN_ID; report the command error to the user."
  exit 1
fi

if ! "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" plan read --plan-id "$PLAN_ID" "${PLAN_STATE_ARGS[@]}" >/dev/null; then
  record_plan_import_failure "approach plan read failed for $PLAN_ID; report the command error to the user."
  exit 1
fi

if [ -n "${FLOW_PLAN_PHASE_ID:-}" ]; then
  if ! "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow phase complete \
    --flow-id "$FLOW_ID" \
    --phase-id "$FLOW_PLAN_PHASE_ID" \
    --summary "Imported plan $PLAN_ID." \
    "${FLOW_STATE_ARGS[@]}"; then
    record_plan_import_failure "approach flow phase complete failed after importing plan $PLAN_ID; report the command error to the user."
    exit 1
  fi
else
  echo "Imported and linked plan $PLAN_ID; Flow $FLOW_ID has no plan-kind phase to complete, so leave it ready for its configured first phase." >&2
fi
```

Only complete the new Flow's ready plan-kind phase after all four steps succeed:
plan save, flow-plan link, plan readback, and phase completion. If the selected
preset has no ready plan-kind phase, link the plan but do not complete a Flow
phase.
If there is no concrete plan body, report the new Flow ID and leave Plan ready
for a normal Flow Plan launch.

## Persistence Failures

If any `$APPROACH_BIN flow` or `$APPROACH_BIN plan` command exits non-zero, report the command
error. These persistence failures must not be treated as success. Do not say a
Flow was created, a plan was saved, a plan was linked, or a phase was completed
unless the corresponding command succeeded.

When the Flow exists but imported-plan persistence fails, attempt to record the
failed import on the new Flow Plan phase and report whether that persistence
also succeeded. The import snippet above uses this recovery command:

```bash
"${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}" flow phase block \
  --flow-id "$FLOW_ID" \
  --phase-id "$FLOW_PLAN_PHASE_ID" \
  --notes "Plan import failed; report the approach command error to the user." \
  "${FLOW_STATE_ARGS[@]}"
```

Use `$APPROACH_BIN flow phase needs-attention --flow-id "$FLOW_ID" --phase-id
"$FLOW_PLAN_PHASE_ID" --notes "..." "${FLOW_STATE_ARGS[@]}"` instead when the
Flow can continue but the imported plan should be reviewed before
implementation. If this recovery update fails too, report both the original
command error and the recovery command error.
