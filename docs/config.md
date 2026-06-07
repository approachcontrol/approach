# wtui Configuration

wtui reads an optional TOML config file before scanning repositories.

## Config Path

wtui looks for config in this order:

1. `$XDG_CONFIG_HOME/wtui/config.toml`
2. `~/.config/wtui/config.toml`

The file is optional. If it does not exist, wtui starts with built-in defaults.
If a config file exists but cannot be read or parsed, startup fails before
repository scanning. wtui only falls through to the next path when the earlier
path does not exist.

## Precedence

Environment variables remain the highest-precedence settings where they already
exist:

| Setting | Highest precedence | Config fallback | Built-in default |
|---------|--------------------|-----------------|------------------|
| Scan root | `WORKTREE_ROOT` | `[scan].root` | `~/dev` |
| Terminal command | `TERMINAL` | none; `[terminal].command` is parsed but unused | platform fallback |
| Coding agent | none | `[agent].command` | unset |
| Plan launch prompt | none | `[agent].plan_prompt` | built-in plan implementation prompt |
| TUI artifact root | `WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | `$XDG_STATE_HOME/wtui/sessions/v1` or `~/.local/state/wtui/sessions/v1` |
| Session hook root | `--state-root` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root |
| Plan state root | `--state-root` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/plans/...`) |
| Flow state root | `--state-root` > `WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` | `[sessions].root` | same as sessions root (`<root>/flows/...`) |
| Bootstrap hook timeout | none | `[bootstrap].timeout_seconds` or hook override | `120` seconds |

`[scan].root` and `[sessions].root` support `~` and `~/...` expansion.
Session roots must resolve to absolute paths so captured transcripts stay out of
repositories.
`WORKTREE_ROOT` is passed through as provided by the environment.

## Example

```toml
[scan]
root = "~/dev"
max_depth = 2

[editor]
command = "code"

[terminal]
command = "wezterm start"

[provider]
name = "github"

[launch]
prefer_multiplexer = true

[agent]
command = "codex"
plan_prompt = "Implement the saved wtui plan {title} (ID: {plan_id}) at {plan_path}. Read the plan file, then begin implementation."

[sessions]
root = "~/.local/state/wtui/sessions/v1"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 120

[[bootstrap.hooks]]
repo_path = "~/dev/wtui"
script = ".wtui/bootstrap"

[[bootstrap.hooks]]
repo_path = "~/dev/client-api"
script = "~/bin/bootstrap-client-api"
timeout_seconds = 300
```

## Sections

### `[scan]`

Controls repository discovery.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Directory to scan for git repositories. |
| `max_depth` | integer | Scan depth below `root`; `1` scans immediate children, `2` also scans one level deeper. |

When `max_depth` is omitted or set to `0`, wtui uses the scanner default of `2`.
Values greater than `2` behave like `2`.

### `[editor]`

Parsed for future editor-launch settings. The current editor action still opens
VS Code with `code`.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Future editor command setting. |

### `[terminal]`

Parsed for future terminal-launch settings. The current terminal action still
uses tmux/Zellij detection, then `TERMINAL`, then platform fallbacks.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Future terminal command setting. |

### `[provider]`

Parsed for future provider-specific features.

| Key | Type | Description |
|-----|------|-------------|
| `name` | string | Future provider name, such as `github`. |

### `[launch]`

Parsed for future launch behavior.

| Key | Type | Description |
|-----|------|-------------|
| `prefer_multiplexer` | boolean | Future launch preference for tmux/Zellij behavior. |

### `[agent]`

Stores the selected coding agent for interactive launches. Pressing `A` in wtui
updates this value immediately, creating the config file if needed.

| Key | Type | Description |
|-----|------|-------------|
| `command` | string | Supported values: `codex`, `codex-app`, or `claude`. |
| `plan_prompt` | string | Optional one-line template for the editable instructions opened by `i` in the plans pane. Supports `{title}`, `{plan_id}`, `{plan_path}`, `{repo_path}`, and `{worktree_path}`. Unknown placeholders remain literal. Blank or omitted uses the built-in prompt. |

### `[sessions]`

Controls portable agent-session storage. Session metadata and normalized
transcripts are stored outside repositories by default. Each provider session is
stored under a hashed session directory, with the raw provider session ID kept in
`meta.json`.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Optional absolute state root for session files. Supports `~` expansion. |
| `copy_raw_transcripts` | boolean | Whether hook ingestion also preserves provider-native transcript JSONL as `raw.jsonl`. Defaults to `false`. |

