---
name: approach-plan-persist
description: Persist agent plans to Approach so they show up in the plans pane (mode 7). Use whenever a plan is created, revised, approved, started, completed, blocked, or superseded, and when recording per-phase progress.
---

# Persisting plans to Approach

`approach` stores saved plans next to captured agent sessions, under the shared
agent-artifact root (`$XDG_STATE_HOME/approach/sessions/v1/plans/...` or
`~/.local/state/approach/sessions/v1/plans/...`). Persisted plans appear in the
`approach` TUI under the plans pane (mode key `7`). This skill is instruction-only:
it tells you when and how to call the `approach plan` CLI.

## When to persist

Save or update the plan whenever its lifecycle changes:

- **created** — first time you write a plan → `approach plan save` (status `draft`).
- **revised** — you edited the plan body → `approach plan save` reusing the same `--plan-id`.
- **approved** — the user accepted the plan → `approach plan save --status approved`.
- **started** — you began executing → `approach plan save --status in_progress`.
- **completed** / **blocked** / **superseded** → `approach plan save --status <status>`.

Plan statuses: `draft`, `approved`, `in_progress`, `completed`, `blocked`,
`superseded`.

## How to persist the plan body

Pipe the full Markdown plan on stdin (or pass `--file`). `approach plan save` prints
only the generated (or reused) `plan_id` on success:

```bash
PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | approach plan save --title "Persist plans in approach")
```

Reuse the `plan_id` for every later edit so you update the same record instead of
creating duplicates:

```bash
printf '%s' "$UPDATED_MARKDOWN" | approach plan save --plan-id "$PLAN_ID" --status in_progress
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
approach plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store tracer bullet" --status completed --order 1
approach plan phase set --plan-id "$PLAN_ID" --phase-id cli   --title "CLI subcommands"      --status in_progress --order 2
```

Phase statuses: `pending`, `in_progress`, `completed`, `blocked`, `skipped`.
Re-running `phase set` with the same `--phase-id` updates that phase in place.

## Reading plans back

```bash
approach plan list --repo-path "$APPROACH_REPO_PATH" --json   # machine-readable list (requires --json)
approach plan read --plan-id "$PLAN_ID"                    # prints the plan Markdown only
```

## If persistence fails

If any `approach plan` command exits non-zero, tell the user the plan was not saved
(include the error). Do not silently continue as if it succeeded.
