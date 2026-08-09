# Approach

A terminal UI for managing git worktrees across repositories — plus the
coding-agent sessions, saved plans, and multi-phase Flows that run inside
them.

![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)

## Install

### Homebrew

```bash
brew install --cask approachcontrol/tap/approach
```

### GitHub Releases

Download a pre-built macOS or Linux binary from the
[GitHub Releases](https://github.com/approachcontrol/approach/releases) page.

### Go Install

```bash
go install github.com/approachcontrol/approach/cmd/approach@latest
```

### Build from Source

```bash
git clone https://github.com/approachcontrol/approach.git
cd approach
make build
```

The binary is built to `bin/approach`.

## Usage

```bash
# Run with default root (~/dev)
./bin/approach

# Run with a custom root
WORKTREE_ROOT=~/projects ./bin/approach
```

### Getting Around

Repos on the left, content on the right. The essentials:

| Key | Action |
|-----|--------|
| `↑`/`↓` or `k`/`j` | Move selection |
| `enter` / `tab` | Collapse repos and focus content / focus content without collapsing |
| `ctrl+r` / `bksp` | Restore and focus the full repo pane |
| `1`–`4` | Git view, sessions, plans, flows |
| `w`/`b`/`s`/`h`/`r` | Git subviews: worktrees, branches, stashes, history, reflog |
| `ctrl+a` | Toggle Active Flows (all repos) |
| `/` | Fuzzy filter the active pane |
| `D` | Toggle destructive mode — deletion keys stay disabled until this is on |
| `a` | Launch the configured coding agent |
| `n` | Create a worktree, branch, Flow, or repo (context-dependent) |
| `N` | Create a worktree and launch the agent in it |
| `enter` | Page a diff or transcript, or expand phases |
| `q`/`esc` | Close a dialog or quit |

The full key reference and per-view behavior — git subviews, sessions, plans,
Flows, embedded terminals, recovery states — is in
[docs/tui-guide.md](docs/tui-guide.md).

## Agents, Plans, and Flows

Approach launches Codex and Claude Code in your worktrees, captures their
sessions, and tracks longer tasks as Flows: persisted phase graphs
(plan → review → implementation → PR → merge) that can auto-advance and run
agents headlessly in embedded terminals.

Agents persist plans and Flow progress through the `approach plan` and
`approach flow` CLIs. The canonical agent instructions are the bundled skills
at `agent-skills/approach-plan-persist/`, `agent-skills/approach-flow/`, and
`agent-skills/approach-flow-create/` — install or symlink them into your
agent's user-level skill directory, such as `~/.codex/skills/` for Codex or
the equivalent Claude skills directory. Phase transitions, gating, and merge rules are documented in
[docs/flow-phases.md](docs/flow-phases.md).

## Sessions and Hooks

Agents launched from Approach are wired automatically. To capture sessions
started outside Approach, configure Claude Code or Codex hooks to call:

```bash
approach session-hook --provider claude
approach session-hook --provider codex
```

Transcripts are stored under the user state directory with restrictive
permissions, never inside repositories. Storage layout, security details, and
resume behavior are in [docs/agent-sessions.md](docs/agent-sessions.md).

## Configuration

Approach reads an optional TOML config file from
`$XDG_CONFIG_HOME/approach/config.toml` or `~/.config/approach/config.toml`:

```toml
[scan]
root = "~/projects"

[agent]
command = "claude"
claude_model = "claude-sonnet-5"

[ui]
default_view = 8
```

`WORKTREE_ROOT` overrides `[scan].root`. Unknown sections and keys are ignored
for version compatibility; malformed TOML, wrong types for known keys, and
invalid known values fail startup. The full reference — models and reasoning effort, Flow
presets, prompt templates, bootstrap hooks, sessions storage, terminal
settings, and environment variables — is in [docs/config.md](docs/config.md).

## Development

```bash
make build   # Build binary to bin/approach
make test    # Run all tests
make run     # Build and run with optional ignored repo-local .config/
make tidy    # go mod tidy
make clean   # Remove bin/
```

## Requirements

- Go 1.26+
- Git 2.15+ (worktree support)
- macOS clipboard: `pbcopy` (included with macOS)
- Linux clipboard: install one of `wl-copy`, `xclip`, or `xsel`
- Linux terminal launch: set `TERMINAL` to your terminal emulator; when no tmux/Zellij/`TERMINAL` launch is available, Approach falls back to launching `$SHELL` in the worktree directory
