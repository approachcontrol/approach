---
name: wtui-plan-persist
description: Persist agent plans to wtui so they show up in the plans pane (mode 7). Use whenever a plan is created, revised, approved, started, completed, blocked, or superseded, and when recording per-phase progress.
---

# Persisting plans to wtui

`wtui` stores saved plans next to captured agent sessions, under the shared
agent-artifact root (`$XDG_STATE_HOME/wtui/sessions/v1/plans/...` or
`~/.local/state/wtui/sessions/v1/plans/...`). Persisted plans appear in the
`wtui` TUI under the plans pane (mode key `7`). This skill is instruction-only:
it tells you when and how to call the `wtui plan` CLI.

## When to persist

Save or update the plan whenever its lifecycle changes:

- **created** — first time you write a plan → `wtui plan save` (status `draft`).
- **revised** — you edited the plan body → `wtui plan save` reusing the same `--plan-id`.
- **approved** — the user accepted the plan → `wtui plan save --status approved`.
- **started** — you began executing → `wtui plan save --status in_progress`.
- **completed** / **blocked** / **superseded** → `wtui plan save --status <status>`.

Plan statuses: `draft`, `approved`, `in_progress`, `completed`, `blocked`,
`superseded`.

## How to persist the plan body

Pipe the full Markdown plan on stdin (or pass `--file`). `wtui plan save` prints
only the generated (or reused) `plan_id` on success:

```bash
PLAN_ID=$(printf '%s' "$PLAN_MARKDOWN" | wtui plan save --title "Persist plans in wtui")
```

Reuse the `plan_id` for every later edit so you update the same record instead of
creating duplicates:

```bash
printf '%s' "$UPDATED_MARKDOWN" | wtui plan save --plan-id "$PLAN_ID" --status in_progress
```

`save` always replaces the Markdown and title from the command (both are
required). It updates `status`, `source`, `summary`, and repo/session metadata
only when you supply them, and otherwise preserves the stored values plus
`created_at` and recorded phases. So a body-only revise keeps the existing
status; pass `--status` whenever the lifecycle changes.

When wtui launched the agent it exports `WTUI_AGENT`, `WTUI_LAUNCH_ID`,
`WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, and `WTUI_COMMIT`; the CLI
fills omitted metadata from those automatically, so you usually only need
`--title` (and `--plan-id` for edits).

## How to record phases

Record each phase explicitly as its status changes (v1 does not parse phases out
of the Markdown):

```bash
wtui plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store tracer bullet" --status completed --order 1
wtui plan phase set --plan-id "$PLAN_ID" --phase-id cli   --title "CLI subcommands"      --status in_progress --order 2
```

Phase statuses: `pending`, `in_progress`, `completed`, `blocked`, `skipped`.
Re-running `phase set` with the same `--phase-id` updates that phase in place.

## Reading plans back

```bash
wtui plan list --repo-path "$WTUI_REPO_PATH" --json   # machine-readable list (requires --json)
wtui plan read --plan-id "$PLAN_ID"                    # prints the plan Markdown only
```

## If persistence fails

If any `wtui plan` command exits non-zero, tell the user the plan was not saved
(include the error). Do not silently continue as if it succeeded.
