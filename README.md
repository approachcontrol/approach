# wtui

A terminal UI for managing git worktrees across repositories.

![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)

## Install

### Homebrew

```bash
brew install --cask brian-bell/tap/wtui
```

### GitHub Releases

Download a pre-built macOS or Linux binary from the
[GitHub Releases](https://github.com/brian-bell/wtui/releases) page.

### Go Install

```bash
go install github.com/brian-bell/wtui/cmd/wtui@latest
```

### Build from Source

```bash
git clone https://github.com/brian-bell/wtui.git
cd wtui
make build
```

The binary is built to `bin/wtui`.

## Usage

```bash
# Run with default root (~/dev)
./bin/wtui

# Run with a custom root
WORKTREE_ROOT=~/projects ./bin/wtui
```

### Keys

The UI has two panes: repos on the left, content on the right. `tab` switches focus between them. The active pane is highlighted with a blue border.

**Destructive mode:** The app starts in read-only mode — deletion keys are disabled. Press `D` (Shift+D) to toggle destructive mode on/off. When active, the right pane border turns red and delete/drop hints appear in red as a visual warning.

**Fuzzy filter:** Press `/` in the active pane to type-ahead filter repos or right-pane items. `enter` keeps the filter, `esc` clears it, `backspace` edits it.

Empty panes explain why they are empty: no data for the selected repo, no fuzzy
filter matches, or a load failure with details in the status bar.

**Left pane (repos)**

| Key | Action |
|-----|--------|
| `↑`/`k` | Select previous repo |
| `↓`/`j` | Select next repo |
| `/` | Fuzzy filter repos |
| `A` | Choose and persist the coding agent (`codex`, `codex-app`, or `claude`) |
| `D` | Toggle destructive mode |
| `f` | Fetch all currently visible repos with `--prune` |
| `tab` | Switch focus to right pane |
| `q`/`esc` | Quit |

**Right pane (content)**

| Key | Action |
|-----|--------|
| `↑`/`k` | Move selection up |
| `↓`/`j` | Move selection down |
| `/` | Fuzzy filter the current item list |
| `1`/`2`/`3`/`4`/`5`/`6`/`7`/`8` | Switch to worktrees / branches / stashes / history / reflog / sessions / plans / flows |
| `←`/`h`/`→`/`l` | Cycle through modes |
| `enter` | View diff (dirty worktree, dirty branch, stash, commit, or reflog entry), session transcript, or expand/collapse plan phases |
| `n` | Create a new worktree in worktrees view, or a new branch in branches view |
| `P` | Create a review worktree from a GitHub PR number or URL |
| `N` | Create a new worktree and launch the selected coding agent |
| `m` | Move or rename a linked worktree (worktrees view) |
| `A` | Choose and persist the coding agent (`codex`, `codex-app`, or `claude`) |
| `a` | Launch the selected coding agent in the selected worktree |
| `d` | Delete worktree/branch or drop stash — requires destructive mode |
| `p` | Prune stale worktree — requires destructive mode (worktrees view) |
| `u` | Unlock a locked worktree (worktrees view) |
| `f` | Fetch with `--prune` (worktrees and branches views) |
| `F` | Pull with `--ff-only` (worktrees, and branches with a checked-out worktree) |
| `t` | Open or attach to a tmux/Zellij session for the worktree |
| `c` | Open VSCode at worktree path |
| `y` | Copy hash to clipboard (history/reflog view), selected agent session ID (sessions view), or plan Markdown path (plans view) |
| `r` | Resume selected agent session (sessions view) |
| `s` | Show selected agent session summary (sessions view) |
| `o` | Open selected plan Markdown in a plain-text overlay (plans view), or the linked plan body (flows view) |
| `i` | Edit launch instructions and launch the selected plan or selected plan phase (plans view) |
| `D` | Toggle destructive mode |
| `tab` | Switch focus to left pane |
| `q`/`esc` | Close overlay or quit |

The right pane header shows the active mode. Press `1`–`8` or use arrow keys to
switch between worktrees, branches, stashes, history, reflog, sessions, plans,
and flows.

When the left repo pane is focused, press `f` to run `git fetch --prune` for
the currently visible repos. Repo filtering limits the batch to the filtered
list captured when the key is pressed.

### Worktrees view (mode 1)

The default view. Shows all worktree checkouts for the selected repo. The main (root) worktree always appears first with a blue `[root]` annotation.

Each row shows the branch name (or `(detached)` for detached HEAD), status indicators, and the worktree path:

- `✔` green: clean working tree
- `●` red: dirty — shows `N files +X/-Y` (lines added/deleted)
- `✗` red: stale — worktree directory no longer exists

Press `A` to choose `codex`, `codex-app`, or `claude`; wtui persists the choice to config.
Press `a` to launch the selected agent in the current non-stale worktree, or
`N` to create a worktree and launch the agent there immediately. Press `n` to
create a worktree without launching an agent. Enter an existing branch, tag, or
new branch name; wtui creates it under a sibling `<repo>-worktrees/` directory
and refreshes the list. Press `P` to create a review worktree from a GitHub PR
number or URL; wtui fetches the PR head into `pr-<number>` and checks it out
under the same sibling worktree directory. If a matching `[bootstrap]` hook is
configured, wtui runs it after successful worktree creation; hook failures keep
the worktree, show a status error, and prevent automatic agent launch for `N`.
Press `f` to `git fetch --prune` and `F` to `git pull --ff-only` for the
selected worktree. Press `m` on a linked non-stale, unlocked worktree to move it
to an absolute path or rename it with a sibling-relative destination; dirty local
changes move with the worktree. Locked worktrees cannot be moved, deleted, or
pruned; press `u` to unlock one.

### Branches view (mode 2)

Shows non-worktree branches and the root branch. Worktree branches are managed in the worktrees view (mode 1) and are hidden here to avoid duplication. The root branch (checked out at the repo root) is pinned to position 0 with a blue `[root]` annotation and cannot be deleted.

Status indicators stack on each branch:

- `✔` green: even with upstream, clean working tree
- `●` yellow: ahead/behind upstream — shows `+N/-N` counts
- `●` red: dirty worktree — shows `N files +X/-Y` (lines added/deleted)
- `●` purple: no upstream or upstream gone
- `merged` cyan: branch is fully merged into the cleanup branch (`main` or `master`)

Branches ahead of upstream show up to 5 unpushed commit messages, with overflow
count. Press `n` to create a new branch from the selected branch, without
checking it out or creating a worktree. When the root branch is dirty, `enter`
opens a full-screen diff overlay. `t` opens or attaches to a tmux/Zellij
session, `c` opens VSCode at the worktree path, and `a` launches the selected
coding agent for checked-out branch rows only. `f` runs `git fetch --prune`,
and `F` runs `git pull --ff-only` for branches that have a checked-out
worktree. `d` deletes non-worktree branches, with a force-retry prompt on
failure. Deletion requires destructive mode to be enabled first (`D`).

### Stashes view (mode 3)

Browse stashes for the selected repo. Long stash messages wrap to two lines (date + message start, then indented continuation). Use `↑`/`↓` to select a stash, `enter` to view its diff in a full-screen overlay, `d` to drop the selected stash (with confirmation, requires destructive mode). The stash list scrolls when entries exceed the pane height.

### History view (mode 4)

Browse recent commits (up to 50) for the selected repo. Each row shows the commit hash, author, relative date, and subject. Use `enter` to view the full commit diff, `y` to copy the commit hash to clipboard, `t` to open or attach to a tmux/Zellij session, and `c` to open VSCode at the repo root.

### Reflog view (mode 5)

Browse HEAD reflog entries (up to 50) for the selected repo. Each row shows the abbreviated hash, selector (e.g. `HEAD@{0}`), relative date, and subject. Use `enter` to view the diff for that entry — checkout entries with no tree changes show "No changes at this reflog entry". Use `y` to copy the entry hash to clipboard.

### Sessions view (mode 6)

Browse captured Claude Code and Codex sessions associated with the selected
repo. Rows show provider, branch, worktree, status, and summary. Use `/` to
filter sessions by provider, session ID, launch ID, branch, worktree, model,
status, or summary. Press `enter` to open the normalized transcript overlay,
`s` to show the selected summary, `r` to resume the selected provider session,
or `y` to copy the raw provider session ID.

Session data is stored under the user state directory by default:
`$XDG_STATE_HOME/wtui/sessions/v1`, or
`~/.local/state/wtui/sessions/v1` when `XDG_STATE_HOME` is unset. Transcripts
may contain secrets or private prompts; wtui keeps them outside repositories and
uses restrictive file permissions for created session files. Provider session IDs
are stored in hashed directory names instead of raw path components.
When resuming a session, wtui runs the provider resume command from the recorded
session `cwd` when present, falling back to the captured worktree path, while
preserving the stored worktree metadata for subsequent hooks.

### Plans view (mode 7)

Browse saved agent plans for the selected repo. Rows show status, branch, phase
progress (`completed/total`), the updated date, and the title. Use `/` to filter
plans by title, summary, status, branch, worktree basename, provider, session
ID, launch ID, and phase titles/statuses. Press `enter` to expand or collapse
the selected plan's phase rows, `o` to open the plan Markdown in a plain-text
overlay, and `y` to copy the plan Markdown path. Press `i` to edit launch
instructions for the selected plan or selected phase, then `enter` to launch
the selected agent or `esc` to cancel; blank instructions are rejected.

Plans are persisted explicitly by agents through the `wtui plan` CLI rather than
captured from hooks. Plans share the agent-artifact root with sessions: they are
stored under `<sessions root>/plans/<plan-id>/` (`meta.json` plus `plan.md`),
that is `$XDG_STATE_HOME/wtui/sessions/v1/plans/...` or
`~/.local/state/wtui/sessions/v1/plans/...` by default. **Because plans live
beside sessions, moving or cleaning the sessions root (including via
`WTUI_PLAN_STATE_ROOT` or the TUI-level `WTUI_FLOW_STATE_ROOT`) also moves or
removes your saved plans.**

Agents persist plans with the `wtui plan` subcommands (these load config to
resolve the artifact root but never scan repositories or start the TUI):

```bash
# Save (or update with --plan-id) a plan; reads Markdown from --file or stdin,
# prints only the plan_id.
printf '%s' "$PLAN_MD" | wtui plan save --title "Persist plans" --status draft

# Record per-phase progress.
wtui plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store" --status completed --order 1

# Read plans back.
wtui plan list --repo-path "$REPO" --json   # requires --json in v1
wtui plan read --plan-id "$PLAN_ID"          # prints Markdown only
```

The plan state root is resolved with this precedence: `--state-root` >
`WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` > `[sessions].root` > the user
state default. CLI-launched agents get `WTUI_PLAN_STATE_ROOT` and
`WTUI_SESSION_STATE_ROOT` set to the same resolved root. Omitted metadata is
filled first from `WTUI_AGENT`, `WTUI_LAUNCH_ID`, `WTUI_REPO_PATH`,
`WTUI_WORKTREE_PATH`, `WTUI_BRANCH`, and `WTUI_COMMIT`; for new plans, and for
updates that provide a repo or worktree location, wtui also resolves best-effort
repo, worktree, branch, and commit metadata from git. `codex-app` launches use
macOS `open`, so they do not inherit `WTUI_*`; wtui includes equivalent metadata
in the launch prompt, and agents should pass the listed `--state-root` when
running `wtui plan` commands. The `wtui-plan-persist` skill instructs agents on
when and how to save plans. Its canonical source lives in
`agent-skills/wtui-plan-persist/`, which is intentionally outside Codex and
Claude's repo auto-discovery directories so it can be symlinked into user-level
skill dirs for use across repos. v1 has no TUI plan editing or deletion.

### Flows view (mode 8)

Browse persisted Flow records for the selected repo. Rows show status, branch
or worktree basename, phase progress (`completed/total`, counting skipped phases
as done), linked plan ID when present, PR number or label, updated date, and
title. Use `/` to filter by title, instructions, status, branch, worktree
basename, plan metadata, PR metadata, phase titles/statuses/summaries, and
linked session metadata. Press `o` to open the linked plan body in a plain-text
overlay; wtui shows a status message when the selected Flow has no linked plan.

Flows are task-centric workflow records stored beside sessions and plans under
`<sessions root>/flows/<flow-id>/meta.json`. The TUI does not mutate Flow
records in v1; create, read, list, update phase state, and link saved plans with
the `wtui flow` CLI:

```bash
# Create a flow; --repo-path must be absolute and --json is required in v1.
wtui flow create --title "Ship saved plans" \
  --instructions "Plan, implement, review, open a PR, and merge." \
  --repo-path "$REPO" --json

# List or read flows.
wtui flow list --repo-path "$REPO" --json
wtui flow read --flow-id "$FLOW_ID"

# Link a saved plan artifact back to a flow.
wtui flow plan set --flow-id "$FLOW_ID" --plan-id "$PLAN_ID"
```

The flow state root is resolved with this precedence: `--state-root` >
`WTUI_FLOW_STATE_ROOT` > `WTUI_PLAN_STATE_ROOT` > `WTUI_SESSION_STATE_ROOT` >
`[sessions].root` > the user state default. In TUI startup,
`WTUI_FLOW_STATE_ROOT`, `WTUI_PLAN_STATE_ROOT`, or `WTUI_SESSION_STATE_ROOT`
relocates the shared artifact root for sessions, plans, and flows.

Flow-launched agents should use the canonical `wtui-flow` skill source at
`agent-skills/wtui-flow/`. Install or symlink it beside
`agent-skills/wtui-plan-persist/` in the user-level skill directory for your
agent, such as `~/.codex/skills/wtui-flow` for Codex or the equivalent Claude
skills directory. The skill activates when `WTUI_FLOW_ID` and
`WTUI_FLOW_PHASE_ID` are present, reads the active flow before updates, and uses
the implemented `wtui flow` and `wtui plan` commands for persistence.

## Configuration

wtui reads an optional TOML config file before scanning repositories:

```text
$XDG_CONFIG_HOME/wtui/config.toml
~/.config/wtui/config.toml
```

Example:

```toml
[scan]
root = "~/projects"
max_depth = 2

[agent]
command = "codex"
plan_prompt = "Implement the saved wtui plan {title} (ID: {plan_id}) at {plan_path}. Read the plan file, then begin implementation."

[sessions]
root = "~/.local/state/wtui/sessions/v1"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 120

[[bootstrap.hooks]]
repo_path = "~/projects/wtui"
script = ".wtui/bootstrap"
```

`WORKTREE_ROOT` overrides `[scan].root` when both are set. `[agent].plan_prompt`
customizes the editable instructions shown before launching an agent from the
plans pane. See [docs/config.md](docs/config.md) for the full config reference,
including sessions storage, bootstrap hook settings, and parsed foundation
fields for editor, terminal, provider, launch, and agent settings.

| Env var | Default | Description |
|---------|---------|-------------|
| `WORKTREE_ROOT` | `[scan].root` or `~/dev` | Root directory to scan for git repos; depth defaults to 2 and can be reduced with `[scan].max_depth` |
| `TERMINAL` | unset | Terminal command to use when `t` opens a worktree outside tmux/Zellij |
| `WTUI_SESSION_STATE_ROOT` | `[sessions].root` or user state default | Session hook storage root; normally set automatically for agents launched by wtui |
| `WTUI_PLAN_STATE_ROOT` | `WTUI_SESSION_STATE_ROOT`, `[sessions].root`, or user state default | Saved-plan artifact root for `wtui plan`; set automatically for agents launched by wtui. In the TUI it relocates the whole artifact root, moving sessions, plans, and flows |
| `WTUI_FLOW_STATE_ROOT` | `WTUI_PLAN_STATE_ROOT`, `WTUI_SESSION_STATE_ROOT`, `[sessions].root`, or user state default | Flow artifact root for `wtui flow`. In the TUI it has highest precedence for the shared sessions/plans/flows artifact root |

### Agent session hooks

CLI agents launched from wtui with `a`, `N`, or session resume `r` are wired
automatically: wtui passes Claude Code or Codex a session-end hook that calls
the current wtui binary, and it includes `WTUI_*` metadata so hook records can
be associated with the repo, worktree, branch, and launch. `codex-app` opens via
macOS deep link instead; wtui scrubs inherited `WTUI_*` from `open` and includes
launch metadata in the prompt when a prompt is provided.

For manual agent sessions that are not launched by wtui, configure Claude Code
or Codex hooks to call wtui:

```bash
wtui session-hook --provider claude
wtui session-hook --provider codex
```

For local testing, use `--state-root /tmp/wtui-sessions-test`.

Codex may ask you to review and trust the injected hook with `/hooks` before it
runs it. After trust is recorded for the unchanged hook command, later
wtui-launched Codex sessions can save normally.

`session-hook` loads the normal wtui config, so `[sessions].root` and
`copy_raw_transcripts` apply to hook ingestion. `--state-root` overrides the
configured sessions root for one hook invocation. Raw provider transcript copies
are off by default; set `copy_raw_transcripts = true` to also preserve
provider-native `raw.jsonl` alongside normalized transcript events.

## Development

```bash
make build   # Build binary to bin/wtui
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
- Linux terminal launch: set `TERMINAL` to your terminal emulator; when no tmux/Zellij/`TERMINAL` launch is available, wtui falls back to launching `$SHELL` in the worktree directory
