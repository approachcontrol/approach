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

# Serve the read-only GraphQL API on 127.0.0.1:8787
./bin/approach serve

# Report what is in the artifact root, and migrate it after a release bump
./bin/approach db inspect --json
./bin/approach db migrate
```

`approach serve` exposes repos and Flows over `POST /graphql` for external
tools and dashboards. It is read-only, binds loopback by default, and requires
a token on any other bind address. See `docs/graphql-api.md`.

### Getting Around

Repos are on the left. The content column is split into a Git/Beads pane on
top and a Sessions/Plans/Flows pane on the bottom; the shortcut rail is on the
right. The essentials:

| Key | Action |
|-----|--------|
| `↑`/`↓` or `k`/`j` | Move selection |
| `enter` / `tab` | Collapse repos and focus the top pane / cycle focus without collapsing |
| `ctrl+r` | Restore and focus the full repo pane outside search or terminal input |
| `bksp` | Cycle focus in reverse outside search or terminal input |
| Top `1` / `2` | Git / Beads at its last-used subview (Ready on first Beads entry) |
| Bottom `1` / `2` / `3` | Sessions / Plans / Flows |
| `w`/`b`/`s`/`h`/`r` | Git subviews: worktrees, branches, stashes, history, reflog |
| `r`/`b`/`o`/`i`/`c` | Beads-only subviews: ready, blocked, open, in-progress, closed |
| `←`/`→` | Wrap between Git and Beads in the top pane, or Sessions, Plans, and Flows in the bottom pane |
| `ctrl+a` | Toggle Active Flows (all repos) |
| `ctrl+p` | Toggle PR Babysitter for live mergeability and checks across PRs awaiting merge |
| `/` | Fuzzy filter the active pane |
| `f` | Fetch in eligible repo/Git contexts; in a settled Beads Ready pane, create a parked Flow with its worktree for the selected Bead |
| `F` | In a settled Beads Ready pane, create the selected Bead's Flow and start its first actionable phase; pull in eligible Git contexts and elsewhere outside that Ready selection |
| `S` | In a settled Beads Ready pane with an epic selected, launch one configured agent at the repository root to slice that epic into child Beads with the `slice-issues` skill |
| `f5` | Rescan repositories and refresh both stored content panes; the visible takeover refreshes independently too |
| `D` | Toggle destructive mode — deletion keys stay disabled until this is on |
| `a` | Launch the configured coding agent; on a selected epic, toggle its auto-progression; on a Flow, toggle phase auto mode |
| `n` | Create a worktree, branch, Flow, or repo (context-dependent) |
| `N` | Create a worktree and launch the agent in it |
| `enter` | Page a diff, transcript, or selected bead detail, or expand phases |
| `q`/`esc` | Close a dialog or quit |

The full key reference and per-view behavior — git subviews, Beads, sessions,
plans, Flows, embedded terminals, recovery states — is in
[docs/tui-guide.md](docs/tui-guide.md).

### Beads

With the top pane focused, press `2` to enter the selected repository's Beads
group at its last-used subview, defaulting to Ready on first entry.
All Beads queries and detail reads are read-only. The only tracker mutation is
the explicit claim performed when epic auto-progression prepares a new child
Flow; ordinary browsing and manual Ready Flow creation never mutate Beads.
`S` on a Ready epic is no exception: it only hands a launched agent the epic ID
and the slicing contract, and the approved child Beads are created by that
agent, never by the TUI.
Pressing `2` again inside Beads is a no-op. While Beads is active,
`r`/`b`/`o`/`i`/`c` switch directly to Ready, Blocked, Open, In-Progress, and
Closed, and `←`/`→` step and wrap through those five subviews. The letters
remain scoped to Beads, so Git and other views keep their existing meanings.
From Git, `←`/`→` enters Beads at the remembered subview; the same arrows return
to Git at its remembered subview.

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
rows and selections, retains each subview's filter, and fetches the stored
top-pane view alongside the stored bottom-pane view. Request tokens prevent
older repo, refresh, or subview results from replacing either stored pane's
current data.

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

In Ready only, press `f` or `F` on a settled visible selection whose Bead has a
usable ID to create one Approach-owned Flow in the selected repository.
Its title is `<trimmed bead ID>: <trimmed bead title>` and its instructions are
``Use Bead <id> as the durable source of requirements. Read it with `bd show <id>` before planning or implementation.`` The configured Flow preset supplies
the phase graph, and both shortcuts prepare the Flow exactly like a Flows-pane
`n` submission with Plan Now off: it creates the `flow/<slug>` branch and
worktree from the repository's current HEAD, records the start metadata, and
runs the bootstrap hook. Lowercase `f` then parks the Flow without linking a
saved plan or GitHub Issue, starting a phase, or launching an agent. Both
shortcuts persist an independent Bead link from the selected row: the trimmed
Bead ID plus its trimmed parent epic ID when present. Uppercase `F` instead
starts the first actionable phase through the normal creation-time Flow launch
path, using the configured agent, model, reasoning effort, prompt templates,
session root, and default-on headless setting. CLI agents stay embedded for this
creation-time start even in tmux mode; external-only agents keep their normal
external route. Neither shortcut invokes `bd` or claims the issue, so Beads
remains untouched.

Selecting an epic in any Beads subview also loads its persisted progression
state alongside the direct-child and Ready snapshots. Press `a` when the
footer shows `a: auto on` to prepare the first ready direct child's Flow and
enable progression, or `a: auto off` to disable it. Enabled rows show
`[epic]  [auto]`. Enablement reuses the Ready ordering and exact create-only
request mapping. After revalidating the selected child, Approach persists its
receipt-less exact-link Flow identity, then claims the child with
`bd update --claim -- <id>` before creating a worktree; the positional separator
prevents flag-shaped IDs from being interpreted as options. A child that is no
longer both direct and ready stops the attempt without a Flow or claim. A claim
error is shown with its cause and retains the marked unprepared Flow identity so
retry can repeat the same-actor idempotent claim when a post-start error may mean
the claim already landed. The generated Flow
instructions use `bd show -- <id>` so the same IDs remain positional on lookup.
Before choosing a new ready sibling, enablement recovers a direct child's open
marked receipt-less or prepared-pending exact-link Flow independently of Ready
state; retry reserves and revalidates both that Flow identity and current direct
membership, then reclaims it idempotently before adopting a prepared Flow or
surfacing incomplete preparation instead of skipping it. Partial listings,
ambiguity, running Flows, and terminal Flows still refuse enablement. It does
not start a phase or launch an
agent. With no ready or recoverable child it reports that progression remains
off and writes neither a claim, Flow, nor progression state.

When an enabled epic exhausts its ready children, Approach persists an explicit
completed progression state. Completion remains disabled for compatibility but
is distinct from pressing `a` to turn progression off; disabled state alone is
never interpreted as completion.

The Ready selection owns both keys while either request is in flight, preventing
repeated or mixed presses from creating duplicates. `F` is advertised only when
an agent is configured; when it is not, the owned key is consumed instead of
falling through to pull. Repository changes invalidate stale completions and
release any held launch reservation. Outside an owned Ready selection, `F`
retains its normal pull behavior.

Lowercase `f` produces the same parked Flow as the Flows-pane `n` form, so `g`
on its first phase launches the agent inside the isolated worktree. For either
Ready shortcut, an initial store failure leaves no Flow; worktree failure keeps
the persisted Flow with its launchable phases blocked; start-metadata failure
keeps the Flow and reports its ID even though the new worktree could not be
recorded; and bootstrap failure keeps the recorded worktree with blocked
phases. For uppercase `F`, later reservation, launch-bookkeeping, and
agent-spawn failures also keep the Flow for recovery and release their
reservation. A Flow that
has no worktree at all — one created through `approach flow create` without
`--worktree-path`, for example — gets one created on its first phase launch
rather than running the agent in the repository root; when that creation is
impossible the launch is refused with the reason.

## Agents, Plans, and Flows

Approach launches Codex, Claude Code, and Cursor CLI in your worktrees, captures their
sessions, and tracks longer tasks as Flows: persisted phase graphs
(plan → review → implementation → PR → merge) that can auto-advance and run
agents in embedded terminals. Each Flow persists its own default-on
headless/interactive preference for manual phase and repair launches; automatic
phase launches remain always headless.

Agents run in Approach's embedded terminal by default. Setting
`[launch].backend = "tmux"` opts into tmux mode, where each repo gets one tmux
session on your default tmux server and most interactive CLI agent launches
become windows in it — visible to your own `tmux ls`, reattachable with `T` or
`tmux attach`, and outliving the TUI. Headless launches, Flow repair, and the
plan launch performed during Flow creation keep their embedded routes, and
Approach falls back to the embedded terminal when tmux is not installed. See
[docs/config.md](docs/config.md) and [docs/tui-guide.md](docs/tui-guide.md).
Tracked Flow phase windows retain Flow occupancy with a kernel lease until the
agent process exits, even if the phase is already completed and even across TUI
restarts. Successor launches and AutoMode defer without polling tmux.

Agents persist plans and Flow progress through the `approach plan` and
`approach flow` CLIs. The canonical agent instructions are the bundled skills
at `agent-skills/approach-plan-persist/`, `agent-skills/approach-flow/`, and
`agent-skills/approach-flow-create/` — install or symlink them into your
agent's user-level skill directory, such as `~/.codex/skills/` for Codex,
the equivalent Claude skills directory, or Cursor's skills location. The bundled installer does this for you:

```bash
./agent-skills/install.sh            # symlink into every agent skills dir found
./agent-skills/install.sh --dry-run  # preview the changes first
```

Symlinks keep the skills tracking your checkout; pass `--copy` for detached
copies, or `--target DIR` to choose the destination explicitly. Re-run it after
moving the repository to repair links that still point at the old path.
Phase transitions, gating, and merge rules are documented in
[docs/flow-phases.md](docs/flow-phases.md).

## Sessions and Hooks

Agents launched from Approach are wired automatically. To capture sessions
started outside Approach, configure Claude Code, Codex, or Cursor CLI hooks to call:

```bash
approach session-hook --provider claude
approach session-hook --provider codex
approach session-hook --provider cursor-agent
```

The hook writes a warning to stderr and still exits zero when it captures the
session but cannot attach it to a Flow — a schema-compatibility notice is not a
persistence failure. Run `approach db migrate` when you see one.

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
```

