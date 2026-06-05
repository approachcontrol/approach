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

`[scan].root` supports `~` and `~/...` expansion. `WORKTREE_ROOT` is passed
through as provided by the environment.

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
