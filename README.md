# wtui

A terminal UI for managing git worktrees across repositories.

![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)

## Install

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
| `d` | Delete worktree/branch or drop stash — requires destructive mode |
| `p` | Prune stale worktree — requires destructive mode (worktrees view) |
| `t` | Open terminal at worktree path |
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

### Branches view (mode 2)

Shows non-worktree branches and the root branch. Worktree branches are managed in the worktrees view (mode 1) and are hidden here to avoid duplication. The root branch (checked out at the repo root) is pinned to position 0 with a blue `[root]` annotation and cannot be deleted.

Status indicators stack on each branch:

- `✔` green: even with upstream, clean working tree
- `●` yellow: ahead/behind upstream — shows `+N/-N` counts
- `●` red: dirty worktree — shows `N files +X/-Y` (lines added/deleted)
- `●` purple: no upstream or upstream gone

Branches ahead of upstream show up to 5 unpushed commit messages, with overflow count. When the root branch is dirty, `enter` opens a full-screen diff overlay. `t`/`c` open a terminal or VSCode at the worktree path (root branch only). `d` deletes non-worktree branches, with a force-retry prompt on failure. Deletion requires destructive mode to be enabled first (`D`).

### Stashes view (mode 3)

Browse stashes for the selected repo. Long stash messages wrap to two lines (date + message start, then indented continuation). Use `↑`/`↓` to select a stash, `enter` to view its diff in a full-screen overlay, `d` to drop the selected stash (with confirmation, requires destructive mode). The stash list scrolls when entries exceed the pane height.

### History view (mode 4)

Browse recent commits (up to 50) for the selected repo. Each row shows the commit hash, author, relative date, and subject. Use `enter` to view the full commit diff, `y` to copy the commit hash to clipboard, and `t`/`c` to open terminal or VSCode at the repo root.

### Reflog view (mode 5)

Browse HEAD reflog entries (up to 50) for the selected repo. Each row shows the abbreviated hash, selector (e.g. `HEAD@{0}`), relative date, and subject. Use `enter` to view the diff for that entry — checkout entries with no tree changes show "No changes at this reflog entry". Use `y` to copy the entry hash to clipboard.

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `WORKTREE_ROOT` | `~/dev` | Root directory to scan for git repos (up to 2 levels deep) |

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