`WORKTREE_ROOT` overrides `[scan].root`. Unknown sections and keys are ignored
for version compatibility; malformed TOML, wrong types for known keys, and
invalid known values fail startup. Legacy `[ui].default_view` assignments are
accepted but ignored: startup is always repos-focused with Beads/Ready above
Flows. The full reference — models and reasoning effort, Flow
presets, prompt templates, bootstrap hooks, sessions storage, terminal
settings, and environment variables — is in [docs/config.md](docs/config.md).

Flows are stored in `<artifact-root>/approach.db` using SQLite WAL mode. On the
first open after upgrading, Approach migrates legacy `<artifact-root>/flows/`
records, leaving that directory unchanged in place and reporting what it did
both on stderr and in `<artifact-root>/FLOW-MIGRATION-NOTICE.txt`. Saved plans
and agent sessions remain file-backed. SQLite schema v3 adds protected Flow
preparation receipts and the `epic_progressions` table; schema v4 adds the
durable progression `done` field; schema v5 adds the compatibility guard for
progression-claim recovery markers; schema v6 adds the protected
per-preparation nonce projection. Versions 0 through 5 upgrade
transactionally; the v3→v4 migration rewrites only progression JSON records,
conservatively setting historical rows to `done:false`, and never rewrites
existing Flow JSON blobs.

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
