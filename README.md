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
| `D` | Toggle destructive mode |
| `tab` | Switch focus to right pane |
| `q`/`esc` | Quit |

**Right pane (content)**

| Key | Action |
|-----|--------|
| `↑`/`k` | Move selection up |
| `↓`/`j` | Move selection down |
| `/` | Fuzzy filter the current item list |
| `1`/`2`/`3`/`4`/`5` | Switch to worktrees / branches / stashes / history / reflog |
| `←`/`h`/`→`/`l` | Cycle through modes |
| `enter` | View diff (dirty worktree, dirty branch, stash, commit, or reflog entry) |
| `n` | Create a new worktree from a branch, tag, or new branch name |
| `d` | Delete worktree/branch or drop stash — requires destructive mode |
| `p` | Prune stale worktree — requires destructive mode (worktrees view) |
| `u` | Unlock a locked worktree (worktrees view) |
| `f` | Fetch with `--prune` (worktrees and branches views) |
| `F` | Pull with `--ff-only` (worktrees, and branches with a checked-out worktree) |
| `t` | Open or attach to a tmux/Zellij session for the worktree |
| `c` | Open VSCode at worktree path |
| `y` | Copy hash to clipboard (history/reflog view) |
| `D` | Toggle destructive mode |
| `tab` | Switch focus to left pane |
| `q`/`esc` | Close overlay or quit |

The right pane header shows the active mode. Press `1`–`5` or use arrow keys to switch between worktrees, branches, stashes, history, and reflog.

### Worktrees view (mode 1)

The default view. Shows all worktree checkouts for the selected repo. The main (root) worktree always appears first with a blue `[root]` annotation.

Each row shows the branch name (or `(detached)` for detached HEAD), status indicators, and the worktree path:

- `✔` green: clean working tree
- `●` red: dirty — shows `N files +X/-Y` (lines added/deleted)
- `✗` red: stale — worktree directory no longer exists

Press `n` to create a worktree. Enter an existing branch, tag, or new branch name; wtui creates it under a sibling `<repo>-worktrees/` directory and refreshes the list. Press `f` to `git fetch --prune` and `F` to `git pull --ff-only` for the selected worktree. Locked worktrees cannot be deleted or pruned; press `u` to unlock one.

### Branches view (mode 2)

Shows non-worktree branches and the root branch. Worktree branches are managed in the worktrees view (mode 1) and are hidden here to avoid duplication. The root branch (checked out at the repo root) is pinned to position 0 with a blue `[root]` annotation and cannot be deleted.

Status indicators stack on each branch:

- `✔` green: even with upstream, clean working tree
- `●` yellow: ahead/behind upstream — shows `+N/-N` counts
- `●` red: dirty worktree — shows `N files +X/-Y` (lines added/deleted)
- `●` purple: no upstream or upstream gone
- `merged` cyan: branch is fully merged into the cleanup branch (`main` or `master`)

Branches ahead of upstream show up to 5 unpushed commit messages, with overflow count. When the root branch is dirty, `enter` opens a full-screen diff overlay. `t` opens or attaches to a tmux/Zellij session and `c` opens VSCode at the worktree path (root branch only). `f` runs `git fetch --prune`, and `F` runs `git pull --ff-only` for branches that have a checked-out worktree. `d` deletes non-worktree branches, with a force-retry prompt on failure. Deletion requires destructive mode to be enabled first (`D`).

### Stashes view (mode 3)

Browse stashes for the selected repo. Long stash messages wrap to two lines (date + message start, then indented continuation). Use `↑`/`↓` to select a stash, `enter` to view its diff in a full-screen overlay, `d` to drop the selected stash (with confirmation, requires destructive mode). The stash list scrolls when entries exceed the pane height.

### History view (mode 4)

Browse recent commits (up to 50) for the selected repo. Each row shows the commit hash, author, relative date, and subject. Use `enter` to view the full commit diff, `y` to copy the commit hash to clipboard, `t` to open or attach to a tmux/Zellij session, and `c` to open VSCode at the repo root.

### Reflog view (mode 5)

Browse HEAD reflog entries (up to 50) for the selected repo. Each row shows the abbreviated hash, selector (e.g. `HEAD@{0}`), relative date, and subject. Use `enter` to view the diff for that entry — checkout entries with no tree changes show "No changes at this reflog entry". Use `y` to copy the entry hash to clipboard.

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
```

`WORKTREE_ROOT` overrides `[scan].root` when both are set. See
[docs/config.md](docs/config.md) for the full config reference, including parsed
foundation fields for editor, terminal, provider, and launch settings.

| Env var | Default | Description |
|---------|---------|-------------|
| `WORKTREE_ROOT` | `[scan].root` or `~/dev` | Root directory to scan for git repos; depth defaults to 2 and can be reduced with `[scan].max_depth` |
| `TERMINAL` | unset | Terminal command to use when `t` opens a worktree outside tmux/Zellij |

## Development

```bash
make build   # Build binary to bin/wtui
make test    # Run all tests
make run     # Build and run
make tidy    # go mod tidy
make clean   # Remove bin/
```

## Requirements

- Go 1.26+
- Git 2.15+ (worktree support)
- macOS clipboard: `pbcopy` (included with macOS)
- Linux clipboard: install one of `wl-copy`, `xclip`, or `xsel`
- Linux terminal launch: set `TERMINAL` to your terminal emulator; when no tmux/Zellij/`TERMINAL` launch is available, wtui falls back to launching `$SHELL` in the worktree directory
