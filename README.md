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
git config core.hooksPath .beads/hooks
make build
```

The per-clone `core.hooksPath` setting activates the repository's tracked Beads
hooks; they skip their Beads step when `bd` is unavailable. The binary is built
to `bin/approach`.

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
| `ctrl+r` / `bksp` | Restore and focus the full repo pane outside search or terminal input |
| `1`–`5` | Git view, sessions, plans, flows, Beads group at its last-used subview (Open on first entry); `6`–`9` are unbound |
| `w`/`b`/`s`/`h`/`r` | Git subviews: worktrees, branches, stashes, history, reflog |
| `r`/`b`/`o`/`i`/`c` | Beads-only subviews: ready, blocked, open, in-progress, closed |
| `←`/`→` | Wrap within Git or Beads subviews; elsewhere step through Git, sessions, plans, flows, and Beads, after which the arrows stay inside whichever group they entered |
| `ctrl+a` | Toggle Active Flows (all repos) |
| `/` | Fuzzy filter the active pane |
| `f` | Fetch in eligible repo/Git contexts; in a settled Beads Ready pane, create a record-only Flow for the selected Bead |
| `f5` | Rescan repositories and refresh the current view, including the active Beads subview |
| `D` | Toggle destructive mode — deletion keys stay disabled until this is on |
| `a` | Launch the configured coding agent |
| `n` | Create a worktree, branch, Flow, or repo (context-dependent) |
| `N` | Create a worktree and launch the agent in it |
| `enter` | Page a diff, transcript, or selected bead detail, or expand phases |
| `q`/`esc` | Close a dialog or quit |

The full key reference and per-view behavior — git subviews, Beads, sessions,
plans, Flows, embedded terminals, recovery states — is in
[docs/tui-guide.md](docs/tui-guide.md).

### Beads

With the content pane focused, press `5` to enter the selected repository's
Beads group at its last-used subview, defaulting to Open on first entry.
All Beads queries and detail reads are read-only, and Approach never mutates
tracker state. Pressing `5` again inside Beads is a no-op. While Beads is active,
`r`/`b`/`o`/`i`/`c` switch directly to Ready, Blocked, Open, In-Progress, and
Closed, and `←`/`→` step and wrap through those five subviews. The letters
remain scoped to Beads, so Git and other views keep their existing meanings.
From an ungrouped view, Beads is the fifth top-level arrow stop after Flows and
opens at the remembered subview; arrows never spill out of either grouped view.

Ready comes from `bd ready`; Blocked, Open, In-Progress, and Closed come from
their respective `bd list -s ...` status queries. Ready, Blocked, Open, and
In-Progress sort by priority and natural ID. Closed fetches the newest 100 with
`bd list -s closed --json --limit 100 --sort closed --reverse --readonly`, then
sorts the bounded result by descending close time and natural ID. Its total
comes from `bd stats --json --no-activity --readonly`. A settled Closed header
shows the accepted row count plainly through 100, or `<fetched> of <total>`
when the stats total is larger; filtering the pane does not change that source
count. A Ready bead intentionally also appears in Open when its status is open;
the subviews are independent and do not deduplicate across queries. Rows render
as `<id>  P<n>  <title>`, with two spaces and the assignee appended when present.

Each subview keeps independent rows, a fuzzy `/` filter over ID/title/assignee,
and cursor/scroll state. Switching subviews and pressing `f5` query
asynchronously while retaining that same-repo pane state internally; the UI
shows only the subview's `loading` message until the accepted replacement
arrives, then reapplies the filter and clamps selection and scroll to the new
result. Moving to another repo invalidates all Beads requests, clears old-repo
rows and selections, retains each subview's filter, and fetches only the active
subview. Request tokens prevent older repo, refresh, or subview results from
replacing the active pane.

Successful empty queries show `no ready beads`, `no blocked beads`, `no open
beads`, `no in-progress beads`, or `no closed beads`. A missing `bd` executable
or Beads project/database shows the calm `beads not configured` state; other
command failures and invalid JSON show a persistent `Could not load ...` error
with sanitized detail. Press
`enter` on a visible selected bead after its subview settles to asynchronously
page the raw human-readable output of `bd show <id> --readonly` through
`less -R`. A newer detail request, subview or repo change, or Beads refresh
invalidates an older result; delivery also requires the same bead to remain the
visible selection.

In Ready only, press `f` on a settled visible selection whose Bead has a usable
ID to create one record-only, Approach-owned Flow in the selected repository.
Its title is `<trimmed bead ID>: <trimmed bead title>` and its instructions are
``Use Bead <id> as the durable source of requirements. Read it with `bd show <id>` before planning or implementation.`` The configured Flow preset supplies
the phase graph, with normal creation defaults, but this shortcut supplies no
worktree, branch, base ref, commit, plan/link, launch, session, agent, issue, or
PR metadata. It does not run a bootstrap hook, start a phase, launch an agent,
or invoke `bd`; the Bead remains untouched.

A record-only Flow is not the same as the parked Flow the Flows-pane `n` form
creates: it has no worktree, so the Flows pane shows it with the
`missing-worktree` branch label and a `recover-worktree` phase state, and
launching its first phase with `g` runs the agent in the repository root rather
than an isolated worktree. No operation currently attaches a worktree to this
existing record; if isolation is required, create a separate Flow through the
normal `n` path instead of launching this one.

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