When `root` is omitted, wtui uses `$XDG_STATE_HOME/wtui/sessions/v1`, or
`~/.local/state/wtui/sessions/v1` when `XDG_STATE_HOME` is unset.
Relative roots other than `~`/`~/...` fail config parsing.

`[sessions].root` doubles as the **agent-artifact root**: sessions, saved plans,
and Flow records are stored under `<root>/sessions/...`,
`<root>/plans/<plan-id>/`, and `<root>/flows/<flow-id>/`. There is no separate
plans or flows config in v1. **Moving or cleaning the sessions root therefore
also moves or removes saved plans and Flow records.**

## Saved Plans

Agents persist plans explicitly through the `wtui plan` subcommands; plans are
not captured from provider hooks in v1. Each plan is stored as
`<artifact-root>/plans/<plan-id>/meta.json` plus `plan.md`, with the same
restrictive permissions (`0700` directories, `0600` files) and atomic writes as
sessions. They appear in the TUI plans pane (mode `7`).

```bash
# Save or update (reuse --plan-id) a plan; Markdown comes from --file or stdin.
# Prints only the plan_id.
printf '%s' "$PLAN_MD" | wtui plan save --title "Persist plans" [--plan-id ID] \
    [--summary TEXT] [--status STATUS] [--source SOURCE] [--provider PROVIDER] \
    [--session-id ID] [--launch-id ID] [--repo-path PATH] [--worktree-path PATH] \
    [--branch BRANCH] [--commit HASH] [--file PATH] [--state-root PATH]

wtui plan phase set --plan-id ID --phase-id ID --title TITLE --status STATUS [--order N] [--state-root PATH]
wtui plan list [--repo-path PATH] [--state-root PATH] --json   # --json required in v1
wtui plan read --plan-id ID [--state-root PATH]                # prints Markdown only
```

Plan statuses: `draft`, `approved`, `in_progress`, `completed`, `blocked`,
`superseded`. Phase statuses: `pending`, `in_progress`, `completed`, `blocked`,
`skipped`. `plan_id` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`; when
omitted, wtui generates `YYYYMMDDTHHMMSSZ-<title-slug>` with a `-2`, `-3`, …
suffix on collision.

`save` always replaces Markdown and title from the command (both required), and
updates `status`, `source`, `summary`, and repo/session metadata only when
supplied; otherwise it preserves the stored values, `created_at`, and recorded
phases. A body-only re-save keeps the existing status.

The plan state root is resolved as: `--state-root` > `WTUI_PLAN_STATE_ROOT` >
`WTUI_SESSION_STATE_ROOT` > `[sessions].root` > the user state default. The
`wtui plan` commands may load config to resolve the root but never scan repos or
start the TUI. Omitted metadata is filled from `WTUI_AGENT` (provider),
`WTUI_LAUNCH_ID`, `WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, and
`WTUI_COMMIT`; for new plans, and for updates that provide a repo or worktree
location, wtui also resolves best-effort repo, worktree, branch, and commit
metadata from git. `codex-app` launches do not inherit `WTUI_*` because wtui
opens a macOS deep link; use the metadata block in the launch prompt to pass
`--state-root` or export the listed vars before running `wtui plan`. The
`wtui-plan-persist` skill instructs agents on when and how to save plans; its
canonical source lives in
`agent-skills/wtui-plan-persist/` for symlinking into user-level Codex/Claude
skill directories.

## Flows

Flow records are task-centric workflow records created explicitly through
`wtui flow`. Each record is stored as
`<artifact-root>/flows/<flow-id>/meta.json`, with restrictive permissions
(`0700` directories, `0600` files) and atomic writes. They appear in the TUI
flows pane (mode `8`), which is browse/filter only in v1.

```bash
# Create a flow. --repo-path must be absolute, instructions are required, and
# --json is required in v1.
wtui flow create --title "Ship saved plans" \
    --instructions "Plan, implement, review, open a PR, and merge." \
    --repo-path "$REPO" [--worktree-path PATH] [--branch BRANCH] \
    [--base-ref REF] [--commit HASH] [--state-root PATH] --json

# You may also read instructions from a file.
wtui flow create --title "Ship saved plans" \
    --instructions-file ./instructions.md --repo-path "$REPO" --json

wtui flow list [--repo-path PATH] [--state-root PATH] --json
wtui flow read --flow-id ID [--state-root PATH]
```

