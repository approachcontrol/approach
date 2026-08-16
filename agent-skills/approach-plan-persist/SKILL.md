---
name: approach-plan-persist
description: Persist agent plans to Approach so they show up in the plans pane (mode 7). Use whenever a plan is created, revised, approved, started, completed, blocked, or superseded, and when recording per-phase progress.
---

# Persisting plans to Approach

`approach` stores saved plans next to captured agent sessions, under the shared
agent-artifact root (`$XDG_STATE_HOME/approach/sessions/v1/plans/...` or
`~/.local/state/approach/sessions/v1/plans/...`). Persisted plans appear in the
`approach` TUI under the plans pane (mode key `7`). This skill is instruction-only:
it tells you when and how to call the `$APPROACH_BIN plan` CLI.

## Resolve the approach binary first

`APPROACH_EXECUTABLE` pins the build that launched this agent. Resolve it once,
before any command below; without it the launcher and this agent can be
different builds and a save may fail at the database schema gate. The `-x`
retest matters: `:-` only substitutes when the variable is *unset*, so a pinned
path that was evicted or never materialized would hard-fail instead of degrading
to `PATH`.

```bash
APPROACH_BIN="${APPROACH_EXECUTABLE:-approach}"
[ -x "$APPROACH_BIN" ] || APPROACH_BIN=approach
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
PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | "$APPROACH_BIN" plan save --title "Persist plans in approach")
```

Reuse the `plan_id` for every later edit so you update the same record instead of
creating duplicates:

```bash
printf '%s' "$UPDATED_MARKDOWN" | "$APPROACH_BIN" plan save --plan-id "$PLAN_ID" --status in_progress
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
"$APPROACH_BIN" plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store tracer bullet" --status completed --order 1
"$APPROACH_BIN" plan phase set --plan-id "$PLAN_ID" --phase-id cli   --title "CLI subcommands"      --status in_progress --order 2
```

Phase statuses: `pending`, `in_progress`, `completed`, `blocked`, `skipped`.
Re-running `phase set` with the same `--phase-id` updates that phase in place.

## Reading plans back

```bash
"$APPROACH_BIN" plan list --repo-path "$APPROACH_REPO_PATH" --json   # machine-readable list (requires --json)
"$APPROACH_BIN" plan read --plan-id "$PLAN_ID"                    # prints the plan Markdown only
```

## If persistence fails

If any `$APPROACH_BIN plan` command exits non-zero, tell the user the plan was not saved
(include the error). Do not silently continue as if it succeeded.
