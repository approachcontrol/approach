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
| Sessions root | `WTUI_SESSION_STATE_ROOT` for hooks | `[sessions].root` | `$XDG_STATE_HOME/wtui/sessions/v1` or `~/.local/state/wtui/sessions/v1` |
| Bootstrap hook timeout | none | `[bootstrap].timeout_seconds` or hook override | `120` seconds |

`[scan].root` and `[sessions].root` support `~` and `~/...` expansion.
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

[sessions]
root = "~/.local/state/wtui/sessions/v1"
copy_raw_transcripts = true

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
| `command` | string | Supported values: `codex` or `claude`. |

### `[sessions]`

Controls portable agent-session storage. Session metadata and normalized
transcripts are stored outside repositories by default.

| Key | Type | Description |
|-----|------|-------------|
| `root` | string | Optional state root for session files. Supports `~` expansion. |
| `copy_raw_transcripts` | boolean | Whether hook ingestion also preserves provider-native transcript JSONL as `raw.jsonl`. Defaults to `true`. |

When `root` is omitted, wtui uses `$XDG_STATE_HOME/wtui/sessions/v1`, or
`~/.local/state/wtui/sessions/v1` when `XDG_STATE_HOME` is unset.

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

wtui can ingest Claude Code and Codex hook payloads:

```bash
wtui session-hook --provider claude
wtui session-hook --provider codex
```

For development and tests, pass an explicit state root:

```bash
wtui session-hook --provider codex --state-root /tmp/wtui-sessions-test
```

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

When wtui launches an agent with `a` or `N`, it appends these environment
variables so hooks can associate sessions with the selected repo/worktree:
`WTUI_AGENT`, `WTUI_LAUNCH_ID`, `WTUI_REPO_PATH`, `WTUI_WORKTREE_PATH`,
`WTUI_BRANCH`, `WTUI_COMMIT`, and `WTUI_SESSION_STATE_ROOT`.

Transcripts can contain secrets, credentials, private prompts, and proprietary
code. Keep the sessions root in user-private storage and avoid committing
captured transcript files.