Flow IDs use the same safe single-path-segment shape as plans:
`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. Generated IDs use
`YYYYMMDDTHHMMSSZ-<title-slug>` with a numeric suffix on collision. New flows
start with a default phase graph: plan, plan review, implementation, review
loop, PR creation, autoreview, and merge.

Flow statuses are derived from phase and merge state. Flow statuses include
`pending`, `in_progress`, `needs_attention`, `blocked`, `completed`, `merged`,
and `abandoned`. Phase statuses include `pending`, `ready`, `running`,
`needs_attention`, `completed`, `blocked`, and `skipped`.

The flow state root is resolved as: `--state-root` >
`WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` >
`[sessions].root` > the user state default. In TUI startup,
`WTUI_FLOW_STATE_ROOT` has highest precedence for the shared artifact root; if
it is set, the TUI reads sessions, plans, and flows from that root.

### `[bootstrap]`

Configures optional per-repo scripts that run after wtui successfully creates a
worktree with `n`, `P`, or `N`. Hooks are opt-in and are matched by configured
repo path. wtui does not auto-discover scripts from scanned repositories.

| Key | Type | Description |
|-----|------|-------------|
| `timeout_seconds` | integer | Default hook timeout. Omitted or `0` means `120`; negative values fail startup. |

Add one `[[bootstrap.hooks]]` entry per repo:

| Key | Type | Description |
|-----|------|-------------|
| `repo_path` | string | Required repo path to match. Supports `~` expansion. |
| `script` | string | Required script path. Relative paths resolve from the newly created worktree; `~` paths are expanded. |
| `timeout_seconds` | integer | Optional per-hook timeout override; negative values fail startup. |

Bootstrap scripts execute directly, not through a shell. The script file must
exist, be a regular file, and be executable. wtui sets the script working
directory to the new worktree and appends these environment variables:
`WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`, `WTUI_WORKTREE_REF`, and
`WTUI_WORKTREE_CREATE_KIND`.

If a hook fails, wtui keeps the created worktree and branch, refreshes the
worktree list, and shows the hook error in the status bar. For `N`, a hook
failure prevents automatic agent launch; the agent can still be launched
manually afterward.

## Agent Session Hooks

Agents launched from wtui with `a`, `N`, or session resume `r` are wired
automatically. wtui passes Claude Code or Codex a session-end hook that calls
the current wtui binary, and it appends the environment metadata listed below so
the hook can associate the session with the selected repo and worktree.

wtui can also ingest hook payloads from manual provider configuration:

```bash
wtui session-hook --provider claude
wtui session-hook --provider codex
```

For development and tests, pass an explicit state root:

```bash
wtui session-hook --provider codex --state-root /tmp/wtui-sessions-test
```

`session-hook` loads the normal wtui config before ingesting the hook payload.
`--state-root` overrides `[sessions].root`, and `WTUI_SESSION_STATE_ROOT`
overrides the configured root when `--state-root` is omitted. The
`copy_raw_transcripts` setting controls whether provider-native transcript data
is copied to `raw.jsonl`; it is off by default, and normalized transcript events
are still written for the sessions view.

Codex may ask you to review and trust the injected hook with `/hooks` before it
runs it. After trust is recorded for the unchanged hook command, later
wtui-launched Codex sessions can save normally.

Claude Code hook example:

```json
{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "wtui session-hook --provider claude"
          }
        ]
      }
    ]
  }
}
```

Codex hook example:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "wtui session-hook --provider codex"
          }
        ]
      }
    ]
  }
}
```

When wtui launches or resumes a CLI agent session, it appends these environment
variables:
`WTUI_AGENT`, `WTUI_LAUNCH_ID`, `WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`,
`WTUI_BRANCH`, `WTUI_COMMIT`, and `WTUI_SESSION_STATE_ROOT`.
`codex-app` launches are the exception: wtui opens a macOS deep link, scrubs
inherited `WTUI_*` from `open`, and includes launch metadata in the prompt when a
prompt is provided.

Session resume uses the stored provider session ID. Codex resumes with
`codex ... resume <session-id>` and Claude Code resumes with
`claude ... --resume <session-id>`, while preserving the same wtui hook and
metadata environment wiring as fresh launches.

For Codex hook payloads with `hook_event_name = "Stop"`, wtui records the
session as ended. Claude hook ingestion also records ended sessions, using the
payload end time when present and the current time as a fallback.

Transcripts can contain secrets, credentials, private prompts, and proprietary
code. Keep the sessions root in user-private storage and avoid committing
captured transcript files.
