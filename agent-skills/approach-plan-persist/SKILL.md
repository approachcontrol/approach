---
name: approach-plan-persist
description: Persist agent plans to Approach so they show up in the plans pane (mode 7). Use whenever a plan is created, revised, approved, started, completed, blocked, or superseded, and when recording per-phase progress.
---

# Persisting plans to Approach

`approach` stores saved plans next to captured agent sessions, under the shared
agent-artifact root (`$XDG_STATE_HOME/approach/sessions/v1/plans/...` or
`~/.local/state/approach/sessions/v1/plans/...`; a development build of approach
substitutes `approach-dev` for `approach` there). Persisted plans appear in the
`approach` TUI under the plans pane (mode key `7`). This skill is instruction-only:
it tells you when and how to call the `$APPROACH_BIN plan` CLI.

## Resolve the approach binary first

`APPROACH_EXECUTABLE` pins the build that launched this agent. Resolve it before
any command below; without it the launcher and this agent can be different builds
and a save may fail at the database schema gate. If it is set but not runnable,
stop and report it — do not fall back to `approach` on `PATH`.

```bash
# Resolve the approach binary. APPROACH_EXECUTABLE pins the build that launched
# this agent; without it the launcher and this agent can be different builds, and
# a phase result may then be unpersistable.
#
# APPROACH_BIN is a shell variable, NOT an exported one, so it does not survive
# into a separate command invocation. That is why every approach call below
# spells `${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}` rather than
# `$APPROACH_BIN`: the expansion re-resolves in a fresh shell, so the COMMAND
# WORD is always a real binary.
#
# APPROACH_EXECUTABLE comes FIRST, and the order is the point. The launcher
# exports the pin; APPROACH_BIN is an ordinary name a user's shell profile may
# already export, and every launch inherits it. Resolving APPROACH_BIN first
# would let a stale ambient value silently outrank the pin — the mixed-build
# failure this whole block exists to stop.
#
# That covers the command word only. The blocks below still share PLAN_ID and
# PLAN_MARKDOWN, so run them in one shell, or re-establish those first. And the executability test lives here, not in the
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
```

## When to persist

Save or update the plan whenever its lifecycle changes:

- **created** — first time you write a plan → `$APPROACH_BIN plan save` (status `draft`).
- **revised** — you edited the plan body → `$APPROACH_BIN plan save` reusing the same `--plan-id`.
- **approved** — the user accepted the plan → `$APPROACH_BIN plan save --status approved`.
- **started** — you began executing → `$APPROACH_BIN plan save --status in_progress`.
- **completed** / **blocked** / **superseded** → `$APPROACH_BIN plan save --status <status>`.

Plan statuses: `draft`, `approved`, `in_progress`, `completed`, `blocked`,
`superseded`.

## How to persist the plan body

Pipe the full Markdown plan on stdin (or pass `--file`). `$APPROACH_BIN plan save` prints
only the generated (or reused) `plan_id` on success:

```bash
PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | "${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan save --title "Persist plans in approach")
```

Reuse the `plan_id` for every later edit so you update the same record instead of
creating duplicates:

```bash
printf '%s' "$UPDATED_MARKDOWN" | "${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan save --plan-id "$PLAN_ID" --status in_progress
```

`save` always replaces the Markdown and title from the command (both are
required). It updates `status`, `source`, `summary`, and repo/session metadata
only when you supply them, and otherwise preserves the stored values plus
`created_at` and recorded phases. So a body-only revise keeps the existing
status; pass `--status` whenever the lifecycle changes.

When Approach launched a CLI agent it exports `APPROACH_AGENT`, `APPROACH_LAUNCH_ID`,
`APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`, `APPROACH_BRANCH`, and `APPROACH_COMMIT`; the CLI
fills omitted metadata from those automatically, so you usually only need
`--title` (and `--plan-id` for edits).

## How to record phases

Record each phase explicitly as its status changes (v1 does not parse phases out
of the Markdown):

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store tracer bullet" --status completed --order 1
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan phase set --plan-id "$PLAN_ID" --phase-id cli   --title "CLI subcommands"      --status in_progress --order 2
```

Phase statuses: `pending`, `in_progress`, `completed`, `blocked`, `skipped`.
Re-running `phase set` with the same `--phase-id` updates that phase in place.

## Reading plans back

```bash
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan list --repo-path "$APPROACH_REPO_PATH" --json   # machine-readable list (requires --json)
"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" plan read --plan-id "$PLAN_ID"                    # prints the plan Markdown only
```

## If persistence fails

If any `$APPROACH_BIN plan` command exits non-zero, tell the user the plan was not saved
(include the error). Do not silently continue as if it succeeded.
