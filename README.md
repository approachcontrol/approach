# Approach

A terminal UI for managing git worktrees across repositories — plus Beads
issues, coding-agent sessions, saved plans, and multi-phase Flows that run
inside them.

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
| `enter`/`tab`, `bksp` | Focus the content pane / return to the repo pane |
| `1`–`5` | Git view, sessions, plans, flows, Beads group at Open (`6`–`9` are unbound) |
| `w`/`b`/`s`/`h`/`r` | Git subviews: worktrees, branches, stashes, history, reflog |
| `r`/`b`/`o`/`i`/`c` | Beads-only subviews: ready, blocked, open, in-progress, closed |
| `ctrl+a` | Toggle Active Flows (all repos) |
| `/` | Fuzzy filter the active pane (the Beads content pane is not filterable in this slice) |
| `f5` | Rescan repositories and refresh the current view, including the active Beads subview |
| `D` | Toggle destructive mode — deletion keys stay disabled until this is on |
| `a` | Launch the configured coding agent |
| `n` | Create a worktree, branch, Flow, or repo (context-dependent) |
| `N` | Create a worktree and launch the agent in it |
| `enter` | Page a diff or transcript, or expand phases |
| `q`/`esc` | Close a dialog or quit |

The full key reference and per-view behavior — git subviews, Beads, sessions,
plans, Flows, embedded terminals, recovery states — is in
[docs/tui-guide.md](docs/tui-guide.md).

### Beads

With the content pane focused, press `5` to enter the selected repository's
read-only Beads group at Open. While Beads is active, `r`/`b`/`o`/`i`/`c`
switch directly to Ready, Blocked, Open, In-Progress, and Closed; these letters
remain scoped to Beads, so Git and other views keep their existing meanings.

Ready comes from `bd ready`; Blocked, Open, In-Progress, and Closed come from
their respective `bd list -s ...` status queries. Ready, Blocked, Open, and
In-Progress sort by priority and natural ID. Closed is currently uncapped and
sorts by descending close time, then natural ID. A Ready bead intentionally
also appears in Open when its status is open; the subviews are independent and
do not deduplicate across queries. Rows render as `<id>  P<n>  <title>`, with
two spaces and the assignee appended when present.

Switching subviews, moving the repo cursor, and pressing `f5` query
asynchronously; request tokens prevent older repo, refresh, or subview results
from replacing the active pane. Successful empty queries show `no ready beads`,
`no blocked beads`, `no open beads`, `no in-progress beads`, or
`no closed beads`. While a query is pending the corresponding message starts
with `loading`; any unavailable or failed query still shows the shared
`beads not configured` state in this slice.

Beads remains outside horizontal-arrow cycling, `5` always targets Open, and
the content pane has no filter or detail pager yet. Closed count/capping,
configured-versus-error classification, sticky Beads re-entry, and
`default_view` values beyond the existing 1–9 vocabulary remain deferred.

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
